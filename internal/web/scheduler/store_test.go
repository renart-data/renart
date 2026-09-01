package scheduler

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorePersistsAndValidatesVersionedRunSpec(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	start := time.Date(2026, 7, 16, 8, 0, 0, 123456789, time.UTC)
	end := start.Add(time.Hour)
	executionTime := start.Add(30 * time.Minute)
	run := PipelineRun{
		ID: "spec-run", PipelineID: "pipeline-id", Pipeline: "analytics",
		Environment: "prod", Trigger: RunTriggerManual, Status: RunStatusQueued,
		WinStart: &start, WinEnd: &end, SnapshotVersionID: "snapshot-id",
		FullRefresh: true, SensorMode: "skip", ExecutionTime: &executionTime,
		ExpectedSourceMerkle: strings.Repeat("a", 64), ExpectedConfigurationDigest: strings.Repeat("b", 64),
	}
	spec := manualRunSpec(run, RunSourceSnapshot, "prod")
	runID, err := store.CreateWithSpec(context.Background(), run, spec)
	require.NoError(t, err)
	assert.Equal(t, run.ID, runID)

	persisted, found, err := store.GetRunSpec(context.Background(), runID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, spec, persisted)
	assert.Equal(t, executionTime, *persisted.Requested.ExecutionTime)
	require.NotNil(t, persisted.Expected)
	assert.Equal(t, strings.Repeat("a", 64), persisted.Expected.SourceMerkle)
	assert.Equal(t, strings.Repeat("b", 64), persisted.Expected.ConfigurationDigest)

	_, err = store.db.Exec(`UPDATE pipeline_run_specs SET version = 4, body = json_set(body, '$.version', 4) WHERE run_id = ?`, runID)
	require.NoError(t, err)
	_, found, err = store.GetRunSpec(context.Background(), runID)
	require.True(t, found)
	require.ErrorContains(t, err, "unsupported run spec version 4")

	_, err = store.db.Exec(`UPDATE pipeline_run_specs SET version = 1, body = json_set(body, '$.version', 1, '$.future_behavior', true) WHERE run_id = ?`, runID)
	require.NoError(t, err)
	_, found, err = store.GetRunSpec(context.Background(), runID)
	require.True(t, found)
	require.ErrorContains(t, err, "unknown field")
}

func TestOpenStoreEncodesBoundTimesAsCanonicalUTC(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	local := time.FixedZone("test-local", 2*60*60)
	bound := time.Date(2026, 7, 20, 20, 30, 30, 123456789, local)
	var encoded string
	require.NoError(t, store.db.QueryRow(`SELECT CAST(? AS TEXT)`, bound).Scan(&encoded))
	assert.Equal(t, "2026-07-20 18:30:30.123456789+00:00", encoded)
	assert.NotContains(t, encoded, "m=")
	var parseable bool
	require.NoError(t, store.db.QueryRow(`SELECT julianday(?) IS NOT NULL`, bound).Scan(&parseable))
	assert.True(t, parseable)
}

func TestSetRunRiverJobReleasesOnlyTerminalHistoricalLink(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("terminal link", func(t *testing.T) {
		t.Parallel()
		store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
		require.NoError(t, err)
		defer store.Close()

		reusedJobID := int64(42)
		terminalID, err := store.Create(ctx, PipelineRun{
			ID: "terminal", PipelineID: "old-pipeline", Pipeline: "old",
			Trigger: RunTriggerSchedule, Status: RunStatusSuccess, RiverJobID: &reusedJobID,
		})
		require.NoError(t, err)
		queuedID, err := store.Create(ctx, PipelineRun{
			ID: "queued", PipelineID: "new-pipeline", Pipeline: "new",
			Trigger: RunTriggerSchedule, Status: RunStatusQueued,
		})
		require.NoError(t, err)

		require.NoError(t, store.SetRunRiverJob(ctx, queuedID, reusedJobID))

		terminal, _, _, err := store.Get(ctx, terminalID)
		require.NoError(t, err)
		assert.Nil(t, terminal.RiverJobID)
		queued, _, _, err := store.Get(ctx, queuedID)
		require.NoError(t, err)
		require.NotNil(t, queued.RiverJobID)
		assert.Equal(t, reusedJobID, *queued.RiverJobID)
		linkedRunID, found, err := store.RunIDForRiverJob(ctx, reusedJobID)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, queuedID, linkedRunID)
	})

	t.Run("active link", func(t *testing.T) {
		t.Parallel()
		store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
		require.NoError(t, err)
		defer store.Close()

		reusedJobID := int64(42)
		activeID, err := store.Create(ctx, PipelineRun{
			ID: "active", PipelineID: "active-pipeline", Pipeline: "active",
			Trigger: RunTriggerManual, Status: RunStatusRunning, RiverJobID: &reusedJobID,
		})
		require.NoError(t, err)
		queuedID, err := store.Create(ctx, PipelineRun{
			ID: "queued", PipelineID: "new-pipeline", Pipeline: "new",
			Trigger: RunTriggerManual, Status: RunStatusQueued,
		})
		require.NoError(t, err)

		require.Error(t, store.SetRunRiverJob(ctx, queuedID, reusedJobID))

		active, _, _, err := store.Get(ctx, activeID)
		require.NoError(t, err)
		require.NotNil(t, active.RiverJobID)
		assert.Equal(t, reusedJobID, *active.RiverJobID)
		queued, _, _, err := store.Get(ctx, queuedID)
		require.NoError(t, err)
		assert.Nil(t, queued.RiverJobID)
	})

	t.Run("missing replacement run", func(t *testing.T) {
		t.Parallel()
		store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
		require.NoError(t, err)
		defer store.Close()

		reusedJobID := int64(42)
		terminalID, err := store.Create(ctx, PipelineRun{
			ID: "terminal", PipelineID: "old-pipeline", Pipeline: "old",
			Trigger: RunTriggerSchedule, Status: RunStatusFailed, RiverJobID: &reusedJobID,
		})
		require.NoError(t, err)

		require.ErrorContains(t, store.SetRunRiverJob(ctx, "missing", reusedJobID), "was not found")

		terminal, _, _, err := store.Get(ctx, terminalID)
		require.NoError(t, err)
		require.NotNil(t, terminal.RiverJobID)
		assert.Equal(t, reusedJobID, *terminal.RiverJobID)
	})
}

func TestStorePersistsValidatesAndCascadesVersionedRunPlan(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := PipelineRun{
		ID: "plan-run", PipelineID: "pipeline-id", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	}
	spec := manualRunSpec(run, RunSourceWorkingTree, "")
	runID, err := store.CreateWithSpec(ctx, run, spec)
	require.NoError(t, err)
	plan := validPipelineRunPlan(t)
	tx, err := store.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, store.insertRunPlan(ctx, tx, runID, plan))
	require.NoError(t, tx.Commit())

	persisted, found, err := store.GetRunPlan(ctx, runID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, plan, persisted)

	_, err = store.db.Exec(`UPDATE pipeline_run_plans SET version = 99, body = json_set(body, '$.version', 99) WHERE run_id = ?`, runID)
	require.NoError(t, err)
	_, found, err = store.GetRunPlan(ctx, runID)
	require.True(t, found)
	require.ErrorIs(t, err, ErrInvalidStoredRunPlan)
	require.ErrorContains(t, err, "unsupported pipeline run plan version 99")

	_, err = store.db.Exec(`DELETE FROM pipeline_runs WHERE id = ?`, runID)
	require.NoError(t, err)
	assert.Equal(t, 0, countRows(t, store, `SELECT COUNT(*) FROM pipeline_run_plans WHERE run_id = ?`, runID))
	assert.Equal(t, 0, countRows(t, store, `SELECT COUNT(*) FROM pipeline_run_units WHERE run_id = ?`, runID))
}

