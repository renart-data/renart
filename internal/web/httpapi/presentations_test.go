package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"renart/internal/web/model"
	"renart/internal/web/service"
)

type presentationHandlerStub struct {
	workspaceID string
	create      service.CreatePresentationRequest
	update      service.UpdatePresentationRequest
	replace     service.ReplacePresentationRequest
	run         model.PresentationRunRequest
}

func (stub *presentationHandlerStub) Run(_ context.Context, workspaceID string, request model.PresentationRunRequest) (model.PresentationRunResult, *service.APIError) {
	stub.workspaceID = workspaceID
	stub.run = request
	return model.PresentationRunResult{Status: "ok"}, nil
}

func (stub *presentationHandlerStub) Replace(_ context.Context, workspaceID string, request service.ReplacePresentationRequest) (service.PresentationDocument, *service.APIError) {
	stub.workspaceID = workspaceID
	stub.replace = request
	return service.PresentationDocument{}, nil
}

func (stub *presentationHandlerStub) Get(_ context.Context, workspaceID string) (service.PresentationDocument, *service.APIError) {
	stub.workspaceID = workspaceID
	return service.PresentationDocument{}, nil
}

func (stub *presentationHandlerStub) Create(_ context.Context, request service.CreatePresentationRequest) (service.PresentationDocument, *service.APIError) {
	stub.create = request
	return service.PresentationDocument{}, nil
}

func (stub *presentationHandlerStub) Update(_ context.Context, workspaceID string, request service.UpdatePresentationRequest) (service.PresentationDocument, *service.APIError) {
	stub.workspaceID = workspaceID
	stub.update = request
	return service.PresentationDocument{}, nil
}

func TestPresentationRoutesUseBoundedTypedRequests(t *testing.T) {
	stub := &presentationHandlerStub{}
	router := chi.NewRouter()
	RegisterPresentationRoutes(router, &PresentationAPI{Service: stub})

	create := httptest.NewRecorder()
	router.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/presentations", strings.NewReader(`{"kind":"dashboard","title":"Sales"}`)))
	if create.Code != http.StatusCreated || stub.create.Kind != "dashboard" || stub.create.Title != "Sales" {
		t.Fatalf("unexpected create response=%d request=%+v", create.Code, stub.create)
	}

	update := httptest.NewRecorder()
	router.ServeHTTP(update, httptest.NewRequest(http.MethodPut, "/api/presentations/presentation-id", strings.NewReader(`{"expected_revision":"v1:abc","content":"version: 1"}`)))
	if update.Code != http.StatusOK || stub.workspaceID != "presentation-id" || stub.update.ExpectedRevision != "v1:abc" {
		t.Fatalf("unexpected update response=%d id=%q request=%+v", update.Code, stub.workspaceID, stub.update)
	}

	replace := httptest.NewRecorder()
	router.ServeHTTP(replace, httptest.NewRequest(http.MethodPut, "/api/presentations/presentation-id/definition", strings.NewReader(`{"expected_revision":"v1:def","artifact":{"id":"sales","kind":"dashboard","version":1,"revision":"v1:def","title":"Sales","path":"dashboards/sales.dashboard.yml","workspace_id":"presentation-id"}}`)))
	if replace.Code != http.StatusOK || stub.workspaceID != "presentation-id" || stub.replace.ExpectedRevision != "v1:def" || stub.replace.Artifact.ID != "sales" {
		t.Fatalf("unexpected replace response=%d id=%q request=%+v", replace.Code, stub.workspaceID, stub.replace)
	}

	run := httptest.NewRecorder()
	router.ServeHTTP(run, httptest.NewRequest(http.MethodPost, "/api/presentations/presentation-id/run", strings.NewReader(`{"environment":"dev","filter_values":{"region":"eu"},"visualization_ids":["revenue"]}`)))
	if run.Code != http.StatusOK || stub.workspaceID != "presentation-id" || stub.run.Environment != "dev" || stub.run.FilterValues["region"] != "eu" {
		t.Fatalf("unexpected run response=%d id=%q request=%+v", run.Code, stub.workspaceID, stub.run)
	}
}
