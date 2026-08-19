package service

import (
	"reflect"
	"testing"

	"renart/internal/web/model"
)

func TestBuildArtifactIndexProjectsHostsComponentsAndLineage(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			ID: "pipeline-workspace-id", UUID: "pipeline-uuid", Name: "analytics", Path: "analytics",
			Assets: []model.Asset{
				{
					ID: "orders-workspace-id", Name: "analytics.orders", Path: "analytics/assets/orders.sql",
					Type: "pg.source", Columns: []model.Column{{Name: "ordered_at", Type: "timestamp"}},
				},
				{
					ID: "summary-workspace-id", Name: "analytics.summary", Path: "analytics/assets/summary.sql",
					Type: "pg.sql", Columns: []model.Column{{Name: "revenue", Type: "numeric"}},
					Dependencies: []model.AssetDependency{{
						Value: "analytics.orders", ResolvedAssetID: "orders-workspace-id",
					}},
				},
			},
		}},
		Notebooks: []model.Notebook{{
			ID: "notebook-workspace-id", UUID: "notebook-uuid", Title: "Revenue", Path: "notebooks/revenue",
			Parameters: []model.NotebookParameter{{ID: "region", Label: "Region", Type: "select", Default: "eu"}},
			Blocks: []model.NotebookBlock{
				{Cell: "cell-source"},
				{Cell: "cell-monthly"},
				{ID: "md-findings", Markdown: "## Findings"},
				{ID: "viz-revenue", Visualization: &model.NotebookVisualization{
					ID: "viz-revenue", Source: "cell-monthly",
					Definition: map[string]any{
						"version": 1,
						"type":    "line",
						"encoding": map[string]any{
							"x": map[string]any{"field": "month"},
							"y": []any{map[string]any{"field": "revenue"}},
						},
					},
				}},
			},
			Cells: []model.Asset{
				{
					CellID: "cell-source", Name: "orders", Path: "notebooks/revenue/orders.sql",
					Type: "duckdb.sql", ExternalRefs: []string{"analytics.orders"},
					Columns: []model.Column{{Name: "ordered_at", Type: "timestamp"}},
				},
				{
					CellID: "cell-monthly", Name: "monthly", Path: "notebooks/revenue/monthly.sql",
					Type: "duckdb.sql", Upstreams: []string{"orders"},
					Columns: []model.Column{{Name: "month", Type: "date"}, {Name: "revenue", Type: "numeric"}},
				},
			},
		}},
	}

	index := BuildArtifactIndex(state)
	if len(index.Artifacts) != 3 {
		t.Fatalf("expected two assets and one notebook, got %+v", index.Artifacts)
	}
	if index.Revision == "" {
		t.Fatal("artifact index has no revision")
	}
	notebookArtifact := findArtifact(t, index, artifactKindNotebook, "notebook-uuid")
	if len(notebookArtifact.Components) != 5 || len(index.Containment) != 5 {
		t.Fatalf("notebook containment was not projected: artifact=%+v containment=%+v", notebookArtifact, index.Containment)
	}
	if notebookArtifact.Components[0].Kind != componentKindParameter ||
		notebookArtifact.Components[1].Kind != componentKindCell ||
		notebookArtifact.Components[3].Kind != componentKindMarkdown ||
		notebookArtifact.Components[4].Kind != componentKindVisualization {
		t.Fatalf("unexpected component projection: %+v", notebookArtifact.Components)
	}

	wantEdges := map[string]bool{
		"pipeline-uuid:analytics.orders->pipeline-uuid:analytics.summary": true,
		"pipeline-uuid:analytics.orders->notebook-uuid/cell-source":       true,
		"notebook-uuid/cell-source->notebook-uuid/cell-monthly":           true,
		"notebook-uuid/cell-monthly->notebook-uuid/viz-revenue":           true,
	}
	for _, dependency := range index.Dependencies {
		delete(wantEdges, readableArtifactEdge(dependency))
		if dependency.Consumer.ComponentID == "viz-revenue" {
			if !reflect.DeepEqual(dependency.Columns, []model.ArtifactColumnUsage{
				{Name: "month", Role: "encoding.x.field"},
				{Name: "revenue", Role: "encoding.y[0].field"},
			}) {
				t.Fatalf("visualization column lineage is wrong: %+v", dependency.Columns)
			}
		}
	}
	if len(wantEdges) != 0 {
		t.Fatalf("missing artifact lineage edges: %v (all=%+v)", wantEdges, index.Dependencies)
	}

	again := BuildArtifactIndex(state)
	if again.Revision != index.Revision || !reflect.DeepEqual(again, index) {
		t.Fatalf("artifact index is not deterministic:\nfirst=%+v\nsecond=%+v", index, again)
	}
}

func TestBuildArtifactIndexDoesNotResolveAmbiguousNotebookExternalRelation(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{
			{ID: "p1", UUID: "p1", Assets: []model.Asset{{ID: "a1", Name: "public.accounts"}}},
			{ID: "p2", UUID: "p2", Assets: []model.Asset{{ID: "a2", Name: "public.accounts"}}},
		},
		Notebooks: []model.Notebook{{
			ID: "n", UUID: "n", Blocks: []model.NotebookBlock{{Cell: "c"}},
			Cells: []model.Asset{{CellID: "c", Name: "query", ExternalRefs: []string{"public.accounts"}}},
		}},
	}
	index := BuildArtifactIndex(state)
	if len(index.Dependencies) != 0 {
		t.Fatalf("ambiguous relation produced a misleading edge: %+v", index.Dependencies)
	}
}

func findArtifact(t *testing.T, index model.ArtifactIndex, kind, id string) model.ArtifactDescriptor {
	t.Helper()
	for _, artifact := range index.Artifacts {
		if artifact.Kind == kind && artifact.ID == id {
			return artifact
		}
	}
	t.Fatalf("artifact %s/%s not found", kind, id)
	return model.ArtifactDescriptor{}
}

func readableArtifactEdge(dependency model.ArtifactDependency) string {
	format := func(ref model.ArtifactRef) string {
		if ref.ComponentID == "" {
			return ref.ArtifactID
		}
		return ref.ArtifactID + "/" + ref.ComponentID
	}
	return format(dependency.Producer) + "->" + format(dependency.Consumer)
}
