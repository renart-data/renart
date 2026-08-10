package matlog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"go.uber.org/zap"
	"renart/internal/web/bus"
	"renart/internal/web/fingerprint"
	"renart/internal/web/identity"
)

var executionWindowReference = regexp.MustCompile(`(?i)\{\{[-\s]*(?:start|end)_(?:date|date_nodash|datetime|timestamp)\b`)

// PipelineResolver loads the parsed pipeline for a stable pipeline UUID.
type PipelineResolver func(ctx context.Context, pipelineUUID string) (*pipeline.Pipeline, error)

// PathResolver parses a pipeline from an explicit directory (a materialized
// snapshot); used so snapshot runs record the deployed code's fingerprints
// rather than the working tree's.
type PathResolver func(ctx context.Context, pipelineDir string) (*pipeline.Pipeline, error)

// Recorder subscribes to RunCompleted bus events and writes materialization
// facts from pre-execution target/fingerprint evidence when available. Legacy
// events retain the completion-time fingerprint fallback until rebuilt.
type Recorder struct {
	store       *Store
	engine      *fingerprint.Engine
	resolve     PipelineResolver
	resolvePath PathResolver
	logger      *zap.Logger
}

func NewRecorder(store *Store, engine *fingerprint.Engine, resolve PipelineResolver, resolvePath PathResolver, logger *zap.Logger) *Recorder {
	return &Recorder{store: store, engine: engine, resolve: resolve, resolvePath: resolvePath, logger: logger}
}

