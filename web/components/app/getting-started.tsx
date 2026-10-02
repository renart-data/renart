import { useLocation, useNavigate } from "@tanstack/react-router";
import { useAtomValue, useSetAtom } from "jotai";
import { ArrowRight, BookOpen, CheckCircle2, Circle, Sparkles, X } from "lucide-react";
import { useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { useGettingStartedStalenessObserver } from "@/hooks/use-getting-started-observer";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { openConnectDataAtom } from "@/lib/atoms/domains/connect-data";
import { workspaceAtom } from "@/lib/atoms/domains/workspace";
import { stalenessEventAtom } from "@/lib/atoms/domains/results";
import {
  DOCS_ORIGIN,
  dismissGettingStartedAtom,
  gettingStartedAtom,
  gettingStartedItems,
  gettingStartedProgress,
  markGettingStartedAtom,
  observedOwnConnection,
  observedRouteItem,
  readGettingStarted,
  type GettingStartedItem,
  type GettingStartedItemId,
} from "@/lib/getting-started";
import { highlightNavigationElement } from "@/lib/navigation-arrival";
import type { WorkspaceState } from "@/lib/types";
import { cn } from "@/lib/utils";

// Loads the open project's checklist and marks items as the app observes them.
export function useGettingStartedTracker(projectId: string | null | undefined) {
  const setChecklist = useSetAtom(gettingStartedAtom);
  const checklist = useAtomValue(gettingStartedAtom);
  const mark = useSetAtom(markGettingStartedAtom);
  const pathname = useLocation({ select: (location) => location.pathname });
  const staleness = useAtomValue(stalenessEventAtom);
  const observeGettingStartedStaleness = useGettingStartedStalenessObserver();
  const { normalizedConfigEnvironments } = useWorkspaceSettingsData();

  useEffect(() => {
    const record = projectId ? readGettingStarted(projectId) : null;
    setChecklist(record && projectId ? { projectId, record } : null);
  }, [projectId, setChecklist]);

  const active = Boolean(checklist && !checklist.record.dismissed);

  useEffect(() => {
    if (!active) return;
    const item = observedRouteItem(pathname);
    if (item) mark({ id: item });
  }, [active, mark, pathname]);

  useEffect(() => {
    if (staleness) observeGettingStartedStaleness(staleness.assets);
  }, [observeGettingStartedStaleness, staleness]);

  useEffect(() => {
    if (active && observedOwnConnection(normalizedConfigEnvironments)) mark({ id: "connect" });
  }, [active, mark, normalizedConfigEnvironments]);
  // Inspect results and schedules mark their items where they are loaded
  // (AssetInspectView, useEnvSchedules).
}

export function GettingStartedChip() {
  const checklist = useAtomValue(gettingStartedAtom);
  const dismiss = useSetAtom(dismissGettingStartedAtom);
  const workspace = useAtomValue(workspaceAtom);
  const navigate = useNavigate();
  const openConnectData = useSetAtom(openConnectDataAtom);
  const [open, setOpen] = useState(false);

  if (!checklist || checklist.record.dismissed) return null;
  const { done, total } = gettingStartedProgress(checklist.record);
  const complete = done === total;

  const showMe = (item: GettingStartedItem) => {
    setOpen(false);
    if (item.id === "connect") {
      openConnectData(true);
      return;
    }
    const destination = gettingStartedDestination(item.id, workspace);
    if (!destination) return;
    void destination.go(navigate).then(() => {
      if (destination.target) highlightWhenReady(destination.target);
    });
  };

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          aria-label={`Getting started, ${done} of ${total} done`}
          className="mr-1 h-8 gap-2 px-2 text-zinc-300 hover:bg-zinc-800 hover:text-white"
        >
          <ProgressRing done={done} total={total} />
          <span className="hidden text-xs lg:inline">Getting started</span>
          <span className="text-[11px] tabular-nums text-zinc-500">
            {done}/{total}
          </span>
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-[22rem] p-0">
        <div className="flex items-start justify-between gap-3 border-b px-4 py-3">
          <div className="min-w-0">
            <h2 className="flex items-center gap-1.5 text-sm font-semibold">
              <Sparkles className="size-3.5 text-primary" />
              Getting started
            </h2>
            <p className="mt-0.5 text-xs text-muted-foreground">
              {complete
                ? "You've seen the whole workflow. Nice work."
                : "Each step completes when Renart sees you do it."}
            </p>
          </div>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Hide getting started"
            title="Hide getting started"
            onClick={() => {
              setOpen(false);
              dismiss(true);
            }}
          >
            <X />
          </Button>
        </div>
        <ol className="grid max-h-[min(28rem,70vh)] gap-0.5 overflow-y-auto p-1.5">
          {gettingStartedItems.map((item) => {
            const itemDone = Boolean(checklist.record.done[item.id]);
            return (
              <li
                key={item.id}
                className={cn(
                  "group flex gap-2.5 rounded-md px-2.5 py-2",
                  !itemDone && "hover:bg-muted/60",
                )}
              >
                {itemDone ? (
                  <CheckCircle2
                    aria-label="Done"
                    className="mt-0.5 size-4 shrink-0 text-primary motion-safe:animate-in motion-safe:zoom-in-50"
                  />
                ) : (
                  <Circle
                    aria-label="Not done yet"
                    className="mt-0.5 size-4 shrink-0 text-muted-foreground/50"
                  />
                )}
                <div className="min-w-0 flex-1">
                  <div
                    className={cn(
                      "text-sm",
                      itemDone
                        ? "text-muted-foreground line-through decoration-muted-foreground/40"
                        : "font-medium",
                    )}
                  >
                    {item.title}
                  </div>
                  {!itemDone ? (
                    <>
                      <p className="mt-0.5 text-xs leading-relaxed text-muted-foreground">
                        {item.description}
                      </p>
                      <div className="mt-1.5 flex items-center gap-3">
                        <button
                          type="button"
                          onClick={() => showMe(item)}
                          className="flex items-center gap-1 text-xs font-medium text-primary hover:underline"
                        >
                          Show me
                          <ArrowRight className="size-3" />
                        </button>
                        <a
                          href={`${DOCS_ORIGIN}${item.docsPath}`}
                          target="_blank"
                          rel="noreferrer"
                          className="flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
                        >
                          <BookOpen className="size-3" />
                          Docs
                        </a>
                      </div>
                    </>
                  ) : null}
                </div>
              </li>
            );
          })}
        </ol>
      </PopoverContent>
    </Popover>
  );
}

