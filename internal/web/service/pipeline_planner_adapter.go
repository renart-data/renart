package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"

	webexecution "renart/internal/web/execution"
	"renart/internal/web/fingerprint"
	"renart/internal/web/identity"
	"renart/internal/web/matlog"
	"renart/internal/web/policy"
	"renart/internal/web/snapshot"
	"renart/internal/web/staleness"
)

type pipelinePlannerStaleness struct {
	owner *PipelinePlanService
}

func (s pipelinePlannerStaleness) Evaluate(
	ctx context.Context,
	selection staleness.Selection,
	parsed *pipeline.Pipeline,
) (staleness.Snapshot, error) {
	if s.owner == nil || s.owner.deps.Staleness == nil {
		return staleness.Snapshot{}, errors.New("staleness evaluation is unavailable")
	}
	return s.owner.deps.Staleness.Evaluate(ctx, selection, parsed)
}

type pipelinePlannerConfiguration struct {
	owner *PipelinePlanService
	cfg   *config.Config
}

func (c *pipelinePlannerConfiguration) EnvironmentName() string {
	if c == nil || c.cfg == nil {
		return ""
	}
	return c.cfg.SelectedEnvironmentName
}

func (c *pipelinePlannerConfiguration) SchemaPrefix() string {
	if c == nil || c.cfg == nil || c.cfg.SelectedEnvironment == nil {
		return ""
	}
	return c.cfg.SelectedEnvironment.SchemaPrefix
}

func (c *pipelinePlannerConfiguration) InitialIdentity() webexecution.ConfigurationIdentity {
	if c == nil || c.owner == nil {
		return webexecution.ConfigurationIdentity{}
	}
	value := selectedConfigurationIdentityWithBindings(c.owner.deps.WorkspaceRoot, c.cfg, nil)
	return webexecution.ConfigurationIdentity{Digest: value.Digest, Fidelity: string(value.Fidelity)}
}

func (c *pipelinePlannerConfiguration) BindSelection(
	parsed *pipeline.Pipeline,
	selectedAssets []*pipeline.Asset,
	requestedNames []string,
) (webexecution.ConfigurationIdentity, *APIError) {
	if c == nil || c.owner == nil || parsed == nil {
		return webexecution.ConfigurationIdentity{}, &APIError{
			Status: 500, Code: "configuration_identity_unavailable",
			Message: "selected execution configuration could not be resolved",
		}
	}
	configurationAssets := selectedAssets
	if requestedNames != nil {
		assetByName := make(map[string]*pipeline.Asset, len(parsed.Assets))
		for _, asset := range parsed.Assets {
			if asset != nil {
				assetByName[asset.Name] = asset
			}
		}
		configurationAssets = make([]*pipeline.Asset, 0, len(requestedNames))
		seen := make(map[string]struct{}, len(requestedNames))
		for _, name := range requestedNames {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}
			asset := assetByName[name]
			if asset == nil {
				return webexecution.ConfigurationIdentity{}, &APIError{
					Status: 409, Code: "reviewed_asset_missing",
					Message: "a reviewed asset no longer exists in the selected source",
				}
			}
			if !pipelinePlanAssetIsExecutable(asset) {
				continue
			}
			seen[name] = struct{}{}
			configurationAssets = append(configurationAssets, asset)
		}
	}
	value := selectedPipelineConfigurationIdentity(
		c.owner.deps.WorkspaceRoot, c.cfg, parsed, configurationAssets,
	)
	return webexecution.ConfigurationIdentity{Digest: value.Digest, Fidelity: string(value.Fidelity)}, nil
}

func (s *resolvedPipelinePlanSource) Pipeline() *pipeline.Pipeline {
	if s == nil {
		return nil
	}
	return s.parsed
}

func (s *resolvedPipelinePlanSource) Identity() AssetRenderSource {
	if s == nil {
		return AssetRenderSource{}
	}
	return s.source
}

func (s *resolvedPipelinePlanSource) Verify() *APIError {
	if s == nil {
		return &APIError{Status: 500, Code: "source_identity_failed", Message: "pipeline source identity could not be verified"}
	}
	latest, err := snapshot.CollectSourceState(s.pipelineDir)
	if err != nil {
		return &APIError{Status: 500, Code: "source_identity_failed", Message: "pipeline source identity could not be verified"}
	}
	if !s.state.Equal(latest) {
		return &APIError{Status: 409, Code: "source_changed", Message: "pipeline source changed while planning; regenerate the plan"}
	}
	return nil
}

func (s *resolvedPipelinePlanSource) Close() {
	if s != nil && s.cleanup != nil {
		s.cleanup()
	}
}

