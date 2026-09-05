package httpapi

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"renart/internal/web/databrowser"
)

func TestDataAddressHTTPBoundary(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "data.csv"), []byte("id\n1\n"), 0600))
	service := databrowser.New(databrowser.Dependencies{WorkspaceRoot: root, ListConnections: func(context.Context, string) (string, []databrowser.ConnectionConfig, int64, error) {
		return "dev", nil, 1, nil
	}})
	router := chi.NewRouter()
	RegisterDataBrowserRoutes(router, &DataBrowserAPI{Service: service})
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"environment":"dev","address":{"source_kind":"local_files","path":"data.csv"}}`, 200},
		{`{"environment":"dev","address":{"source_kind":"local_files","path":"../secret.csv"}}`, 400},
		{`{"environment":"dev","address":{"source_kind":"local_files","path":"data.csv","sql":"delete"}}`, 400},
		{`{"environment":"dev","address":{"source_kind":"local_files","path":"` + strings.Repeat("a", 17000) + `"}}`, 400},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("POST", "/api/data-browser/resolve", strings.NewReader(test.body)))
		require.Equal(t, test.status, response.Code, response.Body.String())
	}
}
