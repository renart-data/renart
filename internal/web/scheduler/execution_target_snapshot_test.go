package scheduler

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorePersistsImmutableExecutionTargetSnapshotForRunAndRecovery(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()

	runID, err := store.Create(ctx, PipelineRun{
		ID: "target-run", PipelineID: "pipeline-id", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)
	snapshot := testExecutionTargetSnapshot()
	require.NoError(t, store.SetRunExecutionTargetSnapshot(ctx, runID, snapshot))
	require.NoError(t, store.SetRunExecutionTargetSnapshot(ctx, runID, snapshot), "an exact retry is idempotent")

	run, _, _, err := store.Get(ctx, runID)
	require.NoError(t, err)
	require.NotNil(t, run.ExecutionTargetSnapshot)
	assert.Equal(t, snapshot, *run.ExecutionTargetSnapshot)
	runs, err := store.List(ctx, RunFilter{})
	require.NoError(t, err)
	require.Len(t, runs.Runs, 1)
	require.NotNil(t, runs.Runs[0].ExecutionTargetSnapshot)
	assert.Equal(t, snapshot, *runs.Runs[0].ExecutionTargetSnapshot)

	conflict := testExecutionTargetSnapshot()
	entry := conflict.Entries["analytics.orders"]
	entry.Fingerprint = "v2:changed"
	conflict.Entries["analytics.orders"] = entry
	require.ErrorIs(t, store.SetRunExecutionTargetSnapshot(ctx, runID, conflict), ErrExecutionTargetSnapshotConflict)
	run, _, _, err = store.Get(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, snapshot, *run.ExecutionTargetSnapshot, "conflicting evidence cannot replace recovery provenance")

	require.NoError(t, store.Finish(ctx, runID, RunStatusSuccess, nil))
	require.ErrorContains(t, store.SetRunExecutionTargetSnapshot(ctx, runID, snapshot), "already terminal")
}

func TestStorePersistsVersionFiveExternalSourceEvidence(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	runID, err := store.Create(ctx, PipelineRun{
		ID: "source-target-run", PipelineID: "pipeline-id", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)
	assetID := "pipeline-uuid:public.accounts"
	snapshot := ExecutionTargetSnapshot{
		Version: ExecutionTargetSnapshotVersionV5, PipelineUUID: "pipeline-uuid",
		Entries: map[string]ExecutionTargetSnapshotEntry{
			"public.accounts": {
				AssetID: assetID, ExternalSource: true, TargetFidelity: ExecutionTargetFidelityRuntimeOnly,
				WriteResourceKind: "pipeline", WriteResourceFidelity: ExecutionTargetFidelityRuntimeOnly,
				ExecutionContract: PipelineRunExecutionContract{
					AssetID: assetID, AssetName: "public.accounts", ConnectionKeys: []string{},
					MutationResources: PipelineRunPlanResources{
						Isolation: PipelineRunResourceIsolationPipeline, Claims: []PipelineRunResourceClaim{},
					},
					CoordinationResources: PipelineRunPlanResources{
						Isolation: PipelineRunResourceIsolationPipeline, Claims: []PipelineRunResourceClaim{},
					},
				},
				Fingerprint: "v3:source", OwnContent: "v3:source-own",
				ConsumedVarsHash: "consumed-source", VarsHash: "all-vars", CoverageMode: "marker",
			},
		},
	}

	require.NoError(t, store.SetRunExecutionTargetSnapshot(ctx, runID, snapshot))
	run, _, _, err := store.Get(ctx, runID)
	require.NoError(t, err)
	require.NotNil(t, run.ExecutionTargetSnapshot)
	assert.True(t, run.ExecutionTargetSnapshot.Entries["public.accounts"].ExternalSource)
}

func TestStoreValidatesExecutionTargetSnapshotIdentity(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	runID, err := store.Create(ctx, PipelineRun{
		ID: "target-validation", PipelineID: "pipeline-id", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)

	tests := []struct {
		name   string
		mutate func(*ExecutionTargetSnapshot)
	}{
		{name: "version", mutate: func(snapshot *ExecutionTargetSnapshot) { snapshot.Version = 99 }},
		{name: "configuration digest", mutate: func(snapshot *ExecutionTargetSnapshot) { snapshot.ConfigurationDigest = "short" }},
		{name: "configuration fidelity", mutate: func(snapshot *ExecutionTargetSnapshot) { snapshot.ConfigurationFidelity = "best_effort" }},
		{name: "runtime configuration with digest", mutate: func(snapshot *ExecutionTargetSnapshot) {
			snapshot.ConfigurationFidelity = ExecutionTargetFidelityRuntimeOnly
		}},
		{name: "empty entries", mutate: func(snapshot *ExecutionTargetSnapshot) { snapshot.Entries = nil }},
		{name: "noncanonical name", mutate: func(snapshot *ExecutionTargetSnapshot) {
			entry := snapshot.Entries["analytics.orders"]
			delete(snapshot.Entries, "analytics.orders")
			snapshot.Entries[" analytics.orders"] = entry
		}},
		{name: "missing asset id", mutate: func(snapshot *ExecutionTargetSnapshot) {
			entry := snapshot.Entries["analytics.orders"]
			entry.AssetID = ""
			snapshot.Entries["analytics.orders"] = entry
		}},
		{name: "duplicate asset id", mutate: func(snapshot *ExecutionTargetSnapshot) {
			entry := snapshot.Entries["analytics.sensor"]
			entry.AssetID = snapshot.Entries["analytics.orders"].AssetID
			snapshot.Entries["analytics.sensor"] = entry
		}},
		{name: "runtime identity", mutate: func(snapshot *ExecutionTargetSnapshot) {
			entry := snapshot.Entries["analytics.sensor"]
			entry.TargetFidelity = ExecutionTargetFidelityRuntimeOnly
			entry.TargetIdentity = "renart-physical-target-v1:false-claim"
			snapshot.Entries["analytics.sensor"] = entry
		}},
		{name: "write evidence without target", mutate: func(snapshot *ExecutionTargetSnapshot) {
			entry := snapshot.Entries["analytics.sensor"]
			entry.TargetWriteEvidenceRequired = true
			snapshot.Entries["analytics.sensor"] = entry
		}},
		{name: "unknown fidelity", mutate: func(snapshot *ExecutionTargetSnapshot) {
			entry := snapshot.Entries["analytics.orders"]
			entry.TargetFidelity = "semantic"
			snapshot.Entries["analytics.orders"] = entry
		}},
		{name: "missing fingerprint evidence", mutate: func(snapshot *ExecutionTargetSnapshot) {
			entry := snapshot.Entries["analytics.orders"]
			entry.OwnContent = ""
			snapshot.Entries["analytics.orders"] = entry
		}},
		{name: "external source before version five", mutate: func(snapshot *ExecutionTargetSnapshot) {
			entry := snapshot.Entries["analytics.orders"]
			entry.ExternalSource = true
			snapshot.Entries["analytics.orders"] = entry
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := testExecutionTargetSnapshot()
			tt.mutate(&snapshot)
			require.ErrorIs(t, store.SetRunExecutionTargetSnapshot(ctx, runID, snapshot), ErrInvalidExecutionTargetSnapshot)
		})
	}

	run, _, _, err := store.Get(ctx, runID)
	require.NoError(t, err)
	assert.Nil(t, run.ExecutionTargetSnapshot)
}

func TestStoreRejectsInvalidPersistedExecutionTargetSnapshotOnLoad(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	runID, err := store.Create(ctx, PipelineRun{
		ID: "invalid-target", PipelineID: "pipeline-id", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)
	_, err = store.db.ExecContext(ctx, `
		UPDATE pipeline_runs
		SET execution_target_snapshot = '{"version":2,"entries":{}}'
		WHERE id = ?`, runID)
	require.NoError(t, err)
	_, _, _, err = store.Get(ctx, runID)
	require.ErrorIs(t, err, ErrInvalidExecutionTargetSnapshot)
	_, err = store.List(ctx, RunFilter{})
	require.ErrorIs(t, err, ErrInvalidExecutionTargetSnapshot)
}

func TestStoreRejectsFirstExecutionTargetCaptureAfterAStepStarted(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	runID, err := store.Create(ctx, PipelineRun{
		ID: "late-target", PipelineID: "pipeline-id", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)
	require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{
		RunID: runID, Asset: "analytics.orders", Status: RunStatusRunning,
	}))
	require.ErrorContains(
		t,
		store.SetRunExecutionTargetSnapshot(ctx, runID, testExecutionTargetSnapshot()),
		"after the first step started",
	)
	run, _, _, err := store.Get(ctx, runID)
	require.NoError(t, err)
	assert.Nil(t, run.ExecutionTargetSnapshot)
}

func TestStoreAssignsStableDeterministicTerminalStepOrdinals(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	runID, err := store.Create(ctx, PipelineRun{
		ID: "ordinal-run", PipelineID: "pipeline-id", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)
	started := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	finished := started.Add(time.Minute)

	for _, asset := range []string{"zeta", "alpha", "second", "first"} {
		require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{
			RunID: runID, Asset: asset, Status: RunStatusRunning, StartedAt: &started,
		}))
	}
	require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{
		RunID: runID, Asset: "second", Status: RunStatusSuccess, StartedAt: &started, FinishedAt: &finished,
	}))
	require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{
		RunID: runID, Asset: "first", Status: RunStatusSuccess, StartedAt: &started, FinishedAt: &finished,
	}))
	require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{
		RunID: runID, Asset: "second", Status: RunStatusSuccess, StartedAt: &started, FinishedAt: &finished,
	}), "terminal replay preserves its original ordinal")
	require.NoError(t, store.FinishOpenSteps(ctx, runID, RunStatusFailed, finished, assert.AnError))

	steps, err := store.ListSteps(ctx, runID)
	require.NoError(t, err)
	require.Len(t, steps, 4)
	assert.Equal(t, []string{"second", "first", "alpha", "zeta"}, []string{
		steps[0].Asset, steps[1].Asset, steps[2].Asset, steps[3].Asset,
	})
	for ordinal, step := range steps {
		require.NotNil(t, step.CompletionOrdinal)
		assert.EqualValues(t, ordinal, *step.CompletionOrdinal)
	}

	conflicting := int64(99)
	require.ErrorContains(t, store.UpsertStep(ctx, PipelineRunStep{
		RunID: runID, Asset: "second", Status: RunStatusSuccess,
		StartedAt: &started, FinishedAt: &finished, CompletionOrdinal: &conflicting,
	}), "already 0")
}

