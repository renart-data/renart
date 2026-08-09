package scheduler

import "time"

const (
	ExecutionTargetSnapshotVersionV1 = 1
	ExecutionTargetSnapshotVersionV2 = 2
	ExecutionTargetSnapshotVersionV3 = 3
	ExecutionTargetSnapshotVersionV4 = 4
)

type RunStatus string

const (
	RunStatusQueued    RunStatus = "queued"
	RunStatusRunning   RunStatus = "running"
	RunStatusSuccess   RunStatus = "success"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCancelled RunStatus = "cancelled"
)

type RunTrigger string

const (
	RunTriggerSchedule RunTrigger = "schedule"
	RunTriggerManual   RunTrigger = "manual"
	RunTriggerAPI      RunTrigger = "api"
	RunTriggerCLI      RunTrigger = "cli"
)

// RunSource identifies the source tree a manual run executes. Scheduled runs
// receive their source from the persisted environment schedule instead.
type RunSource string

const (
	RunSourceWorkingTree RunSource = "working_tree"
	RunSourceSnapshot    RunSource = "snapshot"
)

type PipelineSchedule struct {
	PipelineID   string     `json:"pipeline_id"`
	PipelineUUID string     `json:"pipeline_uuid,omitempty"`
	PipelineName string     `json:"pipeline_name"`
	PipelinePath string     `json:"pipeline_path"`
	Schedule     string     `json:"schedule"`
	Timezone     string     `json:"timezone"`
	Catchup      bool       `json:"catchup"`
	Enabled      bool       `json:"enabled"`
	NextRunAt    *time.Time `json:"next_run_at,omitempty"`
}

type CatchupPolicy string

const (
	CatchupSkip     CatchupPolicy = "skip"
	CatchupRunOnce  CatchupPolicy = "run_once"
	CatchupBackfill CatchupPolicy = "backfill"
)

type ScheduleStatus string

const (
	ScheduleStatusActive   ScheduleStatus = "active"
	ScheduleStatusPaused   ScheduleStatus = "paused"
	ScheduleStatusArchived ScheduleStatus = "archived"
	// ScheduleStatusDelegated is reserved for cloud-executed schedules.
	ScheduleStatusDelegated ScheduleStatus = "delegated"
)

// ScheduleOccurrenceStatus describes the durable lifecycle of one actual
// due/catch-up interval. It is independent from River's temporary job state.
type ScheduleOccurrenceStatus string

const (
	ScheduleOccurrencePending              ScheduleOccurrenceStatus = "pending"
	ScheduleOccurrenceWaitingPrerequisites ScheduleOccurrenceStatus = "waiting_prerequisites"
	ScheduleOccurrenceAdmitting            ScheduleOccurrenceStatus = "admitting"
	ScheduleOccurrenceActive               ScheduleOccurrenceStatus = "active"
	ScheduleOccurrenceSuccess              ScheduleOccurrenceStatus = "success"
	ScheduleOccurrenceFailed               ScheduleOccurrenceStatus = "failed"
	ScheduleOccurrenceCancelled            ScheduleOccurrenceStatus = "cancelled"
)

// ScheduleOccurrence is the durable identity shared by duplicate signals and
// all retry attempts for one normalized half-open schedule interval.
type ScheduleOccurrence struct {
	Key                  string                   `json:"key"`
	PipelineUUID         string                   `json:"pipeline_uuid"`
	Environment          string                   `json:"environment"`
	IntervalStart        time.Time                `json:"interval_start"`
	IntervalEnd          time.Time                `json:"interval_end"`
	Status               ScheduleOccurrenceStatus `json:"status"`
	CurrentRunID         string                   `json:"current_run_id,omitempty"`
	AttemptCount         int                      `json:"attempt_count"`
	PrerequisitePlan     *PipelineRunPlan         `json:"-"`
	PrerequisiteDeadline *time.Time               `json:"prerequisite_deadline,omitempty"`
	PrerequisiteReason   string                   `json:"prerequisite_reason,omitempty"`
	CreatedAt            time.Time                `json:"created_at"`
	UpdatedAt            time.Time                `json:"updated_at"`
}

