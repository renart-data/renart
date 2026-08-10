package matlog_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"renart/internal/web/bus"
	"renart/internal/web/fingerprint"
	"renart/internal/web/identity"
	"renart/internal/web/matlog"
)

func TestRecorderUsesCapturedTargetsAndDurableLatestUpstreamWriter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openTestStore(t)
	engine := fingerprint.NewEngine()
	upstream := recorderSQLAsset("analytics.upstream", "select 1")
	downstream := recorderSQLAsset("analytics.downstream", "select * from analytics.upstream")
	downstream.Upstreams = []pipeline.Upstream{{Type: "asset", Value: upstream.Name}}
	pl := &pipeline.Pipeline{
		LegacyID: "pipeline-uuid", Name: "analytics",
		DefinitionFile: pipeline.DefinitionFile{Path: "/workspace/analytics/pipeline.yml"},
		Assets:         []*pipeline.Asset{upstream, downstream},
	}
	vars := fingerprint.EffectiveVars(pl, nil)
	results, err := engine.DAG(pl, vars)
	require.NoError(t, err)
	varsHash := fingerprint.AllVarsHash(vars)
	upstreamID := identity.AssetID(pl.LegacyID, upstream.Name)
	downstreamID := identity.AssetID(pl.LegacyID, downstream.Name)

	oldCompletion := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	require.NoError(t, store.Record(ctx, matlog.Materialization{
		AssetID: upstreamID, Environment: "prod",
		Fingerprint: string(results[upstreamID].FP), OwnContent: string(results[upstreamID].OwnContent),
		VarsHash: varsHash, RunID: "upstream-run", TargetIdentity: "target-upstream",
		CompletionID: "upstream-run", MaterializedAt: oldCompletion,
	}))
	_, err = store.Prune(ctx, oldCompletion.Add(time.Hour))
	require.NoError(t, err)
	legacyLatest, err := store.LatestFingerprint(ctx, []string{upstreamID}, "prod")
	require.NoError(t, err)
	assert.Empty(t, legacyLatest, "the raw fact was pruned; only the durable physical writer remains")

	finished := time.Date(2026, 7, 17, 12, 30, 0, 0, time.UTC)
	snapshotEntries := map[string]bus.ExecutionTargetSnapshotEntry{
		upstream.Name:   recorderTargetEntry(upstreamID, "target-upstream", results[upstreamID], varsHash),
		downstream.Name: recorderTargetEntry(downstreamID, "target-downstream", results[downstreamID], varsHash),
	}
	snapshotEntries[upstream.Name] = withCapturedSemantics(snapshotEntries[upstream.Name], upstream)
	snapshotEntries[downstream.Name] = withCapturedSemantics(snapshotEntries[downstream.Name], downstream)
	snapshotEntries[upstream.Name] = withCapturedWriteResource(snapshotEntries[upstream.Name])
	snapshotEntries[downstream.Name] = withCapturedWriteResource(snapshotEntries[downstream.Name])
	// Mutate the live graph after capture. Version-two recording must never ask
	// the resolver for this changed source or combine it with captured hashes.
	downstream.Upstreams = []pipeline.Upstream{{Type: "asset", Value: "analytics.different_upstream"}}
	resolveCalls := 0
	recorder := matlog.NewRecorder(store, engine, func(context.Context, string) (*pipeline.Pipeline, error) {
		resolveCalls++
		return pl, nil
	}, nil, nil)
	event := bus.RunCompleted{
		RunID: "downstream-run", CompletionID: "downstream-run",
		PipelineUUID: pl.LegacyID, Environment: "prod", CompletedAt: finished.Add(time.Minute),
		SnapshotVersionID:              "snapshot-7",
		ExecutionTargetSnapshotVersion: 3, ExecutionPipelineUUID: pl.LegacyID, ExecutionTargets: snapshotEntries,
		Assets: []bus.AssetRun{{
			AssetID: downstreamID, AssetName: downstream.Name, Status: "succeeded",
			FinishedAt: &finished, CompletionOrdinal: 0, HasCompletionOrdinal: true,
			UpstreamWriters: map[string]bus.UpstreamWriterSnapshot{
				upstreamID: {
					AssetID: upstreamID, TargetIdentity: "target-upstream",
					Fingerprint: string(results[upstreamID].FP), VarsHash: varsHash,
					TargetGeneration: 1, CompletionID: "upstream-run", MaterializedAt: oldCompletion,
				},
			},
			HasUpstreamWriterSnapshot: true,
			TargetIdentity:            "target-downstream", TargetFidelity: "exact",
			Fingerprint: string(results[downstreamID].FP), OwnContent: string(results[downstreamID].OwnContent),
			ConsumedVarsHash: results[downstreamID].ConsumedVarsHash, VarsHash: varsHash,
		}},
	}
	require.NoError(t, recorder.HandleRunCompleted(event))
	assert.Zero(t, resolveCalls, "a self-contained completion must not resolve mutable source")

	writers, err := store.LatestWriters(ctx, []string{"target-downstream"})
	require.NoError(t, err)
	writer := writers["target-downstream"]
	assert.Equal(t, downstreamID, writer.AssetID)
	assert.Equal(t, string(results[downstreamID].FP), writer.Fingerprint,
		"the downstream target is current even though its upstream raw fact was pruned")
	assert.Equal(t, finished, writer.MaterializedAt, "per-asset task completion orders the physical write")
	assert.Equal(t, "downstream-run", writer.CompletionID)
	assert.Equal(t, "snapshot-7", writer.SnapshotVersionID)

	coverage, err := store.CurrentTargetCoverage(ctx, map[string]string{downstreamID: "target-downstream"}, "prod", varsHash)
	require.NoError(t, err)
	require.Len(t, coverage[downstreamID], 1)
	assert.Equal(t, string(results[downstreamID].FP), coverage[downstreamID][0].Fingerprint)
}

