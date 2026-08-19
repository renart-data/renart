"use client";

import {
  ArrowDown,
  ArrowUp,
  BarChart3,
  FileText,
  GripVertical,
  Plus,
  ScissorsLineDashed,
  SlidersHorizontal,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import { useState } from "react";

import { hasAuthoringDragItem, readAuthoringDragItem } from "@/components/app/authoring-drag";
import type { ChartType } from "@/components/app/chart-type-picker";
import { MarkdownEditor } from "@/components/app/markdown-editor";
import { PresentationVisualizationCard } from "@/components/app/presentation-viewer";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import type {
  PresentationArtifact,
  PresentationDatasetResult,
  PresentationFinding,
  PresentationSection,
} from "@/lib/api-presentations";
import { cn } from "@/lib/utils";

import type { PresentationBuilderSelection } from "./presentation-builder-model";

export function ReportCanvas({
  artifact,
  results,
  loadingIDs,
  selection,
  findings,
  onSelect,
  onSectionChange,
  onMove,
  onDelete,
  onInsert,
  onDropVisualization,
}: {
  artifact: PresentationArtifact;
  results: Record<string, PresentationDatasetResult>;
  loadingIDs: ReadonlySet<string>;
  selection: PresentationBuilderSelection;
  findings: PresentationFinding[];
  onSelect: (selection: PresentationBuilderSelection) => void;
  onSectionChange: (section: PresentationSection) => void;
  onMove: (from: number, to: number) => void;
  onDelete: (sectionID: string) => void;
  onInsert: (index: number, kind: "text" | "visualization" | "page_break") => void;
  onDropVisualization: (index: number, type: ChartType) => void;
}) {
  const sections = artifact.sections ?? [];
  const visualizations = new Map(
    (artifact.visualizations ?? []).map((visualization) => [visualization.id, visualization]),
  );
  const [draggingIndex, setDraggingIndex] = useState<number | null>(null);
  const [announcement, setAnnouncement] = useState("");
  const moveSection = (from: number, to: number) => {
    const section = sections[from];
    if (!section || from === to) return;
    onMove(from, to);
    setAnnouncement(
      `Moved report section ${section.title || section.id} to position ${to + 1} of ${sections.length}.`,
    );
  };

  return (
    <article
      data-testid="report-canvas"
      className="mx-auto min-h-[56rem] w-full max-w-[52rem] rounded-sm border bg-card px-6 py-8 shadow-lg sm:px-10"
      onDragOver={(event) => {
        if (!hasAuthoringDragItem(event)) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = "copy";
      }}
      onDrop={(event) => {
        const item = readAuthoringDragItem(event);
        if (!item) return;
        event.preventDefault();
        if (item.kind !== "visualization") return;
        onDropVisualization(sections.length, item.chartType);
      }}
    >
      <h1 className="mb-1 text-2xl font-semibold">{artifact.title}</h1>
      <p className="mb-8 text-xs text-muted-foreground">
        Git-native report · select a block to edit it
      </p>
      <InsertSectionButton
        index={0}
        disabledPageBreak
        onInsert={onInsert}
        onDropVisualization={onDropVisualization}
      />
      {sections.length === 0 ? (
        <div
          className="flex min-h-80 flex-col items-center justify-center gap-3 rounded-xl border border-dashed bg-muted/15 text-center transition-colors"
          onDragOver={(event) => {
            if (!hasAuthoringDragItem(event)) return;
            event.preventDefault();
            event.dataTransfer.dropEffect = "copy";
          }}
          onDrop={(event) => {
            const item = readAuthoringDragItem(event);
            if (!item) return;
            event.preventDefault();
            event.stopPropagation();
            if (item.kind !== "visualization") return;
            onDropVisualization(0, item.chartType);
          }}
        >
          <FileText className="size-7 text-muted-foreground" />
          <div>
            <p className="text-sm font-medium">Start the report</p>
            <p className="mt-1 text-xs text-muted-foreground">
              Build a narrative from text and checked visualizations.
            </p>
          </div>
          <Button onClick={() => onInsert(0, "text")}>
            <Plus /> Add text
          </Button>
        </div>
      ) : (
        <div className="flex flex-col gap-1">
          {sections.map((section, index) => {
            const visualization = section.visualization
              ? visualizations.get(section.visualization)
              : undefined;
            const sectionSelected = selection.kind === "section" && selection.id === section.id;
            const visualizationSelected =
              Boolean(visualization) &&
              selection.kind === "visualization" &&
              selection.id === visualization?.id;
            const selected = sectionSelected || visualizationSelected;
            const sectionFindings = findings.filter((finding) =>
              finding.path?.startsWith(`sections[${index}]`),
            );
            return (
              <div key={section.id}>
                <section
                  tabIndex={0}
                  aria-label={`Report section ${section.title || section.id}`}
                  draggable
                  onDragStart={() => setDraggingIndex(index)}
                  onDragEnd={() => setDraggingIndex(null)}
                  onDragOver={(event) => event.preventDefault()}
                  onDrop={(event) => {
                    event.preventDefault();
                    const item = readAuthoringDragItem(event);
                    if (item?.kind === "visualization") {
                      event.stopPropagation();
                      onDropVisualization(index, item.chartType);
                      setDraggingIndex(null);
                      return;
                    }
                    if (draggingIndex !== null && draggingIndex !== index)
                      moveSection(draggingIndex, index);
                    setDraggingIndex(null);
                  }}
                  onClick={() => onSelect({ kind: "section", id: section.id })}
                  onFocus={(event) => {
                    if (event.target === event.currentTarget)
                      onSelect({ kind: "section", id: section.id });
                  }}
                  onKeyDown={(event) => {
                    if (
                      event.target !== event.currentTarget ||
                      (event.key !== "Enter" && event.key !== " ")
                    )
                      return;
                    event.preventDefault();
                    onSelect({ kind: "section", id: section.id });
                  }}
                  className={cn(
                    "group relative rounded-xl border border-transparent px-3 py-4 transition-colors",
                    selected && "border-primary/35 bg-primary/[0.025] ring-2 ring-primary/20",
                    draggingIndex === index && "opacity-50",
                  )}
                >
                  <div className="absolute top-2 right-2 z-10 flex items-center gap-0.5 rounded-md border bg-background/90 p-0.5 opacity-0 shadow-xs transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      className="cursor-grab"
                      aria-label={`Drag section ${section.title || section.id}`}
                      title="Drag to reorder"
                    >
                      <GripVertical />
                    </Button>
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      disabled={index === 0}
                      aria-label="Move section up"
                      onClick={(event) => {
                        event.stopPropagation();
                        moveSection(index, index - 1);
                      }}
                    >
                      <ArrowUp />
                    </Button>
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      disabled={index === sections.length - 1}
                      aria-label="Move section down"
                      onClick={(event) => {
                        event.stopPropagation();
                        moveSection(index, index + 1);
                      }}
                    >
                      <ArrowDown />
                    </Button>
                    <Button
                      size="icon-xs"
                      variant="ghost"
                      aria-label={`Delete section ${section.id}`}
                      onClick={(event) => {
                        event.stopPropagation();
                        onDelete(section.id);
                      }}
                    >
                      <Trash2 />
                    </Button>
                  </div>
                  {sectionFindings.length > 0 ? (
                    <Badge
                      variant="outline"
                      className="absolute top-2 left-2 border-amber-500/35 text-amber-700"
                    >
                      <TriangleAlert /> {sectionFindings.length}
                    </Badge>
                  ) : null}
                  {section.markdown !== undefined ? (
                    <div className="flex flex-col gap-2 pt-2">
                      {sectionSelected ? (
                        <Input
                          aria-label="Section title"
                          value={section.title ?? ""}
                          placeholder="Section title"
                          className="h-auto border-transparent bg-transparent px-0 text-lg font-semibold shadow-none hover:border-input focus-visible:border-input"
                          onChange={(event) =>
                            onSectionChange({ ...section, title: event.target.value })
                          }
                        />
                      ) : section.title ? (
                        <h2 className="mb-1 px-3 text-lg font-semibold">{section.title}</h2>
                      ) : null}
                      <MarkdownEditor
                        value={section.markdown}
                        selected={sectionSelected}
                        ariaLabel="Section markdown"
                        placeholder="Write the report narrative…"
                        onChange={(markdown) => onSectionChange({ ...section, markdown })}
                      />
                    </div>
                  ) : visualization ? (
                    <div
                      role="button"
                      tabIndex={0}
                      aria-label={`Edit visualization ${visualization.id}`}
                      className={cn(
                        "group/chart relative rounded-lg pt-2 outline-none transition-shadow focus-visible:ring-2 focus-visible:ring-ring",
                        visualizationSelected && "ring-2 ring-primary/25",
                      )}
                      onClick={(event) => {
                        event.stopPropagation();
                        onSelect({ kind: "visualization", id: visualization.id });
                      }}
                      onKeyDown={(event) => {
                        if (event.key !== "Enter" && event.key !== " ") return;
                        event.preventDefault();
                        event.stopPropagation();
                        onSelect({ kind: "visualization", id: visualization.id });
                      }}
                    >
                      <span className="pointer-events-none absolute top-4 right-2 z-10 flex items-center gap-1 rounded-md border bg-background/90 px-2 py-1 text-[10px] font-medium opacity-0 shadow-xs transition-opacity group-hover/chart:opacity-100 group-focus-visible/chart:opacity-100">
                        <SlidersHorizontal className="size-3" /> Edit visualization
                      </span>
                      <PresentationVisualizationCard
                        visualization={visualization}
                        result={results[visualization.id]}
                        loading={loadingIDs.has(visualization.id)}
                      />
                    </div>
                  ) : (
                    <div className="rounded-lg border border-dashed p-6 text-center text-xs text-muted-foreground">
                      Choose a visualization in the inspector.
                    </div>
                  )}
                </section>
                {section.page_break ? (
                  <div className="my-2 flex items-center gap-2 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">
                    <span className="h-px flex-1 border-t border-dashed" />
                    <ScissorsLineDashed className="size-3" /> Page break
                    <span className="h-px flex-1 border-t border-dashed" />
                  </div>
                ) : null}
                <InsertSectionButton
                  index={index + 1}
                  onInsert={onInsert}
                  onDropVisualization={onDropVisualization}
                />
              </div>
            );
          })}
        </div>
      )}
      <p className="sr-only" aria-live="polite">
        {announcement}
      </p>
    </article>
  );
}

function InsertSectionButton({
  index,
  disabledPageBreak = false,
  onInsert,
  onDropVisualization,
}: {
  index: number;
  disabledPageBreak?: boolean;
  onInsert: (index: number, kind: "text" | "visualization" | "page_break") => void;
  onDropVisualization: (index: number, type: ChartType) => void;
}) {
  const [dropActive, setDropActive] = useState(false);
  return (
    <div
      className={cn(
        "group/insert flex h-7 items-center justify-center rounded-md transition-colors",
        dropActive && "bg-primary/10",
      )}
      onDragEnter={(event) => {
        if (!hasAuthoringDragItem(event)) return;
        event.preventDefault();
        setDropActive(true);
      }}
      onDragOver={(event) => {
        if (!hasAuthoringDragItem(event)) return;
        event.preventDefault();
        event.stopPropagation();
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
        event.stopPropagation();
        setDropActive(false);
        if (item.kind !== "visualization") return;
        onDropVisualization(index, item.chartType);
      }}
    >
      <span className="h-px flex-1 bg-border opacity-0 transition-opacity group-hover/insert:opacity-100" />
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            size="icon-xs"
            variant="outline"
            className="mx-2 opacity-0 transition-opacity group-hover/insert:opacity-100 focus:opacity-100 data-open:opacity-100"
            aria-label="Insert report block"
          >
            <Plus />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="center">
          <DropdownMenuItem onSelect={() => onInsert(index, "text")}>
            <FileText /> Text
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => onInsert(index, "visualization")}>
            <BarChart3 /> Visualization
          </DropdownMenuItem>
          <DropdownMenuItem
            disabled={disabledPageBreak}
            onSelect={() => onInsert(index, "page_break")}
          >
            <ScissorsLineDashed /> Page break
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <span className="h-px flex-1 bg-border opacity-0 transition-opacity group-hover/insert:opacity-100" />
    </div>
  );
}
