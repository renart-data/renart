import type {
  PipelinePlan,
  PipelinePlanRequest,
  PipelinePlanSelectionRequest,
} from "@/lib/generated/api-types";

export type PlanSelectionMode = "all" | "needed" | "asset" | "selector" | "selector_needed";
export type SensorMode = "once" | "wait" | "skip";
export type PlanIntent = "run" | "deploy";

export type PipelinePlanReviewState = {
  request: PipelinePlanRequest | null;
  plan: PipelinePlan | null;
  loading: boolean;
  contentLoading: boolean;
  stageContentLoaded: boolean;
  error: string | null;
  confirming: boolean;
  confirmation: string;
  activeRunId: string | null;
  selectorDraft: string;
  runOptionsOpen: boolean;
};

export type PipelinePlanReviewEvent =
  | { type: "opened" }
  | { type: "request_set"; request: PipelinePlanRequest }
  | { type: "request_changed"; request: PipelinePlanRequest }
  | { type: "plan_load_started"; includeStageContent: boolean }
  | {
      type: "plan_loaded";
      plan: PipelinePlan;
      request: PipelinePlanRequest;
      includeStageContent: boolean;
    }
  | { type: "plan_load_failed"; message: string }
  | { type: "confirmation_changed"; confirmation: string }
  | { type: "selector_draft_changed"; selector: string }
  | { type: "run_options_changed"; open: boolean }
  | { type: "confirm_started" }
  | { type: "confirm_finished" }
  | { type: "confirm_failed"; message: string; activeRunId?: string }
  | {
      type: "plan_refreshed";
      plan: PipelinePlan;
      request: PipelinePlanRequest;
      message: string;
    };

export const initialPipelinePlanReviewState: PipelinePlanReviewState = {
  request: null,
  plan: null,
  loading: false,
  contentLoading: false,
  stageContentLoaded: false,
  error: null,
  confirming: false,
  confirmation: "",
  activeRunId: null,
  selectorDraft: "*",
  runOptionsOpen: false,
};

export function pipelinePlanReviewReducer(
  state: PipelinePlanReviewState,
  event: PipelinePlanReviewEvent,
): PipelinePlanReviewState {
  switch (event.type) {
    case "opened":
      return { ...initialPipelinePlanReviewState, loading: true };
    case "request_set":
      return { ...state, request: event.request };
    case "request_changed":
      return { ...state, request: event.request, stageContentLoaded: false };
    case "plan_load_started":
      return {
        ...state,
        loading: event.includeStageContent ? state.loading : true,
        contentLoading: event.includeStageContent ? true : state.contentLoading,
        error: null,
        activeRunId: null,
      };
    case "plan_loaded":
      return {
        ...state,
        plan: event.plan,
        request: event.request,
        selectorDraft: isSelectorMode(event.plan.selection.mode)
          ? (event.plan.selection.selector ?? "")
          : state.selectorDraft,
        loading: false,
        contentLoading: false,
        stageContentLoaded: event.includeStageContent,
        error: null,
        activeRunId: null,
      };
    case "plan_load_failed":
      return {
        ...state,
        loading: false,
        contentLoading: false,
        error: event.message,
      };
    case "confirmation_changed":
      return { ...state, confirmation: event.confirmation };
    case "selector_draft_changed":
      return { ...state, selectorDraft: event.selector };
    case "run_options_changed":
      return { ...state, runOptionsOpen: event.open };
    case "confirm_started":
      return { ...state, confirming: true, error: null, activeRunId: null };
    case "confirm_finished":
      return { ...state, confirming: false };
    case "confirm_failed":
      return {
        ...state,
        error: event.message,
        activeRunId: event.activeRunId ?? null,
      };
    case "plan_refreshed":
      return {
        ...state,
        plan: event.plan,
        request: event.request,
        selectorDraft: isSelectorMode(event.plan.selection.mode)
          ? (event.plan.selection.selector ?? "")
          : state.selectorDraft,
        stageContentLoaded: false,
        error: event.message,
        activeRunId: null,
      };
  }
}

