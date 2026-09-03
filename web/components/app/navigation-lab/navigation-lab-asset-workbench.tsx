import {
  ArrowRight,
  BookOpen,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  ChevronUp,
  CircleDot,
  Code2,
  Database,
  Plus,
  RefreshCw,
  Search,
  ShieldCheck,
  Sliders,
  Table2,
  TerminalSquare,
  TriangleAlert,
  X,
  type LucideIcon,
} from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";

import { kindMeta } from "@/components/app/app-data";
import type { AppLineageCanvasAsset } from "@/components/app/lineage-canvas";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";

import type { SettingsView } from "./navigation-lab-data";

export type AssetResultTab = "inspect" | "render" | "materialize" | "query" | "typecheck";
export type SpecialDocumentTab = "adhoc" | "notebook";

export function NavigationLabDocumentTabs({
  assets,
  selectedAssetId,
  openAssetIds,
  openSpecialTabs,
  activeDocument,
  onAssetSelect,
  onCloseAsset,
  onSpecialSelect,
  onCloseSpecial,
}: {
  assets: AppLineageCanvasAsset[];
  selectedAssetId: string;
  openAssetIds: string[];
  openSpecialTabs: SpecialDocumentTab[];
  activeDocument: "asset" | SpecialDocumentTab;
  onAssetSelect: (assetId: string) => void;
  onCloseAsset: (assetId: string) => void;
  onSpecialSelect: (tab: SpecialDocumentTab) => void;
  onCloseSpecial: (tab: SpecialDocumentTab) => void;
}) {
  const openAssets = openAssetIds
    .map((assetId) => assets.find((asset) => asset.id === assetId))
    .filter((asset): asset is AppLineageCanvasAsset => Boolean(asset));

  return (
    <div
      role="tablist"
      aria-label="Open workbench documents"
      className="no-scrollbar flex min-w-0 flex-1 items-center gap-1 overflow-x-auto py-1"
    >
      {openAssets.map((asset) => {
        const active = activeDocument === "asset" && asset.id === selectedAssetId;
        const Icon = kindMeta[asset.kind].icon;
        return (
          <DocumentTab
            key={asset.id}
            active={active}
            icon={Icon}
            label={assetFileName(asset, false)}
            status={asset.status === "overdue" ? "stale" : undefined}
            onSelect={() => onAssetSelect(asset.id)}
            onClose={() => onCloseAsset(asset.id)}
          />
        );
      })}
      {openSpecialTabs.map((tab) => (
        <DocumentTab
          key={tab}
          active={activeDocument === tab}
          icon={tab === "adhoc" ? TerminalSquare : BookOpen}
          label={tab === "adhoc" ? "Ad-hoc query" : "Cohort explorer"}
          onSelect={() => onSpecialSelect(tab)}
          onClose={() => onCloseSpecial(tab)}
        />
      ))}
    </div>
  );
}

function DocumentTab({
  active,
  icon: Icon,
  label,
  status,
  onSelect,
  onClose,
}: {
  active: boolean;
  icon: LucideIcon;
  label: string;
  status?: "stale";
  onSelect: () => void;
  onClose: () => void;
}) {
  const tabRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!active) return;
    tabRef.current?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [active]);

  return (
    <div
      ref={tabRef}
      className={cn(
        "group flex h-8 min-w-28 max-w-44 shrink-0 items-center rounded-lg border text-xs transition-colors",
        active
          ? "border-primary/30 bg-primary/10 text-foreground shadow-sm"
          : "border-transparent bg-muted/50 text-muted-foreground hover:border-border hover:bg-muted",
      )}
    >
      <button
        type="button"
        role="tab"
        aria-selected={active}
        className="flex min-w-0 flex-1 items-center gap-1.5 pl-2 text-left"
        onClick={onSelect}
      >
        <Icon className={cn("size-3.5 shrink-0", active && "text-primary")} />
        <span className="truncate font-mono">{label}</span>
        {status === "stale" ? (
          <span
            className="size-1.5 shrink-0 rounded-full bg-amber-500"
            aria-label="Asset is stale"
          />
        ) : null}
      </button>
      <button
        type="button"
        className="mx-1 flex size-5 shrink-0 items-center justify-center rounded-md opacity-50 hover:bg-background/80 hover:opacity-100 focus:opacity-100"
        aria-label={`Close ${label}`}
        onClick={onClose}
      >
        <X className="size-3" />
      </button>
    </div>
  );
}

