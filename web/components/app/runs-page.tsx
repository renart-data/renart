import { Link, useNavigate } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import {
  ArrowLeft,
  AlertTriangle,
  CircleStop,
  ChevronLeft,
  ChevronRight,
  ListTree,
  Loader2,
  Play,
  RotateCw,
  Search,
  Terminal,
  X,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import { AnsiOutput } from "@/components/ansi-output";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { formatSchedulerDate, usePipelineRuns } from "@/hooks/use-pipeline-runs";
import { useFollowOutputScroll } from "@/hooks/use-follow-output-scroll";
import { workspaceAtom } from "@/lib/atoms/domains/workspace";
import { activePipelineRunConflict, type PipelineRunSource } from "@/lib/api-scheduler";
import type {
  PipelineRun,
  PipelineRunLogLine,
  PipelineRunPlan,
  PipelineRunStep,
  PipelineRunUnit,
} from "@/lib/types";
import { cn } from "@/lib/utils";
import { awaitWorkspaceSaves } from "@/lib/workspace-save-barrier";
import { deploymentLabel } from "@/lib/deployment-label";

import { PageHeader, AppPage, AppPanel, SimpleTable, StatusPill } from "./app-primitives";
import { AppContextSidebarFrame } from "./workbench/workbench-context-sidebar";
import { WorkbenchPortal, useWorkbench } from "./workbench/workbench-slots";

const runTabsTriggerClass = "flex-none";
const runStatuses = ["all", "queued", "running", "success", "failed", "cancelled"] as const;
const pageSize = 8;

type RunScrollRequest = {
  asset: string;
  target: "events" | "timeline";
  sequence: number;
};

export type AppRunsSearch = {
  q?: string;
  status?: (typeof runStatuses)[number];
  page?: number;
};

export function normalizeAppRunsSearch(search: Record<string, unknown>): AppRunsSearch {
  const rawPage =
    typeof search.page === "number"
      ? search.page
      : typeof search.page === "string"
        ? Number(search.page)
        : undefined;
  const page = rawPage && Number.isFinite(rawPage) && rawPage > 0 ? Math.floor(rawPage) : undefined;
  return {
    q: typeof search.q === "string" && search.q.trim() ? search.q : undefined,
    status: runStatuses.includes(search.status as never)
      ? (search.status as AppRunsSearch["status"])
      : undefined,
    page,
  };
}

export function AppRunsPage({
  search = {},
  onSearchChange,
}: {
  search?: AppRunsSearch;
  onSearchChange?: (search: AppRunsSearch) => void;
}) {
  const q = search.q ?? "";
  const status = search.status ?? "all";
  const requestedPage = search.page ?? 1;
  const runsQuery = useMemo(
    () => ({
      limit: pageSize,
      offset: (requestedPage - 1) * pageSize,
      q: q.trim() || undefined,
      status: status === "all" ? undefined : status,
    }),
    [q, requestedPage, status],
  );
  const { runs, loading, runsTotal, runsOffset, runsError, refreshRuns } = usePipelineRuns({
    runsQuery,
  });
  const pages = Math.max(1, Math.ceil(runsTotal / pageSize));
  const page = Math.min(requestedPage, pages);
  const visibleRuns = runs;
  const updateSearch = (next: AppRunsSearch) => onSearchChange?.({ ...search, ...next });
  const { setMobileNavigationOpen } = useWorkbench();

  useEffect(() => {
    if (requestedPage > pages) {
      updateSearch({ page: pages });
    }
  }, [pages, requestedPage]);

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <WorkbenchPortal slot="context">
        <RunNavigationSidebar
          runs={visibleRuns}
          total={runsTotal}
          loading={loading}
          q={q}
          status={status}
          search={search}
          onSearchChange={updateSearch}
          onNavigate={() => setMobileNavigationOpen(false)}
        />
      </WorkbenchPortal>
      {runsError ? (
        <div className="p-2 pb-0">
          <Alert variant="destructive">
            <AlertTriangle />
            <AlertTitle>Runs could not be refreshed</AlertTitle>
            <AlertDescription className="flex items-center justify-between gap-3">
              <span>{runsError} The last successfully loaded rows remain visible.</span>
              <Button variant="outline" size="xs" onClick={() => void refreshRuns()}>
                <RotateCw />
                Retry
              </Button>
            </AlertDescription>
          </Alert>
        </div>
      ) : null}
      <div className="flex min-h-0 flex-1 flex-col overflow-hidden">
        <div className="min-h-0 flex-1 overflow-auto">
          <SimpleTable
            columns={[
              "Status",
              "Run ID",
              "Pipeline",
              "Environment",
              "Trigger",
              "Started",
              "Duration",
              "",
            ]}
            rows={visibleRuns.map((run) => [
              <StatusPill key="status" status={run.status} />,
              <Link
                key="id"
                to="/runs/$runId"
                params={{ runId: run.id }}
                search={search}
                className="font-mono text-primary hover:underline"
              >
                {run.id}
              </Link>,
              <span key="pipeline" className="font-mono">
                {run.pipeline}
              </span>,
              run.environment || "default",
              <span key="trigger" className="capitalize">
                {run.trigger}
              </span>,
              formatSchedulerDate(run.started_at),
              formatRunDuration(run),
              <Button key="open" asChild variant="ghost" size="icon-sm">
                <Link to="/runs/$runId" params={{ runId: run.id }} search={search}>
                  <ChevronRight className="size-4" />
                </Link>
              </Button>,
            ])}
          />
        </div>
        <div className="flex h-11 shrink-0 items-center gap-3 border-t px-3 text-xs text-muted-foreground">
          <span>
            {runsTotal === 0
              ? "0 runs"
              : `${runsOffset + 1}-${runsOffset + visibleRuns.length} of ${runsTotal}`}
          </span>
          <div className="flex-1" />
          <Button
            variant="outline"
            size="xs"
            disabled={page <= 1}
            onClick={() => updateSearch({ page: page - 1 })}
          >
            <ChevronLeft className="size-3" />
            Prev
          </Button>
          <span className="font-mono">
            {page} / {pages}
          </span>
          <Button
            variant="outline"
            size="xs"
            disabled={page >= pages}
            onClick={() => updateSearch({ page: page + 1 })}
          >
            Next
            <ChevronRight className="size-3" />
          </Button>
        </div>
      </div>
    </div>
  );
}

