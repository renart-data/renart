package service

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/authoringdiag"
)

func TestPipelineDependencyIssuesUseBruinDependencySemantics(t *testing.T) {
	t.Parallel()

	upstream := &pipeline.Asset{Name: "analytics.upstream", Type: pipeline.AssetTypeDuckDBQuery}
	reader := &pipeline.Asset{
		Name: "analytics.reader",
		Type: pipeline.AssetType("api"),
		Upstreams: []pipeline.Upstream{
			{Type: "asset", Value: upstream.Name},
			{Type: "uri", Value: "s3://external-bucket/orders"},
			{Type: "uri", Value: "s3://lineage-only/orders", Mode: pipeline.UpstreamModeSymbolic},
			{Type: "asset", Value: "analytics.missing"},
		},
	}
	pl := &pipeline.Pipeline{Name: "analytics", Assets: []*pipeline.Asset{upstream, reader}}

	issues, err := pipelineDependencyIssues(context.Background(), pl)
	require.NoError(t, err)
	require.Len(t, issues, 2)
	byCode := make(map[string]pipelineDependencyIssue, len(issues))
	for _, issue := range issues {
		byCode[issue.Code] = issue
	}
	assert.Same(t, reader, byCode[dependencyExistsRule].Asset)
	assert.Equal(t, "Dependency 'analytics.missing' does not exist", byCode[dependencyExistsRule].Description)
	assert.Contains(t, byCode[authoringdiag.CodeCrossPipelineExecutionPending].Description, "s3://external-bucket/orders")
}

func TestValidateDirectRunDependenciesPrintsBruinStyleReport(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	pipelineRoot := filepath.Join(root, "analytics")
	asset := &pipeline.Asset{
		Name:           "analytics.reader",
		Type:           pipeline.AssetType("load"),
		DefinitionFile: pipeline.TaskDefinitionFile{Path: filepath.Join(pipelineRoot, "assets", "reader.asset.yml")},
		Upstreams:      []pipeline.Upstream{{Type: "asset", Value: "analytics.missing"}},
	}
	pl := &pipeline.Pipeline{
		Name:           "analytics",
		DefinitionFile: pipeline.DefinitionFile{Path: filepath.Join(pipelineRoot, "pipeline.yml")},
		Assets:         []*pipeline.Asset{asset},
	}

	var output bytes.Buffer
	err := validateDirectRunDependencies(context.Background(), &output, pl, root)
	var validationErr pipelineDependencyValidationError
	require.ErrorAs(t, err, &validationErr)
	assert.Equal(t, 1, validationErr.count)

	plain := directAssetLogANSI.ReplaceAllString(output.String(), "")
	assert.Contains(t, plain, "Pipeline: analytics (analytics)")
	assert.Contains(t, plain, "analytics.reader (assets/reader.asset.yml)")
	assert.Contains(t, plain, "Dependency 'analytics.missing' does not exist (dependency-exists)")
	assert.Contains(t, plain, "Checked dependencies for 1 asset and found 1 issue")
}

func TestCheckPipelineReportsMissingDeclaredDependency(t *testing.T) {
	t.Parallel()

	parsed, root := writeTypeCheckWorkspace(t, "name: analytics", map[string]string{
		"reader.sql": `
/* @bruin
name: analytics.reader
type: duckdb.sql
materialization:
  type: view
depends:
  - analytics.missing
@bruin */
select 1 as id
`,
	})

	report := runTypeCheck(t, parsed, root)
	reader := findAsset(t, report, "analytics.reader")
	assert.Equal(t, typeCheckStatusError, reader.Status)
	assert.True(t, hasFinding(reader, typeCheckSeverityError, "Dependency 'analytics.missing' does not exist"),
		"expected missing dependency finding, got %+v", reader.Findings)
	assert.Equal(t, 1, report.Summary.Errors)
}

func TestDirectRunsValidateTheWholePipelineBeforeExecution(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		run  func(*HybridBruinExecutor, string, string, func([]byte)) ([]byte, error)
	}{
		{
			name: "single asset",
			run: func(executor *HybridBruinExecutor, pipelineRoot, assetPath string, onChunk func([]byte)) ([]byte, error) {
				return executor.RunAsset(context.Background(), RunAssetRequest{AssetPath: assetPath}, onChunk)
			},
		},
		{
			name: "pipeline",
			run: func(executor *HybridBruinExecutor, pipelineRoot, _ string, onChunk func([]byte)) ([]byte, error) {
				return executor.RunPipeline(context.Background(), RunPipelineRequest{Target: pipelineRoot}, onChunk)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspaceRoot, pipelineRoot, assetPath := writeMissingDependencyRunWorkspace(t)
			executor := NewHybridBruinExecutor(workspaceRoot, "", nil, func() *pipeline.Builder {
				return NewRenartPipelineBuilder(afero.NewOsFs())
			})

			var chunks bytes.Buffer
			output, err := test.run(executor, pipelineRoot, assetPath, func(chunk []byte) {
				_, _ = chunks.Write(chunk)
			})
			var validationErr pipelineDependencyValidationError
			require.True(t, errors.As(err, &validationErr), "unexpected error: %v", err)
			assert.Equal(t, output, chunks.Bytes())

			plain := directAssetLogANSI.ReplaceAllString(string(output), "")
			assert.Contains(t, plain, "Analyzed the pipeline 'analytics' with 2 assets.")
			assert.Contains(t, plain, "analytics.broken (assets/broken.sql)")
			assert.Contains(t, plain, "Dependency 'analytics.ghost' does not exist")
			assert.NotContains(t, plain, "Interval:")
			assert.NotContains(t, plain, "Starting the pipeline execution")
			_, statErr := os.Stat(filepath.Join(workspaceRoot, "duckdb-files", "local.db"))
			assert.True(t, os.IsNotExist(statErr), "validation should run before opening the destination database")
		})
	}
}

func TestRenartDryRunAssetTypeRuleAcceptsNativeTypes(t *testing.T) {
	t.Parallel()

	for _, assetType := range []pipeline.AssetType{"api", "load", pipeline.AssetTypeDuckDBQuery} {
		issues, err := ensureRenartAssetType(context.Background(), &pipeline.Pipeline{}, &pipeline.Asset{Type: assetType})
		require.NoError(t, err)
		assert.Empty(t, issues, string(assetType))
	}

	issues, err := ensureRenartAssetType(context.Background(), &pipeline.Pipeline{}, &pipeline.Asset{Type: "unknown.custom"})
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0].Description, "Invalid asset type")
}

func writeMissingDependencyRunWorkspace(t *testing.T) (string, string, string) {
	t.Helper()

	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetsRoot := filepath.Join(pipelineRoot, "assets")
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceRoot, ".bruin.yml"), []byte(strings.TrimSpace(`
default_environment: default
environments:
  default:
    connections:
      duckdb:
        - name: duckdb-default
          path: duckdb-files/local.db
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte(strings.TrimSpace(`
name: analytics
default_connections:
  duckdb: duckdb-default
`)+"\n"), 0o644))

	targetPath := filepath.Join(assetsRoot, "target.sql")
	require.NoError(t, os.WriteFile(targetPath, []byte(strings.TrimSpace(`
/* @bruin
name: analytics.target
type: duckdb.sql
materialization:
  type: table
@bruin */
select 1 as id
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "broken.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.broken
type: duckdb.sql
depends:
  - analytics.ghost
materialization:
  type: table
@bruin */
select 2 as id
`)+"\n"), 0o644))

	return workspaceRoot, pipelineRoot, targetPath
}