type AssetEditorProps = {
  assets: AppLineageCanvasAsset[];
  selectedAssetId: string;
  onMessage: (message: string) => void;
};

export function NavigationLabAssetEditor({ assets, selectedAssetId, onMessage }: AssetEditorProps) {
  const selectedAsset = assets.find((asset) => asset.id === selectedAssetId) ?? assets[0];

  if (!selectedAsset) return null;

  return (
    <section className="flex min-h-0 min-w-0 flex-1 flex-col bg-background">
      <div className="min-h-0 flex-1">
        {usesCodeEditor(selectedAsset.kind) ? (
          <CodeAssetEditor asset={selectedAsset} onMessage={onMessage} />
        ) : (
          <StructuredAssetEditor asset={selectedAsset} onMessage={onMessage} />
        )}
      </div>
    </section>
  );
}

function usesCodeEditor(kind: AppLineageCanvasAsset["kind"]) {
  return kind === "sql" || kind === "python" || kind === "source";
}

function CodeAssetEditor({
  asset,
  onMessage,
}: {
  asset: AppLineageCanvasAsset;
  onMessage: (message: string) => void;
}) {
  const lines = assetSource(asset).split("\n");
  return (
    <div className="relative flex h-full min-h-0 flex-col bg-zinc-950 text-zinc-100">
      <ScrollArea className="min-h-0 flex-1" showHorizontalScrollBar>
        <div className="min-w-max py-3 font-mono text-xs leading-6">
          {lines.map((line, index) => (
            <div key={`${index}-${line}`} className="flex min-h-6 hover:bg-zinc-900/70">
              <span className="w-12 shrink-0 select-none border-r border-zinc-800 pr-3 text-right text-zinc-600">
                {index + 1}
              </span>
              <code className="whitespace-pre px-4">{line || " "}</code>
            </div>
          ))}
        </div>
      </ScrollArea>
      <div className="flex h-7 shrink-0 items-center border-t border-zinc-800 px-3 text-[10px] text-zinc-400">
        <span>{asset.kind === "source" ? "YAML" : asset.kind.toUpperCase()}</span>
        <span className="mx-2">·</span>
        <span>{asset.kind === "source" ? "Schema valid" : "No type errors"}</span>
        <span className="ml-auto">Ln 18, Col 3 · Spaces: 2</span>
      </div>
      {asset.kind !== "source" ? (
        <Button
          className="absolute bottom-9 right-2 shadow-md"
          variant="secondary"
          size="xs"
          onClick={() => onMessage(`${assetFileName(asset, false)} formatted`)}
        >
          Format
        </Button>
      ) : null}
    </div>
  );
}

function StructuredAssetEditor({
  asset,
  onMessage,
}: {
  asset: AppLineageCanvasAsset;
  onMessage: (message: string) => void;
}) {
  const fields = structuredFields(asset);
  return (
    <ScrollArea className="h-full bg-muted/15" showHorizontalScrollBar={false}>
      <div className="mx-auto max-w-3xl px-4 py-5">
        <div className="mb-5 flex items-start gap-3 border-b pb-4">
          <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
            {(() => {
              const Icon = kindMeta[asset.kind].icon;
              return <Icon className="size-4" />;
            })()}
          </span>
          <div>
            <p className="text-sm font-semibold">{kindMeta[asset.kind].label} parameters</p>
            <p className="text-xs text-muted-foreground">
              Structured fields update the underlying {kindMeta[asset.kind].ext} file.
            </p>
          </div>
        </div>
        <div className="grid gap-x-4 gap-y-3 sm:grid-cols-2">
          {fields.map((field) => (
            <label key={field.label} className={cn("space-y-1.5", field.wide && "sm:col-span-2")}>
              <span className="text-xs font-medium">{field.label}</span>
              <Input defaultValue={field.value} className="font-mono text-xs" />
              {field.hint ? (
                <span className="block text-[10px] text-muted-foreground">{field.hint}</span>
              ) : null}
            </label>
          ))}
        </div>
        <div className="mt-5 flex items-center border-t pt-3">
          <span className="text-[10px] text-muted-foreground">
            Changes are saved to {assetFileName(asset, false)}.
          </span>
          <Button
            className="ml-auto"
            size="sm"
            onClick={() => onMessage(`${kindMeta[asset.kind].label} parameters saved`)}
          >
            Save parameters
          </Button>
        </div>
      </div>
    </ScrollArea>
  );
}

