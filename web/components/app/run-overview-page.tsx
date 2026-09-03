import { Link } from "@tanstack/react-router";
import {
  Activity,
  AlertTriangle,
  CalendarClock,
  CheckCircle2,
  ChevronRight,
  Circle,
  Clock3,
  Loader2,
  PackageCheck,
  RefreshCw,
  Rocket,
  ShieldAlert,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import {
  type RunOverviewIssue,
  type RunOverviewTimelineRow,
  useRunOverviewModel,
} from "@/hooks/use-run-overview-model";
import {
  scheduleTimelineBuckets,
  scheduleTimelineLeft,
  type ScheduleTimelineBucket,
  type ScheduleTimelineWindow,
} from "@/lib/schedule-timeline-model";
import type { PipelineRun } from "@/lib/types";
import { cn } from "@/lib/utils";

import { AppContextSidebarFrame } from "./workbench/workbench-context-sidebar";
import { WorkbenchPortal, useWorkbench } from "./workbench/workbench-slots";

export type AppRunOverviewSearch = {
  pipeline?: string;
  range?: ScheduleTimelineBucket;
};

export function normalizeAppRunOverviewSearch(
  search: Record<string, unknown>,
): AppRunOverviewSearch {
  return {
    pipeline: typeof search.pipeline === "string" && search.pipeline ? search.pipeline : undefined,
    range: scheduleTimelineBuckets.includes(search.range as ScheduleTimelineBucket)
      ? (search.range as ScheduleTimelineBucket)
      : undefined,
  };
}

export function AppRunOverviewPage({
  search = {},
  onSearchChange,
}: {
  search?: AppRunOverviewSearch;
  onSearchChange?: (search: AppRunOverviewSearch) => void;
}) {
  const density = useOverviewTimelineDensity();
  const range = search.range ?? "24hr";
  const { model, loading, error, schedulerOwnership, refresh } = useRunOverviewModel({
    pipelineId: search.pipeline,
    bucket: range,
    density,
  });
  const { setMobileNavigationOpen } = useWorkbench();
  const selectPipeline = (pipeline?: string) => {
    onSearchChange?.({ ...search, pipeline });
    setMobileNavigationOpen(false);
  };

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <WorkbenchPortal slot="context">
        <AppContextSidebarFrame
          title="Run overview"
          subtitle={`${model.environment} · ${model.pipelines.length} pipelines`}
          actions={
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Refresh run overview"
              disabled={loading}
              onClick={() => void refresh()}
            >
              <RefreshCw className={loading ? "animate-spin" : undefined} />
            </Button>
          }
        >
          <div className="space-y-4 p-2">
            <section>
              <p className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                Pipelines
              </p>
              <div className="space-y-0.5">
                <button
                  type="button"
                  className={cn(
                    "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-muted",
                    !model.selectedPipelineId && "bg-primary/10 text-primary",
                  )}
                  onClick={() => selectPipeline(undefined)}
                >
                  <Activity className="size-3.5 shrink-0" />
                  <span className="min-w-0 flex-1 truncate">All pipelines</span>
                  <span className="font-mono text-[10px] text-muted-foreground">
                    {model.pipelines.length}
                  </span>
                </button>
                {model.pipelines.map((pipeline) => (
                  <button
                    key={pipeline.id}
                    type="button"
                    className={cn(
                      "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-muted",
                      model.selectedPipelineId === pipeline.id && "bg-primary/10 text-primary",
                    )}
                    onClick={() => selectPipeline(pipeline.id)}
                  >
                    <span
                      className={cn(
                        "size-2 shrink-0 rounded-full bg-muted-foreground",
                        pipeline.health === "healthy" && "bg-emerald-500",
                        pipeline.health === "running" && "bg-blue-500",
                        pipeline.health === "waiting" && "bg-amber-500",
                        pipeline.health === "failed" && "bg-red-500",
                      )}
                    />
                    <span className="min-w-0 flex-1 truncate font-mono">{pipeline.name}</span>
                    <span className="truncate text-[9px] text-muted-foreground">
                      {pipeline.healthLabel}
                    </span>
                  </button>
                ))}
              </div>
            </section>
            <section>
              <p className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                Active context
              </p>
              <dl className="divide-y rounded-lg border text-xs">
                <ContextDatum label="Environment" value={model.environment} />
                <ContextDatum
                  label="Scheduler"
                  value={
                    schedulerOwnership?.state === "owner"
                      ? "Active here"
                      : schedulerOwnership?.state === "follower"
                        ? "Remote owner"
                        : "Unavailable"
                  }
                />
                <ContextDatum label="Scope" value={model.selectedPipeline?.name ?? "Workspace"} />
              </dl>
            </section>
          </div>
        </AppContextSidebarFrame>
      </WorkbenchPortal>

      {error ? (
        <div className="p-2 pb-0">
          <Alert variant="destructive">
            <AlertTriangle />
            <AlertTitle>Run overview is partially unavailable</AlertTitle>
            <AlertDescription>
              {error} Last successfully loaded operational data remains visible.
            </AlertDescription>
          </Alert>
        </div>
      ) : null}

      <ConnectedReadoutStrip readouts={model.readouts} loading={loading} />

      <div className="flex h-10 shrink-0 items-center gap-2 border-b px-3">
        <div className="min-w-0 flex-1">
          <p className="truncate text-xs font-medium">Projected and actual runs</p>
          <p className="truncate text-[10px] text-muted-foreground">
            Duration is encoded on the shared time axis
          </p>
        </div>
        <TimelineLegend />
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          spacing={0}
          value={range}
          aria-label="Run overview range"
          onValueChange={(value) => {
            if (scheduleTimelineBuckets.includes(value as ScheduleTimelineBucket)) {
              onSearchChange?.({ ...search, range: value as ScheduleTimelineBucket });
            }
          }}
        >
          {scheduleTimelineBuckets.map((item) => (
            <ToggleGroupItem key={item} value={item} aria-label={`Show ${item} overview`}>
              {item}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </div>

      <div className="min-h-0 flex-1 overflow-auto p-2">
        <RunOverviewTimeline rows={model.timelineRows} window={model.timelineWindow} />
        <div className="mt-2 grid overflow-hidden rounded-lg border bg-border lg:grid-cols-2 lg:gap-px">
          <OverviewIssueSection title="Needs attention" issues={model.attention} />
          <OverviewIssueSection title="Deployment readiness" issues={model.readiness} />
        </div>
      </div>
    </div>
  );
}

function ContextDatum({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center gap-3 px-2 py-2">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0 flex-1 truncate text-right font-medium">{value}</dd>
    </div>
  );
}

const readoutIcons = [Rocket, Clock3, Activity, PackageCheck] as const;

function ConnectedReadoutStrip({
  readouts,
  loading,
}: {
  readouts: Array<{ label: string; value: string; detail: string }>;
  loading: boolean;
}) {
  return (
    <div className="grid shrink-0 grid-cols-2 divide-x divide-y border-b bg-muted/10 md:grid-cols-4 md:divide-y-0">
      {readouts.map((readout, index) => {
        const Icon = readoutIcons[index] ?? Circle;
        return (
          <div key={readout.label} className="flex min-w-0 items-start gap-2 px-3 py-2.5">
            <Icon className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
            <div className="min-w-0 flex-1">
              <p className="truncate text-[9px] font-semibold uppercase tracking-wide text-muted-foreground">
                {readout.label}
              </p>
              <p className="truncate text-sm font-semibold">{readout.value}</p>
              <p className="truncate text-[9px] text-muted-foreground">{readout.detail}</p>
            </div>
            {loading && index === 0 ? (
              <Loader2 aria-label="Loading run overview" className="size-3 animate-spin" />
            ) : null}
          </div>
        );
      })}
    </div>
  );
}

function TimelineLegend() {
  return (
    <div className="hidden items-center gap-2 text-[9px] text-muted-foreground xl:flex">
      <span className="flex items-center gap-1">
        <span className="h-3 w-2 rounded-sm border border-dashed border-primary bg-primary/5" />
        Projected
      </span>
      <span className="flex items-center gap-1">
        <span className="h-2.5 w-4 rounded-sm border border-emerald-700 bg-emerald-500" />
        Succeeded
      </span>
      <span className="flex items-center gap-1">
        <span className="h-2.5 w-4 rounded-sm border border-red-800 bg-red-500" />
        Failed
      </span>
    </div>
  );
}

function RunOverviewTimeline({
  rows,
  window,
}: {
  rows: RunOverviewTimelineRow[];
  window: ScheduleTimelineWindow;
}) {
  if (rows.length === 0) {
    return (
      <div className="flex min-h-40 items-center justify-center rounded-lg border text-sm text-muted-foreground">
        No projected or actual runs in this time range.
      </div>
    );
  }
  return (
    <div className="overflow-auto rounded-lg border" data-testid="run-overview-timeline">
      <div className="min-w-[900px]">
        <div className="sticky top-0 z-20 grid h-9 grid-cols-[15rem_minmax(40rem,1fr)] border-b bg-background text-[10px] font-medium text-muted-foreground">
          <div className="flex items-center px-3">Pipeline</div>
          <div className="relative border-l">
            {timelineHeaderTicks(window).map((tick) => (
              <span
                key={tick.at}
                className="absolute top-1/2 -translate-x-1/2 -translate-y-1/2 whitespace-nowrap px-1"
                style={{ left: `${tick.left}%` }}
              >
                {tick.label}
              </span>
            ))}
          </div>
        </div>
        {rows.map((row) => (
          <OverviewTimelineRow key={row.pipeline.id} row={row} window={window} />
        ))}
      </div>
    </div>
  );
}

function OverviewTimelineRow({
  row,
  window,
}: {
  row: RunOverviewTimelineRow;
  window: ScheduleTimelineWindow;
}) {
  const positionedRuns = useMemo(() => positionRuns(row.runs, window), [row.runs, window]);
  const laneCount = Math.max(1, ...positionedRuns.map((item) => item.lane + 1));
  const rowHeight = Math.max(54, 34 + laneCount * 16);
  const nowLeft = scheduleTimelineLeft(Date.now(), window);
  return (
    <div
      className="grid grid-cols-[15rem_minmax(40rem,1fr)] border-b last:border-b-0 hover:bg-muted/20"
      style={{ minHeight: rowHeight }}
    >
      <div className="flex min-w-0 items-center gap-2 px-3 py-2">
        <Activity className="size-3.5 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <p className="truncate font-mono text-xs font-medium">{row.pipeline.name}</p>
          <p className="truncate text-[9px] text-muted-foreground">{row.cadence}</p>
        </div>
        <span className="text-[9px] text-muted-foreground">{row.runs.length} runs</span>
      </div>
      <div className="relative overflow-hidden border-l" style={{ height: rowHeight }}>
        {timelineHeaderTicks(window).map((tick) => (
          <span
            key={tick.at}
            aria-hidden="true"
            className="absolute inset-y-0 w-px bg-border/65"
            style={{ left: `${tick.left}%` }}
          />
        ))}
        {row.projections.map((projection, index) => (
          <ProjectedRunMarker
            key={`${projection.schedule.pipeline_uuid}:${projection.schedule.environment}:${projection.at}:${index}`}
            projection={projection}
          />
        ))}
        {positionedRuns.map((positioned) => (
          <ActualRunBar key={positioned.run.id} positioned={positioned} />
        ))}
        {nowLeft !== null ? (
          <span
            aria-hidden="true"
            className="absolute inset-y-0 z-10 w-px bg-foreground/60"
            style={{ left: `${nowLeft}%` }}
          />
        ) : null}
      </div>
    </div>
  );
}

function ProjectedRunMarker({
  projection,
}: {
  projection: RunOverviewTimelineRow["projections"][number];
}) {
  const pipelineLabel = projection.schedule.pipeline_name || projection.schedule.pipeline_uuid;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Link
          to="/schedules"
          search={{ pipeline: pipelineLabel }}
          aria-label={`Open projected ${pipelineLabel} run at ${formatTimelineDate(projection.at)}`}
          className={cn(
            "absolute top-2 z-[5] h-5 min-w-1 rounded-sm border border-dashed bg-background",
            projection.kind === "persisted"
              ? "border-primary bg-primary/10"
              : "border-primary/50 bg-primary/5",
            projection.phase === "past" && "border-amber-600 bg-amber-500/10",
          )}
          style={{ left: `${projection.left}%`, width: `${projection.width}%` }}
        />
      </TooltipTrigger>
      <TooltipContent>
        <p className="font-medium">Projected {pipelineLabel} run</p>
        <p>{formatTimelineDate(projection.at)}</p>
        <p className="text-background/70">
          {projection.kind === "persisted" ? "Scheduler next occurrence" : "Calculated from cron"}
        </p>
      </TooltipContent>
    </Tooltip>
  );
}

type PositionedRun = {
  run: PipelineRun;
  lane: number;
  left: number;
  width: number;
  startedAt: number;
  finishedAt: number;
};

function ActualRunBar({ positioned }: { positioned: PositionedRun }) {
  const { run, lane, left, width, startedAt, finishedAt } = positioned;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Link
          to="/runs/$runId"
          params={{ runId: run.id }}
          aria-label={`Open ${run.status} ${run.pipeline} run lasting ${formatDuration(finishedAt - startedAt)}`}
          data-run-status={run.status}
          className={cn(
            "absolute z-[6] h-3.5 overflow-hidden rounded-sm border shadow-sm",
            run.status === "success" && "border-emerald-700 bg-emerald-500",
            run.status === "failed" && "border-red-800 bg-red-500",
            run.status === "running" && "border-blue-800 bg-blue-500",
            run.status === "queued" && "border-dashed border-amber-700 bg-amber-300",
            run.status === "cancelled" && "border-zinc-700 bg-zinc-400",
          )}
          style={{
            left: `${left}%`,
            top: 30 + lane * 16,
            width: `max(${width}%, 12px)`,
          }}
        >
          <span className="sr-only">{run.status}</span>
        </Link>
      </TooltipTrigger>
      <TooltipContent className="space-y-0.5">
        <p className="font-medium">
          {run.pipeline} · <span className="capitalize">{run.status}</span>
        </p>
        <p>{formatTimelineDate(run.started_at)}</p>
        <p>
          {formatDuration(finishedAt - startedAt)} · {run.trigger}
        </p>
      </TooltipContent>
    </Tooltip>
  );
}

function OverviewIssueSection({ title, issues }: { title: string; issues: RunOverviewIssue[] }) {
  return (
    <section className="min-w-0 bg-background">
      <div className="flex h-9 items-center gap-2 border-b px-3">
        <h2 className="text-xs font-semibold">{title}</h2>
        {issues.length > 0 ? (
          <Badge
            variant={
              issues.some((issue) => issue.tone === "destructive") ? "destructive" : "outline"
            }
          >
            {issues.length}
          </Badge>
        ) : null}
      </div>
      <div className="divide-y">
        {issues.length === 0 ? (
          <div className="flex min-h-14 items-center gap-2 px-3 text-xs text-muted-foreground">
            <CheckCircle2 className="size-4 text-emerald-500" />
            Nothing needs attention in this scope.
          </div>
        ) : (
          issues.map((issue) => (
            <IssueRow key={issue.id} issue={issue} readiness={title === "Deployment readiness"} />
          ))
        )}
      </div>
    </section>
  );
}

function IssueRow({ issue, readiness }: { issue: RunOverviewIssue; readiness: boolean }) {
  const Icon =
    issue.tone === "destructive"
      ? ShieldAlert
      : issue.tone === "warning"
        ? AlertTriangle
        : CalendarClock;
  const content = (
    <>
      <Icon
        className={cn(
          "mt-0.5 size-3.5 shrink-0 text-muted-foreground",
          issue.tone === "destructive" && "text-red-500",
          issue.tone === "warning" && "text-amber-500",
        )}
      />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-xs font-medium">{issue.title}</span>
        <span className="block truncate text-[9px] text-muted-foreground">{issue.detail}</span>
      </span>
      <ChevronRight className="size-3.5 shrink-0 text-muted-foreground" />
    </>
  );
  if (issue.runId) {
    return (
      <Button
        asChild
        variant="ghost"
        className="h-auto w-full justify-start rounded-none px-3 py-2"
      >
        <Link to="/runs/$runId" params={{ runId: issue.runId }}>
          {content}
        </Link>
      </Button>
    );
  }
  return (
    <Button asChild variant="ghost" className="h-auto w-full justify-start rounded-none px-3 py-2">
      <Link
        to={readiness ? "/schedules/deployments" : "/schedules"}
        search={readiness ? undefined : { pipeline: issue.pipelineId }}
      >
        {content}
      </Link>
    </Button>
  );
}

function positionRuns(runs: PipelineRun[], window: ScheduleTimelineWindow): PositionedRun[] {
  const laneEnds: number[] = [];
  return runs
    .map((run) => {
      const startedAt = Date.parse(run.started_at ?? "");
      const finishedAt = run.finished_at ? Date.parse(run.finished_at) : Date.now();
      return { run, startedAt, finishedAt: Math.max(finishedAt, startedAt) };
    })
    .filter((item) => Number.isFinite(item.startedAt) && Number.isFinite(item.finishedAt))
    .sort(
      (left, right) => left.startedAt - right.startedAt || left.run.id.localeCompare(right.run.id),
    )
    .map(({ run, startedAt, finishedAt }) => {
      const clippedStart = Math.max(window.start, startedAt);
      const clippedEnd = Math.min(window.end, finishedAt);
      const span = Math.max(window.end - window.start, 1);
      const left = ((clippedStart - window.start) / span) * 100;
      const width = Math.max(((clippedEnd - clippedStart) / span) * 100, 0);
      let lane = laneEnds.findIndex((laneEnd) => startedAt >= laneEnd);
      if (lane === -1) lane = laneEnds.length;
      laneEnds[lane] = finishedAt;
      return { run, lane, left, width, startedAt, finishedAt };
    });
}

function timelineHeaderTicks(window: ScheduleTimelineWindow) {
  const formatter = new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" });
  return Array.from({ length: 7 }, (_, index) => {
    const left = (index / 6) * 100;
    const at = window.start + ((window.end - window.start) * index) / 6;
    return { at, left, label: formatter.format(new Date(at)) };
  });
}

function formatDuration(milliseconds: number) {
  const seconds = Math.max(0, Math.round(milliseconds / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  const remainder = seconds % 60;
  return `${minutes}m ${remainder}s`;
}

function formatTimelineDate(value?: string) {
  if (!value) return "Time unavailable";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(
    date,
  );
}

function useOverviewTimelineDensity() {
  const [density, setDensity] = useState<"compact" | "regular">("regular");
  useEffect(() => {
    const media = window.matchMedia("(max-width: 1100px)");
    const update = () => setDensity(media.matches ? "compact" : "regular");
    update();
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, []);
  return density;
}
