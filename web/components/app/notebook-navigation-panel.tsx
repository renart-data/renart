"use client";

import {
  CircleDot,
  CornerDownRight,
  ExternalLink,
  ListTree,
  Search,
  Workflow,
  X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  buildNotebookFlowModel,
  buildNotebookNavigationEntries,
  filterNotebookNavigationEntries,
  notebookFlowContext,
  type NotebookNavigationKind,
} from "@/lib/notebook-navigation";
import type { WebNotebook, WebNotebookBlock } from "@/lib/types";
import { cn } from "@/lib/utils";

const NAVIGATION_KIND_LABELS: Record<NotebookNavigationKind, string> = {
  sql: "SQL",
  python: "Python",
  source: "Source",
  markdown: "Text",
  control: "Control",
  visualization: "Chart",
};

export function NotebookOutlinePanel({
  notebook,
  selectedBlockKey,
  onSelectBlock,
}: {
  notebook: WebNotebook;
  selectedBlockKey: string | null;
  onSelectBlock: (block: WebNotebookBlock) => void;
}) {
  const [query, setQuery] = useState("");
  useEffect(() => setQuery(""), [notebook.id]);
  const entries = useMemo(() => buildNotebookNavigationEntries(notebook), [notebook]);
  const matches = useMemo(() => filterNotebookNavigationEntries(entries, query), [entries, query]);

  return (
    <div data-testid="notebook-outline-panel" className="flex min-w-0 flex-col gap-2 p-2">
      <div className="relative">
        <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={query}
          aria-label="Search notebook"
          placeholder="Search code and blocks"
          className="h-8 pr-8 pl-8 text-xs"
          onChange={(event) => setQuery(event.target.value)}
        />
        {query ? (
          <Button
            type="button"
            size="icon-xs"
            variant="ghost"
            aria-label="Clear notebook search"
            className="absolute top-1/2 right-1.5 -translate-y-1/2"
            onClick={() => setQuery("")}
          >
            <X />
          </Button>
        ) : null}
      </div>
      {query ? (
        <p aria-live="polite" className="px-1 text-[10px] text-muted-foreground">
          {matches.length} matching block{matches.length === 1 ? "" : "s"}
        </p>
      ) : null}
      <div className="flex min-w-0 flex-col gap-1">
        {matches.map((entry) => {
          const selected = selectedBlockKey === entry.key;
          return (
            <button
              key={entry.key}
              type="button"
              aria-current={selected ? "location" : undefined}
              className={cn(
                "flex min-w-0 items-start gap-2 rounded-md px-2 py-2 text-left transition-colors hover:bg-accent",
                selected && "bg-primary/8 text-foreground ring-1 ring-primary/20",
              )}
              onClick={() => onSelectBlock(entry.block)}
            >
              <ListTree className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1">
                <span className="flex min-w-0 items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-xs font-medium">{entry.title}</span>
                  <span className="shrink-0 text-[9px] font-medium uppercase tracking-wide text-muted-foreground">
                    {NAVIGATION_KIND_LABELS[entry.kind]}
                  </span>
                </span>
                {query && entry.snippet ? (
                  <span className="mt-0.5 line-clamp-2 block text-[10px] leading-4 text-muted-foreground">
                    {entry.snippet}
                  </span>
                ) : entry.detail ? (
                  <span className="mt-0.5 block truncate text-[10px] text-muted-foreground">
                    {entry.detail}
                  </span>
                ) : null}
              </span>
            </button>
          );
        })}
        {matches.length === 0 ? (
          <div className="rounded-lg border border-dashed px-3 py-6 text-center text-xs text-muted-foreground">
            No notebook blocks match “{query}”.
          </div>
        ) : null}
      </div>
    </div>
  );
}