// DeferredScheduleOccurrence is the small public projection shown on a
// schedule row while a due interval is waiting for planning or the run slot.
type DeferredScheduleOccurrence struct {
	IntervalStart        time.Time                `json:"interval_start"`
	IntervalEnd          time.Time                `json:"interval_end"`
	AttemptCount         int                      `json:"attempt_count"`
	Status               ScheduleOccurrenceStatus `json:"status"`
	PrerequisiteDeadline *time.Time               `json:"prerequisite_deadline,omitempty"`
	PrerequisiteReason   string                   `json:"prerequisite_reason,omitempty"`
}

const (
	// ArchivedReasonMissing marks reconciler tombstones (pipeline file gone,
	// e.g. branch switch); these auto-restore when the file reappears.
	ArchivedReasonMissing = "missing"
	// ArchivedReasonUser marks explicit deletions; never auto-restored.
	ArchivedReasonUser = "user"
	// ArchivedReasonDeclarationMissing marks a declaration-managed schedule
	// removed from .renart/schedules.yml. Re-adding the same stable key restores
	// the row without losing its local deployment pin or history.
	ArchivedReasonDeclarationMissing = "declaration_missing"
)

// EnvSchedule is one (pipeline, environment) schedule row — the unit of
// schedule identity.
type EnvSchedule struct {
	PipelineUUID      string `json:"pipeline_uuid"`
	Environment       string `json:"environment"`
	SnapshotVersionID string `json:"snapshot_version_id,omitempty"`
	SnapshotOrdinal   int64  `json:"snapshot_ordinal,omitempty"`
	Cron              string `json:"cron"`
	Timezone          string `json:"timezone"`
	// Vars is private execution context. Public schedule DTOs expose only
	// VariableNames so ordinary values never enter API responses or SSE state.
	Vars                 map[string]any    `json:"-"`
	SecretRefs           map[string]string `json:"-"`
	VariableNames        []string          `json:"variable_names,omitempty"`
	SecretReferenceNames []string          `json:"secret_reference_names,omitempty"`
	DeclarationManaged   bool              `json:"declaration_managed"`
	CatchupPolicy        CatchupPolicy     `json:"catchup_policy"`
	Status               ScheduleStatus    `json:"status"`
	ArchivedReason       string            `json:"archived_reason,omitempty"`
	NextRunAt            *time.Time        `json:"next_run_at,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
	UpdatedAt            time.Time         `json:"updated_at"`

	// Resolved presentation fields (not persisted).
	PipelineID         string                      `json:"pipeline_id,omitempty"` // path-encoded API ID
	PipelineName       string                      `json:"pipeline_name,omitempty"`
	LastRun            *PipelineRun                `json:"last_run,omitempty"`
	DeferredOccurrence *DeferredScheduleOccurrence `json:"deferred_occurrence,omitempty"`
}

// PipelineRef resolves a stable pipeline UUID to its current workspace
// incarnation.
type PipelineRef struct {
	EncodedID string
	Name      string
}

// ScheduledRunPlanRequest is the exact, private context resolved by a due or
// catch-up signal. Variable values never enter public run or plan DTOs.
type ScheduledRunPlanRequest struct {
	PipelineID                string
	PipelineUUID              string
	Environment               string
	SnapshotVersionID         string
	Start                     time.Time
	End                       time.Time
	ExecutionTime             time.Time
	VariableOverrides         map[string]any
	FrozenProducerDeployments map[string]string
}

type ScheduledRunPlanResult struct {
	Plan                 PipelineRunPlan
	WaitForPrerequisites bool
}

// UpsertEnvScheduleRequest creates or updates a per-environment schedule.
type UpsertEnvScheduleRequest struct {
	// Environment comes exclusively from the URL path at the HTTP boundary.
	// Keeping it out of JSON prevents a second, conflicting schedule identity.
	Environment       string            `json:"-"`
	Cron              string            `json:"cron"`
	Timezone          string            `json:"timezone"`
	Vars              map[string]any    `json:"vars,omitempty"`
	SecretRefs        map[string]string `json:"secret_refs,omitempty"`
	CatchupPolicy     CatchupPolicy     `json:"catchup_policy,omitempty"`
	SnapshotVersionID string            `json:"snapshot_version_id,omitempty"`
	// DeployNow deploys the working tree and pins the schedule to the new
	// snapshot when none exists yet.
	DeployNow bool `json:"deploy_now,omitempty"`
	// PreserveSnapshot resolves the current pin on the server while updating an
	// existing declaration. It avoids round-tripping a potentially stale local
	// deployment pin through the public API.
	PreserveSnapshot bool `json:"preserve_snapshot,omitempty"`
	// PreserveVariables keeps private literal values and secret references for
	// an existing declaration. Public schedule responses intentionally expose
	// names only, so editors cannot safely round-trip these values.
	PreserveVariables bool `json:"preserve_variables,omitempty"`
	Paused            bool `json:"paused,omitempty"`
}

type EnvSchedulePinSelection struct {
	Environment string `json:"environment"`
	// ExpectedSnapshotVersionID is a pointer so the promotion API can
	// distinguish a missing concurrency token from an explicit empty token.
	// Empty is the valid current pin for a paused schedule that has never been
	// deployed.
	ExpectedSnapshotVersionID *string `json:"expected_snapshot_version_id"`
}

type PromoteEnvSchedulesRequest struct {
	SnapshotVersionID string                    `json:"snapshot_version_id"`
	Schedules         []EnvSchedulePinSelection `json:"schedules"`
}

type UpdateScheduleRequest struct {
	Enabled  bool   `json:"enabled"`
	Schedule string `json:"schedule"`
	Timezone string `json:"timezone"`
	Catchup  bool   `json:"catchup"`
}

type TriggerRequest struct {
	Environment string `json:"environment"`
	Start       string `json:"start,omitempty"`
	End         string `json:"end,omitempty"`
	// Source is normalized by the admission layer. Empty remains a temporary
	// internal compatibility value and is treated as working_tree; callers that
	// request a snapshot must always provide its exact immutable version ID.
	Source            RunSource `json:"source,omitempty"`
	SnapshotVersionID string    `json:"snapshot_version_id,omitempty"`
	// LegacyTrigger accepts the former client hint for rolling compatibility.
	// It never controls persisted origin; only "manual" is accepted at HTTP.
	LegacyTrigger        string `json:"trigger,omitempty"`
	FullRefresh          bool   `json:"full_refresh,omitempty"`
	Backfill             bool   `json:"backfill,omitempty"`
	ConfirmedEnvironment string `json:"confirmed_environment,omitempty"`
	SensorMode           string `json:"sensor_mode,omitempty"`
	// Expected identities and the preview execution time are server-owned plan
	// confirmation evidence. The ordinary trigger JSON endpoint cannot set them.
	ExpectedSourceMerkle        string `json:"-"`
	ExpectedConfigurationDigest string `json:"-"`
	ExecutionTime               string `json:"-"`
	// VariableOverrides is server-owned schedule/run context. The ordinary
	// trigger JSON endpoint cannot supply it.
	VariableOverrides  map[string]any    `json:"-"`
	VariableReferences map[string]string `json:"-"`
	// ConfirmedPlan is the server-regenerated, redacted plan admitted with this
	// run. It is never accepted from the ordinary trigger JSON endpoint.
	ConfirmedPlan *PipelineRunPlan `json:"-"`
}

// PipelineRunReexecution describes the truthful action available from run
// details. Exact means Renart still retains and can resolve the original
// private RunSpec plus its immutable reviewed plan. CurrentSettings is the
// explicit fallback for legacy, inline, blocked, or drifted runs.
type PipelineRunReexecution struct {
	Mode           PipelineRunReexecutionMode `json:"mode"`
	Reason         string                     `json:"reason,omitempty"`
	Selection      string                     `json:"selection,omitempty"`
	ExecutionUnits int                        `json:"execution_units,omitempty"`
}

type PipelineRunReexecutionMode string

const (
	PipelineRunReexecutionExact           PipelineRunReexecutionMode = "exact"
	PipelineRunReexecutionCurrentSettings PipelineRunReexecutionMode = "current_settings"
)

// RunReexecutionValidationRequest is private server-to-server validation
// input. Values such as variable overrides and authorization are deliberately
// never serialized into run-detail responses or SSE events.
type RunReexecutionValidationRequest struct {
	OriginalRunID               string
	PipelineID                  string
	PipelineUUID                string
	Environment                 string
	Source                      RunSource
	SnapshotVersionID           string
	ExpectedSourceMerkle        string
	ExpectedConfigurationDigest string
	VariableOverrides           map[string]any
	ConfigurationAssetNames     []string
	FullRefresh                 bool
	Backfill                    bool
	ConfirmedEnvironment        string
}

// InlineRunAdmission is the server-normalized contract for a synchronous,
// streaming full-pipeline execution. The execution service resolves policy,
// defaults, and the exact window before admission; the scheduler service owns
// durable provenance and the pipeline-global run slot without involving River.
type InlineRunAdmission struct {
	PipelineID           string
	PipelineUUID         string
	PipelineName         string
	Environment          string
	Origin               RunTrigger
	Source               RunSource
	SnapshotVersionID    string
	Start                time.Time
	End                  time.Time
	ExecutionTime        time.Time
	VariableOverrides    map[string]any
	FullRefresh          bool
	Backfill             bool
	ConfirmedEnvironment string
	SensorMode           string
	Selection            RunSelection
}

// RunSelectionMode identifies the immutable work selection retained by a
// RunSpec. Full-pipeline runs use all; one-click asset/scoped runs use asset;
// Build-needed runs use needed with one unit per exact asset/window.
type RunSelectionMode string

const (
	RunSelectionAll    RunSelectionMode = "all"
	RunSelectionAsset  RunSelectionMode = "asset"
	RunSelectionNeeded RunSelectionMode = "needed"
)

// RunSelection is server-normalized provenance for an inline execution. Scope
// and AnchorAssetID are populated only for asset selections. Units are ordered
// exactly as physical execution will occur.
type RunSelection struct {
	Mode          RunSelectionMode
	Scope         string
	AnchorAssetID string
	Units         []RunSelectionUnit
}

// RunSelectionUnit is one immutable asset/window execution decision. AssetPath
// is the canonical workspace-relative path used by direct working-tree runs;
// Start and End are optional as a pair for future non-windowed operations.
type RunSelectionUnit struct {
	AssetID   string
	AssetName string
	AssetPath string
	Start     *time.Time
	End       *time.Time
	Reason    string
}

type PipelineRun struct {
	ID         string `json:"id"`
	PipelineID string `json:"pipeline_id"`
	// RiverJobID links this application-level run to its internal queue job.
	// It is deliberately not exposed through the API.
	RiverJobID *int64 `json:"-"`
	// PipelineUUID is stable admission/event identity for scheduled and inline
	// runs. It is retained privately in the RunSpec and durable UUID slot rather
	// than duplicated onto the public run row.
	PipelineUUID string     `json:"pipeline_uuid,omitempty"`
	Pipeline     string     `json:"pipeline"`
	Environment  string     `json:"environment"`
	Trigger      RunTrigger `json:"trigger"`
	Status       RunStatus  `json:"status"`
	WinStart     *time.Time `json:"win_start,omitempty"`
	WinEnd       *time.Time `json:"win_end,omitempty"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	Error        string     `json:"error,omitempty"`
	LogRef       string     `json:"log_ref,omitempty"`
	// Cancellable is derived from the linked River job when loading run
	// details. CancellationRequestedAt remains visible while a running worker
	// cooperatively unwinds after a user abort request.
	Cancellable             bool       `json:"cancellable,omitempty"`
	CancellationRequestedAt *time.Time `json:"cancellation_requested_at,omitempty"`
	// SnapshotVersionID records the deployed snapshot the run executed;
	// empty for working-tree builds.
	SnapshotVersionID string `json:"snapshot_version_id,omitempty"`
	// SnapshotOrdinal is resolved presentation metadata for the immutable
	// version above. The UUID remains the persisted execution contract.
	SnapshotOrdinal int64 `json:"snapshot_ordinal,omitempty"`
	// FullRefresh, Backfill, and SensorMode hold the normalized admission request
	// until ExecutionContextResolved becomes true. Once resolved, they are the
	// effective modes persisted before the first asset starts. They remain an
	// internal recovery contract until the API has a nested requested/effective
	// execution-context DTO.
	FullRefresh bool   `json:"-"`
	Backfill    bool   `json:"-"`
	SensorMode  string `json:"-"`
	// Plan confirmation evidence is persisted only in the private RunSpec.
	ExecutionTime               *time.Time     `json:"-"`
	ExpectedSourceMerkle        string         `json:"-"`
	ExpectedConfigurationDigest string         `json:"-"`
	VariableOverrides           map[string]any `json:"-"`
	// ExecutionContextResolved distinguishes effective execution provenance from
	// pending, pre-execution-failed, and legacy request-only rows. Callers must
	// not treat environment, window, or mode fields as executed context while it
	// is false.
	ExecutionContextResolved bool `json:"execution_context_resolved"`
	// ExecutionTargetSnapshot is the immutable, secret-free execution identity
	// captured after runtime context resolution and before the first asset is
	// executed. It remains private recovery provenance rather than API state.
	ExecutionTargetSnapshot *ExecutionTargetSnapshot `json:"-"`
	// ConfirmedPlan is loaded from the immutable reviewed-plan row for worker
	// execution. It is never exposed through run-list DTOs or SSE events.
	ConfirmedPlan *PipelineRunPlan `json:"-"`
}

