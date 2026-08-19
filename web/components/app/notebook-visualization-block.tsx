"use client";

import type { Monaco } from "@monaco-editor/react";
import type * as MonacoNS from "monaco-editor";
import {
  AreaChart,
  BarChart3,
  Check,
  CircleDot,
  Hash,
  LineChart,
  Loader2,
  PieChart,
  Plus,
  Save,
  Table2,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";

import { NotebookVisualizationRenderer } from "@/components/app/notebook-viz";
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";
import {
  checkNotebookVisualization,
  NotebookCellRunResult,
  NotebookVisualizationCheckResult,
  NotebookVisualizationDefinition,
  PresentationResolvedColumn,
  VisualizationFieldEncoding,
} from "@/lib/api-notebooks";
import type { NotebookVisualization } from "@/lib/generated/api-types";
import { loadMonacoEditorModule } from "@/lib/load-monaco-editor";
import { defineBruinMonacoThemes } from "@/lib/monaco-theme";
import type { WebAsset } from "@/lib/types";
import { cn } from "@/lib/utils";

import { AppPanel } from "./app-primitives";

const MonacoEditor = lazy(async () => {
  const module = await loadMonacoEditorModule();
  return { default: module.default };
});

const VISUALIZATION_TYPES = [
  { value: "table", label: "Table", icon: Table2 },
  { value: "kpi", label: "KPI", icon: Hash },
  { value: "bar", label: "Bar", icon: BarChart3 },
  { value: "line", label: "Line", icon: LineChart },
  { value: "area", label: "Area", icon: AreaChart },
  { value: "scatter", label: "Scatter", icon: CircleDot },
  { value: "pie", label: "Pie", icon: PieChart },
  { value: "donut", label: "Donut", icon: PieChart },
] as const;

type VisualizationType = (typeof VISUALIZATION_TYPES)[number]["value"];

export function normalizeVisualizationDefinition(
  raw: Record<string, unknown>,
): NotebookVisualizationDefinition {
  const authoredType = typeof raw.type === "string" ? raw.type : "table";
  const type = VISUALIZATION_TYPES.some((option) => option.value === authoredType)
    ? (authoredType as VisualizationType)
    : "table";
  const encoding =
    raw.encoding && typeof raw.encoding === "object" && !Array.isArray(raw.encoding)
      ? (raw.encoding as NotebookVisualizationDefinition["encoding"])
      : {};
  return {
    ...(raw as NotebookVisualizationDefinition),
    version: typeof raw.version === "number" ? raw.version : 1,
    type,
    encoding: {
      ...encoding,
      y: Array.isArray(encoding?.y) ? encoding.y : [],
    },
  };
}

export function visualizationDefinitionRecord(
  definition: NotebookVisualizationDefinition,
): Record<string, unknown> {
  return definition as unknown as Record<string, unknown>;
}

function cellName(cells: WebAsset[], id: string): string {
  return cells.find((cell) => cell.cell_id === id)?.name ?? id;
}

export function NotebookVisualizationBlockCard({
  notebookId,
  blockId,
  visualization,
  cells,
  results,
  busy,
  onSave,
  onDelete,
}: {
  notebookId: string;
  blockId: string;
  visualization: NotebookVisualization;
  cells: WebAsset[];
  results: Record<string, NotebookCellRunResult>;
  busy: boolean;
  onSave: (source: string, definition: Record<string, unknown>) => Promise<boolean>;
  onDelete: () => Promise<void>;
}) {
  const initialDefinition = useMemo(
    () => normalizeVisualizationDefinition(visualization.definition),
    [visualization.definition],
  );
  const [mode, setMode] = useState<"visual" | "definition">("visual");
  const [source, setSource] = useState(visualization.source);
  const [definition, setDefinition] = useState(initialDefinition);
  const [definitionYAML, setDefinitionYAML] = useState("");
  const [canonicalYAML, setCanonicalYAML] = useState("");
  const [check, setCheck] = useState<NotebookVisualizationCheckResult | null>(null);
  const [checking, setChecking] = useState(true);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const checkSequence = useRef(0);

  useEffect(() => {
    setSource(visualization.source);
    setDefinition(initialDefinition);
    setDefinitionYAML("");
    setCanonicalYAML("");
  }, [initialDefinition, visualization.source]);

  const runCheck = useCallback(
    async (input: {
      source: string;
      definition?: Record<string, unknown>;
      definition_yaml?: string;
    }) => {
      const sequence = ++checkSequence.current;
      setChecking(true);
      try {
        const next = await checkNotebookVisualization(notebookId, input);
        if (sequence !== checkSequence.current) return;
        setCheck(next);
        if (input.definition && next.definition_yaml) {
          setCanonicalYAML(next.definition_yaml);
          if (!definitionYAML) setDefinitionYAML(next.definition_yaml);
        }
      } catch (error) {
        if (sequence !== checkSequence.current) return;
        setCheck({
          status: "ok",
          source: input.source,
          schema: {
            source: { kind: "notebook", artifact_id: notebookId },
            columns: [],
            complete: false,
            sampled: false,
          },
          findings: [
            {
              code: "visualization-check-failed",
              severity: "error",
              message: error instanceof Error ? error.message : "Visualization check failed.",
            },
          ],
          can_apply: false,
        });
      } finally {
        if (sequence === checkSequence.current) setChecking(false);
      }
    },
    [definitionYAML, notebookId],
  );

  useEffect(() => {
    const timer = window.setTimeout(() => {
      if (mode === "definition") {
        void runCheck({ source, definition_yaml: definitionYAML });
      } else {
        void runCheck({ source, definition: visualizationDefinitionRecord(definition) });
      }
    }, 180);
    return () => window.clearTimeout(timer);
  }, [definition, definitionYAML, mode, runCheck, source]);

  const previewDefinition = useMemo(() => {
    if (mode === "definition" && check?.definition)
      return normalizeVisualizationDefinition(check.definition);
    return definition;
  }, [check?.definition, definition, mode]);
  const sourceResult = results[source];
  const dirty =
    source !== visualization.source ||
    JSON.stringify(visualizationDefinitionRecord(previewDefinition)) !==
      JSON.stringify(visualizationDefinitionRecord(initialDefinition));
  const canSave = Boolean(
    check?.can_apply && check.definition && dirty && !checking && !saving && !busy,
  );

  const save = async () => {
    if (!canSave || !check?.definition) return;
    setSaving(true);
    const saved = await onSave(source, check.definition);
    if (saved) {
      const normalized = normalizeVisualizationDefinition(check.definition);
      setDefinition(normalized);
      const yaml = check.definition_yaml ?? canonicalYAML;
      setCanonicalYAML(yaml);
      setDefinitionYAML(yaml);
    }
    setSaving(false);
  };

  return (
    <AppPanel className="border-violet-500/25 bg-violet-500/[0.025]">
      <DelimitedCardHeader>
        <CircleDot className="size-3.5 text-violet-500" />
        <DelimitedCardTitle className="truncate">
          {definition.title?.trim() || "Visualization"}
        </DelimitedCardTitle>
        <Badge variant="outline" className="font-normal">
          {definition.type}
        </Badge>
        <span className="ml-auto truncate text-[11px] text-muted-foreground">
          from <span className="font-mono">{cellName(cells, source)}</span>
        </span>
        <Button
          variant="ghost"
          size="icon-sm"
          disabled={busy || deleting}
          aria-label="Delete visualization"
          onClick={() => {
            setDeleting(true);
            void onDelete().finally(() => setDeleting(false));
          }}
        >
          {deleting ? <Loader2 className="animate-spin" /> : <Trash2 />}
        </Button>
      </DelimitedCardHeader>
      <DelimitedCardContent className="space-y-4">
        <div className="grid min-w-0 gap-4 lg:grid-cols-[minmax(18rem,0.85fr)_minmax(0,1.15fr)]">
          <div className="min-w-0 rounded-lg border bg-background/70 p-3">
            <Field className="mb-3">
              <FieldLabel>Data source</FieldLabel>
              <FieldCombobox
                value={source}
                items={cells.map((cell) => ({ value: cell.cell_id ?? "", label: cell.name }))}
                placeholder="Choose a notebook result"
                onChange={setSource}
              />
            </Field>
            <Tabs
              value={mode}
              onValueChange={(value) => {
                const next = value as "visual" | "definition";
                if (next === "definition") setDefinitionYAML(canonicalYAML);
                if (next === "visual" && check?.definition && check.can_apply) {
                  setDefinition(normalizeVisualizationDefinition(check.definition));
                }
                setMode(next);
              }}
            >
              <TabsList>
                <TabsTrigger value="visual">Visual</TabsTrigger>
                <TabsTrigger value="definition" disabled={!canonicalYAML}>
                  Definition
                </TabsTrigger>
              </TabsList>
              <TabsContent value="visual" className="pt-3">
                <VisualizationBuilder
                  definition={definition}
                  columns={check?.schema.columns ?? []}
                  onChange={setDefinition}
                />
              </TabsContent>
              <TabsContent value="definition" className="pt-3">
                <VisualizationDefinitionEditor
                  blockId={blockId}
                  value={definitionYAML}
                  onChange={setDefinitionYAML}
                />
              </TabsContent>
            </Tabs>
          </div>
          <div className="min-w-0 rounded-lg border bg-background/70 p-3">
            <div className="mb-3 flex items-center gap-2">
              <span className="text-xs font-medium">Preview</span>
              {check?.schema.sampled ? (
                <Badge variant="outline" className="border-amber-500/30 text-amber-700">
                  sampled data
                </Badge>
              ) : null}
              {checking ? (
                <Loader2 className="ml-auto size-3.5 animate-spin text-muted-foreground" />
              ) : null}
            </div>
            {sourceResult?.status === "ok" &&
            sourceResult.columns.length > 0 &&
            check?.can_apply ? (
              <NotebookVisualizationRenderer definition={previewDefinition} result={sourceResult} />
            ) : (
              <div className="flex min-h-56 items-center justify-center rounded-lg border border-dashed bg-muted/20 px-6 text-center text-xs text-muted-foreground">
                {sourceResult?.status === "error"
                  ? "The source cell failed. Fix and run it to preview this visualization."
                  : "Run the source cell to preview this visualization. Its fields can still be checked statically."}
              </div>
            )}
          </div>
        </div>

        {check?.findings.length ? (
          <div className="space-y-1" aria-live="polite">
            {check.findings.map((finding, index) => (
              <div
                key={`${finding.code}:${finding.path ?? ""}:${index}`}
                className={cn(
                  "flex items-start gap-2 rounded-md border px-2.5 py-2 text-xs",
                  finding.severity === "error"
                    ? "border-red-500/25 bg-red-500/5 text-red-700 dark:text-red-300"
                    : "border-amber-500/25 bg-amber-500/5 text-amber-700 dark:text-amber-300",
                )}
              >
                {finding.severity === "error" ? <TriangleAlert /> : <TriangleAlert />}
                <span className="min-w-0">
                  {finding.message}
                  {finding.path ? (
                    <span className="ml-1 font-mono text-[10px] opacity-70">{finding.path}</span>
                  ) : null}
                </span>
              </div>
            ))}
          </div>
        ) : null}

        <div className="flex items-center justify-end gap-2 border-t pt-3">
          {dirty && !check?.can_apply ? (
            <span className="mr-auto text-[11px] text-muted-foreground">
              Resolve the definition errors before applying.
            </span>
          ) : dirty ? (
            <span className="mr-auto text-[11px] text-muted-foreground">
              Changes stay local to this block until applied.
            </span>
          ) : null}
          <Button size="sm" disabled={!canSave} onClick={() => void save()}>
            {saving ? <Loader2 className="animate-spin" /> : dirty ? <Save /> : <Check />}
            {dirty ? "Apply visualization" : "Saved"}
          </Button>
        </div>
      </DelimitedCardContent>
    </AppPanel>
  );
}

export function VisualizationBuilder({
  definition,
  columns,
  onChange,
}: {
  definition: NotebookVisualizationDefinition;
  columns: PresentationResolvedColumn[];
  onChange: (definition: NotebookVisualizationDefinition) => void;
}) {
  const patch = (next: Partial<NotebookVisualizationDefinition>) =>
    onChange({ ...definition, ...next });
  const encoding = definition.encoding ?? { y: [] };
  const patchEncoding = (next: Partial<NonNullable<NotebookVisualizationDefinition["encoding"]>>) =>
    patch({ encoding: { ...encoding, ...next } });
  const y = encoding.y ?? [];
  const allFields = columns.map((column) => ({
    value: column.name,
    label: column.physical_type ? `${column.name} · ${column.physical_type}` : column.name,
  }));
  const numericFields = columns
    .filter((column) => column.semantic_type === "numeric" || column.semantic_type === "unknown")
    .map((column) => ({ value: column.name, label: column.name }));
  const measureFields = numericFields.length > 0 ? numericFields : allFields;
  const categoryFields = columns
    .filter((column) =>
      ["categorical", "boolean", "temporal", "unknown"].includes(column.semantic_type),
    )
    .map((column) => ({ value: column.name, label: column.name }));

  return (
    <FieldGroup className="gap-4">
      <Field>
        <FieldLabel>Chart type</FieldLabel>
        <div className="grid grid-cols-4 gap-1.5">
          {VISUALIZATION_TYPES.map((option) => {
            const Icon = option.icon;
            return (
              <Button
                key={option.value}
                type="button"
                size="sm"
                variant={definition.type === option.value ? "secondary" : "outline"}
                className="min-w-0 justify-start px-2"
                onClick={() => patch({ type: option.value })}
              >
                <Icon />
                <span className="truncate">{option.label}</span>
              </Button>
            );
          })}
        </div>
      </Field>
      <Field>
        <FieldLabel htmlFor="visualization-title">Title</FieldLabel>
        <Input
          id="visualization-title"
          value={definition.title ?? ""}
          placeholder="Optional title"
          onChange={(event) => patch({ title: event.target.value || undefined })}
        />
      </Field>

      {definition.type === "kpi" ? (
        <>
          <EncodingField
            label="Value"
            value={definition.value}
            items={measureFields}
            onChange={(value) => patch({ value })}
          />
          <EncodingField
            label="Comparison"
            value={definition.compare}
            items={measureFields}
            optional
            onChange={(value) => patch({ compare: value })}
          />
        </>
      ) : definition.type !== "table" ? (
        <>
          <EncodingField
            label={definition.type === "pie" || definition.type === "donut" ? "Category" : "X axis"}
            value={encoding.x}
            items={
              definition.type === "scatter"
                ? columns
                    .filter((column) =>
                      ["numeric", "temporal", "unknown"].includes(column.semantic_type),
                    )
                    .map((column) => ({ value: column.name, label: column.name }))
                : allFields
            }
            onChange={(value) => patchEncoding({ x: value })}
          />
          <Field>
            <div className="flex items-center justify-between">
              <FieldLabel>{definition.type === "scatter" ? "Y axis" : "Measures"}</FieldLabel>
              {definition.type !== "scatter" ? (
                <Button
                  type="button"
                  size="xs"
                  variant="ghost"
                  onClick={() => patchEncoding({ y: [...y, { field: "" }] })}
                >
                  <Plus /> Add measure
                </Button>
              ) : null}
            </div>
            <div className="space-y-2">
              {(y.length > 0 ? y : [{ field: "" }]).map((measure, index) => (
                <div key={index} className="flex min-w-0 items-start gap-1.5">
                  <EncodingControls
                    value={measure}
                    items={measureFields}
                    placeholder="Choose a numeric field"
                    onChange={(nextMeasure) => {
                      const next = y.length > 0 ? [...y] : [{ field: "" }];
                      next[index] = nextMeasure;
                      patchEncoding({ y: next });
                    }}
                  />
                  {y.length > 1 ? (
                    <Button
                      type="button"
                      size="icon-sm"
                      variant="ghost"
                      aria-label="Remove measure"
                      onClick={() =>
                        patchEncoding({ y: y.filter((_, candidate) => candidate !== index) })
                      }
                    >
                      <Trash2 />
                    </Button>
                  ) : null}
                </div>
              ))}
            </div>
          </Field>
          {!["pie", "donut", "scatter"].includes(definition.type) ? (
            <EncodingField
              label="Series"
              value={encoding.series}
              items={categoryFields.length > 0 ? categoryFields : allFields}
              optional
              onChange={(value) => patchEncoding({ series: value })}
            />
          ) : null}
        </>
      ) : (
        <FieldDescription>
          The table shows all fields by default. Use the Definition editor to select or reorder a
          subset.
        </FieldDescription>
      )}

      <div className="grid gap-3 sm:grid-cols-2">
        <Field>
          <FieldLabel htmlFor="visualization-limit">Preview row limit</FieldLabel>
          <Input
            id="visualization-limit"
            type="number"
            min={1}
            max={1000}
            value={definition.presentation_limit ?? 200}
            onChange={(event) =>
              patch({ presentation_limit: Math.max(1, Number(event.target.value) || 1) })
            }
          />
        </Field>
        <div className="space-y-2 pt-1">
          <CheckField
            label="Require complete data"
            checked={definition.require_complete ?? false}
            onChange={(checked) => patch({ require_complete: checked })}
          />
          {!["table", "kpi", "pie", "donut", "scatter"].includes(definition.type) ? (
            <CheckField
              label="Stack series"
              checked={definition.stacked ?? false}
              onChange={(checked) => patch({ stacked: checked })}
            />
          ) : null}
        </div>
      </div>
    </FieldGroup>
  );
}

function EncodingField({
  label,
  value,
  items,
  optional,
  onChange,
}: {
  label: string;
  value?: VisualizationFieldEncoding;
  items: Array<{ value: string; label: string }>;
  optional?: boolean;
  onChange: (value: VisualizationFieldEncoding | undefined) => void;
}) {
  return (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      <EncodingControls
        value={value ?? { field: "" }}
        items={items}
        placeholder={optional ? "None" : "Choose a field"}
        allowClear={optional}
        onChange={(next) => onChange(next.field ? next : undefined)}
      />
    </Field>
  );
}

function EncodingControls({
  value,
  items,
  placeholder,
  allowClear = false,
  onChange,
}: {
  value: VisualizationFieldEncoding;
  items: Array<{ value: string; label: string }>;
  placeholder: string;
  allowClear?: boolean;
  onChange: (value: VisualizationFieldEncoding) => void;
}) {
  return (
    <div className="grid min-w-0 flex-1 gap-1.5 sm:grid-cols-[minmax(0,1fr)_minmax(6rem,0.55fr)_7rem]">
      <FieldCombobox
        value={value.field}
        items={items}
        placeholder={placeholder}
        allowClear={allowClear}
        onChange={(field) => onChange({ ...value, field })}
      />
      <Input
        aria-label="Field label"
        value={value.label ?? ""}
        placeholder="Label"
        className="h-8 text-xs"
        onChange={(event) => onChange({ ...value, label: event.target.value || undefined })}
      />
      <Select
        value={value.format || "__auto__"}
        onValueChange={(format) =>
          onChange({ ...value, format: format === "__auto__" ? undefined : format })
        }
      >
        <SelectTrigger size="sm" aria-label="Field format" className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent align="end">
          <SelectItem value="__auto__">Auto</SelectItem>
          <SelectItem value="number">Number</SelectItem>
          <SelectItem value="currency">Currency</SelectItem>
          <SelectItem value="percent">Percent</SelectItem>
          <SelectItem value="date">Date</SelectItem>
          <SelectItem value="datetime">Date & time</SelectItem>
        </SelectContent>
      </Select>
    </div>
  );
}

function CheckField({
  label,
  checked,
  onChange,
}: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className="flex cursor-pointer items-center gap-2 text-xs">
      <Checkbox checked={checked} onCheckedChange={(value) => onChange(value === true)} />
      {label}
    </label>
  );
}

