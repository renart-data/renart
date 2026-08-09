package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleOccurrenceKeyIsStableForNormalizedInterval(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 7, 18, 10, 0, 0, 123456789, time.FixedZone("CEST", 2*60*60))
	end := start.Add(time.Hour)
	first, err := newScheduleOccurrence(" pipeline-uuid ", " prod ", start, end)
	require.NoError(t, err)
	second, err := newScheduleOccurrence("pipeline-uuid", "prod", start.UTC(), end.UTC())
	require.NoError(t, err)
	otherEnvironment, err := newScheduleOccurrence("pipeline-uuid", "dev", start.UTC(), end.UTC())
	require.NoError(t, err)

	assert.Equal(t, first.Key, second.Key)
	assert.Len(t, first.Key, 64)
	assert.NotEqual(t, first.Key, otherEnvironment.Key)
	assert.Equal(t, start.UTC(), first.IntervalStart)
	require.ErrorContains(t, func() error {
		_, invalidErr := newScheduleOccurrence("pipeline-uuid", "prod", end, start)
		return invalidErr
	}(), "increasing interval")
}

func TestScheduleOccurrenceDeduplicatesActiveAndSuccessfulSignals(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	occurrence, run, spec, plan := scheduledOccurrenceFixture(t)

	persisted, changed, err := store.EnsureScheduleOccurrence(ctx, occurrence)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, ScheduleOccurrencePending, persisted.Status)
	_, changed, err = store.EnsureScheduleOccurrence(ctx, occurrence)
	require.NoError(t, err)
	assert.False(t, changed)

	runID, err := store.CreateScheduleOccurrenceAttemptWithSpecAndPlan(ctx, occurrence, run, spec, plan)
	require.NoError(t, err)
	require.NotEmpty(t, runID)
	active, found, err := store.GetScheduleOccurrence(ctx, occurrence.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleOccurrenceActive, active.Status)
	assert.Equal(t, runID, active.CurrentRunID)
	assert.Equal(t, 1, active.AttemptCount)

	_, err = store.CreateScheduleOccurrenceAttemptWithSpecAndPlan(ctx, occurrence, run, spec, plan)
	require.ErrorIs(t, err, ErrScheduleOccurrenceAlreadyAdmitted)
	var duplicate *ScheduleOccurrenceAlreadyAdmittedError
	require.ErrorAs(t, err, &duplicate)
	assert.Equal(t, runID, duplicate.RunID)
	assert.Equal(t, ScheduleOccurrenceActive, duplicate.Status)
	assert.Equal(t, 1, countRows(t, store, `SELECT COUNT(*) FROM pipeline_runs`))

	started := time.Now().UTC()
	finished := started.Add(time.Second)
	require.NoError(t, store.UpdateRunUnit(ctx, runID, PipelineRunUnitEvent{
		Position: 0, Status: PipelineRunUnitRunning, StartedAt: &started,
	}))
	require.NoError(t, store.UpdateRunUnit(ctx, runID, PipelineRunUnitEvent{
		Position: 0, Status: PipelineRunUnitSuccess, StartedAt: &started, FinishedAt: &finished,
	}))
	require.NoError(t, store.Finish(ctx, runID, RunStatusSuccess, nil))
	succeeded, found, err := store.GetScheduleOccurrence(ctx, occurrence.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleOccurrenceSuccess, succeeded.Status)

	_, changed, err = store.EnsureScheduleOccurrence(ctx, occurrence)
	require.NoError(t, err)
	assert.False(t, changed, "a successful occurrence is immutable when duplicate signals arrive")
	_, err = store.CreateScheduleOccurrenceAttemptWithSpecAndPlan(ctx, occurrence, run, spec, plan)
	require.ErrorIs(t, err, ErrScheduleOccurrenceAlreadyAdmitted)
	assert.Equal(t, 1, countRows(t, store, `SELECT COUNT(*) FROM pipeline_runs`))
}

