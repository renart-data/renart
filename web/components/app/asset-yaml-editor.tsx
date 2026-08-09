"use client";

import { useEffect, useMemo, useState } from "react";

import { useAtomValue } from "jotai";
import { Check, Database, KeyRound, Loader2, Plus, X } from "lucide-react";

import { cn } from "@/lib/utils";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  applyAssetTransaction,
  reconcileAssetColumns,
  refreshAssetColumnsFromDefinition,
  refreshAssetColumnsFromMaterializedOutput,
} from "@/lib/api-asset-transactions";
import { updateAsset } from "@/lib/api-assets";
import { inferAPIAsset, updateAssetColumns } from "@/lib/api-assets-columns";
import { classifyDependencies, parseAssetProvenance } from "@/lib/asset-provenance";
import {
  type AssetColumnRefreshMode,
  getAssetColumnRefreshMode,
  isSeedAssetType,
  isSensorAssetType,
  usesSQLSource,
} from "@/lib/asset-types";
import { selectedEnvironmentAtom } from "@/lib/atoms/workspace";
import { WebAsset, WebColumn } from "@/lib/types";
import { assetCreationKindForType } from "@/lib/asset-creation-profile";

import {
  COLUMN_CHECK_NAMES,
  ColumnCombobox,
  VALUE_CHECKS,
  checkValueFor,
  formatCheckValue,
  inferMaterializationTimeGranularity,
  materializationEditorState,
  materializationSelectionInput,
} from "./asset-guided-cards";
import { AssetDependencyPicker } from "./asset-dependency-picker";

/**
 * An interactive, YAML-shaped view of an asset's configurable metadata — an
 * alternative to the focused cards (§15, structured rather than a raw textarea).
 * Every value is an inline widget: text where free-form, a dropdown where the
 * value set is constrained, and YAML-list rows with an add affordance for
 * collections (tags, dependencies, checks). Labels and empty states render as
 * `#` comments. It drives the same asset API + transactions as the cards, so the
 * two stay in sync through the workspace SSE stream.
 */
export function AssetYamlEditor({ asset, pipelineId }: { asset: WebAsset; pipelineId: string }) {
  const isSql = useMemo(() => usesSQLSource(asset), [asset]);
  const columnRefreshMode = getAssetColumnRefreshMode(asset.type, asset.parameters);

  return (
    <ScrollArea className="min-h-0 flex-1 bg-background">
      <div className="font-monaco p-3 text-[13px] leading-6">
        <IdentitySection asset={asset} pipelineId={pipelineId} />
        <SemanticParametersSection asset={asset} pipelineId={pipelineId} />
        <MaterializationSection asset={asset} pipelineId={pipelineId} />
        <DependsSection asset={asset} />
        {columnRefreshMode !== "none" ? (
          <ColumnsSection asset={asset} isSql={isSql} refreshMode={columnRefreshMode} />
        ) : null}
      </div>
    </ScrollArea>
  );
}

// --- YAML primitives ---

export function Line({
  depth = 0,
  children,
  className,
}: {
  depth?: number;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex items-center gap-1.5", className)} style={{ paddingLeft: depth * 14 }}>
      {children}
    </div>
  );
}

export function Key({ children }: { children: React.ReactNode }) {
  return <span className="shrink-0 text-sky-700 dark:text-sky-300">{children}:</span>;
}

function Dash() {
  return <span className="shrink-0 text-muted-foreground">-</span>;
}

export function Comment({ depth = 0, children }: { depth?: number; children: React.ReactNode }) {
  return (
    <div className="flex" style={{ paddingLeft: depth * 14 }}>
      <span className="italic text-emerald-700/80 dark:text-emerald-400/70"># {children}</span>
    </div>
  );
}

