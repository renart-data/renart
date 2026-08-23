package service

import webexecution "renart/internal/web/execution"

type AssetRenderStatus = webexecution.RenderStatus
type AssetRenderStageStatus = webexecution.RenderStageStatus
type AssetRenderFidelity = webexecution.RenderFidelity
type AssetRenderRequest = webexecution.RenderRequest
type AssetRenderSource = webexecution.RenderSource
type AssetRenderContext = webexecution.RenderContext
type AssetRenderVariableProvenance = webexecution.VariableProvenance
type AssetRenderProvenance = webexecution.RenderProvenance
type AssetRenderTarget = webexecution.RenderTarget
type AssetRenderWriteResource = webexecution.WriteResource
type AssetRenderAsset = webexecution.RenderAsset
type AssetRenderStage = webexecution.RenderStage
type AssetRenderRedaction = webexecution.RenderRedaction
type AssetRenderIssue = webexecution.RenderIssue
type AssetRenderResult = webexecution.RenderResult

const (
	AssetRenderStatusOK          = webexecution.RenderStatusOK
	AssetRenderStatusPartial     = webexecution.RenderStatusPartial
	AssetRenderStatusUnsupported = webexecution.RenderStatusUnsupported
	AssetRenderStatusError       = webexecution.RenderStatusError

	AssetRenderStageStatusOK          = webexecution.RenderStageStatusOK
	AssetRenderStageStatusUnsupported = webexecution.RenderStageStatusUnsupported
	AssetRenderStageStatusError       = webexecution.RenderStageStatusError

	AssetRenderFidelityExact       = webexecution.RenderFidelityExact
	AssetRenderFidelitySemantic    = webexecution.RenderFidelitySemantic
	AssetRenderFidelityRuntimeOnly = webexecution.RenderFidelityRuntimeOnly
	AssetRenderFidelityUnsupported = webexecution.RenderFidelityUnsupported
)

type PipelinePlanSourceRequest = webexecution.PlanSourceRequest
type PipelinePlanSelectionRequest = webexecution.PlanSelectionRequest
type PipelinePlanRequest = webexecution.PlanRequest
type PipelinePlanConfirmRequest = webexecution.PlanConfirmRequest
type PipelinePlanReviewedIdentity = webexecution.ReviewedIdentity
type PipelinePlanContext = webexecution.PlanContext
type PipelinePlanIssue = webexecution.PlanIssue
type PipelinePlanReadiness = webexecution.PlanReadiness
type PipelinePlanSelection = webexecution.PlanSelection
type PipelinePlanRender = webexecution.PlanRender
type PipelinePlanAsset = webexecution.PlanAsset
type PipelinePlanExecutionUnit = webexecution.PlanExecutionUnit
type PipelinePlanPrerequisite = webexecution.Prerequisite
type PipelinePlanProducerDeployment = webexecution.ProducerDeployment
type PipelinePlanResourceClaim = webexecution.ResourceClaim
type PipelinePlanResources = webexecution.Resources
type PipelinePlanExecutionContract = webexecution.ExecutionContract
type PipelinePlanSummary = webexecution.PlanSummary
type PipelinePlan = webexecution.Plan

type ExecutionCoverageMode = webexecution.CoverageMode
type ExecutionUpstreamSnapshot = webexecution.UpstreamSnapshot
type ExecutionTargetSnapshot = webexecution.TargetSnapshot
type ExecutionTargetSnapshotEntry = webexecution.TargetSnapshotEntry
type PipelineRunSpec = webexecution.RunSpec
type PipelineExecutionPlan = webexecution.ExecutionPlan
type PipelineExecutionUnit = webexecution.ExecutionUnit
type PipelineExecutionUnitEvent = webexecution.ExecutionUnitEvent
type ResolvedPipelineRunContext = webexecution.ResolvedRunContext

const (
	PipelinePlanStatusReady   = webexecution.PlanStatusReady
	PipelinePlanStatusWarning = webexecution.PlanStatusWarning
	PipelinePlanStatusBlocked = webexecution.PlanStatusBlocked

	PipelinePlanSourceWorkingTree = webexecution.PlanSourceWorkingTree
	PipelinePlanSourceSnapshot    = webexecution.PlanSourceSnapshot
	PipelinePlanPurposeExecution  = webexecution.PlanPurposeExecution
	PipelinePlanPurposeDeployment = webexecution.PlanPurposeDeployment

	PipelinePlanSelectionNeeded         = webexecution.PlanSelectionNeeded
	PipelinePlanSelectionAll            = webexecution.PlanSelectionAll
	PipelinePlanSelectionAsset          = webexecution.PlanSelectionAsset
	PipelinePlanSelectionSelector       = webexecution.PlanSelectionSelector
	PipelinePlanSelectionSelectorNeeded = webexecution.PlanSelectionSelectorNeeded

	PipelinePlanResourceIsolationResources = webexecution.PlanResourceIsolationResources
	PipelinePlanResourceIsolationPipeline  = webexecution.PlanResourceIsolationPipeline

	PipelinePlanPrerequisiteReady   = webexecution.PlanPrerequisiteReady
	PipelinePlanPrerequisiteBlocked = webexecution.PlanPrerequisiteBlocked

	ExecutionTargetSnapshotVersion = webexecution.ExecutionTargetSnapshotVersion
	PipelineExecutionPlanVersionV3 = webexecution.ExecutionPlanVersionV3

	ExecutionCoverageMarker          = webexecution.CoverageMarker
	ExecutionCoverageUnionIntervals  = webexecution.CoverageUnionIntervals
	ExecutionCoverageReplaceInterval = webexecution.CoverageReplaceInterval
)
