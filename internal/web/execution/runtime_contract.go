package execution

import (
	"time"

	"renart/internal/web/bus"
)

const (
	ExecutionTargetSnapshotVersion = 5
	ExecutionPlanVersionV3         = 3
)

type CoverageMode string

const (
	CoverageMarker          CoverageMode = "marker"
	CoverageUnionIntervals  CoverageMode = "union_intervals"
	CoverageReplaceInterval CoverageMode = "replace_interval"
)

type UpstreamSnapshot struct {
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

// TargetSnapshot captures the value-only target and fingerprint context
// resolved immediately before execution.
type TargetSnapshot struct {
	Version               int                            `json:"version"`
	PipelineUUID          string                         `json:"pipeline_uuid"`
	ConfigurationDigest   string                         `json:"configuration_digest,omitempty"`
	ConfigurationFidelity string                         `json:"configuration_fidelity,omitempty"`
	Entries               map[string]TargetSnapshotEntry `json:"entries"`
}

type TargetSnapshotEntry struct {
	AssetID                     string             `json:"asset_id"`
	ExternalSource              bool               `json:"external_source,omitempty"`
	TargetIdentity              string             `json:"target_identity"`
	TargetFidelity              RenderFidelity     `json:"target_fidelity"`
	TargetWriteEvidenceRequired bool               `json:"target_write_evidence_required,omitempty"`
	WriteResourceKind           string             `json:"write_resource_kind"`
	WriteResourceIdentity       string             `json:"write_resource_identity,omitempty"`
	WriteResourceFidelity       RenderFidelity     `json:"write_resource_fidelity"`
	ExecutionContract           ExecutionContract  `json:"execution_contract"`
	Fingerprint                 string             `json:"fingerprint"`
	OwnContent                  string             `json:"own_content"`
	ConsumedVarsHash            string             `json:"consumed_vars_hash"`
	VarsHash                    string             `json:"vars_hash"`
	Upstreams                   []UpstreamSnapshot `json:"upstreams"`
	CoverageMode                CoverageMode       `json:"coverage_mode"`
	RefreshRestricted           bool               `json:"refresh_restricted"`
}

// RunSpec is the normalized private input for one pipeline execution. It
// carries exact reviewed contracts and synchronous persistence callbacks; it
// is never exposed as a public HTTP DTO.
type RunSpec struct {
	RunID                       string
	CompletionID                string
	PipelineID                  string
	PipelineUUID                string
	Environment                 string
	Scheduled                   bool
	SensorMode                  string
	DryRun                      bool
	FullRefresh                 bool
	Backfill                    bool
	StartDate                   string
	EndDate                     string
	ConfirmedEnvironment        string
	SnapshotDir                 string
	SnapshotVersionID           string
	ExecutionTime               string
	VariableOverrides           map[string]any
	ExpectedSourceMerkle        string
	ExpectedConfigurationDigest string
	Plan                        *ExecutionPlan
	ConfigPath                  string
	OnContextResolved           func(ResolvedRunContext) error
	OnTargetsResolved           func(TargetSnapshot) error
	OnExecutionUnitsResolved    func([]ExecutionUnit) error
	OnUnit                      func(ExecutionUnitEvent) error
	// TargetSnapshot is populated internally after resolution and carried only
	// on the synchronous completion path.
	TargetSnapshot *TargetSnapshot
}

type ExecutionPlan struct {
	Version        int
	SelectionMode  string
	MaxActiveSteps int
	Contracts      []ExecutionContract
	Prerequisites  []Prerequisite
	Units          []ExecutionUnit
}

type ExecutionUnit struct {
	Position            int
	AssetID             string
	AssetName           string
	AssetPath           string
	StartDate           string
	EndDate             string
	RenderIndex         int
	Reason              string
	DependencyPositions []int
}

type ExecutionUnitEvent struct {
	Position   int
	Status     string
	StartedAt  *time.Time
	FinishedAt *time.Time
	Error      string
}

type ResolvedRunContext struct {
	Environment string
	WinStart    time.Time
	WinEnd      time.Time
	FullRefresh bool
	Backfill    bool
	SensorMode  string
}

// PipelineRequest is the private contract passed from execution orchestration
// to the physical Bruin adapter. Its callbacks form the durability barrier
// before targets or execution units can start.
type PipelineRequest struct {
	Target             string
	Environment        string
	SensorMode         string
	DryRun             bool
	StartDate          string
	EndDate            string
	ExecutionTime      time.Time
	VariableOverrides  map[string]any
	RunID              string
	AssetEvent         func(AssetEvent) error
	SelectionMode      string
	PlanVersion        int
	MaxActiveSteps     int
	ExecutionContracts []ExecutionContract
	Prerequisites      []Prerequisite
	ExecutionUnits     []ExecutionUnit
	UnitEvent          func(ExecutionUnitEvent) error
	// OnExecutionUnitsResolved must succeed after a dynamic full-pipeline plan
	// is normalized and before the first unit starts.
	OnExecutionUnitsResolved func([]ExecutionUnit) error
	// BeforeTargetWrite fences operators whose successful main task does not
	// itself prove that the declared output was written.
	BeforeTargetWrite func(assetName string) error
	// OnTargetsResolved must succeed after effective target resolution and
	// before the first task starts. Dry runs do not resolve target snapshots.
	OnTargetsResolved func(TargetSnapshot) error
	// ConfigPath overrides .bruin.yml discovery for immutable snapshot runs.
	ConfigPath  string
	FullRefresh bool
}

type AssetEvent struct {
	Asset                     string
	Status                    string
	TaskKind                  string
	CheckName                 string
	CheckColumn               string
	CheckBlocking             bool
	StartedAt                 *time.Time
	FinishedAt                *time.Time
	Error                     string
	CompletionOrdinal         *int64
	UpstreamWriters           map[string]bus.UpstreamWriterSnapshot
	HasUpstreamWriterSnapshot bool
	UnitPosition              int
	HasUnitPosition           bool
}
