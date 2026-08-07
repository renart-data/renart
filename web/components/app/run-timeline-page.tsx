import { Link } from "@tanstack/react-router";
import { AlertTriangle, GanttChart, RefreshCw } from "lucide-react";
import { useMemo, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Empty,
  EmptyContent,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Skeleton } from "@/components/ui/skeleton";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { usePipelineRuns } from "@/hooks/use-pipeline-runs";
import type { PipelineRun } from "@/lib/types";
import { cn } from "@/lib/utils";

import { AppPage, AppPanel, PageHeader } from "./app-primitives";

const timelineRanges = [
  { value: "1h", label: "1 hour", milliseconds: 60 * 60 * 1000 },
  { value: "6h", label: "6 hours", milliseconds: 6 * 60 * 60 * 1000 },
  { value: "24h", label: "24 hours", milliseconds: 24 * 60 * 60 * 1000 },
  { value: "7d", label: "7 days", milliseconds: 7 * 24 * 60 * 60 * 1000 },
  { value: "30d", label: "30 days", milliseconds: 30 * 24 * 60 * 60 * 1000 },
] as const;

type TimelineRange = (typeof timelineRanges)[number]["value"];

export function AppRunTimelinePage() {
  const [range, setRange] = useState<TimelineRange>("24h");
  const runsQuery = useMemo(() => ({ limit: 500 }), []);
  const { runs, runsTotal, loading, runsError, refreshRuns } = usePipelineRuns({ runsQuery });
  const now = Date.now();
  const selectedRange = timelineRanges.find((item) => item.value === range) ?? timelineRanges[2];
  const windowStart = now - selectedRange.milliseconds;
  const visibleRuns = runs
    .filter((run) => {
      const start = run.started_at ? Date.parse(run.started_at) : Number.NaN;
      const end = run.finished_at ? Date.parse(run.finished_at) : now;
      return Number.isFinite(start) && Number.isFinite(end) && end >= windowStart && start <= now;
    })
    .sort((left, right) => Date.parse(right.started_at ?? "") - Date.parse(left.started_at ?? ""));
  const queuedRuns = runs.filter((run) => run.status === "queued").length;
  const ticks = timelineTicks(windowStart, now, selectedRange.milliseconds);

  return (
    <AppPage>
      <PageHeader
        title="Run timeline"
        subtitle="Actual pipeline execution windows from local run history"
        actions={
          <div className="flex items-center gap-2">
            {queuedRuns > 0 ? <Badge variant="outline">{queuedRuns} queued</Badge> : null}
            {runsTotal > runs.length ? (
              <Badge variant="muted">Showing latest {runs.length}</Badge>
            ) : null}
            <Button
              variant="outline"
              size="sm"
              disabled={loading}
              onClick={() => void refreshRuns()}
            >
              <RefreshCw
                data-icon="inline-start"
                className={loading ? "animate-spin" : undefined}
              />
              Refresh
            </Button>
          </div>
        }
      />
      {runsError ? (
        <div className="px-3 pb-2">
          <Alert variant="destructive">
            <AlertTriangle />
            <AlertTitle>Run activity could not be refreshed</AlertTitle>
            <AlertDescription>
              {runsError} Last successfully loaded activity remains visible.
            </AlertDescription>
          </Alert>
        </div>
      ) : null}
      <div className="flex shrink-0 items-center justify-end px-3 pb-2">
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          spacing={0}
          value={range}
          aria-label="Run timeline range"
          onValueChange={(value) => {
            if (timelineRanges.some((item) => item.value === value)) {
              setRange(value as TimelineRange);
            }
          }}
        >
          {timelineRanges.map((item) => (
            <ToggleGroupItem key={item.value} value={item.value} aria-label={`Show ${item.label}`}>
              {item.value}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </div>
      <div className="min-h-0 flex-1 px-3 pb-3">
        <AppPanel className="flex h-full min-h-0 flex-col overflow-hidden">
          {loading && runs.length === 0 ? (
            <div className="flex flex-col gap-3 p-3">
              {Array.from({ length: 5 }, (_, index) => (
                <Skeleton key={index} className="h-11 w-full" />
              ))}
            </div>
          ) : visibleRuns.length === 0 ? (
            <Empty>
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <GanttChart />
                </EmptyMedia>
                <EmptyTitle>No executions in this window</EmptyTitle>
                <EmptyDescription>
                  Runs appear here after they start. Queued runs remain visible on the Runs page.
                </EmptyDescription>
              </EmptyHeader>
              <EmptyContent>
                <Button asChild variant="outline" size="sm">
                  <Link to="/runs">Open run history</Link>
                </Button>
              </EmptyContent>
            </Empty>
          ) : (
            <ScrollArea className="min-h-0 flex-1" viewportClassName="h-full">
              <div className="min-w-[940px]">
                <div className="sticky top-0 z-10 grid grid-cols-[19rem_minmax(36rem,1fr)] border-b bg-card">
                  <div className="flex h-10 items-center px-3 text-xs font-medium">Run</div>
                  <div className="relative h-10 border-l">
                    {ticks.map((tick) => (
                      <div
                        key={tick.at}
                        className="absolute inset-y-0 border-l"
                        style={{ left: `${tick.left}%` }}
                      >
                        <span className="absolute left-1 top-1.5 whitespace-nowrap text-[10px] text-muted-foreground">
                          {tick.label}
                        </span>
                      </div>
                    ))}
                  </div>
                </div>
                {visibleRuns.map((run) => (
                  <RunTimelineRow
                    key={run.id}
                    run={run}
                    start={windowStart}
                    end={now}
                    ticks={ticks}
                  />
                ))}
              </div>
            </ScrollArea>
          )}
        </AppPanel>
      </div>
    </AppPage>
  );
}

function RunTimelineRow({
  run,
  start,
  end,
  ticks,
}: {
  run: PipelineRun;
  start: number;
  end: number;
  ticks: Array<{ at: number; left: number; label: string }>;
}) {
  const startedAt = Date.parse(run.started_at ?? "");
  const finishedAt = run.finished_at ? Date.parse(run.finished_at) : end;
  const clippedStart = Math.max(start, startedAt);
  const clippedEnd = Math.min(end, Math.max(startedAt, finishedAt));
  const span = Math.max(end - start, 1);
  const left = ((clippedStart - start) / span) * 100;
  const width = Math.max(((clippedEnd - clippedStart) / span) * 100, 0.45);

  return (
    <div className="grid min-h-12 grid-cols-[19rem_minmax(36rem,1fr)] border-b last:border-b-0 hover:bg-muted/30">
      <div className="flex min-w-0 items-center gap-2 px-3 py-1.5">
        <RunStatusBadge status={run.status} />
        <div className="min-w-0 flex-1">
          <Link
            to="/runs/$runId"
            params={{ runId: run.id }}
            className="block truncate font-mono text-xs font-medium hover:underline"
          >
            {run.pipeline}
          </Link>
          <p className="truncate text-[10px] text-muted-foreground">
            {run.environment || "default"} · {run.trigger} · {shortRunID(run.id)}
          </p>
        </div>
      </div>
      <div className="relative min-h-12 border-l">
        {ticks.map((tick) => (
          <div
            key={tick.at}
            aria-hidden="true"
            className="absolute inset-y-0 border-l"
            style={{ left: `${tick.left}%` }}
          />
        ))}
        <Tooltip>
          <TooltipTrigger asChild>
            <Link
              to="/runs/$runId"
              params={{ runId: run.id }}
              aria-label={`Open ${run.pipeline} run ${run.id}`}
              className={cn(
                "absolute top-1/2 h-4 -translate-y-1/2 rounded-sm ring-1 ring-background/60",
                run.status === "failed"
                  ? "bg-destructive"
                  : run.status === "cancelled"
                    ? "bg-muted-foreground"
                    : run.status === "running"
                      ? "animate-pulse bg-primary"
                      : "bg-primary",
              )}
              style={{ left: `${left}%`, width: `${Math.min(width, 100 - left)}%` }}
            />
          </TooltipTrigger>
          <TooltipContent className="flex flex-col gap-1">
            <span className="font-medium">{run.pipeline}</span>
            <span>{formatTimelineDate(run.started_at)}</span>
            <span>{formatRunDuration(startedAt, finishedAt, run.status)}</span>
          </TooltipContent>
        </Tooltip>
      </div>
    </div>
  );
}

function RunStatusBadge({ status }: { status: PipelineRun["status"] }) {
  const variant =
    status === "failed"
      ? "destructive"
      : status === "success"
        ? "secondary"
        : status === "running"
          ? "default"
          : "muted";
  return (
    <Badge variant={variant} size="xs" className="capitalize">
      {status}
    </Badge>
  );
}

function timelineTicks(start: number, end: number, duration: number) {
  return Array.from({ length: 5 }, (_, index) => {
    const left = index * 25;
    const at = start + ((end - start) * index) / 4;
    const options: Intl.DateTimeFormatOptions =
      duration > 24 * 60 * 60 * 1000
        ? { month: "short", day: "numeric", hour: "2-digit" }
        : { hour: "2-digit", minute: "2-digit" };
    return { at, left, label: new Intl.DateTimeFormat(undefined, options).format(new Date(at)) };
  });
}

function shortRunID(id: string) {
  return id.length > 12 ? `${id.slice(0, 12)}…` : id;
}

function formatTimelineDate(value?: string) {
  if (!value) return "Not started";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" }).format(
    date,
  );
}

function formatRunDuration(startedAt: number, finishedAt: number, status: PipelineRun["status"]) {
  const milliseconds = Math.max(finishedAt - startedAt, 0);
  const seconds = Math.round(milliseconds / 1000);
  const minutes = Math.floor(seconds / 60);
  const remainder = seconds % 60;
  const value = minutes > 0 ? `${minutes}m ${remainder}s` : `${remainder}s`;
  return status === "running" ? `${value} so far` : value;
}
