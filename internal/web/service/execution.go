package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/sqlparser"
	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"renart/internal/web/bus"
	"renart/internal/web/identity"
	"renart/internal/web/matlog"
	"renart/internal/web/policy"
	"renart/internal/web/runcontext"
	webscheduler "renart/internal/web/scheduler"
)

type InspectResult struct {
	Status                              string
	Columns                             []string
	Rows                                []map[string]any
	RawOutput                           string
	Operation                           OperationMetadata
	Error                               string
	Info                                string
	MissingUpstreamAssetIDs             []string
	MissingUpstreamAssetNames           []string
	MissingUpstreamAssetsMaterializable bool
	Attempts                            int
	Retryable                           bool
	HTTPStatus                          int
}

type MaterializeResult struct {
	Status          string
	Operation       OperationMetadata
	Output          string
	Error           string
	ExitCode        int
	ChangedAssetIDs []string
	MaterializedAt  *time.Time
	Warnings        []string
}

type MaterializeScope string

const (
	MaterializeScopeAsset                 MaterializeScope = "asset"
	MaterializeScopeAssetWithUpstreams    MaterializeScope = "asset_with_upstreams"
	MaterializeScopeAssetWithDownstreams  MaterializeScope = "asset_with_downstreams"
	MaterializeScopeAssetWithNeighborhood MaterializeScope = "asset_with_upstreams_and_downstreams"
)

type TargetWriteStore interface {
	ClaimTargetWrite(context.Context, matlog.TargetWriteClaim) error
	MarkTargetWriteClaimDirty(context.Context, matlog.TargetWriteClaim, time.Time) error
	LatestWriters(context.Context, []string) (map[string]matlog.LatestSuccessfulWriter, error)
}

type ExecutionDependencies struct {
	WorkspaceRoot        string
	ConfigPath           string
	Executor             BruinCommandExecutor
	ResolveAssetByID     func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error)
	ResolveAssetNameByID func(string) string
	FindInspectIDs       func(...string) []string
	CurrentPipelines     func() []PipelineView
	ParseQueryOutput     func([]byte) ([]string, []map[string]any)
	NewPipelineBuilder   func() *pipeline.Builder
	Events               *bus.Bus
	// TargetWrites durably claims exact physical outputs before their main
	// task starts and marks uncertain outcomes dirty. Nil retains the legacy
	// in-memory observation behavior used by isolated tests.
	TargetWrites TargetWriteStore
	// DispatchCompletion persists and dispatches a self-contained completion
	// event. Production wires this through the durable completion outbox; nil
	// falls back to the in-process bus for isolated/legacy callers.
	DispatchCompletion func(context.Context, bus.RunCompleted) error
	// AcquireExecutionLease coordinates physical execution with cross-process
	// recovery of interrupted target-write claims. Production holds the shared
	// lease from before target capture until the durable completion hand-off;
	// nil keeps isolated tests and non-writing callers lightweight.
	AcquireExecutionLease func(context.Context) (release func() error, err error)
	// PolicyFor returns the execution policy for an environment; nil means
	// unrestricted. Enforced here — the run-dispatch chokepoint every
	// execution path goes through — not in UI handlers.
	PolicyFor           func(environment string) policy.EnvironmentPolicy
	SelectedEnvironment func() string
	// InlineRuns records synchronous full-pipeline mutations in the same
	// durable run ledger as River-backed work. It may be attached after service
	// construction because the scheduler store is initialized later at startup.
	InlineRuns InlineRunLedger
}

// InlineRunLedger is implemented by scheduler.Service. Keeping the execution
// service on this narrow lifecycle contract preserves inline SSE streaming
// while giving it the same durable RunSpec, run slot, logs, and steps as queued
// executions.
type InlineRunLedger interface {
	AdmitInlineRun(context.Context, webscheduler.InlineRunAdmission) (webscheduler.PipelineRun, error)
	StartInlineRun(context.Context, string, time.Time) error
	BindInlineRunExecutionUnits(context.Context, string, []webscheduler.RunSelectionUnit) error
	SetInlineRunExecutionTargetSnapshot(context.Context, string, webscheduler.ExecutionTargetSnapshot) error
	RecordInlineRunStep(context.Context, string, webscheduler.RunStepEvent) error
	RecordInlineRunUnit(context.Context, string, webscheduler.PipelineRunUnitEvent) error
	AppendInlineRunLog(context.Context, string, string) error
	FinishInlineRun(context.Context, string, webscheduler.RunStatus, error) error
}

type PipelineView struct {
	ID     string
	UUID   string
	Name   string
	Assets []AssetView
}

type AssetView struct {
	ID                string
	Name              string
	QualityCheckCount int
}

type PipelineMaterializationInfo struct {
	AssetName             string
	Connection            string
	IsMaterialized        bool
	VerificationAvailable bool
	MaterializedAs        string
	RowCount              *int64
	DeclaredMatType       string
}

type PipelineMaterializationState struct {
	AssetID        string `json:"asset_id"`
	IsMaterialized bool   `json:"is_materialized"`
	// VerificationAvailable is process-local evidence for the staleness
	// trust-but-verify pass. It is not part of the browser response: callers
	// must not reinterpret an unavailable credential/warehouse as a confirmed
	// missing relation.
	VerificationAvailable bool   `json:"-"`
	MaterializedAs        string `json:"materialized_as,omitempty"`
	RowCount              *int64 `json:"row_count,omitempty"`
	Connection            string `json:"connection,omitempty"`
	DeclaredMatType       string `json:"materialization_type,omitempty"`
}

type PipelineMaterializationResponse struct {
	PipelineID string                         `json:"pipeline_id"`
	Assets     []PipelineMaterializationState `json:"assets"`
}

type ExecutionService struct {
	deps         ExecutionDependencies
	inlineRunsMu sync.RWMutex
	inlineRuns   InlineRunLedger
}

const inspectReadOnlyErrorMessage = "Inspect only supports read-only single SELECT queries. Materialize the asset to run write, delete, copy, or multi-statement SQL."

func NewExecutionService(deps ExecutionDependencies) *ExecutionService {
	return &ExecutionService{deps: deps, inlineRuns: deps.InlineRuns}
}

// SetInlineRunLedger attaches the durable ledger after the shared scheduler
// store and service have been constructed. Production calls it before serving
// requests; the mutex keeps tests and alternate embedders safe.
func (s *ExecutionService) SetInlineRunLedger(ledger InlineRunLedger) {
	if s == nil {
		return
	}
	s.inlineRunsMu.Lock()
	s.inlineRuns = ledger
	s.inlineRunsMu.Unlock()
}

func (s *ExecutionService) inlineRunLedger() InlineRunLedger {
	if s == nil {
		return nil
	}
	s.inlineRunsMu.RLock()
	defer s.inlineRunsMu.RUnlock()
	return s.inlineRuns
}

type executionOriginContextKey struct{}

// WithExecutionOrigin marks a trusted in-process invocation. HTTP callers do
// not control this value and therefore retain the server-owned API origin.
func WithExecutionOrigin(ctx context.Context, origin webscheduler.RunTrigger) context.Context {
	return context.WithValue(ctx, executionOriginContextKey{}, origin)
}

// ExecutionOrigin returns the trusted server-side origin for an execution.
// Requests without an authenticated in-process marker are API calls.
func ExecutionOrigin(ctx context.Context) webscheduler.RunTrigger {
	if ctx != nil {
		if origin, ok := ctx.Value(executionOriginContextKey{}).(webscheduler.RunTrigger); ok {
			switch origin {
			case webscheduler.RunTriggerManual, webscheduler.RunTriggerAPI, webscheduler.RunTriggerCLI:
				return origin
			}
		}
	}
	return webscheduler.RunTriggerAPI
}

// emitRunCompleted publishes the run-completion event on the process bus.
// This is the single seam Phase 2 (materialization facts) and Phase 3
// (staleness) attach to for run observation.
func (s *ExecutionService) emitRunCompleted(ctx context.Context, runID, pipelineUUID, environment string, window ExecutionTimeWindow, completedAt time.Time, assets []bus.AssetRun) error {
	return s.emitRunCompletedForSpec(ctx, PipelineRunSpec{RunID: runID, Environment: environment}, pipelineUUID, window, completedAt, assets)
}

func (s *ExecutionService) emitRunCompletedForSpec(ctx context.Context, spec PipelineRunSpec, pipelineUUID string, window ExecutionTimeWindow, completedAt time.Time, assets []bus.AssetRun) error {
	if pipelineUUID == "" || len(assets) == 0 {
		return nil
	}
	event := bus.RunCompleted{
		RunID:             spec.RunID,
		CompletionID:      completionIDForRun(spec),
		PipelineUUID:      pipelineUUID,
		Environment:       spec.Environment,
		FullRefresh:       spec.FullRefresh,
		CompletedAt:       completedAt,
		Assets:            assets,
		SnapshotVersionID: spec.SnapshotVersionID,
		SnapshotDir:       spec.SnapshotDir,
	}
	if snapshot := spec.executionTargetSnapshot; snapshot != nil {
		event.ExecutionTargetSnapshotVersion = snapshot.Version
		event.ExecutionPipelineUUID = snapshot.PipelineUUID
		event.ExecutionTargets = make(map[string]bus.ExecutionTargetSnapshotEntry, len(snapshot.Entries))
		for assetName, entry := range snapshot.Entries {
			upstreams := make([]bus.ExecutionUpstreamSnapshot, 0, len(entry.Upstreams))
			for _, upstream := range entry.Upstreams {
				upstreams = append(upstreams, bus.ExecutionUpstreamSnapshot{
					Type: upstream.Type, Value: upstream.Value, Mode: upstream.Mode,
					ResolvedAssetID: upstream.ResolvedAssetID, Required: upstream.Required,
					ProducerPipelineUUID:      upstream.ProducerPipelineUUID,
					ProducerSnapshotVersionID: upstream.ProducerSnapshotVersionID,
					TargetIdentity:            upstream.TargetIdentity, ExpectedFingerprint: upstream.ExpectedFingerprint,
					VarsHash: upstream.VarsHash, TargetGeneration: upstream.TargetGeneration,
					CompletionID: upstream.CompletionID, CompletionOrdinal: upstream.CompletionOrdinal,
				})
			}
			event.ExecutionTargets[assetName] = bus.ExecutionTargetSnapshotEntry{
				AssetID:                     entry.AssetID,
				ExternalSource:              entry.ExternalSource,
				TargetIdentity:              entry.TargetIdentity,
				TargetFidelity:              string(entry.TargetFidelity),
				TargetWriteEvidenceRequired: entry.TargetWriteEvidenceRequired,
				WriteResourceKind:           entry.WriteResourceKind,
				WriteResourceIdentity:       entry.WriteResourceIdentity,
				WriteResourceFidelity:       string(entry.WriteResourceFidelity),
				ExecutionContract:           busExecutionContract(entry.ExecutionContract),
				Fingerprint:                 entry.Fingerprint,
				OwnContent:                  entry.OwnContent,
				ConsumedVarsHash:            entry.ConsumedVarsHash,
				VarsHash:                    entry.VarsHash,
				Upstreams:                   upstreams,
				CoverageMode:                string(entry.CoverageMode),
				RefreshRestricted:           entry.RefreshRestricted,
			}
		}
	}
	if !window.IsZero() {
		start := window.Start
		end := window.End
		event.WinStart = &start
		event.WinEnd = &end
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if s.deps.DispatchCompletion != nil {
		return s.deps.DispatchCompletion(persistCtx, event)
	}
	if s.deps.Events != nil {
		return s.deps.Events.EmitRunCompleted(event)
	}
	return nil
}

func completionIDForRun(spec PipelineRunSpec) string {
	if completionID := strings.TrimSpace(spec.CompletionID); completionID != "" {
		return completionID
	}
	if runID := strings.TrimSpace(spec.RunID); runID != "" {
		return runID
	}
	return uuid.NewString()
}

// checkRunPolicy evaluates the environment policy at the run-dispatch
// chokepoint.
func (s *ExecutionService) checkRunPolicy(request policy.RunRequest) error {
	if s.deps.PolicyFor == nil {
		return nil
	}
	return policy.Check(s.deps.PolicyFor(request.Environment), request)
}

func (s *ExecutionService) effectiveEnvironment(environment string) string {
	if environment = strings.TrimSpace(environment); environment != "" {
		return environment
	}
	if s.deps.SelectedEnvironment != nil {
		return strings.TrimSpace(s.deps.SelectedEnvironment())
	}
	return ""
}

func (s *ExecutionService) effectiveFullRefresh(ctx context.Context, environment string, requested bool) bool {
	if !requested || strings.TrimSpace(s.deps.ConfigPath) == "" {
		return requested
	}
	cfg, err := loadSelectedConfig(s.deps.ConfigPath, environment)
	if err != nil || !selectedEnvironmentRestrictsFullRefresh(cfg) {
		return requested
	}
	addExecutionWarning(ctx, fmt.Sprintf("Full refresh is restricted for environment %s; running configured materialization strategies instead.", environment))
	return false
}

// findPipelineViewForAsset locates the workspace pipeline containing the
// given encoded asset ID.
func (s *ExecutionService) findPipelineViewForAsset(assetID string) (PipelineView, bool) {
	if s.deps.CurrentPipelines == nil {
		return PipelineView{}, false
	}
	for _, view := range s.deps.CurrentPipelines() {
		for _, asset := range view.Assets {
			if asset.ID == assetID {
				return view, true
			}
		}
	}
	return PipelineView{}, false
}

func (s *ExecutionService) InspectAsset(ctx context.Context, assetID, limit, environment, startDate, endDate string) InspectResult {
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return InspectResult{Status: "error", Error: "invalid asset id", HTTPStatus: 400}
	}

	if guardErr := s.ensureAssetInspectable(ctx, assetID, environment, startDate, endDate); guardErr != nil {
		return InspectResult{
			Status:     "error",
			Columns:    []string{},
			Rows:       []map[string]any{},
			RawOutput:  guardErr.Error(),
			Operation:  queryAssetOperation(relAssetPath, limit, environment, ""),
			Error:      guardErr.Error(),
			Attempts:   0,
			Retryable:  false,
			HTTPStatus: 400,
		}
	}

	if result, ok := s.inspectMaterializedNonSQLAsset(ctx, assetID, relAssetPath, limit, environment); ok {
		return result
	}

	queryReq := QueryAssetRequest{
		AssetPath:   relAssetPath,
		Limit:       limit,
		Environment: environment,
		StartDate:   startDate,
		EndDate:     endDate,
		Output:      "json",
	}
	timeWindow, _ := s.resolveAssetExecutionTimeWindow(ctx, assetID, startDate, endDate, time.Now().UTC())
	operation := withOperationTimeWindow(queryAssetOperation(relAssetPath, limit, environment, ""), timeWindow)

	var output []byte
	var attempts int
	run := func() error {
		var runErr error
		output, runErr, attempts = s.deps.Executor.RunWithRetry(ctx, queryReq, 4, 150*time.Millisecond)
		return runErr
	}

	err = run()

	if err != nil {
		statusCode := 400
		errorMessage := err.Error()
		rawOutput := extractInspectRawOutput(output)
		if rawOutput == "" {
			rawOutput = string(output)
		}
		if IsDuckDBLockError(err, output) {
			statusCode = 409
			errorMessage = "duckdb database is busy (lock held by another process), please retry"
		}
		detectionText := rawOutput
		if strings.TrimSpace(detectionText) == "" {
			detectionText = errorMessage
		}
		missingUpstreamIDs, missingUpstreamNames := s.findMissingUpstreamAssets(ctx, assetID, detectionText)
		return InspectResult{
			Status:                              "error",
			Columns:                             []string{},
			Rows:                                []map[string]any{},
			RawOutput:                           rawOutput,
			Operation:                           operation,
			Error:                               errorMessage,
			MissingUpstreamAssetIDs:             missingUpstreamIDs,
			MissingUpstreamAssetNames:           missingUpstreamNames,
			MissingUpstreamAssetsMaterializable: len(missingUpstreamIDs) > 0,
			Attempts:                            attempts,
			Retryable:                           statusCode == 409,
			HTTPStatus:                          statusCode,
		}
	}

	columns, rows := s.deps.ParseQueryOutput(output)
	// Surface the executed (rendered) query so the UI can show what actually ran.
	operation.Query = ExtractQueryTextFromOutput(output)
	return InspectResult{
		Status:     "ok",
		Columns:    columns,
		Rows:       rows,
		RawOutput:  string(output),
		Operation:  operation,
		Attempts:   attempts,
		HTTPStatus: 200,
	}
}

