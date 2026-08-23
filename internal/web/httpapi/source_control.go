package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/service"
)

type SourceControlAPI struct {
	Service *service.SourceControlService
}

type sourceControlPathsRequest struct {
	Paths []string `json:"paths"`
}

type sourceControlCommitRequest struct {
	Message string `json:"message"`
}

type sourceControlCheckoutRequest struct {
	Branch string `json:"branch"`
}

func RegisterSourceControlRoutes(router chi.Router, handlers *SourceControlAPI) {
	router.Get("/api/source-control/status", handlers.HandleStatus)
	router.Get("/api/source-control/branches", handlers.HandleBranches)
	router.Get("/api/source-control/diff", handlers.HandleDiff)
	router.Post("/api/source-control/init", handlers.HandleInit)
	router.Post("/api/source-control/stage", handlers.HandleStage)
	router.Post("/api/source-control/unstage", handlers.HandleUnstage)
	router.Post("/api/source-control/checkout", handlers.HandleCheckout)
	router.Post("/api/source-control/commit", handlers.HandleCommit)
}

func (h *SourceControlAPI) HandleStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.Service.Status(r.Context())
	if err != nil {
		webapi.WriteBadRequest(w, "source_control_status_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "repository": status})
}

func (h *SourceControlAPI) HandleBranches(w http.ResponseWriter, r *http.Request) {
	branches, err := h.Service.Branches(r.Context())
	if err != nil {
		webapi.WriteBadRequest(w, "source_control_branches_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "branches": branches})
}

func (h *SourceControlAPI) HandleInit(w http.ResponseWriter, r *http.Request) {
	status, err := h.Service.Init(r.Context())
	if err != nil {
		webapi.WriteBadRequest(w, "source_control_init_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "repository": status})
}

func (h *SourceControlAPI) HandleDiff(w http.ResponseWriter, r *http.Request) {
	diff, err := h.Service.Diff(r.URL.Query().Get("path"), r.URL.Query().Get("staged") == "true")
	if err != nil {
		webapi.WriteBadRequest(w, "source_control_diff_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "diff": diff})
}

func (h *SourceControlAPI) HandleStage(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[sourceControlPathsRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	if err := h.Service.Stage(req.Paths); err != nil {
		webapi.WriteBadRequest(w, "source_control_stage_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (h *SourceControlAPI) HandleUnstage(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[sourceControlPathsRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	if err := h.Service.Unstage(req.Paths); err != nil {
		webapi.WriteBadRequest(w, "source_control_unstage_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (h *SourceControlAPI) HandleCheckout(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[sourceControlCheckoutRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	if err := h.Service.Checkout(req.Branch); err != nil {
		webapi.WriteBadRequest(w, "source_control_checkout_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (h *SourceControlAPI) HandleCommit(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[sourceControlCommitRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	commit, err := h.Service.Commit(req.Message)
	if err != nil {
		webapi.WriteBadRequest(w, "source_control_commit_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "commit": commit})
}
