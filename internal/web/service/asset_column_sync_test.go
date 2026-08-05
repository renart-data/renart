package service

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	webmodel "renart/internal/web/model"
	"renart/internal/web/service/assetmeta"
)

func TestAnalyzeColumnSchemaUsesConservativeSafetyRules(t *testing.T) {
	tests := []struct {
		name          string
		current       []pipeline.Column
		meta          assetmeta.RenartMeta
		snapshots     []webmodel.ColumnSchemaSourceSnapshot
		wantKinds     []string
		wantChanges   bool
		wantConflicts bool
	}{
		{
			name:    "new columns and unknown to known types are safe",
			current: []pipeline.Column{{Name: "id"}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id", Type: "INTEGER"}, webmodel.Column{Name: "name", Type: "VARCHAR"}),
			},
			wantKinds:   []string{"type_filled", "added"},
			wantChanges: true,
		},
		{
			name:    "known type changes require review",
			current: []pipeline.Column{{Name: "id", Type: "INTEGER"}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id", Type: "BIGINT"}),
			},
			wantKinds:     []string{"type_conflict"},
			wantConflicts: true,
		},
		{
			name:    "known type becoming unknown requires review",
			current: []pipeline.Column{{Name: "id", Type: "INTEGER"}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id"}),
			},
			wantKinds:     []string{"type_conflict"},
			wantConflicts: true,
		},
		{
			name:    "unknown type remaining unknown is unchanged",
			current: []pipeline.Column{{Name: "id"}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id"}),
			},
			wantKinds: []string{"unchanged"},
		},
		{
			name: "saved column deletions require review",
			current: []pipeline.Column{
				{Name: "id", Type: "INTEGER"},
				{Name: "legacy", Type: "VARCHAR"},
			},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id", Type: "INTEGER"}),
			},
			wantKinds:     []string{"unchanged", "removed"},
			wantConflicts: true,
		},
		{
			name:    "advisory source may fill an unknown definition type",
			current: []pipeline.Column{{Name: "id"}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id"}),
				observedSnapshot("materialized", false, webmodel.Column{Name: "id", Type: "BIGINT"}),
			},
			wantKinds:   []string{"type_filled"},
			wantChanges: true,
		},
		{
			name:    "different known source types require review",
			current: []pipeline.Column{{Name: "id", Type: "INTEGER"}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id", Type: "INTEGER"}),
				observedSnapshot("materialized", false, webmodel.Column{Name: "id", Type: "BIGINT"}),
			},
			wantKinds:     []string{"source_conflict"},
			wantConflicts: true,
		},
		{
			name:    "complete table absence is schema drift",
			current: []pipeline.Column{{Name: "id", Type: "INTEGER"}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id", Type: "INTEGER"}),
				observedSnapshot("materialized", false),
			},
			wantKinds:     []string{"source_missing"},
			wantConflicts: true,
		},
		{
			name:    "sampled response absence is not deletion evidence",
			current: []pipeline.Column{{Name: "id", Type: "INTEGER"}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id", Type: "INTEGER"}),
				observedSnapshot("live_response", true),
			},
			wantKinds: []string{"unchanged"},
		},
		{
			name:    "sampled response fallback retains unobserved saved columns",
			current: []pipeline.Column{{Name: "id", Type: "INTEGER"}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				observedSnapshot("live_response", true),
			},
			wantKinds: []string{"partial_unobserved"},
		},
		{
			name:    "complete advisory source can still refine a partial fallback",
			current: []pipeline.Column{{Name: "id"}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				observedSnapshot("live_response", true),
				observedSnapshot("materialized", false, webmodel.Column{Name: "id", Type: "BIGINT"}),
			},
			wantKinds:   []string{"type_filled"},
			wantChanges: true,
		},
		{
			name:    "complete advisory mismatch still conflicts with a partial fallback",
			current: []pipeline.Column{{Name: "id", Type: "INTEGER"}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				observedSnapshot("live_response", true),
				observedSnapshot("materialized", false, webmodel.Column{Name: "id", Type: "BIGINT"}),
			},
			wantKinds:     []string{"type_conflict"},
			wantConflicts: true,
		},
		{
			name:    "advisory only columns require review",
			current: []pipeline.Column{{Name: "id", Type: "INTEGER"}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id", Type: "INTEGER"}),
				observedSnapshot(
					"materialized",
					false,
					webmodel.Column{Name: "id", Type: "INTEGER"},
					webmodel.Column{Name: "warehouse_only", Type: "VARCHAR"},
				),
			},
			wantKinds:     []string{"unchanged", "observed_only"},
			wantConflicts: true,
		},
		{
			name:    "manual columns are not treated as deletions",
			current: []pipeline.Column{{Name: "id", Type: "INTEGER"}, {Name: "manual_note", Type: "VARCHAR"}},
			meta:    assetmeta.RenartMeta{ColAdd: []string{"manual_note"}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id", Type: "INTEGER"}),
			},
			wantKinds: []string{"unchanged", "manual"},
		},
		{
			name:    "owned saved types remain resolved",
			current: []pipeline.Column{{Name: "id", Type: "BIGINT"}},
			meta:    assetmeta.RenartMeta{ColOwn: map[string][]string{"id": {"type"}}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id", Type: "INTEGER"}),
			},
			wantKinds: []string{"owned"},
		},
		{
			name:    "materialized provenance preserves a known type when sql is unknown",
			current: []pipeline.Column{{Name: "id", Type: "BIGINT"}},
			meta:    assetmeta.RenartMeta{ColSource: map[string]string{"id": columnSourceCodeMaterialized}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id"}),
			},
			wantKinds: []string{"provenance"},
		},
		{
			name:    "fresh materialized provenance wins over conflicting static inference",
			current: []pipeline.Column{{Name: "id", Type: "BIGINT"}},
			meta:    assetmeta.RenartMeta{ColSource: map[string]string{"id": columnSourceCodeMaterialized}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id", Type: "INTEGER"}),
				observedSnapshotWithFresh("materialized", false, true, webmodel.Column{Name: "id", Type: "BIGINT"}),
			},
			wantKinds: []string{"provenance"},
		},
		{
			name:    "stale materialized provenance does not hide conflicting inference",
			current: []pipeline.Column{{Name: "id", Type: "BIGINT"}},
			meta:    assetmeta.RenartMeta{ColSource: map[string]string{"id": columnSourceCodeMaterialized}},
			snapshots: []webmodel.ColumnSchemaSourceSnapshot{
				definitionSnapshot(webmodel.Column{Name: "id", Type: "INTEGER"}),
				observedSnapshotWithFresh("materialized", false, false, webmodel.Column{Name: "id", Type: "BIGINT"}),
			},
			wantKinds:     []string{"source_conflict"},
			wantConflicts: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysis := analyzeColumnSchema(tt.current, tt.meta, tt.snapshots)
			kinds := make([]string, 0, len(analysis.rows))
			for _, row := range analysis.rows {
				kinds = append(kinds, row.Kind)
			}
			assert.Equal(t, tt.wantKinds, kinds)
			assert.Equal(t, tt.wantChanges, analysis.hasChanges)
			assert.Equal(t, tt.wantConflicts, analysis.hasConflicts)
		})
	}
}

