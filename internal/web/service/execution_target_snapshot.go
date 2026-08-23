package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"

	"renart/internal/web/dependencygraph"
	"renart/internal/web/fingerprint"
	"renart/internal/web/identity"
	"renart/internal/web/matlog"
)

func (e *HybridBruinExecutor) resolveExecutionTargetSnapshot(
	pl *pipeline.Pipeline,
	cfg *config.Config,
	selectedAssets []*pipeline.Asset,
) (ExecutionTargetSnapshot, error) {
	return e.resolveExecutionTargetSnapshotForSelection(pl, cfg, selectedAssets, selectedAssets)
}

func (e *HybridBruinExecutor) resolveExecutionTargetSnapshotForSelection(
	pl *pipeline.Pipeline,
	cfg *config.Config,
	targetAssets []*pipeline.Asset,
	configurationAssets []*pipeline.Asset,
) (ExecutionTargetSnapshot, error) {
	return e.resolveExecutionTargetSnapshotForReviewedSelection(
		pl, cfg, targetAssets, configurationAssets, nil,
	)
}

func (e *HybridBruinExecutor) resolveExecutionTargetSnapshotForReviewedSelection(
	pl *pipeline.Pipeline,
	cfg *config.Config,
	targetAssets []*pipeline.Asset,
	configurationAssets []*pipeline.Asset,
	reviewedPrerequisites []PipelinePlanPrerequisite,
) (ExecutionTargetSnapshot, error) {
	if pl == nil {
		return ExecutionTargetSnapshot{}, fmt.Errorf("pipeline is required")
	}
	pipelineID := strings.TrimSpace(pl.LegacyID)
	if pipelineID == "" {
		return ExecutionTargetSnapshot{}, fmt.Errorf("pipeline %q has no stable id", pl.Name)
	}

	vars := fingerprint.EffectiveVars(pl, nil)
	engine := e.fingerprintEngine
	if engine == nil {
		engine = fingerprint.NewEngine()
	}
	snapshotBoundPrerequisites, err := reviewedPrerequisiteSnapshotBinding(reviewedPrerequisites)
	if err != nil {
		return ExecutionTargetSnapshot{}, err
	}
	results, graph, err := e.executionFingerprintResultsForPrerequisites(
		pl, vars, reviewedPrerequisites, snapshotBoundPrerequisites,
	)
	if err != nil {
		return ExecutionTargetSnapshot{}, err
	}
	reviewedByConsumerURI := make(map[string]PipelinePlanPrerequisite, len(reviewedPrerequisites))
	for _, prerequisite := range reviewedPrerequisites {
		key := executionPrerequisiteKey(prerequisite.ConsumerAssetID, prerequisite.URI)
		if key == "\x00" || prerequisite.Status != PipelinePlanPrerequisiteReady {
			return ExecutionTargetSnapshot{}, fmt.Errorf("reviewed cross-pipeline prerequisite is incomplete")
		}
		if _, duplicate := reviewedByConsumerURI[key]; duplicate {
			return ExecutionTargetSnapshot{}, fmt.Errorf("reviewed cross-pipeline prerequisite is duplicated")
		}
		reviewedByConsumerURI[key] = prerequisite
	}
	selectedForExecution := make(map[string]struct{}, len(configurationAssets))
	for _, asset := range configurationAssets {
		if asset != nil {
			selectedForExecution[asset.Name] = struct{}{}
		}
	}
	usedPrerequisites := make(map[string]struct{}, len(reviewedPrerequisites))

	entries := make(map[string]ExecutionTargetSnapshotEntry, len(targetAssets))
	varsHash := fingerprint.AllVarsHash(vars)
	for _, asset := range targetAssets {
		if asset == nil {
			return ExecutionTargetSnapshot{}, fmt.Errorf("selected asset is nil")
		}
		assetName := asset.Name
		if strings.TrimSpace(assetName) == "" {
			return ExecutionTargetSnapshot{}, fmt.Errorf("selected asset has no name")
		}
		if _, exists := entries[assetName]; exists {
			return ExecutionTargetSnapshot{}, fmt.Errorf("selected asset %q is duplicated", assetName)
		}

		assetID := identity.AssetID(pipelineID, assetName)
		result, ok := results[assetID]
		if !ok {
			return ExecutionTargetSnapshot{}, fmt.Errorf("fingerprint result is missing for asset %q", assetName)
		}
		target := resolveAssetPhysicalTarget(e.workspaceRoot, &directPipelineInfo{
			Pipeline: pl,
			Asset:    asset,
			Config:   cfg,
		})
		executionContract, err := executionContractForAsset(
			e.workspaceRoot,
			cfg,
			pl,
			asset,
			target,
		)
		if err != nil {
			return ExecutionTargetSnapshot{}, err
		}
		entry := ExecutionTargetSnapshotEntry{
			AssetID:                     assetID,
			ExternalSource:              isSourceAssetType(asset.Type),
			TargetIdentity:              target.Identity,
			TargetFidelity:              target.Fidelity,
			TargetWriteEvidenceRequired: pythonTargetWriteEvidenceRequired(asset, target),
			WriteResourceKind:           target.WriteResource.Kind,
			WriteResourceIdentity:       target.WriteResource.Identity,
			WriteResourceFidelity:       target.WriteResource.Fidelity,
			ExecutionContract:           executionContract,
			Fingerprint:                 string(result.FP),
			OwnContent:                  string(result.OwnContent),
			ConsumedVarsHash:            result.ConsumedVarsHash,
			VarsHash:                    varsHash,
			CoverageMode:                executionCoverageMode(asset),
			RefreshRestricted:           asset.RefreshRestricted != nil && *asset.RefreshRestricted,
		}
		entry.Upstreams = make([]ExecutionUpstreamSnapshot, 0, len(asset.Upstreams))
		for _, upstream := range asset.Upstreams {
			upstreamSnapshot := ExecutionUpstreamSnapshot{
				Type: upstream.Type, Value: upstream.Value, Mode: upstream.Mode.String(),
			}
			edge, resolved := executionWorkspaceEdge(graph, assetID, upstream)
			if resolved {
				upstreamSnapshot.ResolvedAssetID = edge.ProducerID
			}
			_, selected := selectedForExecution[asset.Name]
			if selected && strings.EqualFold(strings.TrimSpace(upstream.Type), "uri") &&
				upstream.Mode != pipeline.UpstreamModeSymbolic {
				key := executionPrerequisiteKey(assetID, strings.TrimSpace(upstream.Value))
				prerequisite, reviewed := reviewedByConsumerURI[key]
				if !reviewed || (!snapshotBoundPrerequisites && (!resolved || prerequisite.ProducerAssetID != edge.ProducerID)) {
					return ExecutionTargetSnapshot{}, fmt.Errorf(
						"asset %s has no matching reviewed prerequisite for URI %q",
						asset.Name,
						strings.TrimSpace(upstream.Value),
					)
				}
				if snapshotBoundPrerequisites {
					if prerequisite.Environment != cfg.SelectedEnvironmentName ||
						strings.TrimSpace(prerequisite.ProducerAssetID) == "" ||
						strings.TrimSpace(prerequisite.ExpectedFingerprint) == "" ||
						strings.TrimSpace(prerequisite.TargetIdentity) == "" ||
						strings.TrimSpace(prerequisite.VarsHash) == "" {
						return ExecutionTargetSnapshot{}, fmt.Errorf("reviewed snapshot prerequisite %q is incomplete", prerequisite.URI)
					}
					upstreamSnapshot.ResolvedAssetID = prerequisite.ProducerAssetID
				} else {
					producer := graph.Nodes[edge.ProducerID]
					producerResult, ok := results[edge.ProducerID]
					if producer == nil || !ok {
						return ExecutionTargetSnapshot{}, fmt.Errorf("reviewed prerequisite producer is unavailable")
					}
					producerVars := fingerprint.EffectiveVars(producer.Pipeline, nil)
					producerTarget := resolveAssetPhysicalTarget(e.workspaceRoot, &directPipelineInfo{
						Pipeline: producer.Pipeline, Asset: producer.Asset, Config: cfg,
					})
					if prerequisite.Environment != cfg.SelectedEnvironmentName ||
						prerequisite.ExpectedFingerprint != string(producerResult.FP) ||
						prerequisite.VarsHash != fingerprint.AllVarsHash(producerVars) ||
						producerTarget.Fidelity != AssetRenderFidelityExact ||
						prerequisite.TargetIdentity != producerTarget.Identity {
						return ExecutionTargetSnapshot{}, fmt.Errorf(
							"cross-pipeline prerequisite %q changed after plan confirmation",
							prerequisite.URI,
						)
					}
				}
				upstreamSnapshot.Required = true
				if snapshotBoundPrerequisites {
					upstreamSnapshot.ProducerPipelineUUID = prerequisite.ProducerPipelineUUID
					upstreamSnapshot.ProducerSnapshotVersionID = prerequisite.ProducerSnapshotVersionID
				}
				upstreamSnapshot.TargetIdentity = prerequisite.TargetIdentity
				upstreamSnapshot.ExpectedFingerprint = prerequisite.ExpectedFingerprint
				upstreamSnapshot.VarsHash = prerequisite.VarsHash
				upstreamSnapshot.TargetGeneration = prerequisite.TargetGeneration
				upstreamSnapshot.CompletionID = prerequisite.WriterCompletionID
				upstreamSnapshot.CompletionOrdinal = prerequisite.WriterCompletionOrdinal
				usedPrerequisites[key] = struct{}{}
			}
			entry.Upstreams = append(entry.Upstreams, upstreamSnapshot)
		}
		entries[assetName] = entry
	}
	if len(usedPrerequisites) != len(reviewedByConsumerURI) {
		return ExecutionTargetSnapshot{}, fmt.Errorf("reviewed cross-pipeline prerequisites do not match the execution selection")
	}

	configurationIdentity := selectedPipelineConfigurationIdentity(
		e.workspaceRoot,
		cfg,
		pl,
		configurationAssets,
	)
	return ExecutionTargetSnapshot{
		Version:               ExecutionTargetSnapshotVersion,
		PipelineUUID:          pipelineID,
		ConfigurationDigest:   configurationIdentity.Digest,
		ConfigurationFidelity: string(configurationIdentity.Fidelity),
		Entries:               entries,
	}, nil
}

