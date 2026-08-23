package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	webapi "renart/internal/web/api"
	"renart/internal/web/service"
)

const maxAssetRenderRequestBytes = 64 << 10

type AssetRenderHandlers interface {
	RenderAsset(ctx context.Context, assetID string, req service.AssetRenderRequest) (service.AssetRenderResult, *service.APIError)
}

type AssetRenderAPI struct {
	Service AssetRenderHandlers
}

func RegisterAssetRenderRoutes(router chi.Router, handlers *AssetRenderAPI) {
	router.Post("/api/assets/{assetID}/render", handlers.HandleRenderAsset)
}

func (h *AssetRenderAPI) HandleRenderAsset(w http.ResponseWriter, r *http.Request) {
	assetID := strings.TrimSpace(chi.URLParam(r, "assetID"))
	if assetID == "" {
		webapi.WriteBadRequest(w, "asset_id_required", "asset id is required")
		return
	}

	req, err := decodeJSONObject[service.AssetRenderRequest](w, r, maxAssetRenderRequestBytes)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	result, apiErr := h.Service.RenderAsset(r.Context(), assetID, req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, result)
}
