package httpapi

import (
	"context"
	"encoding/json"
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

func TestDataBrowserPatternHTTPBoundary(t *testing.T) {
	var revision int64 = 1
	calls := 0
	service := databrowser.New(databrowser.Dependencies{
		ListConnections: func(_ context.Context, env string) (string, []databrowser.ConnectionConfig, int64, error) {
			return env, []databrowser.ConnectionConfig{{Name: "lake", Type: "s3", Storage: true}}, revision, nil
		},
		ListStorage: func(context.Context, string, databrowser.StorageQuery, string) (databrowser.StorageListing, error) {
			t.Fatal("Resolving a pattern must not list objects")
			return databrowser.StorageListing{}, nil
		},
		StoragePatternReference: func(_ context.Context, connection, pattern, environment string) (string, error) {
			calls++
			require.Equal(t, "lake", connection)
			require.Equal(t, "dev", environment)
			return "s3://bucket/root/" + pattern, nil
		},
	})
	connections, apiErr := service.Connections(t.Context(), "dev")
	require.Nil(t, apiErr)
	require.Len(t, connections.Connections, 1)
	router := chi.NewRouter()
	RegisterDataBrowserRoutes(router, &DataBrowserAPI{Service: service})
	request := func(pattern, environment string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/api/data-browser/connections/"+connections.Connections[0].ID+"/pattern?environment="+environment+"&pattern="+url.QueryEscape(pattern), nil))
		return response
	}
	response := request("orders/part-?.csv", "dev")
	require.Equal(t, 200, response.Code, response.Body.String())
	var result databrowser.ObjectResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, "s3://bucket/root/orders/part-?.csv", result.Object.ReferenceText)
	require.True(t, result.Object.Capabilities.LoadSource)
	require.False(t, result.Object.Capabilities.LoadDestination)
	for _, invalid := range []string{"", "../*.csv", "s3://another-bucket/*", "literal.csv"} {
		require.Equal(t, 400, request(invalid, "dev").Code, invalid)
	}
	require.Equal(t, 409, request("orders/*", "prod").Code)
	revision++
	require.Equal(t, 409, request("orders/*", "dev").Code)
	_, apiErr = service.Object(t.Context(), result.Object.ID, "dev")
	require.NotNil(t, apiErr)
	require.Equal(t, "data_browser_revision_stale", apiErr.Code)
	require.Equal(t, 1, calls, "Invalid or outdated selectors must not resolve credentials")
}

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
	for _, test := range []struct {
		pattern string
		status  int
	}{
		{"events/day=2026-??/*.csv", 200},
		{"../*.csv", 400},
		{"events/**/*.csv", 400},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("GET", "/api/data-browser/connections/"+connections.Connections[0].ID+"/prefix?environment=dev&pattern="+url.QueryEscape(test.pattern), nil))
		require.Equal(t, test.status, response.Code, response.Body.String())
	}
	require.Equal(t, []string{"", "day=2026-09", " space", "day=2026-"}, filters, "wildcards must never reach provider queries")
}