// HandleRunCompleted is the synchronous bus subscriber. Failures are logged
// and returned so durable crash recovery is acknowledged only after derived
// state commits. Normal inline execution may deliberately ignore the returned
// error until it has a completion outbox; retrying a physical write here would
// be unsafe.
func (r *Recorder) HandleRunCompleted(event bus.RunCompleted) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var parsed *pipeline.Pipeline
	var err error
	capturedV2 := event.ExecutionTargetSnapshotVersion >= executionTargetSnapshotVersionV2
	if !capturedV2 {
		if event.SnapshotDir != "" && r.resolvePath != nil {
			parsed, err = r.resolvePath(ctx, event.SnapshotDir)
		} else {
			parsed, err = r.resolve(ctx, event.PipelineUUID)
		}
		if err != nil {
			r.warn("failed to resolve pipeline for materialization log", event.PipelineUUID, err)
			return fmt.Errorf("resolve pipeline %s for materialization log: %w", event.PipelineUUID, err)
		}
	}

	fingerprintCtx, err := r.fingerprintContext(parsed, event)
	if err != nil {
		r.warn("failed to load execution fingerprint context for materialization log", event.PipelineUUID, err)
		return err
	}
	parsed = fingerprintCtx.parsed
	results := fingerprintCtx.results
	varsHashes := fingerprintCtx.varsHashes
	targetsByID := fingerprintCtx.targetsByID
	captured := fingerprintCtx.captured

	// Record each asset's *achieved* fingerprint — the data it actually
	// produced, folding in the fingerprint of each upstream as physically read
	// — not the current target. Materializing a downstream while an upstream is
	// stale must record as stale, not silently fresh. latest holds each asset's
	// pre-run fingerprint (this run's facts are not yet written), so an upstream
	// not rebuilt here contributes its old fingerprint; one rebuilt here
	// contributes its fresh achieved fingerprint via topo order.
	materializedSucceeded := make(map[string]bool, len(event.Assets))
	assetIDs := make([]string, 0, len(results))
	for id := range results {
		assetIDs = append(assetIDs, id)
	}
	for _, assetRun := range event.Assets {
		if assetRun.Status != "succeeded" {
			continue
		}
		target := targetsByID[assetRun.AssetID]
		if target.TargetWriteEvidenceRequired {
			observed, evidenceErr := r.store.HasTargetWriteEvidence(ctx, TargetWriteClaim{
				TargetIdentity: target.TargetIdentity,
				CompletionID:   event.CompletionID,
				AssetID:        assetRun.AssetID,
			})
			if evidenceErr != nil {
				r.warn("failed to load operator target-write evidence", assetRun.AssetID, evidenceErr)
				return fmt.Errorf("load operator target-write evidence for %s: %w", assetRun.AssetID, evidenceErr)
			}
			if !observed {
				continue
			}
		}
		materializedSucceeded[assetRun.AssetID] = true
	}
	latestAchieved, err := r.latestAchievedLookup(
		ctx, assetIDs, event.Environment, targetsByID, event.Assets,
		captured, event.ExecutionTargetSnapshotVersion,
	)
	if err != nil {
		r.warn("failed to load latest physical writers for materialization log", event.PipelineUUID, err)
		return fmt.Errorf("load latest physical writers for pipeline %s: %w", event.PipelineUUID, err)
	}
	achieved, err := r.engine.AchievedFingerprintsByConsumerResolved(
		parsed,
		results,
		materializedSucceeded,
		fingerprintCtx.resolveUpstream,
		latestAchieved,
	)
	if err != nil {
		r.warn("failed to compute achieved fingerprints for materialization log", event.PipelineUUID, err)
		return fmt.Errorf("compute achieved fingerprints for pipeline %s: %w", event.PipelineUUID, err)
	}

	var recordErrs []error
	for _, assetRun := range event.Assets {
		if !materializedSucceeded[assetRun.AssetID] {
			continue
		}
		result, ok := results[assetRun.AssetID]
		if !ok {
			continue
		}
		achievedFP, ok := achieved[assetRun.AssetID]
		if !ok {
			continue
		}
		materializedAt := assetRunCompletionTime(assetRun, event.CompletedAt)
		materialization := Materialization{
			AssetID:           assetRun.AssetID,
			Environment:       event.Environment,
			Fingerprint:       string(achievedFP),
			OwnContent:        string(result.OwnContent),
			VarsHash:          varsHashes[assetRun.AssetID],
			RunID:             event.RunID,
			SnapshotVersionID: event.SnapshotVersionID,
			TargetIdentity:    targetsByID[assetRun.AssetID].TargetIdentity,
			CompletionID:      event.CompletionID,
			CompletionOrdinal: assetRun.CompletionOrdinal,
			MaterializedAt:    materializedAt,
		}
		asset := parsed.GetAssetByName(assetRun.AssetName)
		behavior := fingerprintCtx.coverageBehavior(assetRun.AssetID, asset)
		effectiveFullRefresh := event.FullRefresh && !fingerprintCtx.refreshRestricted(assetRun.AssetID, asset)
		if behavior != coverageMarker {
			if event.WinStart == nil || event.WinEnd == nil {
				r.warn("skipping interval materialization fact without a complete run window", assetRun.AssetID, nil)
				continue
			}
			materialization.IntervalStart = event.WinStart
			materialization.IntervalEnd = event.WinEnd
			materialization.ReplaceCoverage = behavior == coverageReplaceInterval || effectiveFullRefresh
		} else if effectiveFullRefresh {
			materialization.ReplaceCoverage = true
		}
		if err := r.store.Record(ctx, materialization); err != nil {
			r.warn("failed to record materialization", assetRun.AssetID, err)
			if !errors.Is(err, ErrTargetWriterAmbiguous) {
				recordErrs = append(recordErrs, fmt.Errorf("record materialization for %s: %w", assetRun.AssetID, err))
			}
		}
	}

	// Record the latest run attempt (success or failure) per asset. The facts
	// above only capture successes; this lets the staleness service tell an
	// untested edit from a run that was attempted and failed, and surface an
	// unchanged asset whose last run failed. Fingerprint is the target of what
	// ran, compared later against the asset's current fingerprint.
	for _, assetRun := range event.Assets {
		result, ok := results[assetRun.AssetID]
		if !ok {
			continue
		}
		if err := r.store.RecordRun(ctx, AssetRunRecord{
			AssetID:       assetRun.AssetID,
			Environment:   event.Environment,
			Fingerprint:   string(result.FP),
			Status:        assetRun.Status,
			RunID:         event.RunID,
			RanAt:         assetRunCompletionTime(assetRun, event.CompletedAt),
			QualityStatus: assetRun.QualityStatus,
			FailedChecks:  append([]bus.QualityCheckFailure(nil), assetRun.FailedChecks...),
		}); err != nil {
			r.warn("failed to record run attempt", assetRun.AssetID, err)
			recordErrs = append(recordErrs, fmt.Errorf("record run attempt for %s: %w", assetRun.AssetID, err))
		}
	}
	return errors.Join(recordErrs...)
}

const (
	executionTargetSnapshotVersionV1 = 1
	executionTargetSnapshotVersionV2 = 2
	executionTargetSnapshotVersionV3 = 3
	executionTargetSnapshotVersionV4 = 4
	executionTargetSnapshotVersionV5 = 5
)