// ExecutionTargetSnapshot captures the exact source and physical-target
// identity used by one run. Entries are keyed by canonical asset name because
// persisted run steps use that name; AssetID retains the durable identity used
// by fingerprint, coverage, and materialization stores.
type ExecutionTargetSnapshot struct {
	Version               int                                     `json:"version"`
	PipelineUUID          string                                  `json:"pipeline_uuid,omitempty"`
	ConfigurationDigest   string                                  `json:"configuration_digest,omitempty"`
	ConfigurationFidelity string                                  `json:"configuration_fidelity,omitempty"`
	Entries               map[string]ExecutionTargetSnapshotEntry `json:"entries"`
}

type ExecutionUpstreamSnapshot struct {
	Type                      string `json:"type"`
	Value                     string `json:"value"`
	Mode                      string `json:"mode,omitempty"`
	ResolvedAssetID           string `json:"resolved_asset_id,omitempty"`
	Required                  bool   `json:"required,omitempty"`
	ProducerPipelineUUID      string `json:"producer_pipeline_uuid,omitempty"`
	ProducerSnapshotVersionID string `json:"producer_snapshot_version_id,omitempty"`
	TargetIdentity            string `json:"target_identity,omitempty"`
	ExpectedFingerprint       string `json:"expected_fingerprint,omitempty"`
	VarsHash                  string `json:"vars_hash,omitempty"`
	TargetGeneration          int64  `json:"target_generation,omitempty"`
	CompletionID              string `json:"completion_id,omitempty"`
	CompletionOrdinal         int64  `json:"completion_ordinal,omitempty"`
}