func TestStoreTracksPipelineRunUnitsAndClosesUnfinishedWork(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()

	plan := validPipelineRunPlan(t)
	plan.ExecutionUnits = append(plan.ExecutionUnits,
		PipelineRunExecutionUnit{
			AssetID: "pipeline-uuid:analytics.customers", AssetName: "analytics.customers",
			StartDate: "2026-07-17T11:00:00Z", EndDate: "2026-07-17T12:00:00Z",
			Reason: "selected_all",
		},
		PipelineRunExecutionUnit{
			AssetID: "pipeline-uuid:analytics.report", AssetName: "analytics.report",
			StartDate: "2026-07-17T11:00:00Z", EndDate: "2026-07-17T12:00:00Z",
			Reason: "selected_all",
		},
	)
	plan.Artifact = pipelineRunPlanArtifact(t, plan)
	run := PipelineRun{
		ID: "unit-run", PipelineID: "pipeline-id", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusRunning,
	}
	spec := manualRunSpec(run, RunSourceWorkingTree, "")
	_, err = store.CreateWithSpec(ctx, run, spec)
	require.NoError(t, err)
	tx, err := store.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, store.insertRunPlan(ctx, tx, run.ID, plan))
	require.NoError(t, tx.Commit())

	started := time.Date(2026, 7, 17, 12, 1, 0, 0, time.UTC)
	finished := started.Add(time.Minute)
	require.NoError(t, store.UpdateRunUnit(ctx, run.ID, PipelineRunUnitEvent{
		Position: 0, Status: PipelineRunUnitRunning, StartedAt: &started,
	}))
	require.NoError(t, store.UpdateRunUnit(ctx, run.ID, PipelineRunUnitEvent{
		Position: 0, Status: PipelineRunUnitSuccess, FinishedAt: &finished,
	}))
	require.NoError(t, store.UpdateRunUnit(ctx, run.ID, PipelineRunUnitEvent{
		Position: 1, Status: PipelineRunUnitRunning, StartedAt: &started,
	}))
	require.NoError(t, store.FinalizeExecution(ctx, run.ID, RunStatusFailed, finished, assert.AnError, "", nil))

	units, err := store.ListRunUnits(ctx, run.ID)
	require.NoError(t, err)
	require.Len(t, units, 3)
	assert.Equal(t, PipelineRunUnitSuccess, units[0].Status)
	assert.Equal(t, &started, units[0].StartedAt)
	assert.Equal(t, &finished, units[0].FinishedAt)
	assert.Equal(t, PipelineRunUnitFailed, units[1].Status)
	assert.Equal(t, PipelineRunUnitSkipped, units[2].Status)
	assert.Contains(t, units[2].Error, assert.AnError.Error())
}

func TestRunSpecExpectedPlanIdentityFailsClosed(t *testing.T) {
	t.Parallel()
	executionTime := time.Date(2026, 7, 17, 10, 0, 0, 0, time.UTC)
	valid := runSpecV1{
		Version:  runSpecVersionV1,
		Pipeline: runPipelineIdentity{ID: "pipeline-id", Name: "analytics"},
		Origin:   RunTriggerManual,
		Dispatch: runDispatchRiver,
		Source:   runSourceSpec{Kind: RunSourceWorkingTree},
		Requested: runRequestedContext{
			ExecutionTime: &executionTime,
		},
		Expected: &runExpectedIdentity{
			SourceMerkle:        strings.Repeat("a", 64),
			ConfigurationDigest: strings.Repeat("b", 64),
		},
		Selection: runSelectionAll,
	}
	require.NoError(t, valid.validate())

	missingTime := valid
	missingTime.Requested.ExecutionTime = nil
	require.ErrorContains(t, missingTime.validate(), "requires execution_time")
	badSource := valid
	badSource.Expected = &runExpectedIdentity{SourceMerkle: "ABC", ConfigurationDigest: strings.Repeat("b", 64)}
	require.ErrorContains(t, badSource.validate(), "source_merkle")
	badConfiguration := valid
	badConfiguration.Expected = &runExpectedIdentity{SourceMerkle: strings.Repeat("a", 64), ConfigurationDigest: "short"}
	require.ErrorContains(t, badConfiguration.validate(), "configuration_digest")
}

func TestRunSpecVariableReferencesAreStrictAndSecretFree(t *testing.T) {
	t.Parallel()
	spec := runSpecV1{
		Version:  runSpecVersionV1,
		Pipeline: runPipelineIdentity{ID: "pipeline-id", Name: "analytics"},
		Origin:   RunTriggerManual, Dispatch: runDispatchRiver,
		Source: runSourceSpec{Kind: RunSourceWorkingTree}, Selection: runSelectionAll,
		Requested: runRequestedContext{
			Variables:          map[string]any{"region": "eu"},
			VariableReferences: map[string]string{"token": "env:RENART_TOKEN"},
		},
	}
	require.NoError(t, spec.validate())
	body, err := marshalRunSpec(spec)
	require.NoError(t, err)
	assert.Contains(t, string(body), "env:RENART_TOKEN")
	assert.NotContains(t, string(body), "secret-value")

	duplicate := spec
	duplicate.Requested.Variables = map[string]any{"token": "secret-value"}
	require.ErrorContains(t, duplicate.validate(), "both a value and a secret reference")
	invalid := spec
	invalid.Requested.VariableReferences = map[string]string{"token": "literal-secret"}
	require.ErrorContains(t, invalid.validate(), "provider:key")
}

func TestStatusFromResultPreservesCancellation(t *testing.T) {
	t.Parallel()

	status, err := statusFromResult(RunResult{Status: "cancelled", Error: "context canceled"})
	assert.Equal(t, RunStatusCancelled, status)
	require.ErrorContains(t, err, "context canceled")

	status, err = statusFromResult(RunResult{Status: "canceled"})
	assert.Equal(t, RunStatusCancelled, status)
	require.ErrorContains(t, err, "pipeline run was cancelled")
}

func TestStoreEnforcesOneAtomicActiveRunPerPipeline(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	const attempts = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	accepted := make(chan string, attempts)
	rejected := make(chan error, attempts)
	for index := 0; index < attempts; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			id, err := store.Create(context.Background(), PipelineRun{
				ID: fmt.Sprintf("run-%d", index), PipelineID: "pipeline-id", Pipeline: "analytics",
				Trigger: RunTriggerManual, Status: RunStatusQueued,
			})
			if err != nil {
				rejected <- err
				return
			}
			accepted <- id
		}(index)
	}
	close(start)
	wg.Wait()
	close(accepted)
	close(rejected)

	acceptedIDs := make([]string, 0, 1)
	for id := range accepted {
		acceptedIDs = append(acceptedIDs, id)
	}
	require.Len(t, acceptedIDs, 1)
	for err := range rejected {
		assert.ErrorIs(t, err, ErrPipelineRunActive)
		var conflict *PipelineRunActiveError
		require.ErrorAs(t, err, &conflict)
		assert.Equal(t, acceptedIDs[0], conflict.ActiveRunID)
	}

	otherID, err := store.Create(context.Background(), PipelineRun{
		ID: "other-run", PipelineID: "other-pipeline", Pipeline: "marketing",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)
	assert.Equal(t, "other-run", otherID)
}

