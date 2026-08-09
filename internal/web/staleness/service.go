// Package staleness classifies every asset of a pipeline against the
// materialization coverage table for the current selection (environment,
// time range, variables). It keeps the last-requested selection per
// pipeline in memory and recomputes on AssetSaved / RunCompleted bus
// events, pushing updates over SSE — the UI never polls per render.
package staleness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"go.uber.org/zap"
	"renart/internal/web/bus"
	"renart/internal/web/fingerprint"
	"renart/internal/web/identity"
	"renart/internal/web/matlog"
)

type Status string

const (
	// StatusFresh: coverage exists for the current fingerprint + vars + range.
	StatusFresh Status = "fresh"
	// StatusStaleEdited: this asset's own definition changed since last build.
	StatusStaleEdited Status = "stale_edited"
	// StatusStaleDeployment: the latest physical output came from a deployed
	// snapshot whose asset definition differs from the saved working tree.
	StatusStaleDeployment Status = "stale_deployment"
	// StatusStaleUpstream: inherited staleness via the Merkle cascade (or a
	// changed variable value — own content matches, the full hash does not).
	StatusStaleUpstream Status = "stale_upstream"
	// StatusPartial: incremental asset with some but not all of the selected
	// range covered.
	StatusPartial Status = "partial"
	// StatusNeverBuilt: no coverage row in this environment at all, under
	// any fingerprint.
	StatusNeverBuilt Status = "never_built"
	// StatusMissing: the log says fresh but verification could not find the
	// table in the warehouse.
	StatusMissing Status = "missing"
	// StatusVolatile marks sensors. A successful check is useful run history,
	// not durable materialization coverage, so every requested run checks again.
	StatusVolatile Status = "volatile"
	// StatusExternal marks source assets whose physical data is maintained
	// outside Renart. Their declarations can participate in dependency
	// fingerprints, but the source itself has no Renart build/freshness state.
	StatusExternal Status = "external"
)

// Interval is a [Start, End) time range.
type Interval struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// TargetFidelity describes whether the physical output selected for an asset
// can safely participate in freshness. Only exact targets have a durable,
// comparable identity. Runtime-only and unresolved targets deliberately fail
// closed, while legacy is retained for callers that have not supplied target
// resolution yet.
type TargetFidelity string

const (
	TargetFidelityExact       TargetFidelity = "exact"
	TargetFidelityRuntimeOnly TargetFidelity = "runtime_only"
	TargetFidelityUnresolved  TargetFidelity = "unresolved"
	TargetFidelityLegacy      TargetFidelity = "legacy"
)

// LatestPhysicalOutput is the durable global writer currently associated with
// an exact selected target. WriterAssetID and WriterEnvironment describe the
// output that is physically present; they can differ from the selected asset
// and environment when another context displaced it. An ambiguous writer is
// exposed for diagnosis but never contributes reusable coverage.
type LatestPhysicalOutput struct {
	TargetIdentity    string    `json:"target_identity"`
	TargetGeneration  int64     `json:"target_generation"`
	WriterAssetID     string    `json:"writer_asset_id"`
	WriterEnvironment string    `json:"writer_environment"`
	Fingerprint       string    `json:"fingerprint"`
	VarsHash          string    `json:"vars_hash"`
	RunID             string    `json:"run_id,omitempty"`
	SnapshotVersionID string    `json:"snapshot_version_id,omitempty"`
	MaterializedAt    time.Time `json:"materialized_at"`
	CompletionID      string    `json:"completion_id"`
	CompletionOrdinal int64     `json:"completion_ordinal"`
	Ambiguous         bool      `json:"ambiguous"`
}