func TestRecorderTreatsCapturedExternalSourceDeclarationAsAchievable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openTestStore(t)
	engine := fingerprint.NewEngine()
	source := &pipeline.Asset{
		Name: "public.accounts", Type: pipeline.AssetType("pg.source"),
	}
	consumer := recorderSQLAsset("analytics.accounts", "select * from public.accounts")
	consumer.Upstreams = []pipeline.Upstream{{Type: "asset", Value: source.Name}}
	pl := &pipeline.Pipeline{LegacyID: "pipeline-uuid", Name: "analytics", Assets: []*pipeline.Asset{source, consumer}}
	vars := fingerprint.EffectiveVars(pl, nil)
	results, err := engine.DAG(pl, vars)
	require.NoError(t, err)
	varsHash := fingerprint.AllVarsHash(vars)
	sourceID := identity.AssetID(pl.LegacyID, source.Name)
	consumerID := identity.AssetID(pl.LegacyID, consumer.Name)

	sourceEntry := withCapturedSemantics(recorderTargetEntry(sourceID, "", results[sourceID], varsHash), source)
	sourceEntry.ExternalSource = true
	sourceEntry.TargetFidelity = "runtime_only"
	sourceEntry.WriteResourceKind = "pipeline"
	sourceEntry.WriteResourceFidelity = "runtime_only"
	sourceEntry = withCapturedExecutionContract(sourceEntry, source.Name)
	consumerEntry := withCapturedWriteResource(
		withCapturedSemantics(recorderTargetEntry(consumerID, "target-consumer", results[consumerID], varsHash), consumer),
	)
	consumerEntry.Upstreams[0].ResolvedAssetID = sourceID
	consumerEntry = withCapturedExecutionContract(consumerEntry, consumer.Name)
	entries := map[string]bus.ExecutionTargetSnapshotEntry{
		source.Name: sourceEntry, consumer.Name: consumerEntry,
	}
	finished := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	recorder := matlog.NewRecorder(store, engine, nil, nil, nil)
	require.NoError(t, recorder.HandleRunCompleted(bus.RunCompleted{
		RunID: "consumer-run", CompletionID: "consumer-run", PipelineUUID: pl.LegacyID,
		Environment: "default", CompletedAt: finished,
		ExecutionTargetSnapshotVersion: 5, ExecutionPipelineUUID: pl.LegacyID,
		ExecutionTargets: entries,
		Assets:           []bus.AssetRun{capturedAssetRun(consumer.Name, consumerID, consumerEntry, finished)},
	}))

	writers, err := store.LatestWriters(ctx, []string{consumerEntry.TargetIdentity})
	require.NoError(t, err)
	require.Contains(t, writers, consumerEntry.TargetIdentity)
	assert.Equal(t, string(results[consumerID].FP), writers[consumerEntry.TargetIdentity].Fingerprint,
		"a successful consumer must achieve its current target without a source materialization fact")
}

