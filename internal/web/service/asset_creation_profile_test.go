package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newAssetCreationProfileTestService(t *testing.T, pipelineYAML, configYAML string) (*AssetService, string) {
	t.Helper()
	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(pipelineRoot, "assets", "analytics"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(pipelineYAML), 0o644))
	configPath := filepath.Join(workspaceRoot, ".bruin.yml")
	require.NoError(t, os.WriteFile(configPath, []byte(configYAML), 0o600))
	resolver := newAssetTestResolver(workspaceRoot)
	return NewAssetService(AssetDependencies{
		WorkspaceRoot:                workspaceRoot,
		ConfigPath:                   configPath,
		ResolveAssetByID:             resolver.ResolveAssetByID,
		DefaultAssetContent:          DefaultAssetContent,
		DerivedAssetContent:          DefaultDerivedSQLAssetContent,
		EnsurePythonProject:          func(string, string, string) error { return nil },
		SuppressWatcher:              func(string) {},
		PushWorkspaceUpdateImmediate: func(context.Context, string, string) {},
		PushWorkspaceUpdateImmediateWithChangedIDs: func(context.Context, string, string, []string) {},
		SelectedEnvironment:                        func() string { return "dev" },
	}), pipelineRoot
}

const assetCreationProfileTestConfig = `default_environment: dev
environments:
  dev:
    connections:
      duckdb:
        - name: warehouse
          path: warehouse.duckdb
      postgres:
        - name: reporting
          host: localhost
          port: 5432
          username: renart
          password: renart
          database: analytics
      doris:
        - name: partial-engine
          host: localhost
          port: 9030
          username: renart
          password: renart
          database: analytics
      s3:
        - name: object-store
          bucket_name: demo
  prod:
    connections:
      duckdb:
        - name: reporting
          path: prod.duckdb
`

func TestAssetCreationProfileDerivesSupportedTypesAndPortabilityWarnings(t *testing.T) {
	t.Parallel()
	service, _ := newAssetCreationProfileTestService(t, `name: analytics
default_connections:
  postgres: reporting
`, assetCreationProfileTestConfig)

	profile, apiErr := service.AssetCreationProfile(context.Background(), EncodeID("analytics"), "dev")
	require.Nil(t, apiErr)
	assert.Equal(t, "dev", profile.Environment)

	sqlProfile, ok := findAssetCreationKindProfile(profile, assetCreationKindSQL)
	require.True(t, ok)
	sqlTarget, ok := findAssetCreationRoleProfile(sqlProfile, assetCreationRoleTarget)
	require.True(t, ok)
	assert.Equal(t, "resolved", sqlTarget.Default.Status)
	assert.Equal(t, "reporting", sqlTarget.Default.Connection)
	assert.Equal(t, "pg.sql", sqlTarget.Default.Candidates[0].AssetType)

	connections := map[string]AssetCreationConnection{}
	for _, connection := range sqlTarget.Connections {
		connections[connection.Name] = connection
	}
	assert.Equal(t, "duckdb.sql", connections["warehouse"].Candidates[0].AssetType)
	assert.Equal(t, "postgres", connections["reporting"].Candidates[0].Dialect)
	assert.NotContains(t, connections, "partial-engine")
	require.Len(t, connections["reporting"].PortabilityWarnings, 1)
	assert.Equal(t, "type_mismatch", connections["reporting"].PortabilityWarnings[0].Code)

	connectionTypeNames := make([]string, 0, len(sqlTarget.ConnectionTypes))
	for _, connectionType := range sqlTarget.ConnectionTypes {
		connectionTypeNames = append(connectionTypeNames, connectionType.TypeName)
	}
	assert.Contains(t, connectionTypeNames, "duckdb")
	assert.Contains(t, connectionTypeNames, "postgres")
	assert.NotContains(t, connectionTypeNames, "doris")
	assert.Equal(t, "duckdb.sql", sqlTarget.ConnectionTypeCandidates["duckdb"][0].AssetType)
	assert.Equal(t, "pg.sql", sqlTarget.ConnectionTypeCandidates["postgres"][0].AssetType)

	loadProfile, _ := findAssetCreationKindProfile(profile, assetCreationKindLoad)
	loadSource, _ := findAssetCreationRoleProfile(loadProfile, assetCreationRoleSource)
	assert.Equal(t, "not_applicable", loadSource.Default.Status)
	assert.Contains(t, assetCreationConnectionNames(loadSource.Connections), "local")
	assert.Contains(t, assetCreationConnectionNames(loadSource.Connections), "object-store")

	apiProfile, _ := findAssetCreationKindProfile(profile, assetCreationKindAPI)
	apiTarget, _ := findAssetCreationRoleProfile(apiProfile, assetCreationRoleTarget)
	assert.NotContains(t, assetCreationConnectionNames(apiTarget.Connections), "object-store")
}

