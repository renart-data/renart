"use client";

import { Table2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldLabel } from "@/components/ui/field";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type { PresentationDataset } from "@/lib/api-presentations";

import { ChartTypePreview, type ChartType } from "../chart-type-picker";
import { datasetColumns, type AssetChoice } from "../presentation-visual-editor";
import {
  visualizationSuggestions,
  type VisualizationSuggestion,
} from "./presentation-builder-model";

export function AddVisualizationDialog({
  open,
  datasets,
  assetChoices,
  preferredType,
  onOpenChange,
  onAdd,
}: {
  open: boolean;
  datasets: PresentationDataset[];
  assetChoices: AssetChoice[];
  preferredType?: string;
  onOpenChange: (open: boolean) => void;
  onAdd: (datasetID: string, suggestion: VisualizationSuggestion) => void;
}) {
  const [datasetID, setDatasetID] = useState(datasets[0]?.id ?? "");
  useEffect(() => {
    if (!open) return;
    if (!datasets.some((dataset) => dataset.id === datasetID)) setDatasetID(datasets[0]?.id ?? "");
  }, [datasetID, datasets, open]);
  const columns = useMemo(
    () => datasetColumns(datasetID, datasets, assetChoices),
    [assetChoices, datasetID, datasets],
  );
  const suggestions = useMemo(() => {
    const values = visualizationSuggestions(columns);
    if (!preferredType) return values;
    return [...values].sort(
      (left, right) => Number(right.type === preferredType) - Number(left.type === preferredType),
    );
  }, [columns, preferredType]);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[min(46rem,calc(100vh-2rem))] overflow-hidden sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>Add a visualization</DialogTitle>
          <DialogDescription>
            Choose a checked dataset. Renart suggests compatible starting points from its columns.
          </DialogDescription>
        </DialogHeader>
        {datasets.length === 0 ? (
          <div className="flex min-h-48 flex-col items-center justify-center gap-3 rounded-xl border border-dashed bg-muted/20 p-6 text-center">
            <Table2 className="size-7 text-muted-foreground" />
            <div>
              <p className="text-sm font-medium">Add a dataset first</p>
              <p className="mt-1 text-xs text-muted-foreground">
                Visualizations need an asset-backed or read-only query dataset.
              </p>
            </div>
          </div>
        ) : (
          <div className="flex min-h-0 flex-col gap-4">
            <Field>
              <FieldLabel>Dataset</FieldLabel>
              <Select value={datasetID} onValueChange={setDatasetID}>
                <SelectTrigger className="w-full sm:max-w-sm">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {datasets.map((dataset) => (
                    <SelectItem key={dataset.id} value={dataset.id}>
                      {dataset.id}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <div className="flex min-w-0 flex-wrap gap-1.5">
              {columns.slice(0, 12).map((column) => (
                <Badge key={column.name} variant="secondary" className="max-w-44 font-normal">
                  <span className="truncate">{column.name}</span>
                  <span className="text-muted-foreground">· {column.semantic_type}</span>
                </Badge>
              ))}
              {columns.length === 0 ? (
                <span className="text-xs text-muted-foreground">
                  No columns are known yet; start with a table and configure it in the inspector.
                </span>
              ) : null}
            </div>
            <div className="grid min-h-0 gap-2 overflow-y-auto pr-1 sm:grid-cols-2">
              {suggestions.map((suggestion) => {
                return (
                  <button
                    key={suggestion.key}
                    type="button"
                    className="group flex min-w-0 items-start gap-3 rounded-xl border bg-card p-3 text-left shadow-xs transition-colors hover:border-primary/35 hover:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                    onClick={() => {
                      onAdd(datasetID, suggestion);
                      onOpenChange(false);
                    }}
                  >
                    <span className="flex w-20 shrink-0 items-center justify-center rounded-lg bg-primary/5 px-1 text-primary">
                      <ChartTypePreview type={suggestion.type as ChartType} />
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="flex items-center gap-2">
                        <span className="truncate text-sm font-medium">{suggestion.title}</span>
                        <Badge variant="outline" className="ml-auto shrink-0 font-normal">
                          {suggestion.type}
                        </Badge>
                      </span>
                      <span className="mt-1 block text-xs text-muted-foreground">
                        {suggestion.description}
                      </span>
                    </span>
                  </button>
                );
              })}
            </div>
          </div>
        )}
        <div className="flex justify-end border-t pt-3">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
