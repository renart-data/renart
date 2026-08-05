package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	webmodel "renart/internal/web/model"
	"renart/internal/web/service/assetmeta"
)

func TestColumnInferenceSourcesAreAssetCapabilities(t *testing.T) {
	tests := []struct {
		name       string
		asset      *pipeline.Asset
		connection string
		want       []string
	}{
		{
			name: "sql definition and current table",
			asset: &pipeline.Asset{
				Type:            pipeline.AssetTypeDuckDBQuery,
				Materialization: pipeline.Materialization{Type: pipeline.MaterializationTypeView},
			},
			connection: "warehouse",
			want:       []string{columnSourceDefinition, columnSourceMaterialized},
		},
		{
			name:       "api definition live response and current table",
			asset:      &pipeline.Asset{Type: pipeline.AssetType("api")},
			connection: "warehouse",
			want:       []string{columnSourceDefinition, columnSourceLiveResponse, columnSourceMaterialized},
		},
		{
			name:       "load source and current table",
			asset:      &pipeline.Asset{Type: pipeline.AssetType(loadAssetType)},
			connection: "warehouse",
			want:       []string{columnSourceDefinition, columnSourceMaterialized},
		},
		{
			name:       "local seed file and current table",
			asset:      &pipeline.Asset{Type: pipeline.AssetTypeDuckDBSeed, Parameters: pipeline.ParameterMap{"path": "./users.csv"}},
			connection: "warehouse",
			want:       []string{columnSourceDefinition, columnSourceMaterialized},
		},
		{
			name:       "remote seed only current table",
			asset:      &pipeline.Asset{Type: pipeline.AssetTypeDuckDBSeed, Parameters: pipeline.ParameterMap{"path": "https://example.com/users.csv"}},
			connection: "warehouse",
			want:       []string{columnSourceMaterialized},
		},
		{
			name: "python table only has current table evidence",
			asset: &pipeline.Asset{
				Type:            pipeline.AssetTypePython,
				Materialization: pipeline.Materialization{Type: pipeline.MaterializationTypeTable},
			},
			connection: "warehouse",
			want:       []string{columnSourceMaterialized},
		},
		{
			name:       "source anchor has no output schema",
			asset:      &pipeline.Asset{Type: pipeline.AssetTypePostgresSource},
			connection: "warehouse",
			want:       []string{},
		},
		{
			name:       "sensor has no schema",
			asset:      &pipeline.Asset{Type: pipeline.AssetTypePostgresQuerySensor},
			connection: "warehouse",
			want:       []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assetContext := AssetSchemaContext{Asset: tt.asset, ConnectionName: tt.connection}
			definitionMatches := 0
			for _, provider := range assetSchemaSourceProviders() {
				if provider.ID() == columnSourceDefinition && provider.Matches(assetContext) {
					definitionMatches++
				}
			}
			assert.LessOrEqual(t, definitionMatches, 1, "one registry provider must own an asset's definition schema")

			sources := columnInferenceSourcesForAsset(tt.asset, tt.connection)
			ids := make([]string, 0, len(sources))
			for _, source := range sources {
				ids = append(ids, source.ID)
			}
			assert.Equal(t, tt.want, ids)
			for _, source := range sources {
				if source.ID == columnSourceLiveResponse {
					assert.True(t, source.MayOmitColumns, "a sampled response cannot prove a column was deleted")
				}
			}
		})
	}
}

func TestAPIColumnInferenceUsesCanonicalTargetConnection(t *testing.T) {
	asset := &pipeline.Asset{Name: "example.api", Type: pipeline.AssetType("api")}
	parsedPipeline := &pipeline.Pipeline{
		DefaultConnections: pipeline.EmptyStringMap{"duckdb": "duckdb-default"},
	}

	sources := columnInferenceSourcesForPipelineAsset(asset, parsedPipeline)
	ids := make([]string, 0, len(sources))
	for _, source := range sources {
		ids = append(ids, source.ID)
	}
	assert.Equal(t, []string{
		columnSourceDefinition,
		columnSourceLiveResponse,
		columnSourceMaterialized,
	}, ids)

	request, err := BuildInferAssetColumnsQuery(parsedPipeline, asset, "dev")
	require.NoError(t, err)
	assert.Equal(t, "duckdb-default", request.ConnectionName)
	assert.Equal(t, `select * from "example"."api" limit 1`, request.Query)
	assert.Equal(t, "dev", request.Environment)
	assert.True(t, request.LogicalSchema)
}

