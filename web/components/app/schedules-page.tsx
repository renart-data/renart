import { Link } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import {
  AlertTriangle,
  ArchiveRestore,
  ChevronRight,
  CircleCheck,
  Clock,
  Loader2,
  MoreHorizontal,
  Package,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  Search,
} from "lucide-react";
import { type ReactNode, useEffect, useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Switch } from "@/components/ui/switch";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { envScheduleKey, useEnvSchedules } from "@/hooks/use-env-schedules";
import { formatSchedulerDate, usePipelineRuns } from "@/hooks/use-pipeline-runs";
import { usePipelineDeploy } from "@/hooks/use-pipeline-deploy";
import { activePipelineRunConflict } from "@/lib/api-scheduler";
import {
  triggerEnvSchedule,
  type CatchupPolicy,
  type EnvSchedule,
  type UpsertEnvScheduleInput,
} from "@/lib/api-env-schedules";
import { workspaceAtom } from "@/lib/atoms/domains/workspace";
import type { PipelineRun } from "@/lib/types";
import { deploymentLabel } from "@/lib/deployment-label";
import { cn } from "@/lib/utils";
import {
  scheduleExpectedSlots as expectedSlots,
  scheduleTimelineAxis as timelineAxis,
  scheduleTimelineBuckets as buckets,
  scheduleTimelineLeft as timelineLeft,
  scheduleTimelineWindow as timelineWindow,
  type ScheduleTimelineDensity as TimelineDensity,
  type ScheduleTimelineInput as TimelineSchedule,
  type ScheduleTimelineTick as TimelineTick,
  type ScheduleTimelineWindow as TimelineWindow,
} from "@/lib/schedule-timeline-model";

import { PipelinePlanSheet } from "./pipeline-plan-sheet";
import { AppContextSidebarFrame } from "./workbench/workbench-context-sidebar";
import { WorkbenchPortal, useWorkbench } from "./workbench/workbench-slots";

