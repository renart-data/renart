package httpapi

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	webscheduler "renart/internal/web/scheduler"
	"renart/internal/web/service"
)

const (
	testSessionToken = "server-secret"
	testAccessToken  = "fake-access-token-for-tests"
	testCookieName   = "renart_access_8080"
)

// accessGuardHandler wraps a handler that records the execution origin it
// was reached with.
func accessGuardHandler(t *testing.T, opts AccessOptions) (http.Handler, *webscheduler.RunTrigger, *bool) {
	t.Helper()
	var origin webscheduler.RunTrigger
	reached := false
	handler := SameOriginGuardWithOptions(opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		origin = service.ExecutionOrigin(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	return handler, &origin, &reached
}

func accessOn() AccessOptions {
	return AccessOptions{SessionToken: testSessionToken, AccessToken: testAccessToken, CookieName: testCookieName}
}

func validAccessCookie() *http.Cookie {
	return &http.Cookie{Name: testCookieName, Value: accessCookieValue(testAccessToken)}
}

func TestAccessAuthOffKeepsGuardBehaviour(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		method string
		target string
		header map[string]string
		status int
	}{
		{name: "get without credentials", method: http.MethodGet, target: "/api/workspace", status: http.StatusNoContent},
		{name: "html navigation", method: http.MethodGet, target: "/", header: map[string]string{"Accept": "text/html"}, status: http.StatusNoContent},
		{name: "auth path falls through", method: http.MethodGet, target: "/auth", status: http.StatusNoContent},
		{name: "post without origin", method: http.MethodPost, target: "/api/run", status: http.StatusNoContent},
		{name: "post with loopback origin", method: http.MethodPost, target: "http://127.0.0.1:8080/api/run", header: map[string]string{"Origin": "http://127.0.0.1:5173"}, status: http.StatusNoContent},
		{name: "cross-origin post", method: http.MethodPost, target: "http://127.0.0.1:8080/api/run", header: map[string]string{"Origin": "https://evil.example"}, status: http.StatusForbidden},
		{name: "wrong token", method: http.MethodGet, target: "/api/workspace", header: map[string]string{"X-Renart-Token": "nope"}, status: http.StatusForbidden},
		{name: "access-like token is not special", method: http.MethodGet, target: "/api/workspace", header: map[string]string{"Authorization": "Bearer " + testAccessToken}, status: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, guard := range []func(http.Handler) http.Handler{
				SameOriginGuardWithToken(testSessionToken),
				SameOriginGuardWithOptions(AccessOptions{SessionToken: testSessionToken}),
			} {
				handler := guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				}))
				request := httptest.NewRequest(tc.method, tc.target, nil)
				for key, value := range tc.header {
					request.Header.Set(key, value)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				assert.Equal(t, tc.status, response.Code)
				assert.Empty(t, response.Header().Values("Set-Cookie"))
			}
		})
	}
}

