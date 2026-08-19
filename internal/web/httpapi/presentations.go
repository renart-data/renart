package httpapi

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/model"
	"renart/internal/web/service"
)

type PresentationHandlers interface {
	Get(ctx context.Context, workspaceID string) (service.PresentationDocument, *service.APIError)
	Create(ctx context.Context, request service.CreatePresentationRequest) (service.PresentationDocument, *service.APIError)
	Update(ctx context.Context, workspaceID string, request service.UpdatePresentationRequest) (service.PresentationDocument, *service.APIError)
	Replace(ctx context.Context, workspaceID string, request service.ReplacePresentationRequest) (service.PresentationDocument, *service.APIError)
	Run(ctx context.Context, workspaceID string, request model.PresentationRunRequest) (model.PresentationRunResult, *service.APIError)
}

type PresentationAPI struct {
	Service PresentationHandlers
}

func RegisterPresentationRoutes(router chi.Router, handlers *PresentationAPI) {
	router.Post("/api/presentations", handlers.HandleCreate)
	router.Get("/api/presentations/{id}", handlers.HandleGet)
	router.Put("/api/presentations/{id}", handlers.HandleUpdate)
	router.Put("/api/presentations/{id}/definition", handlers.HandleReplace)
	router.Post("/api/presentations/{id}/run", handlers.HandleRun)
}

func (h *PresentationAPI) HandleRun(writer http.ResponseWriter, request *http.Request) {
	var body model.PresentationRunRequest
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 256<<10)).Decode(&body); err != nil {
		webapi.WriteBadRequest(writer, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := h.Service.Run(request.Context(), chi.URLParam(request, "id"), body)
	if apiErr != nil {
		writePresentationError(writer, apiErr)
		return
	}
	webapi.WriteJSON(writer, http.StatusOK, result)
}

func (h *PresentationAPI) HandleReplace(writer http.ResponseWriter, request *http.Request) {
	var body service.ReplacePresentationRequest
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 3<<20)).Decode(&body); err != nil {
		webapi.WriteBadRequest(writer, "invalid_request_body", err.Error())
		return
	}
	document, apiErr := h.Service.Replace(request.Context(), chi.URLParam(request, "id"), body)
	if apiErr != nil {
		writePresentationError(writer, apiErr)
		return
	}
	webapi.WriteJSON(writer, http.StatusOK, map[string]any{"status": "ok", "document": document})
}

func (h *PresentationAPI) HandleGet(writer http.ResponseWriter, request *http.Request) {
	document, apiErr := h.Service.Get(request.Context(), chi.URLParam(request, "id"))
	if apiErr != nil {
		writePresentationError(writer, apiErr)
		return
	}
	webapi.WriteJSON(writer, http.StatusOK, map[string]any{"status": "ok", "document": document})
}

func (h *PresentationAPI) HandleCreate(writer http.ResponseWriter, request *http.Request) {
	var body service.CreatePresentationRequest
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10)).Decode(&body); err != nil {
		webapi.WriteBadRequest(writer, "invalid_request_body", err.Error())
		return
	}
	document, apiErr := h.Service.Create(request.Context(), body)
	if apiErr != nil {
		writePresentationError(writer, apiErr)
		return
	}
	webapi.WriteJSON(writer, http.StatusCreated, map[string]any{"status": "ok", "document": document})
}

func (h *PresentationAPI) HandleUpdate(writer http.ResponseWriter, request *http.Request) {
	var body service.UpdatePresentationRequest
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 3<<20)).Decode(&body); err != nil {
		webapi.WriteBadRequest(writer, "invalid_request_body", err.Error())
		return
	}
	document, apiErr := h.Service.Update(request.Context(), chi.URLParam(request, "id"), body)
	if apiErr != nil {
		writePresentationError(writer, apiErr)
		return
	}
	webapi.WriteJSON(writer, http.StatusOK, map[string]any{"status": "ok", "document": document})
}

func writePresentationError(writer http.ResponseWriter, apiErr *service.APIError) {
	webapi.WriteJSON(writer, apiErr.Status, map[string]any{
		"status": "error",
		"error":  map[string]string{"code": apiErr.Code, "message": apiErr.Message},
	})
}
