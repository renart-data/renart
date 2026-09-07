package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
)

type WorkspaceReader interface {
	CurrentWorkspace() any
	CurrentWorkspaceLite() any
	SubscribeWorkspaceEvents() chan []byte
	UnsubscribeWorkspaceEvents(ch chan []byte)
}

type WorkspaceHandlers struct {
	Reader WorkspaceReader
}

func RegisterWorkspaceRoutes(router chi.Router, handlers *WorkspaceHandlers) {
	router.Get("/api/events", handlers.HandleEvents)
	router.Get("/api/workspace", handlers.HandleGetWorkspace)
}

func (h *WorkspaceHandlers) HandleGetWorkspace(w http.ResponseWriter, _ *http.Request) {
	webapi.WriteJSON(w, http.StatusOK, h.Reader.CurrentWorkspace())
}

func (h *WorkspaceHandlers) HandleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		webapi.WriteInternalError(w, "streaming_unsupported", "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := h.Reader.SubscribeWorkspaceEvents()
	defer h.Reader.UnsubscribeWorkspaceEvents(ch)

	if payload, err := json.Marshal(h.Reader.CurrentWorkspaceLite()); err == nil {
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return
		}
		flusher.Flush()
	}

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", msg); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

type WorkspaceUpdatedEvent struct {
	Type      string `json:"type"`
	Workspace any    `json:"workspace"`
	Lite      bool   `json:"lite,omitempty"`
}

type WorkspacePublisher interface {
	ConfigChanged(context.Context, string, string)
}