func TestExecutionTargetSnapshotMigrationBackfillsStepOrderAndDowngradesCleanly(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := OpenStore(path)
	require.NoError(t, err)
	ctx := context.Background()
	migrations, err := fs.Sub(schedulerMigrations, "storedb/migrations")
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectSQLite3, store.db, migrations)
	require.NoError(t, err)
	_, err = provider.DownTo(ctx, 13)
	require.NoError(t, err)
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO pipeline_runs (id, pipeline_id, pipeline, environment, trigger, status)
		VALUES ('legacy-target-run', 'pipeline-id', 'analytics', 'prod', 'manual', 'running')`)
	require.NoError(t, err)
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO pipeline_run_steps (run_id, asset, status, started_at, finished_at)
		VALUES
			('legacy-target-run', 'later', 'success', '2026-07-17T10:00:00Z', '2026-07-17T10:02:00Z'),
			('legacy-target-run', 'same-b', 'failed', '2026-07-17T10:00:00Z', '2026-07-17T10:01:00Z'),
			('legacy-target-run', 'same-a', 'success', '2026-07-17T10:00:00Z', '2026-07-17T10:01:00Z'),
			('legacy-target-run', 'open', 'running', '2026-07-17T10:03:00Z', NULL)`)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	store, err = OpenStore(path)
	require.NoError(t, err)
	var snapshot string
	require.NoError(t, store.db.QueryRowContext(ctx, `
		SELECT execution_target_snapshot FROM pipeline_runs WHERE id = 'legacy-target-run'`).Scan(&snapshot))
	assert.Empty(t, snapshot)
	steps, err := store.ListSteps(ctx, "legacy-target-run")
	require.NoError(t, err)
	require.Len(t, steps, 4)
	assert.Equal(t, []string{"same-a", "same-b", "later", "open"}, []string{
		steps[0].Asset, steps[1].Asset, steps[2].Asset, steps[3].Asset,
	})
	for ordinal := 0; ordinal < 3; ordinal++ {
		require.NotNil(t, steps[ordinal].CompletionOrdinal)
		assert.EqualValues(t, ordinal, *steps[ordinal].CompletionOrdinal)
	}
	assert.Nil(t, steps[3].CompletionOrdinal)

	migrations, err = fs.Sub(schedulerMigrations, "storedb/migrations")
	require.NoError(t, err)
	provider, err = goose.NewProvider(goose.DialectSQLite3, store.db, migrations)
	require.NoError(t, err)
	_, err = provider.DownTo(ctx, 13)
	require.NoError(t, err)
	assert.Equal(t, 0, countRows(t, store, `
		SELECT COUNT(*) FROM pragma_table_info('pipeline_runs')
		WHERE name = 'execution_target_snapshot'`))
	assert.Equal(t, 0, countRows(t, store, `
		SELECT COUNT(*) FROM pragma_table_info('pipeline_run_steps')
		WHERE name = 'completion_ordinal'`))
	require.NoError(t, store.Close())
}

