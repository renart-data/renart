package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/go-chi/chi/v5"

	webapi "renart/internal/web/api"
	"renart/internal/web/scheduler"
	"renart/internal/web/service"
)

const maxPipelinePlanRequestBytes = 256 << 10

type PipelinePlanHandlers interface {
	Plan(ctx context.Context, pipelineID string, req service.PipelinePlanRequest) (service.PipelinePlan, *service.APIError)
}

type PipelinePlanAPI struct {
	Service PipelinePlanHandlers
	Runs    interface {
		TriggerPipeline(ctx context.Context, pipelineID string, req scheduler.TriggerRequest) (scheduler.PipelineRun, error)
	}
}

func RegisterPipelinePlanRoutes(router chi.Router, handlers *PipelinePlanAPI) {
	router.Post("/api/pipelines/{id}/plan", handlers.HandlePipelinePlan)
	router.Post("/api/pipelines/{id}/plan/confirm", handlers.HandleConfirmPipelinePlan)
}

func (h *PipelinePlanAPI) HandlePipelinePlan(w http.ResponseWriter, r *http.Request) {
	pipelineID := strings.TrimSpace(chi.URLParam(r, "id"))
	if pipelineID == "" {
		webapi.WriteBadRequest(w, "pipeline_id_required", "pipeline id is required")
		return
	}
	if h == nil || h.Service == nil {
		webapi.WriteInternalError(w, "planner_unavailable", "pipeline planning is unavailable")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPipelinePlanRequestBytes)
	req, err := decodeStrictJSONObject[service.PipelinePlanRequest](r.Body)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}

	plan, apiErr := h.Service.Plan(r.Context(), pipelineID, req)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	webapi.WriteJSON(w, http.StatusOK, plan)
}