func (s *ExecutionService) inspectMaterializedNonSQLAsset(ctx context.Context, assetID, relAssetPath, limit, environment string) (InspectResult, bool) {
	if s.deps.ResolveAssetByID == nil {
		return InspectResult{}, false
	}

	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil || parsedPipeline == nil || asset == nil || asset.IsSQLAsset() {
		return InspectResult{}, false
	}

	rowLimit := normalizeInspectLimit(limit)
	if isSensorAssetType(asset.Type) {
		return InspectResult{
			Status:     "info",
			Columns:    []string{},
			Rows:       []map[string]any{},
			Operation:  queryAssetOperation(relAssetPath, limit, environment, ""),
			Info:       "Sensors do not materialize previewable data. Run the sensor to check its condition now.",
			HTTPStatus: 200,
		}, true
	}

	// A non-SQL asset (python, api, load) is inspected by previewing the table it
	// materializes into. For load assets the destination is a flat parameter; for
	// python/api it's the asset's own connection + name.
	var connectionName, tableName string
	if isLoadAsset(asset) {
		params, paramsErr := resolvedLoadParams(asset, parsedPipeline)
		if paramsErr != nil {
			return InspectResult{}, false
		}
		if isLocalLoadConnection(params.DestinationConnection) || strings.TrimSpace(params.DestinationObject) != "" {
			// A load asset that writes to a file/object has no queryable table —
			// surface an informational note rather than an error.
			return InspectResult{
				Status:     "info",
				Columns:    []string{},
				Rows:       []map[string]any{},
				Operation:  queryAssetOperation(relAssetPath, limit, environment, ""),
				Info:       fmt.Sprintf("This load asset writes to %s, which can't be previewed as a database table.", strings.TrimSpace(params.DestinationObject)),
				HTTPStatus: 200,
			}, true
		}
		if strings.TrimSpace(params.DestinationConnection) == "" {
			return InspectResult{}, false
		}
		connectionName = params.DestinationConnection
		tableName = asset.Name
	} else {
		connectionName, err = targetConnectionNameForAsset(asset, parsedPipeline)
		if err != nil || strings.TrimSpace(connectionName) == "" {
			return InspectResult{}, false
		}
		tableName = asset.Name
	}

	query := fmt.Sprintf("select * from %s limit %d", tableName, rowLimit)
	operation := queryConnectionOperation(connectionName, query, environment)
	operation.AssetPath = relAssetPath
	operation.Target = relAssetPath
	operation.Limit = limit

	columns, rows, err := s.RunConnectionQueryForEnvironment(ctx, connectionName, environment, query)
	if err != nil {
		return InspectResult{
			Status:     "error",
			Columns:    []string{},
			Rows:       []map[string]any{},
			RawOutput:  err.Error(),
			Operation:  operation,
			Error:      fmt.Sprintf("No materialized table found for %s on connection %s. Materialize the asset first, then inspect again.", asset.Name, connectionName),
			Attempts:   0,
			Retryable:  false,
			HTTPStatus: 400,
		}, true
	}

	output, _ := json.Marshal(QueryRowsEnvelope{Columns: columns, Rows: rows})
	return InspectResult{
		Status:     "ok",
		Columns:    columns,
		Rows:       rows,
		RawOutput:  string(output),
		Operation:  operation,
		Attempts:   1,
		HTTPStatus: 200,
	}, true
}

func normalizeInspectLimit(limit string) int {
	trimmed := strings.TrimSpace(limit)
	if trimmed == "" {
		return 100
	}
	var value int
	if _, err := fmt.Sscanf(trimmed, "%d", &value); err != nil || value <= 0 {
		return 100
	}
	if value > 1000 {
		return 1000
	}
	return value
}

func (s *ExecutionService) ensureAssetInspectable(ctx context.Context, assetID, environment, startDate, endDate string) error {
	if s.deps.ResolveAssetByID == nil {
		return nil
	}

	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return err
	}
	if asset == nil || parsedPipeline == nil || !asset.IsSQLAsset() {
		return nil
	}

	queryStr, err := getDirectRenderedQuery(
		ctx,
		&directPipelineInfo{
			Pipeline: parsedPipeline,
			Asset:    asset,
			Config:   loadExecutionConfigOrEmpty(s.deps.ConfigPath),
		},
		environment,
		startDate,
		endDate,
	)
	if err != nil {
		return nil
	}

	ok, err := isReadOnlySelectQuery(queryStr, asset.Type)
	if err != nil {
		return nil
	}
	if ok {
		return nil
	}

	return fmt.Errorf(inspectReadOnlyErrorMessage)
}

func loadExecutionConfigOrEmpty(configPath string) *config.Config {
	selected, err := selectConfigEnvironment(loadConfigOrEmpty(configPath), "")
	if err != nil {
		return &config.Config{}
	}
	return selected
}

func isReadOnlySelectQuery(queryStr string, assetType pipeline.AssetType) (bool, error) {
	dialect, err := sqlparser.AssetTypeToDialect(assetType)
	if err != nil {
		return false, err
	}

	parser, err := sqlparser.NewSQLParser(false)
	if err != nil {
		return false, err
	}
	defer parser.Close()

	return parser.IsSingleSelectQuery(queryStr, dialect)
}