func testExecutionTargetSnapshot() ExecutionTargetSnapshot {
	return ExecutionTargetSnapshot{
		Version: ExecutionTargetSnapshotVersionV2, PipelineUUID: "pipeline-uuid",
		ConfigurationDigest: strings.Repeat("c", 64), ConfigurationFidelity: ExecutionTargetFidelityExact,
		Entries: map[string]ExecutionTargetSnapshotEntry{
			"analytics.orders": {
				AssetID:                     "pipeline-uuid:analytics.orders",
				TargetIdentity:              "renart-physical-target-v1:orders",
				TargetFidelity:              ExecutionTargetFidelityExact,
				TargetWriteEvidenceRequired: true,
				Fingerprint:                 "v2:orders",
				OwnContent:                  "v2:orders-own",
				ConsumedVarsHash:            "consumed-orders",
				VarsHash:                    "all-vars",
				CoverageMode:                "marker",
			},
			"analytics.sensor": {
				AssetID:          "pipeline-uuid:analytics.sensor",
				TargetFidelity:   ExecutionTargetFidelityExact,
				Fingerprint:      "v2:sensor",
				OwnContent:       "v2:sensor-own",
				ConsumedVarsHash: "consumed-sensor",
				VarsHash:         "all-vars",
				CoverageMode:     "marker",
			},
		},
	}
}
