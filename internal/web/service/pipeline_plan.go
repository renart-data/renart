package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"

	"renart/internal/sqllsp"
	webexecution "renart/internal/web/execution"
	"renart/internal/web/fingerprint"
	"renart/internal/web/matlog"
	webmodel "renart/internal/web/model"
	"renart/internal/web/policy"
	"renart/internal/web/snapshot"
	"renart/internal/web/staleness"
)

type PipelinePlanSnapshotStore interface {
	Latest(ctx context.Context, pipelineUUID string) (*snapshot.Snapshot, error)
	ValidateMetadata(ctx context.Context, versionID, pipelineUUID string) (snapshot.Snapshot, error)
	MaterializeForPipelineExecution(ctx context.Context, versionID, pipelineUUID, destDir string) error
}

type PipelinePlanStaleness interface {
	Evaluate(ctx context.Context, selection staleness.Selection, parsed *pipeline.Pipeline) (staleness.Snapshot, error)
}

type PipelinePlanDependencies struct {
	WorkspaceRoot             string
	ConfigPath                string
	Snapshots                 PipelinePlanSnapshotStore
	Staleness                 PipelinePlanStaleness
	DependencyGraph           WorkspaceDependencyGraphResolver
	WorkspaceGraph            func(context.Context) (sqllsp.CanonicalGraph, error)
	CurrentState              func() webmodel.WorkspaceState
	Fingerprints              *fingerprint.Engine
	Materializations          *matlog.Store
	ResolveProducerDeployment func(context.Context, string, string) (PipelinePlanProducerDeployment, error)
	ResolvePipelineUUID       func(pipelineID string) (string, bool)
	PolicyFor                 func(environment string) policy.EnvironmentPolicy
	ActiveRunID               func(ctx context.Context, pipelineID, pipelineUUID string) (string, error)
	ConflictingRunID          func(ctx context.Context, pipelineID, pipelineUUID string, resources PipelinePlanResources) (string, error)
	NewPipelineBuilder        func() *pipeline.Builder
	Now                       func() time.Time
}

type PipelinePlanService struct {
	deps    PipelinePlanDependencies
	planner *webexecution.Planner
}

func NewPipelinePlanService(deps PipelinePlanDependencies) *PipelinePlanService {
	service := &PipelinePlanService{deps: deps}
	service.planner = webexecution.NewPlanner(service.executionPlannerDependencies())
	return service
}

type resolvedPipelinePlanSource struct {
	root               string
	pipelineDir        string
	relPath            string
	parsed             *pipeline.Pipeline
	manifest           map[string]string
	dependencyManifest snapshot.DependencyManifest
	state              snapshot.SourceState
	source             AssetRenderSource
	cleanup            func()
}

func (s *PipelinePlanService) Plan(
	ctx context.Context,
	pipelineID string,
	req PipelinePlanRequest,
) (PipelinePlan, *APIError) {
	if s == nil || s.planner == nil || strings.TrimSpace(s.deps.WorkspaceRoot) == "" || s.deps.Staleness == nil {
		return PipelinePlan{}, &APIError{
			Status: 500, Code: "planner_unavailable",
			Message: "pipeline planning is unavailable",
		}
	}
	return s.planner.Plan(ctx, pipelineID, req)
}

func (s *PipelinePlanService) workspaceSQLGraph(ctx context.Context) (sqllsp.CanonicalGraph, error) {
	if s.deps.WorkspaceGraph != nil {
		return s.deps.WorkspaceGraph(ctx)
	}
	state, err := NewWorkspaceService(s.deps.WorkspaceRoot, s.deps.ConfigPath).ComputeState(ctx)
	if err != nil {
		return sqllsp.CanonicalGraph{}, err
	}
	return buildWorkspaceCanonicalGraph(ctx, s.deps.WorkspaceRoot, state), nil
}

