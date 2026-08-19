package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"renart/internal/web/model"
)

func TestPresentationServiceCreateGetAndRevisionedUpdate(t *testing.T) {
	root := t.TempDir()
	updates := 0
	service := NewPresentationService(PresentationDependencies{
		WorkspaceRoot: root,
		CurrentState: func() model.WorkspaceState {
			return model.WorkspaceState{Pipelines: []model.Pipeline{{
				UUID: "pipeline-id",
				Assets: []model.Asset{{
					ID: "asset-id", Name: "analytics.monthly_sales",
					Columns: []model.Column{{Name: "month", Type: "date"}, {Name: "revenue", Type: "numeric"}},
				}},
			}}}
		},
		PushWorkspaceUpdate: func(context.Context, string, string) { updates++ },
	})

	created, apiErr := service.Create(context.Background(), CreatePresentationRequest{Kind: "dashboard", Title: "Sales Overview"})
	if apiErr != nil {
		t.Fatalf("create: %+v", apiErr)
	}
	if created.Artifact.ID != "sales_overview" || created.Artifact.WorkspaceID == "" || updates != 1 {
		t.Fatalf("unexpected create result: %+v updates=%d", created, updates)
	}
	if _, err := os.Stat(filepath.Join(root, "dashboards", "sales_overview.dashboard.yml")); err != nil {
		t.Fatalf("dashboard file missing: %v", err)
	}

	draft := `version: 1
id: sales_overview
title: Sales overview
datasets:
  monthly:
    asset: analytics.monthly_sales
visualizations:
  - id: revenue
    dataset: monthly
    definition:
      version: 1
      type: line
      encoding:
        x:
          field: month
        y:
          - field: revenue
layout:
  - visualization: revenue
`
	updated, apiErr := service.Update(context.Background(), created.Artifact.WorkspaceID, UpdatePresentationRequest{
		ExpectedRevision: created.Artifact.Revision,
		Content:          draft,
	})
	if apiErr != nil {
		t.Fatalf("update: %+v", apiErr)
	}
	if updated.Artifact.Revision == created.Artifact.Revision || len(updated.Artifact.Problems) != 0 || updates != 2 {
		t.Fatalf("unexpected update result: %+v updates=%d", updated, updates)
	}

	loaded, apiErr := service.Get(context.Background(), created.Artifact.WorkspaceID)
	if apiErr != nil || loaded.Content != draft {
		t.Fatalf("get: document=%+v error=%+v", loaded, apiErr)
	}
	visualSnapshot := updated.Artifact
	visualSnapshot.Title = "Sales performance"
	replaced, apiErr := service.Replace(context.Background(), created.Artifact.WorkspaceID, ReplacePresentationRequest{
		ExpectedRevision: updated.Artifact.Revision, Artifact: visualSnapshot,
	})
	if apiErr != nil || replaced.Artifact.Title != "Sales performance" || !strings.Contains(replaced.Content, "title: Sales performance") {
		t.Fatalf("replace typed definition: document=%+v error=%+v", replaced, apiErr)
	}
	if _, apiErr := service.Update(context.Background(), created.Artifact.WorkspaceID, UpdatePresentationRequest{
		ExpectedRevision: created.Artifact.Revision, Content: draft + "\n",
	}); apiErr == nil || apiErr.Code != "presentation_edit_conflict" {
		t.Fatalf("stale update did not conflict: %+v", apiErr)
	}
}

func TestPresentationServiceKeepsInvalidDraftOutOfFilesystem(t *testing.T) {
	root := t.TempDir()
	service := NewPresentationService(PresentationDependencies{WorkspaceRoot: root})
	created, apiErr := service.Create(context.Background(), CreatePresentationRequest{Kind: "report", Title: "Weekly"})
	if apiErr != nil {
		t.Fatalf("create: %+v", apiErr)
	}
	before := created.Content
	_, apiErr = service.Update(context.Background(), created.Artifact.WorkspaceID, UpdatePresentationRequest{
		ExpectedRevision: created.Artifact.Revision,
		Content:          "version: [broken",
	})
	if apiErr == nil || apiErr.Code != "presentation_draft_invalid" {
		t.Fatalf("malformed draft was accepted: %+v", apiErr)
	}
	loaded, apiErr := service.Get(context.Background(), created.Artifact.WorkspaceID)
	if apiErr != nil || loaded.Content != before {
		t.Fatalf("malformed draft replaced the good file: document=%+v error=%+v", loaded, apiErr)
	}

	changedID := strings.Replace(before, "id: weekly", "id: renamed", 1)
	_, apiErr = service.Update(context.Background(), created.Artifact.WorkspaceID, UpdatePresentationRequest{
		ExpectedRevision: created.Artifact.Revision, Content: changedID,
	})
	if apiErr == nil || apiErr.Code != "presentation_id_immutable" {
		t.Fatalf("identity edit was accepted: %+v", apiErr)
	}
}