type executionFingerprintContext struct {
	parsed              *pipeline.Pipeline
	results             map[string]fingerprint.Result
	varsHashes          map[string]string
	targetsByID         map[string]bus.ExecutionTargetSnapshotEntry
	coverageModes       map[string]string
	refreshRestrictions map[string]bool
	resolvedUpstreams   map[string]map[string]string
	captured            bool
}

func (c executionFingerprintContext) resolveUpstream(
	consumerAssetID string,
	upstream pipeline.Upstream,
) (string, bool, bool) {
	key := strings.ToLower(strings.TrimSpace(upstream.Type)) + "\x00" + strings.TrimSpace(upstream.Value)
	if resolved := c.resolvedUpstreams[consumerAssetID][key]; strings.TrimSpace(resolved) != "" {
		return resolved, c.targetsByID[resolved].ExternalSource, true
	}
	if c.parsed == nil {
		return "", false, false
	}
	asset := c.parsed.GetAssetByName(upstream.Value)
	if asset == nil {
		return "", false, false
	}
	return identity.AssetID(c.parsed.LegacyID, asset.Name), recorderSourceAsset(asset), true
}

func recorderSourceAsset(asset *pipeline.Asset) bool {
	return asset != nil && strings.HasSuffix(strings.ToLower(strings.TrimSpace(string(asset.Type))), ".source")
}

func (c executionFingerprintContext) coverageBehavior(assetID string, fallback *pipeline.Asset) coverageBehavior {
	if mode, ok := c.coverageModes[assetID]; ok {
		switch mode {
		case "marker":
			return coverageMarker
		case "union_intervals":
			return coverageUnionIntervals
		case "replace_interval":
			return coverageReplaceInterval
		}
	}
	return coverageBehaviorFor(fallback)
}

func (c executionFingerprintContext) refreshRestricted(assetID string, fallback *pipeline.Asset) bool {
	if restricted, ok := c.refreshRestrictions[assetID]; ok {
		return restricted
	}
	return refreshRestricted(fallback)
}

