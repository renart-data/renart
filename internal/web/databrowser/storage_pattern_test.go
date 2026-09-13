package databrowser

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoragePatternIsScopedSourceOnlyWithoutListing(t *testing.T) {
	for _, kind := range []string{"s3", "sftp"} {
		t.Run(kind, func(t *testing.T) {
			svc := New(Dependencies{
				ListConnections: staticConnections([]ConnectionConfig{{Name: "lake", Type: kind, Storage: true, AccessMode: "read_only"}}),
				ListStorage: func(context.Context, string, StorageQuery, string) (StorageListing, error) {
					t.Fatal("A pattern source must not expand a possibly truncated listing")
					return StorageListing{}, nil
				},
				StoragePatternReference: func(_ context.Context, connection, pattern, environment string) (string, error) {
					require.Equal(t, "lake", connection)
					require.Equal(t, "dev", environment)
					return kind + "://bucket/configured-root/" + pattern, nil
				},
			})
			connections, apiErr := svc.Connections(t.Context(), "dev")
			require.Nil(t, apiErr)
			id := connections.Connections[0].ID
			result, apiErr := svc.StoragePattern(t.Context(), id, "orders/day=*/part-?.csv", "dev")
			require.Nil(t, apiErr)
			require.Equal(t, kind+"://bucket/configured-root/orders/day=*/part-?.csv", result.Object.ReferenceText)
			require.True(t, result.Object.Capabilities.LoadSource)
			require.False(t, result.Object.Capabilities.LoadDestination)
			require.False(t, result.Object.Capabilities.NotebookSource)
			require.False(t, result.Object.Capabilities.PreviewRows)
			require.Nil(t, result.Object.Address, "A selector is not a routable object")
			again, apiErr := svc.Object(t.Context(), result.Object.ID, "dev")
			require.Nil(t, apiErr)
			require.Equal(t, result.Object.ReferenceText, again.Object.ReferenceText)
			_, apiErr = svc.Object(t.Context(), result.Object.ID, "other")
			require.NotNil(t, apiErr)
			for _, invalid := range []string{"literal.csv", "../*.csv", "/a*", "a/**/b", "a/[abc]*", "a|b*", "a*\x00", "a//b*", "s3://other/*"} {
				_, apiErr = svc.StoragePattern(t.Context(), id, invalid, "dev")
				require.NotNil(t, apiErr, invalid)
			}
			_, apiErr = svc.Preview(t.Context(), PreviewRequest{ObjectID: result.Object.ID, Environment: "dev"})
			require.NotNil(t, apiErr, "Patterns are authoring sources, not executable previews")
		})
	}
}