func TestRecorderRejectsCompletionThatDiffersFromCapturedSnapshot(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	asset := recorderSQLAsset("analytics.orders", "select 1")
	pl := &pipeline.Pipeline{
		LegacyID: "pipeline-uuid", Name: "analytics",
		DefinitionFile: pipeline.DefinitionFile{Path: "/workspace/analytics/pipeline.yml"},
		Assets:         []*pipeline.Asset{asset},
	}
	engine := fingerprint.NewEngine()
	vars := fingerprint.EffectiveVars(pl, nil)
	results, err := engine.DAG(pl, vars)
	require.NoError(t, err)
	assetID := identity.AssetID(pl.LegacyID, asset.Name)
	entry := recorderTargetEntry(assetID, "target-orders", results[assetID], fingerprint.AllVarsHash(vars))
	recorder := matlog.NewRecorder(store, engine, func(context.Context, string) (*pipeline.Pipeline, error) {
		return pl, nil
	}, nil, nil)

	err = recorder.HandleRunCompleted(bus.RunCompleted{
		CompletionID: "completion-id", PipelineUUID: pl.LegacyID, Environment: "prod",
		CompletedAt: time.Now().UTC(), ExecutionTargetSnapshotVersion: 1,
		ExecutionTargets: map[string]bus.ExecutionTargetSnapshotEntry{asset.Name: entry},
		Assets: []bus.AssetRun{{
			AssetID: assetID, AssetName: asset.Name, Status: "succeeded",
			TargetIdentity: "different-target", TargetFidelity: entry.TargetFidelity,
			Fingerprint: entry.Fingerprint, OwnContent: entry.OwnContent,
			ConsumedVarsHash: entry.ConsumedVarsHash, VarsHash: entry.VarsHash,
		}},
	})
	require.ErrorContains(t, err, "does not match its execution target snapshot")
	writers, loadErr := store.LatestWriters(context.Background(), []string{"target-orders", "different-target"})
	require.NoError(t, loadErr)
	assert.Empty(t, writers)
}

func TestRecorderRequiresOperatorWriteEvidenceForSuccessfulPythonTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openTestStore(t)
	engine := fingerprint.NewEngine()
	asset := recorderSQLAsset("analytics.python_table", "def materialize(): return value")
	asset.Type = pipeline.AssetTypePython
	pl := &pipeline.Pipeline{LegacyID: "pipeline-uuid", Name: "analytics", Assets: []*pipeline.Asset{asset}}
	vars := fingerprint.EffectiveVars(pl, nil)
	results, err := engine.DAG(pl, vars)
	require.NoError(t, err)
	assetID := identity.AssetID(pl.LegacyID, asset.Name)
	varsHash := fingerprint.AllVarsHash(vars)
	entry := withCapturedSemantics(recorderTargetEntry(assetID, "target-python-table", results[assetID], varsHash), asset)
	entry.TargetWriteEvidenceRequired = true
	recorder := matlog.NewRecorder(store, engine, nil, nil, nil)

	completion := func(id string, finished time.Time) bus.RunCompleted {
		return bus.RunCompleted{
			RunID: id, CompletionID: id, PipelineUUID: pl.LegacyID, Environment: "default", CompletedAt: finished,
			ExecutionTargetSnapshotVersion: 2, ExecutionPipelineUUID: pl.LegacyID,
			ExecutionTargets: map[string]bus.ExecutionTargetSnapshotEntry{asset.Name: entry},
			Assets:           []bus.AssetRun{capturedAssetRun(asset.Name, assetID, entry, finished)},
		}
	}

	noOutputAt := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	require.NoError(t, recorder.HandleRunCompleted(completion("python-no-output", noOutputAt)))
	coverage, err := store.CurrentTargetCoverage(ctx, map[string]string{assetID: entry.TargetIdentity}, "default", varsHash)
	require.NoError(t, err)
	assert.Empty(t, coverage[assetID], "a successful None return must not claim physical freshness")
	runs, err := store.LastRuns(ctx, []string{assetID}, "default")
	require.NoError(t, err)
	assert.Equal(t, "succeeded", runs[assetID].Status, "the execution attempt remains successful")

	writtenAt := noOutputAt.Add(time.Hour)
	claim := matlog.TargetWriteClaim{
		TargetIdentity: entry.TargetIdentity, CompletionID: "python-with-output", AssetID: assetID, ClaimedAt: writtenAt.Add(-time.Second),
	}
	require.NoError(t, store.ClaimTargetWrite(ctx, claim))
	require.NoError(t, recorder.HandleRunCompleted(completion("python-with-output", writtenAt)))
	coverage, err = store.CurrentTargetCoverage(ctx, map[string]string{assetID: entry.TargetIdentity}, "default", varsHash)
	require.NoError(t, err)
	require.Len(t, coverage[assetID], 1)
	assert.Equal(t, string(results[assetID].FP), coverage[assetID][0].Fingerprint)
	evidence, err := store.HasTargetWriteEvidence(ctx, claim)
	require.NoError(t, err)
	assert.True(t, evidence, "the committed fact preserves replay evidence after its claim is cleared")
}

