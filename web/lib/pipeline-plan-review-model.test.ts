import { describe, expect, it } from "vitest";

import type { PipelinePlan, PipelinePlanRequest } from "@/lib/generated/api-types";

import {
  createPipelinePlanRequest,
  derivePipelinePlanReview,
  initialPipelinePlanReviewState,
  pipelinePlanReviewReducer,
  type PipelinePlanReviewState,
} from "./pipeline-plan-review-model";
import { canonicalPipelinePlanReviewedIdentity } from "./api-pipeline-plan";

function plan(overrides: Partial<PipelinePlan> = {}): PipelinePlan {
  return {
    id: "plan-1",
    status: "ready",
    pipeline_id: "pipeline-id",
    pipeline_uuid: "pipeline-uuid",
    pipeline_name: "analytics",
    source: { kind: "working_tree", merkle_root: "a".repeat(64), pipeline_path: "analytics" },
    context: {
      environment: "default",
      start_date: "2026-08-22T00:00:00Z",
      end_date: "2026-08-23T00:00:00Z",
      execution_time: "2026-08-23T10:00:00Z",
      max_active_steps: 1,
      requested_full_refresh: false,
      full_refresh: false,
      backfill: false,
      sensor_mode: "once",
      variables_digest: "variables",
      variable_provenance: [],
      configuration_fidelity: "exact",
      destructive: false,
    },
    readiness: {
      code_checks: {} as PipelinePlan["readiness"]["code_checks"],
      blockers: [],
      warnings: [],
    },
    selection: { mode: "all" },
    prerequisites: [],
    resources: { isolation: "pipeline", claims: [] },
    assets: [],
    execution_contracts: [],
    execution_units: [],
    summary: {
      assets: 2,
      execution_units: 2,
      stages: 2,
      destructive_operations: 0,
      blockers: 0,
      warnings: 0,
    },
    ...overrides,
  };
}

describe("pipeline plan review model", () => {
  it("carries the reviewed semantic impact digest into confirmation", () => {
    const reviewed = canonicalPipelinePlanReviewedIdentity(
      plan({
        semantic_impact: {
          version: "v1",
          digest: "v1:semantic",
          status: "available",
          complete: true,
          assets: [],
          summary: {
            added: 0,
            removed: 0,
            modified: 0,
            formatting_only: 0,
            behavior_changes: 0,
            schema_changes: 0,
            incomplete: 0,
            warnings: 0,
          },
        },
      }),
    );
    expect(reviewed.semantic_impact_digest).toBe("v1:semantic");
  });

  it("creates a deterministic request for run and deployment review", () => {
    expect(
      createPipelinePlanRequest({
        intent: "run",
        environment: "staging",
        timeWindow: { start: "start", end: "end" },
        sourceKind: "snapshot",
        sourceVersion: "deployment-2",
        initialSelection: { mode: "asset", asset_name: "analytics.orders" },
        executionTime: "now",
      }),
    ).toEqual({
      purpose: "execution",
      environment: "staging",
      start_date: "start",
      end_date: "end",
      execution_time: "now",
      sensor_mode: "once",
      source: { kind: "snapshot", version_id: "deployment-2" },
      selection: { mode: "asset", asset_name: "analytics.orders" },
    });

    expect(
      createPipelinePlanRequest({
        intent: "deploy",
        environment: "",
        executionTime: "now",
      }),
    ).toMatchObject({ purpose: "deployment", selection: { mode: "all" } });
  });

  it("reduces planning transitions without leaving stale loading or error state", () => {
    const request: PipelinePlanRequest = {
      purpose: "execution",
      selection: { mode: "selector", selector: "tag:daily" },
    };
    const loadedPlan = plan({ selection: { mode: "selector", selector: "tag:daily" } });
    let state = pipelinePlanReviewReducer(initialPipelinePlanReviewState, { type: "opened" });
    state = pipelinePlanReviewReducer(state, {
      type: "plan_load_failed",
      message: "temporary failure",
    });
    state = pipelinePlanReviewReducer(state, {
      type: "plan_load_started",
      includeStageContent: false,
    });
    state = pipelinePlanReviewReducer(state, {
      type: "plan_loaded",
      plan: loadedPlan,
      request,
      includeStageContent: false,
    });

    expect(state).toMatchObject({
      plan: loadedPlan,
      request,
      selectorDraft: "tag:daily",
      loading: false,
      contentLoading: false,
      stageContentLoaded: false,
      error: null,
    });

    state = pipelinePlanReviewReducer(state, {
      type: "request_changed",
      request: { ...request, include_stage_content: true },
    });
    expect(state.stageContentLoaded).toBe(false);
  });

  it("requires reviewed selectors and destructive confirmation before enabling the action", () => {
    const destructivePlan = plan({
      context: { ...plan().context, destructive: true },
      selection: { mode: "selector", selector: "tag:daily" },
    });
    let state: PipelinePlanReviewState = {
      ...initialPipelinePlanReviewState,
      plan: destructivePlan,
      request: { selection: { mode: "selector", selector: "tag:daily" } },
      selectorDraft: "tag:changed",
    };

    expect(
      derivePipelinePlanReview(state, {
        intent: "run",
        confirmDestructive: true,
        deploymentExists: false,
      }).canConfirm,
    ).toBe(false);

    state = pipelinePlanReviewReducer(state, {
      type: "selector_draft_changed",
      selector: "tag:daily",
    });
    state = pipelinePlanReviewReducer(state, {
      type: "confirmation_changed",
      confirmation: "default",
    });
    const derived = derivePipelinePlanReview(state, {
      intent: "run",
      confirmDestructive: true,
      deploymentExists: false,
    });
    expect(derived).toMatchObject({
      selectorDraftApplied: true,
      selectorPlanIsCurrent: true,
      confirmationMatches: true,
      canConfirm: true,
    });
  });

  it("keeps blocked plans and completed deployments non-confirmable", () => {
    const blocked = plan({ status: "blocked" });
    const state = { ...initialPipelinePlanReviewState, plan: blocked };
    expect(
      derivePipelinePlanReview(state, {
        intent: "deploy",
        confirmDestructive: false,
        deploymentExists: false,
      }).canConfirm,
    ).toBe(false);

    expect(
      derivePipelinePlanReview(
        { ...state, plan: plan() },
        { intent: "deploy", confirmDestructive: false, deploymentExists: true },
      ).canConfirm,
    ).toBe(false);
  });
});
