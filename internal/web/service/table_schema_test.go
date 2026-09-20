package service

import (
	"context"
	"errors"
	"github.com/spf13/afero"
	"renart/internal/sqlintelligence"
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Like the pinned MySQL-family clients, this returns metadata rows but no
// ColumnTypes. Tests exercise the executor and observer rather than inventing
// the missing driver type strings in a successful query fixture.
type tableMetadataQuerier struct {
	queries []string
	result  *query.QueryResult
	err     error
}

func (q *tableMetadataQuerier) SelectWithSchema(_ context.Context, statement *query.Query) (*query.QueryResult, error) {
	q.queries = append(q.queries, statement.Query)
	return q.result, q.err
}
func columnMetadataFixture() *query.QueryResult {
	return &query.QueryResult{
		Columns: []string{"COLUMN_NAME", "COLUMN_TYPE"},
		Rows:    [][]any{{"order_id", "bigint"}, {[]byte("amount"), []byte("decimal(18,4)")}, {"label", "varchar(120)"}, {"tags", "array<varchar(40)>"}},
	}
}

func TestTableSchemaDiscoveryUsesDatabaseTypesAcrossMySQLClients(t *testing.T) {
	for _, engine := range []string{"mysql", "vitess", "planetscale", "planetscale_mysql", "starrocks", "doris"} {
		t.Run(engine, func(t *testing.T) {
			conn := &tableMetadataQuerier{result: columnMetadataFixture()}
			newManager := func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
				return &stubConnectionManager{conn: conn, connectionType: engine}, nil
			}
			executor := NewHybridBruinExecutor(".", "bruin", newManager, nil)
			service := NewSQLService(SQLDependencies{Executor: executor, NewConnectionManager: newManager})
			observer := &recordingRemoteCatalogObserver{}
			service.SetRemoteCatalogObserver(observer)
			result, status := service.TableColumns(t.Context(), "warehouse", "analytics.orders", "dev")
			require.Equal(t, 200, status, result.Error)
			require.Equal(t, []SQLColumn{{Name: "order_id", Type: "bigint"}, {Name: "amount", Type: "decimal(18,4)"}, {Name: "label", Type: "varchar(120)"}, {Name: "tags", Type: "array<varchar(40)>"}}, result.Columns)
			assert.Equal(t, result.Columns, observer.columnResults)
			assert.Equal(t, RemoteCatalogScope{Connection: "warehouse", Environment: "dev"}, observer.columnScope)
			require.Len(t, conn.queries, 1)
			readOnly, err := sqlintelligence.IsReadOnlySingleQuery(conn.queries[0], "mysql")
			require.NoError(t, err)
			require.True(t, readOnly, "metadata lookup must work on a read-only connection")
			assert.Contains(t, conn.queries[0], "information_schema.columns")
			assert.NotContains(t, conn.queries[0], "SELECT *")
			assert.Equal(t, conn.queries[0], result.Operation.Query)
		})
	}
}

func TestCurrentTableInferenceUsesResolvedConnectionDialect(t *testing.T) {
	// Load/API/Python targets cannot reliably derive the warehouse from the
	// asset type. Resolve it from the actual destination connection.
	conn := &tableMetadataQuerier{result: columnMetadataFixture()}
	executor := NewHybridBruinExecutor(".", "bruin", func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
		return &stubConnectionManager{conn: conn, connectionType: "starrocks"}, nil
	}, nil)
	asset := &pipeline.Asset{Name: "lake.analytics.orders", Type: pipeline.AssetType("api"), Connection: "warehouse"}
	service := &AssetService{deps: AssetDependencies{Executor: executor}}
	columns, _, apiErr := service.inferMaterializedAssetColumns(t.Context(), &pipeline.Pipeline{}, asset, "dev")
	require.Nil(t, apiErr)
	require.Len(t, columns, 4)
	assert.Equal(t, "decimal(18,4)", columns[1].Type)
	require.Len(t, conn.queries, 1)
	assert.Contains(t, conn.queries[0], "FROM `lake`.information_schema.columns")
	assert.Contains(t, conn.queries[0], "TABLE_SCHEMA = 'analytics'")
}

