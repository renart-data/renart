package databrowser

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStorageWildcardSearchUsesLiteralMatchedBranches(t *testing.T) {
	for _, kind := range []string{"s3", "sftp"} {
		t.Run(kind, func(t *testing.T) {
			var queries []StorageQuery
			svc := New(Dependencies{
				ListConnections: staticConnections([]ConnectionConfig{{Name: "lake", Type: kind, Storage: true}}),
				ListStorage: func(_ context.Context, _ string, q StorageQuery, _ string) (StorageListing, error) {
					require.NoError(t, q.Validate())
					queries = append(queries, q)
					if q.Prefix == "table/" {
						return StorageListing{Entries: []StorageEntry{
							{Path: "table/day=2026-09-1/", Directory: true}, {Path: "table/day=2026-09-2/", Directory: true},
							{Path: "table/day=2025-09-1/", Directory: true}, {Path: "outside/bad/", Directory: true},
						}}, nil
					}
					return StorageListing{Entries: []StorageEntry{{Path: q.Prefix + "part-01.parquet"}, {Path: q.Prefix + "part-01.csv"}}}, nil
				},
			})
			connections, apiErr := svc.Connections(t.Context(), "dev")
			require.Nil(t, apiErr)
			result, apiErr := svc.SearchStorage(t.Context(), connections.Connections[0].ID, "table/day=2026-09-?/part*.parquet", "dev")
			require.Nil(t, apiErr)
			require.False(t, result.Truncated)
			require.Len(t, result.Nodes, 2)
			require.Len(t, queries, 3)
			require.Equal(t, "day=2026-09-1/part-01.parquet", result.Nodes[0].Label)
			require.NotEmpty(t, result.Nodes[0].Address)
			require.NotContains(t, result.Nodes[0].ReferenceText, "*")
			if kind == "s3" {
				require.Equal(t, "day=2026-09-", queries[0].NamePrefix)
				require.Equal(t, "part", queries[1].NamePrefix)
			}
			for _, query := range queries {
				require.NotContains(t, query.Prefix+query.NamePrefix, "*")
				require.NotContains(t, query.Prefix+query.NamePrefix, "?")
			}
		})
	}
}

func TestStorageWildcardSearchBudgetsAndValidation(t *testing.T) {
	calls := 0
	svc := New(Dependencies{
		ListConnections: staticConnections([]ConnectionConfig{{Name: "lake", Type: "s3", Storage: true}}),
		ListStorage: func(_ context.Context, _ string, q StorageQuery, _ string) (StorageListing, error) {
			calls++
			if q.Prefix == "" {
				entries := []StorageEntry{}
				for i := 0; i < 50; i++ {
					entries = append(entries, StorageEntry{Path: fmt.Sprintf("day=%02d/", i), Directory: true})
				}
				return StorageListing{Entries: entries}, nil
			}
			return StorageListing{Entries: []StorageEntry{{Path: q.Prefix + "data.csv"}}}, nil
		},
	})
	connections, apiErr := svc.Connections(t.Context(), "dev")
	require.Nil(t, apiErr)
	id := connections.Connections[0].ID
	for _, pattern := range []string{"../*.csv", "/a*", "a/**/x", "a/[ab]", "a|b*", "a?\x00", "a//b*", strings.Repeat("a/", 33) + "*"} {
		_, apiErr = svc.SearchStorage(t.Context(), id, pattern, "dev")
		require.NotNil(t, apiErr, pattern)
		require.Equal(t, 400, apiErr.Status)
	}
	require.Zero(t, calls)
	result, apiErr := svc.SearchStorage(t.Context(), id, "day=*/data.csv", "dev")
	require.Nil(t, apiErr)
	require.True(t, result.Truncated)
	require.Equal(t, maxStorageSearchListings, calls)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, apiErr = svc.SearchStorage(ctx, id, "day=*", "dev")
	require.NotNil(t, apiErr)
}
