package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/google/uuid"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"renart/internal/web/bus"
	"renart/internal/web/matlog"
	"renart/internal/web/policy"
)

type stubTargetWriteStore struct {
	claim  func(context.Context, matlog.TargetWriteClaim) error
	dirty  func(context.Context, matlog.TargetWriteClaim, time.Time) error
	latest func(context.Context, []string) (map[string]matlog.LatestSuccessfulWriter, error)
	claims []matlog.TargetWriteClaim
	dirtys []matlog.TargetWriteClaim
}

func TestPipelineRunObservationRequiresExactReviewedCrossPipelineWriter(t *testing.T) {
	materializedAt := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	writer := matlog.LatestSuccessfulWriter{
		TargetIdentity: "target", TargetGeneration: 7,
		AssetID: "producer:raw.orders", Environment: "default",
		Fingerprint: "v3:producer", VarsHash: "vars",
		CompletionID: "producer-run", CompletionOrdinal: 2,
		MaterializedAt: materializedAt,
	}
	store := &stubTargetWriteStore{latest: func(context.Context, []string) (map[string]matlog.LatestSuccessfulWriter, error) {
		return map[string]matlog.LatestSuccessfulWriter{"target": writer}, nil
	}}
	observation := newPipelineRunObservation(nil)
	observation.targetWrites = store
	observation.targetWriteCtx = context.Background()
	require.NoError(t, observation.captureExecutionTargets(ExecutionTargetSnapshot{
		Version: ExecutionTargetSnapshotVersion,
		Entries: map[string]ExecutionTargetSnapshotEntry{
			"analytics.orders": {
				AssetID: "consumer:analytics.orders",
				Upstreams: []ExecutionUpstreamSnapshot{{
					Type: "uri", Value: "duckdb://warehouse/raw/orders", Mode: "full",
					ResolvedAssetID: writer.AssetID, Required: true,
					TargetIdentity: writer.TargetIdentity, ExpectedFingerprint: writer.Fingerprint,
					VarsHash: writer.VarsHash, TargetGeneration: writer.TargetGeneration,
					CompletionID: writer.CompletionID, CompletionOrdinal: writer.CompletionOrdinal,
				}},
			},
		},
	}))

	captured, present, err := observation.captureUpstreamWriterSnapshot("analytics.orders")
	require.NoError(t, err)
	assert.True(t, present)
	require.Contains(t, captured, writer.AssetID)
	assert.Equal(t, writer.TargetGeneration, captured[writer.AssetID].TargetGeneration)

	changed := newPipelineRunObservation(nil)
	changed.targetWrites = &stubTargetWriteStore{latest: func(context.Context, []string) (map[string]matlog.LatestSuccessfulWriter, error) {
		newer := writer
		newer.TargetGeneration++
		newer.CompletionID = "newer-run"
		return map[string]matlog.LatestSuccessfulWriter{"target": newer}, nil
	}}
	changed.targetWriteCtx = context.Background()
	require.NoError(t, changed.captureExecutionTargets(observation.executionTargets))
	_, _, err = changed.captureUpstreamWriterSnapshot("analytics.orders")
	assert.ErrorContains(t, err, "changed before execution")
}

func (s *stubTargetWriteStore) LatestWriters(ctx context.Context, targets []string) (map[string]matlog.LatestSuccessfulWriter, error) {
	if s.latest != nil {
		return s.latest(ctx, targets)
	}
	return map[string]matlog.LatestSuccessfulWriter{}, nil
}

func (s *stubTargetWriteStore) ClaimTargetWrite(ctx context.Context, claim matlog.TargetWriteClaim) error {
	s.claims = append(s.claims, claim)
	if s.claim != nil {
		return s.claim(ctx, claim)
	}
	return nil
}

func (s *stubTargetWriteStore) MarkTargetWriteClaimDirty(ctx context.Context, claim matlog.TargetWriteClaim, at time.Time) error {
	s.dirtys = append(s.dirtys, claim)
	if s.dirty != nil {
		return s.dirty(ctx, claim, at)
	}
	return nil
}

func newExecutionTestResolver(workspaceRoot string) *WorkspaceResolver {
	return NewWorkspaceResolver(workspaceRoot, func(ctx context.Context, pipelinePath string) (*pipeline.Pipeline, error) {
		osFS := afero.NewOsFs()
		builder := pipeline.NewBuilder(
			BuilderConfig,
			pipeline.CreateTaskFromYamlDefinition(osFS),
			pipeline.CreateTaskFromFileComments(osFS),
			osFS,
			DefaultGlossaryReader,
			jinja.VariantRendererFactory,
		)
		return builder.CreatePipelineFromPath(ctx, pipelinePath, pipeline.WithMutate())
	})
}

type stubExecutionExecutor struct {
	runAssetOutput           []byte
	runAssetErr              error
	runAssetChunks           [][]byte
	runAssetEvents           []ExecutionAssetEvent
	runAssetTargets          *ExecutionTargetSnapshot
	runAssetRequests         []RunAssetRequest
	runPipelineOutput        []byte
	runPipelineErr           error
	runPipelineChunks        [][]byte
	runPipelineEvents        []ExecutionAssetEvent
	runPipelineTargets       *ExecutionTargetSnapshot
	runPipelineResolvedUnits []PipelineExecutionUnit
	runPipelineReqs          []RunPipelineRequest
	runPipelineExecute       func(RunPipelineRequest) error
	queryConnOutput          []byte
	queryConnErr             error
	queryConnReqs            []QueryConnectionRequest
	queryConnection          func(QueryConnectionRequest) ([]byte, error)
	runWithRetry             func(context.Context, QueryAssetRequest, int, time.Duration) ([]byte, error, int)
	onRunAsset               func()
	onRunPipeline            func()
}

func (s *stubExecutionExecutor) RunAsset(_ context.Context, req RunAssetRequest, onChunk func([]byte)) ([]byte, error) {
	if s.onRunAsset != nil {
		s.onRunAsset()
	}
	s.runAssetRequests = append(s.runAssetRequests, req)
	for _, chunk := range s.runAssetChunks {
		if onChunk != nil {
			onChunk(chunk)
		}
	}
	if s.runAssetTargets != nil && req.OnTargetsResolved != nil {
		snapshot := executionTestSnapshotWithResources(*s.runAssetTargets)
		if err := req.OnTargetsResolved(snapshot); err != nil {
			return s.runAssetOutput, err
		}
	}
	for _, event := range s.runAssetEvents {
		if req.AssetEvent != nil {
			if err := req.AssetEvent(event); err != nil {
				return s.runAssetOutput, err
			}
		}
	}
	return s.runAssetOutput, s.runAssetErr
}

func (s *stubExecutionExecutor) RunPipeline(_ context.Context, req RunPipelineRequest, onChunk func([]byte)) ([]byte, error) {
	if s.onRunPipeline != nil {
		s.onRunPipeline()
	}
	s.runPipelineReqs = append(s.runPipelineReqs, req)
	for _, chunk := range s.runPipelineChunks {
		if onChunk != nil {
			onChunk(chunk)
		}
	}
	if len(s.runPipelineResolvedUnits) > 0 && req.OnExecutionUnitsResolved != nil {
		if err := req.OnExecutionUnitsResolved(s.runPipelineResolvedUnits); err != nil {
			return s.runPipelineOutput, err
		}
	}
	if s.runPipelineTargets != nil && req.OnTargetsResolved != nil {
		snapshot := executionTestSnapshotWithResources(*s.runPipelineTargets)
		if err := req.OnTargetsResolved(snapshot); err != nil {
			return s.runPipelineOutput, err
		}
	}
	if s.runPipelineExecute != nil {
		if err := s.runPipelineExecute(req); err != nil {
			return s.runPipelineOutput, err
		}
		return s.runPipelineOutput, s.runPipelineErr
	}
	for _, event := range s.runPipelineEvents {
		if req.AssetEvent != nil {
			if err := req.AssetEvent(event); err != nil {
				return s.runPipelineOutput, err
			}
		}
	}
	return s.runPipelineOutput, s.runPipelineErr
}

func executionTestSnapshotWithResources(snapshot ExecutionTargetSnapshot) ExecutionTargetSnapshot {
	if snapshot.Version < ExecutionTargetSnapshotVersion {
		return snapshot
	}
	entries := make(map[string]ExecutionTargetSnapshotEntry, len(snapshot.Entries))
	for name, entry := range snapshot.Entries {
		if entry.WriteResourceKind == "" && entry.WriteResourceFidelity == "" {
			entry.WriteResourceKind = assetWriteResourcePipeline
			entry.WriteResourceFidelity = AssetRenderFidelityRuntimeOnly
		}
		if entry.ExecutionContract.AssetID == "" && entry.ExecutionContract.AssetName == "" {
			mutation := pipelineExclusiveResources()
			if entry.WriteResourceFidelity == AssetRenderFidelityExact {
				mutation = PipelinePlanResources{
					Isolation: PipelinePlanResourceIsolationResources,
					Claims:    []PipelinePlanResourceClaim{},
				}
				if entry.WriteResourceKind != assetWriteResourceNone {
					mutation.Claims = append(mutation.Claims, PipelinePlanResourceClaim{
						Kind: entry.WriteResourceKind, Identity: entry.WriteResourceIdentity,
					})
				}
			}
			entry.ExecutionContract = PipelinePlanExecutionContract{
				AssetID: entry.AssetID, AssetName: name, ConnectionKeys: []string{},
				MutationResources: mutation, CoordinationResources: clonePipelinePlanResources(mutation),
			}
		}
		entries[name] = entry
	}
	snapshot.Entries = entries
	return snapshot
}

