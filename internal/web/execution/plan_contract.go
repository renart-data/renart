package execution

import webtypecheck "renart/internal/web/typecheck"

const (
	PlanStatusReady   = "ready"
	PlanStatusWarning = "warning"
	PlanStatusBlocked = "blocked"

	PlanSourceWorkingTree = "working_tree"
	PlanSourceSnapshot    = "snapshot"
	PlanPurposeExecution  = "execution"
	PlanPurposeDeployment = "deployment"

	PlanSelectionNeeded         = "needed"
	PlanSelectionAll            = "all"
	PlanSelectionAsset          = "asset"
	PlanSelectionSelector       = "selector"
	PlanSelectionSelectorNeeded = "selector_needed"

	PlanResourceIsolationResources = "resources"
	PlanResourceIsolationPipeline  = "pipeline"

	PlanPrerequisiteReady   = "ready"
	PlanPrerequisiteBlocked = "blocked"
)

// renart:web-name PipelinePlanSourceRequest
type PlanSourceRequest struct {
	Kind      string `json:"kind,omitempty"`
	VersionID string `json:"version_id,omitempty"`
}

// renart:web-name PipelinePlanSelectionRequest
type PlanSelectionRequest struct {
	Mode      string `json:"mode,omitempty"`
	AssetName string `json:"asset_name,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Selector  string `json:"selector,omitempty"`
}

// PlanRequest contains only behavior-changing plan inputs.
// renart:web
// renart:web-name PipelinePlanRequest
type PlanRequest struct {
	Purpose             string               `json:"purpose,omitempty"`
	Environment         string               `json:"environment,omitempty"`
	StartDate           string               `json:"start_date,omitempty"`
	EndDate             string               `json:"end_date,omitempty"`
	ExecutionTime       string               `json:"execution_time,omitempty"`
	FullRefresh         bool                 `json:"full_refresh,omitempty"`
	Backfill            bool                 `json:"backfill,omitempty"`
	SensorMode          string               `json:"sensor_mode,omitempty"`
	Source              PlanSourceRequest    `json:"source,omitempty"`
	Selection           PlanSelectionRequest `json:"selection,omitempty"`
	IncludeStageContent bool                 `json:"include_stage_content,omitempty"`

	ConfigurationAssetNames []string          `json:"-"`
	VariableOverrides       map[string]any    `json:"-"`
	VariableOverrideSource  string            `json:"-"`
	ProducerDeploymentPins  map[string]string `json:"-"`
	SkipActiveRunCheck      bool              `json:"-"`
	SkipDataStateCheck      bool              `json:"-"`
	Scheduled               bool              `json:"-"`
}

// PlanConfirmRequest carries the exact request used to regenerate a reviewed
// plan; rendered content is never accepted from the client.
// renart:web
// renart:web-name PipelinePlanConfirmRequest
type PlanConfirmRequest struct {
	PlanID               string            `json:"plan_id"`
	Plan                 PlanRequest       `json:"plan"`
	ConfirmedEnvironment string            `json:"confirmed_environment,omitempty"`
	Reviewed             *ReviewedIdentity `json:"reviewed,omitempty"`
}

// renart:web-name PipelinePlanReviewedIdentity
type ReviewedIdentity struct {
	PipelineUUID         string              `json:"pipeline_uuid"`
	Source               RenderSource        `json:"source"`
	Context              PlanContext         `json:"context"`
	Selection            PlanSelection       `json:"selection"`
	SemanticImpactDigest string              `json:"semantic_impact_digest,omitempty"`
	Prerequisites        []Prerequisite      `json:"prerequisites,omitempty"`
	Resources            Resources           `json:"resources"`
	ExecutionContracts   []ExecutionContract `json:"execution_contracts"`
	ExecutionUnits       []PlanExecutionUnit `json:"execution_units"`
}

// renart:web-name PipelinePlanContext
type PlanContext struct {
	Environment           string               `json:"environment,omitempty"`
	SchemaPrefix          string               `json:"schema_prefix,omitempty"`
	StartDate             string               `json:"start_date"`
	EndDate               string               `json:"end_date"`
	ExecutionTime         string               `json:"execution_time"`
	MaxActiveSteps        int                  `json:"max_active_steps"`
	RequestedFullRefresh  bool                 `json:"requested_full_refresh"`
	FullRefresh           bool                 `json:"full_refresh"`
	Backfill              bool                 `json:"backfill"`
	SensorMode            string               `json:"sensor_mode"`
	VariablesDigest       string               `json:"variables_digest"`
	VariableProvenance    []VariableProvenance `json:"variable_provenance"`
	ConfigurationDigest   string               `json:"configuration_digest,omitempty"`
	ConfigurationFidelity string               `json:"configuration_fidelity"`
	Destructive           bool                 `json:"destructive"`
}

// renart:web-name PipelinePlanIssue
type PlanIssue struct {
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	AssetID   string `json:"asset_id,omitempty"`
	AssetName string `json:"asset_name,omitempty"`
}

// renart:web-name PipelinePlanReadiness
type PlanReadiness struct {
	CodeChecks webtypecheck.Report `json:"code_checks"`
	Blockers   []PlanIssue         `json:"blockers"`
	Warnings   []PlanIssue         `json:"warnings"`
	ActiveRun  string              `json:"active_run_id,omitempty"`
}

// renart:web-name PipelinePlanSelection
type PlanSelection struct {
	Mode           string `json:"mode"`
	AssetName      string `json:"asset_name,omitempty"`
	Scope          string `json:"scope,omitempty"`
	Selector       string `json:"selector,omitempty"`
	DataStateToken string `json:"data_state_token,omitempty"`
}

// renart:web-name PipelinePlanRender
type PlanRender struct {
	StartDate   string            `json:"start_date"`
	EndDate     string            `json:"end_date"`
	Status      RenderStatus      `json:"status"`
	FullRefresh bool              `json:"full_refresh"`
	Stages      []RenderStage     `json:"stages"`
	Issues      []RenderIssue     `json:"issues"`
	Redactions  []RenderRedaction `json:"redactions"`
}

// renart:web-name PipelinePlanAsset
type PlanAsset struct {
	ID               string       `json:"id"`
	WorkspaceAssetID string       `json:"workspace_asset_id,omitempty"`
	Name             string       `json:"name"`
	Type             string       `json:"type"`
	Dialect          string       `json:"dialect,omitempty"`
	ConnectionName   string       `json:"connection_name,omitempty"`
	Fingerprint      string       `json:"fingerprint,omitempty"`
	Target           RenderTarget `json:"target"`
	Staleness        string       `json:"staleness,omitempty"`
	InclusionReasons []string     `json:"inclusion_reasons"`
	Renders          []PlanRender `json:"renders"`
}

// renart:web-name PipelinePlanExecutionUnit
type PlanExecutionUnit struct {
	AssetID             string `json:"asset_id"`
	AssetName           string `json:"asset_name"`
	StartDate           string `json:"start_date"`
	EndDate             string `json:"end_date"`
	RenderIndex         int    `json:"render_index"`
	Reason              string `json:"reason"`
	DependencyPositions []int  `json:"dependency_positions"`
}

// renart:web-name PipelinePlanPrerequisite
type Prerequisite struct {
	Status                    string  `json:"status"`
	Reason                    string  `json:"reason"`
	ConsumerAssetID           string  `json:"consumer_asset_id"`
	ConsumerAssetName         string  `json:"consumer_asset_name"`
	URI                       string  `json:"uri"`
	ProducerPipelineID        string  `json:"producer_pipeline_id"`
	ProducerPipelineUUID      string  `json:"producer_pipeline_uuid"`
	ProducerPipelineName      string  `json:"producer_pipeline_name"`
	ProducerAssetID           string  `json:"producer_asset_id"`
	ProducerAssetName         string  `json:"producer_asset_name"`
	ProducerSnapshotVersionID string  `json:"producer_snapshot_version_id,omitempty"`
	ProducerDeploymentOrdinal int64   `json:"producer_deployment_ordinal,omitempty"`
	Environment               string  `json:"environment"`
	RequiredStart             string  `json:"required_start"`
	RequiredEnd               string  `json:"required_end"`
	ExpectedFingerprint       string  `json:"expected_fingerprint"`
	TargetIdentity            string  `json:"target_identity,omitempty"`
	VarsHash                  string  `json:"vars_hash"`
	TargetGeneration          int64   `json:"target_generation,omitempty"`
	WriterRunID               string  `json:"writer_run_id,omitempty"`
	WriterSnapshotVersionID   string  `json:"writer_snapshot_version_id,omitempty"`
	WriterCompletionID        string  `json:"writer_completion_id,omitempty"`
	WriterCompletionOrdinal   int64   `json:"writer_completion_ordinal,omitempty"`
	WriterMaterializedAt      string  `json:"writer_materialized_at,omitempty"`
	CoveredSeconds            float64 `json:"covered_seconds,omitempty"`
	RequiredSeconds           float64 `json:"required_seconds,omitempty"`
}

type ProducerDeployment struct {
	PipelineID        string
	PipelineName      string
	SnapshotVersionID string
	VariableOverrides map[string]any
	ScheduleFound     bool
	ScheduleStatus    string
}

// renart:web-name PipelinePlanResourceClaim
type ResourceClaim struct {
	Kind     string `json:"kind"`
	Identity string `json:"identity"`
}

// renart:web-name PipelinePlanResources
type Resources struct {
	Isolation string          `json:"isolation"`
	Claims    []ResourceClaim `json:"claims"`
}

// renart:web-name PipelinePlanExecutionContract
type ExecutionContract struct {
	AssetID               string    `json:"asset_id"`
	AssetName             string    `json:"asset_name"`
	ConnectionKeys        []string  `json:"connection_keys"`
	MutationResources     Resources `json:"mutation_resources"`
	CoordinationResources Resources `json:"coordination_resources"`
}

// renart:web-name PipelinePlanSummary
type PlanSummary struct {
	Assets                int `json:"assets"`
	ExecutionUnits        int `json:"execution_units"`
	Stages                int `json:"stages"`
	DestructiveOperations int `json:"destructive_operations"`
	Blockers              int `json:"blockers"`
	Warnings              int `json:"warnings"`
}

// Plan is the immutable, secret-free result reviewed before execution.
// renart:web
// renart:web-name PipelinePlan
type Plan struct {
	ID                 string                `json:"id"`
	Status             string                `json:"status"`
	PipelineID         string                `json:"pipeline_id"`
	PipelineUUID       string                `json:"pipeline_uuid"`
	PipelineName       string                `json:"pipeline_name"`
	Source             RenderSource          `json:"source"`
	Context            PlanContext           `json:"context"`
	Readiness          PlanReadiness         `json:"readiness"`
	Selection          PlanSelection         `json:"selection"`
	SemanticImpact     *SemanticImpactReport `json:"semantic_impact,omitempty"`
	Prerequisites      []Prerequisite        `json:"prerequisites"`
	Resources          Resources             `json:"resources"`
	Assets             []PlanAsset           `json:"assets"`
	ExecutionContracts []ExecutionContract   `json:"execution_contracts"`
	ExecutionUnits     []PlanExecutionUnit   `json:"execution_units"`
	Summary            PlanSummary           `json:"summary"`
}
