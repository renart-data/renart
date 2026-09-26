package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/telemetry"
)

type TelemetryAPI struct {
	Client  *telemetry.Client
	Changed func()
}

func RegisterTelemetryRoutes(router chi.Router, handlers *TelemetryAPI) {
	router.Get("/api/telemetry", handlers.Status)
	router.Put("/api/telemetry", handlers.Update)
	router.Get("/api/telemetry/sample", handlers.Sample)
}
func (h *TelemetryAPI) Status(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	webapi.WriteJSON(w, http.StatusOK, h.Client.Status())
}
func (h *TelemetryAPI) Update(w http.ResponseWriter, r *http.Request) {
	request, err := decodeJSONObject[telemetry.UpdateRequest](w, r, 1024)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	if h.Client == nil {
		webapi.WriteError(w, http.StatusServiceUnavailable, "usage_unavailable", "Usage settings are unavailable")
		return
	}
	if request.Enabled == nil && !request.AcknowledgeNotice && !request.ResetIdentity {
		webapi.WriteBadRequest(w, "missing_usage_settings", "Choose a usage setting to change")
		return
	}
	result, err := h.Client.Update(request)
	if err != nil {
		webapi.WriteError(w, http.StatusInternalServerError, "usage_settings_failed", "Could not save usage settings")
		return
	}
	if h.Changed != nil {
		h.Changed()
	}
	w.Header().Set("Cache-Control", "no-store")
	webapi.WriteJSON(w, http.StatusOK, result)
}
func (h *TelemetryAPI) Sample(w http.ResponseWriter, _ *http.Request) {
	if h.Client == nil {
		webapi.WriteError(w, http.StatusServiceUnavailable, "usage_unavailable", "Usage settings are unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	webapi.WriteJSON(w, http.StatusOK, h.Client.Sample())
}