function structuredFields(
  asset: AppLineageCanvasAsset,
): Array<{ label: string; value: string; wide?: boolean; hint?: string }> {
  if (asset.kind === "seed") {
    return [
      { label: "Input file", value: "data/regions.csv", wide: true },
      { label: "Format", value: "CSV" },
      { label: "Delimiter", value: "," },
      { label: "Header row", value: "Enabled" },
      { label: "Target table", value: asset.name },
    ];
  }
  if (asset.kind === "api") {
    return [
      { label: "Request URL", value: "https://api.example.com/v1/accounts", wide: true },
      { label: "Method", value: "POST" },
      { label: "Records path", value: "$.data.accounts" },
      { label: "Request body", value: '{"active": true, "limit": 100}', wide: true },
    ];
  }
  if (asset.kind === "load") {
    return [
      { label: "Source connection", value: "postgres-production" },
      { label: "Source stream", value: "public.subscription_events" },
      { label: "Target connection", value: "analytics-warehouse" },
      { label: "Target table", value: asset.name },
      {
        label: "Mode",
        value: "incremental",
        hint: "Uses the saved cursor after the initial full snapshot.",
      },
    ];
  }
  if (asset.kind === "sensor") {
    return [
      { label: "Connection", value: "postgres-production" },
      { label: "Check interval", value: "30s" },
      {
        label: "Condition query",
        value: "select max(updated_at) > {{ start_datetime }}",
        wide: true,
      },
    ];
  }
  return [
    { label: "Asset under test", value: asset.name, wide: true },
    { label: "Expected rows", value: "> 0" },
    { label: "Blocking", value: "Enabled" },
  ];
}

export function NavigationLabAssetResults({
  activeTab,
  collapsed,
  onTabChange,
  onToggleCollapse,
}: {
  activeTab: AssetResultTab;
  collapsed: boolean;
  onTabChange: (tab: AssetResultTab) => void;
  onToggleCollapse: () => void;
}) {
  const tabs: Array<{ id: AssetResultTab; label: string; icon: LucideIcon }> = [
    { id: "inspect", label: "Inspect", icon: Search },
    { id: "render", label: "Render", icon: Code2 },
    { id: "materialize", label: "Materialize", icon: TerminalSquare },
    { id: "query", label: "Query", icon: Table2 },
    { id: "typecheck", label: "Type check", icon: ShieldCheck },
  ];

  return (
    <section
      className={cn(
        "flex shrink-0 flex-col overflow-hidden rounded-xl border bg-background shadow-sm transition-[height]",
        collapsed ? "h-9" : "h-48",
      )}
    >
      <div className="flex h-9 shrink-0 items-center overflow-x-auto border-b px-1">
        {tabs.map((tab) => {
          const Icon = tab.icon;
          return (
            <button
              key={tab.id}
              type="button"
              className={cn(
                "flex h-8 shrink-0 items-center gap-1.5 border-b-2 px-2 text-[11px] text-muted-foreground",
                activeTab === tab.id && "border-primary text-foreground",
              )}
              onClick={() => {
                onTabChange(tab.id);
                if (collapsed) onToggleCollapse();
              }}
            >
              <Icon className="size-3.5" />
              {tab.label}
              {tab.id === "typecheck" ? (
                <span className="size-1.5 rounded-full bg-amber-500" aria-label="One warning" />
              ) : null}
            </button>
          );
        })}
        <Button
          variant="ghost"
          size="icon-xs"
          className="ml-auto shrink-0"
          aria-label={collapsed ? "Expand results" : "Collapse results"}
          onClick={onToggleCollapse}
        >
          {collapsed ? <ChevronUp /> : <ChevronDown />}
        </Button>
      </div>
      {!collapsed ? <ResultContent tab={activeTab} /> : null}
    </section>
  );
}

