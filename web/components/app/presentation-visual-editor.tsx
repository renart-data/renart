"use client";

import {
  ArrowDown,
  ArrowUp,
  BarChart3,
  Database,
  FileText,
  Filter,
  Plus,
  Table2,
  Trash2,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";

import {
  normalizeVisualizationDefinition,
  VisualizationBuilder,
  visualizationDefinitionRecord,
} from "@/components/app/notebook-visualization-block";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Combobox,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
} from "@/components/ui/combobox";
import {
  DelimitedCard,
  DelimitedCardContent,
  DelimitedCardHeader,
  DelimitedCardTitle,
} from "@/components/ui/delimited-card";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import type { PresentationResolvedColumn } from "@/lib/api-notebooks";
import type {
  PresentationArtifact,
  PresentationDataset,
  PresentationFilter,
  PresentationFilterBinding,
  PresentationSection,
  PresentationVisualization,
} from "@/lib/api-presentations";
import type { WebAsset, WebColumn, WorkspaceState } from "@/lib/types";
import { cn } from "@/lib/utils";

type AssetChoice = {
  value: string;
  label: string;
  detail: string;
  asset: WebAsset;
};

const FILTER_TYPES = [
  "select",
  "multi_select",
  "date",
  "date_range",
  "number",
  "text",
  "boolean",
] as const;

