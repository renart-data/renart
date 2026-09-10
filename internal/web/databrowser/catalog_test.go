package databrowser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/web/dataaddress"
	"renart/internal/web/model"
	"renart/internal/web/sqlnamespace"
)

func TestCatalogBrowserResolvePreviewAndRevision(t *testing.T) {
	var calls []sqlnamespace.Scope
	revision := int64(1)
	var sql string
	s := New(Dependencies{
		ListConnections: func(context.Context, string) (string, []ConnectionConfig, int64, error) {
			return "dev", []ConnectionConfig{{Name: "sr", Type: "starrocks", Queryable: true}}, revision, nil
		},
		ListWarehouse: func(_ context.Context, connection string, scope sqlnamespace.Scope, environment string) ([]sqlnamespace.Entry, error) {
			require.Equal(t, "sr", connection)
			require.Equal(t, "dev", environment)
			calls = append(calls, scope)
			if scope.Catalog == "" && scope.Database == "" {
				return []sqlnamespace.Entry{{Kind: "catalog", Name: "lake", Scope: sqlnamespace.Scope{Catalog: "lake"}, Default: true}, {Kind: "catalog", Name: "warehouse", Scope: sqlnamespace.Scope{Catalog: "warehouse"}}}, nil
			}
			if scope.Catalog == "" {
				scope.Catalog = "lake"
			}
			if scope.Database == "" {
				scope.Database = "sales"
				return []sqlnamespace.Entry{{Kind: "database", Name: "sales", Scope: scope}}, nil
			}
			return []sqlnamespace.Entry{{Kind: "table", Name: "Order.Items", Scope: scope, Reference: sqlnamespace.Reference("starrocks", scope.Catalog, scope.Database, "Order.Items")}}, nil
		},
		ListColumns: func(_ context.Context, _, table, _ string) ([]model.SQLColumn, error) {
			return []model.SQLColumn{{Name: table, Type: "INT"}}, nil
		},
		RunQuery: func(_ context.Context, _, _, query string, _ int) (QueryResult, error) {
			sql = query
			return QueryResult{}, nil
		},
	})
	connections, apiErr := s.Connections(t.Context(), "dev")
	require.Nil(t, apiErr)
	var connection Connection
	for _, c := range connections.Connections {
		if c.Name == "sr" {
			connection = c
		}
	}
	catalogs, apiErr := s.Children(t.Context(), connection.ID, "", "dev")
	require.Nil(t, apiErr)
	require.Len(t, calls, 1)
	require.True(t, catalogs.Nodes[0].IsDefault)
	require.Equal(t, "catalog", catalogs.Nodes[0].NamespaceKind)
	for _, catalog := range catalogs.Nodes {
		databases, err := s.Children(t.Context(), connection.ID, catalog.ID, "dev")
		require.Nil(t, err)
		tables, err := s.Children(t.Context(), connection.ID, databases.Nodes[0].ID, "dev")
		require.Nil(t, err)
		address := *tables.Nodes[0].Address
		require.Equal(t, catalog.Label, address.Catalog)
		object, err := s.Resolve(t.Context(), ResolveRequest{Environment: "dev", Address: address})
		require.Nil(t, err)
		require.Equal(t, address, *object.Object.Address)
		require.Equal(t, connection.ID, object.Object.ConnectionID)
		require.Empty(t, sql, "resolve must not preview")
		_, err = s.Preview(t.Context(), PreviewRequest{ObjectID: object.Object.ID, Environment: "dev"})
		require.Nil(t, err)
		require.Contains(t, sql, "`"+catalog.Label+"`.`sales`.`Order.Items`")
		sql = ""
		revision++
		_, err = s.Object(t.Context(), object.Object.ID, "dev")
		require.NotNil(t, err)
		require.Equal(t, "data_browser_revision_stale", err.Code)
		fresh, err := s.Resolve(t.Context(), ResolveRequest{Environment: "dev", Address: address})
		require.Nil(t, err)
		require.NotEqual(t, object.Object.ID, fresh.Object.ID)
		revision--
	}
	legacy, err := s.Resolve(t.Context(), ResolveRequest{Environment: "dev", Address: dataaddress.Address{SourceKind: "warehouse", Connection: "sr", ConnectionType: "starrocks", Database: "sales", Name: "Order.Items"}})
	require.Nil(t, err)
	require.Equal(t, "lake", legacy.Object.Address.Catalog)
}

func TestCatalogBrowserResolvesLegacyNativeAddresses(t *testing.T) {
	for _, engine := range []string{"trino", "duckdb", "motherduck", "databricks"} {
		t.Run(engine, func(t *testing.T) {
			s := New(Dependencies{
				ListConnections: staticConnections([]ConnectionConfig{{Name: "warehouse", Type: engine, Queryable: true}}),
				ListWarehouse: func(_ context.Context, _ string, scope sqlnamespace.Scope, _ string) ([]sqlnamespace.Entry, error) {
					require.Empty(t, scope.Database)
					require.Equal(t, "sales", scope.Schema)
					if engine == "trino" {
						require.Equal(t, "lake", scope.Catalog)
					} else {
						require.Empty(t, scope.Catalog, "resolve only against the configured default")
						scope.Catalog = "lake"
					}
					return []sqlnamespace.Entry{{Kind: "table", Name: "orders", Scope: scope, Reference: "lake.sales.orders"}}, nil
				},
			})
			address := dataaddress.Address{SourceKind: "warehouse", Connection: "warehouse", ConnectionType: engine, Database: "sales", Name: "orders"}
			if engine == "trino" {
				address.Database, address.Schema = "lake", "sales"
			}
			resolved, err := s.Resolve(t.Context(), ResolveRequest{Environment: "dev", Address: address})
			require.Nil(t, err)
			require.Equal(t, "lake", resolved.Object.Address.Catalog)
			require.Equal(t, "sales", resolved.Object.Address.Schema)
			require.Equal(t, "lake.sales.orders", resolved.Object.ReferenceText)
		})
	}
}
