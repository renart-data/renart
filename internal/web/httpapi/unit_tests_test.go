package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"renart/internal/web/service"
)

type unitTestHTTPStub struct {
	AssetHandlers
	calls int
}

func (s *unitTestHTTPStub) UnitTestContext(_ context.Context, id string) (service.SQLUnitTestContext, *APIError) {
	return service.SQLUnitTestContext{Status: "ok", Revision: id}, nil
}
func (s *unitTestHTTPStub) RunUnitTests(_ context.Context, id string, request service.SQLUnitTestRunRequest) (service.SQLUnitTestRunResponse, *APIError) {
	s.calls++
	if request.Revision != "current" {
		return service.SQLUnitTestRunResponse{}, &APIError{Status: 409, Code: "unit_tests_conflict", Message: "Reload tests"}
	}
	return service.SQLUnitTestRunResponse{Status: "ok", Results: []service.SQLUnitTestResult{{Name: id, Status: "passed"}}}, nil
}
func TestUnitTestHTTPRoutesValidateBodiesAndPreserveConflicts(t *testing.T) {
	stub := &unitTestHTTPStub{}
	handlers := &AssetsAPI{Service: stub}
	router := chi.NewRouter()
	router.Get("/api/assets/{assetID}/unit-tests", handlers.HandleUnitTestContext)
	router.Post("/api/assets/{assetID}/unit-tests/run", handlers.HandleRunUnitTests)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/api/assets/a/unit-tests", "", 200},
		{"POST", "/api/assets/a/unit-tests/run", `{"revision":"current"}`, 200},
		{"POST", "/api/assets/a/unit-tests/run", `{"revision":"old"}`, 409},
		{"POST", "/api/assets/a/unit-tests/run", `[]`, 400},
		{"POST", "/api/assets/a/unit-tests/run", `{"revision":"` + strings.Repeat("x", 4096) + `"}`, 400},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		require.Equal(t, tc.status, response.Code, response.Body.String())
	}
	require.Equal(t, 2, stub.calls)
}
