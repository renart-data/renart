"use client";

import { useMemo } from "react";
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Line,
  LineChart,
  Pie,
  PieChart,
  Scatter,
  ScatterChart,
  XAxis,
  YAxis,
} from "recharts";

import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import { VirtualDataTable } from "@/components/virtual-data-table";
import {
  NotebookCellRunResult,
  NotebookVisualizationDefinition,
  VisualizationFieldEncoding,
  VizDirective,
} from "@/lib/api-notebooks";
import { visualizationPaletteColors } from "@/lib/visualization-palettes";
import {
  boundedRowsToObjects,
  NOTEBOOK_CHART_SERIES_CAP,
  pivotChartSeries,
} from "@/lib/notebook-viz-data";

const CHART_ROW_CAP = 200;
function asArray(value: unknown): string[] {
  if (Array.isArray(value)) return value.map(String);
  if (typeof value === "string" && value) return [value];
  return [];
}

function MissingColumnNotice({ columns }: { columns: string[] }) {
  return (
    <div className="flex h-full min-h-32 items-center justify-center rounded-lg border border-dashed bg-muted/30 px-4 text-center text-xs text-muted-foreground">
      {columns.length === 1
        ? `Column '${columns[0]}' is not in the result.`
        : `Columns ${columns.map((column) => `'${column}'`).join(", ")} are not in the result.`}
    </div>
  );
}

function missingColumns(needed: string[], present: string[]): string[] {
  const lower = new Set(present.map((column) => column.toLowerCase()));
  return needed.filter((column) => !lower.has(column.toLowerCase()));
}

function fieldName(encoding?: VisualizationFieldEncoding): string {
  return encoding?.field?.trim() ?? "";
}

function resolvedFieldName(authored: string, columns: string[]): string {
  return columns.find((column) => column.toLowerCase() === authored.toLowerCase()) ?? authored;
}

function formatNumber(value: unknown, format?: string): string {
  const numeric = typeof value === "number" ? value : Number(value);
  if (!Number.isFinite(numeric)) return String(value ?? "");
  if (format === "currency") {
    return new Intl.NumberFormat(undefined, {
      style: "currency",
      currency: "USD",
      maximumFractionDigits: 0,
    }).format(numeric);
  }
  if (format === "percent") {
    return new Intl.NumberFormat(undefined, {
      style: "percent",
      maximumFractionDigits: 1,
    }).format(numeric);
  }
  return new Intl.NumberFormat().format(numeric);
}

function resultLimit(definition: NotebookVisualizationDefinition): number {
  const requested = definition.presentation_limit ?? CHART_ROW_CAP;
  return Math.max(1, Math.min(requested, 1_000));
}

function visualizationAriaLabel(
  definition: NotebookVisualizationDefinition,
  result: NotebookCellRunResult,
) {
  const title = definition.title?.trim();
  const kind = definition.type === "kpi" ? "KPI" : `${definition.type} visualization`;
  const fields = [
    definition.encoding?.x,
    ...(definition.encoding?.y ?? []),
    definition.encoding?.series,
    definition.encoding?.color,
    definition.value,
    definition.compare,
    ...(definition.columns ?? []),
  ]
    .map(fieldName)
    .filter(Boolean);
  const fieldSummary = [...new Set(fields)].join(", ");
  return [title, kind, fieldSummary ? `Fields: ${fieldSummary}` : "", `${result.total_rows} rows`]
    .filter(Boolean)
    .join(". ");
}

