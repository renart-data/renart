"use client";

import { Link } from "@tanstack/react-router";
import { ResourceLink } from "./resource-link";
import {
  AlertTriangle,
  CheckCircle2,
  ChevronRight,
  FileCode2,
  Loader2,
  Package,
  Play,
  ShieldAlert,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";

import { StalenessBadge } from "@/components/app/app-primitives";
import { ReadOnlyRenderedOperationDiff } from "@/components/app/asset-render-view";
import { SemanticAssetImpactRow } from "@/components/app/semantic-impact-review";
import { useIsMobile } from "@/hooks/use-mobile";
import { deploymentDiffAnnotations } from "@/lib/deployment-diff-annotations";
import type { DeploymentReviewRow } from "@/lib/deployment-review";
import {
  buildDeploymentReview,
  deploymentRowSummary,
  deploymentRowTone,
} from "@/lib/deployment-review";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Field,
  FieldContent,
  FieldDescription,
  FieldGroup,
  FieldLabel,
} from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { APIError } from "@/lib/api-core";
import {
  getDeploymentFileDiff,
  getDeployStatus,
  type DeploymentFileDiff,
  type DeployResponse,
  type DeployStatus,
} from "@/lib/api-deploy";
import {
  getEnvSchedules,
  type EnvSchedule,
  type EnvSchedulePinSelection,
  type SchedulerOwnership,
} from "@/lib/api-env-schedules";
import {
  canonicalPipelinePlanRequest,
  canonicalPipelinePlanReviewedIdentity,
  confirmPipelinePlan,
  pipelinePlanFromConflict,
  planPipeline,
  type PipelinePlan,
  type PipelinePlanRequest,
} from "@/lib/api-pipeline-plan";
import { activePipelineRunConflict, type PipelineRunSource } from "@/lib/api-scheduler";
import type { AssetStaleness } from "@/lib/api-staleness";
import type { PipelineRun } from "@/lib/types";
import type { PipelinePlanIssue, PipelinePlanSelectionRequest } from "@/lib/generated/api-types";
import {
  createPipelinePlanRequest,
  derivePipelinePlanReview,
  initialPipelinePlanReviewState,
  pipelinePlanReviewReducer,
  sensorModeLabel,
  type PlanIntent,
  type SensorMode,
} from "@/lib/pipeline-plan-review-model";
import { awaitWorkspaceSaves } from "@/lib/workspace-save-barrier";
import { cn } from "@/lib/utils";
import { deploymentLabel } from "@/lib/deployment-label";

// Issues the dialog already shows as a dedicated control rather than as a
// problem: the conflicting-run alert and the typed destructive confirmation.
const issuesShownElsewhere = new Set([
  "pipeline_already_running",
  "destructive_confirmation_required",
]);
const collapsedAssetCount = 8;