export function AppSchedulesPage({ initialQuery = "" }: { initialQuery?: string }) {
  const { runs, runsError, refreshRuns } = usePipelineRuns();
  const envSchedules = useEnvSchedules();
  const [query, setQuery] = useState(initialQuery);
  const [bucket, setBucket] = useState<(typeof buckets)[number]>("12hr");
  const [newScheduleOpen, setNewScheduleOpen] = useState(false);
  const [editingSchedule, setEditingSchedule] = useState<EnvSchedule | null>(null);
  const [deploymentReview, setDeploymentReview] = useState<{
    pipelineId: string;
    pipelineName: string;
    environment: string;
  } | null>(null);
  const tickDensity = useTimelineTickDensity();
  const window = timelineWindow(bucket, tickDensity);
  const axis = timelineAxis(window);
  const filteredSchedules = envSchedules.schedules.filter((schedule) => {
    const value = query.trim().toLowerCase();
    return (
      !value ||
      (schedule.pipeline_name ?? "").toLowerCase().includes(value) ||
      schedule.environment.toLowerCase().includes(value) ||
      schedule.cron.toLowerCase().includes(value)
    );
  });
  const schedulerRefreshError = runsError;
  const { mobileNavigationOpen, setMobileNavigationOpen } = useWorkbench();

  const openNewSchedule = () => {
    if (!mobileNavigationOpen) {
      setNewScheduleOpen(true);
      return;
    }
    setMobileNavigationOpen(false);
    setTimeout(() => setNewScheduleOpen(true), 220);
  };

  useEffect(() => {
    setQuery(initialQuery);
  }, [initialQuery]);

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <WorkbenchPortal slot="context">
        <AppContextSidebarFrame
          title="Schedules"
          subtitle={`${envSchedules.schedules.length} active binding${envSchedules.schedules.length === 1 ? "" : "s"}`}
          actions={
            <Button
              size="xs"
              disabled={!envSchedules.canMutate}
              title={!envSchedules.canMutate ? envSchedules.ownershipReason : undefined}
              aria-label="New schedule"
              onClick={openNewSchedule}
            >
              <Plus />
              New
            </Button>
          }
        >
          <div className="space-y-3 p-2">
            <div className="relative h-8 rounded-md border bg-background">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                aria-label="Filter schedules"
                className="h-full border-0 bg-transparent pl-8 text-xs shadow-none focus-visible:ring-0"
                placeholder="Filter schedules..."
                value={query}
                onChange={(event) => setQuery(event.target.value)}
              />
            </div>
            <div className="flex items-center gap-2 px-2 text-[10px] text-muted-foreground">
              {envSchedules.loading ? (
                <>
                  <Loader2 className="size-3 animate-spin" /> Loading schedules
                </>
              ) : envSchedules.canMutate ? (
                <>
                  <CircleCheck className="size-3 text-emerald-500" /> Scheduler active here
                </>
              ) : (
                <>
                  <AlertTriangle className="size-3 text-amber-500" /> Read-only here
                </>
              )}
            </div>
            <section>
              <p className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                Schedule bindings
              </p>
              <div className="space-y-0.5">
                {envSchedules.schedules.map((schedule) => {
                  const pipelineLabel = schedule.pipeline_name || schedule.pipeline_uuid;
                  return (
                    <button
                      key={envScheduleKey(schedule)}
                      type="button"
                      className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-muted"
                      onClick={() => {
                        setQuery(pipelineLabel);
                        setMobileNavigationOpen(false);
                      }}
                    >
                      <span
                        className={`size-2 shrink-0 rounded-full ${schedule.status === "active" ? "bg-emerald-500" : "bg-muted-foreground"}`}
                      />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate font-mono">{pipelineLabel}</span>
                        <span className="block truncate text-[9px] text-muted-foreground">
                          {schedule.environment} · {schedule.cron}
                        </span>
                      </span>
                      <ChevronRight className="size-3 shrink-0 text-muted-foreground" />
                    </button>
                  );
                })}
                {!envSchedules.loading && envSchedules.schedules.length === 0 ? (
                  <p className="px-2 py-3 text-xs text-muted-foreground">
                    No schedules configured.
                  </p>
                ) : null}
              </div>
            </section>
          </div>
        </AppContextSidebarFrame>
      </WorkbenchPortal>
      {!envSchedules.loading && !envSchedules.canMutate ? (
        <div className="p-2 pb-0">
          <Alert
            variant={envSchedules.ownership?.state === "unavailable" ? "destructive" : "default"}
          >
            <AlertTriangle />
            <AlertTitle>
              {envSchedules.ownership?.state === "follower"
                ? "Schedules are managed by another Renart process"
                : "Scheduler unavailable"}
            </AlertTitle>
            <AlertDescription>
              {envSchedules.ownershipReason} Existing schedules remain visible, but changes and runs
              are disabled here.
            </AlertDescription>
          </Alert>
        </div>
      ) : null}
      {schedulerRefreshError ? (
        <div className="p-2 pb-0">
          <Alert variant="destructive">
            <AlertTriangle />
            <AlertTitle>Scheduler activity could not be refreshed</AlertTitle>
            <AlertDescription className="flex items-center justify-between gap-3">
              <span>
                {schedulerRefreshError} Last successfully loaded activity remains visible.
              </span>
              <Button variant="outline" size="xs" onClick={() => void refreshRuns()}>
                <RefreshCw />
                Retry
              </Button>
            </AlertDescription>
          </Alert>
        </div>
      ) : null}
      <div className="flex h-10 shrink-0 items-center gap-2 border-b px-3">
        <div className="min-w-0 flex-1">
          <p className="truncate text-xs font-medium">
            {query ? `Filtered by ${query}` : "Schedule timeline"}
          </p>
          <p className="truncate text-[9px] text-muted-foreground">
            Desired bindings execute their pinned deployment
          </p>
        </div>
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          spacing={0}
          value={bucket}
          aria-label="Timeline range"
          className="ml-auto"
          onValueChange={(value) => {
            if (buckets.includes(value as (typeof buckets)[number])) {
              setBucket(value as (typeof buckets)[number]);
            }
          }}
        >
          {buckets.map((item) => (
            <ToggleGroupItem key={item} value={item} aria-label={`Show ${item} timeline`}>
              {item}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        <TooltipProvider>
          <div className="min-w-[1040px]">
            <div className="sticky top-0 z-10 grid h-9 grid-cols-[19rem_minmax(24rem,1fr)_16rem] items-center border-b bg-background text-[11px] font-semibold uppercase text-muted-foreground">
              <div className="px-3">Schedule</div>
              <TimelineAxis axis={axis} />
              <div className="px-3 text-right">Actions</div>
            </div>
            {envSchedules.loading && filteredSchedules.length === 0 ? (
              <div className="flex h-24 items-center gap-2 px-3 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                Loading schedules...
              </div>
            ) : null}
            {!envSchedules.loading && filteredSchedules.length === 0 ? (
              <div className="px-3 py-8 text-sm text-muted-foreground">
                No schedules yet. Use “New schedule” to run a pipeline in an environment.
              </div>
            ) : null}
            {filteredSchedules.map((schedule) => (
              <EnvScheduleRow
                key={envScheduleKey(schedule)}
                schedule={schedule}
                window={window}
                axis={axis}
                busy={envSchedules.busyKey === envScheduleKey(schedule)}
                canMutate={envSchedules.canMutate}
                ownershipReason={envSchedules.ownershipReason}
                activeRun={runs.find(
                  (run) =>
                    run.pipeline_id === schedule.pipeline_id &&
                    run.environment === schedule.environment &&
                    (run.status === "queued" || run.status === "running"),
                )}
                onSetStatus={(status) => envSchedules.setStatus(schedule, status)}
                onArchive={() => envSchedules.archive(schedule)}
                onEdit={() => setEditingSchedule(schedule)}
                onReviewDeployment={() => {
                  if (!schedule.pipeline_id) return;
                  setDeploymentReview({
                    pipelineId: schedule.pipeline_id,
                    pipelineName: schedule.pipeline_name || schedule.pipeline_uuid,
                    environment: schedule.environment,
                  });
                }}
              />
            ))}
            {envSchedules.archived.length > 0 ? (
              <ArchivedSection
                archived={envSchedules.archived}
                canMutate={envSchedules.canMutate}
                ownershipReason={envSchedules.ownershipReason}
                onRestore={(schedule) => void envSchedules.setStatus(schedule, "active")}
              />
            ) : null}
          </div>
        </TooltipProvider>
      </div>
      <NewEnvScheduleDialog
        open={newScheduleOpen}
        onOpenChange={setNewScheduleOpen}
        canMutate={envSchedules.canMutate}
        ownershipReason={envSchedules.ownershipReason}
        onCreate={async (pipeline, environment, input) => {
          await envSchedules.upsert(
            { pipeline_uuid: pipeline.uuid ?? "", environment, pipeline_id: pipeline.id },
            input,
          );
        }}
      />
      <EditEnvScheduleDialog
        schedule={editingSchedule}
        canMutate={envSchedules.canMutate}
        ownershipReason={envSchedules.ownershipReason}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setEditingSchedule(null);
        }}
        onSave={async (schedule, input) => {
          if (!schedule.pipeline_id) throw new Error("Pipeline details are unavailable.");
          await envSchedules.upsert(
            {
              pipeline_uuid: schedule.pipeline_uuid,
              environment: schedule.environment,
              pipeline_id: schedule.pipeline_id,
            },
            input,
          );
        }}
      />
      <ScheduleDeploymentReview
        target={deploymentReview}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setDeploymentReview(null);
        }}
        onSchedulesChanged={envSchedules.refresh}
      />
    </div>
  );
}

