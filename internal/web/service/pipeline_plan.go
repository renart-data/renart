package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"

	"renart/internal/web/fingerprint"
	"renart/internal/web/identity"
	"renart/internal/web/matlog"
	"renart/internal/web/policy"
	"renart/internal/web/runcontext"
	"renart/internal/web/snapshot"
	"renart/internal/web/staleness"
)

const (
	PipelinePlanStatusReady   = "ready"
	PipelinePlanStatusWarning = "warning"
	PipelinePlanStatusBlocked = "blocked"

	PipelinePlanSourceWorkingTree = "working_tree"
	PipelinePlanSourceSnapshot    = "snapshot"
	PipelinePlanPurposeExecution  = "execution"
	PipelinePlanPurposeDeployment = "deployment"

	PipelinePlanSelectionNeeded         = "needed"
	PipelinePlanSelectionAll            = "all"
	PipelinePlanSelectionAsset          = "asset"
	PipelinePlanSelectionSelector       = "selector"
	PipelinePlanSelectionSelectorNeeded = "selector_needed"

	PipelinePlanResourceIsolationResources = "resources"
	PipelinePlanResourceIsolationPipeline  = "pipeline"
)

type PipelinePlanSourceRequest struct {
	Kind      string `json:"kind,omitempty"`
	VersionID string `json:"version_id,omitempty"`
}

