package execution

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"

	"renart/internal/web/apperror"
	"renart/internal/web/policy"
	"renart/internal/web/runcontext"
	"renart/internal/web/staleness"
	webtypecheck "renart/internal/web/typecheck"
)

type PlannerStaleness interface {
	Evaluate(context.Context, staleness.Selection, *pipeline.Pipeline) (staleness.Snapshot, error)
}

type ConfigurationIdentity struct {
	Digest   string
	Fidelity string
}

// PlannerConfiguration is a selected environment plus the secret-free
// identity operations needed during review. Concrete Bruin configuration and
// credentials remain in the adapter.
type PlannerConfiguration interface {
	EnvironmentName() string
	SchemaPrefix() string
	InitialIdentity() ConfigurationIdentity
	BindSelection(*pipeline.Pipeline, []*pipeline.Asset, []string) (ConfigurationIdentity, *apperror.Error)
}

// PlannerSource is one exact working-tree or deployed source. Verification is
// performed after all read-only planning work so a concurrent edit cannot be
// admitted under an earlier identity.
type PlannerSource interface {
	Pipeline() *pipeline.Pipeline
	Identity() RenderSource
	Verify() *apperror.Error
	Close()
}

type PlannerVariableContext struct {
	Digest     string
	Provenance []VariableProvenance
}

type PlannerSessionInput struct {
	Plan          *Plan
	Request       PlanRequest
	Purpose       string
	Window        TimeWindow
	ExecutionTime time.Time
}

type PlannedAssetResult struct {
	Asset                 PlanAsset
	ExecutionUnits        []PlanExecutionUnit
	Blockers              []PlanIssue
	Warnings              []PlanIssue
	Stages                int
	DestructiveOperations int
	FullRefresh           bool
}

// PlannerSession adapts source-specific type-checking, rendering, immutable
// prerequisite evidence, and execution-target binding. One session owns any
// temporary snapshot workspace and renderer cache for the complete plan.
type PlannerSession interface {
	CodeChecks() webtypecheck.Report
	ApplyPrerequisites(context.Context, *Plan, []SelectedPlanAsset)
	PlanAsset(context.Context, SelectedPlanAsset, staleness.AssetStatus, TimeWindow) (PlannedAssetResult, *apperror.Error)
	BindExecutionContracts(context.Context, []PlanAsset) ([]ExecutionContract, *apperror.Error)
	Close()
}

// PlannerSemanticImpact is an optional deployment-review capability. Execution
// planning adapters need not implement it.
type PlannerSemanticImpact interface {
	SemanticImpact(context.Context) SemanticImpactReport
}

type PlannerDependencies struct {
	ResolvePipelineUUID func(string) (string, bool)
	LoadConfiguration   func(string) (PlannerConfiguration, error)
	ResolveSource       func(context.Context, string, string, PlanSourceRequest, map[string]any) (PlannerSource, bool, *apperror.Error)
	OpenSession         func(context.Context, PlannerSource, PlannerConfiguration, PlannerSessionInput) (PlannerSession, *apperror.Error)
	ResolveVariables    func(*pipeline.Pipeline, map[string]any, string) PlannerVariableContext
	IsExecutable        func(*pipeline.Asset) bool
	EffectiveSensorMode func(string, bool) string
	Staleness           PlannerStaleness
	PolicyFor           func(string) policy.EnvironmentPolicy
	ConflictingRunID    func(context.Context, string, string, Resources) (string, error)
	Now                 func() time.Time
}

// Planner owns the reviewed execution-plan workflow. Filesystem snapshots,
// Bruin rendering, type-checking, and durable evidence remain behind ports;
// request normalization, selection, policy, resource admission, and identity
// are one deterministic application service.
type Planner struct {
	deps PlannerDependencies
}

func NewPlanner(deps PlannerDependencies) *Planner {
	return &Planner{deps: deps}
}

