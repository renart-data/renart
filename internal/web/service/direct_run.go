package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/bruin-data/bruin/pkg/config"
	bruinexecutor "github.com/bruin-data/bruin/pkg/executor"
	"github.com/bruin-data/bruin/pkg/git"
	"github.com/bruin-data/bruin/pkg/jinja"
	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/bruin-data/bruin/pkg/scheduler"
	"github.com/bruin-data/bruin/pkg/sqlparser"
	"github.com/spf13/afero"
	"go.uber.org/zap"

	"renart/internal/web/executiongraph"
	"renart/internal/web/identity"
	"renart/internal/web/runstate"
	"renart/internal/web/secretstore"
)

func (e *HybridBruinExecutor) RunAsset(ctx context.Context, req RunAssetRequest, onChunk func([]byte)) ([]byte, error) {
	ctx = secretstore.WithPurpose(ctx, secretstore.PurposeMaterialize)
	if e.newPipelineBuilder == nil {
		return nil, fmt.Errorf("direct run requires a pipeline builder")
	}

	pp, err := getDirectPipelineAndAsset(ctx, e.workspaceRoot, req.AssetPath, afero.NewOsFs())
	if err != nil {
		return nil, err
	}
	printer := &streamCaptureWriter{buffer: bytes.NewBuffer(nil), onChunk: onChunk}
	writeDirectRunAnalysis(printer, pp.Pipeline, pp.Asset)
	if err := validateDirectRunDependencies(ctx, printer, pp.Pipeline, e.workspaceRoot); err != nil {
		return printer.buffer.Bytes(), err
	}

	if shouldFallbackToCLIRunAsset(pp.Asset, pp.Pipeline) {
		return printer.buffer.Bytes(), fmt.Errorf("direct run is not supported for asset type %q", pp.Asset.Type)
	}
	if _, err := selectConfigEnvironment(pp.Config, req.Environment); err != nil {
		return printer.buffer.Bytes(), fmt.Errorf("failed to use the environment '%s': %w", req.Environment, err)
	}
	applySelectedEnvironmentRefreshRestriction(pp.Config, pp.Pipeline.Assets)
	manager, err := e.directConnectionManager(ctx, pp.Config)
	if err != nil {
		return printer.buffer.Bytes(), err
	}

	runID := newRenartRunID()
	executionTime := time.Now().UTC()
	timeWindow, err := ResolveExecutionTimeWindow(string(pp.Pipeline.Schedule), req.StartDate, req.EndDate, executionTime)
	if err != nil {
		return printer.buffer.Bytes(), err
	}
	runCtx, parser, cleanup, err := buildDirectRunAssetContext(ctx, pp, timeWindow, executionTime, runID)
	if err != nil {
		return printer.buffer.Bytes(), err
	}
	defer cleanup()
	runCtx = context.WithValue(runCtx, pipeline.RunConfigFullRefresh, req.FullRefresh)
	runCtx = withTargetWriteStartCallback(runCtx, req.BeforeTargetWrite)

	renderer, err := buildDirectRunAssetRenderer(pp, timeWindow, executionTime, runID)
	if err != nil {
		return printer.buffer.Bytes(), err
	}
	if err := resolveDirectExecutionHookTemplates(directAssetHookRenderContext(runCtx, pp.Asset, req.FullRefresh), pp.Pipeline, pp.Asset, renderer); err != nil {
		return printer.buffer.Bytes(), err
	}

	mainExecutors := map[pipeline.AssetType]bruinexecutor.Config{}
	if isAPIAsset(pp.Asset) || isLoadAsset(pp.Asset) {
		// Main execution stays on Renart's HTTP/Sling path below, but the
		// scheduler still creates quality-check and metadata task instances.
		mainExecutors, err = buildDirectCheckExecutors(manager, renderer)
		if err != nil {
			return printer.buffer.Bytes(), err
		}
	} else {
		mainExecutors, err = buildDirectMainExecutors(manager, renderer, parser, pp.Pipeline, pp.Config, e.runRegistry, e.duckDBCoordinator, e.duckDBSessions, e.workspaceRoot, e.disableDuckDBFilesystemAccess, req.FullRefresh, effectiveSensorMode(req.SensorMode, false))
		if err != nil {
			return printer.buffer.Bytes(), err
		}
	}

	s := scheduler.NewScheduler(zap.NewNop().Sugar(), pp.Pipeline, runID)
	s.MarkAll(scheduler.Skipped)
	if !s.MarkAsset(pp.Asset, scheduler.Pending, false) {
		return printer.buffer.Bytes(), fmt.Errorf("asset '%s' was not found among the pipeline's scheduled task instances", pp.Asset.Name)
	}

	pending := s.GetTaskInstancesByStatus(scheduler.Pending)
	if len(pending) == 0 {
		return printer.buffer.Bytes(), nil
	}
	if err := e.notifyExecutionTargetsResolved(pp.Pipeline, pp.Config, pp.Pipeline.Assets, req.OnTargetsResolved); err != nil {
		return printer.buffer.Bytes(), err
	}

	var regRun *runstate.Run
	if e.runRegistry != nil {
		regRun = e.runRegistry.BeginRun(runID, pp.Config.SelectedEnvironmentName, []string{pp.Asset.Name})
		defer regRun.End()
	}

	formatting := directRunFormatting{}
	if startDate, ok := runCtx.Value(pipeline.RunConfigStartDate).(time.Time); ok {
		formatting.startDate = startDate
	}
	if endDate, ok := runCtx.Value(pipeline.RunConfigEndDate).(time.Time); ok {
		formatting.endDate = endDate
	}
	writeDirectRunWindow(printer, formatting)
	runCtx = context.WithValue(runCtx, bruinexecutor.ContextLogger, zap.NewNop().Sugar())

	seq := bruinexecutor.Sequential{TaskTypeMap: mainExecutors}
	results := make([]*scheduler.TaskExecutionResult, 0, len(pending))
	startedAt := time.Now()

	for {
		pending = s.GetTaskInstancesByStatus(scheduler.Pending)
		if len(pending) == 0 {
			break
		}

		progressed := false
		for _, instance := range pending {
			if instance.GetType() != scheduler.TaskInstanceTypeMain &&
				instance.GetType() != scheduler.TaskInstanceTypeColumnCheck &&
				instance.GetType() != scheduler.TaskInstanceTypeCustomCheck &&
				instance.GetType() != scheduler.TaskInstanceTypeMetadataPush {
				continue
			}
			if !allDirectRunPipelineDependenciesSucceeded(instance) {
				continue
			}

			progressed = true
			taskStartedAt := time.Now()
			if eventErr := emitDirectRunAssetEvent(req.AssetEvent, instance, "running", taskStartedAt, time.Time{}, nil); eventErr != nil {
				return printer.buffer.Bytes(), fmt.Errorf("persist running event for asset %q: %w", instance.GetAsset().Name, eventErr)
			}
			instance.MarkAs(scheduler.Running)
			writeDirectRunLifecycle(printer, instance, nil, true, 0)
			finishTask := beginRegistryTask(regRun, instance)
			runErr := e.runDirectTask(runCtx, pp.Pipeline, instance, renderer, manager, &seq, printer)
			if runErr != nil {
				finishTask(runErr)
				instance.MarkAs(scheduler.Failed)
				resultErr := runErr
				if eventErr := emitDirectRunAssetEvent(req.AssetEvent, instance, directRunTerminalErrorStatus(runCtx, runErr), taskStartedAt, time.Now(), runErr); eventErr != nil {
					resultErr = errors.Join(runErr, fmt.Errorf("persist failed event for asset %q: %w", instance.GetAsset().Name, eventErr))
				}
				results = append(results, &scheduler.TaskExecutionResult{Instance: instance, Error: resultErr})
				writeDirectRunLifecycle(printer, instance, resultErr, false, time.Since(taskStartedAt))
				writeDirectRunSummary(printer, buildDirectRunSummary(results, time.Since(startedAt)))
				_ = e.saveDirectRunLog(ctx, pp.Pipeline, s, runID, req.Environment, timeWindow, []string{"renart", "run", req.AssetPath})
				return printer.buffer.Bytes(), resultErr
			}
			finishTask(nil)
			if eventErr := emitDirectRunAssetEvent(req.AssetEvent, instance, "success", taskStartedAt, time.Now(), nil); eventErr != nil {
				resultErr := fmt.Errorf("persist success event for asset %q: %w", instance.GetAsset().Name, eventErr)
				instance.MarkAs(scheduler.Failed)
				results = append(results, &scheduler.TaskExecutionResult{Instance: instance, Error: resultErr})
				writeDirectRunLifecycle(printer, instance, resultErr, false, time.Since(taskStartedAt))
				writeDirectRunSummary(printer, buildDirectRunSummary(results, time.Since(startedAt)))
				_ = e.saveDirectRunLog(ctx, pp.Pipeline, s, runID, req.Environment, timeWindow, []string{"renart", "run", req.AssetPath})
				return printer.buffer.Bytes(), resultErr
			}
			instance.MarkAs(scheduler.Succeeded)
			results = append(results, &scheduler.TaskExecutionResult{Instance: instance, Error: nil})
			writeDirectRunLifecycle(printer, instance, nil, false, time.Since(taskStartedAt))
		}

		if !progressed {
			writeDirectRunSummary(printer, buildDirectRunSummary(results, time.Since(startedAt)))
			return printer.buffer.Bytes(), fmt.Errorf("direct run stalled: no runnable task instances remained")
		}
	}

	writeDirectRunSummary(printer, buildDirectRunSummary(results, time.Since(startedAt)))
	_ = e.saveDirectRunLog(ctx, pp.Pipeline, s, runID, req.Environment, timeWindow, []string{"renart", "run", req.AssetPath})
	return printer.buffer.Bytes(), nil
}

