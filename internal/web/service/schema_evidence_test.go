package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	webmodel "renart/internal/web/model"
)

func TestResolveSchemaEvidencePartitionsIncomparableObservations(t *testing.T) {
	currentScope := SchemaEvidenceScope{Environment: "dev", Connection: "warehouse", Relation: "analytics.orders"}
	definition := SchemaEvidence{
		Source: webmodel.ColumnInferenceSource{ID: columnSourceDefinition},
		Stage:  SchemaStageDeclaration, Scope: currentScope, AssetRevision: "current", OutputIdentity: "current-output",
	}
	stale := false
	tests := []struct {
		name           string
		observed       SchemaEvidence
		classification string
	}{
		{
			name: "stale materialization",
			observed: SchemaEvidence{
				Source: webmodel.ColumnInferenceSource{ID: columnSourceMaterialized},
				Stage:  SchemaStageMaterialized, Scope: currentScope, Fresh: &stale,
			},
			classification: "stale",
		},
		{
			name: "different environment",
			observed: SchemaEvidence{
				Source: webmodel.ColumnInferenceSource{ID: columnSourceMaterialized},
				Stage:  SchemaStageMaterialized,
				Scope:  SchemaEvidenceScope{Environment: "prod", Connection: "warehouse", Relation: "analytics.orders"},
			},
			classification: "scoped",
		},
		{
			name: "different revision",
			observed: SchemaEvidence{
				Source: webmodel.ColumnInferenceSource{ID: columnSourceLiveResponse},
				Stage:  SchemaStageRuntime, Scope: currentScope, AssetRevision: "old", OutputIdentity: "current-output",
			},
			classification: "stale",
		},
		{
			name: "different output",
			observed: SchemaEvidence{
				Source: webmodel.ColumnInferenceSource{ID: columnSourceMaterialized},
				Stage:  SchemaStageMaterialized, Scope: currentScope, AssetRevision: "current", OutputIdentity: "other-output",
			},
			classification: "scoped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved := resolveSchemaEvidence([]SchemaEvidence{definition, tt.observed}, currentScope)
			require.Len(t, resolved.Comparable, 1)
			require.Len(t, resolved.Excluded, 1)
			assert.Equal(t, tt.classification, resolved.Excluded[0].Classification)
			assert.NotEmpty(t, resolved.Excluded[0].Reason)
		})
	}
}

func TestResolveSchemaEvidenceKeepsPartialAndUnanchoredHistoricalEvidence(t *testing.T) {
	scope := SchemaEvidenceScope{Environment: "dev", Connection: "warehouse", Relation: "analytics.api"}
	partial := SchemaEvidence{
		Source: webmodel.ColumnInferenceSource{ID: columnSourceLiveResponse},
		Stage:  SchemaStageRuntime, Scope: scope, Completeness: SchemaPartial,
	}
	resolved := resolveSchemaEvidence([]SchemaEvidence{partial}, scope)
	require.Len(t, resolved.Comparable, 1)
	assert.Empty(t, resolved.Excluded)

	stale := false
	historicalOnly := SchemaEvidence{
		Source: webmodel.ColumnInferenceSource{ID: columnSourceMaterialized},
		Stage:  SchemaStageMaterialized, Scope: scope, Fresh: &stale,
	}
	resolved = resolveSchemaEvidence([]SchemaEvidence{historicalOnly}, scope)
	require.Len(t, resolved.Comparable, 1, "an observed source may seed a schema when no declaration exists")
}

func TestSchemaEvidenceAccessFailsClosed(t *testing.T) {
	apiErr := requireSchemaEvidenceAccess(
		SchemaEvidenceAccess{Network: true, Warehouse: true},
		SchemaEvidenceAccess{Network: true},
	)
	require.NotNil(t, apiErr)
	assert.Equal(t, "schema_evidence_access_denied", apiErr.Code)
	assert.Contains(t, apiErr.Message, "warehouse")
}

func TestPartialSchemaEvidenceCannotRemoveDeclaredColumns(t *testing.T) {
	contract := []pipeline.Column{{Name: "id", Type: "BIGINT"}, {Name: "optional_name", Type: "VARCHAR"}}
	evidence := SchemaEvidence{
		Source:       webmodel.ColumnInferenceSource{ID: columnSourceLiveResponse},
		Stage:        SchemaStageRuntime,
		Completeness: SchemaPartial,
		Columns:      []WorkspaceColumn{{Name: "id", Type: "BIGINT"}},
	}
	drift := compareContractWithEvidence(contract, evidence)
	assert.Zero(t, drift.Removed)
	assert.Empty(t, drift.Items)

	evidence.Completeness = SchemaComplete
	drift = compareContractWithEvidence(contract, evidence)
	assert.Equal(t, 1, drift.Removed)
	require.Len(t, drift.Items, 1)
	assert.Equal(t, "optional_name", drift.Items[0].Column)
}

func TestSchemaEvidenceOutputIdentityUsesExactPhysicalTarget(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, ".bruin.yml")
	require.NoError(t, os.WriteFile(configPath, []byte(`default_environment: default
environments:
  default:
    connections:
      duckdb:
        - name: warehouse
          path: warehouse.duckdb
`), 0o644))
	asset := &pipeline.Asset{
		Name: "analytics.orders", Type: pipeline.AssetTypeDuckDBQuery, Connection: "warehouse",
		Materialization: pipeline.Materialization{Type: pipeline.MaterializationTypeTable},
	}
	pp := &pipeline.Pipeline{LegacyID: "pipeline-id", Assets: []*pipeline.Asset{asset}}
	service := NewAssetService(AssetDependencies{WorkspaceRoot: root, ConfigPath: configPath})

	outputIdentity := service.schemaEvidenceOutputIdentity(pp, asset, "default")
	assert.NotEmpty(t, outputIdentity)
	assert.NotContains(t, outputIdentity, "warehouse")

	unknown := NewAssetService(AssetDependencies{WorkspaceRoot: root, ConfigPath: filepath.Join(root, "missing.yml")})
	assert.Empty(t, unknown.schemaEvidenceOutputIdentity(pp, asset, "default"))
}
