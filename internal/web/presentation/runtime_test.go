package presentation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"renart/internal/web/model"
)

type runtimeConnectionLookup struct {
	connectionType string
}

func (lookup runtimeConnectionLookup) GetConnectionType(string) string {
	return lookup.connectionType
}

func TestRenderQueryUsesTypedBindingsAndWarehouseLimit(t *testing.T) {
	definitions := []FilterDefinition{
		{ID: "region", Type: ParameterTypeText, Default: "eu"},
		{ID: "active", Type: ParameterTypeBoolean, Default: true},
	}
	values, findings := ResolveParameterValues(definitions, map[string]any{
		"region": "O'Reilly%_!", "active": true,
	})
	if problem := FirstError(findings); problem != nil {
		t.Fatalf("resolve filter values: %+v", problem)
	}
	literals, err := ParameterSQLLiterals(definitions, values)
	if err != nil {
		t.Fatal(err)
	}
	query, err := RenderQuery(
		"SELECT region, active FROM sales;", "mssql", definitions, values, literals,
		[]FilterBinding{
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
}

func TestRuntimeServiceExecutesQueryDatasetWithoutServiceFacade(t *testing.T) {
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
    query: SELECT region, revenue FROM monthly_sales
    columns:
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
  - id: revenue
    dataset: sales
    definition:
      version: 1
      type: table
    filter_bindings:
      - filter: region
        column: region
        operator: equals
layout:
  - visualization: revenue
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	queries := make([]string, 0, 1)
	documents := NewDocumentService(DocumentDependencies{
		WorkspaceRoot: root,
		Enrich: func(ctx context.Context, artifact *Artifact) map[string]ResolvedSchema {
			schemas := map[string]ResolvedSchema{
				"sales": {
					Source: DataSourceRef{Kind: "dataset", ArtifactID: artifact.ID, ComponentID: "sales"},
					Columns: []ResolvedColumn{
						{Name: "region", PhysicalType: "varchar", SemanticType: SemanticCategorical},
						{Name: "revenue", PhysicalType: "bigint", SemanticType: SemanticNumeric},
					},
					Complete: true,
				},
			}
			artifact.Problems = (Checker{}).CheckArtifact(ctx, *artifact, schemas, CheckOptions{Strict: true})
			return schemas
		},
	})
	runtime := NewRuntimeService(RuntimeDependencies{
		Documents: documents,
		NewConnectionLookup: func(context.Context, string) (ConnectionTypeLookup, error) {
			return runtimeConnectionLookup{connectionType: "duckdb"}, nil
		},
		RunConnectionQuery: func(_ context.Context, connection, environment, query string) ([]string, []map[string]any, error) {
			if connection != "warehouse" || environment != "dev" {
				t.Fatalf("unexpected runtime target connection=%q environment=%q", connection, environment)
			}
			queries = append(queries, query)
			return []string{"region", "revenue"}, []map[string]any{{"region": "us", "revenue": int64(42)}}, nil
		},
	})

	result, apiErr := runtime.Run(context.Background(), encodeWorkspaceID("dashboards/sales.dashboard.yml"), model.PresentationRunRequest{
		Environment: "dev", FilterValues: map[string]any{"region": "us"},
	})
	if apiErr != nil {
		t.Fatalf("run: %+v", apiErr)
	}
	if result.Status != "ok" || result.Visualizations["revenue"].Rows[0][1] != int64(42) {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(queries) != 1 || !strings.Contains(queries[0], `"region" = 'us'`) || !strings.Contains(queries[0], "LIMIT 1001") {
		t.Fatalf("unexpected runtime queries: %#v", queries)
	}
}