func reviewedPrerequisiteSnapshotBinding(prerequisites []PipelinePlanPrerequisite) (bool, error) {
	if len(prerequisites) == 0 {
		return false, nil
	}
	bound := 0
	for _, prerequisite := range prerequisites {
		if strings.TrimSpace(prerequisite.ProducerSnapshotVersionID) != "" {
			bound++
		}
	}
	if bound != 0 && bound != len(prerequisites) {
		return false, fmt.Errorf("reviewed cross-pipeline prerequisites mix working-tree and deployment bindings")
	}
	return bound == len(prerequisites), nil
}

func (e *HybridBruinExecutor) executionFingerprintResultsForPrerequisites(
	pl *pipeline.Pipeline,
	vars fingerprint.Vars,
	prerequisites []PipelinePlanPrerequisite,
	snapshotBound bool,
) (map[string]fingerprint.Result, dependencygraph.Graph, error) {
	if !snapshotBound {
		return e.executionFingerprintResults(pl, vars)
	}
	if e.validateProducerDeployment == nil {
		return nil, dependencygraph.Graph{}, fmt.Errorf("producer deployment validation is unavailable")
	}
	external := make(map[fingerprint.ExternalUpstreamKey]fingerprint.Fingerprint, len(prerequisites))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, prerequisite := range prerequisites {
		if strings.TrimSpace(prerequisite.ProducerPipelineUUID) == "" || prerequisite.ProducerDeploymentOrdinal < 1 {
			return nil, dependencygraph.Graph{}, fmt.Errorf(
				"reviewed snapshot prerequisite %q has invalid producer deployment coordinates",
				prerequisite.URI,
			)
		}
		if err := e.validateProducerDeployment(
			ctx, prerequisite.ProducerPipelineUUID, prerequisite.ProducerSnapshotVersionID,
		); err != nil {
			return nil, dependencygraph.Graph{}, fmt.Errorf(
				"producer deployment %s is no longer executable: %w",
				prerequisite.ProducerSnapshotVersionID, err,
			)
		}
		external[fingerprint.ExternalUpstreamKey{
			ConsumerAssetID: prerequisite.ConsumerAssetID,
			Type:            "uri", Value: prerequisite.URI,
		}] = fingerprint.Fingerprint(prerequisite.ExpectedFingerprint)
	}
	results, err := e.fingerprintEngine.DAGWithExternalFingerprints(pl, vars, external)
	if err != nil {
		return nil, dependencygraph.Graph{}, err
	}
	graph := dependencygraph.Resolve([]dependencygraph.PipelineInput{{
		UUID: strings.TrimSpace(pl.LegacyID), Parsed: pl,
	}})
	return results, graph, nil
}

