"use client";

import { BarChart3, Database, FileText, Filter, Plus } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { PresentationArtifact } from "@/lib/api-presentations";
import type { AuthoredControlType } from "@/lib/authored-controls";
import { cn } from "@/lib/utils";

import { datasetColumns, type AssetChoice } from "../presentation-visual-editor";
import { ChartTypePicker } from "../chart-type-picker";
import { ControlTypePicker } from "../control-type-picker";
import { DocumentAuthoringSidebar, type DocumentAuthoringTab } from "../document-authoring-sidebar";
import type { PresentationBuilderSelection } from "./presentation-builder-model";

export function PresentationSidebar({
  artifact,
  assetChoices,
  selection,
  onSelect,
  onAddDataset,
  onAddFilter,
  onAddControl,
  onAddVisualization,
  onAddText,
}: {
  artifact: PresentationArtifact;
  assetChoices: AssetChoice[];
  selection: PresentationBuilderSelection;
  onSelect: (selection: PresentationBuilderSelection) => void;
  onAddDataset: () => void;
  onAddFilter: () => void;
  onAddControl: (type: AuthoredControlType) => void;
  onAddVisualization: (preferredType?: string) => void;
  onAddText: () => void;
}) {
  const isReport = artifact.kind === "report";
  const tabs: DocumentAuthoringTab[] = [];
  if (isReport) {
    tabs.push({
      value: "outline",
      label: "Outline",
      content: (
        <ReportOutline
          artifact={artifact}
          selection={selection}
          onSelect={onSelect}
          onAddText={onAddText}
          onAddVisualization={onAddVisualization}
        />
      ),
    });
  }
  tabs.push({
    value: "add",
    label: "Add",
    content: (
      <ComponentPalette
        isReport={isReport}
        hasDatasets={(artifact.datasets ?? []).length > 0}
        onAddDataset={onAddDataset}
        onAddFilter={onAddFilter}
        onAddControl={onAddControl}
        onAddText={onAddText}
        onAddVisualization={onAddVisualization}
      />
    ),
  });
  tabs.push({
    value: "data",
    label: "Data",
    content: (
      <PresentationDataList
        artifact={artifact}
        assetChoices={assetChoices}
        selection={selection}
        onSelect={onSelect}
        onAddDataset={onAddDataset}
      />
    ),
  });

  return (
    <DocumentAuthoringSidebar
      label={`${isReport ? "Report" : "Dashboard"} authoring tools`}
      defaultValue={isReport ? "outline" : "add"}
      tabs={tabs}
    />
  );
}

