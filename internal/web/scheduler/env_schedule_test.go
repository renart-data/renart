package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openEnvTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestEnvScheduleStoreRoundTrip(t *testing.T) {
	store := openEnvTestStore(t)
	ctx := context.Background()

	require.NoError(t, store.UpsertEnvSchedule(ctx, EnvSchedule{
		PipelineUUID:      "uuid-1",
		Environment:       "prod",
		SnapshotVersionID: "snap-1",
		Cron:              "0 * * * *",
		Timezone:          "UTC",
		Vars:              map[string]any{"region": "eu"},
		CatchupPolicy:     CatchupRunOnce,
		Status:            ScheduleStatusActive,
	}))
	require.NoError(t, store.UpsertEnvSchedule(ctx, EnvSchedule{
		PipelineUUID:  "uuid-1",
		Environment:   "staging",
		Cron:          "@daily",
		Timezone:      "UTC",
		CatchupPolicy: CatchupSkip,
		Status:        ScheduleStatusPaused,
	}))

	rows, err := store.ListEnvSchedules(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2)

	prod, found, err := store.GetEnvSchedule(ctx, "uuid-1", "prod")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "snap-1", prod.SnapshotVersionID)
	assert.Equal(t, CatchupRunOnce, prod.CatchupPolicy)
	assert.Equal(t, map[string]any{"region": "eu"}, prod.Vars)

	// Same pipeline, independent environments.
	staging, found, err := store.GetEnvSchedule(ctx, "uuid-1", "staging")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleStatusPaused, staging.Status)

	require.NoError(t, store.SetEnvScheduleStatus(ctx, "uuid-1", "prod", ScheduleStatusArchived, ArchivedReasonMissing))
	archived, _, err := store.GetEnvSchedule(ctx, "uuid-1", "prod")
	require.NoError(t, err)
	assert.Equal(t, ScheduleStatusArchived, archived.Status)
	assert.Equal(t, ArchivedReasonMissing, archived.ArchivedReason)

	next := time.Now().UTC().Add(time.Hour)
	require.NoError(t, store.SetEnvScheduleNextRun(ctx, "uuid-1", "staging", &next))
	staging, _, err = store.GetEnvSchedule(ctx, "uuid-1", "staging")
	require.NoError(t, err)
	require.NotNil(t, staging.NextRunAt)
	assert.WithinDuration(t, next, *staging.NextRunAt, time.Second)
}

func TestReconcileContainsPerRowStoreFailures(t *testing.T) {
	store := openEnvTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	for _, row := range []EnvSchedule{
		{
			PipelineUUID:      "a-missing",
			Environment:       "prod",
			SnapshotVersionID: "snap-missing",
			Cron:              "@hourly",
			Timezone:          "UTC",
			CatchupPolicy:     CatchupSkip,
			Status:            ScheduleStatusActive,
		},
		{
			PipelineUUID:      "b-healthy",
			Environment:       "prod",
			SnapshotVersionID: "snap-healthy",
			Cron:              "@hourly",
			Timezone:          "UTC",
			CatchupPolicy:     CatchupSkip,
			Status:            ScheduleStatusActive,
		},
	} {
		require.NoError(t, store.UpsertEnvSchedule(ctx, row))
	}
	_, err := store.db.ExecContext(ctx, `
		CREATE TRIGGER fail_missing_schedule_archive
		BEFORE UPDATE OF status ON renart_schedules
		WHEN OLD.pipeline_id = 'a-missing'
		BEGIN
			SELECT RAISE(ABORT, 'injected per-row schedule failure');
		END`)
	require.NoError(t, err)

	service := New(Options{
		Store:    store,
		StateDir: t.TempDir(),
		Runner:   func(context.Context, RunRequest, func(string)) RunResult { return RunResult{} },
		ResolvePipelineRef: func(_ context.Context, uuid string) (PipelineRef, bool) {
			if uuid == "b-healthy" {
				return PipelineRef{EncodedID: "healthy", Name: "healthy"}, true
			}
			return PipelineRef{}, false
		},
		CheckSnapshot: func(context.Context, string, string) error { return nil },
	})
	require.NoError(t, service.Start(ctx), "one broken row must not make the scheduler unavailable")
	t.Cleanup(service.Stop)
	assert.Equal(t, SchedulerOwnershipOwner, service.Ownership().State)

	missing, found, err := store.GetEnvSchedule(ctx, "a-missing", "prod")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleStatusActive, missing.Status, "the failed mutation remains retryable")

	healthy, found, err := store.GetEnvSchedule(ctx, "b-healthy", "prod")
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, healthy.NextRunAt, "later rows must still reconcile")
}