func (r *Recorder) fingerprintContext(
	parsed *pipeline.Pipeline,
	event bus.RunCompleted,
) (executionFingerprintContext, error) {
	captured := event.ExecutionTargetSnapshotVersion != 0 || len(event.ExecutionTargets) > 0
	if !captured {
		if parsed == nil {
			return executionFingerprintContext{}, fmt.Errorf("pipeline %s has no parsed source", event.PipelineUUID)
		}
		vars := fingerprint.EffectiveVars(parsed, nil)
		results, err := r.engine.DAG(parsed, vars)
		if err != nil {
			return executionFingerprintContext{}, fmt.Errorf("fingerprint pipeline %s for materialization log: %w", event.PipelineUUID, err)
		}
		varsHash := fingerprint.AllVarsHash(vars)
		varsHashes := make(map[string]string, len(results))
		for assetID := range results {
			varsHashes[assetID] = varsHash
		}
		return executionFingerprintContext{
			parsed: parsed, results: results, varsHashes: varsHashes,
			targetsByID: map[string]bus.ExecutionTargetSnapshotEntry{}, captured: false,
		}, nil
	}
	if event.ExecutionTargetSnapshotVersion != executionTargetSnapshotVersionV1 &&
		event.ExecutionTargetSnapshotVersion != executionTargetSnapshotVersionV2 &&
		event.ExecutionTargetSnapshotVersion != executionTargetSnapshotVersionV3 &&
		event.ExecutionTargetSnapshotVersion != executionTargetSnapshotVersionV4 &&
		event.ExecutionTargetSnapshotVersion != executionTargetSnapshotVersionV5 {
		return executionFingerprintContext{}, fmt.Errorf("pipeline %s has unsupported execution target snapshot version %d", event.PipelineUUID, event.ExecutionTargetSnapshotVersion)
	}
	if len(event.ExecutionTargets) == 0 {
		return executionFingerprintContext{}, fmt.Errorf("pipeline %s execution target snapshot is empty", event.PipelineUUID)
	}
	if strings.TrimSpace(event.CompletionID) == "" {
		return executionFingerprintContext{}, fmt.Errorf("pipeline %s execution target snapshot has no completion identity", event.PipelineUUID)
	}

	pipelineUUID := strings.TrimSpace(event.PipelineUUID)
	if pipelineUUID == "" {
		return executionFingerprintContext{}, fmt.Errorf("execution target snapshot has no pipeline identity")
	}
	if event.ExecutionTargetSnapshotVersion >= executionTargetSnapshotVersionV2 {
		if strings.TrimSpace(event.ExecutionPipelineUUID) != pipelineUUID {
			return executionFingerprintContext{}, fmt.Errorf("execution target snapshot does not match completion pipeline identity")
		}
		parsed = pipelineFromExecutionSnapshot(pipelineUUID, event.ExecutionTargets)
	} else if parsed == nil || strings.TrimSpace(parsed.LegacyID) != pipelineUUID {
		return executionFingerprintContext{}, fmt.Errorf("pipeline execution target snapshot does not match parsed pipeline identity")
	}
	if event.ExecutionTargetSnapshotVersion == executionTargetSnapshotVersionV1 {
		if err := r.validateLegacySnapshotSource(parsed, event.ExecutionTargets); err != nil {
			return executionFingerprintContext{}, err
		}
	}
	results := make(map[string]fingerprint.Result, len(parsed.Assets))
	varsHashes := make(map[string]string, len(parsed.Assets))
	targetsByID := make(map[string]bus.ExecutionTargetSnapshotEntry, len(parsed.Assets))
	coverageModes := make(map[string]string, len(parsed.Assets))
	refreshRestrictions := make(map[string]bool, len(parsed.Assets))
	resolvedUpstreams := make(map[string]map[string]string, len(parsed.Assets))
	commonVarsHash := ""
	for _, asset := range parsed.Assets {
		if asset == nil || strings.TrimSpace(asset.Name) == "" {
			return executionFingerprintContext{}, fmt.Errorf("pipeline %s contains an unnamed asset", pipelineUUID)
		}
		entry, ok := event.ExecutionTargets[asset.Name]
		if !ok {
			return executionFingerprintContext{}, fmt.Errorf("pipeline %s execution target snapshot has no entry for %s", pipelineUUID, asset.Name)
		}
		expectedID := identity.AssetID(pipelineUUID, asset.Name)
		if entry.AssetID != expectedID {
			return executionFingerprintContext{}, fmt.Errorf("execution target snapshot asset identity does not match %s", asset.Name)
		}
		if err := validateCapturedExecutionTarget(asset.Name, entry, event.ExecutionTargetSnapshotVersion); err != nil {
			return executionFingerprintContext{}, err
		}
		if commonVarsHash == "" {
			commonVarsHash = entry.VarsHash
		} else if entry.VarsHash != commonVarsHash {
			return executionFingerprintContext{}, fmt.Errorf("pipeline %s execution target snapshot has inconsistent variables hashes", pipelineUUID)
		}
		results[entry.AssetID] = fingerprint.Result{
			FP:               fingerprint.Fingerprint(entry.Fingerprint),
			OwnContent:       fingerprint.Fingerprint(entry.OwnContent),
			ConsumedVarsHash: entry.ConsumedVarsHash,
		}
		for _, upstream := range entry.Upstreams {
			if strings.TrimSpace(upstream.ResolvedAssetID) == "" {
				continue
			}
			if resolvedUpstreams[entry.AssetID] == nil {
				resolvedUpstreams[entry.AssetID] = make(map[string]string)
			}
			key := strings.ToLower(strings.TrimSpace(upstream.Type)) + "\x00" + strings.TrimSpace(upstream.Value)
			resolvedUpstreams[entry.AssetID][key] = upstream.ResolvedAssetID
		}
		varsHashes[entry.AssetID] = entry.VarsHash
		targetsByID[entry.AssetID] = entry
		if event.ExecutionTargetSnapshotVersion >= executionTargetSnapshotVersionV2 {
			coverageModes[entry.AssetID] = entry.CoverageMode
			refreshRestrictions[entry.AssetID] = entry.RefreshRestricted
		}
	}
	if len(targetsByID) != len(event.ExecutionTargets) {
		return executionFingerprintContext{}, fmt.Errorf("pipeline %s execution target snapshot contains assets outside the executed graph", pipelineUUID)
	}
	seenRuns := make(map[string]struct{}, len(event.Assets))
	succeededRuns := make(map[string]bool, len(event.Assets))
	for _, assetRun := range event.Assets {
		if assetRun.Status == "succeeded" {
			succeededRuns[assetRun.AssetID] = true
		}
	}
	for _, assetRun := range event.Assets {
		if _, duplicate := seenRuns[assetRun.AssetName]; duplicate {
			return executionFingerprintContext{}, fmt.Errorf("completed asset %s appears more than once", assetRun.AssetName)
		}
		seenRuns[assetRun.AssetName] = struct{}{}
		entry, ok := event.ExecutionTargets[assetRun.AssetName]
		if !ok || entry.AssetID != assetRun.AssetID {
			return executionFingerprintContext{}, fmt.Errorf("completed asset %s is absent from the execution target snapshot", assetRun.AssetName)
		}
		if assetRun.Fingerprint != entry.Fingerprint || assetRun.OwnContent != entry.OwnContent ||
			assetRun.ConsumedVarsHash != entry.ConsumedVarsHash || assetRun.VarsHash != entry.VarsHash ||
			assetRun.TargetIdentity != entry.TargetIdentity || assetRun.TargetFidelity != entry.TargetFidelity {
			return executionFingerprintContext{}, fmt.Errorf("completed asset %s does not match its execution target snapshot", assetRun.AssetID)
		}
		if assetRun.Status == "succeeded" && (assetRun.FinishedAt == nil || assetRun.FinishedAt.IsZero() || !assetRun.HasCompletionOrdinal) {
			return executionFingerprintContext{}, fmt.Errorf("completed asset %s has incomplete completion coordinates", assetRun.AssetID)
		}
		if assetRun.Status == "succeeded" && event.ExecutionTargetSnapshotVersion >= executionTargetSnapshotVersionV2 {
			if !assetRun.HasUpstreamWriterSnapshot {
				return executionFingerprintContext{}, fmt.Errorf("completed asset %s has no upstream writer snapshot", assetRun.AssetID)
			}
			if err := validateCapturedUpstreamWriters(assetRun, entry, event.ExecutionTargets); err != nil {
				return executionFingerprintContext{}, err
			}
		}
		if assetRun.Status == "succeeded" && event.ExecutionTargetSnapshotVersion == executionTargetSnapshotVersionV1 {
			asset := parsed.GetAssetByName(assetRun.AssetName)
			if asset != nil {
				for _, upstream := range asset.Upstreams {
					entry, inPipeline := event.ExecutionTargets[upstream.Value]
					if inPipeline && !succeededRuns[entry.AssetID] {
						return executionFingerprintContext{}, fmt.Errorf(
							"completed asset %s uses upstream %s but version-one evidence has no upstream writer snapshot",
							assetRun.AssetID,
							entry.AssetID,
						)
					}
				}
			}
		}
	}
	return executionFingerprintContext{
		parsed: parsed, results: results, varsHashes: varsHashes, targetsByID: targetsByID,
		coverageModes: coverageModes, refreshRestrictions: refreshRestrictions,
		resolvedUpstreams: resolvedUpstreams, captured: true,
	}, nil
}

