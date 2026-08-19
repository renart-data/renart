package service

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"

	"renart/internal/web/model"
	"renart/internal/web/notebook"
)

// readNotebookDependencies returns the `[project].dependencies` list from a
// notebook's pyproject.toml, or nil when there is none.
func readNotebookDependencies(dir string) []string {
	return readPyprojectDependencies(filepath.Join(dir, pyprojectFile))
}

// writeNotebookDependencies sets `[project].dependencies` in the notebook's
// pyproject.toml, preserving any other tables, and creating a minimal project
// table when the file is new.
func writeNotebookDependencies(dir string, deps []string) error {
	return writePyprojectDependencies(filepath.Join(dir, pyprojectFile), "renart-notebook", deps)
}

// dependenciesFromText splits a newline-separated dependency list (the dialog's
// edit format) into trimmed, comment-free package specifiers.
func dependenciesFromText(content string) []string {
	deps := make([]string, 0)
	for _, line := range strings.Split(content, "\n") {
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			deps = append(deps, trimmed)
		}
	}
	return deps
}

// UpdateDependencies replaces the notebook's Python dependencies (pyproject.toml
// `[project].dependencies`) and returns the refreshed notebook.
func (s *NotebookService) UpdateDependencies(notebookID, content string) (model.Notebook, *APIError) {
	unlockNotebook := s.lockNotebookEdit(notebookID)
	defer unlockNotebook()

	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return model.Notebook{}, apiErr
	}
	dependencies := dependenciesFromText(content)
	if slices.Equal(readNotebookDependencies(nb.Dir), dependencies) {
		return s.toModel(nb), nil
	}
	if err := writeNotebookDependencies(nb.Dir, dependencies); err != nil {
		return model.Notebook{}, &APIError{Status: http.StatusInternalServerError, Code: "dependencies_update_failed", Message: err.Error()}
	}
	fresh, loadErr := s.load(notebookID)
	if loadErr != nil {
		return model.Notebook{}, loadErr
	}
	pythonCellIDs := make([]string, 0)
	for _, cell := range fresh.Cells {
		if notebook.IsPythonCell(cell) {
			pythonCellIDs = append(pythonCellIDs, cell.ID)
		}
	}
	s.onCellsChanged(notebookID, fresh, pythonCellIDs)
	s.pushUpdate(filepath.Join(nb.Dir, pyprojectFile))
	return s.toModel(fresh), nil
}
