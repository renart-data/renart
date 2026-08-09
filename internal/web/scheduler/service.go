package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riversqlite"
	"github.com/riverqueue/river/rivertype"
	"github.com/robfig/cron/v3"
	"renart/internal/web/runcontext"
	"renart/internal/web/secretstore"
)

const (
	pipelineRunQueue          = "renart_pipeline_runs"
	pipelineRunJobKind        = "renart-pipeline-run"
	scheduleSignalJobKind     = "renart-schedule-signal-v2"
	housekeepingJobKind       = "renart-housekeeping"
	runFinalizationTimeout    = 30 * time.Second
	indeterminateRetryMessage = "pipeline run was retried while still marked running after its worker returned; the previous physical outcome is indeterminate, so Renart did not repeat physical execution"
)

type PipelineSource func(context.Context) ([]PipelineSchedule, error)

type Service struct {
	store                     *Store
	runner                    func(context.Context, RunRequest, func(string)) RunResult
	pipelines                 PipelineSource
	publish                   func(any)
	stateDir                  string
	housekeeping              func(context.Context) error
	resolvePipelineRef        func(context.Context, string) (PipelineRef, bool)
	defaultEnvironment        func() string
	pipelineIntervalAware     func(context.Context, string) bool
	deployPipeline            func(context.Context, string) (string, error)
	latestSnapshot            func(context.Context, string) (string, bool, error)
	checkSnapshot             func(context.Context, string, string) error
	validateSnapshot          func(context.Context, string, string) error
	validateScheduleVariables func(context.Context, string, string, map[string]any) error
	resolveScheduleSecrets    func(context.Context, string, map[string]string) (map[string]any, error)
	declarations              *ScheduleDeclarationStore
	planScheduledRun          func(context.Context, ScheduledRunPlanRequest) (ScheduledRunPlanResult, error)
	validateReexecution       func(context.Context, RunReexecutionValidationRequest) error
	snapshotOrdinal           func(context.Context, string) (int64, error)
	recoverRun                func(context.Context, PipelineRun, []PipelineRunStep, []PipelineRunUnit) error
	lock                      *flock.Flock
	riverClient               *river.Client[*sql.Tx]
	activeRunCancels          map[string]context.CancelFunc
	mu                        sync.Mutex
	schedulerOn               bool
	ownershipState            SchedulerOwnershipState
	ownerMessage              string
}

type Options struct {
	Store     *Store
	Runner    func(context.Context, RunRequest, func(string)) RunResult
	Pipelines PipelineSource
	Publish   func(any)
	StateDir  string
	// Housekeeping runs daily as a River periodic job (materialization-log
	// retention pruning and similar maintenance).
	Housekeeping func(context.Context) error
	// ResolvePipelineRef maps a stable pipeline UUID to its current
	// workspace incarnation; ok=false means the pipeline file is gone
	// (deleted or on another branch).
	ResolvePipelineRef func(context.Context, string) (PipelineRef, bool)
	// DefaultEnvironment names the environment legacy single-env schedules
	// are migrated to.
	DefaultEnvironment func() string
	// PipelineIntervalAware reports whether the pipeline has interval-aware
	// assets; the backfill catch-up policy is only legal for those.
	PipelineIntervalAware func(context.Context, string) bool
	// DeployPipeline snapshots the working tree and returns the new
	// version ID (the "deploy now" path when enabling a schedule).
	DeployPipeline func(context.Context, string) (string, error)
	// LatestSnapshot resolves the current exact deployment for a stable
	// pipeline UUID. It is used only by the one-time pinless-row migration.
	LatestSnapshot func(context.Context, string) (versionID string, found bool, err error)
	// CheckSnapshot verifies that an exact version belongs to the pipeline and
	// remains executable. Reconciliation caches each version check for one pass.
	CheckSnapshot func(context.Context, string, string) error
	// ValidateSnapshot verifies that an exact version exists, belongs to the
	// pipeline, and is executable before a schedule can retain the pin.
	ValidateSnapshot func(context.Context, string, string) error
	// ValidateScheduleVariables checks overrides against the declarations in
	// the exact pinned snapshot without connecting to a destination.
	ValidateScheduleVariables func(context.Context, string, string, map[string]any) error
	// ResolveScheduleSecrets resolves stable declaration references just in
	// time. Resolved values are used for planning/execution but are never
	// persisted in declarations, River signals, or retained RunSpecs.
	ResolveScheduleSecrets func(context.Context, string, map[string]string) (map[string]any, error)
	// ScheduleDeclarations is the version-controlled desired-state store. Nil
	// preserves the legacy local-only behavior for isolated tests/embedders.
	ScheduleDeclarations *ScheduleDeclarationStore
	// PlanScheduledRun produces the redacted immutable plan for an actual due
	// interval. Admission persists it atomically with the RunSpec and run slot.
	PlanScheduledRun func(context.Context, ScheduledRunPlanRequest) (ScheduledRunPlanResult, error)
	// ValidateReexecution fails closed unless the retained source, variables,
	// selected configuration, and current policy still permit exact replay.
	ValidateReexecution func(context.Context, RunReexecutionValidationRequest) error
	// SnapshotOrdinal resolves presentation identity for an immutable version.
	// A missing historical deployment never blocks run or schedule history.
	SnapshotOrdinal func(context.Context, string) (int64, error)
	// RecoverRun observes a run reconciled after an unclean stop. The callback
	// receives the persisted terminal steps and execution units after open work
	// has been failed and unreached units have been skipped. It may rebuild
	// derived state, but must never execute the pipeline again.
	RecoverRun func(context.Context, PipelineRun, []PipelineRunStep, []PipelineRunUnit) error
}

// ResolveEnvScheduleExecutionContext returns the private variables and exact
// deployment pin for one producer schedule. It is used by cross-pipeline
// prerequisite planning only; resolved values never enter public schedule or
// run DTOs.
func (s *Service) ResolveEnvScheduleExecutionContext(
	ctx context.Context,
	pipelineUUID string,
	environment string,
) (EnvSchedule, map[string]any, bool, error) {
	if s == nil || s.store == nil {
		return EnvSchedule{}, nil, false, errors.New("scheduler store is unavailable")
	}
	schedule, found, err := s.store.GetEnvSchedule(ctx, strings.TrimSpace(pipelineUUID), strings.TrimSpace(environment))
	if err != nil || !found {
		return EnvSchedule{}, nil, found, err
	}
	variables, err := s.resolveScheduleVariables(
		secretstore.WithPurpose(ctx, secretstore.PurposeScheduledRun),
		schedule.Environment,
		schedule.Vars,
		schedule.SecretRefs,
	)
	if err != nil {
		return EnvSchedule{}, nil, true, err
	}
	return schedule, variables, true, nil
}

type pipelineRunJobArgs struct {
	RunID        string `json:"run_id,omitempty" river:"unique"`
	PipelineID   string `json:"pipeline_id,omitempty" river:"unique"`
	PipelineName string `json:"pipeline_name,omitempty"`
	// The remaining fields decode pre-v2 scheduled jobs that can survive an
	// upgrade in River. New execution jobs carry RunID only; new due intervals
	// use scheduleSignalJobArgs below.
	PipelineUUID       string            `json:"pipeline_uuid,omitempty" river:"unique"`
	Environment        string            `json:"environment,omitempty" river:"unique"`
	Trigger            RunTrigger        `json:"trigger,omitempty"`
	Schedule           string            `json:"schedule,omitempty"`
	Timezone           string            `json:"timezone,omitempty"`
	Start              string            `json:"start,omitempty" river:"unique"`
	End                string            `json:"end,omitempty" river:"unique"`
	SnapshotVersionID  string            `json:"snapshot_version_id,omitempty"`
	Variables          map[string]any    `json:"variables,omitempty"`
	VariableReferences map[string]string `json:"variable_references,omitempty"`
	// These fields remain only for strict decoding of pre-RunSpec River jobs.
	// New manual and scheduled execution jobs reconstruct behavior from RunID.
	Backfill             bool   `json:"backfill,omitempty"`
	ConfirmedEnvironment string `json:"confirmed_environment,omitempty"`
	FullRefresh          bool   `json:"full_refresh,omitempty"`
	SensorMode           string `json:"sensor_mode,omitempty"`
	// OccurrenceKey is derived inside the due-signal worker after the interval
	// is normalized. It is never accepted from or persisted as queue behavior.
	OccurrenceKey string `json:"-"`
}

func (pipelineRunJobArgs) Kind() string { return pipelineRunJobKind }

// scheduleSignalJobArgs contains the immutable schedule revision captured at
// firing plus its normalized interval. It is planning/admission input, never
// the physical execution contract; the resulting worker job carries only a
// durable run ID.
type scheduleSignalJobArgs struct {
	PipelineUUID       string            `json:"pipeline_uuid" river:"unique"`
	PipelineName       string            `json:"pipeline_name,omitempty"`
	Environment        string            `json:"environment" river:"unique"`
	Schedule           string            `json:"schedule,omitempty"`
	Timezone           string            `json:"timezone,omitempty"`
	Start              string            `json:"start" river:"unique"`
	End                string            `json:"end" river:"unique"`
	SnapshotVersionID  string            `json:"snapshot_version_id"`
	Variables          map[string]any    `json:"variables,omitempty"`
	VariableReferences map[string]string `json:"variable_references,omitempty"`
}

func (scheduleSignalJobArgs) Kind() string { return scheduleSignalJobKind }

func (args scheduleSignalJobArgs) admissionArgs() pipelineRunJobArgs {
	return pipelineRunJobArgs{
		PipelineUUID:       args.PipelineUUID,
		PipelineName:       args.PipelineName,
		Environment:        args.Environment,
		Trigger:            RunTriggerSchedule,
		Schedule:           args.Schedule,
		Timezone:           args.Timezone,
		Start:              args.Start,
		End:                args.End,
		SnapshotVersionID:  args.SnapshotVersionID,
		Variables:          args.Variables,
		VariableReferences: args.VariableReferences,
	}
}

type scheduleSignalWorker struct {
	river.WorkerDefaults[scheduleSignalJobArgs]
	service *Service
	client  *river.Client[*sql.Tx]
}

func (w *scheduleSignalWorker) Work(ctx context.Context, job *river.Job[scheduleSignalJobArgs]) error {
	var riverJobID int64
	if job.JobRow != nil {
		riverJobID = job.ID
	}
	if w.client == nil {
		err := errors.New("River client is unavailable for scheduled admission")
		warnPipelineRunJobSnooze("admit_schedule_occurrence", riverJobID, "", "", job.Args.PipelineUUID, err)
		return river.JobSnooze(runSpecRetrySnoozeTime)
	}
	err := w.service.admitScheduledSignal(ctx, w.client, job.Args.admissionArgs())
	if err == nil {
		return nil
	}
	var invalidSignal *invalidScheduleSignalError
	if errors.As(err, &invalidSignal) {
		return river.JobCancel(err)
	}
	if errors.Is(err, ErrPipelineRunActive) {
		return river.JobSnooze(runSpecRetrySnoozeTime)
	}
	warnPipelineRunJobSnooze("admit_schedule_occurrence", riverJobID, "", "", job.Args.PipelineUUID, err)
	return river.JobSnooze(runSpecRetrySnoozeTime)
}

type pipelineRunWorker struct {
	river.WorkerDefaults[pipelineRunJobArgs]
	service *Service
}

type invalidScheduleSignalError struct{ err error }

func (e *invalidScheduleSignalError) Error() string { return e.err.Error() }
func (e *invalidScheduleSignalError) Unwrap() error { return e.err }

type scheduledPlanBlockedError struct {
	RunID string
	err   error
}

func (e *scheduledPlanBlockedError) Error() string { return e.err.Error() }
func (e *scheduledPlanBlockedError) Unwrap() error { return e.err }

type runStartPersistenceError struct{ err error }

func (e *runStartPersistenceError) Error() string { return e.err.Error() }
func (e *runStartPersistenceError) Unwrap() error { return e.err }

