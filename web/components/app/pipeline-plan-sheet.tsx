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
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  ReadOnlyRenderedOperation,
  ReadOnlyRenderedOperationDiff,
  assetRenderStageLabel,
} from "@/components/app/asset-render-view";
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
import { awaitWorkspaceSaves } from "@/lib/workspace-save-barrier";
import { cn } from "@/lib/utils";
import { deploymentLabel } from "@/lib/deployment-label";

type PlanSelectionMode = "all" | "needed" | "selector" | "selector_needed";
type SensorMode = "once" | "wait" | "skip";
type PlanIntent = "run" | "deploy";

export function PipelinePlanSheet({
  open,
  onOpenChange,
  pipelineId,
  pipelineName,
  environment,
  timeWindow,
  source,
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
  intent?: PlanIntent;
  confirmDestructive?: boolean;
  onAccepted?: (run: PipelineRun, plan: PipelinePlan) => void;
  onDeploy?: (expectedSourceMerkle: string) => Promise<DeployResponse>;
  onSchedulesChanged?: () => void | Promise<void>;
}) {
  const [request, setRequest] = useState<PipelinePlanRequest | null>(null);
  const [plan, setPlan] = useState<PipelinePlan | null>(null);
  const [loading, setLoading] = useState(false);
  const [contentLoading, setContentLoading] = useState(false);
  const [stageContentLoaded, setStageContentLoaded] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [confirmation, setConfirmation] = useState("");
  const [activeRunId, setActiveRunId] = useState<string | null>(null);
  const [deploySchedulesOpen, setDeploySchedulesOpen] = useState(false);
  const [deployStatus, setDeployStatus] = useState<DeployStatus | null>(null);
  const [deployment, setDeployment] = useState<DeployResponse | null>(null);
  const [schedules, setSchedules] = useState<EnvSchedule[]>([]);
  const [schedulerOwnership, setSchedulerOwnership] = useState<SchedulerOwnership | null>(null);
  const [selectedScheduleKeys, setSelectedScheduleKeys] = useState<Set<string>>(() => new Set());
  const [promoting, setPromoting] = useState(false);
  const [promotionError, setPromotionError] = useState<string | null>(null);
  const [selectorDraft, setSelectorDraft] = useState("*");
  const requestSerial = useRef(0);
  const initialPlanContext = useRef<string | null>(null);
  const requestedSourceKind = intent === "deploy" ? "working_tree" : source?.source;
  const requestedSourceVersion =
    intent !== "deploy" && source?.source === "snapshot" ? source.snapshot_version_id : undefined;

  const fetchPlan = useCallback(
    async (input: PipelinePlanRequest, includeStageContent = false) => {
      const serial = ++requestSerial.current;
      if (includeStageContent) setContentLoading(true);
      else setLoading(true);
      setError(null);
      setActiveRunId(null);
      try {
        const next = await planPipeline(pipelineId, {
          ...input,
          include_stage_content: includeStageContent,
        });
        if (serial !== requestSerial.current) return;
        setPlan(next);
        if (next.selection.mode === "selector" || next.selection.mode === "selector_needed") {
          setSelectorDraft(next.selection.selector ?? "");
        }
        setRequest({
          ...canonicalPipelinePlanRequest(next, false),
          purpose: input.purpose,
        });
        setStageContentLoaded(includeStageContent);
      } catch (cause) {
        if (serial !== requestSerial.current) return;
        setError(cause instanceof Error ? cause.message : "Pipeline planning failed.");
      } finally {
        if (serial === requestSerial.current) {
          setLoading(false);
          setContentLoading(false);
        }
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
    setPlan(null);
    setRequest(null);
    setError(null);
    setActiveRunId(null);
    setConfirmation("");
    setDeploySchedulesOpen(false);
    setStageContentLoaded(false);
    setDeployStatus(null);
    setDeployment(null);
    setSchedules([]);
    setSchedulerOwnership(null);
    setSelectedScheduleKeys(new Set());
    setPromotionError(null);
    setSelectorDraft("*");
    setLoading(true);
    const serial = ++requestSerial.current;
    void (async () => {
      try {
        await awaitWorkspaceSaves();
        if (serial !== requestSerial.current) return;
        const input: PipelinePlanRequest = {
          purpose: intent === "deploy" ? "deployment" : "execution",
          environment: environment || undefined,
          start_date: timeWindow?.start,
          end_date: timeWindow?.end,
          execution_time: new Date().toISOString(),
          sensor_mode: "once",
          source: requestedSourceKind
            ? {
                kind: requestedSourceKind,
                version_id: requestedSourceVersion,
              }
            : undefined,
          selection: { mode: "all" },
        };
        setRequest(input);
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
        setError(cause instanceof Error ? cause.message : "Saving the workspace failed.");
        setLoading(false);
      }
    })();
  }, [
    environment,
    fetchPlan,
    intent,
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
    setRequest(next);
    setStageContentLoaded(false);
    void fetchPlan(next);
  };

  const selectionMode = (request?.selection?.mode ??
    plan?.selection.mode ??
    "all") as PlanSelectionMode;
  const selectorMode = selectionMode === "selector" || selectionMode === "selector_needed";
  const appliedSelector = request?.selection?.selector?.trim() ?? "";
  const selectorDraftApplied = !selectorMode || selectorDraft.trim() === appliedSelector;
  const selectorPlanIsCurrent = Boolean(
    selectorMode &&
    plan?.selection.mode === selectionMode &&
    plan.selection.selector === appliedSelector,
  );
  const sensorMode = (request?.sensor_mode ?? plan?.context.sensor_mode ?? "once") as SensorMode;
  const fullRefresh = Boolean(request?.full_refresh ?? plan?.context.requested_full_refresh);
  const destructiveConfirmationRequired = Boolean(
    intent === "run" && confirmDestructive && plan?.context.destructive,
  );
  const confirmationMatches =
    !destructiveConfirmationRequired || confirmation.trim() === plan?.context.environment;
  const hasBlockers = Boolean(
    plan && (plan.status === "blocked" || plan.readiness.blockers.length),
  );
  const canConfirm = Boolean(
    plan &&
    !hasBlockers &&
    confirmationMatches &&
    selectorDraftApplied &&
    !loading &&
    !error &&
    !deployment &&
    (intent === "deploy" ? plan.summary.assets > 0 : plan.summary.execution_units > 0),
  );

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
    void fetchPlan(canonicalPipelinePlanRequest(plan, true), true);
  };

  const confirm = async () => {
    if (!plan || !canConfirm) return;
    setConfirming(true);
    setError(null);
    setActiveRunId(null);
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
        setError("The saved source changed after review. Review the refreshed deployment plan.");
        setDeployStatus(await getDeployStatus(pipelineId));
        await fetchPlan(request);
        return;
      }
      const refreshed = pipelinePlanFromConflict(cause);
      if (refreshed) {
        setPlan(refreshed);
        setRequest(canonicalPipelinePlanRequest(refreshed, false));
        setStageContentLoaded(false);
        setError(
          cause instanceof APIError && cause.code === "plan_data_changed"
            ? "The data state now requires additional or changed work. Review the refreshed plan before running."
            : cause instanceof APIError && cause.code === "plan_stale"
              ? "The source or configuration changed. Review the refreshed plan before running."
              : cause instanceof Error
                ? cause.message
                : "The refreshed plan is blocked.",
        );
        return;
      }
      const active = activePipelineRunConflict(cause);
      if (active) {
        setActiveRunId(active.activeRunId);
        setError("Another run was admitted first. Open it to follow its progress.");
        return;
      }
      setError(cause instanceof Error ? cause.message : "Pipeline run could not be started.");
    } finally {
      setConfirming(false);
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
        className="flex h-[min(92dvh,58rem)] max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-none flex-col gap-0 overflow-hidden p-0 sm:max-w-6xl xl:max-w-7xl"
        data-testid="pipeline-plan-sheet"
      >
        <ScrollArea className="min-h-0 flex-1" data-testid="pipeline-plan-scroll">
          <DialogHeader className="border-b px-5 py-4 pr-12">
            <div className="flex min-w-0 items-center gap-2">
              <DialogTitle className="truncate">
                {intent === "deploy" ? "Review deployment" : "Review pipeline run"}
              </DialogTitle>
              {plan ? <PlanStatusBadge status={plan.status} /> : null}
              {(loading || contentLoading) && plan ? (
                <Loader2 className="size-3.5 animate-spin" />
              ) : null}
            </div>
            <DialogDescription>
              {pipelineName} · saved source preview ·{" "}
              {intent === "deploy"
                ? "no data is executed by deployment"
                : "nothing executes until you confirm"}
            </DialogDescription>
          </DialogHeader>

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
              {intent === "deploy" ? (
                <PlanContextItem
                  label="Mode"
                  value={`${fullRefresh ? "full refresh" : "incremental"} · sensor ${sensorMode}`}
                />
              ) : null}
            </dl>
            {intent === "run" ? (
              <FieldSet className="mt-3 gap-3 border-t pt-3">
                <FieldLegend
                  variant="label"
                  className="mb-0 text-[10px] tracking-wide text-muted-foreground uppercase"
                >
                  Run options
                </FieldLegend>
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
                      onValueChange={(value) => {
                        const mode = value as PlanSelectionMode;
                        const usesSelector = mode === "selector" || mode === "selector_needed";
                        const selector = selectorDraft.trim() || appliedSelector || "*";
                        if (usesSelector) setSelectorDraft(selector);
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
                          <SelectItem value="all">Entire pipeline</SelectItem>
                          <SelectItem value="needed">Needed assets</SelectItem>
                          <SelectItem value="selector">Matching selector</SelectItem>
                          <SelectItem value="selector_needed">Needed matching selector</SelectItem>
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
                          onChange={(event) => setSelectorDraft(event.target.value)}
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
            ) : (
              <Alert className="mt-3">
                <Package />
                <AlertTitle>Representative execution preview</AlertTitle>
                <AlertDescription>
                  Deployment stores this saved source, not rendered SQL. Scheduled runs render it
                  again with their actual interval, environment, and variables.
                </AlertDescription>
              </Alert>
            )}
          </div>

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
                onChange={(event) => setConfirmation(event.target.value)}
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
                  ? "Deployment rechecks the saved source identity. It never deploys an unsaved editor buffer."
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
    <div className="mx-auto w-full max-w-5xl space-y-5 p-5">
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
  const [assetsOpen, setAssetsOpen] = useState(false);
  const [checksOpen, setChecksOpen] = useState(false);
  const [detailsOpen, setDetailsOpen] = useState(false);
  const schedulesRef = useRef<HTMLDivElement | null>(null);
  const runtimeChecks = plan.assets.flatMap((asset) =>
    asset.renders.flatMap((render) => render.stages.filter((stage) => stage.kind === "check")),
  );
  const changedFiles = status
    ? (status.added_files?.length ?? 0) +
      (status.changed_files?.length ?? 0) +
      (status.removed_files?.length ?? 0)
    : null;

  useEffect(() => {
    if (!deployment || !schedulesOpen) return;
    const frame = window.requestAnimationFrame(() =>
      schedulesRef.current?.scrollIntoView({ behavior: "smooth", block: "nearest" }),
    );
    return () => window.cancelAnimationFrame(frame);
  }, [deployment, schedulesOpen]);

  return (
    <div className="mx-auto flex w-full max-w-5xl flex-col gap-5 p-5">
      <PlanIssues title="Blockers" issues={plan.readiness.blockers} destructive />
      <PlanIssues title="Warnings" issues={plan.readiness.warnings} />
      <PlanPrerequisites plan={plan} />

      {deployment ? (
        <Alert>
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
      ) : plan.readiness.blockers.length === 0 && plan.readiness.warnings.length === 0 ? (
        <Alert>
          <CheckCircle2 />
          <AlertTitle>Ready to deploy</AlertTitle>
          <AlertDescription>
            The saved source and representative operation graph passed planning.
          </AlertDescription>
        </Alert>
      ) : null}

      <section aria-labelledby="pipeline-deploy-source-changes">
        <div className="mb-2 flex flex-wrap items-end justify-between gap-2">
          <div>
            <h3 id="pipeline-deploy-source-changes" className="text-sm font-medium">
              Source changes
            </h3>
            <p className="text-xs text-muted-foreground">
              {changedFiles === null
                ? "Comparing the saved workspace with the latest deployment…"
                : changedFiles === 0
                  ? "The saved workspace matches the latest deployment."
                  : `${changedFiles} changed ${changedFiles === 1 ? "file" : "files"} will be captured.`}
            </p>
          </div>
          <Badge variant="outline" size="xs">
            {plan.summary.assets} {plan.summary.assets === 1 ? "asset" : "assets"}
          </Badge>
        </div>
        <DeploymentFileChanges pipelineId={pipelineId} status={status} autoOpenFirst={false} />
      </section>

      <PlanCodeReview plan={plan} />

      <PlanExecutionReview
        plan={plan}
        contentLoading={contentLoading}
        contentLoaded={contentLoaded}
        onLoadStageContent={onLoadStageContent}
        representative
      />

      <Collapsible open={assetsOpen} onOpenChange={setAssetsOpen} className="rounded-lg border">
        <CollapsibleTrigger asChild>
          <Button variant="ghost" className="h-auto w-full justify-start rounded-lg px-3 py-2.5">
            <ChevronRight
              className={cn("size-4 shrink-0 transition-transform", assetsOpen && "rotate-90")}
            />
            <span className="font-medium">Deployment contents</span>
            <span className="truncate text-xs font-normal text-muted-foreground">
              {plan.summary.assets} {plan.summary.assets === 1 ? "asset" : "assets"} ·{" "}
              {plan.summary.execution_units} representative{" "}
              {plan.summary.execution_units === 1 ? "step" : "steps"}
            </span>
          </Button>
        </CollapsibleTrigger>
        <CollapsibleContent className="border-t p-3">
          <PlanAssets plan={plan} />
        </CollapsibleContent>
      </Collapsible>

      {runtimeChecks.length > 0 ? (
        <Collapsible open={checksOpen} onOpenChange={setChecksOpen} className="rounded-lg border">
          <CollapsibleTrigger asChild>
            <Button variant="ghost" className="h-auto w-full justify-start rounded-lg px-3 py-2.5">
              <ChevronRight
                className={cn("size-4 shrink-0 transition-transform", checksOpen && "rotate-90")}
              />
              <span className="font-medium">Runtime quality checks</span>
              <span className="truncate text-xs font-normal text-muted-foreground">
                {runtimeChecks.length} {runtimeChecks.length === 1 ? "check" : "checks"} previewed
              </span>
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent className="border-t p-3">
            <RuntimeChecksReview plan={plan} />
          </CollapsibleContent>
        </Collapsible>
      ) : null}

      <Collapsible open={detailsOpen} onOpenChange={setDetailsOpen} className="rounded-lg border">
        <CollapsibleTrigger asChild>
          <Button variant="ghost" className="h-auto w-full justify-start rounded-lg px-3 py-2.5">
            <ChevronRight
              className={cn("size-4 shrink-0 transition-transform", detailsOpen && "rotate-90")}
            />
            <span className="font-medium">Plan identities</span>
            <span className="truncate text-xs font-normal text-muted-foreground">
              saved source, configuration, and variables
            </span>
          </Button>
        </CollapsibleTrigger>
        <CollapsibleContent className="border-t p-3">
          <DeploymentPlanDetails plan={plan} />
        </CollapsibleContent>
      </Collapsible>

      <div ref={schedulesRef}>
        <Collapsible
          open={schedulesOpen}
          onOpenChange={onSchedulesOpenChange}
          className="rounded-lg border"
        >
          <CollapsibleTrigger asChild>
            <Button variant="ghost" className="h-auto w-full justify-start rounded-lg px-3 py-2.5">
              <ChevronRight
                className={cn("size-4 shrink-0 transition-transform", schedulesOpen && "rotate-90")}
              />
              <span className="font-medium">Schedules</span>
              <span className="truncate text-xs font-normal text-muted-foreground">
                {deployment
                  ? promotionCandidates.length > 0
                    ? `${promotionCandidates.length} ${promotionCandidates.length === 1 ? "pin can" : "pins can"} move`
                    : "all pins current"
                  : `${schedules.length} current ${schedules.length === 1 ? "pin" : "pins"}`}
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
      {withFindings.length === 0 ? (
        <div className="flex items-center gap-2 rounded-lg border px-3 py-2.5 text-sm">
          <CheckCircle2
            className="size-4 shrink-0 text-primary"
            aria-label="All code checks passed"
          />
          <span>All code checks passed.</span>
        </div>
      ) : (
        <div className="divide-y rounded-lg border">
          {withFindings.map((asset) => (
            <div key={asset.name} className="px-3 py-2.5">
              <div className="font-mono text-sm font-medium">{asset.name}</div>
              <ul className="mt-1 space-y-1 text-xs">
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
        </div>
      )}
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

type DeploymentChange = {
  path: string;
  label: "Added" | "Changed" | "Removed";
  variant: "secondary" | "outline" | "destructive";
};

function DeploymentFileChanges({
  pipelineId,
  status,
  autoOpenFirst = true,
}: {
  pipelineId: string;
  status: DeployStatus | null;
  autoOpenFirst?: boolean;
}) {
  const changes = useMemo<DeploymentChange[]>(
    () =>
      status
        ? [
            ...(status.added_files ?? []).map((path) => ({
              path,
              label: "Added" as const,
              variant: "secondary" as const,
            })),
            ...(status.changed_files ?? []).map((path) => ({
              path,
              label: "Changed" as const,
              variant: "outline" as const,
            })),
            ...(status.removed_files ?? []).map((path) => ({
              path,
              label: "Removed" as const,
              variant: "destructive" as const,
            })),
          ]
        : [],
    [status],
  );
  const [selectedPath, setSelectedPath] = useState("");
  const [diff, setDiff] = useState<DeploymentFileDiff | null>(null);
  const [diffLoading, setDiffLoading] = useState(false);
  const [diffError, setDiffError] = useState<string | null>(null);

  useEffect(() => {
    setSelectedPath((current) =>
      changes.some((change) => change.path === current)
        ? current
        : autoOpenFirst
          ? (changes[0]?.path ?? "")
          : "",
    );
  }, [autoOpenFirst, changes]);

  useEffect(() => {
    if (!status || !selectedPath) {
      setDiff(null);
      setDiffError(null);
      setDiffLoading(false);
      return;
    }
    let cancelled = false;
    setDiffLoading(true);
    setDiffError(null);
    getDeploymentFileDiff(pipelineId, selectedPath, status.version_id)
      .then((nextDiff) => {
        if (!cancelled) setDiff(nextDiff);
      })
      .catch((cause: unknown) => {
        if (cancelled) return;
        setDiff(null);
        setDiffError(
          cause instanceof Error ? cause.message : "Could not load the file comparison.",
        );
      })
      .finally(() => {
        if (!cancelled) setDiffLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [pipelineId, selectedPath, status?.source_merkle, status?.version_id]);

  if (!status) {
    return <Skeleton className="h-40" />;
  }
  const groups = [
    { label: "Added", paths: status.added_files ?? [], variant: "secondary" as const },
    { label: "Changed", paths: status.changed_files ?? [], variant: "outline" as const },
    { label: "Removed", paths: status.removed_files ?? [], variant: "destructive" as const },
  ].filter((group) => group.paths.length > 0);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <Badge variant="muted" size="xs" title={status.source_merkle}>
          Saved source {status.source_merkle?.slice(0, 8) || "unavailable"}
        </Badge>
        {status.has_snapshot ? (
          <span>
            Compared with {deploymentLabel(status.ordinal, status.version_id, "deployment")}
          </span>
        ) : (
          <span>First deployment; every source file is new.</span>
        )}
      </div>
      {groups.length === 0 ? (
        <Alert>
          <CheckCircle2 />
          <AlertTitle>No source changes</AlertTitle>
          <AlertDescription>
            The saved working tree already matches the latest deployment.
          </AlertDescription>
        </Alert>
      ) : (
        <div className="flex min-w-0 flex-col gap-4">
          {groups.map((group) => (
            <section key={group.label} className="flex min-w-0 flex-col gap-2">
              <h3 className="text-xs font-medium">
                {group.label} <span className="text-muted-foreground">({group.paths.length})</span>
              </h3>
              <div className="divide-y overflow-hidden rounded-md border">
                {group.paths.map((path) => {
                  const open = selectedPath === path;
                  return (
                    <Collapsible
                      key={path}
                      open={open}
                      onOpenChange={(nextOpen) => setSelectedPath(nextOpen ? path : "")}
                    >
                      <CollapsibleTrigger asChild>
                        <button
                          type="button"
                          className={cn(
                            "flex w-full min-w-0 items-center gap-2 px-3 py-2 text-left text-xs transition-colors hover:bg-muted/50",
                            open && "bg-muted",
                          )}
                        >
                          <ChevronRight
                            className={cn(
                              "size-3.5 shrink-0 text-muted-foreground transition-transform",
                              open && "rotate-90",
                            )}
                          />
                          <Badge variant={group.variant} size="xs">
                            {group.label}
                          </Badge>
                          <span className="min-w-0 flex-1 truncate font-mono" title={path}>
                            {path}
                          </span>
                        </button>
                      </CollapsibleTrigger>
                      <CollapsibleContent className="min-w-0 border-t bg-background p-3">
                        <DeploymentFileDiffPreview
                          pipelineId={pipelineId}
                          sourceVersion={status.version_id}
                          path={path}
                          diff={open ? diff : null}
                          loading={open && diffLoading}
                          error={open ? diffError : null}
                        />
                      </CollapsibleContent>
                    </Collapsible>
                  );
                })}
              </div>
            </section>
          ))}
        </div>
      )}
    </div>
  );
}

function DeploymentFileDiffPreview({
  pipelineId,
  sourceVersion,
  path,
  diff,
  loading,
  error,
}: {
  pipelineId: string;
  sourceVersion?: string;
  path: string;
  diff: DeploymentFileDiff | null;
  loading: boolean;
  error: string | null;
}) {
  if (loading && !diff) {
    return <Skeleton className="h-80 min-w-0" />;
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
    <div className="min-w-0 overflow-hidden rounded-md border" data-testid="deployment-file-diff">
      <div className="grid grid-cols-2 border-b bg-muted/30 text-xs font-medium">
        <div className="min-w-0 truncate border-r px-3 py-2">
          Current deployment{diff.before_exists ? "" : " · not present"}
        </div>
        <div className="min-w-0 truncate px-3 py-2">
          Saved workspace{diff.after_exists ? "" : " · not present"}
        </div>
      </div>
      <div className="h-80 min-w-0">
        <ReadOnlyRenderedOperationDiff
          original={diff.before_exists ? (diff.before ?? "") : ""}
          modified={diff.after_exists ? (diff.after ?? "") : ""}
          language={language}
          modelKey={modelPrefix}
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