// AssetStatus is the classification result for one asset.
type AssetStatus struct {
	AssetID            string     `json:"asset_id"` // durable: pipeline UUID + ":" + name
	AssetName          string     `json:"asset_name"`
	Status             Status     `json:"status"`
	Fingerprint        string     `json:"fingerprint"`
	IntervalAware      bool       `json:"interval_aware"`
	BackfillSafe       bool       `json:"backfill_safe"`
	Volatile           bool       `json:"volatile,omitempty"`
	CoveredSeconds     float64    `json:"covered_seconds,omitempty"`
	TotalSeconds       float64    `json:"total_seconds,omitempty"`
	Gaps               []Interval `json:"gaps,omitempty"` // uncovered sub-ranges, the Build-stale plan input
	LastMaterializedAt *time.Time `json:"last_materialized_at,omitempty"`
	// Last run attempt (success or failure), orthogonal to the base Status.
	// Together with the base status and the current fingerprint they let the UI
	// tell an untested edit from a run that failed, and an unchanged asset whose
	// last run failed. LastRunOnCurrentContent is true when the last run's
	// fingerprint matches the asset's current fingerprint (i.e. the run was on
	// the content still on disk).
	LastRunStatus           string     `json:"last_run_status,omitempty"` // "succeeded" | "failed" | "cancelled"
	LastRunAt               *time.Time `json:"last_run_at,omitempty"`
	LastRunOnCurrentContent bool       `json:"last_run_on_current_content,omitempty"`
	// Quality is the latest completed assertion outcome, independent of data
	// freshness and the main-task status. QualityOnCurrentContent prevents an
	// old failure from being presented as a problem with newly edited SQL.
	QualityStatus           bus.QualityStatus         `json:"quality_status,omitempty"`
	FailedChecks            []bus.QualityCheckFailure `json:"failed_checks,omitempty"`
	QualityRunID            string                    `json:"quality_run_id,omitempty"`
	QualityCheckedAt        *time.Time                `json:"quality_checked_at,omitempty"`
	QualityOnCurrentContent bool                      `json:"quality_on_current_content,omitempty"`
	// TargetFidelity and TargetIdentity describe the output selected by the
	// current environment/configuration. LatestOutput describes what is
	// physically present there, when that fact is durably known. Exact targets
	// with an active/dirty write claim intentionally have no LatestOutput.
	TargetFidelity TargetFidelity        `json:"target_fidelity"`
	TargetIdentity string                `json:"target_identity,omitempty"`
	LatestOutput   *LatestPhysicalOutput `json:"latest_output,omitempty"`
}

// Snapshot is one internally consistent staleness computation. The opaque
// DataStateToken changes exactly when data state relevant to a needed
// selection changes; callers can compare it without interpreting writer or
// coverage internals.
type Snapshot struct {
	DataStateToken string        `json:"data_state_token"`
	Assets         []AssetStatus `json:"assets"`
}

// Selection identifies what the user is looking at.
type Selection struct {
	PipelineUUID string
	// EncodedPipelineID is the path-encoded API pipeline ID, carried along
	// so published events and verification calls can address API routes.
	EncodedPipelineID string
	Environment       string
	// Start/End bound the freshness check for interval-aware assets; when
	// nil, any coverage under the current fingerprint counts as fresh.
	Start, End *time.Time
	// VarOverrides are merged over pipeline variable defaults.
	VarOverrides map[string]any
}

func (s Selection) rangeInterval() *Interval {
	if s.Start == nil || s.End == nil || !s.End.After(*s.Start) {
		return nil
	}
	return &Interval{Start: s.Start.UTC(), End: s.End.UTC()}
}

// Verifier asynchronously checks which assets actually exist in the
// warehouse; assets reported false are downgraded from fresh to missing.
type Verifier func(ctx context.Context, selection Selection, assetNames []string) (map[string]bool, error)

// PhysicalTarget is the secret-free result of resolving the physical output
// selected for one asset. Identity is usable for freshness only when Exact is
// true and the identity is non-empty. Runtime-dependent and otherwise unknown
// targets deliberately remain inexact so historical coverage cannot be
// mistaken for evidence about the currently selected output.
type PhysicalTarget struct {
	Identity string
	Exact    bool
	// Fidelity is optional for compatibility with existing resolvers. When it
	// is empty, Exact maps to exact and a present non-exact result maps to
	// runtime_only.
	Fidelity TargetFidelity
}

// TargetResolver resolves selected physical outputs without opening a
// warehouse. Results are keyed by canonical asset ID (pipeline UUID + asset
// name). Missing, empty, or inexact results fail closed for that asset.
//
// The callback is a dependency rather than a service-package import so target
// resolution can share the execution resolver at composition time without
// introducing a staleness/service package cycle.
type TargetResolver func(
	ctx context.Context,
	selection Selection,
	parsed *pipeline.Pipeline,
) (map[string]PhysicalTarget, error)

type Dependencies struct {
	Store   *matlog.Store
	Engine  *fingerprint.Engine
	Resolve matlog.PipelineResolver
	// ResolveTargets enables generation-aware physical-output freshness. It is
	// optional only for legacy callers and tests; production supplies the same
	// selected-context resolver used before execution.
	ResolveTargets TargetResolver
	// Publish pushes staleness.updated events to SSE clients.
	Publish func(any)
	// Verify is optional trust-but-verify support; it runs async, throttled
	// to once per (pipeline, environment) per session, and never blocks
	// status computation.
	Verify Verifier
	Logger *zap.Logger
}

