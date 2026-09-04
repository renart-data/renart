"use client";

import { Link } from "@tanstack/react-router";
import {
  AlertTriangle,
  CheckCircle2,
  ChevronRight,
  CircleAlert,
  FileCode2,
  Loader2,
  Package,
  Play,
  RefreshCw,
  ShieldAlert,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from "react";

import {
  ReadOnlyRenderedOperation,
  ReadOnlyRenderedOperationDiff,
  assetRenderStageLabel,
} from "@/components/app/asset-render-view";
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
  FieldLegend,
  FieldSet,
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
  promoteEnvSchedules,
  type EnvSchedule,
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
import type { PipelineRun } from "@/lib/types";
import type { PipelinePlanSelectionRequest } from "@/lib/generated/api-types";
import {
  createPipelinePlanRequest,
  derivePipelinePlanReview,
  initialPipelinePlanReviewState,
  pipelinePlanReviewReducer,
  planSelectionLabel,
  sensorModeLabel,
  type PlanIntent,
  type PlanSelectionMode,
  type SensorMode,
} from "@/lib/pipeline-plan-review-model";
import { awaitWorkspaceSaves } from "@/lib/workspace-save-barrier";
import { cn } from "@/lib/utils";
import { deploymentLabel } from "@/lib/deployment-label";

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
  onAccepted?: (run: PipelineRun, plan: PipelinePlan) => void;
  onDeploy?: (expectedSourceMerkle: string) => Promise<DeployResponse>;
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
    contentLoading,
    stageContentLoaded,
    error,
    confirming,
    confirmation,
    activeRunId,
    selectorDraft,
    runOptionsOpen,
  } = review;
  const [deploySchedulesOpen, setDeploySchedulesOpen] = useState(false);
  const [deployStatus, setDeployStatus] = useState<DeployStatus | null>(null);
  const [deployment, setDeployment] = useState<DeployResponse | null>(null);
  const [schedules, setSchedules] = useState<EnvSchedule[]>([]);
  const [schedulerOwnership, setSchedulerOwnership] = useState<SchedulerOwnership | null>(null);
  const [selectedScheduleKeys, setSelectedScheduleKeys] = useState<Set<string>>(() => new Set());
  const [promoting, setPromoting] = useState(false);
  const [promotionError, setPromotionError] = useState<string | null>(null);
  const requestSerial = useRef(0);
  const initialPlanContext = useRef<string | null>(null);
  const requestedSourceKind = intent === "deploy" ? "working_tree" : source?.source;
  const requestedSourceVersion =
    intent !== "deploy" && source?.source === "snapshot" ? source.snapshot_version_id : undefined;

  const fetchPlan = useCallback(
    async (input: PipelinePlanRequest, includeStageContent = false) => {
      const serial = ++requestSerial.current;
      dispatchReview({ type: "plan_load_started", includeStageContent });
      try {
        const next = await planPipeline(pipelineId, {
          ...input,
          include_stage_content: includeStageContent,
        });
        if (serial !== requestSerial.current) return;
        dispatchReview({
          type: "plan_loaded",
          plan: next,
          request: {
            ...canonicalPipelinePlanRequest(next, false),
            purpose: input.purpose,
          },
          includeStageContent,
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
    setDeploySchedulesOpen(false);
    setDeployStatus(null);
    setDeployment(null);
    setSchedules([]);
    setSchedulerOwnership(null);
    setSelectedScheduleKeys(new Set());
    setPromotionError(null);
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
            getEnvSchedules(),
          ]);
          if (serial !== requestSerial.current) return;
          setDeployStatus(statusResponse);
          setSchedules(scheduleResponse.schedules ?? []);
          setSchedulerOwnership(scheduleResponse.scheduler);
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
    selectorPlanIsCurrent,
    sensorMode,
    fullRefresh,
    destructiveConfirmationRequired,
    canConfirm,
  } = derivePipelinePlanReview(review, {
    intent,
    confirmDestructive,
    deploymentExists: Boolean(deployment),
  });

  const applySelector = () => {
    const selector = selectorDraft.trim();
    if (!selector || !selectorMode) return;
    updateRequest((current) => ({
      ...current,
      selection: { mode: selectionMode, selector },
    }));
  };

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
  const promotionCandidates = useMemo(
    () =>
      deployment
        ? pipelineSchedules.filter(
            (schedule) => schedule.snapshot_version_id !== deployment.snapshot.version_id,
          )
        : [],
    [deployment, pipelineSchedules],
  );

  const loadStageContent = () => {
    if (!plan || stageContentLoaded || contentLoading) return;
    void fetchPlan(
      { ...canonicalPipelinePlanRequest(plan, true), purpose: request?.purpose },
      true,
    );
  };

  const confirm = async () => {
    if (!plan || !canConfirm) return;
    dispatchReview({ type: "confirm_started" });
    try {
      if (intent === "deploy") {
        if (!onDeploy) {
          throw new Error("Deployment is unavailable.");
        }
        const response = await onDeploy(plan.source.merkle_root);
        setDeployment(response);
        setDeployStatus(await getDeployStatus(pipelineId));
        const scheduleResponse = await getEnvSchedules();
        setSchedules(scheduleResponse.schedules ?? []);
        setSchedulerOwnership(scheduleResponse.scheduler);
        setSelectedScheduleKeys(new Set());
        setDeploySchedulesOpen(true);
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
          message: "The saved source changed after review. Review the refreshed deployment plan.",
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
              ? "The data state now requires additional or changed work. Review the refreshed plan before running."
              : cause instanceof APIError && cause.code === "plan_stale"
                ? "The source or configuration changed. Review the refreshed plan before running."
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
          message: "Another run was admitted first. Open it to follow its progress.",
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

  const promoteSelectedSchedules = async () => {
    if (!deployment || !plan || selectedScheduleKeys.size === 0) return;
    const selected = promotionCandidates.filter((schedule) =>
      selectedScheduleKeys.has(schedule.environment),
    );
    setPromoting(true);
    setPromotionError(null);
    try {
      await promoteEnvSchedules(
        pipelineId,
        deployment.snapshot.version_id,
        selected.map((schedule) => ({
          environment: schedule.environment,
          expected_snapshot_version_id: schedule.snapshot_version_id ?? "",
        })),
      );
      const scheduleResponse = await getEnvSchedules();
      setSchedules(scheduleResponse.schedules ?? []);
      setSchedulerOwnership(scheduleResponse.scheduler);
      setSelectedScheduleKeys(new Set());
      await onSchedulesChanged?.();
    } catch (cause) {
      setPromotionError(cause instanceof Error ? cause.message : "Schedules could not be updated.");
    } finally {
      setPromoting(false);
    }
  };

  const sourceLabel = plan
    ? planSourceLabel(plan)
    : intent === "deploy"
      ? "Saved working tree"
      : sourceInputLabel(source);
  const finalActionLabel = plan
    ? intent === "deploy"
      ? `Deploy ${plan.summary.assets} ${plan.summary.assets === 1 ? "asset" : "assets"}`
      : `Run ${plan.summary.assets} ${plan.summary.assets === 1 ? "asset" : "assets"} from ${runSourceLabel(plan)}`
    : intent === "deploy"
      ? "Deploy pipeline"
      : "Run pipeline";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className={cn(
          "flex max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-none flex-col gap-0 overflow-hidden p-0",
          intent === "deploy"
            ? "h-auto sm:max-w-[960px]"
            : "h-[min(92dvh,58rem)] sm:max-w-6xl xl:max-w-7xl",
        )}
        data-testid="pipeline-plan-sheet"
      >
        <ScrollArea
          className="min-h-0 flex-1"
          data-testid="pipeline-plan-scroll"
          showHorizontalScrollBar={intent !== "deploy"}
          viewportClassName={intent === "deploy" ? "[&>div]:!block" : undefined}
        >
          <DialogHeader className="border-b px-5 py-4 pr-12">
            <div className="flex min-w-0 items-center gap-2">
              <DialogTitle className="truncate">
                {intent === "deploy"
                  ? "Review deployment"
                  : initialSelection?.mode === "asset"
                    ? "Review asset run"
                    : "Review pipeline run"}
              </DialogTitle>
              {plan && intent !== "deploy" ? <PlanStatusBadge status={plan.status} /> : null}
              {(loading || contentLoading) && plan ? (
                <Loader2 className="size-3.5 animate-spin" />
              ) : null}
            </div>
            <DialogDescription>
              {intent === "deploy" ? (
                <span className="flex flex-wrap items-center gap-x-2 gap-y-1 text-xs">
                  <span className="font-medium text-foreground">{pipelineName}</span>
                  <span>→ {plan?.context.environment || environment || "default"}</span>
                  <span aria-hidden="true">/</span>
                  <span>
                    {!deployStatus
                      ? "Resolving source…"
                      : deployStatus.has_snapshot
                        ? deploymentLabel(deployStatus.ordinal, deployStatus.version_id)
                        : "First deployment"}{" "}
                    → saved workspace
                  </span>
                </span>
              ) : (
                `${pipelineName} · saved source preview · nothing executes until you confirm`
              )}
            </DialogDescription>
          </DialogHeader>

          {intent === "run" ? (
            <div className="border-b px-5 py-3">
              <dl
                className={cn(
                  "grid min-w-0 grid-cols-2 gap-x-6 gap-y-2 text-xs",
                  intent === "run" ? "sm:grid-cols-3" : "sm:grid-cols-4",
                )}
              >
                <PlanContextItem label="Source" value={sourceLabel} />
                <PlanContextItem
                  label="Environment"
                  value={plan?.context.environment || environment || "default"}
                />
                <PlanContextItem
                  label="Window"
                  value={
                    plan
                      ? formatPlanWindow(plan.context.start_date, plan.context.end_date)
                      : timeWindow
                        ? formatPlanWindow(timeWindow.start, timeWindow.end)
                        : "Resolving…"
                  }
                />
              </dl>
              {intent === "run" ? (
                <Collapsible
                  open={runOptionsOpen}
                  onOpenChange={(nextOpen) =>
                    dispatchReview({ type: "run_options_changed", open: nextOpen })
                  }
                  className="mt-3 rounded-lg border"
                >
                  <CollapsibleTrigger asChild>
                    <Button
                      type="button"
                      variant="ghost"
                      className="h-auto w-full justify-start rounded-lg px-3 py-2.5"
                    >
                      <ChevronRight
                        className={cn(
                          "size-4 shrink-0 transition-transform",
                          runOptionsOpen && "rotate-90",
                        )}
                      />
                      <span className="font-medium">Run options</span>
                      <span className="truncate text-xs font-normal text-muted-foreground">
                        {planSelectionLabel(selectionMode)} · {sensorModeLabel(sensorMode)}
                        {fullRefresh ? " · full refresh" : ""}
                      </span>
                    </Button>
                  </CollapsibleTrigger>
                  <CollapsibleContent className="border-t p-3">
                    <FieldSet className="gap-3">
                      <FieldLegend className="sr-only">Run options</FieldLegend>
                      <FieldGroup className="grid grid-cols-1 gap-3 sm:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_auto] sm:items-end">
                        <Field className="gap-1">
                          <FieldLabel
                            htmlFor="pipeline-plan-scope"
                            className="text-[11px] text-muted-foreground"
                          >
                            Scope
                          </FieldLabel>
                          <Select
                            value={selectionMode}
                            disabled={selectionMode === "asset"}
                            onValueChange={(value) => {
                              const mode = value as PlanSelectionMode;
                              const usesSelector =
                                mode === "selector" || mode === "selector_needed";
                              const selector = selectorDraft.trim() || appliedSelector || "*";
                              if (usesSelector) {
                                dispatchReview({ type: "selector_draft_changed", selector });
                              }
                              updateRequest((current) => ({
                                ...current,
                                selection: usesSelector ? { mode, selector } : { mode },
                              }));
                            }}
                          >
                            <SelectTrigger id="pipeline-plan-scope" size="sm" className="w-full">
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectGroup>
                                {selectionMode === "asset" ? (
                                  <SelectItem value="asset">Selected asset</SelectItem>
                                ) : (
                                  <>
                                    <SelectItem value="all">Entire pipeline</SelectItem>
                                    <SelectItem value="needed">Needed assets</SelectItem>
                                    <SelectItem value="selector">Matching selector</SelectItem>
                                    <SelectItem value="selector_needed">
                                      Needed matching selector
                                    </SelectItem>
                                  </>
                                )}
                              </SelectGroup>
                            </SelectContent>
                          </Select>
                        </Field>
                        <Field className="gap-1">
                          <FieldLabel
                            htmlFor="pipeline-plan-sensor"
                            className="text-[11px] text-muted-foreground"
                          >
                            Sensors
                          </FieldLabel>
                          <Select
                            value={sensorMode}
                            onValueChange={(value) =>
                              updateRequest((current) => ({
                                ...current,
                                sensor_mode: value as SensorMode,
                              }))
                            }
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
                        <Field
                          orientation="horizontal"
                          className="h-8 w-full self-end rounded-md border px-3 text-xs sm:w-auto"
                        >
                          <Switch
                            id="pipeline-plan-full-refresh"
                            size="sm"
                            checked={fullRefresh}
                            onCheckedChange={(checked) =>
                              updateRequest((current) => ({ ...current, full_refresh: checked }))
                            }
                          />
                          <FieldLabel htmlFor="pipeline-plan-full-refresh" className="font-normal">
                            Full refresh
                          </FieldLabel>
                        </Field>
                        {selectorMode ? (
                          <Field
                            className="border-t pt-3 sm:col-span-3"
                            data-invalid={!selectorDraft.trim()}
                          >
                            <FieldLabel htmlFor="pipeline-plan-selector">Asset selector</FieldLabel>
                            <div className="flex min-w-0 flex-wrap items-center gap-2">
                              <Input
                                id="pipeline-plan-selector"
                                value={selectorDraft}
                                onChange={(event) =>
                                  dispatchReview({
                                    type: "selector_draft_changed",
                                    selector: event.target.value,
                                  })
                                }
                                onKeyDown={(event) => {
                                  if (event.key === "Enter") {
                                    event.preventDefault();
                                    applySelector();
                                  }
                                }}
                                placeholder="tag:daily,path:assets/marts +analytics.orders"
                                aria-invalid={!selectorDraft.trim()}
                                className="min-w-56 flex-1 font-mono"
                              />
                              <Button
                                variant="outline"
                                size="sm"
                                onClick={applySelector}
                                disabled={!selectorDraft.trim() || selectorDraftApplied || loading}
                              >
                                Apply
                              </Button>
                            </div>
                            <FieldDescription>
                              {!selectorDraftApplied
                                ? "Apply the expression to validate it and preview its assets."
                                : loading
                                  ? "Resolving selector…"
                                  : selectorPlanIsCurrent && plan
                                    ? `${plan.summary.assets} ${plan.summary.assets === 1 ? "asset" : "assets"} selected. Use spaces for union, commas for intersection, and + for graph expansion.`
                                    : "Use spaces for union, commas for intersection, and + for graph expansion."}
                            </FieldDescription>
                          </Field>
                        ) : null}
                      </FieldGroup>
                    </FieldSet>
                  </CollapsibleContent>
                </Collapsible>
              ) : null}
            </div>
          ) : null}

          {plan && intent === "run" ? (
            <RunPlanReview
              plan={plan}
              contentLoading={contentLoading}
              contentLoaded={stageContentLoaded}
              onLoadStageContent={loadStageContent}
            />
          ) : plan ? (
            <DeployPlanReview
              pipelineId={pipelineId}
              plan={plan}
              status={deployStatus}
              deployment={deployment}
              contentLoading={contentLoading}
              contentLoaded={stageContentLoaded}
              onLoadStageContent={loadStageContent}
              schedules={pipelineSchedules}
              promotionCandidates={promotionCandidates}
              schedulerOwnership={schedulerOwnership}
              selectedScheduleKeys={selectedScheduleKeys}
              onSelectedScheduleKeysChange={setSelectedScheduleKeys}
              promotionError={promotionError}
              schedulesOpen={deploySchedulesOpen}
              onSchedulesOpenChange={setDeploySchedulesOpen}
            />
          ) : (
            <div className="p-5">
              {loading ? <PlanLoading /> : null}
              {!loading && error ? (
                <Alert variant="destructive">
                  <AlertTriangle />
                  <AlertTitle>Plan failed</AlertTitle>
                  <AlertDescription>{error}</AlertDescription>
                </Alert>
              ) : null}
            </div>
          )}
        </ScrollArea>

        <DialogFooter className="shrink-0 flex-col gap-3 border-t bg-muted/10 px-5 py-4 sm:flex-col sm:justify-start">
          {error && plan ? (
            <Alert variant="destructive">
              <AlertTriangle />
              <AlertTitle>Plan needs attention</AlertTitle>
              <AlertDescription>
                {error}{" "}
                {activeRunId ? (
                  <Link to="/runs/$runId" params={{ runId: activeRunId }}>
                    Open active run
                  </Link>
                ) : null}
              </AlertDescription>
            </Alert>
          ) : null}
          {destructiveConfirmationRequired ? (
            <div className="space-y-1.5 text-left">
              <Label htmlFor="pipeline-plan-confirm-environment">
                Type <span className="font-mono">{plan?.context.environment}</span> to confirm
                destructive operations
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
          {deployment ? (
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="min-w-0 flex-1 text-left text-[11px] text-muted-foreground">
                {promotionCandidates.length > 0
                  ? `${promotionCandidates.length} schedule${promotionCandidates.length === 1 ? " is" : "s are"} not using this deployment. Only selected schedules will move.`
                  : "Every schedule for this pipeline is already on this deployment."}
              </div>
              <div className="flex items-center gap-2">
                <Button variant="outline" onClick={() => onOpenChange(false)}>
                  Close
                </Button>
                {promotionCandidates.length > 0 ? (
                  <Button
                    onClick={() => void promoteSelectedSchedules()}
                    disabled={
                      selectedScheduleKeys.size === 0 ||
                      promoting ||
                      schedulerOwnership?.state !== "owner"
                    }
                  >
                    {promoting ? (
                      <Loader2 data-icon="inline-start" className="animate-spin" />
                    ) : (
                      <RefreshCw data-icon="inline-start" />
                    )}
                    {promoting
                      ? "Updating…"
                      : `Update ${selectedScheduleKeys.size} schedule${selectedScheduleKeys.size === 1 ? "" : "s"}`}
                  </Button>
                ) : null}
              </div>
            </div>
          ) : (
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="min-w-0 flex-1 text-left text-[11px] text-muted-foreground">
                {intent === "deploy"
                  ? "Saves definitions only · no data execution"
                  : selectionMode === "needed" || selectionMode === "selector_needed"
                    ? "Confirmation omits work that became fresh, but never adds new work without another review."
                    : plan?.readiness.active_run_id
                      ? "Another execution owns a write resource needed by this plan."
                      : "Confirmation rechecks the complete plan before the run is admitted."}
              </div>
              <Button onClick={() => void confirm()} disabled={!canConfirm || confirming}>
                {confirming ? (
                  <Loader2 data-icon="inline-start" className="animate-spin" />
                ) : intent === "deploy" ? (
                  <Package data-icon="inline-start" />
                ) : (
                  <Play data-icon="inline-start" />
                )}
                {confirming ? (intent === "deploy" ? "Deploying…" : "Starting…") : finalActionLabel}
              </Button>
            </div>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PlanContextItem({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
        {label}
      </dt>
      <dd className="truncate font-medium" title={value}>
        {value}
      </dd>
    </div>
  );
}

function PlanReadySummary({ plan, intent }: { plan: PipelinePlan; intent: PlanIntent }) {
  const codeChecks = plan.readiness.code_checks.summary;
  const prerequisitesReady = plan.prerequisites.every((item) => item.status === "ready");
  if (
    plan.readiness.blockers.length > 0 ||
    plan.readiness.warnings.length > 0 ||
    plan.readiness.active_run_id ||
    !prerequisitesReady ||
    codeChecks.errors > 0 ||
    codeChecks.warnings > 0
  ) {
    return null;
  }

  return (
    <Alert>
      <CheckCircle2 aria-label="All code checks passed" />
      <AlertTitle>{intent === "deploy" ? "Ready to deploy" : "Ready to run"}</AlertTitle>
      <AlertDescription>
        {plan.summary.assets} {plan.summary.assets === 1 ? "asset" : "assets"} selected · code
        checks passed
        {plan.prerequisites.length > 0 ? ` · ${plan.prerequisites.length} prerequisites ready` : ""}
      </AlertDescription>
    </Alert>
  );
}

function RunPlanReview({
  plan,
  contentLoading,
  contentLoaded,
  onLoadStageContent,
}: {
  plan: PipelinePlan;
  contentLoading: boolean;
  contentLoaded: boolean;
  onLoadStageContent: () => void;
}) {
  const [detailsOpen, setDetailsOpen] = useState(false);

  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-5 p-5">
      <PlanIssues title="Blockers" issues={plan.readiness.blockers} destructive />
      <PlanIssues title="Warnings" issues={plan.readiness.warnings} />
      <PlanPrerequisites plan={plan} />
      {plan.readiness.active_run_id ? (
        <Alert variant="destructive">
          <ShieldAlert />
          <AlertTitle>Conflicting run</AlertTitle>
          <AlertDescription>
            Another queued or running execution owns a selected write resource.{" "}
            <Link to="/runs/$runId" params={{ runId: plan.readiness.active_run_id }}>
              Open active run
            </Link>
          </AlertDescription>
        </Alert>
      ) : null}

      <PlanReadySummary plan={plan} intent="run" />
      <PlanCodeReview plan={plan} />

      <PlanExecutionReview
        plan={plan}
        contentLoading={contentLoading}
        contentLoaded={contentLoaded}
        onLoadStageContent={onLoadStageContent}
      />

      <Collapsible open={detailsOpen} onOpenChange={setDetailsOpen} className="rounded-lg border">
        <CollapsibleTrigger asChild>
          <Button variant="ghost" className="h-auto w-full justify-start rounded-lg px-3 py-2.5">
            <ChevronRight
              className={cn("size-4 shrink-0 transition-transform", detailsOpen && "rotate-90")}
            />
            <span className="font-medium">Plan details</span>
            <span className="truncate text-xs font-normal text-muted-foreground">
              source, identities, and write isolation
            </span>
          </Button>
        </CollapsibleTrigger>
        <CollapsibleContent className="border-t p-3">
          <RunPlanDetails plan={plan} />
        </CollapsibleContent>
      </Collapsible>
    </div>
  );
}

function DeployPlanReview({
  pipelineId,
  plan,
  status,
  deployment,
  contentLoading,
  contentLoaded,
  onLoadStageContent,
  schedules,
  promotionCandidates,
  schedulerOwnership,
  selectedScheduleKeys,
  onSelectedScheduleKeysChange,
  promotionError,
  schedulesOpen,
  onSchedulesOpenChange,
}: {
  pipelineId: string;
  plan: PipelinePlan;
  status: DeployStatus | null;
  deployment: DeployResponse | null;
  contentLoading: boolean;
  contentLoaded: boolean;
  onLoadStageContent: () => void;
  schedules: EnvSchedule[];
  promotionCandidates: EnvSchedule[];
  schedulerOwnership: SchedulerOwnership | null;
  selectedScheduleKeys: Set<string>;
  onSelectedScheduleKeysChange: (selected: Set<string>) => void;
  promotionError: string | null;
  schedulesOpen: boolean;
  onSchedulesOpenChange: (open: boolean) => void;
}) {
  const [detailsOpen, setDetailsOpen] = useState(false);
  const deploymentReview = useMemo(() => buildDeploymentReview(plan, status), [plan, status]);
  const schedulesRef = useRef<HTMLDivElement | null>(null);
  const runtimeChecks = plan.assets.flatMap((asset) =>
    asset.renders.flatMap((render) => render.stages.filter((stage) => stage.kind === "check")),
  );

  useEffect(() => {
    if (!deployment || !schedulesOpen) return;
    const frame = window.requestAnimationFrame(() =>
      schedulesRef.current?.scrollIntoView({ behavior: "smooth", block: "nearest" }),
    );
    return () => window.cancelAnimationFrame(frame);
  }, [deployment, schedulesOpen]);

  return (
    <div className="flex min-w-0 w-full flex-col" data-testid="deployment-review">
      {deploymentReview.blockers.length || deploymentReview.warnings.length ? (
        <div className="space-y-3 px-5 pt-4">
          <PlanIssues title="Blockers" issues={deploymentReview.blockers} destructive />
          <PlanIssues title="Warnings" issues={deploymentReview.warnings} />
        </div>
      ) : null}
      {plan.prerequisites.some((item) => item.status !== "ready") ? (
        <div className="px-5 pt-4">
          <PlanPrerequisites plan={plan} />
        </div>
      ) : null}

      {deployment ? (
        <Alert className="mx-5 mt-4 w-auto">
          <CheckCircle2 />
          <AlertTitle>
            {deployment.created ? "Deployment created" : "Deployment already current"}
          </AlertTitle>
          <AlertDescription>
            {deploymentLabel(deployment.snapshot.ordinal, deployment.snapshot.version_id)} ·{" "}
            {deployment.snapshot.file_count} files · source{" "}
            {deployment.snapshot.merkle_root.slice(0, 8)}
          </AlertDescription>
        </Alert>
      ) : null}

      {!deployment ? (
        <DeploymentFileChanges pipelineId={pipelineId} plan={plan} status={status} />
      ) : null}

      <Collapsible open={detailsOpen} onOpenChange={setDetailsOpen}>
        <CollapsibleTrigger asChild>
          <Button
            variant="ghost"
            className="h-auto w-full justify-start rounded-none px-5 py-3 text-xs text-muted-foreground"
          >
            <ChevronRight
              className={cn("size-4 shrink-0 transition-transform", detailsOpen && "rotate-90")}
            />
            <span className="font-medium">Deployment details</span>
            <span className="truncate text-xs font-normal text-muted-foreground">
              {plan.summary.assets} {plan.summary.assets === 1 ? "asset" : "assets"}
              {runtimeChecks.length > 0
                ? ` · ${runtimeChecks.length} runtime ${runtimeChecks.length === 1 ? "check" : "checks"}`
                : ""}
              {deploymentReview.notices.length ? " · runtime notices" : ""}
            </span>
          </Button>
        </CollapsibleTrigger>
        <CollapsibleContent className="flex flex-col gap-5 border-t p-5">
          <p className="text-xs text-muted-foreground">
            Deployment captures saved definitions, not an unsaved editor buffer. Confirmation
            rechecks the reviewed source identity. Data execution and schedule promotion are
            separate actions.
          </p>
          <PlanIssues title="Runtime notices" issues={deploymentReview.notices} />
          <dl className="grid gap-2 text-xs sm:grid-cols-2">
            <PlanContextItem
              label="Representative window"
              value={formatPlanWindow(plan.context.start_date, plan.context.end_date)}
            />
            <PlanContextItem
              label="Representative mode"
              value={`${plan.context.full_refresh ? "full refresh" : "incremental"} · sensor ${plan.context.sensor_mode}`}
            />
          </dl>
          <PlanExecutionReview
            plan={plan}
            contentLoading={contentLoading}
            contentLoaded={contentLoaded}
            onLoadStageContent={onLoadStageContent}
            representative
          />
          {plan.prerequisites.length > 0 &&
          plan.prerequisites.every((item) => item.status === "ready") ? (
            <PlanPrerequisites plan={plan} />
          ) : null}
          <section aria-labelledby="pipeline-deployment-assets">
            <h3 id="pipeline-deployment-assets" className="mb-2 text-sm font-medium">
              Included assets
            </h3>
            <PlanAssets plan={plan} />
          </section>
          {runtimeChecks.length > 0 ? (
            <section aria-labelledby="pipeline-deployment-runtime-checks">
              <h3 id="pipeline-deployment-runtime-checks" className="mb-2 text-sm font-medium">
                Runtime quality checks
              </h3>
              <RuntimeChecksReview plan={plan} />
            </section>
          ) : null}
          <section aria-labelledby="pipeline-deployment-identities">
            <h3 id="pipeline-deployment-identities" className="mb-2 text-sm font-medium">
              Saved identities
            </h3>
            <DeploymentPlanDetails plan={plan} />
          </section>
        </CollapsibleContent>
      </Collapsible>

      {deployment ? (
        <div ref={schedulesRef}>
          <Collapsible
            open={schedulesOpen}
            onOpenChange={onSchedulesOpenChange}
            className="rounded-lg border"
          >
            <CollapsibleTrigger asChild>
              <Button
                variant="ghost"
                className="h-auto w-full justify-start rounded-lg px-3 py-2.5"
              >
                <ChevronRight
                  className={cn(
                    "size-4 shrink-0 transition-transform",
                    schedulesOpen && "rotate-90",
                  )}
                />
                <span className="font-medium">Update schedules</span>
                <span className="truncate text-xs font-normal text-muted-foreground">
                  {promotionCandidates.length > 0
                    ? `${promotionCandidates.length} ${promotionCandidates.length === 1 ? "pin can" : "pins can"} move`
                    : "all pins current"}
                </span>
              </Button>
            </CollapsibleTrigger>
            <CollapsibleContent className="border-t p-3">
              <DeploymentSchedulePromotion
                schedules={schedules}
                candidates={promotionCandidates}
                deployment={deployment}
                ownership={schedulerOwnership}
                selected={selectedScheduleKeys}
                onSelectedChange={onSelectedScheduleKeysChange}
                error={promotionError}
              />
            </CollapsibleContent>
          </Collapsible>
        </div>
      ) : null}
    </div>
  );
}

function PlanExecutionReview({
  plan,
  contentLoading,
  contentLoaded,
  onLoadStageContent,
  representative = false,
}: {
  plan: PipelinePlan;
  contentLoading: boolean;
  contentLoaded: boolean;
  onLoadStageContent: () => void;
  representative?: boolean;
}) {
  const [open, setOpen] = useState(false);
  const runtimeChecks = plan.assets.flatMap((asset) =>
    asset.renders.flatMap((render) => render.stages.filter((stage) => stage.kind === "check")),
  );
  const maxActiveSteps = plan.context.max_active_steps;
  const conservativelySerializedAssets = conservativeTargetIsolationCount(plan);
  const suffix = representative ? "deploy" : "run";

  return (
    <Collapsible
      open={open}
      onOpenChange={(nextOpen) => {
        setOpen(nextOpen);
        if (nextOpen) onLoadStageContent();
      }}
      className="rounded-lg border"
    >
      <CollapsibleTrigger asChild>
        <Button variant="ghost" className="h-auto w-full justify-start rounded-lg px-3 py-2.5">
          <ChevronRight
            className={cn("size-4 shrink-0 transition-transform", open && "rotate-90")}
          />
          <span className="font-medium">Execution details</span>
          <span className="truncate text-xs font-normal text-muted-foreground">
            {plan.summary.execution_units} {representative ? "representative " : ""}
            {plan.summary.execution_units === 1 ? "step" : "steps"} · order and rendered operations
          </span>
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent className="border-t p-3">
        <div className="space-y-5">
          <section aria-labelledby={`pipeline-plan-execution-order-${suffix}`}>
            <div className="mb-2 flex flex-wrap items-end justify-between gap-2">
              <div>
                <h3 id={`pipeline-plan-execution-order-${suffix}`} className="text-sm font-medium">
                  Execution order
                </h3>
                <p className="text-xs text-muted-foreground">
                  {plan.summary.execution_units}{" "}
                  {plan.summary.execution_units === 1 ? "step" : "steps"} across{" "}
                  {plan.summary.assets} {plan.summary.assets === 1 ? "asset" : "assets"}, shown in
                  stable plan order
                </p>
                <p className="text-xs text-muted-foreground">
                  {representative
                    ? "Scheduled runs render these operations again with their own execution context. "
                    : ""}
                  {maxActiveSteps > 1
                    ? `Up to ${maxActiveSteps} assets may run concurrently. Dependencies, connection limits, and shared targets can reduce that number.`
                    : "Assets will run one at a time for this pipeline."}
                  {conservativelySerializedAssets > 0
                    ? ` ${conservativelySerializedAssets} ${conservativelySerializedAssets === 1 ? "asset uses" : "assets use"} conservative target isolation and will run alone.`
                    : ""}
                </p>
              </div>
              <div className="flex flex-wrap gap-1">
                <Badge variant="outline" size="xs">
                  {maxActiveSteps > 1 ? `Up to ${maxActiveSteps} active` : "Sequential"}
                </Badge>
                {runtimeChecks.length > 0 ? (
                  <Badge variant="outline" size="xs">
                    {runtimeChecks.length} runtime {runtimeChecks.length === 1 ? "check" : "checks"}
                  </Badge>
                ) : null}
                {plan.summary.destructive_operations > 0 ? (
                  <Badge variant="destructive" size="xs">
                    {plan.summary.destructive_operations} destructive
                  </Badge>
                ) : null}
              </div>
            </div>
            <PlanExecutionSequence plan={plan} />
          </section>

          <section
            className="border-t pt-4"
            aria-labelledby={`pipeline-plan-rendered-operations-${suffix}`}
          >
            <div className="mb-2">
              <h3
                id={`pipeline-plan-rendered-operations-${suffix}`}
                className="text-sm font-medium"
              >
                Rendered operations
              </h3>
              <p className="text-xs text-muted-foreground">
                Inspect generated SQL and runtime operations before continuing.
              </p>
            </div>
            <PlanExecution
              plan={plan}
              contentLoading={contentLoading}
              contentLoaded={contentLoaded}
            />
          </section>
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

function PlanExecutionSequence({ plan }: { plan: PipelinePlan }) {
  const assetsByID = new Map(plan.assets.map((asset) => [asset.id, asset]));
  const assetsByName = new Map(plan.assets.map((asset) => [asset.name, asset]));

  if (plan.execution_units.length === 0) {
    return (
      <div className="rounded-lg border px-3 py-4 text-sm text-muted-foreground">
        {plan.assets.length === 0
          ? "No assets are selected."
          : "No executable steps were produced. Review the planning issues above."}
      </div>
    );
  }

  return (
    <ol className="divide-y rounded-lg border">
      {plan.execution_units.map((unit, index) => {
        const asset = assetsByID.get(unit.asset_id) ?? assetsByName.get(unit.asset_name);
        const render = asset?.renders[unit.render_index];
        const stages = render?.stages ?? [];
        const checks = stages.filter((stage) => stage.kind === "check");
        const operations = stages.filter((stage) => stage.kind !== "check");
        return (
          <li
            key={`${unit.asset_id}:${unit.render_index}:${unit.start_date}:${unit.end_date}`}
            className="grid min-w-0 grid-cols-[1.75rem_minmax(0,1fr)] gap-3 px-3 py-3"
          >
            <span className="flex size-7 items-center justify-center rounded-full border bg-muted/40 text-xs font-medium tabular-nums">
              {index + 1}
            </span>
            <div className="min-w-0">
              <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                <span className="truncate font-mono text-sm font-medium" title={unit.asset_name}>
                  {unit.asset_name}
                </span>
                {asset?.type ? (
                  <Badge variant="outline" size="xs">
                    {asset.type}
                  </Badge>
                ) : null}
                {asset?.staleness ? (
                  <Badge variant="muted" size="xs">
                    {humanizePlanToken(asset.staleness)}
                  </Badge>
                ) : null}
              </div>
              <div className="mt-1 flex flex-wrap gap-x-2 gap-y-0.5 text-[11px] text-muted-foreground">
                <span>{formatPlanWindow(unit.start_date, unit.end_date)}</span>
                <span>{humanizePlanToken(unit.reason)}</span>
                {unit.dependency_positions.length > 0 ? (
                  <span>
                    after{" "}
                    {unit.dependency_positions.map((position) => `step ${position + 1}`).join(", ")}
                  </span>
                ) : (
                  <span>ready immediately</span>
                )}
                <span>
                  {operations.length} {operations.length === 1 ? "operation" : "operations"}
                  {checks.length > 0
                    ? ` · ${checks.length} ${checks.length === 1 ? "check" : "checks"}`
                    : ""}
                </span>
              </div>
              {stages.length > 0 ? (
                <div className="mt-1.5 flex flex-wrap items-center gap-x-1 text-[11px] text-muted-foreground">
                  {stages.map((stage, stageIndex) => (
                    <span
                      key={`${stage.kind}:${stage.label ?? stage.check_name ?? stageIndex}`}
                      className="inline-flex items-center gap-1"
                    >
                      {stageIndex > 0 ? <span aria-hidden="true">→</span> : null}
                      <span>{assetRenderStageLabel(stage)}</span>
                    </span>
                  ))}
                </div>
              ) : null}
              {render?.issues?.length ? (
                <ul className="mt-2 space-y-1 text-xs text-amber-700 dark:text-amber-300">
                  {(render.issues ?? []).map((issue, issueIndex) => (
                    <li key={`${issue.code}:${issueIndex}`}>{issue.message}</li>
                  ))}
                </ul>
              ) : null}
            </div>
          </li>
        );
      })}
    </ol>
  );
}

function PlanCodeReview({ plan }: { plan: PipelinePlan }) {
  const report = plan.readiness.code_checks;
  const withFindings = report.assets.filter((asset) => asset.findings.length > 0);
  const presentationFindings = (report.presentations ?? []).filter(
    (artifact) => artifact.findings.length > 0,
  );
  if (withFindings.length === 0 && presentationFindings.length === 0) return null;
  const passing = report.assets.length - withFindings.length;
  return (
    <section aria-labelledby="pipeline-plan-code-review">
      <div className="mb-2 flex flex-wrap items-end justify-between gap-2">
        <div>
          <h3 id="pipeline-plan-code-review" className="text-sm font-medium">
            Code checks
          </h3>
          <p className="text-xs text-muted-foreground">
            {report.summary.errors} errors · {report.summary.warnings} warnings
          </p>
        </div>
        {passing > 0 ? (
          <span className="text-[11px] text-muted-foreground">
            {passing} {passing === 1 ? "asset" : "assets"} passed
          </span>
        ) : null}
      </div>
      <div className="divide-y rounded-lg border">
        {withFindings.map((asset) => (
          <div key={asset.name} className="px-3 py-2.5">
            <div className="font-mono text-sm font-medium">{asset.name}</div>
            <ul className="mt-1 flex flex-col gap-1 text-xs">
              {asset.findings.map((finding, index) => (
                <li
                  key={`${finding.code}:${finding.message}:${index}`}
                  className={
                    finding.severity === "error"
                      ? "text-destructive"
                      : "text-amber-700 dark:text-amber-300"
                  }
                >
                  {finding.message}
                </li>
              ))}
            </ul>
          </div>
        ))}
        {presentationFindings.map((artifact) => (
          <div key={`${artifact.kind}:${artifact.id}`} className="px-3 py-2.5">
            <div className="flex min-w-0 items-center gap-2">
              <Link
                to={
                  artifact.kind === "dashboard"
                    ? "/dashboards/$presentationId"
                    : "/reports/$presentationId"
                }
                params={{ presentationId: artifact.workspace_id }}
                className="min-w-0 truncate text-sm font-medium hover:underline"
              >
                {artifact.title}
              </Link>
              <Badge variant="muted" size="xs">
                {artifact.kind}
              </Badge>
            </div>
            <ul className="mt-1 flex flex-col gap-1 text-xs">
              {artifact.findings.map((finding, index) => (
                <li
                  key={`${finding.code}:${finding.path ?? ""}:${index}`}
                  className={
                    finding.severity === "error"
                      ? "text-destructive"
                      : "text-amber-700 dark:text-amber-300"
                  }
                >
                  {finding.message}
                  {finding.path ? (
                    <span className="ml-1 font-mono text-[10px] opacity-70">{finding.path}</span>
                  ) : null}
                </li>
              ))}
            </ul>
          </div>
        ))}
      </div>
    </section>
  );
}

function RunPlanDetails({ plan }: { plan: PipelinePlan }) {
  const conservativelySerializedAssets = conservativeTargetIsolationCount(plan);
  return (
    <div className="space-y-3">
      <div className="grid grid-cols-2 divide-x divide-y rounded-lg border sm:grid-cols-4 sm:divide-y-0">
        <SummaryMetric label="Assets" value={plan.summary.assets} />
        <SummaryMetric label="Execution units" value={plan.summary.execution_units} />
        <SummaryMetric label="Operations" value={plan.summary.stages} />
        <SummaryMetric label="Destructive" value={plan.summary.destructive_operations} />
      </div>
      <dl className="grid gap-x-6 gap-y-2 text-xs sm:grid-cols-2">
        <PlanDetailItem label="Source identity" value={plan.source.merkle_root} />
        <PlanDetailItem label="Variables identity" value={plan.context.variables_digest} />
        <PlanDetailItem
          label="Configuration identity"
          value={plan.context.configuration_digest || "not available"}
        />
        <PlanDetailItem
          label="Configuration fidelity"
          value={humanizePlanToken(plan.context.configuration_fidelity)}
        />
        <PlanDetailItem
          label="Write isolation"
          value={humanizePlanToken(plan.resources.isolation)}
        />
        <PlanDetailItem
          label="Write claims"
          value={`${plan.resources.claims.length} ${plan.resources.claims.length === 1 ? "resource" : "resources"}`}
        />
        <PlanDetailItem
          label="Maximum active steps"
          value={String(plan.context.max_active_steps)}
        />
        <PlanDetailItem
          label="Conservative target isolation"
          value={`${conservativelySerializedAssets} ${conservativelySerializedAssets === 1 ? "asset" : "assets"}`}
        />
      </dl>
      {plan.resources.claims.length > 0 ? (
        <div className="flex flex-wrap gap-1">
          {plan.resources.claims.map((claim) => (
            <Badge
              key={`${claim.kind}:${claim.identity}`}
              variant="muted"
              size="xs"
              title={claim.identity}
            >
              {humanizePlanToken(claim.kind)} · {claim.identity.slice(0, 10)}
            </Badge>
          ))}
        </div>
      ) : null}
      <p className="text-[11px] text-muted-foreground">
        These identities and the complete ordered plan are rechecked when you confirm and before
        execution starts.
      </p>
    </div>
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

function conservativeTargetIsolationCount(plan: PipelinePlan) {
  return plan.execution_contracts.filter(
    (contract) => contract.coordination_resources.isolation === "pipeline",
  ).length;
}

function DeploymentPlanDetails({ plan }: { plan: PipelinePlan }) {
  return (
    <div className="flex flex-col gap-3">
      <dl className="grid gap-x-6 gap-y-2 text-xs sm:grid-cols-2">
        <PlanDetailItem label="Source identity" value={plan.source.merkle_root} />
        <PlanDetailItem label="Variables identity" value={plan.context.variables_digest} />
        <PlanDetailItem
          label="Configuration identity"
          value={plan.context.configuration_digest || "not available"}
        />
        <PlanDetailItem
          label="Configuration fidelity"
          value={humanizePlanToken(plan.context.configuration_fidelity)}
        />
      </dl>
      <p className="text-[11px] text-muted-foreground">
        Deployment rechecks the saved source identity before capturing files. The configuration,
        variables, and operations describe representative future execution; scheduled runs render
        them again with their own context.
      </p>
    </div>
  );
}

function PlanDetailItem({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-[10px] font-medium tracking-wide text-muted-foreground uppercase">
        {label}
      </dt>
      <dd className="truncate font-mono" title={value}>
        {value}
      </dd>
    </div>
  );
}

function PlanStatusBadge({ status }: { status: string }) {
  return (
    <Badge
      variant={
        status === "blocked" ? "destructive" : status === "warning" ? "secondary" : "outline"
      }
      size="xs"
    >
      {status === "ready" ? (
        <CheckCircle2 data-icon="inline-start" />
      ) : (
        <CircleAlert data-icon="inline-start" />
      )}
      {status}
    </Badge>
  );
}

function SummaryMetric({ label, value }: { label: string; value: number }) {
  return (
    <div className="px-3 py-2.5">
      <div className="text-lg font-semibold tabular-nums">{value}</div>
      <div className="text-[10px] tracking-wide text-muted-foreground uppercase">{label}</div>
    </div>
  );
}

function PlanIssues({
  title,
  issues,
  destructive = false,
}: {
  title: string;
  issues: PipelinePlan["readiness"]["blockers"];
  destructive?: boolean;
}) {
  if (issues.length === 0) return null;
  return (
    <Alert
      variant={destructive ? "destructive" : "default"}
      className={cn(!destructive && "border-amber-500/40")}
    >
      <AlertTriangle />
      <AlertTitle>{title}</AlertTitle>
      <AlertDescription>
        <ul className="space-y-1">
          {issues.map((issue, index) => (
            <li key={`${issue.code}:${issue.asset_id ?? index}`}>
              {issue.asset_name ? <span className="font-medium">{issue.asset_name}: </span> : null}
              {issue.message}
            </li>
          ))}
        </ul>
      </AlertDescription>
    </Alert>
  );
}

function PlanAssets({ plan }: { plan: PipelinePlan }) {
  if (plan.assets.length === 0) {
    return <p className="text-muted-foreground">No assets are selected for this plan.</p>;
  }
  return (
    <div className="divide-y border-y">
      {plan.assets.map((asset, index) => (
        <div key={asset.id} className="flex min-w-0 gap-3 py-3">
          <span className="w-5 shrink-0 text-right text-[10px] tabular-nums text-muted-foreground">
            {index + 1}
          </span>
          <div className="min-w-0 flex-1">
            <div className="flex min-w-0 flex-wrap items-center gap-1.5">
              <span className="truncate font-mono font-medium" title={asset.name}>
                {asset.name}
              </span>
              <Badge variant="outline" size="xs">
                {asset.type}
              </Badge>
              {asset.staleness ? (
                <Badge variant="muted" size="xs">
                  {asset.staleness.replaceAll("_", " ")}
                </Badge>
              ) : null}
            </div>
            <div className="mt-1 flex flex-wrap gap-1 text-[11px] text-muted-foreground">
              {asset.inclusion_reasons.map((reason) => (
                <span key={reason}>{reason.replaceAll("_", " ")}</span>
              ))}
              <span>
                · {asset.renders.length} render{asset.renders.length === 1 ? "" : "s"}
              </span>
            </div>
          </div>
        </div>
      ))}
    </div>
  );
}

function RuntimeChecksReview({ plan }: { plan: PipelinePlan }) {
  const runtimeChecks = plan.assets.flatMap((asset) =>
    asset.renders.flatMap((render) =>
      render.stages
        .filter((stage) => stage.kind === "check")
        .map((stage) => ({ asset: asset.name, stage })),
    ),
  );
  if (runtimeChecks.length === 0) {
    return <p className="text-muted-foreground">No runtime checks are planned.</p>;
  }
  return (
    <div className="divide-y border-y">
      {runtimeChecks.map(({ asset, stage }, index) => (
        <div
          key={`${asset}:${stage.label ?? stage.kind}:${index}`}
          className="flex items-center justify-between gap-3 py-2.5"
        >
          <div className="min-w-0">
            <div className="truncate font-medium">{stage.label || "Quality check"}</div>
            <div className="truncate font-mono text-[11px] text-muted-foreground">{asset}</div>
          </div>
          <Badge variant={stage.status === "ok" ? "outline" : "destructive"} size="xs">
            {stage.fidelity}
          </Badge>
        </div>
      ))}
    </div>
  );
}

function PlanExecution({
  plan,
  contentLoading,
  contentLoaded,
}: {
  plan: PipelinePlan;
  contentLoading: boolean;
  contentLoaded: boolean;
}) {
  const operations = useMemo(
    () =>
      plan.assets.flatMap((asset) =>
        asset.renders.flatMap((render, renderIndex) =>
          render.stages.map((stage, stageIndex) => ({
            key: `${asset.id}:${renderIndex}:${stageIndex}`,
            asset: asset.name,
            render,
            stage,
          })),
        ),
      ),
    [plan],
  );
  const [selectedKey, setSelectedKey] = useState(operations[0]?.key ?? "");
  useEffect(() => {
    if (!operations.some((operation) => operation.key === selectedKey)) {
      setSelectedKey(operations[0]?.key ?? "");
    }
  }, [operations, selectedKey]);
  const operation = operations.find((candidate) => candidate.key === selectedKey) ?? operations[0];

  if (contentLoading && !contentLoaded) {
    return (
      <div className="flex h-80 items-center justify-center gap-2 text-muted-foreground">
        <Loader2 className="size-4 animate-spin" /> Loading rendered operations…
      </div>
    );
  }
  if (!operation) {
    return <p className="text-muted-foreground">No renderable operations are planned.</p>;
  }
  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-end gap-2">
        <div className="min-w-0 flex-1">
          <Label
            htmlFor="pipeline-plan-operation"
            className="mb-1 block text-[11px] text-muted-foreground"
          >
            Operation
          </Label>
          <Select value={operation.key} onValueChange={setSelectedKey}>
            <SelectTrigger id="pipeline-plan-operation" size="sm" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {operations.map((candidate) => (
                <SelectItem key={candidate.key} value={candidate.key}>
                  {candidate.asset} · {assetRenderStageLabel(candidate.stage)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <Badge variant="outline" size="xs">
          {operation.stage.fidelity}
        </Badge>
        <Badge variant="muted" size="xs">
          Preview — not executed
        </Badge>
      </div>
      <div className="flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
        <span className="font-mono text-foreground">{operation.asset}</span>
        <span>{formatPlanWindow(operation.render.start_date, operation.render.end_date)}</span>
        <span>{operation.stage.language}</span>
      </div>
      <div className="h-[min(52vh,32rem)] min-h-72 overflow-hidden rounded-md border bg-background">
        {operation.stage.content ? (
          <ReadOnlyRenderedOperation
            content={operation.stage.content}
            language={operation.stage.language || "text"}
            modelKey={`pipeline-plan:${plan.id}:${operation.key}`}
          />
        ) : (
          <div className="flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-muted-foreground">
            <FileCode2 className="size-5" />
            <p>{operation.stage.message || "This operation is only available at runtime."}</p>
          </div>
        )}
      </div>
    </div>
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
      {impact?.status === "available" && impact.complete ? null : (
        <p className="px-5 py-3 text-xs text-muted-foreground" role="status">
          {impact?.status === "no_baseline"
            ? "First deployment — no semantic baseline yet."
            : impact?.status === "available"
              ? "Semantic analysis is incomplete; additional effects may be unknown."
              : impact?.reason ||
                "Semantic analysis is unavailable. Review source changes and code checks."}
        </p>
      )}
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

function DeploymentSchedulePromotion({
  schedules,
  candidates,
  deployment,
  ownership,
  selected,
  onSelectedChange,
  error,
}: {
  schedules: EnvSchedule[];
  candidates: EnvSchedule[];
  deployment: DeployResponse | null;
  ownership: SchedulerOwnership | null;
  selected: Set<string>;
  onSelectedChange: (selected: Set<string>) => void;
  error: string | null;
}) {
  if (schedules.length === 0) {
    return (
      <Alert>
        <CheckCircle2 />
        <AlertTitle>No schedules to update</AlertTitle>
        <AlertDescription>
          This pipeline has no active or paused environment schedules.
        </AlertDescription>
      </Alert>
    );
  }

  if (!deployment) {
    return (
      <div className="space-y-3">
        <div>
          <h3 className="text-sm font-medium">Current schedule pins</h3>
          <p className="text-xs text-muted-foreground">
            Deployment does not move these automatically. After creating the deployment, you can
            select exactly which schedules to promote.
          </p>
        </div>
        <dl className="divide-y rounded-md border">
          {schedules.map((schedule) => (
            <div
              key={schedule.environment}
              className="flex min-w-0 items-center justify-between gap-3 px-3 py-2 text-xs"
            >
              <dt className="min-w-0 truncate font-medium">{schedule.environment}</dt>
              <dd className="shrink-0 font-mono text-muted-foreground">
                {deploymentLabel(schedule.snapshot_ordinal, schedule.snapshot_version_id)}
              </dd>
            </div>
          ))}
        </dl>
      </div>
    );
  }

  if (candidates.length === 0) {
    return (
      <Alert>
        <CheckCircle2 />
        <AlertTitle>Schedules are current</AlertTitle>
        <AlertDescription>
          Every schedule for this pipeline is pinned to{" "}
          {deploymentLabel(
            deployment.snapshot.ordinal,
            deployment.snapshot.version_id,
            "deployment",
          )}
          .
        </AlertDescription>
      </Alert>
    );
  }

  const canPromote = ownership?.state === "owner";
  return (
    <div className="space-y-4">
      <div>
        <h3 className="text-sm font-medium">Promote selected schedules</h3>
        <p className="text-xs text-muted-foreground">
          Move only the checked schedule pins to{" "}
          {deploymentLabel(
            deployment.snapshot.ordinal,
            deployment.snapshot.version_id,
            "deployment",
          )}
          . Unchecked schedules keep their current deployment state.
        </p>
      </div>
      {!canPromote ? (
        <Alert variant="destructive">
          <ShieldAlert />
          <AlertTitle>Schedules are read-only here</AlertTitle>
          <AlertDescription>
            {ownership?.message ?? "Scheduler ownership is unavailable."}
          </AlertDescription>
        </Alert>
      ) : null}
      {error ? (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertTitle>Schedules were not updated</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}
      <FieldGroup data-slot="checkbox-group" className="gap-2">
        {candidates.map((schedule) => {
          const checkboxID = `promote-schedule-${schedule.environment}`;
          return (
            <Field
              key={schedule.environment}
              orientation="horizontal"
              data-disabled={!canPromote || undefined}
              className="rounded-md border px-3 py-2"
            >
              <Checkbox
                id={checkboxID}
                checked={selected.has(schedule.environment)}
                disabled={!canPromote}
                onCheckedChange={(checked) => {
                  const next = new Set(selected);
                  if (checked === true) next.add(schedule.environment);
                  else next.delete(schedule.environment);
                  onSelectedChange(next);
                }}
              />
              <FieldContent>
                <FieldLabel htmlFor={checkboxID}>{schedule.environment}</FieldLabel>
                <FieldDescription>
                  {deploymentLabel(schedule.snapshot_ordinal, schedule.snapshot_version_id)} ·{" "}
                  {schedule.status}
                </FieldDescription>
              </FieldContent>
            </Field>
          );
        })}
      </FieldGroup>
    </div>
  );
}

function PlanLoading() {
  return (
    <div className="space-y-4" aria-label="Planning pipeline">
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
        {Array.from({ length: 4 }).map((_, index) => (
          <Skeleton key={index} className="h-14" />
        ))}
      </div>
      <Skeleton className="h-20" />
      <Skeleton className="h-32" />
    </div>
  );
}

function sourceInputLabel(source?: PipelineRunSource | null) {
  if (!source) return "Policy default";
  return source.source === "working_tree"
    ? "Saved working tree"
    : deploymentLabel(undefined, source.snapshot_version_id);
}

function planSourceLabel(plan: PipelinePlan) {
  return plan.source.kind === "working_tree"
    ? `Saved working tree · ${plan.source.merkle_root.slice(0, 8)}`
    : deploymentLabel(
        plan.source.deployment_ordinal,
        plan.source.version_id || plan.source.merkle_root,
      );
}

function runSourceLabel(plan: PipelinePlan) {
  return plan.source.kind === "working_tree"
    ? "working tree"
    : deploymentLabel(
        plan.source.deployment_ordinal,
        plan.source.version_id || plan.source.merkle_root,
        "deployment",
      );
}

function humanizePlanToken(value: string) {
  return value.replaceAll("_", " ").replace(/\b\w/g, (character) => character.toUpperCase());
}

function formatPlanWindow(start: string, end: string) {
  const format = (value: string) => {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toISOString().slice(0, 16).replace("T", " ");
  };
  return `${format(start)}–${format(end)} UTC`;
}
