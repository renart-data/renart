package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"renart/internal/web/notebook"
)

func controlOptionNotebook(t *testing.T) (*NotebookService, string, *notebook.Notebook) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "notebooks/options/notebook.yml", `version: 2
id: 11111111-0000-0000-0000-000000000302
parameters:
  - id: region
    label: Region
    type: select
    default: de
    options:
      dataset: source01
      value_field: code
      label_field: label
blocks:
  - cell: source01
  - control: region
`)
	writeWorkspaceFile(t, root, "notebooks/options/regions.sql", "/* @bruin\nid: source01\ntype: duckdb.sql\n@bruin */\nselect 'de' as code, 'Germany' as label\n")

	service := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	notebookID := EncodeID("notebooks/options")
	nb, apiErr := service.load(notebookID)
	if apiErr != nil {
		t.Fatalf("load notebook: %+v", apiErr)
	}
	session, err := service.store.Open(nb.UUID)
	if err != nil {
		t.Fatal(err)
	}
	object := `"` + notebook.CellObjectName("source01") + `"`
	if err := session.Exec(context.Background(), "create table "+object+" (code varchar, label varchar)"); err != nil {
		t.Fatal(err)
	}
	if err := session.Exec(context.Background(), "insert into "+object+" values ('de', 'Germany'), ('us', 'United States')"); err != nil {
		t.Fatal(err)
	}
	session.Close()

	service.hydrateRuntime(nb)
	runtime := service.runtimes.get(nb.UUID)
	runtime.mu.Lock()
	runtime.results["source01"] = notebook.CellRunResult{
		CellID: "source01", Status: notebook.CellRunOK, Fingerprint: notebook.CellFingerprint(nb, nb.CellByID("source01")),
	}
	runtime.mu.Unlock()
	return service, notebookID, nb
}

func TestRefreshControlOptionsReadsOnlyFreshLocalProducer(t *testing.T) {
	service, notebookID, nb := controlOptionNotebook(t)
	result, apiErr := service.RefreshControlOptions(context.Background(), notebookID, "region")
	if apiErr != nil {
		t.Fatalf("refresh options: %+v", apiErr)
	}
	if result.Status != "ok" || result.Dataset != "source01" || len(result.Rows) != 2 {
		t.Fatalf("unexpected option result: %+v", result)
	}

	runtime := service.runtimes.get(nb.UUID)
	runtime.mu.Lock()
	runtime.stale["source01"] = true
	runtime.mu.Unlock()
	if _, apiErr := service.RefreshControlOptions(context.Background(), notebookID, "region"); apiErr == nil || apiErr.Code != "notebook_control_options_stale" {
		t.Fatalf("expected stale producer rejection, got %+v", apiErr)
	}

	runtime.mu.Lock()
	runtime.stale["source01"] = false
	runtime.mu.Unlock()
	_, finishRun := runtime.beginManualRun(context.Background(), []string{"source01"})
	if _, apiErr := service.RefreshControlOptions(context.Background(), notebookID, "region"); apiErr == nil || apiErr.Code != "notebook_control_options_running" {
		t.Fatalf("expected running producer rejection, got %+v", apiErr)
	}
	finishRun()
}

func TestRefreshControlOptionsRejectsUnconfiguredControl(t *testing.T) {
	service, notebookID, _ := controlOptionNotebook(t)
	if _, apiErr := service.RefreshControlOptions(context.Background(), notebookID, "missing"); apiErr == nil || apiErr.Code != "notebook_control_not_found" {
		t.Fatalf("expected missing control error, got %+v", apiErr)
	}
}
