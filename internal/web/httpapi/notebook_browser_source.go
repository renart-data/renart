package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/apperror"
	"renart/internal/web/databrowser"
	"renart/internal/web/service"
)

type NotebookBrowserSourceAPI struct {
	Browser interface {
		NotebookSource(context.Context, string, string) (databrowser.NotebookSourceReference, *apperror.Error)
	}
	Notebooks interface {
		PrepareBrowserSource(string, databrowser.NotebookSourceReference, service.NotebookBrowserSourceRequest) (service.NotebookChangePlan, *service.APIError)
		ApplyBrowserSource(string, databrowser.NotebookSourceReference, service.NotebookBrowserSourceRequest) (service.NotebookChangeApplyResult, *service.APIError)
	}
}

func RegisterNotebookBrowserSourceRoutes(router chi.Router, h *NotebookBrowserSourceAPI) {
	router.Post("/api/notebooks/{id}/data-browser/prepare", h.Prepare)
	router.Post("/api/notebooks/{id}/data-browser/apply", h.Apply)
}
func (h *NotebookBrowserSourceAPI) Prepare(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, false)
}
func (h *NotebookBrowserSourceAPI) Apply(w http.ResponseWriter, r *http.Request) {
	h.handle(w, r, true)
}
func (h *NotebookBrowserSourceAPI) handle(w http.ResponseWriter, r *http.Request, apply bool) {
	req, err := decodeJSONObject[service.NotebookBrowserSourceRequest](w, r, 128<<10)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	source, apiErr := h.Browser.NotebookSource(ctx, req.ObjectID, req.Environment)
	if apiErr != nil {
		writeDataBrowserError(w, apiErr)
		return
	}
	if ctx.Err() != nil {
		webapi.WriteBadRequest(w, "notebook_browser_cancelled", "Source discovery was cancelled. Try again.")
		return
	}
	if apply {
		result, apiErr := h.Notebooks.ApplyBrowserSource(chi.URLParam(r, "id"), source, req)
		if apiErr != nil {
			writeDataBrowserError(w, apiErr)
			return
		}
		webapi.WriteJSON(w, http.StatusCreated, result)
	} else {
		result, apiErr := h.Notebooks.PrepareBrowserSource(chi.URLParam(r, "id"), source, req)
		if apiErr != nil {
			writeDataBrowserError(w, apiErr)
			return
		}
		webapi.WriteJSON(w, http.StatusOK, result)
	}
}