func extractInspectRawOutput(output []byte) string {
	trimmed := strings.TrimSpace(string(output))
	if trimmed == "" {
		return ""
	}

	var envelope map[string]any
	if err := json.Unmarshal(output, &envelope); err != nil {
		return trimmed
	}

	for _, key := range []string{"raw_output", "error"} {
		if value, ok := envelope[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}

	return trimmed
}

func (s *ExecutionService) MaterializeAssetStream(ctx context.Context, assetID, environment, scope, startDate, endDate string, fullRefresh, backfill bool, confirmedEnvironment string, onChunk func([]byte)) MaterializeResult {
	return s.MaterializeAssetStreamWithSensorMode(ctx, assetID, environment, scope, startDate, endDate, fullRefresh, backfill, confirmedEnvironment, "", onChunk)
}

func (s *ExecutionService) MaterializeAssetStreamWithSensorMode(ctx context.Context, assetID, environment, scope, startDate, endDate string, fullRefresh, backfill bool, confirmedEnvironment, sensorMode string, onChunk func([]byte)) MaterializeResult {
	normalizedContext, contextErr := runcontext.Normalize(runcontext.Input{
		Start:       startDate,
		End:         endDate,
		FullRefresh: fullRefresh,
		Backfill:    backfill,
		SensorMode:  sensorMode,
	})
	if contextErr != nil {
		return MaterializeResult{Status: "error", Error: contextErr.Error(), ExitCode: 1}
	}
	startDate = normalizedContext.StartString()
	endDate = normalizedContext.EndString()
	sensorMode = normalizedContext.SensorMode

	ctx, warnings := withExecutionWarnings(ctx)
	environment = s.effectiveEnvironment(environment)
	fullRefresh = s.effectiveFullRefresh(ctx, environment, fullRefresh)
	relAssetPath, err := DecodeID(assetID)
	if err != nil {
		return MaterializeResult{Status: "error", Error: "invalid asset id", ExitCode: 1}
	}

	normalizedScope, scopeErr := NormalizeMaterializeScope(scope)
	if scopeErr != nil {
		return MaterializeResult{Status: "error", Error: scopeErr.Error(), ExitCode: 1}
	}
	if backfill {
		if normalizedScope != MaterializeScopeAsset {
			return MaterializeResult{Status: "error", Error: "backfill only supports a single asset", ExitCode: 1}
		}
		if s.deps.ResolveAssetByID == nil {
			return MaterializeResult{Status: "error", Error: "asset resolution is not available for backfill", ExitCode: 1}
		}
		_, _, asset, resolveErr := s.deps.ResolveAssetByID(ctx, assetID)
		if resolveErr != nil || asset == nil {
			return MaterializeResult{Status: "error", Error: "asset could not be resolved for backfill", ExitCode: 1}
		}
		if !matlog.BackfillSafe(asset) {
			return MaterializeResult{Status: "error", Error: "asset materialization is not safe to backfill by independent execution windows", ExitCode: 1}
		}
	}
	policyRequest := policy.RunRequest{
		Environment:          environment,
		Interactive:          true,
		Destructive:          fullRefresh || backfill,
		ConfirmedEnvironment: strings.TrimSpace(confirmedEnvironment),
	}
	if err := s.checkRunPolicy(policyRequest); err != nil {
		return MaterializeResult{Status: "error", Error: err.Error(), ExitCode: 1}
	}

	executionTime := time.Now().UTC()
	timeWindow, timeWindowErr := s.resolveAssetExecutionTimeWindow(ctx, assetID, startDate, endDate, executionTime)
	if timeWindowErr != nil {
		return MaterializeResult{Status: "error", Error: timeWindowErr.Error(), ExitCode: 1}
	}
	operation := withOperationTimeWindow(runOperation(relAssetPath, "", relAssetPath, environment), timeWindow)
	var output []byte
	assetIDsToRefresh := []string{assetID}
	materializedAssetIDs := []string{assetID}
	assetNamesToRecord := make([]string, 0, 1)
	selectedAssetIDs := []string{assetID}
	selectedAssetPaths := []string{relAssetPath}
	selectionReasons := []string{"explicit"}
	sensorMode = effectiveSensorMode(sensorMode, false)
	pipelineView, pipelineFound := s.findPipelineViewForAsset(assetID)
	pipelineID := strings.TrimSpace(pipelineView.ID)
	inlineLedger := s.inlineRunLedger()
	if normalizedScope != MaterializeScopeAsset {
		scoped, scopedErr := s.resolveMaterializeAssetScope(ctx, assetID, normalizedScope)
		if scopedErr != nil {
			return MaterializeResult{Status: "error", Error: scopedErr.Error(), ExitCode: 1}
		}
		operation = withOperationTimeWindow(scopedRunOperation(relAssetPath, scoped.PipelineID, relAssetPath, environment, string(normalizedScope), scoped.AssetPaths), timeWindow)
		assetIDsToRefresh = scoped.RefreshAssetIDs
		materializedAssetIDs = scoped.AssetIDs
		assetNamesToRecord = scoped.AssetNames
		selectedAssetIDs = scoped.AssetIDs
		selectedAssetPaths = scoped.AssetPaths
		selectionReasons = scoped.Reasons
		pipelineID = strings.TrimSpace(scoped.PipelineID)
		if strings.TrimSpace(pipelineView.Name) == "" {
			pipelineView.Name = scoped.PipelineName
		}
	} else if s.deps.ResolveAssetNameByID != nil {
		if assetName := strings.TrimSpace(s.deps.ResolveAssetNameByID(assetID)); assetName != "" {
			assetNamesToRecord = append(assetNamesToRecord, assetName)
		}
	}
	if pipelineID == "" && pipelineFound {
		pipelineID = strings.TrimSpace(pipelineView.ID)
	}
	if inlineLedger != nil && normalizedScope == MaterializeScopeAsset &&
		(pipelineID == "" || len(assetNamesToRecord) == 0) && s.deps.ResolveAssetByID != nil {
		_, parsed, selected, resolveErr := s.deps.ResolveAssetByID(ctx, assetID)
		if resolveErr == nil && parsed != nil && selected != nil {
			if pipelineID == "" {
				pipelineID = encodePipelineIDForParsedPipeline(s.deps.WorkspaceRoot, parsed)
			}
			if strings.TrimSpace(pipelineView.Name) == "" {
				pipelineView.Name = strings.TrimSpace(parsed.Name)
			}
			if len(assetNamesToRecord) == 0 && strings.TrimSpace(selected.Name) != "" {
				assetNamesToRecord = append(assetNamesToRecord, selected.Name)
				selectedAssetIDs[0] = encodePipelineAssetID(s.deps.WorkspaceRoot, selected)
				selectedAssetPaths[0] = assetRunPathForPipelineAsset(s.deps.WorkspaceRoot, selected)
			}
		}
	}

	completionID := uuid.NewString()
	var inlineRunID string
	inlineFinalized := false
	finishInline := func(status webscheduler.RunStatus, runErr error) error {
		if inlineLedger == nil || inlineRunID == "" || inlineFinalized {
			return nil
		}
		inlineFinalized = true
		return inlineLedger.FinishInlineRun(ctx, inlineRunID, status, runErr)
	}
	if inlineLedger != nil {
		if pipelineID == "" || len(assetNamesToRecord) != len(selectedAssetPaths) ||
			len(selectedAssetIDs) != len(selectedAssetPaths) || len(selectionReasons) != len(selectedAssetPaths) {
			return MaterializeResult{
				Status: "error", Operation: operation, ExitCode: 1, Warnings: warnings.snapshot(),
				Error: "admit durable inline run: selected asset provenance is incomplete",
			}
		}
		selectionUnits := make([]webscheduler.RunSelectionUnit, 0, len(selectedAssetPaths))
		for index, assetPath := range selectedAssetPaths {
			start, end := timeWindow.Start, timeWindow.End
			selectionUnits = append(selectionUnits, webscheduler.RunSelectionUnit{
				AssetID: selectedAssetIDs[index], AssetName: assetNamesToRecord[index], AssetPath: assetPath,
				Start: &start, End: &end, Reason: selectionReasons[index],
			})
		}
		pipelineTarget, targetErr := ResolvePipelineRunTarget(pipelineID)
		if targetErr != nil {
			return MaterializeResult{Status: "error", Operation: operation, Error: "admit durable inline run: invalid pipeline id", ExitCode: 1, Warnings: warnings.snapshot()}
		}
		admitted, admitErr := inlineLedger.AdmitInlineRun(ctx, webscheduler.InlineRunAdmission{
			PipelineID: pipelineID, PipelineUUID: strings.TrimSpace(pipelineView.UUID),
			PipelineName: inlinePipelineName(pipelineView, pipelineTarget, s.deps.WorkspaceRoot),
			Environment:  environment, Origin: ExecutionOrigin(ctx), Source: webscheduler.RunSourceWorkingTree,
			Start: timeWindow.Start, End: timeWindow.End, ExecutionTime: executionTime,
			FullRefresh: fullRefresh, Backfill: backfill, ConfirmedEnvironment: confirmedEnvironment,
			SensorMode: sensorMode,
			Selection: webscheduler.RunSelection{
				Mode: webscheduler.RunSelectionAsset, Scope: string(normalizedScope),
				AnchorAssetID: assetID, Units: selectionUnits,
			},
		})
		if admitErr != nil {
			return MaterializeResult{Status: "error", Operation: operation, Error: "admit durable inline run: " + admitErr.Error(), ExitCode: 1, Warnings: warnings.snapshot()}
		}
		inlineRunID = admitted.ID
		completionID = admitted.ID
		if startErr := inlineLedger.StartInlineRun(ctx, admitted.ID, time.Now().UTC()); startErr != nil {
			startErr = errors.Join(startErr, finishInline(webscheduler.RunStatusFailed, startErr))
			return MaterializeResult{Status: "error", Operation: operation, Error: "start durable inline run: " + startErr.Error(), ExitCode: 1, Warnings: warnings.snapshot()}
		}
		defer func() {
			if !inlineFinalized {
				_ = finishInline(webscheduler.RunStatusFailed, errors.New("inline execution ended before durable finalization"))
			}
		}()
	}

	assetEvent := func(event ExecutionAssetEvent) error {
		if inlineLedger == nil {
			return nil
		}
		if err := inlineLedger.RecordInlineRunStep(ctx, inlineRunID, schedulerRunStepEvent(event)); err != nil {
			return fmt.Errorf("persist inline execution step: %w", err)
		}
		return nil
	}
	streamChunk := onChunk
	if inlineLedger != nil {
		streamChunk = func(chunk []byte) {
			_ = inlineLedger.AppendInlineRunLog(ctx, inlineRunID, string(chunk))
			if onChunk != nil {
				onChunk(chunk)
			}
		}
	}
	observed := newPipelineRunObservation(assetEvent)
	observed.configureTargetWrites(ctx, completionID, s.deps.TargetWrites)
	targetsResolved := observed.captureExecutionTargets
	if inlineLedger != nil {
		targetsResolved = func(snapshot ExecutionTargetSnapshot) error {
			if err := inlineLedger.SetInlineRunExecutionTargetSnapshot(ctx, inlineRunID, schedulerExecutionTargetSnapshot(snapshot)); err != nil {
				return fmt.Errorf("persist inline execution targets: %w", err)
			}
			return observed.captureExecutionTargets(snapshot)
		}
	}
	releaseExecutionLease, leaseErr := s.acquireExecutionLease(ctx)
	if leaseErr != nil {
		leaseErr = errors.Join(leaseErr, finishInline(webscheduler.RunStatusFailed, leaseErr))
		return MaterializeResult{Status: "error", Operation: operation, Error: "acquire workspace execution lease: " + leaseErr.Error(), ExitCode: 1, Warnings: warnings.snapshot()}
	}
	defer func() { _ = releaseExecutionLease() }()
	if err := s.checkRunPolicy(policyRequest); err != nil {
		err = errors.Join(err, finishInline(webscheduler.RunStatusFailed, err))
		return MaterializeResult{Status: "error", Operation: operation, Error: err.Error(), ExitCode: 1, Warnings: warnings.snapshot()}
	}

	var combined bytes.Buffer
	var runErr error
	for position, assetPath := range selectedAssetPaths {
		started := time.Now().UTC()
		if inlineLedger != nil {
			if err := inlineLedger.RecordInlineRunUnit(ctx, inlineRunID, webscheduler.PipelineRunUnitEvent{
				Position: position, Status: webscheduler.PipelineRunUnitRunning, StartedAt: &started,
			}); err != nil {
				runErr = err
				break
			}
		}
		chunkOutput, assetErr := s.runSingleAssetMaterializationObserved(
			ctx, assetPath, environment, timeWindow, fullRefresh, sensorMode,
			streamChunk, observed.handle, targetsResolved, observed.beginTargetWrite,
		)
		if len(chunkOutput) > 0 {
			_, _ = combined.Write(chunkOutput)
		}
		if inlineLedger != nil {
			finished := time.Now().UTC()
			unitStatus := webscheduler.PipelineRunUnitSuccess
			unitError := ""
			if assetErr != nil {
				unitStatus = webscheduler.PipelineRunUnitFailed
				unitError = assetErr.Error()
				if executionWasCancelled(ctx, assetErr) {
					unitStatus = webscheduler.PipelineRunUnitCancelled
				}
			}
			if unitErr := inlineLedger.RecordInlineRunUnit(ctx, inlineRunID, webscheduler.PipelineRunUnitEvent{
				Position: position, Status: unitStatus, FinishedAt: &finished, Error: unitError,
			}); unitErr != nil {
				assetErr = errors.Join(assetErr, unitErr)
			}
		}
		if assetErr != nil {
			runErr = assetErr
			break
		}
	}
	output = combined.Bytes()
	var completionErr error

	changedAssetIDs := make([]string, 0)
	var materializedAt *time.Time
	now := time.Now().UTC()
	if pipelineFound || observed.pipelineUUID() != "" {
		completionStatus := "succeeded"
		if runErr != nil {
			completionStatus = "failed"
			if ctx.Err() != nil {
				completionStatus = "cancelled"
			}
		}
		runAssets, _ := observed.completedAssetsForNames(pipelineView, completionStatus, assetNamesToRecord)
		if len(runAssets) > 0 {
			pipelineUUID := observed.pipelineUUID()
			if pipelineUUID == "" {
				pipelineUUID = pipelineView.UUID
			}
			completionErr = s.emitRunCompletedForSpec(ctx, PipelineRunSpec{
				RunID:                   inlineRunID,
				CompletionID:            completionID,
				Environment:             environment,
				FullRefresh:             fullRefresh,
				executionTargetSnapshot: observed.executionTargetSnapshot(),
			}, pipelineUUID, timeWindow, now, runAssets)
			if completionErr != nil {
				completionErr = errors.Join(completionErr, observed.markSuccessfulTargetWritesDirty(now))
			}
		}
	}
	if inlineRunID != "" {
		terminalStatus := webscheduler.RunStatusSuccess
		terminalErr := errors.Join(runErr, completionErr)
		if terminalErr != nil {
			terminalStatus = webscheduler.RunStatusFailed
			if runErr != nil && executionWasCancelled(ctx, runErr) {
				terminalStatus = webscheduler.RunStatusCancelled
			}
		}
		if finishErr := finishInline(terminalStatus, terminalErr); finishErr != nil {
			completionErr = errors.Join(completionErr, fmt.Errorf("finalize durable inline run: %w", finishErr))
		}
	}
	if runErr == nil && completionErr == nil {
		materializedAt = &now
		changedAssetIDs = s.deps.FindInspectIDs(assetIDsToRefresh...)
	}

	status := "ok"
	errorMessage := ""
	exitCode := 0
	if runErr != nil || completionErr != nil {
		status = "error"
		if runErr != nil && executionWasCancelled(ctx, runErr) {
			status = "cancelled"
		}
		exitCode = 1
		if runErr == nil {
			errorMessage = "physical execution completed, but its durable completion could not be recorded: " + completionErr.Error()
		} else if completionErr != nil {
			errorMessage = errors.Join(runErr, fmt.Errorf("record durable completion: %w", completionErr)).Error()
		} else {
			errorMessage = runErr.Error()
		}
		if runErr != nil && IsDuckDBLockError(runErr, output) {
			errorMessage = "duckdb database is busy (lock held by another process), please retry"
		}
	}

	if runErr != nil {
		materializedAssetIDs = nil
	}

	return MaterializeResult{
		Status:          status,
		Operation:       operation,
		Output:          string(output),
		Error:           errorMessage,
		ExitCode:        exitCode,
		ChangedAssetIDs: coalesceMaterializedAssetIDs(changedAssetIDs, materializedAssetIDs),
		MaterializedAt:  materializedAt,
		Warnings:        warnings.snapshot(),
	}
}

func (s *ExecutionService) runSingleAssetMaterialization(ctx context.Context, assetPath, environment string, timeWindow ExecutionTimeWindow, fullRefresh bool, onChunk func([]byte)) ([]byte, error) {
	return s.runSingleAssetMaterializationWithSensorMode(ctx, assetPath, environment, timeWindow, fullRefresh, sensorModeOnce, onChunk)
}

func (s *ExecutionService) runSingleAssetMaterializationWithSensorMode(ctx context.Context, assetPath, environment string, timeWindow ExecutionTimeWindow, fullRefresh bool, sensorMode string, onChunk func([]byte)) ([]byte, error) {
	return s.runSingleAssetMaterializationObserved(ctx, assetPath, environment, timeWindow, fullRefresh, sensorMode, onChunk, nil, nil, nil)
}

func (s *ExecutionService) runScopedAssetMaterialization(ctx context.Context, assetPaths []string, environment string, timeWindow ExecutionTimeWindow, fullRefresh bool, sensorMode string, onChunk func([]byte)) ([]byte, error) {
	return s.runScopedAssetMaterializationObserved(ctx, assetPaths, environment, timeWindow, fullRefresh, sensorMode, onChunk, nil, nil, nil)
}

func (s *ExecutionService) runSingleAssetMaterializationObserved(
	ctx context.Context,
	assetPath, environment string,
	timeWindow ExecutionTimeWindow,
	fullRefresh bool,
	sensorMode string,
	onChunk func([]byte),
	onAssetEvent func(ExecutionAssetEvent) error,
	onTargetsResolved func(ExecutionTargetSnapshot) error,
	onTargetWriteStarting func(string) error,
) ([]byte, error) {
	return s.deps.Executor.RunAsset(ctx, RunAssetRequest{
		AssetPath:         assetPath,
		Environment:       environment,
		SensorMode:        effectiveSensorMode(sensorMode, false),
		StartDate:         timeWindow.StartRFC3339(),
		EndDate:           timeWindow.EndRFC3339(),
		AssetEvent:        onAssetEvent,
		FullRefresh:       fullRefresh,
		BeforeTargetWrite: onTargetWriteStarting,
		OnTargetsResolved: onTargetsResolved,
	}, onChunk)
}

func (s *ExecutionService) runScopedAssetMaterializationObserved(
	ctx context.Context,
	assetPaths []string,
	environment string,
	timeWindow ExecutionTimeWindow,
	fullRefresh bool,
	sensorMode string,
	onChunk func([]byte),
	onAssetEvent func(ExecutionAssetEvent) error,
	onTargetsResolved func(ExecutionTargetSnapshot) error,
	onTargetWriteStarting func(string) error,
) ([]byte, error) {
	var combined bytes.Buffer
	for _, assetPath := range assetPaths {
		chunkOutput, err := s.runSingleAssetMaterializationObserved(
			ctx, assetPath, environment, timeWindow, fullRefresh, sensorMode,
			onChunk, onAssetEvent, onTargetsResolved, onTargetWriteStarting,
		)
		if len(chunkOutput) > 0 {
			_, _ = combined.Write(chunkOutput)
		}
		if err != nil {
			return combined.Bytes(), err
		}
	}
	return combined.Bytes(), nil
}

type materializeAssetScopeResult struct {
	PipelineID      string
	PipelineName    string
	AssetIDs        []string
	AssetPaths      []string
	AssetNames      []string
	Reasons         []string
	RefreshAssetIDs []string
}

// NormalizeMaterializeScope validates an asset materialization selection and
// supplies the one-asset default used by both HTTP and service callers.
func NormalizeMaterializeScope(scope string) (MaterializeScope, error) {
	trimmed := strings.TrimSpace(scope)
	if trimmed == "" {
		return MaterializeScopeAsset, nil
	}
	value := MaterializeScope(trimmed)
	switch value {
	case MaterializeScopeAsset, MaterializeScopeAssetWithUpstreams, MaterializeScopeAssetWithDownstreams, MaterializeScopeAssetWithNeighborhood:
		return value, nil
	default:
		return "", fmt.Errorf("invalid materialize scope %q", scope)
	}
}

func coalesceMaterializedAssetIDs(changedAssetIDs, materializedAssetIDs []string) []string {
	if len(changedAssetIDs) > 0 {
		return changedAssetIDs
	}
	return materializedAssetIDs
}

func (s *ExecutionService) resolveMaterializeAssetScope(ctx context.Context, assetID string, scope MaterializeScope) (materializeAssetScopeResult, error) {
	if s.deps.ResolveAssetByID == nil {
		return materializeAssetScopeResult{}, fmt.Errorf("asset resolution is not available")
	}

	_, parsedPipeline, selectedAsset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil {
		return materializeAssetScopeResult{}, err
	}
	if parsedPipeline == nil || selectedAsset == nil {
		return materializeAssetScopeResult{}, fmt.Errorf("asset not found")
	}

	assetIDByName := make(map[string]string, len(parsedPipeline.Assets))
	assetPathByName := make(map[string]string, len(parsedPipeline.Assets))
	assetByName := make(map[string]*pipeline.Asset, len(parsedPipeline.Assets))
	downstreamByName := make(map[string][]string)
	for _, asset := range parsedPipeline.Assets {
		assetByName[asset.Name] = asset
		assetIDByName[asset.Name] = encodePipelineAssetID(s.deps.WorkspaceRoot, asset)
		assetPathByName[asset.Name] = assetRunPathForPipelineAsset(s.deps.WorkspaceRoot, asset)
		for _, upstream := range asset.Upstreams {
			upstreamName := strings.TrimSpace(upstream.Value)
			if upstreamName == "" {
				continue
			}
			downstreamByName[upstreamName] = append(downstreamByName[upstreamName], asset.Name)
		}
	}

	selected := make(map[string]struct{})
	queue := []string{selectedAsset.Name}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if _, seen := selected[name]; seen {
			continue
		}
		selected[name] = struct{}{}
		current := assetByName[name]
		if current == nil {
			continue
		}
		if scope == MaterializeScopeAssetWithUpstreams || scope == MaterializeScopeAssetWithNeighborhood {
			for _, upstream := range current.Upstreams {
				upstreamName := strings.TrimSpace(upstream.Value)
				if upstreamName == "" || assetByName[upstreamName] == nil {
					continue
				}
				queue = append(queue, upstreamName)
			}
		}
		if scope == MaterializeScopeAssetWithDownstreams || scope == MaterializeScopeAssetWithNeighborhood {
			for _, downstream := range downstreamByName[name] {
				if assetByName[downstream] == nil {
					continue
				}
				queue = append(queue, downstream)
			}
		}
	}

	orderedNames := make([]string, 0, len(parsedPipeline.Assets))
	for _, asset := range parsedPipeline.Assets {
		if _, ok := selected[asset.Name]; ok {
			orderedNames = append(orderedNames, asset.Name)
		}
	}
	if len(orderedNames) == 0 {
		return materializeAssetScopeResult{}, fmt.Errorf("asset scope is empty")
	}

	assetIDs := make([]string, 0, len(orderedNames))
	assetPaths := make([]string, 0, len(orderedNames))
	assetNames := make([]string, 0, len(orderedNames))
	reasons := make([]string, 0, len(orderedNames))
	upstreamNames := pipelinePlanUpstreamClosure(selectedAsset.Name, assetByName)
	downstreamNames := pipelinePlanDownstreamClosure(selectedAsset.Name, downstreamByName)
	for _, name := range orderedNames {
		assetIDs = append(assetIDs, assetIDByName[name])
		assetPaths = append(assetPaths, assetPathByName[name])
		assetNames = append(assetNames, name)
		reason := "explicit"
		if _, upstream := upstreamNames[name]; upstream {
			reason = "required_upstream"
		} else if _, downstream := downstreamNames[name]; downstream {
			reason = "selected_downstream"
		}
		reasons = append(reasons, reason)
	}

	refreshIDs := append([]string(nil), assetIDs...)
	return materializeAssetScopeResult{
		PipelineID:      encodePipelineIDForParsedPipeline(s.deps.WorkspaceRoot, parsedPipeline),
		PipelineName:    strings.TrimSpace(parsedPipeline.Name),
		AssetIDs:        assetIDs,
		AssetPaths:      assetPaths,
		AssetNames:      assetNames,
		Reasons:         reasons,
		RefreshAssetIDs: refreshIDs,
	}, nil
}