func (w *pipelineRunWorker) Work(ctx context.Context, job *river.Job[pipelineRunJobArgs]) error {
	var riverJobID int64
	if job.JobRow != nil {
		riverJobID = job.ID
	}
	run, spec, ok, err := w.service.prepareRun(ctx, riverJobID, job.Args)
	if err != nil {
		if errors.Is(err, ErrPipelineRunActive) && strings.TrimSpace(job.Args.RunID) == "" {
			return river.JobSnooze(runSpecRetrySnoozeTime)
		}
		var invalidSpec *invalidRunSpecError
		if errors.As(err, &invalidSpec) {
			if finishErr := w.service.failRunBeforeExecution(ctx, invalidSpec.RunID, err); finishErr != nil {
				return errors.Join(err, finishErr)
			}
			return river.JobCancel(err)
		}
		var invalidPlan *invalidRunPlanError
		if errors.As(err, &invalidPlan) {
			if finishErr := w.service.failRunBeforeExecution(ctx, invalidPlan.RunID, err); finishErr != nil {
				return errors.Join(err, finishErr)
			}
			return river.JobCancel(err)
		}
		var invalidSignal *invalidScheduleSignalError
		if errors.As(err, &invalidSignal) {
			return river.JobCancel(err)
		}
		var blockedPlan *scheduledPlanBlockedError
		if errors.As(err, &blockedPlan) {
			if finishErr := w.service.failRunBeforeExecution(ctx, blockedPlan.RunID, err); finishErr != nil {
				return errors.Join(err, finishErr)
			}
			return river.JobCancel(err)
		}
		if strings.TrimSpace(job.Args.RunID) != "" || strings.TrimSpace(job.Args.PipelineUUID) != "" || job.Args.Trigger == RunTriggerSchedule {
			warnPipelineRunJobSnooze("prepare_run", riverJobID, job.Args.RunID, job.Args.PipelineID, job.Args.PipelineUUID, err)
			return river.JobSnooze(runSpecRetrySnoozeTime)
		}
		return err
	}
	if !ok {
		return nil
	}
	err = w.service.execute(ctx, run, spec)
	var startErr *runStartPersistenceError
	if errors.As(err, &startErr) {
		warnPipelineRunJobSnooze("persist_run_start", riverJobID, run.ID, run.PipelineID, run.PipelineUUID, err)
		return river.JobSnooze(runSpecRetrySnoozeTime)
	}
	return err
}

func warnPipelineRunJobSnooze(phase string, riverJobID int64, runID, pipelineID, pipelineUUID string, err error) {
	slog.Warn("pipeline run job hit a persistence error; retrying",
		"phase", phase,
		"river_job_id", riverJobID,
		"run_id", strings.TrimSpace(runID),
		"pipeline_id", strings.TrimSpace(pipelineID),
		"pipeline_uuid", strings.TrimSpace(pipelineUUID),
		"retry_after", runSpecRetrySnoozeTime,
		"error", err,
	)
}

func (s *Service) failRunBeforeExecution(ctx context.Context, runID string, runErr error) error {
	if err := s.store.Finish(ctx, runID, RunStatusFailed, runErr); err != nil {
		return fmt.Errorf("fail pipeline run %s before execution: %w", runID, err)
	}
	run, _, _, err := s.store.Get(ctx, runID)
	if err != nil {
		return fmt.Errorf("reload failed pipeline run %s: %w", runID, err)
	}
	s.publishRunEvent("run.finished", run)
	return nil
}

type housekeepingJobArgs struct{}

func (housekeepingJobArgs) Kind() string { return housekeepingJobKind }

type housekeepingWorker struct {
	river.WorkerDefaults[housekeepingJobArgs]
	service *Service
}

func (w *housekeepingWorker) Work(ctx context.Context, job *river.Job[housekeepingJobArgs]) error {
	if w.service.housekeeping == nil {
		return nil
	}
	return w.service.housekeeping(ctx)
}

type cronPeriodicSchedule struct {
	schedule cron.Schedule
}

func (s cronPeriodicSchedule) Next(current time.Time) time.Time {
	return s.schedule.Next(current)
}

func New(options Options) *Service {
	resolveScheduleSecrets := options.ResolveScheduleSecrets
	if resolveScheduleSecrets == nil {
		resolveScheduleSecrets = resolveEnvironmentScheduleSecrets
	}
	return &Service{
		store:                     options.Store,
		runner:                    options.Runner,
		pipelines:                 options.Pipelines,
		publish:                   options.Publish,
		stateDir:                  options.StateDir,
		housekeeping:              options.Housekeeping,
		resolvePipelineRef:        options.ResolvePipelineRef,
		defaultEnvironment:        options.DefaultEnvironment,
		pipelineIntervalAware:     options.PipelineIntervalAware,
		deployPipeline:            options.DeployPipeline,
		latestSnapshot:            options.LatestSnapshot,
		checkSnapshot:             options.CheckSnapshot,
		validateSnapshot:          options.ValidateSnapshot,
		validateScheduleVariables: options.ValidateScheduleVariables,
		resolveScheduleSecrets:    resolveScheduleSecrets,
		declarations:              options.ScheduleDeclarations,
		planScheduledRun:          options.PlanScheduledRun,
		validateReexecution:       options.ValidateReexecution,
		snapshotOrdinal:           options.SnapshotOrdinal,
		recoverRun:                options.RecoverRun,
		activeRunCancels:          make(map[string]context.CancelFunc),
		ownershipState:            SchedulerOwnershipUnavailable,
		ownerMessage:              "scheduler has not started",
	}
}

func (s *Service) Start(ctx context.Context) error {
	if s == nil {
		return nil
	}
	if s.store == nil || s.runner == nil {
		s.mu.Lock()
		s.setOwnershipLocked(SchedulerOwnershipUnavailable, "scheduler is not configured")
		s.mu.Unlock()
		return errors.New("scheduler is not configured")
	}
	s.mu.Lock()
	s.setOwnershipLocked(SchedulerOwnershipUnavailable, "scheduler is starting")
	s.mu.Unlock()
	if err := os.MkdirAll(s.stateDir, 0o755); err != nil {
		s.mu.Lock()
		s.setOwnershipLocked(SchedulerOwnershipUnavailable, err.Error())
		s.mu.Unlock()
		return err
	}
	s.lock = flock.New(filepath.Join(s.stateDir, "scheduler.lock"))
	locked, err := s.lock.TryLock()
	if err != nil {
		s.mu.Lock()
		s.setOwnershipLocked(SchedulerOwnershipUnavailable, err.Error())
		s.mu.Unlock()
		return err
	}
	if !locked {
		s.mu.Lock()
		s.setOwnershipLocked(SchedulerOwnershipFollower, "scheduler lock is held by another Renart process; schedules are read-only in this process")
		s.mu.Unlock()
		return nil
	}
	unlockOnError := true
	defer func() {
		if unlockOnError {
			_ = s.lock.Unlock()
		}
	}()

	workers := river.NewWorkers()
	river.AddWorker(workers, &pipelineRunWorker{service: s})
	signalWorker := &scheduleSignalWorker{service: s}
	river.AddWorker(workers, signalWorker)
	river.AddWorker(workers, &housekeepingWorker{service: s})
	client, err := river.NewClient(riversqlite.New(s.store.db), &river.Config{
		CompletedJobRetentionPeriod: 24 * time.Hour,
		DiscardedJobRetentionPeriod: 7 * 24 * time.Hour,
		FetchPollInterval:           time.Second,
		JobTimeout:                  -1,
		Logger:                      slog.New(slog.NewTextHandler(io.Discard, nil)),
		MaxAttempts:                 1,
		PollOnly:                    true,
		Queues:                      map[string]river.QueueConfig{pipelineRunQueue: {MaxWorkers: 4}},
		SoftStopTimeout:             5 * time.Second,
		Workers:                     workers,
	})
	if err != nil {
		s.mu.Lock()
		s.setOwnershipLocked(SchedulerOwnershipUnavailable, err.Error())
		s.mu.Unlock()
		return err
	}
	signalWorker.client = client
	recovery, err := s.recoverOrphanedRuns(ctx)
	if err != nil {
		s.mu.Lock()
		s.setOwnershipLocked(SchedulerOwnershipUnavailable, "scheduler startup recovery failed: "+err.Error())
		s.mu.Unlock()
		return fmt.Errorf("scheduler startup recovery failed: %w", err)
	}
	if recovery.ReconciledRuns > 0 || recovery.RiverJobsCancelled > 0 || recovery.RiverJobsRequeued > 0 || recovery.ReplayedRuns > 0 || recovery.SkippedRunReplays > 0 || recovery.ReplayFailures > 0 {
		slog.Info("scheduler startup recovery completed",
			"runs_reconciled", recovery.ReconciledRuns,
			"river_jobs_cancelled", recovery.RiverJobsCancelled,
			"river_jobs_requeued", recovery.RiverJobsRequeued,
			"runs_replayed", recovery.ReplayedRuns,
			"run_replays_skipped", recovery.SkippedRunReplays,
			"replay_failures", recovery.ReplayFailures,
		)
	}
	if err := client.Start(ctx); err != nil {
		s.mu.Lock()
		s.setOwnershipLocked(SchedulerOwnershipUnavailable, err.Error())
		s.mu.Unlock()
		return err
	}
	s.mu.Lock()
	s.riverClient = client
	s.schedulerOn = true
	s.setOwnershipLocked(SchedulerOwnershipOwner, "")
	s.mu.Unlock()
	if err := s.Reconcile(ctx); err != nil {
		s.mu.Lock()
		if s.riverClient == client {
			s.riverClient = nil
		}
		s.schedulerOn = false
		s.setOwnershipLocked(SchedulerOwnershipUnavailable, "scheduler reconciliation failed: "+err.Error())
		s.mu.Unlock()
		stopRiverClient(client)
		return err
	}
	unlockOnError = false
	go func() {
		<-ctx.Done()
		s.Stop()
	}()
	return nil
}

// orphanedRunError explains a run that was reconciled after an unclean stop.
const orphanedRunError = "interrupted: the server stopped while this run was executing"

type startupRecoverySummary struct {
	ReconciledRuns     int
	RiverJobsCancelled int64
	RiverJobsRequeued  int64
	ReplayedRuns       int
	SkippedRunReplays  int
	ReplayFailures     int
}

// recoverOrphanedRuns fails any run left "running" by a previous process that
// was killed mid-execution (otherwise it would stay running forever), and
// notifies listeners so open run views update.
func (s *Service) recoverOrphanedRuns(ctx context.Context) (startupRecoverySummary, error) {
	var summary startupRecoverySummary
	recovery, err := s.store.ReconcileInterruptedState(ctx, orphanedRunError)
	if err != nil {
		return summary, fmt.Errorf("reconcile interrupted runs: %w", err)
	}
	summary.ReconciledRuns = len(recovery.RunIDs)
	summary.RiverJobsCancelled = recovery.RiverJobsCancelled
	summary.RiverJobsRequeued = recovery.RiverJobsRequeued
	ids, err := s.store.PendingRunRecoveries(ctx)
	if err != nil {
		return summary, fmt.Errorf("list pending run recoveries: %w", err)
	}
	for _, id := range ids {
		run, _, steps, getErr := s.store.Get(ctx, id)
		if getErr != nil {
			return summary, fmt.Errorf("load reconciled run %s: %w", id, getErr)
		}
		finishedAt := time.Now().UTC()
		if run.FinishedAt != nil && !run.FinishedAt.IsZero() {
			finishedAt = run.FinishedAt.UTC()
		}
		var recoveredRunErr error
		if message := strings.TrimSpace(run.Error); message != "" {
			recoveredRunErr = errors.New(message)
		}
		if unitsErr := s.store.FinishOpenUnits(ctx, id, run.Status, finishedAt, recoveredRunErr); unitsErr != nil {
			return summary, fmt.Errorf("close reconciled run units %s: %w", id, unitsErr)
		}
		units, unitsErr := s.store.ListRunUnits(ctx, id)
		if unitsErr != nil {
			return summary, fmt.Errorf("load reconciled run units %s: %w", id, unitsErr)
		}
		if spec, found, specErr := s.store.GetRunSpec(ctx, id); specErr != nil {
			return summary, fmt.Errorf("load reconciled run spec %s: %w", id, specErr)
		} else if found {
			if bindingErr := validateRunSpecImmutableBinding(run, spec); bindingErr != nil {
				return summary, fmt.Errorf("validate reconciled run spec %s: %w", id, bindingErr)
			}
			run = applyRecoveredRunSpecIdentity(run, spec)
		}
		if !run.ExecutionContextResolved {
			// Current builds persist the effective context before invoking the
			// executor, so an unresolved row with terminal steps can only come from
			// a legacy build. Its River arguments are request intent, not proof of
			// what environment policy and default-window resolution actually used.
			// Acknowledge it without emitting RunCompleted so freshness fails safe.
			slog.Warn("skipping interrupted run derived-state replay because its effective execution context was not persisted", "run_id", id)
			if markErr := s.store.MarkRunRecoveryReplayed(ctx, id); markErr != nil {
				return summary, fmt.Errorf("acknowledge skipped run recovery %s: %w", id, markErr)
			}
			summary.SkippedRunReplays++
			s.publishRunEvent("run.finished", run)
			continue
		}
		if s.recoverRun != nil {
			if recoverErr := s.recoverRun(ctx, run, steps, units); recoverErr != nil {
				slog.Warn("failed to replay reconciled pipeline run", "run_id", id, "error", recoverErr)
				summary.ReplayFailures++
				s.publishRunEvent("run.finished", run)
				continue
			}
			if markErr := s.store.MarkRunRecoveryReplayed(ctx, id); markErr != nil {
				return summary, fmt.Errorf("acknowledge run recovery %s: %w", id, markErr)
			}
			summary.ReplayedRuns++
		}
		s.publishRunEvent("run.finished", run)
	}
	return summary, nil
}

