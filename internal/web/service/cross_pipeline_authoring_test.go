package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/authoringdiag"
	"renart/internal/sqllsp"
	"renart/internal/web/model"
)

func crossPipelineAuthoringState() model.WorkspaceState {
	return model.WorkspaceState{
		Pipelines: []model.Pipeline{
			{
				ID:   "producer-pipeline",
				Name: "raw",
				Assets: []model.Asset{{
					ID:         "producer-asset",
					Name:       "raw.orders",
					URI:        "duckdb://warehouse/raw/orders",
					Type:       "duckdb.sql",
					Path:       "raw/assets/orders.sql",
					Content:    "select 1::bigint as id",
					Connection: "duckdb-default",
				}},
			},
			{
				ID:   "consumer-pipeline",
				Name: "analytics",
				Assets: []model.Asset{{
					ID:         "consumer-asset",
					Name:       "analytics.report",
					Type:       "duckdb.sql",
					Path:       "analytics/assets/report.sql",
					Content:    "select * from raw.orders",
					Connection: "duckdb-default",
				}},
			},
		},
	}
}

func TestCrossPipelineAuthoringReferenceOffersExplicitDependencyModes(t *testing.T) {
	state := crossPipelineAuthoringState()
	doc := sqllsp.TextDocumentItem{URI: "file:///analytics/report.sql", Text: "select * from raw.orders"}
	references := crossPipelineAuthoringReferences(
		state,
		"consumer-asset",
		sqllsp.NewEngine(sqllsp.CanonicalGraph{}),
		doc,
	)

	require.Len(t, references, 1)
	reference := references[0]
	assert.Equal(t, crossPipelineReferenceDeclarable, reference.Status)
	diagnostic := reference.diagnostic()
	assert.Equal(t, authoringdiag.CodeCrossPipelineDependencyMissing, diagnostic.Code)
	assert.Equal(t, 2, diagnostic.Severity)
	actions := reference.codeActions(diagnostic)
	require.Len(t, actions, 2)
	require.NotNil(t, actions[0].Action)
	assert.Equal(t, sqlLSPActionDeclareCrossPipelineDependency, actions[0].Action.Type)
	assert.Equal(t, "consumer-asset", actions[0].Action.AssetID)
	assert.Equal(t, "duckdb://warehouse/raw/orders", actions[0].Action.URI)
	assert.Equal(t, "full", actions[0].Action.Mode)
	assert.True(t, actions[0].IsPreferred)
	assert.Equal(t, "symbolic", actions[1].Action.Mode)

	resolutions := reference.typeCheckResolutions()
	require.Len(t, resolutions, 2)
	require.NotNil(t, resolutions[0].Transaction)
	require.NotNil(t, resolutions[0].Transaction.Dependency)
	assert.Equal(t, TxDependencyManualAdd, resolutions[0].Transaction.Type)
	assert.Equal(t, "full", resolutions[0].Transaction.Dependency.Mode)
}

func TestCrossPipelineAuthoringReferenceRespectsDeclaredAndLocalDependencies(t *testing.T) {
	t.Run("declared URI", func(t *testing.T) {
		state := crossPipelineAuthoringState()
		state.Pipelines[1].Assets[0].Dependencies = []model.AssetDependency{{
			Type:  "uri",
			Value: "duckdb://warehouse/raw/orders",
			Mode:  "symbolic",
		}}
		references := crossPipelineAuthoringReferences(
			state,
			"consumer-asset",
			sqllsp.NewEngine(sqllsp.CanonicalGraph{}),
			sqllsp.TextDocumentItem{Text: "select * from raw.orders"},
		)
		assert.Empty(t, references)
	})

	t.Run("pipeline local relation wins", func(t *testing.T) {
		state := crossPipelineAuthoringState()
		state.Pipelines[1].Assets = append(state.Pipelines[1].Assets, model.Asset{
			ID:   "local-orders",
			Name: "raw.orders",
		})
		references := crossPipelineAuthoringReferences(
			state,
			"consumer-asset",
			sqllsp.NewEngine(sqllsp.CanonicalGraph{}),
			sqllsp.TextDocumentItem{Text: "select * from raw.orders"},
		)
		assert.Empty(t, references)
	})
}

