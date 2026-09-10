package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancelRunFinalizesQueuedJobImmediately(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	client, err := river.NewClient(riversqlite.New(store.db), &river.Config{})
	require.NoError(t, err)
	service := New(Options{
		Store:  store,
		Runner: func(context.Context, RunRequest, func(string)) RunResult { return RunResult{} },
	})
	service.mu.Lock()
	service.riverClient = client
	service.schedulerOn = true
	service.setOwnershipLocked(SchedulerOwnershipOwner, "")
	service.mu.Unlock()

	run := PipelineRun{
		PipelineID: "pipeline-id", PipelineUUID: "pipeline-uuid", Pipeline: "analytics",
		Trigger: RunTriggerManual, Status: RunStatusQueued,
	}
	runID, err := store.CreateWithSpec(ctx, run, manualRunSpec(run, RunSourceWorkingTree, ""))
	require.NoError(t, err)
	inserted, err := client.Insert(ctx, pipelineRunJobArgs{RunID: runID}, pipelineRunInsertOpts())
	require.NoError(t, err)
	require.NoError(t, store.SetRunRiverJob(ctx, runID, inserted.Job.ID))

	cancelled, err := service.CancelRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusCancelled, cancelled.Status)
	assert.Equal(t, userCancelledRunMessage, cancelled.Error)
	assert.NotNil(t, cancelled.FinishedAt)

	var riverState string
	require.NoError(t, store.db.QueryRowContext(ctx,
		`SELECT state FROM river_job WHERE id = ?`, inserted.Job.ID,
	).Scan(&riverState))
	assert.Equal(t, string(rivertype.JobStateCancelled), riverState)
	assert.Zero(t, countRows(t, store, `SELECT COUNT(*) FROM pipeline_run_slots WHERE run_id = ?`, runID))
}

func TestCancelRunRequestsRunningWorkerCancellation(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	store, err := OpenStore(filepath.Join(stateDir, "state.db"))
	require.NoError(t, err)
	defer store.Close()

	serviceCtx, stop := context.WithCancel(context.Background())
	defer stop()
	started := make(chan struct{})
	service := New(Options{
		Store:    store,
		StateDir: stateDir,
		Runner: func(ctx context.Context, _ RunRequest, _ func(string)) RunResult {
			close(started)
			<-ctx.Done()
			return RunResult{Status: "cancelled", Error: ctx.Err().Error()}
		},
	})
	require.NoError(t, service.Start(serviceCtx))
	defer service.Stop()

	run, err := service.Trigger(serviceCtx, PipelineSchedule{
		PipelineID: "pipeline-id", PipelineUUID: "pipeline-uuid", PipelineName: "analytics",
	}, TriggerRequest{Source: RunSourceWorkingTree})
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("pipeline runner did not start")
	}

	stopping, err := service.CancelRun(context.Background(), run.ID)
	require.NoError(t, err)
	assert.Equal(t, RunStatusRunning, stopping.Status)
	assert.False(t, stopping.Cancellable)
	assert.NotNil(t, stopping.CancellationRequestedAt)

	require.Eventually(t, func() bool {
		finished, logs, _, getErr := service.GetRun(context.Background(), run.ID)
		if getErr != nil || finished.Status != RunStatusCancelled {
			return false
		}
		return len(logs) == 1 && logs[0].Line == "Cancellation requested by user."
	}, 5*time.Second, 20*time.Millisecond)
}

func TestCancelRunRejectsTerminalAndUnownedInlineExecutions(t *testing.T) {
	t.Parallel()
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()

	service := New(Options{
		Store:  store,
		Runner: func(context.Context, RunRequest, func(string)) RunResult { return RunResult{} },
	})
	service.mu.Lock()
	service.schedulerOn = true
	service.ownershipState = SchedulerOwnershipOwner
	service.riverClient, err = river.NewClient(riversqlite.New(store.db), &river.Config{})
	service.mu.Unlock()
	require.NoError(t, err)

	terminalID, err := store.Create(context.Background(), PipelineRun{
		PipelineID: "finished", Pipeline: "finished", Trigger: RunTriggerManual, Status: RunStatusSuccess,
	})
	require.NoError(t, err)
	_, err = service.CancelRun(context.Background(), terminalID)
	require.ErrorIs(t, err, ErrRunCancellationUnavailable)

	inline := PipelineRun{
		PipelineID: "inline", Pipeline: "inline", Trigger: RunTriggerAPI, Status: RunStatusQueued,
	}
	inlineID, err := store.CreateWithSpec(
		context.Background(),
		inline,
		inlineRunSpec(inline, RunSourceWorkingTree, ""),
	)
	require.NoError(t, err)
	_, err = service.CancelRun(context.Background(), inlineID)
	require.ErrorIs(t, err, ErrRunCancellationUnavailable)
}

func TestCancelRunRequestsOwnedInlineCancellationWithoutReleasingItsSlot(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "state.db"))
	require.NoError(t, err)
	defer store.Close()
	service := New(Options{Store: store, Runner: func(context.Context, RunRequest, func(string)) RunResult { return RunResult{} }})
	service.mu.Lock()
	service.schedulerOn = true
	service.ownershipState = SchedulerOwnershipOwner
	service.riverClient, err = river.NewClient(riversqlite.New(store.db), &river.Config{})
	service.mu.Unlock()
	ctx := context.Background()
	row := PipelineRun{PipelineID: "inline", Pipeline: "inline", Trigger: RunTriggerAPI, Status: RunStatusQueued}
	id, err := store.CreateWithSpec(ctx, row, inlineRunSpec(row, RunSourceWorkingTree, ""))
	require.NoError(t, err)
	execution, cancel := context.WithCancel(ctx)
	defer cancel()
	release := service.RegisterInlineRunCancellation(id, cancel)
	defer release()
	require.NoError(t, service.StartInlineRun(ctx, id, time.Now().UTC()))
	before, _, _, err := service.GetRun(ctx, id)
	require.NoError(t, err)
	require.True(t, before.Cancellable)
	stopping, err := service.CancelRun(ctx, id)
	require.NoError(t, err)
	assert.ErrorIs(t, execution.Err(), context.Canceled)
	assert.Equal(t, RunStatusRunning, stopping.Status)
	assert.NotNil(t, stopping.CancellationRequestedAt)
	assert.False(t, stopping.Cancellable)
	again, err := service.CancelRun(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, stopping.CancellationRequestedAt, again.CancellationRequestedAt)
	assert.Positive(t, countRows(t, store, `SELECT COUNT(*) FROM pipeline_run_slots WHERE run_id = ?`, id))
	require.NoError(t, service.FinishInlineRun(execution, id, RunStatusCancelled, execution.Err()))
	release()
	done, _, _, err := service.GetRun(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, RunStatusCancelled, done.Status)
	assert.False(t, done.Cancellable)
	assert.Zero(t, countRows(t, store, `SELECT COUNT(*) FROM pipeline_run_slots WHERE run_id = ?`, id))
}