func (s *Service) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	client := s.riverClient
	s.riverClient = nil
	s.schedulerOn = false
	s.setOwnershipLocked(SchedulerOwnershipUnavailable, "scheduler is stopped")
	s.mu.Unlock()
	if client != nil {
		stopRiverClient(client)
	}
	if s.lock != nil {
		_ = s.lock.Unlock()
	}
}

func stopRiverClient(client *river.Client[*sql.Tx]) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := client.Stop(ctx); err == nil {
		return
	} else {
		slog.Warn("River client did not stop gracefully; cancelling active jobs", "error", err)
	}
	hardCtx, hardCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer hardCancel()
	if err := client.StopAndCancel(hardCtx); err != nil {
		slog.Warn("River client did not stop after cancellation", "error", err)
	}
}

func (s *Service) clearRiverClient(client *river.Client[*sql.Tx]) {
	s.mu.Lock()
	if s.riverClient == client {
		s.riverClient = nil
	}
	s.mu.Unlock()
}

// Reconcile diffs the persisted (pipeline, environment) schedule rows
// against the workspace and the running River periodic jobs. Pipelines
// whose files are gone get archived tombstones (run history is untouched);
// tombstones whose pipeline reappears — same UUID, e.g. after a branch
// switch — are reactivated. Catch-up policies are applied for active rows.
func (s *Service) Reconcile(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	client := s.riverClient
	on := s.schedulerOn && s.ownershipState == SchedulerOwnershipOwner
	s.mu.Unlock()
	if !on || client == nil {
		return nil
	}

	if err := s.migrateLegacySchedules(ctx); err != nil {
		return err
	}
	if err := s.reconcileScheduleDeclarations(ctx); err != nil {
		return err
	}

	rows, err := s.store.ListEnvSchedules(ctx)
	if err != nil {
		return err
	}
	logRowError := func(row EnvSchedule, operation string, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		slog.Warn("schedule reconciliation skipped a row operation",
			"pipeline_uuid", row.PipelineUUID,
			"environment", row.Environment,
			"operation", operation,
			"error", err,
		)
		return nil
	}

	now := time.Now()
	jobs := make([]*river.PeriodicJob, 0, len(rows)+1)
	snapshotChecks := make(map[string]error)
	for _, row := range rows {
		row := row
		ref, exists := s.resolveRef(ctx, row.PipelineUUID)

		switch row.Status {
		case ScheduleStatusActive, ScheduleStatusPaused:
			if !exists {
				if err := s.store.SetEnvScheduleStatus(ctx, row.PipelineUUID, row.Environment, ScheduleStatusArchived, ArchivedReasonMissing); err != nil {
					if fatalErr := logRowError(row, "archive missing pipeline", err); fatalErr != nil {
						return fatalErr
					}
				}
				continue
			}
		case ScheduleStatusArchived:
			if exists && row.ArchivedReason == ArchivedReasonMissing {
				restoredStatus := ScheduleStatusActive
				if strings.TrimSpace(row.SnapshotVersionID) == "" {
					restoredStatus = ScheduleStatusPaused
				}
				if err := s.store.SetEnvScheduleStatus(ctx, row.PipelineUUID, row.Environment, restoredStatus, ""); err != nil {
					if fatalErr := logRowError(row, "restore pipeline", err); fatalErr != nil {
						return fatalErr
					}
					continue
				}
				row.Status = restoredStatus
			} else {
				continue
			}
		default:
			continue
		}
		if row.Status != ScheduleStatusActive {
			continue
		}
		snapshotCheckKey := row.PipelineUUID + "\x00" + strings.TrimSpace(row.SnapshotVersionID)
		snapshotErr, checked := snapshotChecks[snapshotCheckKey]
		if !checked {
			snapshotErr = s.checkScheduleSnapshot(ctx, row.PipelineUUID, row.SnapshotVersionID)
			snapshotChecks[snapshotCheckKey] = snapshotErr
		}
		if snapshotErr != nil {
			slog.Warn("pausing schedule with invalid deployment pin", "pipeline_uuid", row.PipelineUUID, "environment", row.Environment, "error", snapshotErr)
			if pauseErr := s.store.SetEnvScheduleStatus(ctx, row.PipelineUUID, row.Environment, ScheduleStatusPaused, ""); pauseErr != nil {
				if fatalErr := logRowError(row, "pause invalid deployment", pauseErr); fatalErr != nil {
					return fatalErr
				}
			}
			continue
		}
		if variablesErr := s.validateEnvScheduleVariables(ctx, row); variablesErr != nil {
			slog.Warn("pausing schedule with invalid variable overrides", "pipeline_uuid", row.PipelineUUID, "environment", row.Environment, "error", variablesErr)
			if pauseErr := s.store.SetEnvScheduleStatus(ctx, row.PipelineUUID, row.Environment, ScheduleStatusPaused, ""); pauseErr != nil {
				if fatalErr := logRowError(row, "pause invalid variables", pauseErr); fatalErr != nil {
					return fatalErr
				}
			}
			continue
		}

		schedule, parseErr := parseSchedule(row.Cron, row.Timezone)
		if parseErr != nil {
			slog.Warn("pausing schedule with invalid persisted cron", "pipeline_uuid", row.PipelineUUID, "environment", row.Environment, "error", parseErr)
			if pauseErr := s.store.SetEnvScheduleStatus(ctx, row.PipelineUUID, row.Environment, ScheduleStatusPaused, ""); pauseErr != nil {
				if fatalErr := logRowError(row, "pause invalid cron", pauseErr); fatalErr != nil {
					return fatalErr
				}
			}
			continue
		}
		next := schedule.Next(now)
		if err := s.store.SetEnvScheduleNextRun(ctx, row.PipelineUUID, row.Environment, &next); err != nil {
			// next_run_at is derived presentation state. A failed write must not
			// prevent this row's future ticks or unrelated schedules from running.
			if fatalErr := logRowError(row, "persist next run", err); fatalErr != nil {
				return fatalErr
			}
		}
		if err := s.catchUp(ctx, client, row, ref, schedule); err != nil {
			// Catch-up and normal periodic ticks are independent. Keep the latter
			// registered and let a later reconciliation retry missed intervals.
			if fatalErr := logRowError(row, "enqueue catch-up", err); fatalErr != nil {
				return fatalErr
			}
		}

		jobs = append(jobs, river.NewPeriodicJob(cronPeriodicSchedule{schedule: schedule}, func() (river.JobArgs, *river.InsertOpts) {
			args := scheduleSignalJobArgs{
				PipelineUUID:       row.PipelineUUID,
				PipelineName:       ref.Name,
				Environment:        row.Environment,
				Schedule:           row.Cron,
				Timezone:           row.Timezone,
				SnapshotVersionID:  row.SnapshotVersionID,
				Variables:          row.Vars,
				VariableReferences: row.SecretRefs,
			}
			if start, end, ok := previousScheduleInterval(schedule, time.Now().UTC()); ok {
				args.Start = start.UTC().Format(time.RFC3339Nano)
				args.End = end.UTC().Format(time.RFC3339Nano)
			}
			return args, scheduleSignalInsertOpts()
		}, &river.PeriodicJobOpts{ID: "schedule:" + row.PipelineUUID + ":" + row.Environment}))
	}

	if s.housekeeping != nil {
		jobs = append(jobs, river.NewPeriodicJob(river.PeriodicInterval(24*time.Hour), func() (river.JobArgs, *river.InsertOpts) {
			return housekeepingJobArgs{}, &river.InsertOpts{MaxAttempts: 1, Queue: pipelineRunQueue}
		}, &river.PeriodicJobOpts{ID: "renart-housekeeping", RunOnStart: true}))
	}
	client.PeriodicJobs().Clear()
	_, err = client.PeriodicJobs().AddManySafely(jobs)
	return err
}

func (s *Service) resolveRef(ctx context.Context, pipelineUUID string) (PipelineRef, bool) {
	if s.resolvePipelineRef == nil {
		return PipelineRef{}, false
	}
	return s.resolvePipelineRef(ctx, pipelineUUID)
}