func (e *HybridBruinExecutor) RunPipeline(ctx context.Context, req RunPipelineRequest, onChunk func([]byte)) ([]byte, error) {
	ctx = secretstore.WithPurpose(ctx, secretstore.PurposeMaterialize)
	if e.newPipelineBuilder == nil {
		return nil, fmt.Errorf("direct pipeline run requires a pipeline builder")
	}

	resolvedTarget := resolveDirectPath(e.workspaceRoot, req.Target)
	builder := e.newPipelineBuilder()
	if err := addVariableOverrides(builder, req.VariableOverrides); err != nil {
		return nil, err
	}
	foundPipeline, err := builder.CreatePipelineFromPath(ctx, resolvedTarget, pipeline.WithMutate())
	if err != nil {
		return nil, err
	}
	if foundPipeline == nil {
		return nil, fmt.Errorf("pipeline not found")
	}
	configPath := strings.TrimSpace(req.ConfigPath)
	if configPath == "" {
		repoRoot, repoErr := git.FindRepoFromPath(resolvedTarget)
		if repoErr != nil {
			return nil, fmt.Errorf("failed to find the git repository root: %w", repoErr)
		}
		configPath = filepath.Join(repoRoot.Path, ".bruin.yml")
	}
	cfg, err := loadSelectedConfig(configPath, req.Environment)
	if err != nil {
		return nil, err
	}
	if req.DryRun {
		manager, err := e.directConnectionManager(ctx, cfg)
		if err != nil {
			return nil, err
		}
		return e.dryRunPipeline(ctx, foundPipeline, cfg, manager, onChunk)
	}

	printer := &streamCaptureWriter{buffer: bytes.NewBuffer(nil), onChunk: onChunk}
	writeDirectRunAnalysis(printer, foundPipeline, nil)
	if err := validateDirectRunDependencies(ctx, printer, foundPipeline, e.workspaceRoot); err != nil {
		return printer.buffer.Bytes(), err
	}
	selectionMode := strings.TrimSpace(req.SelectionMode)
	plannedExecution := selectionMode != "" &&
		(selectionMode != PipelinePlanSelectionAll || req.PlanVersion >= PipelineExecutionPlanVersionV3)
	if !plannedExecution && shouldFallbackToCLIRunPipeline(foundPipeline) {
		return printer.buffer.Bytes(), fmt.Errorf("direct pipeline run is not supported for one or more asset types")
	}
	applySelectedEnvironmentRefreshRestriction(cfg, foundPipeline.Assets)
	manager, err := e.directConnectionManager(ctx, cfg)
	if err != nil {
		return printer.buffer.Bytes(), err
	}

	pp := &directPipelineInfo{Pipeline: foundPipeline, Config: cfg}
	runID := strings.TrimSpace(req.RunID)
	if runID == "" {
		runID = newRenartRunID()
	}
	executionTime := req.ExecutionTime.UTC()
	if req.ExecutionTime.IsZero() {
		executionTime = time.Now().UTC()
	}
	if plannedExecution {
		return e.runPlannedPipeline(ctx, req, pp, manager, printer, executionTime, runID, resolvedTarget)
	}
	timeWindow, err := ResolveExecutionTimeWindow(string(foundPipeline.Schedule), req.StartDate, req.EndDate, executionTime)
	if err != nil {
		return printer.buffer.Bytes(), err
	}
	if effectivePipelineMaxActiveSteps(foundPipeline) > 1 {
		req.PlanVersion = PipelineExecutionPlanVersionV3
		req.SelectionMode = PipelinePlanSelectionAll
		req.MaxActiveSteps = effectivePipelineMaxActiveSteps(foundPipeline)
		req.ExecutionUnits, err = runtimePipelineExecutionUnits(foundPipeline, timeWindow)
		if err != nil {
			return printer.buffer.Bytes(), err
		}
		return e.runPlannedPipeline(ctx, req, pp, manager, printer, executionTime, runID, resolvedTarget)
	}
	runCtx, parser, cleanup, err := buildDirectRunAssetContext(ctx, pp, timeWindow, executionTime, runID)
	if err != nil {
		return printer.buffer.Bytes(), err
	}
	defer cleanup()
	runCtx = context.WithValue(runCtx, pipeline.RunConfigFullRefresh, req.FullRefresh)
	runCtx = withTargetWriteStartCallback(runCtx, req.BeforeTargetWrite)
	renderer, err := buildDirectRunAssetRenderer(pp, timeWindow, executionTime, runID)
	if err != nil {
		return printer.buffer.Bytes(), err
	}
	for _, asset := range foundPipeline.Assets {
		if err := resolveDirectExecutionHookTemplates(directAssetHookRenderContext(runCtx, asset, req.FullRefresh), foundPipeline, asset, renderer); err != nil {
			return printer.buffer.Bytes(), err
		}
	}
	mainExecutors, err := buildDirectMainExecutors(manager, renderer, parser, foundPipeline, cfg, e.runRegistry, e.duckDBCoordinator, e.duckDBSessions, e.workspaceRoot, e.disableDuckDBFilesystemAccess, req.FullRefresh, effectiveSensorMode(req.SensorMode, false))
	if err != nil {
		return printer.buffer.Bytes(), err
	}

	formatting := directRunFormatting{}
	if startDate, ok := runCtx.Value(pipeline.RunConfigStartDate).(time.Time); ok {
		formatting.startDate = startDate
	}
	if endDate, ok := runCtx.Value(pipeline.RunConfigEndDate).(time.Time); ok {
		formatting.endDate = endDate
	}
	writeDirectRunWindow(printer, formatting)
	runCtx = context.WithValue(runCtx, bruinexecutor.ContextLogger, zap.NewNop().Sugar())

	seq := bruinexecutor.Sequential{TaskTypeMap: mainExecutors}
	s := scheduler.NewScheduler(zap.NewNop().Sugar(), foundPipeline, runID)
	s.MarkAll(scheduler.Pending)
	if err := e.notifyExecutionTargetsResolved(foundPipeline, cfg, foundPipeline.Assets, req.OnTargetsResolved); err != nil {
		return printer.buffer.Bytes(), err
	}
	results := make([]*scheduler.TaskExecutionResult, 0)
	startedAt := time.Now()

	var regRun *runstate.Run
	if e.runRegistry != nil {
		planned := make([]string, 0, len(foundPipeline.Assets))
		for _, asset := range foundPipeline.Assets {
			if asset != nil {
				planned = append(planned, asset.Name)
			}
		}
		regRun = e.runRegistry.BeginRun(runID, cfg.SelectedEnvironmentName, planned)
		defer regRun.End()
	}

	for {
		pending := s.GetTaskInstancesByStatus(scheduler.Pending)
		if len(pending) == 0 {
			break
		}

		progressed := false
		for _, instance := range pending {
			if instance.GetType() != scheduler.TaskInstanceTypeMain &&
				instance.GetType() != scheduler.TaskInstanceTypeColumnCheck &&
				instance.GetType() != scheduler.TaskInstanceTypeCustomCheck &&
				instance.GetType() != scheduler.TaskInstanceTypeMetadataPush {
				continue
			}
			if !allDirectRunPipelineDependenciesSucceeded(instance) {
				continue
			}

			progressed = true
			taskStartedAt := time.Now()
			if eventErr := emitDirectRunAssetEvent(req.AssetEvent, instance, "running", taskStartedAt, time.Time{}, nil); eventErr != nil {
				return printer.buffer.Bytes(), fmt.Errorf("persist running event for asset %q: %w", instance.GetAsset().Name, eventErr)
			}
			instance.MarkAs(scheduler.Running)
			writeDirectRunLifecycle(printer, instance, nil, true, 0)
			finishTask := beginRegistryTask(regRun, instance)
			runErr := e.runDirectTask(runCtx, foundPipeline, instance, renderer, manager, &seq, printer)
			finishTask(runErr)
			if runErr != nil {
				instance.MarkAs(scheduler.Failed)
				resultErr := runErr
				if eventErr := emitDirectRunAssetEvent(req.AssetEvent, instance, directRunTerminalErrorStatus(runCtx, runErr), taskStartedAt, time.Now(), runErr); eventErr != nil {
					resultErr = errors.Join(runErr, fmt.Errorf("persist failed event for asset %q: %w", instance.GetAsset().Name, eventErr))
				}
				results = append(results, &scheduler.TaskExecutionResult{Instance: instance, Error: resultErr})
				writeDirectRunLifecycle(printer, instance, resultErr, false, time.Since(taskStartedAt))
				writeDirectRunSummary(printer, buildDirectRunSummary(results, time.Since(startedAt)))
				_ = e.saveDirectRunLog(ctx, foundPipeline, s, runID, req.Environment, timeWindow, []string{"renart", "run", req.Target})
				return printer.buffer.Bytes(), resultErr
			}
			if eventErr := emitDirectRunAssetEvent(req.AssetEvent, instance, "success", taskStartedAt, time.Now(), nil); eventErr != nil {
				resultErr := fmt.Errorf("persist success event for asset %q: %w", instance.GetAsset().Name, eventErr)
				instance.MarkAs(scheduler.Failed)
				results = append(results, &scheduler.TaskExecutionResult{Instance: instance, Error: resultErr})
				writeDirectRunLifecycle(printer, instance, resultErr, false, time.Since(taskStartedAt))
				writeDirectRunSummary(printer, buildDirectRunSummary(results, time.Since(startedAt)))
				_ = e.saveDirectRunLog(ctx, foundPipeline, s, runID, req.Environment, timeWindow, []string{"renart", "run", req.Target})
				return printer.buffer.Bytes(), resultErr
			}
			instance.MarkAs(scheduler.Succeeded)
			results = append(results, &scheduler.TaskExecutionResult{Instance: instance, Error: nil})
			writeDirectRunLifecycle(printer, instance, nil, false, time.Since(taskStartedAt))
		}

		if !progressed {
			writeDirectRunSummary(printer, buildDirectRunSummary(results, time.Since(startedAt)))
			_ = e.saveDirectRunLog(ctx, foundPipeline, s, runID, req.Environment, timeWindow, []string{"renart", "run", req.Target})
			return printer.buffer.Bytes(), fmt.Errorf("direct run stalled: no runnable task instances remained")
		}
	}

	writeDirectRunSummary(printer, buildDirectRunSummary(results, time.Since(startedAt)))
	_ = e.saveDirectRunLog(ctx, foundPipeline, s, runID, req.Environment, timeWindow, []string{"renart", "run", req.Target})
	return printer.buffer.Bytes(), nil
}

