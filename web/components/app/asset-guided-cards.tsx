"use client";

import { type Ref, useCallback, useEffect, useId, useMemo, useRef, useState } from "react";

import { useAtomValue } from "jotai";
import {
  AlertTriangle,
  Ban,
  Check,
  ChevronRight,
  ChevronsUpDown,
  Columns3,
  GitBranch,
  KeyRound,
  Plus,
  RefreshCw,
  RotateCcw,
  ShieldCheck,
  SlidersHorizontal,
  Trash2,
  X,
} from "lucide-react";

import { selectedEnvironmentAtom, workspaceAtom } from "@/lib/atoms/workspace";
import type { AssetStaleness, FailedQualityCheck } from "@/lib/api-staleness";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Checkbox } from "@/components/ui/checkbox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
} from "@/components/ui/command";
import { Input } from "@/components/ui/input";
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Spinner } from "@/components/ui/spinner";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { applyAssetTransaction } from "@/lib/api-asset-transactions";
import { updateAsset, updateAssetColumns } from "@/lib/api-assets";
import { applyAssetColumnSchemaResolution, syncAssetColumns } from "@/lib/api-assets-columns";
import type {
  ColumnInferenceSource,
  ColumnSchemaResolution,
  ColumnSchemaSyncResult,
  MaterializationCapability,
} from "@/lib/generated/api-types";
import {
  artifactRefKey,
  columnImpactsForAsset,
  type ColumnImpactView,
} from "@/lib/artifact-column-impact";
import { classifyDependencies, columnStatus, parseAssetProvenance } from "@/lib/asset-provenance";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { getAssetColumnRefreshMode, isSeedAssetType, isSqlAssetType } from "@/lib/asset-types";
import { cn } from "@/lib/utils";
import { WebAsset, WebColumn } from "@/lib/types";
import { AssetConnectionEditor } from "./asset-connection-editor";
import { AssetCustomChecks } from "./asset-custom-checks";
import { AssetHooks } from "./asset-hooks";
import { MultiValueInput } from "./multi-value-input";
import { SchemaSyncDialog } from "./schema-sync-dialog";
import { AssetDependencyPicker } from "./asset-dependency-picker";
import { useResourceNavigation } from "@/hooks/use-resource-navigation";
import { resolveColumn, type ColumnTarget } from "@/lib/resource-navigation";

/**
 * Guided metadata cards for the app asset editor (§13–14 of the asset
 * editing concept). Renders focused, editable sections beside the SQL editor so
 * users edit asset intent without touching raw YAML; every edit flows through
 * the asset API, and the workspace SSE stream refreshes the asset prop.
 */
export type QualityCheckFocus = FailedQualityCheck & { token: number };
type AssetMetadataTab = "general" | "lineage" | "columns" | "checks";

export function AssetGuidedCards({
  asset,
  pipelineId,
  quality,
  focusedCheck,
  onGoToAsset,
}: {
  asset: WebAsset;
  pipelineId: string;
  quality?: AssetStaleness;
  focusedCheck?: QualityCheckFocus | null;
  onGoToAsset?: (pipelineId: string, assetId: string) => void;
}) {
  const supportsColumns =
    (asset.column_inference_sources?.length ?? 0) > 0 ||
    getAssetColumnRefreshMode(asset.type, asset.parameters) !== "none";
  const navigation = useResourceNavigation();
  const linked = navigation.detail;
  const target = linked?.target;
  const addressed =
    target &&
    (target.kind === "asset-column" || target.kind === "asset-section") &&
    target.asset_id === asset.id
      ? target
      : undefined;
  const section = addressed?.kind === "asset-column" ? "columns" : addressed?.section;
  const [localTab, setActiveTab] = useState<AssetMetadataTab>(focusedCheck ? "checks" : "general");
  const activeTab: AssetMetadataTab =
    section === "columns" || section === "checks"
      ? section
      : section === "dependencies"
        ? "lineage"
        : section === "identity" || section === "materialization"
          ? "general"
          : localTab;
  useEffect(() => {
    if (section && section !== "source") setActiveTab(activeTab);
  }, [section, activeTab]);
  const linkedColumn =
    addressed?.kind === "asset-column"
      ? resolveColumn(asset.columns ?? [], addressed.column)
      : undefined;
  const linkedCheckColumn =
    addressed?.kind === "asset-section" && addressed.column
      ? resolveColumn(asset.columns ?? [], addressed.column)
      : undefined;
  const linkedCheck = linkedCheckColumn?.checks?.filter(
    (check) => check.name === addressed?.check_name,
  );
  const sectionFocus = useCallback(
    (node: HTMLDivElement | null) => {
      if (!node || section !== "materialization") return;
      const frame = requestAnimationFrame(() => {
        if (!node.isConnected || node.getClientRects().length === 0) return;
        node.focus({ preventScroll: true });
        const viewport = node.closest('[data-slot="scroll-area-viewport"]');
        if (viewport)
          viewport.scrollTop +=
            node.getBoundingClientRect().top - viewport.getBoundingClientRect().top;
      });
      return () => cancelAnimationFrame(frame);
    },
    [section],
  );
  const routeCheck: QualityCheckFocus | undefined =
    linkedCheckColumn && linkedCheck?.length === 1
      ? { kind: "column", column: linkedCheckColumn.name, name: linkedCheck[0].name, token: 0 }
      : undefined;
  const [localFocus, setLocalFocus] = useState<QualityCheckFocus | null>(null);
  useEffect(() => setLocalFocus(null), [focusedCheck?.token]);
  const focusedCheckToken = focusedCheck?.token;
  useEffect(() => {
    if (focusedCheckToken !== undefined) setActiveTab("checks");
  }, [focusedCheckToken]);
  const activeFocus = routeCheck ?? localFocus ?? focusedCheck;
  const dependencyCount = asset.dependencies?.length ?? asset.upstreams?.length ?? 0;
  const columnCount = asset.columns?.length ?? 0;
  const checkCount =
    (asset.custom_checks?.length ?? 0) +
    (asset.columns ?? []).reduce((count, column) => count + (column.checks?.length ?? 0), 0);

  return (
    <Tabs
      value={activeTab}
      onValueChange={(value) => {
        setActiveTab(value as AssetMetadataTab);
        void navigation.open(
          {
            kind: "asset-section",
            asset_id: asset.id,
            section:
              value === "general" ? "identity" : value === "lineage" ? "dependencies" : value,
          },
          linked?.environment,
        );
      }}
      className="min-h-0 w-full flex-1 gap-0"
    >
      <div className="shrink-0 border-b px-2 py-1.5">
        <TabsList
          aria-label="Asset property sections"
          className={cn("grid w-full", supportsColumns ? "grid-cols-4" : "grid-cols-2")}
        >
          <MetadataTab value="general" label="General" icon={SlidersHorizontal} />
          <MetadataTab value="lineage" label="Lineage" icon={GitBranch} count={dependencyCount} />
          {supportsColumns ? (
            <MetadataTab value="columns" label="Columns" icon={Columns3} count={columnCount} />
          ) : null}
          {supportsColumns ? (
            <MetadataTab value="checks" label="Checks" icon={ShieldCheck} count={checkCount} />
          ) : null}
        </TabsList>
      </div>
      <ScrollArea className="min-h-0 w-full flex-1">
        {addressed?.kind === "asset-column" && !linkedColumn ? (
          <p role="alert" className="p-3 text-sm">
            The linked column is missing or ambiguous. No other column has been selected.
          </p>
        ) : null}
        {addressed?.check_name && !routeCheck ? (
          <p role="alert" className="p-3 text-sm">
            The linked check is missing or ambiguous.
          </p>
        ) : null}
        <TabsContent value="general" forceMount className="m-0 data-[state=inactive]:hidden">
          <div className="divide-y px-3">
            <IdentityCard asset={asset} pipelineId={pipelineId} />
            <div tabIndex={-1} ref={sectionFocus}>
              <MaterializationCard asset={asset} pipelineId={pipelineId} />
            </div>
            {isSqlAssetType(asset.type) ? (
              <GuidedCard title="SQL hooks">
                <AssetHooks asset={asset} />
              </GuidedCard>
            ) : null}
          </div>
        </TabsContent>
        <TabsContent value="lineage" forceMount className="m-0 data-[state=inactive]:hidden">
          <div className="px-3">
            <DependenciesCard asset={asset} onGoToAsset={onGoToAsset} />
          </div>
        </TabsContent>
        {supportsColumns ? (
          <TabsContent value="columns" forceMount className="m-0 data-[state=inactive]:hidden">
            <div className="px-3">
              <ColumnsCard
                asset={asset}
                environmentOverride={addressed ? linked?.environment : undefined}
                focusedColumn={linkedColumn?.name}
                focusedField={addressed?.kind === "asset-column" ? addressed.field : undefined}
                focusToken={linkedColumn ? JSON.stringify(addressed) : undefined}
                onFocusColumn={(column, field = "type") =>
                  void navigation.open(
                    { kind: "asset-column", asset_id: asset.id, column, field },
                    linked?.environment,
                  )
                }
              />
            </div>
          </TabsContent>
        ) : null}
        {supportsColumns ? (
          <TabsContent value="checks" forceMount className="m-0 data-[state=inactive]:hidden">
            <div className="px-3">
              <QualityChecksCard
                asset={asset}
                quality={quality}
                focusedCheck={activeFocus}
                onFocusCheck={(check) => {
                  setLocalFocus({ ...check, token: Date.now() });
                  if (check.kind === "column")
                    void navigation.open(
                      {
                        kind: "asset-section",
                        asset_id: asset.id,
                        section: "checks",
                        column: check.column,
                        check_name: check.name,
                      },
                      linked?.environment,
                    );
                }}
              />
            </div>
          </TabsContent>
        ) : null}
      </ScrollArea>
    </Tabs>
  );
}