/** Pure renderer for the durable, versioned visualization definition. */
export function NotebookVisualizationRenderer({
  definition,
  result,
}: {
  definition: NotebookVisualizationDefinition;
  result: NotebookCellRunResult;
}) {
  const limit = resultLimit(definition);
  const rows = useMemo(
    () => boundedRowsToObjects(result.columns, result.rows, limit),
    [limit, result.columns, result.rows],
  );
  const seriesColors = visualizationPaletteColors(definition.palette);
  const ariaLabel = visualizationAriaLabel(definition, result);

  if (definition.type === "table") {
    const requested = (definition.columns ?? []).map(fieldName).filter(Boolean);
    const columns = requested.length > 0 ? requested : result.columns;
    const missing = missingColumns(columns, result.columns);
    if (missing.length > 0) return <MissingColumnNotice columns={missing} />;
    const indexes = columns.map((column) =>
      result.columns.findIndex((candidate) => candidate.toLowerCase() === column.toLowerCase()),
    );
    const visibleRows = result.rows.slice(0, limit);
    const columnKeys = columns.map((_, index) => `column_${index}`);
    const tableRows = visibleRows.map((row) =>
      Object.fromEntries(
        indexes.map((sourceIndex, columnIndex) => [columnKeys[columnIndex], row[sourceIndex]]),
      ),
    );
    return (
      <div className="overflow-hidden rounded-lg border">
        <VirtualDataTable
          ariaLabel={ariaLabel}
          columnKeys={columnKeys}
          columns={columns}
          dense
          frameless
          height={288}
          rows={tableRows}
          viewportClassName="max-h-72"
        />
        {result.total_rows > visibleRows.length ? (
          <div className="border-t bg-muted/30 px-2 py-1 text-[11px] text-muted-foreground">
            showing {visibleRows.length} of {result.total_rows} rows
          </div>
        ) : null}
      </div>
    );
  }

  if (definition.type === "kpi") {
    return <KpiDefinitionCard definition={definition} result={result} />;
  }

  const authoredXKey = fieldName(definition.encoding?.x);
  const yFields = definition.encoding?.y ?? [];
  const authoredYKeys = yFields.map(fieldName).filter(Boolean);
  const authoredSeriesKey = fieldName(definition.encoding?.series ?? definition.encoding?.color);
  const needed = [authoredXKey, ...authoredYKeys, authoredSeriesKey].filter(Boolean);
  const missing = missingColumns(needed, result.columns);
  if (missing.length > 0) return <MissingColumnNotice columns={missing} />;

  const xKey = resolvedFieldName(authoredXKey, result.columns);
  const yKeys = authoredYKeys.map((field) => resolvedFieldName(field, result.columns));
  const seriesKey = authoredSeriesKey ? resolvedFieldName(authoredSeriesKey, result.columns) : "";

  const capped = rows;
  let chartData = capped;
  let renderedSeries = yKeys.slice(0, NOTEBOOK_CHART_SERIES_CAP);
  let totalSeries = yKeys.length;
  let pivotedSeries = false;
  if (seriesKey && yKeys.length === 1) {
    const pivoted = pivotChartSeries(capped, xKey, yKeys[0], seriesKey);
    chartData = pivoted.data;
    renderedSeries = pivoted.series;
    totalSeries = pivoted.totalSeries;
    pivotedSeries = true;
  }
  const config: ChartConfig = {};
  renderedSeries.forEach((key, index) => {
    const authored = yFields[index];
    config[key] = {
      label: pivotedSeries ? key : authored?.label || key,
    };
  });
  const showLegend = definition.show_legend ?? renderedSeries.length > 1;
  const rowsTruncated = result.rows.length > rows.length || result.total_rows > result.rows.length;
  const seriesTruncated = totalSeries > renderedSeries.length;

  return (
    <figure aria-label={ariaLabel}>
      <ChartContainer config={config} className="h-64 w-full">
        {definition.type === "bar" ? (
          <BarChart data={chartData} accessibilityLayer>
            <CartesianGrid vertical={false} />
            <XAxis dataKey={xKey} tickLine={false} axisLine={false} tickMargin={8} />
            <YAxis tickLine={false} axisLine={false} tickMargin={8} />
            <ChartTooltip content={(props) => <ChartTooltipContent {...props} />} />
            {showLegend ? <ChartLegend content={<ChartLegendContent />} /> : null}
            {renderedSeries.map((key, index) => (
              <Bar
                key={key}
                dataKey={key}
                fill={seriesColors[index % seriesColors.length]}
                stackId={definition.stacked ? "stack" : undefined}
                radius={definition.stacked ? 0 : 4}
              />
            ))}
          </BarChart>
        ) : definition.type === "area" ? (
          <AreaChart data={chartData} accessibilityLayer>
            <CartesianGrid vertical={false} />
            <XAxis dataKey={xKey} tickLine={false} axisLine={false} tickMargin={8} />
            <YAxis tickLine={false} axisLine={false} tickMargin={8} />
            <ChartTooltip content={(props) => <ChartTooltipContent {...props} />} />
            {showLegend ? <ChartLegend content={<ChartLegendContent />} /> : null}
            {renderedSeries.map((key, index) => (
              <Area
                key={key}
                dataKey={key}
                type="monotone"
                fill={seriesColors[index % seriesColors.length]}
                stroke={seriesColors[index % seriesColors.length]}
                stackId={definition.stacked ? "stack" : undefined}
                fillOpacity={0.2}
              />
            ))}
          </AreaChart>
        ) : definition.type === "pie" || definition.type === "donut" ? (
          <PieChart accessibilityLayer>
            <ChartTooltip content={(props) => <ChartTooltipContent {...props} />} />
            <Pie
              data={capped}
              dataKey={yKeys[0]}
              nameKey={xKey}
              outerRadius={92}
              innerRadius={definition.type === "donut" ? 50 : 0}
            >
              {capped.map((_, index) => (
                <Cell key={index} fill={seriesColors[index % seriesColors.length]} />
              ))}
            </Pie>
          </PieChart>
        ) : definition.type === "scatter" ? (
          <ScatterChart accessibilityLayer>
            <CartesianGrid />
            <XAxis dataKey={xKey} name={definition.encoding?.x?.label || xKey} />
            <YAxis dataKey={yKeys[0]} name={yFields[0]?.label || yKeys[0]} />
            <ChartTooltip cursor={{ strokeDasharray: "3 3" }} />
            <Scatter data={capped} fill={seriesColors[0]} />
          </ScatterChart>
        ) : (
          <LineChart data={chartData} accessibilityLayer>
            <CartesianGrid vertical={false} />
            <XAxis dataKey={xKey} tickLine={false} axisLine={false} tickMargin={8} />
            <YAxis tickLine={false} axisLine={false} tickMargin={8} />
            <ChartTooltip content={(props) => <ChartTooltipContent {...props} />} />
            {showLegend ? <ChartLegend content={<ChartLegendContent />} /> : null}
            {renderedSeries.map((key, index) => (
              <Line
                key={key}
                dataKey={key}
                type="monotone"
                stroke={seriesColors[index % seriesColors.length]}
                strokeWidth={2}
                dot={false}
              />
            ))}
          </LineChart>
        )}
      </ChartContainer>
      {rowsTruncated || seriesTruncated ? (
        <figcaption className="mt-1 text-[11px] text-muted-foreground" role="status">
          {rowsTruncated ? `Previewing ${rows.length} of ${result.total_rows} rows.` : null}
          {rowsTruncated && seriesTruncated ? " " : null}
          {seriesTruncated
            ? `Showing the first ${renderedSeries.length} of ${totalSeries} series.`
            : null}
        </figcaption>
      ) : null}
    </figure>
  );
}