func assetRunPathForPipelineAsset(workspaceRoot string, asset *pipeline.Asset) string {
	assetPath := asset.ExecutableFile.Path
	if assetPath == "" {
		assetPath = asset.DefinitionFile.Path
	}
	relPath, err := filepath.Rel(workspaceRoot, assetPath)
	if err != nil {
		return filepath.ToSlash(assetPath)
	}
	return filepath.ToSlash(relPath)
}

func encodePipelineAssetID(workspaceRoot string, asset *pipeline.Asset) string {
	return EncodeID(assetRunPathForPipelineAsset(workspaceRoot, asset))
}

func encodePipelineIDForParsedPipeline(workspaceRoot string, parsed *pipeline.Pipeline) string {
	if parsed == nil {
		return ""
	}
	pipelinePath := parsed.DefinitionFile.Path
	if pipelinePath == "" {
		return ""
	}
	relPath, err := filepath.Rel(workspaceRoot, pipelinePath)
	if err != nil {
		return ""
	}
	return EncodeID(filepath.ToSlash(relPath))
}

func (s *ExecutionService) findMissingUpstreamAssets(ctx context.Context, assetID, rawOutput string) ([]string, []string) {
	if s.deps.ResolveAssetByID == nil || strings.TrimSpace(rawOutput) == "" {
		return nil, nil
	}

	_, parsedPipeline, asset, err := s.deps.ResolveAssetByID(ctx, assetID)
	if err != nil || parsedPipeline == nil || asset == nil {
		return nil, nil
	}

	missingObjectNames := extractMissingObjectNames(rawOutput)
	if len(missingObjectNames) == 0 {
		return nil, nil
	}

	assetIDs := make([]string, 0)
	assetNames := make([]string, 0)
	for _, upstream := range asset.Upstreams {
		upstreamName := strings.TrimSpace(upstream.Value)
		if upstreamName == "" {
			continue
		}
		if _, ok := missingObjectNames[normalizeMissingObjectIdentifier(upstreamName)]; !ok {
			continue
		}
		upstreamAsset := parsedPipeline.GetAssetByNameCaseInsensitive(upstreamName)
		if upstreamAsset == nil {
			continue
		}
		assetIDs = append(assetIDs, encodePipelineAssetID(s.deps.WorkspaceRoot, upstreamAsset))
		assetNames = append(assetNames, upstreamAsset.Name)
	}
	if len(assetIDs) == 0 {
		return nil, nil
	}
	return assetIDs, assetNames
}

var missingObjectPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)table with name ([a-zA-Z0-9_\.\"]+) does not exist`),
	regexp.MustCompile(`(?i)relation ([a-zA-Z0-9_\.\"]+) does not exist`),
	regexp.MustCompile(`(?i)no such table:?\s*([a-zA-Z0-9_\.\"]+)`),
	regexp.MustCompile(`(?i)object ([a-zA-Z0-9_\.\"]+) does not exist`),
}

func extractMissingObjectNames(rawOutput string) map[string]struct{} {
	result := make(map[string]struct{})
	for _, pattern := range missingObjectPatterns {
		matches := pattern.FindAllStringSubmatch(rawOutput, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			name := normalizeMissingObjectIdentifier(match[1])
			if name == "" {
				continue
			}
			result[name] = struct{}{}
		}
	}
	return result
}

func normalizeMissingObjectIdentifier(name string) string {
	return strings.ToLower(strings.Trim(strings.ReplaceAll(name, `"`, ""), " "))
}

func (s *ExecutionService) GetPipelineMaterialization(ctx context.Context, pipelineID, environment string) (PipelineMaterializationResponse, *APIError) {
	relPipelinePath, err := DecodeID(pipelineID)
	if err != nil {
		return PipelineMaterializationResponse{}, badRequestError("invalid_pipeline_id", "invalid pipeline id")
	}
	absPipelinePath, err := NewWorkspaceResolver(s.deps.WorkspaceRoot, nil).JoinPath(relPipelinePath)
	if err != nil {
		return PipelineMaterializationResponse{}, badRequestError("invalid_pipeline_path", err.Error())
	}
	parsed, err := s.deps.NewPipelineBuilder().CreatePipelineFromPath(ctx, absPipelinePath, pipeline.WithMutate())
	if err != nil {
		return PipelineMaterializationResponse{}, badRequestError("pipeline_parse_failed", err.Error())
	}

	matInfo := s.inspectPipelineMaterializations(ctx, parsed, environment)
	assets := make([]PipelineMaterializationState, 0, len(parsed.Assets))

	for _, asset := range parsed.Assets {
		assetPath := asset.ExecutableFile.Path
		if assetPath == "" {
			assetPath = asset.DefinitionFile.Path
		}

		relAssetPath, relErr := filepath.Rel(s.deps.WorkspaceRoot, assetPath)
		if relErr != nil {
			relAssetPath = assetPath
		}

		connectionName := ""
		if conn, connErr := targetConnectionNameForAsset(asset, parsed); connErr == nil {
			connectionName = conn
		}

		key := MaterializationAssetKey(asset.Name, connectionName)
		item := PipelineMaterializationState{
			AssetID:         EncodeID(filepath.ToSlash(relAssetPath)),
			Connection:      connectionName,
			DeclaredMatType: string(asset.Materialization.Type),
		}

		if info, ok := matInfo[key]; ok {
			item.IsMaterialized = info.IsMaterialized
			item.VerificationAvailable = info.VerificationAvailable
			item.MaterializedAs = info.MaterializedAs
			item.RowCount = info.RowCount
			if info.DeclaredMatType != "" {
				item.DeclaredMatType = info.DeclaredMatType
			}
		}

		assets = append(assets, item)
	}

	return PipelineMaterializationResponse{PipelineID: pipelineID, Assets: assets}, nil
}

func (s *ExecutionService) MaterializePipelineStream(ctx context.Context, pipelineID, environment string, dryRun, fullRefresh, backfill bool, startDate, endDate, confirmedEnvironment string, onChunk func([]byte)) MaterializeResult {
	return s.MaterializePipelineStreamWithSensorMode(ctx, pipelineID, environment, dryRun, fullRefresh, backfill, startDate, endDate, confirmedEnvironment, "", onChunk)
}

func (s *ExecutionService) MaterializePipelineStreamWithSensorMode(ctx context.Context, pipelineID, environment string, dryRun, fullRefresh, backfill bool, startDate, endDate, confirmedEnvironment, sensorMode string, onChunk func([]byte)) MaterializeResult {
	return s.MaterializePipelineRun(ctx, PipelineRunSpec{
		PipelineID:           pipelineID,
		Environment:          environment,
		SensorMode:           sensorMode,
		DryRun:               dryRun,
		FullRefresh:          fullRefresh,
		Backfill:             backfill,
		StartDate:            startDate,
		EndDate:              endDate,
		ConfirmedEnvironment: confirmedEnvironment,
	}, onChunk, nil)
}

func (s *ExecutionService) MaterializePipelineStreamWithAssetEvents(ctx context.Context, pipelineID, environment string, dryRun, fullRefresh, backfill bool, startDate, endDate, confirmedEnvironment string, onChunk func([]byte), onAssetEvent func(ExecutionAssetEvent) error) MaterializeResult {
	return s.MaterializePipelineRun(ctx, PipelineRunSpec{
		PipelineID:           pipelineID,
		Environment:          environment,
		DryRun:               dryRun,
		FullRefresh:          fullRefresh,
		Backfill:             backfill,
		StartDate:            startDate,
		EndDate:              endDate,
		ConfirmedEnvironment: confirmedEnvironment,
	}, onChunk, onAssetEvent)
}

// MaterializePipelineStreamForRun is the variant used by the scheduler: the
// run ID is threaded through so the RunCompleted bus event attributes
// materializations to the scheduler run record.
func (s *ExecutionService) MaterializePipelineStreamForRun(ctx context.Context, runID, pipelineID, environment string, dryRun, fullRefresh bool, startDate, endDate string, onChunk func([]byte), onAssetEvent func(ExecutionAssetEvent) error) MaterializeResult {
	return s.MaterializePipelineRun(ctx, PipelineRunSpec{
		RunID:       runID,
		PipelineID:  pipelineID,
		Environment: environment,
		Scheduled:   true,
		DryRun:      dryRun,
		FullRefresh: fullRefresh,
		StartDate:   startDate,
		EndDate:     endDate,
	}, onChunk, onAssetEvent)
}

// PipelineRunSpec describes one pipeline execution. When SnapshotDir is set
// the executor runs the materialized snapshot instead of the working tree;
// PipelineID still identifies the pipeline for events and asset listing.
type PipelineRunSpec struct {
	RunID string
	// CompletionID orders target-aware writes. Scheduler-backed runs use RunID;
	// inline executions receive a UUID before execution.
	CompletionID string
	PipelineID   string
	// PipelineUUID is the stable identity admitted with a scheduler RunSpec.
	// Snapshot execution must use it instead of re-resolving the mutable path.
	PipelineUUID string
	Environment  string
	// Scheduled is derived from the server-owned run origin. A queued manual
	// run also has a RunID, so RunID must not be used for this distinction.
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
	Plan                        *PipelineExecutionPlan
	// ConfigPath points the executor at .bruin.yml when the target directory
	// is outside the workspace git repository (snapshot runs).
	ConfigPath string
	// OnContextResolved persists the effective context after policy and source
	// normalization but before the first asset starts. A scheduler-backed run
	// uses it to make crash recovery preserve materialization semantics.
	OnContextResolved func(ResolvedPipelineRunContext) error
	// OnTargetsResolved persists the complete value-only pipeline snapshot
	// after effective configuration is selected and before the first task.
	OnTargetsResolved func(ExecutionTargetSnapshot) error
	// OnExecutionUnitsResolved persists a dynamically resolved full-pipeline
	// unit selection before any unit can start.
	OnExecutionUnitsResolved func([]PipelineExecutionUnit) error
	// OnUnit persists progress for one exact asset/window execution unit.
	OnUnit func(PipelineExecutionUnitEvent) error
	// executionTargetSnapshot is populated internally by the executor callback
	// and carried only on the synchronous completion event.
	executionTargetSnapshot *ExecutionTargetSnapshot
}

type PipelineExecutionPlan struct {
	Version        int
	SelectionMode  string
	MaxActiveSteps int
	Contracts      []PipelinePlanExecutionContract
	Prerequisites  []PipelinePlanPrerequisite
	Units          []PipelineExecutionUnit
}

type PipelineExecutionUnit struct {
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

func inlineRunSelection(plan *PipelineExecutionPlan) webscheduler.RunSelection {
	selection := webscheduler.RunSelection{Mode: webscheduler.RunSelectionAll}
	if plan == nil {
		return selection
	}
	switch strings.TrimSpace(plan.SelectionMode) {
	case PipelinePlanSelectionNeeded, PipelinePlanSelectionSelectorNeeded:
		selection.Mode = webscheduler.RunSelectionNeeded
	case PipelinePlanSelectionAsset:
		selection.Mode = webscheduler.RunSelectionAsset
	default:
		selection.Mode = webscheduler.RunSelectionAll
	}
	selection.Units = schedulerInlineRunExecutionUnits(plan.Units)
	return selection
}

func schedulerInlineRunExecutionUnits(
	units []PipelineExecutionUnit,
) []webscheduler.RunSelectionUnit {
	selectionUnits := make([]webscheduler.RunSelectionUnit, 0, len(units))
	for _, unit := range units {
		start, startErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(unit.StartDate))
		end, endErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(unit.EndDate))
		var startPointer, endPointer *time.Time
		if startErr == nil && endErr == nil && start.Before(end) {
			start, end = start.UTC(), end.UTC()
			startPointer, endPointer = &start, &end
		}
		selectionUnits = append(selectionUnits, webscheduler.RunSelectionUnit{
			AssetID: unit.AssetID, AssetName: unit.AssetName, AssetPath: unit.AssetPath,
			Start: startPointer, End: endPointer, Reason: unit.Reason,
		})
	}
	return selectionUnits
}

type PipelineExecutionUnitEvent struct {
	Position   int
	Status     string
	StartedAt  *time.Time
	FinishedAt *time.Time
	Error      string
}

type ResolvedPipelineRunContext struct {
	Environment string
	WinStart    time.Time
	WinEnd      time.Time
	FullRefresh bool
	Backfill    bool
	SensorMode  string
}