function EnvScheduleRow({
  schedule,
  window,
  axis,
  busy,
  canMutate,
  ownershipReason,
  activeRun,
  onSetStatus,
  onArchive,
  onEdit,
  onReviewDeployment,
}: {
  schedule: EnvSchedule;
  window: TimelineWindow;
  axis: TimelineTick[];
  busy: boolean;
  canMutate: boolean;
  ownershipReason: string;
  activeRun?: PipelineRun;
  onSetStatus: (status: "active" | "paused") => Promise<void>;
  onArchive: () => Promise<void>;
  onEdit: () => void;
  onReviewDeployment: () => void;
}) {
  const deployState = usePipelineDeploy(schedule.pipeline_id);
  const configuredEnabled = schedule.status === "active";
  const latestVersion = deployState.status?.version_id;
  const latestOrdinal = deployState.status?.ordinal;
  const pinnedVersion = schedule.snapshot_version_id?.trim() ?? "";
  const pinnedDeployment = deploymentLabel(schedule.snapshot_ordinal, pinnedVersion, "deployment");
  const overrideNames = [...(schedule.variable_names ?? [])].sort();
  const secretReferenceNames = [...(schedule.secret_reference_names ?? [])].sort();
  const deferredOccurrence = schedule.deferred_occurrence;
  const waitingForPrerequisites = deferredOccurrence?.status === "waiting_prerequisites";
  const deploymentOutdated = Boolean(
    latestVersion && pinnedVersion && latestVersion !== pinnedVersion,
  );
  const pinnedDeploymentCorrupt = Boolean(
    pinnedVersion &&
    latestVersion === pinnedVersion &&
    deployState.status?.has_snapshot &&
    !deployState.status.executable,
  );
  const [triggering, setTriggering] = useState(false);
  const [actionError, setActionError] = useState<{
    message: string;
    activeRunId?: string;
  } | null>(null);
  const sourceBlockReason = !pinnedVersion
    ? "This schedule needs an exact deployment pin before it can run"
    : pinnedDeploymentCorrupt
      ? `Pinned ${pinnedDeployment} failed its integrity check${deployState.status?.integrity_error ? `: ${deployState.status.integrity_error}` : ""}`
      : undefined;
  const runBlockReason = !canMutate ? ownershipReason : sourceBlockReason;
  const enabled = configuredEnabled && !sourceBlockReason;
  const timeline: TimelineSchedule = {
    schedule: schedule.cron,
    timezone: schedule.timezone,
    enabled,
    next_run_at: enabled ? schedule.next_run_at : undefined,
  };
  const slots = expectedSlots(timeline, window);
  const runBusy = busy || triggering || Boolean(activeRun);
  const runDisabled = runBusy || Boolean(runBlockReason);
  const runLabel =
    activeRun?.status === "running"
      ? "Running"
      : activeRun?.status === "queued"
        ? "Queued"
        : schedule.snapshot_ordinal
          ? `Run pinned #${schedule.snapshot_ordinal}`
          : "Run pinned";
  const pipelineLabel = schedule.pipeline_name || schedule.pipeline_uuid;
  const lastRunAt = schedule.last_run?.finished_at ?? schedule.last_run?.started_at;
  const lastRunLabel = schedule.last_run
    ? `${sentenceCase(schedule.last_run.status)} ${formatSchedulerDate(lastRunAt)}`
    : "Not run yet";
  const nowLeft = timelineLeft(Date.now(), window);
  const runWindowDescription = `Environment ${schedule.environment}. This action uses ${pinnedDeployment}${overrideNames.length > 0 ? ` with its stored overrides (${overrideNames.join(", ")})` : ""}. It sends no interval; when execution starts, the backend resolves the effective window from that deployment.`;
  const triggerNow = async () => {
    if (!schedule.pipeline_id || runBlockReason) return;
    setTriggering(true);
    setActionError(null);
    try {
      await triggerEnvSchedule(schedule.pipeline_id, schedule.environment);
    } catch (cause) {
      const conflict = activePipelineRunConflict(cause);
      setActionError({
        message: conflict
          ? "Another queued or running execution conflicts with this run."
          : cause instanceof Error
            ? cause.message
            : "Failed to queue the run.",
        activeRunId: conflict?.activeRunId,
      });
    } finally {
      setTriggering(false);
    }
  };
  const updateStatus = async (status: "active" | "paused") => {
    setActionError(null);
    try {
      await onSetStatus(status);
    } catch (cause) {
      setActionError({
        message: cause instanceof Error ? cause.message : "Failed to update the schedule.",
      });
    }
  };
  const archive = async () => {
    setActionError(null);
    try {
      await onArchive();
    } catch (cause) {
      setActionError({
        message: cause instanceof Error ? cause.message : "Failed to archive the schedule.",
      });
    }
  };
  return (
    <div
      className="grid min-h-[4.75rem] grid-cols-[19rem_minmax(24rem,1fr)_16rem] border-b hover:bg-muted/40"
      data-testid="schedule-row"
      data-pipeline={pipelineLabel}
      data-environment={schedule.environment}
    >
      <div className="flex min-w-0 items-start gap-2.5 px-3 py-2">
        <Switch
          className="mt-0.5"
          checked={configuredEnabled}
          disabled={!canMutate || busy || (!configuredEnabled && Boolean(sourceBlockReason))}
          title={!canMutate ? ownershipReason : undefined}
          aria-label={`${configuredEnabled ? "Pause" : "Resume"} ${pipelineLabel} in ${schedule.environment}`}
          onCheckedChange={(next) => void updateStatus(next ? "active" : "paused")}
        />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2 text-xs font-medium text-foreground">
            <Clock className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="truncate" title={pipelineLabel}>
              {pipelineLabel}
            </span>
            <Badge variant="secondary" size="xs">
              {schedule.environment}
            </Badge>
          </div>
          <dl
            className="mt-1 flex min-w-0 flex-wrap gap-x-2.5 gap-y-0.5 text-[10px] text-muted-foreground"
            data-testid="schedule-metadata"
          >
            <ScheduleMetadata label="" testId="schedule-cadence">
              <span className="break-all font-mono text-foreground">
                {schedule.cron} · {schedule.timezone || "UTC"}
              </span>
            </ScheduleMetadata>
            <ScheduleMetadata label="" testId="schedule-last-run">
              <span className="inline-flex items-center gap-1 break-words text-foreground">
                <span
                  className={cn(
                    "size-1.5 shrink-0 rounded-full bg-muted-foreground",
                    schedule.last_run?.status === "success" && "bg-emerald-500",
                    schedule.last_run?.status === "failed" && "bg-red-500",
                    schedule.last_run?.status === "running" && "bg-blue-500",
                  )}
                />
                {lastRunLabel}
              </span>
            </ScheduleMetadata>
            <ScheduleMetadata label="" testId="schedule-deployment">
              {pinnedVersion ? (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <span
                      className="inline-flex items-center gap-1 font-mono text-foreground"
                      tabIndex={0}
                    >
                      <Package className="size-3 shrink-0 text-muted-foreground" />
                      {deploymentLabel(schedule.snapshot_ordinal, pinnedVersion)}
                    </span>
                  </TooltipTrigger>
                  <TooltipContent>
                    Pinned {pinnedDeployment} ({pinnedVersion})
                  </TooltipContent>
                </Tooltip>
              ) : (
                <span className="text-foreground">Not pinned</span>
              )}
            </ScheduleMetadata>
          </dl>
          {sourceBlockReason ||
          deploymentOutdated ||
          overrideNames.length > 0 ||
          deferredOccurrence ? (
            <div className="mt-1.5 flex flex-wrap gap-1" data-testid="schedule-state-badges">
              {!pinnedVersion ? (
                <Badge variant="destructive" size="xs">
                  Needs deployment
                </Badge>
              ) : null}
              {overrideNames.length > 0 ? (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Badge variant="secondary" size="xs" tabIndex={0}>
                      Overrides
                    </Badge>
                  </TooltipTrigger>
                  <TooltipContent>
                    Applied from this schedule to its pinned deployment: {overrideNames.join(", ")}.
                    {secretReferenceNames.length > 0
                      ? ` Values for ${secretReferenceNames.join(", ")} are resolved from environment references only when planning or running.`
                      : ""}
                  </TooltipContent>
                </Tooltip>
              ) : null}
              {schedule.catchup_policy !== "skip" ? (
                <Badge variant="outline" size="xs" data-testid="schedule-run-window-context">
                  {catchupPolicyLabel(schedule.catchup_policy)}
                </Badge>
              ) : null}
              {deferredOccurrence ? (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Badge
                      variant="outline"
                      size="xs"
                      tabIndex={0}
                      aria-label={
                        waitingForPrerequisites
                          ? `Waiting for prerequisites. ${deferredOccurrence.prerequisite_reason ?? "A cross-pipeline producer is not ready."} The scheduled interval holds no run slot.`
                          : undefined
                      }
                    >
                      {waitingForPrerequisites ? <AlertTriangle /> : <Clock />}
                      {waitingForPrerequisites
                        ? "Waiting for prerequisites"
                        : deferredOccurrence.attempt_count > 0
                          ? "Retry waiting"
                          : "Run waiting"}
                    </Badge>
                  </TooltipTrigger>
                  <TooltipContent className="max-w-80">
                    {waitingForPrerequisites ? (
                      <div className="space-y-1">
                        <p>
                          {deferredOccurrence.prerequisite_reason ??
                            "A cross-pipeline producer is not ready."}
                        </p>
                        <p>
                          Scheduled interval{" "}
                          {formatSchedulerDate(deferredOccurrence.interval_start)} to{" "}
                          {formatSchedulerDate(deferredOccurrence.interval_end)} holds no run slot
                          and will be checked again after producer activity
                          {deferredOccurrence.prerequisite_deadline
                            ? ` until ${formatSchedulerDate(deferredOccurrence.prerequisite_deadline)}.`
                            : "."}
                        </p>
                      </div>
                    ) : (
                      <>
                        Scheduled interval {formatSchedulerDate(deferredOccurrence.interval_start)}{" "}
                        to {formatSchedulerDate(deferredOccurrence.interval_end)} is durably
                        retained and will be admitted when planning and the pipeline run slot are
                        available.
                      </>
                    )}
                  </TooltipContent>
                </Tooltip>
              ) : null}
              {deploymentOutdated ? (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Badge variant="destructive" size="xs" tabIndex={0}>
                      Older deployment
                    </Badge>
                  </TooltipTrigger>
                  <TooltipContent className="max-w-80">
                    This schedule runs {pinnedDeployment}. The latest is{" "}
                    {deploymentLabel(latestOrdinal, latestVersion, "deployment")}. Data freshness is
                    tracked separately.
                  </TooltipContent>
                </Tooltip>
              ) : null}
              {pinnedDeploymentCorrupt ? (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Badge variant="destructive" size="xs" tabIndex={0}>
                      Deployment needs repair
                    </Badge>
                  </TooltipTrigger>
                  <TooltipContent className="max-w-80">
                    {deployState.status?.integrity_error ??
                      "The pinned deployment failed its integrity check."}
                  </TooltipContent>
                </Tooltip>
              ) : null}
            </div>
          ) : null}
        </div>
      </div>
      <div
        className="relative min-h-[4.75rem] border-x bg-muted/20"
        data-testid="schedule-timeline"
      >
        <TimelineGrid axis={axis} />
        {slots.map((slot, index) => (
          <Tooltip key={`${slot.at}-${slot.kind}-${index}`}>
            <TooltipTrigger asChild>
              <span
                className={slotClassName(slot.kind, enabled, slot.phase)}
                style={{ left: `${slot.left}%`, width: `${slot.width}%` }}
                tabIndex={0}
                role="img"
                aria-label={`${slot.kind === "persisted" ? "Next scheduled run" : slot.phase === "past" ? "Past expected run" : "Expected run"} ${formatSchedulerDate(slot.at)}`}
              />
            </TooltipTrigger>
            <TooltipContent>
              <div className="font-medium">
                {slot.kind === "persisted"
                  ? "Next scheduled run"
                  : slot.phase === "past"
                    ? "Past expected run"
                    : "Expected run"}
              </div>
              <div className="font-mono">{formatSchedulerDate(slot.at)}</div>
              {slot.kind === "projected" ? (
                <div className="text-background/70">Projected from the schedule</div>
              ) : null}
            </TooltipContent>
          </Tooltip>
        ))}
        {nowLeft !== null ? <NowMarker left={nowLeft} /> : null}
      </div>
      <div
        className="flex min-w-0 flex-col items-end justify-center gap-1 px-2 py-2"
        data-testid="schedule-actions"
      >
        {actionError ? (
          <div
            className="flex min-w-0 items-center justify-end gap-1 text-right text-[11px] text-destructive"
            role="alert"
          >
            <span className="min-w-0 whitespace-normal">{actionError.message}</span>
            {actionError.activeRunId ? (
              <Button asChild variant="link" size="xs">
                <Link to="/runs/$runId" params={{ runId: actionError.activeRunId }}>
                  Open active run
                </Link>
              </Button>
            ) : null}
          </div>
        ) : null}
        <div className="flex items-center justify-end gap-1.5">
          {deploymentOutdated || !pinnedVersion || pinnedDeploymentCorrupt ? (
            <Button
              size="sm"
              variant="secondary"
              disabled={!canMutate || busy}
              title={
                !canMutate
                  ? ownershipReason
                  : "Review the saved pipeline, deploy it, then choose which schedule pins to update"
              }
              onClick={onReviewDeployment}
            >
              {busy ? (
                <Loader2 data-icon="inline-start" className="animate-spin" />
              ) : (
                <RefreshCw data-icon="inline-start" />
              )}
              {pinnedDeploymentCorrupt ? "Review repair" : "Review deployment"}
            </Button>
          ) : null}
          <Tooltip>
            <TooltipTrigger asChild>
              <span className="inline-flex" tabIndex={0}>
                <Button size="sm" disabled={runDisabled} onClick={() => void triggerNow()}>
                  {runBusy ? (
                    <Loader2 data-icon="inline-start" className="animate-spin" />
                  ) : (
                    <Play data-icon="inline-start" />
                  )}
                  {runLabel}
                </Button>
              </span>
            </TooltipTrigger>
            <TooltipContent className="max-w-80">
              {runBlockReason ?? runWindowDescription}
            </TooltipContent>
          </Tooltip>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                size="icon-sm"
                variant="ghost"
                disabled={!canMutate || busy}
                aria-label={`More actions for ${pipelineLabel} in ${schedule.environment}`}
                title={!canMutate ? ownershipReason : "More schedule actions"}
              >
                <MoreHorizontal />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-48">
              <DropdownMenuGroup>
                <DropdownMenuItem onSelect={onEdit}>
                  <Pencil />
                  Edit schedule
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => void archive()}>
                  <ArchiveRestore />
                  Archive schedule
                </DropdownMenuItem>
              </DropdownMenuGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>
    </div>
  );
}