func (p *Planner) Plan(ctx context.Context, pipelineID string, req PlanRequest) (Plan, *apperror.Error) {
	if p == nil || p.deps.ResolvePipelineUUID == nil || p.deps.LoadConfiguration == nil ||
		p.deps.ResolveSource == nil || p.deps.OpenSession == nil || p.deps.ResolveVariables == nil ||
		p.deps.IsExecutable == nil || p.deps.Staleness == nil {
		return Plan{}, applicationError(500, "planner_unavailable", "pipeline planning is unavailable")
	}
	pipelineID = strings.TrimSpace(pipelineID)
	if pipelineID == "" {
		return Plan{}, applicationError(400, "pipeline_id_required", "pipeline id is required")
	}
	pipelineUUID, ok := p.deps.ResolvePipelineUUID(pipelineID)
	if !ok {
		return Plan{}, applicationError(404, "pipeline_not_found", "pipeline not found")
	}

	normalized, err := runcontext.Normalize(runcontext.Input{
		Start: req.StartDate, End: req.EndDate, FullRefresh: req.FullRefresh,
		Backfill: req.Backfill, SensorMode: req.SensorMode,
	})
	if err != nil {
		return Plan{}, applicationError(400, "invalid_execution_context", err.Error())
	}
	executionTime, err := p.executionTime(req.ExecutionTime)
	if err != nil {
		return Plan{}, applicationError(400, "invalid_execution_time", "execution_time must be an RFC3339 timestamp")
	}
	configuration, err := p.deps.LoadConfiguration(req.Environment)
	if err != nil || configuration == nil {
		return Plan{}, applicationError(400, "invalid_environment", "selected environment is not configured")
	}
	environment := configuration.EnvironmentName()
	environmentPolicy := policy.EnvironmentPolicy{}
	if p.deps.PolicyFor != nil {
		environmentPolicy = p.deps.PolicyFor(environment)
	}

	purpose, err := NormalizePlanPurpose(req.Purpose)
	if err != nil {
		return Plan{}, applicationError(400, "invalid_plan_purpose", err.Error())
	}
	if purpose == PlanPurposeDeployment && strings.TrimSpace(req.Source.Kind) == "" {
		req.Source.Kind = PlanSourceWorkingTree
	}
	sourceRequest, err := NormalizePlanSource(req.Source, environmentPolicy.DeployedOnly)
	if err != nil {
		return Plan{}, applicationError(400, "invalid_plan_source", err.Error())
	}
	if purpose == PlanPurposeDeployment && sourceRequest.Kind != PlanSourceWorkingTree {
		return Plan{}, applicationError(400, "invalid_plan_source", "deployment review requires the saved working tree")
	}
	selectionRequest, err := NormalizePlanSelection(req.Selection)
	if err != nil {
		return Plan{}, applicationError(400, "invalid_plan_selection", err.Error())
	}
	if purpose == PlanPurposeDeployment && selectionRequest.Mode != PlanSelectionAll {
		return Plan{}, applicationError(400, "invalid_plan_selection", "deployment review requires the entire pipeline")
	}

	initialIdentity := configuration.InitialIdentity()
	base := newPlanBase(
		pipelineID, pipelineUUID, sourceRequest, selectionRequest, environment,
		configuration.SchemaPrefix(), initialIdentity, executionTime, req,
		p.effectiveSensorMode(normalized.SensorMode, req.Scheduled),
	)
	source, deploymentRequired, apiErr := p.deps.ResolveSource(
		ctx, pipelineID, pipelineUUID, sourceRequest, req.VariableOverrides,
	)
	if apiErr != nil {
		return Plan{}, apiErr
	}
	if deploymentRequired {
		base.Readiness.Blockers = append(base.Readiness.Blockers, PlanIssue{
			Code: "deployment_required", Severity: "error",
			Message: "this environment only executes deployed snapshots; deploy the pipeline first",
		})
		finalizePlan(&base)
		return base, nil
	}
	if source == nil || source.Pipeline() == nil {
		return Plan{}, applicationError(500, "pipeline_source_unavailable", "pipeline source is unavailable")
	}
	defer source.Close()
	parsed := source.Pipeline()
	base.Source = source.Identity()
	base.PipelineName = parsed.Name

	window, err := ResolveTimeWindow(
		string(parsed.Schedule), normalized.StartString(), normalized.EndString(), executionTime,
	)
	if err != nil {
		return Plan{}, applicationError(400, "invalid_time_window", err.Error())
	}
	variables := p.deps.ResolveVariables(parsed, req.VariableOverrides, req.VariableOverrideSource)
	base.Context.StartDate = window.StartRFC3339()
	base.Context.EndDate = window.EndRFC3339()
	base.Context.MaxActiveSteps = EffectiveMaxActiveSteps(parsed)
	base.Context.VariablesDigest = variables.Digest
	base.Context.VariableProvenance = variables.Provenance
	base.Context.Destructive = req.Backfill

	session, apiErr := p.deps.OpenSession(ctx, source, configuration, PlannerSessionInput{
		Plan: &base, Request: req, Purpose: purpose, Window: window, ExecutionTime: executionTime,
	})
	if apiErr != nil {
		return Plan{}, apiErr
	}
	if session == nil {
		return Plan{}, applicationError(500, "planner_session_unavailable", "pipeline planning session is unavailable")
	}
	defer session.Close()
	base.Readiness.CodeChecks = session.CodeChecks()
	base.Readiness.CodeChecks.PipelineID = pipelineID
	appendCodeCheckIssues(&base, purpose == PlanPurposeDeployment)

	staleSnapshot := staleness.Snapshot{}
	dataStateAvailable := false
	if purpose == PlanPurposeExecution && !req.SkipDataStateCheck {
		staleSnapshot, err = p.deps.Staleness.Evaluate(ctx, staleness.Selection{
			PipelineUUID: pipelineUUID, EncodedPipelineID: pipelineID,
			Environment: environment, Start: &window.Start, End: &window.End,
			VarOverrides: req.VariableOverrides,
		}, parsed)
		if err != nil {
			base.Readiness.Blockers = append(base.Readiness.Blockers, PlanIssue{
				Code: "data_state_unavailable", Severity: "error",
				Message: "current data state could not be evaluated for this source and environment",
			})
		} else {
			dataStateAvailable = true
			base.Selection.DataStateToken = staleSnapshot.DataStateToken
		}
	}

	selected, err := SelectPlanAssets(parsed, selectionRequest, staleSnapshot.Assets, dataStateAvailable)
	if err != nil {
		return Plan{}, applicationError(400, "invalid_plan_selection", err.Error())
	}
	session.ApplyPrerequisites(ctx, &base, selected)
	if req.Backfill && (selectionRequest.Mode != PlanSelectionAsset || selectionRequest.Scope != "asset") {
		base.Readiness.Blockers = append(base.Readiness.Blockers, PlanIssue{
			Code: "backfill_scope_unsupported", Severity: "error",
			Message: "backfill currently requires one explicitly selected asset without dependency expansion",
		})
	}

	selectedAssets := make([]*pipeline.Asset, 0, len(selected))
	for _, item := range selected {
		if p.deps.IsExecutable(item.Asset) {
			selectedAssets = append(selectedAssets, item.Asset)
		}
	}
	selectedIdentity, apiErr := configuration.BindSelection(parsed, selectedAssets, req.ConfigurationAssetNames)
	if apiErr != nil {
		return Plan{}, apiErr
	}
	base.Context.ConfigurationDigest = selectedIdentity.Digest
	base.Context.ConfigurationFidelity = selectedIdentity.Fidelity
	if len(selectedAssets) > 0 && (selectedIdentity.Fidelity != "exact" || selectedIdentity.Digest == "") {
		issue := PlanIssue{
			Code: "configuration_identity_unavailable", Severity: "error",
			Message: "the selected execution configuration cannot be bound to a stable, secret-free identity",
		}
		if purpose == PlanPurposeDeployment {
			issue.Severity = "warning"
			base.Readiness.Warnings = append(base.Readiness.Warnings, issue)
		} else {
			base.Readiness.Blockers = append(base.Readiness.Blockers, issue)
		}
	}

	statusByName := make(map[string]staleness.AssetStatus, len(staleSnapshot.Assets))
	for _, status := range staleSnapshot.Assets {
		statusByName[status.AssetName] = status
	}
	for _, item := range selected {
		result, planErr := session.PlanAsset(ctx, item, statusByName[item.Asset.Name], window)
		if planErr != nil {
			return Plan{}, planErr
		}
		base.Assets = append(base.Assets, result.Asset)
		base.ExecutionUnits = append(base.ExecutionUnits, result.ExecutionUnits...)
		base.Readiness.Blockers = append(base.Readiness.Blockers, result.Blockers...)
		base.Readiness.Warnings = append(base.Readiness.Warnings, result.Warnings...)
		base.Summary.Stages += result.Stages
		base.Summary.DestructiveOperations += result.DestructiveOperations
		base.Context.FullRefresh = base.Context.FullRefresh || result.FullRefresh
	}
	if err := BindPlanExecutionDependencies(parsed, base.ExecutionUnits); err != nil {
		return Plan{}, applicationError(500, "execution_graph_invalid", "selected execution dependencies could not be bound")
	}
	base.ExecutionContracts, apiErr = session.BindExecutionContracts(ctx, base.Assets)
	if apiErr != nil {
		return Plan{}, apiErr
	}
	base.Resources = AggregateMutationResources(base.ExecutionContracts)
	if purpose == PlanPurposeDeployment {
		if provider, ok := session.(PlannerSemanticImpact); ok {
			report := provider.SemanticImpact(ctx)
			base.SemanticImpact = &report
			appendSemanticImpactIssues(&base, report)
		}
	}
	if purpose == PlanPurposeExecution && !req.SkipActiveRunCheck {
		p.appendActiveRunIssue(ctx, &base)
	}
	if req.Backfill {
		base.Summary.DestructiveOperations += len(base.ExecutionUnits)
	}
	base.Context.Destructive = base.Summary.DestructiveOperations > 0
	if purpose == PlanPurposeExecution {
		appendPolicyIssues(&base, environmentPolicy, req.Scheduled)
	}
	if apiErr := source.Verify(); apiErr != nil {
		return Plan{}, apiErr
	}
	finalizePlan(&base)
	return base, nil
}