func (h *PipelinePlanAPI) HandleConfirmPipelinePlan(w http.ResponseWriter, r *http.Request) {
	pipelineID := strings.TrimSpace(chi.URLParam(r, "id"))
	if pipelineID == "" {
		webapi.WriteBadRequest(w, "pipeline_id_required", "pipeline id is required")
		return
	}
	if h == nil || h.Service == nil || h.Runs == nil {
		webapi.WriteInternalError(w, "planner_unavailable", "pipeline plan confirmation is unavailable")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxPipelinePlanRequestBytes)
	req, err := decodeStrictJSONObject[service.PipelinePlanConfirmRequest](r.Body)
	if err != nil {
		webapi.WriteBadRequest(w, "invalid_request_body", err.Error())
		return
	}
	req.PlanID = strings.TrimSpace(req.PlanID)
	if req.PlanID == "" {
		webapi.WriteBadRequest(w, "plan_id_required", "plan_id is required")
		return
	}
	purpose := strings.TrimSpace(req.Plan.Purpose)
	if purpose != "" && purpose != service.PipelinePlanPurposeExecution {
		webapi.WriteBadRequest(w, "plan_purpose_not_confirmable", "only execution plans can be confirmed as runs")
		return
	}
	selectionMode := strings.TrimSpace(req.Plan.Selection.Mode)
	if selectionMode != service.PipelinePlanSelectionAll &&
		selectionMode != service.PipelinePlanSelectionNeeded &&
		selectionMode != service.PipelinePlanSelectionAsset &&
		selectionMode != service.PipelinePlanSelectionSelector &&
		selectionMode != service.PipelinePlanSelectionSelectorNeeded {
		webapi.WriteBadRequest(w, "plan_selection_not_confirmable", "the selected pipeline plan mode cannot be confirmed")
		return
	}
	if strings.TrimSpace(req.Plan.ExecutionTime) == "" {
		webapi.WriteBadRequest(w, "execution_time_required", "plan.execution_time is required for confirmation")
		return
	}
	req.Plan.IncludeStageContent = false

	plan, apiErr := h.Service.Plan(r.Context(), pipelineID, req.Plan)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var preview *scheduler.PipelineRunPlanPreview
	if plan.ID != req.PlanID {
		var outcome string
		preview, outcome = h.confirmNeededPlanShrink(r.Context(), pipelineID, req, plan)
		switch outcome {
		case "safe":
		case "expanded":
			webapi.WriteErrorWithDetails(w, http.StatusConflict, "plan_data_changed", "the Needed plan now requires additional or changed work; review the refreshed plan before running", map[string]any{"plan": plan})
			return
		default:
			webapi.WriteErrorWithDetails(w, http.StatusConflict, "plan_stale", "the pipeline plan changed; review the refreshed plan before running", map[string]any{"plan": plan})
			return
		}
	}
	if issue := pipelinePlanConfirmationIssue(plan); issue != nil &&
		!(issue.Code == "empty_execution_plan" && preview != nil && len(preview.ExecutionUnits) > 0) {
		alreadyReported := false
		for _, blocker := range plan.Readiness.Blockers {
			if blocker.Code == issue.Code {
				alreadyReported = true
				break
			}
		}
		if !alreadyReported {
			plan.Readiness.Blockers = append(plan.Readiness.Blockers, *issue)
		}
		plan.Summary.Blockers = len(plan.Readiness.Blockers)
		plan.Status = service.PipelinePlanStatusBlocked
	}
	if len(plan.Readiness.Blockers) > 0 || plan.Status == service.PipelinePlanStatusBlocked {
		webapi.WriteErrorWithDetails(w, http.StatusConflict, "plan_blocked", "the pipeline plan is not ready to run", map[string]any{"plan": plan})
		return
	}
	if pipelinePlanRequiresDestructiveConfirmation(plan) &&
		strings.TrimSpace(req.ConfirmedEnvironment) != strings.TrimSpace(plan.Context.Environment) {
		webapi.WriteBadRequest(w, "destructive_confirmation_required", "type the environment name to confirm destructive operations")
		return
	}

	run, err := h.Runs.TriggerPipeline(r.Context(), pipelineID, scheduler.TriggerRequest{
		Environment:                 plan.Context.Environment,
		Start:                       plan.Context.StartDate,
		End:                         plan.Context.EndDate,
		Source:                      scheduler.RunSource(plan.Source.Kind),
		SnapshotVersionID:           plan.Source.VersionID,
		FullRefresh:                 plan.Context.FullRefresh,
		Backfill:                    plan.Context.Backfill,
		ConfirmedEnvironment:        strings.TrimSpace(req.ConfirmedEnvironment),
		SensorMode:                  plan.Context.SensorMode,
		ExpectedSourceMerkle:        plan.Source.MerkleRoot,
		ExpectedConfigurationDigest: plan.Context.ConfigurationDigest,
		ExecutionTime:               plan.Context.ExecutionTime,
		ConfirmedPlan:               durablePipelineRunPlan(plan, preview),
	})
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
		webapi.WriteBadRequest(w, "pipeline_plan_confirm_failed", err.Error())
		return
	}
	webapi.WriteJSON(w, http.StatusAccepted, map[string]any{
		"status": "ok", "plan_id": plan.ID, "run": run,
		"preview_units_omitted": pipelineRunPlanOmittedCount(preview),
	})
}

