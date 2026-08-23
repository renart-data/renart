package httpapi

import (
	"context"
	"net/http"
	"strings"

	webapi "renart/internal/web/api"
	"renart/internal/web/service"

	"github.com/go-chi/chi/v5"
)

type JinjaRenderHandlers interface {
	Render(ctx context.Context, assetID string, req service.JinjaRenderRequest) (service.JinjaRenderResult, *service.APIError)
}

type JinjaRenderAPI struct {
	Service JinjaRenderHandlers
}

func RegisterJinjaRenderRoutes(router chi.Router, handlers *JinjaRenderAPI) {
	router.Post("/api/assets/{assetID}/render-jinja", handlers.HandleRenderJinja)
}

func (h *JinjaRenderAPI) HandleRenderJinja(w http.ResponseWriter, r *http.Request) {
	assetID := strings.TrimSpace(chi.URLParam(r, "assetID"))
	if assetID == "" {
		webapi.WriteBadRequest(w, "asset_id_required", "asset id is required")
		return
	}

	req, err := decodeJSONObject[service.JinjaRenderRequest](w, r, 0)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	result, apiErr := h.Service.Render(r.Context(), assetID, req)
	if apiErr != nil {
		webapi.WriteJSON(w, apiErr.Status, map[string]any{
			"status": "error",
			"error":  map[string]string{"code": apiErr.Code, "message": apiErr.Message},
		})
		return
	}

	webapi.WriteJSON(w, http.StatusOK, result)
}