function RunNavigationSidebar({
  runs,
  total,
  loading,
  q,
  status,
  search,
  selectedRunId,
  onSearchChange,
  onNavigate,
}: {
  runs: PipelineRun[];
  total: number;
  loading: boolean;
  q?: string;
  status?: (typeof runStatuses)[number];
  search: AppRunsSearch;
  selectedRunId?: string;
  onSearchChange?: (next: AppRunsSearch) => void;
  onNavigate?: () => void;
}) {
  const activeStatus = status ?? "all";
  return (
    <AppContextSidebarFrame
      title="Runs"
      subtitle={`${total} recorded execution${total === 1 ? "" : "s"}`}
    >
      <div className="space-y-3 p-2">
        {onSearchChange ? (
          <>
            <div className="relative h-8 rounded-md border bg-background">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                aria-label="Search runs"
                value={q ?? ""}
                onChange={(event) =>
                  onSearchChange({ q: event.target.value || undefined, page: 1 })
                }
                placeholder="Search runs..."
                className="h-full border-0 bg-transparent pl-8 pr-8 text-xs shadow-none focus-visible:ring-0"
              />
              {loading ? (
                <Loader2
                  aria-label="Loading runs"
                  className="pointer-events-none absolute right-2.5 top-1/2 size-3.5 -translate-y-1/2 animate-spin text-muted-foreground"
                />
              ) : q ? (
                <Button
                  variant="ghost"
                  size="icon-xs"
                  className="absolute right-1 top-1/2 -translate-y-1/2"
                  aria-label="Clear run search"
                  onClick={() => onSearchChange({ q: undefined, page: 1 })}
                >
                  <X />
                </Button>
              ) : null}
            </div>
            <div className="flex flex-wrap gap-1">
              {runStatuses.map((item) => (
                <Button
                  key={item}
                  variant={activeStatus === item ? "secondary" : "ghost"}
                  size="xs"
                  className="h-6 capitalize"
                  onClick={() =>
                    onSearchChange({ status: item === "all" ? undefined : item, page: 1 })
                  }
                >
                  {item}
                </Button>
              ))}
            </div>
          </>
        ) : null}
        <div>
          <p className="px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
            {onSearchChange ? "Current page" : "Recent runs"}
          </p>
          <div className="space-y-0.5">
            {runs.slice(0, 12).map((run) => (
              <Link
                key={run.id}
                to="/runs/$runId"
                params={{ runId: run.id }}
                search={search}
                className={cn(
                  "flex items-center gap-2 rounded-md px-2 py-1.5 text-xs hover:bg-muted",
                  selectedRunId === run.id && "bg-primary/10 text-primary",
                )}
                onClick={onNavigate}
              >
                <span
                  className={cn(
                    "size-2 shrink-0 rounded-full bg-muted-foreground",
                    run.status === "success" && "bg-emerald-500",
                    run.status === "failed" && "bg-red-500",
                    run.status === "running" && "bg-blue-500",
                    run.status === "queued" && "bg-amber-500",
                  )}
                />
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-mono">{run.pipeline}</span>
                  <span className="block truncate text-[9px] text-muted-foreground">
                    {formatSchedulerDate(run.started_at)} · {formatRunDuration(run)}
                  </span>
                </span>
                <ChevronRight className="size-3 shrink-0 text-muted-foreground" />
              </Link>
            ))}
            {!loading && runs.length === 0 ? (
              <p className="px-2 py-3 text-xs text-muted-foreground">No runs match this view.</p>
            ) : null}
          </div>
        </div>
      </div>
    </AppContextSidebarFrame>
  );
}