// migrateLegacySchedules converts the pre-Phase-5 single-environment
// schedule settings (pipeline.yml schedule + enabled flag) into explicit
// (pipeline, environment) rows, once. The implicit "default" path is gone
// afterwards: every schedule names its environment.
func (s *Service) migrateLegacySchedules(ctx context.Context) error {
	count, err := s.store.CountEnvSchedules(ctx)
	if err != nil || count > 0 {
		return err
	}
	if s.pipelines == nil {
		return nil
	}
	items, err := s.pipelines(ctx)
	if err != nil {
		return err
	}
	environment := "default"
	if s.defaultEnvironment != nil {
		if name := strings.TrimSpace(s.defaultEnvironment()); name != "" {
			environment = name
		}
	}
	for _, item := range items {
		enabled, ok, err := s.store.ScheduleEnabled(ctx, item.PipelineID)
		if err != nil {
			return err
		}
		// The schedule_enabled flag is an explicit *override*: when set it wins,
		// but its absence means the legacy default — a pipeline with a schedule
		// string is enabled (mirroring PipelineService.GetSchedule, where
		// Enabled = schedule != ""). Requiring an explicit row would skip every
		// config-defined schedule, since nothing writes that flag outside tests,
		// leaving the redesign Schedules page permanently empty.
		if ok && !enabled {
			continue
		}
		if strings.TrimSpace(item.Schedule) == "" || item.PipelineUUID == "" {
			continue
		}
		policy := CatchupSkip
		if item.Catchup {
			policy = CatchupRunOnce
		}
		status := ScheduleStatusPaused
		versionID := ""
		if s.latestSnapshot != nil {
			latest, found, latestErr := s.latestSnapshot(ctx, item.PipelineUUID)
			if latestErr != nil {
				slog.Warn("legacy schedule could not resolve a deployment and was paused", "pipeline_uuid", item.PipelineUUID, "error", latestErr)
			} else if found && strings.TrimSpace(latest) != "" {
				latest = strings.TrimSpace(latest)
				if validateErr := s.validateScheduleSnapshot(ctx, item.PipelineUUID, latest); validateErr != nil {
					slog.Warn("legacy schedule deployment is invalid and was paused", "pipeline_uuid", item.PipelineUUID, "error", validateErr)
				} else {
					versionID = latest
					status = ScheduleStatusActive
				}
			}
		}
		if err := s.store.UpsertEnvSchedule(ctx, EnvSchedule{
			PipelineUUID:      item.PipelineUUID,
			Environment:       environment,
			SnapshotVersionID: versionID,
			Cron:              strings.TrimSpace(item.Schedule),
			Timezone:          item.Timezone,
			CatchupPolicy:     policy,
			Status:            status,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validateScheduleSnapshot(ctx context.Context, pipelineUUID, versionID string) error {
	versionID = strings.TrimSpace(versionID)
	if versionID == "" {
		return errors.New("a deployed snapshot is required to schedule this pipeline")
	}
	if s.validateSnapshot == nil {
		return errors.New("snapshot validation is unavailable")
	}
	if err := s.validateSnapshot(ctx, strings.TrimSpace(pipelineUUID), versionID); err != nil {
		return fmt.Errorf("deployment %s is not executable for this pipeline: %w", versionID, err)
	}
	return nil
}

func (s *Service) validateScheduleVariableOverrides(
	ctx context.Context,
	pipelineUUID string,
	versionID string,
	overrides map[string]any,
) error {
	if len(overrides) == 0 {
		return nil
	}
	if s.validateScheduleVariables == nil {
		return errors.New("schedule variable validation is unavailable")
	}
	if err := s.validateScheduleVariables(
		ctx, strings.TrimSpace(pipelineUUID), strings.TrimSpace(versionID), overrides,
	); err != nil {
		return fmt.Errorf("schedule variables are invalid for the pinned deployment: %w", err)
	}
	return nil
}

func (s *Service) checkScheduleSnapshot(ctx context.Context, pipelineUUID, versionID string) error {
	versionID = strings.TrimSpace(versionID)
	if versionID == "" {
		return errors.New("a deployed snapshot is required to schedule this pipeline")
	}
	check := s.checkSnapshot
	if check == nil {
		check = s.validateSnapshot
	}
	if check == nil {
		return errors.New("snapshot validation is unavailable")
	}
	if err := check(ctx, strings.TrimSpace(pipelineUUID), versionID); err != nil {
		return fmt.Errorf("deployment %s is not executable for this pipeline: %w", versionID, err)
	}
	return nil
}

func watermarkKey(row EnvSchedule) string {
	return row.PipelineUUID + "|" + row.Environment
}

// catchUp applies the schedule's catch-up policy for intervals missed while
// the process was down (laptop closed overnight).
func (s *Service) catchUp(ctx context.Context, client *river.Client[*sql.Tx], row EnvSchedule, ref PipelineRef, schedule cron.Schedule) error {
	key := watermarkKey(row)
	now := time.Now().UTC()
	_, prevEnd, found := previousScheduleInterval(schedule, now)
	if !found {
		return nil
	}
	last, ok, err := s.store.LastInterval(ctx, key)
	if err != nil {
		return err
	}
	if !ok {
		// First reconcile for this schedule: start tracking from here, no
		// retroactive catch-up.
		return s.store.SetInterval(ctx, key, prevEnd)
	}
	if !last.Before(prevEnd) {
		return nil
	}

	insertCatchupJob := func(start, end time.Time) error {
		args := scheduleSignalJobArgs{
			PipelineUUID:       row.PipelineUUID,
			PipelineName:       ref.Name,
			Environment:        row.Environment,
			Schedule:           row.Cron,
			Timezone:           row.Timezone,
			Start:              start.UTC().Format(time.RFC3339Nano),
			End:                end.UTC().Format(time.RFC3339Nano),
			SnapshotVersionID:  row.SnapshotVersionID,
			Variables:          row.Vars,
			VariableReferences: row.SecretRefs,
		}
		_, err := client.Insert(ctx, args, scheduleSignalInsertOpts())
		return err
	}

	switch row.CatchupPolicy {
	case CatchupRunOnce:
		return insertCatchupJob(last, prevEnd)
	case CatchupBackfill:
		cursor := last
		const maxBackfillRuns = 100
		for count := 0; count < maxBackfillRuns; count++ {
			next := schedule.Next(cursor)
			if !next.After(cursor) || next.After(prevEnd) {
				break
			}
			if err := insertCatchupJob(cursor, next); err != nil {
				return err
			}
			cursor = next
		}
	default: // CatchupSkip
		return s.store.SetInterval(ctx, key, prevEnd)
	}
	return nil
}

func (s *Service) ListSchedules(ctx context.Context) ([]PipelineSchedule, error) {
	if s.pipelines == nil {
		return nil, nil
	}
	items, err := s.pipelines(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if err := s.applyScheduleSettings(ctx, &items[i]); err != nil {
			return nil, err
		}
		if !items[i].Enabled || strings.TrimSpace(items[i].Schedule) == "" {
			continue
		}
		schedule, err := parseSchedule(items[i].Schedule, items[i].Timezone)
		if err == nil {
			next := schedule.Next(time.Now())
			items[i].NextRunAt = &next
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].PipelineName < items[j].PipelineName })
	return items, nil
}

// applyScheduleSettings derives the legacy single-flag Enabled view from
// the per-environment schedule rows: enabled means any active row exists
// for the pipeline. Falls back to the pre-migration settings table while
// no rows exist yet.
func (s *Service) applyScheduleSettings(ctx context.Context, item *PipelineSchedule) error {
	if s.store == nil || item == nil {
		return nil
	}
	if item.PipelineUUID != "" {
		rows, err := s.store.ListEnvSchedules(ctx)
		if err != nil {
			return err
		}
		seen := false
		enabled := false
		for _, row := range rows {
			if row.PipelineUUID != item.PipelineUUID {
				continue
			}
			seen = true
			if row.Status == ScheduleStatusActive {
				enabled = true
				break
			}
		}
		if seen {
			item.Enabled = enabled && strings.TrimSpace(item.Schedule) != ""
			return nil
		}
	}
	enabled, ok, err := s.store.ScheduleEnabled(ctx, item.PipelineID)
	if err != nil {
		return err
	}
	if ok {
		item.Enabled = enabled && strings.TrimSpace(item.Schedule) != ""
	}
	return nil
}

// ListAllEnvSchedules returns the live (active/paused) rows and the
// archived tombstones, with presentation fields resolved.
func (s *Service) ListAllEnvSchedules(ctx context.Context) (live []EnvSchedule, archived []EnvSchedule, err error) {
	rows, err := s.store.ListEnvSchedules(ctx)
	if err != nil {
		return nil, nil, err
	}
	live = make([]EnvSchedule, 0, len(rows))
	archived = make([]EnvSchedule, 0)
	for _, row := range rows {
		s.hydrateSnapshotOrdinal(ctx, &row.SnapshotOrdinal, row.SnapshotVersionID)
		if ref, ok := s.resolveRef(ctx, row.PipelineUUID); ok {
			row.PipelineID = ref.EncodedID
			row.PipelineName = ref.Name
		}
		if row.Status == ScheduleStatusActive {
			if schedule, parseErr := parseSchedule(row.Cron, row.Timezone); parseErr == nil {
				next := schedule.Next(time.Now())
				row.NextRunAt = &next
			}
		}
		if row.PipelineID != "" {
			if list, listErr := s.store.List(ctx, RunFilter{PipelineID: row.PipelineID, Environment: row.Environment, Limit: 1}); listErr == nil && len(list.Runs) > 0 {
				lastRun := list.Runs[0]
				s.hydrateSnapshotOrdinal(ctx, &lastRun.SnapshotOrdinal, lastRun.SnapshotVersionID)
				row.LastRun = &lastRun
			}
		}
		deferred, foundDeferred, deferredErr := s.store.DeferredScheduleOccurrence(
			ctx, row.PipelineUUID, row.Environment,
		)
		if deferredErr != nil {
			return nil, nil, deferredErr
		}
		if foundDeferred {
			row.DeferredOccurrence = &deferred
		}
		if row.Status == ScheduleStatusArchived {
			archived = append(archived, row)
		} else {
			live = append(live, row)
		}
	}
	sort.Slice(live, func(i, j int) bool {
		if live[i].PipelineName != live[j].PipelineName {
			return live[i].PipelineName < live[j].PipelineName
		}
		return live[i].Environment < live[j].Environment
	})
	return live, archived, nil
}

// UpsertEnvSchedule creates or updates the schedule for one (pipeline,
// environment). Enabling requires a deployed snapshot: pass an explicit
// version, set DeployNow, or rely on an already-pinned version.
func (s *Service) UpsertEnvSchedule(ctx context.Context, pipelineUUID string, req UpsertEnvScheduleRequest) (EnvSchedule, error) {
	snapshotVersionID := strings.TrimSpace(req.SnapshotVersionID)
	sourceSelections := 0
	if req.DeployNow {
		sourceSelections++
	}
	if snapshotVersionID != "" {
		sourceSelections++
	}
	if req.PreserveSnapshot {
		sourceSelections++
	}
	if sourceSelections > 1 {
		return EnvSchedule{}, errors.New("deploy_now, snapshot_version_id, and preserve_snapshot are mutually exclusive")
	}
	environment := strings.TrimSpace(req.Environment)
	if environment == "" {
		return EnvSchedule{}, errors.New("environment is required: per-environment schedules have no implicit default")
	}
	cronExpr := strings.TrimSpace(req.Cron)
	if cronExpr == "" {
		return EnvSchedule{}, errors.New("cron expression is required")
	}
	timezone := strings.TrimSpace(req.Timezone)
	if timezone == "" {
		timezone = "UTC"
	}
	if _, err := parseSchedule(cronExpr, timezone); err != nil {
		return EnvSchedule{}, fmt.Errorf("invalid cron expression: %w", err)
	}
	policy := req.CatchupPolicy
	if policy == "" {
		policy = CatchupSkip
	}
	switch policy {
	case CatchupSkip, CatchupRunOnce, CatchupBackfill:
	default:
		return EnvSchedule{}, fmt.Errorf("invalid catchup policy %q", policy)
	}
	if policy == CatchupBackfill && s.pipelineIntervalAware != nil && !s.pipelineIntervalAware(ctx, pipelineUUID) {
		return EnvSchedule{}, errors.New("backfill catch-up requires interval-aware assets (incremental materialization)")
	}
	if err := s.RequireOwner(); err != nil {
		return EnvSchedule{}, err
	}

	existing, found, err := s.store.GetEnvSchedule(ctx, pipelineUUID, environment)
	if err != nil {
		return EnvSchedule{}, err
	}
	if req.PreserveSnapshot && !found {
		return EnvSchedule{}, errors.New("preserve_snapshot requires an existing schedule")
	}
	if req.PreserveVariables && !found {
		return EnvSchedule{}, errors.New("preserve_variables requires an existing schedule")
	}
	if req.PreserveVariables && (req.Vars != nil || req.SecretRefs != nil) {
		return EnvSchedule{}, errors.New("preserve_variables cannot be combined with vars or secret_refs")
	}

	variables := cloneScheduleVariables(req.Vars)
	secretRefs := cloneScheduleSecretRefs(req.SecretRefs)
	if req.PreserveVariables {
		variables = cloneScheduleVariables(existing.Vars)
		secretRefs = cloneScheduleSecretRefs(existing.SecretRefs)
	}
	declaration := normalizeScheduleDeclaration(ScheduleDeclaration{
		Cron:          cronExpr,
		Timezone:      timezone,
		CatchupPolicy: policy,
		Paused:        req.Paused,
		Variables:     variables,
		SecretRefs:    secretRefs,
	})
	if err := validateScheduleDeclaration(declaration); err != nil {
		return EnvSchedule{}, err
	}
	if len(secretRefs) > 0 && s.declarations == nil {
		return EnvSchedule{}, errors.New("secret references require version-controlled schedule declarations")
	}

	if req.PreserveSnapshot {
		snapshotVersionID = existing.SnapshotVersionID
	} else if snapshotVersionID == "" && found && !req.DeployNow {
		// Backward compatibility for existing API clients. New editing clients
		// should use preserve_snapshot so their intent is explicit.
		snapshotVersionID = existing.SnapshotVersionID
	}
	if req.DeployNow {
		if s.deployPipeline == nil {
			return EnvSchedule{}, errors.New("deploy is not available")
		}
		deployed, deployErr := s.deployPipeline(ctx, pipelineUUID)
		if deployErr != nil {
			return EnvSchedule{}, fmt.Errorf("deploy failed: %w", deployErr)
		}
		snapshotVersionID = deployed
	}
	if snapshotVersionID == "" && (!found || !req.PreserveSnapshot || !req.Paused) {
		return EnvSchedule{}, errors.New("a deployed snapshot is required to schedule this pipeline; deploy it first or pass deploy_now")
	}
	if snapshotVersionID != "" {
		if err := s.validateScheduleSnapshot(ctx, pipelineUUID, snapshotVersionID); err != nil {
			return EnvSchedule{}, err
		}
		resolvedVariables, err := s.resolveScheduleVariables(
			secretstore.WithPurpose(ctx, secretstore.PurposeScheduleValidation),
			environment,
			variables,
			secretRefs,
		)
		if err != nil {
			return EnvSchedule{}, err
		}
		if err := s.validateScheduleVariableOverrides(ctx, pipelineUUID, snapshotVersionID, resolvedVariables); err != nil {
			return EnvSchedule{}, err
		}
	} else if !req.PreserveVariables && (len(variables) > 0 || len(secretRefs) > 0) {
		return EnvSchedule{}, errors.New("schedule variables require a deployed snapshot for validation")
	}

	status := ScheduleStatusActive
	if req.Paused {
		status = ScheduleStatusPaused
	}
	schedule := EnvSchedule{
		PipelineUUID:       pipelineUUID,
		Environment:        environment,
		SnapshotVersionID:  snapshotVersionID,
		Cron:               cronExpr,
		Timezone:           timezone,
		Vars:               variables,
		SecretRefs:         secretRefs,
		DeclarationManaged: s.declarations != nil,
		CatchupPolicy:      policy,
		Status:             status,
	}
	if found {
		schedule.CreatedAt = existing.CreatedAt
	}
	if s.declarations != nil {
		if err := s.declarations.Set(pipelineUUID, environment, declaration); err != nil {
			return EnvSchedule{}, fmt.Errorf("write schedule declaration: %w", err)
		}
	}
	if err := s.store.UpsertEnvSchedule(ctx, schedule); err != nil {
		return EnvSchedule{}, err
	}
	if err := s.Reconcile(ctx); err != nil {
		return EnvSchedule{}, err
	}
	updated, _, err := s.store.GetEnvSchedule(ctx, pipelineUUID, environment)
	if err != nil {
		return EnvSchedule{}, err
	}
	if ref, ok := s.resolveRef(ctx, pipelineUUID); ok {
		updated.PipelineID = ref.EncodedID
		updated.PipelineName = ref.Name
	}
	s.hydrateSnapshotOrdinal(ctx, &updated.SnapshotOrdinal, updated.SnapshotVersionID)
	return updated, nil
}

// PromoteEnvSchedules explicitly advances selected schedule pins after a
// deployment review. Expected pins make the batch all-or-nothing if another
// editor changed any selected row in the meantime.
func (s *Service) PromoteEnvSchedules(ctx context.Context, pipelineUUID string, req PromoteEnvSchedulesRequest) ([]EnvSchedule, error) {
	if err := s.RequireOwner(); err != nil {
		return nil, err
	}
	pipelineUUID = strings.TrimSpace(pipelineUUID)
	versionID := strings.TrimSpace(req.SnapshotVersionID)
	if pipelineUUID == "" || versionID == "" {
		return nil, errors.New("pipeline and snapshot_version_id are required")
	}
	if len(req.Schedules) == 0 {
		return nil, errors.New("select at least one schedule to update")
	}
	if len(req.Schedules) > 100 {
		return nil, errors.New("at most 100 schedules can be updated at once")
	}
	if err := s.validateScheduleSnapshot(ctx, pipelineUUID, versionID); err != nil {
		return nil, err
	}

	selections := make([]envSchedulePinExpectation, 0, len(req.Schedules))
	seen := make(map[string]struct{}, len(req.Schedules))
	for _, requested := range req.Schedules {
		environment := strings.TrimSpace(requested.Environment)
		if environment == "" || requested.ExpectedSnapshotVersionID == nil {
			return nil, errors.New("each selected schedule requires environment and expected_snapshot_version_id")
		}
		expectedVersion := strings.TrimSpace(*requested.ExpectedSnapshotVersionID)
		if _, duplicate := seen[environment]; duplicate {
			return nil, fmt.Errorf("schedule %s was selected more than once", environment)
		}
		seen[environment] = struct{}{}
		existing, found, err := s.store.GetEnvSchedule(ctx, pipelineUUID, environment)
		if err != nil {
			return nil, err
		}
		if !found || existing.Status == ScheduleStatusArchived {
			return nil, fmt.Errorf("schedule %s was not found", environment)
		}
		if existing.SnapshotVersionID != expectedVersion {
			return nil, fmt.Errorf("schedule %s changed after deployment review", environment)
		}
		candidate := existing
		candidate.SnapshotVersionID = versionID
		if err := s.validateEnvScheduleVariables(ctx, candidate); err != nil {
			return nil, fmt.Errorf("schedule %s cannot use the selected deployment: %w", environment, err)
		}
		if expectedVersion == versionID {
			continue
		}
		selections = append(selections, envSchedulePinExpectation{
			Environment: environment, ExpectedSnapshotVersionID: expectedVersion,
		})
	}
	if len(selections) > 0 {
		if err := s.store.PromoteEnvSchedulePins(ctx, pipelineUUID, versionID, selections); err != nil {
			return nil, err
		}
		if err := s.Reconcile(ctx); err != nil {
			return nil, err
		}
	}

	updated := make([]EnvSchedule, 0, len(req.Schedules))
	for _, requested := range req.Schedules {
		row, found, err := s.store.GetEnvSchedule(ctx, pipelineUUID, strings.TrimSpace(requested.Environment))
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("schedule %s disappeared after promotion", requested.Environment)
		}
		if ref, ok := s.resolveRef(ctx, pipelineUUID); ok {
			row.PipelineID = ref.EncodedID
			row.PipelineName = ref.Name
		}
		s.hydrateSnapshotOrdinal(ctx, &row.SnapshotOrdinal, row.SnapshotVersionID)
		updated = append(updated, row)
	}
	return updated, nil
}

// SetEnvScheduleLifecycle pauses or resumes one (pipeline, environment).
func (s *Service) SetEnvScheduleLifecycle(ctx context.Context, pipelineUUID, environment string, status ScheduleStatus) error {
	if status != ScheduleStatusActive && status != ScheduleStatusPaused {
		return fmt.Errorf("invalid schedule status %q", status)
	}
	if err := s.RequireOwner(); err != nil {
		return err
	}
	existing, found, err := s.store.GetEnvSchedule(ctx, pipelineUUID, environment)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("schedule not found")
	}
	if status == ScheduleStatusActive {
		if err := s.validateScheduleSnapshot(ctx, pipelineUUID, existing.SnapshotVersionID); err != nil {
			return err
		}
		if err := s.validateEnvScheduleVariables(ctx, existing); err != nil {
			return err
		}
	}
	if existing.DeclarationManaged {
		if s.declarations == nil {
			return errors.New("schedule declaration store is unavailable")
		}
		declaration, found, err := s.declarations.Get(pipelineUUID, environment)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("schedule declaration is missing")
		}
		declaration.Paused = status == ScheduleStatusPaused
		if err := s.declarations.Set(pipelineUUID, environment, declaration); err != nil {
			return fmt.Errorf("write schedule declaration: %w", err)
		}
	}
	if err := s.store.SetEnvScheduleStatus(ctx, pipelineUUID, environment, status, ""); err != nil {
		return err
	}
	return s.Reconcile(ctx)
}