type PipelinePlanSelectionRequest struct {
	Mode      string `json:"mode,omitempty"`
	AssetName string `json:"asset_name,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Selector  string `json:"selector,omitempty"`
}

// PipelinePlanRequest contains only behavior-changing plan inputs. Source is
// resolved to an exact Merkle/version before the response is returned.
type PipelinePlanRequest struct {
	Purpose             string                       `json:"purpose,omitempty"`
	Environment         string                       `json:"environment,omitempty"`
	StartDate           string                       `json:"start_date,omitempty"`
	EndDate             string                       `json:"end_date,omitempty"`
	ExecutionTime       string                       `json:"execution_time,omitempty"`
	FullRefresh         bool                         `json:"full_refresh,omitempty"`
	Backfill            bool                         `json:"backfill,omitempty"`
	SensorMode          string                       `json:"sensor_mode,omitempty"`
	Source              PipelinePlanSourceRequest    `json:"source,omitempty"`
	Selection           PipelinePlanSelectionRequest `json:"selection,omitempty"`
	IncludeStageContent bool                         `json:"include_stage_content,omitempty"`
	// ConfigurationAssetNames is an internal confirmation-only override used
	// to re-evaluate the reviewed selection's configuration after a Needed plan
	// shrinks. It is never decoded from HTTP JSON.
	ConfigurationAssetNames []string `json:"-"`
	// VariableOverrides and their value-free provenance source are internal
	// admission inputs. Public plan JSON cannot set or recover their values.
	VariableOverrides      map[string]any `json:"-"`
	VariableOverrideSource string         `json:"-"`
	// Scheduled admission arbitrates the active run slot transactionally and
	// always selects all assets, so transient active/data-state checks must not
	// turn a due interval into a failed plan before admission.
	SkipActiveRunCheck bool `json:"-"`
	SkipDataStateCheck bool `json:"-"`
	Scheduled          bool `json:"-"`
}

// PipelinePlanConfirmRequest carries the exact read-only request used to
// regenerate a reviewed plan. Confirmation never trusts rendered content from
// the client; it compares PlanID against a fresh server-side result.
type PipelinePlanConfirmRequest struct {
	PlanID               string                        `json:"plan_id"`
	Plan                 PipelinePlanRequest           `json:"plan"`
	ConfirmedEnvironment string                        `json:"confirmed_environment,omitempty"`
	Reviewed             *PipelinePlanReviewedIdentity `json:"reviewed,omitempty"`
}

// PipelinePlanReviewedIdentity is exactly the compact value hashed into a
// plan ID. Confirmation can therefore validate a prior preview without
// accepting rendered statements back from the client.
type PipelinePlanReviewedIdentity struct {
	PipelineUUID       string                          `json:"pipeline_uuid"`
	Source             AssetRenderSource               `json:"source"`
	Context            PipelinePlanContext             `json:"context"`
	Selection          PipelinePlanSelection           `json:"selection"`
	Prerequisites      []PipelinePlanPrerequisite      `json:"prerequisites,omitempty"`
	Resources          PipelinePlanResources           `json:"resources"`
	ExecutionContracts []PipelinePlanExecutionContract `json:"execution_contracts"`
	ExecutionUnits     []PipelinePlanExecutionUnit     `json:"execution_units"`
}

type PipelinePlanContext struct {
	Environment           string                          `json:"environment,omitempty"`
	SchemaPrefix          string                          `json:"schema_prefix,omitempty"`
	StartDate             string                          `json:"start_date"`
	EndDate               string                          `json:"end_date"`
	ExecutionTime         string                          `json:"execution_time"`
	MaxActiveSteps        int                             `json:"max_active_steps"`
	RequestedFullRefresh  bool                            `json:"requested_full_refresh"`
	FullRefresh           bool                            `json:"full_refresh"`
	Backfill              bool                            `json:"backfill"`
	SensorMode            string                          `json:"sensor_mode"`
	VariablesDigest       string                          `json:"variables_digest"`
	VariableProvenance    []AssetRenderVariableProvenance `json:"variable_provenance"`
	ConfigurationDigest   string                          `json:"configuration_digest,omitempty"`
	ConfigurationFidelity string                          `json:"configuration_fidelity"`
	Destructive           bool                            `json:"destructive"`
}

type PipelinePlanIssue struct {
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	AssetID   string `json:"asset_id,omitempty"`
	AssetName string `json:"asset_name,omitempty"`
}

type PipelinePlanReadiness struct {
	CodeChecks TypeCheckReport     `json:"code_checks"`
	Blockers   []PipelinePlanIssue `json:"blockers"`
	Warnings   []PipelinePlanIssue `json:"warnings"`
	ActiveRun  string              `json:"active_run_id,omitempty"`
}

type PipelinePlanSelection struct {
	Mode           string `json:"mode"`
	AssetName      string `json:"asset_name,omitempty"`
	Scope          string `json:"scope,omitempty"`
	Selector       string `json:"selector,omitempty"`
	DataStateToken string `json:"data_state_token,omitempty"`
}

type PipelinePlanRender struct {
	StartDate   string                 `json:"start_date"`
	EndDate     string                 `json:"end_date"`
	Status      AssetRenderStatus      `json:"status"`
	FullRefresh bool                   `json:"full_refresh"`
	Stages      []AssetRenderStage     `json:"stages"`
	Issues      []AssetRenderIssue     `json:"issues"`
	Redactions  []AssetRenderRedaction `json:"redactions"`
}

type PipelinePlanAsset struct {
	ID               string               `json:"id"`
	WorkspaceAssetID string               `json:"workspace_asset_id,omitempty"`
	Name             string               `json:"name"`
	Type             string               `json:"type"`
	Dialect          string               `json:"dialect,omitempty"`
	ConnectionName   string               `json:"connection_name,omitempty"`
	Fingerprint      string               `json:"fingerprint,omitempty"`
	Target           AssetRenderTarget    `json:"target"`
	Staleness        string               `json:"staleness,omitempty"`
	InclusionReasons []string             `json:"inclusion_reasons"`
	Renders          []PipelinePlanRender `json:"renders"`
}

type PipelinePlanExecutionUnit struct {
	AssetID             string `json:"asset_id"`
	AssetName           string `json:"asset_name"`
	StartDate           string `json:"start_date"`
	EndDate             string `json:"end_date"`
	RenderIndex         int    `json:"render_index"`
	Reason              string `json:"reason"`
	DependencyPositions []int  `json:"dependency_positions"`
}

const (
	PipelinePlanPrerequisiteReady   = "ready"
	PipelinePlanPrerequisiteBlocked = "blocked"
)

// PipelinePlanPrerequisite is the immutable, secret-free producer evidence a
// full URI dependency requires. Ready evidence is bound into the reviewed plan
// and revalidated immediately before the consumer task starts.
type PipelinePlanPrerequisite struct {
	Status                  string  `json:"status"`
	Reason                  string  `json:"reason"`
	ConsumerAssetID         string  `json:"consumer_asset_id"`
	ConsumerAssetName       string  `json:"consumer_asset_name"`
	URI                     string  `json:"uri"`
	ProducerPipelineID      string  `json:"producer_pipeline_id"`
	ProducerPipelineUUID    string  `json:"producer_pipeline_uuid"`
	ProducerPipelineName    string  `json:"producer_pipeline_name"`
	ProducerAssetID         string  `json:"producer_asset_id"`
	ProducerAssetName       string  `json:"producer_asset_name"`
	Environment             string  `json:"environment"`
	RequiredStart           string  `json:"required_start"`
	RequiredEnd             string  `json:"required_end"`
	ExpectedFingerprint     string  `json:"expected_fingerprint"`
	TargetIdentity          string  `json:"target_identity,omitempty"`
	VarsHash                string  `json:"vars_hash"`
	TargetGeneration        int64   `json:"target_generation,omitempty"`
	WriterRunID             string  `json:"writer_run_id,omitempty"`
	WriterSnapshotVersionID string  `json:"writer_snapshot_version_id,omitempty"`
	WriterCompletionID      string  `json:"writer_completion_id,omitempty"`
	WriterCompletionOrdinal int64   `json:"writer_completion_ordinal,omitempty"`
	WriterMaterializedAt    string  `json:"writer_materialized_at,omitempty"`
	CoveredSeconds          float64 `json:"covered_seconds,omitempty"`
	RequiredSeconds         float64 `json:"required_seconds,omitempty"`
}

// PipelinePlanResourceClaim is one exclusive, secret-free mutation resource.
// Identity is generated by the execution target resolver; raw database and
// filesystem coordinates never enter the public plan or durable run artifact.
type PipelinePlanResourceClaim struct {
	Kind     string `json:"kind"`
	Identity string `json:"identity"`
}

// PipelinePlanResources describes admission isolation for the selected work.
// Resources allows distinct proven outputs to execute concurrently. Pipeline
// retains the conservative logical-pipeline slot whenever any selected
// operator cannot prove a stable write-resource contract.
type PipelinePlanResources struct {
	Isolation string                      `json:"isolation"`
	Claims    []PipelinePlanResourceClaim `json:"claims"`
}

// PipelinePlanExecutionContract is the reviewed, secret-free runtime policy
// for one selected asset. It is stored once even when the asset has multiple
// execution windows.
type PipelinePlanExecutionContract struct {
	AssetID               string                `json:"asset_id"`
	AssetName             string                `json:"asset_name"`
	ConnectionKeys        []string              `json:"connection_keys"`
	MutationResources     PipelinePlanResources `json:"mutation_resources"`
	CoordinationResources PipelinePlanResources `json:"coordination_resources"`
}

type PipelinePlanSummary struct {
	Assets                int `json:"assets"`
	ExecutionUnits        int `json:"execution_units"`
	Stages                int `json:"stages"`
	DestructiveOperations int `json:"destructive_operations"`
	Blockers              int `json:"blockers"`
	Warnings              int `json:"warnings"`
}

type PipelinePlan struct {
	ID                 string                          `json:"id"`
	Status             string                          `json:"status"`
	PipelineID         string                          `json:"pipeline_id"`
	PipelineUUID       string                          `json:"pipeline_uuid"`
	PipelineName       string                          `json:"pipeline_name"`
	Source             AssetRenderSource               `json:"source"`
	Context            PipelinePlanContext             `json:"context"`
	Readiness          PipelinePlanReadiness           `json:"readiness"`
	Selection          PipelinePlanSelection           `json:"selection"`
	Prerequisites      []PipelinePlanPrerequisite      `json:"prerequisites"`
	Resources          PipelinePlanResources           `json:"resources"`
	Assets             []PipelinePlanAsset             `json:"assets"`
	ExecutionContracts []PipelinePlanExecutionContract `json:"execution_contracts"`
	ExecutionUnits     []PipelinePlanExecutionUnit     `json:"execution_units"`
	Summary            PipelinePlanSummary             `json:"summary"`
}

type PipelinePlanSnapshotStore interface {
	Latest(ctx context.Context, pipelineUUID string) (*snapshot.Snapshot, error)
	ValidateMetadata(ctx context.Context, versionID, pipelineUUID string) (snapshot.Snapshot, error)
	MaterializeForPipelineExecution(ctx context.Context, versionID, pipelineUUID, destDir string) error
}

type PipelinePlanStaleness interface {
	Evaluate(ctx context.Context, selection staleness.Selection, parsed *pipeline.Pipeline) (staleness.Snapshot, error)
}

type PipelinePlanDependencies struct {
	WorkspaceRoot       string
	ConfigPath          string
	Snapshots           PipelinePlanSnapshotStore
	Staleness           PipelinePlanStaleness
	DependencyGraph     WorkspaceDependencyGraphResolver
	Fingerprints        *fingerprint.Engine
	Materializations    *matlog.Store
	ResolvePipelineUUID func(pipelineID string) (string, bool)
	PolicyFor           func(environment string) policy.EnvironmentPolicy
	ActiveRunID         func(ctx context.Context, pipelineID, pipelineUUID string) (string, error)
	ConflictingRunID    func(ctx context.Context, pipelineID, pipelineUUID string, resources PipelinePlanResources) (string, error)
	NewPipelineBuilder  func() *pipeline.Builder
	Now                 func() time.Time
}

type PipelinePlanService struct {
	deps PipelinePlanDependencies
}

func NewPipelinePlanService(deps PipelinePlanDependencies) *PipelinePlanService {
	return &PipelinePlanService{deps: deps}
}

type resolvedPipelinePlanSource struct {
	root        string
	pipelineDir string
	relPath     string
	parsed      *pipeline.Pipeline
	manifest    map[string]string
	state       snapshot.SourceState
	source      AssetRenderSource
	cleanup     func()
}

func (s *PipelinePlanService) Plan(
	ctx context.Context,
	pipelineID string,
	req PipelinePlanRequest,
) (PipelinePlan, *APIError) {
	if s == nil || strings.TrimSpace(s.deps.WorkspaceRoot) == "" || s.deps.Staleness == nil {
		return PipelinePlan{}, &APIError{Status: 500, Code: "planner_unavailable", Message: "pipeline planning is unavailable"}
	}
	pipelineID = strings.TrimSpace(pipelineID)
	if pipelineID == "" {
		return PipelinePlan{}, &APIError{Status: 400, Code: "pipeline_id_required", Message: "pipeline id is required"}
	}
	pipelineUUID, ok := s.resolvePipelineUUID(pipelineID)
	if !ok {
		return PipelinePlan{}, &APIError{Status: 404, Code: "pipeline_not_found", Message: "pipeline not found"}
	}

	normalized, err := runcontext.Normalize(runcontext.Input{
		Start:       req.StartDate,
		End:         req.EndDate,
		FullRefresh: req.FullRefresh,
		Backfill:    req.Backfill,
		SensorMode:  req.SensorMode,
	})
	if err != nil {
		return PipelinePlan{}, &APIError{Status: 400, Code: "invalid_execution_context", Message: err.Error()}
	}
	executionTime, err := s.executionTime(req.ExecutionTime)
	if err != nil {
		return PipelinePlan{}, &APIError{Status: 400, Code: "invalid_execution_time", Message: "execution_time must be an RFC3339 timestamp"}
	}
	cfg, err := loadSelectedConfigReadOnlyFS(afero.NewOsFs(), s.deps.ConfigPath, req.Environment)
	if err != nil {
		return PipelinePlan{}, &APIError{Status: 400, Code: "invalid_environment", Message: "selected environment is not configured"}
	}
	environment := cfg.SelectedEnvironmentName
	envPolicy := policy.EnvironmentPolicy{}
	if s.deps.PolicyFor != nil {
		envPolicy = s.deps.PolicyFor(environment)
	}

	purpose, err := normalizePipelinePlanPurpose(req.Purpose)
	if err != nil {
		return PipelinePlan{}, &APIError{Status: 400, Code: "invalid_plan_purpose", Message: err.Error()}
	}
	if purpose == PipelinePlanPurposeDeployment && strings.TrimSpace(req.Source.Kind) == "" {
		req.Source.Kind = PipelinePlanSourceWorkingTree
	}
	sourceRequest, err := normalizePipelinePlanSource(req.Source, envPolicy)
	if err != nil {
		return PipelinePlan{}, &APIError{Status: 400, Code: "invalid_plan_source", Message: err.Error()}
	}
	if purpose == PipelinePlanPurposeDeployment && sourceRequest.Kind != PipelinePlanSourceWorkingTree {
		return PipelinePlan{}, &APIError{Status: 400, Code: "invalid_plan_source", Message: "deployment review requires the saved working tree"}
	}
	selectionRequest, err := normalizePipelinePlanSelection(req.Selection)
	if err != nil {
		return PipelinePlan{}, &APIError{Status: 400, Code: "invalid_plan_selection", Message: err.Error()}
	}
	if purpose == PipelinePlanPurposeDeployment && selectionRequest.Mode != PipelinePlanSelectionAll {
		return PipelinePlan{}, &APIError{Status: 400, Code: "invalid_plan_selection", Message: "deployment review requires the entire pipeline"}
	}

	base := PipelinePlan{
		Status:       PipelinePlanStatusBlocked,
		PipelineID:   pipelineID,
		PipelineUUID: pipelineUUID,
		Source: AssetRenderSource{
			Kind:      sourceRequest.Kind,
			VersionID: sourceRequest.VersionID,
		},
		Readiness: PipelinePlanReadiness{
			CodeChecks: TypeCheckReport{Assets: []TypeCheckAsset{}},
			Blockers:   []PipelinePlanIssue{},
			Warnings:   []PipelinePlanIssue{},
		},
		Selection: PipelinePlanSelection{
			Mode:      selectionRequest.Mode,
			AssetName: selectionRequest.AssetName,
			Scope:     selectionRequest.Scope,
			Selector:  selectionRequest.Selector,
		},
		Resources: PipelinePlanResources{
			Isolation: PipelinePlanResourceIsolationPipeline,
			Claims:    []PipelinePlanResourceClaim{},
		},
		Assets:             []PipelinePlanAsset{},
		Prerequisites:      []PipelinePlanPrerequisite{},
		ExecutionContracts: []PipelinePlanExecutionContract{},
		ExecutionUnits:     []PipelinePlanExecutionUnit{},
	}
	initialConfigIdentity := selectedConfigurationIdentityWithBindings(
		s.deps.WorkspaceRoot,
		cfg,
		nil,
	)
	base.Context = PipelinePlanContext{
		Environment:           environment,
		ExecutionTime:         executionTime.Format(time.RFC3339Nano),
		MaxActiveSteps:        1,
		RequestedFullRefresh:  req.FullRefresh,
		Backfill:              req.Backfill,
		SensorMode:            effectiveSensorMode(normalized.SensorMode, req.Scheduled),
		ConfigurationDigest:   initialConfigIdentity.Digest,
		ConfigurationFidelity: string(initialConfigIdentity.Fidelity),
	}
	if cfg.SelectedEnvironment != nil {
		base.Context.SchemaPrefix = cfg.SelectedEnvironment.SchemaPrefix
	}

	resolved, deploymentRequired, apiErr := s.resolveSource(
		ctx, pipelineID, pipelineUUID, sourceRequest, req.VariableOverrides,
	)
	if apiErr != nil {
		return PipelinePlan{}, apiErr
	}
	if deploymentRequired {
		base.Readiness.Blockers = append(base.Readiness.Blockers, PipelinePlanIssue{
			Code:     "deployment_required",
			Severity: "error",
			Message:  "this environment only executes deployed snapshots; deploy the pipeline first",
		})
		base.Summary.Blockers = 1
		base.ID = pipelinePlanID(base)
		return base, nil
	}
	defer resolved.cleanup()
	base.Source = resolved.source
	base.PipelineName = resolved.parsed.Name

	timeWindow, err := ResolveExecutionTimeWindow(
		string(resolved.parsed.Schedule),
		normalized.StartString(),
		normalized.EndString(),
		executionTime,
	)
	if err != nil {
		return PipelinePlan{}, &APIError{Status: 400, Code: "invalid_time_window", Message: err.Error()}
	}

	vars := fingerprint.EffectiveVars(resolved.parsed, req.VariableOverrides)
	varsDigest := fingerprint.AllVarsHash(vars)
	base.Context = PipelinePlanContext{
		Environment:          environment,
		StartDate:            timeWindow.StartRFC3339(),
		EndDate:              timeWindow.EndRFC3339(),
		ExecutionTime:        executionTime.Format(time.RFC3339Nano),
		MaxActiveSteps:       effectivePipelineMaxActiveSteps(resolved.parsed),
		RequestedFullRefresh: req.FullRefresh,
		Backfill:             req.Backfill,
		SensorMode:           effectiveSensorMode(normalized.SensorMode, req.Scheduled),
		VariablesDigest:      varsDigest,
		VariableProvenance: assetRenderVariableProvenanceWithOverrides(
			resolved.parsed, req.VariableOverrides, req.VariableOverrideSource,
		),
		ConfigurationDigest:   initialConfigIdentity.Digest,
		ConfigurationFidelity: string(initialConfigIdentity.Fidelity),
		Destructive:           req.Backfill,
	}
	if cfg.SelectedEnvironment != nil {
		base.Context.SchemaPrefix = cfg.SelectedEnvironment.SchemaPrefix
	}

	base.Readiness.CodeChecks = CheckPipelineAt(
		ctx,
		afero.NewOsFs(),
		resolved.parsed,
		resolved.root,
		timeWindow,
		executionTime,
	)
	base.Readiness.CodeChecks.PipelineID = pipelineID
	s.addCodeCheckIssues(&base)
	staleSnapshot := staleness.Snapshot{}
	dataStateAvailable := false
	if purpose == PipelinePlanPurposeExecution && !req.SkipDataStateCheck {
		staleSelection := staleness.Selection{
			PipelineUUID:      pipelineUUID,
			EncodedPipelineID: pipelineID,
			Environment:       environment,
			Start:             &timeWindow.Start,
			End:               &timeWindow.End,
			VarOverrides:      req.VariableOverrides,
		}
		var staleErr error
		staleSnapshot, staleErr = s.deps.Staleness.Evaluate(ctx, staleSelection, resolved.parsed)
		if staleErr != nil {
			base.Readiness.Blockers = append(base.Readiness.Blockers, PipelinePlanIssue{
				Code:     "data_state_unavailable",
				Severity: "error",
				Message:  "current data state could not be evaluated for this source and environment",
			})
		} else {
			dataStateAvailable = true
			base.Selection.DataStateToken = staleSnapshot.DataStateToken
		}
	}

	selected := selectPipelinePlanAssets(resolved.parsed, selectionRequest, staleSnapshot.Assets, dataStateAvailable)
	if selected.err != nil {
		return PipelinePlan{}, &APIError{Status: 400, Code: "invalid_plan_selection", Message: selected.err.Error()}
	}
	s.addCrossPipelinePrerequisites(
		ctx,
		&base,
		resolved.parsed,
		selected,
		cfg,
		resolved.source.Kind,
		purpose,
		timeWindow,
		req.VariableOverrides,
	)
	if req.Backfill && (selectionRequest.Mode != PipelinePlanSelectionAsset || selectionRequest.Scope != "asset") {
		base.Readiness.Blockers = append(base.Readiness.Blockers, PipelinePlanIssue{
			Code:     "backfill_scope_unsupported",
			Severity: "error",
			Message:  "backfill currently requires one explicitly selected asset without dependency expansion",
		})
	}

	renderer := newAssetRenderServiceForSource(
		resolved.root,
		s.deps.WorkspaceRoot,
		s.deps.ConfigPath,
		resolved.source,
	)
	renderer.variableOverrides = req.VariableOverrides
	renderer.variableOverrideSource = req.VariableOverrideSource
	renderer.collectManifest = func(string) (map[string]string, error) {
		return resolved.manifest, nil
	}

	statusByName := make(map[string]staleness.AssetStatus, len(staleSnapshot.Assets))
	for _, status := range staleSnapshot.Assets {
		statusByName[status.AssetName] = status
	}
	selectedAssets := make([]*pipeline.Asset, 0, len(selected.items))
	for _, item := range selected.items {
		if pipelinePlanAssetIsExecutable(item.asset) {
			selectedAssets = append(selectedAssets, item.asset)
		}
	}
	configurationAssets := selectedAssets
	if req.ConfigurationAssetNames != nil {
		assetByName := make(map[string]*pipeline.Asset, len(resolved.parsed.Assets))
		for _, asset := range resolved.parsed.Assets {
			if asset != nil {
				assetByName[asset.Name] = asset
			}
		}
		configurationAssets = make([]*pipeline.Asset, 0, len(req.ConfigurationAssetNames))
		seenConfigurationAssets := make(map[string]struct{}, len(req.ConfigurationAssetNames))
		for _, name := range req.ConfigurationAssetNames {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, exists := seenConfigurationAssets[name]; exists {
				continue
			}
			asset := assetByName[name]
			if asset == nil {
				return PipelinePlan{}, &APIError{Status: 409, Code: "reviewed_asset_missing", Message: "a reviewed asset no longer exists in the selected source"}
			}
			if !pipelinePlanAssetIsExecutable(asset) {
				continue
			}
			seenConfigurationAssets[name] = struct{}{}
			configurationAssets = append(configurationAssets, asset)
		}
	}
	selectedConfiguration := selectedPipelineConfigurationIdentity(
		s.deps.WorkspaceRoot,
		cfg,
		resolved.parsed,
		configurationAssets,
	)
	base.Context.ConfigurationDigest = selectedConfiguration.Digest
	base.Context.ConfigurationFidelity = string(selectedConfiguration.Fidelity)
	if len(selectedAssets) > 0 && (selectedConfiguration.Fidelity != runcontext.IdentityFidelityExact || selectedConfiguration.Digest == "") {
		issue := PipelinePlanIssue{
			Code:     "configuration_identity_unavailable",
			Severity: "error",
			Message:  "the selected execution configuration cannot be bound to a stable, secret-free identity",
		}
		if purpose == PipelinePlanPurposeDeployment {
			issue.Severity = "warning"
			base.Readiness.Warnings = append(base.Readiness.Warnings, issue)
		} else {
			base.Readiness.Blockers = append(base.Readiness.Blockers, issue)
		}
	}
	for _, item := range selected.items {
		asset := item.asset
		status := statusByName[asset.Name]
		planAsset := PipelinePlanAsset{
			ID:               identity.AssetID(pipelineUUID, asset.Name),
			WorkspaceAssetID: planWorkspaceAssetID(resolved.root, asset),
			Name:             asset.Name,
			Type:             string(asset.Type),
			Staleness:        string(status.Status),
			InclusionReasons: append([]string(nil), item.reasons...),
			Renders:          []PipelinePlanRender{},
		}
		if strings.TrimSpace(status.Fingerprint) != "" {
			planAsset.Fingerprint = status.Fingerprint
		}
		if !pipelinePlanAssetIsExecutable(asset) {
			planAsset.Target = AssetRenderTarget{
				Kind:     assetRenderTargetKindNone,
				Fidelity: AssetRenderFidelityExact,
				WriteResource: AssetRenderWriteResource{
					Kind: assetWriteResourceNone, Fidelity: AssetRenderFidelityExact,
				},
			}
			connectionName, _ := assetRenderConnectionName(&directPipelineInfo{
				Pipeline: resolved.parsed, Asset: asset, Config: cfg,
			})
			planAsset.ConnectionName = connectionName
			base.Assets = append(base.Assets, planAsset)
			continue
		}
		if pipelineAssetParseError(asset) != "" {
			base.Assets = append(base.Assets, planAsset)
			continue
		}
		windows := item.windows
		if len(windows) == 0 {
			windows = []ExecutionTimeWindow{timeWindow}
		}
		if req.Backfill && !matlog.BackfillSafe(asset) {
			base.Readiness.Blockers = append(base.Readiness.Blockers, PipelinePlanIssue{
				Code:      "asset_not_backfill_safe",
				Severity:  "error",
				Message:   "asset cannot be safely backfilled as independent execution windows",
				AssetID:   planAsset.ID,
				AssetName: asset.Name,
			})
		}

		assetPath, pathErr := assetPathRelativeToRoot(resolved.root, asset)
		if pathErr != nil || strings.HasPrefix(assetPath, "../") || assetPath == ".." {
			s.addAssetRenderFailure(&base, planAsset.ID, asset.Name, "asset source path could not be resolved")
			base.Assets = append(base.Assets, planAsset)
			continue
		}
		for _, window := range windows {
			result, renderErr := renderer.RenderPath(ctx, assetPath, AssetRenderRequest{
				Environment:   environment,
				StartDate:     window.StartRFC3339(),
				EndDate:       window.EndRFC3339(),
				ExecutionTime: executionTime.Format(time.RFC3339Nano),
				FullRefresh:   req.FullRefresh,
			})
			if errors.Is(renderErr, ErrAssetRenderSourceChanged) {
				return PipelinePlan{}, &APIError{Status: 409, Code: "source_changed", Message: "pipeline source changed while planning; regenerate the plan"}
			}
			if renderErr != nil {
				s.addAssetRenderFailure(&base, planAsset.ID, asset.Name, safePipelinePlanRenderError(renderErr))
				planAsset.Renders = append(planAsset.Renders, PipelinePlanRender{
					StartDate:  window.StartRFC3339(),
					EndDate:    window.EndRFC3339(),
					Status:     AssetRenderStatusError,
					Stages:     []AssetRenderStage{},
					Issues:     []AssetRenderIssue{},
					Redactions: []AssetRenderRedaction{},
				})
				continue
			}
			if planAsset.Dialect == "" {
				planAsset.Dialect = result.Asset.Dialect
				planAsset.ConnectionName = result.Asset.ConnectionName
				if planAsset.Fingerprint == "" {
					planAsset.Fingerprint = result.Asset.Fingerprint
				}
				planAsset.Target = result.Asset.Target
			}
			planRender := PipelinePlanRender{
				StartDate:   window.StartRFC3339(),
				EndDate:     window.EndRFC3339(),
				Status:      result.Status,
				FullRefresh: result.Provenance.Context.FullRefresh,
				Stages:      clonePipelinePlanStages(result.Stages, req.IncludeStageContent),
				Issues:      append([]AssetRenderIssue(nil), result.Issues...),
				Redactions:  append([]AssetRenderRedaction(nil), result.Redactions...),
			}
			renderIndex := len(planAsset.Renders)
			planAsset.Renders = append(planAsset.Renders, planRender)
			base.ExecutionUnits = append(base.ExecutionUnits, PipelinePlanExecutionUnit{
				AssetID:     planAsset.ID,
				AssetName:   asset.Name,
				StartDate:   window.StartRFC3339(),
				EndDate:     window.EndRFC3339(),
				RenderIndex: renderIndex,
				Reason:      item.reasons[0],
			})
			s.addRenderIssues(&base, planAsset.ID, asset.Name, result)
			base.Summary.Stages += len(result.Stages)
			if result.Provenance.Context.FullRefresh {
				base.Context.FullRefresh = true
				base.Summary.DestructiveOperations++
			}
		}
		base.Assets = append(base.Assets, planAsset)
	}
	if err := bindPipelinePlanExecutionDependencies(resolved.parsed, base.ExecutionUnits); err != nil {
		return PipelinePlan{}, &APIError{
			Status:  500,
			Code:    "execution_graph_invalid",
			Message: "selected execution dependencies could not be bound",
		}
	}
	base.ExecutionContracts, err = pipelinePlanExecutionContracts(
		s.deps.WorkspaceRoot,
		cfg,
		resolved.parsed,
		base.Assets,
	)
	if err != nil {
		return PipelinePlan{}, &APIError{
			Status:  500,
			Code:    "execution_contract_invalid",
			Message: "selected execution resources could not be bound",
		}
	}
	base.Resources = aggregatePipelinePlanMutationResources(base.ExecutionContracts)
	if purpose == PipelinePlanPurposeExecution && !req.SkipActiveRunCheck {
		s.addActiveRunIssue(ctx, &base)
	}

	if req.Backfill {
		base.Summary.DestructiveOperations += len(base.ExecutionUnits)
	}
	base.Context.Destructive = base.Summary.DestructiveOperations > 0
	if purpose == PipelinePlanPurposeExecution {
		s.addPolicyIssues(&base, envPolicy, req.Scheduled)
	}

	latestSourceState, stateErr := snapshot.CollectSourceState(resolved.pipelineDir)
	if stateErr != nil {
		return PipelinePlan{}, &APIError{Status: 500, Code: "source_identity_failed", Message: "pipeline source identity could not be verified"}
	}
	if !resolved.state.Equal(latestSourceState) {
		return PipelinePlan{}, &APIError{Status: 409, Code: "source_changed", Message: "pipeline source changed while planning; regenerate the plan"}
	}
	base.Readiness.Blockers = dedupePipelinePlanIssues(base.Readiness.Blockers)
	base.Readiness.Warnings = dedupePipelinePlanIssues(base.Readiness.Warnings)
	base.Readiness.Warnings = aggregatePythonRuntimePlanWarnings(base.Assets, base.Readiness.Warnings)
	base.Summary.Assets = len(base.Assets)
	base.Summary.ExecutionUnits = len(base.ExecutionUnits)
	base.Summary.Blockers = len(base.Readiness.Blockers)
	base.Summary.Warnings = len(base.Readiness.Warnings)
	base.Status = PipelinePlanStatusReady
	if base.Summary.Warnings > 0 {
		base.Status = PipelinePlanStatusWarning
	}
	if base.Summary.Blockers > 0 {
		base.Status = PipelinePlanStatusBlocked
	}
	base.ID = pipelinePlanID(base)
	return base, nil
}

func pipelinePlanAssetIsExecutable(asset *pipeline.Asset) bool {
	return asset != nil && !isSourceAssetType(asset.Type)
}

func effectivePipelineMaxActiveSteps(pl *pipeline.Pipeline) int {
	if pl == nil || pl.MaxActiveSteps == nil || *pl.MaxActiveSteps < 1 {
		return 1
	}
	return *pl.MaxActiveSteps
}

// bindPipelinePlanExecutionDependencies records a conservative, stable unit
// DAG. Multiple windows for one asset are chained. The first selected window
// waits for the final selected window of every selected in-pipeline upstream;
// unselected upstreams remain reviewed data-state preconditions rather than
// runtime nodes.
func bindPipelinePlanExecutionDependencies(pl *pipeline.Pipeline, units []PipelinePlanExecutionUnit) error {
	if len(units) == 0 {
		return nil
	}
	if pl == nil {
		return errors.New("pipeline is required")
	}
	assetByName := make(map[string]*pipeline.Asset, len(pl.Assets))
	positionsByAsset := make(map[string][]int, len(pl.Assets))
	for _, asset := range pl.Assets {
		if asset != nil {
			assetByName[asset.Name] = asset
		}
	}
	for position := range units {
		name := strings.TrimSpace(units[position].AssetName)
		if assetByName[name] == nil {
			return fmt.Errorf("execution unit %d references unknown asset %q", position, name)
		}
		positionsByAsset[name] = append(positionsByAsset[name], position)
	}
	for assetName, positions := range positionsByAsset {
		asset := assetByName[assetName]
		for index, position := range positions {
			dependencies := make([]int, 0, len(asset.Upstreams)+1)
			if index > 0 {
				dependencies = append(dependencies, positions[index-1])
			} else {
				for _, upstream := range asset.Upstreams {
					upstreamPositions := positionsByAsset[strings.TrimSpace(upstream.Value)]
					if len(upstreamPositions) > 0 {
						dependencies = append(dependencies, upstreamPositions[len(upstreamPositions)-1])
					}
				}
			}
			sort.Ints(dependencies)
			dependencies = dedupeSortedInts(dependencies)
			for _, dependency := range dependencies {
				if dependency < 0 || dependency >= position {
					return fmt.Errorf(
						"execution unit %d has non-topological dependency %d",
						position,
						dependency,
					)
				}
			}
			units[position].DependencyPositions = dependencies
		}
	}
	return nil
}

func dedupeSortedInts(values []int) []int {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func (s *PipelinePlanService) resolvePipelineUUID(pipelineID string) (string, bool) {
	if s.deps.ResolvePipelineUUID == nil {
		return "", false
	}
	return s.deps.ResolvePipelineUUID(pipelineID)
}

func (s *PipelinePlanService) executionTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return time.Time{}, err
		}
		return parsed.UTC(), nil
	}
	if s.deps.Now != nil {
		return s.deps.Now().UTC(), nil
	}
	return time.Now().UTC(), nil
}

func normalizePipelinePlanPurpose(raw string) (string, error) {
	purpose := strings.TrimSpace(raw)
	if purpose == "" {
		return PipelinePlanPurposeExecution, nil
	}
	switch purpose {
	case PipelinePlanPurposeExecution, PipelinePlanPurposeDeployment:
		return purpose, nil
	default:
		return "", fmt.Errorf("purpose must be execution or deployment")
	}
}

func normalizePipelinePlanSource(req PipelinePlanSourceRequest, envPolicy policy.EnvironmentPolicy) (PipelinePlanSourceRequest, error) {
	req.Kind = strings.TrimSpace(req.Kind)
	req.VersionID = strings.TrimSpace(req.VersionID)
	if req.Kind == "" && req.VersionID != "" {
		req.Kind = PipelinePlanSourceSnapshot
	}
	if req.Kind == "" {
		if envPolicy.DeployedOnly {
			req.Kind = PipelinePlanSourceSnapshot
		} else {
			req.Kind = PipelinePlanSourceWorkingTree
		}
	}
	switch req.Kind {
	case PipelinePlanSourceWorkingTree:
		if req.VersionID != "" {
			return PipelinePlanSourceRequest{}, fmt.Errorf("working_tree source does not accept version_id")
		}
	case PipelinePlanSourceSnapshot:
	default:
		return PipelinePlanSourceRequest{}, fmt.Errorf("source kind must be working_tree or snapshot")
	}
	return req, nil
}

func normalizePipelinePlanSelection(req PipelinePlanSelectionRequest) (PipelinePlanSelectionRequest, error) {
	req.Mode = strings.TrimSpace(req.Mode)
	req.AssetName = strings.TrimSpace(req.AssetName)
	req.Scope = strings.TrimSpace(req.Scope)
	req.Selector = strings.TrimSpace(req.Selector)
	if req.Mode == "" {
		req.Mode = PipelinePlanSelectionNeeded
	}
	switch req.Mode {
	case PipelinePlanSelectionNeeded, PipelinePlanSelectionAll:
		if req.AssetName != "" || req.Scope != "" || req.Selector != "" {
			return PipelinePlanSelectionRequest{}, fmt.Errorf("asset_name, scope, and selector are not valid for %s selection", req.Mode)
		}
	case PipelinePlanSelectionAsset:
		if req.Selector != "" {
			return PipelinePlanSelectionRequest{}, fmt.Errorf("selector is only valid for selector selection")
		}
		if req.AssetName == "" {
			return PipelinePlanSelectionRequest{}, fmt.Errorf("asset selection requires asset_name")
		}
		if req.Scope == "" {
			req.Scope = "asset"
		}
		switch req.Scope {
		case "asset", "asset_with_upstreams", "asset_with_downstreams", "asset_with_upstreams_and_downstreams":
		default:
			return PipelinePlanSelectionRequest{}, fmt.Errorf("invalid asset selection scope")
		}
	case PipelinePlanSelectionSelector, PipelinePlanSelectionSelectorNeeded:
		if req.AssetName != "" || req.Scope != "" {
			return PipelinePlanSelectionRequest{}, fmt.Errorf("asset_name and scope are not valid for selector selection")
		}
		if req.Selector == "" {
			return PipelinePlanSelectionRequest{}, fmt.Errorf("selector selection requires selector")
		}
		if len(req.Selector) > 4096 {
			return PipelinePlanSelectionRequest{}, fmt.Errorf("selector exceeds the 4096 byte limit")
		}
	default:
		return PipelinePlanSelectionRequest{}, fmt.Errorf("selection mode must be needed, all, asset, selector, or selector_needed")
	}
	return req, nil
}

func (s *PipelinePlanService) resolveSource(
	ctx context.Context,
	pipelineID string,
	pipelineUUID string,
	req PipelinePlanSourceRequest,
	variableOverrides map[string]any,
) (*resolvedPipelinePlanSource, bool, *APIError) {
	relPath, err := DecodeID(pipelineID)
	if err != nil {
		return nil, false, &APIError{Status: 400, Code: "invalid_pipeline_id", Message: "pipeline id is invalid"}
	}
	relPath = filepath.ToSlash(relPath)
	builder := s.deps.NewPipelineBuilder
	if builder == nil {
		builder = func() *pipeline.Builder { return NewRenartPipelineBuilder(afero.NewOsFs()) }
	}

	resolved := &resolvedPipelinePlanSource{relPath: relPath, cleanup: func() {}}
	if req.Kind == PipelinePlanSourceWorkingTree {
		pipelineDir, joinErr := SafeJoin(s.deps.WorkspaceRoot, relPath)
		if joinErr != nil {
			return nil, false, &APIError{Status: 400, Code: "invalid_pipeline_id", Message: "pipeline id is invalid"}
		}
		resolved.root = s.deps.WorkspaceRoot
		resolved.pipelineDir = pipelineDir
		resolved.source = AssetRenderSource{Kind: PipelinePlanSourceWorkingTree, PipelinePath: relPath}
	} else {
		if s.deps.Snapshots == nil {
			return nil, false, &APIError{Status: 500, Code: "snapshot_store_unavailable", Message: "deployment snapshots are unavailable"}
		}
		var deployed snapshot.Snapshot
		if req.VersionID == "" {
			latest, latestErr := s.deps.Snapshots.Latest(ctx, pipelineUUID)
			if latestErr != nil {
				return nil, false, &APIError{Status: 500, Code: "snapshot_lookup_failed", Message: "deployment snapshot could not be loaded"}
			}
			if latest == nil {
				return nil, true, nil
			}
			deployed = *latest
		} else {
			validated, validateErr := s.deps.Snapshots.ValidateMetadata(ctx, req.VersionID, pipelineUUID)
			if validateErr != nil {
				return nil, false, &APIError{Status: 404, Code: "snapshot_not_found", Message: "deployment snapshot was not found for this pipeline"}
			}
			deployed = validated
		}

		tempRoot, tempErr := os.MkdirTemp("", "renart-plan-")
		if tempErr != nil {
			return nil, false, &APIError{Status: 500, Code: "snapshot_materialize_failed", Message: "deployment snapshot could not be prepared"}
		}
		resolved.cleanup = func() { _ = os.RemoveAll(tempRoot) }
		pipelineDir, joinErr := SafeJoin(tempRoot, relPath)
		if joinErr != nil {
			resolved.cleanup()
			return nil, false, &APIError{Status: 400, Code: "invalid_pipeline_id", Message: "pipeline id is invalid"}
		}
		if mkdirErr := os.MkdirAll(pipelineDir, 0o755); mkdirErr != nil {
			resolved.cleanup()
			return nil, false, &APIError{Status: 500, Code: "snapshot_materialize_failed", Message: "deployment snapshot could not be prepared"}
		}
		if materializeErr := s.deps.Snapshots.MaterializeForPipelineExecution(ctx, deployed.VersionID, pipelineUUID, pipelineDir); materializeErr != nil {
			resolved.cleanup()
			return nil, false, &APIError{Status: 500, Code: "snapshot_materialize_failed", Message: "deployment snapshot failed integrity validation"}
		}
		resolved.root = tempRoot
		resolved.pipelineDir = pipelineDir
		resolved.manifest = deployed.Manifest
		resolved.source = AssetRenderSource{
			Kind:              PipelinePlanSourceSnapshot,
			VersionID:         deployed.VersionID,
			DeploymentOrdinal: deployed.Ordinal,
			PipelinePath:      relPath,
			MerkleRoot:        deployed.MerkleRoot,
		}
	}

	state, stateErr := snapshot.CollectSourceState(resolved.pipelineDir)
	if stateErr != nil {
		resolved.cleanup()
		return nil, false, &APIError{Status: 500, Code: "source_identity_failed", Message: "pipeline source identity could not be computed"}
	}
	resolved.state = state
	if resolved.manifest == nil {
		manifest, manifestErr := snapshot.CollectManifestHashes(resolved.pipelineDir)
		if manifestErr != nil || len(manifest) == 0 {
			resolved.cleanup()
			return nil, false, &APIError{Status: 500, Code: "source_identity_failed", Message: "pipeline source identity could not be computed"}
		}
		resolved.manifest = manifest
		resolved.source.MerkleRoot = snapshot.ManifestRoot(manifest)
	}
	pipelineBuilder := builder()
	if overrideErr := addVariableOverrides(pipelineBuilder, variableOverrides); overrideErr != nil {
		resolved.cleanup()
		return nil, false, &APIError{Status: 400, Code: "invalid_variable_overrides", Message: overrideErr.Error()}
	}
	parsed, parseErr := pipelineBuilder.CreatePipelineFromPath(ctx, resolved.pipelineDir, pipeline.WithMutate())
	if parseErr != nil {
		if errors.Is(parseErr, ErrInvalidVariableOverrides) {
			resolved.cleanup()
			return nil, false, &APIError{Status: 400, Code: "invalid_variable_overrides", Message: parseErr.Error()}
		}
		// A malformed asset is an asset-scoped plan blocker, not a reason to
		// erase every valid sibling from the review. The tolerant builder keeps
		// the broken file as a marked placeholder. Pipeline-level YAML errors
		// still fail here because the tolerant builder cannot recover them.
		tolerantBuilder := NewRenartTolerantPipelineBuilder(afero.NewOsFs())
		if overrideErr := addVariableOverrides(tolerantBuilder, variableOverrides); overrideErr != nil {
			resolved.cleanup()
			return nil, false, &APIError{Status: 400, Code: "invalid_variable_overrides", Message: overrideErr.Error()}
		}
		parsed, parseErr = tolerantBuilder.CreatePipelineFromPath(ctx, resolved.pipelineDir, pipeline.WithMutate())
		if parseErr != nil {
			resolved.cleanup()
			if errors.Is(parseErr, ErrInvalidVariableOverrides) {
				return nil, false, &APIError{Status: 400, Code: "invalid_variable_overrides", Message: parseErr.Error()}
			}
			return nil, false, &APIError{Status: 400, Code: "pipeline_invalid", Message: "pipeline source could not be parsed"}
		}
	}
	if parsed == nil {
		resolved.cleanup()
		return nil, false, &APIError{Status: 400, Code: "pipeline_invalid", Message: "pipeline source is empty"}
	}
	if parsed.LegacyID == "" {
		parsed.LegacyID = pipelineUUID
	} else if parsed.LegacyID != pipelineUUID {
		resolved.cleanup()
		return nil, false, &APIError{Status: 409, Code: "pipeline_identity_mismatch", Message: "pipeline source identity does not match the requested pipeline"}
	}
	resolved.parsed = parsed
	resolved.source.PipelinePath = workspaceRelativeRenderPath(resolved.root, parsed.DefinitionFile.Path)
	return resolved, false, nil
}

type selectedPipelinePlanAsset struct {
	asset   *pipeline.Asset
	reasons []string
	windows []ExecutionTimeWindow
}

type selectedPipelinePlanAssets struct {
	items []selectedPipelinePlanAsset
	err   error
}

func selectPipelinePlanAssets(
	parsed *pipeline.Pipeline,
	req PipelinePlanSelectionRequest,
	statuses []staleness.AssetStatus,
	dataStateAvailable bool,
) selectedPipelinePlanAssets {
	if parsed == nil {
		return selectedPipelinePlanAssets{err: fmt.Errorf("pipeline is unavailable")}
	}
	statusByName := make(map[string]staleness.AssetStatus, len(statuses))
	for _, status := range statuses {
		statusByName[status.AssetName] = status
	}

	plans := make([]StaleAssetPlan, 0, len(parsed.Assets))
	reasons := make(map[string][]string, len(parsed.Assets))
	switch req.Mode {
	case PipelinePlanSelectionNeeded:
		if !dataStateAvailable {
			return selectedPipelinePlanAssets{}
		}
		plans = BuildStalePlan(statuses, nil)
		for _, plan := range plans {
			reasons[plan.AssetName] = []string{pipelinePlanStalenessReason(statusByName[plan.AssetName])}
		}
	case PipelinePlanSelectionAll:
		for _, asset := range parsed.Assets {
			plans = append(plans, StaleAssetPlan{AssetName: asset.Name})
			reasons[asset.Name] = []string{"entire_pipeline"}
		}
	case PipelinePlanSelectionAsset:
		assetByName, downstream := pipelinePlanGraph(parsed)
		target := assetByName[req.AssetName]
		if target == nil {
			return selectedPipelinePlanAssets{err: fmt.Errorf("selected asset was not found")}
		}
		include := map[string]struct{}{target.Name: {}}
		reasons[target.Name] = []string{"explicit"}
		if req.Scope == "asset_with_upstreams" || req.Scope == "asset_with_upstreams_and_downstreams" {
			for name := range pipelinePlanUpstreamClosure(target.Name, assetByName) {
				include[name] = struct{}{}
				reasons[name] = append(reasons[name], "required_upstream")
			}
		}
		if req.Scope == "asset_with_downstreams" || req.Scope == "asset_with_upstreams_and_downstreams" {
			for name := range pipelinePlanDownstreamClosure(target.Name, downstream) {
				include[name] = struct{}{}
				reasons[name] = append(reasons[name], "selected_downstream")
			}
		}
		for name := range include {
			plans = append(plans, StaleAssetPlan{AssetName: name})
		}
	case PipelinePlanSelectionSelector, PipelinePlanSelectionSelectorNeeded:
		matched, err := pipeline.ResolveSelectorAssets(req.Selector, parsed)
		if err != nil {
			return selectedPipelinePlanAssets{err: fmt.Errorf("resolve selector: %w", err)}
		}
		matchedNames := make(map[string]struct{}, len(matched))
		for _, asset := range matched {
			if asset != nil {
				matchedNames[asset.Name] = struct{}{}
			}
		}
		if req.Mode == PipelinePlanSelectionSelectorNeeded {
			if !dataStateAvailable {
				return selectedPipelinePlanAssets{}
			}
			for _, plan := range BuildStalePlan(statuses, nil) {
				if _, ok := matchedNames[plan.AssetName]; !ok {
					continue
				}
				plans = append(plans, plan)
				reasons[plan.AssetName] = []string{
					pipelinePlanStalenessReason(statusByName[plan.AssetName]),
					"selector_match",
				}
			}
		} else {
			for _, asset := range matched {
				if asset == nil {
					continue
				}
				plans = append(plans, StaleAssetPlan{AssetName: asset.Name})
				reasons[asset.Name] = []string{"selector_match"}
			}
		}
	}

	ordered, unknown := orderStalePlan(parsed, plans)
	if len(unknown) > 0 {
		return selectedPipelinePlanAssets{err: fmt.Errorf("selection contains unknown assets")}
	}
	items := make([]selectedPipelinePlanAsset, 0, len(ordered))
	for _, step := range ordered {
		items = append(items, selectedPipelinePlanAsset{
			asset:   step.asset,
			reasons: dedupeStrings(reasons[step.asset.Name]),
			windows: step.plan.Windows,
		})
	}
	return selectedPipelinePlanAssets{items: items}
}

func pipelinePlanGraph(parsed *pipeline.Pipeline) (map[string]*pipeline.Asset, map[string][]string) {
	assetByName := make(map[string]*pipeline.Asset, len(parsed.Assets))
	downstream := make(map[string][]string)
	for _, asset := range parsed.Assets {
		assetByName[asset.Name] = asset
	}
	for _, asset := range parsed.Assets {
		for _, upstream := range asset.Upstreams {
			name := strings.TrimSpace(upstream.Value)
			if assetByName[name] != nil {
				downstream[name] = append(downstream[name], asset.Name)
			}
		}
	}
	return assetByName, downstream
}

func pipelinePlanUpstreamClosure(name string, assets map[string]*pipeline.Asset) map[string]struct{} {
	result := make(map[string]struct{})
	queue := []string{name}
	for len(queue) > 0 {
		current := assets[queue[0]]
		queue = queue[1:]
		if current == nil {
			continue
		}
		for _, upstream := range current.Upstreams {
			upstreamName := strings.TrimSpace(upstream.Value)
			if upstreamName == name || assets[upstreamName] == nil {
				continue
			}
			if _, exists := result[upstreamName]; exists {
				continue
			}
			result[upstreamName] = struct{}{}
			queue = append(queue, upstreamName)
		}
	}
	return result
}

func pipelinePlanDownstreamClosure(name string, downstream map[string][]string) map[string]struct{} {
	result := make(map[string]struct{})
	queue := append([]string(nil), downstream[name]...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current == name {
			continue
		}
		if _, exists := result[current]; exists {
			continue
		}
		result[current] = struct{}{}
		queue = append(queue, downstream[current]...)
	}
	return result
}

func pipelinePlanStalenessReason(status staleness.AssetStatus) string {
	switch status.Status {
	case staleness.StatusStaleEdited:
		return "stale_edited"
	case staleness.StatusStaleDeployment:
		return "stale_deployment"
	case staleness.StatusStaleUpstream:
		return "stale_upstream"
	case staleness.StatusPartial:
		return "uncovered_interval"
	case staleness.StatusNeverBuilt:
		return "never_built"
	case staleness.StatusMissing:
		return "missing_output"
	case staleness.StatusVolatile:
		return "volatile_sensor"
	default:
		return string(status.Status)
	}
}

func planWorkspaceAssetID(root string, asset *pipeline.Asset) string {
	path, err := assetPathRelativeToRoot(root, asset)
	if err != nil || path == ".." || strings.HasPrefix(path, "../") {
		return ""
	}
	return EncodeID(filepath.ToSlash(path))
}

func clonePipelinePlanStages(stages []AssetRenderStage, includeContent bool) []AssetRenderStage {
	cloned := append([]AssetRenderStage(nil), stages...)
	if includeContent {
		return cloned
	}
	for index := range cloned {
		cloned[index].Content = ""
	}
	return cloned
}

func safePipelinePlanRenderError(err error) string {
	var boundary *assetRenderBoundaryError
	if errors.As(err, &boundary) && strings.TrimSpace(boundary.message) != "" {
		return boundary.message
	}
	return "asset could not be rendered for this execution context"
}

func (s *PipelinePlanService) addCodeCheckIssues(plan *PipelinePlan) {
	for _, asset := range plan.Readiness.CodeChecks.Assets {
		assetID := identity.AssetID(plan.PipelineUUID, asset.Name)
		for _, finding := range asset.Findings {
			issue := PipelinePlanIssue{
				Code:      "code_check_" + finding.Severity,
				Severity:  finding.Severity,
				Message:   finding.Message,
				AssetID:   assetID,
				AssetName: asset.Name,
			}
			if finding.Severity == typeCheckSeverityError {
				plan.Readiness.Blockers = append(plan.Readiness.Blockers, issue)
			} else {
				plan.Readiness.Warnings = append(plan.Readiness.Warnings, issue)
			}
		}
	}
}

func (s *PipelinePlanService) addPolicyIssues(plan *PipelinePlan, envPolicy policy.EnvironmentPolicy, scheduled bool) {
	if envPolicy.Protected && !scheduled {
		plan.Readiness.Blockers = append(plan.Readiness.Blockers, PipelinePlanIssue{
			Code:     "interactive_execution_protected",
			Severity: "error",
			Message:  "interactive execution is disabled for this protected environment",
		})
	}
	if envPolicy.DeployedOnly && plan.Source.Kind != PipelinePlanSourceSnapshot {
		plan.Readiness.Blockers = append(plan.Readiness.Blockers, PipelinePlanIssue{
			Code:     "deployed_source_required",
			Severity: "error",
			Message:  "this environment only executes deployed snapshots",
		})
	}
	if envPolicy.ConfirmDestructive && plan.Context.Destructive {
		plan.Readiness.Warnings = append(plan.Readiness.Warnings, PipelinePlanIssue{
			Code:     "destructive_confirmation_required",
			Severity: "warning",
			Message:  "running this destructive plan requires typing the environment name",
		})
	}
}

func (s *PipelinePlanService) addAssetRenderFailure(plan *PipelinePlan, assetID, assetName, message string) {
	plan.Readiness.Blockers = append(plan.Readiness.Blockers, PipelinePlanIssue{
		Code:      "asset_render_failed",
		Severity:  "error",
		Message:   message,
		AssetID:   assetID,
		AssetName: assetName,
	})
}

func (s *PipelinePlanService) addRenderIssues(plan *PipelinePlan, assetID, assetName string, result AssetRenderResult) {
	if result.Status == AssetRenderStatusUnsupported || result.Status == AssetRenderStatusError {
		plan.Readiness.Blockers = append(plan.Readiness.Blockers, PipelinePlanIssue{
			Code:      "asset_render_" + string(result.Status),
			Severity:  "error",
			Message:   "asset execution could not be rendered completely",
			AssetID:   assetID,
			AssetName: assetName,
		})
	} else if message, warn := pipelinePlanPartialRenderWarning(result); result.Status == AssetRenderStatusPartial && warn {
		plan.Readiness.Warnings = append(plan.Readiness.Warnings, PipelinePlanIssue{
			Code:      "asset_render_partial",
			Severity:  "warning",
			Message:   message,
			AssetID:   assetID,
			AssetName: assetName,
		})
	}
	for _, issue := range result.Issues {
		planIssue := PipelinePlanIssue{
			Code:      issue.Code,
			Severity:  issue.Severity,
			Message:   issue.Message,
			AssetID:   assetID,
			AssetName: assetName,
		}
		if issue.Severity == "error" {
			plan.Readiness.Blockers = append(plan.Readiness.Blockers, planIssue)
		} else {
			plan.Readiness.Warnings = append(plan.Readiness.Warnings, planIssue)
		}
	}
	for _, stage := range result.Stages {
		if stage.Status == AssetRenderStageStatusOK {
			continue
		}
		issue := PipelinePlanIssue{
			Code:      "stage_render_" + string(stage.Status),
			Severity:  "error",
			Message:   stage.Message,
			AssetID:   assetID,
			AssetName: assetName,
		}
		if strings.TrimSpace(issue.Message) == "" {
			issue.Message = "execution stage could not be rendered"
		}
		if stage.Kind == "check" && stage.CheckBlocking != nil && !*stage.CheckBlocking {
			issue.Severity = "warning"
			plan.Readiness.Warnings = append(plan.Readiness.Warnings, issue)
		} else {
			plan.Readiness.Blockers = append(plan.Readiness.Blockers, issue)
		}
	}
}

func pipelinePlanPartialRenderWarning(result AssetRenderResult) (string, bool) {
	for _, stage := range result.Stages {
		if stage.Status == AssetRenderStageStatusError {
			return "one or more execution stages could not be rendered", true
		}
		if stage.Status == AssetRenderStageStatusUnsupported || stage.Fidelity == AssetRenderFidelityUnsupported {
			return "one or more execution stages cannot be rendered statically", true
		}
	}
	for _, stage := range result.Stages {
		if stage.Fidelity == AssetRenderFidelityRuntimeOnly {
			return "some execution details are only available at runtime", true
		}
	}
	if result.Asset.Target.Fidelity == AssetRenderFidelityRuntimeOnly {
		return "the physical output target is only available at runtime", true
	}
	return "", false
}

func pipelinePlanID(plan PipelinePlan) string {
	return PipelinePlanReviewedIdentityID(PipelinePlanReviewedIdentity{
		PipelineUUID:       plan.PipelineUUID,
		Source:             plan.Source,
		Context:            plan.Context,
		Selection:          plan.Selection,
		Prerequisites:      plan.Prerequisites,
		Resources:          plan.Resources,
		ExecutionContracts: plan.ExecutionContracts,
		ExecutionUnits:     plan.ExecutionUnits,
	})
}

func PipelinePlanReviewedIdentityID(identity PipelinePlanReviewedIdentity) string {
	encoded, _ := json.Marshal(identity)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func PipelinePlanReviewedIdentityFromPlan(plan PipelinePlan) PipelinePlanReviewedIdentity {
	return PipelinePlanReviewedIdentity{
		PipelineUUID: plan.PipelineUUID, Source: plan.Source, Context: plan.Context,
		Selection: plan.Selection, Prerequisites: append([]PipelinePlanPrerequisite(nil), plan.Prerequisites...), Resources: plan.Resources,
		ExecutionContracts: append([]PipelinePlanExecutionContract(nil), plan.ExecutionContracts...),
		ExecutionUnits:     append([]PipelinePlanExecutionUnit(nil), plan.ExecutionUnits...),
	}
}

func (s *PipelinePlanService) addActiveRunIssue(ctx context.Context, plan *PipelinePlan) {
	if s == nil || plan == nil {
		return
	}
	var (
		activeRunID string
		err         error
	)
	if s.deps.ConflictingRunID != nil {
		activeRunID, err = s.deps.ConflictingRunID(
			ctx, plan.PipelineID, plan.PipelineUUID, plan.Resources,
		)
	} else if s.deps.ActiveRunID != nil {
		activeRunID, err = s.deps.ActiveRunID(ctx, plan.PipelineID, plan.PipelineUUID)
	} else {
		return
	}
	if err != nil {
		plan.Readiness.Warnings = append(plan.Readiness.Warnings, PipelinePlanIssue{
			Code:     "active_run_check_failed",
			Severity: "warning",
			Message:  "Renart could not determine whether the selected write resources are already active",
		})
		return
	}
	if strings.TrimSpace(activeRunID) == "" {
		return
	}
	plan.Readiness.ActiveRun = activeRunID
	plan.Readiness.Blockers = append(plan.Readiness.Blockers, PipelinePlanIssue{
		Code:     "pipeline_already_running",
		Severity: "error",
		Message:  "another queued or running execution owns one of the selected write resources",
	})
}

func dedupePipelinePlanIssues(issues []PipelinePlanIssue) []PipelinePlanIssue {
	seen := make(map[string]struct{}, len(issues))
	result := make([]PipelinePlanIssue, 0, len(issues))
	for _, issue := range issues {
		key := issue.Code + "\x00" + issue.AssetID + "\x00" + issue.Message
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, issue)
	}
	return result
}

func aggregatePythonRuntimePlanWarnings(
	assets []PipelinePlanAsset,
	issues []PipelinePlanIssue,
) []PipelinePlanIssue {
	pythonAssets := make(map[string]string)
	for _, asset := range assets {
		if strings.Contains(strings.ToLower(asset.Type), "python") {
			pythonAssets[asset.ID] = asset.Name
		}
	}

	names := make([]string, 0)
	for _, issue := range issues {
		if issue.Code != "asset_render_partial" ||
			!strings.Contains(strings.ToLower(issue.Message), "runtime") {
			continue
		}
		if name, ok := pythonAssets[issue.AssetID]; ok {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return issues
	}

	result := make([]PipelinePlanIssue, 0, len(issues)-len(names)+1)
	added := false
	for _, issue := range issues {
		_, isPython := pythonAssets[issue.AssetID]
		isRuntimeWarning := issue.Code == "asset_render_partial" &&
			strings.Contains(strings.ToLower(issue.Message), "runtime")
		if !isPython || !isRuntimeWarning {
			result = append(result, issue)
			continue
		}
		if added {
			continue
		}
		added = true
		if len(names) == 1 {
			result = append(result, PipelinePlanIssue{
				Code:     "python_execution_runtime_only",
				Severity: "warning",
				Message:  fmt.Sprintf("execution details for Python asset %s are resolved at runtime", names[0]),
			})
			continue
		}
		result = append(result, PipelinePlanIssue{
			Code:     "python_execution_runtime_only",
			Severity: "warning",
			Message: fmt.Sprintf(
				"execution details for %d Python assets are resolved at runtime: %s",
				len(names),
				strings.Join(names, ", "),
			),
		})
	}
	return result
}

func dedupeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