// InlineText reads as a plain YAML value but edits on commit (blur / Enter).
export function InlineText({
  value,
  placeholder,
  ariaLabel,
  onCommit,
}: {
  value: string;
  placeholder?: string;
  ariaLabel?: string;
  onCommit: (next: string) => void;
}) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  return (
    <input
      className="font-monaco min-w-0 flex-1 rounded-sm bg-transparent px-1 text-foreground outline-none ring-offset-background placeholder:text-muted-foreground/60 hover:bg-muted/50 focus:bg-muted/60 focus:ring-1 focus:ring-ring"
      value={draft}
      placeholder={placeholder}
      aria-label={ariaLabel}
      onChange={(event) => setDraft(event.target.value)}
      onBlur={() => onCommit(draft)}
      onKeyDown={(event) => {
        if (event.key === "Enter") {
          event.currentTarget.blur();
        } else if (event.key === "Escape") {
          setDraft(value);
          event.currentTarget.blur();
        }
      }}
    />
  );
}

export function InlineSelect({
  value,
  options = [],
  groups,
  onChange,
  placeholder,
  ariaLabel,
}: {
  value: string;
  options?: { value: string; label: string }[];
  groups?: Array<{ label: string; options: { value: string; label: string }[] }>;
  onChange: (next: string) => void;
  placeholder?: string;
  ariaLabel?: string;
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger
        className="font-monaco h-6 w-auto gap-1 border-none bg-muted/40 px-1.5 text-xs hover:bg-muted/70 focus:ring-1"
        aria-label={ariaLabel}
      >
        <SelectValue placeholder={placeholder} />
      </SelectTrigger>
      <SelectContent>
        {groups ? (
          groups.map((group) => (
            <SelectGroup key={group.label}>
              <SelectLabel>{group.label}</SelectLabel>
              {group.options.map((option) => (
                <SelectItem key={option.value} value={option.value} className="font-monaco text-xs">
                  {option.label}
                </SelectItem>
              ))}
            </SelectGroup>
          ))
        ) : (
          <SelectGroup>
            {options.map((option) => (
              <SelectItem key={option.value} value={option.value} className="font-monaco text-xs">
                {option.label}
              </SelectItem>
            ))}
          </SelectGroup>
        )}
      </SelectContent>
    </Select>
  );
}

// AddItem renders a `- ` list row whose value is a small input committed on
// Enter or via the add button.
function AddItem({
  depth,
  placeholder,
  onAdd,
}: {
  depth: number;
  placeholder: string;
  onAdd: (value: string) => void;
}) {
  const [value, setValue] = useState("");
  const commit = () => {
    const trimmed = value.trim();
    if (!trimmed) return;
    onAdd(trimmed);
    setValue("");
  };
  return (
    <Line depth={depth}>
      <Dash />
      <input
        className="font-monaco min-w-0 flex-1 rounded-sm bg-transparent px-1 text-foreground outline-none placeholder:text-muted-foreground/60 hover:bg-muted/50 focus:bg-muted/60 focus:ring-1 focus:ring-ring"
        value={value}
        placeholder={placeholder}
        onChange={(event) => setValue(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter") commit();
        }}
      />
      <button
        type="button"
        className="shrink-0 rounded-sm p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-40"
        aria-label="Add"
        disabled={!value.trim()}
        onClick={commit}
      >
        <Plus className="size-3" />
      </button>
    </Line>
  );
}

export function RemoveButton({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <button
      type="button"
      className="shrink-0 rounded-sm p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
      aria-label={label}
      onClick={onClick}
    >
      <X className="size-3" />
    </button>
  );
}

// --- Sections ---