function ResultContent({ tab }: { tab: AssetResultTab }) {
  if (tab === "inspect") {
    return (
      <ScrollArea className="min-h-0 flex-1">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>account_id</TableHead>
              <TableHead className="text-right">health_score</TableHead>
              <TableHead>risk_band</TableHead>
              <TableHead>calculated_at</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {[
              ["8c1f…02a", "94", "healthy", "2026-09-01 10:42"],
              ["b30a…9dd", "61", "watch", "2026-09-01 10:42"],
              ["e471…c12", "28", "high risk", "2026-09-01 10:42"],
            ].map((row) => (
              <TableRow key={row[0]}>
                {row.map((cell, index) => (
                  <TableCell
                    key={cell}
                    className={cn("font-mono text-xs", index === 1 && "text-right")}
                  >
                    {cell}
                  </TableCell>
                ))}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </ScrollArea>
    );
  }
  if (tab === "render") {
    return (
      <ScrollArea className="min-h-0 flex-1 bg-zinc-950 p-3 font-mono text-xs text-zinc-300">
        <pre>{`create or replace view analytics.customer_health as\nselect account_id, health_score, risk_band, calculated_at\nfrom __renart_rendered_query;`}</pre>
      </ScrollArea>
    );
  }
  if (tab === "materialize") {
    return (
      <ScrollArea className="min-h-0 flex-1 bg-zinc-950 p-3 font-mono text-xs leading-5 text-zinc-300">
        <p className="text-emerald-400">✓ Saved source synchronized</p>
        <p>→ Materializing analytics.customer_health on duckdb-default</p>
        <p>→ 126,309 rows written in 842 ms</p>
        <p className="text-emerald-400">✓ Schema matches 4 declared columns</p>
      </ScrollArea>
    );
  }
  if (tab === "query") {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center text-xs text-muted-foreground">
        Ad-hoc query output appears here without changing the asset schema.
      </div>
    );
  }
  return (
    <div className="min-h-0 flex-1 overflow-auto divide-y">
      <div className="flex items-start gap-2 px-3 py-2 text-xs">
        <CheckCircle2 className="mt-0.5 size-3.5 text-emerald-500" />
        <div>
          <p className="font-medium">Declared output matches the SQL projection</p>
          <p className="text-[10px] text-muted-foreground">
            4 columns checked across 3 dependencies
          </p>
        </div>
      </div>
      <div className="flex items-start gap-2 px-3 py-2 text-xs">
        <TriangleAlert className="mt-0.5 size-3.5 text-amber-500" />
        <div>
          <p className="font-medium">Upstream raw.accounts is a remote source</p>
          <p className="text-[10px] text-muted-foreground">Metadata was observed 2 minutes ago.</p>
        </div>
      </div>
    </div>
  );
}

export function NavigationLabAssetInspector({
  asset,
  assets,
  onAssetSelect,
  onOpenSettings,
  onMessage,
}: {
  asset: AppLineageCanvasAsset;
  assets: AppLineageCanvasAsset[];
  onAssetSelect: (assetId: string) => void;
  onOpenSettings: (view: SettingsView) => void;
  onMessage: (message: string) => void;
}) {
  const dependencies = (asset.upstreams ?? [])
    .map((assetId) => assets.find((candidate) => candidate.id === assetId))
    .filter((candidate): candidate is AppLineageCanvasAsset => Boolean(candidate));
  const columns = assetColumns(asset);
  const nameParts = asset.name.split(".");
  const title = nameParts.pop() ?? asset.name;
  const namespace = nameParts.join(".");
  const [activeTab, setActiveTab] = useState("general");

  return (
    <aside
      data-testid="asset-inspector"
      className="hidden w-72 shrink-0 overflow-hidden rounded-xl border bg-background shadow-sm xl:flex xl:min-h-0 xl:flex-col"
    >
      <div className="flex shrink-0 items-center gap-2 border-b px-3 py-2">
        <Sliders className="size-4 shrink-0 text-primary" />
        <div className="min-w-0 flex-1">
          <div className="truncate font-mono text-[13px] font-medium">{title}</div>
          <p className="truncate text-[11px] text-muted-foreground">
            {[namespace, assetType(asset)].filter(Boolean).join(" · ")}
          </p>
        </div>
      </div>
      <Tabs value={activeTab} onValueChange={setActiveTab} className="min-h-0 flex-1 gap-0">
        <div className="shrink-0 border-b">
          <TabsList
            variant="line"
            aria-label="Asset metadata sections"
            className="h-9 w-full justify-start gap-0 px-1"
          >
            <InspectorTab value="general" label="General" />
            <InspectorTab value="dependencies" label="Lineage" count={dependencies.length} />
            <InspectorTab value="columns" label="Columns" count={columns.length} />
            <InspectorTab value="checks" label="Checks" count={1} />
          </TabsList>
        </div>

        <InspectorTabContent value="general">
          <InspectorSection title="Identity">
            <InspectorField label="Name">
              <Input className="h-8 font-mono text-xs" value={asset.name} readOnly />
            </InspectorField>
            <InspectorField label="Type" description="Managed by the asset kind and connection.">
              <Input className="h-8 font-mono text-xs" value={assetType(asset)} readOnly />
            </InspectorField>
            <InspectorField
              label="Connection"
              description="Only connections supported for this role in the selected environment are shown."
            >
              <button
                type="button"
                className="flex h-8 w-full items-center gap-2 rounded-md border bg-background px-2 text-left text-xs hover:bg-muted/50"
                onClick={() => onOpenSettings("connections")}
              >
                <Database className="size-3.5 text-blue-500" />
                <span className="min-w-0 flex-1 truncate font-medium">
                  {assetConnection(asset)}
                </span>
                <ArrowRight className="size-3 text-muted-foreground" />
              </button>
            </InspectorField>
            <InspectorField label="Owner">
              <Input className="h-8 text-xs" placeholder="team@company.com" />
            </InspectorField>
            <InspectorField label="Description">
              <Input className="h-8 text-xs" placeholder="What this asset produces" />
            </InspectorField>
            <InspectorField label="Tags">
              <Input className="h-8 text-xs" placeholder="Add tag" />
            </InspectorField>
            <InspectorField
              label="URI"
              description="Explicit producer identity for dependencies from sibling pipelines."
            >
              <Input
                className="h-8 font-mono text-[10px]"
                value={`renart://growth/revenue-model/${asset.name}`}
                readOnly
              />
            </InspectorField>
          </InspectorSection>

          {asset.kind !== "source" ? (
            <InspectorSection title="Materialization">
              <InspectorField label="Write behavior">
                <button
                  type="button"
                  className="flex h-8 w-full items-center rounded-md border bg-background px-2 text-left text-xs hover:bg-muted/50"
                  onClick={() => onMessage("Materialization options opened")}
                >
                  <span className="flex-1">{assetMaterialization(asset)}</span>
                  <ChevronDown className="size-3.5 text-muted-foreground" />
                </button>
              </InspectorField>
            </InspectorSection>
          ) : null}

          {asset.kind === "sql" ? (
            <InspectorSection title="SQL hooks">
              <div className="flex items-center gap-2 rounded-md border px-2 py-2 text-[11px] text-muted-foreground">
                <CircleDot className="size-3.5 shrink-0" />
                <span className="min-w-0 flex-1">No pre- or post-hooks configured</span>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  aria-label="Add SQL hook"
                  onClick={() => onMessage("SQL hook editor opened")}
                >
                  <Plus />
                </Button>
              </div>
            </InspectorSection>
          ) : null}
        </InspectorTabContent>

        <InspectorTabContent value="dependencies">
          <InspectorSection title="Dependencies">
            {dependencies.length ? (
              <div className="space-y-1.5">
                <p className="text-[10px] font-medium uppercase tracking-wide text-muted-foreground/70">
                  Inferred from SQL
                </p>
                <div className="divide-y overflow-hidden rounded-md border">
                  {dependencies.map((dependency) => {
                    const Icon = kindMeta[dependency.kind].icon;
                    return (
                      <button
                        key={dependency.id}
                        type="button"
                        className="flex w-full items-center gap-2 px-2 py-2 text-left text-xs hover:bg-muted/50"
                        onClick={() => onAssetSelect(dependency.id)}
                      >
                        <Icon className="size-3.5 text-primary" />
                        <span className="min-w-0 flex-1">
                          <span className="block truncate font-mono text-[11px]">
                            {dependency.name}
                          </span>
                          <span className="block text-[9px] text-muted-foreground">
                            revenue-model
                          </span>
                        </span>
                        <ArrowRight className="size-3 text-muted-foreground" />
                      </button>
                    );
                  })}
                </div>
              </div>
            ) : (
              <p className="text-[11px] text-muted-foreground">No dependencies yet.</p>
            )}
            <div className="flex gap-1.5">
              <Input className="h-8 text-xs" placeholder="Add dependency (asset name)" />
              <Button
                variant="outline"
                size="icon-sm"
                aria-label="Add dependency"
                onClick={() => onMessage("Dependency picker opened")}
              >
                <Plus />
              </Button>
            </div>
          </InspectorSection>
        </InspectorTabContent>

        <InspectorTabContent value="columns">
          <InspectorSection
            title="Columns"
            action={
              <Button
                variant="outline"
                size="xs"
                onClick={() => onMessage("Schema derivation opened")}
              >
                <RefreshCw data-icon="inline-start" /> Sync schema
              </Button>
            }
          >
            <p className="text-[11px] text-muted-foreground">
              Select a column to edit its type, description, and behavior.
            </p>
            <div className="divide-y overflow-hidden rounded-md border">
              {columns.map(([name, type], index) => (
                <button
                  key={name}
                  type="button"
                  className="flex w-full items-center gap-2 px-2 py-2 text-left hover:bg-muted/50"
                  onClick={() => onMessage(`${name} column editor opened`)}
                >
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-1.5">
                      <span className="truncate font-mono text-[11px]">{name}</span>
                      {index === 0 ? (
                        <Badge variant="outline" size="xs">
                          Primary key
                        </Badge>
                      ) : null}
                    </span>
                    <span className="block text-[9px] text-muted-foreground">
                      Inferred from SQL{index < 3 ? ` · ${index + 1} downstream` : ""}
                    </span>
                  </span>
                  <span className="text-[9px] text-muted-foreground">{type.toLowerCase()}</span>
                  <ChevronRight className="size-3 text-muted-foreground" />
                </button>
              ))}
            </div>
            <InspectorField label="Add column">
              <div className="flex gap-1.5">
                <Input className="h-8 text-xs" placeholder="Column name" />
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => onMessage("Empty column row added")}
                >
                  <Plus data-icon="inline-start" /> Add
                </Button>
              </div>
            </InspectorField>
          </InspectorSection>
        </InspectorTabContent>

        <InspectorTabContent value="checks">
          <InspectorSection title="Quality checks">
            <div className="space-y-1.5">
              <p className="text-[11px] font-medium">Custom SQL checks</p>
              <div className="flex gap-1.5">
                <Input className="h-8 font-mono text-[10px]" placeholder="Boolean SQL assertion" />
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => onMessage("Custom quality check added")}
                >
                  Add
                </Button>
              </div>
              <p className="text-[10px] text-muted-foreground">
                Run a SQL assertion after this asset is materialized.
              </p>
            </div>
            <div className="space-y-1.5 border-t pt-3">
              <p className="text-[11px] font-medium">Column checks</p>
              <div className="flex gap-1.5">
                <button
                  type="button"
                  className="flex h-8 min-w-0 flex-1 items-center rounded-md border px-2 text-left text-xs"
                >
                  <ShieldCheck className="mr-1.5 size-3.5 text-emerald-500" />
                  <span className="truncate">account_id</span>
                </button>
                <button
                  type="button"
                  className="flex h-8 min-w-0 flex-1 items-center rounded-md border px-2 text-left font-mono text-[10px]"
                >
                  <span className="truncate">not_null</span>
                  <ChevronDown className="ml-auto size-3 text-muted-foreground" />
                </button>
              </div>
              <Button
                variant="outline"
                size="xs"
                onClick={() => onMessage("Column quality check added")}
              >
                <Plus data-icon="inline-start" /> Add check
              </Button>
            </div>
          </InspectorSection>
        </InspectorTabContent>
      </Tabs>
    </aside>
  );
}