export function createPipelinePlanRequest({
  intent,
  environment,
  timeWindow,
  sourceKind,
  sourceVersion,
  initialSelection,
  executionTime,
}: {
  intent: PlanIntent;
  environment: string;
  timeWindow?: { start: string; end: string } | null;
  sourceKind?: string;
  sourceVersion?: string;
  initialSelection?: PipelinePlanSelectionRequest | null;
  executionTime: string;
}): PipelinePlanRequest {
  return {
    purpose: intent === "deploy" ? "deployment" : "execution",
    environment: environment || undefined,
    start_date: timeWindow?.start,
    end_date: timeWindow?.end,
    execution_time: executionTime,
    sensor_mode: "once",
    source: sourceKind ? { kind: sourceKind, version_id: sourceVersion } : undefined,
    selection: initialSelection ? { ...initialSelection } : { mode: "all" },
  };
}

export type PipelinePlanReviewDerived = {
  selectionMode: PlanSelectionMode;
  selectorMode: boolean;
  appliedSelector: string;
  selectorDraftApplied: boolean;
  selectorPlanIsCurrent: boolean;
  sensorMode: SensorMode;
  fullRefresh: boolean;
  destructiveConfirmationRequired: boolean;
  confirmationMatches: boolean;
  hasBlockers: boolean;
  canConfirm: boolean;
};

export function derivePipelinePlanReview(
  state: PipelinePlanReviewState,
  {
    intent,
    confirmDestructive,
    deploymentExists,
  }: { intent: PlanIntent; confirmDestructive: boolean; deploymentExists: boolean },
): PipelinePlanReviewDerived {
  const selectionMode = asSelectionMode(
    state.request?.selection?.mode ?? state.plan?.selection.mode,
  );
  const selectorMode = isSelectorMode(selectionMode);
  const appliedSelector = state.request?.selection?.selector?.trim() ?? "";
  const selectorDraftApplied = !selectorMode || state.selectorDraft.trim() === appliedSelector;
  const selectorPlanIsCurrent = Boolean(
    selectorMode &&
    state.plan?.selection.mode === selectionMode &&
    state.plan.selection.selector === appliedSelector,
  );
  const sensorMode = asSensorMode(state.request?.sensor_mode ?? state.plan?.context.sensor_mode);
  const fullRefresh = Boolean(
    state.request?.full_refresh ?? state.plan?.context.requested_full_refresh,
  );
  const destructiveConfirmationRequired = Boolean(
    intent === "run" && confirmDestructive && state.plan?.context.destructive,
  );
  const confirmationMatches =
    !destructiveConfirmationRequired ||
    state.confirmation.trim() === state.plan?.context.environment;
  const hasBlockers = Boolean(
    state.plan && (state.plan.status === "blocked" || state.plan.readiness.blockers.length > 0),
  );
  const canConfirm = Boolean(
    state.plan &&
    !hasBlockers &&
    confirmationMatches &&
    selectorDraftApplied &&
    !state.loading &&
    !state.error &&
    !deploymentExists &&
    (intent === "deploy" ? state.plan.summary.assets > 0 : state.plan.summary.execution_units > 0),
  );

  return {
    selectionMode,
    selectorMode,
    appliedSelector,
    selectorDraftApplied,
    selectorPlanIsCurrent,
    sensorMode,
    fullRefresh,
    destructiveConfirmationRequired,
    confirmationMatches,
    hasBlockers,
    canConfirm,
  };
}

export function planSelectionLabel(mode: PlanSelectionMode) {
  switch (mode) {
    case "asset":
      return "Selected asset";
    case "needed":
      return "Needed assets";
    case "selector":
      return "Matching selector";
    case "selector_needed":
      return "Needed matching selector";
    default:
      return "Entire pipeline";
  }
}

export function sensorModeLabel(mode: SensorMode) {
  switch (mode) {
    case "wait":
      return "wait for sensors";
    case "skip":
      return "skip sensors";
    default:
      return "check sensors once";
  }
}

function isSelectorMode(mode?: string): mode is "selector" | "selector_needed" {
  return mode === "selector" || mode === "selector_needed";
}

function asSelectionMode(mode?: string): PlanSelectionMode {
  return mode === "needed" || mode === "asset" || mode === "selector" || mode === "selector_needed"
    ? mode
    : "all";
}

function asSensorMode(mode?: string): SensorMode {
  return mode === "wait" || mode === "skip" ? mode : "once";
}