function FieldCombobox({
  value,
  items,
  placeholder,
  allowClear = false,
  onChange,
}: {
  value: string;
  items: Array<{ value: string; label: string }>;
  placeholder: string;
  allowClear?: boolean;
  onChange: (value: string) => void;
}) {
  const selected = items.find((item) => item.value === value) ?? null;
  const [inputValue, setInputValue] = useState(selected?.label ?? "");
  useEffect(() => setInputValue(selected?.label ?? ""), [selected?.label]);
  return (
    <Combobox
      autoHighlight
      items={items}
      value={selected}
      inputValue={inputValue}
      itemToStringLabel={(item: { value: string; label: string }) => item.label}
      itemToStringValue={(item: { value: string; label: string }) => item.value}
      isItemEqualToValue={(left, right) => left.value === right.value}
      onInputValueChange={(next, details) => {
        if (details.reason !== "item-press") setInputValue(next);
      }}
      onValueChange={(next) => {
        onChange(next?.value ?? "");
        setInputValue(next?.label ?? "");
      }}
    >
      <ComboboxInput
        aria-label={placeholder}
        placeholder={placeholder}
        className="h-8 w-full text-xs"
        showClear={allowClear && Boolean(value)}
      />
      <ComboboxContent>
        <ComboboxEmpty>No matching fields.</ComboboxEmpty>
        <ComboboxList>
          {(item: { value: string; label: string }) => (
            <ComboboxItem key={item.value} value={item}>
              <span className="truncate">{item.label}</span>
            </ComboboxItem>
          )}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  );
}

