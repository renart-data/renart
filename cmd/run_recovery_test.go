package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/bus"
	"renart/internal/web/fingerprint"
	"renart/internal/web/identity"
	"renart/internal/web/matlog"
	webscheduler "renart/internal/web/scheduler"
	"renart/internal/web/snapshot"
)

func TestReplayRecoveredRunEmitsPersistedStepsAgainstPinnedSnapshot(t *testing.T) {
	ctx := context.Background()
	schedulerStore, err := webscheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer schedulerStore.Close()

	pipelineDir := filepath.Join(t.TempDir(), "analytics")
	require.NoError(t, os.MkdirAll(filepath.Join(pipelineDir, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineDir, "pipeline.yml"), []byte("name: analytics\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineDir, "assets", "finished.sql"), []byte("select 1\n"), 0o644))

	snapshotStore := snapshot.NewStore(schedulerStore.DB())
	deployed, _, err := snapshotStore.Deploy(ctx, "pipeline-uuid", pipelineDir, "test")
	require.NoError(t, err)

	events := bus.New()
	server := &webServer{snapshotStore: snapshotStore, eventBus: events}
	var got bus.RunCompleted
	var materializedFile string
	events.OnRunCompleted(func(event bus.RunCompleted) error {
		got = event
		materializedFile = filepath.Join(event.SnapshotDir, "assets", "finished.sql")
		content, readErr := os.ReadFile(materializedFile)
		require.NoError(t, readErr)
		assert.Equal(t, "select 1\n", string(content))
		return nil
	})

	windowStart := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	windowEnd := windowStart.Add(30 * time.Minute)
	finishedAt := windowEnd.Add(time.Minute)
	firstStarted := windowStart.Add(time.Minute)
	firstFinished := firstStarted.Add(time.Minute)
	secondStarted := firstFinished
	secondFinished := secondStarted.Add(time.Minute)
	firstOrdinal := int64(0)
	secondOrdinal := int64(1)
	err = server.replayRecoveredRun(ctx, webscheduler.PipelineRun{
		ID: "run-id", PipelineID: "encoded-path", Pipeline: "analytics",
		Environment: "prod", Status: webscheduler.RunStatusFailed,
		WinStart: &windowStart, WinEnd: &windowEnd, FinishedAt: &finishedAt,
		SnapshotVersionID: deployed.VersionID, FullRefresh: true,
		ExecutionContextResolved: true,
		ExecutionTargetSnapshot: &webscheduler.ExecutionTargetSnapshot{
			Version: webscheduler.ExecutionTargetSnapshotVersionV1,
			Entries: map[string]webscheduler.ExecutionTargetSnapshotEntry{
				"analytics.finished": {
					AssetID:        identity.AssetID("pipeline-uuid", "analytics.finished"),
					TargetIdentity: "target-finished", TargetFidelity: "exact",
					Fingerprint: "v2:finished", OwnContent: "v2:finished-own",
					ConsumedVarsHash: "consumed", VarsHash: "vars",
				},
				"analytics.interrupted": {
					AssetID:        identity.AssetID("pipeline-uuid", "analytics.interrupted"),
					TargetIdentity: "target-interrupted", TargetFidelity: "exact",
					Fingerprint: "v2:interrupted", OwnContent: "v2:interrupted-own",
					ConsumedVarsHash: "consumed", VarsHash: "vars",
				},
			},
		},
	}, []webscheduler.PipelineRunStep{
		{Asset: "analytics.finished", Status: webscheduler.RunStatusSuccess, StartedAt: &firstStarted, FinishedAt: &firstFinished, CompletionOrdinal: &firstOrdinal},
		{Asset: "analytics.interrupted", Status: webscheduler.RunStatusFailed, StartedAt: &secondStarted, FinishedAt: &secondFinished, CompletionOrdinal: &secondOrdinal},
		{Asset: "analytics.unreached", Status: webscheduler.RunStatusQueued},
	}, nil)
	require.NoError(t, err)

	assert.Equal(t, "run-id", got.RunID)
	assert.Equal(t, "run-id", got.CompletionID)
	assert.Equal(t, "pipeline-uuid", got.PipelineUUID)
	assert.Equal(t, "prod", got.Environment)
	assert.Equal(t, deployed.VersionID, got.SnapshotVersionID)
	assert.Equal(t, finishedAt, got.CompletedAt)
	assert.Equal(t, &windowStart, got.WinStart)
	assert.Equal(t, &windowEnd, got.WinEnd)
	assert.True(t, got.FullRefresh)
	assert.Equal(t, webscheduler.ExecutionTargetSnapshotVersionV1, got.ExecutionTargetSnapshotVersion)
	assert.Equal(t, "target-finished", got.ExecutionTargets["analytics.finished"].TargetIdentity)
	assert.Equal(t, []bus.AssetRun{
		{
			AssetID: identity.AssetID("pipeline-uuid", "analytics.finished"), AssetName: "analytics.finished", Status: "succeeded",
			StartedAt: &firstStarted, FinishedAt: &firstFinished, CompletionOrdinal: 0, HasCompletionOrdinal: true,
			TargetIdentity: "target-finished", TargetFidelity: "exact", Fingerprint: "v2:finished",
			OwnContent: "v2:finished-own", ConsumedVarsHash: "consumed", VarsHash: "vars",
		},
		{
			AssetID: identity.AssetID("pipeline-uuid", "analytics.interrupted"), AssetName: "analytics.interrupted", Status: "failed",
			StartedAt: &secondStarted, FinishedAt: &secondFinished, CompletionOrdinal: 1, HasCompletionOrdinal: true,
			TargetIdentity: "target-interrupted", TargetFidelity: "exact", Fingerprint: "v2:interrupted",
			OwnContent: "v2:interrupted-own", ConsumedVarsHash: "consumed", VarsHash: "vars",
		},
	}, got.Assets)
	assert.NoDirExists(t, got.SnapshotDir, "snapshot temp dir is removed after synchronous dispatch")
	assert.NoFileExists(t, materializedFile)
}

func TestReplayRecoveredWorkingTreeRunUsesSelfContainedV2Snapshot(t *testing.T) {
	t.Parallel()

	events := bus.New()
	server := &webServer{
		// Deliberately omit workspaceCoord. A recovery implementation that tries
		// to resolve the stale path through the current workspace will panic.
		eventBus: events,
	}

	var got bus.RunCompleted
	events.OnRunCompleted(func(event bus.RunCompleted) error {
		got = event
		return nil
	})

	startedAt := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	finishedAt := startedAt.Add(time.Minute)
	ordinal := int64(0)
	pipelineUUID := "pipeline-uuid"
	upstreamMaterializedAt := startedAt.Add(-time.Minute)
	upstreamAssetID := identity.AssetID(pipelineUUID, "analytics.source")
	err := server.replayRecoveredRun(context.Background(), webscheduler.PipelineRun{
		ID:                       "working-tree-run",
		PipelineID:               "pipelines/old-name/pipeline.yml",
		PipelineUUID:             pipelineUUID,
		Environment:              "prod",
		ExecutionContextResolved: true,
		ExecutionTargetSnapshot: &webscheduler.ExecutionTargetSnapshot{
			Version:      webscheduler.ExecutionTargetSnapshotVersionV2,
			PipelineUUID: pipelineUUID,
			Entries: map[string]webscheduler.ExecutionTargetSnapshotEntry{
				"analytics.source": {
					AssetID:        identity.AssetID(pipelineUUID, "analytics.source"),
					TargetFidelity: "runtime_only",
					Fingerprint:    "v2:source",
					OwnContent:     "v2:source-own",
					VarsHash:       "vars",
				},
				"analytics.finished": {
					AssetID:          identity.AssetID(pipelineUUID, "analytics.finished"),
					TargetIdentity:   "duckdb:analytics.finished",
					TargetFidelity:   "exact",
					Fingerprint:      "v2:finished",
					OwnContent:       "v2:finished-own",
					ConsumedVarsHash: "consumed",
					VarsHash:         "vars",
					Upstreams: []webscheduler.ExecutionUpstreamSnapshot{{
						Type: "asset", Value: "analytics.source",
					}},
					CoverageMode:      "union_intervals",
					RefreshRestricted: true,
				},
			},
		},
	}, []webscheduler.PipelineRunStep{{
		Asset:                     "analytics.finished",
		Status:                    webscheduler.RunStatusSuccess,
		StartedAt:                 &startedAt,
		FinishedAt:                &finishedAt,
		CompletionOrdinal:         &ordinal,
		HasUpstreamWriterSnapshot: true,
		UpstreamWriters: map[string]webscheduler.UpstreamWriterSnapshot{
			upstreamAssetID: {
				AssetID:           upstreamAssetID,
				TargetIdentity:    "duckdb:analytics.source",
				Fingerprint:       "v2:source",
				VarsHash:          "vars",
				TargetGeneration:  3,
				CompletionID:      "source-completion",
				CompletionOrdinal: 1,
				MaterializedAt:    upstreamMaterializedAt,
			},
		},
	}}, nil)
	require.NoError(t, err)

	assert.Equal(t, pipelineUUID, got.PipelineUUID)
	assert.Equal(t, pipelineUUID, got.ExecutionPipelineUUID)
	assert.Equal(t, webscheduler.ExecutionTargetSnapshotVersionV2, got.ExecutionTargetSnapshotVersion)
	assert.Empty(t, got.SnapshotDir)
	require.Len(t, got.Assets, 1)
	assert.Equal(t, identity.AssetID(pipelineUUID, "analytics.finished"), got.Assets[0].AssetID)
	assert.True(t, got.Assets[0].HasUpstreamWriterSnapshot)
	assert.Equal(t, map[string]bus.UpstreamWriterSnapshot{
		upstreamAssetID: {
			AssetID:           upstreamAssetID,
			TargetIdentity:    "duckdb:analytics.source",
			Fingerprint:       "v2:source",
			VarsHash:          "vars",
			TargetGeneration:  3,
			CompletionID:      "source-completion",
			CompletionOrdinal: 1,
			MaterializedAt:    upstreamMaterializedAt,
		},
	}, got.Assets[0].UpstreamWriters)
	assert.Equal(t, []bus.ExecutionUpstreamSnapshot{{Type: "asset", Value: "analytics.source"}}, got.ExecutionTargets["analytics.finished"].Upstreams)
	assert.Equal(t, "union_intervals", got.ExecutionTargets["analytics.finished"].CoverageMode)
	assert.True(t, got.ExecutionTargets["analytics.finished"].RefreshRestricted)
}

func TestReplayRecoveredRunPreservesVersionFourExecutionContract(t *testing.T) {
	t.Parallel()

	events := bus.New()
	server := &webServer{eventBus: events}
	var got bus.RunCompleted
	events.OnRunCompleted(func(event bus.RunCompleted) error {
		got = event
		return nil
	})

	pipelineUUID := "pipeline-uuid"
	assetName := "analytics.finished"
	assetID := identity.AssetID(pipelineUUID, assetName)
	contract := webscheduler.PipelineRunExecutionContract{
		AssetID:        assetID,
		AssetName:      assetName,
		ConnectionKeys: []string{strings.Repeat("a", 64)},
		MutationResources: webscheduler.PipelineRunPlanResources{
			Isolation: "resources",
			Claims:    []webscheduler.PipelineRunResourceClaim{},
		},
		CoordinationResources: webscheduler.PipelineRunPlanResources{
			Isolation: "resources",
			Claims:    []webscheduler.PipelineRunResourceClaim{},
		},
	}
	finishedAt := time.Date(2026, 7, 27, 10, 0, 0, 0, time.UTC)
	ordinal := int64(0)
	err := server.replayRecoveredRun(context.Background(), webscheduler.PipelineRun{
		ID:                       "version-four-run",
		PipelineUUID:             pipelineUUID,
		ExecutionContextResolved: true,
		ExecutionTargetSnapshot: &webscheduler.ExecutionTargetSnapshot{
			Version:      webscheduler.ExecutionTargetSnapshotVersionV4,
			PipelineUUID: pipelineUUID,
			Entries: map[string]webscheduler.ExecutionTargetSnapshotEntry{
				assetName: {
					AssetID:               assetID,
					TargetFidelity:        "exact",
					WriteResourceKind:     "none",
					WriteResourceFidelity: "exact",
					ExecutionContract:     contract,
					Fingerprint:           "v2:finished",
					OwnContent:            "v2:finished-own",
					ConsumedVarsHash:      "consumed",
					VarsHash:              "vars",
				},
			},
		},
	}, []webscheduler.PipelineRunStep{{
		Asset:                     assetName,
		Status:                    webscheduler.RunStatusSuccess,
		FinishedAt:                &finishedAt,
		CompletionOrdinal:         &ordinal,
		UpstreamWriters:           map[string]webscheduler.UpstreamWriterSnapshot{},
		HasUpstreamWriterSnapshot: true,
	}}, nil)
	require.NoError(t, err)

	require.Equal(t, webscheduler.ExecutionTargetSnapshotVersionV4, got.ExecutionTargetSnapshotVersion)
	entry := got.ExecutionTargets[assetName]
	assert.Equal(t, assetID, entry.ExecutionContract.AssetID)
	assert.Equal(t, assetName, entry.ExecutionContract.AssetName)
	assert.Equal(t, []string{strings.Repeat("a", 64)}, entry.ExecutionContract.ConnectionKeys)
	assert.NoError(t, bus.ValidateExecutionContract(assetName, entry))
}

func TestReplayRecoveredRunPreservesVersionFiveExternalSourceEvidence(t *testing.T) {
	t.Parallel()

	events := bus.New()
	server := &webServer{eventBus: events}
	var got bus.RunCompleted
	events.OnRunCompleted(func(event bus.RunCompleted) error {
		got = event
		return nil
	})

	pipelineUUID := "pipeline-uuid"
	sourceName := "public.accounts"
	sourceID := identity.AssetID(pipelineUUID, sourceName)
	consumerName := "analytics.accounts"
	consumerID := identity.AssetID(pipelineUUID, consumerName)
	resourceOnlyContract := func(assetID, assetName string) webscheduler.PipelineRunExecutionContract {
		return webscheduler.PipelineRunExecutionContract{
			AssetID: assetID, AssetName: assetName, ConnectionKeys: []string{},
			MutationResources: webscheduler.PipelineRunPlanResources{
				Isolation: "resources", Claims: []webscheduler.PipelineRunResourceClaim{},
			},
			CoordinationResources: webscheduler.PipelineRunPlanResources{
				Isolation: "resources", Claims: []webscheduler.PipelineRunResourceClaim{},
			},
		}
	}
	finishedAt := time.Date(2026, 8, 11, 11, 0, 0, 0, time.UTC)
	ordinal := int64(0)
	require.NoError(t, server.replayRecoveredRun(context.Background(), webscheduler.PipelineRun{
		ID: "version-five-source-run", PipelineUUID: pipelineUUID, ExecutionContextResolved: true,
		ExecutionTargetSnapshot: &webscheduler.ExecutionTargetSnapshot{
			Version: webscheduler.ExecutionTargetSnapshotVersionV5, PipelineUUID: pipelineUUID,
			Entries: map[string]webscheduler.ExecutionTargetSnapshotEntry{
				sourceName: {
					AssetID: sourceID, ExternalSource: true, TargetFidelity: "runtime_only",
					WriteResourceKind: "pipeline", WriteResourceFidelity: "runtime_only",
					ExecutionContract: webscheduler.PipelineRunExecutionContract{
						AssetID: sourceID, AssetName: sourceName, ConnectionKeys: []string{},
						MutationResources: webscheduler.PipelineRunPlanResources{
							Isolation: "pipeline", Claims: []webscheduler.PipelineRunResourceClaim{},
						},
						CoordinationResources: webscheduler.PipelineRunPlanResources{
							Isolation: "pipeline", Claims: []webscheduler.PipelineRunResourceClaim{},
						},
					},
					Fingerprint: "v3:source", OwnContent: "v3:source-own",
					ConsumedVarsHash: "source-vars", VarsHash: "vars", CoverageMode: "marker",
				},
				consumerName: {
					AssetID: consumerID, TargetFidelity: "exact",
					WriteResourceKind: "none", WriteResourceFidelity: "exact",
					ExecutionContract: resourceOnlyContract(consumerID, consumerName),
					Fingerprint:       "v3:consumer", OwnContent: "v3:consumer-own",
					ConsumedVarsHash: "consumer-vars", VarsHash: "vars", CoverageMode: "marker",
					Upstreams: []webscheduler.ExecutionUpstreamSnapshot{{
						Type: "asset", Value: sourceName, ResolvedAssetID: sourceID,
					}},
				},
			},
		},
	}, []webscheduler.PipelineRunStep{{
		Asset: consumerName, Status: webscheduler.RunStatusSuccess,
		FinishedAt: &finishedAt, CompletionOrdinal: &ordinal,
		UpstreamWriters: map[string]webscheduler.UpstreamWriterSnapshot{}, HasUpstreamWriterSnapshot: true,
	}}, nil))

	require.Equal(t, webscheduler.ExecutionTargetSnapshotVersionV5, got.ExecutionTargetSnapshotVersion)
	assert.True(t, got.ExecutionTargets[sourceName].ExternalSource)
	assert.Equal(t, sourceID, got.ExecutionTargets[consumerName].Upstreams[0].ResolvedAssetID)
}

func TestReplayRecoveredRunUsesPersistedUnitCompletionCoordinates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	schedulerStore, err := webscheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer schedulerStore.Close()

	materializations := matlog.NewStore(schedulerStore.DB())
	engine := fingerprint.NewEngine()
	recorder := matlog.NewRecorder(materializations, engine, nil, nil, nil)
	events := bus.New()
	events.OnRunCompleted(recorder.HandleRunCompleted)
	server := &webServer{eventBus: events}

	pipelineUUID := "pipeline-uuid"
	assetName := "analytics.finished"
	assetID := identity.AssetID(pipelineUUID, assetName)
	start := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	finished := start.Add(time.Minute)
	unitFinished := finished.Add(15 * time.Millisecond)
	entry := bus.ExecutionTargetSnapshotEntry{
		AssetID:          assetID,
		TargetIdentity:   "duckdb:analytics.finished",
		TargetFidelity:   "exact",
		Fingerprint:      "v2:finished",
		OwnContent:       "v2:finished-own",
		ConsumedVarsHash: "consumed",
		VarsHash:         "vars",
		CoverageMode:     "marker",
	}
	runAsset := bus.AssetRun{
		AssetID:                   assetID,
		AssetName:                 assetName,
		Status:                    "succeeded",
		FinishedAt:                &finished,
		CompletionOrdinal:         0,
		HasCompletionOrdinal:      true,
		TargetIdentity:            entry.TargetIdentity,
		TargetFidelity:            entry.TargetFidelity,
		Fingerprint:               entry.Fingerprint,
		OwnContent:                entry.OwnContent,
		ConsumedVarsHash:          entry.ConsumedVarsHash,
		VarsHash:                  entry.VarsHash,
		UpstreamWriters:           map[string]bus.UpstreamWriterSnapshot{},
		HasUpstreamWriterSnapshot: true,
	}
	require.NoError(t, recorder.HandleRunCompleted(bus.RunCompleted{
		RunID:                          "interrupted-run",
		CompletionID:                   "interrupted-run/unit/0",
		PipelineUUID:                   pipelineUUID,
		Environment:                    "prod",
		WinStart:                       &start,
		WinEnd:                         &end,
		CompletedAt:                    finished,
		Assets:                         []bus.AssetRun{runAsset},
		ExecutionTargetSnapshotVersion: webscheduler.ExecutionTargetSnapshotVersionV2,
		ExecutionPipelineUUID:          pipelineUUID,
		ExecutionTargets:               map[string]bus.ExecutionTargetSnapshotEntry{assetName: entry},
	}))

	ordinal := int64(23)
	err = server.replayRecoveredRun(ctx, webscheduler.PipelineRun{
		ID:                       "interrupted-run",
		PipelineUUID:             pipelineUUID,
		Environment:              "prod",
		ExecutionContextResolved: true,
		ExecutionTargetSnapshot: &webscheduler.ExecutionTargetSnapshot{
			Version:      webscheduler.ExecutionTargetSnapshotVersionV2,
			PipelineUUID: pipelineUUID,
			Entries: map[string]webscheduler.ExecutionTargetSnapshotEntry{
				assetName: {
					AssetID:          assetID,
					TargetIdentity:   entry.TargetIdentity,
					TargetFidelity:   entry.TargetFidelity,
					Fingerprint:      entry.Fingerprint,
					OwnContent:       entry.OwnContent,
					ConsumedVarsHash: entry.ConsumedVarsHash,
					VarsHash:         entry.VarsHash,
					CoverageMode:     entry.CoverageMode,
				},
			},
		},
	}, []webscheduler.PipelineRunStep{{
		Asset:                     assetName,
		Status:                    webscheduler.RunStatusSuccess,
		FinishedAt:                &finished,
		CompletionOrdinal:         &ordinal,
		UpstreamWriters:           map[string]webscheduler.UpstreamWriterSnapshot{},
		HasUpstreamWriterSnapshot: true,
	}}, []webscheduler.PipelineRunUnit{{
		Position: 0, AssetID: assetID, AssetName: assetName,
		StartDate:  start.Format(time.RFC3339Nano),
		EndDate:    end.Format(time.RFC3339Nano),
		Status:     webscheduler.PipelineRunUnitSuccess,
		FinishedAt: &unitFinished,
	}, {
		Position: 1, AssetID: "unreached", AssetName: "analytics.unreached",
		StartDate:  start.Format(time.RFC3339Nano),
		EndDate:    end.Format(time.RFC3339Nano),
		Status:     webscheduler.PipelineRunUnitSkipped,
		FinishedAt: &finished,
	}})
	require.NoError(t, err, "unit-aware recovery must acknowledge an already committed unit fact")

	var completionID string
	var completionOrdinal int64
	require.NoError(t, schedulerStore.DB().QueryRowContext(ctx, `
		SELECT completion_id, completion_ordinal
		FROM renart_materializations
		WHERE asset_id = ? AND environment = ? AND run_id = ?`,
		assetID, "prod", "interrupted-run",
	).Scan(&completionID, &completionOrdinal))
	assert.Equal(t, "interrupted-run/unit/0", completionID)
	assert.Zero(t, completionOrdinal)
}

func TestReplayRecoveredDeployedRunDoesNotMaterializeSelfContainedV2Snapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	schedulerStore, err := webscheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer schedulerStore.Close()

	pipelineDir := filepath.Join(t.TempDir(), "analytics")
	require.NoError(t, os.MkdirAll(filepath.Join(pipelineDir, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineDir, "pipeline.yml"), []byte("name: analytics\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineDir, "assets", "finished.sql"), []byte("select 1\n"), 0o644))

	snapshotStore := snapshot.NewStore(schedulerStore.DB())
	deployed, _, err := snapshotStore.Deploy(ctx, "pipeline-uuid", pipelineDir, "test")
	require.NoError(t, err)
	assetHash := deployed.Manifest["assets/finished.sql"]
	_, err = schedulerStore.DB().ExecContext(ctx, `UPDATE renart_blobs SET content = ? WHERE hash = ?`, []byte("corrupt"), assetHash)
	require.NoError(t, err)

	events := bus.New()
	server := &webServer{snapshotStore: snapshotStore, eventBus: events}
	var got bus.RunCompleted
	events.OnRunCompleted(func(event bus.RunCompleted) error {
		got = event
		return nil
	})

	finishedAt := time.Date(2026, 7, 17, 10, 1, 0, 0, time.UTC)
	ordinal := int64(0)
	err = server.replayRecoveredRun(ctx, webscheduler.PipelineRun{
		ID:                       "deployed-v2-run",
		PipelineID:               "analytics",
		PipelineUUID:             "pipeline-uuid",
		SnapshotVersionID:        deployed.VersionID,
		ExecutionContextResolved: true,
		ExecutionTargetSnapshot: &webscheduler.ExecutionTargetSnapshot{
			Version:      webscheduler.ExecutionTargetSnapshotVersionV2,
			PipelineUUID: "pipeline-uuid",
			Entries: map[string]webscheduler.ExecutionTargetSnapshotEntry{
				"analytics.finished": {
					AssetID:          identity.AssetID("pipeline-uuid", "analytics.finished"),
					TargetIdentity:   "duckdb:analytics.finished",
					TargetFidelity:   "exact",
					Fingerprint:      "v2:finished",
					OwnContent:       "v2:finished-own",
					ConsumedVarsHash: "consumed",
					VarsHash:         "vars",
					CoverageMode:     "marker",
				},
			},
		},
	}, []webscheduler.PipelineRunStep{{
		Asset:                     "analytics.finished",
		Status:                    webscheduler.RunStatusSuccess,
		FinishedAt:                &finishedAt,
		CompletionOrdinal:         &ordinal,
		UpstreamWriters:           map[string]webscheduler.UpstreamWriterSnapshot{},
		HasUpstreamWriterSnapshot: true,
	}}, nil)
	require.NoError(t, err, "v2 recovery should read snapshot metadata without materializing corrupt blobs")

	assert.Equal(t, deployed.VersionID, got.SnapshotVersionID)
	assert.Empty(t, got.SnapshotDir)
	assert.Equal(t, webscheduler.ExecutionTargetSnapshotVersionV2, got.ExecutionTargetSnapshotVersion)
	assert.Equal(t, "marker", got.ExecutionTargets["analytics.finished"].CoverageMode)
}

func TestReplayRecoveredRunPropagatesCompletionPersistenceFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schedulerStore, err := webscheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer schedulerStore.Close()

	pipelineDir := filepath.Join(t.TempDir(), "analytics")
	require.NoError(t, os.MkdirAll(filepath.Join(pipelineDir, "assets"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineDir, "pipeline.yml"), []byte("name: analytics\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineDir, "assets", "finished.sql"), []byte("select 1\n"), 0o644))

	snapshotStore := snapshot.NewStore(schedulerStore.DB())
	deployed, _, err := snapshotStore.Deploy(ctx, "pipeline-uuid", pipelineDir, "test")
	require.NoError(t, err)
	events := bus.New()
	events.OnRunCompleted(func(bus.RunCompleted) error {
		return errors.New("completion subscriber failed")
	})
	server := &webServer{snapshotStore: snapshotStore, eventBus: events}

	err = server.replayRecoveredRun(ctx, webscheduler.PipelineRun{
		ID:                       "run-id",
		PipelineID:               "encoded-path",
		SnapshotVersionID:        deployed.VersionID,
		ExecutionContextResolved: true,
	}, []webscheduler.PipelineRunStep{{
		Asset:  "analytics.finished",
		Status: webscheduler.RunStatusSuccess,
	}}, nil)
	require.ErrorContains(t, err, "completion subscriber failed")
}

func TestRecoveredAssetRunStatusIgnoresNonTerminalSteps(t *testing.T) {
	for _, status := range []webscheduler.RunStatus{"", webscheduler.RunStatusQueued, webscheduler.RunStatusRunning} {
		mapped, ok := recoveredAssetRunStatus(status)
		assert.False(t, ok)
		assert.Empty(t, mapped)
	}
}

func TestReplayRecoveredRunSkipsSourceResolutionWithoutTerminalSteps(t *testing.T) {
	t.Parallel()
	events := bus.New()
	emitted := false
	events.OnRunCompleted(func(bus.RunCompleted) error { emitted = true; return nil })
	server := &webServer{eventBus: events}

	err := server.replayRecoveredRun(context.Background(), webscheduler.PipelineRun{
		ID: "pre-execution", SnapshotVersionID: "missing",
	}, []webscheduler.PipelineRunStep{{Asset: "analytics.pending", Status: webscheduler.RunStatusRunning}}, nil)
	require.NoError(t, err)
	assert.False(t, emitted)
}

func TestReplayRecoveredRunSkipsUnresolvedContextWithTerminalSteps(t *testing.T) {
	t.Parallel()
	events := bus.New()
	emitted := false
	events.OnRunCompleted(func(bus.RunCompleted) error { emitted = true; return nil })
	server := &webServer{eventBus: events}

	err := server.replayRecoveredRun(context.Background(), webscheduler.PipelineRun{
		ID: "legacy-unresolved", SnapshotVersionID: "must-not-be-resolved",
	}, []webscheduler.PipelineRunStep{{Asset: "analytics.finished", Status: webscheduler.RunStatusSuccess}}, nil)
	require.NoError(t, err)
	assert.False(t, emitted)
}

func TestReplayRecoveredRunRejectsCorruptPinnedSnapshotWithTerminalSteps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	schedulerStore, err := webscheduler.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer schedulerStore.Close()
	pipelineDir := filepath.Join(t.TempDir(), "analytics")
	require.NoError(t, os.MkdirAll(pipelineDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pipelineDir, "pipeline.yml"), []byte("name: analytics\n"), 0o644))
	snapshotStore := snapshot.NewStore(schedulerStore.DB())
	deployed, _, err := snapshotStore.Deploy(ctx, "pipeline-uuid", pipelineDir, "test")
	require.NoError(t, err)
	hash := deployed.Manifest["pipeline.yml"]
	_, err = schedulerStore.DB().ExecContext(ctx, `UPDATE renart_blobs SET content = ? WHERE hash = ?`, []byte("corrupt"), hash)
	require.NoError(t, err)

	events := bus.New()
	emitted := false
	events.OnRunCompleted(func(bus.RunCompleted) error { emitted = true; return nil })
	server := &webServer{snapshotStore: snapshotStore, eventBus: events}
	err = server.replayRecoveredRun(ctx, webscheduler.PipelineRun{
		ID: "corrupt-run", SnapshotVersionID: deployed.VersionID,
		ExecutionContextResolved: true,
	}, []webscheduler.PipelineRunStep{{Asset: "analytics.finished", Status: webscheduler.RunStatusSuccess}}, nil)
	require.ErrorContains(t, err, "blob hash mismatch")
	assert.False(t, emitted)
}