func (e *HybridBruinExecutor) runPlannedPipeline(
	ctx context.Context,
	req RunPipelineRequest,
	pp *directPipelineInfo,
	manager config.ConnectionAndDetailsGetter,
	printer *streamCaptureWriter,
	executionTime time.Time,
	runID string,
	sourceRoot string,
) ([]byte, error) {
	if pp == nil || pp.Pipeline == nil || pp.Config == nil {
		return printer.buffer.Bytes(), errors.New("planned pipeline execution requires a parsed pipeline and configuration")
	}
	assetByName := make(map[string]*pipeline.Asset, len(pp.Pipeline.Assets))
	for _, asset := range pp.Pipeline.Assets {
		if asset != nil {
			assetByName[asset.Name] = asset
		}
	}
	configurationAssets := make([]*pipeline.Asset, 0, len(req.ExecutionUnits))
	seenAssets := make(map[string]struct{}, len(req.ExecutionUnits))
	for index, unit := range req.ExecutionUnits {
		if unit.Position != index {
			return printer.buffer.Bytes(), fmt.Errorf("planned execution unit %d has position %d", index, unit.Position)
		}
		asset := assetByName[strings.TrimSpace(unit.AssetName)]
		if asset == nil {
			return printer.buffer.Bytes(), fmt.Errorf("planned execution asset %q was not found", unit.AssetName)
		}
		if expectedID := identity.AssetID(pp.Pipeline.LegacyID, asset.Name); strings.TrimSpace(unit.AssetID) != expectedID {
			return printer.buffer.Bytes(), fmt.Errorf("planned execution asset %q has changed identity", unit.AssetName)
		}
		if shouldFallbackToCLIRunAsset(asset, pp.Pipeline) {
			return printer.buffer.Bytes(), fmt.Errorf("direct run is not supported for planned asset type %q", asset.Type)
		}
		if strings.TrimSpace(unit.AssetPath) == "" {
			req.ExecutionUnits[index].AssetPath = assetRunPathForPipelineAsset(e.workspaceRoot, asset)
		}
		if _, exists := seenAssets[asset.Name]; !exists {
			seenAssets[asset.Name] = struct{}{}
			configurationAssets = append(configurationAssets, asset)
		}
	}
	if req.PlanVersion >= PipelineExecutionPlanVersionV3 &&
		req.MaxActiveSteps != effectivePipelineMaxActiveSteps(pp.Pipeline) {
		return printer.buffer.Bytes(), fmt.Errorf(
			"planned max_active_steps changed during execution: reviewed %d, current %d",
			req.MaxActiveSteps,
			effectivePipelineMaxActiveSteps(pp.Pipeline),
		)
	}
	if req.PlanVersion >= PipelineExecutionPlanVersionV3 && req.MaxActiveSteps < 1 {
		return printer.buffer.Bytes(), errors.New("planned max_active_steps must be greater than zero")
	}
	if err := validatePipelineExecutionUnits(req.ExecutionUnits, req.PlanVersion); err != nil {
		return printer.buffer.Bytes(), err
	}
	if req.OnExecutionUnitsResolved != nil {
		if err := req.OnExecutionUnitsResolved(
			append([]PipelineExecutionUnit(nil), req.ExecutionUnits...),
		); err != nil {
			return printer.buffer.Bytes(), fmt.Errorf("persist resolved execution units: %w", err)
		}
	}
	snapshot, err := e.resolveExecutionTargetSnapshotForSelection(
		pp.Pipeline,
		pp.Config,
		pp.Pipeline.Assets,
		configurationAssets,
	)
	if err != nil {
		return printer.buffer.Bytes(), fmt.Errorf("resolve execution target snapshot: %w", err)
	}
	if req.PlanVersion >= PipelineExecutionPlanVersionV3 {
		actualContracts, contractErr := executionContractsForUnits(snapshot, req.ExecutionUnits)
		if contractErr != nil {
			return printer.buffer.Bytes(), contractErr
		}
		if req.ExecutionContracts == nil {
			// Unreviewed full runs are normalized locally immediately before
			// execution. Reviewed and recovered runs always carry contracts.
			req.ExecutionContracts = actualContracts
		} else if !equalPipelinePlanExecutionContracts(req.ExecutionContracts, actualContracts) {
			return printer.buffer.Bytes(), errors.New(
				"planned execution connection or resource contract changed during execution",
			)
		}
	}
	if req.OnTargetsResolved != nil {
		if err := req.OnTargetsResolved(snapshot); err != nil {
			return printer.buffer.Bytes(), fmt.Errorf("persist execution target snapshot: %w", err)
		}
	}
	if len(req.ExecutionUnits) == 0 {
		_, _ = printer.Write([]byte("No reviewed execution units remain; the Needed run is complete.\n"))
		return printer.buffer.Bytes(), nil
	}

	var regRun *runstate.Run
	if e.runRegistry != nil {
		planned := make([]string, 0, len(configurationAssets))
		for _, asset := range configurationAssets {
			planned = append(planned, asset.Name)
		}
		regRun = e.runRegistry.BeginRun(runID, pp.Config.SelectedEnvironmentName, planned)
		defer regRun.End()
	}

	if req.PlanVersion >= PipelineExecutionPlanVersionV3 {
		err := e.runPlannedPipelineUnits(
			ctx,
			req,
			pp,
			manager,
			printer,
			executionTime,
			runID,
			sourceRoot,
			assetByName,
			snapshot,
			regRun,
		)
		return printer.buffer.Bytes(), err
	}

	for _, unit := range req.ExecutionUnits {
		asset := assetByName[unit.AssetName]
		window, err := ResolveExecutionTimeWindow(
			string(pp.Pipeline.Schedule), unit.StartDate, unit.EndDate, executionTime,
		)
		if err != nil {
			return printer.buffer.Bytes(), fmt.Errorf("resolve planned window for %s: %w", unit.AssetName, err)
		}
		if err := validatePlannedExecutionWindow(unit, window); err != nil {
			return printer.buffer.Bytes(), err
		}
		if err := e.runPlannedPipelineUnit(ctx, req, pp, manager, printer, executionTime, runID, sourceRoot, unit, asset, window, regRun, nil); err != nil {
			return printer.buffer.Bytes(), err
		}
	}
	return printer.buffer.Bytes(), nil
}