function MetadataTab({
  value,
  label,
  icon: Icon,
  count,
}: {
  value: AssetMetadataTab;
  label: string;
  icon: typeof SlidersHorizontal;
  count?: number;
}) {
  return (
    <TabsTrigger
      value={value}
      aria-label={label}
      title={typeof count === "number" ? `${label} (${count})` : label}
      className="min-w-0 gap-1 px-1 text-[11px]"
    >
      <Icon className="size-3.5" />
      <span className="truncate">{label}</span>
    </TabsTrigger>
  );
}

/**
 * A borderless section: an eyebrow title and its controls, separated from the
 * next section by a hairline divider (the parent's divide-y) rather than a card,
 * so a stack of sections reads as one calm form instead of nested boxes.
 */
function GuidedCard({
  title,
  action,
  children,
}: {
  title: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <section className="flex flex-col gap-2.5 py-4">
      <div className="flex min-h-5 flex-wrap items-center justify-between gap-2">
        <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          {title}
        </h3>
        {action}
      </div>
      {children}
    </section>
  );
}

// --- Identity card (§14.1) ---

export function IdentityCard({ asset, pipelineId }: { asset: WebAsset; pipelineId: string }) {
  const fieldIdPrefix = `${useId()}-identity`;
  const fieldId = (name: string) => `${fieldIdPrefix}-${name}`;
  const workspace = useAtomValue(workspaceAtom);
  const uri = asset.uri?.trim() ?? "";
  const uriConflict = useMemo(() => {
    if (!uri) return undefined;
    for (const pipeline of workspace?.pipelines ?? []) {
      for (const candidate of pipeline.assets) {
        if (candidate.id !== asset.id && candidate.uri?.trim() === uri) {
          return `${pipeline.name}/${candidate.name}`;
        }
      }
    }
    return undefined;
  }, [asset.id, uri, workspace]);

  const updateMetaDescription = (description: string) => {
    const nextMeta = { ...(asset.meta ?? {}) };
    if (description.trim()) {
      nextMeta.description = description.trim();
    } else {
      delete nextMeta.description;
    }
    void updateAsset(pipelineId, asset.id, { meta: nextMeta });
  };

  return (
    <GuidedCard title="Identity">
      <FieldGroup>
        <FieldRow label="Name" htmlFor={fieldId("name")}>
          <CommitInput
            id={fieldId("name")}
            mono
            value={asset.name}
            placeholder="analytics.orders"
            onCommit={(name) => {
              if (name.trim() && name.trim() !== asset.name) {
                void updateAsset(pipelineId, asset.id, { name: name.trim() });
              }
            }}
          />
        </FieldRow>
        <FieldRow label="Type" htmlFor={fieldId("type")}>
          <Input
            id={fieldId("type")}
            value={asset.type}
            className="h-8 font-monaco text-xs"
            readOnly
            aria-readonly="true"
          />
          <FieldDescription>Managed by the asset kind and connection.</FieldDescription>
        </FieldRow>
        <AssetConnectionEditor asset={asset} pipelineId={pipelineId} />
        <FieldRow label="Owner" htmlFor={fieldId("owner")}>
          <CommitInput
            id={fieldId("owner")}
            value={asset.owner ?? ""}
            placeholder="team@company.com"
            onCommit={(owner) => {
              if (owner !== (asset.owner ?? "")) void updateAsset(pipelineId, asset.id, { owner });
            }}
          />
        </FieldRow>
        <FieldRow label="Description" htmlFor={fieldId("description")}>
          <CommitInput
            id={fieldId("description")}
            value={asset.meta?.description ?? ""}
            placeholder="What this asset produces"
            onCommit={(description) => {
              if (description !== (asset.meta?.description ?? "")) {
                updateMetaDescription(description);
              }
            }}
          />
        </FieldRow>
        <FieldRow label="Tags" htmlFor={fieldId("tags")}>
          <MultiValueInput
            id={fieldId("tags")}
            value={asset.tags ?? []}
            placeholder="Add tag"
            onChange={(tags) => {
              if (tags.join("\n") !== (asset.tags ?? []).join("\n")) {
                void updateAsset(pipelineId, asset.id, { tags });
              }
            }}
          />
        </FieldRow>
        <Field data-invalid={uriConflict ? true : undefined}>
          <FieldLabel htmlFor={fieldId("uri")}>URI</FieldLabel>
          <CommitInput
            id={fieldId("uri")}
            mono
            value={asset.uri ?? ""}
            placeholder="duckdb://warehouse/schema/table"
            ariaInvalid={Boolean(uriConflict)}
            ariaDescribedBy={fieldId("uri-description")}
            onCommit={(nextURI) => {
              const normalized = nextURI.trim();
              if (normalized !== uri) {
                void applyAssetTransaction(asset.id, {
                  type: "asset.uri.set",
                  asset_uri: normalized,
                });
              }
            }}
          />
          <FieldDescription id={fieldId("uri-description")}>
            {uriConflict
              ? `Already declared by ${uriConflict}. Producer URIs must be unique.`
              : "Explicit producer identity for dependencies from sibling pipelines."}
          </FieldDescription>
        </Field>
      </FieldGroup>
    </GuidedCard>
  );
}

// --- Materialization card (§14.2) ---

export type MaterializationOption = {
  value: string;
  label: string;
  type: string;
  strategy: string;
  capability?: MaterializationCapability;
  custom?: boolean;
};

export const MATERIALIZATION_OPTIONS: MaterializationOption[] = [
  { value: "none", label: "None (run only)", type: "", strategy: "" },
  { value: "view", label: "View", type: "view", strategy: "" },
  {
    value: "create+replace",
    label: "Table (replace)",
    type: "table",
    strategy: "create+replace",
  },
  {
    value: "truncate+insert",
    label: "Table (truncate)",
    type: "table",
    strategy: "truncate+insert",
  },
  { value: "append", label: "Append rows", type: "table", strategy: "append" },
  { value: "merge", label: "Merge by key", type: "table", strategy: "merge" },
  {
    value: "delete+insert",
    label: "Replace matching keys",
    type: "table",
    strategy: "delete+insert",
  },
  {
    value: "time_interval",
    label: "Incremental (time interval)",
    type: "table",
    strategy: "time_interval",
  },
];

