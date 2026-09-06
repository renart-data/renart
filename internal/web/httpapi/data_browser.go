package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/apperror"
	"renart/internal/web/databrowser"
	"renart/internal/web/service"
)

type DataBrowserHandlers interface {
	Resolve(ctx context.Context, request databrowser.ResolveRequest) (databrowser.ObjectResponse, *apperror.Error)
	Connections(ctx context.Context, environment string) (databrowser.ConnectionsResponse, *apperror.Error)
	Children(ctx context.Context, connectionID, parentID, environment string) (databrowser.ChildrenResponse, *apperror.Error)
	Object(ctx context.Context, objectID, environment string) (databrowser.ObjectResponse, *apperror.Error)
	Preview(ctx context.Context, request databrowser.PreviewRequest) (databrowser.PreviewResponse, *apperror.Error)
}

type DataBrowserAPI struct {
	Service DataBrowserHandlers
	Sources interface {
		ImportDataBrowserSource(context.Context, string, databrowser.Object, bool, bool) (service.ExternalRelationImportResult, *service.APIError)
	}
	Publisher PipelineChangePublisher
}

func RegisterDataBrowserRoutes(router chi.Router, handlers *DataBrowserAPI) {
	router.Get("/api/data-browser/connections", handlers.HandleConnections)
	router.Get("/api/data-browser/connections/{connectionID}/children", handlers.HandleChildren)
	router.Get("/api/data-browser/objects/{objectID}", handlers.HandleObject)
	router.Post("/api/data-browser/preview", handlers.HandlePreview)
	router.Post("/api/data-browser/resolve", handlers.HandleResolve)
	router.Post("/api/pipelines/{id}/data-browser/sources/preview", handlers.HandlePreviewSource)
	router.Post("/api/pipelines/{id}/data-browser/sources", handlers.HandleCreateSource)
}

func (h *DataBrowserAPI) HandlePreviewSource(w http.ResponseWriter, r *http.Request) {
	h.handleSource(w, r, true)
}

func (h *DataBrowserAPI) HandleCreateSource(w http.ResponseWriter, r *http.Request) {
	h.handleSource(w, r, false)
}

func (h *DataBrowserAPI) handleSource(w http.ResponseWriter, r *http.Request, preview bool) {
	request, err := decodeJSONObject[service.DataBrowserSourceRequest](w, r, 16<<10)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	if h.Sources == nil || strings.TrimSpace(request.Environment) == "" || strings.TrimSpace(request.ObjectID) == "" {
		webapi.WriteBadRequest(w, "source_reference_required", "Choose a table and environment before creating a Source asset.")
		return
	}
	object, apiErr := h.Service.Object(r.Context(), request.ObjectID, request.Environment)
	if apiErr != nil {
		writeDataBrowserError(w, apiErr)
		return
	}
	columns := request.IncludeColumns == nil || *request.IncludeColumns
	result, importErr := h.Sources.ImportDataBrowserSource(r.Context(), chi.URLParam(r, "id"), object.Object, preview, columns)
	if importErr != nil {
		webapi.WriteJSON(w, importErr.Status, ErrorResponse{Status: "error", Error: ErrorResponseBody{Code: importErr.Code, Message: importErr.Message}})
		return
	}
	status := http.StatusOK
	if !preview {
		status = http.StatusCreated
		if h.Publisher != nil {
			h.Publisher.WorkspaceChanged(r.Context(), result.PipelinePath, "pipeline.source-created")
		}
	}
	webapi.WriteJSON(w, status, result)
}

func (h *DataBrowserAPI) HandleResolve(w http.ResponseWriter, r *http.Request) {
	request, err := decodeJSONObject[databrowser.ResolveRequest](w, r, 16<<10)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	response, apiErr := h.Service.Resolve(r.Context(), request)
	if apiErr != nil {
		writeDataBrowserError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, response)
}

func (h *DataBrowserAPI) HandleConnections(w http.ResponseWriter, r *http.Request) {
	response, apiErr := h.Service.Connections(r.Context(), strings.TrimSpace(r.URL.Query().Get("environment")))
	if apiErr != nil {
		writeDataBrowserError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, response)
}

func (h *DataBrowserAPI) HandleChildren(w http.ResponseWriter, r *http.Request) {
	response, apiErr := h.Service.Children(
		r.Context(),
		chi.URLParam(r, "connectionID"),
		strings.TrimSpace(r.URL.Query().Get("parent_id")),
		strings.TrimSpace(r.URL.Query().Get("environment")),
	)
	if apiErr != nil {
		writeDataBrowserError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, response)
}

func (h *DataBrowserAPI) HandleObject(w http.ResponseWriter, r *http.Request) {
	response, apiErr := h.Service.Object(
		r.Context(),
		chi.URLParam(r, "objectID"),
		strings.TrimSpace(r.URL.Query().Get("environment")),
	)
	if apiErr != nil {
		writeDataBrowserError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, response)
}

func (h *DataBrowserAPI) HandlePreview(w http.ResponseWriter, r *http.Request) {
	request, err := decodeJSONObject[databrowser.PreviewRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	response, apiErr := h.Service.Preview(r.Context(), request)
	if apiErr != nil {
		writeDataBrowserError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, response)
}

func writeDataBrowserError(w http.ResponseWriter, apiErr *apperror.Error) {
	webapi.WriteJSON(w, apiErr.Status, ErrorResponse{
		Status: "error",
		Error:  ErrorResponseBody{Code: apiErr.Code, Message: apiErr.Message},
	})
}