// ArchiveEnvSchedule is the user-facing delete: the row becomes a tombstone
// and run history stays addressable. Declaration-managed rows restore only if
// the same stable key is later re-added to .renart/schedules.yml; local legacy
// rows require an explicit lifecycle restore.
func (s *Service) ArchiveEnvSchedule(ctx context.Context, pipelineUUID, environment string) error {
	if err := s.RequireOwner(); err != nil {
		return err
	}
	existing, found, err := s.store.GetEnvSchedule(ctx, pipelineUUID, environment)
	if err != nil {
		return err
	}
	if !found {
		return errors.New("schedule not found")
	}
	reason := ArchivedReasonUser
	if existing.DeclarationManaged {
		if s.declarations == nil {
			return errors.New("schedule declaration store is unavailable")
		}
		if err := s.declarations.Remove(pipelineUUID, environment); err != nil {
			return fmt.Errorf("remove schedule declaration: %w", err)
		}
		reason = ArchivedReasonDeclarationMissing
	}
	if err := s.store.SetEnvScheduleStatus(ctx, pipelineUUID, environment, ScheduleStatusArchived, reason); err != nil {
		return err
	}
	return s.Reconcile(ctx)
}

func (s *Service) Trigger(ctx context.Context, pipeline PipelineSchedule, req TriggerRequest) (PipelineRun, error) {
	if s.store == nil || s.runner == nil {
		return PipelineRun{}, errors.New("scheduler is not configured")
	}
	if err := s.RequireOwner(); err != nil {
		return PipelineRun{}, err
	}
	s.mu.Lock()
	client := s.riverClient
	s.mu.Unlock()
	if client == nil {
		return PipelineRun{}, errors.New("scheduler is not running")
	}
	source, snapshotVersionID, err := normalizeManualRunSource(req.Source, req.SnapshotVersionID)
	if err != nil {
		return PipelineRun{}, err
	}
	normalizedContext, err := runcontext.Normalize(runcontext.Input{
		Start:       req.Start,
		End:         req.End,
		FullRefresh: req.FullRefresh,
		Backfill:    req.Backfill,
		SensorMode:  req.SensorMode,
	})
	if err != nil {
		return PipelineRun{}, err
	}
	start := normalizedContext.Start
	end := normalizedContext.End
	sensorMode := normalizedContext.SensorMode
	var executionTime *time.Time
	if rawExecutionTime := strings.TrimSpace(req.ExecutionTime); rawExecutionTime != "" {
		parsedExecutionTime, parseErr := time.Parse(time.RFC3339Nano, rawExecutionTime)
		if parseErr != nil {
			return PipelineRun{}, fmt.Errorf("execution_time must be an RFC3339 timestamp: %w", parseErr)
		}
		parsedExecutionTime = parsedExecutionTime.UTC()
		executionTime = &parsedExecutionTime
	}
	run := PipelineRun{
		PipelineID:                  pipeline.PipelineID,
		PipelineUUID:                pipeline.PipelineUUID,
		Pipeline:                    pipeline.PipelineName,
		Environment:                 strings.TrimSpace(req.Environment),
		Trigger:                     RunTriggerManual,
		Status:                      RunStatusQueued,
		WinStart:                    start,
		WinEnd:                      end,
		SnapshotVersionID:           snapshotVersionID,
		FullRefresh:                 req.FullRefresh,
		Backfill:                    req.Backfill,
		SensorMode:                  sensorMode,
		VariableOverrides:           req.VariableOverrides,
		ExecutionTime:               executionTime,
		ExpectedSourceMerkle:        strings.TrimSpace(req.ExpectedSourceMerkle),
		ExpectedConfigurationDigest: strings.TrimSpace(req.ExpectedConfigurationDigest),
	}
	spec := manualRunSpec(run, source, req.ConfirmedEnvironment)
	spec.Requested.VariableReferences = cloneScheduleSecretRefs(req.VariableReferences)
	if err := spec.validate(); err != nil {
		return PipelineRun{}, err
	}
	if req.ConfirmedPlan != nil {
		if err := validateRunPlanAdmissionBinding(run, spec, *req.ConfirmedPlan); err != nil {
			return PipelineRun{}, err
		}
	}
	return s.admitQueuedRun(ctx, client, run, spec, req.ConfirmedPlan)
}

