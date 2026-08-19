package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"renart/internal/web/service"
)

func TestNotebookAgentRoutes(t *testing.T) {
	t.Parallel()

	runner := make(chan struct{})
	agent := service.NewNotebookAgentService(context.Background(), service.NotebookAgentDependencies{
		ValidateNotebook: func(notebookID string) *service.APIError {
			if notebookID != "notebook-one" {
				return &service.APIError{Status: http.StatusNotFound, Code: "notebook_not_found", Message: "missing"}
			}
			return nil
		},
		LookPath: func(file string) (string, error) {
			if file == "codex" {
				return "/usr/bin/codex", nil
			}
			return "", errors.New("missing")
		},
		RunProvider: func(ctx context.Context, _ service.NotebookAgentProviderRunRequest, emit func(service.NotebookAgentStreamEvent)) (service.NotebookAgentProviderRunResult, error) {
			emit(service.NotebookAgentStreamEvent{Kind: "text", Text: "Working on it."})
			close(runner)
			<-ctx.Done()
			return service.NotebookAgentProviderRunResult{}, ctx.Err()
		},
	})
	t.Cleanup(agent.Close)
	router := chi.NewRouter()
	RegisterNotebookAgentRoutes(router, &NotebookAgentAPI{Service: agent})

	response := serveNotebookAgentRequest(router, http.MethodGet, "/api/notebooks/notebook-one/agent", "")
	if response.Code != http.StatusOK {
		t.Fatalf("state status = %d: %s", response.Code, response.Body.String())
	}
	var state struct {
		Status string `json:"status"`
		Agent  struct {
			Providers    []service.NotebookAgentProvider `json:"providers"`
			Conversation service.NotebookAgentSnapshot   `json:"conversation"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Agent.Providers) != 3 || !state.Agent.Providers[0].Available {
		t.Fatalf("unexpected providers: %+v", state.Agent.Providers)
	}
	if state.Agent.Conversation.Messages == nil || state.Agent.Conversation.Activities == nil {
		t.Fatalf("empty conversation arrays must encode as arrays: %+v", state.Agent.Conversation)
	}

	response = serveNotebookAgentRequest(router, http.MethodPost, "/api/notebooks/notebook-one/agent/messages", `{"provider":"codex","mode":"ask","message":"Explain this"}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("start status = %d: %s", response.Code, response.Body.String())
	}
	<-runner
	response = serveNotebookAgentRequest(router, http.MethodPost, "/api/notebooks/notebook-one/agent/cancel", `{}`)
	if response.Code != http.StatusOK {
		t.Fatalf("cancel status = %d: %s", response.Code, response.Body.String())
	}

	response = serveNotebookAgentRequest(router, http.MethodPost, "/api/notebooks/notebook-one/agent/messages", `{"provider":"codex","mode":"ask","message":"","extra":true}`)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "unknown field") {
		t.Fatalf("strict request status = %d: %s", response.Code, response.Body.String())
	}
}

func serveNotebookAgentRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