function InspectorTab({ value, label, count }: { value: string; label: string; count?: number }) {
  return (
    <TabsTrigger value={value} className="h-8 min-w-0 flex-1 gap-0.5 px-1 text-[10px]">
      <span className="truncate">{label}</span>
      {count !== undefined ? (
        <span className="rounded bg-muted px-1 font-mono text-[9px] text-muted-foreground">
          {count}
        </span>
      ) : null}
    </TabsTrigger>
  );
}

function InspectorTabContent({ value, children }: { value: string; children: ReactNode }) {
  return (
    <TabsContent value={value} className="min-h-0 overflow-hidden">
      <ScrollArea className="h-full min-h-0" showHorizontalScrollBar={false}>
        <div className="divide-y px-3">{children}</div>
      </ScrollArea>
    </TabsContent>
  );
}

function InspectorSection({
  title,
  action,
  children,
}: {
  title: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="flex flex-col gap-2.5 py-4">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
          {title}
        </h3>
        {action}
      </div>
      {children}
    </section>
  );
}

function InspectorField({
  label,
  description,
  children,
}: {
  label: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <p className="text-[11px] font-medium">{label}</p>
      {children}
      {description ? <p className="text-[10px] text-muted-foreground">{description}</p> : null}
    </div>
  );
}

function assetFileName(asset: AppLineageCanvasAsset, includePath: boolean) {
  const baseName = `${asset.displayName ?? asset.name.split(".").at(-1) ?? asset.name}${kindMeta[asset.kind].ext}`;
  return includePath ? `assets/${asset.group}/${baseName}` : baseName;
}

