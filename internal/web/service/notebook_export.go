package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"renart/internal/web/notebook"
)

// NotebookCellExport is a private staged result ready for one HTTP response.
// The handler must call Cleanup after streaming it.
type NotebookCellExport struct {
	Path        string
	Filename    string
	ContentType string
	cleanupDir  string
}

// Cleanup removes the private one-response staging directory.
func (export NotebookCellExport) Cleanup() {
	if export.cleanupDir != "" {
		_ = os.RemoveAll(export.cleanupDir)
	}
}

var notebookExportFilenameSanitizer = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// ExportCell stages the current complete cell relation as CSV or Parquet.
func (s *NotebookService) ExportCell(
	ctx context.Context,
	notebookID, cellID, rawFormat string,
) (NotebookCellExport, *APIError) {
	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return NotebookCellExport{}, apiErr
	}
	cell := nb.CellByID(cellID)
	if cell == nil {
		return NotebookCellExport{}, &APIError{Status: http.StatusNotFound, Code: "cell_not_found", Message: "cell not found"}
	}
	parameterValues := s.currentNotebookParameterValues(nb)

	format := notebook.CellExportFormat(strings.ToLower(strings.TrimSpace(rawFormat)))
	contentType := "text/csv; charset=utf-8"
	if format == "" {
		format = notebook.CellExportCSV
	}
	if format == notebook.CellExportParquet {
		contentType = "application/vnd.apache.parquet"
	} else if format != notebook.CellExportCSV {
		return NotebookCellExport{}, &APIError{Status: http.StatusBadRequest, Code: "invalid_notebook_export_format", Message: "format must be csv or parquet"}
	}

	exportRoot := filepath.Join(s.deps.WorkspaceRoot, ".renart", "notebook-exports")
	if err := os.MkdirAll(exportRoot, 0o700); err != nil {
		return NotebookCellExport{}, &APIError{Status: http.StatusInternalServerError, Code: "notebook_export_failed", Message: err.Error()}
	}
	if err := os.Chmod(exportRoot, 0o700); err != nil {
		return NotebookCellExport{}, &APIError{Status: http.StatusInternalServerError, Code: "notebook_export_failed", Message: err.Error()}
	}
	stagingDir, err := os.MkdirTemp(exportRoot, "export-")
	if err != nil {
		return NotebookCellExport{}, &APIError{Status: http.StatusInternalServerError, Code: "notebook_export_failed", Message: err.Error()}
	}
	export := NotebookCellExport{cleanupDir: stagingDir, ContentType: contentType}
	failure := true
	defer func() {
		if failure {
			export.Cleanup()
		}
	}()

	extension := string(format)
	baseName := notebookExportFilenameSanitizer.ReplaceAllString(strings.TrimSpace(cell.Asset.Name), "_")
	baseName = strings.Trim(baseName, "._-")
	if baseName == "" {
		baseName = "notebook-result"
	}
	export.Filename = fmt.Sprintf("%s.%s", baseName, extension)
	export.Path = filepath.Join(stagingDir, "result."+extension)
	if err := s.store.ExportCell(ctx, nb, cellID, format, export.Path, parameterValues); err != nil {
		switch {
		case errors.Is(err, notebook.ErrCellResultUnavailable):
			return NotebookCellExport{}, &APIError{Status: http.StatusConflict, Code: "notebook_result_unavailable", Message: "Run this cell successfully before exporting its result."}
		case errors.Is(err, notebook.ErrCellResultStale):
			return NotebookCellExport{}, &APIError{Status: http.StatusConflict, Code: "notebook_result_stale", Message: "This result is stale. Run the cell again before exporting it."}
		default:
			return NotebookCellExport{}, &APIError{Status: http.StatusInternalServerError, Code: "notebook_export_failed", Message: err.Error()}
		}
	}
	failure = false
	return export, nil
}