function VisualizationDefinitionEditor({
  blockId,
  value,
  onChange,
}: {
  blockId: string;
  value: string;
  onChange: (value: string) => void;
}) {
  const { monacoTheme } = useWorkspaceTheme();
  const completionDisposable = useRef<MonacoNS.IDisposable | null>(null);
  useEffect(() => () => completionDisposable.current?.dispose(), []);
  const beforeMount = useCallback((monaco: Monaco) => defineBruinMonacoThemes(monaco), []);
  const onMount = useCallback(
    (_: MonacoNS.editor.IStandaloneCodeEditor, monaco: Monaco) => {
      completionDisposable.current?.dispose();
      completionDisposable.current = monaco.languages.registerCompletionItemProvider("yaml", {
        provideCompletionItems: (
          model: MonacoNS.editor.ITextModel,
          position: MonacoNS.Position,
        ) => {
          if (!model.uri.path.includes(`/notebook-visualization/${blockId}.yml`))
            return { suggestions: [] };
          const range = new monaco.Range(
            position.lineNumber,
            position.column,
            position.lineNumber,
            position.column,
          );
          return {
            suggestions: [
              "version",
              "type",
              "title",
              "encoding",
              "columns",
              "value",
              "compare",
              "stacked",
              "show_legend",
              "require_complete",
              "presentation_limit",
              "x",
              "y",
              "series",
              "color",
              "tooltip",
              "field",
              "label",
              "format",
            ].map((label) => ({
              label,
              kind: monaco.languages.CompletionItemKind.Property,
              insertText: `${label}: `,
              range,
            })),
          };
        },
      });
    },
    [blockId],
  );
  return (
    <div className="h-80 overflow-hidden rounded-md border bg-background">
      <Suspense
        fallback={
          <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
            Loading definition editor…
          </div>
        }
      >
        <MonacoEditor
          aria-label="Visualization definition YAML"
          language="yaml"
          path={`inmemory://renart/notebook-visualization/${blockId}.yml`}
          value={value}
          theme={monacoTheme}
          beforeMount={beforeMount}
          onMount={onMount}
          onChange={(next) => onChange(next ?? "")}
          options={{
            automaticLayout: true,
            fontSize: 12,
            lineNumbers: "on",
            lineNumbersMinChars: 3,
            minimap: { enabled: false },
            padding: { top: 8, bottom: 8 },
            scrollBeyondLastLine: false,
            tabSize: 2,
            wordWrap: "on",
          }}
        />
      </Suspense>
    </div>
  );
}