func TestScheduleOccurrenceRetriesFailedRunAsNumberedAttempt(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	occurrence, run, spec, plan := scheduledOccurrenceFixture(t)
	_, _, err = store.EnsureScheduleOccurrence(ctx, occurrence)
	require.NoError(t, err)

	firstRunID, err := store.CreateScheduleOccurrenceAttemptWithSpecAndPlan(ctx, occurrence, run, spec, plan)
	require.NoError(t, err)
	require.NoError(t, store.Finish(ctx, firstRunID, RunStatusFailed, errors.New("temporary warehouse failure")))
	failed, found, err := store.GetScheduleOccurrence(ctx, occurrence.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleOccurrenceFailed, failed.Status)
	assert.Equal(t, 1, failed.AttemptCount)

	pending, changed, err := store.EnsureScheduleOccurrence(ctx, occurrence)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, ScheduleOccurrencePending, pending.Status)
	deferred, found, err := store.DeferredScheduleOccurrence(ctx, occurrence.PipelineUUID, occurrence.Environment)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, 1, deferred.AttemptCount)

	secondRunID, err := store.CreateScheduleOccurrenceAttemptWithSpecAndPlan(ctx, occurrence, run, spec, plan)
	require.NoError(t, err)
	assert.NotEqual(t, firstRunID, secondRunID)
	retried, found, err := store.GetScheduleOccurrence(ctx, occurrence.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleOccurrenceActive, retried.Status)
	assert.Equal(t, secondRunID, retried.CurrentRunID)
	assert.Equal(t, 2, retried.AttemptCount)
	assert.Equal(t, 2, countRows(t, store, `SELECT COUNT(*) FROM schedule_occurrence_attempts WHERE occurrence_key = ?`, occurrence.Key))
}

func TestScheduleOccurrenceSlotConflictRollsBackAttemptAndStaysDeferred(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	occurrence, scheduledRun, scheduledSpec, plan := scheduledOccurrenceFixture(t)
	_, _, err = store.EnsureScheduleOccurrence(ctx, occurrence)
	require.NoError(t, err)

	manualRun := PipelineRun{
		ID: "manual-owner", PipelineID: scheduledRun.PipelineID, PipelineUUID: scheduledRun.PipelineUUID,
		Pipeline: scheduledRun.Pipeline, Environment: "dev", Trigger: RunTriggerManual, Status: RunStatusQueued,
	}
	_, err = store.CreateWithSpec(ctx, manualRun, manualRunSpec(manualRun, RunSourceWorkingTree, ""))
	require.NoError(t, err)

	_, err = store.CreateScheduleOccurrenceAttemptWithSpecAndPlan(ctx, occurrence, scheduledRun, scheduledSpec, plan)
	require.ErrorIs(t, err, ErrPipelineRunActive)
	persisted, found, err := store.GetScheduleOccurrence(ctx, occurrence.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleOccurrencePending, persisted.Status)
	assert.Zero(t, persisted.AttemptCount)
	assert.Empty(t, persisted.CurrentRunID)
	assert.Equal(t, 0, countRows(t, store, `SELECT COUNT(*) FROM schedule_occurrence_attempts`))
	assert.Equal(t, 1, countRows(t, store, `SELECT COUNT(*) FROM pipeline_runs`))
}

func TestConcurrentScheduleOccurrenceAdmissionCreatesOneRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	firstStore, err := OpenStore(path)
	require.NoError(t, err)
	defer firstStore.Close()
	secondStore, err := OpenStore(path)
	require.NoError(t, err)
	defer secondStore.Close()
	ctx := context.Background()
	occurrence, run, spec, plan := scheduledOccurrenceFixture(t)
	_, _, err = firstStore.EnsureScheduleOccurrence(ctx, occurrence)
	require.NoError(t, err)

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, store := range []*Store{firstStore, secondStore} {
		wg.Add(1)
		go func(store *Store) {
			defer wg.Done()
			<-start
			_, admissionErr := store.CreateScheduleOccurrenceAttemptWithSpecAndPlan(ctx, occurrence, run, spec, plan)
			results <- admissionErr
		}(store)
	}
	close(start)
	wg.Wait()
	close(results)
	var admitted, duplicate int
	for result := range results {
		switch {
		case result == nil:
			admitted++
		case errors.Is(result, ErrScheduleOccurrenceAlreadyAdmitted):
			duplicate++
		default:
			require.NoError(t, result)
		}
	}
	assert.Equal(t, 1, admitted)
	assert.Equal(t, 1, duplicate)
	assert.Equal(t, 1, countRows(t, firstStore, `SELECT COUNT(*) FROM pipeline_runs`))
}

func TestScheduledWorkerDoesNotReexecuteCompletedOccurrence(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	var runnerCalls atomic.Int32
	service := New(Options{
		Store: store,
		ResolvePipelineRef: func(context.Context, string) (PipelineRef, bool) {
			return PipelineRef{EncodedID: "pipeline-id", Name: "analytics"}, true
		},
		PlanScheduledRun: testScheduledRunPlan,
		Runner: func(_ context.Context, req RunRequest, _ func(string)) RunResult {
			runnerCalls.Add(1)
			if err := completeTestScheduledRunUnits(req); err != nil {
				return RunResult{Status: "error", Error: err.Error()}
			}
			return RunResult{Status: "ok"}
		},
	})
	start := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	args := pipelineRunJobArgs{
		PipelineUUID: "pipeline-uuid", Environment: "prod", PipelineName: "analytics",
		Start: start.Format(time.RFC3339Nano), End: start.Add(time.Hour).Format(time.RFC3339Nano),
		SnapshotVersionID: "snapshot-id",
	}
	worker := &pipelineRunWorker{service: service}
	require.NoError(t, worker.Work(context.Background(), &river.Job[pipelineRunJobArgs]{
		JobRow: &rivertype.JobRow{ID: 101}, Args: args,
	}))
	require.NoError(t, worker.Work(context.Background(), &river.Job[pipelineRunJobArgs]{
		JobRow: &rivertype.JobRow{ID: 202}, Args: args,
	}))

	assert.EqualValues(t, 1, runnerCalls.Load())
	assert.Equal(t, 1, countRows(t, store, `SELECT COUNT(*) FROM pipeline_runs`))
	assert.Equal(t, 1, countRows(t, store, `SELECT COUNT(*) FROM schedule_occurrences`))
}

