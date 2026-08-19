package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bruin-data/bruin/pkg/config"
	"renart/internal/web/model"
	"renart/internal/web/presentation"
)

func TestRenderPresentationQueryUsesTypedBindingsAndWarehouseLimit(t *testing.T) {
	definitions := []presentation.FilterDefinition{
		{ID: "region", Type: presentation.ParameterTypeText, Default: "eu"},
		{ID: "active", Type: presentation.ParameterTypeBoolean, Default: true},
	}
	values, findings := presentation.ResolveParameterValues(definitions, map[string]any{
		"region": "O'Reilly%_!", "active": true,
	})
	if problem := firstPresentationError(findings); problem != nil {
		t.Fatalf("resolve filter values: %+v", problem)
	}
	literals, err := presentation.ParameterSQLLiterals(definitions, values)
	if err != nil {
		t.Fatal(err)
	}
	query, err := renderPresentationQuery(
		"SELECT region, active FROM sales;", "mssql", definitions, values, literals,
		[]presentation.FilterBinding{
			{Filter: "region", Column: "region", Operator: "contains"},
			{Filter: "active", Column: "active", Operator: "equals"},
		},
		"sales", 101,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"SELECT TOP (101)",
		"[region] LIKE '%O''Reilly!%!_!!%' ESCAPE '!'",
		"[active] = TRUE",
	} {
		if !strings.Contains(query, expected) {
			t.Fatalf("query does not contain %q:\n%s", expected, query)
		}
	}
	if strings.Contains(query, "sales;\n") {
		t.Fatalf("wrapped query retained its trailing semicolon:\n%s", query)
	}
}

func TestPresentationRunExecutesOnlyRequestedVisualizationWithItsFilters(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dashboards", "sales.dashboard.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `version: 1
id: sales
title: Sales
datasets:
  sales:
    connection: warehouse
    query: SELECT month, region, revenue FROM monthly_sales
    columns:
      - name: month
        type: date
      - name: region
        type: varchar
      - name: revenue
        type: bigint
filters:
  - id: region
    type: select
    default: eu
    options:
      values: [eu, us]
visualizations:
  - id: filtered_revenue
    dataset: sales
    definition:
      version: 1
      type: line
      encoding:
        x: {field: month}
        y: [{field: revenue}]
    filter_bindings:
      - filter: region
        column: region
        operator: equals
  - id: all_revenue
    dataset: sales
    definition:
      version: 1
      type: table
layout:
  - visualization: filtered_revenue
  - visualization: all_revenue
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	queries := make([]string, 0, 1)
	service := NewPresentationService(PresentationDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() model.WorkspaceState { return model.WorkspaceState{} },
		NewConnectionManager: func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
			return &stubConnectionManager{connectionType: "duckdb"}, nil
		},
		RunConnectionQuery: func(_ context.Context, connection, environment, query string) ([]string, []map[string]any, error) {
			if connection != "warehouse" || environment != "dev" {
				t.Fatalf("unexpected runtime target connection=%q environment=%q", connection, environment)
			}
			queries = append(queries, query)
			return []string{"month", "region", "revenue"}, []map[string]any{{
				"month": "2026-08-01", "region": "us", "revenue": float64(42),
			}}, nil
		},
	})

	result, apiErr := service.Run(context.Background(), EncodeID("dashboards/sales.dashboard.yml"), model.PresentationRunRequest{
		Environment: "dev", FilterValues: map[string]any{"region": "us"},
		VisualizationIDs: []string{"filtered_revenue"},
	})
	if apiErr != nil {
		t.Fatalf("run: %+v", apiErr)
	}
	if result.Status != "ok" || result.FilterValues["region"] != "us" || len(result.Visualizations) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	visualization := result.Visualizations["filtered_revenue"]
	if visualization.Status != "ok" || visualization.TotalRows != 1 || len(visualization.Rows) != 1 || visualization.Rows[0][2] != float64(42) {
		t.Fatalf("unexpected visualization result: %+v", visualization)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], `"region" = 'us'`) || !strings.Contains(queries[0], "LIMIT 1001") {
		t.Fatalf("unexpected runtime queries: %#v", queries)
	}
}

func TestPresentationRunRejectsSideEffectingQueryBeforeExecution(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "reports", "unsafe.report.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `version: 1
id: unsafe
title: Unsafe
datasets:
  data:
    connection: warehouse
    query: DELETE FROM important_table
    columns:
      - name: id
        type: bigint
visualizations:
  - id: rows
    dataset: data
    definition: {version: 1, type: table}
sections:
  - id: rows
    visualization: rows
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	executed := false
	service := NewPresentationService(PresentationDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() model.WorkspaceState { return model.WorkspaceState{} },
		NewConnectionManager: func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
			return &stubConnectionManager{connectionType: "duckdb"}, nil
		},
		RunConnectionQuery: func(context.Context, string, string, string) ([]string, []map[string]any, error) {
			executed = true
			return nil, nil, nil
		},
	})
	result, apiErr := service.Run(context.Background(), EncodeID("reports/unsafe.report.yml"), model.PresentationRunRequest{})
	if apiErr != nil {
		t.Fatalf("run returned transport error instead of a component result: %+v", apiErr)
	}
	if executed || result.Status != "error" || !strings.Contains(result.Visualizations["rows"].Error, "read-only SELECT") {
		t.Fatalf("unsafe query was not rejected safely: executed=%v result=%+v", executed, result)
	}
}