export function NotebookFlowPanel({
  notebook,
  selectedBlockKey,
  staleCellIds,
  runningCellIds,
  onSelectBlock,
}: {
  notebook: WebNotebook;
  selectedBlockKey: string | null;
  staleCellIds: Set<string>;
  runningCellIds: Set<string>;
  onSelectBlock: (block: WebNotebookBlock) => void;
}) {
  const model = useMemo(() => buildNotebookFlowModel(notebook), [notebook]);
  const selectedNode = model.nodes.find((node) => node.key === selectedBlockKey);
  const context = useMemo(
    () => notebookFlowContext(model, selectedNode?.key ?? null),
    [model, selectedNode?.key],
  );

  return (
    <div data-testid="notebook-flow-panel" className="flex min-w-0 flex-col gap-2 p-2">
      <div className="rounded-lg border bg-muted/20 p-2.5">
        <p className="flex items-center gap-1.5 text-xs font-medium">
          <Workflow className="size-3.5 text-primary" />
          Notebook flow
        </p>
        <p className="mt-1 text-[10px] leading-4 text-muted-foreground">
          Select a block to trace its upstream and downstream path. External references stay visible
          instead of being guessed as notebook edges.
        </p>
      </div>
      <div className="flex min-w-0 flex-col gap-1">
        {model.nodes.map((node) => {
          const selected = selectedNode?.key === node.key;
          const ancestor = context.ancestors.has(node.key);
          const descendant = context.descendants.has(node.key);
          const unrelated = Boolean(selectedNode) && !selected && !ancestor && !descendant;
          const running = Boolean(node.cellId && runningCellIds.has(node.cellId));
          const stale = Boolean(node.cellId && staleCellIds.has(node.cellId));
          const relation = selected
            ? "Selected"
            : ancestor
              ? "Upstream"
              : descendant
                ? "Downstream"
                : undefined;

          return (
            <button
              key={node.key}
              type="button"
              aria-current={selected ? "location" : undefined}
              aria-label={`${node.title}${relation ? `, ${relation}` : ""}`}
              className={cn(
                "relative flex min-w-0 items-start gap-2 rounded-md py-2 pr-2 text-left transition-[color,background-color,opacity] hover:bg-accent",
                selected && "bg-primary/8 ring-1 ring-primary/20",
                ancestor && "bg-sky-500/8",
                descendant && "bg-emerald-500/8",
                unrelated && "opacity-45 hover:opacity-100",
              )}
              style={{ paddingLeft: `${8 + Math.min(node.depth, 5) * 12}px` }}
              onClick={() => onSelectBlock(node.block)}
            >
              {node.depth > 0 ? (
                <CornerDownRight className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
              ) : (
                <CircleDot
                  className={cn(
                    "mt-0.5 size-3.5 shrink-0 text-muted-foreground",
                    running && "animate-pulse text-sky-500",
                    stale && !running && "text-amber-500",
                  )}
                />
              )}
              <span className="min-w-0 flex-1">
                <span className="flex min-w-0 items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-xs font-medium">{node.title}</span>
                  <span className="shrink-0 text-[9px] font-medium uppercase tracking-wide text-muted-foreground">
                    {NAVIGATION_KIND_LABELS[node.kind]}
                  </span>
                </span>
                <span className="mt-0.5 block truncate text-[10px] text-muted-foreground">
                  {running
                    ? "Running"
                    : stale
                      ? "Stale"
                      : `${node.upstreamNodeIds.length} upstream · ${node.downstreamNodeIds.length} downstream`}
                </span>
                {node.externalReferences.length > 0 ? (
                  <span className="mt-1 flex min-w-0 items-center gap-1 text-[9px] text-muted-foreground">
                    <ExternalLink className="size-3 shrink-0" />
                    <span className="truncate" title={node.externalReferences.join(", ")}>
                      {node.externalReferences.join(", ")}
                    </span>
                  </span>
                ) : null}
              </span>
            </button>
          );
        })}
        {model.nodes.length === 0 ? (
          <div className="rounded-lg border border-dashed px-3 py-6 text-center text-xs text-muted-foreground">
            Add a data, control, or visualization block to see notebook flow.
          </div>
        ) : null}
      </div>
    </div>
  );
}