func (s *stubExecutionExecutor) QueryAsset(context.Context, QueryAssetRequest) ([]byte, error) {
	return nil, nil
}

func (s *stubExecutionExecutor) QueryConnection(_ context.Context, req QueryConnectionRequest) ([]byte, error) {
	s.queryConnReqs = append(s.queryConnReqs, req)
	if s.queryConnection != nil {
		return s.queryConnection(req)
	}
	return s.queryConnOutput, s.queryConnErr
}

func (s *stubExecutionExecutor) FormatAsset(context.Context, FormatAssetRequest) ([]byte, error) {
	return nil, nil
}

func (s *stubExecutionExecutor) ApplyPatch(context.Context, PatchRequest) ([]byte, error) {
	return nil, nil
}

func (s *stubExecutionExecutor) ImportDatabase(context.Context, ImportDatabaseRequest) ([]byte, error) {
	return nil, nil
}

func (s *stubExecutionExecutor) RunWithRetry(ctx context.Context, req QueryAssetRequest, retries int, delay time.Duration) ([]byte, error, int) {
	if s.runWithRetry != nil {
		return s.runWithRetry(ctx, req, retries, delay)
	}
	return nil, nil, 0
}

func TestExecutionServiceMaterializeAssetStreamPreservesSuccessOutput(t *testing.T) {
	t.Parallel()

	assetID := EncodeID("pipelines/orders/assets/orders.sql")
	started := time.Date(2026, 7, 17, 11, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	executor := &stubExecutionExecutor{
		runAssetOutput: []byte("asset run complete\n"),
		runAssetChunks: [][]byte{[]byte("asset "), []byte("run complete\n")},
		runAssetEvents: []ExecutionAssetEvent{
			{Asset: "analytics.orders", Status: "running", StartedAt: &started},
			{Asset: "analytics.orders", Status: "success", StartedAt: &started, FinishedAt: &finished},
		},
		runAssetTargets: &ExecutionTargetSnapshot{
			Version: ExecutionTargetSnapshotVersion, PipelineUUID: "orders-uuid",
			Entries: map[string]ExecutionTargetSnapshotEntry{
				"analytics.orders": {
					AssetID: "orders-uuid:analytics.orders", TargetIdentity: "target-orders",
					TargetFidelity: AssetRenderFidelityExact, Fingerprint: "v2:orders",
					OwnContent: "v2:orders-own", ConsumedVarsHash: "consumed", VarsHash: "vars", CoverageMode: ExecutionCoverageMarker,
				},
			},
		},
	}
	streamed := make([]string, 0)
	events := bus.New()
	var completed bus.RunCompleted
	events.OnRunCompleted(func(event bus.RunCompleted) error { completed = event; return nil })

	svc := NewExecutionService(ExecutionDependencies{
		ConfigPath: "/path/that/does/not/exist",
		Executor:   executor,
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "pipelines/orders/assets/orders.sql", &pipeline.Pipeline{}, &pipeline.Asset{Connection: "warehouse"}, nil
		},
		ResolveAssetNameByID: func(string) string { return "analytics.orders" },
		FindInspectIDs:       func(...string) []string { return []string{"inspect-1", "inspect-2"} },
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{
				ID:     EncodeID("pipelines/orders/pipeline.yml"),
				UUID:   "orders-uuid",
				Assets: []AssetView{{ID: assetID, Name: "analytics.orders"}},
			}}
		},
		Events: events,
	})

	result := svc.MaterializeAssetStream(context.Background(), assetID, "", "", "", "", false, false, "", func(chunk []byte) {
		streamed = append(streamed, string(chunk))
	})

	require.Len(t, executor.runAssetRequests, 1)
	assert.Equal(t, "pipelines/orders/assets/orders.sql", executor.runAssetRequests[0].AssetPath)
	assert.Equal(t, sensorModeOnce, executor.runAssetRequests[0].SensorMode)
	assert.Equal(t, "ok", result.Status)
	assert.Equal(t, "asset run complete\n", result.Output)
	assert.Empty(t, result.Error)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "run", result.Operation.Type)
	assert.Equal(t, "pipelines/orders/assets/orders.sql", result.Operation.Target)
	assert.Equal(t, []string{"inspect-1", "inspect-2"}, result.ChangedAssetIDs)
	assert.NotNil(t, result.MaterializedAt)
	assert.Equal(t, []string{"asset ", "run complete\n"}, streamed)
	require.Len(t, completed.Assets, 1)
	_, err := uuid.Parse(completed.CompletionID)
	require.NoError(t, err)
	assert.Equal(t, "analytics.orders", completed.Assets[0].AssetName)
	assert.Equal(t, "succeeded", completed.Assets[0].Status)
	assert.Equal(t, "target-orders", completed.Assets[0].TargetIdentity)
	assert.Equal(t, finished, *completed.Assets[0].FinishedAt)
	assert.Equal(t, "target-orders", completed.ExecutionTargets["analytics.orders"].TargetIdentity)
}

func TestExecutionServiceEnforcesDestructiveConfirmationAndEmitsFullRefresh(t *testing.T) {
	t.Parallel()

	assetID := EncodeID("pipelines/orders/assets/orders.sql")
	started := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	executor := &stubExecutionExecutor{
		runAssetEvents: []ExecutionAssetEvent{
			{Asset: "analytics.orders", Status: "running", StartedAt: &started},
			{Asset: "analytics.orders", Status: "success", StartedAt: &started, FinishedAt: &finished},
		},
	}
	events := bus.New()
	var completed bus.RunCompleted
	events.OnRunCompleted(func(event bus.RunCompleted) error { completed = event; return nil })
	svc := NewExecutionService(ExecutionDependencies{
		Executor:            executor,
		SelectedEnvironment: func() string { return "prod" },
		PolicyFor: func(environment string) policy.EnvironmentPolicy {
			assert.Equal(t, "prod", environment)
			return policy.EnvironmentPolicy{ConfirmDestructive: true}
		},
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "pipelines/orders/assets/orders.sql", &pipeline.Pipeline{}, &pipeline.Asset{Connection: "warehouse"}, nil
		},
		ResolveAssetNameByID: func(string) string { return "analytics.orders" },
		FindInspectIDs:       func(...string) []string { return []string{assetID} },
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{
				ID:     EncodeID("pipelines/orders/pipeline.yml"),
				UUID:   "orders-uuid",
				Assets: []AssetView{{ID: assetID, Name: "analytics.orders"}},
			}}
		},
		Events: events,
	})

	rejected := svc.MaterializeAssetStream(context.Background(), assetID, "", "", "", "", true, false, "wrong", nil)
	assert.Equal(t, "error", rejected.Status)
	assert.Contains(t, rejected.Error, "requires typing")
	assert.Empty(t, executor.runAssetRequests)

	accepted := svc.MaterializeAssetStream(context.Background(), assetID, "", "", "", "", true, false, "prod", nil)
	assert.Equal(t, "ok", accepted.Status)
	require.Len(t, executor.runAssetRequests, 1)
	assert.Equal(t, "prod", executor.runAssetRequests[0].Environment)
	assert.True(t, executor.runAssetRequests[0].FullRefresh)
	assert.True(t, completed.FullRefresh)
}

func TestExecutionServiceHonorsEnvironmentFullRefreshRestriction(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	configPath := filepath.Join(workspaceRoot, ".bruin.yml")
	require.NoError(t, os.WriteFile(configPath, []byte(strings.TrimSpace(`
default_environment: prod
environments:
  prod:
    config:
      full_refresh_restricted: true
    connections: {}
`)+"\n"), 0o644))

	assetID := EncodeID("pipelines/orders/assets/orders.sql")
	executor := &stubExecutionExecutor{}
	events := bus.New()
	var completed bus.RunCompleted
	events.OnRunCompleted(func(event bus.RunCompleted) error { completed = event; return nil })
	svc := NewExecutionService(ExecutionDependencies{
		ConfigPath:          configPath,
		Executor:            executor,
		SelectedEnvironment: func() string { return "prod" },
		PolicyFor: func(string) policy.EnvironmentPolicy {
			return policy.EnvironmentPolicy{ConfirmDestructive: true}
		},
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "pipelines/orders/assets/orders.sql", &pipeline.Pipeline{}, &pipeline.Asset{Connection: "warehouse"}, nil
		},
		ResolveAssetNameByID: func(string) string { return "analytics.orders" },
		FindInspectIDs:       func(...string) []string { return []string{assetID} },
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{
				ID:     EncodeID("pipelines/orders/pipeline.yml"),
				UUID:   "orders-uuid",
				Assets: []AssetView{{ID: assetID, Name: "analytics.orders"}},
			}}
		},
		Events: events,
	})

	result := svc.MaterializeAssetStream(context.Background(), assetID, "", "", "", "", true, false, "", nil)

	assert.Equal(t, "ok", result.Status)
	require.Len(t, executor.runAssetRequests, 1)
	assert.False(t, executor.runAssetRequests[0].FullRefresh)
	assert.False(t, completed.FullRefresh)
	require.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "restricted for environment prod")
}

