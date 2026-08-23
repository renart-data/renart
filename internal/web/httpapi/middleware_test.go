package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	webscheduler "renart/internal/web/scheduler"
	"renart/internal/web/service"
)

func TestRequestLoggerRecordsResponseSize(t *testing.T) {
	t.Parallel()

	core, observed := observer.New(zap.InfoLevel)
	handler := RequestLogger(zap.New(core))(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte("hello"))
	}))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/example", nil))

	require.Equal(t, http.StatusCreated, response.Code)
	entries := observed.FilterMessage("http").All()
	require.Len(t, entries, 1)
	fields := entries[0].ContextMap()
	assert.EqualValues(t, 5, fields["response_bytes"])
}

func TestSessionTokenAuthenticatesCLIRunOrigin(t *testing.T) {
	t.Parallel()
	var origin webscheduler.RunTrigger
	handler := SameOriginGuardWithToken("server-secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin = service.ExecutionOrigin(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/run", nil)
	request.Header.Set("X-Renart-Token", "server-secret")
	request.Header.Set("X-Renart-UI-Execution-Origin", "manual")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, webscheduler.RunTriggerCLI, origin)
}

func TestSameOriginBrowserCanMarkManualRunOrigin(t *testing.T) {
	t.Parallel()
	var origin webscheduler.RunTrigger
	handler := SameOriginGuardWithToken("server-secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin = service.ExecutionOrigin(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/run", nil)
	request.Header.Set("Origin", "http://127.0.0.1:5173")
	request.Header.Set("X-Renart-UI-Execution-Origin", "manual")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, webscheduler.RunTriggerManual, origin)
}

func TestHeaderWithoutBrowserOriginCannotMarkManualRunOrigin(t *testing.T) {
	t.Parallel()
	var origin webscheduler.RunTrigger
	handler := SameOriginGuardWithToken("server-secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin = service.ExecutionOrigin(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/run", nil)
	request.Header.Set("X-Renart-UI-Execution-Origin", "manual")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, webscheduler.RunTriggerAPI, origin)
}

func TestOrdinaryHTTPRunOriginRemainsAPI(t *testing.T) {
	t.Parallel()
	var origin webscheduler.RunTrigger
	handler := SameOriginGuardWithToken("server-secret")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin = service.ExecutionOrigin(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/run", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, webscheduler.RunTriggerAPI, origin)
}
