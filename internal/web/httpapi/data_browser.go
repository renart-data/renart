package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/apperror"
	"renart/internal/web/databrowser"
)

type DataBrowserHandlers interface {
	Connections(ctx context.Context, environment string) (databrowser.ConnectionsResponse, *apperror.Error)
	Children(ctx context.Context, connectionID, parentID, environment string) (databrowser.ChildrenResponse, *apperror.Error)
	Object(ctx context.Context, objectID, environment string) (databrowser.ObjectResponse, *apperror.Error)
	Preview(ctx context.Context, request databrowser.PreviewRequest) (databrowser.PreviewResponse, *apperror.Error)
}

type DataBrowserAPI struct {
	Service DataBrowserHandlers
}

func RegisterDataBrowserRoutes(router chi.Router, handlers *DataBrowserAPI) {
	router.Get("/api/data-browser/connections", handlers.HandleConnections)
	router.Get("/api/data-browser/connections/{connectionID}/children", handlers.HandleChildren)
	router.Get("/api/data-browser/objects/{objectID}", handlers.HandleObject)
	router.Post("/api/data-browser/preview", handlers.HandlePreview)
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