function IdentitySection({ asset, pipelineId }: { asset: WebAsset; pipelineId: string }) {
  const hasTargetConnection = Boolean(assetCreationKindForType(asset.type));
  const tags = asset.tags ?? [];

  const setTags = (next: string[]) => {
    void updateAsset(pipelineId, asset.id, { tags: next });
  };
  const setMetaDescription = (description: string) => {
    const nextMeta = { ...(asset.meta ?? {}) };
    if (description.trim()) {
      nextMeta.description = description.trim();
    } else {
      delete nextMeta.description;
    }
    void updateAsset(pipelineId, asset.id, { meta: nextMeta });
  };

  return (
    <>
      <Line>
        <Key>name</Key>
        <InlineText
          value={asset.name}
          placeholder="analytics.orders"
          onCommit={(name) => {
            if (name.trim() && name.trim() !== asset.name)
              void updateAsset(pipelineId, asset.id, { name: name.trim() });
          }}
        />
      </Line>
      <Line>
        <Key>type</Key>
        <span className="text-foreground">{asset.type}</span>
      </Line>
      <Line>
        <Key>uri</Key>
        <InlineText
          value={asset.uri ?? ""}
          placeholder="duckdb://warehouse/schema/table"
          onCommit={(uri) => {
            if (uri.trim() !== (asset.uri ?? "").trim()) {
              void applyAssetTransaction(asset.id, {
                type: "asset.uri.set",
                asset_uri: uri.trim(),
              });
            }
          }}
        />
      </Line>
      {hasTargetConnection ? (
        <Line>
          <Key>connection</Key>
          <span className="truncate text-foreground">
            {(asset.explicit_connection ?? "").trim() ||
              (asset.connection ? `auto (${asset.connection})` : "auto")}
          </span>
        </Line>
      ) : null}
      <Line>
        <Key>owner</Key>
        <InlineText
          value={asset.owner ?? ""}
          placeholder="team@company.com"
          onCommit={(owner) => {
            if (owner !== (asset.owner ?? "")) void updateAsset(pipelineId, asset.id, { owner });
          }}
        />
      </Line>
      <Line>
        <Key>description</Key>
        <InlineText
          value={asset.meta?.description ?? ""}
          placeholder="What this asset produces"
          onCommit={(description) => {
            if (description !== (asset.meta?.description ?? "")) {
              setMetaDescription(description);
            }
          }}
        />
      </Line>
      <Line>
        <Key>tags</Key>
      </Line>
      {tags.map((tag) => (
        <Line key={tag} depth={1}>
          <Dash />
          <span className="flex-1 text-foreground">{tag}</span>
          <RemoveButton
            label={`Remove tag ${tag}`}
            onClick={() => setTags(tags.filter((t) => t !== tag))}
          />
        </Line>
      ))}
      <AddItem
        depth={1}
        placeholder="add tag"
        onAdd={(tag) => {
          if (!tags.includes(tag)) setTags([...tags, tag]);
        }}
      />
    </>
  );
}

function SemanticParametersSection({ asset, pipelineId }: { asset: WebAsset; pipelineId: string }) {
  const isSeed = isSeedAssetType(asset.type);
  const isSensor = isSensorAssetType(asset.type);
  if (!isSeed && !isSensor) return null;

  const saveParameter = (key: string, value: string) => {
    const parameters = { ...(asset.parameters ?? {}) };
    if (value.trim()) {
      parameters[key] = value.trim();
    } else {
      delete parameters[key];
    }
    void updateAsset(pipelineId, asset.id, { parameters });
  };
  const sensorVariant = asset.type.split(".sensor.")[1] ?? "";

  return (
    <>
      <Line className="mt-1">
        <Key>parameters</Key>
      </Line>
      {isSeed ? (
        <>
          <Line depth={1}>
            <Key>path</Key>
            <InlineText
              value={asset.parameters?.path ?? ""}
              placeholder="./customers.csv"
              onCommit={(value) => saveParameter("path", value)}
            />
          </Line>
          <Line depth={1}>
            <Key>file_type</Key>
            <InlineSelect
              value={asset.parameters?.file_type ?? "csv"}
              options={["csv", "parquet", "json", "jsonl", "ndjson", "avro"].map((value) => ({
                value,
                label: value,
              }))}
              onChange={(value) => saveParameter("file_type", value)}
            />
          </Line>
          <Line depth={1}>
            <Key>enforce_schema</Key>
            <InlineSelect
              value={asset.parameters?.enforce_schema ?? "true"}
              options={[
                { value: "true", label: "true" },
                { value: "false", label: "false" },
              ]}
              onChange={(value) => saveParameter("enforce_schema", value)}
            />
          </Line>
        </>
      ) : (
        <>
          {sensorVariant === "query" ? (
            <Line depth={1}>
              <Key>query</Key>
              <InlineText
                value={asset.parameters?.query ?? ""}
                placeholder="select count(*) > 0 from analytics.orders"
                onCommit={(value) => saveParameter("query", value)}
              />
            </Line>
          ) : null}
          {sensorVariant === "table" ? (
            <Line depth={1}>
              <Key>table</Key>
              <InlineText
                value={asset.parameters?.table ?? ""}
                placeholder="analytics.orders"
                onCommit={(value) => saveParameter("table", value)}
              />
            </Line>
          ) : null}
          {sensorVariant === "key" ? (
            <>
              <Line depth={1}>
                <Key>bucket_name</Key>
                <InlineText
                  value={asset.parameters?.bucket_name ?? ""}
                  placeholder="raw-data"
                  onCommit={(value) => saveParameter("bucket_name", value)}
                />
              </Line>
              <Line depth={1}>
                <Key>bucket_key</Key>
                <InlineText
                  value={asset.parameters?.bucket_key ?? ""}
                  placeholder="daily/orders.csv"
                  onCommit={(value) => saveParameter("bucket_key", value)}
                />
              </Line>
            </>
          ) : null}
          <Line depth={1}>
            <Key>poke_interval</Key>
            <InlineText
              value={asset.parameters?.poke_interval ?? "30"}
              placeholder="30"
              onCommit={(value) => saveParameter("poke_interval", value)}
            />
          </Line>
          <Line depth={1}>
            <Key>timeout</Key>
            <InlineText
              value={asset.parameters?.timeout ?? "24h"}
              placeholder="24h"
              onCommit={(value) => saveParameter("timeout", value)}
            />
          </Line>
          <Comment depth={1}>manual runs check once; scheduled runs wait</Comment>
        </>
      )}
    </>
  );
}