func TestExecutionServiceOnlyBackfillsReplaySafeAssets(t *testing.T) {
	t.Parallel()

	assetID := EncodeID("pipelines/orders/assets/orders.sql")
	resolvedAsset := &pipeline.Asset{
		Type: pipeline.AssetTypeDuckDBQuery,
		Materialization: pipeline.Materialization{
			Type:            pipeline.MaterializationTypeTable,
			Strategy:        pipeline.MaterializationStrategyTimeInterval,
			IncrementalKey:  "event_at",
			TimeGranularity: pipeline.MaterializationTimeGranularityTimestamp,
		},
	}
	executor := &stubExecutionExecutor{}
	svc := NewExecutionService(ExecutionDependencies{
		Executor:            executor,
		SelectedEnvironment: func() string { return "prod" },
		PolicyFor: func(string) policy.EnvironmentPolicy {
			return policy.EnvironmentPolicy{ConfirmDestructive: true}
		},
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "pipelines/orders/assets/orders.sql", &pipeline.Pipeline{Schedule: "daily"}, resolvedAsset, nil
		},
		ResolveAssetNameByID: func(string) string { return "analytics.orders" },
		FindInspectIDs:       func(...string) []string { return []string{assetID} },
	})

	unsafe := *resolvedAsset
	unsafe.Materialization.Strategy = pipeline.MaterializationStrategyAppend
	resolvedAsset = &unsafe
	missingWindow := svc.MaterializeAssetStream(context.Background(), assetID, "", "asset", "", "", false, true, "prod", nil)
	assert.Equal(t, "error", missingWindow.Status)
	assert.Contains(t, missingWindow.Error, "explicit start and end")
	assert.Empty(t, executor.runAssetRequests)
	conflictingMode := svc.MaterializeAssetStream(context.Background(), assetID, "", "asset", "2024-01-01T00:00:00Z", "2024-01-02T00:00:00Z", true, true, "prod", nil)
	assert.Equal(t, "error", conflictingMode.Status)
	assert.Contains(t, conflictingMode.Error, "mutually exclusive")
	assert.Empty(t, executor.runAssetRequests)

	rejected := svc.MaterializeAssetStream(context.Background(), assetID, "", "asset", "2024-01-01T00:00:00Z", "2024-01-02T00:00:00Z", false, true, "prod", nil)
	assert.Equal(t, "error", rejected.Status)
	assert.Contains(t, rejected.Error, "not safe to backfill")
	assert.Empty(t, executor.runAssetRequests)

	resolvedAsset.Materialization.Strategy = pipeline.MaterializationStrategyTimeInterval
	accepted := svc.MaterializeAssetStream(context.Background(), assetID, "", "asset", "2024-01-01T00:00:00Z", "2024-01-02T00:00:00Z", false, true, "prod", nil)
	assert.Equal(t, "ok", accepted.Status)
	require.Len(t, executor.runAssetRequests, 1)
	assert.Equal(t, "2024-01-01T00:00:00Z", executor.runAssetRequests[0].StartDate)
	assert.Equal(t, "2024-01-02T00:00:00Z", executor.runAssetRequests[0].EndDate)
}

func TestExecutionServiceMaterializeAssetStreamPreservesFailureOutput(t *testing.T) {
	t.Parallel()

	assetID := EncodeID("pipelines/orders/assets/orders.sql")
	executor := &stubExecutionExecutor{
		runAssetOutput: []byte("asset failed after direct execution\n"),
		runAssetErr:    errors.New("asset failed"),
		runAssetEvents: []ExecutionAssetEvent{{
			Asset: "analytics.orders", Status: "failed",
			StartedAt:  func() *time.Time { value := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC); return &value }(),
			FinishedAt: func() *time.Time { value := time.Date(2026, 7, 17, 12, 0, 1, 0, time.UTC); return &value }(),
		}},
	}
	events := bus.New()
	var completed bus.RunCompleted
	events.OnRunCompleted(func(event bus.RunCompleted) error { completed = event; return nil })

	svc := NewExecutionService(ExecutionDependencies{
		ConfigPath: "/path/that/does/not/exist",
		Executor:   executor,
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "pipelines/orders/assets/orders.sql", &pipeline.Pipeline{}, &pipeline.Asset{Connection: "warehouse"}, nil
		},
		ResolveAssetNameByID: func(string) string { return "analytics.orders" },
		FindInspectIDs:       func(...string) []string { return []string{"inspect-1"} },
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{
				ID:     EncodeID("pipelines/orders/pipeline.yml"),
				UUID:   "orders-uuid",
				Assets: []AssetView{{ID: assetID, Name: "analytics.orders"}},
			}}
		},
		Events: events,
	})

	result := svc.MaterializeAssetStream(context.Background(), assetID, "", "", "", "", false, false, "", nil)

	require.Len(t, executor.runAssetRequests, 1)
	assert.Equal(t, "error", result.Status)
	assert.Equal(t, "asset failed after direct execution\n", result.Output)
	assert.Equal(t, "asset failed", result.Error)
	assert.Equal(t, 1, result.ExitCode)
	assert.Empty(t, result.ChangedAssetIDs)
	assert.Nil(t, result.MaterializedAt)
	require.Len(t, completed.Assets, 1)
	assert.Equal(t, "failed", completed.Assets[0].Status)
}

func TestExecutionServiceMaterializePipelineStreamPreservesSuccessOutput(t *testing.T) {
	t.Parallel()

	pipelineID := EncodeID("pipelines/orders/pipeline.yml")
	started := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	secondStarted := started.Add(time.Second)
	secondFinished := secondStarted.Add(time.Second)
	executor := &stubExecutionExecutor{
		runPipelineOutput: []byte("pipeline run complete\n"),
		runPipelineChunks: [][]byte{[]byte("pipeline "), []byte("run complete\n")},
		runPipelineEvents: []ExecutionAssetEvent{
			{Asset: "analytics.orders", Status: "success", StartedAt: &started, FinishedAt: &secondStarted},
			{Asset: "analytics.order_items", Status: "success", StartedAt: &secondStarted, FinishedAt: &secondFinished},
		},
		runPipelineTargets: &ExecutionTargetSnapshot{
			Version: ExecutionTargetSnapshotVersion, PipelineUUID: "orders-uuid",
			Entries: map[string]ExecutionTargetSnapshotEntry{
				"analytics.orders": {
					AssetID: "orders-uuid:analytics.orders", TargetIdentity: "target-orders",
					TargetFidelity: AssetRenderFidelityExact, Fingerprint: "v2:orders", OwnContent: "v2:orders-own",
					ConsumedVarsHash: "consumed", VarsHash: "vars", CoverageMode: ExecutionCoverageMarker,
				},
				"analytics.order_items": {
					AssetID: "orders-uuid:analytics.order_items", TargetIdentity: "target-items",
					TargetFidelity: AssetRenderFidelityExact, Fingerprint: "v2:items", OwnContent: "v2:items-own",
					ConsumedVarsHash: "consumed", VarsHash: "vars", CoverageMode: ExecutionCoverageMarker,
				},
			},
		},
	}
	streamed := make([]string, 0)
	events := bus.New()
	var completed bus.RunCompleted
	events.OnRunCompleted(func(event bus.RunCompleted) error { completed = event; return nil })

	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{
				ID:   pipelineID,
				UUID: "orders-uuid",
				Assets: []AssetView{
					{ID: "asset-1", Name: "analytics.orders"},
					{ID: "asset-2", Name: "analytics.order_items"},
				},
			}}
		},
		Events: events,
	})

	result := svc.MaterializePipelineStream(context.Background(), pipelineID, "", false, false, false, "", "", "", func(chunk []byte) {
		streamed = append(streamed, string(chunk))
	})

	require.Len(t, executor.runPipelineReqs, 1)
	assert.Equal(t, "pipelines/orders", executor.runPipelineReqs[0].Target)
	assert.Equal(t, sensorModeOnce, executor.runPipelineReqs[0].SensorMode)
	assert.Equal(t, "ok", result.Status)
	assert.Equal(t, "pipeline run complete\n", result.Output)
	assert.Empty(t, result.Error)
	assert.Equal(t, 0, result.ExitCode)
	assert.Equal(t, "run", result.Operation.Type)
	assert.Equal(t, "pipelines/orders", result.Operation.Target)
	assert.Equal(t, []string{"asset-1", "asset-2"}, result.ChangedAssetIDs)
	assert.NotNil(t, result.MaterializedAt)
	assert.Equal(t, []string{"pipeline ", "run complete\n"}, streamed)
	require.Len(t, completed.Assets, 2)
	assert.Equal(t, "succeeded", completed.Assets[0].Status)
	assert.Equal(t, "succeeded", completed.Assets[1].Status)
}

