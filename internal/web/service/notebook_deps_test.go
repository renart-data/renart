package service

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"renart/internal/web/notebook"
)

func pythonDependencyNotebook(t *testing.T) (*NotebookService, string, *notebook.Notebook) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, root, "notebooks/python/notebook.yml", "id: 11111111-0000-0000-0000-000000000203\nblocks:\n  - cell: python01\n  - cell: sqlcell1\n")
	writeWorkspaceFile(t, root, "notebooks/python/model.py", "\"\"\" @bruin\nid: python01\nclass: notebook\ntype: python\n@bruin \"\"\"\n\ndef materialize():\n    return None\n")
	writeWorkspaceFile(t, root, "notebooks/python/summary.sql", "/* @bruin\nid: sqlcell1\ntype: duckdb.sql\n@bruin */\nselect * from model\n")

	service := NewNotebookService(NotebookDependencies{WorkspaceRoot: root})
	notebookID := EncodeID("notebooks/python")
	nb, apiErr := service.load(notebookID)
	if apiErr != nil {
		t.Fatalf("load notebook: %+v", apiErr)
	}
	service.hydrateRuntime(nb)
	runtime := service.runtimes.get(nb.UUID)
	runtime.mu.Lock()
	for _, cell := range nb.Cells {
		runtime.results[cell.ID] = notebook.CellRunResult{
			CellID: cell.ID, Status: notebook.CellRunOK,
			Fingerprint: notebook.CellFingerprint(nb, cell),
		}
	}
	runtime.mu.Unlock()
	return service, notebookID, nb
}

func TestRuntimeReconcilesDirectPythonDependencyEdits(t *testing.T) {
	service, notebookID, nb := pythonDependencyNotebook(t)
	writeWorkspaceFile(t, service.deps.WorkspaceRoot, "notebooks/python/pyproject.toml", "[project]\nname = \"renart-notebook\"\ndependencies = [\"pandas\"]\n")

	runtime, apiErr := service.Runtime(notebookID)
	if apiErr != nil {
		t.Fatalf("load runtime: %+v", apiErr)
	}
	if !slices.Contains(runtime.Stale, "python01") || !slices.Contains(runtime.Stale, "sqlcell1") {
		t.Fatalf("dependency edit did not stale Python closure: %+v (notebook %+v)", runtime.Stale, nb.Cells)
	}
}

func TestRuntimeFingerprintReconciliationDetectsEachDirectEditOnce(t *testing.T) {
	service, notebookID, nb := pythonDependencyNotebook(t)
	runtime := service.runtimes.get(nb.UUID)

	writeWorkspaceFile(t, service.deps.WorkspaceRoot, "notebooks/python/pyproject.toml", "[project]\nname = \"renart-notebook\"\ndependencies = [\"pandas\"]\n")
	first, apiErr := service.load(notebookID)
	if apiErr != nil {
		t.Fatalf("load first edit: %+v", apiErr)
	}
	if !service.reconcileRuntimeFingerprints(first, runtime) {
		t.Fatal("first dependency edit was not detected")
	}
	if service.reconcileRuntimeFingerprints(first, runtime) {
		t.Fatal("unchanged dependency state was reconciled twice")
	}
	runtime.mu.Lock()
	runtime.autoFailed["python01"] = true
	runtime.mu.Unlock()

	writeWorkspaceFile(t, service.deps.WorkspaceRoot, "notebooks/python/pyproject.toml", "[project]\nname = \"renart-notebook\"\ndependencies = [\"polars\"]\n")
	second, apiErr := service.load(notebookID)
	if apiErr != nil {
		t.Fatalf("load second edit: %+v", apiErr)
	}
	if !service.reconcileRuntimeFingerprints(second, runtime) {
		t.Fatal("second dependency edit was hidden by the existing stale state")
	}
	runtime.mu.Lock()
	stillParked := runtime.autoFailed["python01"]
	runtime.mu.Unlock()
	if stillParked {
		t.Fatal("a direct edit did not clear the parked failure state")
	}
}

func TestUpdatePythonDependenciesMarksClosureOnceAndNoOpsIdenticalSave(t *testing.T) {
	service, notebookID, _ := pythonDependencyNotebook(t)
	if _, apiErr := service.UpdateDependencies(notebookID, "pandas\n"); apiErr != nil {
		t.Fatalf("update dependencies: %+v", apiErr)
	}
	runtime, apiErr := service.Runtime(notebookID)
	if apiErr != nil {
		t.Fatalf("load runtime: %+v", apiErr)
	}
	if !slices.Contains(runtime.Stale, "python01") || !slices.Contains(runtime.Stale, "sqlcell1") {
		t.Fatalf("dependency update did not stale Python closure: %+v", runtime.Stale)
	}

	fresh, loadErr := service.load(notebookID)
	if loadErr != nil {
		t.Fatalf("reload notebook: %+v", loadErr)
	}
	state := service.runtimes.get(fresh.UUID)
	state.mu.Lock()
	state.stale = map[string]bool{}
	for _, cell := range fresh.Cells {
		result := state.results[cell.ID]
		result.Fingerprint = notebook.CellFingerprint(fresh, cell)
		state.results[cell.ID] = result
	}
	state.mu.Unlock()

	if _, apiErr := service.UpdateDependencies(notebookID, "pandas\n"); apiErr != nil {
		t.Fatalf("repeat dependencies: %+v", apiErr)
	}
	runtime, apiErr = service.Runtime(notebookID)
	if apiErr != nil {
		t.Fatalf("reload runtime: %+v", apiErr)
	}
	if len(runtime.Stale) != 0 {
		t.Fatalf("identical dependency save marked cells stale: %+v", runtime.Stale)
	}
}