type pipelinePlanningSession struct {
	owner                *PipelinePlanService
	source               *resolvedPipelinePlanSource
	configuration        *pipelinePlannerConfiguration
	input                webexecution.PlannerSessionInput
	checks               TypeCheckReport
	renderer             *AssetRenderService
	snapshotWorkspace    *snapshotPrerequisiteWorkspace
	snapshotWorkspaceErr error
}

func (s *PipelinePlanService) executionPlannerDependencies() webexecution.PlannerDependencies {
	return webexecution.PlannerDependencies{
		ResolvePipelineUUID: s.resolvePipelineUUID,
		LoadConfiguration: func(environment string) (webexecution.PlannerConfiguration, error) {
			cfg, err := loadSelectedConfigReadOnlyFS(afero.NewOsFs(), s.deps.ConfigPath, environment)
			if err != nil {
				return nil, err
			}
			return &pipelinePlannerConfiguration{owner: s, cfg: cfg}, nil
		},
		ResolveSource: func(
			ctx context.Context,
			pipelineID, pipelineUUID string,
			req webexecution.PlanSourceRequest,
			variableOverrides map[string]any,
		) (webexecution.PlannerSource, bool, *APIError) {
			return s.resolveSource(ctx, pipelineID, pipelineUUID, req, variableOverrides)
		},
		OpenSession: s.openPipelinePlanningSession,
		ResolveVariables: func(
			parsed *pipeline.Pipeline,
			variableOverrides map[string]any,
			variableOverrideSource string,
		) webexecution.PlannerVariableContext {
			vars := fingerprint.EffectiveVars(parsed, variableOverrides)
			return webexecution.PlannerVariableContext{
				Digest: fingerprint.AllVarsHash(vars),
				Provenance: assetRenderVariableProvenanceWithOverrides(
					parsed, variableOverrides, variableOverrideSource,
				),
			}
		},
		IsExecutable:        pipelinePlanAssetIsExecutable,
		EffectiveSensorMode: effectiveSensorMode,
		Staleness:           pipelinePlannerStaleness{owner: s},
		PolicyFor: func(environment string) policy.EnvironmentPolicy {
			if s.deps.PolicyFor == nil {
				return policy.EnvironmentPolicy{}
			}
			return s.deps.PolicyFor(environment)
		},
		ConflictingRunID: func(ctx context.Context, pipelineID, pipelineUUID string, resources PipelinePlanResources) (string, error) {
			if s.deps.ConflictingRunID != nil {
				return s.deps.ConflictingRunID(ctx, pipelineID, pipelineUUID, resources)
			}
			if s.deps.ActiveRunID != nil {
				return s.deps.ActiveRunID(ctx, pipelineID, pipelineUUID)
			}
			return "", nil
		},
		Now: func() time.Time {
			if s.deps.Now != nil {
				return s.deps.Now()
			}
			return time.Now().UTC()
		},
	}
}

func (s *PipelinePlanService) openPipelinePlanningSession(
	ctx context.Context,
	source webexecution.PlannerSource,
	configuration webexecution.PlannerConfiguration,
	input webexecution.PlannerSessionInput,
) (webexecution.PlannerSession, *APIError) {
	resolved, ok := source.(*resolvedPipelinePlanSource)
	if !ok || resolved == nil || resolved.parsed == nil {
		return nil, &APIError{Status: 500, Code: "pipeline_source_unavailable", Message: "pipeline source is unavailable"}
	}
	selected, ok := configuration.(*pipelinePlannerConfiguration)
	if !ok || selected == nil || selected.cfg == nil {
		return nil, &APIError{Status: 500, Code: "configuration_unavailable", Message: "selected execution configuration is unavailable"}
	}
	session := &pipelinePlanningSession{
		owner: s, source: resolved, configuration: selected, input: input,
	}
	if resolved.source.Kind == PipelinePlanSourceSnapshot &&
		pipelineHasSelectedFullURIDependency(resolved.parsed, allPipelineAssetIDs(resolved.parsed)) &&
		s.deps.Snapshots != nil && s.deps.ResolveProducerDeployment != nil {
		workspace, err := s.resolveSnapshotPrerequisiteWorkspace(
			ctx, input.Plan, resolved, input.Request.ProducerDeploymentPins,
		)
		if err != nil {
			session.snapshotWorkspaceErr = err
		} else {
			session.snapshotWorkspace = &workspace
		}
	}
	checkOptions := typeCheckOptions{}
	if session.snapshotWorkspace != nil {
		checkOptions.WorkspaceGraph = &session.snapshotWorkspace.sqlGraph
	} else if resolved.source.Kind == PipelinePlanSourceWorkingTree &&
		pipelineHasSelectedFullURIDependency(resolved.parsed, allPipelineAssetIDs(resolved.parsed)) {
		if workspaceGraph, err := s.workspaceSQLGraph(ctx); err == nil {
			checkOptions.WorkspaceGraph = &workspaceGraph
		}
	}
	session.checks = checkPipelineAt(
		ctx, afero.NewOsFs(), resolved.parsed, resolved.root,
		input.Window, input.ExecutionTime, checkOptions,
	)
	if input.Purpose == PipelinePlanPurposeDeployment && s.deps.CurrentState != nil {
		AppendPresentationTypeChecks(
			ctx, afero.NewOsFs(), s.deps.WorkspaceRoot, input.Plan.PipelineID,
			s.deps.CurrentState(), &session.checks,
		)
	}
	session.renderer = newAssetRenderServiceForSource(
		resolved.root, s.deps.WorkspaceRoot, s.deps.ConfigPath, resolved.source,
	)
	session.renderer.variableOverrides = input.Request.VariableOverrides
	session.renderer.variableOverrideSource = input.Request.VariableOverrideSource
	session.renderer.collectManifest = func(string) (map[string]string, error) {
		return resolved.manifest, nil
	}
	return session, nil
}