type plannedPipelineUnit struct {
	unit   PipelineExecutionUnit
	asset  *pipeline.Asset
	window ExecutionTimeWindow
	node   executiongraph.Node
}

func (e *HybridBruinExecutor) runPlannedPipelineUnits(
	ctx context.Context,
	req RunPipelineRequest,
	pp *directPipelineInfo,
	manager config.ConnectionAndDetailsGetter,
	printer *streamCaptureWriter,
	executionTime time.Time,
	runID string,
	sourceRoot string,
	assetByName map[string]*pipeline.Asset,
	snapshot ExecutionTargetSnapshot,
	regRun *runstate.Run,
) error {
	units := make([]plannedPipelineUnit, 0, len(req.ExecutionUnits))
	usedConnections := make(map[string]struct{})
	for _, unit := range req.ExecutionUnits {
		asset := assetByName[unit.AssetName]
		window, err := ResolveExecutionTimeWindow(
			string(pp.Pipeline.Schedule),
			unit.StartDate,
			unit.EndDate,
			executionTime,
		)
		if err != nil {
			return fmt.Errorf("resolve planned window for %s: %w", unit.AssetName, err)
		}
		if err := validatePlannedExecutionWindow(unit, window); err != nil {
			return err
		}
		entry, exists := snapshot.Entries[unit.AssetName]
		if !exists || entry.AssetID != unit.AssetID {
			return fmt.Errorf("planned execution target for %q is unavailable", unit.AssetName)
		}
		contract := entry.ExecutionContract
		connections := executionConnectionNames(&directPipelineInfo{
			Pipeline: pp.Pipeline,
			Asset:    asset,
			Config:   pp.Config,
		})
		if !equalStringSlices(executionConnectionKeys(connections), contract.ConnectionKeys) {
			return fmt.Errorf("execution connection contract for %q changed", asset.Name)
		}
		for _, name := range connections {
			usedConnections[name] = struct{}{}
		}
		resources, exclusive := pipelineExecutionResources(contract.CoordinationResources)
		units = append(units, plannedPipelineUnit{
			unit:   unit,
			asset:  asset,
			window: window,
			node: executiongraph.Node{
				Position:     unit.Position,
				Dependencies: append([]int(nil), unit.DependencyPositions...),
				Connections:  connections,
				Resources:    resources,
				Exclusive:    exclusive,
			},
		})
	}
	connectionLimits, err := pipelineExecutionConnectionLimits(pp.Config, manager, usedConnections)
	if err != nil {
		return err
	}
	nodes := make([]executiongraph.Node, len(units))
	for index := range units {
		nodes[index] = units[index].node
	}
	runner := executiongraph.Runner{
		MaxActive:        executionMaxActiveSteps(req.MaxActiveSteps),
		Workspace:        e.workspaceBudget,
		ConnectionLimits: connectionLimits,
		OnBypass: func(event executiongraph.Bypass) error {
			finishedAt := time.Now().UTC()
			status := string(event.Status)
			eventErr := event.Err
			if event.Status == executiongraph.BypassSkipped && len(event.BlockedBy) > 0 {
				blockedNames := make([]string, 0, len(event.BlockedBy))
				for _, position := range event.BlockedBy {
					if position >= 0 && position < len(units) {
						blockedNames = append(blockedNames, units[position].unit.AssetName)
					}
				}
				if len(blockedNames) > 0 {
					eventErr = fmt.Errorf(
						"upstream execution failed: %s",
						strings.Join(blockedNames, ", "),
					)
				}
			}
			return emitPipelineExecutionUnitEvent(
				req.UnitEvent,
				event.Node.Position,
				status,
				nil,
				&finishedAt,
				eventErr,
			)
		},
	}
	writeDirectPlannedRunWindows(printer, units)
	runStartedAt := time.Now()
	unitSummaries := make([]*directRunSummary, len(units))
	unitErrors := make([]error, len(units))
	runErr := runner.Run(ctx, nodes, func(ctx context.Context, node executiongraph.Node) error {
		unit := units[node.Position]
		err := e.runPlannedPipelineUnit(
			ctx,
			req,
			pp,
			manager,
			printer,
			executionTime,
			runID,
			sourceRoot,
			unit.unit,
			unit.asset,
			unit.window,
			regRun,
			func(summary directRunSummary) {
				unitSummaries[node.Position] = &summary
			},
		)
		unitErrors[node.Position] = err
		return err
	})
	writeDirectRunSummary(
		printer,
		buildPlannedPipelineRunSummary(units, unitSummaries, unitErrors, runErr, time.Since(runStartedAt)),
	)
	return runErr
}

func buildPlannedPipelineRunSummary(
	units []plannedPipelineUnit,
	unitSummaries []*directRunSummary,
	unitErrors []error,
	runErr error,
	duration time.Duration,
) directRunSummary {
	results := make([]*scheduler.TaskExecutionResult, 0)
	for _, summary := range unitSummaries {
		if summary != nil {
			results = append(results, summary.results...)
		}
	}
	combined := buildDirectRunSummary(results, duration)
	seenFailures := make(map[string]struct{}, len(combined.failedAssets))
	for _, assetName := range combined.failedAssets {
		seenFailures[assetName] = struct{}{}
	}
	for position, err := range unitErrors {
		if err == nil {
			continue
		}
		assetName := ""
		if position < len(units) {
			assetName = units[position].unit.AssetName
		}
		if _, exists := seenFailures[assetName]; exists {
			continue
		}
		seenFailures[assetName] = struct{}{}
		combined.failedAssets = append(combined.failedAssets, assetName)
		combined.supplementalFailures = append(combined.supplementalFailures, directRunFailure{
			assetName: assetName,
			err:       err,
		})
	}
	if runErr != nil && len(combined.failedAssets) == 0 {
		combined.failedAssets = append(combined.failedAssets, "pipeline execution")
		combined.supplementalFailures = append(combined.supplementalFailures, directRunFailure{
			assetName: "pipeline execution",
			err:       runErr,
		})
	}
	return combined
}

