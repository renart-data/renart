package service

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/require"
	"renart/internal/web/policy"
)

func TestReadOnlyCreationProfileKeepsSourcesNotDestinations(t *testing.T) {
	svc, _ := newAssetCreationProfileTestService(t, "name: analytics\ndefault_connections:\n  duckdb: warehouse\n", assetCreationProfileTestConfig)
	require.NoError(t, policy.Save(filepath.Join(svc.deps.WorkspaceRoot, ".renart", "environments.yml"), policy.Config{Environments: map[string]policy.EnvironmentPolicy{"dev": {Connections: map[string]policy.ConnectionPolicy{"warehouse": {AccessMode: policy.ReadOnly}}}}}))
	profile, apiErr := svc.AssetCreationProfile(context.Background(), EncodeID("analytics"), "dev")
	require.Nil(t, apiErr)
	load, _ := findAssetCreationKindProfile(profile, assetCreationKindLoad)
	source, _ := findAssetCreationRoleProfile(load, assetCreationRoleSource)
	destination, _ := findAssetCreationRoleProfile(load, assetCreationRoleDestination)
	require.Contains(t, assetCreationConnectionNames(source.Connections), "warehouse")
	require.NotContains(t, assetCreationConnectionNames(destination.Connections), "warehouse")
	sql, _ := findAssetCreationKindProfile(profile, assetCreationKindSQL)
	reader, _ := findAssetCreationRoleProfile(sql, "read_target")
	require.Contains(t, assetCreationConnectionNames(reader.Connections), "warehouse", "existing SQL without materialization must remain selectable on read-only connections")
	_, apiErr = svc.Create(context.Background(), EncodeID("analytics"), CreateAssetParams{Name: "analytics.copy", Kind: "load", Environment: "dev", Connection: "warehouse", Parameters: map[string]string{"source_connection": "reporting", "source_table": "orders"}})
	require.NotNil(t, apiErr)
}

func TestAssetAccessRequirements(t *testing.T) {
	pl := &pipeline.Pipeline{DefaultConnections: map[string]string{"duckdb": "db"}}
	for _, tc := range []struct {
		name, sql       string
		typ             pipeline.AssetType
		materialization pipeline.MaterializationType
		want            policy.Effect
	}{
		{"source", "", pipeline.AssetTypeDuckDBSource, "", policy.Read},
		{"query", "select 1", pipeline.AssetTypeDuckDBQuery, "", policy.Read},
		{"view", "select 1", pipeline.AssetTypeDuckDBQuery, pipeline.MaterializationTypeView, policy.Write},
		{"DDL", "create table t as select 1", pipeline.AssetTypeDuckDBQuery, "", policy.Unknown},
		{"select into", "select 1 into t", pipeline.AssetTypePostgresQuery, "", policy.Unknown},
		{"script", "select 1; delete from t", pipeline.AssetTypeDuckDBQuery, "", policy.Unknown},
		{"python", "", pipeline.AssetTypePython, pipeline.MaterializationTypeTable, policy.Write},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &pipeline.Asset{Name: "t", Type: tc.typ, Connection: "db", ExecutableFile: pipeline.ExecutableFile{Content: tc.sql}, Materialization: pipeline.Materialization{Type: tc.materialization}}
			reqs, err := assetAccessRequirements(pl, a)
			require.NoError(t, err)
			require.NotEmpty(t, reqs)
			require.Equal(t, tc.want, reqs[0].Effect)
		})
	}
	load := &pipeline.Asset{Name: "copied", Type: loadAssetType, Connection: "destination", Parameters: pipeline.ParameterMap{"source_connection": "source", "source_table": "orders"}}
	reqs, err := assetAccessRequirements(pl, load)
	require.NoError(t, err)
	require.Contains(t, reqs, policy.Requirement{Connection: "source", Effect: policy.Read, Operation: "load_source"})
	require.Contains(t, reqs, policy.Requirement{Connection: "destination", Effect: policy.Write, Operation: "load_destination"})
}

