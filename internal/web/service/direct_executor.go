package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	bruinscheduler "github.com/bruin-data/bruin/pkg/scheduler"
	"github.com/spf13/afero"

	"renart/internal/web/duckcoord"
	"renart/internal/web/duckdbsession"
	"renart/internal/web/executiongraph"
	"renart/internal/web/fingerprint"
	"renart/internal/web/runstate"
)

type HybridBruinExecutor struct {
	newConnectionManager          func(context.Context, string) (config.ConnectionAndDetailsGetter, error)
	newPipelineBuilder            func() *pipeline.Builder
	workspaceRoot                 string
	logSink                       ExecutionLogSink
	duckDBCoordinator             *duckcoord.Coordinator
	duckDBSessions                *duckdbsession.Manager
	disableDuckDBFilesystemAccess bool
	fingerprintEngine             *fingerprint.Engine
	workspaceBudget               *executiongraph.Budget
	directTaskGate                func(context.Context, bruinscheduler.TaskInstance) error
	// runRegistry tracks in-flight materializations across every run this
	// executor performs, so the python run broker can wait on them.
	runRegistry *runstate.Registry
}

func NewHybridBruinExecutor(
	workspaceRoot string,
	binaryPath string,
	newConnectionManager func(context.Context, string) (config.ConnectionAndDetailsGetter, error),
	newPipelineBuilder func() *pipeline.Builder,
) *HybridBruinExecutor {
	logSink := ExecutionLogSink(NoopExecutionLogSink{})
	if strings.TrimSpace(workspaceRoot) != "" && filepath.IsAbs(workspaceRoot) {
		logSink = NewBruinFileExecutionLogSink(workspaceRoot)
	}
	duckDBCoordinator := duckcoord.New(duckcoord.Options{})
	return &HybridBruinExecutor{
		newConnectionManager: newConnectionManager,
		newPipelineBuilder:   newPipelineBuilder,
		workspaceRoot:        workspaceRoot,
		logSink:              logSink,
		duckDBCoordinator:    duckDBCoordinator,
		duckDBSessions:       duckdbsession.New(duckDBCoordinator),
		fingerprintEngine:    fingerprint.NewEngine(),
		workspaceBudget:      executiongraph.NewBudget(executionWorkspaceLimit()),
		runRegistry:          runstate.NewRegistry(),
	}
}

const (
	executionWorkspaceLimitEnvironment  = "RENART_EXECUTION_WORKSPACE_MAX_ACTIVE_STEPS"
	executionForceSequentialEnvironment = "RENART_EXECUTION_FORCE_SEQUENTIAL"
)

func executionWorkspaceLimit() int {
	const fallback = 8
	raw := strings.TrimSpace(os.Getenv(executionWorkspaceLimitEnvironment))
	if raw == "" {
		return fallback
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 {
		return fallback
	}
	return limit
}

func executionMaxActiveSteps(requested int) int {
	raw := strings.TrimSpace(os.Getenv(executionForceSequentialEnvironment))
	forceSequential, err := strconv.ParseBool(raw)
	if err == nil && forceSequential {
		return 1
	}
	return requested
}

// SetDuckDBCoordinator replaces the database coordinator. It is primarily
// useful for tests that need an isolated lock directory.
func (e *HybridBruinExecutor) SetDuckDBCoordinator(coordinator *duckcoord.Coordinator) {
	if coordinator == nil {
		coordinator = duckcoord.New(duckcoord.Options{})
	}
	e.duckDBCoordinator = coordinator
	e.duckDBSessions = duckdbsession.New(coordinator)
}

// SetDuckDBFilesystemAccess controls LocalFileSystem access on native DuckDB
// execution connections. It defaults to enabled.
func (e *HybridBruinExecutor) SetDuckDBFilesystemAccess(enabled bool) {
	e.disableDuckDBFilesystemAccess = !enabled
}

func (e *HybridBruinExecutor) SetExecutionWorkspaceBudget(budget *executiongraph.Budget) {
	if budget == nil {
		budget = executiongraph.NewBudget(executionWorkspaceLimit())
	}
	e.workspaceBudget = budget
}

// SetDirectTaskGate installs a cancellable test seam immediately before a
// physical task. Production leaves it nil.
func (e *HybridBruinExecutor) SetDirectTaskGate(
	gate func(context.Context, bruinscheduler.TaskInstance) error,
) {
	e.directTaskGate = gate
}

func (e *HybridBruinExecutor) SetExecutionLogSink(sink ExecutionLogSink) {
	if sink == nil {
		sink = NoopExecutionLogSink{}
	}
	e.logSink = sink
}

// SetFingerprintEngine shares the canonical fingerprint cache with services
// that plan, execute, and record the same workspace. A nil engine restores an
// isolated engine rather than leaving target capture without one.
func (e *HybridBruinExecutor) SetFingerprintEngine(engine *fingerprint.Engine) {
	if engine == nil {
		engine = fingerprint.NewEngine()
	}
	e.fingerprintEngine = engine
}

func (e *HybridBruinExecutor) executionLogSink() ExecutionLogSink {
	if e == nil || e.logSink == nil {
		return NoopExecutionLogSink{}
	}
	return e.logSink
}

func (e *HybridBruinExecutor) FormatAsset(ctx context.Context, req FormatAssetRequest) ([]byte, error) {
	_ = ctx
	assetPath := resolveDirectPath(e.workspaceRoot, req.AssetPath)
	osFS := afero.NewOsFs()
	builder := NewRenartPipelineBuilder(osFS)
	asset, err := builder.CreateAssetFromFile(assetPath, nil)
	if err != nil {
		return nil, err
	}
	if asset == nil {
		return nil, fmt.Errorf("no valid asset found in the file")
	}
	if err := asset.Persist(afero.NewOsFs()); err != nil {
		return nil, err
	}
	return []byte(""), nil
}

func (e *HybridBruinExecutor) ApplyPatch(ctx context.Context, req PatchRequest) ([]byte, error) {
	switch req.Operation {
	case "fill-asset-dependencies":
		return e.applyFillAssetDependencies(ctx, req.TargetPath)
	case "fill-columns-from-db":
		return e.applyFillColumnsFromDB(ctx, req.TargetPath)
	default:
		return nil, fmt.Errorf("direct patch operation %q is not supported", req.Operation)
	}
}

func (e *HybridBruinExecutor) RunWithRetry(
	ctx context.Context,
	req QueryAssetRequest,
	retries int,
	initialDelay time.Duration,
) ([]byte, error, int) {
	attempt := 0
	delay := initialDelay
	for {
		attempt++
		output, err := e.QueryAsset(ctx, req)
		if err == nil {
			return output, nil, attempt
		}
		if !IsDuckDBLockError(err, output) || attempt > retries {
			return output, err, attempt
		}
		select {
		case <-ctx.Done():
			return output, ctx.Err(), attempt
		case <-time.After(delay):
		}
		delay *= 2
	}
}