func TestV2ScheduleSignalAtomicallyQueuesRunIDOnlyExecution(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	client, err := river.NewClient(riversqlite.New(store.db), &river.Config{})
	require.NoError(t, err)
	var runnerCalls atomic.Int32
	service := New(Options{
		Store: store,
		ResolvePipelineRef: func(context.Context, string) (PipelineRef, bool) {
			return PipelineRef{EncodedID: "pipeline-id", Name: "analytics"}, true
		},
		PlanScheduledRun: testScheduledRunPlan,
		Runner: func(_ context.Context, req RunRequest, _ func(string)) RunResult {
			runnerCalls.Add(1)
			require.True(t, req.Scheduled)
			assert.Equal(t, map[string]any{"region": "eu"}, req.VariableOverrides)
			if unitErr := completeTestScheduledRunUnits(req); unitErr != nil {
				return RunResult{Status: "error", Error: unitErr.Error()}
			}
			return RunResult{Status: "ok"}
		},
	})
	start := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	args := scheduleSignalJobArgs{
		PipelineUUID: "pipeline-uuid", PipelineName: "analytics", Environment: "prod",
		Schedule: "@hourly", Timezone: "UTC",
		Start: start.Format(time.RFC3339Nano), End: start.Add(time.Hour).Format(time.RFC3339Nano),
		SnapshotVersionID: "snapshot-id", Variables: map[string]any{"region": "eu"},
	}
	signalWorker := &scheduleSignalWorker{service: service, client: client}
	require.NoError(t, signalWorker.Work(context.Background(), &river.Job[scheduleSignalJobArgs]{
		JobRow: &rivertype.JobRow{ID: 55}, Args: args,
	}))
	assert.Zero(t, runnerCalls.Load(), "the lightweight signal must not execute physical work")

	runs, err := store.List(context.Background(), RunFilter{PipelineID: "pipeline-id"})
	require.NoError(t, err)
	require.Len(t, runs.Runs, 1)
	run := runs.Runs[0]
	require.NotNil(t, run.RiverJobID)
	var kind, body string
	require.NoError(t, store.db.QueryRowContext(context.Background(), `
		SELECT kind, json(args) FROM river_job WHERE id = ?`, *run.RiverJobID).Scan(&kind, &body))
	assert.Equal(t, pipelineRunJobKind, kind)
	assert.JSONEq(t, `{"run_id":"`+run.ID+`"}`, body)
	assert.Equal(t, 1, countRows(t, store, `SELECT COUNT(*) FROM schedule_occurrence_attempts`))

	// A second signal while the occurrence is active is an idempotent no-op.
	require.NoError(t, signalWorker.Work(context.Background(), &river.Job[scheduleSignalJobArgs]{
		JobRow: &rivertype.JobRow{ID: 56}, Args: args,
	}))
	assert.Equal(t, 1, countRows(t, store, `SELECT COUNT(*) FROM pipeline_runs`))
	assert.Equal(t, 1, countRows(t, store, `SELECT COUNT(*) FROM river_job WHERE kind = ?`, pipelineRunJobKind))

	executionWorker := &pipelineRunWorker{service: service}
	require.NoError(t, executionWorker.Work(context.Background(), &river.Job[pipelineRunJobArgs]{
		JobRow: &rivertype.JobRow{ID: *run.RiverJobID}, Args: pipelineRunJobArgs{RunID: run.ID},
	}))
	assert.EqualValues(t, 1, runnerCalls.Load())
	finished, _, _, err := store.Get(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusSuccess, finished.Status)
	occurrence, err := newScheduleOccurrence("pipeline-uuid", "prod", start, start.Add(time.Hour))
	require.NoError(t, err)
	persisted, found, err := store.GetScheduleOccurrence(context.Background(), occurrence.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleOccurrenceSuccess, persisted.Status)
}