func TestRecorderRejectsVersionOneSnapshotAfterSourceTopologyChanges(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	upstream := recorderSQLAsset("analytics.upstream", "select 1")
	downstream := recorderSQLAsset("analytics.downstream", "select * from analytics.upstream")
	downstream.Upstreams = []pipeline.Upstream{{Type: "asset", Value: upstream.Name}}
	pl := &pipeline.Pipeline{LegacyID: "pipeline-uuid", Assets: []*pipeline.Asset{upstream, downstream}}
	engine := fingerprint.NewEngine()
	vars := fingerprint.EffectiveVars(pl, nil)
	results, err := engine.DAG(pl, vars)
	require.NoError(t, err)
	varsHash := fingerprint.AllVarsHash(vars)
	entries := map[string]bus.ExecutionTargetSnapshotEntry{
		upstream.Name:   recorderTargetEntry(identity.AssetID(pl.LegacyID, upstream.Name), "target-upstream", results[identity.AssetID(pl.LegacyID, upstream.Name)], varsHash),
		downstream.Name: recorderTargetEntry(identity.AssetID(pl.LegacyID, downstream.Name), "target-downstream", results[identity.AssetID(pl.LegacyID, downstream.Name)], varsHash),
	}

	// Version one did not persist topology, so replaying against this edited
	// source must stop rather than combine captured hashes with the new graph.
	downstream.Upstreams = nil
	recorder := matlog.NewRecorder(store, engine, func(context.Context, string) (*pipeline.Pipeline, error) {
		return pl, nil
	}, nil, nil)
	err = recorder.HandleRunCompleted(bus.RunCompleted{
		CompletionID: "completion-id", PipelineUUID: pl.LegacyID, Environment: "prod",
		ExecutionTargetSnapshotVersion: 1, ExecutionTargets: entries,
	})
	require.ErrorContains(t, err, "version-one execution target snapshot no longer matches parsed pipeline source")
}

func TestRecorderRejectsVersionOneDownstreamSuccessWithoutUpstreamSuccess(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	upstream := recorderSQLAsset("analytics.upstream", "select 1")
	downstream := recorderSQLAsset("analytics.downstream", "select * from analytics.upstream")
	downstream.Upstreams = []pipeline.Upstream{{Type: "asset", Value: upstream.Name}}
	pl := &pipeline.Pipeline{LegacyID: "pipeline-uuid", Assets: []*pipeline.Asset{upstream, downstream}}
	engine := fingerprint.NewEngine()
	vars := fingerprint.EffectiveVars(pl, nil)
	results, err := engine.DAG(pl, vars)
	require.NoError(t, err)
	varsHash := fingerprint.AllVarsHash(vars)
	upstreamID := identity.AssetID(pl.LegacyID, upstream.Name)
	downstreamID := identity.AssetID(pl.LegacyID, downstream.Name)
	entries := map[string]bus.ExecutionTargetSnapshotEntry{
		upstream.Name:   recorderTargetEntry(upstreamID, "target-upstream", results[upstreamID], varsHash),
		downstream.Name: recorderTargetEntry(downstreamID, "target-downstream", results[downstreamID], varsHash),
	}
	finished := time.Now().UTC()
	recorder := matlog.NewRecorder(store, engine, func(context.Context, string) (*pipeline.Pipeline, error) {
		return pl, nil
	}, nil, nil)

	err = recorder.HandleRunCompleted(bus.RunCompleted{
		CompletionID: "completion-id", PipelineUUID: pl.LegacyID, Environment: "prod",
		CompletedAt: finished, ExecutionTargetSnapshotVersion: 1, ExecutionTargets: entries,
		Assets: []bus.AssetRun{
			{
				AssetID: upstreamID, AssetName: upstream.Name, Status: "failed",
				TargetIdentity: entries[upstream.Name].TargetIdentity, TargetFidelity: entries[upstream.Name].TargetFidelity,
				Fingerprint: entries[upstream.Name].Fingerprint, OwnContent: entries[upstream.Name].OwnContent,
				ConsumedVarsHash: entries[upstream.Name].ConsumedVarsHash, VarsHash: entries[upstream.Name].VarsHash,
			},
			{
				AssetID: downstreamID, AssetName: downstream.Name, Status: "succeeded", FinishedAt: &finished,
				CompletionOrdinal: 1, HasCompletionOrdinal: true,
				TargetIdentity: entries[downstream.Name].TargetIdentity, TargetFidelity: entries[downstream.Name].TargetFidelity,
				Fingerprint: entries[downstream.Name].Fingerprint, OwnContent: entries[downstream.Name].OwnContent,
				ConsumedVarsHash: entries[downstream.Name].ConsumedVarsHash, VarsHash: entries[downstream.Name].VarsHash,
			},
		},
	})
	require.ErrorContains(t, err, "version-one evidence has no upstream writer snapshot")
}

