package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/execution"
	"renart/internal/web/policy"
	"renart/internal/web/scheduler"
	"renart/internal/web/service"
)

type pipelinePlanHandlerStub struct {
	calls      int
	pipelineID string
	req        service.PipelinePlanRequest
	plan       service.PipelinePlan
	err        *service.APIError
}

func (s *pipelinePlanHandlerStub) Plan(
	_ context.Context,
	pipelineID string,
	req service.PipelinePlanRequest,
) (service.PipelinePlan, *service.APIError) {
	s.calls++
	s.pipelineID = pipelineID
	s.req = req
	return s.plan, s.err
}

type pipelinePlanRunStub struct {
	calls      int
	pipelineID string
	req        scheduler.TriggerRequest
	run        scheduler.PipelineRun
	err        error
}

func (s *pipelinePlanRunStub) TriggerPipeline(
	_ context.Context,
	pipelineID string,
	req scheduler.TriggerRequest,
) (scheduler.PipelineRun, error) {
	s.calls++
	s.pipelineID = pipelineID
	s.req = req
	return s.run, s.err
}

func TestHandlePipelinePlanAcceptsDeclaredReadOnlyContext(t *testing.T) {
	t.Parallel()
	stub := &pipelinePlanHandlerStub{plan: service.PipelinePlan{ID: "plan-id", Status: service.PipelinePlanStatusReady}}
	response := pipelinePlanRequest(stub, `{
  "environment":"dev",
  "start_date":"2026-07-15T00:00:00Z",
  "end_date":"2026-07-16T00:00:00Z",
  "execution_time":"2026-07-16T12:00:00Z",
  "full_refresh":true,
  "backfill":false,
  "sensor_mode":"once",
  "source":{"kind":"snapshot","version_id":"version-1"},
  "selection":{"mode":"asset","asset_name":"analytics.report","scope":"asset_with_upstreams"},
  "include_stage_content":true
}`)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, 1, stub.calls)
	assert.Equal(t, "pipeline-id", stub.pipelineID)
	assert.Equal(t, "dev", stub.req.Environment)
	assert.Equal(t, "version-1", stub.req.Source.VersionID)
	assert.Equal(t, "analytics.report", stub.req.Selection.AssetName)
	assert.True(t, stub.req.IncludeStageContent)
	assert.Contains(t, response.Body.String(), `"id":"plan-id"`)
}

func TestHandlePipelinePlanAcceptsCustomSelector(t *testing.T) {
	t.Parallel()
	stub := &pipelinePlanHandlerStub{plan: service.PipelinePlan{ID: "plan-id", Status: service.PipelinePlanStatusReady}}
	response := pipelinePlanRequest(stub, `{"selection":{"mode":"selector_needed","selector":"tag:daily,+analytics.orders"}}`)

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, 1, stub.calls)
	assert.Equal(t, service.PipelinePlanSelectionSelectorNeeded, stub.req.Selection.Mode)
	assert.Equal(t, "tag:daily,+analytics.orders", stub.req.Selection.Selector)
}

func TestHandlePipelinePlanRejectsUnknownOrMalformedInput(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"run":true}`,
		`{"source":{"latest":true}}`,
		`{"selection":{"expression":"tag:daily"}}`,
		`null`,
		`[]`,
		`{}` + "\n" + `{}`,
		`{"environment":`,
	} {
		stub := &pipelinePlanHandlerStub{}
		response := pipelinePlanRequest(stub, body)
		assert.Equal(t, http.StatusBadRequest, response.Code, body)
		assert.Zero(t, stub.calls, body)
	}
}

func TestHandlePipelinePlanRejectsOversizedBody(t *testing.T) {
	t.Parallel()
	stub := &pipelinePlanHandlerStub{}
	response := pipelinePlanRequest(stub, `{"environment":"`+strings.Repeat("x", maxPipelinePlanRequestBytes)+`"}`)
	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Zero(t, stub.calls)
}

func TestHandlePipelinePlanUsesSharedErrorEnvelope(t *testing.T) {
	t.Parallel()
	stub := &pipelinePlanHandlerStub{err: &service.APIError{
		Status:  http.StatusConflict,
		Code:    "source_changed",
		Message: "pipeline source changed while planning; regenerate the plan",
	}}
	response := pipelinePlanRequest(stub, `{}`)
	require.Equal(t, http.StatusConflict, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"source_changed"`)
}