func TestV2ScheduleSignalRollsBackAttemptWhenExecutionJobInsertFails(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	client, err := river.NewClient(riversqlite.New(store.db), &river.Config{})
	require.NoError(t, err)
	service := New(Options{
		Store: store,
		ResolvePipelineRef: func(context.Context, string) (PipelineRef, bool) {
			return PipelineRef{EncodedID: "pipeline-id", Name: "analytics"}, true
		},
		PlanScheduledRun: testScheduledRunPlan,
	})
	require.NoError(t, func() error {
		_, triggerErr := store.db.ExecContext(ctx, `
			CREATE TRIGGER reject_scheduled_execution_job
			BEFORE INSERT ON river_job
			WHEN NEW.kind = 'renart-pipeline-run'
			BEGIN
				SELECT RAISE(ABORT, 'injected scheduled dispatch failure');
			END`)
		return triggerErr
	}())
	start := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	err = service.admitScheduledSignal(ctx, client, scheduleSignalJobArgs{
		PipelineUUID: "pipeline-uuid", Environment: "prod",
		Start: start.Format(time.RFC3339Nano), End: start.Add(time.Hour).Format(time.RFC3339Nano),
		SnapshotVersionID: "snapshot-id",
	}.admissionArgs())
	require.ErrorContains(t, err, "injected scheduled dispatch failure")

	occurrence, err := newScheduleOccurrence("pipeline-uuid", "prod", start, start.Add(time.Hour))
	require.NoError(t, err)
	persisted, found, err := store.GetScheduleOccurrence(ctx, occurrence.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleOccurrencePending, persisted.Status)
	assert.Zero(t, persisted.AttemptCount)
	assert.Equal(t, 0, countRows(t, store, `SELECT COUNT(*) FROM pipeline_runs`))
	assert.Equal(t, 0, countRows(t, store, `SELECT COUNT(*) FROM pipeline_run_specs`))
	assert.Equal(t, 0, countRows(t, store, `SELECT COUNT(*) FROM schedule_occurrence_attempts`))
	assert.Equal(t, 0, countRows(t, store, `SELECT COUNT(*) FROM river_job WHERE kind = ?`, pipelineRunJobKind))
}

func TestV2ScheduleSignalSnoozesBehindActivePipelineRun(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	client, err := river.NewClient(riversqlite.New(store.db), &river.Config{})
	require.NoError(t, err)
	manual := PipelineRun{
		ID: "manual-owner", PipelineID: "pipeline-id", PipelineUUID: "pipeline-uuid",
		Pipeline: "analytics", Environment: "dev", Trigger: RunTriggerManual, Status: RunStatusQueued,
	}
	_, err = store.CreateWithSpec(ctx, manual, manualRunSpec(manual, RunSourceWorkingTree, ""))
	require.NoError(t, err)
	service := New(Options{
		Store: store,
		ResolvePipelineRef: func(context.Context, string) (PipelineRef, bool) {
			return PipelineRef{EncodedID: "pipeline-id", Name: "analytics"}, true
		},
		PlanScheduledRun: testScheduledRunPlan,
	})
	start := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	args := scheduleSignalJobArgs{
		PipelineUUID: "pipeline-uuid", Environment: "prod",
		Start: start.Format(time.RFC3339Nano), End: start.Add(time.Hour).Format(time.RFC3339Nano),
		SnapshotVersionID: "snapshot-id",
	}
	err = (&scheduleSignalWorker{service: service, client: client}).Work(
		ctx,
		&river.Job[scheduleSignalJobArgs]{JobRow: &rivertype.JobRow{ID: 77}, Args: args},
	)
	var snooze *river.JobSnoozeError
	require.ErrorAs(t, err, &snooze)

	occurrence, err := newScheduleOccurrence("pipeline-uuid", "prod", start, start.Add(time.Hour))
	require.NoError(t, err)
	persisted, found, err := store.GetScheduleOccurrence(ctx, occurrence.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleOccurrencePending, persisted.Status)
	assert.Zero(t, persisted.AttemptCount)
	assert.Equal(t, 1, countRows(t, store, `SELECT COUNT(*) FROM pipeline_runs`))
	assert.Equal(t, 0, countRows(t, store, `SELECT COUNT(*) FROM river_job WHERE kind = ?`, pipelineRunJobKind))
}

