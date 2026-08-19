package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"renart/internal/web/model"
	"renart/internal/web/presentation"
)

func TestNotebookVisualizationCheckUsesStaticSourceSchema(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	created, apiErr := svc.Create(CreateNotebookRequest{Title: "Typed presentation"})
	if apiErr != nil {
		t.Fatalf("create notebook: %+v", apiErr)
	}
	source := created.Cells[0].CellID

	checked, apiErr := svc.CheckVisualization(context.Background(), created.ID, NotebookVisualizationCheckRequest{
		Source: source,
		DefinitionYAML: `version: 1
type: line
encoding:
  x:
    field: greeting
  y:
    - field: answer
`,
	})
	if apiErr != nil {
		t.Fatalf("check visualization: %+v", apiErr)
	}
	if !checked.CanApply || len(checked.Schema.Columns) != 2 {
		t.Fatalf("expected applicable statically typed visualization: %+v", checked)
	}
	if checked.Schema.Columns[0].Name != "greeting" || checked.Schema.Columns[1].Name != "answer" {
		t.Fatalf("unexpected inferred schema: %+v", checked.Schema.Columns)
	}

	incompatible, apiErr := svc.CheckVisualization(context.Background(), created.ID, NotebookVisualizationCheckRequest{
		Source: source,
		Definition: map[string]any{
			"version": 1,
			"type":    "line",
			"encoding": map[string]any{
				"x": map[string]any{"field": "answer"},
				"y": []any{map[string]any{"field": "greeting"}},
			},
		},
	})
	if apiErr != nil {
		t.Fatalf("check incompatible visualization: %+v", apiErr)
	}
	if incompatible.CanApply || !findingWithCode(incompatible.Findings, "visualization-field-type-incompatible") {
		t.Fatalf("known incompatible field was not rejected: %+v", incompatible)
	}
}

func TestNotebookVisualizationChangeSetCannotBypassChecker(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	created, apiErr := svc.Create(CreateNotebookRequest{Title: "Checked changes"})
	if apiErr != nil {
		t.Fatalf("create notebook: %+v", apiErr)
	}
	plan, apiErr := svc.PrepareChangeSet(created.ID, NotebookChangeSet{
		BaseRevision: created.Revision,
		Operations: []NotebookOperation{{
			Kind: NotebookOperationVisualizationCreate,
			Visualization: &model.NotebookVisualization{
				Source: created.Cells[0].CellID,
				Definition: map[string]any{
					"version": 1,
					"type":    "line",
					"encoding": map[string]any{
						"x": map[string]any{"field": "answer"},
						"y": []any{map[string]any{"field": "greeting"}},
					},
				},
			},
		}},
	})
	if apiErr != nil {
		t.Fatalf("prepare should return a reviewable invalid plan: %+v", apiErr)
	}
	if plan.CanApply || len(plan.BlockingProblems) == 0 {
		t.Fatalf("incompatible visualization change bypassed backend checker: %+v", plan)
	}
}

func findingWithCode(findings []presentation.Finding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