func TestStoreActiveSlotUsesStableUUIDAndNamespacedAliases(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()

	firstID, err := store.Create(ctx, PipelineRun{
		ID: "first", PipelineID: "old-path", PipelineUUID: "stable-uuid", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)
	_, err = store.Create(ctx, PipelineRun{
		ID: "renamed", PipelineID: "new-path", PipelineUUID: "stable-uuid", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.ErrorIs(t, err, ErrPipelineRunActive)
	var conflict *PipelineRunActiveError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, firstID, conflict.ActiveRunID)

	// Path and UUID aliases are namespaced, so equal raw values belonging to
	// different pipelines cannot create a false conflict.
	_, err = store.Create(ctx, PipelineRun{
		ID: "unrelated", PipelineID: "stable-uuid", PipelineUUID: "other-uuid", Pipeline: "marketing",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)

	require.NoError(t, store.Finish(ctx, firstID, RunStatusSuccess, nil))
	_, err = store.Create(ctx, PipelineRun{
		ID: "renamed", PipelineID: "new-path", PipelineUUID: "stable-uuid", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)
}

func TestSetRunSpecIfMissingReturnsPersistedWinner(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	run := PipelineRun{
		ID: "legacy", PipelineID: "pipeline-id", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	}
	_, err = store.Create(ctx, run)
	require.NoError(t, err)

	first := manualRunSpec(run, RunSourceWorkingTree, "")
	first.Requested.FullRefresh = true
	persisted, err := store.SetRunSpecIfMissing(ctx, run.ID, first)
	require.NoError(t, err)
	assert.Equal(t, first, persisted)

	loser := manualRunSpec(run, RunSourceWorkingTree, "")
	loser.Requested.SensorMode = "skip"
	persisted, err = store.SetRunSpecIfMissing(ctx, run.ID, loser)
	require.NoError(t, err)
	assert.Equal(t, first, persisted, "the stored spec remains authoritative after an insertion race")
}

func TestSetLegacyScheduledRunSpecClaimsStableUUIDSlot(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	start := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	run := PipelineRun{
		ID: "legacy-scheduled", PipelineID: "old-path", Pipeline: "analytics",
		Environment: "prod", Trigger: RunTriggerSchedule, Status: RunStatusQueued,
		WinStart: &start, WinEnd: &end, SnapshotVersionID: "snapshot-id",
	}
	_, err = store.Create(ctx, run)
	require.NoError(t, err)
	spec := scheduledRunSpec(run, pipelineRunJobArgs{
		PipelineUUID: "stable-uuid", Environment: "prod", Schedule: "@hourly", Timezone: "UTC",
	})

	persisted, err := store.SetRunSpecIfMissing(ctx, run.ID, spec)
	require.NoError(t, err)
	assert.Equal(t, "stable-uuid", persisted.Pipeline.UUID)
	assert.Equal(t, 1, countRows(t, store, `SELECT COUNT(*) FROM pipeline_run_slots WHERE slot_key = 'uuid:stable-uuid' AND run_id = ?`, run.ID))
}

func TestOpenStoreRejectsCorruptDatabaseBeforeMigrations(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := OpenStore(path)
	require.NoError(t, err)

	var pageSize, rootPage int64
	require.NoError(t, store.db.QueryRow(`PRAGMA page_size`).Scan(&pageSize))
	require.NoError(t, store.db.QueryRow(`
		SELECT rootpage
		FROM sqlite_schema
		WHERE type = 'table' AND name = 'pipeline_run_logs'`).Scan(&rootPage))
	require.Positive(t, pageSize)
	require.Positive(t, rootPage)
	require.NoError(t, store.Close())

	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	require.NoError(t, err)
	_, writeErr := file.WriteAt(make([]byte, pageSize), (rootPage-1)*pageSize)
	require.NoError(t, writeErr)
	require.NoError(t, file.Close())
	require.False(t, matchesIntegrityStamp(path), "a database changed after clean close must be checked again")

	corruptStore, err := OpenStore(path)
	if corruptStore != nil {
		_ = corruptStore.Close()
	}
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrStateDatabaseIntegrity)
	assert.ErrorContains(t, err, path)
	assert.ErrorContains(t, err, "back up state.db, state.db-wal, and state.db-shm")
}

func TestOpenStoreTracksCleanAndActiveDatabaseState(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := OpenStore(path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	require.FileExists(t, integrityStampPath(path))
	require.True(t, matchesIntegrityStamp(path))

	store, err = OpenStore(path)
	require.NoError(t, err)
	require.NoFileExists(t, integrityStampPath(path), "an open database must not retain a clean-close stamp")
	require.NoError(t, store.Close())
	require.True(t, matchesIntegrityStamp(path))

	require.NoError(t, os.WriteFile(path+"-wal", []byte("pending writes"), 0o600))
	require.False(t, matchesIntegrityStamp(path), "a non-empty WAL must force an integrity check")
}

func TestStoreCreatesRunsLogsAndWatermarks(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), ".renart", "state.db"))
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	id, err := store.Create(ctx, PipelineRun{PipelineID: "pipeline-id", Pipeline: "analytics", Environment: "dev", Trigger: RunTriggerManual, Status: RunStatusQueued, WinStart: &start, WinEnd: &end})
	require.NoError(t, err)
	require.NotEmpty(t, id)
	require.NoError(t, store.MarkRunning(ctx, id, start))
	require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{RunID: id, Asset: "orders_cleaned", Status: RunStatusRunning, StartedAt: &start}))
	require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{RunID: id, Asset: "orders_cleaned", Status: RunStatusSuccess, FinishedAt: &end}))
	require.NoError(t, store.AppendLog(ctx, id, LogLine{At: start, Line: "hello"}))
	require.NoError(t, store.Finish(ctx, id, RunStatusSuccess, nil))
	require.NoError(t, store.SetInterval(ctx, "pipeline-id", end))

	result, err := store.List(ctx, RunFilter{PipelineID: "pipeline-id"})
	require.NoError(t, err)
	runs := result.Runs
	require.Len(t, runs, 1)
	assert.Equal(t, 1, result.Total)
	assert.Equal(t, RunStatusSuccess, runs[0].Status)

	run, logs, steps, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "analytics", run.Pipeline)
	require.Len(t, logs, 1)
	assert.Equal(t, "hello", logs[0].Line)
	require.Len(t, steps, 1)
	assert.Equal(t, "orders_cleaned", steps[0].Asset)
	assert.Equal(t, RunStatusSuccess, steps[0].Status)
	require.NotNil(t, steps[0].StartedAt)
	require.NotNil(t, steps[0].FinishedAt)

	watermark, ok, err := store.LastInterval(ctx, "pipeline-id")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, end, watermark)
}

func TestFinishScheduledSuccessIsAtomicWithWatermark(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	started := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	end := started.Add(time.Hour)
	runID, err := store.Create(ctx, PipelineRun{
		ID: "atomic-finish", PipelineID: "pipeline-id", Pipeline: "analytics",
		Environment: "prod", Trigger: RunTriggerSchedule, Status: RunStatusQueued,
		WinStart: &started, WinEnd: &end,
	})
	require.NoError(t, err)
	require.NoError(t, store.MarkRunning(ctx, runID, started))
	_, err = store.db.ExecContext(ctx, `
		CREATE TRIGGER reject_test_watermark
		BEFORE INSERT ON schedule_watermarks
		BEGIN
			SELECT RAISE(ABORT, 'test watermark failure');
		END`)
	require.NoError(t, err)

	err = store.FinishScheduledSuccess(ctx, runID, "pipeline-id|prod", end)
	require.ErrorContains(t, err, "test watermark failure")
	run, _, _, err := store.Get(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusRunning, run.Status, "run finish must roll back with the watermark")
	_, ok, err := store.LastInterval(ctx, "pipeline-id|prod")
	require.NoError(t, err)
	assert.False(t, ok)

	_, err = store.db.ExecContext(ctx, `DROP TRIGGER reject_test_watermark`)
	require.NoError(t, err)
	require.NoError(t, store.FinishScheduledSuccess(ctx, runID, "pipeline-id|prod", end))
	run, _, _, err = store.Get(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusSuccess, run.Status)
	watermark, ok, err := store.LastInterval(ctx, "pipeline-id|prod")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, end, watermark)

	err = store.FinishScheduledSuccess(ctx, "missing-run", "pipeline-id|prod", end.Add(time.Hour))
	require.ErrorContains(t, err, "active pipeline run missing-run was not found")
	watermark, ok, err = store.LastInterval(ctx, "pipeline-id|prod")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, end, watermark, "a missing run must not advance progress")
}

