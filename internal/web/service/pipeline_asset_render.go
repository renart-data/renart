package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strconv"
	"strings"

	"renart/internal/web/policy"
	"renart/internal/web/snapshot"
)

// renart:web
type PipelineAssetRenderRequest struct {
	AssetName     string                    `json:"asset_name"`
	Source        PipelinePlanSourceRequest `json:"source,omitempty"`
	Environment   string                    `json:"environment,omitempty"`
	StartDate     string                    `json:"start_date,omitempty"`
	EndDate       string                    `json:"end_date,omitempty"`
	ExecutionTime string                    `json:"execution_time,omitempty"`
	FullRefresh   bool                      `json:"full_refresh"`
}

// renart:web
type PipelineAssetRenderComparisonRequest struct {
	AssetName         string `json:"asset_name"`
	SnapshotVersionID string `json:"snapshot_version_id,omitempty"`
	Environment       string `json:"environment,omitempty"`
	StartDate         string `json:"start_date,omitempty"`
	EndDate           string `json:"end_date,omitempty"`
	ExecutionTime     string `json:"execution_time,omitempty"`
	FullRefresh       bool   `json:"full_refresh"`
}

type AssetRenderStageComparison struct {
	Key         string            `json:"key"`
	Status      string            `json:"status"`
	Deployment  *AssetRenderStage `json:"deployment,omitempty"`
	WorkingTree *AssetRenderStage `json:"working_tree,omitempty"`
}

type AssetRenderComparisonSummary struct {
	Added     int `json:"added"`
	Removed   int `json:"removed"`
	Changed   int `json:"changed"`
	Unchanged int `json:"unchanged"`
}

// renart:web
type PipelineAssetRenderComparison struct {
	Status      string                       `json:"status"`
	AssetName   string                       `json:"asset_name"`
	Snapshot    AssetRenderSource            `json:"snapshot"`
	WorkingTree AssetRenderSource            `json:"working_tree"`
	Deployment  *AssetRenderResult           `json:"deployment,omitempty"`
	Current     *AssetRenderResult           `json:"current,omitempty"`
	Stages      []AssetRenderStageComparison `json:"stages"`
	Summary     AssetRenderComparisonSummary `json:"summary"`
}

func (s *PipelinePlanService) RenderPipelineAsset(
	ctx context.Context,
	pipelineID string,
	req PipelineAssetRenderRequest,
) (AssetRenderResult, *APIError) {
	result, _, found, apiErr := s.renderPipelineAsset(ctx, pipelineID, req, false)
	if apiErr != nil {
		return AssetRenderResult{}, apiErr
	}
	if !found {
		return AssetRenderResult{}, &APIError{Status: 404, Code: "asset_not_found", Message: "asset was not found in the selected source"}
	}
	return result, nil
}

func (s *PipelinePlanService) ComparePipelineAssetRenders(
	ctx context.Context,
	pipelineID string,
	req PipelineAssetRenderComparisonRequest,
) (PipelineAssetRenderComparison, *APIError) {
	assetName := strings.TrimSpace(req.AssetName)
	if assetName == "" {
		return PipelineAssetRenderComparison{}, &APIError{Status: 400, Code: "asset_name_required", Message: "asset_name is required"}
	}

	current, currentSource, found, apiErr := s.renderPipelineAsset(ctx, pipelineID, PipelineAssetRenderRequest{
		AssetName:     assetName,
		Source:        PipelinePlanSourceRequest{Kind: PipelinePlanSourceWorkingTree},
		Environment:   req.Environment,
		StartDate:     req.StartDate,
		EndDate:       req.EndDate,
		ExecutionTime: req.ExecutionTime,
		FullRefresh:   req.FullRefresh,
	}, false)
	if apiErr != nil {
		return PipelineAssetRenderComparison{}, apiErr
	}
	if !found {
		return PipelineAssetRenderComparison{}, &APIError{Status: 404, Code: "asset_not_found", Message: "asset was not found in the saved working tree"}
	}

	// Bind both sides to the exact context resolved for the working tree. A
	// snapshot's own default cadence may differ, but a rendered diff must not
	// accidentally compare two different windows or execution timestamps.
	context := current.Provenance.Context
	deployed, snapshotSource, deployedFound, apiErr := s.renderPipelineAsset(ctx, pipelineID, PipelineAssetRenderRequest{
		AssetName: assetName,
		Source: PipelinePlanSourceRequest{
			Kind:      PipelinePlanSourceSnapshot,
			VersionID: strings.TrimSpace(req.SnapshotVersionID),
		},
		Environment:   context.Environment,
		StartDate:     context.StartDate,
		EndDate:       context.EndDate,
		ExecutionTime: context.ExecutionTime,
		FullRefresh:   context.RequestedFullRefresh,
	}, true)
	if apiErr != nil {
		return PipelineAssetRenderComparison{}, apiErr
	}

	var deployedResult *AssetRenderResult
	if deployedFound {
		copy := deployed
		deployedResult = &copy
	}
	currentCopy := current
	stages, summary := compareAssetRenderStages(deployedResult, &currentCopy)
	status := "unchanged"
	if summary.Added > 0 || summary.Removed > 0 || summary.Changed > 0 {
		status = "changed"
	}
	return PipelineAssetRenderComparison{
		Status:      status,
		AssetName:   assetName,
		Snapshot:    snapshotSource,
		WorkingTree: currentSource,
		Deployment:  deployedResult,
		Current:     &currentCopy,
		Stages:      stages,
		Summary:     summary,
	}, nil
}

