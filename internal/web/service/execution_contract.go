package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"

	"renart/internal/web/duckcoord"
	webexecution "renart/internal/web/execution"
	"renart/internal/web/identity"
)

func pipelinePlanExecutionContracts(
	workspaceRoot string,
	cfg *config.Config,
	pl *pipeline.Pipeline,
	plannedAssets []PipelinePlanAsset,
) ([]PipelinePlanExecutionContract, error) {
	if pl == nil {
		return nil, fmt.Errorf("pipeline is required")
	}
	assetByID := make(map[string]*pipeline.Asset, len(pl.Assets))
	for _, asset := range pl.Assets {
		if asset == nil {
			continue
		}
		assetByID[identityForPipelineAsset(pl, asset)] = asset
	}
	result := make([]PipelinePlanExecutionContract, 0, len(plannedAssets))
	for _, planned := range plannedAssets {
		asset := assetByID[planned.ID]
		if asset == nil {
			return nil, fmt.Errorf("planned asset %q was not found", planned.Name)
		}
		if !pipelinePlanAssetIsExecutable(asset) {
			continue
		}
		contract, err := executionContractForAsset(
			workspaceRoot,
			cfg,
			pl,
			asset,
			planned.Target,
		)
		if err != nil {
			return nil, fmt.Errorf("bind execution contract for %q: %w", asset.Name, err)
		}
		result = append(result, contract)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].AssetID < result[j].AssetID
	})
	return result, nil
}

func identityForPipelineAsset(pl *pipeline.Pipeline, asset *pipeline.Asset) string {
	if pl == nil || asset == nil {
		return ""
	}
	return identity.AssetID(strings.TrimSpace(pl.LegacyID), asset.Name)
}

func executionContractForAsset(
	workspaceRoot string,
	cfg *config.Config,
	pl *pipeline.Pipeline,
	asset *pipeline.Asset,
	target AssetRenderTarget,
) (PipelinePlanExecutionContract, error) {
	if pl == nil || asset == nil {
		return PipelinePlanExecutionContract{}, fmt.Errorf("pipeline and asset are required")
	}
	connections := executionConnectionNames(&directPipelineInfo{
		Pipeline: pl,
		Asset:    asset,
		Config:   cfg,
	})
	mutation := executionMutationResources(target)
	coordination := clonePipelinePlanResources(mutation)
	targetConnectionName, _ := targetConnectionNameForAsset(asset, pl)
	if coordination.Isolation != PipelinePlanResourceIsolationPipeline {
		for _, name := range connections {
			connection, ok := selectedConfigurationConnection(cfg, name)
			if !ok {
				coordination = pipelineExclusiveResources()
				break
			}
			rawPath, isDuckDB := duckDBConnectionPath(connection)
			if !isDuckDB {
				continue
			}
			canonicalPath, err := duckcoord.CanonicalPath(workspaceRoot, rawPath)
			if err != nil || strings.TrimSpace(canonicalPath) == "" {
				coordination = pipelineExclusiveResources()
				break
			}
			if name == targetConnectionName && hasRelationScopedDuckDBWriteResource(target, canonicalPath) {
				continue
			}
			resource := exactAssetWriteResource(assetWriteResourceDuckDB, canonicalPath, "")
			if resource.Fidelity != AssetRenderFidelityExact || resource.Identity == "" {
				coordination = pipelineExclusiveResources()
				break
			}
			coordination.Claims = append(coordination.Claims, PipelinePlanResourceClaim{
				Kind: resource.Kind, Identity: resource.Identity,
			})
		}
	}
	coordination = canonicalPipelinePlanResources(coordination)
	return PipelinePlanExecutionContract{
		AssetID:               identityForPipelineAsset(pl, asset),
		AssetName:             asset.Name,
		ConnectionKeys:        executionConnectionKeys(connections),
		MutationResources:     canonicalPipelinePlanResources(mutation),
		CoordinationResources: coordination,
	}, nil
}

func hasRelationScopedDuckDBWriteResource(target AssetRenderTarget, canonicalPath string) bool {
	resource := target.WriteResource
	if resource.Fidelity != AssetRenderFidelityExact ||
		resource.Kind != assetWriteResourceDuckDB ||
		strings.TrimSpace(resource.Identity) == "" {
		return false
	}
	databaseResource := exactAssetWriteResource(assetWriteResourceDuckDB, canonicalPath, "")
	return databaseResource.Fidelity == AssetRenderFidelityExact &&
		databaseResource.Identity != "" &&
		resource.Identity != databaseResource.Identity
}

