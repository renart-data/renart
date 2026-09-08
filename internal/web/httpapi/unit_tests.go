package httpapi

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	webapi "renart/internal/web/api"
	"renart/internal/web/service"
)

type unitTestHandlers interface {
	UnitTestContext(context.Context, string) (service.SQLUnitTestContext, *APIError)
	RunUnitTests(context.Context, string, service.SQLUnitTestRunRequest) (service.SQLUnitTestRunResponse, *APIError)
}

func (h *AssetsAPI) HandleUnitTestContext(w http.ResponseWriter, r *http.Request) {
	service, ok := h.Service.(unitTestHandlers)
	if !ok {
		webapi.WriteError(w, http.StatusServiceUnavailable, "unit_tests_unavailable", "Unit tests are unavailable.")
		return
	}
	response, apiErr := service.UnitTestContext(r.Context(), chi.URLParam(r, "assetID"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, response)
}

func (h *AssetsAPI) HandleRunUnitTests(w http.ResponseWriter, r *http.Request) {
	request, err := decodeJSONObject[service.SQLUnitTestRunRequest](w, r, 4096)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	service, ok := h.Service.(unitTestHandlers)
	if !ok {
		webapi.WriteError(w, http.StatusServiceUnavailable, "unit_tests_unavailable", "Unit tests are unavailable.")
		return
	}
	response, apiErr := service.RunUnitTests(r.Context(), chi.URLParam(r, "assetID"), request)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, response)
}