func TestSyncAssetColumnsAcceptsCurrentTableForAPIAsset(t *testing.T) {
	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetsRoot := filepath.Join(pipelineRoot, "assets")
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(`name: analytics
default_connections:
  duckdb: duckdb-default
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "api.asset.yml"), []byte(`name: analytics.api
type: api
parameters:
  request:
    url: https://example.invalid/records
  response:
    fields:
      id: id
columns:
  - name: id
    type: VARCHAR
materialization:
  type: table
`), 0o644))

	executor := &stubRunRunner{output: []byte(`{"columns":[{"name":"id","type":"VARCHAR"},{"name":"_sling_loaded_at","type":"TIMESTAMP"}]}`)}
	service := NewAssetService(AssetDependencies{
		WorkspaceRoot:                workspaceRoot,
		ResolveAssetByID:             newAssetTestResolver(workspaceRoot).ResolveAssetByID,
		Executor:                     executor,
		SuppressWatcher:              func(string) {},
		PushWorkspaceUpdateImmediate: func(context.Context, string, string) {},
	})

	result, apiErr := service.SyncAssetColumns(
		context.Background(),
		EncodeID("analytics/assets/api.asset.yml"),
		[]string{columnSourceMaterialized},
		"dev",
	)
	require.Nil(t, apiErr)
	assert.Equal(t, columnSyncStatusApplied, result.Status)
	require.Len(t, result.Sources, 2)
	assert.Equal(t, columnSourceDefinition, result.Sources[0].Source.ID)
	assert.Equal(t, columnSourceMaterialized, result.Sources[1].Source.ID)
	assert.Equal(t, []webmodel.Column{{Name: "id", Type: "VARCHAR"}}, result.Sources[1].Columns)
	assert.Empty(t, result.Sources[1].Notes)
	assert.Contains(t, executor.args, "duckdb-default")

	_, _, syncedAsset, err := service.deps.ResolveAssetByID(context.Background(), EncodeID("analytics/assets/api.asset.yml"))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"id": columnSourceCodeMaterialized}, assetmeta.ParseAsset(syncedAsset).ColSource)
	assert.NotContains(t, syncedAsset.Meta, assetmeta.KeyColSource)
	require.Len(t, syncedAsset.Columns, 1)
	assert.Equal(t, columnSourceCodeMaterialized, syncedAsset.Columns[0].Meta[assetmeta.ColumnKeySource])

	// Once the exception is recorded, a normal sync re-observes its source even
	// when the UI does not explicitly select Current table again.
	result, apiErr = service.SyncAssetColumns(
		context.Background(),
		EncodeID("analytics/assets/api.asset.yml"),
		nil,
		"dev",
	)
	require.Nil(t, apiErr)
	assert.Equal(t, columnSyncStatusUnchanged, result.Status)
	require.Len(t, result.Sources, 2)
	assert.Equal(t, columnSourceMaterialized, result.Sources[1].Source.ID)
	assert.Contains(t, strings.Join(result.Notes, " "), "saved type source")
}

func TestCompareColumnSchemasReportsMeaningfulDrift(t *testing.T) {
	drift := compareColumnSchemas(
		[]pipeline.Column{
			{Name: "id", Type: "INTEGER"},
			{Name: "display_name", Type: "VARCHAR"},
			{Name: "legacy", Type: "TEXT"},
		},
		[]WorkspaceColumn{
			{Name: "id", Type: "int32"},
			{Name: "display_name", Type: "string"},
			{Name: "created_at", Type: "TIMESTAMP"},
		},
	)

	assert.Equal(t, 1, drift.Added)
	assert.Equal(t, 1, drift.Removed)
	assert.Equal(t, 0, drift.TypeChanged)
	assert.Equal(t, 2, drift.Unchanged)
	assert.Equal(t, []webmodel.ColumnSchemaDriftItem{
		{Column: "created_at", Kind: "added", InferredType: "TIMESTAMP"},
		{Column: "legacy", Kind: "removed", CurrentType: "TEXT"},
	}, drift.Items)
}

func TestCompareColumnSchemasPreservesStructuredTypeDetails(t *testing.T) {
	precision := 18
	declaredScale := 4
	matchingScale := 4
	differentScale := 2
	current := []pipeline.Column{{
		Name: "amount", Type: "decimal", Precision: &precision, Scale: &declaredScale,
	}}

	drift := compareColumnSchemas(current, []WorkspaceColumn{{
		Name: "amount", Type: "numeric", Precision: &precision, Scale: &matchingScale,
	}})
	assert.Equal(t, 1, drift.Unchanged)
	assert.Zero(t, drift.TypeChanged)

	drift = compareColumnSchemas(current, []WorkspaceColumn{{
		Name: "amount", Type: "numeric", Precision: &precision, Scale: &differentScale,
	}})
	assert.Equal(t, 1, drift.TypeChanged)
	require.Len(t, drift.Items, 1)
	assert.Equal(t, "decimal(18, 4)", drift.Items[0].CurrentType)
	assert.Equal(t, "numeric(18, 2)", drift.Items[0].InferredType)
}

func TestPreviewAssetColumnsDoesNotPersistUntilApplied(t *testing.T) {
	header := `/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
columns:
  - name: order_id
    type: BIGINT
@bruin */`
	service, assetID, absPath := newColumnReconcileWorkspace(t, header)
	before, err := os.ReadFile(absPath)
	require.NoError(t, err)

	preview, apiErr := service.PreviewAssetColumns(context.Background(), assetID, columnSourceDefinition, "")
	require.Nil(t, apiErr)
	require.Equal(t, "ok", preview.Status)
	assert.Equal(t, columnSourceDefinition, preview.Source.ID)
	assert.Equal(t, 1, preview.Drift.TypeChanged)
	assert.Equal(t, "INTEGER", preview.Columns[0].Type)

	after, err := os.ReadFile(absPath)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after))
}
