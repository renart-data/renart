package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeDuckDBDriver struct {
	ready     bool
	ensureErr error
	// The context's error at the time of each Ensure call.
	ensureCtxErrs []error
}

func (f *fakeDuckDBDriver) Ready(context.Context) bool { return f.ready }

func (f *fakeDuckDBDriver) Ensure(ctx context.Context) error {
	f.ensureCtxErrs = append(f.ensureCtxErrs, ctx.Err())
	if f.ensureErr == nil {
		f.ready = true
	}
	return f.ensureErr
}

func TestDuckDBDriverRoutes(t *testing.T) {
	driver := &fakeDuckDBDriver{}
	router := chi.NewRouter()
	RegisterProjectRoutes(router, &ProjectsAPI{DuckDBDriver: driver})

	serve := func(method string) (*httptest.ResponseRecorder, DuckDBDriverResponse) {
		request := httptest.NewRequest(method, "http://127.0.0.1/api/projects/duckdb-driver", nil)
		ctx, cancel := context.WithCancel(request.Context())
		// The install must outlive a client that goes away mid-download.
		cancel()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request.WithContext(ctx))
		var body DuckDBDriverResponse
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		return response, body
	}

	response, body := serve(http.MethodGet)
	require.Equal(t, http.StatusOK, response.Code)
	assert.False(t, body.Ready)

	response, body = serve(http.MethodPost)
	require.Equal(t, http.StatusOK, response.Code)
	assert.True(t, body.Ready)
	require.Len(t, driver.ensureCtxErrs, 1)
	assert.NoError(t, driver.ensureCtxErrs[0])

	response, body = serve(http.MethodGet)
	require.Equal(t, http.StatusOK, response.Code)
	assert.True(t, body.Ready)

	driver.ensureErr = errors.New("registry unreachable")
	response, _ = serve(http.MethodPost)
	require.Equal(t, http.StatusInternalServerError, response.Code)
	assert.Contains(t, response.Body.String(), "registry unreachable")
	assert.Contains(t, response.Body.String(), "restart Renart")
}