func validateCapturedUpstreamWriters(
	assetRun bus.AssetRun,
	consumer bus.ExecutionTargetSnapshotEntry,
	targets map[string]bus.ExecutionTargetSnapshotEntry,
) error {
	allowed := make(map[string]bus.ExecutionTargetSnapshotEntry)
	reviewed := make(map[string]bus.ExecutionUpstreamSnapshot)
	for _, upstream := range consumer.Upstreams {
		if upstream.Required && strings.TrimSpace(upstream.ResolvedAssetID) != "" {
			allowed[upstream.ResolvedAssetID] = bus.ExecutionTargetSnapshotEntry{
				AssetID: upstream.ResolvedAssetID, TargetIdentity: upstream.TargetIdentity,
				TargetFidelity: "exact",
			}
			reviewed[upstream.ResolvedAssetID] = upstream
			continue
		}
		if entry, ok := targets[upstream.Value]; ok {
			allowed[entry.AssetID] = entry
		}
	}
	for upstreamID, writer := range assetRun.UpstreamWriters {
		target, ok := allowed[upstreamID]
		if !ok {
			return fmt.Errorf("completed asset %s captured writer for a non-upstream asset %s", assetRun.AssetID, upstreamID)
		}
		if writer.AssetID != upstreamID || target.AssetID != upstreamID || target.TargetFidelity != "exact" ||
			writer.TargetIdentity != target.TargetIdentity || strings.TrimSpace(writer.TargetIdentity) != writer.TargetIdentity {
			return fmt.Errorf("completed asset %s has mismatched upstream writer identity for %s", assetRun.AssetID, upstreamID)
		}
		if strings.TrimSpace(writer.Fingerprint) == "" || strings.TrimSpace(writer.VarsHash) == "" ||
			strings.TrimSpace(writer.CompletionID) == "" || strings.TrimSpace(writer.CompletionID) != writer.CompletionID ||
			writer.TargetGeneration <= 0 || writer.CompletionOrdinal < 0 || writer.MaterializedAt.IsZero() {
			return fmt.Errorf("completed asset %s has incomplete upstream writer evidence for %s", assetRun.AssetID, upstreamID)
		}
		if expected, ok := reviewed[upstreamID]; ok &&
			(writer.Fingerprint != expected.ExpectedFingerprint || writer.VarsHash != expected.VarsHash ||
				writer.TargetGeneration != expected.TargetGeneration || writer.CompletionID != expected.CompletionID ||
				writer.CompletionOrdinal != expected.CompletionOrdinal) {
			return fmt.Errorf("completed asset %s has changed cross-pipeline writer evidence for %s", assetRun.AssetID, upstreamID)
		}
	}
	return nil
}