func TestHandleConfirmPipelinePlanRegeneratesAndTriggersExactPlan(t *testing.T) {
	t.Parallel()
	plan := confirmablePipelinePlan()
	planner := &pipelinePlanHandlerStub{plan: plan}
	runs := &pipelinePlanRunStub{run: scheduler.PipelineRun{ID: "run-1", Status: scheduler.RunStatusQueued}}
	response := pipelinePlanConfirmRequest(planner, runs, `{
  "plan_id":"plan-id",
  "plan":{
    "environment":"dev",
    "execution_time":"2026-07-17T12:00:00Z",
    "full_refresh":true,
    "source":{"kind":"working_tree"},
    "selection":{"mode":"all"},
    "include_stage_content":true
  }
}`)

	require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
	assert.Equal(t, 1, planner.calls)
	assert.False(t, planner.req.IncludeStageContent, "confirmation regenerates without trusting rendered client content")
	require.Equal(t, 1, runs.calls)
	assert.Equal(t, "pipeline-id", runs.pipelineID)
	assert.Equal(t, "dev", runs.req.Environment)
	assert.Equal(t, scheduler.RunSourceWorkingTree, runs.req.Source)
	assert.Equal(t, "2026-07-17T11:00:00Z", runs.req.Start)
	assert.Equal(t, "2026-07-17T12:00:00Z", runs.req.End)
	assert.Equal(t, "2026-07-17T12:00:00Z", runs.req.ExecutionTime)
	assert.True(t, runs.req.FullRefresh)
	assert.Equal(t, strings.Repeat("a", 64), runs.req.ExpectedSourceMerkle)
	assert.Equal(t, strings.Repeat("b", 64), runs.req.ExpectedConfigurationDigest)
	require.NotNil(t, runs.req.ConfirmedPlan)
	assert.Equal(t, plan.ID, runs.req.ConfirmedPlan.PlanID)
	assert.Equal(t, scheduler.PipelineRunPlanVersionV3, runs.req.ConfirmedPlan.Version)
	assert.Equal(t, 2, runs.req.ConfirmedPlan.MaxActiveSteps)
	assert.Equal(t, plan.Selection.DataStateToken, runs.req.ConfirmedPlan.Selection.DataStateToken)
	require.Len(t, runs.req.ConfirmedPlan.ExecutionContracts, 1)
	assert.Equal(t, "asset-1", runs.req.ConfirmedPlan.ExecutionContracts[0].AssetID)
	assert.True(t, execution.EqualExecutionContracts(plan.ExecutionContracts, runs.req.ConfirmedPlan.ExecutionContracts), "durable binding must retain every reviewed access requirement")
	var artifact service.PipelinePlan
	require.NoError(t, json.Unmarshal(runs.req.ConfirmedPlan.Artifact, &artifact))
	assert.True(t, execution.EqualExecutionContracts(artifact.ExecutionContracts, runs.req.ConfirmedPlan.ExecutionContracts))
	require.Len(t, runs.req.ConfirmedPlan.ExecutionUnits, 1)
	assert.Equal(t, "analytics.orders", runs.req.ConfirmedPlan.ExecutionUnits[0].AssetName)
	assert.NotContains(t, string(runs.req.ConfirmedPlan.Artifact), `"content":"select`)
	assert.Contains(t, response.Body.String(), `"plan_id":"plan-id"`)
	assert.Contains(t, response.Body.String(), `"id":"run-1"`)
}

