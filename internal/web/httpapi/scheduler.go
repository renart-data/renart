package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/scheduler"
)

type SchedulerHandlers interface {
	ListSchedules(ctx context.Context) ([]scheduler.PipelineSchedule, error)
	GetPipelineSchedule(ctx context.Context, pipelineID string) (scheduler.PipelineSchedule, error)
	UpdatePipelineSchedule(ctx context.Context, pipelineID string, req scheduler.UpdateScheduleRequest) (scheduler.PipelineSchedule, error)
	TriggerPipeline(ctx context.Context, pipelineID string, req scheduler.TriggerRequest) (scheduler.PipelineRun, error)
	ListRuns(ctx context.Context, filter scheduler.RunFilter) (scheduler.RunList, error)
	GetRun(ctx context.Context, runID string) (scheduler.PipelineRun, []scheduler.LogLine, []scheduler.PipelineRunStep, error)
	GetRunPlan(ctx context.Context, runID string) (scheduler.PipelineRunPlan, bool, error)
	ListRunUnits(ctx context.Context, runID string) ([]scheduler.PipelineRunUnit, error)
	GetRunReexecution(ctx context.Context, runID string) (scheduler.PipelineRunReexecution, error)
	ReexecuteRun(ctx context.Context, runID string) (scheduler.PipelineRun, error)
	CancelRun(ctx context.Context, runID string) (scheduler.PipelineRun, error)
}

type SchedulerAPI struct {
	Service SchedulerHandlers
}

func RegisterSchedulerRoutes(router chi.Router, handlers *SchedulerAPI) {
	router.Get("/api/schedules", handlers.HandleListSchedules)
	router.Get("/api/pipelines/{id}/schedule", handlers.HandleGetPipelineSchedule)
	router.Put("/api/pipelines/{id}/schedule", handlers.HandleUpdatePipelineSchedule)
	router.Post("/api/pipelines/{id}/trigger", handlers.HandleTriggerPipeline)
	router.Get("/api/runs", handlers.HandleListRuns)
	router.Get("/api/runs/{id}", handlers.HandleGetRun)
	router.Post("/api/runs/{id}/reexecute", handlers.HandleReexecuteRun)
	router.Post("/api/runs/{id}/cancel", handlers.HandleCancelRun)
}

