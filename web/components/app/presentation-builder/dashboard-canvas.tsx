"use client";

import "react-grid-layout/css/styles.css";

import { Copy, GripHorizontal, MoreHorizontal, Plus, Trash2, TriangleAlert } from "lucide-react";
import { useMemo, useState } from "react";
import { GridLayout, useContainerWidth, verticalCompactor, type Layout } from "react-grid-layout";

import { hasAuthoringDragItem, readAuthoringDragItem } from "@/components/app/authoring-drag";
import type { ChartType } from "@/components/app/chart-type-picker";
import {
  normalizeVisualizationDefinition,
  visualizationDefinitionRecord,
} from "@/components/app/notebook-visualization-block";
import { PresentationVisualizationCard } from "@/components/app/presentation-viewer";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import type {
  PresentationArtifact,
  PresentationDatasetResult,
  PresentationFinding,
  PresentationLayoutItem,
  PresentationVisualization,
} from "@/lib/api-presentations";
import { cn } from "@/lib/utils";

import {
  derivedDashboardSpan,
  normalizedDashboardLayout,
  presentationLayoutFromGrid,
  type PresentationBuilderSelection,
  type PresentationPreviewMode,
} from "./presentation-builder-model";

export function DashboardCanvas({
  artifact,
  results,
  loadingIDs,
  selection,
  previewMode,
  findings,
  onSelect,
  onLayoutCommit,
  onVisualizationChange,
  onDuplicate,
  onDelete,
  onAdd,
  onDropVisualization,
}: {
  artifact: PresentationArtifact;
  results: Record<string, PresentationDatasetResult>;
  loadingIDs: ReadonlySet<string>;
  selection: PresentationBuilderSelection;
  previewMode: PresentationPreviewMode;
  findings: PresentationFinding[];
  onSelect: (selection: PresentationBuilderSelection) => void;
  onLayoutCommit: (layout: PresentationLayoutItem[]) => void;
  onVisualizationChange: (visualization: PresentationVisualization) => void;
  onDuplicate: (visualizationID: string) => void;
  onDelete: (visualizationID: string) => void;
  onAdd: () => void;
  onDropVisualization: (
    type: ChartType,
    placement?: { x: number; y: number; width: number; height: number },
  ) => void;
}) {
  const visualizations = artifact.visualizations ?? [];
  const layout = useMemo(() => normalizedDashboardLayout(artifact), [artifact]);
  const { width, containerRef, mounted } = useContainerWidth({ initialWidth: 960 });
  const [announcement, setAnnouncement] = useState("");
  const [dropActive, setDropActive] = useState(false);
  const commitGrid = (next: Layout, message: string) => {
    onLayoutCommit(presentationLayoutFromGrid(next));
    setAnnouncement(message);
  };

  if (visualizations.length === 0) {
    return (
      <div
        data-testid="dashboard-canvas"
        className={cn(
          "flex min-h-[28rem] items-center justify-center rounded-2xl border border-dashed bg-card/70 p-8 text-center shadow-xs transition-colors",
          dropActive && "border-primary bg-primary/5",
        )}
        onDragEnter={(event) => {
          if (!hasAuthoringDragItem(event)) return;
          event.preventDefault();
          setDropActive(true);
        }}
        onDragOver={(event) => {
          if (!hasAuthoringDragItem(event)) return;
          event.preventDefault();
          event.dataTransfer.dropEffect = "copy";
        }}
        onDragLeave={(event) => {
          if (event.currentTarget.contains(event.relatedTarget as Node | null)) return;
          setDropActive(false);
        }}
        onDrop={(event) => {
          const item = readAuthoringDragItem(event);
          if (!item) return;
          event.preventDefault();
          setDropActive(false);
          if (item.kind !== "visualization") return;
          const size = droppedVisualizationSize(item.chartType);
          onDropVisualization(item.chartType, { x: 0, y: 0, ...size });
        }}
      >
        <div className="flex max-w-sm flex-col items-center gap-3">
          <span className="flex size-11 items-center justify-center rounded-xl bg-primary/10 text-primary">
            <Plus className="size-5" />
          </span>
          <div>
            <p className="text-sm font-medium">Build the first view</p>
            <p className="mt-1 text-xs text-muted-foreground">
              Add a chart, KPI, or table. The canvas becomes the saved dashboard layout.
            </p>
          </div>
          <Button onClick={onAdd} disabled={(artifact.datasets ?? []).length === 0}>
            <Plus /> Add visualization
          </Button>
        </div>
      </div>
    );
  }

  if (previewMode !== "desktop") {
    const columns = previewMode === "tablet" ? "grid-cols-2" : "grid-cols-1";
    return (
      <div
        data-testid="dashboard-canvas"
        className={cn("grid gap-3", columns, dropActive && "rounded-xl ring-2 ring-primary/30")}
        onDragOver={(event) => {
          if (!hasAuthoringDragItem(event)) return;
          event.preventDefault();
          event.dataTransfer.dropEffect = "copy";
          setDropActive(true);
        }}
        onDragLeave={() => setDropActive(false)}
        onDrop={(event) => {
          const item = readAuthoringDragItem(event);
          if (!item) return;
          event.preventDefault();
          setDropActive(false);
          if (item.kind !== "visualization") return;
          onDropVisualization(item.chartType);
        }}
      >
        {layout.map((item) => {
          const visualization = visualizations.find((candidate) => candidate.id === item.i);
          if (!visualization) return null;
          return (
            <div
              key={visualization.id}
              className={cn(
                previewMode === "tablet" &&
                  derivedDashboardSpan(item.w, previewMode) === 2 &&
                  "col-span-2",
              )}
            >
              <EditableVisualizationCard
                visualization={visualization}
                result={results[visualization.id]}
                loading={loadingIDs.has(visualization.id)}
                selected={selection.kind === "visualization" && selection.id === visualization.id}
                findings={findingsForVisualization(artifact, findings, visualization.id)}
                onSelect={() => onSelect({ kind: "visualization", id: visualization.id })}
                onChange={onVisualizationChange}
                onDuplicate={() => onDuplicate(visualization.id)}
                onDelete={() => onDelete(visualization.id)}
              />
            </div>
          );
        })}
      </div>
    );
  }

  return (
    <div ref={containerRef} data-testid="dashboard-canvas" className="min-w-0">
      {mounted ? (
        <GridLayout
          width={width}
          layout={layout}
          gridConfig={{ cols: 12, rowHeight: 72, margin: [12, 12], containerPadding: [0, 0] }}
          dragConfig={{
            enabled: true,
            bounded: true,
            handle: ".presentation-drag-handle",
            cancel: ".presentation-grid-interactive",
          }}
          resizeConfig={{ enabled: true, handles: ["se"] }}
          dropConfig={{
            enabled: true,
            defaultItem: { w: 6, h: 4 },
            onDragOver: (event) => {
              if (!hasAuthoringDragItem(event)) return false;
              return { w: 6, h: 4 };
            },
          }}
          compactor={verticalCompactor}
          onDragStop={(next, _previous, item) =>
            commitGrid(
              next,
              item
                ? `Moved ${item.i} to column ${item.x + 1}, row ${item.y + 1}.`
                : "Dashboard layout updated.",
            )
          }
          onResizeStop={(next, _previous, item) =>
            commitGrid(
              next,
              item
                ? `Resized ${item.i} to ${item.w} columns by ${item.h} rows.`
                : "Dashboard layout updated.",
            )
          }
          onDrop={(_next, item, event) => {
            const payload = readAuthoringDragItem(event as DragEvent);
            if (!payload) return;
            event.preventDefault();
            if (payload.kind !== "visualization") return;
            const size = droppedVisualizationSize(payload.chartType);
            onDropVisualization(payload.chartType, {
              x: item?.x ?? 0,
              y: item?.y ?? 0,
              width: item?.w ?? size.width,
              height: item?.h ?? size.height,
            });
          }}
          className="[&_.react-grid-item]:motion-reduce:!transition-none [&_.react-grid-item.react-grid-placeholder]:!rounded-xl [&_.react-grid-item.react-grid-placeholder]:!bg-primary [&_.react-grid-item.react-grid-placeholder]:!opacity-15"
        >
          {visualizations.map((visualization) => (
            <div key={visualization.id} className="min-w-0">
              <EditableVisualizationCard
                visualization={visualization}
                result={results[visualization.id]}
                loading={loadingIDs.has(visualization.id)}
                selected={selection.kind === "visualization" && selection.id === visualization.id}
                findings={findingsForVisualization(artifact, findings, visualization.id)}
                onSelect={() => onSelect({ kind: "visualization", id: visualization.id })}
                onChange={onVisualizationChange}
                onDuplicate={() => onDuplicate(visualization.id)}
                onDelete={() => onDelete(visualization.id)}
              />
            </div>
          ))}
        </GridLayout>
      ) : (
        <div className="min-h-[24rem] rounded-2xl border bg-card/50" />
      )}
      <p className="sr-only" aria-live="polite">
        {announcement}
      </p>
    </div>
  );
}