function MaterializationSection({ asset, pipelineId }: { asset: WebAsset; pipelineId: string }) {
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
    <>
      <Line className="mt-1">
        <Key>materialization</Key>
      </Line>
      <Line depth={1}>
        <Key>type</Key>
        <InlineSelect
          value={selectedValue}
          options={options.map((option) => ({ value: option.value, label: option.label }))}
          onChange={(value) => {
            const option = options.find((item) => item.value === value);
            if (!option || option.custom) return;
            save(materializationSelectionInput(asset, option));
          }}
        />
      </Line>
      {selected.capability?.requires_incremental_key ||
      selected.capability?.supports_incremental_key ? (
        <Line depth={1}>
          <Key>incremental_key</Key>
          <ColumnCombobox
            columns={asset.columns ?? []}
            value={asset.incremental_key ?? ""}
            placeholder={
              selected.capability?.requires_incremental_key ? "loaded_at" : "updated_at (optional)"
            }
            className="h-6 min-w-40 border-none bg-muted/40 shadow-none"
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
        </Line>
      ) : null}
      {selected.capability?.requires_time_granularity ? (
        <Line depth={1}>
          <Key>time_granularity</Key>
          <InlineSelect
            value={asset.time_granularity ?? ""}
            options={[
              { value: "timestamp", label: "timestamp" },
              { value: "date", label: "date" },
            ]}
            onChange={(timeGranularity) => save({ time_granularity: timeGranularity })}
            placeholder="select date or timestamp"
          />
        </Line>
      ) : null}
      {selected.capability?.supports_partition_by ? (
        <Line depth={1}>
          <Key>partition_by</Key>
          <InlineText
            value={asset.partition_by ?? ""}
            placeholder="event_date"
            onCommit={(partitionBy) => {
              if (partitionBy !== (asset.partition_by ?? "")) save({ partition_by: partitionBy });
            }}
          />
        </Line>
      ) : null}
      {selected.capability?.supports_cluster_by ? (
        <>
          <Line depth={1}>
            <Key>cluster_by</Key>
          </Line>
          {(asset.cluster_by ?? []).map((cluster, index) => (
            <Line key={`${cluster}-${index}`} depth={2}>
              <Dash />
              <span className="flex-1 text-foreground">{cluster}</span>
              <RemoveButton
                label={`Remove cluster expression ${cluster}`}
                onClick={() =>
                  save({ cluster_by: (asset.cluster_by ?? []).filter((item) => item !== cluster) })
                }
              />
            </Line>
          ))}
          <AddItem
            depth={2}
            placeholder="add column or expression"
            onAdd={(cluster) => {
              if (!(asset.cluster_by ?? []).includes(cluster)) {
                save({ cluster_by: [...(asset.cluster_by ?? []), cluster] });
              }
            }}
          />
        </>
      ) : null}
      {selected.capability?.requires_primary_key ? (
        <Comment depth={2}>
          {primaryKeys.length === 0
            ? `${selected.value === "merge" ? "merge" : "this mode"} needs a primary_key column — set one under columns below`
            : `primary key${primaryKeys.length === 1 ? "" : "s"}: ${primaryKeys.join(", ")}`}
        </Comment>
      ) : null}
      {error ? <Comment depth={2}>{error}</Comment> : null}
    </>
  );
}