func TestAnalyzeColumnSchemaPartialPrimaryRetainsUnobservedColumnsDuringSafeApply(t *testing.T) {
	current := []pipeline.Column{{Name: "existing", Type: "INTEGER"}}
	analysis := analyzeColumnSchema(
		current,
		assetmeta.RenartMeta{},
		[]webmodel.ColumnSchemaSourceSnapshot{
			observedSnapshot("live_response", true, webmodel.Column{Name: "new_field", Type: "VARCHAR"}),
		},
	)

	require.True(t, analysis.hasChanges)
	require.False(t, analysis.hasConflicts)
	assert.Equal(t, []string{"new_field", "existing"}, workspaceColumnNames(analysis.managedColumns))
	assert.Equal(t, []string{"new_field", "existing"}, workspaceColumnNames(analysis.candidateColumns))

	final, _, _ := assetmeta.ReconcileColumns(assetmeta.ColumnReconcileInput{
		Inferred: ModelColumnsToPipelineColumns(analysis.managedColumns),
		Current:  current,
	})
	assert.Equal(t, []string{"new_field", "existing"}, pipelineColumnNames(final))
}

func TestSyncAssetColumnsAppliesOnlySafeChanges(t *testing.T) {
	t.Run("unknown to known applies", func(t *testing.T) {
		header := strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
columns:
  - name: order_id
@bruin */`)
		service, assetID, absPath := newColumnReconcileWorkspace(t, header)

		result, apiErr := service.SyncAssetColumns(context.Background(), assetID, nil, "")
		require.Nil(t, apiErr)
		assert.Equal(t, columnSyncStatusApplied, result.Status)
		require.Len(t, result.Columns, 1)
		assert.Equal(t, "INTEGER", result.Columns[0].Type)

		content, err := os.ReadFile(absPath)
		require.NoError(t, err)
		assert.Contains(t, string(content), "type: INTEGER")
	})

	t.Run("known change returns conflict without writing", func(t *testing.T) {
		header := strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
columns:
  - name: order_id
    type: BIGINT
@bruin */`)
		service, assetID, absPath := newColumnReconcileWorkspace(t, header)
		before, err := os.ReadFile(absPath)
		require.NoError(t, err)

		result, apiErr := service.SyncAssetColumns(context.Background(), assetID, nil, "")
		require.Nil(t, apiErr)
		assert.Equal(t, columnSyncStatusConflicts, result.Status)
		require.Len(t, result.Rows, 1)
		assert.Equal(t, "type_conflict", result.Rows[0].Kind)

		after, err := os.ReadFile(absPath)
		require.NoError(t, err)
		assert.Equal(t, string(before), string(after))
	})
}