type Service struct {
	deps Dependencies

	mu sync.Mutex
	// selections holds the last-requested selection per pipeline UUID; bus
	// events recompute exactly these.
	selections map[string]Selection
	// snapshots caches the last computed result per pipeline UUID.
	snapshots map[string]Snapshot
	// verified tracks (pipelineUUID, environment) pairs already verified
	// this session; missing tables found are remembered until the next run.
	verified       map[string]bool
	missingByPanel map[string]map[string]bool // panelKey -> assetName -> missing
}

func New(deps Dependencies) *Service {
	return &Service{
		deps:           deps,
		selections:     make(map[string]Selection),
		snapshots:      make(map[string]Snapshot),
		verified:       make(map[string]bool),
		missingByPanel: make(map[string]map[string]bool),
	}
}

// AttachBus subscribes the service to the process event bus.
func (s *Service) AttachBus(events bus.Events) {
	events.OnAssetSaved(func(event bus.AssetSaved) {
		s.recomputePipeline(event.PipelineUUID, "asset saved")
	})
	events.OnRunCompleted(func(event bus.RunCompleted) error {
		s.clearMissingAfterSuccessfulRun(event)
		s.recomputePipeline(event.PipelineUUID, "run completed")
		return nil
	})
	events.OnTargetWriteChanged(func(event bus.TargetWriteChanged) {
		// Claim acquisition is on the physical execution's critical path. Publish
		// the fail-closed snapshot asynchronously so warehouse work is not delayed
		// by fingerprinting, while dirty claims still reach the UI even when no
		// completion event can be persisted.
		go s.recomputePipeline(event.PipelineUUID, "target write changed")
	})
}

func (s *Service) clearMissingAfterSuccessfulRun(event bus.RunCompleted) {
	key := verifyKey(Selection{
		PipelineUUID: event.PipelineUUID,
		Environment:  event.Environment,
	})
	s.mu.Lock()
	defer s.mu.Unlock()

	missing := s.missingByPanel[key]
	succeeded := false
	for _, asset := range event.Assets {
		if asset.Status != "succeeded" || strings.TrimSpace(asset.AssetName) == "" {
			continue
		}
		succeeded = true
		delete(missing, asset.AssetName)
	}
	if !succeeded {
		return
	}
	if missing != nil && len(missing) == 0 {
		delete(s.missingByPanel, key)
	}
	// The completed write invalidates the old warehouse observation. Allow the
	// next panel fetch to verify the newly written output once.
	delete(s.verified, key)
}

// Statuses preserves the original asset-only API for planners and other
// internal callers. HTTP and SSE use Snapshot so they can also carry the
// data-state token from the same computation.
func (s *Service) Statuses(ctx context.Context, selection Selection) ([]AssetStatus, error) {
	snapshot, err := s.Snapshot(ctx, selection)
	if err != nil {
		return nil, err
	}
	return snapshot.Assets, nil
}

// Snapshot computes and caches the full staleness result for a selection.
// This is the selection-change entry point: target writers and coverage are
// loaded in batches, and both the classifications and token are derived from
// that one view.
func (s *Service) Snapshot(ctx context.Context, selection Selection) (Snapshot, error) {
	snapshot, err := s.compute(ctx, selection)
	if err != nil {
		return Snapshot{}, err
	}

	s.mu.Lock()
	s.selections[selection.PipelineUUID] = selection
	s.snapshots[selection.PipelineUUID] = snapshot
	shouldVerify := s.deps.Verify != nil && !s.verified[verifyKey(selection)]
	if shouldVerify {
		s.verified[verifyKey(selection)] = true
	}
	s.mu.Unlock()

	if shouldVerify {
		go s.runVerification(selection)
	}
	return snapshot, nil
}

// Evaluate computes a staleness snapshot for an already-resolved pipeline
// without changing the UI's cached working-tree selection or starting
// asynchronous warehouse verification. Read-only execution planning uses this
// for immutable deployment sources whose files do not live in the workspace.
func (s *Service) Evaluate(
	ctx context.Context,
	selection Selection,
	parsed *pipeline.Pipeline,
) (Snapshot, error) {
	if parsed == nil {
		return Snapshot{}, fmt.Errorf("staleness: parsed pipeline is required")
	}
	return s.computeParsed(ctx, selection, parsed)
}

func verifyKey(selection Selection) string {
	return selection.PipelineUUID + "\x00" + selection.Environment
}