func TestHandleConfirmPipelinePlanRejectsDeploymentReviewPurpose(t *testing.T) {
	t.Parallel()
	planner := &pipelinePlanHandlerStub{plan: confirmablePipelinePlan()}
	runs := &pipelinePlanRunStub{}
	response := pipelinePlanConfirmRequest(planner, runs, `{
  "plan_id":"plan-id",
  "plan":{
    "purpose":"deployment",
    "execution_time":"2026-07-17T12:00:00Z",
    "selection":{"mode":"all"}
  }
}`)

	require.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"plan_purpose_not_confirmable"`)
	assert.Zero(t, planner.calls)
	assert.Zero(t, runs.calls)
}

func TestHandleConfirmPipelinePlanRequiresReviewAgainWhenPlanChanged(t *testing.T) {
	t.Parallel()
	planner := &pipelinePlanHandlerStub{plan: confirmablePipelinePlan()}
	runs := &pipelinePlanRunStub{}
	response := pipelinePlanConfirmRequest(planner, runs, `{
  "plan_id":"old-plan",
  "plan":{"execution_time":"2026-07-17T12:00:00Z","selection":{"mode":"all"}}
}`)

	require.Equal(t, http.StatusConflict, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"plan_stale"`)
	assert.Contains(t, response.Body.String(), `"plan":{"id":"plan-id"`)
	assert.Zero(t, runs.calls)
}

func TestHandleConfirmPipelinePlanRejectsBlockedAndAcceptsSelectedExecution(t *testing.T) {
	t.Parallel()
	t.Run("blocked", func(t *testing.T) {
		plan := confirmablePipelinePlan()
		plan.Status = service.PipelinePlanStatusBlocked
		plan.Readiness.Blockers = []service.PipelinePlanIssue{{Code: "pipeline_invalid", Severity: "error", Message: "invalid"}}
		planner := &pipelinePlanHandlerStub{plan: plan}
		runs := &pipelinePlanRunStub{}
		response := pipelinePlanConfirmRequest(planner, runs, `{
  "plan_id":"plan-id",
  "plan":{"execution_time":"2026-07-17T12:00:00Z","selection":{"mode":"all"}}
}`)
		require.Equal(t, http.StatusConflict, response.Code)
		assert.Contains(t, response.Body.String(), `"code":"plan_blocked"`)
		assert.Zero(t, runs.calls)
	})

	t.Run("needed selection", func(t *testing.T) {
		plan := confirmablePipelinePlan()
		plan.Selection.Mode = service.PipelinePlanSelectionNeeded
		planner := &pipelinePlanHandlerStub{plan: plan}
		runs := &pipelinePlanRunStub{}
		response := pipelinePlanConfirmRequest(planner, runs, `{
  "plan_id":"plan-id",
  "plan":{"execution_time":"2026-07-17T12:00:00Z","selection":{"mode":"needed"}}
}`)
		require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
		assert.Equal(t, 1, planner.calls)
		require.Equal(t, 1, runs.calls)
		require.NotNil(t, runs.req.ConfirmedPlan)
		assert.Equal(t, service.PipelinePlanSelectionNeeded, runs.req.ConfirmedPlan.Selection.Mode)
	})

	t.Run("asset closure selection", func(t *testing.T) {
		plan := confirmablePipelinePlan()
		plan.Selection = service.PipelinePlanSelection{
			Mode:      service.PipelinePlanSelectionAsset,
			AssetName: "analytics.orders",
			Scope:     "asset_with_upstreams",
		}
		planner := &pipelinePlanHandlerStub{plan: plan}
		runs := &pipelinePlanRunStub{}
		response := pipelinePlanConfirmRequest(planner, runs, `{
  "plan_id":"plan-id",
  "plan":{
    "execution_time":"2026-07-17T12:00:00Z",
    "selection":{
      "mode":"asset",
      "asset_name":"analytics.orders",
      "scope":"asset_with_upstreams"
    }
  }
}`)
		require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
		require.Equal(t, 1, runs.calls)
		require.NotNil(t, runs.req.ConfirmedPlan)
		assert.Equal(t, service.PipelinePlanSelectionAsset, runs.req.ConfirmedPlan.Selection.Mode)
		assert.Equal(t, "analytics.orders", runs.req.ConfirmedPlan.Selection.AssetName)
		assert.Equal(t, "asset_with_upstreams", runs.req.ConfirmedPlan.Selection.Scope)
	})
}

