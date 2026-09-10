package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/riverqueue/river/rivertype"
)

const userCancelledRunMessage = "run aborted by user"

var ErrRunCancellationUnavailable = errors.New("run cancellation is unavailable")

type inlineRunCancellation struct {
	cancel      context.CancelFunc
	requestedAt *time.Time
}

// RegisterInlineRunCancellation attaches the actual request-owned executor.
// Historical rows alone never grant an abort capability. Registration lasts
// until the executor's normal finalizer has released its durable resource slots.
func (s *Service) RegisterInlineRunCancellation(runID string, cancel context.CancelFunc) func() {
	entry := &inlineRunCancellation{cancel: cancel}
	s.mu.Lock()
	if s.inlineRunCancels == nil {
		s.inlineRunCancels = make(map[string]*inlineRunCancellation)
	}
	s.inlineRunCancels[runID] = entry
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.inlineRunCancels[runID] == entry {
			delete(s.inlineRunCancels, runID)
		}
	}
}

// RunCancellationUnavailableError explains why an active-looking historical
// row cannot be interrupted by this scheduler process.
type RunCancellationUnavailableError struct {
	Reason string
}

func (e *RunCancellationUnavailableError) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return ErrRunCancellationUnavailable.Error()
	}
	return fmt.Sprintf("%s: %s", ErrRunCancellationUnavailable, strings.TrimSpace(e.Reason))
}

func (e *RunCancellationUnavailableError) Unwrap() error {
	return ErrRunCancellationUnavailable
}

type runCancellationState struct {
	Cancellable bool
	RequestedAt *time.Time
}