func TestFinalizeExecutionAtomicallyClosesStepsRunAndWatermark(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	started := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	finished := started.Add(time.Minute)
	upTo := started.Add(time.Hour)
	runID, err := store.Create(ctx, PipelineRun{
		ID: "atomic-execution-finish", PipelineID: "pipeline-id", Pipeline: "analytics",
		Environment: "prod", Trigger: RunTriggerSchedule, Status: RunStatusQueued,
	})
	require.NoError(t, err)
	require.NoError(t, store.MarkRunning(ctx, runID, started))
	require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{
		RunID: runID, Asset: "analytics.orders", Status: RunStatusRunning, StartedAt: &started,
	}))
	_, err = store.db.ExecContext(ctx, `
		CREATE TRIGGER reject_atomic_execution_watermark
		BEFORE INSERT ON schedule_watermarks
		BEGIN
			SELECT RAISE(ABORT, 'atomic execution watermark failure');
		END`)
	require.NoError(t, err)

	err = store.FinalizeExecution(ctx, runID, RunStatusSuccess, finished, nil, "pipeline-id|prod", &upTo)
	require.ErrorContains(t, err, "atomic execution watermark failure")
	run, _, steps, err := store.Get(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusRunning, run.Status)
	require.Len(t, steps, 1)
	assert.Equal(t, RunStatusRunning, steps[0].Status)
	assert.Nil(t, steps[0].FinishedAt)

	_, err = store.db.ExecContext(ctx, `DROP TRIGGER reject_atomic_execution_watermark`)
	require.NoError(t, err)
	require.NoError(t, store.FinalizeExecution(ctx, runID, RunStatusSuccess, finished, nil, "pipeline-id|prod", &upTo))
	run, _, steps, err = store.Get(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusSuccess, run.Status)
	assert.Equal(t, finished, *run.FinishedAt)
	require.Len(t, steps, 1)
	assert.Equal(t, RunStatusSuccess, steps[0].Status)
	assert.Equal(t, finished, *steps[0].FinishedAt)
	watermark, ok, err := store.LastInterval(ctx, "pipeline-id|prod")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, upTo, watermark)
}

func TestFailOrphanedRunsReconcilesRunningRuns(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), ".renart", "state.db"))
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	started := time.Date(2026, 1, 2, 9, 0, 0, 0, time.UTC)

	// A run that was executing when the process died: still "running", with an
	// open step.
	orphan, err := store.Create(ctx, PipelineRun{PipelineID: "p1", Pipeline: "analytics", Environment: "dev", Trigger: RunTriggerManual, Status: RunStatusQueued})
	require.NoError(t, err)
	require.NoError(t, store.MarkRunning(ctx, orphan, started))
	require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{RunID: orphan, Asset: "orders", Status: RunStatusRunning, StartedAt: &started}))

	// A run that finished normally must be left untouched.
	done, err := store.Create(ctx, PipelineRun{PipelineID: "p2", Pipeline: "marketing", Environment: "dev", Trigger: RunTriggerManual, Status: RunStatusQueued})
	require.NoError(t, err)
	require.NoError(t, store.MarkRunning(ctx, done, started))
	require.NoError(t, store.Finish(ctx, done, RunStatusSuccess, nil))

	recovery, err := store.ReconcileInterruptedState(ctx, orphanedRunError)
	require.NoError(t, err)
	require.Equal(t, []string{orphan}, recovery.RunIDs)
	assert.Zero(t, recovery.RiverJobsCancelled)
	pending, err := store.PendingRunRecoveries(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{orphan}, pending)

	orphanRun, _, steps, err := store.Get(ctx, orphan)
	require.NoError(t, err)
	assert.Equal(t, RunStatusFailed, orphanRun.Status)
	assert.Equal(t, orphanedRunError, orphanRun.Error)
	require.NotNil(t, orphanRun.FinishedAt)
	require.Len(t, steps, 1)
	assert.Equal(t, RunStatusFailed, steps[0].Status)
	require.NotNil(t, steps[0].FinishedAt)

	doneRun, _, _, err := store.Get(ctx, done)
	require.NoError(t, err)
	assert.Equal(t, RunStatusSuccess, doneRun.Status)

	// Idempotent: a second pass finds nothing to reconcile.
	again, err := store.ReconcileInterruptedState(ctx, orphanedRunError)
	require.NoError(t, err)
	assert.Empty(t, again.RunIDs)
	assert.Zero(t, again.RiverJobsCancelled)
	pending, err = store.PendingRunRecoveries(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{orphan}, pending, "replay remains pending until acknowledged")
	require.NoError(t, store.MarkRunRecoveryReplayed(ctx, orphan))
	pending, err = store.PendingRunRecoveries(ctx)
	require.NoError(t, err)
	assert.Empty(t, pending)
}