func TestSyncAssetColumnsExcludesStaleMaterializationFromDefinitionCollision(t *testing.T) {
	header := strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
columns:
  - name: order_id
    type: INTEGER
@bruin */`)
	service, assetID, _ := newColumnReconcileWorkspace(t, header)
	service.deps.Executor = &stubRunRunner{output: []byte(`{"columns":[{"name":"order_id","type":"BIGINT"}]}`)}
	service.deps.MaterializedSchemaFresh = func(context.Context, string, string, string) (bool, error) {
		return false, nil
	}

	result, apiErr := service.SyncAssetColumns(
		context.Background(), assetID, []string{columnSourceMaterialized}, "dev",
	)
	require.Nil(t, apiErr)
	assert.Equal(t, columnSyncStatusUnchanged, result.Status)
	require.Len(t, result.Sources, 2)
	assert.Equal(t, "comparable", result.Sources[0].Classification)
	assert.Equal(t, "stale", result.Sources[1].Classification)
	assert.Contains(t, result.Sources[1].ExcludedReason, "older asset fingerprint")
	require.Len(t, result.Rows, 1)
	assert.Equal(t, "unchanged", result.Rows[0].Kind)
}

func TestApplyAssetColumnSchemaResolutionPersistsTheChoice(t *testing.T) {
	header := strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
columns:
  - name: order_id
    type: BIGINT
@bruin */`)
	service, assetID, _ := newColumnReconcileWorkspace(t, header)
	syncResult, apiErr := service.SyncAssetColumns(context.Background(), assetID, nil, "")
	require.Nil(t, apiErr)
	require.Equal(t, columnSyncStatusConflicts, syncResult.Status)

	result, apiErr := service.ApplyAssetColumnSchemaResolution(
		context.Background(),
		assetID,
		syncResult.ManagedColumns,
		syncResult.CandidateColumns,
		syncResult.Columns,
		[]webmodel.ColumnSchemaResolution{{
			Column: "order_id", Action: "use", Source: "current", Type: "BIGINT",
		}},
	)
	require.Nil(t, apiErr)
	require.Len(t, result.Columns, 1)
	assert.Equal(t, "BIGINT", result.Columns[0].Type)

	_, _, asset, err := service.deps.ResolveAssetByID(context.Background(), assetID)
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"order_id": {"type"}}, assetmeta.ParseAsset(asset).ColOwn)
	assert.NotContains(t, asset.Meta, assetmeta.KeyColOwn)
	assert.Equal(t, "type", asset.Columns[0].Meta[assetmeta.ColumnKeyOwned])
}