// recomputePipeline refreshes the cached selection for a pipeline after a
// bus event and publishes the updated statuses.
func (s *Service) recomputePipeline(pipelineUUID, reason string) {
	s.mu.Lock()
	selection, ok := s.selections[pipelineUUID]
	s.mu.Unlock()
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	snapshot, err := s.compute(ctx, selection)
	if err != nil {
		if s.deps.Logger != nil {
			s.deps.Logger.Warn("staleness recompute failed",
				zap.String("pipeline", pipelineUUID), zap.String("trigger", reason), zap.Error(err))
		}
		return
	}

	s.mu.Lock()
	s.snapshots[pipelineUUID] = snapshot
	s.mu.Unlock()
	s.publish(selection, snapshot)
}

func (s *Service) publish(selection Selection, snapshot Snapshot) {
	if s.deps.Publish == nil {
		return
	}
	event := map[string]any{
		"type":             "staleness.updated",
		"pipeline_id":      selection.EncodedPipelineID,
		"pipeline_uuid":    selection.PipelineUUID,
		"environment":      selection.Environment,
		"data_state_token": snapshot.DataStateToken,
		"assets":           snapshot.Assets,
	}
	// The selection's range rides along so clients can discard pushes
	// computed for a selection they have already moved away from.
	if selection.Start != nil {
		event["start"] = selection.Start.UTC().Format(time.RFC3339)
	}
	if selection.End != nil {
		event["end"] = selection.End.UTC().Format(time.RFC3339)
	}
	s.deps.Publish(event)
}

func (s *Service) compute(ctx context.Context, selection Selection) (Snapshot, error) {
	parsed, err := s.deps.Resolve(ctx, selection.PipelineUUID)
	if err != nil {
		return Snapshot{}, err
	}
	return s.computeParsed(ctx, selection, parsed)
}

func (s *Service) computeParsed(ctx context.Context, selection Selection, parsed *pipeline.Pipeline) (Snapshot, error) {
	vars := fingerprint.EffectiveVars(parsed, selection.VarOverrides)
	results, err := s.deps.Engine.DAG(parsed, vars)
	if err != nil {
		return Snapshot{}, err
	}
	varsHash := fingerprint.AllVarsHash(vars)

	assetIDs := make([]string, 0, len(results))
	for id := range results {
		assetIDs = append(assetIDs, id)
	}
	sort.Strings(assetIDs)

	coverageContext, err := s.loadCoverageContext(
		ctx,
		selection,
		parsed,
		assetIDs,
		varsHash,
	)
	if err != nil {
		return Snapshot{}, err
	}
	lastRuns, err := s.deps.Store.LastRuns(ctx, assetIDs, selection.Environment)
	if err != nil {
		return Snapshot{}, err
	}

	s.mu.Lock()
	missing := s.missingByPanel[verifyKey(selection)]
	s.mu.Unlock()

	selectedRange := selection.rangeInterval()
	statuses := make([]AssetStatus, 0, len(parsed.Assets))
	for _, asset := range parsed.Assets {
		assetID := identity.AssetID(selection.PipelineUUID, asset.Name)
		result, ok := results[assetID]
		if !ok {
			continue
		}
		status := classify(
			asset,
			assetID,
			result,
			coverageContext.coverage[assetID],
			coverageContext.anyBuilt[assetID],
			coverageContext.lastOwnContent[assetID],
			selectedRange,
		)
		applyTargetContext(
			&status,
			selection.Environment,
			coverageContext.targets[assetID],
			coverageContext.writers,
		)
		if status.Status != StatusExternal {
			applyLastRun(&status, lastRuns[assetID], result)
		}
		if status.Status == StatusFresh && missing != nil && missing[asset.Name] && verifiableByName(asset) {
			status.Status = StatusMissing
		}
		statuses = append(statuses, status)
	}
	token, err := dataStateToken(selection, varsHash, assetIDs, coverageContext, missing)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{DataStateToken: token, Assets: statuses}, nil
}

type selectedTarget struct {
	Fidelity TargetFidelity
	Identity string
}

type coverageContext struct {
	coverage       map[string][]matlog.CoverageRow
	anyBuilt       map[string]bool
	lastOwnContent map[string]string
	targets        map[string]selectedTarget
	writers        map[string]matlog.LatestSuccessfulWriter
}

