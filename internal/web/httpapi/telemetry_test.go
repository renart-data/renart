package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"renart/internal/web/telemetry"
)

func TestUsageSettingsHTTPBoundary(t *testing.T) {
	client := telemetry.New(telemetry.Options{Version: "dev", ConfigPath: filepath.Join(t.TempDir(), "telemetry.json"), Getenv: func(string) string { return "" }})
	defer client.Close()
	router := chi.NewRouter()
	router.Use(SameOriginGuard())
	RegisterTelemetryRoutes(router, &TelemetryAPI{Client: client})
	for _, tc := range []struct {
		method, path, body, origin string
		status                     int
	}{
		{"GET", "/api/telemetry", "", "", 200},
		{"GET", "/api/telemetry/sample", "", "", 200},
		{"PUT", "/api/telemetry", `{"enabled":false}`, "http://127.0.0.1", 200},
		{"PUT", "/api/telemetry", `{"sql":"select secret"}`, "http://127.0.0.1", 400},
		{"PUT", "/api/telemetry", `{"endpoint":"https://elsewhere.example"}`, "http://127.0.0.1", 400},
		{"PUT", "/api/telemetry", `{}`, "http://127.0.0.1", 400},
		{"PUT", "/api/telemetry", `null`, "http://127.0.0.1", 400},
		{"PUT", "/api/telemetry", `{"enabled":true} {}`, "http://127.0.0.1", 400},
		{"PUT", "/api/telemetry", `{"enabled":true}`, "https://untrusted.example", 403},
	} {
		request := httptest.NewRequest(tc.method, "http://127.0.0.1"+tc.path, strings.NewReader(tc.body))
		if tc.origin != "" {
			request.Header.Set("Origin", tc.origin)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, tc.status, response.Code, "%s %s %s: %s", tc.method, tc.path, tc.body, response.Body.String())
		if response.Code == http.StatusOK {
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		}
	}
	require.False(t, client.Status().Enabled)
}