func (s *PipelinePlanService) renderPipelineAsset(
	ctx context.Context,
	pipelineID string,
	req PipelineAssetRenderRequest,
	allowMissing bool,
) (AssetRenderResult, AssetRenderSource, bool, *APIError) {
	if s == nil {
		return AssetRenderResult{}, AssetRenderSource{}, false, &APIError{Status: 500, Code: "renderer_unavailable", Message: "pipeline asset rendering is unavailable"}
	}
	assetName := strings.TrimSpace(req.AssetName)
	if assetName == "" {
		return AssetRenderResult{}, AssetRenderSource{}, false, &APIError{Status: 400, Code: "asset_name_required", Message: "asset_name is required"}
	}
	pipelineUUID, ok := s.resolvePipelineUUID(strings.TrimSpace(pipelineID))
	if !ok || strings.TrimSpace(pipelineUUID) == "" {
		return AssetRenderResult{}, AssetRenderSource{}, false, &APIError{Status: 404, Code: "pipeline_not_found", Message: "pipeline was not found"}
	}
	sourceRequest, err := normalizePipelinePlanSource(req.Source, policy.EnvironmentPolicy{})
	if err != nil {
		return AssetRenderResult{}, AssetRenderSource{}, false, &APIError{Status: 400, Code: "invalid_render_source", Message: err.Error()}
	}
	resolved, deploymentRequired, apiErr := s.resolveSource(ctx, pipelineID, pipelineUUID, sourceRequest, nil)
	if apiErr != nil {
		return AssetRenderResult{}, AssetRenderSource{}, false, apiErr
	}
	if deploymentRequired {
		return AssetRenderResult{}, AssetRenderSource{}, false, &APIError{Status: 404, Code: "deployment_not_found", Message: "this pipeline has no deployment to render"}
	}
	defer resolved.cleanup()

	var assetPath string
	for _, asset := range resolved.parsed.Assets {
		if asset == nil || asset.Name != assetName {
			continue
		}
		assetPath, err = assetPathRelativeToRoot(resolved.root, asset)
		if err != nil || assetPath == ".." || strings.HasPrefix(assetPath, "../") {
			return AssetRenderResult{}, resolved.source, false, &APIError{Status: 400, Code: "invalid_asset_path", Message: "asset source path could not be resolved"}
		}
		break
	}
	if assetPath == "" {
		if allowMissing {
			return AssetRenderResult{}, resolved.source, false, nil
		}
		return AssetRenderResult{}, resolved.source, false, &APIError{Status: 404, Code: "asset_not_found", Message: "asset was not found in the selected source"}
	}

	renderer := newAssetRenderServiceForSource(
		resolved.root, s.deps.WorkspaceRoot, s.deps.ConfigPath, resolved.source,
	)
	renderer.collectManifest = func(string) (map[string]string, error) {
		return resolved.manifest, nil
	}
	result, renderErr := renderer.RenderPath(ctx, assetPath, AssetRenderRequest{
		Environment:   strings.TrimSpace(req.Environment),
		StartDate:     strings.TrimSpace(req.StartDate),
		EndDate:       strings.TrimSpace(req.EndDate),
		ExecutionTime: strings.TrimSpace(req.ExecutionTime),
		FullRefresh:   req.FullRefresh,
	})
	if renderErr != nil {
		return AssetRenderResult{}, resolved.source, false, assetRenderAPIError(renderErr)
	}
	latestSourceState, stateErr := snapshot.CollectSourceState(resolved.pipelineDir)
	if stateErr != nil {
		return AssetRenderResult{}, resolved.source, false, &APIError{Status: 500, Code: "source_identity_failed", Message: "asset source identity could not be verified"}
	}
	if !resolved.state.Equal(latestSourceState) {
		return AssetRenderResult{}, resolved.source, false, &APIError{Status: 409, Code: "source_changed", Message: "asset source changed while rendering; retry the preview"}
	}
	return result, resolved.source, true, nil
}