function assetConnection(asset: AppLineageCanvasAsset) {
  if (asset.kind === "source") return "postgres-production";
  if (asset.kind === "python") return "Python runtime";
  if (asset.kind === "load") return "postgres-production → analytics-warehouse";
  return "duckdb-default";
}

function assetType(asset: AppLineageCanvasAsset) {
  if (asset.kind === "source") return "postgres.source";
  if (asset.kind === "python") return "python";
  if (asset.kind === "load") return "postgres.load";
  return "duckdb.sql";
}

function assetMaterialization(asset: AppLineageCanvasAsset) {
  if (asset.kind === "python" || asset.kind === "load") return "Table (replace)";
  return "View";
}

function assetColumns(asset: AppLineageCanvasAsset): Array<[string, string]> {
  if (asset.kind === "source") {
    return [
      ["account_id", "UUID"],
      ["company_name", "VARCHAR"],
      ["plan", "VARCHAR"],
      ["created_at", "TIMESTAMPTZ"],
    ];
  }
  if (asset.kind === "python") {
    return [
      ["account_id", "UUID"],
      ["subscription_state", "VARCHAR"],
      ["risk_score", "DOUBLE"],
      ["updated_at", "TIMESTAMP"],
    ];
  }
  return [
    ["account_id", "UUID"],
    ["health_score", "DOUBLE"],
    ["risk_band", "VARCHAR"],
    ["calculated_at", "TIMESTAMP"],
  ];
}