func TestAccessAuthRejectsRequestsWithoutCredentials(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		method   string
		target   string
		header   map[string]string
		status   int
		location string
	}{
		{name: "api get", method: http.MethodGet, target: "/api/workspace", status: http.StatusUnauthorized},
		{name: "sse", method: http.MethodGet, target: "/api/events", header: map[string]string{"Accept": "text/event-stream"}, status: http.StatusUnauthorized},
		{name: "project mount", method: http.MethodGet, target: "/api/projects/p1/workspace", status: http.StatusUnauthorized},
		{name: "project health", method: http.MethodGet, target: "/api/projects/p1/health", status: http.StatusUnauthorized},
		{name: "api get asking for html", method: http.MethodGet, target: "/api/workspace", header: map[string]string{"Accept": "text/html"}, status: http.StatusUnauthorized},
		{name: "post without origin", method: http.MethodPost, target: "/api/run", status: http.StatusUnauthorized},
		{name: "post with loopback origin", method: http.MethodPost, target: "http://127.0.0.1:8080/api/run", header: map[string]string{"Origin": "http://127.0.0.1:8080"}, status: http.StatusUnauthorized},
		{name: "options", method: http.MethodOptions, target: "/api/run", status: http.StatusUnauthorized},
		{name: "static asset", method: http.MethodGet, target: "/assets/index.js", status: http.StatusUnauthorized},
		{name: "html navigation", method: http.MethodGet, target: "/pipelines/x?tab=runs&a=1", header: map[string]string{"Accept": "text/html,application/xhtml+xml"}, status: http.StatusSeeOther, location: "/auth?next=" + url.QueryEscape("/pipelines/x?tab=runs&a=1")},
		{name: "root navigation", method: http.MethodGet, target: "/", header: map[string]string{"Accept": "text/html"}, status: http.StatusSeeOther, location: "/auth?next=%2F"},
		{name: "tampered cookie", method: http.MethodGet, target: "/api/workspace", header: map[string]string{"Cookie": testCookieName + "=" + strings.Repeat("0", 64)}, status: http.StatusUnauthorized},
		{name: "raw token as cookie", method: http.MethodGet, target: "/api/workspace", header: map[string]string{"Cookie": testCookieName + "=" + testAccessToken}, status: http.StatusUnauthorized},
		{name: "cookie for another port", method: http.MethodGet, target: "/api/workspace", header: map[string]string{"Cookie": "renart_access_8081=" + accessCookieValue(testAccessToken)}, status: http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handler, _, reached := accessGuardHandler(t, accessOn())
			request := httptest.NewRequest(tc.method, tc.target, nil)
			for key, value := range tc.header {
				request.Header.Set(key, value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			assert.False(t, *reached)
			require.Equal(t, tc.status, response.Code)
			if tc.status == http.StatusUnauthorized {
				assert.Equal(t, `Bearer realm="renart"`, response.Header().Get("WWW-Authenticate"))
				assert.Contains(t, response.Body.String(), `"authentication_required"`)
			}
			if tc.location != "" {
				assert.Equal(t, tc.location, response.Header().Get("Location"))
			}
		})
	}
}

func TestAccessAuthHealthWithoutCredentialsIsStatusOnly(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		handler, _, reached := accessGuardHandler(t, accessOn())
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, "/api/health", nil))

		assert.False(t, *reached, method)
		require.Equal(t, http.StatusOK, response.Code, method)
		if method == http.MethodGet {
			var body map[string]any
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			assert.Equal(t, map[string]any{"status": "ok"}, body)
		}
	}

	// An authenticated caller reaches the real health handler.
	handler, _, reached := accessGuardHandler(t, accessOn())
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	request.Header.Set("Authorization", "Bearer "+testSessionToken)
	handler.ServeHTTP(httptest.NewRecorder(), request)
	assert.True(t, *reached)
}

func TestAccessAuthHeaderTokens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		header string
		value  string
		status int
		origin webscheduler.RunTrigger
	}{
		{name: "access token as bearer", header: "Authorization", value: "Bearer " + testAccessToken, status: http.StatusNoContent, origin: webscheduler.RunTriggerCLI},
		{name: "access token as x-renart-token", header: "X-Renart-Token", value: testAccessToken, status: http.StatusNoContent, origin: webscheduler.RunTriggerCLI},
		{name: "session token as bearer", header: "Authorization", value: "Bearer " + testSessionToken, status: http.StatusNoContent, origin: webscheduler.RunTriggerCLI},
		{name: "session token as x-renart-token", header: "X-Renart-Token", value: testSessionToken, status: http.StatusNoContent, origin: webscheduler.RunTriggerCLI},
		{name: "wrong token", header: "X-Renart-Token", value: "wrong-token-value-here", status: http.StatusForbidden},
		{name: "cookie value as header", header: "X-Renart-Token", value: accessCookieValue(testAccessToken), status: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handler, origin, reached := accessGuardHandler(t, accessOn())
			// A cross-origin POST: header tokens are trusted regardless of Origin.
			request := httptest.NewRequest(http.MethodPost, "http://renart.example:8080/api/run", nil)
			request.Header.Set("Origin", "https://elsewhere.example")
			request.Header.Set("X-Renart-UI-Execution-Origin", "manual")
			request.Header.Set(tc.header, tc.value)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			require.Equal(t, tc.status, response.Code)
			if tc.status == http.StatusForbidden {
				assert.False(t, *reached)
				assert.Contains(t, response.Body.String(), `"invalid_session_token"`)
				return
			}
			assert.Equal(t, tc.origin, *origin)
		})
	}
}