func TestPresentationPreviewRunsUnsavedDraftWithoutWritingOrPublishing(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dashboards", "preview.dashboard.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	saved := `version: 1
id: preview
title: Preview
datasets:
  data:
    connection: warehouse
    query: SELECT 1 AS saved_value
    columns:
      - name: draft_value
        type: bigint
visualizations:
  - id: metric
    dataset: data
    definition:
      version: 1
      type: kpi
      value: {field: draft_value}
layout:
  - visualization: metric
    width: 6
    height: 4
`
	if err := os.WriteFile(path, []byte(saved), 0o644); err != nil {
		t.Fatal(err)
	}
	executed := make([]string, 0, 1)
	published := 0
	service := NewPresentationService(PresentationDependencies{
		WorkspaceRoot: root,
		CurrentState:  func() model.WorkspaceState { return model.WorkspaceState{} },
		NewConnectionManager: func(context.Context, string) (config.ConnectionAndDetailsGetter, error) {
			return &stubConnectionManager{connectionType: "duckdb"}, nil
		},
		RunConnectionQuery: func(_ context.Context, _, _ string, query string) ([]string, []map[string]any, error) {
			executed = append(executed, query)
			return []string{"draft_value"}, []map[string]any{{"draft_value": int64(2)}}, nil
		},
		PushWorkspaceUpdate: func(context.Context, string, string) { published++ },
	})
	document, apiErr := service.Get(context.Background(), EncodeID("dashboards/preview.dashboard.yml"))
	if apiErr != nil {
		t.Fatalf("get: %+v", apiErr)
	}
	draft := document.Artifact
	draft.Title = "Unsaved preview"
	draft.Datasets[0].Query = "SELECT 2 AS draft_value"
	result, apiErr := service.Preview(context.Background(), document.Artifact.WorkspaceID, model.PresentationPreviewRequest{
		ExpectedRevision: document.Artifact.Revision,
		Artifact:         draft,
		Environment:      "dev",
		VisualizationIDs: []string{"metric"},
	})
	if apiErr != nil {
		t.Fatalf("preview: %+v", apiErr)
	}
	if result.Status != "ok" || len(result.Findings) != 0 || result.Visualizations["metric"].Rows[0][0] != int64(2) {
		t.Fatalf("unexpected preview result: %+v", result)
	}
	if len(executed) != 1 || !strings.Contains(executed[0], "SELECT 2 AS draft_value") || strings.Contains(executed[0], "saved_value") {
		t.Fatalf("preview did not execute the draft query: %#v", executed)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != saved || published != 0 {
		t.Fatalf("preview mutated authored state: published=%d\n%s", published, after)
	}

	invalid := draft
	invalid.Visualizations = append([]model.PresentationVisualization(nil), draft.Visualizations...)
	invalid.Visualizations[0].Definition = map[string]any{"version": 1, "type": "kpi", "value": map[string]any{"field": "missing"}}
	invalidResult, apiErr := service.Preview(context.Background(), document.Artifact.WorkspaceID, model.PresentationPreviewRequest{
		ExpectedRevision: document.Artifact.Revision,
		Artifact:         invalid,
	})
	if apiErr != nil {
		t.Fatalf("invalid preview: %+v", apiErr)
	}
	if invalidResult.Status != "invalid" || len(invalidResult.Findings) == 0 || len(executed) != 1 {
		t.Fatalf("invalid preview should report findings without executing: %+v queries=%d", invalidResult, len(executed))
	}

	if _, apiErr := service.Preview(context.Background(), document.Artifact.WorkspaceID, model.PresentationPreviewRequest{
		ExpectedRevision: "v1:stale",
		Artifact:         draft,
	}); apiErr == nil || apiErr.Code != "presentation_preview_conflict" {
		t.Fatalf("expected preview revision conflict, got %+v", apiErr)
	}
}