function assetSource(asset: AppLineageCanvasAsset) {
  if (asset.kind === "source") {
    return `name: ${asset.name}
type: postgres.source
connection: postgres-production
table: public.accounts
description: Existing customer accounts

columns:
  - name: account_id
    type: uuid
    primary_key: true
    checks:
      - name: not_null
  - name: company_name
    type: varchar
  - name: plan
    type: varchar
  - name: created_at
    type: timestamptz`;
  }
  if (asset.kind === "python") {
    return `""" @bruin
name: ${asset.name}
type: python
depends:
  - raw.accounts
columns:
  - name: account_id
    type: uuid
  - name: risk_score
    type: double
@bruin """

import pandas as pd

def materialize():
    accounts = query("select * from raw.accounts", connection="duckdb-default")
    accounts["risk_score"] = accounts.apply(score_subscription, axis=1)
    return accounts`;
  }
  return `/* @bruin
name: ${asset.name}
type: duckdb.sql
materialization:
  type: view
depends:
  - staging.accounts
  - staging.subscriptions
columns:
  - name: account_id
    type: uuid
  - name: health_score
    type: double
  - name: risk_band
    type: varchar
  - name: calculated_at
    type: timestamp
@bruin */

with active_accounts as (
  select account_id, plan, created_at
  from staging.accounts
  where is_active
)

select
  a.account_id,
  100 - coalesce(s.risk_score, 0) as health_score,
  case when s.risk_score > 70 then 'high risk'
       when s.risk_score > 35 then 'watch'
       else 'healthy' end as risk_band,
  current_timestamp as calculated_at
from active_accounts a
left join staging.subscriptions s using (account_id)`;
}