func TestReconcileInterruptedStateCancelsClaimedRiverJobsAndPreservesQueuedJobs(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), ".renart", "state.db"))
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	claimedRunID, err := store.Create(ctx, PipelineRun{
		ID: "claimed-run", PipelineID: "pipeline-id", Pipeline: "analytics",
		Environment: "prod", Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)
	claimedJobID := insertTestRiverJob(t, store, pipelineRunJobArgs{RunID: claimedRunID})
	markTestRiverJobRunning(t, store, claimedJobID)

	queuedRunID, err := store.Create(ctx, PipelineRun{
		ID: "queued-run", PipelineID: "other-pipeline", Pipeline: "marketing",
		Environment: "prod", Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)
	queuedJobID := insertTestRiverJob(t, store, pipelineRunJobArgs{RunID: queuedRunID})
	require.NoError(t, store.SetRunRiverJob(ctx, queuedRunID, queuedJobID))

	finishedJobID := insertTestRiverJob(t, store, pipelineRunJobArgs{RunID: "finished-run"})
	finishedRunID, err := store.Create(ctx, PipelineRun{
		ID: "finished-run", PipelineID: "finished-pipeline", Pipeline: "finished",
		Environment: "prod", Trigger: RunTriggerManual, Status: RunStatusSuccess,
		RiverJobID: &finishedJobID,
	})
	require.NoError(t, err)
	markTestRiverJobRunning(t, store, finishedJobID)

	housekeepingJobID := insertTestRiverJob(t, store, housekeepingJobArgs{})
	markTestRiverJobRunning(t, store, housekeepingJobID)

	recovery, err := store.ReconcileInterruptedState(ctx, orphanedRunError)
	require.NoError(t, err)
	assert.Equal(t, []string{claimedRunID}, recovery.RunIDs)
	assert.EqualValues(t, 3, recovery.RiverJobsCancelled)

	claimedRun, _, _, err := store.Get(ctx, claimedRunID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusFailed, claimedRun.Status)
	assert.Equal(t, orphanedRunError, claimedRun.Error)
	require.NotNil(t, claimedRun.RiverJobID)
	assert.Equal(t, claimedJobID, *claimedRun.RiverJobID)

	queuedRun, _, _, err := store.Get(ctx, queuedRunID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusQueued, queuedRun.Status)
	assertRiverJobState(t, store, queuedJobID, rivertype.JobStateAvailable)

	finishedRun, _, _, err := store.Get(ctx, finishedRunID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusSuccess, finishedRun.Status)
	assertRiverJobState(t, store, claimedJobID, rivertype.JobStateCancelled)
	assertRiverJobState(t, store, finishedJobID, rivertype.JobStateCancelled)
	assertRiverJobState(t, store, housekeepingJobID, rivertype.JobStateCancelled)

	var riverErrors string
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT json(errors) FROM river_job WHERE id = ?`, claimedJobID).Scan(&riverErrors))
	assert.Contains(t, riverErrors, orphanedRunError)
}

func TestReconcileInterruptedStateRepairsQueuedRowsAndRequeuesUnadmittedScheduleSignal(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), ".renart", "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()

	availableRunID, err := store.Create(ctx, PipelineRun{
		ID: "available-legacy", PipelineID: "available-pipeline", Pipeline: "available",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)
	availableJobID := insertTestRiverJob(t, store, pipelineRunJobArgs{RunID: availableRunID})

	terminalRunID, err := store.Create(ctx, PipelineRun{
		ID: "terminal-linked", PipelineID: "terminal-pipeline", Pipeline: "terminal",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)
	terminalJobID := insertTestRiverJob(t, store, pipelineRunJobArgs{RunID: terminalRunID})
	require.NoError(t, store.SetRunRiverJob(ctx, terminalRunID, terminalJobID))
	_, err = store.db.ExecContext(ctx, `UPDATE river_job SET state = ?, finalized_at = ? WHERE id = ?`,
		string(rivertype.JobStateDiscarded), formatTime(time.Now().UTC()), terminalJobID)
	require.NoError(t, err)

	joblessRunID, err := store.Create(ctx, PipelineRun{
		ID: "jobless", PipelineID: "jobless-pipeline", Pipeline: "jobless",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)

	dueArgs := pipelineRunJobArgs{
		PipelineUUID: "scheduled-uuid", Environment: "prod",
		Start: "2026-07-16T08:00:00Z", End: "2026-07-16T09:00:00Z",
		SnapshotVersionID: "snapshot-id",
	}
	dueJobID := insertTestRiverJob(t, store, dueArgs)
	markTestRiverJobRunning(t, store, dueJobID)
	var dueArgsBefore string
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT json(args) FROM river_job WHERE id = ?`, dueJobID).Scan(&dueArgsBefore))
	v2SignalArgs := scheduleSignalJobArgs{
		PipelineUUID: "scheduled-v2-uuid", Environment: "prod",
		Start: "2026-07-16T09:00:00Z", End: "2026-07-16T10:00:00Z",
		SnapshotVersionID: "snapshot-id",
	}
	v2SignalJobID := insertTestRiverJob(t, store, v2SignalArgs)
	markTestRiverJobRunning(t, store, v2SignalJobID)
	var v2ArgsBefore string
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT json(args) FROM river_job WHERE id = ?`, v2SignalJobID).Scan(&v2ArgsBefore))

	recovery, err := store.ReconcileInterruptedState(ctx, orphanedRunError)
	require.NoError(t, err)
	assert.Equal(t, []string{joblessRunID, terminalRunID}, recovery.RunIDs)
	assert.Zero(t, recovery.RiverJobsCancelled)
	assert.EqualValues(t, 2, recovery.RiverJobsRequeued)

	available, _, _, err := store.Get(ctx, availableRunID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusQueued, available.Status)
	require.NotNil(t, available.RiverJobID)
	assert.Equal(t, availableJobID, *available.RiverJobID)
	assertRiverJobState(t, store, availableJobID, rivertype.JobStateAvailable)

	for _, runID := range []string{joblessRunID, terminalRunID} {
		run, _, _, getErr := store.Get(ctx, runID)
		require.NoError(t, getErr)
		assert.Equal(t, RunStatusFailed, run.Status)
		assert.Equal(t, orphanedRunError, run.Error)
	}
	assertRiverJobState(t, store, terminalJobID, rivertype.JobStateDiscarded)
	assertRiverJobState(t, store, dueJobID, rivertype.JobStateAvailable)
	assertRiverJobState(t, store, v2SignalJobID, rivertype.JobStateAvailable)
	var dueArgsAfter string
	var dueAttempt int
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT json(args), attempt FROM river_job WHERE id = ?`, dueJobID).Scan(&dueArgsAfter, &dueAttempt))
	assert.JSONEq(t, dueArgsBefore, dueArgsAfter, "recovery must preserve the exact scheduled interval signal")
	assert.Zero(t, dueAttempt)
	var v2ArgsAfter string
	var v2Attempt int
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT json(args), attempt FROM river_job WHERE id = ?`, v2SignalJobID).Scan(&v2ArgsAfter, &v2Attempt))
	assert.JSONEq(t, v2ArgsBefore, v2ArgsAfter, "recovery must preserve the v2 due signal revision and interval")
	assert.Zero(t, v2Attempt)

	// Failed orphan rows released their path aliases, so neither can hold the
	// active slot forever after an upgrade.
	_, err = store.Create(ctx, PipelineRun{PipelineID: "jobless-pipeline", Pipeline: "replacement", Trigger: RunTriggerManual})
	require.NoError(t, err)
}

func TestReconcileInterruptedStateRequeuesMalformedRiverRetryTimestamps(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), ".renart", "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()

	runID, err := store.Create(ctx, PipelineRun{
		ID: "snoozed-run", PipelineID: "pipeline-id", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.NoError(t, err)
	runJobID := insertTestRiverJob(t, store, pipelineRunJobArgs{RunID: runID})
	require.NoError(t, store.SetRunRiverJob(ctx, runID, runJobID))
	signalArgs := scheduleSignalJobArgs{
		PipelineUUID: "pipeline-uuid", Environment: "prod",
		Start: "2026-07-20T18:00:00Z", End: "2026-07-20T18:30:00Z",
		SnapshotVersionID: "snapshot-id",
	}
	signalJobID := insertTestRiverJob(t, store, signalArgs)
	rfc3339JobID := insertTestRiverJob(t, store, scheduleSignalJobArgs{
		PipelineUUID: "rfc3339-pipeline-uuid", Environment: "prod",
		Start: "2026-07-20T18:30:00Z", End: "2026-07-20T19:00:00Z",
		SnapshotVersionID: "snapshot-id",
	})

	const malformed = "2026-07-20 20:30:30.199198727 +0200 CEST m=+44.500788305"
	_, err = store.db.ExecContext(ctx, `
		UPDATE river_job
		SET state = CASE id WHEN ? THEN ? ELSE ? END,
		    attempt = CASE id WHEN ? THEN 1 ELSE 0 END,
		    scheduled_at = ?
		WHERE id IN (?, ?)`,
		runJobID, string(rivertype.JobStateRetryable), string(rivertype.JobStateScheduled),
		runJobID, malformed, runJobID, signalJobID,
	)
	require.NoError(t, err)
	_, err = store.db.ExecContext(ctx, `
		UPDATE river_job SET state = ?, scheduled_at = ? WHERE id = ?`,
		string(rivertype.JobStateAvailable), "2026-07-20T19:03:46.243023719Z", rfc3339JobID)
	require.NoError(t, err)
	var signalArgsBefore string
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT json(args) FROM river_job WHERE id = ?`, signalJobID).Scan(&signalArgsBefore))

	recovery, err := store.ReconcileInterruptedState(ctx, orphanedRunError)
	require.NoError(t, err)
	assert.Empty(t, recovery.RunIDs)
	assert.EqualValues(t, 3, recovery.RiverJobsRequeued)
	for _, jobID := range []int64{runJobID, signalJobID, rfc3339JobID} {
		assertRiverJobState(t, store, jobID, rivertype.JobStateAvailable)
		var scheduledAt string
		var parseable bool
		var dueForRiver bool
		require.NoError(t, store.db.QueryRowContext(ctx, `
			SELECT CAST(scheduled_at AS TEXT),
			       julianday(scheduled_at) IS NOT NULL,
			       CAST(scheduled_at AS TEXT) <= ?
			FROM river_job WHERE id = ?`, formatRiverTime(time.Now().UTC().Add(time.Second)), jobID).
			Scan(&scheduledAt, &parseable, &dueForRiver))
		assert.True(t, parseable)
		assert.True(t, dueForRiver, "recovered timestamp must be eligible under River's lexical comparison")
		assert.NotContains(t, scheduledAt, "m=")
		assert.NotContains(t, scheduledAt, "T")
	}
	var runAttempt, signalAttempt int
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT attempt FROM river_job WHERE id = ?`, runJobID).Scan(&runAttempt))
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT attempt FROM river_job WHERE id = ?`, signalJobID).Scan(&signalAttempt))
	assert.Equal(t, 1, runAttempt, "timestamp repair must not reinterpret River attempt accounting")
	assert.Zero(t, signalAttempt)
	var signalArgsAfter string
	require.NoError(t, store.db.QueryRowContext(ctx, `SELECT json(args) FROM river_job WHERE id = ?`, signalJobID).Scan(&signalArgsAfter))
	assert.JSONEq(t, signalArgsBefore, signalArgsAfter)
	run, _, _, err := store.Get(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusQueued, run.Status)

	again, err := store.ReconcileInterruptedState(ctx, orphanedRunError)
	require.NoError(t, err)
	assert.Zero(t, again.RiverJobsRequeued, "canonical timestamps are not requeued repeatedly")
}

