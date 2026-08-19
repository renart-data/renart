package presentation

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/afero"
)

func TestLoadDashboardArtifactRoundTripsDeterministically(t *testing.T) {
	fs := afero.NewMemMapFs()
	path := "/workspace/presentations/sales.dashboard.yml"
	content := `version: 1
id: sales_overview
title: Sales overview
datasets:
  monthly_sales:
    asset: analytics.monthly_sales
filters:
  - id: region
    label: Region
    type: select
    default: eu
    options:
      values: [eu, us]
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
    filter_bindings:
      - filter: region
        column: region
        operator: equals
layout:
  - visualization: revenue_by_month
    width: 6
    height: 4
`
	if err := afero.WriteFile(fs, path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	artifact, err := LoadArtifact(fs, path)
	if err != nil {
		t.Fatal(err)
	}
	if artifact.Kind != ArtifactKindDashboard || artifact.ID != "sales_overview" || len(artifact.Problems) != 0 {
		t.Fatalf("unexpected dashboard: %+v", artifact)
	}
	if !strings.HasPrefix(artifact.Revision, "v1:") {
		t.Fatalf("missing content revision: %q", artifact.Revision)
	}

	first, err := MarshalArtifact(*artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, path, first, 0o644); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadArtifact(fs, path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := MarshalArtifact(*reloaded)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("canonical artifact changed:\n%s\n---\n%s", first, second)
	}
}

func TestLoadReportArtifactFindsStructuralProblems(t *testing.T) {
	fs := afero.NewMemMapFs()
	path := "/workspace/reports/weekly.report.yml"
	content := `version: 1
id: weekly_report
title: Weekly report
datasets:
  sales:
    query: select 1 as revenue
visualizations:
  - id: revenue
    dataset: missing
    definition:
      version: 1
      type: kpi
      value:
        field: revenue
sections:
  - id: summary
    markdown: Summary
    visualization: revenue
`
	if err := afero.WriteFile(fs, path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	artifact, err := LoadArtifact(fs, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{
		"presentation-dataset-connection-required",
		"presentation-visualization-dataset-missing",
		"report-section-content-invalid",
	} {
		if !hasFinding(artifact.Problems, code) {
			t.Fatalf("missing %s in %+v", code, artifact.Problems)
		}
	}
}

func TestDiscoverArtifactsSkipsGeneratedAndHiddenTrees(t *testing.T) {
	fs := afero.NewMemMapFs()
	for _, path := range []string{
		"/workspace/dashboards/sales.dashboard.yml",
		"/workspace/reports/report.yml",
		"/workspace/.renart/hidden.dashboard.yml",
		"/workspace/node_modules/vendor.report.yml",
		"/workspace/dashboards/not-a-dashboard.yaml",
	} {
		if err := afero.WriteFile(fs, path, []byte("version: 1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := DiscoverArtifacts(fs, "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Clean("/workspace/dashboards/sales.dashboard.yml"),
		filepath.Clean("/workspace/reports/report.yml"),
	}
	if len(paths) != len(want) {
		t.Fatalf("unexpected discovered paths: %v", paths)
	}
	for index := range want {
		if filepath.Clean(paths[index]) != want[index] {
			t.Fatalf("path %d: got %q want %q", index, paths[index], want[index])
		}
	}
}

func TestLoadArtifactRejectsUnknownFields(t *testing.T) {
	fs := afero.NewMemMapFs()
	path := "/workspace/sales.dashboard.yml"
	if err := afero.WriteFile(fs, path, []byte("version: 1\nid: sales\ntitle: Sales\nunknown: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadArtifact(fs, path); err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("unknown field was not rejected: %v", err)
	}
}

func TestArtifactCheckerValidatesVisualizationAndFilterBindings(t *testing.T) {
	artifact := Artifact{
		Version: 1, Kind: ArtifactKindDashboard, ID: "sales", Title: "Sales",
		Datasets: map[string]DatasetDefinition{"monthly": {Asset: "analytics.monthly"}},
		Filters:  []FilterDefinition{{ID: "region", Type: ParameterTypeSelect, Default: "eu"}},
		Visualizations: []ArtifactVisualization{{
			ID: "revenue", Dataset: "monthly",
			Definition: map[string]any{
				"version": 1, "type": "line",
				"encoding": map[string]any{
					"x": map[string]any{"field": "month"},
					"y": []any{map[string]any{"field": "revenue"}},
				},
			},
			FilterBindings: []FilterBinding{{Filter: "region", Column: "region", Operator: "equals"}},
		}},
	}
	datasets := map[string]ResolvedSchema{
		"monthly": {Complete: true, Columns: []ResolvedColumn{
			{Name: "month", PhysicalType: "date"},
			{Name: "revenue", PhysicalType: "numeric"},
			{Name: "region", PhysicalType: "varchar"},
		}},
	}
	findings := (Checker{}).CheckArtifact(context.Background(), artifact, datasets, CheckOptions{Strict: true})
	if len(findings) != 0 {
		t.Fatalf("valid dashboard failed strict checking: %+v", findings)
	}

	datasets["monthly"] = ResolvedSchema{Complete: true, Columns: []ResolvedColumn{
		{Name: "month", PhysicalType: "date"},
		{Name: "revenue", PhysicalType: "varchar"},
	}}
	findings = (Checker{}).CheckArtifact(context.Background(), artifact, datasets, CheckOptions{Strict: true})
	if !hasFinding(findings, "visualization-field-type-incompatible") || !hasFinding(findings, "filter-binding-column-missing") {
		t.Fatalf("strict artifact problems were not reported: %+v", findings)
	}
}

func TestArtifactRejectsFilterBindingToSiblingDataset(t *testing.T) {
	artifact := Artifact{
		Version: 1, Kind: ArtifactKindDashboard, ID: "sales", Title: "Sales",
		Datasets: map[string]DatasetDefinition{
			"monthly": {Asset: "analytics.monthly"},
			"regions": {Asset: "analytics.regions"},
		},
		Filters: []FilterDefinition{{ID: "region", Type: ParameterTypeSelect, Default: "eu"}},
		Visualizations: []ArtifactVisualization{{
			ID: "revenue", Dataset: "monthly",
			Definition: map[string]any{
				"version": 1, "type": "table",
				"columns": []any{map[string]any{"field": "region"}},
			},
			FilterBindings: []FilterBinding{{
				Filter: "region", Dataset: "regions", Column: "region", Operator: "equals",
			}},
		}},
	}

	findings := CheckArtifactDefinition(artifact)
	if !hasFinding(findings, "filter-binding-dataset-mismatch") {
		t.Fatalf("cross-dataset filter binding was not rejected: %+v", findings)
	}
}