func TestExecutionServiceRecordsEachReviewedWindowWithDistinctCompletionIdentity(t *testing.T) {
	t.Parallel()

	pipelineID := EncodeID("pipelines/orders/pipeline.yml")
	started := time.Date(2026, 7, 17, 9, 0, 0, 0, time.UTC)
	snapshot := ExecutionTargetSnapshot{
		Version: ExecutionTargetSnapshotVersion, PipelineUUID: "orders-uuid",
		Entries: map[string]ExecutionTargetSnapshotEntry{
			"analytics.orders": {
				AssetID: "orders-uuid:analytics.orders", TargetIdentity: "target-orders",
				TargetFidelity: AssetRenderFidelityExact, Fingerprint: "v2:orders", OwnContent: "v2:orders-own",
				ConsumedVarsHash: "consumed", VarsHash: "vars", CoverageMode: ExecutionCoverageUnionIntervals,
			},
		},
	}
	units := []PipelineExecutionUnit{
		{
			Position: 0, AssetID: "orders-uuid:analytics.orders", AssetName: "analytics.orders",
			StartDate: "2026-07-17T09:00:00Z", EndDate: "2026-07-17T10:00:00Z", Reason: "coverage_gap",
		},
		{
			Position: 1, AssetID: "orders-uuid:analytics.orders", AssetName: "analytics.orders",
			StartDate: "2026-07-17T10:00:00Z", EndDate: "2026-07-17T11:00:00Z", Reason: "coverage_gap",
		},
	}
	executor := &stubExecutionExecutor{runPipelineTargets: &snapshot}
	executor.runPipelineExecute = func(req RunPipelineRequest) error {
		for position := range units {
			unitStarted := started.Add(time.Duration(position) * time.Hour)
			unitFinished := unitStarted.Add(time.Second)
			if err := req.UnitEvent(PipelineExecutionUnitEvent{
				Position: position, Status: "running", StartedAt: &unitStarted,
			}); err != nil {
				return err
			}
			if err := req.AssetEvent(ExecutionAssetEvent{
				Asset: "analytics.orders", Status: "running", StartedAt: &unitStarted,
				UnitPosition: position, HasUnitPosition: true,
			}); err != nil {
				return err
			}
			if err := req.AssetEvent(ExecutionAssetEvent{
				Asset: "analytics.orders", Status: "success", StartedAt: &unitStarted, FinishedAt: &unitFinished,
				UnitPosition: position, HasUnitPosition: true,
			}); err != nil {
				return err
			}
			if err := req.UnitEvent(PipelineExecutionUnitEvent{
				Position: position, Status: "success", StartedAt: &unitStarted, FinishedAt: &unitFinished,
			}); err != nil {
				return err
			}
		}
		return nil
	}
	writeStore := &stubTargetWriteStore{}
	var completions []bus.RunCompleted
	svc := NewExecutionService(ExecutionDependencies{
		Executor:     executor,
		TargetWrites: writeStore,
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{
				ID: pipelineID, UUID: "orders-uuid",
				Assets: []AssetView{{ID: "encoded-orders", Name: "analytics.orders"}},
			}}
		},
		DispatchCompletion: func(_ context.Context, event bus.RunCompleted) error {
			completions = append(completions, event)
			return nil
		},
	})

	result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{
		RunID: "run-reviewed-windows", PipelineID: pipelineID, PipelineUUID: "orders-uuid",
		StartDate: units[0].StartDate, EndDate: units[1].EndDate,
		Plan: &PipelineExecutionPlan{SelectionMode: PipelinePlanSelectionNeeded, Units: units},
	}, nil, nil)

	require.Equal(t, "ok", result.Status, result.Error)
	require.Len(t, completions, 2)
	assert.Equal(t, "run-reviewed-windows", completions[0].RunID)
	assert.Equal(t, "run-reviewed-windows", completions[1].RunID)
	assert.Equal(t, "run-reviewed-windows/unit/0", completions[0].CompletionID)
	assert.Equal(t, "run-reviewed-windows/unit/1", completions[1].CompletionID)
	assert.Equal(t, units[0].StartDate, completions[0].WinStart.Format(time.RFC3339))
	assert.Equal(t, units[0].EndDate, completions[0].WinEnd.Format(time.RFC3339))
	assert.Equal(t, units[1].StartDate, completions[1].WinStart.Format(time.RFC3339))
	assert.Equal(t, units[1].EndDate, completions[1].WinEnd.Format(time.RFC3339))
	require.Len(t, completions[0].Assets, 1)
	require.Len(t, completions[1].Assets, 1)
	assert.EqualValues(t, 0, completions[0].Assets[0].CompletionOrdinal)
	assert.EqualValues(t, 1, completions[1].Assets[0].CompletionOrdinal)
	require.Len(t, writeStore.claims, 2)
	assert.Equal(t, completions[0].CompletionID, writeStore.claims[0].CompletionID)
	assert.Equal(t, completions[1].CompletionID, writeStore.claims[1].CompletionID)
}

func TestExecutionServiceScheduledPipelineUsesWaitSensorMode(t *testing.T) {
	t.Parallel()
	pipelineID := EncodeID("pipelines/orders/pipeline.yml")
	executor := &stubExecutionExecutor{}
	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		PolicyFor: func(string) policy.EnvironmentPolicy {
			return policy.EnvironmentPolicy{Protected: true}
		},
	})

	result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{
		RunID:       "scheduled-run",
		PipelineID:  pipelineID,
		Environment: "prod",
		Scheduled:   true,
		DryRun:      true,
	}, nil, nil)

	assert.Equal(t, "ok", result.Status)
	require.Len(t, executor.runPipelineReqs, 1)
	assert.Equal(t, sensorModeWait, executor.runPipelineReqs[0].SensorMode)
}

func TestExecutionServiceQueuedManualPipelineUsesInteractiveSemantics(t *testing.T) {
	t.Parallel()
	pipelineID := EncodeID("pipelines/orders/pipeline.yml")

	t.Run("durable run id does not select scheduled sensor mode", func(t *testing.T) {
		executor := &stubExecutionExecutor{}
		svc := NewExecutionService(ExecutionDependencies{Executor: executor})

		result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{
			RunID:      "queued-manual-run",
			PipelineID: pipelineID,
			DryRun:     true,
		}, nil, nil)

		assert.Equal(t, "ok", result.Status)
		require.Len(t, executor.runPipelineReqs, 1)
		assert.Equal(t, sensorModeOnce, executor.runPipelineReqs[0].SensorMode)
	})

	t.Run("manual deployment remains interactive in a protected environment", func(t *testing.T) {
		executor := &stubExecutionExecutor{}
		svc := NewExecutionService(ExecutionDependencies{
			Executor: executor,
			PolicyFor: func(string) policy.EnvironmentPolicy {
				return policy.EnvironmentPolicy{Protected: true}
			},
		})

		result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{
			RunID:       "queued-manual-run",
			PipelineID:  pipelineID,
			Environment: "prod",
			SnapshotDir: "/tmp/deployed-snapshot",
			DryRun:      true,
		}, nil, nil)

		assert.Equal(t, "error", result.Status)
		assert.Contains(t, result.Error, "protected")
		assert.Empty(t, executor.runPipelineReqs)
	})
}

func TestExecutionServiceHonorsInteractiveSensorModeOverride(t *testing.T) {
	t.Parallel()
	pipelineID := EncodeID("pipelines/orders/pipeline.yml")
	executor := &stubExecutionExecutor{}
	svc := NewExecutionService(ExecutionDependencies{Executor: executor})

	result := svc.MaterializePipelineStreamWithSensorMode(
		context.Background(), pipelineID, "", false, false, false, "", "", "", sensorModeSkip, nil,
	)

	assert.Equal(t, "ok", result.Status)
	require.Len(t, executor.runPipelineReqs, 1)
	assert.Equal(t, sensorModeSkip, executor.runPipelineReqs[0].SensorMode)
}

func TestExecutionServicePinnedPipelineUsesSnapshotDefaultWindow(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	workspacePipelineRoot := filepath.Join(workspaceRoot, "pipelines", "orders")
	snapshotRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(workspacePipelineRoot, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(workspacePipelineRoot, "pipeline.yml"),
		[]byte("name: orders\nschedule: daily\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(snapshotRoot, "pipeline.yml"),
		[]byte("name: orders\nschedule: hourly\n"),
		0o644,
	))

	pipelineID := EncodeID("pipelines/orders/pipeline.yml")
	executor := &stubExecutionExecutor{}
	svc := NewExecutionService(ExecutionDependencies{
		WorkspaceRoot: workspaceRoot,
		Executor:      executor,
		NewPipelineBuilder: func() *pipeline.Builder {
			return NewRenartPipelineBuilder(afero.NewOsFs())
		},
	})

	result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{
		PipelineID:  pipelineID,
		SnapshotDir: snapshotRoot,
	}, nil, nil)

	require.Equal(t, "ok", result.Status, result.Error)
	require.Len(t, executor.runPipelineReqs, 1)
	request := executor.runPipelineReqs[0]
	start, err := time.Parse(time.RFC3339, request.StartDate)
	require.NoError(t, err)
	end, err := time.Parse(time.RFC3339, request.EndDate)
	require.NoError(t, err)
	assert.Equal(t, time.Hour, end.Sub(start), "snapshot is hourly while the working tree is daily")
	assert.Equal(t, request.StartDate, result.Operation.StartDate)
	assert.Equal(t, request.EndDate, result.Operation.EndDate)
}

func TestExecutionServicePinnedPipelineKeepsExplicitWindow(t *testing.T) {
	t.Parallel()

	executor := &stubExecutionExecutor{}
	svc := NewExecutionService(ExecutionDependencies{Executor: executor})
	result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{
		PipelineID:  EncodeID("pipelines/orders/pipeline.yml"),
		SnapshotDir: t.TempDir(),
		StartDate:   "2026-07-16T08:00:00Z",
		EndDate:     "2026-07-16T09:00:00Z",
	}, nil, nil)

	require.Equal(t, "ok", result.Status, result.Error)
	require.Len(t, executor.runPipelineReqs, 1)
	assert.Equal(t, "2026-07-16T08:00:00Z", executor.runPipelineReqs[0].StartDate)
	assert.Equal(t, "2026-07-16T09:00:00Z", executor.runPipelineReqs[0].EndDate)
	assert.Equal(t, executor.runPipelineReqs[0].StartDate, result.Operation.StartDate)
	assert.Equal(t, executor.runPipelineReqs[0].EndDate, result.Operation.EndDate)
}