// Version-one snapshots captured target and fingerprint evidence but not the
// executed dependency graph or coverage contract. They are therefore safe to
// replay only while the immutable snapshot/current source still produces the
// exact captured evidence. A changed source must fail closed instead of
// combining old fingerprints with new topology or materialization semantics.
func (r *Recorder) validateLegacySnapshotSource(parsed *pipeline.Pipeline, entries map[string]bus.ExecutionTargetSnapshotEntry) error {
	vars := fingerprint.EffectiveVars(parsed, nil)
	results, err := r.engine.DAG(parsed, vars)
	if err != nil {
		return fmt.Errorf("validate version-one execution target snapshot source: %w", err)
	}
	varsHash := fingerprint.AllVarsHash(vars)
	if len(results) != len(entries) {
		return fmt.Errorf("version-one execution target snapshot no longer matches parsed pipeline source")
	}
	for _, asset := range parsed.Assets {
		if asset == nil {
			return fmt.Errorf("version-one execution target snapshot source contains a nil asset")
		}
		entry, ok := entries[asset.Name]
		if !ok {
			return fmt.Errorf("version-one execution target snapshot no longer matches parsed pipeline source")
		}
		assetID := identity.AssetID(parsed.LegacyID, asset.Name)
		result, ok := results[assetID]
		if !ok || entry.AssetID != assetID || entry.Fingerprint != string(result.FP) ||
			entry.OwnContent != string(result.OwnContent) || entry.ConsumedVarsHash != result.ConsumedVarsHash ||
			entry.VarsHash != varsHash {
			return fmt.Errorf("version-one execution target snapshot no longer matches parsed pipeline source")
		}
	}
	return nil
}