function DependsSection({ asset }: { asset: WebAsset }) {
  const { inferred, manual, ignored } = useMemo(() => classifyDependencies(asset), [asset]);
  const [addingKind, setAddingKind] = useState<"asset" | "uri">("asset");
  const [addingMode, setAddingMode] = useState<"full" | "symbolic">("full");
  const apply = (tx: Parameters<typeof applyAssetTransaction>[1]) => {
    void applyAssetTransaction(asset.id, tx);
  };
  const presentDependencies = useMemo(
    () =>
      new Set(
        [...inferred, ...manual].map(
          (dependency) => `${dependency.kind}:${dependency.value.trim().toLowerCase()}`,
        ),
      ),
    [inferred, manual],
  );
  const hasAny = inferred.length > 0 || manual.length > 0;

  return (
    <>
      <Line className="mt-1">
        <Key>depends</Key>
      </Line>
      {!hasAny ? (
        <Comment depth={1}>none yet — add a dependency below or pick from existing assets</Comment>
      ) : null}
      {inferred.length > 0 ? <Comment depth={1}>inferred from SQL</Comment> : null}
      {inferred.map((dep) => (
        <Line key={dep.key} depth={1}>
          <Dash />
          <span className="flex-1 text-foreground">{dep.name}</span>
          <button
            type="button"
            className="shrink-0 rounded-sm px-1 text-[10px] text-muted-foreground hover:bg-muted hover:text-foreground"
            onClick={() => apply({ type: "dependency.inferred.ignore", dependency_key: dep.key })}
          >
            ignore
          </button>
        </Line>
      ))}
      {manual.length > 0 ? <Comment depth={1}>manual</Comment> : null}
      {manual.map((dep) => (
        <Line key={dep.key} depth={1}>
          <Dash />
          <span className="flex-1 text-foreground">
            {dep.name}
            <span className="ml-1 text-muted-foreground">
              ({dep.kind === "uri" ? "uri, " : ""}
              {dep.mode})
            </span>
            {dep.resolvedPipelineName ? (
              <span className="ml-1 text-muted-foreground">· {dep.resolvedPipelineName}</span>
            ) : null}
          </span>
          <RemoveButton
            label={`Remove dependency ${dep.name}`}
            onClick={() => apply({ type: "dependency.manual.remove", dependency_key: dep.key })}
          />
        </Line>
      ))}
      {ignored.length > 0 ? (
        <Comment depth={1}>ignored — restore to let inference manage them again</Comment>
      ) : null}
      {ignored.map((dep) => (
        <div key={dep.key} className="flex items-center gap-1.5" style={{ paddingLeft: 14 }}>
          <span className="italic text-emerald-700/80 dark:text-emerald-400/70">
            # - {dep.value}
          </span>
          <button
            type="button"
            className="shrink-0 rounded-sm px-1 text-[10px] text-muted-foreground hover:bg-muted hover:text-foreground"
            onClick={() => apply({ type: "dependency.inferred.restore", dependency_key: dep.key })}
          >
            restore
          </button>
        </div>
      ))}
      <Line depth={1} className="text-muted-foreground">
        <span># add as</span>
        <InlineSelect
          value={addingKind}
          onChange={(value) => setAddingKind(value as "asset" | "uri")}
          options={[
            { value: "asset", label: "asset" },
            { value: "uri", label: "uri" },
          ]}
        />
        <InlineSelect
          value={addingMode}
          onChange={(value) => setAddingMode(value as "full" | "symbolic")}
          options={[
            { value: "full", label: "full" },
            { value: "symbolic", label: "symbolic" },
          ]}
        />
      </Line>
      <AddItem
        depth={1}
        placeholder={addingKind === "uri" ? "producer URI" : "same-pipeline asset name"}
        onAdd={(value) =>
          apply({
            type: "dependency.manual.add",
            dependency:
              addingKind === "uri"
                ? { uri: value, mode: addingMode }
                : { asset: value, mode: addingMode },
          })
        }
      />
      <AssetDependencyPicker
        assetId={asset.id}
        present={presentDependencies}
        mode={addingMode}
        onPick={(dependency) => apply({ type: "dependency.manual.add", dependency })}
        className="ml-[14px] mt-0.5 px-1 font-monaco text-[11px]"
      />
    </>
  );
}

