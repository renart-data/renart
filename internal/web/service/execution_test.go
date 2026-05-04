package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubExecutionExecutor struct {
	runAssetOutput    []byte
	runAssetErr       error
	runAssetChunks    [][]byte
	runAssetRequests  []RunAssetRequest
	runPipelineOutput []byte
	runPipelineErr    error
	runPipelineChunks [][]byte
	runPipelineReqs   []RunPipelineRequest
	queryConnOutput   []byte
	queryConnErr      error
	queryConnReqs     []QueryConnectionRequest
}

func (s *stubExecutionExecutor) RunAsset(_ context.Context, req RunAssetRequest, onChunk func([]byte)) ([]byte, error) {
	s.runAssetRequests = append(s.runAssetRequests, req)
	for _, chunk := range s.runAssetChunks {
		if onChunk != nil {
			onChunk(chunk)
		}
	}
	return s.runAssetOutput, s.runAssetErr
}

func (s *stubExecutionExecutor) RunPipeline(_ context.Context, req RunPipelineRequest, onChunk func([]byte)) ([]byte, error) {
	s.runPipelineReqs = append(s.runPipelineReqs, req)
	for _, chunk := range s.runPipelineChunks {
		if onChunk != nil {
			onChunk(chunk)
		}
	}
	return s.runPipelineOutput, s.runPipelineErr
}

func (s *stubExecutionExecutor) QueryAsset(context.Context, QueryAssetRequest) ([]byte, error) {
	return nil, nil
}

func (s *stubExecutionExecutor) QueryConnection(_ context.Context, req QueryConnectionRequest) ([]byte, error) {
	s.queryConnReqs = append(s.queryConnReqs, req)
	return s.queryConnOutput, s.queryConnErr
}

func (s *stubExecutionExecutor) FormatAsset(context.Context, FormatAssetRequest) ([]byte, error) {
	return nil, nil
}

func (s *stubExecutionExecutor) ApplyPatch(context.Context, PatchRequest) ([]byte, error) {
	return nil, nil
}

func (s *stubExecutionExecutor) ImportDatabase(context.Context, ImportDatabaseRequest) ([]byte, error) {
	return nil, nil
}

func (s *stubExecutionExecutor) RunWithRetry(context.Context, QueryAssetRequest, int, time.Duration) ([]byte, error, int) {
	return nil, nil, 0
}

func TestExecutionServiceMaterializeAssetStreamPreservesSuccessOutput(t *testing.T) {
	t.Parallel()

	assetID := EncodeID("pipelines/orders/assets/orders.sql")
	executor := &stubExecutionExecutor{
		runAssetOutput: []byte("asset run complete\n"),
		runAssetChunks: [][]byte{[]byte("asset "), []byte("run complete\n")},
	}
	recordedName := ""
	recordedStatus := ""
	var recordedAt time.Time
	streamed := make([]string, 0)

	svc := NewExecutionService(ExecutionDependencies{
		ConfigPath: "/path/that/does/not/exist",
		Executor:   executor,
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "pipelines/orders/assets/orders.sql", &pipeline.Pipeline{}, &pipeline.Asset{Connection: "warehouse"}, nil
		},
		ResolveAssetNameByID: func(string) string { return "analytics.orders" },
		FindInspectIDs:       func(...string) []string { return []string{"inspect-1", "inspect-2"} },
		RecordMaterialization: func(name string, at time.Time, status string) {
			recordedName = name
			recordedAt = at
			recordedStatus = status
		},
	})

	result := svc.MaterializeAssetStream(context.Background(), assetID, "", func(chunk []byte) {
		streamed = append(streamed, string(chunk))
	})

	require.Len(t, executor.runAssetRequests, 1)
	assert.Equal(t, "pipelines/orders/assets/orders.sql", executor.runAssetRequests[0].AssetPath)
	assert.Equal(t, "ok", result.Status)
	assert.Equal(t, "asset run complete\n", result.Output)
	assert.Empty(t, result.Error)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "run", result.Operation.Type)
	assert.Equal(t, "pipelines/orders/assets/orders.sql", result.Operation.Target)
	assert.Equal(t, []string{"inspect-1", "inspect-2"}, result.ChangedAssetIDs)
	assert.NotNil(t, result.MaterializedAt)
	assert.Equal(t, []string{"asset ", "run complete\n"}, streamed)
	assert.Equal(t, "analytics.orders", recordedName)
	assert.Equal(t, "succeeded", recordedStatus)
	assert.False(t, recordedAt.IsZero())
}

