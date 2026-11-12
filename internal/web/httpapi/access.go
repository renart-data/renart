package httpapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	webapi "renart/internal/web/api"
)

// AccessOptions configures SameOriginGuardWithOptions.
type AccessOptions struct {
	// SessionToken is the per-process discovery token the local CLI reads
	// from .renart/server.json.
	SessionToken string
	// AccessToken turns access auth on when set: every request must then
	// present it (or the session token) as a header, or carry the sign-in
	// cookie derived from it. Empty keeps the loopback behaviour unchanged.
	AccessToken string
	// CookieName names the sign-in cookie. It should include the listener
	// port so two servers on one host keep separate cookies.
	CookieName string
}

const (
	defaultAccessCookieName = "renart_access"
	accessCookieLabel       = "renart-access-cookie-v1"
	accessCookieMaxAge      = 30 * 24 * 60 * 60
	authPath                = "/auth"
	authLogoutPath          = "/auth/logout"
	authFormMaxBytes        = 8 << 10
)

// accessAuth is the access-token half of the guard. It exists only when
// access auth is on.
type accessAuth struct {
	token       string
	cookieName  string
	cookieValue string
}

func newAccessAuth(opts AccessOptions) *accessAuth {
	if opts.AccessToken == "" {
		return nil
	}
	name := strings.TrimSpace(opts.CookieName)
	if name == "" {
		name = defaultAccessCookieName
	}
	return &accessAuth{
		token:       opts.AccessToken,
		cookieName:  name,
		cookieValue: accessCookieValue(opts.AccessToken),
	}
}

// accessCookieValue derives the cookie from the token so the raw token
// never sits in the browser's cookie store.
func accessCookieValue(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(accessCookieLabel))
	return hex.EncodeToString(mac.Sum(nil))
}

func tokenMatches(presented, want string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(want)) == 1
}

func (a *accessAuth) cookieValid(r *http.Request) bool {
	cookie, err := r.Cookie(a.cookieName)
	if err != nil {
		return false
	}
	return tokenMatches(cookie.Value, a.cookieValue)
}

func requestIsTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

func (a *accessAuth) setCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     a.cookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   requestIsTLS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// handleAuthRoute serves the sign-in endpoints and reports whether it did.
func (a *accessAuth) handleAuthRoute(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case authPath:
		switch r.Method {
		case http.MethodGet, http.MethodHead:
			a.renderSignIn(w, http.StatusOK, signInPage{
				SignedIn: a.cookieValid(r),
				Next:     safeNextPath(r.URL.Query().Get("next")),
			})
		case http.MethodPost:
			a.handleSignIn(w, r)
		default:
			w.Header().Set("Allow", "GET, HEAD, POST")
			webapi.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
		return true
	case authLogoutPath:
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			webapi.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return true
		}
		if !authFormOriginAllowed(r) {
			webapi.WriteError(w, http.StatusForbidden, "cross_origin_rejected", "cross-origin request rejected")
			return true
		}
		a.setCookie(w, r, "", -1)
		http.Redirect(w, r, authPath, http.StatusSeeOther)
		return true
	}
	return false
}

func (a *accessAuth) handleSignIn(w http.ResponseWriter, r *http.Request) {
	if !authFormOriginAllowed(r) {
		webapi.WriteError(w, http.StatusForbidden, "cross_origin_rejected", "cross-origin request rejected")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, authFormMaxBytes)
	if err := r.ParseForm(); err != nil {
		a.renderSignIn(w, http.StatusBadRequest, signInPage{Next: "/", Error: "The sign-in form could not be read."})
		return
	}
	next := safeNextPath(r.PostForm.Get("next"))
	if !tokenMatches(strings.TrimSpace(r.PostForm.Get("token")), a.token) {
		a.renderSignIn(w, http.StatusUnauthorized, signInPage{Next: next, Error: "That token is not right."})
		return
	}
	a.setCookie(w, r, a.cookieValue, accessCookieMaxAge)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// authFormOriginAllowed keeps other sites from posting the sign-in forms
// (login and logout CSRF). Forms submitted without an Origin header pass.
func authFormOriginAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || originAllowed(r, origin)
}