func (h *SchedulerAPI) HandleListSchedules(w http.ResponseWriter, r *http.Request) {
	items, err := h.Service.ListSchedules(r.Context())
	if err != nil {
		webapi.WriteInternalError(w, "schedules_list_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "schedules": items})
}

func (h *SchedulerAPI) HandleGetPipelineSchedule(w http.ResponseWriter, r *http.Request) {
	item, err := h.Service.GetPipelineSchedule(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		webapi.WriteBadRequest(w, "schedule_get_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "schedule": item})
}

func (h *SchedulerAPI) HandleUpdatePipelineSchedule(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSONObject[scheduler.UpdateScheduleRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	item, err := h.Service.UpdatePipelineSchedule(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		if errors.Is(err, scheduler.ErrSchedulerNotOwner) {
			webapi.WriteConflict(w, "scheduler_not_owner", err.Error())
			return
		}
		webapi.WriteBadRequest(w, "schedule_update_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "schedule": item})
}

func (h *SchedulerAPI) HandleTriggerPipeline(w http.ResponseWriter, r *http.Request) {
	var req scheduler.TriggerRequest
	if r.Body != nil && r.Body != http.NoBody {
		r.Body = http.MaxBytesReader(w, r.Body, defaultMaxJSONRequestBytes)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var decoded *scheduler.TriggerRequest
		if err := decoder.Decode(&decoded); err != nil && !errors.Is(err, io.EOF) {
			webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
			return
		} else if err == nil {
			if decoded == nil {
				webapi.WriteBadRequest(w, "invalid_request_body", "request body must be a JSON object")
				return
			}
			req = *decoded
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			if err == nil {
				err = errors.New("request body must contain a single JSON object")
			}
			webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
			return
		}
	}
	if trigger := strings.TrimSpace(req.LegacyTrigger); trigger != "" && trigger != string(scheduler.RunTriggerManual) {
		webapi.WriteBadRequest(w, "invalid_request_body", "trigger origin is server-owned; omit trigger or use the legacy manual value")
		return
	}
	req.LegacyTrigger = ""
	run, err := h.Service.TriggerPipeline(r.Context(), chi.URLParam(r, "id"), req)
	if err != nil {
		if errors.Is(err, scheduler.ErrSchedulerNotOwner) {
			webapi.WriteConflict(w, "scheduler_not_owner", err.Error())
			return
		}
		var activeRun *scheduler.PipelineRunActiveError
		if errors.As(err, &activeRun) {
			webapi.WriteErrorWithDetails(w, http.StatusConflict, "pipeline_run_active", err.Error(), map[string]string{
				"pipeline_id":   activeRun.PipelineID,
				"active_run_id": activeRun.ActiveRunID,
			})
			return
		}
		webapi.WriteBadRequest(w, "pipeline_trigger_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusAccepted, map[string]any{"status": "ok", "run": run})
}

func (h *SchedulerAPI) HandleListRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset <= 0 {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page > 1 && limit > 0 {
			offset = (page - 1) * limit
		}
	}
	result, err := h.Service.ListRuns(r.Context(), scheduler.RunFilter{
		PipelineID:  r.URL.Query().Get("pipeline_id"),
		Environment: r.URL.Query().Get("environment"),
		Status:      parseRunStatus(r.URL.Query().Get("status")),
		Query:       r.URL.Query().Get("q"),
		Limit:       limit,
		Offset:      offset,
	})
	if err != nil {
		webapi.WriteInternalError(w, "runs_list_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "runs": result.Runs, "total": result.Total, "limit": result.Limit, "offset": result.Offset})
}

func parseRunStatus(value string) scheduler.RunStatus {
	switch scheduler.RunStatus(strings.TrimSpace(value)) {
	case scheduler.RunStatusQueued, scheduler.RunStatusRunning, scheduler.RunStatusSuccess, scheduler.RunStatusFailed, scheduler.RunStatusCancelled:
		return scheduler.RunStatus(strings.TrimSpace(value))
	default:
		return ""
	}
}

func (h *SchedulerAPI) HandleGetRun(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "id")
	run, logs, steps, err := h.Service.GetRun(r.Context(), runID)
	if err != nil {
		webapi.WriteBadRequest(w, "run_get_failed", err.Error())
		return
	}
	units, err := h.Service.ListRunUnits(r.Context(), runID)
	if err != nil {
		webapi.WriteBadRequest(w, "run_get_failed", err.Error())
		return
	}
	plan, hasPlan, err := h.Service.GetRunPlan(r.Context(), runID)
	if err != nil {
		webapi.WriteBadRequest(w, "run_get_failed", err.Error())
		return
	}
	var planResponse any
	if hasPlan {
		planResponse = plan
	}
	reexecution, err := h.Service.GetRunReexecution(r.Context(), runID)
	if err != nil {
		webapi.WriteBadRequest(w, "run_get_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "run": run, "logs": logs, "steps": steps,
		"plan": planResponse, "units": units, "reexecution": reexecution,
	})
}

func (h *SchedulerAPI) HandleReexecuteRun(w http.ResponseWriter, r *http.Request) {
	if _, err := decodeJSONObject[struct{}](w, r, 0); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	run, err := h.Service.ReexecuteRun(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, scheduler.ErrSchedulerNotOwner) {
			webapi.WriteConflict(w, "scheduler_not_owner", err.Error())
			return
		}
		var activeRun *scheduler.PipelineRunActiveError
		if errors.As(err, &activeRun) {
			webapi.WriteErrorWithDetails(w, http.StatusConflict, "pipeline_run_active", err.Error(), map[string]string{
				"pipeline_id": activeRun.PipelineID, "active_run_id": activeRun.ActiveRunID,
			})
			return
		}
		var unavailable *scheduler.ExactReexecutionUnavailableError
		if errors.As(err, &unavailable) {
			webapi.WriteErrorWithDetails(w, http.StatusConflict, "exact_reexecution_unavailable", err.Error(), map[string]string{
				"reason": strings.TrimSpace(unavailable.Reason),
			})
			return
		}
		webapi.WriteBadRequest(w, "run_reexecution_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusAccepted, map[string]any{"status": "ok", "run": run})
}

func (h *SchedulerAPI) HandleCancelRun(w http.ResponseWriter, r *http.Request) {
	if _, err := decodeJSONObject[struct{}](w, r, 0); err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	run, err := h.Service.CancelRun(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, scheduler.ErrSchedulerNotOwner) {
			webapi.WriteConflict(w, "scheduler_not_owner", err.Error())
			return
		}
		var unavailable *scheduler.RunCancellationUnavailableError
		if errors.As(err, &unavailable) {
			webapi.WriteErrorWithDetails(w, http.StatusConflict, "run_cancellation_unavailable", err.Error(), map[string]string{
				"reason": strings.TrimSpace(unavailable.Reason),
			})
			return
		}
		webapi.WriteBadRequest(w, "run_cancellation_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusAccepted, map[string]any{"status": "ok", "run": run})
}