export function PipelinePlanSheet({
  open,
  onOpenChange,
  pipelineId,
  pipelineName,
  environment,
  timeWindow,
  source,
  initialSelection,
  intent = "run",
  confirmDestructive = false,
  offerScheduleUpdates = true,
  onAccepted,
  onDeploy,
  onSchedulesChanged,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  pipelineId: string;
  pipelineName: string;
  environment: string;
  timeWindow?: { start: string; end: string } | null;
  source?: PipelineRunSource | null;
  initialSelection?: PipelinePlanSelectionRequest | null;
  intent?: PlanIntent;
  confirmDestructive?: boolean;
  /** Offer to move this pipeline's schedules to the new deployment. */
  offerScheduleUpdates?: boolean;
  onAccepted?: (run: PipelineRun, plan: PipelinePlan) => void;
  onDeploy?: (
    expectedSourceMerkle: string,
    schedules: EnvSchedulePinSelection[],
  ) => Promise<DeployResponse>;
  onSchedulesChanged?: () => void | Promise<void>;
}) {
  const [review, dispatchReview] = useReducer(
    pipelinePlanReviewReducer,
    initialPipelinePlanReviewState,
  );
  const {
    request,
    plan,
    loading,
    error,
    confirming,
    confirmation,
    activeRunId,
    selectorDraft,
    runOptionsOpen,
  } = review;
  const closingForDetail = useRef(false);
  const [deployStatus, setDeployStatus] = useState<DeployStatus | null>(null);
  const [deployment, setDeployment] = useState<DeployResponse | null>(null);
  const [schedules, setSchedules] = useState<EnvSchedule[]>([]);
  const [schedulerOwnership, setSchedulerOwnership] = useState<SchedulerOwnership | null>(null);
  // Null until the user changes the selection; until then the defaults apply.
  const [chosenSchedules, setChosenSchedules] = useState<Set<string> | null>(null);
  const requestSerial = useRef(0);
  const initialPlanContext = useRef<string | null>(null);
  const requestedSourceKind = intent === "deploy" ? "working_tree" : source?.source;
  const requestedSourceVersion =
    intent !== "deploy" && source?.source === "snapshot" ? source.snapshot_version_id : undefined;
  const loadSchedules = intent === "deploy" && offerScheduleUpdates;

  const fetchPlan = useCallback(
    async (input: PipelinePlanRequest) => {
      const serial = ++requestSerial.current;
      dispatchReview({ type: "plan_load_started" });
      try {
        const next = await planPipeline(pipelineId, input);
        if (serial !== requestSerial.current) return;
        dispatchReview({
          type: "plan_loaded",
          plan: next,
          request: { ...canonicalPipelinePlanRequest(next, false), purpose: input.purpose },
        });
      } catch (cause) {
        if (serial !== requestSerial.current) return;
        dispatchReview({
          type: "plan_load_failed",
          message: cause instanceof Error ? cause.message : "Pipeline planning failed.",
        });
      }
    },
    [pipelineId],
  );

  useEffect(() => {
    if (!open) {
      initialPlanContext.current = null;
      requestSerial.current += 1;
      return;
    }
    if (initialPlanContext.current !== null) return;
    initialPlanContext.current = "open";
    dispatchReview({ type: "opened" });
    setDeployStatus(null);
    setDeployment(null);
    setSchedules([]);
    setSchedulerOwnership(null);
    setChosenSchedules(null);
    const serial = ++requestSerial.current;
    void (async () => {
      try {
        await awaitWorkspaceSaves();
        if (serial !== requestSerial.current) return;
        const input = createPipelinePlanRequest({
          intent,
          environment,
          timeWindow,
          sourceKind: requestedSourceKind,
          sourceVersion: requestedSourceVersion,
          initialSelection,
          executionTime: new Date().toISOString(),
        });
        dispatchReview({ type: "request_set", request: input });
        if (intent === "deploy") {
          const [statusResponse, scheduleResponse] = await Promise.all([
            getDeployStatus(pipelineId),
            loadSchedules ? getEnvSchedules() : Promise.resolve(null),
          ]);
          if (serial !== requestSerial.current) return;
          setDeployStatus(statusResponse);
          setSchedules(scheduleResponse?.schedules ?? []);
          setSchedulerOwnership(scheduleResponse?.scheduler ?? null);
        }
        await fetchPlan(input);
      } catch (cause) {
        if (serial !== requestSerial.current) return;
        dispatchReview({
          type: "plan_load_failed",
          message: cause instanceof Error ? cause.message : "Saving the workspace failed.",
        });
      }
    })();
  }, [
    environment,
    fetchPlan,
    intent,
    initialSelection?.asset_name,
    initialSelection?.mode,
    initialSelection?.scope,
    initialSelection?.selector,
    loadSchedules,
    open,
    pipelineId,
    requestedSourceKind,
    requestedSourceVersion,
    timeWindow?.end,
    timeWindow?.start,
  ]);

  const updateRequest = (update: (current: PipelinePlanRequest) => PipelinePlanRequest) => {
    if (!request) return;
    const next = update(request);
    dispatchReview({ type: "request_changed", request: next });
    void fetchPlan(next);
  };

  const {
    selectionMode,
    selectorMode,
    appliedSelector,
    selectorDraftApplied,
    sensorMode,
    fullRefresh,
    destructiveConfirmationRequired,
    canConfirm,
  } = derivePipelinePlanReview(review, {
    intent,
    confirmDestructive,
    deploymentExists: Boolean(deployment),
  });
  const neededOnly = selectionMode === "needed" || selectionMode === "selector_needed";

  // Needed/All and the optional filter combine into the four plan selections.
  const selectAssets = (needed: boolean, selector: string) =>
    updateRequest((current) => ({
      ...current,
      selection: selector
        ? { mode: needed ? "selector_needed" : "selector", selector }
        : { mode: needed ? "needed" : "all" },
    }));

  const pipelineSchedules = useMemo(
    () =>
      plan
        ? schedules.filter(
            (schedule) =>
              schedule.pipeline_uuid === plan.pipeline_uuid && schedule.status !== "archived",
          )
        : [],
    [plan, schedules],
  );
  const canUpdateSchedules = loadSchedules && schedulerOwnership?.state === "owner";
  const scheduleSelection = useMemo(
    () => chosenSchedules ?? defaultScheduleSelection(pipelineSchedules, deployStatus),
    [chosenSchedules, deployStatus, pipelineSchedules],
  );
  const selectedPins: EnvSchedulePinSelection[] = canUpdateSchedules
    ? pipelineSchedules
        .filter((schedule) => scheduleSelection.has(schedule.environment))
        .map((schedule) => ({
          environment: schedule.environment,
          expected_snapshot_version_id: schedule.snapshot_version_id ?? "",
        }))
    : [];

  const confirm = async () => {
    if (!plan || !canConfirm) return;
    dispatchReview({ type: "confirm_started" });
    try {
      if (intent === "deploy") {
        if (!onDeploy) {
          throw new Error("Deployment is unavailable.");
        }
        const response = await onDeploy(plan.source.merkle_root, selectedPins);
        setDeployment(response);
        if (response.schedules?.length) await onSchedulesChanged?.();
        return;
      }
      const response = await confirmPipelinePlan(pipelineId, {
        plan_id: plan.id,
        plan: canonicalPipelinePlanRequest(plan, false),
        reviewed: canonicalPipelinePlanReviewedIdentity(plan),
        confirmed_environment: destructiveConfirmationRequired ? confirmation.trim() : undefined,
      });
      onAccepted?.(response.run, plan);
      onOpenChange(false);
    } catch (cause) {
      if (
        intent === "deploy" &&
        cause instanceof APIError &&
        cause.code === "deployment_source_changed" &&
        request
      ) {
        dispatchReview({
          type: "confirm_failed",
          message: "The saved files changed after review. Check the refreshed changes.",
        });
        setDeployStatus(await getDeployStatus(pipelineId));
        await fetchPlan(request);
        return;
      }
      const refreshed = pipelinePlanFromConflict(cause);
      if (refreshed) {
        dispatchReview({
          type: "plan_refreshed",
          plan: refreshed,
          request: {
            ...canonicalPipelinePlanRequest(refreshed, false),
            purpose: request?.purpose,
          },
          message:
            cause instanceof APIError && cause.code === "plan_data_changed"
              ? "Some assets changed state since you opened this. Check the refreshed list before running."
              : cause instanceof APIError && cause.code === "plan_stale"
                ? "The saved files or settings changed. Check the refreshed list before running."
                : cause instanceof Error
                  ? cause.message
                  : "The refreshed plan is blocked.",
        });
        return;
      }
      const active = activePipelineRunConflict(cause);
      if (active) {
        dispatchReview({
          type: "confirm_failed",
          message: "Another run started first.",
          activeRunId: active.activeRunId,
        });
        return;
      }
      dispatchReview({
        type: "confirm_failed",
        message: cause instanceof Error ? cause.message : "Pipeline run could not be started.",
      });
    } finally {
      dispatchReview({ type: "confirm_finished" });
    }
  };

  const title =
    intent === "deploy"
      ? `Deploy ${pipelineName}`
      : initialSelection?.mode === "asset" && initialSelection.asset_name
        ? `Run ${initialSelection.asset_name}`
        : `Run ${pipelineName}`;
  const movingPins = selectedPins.filter(
    (pin) => !deployStatus?.in_sync || pin.expected_snapshot_version_id !== deployStatus.version_id,
  ).length;
  const actionLabel = plan
    ? intent === "deploy"
      ? deployStatus?.has_snapshot && deployStatus.in_sync
        ? movingPins > 0
          ? `Update ${movingPins} ${movingPins === 1 ? "schedule" : "schedules"}`
          : "Deploy"
        : selectedPins.length > 0
          ? `Deploy and update ${selectedPins.length} ${selectedPins.length === 1 ? "schedule" : "schedules"}`
          : "Deploy"
      : `Run ${plan.summary.assets} ${plan.summary.assets === 1 ? "asset" : "assets"}`
    : intent === "deploy"
      ? "Deploy"
      : "Run";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        onClickCapture={(event) => {
          if (
            event.button === 0 &&
            !event.ctrlKey &&
            !event.metaKey &&
            !event.shiftKey &&
            !event.altKey &&
            event.target instanceof Element &&
            event.target.closest('[data-resource-link="true"]')
          ) {
            closingForDetail.current = true;
            onOpenChange(false);
          }
        }}
        onCloseAutoFocus={(event) => {
          if (closingForDetail.current) event.preventDefault();
          closingForDetail.current = false;
        }}
        className={cn(
          "flex h-auto max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-none flex-col gap-0 overflow-hidden p-0",
          intent === "deploy" ? "sm:max-w-[960px]" : "sm:max-w-2xl",
        )}
        data-testid="pipeline-plan-sheet"
      >
        <ScrollArea
          className="min-h-0 flex-1"
          data-testid="pipeline-plan-scroll"
          viewportClassName="[&>div]:!block"
        >
          <DialogHeader className="border-b px-5 py-4 pr-12">
            <div className="flex min-w-0 items-center gap-2">
              <DialogTitle className="truncate">{title}</DialogTitle>
              {loading && plan ? <Loader2 className="size-3.5 animate-spin" /> : null}
            </div>
            <DialogDescription className="truncate text-xs">
              {intent === "deploy"
                ? deployContextLabel(deployStatus)
                : runContextLabel(plan, environment, source, timeWindow)}
            </DialogDescription>
          </DialogHeader>

          {deployment ? (
            <DeploymentResult
              deployment={deployment}
              schedulesOffered={canUpdateSchedules && pipelineSchedules.length > 0}
            />
          ) : intent === "run" ? (
            <div className="flex min-w-0 flex-col gap-4 px-5 py-4">
              <RunScope
                assetMode={selectionMode === "asset"}
                neededOnly={neededOnly}
                disabled={!request}
                onChange={(needed) => selectAssets(needed, selectorMode ? appliedSelector : "")}
              />
              {plan ? (
                <RunPlanReview
                  plan={plan}
                  loading={loading}
                  neededOnly={neededOnly}
                  onRunAll={() => selectAssets(false, selectorMode ? appliedSelector : "")}
                />
              ) : (
                <PlanPending loading={loading} error={error} intent={intent} />
              )}
              {request ? (
                <RunOptions
                  open={runOptionsOpen}
                  onOpenChange={(nextOpen) =>
                    dispatchReview({ type: "run_options_changed", open: nextOpen })
                  }
                  assetMode={selectionMode === "asset"}
                  selectorDraft={selectorDraft}
                  appliedSelector={appliedSelector}
                  selectorDraftApplied={selectorDraftApplied}
                  loading={loading}
                  sensorMode={sensorMode}
                  fullRefresh={fullRefresh}
                  onSelectorDraftChange={(selector) =>
                    dispatchReview({ type: "selector_draft_changed", selector })
                  }
                  onApplySelector={() => selectAssets(neededOnly, selectorDraft.trim())}
                  onSensorModeChange={(mode) =>
                    updateRequest((current) => ({ ...current, sensor_mode: mode }))
                  }
                  onFullRefreshChange={(checked) =>
                    updateRequest((current) => ({ ...current, full_refresh: checked }))
                  }
                />
              ) : null}
            </div>
          ) : plan ? (
            <DeployPlanReview
              pipelineId={pipelineId}
              plan={plan}
              status={deployStatus}
              schedulesOffered={loadSchedules}
              schedules={pipelineSchedules}
              ownership={schedulerOwnership}
              selected={scheduleSelection}
              onSelectedChange={setChosenSchedules}
            />
          ) : (
            <div className="px-5 py-4">
              <PlanPending loading={loading} error={error} intent={intent} />
            </div>
          )}
        </ScrollArea>

        <DialogFooter className="shrink-0 flex-col gap-3 border-t bg-muted/10 px-5 py-3 sm:flex-col sm:justify-start">
          {error && plan && !deployment ? (
            <Alert variant="destructive">
              <AlertTriangle />
              <AlertDescription>
                {error}{" "}
                {activeRunId ? (
                  <Link to="/runs/$runId" params={{ runId: activeRunId }}>
                    Open the active run
                  </Link>
                ) : null}
              </AlertDescription>
            </Alert>
          ) : null}
          {destructiveConfirmationRequired && !deployment ? (
            <div className="space-y-1.5 text-left">
              <Label htmlFor="pipeline-plan-confirm-environment">
                This run replaces data. Type{" "}
                <span className="font-mono">{plan?.context.environment}</span> to confirm.
              </Label>
              <Input
                id="pipeline-plan-confirm-environment"
                value={confirmation}
                onChange={(event) =>
                  dispatchReview({
                    type: "confirmation_changed",
                    confirmation: event.target.value,
                  })
                }
                autoComplete="off"
              />
            </div>
          ) : null}
          <div className="flex flex-wrap items-center justify-end gap-2">
            {deployment ? (
              <>
                <Button variant="outline" asChild>
                  <Link to="/schedules" onClick={() => onOpenChange(false)}>
                    View schedules
                  </Link>
                </Button>
                <Button onClick={() => onOpenChange(false)}>Done</Button>
              </>
            ) : (
              <Button onClick={() => void confirm()} disabled={!canConfirm || confirming}>
                {confirming ? (
                  <Loader2 data-icon="inline-start" className="animate-spin" />
                ) : intent === "deploy" ? (
                  <Package data-icon="inline-start" />
                ) : (
                  <Play data-icon="inline-start" />
                )}
                {confirming ? (intent === "deploy" ? "Deploying…" : "Starting…") : actionLabel}
              </Button>
            )}
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function RunScope({
  assetMode,
  neededOnly,
  disabled,
  onChange,
}: {
  assetMode: boolean;
  neededOnly: boolean;
  disabled: boolean;
  onChange: (needed: boolean) => void;
}) {
  if (assetMode) return null;
  return (
    <ToggleGroup
      type="single"
      variant="outline"
      size="sm"
      spacing={0}
      className="grid w-full grid-cols-2 sm:w-auto sm:self-start"
      value={neededOnly ? "needed" : "all"}
      disabled={disabled}
      aria-label="Assets to run"
      onValueChange={(value) => {
        if (!value || (value === "needed") === neededOnly) return;
        onChange(value === "needed");
      }}
    >
      <ToggleGroupItem value="needed" title="Only assets that are out of date or never built">
        Out of date
      </ToggleGroupItem>
      <ToggleGroupItem value="all" title="Every asset in the pipeline">
        All assets
      </ToggleGroupItem>
    </ToggleGroup>
  );
}

function RunPlanReview({
  plan,
  loading,
  neededOnly,
  onRunAll,
}: {
  plan: PipelinePlan;
  loading: boolean;
  neededOnly: boolean;
  onRunAll: () => void;
}) {
  const issues = [...plan.readiness.blockers, ...plan.readiness.warnings];
  const nothingToRun =
    plan.execution_units.length === 0 && plan.readiness.blockers.length === 0 && !loading;
  return (
    <div className={cn("flex min-w-0 flex-col gap-4", loading && "opacity-60")}>
      <PlanProblems issues={issues} environment={plan.context.environment} />
      {plan.prerequisites.some((item) => item.status !== "ready") ? (
        <PlanPrerequisites plan={plan} />
      ) : null}
      {plan.readiness.active_run_id ? (
        <Alert variant="destructive">
          <ShieldAlert />
          <AlertDescription>
            Another run is already writing to these tables.{" "}
            <Link to="/runs/$runId" params={{ runId: plan.readiness.active_run_id }}>
              Open the active run
            </Link>
          </AlertDescription>
        </Alert>
      ) : null}
      {nothingToRun ? (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-lg border px-3 py-3 text-sm">
          <span className="flex items-center gap-2">
            <CheckCircle2 className="size-4 text-emerald-600 dark:text-emerald-400" />
            {neededOnly ? "Everything is up to date." : "No assets are selected."}
          </span>
          {neededOnly ? (
            <Button variant="outline" size="sm" onClick={onRunAll}>
              Run all assets
            </Button>
          ) : null}
        </div>
      ) : (
        <PlanRunAssets plan={plan} />
      )}
    </div>
  );
}

function PlanRunAssets({ plan }: { plan: PipelinePlan }) {
  const [expanded, setExpanded] = useState(false);
  const rows = useMemo(() => {
    const assetsByID = new Map(plan.assets.map((asset) => [asset.id, asset]));
    const assetsByName = new Map(plan.assets.map((asset) => [asset.name, asset]));
    const ordered = new Map<string, { name: string; staleness?: string; windows: number }>();
    for (const unit of plan.execution_units) {
      const asset = assetsByID.get(unit.asset_id) ?? assetsByName.get(unit.asset_name);
      const key = asset?.id ?? unit.asset_name;
      const row = ordered.get(key);
      if (row) row.windows += 1;
      else ordered.set(key, { name: unit.asset_name, staleness: asset?.staleness, windows: 1 });
    }
    return [...ordered.values()];
  }, [plan]);
  if (rows.length === 0) return null;
  const visible = expanded ? rows : rows.slice(0, collapsedAssetCount);
  const destructive = plan.summary.destructive_operations;
  return (
    <section aria-labelledby="pipeline-plan-assets" className="min-w-0 space-y-2">
      <div className="flex items-baseline justify-between gap-3">
        <h3 id="pipeline-plan-assets" className="text-sm font-medium">
          {rows.length} {rows.length === 1 ? "asset" : "assets"} will run
        </h3>
        {destructive > 0 ? (
          <span className="text-xs text-amber-700 dark:text-amber-300">Replaces existing data</span>
        ) : null}
      </div>
      <ol className="divide-y rounded-lg border">
        {visible.map((row) => (
          <li key={row.name} className="flex min-w-0 items-center gap-2 px-3 py-2">
            <span className="min-w-0 flex-1 truncate font-mono text-xs" title={row.name}>
              {row.name}
            </span>
            {row.windows > 1 ? (
              <span className="shrink-0 text-[11px] text-muted-foreground">
                {row.windows} windows
              </span>
            ) : null}
            {row.staleness ? (
              <StalenessBadge
                staleness={{ status: row.staleness } as AssetStaleness}
                className="shrink-0"
              />
            ) : null}
          </li>
        ))}
      </ol>
      {rows.length > collapsedAssetCount ? (
        <Button
          variant="link"
          size="sm"
          className="h-auto px-0 text-xs"
          onClick={() => setExpanded((value) => !value)}
        >
          {expanded ? "Show fewer" : `Show all ${rows.length}`}
        </Button>
      ) : null}
    </section>
  );
}

function RunOptions({
  open,
  onOpenChange,
  assetMode,
  selectorDraft,
  appliedSelector,
  selectorDraftApplied,
  loading,
  sensorMode,
  fullRefresh,
  onSelectorDraftChange,
  onApplySelector,
  onSensorModeChange,
  onFullRefreshChange,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  assetMode: boolean;
  selectorDraft: string;
  appliedSelector: string;
  selectorDraftApplied: boolean;
  loading: boolean;
  sensorMode: SensorMode;
  fullRefresh: boolean;
  onSelectorDraftChange: (selector: string) => void;
  onApplySelector: () => void;
  onSensorModeChange: (mode: SensorMode) => void;
  onFullRefreshChange: (checked: boolean) => void;
}) {
  const summary = [
    appliedSelector ? `matching ${appliedSelector}` : "",
    sensorMode !== "once" ? sensorModeLabel(sensorMode) : "",
    fullRefresh ? "full refresh" : "",
  ].filter(Boolean);
  return (
    <Collapsible open={open} onOpenChange={onOpenChange}>
      <CollapsibleTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="h-auto max-w-full justify-start px-0 text-xs text-muted-foreground hover:bg-transparent"
        >
          <ChevronRight className={cn("size-3.5 transition-transform", open && "rotate-90")} />
          <span className="font-medium">More options</span>
          {summary.length > 0 ? <span className="truncate">· {summary.join(" · ")}</span> : null}
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent className="mt-2">
        <FieldGroup className="gap-3 rounded-lg border p-3">
          {!assetMode ? (
            <Field className="gap-1" data-invalid={!selectorDraftApplied || undefined}>
              <FieldLabel htmlFor="pipeline-plan-selector" className="text-xs">
                Only assets matching
              </FieldLabel>
              <div className="flex min-w-0 items-center gap-2">
                <Input
                  id="pipeline-plan-selector"
                  value={selectorDraft}
                  onChange={(event) => onSelectorDraftChange(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") {
                      event.preventDefault();
                      onApplySelector();
                    }
                  }}
                  placeholder="tag:daily path:assets/marts +analytics.orders"
                  className="min-w-0 flex-1 font-mono"
                />
                <Button
                  variant="outline"
                  size="sm"
                  onClick={onApplySelector}
                  disabled={selectorDraftApplied || loading}
                >
                  Apply
                </Button>
              </div>
              <FieldDescription className="text-xs">
                {selectorDraftApplied
                  ? "Spaces combine, commas intersect, + adds dependencies."
                  : "Apply the filter to update the list."}
              </FieldDescription>
            </Field>
          ) : null}
          <div className="flex flex-wrap items-end gap-3">
            <Field className="w-44 gap-1">
              <FieldLabel htmlFor="pipeline-plan-sensor" className="text-xs">
                Sensors
              </FieldLabel>
              <Select
                value={sensorMode}
                onValueChange={(value) => onSensorModeChange(value as SensorMode)}
              >
                <SelectTrigger id="pipeline-plan-sensor" size="sm" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="once">Check once</SelectItem>
                    <SelectItem value="wait">Wait</SelectItem>
                    <SelectItem value="skip">Skip</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </Field>
            <Field orientation="horizontal" className="h-8 w-auto rounded-md border px-3 text-xs">
              <Switch
                id="pipeline-plan-full-refresh"
                size="sm"
                checked={fullRefresh}
                onCheckedChange={onFullRefreshChange}
              />
              <FieldLabel htmlFor="pipeline-plan-full-refresh" className="font-normal">
                Full refresh
              </FieldLabel>
            </Field>
          </div>
        </FieldGroup>
      </CollapsibleContent>
    </Collapsible>
  );
}