func (s *pipelinePlanningSession) CodeChecks() TypeCheckReport {
	if s == nil {
		return TypeCheckReport{Assets: []TypeCheckAsset{}}
	}
	return s.checks
}

func (s *pipelinePlanningSession) ApplyPrerequisites(
	ctx context.Context,
	plan *PipelinePlan,
	selected []webexecution.SelectedPlanAsset,
) {
	if s == nil || s.owner == nil || plan == nil {
		return
	}
	compatibility := selectedPipelinePlanAssets{items: make([]selectedPipelinePlanAsset, 0, len(selected))}
	for _, item := range selected {
		compatibility.items = append(compatibility.items, selectedPipelinePlanAsset{
			asset: item.Asset, reasons: item.Reasons, windows: item.Windows,
		})
	}
	s.owner.addCrossPipelinePrerequisites(
		ctx, plan, s.source, compatibility, s.configuration.cfg,
		s.input.Purpose, s.input.Window, s.input.Request.VariableOverrides,
		s.input.Request.ProducerDeploymentPins, s.snapshotWorkspace, s.snapshotWorkspaceErr,
	)
}

func (s *pipelinePlanningSession) PlanAsset(
	ctx context.Context,
	item webexecution.SelectedPlanAsset,
	status staleness.AssetStatus,
	defaultWindow ExecutionTimeWindow,
) (webexecution.PlannedAssetResult, *APIError) {
	if s == nil || s.owner == nil || s.source == nil || item.Asset == nil {
		return webexecution.PlannedAssetResult{}, &APIError{Status: 500, Code: "asset_plan_unavailable", Message: "selected asset could not be planned"}
	}
	asset := item.Asset
	planAsset := PipelinePlanAsset{
		ID:               identity.AssetID(s.input.Plan.PipelineUUID, asset.Name),
		WorkspaceAssetID: planWorkspaceAssetID(s.source.root, asset),
		Name:             asset.Name,
		Type:             string(asset.Type),
		Staleness:        string(status.Status),
		InclusionReasons: append([]string(nil), item.Reasons...),
		Renders:          []PipelinePlanRender{},
	}
	if strings.TrimSpace(status.Fingerprint) != "" {
		planAsset.Fingerprint = status.Fingerprint
	}
	result := webexecution.PlannedAssetResult{Asset: planAsset, Blockers: []PipelinePlanIssue{}, Warnings: []PipelinePlanIssue{}}
	if !pipelinePlanAssetIsExecutable(asset) {
		result.Asset.Target = AssetRenderTarget{
			Kind: assetRenderTargetKindNone, Fidelity: AssetRenderFidelityExact,
			WriteResource: AssetRenderWriteResource{Kind: assetWriteResourceNone, Fidelity: AssetRenderFidelityExact},
		}
		connectionName, _ := assetRenderConnectionName(&directPipelineInfo{
			Pipeline: s.source.parsed, Asset: asset, Config: s.configuration.cfg,
		})
		result.Asset.ConnectionName = connectionName
		return result, nil
	}
	if pipelineAssetParseError(asset) != "" {
		return result, nil
	}
	windows := item.Windows
	if len(windows) == 0 {
		windows = []ExecutionTimeWindow{defaultWindow}
	}
	if s.input.Request.Backfill && !matlog.BackfillSafe(asset) {
		result.Blockers = append(result.Blockers, PipelinePlanIssue{
			Code: "asset_not_backfill_safe", Severity: "error",
			Message: "asset cannot be safely backfilled as independent execution windows",
			AssetID: planAsset.ID, AssetName: asset.Name,
		})
	}
	assetPath, err := assetPathRelativeToRoot(s.source.root, asset)
	if err != nil || strings.HasPrefix(assetPath, "../") || assetPath == ".." {
		result.Blockers = append(result.Blockers, PipelinePlanIssue{
			Code: "asset_render_failed", Severity: "error",
			Message: "asset source path could not be resolved", AssetID: planAsset.ID, AssetName: asset.Name,
		})
		return result, nil
	}
	reason := "needed"
	if len(item.Reasons) > 0 && strings.TrimSpace(item.Reasons[0]) != "" {
		reason = item.Reasons[0]
	}
	for _, window := range windows {
		rendered, renderErr := s.renderer.RenderPath(ctx, assetPath, AssetRenderRequest{
			Environment: s.configuration.EnvironmentName(),
			StartDate:   window.StartRFC3339(), EndDate: window.EndRFC3339(),
			ExecutionTime: s.input.ExecutionTime.Format("2006-01-02T15:04:05.999999999Z07:00"),
			FullRefresh:   s.input.Request.FullRefresh,
		})
		if errors.Is(renderErr, ErrAssetRenderSourceChanged) {
			return webexecution.PlannedAssetResult{}, &APIError{
				Status: 409, Code: "source_changed",
				Message: "pipeline source changed while planning; regenerate the plan",
			}
		}
		if renderErr != nil {
			result.Blockers = append(result.Blockers, PipelinePlanIssue{
				Code: "asset_render_failed", Severity: "error",
				Message: safePipelinePlanRenderError(renderErr), AssetID: planAsset.ID, AssetName: asset.Name,
			})
			result.Asset.Renders = append(result.Asset.Renders, PipelinePlanRender{
				StartDate: window.StartRFC3339(), EndDate: window.EndRFC3339(),
				Status: AssetRenderStatusError, Stages: []AssetRenderStage{},
				Issues: []AssetRenderIssue{}, Redactions: []AssetRenderRedaction{},
			})
			continue
		}
		if result.Asset.Dialect == "" {
			result.Asset.Dialect = rendered.Asset.Dialect
			result.Asset.ConnectionName = rendered.Asset.ConnectionName
			if result.Asset.Fingerprint == "" {
				result.Asset.Fingerprint = rendered.Asset.Fingerprint
			}
			result.Asset.Target = rendered.Asset.Target
		}
		renderIndex := len(result.Asset.Renders)
		result.Asset.Renders = append(result.Asset.Renders, PipelinePlanRender{
			StartDate: window.StartRFC3339(), EndDate: window.EndRFC3339(),
			Status: rendered.Status, FullRefresh: rendered.Provenance.Context.FullRefresh,
			Stages:     clonePipelinePlanStages(rendered.Stages, s.input.Request.IncludeStageContent),
			Issues:     append([]AssetRenderIssue(nil), rendered.Issues...),
			Redactions: append([]AssetRenderRedaction(nil), rendered.Redactions...),
		})
		result.ExecutionUnits = append(result.ExecutionUnits, PipelinePlanExecutionUnit{
			AssetID: planAsset.ID, AssetName: asset.Name,
			StartDate: window.StartRFC3339(), EndDate: window.EndRFC3339(),
			RenderIndex: renderIndex, Reason: reason,
		})
		blockers, warnings := webexecution.RenderIssues(planAsset.ID, asset.Name, rendered)
		result.Blockers = append(result.Blockers, blockers...)
		result.Warnings = append(result.Warnings, warnings...)
		result.Stages += len(rendered.Stages)
		if rendered.Provenance.Context.FullRefresh {
			result.FullRefresh = true
			result.DestructiveOperations++
		}
	}
	return result, nil
}

func (s *pipelinePlanningSession) BindExecutionContracts(
	_ context.Context,
	assets []PipelinePlanAsset,
) ([]PipelinePlanExecutionContract, *APIError) {
	if s == nil || s.owner == nil {
		return nil, &APIError{Status: 500, Code: "execution_contract_invalid", Message: "selected execution resources could not be bound"}
	}
	contracts, err := pipelinePlanExecutionContracts(
		s.owner.deps.WorkspaceRoot, s.configuration.cfg, s.source.parsed, assets,
	)
	if err != nil {
		return nil, &APIError{Status: 500, Code: "execution_contract_invalid", Message: "selected execution resources could not be bound"}
	}
	return contracts, nil
}

func (s *pipelinePlanningSession) Close() {
	if s != nil && s.snapshotWorkspace != nil && s.snapshotWorkspace.cleanup != nil {
		s.snapshotWorkspace.cleanup()
	}
}