func TestRecorderRejectsCapturedAssetNameIDMismatchAndIncompleteCoordinates(t *testing.T) {
	t.Parallel()
	store := openTestStore(t)
	asset := recorderSQLAsset("analytics.orders", "select 1")
	pl := &pipeline.Pipeline{LegacyID: "pipeline-uuid", Assets: []*pipeline.Asset{asset}}
	engine := fingerprint.NewEngine()
	vars := fingerprint.EffectiveVars(pl, nil)
	results, err := engine.DAG(pl, vars)
	require.NoError(t, err)
	assetID := identity.AssetID(pl.LegacyID, asset.Name)
	entry := withCapturedSemantics(
		recorderTargetEntry(assetID, "target-orders", results[assetID], fingerprint.AllVarsHash(vars)),
		asset,
	)
	recorder := matlog.NewRecorder(store, engine, nil, nil, nil)
	base := bus.RunCompleted{
		CompletionID: "completion-id", PipelineUUID: pl.LegacyID, Environment: "prod",
		ExecutionTargetSnapshotVersion: 2, ExecutionPipelineUUID: pl.LegacyID,
		ExecutionTargets: map[string]bus.ExecutionTargetSnapshotEntry{asset.Name: entry},
	}

	wrongName := base
	wrongName.Assets = []bus.AssetRun{capturedAssetRun("analytics.other", assetID, entry, time.Now().UTC())}
	require.ErrorContains(t, recorder.HandleRunCompleted(wrongName), "absent from the execution target snapshot")

	incomplete := base
	run := capturedAssetRun(asset.Name, assetID, entry, time.Now().UTC())
	run.FinishedAt = nil
	incomplete.Assets = []bus.AssetRun{run}
	require.ErrorContains(t, recorder.HandleRunCompleted(incomplete), "incomplete completion coordinates")
}

func TestRecorderAcknowledgesCommittedAmbiguousWriterAndFailsFreshnessClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openTestStore(t)
	asset := recorderSQLAsset("analytics.orders", "select 1")
	pl := &pipeline.Pipeline{LegacyID: "pipeline-uuid", Assets: []*pipeline.Asset{asset}}
	engine := fingerprint.NewEngine()
	vars := fingerprint.EffectiveVars(pl, nil)
	results, err := engine.DAG(pl, vars)
	require.NoError(t, err)
	varsHash := fingerprint.AllVarsHash(vars)
	assetID := identity.AssetID(pl.LegacyID, asset.Name)
	entry := withCapturedSemantics(recorderTargetEntry(assetID, "target-orders", results[assetID], varsHash), asset)
	recorder := matlog.NewRecorder(store, engine, nil, nil, nil)
	finished := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	event := func(completionID string) bus.RunCompleted {
		return bus.RunCompleted{
			RunID: completionID, CompletionID: completionID, PipelineUUID: pl.LegacyID,
			Environment: "prod", CompletedAt: finished,
			ExecutionTargetSnapshotVersion: 2, ExecutionPipelineUUID: pl.LegacyID,
			ExecutionTargets: map[string]bus.ExecutionTargetSnapshotEntry{asset.Name: entry},
			Assets:           []bus.AssetRun{capturedAssetRun(asset.Name, assetID, entry, finished)},
		}
	}

	require.NoError(t, recorder.HandleRunCompleted(event("completion-one")))
	require.NoError(t, recorder.HandleRunCompleted(event("completion-two")),
		"ambiguity is a committed fail-closed outcome, not a retryable recorder failure")
	coverage, err := store.CurrentTargetCoverage(ctx, map[string]string{assetID: entry.TargetIdentity}, "prod", varsHash)
	require.NoError(t, err)
	assert.Empty(t, coverage[assetID])
}