// AdmitInlineRun records a synchronous full-pipeline execution before any
// physical work starts. Inline dispatch shares the same private RunSpec and
// durable pipeline slot as queued work, but deliberately creates no River job.
func (s *Service) AdmitInlineRun(ctx context.Context, req InlineRunAdmission) (PipelineRun, error) {
	if s == nil || s.store == nil {
		return PipelineRun{}, errors.New("run ledger is not configured")
	}
	switch req.Origin {
	case RunTriggerManual, RunTriggerAPI, RunTriggerCLI:
	default:
		return PipelineRun{}, fmt.Errorf("invalid inline run origin %q", req.Origin)
	}
	if req.Start.IsZero() || req.End.IsZero() || !req.Start.Before(req.End) {
		return PipelineRun{}, errors.New("inline run requires a complete increasing execution window")
	}
	source, snapshotVersionID, err := normalizeManualRunSource(req.Source, req.SnapshotVersionID)
	if err != nil {
		return PipelineRun{}, err
	}
	executionTime := req.ExecutionTime.UTC()
	if executionTime.IsZero() {
		executionTime = time.Now().UTC()
	}
	start, end := req.Start.UTC(), req.End.UTC()
	run := PipelineRun{
		PipelineID:               strings.TrimSpace(req.PipelineID),
		PipelineUUID:             strings.TrimSpace(req.PipelineUUID),
		Pipeline:                 strings.TrimSpace(req.PipelineName),
		Environment:              strings.TrimSpace(req.Environment),
		Trigger:                  req.Origin,
		Status:                   RunStatusQueued,
		WinStart:                 &start,
		WinEnd:                   &end,
		SnapshotVersionID:        snapshotVersionID,
		FullRefresh:              req.FullRefresh,
		Backfill:                 req.Backfill,
		SensorMode:               strings.TrimSpace(req.SensorMode),
		VariableOverrides:        req.VariableOverrides,
		ExecutionTime:            &executionTime,
		ExecutionContextResolved: true,
	}
	spec := inlineRunSpec(run, source, req.ConfirmedEnvironment)
	if err := applyInlineRunSelection(&spec, req.Selection); err != nil {
		return PipelineRun{}, fmt.Errorf("normalize inline run selection: %w", err)
	}
	id, err := s.store.CreateWithSpec(ctx, run, spec)
	if err != nil {
		return PipelineRun{}, err
	}
	run.ID = id
	s.publishRunEvent("run.queued", run)
	return run, nil
}

// StartInlineRun transitions an admitted inline run immediately before its
// executor is called. A persistence failure prevents physical execution.
func (s *Service) StartInlineRun(ctx context.Context, runID string, started time.Time) error {
	if s == nil || s.store == nil {
		return errors.New("run ledger is not configured")
	}
	run, _, _, err := s.store.Get(ctx, strings.TrimSpace(runID))
	if err != nil {
		return err
	}
	spec, found, err := s.store.GetRunSpec(ctx, run.ID)
	if err != nil {
		return err
	}
	if !found || spec.Dispatch != runDispatchInlineStreaming {
		return errors.New("run is not an inline-streaming execution")
	}
	if run.Status != RunStatusQueued {
		return fmt.Errorf("inline run %s cannot start from status %s", run.ID, run.Status)
	}
	if err := validateRunSpecBinding(run, spec); err != nil {
		return fmt.Errorf("validate inline run spec: %w", err)
	}
	if err := s.store.validateActiveRunSpecSlotBinding(ctx, run, spec); err != nil {
		return fmt.Errorf("validate inline run slot: %w", err)
	}
	started = started.UTC()
	if started.IsZero() {
		started = time.Now().UTC()
	}
	if err := s.store.MarkRunning(ctx, run.ID, started); err != nil {
		return err
	}
	run.Status = RunStatusRunning
	run.StartedAt = &started
	s.publishRunEvent("run.started", run)
	return nil
}

func (s *Service) SetInlineRunExecutionTargetSnapshot(ctx context.Context, runID string, snapshot ExecutionTargetSnapshot) error {
	if s == nil || s.store == nil {
		return errors.New("run ledger is not configured")
	}
	return s.store.SetRunExecutionTargetSnapshot(ctx, strings.TrimSpace(runID), snapshot)
}

// BindInlineRunExecutionUnits persists the exact full-pipeline work selected
// after the working tree is parsed and before the first physical task starts.
func (s *Service) BindInlineRunExecutionUnits(
	ctx context.Context,
	runID string,
	units []RunSelectionUnit,
) error {
	if s == nil || s.store == nil {
		return errors.New("run ledger is not configured")
	}
	return s.store.BindInlineRunExecutionUnits(ctx, strings.TrimSpace(runID), units)
}

func (s *Service) RecordInlineRunStep(ctx context.Context, runID string, event RunStepEvent) error {
	if s == nil || s.store == nil {
		return errors.New("run ledger is not configured")
	}
	return s.persistRunStep(ctx, strings.TrimSpace(runID), event)
}

func (s *Service) RecordInlineRunUnit(ctx context.Context, runID string, event PipelineRunUnitEvent) error {
	if s == nil || s.store == nil {
		return errors.New("run ledger is not configured")
	}
	return s.persistRunUnit(ctx, strings.TrimSpace(runID), event)
}

func (s *Service) AppendInlineRunLog(ctx context.Context, runID, line string) error {
	if s == nil || s.store == nil {
		return errors.New("run ledger is not configured")
	}
	entry := LogLine{At: time.Now().UTC(), Line: line}
	if err := s.store.AppendLog(ctx, strings.TrimSpace(runID), entry); err != nil {
		return err
	}
	s.publishRunEvent("run.log", map[string]any{"run_id": strings.TrimSpace(runID), "log": entry})
	return nil
}

// FinishInlineRun durably releases the run slot even when the request context
// was cancelled. Inline runs never carry schedule-watermark capability.
func (s *Service) FinishInlineRun(ctx context.Context, runID string, status RunStatus, runErr error) error {
	if s == nil || s.store == nil {
		return errors.New("run ledger is not configured")
	}
	switch status {
	case RunStatusSuccess, RunStatusFailed, RunStatusCancelled:
	default:
		return fmt.Errorf("invalid terminal inline run status %q", status)
	}
	finalizeCtx, cancel := detachedRunFinalizationContext(ctx)
	defer cancel()
	finished := time.Now().UTC()
	if err := s.store.FinalizeExecution(finalizeCtx, strings.TrimSpace(runID), status, finished, runErr, "", nil); err != nil {
		return err
	}
	run, _, _, err := s.store.Get(finalizeCtx, strings.TrimSpace(runID))
	if err != nil {
		slog.Warn("failed to reload finalized inline run", "run_id", strings.TrimSpace(runID), "error", err)
		return nil
	}
	s.publishRunEvent("run.finished", run)
	return nil
}

func (s *Service) admitQueuedRun(ctx context.Context, client *river.Client[*sql.Tx], run PipelineRun, spec runSpecV1, plan *PipelineRunPlan) (PipelineRun, error) {
	if err := spec.validate(); err != nil {
		return PipelineRun{}, err
	}
	if err := validateRunSpecAdmissionBinding(run, spec); err != nil {
		return PipelineRun{}, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		tx, err := s.store.db.BeginTx(ctx, nil)
		if err != nil {
			return PipelineRun{}, err
		}
		queries := s.store.queries.WithTx(tx)
		id, err := s.store.createRun(ctx, queries, run)
		if err == nil {
			err = s.store.claimRunAdmission(ctx, tx, run, id, plan)
		}
		if err == nil {
			err = s.store.insertRunSpec(ctx, tx, id, spec)
		}
		if err == nil && plan != nil {
			err = s.store.insertRunPlan(ctx, tx, id, *plan)
		}
		var inserted *rivertype.JobInsertResult
		if err == nil {
			inserted, err = client.InsertTx(ctx, tx, pipelineRunJobArgs{RunID: id}, pipelineRunInsertOpts())
		}
		if err == nil {
			err = s.store.setRunRiverJob(ctx, queries, id, inserted.Job.ID)
			if err != nil {
				err = fmt.Errorf("link pipeline run to River job: %w", err)
			}
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			_ = tx.Rollback()
		}
		if errors.Is(err, ErrPipelineRunActive) {
			return PipelineRun{}, err
		}
		if isActiveRunSlotConstraint(err) {
			conflict, found, lookupErr := s.store.pipelineRunActiveError(ctx, run.PipelineID, runSlotKeys(run))
			if lookupErr != nil {
				return PipelineRun{}, errors.Join(err, lookupErr)
			}
			if found {
				return PipelineRun{}, conflict
			}
			if attempt == 0 {
				continue
			}
			return PipelineRun{}, fmt.Errorf("pipeline run admission retry exhausted after the active slot changed: %w", err)
		}
		if err != nil {
			return PipelineRun{}, err
		}
		run.ID = id
		riverJobID := inserted.Job.ID
		run.RiverJobID = &riverJobID
		s.hydrateSnapshotOrdinal(ctx, &run.SnapshotOrdinal, run.SnapshotVersionID)
		s.publishRunEvent("run.queued", run)
		return run, nil
	}
	return PipelineRun{}, errors.New("pipeline run admission retry exhausted")
}

func (s *Service) ListRuns(ctx context.Context, filter RunFilter) (RunList, error) {
	list, err := s.store.List(ctx, filter)
	if err != nil {
		return RunList{}, err
	}
	for index := range list.Runs {
		s.hydrateSnapshotOrdinal(ctx, &list.Runs[index].SnapshotOrdinal, list.Runs[index].SnapshotVersionID)
	}
	return list, nil
}

func (s *Service) GetRun(ctx context.Context, id string) (PipelineRun, []LogLine, []PipelineRunStep, error) {
	run, logs, steps, err := s.store.Get(ctx, id)
	if err != nil {
		return PipelineRun{}, nil, nil, err
	}
	s.hydrateSnapshotOrdinal(ctx, &run.SnapshotOrdinal, run.SnapshotVersionID)
	if err := s.hydrateRunCancellation(ctx, &run); err != nil {
		return PipelineRun{}, nil, nil, err
	}
	return run, trimLegacyOutputReplay(logs), steps, nil
}

func (s *Service) hydrateSnapshotOrdinal(ctx context.Context, destination *int64, versionID string) {
	if s == nil || destination == nil || s.snapshotOrdinal == nil || strings.TrimSpace(versionID) == "" {
		return
	}
	ordinal, err := s.snapshotOrdinal(ctx, strings.TrimSpace(versionID))
	if err == nil && ordinal > 0 {
		*destination = ordinal
	}
}

func (s *Service) GetRunPlan(ctx context.Context, id string) (PipelineRunPlan, bool, error) {
	return s.store.GetRunPlan(ctx, id)
}

func (s *Service) ListRunUnits(ctx context.Context, id string) ([]PipelineRunUnit, error) {
	return s.store.ListRunUnits(ctx, id)
}

