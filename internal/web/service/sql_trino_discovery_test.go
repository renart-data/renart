package service

import (
	"context"
	"errors"
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type trinoDiscoveryStub struct {
	queries []string
	rows    [][]any
	err     error
}

func (s *trinoDiscoveryStub) Select(_ context.Context, q *query.Query) ([][]any, error) {
	s.queries = append(s.queries, q.Query)
	return s.rows, s.err
}

func trinoDiscoveryService(conn *trinoDiscoveryStub) *SQLService {
	return NewSQLService(SQLDependencies{NewConnectionManager: func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
		return &stubConnectionManager{conn: conn, connectionType: "trino"}, nil
	}})
}

func TestTrinoDiscoveryCatalogsAndQualifiedTables(t *testing.T) {
	conn := &trinoDiscoveryStub{rows: [][]any{{"warehouse"}, {[]byte("memory")}, {"warehouse"}}}
	svc := trinoDiscoveryService(conn)
	catalogs, apiErr := svc.Databases(t.Context(), "trino-prod", "production")
	require.Nil(t, apiErr)
	assert.Equal(t, []string{"memory", "warehouse"}, catalogs.Databases)
	assert.Equal(t, []string{"SHOW CATALOGS"}, conn.queries)
	conn.rows = [][]any{{"sales", "orders"}, {[]byte("sales"), []byte("customers")}}
	tables, apiErr := svc.Tables(t.Context(), "trino-prod", "warehouse", "production")
	require.Nil(t, apiErr)
	require.Len(t, tables.Tables, 2)
	assert.Equal(t, "warehouse.sales.customers", tables.Tables[0].Name)
	assert.Equal(t, "warehouse.sales.orders", tables.Tables[1].Name)
	assert.Contains(t, conn.queries[1], `FROM "warehouse".information_schema.tables`)
}

func TestTrinoDiscoveryQuotesCatalogAndPropagatesErrors(t *testing.T) {
	conn := &trinoDiscoveryStub{}
	svc := trinoDiscoveryService(conn)
	_, apiErr := svc.Tables(t.Context(), "trino", `a"b; DROP TABLE x--`, "default")
	require.Nil(t, apiErr)
	require.Len(t, conn.queries, 1)
	assert.Contains(t, conn.queries[0], `FROM "a""b; DROP TABLE x--".information_schema.tables`)
	_, apiErr = svc.Tables(t.Context(), "trino", "", "default")
	require.NotNil(t, apiErr)
	assert.Len(t, conn.queries, 1)
	conn.err = errors.New("catalog access denied")
	_, apiErr = svc.Databases(t.Context(), "trino", "default")
	require.NotNil(t, apiErr)
	assert.Equal(t, "sql_database_discovery_failed", apiErr.Code)
	assert.Contains(t, apiErr.Message, "catalog access denied")
}

func TestMySQLCompatibleDiscovery(t *testing.T) {
	for _, connectionType := range []string{"mysql", "starrocks", "doris", "vitess", "planetscale"} {
		t.Run(connectionType, func(t *testing.T) {
			catalog := ""
			if connectionType == "starrocks" {
				catalog = "default_catalog"
			}
			if connectionType == "doris" {
				catalog = "internal"
			}
			catalogSQL, catalogRef := "", ""
			if catalog != "" {
				catalogSQL = "`" + catalog + "`."
				catalogRef = catalog + "."
			}
			conn := &trinoDiscoveryStub{rows: [][]any{{[]byte("analytics")}, {"analytics"}}}
			svc := NewSQLService(SQLDependencies{NewConnectionManager: func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
				return &stubConnectionManager{conn: conn, connectionType: connectionType}, nil
			}})
			result, apiErr := svc.Databases(t.Context(), "warehouse", "dev")
			require.Nil(t, apiErr)
			assert.Equal(t, []string{"analytics"}, result.Databases)
			databaseSQL := "SHOW DATABASES"
			if catalog != "" {
				databaseSQL += " FROM `" + catalog + "`"
			}
			assert.Equal(t, databaseSQL, conn.queries[0])
			conn.rows = [][]any{{[]byte("orders")}}
			tables, apiErr := svc.Tables(t.Context(), "warehouse", "analytics", "dev")
			require.Nil(t, apiErr)
			require.Len(t, tables.Tables, 1)
			assert.Equal(t, catalogRef+"analytics.orders", tables.Tables[0].Name)
			assert.Equal(t, "SHOW TABLES FROM "+catalogSQL+"`analytics`", conn.queries[1])
			_, apiErr = svc.Tables(t.Context(), "warehouse", "a`b; DROP TABLE x--", "dev")
			require.Nil(t, apiErr)
			assert.Equal(t, "SHOW TABLES FROM "+catalogSQL+"`a``b; DROP TABLE x--`", conn.queries[2])
			_, apiErr = svc.Tables(t.Context(), "warehouse", "\x00", "dev")
			require.NotNil(t, apiErr)
			assert.Len(t, conn.queries, 3)
		})
	}
}