function DeployPlanReview({
  pipelineId,
  plan,
  status,
  schedulesOffered,
  schedules,
  ownership,
  selected,
  onSelectedChange,
}: {
  pipelineId: string;
  plan: PipelinePlan;
  status: DeployStatus | null;
  schedulesOffered: boolean;
  schedules: EnvSchedule[];
  ownership: SchedulerOwnership | null;
  selected: Set<string>;
  onSelectedChange: (selected: Set<string>) => void;
}) {
  const deploymentReview = useMemo(() => buildDeploymentReview(plan, status), [plan, status]);
  return (
    <div className="flex min-w-0 w-full flex-col" data-testid="deployment-review">
      {deploymentReview.blockers.length || deploymentReview.warnings.length ? (
        <div className="px-5 pt-4">
          <PlanProblems
            issues={[...deploymentReview.blockers, ...deploymentReview.warnings]}
            environment={plan.context.environment}
          />
        </div>
      ) : null}
      {plan.prerequisites.some((item) => item.status !== "ready") ? (
        <div className="px-5 pt-4">
          <PlanPrerequisites plan={plan} />
        </div>
      ) : null}
      {status?.in_sync && deploymentReview.rows.length === 0 ? null : (
        <DeploymentFileChanges pipelineId={pipelineId} plan={plan} status={status} />
      )}
      {schedulesOffered ? (
        <DeploySchedules
          schedules={schedules}
          status={status}
          ownership={ownership}
          selected={selected}
          onSelectedChange={onSelectedChange}
        />
      ) : null}
    </div>
  );
}