func durablePipelineRunPlan(plan service.PipelinePlan, preview *scheduler.PipelineRunPlanPreview) *scheduler.PipelineRunPlan {
	artifact, err := json.Marshal(plan)
	if err != nil {
		// PipelinePlan contains only JSON-compatible value types. Returning an
		// invalid plan makes scheduler admission fail closed if that changes.
		artifact = nil
	}
	units := make([]scheduler.PipelineRunExecutionUnit, 0, len(plan.ExecutionUnits))
	for _, unit := range plan.ExecutionUnits {
		units = append(units, scheduler.PipelineRunExecutionUnit{
			AssetID: unit.AssetID, AssetName: unit.AssetName,
			StartDate: unit.StartDate, EndDate: unit.EndDate,
			RenderIndex: unit.RenderIndex, Reason: unit.Reason,
			DependencyPositions: append([]int(nil), unit.DependencyPositions...),
		})
	}
	claims := make([]scheduler.PipelineRunResourceClaim, 0, len(plan.Resources.Claims))
	for _, claim := range plan.Resources.Claims {
		claims = append(claims, scheduler.PipelineRunResourceClaim{
			Kind: claim.Kind, Identity: claim.Identity,
		})
	}
	contracts := make([]scheduler.PipelineRunExecutionContract, 0, len(plan.ExecutionContracts))
	for _, contract := range plan.ExecutionContracts {
		contracts = append(contracts, schedulerPipelineRunExecutionContract(contract))
	}
	prerequisites := make([]scheduler.PipelineRunPrerequisite, 0, len(plan.Prerequisites))
	for _, prerequisite := range plan.Prerequisites {
		prerequisites = append(prerequisites, schedulerPipelineRunPrerequisite(prerequisite))
	}
	return &scheduler.PipelineRunPlan{
		Version: scheduler.PipelineRunPlanVersionV3,
		PlanID:  plan.ID, PipelineID: plan.PipelineID, PipelineUUID: plan.PipelineUUID,
		SourceMerkle:        plan.Source.MerkleRoot,
		ConfigurationDigest: plan.Context.ConfigurationDigest,
		ExecutionTime:       plan.Context.ExecutionTime,
		MaxActiveSteps:      plan.Context.MaxActiveSteps,
		Selection: scheduler.PipelineRunPlanSelection{
			Mode: plan.Selection.Mode, AssetName: plan.Selection.AssetName,
			Scope: plan.Selection.Scope, Selector: plan.Selection.Selector,
			DataStateToken: plan.Selection.DataStateToken,
		},
		Resources: scheduler.PipelineRunPlanResources{
			Isolation: plan.Resources.Isolation,
			Claims:    claims,
		},
		ExecutionContracts: contracts,
		Prerequisites:      prerequisites,
		ExecutionUnits:     units,
		Preview:            preview,
		Artifact:           artifact,
	}
}

func schedulerPipelineRunPrerequisite(item service.PipelinePlanPrerequisite) scheduler.PipelineRunPrerequisite {
	return scheduler.PipelineRunPrerequisite{
		Status: item.Status, Reason: item.Reason,
		ConsumerAssetID: item.ConsumerAssetID, ConsumerAssetName: item.ConsumerAssetName,
		URI:                item.URI,
		ProducerPipelineID: item.ProducerPipelineID, ProducerPipelineUUID: item.ProducerPipelineUUID,
		ProducerPipelineName: item.ProducerPipelineName,
		ProducerAssetID:      item.ProducerAssetID, ProducerAssetName: item.ProducerAssetName,
		Environment: item.Environment, RequiredStart: item.RequiredStart, RequiredEnd: item.RequiredEnd,
		ExpectedFingerprint: item.ExpectedFingerprint, TargetIdentity: item.TargetIdentity, VarsHash: item.VarsHash,
		TargetGeneration: item.TargetGeneration, WriterRunID: item.WriterRunID,
		WriterSnapshotVersionID: item.WriterSnapshotVersionID,
		WriterCompletionID:      item.WriterCompletionID, WriterCompletionOrdinal: item.WriterCompletionOrdinal,
		WriterMaterializedAt: item.WriterMaterializedAt,
		CoveredSeconds:       item.CoveredSeconds, RequiredSeconds: item.RequiredSeconds,
	}
}

func schedulerPipelineRunExecutionContract(
	contract service.PipelinePlanExecutionContract,
) scheduler.PipelineRunExecutionContract {
	return scheduler.PipelineRunExecutionContract{
		AssetID:               contract.AssetID,
		AssetName:             contract.AssetName,
		ConnectionKeys:        append([]string(nil), contract.ConnectionKeys...),
		MutationResources:     schedulerPipelinePlanResources(contract.MutationResources),
		CoordinationResources: schedulerPipelinePlanResources(contract.CoordinationResources),
	}
}

