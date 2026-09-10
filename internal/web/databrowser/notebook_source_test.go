package databrowser

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/web/model"
	"renart/internal/web/sqlnamespace"
)

func TestNotebookSourceResolvesExactCatalogWithoutReadingData(t *testing.T) {
	present := true
	revision := int64(1)
	svc := New(Dependencies{
		ListConnections: func(context.Context, string) (string, []ConnectionConfig, int64, error) {
			return "dev", []ConnectionConfig{{Name: "warehouse", Type: "starrocks", Queryable: true, AccessMode: "read_only", NotebookSource: true}}, revision, nil
		},
		ListWarehouse: func(_ context.Context, _ string, scope sqlnamespace.Scope, env string) ([]sqlnamespace.Entry, error) {
			require.Equal(t, "dev", env)
			require.Equal(t, sqlnamespace.Scope{Catalog: "lake", Database: "sales"}, scope)
			if !present {
				return nil, nil
			}
			return []sqlnamespace.Entry{{Kind: "table", Name: "order.items", Reference: "lake.sales.`order.items`", Scope: scope}}, nil
		},
		ListColumns: func(context.Context, string, string, string) ([]model.SQLColumn, error) {
			t.Fatal("must not describe source data")
			return nil, nil
		},
		RunQuery: func(context.Context, string, string, string, int) (QueryResult, error) {
			t.Fatal("must not execute source data")
			return QueryResult{}, nil
		},
	})
	connections, apiErr := svc.Connections(t.Context(), "dev")
	require.Nil(t, apiErr)
	ref, _ := decodeRef(connections.Connections[0].ID)
	ref.Kind, ref.Catalog, ref.Database, ref.Name, ref.LeafName = "table", "lake", "sales", "lake.sales.`order.items`", "order.items"
	id := encodeRef(ref)
	source, apiErr := svc.NotebookSource(t.Context(), id, "dev")
	require.Nil(t, apiErr)
	require.Equal(t, "select * from `lake`.`sales`.`order.items`", source.Query)
	require.Equal(t, "warehouse", source.Connection)
	present = false
	_, apiErr = svc.NotebookSource(t.Context(), id, "dev")
	require.Equal(t, 404, apiErr.Status)
	present = true
	revision++
	_, apiErr = svc.NotebookSource(t.Context(), id, "dev")
	require.Equal(t, 409, apiErr.Status)
	_, apiErr = svc.NotebookSource(t.Context(), id, "prod")
	require.NotNil(t, apiErr)
}

func TestNotebookSourceFilesAreLiteralAndRevalidated(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "orders.csv"), []byte("id\n1\n"), 0600))
	svc := New(Dependencies{WorkspaceRoot: root, ListConnections: staticConnections(nil), RunQuery: func(context.Context, string, string, string, int) (QueryResult, error) {
		t.Fatal("must not preview")
		return QueryResult{}, nil
	}})
	connections, apiErr := svc.Connections(t.Context(), "dev")
	require.Nil(t, apiErr)
	nodes, apiErr := svc.Children(t.Context(), connections.Connections[0].ID, "", "dev")
	require.Nil(t, apiErr)
	source, apiErr := svc.NotebookSource(t.Context(), nodes.Nodes[0].ID, "dev")
	require.Nil(t, apiErr)
	require.Equal(t, "orders.csv", source.URI)
	require.Empty(t, source.Connection)
	require.Equal(t, "csv", source.Format)
	require.NoError(t, os.Remove(filepath.Join(root, "orders.csv")))
	_, apiErr = svc.NotebookSource(t.Context(), nodes.Nodes[0].ID, "dev")
	require.NotNil(t, apiErr)
}

func TestNotebookSourceStorageCapability(t *testing.T) {
	for _, engine := range []string{"s3", "sftp"} {
		t.Run(engine, func(t *testing.T) {
			svc := New(Dependencies{ListConnections: staticConnections([]ConnectionConfig{{Name: "lake", Type: engine, Storage: true}}), ListStorage: func(context.Context, string, StorageQuery, string) (StorageListing, error) {
				return StorageListing{Entries: []StorageEntry{{Path: "orders.csv", Reference: "s3://bucket/orders.csv"}, {Path: "partition/", Reference: "s3://bucket/partition/", Directory: true}, {Path: "notes.txt", Reference: "s3://bucket/notes.txt"}}}, nil
			}})
			connections, apiErr := svc.Connections(t.Context(), "dev")
			require.Nil(t, apiErr)
			nodes, apiErr := svc.Children(t.Context(), connections.Connections[0].ID, "", "dev")
			require.Nil(t, apiErr)
			for _, node := range nodes.Nodes {
				source, err := svc.NotebookSource(t.Context(), node.ID, "dev")
				if engine == "s3" && node.Label == "orders.csv" {
					require.Nil(t, err)
					require.Equal(t, "s3://bucket/orders.csv", source.URI)
				} else {
					require.NotNil(t, err)
				}
			}
		})
	}
}
