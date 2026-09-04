import { APIError, fetchJSONWithBody } from "@/lib/api-core";
import type {
  PipelinePlan,
  PipelinePlanConfirmRequest,
  PipelinePlanReviewedIdentity,
  PipelinePlanRequest,
} from "@/lib/generated/api-types";
import type { PipelineRun } from "@/lib/types";

export type PipelinePlanConfirmResponse = {
  status: "ok";
  plan_id: string;
  run: PipelineRun;
  preview_units_omitted: number;
};

export async function planPipeline(pipelineId: string, input: PipelinePlanRequest) {
  return fetchJSONWithBody<PipelinePlan>(`/api/pipelines/${pipelineId}/plan`, "POST", input);
}

export async function confirmPipelinePlan(pipelineId: string, input: PipelinePlanConfirmRequest) {
  return fetchJSONWithBody<PipelinePlanConfirmResponse>(
    `/api/pipelines/${pipelineId}/plan/confirm`,
    "POST",
    input,
  );
}

export function canonicalPipelinePlanRequest(
  plan: PipelinePlan,
  includeStageContent = false,
): PipelinePlanRequest {
  return {
    environment: plan.context.environment,
    start_date: plan.context.start_date,
    end_date: plan.context.end_date,
    execution_time: plan.context.execution_time,
    full_refresh: plan.context.requested_full_refresh,
    backfill: plan.context.backfill,
    sensor_mode: plan.context.sensor_mode,
    source: {
      kind: plan.source.kind,
      version_id: plan.source.version_id,
    },
    selection: {
      mode: plan.selection.mode,
      asset_name: plan.selection.asset_name,
      scope: plan.selection.scope,
      selector: plan.selection.selector,
    },
    include_stage_content: includeStageContent,
  };
}

export function canonicalPipelinePlanReviewedIdentity(
  plan: PipelinePlan,
): PipelinePlanReviewedIdentity {
  return {
    pipeline_uuid: plan.pipeline_uuid,
    source: plan.source,
    context: plan.context,
    selection: plan.selection,
    semantic_impact_digest: plan.semantic_impact?.digest,
    prerequisites: plan.prerequisites,
    resources: plan.resources,
    execution_contracts: plan.execution_contracts,
    execution_units: plan.execution_units,
  };
}

export function pipelinePlanFromConflict(error: unknown): PipelinePlan | null {
  if (!(error instanceof APIError) || error.status !== 409) return null;
  if (
    error.code !== "plan_stale" &&
    error.code !== "plan_data_changed" &&
    error.code !== "plan_blocked"
  )
    return null;
  if (!error.details || typeof error.details !== "object") return null;
  const plan = (error.details as Record<string, unknown>).plan;
  if (!plan || typeof plan !== "object" || typeof (plan as PipelinePlan).id !== "string") {
    return null;
  }
  return plan as PipelinePlan;
}

export type { PipelinePlan, PipelinePlanRequest };