func TestCurrentConnectionPolicyGuardsBeforeInitialization(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".renart", "environments.yml")
	require.NoError(t, policy.Save(path, policy.Config{Environments: map[string]policy.EnvironmentPolicy{"prod": {Connections: map[string]policy.ConnectionPolicy{"destination": {AccessMode: policy.ReadOnly}}}}}))
	cfg := &config.Config{SelectedEnvironmentName: "prod", SelectedEnvironment: &config.Environment{Connections: &config.Connections{DuckDB: []config.DuckDBConnection{{ConnectionMetadata: config.ConnectionMetadata{Name: "source"}}, {ConnectionMetadata: config.ConnectionMetadata{Name: "destination"}}}}}}
	pl := &pipeline.Pipeline{}
	asset := &pipeline.Asset{Name: "copied", Type: loadAssetType, Connection: "destination", Parameters: pipeline.ParameterMap{"source_connection": "source", "source_table": "orders"}}
	err := checkAssetsConnectionAccess(root, cfg, pl, []*pipeline.Asset{asset})
	require.ErrorContains(t, err, "load_destination")
	// Changing the live project policy is effective even with the same parsed
	// deployment/config object; no captured snapshot policy is authoritative.
	require.NoError(t, policy.Save(path, policy.Config{Environments: map[string]policy.EnvironmentPolicy{"prod": {Connections: map[string]policy.ConnectionPolicy{"source": {AccessMode: policy.ReadOnly}}}}}))
	require.NoError(t, checkAssetsConnectionAccess(root, cfg, pl, []*pipeline.Asset{asset}))
}

func TestReadOnlyDeploymentReviewAndUnselectedBranch(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, "id: pipeline-uuid\nname: analytics\ndefault_connections:\n  duckdb: duckdb-default", map[string]string{
		"read.sql":  "/* @bruin\nname: analytics.read\ntype: duckdb.sql\n@bruin */\nselect 1 as id",
		"write.sql": "/* @bruin\nname: analytics.write\ntype: duckdb.sql\nmaterialization:\n  type: table\n@bruin */\nselect 2 as id",
	})
	svc := newTestPipelinePlanService(root, &pipelinePlanStalenessStub{}, nil)
	request := PipelinePlanRequest{Purpose: PipelinePlanPurposeDeployment, Selection: PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAll}}
	before, apiErr := svc.Plan(t.Context(), EncodeID("analytics"), request)
	require.Nil(t, apiErr)
	require.NoError(t, policy.Save(filepath.Join(root, ".renart", "environments.yml"), policy.Config{Environments: map[string]policy.EnvironmentPolicy{"default": {Connections: map[string]policy.ConnectionPolicy{"duckdb-default": {AccessMode: policy.ReadOnly}}}}}))
	after, apiErr := svc.Plan(t.Context(), EncodeID("analytics"), request)
	require.Nil(t, apiErr)
	require.Equal(t, PipelinePlanStatusBlocked, after.Status)
	require.NotEqual(t, before.ID, after.ID)
	require.True(t, len(after.Readiness.Blockers) > 0)
	require.Equal(t, "connection_read_only", after.Readiness.Blockers[0].DiagnosticCode)
	require.Equal(t, "access_mode", after.Readiness.Blockers[0].Target.Field)
	request.Purpose = ""
	request.Selection = PipelinePlanSelectionRequest{Mode: PipelinePlanSelectionAsset, AssetName: "analytics.read"}
	read, apiErr := svc.Plan(t.Context(), EncodeID("analytics"), request)
	require.Nil(t, apiErr)
	for _, blocker := range read.Readiness.Blockers {
		require.NotEqual(t, "connection_read_only", blocker.DiagnosticCode)
	}
}

func TestReadOnlyNativeModeCannotBeOverridden(t *testing.T) {
	for _, connection := range []config.DuckDBConnection{
		{Path: "example.db", ReadOnly: true},
		{Path: "duckdb://example.db?access_mode=read_only"},
	} {
		require.True(t, nativeConnectionReadOnly(connection))
		p := policy.EnvironmentPolicy{Connections: map[string]policy.ConnectionPolicy{"db": {AccessMode: policy.ReadWrite}}}
		require.Equal(t, policy.ReadOnly, policy.EffectiveMode(p, "db", nativeConnectionReadOnly(connection)))
	}
}