function ScheduleMetadata({
  label,
  testId,
  children,
}: {
  label?: string;
  testId?: string;
  children: ReactNode;
}) {
  return (
    <div className="flex min-w-0 items-baseline gap-1 whitespace-normal" data-testid={testId}>
      {label ? (
        <dt className="shrink-0 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
          {label}
        </dt>
      ) : null}
      <dd className="min-w-0 whitespace-normal" data-schedule-meta-value>
        {children}
      </dd>
    </div>
  );
}

function catchupPolicyLabel(policy: CatchupPolicy) {
  switch (policy) {
    case "run_once":
      return "Run once after downtime";
    case "backfill":
      return "Backfill missed windows";
    default:
      return "Skip missed runs";
  }
}

function sentenceCase(value: string) {
  return value ? `${value.charAt(0).toUpperCase()}${value.slice(1).replaceAll("_", " ")}` : value;
}

function ArchivedSection({
  archived,
  canMutate,
  ownershipReason,
  onRestore,
}: {
  archived: EnvSchedule[];
  canMutate: boolean;
  ownershipReason: string;
  onRestore: (schedule: EnvSchedule) => void;
}) {
  return (
    <div>
      <div className="border-b bg-muted/40 px-3 py-1.5 text-[11px] font-semibold uppercase text-muted-foreground">
        Archived
      </div>
      {archived.map((schedule) => (
        <div
          key={envScheduleKey(schedule)}
          className="flex min-h-10 items-center gap-3 border-b px-3 text-xs text-muted-foreground"
        >
          <span className="truncate font-mono">
            {schedule.pipeline_name || schedule.pipeline_uuid}
          </span>
          <span className="rounded-full bg-muted px-1.5 py-0.5 text-[10px]">
            {schedule.environment}
          </span>
          <span className="truncate font-mono">{schedule.cron}</span>
          <span className="truncate">
            {schedule.archived_reason === "missing"
              ? "pipeline file missing (restores automatically when it reappears)"
              : schedule.archived_reason === "declaration_missing"
                ? "removed from .renart/schedules.yml (re-add or create it again)"
                : "archived"}
          </span>
          <span className="ml-auto" />
          {schedule.pipeline_id && schedule.archived_reason !== "declaration_missing" ? (
            <Button
              size="sm"
              variant="ghost"
              disabled={!canMutate}
              title={!canMutate ? ownershipReason : undefined}
              onClick={() => onRestore(schedule)}
            >
              <ArchiveRestore className="size-3.5" />
              Restore
            </Button>
          ) : null}
        </div>
      ))}
    </div>
  );
}