func (s *Service) loadCoverageContext(
	ctx context.Context,
	selection Selection,
	parsed *pipeline.Pipeline,
	assetIDs []string,
	varsHash string,
) (coverageContext, error) {
	if s.deps.ResolveTargets == nil {
		coverage, err := s.deps.Store.Coverage(ctx, assetIDs, selection.Environment, varsHash)
		if err != nil {
			return coverageContext{}, err
		}
		anyBuilt, err := s.deps.Store.HasAnyCoverage(ctx, assetIDs, selection.Environment)
		if err != nil {
			return coverageContext{}, err
		}
		lastOwnContent, err := s.deps.Store.LatestOwnContent(ctx, assetIDs, selection.Environment)
		if err != nil {
			return coverageContext{}, err
		}
		targets := make(map[string]selectedTarget, len(assetIDs))
		for _, assetID := range assetIDs {
			targets[assetID] = selectedTarget{Fidelity: TargetFidelityLegacy}
		}
		return coverageContext{
			coverage: coverage, anyBuilt: anyBuilt, lastOwnContent: lastOwnContent,
			targets: targets, writers: map[string]matlog.LatestSuccessfulWriter{},
		}, nil
	}

	resolved, err := s.deps.ResolveTargets(ctx, selection, parsed)
	if err != nil {
		return coverageContext{}, err
	}
	assetTargets := make(map[string]string, len(assetIDs))
	targets := make(map[string]selectedTarget, len(assetIDs))
	targetIdentities := make([]string, 0, len(assetIDs))
	for _, assetID := range assetIDs {
		target, ok := resolved[assetID]
		selected := normalizeSelectedTarget(target, ok)
		targets[assetID] = selected
		if selected.Fidelity != TargetFidelityExact {
			continue
		}
		assetTargets[assetID] = selected.Identity
		targetIdentities = append(targetIdentities, selected.Identity)
	}

	// Claims make writes fail closed, and the second writer read prevents a
	// completion that lands between the batched queries from producing a hybrid
	// snapshot (old latest-output metadata with new-generation coverage, or the
	// inverse). A stable read normally completes on the first attempt.
	for attempt := 0; attempt < 3; attempt++ {
		writers, err := s.deps.Store.LatestWriters(ctx, targetIdentities)
		if err != nil {
			return coverageContext{}, err
		}
		coverage, err := s.deps.Store.CurrentTargetCoverage(
			ctx,
			assetTargets,
			selection.Environment,
			varsHash,
		)
		if err != nil {
			return coverageContext{}, err
		}
		currentOwnContent, err := s.deps.Store.CurrentTargetOwnContent(
			ctx,
			assetTargets,
			selection.Environment,
		)
		if err != nil {
			return coverageContext{}, err
		}
		confirmedWriters, err := s.deps.Store.LatestWriters(ctx, targetIdentities)
		if err != nil {
			return coverageContext{}, err
		}
		if !reflect.DeepEqual(writers, confirmedWriters) {
			continue
		}
		anyBuilt := make(map[string]bool, len(currentOwnContent))
		for assetID := range currentOwnContent {
			anyBuilt[assetID] = true
		}
		return coverageContext{
			coverage: coverage, anyBuilt: anyBuilt, lastOwnContent: currentOwnContent,
			targets: targets, writers: writers,
		}, nil
	}
	return coverageContext{}, fmt.Errorf("staleness: physical target state changed during snapshot computation")
}

func normalizeSelectedTarget(target PhysicalTarget, present bool) selectedTarget {
	if !present {
		return selectedTarget{Fidelity: TargetFidelityUnresolved}
	}
	if target.Exact {
		if target.Identity != "" && strings.TrimSpace(target.Identity) == target.Identity {
			return selectedTarget{Fidelity: TargetFidelityExact, Identity: target.Identity}
		}
		return selectedTarget{Fidelity: TargetFidelityUnresolved}
	}
	fidelity := target.Fidelity
	switch fidelity {
	case TargetFidelityRuntimeOnly, TargetFidelityUnresolved:
		return selectedTarget{Fidelity: fidelity}
	default:
		return selectedTarget{Fidelity: TargetFidelityRuntimeOnly}
	}
}

func applyTargetContext(
	status *AssetStatus,
	environment string,
	target selectedTarget,
	writers map[string]matlog.LatestSuccessfulWriter,
) {
	status.TargetFidelity = target.Fidelity
	status.TargetIdentity = target.Identity
	if target.Fidelity != TargetFidelityExact || target.Identity == "" {
		return
	}
	writer, ok := writers[target.Identity]
	if !ok {
		return
	}
	status.LatestOutput = &LatestPhysicalOutput{
		TargetIdentity:    writer.TargetIdentity,
		TargetGeneration:  writer.TargetGeneration,
		WriterAssetID:     writer.AssetID,
		WriterEnvironment: writer.Environment,
		Fingerprint:       writer.Fingerprint,
		VarsHash:          writer.VarsHash,
		RunID:             writer.RunID,
		SnapshotVersionID: writer.SnapshotVersionID,
		MaterializedAt:    writer.MaterializedAt,
		CompletionID:      writer.CompletionID,
		CompletionOrdinal: writer.CompletionOrdinal,
		Ambiguous:         writer.Ambiguous,
	}
	if status.Status == StatusStaleEdited &&
		!writer.Ambiguous &&
		writer.AssetID == status.AssetID &&
		writer.Environment == environment &&
		writer.SnapshotVersionID != "" {
		status.Status = StatusStaleDeployment
	}
}

