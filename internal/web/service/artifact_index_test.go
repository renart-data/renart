package service

import (
	"reflect"
	"strings"
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

func TestBuildArtifactIndexProjectsDashboardDatasetsFiltersAndColumnLineage(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			UUID: "pipeline-uuid",
			Assets: []model.Asset{{
				ID: "sales-workspace-id", Name: "analytics.monthly_sales", URI: "renart://local/monthly-sales",
				Columns: []model.Column{
					{Name: "month", Type: "date"}, {Name: "revenue", Type: "numeric"}, {Name: "region", Type: "varchar"},
				},
			}},
		}},
		Presentations: []model.PresentationArtifact{{
			ID: "sales_overview", Kind: "dashboard", Title: "Sales overview", Path: "dashboards/sales.dashboard.yml",
			Datasets: []model.PresentationDataset{{ID: "monthly_sales", Asset: "renart://local/monthly-sales"}},
			Filters: []model.PresentationFilter{{
				ID: "region", Type: "select", Default: "eu",
				Options: &model.PresentationFilterOptions{Dataset: "monthly_sales", ValueField: "region"},
			}},
			Visualizations: []model.PresentationVisualization{{
				ID: "revenue_by_month", Dataset: "monthly_sales",
				Definition: map[string]any{
					"version": 1, "type": "line",
					"encoding": map[string]any{
						"x": map[string]any{"field": "month"},
						"y": []any{map[string]any{"field": "revenue"}},
					},
				},
				FilterBindings: []model.PresentationFilterBinding{{Filter: "region", Column: "region", Operator: "equals"}},
			}},
		}},
	}

	index := BuildArtifactIndex(state)
	dashboard := findArtifact(t, index, artifactKindDashboard, "sales_overview")
	if len(dashboard.Components) != 3 || dashboard.Components[0].Kind != componentKindDataset ||
		dashboard.Components[1].Kind != componentKindFilter || dashboard.Components[2].Kind != componentKindVisualization {
		t.Fatalf("unexpected dashboard components: %+v", dashboard.Components)
	}
	if !reflect.DeepEqual(dashboard.Components[0].Columns, state.Pipelines[0].Assets[0].Columns) {
		t.Fatalf("asset-backed dataset did not inherit its schema: %+v", dashboard.Components[0].Columns)
	}

	wantEdges := map[string]bool{
		"pipeline-uuid:analytics.monthly_sales->sales_overview/dataset:monthly_sales":         true,
		"sales_overview/dataset:monthly_sales->sales_overview/filter:region":                  true,
		"sales_overview/dataset:monthly_sales->sales_overview/visualization:revenue_by_month": true,
		"sales_overview/filter:region->sales_overview/visualization:revenue_by_month":         true,
	}
	for _, dependency := range index.Dependencies {
		delete(wantEdges, readableArtifactEdge(dependency))
		if dependency.Consumer.ComponentID == "visualization:revenue_by_month" && dependency.Producer.ComponentID == "dataset:monthly_sales" {
			if !reflect.DeepEqual(dependency.Columns, []model.ArtifactColumnUsage{
				{Name: "month", Role: "encoding.x.field"},
				{Name: "revenue", Role: "encoding.y[0].field"},
				{Name: "region", Role: "filter_bindings[0].column"},
			}) {
				t.Fatalf("dashboard visualization column lineage is wrong: %+v", dependency.Columns)
			}
		}
	}
	if len(wantEdges) != 0 {
		t.Fatalf("missing dashboard lineage edges: %v (all=%+v)", wantEdges, index.Dependencies)
	}
}