function ScheduleDeploymentReview({
  target,
  onOpenChange,
  onSchedulesChanged,
}: {
  target: { pipelineId: string; pipelineName: string; environment: string } | null;
  onOpenChange: (open: boolean) => void;
  onSchedulesChanged: () => void | Promise<void>;
}) {
  const deployState = usePipelineDeploy(target?.pipelineId);
  return (
    <PipelinePlanSheet
      open={Boolean(target)}
      onOpenChange={onOpenChange}
      pipelineId={target?.pipelineId ?? ""}
      pipelineName={target?.pipelineName ?? "Pipeline"}
      environment={target?.environment ?? ""}
      intent="deploy"
      onDeploy={(expectedSourceMerkle) => deployState.deploy(expectedSourceMerkle)}
      onSchedulesChanged={onSchedulesChanged}
    />
  );
}

function EditEnvScheduleDialog({
  schedule,
  canMutate,
  ownershipReason,
  onOpenChange,
  onSave,
}: {
  schedule: EnvSchedule | null;
  canMutate: boolean;
  ownershipReason: string;
  onOpenChange: (open: boolean) => void;
  onSave: (schedule: EnvSchedule, input: UpsertEnvScheduleInput) => Promise<void>;
}) {
  const [cron, setCron] = useState("");
  const [timezone, setTimezone] = useState("UTC");
  const [catchupPolicy, setCatchupPolicy] = useState<CatchupPolicy>("skip");
  const [paused, setPaused] = useState(false);
  const [overrideMode, setOverrideMode] = useState<"preserve" | "replace">("preserve");
  const [variableOverrides, setVariableOverrides] = useState("{}");
  const [secretReferences, setSecretReferences] = useState("{}");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!schedule) return;
    setCron(schedule.cron);
    setTimezone(schedule.timezone || "UTC");
    setCatchupPolicy(schedule.catchup_policy || "skip");
    setPaused(schedule.status !== "active");
    setOverrideMode("preserve");
    setVariableOverrides("{}");
    setSecretReferences("{}");
    setError(null);
  }, [schedule]);

  const submit = async () => {
    if (!schedule) return;
    if (!canMutate) {
      setError(ownershipReason);
      return;
    }
    if (!cron.trim()) {
      setError("Cron is required.");
      return;
    }

    let variableInput: Pick<UpsertEnvScheduleInput, "vars" | "secret_refs" | "preserve_variables">;
    try {
      if (overrideMode === "preserve") {
        variableInput = { preserve_variables: true };
      } else {
        const vars = parseVariableOverrides(variableOverrides);
        variableInput = {
          vars,
          secret_refs: parseSecretReferences(secretReferences, vars),
        };
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Variable overrides are invalid.");
      return;
    }

    setSubmitting(true);
    setError(null);
    try {
      await onSave(schedule, {
        cron: cron.trim(),
        timezone: timezone.trim() || "UTC",
        catchup_policy: catchupPolicy,
        paused,
        preserve_snapshot: true,
        ...variableInput,
      });
      onOpenChange(false);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Failed to save schedule.");
    } finally {
      setSubmitting(false);
    }
  };

  const pipelineLabel = schedule?.pipeline_name || schedule?.pipeline_uuid || "Pipeline";
  const storedNames = schedule?.variable_names ?? [];
  const secretNames = schedule?.secret_reference_names ?? [];

  return (
    <Dialog open={Boolean(schedule)} onOpenChange={onOpenChange}>
      <DialogContent className="grid max-h-[calc(100dvh-2rem)] grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Pencil className="size-4 text-primary" />
            Edit schedule
          </DialogTitle>
          <DialogDescription>
            Update the version-controlled declaration. The schedule keeps its current local
            deployment pin unless you explicitly redeploy it from the schedule row.
          </DialogDescription>
        </DialogHeader>
        <ScrollArea
          data-testid="edit-schedule-scroll-area"
          className="-mx-1 min-h-0 min-w-0 px-1"
          viewportClassName="overflow-x-hidden"
          showHorizontalScrollBar={false}
        >
          <FieldGroup className="min-w-0 max-w-full pb-1">
            <div className="grid min-w-0 gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="edit-schedule-pipeline">Pipeline</FieldLabel>
                <Input id="edit-schedule-pipeline" value={pipelineLabel} readOnly />
              </Field>
              <Field>
                <FieldLabel htmlFor="edit-schedule-environment">Environment</FieldLabel>
                <Input
                  id="edit-schedule-environment"
                  value={schedule?.environment ?? ""}
                  readOnly
                />
              </Field>
            </div>
            <div className="grid min-w-0 gap-4 sm:grid-cols-2">
              <Field data-invalid={!cron.trim()}>
                <FieldLabel htmlFor="edit-schedule-cron">Cron</FieldLabel>
                <Input
                  id="edit-schedule-cron"
                  className="font-mono"
                  value={cron}
                  onChange={(event) => setCron(event.target.value)}
                  aria-invalid={!cron.trim()}
                />
              </Field>
              <Field>
                <FieldLabel htmlFor="edit-schedule-timezone">Timezone</FieldLabel>
                <Input
                  id="edit-schedule-timezone"
                  value={timezone}
                  onChange={(event) => setTimezone(event.target.value)}
                  placeholder="UTC"
                />
              </Field>
            </div>
            <Field>
              <FieldLabel htmlFor="edit-schedule-catchup">Catch-up policy</FieldLabel>
              <Select
                value={catchupPolicy}
                onValueChange={(value) => setCatchupPolicy(value as CatchupPolicy)}
              >
                <SelectTrigger id="edit-schedule-catchup" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="skip">Skip missed intervals</SelectItem>
                  <SelectItem value="run_once">Run once to catch up</SelectItem>
                  <SelectItem value="backfill">
                    Backfill each missed interval (incremental assets only)
                  </SelectItem>
                </SelectContent>
              </Select>
            </Field>
            <Field orientation="horizontal">
              <div className="min-w-0 flex-1">
                <FieldLabel htmlFor="edit-schedule-paused">Pause schedule</FieldLabel>
                <FieldDescription>
                  Paused schedules keep their declaration and deployment pin but do not admit runs.
                </FieldDescription>
              </div>
              <Switch
                id="edit-schedule-paused"
                className="mr-3"
                checked={paused}
                onCheckedChange={setPaused}
              />
            </Field>
            <Field>
              <FieldLabel>Variable overrides</FieldLabel>
              <ToggleGroup
                type="single"
                variant="outline"
                spacing={0}
                value={overrideMode}
                onValueChange={(value) => {
                  if (value === "preserve" || value === "replace") setOverrideMode(value);
                }}
                className="grid min-w-0 w-full grid-cols-2"
                aria-label="Variable override behavior"
              >
                <ToggleGroupItem value="preserve" className="min-w-0 w-full whitespace-normal">
                  Keep stored overrides
                </ToggleGroupItem>
                <ToggleGroupItem value="replace" className="min-w-0 w-full whitespace-normal">
                  Replace or clear
                </ToggleGroupItem>
              </ToggleGroup>
              <FieldDescription className="break-words">
                {storedNames.length > 0
                  ? `Stored names: ${[...storedNames].sort().join(", ")}. Values stay private and are never loaded into this form.${
                      secretNames.length > 0
                        ? ` Secret-backed: ${[...secretNames].sort().join(", ")}.`
                        : ""
                    }`
                  : "This schedule currently has no stored overrides."}
              </FieldDescription>
            </Field>
            {overrideMode === "replace" ? (
              <>
                <Field>
                  <FieldLabel htmlFor="edit-schedule-vars">Literal overrides</FieldLabel>
                  <Textarea
                    id="edit-schedule-vars"
                    className="min-h-20 font-mono text-xs"
                    value={variableOverrides}
                    onChange={(event) => setVariableOverrides(event.target.value)}
                    spellCheck={false}
                  />
                  <FieldDescription>
                    Saving an empty object clears all stored literal overrides.
                  </FieldDescription>
                </Field>
                <Field>
                  <FieldLabel htmlFor="edit-schedule-secrets">Secret references</FieldLabel>
                  <Textarea
                    id="edit-schedule-secrets"
                    className="min-h-20 font-mono text-xs"
                    value={secretReferences}
                    onChange={(event) => setSecretReferences(event.target.value)}
                    spellCheck={false}
                  />
                  <FieldDescription>
                    Use env:NAME values. An empty object clears all stored references.
                  </FieldDescription>
                </Field>
              </>
            ) : null}
            <FieldError>{error}</FieldError>
          </FieldGroup>
        </ScrollArea>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
            Cancel
          </Button>
          <Button onClick={() => void submit()} disabled={submitting || !canMutate}>
            {submitting ? <Spinner data-icon="inline-start" /> : null}
            Save changes
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function parseVariableOverrides(value: string): Record<string, unknown> {
  let decoded: unknown;
  try {
    decoded = JSON.parse(value.trim() || "{}");
  } catch {
    throw new Error("Variable overrides must be valid JSON.");
  }
  if (!decoded || typeof decoded !== "object" || Array.isArray(decoded)) {
    throw new Error("Variable overrides must be a JSON object keyed by declared variable name.");
  }
  return decoded as Record<string, unknown>;
}

function parseSecretReferences(
  value: string,
  variables: Record<string, unknown>,
): Record<string, string> {
  let decoded: unknown;
  try {
    decoded = JSON.parse(value.trim() || "{}");
  } catch {
    throw new Error("Secret references must be valid JSON.");
  }
  if (!decoded || typeof decoded !== "object" || Array.isArray(decoded)) {
    throw new Error("Secret references must be a JSON object keyed by declared variable name.");
  }
  for (const [name, reference] of Object.entries(decoded as Record<string, unknown>)) {
    if (
      !name.trim() ||
      name.trim() !== name ||
      typeof reference !== "string" ||
      !/^env:[A-Za-z_][A-Za-z0-9_]*$/.test(reference)
    ) {
      throw new Error("Secret references must use declared variable names and env:NAME values.");
    }
    if (Object.prototype.hasOwnProperty.call(variables, name)) {
      throw new Error(
        `Variable ${name} cannot have both a literal override and a secret reference.`,
      );
    }
  }
  return decoded as Record<string, string>;
}

function NewEnvScheduleDialog({
  open,
  onOpenChange,
  canMutate,
  ownershipReason,
  onCreate,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  canMutate: boolean;
  ownershipReason: string;
  onCreate: (
    pipeline: { id: string; uuid?: string; name: string },
    environment: string,
    input: UpsertEnvScheduleInput,
  ) => Promise<void>;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const pipelines = useMemo(() => workspace?.pipelines ?? [], [workspace?.pipelines]);
  const [pipelineId, setPipelineId] = useState("");
  const [environment, setEnvironment] = useState("");
  const [cron, setCron] = useState("0 * * * *");
  const [timezone, setTimezone] = useState("UTC");
  const [catchupPolicy, setCatchupPolicy] = useState<CatchupPolicy>("skip");
  const [variableOverrides, setVariableOverrides] = useState("{}");
  const [secretReferences, setSecretReferences] = useState("{}");
  const deployState = usePipelineDeploy(pipelineId || undefined);
  const [sourceMode, setSourceMode] = useState<"existing" | "deploy">("deploy");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [pendingDeployment, setPendingDeployment] = useState<{
    pipeline: { id: string; uuid?: string; name: string };
    environment: string;
    input: {
      cron: string;
      timezone: string;
      catchup_policy: CatchupPolicy;
      vars?: Record<string, unknown>;
      secret_refs?: Record<string, string>;
    };
  } | null>(null);

  useEffect(() => {
    if (open) {
      setPipelineId(pipelines[0]?.id ?? "");
      setEnvironment(workspace?.selected_environment ?? "");
      setSourceMode("deploy");
      setVariableOverrides("{}");
      setSecretReferences("{}");
      setError(null);
      setPendingDeployment(null);
    }
  }, [open, pipelines, workspace?.selected_environment]);

  const submit = async () => {
    if (!canMutate) {
      setError(ownershipReason);
      return;
    }
    const pipeline = pipelines.find((item) => item.id === pipelineId);
    if (!pipeline || !environment.trim() || !cron.trim()) {
      setError(
        "Pipeline, environment, and cron are required — schedules have no implicit default environment.",
      );
      return;
    }
    const existingVersion = deployState.status?.version_id?.trim();
    if (sourceMode === "existing" && (!existingVersion || !deployState.status?.executable)) {
      setError(
        "Choose a valid deployment, or deploy the saved workspace when creating the schedule.",
      );
      return;
    }
    let vars: Record<string, unknown> | undefined;
    try {
      const decoded: unknown = JSON.parse(variableOverrides.trim() || "{}");
      if (!decoded || typeof decoded !== "object" || Array.isArray(decoded)) {
        setError("Variable overrides must be a JSON object keyed by declared variable name.");
        return;
      }
      if (Object.keys(decoded).length > 0) vars = decoded as Record<string, unknown>;
    } catch {
      setError("Variable overrides must be valid JSON.");
      return;
    }
    let secretRefs: Record<string, string> | undefined;
    try {
      const decoded: unknown = JSON.parse(secretReferences.trim() || "{}");
      if (!decoded || typeof decoded !== "object" || Array.isArray(decoded)) {
        setError("Secret references must be a JSON object keyed by declared variable name.");
        return;
      }
      const entries = Object.entries(decoded as Record<string, unknown>);
      for (const [name, reference] of entries) {
        if (
          !name.trim() ||
          name.trim() !== name ||
          typeof reference !== "string" ||
          !/^env:[A-Za-z_][A-Za-z0-9_]*$/.test(reference)
        ) {
          setError("Secret references must use declared variable names and env:NAME values.");
          return;
        }
        if (vars && Object.prototype.hasOwnProperty.call(vars, name)) {
          setError(`Variable ${name} cannot have both a literal override and a secret reference.`);
          return;
        }
      }
      if (entries.length > 0) secretRefs = decoded as Record<string, string>;
    } catch {
      setError("Secret references must be valid JSON.");
      return;
    }
    const selectedPipeline = { id: pipeline.id, uuid: pipeline.uuid, name: pipeline.name };
    const scheduleInput = {
      cron: cron.trim(),
      timezone: timezone.trim() || "UTC",
      catchup_policy: catchupPolicy,
      vars,
      secret_refs: secretRefs,
    };
    if (sourceMode === "deploy") {
      setPendingDeployment({
        pipeline: selectedPipeline,
        environment: environment.trim(),
        input: scheduleInput,
      });
      onOpenChange(false);
      return;
    }

    setSubmitting(true);
    setError(null);
    try {
      await onCreate(selectedPipeline, environment.trim(), {
        ...scheduleInput,
        snapshot_version_id: existingVersion!,
      });
      onOpenChange(false);
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : "Failed to save schedule.");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <>
      <Dialog open={open} onOpenChange={onOpenChange}>
        <DialogContent className="grid max-h-[calc(100dvh-2rem)] grid-rows-[auto_minmax(0,1fr)_auto] overflow-hidden sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <Clock className="size-4 text-primary" />
              New schedule
            </DialogTitle>
            <DialogDescription>
              Desired schedule settings are saved in .renart/schedules.yml. Each machine keeps its
              exact deployment pin and run history locally.
            </DialogDescription>
          </DialogHeader>
          <ScrollArea
            data-testid="new-schedule-scroll-area"
            className="-mx-1 min-h-0 min-w-0 px-1"
            viewportClassName="overflow-x-hidden"
            showHorizontalScrollBar={false}
          >
            <div className="min-w-0 max-w-full space-y-3 pb-1">
              <label className="block space-y-1.5">
                <span className="text-xs font-medium text-muted-foreground">Pipeline</span>
                <select
                  className="h-9 w-full rounded-md border bg-background px-2 text-sm"
                  value={pipelineId}
                  onChange={(event) => {
                    setPipelineId(event.target.value);
                    setSourceMode("deploy");
                  }}
                >
                  {pipelines.map((pipeline) => (
                    <option key={pipeline.id} value={pipeline.id}>
                      {pipeline.name || pipeline.path}
                    </option>
                  ))}
                </select>
              </label>
              <label className="block space-y-1.5">
                <span className="text-xs font-medium text-muted-foreground">Environment</span>
                <Input
                  value={environment}
                  onChange={(event) => setEnvironment(event.target.value)}
                  placeholder="prod"
                />
              </label>
              <div className="grid gap-3 sm:grid-cols-2">
                <label className="block space-y-1.5">
                  <span className="text-xs font-medium text-muted-foreground">Cron</span>
                  <Input
                    className="font-mono"
                    value={cron}
                    onChange={(event) => setCron(event.target.value)}
                    placeholder="0 * * * *"
                  />
                </label>
                <label className="block space-y-1.5">
                  <span className="text-xs font-medium text-muted-foreground">Timezone</span>
                  <Input
                    value={timezone}
                    onChange={(event) => setTimezone(event.target.value)}
                    placeholder="UTC"
                  />
                </label>
              </div>
              <label className="block space-y-1.5">
                <span className="text-xs font-medium text-muted-foreground">Catch-up policy</span>
                <select
                  className="h-9 w-full rounded-md border bg-background px-2 text-sm"
                  value={catchupPolicy}
                  onChange={(event) => setCatchupPolicy(event.target.value as CatchupPolicy)}
                >
                  <option value="skip">Skip missed intervals</option>
                  <option value="run_once">Run once to catch up</option>
                  <option value="backfill">
                    Backfill each missed interval (incremental assets only)
                  </option>
                </select>
              </label>
              <label className="block space-y-1.5">
                <span className="text-xs font-medium text-muted-foreground">
                  Variable overrides
                </span>
                <Textarea
                  className="min-h-20 font-mono text-xs"
                  value={variableOverrides}
                  onChange={(event) => setVariableOverrides(event.target.value)}
                  placeholder={'{"region":"eu","limit":100}'}
                  spellCheck={false}
                />
                <span className="block text-[11px] text-muted-foreground">
                  Optional JSON values are validated against the declarations in the pinned
                  deployment. Plans and schedule responses expose names and digests, not values.
                </span>
              </label>
              <label className="block space-y-1.5">
                <span className="text-xs font-medium text-muted-foreground">Secret references</span>
                <Textarea
                  className="min-h-20 font-mono text-xs"
                  value={secretReferences}
                  onChange={(event) => setSecretReferences(event.target.value)}
                  placeholder={'{"api_token":"env:RENART_API_TOKEN"}'}
                  spellCheck={false}
                />
                <span className="block text-[11px] text-muted-foreground">
                  Only env:NAME references are committed. Renart resolves their values from the
                  server process when planning and running; resolved values are never written to
                  schedule or run state.
                </span>
              </label>
              <div className="space-y-1.5">
                <span className="text-xs font-medium text-muted-foreground">Run source</span>
                <ToggleGroup
                  type="single"
                  variant="outline"
                  spacing={0}
                  value={sourceMode}
                  onValueChange={(value) => {
                    if (value === "existing" || value === "deploy") setSourceMode(value);
                  }}
                  className="grid w-full grid-cols-2"
                >
                  <ToggleGroupItem
                    value="existing"
                    className="w-full"
                    disabled={
                      deployState.loading ||
                      !deployState.status?.has_snapshot ||
                      !deployState.status.executable
                    }
                  >
                    {deployState.loading
                      ? "Checking deployment…"
                      : deployState.status?.has_snapshot && !deployState.status.executable
                        ? "Deployment needs repair"
                        : deployState.status?.version_id
                          ? `Use ${deploymentLabel(
                              deployState.status.ordinal,
                              deployState.status.version_id,
                            )}`
                          : "No deployment yet"}
                  </ToggleGroupItem>
                  <ToggleGroupItem value="deploy" className="w-full">
                    Review saved workspace
                  </ToggleGroupItem>
                </ToggleGroup>
                <p className="text-[11px] text-muted-foreground">
                  {sourceMode === "existing" && deployState.status?.version_id
                    ? `The schedule will stay pinned to ${deploymentLabel(
                        deployState.status.ordinal,
                        deployState.status.version_id,
                        "deployment",
                      )}.`
                    : "Review the saved workspace, create a deployment, and pin this schedule to that exact version."}
                </p>
              </div>
              {error ? <p className="text-xs text-red-600">{error}</p> : null}
            </div>
          </ScrollArea>
          <DialogFooter>
            <Button variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
              Cancel
            </Button>
            <Button onClick={() => void submit()} disabled={submitting || !canMutate}>
              {submitting
                ? "Saving…"
                : sourceMode === "deploy"
                  ? "Review & create"
                  : "Create schedule"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <PipelinePlanSheet
        open={Boolean(pendingDeployment)}
        onOpenChange={(nextOpen) => {
          if (!nextOpen) setPendingDeployment(null);
        }}
        pipelineId={pendingDeployment?.pipeline.id ?? ""}
        pipelineName={pendingDeployment?.pipeline.name ?? "Pipeline"}
        environment={pendingDeployment?.environment ?? ""}
        intent="deploy"
        onDeploy={async (expectedSourceMerkle) => {
          if (!pendingDeployment) throw new Error("Schedule details are unavailable.");
          const response = await deployState.deploy(expectedSourceMerkle);
          await onCreate(pendingDeployment.pipeline, pendingDeployment.environment, {
            ...pendingDeployment.input,
            snapshot_version_id: response.snapshot.version_id,
          });
          return response;
        }}
      />
    </>
  );
}

function useTimelineTickDensity() {
  const [density, setDensity] = useState<TimelineDensity>("regular");

  useEffect(() => {
    const media = window.matchMedia("(max-width: 1100px)");
    const update = () => setDensity(media.matches ? "compact" : "regular");
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);

  return density;
}

function TimelineAxis({ axis }: { axis: TimelineTick[] }) {
  return (
    <div className="relative h-full flex-1 border-x">
      {axis.map((tick) => (
        <span
          key={tick.key}
          className="absolute top-1/2 -translate-x-1/2 -translate-y-1/2 whitespace-nowrap px-1 text-center"
          style={{ left: `${tick.left}%` }}
        >
          {tick.label}
        </span>
      ))}
    </div>
  );
}

function TimelineGrid({ axis }: { axis: TimelineTick[] }) {
  return (
    <>
      {axis.map((tick) => (
        <span
          key={tick.key}
          className="absolute inset-y-0 w-px bg-border/60"
          style={{ left: `${tick.left}%` }}
        />
      ))}
    </>
  );
}

function NowMarker({ left }: { left: number }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="absolute inset-y-1 z-10 w-px bg-foreground" style={{ left: `${left}%` }}>
          <span className="absolute -top-0.5 left-1/2 size-1.5 -translate-x-1/2 rounded-full bg-foreground" />
        </span>
      </TooltipTrigger>
      <TooltipContent>
        <div className="font-medium">Now</div>
        <div className="font-mono">{formatSchedulerDate(new Date().toISOString())}</div>
      </TooltipContent>
    </Tooltip>
  );
}

function slotClassName(
  kind: "persisted" | "projected",
  enabled: boolean,
  phase: "past" | "future",
) {
  if (!enabled) {
    return kind === "persisted"
      ? "absolute top-1/2 h-10 -translate-y-1/2 rounded-sm bg-muted-foreground/35"
      : "absolute top-1/2 h-8 -translate-y-1/2 rounded-sm border border-muted-foreground/25 bg-muted-foreground/10";
  }
  return kind === "persisted"
    ? "absolute top-1/2 h-10 -translate-y-1/2 rounded-sm bg-primary"
    : phase === "past"
      ? "absolute top-1/2 h-8 -translate-y-1/2 rounded-sm border border-amber-500/45 bg-amber-500/15"
      : "absolute top-1/2 h-8 -translate-y-1/2 rounded-sm border border-primary/40 bg-primary/15";
}
