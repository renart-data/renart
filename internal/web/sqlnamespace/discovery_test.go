package sqlnamespace

import (
	"context"
	"errors"
	"testing"

	"github.com/bruin-data/bruin/pkg/query"
	"github.com/stretchr/testify/require"
)

type selectorStub struct {
	rows    [][]any
	queries []string
	err     error
}

func (s *selectorStub) Select(_ context.Context, q *query.Query) ([][]any, error) {
	s.queries = append(s.queries, q.Query)
	return s.rows, s.err
}

func TestCatalogDiscoveryIsLazyAndPreservesIdentity(t *testing.T) {
	for _, engine := range []string{"starrocks", "trino", "doris", "databricks", "duckdb", "motherduck"} {
		t.Run(engine, func(t *testing.T) {
			client := &selectorStub{rows: [][]any{{"lake"}, {"warehouse"}}}
			if engine == "doris" {
				client.rows = [][]any{{int64(1), "lake"}, {int64(2), "warehouse"}}
			}
			p := Provider{Engine: engine, Client: client, DefaultCatalog: "lake"}
			catalogs, err := p.Children(t.Context(), Scope{})
			require.NoError(t, err)
			require.Len(t, client.queries, 1)
			require.Len(t, catalogs, 2)
			require.Equal(t, "catalog", catalogs[0].Kind)
			require.True(t, catalogs[0].Default)
			client.rows = [][]any{{"empty"}, {"sales"}}
			namespaces, err := p.Children(t.Context(), Scope{Catalog: "lake"})
			require.NoError(t, err)
			require.Len(t, namespaces, 2)
			require.Len(t, client.queries, 2)
			for _, catalog := range []string{"lake", "warehouse"} {
				scope := namespaces[1].Scope
				scope.Catalog = catalog
				client.rows = [][]any{{"orders"}}
				if engine == "databricks" {
					client.rows = [][]any{{"sales", "orders", false}}
				}
				tables, err := p.Children(t.Context(), scope)
				require.NoError(t, err)
				require.Len(t, tables, 1)
				require.Equal(t, catalog, tables[0].Scope.Catalog)
				require.Equal(t, catalog+".sales.orders", tables[0].Reference)
			}
			require.Len(t, client.queries, 4)
			for _, sql := range client.queries {
				require.NotContains(t, sql, "USE ")
				require.NotContains(t, sql, "SET CATALOG")
			}
		})
	}
}

func TestStarRocksExplicitCatalogAndEscaping(t *testing.T) {
	client := &selectorStub{rows: [][]any{{"Order.Items"}}}
	p := Provider{Engine: "starrocks", Client: client, DefaultCatalog: "lake"}
	tables, err := p.Children(t.Context(), Scope{Database: "a`b"})
	require.NoError(t, err)
	require.Equal(t, "SHOW TABLES FROM `lake`.`a``b`", client.queries[0])
	require.Equal(t, "lake.`a``b`.`Order.Items`", tables[0].Reference)
	require.Equal(t, "lake", tables[0].Scope.Catalog)
	client.err = errors.New("catalog access denied")
	_, err = p.Children(t.Context(), Scope{Catalog: "private"})
	require.EqualError(t, err, "catalog access denied")
	require.Len(t, client.queries, 2, "denied catalog must not fall back to default")
	_, err = p.Children(t.Context(), Scope{Catalog: "a\x00b"})
	require.Error(t, err)
	require.Len(t, client.queries, 2)
}

func TestDiscoveryRejectsInvalidHierarchyAndUnsupportedEngine(t *testing.T) {
	client := &selectorStub{}
	for _, p := range []Provider{{Engine: "starrocks", Client: client}, {Engine: "postgres", Client: client}} {
		_, err := p.Children(t.Context(), Scope{Schema: "public"})
		require.Error(t, err)
	}
	require.Empty(t, client.queries)
}

func TestLegacyCatalogDefaultsUseConnectionContextOnly(t *testing.T) {
	for engine, query := range map[string]string{"duckdb": "SELECT current_database()", "motherduck": "SELECT current_database()", "databricks": "SELECT current_catalog()", "trino": "SELECT current_catalog"} {
		client := &selectorStub{rows: [][]any{{"lake"}}}
		p := Provider{Engine: engine, Client: client}
		catalog, err := p.defaultCatalog(t.Context())
		require.NoError(t, err)
		require.Equal(t, "lake", catalog)
		require.Equal(t, []string{query}, client.queries)
		client.rows = [][]any{{nil}}
		_, err = p.defaultCatalog(t.Context())
		require.ErrorContains(t, err, "explicit catalog is required")
	}
}
