"use client";

import { ArrowRight, Database, Eye, Hammer, Plus, Trash2 } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Handle, NodeProps, Position, useUpdateNodeInternals } from "reactflow";

import {
  AssetNodeMeasurement,
  AssetNodePreview,
} from "@/components/asset-node-preview";
import { AssetTypeIcon, resolveAssetIcon } from "@/components/asset-type-icon";
import { Button } from "@/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { AssetNodeData } from "@/lib/graph";
import {
  buildLineChartSpec,
  buildMarkdown,
  getAssetViewMode,
  getTableDenseMode,
} from "@/lib/asset-visualization";

export function AssetNode({ id, data, selected }: NodeProps<AssetNodeData>) {
  const updateNodeInternals = useUpdateNodeInternals();
  const isIngestrAsset = data.assetType.trim().toLowerCase() === "ingestr";
  const materializationType = normalizeMaterialization(data.materializedAs);

  const previewMode = getAssetViewMode(data.meta);
  const chartType = (data.meta?.web_chart_type ?? "line").trim().toLowerCase();
  const tableDense = getTableDenseMode(data.meta);
  const previewRows = useMemo(
    () => data.preview?.rows ?? [],
    [data.preview?.rows]
  );
  const previewColumns = useMemo(
    () => data.preview?.columns ?? [],
    [data.preview?.columns]
  );
  const previewError = data.preview?.error;
  const isPreviewLoading = Boolean(previewMode && data.previewLoading);
  const chart = useMemo(
    () => buildLineChartSpec(previewRows, data.meta, previewColumns),
    [data.meta, previewColumns, previewRows]
  );
  const markdown = useMemo(
    () => buildMarkdown(data.meta, previewRows),
    [data.meta, previewRows]
  );
  const measurementRef = useRef<HTMLDivElement | null>(null);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [showAddButton, setShowAddButton] = useState(false);
  const { prefix, leaf } = useMemo(
    () => splitAssetName(data.name),
    [data.name]
  );
  const ingestrSource = useMemo(
    () =>
      buildIngestrEndpoint(
        data.parameters?.source_connection,
        data.parameters?.source_table
      ),
    [data.parameters?.source_connection, data.parameters?.source_table]
  );
  const ingestrDestination = useMemo(
    () => buildIngestrEndpoint(data.parameters?.destination, data.connection),
    [data.connection, data.parameters?.destination]
  );

  useEffect(() => {
    const element = containerRef.current;
    if (!element) {
      return;
    }

    const observer = new ResizeObserver(() => {
      updateNodeInternals(id);
    });

    observer.observe(element);

    return () => observer.disconnect();
  }, [id, updateNodeInternals]);

  const rowCountClass =
    data.rowCount === undefined || data.rowCount === null
      ? "text-muted-foreground"
      : data.rowCount === 0
        ? "text-destructive"
        : "text-chart-2";
  const maxWidthStyle = useMemo(
    () => ({ maxWidth: "min(80vw, 760px)" }),
    []
  );

  return (
    <>
      <Handle type="target" position={Position.Top} />
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div
            ref={containerRef}
            className={`relative min-w-56 rounded-lg border-2 bg-card p-3 shadow-sm transition-colors ${
              selected
                ? "border-primary ring-2 ring-primary/30"
                : "border-border/90"
            }`}
            style={maxWidthStyle}
            onMouseLeave={() => setShowAddButton(false)}
            onMouseMove={(event) => {
              const element = containerRef.current;
              if (!element || !data.onCreateDownstreamAsset) {
                setShowAddButton(false);
                return;
              }

              const rect = element.getBoundingClientRect();
              setShowAddButton(event.clientY >= rect.bottom - 36);
            }}
          >
            {(data.isMaterialized || data.materializeLoading) && (
              <span
                aria-label={data.materializeLoading ? "Materializing" : "Materialized"}
                className={`absolute top-2 right-2 size-2.5 rounded-full ${
                  data.materializeLoading
                    ? "animate-spin border border-emerald-500/30 border-t-emerald-500"
                    : data.freshnessStatus === "stale"
                      ? "bg-muted-foreground"
                      : "bg-emerald-500"
                }`}
              />
            )}

            {isIngestrAsset ? (
              <div>
                <div className="flex items-center gap-2">
                  <AssetTypeIcon
                    assetType={data.assetType}
                    className="shrink-0 px-2 text-muted-foreground"
                    size={18}
                  />
                  <div className="min-w-0 mr-4">
                    <div
                      className="truncate text-sm font-semibold leading-tight"
                      title={data.name}
                    >
                      {leaf}
                    </div>
                    {prefix && (
                      <div
                        className="truncate text-[10px] leading-tight text-muted-foreground/90"
                        title={prefix}
                      >
                        {prefix}
                      </div>
                    )}
                  </div>
                </div>

                <div className="mt-3 flex items-center justify-between gap-3 rounded-lg border border-border/70 bg-muted/20 px-3 py-2">
                  <IngestrEndpoint
                    connection={data.parameters?.source_connection}
                    label={ingestrSource.primaryLabel}
                    secondaryLabel={ingestrSource.secondaryLabel}
                  />
                  <ArrowRight className="size-4 shrink-0 text-muted-foreground" />
                  <IngestrEndpoint
                    connection={data.parameters?.destination}
                    label={ingestrDestination.primaryLabel}
                    secondaryLabel={ingestrDestination.secondaryLabel}
                  />
                </div>
              </div>
            ) : (
              <>
                <div className="flex items-center gap-1.5 pr-4">
                  <AssetTypeIcon
                    assetType={data.assetType}
                    className="shrink-0 px-2 text-muted-foreground"
                    connection={data.connection}
                    meta={data.meta}
                  />
                  <div className="min-w-0 flex-1 overflow-hidden">
                    <div
                      className="truncate text-sm font-semibold leading-tight"
                      title={data.name}
                    >
                      {leaf}
                    </div>
                    {prefix && (
                      <div
                        className="truncate text-[10px] leading-tight text-muted-foreground/90"
                        title={prefix}
                      >
                        {prefix}
                      </div>
                    )}
                  </div>
                </div>

                <div className="mt-2 text-xs">
                  Materialization:{" "}
                  <span className="font-medium">{materializationType}</span>
                </div>

                <div className={`text-xs ${rowCountClass}`}>
                  Rows: {formatRowCount(data.rowCount)}
                </div>
              </>
            )}

            <AssetNodePreview
              assetId={id}
              canLoadMorePreviewRows={data.canLoadMorePreviewRows}
              chart={chart}
              chartType={chartType}
              isPreviewLoading={isPreviewLoading}
              markdown={markdown}
              onLoadMorePreviewRows={data.onLoadMorePreviewRows}
              previewColumns={previewColumns}
              dense={tableDense}
              previewError={previewError}
              previewMode={previewMode}
              previewRows={previewRows}
            />

            <AssetNodeMeasurement
              assetId={id}
              canLoadMorePreviewRows={data.canLoadMorePreviewRows}
              dense={tableDense}
              isPreviewLoading={isPreviewLoading}
              markdown={markdown || ""}
              measurementRef={measurementRef}
              previewColumns={previewColumns}
              previewMode={previewMode}
              previewRows={previewRows}
            />

            {data.onCreateDownstreamAsset && showAddButton && (
              <div className="absolute -bottom-4 left-1/2 z-10 -translate-x-1/2 nodrag nopan">
                <Button
                  className="h-8 rounded-full px-3 shadow-md"
                  onClick={(event) => {
                    event.preventDefault();
                    event.stopPropagation();
                    data.onCreateDownstreamAsset?.();
                  }}
                  size="sm"
                  type="button"
                >
                  <Plus className="mr-1 size-3.5" />
                  Add
                </Button>
              </div>
            )}
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent>
          <ContextMenuItem disabled>{data.name}</ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem
            disabled={Boolean(data.materializeLoading)}
            onSelect={() => {
              data.onMaterialize?.();
            }}
          >
            <Hammer />
            Materialize
          </ContextMenuItem>
          <ContextMenuItem
            disabled={Boolean(data.inspectLoading)}
            onSelect={() => {
              data.onInspect?.();
            }}
          >
            <Eye />
            Inspect
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem
            variant="destructive"
            onSelect={() => {
              data.onDelete?.();
            }}
          >
            <Trash2 />
            Delete Asset
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>
      <Handle type="source" position={Position.Bottom} />
    </>
  );
}

