package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/stretchr/testify/require"
	"renart/internal/web/sqlnamespace"
)

type catalogImportConnection struct{ queries []string }

func (c *catalogImportConnection) Select(_ context.Context, q *query.Query) ([][]any, error) {
	c.queries = append(c.queries, q.Query)
	return [][]any{{"orders"}}, nil
}
func (c *catalogImportConnection) SelectWithSchema(_ context.Context, q *query.Query) (*query.QueryResult, error) {
	c.queries = append(c.queries, q.Query)
	column := "lake_id"
	if strings.Contains(q.Query, "warehouse") {
		column = "warehouse_id"
	}
	return &query.QueryResult{Columns: []string{column}, ColumnTypes: []string{"INTEGER"}}, nil
}

func TestCatalogSourceImportKeepsCatalogInFileNameAndColumns(t *testing.T) {
	root := t.TempDir()
	pipelineRoot := filepath.Join(root, "analytics")
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(pipelineRoot, "assets"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte("name: analytics\nschedule: daily\nstart_date: '2024-01-01'\n"), 0600))
	conn := &catalogImportConnection{}
	executor := newCompatDirectExecutor(root, "")
	executor.newConnectionManager = func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
		return &stubConnectionManager{conn: conn, connectionType: "starrocks"}, nil
	}
	for _, catalog := range []string{"lake", "warehouse"} {
		name := catalog + ".sales.orders"
		request := ImportDatabaseRequest{PipelinePath: "analytics", ConnectionName: "sr", Environment: "dev", PreferredAssetName: name, Tables: []string{name}, PreviewOnly: true, RejectExisting: true}
		output, err := executor.ImportDatabase(t.Context(), request)
		require.NoError(t, err)
		var preview directImportDatabaseResponse
		require.NoError(t, json.Unmarshal(output, &preview))
		require.Len(t, preview.Assets, 1)
		require.Equal(t, name, preview.Assets[0].Name)
		require.Equal(t, catalog+"_id", preview.Assets[0].Columns[0].Name)
		path := filepath.Join(root, preview.Assets[0].Path)
		_, err = os.Stat(path)
		require.ErrorIs(t, err, os.ErrNotExist)
		request.PreviewOnly = false
		_, err = executor.ImportDatabase(t.Context(), request)
		require.NoError(t, err)
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Contains(t, string(content), "name: "+name)
		issues, err := ensureCatalogSourceName(t.Context(), nil, &pipeline.Asset{Name: name, Type: "starrocks.source"})
		require.NoError(t, err)
		require.Empty(t, issues)
	}
}

func TestCatalogDiscoverySeedsQualifiedLSPObservations(t *testing.T) {
	conn := &catalogImportConnection{}
	observer := &recordingRemoteCatalogObserver{}
	svc := NewSQLService(SQLDependencies{NewConnectionManager: func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
		return &stubConnectionManager{conn: conn, connectionType: "starrocks"}, nil
	}})
	svc.SetRemoteCatalogObserver(observer)
	entries, err := svc.NamespaceChildren(t.Context(), "sr", sqlnamespace.Scope{Catalog: "lake", Database: "sales"}, "dev")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, "lake.sales.orders", observer.tables[0].Name)
	require.Equal(t, "lake", observer.tables[0].CatalogName)
}

func TestCatalogObservationsDoNotFoldDistinctCatalogNames(t *testing.T) {
	cache := NewRemoteCatalogCache(RemoteCatalogDependencies{})
	scope := RemoteCatalogScope{Connection: "sr", Environment: "dev"}
	cache.ObserveTables(scope, []SQLDiscoveryTableItem{{Name: "Lake.sales.orders", CatalogName: "Lake", ShortName: "orders", SchemaName: "sales"}, {Name: "lake.sales.orders", CatalogName: "lake", ShortName: "orders", SchemaName: "sales"}})
	cache.ObserveColumns(scope, "Lake.sales.orders", []SQLColumn{{Name: "upper", Type: "INT"}})
	cache.ObserveColumns(scope, "lake.sales.orders", []SQLColumn{{Name: "lower", Type: "DOUBLE"}})
	snapshot := cache.Snapshot(scope)
	require.Len(t, snapshot.Relations, 2)
	for _, r := range snapshot.Relations {
		require.Len(t, r.Columns, 1)
		if r.CatalogName == "Lake" {
			require.Equal(t, "upper", r.Columns[0].Name)
		} else {
			require.Equal(t, "lower", r.Columns[0].Name)
		}
	}
	require.Equal(t, -1, remoteCatalogRelationIndex(snapshot.Relations, "sales.orders"))
}
