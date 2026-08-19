package service

import (
	"testing"

	"renart/internal/web/model"
)

func TestResolveNotebookAgentReferencesUsesCanonicalWorkspaceContext(t *testing.T) {
	t.Parallel()

	notebook := model.Notebook{Cells: []model.Asset{{
		CellID: "cell-one", Name: "daily_sales", Type: "duckdb.sql",
	}}}
	workspace := WorkspaceState{Pipelines: []model.Pipeline{{
		ID: "pipeline-one",
		Assets: []model.Asset{{
			ID: "asset-one", Name: "analytics.orders", Type: "pg.sql", Connection: "postgres-other",
		}},
	}}}

	resolved, apiErr := ResolveNotebookAgentReferences(notebook, workspace, []NotebookAgentReferenceRequest{
		{Kind: "cell", ID: "cell-one"},
		{Kind: "asset", ID: "asset-one"},
		{Kind: "asset", ID: "asset-one"},
	})
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if len(resolved) != 2 {
		t.Fatalf("resolved references = %+v", resolved)
	}
	if got := resolved[0]; got.Label != "daily_sales" || got.Detail != "duckdb.sql" {
		t.Fatalf("cell reference = %+v", got)
	}
	if got := resolved[1]; got.Label != "analytics.orders" || got.Detail != "pg.sql, connection postgres-other" {
		t.Fatalf("asset reference = %+v", got)
	}
}

func TestResolveNotebookAgentReferencesRejectsOutOfScopeTargets(t *testing.T) {
	t.Parallel()

	notebook := model.Notebook{Cells: []model.Asset{{CellID: "cell-one", Name: "daily_sales"}}}
	if _, apiErr := ResolveNotebookAgentReferences(notebook, WorkspaceState{}, []NotebookAgentReferenceRequest{{
		Kind: "cell", ID: "cell-from-another-notebook",
	}}); apiErr == nil || apiErr.Code != "notebook_agent_cell_reference_not_found" {
		t.Fatalf("out-of-scope cell error = %+v", apiErr)
	}
	if _, apiErr := ResolveNotebookAgentReferences(notebook, WorkspaceState{}, []NotebookAgentReferenceRequest{{
		Kind: "dashboard", ID: "dashboard-one",
	}}); apiErr == nil || apiErr.Code != "notebook_agent_reference_kind_invalid" {
		t.Fatalf("unsupported reference error = %+v", apiErr)
	}
}
