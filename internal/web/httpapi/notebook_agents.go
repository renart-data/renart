package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/service"
)

const maxNotebookAgentRequestBytes = 64 << 10

type NotebookAgentHandlers interface {
	State(notebookID string) (service.NotebookAgentState, *service.APIError)
	StartTurn(notebookID string, request service.StartNotebookAgentTurnRequest) (service.NotebookAgentSnapshot, *service.APIError)
	Cancel(notebookID string) (service.NotebookAgentSnapshot, *service.APIError)
	Reset(notebookID string) (service.NotebookAgentSnapshot, *service.APIError)
}

type NotebookAgentAPI struct {
	Service NotebookAgentHandlers
}

func RegisterNotebookAgentRoutes(router chi.Router, handlers *NotebookAgentAPI) {
	router.Get("/api/notebooks/{id}/agent", handlers.HandleState)
	router.Post("/api/notebooks/{id}/agent/messages", handlers.HandleStartTurn)
	router.Post("/api/notebooks/{id}/agent/cancel", handlers.HandleCancel)
	router.Delete("/api/notebooks/{id}/agent", handlers.HandleReset)
}

func (h *NotebookAgentAPI) HandleState(w http.ResponseWriter, r *http.Request) {
	state, apiErr := h.Service.State(chi.URLParam(r, "id"))
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "agent": state})
}

func (h *NotebookAgentAPI) HandleStartTurn(w http.ResponseWriter, r *http.Request) {
	request, err := decodeJSONObject[service.StartNotebookAgentTurnRequest](w, r, maxNotebookAgentRequestBytes)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	conversation, apiErr := h.Service.StartTurn(chi.URLParam(r, "id"), request)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusAccepted, map[string]any{"status": "ok", "conversation": conversation})
}

func (h *NotebookAgentAPI) HandleCancel(w http.ResponseWriter, r *http.Request) {
	conversation, apiErr := h.Service.Cancel(chi.URLParam(r, "id"))
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "conversation": conversation})
}

func (h *NotebookAgentAPI) HandleReset(w http.ResponseWriter, r *http.Request) {
	conversation, apiErr := h.Service.Reset(chi.URLParam(r, "id"))
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "conversation": conversation})
}