func (s *ExecutionService) MaterializePipelineRun(ctx context.Context, spec PipelineRunSpec, onChunk func([]byte), onAssetEvent func(ExecutionAssetEvent) error) MaterializeResult {
	contextInput := runcontext.Input{
		Start:       spec.StartDate,
		End:         spec.EndDate,
		FullRefresh: spec.FullRefresh,
		Backfill:    spec.Backfill,
		SensorMode:  spec.SensorMode,
	}
	normalizedContext, contextErr := runcontext.Normalize(contextInput)
	if contextErr != nil {
		return MaterializeResult{Status: "error", Error: contextErr.Error(), ExitCode: 1}
	}
	if contextErr := runcontext.ValidateDryRun(spec.DryRun, contextInput); contextErr != nil {
		return MaterializeResult{Status: "error", Error: contextErr.Error(), ExitCode: 1}
	}
	spec.StartDate = normalizedContext.StartString()
	spec.EndDate = normalizedContext.EndString()
	spec.SensorMode = normalizedContext.SensorMode
	executionTime := time.Now().UTC()
	if rawExecutionTime := strings.TrimSpace(spec.ExecutionTime); rawExecutionTime != "" {
		parsedExecutionTime, parseErr := time.Parse(time.RFC3339Nano, rawExecutionTime)
		if parseErr != nil {
			return MaterializeResult{Status: "error", Error: "invalid execution time", ExitCode: 1}
		}
		executionTime = parsedExecutionTime.UTC()
	}
	ctx, warnings := withExecutionWarnings(ctx)
	spec.Environment = s.effectiveEnvironment(spec.Environment)
	spec.FullRefresh = s.effectiveFullRefresh(ctx, spec.Environment, spec.FullRefresh)
	policyRequest := policy.RunRequest{
		Environment:          spec.Environment,
		Interactive:          !spec.Scheduled,
		SnapshotBased:        spec.SnapshotDir != "",
		Destructive:          !spec.DryRun && (spec.FullRefresh || spec.Backfill),
		ConfirmedEnvironment: strings.TrimSpace(spec.ConfirmedEnvironment),
	}
	if err := s.checkRunPolicy(policyRequest); err != nil {
		return MaterializeResult{Status: "error", Error: err.Error(), ExitCode: 1}
	}
	target, err := ResolvePipelineRunTarget(spec.PipelineID)
	if err != nil {
		return MaterializeResult{Status: "error", Error: "invalid pipeline id", ExitCode: 1}
	}
	if spec.SnapshotDir != "" {
		target = spec.SnapshotDir
	}

	timeWindow := ExecutionTimeWindow{}
	if !spec.DryRun {
		var timeWindowErr error
		timeWindow, timeWindowErr = s.resolvePipelineExecutionTimeWindow(ctx, spec.PipelineID, spec.SnapshotDir, spec.StartDate, spec.EndDate, executionTime)
		if timeWindowErr != nil {
			return MaterializeResult{Status: "error", Error: timeWindowErr.Error(), ExitCode: 1}
		}
	}
	operation := withOperationTimeWindow(runOperation(target, spec.PipelineID, "", spec.Environment), timeWindow)

	var inlineLedger InlineRunLedger
	var inlineRunID string
	inlineFinalized := false
	finishInline := func(status webscheduler.RunStatus, runErr error) error {
		if inlineLedger == nil || inlineRunID == "" || inlineFinalized {
			return nil
		}
		inlineFinalized = true
		return inlineLedger.FinishInlineRun(ctx, inlineRunID, status, runErr)
	}
	if !spec.DryRun && strings.TrimSpace(spec.RunID) == "" {
		inlineLedger = s.inlineRunLedger()
		if inlineLedger != nil {
			pipelineView, _ := s.findPipelineView(spec.PipelineID)
			pipelineUUID := strings.TrimSpace(spec.PipelineUUID)
			if pipelineUUID == "" {
				pipelineUUID = strings.TrimSpace(pipelineView.UUID)
			}
			pipelineName := inlinePipelineName(pipelineView, target, s.deps.WorkspaceRoot)
			source := webscheduler.RunSourceWorkingTree
			if strings.TrimSpace(spec.SnapshotDir) != "" || strings.TrimSpace(spec.SnapshotVersionID) != "" {
				source = webscheduler.RunSourceSnapshot
			}
			admitted, admitErr := inlineLedger.AdmitInlineRun(ctx, webscheduler.InlineRunAdmission{
				PipelineID:           spec.PipelineID,
				PipelineUUID:         pipelineUUID,
				PipelineName:         pipelineName,
				Environment:          spec.Environment,
				Origin:               ExecutionOrigin(ctx),
				Source:               source,
				SnapshotVersionID:    spec.SnapshotVersionID,
				Start:                timeWindow.Start,
				End:                  timeWindow.End,
				ExecutionTime:        executionTime,
				VariableOverrides:    spec.VariableOverrides,
				FullRefresh:          spec.FullRefresh,
				Backfill:             spec.Backfill,
				ConfirmedEnvironment: spec.ConfirmedEnvironment,
				SensorMode:           effectiveSensorMode(spec.SensorMode, false),
				Selection:            inlineRunSelection(spec.Plan),
			})
			if admitErr != nil {
				return MaterializeResult{
					Status: "error", Operation: operation, Error: "admit durable inline run: " + admitErr.Error(),
					ExitCode: 1, Warnings: warnings.snapshot(),
				}
			}
			inlineRunID = admitted.ID
			spec.RunID = admitted.ID
			spec.PipelineUUID = pipelineUUID
			if strings.TrimSpace(spec.CompletionID) == "" {
				spec.CompletionID = admitted.ID
			}
			if startErr := inlineLedger.StartInlineRun(ctx, admitted.ID, time.Now().UTC()); startErr != nil {
				startErr = errors.Join(startErr, finishInline(webscheduler.RunStatusFailed, startErr))
				return MaterializeResult{
					Status: "error", Operation: operation, Error: "start durable inline run: " + startErr.Error(),
					ExitCode: 1, Warnings: warnings.snapshot(),
				}
			}

			if spec.Plan == nil {
				existingUnitsResolved := spec.OnExecutionUnitsResolved
				spec.OnExecutionUnitsResolved = func(units []PipelineExecutionUnit) error {
					if len(units) > 0 {
						if err := inlineLedger.BindInlineRunExecutionUnits(
							ctx,
							inlineRunID,
							schedulerInlineRunExecutionUnits(units),
						); err != nil {
							return fmt.Errorf("persist inline execution units: %w", err)
						}
					}
					if existingUnitsResolved != nil {
						return existingUnitsResolved(units)
					}
					return nil
				}
			}
			existingTargetsResolved := spec.OnTargetsResolved
			spec.OnTargetsResolved = func(snapshot ExecutionTargetSnapshot) error {
				if err := inlineLedger.SetInlineRunExecutionTargetSnapshot(
					ctx, inlineRunID, schedulerExecutionTargetSnapshot(snapshot),
				); err != nil {
					return fmt.Errorf("persist inline execution targets: %w", err)
				}
				if existingTargetsResolved != nil {
					return existingTargetsResolved(snapshot)
				}
				return nil
			}
			existingUnitEvent := spec.OnUnit
			spec.OnUnit = func(event PipelineExecutionUnitEvent) error {
				if err := inlineLedger.RecordInlineRunUnit(ctx, inlineRunID, schedulerPipelineRunUnitEvent(event)); err != nil {
					return fmt.Errorf("persist inline execution unit: %w", err)
				}
				if existingUnitEvent != nil {
					return existingUnitEvent(event)
				}
				return nil
			}
			existingAssetEvent := onAssetEvent
			stepEvents := newPipelineAssetStepEvents(spec.Plan)
			onAssetEvent = func(event ExecutionAssetEvent) error {
				persist, err := stepEvents.shouldPersist(event)
				if err != nil {
					return fmt.Errorf("resolve inline execution step: %w", err)
				}
				if persist {
					if err := inlineLedger.RecordInlineRunStep(ctx, inlineRunID, schedulerRunStepEvent(event)); err != nil {
						return fmt.Errorf("persist inline execution step: %w", err)
					}
				}
				if existingAssetEvent != nil {
					return existingAssetEvent(event)
				}
				return nil
			}
			existingChunk := onChunk
			onChunk = func(chunk []byte) {
				_ = inlineLedger.AppendInlineRunLog(ctx, inlineRunID, string(chunk))
				if existingChunk != nil {
					existingChunk(chunk)
				}
			}
			defer func() {
				if !inlineFinalized {
					_ = finishInline(webscheduler.RunStatusFailed, errors.New("inline execution ended before durable finalization"))
				}
			}()
		}
	}
	if !spec.DryRun && strings.TrimSpace(spec.CompletionID) == "" {
		spec.CompletionID = strings.TrimSpace(spec.RunID)
		if spec.CompletionID == "" {
			spec.CompletionID = uuid.NewString()
		}
	}

	var releaseExecutionLease func() error
	if !spec.DryRun {
		releaseExecutionLease, err = s.acquireExecutionLease(ctx)
		if err != nil {
			runErr := fmt.Errorf("acquire workspace execution lease: %w", err)
			runErr = errors.Join(runErr, finishInline(webscheduler.RunStatusFailed, runErr))
			return MaterializeResult{Status: "error", Operation: operation, Error: runErr.Error(), ExitCode: 1, Warnings: warnings.snapshot()}
		}
		defer func() { _ = releaseExecutionLease() }()
		// Policy is evaluated again after admission and lease acquisition against
		// the same normalized context, immediately before executor side effects.
		if err := s.checkRunPolicy(policyRequest); err != nil {
			runErr := errors.Join(err, finishInline(webscheduler.RunStatusFailed, err))
			return MaterializeResult{Status: "error", Operation: operation, Error: runErr.Error(), ExitCode: 1, Warnings: warnings.snapshot()}
		}
	}
	observed := newPipelineRunObservation(onAssetEvent)
	observed.configureTargetWrites(ctx, spec.CompletionID, s.deps.TargetWrites)
	plannedExecution := spec.Plan != nil && strings.TrimSpace(spec.Plan.SelectionMode) != ""
	currentPipeline, _ := s.findPipelineView(spec.PipelineID)
	plannedChangedAssetIDs := make([]string, 0)
	var plannedCompletionErr error
	var plannedCompletionMu sync.Mutex
	sensorMode := effectiveSensorMode(spec.SensorMode, spec.Scheduled)
	if !spec.DryRun && spec.OnContextResolved != nil {
		if err := spec.OnContextResolved(ResolvedPipelineRunContext{
			Environment: spec.Environment,
			WinStart:    timeWindow.Start,
			WinEnd:      timeWindow.End,
			FullRefresh: spec.FullRefresh,
			Backfill:    spec.Backfill,
			SensorMode:  sensorMode,
		}); err != nil {
			return MaterializeResult{Status: "error", Error: "persist resolved run context: " + err.Error(), ExitCode: 1}
		}
	}
	unitEvent := func(event PipelineExecutionUnitEvent) error {
		if spec.OnUnit != nil {
			if err := spec.OnUnit(event); err != nil {
				return err
			}
		}
		if !plannedExecution {
			return nil
		}
		unit, ok := pipelineExecutionUnitAt(spec.Plan, event.Position)
		if !ok {
			return fmt.Errorf("planned execution unit %d is unavailable", event.Position)
		}
		unitCompletionID := pipelineExecutionUnitCompletionID(spec.CompletionID, event.Position)
		if strings.EqualFold(strings.TrimSpace(event.Status), "running") {
			observed.setAssetCompletionID(unit.AssetName, unitCompletionID)
			return nil
		}
		if !terminalPipelineExecutionUnitStatus(event.Status) {
			return nil
		}
		unitWindow, err := executionWindowForPlannedUnit(unit)
		if err != nil {
			return err
		}
		runAssets, succeededIDs := observed.takeCompletedAssetsForUnit(currentPipeline, unit.AssetName, event.Position)
		pipelineUUID := observed.pipelineUUID()
		if pipelineUUID == "" {
			pipelineUUID = strings.TrimSpace(spec.PipelineUUID)
		}
		if pipelineUUID == "" {
			pipelineUUID = currentPipeline.UUID
		}
		if len(runAssets) == 0 {
			observed.resetCompletedAsset(unit.AssetName)
			return nil
		}
		if pipelineUUID == "" {
			completedAt := time.Now().UTC()
			dirtyErr := observed.markSuccessfulTargetWritesDirty(completedAt)
			observed.resetCompletedAsset(unit.AssetName)
			return errors.Join(errors.New("planned execution pipeline identity is unavailable"), dirtyErr)
		}
		completedAt := time.Now().UTC()
		unitSpec := spec
		unitSpec.CompletionID = unitCompletionID
		completionErr := s.emitRunCompletedForSpec(ctx, unitSpec, pipelineUUID, unitWindow, completedAt, runAssets)
		if completionErr != nil {
			completionErr = errors.Join(completionErr, observed.markSuccessfulTargetWritesDirty(completedAt))
			plannedCompletionMu.Lock()
			plannedCompletionErr = errors.Join(plannedCompletionErr, completionErr)
			plannedCompletionMu.Unlock()
			observed.resetCompletedAsset(unit.AssetName)
			return fmt.Errorf("record durable completion for execution unit %d: %w", event.Position, completionErr)
		}
		plannedCompletionMu.Lock()
		plannedChangedAssetIDs = append(plannedChangedAssetIDs, succeededIDs...)
		plannedCompletionMu.Unlock()
		observed.resetCompletedAsset(unit.AssetName)
		return nil
	}
	request := RunPipelineRequest{
		Target:            target,
		Environment:       spec.Environment,
		SensorMode:        sensorMode,
		DryRun:            spec.DryRun,
		StartDate:         timeWindow.StartRFC3339(),
		EndDate:           timeWindow.EndRFC3339(),
		AssetEvent:        observed.handle,
		BeforeTargetWrite: observed.beginTargetWrite,
		RunID:             spec.RunID,
		OnTargetsResolved: func(snapshot ExecutionTargetSnapshot) error {
			if expected := strings.TrimSpace(spec.ExpectedConfigurationDigest); expected != "" &&
				(snapshot.ConfigurationFidelity != string(runcontext.IdentityFidelityExact) || snapshot.ConfigurationDigest != expected) {
				return fmt.Errorf("execution configuration changed after plan confirmation")
			}
			if spec.OnTargetsResolved != nil {
				if err := spec.OnTargetsResolved(snapshot); err != nil {
					return err
				}
			}
			if err := observed.captureExecutionTargets(snapshot); err != nil {
				return err
			}
			captured := snapshot
			spec.executionTargetSnapshot = &captured
			return nil
		},
		ConfigPath:               spec.ConfigPath,
		FullRefresh:              spec.FullRefresh,
		ExecutionTime:            executionTime,
		VariableOverrides:        spec.VariableOverrides,
		OnExecutionUnitsResolved: spec.OnExecutionUnitsResolved,
		UnitEvent:                unitEvent,
	}
	if spec.Plan != nil {
		request.PlanVersion = spec.Plan.Version
		request.SelectionMode = spec.Plan.SelectionMode
		request.MaxActiveSteps = spec.Plan.MaxActiveSteps
		request.ExecutionContracts = append(
			[]PipelinePlanExecutionContract(nil),
			spec.Plan.Contracts...,
		)
		request.Prerequisites = append([]PipelinePlanPrerequisite(nil), spec.Plan.Prerequisites...)
		request.ExecutionUnits = append([]PipelineExecutionUnit(nil), spec.Plan.Units...)
	}
	output, runErr := s.deps.Executor.RunPipeline(ctx, request, onChunk)

	changedAssetIDs := make([]string, 0)
	var materializedAt *time.Time
	var completionErr error
	if plannedExecution {
		plannedCompletionMu.Lock()
		changedAssetIDs = append(changedAssetIDs, plannedChangedAssetIDs...)
		completionErr = plannedCompletionErr
		plannedCompletionMu.Unlock()
		if runErr != nil && completionErr == nil {
			completionErr = observed.markSuccessfulTargetWritesDirty(time.Now().UTC())
		}
		if runErr == nil && completionErr == nil {
			now := time.Now().UTC()
			materializedAt = &now
		}
	} else if !spec.DryRun {
		now := time.Now().UTC()
		pipelineUUID := observed.pipelineUUID()
		if pipelineUUID == "" {
			pipelineUUID = strings.TrimSpace(spec.PipelineUUID)
		}
		if pipelineUUID == "" {
			pipelineUUID = currentPipeline.UUID
		}
		if pipelineUUID != "" {
			completionStatus := "succeeded"
			if runErr != nil {
				completionStatus = "failed"
				if ctx.Err() != nil {
					completionStatus = "cancelled"
				}
			}
			runAssets, succeededIDs := observed.completedAssets(currentPipeline, completionStatus)
			changedAssetIDs = append(changedAssetIDs, succeededIDs...)
			completionErr = s.emitRunCompletedForSpec(ctx, spec, pipelineUUID, timeWindow, now, runAssets)
			if completionErr != nil {
				completionErr = errors.Join(completionErr, observed.markSuccessfulTargetWritesDirty(now))
			}
			if runErr == nil && completionErr == nil {
				materializedAt = &now
			}
		}
	}
	if inlineRunID != "" {
		terminalStatus := webscheduler.RunStatusSuccess
		terminalErr := errors.Join(runErr, completionErr)
		if terminalErr != nil {
			terminalStatus = webscheduler.RunStatusFailed
			if runErr != nil && executionWasCancelled(ctx, runErr) {
				terminalStatus = webscheduler.RunStatusCancelled
			}
		}
		if finishErr := finishInline(terminalStatus, terminalErr); finishErr != nil {
			completionErr = errors.Join(completionErr, fmt.Errorf("finalize durable inline run: %w", finishErr))
		}
	}

	status := "ok"
	errorMessage := ""
	exitCode := 0
	if runErr != nil || completionErr != nil {
		status = "error"
		if runErr != nil && executionWasCancelled(ctx, runErr) {
			status = "cancelled"
		}
		exitCode = 1
		if runErr == nil {
			errorMessage = "physical execution completed, but its durable completion could not be recorded: " + completionErr.Error()
		} else if completionErr != nil {
			errorMessage = errors.Join(runErr, fmt.Errorf("record durable completion: %w", completionErr)).Error()
		} else {
			errorMessage = runErr.Error()
		}
	}

	return MaterializeResult{
		Status:          status,
		Operation:       operation,
		Output:          string(output),
		Error:           errorMessage,
		ExitCode:        exitCode,
		ChangedAssetIDs: changedAssetIDs,
		MaterializedAt:  materializedAt,
		Warnings:        warnings.snapshot(),
	}
}

func terminalPipelineExecutionUnitStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeeded", "failed", "cancelled", "canceled", "skipped":
		return true
	default:
		return false
	}
}

func pipelineExecutionUnitAt(plan *PipelineExecutionPlan, position int) (PipelineExecutionUnit, bool) {
	if plan == nil || position < 0 || position >= len(plan.Units) {
		return PipelineExecutionUnit{}, false
	}
	unit := plan.Units[position]
	if unit.Position != position {
		return PipelineExecutionUnit{}, false
	}
	return unit, true
}

func pipelineExecutionUnitCompletionID(base string, position int) string {
	return fmt.Sprintf("%s/unit/%d", strings.TrimSpace(base), position)
}

func executionWindowForPlannedUnit(unit PipelineExecutionUnit) (ExecutionTimeWindow, error) {
	start, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(unit.StartDate))
	if err != nil {
		return ExecutionTimeWindow{}, fmt.Errorf("planned execution unit %d has an invalid start time", unit.Position)
	}
	end, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(unit.EndDate))
	if err != nil || !start.Before(end) {
		return ExecutionTimeWindow{}, fmt.Errorf("planned execution unit %d has an invalid end time", unit.Position)
	}
	return ExecutionTimeWindow{Start: start.UTC(), End: end.UTC()}, nil
}

func executionWasCancelled(ctx context.Context, runErr error) bool {
	return errors.Is(runErr, context.Canceled) ||
		errors.Is(runErr, context.DeadlineExceeded) ||
		(ctx != nil && ctx.Err() != nil)
}

func (s *ExecutionService) acquireExecutionLease(ctx context.Context) (func() error, error) {
	if s.deps.AcquireExecutionLease == nil {
		return func() error { return nil }, nil
	}
	release, err := s.deps.AcquireExecutionLease(ctx)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, errors.New("execution lease returned a nil release function")
	}
	return release, nil
}

type pipelineRunObservation struct {
	mu                 sync.Mutex
	onEvent            func(ExecutionAssetEvent) error
	targetWriteCtx     context.Context
	targetWrites       TargetWriteStore
	completionID       string
	assetCompletionIDs map[string]string
	claims             map[string]matlog.TargetWriteClaim
	upstreamWriters    map[string]map[string]bus.UpstreamWriterSnapshot
	hasUpstreamReads   map[string]bool
	order              []string
	statuses           map[string]string
	startedAt          map[string]*time.Time
	finishedAt         map[string]*time.Time
	ordinals           map[string]int64
	terminal           map[string]bool
	qualityTerminal    map[string]map[string]string
	qualityFailures    map[string]map[string]bus.QualityCheckFailure
	nextOrdinal        int64
	executionTargets   ExecutionTargetSnapshot
}

func newPipelineRunObservation(onEvent func(ExecutionAssetEvent) error) *pipelineRunObservation {
	return &pipelineRunObservation{
		onEvent:            onEvent,
		claims:             make(map[string]matlog.TargetWriteClaim),
		upstreamWriters:    make(map[string]map[string]bus.UpstreamWriterSnapshot),
		hasUpstreamReads:   make(map[string]bool),
		statuses:           make(map[string]string),
		startedAt:          make(map[string]*time.Time),
		finishedAt:         make(map[string]*time.Time),
		ordinals:           make(map[string]int64),
		terminal:           make(map[string]bool),
		qualityTerminal:    make(map[string]map[string]string),
		qualityFailures:    make(map[string]map[string]bus.QualityCheckFailure),
		assetCompletionIDs: make(map[string]string),
	}
}

func (o *pipelineRunObservation) configureTargetWrites(ctx context.Context, completionID string, store TargetWriteStore) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.targetWriteCtx = ctx
	o.completionID = strings.TrimSpace(completionID)
	o.targetWrites = store
}

func (o *pipelineRunObservation) setAssetCompletionID(assetName, completionID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	assetName = strings.TrimSpace(assetName)
	if assetName == "" {
		return
	}
	o.assetCompletionIDs[assetName] = strings.TrimSpace(completionID)
}

func (o *pipelineRunObservation) handle(event ExecutionAssetEvent) error {
	assetName := strings.TrimSpace(event.Asset)
	if assetName == "" {
		return nil
	}
	if isQualityCheckTask(event.TaskKind) {
		o.observeQualityCheck(assetName, event)
		return nil
	}
	status := completedExecutionStatus(event.Status)
	running := strings.EqualFold(strings.TrimSpace(event.Status), "running")
	if status == "" && !running {
		return nil
	}
	if running {
		writers, captured, err := o.captureUpstreamWriterSnapshot(assetName)
		if err != nil {
			return err
		}
		event.UpstreamWriters = writers
		event.HasUpstreamWriterSnapshot = captured
	}
	o.mu.Lock()
	if _, exists := o.statuses[assetName]; !exists {
		o.order = append(o.order, assetName)
	}
	if event.StartedAt != nil && !event.StartedAt.IsZero() {
		started := event.StartedAt.UTC()
		if o.startedAt[assetName] == nil {
			o.startedAt[assetName] = &started
		}
		event.StartedAt = o.startedAt[assetName]
	}
	if status != "" {
		o.statuses[assetName] = status
		if !o.terminal[assetName] {
			o.terminal[assetName] = true
			o.ordinals[assetName] = o.nextOrdinal
			o.nextOrdinal++
			finished := time.Now().UTC()
			if event.FinishedAt != nil && !event.FinishedAt.IsZero() {
				finished = event.FinishedAt.UTC()
			}
			o.finishedAt[assetName] = &finished
		}
		ordinal := o.ordinals[assetName]
		event.CompletionOrdinal = &ordinal
		event.FinishedAt = o.finishedAt[assetName]
	} else if _, exists := o.statuses[assetName]; !exists {
		o.statuses[assetName] = ""
	}
	if !running {
		event.UpstreamWriters = cloneUpstreamWriterSnapshot(o.upstreamWriters[assetName])
		event.HasUpstreamWriterSnapshot = o.hasUpstreamReads[assetName]
	}
	o.mu.Unlock()

	if running {
		if o.onEvent != nil {
			if err := o.onEvent(event); err != nil {
				return err
			}
		}
		// The scheduler's running step is durable before the physical claim, but
		// the direct executor cannot start the task until this callback returns.
		// A claim failure therefore aborts safely without touching the target.
		return o.claimTargetWrite(assetName, event.StartedAt, false)
	}
	if status == "failed" || status == "cancelled" {
		// Persist physical uncertainty before forwarding the terminal scheduler
		// step. A step-store failure must not leave the previous writer trusted.
		if err := o.markTargetWriteDirty(assetName, event.FinishedAt); err != nil {
			return err
		}
	}
	// Observation happens before persistence forwarding so a physical success
	// remains represented truthfully even if terminal step persistence fails.
	if o.onEvent != nil {
		return o.onEvent(event)
	}
	return nil
}

func isQualityCheckTask(kind string) bool {
	switch strings.TrimSpace(kind) {
	case executionTaskKindColumnCheck, executionTaskKindCustomCheck:
		return true
	default:
		return false
	}
}

func (o *pipelineRunObservation) observeQualityCheck(assetName string, event ExecutionAssetEvent) {
	status := completedExecutionStatus(event.Status)
	if status == "" {
		return
	}
	name := strings.TrimSpace(event.CheckName)
	column := strings.TrimSpace(event.CheckColumn)
	kind := strings.TrimSpace(event.TaskKind)
	if name == "" || (kind == executionTaskKindColumnCheck && column == "") {
		return
	}
	key := kind + "\x00" + column + "\x00" + name

	o.mu.Lock()
	defer o.mu.Unlock()
	if o.qualityTerminal[assetName] == nil {
		o.qualityTerminal[assetName] = make(map[string]string)
	}
	o.qualityTerminal[assetName][key] = status
	if o.qualityFailures[assetName] == nil {
		o.qualityFailures[assetName] = make(map[string]bus.QualityCheckFailure)
	}
	if status != "failed" {
		delete(o.qualityFailures[assetName], key)
		return
	}
	checkKind := bus.QualityCheckKindCustom
	if kind == executionTaskKindColumnCheck {
		checkKind = bus.QualityCheckKindColumn
	}
	o.qualityFailures[assetName][key] = bus.QualityCheckFailure{
		Kind:     checkKind,
		Name:     name,
		Column:   column,
		Blocking: event.CheckBlocking,
	}
}

func (o *pipelineRunObservation) captureUpstreamWriterSnapshot(assetName string) (map[string]bus.UpstreamWriterSnapshot, bool, error) {
	o.mu.Lock()
	if o.hasUpstreamReads[assetName] {
		writers := cloneUpstreamWriterSnapshot(o.upstreamWriters[assetName])
		o.mu.Unlock()
		return writers, true, nil
	}
	store := o.targetWrites
	baseCtx := o.targetWriteCtx
	snapshot := o.executionTargets
	consumer, exists := snapshot.Entries[assetName]
	o.mu.Unlock()
	if store == nil {
		return nil, false, nil
	}
	if snapshot.Version < ExecutionTargetSnapshotVersion || !exists {
		return nil, false, fmt.Errorf("capture upstream physical writers for %s: execution target snapshot is unavailable", assetName)
	}

	targets := make([]string, 0, len(consumer.Upstreams))
	type expectedUpstream struct {
		target   ExecutionTargetSnapshotEntry
		reviewed ExecutionUpstreamSnapshot
	}
	upstreams := make(map[string]expectedUpstream, len(consumer.Upstreams))
	seenTargets := make(map[string]struct{}, len(consumer.Upstreams))
	for _, upstream := range consumer.Upstreams {
		if upstream.Required {
			if strings.TrimSpace(upstream.ResolvedAssetID) == "" ||
				strings.TrimSpace(upstream.TargetIdentity) == "" ||
				strings.TrimSpace(upstream.ExpectedFingerprint) == "" ||
				strings.TrimSpace(upstream.VarsHash) == "" ||
				upstream.TargetGeneration < 1 || strings.TrimSpace(upstream.CompletionID) == "" {
				return nil, false, fmt.Errorf("capture upstream physical writers for %s: reviewed prerequisite is incomplete", assetName)
			}
			if (strings.TrimSpace(upstream.ProducerPipelineUUID) == "") !=
				(strings.TrimSpace(upstream.ProducerSnapshotVersionID) == "") {
				return nil, false, fmt.Errorf("capture upstream physical writers for %s: producer deployment evidence is incomplete", assetName)
			}
			upstreams[upstream.ResolvedAssetID] = expectedUpstream{
				target: ExecutionTargetSnapshotEntry{
					AssetID: upstream.ResolvedAssetID, TargetIdentity: upstream.TargetIdentity,
					TargetFidelity: AssetRenderFidelityExact,
				},
				reviewed: upstream,
			}
			if _, seen := seenTargets[upstream.TargetIdentity]; !seen {
				seenTargets[upstream.TargetIdentity] = struct{}{}
				targets = append(targets, upstream.TargetIdentity)
			}
			continue
		}
		entry, inPipeline := snapshot.Entries[upstream.Value]
		if !inPipeline || entry.TargetFidelity != AssetRenderFidelityExact || entry.TargetIdentity == "" {
			continue
		}
		upstreams[entry.AssetID] = expectedUpstream{target: entry}
		if _, seen := seenTargets[entry.TargetIdentity]; !seen {
			seenTargets[entry.TargetIdentity] = struct{}{}
			targets = append(targets, entry.TargetIdentity)
		}
	}
	sort.Strings(targets)
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	writers, err := store.LatestWriters(baseCtx, targets)
	if err != nil {
		return nil, false, fmt.Errorf("capture upstream physical writers for %s: %w", assetName, err)
	}
	captured := make(map[string]bus.UpstreamWriterSnapshot, len(upstreams))
	for upstreamID, expected := range upstreams {
		target := expected.target
		writer, ok := writers[target.TargetIdentity]
		if expected.reviewed.Required {
			reviewed := expected.reviewed
			if !ok || writer.Ambiguous || writer.AssetID != upstreamID ||
				writer.TargetIdentity != target.TargetIdentity ||
				writer.Fingerprint != reviewed.ExpectedFingerprint || writer.VarsHash != reviewed.VarsHash ||
				writer.TargetGeneration != reviewed.TargetGeneration ||
				writer.CompletionID != reviewed.CompletionID ||
				writer.CompletionOrdinal != reviewed.CompletionOrdinal {
				return nil, false, fmt.Errorf(
					"capture upstream physical writers for %s: cross-pipeline prerequisite %q changed before execution",
					assetName,
					reviewed.Value,
				)
			}
		}
		if !ok || writer.Ambiguous || writer.AssetID != upstreamID || writer.TargetIdentity != target.TargetIdentity {
			continue
		}
		captured[upstreamID] = bus.UpstreamWriterSnapshot{
			AssetID: writer.AssetID, TargetIdentity: writer.TargetIdentity,
			Fingerprint: writer.Fingerprint, VarsHash: writer.VarsHash,
			TargetGeneration: writer.TargetGeneration, CompletionID: writer.CompletionID,
			CompletionOrdinal: writer.CompletionOrdinal, MaterializedAt: writer.MaterializedAt.UTC(),
		}
	}
	o.mu.Lock()
	o.upstreamWriters[assetName] = cloneUpstreamWriterSnapshot(captured)
	o.hasUpstreamReads[assetName] = true
	o.mu.Unlock()
	return captured, true, nil
}

