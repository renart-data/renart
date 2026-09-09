package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	webmodel "renart/internal/web/model"
	"renart/internal/web/runcontext"
	"renart/internal/web/service"
)

type (
	InspectExecutionResult    = service.InspectResult
	MaterializeExecutionEvent = service.MaterializeResult
)

type ExecutionHandlers interface {
	InspectAsset(ctx context.Context, assetID, limit, environment, startDate, endDate string) InspectExecutionResult
	MaterializeAssetStreamWithSensorMode(ctx context.Context, assetID, environment, scope, startDate, endDate string, fullRefresh, backfill bool, confirmedEnvironment, sensorMode string, onChunk func([]byte)) MaterializeExecutionEvent
}

type ExecutionAPI struct {
	Service ExecutionHandlers
}

func RegisterExecutionRoutes(router chi.Router, handlers *ExecutionAPI) {
	router.Get("/api/assets/{assetID}/inspect", handlers.HandleInspectAsset)
	router.Post("/api/assets/{assetID}/materialize/stream", handlers.HandleMaterializeAssetStream)
}

func (h *ExecutionAPI) HandleInspectAsset(w http.ResponseWriter, r *http.Request) {
	assetID := chi.URLParam(r, "assetID")
	limit := r.URL.Query().Get("limit")
	environment := r.URL.Query().Get("environment")
	startDate := r.URL.Query().Get("start_date")
	endDate := r.URL.Query().Get("end_date")
	if limit == "" {
		limit = "200"
	}

	result := h.Service.InspectAsset(r.Context(), assetID, limit, environment, startDate, endDate)
	webapi.WriteJSON(w, result.HTTPStatus, map[string]any{
		"status":                                 result.Status,
		"columns":                                result.Columns,
		"rows":                                   result.Rows,
		"preview":                                result.Preview,
		"raw_output":                             result.RawOutput,
		"operation":                              result.Operation,
		"error":                                  result.Error,
		"info":                                   result.Info,
		"missing_upstream_asset_ids":             result.MissingUpstreamAssetIDs,
		"missing_upstream_asset_names":           result.MissingUpstreamAssetNames,
		"missing_upstream_assets_materializable": result.MissingUpstreamAssetsMaterializable,
		"attempts":                               result.Attempts,
		"retryable":                              result.Retryable,
	})
}

func (h *ExecutionAPI) HandleMaterializeAssetStream(w http.ResponseWriter, r *http.Request) {
	assetID := chi.URLParam(r, "assetID")
	query, err := decodeStrictQuery(r,
		"environment", "scope", "start_date", "end_date", "dry_run",
		"full_refresh", "backfill", "confirmed_environment", "sensor_mode",
	)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_execution_context", err.Error())
		return
	}
	dryRun, err := strictQueryBool(query, "dry_run")
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_execution_context", err.Error())
		return
	}
	if dryRun {
		webapi.WriteBadRequest(w, "asset_dry_run_unsupported", "asset dry-run is not supported; use pipeline dry-run")
		return
	}
	fullRefresh, err := strictQueryBool(query, "full_refresh")
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_execution_context", err.Error())
		return
	}
	backfill, err := strictQueryBool(query, "backfill")
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_execution_context", err.Error())
		return
	}
	normalized, err := runcontext.Normalize(runcontext.Input{
		Start:       query.Get("start_date"),
		End:         query.Get("end_date"),
		FullRefresh: fullRefresh,
		Backfill:    backfill,
		SensorMode:  query.Get("sensor_mode"),
	})
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_execution_context", err.Error())
		return
	}
	scope, err := service.NormalizeMaterializeScope(query.Get("scope"))
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_execution_context", err.Error())
		return
	}
	environment := query.Get("environment")
	startDate := normalized.StartString()
	endDate := normalized.EndString()
	confirmedEnvironment := query.Get("confirmed_environment")
	sensorMode := normalized.SensorMode
	flusher, ok := w.(http.Flusher)
	if !ok {
		webapi.WriteInternalError(w, "streaming_unsupported", "streaming unsupported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	_ = WriteSSEJSON(w, flusher, "start", map[string]any{"operation": webmodel.OperationMetadata{Type: "run", AssetPath: assetID, Target: assetID}})
	result := h.Service.MaterializeAssetStreamWithSensorMode(r.Context(), assetID, environment, string(scope), startDate, endDate, fullRefresh, backfill, confirmedEnvironment, sensorMode, func(chunk []byte) {
		_ = WriteSSEJSON(w, flusher, "output", map[string]any{"chunk": string(chunk)})
	})
	_ = WriteSSEJSON(w, flusher, "done", map[string]any{
		"status":            result.Status,
		"operation":         result.Operation,
		"output":            result.Output,
		"error":             result.Error,
		"exit_code":         result.ExitCode,
		"changed_asset_ids": result.ChangedAssetIDs,
		"materialized_at":   result.MaterializedAt,
		"warnings":          result.Warnings,
	})
}

func WriteSSEJSON(w http.ResponseWriter, flusher http.Flusher, event string, body any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}

	if event != "" {
		if _, err := fmt.Fprintf(w, "event: %s\n", event); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}

	flusher.Flush()
	return nil
}
