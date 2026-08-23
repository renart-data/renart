package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/bruin-data/bruin/pkg/pipeline"
	"github.com/spf13/afero"

	"renart/internal/web/bus"
	"renart/internal/web/policy"
	webscheduler "renart/internal/web/scheduler"
	"renart/internal/web/service"
)

// configureExecutionService keeps physical-execution adapters at the
// composition root. Admission and the private run contracts live in
// internal/web/execution; Bruin execution remains behind the service facade
// while that adapter is migrated incrementally.
func configureExecutionService(
	server *webServer,
	workspaceRoot string,
	coordinator *workspaceExecutionCoordinator,
) {
	server.executionSvc = service.NewExecutionService(service.ExecutionDependencies{
		WorkspaceRoot:        workspaceRoot,
		ConfigPath:           resolveConfigFilePath(workspaceRoot),
		Executor:             server.executor,
		ResolveAssetByID:     server.resolveAssetByID,
		ResolveAssetNameByID: server.findAssetNameByID,
		FindInspectIDs:       server.findMaterializationInspectIDs,
		CurrentPipelines: func() []service.PipelineView {
			state := server.currentState()
			pipelines := make([]service.PipelineView, 0, len(state.Pipelines))
			for _, pipeline := range state.Pipelines {
				assets := make([]service.AssetView, 0, len(pipeline.Assets))
				for _, asset := range pipeline.Assets {
					qualityCheckCount := len(asset.CustomChecks)
					for _, column := range asset.Columns {
						qualityCheckCount += len(column.Checks)
					}
					assets = append(assets, service.AssetView{
						ID: asset.ID, Name: asset.Name, QualityCheckCount: qualityCheckCount,
					})
				}
				pipelines = append(pipelines, service.PipelineView{
					ID: pipeline.ID, UUID: pipeline.UUID, Name: pipeline.Name, Assets: assets,
				})
			}
			return pipelines
		},
		Events:       server.eventBus,
		TargetWrites: serverTargetWriteStore{server: server},
		DispatchCompletion: func(ctx context.Context, event bus.RunCompleted) error {
			return server.dispatchRunCompletion(ctx, event)
		},
		AcquireExecutionLease: coordinator.AcquireShared,
		PolicyFor: func(environment string) policy.EnvironmentPolicy {
			if strings.TrimSpace(environment) == "" {
				environment = server.currentState().SelectedEnvironment
			}
			return server.policyLoader.For(environment)
		},
		SelectedEnvironment: func() string { return server.currentState().SelectedEnvironment },
		ParseQueryOutput:    service.ParseQueryJSONOutput,
		NewPipelineBuilder:  server.newPipelineBuilder,
	})
}

// configurePipelinePlanService assembles the planner's filesystem, snapshot,
// staleness, policy, and active-run ports. The reviewed planning workflow itself
// is owned by internal/web/execution.
func configurePipelinePlanService(server *webServer, workspaceRoot string) {
	server.pipelinePlanSvc = service.NewPipelinePlanService(service.PipelinePlanDependencies{
		WorkspaceRoot:    workspaceRoot,
		ConfigPath:       resolveConfigFilePath(workspaceRoot),
		Snapshots:        server.snapshotStore,
		Staleness:        server.stalenessSvc,
		DependencyGraph:  server.resolveWorkspaceDependencyGraph,
		WorkspaceGraph:   server.sqlLSPSvc.WorkspaceGraph,
		CurrentState:     func() service.WorkspaceState { return server.currentState() },
		Fingerprints:     server.fingerprintEngine,
		Materializations: server.matlogStore,
		ResolveProducerDeployment: func(
			ctx context.Context,
			pipelineUUID string,
			environment string,
		) (service.PipelinePlanProducerDeployment, error) {
			selection := service.PipelinePlanProducerDeployment{}
			for _, current := range server.currentState().Pipelines {
				if current.UUID == pipelineUUID {
					selection.PipelineID = current.ID
					selection.PipelineName = current.Name
					break
				}
			}
			if selection.PipelineID == "" {
				return selection, fmt.Errorf("producer pipeline %s is not present in the workspace", pipelineUUID)
			}
			if server.schedulerSvc != nil {
				schedule, variables, found, err := server.schedulerSvc.ResolveEnvScheduleExecutionContext(
					ctx, pipelineUUID, environment,
				)
				if err != nil {
					return selection, err
				}
				selection.ScheduleFound = found
				if found {
					selection.ScheduleStatus = string(schedule.Status)
					selection.VariableOverrides = variables
					if strings.TrimSpace(schedule.SnapshotVersionID) != "" {
						selection.SnapshotVersionID = schedule.SnapshotVersionID
						return selection, nil
					}
				}
			}
			latest, err := server.snapshotStore.Latest(ctx, pipelineUUID)
			if err != nil {
				return selection, err
			}
			if latest != nil {
				selection.SnapshotVersionID = latest.VersionID
			}
			return selection, nil
		},
		ResolvePipelineUUID: server.findPipelineUUIDByID,
		PolicyFor:           server.policyLoader.For,
		ActiveRunID:         server.schedulerStore.ActiveRunID,
		ConflictingRunID: func(
			ctx context.Context,
			pipelineID string,
			pipelineUUID string,
			resources service.PipelinePlanResources,
		) (string, error) {
			claims := make([]webscheduler.PipelineRunResourceClaim, 0, len(resources.Claims))
			for _, claim := range resources.Claims {
				claims = append(claims, webscheduler.PipelineRunResourceClaim{
					Kind: claim.Kind, Identity: claim.Identity,
				})
			}
			return server.schedulerStore.ConflictingRunID(
				ctx,
				pipelineID,
				pipelineUUID,
				webscheduler.PipelineRunPlanResources{
					Isolation: resources.Isolation,
					Claims:    claims,
				},
			)
		},
		NewPipelineBuilder: func() *pipeline.Builder {
			return service.NewRenartPipelineBuilder(afero.NewOsFs())
		},
	})
}