func TestAssetCreationProfileLoadsAPIAssetWithTypedDependency(t *testing.T) {
	t.Parallel()
	service, pipelineRoot := newAssetCreationProfileTestService(t, `name: analytics
default_connections:
  duckdb: warehouse
`, assetCreationProfileTestConfig)
	require.NoError(t, os.WriteFile(
		filepath.Join(pipelineRoot, "assets", "analytics", "weather.asset.yml"),
		[]byte(`type: api
parameters:
  request:
    url: https://api.weather.gov/alerts
  response:
    records_path: features
depends:
  - asset: analytics.orders
    mode: symbolic
`),
		0o644,
	))

	_, apiErr := service.AssetCreationProfile(context.Background(), EncodeID("analytics"), "dev")
	require.Nil(t, apiErr)
}

func TestAssetCreationProfileReportsAmbiguousCompatibleDefaults(t *testing.T) {
	t.Parallel()
	service, _ := newAssetCreationProfileTestService(t, `name: analytics
default_connections:
  duckdb: warehouse
  postgres: reporting
`, assetCreationProfileTestConfig)

	profile, apiErr := service.AssetCreationProfile(context.Background(), EncodeID("analytics"), "dev")
	require.Nil(t, apiErr)
	sqlProfile, _ := findAssetCreationKindProfile(profile, assetCreationKindSQL)
	target, _ := findAssetCreationRoleProfile(sqlProfile, assetCreationRoleTarget)
	assert.Equal(t, "ambiguous", target.Default.Status)
	assert.Contains(t, target.Default.Reason, "reporting, warehouse")
}