func pipelinePlanAssetIsExecutable(asset *pipeline.Asset) bool {
	return asset != nil && !isSourceAssetType(asset.Type)
}

func effectivePipelineMaxActiveSteps(pl *pipeline.Pipeline) int {
	return webexecution.EffectiveMaxActiveSteps(pl)
}

// bindPipelinePlanExecutionDependencies records a conservative, stable unit
// DAG. Multiple windows for one asset are chained. The first selected window
// waits for the final selected window of every selected full in-pipeline
// upstream. Symbolic dependencies remain lineage-only, while unselected full
// upstreams remain reviewed data-state preconditions rather than runtime nodes.
func bindPipelinePlanExecutionDependencies(pl *pipeline.Pipeline, units []PipelinePlanExecutionUnit) error {
	return webexecution.BindPlanExecutionDependencies(pl, units)
}

func (s *PipelinePlanService) resolvePipelineUUID(pipelineID string) (string, bool) {
	if s.deps.ResolvePipelineUUID == nil {
		return "", false
	}
	return s.deps.ResolvePipelineUUID(pipelineID)
}

func normalizePipelinePlanPurpose(raw string) (string, error) {
	return webexecution.NormalizePlanPurpose(raw)
}

func normalizePipelinePlanSource(req PipelinePlanSourceRequest, envPolicy policy.EnvironmentPolicy) (PipelinePlanSourceRequest, error) {
	return webexecution.NormalizePlanSource(req, envPolicy.DeployedOnly)
}

func normalizePipelinePlanSelection(req PipelinePlanSelectionRequest) (PipelinePlanSelectionRequest, error) {
	return webexecution.NormalizePlanSelection(req)
}