export function AppRunDetailPage({
  runId,
  search = {},
}: {
  runId: string;
  search?: AppRunsSearch;
}) {
  const navigate = useNavigate();
  const workspace = useAtomValue(workspaceAtom);
  const {
    runs: recentRuns,
    runsTotal,
    loading: runsLoading,
    selectedRun,
    logs,
    steps,
    plan,
    units,
    reexecution,
    loadingRunId,
    busyPipeline,
    cancellingRunId,
    runDetailError,
    selectRun,
    triggerPipeline,
    reexecuteRun,
    cancelRun,
  } = usePipelineRuns({
    selectedRunId: runId,
  });
  const { setMobileNavigationOpen } = useWorkbench();
  const run = selectedRun;
  const [rerunError, setRerunError] = useState<{
    message: string;
    linkedRunId?: string;
    title?: string;
    linkLabel?: string;
  } | null>(null);
  const [cancelDialogOpen, setCancelDialogOpen] = useState(false);
  const [cancelError, setCancelError] = useState<string | null>(null);
  const [hoveredAsset, setHoveredAsset] = useState<string | null>(null);
  const [selectedAsset, setSelectedAsset] = useState<string | null>(null);
  const [runDetailTab, setRunDetailTab] = useState("events");
  const [scrollRequest, setScrollRequest] = useState<RunScrollRequest | null>(null);
  const scrollSequenceRef = useRef(0);
  useEffect(() => {
    setHoveredAsset(null);
    setSelectedAsset(null);
    setRunDetailTab("events");
    setScrollRequest(null);
  }, [runId]);
  const highlightedAsset = hoveredAsset ?? selectedAsset;
  const scrollToRunAsset = (asset: string, target: RunScrollRequest["target"]) => {
    setSelectedAsset(asset);
    if (target === "events") {
      setRunDetailTab("events");
    }
    setScrollRequest({
      asset,
      target,
      sequence: ++scrollSequenceRef.current,
    });
  };
  const output = useMemo(() => combineRunOutput(logs, run?.error), [logs, run?.error]);
  const assetIdsByName = useMemo(() => {
    const pipeline = workspace?.pipelines.find((candidate) => candidate.id === run?.pipeline_id);
    return new Map(pipeline?.assets.map((asset) => [asset.name, asset.id]) ?? []);
  }, [run?.pipeline_id, workspace?.pipelines]);
  const runAgain = async () => {
    if (!run) return;
    const exactReplay = reexecution?.mode === "exact";
    const executionContextResolved = run.execution_context_resolved === true;
    const hasCompleteRecordedWindow = Boolean(run.win_start && run.win_end);
    if (!exactReplay && executionContextResolved && !hasCompleteRecordedWindow) {
      setRerunError({
        message:
          "This run's resolved execution context has no complete window and cannot be reused safely.",
      });
      return;
    }
    const source: PipelineRunSource = run.snapshot_version_id
      ? { source: "snapshot", snapshot_version_id: run.snapshot_version_id }
      : { source: "working_tree" };
    setRerunError(null);
    let acceptedRunId: string;
    try {
      const response = exactReplay
        ? await reexecuteRun(run)
        : await (async () => {
            if (source.source === "working_tree") {
              await awaitWorkspaceSaves();
            }
            return triggerPipeline(run.pipeline_id, {
              ...source,
              ...(executionContextResolved && run.win_start && run.win_end
                ? {
                    environment: run.environment,
                    start: run.win_start,
                    end: run.win_end,
                  }
                : {}),
            });
          })();
      if (response.status !== "ok" || !response.run?.id) {
        throw new Error("The rerun was not accepted.");
      }
      acceptedRunId = response.run.id;
    } catch (cause) {
      const conflict = activePipelineRunConflict(cause);
      setRerunError({
        message: conflict
          ? "Another queued or running execution conflicts with this run."
          : cause instanceof Error
            ? cause.message
            : "Failed to queue the run.",
        linkedRunId: conflict?.activeRunId,
        title: conflict ? "Conflicting run" : undefined,
        linkLabel: conflict ? "Open active run" : undefined,
      });
      return;
    }
    try {
      await navigate({
        to: "/runs/$runId",
        params: { runId: acceptedRunId },
        search,
      });
    } catch (cause) {
      setRerunError({
        message: `Run ${acceptedRunId} was queued, but its details could not be opened${cause instanceof Error && cause.message ? `: ${cause.message}` : "."}`,
        linkedRunId: acceptedRunId,
        title: "Rerun queued",
        linkLabel: "Open queued run",
      });
    }
  };
  const abortRun = async () => {
    if (!run) return;
    setCancelError(null);
    try {
      const response = await cancelRun(run);
      if (response.status !== "ok") {
        throw new Error("The run could not be stopped.");
      }
    } catch (cause) {
      setCancelError(cause instanceof Error ? cause.message : "The run could not be stopped.");
    }
  };
  const runSidebar = (
    <WorkbenchPortal slot="context">
      <RunNavigationSidebar
        runs={recentRuns}
        total={runsTotal}
        loading={runsLoading}
        search={search}
        selectedRunId={runId}
        onNavigate={() => setMobileNavigationOpen(false)}
      />
    </WorkbenchPortal>
  );

  if (!run) {
    if (runDetailError) {
      return (
        <>
          {runSidebar}
          <AppPage>
            <PageHeader title={`Run ${runId}`} subtitle="Run details could not be loaded" />
            <div className="px-3 pb-3">
              <Alert variant="destructive">
                <AlertTriangle />
                <AlertTitle>Run details unavailable</AlertTitle>
                <AlertDescription className="flex items-center justify-between gap-3">
                  <span>{runDetailError}</span>
                  <Button variant="outline" size="sm" onClick={() => void selectRun(runId)}>
                    <RotateCw />
                    Retry
                  </Button>
                </AlertDescription>
              </Alert>
            </div>
          </AppPage>
        </>
      );
    }
    return (
      <>
        {runSidebar}
        <AppPage>
          <PageHeader
            title="Run"
            subtitle="Loading run details"
            actions={<Loader2 className="size-4 animate-spin text-muted-foreground" />}
          />
        </AppPage>
      </>
    );
  }

  const sourceLabel = run.snapshot_version_id
    ? deploymentLabel(run.snapshot_ordinal, run.snapshot_version_id, "deployment")
    : "saved workspace";
  const rerunSourceLabel = run.snapshot_version_id
    ? deploymentLabel(run.snapshot_ordinal, run.snapshot_version_id, "deployment")
    : "current saved workspace";
  const executionContextResolved = run.execution_context_resolved === true;
  const exactReplayAvailable = reexecution?.mode === "exact";
  const replayContextAvailable = exactReplayAvailable || executionContextResolved;
  const exactUnitCount = reexecution?.execution_units ?? units.length;
  const rerunEnvironmentLabel = replayContextAvailable
    ? run.environment || "default"
    : "current default resolved at start";
  const rerunButtonLabel = exactReplayAvailable
    ? "Re-execute exact plan"
    : "Run again with current settings";
  const compactRerunButtonLabel = exactReplayAvailable ? "Re-execute" : "Run current settings";
  const hasRecordedWindow = replayContextAvailable && Boolean(run.win_start && run.win_end);
  const hasIncompleteRecordedWindow = replayContextAvailable && !hasRecordedWindow;
  const rerunWindowLabel = hasRecordedWindow
    ? `${formatSchedulerDate(run.win_start)} → ${formatSchedulerDate(run.win_end)}`
    : hasIncompleteRecordedWindow
      ? "resolved context is incomplete; rerun unavailable"
      : "current pipeline default resolved at start";
  const rerunDescription = exactReplayAvailable
    ? `Replays the retained source, environment, window, execution time, variables, modes, authorization, and ${exactUnitCount} execution ${exactUnitCount === 1 ? "unit" : "units"} after verifying the selected configuration. The new run is manual and cannot advance a schedule watermark.`
    : `Source: ${run.snapshot_version_id ? `${deploymentLabel(run.snapshot_ordinal, run.snapshot_version_id, "deployment")} (${run.snapshot_version_id})` : "current saved workspace"}. ${executionContextResolved ? `Recorded environment: ${rerunEnvironmentLabel}. Recorded window: ${rerunWindowLabel}.` : "The original effective environment and window are unavailable; current defaults are resolved when the run starts."} Current execution settings are used; full-refresh, backfill, sensor mode, variables, selection, authorization, and schedule-only context are not replayed.${reexecution?.reason ? ` Exact re-execution is unavailable because ${lowercaseFirst(reexecution.reason)}` : ""}`;
  const rerunUnavailable = !exactReplayAvailable && hasIncompleteRecordedWindow;
  const runEnvironmentLabel = executionContextResolved
    ? run.environment || "default"
    : "execution context unavailable";
  const cancellationRequested = Boolean(run.cancellation_requested_at);
  const showAbortAction =
    (run.status === "queued" || run.status === "running") &&
    (Boolean(run.cancellable) || cancellationRequested);

  return (
    <>
      {runSidebar}
      <AppPage>
        <PageHeader
          title={`Run ${run.id}`}
          subtitle={`Run of ${run.pipeline} · ${runEnvironmentLabel} · ${sourceLabel} · ${plan ? `${units.length} execution units` : `${steps.length || "unknown"} assets`} · ${formatRunDuration(run)}`}
          actions={
            <div className="flex items-center gap-2">
              <Button asChild variant="ghost" size="icon-sm">
                <Link to="/runs" search={search}>
                  <ArrowLeft className="size-4" />
                </Link>
              </Button>
              <StatusPill status={run.status} />
              {showAbortAction ? (
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => setCancelDialogOpen(true)}
                  disabled={cancellingRunId === run.id || cancellationRequested}
                  aria-busy={cancellingRunId === run.id}
                >
                  {cancellingRunId === run.id || cancellationRequested ? (
                    <Loader2 data-icon="inline-start" className="animate-spin" />
                  ) : (
                    <CircleStop data-icon="inline-start" />
                  )}
                  {cancellationRequested ? "Stopping" : "Abort run"}
                </Button>
              ) : null}
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button
                    size="sm"
                    onClick={() => void runAgain()}
                    disabled={busyPipeline === run.pipeline_id || rerunUnavailable}
                    aria-busy={busyPipeline === run.pipeline_id}
                    aria-label={rerunButtonLabel}
                    aria-describedby="run-again-context"
                  >
                    {busyPipeline === run.pipeline_id ? (
                      <Loader2 data-icon="inline-start" className="animate-spin" />
                    ) : (
                      <RotateCw data-icon="inline-start" />
                    )}
                    <span className="hidden xl:inline">{rerunButtonLabel}</span>
                    <span className="xl:hidden">{compactRerunButtonLabel}</span>
                  </Button>
                </TooltipTrigger>
                <TooltipContent className="max-w-sm">{rerunDescription}</TooltipContent>
              </Tooltip>
            </div>
          }
        />
        <div
          id="run-again-context"
          className="flex flex-wrap items-center gap-x-2 gap-y-1 px-3 pb-2 text-xs text-muted-foreground"
          data-testid="run-again-context"
        >
          <span>
            {exactReplayAvailable ? "Replay source" : "Run source"}{" "}
            <span className="font-medium text-foreground">{rerunSourceLabel}</span>
          </span>
          <span aria-hidden="true">·</span>
          <span>
            Environment <span className="font-medium text-foreground">{rerunEnvironmentLabel}</span>
          </span>
          <span aria-hidden="true">·</span>
          <span>
            {hasRecordedWindow ? `Recorded window ${rerunWindowLabel}` : rerunWindowLabel}
          </span>
          <span aria-hidden="true">·</span>
          <span>
            Mode{" "}
            <span className="font-medium text-foreground">
              {exactReplayAvailable
                ? `exact ${humanizePlanValue(reexecution?.selection || plan?.selection.mode || "plan").toLowerCase()} plan · ${exactUnitCount} ${exactUnitCount === 1 ? "unit" : "units"}`
                : "current settings"}
            </span>
          </span>
        </div>
        {rerunError ? (
          <div className="px-3 pb-2">
            <Alert variant="destructive">
              <AlertTriangle />
              <AlertTitle>{rerunError.title ?? "Could not start rerun"}</AlertTitle>
              <AlertDescription className="flex flex-wrap items-center gap-2">
                <span>{rerunError.message}</span>
                {rerunError.linkedRunId ? (
                  <Button asChild variant="outline" size="xs">
                    <Link
                      to="/runs/$runId"
                      params={{ runId: rerunError.linkedRunId }}
                      search={search}
                    >
                      {rerunError.linkLabel ?? "Open run"}
                    </Link>
                  </Button>
                ) : null}
              </AlertDescription>
            </Alert>
          </div>
        ) : null}
        {cancelError ? (
          <div className="px-3 pb-2">
            <Alert variant="destructive">
              <AlertTriangle />
              <AlertTitle>Could not stop run</AlertTitle>
              <AlertDescription>{cancelError}</AlertDescription>
            </Alert>
          </div>
        ) : null}
        {runDetailError ? (
          <div className="px-3 pb-2">
            <Alert variant="destructive">
              <AlertTriangle />
              <AlertTitle>Run details could not be refreshed</AlertTitle>
              <AlertDescription className="flex items-center justify-between gap-3">
                <span>{runDetailError} Showing the last successfully loaded details.</span>
                <Button variant="outline" size="xs" onClick={() => void selectRun(runId)}>
                  <RotateCw />
                  Retry
                </Button>
              </AlertDescription>
            </Alert>
          </div>
        ) : null}
        <div className="flex min-h-0 flex-1 flex-col gap-3 px-3 pb-3">
          <RunTimelinePanel
            run={run}
            steps={steps}
            highlightedAsset={highlightedAsset}
            onHoveredAssetChange={setHoveredAsset}
            scrollRequest={scrollRequest}
            onActivateAsset={(asset) => scrollToRunAsset(asset, "events")}
          />
          <AppPanel className="min-h-0 flex-1 overflow-hidden">
            <Tabs
              value={runDetailTab}
              onValueChange={setRunDetailTab}
              className="flex h-full min-h-0 flex-col gap-0 overflow-hidden"
            >
              <div className="border-b px-2 py-1">
                <ScrollArea
                  className="min-w-0"
                  horizontalScrollBarClassName="hidden"
                  viewportClassName="w-full"
                >
                  <TabsList className="w-max max-w-none">
                    <TabsTrigger value="events" className={runTabsTriggerClass}>
                      <Play />
                      Events
                    </TabsTrigger>
                    {plan ? (
                      <TabsTrigger value="plan" className={runTabsTriggerClass}>
                        <ListTree />
                        Plan
                      </TabsTrigger>
                    ) : null}
                    <TabsTrigger value="output" className={runTabsTriggerClass}>
                      <Terminal />
                      Output
                    </TabsTrigger>
                  </TabsList>
                </ScrollArea>
              </div>
              <TabsContent
                value="events"
                className="m-0 min-h-0 flex-1 overflow-hidden data-[state=inactive]:hidden"
              >
                <RunEventsTable
                  run={run}
                  steps={steps}
                  loading={loadingRunId === run.id}
                  assetIdsByName={assetIdsByName}
                  highlightedAsset={highlightedAsset}
                  onHoveredAssetChange={setHoveredAsset}
                  scrollRequest={scrollRequest}
                  onActivateAsset={(asset) => scrollToRunAsset(asset, "timeline")}
                />
              </TabsContent>
              {plan ? (
                <TabsContent
                  value="plan"
                  className="m-0 min-h-0 flex-1 overflow-hidden data-[state=inactive]:hidden"
                >
                  <RunPlanPanel
                    run={run}
                    plan={plan}
                    units={units}
                    assetIdsByName={assetIdsByName}
                  />
                </TabsContent>
              ) : null}
              <TabsContent
                value="output"
                className="m-0 min-h-0 flex-1 overflow-hidden bg-zinc-950 data-[state=inactive]:hidden"
              >
                <RunTerminalOutput runId={run.id} output={output} />
              </TabsContent>
            </Tabs>
          </AppPanel>
        </div>
        <AlertDialog open={cancelDialogOpen} onOpenChange={setCancelDialogOpen}>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>Abort this run?</AlertDialogTitle>
              <AlertDialogDescription>
                {run.status === "queued"
                  ? "The queued execution will be cancelled before any work starts."
                  : "Renart will stop the active executor and preserve the completed events and output recorded so far."}
              </AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel disabled={cancellingRunId === run.id}>
                Keep running
              </AlertDialogCancel>
              <AlertDialogAction
                variant="destructive"
                disabled={cancellingRunId === run.id}
                onClick={() => void abortRun()}
              >
                Abort run
              </AlertDialogAction>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>
      </AppPage>
    </>
  );
}