func cloneUpstreamWriterSnapshot(source map[string]bus.UpstreamWriterSnapshot) map[string]bus.UpstreamWriterSnapshot {
	if source == nil {
		return nil
	}
	clone := make(map[string]bus.UpstreamWriterSnapshot, len(source))
	for assetID, writer := range source {
		clone[assetID] = writer
	}
	return clone
}

func (o *pipelineRunObservation) beginTargetWrite(assetName string) error {
	return o.claimTargetWrite(strings.TrimSpace(assetName), nil, true)
}

func (o *pipelineRunObservation) claimTargetWrite(assetName string, startedAt *time.Time, operatorReported bool) error {
	o.mu.Lock()
	store := o.targetWrites
	entry, captured := o.executionTargets.Entries[assetName]
	if store == nil || !captured || entry.TargetFidelity != AssetRenderFidelityExact || entry.TargetIdentity == "" {
		o.mu.Unlock()
		return nil
	}
	if entry.TargetWriteEvidenceRequired && !operatorReported {
		o.mu.Unlock()
		return nil
	}
	if _, exists := o.claims[assetName]; exists {
		o.mu.Unlock()
		return nil
	}
	claimedAt := time.Now().UTC()
	if startedAt != nil && !startedAt.IsZero() {
		claimedAt = startedAt.UTC()
	}
	claim := matlog.TargetWriteClaim{
		TargetIdentity: entry.TargetIdentity,
		CompletionID:   firstNonEmpty(o.assetCompletionIDs[assetName], o.completionID),
		AssetID:        entry.AssetID,
		ClaimedAt:      claimedAt,
	}
	claimCtx := o.targetWriteCtx
	o.claims[assetName] = claim
	o.mu.Unlock()

	if claimCtx == nil {
		claimCtx = context.Background()
	}
	if err := store.ClaimTargetWrite(claimCtx, claim); err != nil {
		o.mu.Lock()
		delete(o.claims, assetName)
		o.mu.Unlock()
		return fmt.Errorf("claim physical target for %s: %w", assetName, err)
	}
	return nil
}

func (o *pipelineRunObservation) markTargetWriteDirty(assetName string, finishedAt *time.Time) error {
	o.mu.Lock()
	store := o.targetWrites
	claim, claimed := o.claims[assetName]
	baseCtx := o.targetWriteCtx
	o.mu.Unlock()
	if store == nil || !claimed {
		return nil
	}
	at := time.Now().UTC()
	if finishedAt != nil && !finishedAt.IsZero() {
		at = finishedAt.UTC()
	}
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(baseCtx), 10*time.Second)
	defer cancel()
	if err := store.MarkTargetWriteClaimDirty(ctx, claim, at); err != nil && !errors.Is(err, matlog.ErrTargetWriteClaimNotFound) {
		return fmt.Errorf("mark physical target uncertain for %s: %w", assetName, err)
	}
	return nil
}

func (o *pipelineRunObservation) markSuccessfulTargetWritesDirty(at time.Time) error {
	o.mu.Lock()
	names := make([]string, 0, len(o.claims))
	for name := range o.claims {
		if o.statuses[name] == "succeeded" {
			names = append(names, name)
		}
	}
	o.mu.Unlock()
	sort.Strings(names)
	var errs []error
	for _, name := range names {
		finished := at
		o.mu.Lock()
		if recorded := o.finishedAt[name]; recorded != nil {
			finished = recorded.UTC()
		}
		o.mu.Unlock()
		if err := o.markTargetWriteDirty(name, &finished); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (o *pipelineRunObservation) captureExecutionTargets(snapshot ExecutionTargetSnapshot) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.executionTargets.Version != 0 {
		if !reflect.DeepEqual(o.executionTargets, snapshot) {
			return fmt.Errorf("execution target snapshot changed during the run")
		}
		return nil
	}
	o.executionTargets = snapshot
	return nil
}

func (o *pipelineRunObservation) completedAssets(view PipelineView, _ string) ([]bus.AssetRun, []string) {
	return o.completedAssetsForNames(view, "", nil)
}

func (o *pipelineRunObservation) completedAssetsForNames(view PipelineView, _ string, selectedNames []string) ([]bus.AssetRun, []string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	selected := make(map[string]bool, len(selectedNames))
	for _, name := range selectedNames {
		if name = strings.TrimSpace(name); name != "" {
			selected[name] = true
		}
	}
	restrictSelection := selectedNames != nil && len(selected) > 0
	assetsByName := make(map[string]AssetView, len(view.Assets))
	for _, asset := range view.Assets {
		assetsByName[asset.Name] = asset
	}

	runs := make([]bus.AssetRun, 0, len(o.order))
	succeededIDs := make([]string, 0, len(o.order))
	for _, name := range o.order {
		if restrictSelection && !selected[name] {
			continue
		}
		asset, existsInView := assetsByName[name]
		status := o.statuses[name]
		if status == "" || !o.terminal[name] {
			// Completion facts describe observed terminal main-task outcomes only.
			// A pipeline-level result cannot prove that an unobserved asset wrote.
			continue
		}
		entry, captured := o.executionTargets.Entries[name]
		assetID := entry.AssetID
		if !captured {
			if !existsInView || strings.TrimSpace(view.UUID) == "" {
				continue
			}
			assetID = identity.AssetID(view.UUID, name)
		}
		runs = append(runs, bus.AssetRun{
			AssetID:   assetID,
			AssetName: name,
			Status:    status,
		})
		run := &runs[len(runs)-1]
		failures := make([]bus.QualityCheckFailure, 0, len(o.qualityFailures[name]))
		for _, failure := range o.qualityFailures[name] {
			failures = append(failures, failure)
		}
		sort.Slice(failures, func(i, j int) bool {
			left, right := failures[i], failures[j]
			if left.Kind != right.Kind {
				return left.Kind < right.Kind
			}
			if left.Column != right.Column {
				return left.Column < right.Column
			}
			return left.Name < right.Name
		})
		switch {
		case len(failures) > 0:
			run.QualityStatus = bus.QualityStatusFailed
			run.FailedChecks = failures
		case asset.QualityCheckCount > 0 &&
			qualityChecksAllSucceeded(o.qualityTerminal[name], asset.QualityCheckCount):
			run.QualityStatus = bus.QualityStatusPassed
		}
		if started := o.startedAt[name]; started != nil {
			value := started.UTC()
			run.StartedAt = &value
		}
		if finished := o.finishedAt[name]; finished != nil {
			value := finished.UTC()
			run.FinishedAt = &value
		}
		if o.terminal[name] {
			run.CompletionOrdinal = o.ordinals[name]
			run.HasCompletionOrdinal = true
			run.UpstreamWriters = cloneUpstreamWriterSnapshot(o.upstreamWriters[name])
			run.HasUpstreamWriterSnapshot = o.hasUpstreamReads[name]
			if captured {
				run.TargetIdentity = entry.TargetIdentity
				run.TargetFidelity = string(entry.TargetFidelity)
				run.Fingerprint = entry.Fingerprint
				run.OwnContent = entry.OwnContent
				run.ConsumedVarsHash = entry.ConsumedVarsHash
				run.VarsHash = entry.VarsHash
			}
		}
		if status == "succeeded" && existsInView {
			succeededIDs = append(succeededIDs, asset.ID)
		}
	}
	return runs, succeededIDs
}

func (o *pipelineRunObservation) takeCompletedAssetsForUnit(
	view PipelineView,
	assetName string,
	completionOrdinal int,
) ([]bus.AssetRun, []string) {
	runs, succeededIDs := o.completedAssetsForNames(view, "", []string{assetName})
	for index := range runs {
		runs[index].CompletionOrdinal = int64(completionOrdinal)
		runs[index].HasCompletionOrdinal = true
	}
	return runs, succeededIDs
}

func (o *pipelineRunObservation) resetCompletedAsset(assetName string) {
	assetName = strings.TrimSpace(assetName)
	if assetName == "" {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	filtered := o.order[:0]
	for _, name := range o.order {
		if name != assetName {
			filtered = append(filtered, name)
		}
	}
	o.order = filtered
	delete(o.statuses, assetName)
	delete(o.startedAt, assetName)
	delete(o.finishedAt, assetName)
	delete(o.ordinals, assetName)
	delete(o.terminal, assetName)
	delete(o.qualityTerminal, assetName)
	delete(o.qualityFailures, assetName)
	delete(o.upstreamWriters, assetName)
	delete(o.hasUpstreamReads, assetName)
	delete(o.claims, assetName)
	delete(o.assetCompletionIDs, assetName)
}

func qualityChecksAllSucceeded(statuses map[string]string, expected int) bool {
	if expected <= 0 || len(statuses) < expected {
		return false
	}
	for _, status := range statuses {
		if status != "succeeded" {
			return false
		}
	}
	return true
}

func (o *pipelineRunObservation) pipelineUUID() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.TrimSpace(o.executionTargets.PipelineUUID)
}

func (o *pipelineRunObservation) executionTargetSnapshot() *ExecutionTargetSnapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.executionTargets.Version == 0 {
		return nil
	}
	snapshot := o.executionTargets
	return &snapshot
}

func completedExecutionStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeeded", "ok", "finished":
		return "succeeded"
	case "failed", "failure", "error", "errored":
		return "failed"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return ""
	}
}

func inlinePipelineName(view PipelineView, target, workspaceRoot string) string {
	if name := strings.TrimSpace(view.Name); name != "" {
		return name
	}
	cleaned := filepath.Clean(strings.TrimSpace(target))
	if cleaned == "." || cleaned == "" {
		if workspaceName := strings.TrimSpace(filepath.Base(filepath.Clean(workspaceRoot))); workspaceName != "" && workspaceName != "." {
			return workspaceName
		}
		return "workspace"
	}
	if name := strings.TrimSpace(filepath.Base(cleaned)); name != "" && name != "." {
		return name
	}
	return "pipeline"
}

func schedulerExecutionTargetSnapshot(snapshot ExecutionTargetSnapshot) webscheduler.ExecutionTargetSnapshot {
	entries := make(map[string]webscheduler.ExecutionTargetSnapshotEntry, len(snapshot.Entries))
	for assetName, entry := range snapshot.Entries {
		upstreams := make([]webscheduler.ExecutionUpstreamSnapshot, 0, len(entry.Upstreams))
		for _, upstream := range entry.Upstreams {
			upstreams = append(upstreams, webscheduler.ExecutionUpstreamSnapshot{
				Type: upstream.Type, Value: upstream.Value, Mode: upstream.Mode,
				ResolvedAssetID: upstream.ResolvedAssetID, Required: upstream.Required,
				ProducerPipelineUUID:      upstream.ProducerPipelineUUID,
				ProducerSnapshotVersionID: upstream.ProducerSnapshotVersionID,
				TargetIdentity:            upstream.TargetIdentity, ExpectedFingerprint: upstream.ExpectedFingerprint,
				VarsHash: upstream.VarsHash, TargetGeneration: upstream.TargetGeneration,
				CompletionID: upstream.CompletionID, CompletionOrdinal: upstream.CompletionOrdinal,
			})
		}
		entries[assetName] = webscheduler.ExecutionTargetSnapshotEntry{
			AssetID:                     entry.AssetID,
			ExternalSource:              entry.ExternalSource,
			TargetIdentity:              entry.TargetIdentity,
			TargetFidelity:              string(entry.TargetFidelity),
			TargetWriteEvidenceRequired: entry.TargetWriteEvidenceRequired,
			WriteResourceKind:           entry.WriteResourceKind,
			WriteResourceIdentity:       entry.WriteResourceIdentity,
			WriteResourceFidelity:       string(entry.WriteResourceFidelity),
			ExecutionContract:           schedulerPipelineRunExecutionContract(entry.ExecutionContract),
			Fingerprint:                 entry.Fingerprint,
			OwnContent:                  entry.OwnContent,
			ConsumedVarsHash:            entry.ConsumedVarsHash,
			VarsHash:                    entry.VarsHash,
			Upstreams:                   upstreams,
			CoverageMode:                string(entry.CoverageMode),
			RefreshRestricted:           entry.RefreshRestricted,
		}
	}
	return webscheduler.ExecutionTargetSnapshot{
		Version:               snapshot.Version,
		PipelineUUID:          snapshot.PipelineUUID,
		ConfigurationDigest:   snapshot.ConfigurationDigest,
		ConfigurationFidelity: snapshot.ConfigurationFidelity,
		Entries:               entries,
	}
}

func schedulerPipelineRunExecutionContract(
	contract PipelinePlanExecutionContract,
) webscheduler.PipelineRunExecutionContract {
	return webscheduler.PipelineRunExecutionContract{
		AssetID:               contract.AssetID,
		AssetName:             contract.AssetName,
		ConnectionKeys:        append([]string(nil), contract.ConnectionKeys...),
		MutationResources:     schedulerPipelineRunPlanResources(contract.MutationResources),
		CoordinationResources: schedulerPipelineRunPlanResources(contract.CoordinationResources),
	}
}

func busExecutionContract(
	contract PipelinePlanExecutionContract,
) bus.ExecutionContractSnapshot {
	return bus.ExecutionContractSnapshot{
		AssetID:        contract.AssetID,
		AssetName:      contract.AssetName,
		ConnectionKeys: append([]string(nil), contract.ConnectionKeys...),
		MutationResources: busExecutionResources(
			contract.MutationResources,
		),
		CoordinationResources: busExecutionResources(
			contract.CoordinationResources,
		),
	}
}