func appendSemanticImpactIssues(plan *Plan, report SemanticImpactReport) {
	switch report.Status {
	case SemanticImpactStatusNoBaseline:
		return
	case SemanticImpactStatusAvailable:
		if !report.Complete {
			plan.Readiness.Warnings = append(plan.Readiness.Warnings, PlanIssue{
				Code: "semantic_impact_incomplete", Severity: "warning",
				Message: "Semantic impact analysis is incomplete; unknown schema facts may hide additional changes.",
			})
		}
		if report.Summary.Warnings > 0 {
			plan.Readiness.Warnings = append(plan.Readiness.Warnings, PlanIssue{
				Code: "semantic_impact_detected", Severity: "warning",
				Message: fmt.Sprintf(
					"Semantic impact analysis found %d potentially behavior- or schema-affecting asset changes.",
					report.Summary.Warnings,
				),
			})
		}
	default:
		message := strings.TrimSpace(report.Reason)
		if message == "" {
			message = "Semantic impact analysis could not compare this deployment with its baseline."
		}
		plan.Readiness.Warnings = append(plan.Readiness.Warnings, PlanIssue{
			Code: "semantic_impact_unavailable", Severity: "warning", Message: message,
		})
	}
}

func newPlanBase(
	pipelineID, pipelineUUID string,
	source PlanSourceRequest,
	selection PlanSelectionRequest,
	environment, schemaPrefix string,
	identity ConfigurationIdentity,
	executionTime time.Time,
	req PlanRequest,
	sensorMode string,
) Plan {
	return Plan{
		Status: PlanStatusBlocked, PipelineID: pipelineID, PipelineUUID: pipelineUUID,
		Source: RenderSource{Kind: source.Kind, VersionID: source.VersionID},
		Context: PlanContext{
			Environment: environment, SchemaPrefix: schemaPrefix,
			ExecutionTime: executionTime.Format(time.RFC3339Nano), MaxActiveSteps: 1,
			RequestedFullRefresh: req.FullRefresh, Backfill: req.Backfill, SensorMode: sensorMode,
			ConfigurationDigest: identity.Digest, ConfigurationFidelity: identity.Fidelity,
		},
		Readiness: PlanReadiness{
			CodeChecks: webtypecheck.Report{Assets: []webtypecheck.Asset{}},
			Blockers:   []PlanIssue{}, Warnings: []PlanIssue{},
		},
		Selection: PlanSelection{
			Mode: selection.Mode, AssetName: selection.AssetName,
			Scope: selection.Scope, Selector: selection.Selector,
		},
		Resources: PipelineExclusiveResources(), Assets: []PlanAsset{},
		Prerequisites: []Prerequisite{}, ExecutionContracts: []ExecutionContract{},
		ExecutionUnits: []PlanExecutionUnit{},
	}
}