func TestFormatRiverTimeMatchesRiverSQLiteOrderingFormat(t *testing.T) {
	t.Parallel()
	local := time.FixedZone("test-local", 2*60*60)
	value := time.Date(2026, 7, 20, 20, 30, 30, 123800000, local)
	assert.Equal(t, "2026-07-20 18:30:30.124", formatRiverTime(value))
}

func TestRunRecoveryMigrationBackfillsInterruptedRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".renart", "state.db")
	store, err := OpenStore(path)
	require.NoError(t, err)

	ctx := context.Background()
	migrations, err := fs.Sub(schedulerMigrations, "storedb/migrations")
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectSQLite3, store.db, migrations)
	require.NoError(t, err)
	_, err = provider.DownTo(ctx, 7)
	require.NoError(t, err)

	_, err = store.db.ExecContext(ctx, `
		INSERT INTO pipeline_runs (id, pipeline_id, pipeline, environment, trigger, status, error)
		VALUES
			('previously-reconciled', 'p1', 'analytics', 'prod', 'schedule', 'failed', ?),
			('ordinary-failure', 'p1', 'analytics', 'prod', 'schedule', 'failed', 'asset failed')`,
		orphanedRunError,
	)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	store, err = OpenStore(path)
	require.NoError(t, err)
	defer store.Close()
	pending, err := store.PendingRunRecoveries(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"previously-reconciled"}, pending)
}

func TestActiveRunSlotMigrationReconcilesDuplicateLegacyRowsWithoutDeletingHistory(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := OpenStore(path)
	require.NoError(t, err)

	ctx := context.Background()
	migrations, err := fs.Sub(schedulerMigrations, "storedb/migrations")
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectSQLite3, store.db, migrations)
	require.NoError(t, err)
	_, err = provider.DownTo(ctx, 11)
	require.NoError(t, err)
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO pipeline_runs
			(id, pipeline_id, pipeline, environment, trigger, status)
		VALUES
			('legacy-active-a', 'pipeline-id', 'analytics', 'prod', 'manual', 'queued'),
			('legacy-active-b', 'pipeline-id', 'analytics', 'prod', 'manual', 'running')`)
	require.NoError(t, err)
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO pipeline_run_steps (run_id, asset, status, started_at)
		VALUES ('legacy-active-b', 'analytics.orders', 'running', '2026-07-16T08:00:00Z')`)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	store, err = OpenStore(path)
	require.NoError(t, err)
	defer store.Close()
	var active int
	require.NoError(t, store.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pipeline_runs
		WHERE pipeline_id = 'pipeline-id' AND status IN ('queued', 'running')`).Scan(&active))
	assert.Equal(t, 1, active)
	assert.Equal(t, 2, countRows(t, store, `SELECT COUNT(*) FROM pipeline_runs WHERE pipeline_id = 'pipeline-id'`), "migration must preserve every run row")
	assert.Equal(t, 1, countRows(t, store, `SELECT COUNT(*) FROM pipeline_run_slots WHERE slot_key = 'path:pipeline-id' AND run_id = 'legacy-active-a'`))

	survivor, _, _, err := store.Get(ctx, "legacy-active-a")
	require.NoError(t, err)
	assert.Equal(t, RunStatusQueued, survivor.Status)
	conflict, logs, steps, err := store.Get(ctx, "legacy-active-b")
	require.NoError(t, err)
	assert.Equal(t, RunStatusFailed, conflict.Status)
	require.NotNil(t, conflict.FinishedAt)
	assert.Contains(t, conflict.Error, "duplicate active run reconciled during atomic run-slot migration")
	assert.Contains(t, conflict.Error, "retained run legacy-active-a for scheduler recovery")
	require.Len(t, logs, 1)
	assert.Equal(t, conflict.Error, logs[0].Line)
	require.Len(t, steps, 1)
	assert.Equal(t, RunStatusFailed, steps[0].Status)
	require.NotNil(t, steps[0].FinishedAt)
	assert.Equal(t, conflict.Error, steps[0].Error)
	pending, err := store.PendingRunRecoveries(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"legacy-active-b"}, pending)
}

func TestActiveRunSlotMigrationReconcilesDuplicatesBeforeRecoveryColumnsExist(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := OpenStore(path)
	require.NoError(t, err)
	ctx := context.Background()
	migrations, err := fs.Sub(schedulerMigrations, "storedb/migrations")
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectSQLite3, store.db, migrations)
	require.NoError(t, err)
	_, err = provider.DownTo(ctx, 7)
	require.NoError(t, err)
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO pipeline_runs (id, pipeline_id, pipeline, environment, trigger, status)
		VALUES
			('older-a', 'pipeline-id', 'analytics', 'prod', 'manual', 'queued'),
			('older-b', 'pipeline-id', 'analytics', 'prod', 'manual', 'queued')`)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	store, err = OpenStore(path)
	require.NoError(t, err)
	defer store.Close()
	assert.Equal(t, 2, countRows(t, store, `SELECT COUNT(*) FROM pipeline_runs WHERE pipeline_id = 'pipeline-id'`))
	assert.Equal(t, 1, countRows(t, store, `SELECT COUNT(*) FROM pipeline_runs WHERE pipeline_id = 'pipeline-id' AND status = 'queued'`))
	failed, logs, _, err := store.Get(ctx, "older-b")
	require.NoError(t, err)
	assert.Equal(t, RunStatusFailed, failed.Status)
	assert.Contains(t, failed.Error, "retained run older-a for scheduler recovery")
	require.Len(t, logs, 1)
	pending, err := store.PendingRunRecoveries(ctx)
	require.NoError(t, err)
	assert.Empty(t, pending, "pre-context migrations cannot safely replay derived materialization state")
}

func TestActiveRunSlotMigrationBridgesLegacyPathAlias(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := OpenStore(path)
	require.NoError(t, err)
	ctx := context.Background()
	migrations, err := fs.Sub(schedulerMigrations, "storedb/migrations")
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectSQLite3, store.db, migrations)
	require.NoError(t, err)
	_, err = provider.DownTo(ctx, 11)
	require.NoError(t, err)
	_, err = store.db.ExecContext(ctx, `
		INSERT INTO pipeline_runs (id, pipeline_id, pipeline, environment, trigger, status)
		VALUES ('legacy-active', 'pipeline-id', 'analytics', 'prod', 'manual', 'queued')`)
	require.NoError(t, err)
	require.NoError(t, store.Close())

	store, err = OpenStore(path)
	require.NoError(t, err)
	defer store.Close()
	_, err = store.Create(ctx, PipelineRun{
		ID: "new-run", PipelineID: "pipeline-id", PipelineUUID: "stable-uuid", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	})
	require.ErrorIs(t, err, ErrPipelineRunActive)
	var conflict *PipelineRunActiveError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, "legacy-active", conflict.ActiveRunID)
	assert.Equal(t, 1, countRows(t, store, `SELECT COUNT(*) FROM pipeline_run_slots WHERE slot_key = 'path:pipeline-id' AND run_id = 'legacy-active'`))
}