const dataStateTokenVersion = "renart-data-state-v1"

type dataStateTokenInput struct {
	Version      string                `json:"version"`
	PipelineUUID string                `json:"pipeline_uuid"`
	Environment  string                `json:"environment"`
	VarsHash     string                `json:"vars_hash"`
	Range        *dataStateTokenRange  `json:"range,omitempty"`
	Assets       []dataStateTokenAsset `json:"assets"`
}

type dataStateTokenRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type dataStateTokenAsset struct {
	AssetID         string                   `json:"asset_id"`
	TargetFidelity  TargetFidelity           `json:"target_fidelity"`
	TargetIdentity  string                   `json:"target_identity,omitempty"`
	Writer          *dataStateTokenWriter    `json:"writer,omitempty"`
	Coverage        []dataStateTokenCoverage `json:"coverage,omitempty"`
	AnyBuilt        bool                     `json:"any_built,omitempty"`
	LastOwnContent  string                   `json:"last_own_content,omitempty"`
	VerifiedMissing bool                     `json:"verified_missing,omitempty"`
}

type dataStateTokenWriter struct {
	TargetGeneration  int64  `json:"target_generation"`
	WriterAssetID     string `json:"writer_asset_id"`
	WriterEnvironment string `json:"writer_environment"`
	Fingerprint       string `json:"fingerprint"`
	VarsHash          string `json:"vars_hash"`
	Ambiguous         bool   `json:"ambiguous"`
}

type dataStateTokenCoverage struct {
	Fingerprint      string `json:"fingerprint"`
	OwnContent       string `json:"own_content"`
	TargetIdentity   string `json:"target_identity,omitempty"`
	TargetGeneration int64  `json:"target_generation,omitempty"`
	IntervalStart    string `json:"interval_start,omitempty"`
	IntervalEnd      string `json:"interval_end,omitempty"`
}

// dataStateToken intentionally excludes run IDs, completion coordinates, and
// materialization timestamps: replacing a writer with the same physical
// variant and unchanged coverage does not change a needed selection. Writer
// generation/scope/variant, ambiguity, and current-generation coverage do.
func dataStateToken(
	selection Selection,
	varsHash string,
	assetIDs []string,
	context coverageContext,
	missing map[string]bool,
) (string, error) {
	input := dataStateTokenInput{
		Version:      dataStateTokenVersion,
		PipelineUUID: selection.PipelineUUID,
		Environment:  selection.Environment,
		VarsHash:     varsHash,
		Assets:       make([]dataStateTokenAsset, 0, len(assetIDs)),
	}
	if selectedRange := selection.rangeInterval(); selectedRange != nil {
		input.Range = &dataStateTokenRange{
			Start: selectedRange.Start.UTC().Format(time.RFC3339Nano),
			End:   selectedRange.End.UTC().Format(time.RFC3339Nano),
		}
	}

	for _, assetID := range assetIDs {
		target := context.targets[assetID]
		_, assetName, _ := identity.SplitAssetID(assetID)
		assetState := dataStateTokenAsset{
			AssetID:         assetID,
			TargetFidelity:  target.Fidelity,
			TargetIdentity:  target.Identity,
			AnyBuilt:        context.anyBuilt[assetID],
			LastOwnContent:  context.lastOwnContent[assetID],
			VerifiedMissing: missing[assetName],
		}
		if writer, ok := context.writers[target.Identity]; ok && target.Fidelity == TargetFidelityExact {
			assetState.Writer = &dataStateTokenWriter{
				TargetGeneration:  writer.TargetGeneration,
				WriterAssetID:     writer.AssetID,
				WriterEnvironment: writer.Environment,
				Fingerprint:       writer.Fingerprint,
				VarsHash:          writer.VarsHash,
				Ambiguous:         writer.Ambiguous,
			}
		}
		rows := context.coverage[assetID]
		assetState.Coverage = make([]dataStateTokenCoverage, 0, len(rows))
		for _, row := range rows {
			coverage := dataStateTokenCoverage{
				Fingerprint:      row.Fingerprint,
				OwnContent:       row.OwnContent,
				TargetIdentity:   row.TargetIdentity,
				TargetGeneration: row.TargetGeneration,
			}
			if row.IntervalStart != nil {
				coverage.IntervalStart = row.IntervalStart.UTC().Format(time.RFC3339Nano)
			}
			if row.IntervalEnd != nil {
				coverage.IntervalEnd = row.IntervalEnd.UTC().Format(time.RFC3339Nano)
			}
			assetState.Coverage = append(assetState.Coverage, coverage)
		}
		sort.Slice(assetState.Coverage, func(i, j int) bool {
			left, right := assetState.Coverage[i], assetState.Coverage[j]
			if left.TargetIdentity != right.TargetIdentity {
				return left.TargetIdentity < right.TargetIdentity
			}
			if left.TargetGeneration != right.TargetGeneration {
				return left.TargetGeneration < right.TargetGeneration
			}
			if left.Fingerprint != right.Fingerprint {
				return left.Fingerprint < right.Fingerprint
			}
			if left.IntervalStart != right.IntervalStart {
				return left.IntervalStart < right.IntervalStart
			}
			return left.IntervalEnd < right.IntervalEnd
		})
		input.Assets = append(input.Assets, assetState)
	}

	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return dataStateTokenVersion + ":" + hex.EncodeToString(digest[:]), nil
}