function ColumnsSection({
  asset,
  isSql,
  refreshMode,
}: {
  asset: WebAsset;
  isSql: boolean;
  refreshMode: AssetColumnRefreshMode;
}) {
  const columns = asset.columns ?? [];
  const isSQLMerge = isSql && asset.materialization_strategy?.toLowerCase() === "merge";
  const apply = (tx: Parameters<typeof applyAssetTransaction>[1]) => {
    void applyAssetTransaction(asset.id, tx);
  };
  const setColumnType = (name: string, type: string) => {
    const next: WebColumn[] = columns.map((column) =>
      column.name === name ? { ...column, type } : column,
    );
    void updateAssetColumns(asset.id, next);
  };
  const setColumnPrimaryKey = (name: string, primaryKey: boolean) => {
    const next: WebColumn[] = columns.map((column) =>
      column.name === name ? { ...column, primary_key: primaryKey } : column,
    );
    void updateAssetColumns(asset.id, next);
  };
  const setColumnUpdateOnMerge = (name: string, updateOnMerge: boolean) => {
    const next: WebColumn[] = columns.map((column) =>
      column.name === name ? { ...column, update_on_merge: updateOnMerge } : column,
    );
    void updateAssetColumns(asset.id, next);
  };
  const setColumnMergeSQL = (name: string, mergeSQL: string) => {
    const next: WebColumn[] = columns.map((column) =>
      column.name === name ? { ...column, merge_sql: mergeSQL } : column,
    );
    void updateAssetColumns(asset.id, next);
  };

  const existingNames = useMemo(
    () => new Set(columns.map((column) => column.name.toLowerCase())),
    [columns],
  );
  // Columns the user has dropped/ignored — shown commented-out so they can be restored.
  const dropped = useMemo(() => {
    const present = new Set(columns.map((column) => column.name.toLowerCase()));
    return [...parseAssetProvenance(asset.meta).colDrop]
      .filter((name) => !present.has(name))
      .sort();
  }, [asset.meta, columns]);

  return (
    <>
      <Line className="mt-1">
        <Key>columns</Key>
      </Line>
      {columns.length === 0 ? (
        <Comment depth={1}>
          none yet — add one below
          {refreshMode === "materialized"
            ? " or import from the warehouse"
            : " or refresh from the definition"}
        </Comment>
      ) : null}
      {columns.map((column) => (
        <ColumnEntry
          key={column.name}
          column={column}
          onSetType={setColumnType}
          onSetPrimaryKey={setColumnPrimaryKey}
          showMergeFields={isSQLMerge}
          onSetUpdateOnMerge={setColumnUpdateOnMerge}
          onSetMergeSQL={setColumnMergeSQL}
          apply={apply}
          onRemove={() => apply({ type: "column.inferred.drop", column: column.name })}
        />
      ))}
      {dropped.length > 0 ? <Comment depth={1}>ignored — restore to bring back</Comment> : null}
      {dropped.map((name) => (
        <div key={name} className="flex items-center gap-1.5" style={{ paddingLeft: 14 }}>
          <span className="italic text-emerald-700/80 dark:text-emerald-400/70">
            # - name: {name}
          </span>
          <button
            type="button"
            className="shrink-0 rounded-sm px-1 text-[10px] text-muted-foreground hover:bg-muted hover:text-foreground"
            onClick={() => apply({ type: "column.inferred.restore", column: name })}
          >
            restore
          </button>
        </div>
      ))}
      <AddItem
        depth={1}
        placeholder="add column"
        onAdd={(name) => {
          if (!existingNames.has(name.toLowerCase()))
            apply({ type: "column.manual.add", column_def: { name } });
        }}
      />
      <RefreshColumnsButton asset={asset} refreshMode={refreshMode} />
    </>
  );
}

