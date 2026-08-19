"use client";

import {
  ArrowDown,
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  FileText,
  LayoutGrid,
  SlidersHorizontal,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import { useEffect, useId, useRef, type ReactNode } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import type {
  PresentationArtifact,
  PresentationFinding,
  PresentationSection,
} from "@/lib/api-presentations";
import type { WorkspaceState } from "@/lib/types";

import {
  DatasetEditor,
  FilterEditor,
  VisualizationEditor,
  datasetColumns,
  renameDataset,
  renameFilter,
  renameVisualization,
  type AssetChoice,
} from "../presentation-visual-editor";
import {
  normalizedDashboardLayout,
  updateDashboardLayoutItem,
  type PresentationBuilderSelection,
} from "./presentation-builder-model";

export function PresentationInspector({
  artifact,
  workspace,
  assetChoices,
  selection,
  findings,
  focusPath,
  onFocusPathHandled,
  onSelect,
  onChange,
  onDeleteVisualization,
}: {
  artifact: PresentationArtifact;
  workspace: WorkspaceState | null;
  assetChoices: AssetChoice[];
  selection: PresentationBuilderSelection;
  findings: PresentationFinding[];
  focusPath?: string | null;
  onFocusPathHandled?: () => void;
  onSelect: (selection: PresentationBuilderSelection) => void;
  onChange: (artifact: PresentationArtifact, options?: { coalesceKey?: string }) => void;
  onDeleteVisualization: (visualizationID: string) => void;
}) {
  const datasets = artifact.datasets ?? [];
  const filters = artifact.filters ?? [];
  const visualizations = artifact.visualizations ?? [];
  const sections = artifact.sections ?? [];

  if (selection.kind === "dataset") {
    const index = datasets.findIndex((dataset) => dataset.id === selection.id);
    const dataset = datasets[index];
    if (dataset) {
      const referenced = visualizations.some(
        (visualization) => visualization.dataset === dataset.id,
      );
      return (
        <InspectorFrame
          icon={SlidersHorizontal}
          title="Dataset"
          subtitle="Configure the selected data source."
          findings={findingsForPath(findings, `datasets.${dataset.id}`)}
          path={`datasets.${dataset.id}`}
          focusPath={focusPath}
          onFocusPathHandled={onFocusPathHandled}
        >
          <DatasetEditor
            dataset={dataset}
            assetChoices={assetChoices}
            connections={workspace?.query_connections ?? []}
            workspace={workspace}
            presentationId={artifact.id}
            referenced={referenced}
            compact
            pathPrefix={`datasets.${dataset.id}`}
            onChange={(next) => {
              const updated = [...datasets];
              updated[index] = next;
              onChange({ ...artifact, datasets: updated });
            }}
            onRename={(id) => {
              onChange(
                { ...artifact, ...renameDataset(artifact, dataset.id, id, index) },
                {
                  coalesceKey: `dataset:${dataset.id}:id`,
                },
              );
              onSelect({ kind: "dataset", id });
            }}
            onDelete={() => {
              onChange({
                ...artifact,
                datasets: datasets.filter((_, candidate) => candidate !== index),
              });
              onSelect({ kind: "artifact" });
            }}
          />
        </InspectorFrame>
      );
    }
  }

  if (selection.kind === "filter") {
    const index = filters.findIndex((filter) => filter.id === selection.id);
    const filter = filters[index];
    if (filter) {
      return (
        <InspectorFrame
          icon={SlidersHorizontal}
          title="Control"
          subtitle="Edit its default, options, and bindings."
          findings={findingsForIndexedPath(findings, "filters", index)}
          path={`filters[${index}]`}
          focusPath={focusPath}
          onFocusPathHandled={onFocusPathHandled}
        >
          <FilterEditor
            filter={filter}
            datasets={datasets}
            columnsForDataset={(datasetID) => datasetColumns(datasetID, datasets, assetChoices)}
            pathPrefix={`filters[${index}]`}
            onChange={(next) => {
              const updated = [...filters];
              updated[index] = next;
              onChange({ ...artifact, filters: updated });
            }}
            onRename={(id) => {
              onChange(
                { ...artifact, ...renameFilter(artifact, filter.id, id, index) },
                {
                  coalesceKey: `filter:${filter.id}:id`,
                },
              );
              onSelect({ kind: "filter", id });
            }}
            onDelete={() => {
              onChange({
                ...artifact,
                filters: filters.filter((_, candidate) => candidate !== index),
                visualizations: visualizations.map((visualization) => ({
                  ...visualization,
                  filter_bindings: visualization.filter_bindings?.filter(
                    (binding) => binding.filter !== filter.id,
                  ),
                })),
              });
              onSelect({ kind: "artifact" });
            }}
          />
        </InspectorFrame>
      );
    }
  }

  if (selection.kind === "visualization") {
    const index = visualizations.findIndex((visualization) => visualization.id === selection.id);
    const visualization = visualizations[index];
    if (visualization) {
      return (
        <InspectorFrame
          icon={SlidersHorizontal}
          title="Visualization"
          subtitle="Data, appearance, and interaction settings."
          findings={findingsForIndexedPath(findings, "visualizations", index)}
          path={`visualizations[${index}]`}
          focusPath={focusPath}
          onFocusPathHandled={onFocusPathHandled}
        >
          <VisualizationEditor
            visualization={visualization}
            datasets={datasets}
            filters={filters}
            columns={datasetColumns(visualization.dataset, datasets, assetChoices)}
            compact
            pathPrefix={`visualizations[${index}]`}
            onChange={(next) => {
              const updated = [...visualizations];
              updated[index] = next;
              onChange({ ...artifact, visualizations: updated });
            }}
            onRename={(id) => {
              onChange(
                { ...artifact, ...renameVisualization(artifact, visualization.id, id, index) },
                { coalesceKey: `visualization:${visualization.id}:id` },
              );
              onSelect({ kind: "visualization", id });
            }}
            onDelete={() => onDeleteVisualization(visualization.id)}
          />
          {artifact.kind === "dashboard" ? (
            <VisualizationLayoutControls
              artifact={artifact}
              visualizationID={visualization.id}
              pathPrefix={layoutPathForVisualization(artifact, visualization.id)}
              onChange={onChange}
            />
          ) : null}
        </InspectorFrame>
      );
    }
  }

  if (selection.kind === "section") {
    const index = sections.findIndex((section) => section.id === selection.id);
    const section = sections[index];
    if (section) {
      return (
        <InspectorFrame
          icon={FileText}
          title="Report block"
          subtitle="Identity, content source, and print behavior."
          findings={findingsForIndexedPath(findings, "sections", index)}
          path={`sections[${index}]`}
          focusPath={focusPath}
          onFocusPathHandled={onFocusPathHandled}
        >
          <SectionInspector
            section={section}
            pathPrefix={`sections[${index}]`}
            visualizations={visualizations.map((visualization) => visualization.id)}
            onEditVisualization={(visualization) =>
              onSelect({ kind: "visualization", id: visualization })
            }
            onChange={(next, coalesceKey) => {
              const updated = [...sections];
              updated[index] = next;
              onChange(
                { ...artifact, sections: updated },
                coalesceKey ? { coalesceKey } : undefined,
              );
              if (next.id !== section.id) onSelect({ kind: "section", id: next.id });
            }}
            onDelete={() => {
              onChange({
                ...artifact,
                sections: sections.filter((_, candidate) => candidate !== index),
              });
              onSelect({ kind: "artifact" });
            }}
          />
        </InspectorFrame>
      );
    }
  }

  return (
    <InspectorFrame
      icon={SlidersHorizontal}
      title="Presentation"
      subtitle="Artifact settings and checker findings."
      findings={findings}
      path=""
      focusPath={focusPath}
      onFocusPathHandled={onFocusPathHandled}
    >
      <FieldGroup>
        <Field data-presentation-path="title">
          <FieldLabel htmlFor="presentation-builder-title">Title</FieldLabel>
          <Input
            id="presentation-builder-title"
            value={artifact.title}
            onChange={(event) =>
              onChange(
                { ...artifact, title: event.target.value },
                { coalesceKey: "artifact:title" },
              )
            }
          />
        </Field>
        <Field data-presentation-path="id">
          <FieldLabel>Artifact ID</FieldLabel>
          <Input value={artifact.id} readOnly className="font-mono text-xs" />
          <FieldDescription>
            Stable identity stays unchanged when the display title changes.
          </FieldDescription>
        </Field>
      </FieldGroup>
    </InspectorFrame>
  );
}

function InspectorFrame({
  icon: Icon,
  title,
  subtitle,
  findings,
  path,
  focusPath,
  onFocusPathHandled,
  children,
}: {
  icon: typeof SlidersHorizontal;
  title: string;
  subtitle: string;
  findings: PresentationFinding[];
  path: string;
  focusPath?: string | null;
  onFocusPathHandled?: () => void;
  children: ReactNode;
}) {
  const frameRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const frame = frameRef.current;
    if (!frame || focusPath === null || focusPath === undefined) return;
    // Radix returns focus to the menu trigger as the findings menu closes.
    // Focus the inspector on the following task so the requested field wins.
    const timer = window.setTimeout(() => {
      const target = findingFocusTarget(frame, focusPath);
      if (target) {
        target.scrollIntoView({ block: "nearest", behavior: "smooth" });
        target.focus({ preventScroll: true });
      }
      onFocusPathHandled?.();
    });
    return () => window.clearTimeout(timer);
  }, [focusPath, onFocusPathHandled]);

  return (
    <div
      ref={frameRef}
      data-presentation-path={path}
      data-testid="presentation-inspector"
      className="flex min-w-0 flex-col gap-4 overflow-x-hidden p-3"
    >
      <div className="flex items-start gap-2">
        <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
          <Icon className="size-3.5" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <p className="truncate text-sm font-medium">{title}</p>
            {findings.length > 0 ? (
              <Badge variant="outline" className="ml-auto border-amber-500/35 font-normal">
                {findings.length}
              </Badge>
            ) : null}
          </div>
          <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">{subtitle}</p>
        </div>
      </div>
      <Separator />
      {children}
      {findings.length > 0 ? (
        <div className="flex flex-col gap-2">
          {findings.map((finding, index) => (
            <Alert
              key={`${finding.code}:${finding.path ?? ""}:${index}`}
              variant={finding.severity === "error" ? "destructive" : "default"}
            >
              <TriangleAlert />
              <AlertTitle>{finding.severity === "error" ? "Needs attention" : "Review"}</AlertTitle>
              <AlertDescription>{finding.message}</AlertDescription>
            </Alert>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function VisualizationLayoutControls({
  artifact,
  visualizationID,
  pathPrefix,
  onChange,
}: {
  artifact: PresentationArtifact;
  visualizationID: string;
  pathPrefix?: string;
  onChange: (artifact: PresentationArtifact) => void;
}) {
  const layout = normalizedDashboardLayout(artifact);
  const item = layout.find((candidate) => candidate.i === visualizationID);
  if (!item) return null;
  const apply = (patch: Partial<{ x: number; y: number; w: number; h: number }>) =>
    onChange({
      ...artifact,
      layout: updateDashboardLayoutItem(layout, visualizationID, patch),
    });
  return (
    <div
      data-presentation-path={pathPrefix}
      className="flex flex-col gap-3 rounded-lg border bg-muted/15 p-3"
    >
      <div className="flex items-center gap-2 text-xs font-medium">
        <LayoutGrid className="size-3.5 text-primary" /> Layout
      </div>
      <Field data-presentation-path={pathPrefix ? `${pathPrefix}.width` : undefined}>
        <FieldLabel>Width</FieldLabel>
        <Select value={String(item.w)} onValueChange={(value) => apply({ w: Number(value) })}>
          <SelectTrigger className="w-full" aria-label="Visualization width">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {[3, 4, 6, 8, 9, 12].map((width) => (
              <SelectItem key={width} value={String(width)}>
                {width}/12 columns
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <Field data-presentation-path={pathPrefix ? `${pathPrefix}.height` : undefined}>
        <FieldLabel htmlFor={`visualization-height-${visualizationID}`}>Height</FieldLabel>
        <Input
          id={`visualization-height-${visualizationID}`}
          type="number"
          min={2}
          max={20}
          value={item.h}
          onChange={(event) => apply({ h: Number(event.target.value) || 2 })}
        />
      </Field>
      <div className="grid grid-cols-4 gap-1">
        <Button
          data-presentation-path={pathPrefix ? `${pathPrefix}.x` : undefined}
          variant="outline"
          size="icon-sm"
          aria-label="Move left"
          disabled={item.x <= 0}
          onClick={() => apply({ x: item.x - 1 })}
        >
          <ArrowLeft />
        </Button>
        <Button
          data-presentation-path={pathPrefix ? `${pathPrefix}.y` : undefined}
          variant="outline"
          size="icon-sm"
          aria-label="Move up"
          disabled={item.y <= 0}
          onClick={() => apply({ y: item.y - 1 })}
        >
          <ArrowUp />
        </Button>
        <Button
          data-presentation-path={pathPrefix ? `${pathPrefix}.y` : undefined}
          variant="outline"
          size="icon-sm"
          aria-label="Move down"
          onClick={() => apply({ y: item.y + 1 })}
        >
          <ArrowDown />
        </Button>
        <Button
          data-presentation-path={pathPrefix ? `${pathPrefix}.x` : undefined}
          variant="outline"
          size="icon-sm"
          aria-label="Move right"
          disabled={item.x + item.w >= 12}
          onClick={() => apply({ x: item.x + 1 })}
        >
          <ArrowRight />
        </Button>
      </div>
      <FieldDescription>
        Drag and resize on the desktop canvas, or use these controls.
      </FieldDescription>
    </div>
  );
}

function SectionInspector({
  section,
  pathPrefix,
  visualizations,
  onEditVisualization,
  onChange,
  onDelete,
}: {
  section: PresentationSection;
  pathPrefix: string;
  visualizations: string[];
  onEditVisualization: (visualization: string) => void;
  onChange: (section: PresentationSection, coalesceKey?: string) => void;
  onDelete: () => void;
}) {
  const sectionIDInputID = useId();

  return (
    <FieldGroup>
      <Field data-presentation-path={`${pathPrefix}.id`}>
        <FieldLabel htmlFor={sectionIDInputID}>Section ID</FieldLabel>
        <Input
          id={sectionIDInputID}
          value={section.id}
          className="font-mono text-xs"
          onChange={(event) =>
            onChange({ ...section, id: event.target.value }, `section:${section.id}:id`)
          }
        />
      </Field>
      {section.visualization !== undefined ? (
        <>
          <Field data-presentation-path={`${pathPrefix}.visualization`}>
            <FieldLabel>Visualization</FieldLabel>
            <Select
              value={section.visualization}
              onValueChange={(visualization) => onChange({ ...section, visualization })}
            >
              <SelectTrigger className="w-full">
                <SelectValue placeholder="Choose a visualization" />
              </SelectTrigger>
              <SelectContent>
                {visualizations.map((visualization) => (
                  <SelectItem key={visualization} value={visualization}>
                    {visualization}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          {section.visualization ? (
            <Button
              variant="outline"
              onClick={() => onEditVisualization(section.visualization ?? "")}
            >
              <SlidersHorizontal /> Edit visualization settings
            </Button>
          ) : null}
        </>
      ) : (
        <p className="text-xs text-muted-foreground">
          Edit the narrative directly on the report page. The full Markdown source is shown while
          this block is selected.
        </p>
      )}
      <label
        data-presentation-path={`${pathPrefix}.page_break`}
        className="flex items-center gap-2 text-xs"
      >
        <Checkbox
          checked={section.page_break === true}
          onCheckedChange={(checked) => onChange({ ...section, page_break: checked === true })}
        />
        Start a new printed page after this block
      </label>
      <Button variant="outline" className="text-destructive" onClick={onDelete}>
        <Trash2 /> Delete block
      </Button>
    </FieldGroup>
  );
}

function findingsForPath(findings: PresentationFinding[], path: string) {
  return findings.filter((finding) => finding.path?.startsWith(path));
}

function findingsForIndexedPath(findings: PresentationFinding[], prefix: string, index: number) {
  return findingsForPath(findings, `${prefix}[${index}]`);
}

function layoutPathForVisualization(artifact: PresentationArtifact, visualizationID: string) {
  const index = artifact.layout?.findIndex((item) => item.visualization === visualizationID) ?? -1;
  return index >= 0 ? `layout[${index}]` : undefined;
}

function findingFocusTarget(frame: HTMLElement, path: string): HTMLElement | null {
  for (const candidate of findingPathAncestors(path)) {
    const container = frame.querySelector<HTMLElement>(
      `[data-presentation-path="${CSS.escape(candidate)}"]`,
    );
    if (!container) continue;
    if (isFocusable(container)) return container;
    const focusable = container.querySelector<HTMLElement>(
      'input:not([disabled]), textarea:not([disabled]), button:not([disabled]), [tabindex]:not([tabindex="-1"])',
    );
    if (focusable) return focusable;
  }
  return frame.querySelector<HTMLElement>(
    'input:not([disabled]), textarea:not([disabled]), button:not([disabled]), [tabindex]:not([tabindex="-1"])',
  );
}

function findingPathAncestors(path: string): string[] {
  const result = [path];
  let current = path;
  while (current) {
    if (/\[\d+\]$/.test(current)) {
      current = current.replace(/\[\d+\]$/, "");
    } else {
      const dot = current.lastIndexOf(".");
      current = dot >= 0 ? current.slice(0, dot) : "";
    }
    if (!result.includes(current)) result.push(current);
  }
  return result;
}

function isFocusable(element: HTMLElement) {
  return element.matches(
    'input:not([disabled]), textarea:not([disabled]), button:not([disabled]), [tabindex]:not([tabindex="-1"])',
  );
}
