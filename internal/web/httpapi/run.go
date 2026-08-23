package httpapi

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/runcontext"
	"renart/internal/web/service"
)

type RunHandlers interface {
	Execute(ctx context.Context, req service.RunRequest) service.RunResult
}

type RunAPI struct {
	Service RunHandlers
}

func RegisterRunRoutes(router chi.Router, handlers *RunAPI) {
	router.Post("/api/run", handlers.HandleRun)
}

func (h *RunAPI) HandleRun(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[service.RunRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	if req.AssetPath != "" && req.DryRun {
		webapi.WriteBadRequest(w, "asset_dry_run_unsupported", "asset dry-run is not supported; use pipeline dry-run")
		return
	}
	contextInput := runcontext.Input{
		Start:       req.StartDate,
		End:         req.EndDate,
		FullRefresh: req.FullRefresh,
		Backfill:    req.Backfill,
		SensorMode:  req.SensorMode,
	}
	normalizedContext, err := runcontext.Normalize(contextInput)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_execution_context", err.Error())
		return
	}
	if err := runcontext.ValidateDryRun(req.DryRun, contextInput); err != nil {
		webapi.WriteBadRequest(w, "unsupported_dry_run_context", err.Error())
		return
	}
	req.StartDate = normalizedContext.StartString()
	req.EndDate = normalizedContext.EndString()
	req.SensorMode = normalizedContext.SensorMode

	result := h.Service.Execute(r.Context(), req)
	webapi.WriteJSON(w, result.HTTPCode, map[string]any{
		"status":    result.Status,
		"operation": result.Operation,
		"output":    result.Output,
		"error":     result.Error,
		"exit_code": result.ExitCode,
	})
}