function materializationOptionForCapability(
  capability: MaterializationCapability,
): MaterializationOption {
  const known = MATERIALIZATION_OPTIONS.find((option) => option.value === capability.mode);
  return {
    value: capability.mode,
    label: known?.label ?? capability.mode,
    type: capability.type,
    strategy: capability.strategy,
    capability,
  };
}

function currentMaterializationMode(asset: WebAsset) {
  const type = (asset.materialization_type ?? "").toLowerCase();
  const strategy = (asset.materialization_strategy ?? "").toLowerCase();
  if (!type) {
    return (asset.materialization_capabilities ?? []).some((item) => item.mode === "none")
      ? "none"
      : "create+replace";
  }
  if (type === "view") return "view";
  if (type === "table" && !strategy) return "create+replace";
  if (["create+replace", "create_replace", "full-refresh", "full_refresh"].includes(strategy)) {
    return "create+replace";
  }
  if (["truncate+insert", "truncate_insert", "truncate"].includes(strategy)) {
    return "truncate+insert";
  }
  if (strategy === "delete_insert") return "delete+insert";
  return strategy;
}

export function currentMaterializationOption(asset: WebAsset): MaterializationOption {
  const mode = currentMaterializationMode(asset);
  const capability = (asset.materialization_capabilities ?? []).find((item) => item.mode === mode);
  if (capability) return materializationOptionForCapability(capability);

  const type = (asset.materialization_type ?? "").trim();
  const strategy = (asset.materialization_strategy ?? "").trim();
  const detail = strategy || type || mode;
  return {
    value: `custom:${type}:${strategy || mode}`,
    label: `Custom (${detail})`,
    type,
    strategy,
    custom: true,
  };
}

export function materializationEditorState(asset: WebAsset) {
  const options = (asset.materialization_capabilities ?? []).map(
    materializationOptionForCapability,
  );
  const selected = currentMaterializationOption(asset);
  const hasDeclaredMaterialization = Boolean(
    (asset.materialization_type ?? "").trim() || (asset.materialization_strategy ?? "").trim(),
  );
  if (selected.custom && (options.length > 0 || hasDeclaredMaterialization)) {
    options.unshift(selected);
  }
  return {
    selected,
    selectedValue: selected.value,
    options,
    hasEditor: options.length > 0,
  };
}

export function inferMaterializationTimeGranularity(asset: WebAsset, incrementalKey?: string) {
  const key = (incrementalKey ?? asset.incremental_key ?? "").trim().toLowerCase();
  const columnType = (asset.columns ?? [])
    .find((column) => column.name.trim().toLowerCase() === key)
    ?.type?.trim()
    .toLowerCase();
  return columnType?.replace(/\(.*/, "").trim() === "date" ? "date" : "timestamp";
}

export function materializationSelectionInput(asset: WebAsset, option: MaterializationOption) {
  const timeGranularity =
    asset.time_granularity ||
    ((asset.incremental_key ?? "").trim()
      ? inferMaterializationTimeGranularity(asset, asset.incremental_key)
      : "");
  return {
    materialization_type: option.type,
    materialization_strategy: option.strategy,
    ...(option.capability?.requires_time_granularity && timeGranularity
      ? { time_granularity: timeGranularity }
      : {}),
  };
}

export function ColumnCombobox({
  id,
  columns,
  value,
  placeholder,
  className,
  onChange,
}: {
  id?: string;
  columns: WebColumn[];
  value: string;
  placeholder: string;
  className?: string;
  onChange: (value: string) => void;
}) {
  const items = columns.map((column) => column.name).filter(Boolean);
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          id={id}
          variant="outline"
          role="combobox"
          aria-expanded={open}
          className={cn("w-full min-w-0 justify-between font-monaco font-normal", className)}
        >
          <span className="truncate">
            {value || (items.length === 0 ? "Add or infer columns first" : placeholder)}
          </span>
          <ChevronsUpDown data-icon="inline-end" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[var(--radix-popover-trigger-width)] p-0">
        <Command>
          <CommandInput placeholder="Search columns…" />
          <CommandList>
            <CommandEmpty>
              {items.length === 0
                ? "No declared columns. Add or infer columns first."
                : "No matching column."}
            </CommandEmpty>
            <CommandGroup>
              {value ? (
                <CommandItem
                  value="__clear_update_key__"
                  onSelect={() => {
                    onChange("");
                    setOpen(false);
                  }}
                >
                  No update key
                </CommandItem>
              ) : null}
              {items.map((item) => (
                <CommandItem
                  key={item}
                  value={item}
                  onSelect={() => {
                    onChange(item);
                    setOpen(false);
                  }}
                >
                  <Check className={cn(value === item ? "opacity-100" : "opacity-0")} />
                  {item}
                </CommandItem>
              ))}
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  );
}