func TestScheduledPrerequisiteWaitPersistsAcrossRestartFreezesDeploymentAndAdmitsWithoutSlot(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.db")
	store, err := OpenStore(statePath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	selectedVersion := "producer-deployment-v1"
	ready := false
	var observedFrozen []map[string]string
	planScheduled := func(_ context.Context, req ScheduledRunPlanRequest) (ScheduledRunPlanResult, error) {
		frozen := make(map[string]string, len(req.FrozenProducerDeployments))
		for key, value := range req.FrozenProducerDeployments {
			frozen[key] = value
		}
		observedFrozen = append(observedFrozen, frozen)
		version := selectedVersion
		if pinned := req.FrozenProducerDeployments["producer-uuid"]; pinned != "" {
			version = pinned
		}
		return scheduledPrerequisitePlan(t, req, ready, version), nil
	}
	newService := func() *Service {
		return New(Options{
			Store: store,
			ResolvePipelineRef: func(context.Context, string) (PipelineRef, bool) {
				return PipelineRef{EncodedID: "pipeline-id", Name: "analytics"}, true
			},
			PlanScheduledRun: planScheduled,
		})
	}
	service := newService()
	start := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	args := pipelineRunJobArgs{
		PipelineUUID: "pipeline-uuid", Environment: "prod", PipelineName: "analytics",
		Start: start.Format(time.RFC3339Nano), End: start.Add(time.Hour).Format(time.RFC3339Nano),
		SnapshotVersionID: "consumer-deployment",
	}

	_, shouldAdmit, err := service.prepareScheduledRunAdmission(ctx, args)
	var waiting *schedulePrerequisitesWaitingError
	require.ErrorAs(t, err, &waiting)
	assert.False(t, shouldAdmit)
	assert.WithinDuration(t, time.Now().UTC().Add(12*time.Hour), waiting.Deadline, 5*time.Second)
	assert.Equal(t, 0, countRows(t, store, `SELECT COUNT(*) FROM pipeline_runs`))
	assert.Equal(t, 0, countRows(t, store, `SELECT COUNT(*) FROM pipeline_run_slots`))

	occurrence, err := newScheduleOccurrence("pipeline-uuid", "prod", start, start.Add(time.Hour))
	require.NoError(t, err)
	persisted, found, err := store.GetScheduleOccurrence(ctx, occurrence.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleOccurrenceWaitingPrerequisites, persisted.Status)
	require.NotNil(t, persisted.PrerequisitePlan)
	require.NotNil(t, persisted.PrerequisiteDeadline)
	assert.Equal(t, "producer-deployment-v1", persisted.PrerequisitePlan.Prerequisites[0].ProducerSnapshotVersionID)
	firstDeadline := *persisted.PrerequisiteDeadline
	firstExecutionTime := persisted.PrerequisitePlan.ExecutionTime
	deferred, found, err := store.DeferredScheduleOccurrence(ctx, "pipeline-uuid", "prod")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleOccurrenceWaitingPrerequisites, deferred.Status)
	assert.Contains(t, deferred.PrerequisiteReason, "producer is not current")

	require.NoError(t, store.Close())
	store, err = OpenStore(statePath)
	require.NoError(t, err)
	service = newService()
	selectedVersion = "producer-deployment-v2"
	_, shouldAdmit, err = service.prepareScheduledRunAdmission(ctx, args)
	require.ErrorAs(t, err, &waiting)
	assert.False(t, shouldAdmit)
	require.Len(t, observedFrozen, 2)
	assert.Equal(t, "producer-deployment-v1", observedFrozen[1]["producer-uuid"])
	persisted, found, err = store.GetScheduleOccurrence(ctx, occurrence.Key)
	require.NoError(t, err)
	require.True(t, found)
	require.NotNil(t, persisted.PrerequisiteDeadline)
	assert.Equal(t, firstDeadline, *persisted.PrerequisiteDeadline)
	assert.Equal(t, firstExecutionTime, persisted.PrerequisitePlan.ExecutionTime)
	assert.Equal(t, "producer-deployment-v1", persisted.PrerequisitePlan.Prerequisites[0].ProducerSnapshotVersionID)

	ready = true
	prepared, shouldAdmit, err := service.prepareScheduledRunAdmission(ctx, args)
	require.NoError(t, err)
	require.True(t, shouldAdmit)
	require.Len(t, observedFrozen, 3)
	assert.Equal(t, "producer-deployment-v1", observedFrozen[2]["producer-uuid"])
	assert.Equal(t, "producer-deployment-v1", prepared.Plan.Prerequisites[0].ProducerSnapshotVersionID)
	_, err = store.CreateScheduleOccurrenceAttemptWithSpecAndPlan(
		ctx, prepared.Occurrence, prepared.Run, prepared.Spec, prepared.Plan,
	)
	require.NoError(t, err)
	active, found, err := store.GetScheduleOccurrence(ctx, occurrence.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleOccurrenceActive, active.Status)
	assert.Nil(t, active.PrerequisitePlan)
	assert.Nil(t, active.PrerequisiteDeadline)
}

