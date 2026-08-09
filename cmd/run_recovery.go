package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"renart/internal/web/bus"
	"renart/internal/web/identity"
	webscheduler "renart/internal/web/scheduler"

	"go.uber.org/zap"
)

// replayRecoveredRun rebuilds the canonical materialization/run-attempt state
// from scheduler steps and execution units that were durable before an unclean
// server stop. It emits the same completion events as normal execution, but
// never reruns an asset or replays textual logs.
func (s *webServer) replayRecoveredRun(
	ctx context.Context,
	run webscheduler.PipelineRun,
	steps []webscheduler.PipelineRunStep,
	units []webscheduler.PipelineRunUnit,
) error {
	if s == nil || s.eventBus == nil {
		return nil
	}
	if !run.ExecutionContextResolved {
		// The scheduler normally filters these rows. Keep the replay boundary
		// fail-safe as well: legacy request fields cannot prove which defaults and
		// environment restrictions the interrupted executor actually used.
		if s.logger != nil {
			s.logger.Warn("skipping interrupted run replay without persisted effective context", zap.String("run_id", run.ID))
		}
		return nil
	}
	if s.logger != nil {
		s.logger.Info(
			"replaying persisted execution state for interrupted run",
			zap.String("run_id", run.ID),
			zap.Int("steps", len(steps)),
			zap.Int("units", len(units)),
		)
	}
	type recoveredAsset struct {
		step                 webscheduler.PipelineRunStep
		name                 string
		ledgerAssetID        string
		status               string
		startedAt            *time.Time
		finishedAt           *time.Time
		completionID         string
		completionOrdinal    int64
		hasCompletionOrdinal bool
		winStart             *time.Time
		winEnd               *time.Time
		eventCompletedAt     *time.Time
	}
	stepsByAsset := make(map[string]webscheduler.PipelineRunStep, len(steps))
	for _, step := range steps {
		if assetName := strings.TrimSpace(step.Asset); assetName != "" {
			stepsByAsset[assetName] = step
		}
	}
	recovered := make([]recoveredAsset, 0, max(len(steps), len(units)))
	unitAware := len(units) > 0
	if unitAware {
		for _, unit := range units {
			status, attempted := recoveredUnitAssetRunStatus(unit.Status)
			assetName := strings.TrimSpace(unit.AssetName)
			if !attempted || assetName == "" {
				continue
			}
			winStart, winEnd, err := recoveredUnitWindow(unit)
			if err != nil {
				return fmt.Errorf("recovered run %s: %w", run.ID, err)
			}
			step, hasStep := stepsByAsset[assetName]
			if unit.Status == webscheduler.PipelineRunUnitSuccess && !hasStep {
				return fmt.Errorf(
					"recovered run %s successful unit %d has no persisted asset step",
					run.ID,
					unit.Position,
				)
			}
			startedAt := unit.StartedAt
			finishedAt := unit.FinishedAt
			if hasStep {
				if step.StartedAt != nil {
					startedAt = step.StartedAt
				}
				if step.FinishedAt != nil {
					finishedAt = step.FinishedAt
				}
			}
			recovered = append(recovered, recoveredAsset{
				step:                 step,
				name:                 assetName,
				ledgerAssetID:        strings.TrimSpace(unit.AssetID),
				status:               status,
				startedAt:            startedAt,
				finishedAt:           finishedAt,
				completionID:         recoveredUnitCompletionID(run.ID, unit.Position),
				completionOrdinal:    int64(unit.Position),
				hasCompletionOrdinal: true,
				winStart:             winStart,
				winEnd:               winEnd,
				eventCompletedAt:     unit.FinishedAt,
			})
		}
	} else {
		for index, step := range steps {
			status, terminal := recoveredAssetRunStatus(step.Status)
			assetName := strings.TrimSpace(step.Asset)
			if !terminal || assetName == "" {
				continue
			}
			asset := recoveredAsset{
				step:         step,
				name:         assetName,
				status:       status,
				startedAt:    step.StartedAt,
				finishedAt:   step.FinishedAt,
				completionID: run.ID,
				winStart:     run.WinStart,
				winEnd:       run.WinEnd,
			}
			if step.CompletionOrdinal != nil {
				asset.completionOrdinal = *step.CompletionOrdinal
				asset.hasCompletionOrdinal = true
			} else {
				asset.completionOrdinal = int64(index)
			}
			recovered = append(recovered, asset)
		}
	}
	if len(recovered) == 0 {
		return nil
	}

	pipelineUUID := ""
	snapshotDir := ""
	cleanup := func() {}
	defer func() { cleanup() }()

	targetSnapshot := run.ExecutionTargetSnapshot
	selfContainedSnapshot := targetSnapshot != nil && targetSnapshot.Version >= webscheduler.ExecutionTargetSnapshotVersionV2
	if versionID := strings.TrimSpace(run.SnapshotVersionID); versionID != "" {
		if s.snapshotStore == nil {
			return fmt.Errorf("snapshot store is unavailable for recovered run %s", run.ID)
		}
		snapshot, err := s.snapshotStore.Get(ctx, versionID)
		if err != nil {
			return fmt.Errorf("load snapshot %s for recovered run %s: %w", versionID, run.ID, err)
		}
		pipelineUUID = snapshot.PipelineUUID
		if !selfContainedSnapshot {
			tempDir, err := os.MkdirTemp("", "renart-recovered-snapshot-")
			if err != nil {
				return fmt.Errorf("create recovery snapshot directory: %w", err)
			}
			cleanup = func() { _ = os.RemoveAll(tempDir) }
			if err := s.snapshotStore.MaterializeForExecution(ctx, versionID, tempDir); err != nil {
				return fmt.Errorf("materialize snapshot %s for recovered run %s: %w", versionID, run.ID, err)
			}
			snapshotDir = tempDir
		}
	} else {
		pipelineUUID = strings.TrimSpace(run.PipelineUUID)
		if selfContainedSnapshot {
			capturedUUID := strings.TrimSpace(targetSnapshot.PipelineUUID)
			if capturedUUID == "" || (pipelineUUID != "" && capturedUUID != pipelineUUID) {
				return fmt.Errorf("recovered run %s target snapshot does not match its admitted pipeline identity", run.ID)
			}
			pipelineUUID = capturedUUID
		}
		if pipelineUUID == "" {
			for _, candidate := range s.currentState().Pipelines {
				if candidate.UUID == run.PipelineUUID || (run.PipelineUUID == "" && candidate.ID == run.PipelineID) {
					pipelineUUID = candidate.UUID
					break
				}
			}
		}
		if pipelineUUID == "" {
			return fmt.Errorf("pipeline %s for recovered run %s is not in the current workspace", run.PipelineID, run.ID)
		}
	}
	if targetSnapshot != nil && strings.TrimSpace(targetSnapshot.PipelineUUID) != "" && targetSnapshot.PipelineUUID != pipelineUUID {
		return fmt.Errorf("recovered run %s target snapshot pipeline identity does not match executed source", run.ID)
	}

	assets := make([]bus.AssetRun, 0, len(recovered))
	for _, asset := range recovered {
		runAsset := bus.AssetRun{
			AssetID:    identity.AssetID(pipelineUUID, asset.name),
			AssetName:  asset.name,
			Status:     asset.status,
			StartedAt:  asset.startedAt,
			FinishedAt: asset.finishedAt,
		}
		if unitAware && asset.ledgerAssetID != runAsset.AssetID {
			return fmt.Errorf(
				"recovered run %s unit asset identity does not match %s",
				run.ID,
				asset.name,
			)
		}
		runAsset.CompletionOrdinal = asset.completionOrdinal
		runAsset.HasCompletionOrdinal = asset.hasCompletionOrdinal
		if asset.step.HasUpstreamWriterSnapshot {
			runAsset.UpstreamWriters = make(map[string]bus.UpstreamWriterSnapshot, len(asset.step.UpstreamWriters))
			for assetID, writer := range asset.step.UpstreamWriters {
				runAsset.UpstreamWriters[assetID] = bus.UpstreamWriterSnapshot{
					AssetID:           writer.AssetID,
					TargetIdentity:    writer.TargetIdentity,
					Fingerprint:       writer.Fingerprint,
					VarsHash:          writer.VarsHash,
					TargetGeneration:  writer.TargetGeneration,
					CompletionID:      writer.CompletionID,
					CompletionOrdinal: writer.CompletionOrdinal,
					MaterializedAt:    writer.MaterializedAt,
				}
			}
			runAsset.HasUpstreamWriterSnapshot = true
		}
		if snapshot := run.ExecutionTargetSnapshot; snapshot != nil {
			entry, exists := snapshot.Entries[asset.name]
			if !exists {
				return fmt.Errorf("recovered run %s target snapshot has no entry for %s", run.ID, asset.name)
			}
			if asset.status == "succeeded" && (asset.finishedAt == nil || !asset.hasCompletionOrdinal) {
				return fmt.Errorf("recovered run %s successful step %s has incomplete completion coordinates", run.ID, asset.name)
			}
			if snapshot.Version >= webscheduler.ExecutionTargetSnapshotVersionV2 && asset.status == "succeeded" && !asset.step.HasUpstreamWriterSnapshot {
				return fmt.Errorf("recovered run %s successful step %s has no upstream writer snapshot", run.ID, asset.name)
			}
			expectedAssetID := identity.AssetID(pipelineUUID, asset.name)
			if entry.AssetID != expectedAssetID {
				return fmt.Errorf("recovered run %s target snapshot asset identity does not match %s", run.ID, asset.name)
			}
			runAsset.AssetID = entry.AssetID
			runAsset.TargetIdentity = entry.TargetIdentity
			runAsset.TargetFidelity = entry.TargetFidelity
			runAsset.Fingerprint = entry.Fingerprint
			runAsset.OwnContent = entry.OwnContent
			runAsset.ConsumedVarsHash = entry.ConsumedVarsHash
			runAsset.VarsHash = entry.VarsHash
		}
		assets = append(assets, runAsset)
	}

	completedAt := time.Now().UTC()
	if run.FinishedAt != nil && !run.FinishedAt.IsZero() {
		completedAt = run.FinishedAt.UTC()
	}
	event := bus.RunCompleted{
		RunID:             run.ID,
		CompletionID:      run.ID,
		PipelineUUID:      pipelineUUID,
		Environment:       run.Environment,
		WinStart:          run.WinStart,
		WinEnd:            run.WinEnd,
		FullRefresh:       run.FullRefresh,
		CompletedAt:       completedAt,
		Assets:            assets,
		SnapshotVersionID: run.SnapshotVersionID,
		SnapshotDir:       snapshotDir,
	}
	if snapshot := run.ExecutionTargetSnapshot; snapshot != nil {
		event.ExecutionTargetSnapshotVersion = snapshot.Version
		event.ExecutionPipelineUUID = snapshot.PipelineUUID
		event.ExecutionTargets = make(map[string]bus.ExecutionTargetSnapshotEntry, len(snapshot.Entries))
		for assetName, entry := range snapshot.Entries {
			upstreams := make([]bus.ExecutionUpstreamSnapshot, 0, len(entry.Upstreams))
			for _, upstream := range entry.Upstreams {
				upstreams = append(upstreams, bus.ExecutionUpstreamSnapshot{
					Type: upstream.Type, Value: upstream.Value, Mode: upstream.Mode,
					ResolvedAssetID: upstream.ResolvedAssetID, Required: upstream.Required,
					ProducerPipelineUUID:      upstream.ProducerPipelineUUID,
					ProducerSnapshotVersionID: upstream.ProducerSnapshotVersionID,
					TargetIdentity:            upstream.TargetIdentity, ExpectedFingerprint: upstream.ExpectedFingerprint,
					VarsHash: upstream.VarsHash, TargetGeneration: upstream.TargetGeneration,
					CompletionID: upstream.CompletionID, CompletionOrdinal: upstream.CompletionOrdinal,
				})
			}
			event.ExecutionTargets[assetName] = bus.ExecutionTargetSnapshotEntry{
				AssetID:                     entry.AssetID,
				TargetIdentity:              entry.TargetIdentity,
				TargetFidelity:              entry.TargetFidelity,
				TargetWriteEvidenceRequired: entry.TargetWriteEvidenceRequired,
				WriteResourceKind:           entry.WriteResourceKind,
				WriteResourceIdentity:       entry.WriteResourceIdentity,
				WriteResourceFidelity:       entry.WriteResourceFidelity,
				ExecutionContract:           recoveredExecutionContract(entry.ExecutionContract),
				Fingerprint:                 entry.Fingerprint,
				OwnContent:                  entry.OwnContent,
				ConsumedVarsHash:            entry.ConsumedVarsHash,
				VarsHash:                    entry.VarsHash,
				Upstreams:                   upstreams,
				CoverageMode:                entry.CoverageMode,
				RefreshRestricted:           entry.RefreshRestricted,
			}
		}
	}
	if unitAware {
		for index, asset := range recovered {
			unitEvent := event
			unitEvent.CompletionID = asset.completionID
			unitEvent.WinStart = asset.winStart
			unitEvent.WinEnd = asset.winEnd
			unitEvent.Assets = []bus.AssetRun{assets[index]}
			if asset.eventCompletedAt != nil && !asset.eventCompletedAt.IsZero() {
				unitEvent.CompletedAt = asset.eventCompletedAt.UTC()
			}
			if err := s.eventBus.EmitRunCompleted(unitEvent); err != nil {
				return fmt.Errorf(
					"replay recovered run %s completion %s: %w",
					run.ID,
					asset.completionID,
					err,
				)
			}
		}
	} else if err := s.eventBus.EmitRunCompleted(event); err != nil {
		return fmt.Errorf("replay recovered run %s completion: %w", run.ID, err)
	}
	if s.logger != nil {
		completions := 1
		if unitAware {
			completions = len(recovered)
		}
		s.logger.Info(
			"replayed persisted execution state for interrupted run",
			zap.String("run_id", run.ID),
			zap.Int("assets", len(assets)),
			zap.Int("completions", completions),
		)
	}
	return nil
}