func TestPinlessScheduleMigrationFreezesLatestDeploymentOrPauses(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), ".renart", "state.db")
	store, err := OpenStore(path)
	require.NoError(t, err)
	ctx := context.Background()
	migrations, err := fs.Sub(schedulerMigrations, "storedb/migrations")
	require.NoError(t, err)
	provider, err := goose.NewProvider(goose.DialectSQLite3, store.db, migrations)
	require.NoError(t, err)
	_, err = provider.DownTo(ctx, 9)
	require.NoError(t, err)

	for _, snapshot := range []struct {
		version, pipeline, created string
	}{
		{version: "old", pipeline: "with-deploy", created: "2026-01-01T00:00:00Z"},
		{version: "latest", pipeline: "with-deploy", created: "2026-01-02T00:00:00Z"},
		{version: "paused-pin", pipeline: "paused-with-deploy", created: "2026-01-03T00:00:00Z"},
	} {
		_, err = store.db.ExecContext(ctx, `
			INSERT INTO renart_snapshots (version_id, pipeline_id, merkle_root, manifest, git_dirty, created_at)
			VALUES (?, ?, ?, '{}', 0, ?)`, snapshot.version, snapshot.pipeline, snapshot.version, snapshot.created)
		require.NoError(t, err)
	}
	for _, row := range []EnvSchedule{
		{PipelineUUID: "with-deploy", Environment: "prod", Cron: "@daily", Timezone: "UTC", CatchupPolicy: CatchupSkip, Status: ScheduleStatusActive},
		{PipelineUUID: "paused-with-deploy", Environment: "prod", Cron: "@daily", Timezone: "UTC", CatchupPolicy: CatchupSkip, Status: ScheduleStatusPaused},
		{PipelineUUID: "without-deploy", Environment: "prod", Cron: "@daily", Timezone: "UTC", CatchupPolicy: CatchupSkip, Status: ScheduleStatusActive},
		{PipelineUUID: "archived", Environment: "prod", Cron: "@daily", Timezone: "UTC", CatchupPolicy: CatchupSkip, Status: ScheduleStatusArchived, ArchivedReason: ArchivedReasonUser},
	} {
		require.NoError(t, store.UpsertEnvSchedule(ctx, row))
	}
	require.NoError(t, store.Close())

	store, err = OpenStore(path)
	require.NoError(t, err)
	defer store.Close()
	withDeploy, _, err := store.GetEnvSchedule(ctx, "with-deploy", "prod")
	require.NoError(t, err)
	assert.Equal(t, "latest", withDeploy.SnapshotVersionID)
	assert.Equal(t, ScheduleStatusActive, withDeploy.Status)
	paused, _, err := store.GetEnvSchedule(ctx, "paused-with-deploy", "prod")
	require.NoError(t, err)
	assert.Equal(t, "paused-pin", paused.SnapshotVersionID)
	assert.Equal(t, ScheduleStatusPaused, paused.Status)
	withoutDeploy, _, err := store.GetEnvSchedule(ctx, "without-deploy", "prod")
	require.NoError(t, err)
	assert.Empty(t, withoutDeploy.SnapshotVersionID)
	assert.Equal(t, ScheduleStatusPaused, withoutDeploy.Status)
	archived, _, err := store.GetEnvSchedule(ctx, "archived", "prod")
	require.NoError(t, err)
	assert.Empty(t, archived.SnapshotVersionID)
	assert.Equal(t, ScheduleStatusArchived, archived.Status)
}

func insertTestRiverJob(t *testing.T, store *Store, args river.JobArgs) int64 {
	t.Helper()
	client, err := river.NewClient(riversqlite.New(store.db), &river.Config{})
	require.NoError(t, err)
	inserted, err := client.Insert(context.Background(), args, &river.InsertOpts{
		MaxAttempts: 1,
		Queue:       pipelineRunQueue,
	})
	require.NoError(t, err)
	return inserted.Job.ID
}

func markTestRiverJobRunning(t *testing.T, store *Store, jobID int64) {
	t.Helper()
	_, err := store.db.ExecContext(context.Background(), `
		UPDATE river_job
		SET state = ?, attempt = 1, attempted_at = ?
		WHERE id = ?`,
		string(rivertype.JobStateRunning), formatTime(time.Now().UTC()), jobID,
	)
	require.NoError(t, err)
}

func assertRiverJobState(t *testing.T, store *Store, jobID int64, expected rivertype.JobState) {
	t.Helper()
	var state string
	require.NoError(t, store.db.QueryRowContext(context.Background(), `SELECT state FROM river_job WHERE id = ?`, jobID).Scan(&state))
	assert.Equal(t, string(expected), state)
}

func TestStoreDefaultsRunStatusAndGeneratedID(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	id, err := store.Create(ctx, PipelineRun{PipelineID: "pipeline-id", Pipeline: "analytics", Trigger: RunTriggerManual})
	require.NoError(t, err)
	require.NotEmpty(t, id)

	run, _, _, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, id, run.ID)
	assert.Equal(t, RunStatusQueued, run.Status)
}

func TestStoreListOrdersFiltersAndLimitsRuns(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	started := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	older := started.Add(-time.Hour)
	newer := started.Add(time.Hour)

	for _, run := range []PipelineRun{
		{ID: "older", PipelineID: "pipeline-id", Pipeline: "analytics", Trigger: RunTriggerManual, Status: RunStatusSuccess, StartedAt: &older},
		{ID: "first", PipelineID: "pipeline-id", Pipeline: "analytics", Trigger: RunTriggerManual, Status: RunStatusSuccess, StartedAt: &started},
		{ID: "second", PipelineID: "pipeline-id", Pipeline: "analytics", Trigger: RunTriggerManual, Status: RunStatusFailed, StartedAt: &started},
		{ID: "other", PipelineID: "other-pipeline", Pipeline: "other", Trigger: RunTriggerManual, Status: RunStatusSuccess, StartedAt: &newer},
	} {
		_, err := store.Create(ctx, run)
		require.NoError(t, err)
	}

	result, err := store.List(ctx, RunFilter{PipelineID: "pipeline-id", Limit: 2})
	require.NoError(t, err)
	runs := result.Runs
	require.Len(t, runs, 2)
	assert.Equal(t, 3, result.Total)
	assert.Equal(t, []string{"second", "first"}, []string{runs[0].ID, runs[1].ID})

	result, err = store.List(ctx, RunFilter{Limit: 2})
	require.NoError(t, err)
	runs = result.Runs
	require.Len(t, runs, 2)
	assert.Equal(t, 4, result.Total)
	assert.Equal(t, []string{"other", "second"}, []string{runs[0].ID, runs[1].ID})
}

func TestStoreUpsertStepPreservesFirstStartedAtAndIgnoresBlankAsset(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	runID, err := store.Create(ctx, PipelineRun{PipelineID: "pipeline-id", Pipeline: "analytics", Trigger: RunTriggerManual})
	require.NoError(t, err)
	started := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	laterStart := started.Add(time.Minute)
	finished := started.Add(5 * time.Minute)

	require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{RunID: runID, Asset: "orders", Status: RunStatusRunning, StartedAt: &started}))
	require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{RunID: runID, Asset: "orders", Status: RunStatusSuccess, StartedAt: &laterStart, FinishedAt: &finished}))
	require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{RunID: runID, Asset: " ", Status: RunStatusSuccess}))

	steps, err := store.ListSteps(ctx, runID)
	require.NoError(t, err)
	require.Len(t, steps, 1)
	assert.Equal(t, "orders", steps[0].Asset)
	require.NotNil(t, steps[0].StartedAt)
	require.NotNil(t, steps[0].FinishedAt)
	assert.Equal(t, started, *steps[0].StartedAt)
	assert.Equal(t, finished, *steps[0].FinishedAt)
	assert.Equal(t, RunStatusSuccess, steps[0].Status)
}

