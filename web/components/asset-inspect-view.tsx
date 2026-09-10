"use client";

import { lazy, Suspense } from "react";

import { InspectWarningCard } from "@/components/inspect-warning-card";
import { Bar, BarChart, CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";

import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
} from "@/components/ui/chart";
import { ScrollArea } from "@/components/ui/scroll-area";
import { VirtualDataTable } from "@/components/virtual-data-table";
import {
  buildLineChartSpec,
  buildMarkdown,
  getAssetViewMode,
  getTableDenseMode,
} from "@/lib/asset-visualization";

import type { PreviewMetadata } from "@/lib/generated/api-types";

const ReactMarkdown = lazy(() => import("react-markdown"));

type Props = {
  preview?: PreviewMetadata;
  columns: string[];
  rows: Record<string, unknown>[];
  meta?: Record<string, string>;
  loading?: boolean;
  canLoadMore?: boolean;
  onLoadMore?: () => void;
  warning?: string;
  frameless?: boolean;
};

export function AssetInspectView({
  preview,
  columns,
  rows,
  meta,
  loading = false,
  canLoadMore = false,
  onLoadMore,
  warning,
  frameless = false,
}: Props) {
  const view = getAssetViewMode(meta);
  const chartType = (meta?.web_chart_type ?? "line").trim().toLowerCase();
  const tableDense = getTableDenseMode(meta);

  if (view === "markdown") {
    const markdown = buildMarkdown(meta, rows);
    return (
      <ScrollArea
        className={`relative h-full bg-background ${frameless ? "" : "rounded border"}`}
        viewportClassName="p-3 text-sm"
      >
        <article className="max-w-none text-sm leading-6 text-foreground">
          <Suspense fallback={<div className="text-muted-foreground">Loading markdown...</div>}>
            <ReactMarkdown
              components={{
                h1: ({ children }) => (
                  <h1 className="mb-3 mt-1 text-2xl font-bold tracking-tight">{children}</h1>
                ),
                h2: ({ children }) => (
                  <h2 className="mb-2 mt-4 text-xl font-semibold tracking-tight">{children}</h2>
                ),
                h3: ({ children }) => (
                  <h3 className="mb-2 mt-3 text-lg font-semibold">{children}</h3>
                ),
                p: ({ children }) => <p className="mb-2">{children}</p>,
                ul: ({ children }) => <ul className="mb-2 list-disc pl-6">{children}</ul>,
                ol: ({ children }) => <ol className="mb-2 list-decimal pl-6">{children}</ol>,
                li: ({ children }) => <li className="mb-1">{children}</li>,
                code: ({ children }) => (
                  <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.9em]">
                    {children}
                  </code>
                ),
                pre: ({ children }) => (
                  <ScrollArea className="mb-3 rounded border bg-muted/30" viewportClassName="p-3">
                    <pre className="font-mono text-xs">{children}</pre>
                  </ScrollArea>
                ),
              }}
            >
              {markdown || "(No markdown content returned)"}
            </ReactMarkdown>
          </Suspense>
        </article>
        <InspectWarningBanner warning={warning} />
      </ScrollArea>
    );
  }

  if (view === "chart") {
    const chart = buildLineChartSpec(rows, meta, columns);
    if (!chart) {
      return (
        <div className="flex h-full items-center justify-center rounded border bg-background p-4 text-center text-sm text-muted-foreground">
          Select chart columns to render this visualization.
        </div>
      );
    }

    return (
      <div className={`relative h-full bg-background p-2 ${frameless ? "" : "rounded border"}`}>
        <ChartContainer className="h-full min-h-55 w-full" config={chart.config}>
          {chartType === "bar" ? (
            <BarChart accessibilityLayer data={chart.data}>
              <CartesianGrid vertical={false} />
              <XAxis axisLine={false} dataKey={chart.xKey} tickLine={false} tickMargin={8} />
              <YAxis axisLine={false} tickLine={false} tickMargin={8} />
              <ChartTooltip
                content={(props) => <ChartTooltipContent {...props} hideLabel />}
                cursor={false}
              />
              <ChartLegend content={<ChartLegendContent />} />
              {chart.series.map((series) => (
                <Bar key={series} dataKey={series} fill={`var(--color-${series})`} radius={6} />
              ))}
            </BarChart>
          ) : (
            <LineChart accessibilityLayer data={chart.data}>
              <CartesianGrid vertical={false} />
              <XAxis axisLine={false} dataKey={chart.xKey} tickLine={false} tickMargin={8} />
              <YAxis axisLine={false} tickLine={false} tickMargin={8} />
              <ChartTooltip content={(props) => <ChartTooltipContent {...props} />} />
              <ChartLegend content={<ChartLegendContent />} />
              {chart.series.map((series) => (
                <Line
                  key={series}
                  dataKey={series}
                  dot={false}
                  stroke={`var(--color-${series})`}
                  strokeWidth={2}
                  type="monotone"
                />
              ))}
            </LineChart>
          )}
        </ChartContainer>
        <InspectWarningBanner warning={warning} />
      </div>
    );
  }

  return (
    <div className="relative flex h-full min-h-0 flex-col overflow-hidden">
      <VirtualDataTable
        columns={columns}
        rows={rows}
        height="100%"
        dense={tableDense}
        loading={loading}
        canLoadMore={canLoadMore}
        onLoadMore={onLoadMore}
        preview={preview}
        frameless={frameless}
      />
      <InspectWarningBanner warning={warning} />
    </div>
  );
}

function InspectWarningBanner({ warning }: { warning?: string }) {
  if (!warning) {
    return null;
  }

  return (
    <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center p-6">
      <div className="absolute inset-0 bg-background/40 backdrop-brightness-75" />
      <div className="pointer-events-auto">
        <InspectWarningCard message={warning} testId="inspect-warning-banner" />
      </div>
    </div>
  );
}