// runCancellationState derives presentation state from River's durable job
// metadata. This avoids duplicating queue state in pipeline_runs while still
// preserving a cancellation request across a page refresh.
func (s *Store) runCancellationState(ctx context.Context, run PipelineRun) (runCancellationState, error) {
	if run.RiverJobID == nil || (run.Status != RunStatusQueued && run.Status != RunStatusRunning) {
		return runCancellationState{}, nil
	}
	var state string
	var requestedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT state, json_extract(metadata, '$.cancel_attempted_at')
		FROM river_job
		WHERE id = ?`, *run.RiverJobID).Scan(&state, &requestedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return runCancellationState{}, nil
	}
	if err != nil {
		return runCancellationState{}, err
	}
	result := runCancellationState{RequestedAt: parseNullTime(requestedAt)}
	switch rivertype.JobState(state) {
	case rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
		rivertype.JobStateScheduled, rivertype.JobStateRunning:
		result.Cancellable = result.RequestedAt == nil
	}
	return result, nil
}

func cancellationRequestedAt(jobMetadata []byte) *time.Time {
	var metadata struct {
		CancelAttemptedAt time.Time `json:"cancel_attempted_at"`
	}
	if len(jobMetadata) == 0 || json.Unmarshal(jobMetadata, &metadata) != nil || metadata.CancelAttemptedAt.IsZero() {
		return nil
	}
	requestedAt := metadata.CancelAttemptedAt.UTC()
	return &requestedAt
}

func (s *Service) hydrateRunCancellation(ctx context.Context, run *PipelineRun) error {
	if s == nil || s.store == nil || run == nil {
		return nil
	}
	if run.RiverJobID == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if entry := s.inlineRunCancels[run.ID]; entry != nil && (run.Status == RunStatusQueued || run.Status == RunStatusRunning) {
			run.Cancellable = entry.requestedAt == nil
			run.CancellationRequestedAt = entry.requestedAt
		}
		return nil
	}
	state, err := s.store.runCancellationState(ctx, *run)
	if err != nil {
		return err
	}
	run.Cancellable = state.Cancellable
	run.CancellationRequestedAt = state.RequestedAt
	return nil
}

// CancelRun aborts a queued River job immediately or requests cooperative
// cancellation from a running worker. Running executions remain visibly
// active until their executor returns and the normal finalizer closes the
// durable run, steps, units, schedule occurrence, and resource claims.
func (s *Service) CancelRun(ctx context.Context, runID string) (PipelineRun, error) {
	if s == nil || s.store == nil || s.runner == nil {
		return PipelineRun{}, errors.New("scheduler is not configured")
	}
	if err := s.RequireOwner(); err != nil {
		return PipelineRun{}, err
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return PipelineRun{}, errors.New("run id is required")
	}

	s.mu.Lock()
	client := s.riverClient
	s.mu.Unlock()

	run, _, _, err := s.store.Get(ctx, runID)
	if err != nil {
		return PipelineRun{}, err
	}
	if run.Status != RunStatusQueued && run.Status != RunStatusRunning {
		return PipelineRun{}, &RunCancellationUnavailableError{Reason: "the run has already finished"}
	}
	if run.RiverJobID == nil {
		return s.cancelInlineRun(ctx, run)
	}
	if client == nil {
		return PipelineRun{}, errors.New("scheduler is not running")
	}
	if err := s.hydrateRunCancellation(ctx, &run); err != nil {
		return PipelineRun{}, err
	}
	if run.CancellationRequestedAt != nil {
		return run, nil
	}
	if !run.Cancellable {
		return PipelineRun{}, &RunCancellationUnavailableError{
			Reason: "the background job is no longer interruptible",
		}
	}

	job, err := client.JobCancel(ctx, *run.RiverJobID)
	if err != nil {
		return PipelineRun{}, fmt.Errorf("cancel background job for run %s: %w", run.ID, err)
	}

	switch job.State {
	case rivertype.JobStateCancelled:
		finished := time.Now().UTC()
		finalizeCtx, cancel := detachedRunFinalizationContext(ctx)
		defer cancel()
		if err := s.store.FinalizeExecution(
			finalizeCtx,
			run.ID,
			RunStatusCancelled,
			finished,
			errors.New(userCancelledRunMessage),
			"",
			nil,
		); err != nil {
			// A worker may have won the race and finalized between our initial
			// read and River's cancellation update.
			reloaded, _, _, reloadErr := s.store.Get(finalizeCtx, run.ID)
			if reloadErr != nil || (reloaded.Status != RunStatusSuccess &&
				reloaded.Status != RunStatusFailed &&
				reloaded.Status != RunStatusCancelled) {
				return PipelineRun{}, err
			}
			run = reloaded
		} else {
			run, _, _, err = s.store.Get(finalizeCtx, run.ID)
			if err != nil {
				return PipelineRun{}, err
			}
		}
		s.publishRunEvent("run.finished", run)
		return run, nil

	case rivertype.JobStateRunning:
		s.mu.Lock()
		cancelExecution := s.activeRunCancels[run.ID]
		s.mu.Unlock()
		if cancelExecution != nil {
			cancelExecution()
		}
		run.Cancellable = false
		run.CancellationRequestedAt = cancellationRequestedAt(job.Metadata)
		if run.CancellationRequestedAt == nil {
			requestedAt := time.Now().UTC()
			run.CancellationRequestedAt = &requestedAt
		}
		entry := LogLine{At: *run.CancellationRequestedAt, Line: "Cancellation requested by user."}
		if appendErr := s.store.AppendLog(context.WithoutCancel(ctx), run.ID, entry); appendErr != nil {
			slog.Warn("failed to append run cancellation diagnostic", "run_id", run.ID, "error", appendErr)
		} else {
			s.publishRunEvent("run.log", map[string]any{"run_id": run.ID, "log": entry})
		}
		s.publishRunEvent("run.cancellation_requested", run)
		return run, nil

	default:
		return PipelineRun{}, &RunCancellationUnavailableError{
			Reason: fmt.Sprintf("the background job has already reached %s", job.State),
		}
	}
}

func (s *Service) cancelInlineRun(ctx context.Context, run PipelineRun) (PipelineRun, error) {
	s.mu.Lock()
	entry := s.inlineRunCancels[run.ID]
	if entry == nil {
		s.mu.Unlock()
		return PipelineRun{}, &RunCancellationUnavailableError{Reason: "this execution is not active in this Renart process"}
	}
	firstRequest := entry.requestedAt == nil
	if firstRequest {
		now := time.Now().UTC()
		entry.requestedAt = &now
	}
	run.Cancellable = false
	run.CancellationRequestedAt = entry.requestedAt
	entry.cancel()
	s.mu.Unlock()
	if firstRequest {
		line := LogLine{At: *run.CancellationRequestedAt, Line: "Cancellation requested by user."}
		if err := s.store.AppendLog(context.WithoutCancel(ctx), run.ID, line); err != nil {
			slog.Warn("failed to append inline cancellation diagnostic", "run_id", run.ID, "error", err)
		} else {
			s.publishRunEvent("run.log", map[string]any{"run_id": run.ID, "log": line})
		}
		s.publishRunEvent("run.cancellation_requested", run)
	}
	return run, nil
}