func TestStoreFinishOpenStepsUpdatesOnlyUnfinishedSteps(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	runID, err := store.Create(ctx, PipelineRun{PipelineID: "pipeline-id", Pipeline: "analytics", Trigger: RunTriggerManual})
	require.NoError(t, err)
	started := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	previousFinish := started.Add(time.Minute)
	finish := started.Add(2 * time.Minute)

	require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{RunID: runID, Asset: "finished", Status: RunStatusSuccess, StartedAt: &started, FinishedAt: &previousFinish}))
	require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{RunID: runID, Asset: "open", Status: RunStatusRunning, StartedAt: &started}))
	require.NoError(t, store.FinishOpenSteps(ctx, runID, RunStatusFailed, finish, assert.AnError))

	steps, err := store.ListSteps(ctx, runID)
	require.NoError(t, err)
	require.Len(t, steps, 2)
	assert.Equal(t, "finished", steps[0].Asset)
	assert.Equal(t, RunStatusSuccess, steps[0].Status)
	assert.Equal(t, previousFinish, *steps[0].FinishedAt)
	assert.Equal(t, "open", steps[1].Asset)
	assert.Equal(t, RunStatusFailed, steps[1].Status)
	assert.Equal(t, finish, *steps[1].FinishedAt)
	assert.Equal(t, assert.AnError.Error(), steps[1].Error)
}

func TestStoreDeletesRunLogsAndStepsWithRun(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	runID, err := store.Create(ctx, PipelineRun{PipelineID: "pipeline-id", Pipeline: "analytics", Trigger: RunTriggerManual})
	require.NoError(t, err)
	require.NoError(t, store.AppendLog(ctx, runID, LogLine{Line: "hello"}))
	require.NoError(t, store.UpsertStep(ctx, PipelineRunStep{RunID: runID, Asset: "orders", Status: RunStatusRunning}))

	_, err = store.db.ExecContext(ctx, `DELETE FROM pipeline_runs WHERE id = ?`, runID)
	require.NoError(t, err)
	assert.Equal(t, 0, countRows(t, store, `SELECT COUNT(*) FROM pipeline_run_logs WHERE run_id = ?`, runID))
	assert.Equal(t, 0, countRows(t, store, `SELECT COUNT(*) FROM pipeline_run_steps WHERE run_id = ?`, runID))
}

func TestStorePersistsRunsWatermarksAndScheduleSettingsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	store, err := OpenStore(path)
	require.NoError(t, err)

	ctx := context.Background()
	upTo := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	runID, err := store.Create(ctx, PipelineRun{ID: "persisted-run", PipelineID: "pipeline-id", Pipeline: "analytics", Trigger: RunTriggerManual, Status: RunStatusSuccess})
	require.NoError(t, err)
	require.Equal(t, "persisted-run", runID)
	require.NoError(t, store.SetInterval(ctx, "pipeline-id", upTo))
	require.NoError(t, store.SetScheduleEnabled(ctx, "pipeline-id", false))
	require.NoError(t, store.Close())

	store, err = OpenStore(path)
	require.NoError(t, err)
	defer store.Close()

	run, _, _, err := store.Get(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, "analytics", run.Pipeline)
	watermark, ok, err := store.LastInterval(ctx, "pipeline-id")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, upTo, watermark)
	enabled, ok, err := store.ScheduleEnabled(ctx, "pipeline-id")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.False(t, enabled)
}

func TestStoreMigratesRiverQueueTables(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	var applied bool
	err = store.db.QueryRowContext(context.Background(), `SELECT is_applied FROM goose_db_version WHERE version_id = 1`).Scan(&applied)
	require.NoError(t, err)
	assert.True(t, applied)

	var tableName string
	err = store.db.QueryRowContext(context.Background(), `SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'river_job'`).Scan(&tableName)
	require.NoError(t, err)
	assert.Equal(t, "river_job", tableName)
}

func TestStoreDetectsActiveRuns(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	active, err := store.HasActiveRun(ctx, "pipeline-id")
	require.NoError(t, err)
	assert.False(t, active)
	activeRunID, err := store.ActiveRunID(ctx, "pipeline-id", "")
	require.NoError(t, err)
	assert.Empty(t, activeRunID)

	id, err := store.Create(ctx, PipelineRun{PipelineID: "pipeline-id", Pipeline: "analytics", Trigger: RunTriggerManual, Status: RunStatusQueued})
	require.NoError(t, err)
	active, err = store.HasActiveRun(ctx, "pipeline-id")
	require.NoError(t, err)
	assert.True(t, active)
	activeRunID, err = store.ActiveRunID(ctx, "pipeline-id", "")
	require.NoError(t, err)
	assert.Equal(t, id, activeRunID)

	require.NoError(t, store.Finish(ctx, id, RunStatusFailed, assert.AnError))
	active, err = store.HasActiveRun(ctx, "pipeline-id")
	require.NoError(t, err)
	assert.False(t, active)
	activeRunID, err = store.ActiveRunID(ctx, "pipeline-id", "")
	require.NoError(t, err)
	assert.Empty(t, activeRunID)
}

func TestStoreListFiltersAndPaginatesRuns(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	for index, run := range []PipelineRun{
		{PipelineID: "pipeline-a", Pipeline: "analytics", Environment: "default", Trigger: RunTriggerManual, Status: RunStatusSuccess},
		{PipelineID: "pipeline-b", Pipeline: "marketing", Environment: "prod", Trigger: RunTriggerManual, Status: RunStatusFailed},
		{PipelineID: "pipeline-c", Pipeline: "analytics_daily", Environment: "prod", Trigger: RunTriggerManual, Status: RunStatusSuccess},
	} {
		startedAt := base.Add(time.Duration(index) * time.Minute)
		run.StartedAt = &startedAt
		_, err := store.Create(ctx, run)
		require.NoError(t, err)
	}

	result, err := store.List(ctx, RunFilter{Query: "analytics", Status: RunStatusSuccess, Limit: 1})
	require.NoError(t, err)
	require.Len(t, result.Runs, 1)
	assert.Equal(t, 2, result.Total)
	assert.Equal(t, "analytics_daily", result.Runs[0].Pipeline)

	result, err = store.List(ctx, RunFilter{Query: "analytics", Status: RunStatusSuccess, Limit: 1, Offset: 1})
	require.NoError(t, err)
	require.Len(t, result.Runs, 1)
	assert.Equal(t, 2, result.Total)
	assert.Equal(t, "analytics", result.Runs[0].Pipeline)

	result, err = store.List(ctx, RunFilter{Environment: "prod", Limit: 10})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Total)
}

func TestStorePersistsScheduleEnabledState(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	enabled, ok, err := store.ScheduleEnabled(ctx, "pipeline-id")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.False(t, enabled)

	require.NoError(t, store.SetScheduleEnabled(ctx, "pipeline-id", false))
	enabled, ok, err = store.ScheduleEnabled(ctx, "pipeline-id")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.False(t, enabled)

	require.NoError(t, store.SetScheduleEnabled(ctx, "pipeline-id", true))
	enabled, ok, err = store.ScheduleEnabled(ctx, "pipeline-id")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.True(t, enabled)
}

func countRows(t *testing.T, store *Store, query string, args ...any) int {
	t.Helper()
	var count int
	require.NoError(t, store.db.QueryRowContext(context.Background(), query, args...).Scan(&count))
	return count
}