func (s *PipelinePlanService) resolveSource(
	ctx context.Context,
	pipelineID string,
	pipelineUUID string,
	req PipelinePlanSourceRequest,
	variableOverrides map[string]any,
) (*resolvedPipelinePlanSource, bool, *APIError) {
	relPath, err := DecodeID(pipelineID)
	if err != nil {
		return nil, false, &APIError{Status: 400, Code: "invalid_pipeline_id", Message: "pipeline id is invalid"}
	}
	relPath = filepath.ToSlash(relPath)
	builder := s.deps.NewPipelineBuilder
	if builder == nil {
		builder = func() *pipeline.Builder { return NewRenartPipelineBuilder(afero.NewOsFs()) }
	}

	resolved := &resolvedPipelinePlanSource{relPath: relPath, cleanup: func() {}}
	if req.Kind == PipelinePlanSourceWorkingTree {
		pipelineDir, joinErr := SafeJoin(s.deps.WorkspaceRoot, relPath)
		if joinErr != nil {
			return nil, false, &APIError{Status: 400, Code: "invalid_pipeline_id", Message: "pipeline id is invalid"}
		}
		resolved.root = s.deps.WorkspaceRoot
		resolved.pipelineDir = pipelineDir
		resolved.source = AssetRenderSource{Kind: PipelinePlanSourceWorkingTree, PipelinePath: relPath}
	} else {
		if s.deps.Snapshots == nil {
			return nil, false, &APIError{Status: 500, Code: "snapshot_store_unavailable", Message: "deployment snapshots are unavailable"}
		}
		var deployed snapshot.Snapshot
		if req.VersionID == "" {
			latest, latestErr := s.deps.Snapshots.Latest(ctx, pipelineUUID)
			if latestErr != nil {
				return nil, false, &APIError{Status: 500, Code: "snapshot_lookup_failed", Message: "deployment snapshot could not be loaded"}
			}
			if latest == nil {
				return nil, true, nil
			}
			deployed = *latest
		} else {
			validated, validateErr := s.deps.Snapshots.ValidateMetadata(ctx, req.VersionID, pipelineUUID)
			if validateErr != nil {
				return nil, false, &APIError{Status: 404, Code: "snapshot_not_found", Message: "deployment snapshot was not found for this pipeline"}
			}
			deployed = validated
		}

		tempRoot, tempErr := os.MkdirTemp("", "renart-plan-")
		if tempErr != nil {
			return nil, false, &APIError{Status: 500, Code: "snapshot_materialize_failed", Message: "deployment snapshot could not be prepared"}
		}
		resolved.cleanup = func() { _ = os.RemoveAll(tempRoot) }
		pipelineDir, joinErr := SafeJoin(tempRoot, relPath)
		if joinErr != nil {
			resolved.cleanup()
			return nil, false, &APIError{Status: 400, Code: "invalid_pipeline_id", Message: "pipeline id is invalid"}
		}
		if mkdirErr := os.MkdirAll(pipelineDir, 0o755); mkdirErr != nil {
			resolved.cleanup()
			return nil, false, &APIError{Status: 500, Code: "snapshot_materialize_failed", Message: "deployment snapshot could not be prepared"}
		}
		if materializeErr := s.deps.Snapshots.MaterializeForPipelineExecution(ctx, deployed.VersionID, pipelineUUID, pipelineDir); materializeErr != nil {
			resolved.cleanup()
			return nil, false, &APIError{Status: 500, Code: "snapshot_materialize_failed", Message: "deployment snapshot failed integrity validation"}
		}
		resolved.root = tempRoot
		resolved.pipelineDir = pipelineDir
		resolved.manifest = deployed.Manifest
		resolved.dependencyManifest = deployed.DependencyManifest
		resolved.source = AssetRenderSource{
			Kind:              PipelinePlanSourceSnapshot,
			VersionID:         deployed.VersionID,
			DeploymentOrdinal: deployed.Ordinal,
			PipelinePath:      relPath,
			MerkleRoot:        deployed.MerkleRoot,
		}
	}

	state, stateErr := snapshot.CollectSourceState(resolved.pipelineDir)
	if stateErr != nil {
		resolved.cleanup()
		return nil, false, &APIError{Status: 500, Code: "source_identity_failed", Message: "pipeline source identity could not be computed"}
	}
	resolved.state = state
	if resolved.manifest == nil {
		manifest, manifestErr := snapshot.CollectManifestHashes(resolved.pipelineDir)
		if manifestErr != nil || len(manifest) == 0 {
			resolved.cleanup()
			return nil, false, &APIError{Status: 500, Code: "source_identity_failed", Message: "pipeline source identity could not be computed"}
		}
		resolved.manifest = manifest
		resolved.source.MerkleRoot = snapshot.ManifestRoot(manifest)
	}
	pipelineBuilder := builder()
	if overrideErr := addVariableOverrides(pipelineBuilder, variableOverrides); overrideErr != nil {
		resolved.cleanup()
		return nil, false, &APIError{Status: 400, Code: "invalid_variable_overrides", Message: overrideErr.Error()}
	}
	parsed, parseErr := pipelineBuilder.CreatePipelineFromPath(ctx, resolved.pipelineDir, pipeline.WithMutate())
	if parseErr != nil {
		if errors.Is(parseErr, ErrInvalidVariableOverrides) {
			resolved.cleanup()
			return nil, false, &APIError{Status: 400, Code: "invalid_variable_overrides", Message: parseErr.Error()}
		}
		// A malformed asset is an asset-scoped plan blocker, not a reason to
		// erase every valid sibling from the review. The tolerant builder keeps
		// the broken file as a marked placeholder. Pipeline-level YAML errors
		// still fail here because the tolerant builder cannot recover them.
		tolerantBuilder := NewRenartTolerantPipelineBuilder(afero.NewOsFs())
		if overrideErr := addVariableOverrides(tolerantBuilder, variableOverrides); overrideErr != nil {
			resolved.cleanup()
			return nil, false, &APIError{Status: 400, Code: "invalid_variable_overrides", Message: overrideErr.Error()}
		}
		parsed, parseErr = tolerantBuilder.CreatePipelineFromPath(ctx, resolved.pipelineDir, pipeline.WithMutate())
		if parseErr != nil {
			resolved.cleanup()
			if errors.Is(parseErr, ErrInvalidVariableOverrides) {
				return nil, false, &APIError{Status: 400, Code: "invalid_variable_overrides", Message: parseErr.Error()}
			}
			return nil, false, &APIError{Status: 400, Code: "pipeline_invalid", Message: "pipeline source could not be parsed"}
		}
	}
	if parsed == nil {
		resolved.cleanup()
		return nil, false, &APIError{Status: 400, Code: "pipeline_invalid", Message: "pipeline source is empty"}
	}
	if parsed.LegacyID == "" {
		parsed.LegacyID = pipelineUUID
	} else if parsed.LegacyID != pipelineUUID {
		resolved.cleanup()
		return nil, false, &APIError{Status: 409, Code: "pipeline_identity_mismatch", Message: "pipeline source identity does not match the requested pipeline"}
	}
	resolved.parsed = parsed
	resolved.source.PipelinePath = workspaceRelativeRenderPath(resolved.root, parsed.DefinitionFile.Path)
	return resolved, false, nil
}