func TestUpsertEnvScheduleValidation(t *testing.T) {
	store := openEnvTestStore(t)
	rejectDeploymentDependencies := false
	service := New(Options{
		Store:    store,
		StateDir: t.TempDir(),
		Runner:   func(ctx context.Context, req RunRequest, onLog func(string)) RunResult { return RunResult{} },
		ResolvePipelineRef: func(ctx context.Context, uuid string) (PipelineRef, bool) {
			return PipelineRef{EncodedID: "enc", Name: "analytics"}, true
		},
		PipelineIntervalAware: func(ctx context.Context, uuid string) bool { return false },
		DeployPipeline:        func(ctx context.Context, uuid string) (string, error) { return "snap-new", nil },
		ValidateSnapshot: func(_ context.Context, pipelineUUID, versionID string) error {
			if pipelineUUID != "uuid-1" || (versionID != "snap-new" && versionID != "snap-existing") {
				return errors.New("snapshot does not belong to pipeline")
			}
			return nil
		},
		ValidateScheduleVariables: func(_ context.Context, pipelineUUID, versionID string, overrides map[string]any) error {
			if pipelineUUID != "uuid-1" || (versionID != "snap-new" && versionID != "snap-existing") {
				return errors.New("wrong pinned deployment")
			}
			if overrides["region"] == "invalid" {
				return errors.New("variable \"region\" does not satisfy its declared schema")
			}
			return nil
		},
		ValidateScheduleDeploymentDependencies: func(
			_ context.Context,
			pipelineUUID string,
			versionID string,
			environment string,
			overrides map[string]any,
		) error {
			assert.Equal(t, "uuid-1", pipelineUUID)
			assert.NotEmpty(t, versionID)
			assert.NotEmpty(t, environment)
			if environment == "variables" {
				assert.Equal(t, "eu", overrides["region"])
			}
			if rejectDeploymentDependencies {
				return errors.New("URI ownership moved to another producer")
			}
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, service.Start(ctx))
	t.Cleanup(func() {
		cancel()
		service.Stop()
	})

	_, err := service.UpsertEnvSchedule(ctx, "uuid-1", UpsertEnvScheduleRequest{Cron: "@hourly"})
	require.ErrorContains(t, err, "environment is required")

	_, err = service.UpsertEnvSchedule(ctx, "uuid-1", UpsertEnvScheduleRequest{Environment: "prod", Cron: "not a cron"})
	require.ErrorContains(t, err, "invalid cron")

	_, err = service.UpsertEnvSchedule(ctx, "uuid-1", UpsertEnvScheduleRequest{Environment: "prod", Cron: "@hourly", CatchupPolicy: CatchupBackfill})
	require.ErrorContains(t, err, "interval-aware")

	_, err = service.UpsertEnvSchedule(ctx, "uuid-1", UpsertEnvScheduleRequest{Environment: "prod", Cron: "@hourly"})
	require.ErrorContains(t, err, "deployed snapshot is required")

	created, err := service.UpsertEnvSchedule(ctx, "uuid-1", UpsertEnvScheduleRequest{
		Environment: "prod", Cron: "@hourly", DeployNow: true,
		Vars: map[string]any{"region": "eu"},
	})
	require.NoError(t, err)
	assert.Equal(t, "snap-new", created.SnapshotVersionID)
	assert.Equal(t, ScheduleStatusActive, created.Status)
	assert.Equal(t, "enc", created.PipelineID)

	// Editing an existing schedule resolves its current pin and private
	// variables on the server without requiring a redeploy or exposing values.
	updated, err := service.UpsertEnvSchedule(ctx, "uuid-1", UpsertEnvScheduleRequest{
		Environment: "prod", Cron: "@daily", PreserveSnapshot: true, PreserveVariables: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "snap-new", updated.SnapshotVersionID)
	assert.Equal(t, "@daily", updated.Cron)
	assert.Equal(t, map[string]any{"region": "eu"}, updated.Vars)

	_, err = service.UpsertEnvSchedule(ctx, "uuid-1", UpsertEnvScheduleRequest{
		Environment: "missing", Cron: "@daily", PreserveSnapshot: true,
	})
	require.ErrorContains(t, err, "requires an existing schedule")

	_, err = service.UpsertEnvSchedule(ctx, "uuid-1", UpsertEnvScheduleRequest{
		Environment: "prod", Cron: "@daily", PreserveSnapshot: true, DeployNow: true,
	})
	require.ErrorContains(t, err, "mutually exclusive")

	_, err = service.UpsertEnvSchedule(ctx, "uuid-1", UpsertEnvScheduleRequest{
		Environment: "prod", Cron: "@daily", PreserveSnapshot: true, PreserveVariables: true,
		Vars: map[string]any{},
	})
	require.ErrorContains(t, err, "cannot be combined")

	_, err = service.UpsertEnvSchedule(ctx, "uuid-1", UpsertEnvScheduleRequest{
		Environment: "staging", Cron: "@daily", SnapshotVersionID: "wrong",
	})
	require.ErrorContains(t, err, "not executable for this pipeline")

	_, err = service.UpsertEnvSchedule(ctx, "uuid-1", UpsertEnvScheduleRequest{
		Environment: "invalid-vars", Cron: "@daily", SnapshotVersionID: "snap-existing",
		Vars: map[string]any{"region": "invalid"},
	})
	require.ErrorContains(t, err, "invalid for the pinned deployment")

	paused, err := service.UpsertEnvSchedule(ctx, "uuid-1", UpsertEnvScheduleRequest{
		Environment: "variables", Cron: "@daily", SnapshotVersionID: "snap-existing",
		Vars: map[string]any{"region": "eu"}, Paused: true,
	})
	require.NoError(t, err)
	assert.Equal(t, ScheduleStatusPaused, paused.Status)
	err = service.SetEnvScheduleLifecycle(ctx, "uuid-1", "variables", ScheduleStatusActive)
	require.NoError(t, err)
	activeVariables, found, err := store.GetEnvSchedule(ctx, "uuid-1", "variables")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleStatusActive, activeVariables.Status)

	promoted, err := service.PromoteEnvSchedules(ctx, "uuid-1", PromoteEnvSchedulesRequest{
		SnapshotVersionID: "snap-new",
		Schedules: []EnvSchedulePinSelection{
			{Environment: "prod", ExpectedSnapshotVersionID: scheduleSnapshotExpectation("snap-new")},
			{Environment: "variables", ExpectedSnapshotVersionID: scheduleSnapshotExpectation("snap-existing")},
		},
	})
	require.NoError(t, err)
	require.Len(t, promoted, 2)
	assert.Equal(t, "snap-new", promoted[1].SnapshotVersionID)

	rejectDeploymentDependencies = true
	_, err = service.PromoteEnvSchedules(ctx, "uuid-1", PromoteEnvSchedulesRequest{
		SnapshotVersionID: "snap-existing",
		Schedules: []EnvSchedulePinSelection{{
			Environment: "variables", ExpectedSnapshotVersionID: scheduleSnapshotExpectation("snap-new"),
		}},
	})
	require.ErrorContains(t, err, "URI ownership moved")
	variablesSchedule, found, loadErr := store.GetEnvSchedule(ctx, "uuid-1", "variables")
	require.NoError(t, loadErr)
	require.True(t, found)
	assert.Equal(t, "snap-new", variablesSchedule.SnapshotVersionID)
	rejectDeploymentDependencies = false

	_, err = service.PromoteEnvSchedules(ctx, "uuid-1", PromoteEnvSchedulesRequest{
		SnapshotVersionID: "snap-existing",
		Schedules: []EnvSchedulePinSelection{
			{Environment: "prod", ExpectedSnapshotVersionID: scheduleSnapshotExpectation("stale-client-pin")},
			{Environment: "variables", ExpectedSnapshotVersionID: scheduleSnapshotExpectation("snap-new")},
		},
	})
	require.ErrorContains(t, err, "changed after deployment review")
	prod, found, loadErr := store.GetEnvSchedule(ctx, "uuid-1", "prod")
	require.NoError(t, loadErr)
	require.True(t, found)
	assert.Equal(t, "snap-new", prod.SnapshotVersionID, "a stale batch must not partially promote")
}

func TestUpsertEnvScheduleEditsPausedDeclarationBeforeFirstDeployment(t *testing.T) {
	store := openEnvTestStore(t)
	declarations := NewScheduleDeclarationStore(filepath.Join(t.TempDir(), ".renart", "schedules.yml"))
	require.NoError(t, declarations.Set("uuid-1", "prod", ScheduleDeclaration{
		Cron:      "@hourly",
		Timezone:  "UTC",
		Paused:    true,
		Variables: map[string]any{"region": "eu"},
	}))
	service := New(Options{
		Store:                store,
		StateDir:             t.TempDir(),
		Runner:               func(context.Context, RunRequest, func(string)) RunResult { return RunResult{} },
		ScheduleDeclarations: declarations,
		ValidateSnapshot:     func(context.Context, string, string) error { return nil },
		ValidateScheduleVariables: func(context.Context, string, string, map[string]any) error {
			return nil
		},
		ResolvePipelineRef: func(context.Context, string) (PipelineRef, bool) {
			return PipelineRef{EncodedID: "enc", Name: "analytics"}, true
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	require.NoError(t, service.Start(ctx))
	t.Cleanup(func() {
		cancel()
		service.Stop()
	})

	updated, err := service.UpsertEnvSchedule(ctx, "uuid-1", UpsertEnvScheduleRequest{
		Environment: "prod", Cron: "@daily", Timezone: "Europe/Berlin", Paused: true,
		PreserveSnapshot: true, PreserveVariables: true,
	})
	require.NoError(t, err)
	assert.Empty(t, updated.SnapshotVersionID)
	assert.Equal(t, ScheduleStatusPaused, updated.Status)
	assert.Equal(t, map[string]any{"region": "eu"}, updated.Vars)

	declaration, found, err := declarations.Get("uuid-1", "prod")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "@daily", declaration.Cron)
	assert.Equal(t, "Europe/Berlin", declaration.Timezone)
	assert.Equal(t, map[string]any{"region": "eu"}, declaration.Variables)

	_, err = service.UpsertEnvSchedule(ctx, "uuid-1", UpsertEnvScheduleRequest{
		Environment: "prod", Cron: "@daily", PreserveSnapshot: true, PreserveVariables: true,
	})
	require.ErrorContains(t, err, "deployed snapshot is required")

	promoted, err := service.PromoteEnvSchedules(ctx, "uuid-1", PromoteEnvSchedulesRequest{
		SnapshotVersionID: "snap-first",
		Schedules: []EnvSchedulePinSelection{{
			Environment:               "prod",
			ExpectedSnapshotVersionID: scheduleSnapshotExpectation(""),
		}},
	})
	require.NoError(t, err)
	require.Len(t, promoted, 1)
	assert.Equal(t, "snap-first", promoted[0].SnapshotVersionID)
	assert.Equal(t, ScheduleStatusPaused, promoted[0].Status)

	_, err = service.PromoteEnvSchedules(ctx, "uuid-1", PromoteEnvSchedulesRequest{
		SnapshotVersionID: "snap-second",
		Schedules: []EnvSchedulePinSelection{{
			Environment: "prod",
		}},
	})
	require.ErrorContains(t, err, "expected_snapshot_version_id")
}

func scheduleSnapshotExpectation(value string) *string {
	return &value
}

func TestEnvScheduledWorkerRunsWithEnvironmentAndWatermark(t *testing.T) {
	stateDir := t.TempDir()
	store, err := OpenStore(filepath.Join(stateDir, "state.db"))
	require.NoError(t, err)
	defer store.Close()

	var capturedRequest RunRequest
	service := New(Options{
		Store:    store,
		StateDir: stateDir,
		Runner: func(ctx context.Context, req RunRequest, onLog func(string)) RunResult {
			capturedRequest = req
			if err := completeTestScheduledRunUnits(req); err != nil {
				return RunResult{Status: "error", Error: err.Error()}
			}
			return RunResult{Status: "ok"}
		},
		ResolvePipelineRef: func(ctx context.Context, uuid string) (PipelineRef, bool) {
			if uuid == "uuid-1" {
				return PipelineRef{EncodedID: "encoded-id", Name: "analytics"}, true
			}
			return PipelineRef{}, false
		},
		PlanScheduledRun: testScheduledRunPlan,
	})

	worker := &pipelineRunWorker{service: service}
	start := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	require.NoError(t, worker.Work(context.Background(), &river.Job[pipelineRunJobArgs]{Args: pipelineRunJobArgs{
		PipelineUUID:      "uuid-1",
		PipelineName:      "analytics",
		Environment:       "prod",
		Trigger:           RunTriggerSchedule,
		Schedule:          "@hourly",
		Timezone:          "UTC",
		Start:             start.Format(time.RFC3339Nano),
		End:               end.Format(time.RFC3339Nano),
		SnapshotVersionID: "snap-7",
		Variables:         map[string]any{"region": "private-schedule-value"},
	}}))

	assert.Equal(t, "encoded-id", capturedRequest.PipelineID)
	assert.Equal(t, "prod", capturedRequest.Environment)
	assert.Equal(t, "snap-7", capturedRequest.SnapshotVersionID)
	assert.Equal(t, map[string]any{"region": "private-schedule-value"}, capturedRequest.VariableOverrides)
	require.NotNil(t, capturedRequest.ConfirmedPlan)

	result, err := service.ListRuns(context.Background(), RunFilter{PipelineID: "encoded-id"})
	require.NoError(t, err)
	require.Len(t, result.Runs, 1)
	run := result.Runs[0]
	assert.Equal(t, "prod", run.Environment)
	assert.Equal(t, "snap-7", run.SnapshotVersionID)
	plan, found, err := store.GetRunPlan(context.Background(), run.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.NotContains(t, string(plan.Artifact), "private-schedule-value")
	spec, found, err := store.GetRunSpec(context.Background(), run.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, map[string]any{"region": "private-schedule-value"}, spec.Requested.Variables)
	require.NotNil(t, spec.Requested.ExecutionTime)

	// Watermark is keyed by (pipeline UUID, environment), so run history
	// and progress survive directory moves and stay per-environment.
	watermark, ok, err := store.LastInterval(context.Background(), "uuid-1|prod")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, end, watermark)

	// A vanished pipeline skips silently instead of failing the job.
	require.NoError(t, worker.Work(context.Background(), &river.Job[pipelineRunJobArgs]{Args: pipelineRunJobArgs{
		PipelineUUID: "gone",
		Environment:  "prod",
		Trigger:      RunTriggerSchedule,
	}}))
}

func TestCatchUpQueuesV2ScheduleSignalInsteadOfExecutionContract(t *testing.T) {
	t.Parallel()
	store := openEnvTestStore(t)
	ctx := context.Background()
	client, err := river.NewClient(riversqlite.New(store.db), &river.Config{})
	require.NoError(t, err)
	schedule, err := parseSchedule("@hourly", "UTC")
	require.NoError(t, err)
	start, end, ok := previousScheduleInterval(schedule, time.Now().UTC())
	require.True(t, ok)
	row := EnvSchedule{
		PipelineUUID: "uuid-1", Environment: "prod", SnapshotVersionID: "snapshot-id",
		Cron: "@hourly", Timezone: "UTC", CatchupPolicy: CatchupRunOnce,
		Vars: map[string]any{"region": "eu"},
	}
	require.NoError(t, store.SetInterval(ctx, watermarkKey(row), start))
	service := New(Options{Store: store})
	require.NoError(t, service.catchUp(
		ctx, client, row, PipelineRef{EncodedID: "pipeline-id", Name: "analytics"}, schedule,
	))

	var kind, body string
	require.NoError(t, store.db.QueryRowContext(ctx, `
		SELECT kind, json(args)
		FROM river_job
		WHERE queue = ?
		ORDER BY id DESC
		LIMIT 1`, pipelineRunQueue).Scan(&kind, &body))
	assert.Equal(t, scheduleSignalJobKind, kind)
	assert.Contains(t, body, `"pipeline_uuid":"uuid-1"`)
	assert.Contains(t, body, `"environment":"prod"`)
	assert.Contains(t, body, `"snapshot_version_id":"snapshot-id"`)
	assert.Contains(t, body, `"start":"`+formatTime(start)+`"`)
	assert.Contains(t, body, `"end":"`+formatTime(end)+`"`)
	assert.Equal(t, 0, countRows(t, store, `SELECT COUNT(*) FROM river_job WHERE kind = ?`, pipelineRunJobKind))
}

func TestEnvScheduledWorkerFailureDoesNotAdvanceWatermark(t *testing.T) {
	t.Parallel()
	store := openEnvTestStore(t)
	ctx := context.Background()
	service := New(Options{
		Store:    store,
		StateDir: t.TempDir(),
		Runner: func(_ context.Context, req RunRequest, _ func(string)) RunResult {
			require.True(t, req.Scheduled)
			require.Equal(t, "corrupt-snapshot", req.SnapshotVersionID)
			return RunResult{Status: "error", Error: "deployment blob hash mismatch"}
		},
		ResolvePipelineRef: func(context.Context, string) (PipelineRef, bool) {
			return PipelineRef{EncodedID: "encoded-id", Name: "analytics"}, true
		},
		PlanScheduledRun: testScheduledRunPlan,
	})

	start := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	worker := &pipelineRunWorker{service: service}
	require.NoError(t, worker.Work(ctx, &river.Job[pipelineRunJobArgs]{Args: pipelineRunJobArgs{
		PipelineUUID:      "uuid-1",
		PipelineName:      "analytics",
		Environment:       "prod",
		Trigger:           RunTriggerSchedule,
		Start:             start.Format(time.RFC3339Nano),
		End:               end.Format(time.RFC3339Nano),
		SnapshotVersionID: "corrupt-snapshot",
	}}))

	runs, err := service.ListRuns(ctx, RunFilter{PipelineID: "encoded-id"})
	require.NoError(t, err)
	require.Len(t, runs.Runs, 1)
	assert.Equal(t, RunStatusFailed, runs.Runs[0].Status)
	assert.Contains(t, runs.Runs[0].Error, "blob hash mismatch")
	_, hasWatermark, err := store.LastInterval(ctx, "uuid-1|prod")
	require.NoError(t, err)
	assert.False(t, hasWatermark)
}

func TestMigrateLegacySchedules(t *testing.T) {
	store := openEnvTestStore(t)
	ctx := context.Background()
	require.NoError(t, store.SetScheduleEnabled(ctx, "encoded-enabled", true))
	require.NoError(t, store.SetScheduleEnabled(ctx, "encoded-disabled", false))

	service := New(Options{
		Store:    store,
		StateDir: t.TempDir(),
		Runner:   func(ctx context.Context, req RunRequest, onLog func(string)) RunResult { return RunResult{} },
		Pipelines: func(ctx context.Context) ([]PipelineSchedule, error) {
			return []PipelineSchedule{
				{PipelineID: "encoded-enabled", PipelineUUID: "uuid-enabled", PipelineName: "a", Schedule: "@hourly", Timezone: "UTC", Catchup: true},
				{PipelineID: "encoded-disabled", PipelineUUID: "uuid-disabled", PipelineName: "b", Schedule: "@daily", Timezone: "UTC"},
				{PipelineID: "encoded-unscheduled", PipelineUUID: "uuid-unscheduled", PipelineName: "c"},
				// No explicit schedule_enabled row: a config-defined schedule is
				// enabled by default (the legacy Enabled = schedule != "" rule).
				{PipelineID: "encoded-config", PipelineUUID: "uuid-config", PipelineName: "d", Schedule: "@daily", Timezone: "UTC"},
			}, nil
		},
		DefaultEnvironment: func() string { return "dev" },
		LatestSnapshot: func(_ context.Context, pipelineUUID string) (string, bool, error) {
			if pipelineUUID == "uuid-enabled" {
				return "snap-enabled", true, nil
			}
			return "", false, nil
		},
		ValidateSnapshot: func(_ context.Context, pipelineUUID, versionID string) error {
			if pipelineUUID == "uuid-enabled" && versionID == "snap-enabled" {
				return nil
			}
			return errors.New("unexpected snapshot")
		},
	})

	require.NoError(t, service.migrateLegacySchedules(ctx))

	rows, err := store.ListEnvSchedules(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	byUUID := make(map[string]EnvSchedule, len(rows))
	for _, row := range rows {
		byUUID[row.PipelineUUID] = row
	}

	migrated := byUUID["uuid-enabled"]
	assert.Equal(t, "dev", migrated.Environment)
	assert.Equal(t, "@hourly", migrated.Cron)
	assert.Equal(t, CatchupRunOnce, migrated.CatchupPolicy)
	assert.Equal(t, ScheduleStatusActive, migrated.Status)
	assert.Equal(t, "snap-enabled", migrated.SnapshotVersionID)

	// The config-only pipeline has no deployment, so it is retained but paused
	// for review; the explicitly-disabled one stays out.
	config := byUUID["uuid-config"]
	assert.Equal(t, "@daily", config.Cron)
	assert.Equal(t, ScheduleStatusPaused, config.Status)
	assert.Empty(t, config.SnapshotVersionID)
	_, disabledMigrated := byUUID["uuid-disabled"]
	assert.False(t, disabledMigrated, "explicitly disabled schedule must not migrate")

	// Migration is one-shot: a second call must not duplicate or resurrect.
	require.NoError(t, service.migrateLegacySchedules(ctx))
	rows, err = store.ListEnvSchedules(ctx)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
}
