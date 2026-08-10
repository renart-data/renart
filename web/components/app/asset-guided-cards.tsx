"use client";

import { useEffect, useId, useMemo, useRef, useState } from "react";

import { useAtomValue } from "jotai";
import {
  AlertTriangle,
  Ban,
  Check,
  ChevronDown,
  ChevronsUpDown,
  KeyRound,
  Plus,
  RefreshCw,
  RotateCcw,
  Trash2,
  X,
} from "lucide-react";

import { selectedEnvironmentAtom, workspaceAtom } from "@/lib/atoms/workspace";
import type { AssetStaleness, FailedQualityCheck } from "@/lib/api-staleness";

import { Button } from "@/components/ui/button";
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
import { classifyDependencies, columnStatus, parseAssetProvenance } from "@/lib/asset-provenance";
import { ScrollArea } from "@/components/ui/scroll-area";
import { getAssetColumnRefreshMode, isSeedAssetType, isSqlAssetType } from "@/lib/asset-types";
import { cn } from "@/lib/utils";
import { WebAsset, WebColumn } from "@/lib/types";
import { AssetConnectionEditor } from "./asset-connection-editor";
import { AssetCustomChecks } from "./asset-custom-checks";
import { AssetHooks } from "./asset-hooks";
import { MultiValueInput } from "./multi-value-input";
import { SchemaSyncDialog } from "./schema-sync-dialog";
import { AssetDependencyPicker } from "./asset-dependency-picker";

/**
 * Guided metadata cards for the app asset editor (§13–14 of the asset
 * editing concept). Renders focused, editable sections beside the SQL editor so
 * users edit asset intent without touching raw YAML; every edit flows through
 * the asset API, and the workspace SSE stream refreshes the asset prop.
 */
