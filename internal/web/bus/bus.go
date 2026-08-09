// Package bus is the single in-process seam that observes run completion and
// asset saves. The fingerprint cache, the materialization log, and the
// staleness service all attach here instead of each finding their own hook
// into the executor or the file watcher.
package bus

import (
	"errors"
	"sync"
	"time"
)

type QualityStatus string

const (
	QualityStatusPassed QualityStatus = "passed"
	QualityStatusFailed QualityStatus = "failed"

	QualityCheckKindCustom QualityCheckKind = "custom"
	QualityCheckKindColumn QualityCheckKind = "column"
)

type QualityCheckKind string

// QualityCheckFailure identifies a failed assertion without persisting its
// rendered SQL or runtime error. The run log remains the source for potentially
// sensitive execution details.
type QualityCheckFailure struct {
	Kind     QualityCheckKind `json:"kind"`
	Name     string           `json:"name"`
	Column   string           `json:"column,omitempty"`
	Blocking bool             `json:"blocking,omitempty"`
}

// AssetRun describes one asset that a completed run materialized.
type AssetRun struct {
	// AssetID is the durable identifier (pipeline UUID + ":" + asset name).
	AssetID   string
	AssetName string
	Status    string // "succeeded" / "failed" / "cancelled"
	// QualityStatus describes the checks that ran after the main task. It is
	// intentionally separate from Status: a successful materialization can be
	// fresh while its assertions fail.
	QualityStatus QualityStatus
	FailedChecks  []QualityCheckFailure
	// StartedAt/FinishedAt describe the main task that can write the physical
	// output. They deliberately exclude checks and metadata tasks. A recorder
	// may fall back to RunCompleted.CompletedAt for legacy/synthetic events.
	StartedAt  *time.Time
	FinishedAt *time.Time
	// CompletionOrdinal is stable within CompletionID and disambiguates
	// multiple writes that finish at the same persisted timestamp.
	CompletionOrdinal    int64
	HasCompletionOrdinal bool
	// The remaining fields are the secret-free, pre-execution snapshot for this
	// asset. TargetIdentity is empty unless TargetFidelity is exact. Fingerprint
	// fields describe the source/configuration selected before the first task;
	// they must never be reconstructed from mutable configuration at completion.
	TargetIdentity   string
	TargetFidelity   string
	Fingerprint      string
	OwnContent       string
	ConsumedVarsHash string
	VarsHash         string
	// UpstreamWriters is the trusted latest-writer read set captured immediately
	// before this main task began. The explicit presence flag distinguishes an
	// empty read set from legacy evidence that never captured one.
	UpstreamWriters           map[string]UpstreamWriterSnapshot
	HasUpstreamWriterSnapshot bool
}

// UpstreamWriterSnapshot identifies the physical upstream output visible at
// the start of one consumer task. The map containing it is keyed by AssetID.
type UpstreamWriterSnapshot struct {
	AssetID           string
	TargetIdentity    string
	Fingerprint       string
	VarsHash          string
	TargetGeneration  int64
	CompletionID      string
	CompletionOrdinal int64
	MaterializedAt    time.Time
}

type ExecutionUpstreamSnapshot struct {
	Type                string
	Value               string
	Mode                string
	ResolvedAssetID     string
	Required            bool
	TargetIdentity      string
	ExpectedFingerprint string
	VarsHash            string
	TargetGeneration    int64
	CompletionID        string
	CompletionOrdinal   int64
}

type ExecutionResourceClaim struct {
	Kind     string
	Identity string
}

type ExecutionResources struct {
	Isolation string
	Claims    []ExecutionResourceClaim
}

// ExecutionContractSnapshot is the secret-free per-asset admission contract
// retained by version-four execution target snapshots.
type ExecutionContractSnapshot struct {
	AssetID               string
	AssetName             string
	ConnectionKeys        []string
	MutationResources     ExecutionResources
	CoordinationResources ExecutionResources
}

// ExecutionTargetSnapshotEntry is one value-only entry captured before any
// main task starts. The RunCompleted map is keyed by canonical asset name so
// an executed downstream can still resolve the captured identity of an
// upstream that was not part of the selected run.
type ExecutionTargetSnapshotEntry struct {
	AssetID                     string
	TargetIdentity              string
	TargetFidelity              string
	TargetWriteEvidenceRequired bool
	WriteResourceKind           string
	WriteResourceIdentity       string
	WriteResourceFidelity       string
	ExecutionContract           ExecutionContractSnapshot
	Fingerprint                 string
	OwnContent                  string
	ConsumedVarsHash            string
	VarsHash                    string
	Upstreams                   []ExecutionUpstreamSnapshot
	CoverageMode                string
	RefreshRestricted           bool
}