function IngestrEndpoint({
  connection,
  label,
  secondaryLabel,
}: {
  connection?: string;
  label: string;
  secondaryLabel?: string;
}) {
  const resolvedIcon = resolveAssetIcon(undefined, connection, undefined, 18);

  return (
    <div className="flex min-w-0 flex-1 items-center gap-2">
      <div className="flex size-8 shrink-0 items-center justify-center rounded-full bg-background text-muted-foreground shadow-sm ring-1 ring-border/70">
        {resolvedIcon?.icon ?? <Database className="size-4.5" />}
      </div>
      <div className="min-w-0 flex-1">
        <div className="truncate text-xs font-semibold" title={label}>
          {label}
        </div>
        {secondaryLabel ? (
          <div
            className="truncate text-[10px] text-muted-foreground"
            title={secondaryLabel}
          >
            {secondaryLabel}
          </div>
        ) : null}
      </div>
    </div>
  );
}

function formatRowCount(value?: number) {
  if (value === undefined || value === null) {
    return "unknown";
  }

  return value.toLocaleString();
}

function normalizeMaterialization(value?: string) {
  const raw = (value ?? "").toLowerCase();
  if (raw === "table") {
    return "Table";
  }
  if (raw === "view") {
    return "View";
  }
  return "Unset";
}

function splitAssetName(name: string) {
  const parts = name.split(".").filter(Boolean);
  if (parts.length <= 1) {
    return {
      prefix: "",
      leaf: name,
    };
  }

  return {
    prefix: parts.slice(0, -1).join("."),
    leaf: parts[parts.length - 1],
  };
}

function buildIngestrEndpoint(primary?: string, secondary?: string) {
  const normalizedPrimary = compactLabel(primary);
  const normalizedSecondary = compactLabel(secondary);

  if (
    normalizedPrimary &&
    normalizedSecondary &&
    normalizedPrimary !== normalizedSecondary
  ) {
    return {
      primaryLabel: normalizedPrimary,
      secondaryLabel: normalizedSecondary,
    };
  }

  return {
    primaryLabel: normalizedPrimary || normalizedSecondary || "Unknown",
    secondaryLabel:
      normalizedPrimary && normalizedSecondary && normalizedPrimary === normalizedSecondary
        ? undefined
        : normalizedSecondary,
  };
}

function compactLabel(value?: string) {
  const trimmed = (value ?? "").trim();
  if (!trimmed) {
    return "";
  }

  return trimmed.replace(/-default$/i, "");
}
