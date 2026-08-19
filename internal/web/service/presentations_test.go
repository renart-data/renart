package service

import (
	"context"
	"os"
	"testing"
)

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