// RunCompleted is emitted once per finished run (build-mode single asset,
// build-mode pipeline run, or scheduled run).
type RunCompleted struct {
	RunID string // scheduler run ID when applicable, "" for build-mode runs
	// CompletionID is non-empty for new events. Scheduler-backed executions use
	// RunID; inline executions use a generated UUID so replay ordering never
	// depends on a second-resolution log identifier.
	CompletionID string
	PipelineUUID string
	Environment  string
	// WinStart/WinEnd carry the requested execution interval. FullRefresh is
	// independent: a window-filtered full refresh still represents that window.
	WinStart    *time.Time
	WinEnd      *time.Time
	FullRefresh bool
	CompletedAt time.Time
	Assets      []AssetRun
	// ExecutionTargets is the complete parsed pipeline snapshot, not only the
	// assets attempted by this run. SnapshotVersion identifies its contract.
	ExecutionTargetSnapshotVersion int
	ExecutionPipelineUUID          string
	ExecutionTargets               map[string]ExecutionTargetSnapshotEntry
	// SnapshotVersionID/SnapshotDir are set when the run executed a deployed
	// snapshot; SnapshotDir is the materialized source the run actually used
	// (valid for the duration of the event dispatch).
	SnapshotVersionID string
	SnapshotDir       string
}

// AssetSaved is emitted whenever an asset's saved state changes on disk,
// whether through the API or an external editor caught by the watcher.
type AssetSaved struct {
	PipelineUUID string
	AssetID      string // durable identifier
	AssetName    string
	Path         string // workspace-relative path
	SavedAt      time.Time
}

// TargetWriteChanged is emitted after a physical-target claim becomes active
// or dirty. It invalidates freshness immediately, including the failure path
// where no completion event can be dispatched.
type TargetWriteChanged struct {
	PipelineUUID string
	AssetID      string
}

// Events is the seam consumers subscribe to. Handlers run synchronously on
// the emitting goroutine, in subscription order — keep them fast and never
// emit from inside a handler.
type Events interface {
	OnRunCompleted(func(RunCompleted) error) (unsubscribe func())
	OnAssetSaved(func(AssetSaved)) (unsubscribe func())
	OnTargetWriteChanged(func(TargetWriteChanged)) (unsubscribe func())
}

// Bus is the canonical Events implementation. The zero value is unusable;
// call New.
type Bus struct {
	mu         sync.RWMutex
	nextID     int
	runSubs    map[int]func(RunCompleted) error
	saveSubs   map[int]func(AssetSaved)
	targetSubs map[int]func(TargetWriteChanged)
}

func New() *Bus {
	return &Bus{
		runSubs:    make(map[int]func(RunCompleted) error),
		saveSubs:   make(map[int]func(AssetSaved)),
		targetSubs: make(map[int]func(TargetWriteChanged)),
	}
}

func (b *Bus) OnTargetWriteChanged(handler func(TargetWriteChanged)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	b.targetSubs[id] = handler
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.targetSubs, id)
	}
}

func (b *Bus) OnRunCompleted(handler func(RunCompleted) error) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	b.runSubs[id] = handler
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.runSubs, id)
	}
}

func (b *Bus) OnAssetSaved(handler func(AssetSaved)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextID
	b.nextID++
	b.saveSubs[id] = handler
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.saveSubs, id)
	}
}

func (b *Bus) EmitRunCompleted(event RunCompleted) error {
	if b == nil {
		return nil
	}
	var errs []error
	for _, handler := range b.snapshotRunSubs() {
		if err := handler(event); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (b *Bus) EmitAssetSaved(event AssetSaved) {
	if b == nil {
		return
	}
	for _, handler := range b.snapshotSaveSubs() {
		handler(event)
	}
}

func (b *Bus) EmitTargetWriteChanged(event TargetWriteChanged) {
	if b == nil {
		return
	}
	for _, handler := range b.snapshotTargetSubs() {
		handler(event)
	}
}

func (b *Bus) snapshotRunSubs() []func(RunCompleted) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	handlers := make([]func(RunCompleted) error, 0, len(b.runSubs))
	for id := 0; id < b.nextID; id++ {
		if handler, ok := b.runSubs[id]; ok {
			handlers = append(handlers, handler)
		}
	}
	return handlers
}

func (b *Bus) snapshotSaveSubs() []func(AssetSaved) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	handlers := make([]func(AssetSaved), 0, len(b.saveSubs))
	for id := 0; id < b.nextID; id++ {
		if handler, ok := b.saveSubs[id]; ok {
			handlers = append(handlers, handler)
		}
	}
	return handlers
}

func (b *Bus) snapshotTargetSubs() []func(TargetWriteChanged) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	handlers := make([]func(TargetWriteChanged), 0, len(b.targetSubs))
	for id := 0; id < b.nextID; id++ {
		if handler, ok := b.targetSubs[id]; ok {
			handlers = append(handlers, handler)
		}
	}
	return handlers
}
