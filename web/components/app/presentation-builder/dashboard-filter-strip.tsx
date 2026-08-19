"use client";

import { Filter, Plus } from "lucide-react";
import { useState } from "react";

import { hasAuthoringDragItem, readAuthoringDragItem } from "@/components/app/authoring-drag";
import { PresentationFilterControl } from "@/components/app/presentation-viewer";
import { Button } from "@/components/ui/button";
import type { PresentationDatasetResult, PresentationFilter } from "@/lib/api-presentations";
import type { AuthoredControlType } from "@/lib/authored-controls";
import { cn } from "@/lib/utils";

import type { PresentationBuilderSelection } from "./presentation-builder-model";

export function DashboardFilterStrip({
  filters,
  values,
  optionResults,
  selection,
  onValueChange,
  onSelect,
  onAdd,
  onDropControl,
}: {
  filters: PresentationFilter[];
  values: Record<string, unknown>;
  optionResults: Record<string, PresentationDatasetResult>;
  selection: PresentationBuilderSelection;
  onValueChange: (id: string, value: unknown) => void;
  onSelect: (selection: PresentationBuilderSelection) => void;
  onAdd: () => void;
  onDropControl: (type: AuthoredControlType) => void;
}) {
  const [dropActive, setDropActive] = useState(false);
  return (
    <div
      data-testid="presentation-control-strip"
      className={cn(
        "flex min-w-0 flex-wrap items-end gap-2 rounded-xl border bg-card/80 p-2.5 shadow-xs transition-colors",
        dropActive && "border-primary bg-primary/5 ring-2 ring-primary/20",
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
        if (item.kind !== "control") return;
        onDropControl(item.controlType);
      }}
    >
      <div className="mr-1 flex h-8 items-center gap-1.5 px-1 text-xs font-medium text-muted-foreground">
        <Filter className="size-3.5" /> Controls
      </div>
      {filters.map((filter) => (
        <div
          key={filter.id}
          className={cn(
            "rounded-lg p-1 transition-shadow",
            selection.kind === "filter" &&
              selection.id === filter.id &&
              "bg-primary/5 ring-2 ring-primary",
          )}
          onClickCapture={() => onSelect({ kind: "filter", id: filter.id })}
        >
          <PresentationFilterControl
            filter={filter}
            value={values[filter.id]}
            optionResult={optionResults[filter.id]}
            onChange={(value) => onValueChange(filter.id, value)}
          />
        </div>
      ))}
      <Button size="sm" variant="ghost" className="ml-auto" onClick={onAdd}>
        <Plus /> Add control
      </Button>
    </div>
  );
}