func TestAssetServiceSemanticCreateDerivesSQLTypeAndConnection(t *testing.T) {
	t.Parallel()
	service, pipelineRoot := newAssetCreationProfileTestService(t, `name: analytics
default_connections:
  postgres: reporting
`, assetCreationProfileTestConfig)

	response, apiErr := service.Create(context.Background(), EncodeID("analytics"), CreateAssetParams{
		Name:        "analytics.orders",
		Kind:        assetCreationKindSQL,
		Connection:  "reporting",
		Environment: "dev",
	})
	require.Nil(t, apiErr)
	assert.Equal(t, "pg.sql", response.AssetType)
	assert.Equal(t, "reporting", response.Connection)
	assert.Equal(t, "postgres", response.Dialect)

	content, err := os.ReadFile(filepath.Join(pipelineRoot, "assets", "analytics", "orders.sql"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "type: pg.sql")
	assert.Contains(t, string(content), "connection: reporting")
}

func TestAssetServiceSemanticCreatePersistsResolvedDefaultWithoutOverride(t *testing.T) {
	t.Parallel()
	service, pipelineRoot := newAssetCreationProfileTestService(t, `name: analytics
default_connections:
  postgres: reporting
`, assetCreationProfileTestConfig)

	response, apiErr := service.Create(context.Background(), EncodeID("analytics"), CreateAssetParams{
		Name:               "analytics.orders",
		Kind:               assetCreationKindSQL,
		Environment:        "dev",
		UsePipelineDefault: true,
	})
	require.Nil(t, apiErr)
	assert.Equal(t, "pg.sql", response.AssetType)
	assert.Equal(t, "reporting", response.Connection)

	content, err := os.ReadFile(filepath.Join(pipelineRoot, "assets", "analytics", "orders.sql"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "type: pg.sql")
	assert.NotContains(t, string(content), "connection:")
}

func TestAssetServiceConnectionSelectionMigratesTypeAndConnectionAtomically(t *testing.T) {
	t.Parallel()
	service, pipelineRoot := newAssetCreationProfileTestService(t, `name: analytics
default_connections:
  postgres: reporting
`, assetCreationProfileTestConfig)

	created, apiErr := service.Create(context.Background(), EncodeID("analytics"), CreateAssetParams{
		Name:        "analytics.orders",
		Kind:        assetCreationKindSQL,
		Connection:  "reporting",
		Environment: "dev",
	})
	require.Nil(t, apiErr)

	useDefault := AssetConnectionSelectionRequest{
		Environment:        "dev",
		UsePipelineDefault: true,
		ExpectedAssetType:  "pg.sql",
	}
	defaulted, apiErr := service.Update(context.Background(), created.AssetID, AssetUpdateRequest{
		ConnectionSelection: &useDefault,
	})
	require.Nil(t, apiErr)
	assert.Equal(t, "pg.sql", defaulted.AssetType)
	assert.Equal(t, "reporting", defaulted.Connection)
	defaultedContent, err := os.ReadFile(filepath.Join(pipelineRoot, "assets", "analytics", "orders.sql"))
	require.NoError(t, err)
	assert.NotContains(t, string(defaultedContent), "connection:")

	selection := AssetConnectionSelectionRequest{
		Environment:       "dev",
		Connection:        "warehouse",
		ExpectedAssetType: "pg.sql",
	}
	_, apiErr = service.Update(context.Background(), created.AssetID, AssetUpdateRequest{
		ConnectionSelection: &selection,
	})
	require.NotNil(t, apiErr)
	assert.Equal(t, "asset_type_migration_required", apiErr.Code)

	assetPath := filepath.Join(pipelineRoot, "assets", "analytics", "orders.sql")
	unchanged, err := os.ReadFile(assetPath)
	require.NoError(t, err)
	assert.Contains(t, string(unchanged), "type: pg.sql")
	assert.NotContains(t, string(unchanged), "connection:")

	selection.ConfirmTypeMigration = true
	updated, apiErr := service.Update(context.Background(), created.AssetID, AssetUpdateRequest{
		ConnectionSelection: &selection,
	})
	require.Nil(t, apiErr)
	assert.Equal(t, "duckdb.sql", updated.AssetType)
	assert.Equal(t, "warehouse", updated.Connection)
	assert.Equal(t, "duckdb", updated.Dialect)

	content, err := os.ReadFile(assetPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "type: duckdb.sql")
	assert.Contains(t, string(content), "connection: warehouse")
	assert.NotContains(t, string(content), "type: pg.sql")
}

func TestAssetServiceConnectionSelectionRejectsStaleTypeAndDirectTypeChanges(t *testing.T) {
	t.Parallel()
	service, _ := newAssetCreationProfileTestService(t, `name: analytics
default_connections:
  postgres: reporting
`, assetCreationProfileTestConfig)

	created, apiErr := service.Create(context.Background(), EncodeID("analytics"), CreateAssetParams{
		Name:        "analytics.orders",
		Kind:        assetCreationKindSQL,
		Connection:  "reporting",
		Environment: "dev",
	})
	require.Nil(t, apiErr)

	staleSelection := AssetConnectionSelectionRequest{
		Environment:          "dev",
		Connection:           "warehouse",
		ExpectedAssetType:    "duckdb.sql",
		ConfirmTypeMigration: true,
	}
	_, apiErr = service.Update(context.Background(), created.AssetID, AssetUpdateRequest{
		ConnectionSelection: &staleSelection,
	})
	require.NotNil(t, apiErr)
	assert.Equal(t, "asset_type_changed", apiErr.Code)

	directType := "duckdb.sql"
	_, apiErr = service.Update(context.Background(), created.AssetID, AssetUpdateRequest{Type: &directType})
	require.NotNil(t, apiErr)
	assert.Equal(t, "asset_type_change_requires_migration", apiErr.Code)

	directConnection := "warehouse"
	_, apiErr = service.Update(context.Background(), created.AssetID, AssetUpdateRequest{Connection: &directConnection})
	require.NotNil(t, apiErr)
	assert.Equal(t, "asset_type_connection_mismatch", apiErr.Code)
}

func TestSelectAssetConnectionCandidatePreservesSeedAndSensorVariant(t *testing.T) {
	t.Parallel()
	candidates := []AssetCreationCandidate{
		{Variant: "file", AssetType: "duckdb.seed", Operator: "seed"},
		{Variant: "query", AssetType: "duckdb.sensor.query", Operator: "sensor"},
		{Variant: "table", AssetType: "duckdb.sensor.table", Operator: "sensor"},
	}

	tests := []struct {
		name        string
		currentType string
		wantType    string
	}{
		{name: "seed", currentType: "pg.seed", wantType: "duckdb.seed"},
		{name: "query sensor", currentType: "pg.sensor.query", wantType: "duckdb.sensor.query"},
		{name: "table sensor", currentType: "pg.sensor.table", wantType: "duckdb.sensor.table"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate, apiErr := selectAssetConnectionCandidateForExistingType(tt.currentType, candidates)
			require.Nil(t, apiErr)
			assert.Equal(t, tt.wantType, candidate.AssetType)
		})
	}

	_, apiErr := selectAssetConnectionCandidateForExistingType("s3.sensor.key_sensor", candidates)
	require.NotNil(t, apiErr)
	assert.Equal(t, "incompatible_connection", apiErr.Code)
}

func TestAssetServiceSemanticCreateCanonicalizesAPITypeAndConnection(t *testing.T) {
	t.Parallel()
	service, pipelineRoot := newAssetCreationProfileTestService(t, `name: analytics
default_connections:
  postgres: reporting
`, assetCreationProfileTestConfig)

	response, apiErr := service.Create(context.Background(), EncodeID("analytics"), CreateAssetParams{
		Name:        "analytics.events",
		Kind:        assetCreationKindAPI,
		Connection:  "reporting",
		Environment: "dev",
		Content: `type: ingestr
connection: warehouse
parameters:
  request:
    url: https://example.test/events
`,
	})
	require.Nil(t, apiErr)
	assert.Equal(t, apiAssetType, response.AssetType)
	assert.Equal(t, "reporting", response.Connection)

	content, err := os.ReadFile(filepath.Join(pipelineRoot, "assets", "analytics", "events.asset.yml"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "type: api")
	assert.Contains(t, string(content), "connection: reporting")
	assert.NotContains(t, string(content), "type: ingestr")
	assert.NotContains(t, string(content), "connection: warehouse")
	assert.Contains(t, string(content), "url: https://example.test/events")
}

func TestAssetServiceSemanticCreateRequiresExplicitPipelineDefaultChoice(t *testing.T) {
	t.Parallel()
	service, _ := newAssetCreationProfileTestService(t, `name: analytics
default_connections:
  postgres: reporting
`, assetCreationProfileTestConfig)

	_, apiErr := service.Create(context.Background(), EncodeID("analytics"), CreateAssetParams{
		Name:        "analytics.orders",
		Kind:        assetCreationKindSQL,
		Environment: "dev",
	})
	require.NotNil(t, apiErr)
	assert.Equal(t, "missing_connection_choice", apiErr.Code)

	_, apiErr = service.Create(context.Background(), EncodeID("analytics"), CreateAssetParams{
		Name:               "analytics.orders",
		Kind:               assetCreationKindSQL,
		Connection:         "reporting",
		UsePipelineDefault: true,
		Environment:        "dev",
	})
	require.NotNil(t, apiErr)
	assert.Equal(t, "conflicting_connection_choice", apiErr.Code)
}

func TestAssetServiceLegacyCreateRejectsTypeConnectionMismatch(t *testing.T) {
	t.Parallel()
	service, _ := newAssetCreationProfileTestService(t, `name: analytics
default_connections:
  postgres: reporting
`, assetCreationProfileTestConfig)

	_, apiErr := service.Create(context.Background(), EncodeID("analytics"), CreateAssetParams{
		Name:        "analytics.orders",
		Type:        "duckdb.sql",
		Connection:  "reporting",
		Environment: "dev",
	})
	require.NotNil(t, apiErr)
	assert.Equal(t, "asset_type_connection_mismatch", apiErr.Code)
}

func assetCreationConnectionNames(connections []AssetCreationConnection) []string {
	names := make([]string, 0, len(connections))
	for _, connection := range connections {
		names = append(names, connection.Name)
	}
	return names
}