func TestHandleConfirmPipelinePlanRequiresDestructiveEnvironmentConfirmation(t *testing.T) {
	t.Parallel()
	newPlan := func() service.PipelinePlan {
		plan := confirmablePipelinePlan()
		plan.Status = service.PipelinePlanStatusWarning
		plan.Context.Destructive = true
		plan.Context.FullRefresh = true
		plan.Readiness.Warnings = []service.PipelinePlanIssue{{
			Code:     "destructive_confirmation_required",
			Severity: "warning",
			Message:  "running this destructive plan requires typing the environment name",
		}}
		return plan
	}

	t.Run("missing or mismatched confirmation", func(t *testing.T) {
		for _, confirmation := range []string{"", "production"} {
			planner := &pipelinePlanHandlerStub{plan: newPlan()}
			runs := &pipelinePlanRunStub{}
			response := pipelinePlanConfirmRequest(planner, runs, `{
  "plan_id":"plan-id",
  "plan":{"execution_time":"2026-07-17T12:00:00Z","selection":{"mode":"all"}},
  "confirmed_environment":"`+confirmation+`"
}`)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			assert.Contains(t, response.Body.String(), `"code":"destructive_confirmation_required"`)
			assert.Zero(t, runs.calls)
		}
	})

	t.Run("matching confirmation", func(t *testing.T) {
		planner := &pipelinePlanHandlerStub{plan: newPlan()}
		runs := &pipelinePlanRunStub{}
		response := pipelinePlanConfirmRequest(planner, runs, `{
  "plan_id":"plan-id",
  "plan":{"execution_time":"2026-07-17T12:00:00Z","selection":{"mode":"all"}},
  "confirmed_environment":"dev"
}`)
		require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
		require.Equal(t, 1, runs.calls)
		assert.Equal(t, "dev", runs.req.ConfirmedEnvironment)
	})
}