func appendCodeCheckIssues(plan *Plan, includePresentations bool) {
	for _, asset := range plan.Readiness.CodeChecks.Assets {
		assetID := plan.PipelineUUID + ":" + asset.Name
		for _, finding := range asset.Findings {
			issue := PlanIssue{
				Code: "code_check_" + finding.Severity, Severity: finding.Severity,
				Message: finding.Message, AssetID: assetID, AssetName: asset.Name,
				DiagnosticCode: finding.Code, Target: finding.Target,
			}
			if finding.Severity == "error" {
				plan.Readiness.Blockers = append(plan.Readiness.Blockers, issue)
			} else {
				plan.Readiness.Warnings = append(plan.Readiness.Warnings, issue)
			}
		}
	}
	if !includePresentations {
		return
	}
	for _, artifact := range plan.Readiness.CodeChecks.Presentations {
		label := strings.TrimSpace(artifact.Title)
		if label == "" {
			label = artifact.ID
		}
		kind := strings.TrimSpace(artifact.Kind)
		if kind != "" {
			kind = strings.ToUpper(kind[:1]) + kind[1:]
		} else {
			kind = "Presentation"
		}
		for _, finding := range artifact.Findings {
			issue := PlanIssue{
				Code: "presentation_check_" + finding.Severity, Severity: finding.Severity,
				DiagnosticCode: finding.Code, Target: finding.Target,
				Message: fmt.Sprintf("%s %q: %s", kind, label, finding.Message),
			}
			if finding.Severity == "error" {
				plan.Readiness.Blockers = append(plan.Readiness.Blockers, issue)
			} else {
				plan.Readiness.Warnings = append(plan.Readiness.Warnings, issue)
			}
		}
	}
}