export function PresentationVisualEditor({
  artifact,
  workspace,
  onChange,
}: {
  artifact: PresentationArtifact;
  workspace: WorkspaceState | null;
  onChange: (artifact: PresentationArtifact) => void;
}) {
  const datasets = artifact.datasets ?? [];
  const visualizations = artifact.visualizations ?? [];
  const filters = artifact.filters ?? [];
  const [selectedVisualization, setSelectedVisualization] = useState(visualizations[0]?.id ?? "");
  const assetChoices = useMemo(() => workspaceAssetChoices(workspace), [workspace]);

  useEffect(() => {
    if (visualizations.some((visualization) => visualization.id === selectedVisualization)) return;
    setSelectedVisualization(visualizations[0]?.id ?? "");
  }, [selectedVisualization, visualizations]);

  const patch = (next: Partial<PresentationArtifact>) => onChange({ ...artifact, ...next });
  const updateDataset = (index: number, next: PresentationDataset) => {
    const updated = [...datasets];
    updated[index] = next;
    patch({ datasets: updated });
  };
  const updateVisualization = (index: number, next: PresentationVisualization) => {
    const updated = [...visualizations];
    updated[index] = next;
    patch({ visualizations: updated });
  };

  const addDataset = () => {
    const id = nextID(
      "dataset",
      datasets.map((dataset) => dataset.id),
    );
    patch({ datasets: [...datasets, { id, asset: assetChoices[0]?.value ?? "" }] });
  };
  const addVisualization = () => {
    if (datasets.length === 0) return;
    const id = nextID(
      "visualization",
      visualizations.map((visualization) => visualization.id),
    );
    const next: PresentationVisualization = {
      id,
      dataset: datasets[0].id,
      definition: { version: 1, type: "table", presentation_limit: 200 },
    };
    const nextVisualizations = [...visualizations, next];
    if (artifact.kind === "dashboard") {
      patch({
        visualizations: nextVisualizations,
        layout: [...(artifact.layout ?? []), { visualization: id, width: 6, height: 4 }],
      });
    } else {
      const sectionID = nextID(
        "chart",
        (artifact.sections ?? []).map((section) => section.id),
      );
      patch({
        visualizations: nextVisualizations,
        sections: [...(artifact.sections ?? []), { id: sectionID, visualization: id }],
      });
    }
    setSelectedVisualization(id);
  };

  return (
    <div className="space-y-4 pb-8">
      <DelimitedCard>
        <DelimitedCardHeader>
          <FileText className="size-4 text-primary" />
          <DelimitedCardTitle>Identity</DelimitedCardTitle>
        </DelimitedCardHeader>
        <DelimitedCardContent>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="presentation-title">Title</FieldLabel>
              <Input
                id="presentation-title"
                value={artifact.title}
                onChange={(event) => patch({ title: event.target.value })}
              />
            </Field>
            <Field>
              <FieldLabel>Artifact ID</FieldLabel>
              <Input value={artifact.id} readOnly className="font-mono" />
              <FieldDescription>
                The stable ID is created with the file and cannot be renamed in the editor.
              </FieldDescription>
            </Field>
          </FieldGroup>
        </DelimitedCardContent>
      </DelimitedCard>

      <DelimitedCard>
        <DelimitedCardHeader>
          <Database className="size-4 text-primary" />
          <DelimitedCardTitle>Datasets</DelimitedCardTitle>
          <Badge variant="outline" className="font-normal">
            {datasets.length}
          </Badge>
          <Button className="ml-auto" size="xs" variant="outline" onClick={addDataset}>
            <Plus /> Add dataset
          </Button>
        </DelimitedCardHeader>
        <DelimitedCardContent className="space-y-3">
          {datasets.length === 0 ? (
            <EmptyBuilderState
              icon={Database}
              title="Add a dataset first"
              description="Datasets reference pipeline assets or a read-only query on a configured connection."
              action="Add dataset"
              onAction={addDataset}
            />
          ) : (
            datasets.map((dataset, index) => (
              <DatasetEditor
                key={`${dataset.id}:${index}`}
                dataset={dataset}
                assetChoices={assetChoices}
                connections={
                  workspace?.query_connections?.map((connection) => connection.name) ?? []
                }
                referenced={visualizations.some(
                  (visualization) => visualization.dataset === dataset.id,
                )}
                onChange={(next) => updateDataset(index, next)}
                onRename={(nextIDValue) =>
                  patch(renameDataset(artifact, dataset.id, nextIDValue, index))
                }
                onDelete={() =>
                  patch({ datasets: datasets.filter((_, candidate) => candidate !== index) })
                }
              />
            ))
          )}
        </DelimitedCardContent>
      </DelimitedCard>

      <DelimitedCard>
        <DelimitedCardHeader>
          <Filter className="size-4 text-primary" />
          <DelimitedCardTitle>Filters</DelimitedCardTitle>
          <Badge variant="outline" className="font-normal">
            {filters.length}
          </Badge>
          <Button
            className="ml-auto"
            size="xs"
            variant="outline"
            onClick={() => {
              const id = nextID(
                "filter",
                filters.map((filter) => filter.id),
              );
              patch({ filters: [...filters, { id, label: "Filter", type: "text", default: "" }] });
            }}
          >
            <Plus /> Add filter
          </Button>
        </DelimitedCardHeader>
        <DelimitedCardContent className="space-y-3">
          {filters.length === 0 ? (
            <p className="text-xs text-muted-foreground">
              Filters are optional. Add one when viewers should be able to constrain a dataset.
            </p>
          ) : (
            filters.map((filter, index) => (
              <FilterEditor
                key={`${filter.id}:${index}`}
                filter={filter}
                datasets={datasets}
                columnsForDataset={(datasetID) => datasetColumns(datasetID, datasets, assetChoices)}
                onChange={(next) => {
                  const updated = [...filters];
                  updated[index] = next;
                  patch({ filters: updated });
                }}
                onRename={(nextIDValue) =>
                  patch(renameFilter(artifact, filter.id, nextIDValue, index))
                }
                onDelete={() =>
                  patch({
                    filters: filters.filter((_, candidate) => candidate !== index),
                    visualizations: visualizations.map((visualization) => ({
                      ...visualization,
                      filter_bindings: visualization.filter_bindings?.filter(
                        (binding) => binding.filter !== filter.id,
                      ),
                    })),
                  })
                }
              />
            ))
          )}
        </DelimitedCardContent>
      </DelimitedCard>

      <DelimitedCard>
        <DelimitedCardHeader>
          <BarChart3 className="size-4 text-primary" />
          <DelimitedCardTitle>Visualizations</DelimitedCardTitle>
          <Badge variant="outline" className="font-normal">
            {visualizations.length}
          </Badge>
          <Button
            className="ml-auto"
            size="xs"
            variant="outline"
            disabled={datasets.length === 0}
            onClick={addVisualization}
          >
            <Plus /> Add visualization
          </Button>
        </DelimitedCardHeader>
        <DelimitedCardContent>
          {visualizations.length === 0 ? (
            <EmptyBuilderState
              icon={BarChart3}
              title="No visualizations yet"
              description={
                datasets.length === 0
                  ? "Create a dataset before adding a visualization."
                  : "Visualizations are checked against the selected dataset's columns."
              }
              action="Add visualization"
              disabled={datasets.length === 0}
              onAction={addVisualization}
            />
          ) : (
            <div className="grid min-w-0 gap-4 xl:grid-cols-[14rem_minmax(0,1fr)]">
              <div className="space-y-1">
                {visualizations.map((visualization) => (
                  <button
                    key={visualization.id}
                    type="button"
                    onClick={() => setSelectedVisualization(visualization.id)}
                    className={cn(
                      "flex w-full min-w-0 items-center gap-2 rounded-md px-2.5 py-2 text-left text-xs transition-colors",
                      selectedVisualization === visualization.id
                        ? "bg-primary/10 text-foreground"
                        : "text-muted-foreground hover:bg-muted hover:text-foreground",
                    )}
                  >
                    <BarChart3 className="size-3.5 shrink-0" />
                    <span className="truncate font-medium">{visualization.id}</span>
                    <span className="ml-auto truncate text-[10px]">{visualization.dataset}</span>
                  </button>
                ))}
              </div>
              {visualizations.map((visualization, index) =>
                visualization.id === selectedVisualization ? (
                  <VisualizationEditor
                    key={visualization.id}
                    visualization={visualization}
                    datasets={datasets}
                    filters={filters}
                    columns={datasetColumns(visualization.dataset, datasets, assetChoices)}
                    onChange={(next) => updateVisualization(index, next)}
                    onRename={(nextIDValue) =>
                      patch(renameVisualization(artifact, visualization.id, nextIDValue, index))
                    }
                    onDelete={() => {
                      const next = visualizations.filter((_, candidate) => candidate !== index);
                      patch({
                        visualizations: next,
                        layout: artifact.layout?.filter(
                          (item) => item.visualization !== visualization.id,
                        ),
                        sections: artifact.sections?.filter(
                          (section) => section.visualization !== visualization.id,
                        ),
                      });
                      setSelectedVisualization(next[0]?.id ?? "");
                    }}
                  />
                ) : null,
              )}
            </div>
          )}
        </DelimitedCardContent>
      </DelimitedCard>

      {artifact.kind === "dashboard" ? (
        <DashboardLayoutEditor artifact={artifact} onChange={patch} />
      ) : (
        <ReportSectionsEditor artifact={artifact} onChange={patch} />
      )}
    </div>
  );
}