func (s *Service) prepareRun(ctx context.Context, riverJobID int64, args pipelineRunJobArgs) (PipelineRun, runSpecV1, bool, error) {
	if strings.TrimSpace(args.RunID) == "" && riverJobID != 0 {
		if existingRunID, found, err := s.store.RunIDForRiverJob(ctx, riverJobID); err != nil {
			return PipelineRun{}, runSpecV1{}, false, err
		} else if found {
			args.RunID = existingRunID
		}
	}
	if strings.TrimSpace(args.RunID) != "" {
		run, _, _, err := s.store.Get(ctx, args.RunID)
		if err != nil {
			return PipelineRun{}, runSpecV1{}, false, err
		}
		if run.Status != RunStatusQueued && run.Status != RunStatusRunning {
			return PipelineRun{}, runSpecV1{}, false, nil
		}
		s.hydrateSnapshotOrdinal(ctx, &run.SnapshotOrdinal, run.SnapshotVersionID)
		if riverJobID != 0 {
			if run.RiverJobID != nil && *run.RiverJobID != riverJobID {
				return PipelineRun{}, runSpecV1{}, false, &invalidRunSpecError{
					RunID: run.ID,
					Err:   fmt.Errorf("queued run is linked to River job %d, not %d", *run.RiverJobID, riverJobID),
				}
			}
			if run.RiverJobID == nil {
				if err := s.store.SetRunRiverJob(ctx, args.RunID, riverJobID); err != nil {
					return PipelineRun{}, runSpecV1{}, false, err
				}
			}
		}
		spec, found, err := s.store.GetRunSpec(ctx, run.ID)
		if err != nil {
			return PipelineRun{}, runSpecV1{}, false, err
		}
		if !found {
			spec, err = legacyRunSpec(run, args)
			if err != nil {
				return PipelineRun{}, runSpecV1{}, false, &invalidRunSpecError{RunID: run.ID, Err: err}
			}
			spec, err = s.store.SetRunSpecIfMissing(ctx, run.ID, spec)
			if err != nil {
				if errors.Is(err, ErrPipelineRunActive) {
					return PipelineRun{}, runSpecV1{}, false, &invalidRunSpecError{RunID: run.ID, Err: err}
				}
				return PipelineRun{}, runSpecV1{}, false, err
			}
			run, _, _, err = s.store.Get(ctx, run.ID)
			if err != nil {
				return PipelineRun{}, runSpecV1{}, false, err
			}
		}
		if err := validateRunSpecBinding(run, spec); err != nil {
			return PipelineRun{}, runSpecV1{}, false, &invalidRunSpecError{RunID: run.ID, Err: err}
		}
		if spec.Dispatch != runDispatchRiver {
			return PipelineRun{}, runSpecV1{}, false, &invalidRunSpecError{
				RunID: run.ID, Err: fmt.Errorf("River worker cannot execute dispatch %q", spec.Dispatch),
			}
		}
		if err := s.store.validateActiveRunSpecSlotBinding(ctx, run, spec); err != nil {
			return PipelineRun{}, runSpecV1{}, false, &invalidRunSpecError{RunID: run.ID, Err: err}
		}
		if err := s.store.validateActiveRunOccurrenceBinding(ctx, run, spec); err != nil {
			return PipelineRun{}, runSpecV1{}, false, &invalidRunSpecError{RunID: run.ID, Err: err}
		}
		plan, foundPlan, err := s.store.GetRunPlan(ctx, run.ID)
		if err != nil {
			return PipelineRun{}, runSpecV1{}, false, err
		}
		if foundPlan {
			if err := validateRunPlanAdmissionBinding(run, spec, plan); err != nil {
				return PipelineRun{}, runSpecV1{}, false, &invalidRunPlanError{RunID: run.ID, Err: err}
			}
			run.ConfirmedPlan = &plan
			if plan.Blocked {
				return PipelineRun{}, runSpecV1{}, false, &scheduledPlanBlockedError{
					RunID: run.ID, err: fmt.Errorf("scheduled plan is blocked: %s", strings.Join(plan.Blockers, "; ")),
				}
			}
		}
		if run.Status == RunStatusRunning {
			if err := s.finalizeIndeterminateRetry(ctx, run); err != nil {
				return PipelineRun{}, runSpecV1{}, false, err
			}
			return PipelineRun{}, runSpecV1{}, false, nil
		}
		executionSpec, err := s.resolveRunSpecForExecution(ctx, spec, plan, foundPlan)
		if err != nil {
			return PipelineRun{}, runSpecV1{}, false, &invalidRunPlanError{RunID: run.ID, Err: err}
		}
		run.RiverJobID = &riverJobID
		return applyRunSpec(run, executionSpec), executionSpec, true, nil
	}

	prepared, shouldAdmit, err := s.prepareScheduledRunAdmission(ctx, args)
	if err != nil || !shouldAdmit {
		return PipelineRun{}, runSpecV1{}, false, err
	}
	if riverJobID != 0 {
		prepared.Run.RiverJobID = &riverJobID
	}
	id, err := s.store.CreateScheduleOccurrenceAttemptWithSpecAndPlan(
		ctx, prepared.Occurrence, prepared.Run, prepared.Spec, prepared.Plan,
	)
	if err != nil {
		var alreadyAdmitted *ScheduleOccurrenceAlreadyAdmittedError
		if errors.As(err, &alreadyAdmitted) {
			return PipelineRun{}, runSpecV1{}, false, nil
		}
		return PipelineRun{}, runSpecV1{}, false, err
	}
	prepared.Run.ID = id
	prepared.Run = applyRunSpec(prepared.Run, prepared.Spec)
	prepared.Run.ConfirmedPlan = &prepared.Plan
	s.hydrateSnapshotOrdinal(ctx, &prepared.Run.SnapshotOrdinal, prepared.Run.SnapshotVersionID)
	s.publishRunEvent("run.queued", prepared.Run)
	if prepared.Plan.Blocked {
		return PipelineRun{}, runSpecV1{}, false, &scheduledPlanBlockedError{
			RunID: id,
			err:   fmt.Errorf("scheduled plan is blocked: %s", strings.Join(prepared.Plan.Blockers, "; ")),
		}
	}
	return prepared.Run, prepared.Spec, true, nil
}

type scheduledRunAdmission struct {
	Occurrence ScheduleOccurrence
	Run        PipelineRun
	Spec       runSpecV1
	Plan       PipelineRunPlan
}

func (s *Service) prepareScheduledRunAdmission(
	ctx context.Context,
	args pipelineRunJobArgs,
) (scheduledRunAdmission, bool, error) {
	// Resolve the stable UUID to the current workspace incarnation; a missing
	// pipeline skips silently because reconciliation will archive its schedule.
	pipelineUUID := strings.TrimSpace(args.PipelineUUID)
	encodedPipelineID := strings.TrimSpace(args.PipelineID)
	pipelineName := args.PipelineName
	if pipelineUUID != "" {
		ref, ok := s.resolveRef(ctx, pipelineUUID)
		if !ok {
			return scheduledRunAdmission{}, false, nil
		}
		encodedPipelineID = ref.EncodedID
		if ref.Name != "" {
			pipelineName = ref.Name
		}
	}

	if encodedPipelineID == "" {
		return scheduledRunAdmission{}, false, &invalidScheduleSignalError{err: errors.New("pipeline id is required")}
	}
	args.PipelineID = encodedPipelineID
	start, end := s.scheduledWindow(ctx, args, time.Now().UTC())
	explicitStart, explicitEnd, err := parseRequestWindow(args.Start, args.End)
	if err != nil {
		return scheduledRunAdmission{}, false, &invalidScheduleSignalError{err: fmt.Errorf("invalid scheduled interval: %w", err)}
	}
	if explicitStart != nil {
		start = *explicitStart
		end = *explicitEnd
	}
	occurrence, err := newScheduleOccurrence(pipelineUUID, args.Environment, start, end)
	if err != nil {
		return scheduledRunAdmission{}, false, &invalidScheduleSignalError{err: err}
	}
	occurrence, _, err = s.store.EnsureScheduleOccurrence(ctx, occurrence)
	if err != nil {
		return scheduledRunAdmission{}, false, err
	}
	s.publishScheduleOccurrenceEvent(occurrence)
	if occurrence.Status == ScheduleOccurrenceActive || occurrence.Status == ScheduleOccurrenceSuccess {
		return scheduledRunAdmission{}, false, nil
	}
	args.OccurrenceKey = occurrence.Key
	resolvedVariables, err := s.resolveScheduleVariables(
		secretstore.WithPurpose(ctx, secretstore.PurposeScheduledRun),
		args.Environment,
		args.Variables,
		args.VariableReferences,
	)
	if err != nil {
		return scheduledRunAdmission{}, false, &invalidScheduleSignalError{err: fmt.Errorf("resolve scheduled variables: %w", err)}
	}
	run := PipelineRun{
		PipelineID:        encodedPipelineID,
		PipelineUUID:      pipelineUUID,
		Pipeline:          pipelineName,
		Environment:       strings.TrimSpace(args.Environment),
		Trigger:           RunTriggerSchedule,
		Status:            RunStatusQueued,
		WinStart:          &start,
		WinEnd:            &end,
		SnapshotVersionID: args.SnapshotVersionID,
		FullRefresh:       args.FullRefresh,
		Backfill:          args.Backfill,
		SensorMode:        strings.TrimSpace(args.SensorMode),
		VariableOverrides: cloneScheduleVariables(args.Variables),
	}
	executionTime := time.Now().UTC()
	run.ExecutionTime = &executionTime
	spec := scheduledRunSpec(run, args)
	if s.planScheduledRun == nil {
		return scheduledRunAdmission{}, false, errors.New("scheduled-run planning is unavailable")
	}
	planned, err := s.planScheduledRun(ctx, ScheduledRunPlanRequest{
		PipelineID: encodedPipelineID, PipelineUUID: pipelineUUID,
		Environment: run.Environment, SnapshotVersionID: run.SnapshotVersionID,
		Start: start, End: end, ExecutionTime: executionTime,
		VariableOverrides: resolvedVariables,
	})
	if err != nil {
		return scheduledRunAdmission{}, false, fmt.Errorf("plan scheduled run: %w", err)
	}
	run.ExpectedSourceMerkle = planned.Plan.SourceMerkle
	run.ExpectedConfigurationDigest = planned.Plan.ConfigurationDigest
	spec.Expected = &runExpectedIdentity{
		SourceMerkle: planned.Plan.SourceMerkle, ConfigurationDigest: planned.Plan.ConfigurationDigest,
	}
	if err := spec.validate(); err != nil {
		return scheduledRunAdmission{}, false, &invalidScheduleSignalError{err: err}
	}
	return scheduledRunAdmission{
		Occurrence: occurrence,
		Run:        run,
		Spec:       spec,
		Plan:       planned.Plan,
	}, true, nil
}

func (s *Service) admitScheduledSignal(
	ctx context.Context,
	client *river.Client[*sql.Tx],
	args pipelineRunJobArgs,
) error {
	prepared, shouldAdmit, err := s.prepareScheduledRunAdmission(ctx, args)
	if err != nil || !shouldAdmit {
		return err
	}
	var inserted *rivertype.JobInsertResult
	id, err := s.store.createScheduleOccurrenceAttemptWithSpecAndPlan(
		ctx,
		prepared.Occurrence,
		prepared.Run,
		prepared.Spec,
		prepared.Plan,
		func(tx *sql.Tx, runID string) error {
			var insertErr error
			inserted, insertErr = client.InsertTx(
				ctx, tx, pipelineRunJobArgs{RunID: runID}, pipelineRunInsertOpts(),
			)
			if insertErr != nil {
				return insertErr
			}
			if err := s.store.setRunRiverJob(ctx, s.store.queries.WithTx(tx), runID, inserted.Job.ID); err != nil {
				return fmt.Errorf("link scheduled run to River execution job: %w", err)
			}
			return nil
		},
	)
	if err != nil {
		var alreadyAdmitted *ScheduleOccurrenceAlreadyAdmittedError
		if errors.As(err, &alreadyAdmitted) {
			return nil
		}
		return err
	}
	prepared.Run.ID = id
	riverJobID := inserted.Job.ID
	prepared.Run.RiverJobID = &riverJobID
	prepared.Run = applyRunSpec(prepared.Run, prepared.Spec)
	prepared.Run.ConfirmedPlan = &prepared.Plan
	s.hydrateSnapshotOrdinal(ctx, &prepared.Run.SnapshotOrdinal, prepared.Run.SnapshotVersionID)
	s.publishRunEvent("run.queued", prepared.Run)
	return nil
}