type ExecutionTargetSnapshotEntry struct {
	AssetID                     string                       `json:"asset_id"`
	TargetIdentity              string                       `json:"target_identity,omitempty"`
	TargetFidelity              string                       `json:"target_fidelity"`
	TargetWriteEvidenceRequired bool                         `json:"target_write_evidence_required,omitempty"`
	WriteResourceKind           string                       `json:"write_resource_kind,omitempty"`
	WriteResourceIdentity       string                       `json:"write_resource_identity,omitempty"`
	WriteResourceFidelity       string                       `json:"write_resource_fidelity,omitempty"`
	ExecutionContract           PipelineRunExecutionContract `json:"execution_contract,omitempty"`
	Fingerprint                 string                       `json:"fingerprint"`
	OwnContent                  string                       `json:"own_content"`
	ConsumedVarsHash            string                       `json:"consumed_vars_hash"`
	VarsHash                    string                       `json:"vars_hash"`
	Upstreams                   []ExecutionUpstreamSnapshot  `json:"upstreams,omitempty"`
	CoverageMode                string                       `json:"coverage_mode,omitempty"`
	RefreshRestricted           bool                         `json:"refresh_restricted,omitempty"`
}

type PipelineRunStep struct {
	RunID                     string                            `json:"run_id"`
	Asset                     string                            `json:"asset"`
	Status                    RunStatus                         `json:"status"`
	StartedAt                 *time.Time                        `json:"started_at,omitempty"`
	FinishedAt                *time.Time                        `json:"finished_at,omitempty"`
	Error                     string                            `json:"error,omitempty"`
	CompletionOrdinal         *int64                            `json:"completion_ordinal,omitempty"`
	UpstreamWriters           map[string]UpstreamWriterSnapshot `json:"-"`
	HasUpstreamWriterSnapshot bool                              `json:"-"`
}

