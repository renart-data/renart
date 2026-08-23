package presentation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentServiceOwnsRevisionedFileLifecycle(t *testing.T) {
	root := t.TempDir()
	updates := 0
	service := NewDocumentService(DocumentDependencies{
		WorkspaceRoot: root,
		PushWorkspaceUpdate: func(context.Context, string, string) {
			updates++
		},
	})

	created, apiErr := service.Create(context.Background(), CreatePresentationRequest{
		Kind: "dashboard", Title: "Sales Overview",
	})
	if apiErr != nil {
		t.Fatalf("create: %+v", apiErr)
	}
	if created.Artifact.ID != "sales_overview" || updates != 1 {
		t.Fatalf("unexpected create result: %+v updates=%d", created, updates)
	}

	updatedContent := strings.Replace(created.Content, "title: Sales Overview", "title: Sales performance", 1)
	updated, apiErr := service.Update(context.Background(), created.Artifact.WorkspaceID, UpdatePresentationRequest{
		ExpectedRevision: created.Artifact.Revision,
		Content:          updatedContent,
	})
	if apiErr != nil {
		t.Fatalf("update: %+v", apiErr)
	}
	if updated.Artifact.Revision == created.Artifact.Revision || updated.Artifact.Title != "Sales performance" || updates != 2 {
		t.Fatalf("unexpected update result: %+v updates=%d", updated, updates)
	}

	_, apiErr = service.Update(context.Background(), created.Artifact.WorkspaceID, UpdatePresentationRequest{
		ExpectedRevision: created.Artifact.Revision,
		Content:          created.Content,
	})
	if apiErr == nil || apiErr.Code != "presentation_edit_conflict" {
		t.Fatalf("stale update did not conflict: %+v", apiErr)
	}

	content, err := os.ReadFile(filepath.Join(root, "dashboards", "sales_overview.dashboard.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != updatedContent {
		t.Fatalf("stale update changed the file:\n%s", content)
	}
}

func TestDocumentServiceRejectsWorkspacePathTraversal(t *testing.T) {
	service := NewDocumentService(DocumentDependencies{WorkspaceRoot: t.TempDir()})
	_, apiErr := service.Get(context.Background(), encodeWorkspaceID("../outside.dashboard.yml"))
	if apiErr == nil || apiErr.Code != "presentation_id_invalid" {
		t.Fatalf("path traversal was accepted: %+v", apiErr)
	}
}