func TestExecutionServiceMaterializeAssetStreamPreservesFailureOutput(t *testing.T) {
	t.Parallel()

	assetID := EncodeID("pipelines/orders/assets/orders.sql")
	executor := &stubExecutionExecutor{
		runAssetOutput: []byte("asset failed after direct execution\n"),
		runAssetErr:    errors.New("asset failed"),
	}
	recorded := false

	svc := NewExecutionService(ExecutionDependencies{
		ConfigPath: "/path/that/does/not/exist",
		Executor:   executor,
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "pipelines/orders/assets/orders.sql", &pipeline.Pipeline{}, &pipeline.Asset{Connection: "warehouse"}, nil
		},
		ResolveAssetNameByID: func(string) string { return "analytics.orders" },
		FindInspectIDs:       func(...string) []string { return []string{"inspect-1"} },
		RecordMaterialization: func(string, time.Time, string) {
			recorded = true
		},
	})

	result := svc.MaterializeAssetStream(context.Background(), assetID, "", nil)

	require.Len(t, executor.runAssetRequests, 1)
	assert.Equal(t, "error", result.Status)
	assert.Equal(t, "asset failed after direct execution\n", result.Output)
	assert.Equal(t, "asset failed", result.Error)
	assert.Equal(t, 1, result.ExitCode)
	assert.Empty(t, result.ChangedAssetIDs)
	assert.Nil(t, result.MaterializedAt)
	assert.False(t, recorded)
}

func TestExecutionServiceMaterializePipelineStreamPreservesSuccessOutput(t *testing.T) {
	t.Parallel()

	pipelineID := EncodeID("pipelines/orders/pipeline.yml")
	executor := &stubExecutionExecutor{
		runPipelineOutput: []byte("pipeline run complete\n"),
		runPipelineChunks: [][]byte{[]byte("pipeline "), []byte("run complete\n")},
	}
	recorded := make(map[string]string)
	streamed := make([]string, 0)

	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{
				ID: pipelineID,
				Assets: []AssetView{
					{ID: "asset-1", Name: "analytics.orders"},
					{ID: "asset-2", Name: "analytics.order_items"},
				},
			}}
		},
		RecordMaterialization: func(name string, _ time.Time, status string) {
			recorded[name] = status
		},
	})

	result := svc.MaterializePipelineStream(context.Background(), pipelineID, "", func(chunk []byte) {
		streamed = append(streamed, string(chunk))
	})

	require.Len(t, executor.runPipelineReqs, 1)
	assert.Equal(t, "pipelines/orders", executor.runPipelineReqs[0].Target)
	assert.Equal(t, "ok", result.Status)
	assert.Equal(t, "pipeline run complete\n", result.Output)
	assert.Empty(t, result.Error)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "run", result.Operation.Type)
	assert.Equal(t, "pipelines/orders", result.Operation.Target)
	assert.Equal(t, []string{"asset-1", "asset-2"}, result.ChangedAssetIDs)
	assert.NotNil(t, result.MaterializedAt)
	assert.Equal(t, []string{"pipeline ", "run complete\n"}, streamed)
	assert.Equal(t, map[string]string{
		"analytics.orders":      "succeeded",
		"analytics.order_items": "succeeded",
	}, recorded)
}

func TestExecutionServiceMaterializePipelineStreamPreservesFailureOutput(t *testing.T) {
	t.Parallel()

	pipelineID := EncodeID("pipelines/orders/pipeline.yml")
	executor := &stubExecutionExecutor{
		runPipelineOutput: []byte("pipeline failed during direct execution\n"),
		runPipelineErr:    errors.New("pipeline failed"),
	}
	recorded := false

	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{
				ID:     pipelineID,
				Assets: []AssetView{{ID: "asset-1", Name: "analytics.orders"}},
			}}
		},
		RecordMaterialization: func(string, time.Time, string) {
			recorded = true
		},
	})

	result := svc.MaterializePipelineStream(context.Background(), pipelineID, "", nil)

	require.Len(t, executor.runPipelineReqs, 1)
	assert.Equal(t, "error", result.Status)
	assert.Equal(t, "pipeline failed during direct execution\n", result.Output)
	assert.Equal(t, "pipeline failed", result.Error)
	assert.Equal(t, 1, result.ExitCode)
	assert.Empty(t, result.ChangedAssetIDs)
	assert.Nil(t, result.MaterializedAt)
	assert.False(t, recorded)
}