function droppedVisualizationSize(type: ChartType) {
  return type === "kpi"
    ? { width: 3, height: 3 }
    : type === "table"
      ? { width: 12, height: 4 }
      : { width: 6, height: 4 };
}

function EditableVisualizationCard({
  visualization,
  result,
  loading,
  selected,
  findings,
  onSelect,
  onChange,
  onDuplicate,
  onDelete,
}: {
  visualization: PresentationVisualization;
  result?: PresentationDatasetResult;
  loading: boolean;
  selected: boolean;
  findings: PresentationFinding[];
  onSelect: () => void;
  onChange: (visualization: PresentationVisualization) => void;
  onDuplicate: () => void;
  onDelete: () => void;
}) {
  const definition = normalizeVisualizationDefinition(visualization.definition);
  return (
    <ContextMenu>
      <ContextMenuTrigger
        role="group"
        tabIndex={0}
        aria-label={`Visualization ${definition.title || visualization.id}`}
        data-testid={`dashboard-visualization-${visualization.id}`}
        className={cn(
          "block h-full min-w-0 rounded-xl outline-none ring-offset-background transition-shadow focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2",
          selected && "ring-2 ring-primary ring-offset-2",
        )}
        onClick={onSelect}
        onFocus={(event) => {
          if (event.target === event.currentTarget) onSelect();
        }}
      >
        <PresentationVisualizationCard
          visualization={visualization}
          result={result}
          loading={loading}
          minHeight={0}
          header={
            <div className="flex min-w-0 flex-1 items-center gap-1.5">
              <button
                type="button"
                aria-label={`Move ${definition.title || visualization.id}`}
                title="Drag to move"
                className="presentation-drag-handle flex size-7 shrink-0 cursor-grab items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground active:cursor-grabbing"
                onClick={(event) => event.stopPropagation()}
              >
                <GripHorizontal className="size-4" />
              </button>
              <Input
                aria-label={`Title for ${visualization.id}`}
                value={definition.title ?? ""}
                placeholder={visualization.id}
                className="presentation-grid-interactive h-7 min-w-0 flex-1 border-transparent bg-transparent px-1 text-sm font-medium shadow-none hover:border-input focus-visible:border-input"
                onClick={(event) => event.stopPropagation()}
                onChange={(event) =>
                  onChange({
                    ...visualization,
                    definition: visualizationDefinitionRecord({
                      ...definition,
                      title: event.target.value,
                    }),
                  })
                }
              />
              {findings.length > 0 ? (
                <Badge variant="outline" className="shrink-0 border-amber-500/35 text-amber-700">
                  <TriangleAlert /> {findings.length}
                </Badge>
              ) : null}
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    size="icon-xs"
                    variant="ghost"
                    className="presentation-grid-interactive shrink-0"
                    aria-label={`More actions for ${visualization.id}`}
                    onClick={(event) => event.stopPropagation()}
                  >
                    <MoreHorizontal />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" onClick={(event) => event.stopPropagation()}>
                  <DropdownMenuItem onSelect={onDuplicate}>
                    <Copy /> Duplicate
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem variant="destructive" onSelect={onDelete}>
                    <Trash2 /> Delete
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          }
        />
      </ContextMenuTrigger>
      <ContextMenuContent>
        <ContextMenuItem onSelect={onDuplicate}>
          <Copy /> Duplicate
        </ContextMenuItem>
        <ContextMenuSeparator />
        <ContextMenuItem variant="destructive" onSelect={onDelete}>
          <Trash2 /> Delete
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

function findingsForVisualization(
  artifact: PresentationArtifact,
  findings: PresentationFinding[],
  visualizationID: string,
) {
  const index = (artifact.visualizations ?? []).findIndex(
    (visualization) => visualization.id === visualizationID,
  );
  if (index < 0) return [];
  return findings.filter((finding) => finding.path?.startsWith(`visualizations[${index}]`));
}