// verifiableByName reports whether an asset's freshness can be confirmed by
// looking up a warehouse object named after the asset — the assumption the
// trust-but-verify pass makes. It holds for SQL and seed assets. It does NOT
// hold for database Load assets as well because their table is derived from the
// asset name. File/object Load targets carry destination_object and Python
// assets may still write no queryable table, so those rest on the run fact.
func verifiableByName(asset *pipeline.Asset) bool {
	if isSensorAsset(asset) {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(string(asset.Type))) {
	case "load":
		if strings.EqualFold(strings.TrimSpace(asset.Connection), "local") {
			return false
		}
		destinationObject, _ := asset.Parameters.GetString("destination_object")
		return strings.TrimSpace(destinationObject) == ""
	case "python":
		return false
	}
	return true
}

// applyLastRun layers the most recent run attempt onto the base classification.
// It is orthogonal to the base status: a fresh asset can still carry a failed
// last run (an unchanged re-run that failed), and for an edited asset it records
// whether the failing run was on the content still on disk.
func applyLastRun(status *AssetStatus, lastRun matlog.AssetRunRecord, result fingerprint.Result) {
	if lastRun.Status == "" {
		return
	}
	status.LastRunStatus = lastRun.Status
	if !lastRun.RanAt.IsZero() {
		at := lastRun.RanAt
		status.LastRunAt = &at
	}
	status.LastRunOnCurrentContent = lastRun.Fingerprint == string(result.FP)
	if lastRun.QualityStatus != "" {
		status.QualityStatus = lastRun.QualityStatus
		status.FailedChecks = append([]bus.QualityCheckFailure(nil), lastRun.FailedChecks...)
		status.QualityRunID = lastRun.RunID
		status.QualityOnCurrentContent = status.LastRunOnCurrentContent
		if !lastRun.RanAt.IsZero() {
			at := lastRun.RanAt
			status.QualityCheckedAt = &at
		}
	}
}

func classify(asset *pipeline.Asset, assetID string, result fingerprint.Result, rows []matlog.CoverageRow, anyBuilt bool, lastOwnContent string, selectedRange *Interval) AssetStatus {
	status := AssetStatus{
		AssetID:       assetID,
		AssetName:     asset.Name,
		Fingerprint:   string(result.FP),
		IntervalAware: matlog.IntervalAware(asset),
		BackfillSafe:  matlog.BackfillSafe(asset),
	}
	if isSourceAsset(asset) {
		status.Status = StatusExternal
		return status
	}

	currentRows := make([]matlog.CoverageRow, 0, len(rows))
	for _, row := range rows {
		if row.Fingerprint == string(result.FP) {
			currentRows = append(currentRows, row)
			if status.LastMaterializedAt == nil || row.MaterializedAt.After(*status.LastMaterializedAt) {
				at := row.MaterializedAt
				status.LastMaterializedAt = &at
			}
		}
	}
	if isSensorAsset(asset) {
		status.Status = StatusVolatile
		status.Volatile = true
		return status
	}

	if !status.IntervalAware || selectedRange == nil {
		if len(currentRows) > 0 {
			status.Status = StatusFresh
			return status
		}
		return classifyStale(status, anyBuilt, lastOwnContent, result, selectedRange)
	}

	covered, gaps := coverageOfRange(currentRows, *selectedRange)
	status.TotalSeconds = selectedRange.End.Sub(selectedRange.Start).Seconds()
	status.CoveredSeconds = covered.Seconds()
	switch {
	case len(gaps) == 0:
		status.Status = StatusFresh
	case len(currentRows) > 0:
		// The definition is unchanged (rows exist under the current
		// fingerprint) — the selected range just isn't (fully) built. This
		// includes zero coverage: report 0/N built rather than a misleading
		// stale_* state, so switching the time selector to an unbuilt
		// interval reads as "not built for this range".
		status.Status = StatusPartial
		status.Gaps = gaps
	default:
		return classifyStale(status, anyBuilt, lastOwnContent, result, selectedRange)
	}
	return status
}

