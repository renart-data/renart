"use client";

import { Filter } from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import type {
  PresentationDataset,
  PresentationFilter,
  PresentationFilterBinding,
  PresentationVisualization,
} from "@/lib/api-presentations";

import {
  datasetColumns,
  defaultFilterValue,
  type AssetChoice,
} from "../presentation-visual-editor";
import { generatedPresentationID, humanizePresentationLabel } from "./presentation-builder-model";

export function AddFilterDialog({
  open,
  datasets,
  visualizations,
  assetChoices,
  existingIDs,
  onOpenChange,
  onAdd,
  onAddBlank,
}: {
  open: boolean;
  datasets: PresentationDataset[];
  visualizations: PresentationVisualization[];
  assetChoices: AssetChoice[];
  existingIDs: string[];
  onOpenChange: (open: boolean) => void;
  onAdd: (filter: PresentationFilter, bindings: Record<string, PresentationFilterBinding>) => void;
  onAddBlank: () => void;
}) {
  const availableDatasets = useMemo(
    () =>
      datasets
        .map((dataset) => ({
          dataset,
          columns: datasetColumns(dataset.id, datasets, assetChoices),
        }))
        .filter((entry) => entry.columns.length > 0),
    [assetChoices, datasets],
  );
  const [datasetID, setDatasetID] = useState("");
  const [columnName, setColumnName] = useState("");
  const [label, setLabel] = useState("");
  const [filterType, setFilterType] = useState("text");
  const selectedDataset = availableDatasets.find((entry) => entry.dataset.id === datasetID);
  const selectedColumn = selectedDataset?.columns.find((column) => column.name === columnName);

  useEffect(() => {
    if (!open) return;
    const first = availableDatasets[0];
    const column = first?.columns[0];
    setDatasetID(first?.dataset.id ?? "");
    setColumnName(column?.name ?? "");
    setLabel(column ? humanizePresentationLabel(column.name) : "Control");
    setFilterType(filterTypeForSemantic(column?.semantic_type));
  }, [availableDatasets, open]);

  const compatibleDatasets = useMemo(() => {
    if (!selectedColumn) return new Set<string>();
    return new Set(
      availableDatasets
        .filter((entry) => {
          const candidate = entry.columns.find((column) => column.name === selectedColumn.name);
          if (!candidate) return false;
          if (entry.dataset.id === datasetID) return true;
          return (
            selectedColumn.semantic_type !== "unknown" &&
            candidate.semantic_type === selectedColumn.semantic_type
          );
        })
        .map((entry) => entry.dataset.id),
    );
  }, [availableDatasets, datasetID, selectedColumn]);
  const affected = visualizations.filter((visualization) =>
    compatibleDatasets.has(visualization.dataset),
  );

  const chooseColumn = (nextDatasetID: string, nextColumnName: string) => {
    const entry = availableDatasets.find((candidate) => candidate.dataset.id === nextDatasetID);
    const column = entry?.columns.find((candidate) => candidate.name === nextColumnName);
    setDatasetID(nextDatasetID);
    setColumnName(nextColumnName);
    setLabel(column ? humanizePresentationLabel(column.name) : "Control");
    setFilterType(filterTypeForSemantic(column?.semantic_type));
  };

  const submit = () => {
    if (!selectedColumn) return;
    const id = generatedPresentationID(selectedColumn.name, "filter", existingIDs);
    const bindings = Object.fromEntries(
      affected.map((visualization) => [
        visualization.id,
        { filter: id, column: selectedColumn.name, operator: "equals" },
      ]),
    );
    onAdd(
      {
        id,
        label: label.trim() || humanizePresentationLabel(selectedColumn.name),
        type: filterType,
        default: defaultFilterValue(filterType),
      },
      bindings,
    );
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Add a control</DialogTitle>
          <DialogDescription>
            Choose a known field. Renart safely binds matching visualizations for you.
          </DialogDescription>
        </DialogHeader>
        {availableDatasets.length === 0 ? (
          <div className="rounded-xl border border-dashed bg-muted/20 p-6 text-center">
            <Filter className="mx-auto size-6 text-muted-foreground" />
            <p className="mt-3 text-sm font-medium">No known fields yet</p>
            <p className="mt-1 text-xs text-muted-foreground">
              Resolve or declare a dataset schema before adding a field-backed control.
            </p>
          </div>
        ) : (
          <FieldGroup>
            <Field>
              <FieldLabel>Dataset</FieldLabel>
              <Select
                value={datasetID}
                onValueChange={(value) =>
                  chooseColumn(
                    value,
                    availableDatasets.find((entry) => entry.dataset.id === value)?.columns[0]
                      ?.name ?? "",
                  )
                }
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {availableDatasets.map((entry) => (
                    <SelectItem key={entry.dataset.id} value={entry.dataset.id}>
                      {entry.dataset.id}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel>Field</FieldLabel>
              <Select value={columnName} onValueChange={(value) => chooseColumn(datasetID, value)}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {(selectedDataset?.columns ?? []).map((column) => (
                    <SelectItem key={column.name} value={column.name}>
                      {column.name} · {column.semantic_type}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="new-presentation-filter-label">Label</FieldLabel>
              <Input
                id="new-presentation-filter-label"
                value={label}
                onChange={(event) => setLabel(event.target.value)}
              />
            </Field>
            <Field>
              <FieldLabel>Input type</FieldLabel>
              <Select value={filterType} onValueChange={setFilterType}>
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="text">Text</SelectItem>
                  <SelectItem value="number">Number</SelectItem>
                  <SelectItem value="date">Date</SelectItem>
                  <SelectItem value="boolean">Boolean</SelectItem>
                </SelectContent>
              </Select>
              <FieldDescription>
                {affected.length === 0
                  ? "No visualization uses a compatible dataset yet. You can bind it later."
                  : `Will affect ${affected.length} compatible visualization${affected.length === 1 ? "" : "s"}.`}
              </FieldDescription>
              {affected.length > 0 ? (
                <div className="flex flex-wrap gap-1">
                  {affected.map((visualization) => (
                    <Badge key={visualization.id} variant="secondary" className="font-normal">
                      {visualization.id}
                    </Badge>
                  ))}
                </div>
              ) : null}
            </Field>
          </FieldGroup>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          {availableDatasets.length === 0 ? (
            <Button
              onClick={() => {
                onAddBlank();
                onOpenChange(false);
              }}
            >
              <Filter /> Add blank control
            </Button>
          ) : null}
          {availableDatasets.length > 0 ? (
            <Button disabled={!selectedColumn} onClick={submit}>
              <Filter /> Add control
            </Button>
          ) : null}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function filterTypeForSemantic(semanticType: string | undefined) {
  if (semanticType === "numeric") return "number";
  if (semanticType === "temporal") return "date";
  if (semanticType === "boolean") return "boolean";
  return "text";
}