export function MaterializationCard({
  asset,
  pipelineId,
}: {
  asset: WebAsset;
  pipelineId: string;
}) {
  const fieldIdPrefix = `${useId()}-materialization`;
  const fieldId = (name: string) => `${fieldIdPrefix}-${name}`;
  const { selected, selectedValue, options, hasEditor } = materializationEditorState(asset);
  const primaryKeys = (asset.columns ?? [])
    .filter((column) => column.primary_key)
    .map((column) => column.name);
  const [error, setError] = useState("");

  const save = (input: Parameters<typeof updateAsset>[2]) => {
    setError("");
    void updateAsset(pipelineId, asset.id, input).catch((cause) => {
      setError(cause instanceof Error ? cause.message : "Could not update materialization");
    });
  };

  if (!hasEditor) return null;

  return (
    <GuidedCard title="Materialization">
      <FieldGroup>
        <FieldRow label="Write behavior" htmlFor={fieldId("write-behavior")}>
          <Select
            value={selectedValue}
            onValueChange={(value) => {
              const option = options.find((item) => item.value === value);
              if (!option || option.custom) return;
              save(materializationSelectionInput(asset, option));
            }}
          >
            <SelectTrigger id={fieldId("write-behavior")} className="h-8">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {options.map((option) => (
                  <SelectItem key={option.value} value={option.value} disabled={option.custom}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </FieldRow>
        {selected.capability?.requires_incremental_key ||
        selected.capability?.supports_incremental_key ? (
          <FieldRow
            htmlFor={fieldId("incremental-key")}
            label={
              selected.capability?.requires_incremental_key
                ? "Incremental key"
                : "Update key (optional)"
            }
          >
            <ColumnCombobox
              id={fieldId("incremental-key")}
              columns={asset.columns ?? []}
              value={asset.incremental_key ?? ""}
              placeholder={
                selected.capability?.requires_incremental_key ? "loaded_at" : "updated_at"
              }
              onChange={(key) => {
                if (key !== (asset.incremental_key ?? "")) {
                  save({
                    incremental_key: key,
                    ...(selected.capability?.requires_time_granularity && !asset.time_granularity
                      ? { time_granularity: inferMaterializationTimeGranularity(asset, key) }
                      : {}),
                  });
                }
              }}
            />
          </FieldRow>
        ) : null}
        {selected.capability?.requires_time_granularity ? (
          <FieldRow label="Time granularity" htmlFor={fieldId("time-granularity")}>
            <Select
              value={asset.time_granularity ?? ""}
              onValueChange={(timeGranularity) => save({ time_granularity: timeGranularity })}
            >
              <SelectTrigger id={fieldId("time-granularity")} className="h-8">
                <SelectValue placeholder="Select date or timestamp" />
              </SelectTrigger>
              <SelectContent>
                <SelectGroup>
                  <SelectItem value="timestamp">Timestamp</SelectItem>
                  <SelectItem value="date">Date</SelectItem>
                </SelectGroup>
              </SelectContent>
            </Select>
          </FieldRow>
        ) : null}
        {selected.capability?.supports_partition_by ? (
          <FieldRow label="Partition by" htmlFor={fieldId("partition-by")}>
            <CommitInput
              id={fieldId("partition-by")}
              mono
              value={asset.partition_by ?? ""}
              placeholder="event_date"
              onCommit={(partitionBy) => {
                if (partitionBy !== (asset.partition_by ?? "")) {
                  save({ partition_by: partitionBy });
                }
              }}
            />
          </FieldRow>
        ) : null}
        {selected.capability?.supports_cluster_by ? (
          <FieldRow label="Cluster by" htmlFor={fieldId("cluster-by")}>
            <MultiValueInput
              id={fieldId("cluster-by")}
              value={asset.cluster_by ?? []}
              placeholder="Add column or expression"
              onChange={(clusterBy) => {
                if (clusterBy.join("\n") !== (asset.cluster_by ?? []).join("\n")) {
                  save({ cluster_by: clusterBy });
                }
              }}
            />
          </FieldRow>
        ) : null}
        {selected.capability?.requires_primary_key ? (
          <p
            className={cn(
              "text-[11px]",
              primaryKeys.length === 0 ? "text-destructive" : "text-muted-foreground",
            )}
          >
            {primaryKeys.length === 0
              ? `${selected.value === "merge" ? "Merge" : "This mode"} needs at least one primary-key column. Set one with the key control in Columns.`
              : `Primary key${primaryKeys.length === 1 ? "" : "s"}: ${primaryKeys.join(", ")}`}
          </p>
        ) : null}
        {error ? <p className="text-[11px] text-destructive">{error}</p> : null}
      </FieldGroup>
    </GuidedCard>
  );
}

// --- Dependencies card (§14.3) ---

export function DependenciesCard({
  asset,
  onGoToAsset,
}: {
  asset: WebAsset;
  onGoToAsset?: (pipelineId: string, assetId: string) => void;
}) {
  const { inferred, manual, ignored } = useMemo(() => classifyDependencies(asset), [asset]);

  const apply = (tx: Parameters<typeof applyAssetTransaction>[1]) => {
    void applyAssetTransaction(asset.id, tx);
  };

  const isEmpty = inferred.length === 0 && manual.length === 0 && ignored.length === 0;
  const present = useMemo(
    () =>
      new Set(
        [...inferred, ...manual].map(
          (dependency) => `${dependency.kind}:${dependency.value.trim().toLowerCase()}`,
        ),
      ),
    [inferred, manual],
  );

  return (
    <GuidedCard title="Dependencies">
      {isEmpty ? <p className="text-[11px] text-muted-foreground">No dependencies yet.</p> : null}

      {inferred.length > 0 ? (
        <DepSection label="Inferred from SQL">
          {inferred.map((dep) => (
            <DepRow
              key={dep.key}
              name={dep.name}
              detail={dep.resolvedPipelineName}
              onNavigate={
                dep.resolvedPipelineId && dep.resolvedAssetId && onGoToAsset
                  ? () => onGoToAsset(dep.resolvedPipelineId!, dep.resolvedAssetId!)
                  : undefined
              }
              badge={
                dep.kind === "uri"
                  ? dep.mode === "symbolic"
                    ? "URI · symbolic"
                    : "URI"
                  : undefined
              }
              actionLabel="Ignore"
              actionIcon={<Ban className="size-3" />}
              onAction={() =>
                apply({
                  type: "dependency.inferred.ignore",
                  dependency_key: dep.key,
                })
              }
            />
          ))}
        </DepSection>
      ) : null}

      {manual.length > 0 ? (
        <DepSection label="Manual">
          {manual.map((dep) => (
            <DepRow
              key={dep.key}
              name={dep.name}
              detail={dep.resolvedPipelineName}
              badge={dep.kind === "uri" ? "URI" : undefined}
              onNavigate={
                dep.resolvedPipelineId && dep.resolvedAssetId && onGoToAsset
                  ? () => onGoToAsset(dep.resolvedPipelineId!, dep.resolvedAssetId!)
                  : undefined
              }
              mode={dep.mode}
              onModeChange={(mode) =>
                apply({
                  type: "dependency.manual.mode.set",
                  dependency_key: dep.key,
                  dependency_mode: mode,
                })
              }
              actionLabel="Remove"
              actionIcon={<Trash2 className="size-3" />}
              onAction={() =>
                apply({
                  type: "dependency.manual.remove",
                  dependency_key: dep.key,
                })
              }
            />
          ))}
        </DepSection>
      ) : null}

      {ignored.length > 0 ? (
        <DepSection label="Ignored">
          {ignored.map((dep) => (
            <DepRow
              key={dep.key}
              name={dep.value}
              muted
              actionLabel="Restore"
              actionIcon={<RotateCcw className="size-3" />}
              onAction={() =>
                apply({
                  type: "dependency.inferred.restore",
                  dependency_key: dep.key,
                })
              }
            />
          ))}
        </DepSection>
      ) : null}

      <AssetDependencyPicker
        assetId={asset.id}
        present={present}
        onPick={(dependency) => apply({ type: "dependency.manual.add", dependency })}
      />
    </GuidedCard>
  );
}

function DepSection({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-0.5">
      <div className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground/70">
        {label}
      </div>
      <div className="divide-y rounded-md border">{children}</div>
    </div>
  );
}

function DepRow({
  name,
  detail,
  badge,
  mode,
  muted,
  onNavigate,
  onModeChange,
  actionLabel,
  actionIcon,
  onAction,
}: {
  name: string;
  detail?: string;
  badge?: string;
  mode?: "full" | "symbolic";
  muted?: boolean;
  onNavigate?: () => void;
  onModeChange?: (mode: "full" | "symbolic") => void;
  actionLabel: string;
  actionIcon: React.ReactNode;
  onAction: () => void;
}) {
  return (
    <div className="group flex items-center gap-1.5 px-2 py-1 text-xs">
      <span className={cn("min-w-0 flex-1", muted && "text-muted-foreground line-through")}>
        {onNavigate ? (
          <Button
            type="button"
            variant="link"
            size="xs"
            className="block h-auto max-w-full truncate p-0 font-monaco text-xs"
            onClick={onNavigate}
          >
            {name}
          </Button>
        ) : (
          <span className="block truncate font-monaco">{name}</span>
        )}
        {detail ? (
          <span className="block truncate font-sans text-[10px] text-muted-foreground">
            {detail}
          </span>
        ) : null}
      </span>
      {badge ? (
        <span className="rounded bg-muted px-1 text-[10px] text-muted-foreground">{badge}</span>
      ) : null}
      {mode && onModeChange ? (
        <Select value={mode} onValueChange={(value) => onModeChange(value as "full" | "symbolic")}>
          <SelectTrigger
            size="sm"
            className="h-6 w-[6.25rem] text-[10px]"
            aria-label={`Dependency mode for ${name}`}
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              <SelectItem value="full">Full</SelectItem>
              <SelectItem value="symbolic">Symbolic</SelectItem>
            </SelectGroup>
          </SelectContent>
        </Select>
      ) : null}
      <Button
        variant="ghost"
        size="xs"
        className="size-6 shrink-0 p-0 text-muted-foreground opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
        title={actionLabel}
        aria-label={actionLabel}
        onClick={onAction}
      >
        {actionIcon}
      </Button>
    </div>
  );
}

// --- Columns card (§14.4) ---

function fallbackColumnInferenceSources(asset: WebAsset): ColumnInferenceSource[] {
  const mode = getAssetColumnRefreshMode(asset.type, asset.parameters);
  if (mode === "none") return [];
  if (mode === "api") {
    return [
      {
        id: "live_response",
        label: "Live request",
        category: "observed",
        description: "A sampled API response using the asset's current request settings.",
        may_omit_columns: true,
      },
    ];
  }
  if (mode === "materialized") {
    return [
      {
        id: "materialized",
        label: "Current table",
        category: "observed",
        description: "The schema currently reported by the asset's warehouse relation.",
      },
    ];
  }
  return [
    {
      id: "definition",
      label: isSeedAssetType(asset.type) ? "Seed file" : "Asset definition",
      category: "definition",
      description: isSeedAssetType(asset.type)
        ? "The schema Sling detects in the local seed file."
        : "The output schema inferred from the asset definition.",
    },
  ];
}

export function ColumnsCard({
  asset,
  environmentOverride,
  focusedColumn,
  focusToken,
  onFocusColumn,
  focusedField = "type",
}: {
  asset: WebAsset;
  environmentOverride?: string;
  focusedColumn?: string;
  focusToken?: string;
  onFocusColumn?: (column: string, field?: ColumnTarget["field"]) => void;
  focusedField?: ColumnTarget["field"];
}) {
  const schemaSourceIdPrefix = `${useId()}-schema-source`;
  const manualColumnInputId = `${schemaSourceIdPrefix}-manual-column`;
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const environment = environmentOverride ?? selectedEnvironment;
  const workspace = useAtomValue(workspaceAtom);
  const sources = useMemo(
    () =>
      asset.column_inference_sources?.length
        ? asset.column_inference_sources
        : fallbackColumnInferenceSources(asset),
    [asset.column_inference_sources, asset.parameters, asset.type],
  );
  const isSQLMerge =
    isSqlAssetType(asset.type) && asset.materialization_strategy?.toLowerCase() === "merge";
  const provenance = useMemo(
    () => parseAssetProvenance(asset.meta, asset.columns),
    [asset.meta, asset.columns],
  );
  const columns = asset.columns ?? [];
  const impactsByColumn = useMemo(
    () => columnImpactsForAsset(workspace?.artifact_index, asset.id),
    [asset.id, workspace?.artifact_index],
  );
  const definitionSources = useMemo(
    () => sources.filter((source) => source.category === "definition"),
    [sources],
  );
  const advisorySources = useMemo(
    () => sources.filter((source) => source.category === "observed"),
    [sources],
  );
  // Columns the user has ignored (renart_col_drop) that aren't currently present.
  const ignored = useMemo(() => {
    const present = new Set(columns.map((column) => column.name.toLowerCase()));
    return [...provenance.colDrop].filter((name) => !present.has(name)).sort();
  }, [provenance, columns]);
  const [selectedAdvisorySources, setSelectedAdvisorySources] = useState<string[]>([]);
  const [syncResult, setSyncResult] = useState<ColumnSchemaSyncResult | null>(null);
  const [resolverOpen, setResolverOpen] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [applying, setApplying] = useState(false);
  const [manualColumnName, setManualColumnName] = useState("");
  const [addingManualColumn, setAddingManualColumn] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    const available = new Set(advisorySources.map((source) => source.id));
    setSelectedAdvisorySources((selected) => selected.filter((source) => available.has(source)));
  }, [advisorySources]);

  useEffect(() => {
    setSelectedAdvisorySources([]);
    setSyncResult(null);
    setResolverOpen(false);
    setManualColumnName("");
    setAddingManualColumn(false);
    setError(null);
    setNotice(null);
  }, [asset.id]);

  const runSync = async () => {
    if (definitionSources.length === 0 && selectedAdvisorySources.length === 0) return;
    setSyncing(true);
    setError(null);
    setNotice(null);
    try {
      const result = await syncAssetColumns(asset.id, selectedAdvisorySources, environment);
      if (result.status === "conflicts") {
        setSyncResult(result);
        setResolverOpen(true);
      } else {
        setSyncResult(null);
        setResolverOpen(false);
        const baseNotice =
          result.status === "applied"
            ? "Schema synced. Safe changes were applied automatically."
            : "Schema is already in sync.";
        const sourceNotes = result.sources.flatMap((source) => source.notes ?? []);
        setNotice([baseNotice, ...(result.notes ?? []), ...sourceNotes].join(" "));
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to sync schema");
    } finally {
      setSyncing(false);
    }
  };

  const applyResolution = async (resolutions: ColumnSchemaResolution[]) => {
    if (!syncResult) return;
    setApplying(true);
    setError(null);
    try {
      await applyAssetColumnSchemaResolution(asset.id, syncResult, resolutions);
      setSyncResult(null);
      setResolverOpen(false);
      setNotice("Schema resolution applied.");
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to apply schema resolution");
    } finally {
      setApplying(false);
    }
  };

  const addManualColumn = async () => {
    const name = manualColumnName.trim();
    if (!name || addingManualColumn) return;
    if (columns.some((column) => column.name.trim().toLowerCase() === name.toLowerCase())) {
      setError(`Column ${name} already exists.`);
      return;
    }
    setAddingManualColumn(true);
    setError(null);
    setNotice(null);
    try {
      await applyAssetTransaction(asset.id, {
        type: "column.manual.add",
        column_def: { name },
      });
      setManualColumnName("");
      setNotice(`Added manual column ${name}.`);
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Failed to add column");
    } finally {
      setAddingManualColumn(false);
    }
  };

  const setDescription = (column: string, description: string) => {
    void applyAssetTransaction(asset.id, {
      type: "column.description.set",
      column,
      description,
    });
  };

  const dropColumn = (column: string) => {
    void applyAssetTransaction(asset.id, {
      type: "column.inferred.drop",
      column,
    });
  };

  const commitType = (column: WebColumn, nextType: string) => {
    if (nextType === (column.type ?? "")) return;
    const nextColumns = columns.map((c) =>
      c.name.toLowerCase() === column.name.toLowerCase() ? { ...c, type: nextType } : c,
    );
    void updateAssetColumns(asset.id, nextColumns).then(() =>
      applyAssetTransaction(asset.id, {
        type: "column.field.own",
        column: column.name,
        field: "type",
      }),
    );
  };

  // primary_key counts as user metadata (columnHasUserMetadata on the server),
  // so a plain columns update survives refresh-from-definition merges.
  const togglePrimaryKey = (column: WebColumn) => {
    const nextColumns = columns.map((c) =>
      c.name.toLowerCase() === column.name.toLowerCase()
        ? { ...c, primary_key: !c.primary_key }
        : c,
    );
    void updateAssetColumns(asset.id, nextColumns);
  };

  const toggleUpdateOnMerge = (column: WebColumn) => {
    const nextColumns = columns.map((c) =>
      c.name.toLowerCase() === column.name.toLowerCase()
        ? { ...c, update_on_merge: !c.update_on_merge }
        : c,
    );
    void updateAssetColumns(asset.id, nextColumns);
  };

  const commitMergeSQL = (column: WebColumn, mergeSQL: string) => {
    if (mergeSQL === (column.merge_sql ?? "")) return;
    const nextColumns = columns.map((c) =>
      c.name.toLowerCase() === column.name.toLowerCase() ? { ...c, merge_sql: mergeSQL } : c,
    );
    void updateAssetColumns(asset.id, nextColumns);
  };

  return (
    <GuidedCard
      title="Columns"
      action={
        <div className="flex min-w-0 flex-wrap items-center justify-end gap-x-2 gap-y-1.5">
          {advisorySources.map((source) => {
            const id = `${schemaSourceIdPrefix}-${source.id}`;
            const checked = selectedAdvisorySources.includes(source.id);
            return (
              <Field
                key={source.id}
                orientation="horizontal"
                className="w-auto gap-1.5 *:data-[slot=field-label]:flex-none"
                title={source.description}
              >
                <Checkbox
                  id={id}
                  checked={checked}
                  disabled={syncing || applying}
                  onCheckedChange={(nextChecked) =>
                    setSelectedAdvisorySources((selected) =>
                      nextChecked === true
                        ? [...new Set([...selected, source.id])]
                        : selected.filter((sourceID) => sourceID !== source.id),
                    )
                  }
                />
                <FieldLabel htmlFor={id} className="cursor-pointer text-[11px] font-normal">
                  {source.label}
                </FieldLabel>
              </Field>
            );
          })}
          <Button
            variant="outline"
            size="xs"
            disabled={
              syncing ||
              applying ||
              (definitionSources.length === 0 && selectedAdvisorySources.length === 0)
            }
            onClick={() => void runSync()}
            title="Infer the asset schema and apply safe changes"
          >
            {syncing ? (
              <Spinner data-icon="inline-start" />
            ) : (
              <RefreshCw data-icon="inline-start" />
            )}
            Sync schema
          </Button>
        </div>
      }
    >
      {error && !resolverOpen ? <p className="text-[11px] text-destructive">{error}</p> : null}
      {notice ? (
        <p role="status" className="text-[11px] text-muted-foreground">
          {notice}
        </p>
      ) : null}

      <SchemaSyncDialog
        open={resolverOpen}
        result={syncResult}
        applying={applying}
        error={resolverOpen ? error : null}
        onOpenChange={setResolverOpen}
        onApply={(resolutions) => void applyResolution(resolutions)}
      />

      {focusedColumn && ["update_on_merge", "merge_sql"].includes(focusedField) && !isSQLMerge ? (
        <p role="alert" className="text-xs">
          The linked field is not available for this materialization.
        </p>
      ) : null}
      {columns.length === 0 ? (
        <p className="text-[11px] text-muted-foreground">
          No columns. Add one manually or sync the schema from an available source.
        </p>
      ) : (
        <div className="flex flex-col gap-1.5">
          <p className="text-[11px] text-muted-foreground">
            Select a column to edit its type, description, and behavior.
          </p>
          <div className="divide-y overflow-hidden rounded-md border">
            {columns.map((column) => (
              <ColumnRow
                key={column.name}
                column={column}
                focusToken={column.name === focusedColumn ? focusToken : undefined}
                onReveal={() => onFocusColumn?.(column.name)}
                focusedField={focusedField}
                onFieldFocus={(field) => {
                  if (column.name !== focusedColumn || field !== focusedField)
                    onFocusColumn?.(column.name, field);
                }}
                status={columnStatus(column.name, provenance)}
                onCommitType={(type) => commitType(column, type)}
                onCommitDescription={(description) => setDescription(column.name, description)}
                onTogglePrimaryKey={() => togglePrimaryKey(column)}
                showMergeFields={isSQLMerge}
                onToggleUpdateOnMerge={() => toggleUpdateOnMerge(column)}
                onCommitMergeSQL={(mergeSQL) => commitMergeSQL(column, mergeSQL)}
                onDrop={() => dropColumn(column.name)}
                impacts={impactsByColumn.get(column.name.toLowerCase()) ?? []}
              />
            ))}
          </div>
        </div>
      )}

      <Field className="gap-1.5">
        <FieldLabel htmlFor={manualColumnInputId} className="text-[11px] font-normal">
          Add column
        </FieldLabel>
        <InputGroup>
          <InputGroupInput
            id={manualColumnInputId}
            value={manualColumnName}
            placeholder="Column name"
            disabled={addingManualColumn}
            onChange={(event) => setManualColumnName(event.target.value)}
            onKeyDown={(event) => {
              if (event.key !== "Enter") return;
              event.preventDefault();
              void addManualColumn();
            }}
          />
          <InputGroupAddon align="inline-end">
            <InputGroupButton
              aria-label="Add column"
              disabled={!manualColumnName.trim() || addingManualColumn}
              onClick={() => void addManualColumn()}
            >
              {addingManualColumn ? (
                <Spinner data-icon="inline-start" />
              ) : (
                <Plus data-icon="inline-start" />
              )}
              Add
            </InputGroupButton>
          </InputGroupAddon>
        </InputGroup>
      </Field>

      {ignored.length > 0 ? (
        <DepSection label="Ignored">
          {ignored.map((name) => (
            <DepRow
              key={name}
              name={name}
              muted
              actionLabel="Restore"
              actionIcon={<RotateCcw className="size-3" />}
              onAction={() =>
                applyAssetTransaction(asset.id, {
                  type: "column.inferred.restore",
                  column: name,
                })
              }
            />
          ))}
        </DepSection>
      ) : null}
    </GuidedCard>
  );
}

// --- Quality checks card (§14.5) ---

// The standard Bruin column checks. Value-bearing ones take an argument
// (accepted_values a list, min/max a number, pattern a regex); the rest are
// presence assertions.
export const COLUMN_CHECK_NAMES = [
  "not_null",
  "unique",
  "positive",
  "non_negative",
  "negative",
  "accepted_values",
  "pattern",
  "min",
  "max",
] as const;
export const VALUE_CHECKS = new Set(["accepted_values", "pattern", "min", "max"]);

export function checkValueFor(checkName: string, raw: string): unknown {
  if (checkName === "accepted_values") {
    return raw
      .split(",")
      .map((part) => part.trim())
      .filter(Boolean);
  }
  if (checkName === "min" || checkName === "max") {
    const parsed = Number(raw);
    return Number.isNaN(parsed) ? undefined : parsed;
  }
  if (checkName === "pattern") {
    return raw.trim() || undefined;
  }
  return undefined;
}

export function formatCheckValue(value: unknown): string {
  if (value === undefined || value === null || value === "") return "";
  if (Array.isArray(value)) return `: ${value.join(", ")}`;
  return `: ${String(value)}`;
}

function columnCheckKey(column: string, name: string) {
  return `${column.trim().toLowerCase()}\u0000${name.trim().toLowerCase()}`;
}

export function QualityChecksCard({
  asset,
  quality,
  focusedCheck,
  onFocusCheck,
}: {
  asset: WebAsset;
  quality?: AssetStaleness;
  focusedCheck?: QualityCheckFocus | null;
  onFocusCheck: (check: FailedQualityCheck) => void;
}) {
  const columns = asset.columns ?? [];
  const columnsWithChecks = columns.filter((column) => (column.checks?.length ?? 0) > 0);
  const [column, setColumn] = useState("");
  const [checkName, setCheckName] = useState<string>(COLUMN_CHECK_NAMES[0]);
  const [value, setValue] = useState("");
  const [highlightedColumnCheck, setHighlightedColumnCheck] = useState("");
  const columnCheckElements = useRef(new Map<string, HTMLSpanElement>());
  const failedChecks =
    quality?.quality_status === "failed" && quality.quality_on_current_content
      ? (quality.failed_checks ?? [])
      : [];

  useEffect(() => {
    if (focusedCheck?.kind !== "column" || !focusedCheck.column) return;
    const key = columnCheckKey(focusedCheck.column, focusedCheck.name);
    const element = columnCheckElements.current.get(key);
    if (!element) return;
    element.focus({ preventScroll: true });
    const viewport = element.closest<HTMLElement>('[data-slot="scroll-area-viewport"]');
    if (viewport)
      viewport.scrollTop +=
        element.getBoundingClientRect().top -
        viewport.getBoundingClientRect().top -
        viewport.clientHeight / 2;
    setHighlightedColumnCheck(key);
    const timeout = window.setTimeout(() => setHighlightedColumnCheck(""), 2200);
    return () => window.clearTimeout(timeout);
  }, [focusedCheck?.column, focusedCheck?.kind, focusedCheck?.name, focusedCheck?.token]);

  const apply = (tx: Parameters<typeof applyAssetTransaction>[1]) => {
    void applyAssetTransaction(asset.id, tx);
  };

  const addCheck = () => {
    if (!column) return;
    const checkValue = checkValueFor(checkName, value);
    const check: { name: string; value?: unknown } = { name: checkName };
    if (checkValue !== undefined) check.value = checkValue;
    apply({ type: "column.check.add", column, check });
    setValue("");
  };

  return (
    <GuidedCard title="Quality checks">
      {failedChecks.length > 0 ? (
        <div
          data-testid="failed-quality-checks"
          className="rounded-md border border-destructive/30 bg-destructive/5 p-2.5"
        >
          <div className="flex items-start gap-2">
            <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-destructive" />
            <div className="min-w-0">
              <p className="text-[11px] font-medium text-foreground">
                {failedChecks.length === 1
                  ? "The latest check failed"
                  : `${failedChecks.length} checks failed`}
              </p>
              <p className="mt-0.5 text-[10px] text-muted-foreground">
                The materialized data is still tracked separately from these assertions.
              </p>
            </div>
          </div>
          <div className="mt-2 flex flex-wrap gap-1">
            {failedChecks.map((check) => (
              <button
                key={`${check.kind}:${check.column ?? ""}:${check.name}`}
                type="button"
                className="rounded-full border bg-background px-2 py-0.5 font-mono text-[10px] text-foreground outline-none hover:bg-muted focus-visible:ring-1 focus-visible:ring-ring"
                onClick={() => onFocusCheck(check)}
              >
                {check.kind === "column" && check.column
                  ? `${check.column} · ${check.name}`
                  : check.name}
              </button>
            ))}
          </div>
        </div>
      ) : null}
      <AssetCustomChecks
        asset={asset}
        focusedCheck={
          focusedCheck?.kind === "custom"
            ? { name: focusedCheck.name, token: focusedCheck.token }
            : undefined
        }
      />
      <div className="space-y-2.5 border-t pt-3">
        <p className="text-[11px] font-medium text-foreground">Column checks</p>
        {columns.length === 0 ? (
          <p className="text-[11px] text-muted-foreground">
            Add columns first to attach column checks.
          </p>
        ) : (
          <>
            {columnsWithChecks.length === 0 ? (
              <p className="text-[11px] text-muted-foreground">No column checks yet.</p>
            ) : null}
            {columnsWithChecks.map((col) => (
              <div key={col.name} className="space-y-1">
                <div className="font-monaco text-[11px] text-foreground">{col.name}</div>
                <div className="flex flex-wrap gap-1">
                  {(col.checks ?? []).map((check, index) => (
                    <span
                      key={`${check.name}-${index}`}
                      ref={(element) => {
                        const key = columnCheckKey(col.name, check.name);
                        if (element) columnCheckElements.current.set(key, element);
                        else columnCheckElements.current.delete(key);
                      }}
                      data-column-check={`${col.name}:${check.name}`}
                      tabIndex={-1}
                      data-highlighted={
                        highlightedColumnCheck === columnCheckKey(col.name, check.name)
                          ? "true"
                          : undefined
                      }
                      className={cn(
                        "inline-flex items-center gap-1 rounded-full border bg-muted/40 px-2 py-0.5 text-[10px] transition-[border-color,box-shadow,background-color] duration-500",
                        highlightedColumnCheck === columnCheckKey(col.name, check.name) &&
                          "border-destructive/70 bg-destructive/5 ring-2 ring-destructive/20",
                      )}
                    >
                      {check.name}
                      {formatCheckValue(check.value)}
                      <button
                        type="button"
                        className="text-muted-foreground hover:text-foreground"
                        aria-label={`Remove ${check.name} from ${col.name}`}
                        onClick={() =>
                          apply({
                            type: "column.check.remove",
                            column: col.name,
                            check: { name: check.name },
                          })
                        }
                      >
                        <X className="size-2.5" />
                      </button>
                    </span>
                  ))}
                </div>
              </div>
            ))}
            <div className="space-y-1.5">
              <div className="flex items-center gap-1.5">
                <Select value={column} onValueChange={setColumn}>
                  <SelectTrigger className="h-7 text-xs">
                    <SelectValue placeholder="Column" />
                  </SelectTrigger>
                  <SelectContent>
                    {columns
                      .filter((col) => col.name)
                      .map((col) => (
                        <SelectItem key={col.name} value={col.name}>
                          {col.name}
                        </SelectItem>
                      ))}
                  </SelectContent>
                </Select>
                <Select value={checkName} onValueChange={setCheckName}>
                  <SelectTrigger className="h-7 text-xs">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {COLUMN_CHECK_NAMES.map((name) => (
                      <SelectItem key={name} value={name}>
                        {name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              {VALUE_CHECKS.has(checkName) ? (
                <Input
                  className="h-7 text-xs"
                  placeholder={
                    checkName === "accepted_values"
                      ? "a, b, c"
                      : checkName === "pattern"
                        ? "regex"
                        : "number"
                  }
                  value={value}
                  onChange={(event) => setValue(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") addCheck();
                  }}
                />
              ) : null}
              <Button variant="outline" size="xs" disabled={!column} onClick={addCheck}>
                <Plus className="size-3" />
                Add check
              </Button>
            </div>
          </>
        )}
      </div>
    </GuidedCard>
  );
}

function ColumnRow({
  focusedField,
  onFieldFocus,
  column,
  focusToken,
  onReveal,
  status,
  onCommitType,
  onCommitDescription,
  onTogglePrimaryKey,
  showMergeFields,
  onToggleUpdateOnMerge,
  onCommitMergeSQL,
  onDrop,
  impacts,
}: {
  column: WebColumn;
  focusedField: ColumnTarget["field"];
  onFieldFocus: (field: ColumnTarget["field"]) => void;
  focusToken?: string;
  onReveal?: () => void;
  status: ReturnType<typeof columnStatus>;
  onCommitType: (type: string) => void;
  onCommitDescription: (description: string) => void;
  onTogglePrimaryKey: () => void;
  showMergeFields: boolean;
  onToggleUpdateOnMerge: () => void;
  onCommitMergeSQL: (mergeSQL: string) => void;
  onDrop: () => void;
  impacts: ColumnImpactView[];
}) {
  const fieldIdPrefix = `${useId()}-column`;
  const typeInputId = `${fieldIdPrefix}-type`;
  const descriptionInputId = `${fieldIdPrefix}-description`;
  const primaryKeyInputId = `${fieldIdPrefix}-primary-key`;
  const updateOnMergeInputId = `${fieldIdPrefix}-update-on-merge`;
  const mergeSQLInputId = `${fieldIdPrefix}-merge-sql`;
  const [open, setOpen] = useState(Boolean(focusToken));
  const focusedToken = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (focusToken) setOpen(true);
    else focusedToken.current = undefined;
  }, [focusToken]);
  const focusType = useCallback(
    (input: HTMLElement | null) => {
      if (!input || !focusToken || focusedToken.current === focusToken) return;
      const frame = requestAnimationFrame(() => {
        if (!input.isConnected || input.getClientRects().length === 0) return;
        focusedToken.current = focusToken;
        input.focus({ preventScroll: true });
        const viewport = input.closest('[data-slot="scroll-area-viewport"]');
        if (viewport)
          viewport.scrollTop +=
            input.getBoundingClientRect().top - viewport.getBoundingClientRect().top - 48;
      });
      return () => cancelAnimationFrame(frame);
    },
    [focusToken],
  );
  const focusField = (field: ColumnTarget["field"]) =>
    field === focusedField ? focusType : undefined;
  const affectedArtifactCount = new Set(impacts.map((impact) => artifactRefKey(impact.consumer)))
    .size;

  return (
    <Collapsible
      className="group/column"
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) onReveal?.();
      }}
    >
      <CollapsibleTrigger asChild>
        <Button
          variant="ghost"
          className="h-auto w-full justify-start rounded-none px-2.5 py-2 text-left hover:bg-muted/60"
          aria-label={`Edit column ${column.name}`}
        >
          <ChevronRight
            data-icon="inline-start"
            className="text-muted-foreground transition-transform group-data-[state=open]/column:rotate-90"
          />
          <span className="flex min-w-0 flex-1 flex-col gap-1">
            <span className="flex min-w-0 flex-wrap items-center gap-1.5">
              <span className="min-w-0 truncate font-monaco text-xs font-medium">
                {column.name}
              </span>
              <Badge variant="outline" size="xs" className="font-monaco">
                {column.type?.trim() || "Unknown type"}
              </Badge>
              {column.primary_key ? (
                <Badge variant="secondary" size="xs">
                  <KeyRound data-icon="inline-start" />
                  Primary key
                </Badge>
              ) : null}
              <ColumnStatusBadge status={status} />
              {affectedArtifactCount > 0 ? (
                <Badge variant="destructive" size="xs">
                  <AlertTriangle data-icon="inline-start" />
                  {affectedArtifactCount} downstream
                </Badge>
              ) : null}
            </span>
            <span className="truncate text-[11px] font-normal text-muted-foreground">
              {column.description?.trim() || "No description"}
            </span>
          </span>
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent className="border-t bg-muted/20 px-3 py-3">
        <FieldGroup className="gap-3">
          {impacts.length > 0 ? (
            <Alert variant="destructive">
              <AlertTriangle />
              <AlertTitle>Known downstream impact</AlertTitle>
              <AlertDescription>
                Removing or renaming this column would break these statically resolved uses.
                <span className="mt-1.5 flex flex-col gap-1">
                  {impacts.slice(0, 4).map((impact) => (
                    <span
                      key={impact.key}
                      className="flex min-w-0 items-center justify-between gap-2"
                    >
                      <span className="min-w-0 truncate">{impact.label}</span>
                      <Badge variant="outline" size="xs">
                        {impact.useLabel}
                        {impact.distance > 1 ? ` · ${impact.distance} steps` : ""}
                      </Badge>
                    </span>
                  ))}
                  {impacts.length > 4 ? (
                    <span>And {impacts.length - 4} more known uses.</span>
                  ) : null}
                </span>
              </AlertDescription>
            </Alert>
          ) : null}
          <Field>
            <FieldLabel htmlFor={typeInputId}>Type</FieldLabel>
            <CommitInput
              id={typeInputId}
              inputRef={focusField("type")}
              onFocus={() => onFieldFocus("type")}
              className={
                focusToken && focusedField === "type"
                  ? "border-primary ring-2 ring-primary/20"
                  : undefined
              }
              mono
              value={column.type ?? ""}
              placeholder="Unknown"
              onCommit={onCommitType}
            />
          </Field>
          <Field>
            <FieldLabel htmlFor={descriptionInputId}>Description</FieldLabel>
            <CommitInput
              id={descriptionInputId}
              inputRef={focusField("description")}
              onFocus={() => onFieldFocus("description")}
              value={column.description ?? ""}
              placeholder="Describe this column"
              onCommit={onCommitDescription}
            />
          </Field>
          <Field orientation="horizontal" className="w-auto gap-2">
            <Checkbox
              id={primaryKeyInputId}
              ref={focusField("primary_key")}
              onFocus={() => onFieldFocus("primary_key")}
              checked={Boolean(column.primary_key)}
              onCheckedChange={(checked) => {
                if ((checked === true) !== Boolean(column.primary_key)) onTogglePrimaryKey();
              }}
            />
            <FieldLabel htmlFor={primaryKeyInputId} className="cursor-pointer font-normal">
              Primary key
            </FieldLabel>
          </Field>
          {showMergeFields ? (
            <>
              <Field orientation="horizontal" className="w-auto gap-2">
                <Checkbox
                  id={updateOnMergeInputId}
                  ref={focusField("update_on_merge")}
                  onFocus={() => onFieldFocus("update_on_merge")}
                  checked={Boolean(column.update_on_merge)}
                  onCheckedChange={(checked) => {
                    if ((checked === true) !== Boolean(column.update_on_merge)) {
                      onToggleUpdateOnMerge();
                    }
                  }}
                />
                <FieldLabel htmlFor={updateOnMergeInputId} className="cursor-pointer font-normal">
                  Update on merge
                </FieldLabel>
              </Field>
              <Field>
                <FieldLabel htmlFor={mergeSQLInputId}>Merge expression</FieldLabel>
                <CommitInput
                  id={mergeSQLInputId}
                  inputRef={focusField("merge_sql")}
                  onFocus={() => onFieldFocus("merge_sql")}
                  mono
                  value={column.merge_sql ?? ""}
                  placeholder="Optional SQL expression"
                  onCommit={onCommitMergeSQL}
                />
                <FieldDescription>
                  Overrides the ordinary update behavior for this column.
                </FieldDescription>
              </Field>
            </>
          ) : null}
          <div className="flex justify-end">
            {impacts.length > 0 ? (
              <AlertDialog>
                <AlertDialogTrigger asChild>
                  <Button variant="destructive" size="xs">
                    <Trash2 data-icon="inline-start" />
                    Remove column
                  </Button>
                </AlertDialogTrigger>
                <AlertDialogContent>
                  <AlertDialogHeader>
                    <AlertDialogTitle>Remove {column.name}?</AlertDialogTitle>
                    <AlertDialogDescription>
                      Renart found {impacts.length} downstream column use
                      {impacts.length === 1 ? "" : "s"}. Removing this declaration can break those
                      assets and presentation components. Ambiguous dependencies are not included.
                    </AlertDialogDescription>
                  </AlertDialogHeader>
                  <div className="flex flex-col gap-1.5">
                    {impacts.slice(0, 5).map((impact) => (
                      <div
                        key={impact.key}
                        className="flex min-w-0 items-center justify-between gap-2"
                      >
                        <span className="min-w-0 truncate text-xs">{impact.label}</span>
                        <Badge variant="outline" size="xs">
                          {impact.useLabel}
                        </Badge>
                      </div>
                    ))}
                    {impacts.length > 5 ? (
                      <span className="text-xs text-muted-foreground">
                        And {impacts.length - 5} more known uses.
                      </span>
                    ) : null}
                  </div>
                  <AlertDialogFooter>
                    <AlertDialogCancel size="sm">Keep column</AlertDialogCancel>
                    <AlertDialogAction variant="destructive" size="sm" onClick={onDrop}>
                      Remove column
                    </AlertDialogAction>
                  </AlertDialogFooter>
                </AlertDialogContent>
              </AlertDialog>
            ) : (
              <Button variant="destructive" size="xs" onClick={onDrop}>
                <Trash2 data-icon="inline-start" />
                Remove column
              </Button>
            )}
          </div>
        </FieldGroup>
      </CollapsibleContent>
    </Collapsible>
  );
}

function ColumnStatusBadge({ status }: { status: ReturnType<typeof columnStatus> }) {
  const labels: Record<string, string> = {
    inferred: "Inferred from SQL",
    manual: "Added manually",
    "type-owned": "Type overridden",
    "table-inferred": "Inferred from table",
    "live-inferred": "Inferred from response",
  };
  return (
    <Badge variant="muted" size="xs">
      {labels[status] ?? status}
    </Badge>
  );
}

// --- shared field primitives ---

function FieldRow({
  label,
  htmlFor,
  children,
}: {
  label: string;
  htmlFor?: string;
  children: React.ReactNode;
}) {
  return (
    <Field>
      <FieldLabel htmlFor={htmlFor}>{label}</FieldLabel>
      {children}
    </Field>
  );
}

/**
 * An input that holds local edits and commits on blur or Enter, so saves don't
 * fire on every keystroke.
 */
function CommitInput({
  onFocus,
  id,
  inputRef,
  value,
  placeholder,
  onCommit,
  mono,
  className,
  ariaInvalid,
  ariaDescribedBy,
}: {
  id?: string;
  inputRef?: Ref<HTMLInputElement>;
  onFocus?: () => void;
  value: string;
  placeholder?: string;
  onCommit: (value: string) => void;
  mono?: boolean;
  className?: string;
  ariaInvalid?: boolean;
  ariaDescribedBy?: string;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);

  return (
    <Input
      id={id}
      ref={inputRef}
      onFocus={onFocus}
      className={cn("h-8 text-xs", mono && "font-monaco", className)}
      value={draft}
      placeholder={placeholder}
      aria-invalid={ariaInvalid || undefined}
      aria-describedby={ariaDescribedBy}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={() => onCommit(draft)}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.currentTarget.blur();
        }
      }}
    />
  );
}