func isSensorAsset(asset *pipeline.Asset) bool {
	return asset != nil && strings.Contains(strings.ToLower(strings.TrimSpace(string(asset.Type))), ".sensor.")
}

func isSourceAsset(asset *pipeline.Asset) bool {
	return asset != nil && strings.HasSuffix(strings.ToLower(strings.TrimSpace(string(asset.Type))), ".source")
}

func classifyStale(status AssetStatus, anyBuilt bool, lastOwnContent string, result fingerprint.Result, selectedRange *Interval) AssetStatus {
	if !anyBuilt {
		status.Status = StatusNeverBuilt
	} else if lastOwnContent != "" && lastOwnContent == string(result.OwnContent) {
		status.Status = StatusStaleUpstream
	} else {
		status.Status = StatusStaleEdited
	}
	if selectedRange != nil && status.IntervalAware {
		status.Gaps = []Interval{*selectedRange}
		status.TotalSeconds = selectedRange.End.Sub(selectedRange.Start).Seconds()
	}
	return status
}

// coverageOfRange intersects the (already merged, disjoint) coverage rows
// with the selected range, returning the covered duration and the uncovered
// gaps in order.
func coverageOfRange(rows []matlog.CoverageRow, selected Interval) (time.Duration, []Interval) {
	intervals := make([]Interval, 0, len(rows))
	for _, row := range rows {
		if row.IntervalStart == nil || row.IntervalEnd == nil {
			// A full-refresh marker under an interval-aware classification
			// covers everything (e.g. the asset became interval-aware later).
			return selected.End.Sub(selected.Start), nil
		}
		start := row.IntervalStart.UTC()
		end := row.IntervalEnd.UTC()
		if !end.After(selected.Start) || !selected.End.After(start) {
			continue
		}
		if start.Before(selected.Start) {
			start = selected.Start
		}
		if end.After(selected.End) {
			end = selected.End
		}
		intervals = append(intervals, Interval{Start: start, End: end})
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].Start.Before(intervals[j].Start) })

	var covered time.Duration
	gaps := make([]Interval, 0)
	cursor := selected.Start
	for _, interval := range intervals {
		if interval.Start.After(cursor) {
			gaps = append(gaps, Interval{Start: cursor, End: interval.Start})
		}
		covered += interval.End.Sub(interval.Start)
		if interval.End.After(cursor) {
			cursor = interval.End
		}
	}
	if cursor.Before(selected.End) {
		gaps = append(gaps, Interval{Start: cursor, End: selected.End})
	}
	return covered, gaps
}

// runVerification fires the async trust-but-verify pass and republishes if
// any fresh asset turns out to be missing from the warehouse.
func (s *Service) runVerification(selection Selection) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	s.mu.Lock()
	statuses := s.snapshots[selection.PipelineUUID].Assets
	s.mu.Unlock()

	freshNames := make([]string, 0, len(statuses))
	for _, status := range statuses {
		if status.Status == StatusFresh || status.Status == StatusPartial {
			freshNames = append(freshNames, status.AssetName)
		}
	}
	if len(freshNames) == 0 {
		return
	}

	exists, err := s.deps.Verify(ctx, selection, freshNames)
	if err != nil {
		if s.deps.Logger != nil {
			s.deps.Logger.Warn("staleness verification failed", zap.String("pipeline", selection.PipelineUUID), zap.Error(err))
		}
		return
	}

	missing := make(map[string]bool)
	for name, present := range exists {
		if !present {
			missing[name] = true
		}
	}
	if len(missing) == 0 {
		return
	}

	s.mu.Lock()
	s.missingByPanel[verifyKey(selection)] = missing
	s.mu.Unlock()
	s.recomputePipeline(selection.PipelineUUID, "verification")
}