type selectedPipelinePlanAsset struct {
	asset   *pipeline.Asset
	reasons []string
	windows []ExecutionTimeWindow
}

type selectedPipelinePlanAssets struct {
	items []selectedPipelinePlanAsset
	err   error
}

func selectPipelinePlanAssets(
	parsed *pipeline.Pipeline,
	req PipelinePlanSelectionRequest,
	statuses []staleness.AssetStatus,
	dataStateAvailable bool,
) selectedPipelinePlanAssets {
	selected, err := webexecution.SelectPlanAssets(parsed, req, statuses, dataStateAvailable)
	if err != nil {
		return selectedPipelinePlanAssets{err: err}
	}
	items := make([]selectedPipelinePlanAsset, 0, len(selected))
	for _, item := range selected {
		items = append(items, selectedPipelinePlanAsset{
			asset: item.Asset, reasons: item.Reasons, windows: item.Windows,
		})
	}
	return selectedPipelinePlanAssets{items: items}
}

func pipelinePlanUpstreamClosure(name string, assets map[string]*pipeline.Asset) map[string]struct{} {
	return webexecution.UpstreamClosure(name, assets)
}

func pipelinePlanDownstreamClosure(name string, downstream map[string][]string) map[string]struct{} {
	return webexecution.DownstreamClosure(name, downstream)
}

func planWorkspaceAssetID(root string, asset *pipeline.Asset) string {
	path, err := assetPathRelativeToRoot(root, asset)
	if err != nil || path == ".." || strings.HasPrefix(path, "../") {
		return ""
	}
	return EncodeID(filepath.ToSlash(path))
}

func clonePipelinePlanStages(stages []AssetRenderStage, includeContent bool) []AssetRenderStage {
	return webexecution.CloneRenderStages(stages, includeContent)
}

func safePipelinePlanRenderError(err error) string {
	var boundary *assetRenderBoundaryError
	if errors.As(err, &boundary) && strings.TrimSpace(boundary.message) != "" {
		return boundary.message
	}
	return "asset could not be rendered for this execution context"
}

func PipelinePlanReviewedIdentityID(identity PipelinePlanReviewedIdentity) string {
	return webexecution.ReviewedIdentityID(identity)
}

func PipelinePlanReviewedIdentityFromPlan(plan PipelinePlan) PipelinePlanReviewedIdentity {
	return webexecution.ReviewedIdentityFromPlan(plan)
}

func aggregatePythonRuntimePlanWarnings(
	assets []PipelinePlanAsset,
	issues []PipelinePlanIssue,
) []PipelinePlanIssue {
	return webexecution.AggregatePythonRuntimeWarnings(assets, issues)
}