function KpiDefinitionCard({
  definition,
  result,
}: {
  definition: NotebookVisualizationDefinition;
  result: NotebookCellRunResult;
}) {
  const valueKey = fieldName(definition.value);
  const compareKey = fieldName(definition.compare);
  const missing = missingColumns([valueKey, compareKey].filter(Boolean), result.columns);
  if (missing.length > 0) return <MissingColumnNotice columns={missing} />;
  const valueIndex = result.columns.findIndex(
    (column) => column.toLowerCase() === valueKey.toLowerCase(),
  );
  const compareIndex = compareKey
    ? result.columns.findIndex((column) => column.toLowerCase() === compareKey.toLowerCase())
    : -1;
  const firstRow = result.rows[0] ?? [];
  const value = valueIndex >= 0 ? firstRow[valueIndex] : undefined;
  const compare = compareIndex >= 0 ? firstRow[compareIndex] : undefined;
  const delta =
    typeof value === "number" && typeof compare === "number" && compare !== 0
      ? (value - compare) / Math.abs(compare)
      : null;
  return (
    <section
      aria-label={visualizationAriaLabel(definition, result)}
      className="rounded-lg border p-4"
    >
      <div className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        {definition.value?.label || valueKey}
      </div>
      <div className="mt-1 text-3xl font-semibold tracking-tight">
        {formatNumber(value, definition.value?.format)}
      </div>
      {delta !== null ? (
        <div className={`mt-1 text-xs ${delta >= 0 ? "text-emerald-600" : "text-red-500"}`}>
          {delta >= 0 ? "▲" : "▼"} {formatNumber(Math.abs(delta), "percent")} vs{" "}
          {definition.compare?.label || compareKey}
        </div>
      ) : null}
    </section>
  );
}

function legacyDefinition(viz: VizDirective): NotebookVisualizationDefinition {
  const x = typeof viz.options.x === "string" ? { field: viz.options.x } : undefined;
  const y = asArray(viz.options.y).map((field) => ({
    field,
    ...(typeof viz.options.format === "string" ? { format: viz.options.format } : {}),
  }));
  return {
    version: 1,
    type: viz.kind,
    encoding: { x, y },
    columns: asArray(viz.options.columns).map((field) => ({ field })),
    value:
      typeof viz.options.value === "string"
        ? {
            field: viz.options.value,
            ...(typeof viz.options.format === "string" ? { format: viz.options.format } : {}),
          }
        : undefined,
    compare: typeof viz.options.compare === "string" ? { field: viz.options.compare } : undefined,
    stacked: viz.options.stacked === true,
    presentation_limit:
      typeof viz.options.limit === "number" ? Math.trunc(viz.options.limit) : undefined,
  };
}

/** Read-only compatibility renderer for legacy SQL @viz comments. */
export function NotebookVizRenderer({ result }: { result: NotebookCellRunResult }) {
  if (!result.viz) return null;
  return (
    <NotebookVisualizationRenderer definition={legacyDefinition(result.viz)} result={result} />
  );
}
