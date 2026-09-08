package databrowser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoragePathsRejectControlCharactersConsistently(t *testing.T) {
	for _, value := range []string{"a\x00.csv", "a\t.csv", "a\n.csv", "a\x1f.csv", "a\x7f.csv", "a\u0085.csv"} {
		require.Error(t, ValidateStoragePath(value, false), "%q", value)
	}
	require.NoError(t, ValidateStoragePath("events/Frühstück.csv", false))
}

func TestStoragePrefixListsOnlyTheRequestedLocation(t *testing.T) {
	var requested []string
	svc := New(Dependencies{
		ListConnections: staticConnections([]ConnectionConfig{{Name: "lake", Type: "s3", Storage: true}}),
		ListStorage: func(_ context.Context, _, prefix, _ string) (StorageListing, error) {
			requested = append(requested, prefix)
			return StorageListing{Entries: []StorageEntry{
				{Path: "some/deep/path/orders.csv", Reference: "s3://bucket/some/deep/path/orders.csv"},
				{Path: "outside.csv"},
			}}, nil
		},
	})
	connections, apiErr := svc.Connections(t.Context(), "dev")
	require.Nil(t, apiErr)
	id := connections.Connections[0].ID
	result, apiErr := svc.Prefix(t.Context(), id, "some/deep/path/", "dev")
	require.Nil(t, apiErr)
	require.Equal(t, []string{"some/deep/path/"}, requested)
	require.Len(t, result.Nodes, 1)
	require.Equal(t, "orders.csv", result.Nodes[0].Label)
	require.NotEmpty(t, result.ParentID)
	for _, value := range []string{"../secret/", "/absolute/", "a//b/", "a/*/", "s3://other/", "a\n/"} {
		_, apiErr = svc.Prefix(t.Context(), id, value, "dev")
		require.NotNil(t, apiErr, value)
		require.Equal(t, 400, apiErr.Status)
	}
	require.Len(t, requested, 1, "invalid paths must not reach Sling")
	_, apiErr = svc.Prefix(t.Context(), id, "", "other")
	require.NotNil(t, apiErr, "environment scope still applies")
}

func TestStorageHierarchyRevalidatesObjectsAndAccess(t *testing.T) {
	var requested []string
	svc := New(Dependencies{
		ListConnections: staticConnections([]ConnectionConfig{{Name: "lake", Type: "s3", Storage: true, AccessMode: "read_only"}}),
		ListStorage: func(_ context.Context, connection, prefix, environment string) (StorageListing, error) {
			requested = append(requested, prefix)
			require.Equal(t, "lake", connection)
			require.Equal(t, "dev", environment)
			if prefix == "" {
				return StorageListing{Entries: []StorageEntry{{Path: "events/", Directory: true, Reference: "s3://bucket/events/"}}}, nil
			}
			return StorageListing{Entries: []StorageEntry{{Path: "events/a.csv", Reference: "s3://bucket/events/a.csv"}}}, nil
		},
	})
	connections, apiErr := svc.Connections(t.Context(), "dev")
	require.Nil(t, apiErr)
	require.Len(t, connections.Connections, 1)
	connection := connections.Connections[0]
	require.Equal(t, "storage", connection.SourceKind)
	require.False(t, connection.Capabilities.Query)
	nodes, apiErr := svc.Children(t.Context(), connection.ID, "", "dev")
	require.Nil(t, apiErr)
	require.Len(t, nodes.Nodes, 1)
	require.Equal(t, "prefix", nodes.Nodes[0].NamespaceKind)
	prefixID := nodes.Nodes[0].ID
	nodes, apiErr = svc.Children(t.Context(), connection.ID, prefixID, "dev")
	require.Nil(t, apiErr)
	object, apiErr := svc.Object(t.Context(), nodes.Nodes[0].ID, "dev")
	require.Nil(t, apiErr)
	require.Equal(t, "s3://bucket/events/a.csv", object.Object.ReferenceText)
	require.True(t, object.Object.Capabilities.LoadSource)
	require.False(t, object.Object.Capabilities.LoadDestination)
	require.False(t, object.Object.Capabilities.PreviewRows)
	require.Equal(t, []string{"", "events/", "events/"}, requested)
	_, apiErr = svc.Preview(t.Context(), PreviewRequest{ObjectID: nodes.Nodes[0].ID, Environment: "dev"})
	require.NotNil(t, apiErr, "storage must never fall through to SQL preview")
	resolved, apiErr := svc.Resolve(t.Context(), ResolveRequest{Environment: "dev", Address: *object.Object.Address})
	require.Nil(t, apiErr)
	require.Equal(t, object.Object.ID, resolved.Object.ID)
}

func TestStorageObjectRechecksRemovalAndReadOnlyChanges(t *testing.T) {
	configs := []ConnectionConfig{{Name: "lake", Type: "s3", Storage: true}}
	entries := []StorageEntry{{Path: "a.csv", Reference: "s3://bucket/a.csv"}}
	svc := New(Dependencies{
		ListConnections: func(context.Context, string) (string, []ConnectionConfig, int64, error) {
			return "dev", configs, 1, nil
		},
		ListStorage: func(context.Context, string, string, string) (StorageListing, error) {
			return StorageListing{Entries: entries}, nil
		},
	})
	connections, apiErr := svc.Connections(t.Context(), "dev")
	require.Nil(t, apiErr)
	nodes, apiErr := svc.Children(t.Context(), connections.Connections[0].ID, "", "dev")
	require.Nil(t, apiErr)
	object, apiErr := svc.Object(t.Context(), nodes.Nodes[0].ID, "dev")
	require.Nil(t, apiErr)
	require.True(t, object.Object.Capabilities.LoadDestination)
	entries = nil
	_, apiErr = svc.Object(t.Context(), object.Object.ID, "dev")
	require.Equal(t, 404, apiErr.Status)
	entries = []StorageEntry{{Path: "a.csv", Reference: "s3://bucket/a.csv"}}
	configs[0].AccessMode = "read_only"
	_, apiErr = svc.Object(t.Context(), object.Object.ID, "dev")
	require.Equal(t, 409, apiErr.Status)
	fresh, apiErr := svc.Resolve(t.Context(), ResolveRequest{Environment: "dev", Address: *object.Object.Address})
	require.Nil(t, apiErr)
	require.True(t, fresh.Object.Capabilities.LoadSource)
	require.False(t, fresh.Object.Capabilities.LoadDestination)
}
