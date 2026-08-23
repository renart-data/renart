package httpapi

import (
	"crypto/subtle"
	"net"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"go.uber.org/zap"
	webapi "renart/internal/web/api"
	webscheduler "renart/internal/web/scheduler"
	"renart/internal/web/service"
)

func isLoopbackHost(hostname string) bool {
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

// statusWriter records the response status code while preserving the
// http.Flusher behavior streaming handlers (SSE) depend on.
type statusWriter struct {
	http.ResponseWriter
	status        int
	responseBytes int64
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	written, err := w.ResponseWriter.Write(b)
	w.responseBytes += int64(written)
	return written, err
}

func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// RequestLogger logs API requests after completion. Static asset requests
// are skipped to keep the output focused on API traffic.
func RequestLogger(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/api/") {
				next.ServeHTTP(w, r)
				return
			}

			sw := &statusWriter{ResponseWriter: w}
			start := time.Now()
			next.ServeHTTP(sw, r)

			status := sw.status
			if status == 0 {
				status = http.StatusOK
			}
			logger.Info("http",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.Int("status", status),
				zap.Int64("response_bytes", sw.responseBytes),
				zap.Duration("duration", time.Since(start)),
			)
		})
	}
}

// Recoverer converts handler panics into logged 500 responses instead of
// killing the connection silently.
func Recoverer(logger *zap.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					logger.Error("panic in handler",
						zap.Any("panic", rec),
						zap.String("method", r.Method),
						zap.String("path", r.URL.Path),
						zap.ByteString("stack", debug.Stack()),
					)
					webapi.WriteInternalError(w, "internal_error", "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// SameOriginGuard rejects state-changing cross-origin browser requests.
// Renart executes SQL/Python and writes workspace files, so a malicious web
// page must not be able to fire no-preflight POSTs at the local server.
// Browsers attach an Origin header to cross-origin requests; non-browser
// clients (curl, CLI integrations) send none and remain unaffected.
func SameOriginGuard() func(http.Handler) http.Handler {
	return SameOriginGuardWithToken("")
}

// SameOriginGuardWithToken is SameOriginGuard plus an explicit session-token
// bypass: requests carrying the server's token (Authorization: Bearer or
// X-Renart-Token) are trusted regardless of Origin. The CLI sends the token
// from .renart/server.json so its trust no longer rests on the accident of
// having no Origin header (plans/cli-v1.md §2.3). A presented-but-wrong
// token is rejected outright.
func SameOriginGuardWithToken(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if presented := requestSessionToken(r); presented != "" {
				if token != "" && subtle.ConstantTimeCompare([]byte(presented), []byte(token)) == 1 {
					// A valid discovery token is minted by this server and supplied
					// to the local CLI. It is therefore a server-authenticated origin,
					// unlike a client-provided trigger field.
					ctx := service.WithExecutionOrigin(r.Context(), webscheduler.RunTriggerCLI)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				webapi.WriteError(w, http.StatusForbidden, "invalid_session_token", "invalid session token")
				return
			}

			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}

			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			parsed, err := url.Parse(origin)
			if err != nil || parsed.Host == "" {
				webapi.WriteError(w, http.StatusForbidden, "cross_origin_rejected", "cross-origin request rejected")
				return
			}

			// Loopback origins on any port are trusted so the Vite dev
			// server (which proxies /api with a rewritten Host header)
			// keeps working; web pages can never run on a loopback origin
			// unless something local already serves them.
			if strings.EqualFold(parsed.Host, r.Host) || isLoopbackHost(parsed.Hostname()) {
				ctx := r.Context()
				if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Renart-UI-Execution-Origin")), "manual") {
					// Only a browser request that already passed the same-origin check
					// may identify itself as a user-triggered UI execution. Header-only
					// API clients remain API-originated, and discovery-token calls remain CLI.
					ctx = service.WithExecutionOrigin(ctx, webscheduler.RunTriggerManual)
				}
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			webapi.WriteError(w, http.StatusForbidden, "cross_origin_rejected", "cross-origin request rejected")
		})
	}
}

// requestSessionToken extracts a presented session token from the request:
// Authorization: Bearer <token> or the X-Renart-Token header.
func requestSessionToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if rest, ok := strings.CutPrefix(auth, "Bearer "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return strings.TrimSpace(r.Header.Get("X-Renart-Token"))
}