func TestExecutionServicePinnedPipelineRejectsUnresolvedTemplatedSchedule(t *testing.T) {
	t.Parallel()

	snapshotRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(snapshotRoot, "pipeline.yml"),
		[]byte("name: orders\nschedule: \"{{ var.value.execution_schedule }}\"\n"),
		0o644,
	))
	executor := &stubExecutionExecutor{}
	svc := NewExecutionService(ExecutionDependencies{Executor: executor})

	result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{
		PipelineID:  EncodeID("pipelines/orders/pipeline.yml"),
		SnapshotDir: snapshotRoot,
	}, nil, nil)

	assert.Equal(t, "error", result.Status)
	assert.Contains(t, result.Error, "templated deployed pipeline schedule requires an explicit execution window")
	assert.Empty(t, executor.runPipelineReqs)
}

func TestExecutionServiceMaterializePipelineStreamPreservesFailureOutput(t *testing.T) {
	t.Parallel()

	pipelineID := EncodeID("pipelines/orders/pipeline.yml")
	executor := &stubExecutionExecutor{
		runPipelineOutput: []byte("pipeline failed during direct execution\n"),
		runPipelineErr:    errors.New("pipeline failed"),
		runPipelineEvents: []ExecutionAssetEvent{
			{Asset: "analytics.orders", Status: "running"},
			{Asset: "analytics.orders", Status: "success"},
			{Asset: "analytics.order_items", Status: "running"},
			{Asset: "analytics.order_items", Status: "failed", Error: "pipeline failed"},
		},
	}
	events := bus.New()
	var completed bus.RunCompleted
	events.OnRunCompleted(func(event bus.RunCompleted) error { completed = event; return nil })

	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{
				ID:   pipelineID,
				UUID: "orders-uuid",
				Assets: []AssetView{
					{ID: "asset-1", Name: "analytics.orders"},
					{ID: "asset-2", Name: "analytics.order_items"},
					{ID: "asset-3", Name: "analytics.parabola"},
				},
			}}
		},
		Events: events,
	})

	result := svc.MaterializePipelineStream(context.Background(), pipelineID, "", false, false, false, "", "", "", nil)

	require.Len(t, executor.runPipelineReqs, 1)
	assert.Equal(t, "error", result.Status)
	assert.Equal(t, "pipeline failed during direct execution\n", result.Output)
	assert.Equal(t, "pipeline failed", result.Error)
	assert.Equal(t, 1, result.ExitCode)
	assert.Equal(t, []string{"asset-1"}, result.ChangedAssetIDs)
	assert.Nil(t, result.MaterializedAt)
	require.Len(t, completed.Assets, 2)
	assert.Equal(t, "analytics.orders", completed.Assets[0].AssetName)
	assert.Equal(t, "succeeded", completed.Assets[0].Status)
	assert.Equal(t, "analytics.order_items", completed.Assets[1].AssetName)
	assert.Equal(t, "failed", completed.Assets[1].Status)
	for _, asset := range completed.Assets {
		assert.NotEqual(t, "analytics.parabola", asset.AssetName)
	}
}

func TestPipelineRunObservationPersistsTerminalCoordinatesBeforeForwarding(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	finished := started.Add(250 * time.Millisecond)
	forwardErr := errors.New("step persistence failed")
	var forwarded ExecutionAssetEvent
	observed := newPipelineRunObservation(func(event ExecutionAssetEvent) error {
		forwarded = event
		if event.Status == "success" {
			return forwardErr
		}
		return nil
	})
	require.NoError(t, observed.captureExecutionTargets(ExecutionTargetSnapshot{
		Version: ExecutionTargetSnapshotVersion, PipelineUUID: "pipeline-uuid",
		Entries: map[string]ExecutionTargetSnapshotEntry{
			"analytics.orders": {
				AssetID: "pipeline-uuid:analytics.orders", TargetIdentity: "target-1",
				TargetFidelity: AssetRenderFidelityExact, Fingerprint: "v2:fp",
				OwnContent: "v2:own", ConsumedVarsHash: "consumed", VarsHash: "vars", CoverageMode: ExecutionCoverageMarker,
			},
		},
	}))

	require.NoError(t, observed.handle(ExecutionAssetEvent{
		Asset: "analytics.orders", Status: "running", StartedAt: &started,
	}))
	err := observed.handle(ExecutionAssetEvent{
		Asset: "analytics.orders", Status: "success", StartedAt: &started, FinishedAt: &finished,
	})
	require.ErrorIs(t, err, forwardErr)
	require.NotNil(t, forwarded.CompletionOrdinal)
	assert.EqualValues(t, 0, *forwarded.CompletionOrdinal)
	assert.Equal(t, finished, *forwarded.FinishedAt)

	runs, succeeded := observed.completedAssets(PipelineView{
		UUID:   "pipeline-uuid",
		Assets: []AssetView{{ID: "encoded-asset-id", Name: "analytics.orders"}},
	}, "failed")
	require.Len(t, runs, 1)
	assert.Equal(t, "succeeded", runs[0].Status, "the warehouse write succeeded before terminal persistence failed")
	assert.Equal(t, started, *runs[0].StartedAt)
	assert.Equal(t, finished, *runs[0].FinishedAt)
	assert.EqualValues(t, 0, runs[0].CompletionOrdinal)
	assert.Equal(t, "target-1", runs[0].TargetIdentity)
	assert.Equal(t, "v2:fp", runs[0].Fingerprint)
	assert.Equal(t, []string{"encoded-asset-id"}, succeeded)
}

func TestPipelineRunObservationKeepsQualityFailureSeparateFromMainSuccess(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	var forwarded []ExecutionAssetEvent
	observed := newPipelineRunObservation(func(event ExecutionAssetEvent) error {
		forwarded = append(forwarded, event)
		return nil
	})

	require.NoError(t, observed.handle(ExecutionAssetEvent{
		Asset: "analytics.orders", Status: "running", StartedAt: &started,
	}))
	require.NoError(t, observed.handle(ExecutionAssetEvent{
		Asset: "analytics.orders", Status: "success", StartedAt: &started, FinishedAt: &finished,
	}))
	require.NoError(t, observed.handle(ExecutionAssetEvent{
		Asset: "analytics.orders", Status: "running",
		TaskKind: executionTaskKindCustomCheck, CheckName: "no invalid orders", CheckBlocking: true,
	}))
	require.NoError(t, observed.handle(ExecutionAssetEvent{
		Asset: "analytics.orders", Status: "failed",
		TaskKind: executionTaskKindCustomCheck, CheckName: "no invalid orders", CheckBlocking: true,
	}))

	runs, succeeded := observed.completedAssets(PipelineView{
		UUID: "pipeline-uuid",
		Assets: []AssetView{{
			ID: "encoded-asset-id", Name: "analytics.orders", QualityCheckCount: 1,
		}},
	}, "failed")
	require.Len(t, runs, 1)
	assert.Equal(t, "succeeded", runs[0].Status)
	assert.Equal(t, bus.QualityStatusFailed, runs[0].QualityStatus)
	assert.Equal(t, []bus.QualityCheckFailure{{
		Kind: bus.QualityCheckKindCustom, Name: "no invalid orders", Blocking: true,
	}}, runs[0].FailedChecks)
	assert.Equal(t, []string{"encoded-asset-id"}, succeeded)
	assert.Len(t, forwarded, 2, "quality events must not masquerade as scheduler asset steps")
}

func TestPipelineRunObservationMarksQualityPassedOnlyAfterEveryCheck(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	observed := newPipelineRunObservation(nil)
	require.NoError(t, observed.handle(ExecutionAssetEvent{
		Asset: "analytics.orders", Status: "running", StartedAt: &started,
	}))
	require.NoError(t, observed.handle(ExecutionAssetEvent{
		Asset: "analytics.orders", Status: "success", StartedAt: &started, FinishedAt: &finished,
	}))
	require.NoError(t, observed.handle(ExecutionAssetEvent{
		Asset: "analytics.orders", Status: "success",
		TaskKind: executionTaskKindColumnCheck, CheckName: "not_null", CheckColumn: "order_id",
	}))

	view := PipelineView{
		UUID: "pipeline-uuid",
		Assets: []AssetView{{
			ID: "encoded-asset-id", Name: "analytics.orders", QualityCheckCount: 2,
		}},
	}
	runs, _ := observed.completedAssets(view, "succeeded")
	require.Len(t, runs, 1)
	assert.Empty(t, runs[0].QualityStatus, "one successful check does not prove the full suite passed")

	require.NoError(t, observed.handle(ExecutionAssetEvent{
		Asset: "analytics.orders", Status: "success",
		TaskKind: executionTaskKindCustomCheck, CheckName: "no invalid orders",
	}))
	runs, _ = observed.completedAssets(view, "succeeded")
	require.Len(t, runs, 1)
	assert.Equal(t, bus.QualityStatusPassed, runs[0].QualityStatus)
	assert.Empty(t, runs[0].FailedChecks)
}

