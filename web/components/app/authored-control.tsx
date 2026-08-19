"use client";

import { Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Slider } from "@/components/ui/slider";
import { Switch } from "@/components/ui/switch";
import {
  AUTHORED_CONTROL_TYPE_LABELS,
  AUTHORED_CONTROL_TYPES,
  type AuthoredControlDefinition,
  type AuthoredControlOption,
  type AuthoredControlType,
  authoredControlOptions,
  authoredSliderBounds,
  authoredControlType,
  comparableAuthoredControlValue,
  defaultAuthoredControlRange,
  defaultAuthoredControlValue,
  normalizeAuthoredControlList,
} from "@/lib/authored-controls";
import { cn } from "@/lib/utils";

export type AuthoredControlDataset = {
  id: string;
  label?: string;
  columns: Array<{ name: string; detail?: string }>;
};

export function AuthoredControlEditor({
  control,
  datasets = [],
  resolvedOptions,
  idPrefix = "authored-control",
  pathPrefix,
  onChange,
  onRename,
  onDelete,
}: {
  control: AuthoredControlDefinition;
  datasets?: AuthoredControlDataset[];
  resolvedOptions?: AuthoredControlOption[];
  idPrefix?: string;
  pathPrefix?: string;
  onChange: (control: AuthoredControlDefinition) => void;
  onRename: (id: string) => void;
  onDelete: () => void;
}) {
  const type = authoredControlType(control.type);
  const supportsOptions = type === "select" || type === "multi_select";
  const optionMode = control.options?.dataset
    ? "dataset"
    : control.options?.values !== undefined
      ? "static"
      : "none";
  const optionDataset = control.options?.dataset ?? datasets[0]?.id ?? "";
  const optionColumns = datasets.find((dataset) => dataset.id === optionDataset)?.columns ?? [];
  const controlIDInput = `${idPrefix}-${control.id || "new"}-id`;
  const controlLabelInput = `${idPrefix}-${control.id || "new"}-label`;

  return (
    <div
      data-presentation-path={pathPrefix}
      className="flex min-w-0 flex-col gap-3 rounded-lg border bg-background/60 p-3"
    >
      <FieldGroup className="gap-3">
        <div className="grid min-w-0 gap-3 sm:grid-cols-2">
          <Field data-presentation-path={pathPrefix ? `${pathPrefix}.id` : undefined}>
            <FieldLabel htmlFor={controlIDInput}>Control ID</FieldLabel>
            <Input
              id={controlIDInput}
              value={control.id}
              className="font-mono text-xs"
              placeholder="customer_segment"
              onChange={(event) => onRename(event.target.value.trimStart())}
            />
          </Field>
          <Field data-presentation-path={pathPrefix ? `${pathPrefix}.label` : undefined}>
            <FieldLabel htmlFor={controlLabelInput}>Label</FieldLabel>
            <Input
              id={controlLabelInput}
              value={control.label ?? ""}
              placeholder="Customer segment"
              onChange={(event) => onChange({ ...control, label: event.target.value })}
            />
          </Field>
        </div>
        <div className="grid min-w-0 gap-3 sm:grid-cols-[minmax(0,1fr)_auto]">
          <Field data-presentation-path={pathPrefix ? `${pathPrefix}.type` : undefined}>
            <FieldLabel>Input type</FieldLabel>
            <Select
              value={type}
              onValueChange={(next) => {
                const nextType = next as AuthoredControlType;
                const range = defaultAuthoredControlRange(nextType);
                onChange({
                  ...control,
                  type: nextType,
                  default: defaultAuthoredControlValue(nextType),
                  min: range.min,
                  max: range.max,
                  step: range.step,
                  options:
                    nextType === "select" || nextType === "multi_select"
                      ? { values: [] }
                      : undefined,
                });
              }}
            >
              <SelectTrigger className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  {AUTHORED_CONTROL_TYPES.map((candidate) => (
                    <SelectItem key={candidate} value={candidate}>
                      {AUTHORED_CONTROL_TYPE_LABELS[candidate]}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>
          </Field>
          <div className="flex items-end justify-end">
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={`Delete control ${control.id || "untitled"}`}
              onClick={onDelete}
            >
              <Trash2 />
            </Button>
          </div>
        </div>
        <AuthoredControlValueField
          control={control}
          value={control.default}
          label="Default value"
          options={
            resolvedOptions ??
            (control.options?.values !== undefined ? authoredControlOptions(control) : undefined)
          }
          idScope={`${idPrefix}-default`}
          path={pathPrefix ? `${pathPrefix}.default` : undefined}
          onChange={(value) => onChange({ ...control, default: value })}
        />
        {type === "slider" ? (
          <div className="grid min-w-0 gap-3 sm:grid-cols-3">
            <Field data-presentation-path={pathPrefix ? `${pathPrefix}.min` : undefined}>
              <FieldLabel htmlFor={`${idPrefix}-${control.id}-min`}>Minimum</FieldLabel>
              <Input
                id={`${idPrefix}-${control.id}-min`}
                type="number"
                value={control.min ?? 0}
                onChange={(event) => onChange({ ...control, min: Number(event.target.value || 0) })}
              />
            </Field>
            <Field data-presentation-path={pathPrefix ? `${pathPrefix}.max` : undefined}>
              <FieldLabel htmlFor={`${idPrefix}-${control.id}-max`}>Maximum</FieldLabel>
              <Input
                id={`${idPrefix}-${control.id}-max`}
                type="number"
                value={control.max ?? 100}
                onChange={(event) => onChange({ ...control, max: Number(event.target.value || 0) })}
              />
            </Field>
            <Field data-presentation-path={pathPrefix ? `${pathPrefix}.step` : undefined}>
              <FieldLabel htmlFor={`${idPrefix}-${control.id}-step`}>Step</FieldLabel>
              <Input
                id={`${idPrefix}-${control.id}-step`}
                type="number"
                min="0"
                step="any"
                value={control.step ?? 1}
                onChange={(event) =>
                  onChange({ ...control, step: Number(event.target.value || 0) })
                }
              />
            </Field>
          </div>
        ) : null}
        {supportsOptions ? (
          <div className="grid min-w-0 gap-3 sm:grid-cols-[9rem_minmax(0,1fr)]">
            <Field data-presentation-path={pathPrefix ? `${pathPrefix}.options` : undefined}>
              <FieldLabel>Options</FieldLabel>
              <Select
                value={optionMode}
                onValueChange={(mode) => {
                  if (mode === "none") onChange({ ...control, options: undefined });
                  if (mode === "static") onChange({ ...control, options: { values: [] } });
                  if (mode === "dataset") {
                    onChange({
                      ...control,
                      options: { dataset: datasets[0]?.id ?? "", value_field: "" },
                    });
                  }
                }}
              >
                <SelectTrigger className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="none">Unconstrained</SelectItem>
                    <SelectItem value="static">Static values</SelectItem>
                    {datasets.length > 0 ? (
                      <SelectItem value="dataset">From dataset</SelectItem>
                    ) : null}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </Field>
            {optionMode === "static" ? (
              <Field
                data-presentation-path={pathPrefix ? `${pathPrefix}.options.values` : undefined}
              >
                <FieldLabel htmlFor={`${idPrefix}-static-${control.id}`}>Static values</FieldLabel>
                <Input
                  id={`${idPrefix}-static-${control.id}`}
                  value={(control.options?.values ?? []).join(", ")}
                  placeholder="north, south, east, west"
                  onChange={(event) =>
                    onChange({
                      ...control,
                      options: { values: normalizeAuthoredControlList(event.target.value) },
                    })
                  }
                />
                <FieldDescription>Comma-separated values shown by the control.</FieldDescription>
              </Field>
            ) : optionMode === "dataset" ? (
              <div className="grid min-w-0 gap-3 lg:grid-cols-3">
                <Field
                  data-presentation-path={pathPrefix ? `${pathPrefix}.options.dataset` : undefined}
                >
                  <FieldLabel>Dataset</FieldLabel>
                  <Select
                    value={optionDataset}
                    onValueChange={(dataset) =>
                      onChange({
                        ...control,
                        options: { dataset, value_field: "", label_field: "" },
                      })
                    }
                  >
                    <SelectTrigger className="w-full">
                      <SelectValue placeholder="Choose a dataset" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        {datasets.map((dataset) => (
                          <SelectItem key={dataset.id} value={dataset.id}>
                            {dataset.label ?? dataset.id}
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                </Field>
                <AuthoredControlColumnSelect
                  label="Value field"
                  value={control.options?.value_field ?? ""}
                  columns={optionColumns}
                  path={pathPrefix ? `${pathPrefix}.options.value_field` : undefined}
                  onChange={(value_field) =>
                    onChange({
                      ...control,
                      options: { ...control.options, dataset: optionDataset, value_field },
                    })
                  }
                />
                <AuthoredControlColumnSelect
                  label="Label field"
                  value={control.options?.label_field ?? ""}
                  columns={optionColumns}
                  optional
                  path={pathPrefix ? `${pathPrefix}.options.label_field` : undefined}
                  onChange={(label_field) =>
                    onChange({
                      ...control,
                      options: {
                        ...control.options,
                        dataset: optionDataset,
                        value_field: control.options?.value_field ?? "",
                        label_field,
                      },
                    })
                  }
                />
              </div>
            ) : null}
          </div>
        ) : null}
      </FieldGroup>
    </div>
  );
}

export function AuthoredControlValueField({
  control,
  value,
  options: providedOptions,
  label,
  description,
  idScope = "runtime",
  compact = false,
  className,
  path,
  onChange,
}: {
  control: AuthoredControlDefinition;
  value: unknown;
  options?: AuthoredControlOption[];
  label?: string;
  description?: string | false;
  idScope?: string;
  compact?: boolean;
  className?: string;
  path?: string;
  onChange: (value: unknown) => void;
}) {
  const type = authoredControlType(control.type);
  const title = label ?? control.label ?? control.id;
  const detail = description === undefined ? (label ? false : control.id) : description;
  const id = `${idScope}-${control.id}`;
  const hasProvidedOptions = providedOptions !== undefined;
  const options = providedOptions ?? authoredControlOptions(control);

  if (type === "slider") {
    const { min, max, step } = authoredSliderBounds(control);
    const numericValue = typeof value === "number" && Number.isFinite(value) ? value : min;
    const boundedValue = Math.min(max, Math.max(min, numericValue));
    return (
      <Field
        data-presentation-path={path}
        className={cn(compact ? "min-w-48 gap-1" : "gap-2", className)}
      >
        <div className="flex min-w-0 items-baseline justify-between gap-3">
          <FieldLabel htmlFor={id}>{title}</FieldLabel>
          <span className="shrink-0 font-mono text-xs tabular-nums text-muted-foreground">
            {boundedValue}
          </span>
        </div>
        <Slider
          id={id}
          aria-label={title}
          min={min}
          max={max}
          step={step}
          value={[boundedValue]}
          onValueChange={(next) => onChange(next[0] ?? min)}
        />
        {detail ? <FieldDescription>{detail}</FieldDescription> : null}
      </Field>
    );
  }

  if (type === "boolean") {
    return (
      <Field
        data-presentation-path={path}
        orientation="horizontal"
        className={cn(
          "items-center justify-between",
          !compact && "rounded-md border px-3 py-2",
          compact && "h-7 w-auto gap-2 rounded-md border bg-background px-2",
          className,
        )}
      >
        <div className="min-w-0">
          <FieldLabel htmlFor={id} className={cn(compact && "text-xs font-normal")}>
            {title}
          </FieldLabel>
          {detail ? <FieldDescription className="mt-0">{detail}</FieldDescription> : null}
        </div>
        <Switch
          id={id}
          size={compact ? "sm" : "default"}
          checked={value === true}
          onCheckedChange={onChange}
        />
      </Field>
    );
  }

  if (type === "select" && (options.length > 0 || hasProvidedOptions)) {
    const selected = comparableAuthoredControlValue(value);
    return (
      <Field data-presentation-path={path} className={cn(compact && "w-44 gap-1", className)}>
        <FieldLabel htmlFor={id}>{title}</FieldLabel>
        <Select
          disabled={options.length === 0}
          value={selected}
          onValueChange={(next) => {
            const option = options.find(
              (candidate) => comparableAuthoredControlValue(candidate.value) === next,
            );
            if (option) onChange(option.value);
          }}
        >
          <SelectTrigger id={id} className="w-full">
            <SelectValue placeholder={options.length === 0 ? "No options loaded" : "Choose…"} />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              {options.map((option) => (
                <SelectItem
                  key={comparableAuthoredControlValue(option.value)}
                  value={comparableAuthoredControlValue(option.value)}
                >
                  {option.label}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
        {detail ? <FieldDescription>{detail}</FieldDescription> : null}
      </Field>
    );
  }

  if (type === "multi_select" && (options.length > 0 || hasProvidedOptions)) {
    const selected = new Set(Array.isArray(value) ? value.map(comparableAuthoredControlValue) : []);
    return (
      <Field data-presentation-path={path} className={cn(compact && "min-w-48 gap-1", className)}>
        <FieldLabel>{title}</FieldLabel>
        <div
          role="group"
          aria-label={title}
          className={cn(
            "grid gap-2 rounded-md border p-3 sm:grid-cols-2",
            compact && "flex min-h-7 max-w-md flex-wrap gap-x-3 gap-y-1 bg-background px-2 py-1",
          )}
        >
          {options.length === 0 ? (
            <span className="text-xs text-muted-foreground">No options loaded</span>
          ) : null}
          {options.map((option) => {
            const key = comparableAuthoredControlValue(option.value);
            return (
              <label key={key} className="flex items-center gap-1.5 text-xs">
                <Checkbox
                  checked={selected.has(key)}
                  onCheckedChange={(checked) => {
                    const current = Array.isArray(value) ? [...value] : [];
                    onChange(
                      checked === true
                        ? [...current, option.value]
                        : current.filter((item) => comparableAuthoredControlValue(item) !== key),
                    );
                  }}
                />
                {option.label}
              </label>
            );
          })}
        </div>
        {detail ? <FieldDescription>{detail}</FieldDescription> : null}
      </Field>
    );
  }

  if (type === "date_range") {
    const range = Array.isArray(value) ? value.map(String) : ["", ""];
    return (
      <Field data-presentation-path={path} className={cn(compact && "gap-1", className)}>
        <FieldLabel>{title}</FieldLabel>
        <div
          role="group"
          aria-label={title}
          className={cn("grid gap-2 sm:grid-cols-2", compact && "flex items-center gap-1")}
        >
          <Input
            type="date"
            aria-label={`${title} start`}
            className={cn(compact && "w-36")}
            value={range[0] ?? ""}
            onChange={(event) => onChange([event.target.value, range[1] ?? ""])}
          />
          {compact ? <span className="text-xs text-muted-foreground">to</span> : null}
          <Input
            type="date"
            aria-label={`${title} end`}
            className={cn(compact && "w-36")}
            value={range[1] ?? ""}
            onChange={(event) => onChange([range[0] ?? "", event.target.value])}
          />
        </div>
        {detail ? <FieldDescription>{detail}</FieldDescription> : null}
      </Field>
    );
  }

  const inputValue =
    type === "multi_select" ? (Array.isArray(value) ? value : []).join(", ") : String(value ?? "");
  return (
    <Field data-presentation-path={path} className={cn(compact && "w-44 gap-1", className)}>
      <FieldLabel htmlFor={id}>{title}</FieldLabel>
      <Input
        id={id}
        type={type === "number" ? "number" : type === "date" ? "date" : "text"}
        value={inputValue}
        onChange={(event) => {
          if (type === "number") {
            onChange(event.target.value === "" ? 0 : Number(event.target.value));
          } else if (type === "multi_select") {
            onChange(normalizeAuthoredControlList(event.target.value));
          } else {
            onChange(event.target.value);
          }
        }}
      />
      {detail ? <FieldDescription>{detail}</FieldDescription> : null}
    </Field>
  );
}

function AuthoredControlColumnSelect({
  label,
  value,
  columns,
  optional = false,
  path,
  onChange,
}: {
  label: string;
  value: string;
  columns: Array<{ name: string; detail?: string }>;
  optional?: boolean;
  path?: string;
  onChange: (value: string) => void;
}) {
  return (
    <Field data-presentation-path={path}>
      <FieldLabel>{label}</FieldLabel>
      <Select
        value={value || (optional ? "__none__" : "")}
        onValueChange={(next) => onChange(next === "__none__" ? "" : next)}
      >
        <SelectTrigger className="w-full">
          <SelectValue placeholder="Choose a field" />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            {optional ? <SelectItem value="__none__">None</SelectItem> : null}
            {columns.map((column) => (
              <SelectItem key={column.name} value={column.name}>
                {column.name}
                {column.detail ? (
                  <span className="ml-1 text-muted-foreground">· {column.detail}</span>
                ) : null}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
    </Field>
  );
}