function ProgressRing({ done, total }: { done: number; total: number }) {
  const radius = 6;
  const circumference = 2 * Math.PI * radius;
  return (
    <svg viewBox="0 0 16 16" className="size-4 -rotate-90" aria-hidden>
      <circle
        cx="8"
        cy="8"
        r={radius}
        fill="none"
        stroke="currentColor"
        strokeOpacity="0.25"
        strokeWidth="2"
      />
      <circle
        cx="8"
        cy="8"
        r={radius}
        fill="none"
        stroke="var(--primary)"
        strokeWidth="2"
        strokeLinecap="round"
        strokeDasharray={circumference}
        strokeDashoffset={circumference * (1 - done / Math.max(total, 1))}
        className="transition-[stroke-dashoffset] duration-500 ease-out motion-reduce:transition-none"
      />
    </svg>
  );
}

type Navigate = ReturnType<typeof useNavigate>;

type Destination = {
  go: (navigate: Navigate) => Promise<void>;
  // A data-getting-started-target on the destination page to highlight.
  target?: string;
};

// Where "Show me" goes for an item: the first pipeline's most-depended-on
// asset for inspecting and editing, the canvas for a rebuild, and so on.
function gettingStartedDestination(
  id: Exclude<GettingStartedItemId, "connect">,
  workspace: WorkspaceState | null,
): Destination | null {
  const pipeline = workspace?.pipelines[0];
  const asset = pipeline ? mostDependedOnAsset(pipeline.assets) : undefined;
  switch (id) {
    case "inspect":
      if (!pipeline || !asset) return null;
      return {
        go: (navigate) =>
          navigate({
            to: "/pipelines/$pipelineId/assets/$assetId/canvas",
            params: { pipelineId: pipeline.id, assetId: asset.id },
          }),
        target: "inspect",
      };
    case "edit":
      if (!pipeline || !asset) return null;
      return {
        go: (navigate) =>
          navigate({
            to: "/pipelines/$pipelineId/assets/$assetId/split",
            params: { pipelineId: pipeline.id, assetId: asset.id },
          }),
      };
    case "rebuild":
      if (!pipeline) return null;
      return {
        go: (navigate) =>
          navigate({ to: "/pipelines/$pipelineId/canvas", params: { pipelineId: pipeline.id } }),
        target: "review-run",
      };
    case "notebook": {
      const notebook = workspace?.notebooks?.[0];
      return {
        go: (navigate) =>
          notebook
            ? navigate({ to: "/notebooks/$notebookId", params: { notebookId: notebook.id } })
            : navigate({ to: "/notebooks" }),
      };
    }
    case "dashboard":
      return { go: (navigate) => navigate({ to: "/dashboards" }) };
    case "schedule":
      return { go: (navigate) => navigate({ to: "/schedules" }), target: "new-schedule" };
  }
}

function mostDependedOnAsset(assets: WorkspaceState["pipelines"][number]["assets"]) {
  const dependents = new Map<string, number>();
  for (const asset of assets) {
    for (const upstream of asset.upstreams ?? []) {
      dependents.set(upstream, (dependents.get(upstream) ?? 0) + 1);
    }
  }
  return [...assets].sort(
    (left, right) =>
      (dependents.get(right.name) ?? 0) - (dependents.get(left.name) ?? 0) ||
      left.name.localeCompare(right.name),
  )[0];
}

// The destination renders after navigation; wait briefly for its target.
function highlightWhenReady(target: string) {
  const started = performance.now();
  const find = () => {
    const element = document.querySelector<HTMLElement>(
      `[data-getting-started-target="${target}"]`,
    );
    if (element && element.getClientRects().length > 0) {
      element.scrollIntoView({ block: "nearest", behavior: "smooth" });
      highlightNavigationElement(element);
      return;
    }
    if (performance.now() - started < 3000) requestAnimationFrame(find);
  };
  requestAnimationFrame(find);
}