// UpstreamWriterSnapshot is the exact physical output an asset read from one
// upstream immediately before its main task began. The containing map is keyed
// by AssetID. It is persisted with the run step so recovery never has to infer
// read provenance from a later latest-writer state.
type UpstreamWriterSnapshot struct {
	AssetID           string
	TargetIdentity    string
	Fingerprint       string
	VarsHash          string
	TargetGeneration  int64
	CompletionID      string
	CompletionOrdinal int64
	MaterializedAt    time.Time
}

type LogLine struct {
	At   time.Time `json:"at"`
	Line string    `json:"line"`
}

type RunFilter struct {
	PipelineID  string
	Environment string
	Status      RunStatus
	Query       string
	Limit       int
	Offset      int
}

type RunList struct {
	Runs   []PipelineRun `json:"runs"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
}

type RunResult struct {
	Status string
	Error  string
}

type Runner func(ctx Context, req RunRequest, onLog func(string)) RunResult

type Context interface {
	Done() <-chan struct{}
	Err() error
}

type RunRequest struct {
	RunID             string
	PipelineID        string
	PipelineUUID      string
	Environment       string
	Start             string
	End               string
	ExecutionTime     string
	VariableOverrides map[string]any
	// Scheduled is derived from the persisted server-owned run origin. It must
	// not be inferred from RunID because queued manual runs also have one.
	Scheduled bool
	// SnapshotVersionID pins the exact deployed snapshot the run must execute.
	// Empty identifies a manual working-tree run and is invalid when Scheduled.
	SnapshotVersionID           string
	FullRefresh                 bool
	Backfill                    bool
	ConfirmedEnvironment        string
	SensorMode                  string
	ExpectedSourceMerkle        string
	ExpectedConfigurationDigest string
	ConfirmedPlan               *PipelineRunPlan
	// OnContextResolved must be called synchronously after execution policy,
	// source, defaults, and modes are resolved but before the first asset starts.
	// The scheduler uses it to durably replace admission intent and publish a
	// canonical running event.
	OnContextResolved func(RunExecutionContext) error
	// OnTargetsResolved persists the value-only execution target snapshot after
	// effective configuration is selected and before the first task starts.
	OnTargetsResolved func(ExecutionTargetSnapshot) error
	// OnExecutionUnitsResolved persists a full-pipeline unit selection that
	// could only be derived after parsing the selected source. It must complete
	// before the first unit can transition to running.
	OnExecutionUnitsResolved func([]PipelineRunExecutionUnit) error
	// OnStep is synchronous: a running-step persistence failure must stop before
	// the physical task, and a terminal persistence failure must fail closed.
	OnStep func(RunStepEvent) error
	// OnUnit persists one exact asset/window unit. Unlike OnStep, positions can
	// represent multiple windows for the same asset.
	OnUnit func(PipelineRunUnitEvent) error
}

type RunStepEvent struct {
	Asset                     string
	Status                    RunStatus
	StartedAt                 *time.Time
	FinishedAt                *time.Time
	Error                     string
	CompletionOrdinal         *int64
	UpstreamWriters           map[string]UpstreamWriterSnapshot
	HasUpstreamWriterSnapshot bool
}