func validateCapturedExecutionTarget(assetName string, entry bus.ExecutionTargetSnapshotEntry, version int) error {
	if strings.TrimSpace(entry.TargetIdentity) != entry.TargetIdentity {
		return fmt.Errorf("execution target snapshot entry %s has a non-canonical target identity", assetName)
	}
	for field, value := range map[string]string{
		"fingerprint": entry.Fingerprint, "own content": entry.OwnContent,
		"consumed variables hash": entry.ConsumedVarsHash, "variables hash": entry.VarsHash,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("execution target snapshot entry %s has no %s", assetName, field)
		}
	}
	switch entry.TargetFidelity {
	case "exact":
	case "runtime_only":
		if entry.TargetIdentity != "" {
			return fmt.Errorf("runtime-only execution target snapshot entry %s claims an exact identity", assetName)
		}
	default:
		return fmt.Errorf("execution target snapshot entry %s has unsupported fidelity %q", assetName, entry.TargetFidelity)
	}
	if entry.TargetWriteEvidenceRequired && (entry.TargetFidelity != "exact" || entry.TargetIdentity == "") {
		return fmt.Errorf("execution target snapshot entry %s requires write evidence without an exact target", assetName)
	}
	if version < executionTargetSnapshotVersionV3 {
		if entry.WriteResourceKind != "" || entry.WriteResourceIdentity != "" || entry.WriteResourceFidelity != "" {
			return fmt.Errorf("execution target snapshot entry %s contains write-resource evidence before version three", assetName)
		}
	} else if err := validateCapturedWriteResource(assetName, entry); err != nil {
		return err
	}
	if version < executionTargetSnapshotVersionV4 {
		if !bus.ExecutionContractIsEmpty(entry.ExecutionContract) {
			return fmt.Errorf("execution target snapshot entry %s contains an execution contract before version four", assetName)
		}
	} else if err := bus.ValidateExecutionContract(assetName, entry); err != nil {
		return fmt.Errorf("execution target snapshot entry %s has an invalid execution contract: %w", assetName, err)
	}
	if version < executionTargetSnapshotVersionV5 && entry.ExternalSource {
		return fmt.Errorf("execution target snapshot entry %s identifies an external source before version five", assetName)
	}
	if version >= executionTargetSnapshotVersionV2 {
		switch entry.CoverageMode {
		case "marker", "union_intervals", "replace_interval":
		default:
			return fmt.Errorf("execution target snapshot entry %s has unsupported coverage mode %q", assetName, entry.CoverageMode)
		}
		for _, upstream := range entry.Upstreams {
			if strings.TrimSpace(upstream.Type) != upstream.Type || strings.TrimSpace(upstream.Value) == "" || strings.TrimSpace(upstream.Value) != upstream.Value {
				return fmt.Errorf("execution target snapshot entry %s has a non-canonical upstream", assetName)
			}
			if upstream.Required &&
				(strings.TrimSpace(upstream.ResolvedAssetID) == "" || strings.TrimSpace(upstream.TargetIdentity) == "" ||
					strings.TrimSpace(upstream.ExpectedFingerprint) == "" || strings.TrimSpace(upstream.VarsHash) == "" ||
					upstream.TargetGeneration < 1 || strings.TrimSpace(upstream.CompletionID) == "") {
				return fmt.Errorf("execution target snapshot entry %s has an incomplete required upstream", assetName)
			}
		}
	}
	return nil
}

func validateCapturedWriteResource(assetName string, entry bus.ExecutionTargetSnapshotEntry) error {
	kind := strings.TrimSpace(entry.WriteResourceKind)
	identity := strings.TrimSpace(entry.WriteResourceIdentity)
	fidelity := strings.TrimSpace(entry.WriteResourceFidelity)
	if kind != entry.WriteResourceKind || identity != entry.WriteResourceIdentity || fidelity != entry.WriteResourceFidelity {
		return fmt.Errorf("execution target snapshot entry %s has a non-canonical write resource", assetName)
	}
	switch fidelity {
	case "exact":
		switch kind {
		case "none":
			if identity != "" {
				return fmt.Errorf("execution target snapshot entry %s no-write resource claims an identity", assetName)
			}
		case "local_file", "duckdb_database", "warehouse_relation":
			if len(identity) != 64 {
				return fmt.Errorf("execution target snapshot entry %s has an invalid write-resource identity", assetName)
			}
			for _, char := range identity {
				if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
					return fmt.Errorf("execution target snapshot entry %s has an invalid write-resource identity", assetName)
				}
			}
		default:
			return fmt.Errorf("execution target snapshot entry %s has unsupported exact write-resource kind %q", assetName, kind)
		}
	case "runtime_only":
		if kind != "pipeline" || identity != "" {
			return fmt.Errorf("execution target snapshot entry %s runtime write resource is not pipeline-scoped", assetName)
		}
	default:
		return fmt.Errorf("execution target snapshot entry %s has unsupported write-resource fidelity %q", assetName, fidelity)
	}
	return nil
}

func pipelineFromExecutionSnapshot(pipelineUUID string, entries map[string]bus.ExecutionTargetSnapshotEntry) *pipeline.Pipeline {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	assets := make([]*pipeline.Asset, 0, len(names))
	for _, name := range names {
		entry := entries[name]
		asset := &pipeline.Asset{Name: name}
		asset.Upstreams = make([]pipeline.Upstream, 0, len(entry.Upstreams))
		for _, upstream := range entry.Upstreams {
			mode := pipeline.UpstreamModeFull
			if strings.EqualFold(strings.TrimSpace(upstream.Mode), "symbolic") {
				mode = pipeline.UpstreamModeSymbolic
			}
			asset.Upstreams = append(asset.Upstreams, pipeline.Upstream{
				Type: upstream.Type, Value: upstream.Value, Mode: mode,
			})
		}
		assets = append(assets, asset)
	}
	return &pipeline.Pipeline{LegacyID: pipelineUUID, Assets: assets}
}