func (e *HybridBruinExecutor) executionFingerprintResults(
	pl *pipeline.Pipeline,
	vars fingerprint.Vars,
) (map[string]fingerprint.Result, dependencygraph.Graph, error) {
	if e.dependencyGraph == nil {
		results, err := e.fingerprintEngine.DAG(pl, vars)
		return results, dependencygraph.Graph{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	graph, err := e.dependencyGraph(ctx, map[string]*pipeline.Pipeline{strings.TrimSpace(pl.LegacyID): pl})
	if err != nil {
		return nil, dependencygraph.Graph{}, err
	}
	varsByPipeline := workspaceFingerprintVars(graph, strings.TrimSpace(pl.LegacyID), nil)
	varsByPipeline[strings.TrimSpace(pl.LegacyID)] = vars
	results, err := e.fingerprintEngine.WorkspaceDAG(graph, varsByPipeline)
	return results, graph, err
}

func executionWorkspaceEdge(
	graph dependencygraph.Graph,
	consumerID string,
	upstream pipeline.Upstream,
) (dependencygraph.Edge, bool) {
	typeName := strings.ToLower(strings.TrimSpace(upstream.Type))
	if typeName == "" {
		typeName = "asset"
	}
	mode := pipeline.UpstreamModeFull
	if upstream.Mode == pipeline.UpstreamModeSymbolic {
		mode = pipeline.UpstreamModeSymbolic
	}
	for _, edge := range graph.EdgesByConsumer[consumerID] {
		if edge.Type == typeName && edge.Value == strings.TrimSpace(upstream.Value) && edge.Mode == mode {
			return edge, edge.Resolved
		}
	}
	return dependencygraph.Edge{}, false
}

func executionPrerequisiteKey(consumerAssetID, uri string) string {
	return strings.TrimSpace(consumerAssetID) + "\x00" + strings.TrimSpace(uri)
}

func pythonTargetWriteEvidenceRequired(asset *pipeline.Asset, target AssetRenderTarget) bool {
	return asset != nil && asset.Type == pipeline.AssetTypePython &&
		asset.Materialization.Type == pipeline.MaterializationTypeTable &&
		target.Fidelity == AssetRenderFidelityExact && target.Identity != ""
}

func executionCoverageMode(asset *pipeline.Asset) ExecutionCoverageMode {
	if !matlog.IntervalAware(asset) {
		return ExecutionCoverageMarker
	}
	if matlog.BackfillSafe(asset) {
		return ExecutionCoverageUnionIntervals
	}
	return ExecutionCoverageReplaceInterval
}

func (e *HybridBruinExecutor) notifyExecutionTargetsResolved(
	pl *pipeline.Pipeline,
	cfg *config.Config,
	selectedAssets []*pipeline.Asset,
	callback func(ExecutionTargetSnapshot) error,
) error {
	if callback == nil {
		return nil
	}
	snapshot, err := e.resolveExecutionTargetSnapshot(pl, cfg, selectedAssets)
	if err != nil {
		return fmt.Errorf("resolve execution target snapshot: %w", err)
	}
	if err := callback(snapshot); err != nil {
		return fmt.Errorf("persist execution target snapshot: %w", err)
	}
	return nil
}

func (e *HybridBruinExecutor) notifyExecutionTargetsResolvedForSelection(
	pl *pipeline.Pipeline,
	cfg *config.Config,
	targetAssets []*pipeline.Asset,
	configurationAssets []*pipeline.Asset,
	callback func(ExecutionTargetSnapshot) error,
) error {
	if callback == nil {
		return nil
	}
	snapshot, err := e.resolveExecutionTargetSnapshotForSelection(pl, cfg, targetAssets, configurationAssets)
	if err != nil {
		return fmt.Errorf("resolve execution target snapshot: %w", err)
	}
	if err := callback(snapshot); err != nil {
		return fmt.Errorf("persist execution target snapshot: %w", err)
	}
	return nil
}
