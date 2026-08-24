package httpapi

import (
	"context"
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
	RequestQuestionnaire(context.Context, string, string, service.NotebookAgentQuestionnaireRequest) (service.NotebookAgentInteractionResult, *service.APIError)
	RequestConnectionAccess(context.Context, string, string, service.NotebookAgentConnectionAccessRequest) (service.NotebookAgentInteractionResult, *service.APIError)
	ListQueryConnections(string, string) (service.NotebookAgentConnectionListResult, *service.APIError)
	DiscoverConnectionCatalog(context.Context, string, string, service.NotebookAgentConnectionCatalogRequest) (service.NotebookAgentConnectionCatalogResult, *service.APIError)
	QueryConnectionSample(context.Context, string, string, service.NotebookAgentConnectionSampleRequest) (service.NotebookAgentConnectionSampleResult, *service.APIError)
	AnswerInteraction(string, string, service.AnswerNotebookAgentInteractionRequest) (service.NotebookAgentSnapshot, *service.APIError)
}

type NotebookAgentAPI struct {
	Service NotebookAgentHandlers
}

func RegisterNotebookAgentRoutes(router chi.Router, handlers *NotebookAgentAPI) {
	router.Get("/api/notebooks/{id}/agent", handlers.HandleState)
	router.Post("/api/notebooks/{id}/agent/messages", handlers.HandleStartTurn)
	router.Post("/api/notebooks/{id}/agent/cancel", handlers.HandleCancel)
	router.Post("/api/notebooks/{id}/agent/native/questionnaire", handlers.HandleNativeQuestionnaire)
	router.Post("/api/notebooks/{id}/agent/native/connections/request", handlers.HandleNativeConnectionRequest)
	router.Post("/api/notebooks/{id}/agent/native/connections/list", handlers.HandleNativeConnectionList)
	router.Post("/api/notebooks/{id}/agent/native/connections/discover", handlers.HandleNativeConnectionCatalog)
	router.Post("/api/notebooks/{id}/agent/native/connections/query", handlers.HandleNativeConnectionSample)
	router.Post("/api/notebooks/{id}/agent/interactions/{interactionID}/answer", handlers.HandleAnswerInteraction)
	router.Delete("/api/notebooks/{id}/agent", handlers.HandleReset)
}

func (h *NotebookAgentAPI) HandleNativeConnectionRequest(w http.ResponseWriter, r *http.Request) {
	request, err := decodeJSONObject[service.NotebookAgentConnectionAccessRequest](w, r, maxNotebookAgentRequestBytes)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := h.Service.RequestConnectionAccess(
		r.Context(), chi.URLParam(r, "id"), notebookAgentTurnToken(r), request,
	)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "result": result})
}

func (h *NotebookAgentAPI) HandleNativeConnectionList(w http.ResponseWriter, r *http.Request) {
	if _, err := decodeJSONObject[struct{}](w, r, maxNotebookAgentRequestBytes); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := h.Service.ListQueryConnections(chi.URLParam(r, "id"), notebookAgentTurnToken(r))
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "result": result})
}

func (h *NotebookAgentAPI) HandleNativeConnectionCatalog(w http.ResponseWriter, r *http.Request) {
	request, err := decodeJSONObject[service.NotebookAgentConnectionCatalogRequest](w, r, maxNotebookAgentRequestBytes)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := h.Service.DiscoverConnectionCatalog(
		r.Context(), chi.URLParam(r, "id"), notebookAgentTurnToken(r), request,
	)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "result": result})
}

func (h *NotebookAgentAPI) HandleNativeConnectionSample(w http.ResponseWriter, r *http.Request) {
	request, err := decodeJSONObject[service.NotebookAgentConnectionSampleRequest](w, r, maxNotebookAgentRequestBytes)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := h.Service.QueryConnectionSample(
		r.Context(), chi.URLParam(r, "id"), notebookAgentTurnToken(r), request,
	)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "result": result})
}

func notebookAgentTurnToken(r *http.Request) string {
	return r.Header.Get("X-Renart-Agent-Turn-Token")
}

func (h *NotebookAgentAPI) HandleNativeQuestionnaire(w http.ResponseWriter, r *http.Request) {
	request, err := decodeJSONObject[service.NotebookAgentQuestionnaireRequest](w, r, maxNotebookAgentRequestBytes)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := h.Service.RequestQuestionnaire(
		r.Context(),
		chi.URLParam(r, "id"),
		notebookAgentTurnToken(r),
		request,
	)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "result": result})
}

func (h *NotebookAgentAPI) HandleAnswerInteraction(w http.ResponseWriter, r *http.Request) {
	request, err := decodeJSONObject[service.AnswerNotebookAgentInteractionRequest](w, r, maxNotebookAgentRequestBytes)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	conversation, apiErr := h.Service.AnswerInteraction(
		chi.URLParam(r, "id"),
		chi.URLParam(r, "interactionID"),
		request,
	)
	if apiErr != nil {
		writeNotebookError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "conversation": conversation})
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