func runtimePipelineExecutionUnits(
	pl *pipeline.Pipeline,
	window ExecutionTimeWindow,
) ([]PipelineExecutionUnit, error) {
	if pl == nil {
		return nil, errors.New("pipeline is required")
	}
	orderedAssets := pipelineAssetsInTopologicalOrder(pl)
	planned := make([]PipelinePlanExecutionUnit, 0, len(orderedAssets))
	for _, asset := range orderedAssets {
		planned = append(planned, PipelinePlanExecutionUnit{
			AssetID:     identity.AssetID(pl.LegacyID, asset.Name),
			AssetName:   asset.Name,
			StartDate:   window.StartRFC3339(),
			EndDate:     window.EndRFC3339(),
			RenderIndex: 0,
			Reason:      "entire_pipeline",
		})
	}
	if err := bindPipelinePlanExecutionDependencies(pl, planned); err != nil {
		return nil, fmt.Errorf("bind runtime execution dependencies: %w", err)
	}
	units := make([]PipelineExecutionUnit, 0, len(planned))
	for position, unit := range planned {
		units = append(units, PipelineExecutionUnit{
			Position: position, AssetID: unit.AssetID, AssetName: unit.AssetName,
			StartDate: unit.StartDate, EndDate: unit.EndDate,
			RenderIndex: unit.RenderIndex, Reason: unit.Reason,
			DependencyPositions: append([]int(nil), unit.DependencyPositions...),
		})
	}
	return units, nil
}

func validatePipelineExecutionUnits(units []PipelineExecutionUnit, planVersion int) error {
	for position, unit := range units {
		if unit.Position != position {
			return fmt.Errorf("planned execution unit %d has position %d", position, unit.Position)
		}
		if planVersion < PipelineExecutionPlanVersionV3 && len(unit.DependencyPositions) > 0 {
			return fmt.Errorf("legacy execution unit %d contains dependency positions", position)
		}
		previous := -1
		for _, dependency := range unit.DependencyPositions {
			if dependency < 0 || dependency >= position || dependency <= previous {
				return fmt.Errorf("planned execution unit %d has invalid dependencies", position)
			}
			previous = dependency
		}
	}
	return nil
}

func pipelineExecutionResources(resources PipelinePlanResources) ([]string, bool) {
	if resources.Isolation != PipelinePlanResourceIsolationResources {
		return nil, true
	}
	result := make([]string, 0, len(resources.Claims))
	for _, claim := range resources.Claims {
		if strings.TrimSpace(claim.Kind) == "" || strings.TrimSpace(claim.Identity) == "" {
			return nil, true
		}
		result = append(result, claim.Kind+"\x00"+claim.Identity)
	}
	return result, false
}

func pipelineExecutionConnectionLimits(
	cfg *config.Config,
	manager config.ConnectionAndDetailsGetter,
	used map[string]struct{},
) (map[string]int, error) {
	const defaultDuckDBConcurrentAssets = 2

	limits := make(map[string]int)
	if cfg != nil && cfg.SelectedEnvironment != nil && cfg.SelectedEnvironment.Connections != nil {
		configured, err := cfg.SelectedEnvironment.Connections.ConnectionConcurrencyLimits()
		if err != nil {
			return nil, fmt.Errorf("resolve connection concurrency limits: %w", err)
		}
		for name, limit := range configured {
			if _, exists := used[name]; exists {
				limits[name] = limit
			}
		}
	}
	if manager == nil {
		return limits, nil
	}
	for name := range used {
		switch connection := manager.GetConnectionDetails(name).(type) {
		case *config.DuckDBConnection:
			if connection != nil {
				if _, configured := limits[name]; !configured {
					limits[name] = defaultDuckDBConcurrentAssets
				}
			}
		case config.DuckDBConnection:
			if _, configured := limits[name]; !configured {
				limits[name] = defaultDuckDBConcurrentAssets
			}
		}
	}
	return limits, nil
}

func validatePlannedExecutionWindow(unit PipelineExecutionUnit, window ExecutionTimeWindow) error {
	start, startErr := time.Parse(time.RFC3339Nano, unit.StartDate)
	end, endErr := time.Parse(time.RFC3339Nano, unit.EndDate)
	if startErr != nil || endErr != nil || !window.Start.Equal(start) || !window.End.Equal(end) {
		return fmt.Errorf("planned execution window for %s changed during execution", unit.AssetName)
	}
	return nil
}