func compareAssetRenderStages(
	deployment *AssetRenderResult,
	current *AssetRenderResult,
) ([]AssetRenderStageComparison, AssetRenderComparisonSummary) {
	deploymentStages := []AssetRenderStage{}
	if deployment != nil {
		deploymentStages = deployment.Stages
	}
	currentStages := []AssetRenderStage{}
	if current != nil {
		currentStages = current.Stages
	}
	deploymentKeys := indexedAssetRenderStageKeys(deploymentStages)
	currentKeys := indexedAssetRenderStageKeys(currentStages)
	deploymentByKey := make(map[string]AssetRenderStage, len(deploymentStages))
	for index, key := range deploymentKeys {
		deploymentByKey[key] = deploymentStages[index]
	}
	currentByKey := make(map[string]AssetRenderStage, len(currentStages))
	for index, key := range currentKeys {
		currentByKey[key] = currentStages[index]
	}

	orderedKeys := append([]string(nil), currentKeys...)
	seen := make(map[string]struct{}, len(orderedKeys))
	for _, key := range orderedKeys {
		seen[key] = struct{}{}
	}
	for _, key := range deploymentKeys {
		if _, ok := seen[key]; !ok {
			orderedKeys = append(orderedKeys, key)
		}
	}

	comparisons := make([]AssetRenderStageComparison, 0, len(orderedKeys))
	summary := AssetRenderComparisonSummary{}
	for _, key := range orderedKeys {
		before, beforeOK := deploymentByKey[key]
		after, afterOK := currentByKey[key]
		comparison := AssetRenderStageComparison{Key: key}
		switch {
		case !beforeOK:
			comparison.Status = "added"
			afterCopy := after
			comparison.WorkingTree = &afterCopy
			summary.Added++
		case !afterOK:
			comparison.Status = "removed"
			beforeCopy := before
			comparison.Deployment = &beforeCopy
			summary.Removed++
		default:
			beforeCopy, afterCopy := before, after
			comparison.Deployment = &beforeCopy
			comparison.WorkingTree = &afterCopy
			if reflect.DeepEqual(before, after) {
				comparison.Status = "unchanged"
				summary.Unchanged++
			} else {
				comparison.Status = "changed"
				summary.Changed++
			}
		}
		comparisons = append(comparisons, comparison)
	}
	return comparisons, summary
}

func indexedAssetRenderStageKeys(stages []AssetRenderStage) []string {
	counts := make(map[string]int, len(stages))
	keys := make([]string, 0, len(stages))
	for _, stage := range stages {
		signature := strings.Join([]string{
			stage.Kind,
			stage.Label,
			stage.Language,
			stage.CheckKind,
			stage.CheckName,
			stage.CheckColumn,
		}, "\x00")
		ordinal := counts[signature]
		counts[signature] = ordinal + 1
		digest := sha256.Sum256([]byte(signature))
		keys = append(keys, hex.EncodeToString(digest[:12])+":"+strconv.Itoa(ordinal))
	}
	return keys
}