func schedulerPipelinePlanResources(
	resources service.PipelinePlanResources,
) scheduler.PipelineRunPlanResources {
	claims := make([]scheduler.PipelineRunResourceClaim, 0, len(resources.Claims))
	for _, claim := range resources.Claims {
		claims = append(claims, scheduler.PipelineRunResourceClaim{
			Kind: claim.Kind, Identity: claim.Identity,
		})
	}
	return scheduler.PipelineRunPlanResources{
		Isolation: resources.Isolation,
		Claims:    claims,
	}
}

func (h *PipelinePlanAPI) confirmNeededPlanShrink(
	ctx context.Context,
	pipelineID string,
	req service.PipelinePlanConfirmRequest,
	current service.PipelinePlan,
) (*scheduler.PipelineRunPlanPreview, string) {
	selectionMode := strings.TrimSpace(req.Plan.Selection.Mode)
	if (selectionMode != service.PipelinePlanSelectionNeeded &&
		selectionMode != service.PipelinePlanSelectionSelectorNeeded) || req.Reviewed == nil {
		return nil, "stale"
	}
	reviewed := *req.Reviewed
	if service.PipelinePlanReviewedIdentityID(reviewed) != req.PlanID || !sameNeededPlanNonDataIdentity(reviewed, current) {
		return nil, "stale"
	}
	assetNames := make([]string, 0, len(reviewed.ExecutionUnits))
	seen := make(map[string]struct{}, len(reviewed.ExecutionUnits))
	for _, unit := range reviewed.ExecutionUnits {
		name := strings.TrimSpace(unit.AssetName)
		if name == "" {
			return nil, "stale"
		}
		if _, exists := seen[name]; !exists {
			seen[name] = struct{}{}
			assetNames = append(assetNames, name)
		}
	}
	verificationRequest := req.Plan
	verificationRequest.IncludeStageContent = false
	verificationRequest.ConfigurationAssetNames = assetNames
	verification, apiErr := h.Service.Plan(ctx, pipelineID, verificationRequest)
	if apiErr != nil || verification.Context.ConfigurationFidelity != reviewed.Context.ConfigurationFidelity ||
		verification.Context.ConfigurationDigest != reviewed.Context.ConfigurationDigest {
		return nil, "stale"
	}

	omitted, expanded := neededExecutionUnitDelta(reviewed.ExecutionUnits, current.ExecutionUnits)
	if expanded {
		return nil, "expanded"
	}
	return &scheduler.PipelineRunPlanPreview{
		PlanID: req.PlanID, DataStateToken: reviewed.Selection.DataStateToken,
		ExecutionUnits:        schedulerExecutionUnits(reviewed.ExecutionUnits),
		OmittedExecutionUnits: schedulerExecutionUnits(omitted),
	}, "safe"
}

func sameNeededPlanNonDataIdentity(reviewed service.PipelinePlanReviewedIdentity, current service.PipelinePlan) bool {
	if reviewed.PipelineUUID != current.PipelineUUID || reviewed.Source != current.Source {
		return false
	}
	if !reflect.DeepEqual(reviewed.Prerequisites, current.Prerequisites) {
		return false
	}
	reviewedContracts := make(map[string]service.PipelinePlanExecutionContract, len(reviewed.ExecutionContracts))
	for _, contract := range reviewed.ExecutionContracts {
		reviewedContracts[contract.AssetID] = contract
	}
	for _, contract := range current.ExecutionContracts {
		if reviewedContract, exists := reviewedContracts[contract.AssetID]; !exists ||
			!reflect.DeepEqual(reviewedContract, contract) {
			return false
		}
	}
	reviewedSelection := reviewed.Selection
	currentSelection := current.Selection
	reviewedSelection.DataStateToken = ""
	currentSelection.DataStateToken = ""
	if reviewedSelection != currentSelection {
		return false
	}
	reviewedContext := reviewed.Context
	currentContext := current.Context
	reviewedContext.ConfigurationDigest, currentContext.ConfigurationDigest = "", ""
	reviewedContext.ConfigurationFidelity, currentContext.ConfigurationFidelity = "", ""
	reviewedContext.FullRefresh, currentContext.FullRefresh = false, false
	reviewedContext.Destructive, currentContext.Destructive = false, false
	return reflect.DeepEqual(reviewedContext, currentContext)
}