function DeploySchedules({
  schedules,
  status,
  ownership,
  selected,
  onSelectedChange,
}: {
  schedules: EnvSchedule[];
  status: DeployStatus | null;
  ownership: SchedulerOwnership | null;
  selected: Set<string>;
  onSelectedChange: (selected: Set<string>) => void;
}) {
  const canChange = ownership?.state === "owner";
  return (
    <section aria-labelledby="pipeline-deploy-schedules" className="px-5 py-4 not-first:border-t">
      <h3 id="pipeline-deploy-schedules" className="text-xs font-medium">
        Schedules
      </h3>
      {schedules.length === 0 ? (
        <p className="mt-1 text-xs text-muted-foreground">
          No schedule runs this pipeline yet. Create one in{" "}
          <Link to="/schedules" className="underline underline-offset-2">
            Schedules
          </Link>{" "}
          after deploying.
        </p>
      ) : (
        <>
          <p className="mt-1 text-xs text-muted-foreground">
            {canChange
              ? "Checked schedules use this deployment from their next run."
              : (ownership?.message ?? "Schedules can't be changed from this window.")}
          </p>
          <FieldGroup data-slot="checkbox-group" className="mt-3 gap-2">
            {schedules.map((schedule) => {
              const checkboxID = `deploy-schedule-${schedule.environment}`;
              const alreadyCurrent = Boolean(
                status?.in_sync && schedule.snapshot_version_id === status.version_id,
              );
              return (
                <Field
                  key={schedule.environment}
                  orientation="horizontal"
                  data-disabled={!canChange || alreadyCurrent || undefined}
                  className="rounded-md border px-3 py-2"
                >
                  <Checkbox
                    id={checkboxID}
                    checked={canChange && (alreadyCurrent || selected.has(schedule.environment))}
                    disabled={!canChange || alreadyCurrent}
                    onCheckedChange={(checked) => {
                      const next = new Set(selected);
                      if (checked === true) next.add(schedule.environment);
                      else next.delete(schedule.environment);
                      onSelectedChange(next);
                    }}
                  />
                  <FieldContent>
                    <FieldLabel htmlFor={checkboxID}>{schedule.environment}</FieldLabel>
                    <FieldDescription className="text-xs">
                      {schedule.cron} · {schedule.timezone} ·{" "}
                      {scheduleDeploymentLabel(schedule, status)}
                    </FieldDescription>
                  </FieldContent>
                </Field>
              );
            })}
          </FieldGroup>
        </>
      )}
    </section>
  );
}