func TestRecorderUsesWriterVisibleAtConsumerStartNotLaterConcurrentWriter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openTestStore(t)
	engine := fingerprint.NewEngine()
	upstream := recorderSQLAsset("analytics.upstream", "select 1")
	downstream := recorderSQLAsset("analytics.downstream", "select * from analytics.upstream")
	downstream.Upstreams = []pipeline.Upstream{{Type: "asset", Value: upstream.Name}}
	pl := &pipeline.Pipeline{LegacyID: "pipeline-uuid", Assets: []*pipeline.Asset{upstream, downstream}}
	vars := fingerprint.EffectiveVars(pl, nil)
	results, err := engine.DAG(pl, vars)
	require.NoError(t, err)
	varsHash := fingerprint.AllVarsHash(vars)
	upstreamID := identity.AssetID(pl.LegacyID, upstream.Name)
	downstreamID := identity.AssetID(pl.LegacyID, downstream.Name)

	oldTime := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	require.NoError(t, store.Record(ctx, matlog.Materialization{
		AssetID: upstreamID, Environment: "prod", Fingerprint: string(results[upstreamID].FP),
		OwnContent: string(results[upstreamID].OwnContent), VarsHash: varsHash,
		RunID: "old-upstream", TargetIdentity: "target-upstream", CompletionID: "old-upstream",
		MaterializedAt: oldTime,
	}))
	writersAtStart, err := store.LatestWriters(ctx, []string{"target-upstream"})
	require.NoError(t, err)
	visible := writersAtStart["target-upstream"]

	// A different code/variable variant wins the upstream target after the
	// downstream task has started but before its completion is recorded.
	newTime := oldTime.Add(time.Minute)
	require.NoError(t, store.Record(ctx, matlog.Materialization{
		AssetID: upstreamID, Environment: "prod", Fingerprint: "v1:newer-upstream-variant",
		OwnContent: "v1:newer-own", VarsHash: "newer-vars", RunID: "new-upstream",
		TargetIdentity: "target-upstream", CompletionID: "new-upstream", MaterializedAt: newTime,
	}))

	entries := map[string]bus.ExecutionTargetSnapshotEntry{
		upstream.Name: withCapturedSemantics(
			recorderTargetEntry(upstreamID, "target-upstream", results[upstreamID], varsHash), upstream,
		),
		downstream.Name: withCapturedSemantics(
			recorderTargetEntry(downstreamID, "target-downstream", results[downstreamID], varsHash), downstream,
		),
	}
	finished := newTime.Add(time.Minute)
	run := capturedAssetRun(downstream.Name, downstreamID, entries[downstream.Name], finished)
	run.UpstreamWriters = map[string]bus.UpstreamWriterSnapshot{
		upstreamID: {
			AssetID: visible.AssetID, TargetIdentity: visible.TargetIdentity,
			Fingerprint: visible.Fingerprint, VarsHash: visible.VarsHash,
			TargetGeneration: visible.TargetGeneration, CompletionID: visible.CompletionID,
			CompletionOrdinal: visible.CompletionOrdinal, MaterializedAt: visible.MaterializedAt,
		},
	}
	recorder := matlog.NewRecorder(store, engine, nil, nil, nil)
	require.NoError(t, recorder.HandleRunCompleted(bus.RunCompleted{
		RunID: "downstream-run", CompletionID: "downstream-run", PipelineUUID: pl.LegacyID,
		Environment: "prod", CompletedAt: finished,
		ExecutionTargetSnapshotVersion: 2, ExecutionPipelineUUID: pl.LegacyID,
		ExecutionTargets: entries, Assets: []bus.AssetRun{run},
	}))

	expected, err := engine.AchievedFingerprintsByConsumer(pl, results, map[string]bool{downstreamID: true},
		func(_, upstreamAssetID string) (fingerprint.Fingerprint, bool) {
			if upstreamAssetID == upstreamID {
				return fingerprint.Fingerprint(visible.Fingerprint), true
			}
			return "", false
		})
	require.NoError(t, err)
	latest, err := store.LatestWriters(ctx, []string{"target-downstream"})
	require.NoError(t, err)
	assert.Equal(t, string(expected[downstreamID]), latest["target-downstream"].Fingerprint)
	wrong, err := engine.AchievedFingerprintsByConsumer(pl, results, map[string]bool{downstreamID: true},
		func(_, upstreamAssetID string) (fingerprint.Fingerprint, bool) {
			if upstreamAssetID == upstreamID {
				return "v1:newer-upstream-variant", true
			}
			return "", false
		})
	require.NoError(t, err)
	assert.NotEqual(t, string(wrong[downstreamID]), latest["target-downstream"].Fingerprint)
}

