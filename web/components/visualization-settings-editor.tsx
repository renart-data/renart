"use client";

import Editor, { type Monaco } from "@monaco-editor/react";
import { BarChart3, EyeOff, FileText, Table2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import {
  Combobox,
  ComboboxChip,
  ComboboxChips,
  ComboboxChipsInput,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxInput,
  ComboboxItem,
  ComboboxList,
  ComboboxValue,
  useComboboxAnchor,
} from "@/components/ui/combobox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { resolveChartSelection } from "@/lib/asset-visualization";
import { VISUALIZATION_META_KEYS } from "@/lib/visualization-meta";

type VisualizationView = "none" | "chart" | "table" | "markdown";

type Props = {
  meta?: Record<string, string>;
  columnNames: string[];
  previewRows?: Record<string, unknown>[];
  monacoTheme: string;
  disabled?: boolean;
  onSave: (meta: Record<string, string>) => void;
};

export function VisualizationSettingsEditor({
  meta,
  columnNames,
  previewRows = [],
  monacoTheme,
  disabled = false,
  onSave,
}: Props) {
  const markdownMonacoRef = useRef<Monaco | null>(null);
  const markdownEditorUriRef = useRef<string | null>(null);
  const markdownCompletionDisposableRef = useRef<{ dispose(): void } | null>(
    null
  );
  const initialView = useMemo<VisualizationView>(() => {
    const nextView = (meta?.web_view ?? "").trim().toLowerCase();
    if (
      nextView === "chart" ||
      nextView === "table" ||
      nextView === "markdown"
    ) {
      return nextView;
    }
    return "none";
  }, [meta?.web_view]);

  const [view, setView] = useState<VisualizationView>(initialView);
  const [chartType, setChartType] = useState(
    (meta?.web_chart_type ?? "line").trim() || "line"
  );
  const [chartX, setChartX] = useState(meta?.web_chart_x ?? "");
  const [chartSeries, setChartSeries] = useState(meta?.web_chart_series ?? "");
  const [chartSeriesList, setChartSeriesList] = useState<string[]>(
    parseCSV(meta?.web_chart_series ?? "")
  );
  const [chartTitle, setChartTitle] = useState(meta?.web_chart_title ?? "");
  const [tableColumns, setTableColumns] = useState(
    meta?.web_table_columns ?? ""
  );
  const [tableColumnsList, setTableColumnsList] = useState<string[]>(
    parseCSV(meta?.web_table_columns ?? "")
  );
  const [tableLimit, setTableLimit] = useState(meta?.web_table_limit ?? "");
  const [tableDense, setTableDense] = useState(
    (meta?.web_table_dense ?? "").trim().toLowerCase() === "true"
  );
  const [markdownColumn, setMarkdownColumn] = useState(
    meta?.web_markdown_column ?? ""
  );
  const [markdownTemplate, setMarkdownTemplate] = useState(
    meta?.web_markdown_template ?? ""
  );

  const sortedColumns = useMemo(() => [...columnNames].sort(), [columnNames]);
  const hasKnownSchema = sortedColumns.length > 0;
  const inferredChartSelection = useMemo(
    () => resolveChartSelection(undefined, previewRows, sortedColumns),
    [previewRows, sortedColumns]
  );

  const chartSeriesValue = hasKnownSchema
    ? compactUnique(chartSeriesList).join(",")
    : chartSeries.trim();
  const tableColumnsValue = hasKnownSchema
    ? compactUnique(tableColumnsList).join(",")
    : tableColumns.trim();

  const currentVisualizationMeta = useMemo(
    () => pickVisualizationMeta(meta),
    [meta]
  );

  useEffect(() => {
    const localVisualizationMeta = buildVisualizationMeta({
      view,
      chartType,
      chartX,
      chartSeriesValue,
      chartTitle,
      tableColumnsValue,
      tableLimit,
      tableDense,
      markdownColumn,
      markdownTemplate,
    });

    if (
      !areVisualizationMetaEqual(
        currentVisualizationMeta,
        localVisualizationMeta
      )
    ) {
      return;
    }

    const nextView = (meta?.web_view ?? "").trim().toLowerCase();
    const normalizedView: VisualizationView =
      nextView === "chart" || nextView === "table" || nextView === "markdown"
        ? nextView
        : "none";

    const inferredX = inferredChartSelection?.xKey ?? "";
    const inferredSeries = inferredChartSelection?.series ?? [];
    const nextChartSeries = meta?.web_chart_series ?? inferredSeries.join(",");
    const nextTableColumns = meta?.web_table_columns ?? "";

    setView(normalizedView);
    setChartType((meta?.web_chart_type ?? "line").trim() || "line");
    setChartX(meta?.web_chart_x ?? inferredX);
    setChartSeries(nextChartSeries);
    setChartSeriesList(parseCSV(nextChartSeries));
    setChartTitle(meta?.web_chart_title ?? "");
    setTableColumns(nextTableColumns);
    setTableColumnsList(parseCSV(nextTableColumns));
    setTableLimit(meta?.web_table_limit ?? "");
    setTableDense((meta?.web_table_dense ?? "").trim().toLowerCase() === "true");
    setMarkdownColumn(meta?.web_markdown_column ?? "");
    setMarkdownTemplate(meta?.web_markdown_template ?? "");
  }, [
    chartSeriesValue,
    chartTitle,
    chartType,
    chartX,
    currentVisualizationMeta,
    inferredChartSelection,
    markdownColumn,
    markdownTemplate,
    meta,
    tableColumnsValue,
    tableDense,
    tableLimit,
    view,
  ]);

  const nextVisualizationMeta = useMemo(
    () =>
      buildVisualizationMeta({
        view,
        chartType,
        chartX,
        chartSeriesValue,
        chartTitle,
        tableColumnsValue,
        tableLimit,
        tableDense,
        markdownColumn,
        markdownTemplate,
      }),
    [
      chartSeriesValue,
      chartTitle,
      chartType,
      chartX,
      markdownColumn,
      markdownTemplate,
      tableColumnsValue,
      tableDense,
      tableLimit,
      view,
    ]
  );

  const latestOnSaveRef = useRef(onSave);
  useEffect(() => {
    latestOnSaveRef.current = onSave;
  }, [onSave]);

  useEffect(() => {
    const monaco = markdownMonacoRef.current;
    const editorUri = markdownEditorUriRef.current;
    if (!monaco || !editorUri) {
      return;
    }

    markdownCompletionDisposableRef.current?.dispose();
    markdownCompletionDisposableRef.current =
      monaco.languages.registerCompletionItemProvider("markdown", {
        triggerCharacters: ["{", "."],
        provideCompletionItems(
          model: {
            uri: { toString(): string };
            getLineContent(lineNumber: number): string;
          },
          position: { lineNumber: number; column: number },
        ) {
          if (model.uri.toString() !== editorUri) {
            return { suggestions: [] };
          }

          const linePrefix = model
            .getLineContent(position.lineNumber)
            .slice(0, position.column - 1);
          const interpolationContext = parseMarkdownInterpolationContext(
            linePrefix,
          );
          if (!interpolationContext) {
            return { suggestions: [] };
          }

          return {
            suggestions: buildMarkdownInterpolationSuggestions(
              monaco,
              position,
              interpolationContext,
              sortedColumns,
            ),
          };
        },
      });

    return () => {
      markdownCompletionDisposableRef.current?.dispose();
      markdownCompletionDisposableRef.current = null;
    };
  }, [sortedColumns]);

  useEffect(() => {
    if (disabled) {
      return;
    }

    if (areVisualizationMetaEqual(currentVisualizationMeta, nextVisualizationMeta)) {
      return;
    }

    const timeoutId = window.setTimeout(() => {
      latestOnSaveRef.current(nextVisualizationMeta);
    }, 250);

    return () => window.clearTimeout(timeoutId);
  }, [currentVisualizationMeta, disabled, nextVisualizationMeta]);

  return (
    <div className="grid gap-2 ">
      <div className="grid gap-1">
        <Label>View Type</Label>
          <Tabs
            onValueChange={(value) => setView(value as VisualizationView)}
            value={view}
          >
          <TabsList className="grid w-full grid-cols-4" data-testid="visualization-view-type-tabs">
            <TabsTrigger disabled={disabled} value="none">
              <EyeOff className="mr-1 size-3.5 text-zinc-500" />
              None
            </TabsTrigger>
            <TabsTrigger disabled={disabled} value="table">
              <Table2 className="mr-1 size-3.5 text-emerald-600" />
              Table
            </TabsTrigger>
            <TabsTrigger disabled={disabled} value="chart">
              <BarChart3 className="mr-1 size-3.5 text-sky-600" />
              Chart
            </TabsTrigger>
            <TabsTrigger disabled={disabled} value="markdown">
              <FileText className="mr-1 size-3.5 text-amber-600" />
              Markdown
            </TabsTrigger>
          </TabsList>
        </Tabs>
      </div>

      {view === "chart" && (
        <>
          <div className="grid gap-1">
            <Label>Chart Type</Label>
            <Select
              disabled={disabled}
              onValueChange={setChartType}
              value={chartType}
            >
              <SelectTrigger>
                <SelectValue placeholder="Select chart type" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="line">line</SelectItem>
                <SelectItem value="bar">bar</SelectItem>
                <SelectItem value="area">area</SelectItem>
              </SelectContent>
            </Select>
          </div>

          <div className="grid gap-1">
            <Label>X Axis Column</Label>
            {hasKnownSchema ? (
              <ColumnCombobox
                columns={sortedColumns}
                disabled={disabled}
                onChange={setChartX}
                placeholder="Select a column"
                value={chartX}
              />
            ) : (
              <Input
                disabled={disabled}
                onChange={(event) => setChartX(event.target.value)}
                placeholder="e.g. created_at"
                value={chartX}
              />
            )}
          </div>

          <div className="grid gap-1">
            <Label>Series Columns</Label>
            {hasKnownSchema ? (
              <MultiColumnCombobox
                columns={sortedColumns}
                disabled={disabled}
                onChange={setChartSeriesList}
                placeholder="Search columns"
                value={chartSeriesList}
              />
            ) : (
              <Input
                disabled={disabled}
                onChange={(event) => setChartSeries(event.target.value)}
                placeholder="comma-separated, e.g. revenue,orders"
                value={chartSeries}
              />
            )}
          </div>

          {!chartX.trim() || compactUnique(chartSeriesValue.split(",")).length === 0 ? (
            <p className="text-xs text-muted-foreground">
              Select an X axis and at least one series column to render the chart.
            </p>
          ) : null}

          <div className="grid gap-1">
            <Label>Chart Title</Label>
            <Input
              disabled={disabled}
              onChange={(event) => setChartTitle(event.target.value)}
              placeholder="optional"
              value={chartTitle}
            />
          </div>
        </>
      )}

      {view === "table" && (
        <>
          <div className="grid gap-1">
            <Label>Visible Columns</Label>
            {hasKnownSchema ? (
              <MultiColumnCombobox
                columns={sortedColumns}
                disabled={disabled}
                onChange={setTableColumnsList}
                placeholder="Search columns"
                value={tableColumnsList}
              />
            ) : (
              <Input
                disabled={disabled}
                onChange={(event) => setTableColumns(event.target.value)}
                placeholder="comma-separated, leave empty for all"
                value={tableColumns}
              />
            )}
          </div>

          <div className="grid gap-1">
            <Label>Row Limit</Label>
            <Input
              disabled={disabled}
              inputMode="numeric"
              onChange={(event) => setTableLimit(event.target.value)}
              placeholder="e.g. 200"
              value={tableLimit}
            />
          </div>

          <div className="grid gap-1">
            <Label>Dense Mode</Label>
            <Select
              disabled={disabled}
              onValueChange={(value) => setTableDense(value === "true")}
              value={tableDense ? "true" : "false"}
            >
              <SelectTrigger>
                <SelectValue placeholder="Select density" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="false">off</SelectItem>
                <SelectItem value="true">on</SelectItem>
              </SelectContent>
            </Select>
          </div>
        </>
      )}

      {view === "markdown" && (
        <>
          <div className="grid gap-1">
            <Label>Markdown Column</Label>
            {hasKnownSchema ? (
              <ColumnCombobox
                columns={sortedColumns}
                disabled={disabled}
                onChange={setMarkdownColumn}
                placeholder="Optional column"
                value={markdownColumn}
              />
            ) : (
              <Input
                disabled={disabled}
                onChange={(event) => setMarkdownColumn(event.target.value)}
                placeholder="optional"
                value={markdownColumn}
              />
            )}
          </div>

          <div className="grid gap-1">
            <Label>Markdown Template</Label>
            <div className="overflow-hidden rounded-md border">
              <Editor
                beforeMount={(monaco) => {
                  markdownMonacoRef.current = monaco;
                }}
                language="markdown"
                onChange={(value) => setMarkdownTemplate(value ?? "")}
                onMount={(editor) => {
                  markdownEditorUriRef.current =
                    editor.getModel()?.uri.toString() ?? null;
                }}
                options={{
                  fontSize: 12,
                  minimap: { enabled: false },
                  readOnly: disabled,
                  scrollBeyondLastLine: false,
                }}
                theme={monacoTheme}
                value={markdownTemplate}
                height="180px"
              />
            </div>
          </div>
        </>
      )}

      <div className="mt-1 text-right text-xs text-muted-foreground">
        Changes save automatically.
      </div>
    </div>
  );
}

type MarkdownInterpolationContext = {
  expression: string;
  prefix: string;
  partial: string;
};

function parseMarkdownInterpolationContext(
  linePrefix: string,
): MarkdownInterpolationContext | null {
  const lastOpen = linePrefix.lastIndexOf("{{");
  if (lastOpen < 0) {
    return null;
  }

  const lastClose = linePrefix.lastIndexOf("}}");
  if (lastClose > lastOpen) {
    return null;
  }

  const rawExpression = linePrefix.slice(lastOpen + 2);
  const expression = rawExpression.replace(/^\s+/, "");

  const prefixMatch = expression.match(/^(first\.|row\[\d+\]\.|row\d+\.)?([\w]*)$/);
  if (prefixMatch) {
    return {
      expression,
      prefix: prefixMatch[1] ?? "",
      partial: prefixMatch[2] ?? "",
    };
  }

  if (/^(rows\.)?([\w]*)$/.test(expression)) {
    const rowsMatch = expression.match(/^(rows\.)?([\w]*)$/);
    return {
      expression,
      prefix: rowsMatch?.[1] ?? "",
      partial: rowsMatch?.[2] ?? "",
    };
  }

  return null;
}

function buildMarkdownInterpolationSuggestions(
  monaco: Monaco,
  position: { lineNumber: number; column: number },
  context: MarkdownInterpolationContext,
  columns: string[],
) {
  const columnSuggestions = buildMarkdownColumnSuggestions(
    monaco,
    position,
    context,
    columns,
  );

  if (context.prefix) {
    return columnSuggestions;
  }

  const partialLower = context.partial.toLowerCase();
  const suggestions = [
    ...columnSuggestions,
    buildMarkdownKeywordSuggestion(monaco, position, context, {
      label: "rows.length",
      insertText: "rows.length",
      detail: "Number of returned rows",
    }),
    buildMarkdownKeywordSuggestion(monaco, position, context, {
      label: "first.",
      insertText: "first.",
      detail: "First row column access",
    }),
    buildMarkdownKeywordSuggestion(monaco, position, context, {
      label: "row[0].",
      insertText: "row[0].",
      detail: "Indexed row column access",
    }),
    buildMarkdownKeywordSuggestion(monaco, position, context, {
      label: "row0.",
      insertText: "row0.",
      detail: "Indexed row shorthand column access",
    }),
  ];

  return suggestions.filter((suggestion) => {
    const label = String(suggestion.label);
    return !partialLower || label.toLowerCase().includes(partialLower);
  });
}

function buildMarkdownColumnSuggestions(
  monaco: Monaco,
  position: { lineNumber: number; column: number },
  context: MarkdownInterpolationContext,
  columns: string[],
) {
  const uniqueColumns = compactUnique(columns);

  return uniqueColumns.map((column) => ({
    label: context.prefix ? `${context.prefix}${column}` : column,
    kind: monaco.languages.CompletionItemKind.Field,
    detail: context.prefix
      ? `Insert ${context.prefix}${column}`
      : `Insert ${column} from the first row`,
    insertText: column,
    range: {
      startLineNumber: position.lineNumber,
      endLineNumber: position.lineNumber,
      startColumn: position.column - context.partial.length,
      endColumn: position.column,
    },
  }));
}

function buildMarkdownKeywordSuggestion(
  monaco: Monaco,
  position: { lineNumber: number; column: number },
  context: MarkdownInterpolationContext,
  option: {
    label: string;
    insertText: string;
    detail: string;
  },
) {
  return {
    label: option.label,
    kind: monaco.languages.CompletionItemKind.Variable,
    detail: option.detail,
    insertText: option.insertText,
    range: {
      startLineNumber: position.lineNumber,
      endLineNumber: position.lineNumber,
      startColumn: position.column - context.partial.length,
      endColumn: position.column,
    },
  };
}

function buildVisualizationMeta({
  view,
  chartType,
  chartX,
  chartSeriesValue,
  chartTitle,
  tableColumnsValue,
  tableLimit,
  tableDense,
  markdownColumn,
  markdownTemplate,
}: {
  view: VisualizationView;
  chartType: string;
  chartX: string;
  chartSeriesValue: string;
  chartTitle: string;
  tableColumnsValue: string;
  tableLimit: string;
  tableDense: boolean;
  markdownColumn: string;
  markdownTemplate: string;
}): Record<string, string> {
  const nextMeta: Record<string, string> = {};

  if (view !== "none") {
    nextMeta.web_view = view;
  }

  if (view === "chart") {
    const nextChartType = chartType.trim().toLowerCase();
    if (nextChartType) {
      nextMeta.web_chart_type = nextChartType;
    }
    if (chartX.trim()) {
      nextMeta.web_chart_x = chartX.trim();
    }
    if (chartSeriesValue) {
      nextMeta.web_chart_series = chartSeriesValue;
    }
    if (chartTitle.trim()) {
      nextMeta.web_chart_title = chartTitle.trim();
    }
  }

  if (view === "table") {
    if (tableColumnsValue) {
      nextMeta.web_table_columns = tableColumnsValue;
    }
    if (tableLimit.trim()) {
      nextMeta.web_table_limit = tableLimit.trim();
    }
    nextMeta.web_table_dense = tableDense ? "true" : "false";
  }

  if (view === "markdown") {
    if (markdownColumn.trim()) {
      nextMeta.web_markdown_column = markdownColumn.trim();
    }
    if (markdownTemplate.trim()) {
      nextMeta.web_markdown_template = markdownTemplate;
    }
  }

  return nextMeta;
}

function pickVisualizationMeta(
  meta?: Record<string, string>
): Record<string, string> {
  const result: Record<string, string> = {};
  for (const key of VISUALIZATION_META_KEYS) {
    const value = meta?.[key];
    if (value !== undefined) {
      result[key] = value;
    }
  }
  return result;
}

function areVisualizationMetaEqual(
  left: Record<string, string>,
  right: Record<string, string>
): boolean {
  const leftKeys = Object.keys(left).sort();
  const rightKeys = Object.keys(right).sort();
  if (leftKeys.length !== rightKeys.length) {
    return false;
  }

  return leftKeys.every(
    (key, index) => key === rightKeys[index] && left[key] === right[key]
  );
}

function parseCSV(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function compactUnique(values: string[]): string[] {
  const seen = new Set<string>();
  const result: string[] = [];
  for (const value of values) {
    const trimmed = value.trim();
    if (!trimmed || seen.has(trimmed)) {
      continue;
    }
    seen.add(trimmed);
    result.push(trimmed);
  }
  return result;
}

function MultiColumnCombobox({
  value,
  onChange,
  disabled,
  columns,
  placeholder,
}: {
  value: string[];
  onChange: (next: string[]) => void;
  disabled: boolean;
  columns: string[];
  placeholder: string;
}) {
  const anchor = useComboboxAnchor();
  const normalizedValue = compactUnique(value);
  const [draft, setDraft] = useState("");

  const normalizedColumns = useMemo(() => compactUnique(columns), [columns]);
  const draftAsItem = draft.trim();
  const items = useMemo(() => {
    if (!draftAsItem) {
      return normalizedColumns;
    }
    if (normalizedColumns.includes(draftAsItem)) {
      return normalizedColumns;
    }
    return [...normalizedColumns, draftAsItem];
  }, [draftAsItem, normalizedColumns]);

  const toggleValue = (item: string) => {
    const alreadySelected = normalizedValue.some(
      (current) => current.toLowerCase() === item.toLowerCase()
    );

    if (alreadySelected) {
      onChange(
        normalizedValue.filter((current) => current.toLowerCase() !== item.toLowerCase())
      );
      setDraft("");
      return;
    }

    onChange(compactUnique([...normalizedValue, item]));
    setDraft("");
  };

  const commitDraft = () => {
    const raw = draft.trim();
    if (!raw) {
      return;
    }

    const additions = raw
      .split(",")
      .map((part) => part.trim())
      .filter(Boolean);
    if (additions.length === 0) {
      return;
    }

    onChange(compactUnique([...normalizedValue, ...additions]));
    setDraft("");
  };

  return (
    <Combobox
      multiple
      autoHighlight
      items={items}
      onValueChange={(nextValue) =>
        onChange(
          Array.isArray(nextValue) ? compactUnique(nextValue as string[]) : []
        )
      }
      value={normalizedValue}
    >
      <ComboboxChips ref={anchor} className="w-full">
        <ComboboxValue>
          {(values) => (
            <>
              {(values as string[]).map((column) => (
                <ComboboxChip key={column}>{column}</ComboboxChip>
              ))}
              <ComboboxChipsInput
                value={draft}
                placeholder={placeholder}
                disabled={disabled}
                onChange={(event) => setDraft(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === ",") {
                    event.preventDefault();
                    commitDraft();
                  }
                }}
              />
            </>
          )}
        </ComboboxValue>
      </ComboboxChips>
      <ComboboxContent anchor={anchor}>
        <ComboboxEmpty>No columns found.</ComboboxEmpty>
        <ComboboxList>
          {(item) => (
            <ComboboxItem
              key={item}
              value={item}
              onClick={() => {
                toggleValue(item);
              }}
            >
              {item}
            </ComboboxItem>
          )}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  );
}

function ColumnCombobox({
  value,
  onChange,
  disabled,
  columns,
  placeholder,
}: {
  value: string;
  onChange: (value: string) => void;
  disabled: boolean;
  columns: string[];
  placeholder: string;
}) {
  const [inputValue, setInputValue] = useState(value);

  useEffect(() => {
    setInputValue(value);
  }, [value]);

  const items = useMemo(() => {
    const custom = inputValue.trim();
    if (!custom || columns.includes(custom)) {
      return columns;
    }
    return [...columns, custom];
  }, [columns, inputValue]);

  return (
    <Combobox
      items={items}
      onValueChange={(nextValue) => {
        onChange(nextValue ?? "");
        setInputValue(nextValue ?? "");
      }}
      value={value}
    >
      <ComboboxInput
        disabled={disabled}
        onChange={(event) => {
          setInputValue(event.target.value);
          onChange(event.target.value);
        }}
        onKeyDown={(event) => {
          if (event.key === "Enter") {
            event.preventDefault();
            onChange(inputValue.trim());
          }
        }}
        placeholder={placeholder}
        value={inputValue}
      />
      <ComboboxContent>
        <ComboboxEmpty>No columns found.</ComboboxEmpty>
        <ComboboxList>
          {(item) => (
            <ComboboxItem
              key={item}
              value={item}
              onClick={() => {
                onChange(item);
                setInputValue(item);
              }}
            >
              {item}
            </ComboboxItem>
          )}
        </ComboboxList>
      </ComboboxContent>
    </Combobox>
  );
}