func TestApplyAssetColumnSchemaResolutionTracksAdvisoryOnlyColumnSource(t *testing.T) {
	header := strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
columns:
  - name: order_id
    type: INTEGER
@bruin */`)
	service, assetID, _ := newColumnReconcileWorkspace(t, header)

	result, apiErr := service.ApplyAssetColumnSchemaResolution(
		context.Background(),
		assetID,
		[]webmodel.Column{{Name: "order_id", Type: "INTEGER"}},
		[]webmodel.Column{
			{Name: "order_id", Type: "INTEGER"},
			{Name: "warehouse_only", Type: "VARCHAR"},
		},
		[]webmodel.Column{{Name: "order_id", Type: "INTEGER"}},
		[]webmodel.ColumnSchemaResolution{{
			Column: "warehouse_only", Action: "use", Source: "materialized", Type: "VARCHAR",
		}},
	)
	require.Nil(t, apiErr)
	require.Len(t, result.Columns, 2)
	assert.Equal(t, "warehouse_only", result.Columns[1].Name)

	_, _, asset, err := service.deps.ResolveAssetByID(context.Background(), assetID)
	require.NoError(t, err)
	meta := assetmeta.ParseAsset(asset)
	assert.Empty(t, meta.ColAdd)
	assert.Empty(t, meta.ColOwn)
	assert.Equal(t, map[string]string{"warehouse_only": columnSourceCodeMaterialized}, meta.ColSource)
	assert.NotContains(t, asset.Meta, assetmeta.KeyColSource)
	assert.Equal(t, columnSourceCodeMaterialized, asset.Columns[1].Meta[assetmeta.ColumnKeySource])
}

func TestApplyAssetColumnSchemaResolutionUsesAdvisoryTypeForExistingColumn(t *testing.T) {
	header := strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
columns:
  - name: order_id
    type: INTEGER
@bruin */`)
	service, assetID, _ := newColumnReconcileWorkspace(t, header)

	result, apiErr := service.ApplyAssetColumnSchemaResolution(
		context.Background(),
		assetID,
		[]webmodel.Column{{Name: "order_id", Type: "INTEGER"}},
		[]webmodel.Column{{Name: "order_id", Type: "INTEGER"}},
		[]webmodel.Column{{Name: "order_id", Type: "INTEGER"}},
		[]webmodel.ColumnSchemaResolution{{
			Column: "order_id", Action: "use", Source: "materialized", Type: "BIGINT",
		}},
	)
	require.Nil(t, apiErr)
	require.Len(t, result.Columns, 1)
	assert.Equal(t, "BIGINT", result.Columns[0].Type)

	_, _, asset, err := service.deps.ResolveAssetByID(context.Background(), assetID)
	require.NoError(t, err)
	meta := assetmeta.ParseAsset(asset)
	assert.Empty(t, meta.ColOwn)
	assert.Equal(t, map[string]string{"order_id": columnSourceCodeMaterialized}, meta.ColSource)
	assert.NotContains(t, asset.Meta, assetmeta.KeyColSource)
	assert.Equal(t, columnSourceCodeMaterialized, asset.Columns[0].Meta[assetmeta.ColumnKeySource])
}

func TestApplyAssetColumnSchemaResolutionRejectsStaleSavedSchema(t *testing.T) {
	header := strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
columns:
  - name: order_id
    type: INTEGER
@bruin */`)
	service, assetID, _ := newColumnReconcileWorkspace(t, header)

	_, apiErr := service.ApplyAssetColumnSchemaResolution(
		context.Background(),
		assetID,
		[]webmodel.Column{{Name: "order_id", Type: "INTEGER"}},
		[]webmodel.Column{{Name: "order_id", Type: "INTEGER"}},
		[]webmodel.Column{{Name: "order_id", Type: "BIGINT"}},
		nil,
	)
	require.NotNil(t, apiErr)
	assert.Equal(t, "schema_sync_stale", apiErr.Code)
}

func definitionSnapshot(columns ...webmodel.Column) webmodel.ColumnSchemaSourceSnapshot {
	return webmodel.ColumnSchemaSourceSnapshot{
		Source: webmodel.ColumnInferenceSource{
			ID: "definition", Label: "SQL query", Category: "definition",
		},
		Columns: columns,
	}
}

func observedSnapshot(id string, mayOmit bool, columns ...webmodel.Column) webmodel.ColumnSchemaSourceSnapshot {
	return webmodel.ColumnSchemaSourceSnapshot{
		Source: webmodel.ColumnInferenceSource{
			ID: id, Label: id, Category: "observed", MayOmitColumns: mayOmit,
		},
		Columns: columns,
	}
}

func observedSnapshotWithFresh(
	id string,
	mayOmit bool,
	fresh bool,
	columns ...webmodel.Column,
) webmodel.ColumnSchemaSourceSnapshot {
	snapshot := observedSnapshot(id, mayOmit, columns...)
	snapshot.Fresh = &fresh
	return snapshot
}

func workspaceColumnNames(columns []WorkspaceColumn) []string {
	result := make([]string, 0, len(columns))
	for _, column := range columns {
		result = append(result, column.Name)
	}
	return result
}

func pipelineColumnNames(columns []pipeline.Column) []string {
	result := make([]string, 0, len(columns))
	for _, column := range columns {
		result = append(result, column.Name)
	}
	return result
}