func TestHandleConfirmPipelinePlanAcceptsOnlyNeededPlanShrink(t *testing.T) {
	t.Parallel()
	t.Run("omitted unit", func(t *testing.T) {
		reviewed := confirmablePipelinePlan()
		reviewed.Selection.Mode = service.PipelinePlanSelectionNeeded
		reviewed.ExecutionUnits = append(reviewed.ExecutionUnits, service.PipelinePlanExecutionUnit{
			AssetID: "asset-2", AssetName: "analytics.customers",
			StartDate: "2026-07-17T11:00:00Z", EndDate: "2026-07-17T12:00:00Z",
			Reason: "uncovered_interval",
		})
		reviewed.ExecutionContracts = append(
			reviewed.ExecutionContracts,
			service.PipelinePlanExecutionContract{
				AssetID: "asset-2", AssetName: "analytics.customers",
				ConnectionKeys: []string{strings.Repeat("c", 64)},
				MutationResources: service.PipelinePlanResources{
					Isolation: service.PipelinePlanResourceIsolationResources,
					Claims:    []service.PipelinePlanResourceClaim{},
				},
				CoordinationResources: service.PipelinePlanResources{
					Isolation: service.PipelinePlanResourceIsolationResources,
					Claims:    []service.PipelinePlanResourceClaim{},
				},
			},
		)
		reviewed.ID = service.PipelinePlanReviewedIdentityID(service.PipelinePlanReviewedIdentityFromPlan(reviewed))

		current := reviewed
		current.Selection.DataStateToken = "renart-data-state-v1:" + strings.Repeat("e", 64)
		current.ExecutionUnits = append([]service.PipelinePlanExecutionUnit(nil), reviewed.ExecutionUnits[:1]...)
		current.ExecutionContracts = append(
			[]service.PipelinePlanExecutionContract(nil),
			reviewed.ExecutionContracts[:1]...,
		)
		current.ID = service.PipelinePlanReviewedIdentityID(service.PipelinePlanReviewedIdentityFromPlan(current))
		planner := &pipelinePlanHandlerStub{plan: current}
		runs := &pipelinePlanRunStub{run: scheduler.PipelineRun{ID: "run-shrunk"}}
		body, err := json.Marshal(service.PipelinePlanConfirmRequest{
			PlanID: reviewed.ID,
			Plan: service.PipelinePlanRequest{
				ExecutionTime: reviewed.Context.ExecutionTime,
				Selection:     service.PipelinePlanSelectionRequest{Mode: service.PipelinePlanSelectionNeeded},
			},
			Reviewed: pointerTo(service.PipelinePlanReviewedIdentityFromPlan(reviewed)),
		})
		require.NoError(t, err)
		response := pipelinePlanConfirmRequest(planner, runs, string(body))

		require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
		assert.Equal(t, 2, planner.calls, "configuration is re-evaluated for the reviewed asset set")
		require.Equal(t, 1, runs.calls)
		require.NotNil(t, runs.req.ConfirmedPlan)
		require.NotNil(t, runs.req.ConfirmedPlan.Preview)
		assert.Equal(t, reviewed.ID, runs.req.ConfirmedPlan.Preview.PlanID)
		require.Len(t, runs.req.ConfirmedPlan.Preview.OmittedExecutionUnits, 1)
		assert.Equal(t, "analytics.customers", runs.req.ConfirmedPlan.Preview.OmittedExecutionUnits[0].AssetName)
		assert.Contains(t, response.Body.String(), `"preview_units_omitted":1`)
	})

	t.Run("expanded unit", func(t *testing.T) {
		reviewed := confirmablePipelinePlan()
		reviewed.Selection.Mode = service.PipelinePlanSelectionNeeded
		reviewed.ID = service.PipelinePlanReviewedIdentityID(service.PipelinePlanReviewedIdentityFromPlan(reviewed))
		current := reviewed
		current.Selection.DataStateToken = "renart-data-state-v1:" + strings.Repeat("e", 64)
		current.ExecutionUnits = append(append([]service.PipelinePlanExecutionUnit(nil), reviewed.ExecutionUnits...), service.PipelinePlanExecutionUnit{
			AssetID: "asset-2", AssetName: "analytics.customers",
			StartDate: "2026-07-17T11:00:00Z", EndDate: "2026-07-17T12:00:00Z",
			Reason: "uncovered_interval",
		})
		current.ID = service.PipelinePlanReviewedIdentityID(service.PipelinePlanReviewedIdentityFromPlan(current))
		planner := &pipelinePlanHandlerStub{plan: current}
		runs := &pipelinePlanRunStub{}
		body, err := json.Marshal(service.PipelinePlanConfirmRequest{
			PlanID: reviewed.ID,
			Plan: service.PipelinePlanRequest{
				ExecutionTime: reviewed.Context.ExecutionTime,
				Selection:     service.PipelinePlanSelectionRequest{Mode: service.PipelinePlanSelectionNeeded},
			},
			Reviewed: pointerTo(service.PipelinePlanReviewedIdentityFromPlan(reviewed)),
		})
		require.NoError(t, err)
		response := pipelinePlanConfirmRequest(planner, runs, string(body))

		require.Equal(t, http.StatusConflict, response.Code)
		assert.Contains(t, response.Body.String(), `"code":"plan_data_changed"`)
		assert.Zero(t, runs.calls)
	})
}

func pointerTo[T any](value T) *T { return &value }

func TestHandleConfirmPipelinePlanReportsAdmissionRace(t *testing.T) {
	t.Parallel()
	planner := &pipelinePlanHandlerStub{plan: confirmablePipelinePlan()}
	runs := &pipelinePlanRunStub{err: &scheduler.PipelineRunActiveError{PipelineID: "pipeline-id", ActiveRunID: "run-active"}}
	response := pipelinePlanConfirmRequest(planner, runs, `{
  "plan_id":"plan-id",
  "plan":{"execution_time":"2026-07-17T12:00:00Z","selection":{"mode":"all"}}
}`)

	require.Equal(t, http.StatusConflict, response.Code)
	assert.Contains(t, response.Body.String(), `"code":"pipeline_run_active"`)
	assert.Contains(t, response.Body.String(), `"active_run_id":"run-active"`)
}