func TestBuildArtifactIndexDerivesTransitiveSQLColumnImpact(t *testing.T) {
	state := model.WorkspaceState{
		Pipelines: []model.Pipeline{{
			ID: "pipeline", UUID: "pipeline-uuid", Name: "analytics", Path: "analytics",
			Assets: []model.Asset{
				{
					ID: "orders-id", Name: "raw.orders", Path: "analytics/assets/orders.asset.yml",
					Type: "pg.source", Columns: []model.Column{
						{Name: "id", Type: "bigint"}, {Name: "amount", Type: "numeric"},
					},
				},
				{
					ID: "clean-id", Name: "analytics.clean_orders", Path: "analytics/assets/clean_orders.sql",
					Type: "pg.sql",
					Content: `with selected as (
  select id, amount from raw.orders
)
select id as order_id, amount * 2 as gross from selected`,
					Columns: []model.Column{{Name: "order_id", Type: "bigint"}, {Name: "gross", Type: "numeric"}},
					Dependencies: []model.AssetDependency{{
						Value: "raw.orders", ResolvedAssetID: "orders-id",
					}},
				},
				{
					ID: "summary-id", Name: "analytics.order_summary", Path: "analytics/assets/order_summary.sql",
					Type: "pg.sql", Content: "select * from analytics.clean_orders",
					Columns: []model.Column{{Name: "order_id", Type: "bigint"}, {Name: "gross", Type: "numeric"}},
					Dependencies: []model.AssetDependency{{
						Value: "analytics.clean_orders", ResolvedAssetID: "clean-id",
					}},
				},
			},
		}},
		Presentations: []model.PresentationArtifact{{
			ID: "orders_dashboard", Kind: "dashboard", Title: "Orders", Path: "dashboards/orders.dashboard.yml",
			Datasets: []model.PresentationDataset{{ID: "orders", Asset: "analytics.order_summary"}},
			Visualizations: []model.PresentationVisualization{{
				ID: "gross", Dataset: "orders",
				Definition: map[string]any{
					"version": 1, "type": "kpi",
					"encoding": map[string]any{"value": map[string]any{"field": "gross"}},
				},
			}},
		}},
	}

	index := BuildArtifactIndex(state)
	ordersRef := model.ArtifactRef{Kind: artifactKindPipelineAsset, ArtifactID: "pipeline-uuid:raw.orders"}
	cleanRef := model.ArtifactRef{Kind: artifactKindPipelineAsset, ArtifactID: "pipeline-uuid:analytics.clean_orders"}
	summaryRef := model.ArtifactRef{Kind: artifactKindPipelineAsset, ArtifactID: "pipeline-uuid:analytics.order_summary"}
	datasetRef := model.ArtifactRef{Kind: artifactKindDashboard, ArtifactID: "orders_dashboard", ComponentID: "dataset:orders"}
	vizRef := model.ArtifactRef{Kind: artifactKindDashboard, ArtifactID: "orders_dashboard", ComponentID: "visualization:gross"}

	if got := artifactDependencyColumns(t, index, ordersRef, cleanRef); !reflect.DeepEqual(got, []model.ArtifactColumnUsage{
		{Name: "amount", ConsumerColumn: "gross"},
		{Name: "id", ConsumerColumn: "order_id"},
	}) {
		t.Fatalf("source-to-CTE column lineage is wrong: %+v", got)
	}
	if got := artifactDependencyColumns(t, index, cleanRef, summaryRef); !reflect.DeepEqual(got, []model.ArtifactColumnUsage{
		{Name: "gross", ConsumerColumn: "gross"},
		{Name: "order_id", ConsumerColumn: "order_id"},
	}) {
		t.Fatalf("SQL-to-SQL column lineage is wrong: %+v", got)
	}
	if got := artifactDependencyColumns(t, index, summaryRef, datasetRef); !reflect.DeepEqual(got, []model.ArtifactColumnUsage{
		{Name: "gross", ConsumerColumn: "gross"},
		{Name: "order_id", ConsumerColumn: "order_id"},
	}) {
		t.Fatalf("asset-backed dataset identity lineage is wrong: %+v", got)
	}

	want := []model.ArtifactColumnImpact{
		{Producer: ordersRef, Column: "amount", Consumer: cleanRef, ConsumerColumn: "gross", Distance: 1},
		{Producer: ordersRef, Column: "amount", Consumer: summaryRef, ConsumerColumn: "gross", Distance: 2},
		{Producer: ordersRef, Column: "amount", Consumer: datasetRef, ConsumerColumn: "gross", Distance: 3},
		{Producer: ordersRef, Column: "amount", Consumer: vizRef, Role: "encoding.value.field", Distance: 4},
	}
	if got := breakingImpactsFor(index, ordersRef, "amount"); !reflect.DeepEqual(got, want) {
		t.Fatalf("transitive breaking impact is wrong:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestBuildArtifactIndexKeepsAmbiguousColumnLineageUnknown(t *testing.T) {
	state := model.WorkspaceState{Pipelines: []model.Pipeline{{
		UUID: "pipeline-uuid",
		Assets: []model.Asset{
			{ID: "current", Name: "raw.orders", Type: "pg.source", Columns: []model.Column{{Name: "id"}}},
			{ID: "archive", Name: "archive.orders", Type: "pg.source", Columns: []model.Column{{Name: "id"}}},
			{
				ID: "consumer", Name: "analytics.orders", Type: "pg.sql",
				Content: "select id from orders", Columns: []model.Column{{Name: "id"}},
				Dependencies: []model.AssetDependency{
					{Value: "raw.orders", ResolvedAssetID: "current"},
					{Value: "archive.orders", ResolvedAssetID: "archive"},
				},
			},
		},
	}}}

	index := BuildArtifactIndex(state)
	consumer := model.ArtifactRef{Kind: artifactKindPipelineAsset, ArtifactID: "pipeline-uuid:analytics.orders"}
	for _, producerName := range []string{"raw.orders", "archive.orders"} {
		producer := model.ArtifactRef{Kind: artifactKindPipelineAsset, ArtifactID: "pipeline-uuid:" + producerName}
		if got := artifactDependencyColumns(t, index, producer, consumer); len(got) != 0 {
			t.Fatalf("ambiguous short relation invented lineage for %s: %+v", producerName, got)
		}
		if got := breakingImpactsFor(index, producer, "id"); len(got) != 0 {
			t.Fatalf("ambiguous short relation invented impacts for %s: %+v", producerName, got)
		}
	}
}

func TestBuildArtifactIndexIncludesFilterOnlyColumnImpact(t *testing.T) {
	state := model.WorkspaceState{Pipelines: []model.Pipeline{{
		UUID: "pipeline-uuid",
		Assets: []model.Asset{
			{
				ID: "orders", Name: "raw.orders", Type: "pg.source",
				Columns: []model.Column{{Name: "id"}, {Name: "status"}},
			},
			{
				ID: "paid", Name: "analytics.paid_orders", Type: "pg.sql",
				Content:      "select o.id from raw.orders o where o.status = 'paid'",
				Columns:      []model.Column{{Name: "id"}},
				Dependencies: []model.AssetDependency{{Value: "raw.orders", ResolvedAssetID: "orders"}},
			},
		},
	}}}

	index := BuildArtifactIndex(state)
	producer := model.ArtifactRef{Kind: artifactKindPipelineAsset, ArtifactID: "pipeline-uuid:raw.orders"}
	consumer := model.ArtifactRef{Kind: artifactKindPipelineAsset, ArtifactID: "pipeline-uuid:analytics.paid_orders"}
	wantColumns := []model.ArtifactColumnUsage{
		{Name: "id", ConsumerColumn: "id"},
		{Name: "status", Role: artifactColumnRoleQueryReference},
	}
	if got := artifactDependencyColumns(t, index, producer, consumer); !reflect.DeepEqual(got, wantColumns) {
		t.Fatalf("filter-only column reference is wrong: got=%+v want=%+v", got, wantColumns)
	}
	wantImpact := []model.ArtifactColumnImpact{{
		Producer: producer, Column: "status", Consumer: consumer,
		Role: artifactColumnRoleQueryReference, Distance: 1,
	}}
	if got := breakingImpactsFor(index, producer, "status"); !reflect.DeepEqual(got, wantImpact) {
		t.Fatalf("filter-only breaking impact is wrong: got=%+v want=%+v", got, wantImpact)
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

func artifactDependencyColumns(
	t *testing.T,
	index model.ArtifactIndex,
	producer model.ArtifactRef,
	consumer model.ArtifactRef,
) []model.ArtifactColumnUsage {
	t.Helper()
	for _, dependency := range index.Dependencies {
		if artifactRefKey(dependency.Producer) == artifactRefKey(producer) &&
			artifactRefKey(dependency.Consumer) == artifactRefKey(consumer) {
			return dependency.Columns
		}
	}
	t.Fatalf("artifact dependency %s -> %s not found", artifactRefKey(producer), artifactRefKey(consumer))
	return nil
}

func breakingImpactsFor(index model.ArtifactIndex, producer model.ArtifactRef, column string) []model.ArtifactColumnImpact {
	result := make([]model.ArtifactColumnImpact, 0)
	for _, impact := range index.BreakingColumnImpacts {
		if artifactRefKey(impact.Producer) == artifactRefKey(producer) && strings.EqualFold(impact.Column, column) {
			result = append(result, impact)
		}
	}
	return result
}