func (r *Recorder) latestAchievedLookup(
	ctx context.Context,
	assetIDs []string,
	environment string,
	targetsByID map[string]bus.ExecutionTargetSnapshotEntry,
	assetRuns []bus.AssetRun,
	captured bool,
	snapshotVersion int,
) (func(string, string) (fingerprint.Fingerprint, bool), error) {
	if !captured || snapshotVersion < executionTargetSnapshotVersionV2 {
		latest, err := r.store.LatestFingerprint(ctx, assetIDs, environment)
		if err != nil {
			return nil, err
		}
		return func(_, upstreamAssetID string) (fingerprint.Fingerprint, bool) {
			fp, ok := latest[upstreamAssetID]
			return fingerprint.Fingerprint(fp), ok
		}, nil
	}
	runsByID := make(map[string]bus.AssetRun, len(assetRuns))
	for _, assetRun := range assetRuns {
		runsByID[assetRun.AssetID] = assetRun
	}
	return func(consumerAssetID, upstreamAssetID string) (fingerprint.Fingerprint, bool) {
		consumer, ok := runsByID[consumerAssetID]
		if !ok || !consumer.HasUpstreamWriterSnapshot {
			return "", false
		}
		writer, ok := consumer.UpstreamWriters[upstreamAssetID]
		if !ok {
			return "", false
		}
		if writer.AssetID != upstreamAssetID || strings.TrimSpace(writer.TargetIdentity) == "" {
			return "", false
		}
		return fingerprint.Fingerprint(writer.Fingerprint), true
	}, nil
}

func assetRunCompletionTime(assetRun bus.AssetRun, fallback time.Time) time.Time {
	if assetRun.FinishedAt != nil && !assetRun.FinishedAt.IsZero() {
		return assetRun.FinishedAt.UTC()
	}
	return fallback.UTC()
}

func (r *Recorder) warn(message, subject string, err error) {
	if r.logger != nil {
		r.logger.Warn(message, zap.String("subject", subject), zap.Error(err))
	}
}

type coverageBehavior uint8

const (
	coverageMarker coverageBehavior = iota
	coverageUnionIntervals
	coverageReplaceInterval
)

// IntervalAware reports whether the physical result represents a bounded run
// window. This drives staleness coverage display; it does not by itself mean
// independent windows can safely be accumulated by scheduler catch-up.
func IntervalAware(asset *pipeline.Asset) bool {
	return coverageBehaviorFor(asset) != coverageMarker
}

// BackfillSafe reports whether independent windows are replay-safe and can be
// unioned into cumulative coverage. The scheduler uses this narrower contract
// before enabling catch-up backfills.
func BackfillSafe(asset *pipeline.Asset) bool {
	return coverageBehaviorFor(asset) == coverageUnionIntervals
}

func coverageBehaviorFor(asset *pipeline.Asset) coverageBehavior {
	if asset == nil {
		return coverageMarker
	}
	assetType := strings.ToLower(strings.TrimSpace(string(asset.Type)))
	if assetType == "load" || asset.Type == pipeline.AssetTypePython {
		return coverageMarker
	}
	if assetType == "api" {
		if !executionWindowReference.MatchString(apiAssetContent(asset)) {
			return coverageMarker
		}
		if asset.Materialization.Strategy == pipeline.MaterializationStrategyMerge && len(asset.ColumnNamesWithPrimaryKey()) > 0 {
			return coverageUnionIntervals
		}
		return coverageReplaceInterval
	}
	if asset.Materialization.Strategy == pipeline.MaterializationStrategyTimeInterval {
		return coverageUnionIntervals
	}
	return coverageMarker
}

func refreshRestricted(asset *pipeline.Asset) bool {
	return asset != nil && asset.RefreshRestricted != nil && *asset.RefreshRestricted
}

func apiAssetContent(asset *pipeline.Asset) string {
	if asset == nil {
		return ""
	}
	if asset.ExecutableFile.Content != "" {
		return asset.ExecutableFile.Content
	}
	path := asset.ExecutableFile.Path
	if path == "" {
		path = asset.DefinitionFile.Path
	}
	if data, err := os.ReadFile(path); err == nil {
		return string(data)
	}
	return ""
}