function DeploymentResult({
  deployment,
  schedulesOffered,
}: {
  deployment: DeployResponse;
  schedulesOffered: boolean;
}) {
  const label = deploymentLabel(
    deployment.snapshot.ordinal,
    deployment.snapshot.version_id,
    "deployment",
  );
  const moved = deployment.schedules ?? [];
  return (
    <div className="flex flex-col gap-3 px-5 py-4" data-testid="deployment-result">
      <Alert>
        <CheckCircle2 />
        <AlertTitle>
          {deployment.created
            ? `Deployed as ${label}`
            : `${label.charAt(0).toUpperCase()}${label.slice(1)} is already current`}
        </AlertTitle>
        <AlertDescription>
          {moved.length > 0
            ? `${listEnvironments(moved.map((schedule) => schedule.environment))} ${moved.length === 1 ? "uses" : "use"} it from the next run.`
            : schedulesOffered && !deployment.schedule_error
              ? "Schedules keep their current deployment."
              : "Nothing runs until a schedule or run uses it."}
        </AlertDescription>
      </Alert>
      {deployment.schedule_error ? (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertTitle>Schedules kept their deployment</AlertTitle>
          <AlertDescription>
            {deployment.schedule_error}. Update them from Schedules.
          </AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}

function PlanProblems({
  issues,
  environment,
}: {
  issues: PipelinePlanIssue[];
  environment?: string;
}) {
  const groups = useMemo(() => groupPlanIssues(issues), [issues]);
  if (groups.length === 0) return null;
  const count = groups.reduce((total, group) => total + group.issues.length, 0);
  return (
    <section aria-labelledby="pipeline-plan-problems" className="min-w-0 space-y-2">
      <h3 id="pipeline-plan-problems" className="text-sm font-medium">
        {count === 1 ? "1 problem" : `${count} problems`}
      </h3>
      <ul className="divide-y rounded-lg border">
        {groups.map((group) => (
          <li key={group.asset || "pipeline"} className="min-w-0 px-3 py-2.5">
            {group.asset ? (
              <div className="truncate font-mono text-xs font-medium" title={group.asset}>
                {group.asset}
              </div>
            ) : null}
            <ul className={cn("space-y-1 text-xs", group.asset && "mt-1")}>
              {group.issues.map((issue, index) => (
                <li
                  key={`${issue.code}:${index}`}
                  className={cn(
                    "break-words",
                    issue.severity === "error"
                      ? "text-destructive"
                      : "text-amber-700 dark:text-amber-300",
                  )}
                >
                  {issue.message}
                  {issue.target ? (
                    <ResourceLink target={issue.target} environment={environment} />
                  ) : null}
                </li>
              ))}
            </ul>
          </li>
        ))}
      </ul>
    </section>
  );
}

function PlanPending({
  loading,
  error,
  intent,
}: {
  loading: boolean;
  error: string | null;
  intent: PlanIntent;
}) {
  if (loading) {
    return (
      <div className="space-y-2" aria-label="Preparing">
        <Skeleton className="h-4 w-40" />
        <Skeleton className="h-24" />
      </div>
    );
  }
  if (!error) return null;
  return (
    <Alert variant="destructive">
      <AlertTriangle />
      <AlertTitle>
        {intent === "deploy" ? "Couldn't prepare the deployment" : "Couldn't prepare the run"}
      </AlertTitle>
      <AlertDescription>{error}</AlertDescription>
    </Alert>
  );
}

function PlanPrerequisites({ plan }: { plan: PipelinePlan }) {
  if (plan.prerequisites.length === 0) return null;
  const ready = plan.prerequisites.filter((item) => item.status === "ready").length;

  return (
    <section aria-labelledby="pipeline-plan-prerequisites" className="space-y-2">
      <div className="flex min-w-0 items-center justify-between gap-3">
        <div className="min-w-0">
          <h3 id="pipeline-plan-prerequisites" className="text-sm font-medium">
            External prerequisites
          </h3>
          <p className="text-xs text-muted-foreground">
            Renart-observed producer outputs required before this pipeline can read them.
          </p>
        </div>
        <Badge variant={ready === plan.prerequisites.length ? "outline" : "destructive"} size="xs">
          {ready}/{plan.prerequisites.length} ready
        </Badge>
      </div>
      <div className="divide-y rounded-lg border">
        {plan.prerequisites.map((item) => {
          const isReady = item.status === "ready";
          const requiredSeconds = item.required_seconds ?? 0;
          const coveredSeconds = item.covered_seconds ?? 0;
          const coverage =
            requiredSeconds > 0
              ? Math.min(100, Math.round((coveredSeconds / requiredSeconds) * 100))
              : null;
          return (
            <div
              key={`${item.consumer_asset_id}:${item.uri}:${item.producer_asset_id}`}
              className="flex min-w-0 items-start gap-2.5 px-3 py-2.5"
            >
              {isReady ? (
                <CheckCircle2 className="mt-0.5 size-4 shrink-0 text-emerald-600 dark:text-emerald-400" />
              ) : (
                <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" />
              )}
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-sm">
                  {item.producer_pipeline_id ? (
                    <Link
                      to="/pipelines/$pipelineId/canvas"
                      params={{ pipelineId: item.producer_pipeline_id }}
                      className="truncate font-medium hover:underline"
                    >
                      {item.producer_pipeline_name || item.producer_asset_name}
                    </Link>
                  ) : (
                    <span className="truncate font-medium">
                      {item.producer_asset_name || "Unresolved producer"}
                    </span>
                  )}
                  {item.producer_asset_name ? (
                    <span className="truncate text-xs text-muted-foreground">
                      {item.producer_asset_name}
                    </span>
                  ) : null}
                  {item.producer_deployment_ordinal ? (
                    <Badge variant="muted" size="xs">
                      Deployment #{item.producer_deployment_ordinal}
                    </Badge>
                  ) : null}
                </div>
                <p className="truncate text-xs text-muted-foreground" title={item.uri}>
                  {item.uri}
                </p>
                <p className={cn("mt-1 text-xs", !isReady && "text-destructive")}>{item.reason}</p>
              </div>
              {coverage !== null ? (
                <Badge variant="muted" size="xs" className="shrink-0">
                  {coverage}% covered
                </Badge>
              ) : null}
            </div>
          );
        })}
      </div>
    </section>
  );
}

export function DeploymentFileChanges({
  pipelineId,
  plan,
  status,
}: {
  pipelineId: string;
  plan: PipelinePlan;
  status: DeployStatus | null;
}) {
  const review = useMemo(() => buildDeploymentReview(plan, status), [plan, status]);
  const [selectedKey, setSelectedKey] = useState("");
  const selectedRow = review.rows.find((row) => row.key === selectedKey);
  const selectedPath = selectedRow?.path;
  const identity = `${plan.source.merkle_root}:${status?.version_id ?? "first"}`;
  const [comparison, setComparison] = useState<{
    key: string;
    diff?: DeploymentFileDiff;
    error?: string;
  } | null>(null);
  const comparisonKey = `${identity}:${selectedPath ?? ""}`;
  const currentComparison = comparison?.key === comparisonKey ? comparison : null;

  useEffect(() => {
    if (!selectedPath || !status) return;
    let cancelled = false;
    setComparison(null);
    getDeploymentFileDiff(pipelineId, selectedPath, status.version_id)
      .then((diff) => {
        if (!cancelled) setComparison({ key: comparisonKey, diff });
      })
      .catch((cause: unknown) => {
        if (!cancelled)
          setComparison({
            key: comparisonKey,
            error: cause instanceof Error ? cause.message : "Could not load the file comparison.",
          });
      });
    return () => {
      cancelled = true;
    };
  }, [pipelineId, selectedPath, comparisonKey, status?.version_id]);

  const attention = review.rows.filter((row) => deploymentRowTone(row) !== "neutral").length;
  const impact = plan.semantic_impact;
  // Missing analysis must not read as "no impact", but a first deployment has
  // nothing to compare and needs no note.
  const impactNote =
    review.rows.length === 0 ||
    impact?.status === "no_baseline" ||
    (impact?.status === "available" && impact.complete)
      ? null
      : impact?.status === "available"
        ? "Some effects of these changes may not be shown."
        : "Impact analysis is unavailable for these changes.";
  return (
    <section aria-labelledby="pipeline-deploy-source-changes">
      <div className="flex items-center justify-between gap-3 px-5 py-3">
        <h3 id="pipeline-deploy-source-changes" className="text-xs font-medium">
          Changes & impact <span className="ml-1 text-muted-foreground">{review.rows.length}</span>
        </h3>
        <span
          role="status"
          className={cn(
            "text-xs",
            attention ? "text-amber-700 dark:text-amber-300" : "text-muted-foreground",
          )}
        >
          {attention
            ? `${attention} to review`
            : plan.status === "blocked"
              ? "Blocked"
              : "Source review"}
        </span>
      </div>
      {!status ? (
        <Skeleton className="mx-5 h-24" />
      ) : review.rows.length === 0 ? (
        <p className="px-5 pb-4 text-xs text-muted-foreground">
          No source or reported contract changes.
        </p>
      ) : (
        <div className="divide-y border-y">
          {review.rows.map((row) => {
            const open = selectedKey === row.key;
            const tone = deploymentRowTone(row);
            return (
              <Collapsible
                key={row.key}
                open={open}
                onOpenChange={(next) => setSelectedKey(next ? row.key : "")}
              >
                <CollapsibleTrigger asChild>
                  <button
                    type="button"
                    className={cn(
                      "flex w-full min-w-0 items-center gap-2 px-5 py-3 text-left text-xs hover:bg-muted/35 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
                      open && "bg-muted/25",
                    )}
                  >
                    <ChevronRight
                      className={cn(
                        "size-3 shrink-0 text-muted-foreground transition-transform",
                        open && "rotate-90",
                      )}
                    />
                    <FileCode2 className="size-3.5 shrink-0 text-muted-foreground" />
                    <span
                      className="min-w-0 flex-1 truncate font-mono"
                      title={row.path ?? row.name}
                    >
                      {row.path ?? row.name}
                    </span>
                    <span
                      className={cn(
                        "max-w-[45%] truncate text-[11px]",
                        tone === "error"
                          ? "text-destructive"
                          : tone === "warning"
                            ? "text-amber-700 dark:text-amber-300"
                            : "text-muted-foreground",
                      )}
                      title={deploymentRowSummary(row)}
                    >
                      {tone !== "neutral" ? (
                        <span
                          aria-hidden="true"
                          className={cn(
                            "mr-1.5 inline-block size-1.5 rounded-full",
                            tone === "error" ? "bg-destructive" : "bg-warning",
                          )}
                        />
                      ) : null}
                      {deploymentRowSummary(row)}
                    </span>
                  </button>
                </CollapsibleTrigger>
                <CollapsibleContent className="min-w-0 border-t bg-background">
                  {row.findings.length ? (
                    <ul
                      className="space-y-1 border-b px-5 py-3 text-xs"
                      aria-label="Asset findings"
                    >
                      {row.findings.map((finding, index) => (
                        <li
                          key={index}
                          className={
                            finding.severity === "error"
                              ? "text-destructive"
                              : "text-amber-700 dark:text-amber-300"
                          }
                        >
                          {finding.message}
                          {finding.target ? (
                            <ResourceLink
                              target={finding.target}
                              environment={plan.context.environment}
                            />
                          ) : null}
                        </li>
                      ))}
                    </ul>
                  ) : null}
                  {row.path && status ? (
                    <DeploymentFileDiffPreview
                      pipelineId={pipelineId}
                      sourceVersion={identity}
                      path={row.path}
                      row={row}
                      diff={open ? (currentComparison?.diff ?? null) : null}
                      loading={open && !currentComparison}
                      error={open ? (currentComparison?.error ?? null) : null}
                    />
                  ) : (
                    <p className="px-5 py-3 text-xs text-muted-foreground">
                      No source path is available for this asset. Its reported impact is shown
                      below.
                    </p>
                  )}
                  {row.semantic ? (
                    <details className="group border-t px-5 py-3">
                      <summary className="flex cursor-pointer list-none items-center gap-1.5 text-xs focus-visible:ring-2 focus-visible:ring-ring [&::-webkit-details-marker]:hidden">
                        <ChevronRight className="size-3 transition-transform group-open:rotate-90" />
                        Why this matters
                        <span className="ml-auto text-[11px] text-muted-foreground">
                          {row.semantic.columns.length
                            ? `${row.semantic.columns.length} output ${row.semantic.columns.length === 1 ? "change" : "changes"}`
                            : deploymentRowSummary(row)}
                        </span>
                      </summary>
                      <div className="mt-2">
                        <SemanticAssetImpactRow asset={row.semantic} />
                      </div>
                    </details>
                  ) : null}
                </CollapsibleContent>
              </Collapsible>
            );
          })}
        </div>
      )}
      {impactNote ? (
        <p className="px-5 py-3 text-xs text-muted-foreground" role="status">
          {impactNote}
        </p>
      ) : null}
    </section>
  );
}

function DeploymentFileDiffPreview({
  pipelineId,
  sourceVersion,
  path,
  diff,
  loading,
  error,
  row,
}: {
  pipelineId: string;
  sourceVersion?: string;
  path: string;
  diff: DeploymentFileDiff | null;
  loading: boolean;
  error: string | null;
  row: DeploymentReviewRow;
}) {
  const mobile = useIsMobile();
  const annotations = useMemo(
    () => deploymentDiffAnnotations(row, diff?.before ?? "", diff?.after ?? ""),
    [row, diff],
  );
  if (loading) {
    return <Skeleton className="h-56 min-w-0" />;
  }
  if (error) {
    return (
      <Alert variant="destructive" className="min-w-0">
        <AlertTriangle />
        <AlertTitle>Could not load this comparison</AlertTitle>
        <AlertDescription>{error}</AlertDescription>
      </Alert>
    );
  }
  if (!diff) return null;
  if (diff.binary || diff.too_large) {
    return (
      <Alert className="min-w-0">
        <FileCode2 />
        <AlertTitle>{diff.binary ? "Binary file" : "File is too large to preview"}</AlertTitle>
        <AlertDescription>
          {path} is included in the deployment comparison, but its contents are not sent to the
          browser.
        </AlertDescription>
      </Alert>
    );
  }

  const language = deploymentFileLanguage(path);
  const modelPrefix = `deployment-diff:${pipelineId}:${sourceVersion ?? "first"}:${path}`;
  return (
    <div
      className="min-w-0 overflow-hidden rounded-md border"
      data-testid="deployment-file-diff"
      data-diff-layout={mobile ? "inline" : "split"}
    >
      <div className="grid grid-cols-2 border-b bg-muted/30 text-xs font-medium max-md:hidden">
        <div className="min-w-0 truncate border-r px-3 py-2">
          Current deployment{diff.before_exists ? "" : " · not present"}
        </div>
        <div className="min-w-0 truncate px-3 py-2">
          Saved workspace{diff.after_exists ? "" : " · not present"}
        </div>
      </div>
      <div className="border-b bg-muted/30 px-3 py-2 text-xs font-medium md:hidden">
        Current deployment → Saved workspace
      </div>
      <div className="h-56 min-w-0">
        <ReadOnlyRenderedOperationDiff
          original={diff.before_exists ? (diff.before ?? "") : ""}
          modified={diff.after_exists ? (diff.after ?? "") : ""}
          language={language}
          modelKey={modelPrefix}
          useInlineViewWhenSpaceIsLimited
          inline={mobile}
          annotations={annotations}
        />
      </div>
    </div>
  );
}

function deploymentFileLanguage(path: string) {
  const extension = path.split(".").pop()?.toLowerCase();
  switch (extension) {
    case "sql":
      return "sql";
    case "py":
      return "python";
    case "json":
      return "json";
    case "yaml":
    case "yml":
      return "yaml";
    case "md":
      return "markdown";
    default:
      return "text";
  }
}

function groupPlanIssues(issues: PipelinePlanIssue[]) {
  const groups = new Map<string, PipelinePlanIssue[]>();
  for (const issue of issues) {
    if (issuesShownElsewhere.has(issue.code)) continue;
    const key = issue.asset_name ?? "";
    groups.set(key, [...(groups.get(key) ?? []), issue]);
  }
  const hasError = (group: PipelinePlanIssue[]) =>
    group.some((issue) => issue.severity === "error");
  return [...groups.entries()]
    .map(([asset, grouped]) => ({
      asset,
      issues: [...grouped].sort(
        (left, right) => Number(right.severity === "error") - Number(left.severity === "error"),
      ),
    }))
    .sort((left, right) => Number(hasError(right.issues)) - Number(hasError(left.issues)));
}

// Schedules on the latest deployment follow the pipeline, and schedules that
// were never deployed are waiting for one; both move by default. A schedule
// kept on an older deployment stays there unless the user checks it, except
// when nothing changed since the latest deployment: then updating schedules is
// the only thing this dialog can do.
function defaultScheduleSelection(schedules: EnvSchedule[], status: DeployStatus | null) {
  const onlySchedulesCanChange = Boolean(status?.has_snapshot && status.in_sync);
  return new Set(
    schedules
      .filter(
        (schedule) =>
          onlySchedulesCanChange ||
          !schedule.snapshot_version_id ||
          (status?.has_snapshot && schedule.snapshot_version_id === status.version_id),
      )
      .map((schedule) => schedule.environment),
  );
}

function scheduleDeploymentLabel(schedule: EnvSchedule, status: DeployStatus | null) {
  if (!schedule.snapshot_version_id) return "not deployed yet";
  const label = schedule.snapshot_ordinal
    ? `deployment #${schedule.snapshot_ordinal}`
    : `deployment ${schedule.snapshot_version_id.slice(0, 8)}`;
  return schedule.snapshot_version_id === status?.version_id
    ? `uses ${label}, the latest`
    : `uses ${label}`;
}

function deployContextLabel(status: DeployStatus | null) {
  if (!status) return "Checking the latest deployment…";
  if (!status.has_snapshot) return "First deployment";
  const latest = deploymentLabel(status.ordinal, undefined, "deployment");
  const changes =
    (status.changed_files?.length ?? 0) +
    (status.added_files?.length ?? 0) +
    (status.removed_files?.length ?? 0);
  if (changes > 0) return `${changes} ${changes === 1 ? "file" : "files"} changed since ${latest}`;
  return status.in_sync ? `No changes since ${latest}` : `Dependencies changed since ${latest}`;
}

function runContextLabel(
  plan: PipelinePlan | null,
  environment: string,
  source: PipelineRunSource | null | undefined,
  timeWindow: { start: string; end: string } | null | undefined,
) {
  const sourceLabel = plan
    ? plan.source.kind === "working_tree"
      ? "working tree"
      : deploymentLabel(
          plan.source.deployment_ordinal,
          plan.source.version_id || plan.source.merkle_root,
          "deployment",
        )
    : source?.source === "working_tree"
      ? "working tree"
      : source?.source === "snapshot"
        ? deploymentLabel(undefined, source.snapshot_version_id, "deployment")
        : "";
  const window = plan
    ? formatPlanWindow(plan.context.start_date, plan.context.end_date)
    : timeWindow
      ? formatPlanWindow(timeWindow.start, timeWindow.end)
      : "";
  return [plan?.context.environment || environment || "default", sourceLabel, window]
    .filter(Boolean)
    .join(" · ");
}

function listEnvironments(names: string[]) {
  if (names.length <= 1) return names.join("");
  return `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;
}

function formatPlanWindow(start: string, end: string) {
  const format = (value: string) => {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toISOString().slice(0, 16).replace("T", " ");
  };
  return `${format(start)}–${format(end)} UTC`;
}