func executionConnectionKeys(names []string) []string {
	result := make([]string, 0, len(names))
	for _, name := range names {
		digest := sha256.Sum256([]byte("renart-execution-connection-v1\x00" + name))
		result = append(result, hex.EncodeToString(digest[:]))
	}
	sort.Strings(result)
	return result
}

func executionConnectionNames(info *directPipelineInfo) []string {
	if info == nil || info.Asset == nil {
		return []string{}
	}
	primary, _ := assetRenderConnectionName(info)
	names := assetRenderConfigurationConnectionNames(info, primary)
	sort.Strings(names)
	result := names[:0]
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || (len(result) > 0 && result[len(result)-1] == name) {
			continue
		}
		result = append(result, name)
	}
	if result == nil {
		return []string{}
	}
	return result
}

func executionMutationResources(target AssetRenderTarget) PipelinePlanResources {
	resource := target.WriteResource
	kind := strings.TrimSpace(resource.Kind)
	identity := strings.TrimSpace(resource.Identity)
	if resource.Fidelity == AssetRenderFidelityExact && kind == assetWriteResourceNone && identity == "" {
		return PipelinePlanResources{
			Isolation: PipelinePlanResourceIsolationResources,
			Claims:    []PipelinePlanResourceClaim{},
		}
	}
	if resource.Fidelity != AssetRenderFidelityExact || identity == "" ||
		(kind != assetWriteResourceLocalFile &&
			kind != assetWriteResourceDuckDB &&
			kind != assetWriteResourceWarehouse) {
		return pipelineExclusiveResources()
	}
	return PipelinePlanResources{
		Isolation: PipelinePlanResourceIsolationResources,
		Claims: []PipelinePlanResourceClaim{{
			Kind: kind, Identity: identity,
		}},
	}
}

func selectedConfigurationConnection(cfg *config.Config, name string) (any, bool) {
	if cfg == nil || cfg.SelectedEnvironment == nil || cfg.SelectedEnvironment.Connections == nil {
		return nil, false
	}
	connection := cfg.SelectedEnvironment.Connections.GetConnection(name)
	return connection, connection != nil
}

func duckDBConnectionPath(connection any) (string, bool) {
	switch typed := connection.(type) {
	case *config.DuckDBConnection:
		if typed == nil {
			return "", false
		}
		return typed.Path, true
	case config.DuckDBConnection:
		return typed.Path, true
	default:
		return "", false
	}
}

func pipelineExclusiveResources() PipelinePlanResources {
	return webexecution.PipelineExclusiveResources()
}

func clonePipelinePlanResources(resources PipelinePlanResources) PipelinePlanResources {
	return webexecution.CloneResources(resources)
}

func canonicalPipelinePlanResources(resources PipelinePlanResources) PipelinePlanResources {
	return webexecution.CanonicalResources(resources)
}

func aggregatePipelinePlanMutationResources(
	contracts []PipelinePlanExecutionContract,
) PipelinePlanResources {
	return webexecution.AggregateMutationResources(contracts)
}

func executionContractsForUnits(
	snapshot ExecutionTargetSnapshot,
	units []PipelineExecutionUnit,
) ([]PipelinePlanExecutionContract, error) {
	selected := make(map[string]string)
	for _, unit := range units {
		selected[unit.AssetID] = unit.AssetName
	}
	result := make([]PipelinePlanExecutionContract, 0, len(selected))
	for assetID, assetName := range selected {
		entry, exists := snapshot.Entries[assetName]
		if !exists || entry.AssetID != assetID ||
			entry.ExecutionContract.AssetID != assetID ||
			entry.ExecutionContract.AssetName != assetName {
			return nil, fmt.Errorf("execution contract for %q is unavailable", assetName)
		}
		result = append(result, entry.ExecutionContract)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].AssetID < result[j].AssetID
	})
	return result, nil
}

func equalPipelinePlanExecutionContracts(
	left, right []PipelinePlanExecutionContract,
) bool {
	return webexecution.EqualExecutionContracts(left, right)
}

func equalPipelinePlanResources(left, right PipelinePlanResources) bool {
	return webexecution.EqualResources(left, right)
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