func recoveredExecutionContract(
	contract webscheduler.PipelineRunExecutionContract,
) bus.ExecutionContractSnapshot {
	return bus.ExecutionContractSnapshot{
		AssetID:               contract.AssetID,
		AssetName:             contract.AssetName,
		ConnectionKeys:        append([]string(nil), contract.ConnectionKeys...),
		MutationResources:     recoveredExecutionResources(contract.MutationResources),
		CoordinationResources: recoveredExecutionResources(contract.CoordinationResources),
	}
}

func recoveredExecutionResources(
	resources webscheduler.PipelineRunPlanResources,
) bus.ExecutionResources {
	claims := make([]bus.ExecutionResourceClaim, 0, len(resources.Claims))
	for _, claim := range resources.Claims {
		claims = append(claims, bus.ExecutionResourceClaim{
			Kind: claim.Kind, Identity: claim.Identity,
		})
	}
	return bus.ExecutionResources{
		Isolation: resources.Isolation,
		Claims:    claims,
	}
}

func recoveredAssetRunStatus(status webscheduler.RunStatus) (string, bool) {
	switch status {
	case webscheduler.RunStatusSuccess:
		return "succeeded", true
	case webscheduler.RunStatusFailed:
		return "failed", true
	case webscheduler.RunStatusCancelled:
		return "cancelled", true
	default:
		return "", false
	}
}