func TestPipelineRunObservationClaimsBeforeExecutionAndMarksFailuresDirty(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	var order []string
	store := &stubTargetWriteStore{
		claim: func(context.Context, matlog.TargetWriteClaim) error {
			order = append(order, "claim")
			return nil
		},
		dirty: func(context.Context, matlog.TargetWriteClaim, time.Time) error {
			order = append(order, "dirty")
			return nil
		},
	}
	observed := newPipelineRunObservation(func(event ExecutionAssetEvent) error {
		order = append(order, "step-"+event.Status)
		return nil
	})
	observed.configureTargetWrites(context.Background(), "completion-id", store)
	require.NoError(t, observed.captureExecutionTargets(ExecutionTargetSnapshot{
		Version: ExecutionTargetSnapshotVersion, PipelineUUID: "pipeline-uuid",
		Entries: map[string]ExecutionTargetSnapshotEntry{
			"analytics.orders": {
				AssetID: "pipeline-uuid:analytics.orders", TargetIdentity: "target-orders",
				TargetFidelity: AssetRenderFidelityExact, Fingerprint: "v2:fp", OwnContent: "v2:own",
				ConsumedVarsHash: "consumed", VarsHash: "vars", CoverageMode: ExecutionCoverageMarker,
			},
		},
	}))

	require.NoError(t, observed.handle(ExecutionAssetEvent{
		Asset: "analytics.orders", Status: "running", StartedAt: &started,
	}))
	require.Equal(t, []string{"step-running", "claim"}, order,
		"the direct task starts only after the durable claim callback returns")
	require.Len(t, store.claims, 1)
	assert.Equal(t, "completion-id", store.claims[0].CompletionID)
	assert.Equal(t, "pipeline-uuid:analytics.orders", store.claims[0].AssetID)

	require.NoError(t, observed.handle(ExecutionAssetEvent{
		Asset: "analytics.orders", Status: "failed", StartedAt: &started, FinishedAt: &finished,
	}))
	assert.Equal(t, []string{"step-running", "claim", "dirty", "step-failed"}, order,
		"physical uncertainty must be durable before terminal scheduler persistence")
	require.Len(t, store.dirtys, 1)
}

func TestPipelineRunObservationDefersEvidenceRequiredClaimUntilOperatorWrite(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	store := &stubTargetWriteStore{}
	observed := newPipelineRunObservation(nil)
	observed.configureTargetWrites(context.Background(), "python-completion", store)
	require.NoError(t, observed.captureExecutionTargets(ExecutionTargetSnapshot{
		Version: ExecutionTargetSnapshotVersion, PipelineUUID: "pipeline-uuid",
		Entries: map[string]ExecutionTargetSnapshotEntry{
			"analytics.python_table": {
				AssetID: "pipeline-uuid:analytics.python_table", TargetIdentity: "target-python-table",
				TargetFidelity: AssetRenderFidelityExact, TargetWriteEvidenceRequired: true,
				Fingerprint: "v2:fp", OwnContent: "v2:own", ConsumedVarsHash: "consumed", VarsHash: "vars",
				CoverageMode: ExecutionCoverageMarker,
			},
		},
	}))

	require.NoError(t, observed.handle(ExecutionAssetEvent{
		Asset: "analytics.python_table", Status: "running", StartedAt: &started,
	}))
	assert.Empty(t, store.claims, "starting Python code does not prove that materialize() returned output")

	require.NoError(t, observed.beginTargetWrite("analytics.python_table"))
	require.Len(t, store.claims, 1)
	assert.Equal(t, "target-python-table", store.claims[0].TargetIdentity)
	assert.Equal(t, "python-completion", store.claims[0].CompletionID)
}

func TestExecutionServiceFailsClosedWhenCompletionDispatchFails(t *testing.T) {
	t.Parallel()
	pipelineID := EncodeID("pipelines/orders/pipeline.yml")
	started := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	snapshot := ExecutionTargetSnapshot{
		Version: ExecutionTargetSnapshotVersion, PipelineUUID: "orders-uuid",
		Entries: map[string]ExecutionTargetSnapshotEntry{
			"analytics.orders": {
				AssetID: "orders-uuid:analytics.orders", TargetIdentity: "target-orders",
				TargetFidelity: AssetRenderFidelityExact, Fingerprint: "v2:orders", OwnContent: "v2:own",
				ConsumedVarsHash: "consumed", VarsHash: "vars", CoverageMode: ExecutionCoverageMarker,
			},
		},
	}
	executor := &stubExecutionExecutor{
		runPipelineTargets: &snapshot,
		runPipelineEvents: []ExecutionAssetEvent{
			{Asset: "analytics.orders", Status: "running", StartedAt: &started},
			{Asset: "analytics.orders", Status: "success", StartedAt: &started, FinishedAt: &finished},
		},
	}
	store := &stubTargetWriteStore{}
	dispatchErr := errors.New("state database unavailable")
	var completed bus.RunCompleted
	svc := NewExecutionService(ExecutionDependencies{
		Executor:     executor,
		TargetWrites: store,
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{
				ID: pipelineID, UUID: "orders-uuid",
				Assets: []AssetView{{ID: "orders-asset", Name: "analytics.orders"}},
			}}
		},
		DispatchCompletion: func(_ context.Context, event bus.RunCompleted) error {
			completed = event
			return dispatchErr
		},
	})

	result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{PipelineID: pipelineID}, nil, nil)
	require.Equal(t, "error", result.Status)
	assert.ErrorContains(t, errors.New(result.Error), "physical execution completed")
	assert.ErrorContains(t, errors.New(result.Error), dispatchErr.Error())
	assert.Nil(t, result.MaterializedAt)
	require.Len(t, store.claims, 1)
	require.Len(t, store.dirtys, 1, "a pending successful completion must suppress stale writer evidence")
	assert.Equal(t, store.claims[0], store.dirtys[0])
	assert.Equal(t, store.claims[0].CompletionID, completed.CompletionID)
}

func TestExecutionServiceCarriesCapturedTargetsAndUniqueCompletionIdentity(t *testing.T) {
	t.Parallel()
	pipelineID := EncodeID("pipelines/orders/pipeline.yml")
	started := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	snapshot := ExecutionTargetSnapshot{
		Version: ExecutionTargetSnapshotVersion, PipelineUUID: "orders-uuid",
		Entries: map[string]ExecutionTargetSnapshotEntry{
			"analytics.orders": {
				AssetID: "orders-uuid:analytics.orders", TargetIdentity: "target-orders",
				TargetFidelity: AssetRenderFidelityExact, Fingerprint: "v2:orders",
				OwnContent: "v2:orders-own", ConsumedVarsHash: "consumed", VarsHash: "vars", CoverageMode: ExecutionCoverageMarker,
			},
		},
	}
	executor := &stubExecutionExecutor{
		runPipelineTargets: &snapshot,
		runPipelineEvents: []ExecutionAssetEvent{
			{Asset: "analytics.orders", Status: "running", StartedAt: &started},
			{Asset: "analytics.orders", Status: "success", StartedAt: &started, FinishedAt: &finished},
		},
	}
	events := bus.New()
	var completed bus.RunCompleted
	events.OnRunCompleted(func(event bus.RunCompleted) error {
		completed = event
		return nil
	})
	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{
				ID: pipelineID, UUID: "orders-uuid",
				Assets: []AssetView{{ID: "encoded-orders", Name: "analytics.orders"}},
			}}
		},
		Events: events,
	})

	result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{PipelineID: pipelineID}, nil, nil)
	require.Equal(t, "ok", result.Status, result.Error)
	_, err := uuid.Parse(completed.CompletionID)
	require.NoError(t, err)
	assert.Empty(t, completed.RunID, "inline fact keys remain independent from scheduler run IDs")
	assert.Equal(t, ExecutionTargetSnapshotVersion, completed.ExecutionTargetSnapshotVersion)
	assert.Equal(t, "target-orders", completed.ExecutionTargets["analytics.orders"].TargetIdentity)
	require.Len(t, completed.Assets, 1)
	assert.Equal(t, "target-orders", completed.Assets[0].TargetIdentity)
	assert.Equal(t, finished, *completed.Assets[0].FinishedAt)
	assert.EqualValues(t, 0, completed.Assets[0].CompletionOrdinal)
}

func TestExecutionServiceCompletionUsesCapturedDeploymentAssetsNotCurrentWorkspaceView(t *testing.T) {
	t.Parallel()
	pipelineID := EncodeID("pipelines/orders/pipeline.yml")
	started := time.Date(2026, 7, 17, 13, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	snapshot := ExecutionTargetSnapshot{
		Version: ExecutionTargetSnapshotVersion, PipelineUUID: "orders-uuid",
		Entries: map[string]ExecutionTargetSnapshotEntry{
			"analytics.old_orders": {
				AssetID: "orders-uuid:analytics.old_orders", TargetIdentity: "target-orders",
				TargetFidelity: AssetRenderFidelityExact, Fingerprint: "v2:old-orders",
				OwnContent: "v2:old-orders-own", ConsumedVarsHash: "consumed", VarsHash: "vars",
				CoverageMode: ExecutionCoverageMarker,
			},
		},
	}
	executor := &stubExecutionExecutor{
		runPipelineTargets: &snapshot,
		runPipelineEvents: []ExecutionAssetEvent{
			{Asset: "analytics.old_orders", Status: "running", StartedAt: &started},
			{Asset: "analytics.old_orders", Status: "success", StartedAt: &started, FinishedAt: &finished},
		},
	}
	events := bus.New()
	var completed bus.RunCompleted
	events.OnRunCompleted(func(event bus.RunCompleted) error { completed = event; return nil })
	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{
				ID: pipelineID, UUID: "orders-uuid",
				Assets: []AssetView{{ID: "current-new-id", Name: "analytics.new_orders"}},
			}}
		},
		Events: events,
	})

	result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{
		PipelineID: pipelineID, PipelineUUID: "orders-uuid", SnapshotVersionID: "deployed-version",
	}, nil, nil)
	require.Equal(t, "ok", result.Status, result.Error)
	require.Len(t, completed.Assets, 1)
	assert.Equal(t, "analytics.old_orders", completed.Assets[0].AssetName)
	assert.Equal(t, "orders-uuid:analytics.old_orders", completed.Assets[0].AssetID)
	assert.Empty(t, result.ChangedAssetIDs, "current-view IDs are presentation-only and cannot replace captured assets")
	assert.NotNil(t, result.MaterializedAt)
}

