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