func (e *HybridBruinExecutor) runPlannedPipelineUnit(
	ctx context.Context,
	req RunPipelineRequest,
	pp *directPipelineInfo,
	manager config.ConnectionAndDetailsGetter,
	printer *streamCaptureWriter,
	executionTime time.Time,
	runID string,
	sourceRoot string,
	unit PipelineExecutionUnit,
	asset *pipeline.Asset,
	window ExecutionTimeWindow,
	regRun *runstate.Run,
	onSummary func(directRunSummary),
) (resultErr error) {
	unitRunID := fmt.Sprintf("%s-unit-%d", runID, unit.Position)
	runCtx, parser, cleanup, err := buildDirectRunAssetContext(ctx, pp, window, executionTime, unitRunID)
	if err != nil {
		return err
	}
	defer cleanup()
	runCtx = context.WithValue(runCtx, pipeline.RunConfigFullRefresh, req.FullRefresh)
	runCtx = withTargetWriteStartCallback(runCtx, req.BeforeTargetWrite)
	renderer, err := buildDirectRunAssetRenderer(pp, window, executionTime, unitRunID)
	if err != nil {
		return err
	}
	if err := resolveDirectExecutionHookTemplates(
		directAssetHookRenderContext(runCtx, asset, req.FullRefresh), pp.Pipeline, asset, renderer,
	); err != nil {
		return err
	}

	mainExecutors := map[pipeline.AssetType]bruinexecutor.Config{}
	if isAPIAsset(asset) || isLoadAsset(asset) {
		mainExecutors, err = buildDirectCheckExecutors(manager, renderer)
	} else {
		mainExecutors, err = buildDirectMainExecutors(
			manager, renderer, parser, pp.Pipeline, pp.Config, e.runRegistry, e.duckDBCoordinator,
			e.duckDBSessions, sourceRoot, e.disableDuckDBFilesystemAccess, req.FullRefresh, effectiveSensorMode(req.SensorMode, false),
		)
	}
	if err != nil {
		return err
	}

	s := scheduler.NewScheduler(zap.NewNop().Sugar(), pp.Pipeline, unitRunID)
	s.MarkAll(scheduler.Skipped)
	if !s.MarkAsset(asset, scheduler.Pending, false) {
		return fmt.Errorf("planned asset %q has no scheduled task instances", asset.Name)
	}
	pending := s.GetTaskInstancesByStatus(scheduler.Pending)
	if len(pending) == 0 {
		return fmt.Errorf("planned asset %q has no executable task instances", asset.Name)
	}

	formatting := directRunFormatting{startDate: window.Start, endDate: window.End}
	if onSummary == nil {
		writeDirectRunWindow(printer, formatting)
	}
	runCtx = context.WithValue(runCtx, bruinexecutor.ContextLogger, zap.NewNop().Sugar())
	startedAt := time.Now().UTC()
	if err := emitPipelineExecutionUnitEvent(req.UnitEvent, unit.Position, "running", &startedAt, nil, nil); err != nil {
		return fmt.Errorf("persist running execution unit %d: %w", unit.Position, err)
	}

	unitAssetEvent := func(event ExecutionAssetEvent) error {
		event.UnitPosition = unit.Position
		event.HasUnitPosition = true
		if req.AssetEvent == nil {
			return nil
		}
		return req.AssetEvent(event)
	}
	seq := bruinexecutor.Sequential{TaskTypeMap: mainExecutors}
	results := make([]*scheduler.TaskExecutionResult, 0, len(pending))
	unitStartedAt := time.Now()
	defer func() {
		_ = e.saveDirectRunLog(ctx, pp.Pipeline, s, unitRunID, req.Environment, window, []string{"renart", "run", unit.AssetName})
	}()

	for {
		pending = s.GetTaskInstancesByStatus(scheduler.Pending)
		if len(pending) == 0 {
			break
		}
		progressed := false
		for _, instance := range pending {
			if instance.GetType() != scheduler.TaskInstanceTypeMain &&
				instance.GetType() != scheduler.TaskInstanceTypeColumnCheck &&
				instance.GetType() != scheduler.TaskInstanceTypeCustomCheck &&
				instance.GetType() != scheduler.TaskInstanceTypeMetadataPush {
				continue
			}
			if !allDirectRunPipelineDependenciesSucceeded(instance) {
				continue
			}
			progressed = true
			taskStartedAt := time.Now()
			if err := emitDirectRunAssetEvent(unitAssetEvent, instance, "running", taskStartedAt, time.Time{}, nil); err != nil {
				return fmt.Errorf("persist running event for asset %q: %w", asset.Name, err)
			}
			instance.MarkAs(scheduler.Running)
			writeDirectRunLifecycle(printer, instance, nil, true, 0)
			finishTask := beginRegistryTask(regRun, instance)
			runErr := e.runDirectTask(runCtx, pp.Pipeline, instance, renderer, manager, &seq, printer)
			finishTask(runErr)
			if runErr != nil {
				instance.MarkAs(scheduler.Failed)
				resultErr := runErr
				status := directRunTerminalErrorStatus(runCtx, runErr)
				if eventErr := emitDirectRunAssetEvent(unitAssetEvent, instance, status, taskStartedAt, time.Now(), runErr); eventErr != nil {
					resultErr = errors.Join(runErr, fmt.Errorf("persist failed event for asset %q: %w", asset.Name, eventErr))
				}
				results = append(results, &scheduler.TaskExecutionResult{Instance: instance, Error: resultErr})
				writeDirectRunLifecycle(printer, instance, resultErr, false, time.Since(taskStartedAt))
				emitPlannedPipelineUnitSummary(
					printer,
					onSummary,
					buildDirectRunSummary(results, time.Since(unitStartedAt)),
				)
				finishedAt := time.Now().UTC()
				unitStatus := "failed"
				if status == "cancelled" {
					unitStatus = "cancelled"
				}
				if eventErr := emitPipelineExecutionUnitEvent(req.UnitEvent, unit.Position, unitStatus, &startedAt, &finishedAt, resultErr); eventErr != nil {
					resultErr = errors.Join(resultErr, fmt.Errorf("persist failed execution unit %d: %w", unit.Position, eventErr))
				}
				return resultErr
			}
			if err := emitDirectRunAssetEvent(unitAssetEvent, instance, "success", taskStartedAt, time.Now(), nil); err != nil {
				resultErr := fmt.Errorf("persist success event for asset %q: %w", asset.Name, err)
				instance.MarkAs(scheduler.Failed)
				results = append(results, &scheduler.TaskExecutionResult{Instance: instance, Error: resultErr})
				finishedAt := time.Now().UTC()
				if eventErr := emitPipelineExecutionUnitEvent(req.UnitEvent, unit.Position, "failed", &startedAt, &finishedAt, resultErr); eventErr != nil {
					resultErr = errors.Join(resultErr, fmt.Errorf("persist failed execution unit %d: %w", unit.Position, eventErr))
				}
				return resultErr
			}
			instance.MarkAs(scheduler.Succeeded)
			results = append(results, &scheduler.TaskExecutionResult{Instance: instance})
			writeDirectRunLifecycle(printer, instance, nil, false, time.Since(taskStartedAt))
		}
		if !progressed {
			resultErr := errors.New("direct run stalled: no runnable task instances remained")
			finishedAt := time.Now().UTC()
			if eventErr := emitPipelineExecutionUnitEvent(req.UnitEvent, unit.Position, "failed", &startedAt, &finishedAt, resultErr); eventErr != nil {
				resultErr = errors.Join(resultErr, eventErr)
			}
			return resultErr
		}
	}
	emitPlannedPipelineUnitSummary(
		printer,
		onSummary,
		buildDirectRunSummary(results, time.Since(unitStartedAt)),
	)
	finishedAt := time.Now().UTC()
	if err := emitPipelineExecutionUnitEvent(req.UnitEvent, unit.Position, "success", &startedAt, &finishedAt, nil); err != nil {
		return fmt.Errorf("persist successful execution unit %d: %w", unit.Position, err)
	}
	return nil
}

func emitPlannedPipelineUnitSummary(
	printer *streamCaptureWriter,
	onSummary func(directRunSummary),
	summary directRunSummary,
) {
	if onSummary != nil {
		onSummary(summary)
		return
	}
	writeDirectRunSummary(printer, summary)
}

func emitPipelineExecutionUnitEvent(
	onEvent func(PipelineExecutionUnitEvent) error,
	position int,
	status string,
	startedAt, finishedAt *time.Time,
	err error,
) error {
	if onEvent == nil {
		return nil
	}
	event := PipelineExecutionUnitEvent{
		Position: position, Status: status, StartedAt: startedAt, FinishedAt: finishedAt,
	}
	if err != nil {
		event.Error = err.Error()
	}
	return onEvent(event)
}

func directRunTerminalErrorStatus(ctx context.Context, runErr error) string {
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) || (ctx != nil && ctx.Err() != nil) {
		return "cancelled"
	}
	return "failed"
}

func directAssetHookRenderContext(ctx context.Context, asset *pipeline.Asset, requestedFullRefresh bool) context.Context {
	effectiveFullRefresh := requestedFullRefresh && !assetRefreshRestricted(asset)
	return context.WithValue(ctx, pipeline.RunConfigFullRefresh, effectiveFullRefresh)
}

func (e *HybridBruinExecutor) runDirectTask(
	runCtx context.Context,
	pl *pipeline.Pipeline,
	instance scheduler.TaskInstance,
	renderer *jinja.Renderer,
	manager config.ConnectionAndDetailsGetter,
	seq *bruinexecutor.Sequential,
	printer *streamCaptureWriter,
) error {
	asset := instance.GetAsset()
	assetWriter := newDirectAssetLogWriter(printer, pl, asset)
	taskCtx := context.WithValue(runCtx, bruinexecutor.KeyPrinter, assetWriter)
	restoreAsset := applyDeveloperEnvironmentAssetIdentity(taskCtx, asset)
	defer restoreAsset()

	var callbackErr error
	forward := func(chunk []byte) {
		if callbackErr != nil {
			return
		}
		_, callbackErr = assetWriter.Write(chunk)
	}

	var runErr error
	if e.directTaskGate != nil {
		runErr = e.directTaskGate(taskCtx, instance)
	}
	if runErr == nil {
		connectionNames, connectionErr := directTaskConnectionNames(pl, instance)
		if connectionErr != nil {
			runErr = connectionErr
		} else {
			runErr = preflightRuntimeConnections(manager, connectionNames...)
		}
	}
	if runErr == nil {
		switch {
		case isAPIAsset(asset) && instance.GetType() == scheduler.TaskInstanceTypeMain:
			_, runErr = e.runAPIAsset(taskCtx, pl, asset, renderer, manager, forward)
		case isLoadAsset(asset) && instance.GetType() == scheduler.TaskInstanceTypeMain:
			_, runErr = e.runLoadAsset(taskCtx, pl, asset, manager, forward)
		case directTaskOwnsDuckDBCoordination(instance):
			runErr = seq.RunSingleTask(taskCtx, instance)
		default:
			lease, leaseErr := e.acquireDuckDBConnections(
				taskCtx,
				manager,
				directTaskDuckDBConnectionNames(pl, instance),
				directTaskLeaseOwner(taskCtx, pl, asset),
				assetWriter,
			)
			if leaseErr != nil {
				runErr = leaseErr
			} else {
				runErr = seq.RunSingleTask(taskCtx, instance)
				lease.Release()
			}
		}
	}

	if runErr == nil && callbackErr != nil {
		runErr = callbackErr
	}
	if runErr == nil && instance.GetType() == scheduler.TaskInstanceTypeMain {
		e.warnOnDeclaredSchemaDrift(taskCtx, pl, asset, manager, assetWriter)
	}
	if flushErr := assetWriter.Flush(); runErr == nil && flushErr != nil {
		runErr = flushErr
	}
	return runErr
}