func recoveredUnitAssetRunStatus(status webscheduler.PipelineRunUnitStatus) (string, bool) {
	switch status {
	case webscheduler.PipelineRunUnitSuccess:
		return "succeeded", true
	case webscheduler.PipelineRunUnitFailed:
		return "failed", true
	case webscheduler.PipelineRunUnitCancelled:
		return "cancelled", true
	default:
		// Queued units were never attempted, and reconciliation converts them
		// to skipped. Neither state is evidence of an asset run.
		return "", false
	}
}

func recoveredUnitCompletionID(runID string, position int) string {
	return fmt.Sprintf("%s/unit/%d", strings.TrimSpace(runID), position)
}

func recoveredUnitWindow(unit webscheduler.PipelineRunUnit) (*time.Time, *time.Time, error) {
	if unit.Position < 0 {
		return nil, nil, fmt.Errorf("execution unit has invalid position %d", unit.Position)
	}
	start, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(unit.StartDate))
	if err != nil {
		return nil, nil, fmt.Errorf("execution unit %d has an invalid start time", unit.Position)
	}
	end, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(unit.EndDate))
	if err != nil || !start.Before(end) {
		return nil, nil, fmt.Errorf("execution unit %d has an invalid end time", unit.Position)
	}
	start = start.UTC()
	end = end.UTC()
	return &start, &end, nil
}
