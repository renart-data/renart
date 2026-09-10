package duckdbworkspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
	duck "github.com/bruin-data/bruin/pkg/duckdb"
	"github.com/bruin-data/bruin/pkg/query"
	"github.com/stretchr/testify/require"
)

type connectionManager struct {
	connection any
	typeName   string
}

func (m *connectionManager) GetConnection(string) any        { return m.connection }
func (m *connectionManager) GetConnectionDetails(string) any { return nil }
func (m *connectionManager) GetConnectionType(string) string { return m.typeName }

var _ config.ConnectionAndDetailsGetter = (*connectionManager)(nil)

type resolvingConnectionManager struct {
	connectionManager
	err error
}

func (m *resolvingConnectionManager) ResolveConnection(string) (any, error) {
	return m.connection, m.err
}

func TestClientResolvesRelativeFilesFromWorkspace(t *testing.T) {
	workspaceRoot := filepath.Join(t.TempDir(), "Lukas's workspace")
	require.NoError(t, os.MkdirAll(workspaceRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceRoot, "rows.csv"), []byte("id,name\n1,Ada\n"), 0o600))

	base, err := duck.NewClient(duck.Config{Path: ""})
	require.NoError(t, err)
	t.Cleanup(base.Close)
	client := WrapClient(base, workspaceRoot)

	result, err := client.SelectWithSchema(t.Context(), &query.Query{Query: `select count(*) as row_count from './rows.csv'`})
	require.NoError(t, err)
	require.Equal(t, []string{"row_count"}, result.Columns)
	require.Equal(t, [][]any{{int64(1)}}, result.Rows)
}

func TestRelativeFileViewDiscoveryUsesWorkspace(t *testing.T) {
	root := t.TempDir()
	base, err := duck.NewClient(duck.Config{Path: filepath.Join(root, "views.duckdb")})
	require.NoError(t, err)
	t.Cleanup(base.Close)
	client := WrapClient(base, root).(*Client)
	parquet := strings.ReplaceAll(filepath.Join(root, "workspace_view_rows.parquet"), "'", "''")
	require.NoError(t, client.RunQueryWithoutResult(t.Context(), &query.Query{Query: "COPY (SELECT 42 AS value) TO '" + parquet + "' (FORMAT PARQUET)"}))
	require.NoError(t, client.RunQueryWithoutResult(t.Context(), &query.Query{Query: "CREATE VIEW main.relative_rows AS SELECT * FROM 'workspace_view_rows.parquet'"}))
	_, err = base.SelectWithSchema(t.Context(), &query.Query{Query: "SELECT * FROM main.relative_rows"})
	require.Error(t, err, "unscoped native query must reproduce the wrong-working-directory failure")
	t.Run("preview", func(t *testing.T) {
		result, err := client.SelectWithSchema(t.Context(), &query.Query{Query: "SELECT * FROM main.relative_rows"})
		require.NoError(t, err)
		require.Equal(t, [][]any{{int64(42)}}, result.Rows)
	})
	t.Run("schemas", func(t *testing.T) {
		names, err := client.GetDatabases(t.Context())
		require.NoError(t, err)
		require.Contains(t, names, "main")
	})
	t.Run("tables", func(t *testing.T) {
		names, err := client.GetTables(t.Context(), "main")
		require.NoError(t, err)
		require.Contains(t, names, "relative_rows")
	})
	t.Run("columns", func(t *testing.T) {
		columns, err := client.GetColumns(t.Context(), "main", "relative_rows")
		require.NoError(t, err)
		require.Len(t, columns, 1)
		require.Equal(t, "value", columns[0].Name)
	})
	t.Run("summary", func(t *testing.T) {
		_, err := client.GetDatabaseSummary(t.Context())
		require.NoError(t, err)
	})
}

func TestClientDisablesLocalFilesystemAccessWithoutBreakingOrdinaryQueries(t *testing.T) {
	workspaceRoot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workspaceRoot, "rows.csv"), []byte("id,name\n1,Ada\n"), 0o600))

	base, err := duck.NewClient(duck.Config{Path: ""})
	require.NoError(t, err)
	t.Cleanup(base.Close)
	client := WrapClientWithFilesystemAccess(base, workspaceRoot, false)

	_, err = client.SelectWithSchema(t.Context(), &query.Query{Query: `select * from './rows.csv'`})
	require.Error(t, err)
	require.Contains(t, err.Error(), "LocalFileSystem")

	result, err := client.SelectWithSchema(t.Context(), &query.Query{Query: `select 1 as value`})
	require.NoError(t, err)
	require.Equal(t, []string{"value"}, result.Columns)
}

func TestManagerOnlyWrapsDuckDBConnections(t *testing.T) {
	baseClient, err := duck.NewClient(duck.Config{Path: ""})
	require.NoError(t, err)
	t.Cleanup(baseClient.Close)
	duckManager := WrapManager(&connectionManager{connection: baseClient, typeName: "duckdb"}, t.TempDir())

	first := duckManager.GetConnection("warehouse")
	second := duckManager.GetConnection("warehouse")
	require.IsType(t, &Client{}, first)
	require.Same(t, first, second)

	other := &connectionManager{connection: "postgres-client", typeName: "postgres"}
	require.Equal(t, "postgres-client", WrapManager(other, t.TempDir()).GetConnection("warehouse"))
}

func TestManagerPreservesConnectionResolutionErrors(t *testing.T) {
	expected := errors.New("secret is not configured")
	wrapped := WrapManager(&resolvingConnectionManager{
		connectionManager: connectionManager{typeName: "postgres"},
		err:               expected,
	}, t.TempDir())

	resolver, ok := wrapped.(config.ConnectionResolver)
	require.True(t, ok)
	connection, err := resolver.ResolveConnection("warehouse")
	require.Nil(t, connection)
	require.ErrorIs(t, err, expected)
}