function PresentationDataList({
  artifact,
  assetChoices,
  selection,
  onSelect,
  onAddDataset,
}: {
  artifact: PresentationArtifact;
  assetChoices: AssetChoice[];
  selection: PresentationBuilderSelection;
  onSelect: (selection: PresentationBuilderSelection) => void;
  onAddDataset: () => void;
}) {
  return (
    <div className="flex flex-col gap-2 p-2">
      <Button size="sm" variant="outline" className="w-full" onClick={onAddDataset}>
        <Plus /> Add dataset
      </Button>
      {(artifact.datasets ?? []).map((dataset) => {
        const columns = datasetColumns(dataset.id, artifact.datasets ?? [], assetChoices);
        const selected = selection.kind === "dataset" && selection.id === dataset.id;
        return (
          <button
            key={dataset.id}
            type="button"
            className={cn(
              "flex min-w-0 flex-col gap-2 rounded-lg border bg-card p-2.5 text-left transition-colors hover:bg-accent",
              selected && "border-primary/40 bg-primary/5 ring-1 ring-primary/20",
            )}
            onClick={() => onSelect({ kind: "dataset", id: dataset.id })}
          >
            <span className="flex min-w-0 items-center gap-2">
              <Database className="size-3.5 shrink-0 text-primary" />
              <span className="truncate text-xs font-medium">{dataset.id}</span>
              <Badge variant="outline" className="ml-auto shrink-0 font-normal">
                {columns.length} fields
              </Badge>
            </span>
            <span className="truncate text-[10px] text-muted-foreground">
              {dataset.asset || dataset.connection || "Source not configured"}
            </span>
            {columns.length > 0 ? (
              <span className="flex min-w-0 flex-wrap gap-1">
                {columns.slice(0, 5).map((column) => (
                  <span
                    key={column.name}
                    className="max-w-full truncate rounded bg-muted px-1.5 py-0.5 font-mono text-[9px] text-muted-foreground"
                  >
                    {column.name}
                  </span>
                ))}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}

function ComponentPalette({
  isReport,
  hasDatasets,
  onAddDataset,
  onAddFilter,
  onAddControl,
  onAddText,
  onAddVisualization,
}: {
  isReport: boolean;
  hasDatasets: boolean;
  onAddDataset: () => void;
  onAddFilter: () => void;
  onAddControl: (type: AuthoredControlType) => void;
  onAddText: () => void;
  onAddVisualization: (preferredType?: string) => void;
}) {
  return (
    <div className="flex flex-col gap-3 p-2">
      <Button
        size="sm"
        className="w-full"
        onClick={hasDatasets ? () => onAddVisualization() : onAddDataset}
      >
        <Plus /> {hasDatasets ? "Add visualization" : "Add dataset"}
      </Button>
      {isReport ? (
        <Button size="sm" variant="outline" className="w-full" onClick={onAddText}>
          <FileText /> Add text
        </Button>
      ) : null}
      <div className="flex flex-col gap-2">
        <p className="px-1 text-[11px] font-medium text-muted-foreground">Visualizations</p>
        <ChartTypePicker
          compact
          draggable
          disabled={!hasDatasets}
          onValueChange={(type) => onAddVisualization(type)}
        />
      </div>
      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between px-1">
          <p className="text-[11px] font-medium text-muted-foreground">Controls</p>
          <Button size="xs" variant="ghost" onClick={onAddFilter}>
            <Filter /> Add control
          </Button>
        </div>
        <ControlTypePicker draggable onValueChange={onAddControl} />
        <p className="px-1 text-[10px] leading-relaxed text-muted-foreground">
          Drag a typed input onto the control strip, or click to add it.
        </p>
      </div>
      {!hasDatasets ? (
        <p className="px-1 text-[11px] leading-relaxed text-muted-foreground">
          Add a dataset from the Data tab before creating a visualization.
        </p>
      ) : null}
    </div>
  );
}

function ReportOutline({
  artifact,
  selection,
  onSelect,
  onAddText,
  onAddVisualization,
}: {
  artifact: PresentationArtifact;
  selection: PresentationBuilderSelection;
  onSelect: (selection: PresentationBuilderSelection) => void;
  onAddText: () => void;
  onAddVisualization: (preferredType?: string) => void;
}) {
  return (
    <div className="flex flex-col gap-2 p-2">
      <div className="grid grid-cols-2 gap-1.5">
        <Button size="sm" variant="outline" onClick={onAddText}>
          <FileText /> Add text
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={(artifact.datasets ?? []).length === 0}
          onClick={() => onAddVisualization()}
        >
          <BarChart3 /> Add visualization
        </Button>
      </div>
      {(artifact.sections ?? []).map((section, index) => (
        <button
          key={section.id}
          type="button"
          className={cn(
            "flex min-w-0 items-center gap-2 rounded-md px-2 py-2 text-left text-xs transition-colors hover:bg-accent",
            selection.kind === "section" && selection.id === section.id && "bg-primary/10",
          )}
          onClick={() => onSelect({ kind: "section", id: section.id })}
        >
          {section.markdown !== undefined ? (
            <FileText className="size-3.5 shrink-0 text-muted-foreground" />
          ) : (
            <BarChart3 className="size-3.5 shrink-0 text-muted-foreground" />
          )}
          <span className="min-w-0 flex-1 truncate">
            {section.title || section.visualization || section.id}
          </span>
          <span className="font-mono text-[9px] text-muted-foreground">{index + 1}</span>
        </button>
      ))}
    </div>
  );
}
