// Package service provides the business logic layer for the Bruin web server.
package service

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"renart/internal/web/bus"
)

const PipelineExecutionPlanVersionV3 = 3

type RunAssetRequest struct {
	AssetPath   string
	Environment string
	SensorMode  string
	StartDate   string
	EndDate     string
	AssetEvent  func(ExecutionAssetEvent) error
	FullRefresh bool
	// BeforeTargetWrite is called by operators whose successful main task does
	// not itself prove that a declared output was written. It must return before
	// the physical write begins so target fencing remains fail-closed.
	BeforeTargetWrite func(assetName string) error
	// OnTargetsResolved must succeed synchronously after effective execution
	// context and target resolution but before the first task starts.
	OnTargetsResolved func(ExecutionTargetSnapshot) error
}

type RunPipelineRequest struct {
	Target        string
	Environment   string
	SensorMode    string
	DryRun        bool
	StartDate     string
	EndDate       string
	ExecutionTime time.Time
	// VariableOverrides is a private normalized RunSpec input. It is applied as
	// a pipeline mutator before assets are constructed.
	VariableOverrides  map[string]any
	RunID              string
	AssetEvent         func(ExecutionAssetEvent) error
	SelectionMode      string
	PlanVersion        int
	MaxActiveSteps     int
	ExecutionContracts []PipelinePlanExecutionContract
	ExecutionUnits     []PipelineExecutionUnit
	UnitEvent          func(PipelineExecutionUnitEvent) error
	// OnExecutionUnitsResolved must succeed after a dynamic full-pipeline plan
	// is normalized and before the first unit starts.
	OnExecutionUnitsResolved func([]PipelineExecutionUnit) error
	BeforeTargetWrite        func(assetName string) error
	// OnTargetsResolved must succeed synchronously after effective execution
	// context and target resolution but before the first task starts. Dry runs
	// do not resolve or capture execution targets.
	OnTargetsResolved func(ExecutionTargetSnapshot) error
	// ConfigPath overrides .bruin.yml discovery via the git repo root. Set
	// for snapshot runs, whose target directory lives outside the workspace.
	ConfigPath  string
	FullRefresh bool
}

type ExecutionAssetEvent struct {
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

type QueryAssetRequest struct {
	AssetPath   string
	Limit       string
	Environment string
	ConfigFile  string
	Output      string
	StartDate   string
	EndDate     string
}

type QueryConnectionRequest struct {
	ConnectionName string
	Query          string
	Environment    string
	Output         string
	LogicalSchema  bool
}

type FormatAssetRequest struct {
	AssetPath   string
	UseSQLFluff bool
}

type PatchRequest struct {
	Operation  string
	TargetPath string
}

type ImportDatabaseRequest struct {
	PipelinePath   string
	ConnectionName string
	Schema         string
	Schemas        []string
	Tables         []string
	DisableColumns bool
	PreviewOnly    bool
	RejectExisting bool
	Environment    string
	ConfigFilePath string
}

// BruinCommandExecutor executes Bruin operations through typed methods.
// Implementations can shell out to the Bruin CLI today and later call the
// corresponding Go APIs directly.
type BruinCommandExecutor interface {
	RunAsset(ctx context.Context, req RunAssetRequest, onChunk func([]byte)) ([]byte, error)
	RunPipeline(ctx context.Context, req RunPipelineRequest, onChunk func([]byte)) ([]byte, error)
	QueryAsset(ctx context.Context, req QueryAssetRequest) ([]byte, error)
	QueryConnection(ctx context.Context, req QueryConnectionRequest) ([]byte, error)
	FormatAsset(ctx context.Context, req FormatAssetRequest) ([]byte, error)
	ApplyPatch(ctx context.Context, req PatchRequest) ([]byte, error)
	ImportDatabase(ctx context.Context, req ImportDatabaseRequest) ([]byte, error)
	RunWithRetry(ctx context.Context, req QueryAssetRequest, retries int, initialDelay time.Duration) ([]byte, error, int)
}

func IsDuckDBLockError(err error, output []byte) bool {
	if err == nil {
		return false
	}

	message := strings.ToLower(err.Error() + "\n" + string(output))
	return strings.Contains(message, "could not set lock on file") ||
		strings.Contains(message, "conflicting lock is held")
}

type streamCaptureWriter struct {
	mu      sync.Mutex
	buffer  *bytes.Buffer
	onChunk func([]byte)
}

func (w *streamCaptureWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if _, err := w.buffer.Write(p); err != nil {
		return 0, err
	}

	if w.onChunk != nil {
		chunk := append([]byte(nil), p...)
		w.onChunk(chunk)
	}

	return len(p), nil
}

var _ io.Writer = (*streamCaptureWriter)(nil)
