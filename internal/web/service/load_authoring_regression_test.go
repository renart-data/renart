package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func newLoadAuthoringWorkspace(t *testing.T, name string) (*AssetService, string, string) {
	t.Helper()
	root := t.TempDir()
	rel := "analytics/assets/analytics/orders.asset.yml"
	file := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "analytics/pipeline.yml"), []byte("name: analytics\n"), 0o644))
	content := "type: load\nconnection: target\nparameters:\n  source_connection: lake\n  source_table: s3://bucket/orders/*.parquet\nmaterialization:\n  type: table\n  strategy: create+replace\n# keep this comment\nx-custom: kept\n"
	if name != "" {
		content = "name: " + name + "\n" + content
	}
	require.NoError(t, os.WriteFile(file, []byte(content), 0o644))
	svc := NewAssetService(AssetDependencies{
		WorkspaceRoot:                              root,
		ResolveAssetByID:                           newAssetTestResolver(root).ResolveAssetByID,
		SuppressWatcher:                            func(string) {},
		PushWorkspaceUpdateImmediate:               func(context.Context, string, string) {},
		PushWorkspaceUpdateImmediateWithChangedIDs: func(context.Context, string, string, []string) {},
	})
	return svc, EncodeID(rel), file
}

func TestLoadRenamePersistsExplicitAndInferredNames(t *testing.T) {
	for _, explicit := range []string{"", "analytics.orders"} {
		t.Run("explicit="+explicit, func(t *testing.T) {
			svc, id, file := newLoadAuthoringWorkspace(t, explicit)
			name := "reporting.renamed_orders"
			result, apiErr := svc.Update(t.Context(), id, AssetUpdateRequest{Name: &name})
			require.Nil(t, apiErr)
			require.Equal(t, id, result.AssetID, "Load rename must not move its file")
			_, _, asset, err := svc.deps.ResolveAssetByID(t.Context(), id)
			require.NoError(t, err)
			require.Equal(t, name, asset.Name)
			require.Equal(t, "s3://bucket/orders/*.parquet", asset.Parameters[loadParamSourceTable])
			content, err := os.ReadFile(file)
			require.NoError(t, err)
			require.Contains(t, string(content), "# keep this comment")
			require.Contains(t, string(content), "x-custom: kept")
			// Subsequent metadata writes must not restore the old path-derived name.
			owner := "data-team"
			_, apiErr = svc.Update(t.Context(), id, AssetUpdateRequest{Owner: &owner})
			require.Nil(t, apiErr)
			_, _, asset, err = svc.deps.ResolveAssetByID(t.Context(), id)
			require.NoError(t, err)
			require.Equal(t, name, asset.Name)
		})
	}
}

func TestLoadSchemaSyncFallsBackToSelectedCurrentTable(t *testing.T) {
	for _, source := range []string{"s3://bucket/orders/*.parquet", "public.orders", "sftp://host/incoming/*.csv"} {
		t.Run(source, func(t *testing.T) {
			svc, id, file := newLoadAuthoringWorkspace(t, "analytics.orders")
			_, _, asset, err := svc.deps.ResolveAssetByID(t.Context(), id)
			require.NoError(t, err)
			asset.Parameters[loadParamSourceTable] = source
			require.NoError(t, persistYAMLAssetDefinition(svc.fs(), asset))
			before, err := os.ReadFile(file)
			require.NoError(t, err)
			_, apiErr := svc.SyncAssetColumns(t.Context(), id, nil, "dev")
			require.NotNil(t, apiErr, "No implicit warehouse queries")
			svc.deps.Executor = &stubRunRunner{output: []byte(`{"columns":[{"name":"id","type":"BIGINT"},{"name":"_sling_loaded_at","type":"BIGINT"}]}`)}
			result, apiErr := svc.SyncAssetColumns(t.Context(), id, []string{columnSourceMaterialized}, "dev")
			require.Nil(t, apiErr)
			require.Equal(t, columnSyncStatusApplied, result.Status)
			require.Len(t, result.Columns, 1)
			require.Equal(t, "id", result.Columns[0].Name)
			require.Equal(t, "BIGINT", result.Columns[0].Type)
			require.NotEmpty(t, result.Notes)
			require.Len(t, result.Sources, 1)
			require.Equal(t, columnSourceMaterialized, result.Sources[0].Source.ID)
			after, err := os.ReadFile(file)
			require.NoError(t, err)
			require.NotEqual(t, string(before), string(after))
			// Saved provenance re-observes the same table without requiring a new checkbox.
			result, apiErr = svc.SyncAssetColumns(t.Context(), id, nil, "dev")
			require.Nil(t, apiErr)
			require.Equal(t, columnSyncStatusUnchanged, result.Status)
		})
	}
}

func TestLoadSchemaSyncDoesNotHideCurrentTableErrors(t *testing.T) {
	svc, id, file := newLoadAuthoringWorkspace(t, "analytics.orders")
	before, err := os.ReadFile(file)
	require.NoError(t, err)
	svc.deps.Executor = &stubRunRunner{err: errors.New("table unavailable")}
	_, apiErr := svc.SyncAssetColumns(t.Context(), id, []string{columnSourceMaterialized}, "dev")
	require.NotNil(t, apiErr)
	require.Equal(t, "infer_columns_failed", apiErr.Code)
	after, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
}

func TestLoadSchemaSyncRetainsFreshnessOfExplicitTableSeed(t *testing.T) {
	svc, id, file := newLoadAuthoringWorkspace(t, "analytics.orders")
	before, err := os.ReadFile(file)
	require.NoError(t, err)
	svc.deps.Executor = &stubRunRunner{output: []byte(`{"columns":[{"name":"id","type":"BIGINT"}]}`)}
	svc.deps.MaterializedSchemaFresh = func(context.Context, string, string, string) (bool, error) {
		return false, nil
	}
	result, apiErr := svc.SyncAssetColumns(t.Context(), id, []string{columnSourceMaterialized}, "dev")
	require.Nil(t, apiErr)
	// With no source declaration, the explicitly selected table may seed the
	// schema. It must not be promoted to fresh source-definition evidence.
	require.Equal(t, columnSyncStatusApplied, result.Status)
	require.Len(t, result.Sources, 1)
	require.NotNil(t, result.Sources[0].Fresh)
	require.False(t, *result.Sources[0].Fresh)
	require.Contains(t, result.Notes[0], "not a validation of the source files")
	after, err := os.ReadFile(file)
	require.NoError(t, err)
	require.NotEqual(t, string(before), string(after))
}
