package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/model"
)

func TestWorkspaceCoordinatorSnapshotsDoNotAliasCallers(t *testing.T) {
	t.Parallel()

	nullable := true
	state := WorkspaceState{
		Connections: map[string]string{"warehouse": "postgres"},
		Metadata:    map[string][]string{"owners": {"data"}},
		Pipelines: []WorkspacePipeline{{
			Assets: []WorkspaceAsset{{
				ID: "asset", Parameters: map[string]string{"mode": "full"},
				Columns: []model.Column{{
					Name: "id", Nullable: &nullable, Tags: []string{"primary"},
					Meta: map[string]string{"semantic_type": "identifier"},
				}},
			}},
		}},
		Notebooks: []WorkspaceNotebook{{
			Blocks: []model.NotebookBlock{{Visualization: &model.NotebookVisualization{
				ID: "chart", Definition: map[string]any{
					"encoding": map[string]any{"x": []any{map[string]any{"field": "id"}}},
				},
			}}},
		}},
		Presentations: []model.PresentationArtifact{{
			Visualizations: []model.PresentationVisualization{{
				ID: "chart", Definition: map[string]any{"palette": []any{"green", "blue"}},
			}},
		}},
	}

	coordinator := NewWorkspaceCoordinator(WorkspaceCoordinatorDependencies{})
	coordinator.SetState(state)

	// The coordinator owns the state passed to SetState.
	state.Connections["warehouse"] = "snowflake"
	state.Pipelines[0].Assets[0].Parameters["mode"] = "append"
	*state.Pipelines[0].Assets[0].Columns[0].Nullable = false

	first := coordinator.CurrentState()
	assert.Equal(t, "postgres", first.Connections["warehouse"])
	assert.Equal(t, "full", first.Pipelines[0].Assets[0].Parameters["mode"])
	require.NotNil(t, first.Pipelines[0].Assets[0].Columns[0].Nullable)
	assert.True(t, *first.Pipelines[0].Assets[0].Columns[0].Nullable)

	// A consumer owns the returned snapshot and cannot mutate future reads.
	first.Connections["warehouse"] = "duckdb"
	first.Metadata["owners"][0] = "changed"
	first.Pipelines[0].Assets[0].Parameters["mode"] = "merge"
	first.Pipelines[0].Assets[0].Columns[0].Tags[0] = "changed"
	first.Pipelines[0].Assets[0].Columns[0].Meta["semantic_type"] = "changed"
	*first.Pipelines[0].Assets[0].Columns[0].Nullable = false
	notebookEncoding := first.Notebooks[0].Blocks[0].Visualization.Definition["encoding"].(map[string]any)
	notebookEncoding["x"].([]any)[0].(map[string]any)["field"] = "changed"
	first.Presentations[0].Visualizations[0].Definition["palette"].([]any)[0] = "changed"

	second := coordinator.CurrentState()
	assert.Equal(t, "postgres", second.Connections["warehouse"])
	assert.Equal(t, []string{"data"}, second.Metadata["owners"])
	assert.Equal(t, "full", second.Pipelines[0].Assets[0].Parameters["mode"])
	assert.Equal(t, []string{"primary"}, second.Pipelines[0].Assets[0].Columns[0].Tags)
	assert.Equal(t, "identifier", second.Pipelines[0].Assets[0].Columns[0].Meta["semantic_type"])
	assert.True(t, *second.Pipelines[0].Assets[0].Columns[0].Nullable)
	secondNotebookEncoding := second.Notebooks[0].Blocks[0].Visualization.Definition["encoding"].(map[string]any)
	assert.Equal(t, "id", secondNotebookEncoding["x"].([]any)[0].(map[string]any)["field"])
	assert.Equal(t, "green", second.Presentations[0].Visualizations[0].Definition["palette"].([]any)[0])
}

func BenchmarkCloneWorkspaceState(b *testing.B) {
	for _, assets := range []int{10, 100, 1000} {
		state := workspaceSnapshotBenchmarkState(assets)
		b.Run(fmt.Sprintf("assets_%d", assets), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				cloned := cloneWorkspaceState(state)
				if len(cloned.Pipelines[0].Assets) != assets {
					b.Fatal("clone lost assets")
				}
			}
		})
	}
}

func workspaceSnapshotBenchmarkState(assetCount int) WorkspaceState {
	assets := make([]WorkspaceAsset, 0, assetCount)
	for index := range assetCount {
		assets = append(assets, WorkspaceAsset{
			ID: fmt.Sprintf("asset-%d", index), Name: fmt.Sprintf("analytics.asset_%d", index),
			Type: "duckdb.sql", Content: "select id, amount from raw.orders",
			Parameters: map[string]string{"mode": "full"},
			Columns:    []model.Column{{Name: "id", Type: "bigint"}, {Name: "amount", Type: "decimal"}},
			Tags:       []string{"benchmark"},
		})
	}
	return WorkspaceState{
		Pipelines:   []WorkspacePipeline{{ID: "pipeline", Assets: assets}},
		Connections: map[string]string{"duckdb-default": "duckdb"},
		Metadata:    map[string][]string{"owners": {"data"}},
	}
}