func appendPolicyIssues(plan *Plan, environmentPolicy policy.EnvironmentPolicy, scheduled bool) {
	if environmentPolicy.Protected && !scheduled {
		plan.Readiness.Blockers = append(plan.Readiness.Blockers, PlanIssue{
			Code: "interactive_execution_protected", Severity: "error",
			Message: "interactive execution is disabled for this protected environment",
		})
	}
	if environmentPolicy.DeployedOnly && plan.Source.Kind != PlanSourceSnapshot {
		plan.Readiness.Blockers = append(plan.Readiness.Blockers, PlanIssue{
			Code: "deployed_source_required", Severity: "error",
			Message: "this environment only executes deployed snapshots",
		})
	}
	if environmentPolicy.ConfirmDestructive && plan.Context.Destructive {
		plan.Readiness.Warnings = append(plan.Readiness.Warnings, PlanIssue{
			Code: "destructive_confirmation_required", Severity: "warning",
			Message: "running this destructive plan requires typing the environment name",
		})
	}
}

func (p *Planner) appendActiveRunIssue(ctx context.Context, plan *Plan) {
	if p.deps.ConflictingRunID == nil {
		return
	}
	runID, err := p.deps.ConflictingRunID(ctx, plan.PipelineID, plan.PipelineUUID, plan.Resources)
	if err != nil {
		plan.Readiness.Warnings = append(plan.Readiness.Warnings, PlanIssue{
			Code: "active_run_check_failed", Severity: "warning",
			Message: "Renart could not determine whether the selected write resources are already active",
		})
		return
	}
	if strings.TrimSpace(runID) == "" {
		return
	}
	plan.Readiness.ActiveRun = runID
	plan.Readiness.Blockers = append(plan.Readiness.Blockers, PlanIssue{
		Code: "pipeline_already_running", Severity: "error",
		Message: "another queued or running execution owns one of the selected write resources",
	})
}