func recorderSQLAsset(name, content string) *pipeline.Asset {
	return &pipeline.Asset{
		Name: name, Type: pipeline.AssetTypeDuckDBQuery,
		ExecutableFile:  pipeline.ExecutableFile{Path: "/workspace/analytics/assets/" + name + ".sql", Content: content},
		Materialization: pipeline.Materialization{Type: pipeline.MaterializationTypeTable},
	}
}

func recorderTargetEntry(assetID, targetIdentity string, result fingerprint.Result, varsHash string) bus.ExecutionTargetSnapshotEntry {
	return bus.ExecutionTargetSnapshotEntry{
		AssetID: assetID, TargetIdentity: targetIdentity, TargetFidelity: "exact",
		Fingerprint: string(result.FP), OwnContent: string(result.OwnContent),
		ConsumedVarsHash: result.ConsumedVarsHash, VarsHash: varsHash,
	}
}

func withCapturedWriteResource(entry bus.ExecutionTargetSnapshotEntry) bus.ExecutionTargetSnapshotEntry {
	entry.WriteResourceKind = "duckdb_database"
	entry.WriteResourceIdentity = strings.Repeat("a", 64)
	entry.WriteResourceFidelity = "exact"
	return entry
}

func withCapturedSemantics(entry bus.ExecutionTargetSnapshotEntry, asset *pipeline.Asset) bus.ExecutionTargetSnapshotEntry {
	entry.CoverageMode = "marker"
	entry.Upstreams = make([]bus.ExecutionUpstreamSnapshot, 0, len(asset.Upstreams))
	for _, upstream := range asset.Upstreams {
		entry.Upstreams = append(entry.Upstreams, bus.ExecutionUpstreamSnapshot{Type: upstream.Type, Value: upstream.Value})
	}
	return entry
}

func withCapturedExecutionContract(entry bus.ExecutionTargetSnapshotEntry, assetName string) bus.ExecutionTargetSnapshotEntry {
	mutation := bus.ExecutionResources{Isolation: "pipeline", Claims: []bus.ExecutionResourceClaim{}}
	if entry.WriteResourceFidelity == "exact" {
		mutation.Isolation = "resources"
		if entry.WriteResourceKind != "none" {
			mutation.Claims = []bus.ExecutionResourceClaim{{
				Kind: entry.WriteResourceKind, Identity: entry.WriteResourceIdentity,
			}}
		}
	}
	coordination := bus.ExecutionResources{
		Isolation: mutation.Isolation,
		Claims:    append([]bus.ExecutionResourceClaim(nil), mutation.Claims...),
	}
	entry.ExecutionContract = bus.ExecutionContractSnapshot{
		AssetID: entry.AssetID, AssetName: assetName,
		ConnectionKeys: []string{}, MutationResources: mutation, CoordinationResources: coordination,
	}
	return entry
}

func capturedAssetRun(assetName, assetID string, entry bus.ExecutionTargetSnapshotEntry, finished time.Time) bus.AssetRun {
	return bus.AssetRun{
		AssetID: assetID, AssetName: assetName, Status: "succeeded", FinishedAt: &finished,
		CompletionOrdinal: 0, HasCompletionOrdinal: true,
		UpstreamWriters: map[string]bus.UpstreamWriterSnapshot{}, HasUpstreamWriterSnapshot: true,
		TargetIdentity: entry.TargetIdentity, TargetFidelity: entry.TargetFidelity,
		Fingerprint: entry.Fingerprint, OwnContent: entry.OwnContent,
		ConsumedVarsHash: entry.ConsumedVarsHash, VarsHash: entry.VarsHash,
	}
}
