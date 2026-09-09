package service

import (
	"context"
	"errors"
	"net/http"

	"renart/internal/web/notebook"
)

// renart:web
type NotebookPreviewRequest struct {
	ResultID    string `json:"result_id"`
	Environment string `json:"environment"`
	Limit       int    `json:"limit"`
}

func (s *NotebookService) PreviewCell(ctx context.Context, notebookID, cellID string, req NotebookPreviewRequest) (notebook.CellPreviewResult, *APIError) {
	nb, apiErr := s.load(notebookID)
	if apiErr != nil {
		return notebook.CellPreviewResult{}, apiErr
	}
	if req.ResultID == "" {
		return notebook.CellPreviewResult{}, badRequestError("preview_result_required", "A saved result identity is required.")
	}
	result, err := s.store.ReadPreview(ctx, nb.UUID, cellID, req.ResultID, req.Environment, req.Limit, func() (*notebook.Notebook, map[string]any, error) {
		current, apiErr := s.load(notebookID)
		if apiErr != nil {
			return nil, nil, notebook.ErrPreviewExpired
		}
		// Do not hydrate while holding the session lock: hydration also opens
		// the session. A valid process-local generation was already run/hydrated.
		rt := s.runtimes.get(current.UUID)
		rt.mu.Lock()
		params := cloneNotebookParameterValues(rt.parameterValues)
		rt.mu.Unlock()
		return current, params, nil
	})
	if err != nil {
		if errors.Is(err, notebook.ErrPreviewExpired) {
			return notebook.CellPreviewResult{}, &APIError{Status: http.StatusConflict, Code: "notebook_preview_expired", Message: "This saved preview expired or was replaced. Run the cell explicitly to create a new preview."}
		}
		if ctx.Err() != nil {
			return notebook.CellPreviewResult{}, &APIError{Status: http.StatusRequestTimeout, Code: "notebook_preview_cancelled", Message: "Preview loading was cancelled."}
		}
		return notebook.CellPreviewResult{}, &APIError{Status: http.StatusInternalServerError, Code: "notebook_preview_failed", Message: "Could not read the saved preview. Your displayed rows are unchanged."}
	}
	return result, nil
}