function RefreshColumnsButton({
  asset,
  refreshMode,
}: {
  asset: WebAsset;
  refreshMode: AssetColumnRefreshMode;
}) {
  const environment = useAtomValue(selectedEnvironmentAtom);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const isAPIAsset = refreshMode === "api";

  if (refreshMode === "materialized" && !asset.connection) {
    return (
      <Comment depth={1}>no connection set — can&apos;t import columns from the warehouse</Comment>
    );
  }

  const run = () => {
    setLoading(true);
    setError(null);
    if (isAPIAsset) {
      inferAPIAsset(asset.id)
        .then(async (sample) => {
          if (sample.columns.length === 0) {
            setError(sample.warnings[0] ?? "No columns found in the sampled response");
            return;
          }
          await reconcileAssetColumns(asset.id, sample.columns);
          if (sample.warnings.length > 0) setError(sample.warnings.join(" "));
        })
        .catch((err) =>
          setError(err instanceof Error ? err.message : "Failed to sample the API response"),
        )
        .finally(() => setLoading(false));
      return;
    }
    const refresh =
      refreshMode === "materialized"
        ? refreshAssetColumnsFromMaterializedOutput(asset, environment)
        : refreshAssetColumnsFromDefinition(asset.id);
    refresh
      .catch((err) => setError(err instanceof Error ? err.message : "Failed to import columns"))
      .finally(() => setLoading(false));
  };

  return (
    <>
      <Line depth={1}>
        <button
          type="button"
          disabled={loading}
          onClick={run}
          className="font-monaco flex items-center gap-1 rounded-sm px-1 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-50"
        >
          {loading ? <Loader2 className="size-3 animate-spin" /> : <Database className="size-3" />}
          {isAPIAsset
            ? "test response and infer columns"
            : refreshMode === "materialized"
              ? "import columns from warehouse"
              : isSeedAssetType(asset.type)
                ? "infer columns from seed file"
                : "refresh columns from definition"}
        </button>
      </Line>
      {error ? <Comment depth={1}>{error}</Comment> : null}
    </>
  );
}