function DatasetEditor({
  dataset,
  assetChoices,
  connections,
  referenced,
  onChange,
  onRename,
  onDelete,
}: {
  dataset: PresentationDataset;
  assetChoices: AssetChoice[];
  connections: string[];
  referenced: boolean;
  onChange: (dataset: PresentationDataset) => void;
  onRename: (id: string) => void;
  onDelete: () => void;
}) {
  const sourceKind = dataset.query !== undefined ? "query" : "asset";
  const datasetIDInput = `presentation-dataset-${dataset.id || "new"}-id`;
  return (
    <div className="space-y-3 rounded-lg border bg-background/60 p-3">
      <div className="grid min-w-0 gap-3 sm:grid-cols-[minmax(8rem,0.7fr)_8rem_minmax(12rem,1.3fr)_auto]">
        <Field>
          <FieldLabel htmlFor={datasetIDInput}>Dataset ID</FieldLabel>
          <Input
            id={datasetIDInput}
            value={dataset.id}
            className="font-mono text-xs"
            onChange={(event) => onRename(event.target.value)}
          />
        </Field>
        <Field>
          <FieldLabel>Source</FieldLabel>
          <Select
            value={sourceKind}
            onValueChange={(value) =>
              onChange(
                value === "query"
                  ? { id: dataset.id, connection: connections[0] ?? "", query: "select *\nfrom " }
                  : { id: dataset.id, asset: assetChoices[0]?.value ?? "" },
              )
            }
          >
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="asset">Asset</SelectItem>
              <SelectItem value="query">Query</SelectItem>
            </SelectContent>
          </Select>
        </Field>
        {sourceKind === "asset" ? (
          <Field>
            <FieldLabel>Pipeline asset</FieldLabel>
            <AssetCombobox
              value={dataset.asset ?? ""}
              items={assetChoices}
              onChange={(asset) =>
                onChange({ ...dataset, asset, connection: undefined, query: undefined })
              }
            />
          </Field>
        ) : (
          <Field>
            <FieldLabel>Connection</FieldLabel>
            <Select
              value={dataset.connection ?? ""}
              onValueChange={(connection) => onChange({ ...dataset, connection })}
            >
              <SelectTrigger className="w-full">
                <SelectValue placeholder="Choose a connection" />
              </SelectTrigger>
              <SelectContent>
                {connections.map((connection) => (
                  <SelectItem key={connection} value={connection}>
                    {connection}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
        )}
        <div className="flex items-end justify-end">
          <Button
            size="icon-sm"
            variant="ghost"
            disabled={referenced}
            title={
              referenced ? "Remove visualizations that use this dataset first" : "Delete dataset"
            }
            aria-label={`Delete dataset ${dataset.id}`}
            onClick={onDelete}
          >
            <Trash2 />
          </Button>
        </div>
      </div>
      {sourceKind === "query" ? (
        <>
          <Field>
            <FieldLabel>Read-only query</FieldLabel>
            <Textarea
              value={dataset.query ?? ""}
              className="min-h-28 resize-y font-mono text-xs"
              onChange={(event) => onChange({ ...dataset, query: event.target.value })}
            />
          </Field>
          <DeclaredColumnsEditor
            columns={dataset.columns ?? []}
            onChange={(columns) => onChange({ ...dataset, columns })}
          />
        </>
      ) : null}
    </div>
  );
}

function DeclaredColumnsEditor({
  columns,
  onChange,
}: {
  columns: WebColumn[];
  onChange: (columns: WebColumn[]) => void;
}) {
  return (
    <Field>
      <div className="flex items-center justify-between">
        <FieldLabel>Declared columns</FieldLabel>
        <Button
          size="xs"
          variant="ghost"
          onClick={() => onChange([...columns, { name: "column", type: "string" }])}
        >
          <Plus /> Add column
        </Button>
      </div>
      <FieldDescription>
        Query datasets need declared columns until connection-aware inference is available here.
      </FieldDescription>
      <div className="space-y-1.5">
        {columns.map((column, index) => (
          <div
            key={index}
            className="grid grid-cols-[minmax(0,1fr)_minmax(7rem,0.7fr)_auto] gap-1.5"
          >
            <Input
              aria-label={`Column ${index + 1} name`}
              value={column.name}
              placeholder="name"
              className="h-8 font-mono text-xs"
              onChange={(event) => {
                const next = [...columns];
                next[index] = { ...column, name: event.target.value };
                onChange(next);
              }}
            />
            <Input
              aria-label={`Column ${index + 1} type`}
              value={column.type ?? ""}
              placeholder="type"
              className="h-8 font-mono text-xs"
              onChange={(event) => {
                const next = [...columns];
                next[index] = { ...column, type: event.target.value };
                onChange(next);
              }}
            />
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={`Delete column ${column.name || index + 1}`}
              onClick={() => onChange(columns.filter((_, candidate) => candidate !== index))}
            >
              <Trash2 />
            </Button>
          </div>
        ))}
      </div>
    </Field>
  );
}

function FilterEditor({
  filter,
  datasets,
  columnsForDataset,
  onChange,
  onRename,
  onDelete,
}: {
  filter: PresentationFilter;
  datasets: PresentationDataset[];
  columnsForDataset: (dataset: string) => PresentationResolvedColumn[];
  onChange: (filter: PresentationFilter) => void;
  onRename: (id: string) => void;
  onDelete: () => void;
}) {
  const supportsOptions = filter.type === "select" || filter.type === "multi_select";
  const optionMode = filter.options?.dataset
    ? "dataset"
    : filter.options?.values?.length
      ? "static"
      : "none";
  const optionDataset = filter.options?.dataset ?? datasets[0]?.id ?? "";
  const optionColumns = columnsForDataset(optionDataset);
  const filterIDInput = `presentation-filter-${filter.id || "new"}-id`;
  const filterLabelInput = `presentation-filter-${filter.id || "new"}-label`;
  return (
    <div className="space-y-3 rounded-lg border bg-background/60 p-3">
      <div className="grid min-w-0 gap-3 sm:grid-cols-[minmax(8rem,0.7fr)_minmax(8rem,1fr)_9rem_auto]">
        <Field>
          <FieldLabel htmlFor={filterIDInput}>Filter ID</FieldLabel>
          <Input
            id={filterIDInput}
            value={filter.id}
            className="font-mono text-xs"
            onChange={(event) => onRename(event.target.value)}
          />
        </Field>
        <Field>
          <FieldLabel htmlFor={filterLabelInput}>Label</FieldLabel>
          <Input
            id={filterLabelInput}
            value={filter.label ?? ""}
            onChange={(event) => onChange({ ...filter, label: event.target.value })}
          />
        </Field>
        <Field>
          <FieldLabel>Type</FieldLabel>
          <Select
            value={filter.type}
            onValueChange={(type) =>
              onChange({ ...filter, type, default: defaultFilterValue(type), options: undefined })
            }
          >
            <SelectTrigger className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {FILTER_TYPES.map((type) => (
                <SelectItem key={type} value={type}>
                  {type.replace("_", " ")}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <div className="flex items-end justify-end">
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={`Delete filter ${filter.id}`}
            onClick={onDelete}
          >
            <Trash2 />
          </Button>
        </div>
      </div>
      <Field>
        <FieldLabel>Default value</FieldLabel>
        <FilterDefaultEditor
          filter={filter}
          onChange={(value) => onChange({ ...filter, default: value })}
        />
      </Field>
      {supportsOptions ? (
        <div className="grid gap-3 sm:grid-cols-[9rem_minmax(0,1fr)]">
          <Field>
            <FieldLabel>Options</FieldLabel>
            <Select
              value={optionMode}
              onValueChange={(mode) => {
                if (mode === "none") onChange({ ...filter, options: undefined });
                if (mode === "static") onChange({ ...filter, options: { values: [] } });
                if (mode === "dataset")
                  onChange({
                    ...filter,
                    options: { dataset: datasets[0]?.id ?? "", value_field: "" },
                  });
              }}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="none">Unconstrained</SelectItem>
                <SelectItem value="static">Static values</SelectItem>
                <SelectItem value="dataset">From dataset</SelectItem>
              </SelectContent>
            </Select>
          </Field>
          {optionMode === "static" ? (
            <Field>
              <FieldLabel>Comma-separated values</FieldLabel>
              <Input
                value={(filter.options?.values ?? []).join(", ")}
                onChange={(event) =>
                  onChange({
                    ...filter,
                    options: {
                      values: event.target.value
                        .split(",")
                        .map((value) => value.trim())
                        .filter(Boolean),
                    },
                  })
                }
              />
            </Field>
          ) : optionMode === "dataset" ? (
            <div className="grid gap-3 sm:grid-cols-3">
              <Field>
                <FieldLabel>Dataset</FieldLabel>
                <Select
                  value={optionDataset}
                  onValueChange={(dataset) =>
                    onChange({
                      ...filter,
                      options: { ...filter.options, dataset, value_field: "", label_field: "" },
                    })
                  }
                >
                  <SelectTrigger className="w-full">
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
              <Field>
                <FieldLabel>Value field</FieldLabel>
                <ColumnSelect
                  value={filter.options?.value_field ?? ""}
                  columns={optionColumns}
                  onChange={(value_field) =>
                    onChange({
                      ...filter,
                      options: { ...filter.options, dataset: optionDataset, value_field },
                    })
                  }
                />
              </Field>
              <Field>
                <FieldLabel>Label field</FieldLabel>
                <ColumnSelect
                  value={filter.options?.label_field ?? ""}
                  columns={optionColumns}
                  optional
                  onChange={(label_field) =>
                    onChange({
                      ...filter,
                      options: {
                        ...filter.options,
                        dataset: optionDataset,
                        value_field: filter.options?.value_field ?? "",
                        label_field,
                      },
                    })
                  }
                />
              </Field>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function FilterDefaultEditor({
  filter,
  onChange,
}: {
  filter: PresentationFilter;
  onChange: (value: unknown) => void;
}) {
  if (filter.type === "boolean") {
    return (
      <label className="flex h-9 items-center gap-2 text-xs">
        <Checkbox
          checked={filter.default === true}
          onCheckedChange={(value) => onChange(value === true)}
        />
        Enabled by default
      </label>
    );
  }
  if (filter.type === "number") {
    return (
      <Input
        type="number"
        value={typeof filter.default === "number" ? filter.default : 0}
        onChange={(event) => onChange(Number(event.target.value))}
      />
    );
  }
  if (filter.type === "date") {
    return (
      <Input
        type="date"
        value={typeof filter.default === "string" ? filter.default : ""}
        onChange={(event) => onChange(event.target.value)}
      />
    );
  }
  if (filter.type === "date_range") {
    const values = Array.isArray(filter.default) ? filter.default : ["", ""];
    return (
      <div className="grid gap-2 sm:grid-cols-2">
        <Input
          type="date"
          value={String(values[0] ?? "")}
          onChange={(event) => onChange([event.target.value, values[1] ?? ""])}
        />
        <Input
          type="date"
          value={String(values[1] ?? "")}
          onChange={(event) => onChange([values[0] ?? "", event.target.value])}
        />
      </div>
    );
  }
  if (filter.type === "multi_select") {
    return (
      <Input
        value={Array.isArray(filter.default) ? filter.default.join(", ") : ""}
        placeholder="eu, us"
        onChange={(event) =>
          onChange(
            event.target.value
              .split(",")
              .map((value) => value.trim())
              .filter(Boolean),
          )
        }
      />
    );
  }
  return (
    <Input
      value={typeof filter.default === "string" ? filter.default : ""}
      onChange={(event) => onChange(event.target.value)}
    />
  );
}

function VisualizationEditor({
  visualization,
  datasets,
  filters,
  columns,
  onChange,
  onRename,
  onDelete,
}: {
  visualization: PresentationVisualization;
  datasets: PresentationDataset[];
  filters: PresentationFilter[];
  columns: PresentationResolvedColumn[];
  onChange: (visualization: PresentationVisualization) => void;
  onRename: (id: string) => void;
  onDelete: () => void;
}) {
  const bindings = visualization.filter_bindings ?? [];
  const visualizationIDInput = `presentation-visualization-${visualization.id || "new"}-id`;
  return (
    <div className="min-w-0 space-y-4 rounded-lg border bg-background/60 p-3">
      <div className="grid gap-3 sm:grid-cols-[minmax(8rem,0.7fr)_minmax(9rem,1fr)_auto]">
        <Field>
          <FieldLabel htmlFor={visualizationIDInput}>Visualization ID</FieldLabel>
          <Input
            id={visualizationIDInput}
            value={visualization.id}
            className="font-mono text-xs"
            onChange={(event) => onRename(event.target.value)}
          />
        </Field>
        <Field>
          <FieldLabel>Dataset</FieldLabel>
          <Select
            value={visualization.dataset}
            onValueChange={(dataset) =>
              onChange({ ...visualization, dataset, filter_bindings: [] })
            }
          >
            <SelectTrigger className="w-full">
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
        <div className="flex items-end justify-end">
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={`Delete visualization ${visualization.id}`}
            onClick={onDelete}
          >
            <Trash2 />
          </Button>
        </div>
      </div>
      <VisualizationBuilder
        definition={normalizeVisualizationDefinition(visualization.definition)}
        columns={columns}
        onChange={(definition) =>
          onChange({ ...visualization, definition: visualizationDefinitionRecord(definition) })
        }
      />
      <Field>
        <div className="flex items-center justify-between">
          <FieldLabel>Filter bindings</FieldLabel>
          <Button
            size="xs"
            variant="ghost"
            disabled={filters.length === 0 || columns.length === 0}
            onClick={() => {
              const filter = filters[0];
              if (!filter || !columns[0]) return;
              onChange({
                ...visualization,
                filter_bindings: [
                  ...bindings,
                  {
                    filter: filter.id,
                    column: columns[0].name,
                    operator: defaultFilterOperator(filter.type),
                  },
                ],
              });
            }}
          >
            <Plus /> Add binding
          </Button>
        </div>
        {bindings.length === 0 ? (
          <FieldDescription>
            Bind a filter to a column in this visualization's dataset.
          </FieldDescription>
        ) : (
          <div className="space-y-1.5">
            {bindings.map((binding, index) => (
              <FilterBindingEditor
                key={index}
                binding={binding}
                filters={filters}
                columns={columns}
                onChange={(next) => {
                  const updated = [...bindings];
                  updated[index] = next;
                  onChange({ ...visualization, filter_bindings: updated });
                }}
                onDelete={() =>
                  onChange({
                    ...visualization,
                    filter_bindings: bindings.filter((_, candidate) => candidate !== index),
                  })
                }
              />
            ))}
          </div>
        )}
      </Field>
    </div>
  );
}

function FilterBindingEditor({
  binding,
  filters,
  columns,
  onChange,
  onDelete,
}: {
  binding: PresentationFilterBinding;
  filters: PresentationFilter[];
  columns: PresentationResolvedColumn[];
  onChange: (binding: PresentationFilterBinding) => void;
  onDelete: () => void;
}) {
  const selectedFilter = filters.find((filter) => filter.id === binding.filter);
  return (
    <div className="grid gap-1.5 sm:grid-cols-[minmax(7rem,0.7fr)_minmax(7rem,1fr)_9rem_auto]">
      <Select
        value={binding.filter}
        onValueChange={(filterID) => {
          const filter = filters.find((candidate) => candidate.id === filterID);
          onChange({
            ...binding,
            filter: filterID,
            operator: defaultFilterOperator(filter?.type ?? "text"),
          });
        }}
      >
        <SelectTrigger className="w-full">
          <SelectValue placeholder="Filter" />
        </SelectTrigger>
        <SelectContent>
          {filters.map((filter) => (
            <SelectItem key={filter.id} value={filter.id}>
              {filter.label || filter.id}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <ColumnSelect
        value={binding.column}
        columns={columns}
        onChange={(column) => onChange({ ...binding, column })}
      />
      <Select
        value={binding.operator}
        onValueChange={(operator) => onChange({ ...binding, operator })}
      >
        <SelectTrigger className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {filterOperators(selectedFilter?.type ?? "text").map((operator) => (
            <SelectItem key={operator} value={operator}>
              {operator}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Button size="icon-sm" variant="ghost" aria-label="Delete filter binding" onClick={onDelete}>
        <Trash2 />
      </Button>
    </div>
  );
}

function DashboardLayoutEditor({
  artifact,
  onChange,
}: {
  artifact: PresentationArtifact;
  onChange: (next: Partial<PresentationArtifact>) => void;
}) {
  const layout = artifact.layout ?? [];
  const visualizations = artifact.visualizations ?? [];
  return (
    <DelimitedCard>
      <DelimitedCardHeader>
        <Table2 className="size-4 text-primary" />
        <DelimitedCardTitle>Responsive layout</DelimitedCardTitle>
      </DelimitedCardHeader>
      <DelimitedCardContent>
        {layout.length === 0 ? (
          <p className="text-xs text-muted-foreground">Visualizations appear here when added.</p>
        ) : (
          <div className="grid grid-cols-12 gap-2">
            {layout.map((item, index) => (
              <div
                key={`${item.visualization}:${index}`}
                style={{ gridColumn: `span ${Math.min(12, Math.max(1, item.width ?? 6))}` }}
                className="min-w-0 rounded-lg border bg-background/60 p-3"
              >
                <div className="mb-3 flex items-center gap-2">
                  <BarChart3 className="size-3.5 text-primary" />
                  <span className="min-w-0 flex-1 truncate text-xs font-medium">
                    {item.visualization}
                  </span>
                  {!visualizations.some(
                    (visualization) => visualization.id === item.visualization,
                  ) ? (
                    <Badge variant="destructive">Missing</Badge>
                  ) : null}
                  <OrderButtons
                    index={index}
                    count={layout.length}
                    onMove={(direction) => onChange({ layout: moveItem(layout, index, direction) })}
                  />
                </div>
                <div className="grid gap-2 sm:grid-cols-2">
                  <Field>
                    <FieldLabel>Width</FieldLabel>
                    <Select
                      value={String(item.width ?? 6)}
                      onValueChange={(value) => {
                        const next = [...layout];
                        next[index] = { ...item, width: Number(value) };
                        onChange({ layout: next });
                      }}
                    >
                      <SelectTrigger className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {[3, 4, 6, 8, 9, 12].map((width) => (
                          <SelectItem key={width} value={String(width)}>
                            {width}/12
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                  <Field>
                    <FieldLabel>Height</FieldLabel>
                    <Input
                      type="number"
                      min={1}
                      max={20}
                      value={item.height ?? 4}
                      onChange={(event) => {
                        const next = [...layout];
                        next[index] = {
                          ...item,
                          height: Math.max(1, Number(event.target.value) || 1),
                        };
                        onChange({ layout: next });
                      }}
                    />
                  </Field>
                </div>
              </div>
            ))}
          </div>
        )}
      </DelimitedCardContent>
    </DelimitedCard>
  );
}

function ReportSectionsEditor({
  artifact,
  onChange,
}: {
  artifact: PresentationArtifact;
  onChange: (next: Partial<PresentationArtifact>) => void;
}) {
  const sections = artifact.sections ?? [];
  const visualizations = artifact.visualizations ?? [];
  const addSection = (kind: "markdown" | "visualization") => {
    const id = nextID(
      kind === "markdown" ? "text" : "chart",
      sections.map((section) => section.id),
    );
    const section: PresentationSection =
      kind === "markdown"
        ? { id, title: "Section", markdown: "Write your report narrative here." }
        : { id, visualization: visualizations[0]?.id ?? "" };
    onChange({ sections: [...sections, section] });
  };
  return (
    <DelimitedCard>
      <DelimitedCardHeader>
        <FileText className="size-4 text-primary" />
        <DelimitedCardTitle>Report sections</DelimitedCardTitle>
        <div className="ml-auto flex items-center gap-1.5">
          <Button size="xs" variant="outline" onClick={() => addSection("markdown")}>
            <Plus /> Add text
          </Button>
          <Button
            size="xs"
            variant="outline"
            disabled={visualizations.length === 0}
            onClick={() => addSection("visualization")}
          >
            <Plus /> Add chart
          </Button>
        </div>
      </DelimitedCardHeader>
      <DelimitedCardContent className="space-y-3">
        {sections.length === 0 ? (
          <p className="text-xs text-muted-foreground">
            Compose the report from narrative and visualization sections.
          </p>
        ) : (
          sections.map((section, index) => (
            <div
              key={`${section.id}:${index}`}
              className="space-y-3 rounded-lg border bg-background/60 p-3"
            >
              <div className="flex items-center gap-2">
                <Input
                  aria-label="Section ID"
                  value={section.id}
                  className="h-8 max-w-48 font-mono text-xs"
                  onChange={(event) => {
                    const next = [...sections];
                    next[index] = { ...section, id: event.target.value };
                    onChange({ sections: next });
                  }}
                />
                <Badge variant="outline" className="font-normal">
                  {section.markdown !== undefined ? "text" : "visualization"}
                </Badge>
                <div className="ml-auto flex items-center gap-1">
                  <OrderButtons
                    index={index}
                    count={sections.length}
                    onMove={(direction) =>
                      onChange({ sections: moveItem(sections, index, direction) })
                    }
                  />
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    aria-label={`Delete section ${section.id}`}
                    onClick={() =>
                      onChange({ sections: sections.filter((_, candidate) => candidate !== index) })
                    }
                  >
                    <Trash2 />
                  </Button>
                </div>
              </div>
              {section.markdown !== undefined ? (
                <>
                  <Input
                    value={section.title ?? ""}
                    placeholder="Section title"
                    onChange={(event) => {
                      const next = [...sections];
                      next[index] = { ...section, title: event.target.value };
                      onChange({ sections: next });
                    }}
                  />
                  <Textarea
                    value={section.markdown}
                    className="min-h-32 resize-y"
                    onChange={(event) => {
                      const next = [...sections];
                      next[index] = { ...section, markdown: event.target.value };
                      onChange({ sections: next });
                    }}
                  />
                </>
              ) : (
                <Select
                  value={section.visualization ?? ""}
                  onValueChange={(visualization) => {
                    const next = [...sections];
                    next[index] = { ...section, visualization };
                    onChange({ sections: next });
                  }}
                >
                  <SelectTrigger className="w-full">
                    <SelectValue placeholder="Choose a visualization" />
                  </SelectTrigger>
                  <SelectContent>
                    {visualizations.map((visualization) => (
                      <SelectItem key={visualization.id} value={visualization.id}>
                        {visualization.id}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              )}
              <label className="flex items-center gap-2 text-xs">
                <Checkbox
                  checked={section.page_break === true}
                  onCheckedChange={(value) => {
                    const next = [...sections];
                    next[index] = { ...section, page_break: value === true };
                    onChange({ sections: next });
                  }}
                />
                Start a new page after this section when printing
              </label>
            </div>
          ))
        )}
      </DelimitedCardContent>
    </DelimitedCard>
  );
}

function AssetCombobox({
  value,
  items,
  onChange,
}: {
  value: string;
  items: AssetChoice[];
  onChange: (value: string) => void;
}) {
  const selected = items.find((item) => item.value === value) ?? null;
  const [inputValue, setInputValue] = useState(selected?.label ?? value);
  useEffect(() => setInputValue(selected?.label ?? value), [selected?.label, value]);
  return (
    <Combobox
      autoHighlight
      items={items}
      value={selected}
      inputValue={inputValue}
      itemToStringLabel={(item: AssetChoice) => item.label}
      itemToStringValue={(item: AssetChoice) => item.value}
      isItemEqualToValue={(left, right) => left.value === right.value}
      onInputValueChange={(next, details) => {
        if (details.reason !== "item-press") setInputValue(next);
      }}
      onValueChange={(next) => {
        onChange(next?.value ?? "");
        setInputValue(next?.label ?? "");
      }}
    >
      <ComboboxInput aria-label="Pipeline asset" placeholder="Choose an asset" className="w-full" />
      <ComboboxContent>
        <ComboboxEmpty>No matching assets.</ComboboxEmpty>
        <ComboboxList>
          {(item: AssetChoice) => (
            <ComboboxItem key={`${item.detail}:${item.value}`} value={item}>
              <span className="min-w-0">
                <span className="block truncate">{item.label}</span>
                <span className="block truncate text-[10px] text-muted-foreground">
                  {item.detail}
                </span>
              </span>
            </ComboboxItem>
          )}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  );
}

function ColumnSelect({
  value,
  columns,
  optional = false,
  onChange,
}: {
  value: string;
  columns: PresentationResolvedColumn[];
  optional?: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <Select
      value={value || (optional ? "__none__" : "")}
      onValueChange={(next) => onChange(next === "__none__" ? "" : next)}
    >
      <SelectTrigger className="w-full">
        <SelectValue placeholder="Choose a column" />
      </SelectTrigger>
      <SelectContent>
        {optional ? <SelectItem value="__none__">None</SelectItem> : null}
        {columns.map((column) => (
          <SelectItem key={column.name} value={column.name}>
            {column.name}
            <span className="ml-1 text-muted-foreground">
              · {column.physical_type || "unknown"}
            </span>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function OrderButtons({
  index,
  count,
  onMove,
}: {
  index: number;
  count: number;
  onMove: (direction: -1 | 1) => void;
}) {
  return (
    <div className="flex items-center">
      <Button
        size="icon-xs"
        variant="ghost"
        disabled={index === 0}
        aria-label="Move up"
        onClick={() => onMove(-1)}
      >
        <ArrowUp />
      </Button>
      <Button
        size="icon-xs"
        variant="ghost"
        disabled={index === count - 1}
        aria-label="Move down"
        onClick={() => onMove(1)}
      >
        <ArrowDown />
      </Button>
    </div>
  );
}

function EmptyBuilderState({
  icon: Icon,
  title,
  description,
  action,
  disabled = false,
  onAction,
}: {
  icon: typeof Database;
  title: string;
  description: string;
  action: string;
  disabled?: boolean;
  onAction: () => void;
}) {
  return (
    <div className="rounded-lg border border-dashed bg-muted/15 px-6 py-8 text-center">
      <Icon className="mx-auto mb-2 size-6 text-muted-foreground" />
      <p className="text-sm font-medium">{title}</p>
      <p className="mx-auto mt-1 max-w-md text-xs text-muted-foreground">{description}</p>
      <Button size="sm" variant="outline" className="mt-3" disabled={disabled} onClick={onAction}>
        <Plus /> {action}
      </Button>
    </div>
  );
}

function workspaceAssetChoices(workspace: WorkspaceState | null): AssetChoice[] {
  const nameCounts = new Map<string, number>();
  for (const pipeline of workspace?.pipelines ?? []) {
    for (const asset of pipeline.assets)
      nameCounts.set(asset.name.toLowerCase(), (nameCounts.get(asset.name.toLowerCase()) ?? 0) + 1);
  }
  const choices: AssetChoice[] = [];
  for (const pipeline of workspace?.pipelines ?? []) {
    for (const asset of pipeline.assets) {
      const duplicateName = (nameCounts.get(asset.name.toLowerCase()) ?? 0) > 1;
      choices.push({
        value: asset.uri || asset.name,
        label: asset.name,
        detail: `${pipeline.name}${duplicateName && asset.uri ? ` · ${asset.uri}` : ""}`,
        asset,
      });
    }
  }
  return choices.sort(
    (left, right) =>
      left.label.localeCompare(right.label) || left.detail.localeCompare(right.detail),
  );
}

function datasetColumns(
  datasetID: string,
  datasets: PresentationDataset[],
  choices: AssetChoice[],
): PresentationResolvedColumn[] {
  const dataset = datasets.find((candidate) => candidate.id === datasetID);
  if (!dataset) return [];
  const columns = dataset.asset
    ? (choices.find((choice) => choice.value === dataset.asset)?.asset.columns ??
      dataset.columns ??
      [])
    : (dataset.columns ?? []);
  return columns.map((column) => ({
    name: column.name,
    physical_type: column.type ?? "",
    semantic_type: semanticType(column.type ?? ""),
    nullable: column.nullable,
  }));
}

function semanticType(physicalType: string): PresentationResolvedColumn["semantic_type"] {
  const value = physicalType.toLowerCase();
  if (/int|decimal|numeric|real|double|float|number/.test(value)) return "numeric";
  if (/date|time|interval/.test(value)) return "temporal";
  if (/bool/.test(value)) return "boolean";
  if (/blob|binary|bytea/.test(value)) return "binary";
  if (/json|array|struct|map|variant/.test(value)) return "semi_structured";
  if (/geo/.test(value)) return "geospatial";
  if (/char|string|text|uuid|enum/.test(value)) return "categorical";
  return "unknown";
}

function renameDataset(
  artifact: PresentationArtifact,
  previous: string,
  next: string,
  index: number,
): Partial<PresentationArtifact> {
  const datasets = [...(artifact.datasets ?? [])];
  datasets[index] = { ...datasets[index], id: next };
  return {
    datasets,
    filters: artifact.filters?.map((filter) =>
      filter.options?.dataset === previous
        ? { ...filter, options: { ...filter.options, dataset: next } }
        : filter,
    ),
    visualizations: artifact.visualizations?.map((visualization) => ({
      ...visualization,
      dataset: visualization.dataset === previous ? next : visualization.dataset,
      filter_bindings: visualization.filter_bindings?.map((binding) =>
        binding.dataset === previous ? { ...binding, dataset: next } : binding,
      ),
    })),
  };
}

function renameFilter(
  artifact: PresentationArtifact,
  previous: string,
  next: string,
  index: number,
): Partial<PresentationArtifact> {
  const filters = [...(artifact.filters ?? [])];
  filters[index] = { ...filters[index], id: next };
  return {
    filters,
    visualizations: artifact.visualizations?.map((visualization) => ({
      ...visualization,
      filter_bindings: visualization.filter_bindings?.map((binding) =>
        binding.filter === previous ? { ...binding, filter: next } : binding,
      ),
    })),
  };
}

function renameVisualization(
  artifact: PresentationArtifact,
  previous: string,
  next: string,
  index: number,
): Partial<PresentationArtifact> {
  const visualizations = [...(artifact.visualizations ?? [])];
  visualizations[index] = { ...visualizations[index], id: next };
  return {
    visualizations,
    layout: artifact.layout?.map((item) =>
      item.visualization === previous ? { ...item, visualization: next } : item,
    ),
    sections: artifact.sections?.map((section) =>
      section.visualization === previous ? { ...section, visualization: next } : section,
    ),
  };
}

function defaultFilterValue(type: string): unknown {
  if (type === "boolean") return false;
  if (type === "number") return 0;
  if (type === "multi_select" || type === "date_range") return [];
  return "";
}

function defaultFilterOperator(type: string) {
  if (type === "multi_select") return "in";
  if (type === "date_range") return "between";
  return "equals";
}

function filterOperators(type: string) {
  if (type === "multi_select") return ["in", "not_in"];
  if (type === "date_range") return ["between"];
  if (type === "number" || type === "date")
    return [
      "equals",
      "not_equals",
      "greater_than",
      "greater_or_equal",
      "less_than",
      "less_or_equal",
    ];
  if (type === "text") return ["equals", "not_equals", "contains", "starts_with"];
  return ["equals", "not_equals"];
}

function nextID(prefix: string, existing: string[]) {
  const used = new Set(existing);
  if (!used.has(prefix)) return prefix;
  for (let index = 2; ; index += 1) {
    const candidate = `${prefix}_${index}`;
    if (!used.has(candidate)) return candidate;
  }
}

function moveItem<T>(items: T[], index: number, direction: -1 | 1) {
  const target = index + direction;
  if (target < 0 || target >= items.length) return items;
  const next = [...items];
  [next[index], next[target]] = [next[target], next[index]];
  return next;
}