// applyDeveloperEnvironmentAssetIdentity mirrors Bruin's run-command asset
// mutator for the duration of one physical task. Scheduler events, run-state,
// fingerprints, and authored DAG identity stay logical because callers emit
// them before/after this function, while materializers, checks, Seed/Load/API,
// and Python see the prefixed physical relation expected by Bruin operators.
func applyDeveloperEnvironmentAssetIdentity(ctx context.Context, asset *pipeline.Asset) func() {
	if ctx == nil || asset == nil {
		return func() {}
	}
	environment, _ := ctx.Value(config.EnvironmentContextKey).(*config.Environment)
	if environment == nil || strings.TrimSpace(environment.SchemaPrefix) == "" {
		return func() {}
	}

	originalName := asset.Name
	originalUpstreams := append([]pipeline.Upstream(nil), asset.Upstreams...)
	asset.PrefixSchema(environment.SchemaPrefix)
	asset.PrefixUpstreams(environment.SchemaPrefix)
	return func() {
		asset.Name = originalName
		asset.Upstreams = originalUpstreams
	}
}

func applyDeveloperEnvironmentPipelineIdentity(ctx context.Context, pl *pipeline.Pipeline) func() {
	if pl == nil {
		return func() {}
	}
	restores := make([]func(), 0, len(pl.Assets))
	for _, asset := range pl.Assets {
		restores = append(restores, applyDeveloperEnvironmentAssetIdentity(ctx, asset))
	}
	return func() {
		for index := len(restores) - 1; index >= 0; index-- {
			restores[index]()
		}
	}
}

func directTaskOwnsDuckDBCoordination(instance scheduler.TaskInstance) bool {
	return instance != nil &&
		instance.GetType() == scheduler.TaskInstanceTypeMain &&
		instance.GetAsset() != nil &&
		instance.GetAsset().Type == pipeline.AssetTypeDuckDBQuery
}

func directTaskConnectionNames(pl *pipeline.Pipeline, instance scheduler.TaskInstance) ([]string, error) {
	if instance == nil || instance.GetAsset() == nil {
		return nil, nil
	}
	asset := instance.GetAsset()
	switch instance.GetType() {
	case scheduler.TaskInstanceTypeMetadataPush:
		return nil, nil
	case scheduler.TaskInstanceTypeColumnCheck, scheduler.TaskInstanceTypeCustomCheck:
		name, err := targetConnectionNameForAsset(asset, pl)
		if err != nil {
			return nil, err
		}
		return []string{name}, nil
	case scheduler.TaskInstanceTypeMain:
		switch {
		case isAPIAsset(asset):
			name, err := apiConnectionNameForAsset(asset, pl)
			if err != nil {
				return nil, err
			}
			return []string{name}, nil
		case isLoadAsset(asset):
			params, err := resolvedLoadParams(asset, pl)
			if err != nil {
				return nil, err
			}
			names := make([]string, 0, 2)
			if !isLocalLoadConnection(params.SourceConnection) {
				names = append(names, params.SourceConnection)
			}
			if !isLocalLoadConnection(params.DestinationConnection) {
				names = append(names, params.DestinationConnection)
			}
			return names, nil
		default:
			return pl.GetAllConnectionNamesForAsset(asset)
		}
	default:
		return nil, nil
	}
}

func directTaskDuckDBConnectionNames(pl *pipeline.Pipeline, instance scheduler.TaskInstance) []string {
	if instance == nil || instance.GetAsset() == nil {
		return nil
	}
	switch instance.GetType() {
	case scheduler.TaskInstanceTypeColumnCheck, scheduler.TaskInstanceTypeCustomCheck:
		connectionName, err := targetConnectionNameForAsset(instance.GetAsset(), pl)
		if err != nil || connectionName == "" {
			return nil
		}
		return []string{connectionName}
	case scheduler.TaskInstanceTypeMetadataPush:
		// API, Load, and Python metadata dispatch is an explicit no-op in the
		// shared registry, so it must not acquire a warehouse write lease. Keep
		// the established behavior for native warehouse assets whose metadata
		// publishers may still use the configured connection.
		assetType := instance.GetAsset().Type
		if isAPIAsset(instance.GetAsset()) || isLoadAsset(instance.GetAsset()) || assetType == pipeline.AssetTypePython {
			return nil
		}
		return duckDBConnectionNamesForAsset(pl, instance.GetAsset())
	default:
		return duckDBConnectionNamesForAsset(pl, instance.GetAsset())
	}
}

func (e *HybridBruinExecutor) saveDirectRunLog(ctx context.Context, foundPipeline *pipeline.Pipeline, s *scheduler.Scheduler, runID, environment string, timeWindow ExecutionTimeWindow, cmdline []string) error {
	runConfig := &scheduler.RunConfig{
		Environment: environment,
		Output:      "plain",
		StartDate:   timeWindow.StartRFC3339(),
		EndDate:     timeWindow.EndRFC3339(),
	}
	return e.executionLogSink().SaveRunLog(ctx, RunLogRecord{
		Pipeline:  foundPipeline,
		Scheduler: s,
		RunConfig: runConfig,
		RunID:     runID,
		Cmdline:   cmdline,
	})
}

func (e *HybridBruinExecutor) directConnectionManager(ctx context.Context, cfg *config.Config) (config.ConnectionAndDetailsGetter, error) {
	if e.newConnectionManager != nil {
		return e.newConnectionManager(ctx, cfg.SelectedEnvironmentName)
	}

	return newConnectionManagerFromConfig(ctx, cfg)
}

func shouldFallbackToCLIRunAsset(asset *pipeline.Asset, foundPipeline *pipeline.Pipeline) bool {
	if asset == nil || foundPipeline == nil {
		return true
	}
	if isDirectRunAssetTypeSupported(asset.Type) {
		return false
	}
	_, known := bruinexecutor.DefaultExecutorsV2[asset.Type]
	return !known
}

func shouldFallbackToCLIRunPipeline(foundPipeline *pipeline.Pipeline) bool {
	if foundPipeline == nil {
		return true
	}
	for _, asset := range foundPipeline.Assets {
		if asset == nil {
			return true
		}
		if shouldFallbackToCLIRunAsset(asset, foundPipeline) {
			return true
		}
	}
	return false
}

func isDirectRunAssetTypeSupported(assetType pipeline.AssetType) bool {
	if _, ok := directRunAssetTypes[assetType]; ok {
		return true
	}

	// Imported source assets are dependency anchors rather than executable
	// transformations. Bruin deliberately registers known *.source types with
	// no-op operators, so keep those operators intact on Renart's direct path.
	// Requiring a Bruin registration keeps unknown custom source types on the
	// CLI fallback path instead of silently treating them as successful.
	if !isSourceAssetType(assetType) {
		return false
	}
	_, ok := bruinexecutor.DefaultExecutorsV2[assetType]
	return ok
}

const (
	executionTaskKindMain        = "main"
	executionTaskKindColumnCheck = "column_check"
	executionTaskKindCustomCheck = "custom_check"
)

func emitDirectRunAssetEvent(onEvent func(ExecutionAssetEvent) error, instance scheduler.TaskInstance, status string, startedAt, finishedAt time.Time, err error) error {
	if onEvent == nil || instance == nil || instance.GetAsset() == nil {
		return nil
	}
	event := ExecutionAssetEvent{Asset: instance.GetAsset().Name, Status: status}
	switch task := instance.(type) {
	case *scheduler.AssetInstance:
		event.TaskKind = executionTaskKindMain
	case *scheduler.ColumnCheckInstance:
		if task.Column == nil || task.Check == nil {
			return nil
		}
		event.TaskKind = executionTaskKindColumnCheck
		event.CheckName = task.Check.Name
		event.CheckColumn = task.Column.Name
		event.CheckBlocking = task.Blocking()
	case *scheduler.CustomCheckInstance:
		if task.Check == nil {
			return nil
		}
		event.TaskKind = executionTaskKindCustomCheck
		event.CheckName = task.Check.Name
		event.CheckBlocking = task.Blocking()
	default:
		return nil
	}
	if !startedAt.IsZero() {
		start := startedAt.UTC()
		event.StartedAt = &start
	}
	if !finishedAt.IsZero() {
		finish := finishedAt.UTC()
		event.FinishedAt = &finish
	}
	if err != nil {
		event.Error = err.Error()
	}
	return onEvent(event)
}