func finalizePlan(plan *Plan) {
	plan.Readiness.Blockers = DedupePlanIssues(plan.Readiness.Blockers)
	plan.Readiness.Warnings = DedupePlanIssues(plan.Readiness.Warnings)
	plan.Readiness.Warnings = AggregatePythonRuntimeWarnings(plan.Assets, plan.Readiness.Warnings)
	plan.Summary.Assets = len(plan.Assets)
	plan.Summary.ExecutionUnits = len(plan.ExecutionUnits)
	plan.Summary.Blockers = len(plan.Readiness.Blockers)
	plan.Summary.Warnings = len(plan.Readiness.Warnings)
	plan.Status = PlanStatusReady
	if plan.Summary.Warnings > 0 {
		plan.Status = PlanStatusWarning
	}
	if plan.Summary.Blockers > 0 {
		plan.Status = PlanStatusBlocked
	}
	plan.ID = PlanID(*plan)
}

func AggregatePythonRuntimeWarnings(assets []PlanAsset, issues []PlanIssue) []PlanIssue {
	pythonAssets := make(map[string]string)
	for _, asset := range assets {
		if strings.Contains(strings.ToLower(asset.Type), "python") {
			pythonAssets[asset.ID] = asset.Name
		}
	}
	names := make([]string, 0)
	for _, issue := range issues {
		if issue.Code != "asset_render_partial" || !strings.Contains(strings.ToLower(issue.Message), "runtime") {
			continue
		}
		if name, ok := pythonAssets[issue.AssetID]; ok {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return issues
	}
	result := make([]PlanIssue, 0, len(issues)-len(names)+1)
	added := false
	for _, issue := range issues {
		_, isPython := pythonAssets[issue.AssetID]
		isRuntimeWarning := issue.Code == "asset_render_partial" && strings.Contains(strings.ToLower(issue.Message), "runtime")
		if !isPython || !isRuntimeWarning {
			result = append(result, issue)
			continue
		}
		if added {
			continue
		}
		added = true
		if len(names) == 1 {
			result = append(result, PlanIssue{
				Code: "python_execution_runtime_only", Severity: "warning",
				Message: fmt.Sprintf("execution details for Python asset %s are resolved at runtime", names[0]),
			})
			continue
		}
		result = append(result, PlanIssue{
			Code: "python_execution_runtime_only", Severity: "warning",
			Message: fmt.Sprintf(
				"execution details for %d Python assets are resolved at runtime: %s",
				len(names), strings.Join(names, ", "),
			),
		})
	}
	return result
}

func (p *Planner) executionTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Time{}, err
		}
		return parsed.UTC(), nil
	}
	if p.deps.Now != nil {
		return p.deps.Now().UTC(), nil
	}
	return time.Now().UTC(), nil
}

func (p *Planner) effectiveSensorMode(requested string, scheduled bool) string {
	if p.deps.EffectiveSensorMode != nil {
		return p.deps.EffectiveSensorMode(requested, scheduled)
	}
	return strings.TrimSpace(requested)
}

func applicationError(status int, code, message string) *apperror.Error {
	return &apperror.Error{Status: status, Code: code, Message: message}
}
