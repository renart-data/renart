package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"renart/internal/web/dataaddress"
	"renart/internal/web/databrowser"
	"renart/internal/web/service"
)

type recordingBrowserSource struct {
	calls   int
	preview bool
	object  databrowser.Object
	events  int
}

func (s *recordingBrowserSource) ImportDataBrowserSource(_ context.Context, _ string, object databrowser.Object, preview, _ bool) (service.ExternalRelationImportResult, *service.APIError) {
	s.calls++
	s.preview = preview
	s.object = object
	return service.ExternalRelationImportResult{Status: "ok", Preview: preview, PipelinePath: "analytics"}, nil
}
func (s *recordingBrowserSource) WorkspaceChanged(context.Context, string, string) { s.events++ }

func TestDataBrowserSourceBoundaryRevalidatesScopeBeforeEveryWrite(t *testing.T) {
	var revision int64 = 1
	browser := databrowser.New(databrowser.Dependencies{
		ListConnections: func(_ context.Context, env string) (string, []databrowser.ConnectionConfig, int64, error) {
			return env, []databrowser.ConnectionConfig{{Name: "warehouse", Type: "duckdb", Queryable: true}}, revision, nil
		},
		ListTables: func(context.Context, string, string, string) ([]databrowser.Table, error) {
			return []databrowser.Table{{Name: "main.orders", ShortName: "orders", SchemaName: "main"}}, nil
		},
	})
	resolved, apiErr := browser.Resolve(context.Background(), databrowser.ResolveRequest{Environment: "dev", Address: dataaddress.Address{SourceKind: "warehouse", Connection: "warehouse", ConnectionType: "duckdb", Schema: "main", Name: "orders"}})
	require.Nil(t, apiErr)
	sources := &recordingBrowserSource{}
	router := chi.NewRouter()
	RegisterDataBrowserRoutes(router, &DataBrowserAPI{Service: browser, Sources: sources, Publisher: sources})
	request := func(path, body string) int {
		r := httptest.NewRecorder()
		router.ServeHTTP(r, httptest.NewRequest("POST", "/api/pipelines/analytics/data-browser/sources"+path, strings.NewReader(body)))
		return r.Code
	}
	body, err := json.Marshal(service.DataBrowserSourceRequest{ObjectID: resolved.Object.ID, Environment: "dev"})
	require.NoError(t, err)
	require.Equal(t, 200, request("/preview", string(body)))
	require.True(t, sources.preview)
	require.Equal(t, 0, sources.events)
	require.Equal(t, "main.orders", sources.object.ReferenceText)
	require.Equal(t, 201, request("", string(body)))
	require.False(t, sources.preview)
	require.Equal(t, 1, sources.events)
	before := sources.calls
	require.Equal(t, 409, request("", strings.Replace(string(body), `"dev"`, `"prod"`, 1)))
	require.Equal(t, 400, request("", `{"object_id":"bad","environment":"dev","sql":"delete"}`))
	require.Equal(t, 400, request("", `{"object_id":"bad"}`))
	revision++
	require.Equal(t, 409, request("", string(body)))
	require.Equal(t, before, sources.calls)
	require.Equal(t, 1, sources.events)
}