func TestReadOnlyNotebookPromotionDoesNotWrite(t *testing.T) {
	root := promotionWorkspace(t)
	writeWorkspaceFile(t, root, ".bruin.yml", "default_environment: default\nenvironments:\n  default:\n    connections:\n      duckdb:\n        - name: duckdb-default\n          path: data.db\n")
	writeWorkspaceFile(t, root, "notebooks/demo/notebook.yml", "version: 2\nid: demo\ntitle: Demo\nblocks:\n  - cell: sql_one\n")
	writeWorkspaceFile(t, root, "notebooks/demo/one.sql", "/* @bruin\nid: sql_one\nclass: notebook\ntype: duckdb.sql\n@bruin */\nselect 1 as id")
	require.NoError(t, policy.Save(filepath.Join(root, ".renart", "environments.yml"), policy.Config{Environments: map[string]policy.EnvironmentPolicy{"default": {Connections: map[string]policy.ConnectionPolicy{"duckdb-default": {AccessMode: policy.ReadOnly}}}}}))
	svc := promotionNotebookService(t, root)
	_, apiErr := svc.PromoteCell(EncodeID("notebooks/demo"), "sql_one", PromoteCellRequest{PipelineID: EncodeID("analytics"), TargetName: "marts.copied"})
	require.NotNil(t, apiErr)
	require.Equal(t, "connection_read_only", apiErr.Code)
	require.FileExists(t, filepath.Join(root, "notebooks/demo/one.sql"))
	require.NoFileExists(t, filepath.Join(root, "analytics/assets/copied.sql"))
}

func TestReadOnlyAccessPreviewIsReadOnlyAndDirectional(t *testing.T) {
	_, root := writeTypeCheckWorkspace(t, "name: analytics\ndefault_connections:\n  duckdb: duckdb-default", map[string]string{
		"source.asset.yml": "name: source\ntype: duckdb.source\n",
		"read.sql":         "/* @bruin\nname: read\ntype: duckdb.sql\n@bruin */\nselect 1 as id",
		"write.sql":        "/* @bruin\nname: write\ntype: duckdb.sql\nmaterialization:\n  type: table\n@bruin */\nselect 1 as id",
	})
	service := NewConfigService(root, filepath.Join(root, ".bruin.yml"))
	preview, err := service.PreviewConnectionReadOnly(t.Context(), "default", "duckdb-default")
	require.NoError(t, err)
	require.Empty(t, preview.Warnings)
	require.Len(t, preview.Assets, 1)
	require.Equal(t, "write", preview.Assets[0].Asset)
	require.NoFileExists(t, filepath.Join(root, ".renart", "environments.yml"))
	require.NoFileExists(t, filepath.Join(root, "local.db"))
}

func TestReadOnlyRequirementsIncludeHooksChecksAndOpaqueSource(t *testing.T) {
	pl := &pipeline.Pipeline{}
	asset := &pipeline.Asset{Name: "t", Type: pipeline.AssetTypeDuckDBQuery, Connection: "db", ExecutableFile: pipeline.ExecutableFile{Content: "select 1"}, Hooks: pipeline.Hooks{Pre: []pipeline.Hook{{Query: "delete from t"}}}, CustomChecks: []pipeline.CustomCheck{{Query: "select count(*) from t"}}}
	requirements, err := assetAccessRequirements(pl, asset)
	require.NoError(t, err)
	require.Contains(t, requirements, policy.Requirement{Connection: "db", Effect: policy.Unknown, Operation: "sql_hook"})
	require.Contains(t, requirements, policy.Requirement{Connection: "db", Effect: policy.Read, Operation: "custom_check"})
	asset.Type = pipeline.AssetTypeIngestr
	asset.Parameters = pipeline.ParameterMap{"source_connection": "external", "destination": "duckdb"}
	requirements, err = assetAccessRequirements(pl, asset)
	require.NoError(t, err)
	require.Contains(t, requirements, policy.Requirement{Connection: "external", Effect: policy.Unknown, Operation: "ingestr_source"})
}