func TestTableSchemaQueryScopesAndQuotesNames(t *testing.T) {
	cases := []struct{ engine, table, query string }{
		{"starrocks", "lake.sales.orders", "SELECT COLUMN_NAME, COLUMN_TYPE FROM `lake`.information_schema.columns WHERE TABLE_SCHEMA = 'sales' AND TABLE_NAME = 'orders' ORDER BY ORDINAL_POSITION"},
		{"doris", "internal.sales.orders", "SELECT COLUMN_NAME, COLUMN_TYPE FROM `internal`.information_schema.columns WHERE TABLE_SCHEMA = 'sales' AND TABLE_NAME = 'orders' ORDER BY ORDINAL_POSITION"},
		{"mysql", "orders", "SELECT COLUMN_NAME, COLUMN_TYPE FROM information_schema.columns WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'orders' ORDER BY ORDINAL_POSITION"},
		{"starrocks", "`lake``x`.`sales.eu`.`o'rders`", "SELECT COLUMN_NAME, COLUMN_TYPE FROM `lake``x`.information_schema.columns WHERE TABLE_SCHEMA = 'sales.eu' AND TABLE_NAME = 'o''rders' ORDER BY ORDINAL_POSITION"},
		{"mysql", "`sales`.`x'; DROP TABLE orders;--`", "SELECT COLUMN_NAME, COLUMN_TYPE FROM information_schema.columns WHERE TABLE_SCHEMA = 'sales' AND TABLE_NAME = 'x''; DROP TABLE orders;--' ORDER BY ORDINAL_POSITION"},
	}
	for _, tt := range cases {
		got, metadata, err := tableSchemaQuery(tt.engine, tt.table)
		require.NoError(t, err)
		assert.True(t, metadata)
		assert.Equal(t, tt.query, got)
	}
	for _, name := range []string{"", "a.b.c.d", "`unclosed", "a.\\b", "a.\x00b"} {
		_, _, err := tableSchemaQuery("starrocks", name)
		require.Error(t, err, name)
	}
	_, _, err := tableSchemaQuery("mysql", "a.b.c")
	require.Error(t, err)
}

func TestTableSchemaNativeTypesAndEmptyRelations(t *testing.T) {
	for _, engine := range []string{"postgres", "redshift", "mssql", "synapse", "fabric", "oracle", "snowflake", "clickhouse", "trino", "databricks", "google_cloud_platform", "athena", "vertica", "spark", "sail", "dremio", "motherduck"} {
		t.Run(engine, func(t *testing.T) {
			native := &query.QueryResult{Columns: []string{"id"}, ColumnTypes: []string{"BIGINT"}, Rows: [][]any{}}
			conn := &tableMetadataQuerier{result: native}
			result, err := selectTableSchema(t.Context(), conn, engine, "sales.orders")
			require.NoError(t, err)
			assert.Same(t, native, result)
			assert.Contains(t, conn.queries[0], "WHERE 1 = 0")
			assert.NotContains(t, conn.queries[0], "LIMIT")
		})
	}
}

func TestTableSchemaRejectsMissingOrMalformedMetadata(t *testing.T) {
	for _, metadata := range []*query.QueryResult{
		nil,
		{Columns: []string{"other"}},
		{Columns: []string{"COLUMN_NAME", "COLUMN_TYPE"}},
		{Columns: []string{"COLUMN_NAME", "COLUMN_TYPE"}, Rows: [][]any{{"id"}}},
		{Columns: []string{"COLUMN_NAME", "COLUMN_TYPE"}, Rows: [][]any{{"id", nil}}},
	} {
		_, err := tableSchemaFromMetadata(metadata)
		require.Error(t, err)
	}
	conn := &tableMetadataQuerier{err: errors.New("metadata access denied")}
	_, err := selectTableSchema(t.Context(), conn, "starrocks", "analytics.orders")
	require.ErrorIs(t, err, conn.err)
	require.Len(t, conn.queries, 1, "do not hide permission errors by querying a different catalog")
}

func TestFillColumnsKeepsDeclarationsWhenDriverOmitsTypes(t *testing.T) {
	for _, types := range [][]string{nil, {""}} {
		asset := &pipeline.Asset{Name: "analytics.orders", Type: pipeline.AssetTypePostgresQuery, Connection: "warehouse", Columns: []pipeline.Column{{Name: "id", Type: "BIGINT"}}}
		conn := &tableMetadataQuerier{result: &query.QueryResult{Columns: []string{"id"}, ColumnTypes: types}}
		status, err := fillDirectColumnsFromDB(t.Context(), &directPipelineInfo{Asset: asset, Pipeline: &pipeline.Pipeline{}}, afero.NewMemMapFs(), "", &stubConnectionManager{conn: conn, connectionType: "postgres"})
		require.Error(t, err)
		assert.Equal(t, fillStatusFailed, status)
		assert.Equal(t, []pipeline.Column{{Name: "id", Type: "BIGINT"}}, asset.Columns)
	}
}