function ColumnEntry({
  column,
  onSetType,
  onSetPrimaryKey,
  showMergeFields,
  onSetUpdateOnMerge,
  onSetMergeSQL,
  apply,
  onRemove,
}: {
  column: WebColumn;
  onSetType: (name: string, type: string) => void;
  onSetPrimaryKey: (name: string, primaryKey: boolean) => void;
  showMergeFields: boolean;
  onSetUpdateOnMerge: (name: string, updateOnMerge: boolean) => void;
  onSetMergeSQL: (name: string, mergeSQL: string) => void;
  apply: (tx: Parameters<typeof applyAssetTransaction>[1]) => void;
  onRemove: () => void;
}) {
  const [adding, setAdding] = useState(false);
  const [checkName, setCheckName] = useState<string>(COLUMN_CHECK_NAMES[0]);
  const [checkValue, setCheckValue] = useState("");
  const checks = column.checks ?? [];

  const addCheck = () => {
    const value = checkValueFor(checkName, checkValue);
    const check: { name: string; value?: unknown } = { name: checkName };
    if (value !== undefined) check.value = value;
    apply({ type: "column.check.add", column: column.name, check });
    setCheckValue("");
    setAdding(false);
  };

  return (
    <>
      <Line depth={1}>
        <Dash />
        <Key>name</Key>
        <span className="flex-1 text-foreground">{column.name}</span>
        <RemoveButton label={`Remove column ${column.name}`} onClick={onRemove} />
      </Line>
      <Line depth={3}>
        <Key>type</Key>
        <InlineText
          value={column.type ?? ""}
          placeholder="VARCHAR"
          onCommit={(type) => {
            if (type !== (column.type ?? "")) onSetType(column.name, type);
          }}
        />
      </Line>
      {column.primary_key ? (
        <Line depth={3}>
          <Key>primary_key</Key>
          <span className="flex-1 text-foreground">true</span>
          <RemoveButton
            label={`Unset primary key on ${column.name}`}
            onClick={() => onSetPrimaryKey(column.name, false)}
          />
        </Line>
      ) : (
        <Line depth={3}>
          <button
            type="button"
            className="font-monaco flex items-center gap-1 rounded-sm px-1 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"
            aria-label={`Set ${column.name} as primary key`}
            onClick={() => onSetPrimaryKey(column.name, true)}
          >
            <KeyRound className="size-3" />
            set as primary key…
          </button>
        </Line>
      )}
      {showMergeFields ? (
        <>
          {column.update_on_merge ? (
            <Line depth={3}>
              <Key>update_on_merge</Key>
              <span className="flex-1 text-foreground">true</span>
              <RemoveButton
                label={`Do not update ${column.name} on merge`}
                onClick={() => onSetUpdateOnMerge(column.name, false)}
              />
            </Line>
          ) : (
            <Line depth={3}>
              <button
                type="button"
                className="font-monaco flex items-center gap-1 rounded-sm px-1 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"
                aria-label={`Update ${column.name} on merge`}
                onClick={() => onSetUpdateOnMerge(column.name, true)}
              >
                update on merge…
              </button>
            </Line>
          )}
          <Line depth={3}>
            <Key>merge_sql</Key>
            <InlineText
              value={column.merge_sql ?? ""}
              placeholder="optional expression"
              onCommit={(mergeSQL) => {
                if (mergeSQL !== (column.merge_sql ?? "")) onSetMergeSQL(column.name, mergeSQL);
              }}
            />
          </Line>
        </>
      ) : null}
      <Line depth={3}>
        <Key>checks</Key>
      </Line>
      {checks.map((check, index) => (
        <Line key={`${check.name}-${index}`} depth={4}>
          <Dash />
          <span className="flex-1 text-foreground">
            {check.name}
            {formatCheckValue(check.value)}
          </span>
          <RemoveButton
            label={`Remove ${check.name} from ${column.name}`}
            onClick={() =>
              apply({
                type: "column.check.remove",
                column: column.name,
                check: { name: check.name },
              })
            }
          />
        </Line>
      ))}
      {adding ? (
        <Line depth={4}>
          <Dash />
          <InlineSelect
            value={checkName}
            options={COLUMN_CHECK_NAMES.map((name) => ({ value: name, label: name }))}
            onChange={setCheckName}
          />
          {VALUE_CHECKS.has(checkName) ? (
            <input
              autoFocus
              className="font-monaco w-24 min-w-0 rounded-sm bg-transparent px-1 text-foreground outline-none placeholder:text-muted-foreground/60 hover:bg-muted/50 focus:bg-muted/60 focus:ring-1 focus:ring-ring"
              value={checkValue}
              placeholder={
                checkName === "accepted_values"
                  ? "a, b, c"
                  : checkName === "pattern"
                    ? "regex"
                    : "number"
              }
              onChange={(event) => setCheckValue(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") addCheck();
                if (event.key === "Escape") setAdding(false);
              }}
            />
          ) : null}
          <button
            type="button"
            className="shrink-0 rounded-sm p-0.5 text-muted-foreground hover:bg-muted hover:text-foreground"
            aria-label={`Confirm check on ${column.name}`}
            onClick={addCheck}
          >
            <Check className="size-3" />
          </button>
          <RemoveButton
            label="Cancel add check"
            onClick={() => {
              setAdding(false);
              setCheckValue("");
            }}
          />
        </Line>
      ) : (
        <Line depth={4}>
          <button
            type="button"
            className="font-monaco flex items-center gap-1 rounded-sm px-1 text-[11px] text-muted-foreground hover:bg-muted hover:text-foreground"
            onClick={() => setAdding(true)}
          >
            <Plus className="size-3" />
            add check…
          </button>
        </Line>
      )}
    </>
  );
}