func confirmablePipelinePlan() service.PipelinePlan {
	return service.PipelinePlan{
		ID: "plan-id", Status: service.PipelinePlanStatusReady,
		PipelineID: "pipeline-id", PipelineUUID: "pipeline-uuid", PipelineName: "analytics",
		Source: service.AssetRenderSource{
			Kind: service.PipelinePlanSourceWorkingTree, MerkleRoot: strings.Repeat("a", 64),
		},
		Context: service.PipelinePlanContext{
			Environment: "dev", StartDate: "2026-07-17T11:00:00Z", EndDate: "2026-07-17T12:00:00Z",
			ExecutionTime: "2026-07-17T12:00:00Z", FullRefresh: true, SensorMode: "once",
			MaxActiveSteps: 2, ConfigurationDigest: strings.Repeat("b", 64), ConfigurationFidelity: "exact",
		},
		Readiness: service.PipelinePlanReadiness{Blockers: []service.PipelinePlanIssue{}, Warnings: []service.PipelinePlanIssue{}},
		Selection: service.PipelinePlanSelection{
			Mode: service.PipelinePlanSelectionAll, DataStateToken: "renart-data-state-v1:" + strings.Repeat("d", 64),
		},
		Assets: []service.PipelinePlanAsset{{
			ID: "asset-1", Name: "analytics.orders",
			Renders: []service.PipelinePlanRender{{
				StartDate: "2026-07-17T11:00:00Z", EndDate: "2026-07-17T12:00:00Z",
				Stages: []service.AssetRenderStage{{Kind: "query", Content: ""}},
			}},
		}},
		Resources: service.PipelinePlanResources{
			Isolation: service.PipelinePlanResourceIsolationResources,
			Claims:    []service.PipelinePlanResourceClaim{},
		},
		ExecutionContracts: []service.PipelinePlanExecutionContract{{
			AssetID: "asset-1", AssetName: "analytics.orders",
			AccessPolicyIdentity: strings.Repeat("e", 64),
			AccessRequirements:   []execution.AccessRequirement{{ConnectionKey: strings.Repeat("c", 64), Effect: policy.Write, Operation: "materialization"}},
			ConnectionKeys:       []string{strings.Repeat("c", 64)},
			MutationResources: service.PipelinePlanResources{
				Isolation: service.PipelinePlanResourceIsolationResources,
				Claims:    []service.PipelinePlanResourceClaim{},
			},
			CoordinationResources: service.PipelinePlanResources{
				Isolation: service.PipelinePlanResourceIsolationResources,
				Claims:    []service.PipelinePlanResourceClaim{},
			},
		}},
		ExecutionUnits: []service.PipelinePlanExecutionUnit{{
			AssetID: "asset-1", AssetName: "analytics.orders",
			StartDate: "2026-07-17T11:00:00Z", EndDate: "2026-07-17T12:00:00Z",
			Reason: "selected_all",
		}},
	}
}

func pipelinePlanRequest(stub *pipelinePlanHandlerStub, body string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	RegisterPipelinePlanRoutes(router, &PipelinePlanAPI{Service: stub})
	request := httptest.NewRequest(http.MethodPost, "/api/pipelines/pipeline-id/plan", strings.NewReader(body))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func pipelinePlanConfirmRequest(planner *pipelinePlanHandlerStub, runs *pipelinePlanRunStub, body string) *httptest.ResponseRecorder {
	router := chi.NewRouter()
	RegisterPipelinePlanRoutes(router, &PipelinePlanAPI{Service: planner, Runs: runs})
	request := httptest.NewRequest(http.MethodPost, "/api/pipelines/pipeline-id/plan/confirm", strings.NewReader(body))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