export type QualityCheckFocus = FailedQualityCheck & { token: number };

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
  const [localFocus, setLocalFocus] = useState<QualityCheckFocus | null>(null);
  useEffect(() => setLocalFocus(null), [focusedCheck?.token]);
  const activeFocus = localFocus ?? focusedCheck;
  return (
    <ScrollArea className="min-h-0 w-full flex-1">
      <div className="divide-y px-3">
        <IdentityCard asset={asset} pipelineId={pipelineId} />
        <MaterializationCard asset={asset} pipelineId={pipelineId} />
        {isSqlAssetType(asset.type) ? (
          <GuidedCard title="SQL hooks">
            <AssetHooks asset={asset} />
          </GuidedCard>
        ) : null}
        <DependenciesCard asset={asset} onGoToAsset={onGoToAsset} />
        {supportsColumns ? <ColumnsCard asset={asset} /> : null}
        {supportsColumns ? (
          <QualityChecksCard
            asset={asset}
            quality={quality}
            focusedCheck={activeFocus}
            onFocusCheck={(check) => setLocalFocus({ ...check, token: Date.now() })}
          />
        ) : null}
      </div>
    </ScrollArea>
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

function IdentityCard({ asset, pipelineId }: { asset: WebAsset; pipelineId: string }) {
  const fieldIdPrefix = `${useId()}-identity`;
  const fieldId = (name: string) => `${fieldIdPrefix}-${name}`;
  const workspace = useAtomValue(workspaceAtom);
  const uri = asset.uri?.trim() ?? "";
  const [producerIdentityOpen, setProducerIdentityOpen] = useState(false);
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
  useEffect(() => {
    if (uriConflict) setProducerIdentityOpen(true);
  }, [uriConflict]);

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
        <Collapsible
          open={producerIdentityOpen}
          onOpenChange={setProducerIdentityOpen}
          className="rounded-md border"
        >
          <CollapsibleTrigger asChild>
            <Button
              type="button"
              variant="ghost"
              className="group h-auto w-full justify-between gap-2 px-3 py-2 text-xs font-normal"
            >
              <span>Producer identity</span>
              <span className="flex min-w-0 items-center gap-2 text-muted-foreground">
                {uri ? <span className="max-w-44 truncate font-monaco">{uri}</span> : null}
                <ChevronDown className="size-3.5 shrink-0 transition-transform group-data-[state=open]:rotate-180" />
              </span>
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent className="border-t p-3">
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
          </CollapsibleContent>
        </Collapsible>
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

function MaterializationCard({ asset, pipelineId }: { asset: WebAsset; pipelineId: string }) {
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

function DependenciesCard({
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

function ColumnsCard({ asset }: { asset: WebAsset }) {
  const schemaSourceIdPrefix = `${useId()}-schema-source`;
  const manualColumnInputId = `${schemaSourceIdPrefix}-manual-column`;
  const environment = useAtomValue(selectedEnvironmentAtom);
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

      {columns.length === 0 ? (
        <p className="text-[11px] text-muted-foreground">
          No columns. Add one manually or sync the schema from an available source.
        </p>
      ) : (
        <div className="divide-y rounded-md border">
          {columns.map((column) => (
            <ColumnRow
              key={column.name}
              column={column}
              status={columnStatus(column.name, provenance)}
              onCommitType={(type) => commitType(column, type)}
              onCommitDescription={(description) => setDescription(column.name, description)}
              onTogglePrimaryKey={() => togglePrimaryKey(column)}
              showMergeFields={isSQLMerge}
              onToggleUpdateOnMerge={() => toggleUpdateOnMerge(column)}
              onCommitMergeSQL={(mergeSQL) => commitMergeSQL(column, mergeSQL)}
              onDrop={() => dropColumn(column.name)}
            />
          ))}
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

function QualityChecksCard({
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
    element.scrollIntoView({ behavior: "smooth", block: "center" });
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
  column,
  status,
  onCommitType,
  onCommitDescription,
  onTogglePrimaryKey,
  showMergeFields,
  onToggleUpdateOnMerge,
  onCommitMergeSQL,
  onDrop,
}: {
  column: WebColumn;
  status: ReturnType<typeof columnStatus>;
  onCommitType: (type: string) => void;
  onCommitDescription: (description: string) => void;
  onTogglePrimaryKey: () => void;
  showMergeFields: boolean;
  onToggleUpdateOnMerge: () => void;
  onCommitMergeSQL: (mergeSQL: string) => void;
  onDrop: () => void;
}) {
  return (
    <div className="group px-2.5 py-2">
      <div className="flex items-center gap-1.5">
        <span className="min-w-0 flex-1 truncate font-monaco text-xs">{column.name}</span>
        <ColumnStatusBadge status={status} primaryKey={column.primary_key} />
        <Button
          variant="ghost"
          size="xs"
          className={cn(
            "size-6 shrink-0 p-0",
            column.primary_key
              ? "text-amber-600 dark:text-amber-400"
              : "text-muted-foreground opacity-0 group-hover:opacity-100 focus-visible:opacity-100",
          )}
          title={column.primary_key ? "Unset primary key" : "Set as primary key"}
          aria-label={`${column.primary_key ? "Unset" : "Set"} ${column.name} as primary key`}
          onClick={onTogglePrimaryKey}
        >
          <KeyRound className="size-3" />
        </Button>
        <Button
          variant="ghost"
          size="xs"
          className="size-6 shrink-0 p-0 text-muted-foreground opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
          title="Remove column"
          aria-label={`Remove ${column.name}`}
          onClick={onDrop}
        >
          <Trash2 className="size-3" />
        </Button>
      </div>
      <div className="mt-1 flex items-center gap-1.5">
        <CommitInput
          mono
          value={column.type ?? ""}
          placeholder="type"
          onCommit={onCommitType}
          className="h-7 w-28 shrink-0"
        />
        <CommitInput
          value={column.description ?? ""}
          placeholder="describe this column"
          onCommit={onCommitDescription}
          className="h-7 flex-1"
        />
      </div>
      {showMergeFields ? (
        <div className="mt-1.5 flex min-w-0 items-center gap-1.5">
          <Button
            variant={column.update_on_merge ? "secondary" : "outline"}
            size="xs"
            title="Update this column when a primary-key match is found"
            aria-label={`${column.update_on_merge ? "Do not update" : "Update"} ${column.name} on merge`}
            aria-pressed={Boolean(column.update_on_merge)}
            onClick={onToggleUpdateOnMerge}
          >
            <RefreshCw data-icon="inline-start" />
            Update on merge
          </Button>
          <CommitInput
            mono
            value={column.merge_sql ?? ""}
            placeholder="merge SQL (optional)"
            onCommit={onCommitMergeSQL}
            className="h-7 min-w-0 flex-1"
          />
        </div>
      ) : null}
    </div>
  );
}

function ColumnStatusBadge({
  status,
  primaryKey,
}: {
  status: ReturnType<typeof columnStatus>;
  primaryKey?: boolean;
}) {
  const styles: Record<string, string> = {
    inferred: "bg-muted text-muted-foreground",
    manual: "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300",
    "type-owned": "bg-purple-100 text-purple-700 dark:bg-purple-950 dark:text-purple-300",
    "table-inferred": "bg-secondary text-secondary-foreground",
    "live-inferred": "bg-secondary text-secondary-foreground",
  };
  const labels: Record<string, string> = {
    inferred: "SQL inferred",
    manual: "manual",
    "type-owned": "type owned",
    "table-inferred": "table inferred",
    "live-inferred": "live inferred",
  };
  return (
    <span className="flex items-center gap-1">
      {primaryKey ? (
        <span className="rounded bg-amber-100 px-1 text-[10px] font-medium text-amber-700 dark:bg-amber-950 dark:text-amber-300">
          pk
        </span>
      ) : null}
      <span className={cn("rounded px-1 text-[10px]", styles[status] ?? styles.inferred)}>
        {labels[status] ?? status}
      </span>
    </span>
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
  id,
  value,
  placeholder,
  onCommit,
  mono,
  className,
  ariaInvalid,
  ariaDescribedBy,
}: {
  id?: string;
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