func TestExecutionServiceInspectAssetRejectsWriteQueries(t *testing.T) {
	t.Parallel()

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
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte("name: analytics\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "customers.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
@bruin */

copy (select * from analytics.customers) to 'danger.parquet'
`)+"\n"), 0o644))

	buildPipeline := func(ctx context.Context, pipelinePath string) (*pipeline.Pipeline, error) {
		osFS := afero.NewOsFs()
		builder := pipeline.NewBuilder(
			BuilderConfig,
			pipeline.CreateTaskFromYamlDefinition(osFS),
			pipeline.CreateTaskFromFileComments(osFS),
			osFS,
			DefaultGlossaryReader,
		)
		return builder.CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	}

	resolveAssetByID := func(ctx context.Context, assetID string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
		relAssetPath, err := DecodeID(assetID)
		if err != nil {
			return "", nil, nil, err
		}
		assetPath, err := SafeJoin(workspaceRoot, relAssetPath)
		if err != nil {
			return "", nil, nil, err
		}
		parsedPipeline, err := buildPipeline(ctx, filepath.Dir(filepath.Dir(assetPath)))
		if err != nil {
			return "", nil, nil, err
		}
		for _, asset := range parsedPipeline.Assets {
			currentPath := asset.ExecutableFile.Path
			if currentPath == "" {
				currentPath = asset.DefinitionFile.Path
			}
			relCurrent, relErr := filepath.Rel(workspaceRoot, currentPath)
			if relErr == nil && filepath.ToSlash(relCurrent) == filepath.ToSlash(relAssetPath) {
				return filepath.ToSlash(relAssetPath), parsedPipeline, asset, nil
			}
		}
		return "", nil, nil, ErrAssetNotFound
	}

	svc := NewExecutionService(ExecutionDependencies{
		WorkspaceRoot:    workspaceRoot,
		ConfigPath:       filepath.Join(workspaceRoot, ".bruin.yml"),
		Executor:         &stubExecutionExecutor{},
		ResolveAssetByID: resolveAssetByID,
	})

	result := svc.InspectAsset(context.Background(), EncodeID("analytics/assets/customers.sql"), "200", "")

	assert.Equal(t, "error", result.Status)
	assert.Equal(t, 400, result.HTTPStatus)
	assert.Equal(t, inspectReadOnlyErrorMessage, result.Error)
	assert.Equal(t, inspectReadOnlyErrorMessage, result.RawOutput)
	assert.Empty(t, result.Rows)
	assert.Empty(t, result.Columns)
}

func TestExecutionServiceInspectNonSQLAssetQueriesMaterializedTable(t *testing.T) {
	t.Parallel()

	executor := &stubExecutionExecutor{
		queryConnOutput: []byte(`{"columns":["customer_id"],"rows":[{"customer_id":1}]}`),
	}
	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "analytics/assets/load_customers.yml", &pipeline.Pipeline{
					DefaultConnections: pipeline.EmptyStringMap{"duckdb": "duckdb-default"},
				}, &pipeline.Asset{
					Name: "analytics.customers",
					Type: pipeline.AssetTypeIngestr,
					Parameters: map[string]string{
						"destination": "duckdb",
					},
				}, nil
		},
	})

	result := svc.InspectAsset(context.Background(), EncodeID("analytics/assets/load_customers.yml"), "25", "")

	require.Len(t, executor.queryConnReqs, 1)
	assert.Equal(t, "duckdb-default", executor.queryConnReqs[0].ConnectionName)
	assert.Equal(t, "select * from analytics.customers limit 25", executor.queryConnReqs[0].Query)
	assert.Equal(t, "ok", result.Status)
	assert.Equal(t, []string{"customer_id"}, result.Columns)
	assert.Equal(t, []map[string]any{{"customer_id": float64(1)}}, result.Rows)
}

func TestExecutionServiceInspectNonSQLAssetReportsMissingMaterializedTable(t *testing.T) {
	t.Parallel()

	executor := &stubExecutionExecutor{queryConnErr: errors.New("table not found")}
	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "analytics/assets/task.py", &pipeline.Pipeline{
					DefaultConnections: pipeline.EmptyStringMap{"duckdb": "duckdb-default"},
				}, &pipeline.Asset{
					Name: "analytics.python_task",
					Type: pipeline.AssetTypePython,
				}, nil
		},
	})

	result := svc.InspectAsset(context.Background(), EncodeID("analytics/assets/task.py"), "25", "")

	assert.Equal(t, "error", result.Status)
	assert.Contains(t, result.Error, "Materialize the asset first")
	require.Len(t, executor.queryConnReqs, 1)
}