func neededExecutionUnitDelta(reviewed, current []service.PipelinePlanExecutionUnit) ([]service.PipelinePlanExecutionUnit, bool) {
	available := make(map[string][]int, len(reviewed))
	for index, unit := range reviewed {
		available[pipelinePlanExecutionUnitKey(unit)] = append(available[pipelinePlanExecutionUnitKey(unit)], index)
	}
	consumed := make([]bool, len(reviewed))
	for _, unit := range current {
		key := pipelinePlanExecutionUnitKey(unit)
		indexes := available[key]
		matched := -1
		for _, index := range indexes {
			if !consumed[index] {
				matched = index
				break
			}
		}
		if matched < 0 {
			return nil, true
		}
		consumed[matched] = true
	}
	omitted := make([]service.PipelinePlanExecutionUnit, 0)
	for index, unit := range reviewed {
		if !consumed[index] {
			omitted = append(omitted, unit)
		}
	}
	return omitted, false
}

func pipelinePlanExecutionUnitKey(unit service.PipelinePlanExecutionUnit) string {
	return strings.Join([]string{unit.AssetID, unit.AssetName, unit.StartDate, unit.EndDate, unit.Reason}, "\x00")
}

func schedulerExecutionUnits(units []service.PipelinePlanExecutionUnit) []scheduler.PipelineRunExecutionUnit {
	result := make([]scheduler.PipelineRunExecutionUnit, 0, len(units))
	for _, unit := range units {
		result = append(result, scheduler.PipelineRunExecutionUnit{
			AssetID: unit.AssetID, AssetName: unit.AssetName,
			StartDate: unit.StartDate, EndDate: unit.EndDate,
			RenderIndex: unit.RenderIndex, Reason: unit.Reason,
			DependencyPositions: append([]int(nil), unit.DependencyPositions...),
		})
	}
	return result
}

func pipelineRunPlanOmittedCount(preview *scheduler.PipelineRunPlanPreview) int {
	if preview == nil {
		return 0
	}
	return len(preview.OmittedExecutionUnits)
}

func pipelinePlanRequiresDestructiveConfirmation(plan service.PipelinePlan) bool {
	if !plan.Context.Destructive {
		return false
	}
	for _, warning := range plan.Readiness.Warnings {
		if warning.Code == "destructive_confirmation_required" {
			return true
		}
	}
	return false
}

func pipelinePlanConfirmationIssue(plan service.PipelinePlan) *service.PipelinePlanIssue {
	if strings.TrimSpace(plan.Source.MerkleRoot) == "" {
		return &service.PipelinePlanIssue{
			Code: "source_identity_unavailable", Severity: "error",
			Message: "the selected source cannot be bound to an exact identity",
		}
	}
	if plan.Context.ConfigurationFidelity != "exact" || strings.TrimSpace(plan.Context.ConfigurationDigest) == "" {
		return &service.PipelinePlanIssue{
			Code: "configuration_identity_unavailable", Severity: "error",
			Message: "the selected execution configuration cannot be bound to an exact identity",
		}
	}
	if len(plan.ExecutionUnits) == 0 {
		return &service.PipelinePlanIssue{
			Code: "empty_execution_plan", Severity: "error",
			Message: "the pipeline plan contains no execution units",
		}
	}
	return nil
}