func TestScheduledPrerequisiteWaitTimesOutWithoutCreatingRun(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	service := New(Options{
		Store: store,
		ResolvePipelineRef: func(context.Context, string) (PipelineRef, bool) {
			return PipelineRef{EncodedID: "pipeline-id", Name: "analytics"}, true
		},
		PlanScheduledRun: func(_ context.Context, req ScheduledRunPlanRequest) (ScheduledRunPlanResult, error) {
			return scheduledPrerequisitePlan(t, req, false, "producer-deployment-v1"), nil
		},
	})
	start := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	args := pipelineRunJobArgs{
		PipelineUUID: "pipeline-uuid", Environment: "prod",
		Start: start.Format(time.RFC3339Nano), End: start.Add(time.Hour).Format(time.RFC3339Nano),
		SnapshotVersionID: "consumer-deployment",
	}
	_, _, err = service.prepareScheduledRunAdmission(ctx, args)
	var waiting *schedulePrerequisitesWaitingError
	require.ErrorAs(t, err, &waiting)
	require.NoError(t, store.db.QueryRowContext(ctx, `
		UPDATE schedule_occurrences
		SET prerequisite_deadline = ?
		RETURNING occurrence_key`, formatTime(time.Now().UTC().Add(-time.Minute))).Scan(new(string)))

	_, _, err = service.prepareScheduledRunAdmission(ctx, args)
	var invalid *invalidScheduleSignalError
	require.ErrorAs(t, err, &invalid)
	assert.Contains(t, err.Error(), "Timed out")
	occurrence, occurrenceErr := newScheduleOccurrence("pipeline-uuid", "prod", start, start.Add(time.Hour))
	require.NoError(t, occurrenceErr)
	persisted, found, occurrenceErr := store.GetScheduleOccurrence(ctx, occurrence.Key)
	require.NoError(t, occurrenceErr)
	require.True(t, found)
	assert.Equal(t, ScheduleOccurrenceFailed, persisted.Status)
	assert.Contains(t, persisted.PrerequisiteReason, "Timed out")
	assert.Equal(t, 0, countRows(t, store, `SELECT COUNT(*) FROM pipeline_runs`))
}

func TestProducerEventWakeMakesWaitingScheduleSignalAvailable(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	start := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	args := scheduleSignalJobArgs{
		PipelineUUID: "pipeline-uuid", Environment: "prod",
		Start: start.Format(time.RFC3339Nano), End: start.Add(time.Hour).Format(time.RFC3339Nano),
		SnapshotVersionID: "consumer-deployment",
	}
	jobID := insertTestRiverJob(t, store, args)
	_, err = store.db.ExecContext(ctx, `
		UPDATE river_job SET state = ?, scheduled_at = ? WHERE id = ?`,
		string(rivertype.JobStateScheduled), formatRiverTime(time.Now().UTC().Add(time.Hour)), jobID,
	)
	require.NoError(t, err)
	occurrence, err := newScheduleOccurrence("pipeline-uuid", "prod", start, start.Add(time.Hour))
	require.NoError(t, err)
	occurrence, _, err = store.EnsureScheduleOccurrence(ctx, occurrence)
	require.NoError(t, err)
	request := ScheduledRunPlanRequest{
		PipelineID: "pipeline-id", PipelineUUID: occurrence.PipelineUUID,
		Environment: occurrence.Environment, Start: start, End: start.Add(time.Hour),
		ExecutionTime: start.Add(time.Minute),
	}
	planned := scheduledPrerequisitePlan(t, request, false, "producer-deployment-v1")
	_, err = store.MarkScheduleOccurrenceWaiting(
		ctx, occurrence, planned.Plan, time.Now().UTC().Add(12*time.Hour), "producer is not current",
	)
	require.NoError(t, err)
	otherStart := start.Add(time.Hour)
	otherArgs := args
	otherArgs.Start = otherStart.Format(time.RFC3339Nano)
	otherArgs.End = otherStart.Add(time.Hour).Format(time.RFC3339Nano)
	otherJobID := insertTestRiverJob(t, store, otherArgs)
	_, err = store.db.ExecContext(ctx, `
		UPDATE river_job SET state = ?, scheduled_at = ? WHERE id = ?`,
		string(rivertype.JobStateScheduled), formatRiverTime(time.Now().UTC().Add(time.Hour)), otherJobID,
	)
	require.NoError(t, err)
	otherOccurrence, err := newScheduleOccurrence("pipeline-uuid", "prod", otherStart, otherStart.Add(time.Hour))
	require.NoError(t, err)
	otherOccurrence, _, err = store.EnsureScheduleOccurrence(ctx, otherOccurrence)
	require.NoError(t, err)
	otherRequest := request
	otherRequest.Start = otherStart
	otherRequest.End = otherStart.Add(time.Hour)
	otherPlan := scheduledPrerequisitePlan(t, otherRequest, false, "other-deployment-v1")
	otherPlan.Plan.Prerequisites[0].ProducerPipelineUUID = "other-producer"
	otherPlan.Plan.Artifact = pipelineRunPlanArtifact(t, otherPlan.Plan)
	require.NoError(t, otherPlan.Plan.validate())
	_, err = store.MarkScheduleOccurrenceWaiting(
		ctx, otherOccurrence, otherPlan.Plan, time.Now().UTC().Add(12*time.Hour), "other producer is not current",
	)
	require.NoError(t, err)

	woken, err := store.WakeWaitingPrerequisiteSignals(ctx, "unrelated-producer")
	require.NoError(t, err)
	assert.Zero(t, woken)
	assertRiverJobState(t, store, jobID, rivertype.JobStateScheduled)

	woken, err = store.WakeWaitingPrerequisiteSignals(ctx, "producer-uuid")
	require.NoError(t, err)
	assert.EqualValues(t, 1, woken)
	assertRiverJobState(t, store, jobID, rivertype.JobStateAvailable)
	assertRiverJobState(t, store, otherJobID, rivertype.JobStateScheduled)
}

