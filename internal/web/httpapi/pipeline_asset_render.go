package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	webapi "renart/internal/web/api"
	"renart/internal/web/service"
)

type PipelineAssetRenderHandlers interface {
	RenderPipelineAsset(ctx context.Context, pipelineID string, req service.PipelineAssetRenderRequest) (service.AssetRenderResult, *service.APIError)
	ComparePipelineAssetRenders(ctx context.Context, pipelineID string, req service.PipelineAssetRenderComparisonRequest) (service.PipelineAssetRenderComparison, *service.APIError)
}

type PipelineAssetRenderAPI struct {
	Service PipelineAssetRenderHandlers
}

func RegisterPipelineAssetRenderRoutes(router chi.Router, handlers *PipelineAssetRenderAPI) {
	router.Post("/api/pipelines/{id}/assets/render", handlers.HandleRenderPipelineAsset)
	router.Post("/api/pipelines/{id}/assets/render/compare", handlers.HandleComparePipelineAssetRenders)
}

func (h *PipelineAssetRenderAPI) HandleRenderPipelineAsset(w http.ResponseWriter, r *http.Request) {
	pipelineID := strings.TrimSpace(chi.URLParam(r, "id"))
	if pipelineID == "" {
		webapi.WriteBadRequest(w, "pipeline_id_required", "pipeline id is required")
		return
	}
	if h == nil || h.Service == nil {
		webapi.WriteInternalError(w, "renderer_unavailable", "pipeline asset rendering is unavailable")
		return
	}
	req, err := decodeJSONObject[service.PipelineAssetRenderRequest](w, r, maxAssetRenderRequestBytes)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := h.Service.RenderPipelineAsset(r.Context(), pipelineID, req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, result)
}

func (h *PipelineAssetRenderAPI) HandleComparePipelineAssetRenders(w http.ResponseWriter, r *http.Request) {
	pipelineID := strings.TrimSpace(chi.URLParam(r, "id"))
	if pipelineID == "" {
		webapi.WriteBadRequest(w, "pipeline_id_required", "pipeline id is required")
		return
	}
	if h == nil || h.Service == nil {
		webapi.WriteInternalError(w, "renderer_unavailable", "pipeline asset rendering is unavailable")
		return
	}
	req, err := decodeJSONObject[service.PipelineAssetRenderComparisonRequest](w, r, maxAssetRenderRequestBytes)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	result, apiErr := h.Service.ComparePipelineAssetRenders(r.Context(), pipelineID, req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, result)
}