func TestAccessAuthCookieContinuesThroughOriginChecks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		method string
		origin string
		manual bool
		status int
		want   webscheduler.RunTrigger
	}{
		{name: "get", method: http.MethodGet, status: http.StatusNoContent, want: webscheduler.RunTriggerAPI},
		{name: "same-origin post marks manual", method: http.MethodPost, origin: "http://renart.example:8080", manual: true, status: http.StatusNoContent, want: webscheduler.RunTriggerManual},
		{name: "post without origin stays api", method: http.MethodPost, manual: true, status: http.StatusNoContent, want: webscheduler.RunTriggerAPI},
		{name: "cross-origin post", method: http.MethodPost, origin: "https://evil.example", status: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handler, origin, reached := accessGuardHandler(t, accessOn())
			request := httptest.NewRequest(tc.method, "http://renart.example:8080/api/run", nil)
			request.AddCookie(validAccessCookie())
			if tc.origin != "" {
				request.Header.Set("Origin", tc.origin)
			}
			if tc.manual {
				request.Header.Set("X-Renart-UI-Execution-Origin", "manual")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			require.Equal(t, tc.status, response.Code)
			if tc.status == http.StatusForbidden {
				assert.False(t, *reached)
				assert.Contains(t, response.Body.String(), `"cross_origin_rejected"`)
				return
			}
			assert.Equal(t, tc.want, *origin)
		})
	}
}

