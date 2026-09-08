package databrowser

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/web/model"
)

func TestViewDefinitionQueriesAreScopedAndReadOnly(t *testing.T) {
	for _, engine := range []string{"duckdb", "postgres", "redshift", "mysql", "starrocks", "trino", "snowflake", "clickhouse"} {
		t.Run(engine, func(t *testing.T) {
			ref := objectRef{ConnectionType: engine, Database: `cat"alog`, Schema: "reports", Name: "reports.o'hare", LeafName: "o'hare"}
			sql := viewDefinitionQuery(ref)
			require.Contains(t, sql, "select ")
			require.Contains(t, sql, "'reports'")
			require.Contains(t, sql, "'o''hare'")
			require.Contains(t, sql, "limit 2")
			if engine == "trino" || engine == "snowflake" {
				require.Contains(t, sql, `"cat""alog".information_schema.views`)
			}
			if engine == "duckdb" {
				require.Contains(t, sql, "database_name = current_database()")
			}
			ref.Name, ref.LeafName = `reports.back\slash`, `back\slash`
			require.Empty(t, viewDefinitionQuery(ref))
		})
	}
	require.Empty(t, viewDefinitionQuery(objectRef{ConnectionType: "unknown", Name: "main.view"}))
	require.Empty(t, viewDefinitionQuery(objectRef{ConnectionType: "trino", Name: "schema.view"}))
	require.Contains(t, viewDefinitionQuery(objectRef{ConnectionType: "duckdb", Name: "attached.main.view"}), "database_name = 'attached'")
}

func TestMissingViewFileKeepsDefinitionAndExplainsProjectRoot(t *testing.T) {
	root := t.TempDir()
	missing := errors.New(`IO Error: No files found that match the pattern "nested/example.parquet"`)
	const definition = "CREATE VIEW main.example AS SELECT * FROM 'nested/example.parquet';"
	service := New(Dependencies{
		WorkspaceRoot:   root,
		ListConnections: staticConnections([]ConnectionConfig{{Name: "local", Type: "duckdb", Queryable: true}}),
		LookupViewDefinition: func(_ context.Context, connection, environment, sql string) (string, error) {
			require.Equal(t, "local", connection)
			require.Equal(t, "dev", environment)
			require.Contains(t, sql, "from duckdb_views()")
			return definition, nil
		},
		ListColumns: func(context.Context, string, string, string) ([]model.SQLColumn, error) { return nil, missing },
		RunQuery:    func(context.Context, string, string, string, int) (QueryResult, error) { return QueryResult{}, missing },
	})
	connections, apiErr := service.Connections(t.Context(), "dev")
	require.Nil(t, apiErr)
	ref, err := decodeRef(connections.Connections[0].ID)
	require.NoError(t, err)
	ref.Kind, ref.Name, ref.LeafName = "table", "main.example", "example"
	id := encodeRef(ref)
	object, apiErr := service.Object(t.Context(), id, "dev")
	require.Nil(t, apiErr)
	require.Equal(t, definition, object.Object.ViewDefinition)
	require.Empty(t, object.Object.Columns)
	require.Contains(t, object.Object.Warning, root)
	require.Contains(t, object.Object.Warning, "another or nested project")
	require.Contains(t, object.Object.Warning, missing.Error())
	_, apiErr = service.Preview(t.Context(), PreviewRequest{ObjectID: id, Environment: "dev"})
	require.NotNil(t, apiErr)
	require.Contains(t, apiErr.Message, root)
	require.Equal(t, missing, service.describeError(objectRef{ConnectionType: "postgres"}, missing))
	unrelated := errors.New("permission denied")
	require.Equal(t, unrelated, service.describeError(ref, unrelated))
}