func busExecutionResources(resources PipelinePlanResources) bus.ExecutionResources {
	claims := make([]bus.ExecutionResourceClaim, 0, len(resources.Claims))
	for _, claim := range resources.Claims {
		claims = append(claims, bus.ExecutionResourceClaim{
			Kind: claim.Kind, Identity: claim.Identity,
		})
	}
	return bus.ExecutionResources{
		Isolation: resources.Isolation,
		Claims:    claims,
	}
}

func schedulerPipelineRunPlanResources(
	resources PipelinePlanResources,
) webscheduler.PipelineRunPlanResources {
	claims := make([]webscheduler.PipelineRunResourceClaim, 0, len(resources.Claims))
	for _, claim := range resources.Claims {
		claims = append(claims, webscheduler.PipelineRunResourceClaim{
			Kind: claim.Kind, Identity: claim.Identity,
		})
	}
	return webscheduler.PipelineRunPlanResources{
		Isolation: resources.Isolation,
		Claims:    claims,
	}
}

func schedulerRunStepEvent(event ExecutionAssetEvent) webscheduler.RunStepEvent {
	var upstreamWriters map[string]webscheduler.UpstreamWriterSnapshot
	if event.HasUpstreamWriterSnapshot {
		upstreamWriters = make(map[string]webscheduler.UpstreamWriterSnapshot, len(event.UpstreamWriters))
		for assetID, writer := range event.UpstreamWriters {
			upstreamWriters[assetID] = webscheduler.UpstreamWriterSnapshot{
				AssetID: writer.AssetID, TargetIdentity: writer.TargetIdentity,
				Fingerprint: writer.Fingerprint, VarsHash: writer.VarsHash,
				TargetGeneration: writer.TargetGeneration, CompletionID: writer.CompletionID,
				CompletionOrdinal: writer.CompletionOrdinal, MaterializedAt: writer.MaterializedAt,
			}
		}
	}
	return webscheduler.RunStepEvent{
		Asset: event.Asset, Status: schedulerRunStatus(event.Status),
		StartedAt: event.StartedAt, FinishedAt: event.FinishedAt, Error: event.Error,
		CompletionOrdinal: event.CompletionOrdinal, UpstreamWriters: upstreamWriters,
		HasUpstreamWriterSnapshot: event.HasUpstreamWriterSnapshot,
	}
}

func schedulerPipelineRunUnitEvent(event PipelineExecutionUnitEvent) webscheduler.PipelineRunUnitEvent {
	status := webscheduler.PipelineRunUnitRunning
	switch strings.ToLower(strings.TrimSpace(event.Status)) {
	case "success", "succeeded", "ok", "finished":
		status = webscheduler.PipelineRunUnitSuccess
	case "failed", "failure", "error", "errored":
		status = webscheduler.PipelineRunUnitFailed
	case "cancelled", "canceled":
		status = webscheduler.PipelineRunUnitCancelled
	case "skipped":
		status = webscheduler.PipelineRunUnitSkipped
	}
	return webscheduler.PipelineRunUnitEvent{
		Position: event.Position, Status: status,
		StartedAt: event.StartedAt, FinishedAt: event.FinishedAt, Error: event.Error,
	}
}

type pipelineAssetStepEvents struct {
	plan  *PipelineExecutionPlan
	first map[string]int
	last  map[string]int
}

func newPipelineAssetStepEvents(plan *PipelineExecutionPlan) *pipelineAssetStepEvents {
	events := &pipelineAssetStepEvents{
		plan:  plan,
		first: make(map[string]int),
		last:  make(map[string]int),
	}
	if plan == nil || plan.Version < PipelineExecutionPlanVersionV3 {
		return events
	}
	for position, unit := range plan.Units {
		if _, exists := events.first[unit.AssetName]; !exists {
			events.first[unit.AssetName] = position
		}
		events.last[unit.AssetName] = position
	}
	return events
}

// shouldPersist keeps one durable run step per asset while execution units
// retain the exact status of every asset/window pair. The first window opens
// the aggregate step, the last successful window closes it, and any failure or
// cancellation closes it immediately.
func (e *pipelineAssetStepEvents) shouldPersist(event ExecutionAssetEvent) (bool, error) {
	if e == nil || e.plan == nil || e.plan.Version < PipelineExecutionPlanVersionV3 {
		return true, nil
	}
	if !event.HasUnitPosition ||
		event.UnitPosition < 0 ||
		event.UnitPosition >= len(e.plan.Units) {
		return false, fmt.Errorf("execution asset %s has no confirmed unit position", event.Asset)
	}
	unit := e.plan.Units[event.UnitPosition]
	if unit.AssetName != event.Asset {
		return false, fmt.Errorf(
			"execution unit %d belongs to %s, not %s",
			event.UnitPosition, unit.AssetName, event.Asset,
		)
	}
	status := schedulerRunStatus(event.Status)
	switch status {
	case webscheduler.RunStatusRunning:
		return event.UnitPosition == e.first[event.Asset], nil
	case webscheduler.RunStatusSuccess:
		return event.UnitPosition == e.last[event.Asset], nil
	default:
		return true, nil
	}
}

func schedulerRunStatus(status string) webscheduler.RunStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "succeeded", "ok", "finished":
		return webscheduler.RunStatusSuccess
	case "failed", "failure", "error", "errored":
		return webscheduler.RunStatusFailed
	case "cancelled", "canceled":
		return webscheduler.RunStatusCancelled
	case "queued":
		return webscheduler.RunStatusQueued
	default:
		return webscheduler.RunStatusRunning
	}
}

func (s *ExecutionService) findPipelineView(pipelineID string) (PipelineView, bool) {
	if s.deps.CurrentPipelines == nil {
		return PipelineView{}, false
	}
	for _, view := range s.deps.CurrentPipelines() {
		if view.ID == pipelineID {
			return view, true
		}
	}
	return PipelineView{}, false
}

// ResolvePipelineRunTarget validates that the pipeline ID decodes to a
// runnable target path.
func (s *ExecutionService) ResolvePipelineRunTarget(pipelineID string) error {
	_, err := ResolvePipelineRunTarget(pipelineID)
	return err
}

func ResolvePipelineRunTarget(pipelineID string) (string, error) {
	relPath, err := DecodeID(pipelineID)
	if err != nil {
		return "", err
	}

	cleaned := filepath.Clean(relPath)
	base := strings.ToLower(filepath.Base(cleaned))
	if base == "pipeline.yml" || base == "pipeline.yaml" || base == ".pipeline.yml" || base == ".pipeline.yaml" {
		dir := filepath.Dir(cleaned)
		if dir == "." {
			return ".", nil
		}
		return filepath.ToSlash(dir), nil
	}

	return filepath.ToSlash(cleaned), nil
}

func (s *ExecutionService) resolveAssetExecutionTimeWindow(ctx context.Context, assetID, startDate, endDate string, executionTime time.Time) (ExecutionTimeWindow, error) {
	schedule := ""
	if s.deps.ResolveAssetByID != nil {
		_, parsedPipeline, _, err := s.deps.ResolveAssetByID(ctx, assetID)
		if err == nil && parsedPipeline != nil {
			schedule = string(parsedPipeline.Schedule)
		}
	}
	return ResolveExecutionTimeWindow(schedule, startDate, endDate, executionTime)
}

func (s *ExecutionService) resolvePipelineExecutionTimeWindow(ctx context.Context, pipelineID, snapshotDir, startDate, endDate string, executionTime time.Time) (ExecutionTimeWindow, error) {
	// Explicit bounds are already the authoritative execution context. They do
	// not depend on either source's pipeline schedule and must survive a pinned
	// run unchanged.
	if strings.TrimSpace(startDate) != "" || strings.TrimSpace(endDate) != "" {
		return ResolveExecutionTimeWindow("", startDate, endDate, executionTime)
	}

	// A pinned run executes the pipeline materialized in SnapshotDir. Resolve
	// its default interval from that same source rather than from a potentially
	// newer working-tree pipeline.
	if strings.TrimSpace(snapshotDir) != "" {
		schedule, err := readPipelineSchedule(snapshotDir)
		if err != nil {
			return ExecutionTimeWindow{}, fmt.Errorf("resolve deployed pipeline execution window: %w", err)
		}
		return ResolveExecutionTimeWindow(string(schedule), "", "", executionTime)
	}

	if target, err := ResolvePipelineRunTarget(pipelineID); err == nil && s.deps.NewPipelineBuilder != nil {
		absPipelinePath, joinErr := NewWorkspaceResolver(s.deps.WorkspaceRoot, nil).JoinPath(target)
		if joinErr == nil {
			if parsed, parseErr := s.deps.NewPipelineBuilder().CreatePipelineFromPath(ctx, absPipelinePath, pipeline.WithMutate()); parseErr == nil && parsed != nil {
				return ResolveExecutionTimeWindow(string(parsed.Schedule), startDate, endDate, executionTime)
			}
		}
	}
	return ResolveExecutionTimeWindow("", startDate, endDate, executionTime)
}

func readPipelineSchedule(pipelineDir string) (pipeline.Schedule, error) {
	for _, definitionFile := range PipelineDefinitionFiles {
		content, err := os.ReadFile(filepath.Join(pipelineDir, definitionFile))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}

		var definition struct {
			Schedule pipeline.Schedule `yaml:"schedule"`
		}
		if err := yaml.Unmarshal(content, &definition); err != nil {
			return "", err
		}
		if schedule := string(definition.Schedule); strings.Contains(schedule, "{{") || strings.Contains(schedule, "{%") {
			return "", fmt.Errorf("templated deployed pipeline schedule requires an explicit execution window")
		}
		return definition.Schedule, nil
	}
	return "", fmt.Errorf("pipeline definition was not found")
}

func (s *ExecutionService) inspectPipelineMaterializations(ctx context.Context, parsed *pipeline.Pipeline, environment string) map[string]PipelineMaterializationInfo {
	result := make(map[string]PipelineMaterializationInfo)

	assetsByConnection := make(map[string][]*pipeline.Asset)
	for _, asset := range parsed.Assets {
		if isSensorAssetType(asset.Type) {
			continue
		}
		conn, err := targetConnectionNameForAsset(asset, parsed)
		if err != nil || conn == "" {
			continue
		}
		assetsByConnection[conn] = append(assetsByConnection[conn], asset)
	}

	for connName, assets := range assetsByConnection {
		objects, err := s.fetchObjectsForConnection(ctx, connName, environment)
		if err != nil || len(objects) == 0 {
			for _, asset := range assets {
				key := MaterializationAssetKey(asset.Name, connName)
				result[key] = PipelineMaterializationInfo{
					AssetName:             asset.Name,
					Connection:            connName,
					VerificationAvailable: err == nil,
					DeclaredMatType:       string(asset.Materialization.Type),
				}
			}
			continue
		}

		wanted := make(map[string]struct{})
		for _, asset := range assets {
			wanted[NormalizeIdentifier(asset.Name)] = struct{}{}
			parts := strings.Split(NormalizeIdentifier(asset.Name), ".")
			if len(parts) > 1 {
				wanted[parts[len(parts)-1]] = struct{}{}
			}
		}

		candidateObjects := make([]DBObjectInfo, 0)
		for _, object := range objects {
			if _, ok := wanted[NormalizeIdentifier(object.QualifiedName)]; ok {
				candidateObjects = append(candidateObjects, object)
				continue
			}
			if _, ok := wanted[NormalizeIdentifier(object.Name)]; ok {
				candidateObjects = append(candidateObjects, object)
			}
		}

		tableObjects := make([]DBObjectInfo, 0, len(candidateObjects))
		for _, object := range candidateObjects {
			if object.Kind == "table" {
				tableObjects = append(tableObjects, object)
			}
		}

		rowCounts := s.fetchRowCountsForObjects(ctx, connName, environment, tableObjects)

		objectsByName := make(map[string]DBObjectInfo)
		for _, object := range objects {
			objectsByName[NormalizeIdentifier(object.QualifiedName)] = object
			objectsByName[NormalizeIdentifier(object.Name)] = object
		}

		for _, asset := range assets {
			normalized := NormalizeIdentifier(asset.Name)
			object, ok := objectsByName[normalized]
			if !ok {
				parts := strings.Split(normalized, ".")
				if len(parts) > 1 {
					object, ok = objectsByName[parts[len(parts)-1]]
				}
			}

			key := MaterializationAssetKey(asset.Name, connName)
			item := PipelineMaterializationInfo{
				AssetName:             asset.Name,
				Connection:            connName,
				VerificationAvailable: true,
				DeclaredMatType:       string(asset.Materialization.Type),
			}

			if ok {
				item.IsMaterialized = true
				item.MaterializedAs = object.Kind

				if count, hasCount := rowCounts[NormalizeIdentifier(object.QualifiedName)]; hasCount {
					c := count
					item.RowCount = &c
				} else if count, hasCount := rowCounts[NormalizeIdentifier(object.Name)]; hasCount {
					c := count
					item.RowCount = &c
				}
			}

			result[key] = item
		}
	}

	return result
}

func (s *ExecutionService) runConnectionQuery(ctx context.Context, connectionName, query string) ([]string, []map[string]any, error) {
	return s.RunConnectionQueryForEnvironment(ctx, connectionName, "", query)
}

func (s *ExecutionService) RunConnectionQueryForEnvironment(ctx context.Context, connectionName, environment, query string) ([]string, []map[string]any, error) {
	output, err := s.deps.Executor.QueryConnection(ctx, QueryConnectionRequest{
		ConnectionName: connectionName,
		Query:          query,
		Environment:    environment,
		Output:         "json",
	})
	if err != nil {
		return nil, nil, fmt.Errorf("query failed for connection '%s': %w", connectionName, err)
	}

	columns, rows := ParseQueryJSONOutput(output)
	return columns, rows, nil
}

func ReadStringField(row map[string]any, keys ...string) string {
	for _, key := range keys {
		for rowKey, value := range row {
			if strings.EqualFold(rowKey, key) {
				s, ok := value.(string)
				if ok {
					return s
				}
			}
		}
	}
	return ""
}

func ReadInt64Field(row map[string]any, key string) (int64, bool) {
	for rowKey, value := range row {
		if !strings.EqualFold(rowKey, key) {
			continue
		}

		switch v := value.(type) {
		case int:
			return int64(v), true
		case int64:
			return v, true
		case float64:
			return int64(v), true
		case string:
			trimmed := strings.TrimSpace(v)
			if trimmed == "" {
				return 0, false
			}
			var parsed int64
			_, err := fmt.Sscan(trimmed, &parsed)
			if err == nil {
				return parsed, true
			}
		}
	}

	return 0, false
}

func maxTimePtr(a, b *time.Time) *time.Time {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if b.After(*a) {
		return b
	}
	return a
}
