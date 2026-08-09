package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/dependencygraph"
	"renart/internal/web/model"
)

func writeDependencyGraphPipeline(t *testing.T, root, directory, id, name string, assets map[string]string) {
	t.Helper()
	pipelineRoot := filepath.Join(root, directory)
	assetsRoot := filepath.Join(pipelineRoot, "assets")
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(pipelineRoot, "pipeline.yml"),
		[]byte("id: "+id+"\nname: "+name+"\n"),
		0o644,
	))
	for filename, content := range assets {
		path := filepath.Join(assetsRoot, filename)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
}

func workspaceAssetByName(t *testing.T, pipeline model.Pipeline, name string) model.Asset {
	t.Helper()
	for _, asset := range pipeline.Assets {
		if asset.Name == name {
			return asset
		}
	}
	t.Fatalf("asset %q not found in pipeline %q", name, pipeline.Name)
	return model.Asset{}
}

func TestWorkspaceStateResolvesCrossPipelineURIDependenciesByAssetID(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	writeDependencyGraphPipeline(t, root, "raw", "00000000-0000-0000-0000-000000000001", "raw", map[string]string{
		"orders.sql": `/* @bruin
name: shared.orders
uri: duckdb://warehouse/raw/orders
type: duckdb.sql
columns:
  - name: id
    type: BIGINT
@bruin */
select 1::bigint as id
`,
	})
	writeDependencyGraphPipeline(t, root, "analytics", "00000000-0000-0000-0000-000000000002", "analytics", map[string]string{
		"orders.sql": `/* @bruin
name: shared.orders
type: duckdb.sql
@bruin */
select 2 as id
`,
		"daily.sql": `/* @bruin
name: analytics.daily
type: duckdb.sql
depends:
  - uri: duckdb://warehouse/raw/orders
@bruin */
select id from shared.orders
`,
	})

	state, err := NewWorkspaceService(root, "").ComputeState(context.Background())
	require.NoError(t, err)
	require.Len(t, state.Pipelines, 2)
	assert.NotEmpty(t, state.DependencyGraphRevision)
	assert.Empty(t, state.DependencyDiagnostics)

	var rawPipeline, analyticsPipeline model.Pipeline
	for _, pipeline := range state.Pipelines {
		switch pipeline.Name {
		case "raw":
			rawPipeline = pipeline
		case "analytics":
			analyticsPipeline = pipeline
		}
	}
	producer := workspaceAssetByName(t, rawPipeline, "shared.orders")
	consumer := workspaceAssetByName(t, analyticsPipeline, "analytics.daily")
	require.Len(t, consumer.Dependencies, 1)
	dependency := consumer.Dependencies[0]
	assert.Equal(t, "uri", dependency.Type)
	assert.Equal(t, "full", dependency.Mode)
	assert.Equal(t, producer.ID, dependency.ResolvedAssetID)
	assert.Equal(t, rawPipeline.ID, dependency.ResolvedPipelineID)
	assert.Equal(t, "raw", dependency.ResolvedPipeline)
}

func TestWorkspaceStateSurfacesFullAndSymbolicURIResolutionDiagnostics(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	writeDependencyGraphPipeline(t, root, "consumer", "00000000-0000-0000-0000-000000000003", "consumer", map[string]string{
		"full.sql": `/* @bruin
name: consumer.full
type: duckdb.sql
depends:
  - uri: warehouse://missing/full
@bruin */
select 1
`,
		"symbolic.sql": `/* @bruin
name: consumer.symbolic
type: duckdb.sql
depends:
  - uri: warehouse://missing/symbolic
    mode: symbolic
@bruin */
select 1
`,
	})

	state, err := NewWorkspaceService(root, "").ComputeState(context.Background())
	require.NoError(t, err)
	byCodeAndSeverity := make(map[string]int)
	for _, diagnostic := range state.DependencyDiagnostics {
		byCodeAndSeverity[diagnostic.Code+"/"+diagnostic.Severity]++
	}
	assert.Equal(t, 1, byCodeAndSeverity[dependencygraph.CodeUnresolvedURI+"/error"])
	assert.Equal(t, 1, byCodeAndSeverity[dependencygraph.CodeUnresolvedURI+"/warning"])
}

func TestTypeCheckUsesSiblingPipelineSchemasAndWorkspaceDependencyDiagnostics(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
	writeDependencyGraphPipeline(t, root, "raw", "00000000-0000-0000-0000-000000000004", "raw", map[string]string{
		"orders.sql": `/* @bruin
name: raw.orders
uri: duckdb://warehouse/raw/orders
type: duckdb.sql
columns:
  - name: id
    type: BIGINT
@bruin */
select 1::bigint as id
`,
	})
	writeDependencyGraphPipeline(t, root, "analytics", "00000000-0000-0000-0000-000000000005", "analytics", map[string]string{
		"valid.sql": `/* @bruin
name: analytics.valid
type: duckdb.sql
depends:
  - uri: duckdb://warehouse/raw/orders
@bruin */
select id from raw.orders
`,
		"invalid.sql": `/* @bruin
name: analytics.invalid
type: duckdb.sql
depends:
  - uri: duckdb://warehouse/missing
@bruin */
select 1
`,
	})

	ctx := context.Background()
	state, err := NewWorkspaceService(root, "").ComputeState(ctx)
	require.NoError(t, err)
	workspaceGraph := buildWorkspaceCanonicalGraph(ctx, root, state)
	parsed, err := NewRenartPipelineBuilder(afero.NewOsFs()).CreatePipelineFromPath(
		ctx,
		filepath.Join(root, "analytics"),
		pipeline.WithMutate(),
	)
	require.NoError(t, err)
	tw, err := ResolveExecutionTimeWindow(string(parsed.Schedule), "", "", time.Now().UTC())
	require.NoError(t, err)
	report := checkPipelineAt(ctx, afero.NewOsFs(), parsed, root, tw, time.Now().UTC(), typeCheckOptions{
		WorkspaceGraph:        &workspaceGraph,
		DependencyDiagnostics: state.DependencyDiagnostics,
	})

	valid := findAsset(t, report, "analytics.valid")
	for _, finding := range valid.Findings {
		assert.NotEqual(t, "unresolved-relation", finding.Code, "sibling producer should be in the schema: %s", finding.Message)
	}
	invalid := findAsset(t, report, "analytics.invalid")
	require.True(t, hasFindingCode(invalid, dependencygraph.CodeUnresolvedURI, "error"))
}

func hasFindingCode(asset TypeCheckAsset, code, severity string) bool {
	for _, finding := range asset.Findings {
		if finding.Code == code && finding.Severity == severity {
			return true
		}
	}
	return false
}
