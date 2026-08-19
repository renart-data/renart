package service

import (
	"context"
	"os"
	"testing"

	"renart/internal/web/model"
	"renart/internal/web/presentation"
)

func TestResolvePresentationDatasetSchemasInfersQueryOutputsFromWorkspaceAssets(t *testing.T) {
	state := model.WorkspaceState{
		Connections: map[string]string{"duckdb-default": "duckdb"},
		Pipelines: []model.Pipeline{{
			ID: "analytics",
			Assets: []model.Asset{{
				ID: "weather", Name: "analytics.weather", Type: "api",
				Columns: []model.Column{
					{Name: "id", Type: "BIGINT"},
					{Name: "temperature", Type: "DOUBLE"},
				},
			}},
		}},
	}
	artifact := &presentation.Artifact{
		ID: "weather_dashboard",
		Datasets: map[string]presentation.DatasetDefinition{
			"dataset_3": {
				Connection: "duckdb-default",
				Query:      "select * from analytics.weather",
			},
		},
	}

	schemas, findings := resolvePresentationDatasetSchemas(context.Background(), "/workspace", state, artifact)
	if len(findings) != 0 {
		t.Fatalf("query schema inference returned findings: %+v", findings)
	}
	resolved, ok := schemas["dataset_3"]
	if !ok {
		t.Fatal("query dataset schema was not inferred")
	}
	if !resolved.Complete || len(resolved.Columns) != 2 {
		t.Fatalf("unexpected inferred schema: %+v", resolved)
	}
	columnsByName := make(map[string]string, len(resolved.Columns))
	for _, column := range resolved.Columns {
		columnsByName[column.Name] = column.PhysicalType
	}
	if columnsByName["id"] != "BIGINT" || columnsByName["temperature"] != "DOUBLE" {
		t.Fatalf("unexpected inferred columns: %+v", resolved.Columns)
	}

	dto := presentationToModel("/workspace", artifact, schemas)
	if len(dto.Datasets) != 1 || len(dto.Datasets[0].Columns) != 0 || len(dto.Datasets[0].ResolvedColumns) != 2 {
		t.Fatalf("inferred columns should be exposed separately from authored columns: %+v", dto.Datasets)
	}
	for _, finding := range (presentation.Checker{}).CheckArtifact(
		context.Background(), *artifact, schemas, presentation.CheckOptions{Strict: true},
	) {
		if finding.Code == "presentation-dataset-schema-unresolved" {
			t.Fatalf("inferred query dataset remained unresolved: %+v", finding)
		}
	}
}

func TestResolvePresentationDatasetSchemasUsesInferredSQLAssetColumns(t *testing.T) {
	state := model.WorkspaceState{
		Connections: map[string]string{"duckdb-default": "duckdb"},
		Pipelines: []model.Pipeline{{
			ID: "analytics",
			Assets: []model.Asset{{
				ID: "orders", Name: "analytics.orders", Type: "duckdb.sql", Path: "analytics/orders.sql",
				Content: "select 100 as order_id, 1 as customer_id, 42 as total_amount",
			}},
		}},
	}
	artifact := &presentation.Artifact{
		ID: "orders_dashboard",
		Datasets: map[string]presentation.DatasetDefinition{
			"orders": {Connection: "duckdb-default", Query: "select * from analytics.orders"},
		},
	}

	schemas, _ := resolvePresentationDatasetSchemas(context.Background(), "/workspace", state, artifact)
	resolved, ok := schemas["orders"]
	if !ok || len(resolved.Columns) != 3 {
		t.Fatalf("query did not consume the SQL asset's inferred schema: %+v", resolved)
	}
}

func TestComputeStateLoadsGitNativePresentations(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(root+"/.git", 0o755); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "analytics/pipeline.yml", "id: 11111111-2222-3333-4444-555555555555\nname: analytics\n")
	writeWorkspaceFile(t, root, "analytics/assets/monthly_sales.sql", `/* @bruin
name: analytics.monthly_sales
type: duckdb.sql
columns:
  - name: month
    type: date
  - name: revenue
    type: numeric
@bruin */
select date '2026-08-01' as month, 42::decimal as revenue
`)
	writeWorkspaceFile(t, root, "dashboards/sales.dashboard.yml", `version: 1
id: sales_overview
title: Sales overview
datasets:
  monthly_sales:
    asset: analytics.monthly_sales
visualizations:
  - id: revenue_by_month
    dataset: monthly_sales
    definition:
      version: 1
      type: line
      encoding:
        x:
          field: month
        y:
          - field: revenue
layout:
  - visualization: revenue_by_month
`)

	state, err := NewWorkspaceService(root, "").ComputeState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Presentations) != 1 {
		t.Fatalf("expected one presentation, got %+v (errors=%v)", state.Presentations, state.Errors)
	}
	dashboard := state.Presentations[0]
	if dashboard.Kind != "dashboard" || dashboard.ID != "sales_overview" || dashboard.Path != "dashboards/sales.dashboard.yml" {
		t.Fatalf("unexpected dashboard DTO: %+v", dashboard)
	}
	if len(dashboard.Problems) != 0 || len(dashboard.Datasets) != 1 || len(dashboard.Visualizations) != 1 {
		t.Fatalf("dashboard did not load cleanly: %+v", dashboard)
	}
	artifact := findArtifact(t, state.ArtifactIndex, artifactKindDashboard, "sales_overview")
	if len(artifact.Components) != 2 || len(artifact.Components[0].Columns) != 2 {
		t.Fatalf("dashboard artifact projection is incomplete: %+v", artifact)
	}
}

func TestComputeStateKeepsInvalidPresentationEditable(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(root+"/.git", 0o755); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "reports/weekly.report.yml", `version: 1
id: weekly
title: Weekly
sections:
  - id: broken
`)

	state, err := NewWorkspaceService(root, "").ComputeState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Presentations) != 1 || len(state.Presentations[0].Problems) == 0 {
		t.Fatalf("invalid report should remain visible with findings: %+v", state.Presentations)
	}
}
