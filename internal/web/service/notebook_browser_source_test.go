package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"renart/internal/web/databrowser"
)

func TestNotebookBrowserSourceReviewIsExactAndDoesNotExecute(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".bruin.yml"), []byte("default_environment: default\nenvironments:\n  default:\n    connections:\n      duckdb:\n        - name: warehouse\n          path: default.duckdb\n  dev:\n    connections:\n      postgres:\n        - name: warehouse\n          host: localhost\n          username: test\n          database: test\n"), 0600))
	updates := 0
	svc := NewNotebookService(NotebookDependencies{WorkspaceRoot: root, PushWorkspaceUpdate: func(context.Context, string, string) { updates++ }})
	nb, apiErr := svc.Create(CreateNotebookRequest{Title: "Drops"})
	require.Nil(t, apiErr)
	updates = 0
	source := databrowser.NotebookSourceReference{Connection: "warehouse", Query: `select * from "public"."orders"`}
	req := NotebookBrowserSourceRequest{Environment: "dev", BaseRevision: nb.Revision, Position: "after", AfterBlockID: nb.Cells[0].CellID, SnapshotMode: "sample", RowLimit: 25, Name: "orders"}
	plan, apiErr := svc.PrepareBrowserSource(nb.ID, source, req)
	require.Nil(t, apiErr)
	require.True(t, plan.CanApply)
	require.Equal(t, "pg.sql", plan.ChangeSet.Operations[0].AssetType)
	require.Equal(t, "dev", plan.ChangeSet.Operations[0].Environment)
	unchanged, apiErr := svc.Get(nb.ID)
	require.Nil(t, apiErr)
	require.Equal(t, nb.Revision, unchanged.Revision)
	require.Zero(t, updates)
	require.NoDirExists(t, filepath.Join(root, ".renart", "notebook-transfers"))
	bad := plan.ChangeSet
	bad.Operations = append([]NotebookOperation(nil), bad.Operations...)
	bad.Operations[0].Content = "select 'different source'"
	_, apiErr = svc.ApplyBrowserSource(nb.ID, source, NotebookBrowserSourceRequest{Environment: "dev", ChangeSet: &bad})
	require.NotNil(t, apiErr)
	require.Zero(t, updates)
	changedSource := source
	changedSource.Query = "select * from other"
	_, apiErr = svc.ApplyBrowserSource(nb.ID, changedSource, NotebookBrowserSourceRequest{Environment: "dev", ChangeSet: &plan.ChangeSet})
	require.NotNil(t, apiErr)
	applied, apiErr := svc.ApplyBrowserSource(nb.ID, source, NotebookBrowserSourceRequest{Environment: "dev", ChangeSet: &plan.ChangeSet})
	require.Nil(t, apiErr)
	require.Equal(t, plan.ChangeSet.ExpectedRevision, applied.Notebook.Revision)
	require.Equal(t, nb.Cells[0].CellID, applied.Notebook.Blocks[0].Cell)
	require.Equal(t, plan.ChangeSet.Operations[0].CellID, applied.Notebook.Blocks[1].Cell)
	require.Equal(t, 1, updates)
	require.NoDirExists(t, filepath.Join(root, ".renart", "notebook-transfers"))
	_, apiErr = svc.ApplyBrowserSource(nb.ID, source, NotebookBrowserSourceRequest{Environment: "dev", ChangeSet: &plan.ChangeSet})
	require.Equal(t, 409, apiErr.Status)
	req.BaseRevision = applied.Notebook.Revision
	req.AfterBlockID = "deleted-anchor"
	_, apiErr = svc.PrepareBrowserSource(nb.ID, source, req)
	require.NotNil(t, apiErr)
}

func TestNotebookBrowserSourceFilesUseExistingSourceOperations(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0755))
	svc := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	nb, apiErr := svc.Create(CreateNotebookRequest{Title: "Files"})
	require.Nil(t, apiErr)
	source := databrowser.NotebookSourceReference{URI: "data/orders.csv", Format: "csv"}
	plan, apiErr := svc.PrepareBrowserSource(nb.ID, source, NotebookBrowserSourceRequest{Environment: "dev", BaseRevision: nb.Revision, Position: "start"})
	require.Nil(t, apiErr)
	require.Equal(t, "source.create", plan.ChangeSet.Operations[0].Kind)
	require.Equal(t, "full", plan.ChangeSet.Operations[0].Source.Snapshot.Mode)
	result, apiErr := svc.ApplyBrowserSource(nb.ID, source, NotebookBrowserSourceRequest{Environment: "dev", ChangeSet: &plan.ChangeSet})
	require.Nil(t, apiErr)
	require.Equal(t, plan.ChangeSet.Operations[0].CellID, result.Notebook.Blocks[0].Cell)
}

func TestNotebookBrowserSourceAdvertisesOnlyTypedWarehouseTransports(t *testing.T) {
	for _, connectionType := range []string{"duckdb", "postgres", "starrocks", "trino", "google_cloud_platform"} {
		require.True(t, SupportsNotebookWarehouseSource(connectionType), connectionType)
	}
	for _, connectionType := range []string{"sftp", "s3", "dremio", "stripe", "unknown"} {
		require.False(t, SupportsNotebookWarehouseSource(connectionType), connectionType)
	}
}