func TestV2ScheduleSignalRecoveryRequeuesSignalAndRetriesFailedAttempt(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	client, err := river.NewClient(riversqlite.New(store.db), &river.Config{})
	require.NoError(t, err)
	service := New(Options{
		Store: store,
		ResolvePipelineRef: func(context.Context, string) (PipelineRef, bool) {
			return PipelineRef{EncodedID: "pipeline-id", Name: "analytics"}, true
		},
		PlanScheduledRun: testScheduledRunPlan,
	})
	start := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	args := scheduleSignalJobArgs{
		PipelineUUID: "pipeline-uuid", Environment: "prod",
		Start: start.Format(time.RFC3339Nano), End: start.Add(time.Hour).Format(time.RFC3339Nano),
		SnapshotVersionID: "snapshot-id",
	}
	signalJobID := insertTestRiverJob(t, store, args)
	markTestRiverJobRunning(t, store, signalJobID)
	require.NoError(t, service.admitScheduledSignal(ctx, client, args.admissionArgs()))

	firstRuns, err := store.List(ctx, RunFilter{PipelineID: "pipeline-id"})
	require.NoError(t, err)
	require.Len(t, firstRuns.Runs, 1)
	firstRun := firstRuns.Runs[0]
	require.NotNil(t, firstRun.RiverJobID)
	markTestRiverJobRunning(t, store, *firstRun.RiverJobID)

	recovery, err := store.ReconcileInterruptedState(ctx, orphanedRunError)
	require.NoError(t, err)
	assert.Equal(t, []string{firstRun.ID}, recovery.RunIDs)
	assert.EqualValues(t, 1, recovery.RiverJobsRequeued)
	assert.EqualValues(t, 1, recovery.RiverJobsCancelled)
	assertRiverJobState(t, store, signalJobID, rivertype.JobStateAvailable)
	assertRiverJobState(t, store, *firstRun.RiverJobID, rivertype.JobStateCancelled)
	failed, _, _, err := store.Get(ctx, firstRun.ID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusFailed, failed.Status)

	occurrence, err := newScheduleOccurrence("pipeline-uuid", "prod", start, start.Add(time.Hour))
	require.NoError(t, err)
	failedOccurrence, found, err := store.GetScheduleOccurrence(ctx, occurrence.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleOccurrenceFailed, failedOccurrence.Status)
	assert.Equal(t, 1, failedOccurrence.AttemptCount)

	require.NoError(t, (&scheduleSignalWorker{service: service, client: client}).Work(
		ctx,
		&river.Job[scheduleSignalJobArgs]{JobRow: &rivertype.JobRow{ID: signalJobID}, Args: args},
	))
	retried, found, err := store.GetScheduleOccurrence(ctx, occurrence.Key)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ScheduleOccurrenceActive, retried.Status)
	assert.Equal(t, 2, retried.AttemptCount)
	assert.NotEqual(t, firstRun.ID, retried.CurrentRunID)
	assert.Equal(t, 2, countRows(t, store, `SELECT COUNT(*) FROM pipeline_runs`))
	assert.Equal(t, 2, countRows(t, store, `SELECT COUNT(*) FROM schedule_occurrence_attempts`))
}