func TestCrossPipelineAuthoringReferenceExplainsNonAutomatableMatches(t *testing.T) {
	t.Run("producer URI missing", func(t *testing.T) {
		state := crossPipelineAuthoringState()
		state.Pipelines[0].Assets[0].URI = ""
		reference := crossPipelineAuthoringReferences(
			state,
			"consumer-asset",
			sqllsp.NewEngine(sqllsp.CanonicalGraph{}),
			sqllsp.TextDocumentItem{Text: "select * from raw.orders"},
		)[0]
		assert.Equal(t, crossPipelineReferenceProducerURIMissing, reference.Status)
		actions := reference.codeActions(reference.diagnostic())
		require.Len(t, actions, 1)
		assert.Equal(t, sqlLSPActionOpenAsset, actions[0].Action.Type)
		assert.Equal(t, "producer-pipeline", actions[0].Action.PipelineID)
		assert.Equal(t, "producer-asset", actions[0].Action.AssetID)
	})

	t.Run("connection mismatch", func(t *testing.T) {
		state := crossPipelineAuthoringState()
		state.Pipelines[0].Assets[0].Connection = "duckdb-other"
		reference := crossPipelineAuthoringReferences(
			state,
			"consumer-asset",
			sqllsp.NewEngine(sqllsp.CanonicalGraph{}),
			sqllsp.TextDocumentItem{Text: "select * from raw.orders"},
		)[0]
		assert.Equal(t, crossPipelineReferenceConnectionMismatch, reference.Status)
		assert.Empty(t, reference.codeActions(reference.diagnostic()))
		assert.Contains(t, reference.diagnostic().Message, "duckdb-other")
	})

	t.Run("ambiguous short name", func(t *testing.T) {
		state := crossPipelineAuthoringState()
		state.Pipelines = append(state.Pipelines, model.Pipeline{
			ID:   "archive-pipeline",
			Name: "archive",
			Assets: []model.Asset{{
				ID:   "archive-orders",
				Name: "archive.orders",
			}},
		})
		reference := crossPipelineAuthoringReferences(
			state,
			"consumer-asset",
			sqllsp.NewEngine(sqllsp.CanonicalGraph{}),
			sqllsp.TextDocumentItem{Text: "select * from orders"},
		)[0]
		assert.Equal(t, crossPipelineReferenceAmbiguous, reference.Status)
		assert.Equal(t, authoringdiag.CodeCrossPipelineRelationAmbiguous, reference.diagnostic().Code)
		assert.Empty(t, reference.codeActions(reference.diagnostic()))
	})
}

func TestSQLLSPCrossPipelineDiagnosticsAndActionsAreAssetOnly(t *testing.T) {
	state := crossPipelineAuthoringState()
	service := NewSQLLSPService(SQLLSPDependencies{
		WorkspaceRoot: t.TempDir(),
		CurrentState:  func() model.WorkspaceState { return state },
	})
	request := SQLLSPRequest{
		AssetID:         "consumer-asset",
		Content:         "select * from raw.orders",
		DocumentContext: "asset",
	}

	diagnostics, apiErr := service.Diagnostics(context.Background(), request)
	require.Nil(t, apiErr)
	assert.True(t, hasSQLLSPDiagnosticCode(diagnostics.Diagnostics, authoringdiag.CodeCrossPipelineDependencyMissing))
	actions, apiErr := service.CodeActions(context.Background(), request)
	require.Nil(t, apiErr)
	var dependencyActions int
	for _, action := range actions.CodeActions {
		if action.Action != nil && action.Action.Type == sqlLSPActionDeclareCrossPipelineDependency {
			dependencyActions++
		}
	}
	assert.Equal(t, 2, dependencyActions)

	request.DocumentContext = "adhoc"
	adhoc, apiErr := service.Diagnostics(context.Background(), request)
	require.Nil(t, apiErr)
	assert.False(t, hasSQLLSPDiagnosticCode(adhoc.Diagnostics, authoringdiag.CodeCrossPipelineDependencyMissing))
}

func hasSQLLSPDiagnosticCode(diagnostics []sqllsp.Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func TestInteractiveTypeCheckReportsProvisionalCrossPipelineReference(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".bruin.yml"), []byte(`default_environment: default
environments:
  default:
    connections:
      duckdb:
        - name: duckdb-default
          path: local.db
`), 0o644))
	writeDependencyGraphPipeline(t, root, "raw", "00000000-0000-0000-0000-000000000041", "raw", map[string]string{
		"orders.sql": `/* @bruin
name: raw.orders
uri: duckdb://warehouse/raw/orders
type: duckdb.sql
connection: duckdb-default
columns:
  - name: id
    type: BIGINT
@bruin */
select 1::bigint as id
`,
	})
	writeDependencyGraphPipeline(t, root, "analytics", "00000000-0000-0000-0000-000000000042", "analytics", map[string]string{
		"report.sql": `/* @bruin
name: analytics.report
type: duckdb.sql
connection: duckdb-default
@bruin */
select id from raw.orders
`,
	})

	report, apiErr := NewPipelineService(root).TypeCheck(context.Background(), EncodeID("analytics"), "", "")
	require.Nil(t, apiErr)
	asset := findAsset(t, report, "analytics.report")
	finding := findTypeCheckFindingByCode(asset.Findings, authoringdiag.CodeCrossPipelineDependencyMissing)
	require.NotNil(t, finding)
	require.Len(t, finding.Resolutions, 2)
	require.Len(t, report.CrossPipelineReferences, 1)
	reference := report.CrossPipelineReferences[0]
	assert.Equal(t, "declarable", reference.Status)
	assert.Equal(t, "raw.orders", reference.ProducerAssetName)
	assert.Equal(t, "raw", reference.ProducerPipelineName)
	assert.Equal(t, "analytics.report", reference.ConsumerAssetName)
}