// rejectUnauthenticated answers a request that carried no credentials.
func (a *accessAuth) rejectUnauthenticated(w http.ResponseWriter, r *http.Request) {
	isRead := r.Method == http.MethodGet || r.Method == http.MethodHead
	if isRead && r.URL.Path == "/api/health" {
		// Liveness only: version, workspace root and project id stay
		// behind the token.
		webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		return
	}
	if isRead && !strings.HasPrefix(r.URL.Path, "/api/") && strings.Contains(r.Header.Get("Accept"), "text/html") {
		http.Redirect(w, r, authPath+"?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="renart"`)
	webapi.WriteError(w, http.StatusUnauthorized, "authentication_required", "authentication required")
}

// safeNextPath accepts only a local path, so the sign-in redirect cannot be
// pointed at another site.
func safeNextPath(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsRune(next, '\\') {
		return "/"
	}
	for _, c := range next {
		// Browsers drop tabs and newlines from URLs, which would turn
		// "/\t/evil.example" into "//evil.example".
		if c < 0x20 || c == 0x7f {
			return "/"
		}
	}
	return next
}

type signInPage struct {
	SignedIn bool
	Next     string
	Error    string
}

// signInStyle is the page's only style. html/template copies it verbatim
// (it has no CSS comments to strip), so its hash in the CSP matches.
const signInStyle = `
:root { color-scheme: light dark; --bg: #f6f5f2; --panel: #ffffff; --text: #1d1c1a; --muted: #5f5d58; --border: #d9d6cf; --accent: #2f5d50; --accent-text: #ffffff; --error: #a3331f; }
@media (prefers-color-scheme: dark) { :root { --bg: #161615; --panel: #201f1d; --text: #ecebe7; --muted: #a7a49d; --border: #3a3834; --accent: #7fb8a4; --accent-text: #10201b; --error: #f08b76; } }
* { box-sizing: border-box; }
body { margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center; padding: 16px; background: var(--bg); color: var(--text); font: 16px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; }
main { width: 100%; max-width: 360px; background: var(--panel); border: 1px solid var(--border); border-radius: 10px; padding: 24px; }
h1 { margin: 0 0 4px; font-size: 22px; }
p { margin: 0 0 16px; color: var(--muted); }
label { display: block; margin-bottom: 6px; font-weight: 600; }
input { width: 100%; padding: 10px 12px; font: inherit; color: var(--text); background: var(--bg); border: 1px solid var(--border); border-radius: 6px; }
button { width: 100%; margin-top: 16px; padding: 10px 12px; font: inherit; font-weight: 600; color: var(--accent-text); background: var(--accent); border: 0; border-radius: 6px; cursor: pointer; }
a { color: var(--accent); }
.error { margin: 12px 0 0; color: var(--error); }
`

var signInTemplate = template.Must(template.New("signin").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>Sign in to Renart</title>
<style>` + signInStyle + `</style>
</head>
<body>
<main>
<h1>Renart</h1>
{{if .SignedIn}}
<p>You are signed in.</p>
<p><a href="{{.Next}}">Open Renart</a></p>
<form method="post" action="/auth/logout">
<button type="submit">Sign out</button>
</form>
{{else}}
<p>This server needs an access token.</p>
<form method="post" action="/auth">
<input type="hidden" name="next" value="{{.Next}}">
<label for="token">Access token</label>
<input id="token" name="token" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" required autofocus>
{{if .Error}}<p class="error" role="alert">{{.Error}}</p>{{end}}
<button type="submit">Sign in</button>
</form>
{{end}}
</main>
</body>
</html>
`))

var signInStyleHash = func() string {
	sum := sha256.Sum256([]byte(signInStyle))
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}()

func (a *accessAuth) renderSignIn(w http.ResponseWriter, status int, page signInPage) {
	var body bytes.Buffer
	if err := signInTemplate.Execute(&body, page); err != nil {
		webapi.WriteInternalError(w, "internal_error", "internal server error")
		return
	}
	header := w.Header()
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	// same-origin, not no-referrer: under no-referrer a browser posts the
	// form with "Origin: null", which the sign-in origin check refuses.
	header.Set("Referrer-Policy", "same-origin")
	header.Set("X-Frame-Options", "DENY")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy",
		"default-src 'none'; style-src '"+signInStyleHash+"'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}