func scheduledOccurrenceFixture(t testing.TB) (
	ScheduleOccurrence,
	PipelineRun,
	runSpecV1,
	PipelineRunPlan,
) {
	t.Helper()
	start := time.Date(2026, 7, 18, 8, 0, 0, 123456789, time.UTC)
	end := start.Add(time.Hour)
	executionTime := start.Add(30 * time.Minute)
	occurrence, err := newScheduleOccurrence("pipeline-uuid", "prod", start, end)
	require.NoError(t, err)
	planned, err := testScheduledRunPlan(context.Background(), ScheduledRunPlanRequest{
		PipelineID: "pipeline-id", PipelineUUID: occurrence.PipelineUUID,
		Environment: occurrence.Environment, SnapshotVersionID: "snapshot-id",
		Start: start, End: end, ExecutionTime: executionTime,
	})
	require.NoError(t, err)
	run := PipelineRun{
		PipelineID: "pipeline-id", PipelineUUID: occurrence.PipelineUUID, Pipeline: "analytics",
		Environment: occurrence.Environment, Trigger: RunTriggerSchedule, Status: RunStatusQueued,
		WinStart: &start, WinEnd: &end, ExecutionTime: &executionTime,
		SnapshotVersionID:           "snapshot-id",
		ExpectedSourceMerkle:        planned.Plan.SourceMerkle,
		ExpectedConfigurationDigest: planned.Plan.ConfigurationDigest,
	}
	spec := scheduledRunSpec(run, pipelineRunJobArgs{
		PipelineUUID: occurrence.PipelineUUID, Environment: occurrence.Environment,
		Schedule: "@hourly", Timezone: "UTC", OccurrenceKey: occurrence.Key,
	})
	spec.Expected = &runExpectedIdentity{
		SourceMerkle: planned.Plan.SourceMerkle, ConfigurationDigest: planned.Plan.ConfigurationDigest,
	}
	require.NoError(t, spec.validate())
	return occurrence, run, spec, planned.Plan
}

func scheduledPrerequisitePlan(
	t testing.TB,
	req ScheduledRunPlanRequest,
	ready bool,
	producerSnapshotVersionID string,
) ScheduledRunPlanResult {
	t.Helper()
	plan := validPipelineRunPlanV3(t)
	plan.PipelineID = req.PipelineID
	plan.PipelineUUID = req.PipelineUUID
	plan.ExecutionTime = req.ExecutionTime.UTC().Format(time.RFC3339Nano)
	for index := range plan.ExecutionUnits {
		plan.ExecutionUnits[index].AssetID = req.PipelineUUID + strings.TrimPrefix(plan.ExecutionUnits[index].AssetID, "pipeline-uuid")
		plan.ExecutionUnits[index].StartDate = req.Start.UTC().Format(time.RFC3339Nano)
		plan.ExecutionUnits[index].EndDate = req.End.UTC().Format(time.RFC3339Nano)
	}
	for index := range plan.ExecutionContracts {
		plan.ExecutionContracts[index].AssetID = req.PipelineUUID + strings.TrimPrefix(plan.ExecutionContracts[index].AssetID, "pipeline-uuid")
	}
	status := "blocked"
	reason := "producer is not current"
	planIDPart := "4"
	if ready {
		status = "ready"
		reason = "Renart observed complete producer coverage"
		planIDPart = "5"
	}
	plan.PlanID = strings.Repeat(planIDPart, 64)
	plan.Blocked = !ready
	if plan.Blocked {
		plan.Blockers = []string{"raw.orders: " + reason}
	} else {
		plan.Blockers = nil
	}
	plan.Prerequisites = []PipelineRunPrerequisite{{
		Status: status, Reason: reason,
		ConsumerAssetID: req.PipelineUUID + ":analytics.orders", ConsumerAssetName: "analytics.orders",
		URI:                "duckdb://warehouse/raw/orders",
		ProducerPipelineID: "raw", ProducerPipelineUUID: "producer-uuid", ProducerPipelineName: "raw",
		ProducerAssetID: "producer-uuid:raw.orders", ProducerAssetName: "raw.orders",
		ProducerSnapshotVersionID: producerSnapshotVersionID, ProducerDeploymentOrdinal: 1,
		Environment:   req.Environment,
		RequiredStart: req.Start.UTC().Format(time.RFC3339Nano), RequiredEnd: req.End.UTC().Format(time.RFC3339Nano),
		ExpectedFingerprint: "v3:producer", TargetIdentity: strings.Repeat("f", 64), VarsHash: strings.Repeat("e", 64),
		TargetGeneration: 1, WriterRunID: "producer-run", WriterSnapshotVersionID: producerSnapshotVersionID,
		WriterCompletionID: "producer-run", WriterMaterializedAt: req.Start.Add(time.Minute).UTC().Format(time.RFC3339Nano),
		RequiredSeconds: req.End.Sub(req.Start).Seconds(), CoveredSeconds: map[bool]float64{true: req.End.Sub(req.Start).Seconds()}[ready],
	}}
	plan.Artifact = pipelineRunPlanArtifact(t, plan)
	require.NoError(t, plan.validate())
	return ScheduledRunPlanResult{Plan: plan, WaitForPrerequisites: !ready}
}
