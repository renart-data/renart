package httpapi

import (
	"context"
	"net/http/httptest"
	"net/url"
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

func TestDataBrowserPrefixHTTPBoundary(t *testing.T) {
	var requested []string
	var filters []string
	service := databrowser.New(databrowser.Dependencies{
		ListConnections: func(context.Context, string) (string, []databrowser.ConnectionConfig, int64, error) {
			return "dev", []databrowser.ConnectionConfig{{Name: "lake", Type: "s3", Storage: true}}, 1, nil
		},
		ListStorage: func(_ context.Context, _ string, query databrowser.StorageQuery, _ string) (databrowser.StorageListing, error) {
			requested = append(requested, query.Prefix)
			filters = append(filters, query.NamePrefix)
			return databrowser.StorageListing{}, nil
		},
	})
	connections, apiErr := service.Connections(t.Context(), "dev")
	require.Nil(t, apiErr)
	router := chi.NewRouter()
	RegisterDataBrowserRoutes(router, &DataBrowserAPI{Service: service})
	for _, test := range []struct {
		path   string
		status int
	}{
		{"events/Frühstück/", 200},
		{"../outside/", 400},
		{"events/\n/", 400},
		{"/absolute/", 400},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/api/data-browser/connections/"+connections.Connections[0].ID+"/prefix?environment=dev&path="+url.QueryEscape(test.path), nil))
		require.Equal(t, test.status, response.Code, response.Body.String())
	}
	require.Equal(t, []string{"events/Frühstück/"}, requested)
	for _, name := range []string{"day=2026-09", " space", "a/b", "../outside", "a|b", "a\n"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/api/data-browser/connections/"+connections.Connections[0].ID+"/prefix?environment=dev&path=events%2F&name_prefix="+url.QueryEscape(name), nil))
		status := 400
		if name == "day=2026-09" || name == " space" {
			status = 200
		}
		require.Equal(t, status, response.Code, response.Body.String())
	}
	require.Equal(t, []string{"", "day=2026-09", " space"}, filters, "literal name filters must not be trimmed or interpreted as paths")
}
