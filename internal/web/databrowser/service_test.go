package databrowser

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/web/model"
)

func TestConnectionsExposeOnlyQueryableSourcesAndProjectFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	service := New(Dependencies{
		WorkspaceRoot: root,
		ListConnections: func(context.Context, string) (string, []ConnectionConfig, int64, error) {
			return "dev", []ConnectionConfig{
				{Name: "warehouse", Type: "postgres", Queryable: true},
				{Name: "object-store", Type: "s3", Queryable: false},
				{Name: "local", Type: "duckdb", Queryable: true},
			}, 7, nil
		},
		RunQuery: func(context.Context, string, string, string, int) (QueryResult, error) {
			return QueryResult{}, nil
		},
	})

	response, apiErr := service.Connections(context.Background(), "")
	require.Nil(t, apiErr)
	require.Equal(t, "dev", response.Environment)
	require.NotEmpty(t, response.Revision)
	require.Len(t, response.Connections, 3)
	require.Equal(t, []string{"local", "warehouse", localFilesConnectionName}, []string{
		response.Connections[0].Name,
		response.Connections[1].Name,
		response.Connections[2].Name,
	})
	require.True(t, response.Connections[2].Capabilities.PreviewRows)
	for _, connection := range response.Connections {
		require.NotContains(t, connection.ID, "postgres")
	}
}

func TestWarehouseHierarchyAndObjectDetails(t *testing.T) {
	t.Parallel()
	service := New(Dependencies{
		ListConnections: staticConnections([]ConnectionConfig{{Name: "warehouse", Type: "postgres", Queryable: true}}),
		ListDatabases: func(context.Context, string, string) ([]string, error) {
			return []string{"analytics"}, nil
		},
		ListTables: func(context.Context, string, string, string) ([]Table, error) {
			return []Table{{
				Name: "analytics.public.orders", ShortName: "orders", SchemaName: "public", DatabaseName: "analytics",
			}}, nil
		},
		ListColumns: func(context.Context, string, string, string) ([]model.SQLColumn, error) {
			return []model.SQLColumn{{Name: "order_id", Type: "BIGINT"}}, nil
		},
	})

	connections, apiErr := service.Connections(context.Background(), "dev")
	require.Nil(t, apiErr)
	connection := connections.Connections[0]
	databases, apiErr := service.Children(context.Background(), connection.ID, "", "dev")
	require.Nil(t, apiErr)
	require.Equal(t, "database", databases.Nodes[0].NamespaceKind)
	schemas, apiErr := service.Children(context.Background(), connection.ID, databases.Nodes[0].ID, "dev")
	require.Nil(t, apiErr)
	require.Equal(t, "public", schemas.Nodes[0].Label)
	tables, apiErr := service.Children(context.Background(), connection.ID, schemas.Nodes[0].ID, "dev")
	require.Nil(t, apiErr)
	require.Equal(t, "analytics.public.orders", tables.Nodes[0].ReferenceText)

	object, apiErr := service.Object(context.Background(), tables.Nodes[0].ID, "dev")
	require.Nil(t, apiErr)
	require.Equal(t, "orders", object.Object.Name)
	require.Equal(t, []string{"analytics", "public"}, object.Object.Namespace)
	require.Equal(t, []model.SQLColumn{{Name: "order_id", Type: "BIGINT"}}, object.Object.Columns)
}

func TestLocalFilesStayInsideWorkspaceAndPreviewIsServerConstructed(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "data", "nested"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "data", "orders.csv"), []byte("id,name\n1,Ada\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.txt"), []byte("ignore me"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".private"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".private", "secrets.csv"), []byte("token\nsecret\n"), 0o600))
	require.NoError(t, os.Symlink(filepath.Dir(root), filepath.Join(root, "outside")))

	var capturedConnection, capturedQuery string
	service := New(Dependencies{
		WorkspaceRoot:   root,
		ListConnections: staticConnections([]ConnectionConfig{{Name: "local", Type: "duckdb", Queryable: true}}),
		RunQuery: func(_ context.Context, connection, _, query string, _ int) (QueryResult, error) {
			capturedConnection = connection
			capturedQuery = query
			return QueryResult{Columns: []string{"id"}, Rows: []map[string]any{{"id": 1}, {"id": 2}}}, nil
		},
	})

	connections, apiErr := service.Connections(context.Background(), "dev")
	require.Nil(t, apiErr)
	localFiles := connections.Connections[len(connections.Connections)-1]
	rootNodes, apiErr := service.Children(context.Background(), localFiles.ID, "", "dev")
	require.Nil(t, apiErr)
	require.Len(t, rootNodes.Nodes, 1)
	require.Equal(t, "data", rootNodes.Nodes[0].Label)
	dataNodes, apiErr := service.Children(context.Background(), localFiles.ID, rootNodes.Nodes[0].ID, "dev")
	require.Nil(t, apiErr)
	require.Len(t, dataNodes.Nodes, 2)
	require.Equal(t, "nested", dataNodes.Nodes[0].Label)
	require.Equal(t, "orders.csv", dataNodes.Nodes[1].Label)

	preview, apiErr := service.Preview(context.Background(), PreviewRequest{
		ObjectID: dataNodes.Nodes[1].ID, Environment: "dev", Limit: 1,
	})
	require.Nil(t, apiErr)
	require.Equal(t, "local", capturedConnection)
	require.Contains(t, capturedQuery, "read_csv_auto('")
	require.Contains(t, capturedQuery, "limit 2")
	require.NotContains(t, capturedQuery, "notes.txt")
	require.Len(t, preview.Rows, 1)
	require.True(t, preview.Truncated)

	malicious := objectRef{
		Kind: "file", SourceKind: "local_files", Environment: "dev", Revision: localFiles.Revision, Path: "../secret.csv",
	}
	_, apiErr = service.Preview(context.Background(), PreviewRequest{ObjectID: encodeRef(malicious), Environment: "dev"})
	require.NotNil(t, apiErr)
	require.Equal(t, "data_browser_file_invalid", apiErr.Code)

	hidden := malicious
	hidden.Path = ".private/secrets.csv"
	_, apiErr = service.Preview(context.Background(), PreviewRequest{ObjectID: encodeRef(hidden), Environment: "dev"})
	require.NotNil(t, apiErr)
	require.Equal(t, "data_browser_file_invalid", apiErr.Code)
}

func TestStaleConnectionIdentityRequiresRefresh(t *testing.T) {
	t.Parallel()
	revision := int64(1)
	service := New(Dependencies{
		ListConnections: func(context.Context, string) (string, []ConnectionConfig, int64, error) {
			return "dev", []ConnectionConfig{{Name: "warehouse", Type: "postgres", Queryable: true}}, revision, nil
		},
	})
	connections, apiErr := service.Connections(context.Background(), "dev")
	require.Nil(t, apiErr)
	revision++
	_, apiErr = service.Children(context.Background(), connections.Connections[0].ID, "", "dev")
	require.NotNil(t, apiErr)
	require.Equal(t, 409, apiErr.Status)
	require.Equal(t, "data_browser_revision_stale", apiErr.Code)
}

func staticConnections(connections []ConnectionConfig) func(context.Context, string) (string, []ConnectionConfig, int64, error) {
	return func(_ context.Context, environment string) (string, []ConnectionConfig, int64, error) {
		if strings.TrimSpace(environment) == "" {
			environment = "dev"
		}
		return environment, connections, 1, nil
	}
}
