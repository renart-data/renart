"use client";

import { Plus, SlidersHorizontal, Trash2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import type { NotebookParameter } from "@/lib/generated/api-types";

const PARAMETER_TYPES = [
  "text",
  "number",
  "boolean",
  "select",
  "multi_select",
  "date",
  "date_range",
] as const;

type ParameterType = (typeof PARAMETER_TYPES)[number];

const TYPE_LABELS: Record<ParameterType, string> = {
  text: "Text",
  number: "Number",
  boolean: "Boolean",
  select: "Select",
  multi_select: "Multi-select",
  date: "Date",
  date_range: "Date range",
};

function cloneParameters(parameters: NotebookParameter[]) {
  return JSON.parse(JSON.stringify(parameters)) as NotebookParameter[];
}

function defaultForType(type: ParameterType): unknown {
  switch (type) {
    case "number":
      return 0;
    case "boolean":
      return false;
    case "multi_select":
      return [];
    case "date":
      return new Date().toISOString().slice(0, 10);
    case "date_range": {
      const today = new Date().toISOString().slice(0, 10);
      return [today, today];
    }
    default:
      return "";
  }
}

function parameterValuesFrom(parameters: NotebookParameter[], current: Record<string, unknown>) {
  return Object.fromEntries(
    parameters.map((parameter) => [
      parameter.id,
      current[parameter.id] === undefined ? parameter.default : current[parameter.id],
    ]),
  );
}

function normalizeCommaList(value: string) {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function definitionProblem(parameters: NotebookParameter[]) {
  const seen = new Set<string>();
  for (const parameter of parameters) {
    if (!/^[a-z][a-z0-9_]*$/.test(parameter.id)) {
      return `“${parameter.id || "Untitled"}” needs a lowercase id using letters, numbers, and underscores.`;
    }
    if (seen.has(parameter.id)) {
      return `Parameter id “${parameter.id}” is used more than once.`;
    }
    seen.add(parameter.id);
  }
  return "";
}

export function NotebookParametersDialog({
  open,
  onOpenChange,
  parameters,
  values,
  onSaveDefinitions,
  onSaveValues,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  parameters: NotebookParameter[];
  values: Record<string, unknown>;
  onSaveDefinitions: (parameters: NotebookParameter[]) => Promise<void>;
  onSaveValues: (values: Record<string, unknown>) => Promise<void>;
}) {
  const [tab, setTab] = useState("values");
  const [draftParameters, setDraftParameters] = useState<NotebookParameter[]>([]);
  const [draftValues, setDraftValues] = useState<Record<string, unknown>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const wasOpenRef = useRef(false);

  useEffect(() => {
    if (open && !wasOpenRef.current) {
      const definitions = cloneParameters(parameters);
      setDraftParameters(definitions);
      setDraftValues(parameterValuesFrom(definitions, values));
      setTab(parameters.length === 0 ? "definitions" : "values");
      setError("");
    }
    wasOpenRef.current = open;
  }, [open, parameters, values]);

  const validationError = useMemo(() => definitionProblem(draftParameters), [draftParameters]);

  const updateDefinition = (index: number, patch: Partial<NotebookParameter>) => {
    setDraftParameters((current) =>
      current.map((parameter, candidate) =>
        candidate === index ? { ...parameter, ...patch } : parameter,
      ),
    );
  };

  const saveDefinitions = async () => {
    if (validationError) {
      setError(validationError);
      return;
    }
    setBusy(true);
    setError("");
    try {
      await onSaveDefinitions(draftParameters);
      setDraftValues(parameterValuesFrom(draftParameters, {}));
      setTab("values");
    } catch (saveError) {
      setError(String(saveError));
    } finally {
      setBusy(false);
    }
  };

  const saveValues = async () => {
    setBusy(true);
    setError("");
    try {
      await onSaveValues(draftValues);
      onOpenChange(false);
    } catch (saveError) {
      setError(String(saveError));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[min(760px,calc(100vh-2rem))] min-w-0 flex-col overflow-hidden sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <SlidersHorizontal className="size-4" />
            Notebook parameters
          </DialogTitle>
          <DialogDescription>
            Defaults are version-controlled. Current values are local to this Renart session.
          </DialogDescription>
        </DialogHeader>

        <Tabs value={tab} onValueChange={setTab} className="min-h-0 flex-1">
          <TabsList>
            <TabsTrigger value="values">Values</TabsTrigger>
            <TabsTrigger value="definitions">Definitions</TabsTrigger>
          </TabsList>

          <TabsContent value="values" className="min-h-0 flex-1 overflow-hidden">
            <ScrollArea className="h-full max-h-[52vh] pr-3">
              {draftParameters.length === 0 ? (
                <div className="rounded-lg border border-dashed p-6 text-center">
                  <p className="text-sm font-medium">No parameters yet</p>
                  <p className="mt-1 text-xs text-muted-foreground">
                    Add a typed definition, then use it from SQL, Python, sources, and charts.
                  </p>
                  <Button
                    className="mt-4"
                    size="sm"
                    variant="outline"
                    onClick={() => setTab("definitions")}
                  >
                    Add parameter
                  </Button>
                </div>
              ) : (
                <FieldGroup className="py-1">
                  {draftParameters.map((parameter) => (
                    <ParameterValueField
                      key={parameter.id}
                      parameter={parameter}
                      value={draftValues[parameter.id] ?? parameter.default}
                      onChange={(value) =>
                        setDraftValues((current) => ({ ...current, [parameter.id]: value }))
                      }
                    />
                  ))}
                </FieldGroup>
              )}
            </ScrollArea>
          </TabsContent>

          <TabsContent value="definitions" className="min-h-0 flex-1 overflow-hidden">
            <ScrollArea className="h-full max-h-[52vh] pr-3">
              <div className="space-y-3 py-1">
                {draftParameters.map((parameter, index) => (
                  <div key={`${parameter.id}-${index}`} className="rounded-lg border p-4">
                    <div className="mb-4 flex items-start justify-between gap-3">
                      <div className="min-w-0">
                        <p className="truncate text-sm font-medium">
                          {parameter.label || parameter.id || "New parameter"}
                        </p>
                        <p className="font-mono text-[11px] text-muted-foreground">
                          {parameter.id || "parameter_id"}
                        </p>
                      </div>
                      <Button
                        size="icon-sm"
                        variant="ghost"
                        aria-label={`Delete ${parameter.id || "parameter"}`}
                        onClick={() =>
                          setDraftParameters((current) =>
                            current.filter((_, candidate) => candidate !== index),
                          )
                        }
                      >
                        <Trash2 className="size-3.5" />
                      </Button>
                    </div>

                    <FieldGroup className="gap-3">
                      <div className="grid gap-3 sm:grid-cols-2">
                        <Field>
                          <FieldLabel htmlFor={`parameter-id-${index}`}>ID</FieldLabel>
                          <Input
                            id={`parameter-id-${index}`}
                            value={parameter.id}
                            placeholder="customer_segment"
                            onChange={(event) =>
                              updateDefinition(index, { id: event.target.value.trimStart() })
                            }
                          />
                        </Field>
                        <Field>
                          <FieldLabel htmlFor={`parameter-label-${index}`}>Label</FieldLabel>
                          <Input
                            id={`parameter-label-${index}`}
                            value={parameter.label ?? ""}
                            placeholder="Customer segment"
                            onChange={(event) =>
                              updateDefinition(index, { label: event.target.value })
                            }
                          />
                        </Field>
                      </div>
                      <Field>
                        <FieldLabel>Type</FieldLabel>
                        <Select
                          value={parameter.type}
                          onValueChange={(next) => {
                            const type = next as ParameterType;
                            updateDefinition(index, {
                              type,
                              default: defaultForType(type),
                              options:
                                type === "select" || type === "multi_select"
                                  ? { values: [] }
                                  : undefined,
                            });
                          }}
                        >
                          <SelectTrigger>
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            {PARAMETER_TYPES.map((type) => (
                              <SelectItem key={type} value={type}>
                                {TYPE_LABELS[type]}
                              </SelectItem>
                            ))}
                          </SelectContent>
                        </Select>
                      </Field>
                      <ParameterValueField
                        parameter={parameter}
                        value={parameter.default}
                        label="Default"
                        onChange={(value) => updateDefinition(index, { default: value })}
                      />
                      {parameter.type === "select" || parameter.type === "multi_select" ? (
                        <Field>
                          <FieldLabel htmlFor={`parameter-options-${index}`}>
                            Static options
                          </FieldLabel>
                          <Input
                            id={`parameter-options-${index}`}
                            value={(parameter.options?.values ?? []).join(", ")}
                            placeholder="north, south, east, west"
                            onChange={(event) =>
                              updateDefinition(index, {
                                options: { values: normalizeCommaList(event.target.value) },
                              })
                            }
                          />
                          <FieldDescription>
                            Comma-separated values shown by the control.
                          </FieldDescription>
                        </Field>
                      ) : null}
                    </FieldGroup>
                  </div>
                ))}

                <Button
                  type="button"
                  variant="outline"
                  className="w-full border-dashed"
                  onClick={() => {
                    const existing = new Set(draftParameters.map((parameter) => parameter.id));
                    let suffix = draftParameters.length + 1;
                    let id = `parameter_${suffix}`;
                    while (existing.has(id)) {
                      suffix += 1;
                      id = `parameter_${suffix}`;
                    }
                    setDraftParameters((current) => [
                      ...current,
                      { id, label: "", type: "text", default: "" },
                    ]);
                  }}
                >
                  <Plus className="size-3.5" />
                  Add parameter
                </Button>
              </div>
            </ScrollArea>
          </TabsContent>
        </Tabs>

        {error || (tab === "definitions" && validationError) ? (
          <FieldError>{error || validationError}</FieldError>
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
            Cancel
          </Button>
          {tab === "definitions" ? (
            <Button onClick={() => void saveDefinitions()} disabled={busy || !!validationError}>
              Save definitions
            </Button>
          ) : (
            <Button
              onClick={() => void saveValues()}
              disabled={busy || draftParameters.length === 0}
            >
              Apply values
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ParameterValueField({
  parameter,
  value,
  onChange,
  label,
}: {
  parameter: NotebookParameter;
  value: unknown;
  onChange: (value: unknown) => void;
  label?: string;
}) {
  const title = label ?? parameter.label ?? parameter.id;
  const id = `parameter-value-${parameter.id}-${label ? "default" : "runtime"}`;
  const options = (parameter.options?.values ?? []).map(String);

  if (parameter.type === "boolean") {
    return (
      <Field
        orientation="horizontal"
        className="items-center justify-between rounded-md border px-3 py-2"
      >
        <div>
          <FieldLabel htmlFor={id}>{title}</FieldLabel>
          {!label ? <FieldDescription className="mt-0">{parameter.id}</FieldDescription> : null}
        </div>
        <Checkbox
          id={id}
          checked={value === true}
          onCheckedChange={(checked) => onChange(checked === true)}
        />
      </Field>
    );
  }

  if (parameter.type === "select" && options.length > 0) {
    return (
      <Field>
        <FieldLabel>{title}</FieldLabel>
        <Select value={String(value ?? "")} onValueChange={onChange}>
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {options.map((option) => (
              <SelectItem key={option} value={option}>
                {option}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {!label ? <FieldDescription>{parameter.id}</FieldDescription> : null}
      </Field>
    );
  }

  if (parameter.type === "multi_select" && options.length > 0) {
    const selected = Array.isArray(value) ? value.map(String) : [];
    return (
      <Field>
        <FieldLabel>{title}</FieldLabel>
        <div className="grid gap-2 rounded-md border p-3 sm:grid-cols-2">
          {options.map((option) => (
            <label key={option} className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={selected.includes(option)}
                onCheckedChange={(checked) =>
                  onChange(
                    checked
                      ? [...selected, option]
                      : selected.filter((candidate) => candidate !== option),
                  )
                }
              />
              {option}
            </label>
          ))}
        </div>
        {!label ? <FieldDescription>{parameter.id}</FieldDescription> : null}
      </Field>
    );
  }

  if (parameter.type === "date_range") {
    const range = Array.isArray(value) ? value.map(String) : ["", ""];
    return (
      <Field>
        <FieldLabel>{title}</FieldLabel>
        <div className="grid gap-2 sm:grid-cols-2">
          <Input
            type="date"
            aria-label={`${title} start`}
            value={range[0] ?? ""}
            onChange={(event) => onChange([event.target.value, range[1] ?? ""])}
          />
          <Input
            type="date"
            aria-label={`${title} end`}
            value={range[1] ?? ""}
            onChange={(event) => onChange([range[0] ?? "", event.target.value])}
          />
        </div>
        {!label ? <FieldDescription>{parameter.id}</FieldDescription> : null}
      </Field>
    );
  }

  const inputType =
    parameter.type === "number" ? "number" : parameter.type === "date" ? "date" : "text";
  const inputValue =
    parameter.type === "multi_select"
      ? (Array.isArray(value) ? value : []).join(", ")
      : String(value ?? "");
  return (
    <Field>
      <FieldLabel htmlFor={id}>{title}</FieldLabel>
      <Input
        id={id}
        type={inputType}
        value={inputValue}
        onChange={(event) => {
          if (parameter.type === "number") {
            onChange(event.target.value === "" ? 0 : Number(event.target.value));
          } else if (parameter.type === "multi_select") {
            onChange(normalizeCommaList(event.target.value));
          } else {
            onChange(event.target.value);
          }
        }}
      />
      {!label ? <FieldDescription>{parameter.id}</FieldDescription> : null}
    </Field>
  );
}