function RunPlanPanel({
  run,
  plan,
  units,
  assetIdsByName,
}: {
  run: PipelineRun;
  plan: PipelineRunPlan;
  units: PipelineRunUnit[];
  assetIdsByName: Map<string, string>;
}) {
  const artifact = plan.artifact;
  const omittedUnits = plan.preview?.omitted_execution_units ?? [];
  const source = artifact.source;
  const context = artifact.context;
  const facts = [
    ["Source", formatRunPlanSource(source.kind, source.version_id, source.deployment_ordinal)],
    ["Environment", context.environment || "default"],
    ["Scope", formatRunPlanSelection(plan)],
    [
      "Window",
      `${formatSchedulerDate(context.start_date)} → ${formatSchedulerDate(context.end_date)}`,
    ],
    ["Execution time", formatSchedulerDate(plan.execution_time)],
    ["Plan ID", plan.plan_id],
    ["Source identity", plan.source_merkle],
    ["Configuration", plan.configuration_digest],
  ];
  if (plan.selection.data_state_token) {
    facts.push(["Data state", plan.selection.data_state_token]);
  }

  return (
    <ScrollArea className="h-full min-h-0" viewportClassName="h-full">
      <div className="min-w-0" data-testid="run-plan-panel">
        <div className="grid border-b sm:grid-cols-2 xl:grid-cols-4">
          {facts.map(([label, value]) => (
            <RunPlanFact key={label} label={label} value={value} />
          ))}
        </div>

        {omittedUnits.length > 0 ? (
          <div className="border-b p-3">
            <Alert>
              <AlertTriangle />
              <AlertTitle>Needed work changed before confirmation</AlertTitle>
              <AlertDescription>
                {omittedUnits.length} reviewed execution{" "}
                {omittedUnits.length === 1 ? "unit was" : "units were"} no longer needed and
                omitted. No new work was added without review.
              </AlertDescription>
            </Alert>
          </div>
        ) : null}

        <section aria-labelledby="run-plan-units-heading" className="border-b">
          <div className="flex flex-wrap items-baseline justify-between gap-2 px-3 py-2">
            <div>
              <h3 id="run-plan-units-heading" className="text-sm font-medium">
                Execution units
              </h3>
              <p className="text-xs text-muted-foreground">
                Final asset and window order admitted for this run.
              </p>
            </div>
            <span className="font-mono text-xs text-muted-foreground">
              {units.length} final · {omittedUnits.length} omitted
            </span>
          </div>
          {units.length > 0 ? (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="h-8 w-12 text-xs">#</TableHead>
                  <TableHead className="h-8 text-xs">Asset</TableHead>
                  <TableHead className="h-8 text-xs">Window</TableHead>
                  <TableHead className="h-8 text-xs">Reason</TableHead>
                  <TableHead className="h-8 text-xs">Status</TableHead>
                  <TableHead className="h-8 text-right text-xs">Duration</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {units.map((unit) => {
                  const assetId = assetIdsByName.get(unit.asset_name);
                  return (
                    <TableRow key={unit.position} data-testid="run-plan-unit">
                      <TableCell className="py-2 font-mono text-xs text-muted-foreground">
                        {unit.position + 1}
                      </TableCell>
                      <TableCell className="max-w-64 py-2 font-mono text-xs">
                        {assetId ? (
                          <Link
                            to="/pipelines/$pipelineId/assets/$assetId/split"
                            params={{ pipelineId: run.pipeline_id, assetId }}
                            className="break-words text-primary hover:underline"
                          >
                            {unit.asset_name}
                          </Link>
                        ) : (
                          <span className="break-words">{unit.asset_name}</span>
                        )}
                      </TableCell>
                      <TableCell className="py-2 text-xs text-muted-foreground">
                        <span className="whitespace-nowrap">
                          {formatSchedulerDate(unit.start_date)}
                        </span>
                        <span className="mx-1" aria-hidden="true">
                          →
                        </span>
                        <span className="whitespace-nowrap">
                          {formatSchedulerDate(unit.end_date)}
                        </span>
                      </TableCell>
                      <TableCell className="max-w-56 py-2 text-xs">
                        <span>{humanizePlanValue(unit.reason)}</span>
                        {unit.error ? (
                          <span className="mt-0.5 block text-destructive">{unit.error}</span>
                        ) : null}
                      </TableCell>
                      <TableCell className="py-2">
                        <RunUnitStatusPill status={unit.status} />
                      </TableCell>
                      <TableCell className="py-2 text-right font-mono text-xs text-muted-foreground">
                        {formatRunUnitDuration(unit)}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          ) : (
            <div className="border-t border-dashed px-3 py-6 text-center text-xs text-muted-foreground">
              All reviewed Needed work became fresh before confirmation, so this run had no physical
              execution units.
            </div>
          )}
        </section>

        <section aria-labelledby="run-plan-stages-heading">
          <div className="flex flex-wrap items-baseline justify-between gap-2 px-3 py-2">
            <div>
              <h3 id="run-plan-stages-heading" className="text-sm font-medium">
                Planned stages
              </h3>
              <p className="text-xs text-muted-foreground">
                Redacted stage metadata retained from the reviewed plan; statement text is not
                stored.
              </p>
            </div>
            <span className="font-mono text-xs text-muted-foreground">
              {artifact.summary.assets} assets · {artifact.summary.stages} stages
            </span>
          </div>
          <div className="border-t">
            {artifact.assets.map((asset) => {
              const assetId = assetIdsByName.get(asset.name);
              return (
                <div key={asset.id} className="border-b px-3 py-2 last:border-b-0">
                  <div className="flex min-w-0 flex-wrap items-center gap-2">
                    {assetId ? (
                      <Link
                        to="/pipelines/$pipelineId/assets/$assetId/split"
                        params={{ pipelineId: run.pipeline_id, assetId }}
                        className="min-w-0 break-words font-mono text-xs text-primary hover:underline"
                      >
                        {asset.name}
                      </Link>
                    ) : (
                      <span className="min-w-0 break-words font-mono text-xs">{asset.name}</span>
                    )}
                    <Badge variant="outline" size="xs">
                      {asset.type}
                    </Badge>
                    {asset.inclusion_reasons.map((reason) => (
                      <Badge key={reason} variant="muted" size="xs">
                        {humanizePlanValue(reason)}
                      </Badge>
                    ))}
                  </div>
                  <div className="mt-2 space-y-1.5">
                    {asset.renders.map((render, renderIndex) => (
                      <div
                        key={`${asset.id}-${render.start_date}-${render.end_date}-${renderIndex}`}
                        className="flex min-w-0 flex-wrap items-center gap-1.5 text-xs text-muted-foreground"
                      >
                        <span className="mr-1 font-mono text-[11px]">
                          {formatSchedulerDate(render.start_date)} →{" "}
                          {formatSchedulerDate(render.end_date)}
                        </span>
                        {render.stages.map((stage, stageIndex) => (
                          <Badge
                            key={`${stage.kind}-${stage.label ?? ""}-${stageIndex}`}
                            variant={runPlanStageBadgeVariant(stage.status, stage.fidelity)}
                            size="xs"
                            title={stage.message}
                          >
                            {stage.label || humanizePlanValue(stage.kind)} · {stage.fidelity}
                          </Badge>
                        ))}
                      </div>
                    ))}
                  </div>
                </div>
              );
            })}
          </div>
        </section>
      </div>
    </ScrollArea>
  );
}

function RunPlanFact({ label, value }: { label: string; value: string }) {
  const shortened = shortenPlanIdentity(value);
  return (
    <div className="min-w-0 border-r px-3 py-2 last:border-r-0">
      <div className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </div>
      <Tooltip>
        <TooltipTrigger asChild>
          <div className="mt-0.5 truncate font-mono text-xs" title={value}>
            {shortened}
          </div>
        </TooltipTrigger>
        <TooltipContent className="max-w-lg break-all font-mono text-xs">{value}</TooltipContent>
      </Tooltip>
    </div>
  );
}

function RunUnitStatusPill({ status }: { status: PipelineRunUnit["status"] }) {
  if (status !== "skipped") {
    return <StatusPill status={status} />;
  }
  return (
    <span className="inline-flex items-center rounded-full bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
      Skipped
    </span>
  );
}

function formatRunPlanSource(kind: string, versionId?: string, ordinal?: number) {
  if (kind === "snapshot") {
    return versionId
      ? `${deploymentLabel(ordinal, versionId)} (${versionId})`
      : deploymentLabel(ordinal, undefined);
  }
  return "Saved working tree";
}

function formatRunPlanSelection(plan: PipelineRunPlan) {
  if (plan.selection.mode === "selector" || plan.selection.mode === "selector_needed") {
    const prefix = plan.selection.mode === "selector_needed" ? "Needed matching" : "Matching";
    return `${prefix} · ${plan.selection.selector || "Unknown selector"}`;
  }
  if (plan.selection.mode !== "asset") {
    return humanizePlanValue(plan.selection.mode);
  }
  const scope = plan.selection.scope ? ` · ${humanizePlanValue(plan.selection.scope)}` : "";
  return `${plan.selection.asset_name || "Selected asset"}${scope}`;
}

function humanizePlanValue(value: string) {
  const normalized = value.trim().replaceAll("_", " ");
  return normalized ? normalized[0].toUpperCase() + normalized.slice(1) : "Unknown";
}

function lowercaseFirst(value: string) {
  return value ? value[0].toLocaleLowerCase() + value.slice(1) : value;
}

function shortenPlanIdentity(value: string) {
  return /^[a-f0-9]{32,}$/i.test(value) ? `${value.slice(0, 12)}…` : value;
}

function formatRunUnitDuration(unit: PipelineRunUnit) {
  if (!unit.started_at || !unit.finished_at) return "-";
  return formatDurationMs(
    new Date(unit.finished_at).getTime() - new Date(unit.started_at).getTime(),
  );
}

function runPlanStageBadgeVariant(
  status: string,
  fidelity: string,
): "secondary" | "destructive" | "outline" | "muted" {
  if (status === "error") return "destructive";
  if (status === "unsupported" || fidelity === "unsupported" || fidelity === "runtime_only") {
    return "muted";
  }
  return fidelity === "exact" ? "secondary" : "outline";
}

function RunTimelinePanel({
  run,
  steps,
  highlightedAsset,
  onHoveredAssetChange,
  scrollRequest,
  onActivateAsset,
}: {
  run: PipelineRun;
  steps: PipelineRunStep[];
  highlightedAsset: string | null;
  onHoveredAssetChange: (asset: string | null) => void;
  scrollRequest: RunScrollRequest | null;
  onActivateAsset: (asset: string) => void;
}) {
  const timelineRef = useRef<HTMLDivElement | null>(null);
  const timelineScroll = useFollowOutputScroll(steps.length, run.id);
  const now = useNow(run.status === "running");
  const bounds = timelineBounds(run, steps, now);
  const counts = countSteps(steps);
  const scrollable = steps.length >= 20;
  const rowHeight = timelineRowHeight(steps.length);

  useEffect(() => {
    if (scrollRequest?.target !== "timeline") return;
    const target = Array.from(
      timelineRef.current?.querySelectorAll<HTMLElement>(
        '[data-testid="run-timeline-asset-label"]',
      ) ?? [],
    ).find((element) => element.dataset.asset === scrollRequest.asset);
    scrollRunElementIntoView(target);
  }, [scrollRequest]);

  const timeline = (
    <div
      ref={timelineRef}
      className="grid grid-cols-[minmax(7rem,12rem)_minmax(0,1fr)] items-center gap-x-3 p-3"
      data-testid="run-timeline-grid"
      data-row-height={rowHeight}
    >
      <div aria-hidden="true" />
      <div
        className="flex h-5 items-center text-[11px] text-muted-foreground"
        data-testid="run-timeline-axis"
      >
        {timelineTicks(bounds).map((tick, index) => (
          <div key={`${index}-${tick.label}`} className="min-w-0 flex-1 font-mono">
            {tick.label}
          </div>
        ))}
      </div>
      {steps.length === 0 ? (
        <div className="col-span-2 rounded-md border border-dashed p-6 text-center text-xs text-muted-foreground">
          Asset timings will appear here for direct backend runs.
        </div>
      ) : null}
      {steps.map((step) => (
        <StepBar
          key={`${step.run_id}-${step.asset}`}
          step={step}
          bounds={bounds}
          now={now}
          rowHeight={rowHeight}
          highlighted={highlightedAsset === step.asset}
          onHighlightedChange={(highlighted) =>
            onHoveredAssetChange(highlighted ? step.asset : null)
          }
          onActivate={() => onActivateAsset(step.asset)}
        />
      ))}
    </div>
  );
  return (
    <AppPanel className="grid shrink-0 grid-cols-1 overflow-hidden lg:grid-cols-[minmax(0,1fr)_18rem]">
      {scrollable ? (
        <ScrollArea
          className="h-72 min-w-0"
          viewportClassName="h-full min-h-0"
          viewportRef={timelineScroll.viewportRef}
          onViewportScroll={timelineScroll.onViewportScroll}
          data-testid="run-timeline-scroll"
        >
          {timeline}
        </ScrollArea>
      ) : (
        <div className="min-w-0 overflow-hidden">{timeline}</div>
      )}
      <div className="border-t p-2 lg:border-l lg:border-t-0">
        {[
          ["Preparing", counts.queued],
          ["Executing", counts.running],
          ["Errored", counts.failed],
          ["Succeeded", counts.success],
          ["Cancelled", counts.cancelled],
        ].map(([label, count]) => (
          <div
            key={label}
            className="flex h-9 items-center justify-between rounded-md px-2 text-xs hover:bg-muted/60"
          >
            <span className="font-medium">{label}</span>
            <span className="font-mono text-muted-foreground">{count}</span>
          </div>
        ))}
      </div>
    </AppPanel>
  );
}

function StepBar({
  step,
  bounds,
  now,
  rowHeight,
  highlighted,
  onHighlightedChange,
  onActivate,
}: {
  step: PipelineRunStep;
  bounds: { start: number; end: number };
  now: number;
  rowHeight: number;
  highlighted: boolean;
  onHighlightedChange: (highlighted: boolean) => void;
  onActivate: () => void;
}) {
  const start = new Date(step.started_at ?? step.finished_at ?? bounds.start).getTime();
  const end = step.finished_at ? new Date(step.finished_at).getTime() : now;
  const rawLeft = ((start - bounds.start) / (bounds.end - bounds.start)) * 100;
  const width = Math.min(
    100,
    Math.max(1.2, ((Math.max(end, start + 1) - start) / (bounds.end - bounds.start)) * 100),
  );
  const left = Math.min(Math.max(0, rawLeft), 100 - width);
  const duration = formatDurationMs(Math.max(0, end - start));
  const dense = rowHeight < 20;
  const barInset = rowHeight < 16 ? 1 : 2;
  return (
    <>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            className={cn(
              "flex min-w-0 cursor-pointer items-center rounded-sm text-left font-mono transition-colors",
              dense ? "truncate text-[10px] leading-none" : "break-words text-[11px] leading-4",
              highlighted && "bg-primary/10 text-foreground ring-1 ring-primary/30",
            )}
            data-testid="run-timeline-asset-label"
            data-asset={step.asset}
            data-highlighted={highlighted ? "true" : "false"}
            style={{ height: rowHeight }}
            onPointerEnter={() => onHighlightedChange(true)}
            onPointerLeave={() => onHighlightedChange(false)}
            onClick={onActivate}
            aria-label={`Show events for ${step.asset}`}
          >
            {step.asset}
          </button>
        </TooltipTrigger>
        <TooltipContent>{step.asset}</TooltipContent>
      </Tooltip>
      <button
        type="button"
        className={cn(
          "relative cursor-pointer rounded bg-muted/40 transition-colors",
          highlighted && "bg-primary/10 ring-1 ring-inset ring-primary/30",
        )}
        data-testid="run-timeline-track"
        data-asset={step.asset}
        data-highlighted={highlighted ? "true" : "false"}
        style={{ height: rowHeight }}
        onPointerEnter={() => onHighlightedChange(true)}
        onPointerLeave={() => onHighlightedChange(false)}
        onClick={onActivate}
        aria-label={`Show events for ${step.asset}`}
      >
        <Tooltip>
          <TooltipTrigger asChild>
            <span
              className={cn(
                "absolute block min-w-px rounded transition-[filter,box-shadow]",
                step.status === "failed"
                  ? "bg-destructive"
                  : step.status === "running"
                    ? "bg-primary/60"
                    : step.status === "success"
                      ? "bg-primary"
                      : "bg-muted-foreground/45",
                highlighted && "brightness-110 ring-2 ring-foreground/35",
              )}
              data-testid="run-timeline-bar"
              data-status={step.status}
              style={{
                left: `${left}%`,
                width: `${width}%`,
                top: barInset,
                height: Math.max(4, rowHeight - barInset * 2),
              }}
            />
          </TooltipTrigger>
          <TooltipContent>
            <span className="font-mono">{step.asset}</span>
            <span className="ml-1 capitalize">
              · {step.status} · {duration}
            </span>
          </TooltipContent>
        </Tooltip>
      </button>
    </>
  );
}

function RunEventsTable({
  run,
  steps,
  loading,
  assetIdsByName,
  highlightedAsset,
  onHoveredAssetChange,
  scrollRequest,
  onActivateAsset,
}: {
  run: PipelineRun;
  steps: PipelineRunStep[];
  loading: boolean;
  assetIdsByName: Map<string, string>;
  highlightedAsset: string | null;
  onHoveredAssetChange: (asset: string | null) => void;
  scrollRequest: RunScrollRequest | null;
  onActivateAsset: (asset: string) => void;
}) {
  const events = runEvents(run, steps);
  const eventsScroll = useFollowOutputScroll(`${events.length}:${loading}`, run.id);
  useEffect(() => {
    if (scrollRequest?.target !== "events") return;
    const target = Array.from(
      eventsScroll.viewportRef.current?.querySelectorAll<HTMLElement>(
        '[data-testid="run-event-row"]',
      ) ?? [],
    ).find((element) => element.dataset.asset === scrollRequest.asset);
    scrollRunElementIntoView(target);
  }, [scrollRequest]);
  return (
    <ScrollArea
      className="h-full min-h-0"
      viewportClassName="h-full"
      viewportRef={eventsScroll.viewportRef}
      onViewportScroll={eventsScroll.onViewportScroll}
    >
      <Table>
        <TableHeader className="sticky top-0 z-10 bg-card">
          <TableRow>
            <TableHead className="h-8 w-40 text-xs uppercase text-muted-foreground">
              Timestamp
            </TableHead>
            <TableHead className="h-8 w-44 text-xs uppercase text-muted-foreground">
              Asset
            </TableHead>
            <TableHead className="h-8 w-28 text-xs uppercase text-muted-foreground">Type</TableHead>
            <TableHead className="h-8 text-xs uppercase text-muted-foreground">Info</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {events.map((event, index) => {
            const assetId = assetIdsByName.get(event.asset);
            const badge = runEventBadge(event.type);
            const highlighted = highlightedAsset === event.asset;
            return (
              <TableRow
                key={`${event.at}-${event.asset}-${event.type}-${index}`}
                className={cn(
                  "cursor-pointer transition-colors",
                  highlighted && "bg-primary/10 hover:bg-primary/15",
                )}
                data-testid="run-event-row"
                data-asset={event.asset}
                data-highlighted={highlighted ? "true" : "false"}
                onPointerEnter={() => onHoveredAssetChange(event.asset)}
                onPointerLeave={() => onHoveredAssetChange(null)}
                onClick={(clickEvent) => {
                  if ((clickEvent.target as HTMLElement).closest("a, button")) return;
                  onActivateAsset(event.asset);
                }}
                tabIndex={0}
                onKeyDown={(keyEvent) => {
                  if (keyEvent.key !== "Enter" && keyEvent.key !== " ") return;
                  keyEvent.preventDefault();
                  onActivateAsset(event.asset);
                }}
              >
                <TableCell className="h-8 py-1.5 font-mono text-xs text-muted-foreground">
                  {formatSchedulerDate(event.at)}
                </TableCell>
                <TableCell className="h-8 py-1.5 font-mono text-xs">
                  {assetId ? (
                    <Link
                      to="/pipelines/$pipelineId/assets/$assetId/split"
                      params={{ pipelineId: run.pipeline_id, assetId }}
                      className="text-primary hover:underline"
                    >
                      {event.asset}
                    </Link>
                  ) : (
                    event.asset
                  )}
                </TableCell>
                <TableCell className="h-8 py-1.5">
                  <Badge
                    variant={badge.variant}
                    size="xs"
                    className="font-mono uppercase"
                    data-event-type={event.type}
                    data-event-tone={badge.tone}
                  >
                    {event.type}
                  </Badge>
                </TableCell>
                <TableCell className="h-8 py-1.5 text-xs">{event.info}</TableCell>
              </TableRow>
            );
          })}
          {!loading && events.length === 0 ? (
            <TableRow>
              <TableCell colSpan={4} className="py-6 text-center text-xs text-muted-foreground">
                No high-level events captured.
              </TableCell>
            </TableRow>
          ) : null}
        </TableBody>
      </Table>
    </ScrollArea>
  );
}

function runEventBadge(type: string): {
  variant: "default" | "secondary" | "destructive" | "muted";
  tone: "success" | "progress" | "failure" | "cancelled";
} {
  if (type.endsWith("_failed")) {
    return { variant: "destructive", tone: "failure" };
  }
  if (type.endsWith("_cancelled")) {
    return { variant: "muted", tone: "cancelled" };
  }
  if (type === "asset_start") {
    return { variant: "secondary", tone: "progress" };
  }
  return { variant: "default", tone: "success" };
}

function scrollRunElementIntoView(target?: HTMLElement) {
  if (!target) return;
  target.scrollIntoView({
    block: "center",
    behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth",
  });
}

function combineRunOutput(logs: PipelineRunLogLine[], error?: string) {
  const captured = logs.map((log) => log.line).join("");
  const terminalError = error?.trim();
  if (!terminalError || captured.includes(terminalError)) {
    return captured || "No output captured.";
  }

  const separator = captured && !captured.endsWith("\n") ? "\n" : "";
  return `${captured}${separator}${terminalError}\n`;
}

function RunTerminalOutput({ runId, output }: { runId: string; output: string }) {
  const outputScroll = useFollowOutputScroll(output, runId);
  return (
    <ScrollArea
      className="h-full min-h-0"
      viewportClassName="h-full"
      viewportRef={outputScroll.viewportRef}
      onViewportScroll={outputScroll.onViewportScroll}
    >
      <AnsiOutput
        output={output}
        className="font-console whitespace-pre-wrap p-3 text-xs text-zinc-100"
      />
    </ScrollArea>
  );
}

function useNow(active: boolean) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!active) return;
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [active]);
  return now;
}

function timelineBounds(run: PipelineRun, steps: PipelineRunStep[], now: number) {
  const times = [
    run.started_at,
    run.finished_at,
    ...steps.flatMap((step) => [step.started_at, step.finished_at]),
  ]
    .map((value) => (value ? new Date(value).getTime() : NaN))
    .filter(Number.isFinite);
  const start = Math.min(...times, now);
  const end = Math.max(...times, run.status === "running" ? now : 0);
  return { start, end: Math.max(end, start + 1000) };
}

function timelineRowHeight(stepCount: number) {
  if (stepCount >= 20) return 16;
  if (stepCount <= 0) return 28;
  // Keep the axis, padding, and up to 19 asset rows within the panel's
  // 18rem height. Short runs retain the roomier 28px rows.
  return Math.max(12, Math.min(28, Math.floor(244 / stepCount)));
}

function timelineTicks(bounds: { start: number; end: number }) {
  const duration = bounds.end - bounds.start;
  return Array.from({ length: 5 }, (_, index) => {
    const offset = (duration / 4) * index;
    if (duration <= 1000) {
      return { label: `${Math.round(offset)}ms` };
    }
    const seconds = Math.round(offset / 1000);
    return {
      label: seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m ${seconds % 60}s`,
    };
  });
}

function countSteps(steps: PipelineRunStep[]) {
  return {
    queued: steps.filter((step) => step.status === "queued").length,
    running: steps.filter((step) => step.status === "running").length,
    failed: steps.filter((step) => step.status === "failed").length,
    success: steps.filter((step) => step.status === "success").length,
    cancelled: steps.filter((step) => step.status === "cancelled").length,
  };
}

function runEvents(run: PipelineRun, steps: PipelineRunStep[]) {
  const events = steps.flatMap((step) => {
    const items: Array<{ at: string; asset: string; type: string; info: string }> = [];
    if (step.started_at)
      items.push({
        at: step.started_at,
        asset: step.asset,
        type: "asset_start",
        info: `Started ${step.asset}.`,
      });
    if (step.finished_at)
      items.push({
        at: step.finished_at,
        asset: step.asset,
        type:
          step.status === "failed"
            ? "asset_failed"
            : step.status === "cancelled"
              ? "asset_cancelled"
              : "asset_success",
        info:
          step.status === "failed"
            ? step.error || `Failed ${step.asset}.`
            : step.status === "cancelled"
              ? step.error || `Cancelled ${step.asset}.`
              : `Finished ${step.asset} in ${formatStepDuration(step)}.`,
      });
    return items;
  });
  if (run.finished_at)
    events.push({
      at: run.finished_at,
      asset: run.pipeline,
      type:
        run.status === "failed"
          ? "run_failed"
          : run.status === "cancelled"
            ? "run_cancelled"
            : "run_finished",
      info: run.error || `Run ${run.status}.`,
    });
  return events.sort((a, b) => new Date(a.at).getTime() - new Date(b.at).getTime());
}

function formatStepDuration(step: PipelineRunStep) {
  if (!step.started_at || !step.finished_at) return "-";
  return formatDurationMs(
    new Date(step.finished_at).getTime() - new Date(step.started_at).getTime(),
  );
}

function formatRunDuration(run: PipelineRun) {
  if (!run.started_at || !run.finished_at) return run.status === "running" ? "running" : "-";
  return formatDurationMs(new Date(run.finished_at).getTime() - new Date(run.started_at).getTime());
}

function formatDurationMs(ms: number) {
  if (!Number.isFinite(ms) || ms < 0) return "-";
  if (ms < 1000) return `${ms}ms`;
  const seconds = Math.round(ms / 1000);
  const minutes = Math.floor(seconds / 60);
  const remainder = seconds % 60;
  return minutes > 0 ? `${minutes}m ${remainder}s` : `${seconds}s`;
}