func (s *Service) execute(ctx context.Context, run PipelineRun, spec runSpecV1) error {
	executionCtx, cancelExecution := context.WithCancel(ctx)
	s.mu.Lock()
	s.activeRunCancels[run.ID] = cancelExecution
	s.mu.Unlock()
	defer func() {
		cancelExecution()
		s.mu.Lock()
		delete(s.activeRunCancels, run.ID)
		s.mu.Unlock()
	}()
	ctx = executionCtx
	defer func() {
		if recovered := recover(); recovered != nil {
			err := fmt.Errorf("pipeline run panicked: %v", recovered)
			finished := time.Now().UTC()
			finalizeCtx, cancel := detachedRunFinalizationContext(ctx)
			defer cancel()
			if finalizeErr := s.store.FinalizeExecution(finalizeCtx, run.ID, RunStatusFailed, finished, err, "", nil); finalizeErr != nil {
				slog.Error("failed to persist panicked pipeline run", "run_id", run.ID, "error", finalizeErr)
			}
			run.Status = RunStatusFailed
			run.Error = err.Error()
			run.FinishedAt = &finished
			s.publishRunEvent("run.finished", run)
		}
	}()
	run = ensureScheduledRunWindow(run)
	started := time.Now().UTC()
	if err := s.store.MarkRunning(ctx, run.ID, started); err != nil {
		return &runStartPersistenceError{err: err}
	}
	run.Status = RunStatusRunning
	run.StartedAt = &started

	req := RunRequest{
		RunID:                run.ID,
		PipelineID:           spec.Pipeline.ID,
		PipelineUUID:         spec.Pipeline.UUID,
		Environment:          spec.Requested.Environment,
		Scheduled:            spec.Origin == RunTriggerSchedule,
		SnapshotVersionID:    spec.Source.SnapshotVersionID,
		FullRefresh:          spec.Requested.FullRefresh,
		Backfill:             spec.Requested.Backfill,
		ConfirmedEnvironment: spec.Authorization.ConfirmedEnvironment,
		SensorMode:           spec.Requested.SensorMode,
		VariableOverrides:    spec.Requested.Variables,
	}
	if spec.Requested.ExecutionTime != nil {
		req.ExecutionTime = spec.Requested.ExecutionTime.UTC().Format(time.RFC3339Nano)
	}
	if spec.Expected != nil {
		req.ExpectedSourceMerkle = spec.Expected.SourceMerkle
		req.ExpectedConfigurationDigest = spec.Expected.ConfigurationDigest
	}
	req.ConfirmedPlan = run.ConfirmedPlan
	req.OnContextResolved = func(resolved RunExecutionContext) error {
		if err := s.store.SetRunExecutionContext(ctx, run.ID, resolved); err != nil {
			return err
		}
		windowStart := resolved.WinStart
		windowEnd := resolved.WinEnd
		run.Environment = strings.TrimSpace(resolved.Environment)
		run.WinStart = &windowStart
		run.WinEnd = &windowEnd
		run.FullRefresh = resolved.FullRefresh
		run.Backfill = resolved.Backfill
		run.SensorMode = strings.TrimSpace(resolved.SensorMode)
		run.ExecutionContextResolved = true
		s.publishRunEvent("run.started", run)
		return nil
	}
	req.OnTargetsResolved = func(snapshot ExecutionTargetSnapshot) error {
		if err := s.store.SetRunExecutionTargetSnapshot(ctx, run.ID, snapshot); err != nil {
			return err
		}
		captured := snapshot
		run.ExecutionTargetSnapshot = &captured
		return nil
	}
	req.OnExecutionUnitsResolved = func(units []PipelineRunExecutionUnit) error {
		if len(units) == 0 {
			return nil
		}
		return s.store.BindQueuedRunExecutionUnits(ctx, run.ID, units)
	}
	req.OnStep = func(event RunStepEvent) error {
		return s.persistRunStep(ctx, run.ID, event)
	}
	req.OnUnit = func(event PipelineRunUnitEvent) error {
		return s.persistRunUnit(ctx, run.ID, event)
	}
	if run.WinStart != nil {
		req.Start = run.WinStart.Format(time.RFC3339Nano)
	}
	if run.WinEnd != nil {
		req.End = run.WinEnd.Format(time.RFC3339Nano)
	}
	result := s.runner(ctx, req, func(line string) {
		logLine := LogLine{At: time.Now().UTC(), Line: line}
		_ = s.store.AppendLog(ctx, run.ID, logLine)
		s.publishRunEvent("run.log", map[string]any{"run_id": run.ID, "log": logLine})
	})
	status, runErr := statusFromResult(result)
	finished := time.Now().UTC()
	var watermark string
	var watermarkUpTo *time.Time
	if status == RunStatusSuccess && spec.Schedule != nil && spec.Schedule.AdvancesWatermark && spec.Requested.End != nil {
		watermark = spec.Schedule.PipelineUUID + "|" + spec.Schedule.Environment
		upTo := *spec.Requested.End
		watermarkUpTo = &upTo
	}
	finalizeCtx, cancelFinalize := detachedRunFinalizationContext(ctx)
	defer cancelFinalize()
	finishErr := s.store.FinalizeExecution(finalizeCtx, run.ID, status, finished, runErr, watermark, watermarkUpTo)
	if finishErr != nil {
		_ = s.store.AppendLog(finalizeCtx, run.ID, LogLine{At: time.Now().UTC(), Line: "failed to persist run status: " + finishErr.Error()})
		return finishErr
	}
	if persisted, _, _, getErr := s.store.Get(finalizeCtx, run.ID); getErr == nil {
		// Stable scheduled identity is private RunSpec provenance rather than a
		// public run-row column. Preserve it while taking user-visible effective
		// context from the canonical row written by OnContextResolved and Finish.
		persisted.PipelineUUID = run.PipelineUUID
		run = persisted
	} else {
		_ = s.store.AppendLog(finalizeCtx, run.ID, LogLine{At: time.Now().UTC(), Line: "failed to reload canonical run context: " + getErr.Error()})
	}
	if run.FinishedAt != nil {
		finished = *run.FinishedAt
	}
	run.Status = status
	run.FinishedAt = &finished
	if runErr != nil {
		run.Error = runErr.Error()
	}
	s.publishRunEvent("run.finished", run)
	return nil
}

func detachedRunFinalizationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), runFinalizationTimeout)
}

func (s *Service) finalizeIndeterminateRetry(ctx context.Context, run PipelineRun) error {
	finished := time.Now().UTC()
	runErr := errors.New(indeterminateRetryMessage)
	finalizeCtx, cancel := detachedRunFinalizationContext(ctx)
	defer cancel()
	if err := s.store.FinalizeExecution(finalizeCtx, run.ID, RunStatusFailed, finished, runErr, "", nil); err != nil {
		return fmt.Errorf("finalize indeterminate retry for pipeline run %s: %w", run.ID, err)
	}
	if err := s.store.AppendLog(finalizeCtx, run.ID, LogLine{
		At:   finished,
		Line: "scheduler recovery: " + indeterminateRetryMessage,
	}); err != nil {
		slog.Warn("failed to append indeterminate pipeline run retry diagnostic", "run_id", run.ID, "error", err)
	}
	run.Status = RunStatusFailed
	run.Error = indeterminateRetryMessage
	run.FinishedAt = &finished
	s.publishRunEvent("run.finished", run)
	return nil
}

func (s *Service) persistRunStep(ctx context.Context, runID string, event RunStepEvent) error {
	asset := strings.TrimSpace(event.Asset)
	if asset == "" {
		return nil
	}
	step := PipelineRunStep{
		RunID:                     runID,
		Asset:                     asset,
		Status:                    event.Status,
		StartedAt:                 event.StartedAt,
		FinishedAt:                event.FinishedAt,
		Error:                     event.Error,
		CompletionOrdinal:         event.CompletionOrdinal,
		UpstreamWriters:           event.UpstreamWriters,
		HasUpstreamWriterSnapshot: event.HasUpstreamWriterSnapshot,
	}
	if step.Status == "" {
		step.Status = RunStatusRunning
	}
	if err := s.store.UpsertStep(ctx, step); err != nil {
		return fmt.Errorf("persist step %s for run %s: %w", asset, runID, err)
	}
	s.publishRunEvent("run.step", step)
	return nil
}

func (s *Service) persistRunUnit(ctx context.Context, runID string, event PipelineRunUnitEvent) error {
	if err := s.store.UpdateRunUnit(ctx, runID, event); err != nil {
		return fmt.Errorf("persist execution unit %d for run %s: %w", event.Position, runID, err)
	}
	units, err := s.store.ListRunUnits(ctx, runID)
	if err != nil {
		return err
	}
	for _, unit := range units {
		if unit.Position == event.Position {
			s.publishRunEvent("run.unit", map[string]any{"run_id": runID, "unit": unit})
			break
		}
	}
	return nil
}

func (s *Service) windowStart(ctx context.Context, pipelineID string, end time.Time) time.Time {
	if last, ok, err := s.store.LastInterval(ctx, pipelineID); err == nil && ok && !last.IsZero() {
		return last
	}
	return end
}

func (s *Service) scheduledWindow(ctx context.Context, args pipelineRunJobArgs, now time.Time) (time.Time, time.Time) {
	end := now.UTC()
	start := s.windowStart(ctx, args.PipelineID, end)
	if strings.TrimSpace(args.Schedule) != "" {
		if schedule, err := parseSchedule(args.Schedule, args.Timezone); err == nil {
			if scheduledStart, scheduledEnd, ok := previousScheduleInterval(schedule, end); ok {
				end = scheduledEnd
				if start.Before(end) {
					return start, end
				}
				return scheduledStart, end
			}
		}
	}
	if start.Before(end) {
		return start, end
	}
	return end.Add(-time.Minute), end
}

func previousScheduleInterval(schedule cron.Schedule, now time.Time) (time.Time, time.Time, bool) {
	for _, lookback := range []time.Duration{2 * time.Hour, 2 * 24 * time.Hour, 35 * 24 * time.Hour, 370 * 24 * time.Hour} {
		cursor := now.Add(-lookback)
		var beforePrevious time.Time
		var previous time.Time
		for i := 0; i < 10000; i++ {
			next := schedule.Next(cursor)
			if next.After(now) {
				if !beforePrevious.IsZero() && !previous.IsZero() {
					return beforePrevious, previous, true
				}
				break
			}
			beforePrevious = previous
			previous = next
			cursor = next
		}
	}
	return time.Time{}, time.Time{}, false
}

func ensureScheduledRunWindow(run PipelineRun) PipelineRun {
	if run.Trigger != RunTriggerSchedule || run.WinStart == nil || run.WinEnd == nil || run.WinStart.Before(*run.WinEnd) {
		return run
	}
	fixedStart := run.WinEnd.Add(-time.Minute)
	run.WinStart = &fixedStart
	return run
}

func (s *Service) publishRunEvent(eventType string, payload any) {
	if s.publish == nil {
		return
	}
	s.publish(map[string]any{"type": eventType, "run": payload})
}

func (s *Service) publishScheduleOccurrenceEvent(occurrence ScheduleOccurrence) {
	if s.publish == nil {
		return
	}
	s.publish(map[string]any{
		"type":          "schedule.occurrence",
		"pipeline_uuid": occurrence.PipelineUUID,
		"environment":   occurrence.Environment,
	})
}

func pipelineRunInsertOpts() *river.InsertOpts {
	return &river.InsertOpts{
		MaxAttempts: 1,
		Queue:       pipelineRunQueue,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRunning,
				rivertype.JobStateScheduled,
			},
		},
	}
}

func scheduleSignalInsertOpts() *river.InsertOpts {
	return &river.InsertOpts{
		MaxAttempts: 1,
		Queue:       pipelineRunQueue,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStatePending,
				rivertype.JobStateRunning,
				rivertype.JobStateScheduled,
			},
		},
	}
}

func parseSchedule(expr, timezone string) (cron.Schedule, error) {
	location := time.UTC
	if strings.TrimSpace(timezone) != "" {
		loaded, err := time.LoadLocation(strings.TrimSpace(timezone))
		if err != nil {
			return nil, err
		}
		location = loaded
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	if strings.HasPrefix(strings.TrimSpace(expr), "@") {
		return parser.Parse(expr)
	}
	return parser.Parse("CRON_TZ=" + location.String() + " " + expr)
}

func normalizeManualRunSource(source RunSource, snapshotVersionID string) (RunSource, string, error) {
	if source == "" {
		source = RunSourceWorkingTree
	}
	pinnedVersion := strings.TrimSpace(snapshotVersionID)
	switch source {
	case RunSourceWorkingTree:
		if pinnedVersion != "" {
			return "", "", errors.New("snapshot_version_id must be empty when source is working_tree")
		}
		return source, "", nil
	case RunSourceSnapshot:
		if pinnedVersion == "" {
			return "", "", errors.New("snapshot_version_id is required when source is snapshot")
		}
		return source, pinnedVersion, nil
	default:
		return "", "", fmt.Errorf("invalid run source %q: expected working_tree or snapshot", source)
	}
}

func parseRequestWindow(startValue, endValue string) (*time.Time, *time.Time, error) {
	return runcontext.NormalizeWindow(startValue, endValue)
}