func TestExecutionServiceMaterializePipelineStreamDryRunDoesNotEmitCompletion(t *testing.T) {
	t.Parallel()

	pipelineID := EncodeID("pipelines/orders/pipeline.yml")
	executor := &stubExecutionExecutor{
		runPipelineOutput: []byte("dry run complete\n"),
	}
	events := bus.New()
	completed := 0
	events.OnRunCompleted(func(bus.RunCompleted) error { completed++; return nil })

	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{
				ID:     pipelineID,
				Assets: []AssetView{{ID: "asset-1", Name: "analytics.orders"}},
			}}
		},
		Events: events,
	})

	result := svc.MaterializePipelineStream(context.Background(), pipelineID, "", true, false, false, "", "", "", nil)

	require.Len(t, executor.runPipelineReqs, 1)
	assert.True(t, executor.runPipelineReqs[0].DryRun)
	assert.Empty(t, executor.runPipelineReqs[0].StartDate)
	assert.Empty(t, executor.runPipelineReqs[0].EndDate)
	assert.Equal(t, "ok", result.Status)
	assert.Empty(t, result.Operation.StartDate)
	assert.Empty(t, result.Operation.EndDate)
	assert.Empty(t, result.ChangedAssetIDs)
	assert.Nil(t, result.MaterializedAt)
	assert.Zero(t, completed)
}

func TestExecutionServiceDryRunDoesNotResolveExecutionContext(t *testing.T) {
	t.Parallel()

	executor := &stubExecutionExecutor{}
	contextResolved := false
	svc := NewExecutionService(ExecutionDependencies{Executor: executor})

	result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{
		PipelineID: EncodeID("pipelines/orders/pipeline.yml"),
		DryRun:     true,
		OnContextResolved: func(ResolvedPipelineRunContext) error {
			contextResolved = true
			return nil
		},
	}, nil, nil)

	require.Equal(t, "ok", result.Status, result.Error)
	require.Len(t, executor.runPipelineReqs, 1)
	assert.False(t, contextResolved)
	assert.Empty(t, executor.runPipelineReqs[0].StartDate)
	assert.Empty(t, executor.runPipelineReqs[0].EndDate)
	assert.Empty(t, result.Operation.StartDate)
	assert.Empty(t, result.Operation.EndDate)
}

func TestExecutionServiceUsesConfirmedExecutionTime(t *testing.T) {
	t.Parallel()
	executor := &stubExecutionExecutor{}
	svc := NewExecutionService(ExecutionDependencies{Executor: executor})
	executionTime := time.Date(2026, 7, 17, 10, 11, 12, 123456789, time.UTC)

	result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{
		PipelineID:    EncodeID("pipelines/orders/pipeline.yml"),
		ExecutionTime: executionTime.Format(time.RFC3339Nano),
		StartDate:     "2026-07-17T09:00:00Z",
		EndDate:       "2026-07-17T10:00:00Z",
	}, nil, nil)

	require.Equal(t, "ok", result.Status, result.Error)
	require.Len(t, executor.runPipelineReqs, 1)
	assert.Equal(t, executionTime, executor.runPipelineReqs[0].ExecutionTime)
}

func TestExecutionServiceRejectsChangedConfirmedConfiguration(t *testing.T) {
	t.Parallel()
	executor := &stubExecutionExecutor{
		runPipelineTargets: &ExecutionTargetSnapshot{
			Version: ExecutionTargetSnapshotVersion, PipelineUUID: "orders-uuid",
			ConfigurationDigest: strings.Repeat("b", 64), ConfigurationFidelity: "exact",
			Entries: map[string]ExecutionTargetSnapshotEntry{
				"analytics.orders": {
					AssetID: "orders-uuid:analytics.orders", TargetFidelity: AssetRenderFidelityRuntimeOnly,
					Fingerprint: "v2:orders", OwnContent: "v2:own", ConsumedVarsHash: "consumed", VarsHash: "vars",
				},
			},
		},
	}
	svc := NewExecutionService(ExecutionDependencies{Executor: executor})

	result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{
		PipelineID:                  EncodeID("pipelines/orders/pipeline.yml"),
		ExpectedConfigurationDigest: strings.Repeat("a", 64),
	}, nil, nil)

	require.Equal(t, "error", result.Status)
	assert.Contains(t, result.Error, "execution configuration changed after plan confirmation")
}

func TestExecutionServiceHoldsWorkspaceLeaseThroughDurablePipelineCompletion(t *testing.T) {
	t.Parallel()
	pipelineID := EncodeID("pipelines/orders/pipeline.yml")
	started := time.Date(2026, 7, 17, 16, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	leaseHeld := false
	acquired := 0
	released := 0
	executor := &stubExecutionExecutor{
		runPipelineTargets: &ExecutionTargetSnapshot{
			Version: ExecutionTargetSnapshotVersion, PipelineUUID: "orders-uuid",
			Entries: map[string]ExecutionTargetSnapshotEntry{
				"analytics.orders": {
					AssetID: "orders-uuid:analytics.orders", TargetFidelity: AssetRenderFidelityRuntimeOnly,
					Fingerprint: "v2:orders", OwnContent: "v2:own", ConsumedVarsHash: "consumed", VarsHash: "vars",
				},
			},
		},
		runPipelineEvents: []ExecutionAssetEvent{
			{Asset: "analytics.orders", Status: "running", StartedAt: &started},
			{Asset: "analytics.orders", Status: "success", StartedAt: &started, FinishedAt: &finished},
		},
		onRunPipeline: func() {
			assert.True(t, leaseHeld, "physical execution must run under the shared workspace lease")
		},
	}
	dispatched := false
	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		AcquireExecutionLease: func(context.Context) (func() error, error) {
			acquired++
			leaseHeld = true
			return func() error {
				released++
				leaseHeld = false
				return nil
			}, nil
		},
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{ID: pipelineID, UUID: "orders-uuid", Assets: []AssetView{{ID: "asset-id", Name: "analytics.orders"}}}}
		},
		DispatchCompletion: func(context.Context, bus.RunCompleted) error {
			dispatched = true
			assert.True(t, leaseHeld, "the lease must cover the durable completion hand-off")
			return nil
		},
	})

	result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{PipelineID: pipelineID}, nil, nil)

	require.Equal(t, "ok", result.Status, result.Error)
	assert.True(t, dispatched)
	assert.Equal(t, 1, acquired)
	assert.Equal(t, 1, released)
	assert.False(t, leaseHeld)
}

func TestExecutionServiceWorkspaceLeaseCoversAssetRunsAndSkipsDryRuns(t *testing.T) {
	t.Parallel()
	assetID := EncodeID("pipelines/orders/assets/orders.sql")
	leaseHeld := false
	acquired := 0
	executor := &stubExecutionExecutor{
		onRunAsset: func() {
			assert.True(t, leaseHeld)
		},
	}
	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		AcquireExecutionLease: func(context.Context) (func() error, error) {
			acquired++
			leaseHeld = true
			return func() error { leaseHeld = false; return nil }, nil
		},
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "pipelines/orders/assets/orders.sql", &pipeline.Pipeline{}, &pipeline.Asset{}, nil
		},
		ResolveAssetNameByID: func(string) string { return "analytics.orders" },
		FindInspectIDs:       func(ids ...string) []string { return ids },
	})

	assetResult := svc.MaterializeAssetStream(context.Background(), assetID, "", "", "", "", false, false, "", nil)
	require.Equal(t, "ok", assetResult.Status, assetResult.Error)
	assert.Equal(t, 1, acquired)
	assert.False(t, leaseHeld)

	dryResult := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{
		PipelineID: EncodeID("pipelines/orders/pipeline.yml"),
		DryRun:     true,
	}, nil, nil)
	require.Equal(t, "ok", dryResult.Status, dryResult.Error)
	assert.Equal(t, 1, acquired, "read-only planning must not acquire the physical execution lease")
}

func TestExecutionServiceAbortsBeforePhysicalWorkWhenWorkspaceLeaseFails(t *testing.T) {
	t.Parallel()
	executor := &stubExecutionExecutor{}
	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		AcquireExecutionLease: func(context.Context) (func() error, error) {
			return nil, errors.New("lease unavailable")
		},
	})

	result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{
		PipelineID: EncodeID("pipelines/orders/pipeline.yml"),
	}, nil, nil)

	require.Equal(t, "error", result.Status)
	assert.Contains(t, result.Error, "lease unavailable")
	assert.Empty(t, executor.runPipelineReqs)
}

func TestExecutionServicePreservesCancellationStatus(t *testing.T) {
	t.Parallel()
	pipelineID := EncodeID("pipelines/orders/pipeline.yml")
	started := time.Now().UTC()
	finished := started.Add(time.Second)
	executor := &stubExecutionExecutor{
		runPipelineErr: context.Canceled,
		runPipelineEvents: []ExecutionAssetEvent{
			{Asset: "analytics.orders", Status: "running", StartedAt: &started},
			{Asset: "analytics.orders", Status: "cancelled", StartedAt: &started, FinishedAt: &finished, Error: context.Canceled.Error()},
		},
	}
	events := bus.New()
	var completed bus.RunCompleted
	events.OnRunCompleted(func(event bus.RunCompleted) error { completed = event; return nil })
	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		CurrentPipelines: func() []PipelineView {
			return []PipelineView{{
				ID: pipelineID, UUID: "orders-uuid",
				Assets: []AssetView{{ID: "asset-id", Name: "analytics.orders"}},
			}}
		},
		Events: events,
	})

	result := svc.MaterializePipelineRun(context.Background(), PipelineRunSpec{PipelineID: pipelineID}, nil, nil)

	assert.Equal(t, "cancelled", result.Status)
	assert.Equal(t, 1, result.ExitCode)
	assert.Equal(t, context.Canceled.Error(), result.Error)
	require.Len(t, completed.Assets, 1)
	assert.Equal(t, "cancelled", completed.Assets[0].Status)
}