var directRunAssetTypes = map[pipeline.AssetType]struct{}{
	pipeline.AssetTypeDuckDBQuery:             {},
	pipeline.AssetTypeDuckDBSeed:              {},
	pipeline.AssetTypeMotherduckQuery:         {},
	pipeline.AssetTypePostgresQuery:           {},
	pipeline.AssetTypePostgresSeed:            {},
	pipeline.AssetTypeRedshiftQuery:           {},
	pipeline.AssetTypeRedshiftSeed:            {},
	pipeline.AssetTypeBigqueryQuery:           {},
	pipeline.AssetTypeBigquerySeed:            {},
	pipeline.AssetTypeAthenaQuery:             {},
	pipeline.AssetTypeAthenaSeed:              {},
	pipeline.AssetTypeDatabricksQuery:         {},
	pipeline.AssetTypeDatabricksSeed:          {},
	pipeline.AssetTypeFabricQuery:             {},
	pipeline.AssetTypeFabricSeed:              {},
	pipeline.AssetTypeFabricQueryLegacy:       {},
	pipeline.AssetTypeFabricSeedLegacy:        {},
	pipeline.AssetTypeMySQLQuery:              {},
	pipeline.AssetTypeMySQLSeed:               {},
	pipeline.AssetTypeSnowflakeQuery:          {},
	pipeline.AssetTypeSnowflakeSeed:           {},
	pipeline.AssetTypeMsSQLQuery:              {},
	pipeline.AssetTypeMsSQLSeed:               {},
	pipeline.AssetTypeSynapseQuery:            {},
	pipeline.AssetTypeSynapseSeed:             {},
	pipeline.AssetTypeClickHouse:              {},
	pipeline.AssetTypeClickHouseSeed:          {},
	pipeline.AssetTypeStarRocksQuery:          {},
	pipeline.AssetTypeStarRocksSeed:           {},
	pipeline.AssetTypeTrinoQuery:              {},
	assetTypeTrinoSeed:                        {},
	pipeline.AssetTypeVerticaQuery:            {},
	pipeline.AssetTypeVerticaSeed:             {},
	pipeline.AssetTypeOracleQuery:             {},
	pipeline.AssetTypeBigqueryQuerySensor:     {},
	pipeline.AssetTypeBigqueryTableSensor:     {},
	pipeline.AssetTypePostgresQuerySensor:     {},
	pipeline.AssetTypePostgresTableSensor:     {},
	pipeline.AssetTypeRedshiftQuerySensor:     {},
	pipeline.AssetTypeRedshiftTableSensor:     {},
	pipeline.AssetTypeMySQLQuerySensor:        {},
	pipeline.AssetTypeMySQLTableSensor:        {},
	pipeline.AssetTypeClickHouseQuerySensor:   {},
	pipeline.AssetTypeClickHouseTableSensor:   {},
	pipeline.AssetTypeStarRocksQuerySensor:    {},
	pipeline.AssetTypeStarRocksTableSensor:    {},
	pipeline.AssetTypeMsSQLQuerySensor:        {},
	pipeline.AssetTypeMsSQLTableSensor:        {},
	pipeline.AssetTypeFabricQuerySensor:       {},
	pipeline.AssetTypeFabricQuerySensorLegacy: {},
	pipeline.AssetTypeFabricTableSensor:       {},
	pipeline.AssetTypeFabricTableSensorLegacy: {},
	pipeline.AssetTypeDatabricksQuerySensor:   {},
	pipeline.AssetTypeDatabricksTableSensor:   {},
	pipeline.AssetTypeDorisSeed:               {},
	pipeline.AssetTypeDorisQuerySensor:        {},
	pipeline.AssetTypeDorisTableSensor:        {},
	pipeline.AssetTypeDremioQuerySensor:       {},
	pipeline.AssetTypeAthenaSQLSensor:         {},
	pipeline.AssetTypeAthenaTableSensor:       {},
	pipeline.AssetTypeDuckDBQuerySensor:       {},
	pipeline.AssetTypeSynapseQuerySensor:      {},
	pipeline.AssetTypeSynapseTableSensor:      {},
	pipeline.AssetTypeSnowflakeQuerySensor:    {},
	pipeline.AssetTypeSnowflakeTableSensor:    {},
	pipeline.AssetTypeTrinoQuerySensor:        {},
	pipeline.AssetTypeSailQuerySensor:         {},
	pipeline.AssetTypeVerticaQuerySensor:      {},
	pipeline.AssetTypeVerticaTableSensor:      {},
	pipeline.AssetTypeS3KeySensor:             {},
	pipeline.AssetTypePython:                  {},
	pipeline.AssetTypeIngestr:                 {},
	pipeline.AssetType(loadAssetType):         {},
	pipeline.AssetType(apiAssetType):          {},
}

// beginRegistryTask registers a main task instance as in flight in the run
// registry, returning its finish callback (a no-op for check instances and
// when the registry is absent, so call sites stay unconditional).
func beginRegistryTask(regRun *runstate.Run, instance scheduler.TaskInstance) func(error) {
	if regRun == nil || instance.GetType() != scheduler.TaskInstanceTypeMain || instance.GetAsset() == nil {
		return func(error) {}
	}
	return regRun.BeginTask(instance.GetAsset().Name)
}

func allDirectRunPipelineDependenciesSucceeded(instance scheduler.TaskInstance) bool {
	for _, upstream := range instance.GetUpstream() {
		if upstream.GetStatus() != scheduler.Succeeded && upstream.GetStatus() != scheduler.Skipped {
			return false
		}
	}
	return true
}

func buildDirectRunAssetContext(ctx context.Context, pp *directPipelineInfo, timeWindow ExecutionTimeWindow, executionTime time.Time, runID string) (context.Context, *sqlparser.SQLParser, func(), error) {
	runCtx := context.WithValue(ctx, pipeline.RunConfigFullRefresh, false)
	runCtx = context.WithValue(runCtx, pipeline.RunConfigStartDate, timeWindow.Start)
	runCtx = context.WithValue(runCtx, pipeline.RunConfigEndDate, timeWindow.End)
	runCtx = context.WithValue(runCtx, pipeline.RunConfigExecutionDate, executionTime.UTC())
	runCtx = context.WithValue(runCtx, pipeline.RunConfigRunID, runID)
	runCtx = context.WithValue(runCtx, config.EnvironmentContextKey, pp.Config.SelectedEnvironment)
	runCtx = context.WithValue(runCtx, config.EnvironmentNameContextKey, pp.Config.SelectedEnvironmentName)
	isDebug := false
	runCtx = context.WithValue(runCtx, bruinexecutor.KeyIsDebug, &isDebug)
	runCtx = context.WithValue(runCtx, bruinexecutor.KeyVerbose, false)
	runCtx = context.WithValue(runCtx, config.SecretsBackendContextKey, "")

	if pp.Config.SelectedEnvironment == nil || pp.Config.SelectedEnvironment.SchemaPrefix == "" {
		return runCtx, nil, func() {}, nil
	}

	parser, err := sqlparser.NewSQLParser(false)
	if err != nil {
		return nil, nil, nil, err
	}

	cleanup := func() {
		parser.Close()
	}

	return runCtx, parser, cleanup, nil
}

func buildDirectRunAssetRenderer(pp *directPipelineInfo, timeWindow ExecutionTimeWindow, executionTime time.Time, runID string) (*jinja.Renderer, error) {
	macroContent, err := jinja.LoadMacros(afero.NewOsFs(), pp.Pipeline.MacrosPath)
	if err != nil {
		return nil, err
	}

	executionTime = executionTime.UTC()
	return jinja.NewRendererWithStartEndDatesAndMacros(
		&timeWindow.Start,
		&timeWindow.End,
		&executionTime,
		pp.Pipeline.Name,
		runID,
		jinja.Context(pp.Pipeline.Variables.Value()),
		macroContent,
	), nil
}