func postAuthForm(t *testing.T, handler http.Handler, target string, form url.Values, configure func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if configure != nil {
		configure(request)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestAccessAuthSignIn(t *testing.T) {
	t.Parallel()
	handler, _, _ := accessGuardHandler(t, accessOn())

	response := postAuthForm(t, handler, "/auth", url.Values{"token": {"  " + testAccessToken + "\n"}, "next": {"/pipelines?tab=runs"}}, nil)
	require.Equal(t, http.StatusSeeOther, response.Code)
	assert.Equal(t, "/pipelines?tab=runs", response.Header().Get("Location"))
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 1)
	cookie := cookies[0]
	assert.Equal(t, testCookieName, cookie.Name)
	assert.Equal(t, accessCookieValue(testAccessToken), cookie.Value)
	assert.NotContains(t, cookie.Value, testAccessToken)
	assert.Equal(t, "/", cookie.Path)
	assert.Equal(t, 30*24*60*60, cookie.MaxAge)
	assert.True(t, cookie.HttpOnly)
	assert.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	assert.False(t, cookie.Secure)

	// The cookie it set authenticates the next request.
	followUp := httptest.NewRequest(http.MethodGet, "/api/workspace", nil)
	followUp.AddCookie(cookie)
	followUpResponse := httptest.NewRecorder()
	handler.ServeHTTP(followUpResponse, followUp)
	assert.Equal(t, http.StatusNoContent, followUpResponse.Code)
}

func TestAccessAuthSignInCookieIsSecureOverTLS(t *testing.T) {
	t.Parallel()
	handler, _, _ := accessGuardHandler(t, accessOn())
	for name, configure := range map[string]func(*http.Request){
		"tls":           func(r *http.Request) { r.TLS = &tls.ConnectionState{} },
		"forwarded tls": func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") },
	} {
		response := postAuthForm(t, handler, "/auth", url.Values{"token": {testAccessToken}}, configure)
		require.Equal(t, http.StatusSeeOther, response.Code, name)
		cookies := response.Result().Cookies()
		require.Len(t, cookies, 1, name)
		assert.True(t, cookies[0].Secure, name)
	}
}

func TestAccessAuthSignInRejectsWrongToken(t *testing.T) {
	t.Parallel()
	handler, _, _ := accessGuardHandler(t, accessOn())
	for _, token := range []string{"", "wrong-token-value-here", testSessionToken, accessCookieValue(testAccessToken)} {
		response := postAuthForm(t, handler, "/auth", url.Values{"token": {token}, "next": {"/x"}}, nil)
		require.Equal(t, http.StatusUnauthorized, response.Code)
		assert.Empty(t, response.Result().Cookies())
		assert.Contains(t, response.Body.String(), "That token is not right.")
		assert.Contains(t, response.Body.String(), `name="next" value="/x"`)
		assert.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}
}

func TestAccessAuthSignInRejectsCrossOriginForm(t *testing.T) {
	t.Parallel()
	handler, _, _ := accessGuardHandler(t, accessOn())
	response := postAuthForm(t, handler, "http://renart.example:8080/auth", url.Values{"token": {testAccessToken}}, func(r *http.Request) {
		r.Header.Set("Origin", "https://evil.example")
	})
	assert.Equal(t, http.StatusForbidden, response.Code)
	assert.Empty(t, response.Result().Cookies())

	same := postAuthForm(t, handler, "http://renart.example:8080/auth", url.Values{"token": {testAccessToken}}, func(r *http.Request) {
		r.Header.Set("Origin", "http://renart.example:8080")
	})
	assert.Equal(t, http.StatusSeeOther, same.Code)
}

func TestAccessAuthSignInNextMustBeLocal(t *testing.T) {
	t.Parallel()
	handler, _, _ := accessGuardHandler(t, accessOn())
	cases := map[string]string{
		"":                     "/",
		"/":                    "/",
		"/pipelines/a?b=c":     "/pipelines/a?b=c",
		"//evil.example":       "/",
		`/\evil`:               "/",
		"https://evil.example": "/",
		"evil.example":         "/",
		"/\t/evil.example":     "/",
		"/%2F/evil.example":    "/%2F/evil.example",
	}
	for next, want := range cases {
		response := postAuthForm(t, handler, "/auth", url.Values{"token": {testAccessToken}, "next": {next}}, nil)
		require.Equal(t, http.StatusSeeOther, response.Code, next)
		assert.Equal(t, want, response.Header().Get("Location"), next)
	}
}

func TestAccessAuthSignInPage(t *testing.T) {
	t.Parallel()
	handler, _, reached := accessGuardHandler(t, accessOn())

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth?next="+url.QueryEscape(`/a"><script>x</script>`), nil))
	assert.False(t, *reached)
	require.Equal(t, http.StatusOK, response.Code)
	body := response.Body.String()
	assert.Contains(t, body, "This server needs an access token.")
	assert.Contains(t, body, `type="password"`)
	assert.Contains(t, body, `autocomplete="off"`)
	assert.Contains(t, body, ">Sign in</button>")
	assert.NotContains(t, body, "<script>")
	assert.NotContains(t, strings.ToLower(body), "bruin")
	// The style is copied verbatim, so the CSP hash matches it.
	assert.Contains(t, body, "<style>"+signInStyle+"</style>")
	header := response.Header()
	assert.Equal(t, "text/html; charset=utf-8", header.Get("Content-Type"))
	assert.Equal(t, "no-store", header.Get("Cache-Control"))
	assert.Equal(t, "same-origin", header.Get("Referrer-Policy"))
	assert.Equal(t, "DENY", header.Get("X-Frame-Options"))
	csp := header.Get("Content-Security-Policy")
	assert.Contains(t, csp, "default-src 'none'")
	assert.Contains(t, csp, "style-src '"+signInStyleHash+"'")
	assert.Contains(t, csp, "form-action 'self'")
	assert.NotContains(t, csp, "script-src")

	signedIn := httptest.NewRequest(http.MethodGet, "/auth", nil)
	signedIn.AddCookie(validAccessCookie())
	signedInResponse := httptest.NewRecorder()
	handler.ServeHTTP(signedInResponse, signedIn)
	require.Equal(t, http.StatusOK, signedInResponse.Code)
	assert.Contains(t, signedInResponse.Body.String(), "You are signed in.")
	assert.Contains(t, signedInResponse.Body.String(), `action="/auth/logout"`)
	assert.Contains(t, signedInResponse.Body.String(), ">Sign out</button>")
}

func TestAccessAuthLogoutClearsCookie(t *testing.T) {
	t.Parallel()
	handler, _, _ := accessGuardHandler(t, accessOn())
	response := postAuthForm(t, handler, "/auth/logout", url.Values{}, func(r *http.Request) {
		r.AddCookie(validAccessCookie())
	})
	require.Equal(t, http.StatusSeeOther, response.Code)
	assert.Equal(t, "/auth", response.Header().Get("Location"))
	cookies := response.Result().Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, testCookieName, cookies[0].Name)
	assert.Empty(t, cookies[0].Value)
	assert.Less(t, cookies[0].MaxAge, 0)

	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/auth/logout", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, get.Code)
}