func TestExecutionServiceInspectAssetRejectsWriteQueries(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetsRoot := filepath.Join(pipelineRoot, "assets")
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceRoot, ".bruin.yml"), []byte(strings.TrimSpace(`
default_environment: default
environments:
  default:
    connections:
      duckdb:
        - name: duckdb-default
          path: duckdb-files/local.db
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte("name: analytics\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "customers.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.customers
type: duckdb.sql
materialization:
  type: view
@bruin */

copy (select * from analytics.customers) to 'danger.parquet'
`)+"\n"), 0o644))

	resolveAssetByID := newExecutionTestResolver(workspaceRoot).ResolveAssetByID

	svc := NewExecutionService(ExecutionDependencies{
		WorkspaceRoot:    workspaceRoot,
		ConfigPath:       filepath.Join(workspaceRoot, ".bruin.yml"),
		Executor:         &stubExecutionExecutor{},
		ResolveAssetByID: resolveAssetByID,
	})

	result := svc.InspectAsset(context.Background(), EncodeID("analytics/assets/customers.sql"), "200", "", "", "")

	assert.Equal(t, "error", result.Status)
	assert.Equal(t, 400, result.HTTPStatus)
	assert.Equal(t, inspectReadOnlyErrorMessage, result.Error)
	assert.Equal(t, inspectReadOnlyErrorMessage, result.RawOutput)
	assert.Empty(t, result.Rows)
	assert.Empty(t, result.Columns)
}

func TestExecutionServiceInspectNonSQLAssetQueriesMaterializedTable(t *testing.T) {
	t.Parallel()

	executor := &stubExecutionExecutor{
		queryConnOutput: []byte(`{"columns":["customer_id"],"rows":[{"customer_id":1}]}`),
	}
	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "analytics/assets/load_customers.yml", &pipeline.Pipeline{
					DefaultConnections: pipeline.EmptyStringMap{"duckdb": "duckdb-default"},
				}, &pipeline.Asset{
					Name: "analytics.customers",
					Type: pipeline.AssetTypeIngestr,
					Parameters: pipeline.ParameterMap{
						"destination": "duckdb",
					},
				}, nil
		},
	})

	result := svc.InspectAsset(context.Background(), EncodeID("analytics/assets/load_customers.yml"), "25", "", "", "")

	require.Len(t, executor.queryConnReqs, 1)
	assert.Equal(t, "duckdb-default", executor.queryConnReqs[0].ConnectionName)
	assert.Equal(t, "select * from analytics.customers limit 25", executor.queryConnReqs[0].Query)
	assert.Equal(t, "ok", result.Status)
	assert.Equal(t, []string{"customer_id"}, result.Columns)
	assert.Equal(t, []map[string]any{{"customer_id": float64(1)}}, result.Rows)
}

func TestExecutionServiceInspectLoadAssetQueriesDestinationConnection(t *testing.T) {
	t.Parallel()

	executor := &stubExecutionExecutor{
		queryConnOutput: []byte(`{"columns":["id"],"rows":[{"id":7}]}`),
	}
	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "analytics/assets/load_orders.asset.yml", &pipeline.Pipeline{}, &pipeline.Asset{
				Name:       "analytics.orders",
				Type:       pipeline.AssetType(loadAssetType),
				Connection: "duckdb-default",
				Parameters: pipeline.ParameterMap{
					"source_connection": "postgres-prod",
					"source_table":      "public.orders",
				},
			}, nil
		},
	})

	result := svc.InspectAsset(context.Background(), EncodeID("analytics/assets/load_orders.asset.yml"), "25", "", "", "")

	require.Len(t, executor.queryConnReqs, 1)
	assert.Equal(t, "duckdb-default", executor.queryConnReqs[0].ConnectionName)
	assert.Equal(t, "select * from analytics.orders limit 25", executor.queryConnReqs[0].Query)
	assert.Equal(t, "ok", result.Status)
	assert.Equal(t, []string{"id"}, result.Columns)
}

func TestExecutionServiceInspectLoadAssetToLocalFileReturnsInfo(t *testing.T) {
	t.Parallel()

	executor := &stubExecutionExecutor{}
	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "analytics/assets/load_local.asset.yml", &pipeline.Pipeline{}, &pipeline.Asset{
				Name:       "analytics.local_dump",
				Type:       pipeline.AssetType(loadAssetType),
				Connection: "local",
				Parameters: pipeline.ParameterMap{
					"source_connection":  "duckdb-default",
					"source_table":       "analytics.orders",
					"destination_object": "./blub.csv",
				},
			}, nil
		},
	})

	result := svc.InspectAsset(context.Background(), EncodeID("analytics/assets/load_local.asset.yml"), "25", "", "", "")

	assert.Equal(t, "info", result.Status)
	assert.Equal(t, 200, result.HTTPStatus)
	assert.Contains(t, result.Info, "./blub.csv")
	assert.Empty(t, result.Error)
	assert.Empty(t, executor.queryConnReqs, "a local-file load asset must not run a connection query")
}

func TestExecutionServiceInspectSensorReturnsInfoWithoutQueryingTable(t *testing.T) {
	t.Parallel()

	executor := &stubExecutionExecutor{}
	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "analytics/assets/upstream_ready.asset.yml", &pipeline.Pipeline{}, &pipeline.Asset{
				Name: "analytics.upstream_ready",
				Type: pipeline.AssetTypeDuckDBQuerySensor,
			}, nil
		},
	})

	result := svc.InspectAsset(context.Background(), EncodeID("analytics/assets/upstream_ready.asset.yml"), "25", "", "", "")

	assert.Equal(t, "info", result.Status)
	assert.Equal(t, 200, result.HTTPStatus)
	assert.Contains(t, result.Info, "do not materialize previewable data")
	assert.Empty(t, executor.queryConnReqs)
}

func TestExecutionServiceInspectNonSQLAssetReportsMissingMaterializedTable(t *testing.T) {
	t.Parallel()

	executor := &stubExecutionExecutor{queryConnErr: errors.New("table not found")}
	svc := NewExecutionService(ExecutionDependencies{
		Executor: executor,
		ResolveAssetByID: func(context.Context, string) (string, *pipeline.Pipeline, *pipeline.Asset, error) {
			return "analytics/assets/task.py", &pipeline.Pipeline{
					DefaultConnections: pipeline.EmptyStringMap{"duckdb": "duckdb-default"},
				}, &pipeline.Asset{
					Name: "analytics.python_task",
					Type: pipeline.AssetTypePython,
				}, nil
		},
	})

	result := svc.InspectAsset(context.Background(), EncodeID("analytics/assets/task.py"), "25", "", "", "")

	assert.Equal(t, "error", result.Status)
	assert.Contains(t, result.Error, "Materialize the asset first")
	require.Len(t, executor.queryConnReqs, 1)
}

func TestExecutionServiceInspectAssetDetectsMissingRenartUpstream(t *testing.T) {
	t.Parallel()

	workspaceRoot := t.TempDir()
	pipelineRoot := filepath.Join(workspaceRoot, "analytics")
	assetsRoot := filepath.Join(pipelineRoot, "assets", "analytics")
	require.NoError(t, os.MkdirAll(filepath.Join(workspaceRoot, ".git"), 0o755))
	require.NoError(t, os.MkdirAll(assetsRoot, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspaceRoot, ".bruin.yml"), []byte(strings.TrimSpace(`
default_environment: default
environments:
  default:
    connections:
      duckdb:
        - name: duckdb-default
          path: duckdb-files/local.db
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineRoot, "pipeline.yml"), []byte("name: analytics\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "players.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.players
type: duckdb.sql
materialization:
  type: table
@bruin */

select 1 as player_id
`)+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(assetsRoot, "player_stats.sql"), []byte(strings.TrimSpace(`
/* @bruin
name: analytics.player_stats
type: duckdb.sql
materialization:
  type: table
depends:
  - analytics.players
@bruin */

select * from analytics.players
`)+"\n"), 0o644))

	resolveAssetByID := newExecutionTestResolver(workspaceRoot).ResolveAssetByID

	queryErr := errors.New("Catalog Error: Table with name analytics.players does not exist")
	svc := NewExecutionService(ExecutionDependencies{
		WorkspaceRoot: workspaceRoot,
		ConfigPath:    filepath.Join(workspaceRoot, ".bruin.yml"),
		Executor: &stubExecutionExecutor{runWithRetry: func(context.Context, QueryAssetRequest, int, time.Duration) ([]byte, error, int) {
			return nil, queryErr, 1
		}},
		ResolveAssetByID: resolveAssetByID,
	})

	result := svc.InspectAsset(context.Background(), EncodeID("analytics/assets/analytics/player_stats.sql"), "25", "", "", "")

	assert.Equal(t, "error", result.Status)
	assert.Equal(t, []string{EncodeID("analytics/assets/analytics/players.sql")}, result.MissingUpstreamAssetIDs)
	assert.Equal(t, []string{"analytics.players"}, result.MissingUpstreamAssetNames)
	assert.True(t, result.MissingUpstreamAssetsMaterializable)
}
