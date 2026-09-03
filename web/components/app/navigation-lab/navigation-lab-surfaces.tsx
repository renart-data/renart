import {
  AlertCircle,
  ArrowLeft,
  ArrowDownToLine,
  ArrowRight,
  BookOpen,
  Boxes,
  CalendarClock,
  CheckCircle2,
  ChevronLeft,
  ChevronRight,
  CircleDashed,
  Clock3,
  Code2,
  Columns3,
  Copy,
  FileCode2,
  FileText,
  GitCommitHorizontal,
  History,
  LineChart,
  ListFilter,
  ListTree,
  LockKeyhole,
  MoreHorizontal,
  Pin,
  PinOff,
  Play,
  Plus,
  RefreshCw,
  RotateCw,
  Rocket,
  Search,
  ShieldCheck,
  Sparkles,
  Table2,
  Terminal,
  TriangleAlert,
  type LucideIcon,
} from "lucide-react";
import { type DragEvent, type ReactNode, useEffect, useState } from "react";

import { AppLineageCanvas, type AppLineageCanvasAsset } from "@/components/app/lineage-canvas";
import { StatusPill } from "@/components/app/app-primitives";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  DelimitedCard,
  DelimitedCardContent,
  DelimitedCardHeader,
  DelimitedCardTitle,
} from "@/components/ui/delimited-card";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Separator } from "@/components/ui/separator";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { cn } from "@/lib/utils";

import {
  NavigationLabAssetEditor,
  NavigationLabAssetInspector,
  NavigationLabAssetResults,
  NavigationLabDocumentTabs,
  type AssetResultTab,
  type SpecialDocumentTab,
} from "./navigation-lab-asset-workbench";
import {
  canDropAsset,
  labEnvironments,
  paletteAssets,
  type BrowserConnection,
  type BrowserTable,
  type BuildView,
  type ExploreView,
  type OperateView,
  type PaletteAsset,
  type SettingsView,
  type SettingsSection,
} from "./navigation-lab-data";

type BuildSurfaceProps = {
  assets: AppLineageCanvasAsset[];
  selectedAssetId: string;
  openAssetIds: string[];
  openSpecialTabs: SpecialDocumentTab[];
  activeDocument: "asset" | SpecialDocumentTab;
  view: BuildView;
  selectedTable?: BrowserTable;
  selectedDatabaseName?: string;
  selectedSchemaName?: string;
  selectedConnection: BrowserConnection;
  tablePinned: boolean;
  dragKind: PaletteAsset | null;
  onAssetSelect: (assetId: string) => void;
  onCloseAsset: (assetId: string) => void;
  onSpecialTabSelect: (tab: SpecialDocumentTab) => void;
  onCloseSpecialTab: (tab: SpecialDocumentTab) => void;
  onViewChange: (view: BuildView) => void;
  onDropAsset: (palette: PaletteAsset, target: "root" | "downstream" | "gate" | "test") => void;
  onDragEnd: () => void;
  onOpenSettings: (view: SettingsView) => void;
  onToggleTablePinned: () => void;
  onMessage: (message: string) => void;
};

export function BuildSurface({
  assets,
  selectedAssetId,
  openAssetIds,
  openSpecialTabs,
  activeDocument,
  view,
  selectedTable,
  selectedDatabaseName,
  selectedSchemaName,
  selectedConnection,
  tablePinned,
  dragKind,
  onAssetSelect,
  onCloseAsset,
  onSpecialTabSelect,
  onCloseSpecialTab,
  onViewChange,
  onDropAsset,
  onDragEnd,
  onOpenSettings,
  onToggleTablePinned,
  onMessage,
}: BuildSurfaceProps) {
  const selectedAsset = assets.find((asset) => asset.id === selectedAssetId) ?? assets[0];
  const [resultTab, setResultTab] = useState<AssetResultTab>("inspect");
  const [resultsCollapsed, setResultsCollapsed] = useState(false);

  const openResultTab = (tab: AssetResultTab) => {
    setResultTab(tab);
    setResultsCollapsed(false);
  };

  const documentBar = (
    <div className="flex h-11 shrink-0 items-center gap-1.5 overflow-hidden border-b bg-background px-2">
      <div className="hidden min-w-0 shrink-0 2xl:block">
        <div className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <span>growth</span>
          <span>/</span>
          <span className="truncate font-medium text-foreground">revenue-model</span>
        </div>
      </div>
      <NavigationLabDocumentTabs
        assets={assets}
        selectedAssetId={selectedAssetId}
        openAssetIds={openAssetIds}
        openSpecialTabs={openSpecialTabs}
        activeDocument={activeDocument}
        onAssetSelect={onAssetSelect}
        onCloseAsset={onCloseAsset}
        onSpecialSelect={onSpecialTabSelect}
        onCloseSpecial={onCloseSpecialTab}
      />
      {activeDocument === "asset" ? (
        <div className="hidden shrink-0 items-center rounded-lg bg-muted p-0.5 sm:flex">
          <ViewButton
            active={view === "code"}
            icon={Code2}
            label="Code"
            onClick={() => onViewChange("code")}
          />
          <ViewButton
            active={view === "split"}
            icon={Columns3}
            label="Split"
            onClick={() => onViewChange("split")}
          />
          <ViewButton
            active={view === "canvas"}
            icon={Boxes}
            label="Canvas"
            onClick={() => onViewChange("canvas")}
          />
        </div>
      ) : activeDocument === "adhoc" ? (
        <>
          <Badge className="hidden shrink-0 xl:inline-flex" variant="outline" size="xs">
            Not an asset
          </Badge>
          <Button
            className="shrink-0"
            size="sm"
            aria-label="Run query"
            onClick={() => onMessage("Ad-hoc query finished in 82 ms")}
          >
            <Play data-icon="inline-start" />
            <span className="hidden xl:inline">Run query</span>
          </Button>
        </>
      ) : (
        <>
          <Button
            className="hidden shrink-0 lg:inline-flex"
            size="sm"
            variant="outline"
            onClick={() => onMessage("Notebook AI opened")}
          >
            <Sparkles data-icon="inline-start" />
            Ask AI
          </Button>
          <Button
            className="shrink-0"
            size="sm"
            aria-label="Run all notebook cells"
            onClick={() => onMessage("Notebook run started")}
          >
            <Play data-icon="inline-start" />
            <span className="hidden xl:inline">Run all</span>
          </Button>
        </>
      )}
      <Button
        className="hidden shrink-0 lg:inline-flex"
        variant="outline"
        size="sm"
        aria-label="Deploy"
        onClick={() => onMessage("Deployment review opened")}
      >
        <Rocket data-icon="inline-start" />
        <span className="hidden lg:inline">Deploy</span>
      </Button>
      <Button
        size="sm"
        variant={activeDocument === "asset" ? "default" : "outline"}
        aria-label="Review run"
        onClick={() => onMessage("Run review opened")}
      >
        <Play data-icon="inline-start" />
        <span className="hidden sm:inline">Review run</span>
      </Button>
    </div>
  );

  const standaloneSurface =
    activeDocument === "adhoc" ? (
      <AdhocQuerySurface />
    ) : activeDocument === "notebook" ? (
      <NotebookSurface embedded onMessage={onMessage} />
    ) : view === "data" ? (
      selectedTable ? (
        <DataPreviewSurface
          table={selectedTable}
          databaseName={selectedDatabaseName}
          schemaName={selectedSchemaName}
          connection={selectedConnection}
          pinned={tablePinned}
          onTogglePinned={onToggleTablePinned}
          onViewChange={onViewChange}
          onMessage={onMessage}
        />
      ) : (
        <ConnectionDiscoverySurface connection={selectedConnection} />
      )
    ) : null;

  return (
    <div className="flex h-full min-h-0 gap-1.5 bg-muted/30 p-1.5">
      {standaloneSurface ? (
        <div className="flex min-w-0 flex-1 flex-col overflow-hidden rounded-xl border bg-background shadow-sm">
          {documentBar}
          <div className="flex min-h-0 min-w-0 flex-1 overflow-hidden">{standaloneSurface}</div>
        </div>
      ) : (
        <>
          <div className="flex min-w-0 flex-1 flex-col gap-1.5">
            <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border bg-background shadow-sm">
              {documentBar}
              <div className="relative flex min-h-0 flex-1">
                {view !== "canvas" ? (
                  <NavigationLabAssetEditor
                    assets={assets}
                    selectedAssetId={selectedAssetId}
                    onMessage={onMessage}
                  />
                ) : null}

                {view !== "code" ? (
                  <div
                    className={cn(
                      "relative min-w-0 flex-1",
                      view === "split" && "hidden border-l lg:block lg:w-1/2",
                    )}
                  >
                    <NavigationLabLineageCanvas
                      assets={assets}
                      selectedAssetId={selectedAssetId}
                      onAssetSelect={onAssetSelect}
                    />
                    {dragKind ? (
                      <RootDropOverlay
                        palette={dragKind}
                        onDrop={(palette) => onDropAsset(palette, "root")}
                        onDragEnd={onDragEnd}
                      />
                    ) : null}
                  </div>
                ) : null}
                {view !== "code" ? (
                  <div className="absolute right-1.5 top-1.5 z-30 flex items-center gap-2">
                    <Button
                      size="sm"
                      className="shadow-sm"
                      onClick={() => onMessage("Create asset dialog opened")}
                    >
                      <Plus data-icon="inline-start" />
                      New asset
                    </Button>
                  </div>
                ) : null}
              </div>
            </div>
            <NavigationLabAssetResults
              activeTab={resultTab}
              collapsed={resultsCollapsed}
              onTabChange={openResultTab}
              onToggleCollapse={() => setResultsCollapsed((current) => !current)}
            />
          </div>

          <NavigationLabAssetInspector
            asset={selectedAsset}
            assets={assets}
            onAssetSelect={onAssetSelect}
            onOpenSettings={onOpenSettings}
            onMessage={onMessage}
          />
        </>
      )}
    </div>
  );
}

function ViewButton({
  active,
  icon: Icon,
  label,
  onClick,
}: {
  active: boolean;
  icon: LucideIcon;
  label: string;
  onClick: () => void;
}) {
  return (
    <Button
      variant="ghost"
      size="icon-xs"
      className={cn(active && "bg-background text-foreground shadow-sm")}
      aria-label={`${label} view`}
      title={`${label} view`}
      onClick={onClick}
    >
      <Icon />
    </Button>
  );
}

function Property({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg bg-muted/50 p-2">
      <p className="text-[10px] text-muted-foreground">{label}</p>
      <p className="mt-0.5 font-medium">{value}</p>
    </div>
  );
}

function NavigationLabLineageCanvas({
  assets,
  selectedAssetId,
  onAssetSelect,
}: {
  assets: AppLineageCanvasAsset[];
  selectedAssetId: string;
  onAssetSelect: (assetId: string) => void;
}) {
  const groups = ["raw", "staging", "analytics"];
  const groupX: Record<string, number> = { raw: 30, staging: 290, analytics: 550 };
  const positioned = new Map<string, { x: number; y: number }>();

  for (const group of groups) {
    assets
      .filter((asset) => asset.group === group)
      .forEach((asset, index) => {
        positioned.set(asset.id, { x: groupX[group], y: 125 + index * 135 });
      });
  }
  assets
    .filter((asset) => !positioned.has(asset.id))
    .forEach((asset, index) => positioned.set(asset.id, { x: 710, y: 395 + index * 120 }));

  return (
    <div
      className="h-full overflow-auto bg-muted/20"
      style={{
        backgroundImage:
          "radial-gradient(color-mix(in oklab, var(--muted-foreground) 20%, transparent) 1px, transparent 1px)",
        backgroundSize: "22px 22px",
      }}
    >
      <div className="relative h-[680px] min-w-[820px]">
        <svg
          className="pointer-events-none absolute inset-0 size-full"
          viewBox="0 0 820 680"
          preserveAspectRatio="none"
          aria-hidden
        >
          {assets.flatMap((asset) =>
            (asset.upstreams ?? []).map((upstream) => {
              const source = positioned.get(upstream);
              const target = positioned.get(asset.id);
              if (!source || !target) return null;
              const startX = source.x + 190;
              const startY = source.y + 50;
              const endX = target.x;
              const endY = target.y + 50;
              const bend = (startX + endX) / 2;
              return (
                <path
                  key={`${upstream}-${asset.id}`}
                  d={`M ${startX} ${startY} C ${bend} ${startY}, ${bend} ${endY}, ${endX} ${endY}`}
                  fill="none"
                  stroke="var(--border)"
                  strokeWidth="1.5"
                />
              );
            }),
          )}
        </svg>

        {groups.map((group) => {
          const count = assets.filter((asset) => asset.group === group).length;
          if (!count) return null;
          return (
            <div
              key={group}
              className="pointer-events-none absolute rounded-2xl border bg-background/45"
              style={{
                left: groupX[group] - 14,
                top: 82,
                width: 218,
                height: Math.max(165, count * 135 + 48),
              }}
            >
              <div className="flex items-center gap-2 px-3 py-2 text-xs font-semibold">
                <span>{group}</span>
                <Badge variant="secondary" size="xs">
                  {count}
                </Badge>
              </div>
            </div>
          );
        })}

        {assets.map((asset) => {
          const position = positioned.get(asset.id);
          if (!position) return null;
          const title = asset.displayName ?? asset.name.split(".").at(-1) ?? asset.name;
          const Icon =
            asset.kind === "source"
              ? Table2
              : asset.kind === "seed"
                ? FileText
                : asset.kind === "python"
                  ? Code2
                  : asset.kind === "load"
                    ? ArrowDownToLine
                    : FileCode2;
          return (
            <button
              key={asset.id}
              type="button"
              className={cn(
                "absolute z-10 flex h-[102px] w-[190px] flex-col rounded-xl border bg-card text-left shadow-sm transition hover:border-primary/40 hover:shadow-md",
                selectedAssetId === asset.id && "border-primary ring-1 ring-primary/20",
              )}
              style={{ left: position.x, top: position.y }}
              onClick={() => onAssetSelect(asset.id)}
            >
              <span className="flex h-9 items-center gap-2 border-b px-3">
                <Icon className="size-3.5 text-muted-foreground" />
                <span className="min-w-0 flex-1 truncate font-mono text-xs font-medium">
                  {title}
                </span>
                <MoreHorizontal className="size-3.5 text-muted-foreground" />
              </span>
              <span className="flex min-h-0 flex-1 flex-col justify-center gap-1 px-3">
                <span className="flex items-center gap-1.5 text-[10px] text-muted-foreground">
                  {asset.status === "overdue" ? (
                    <Badge variant="destructive" size="xs">
                      Overdue
                    </Badge>
                  ) : (
                    <>
                      <Clock3 className="size-3" />
                      {asset.materializedAt}
                    </>
                  )}
                </span>
                <span className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate text-[10px] text-muted-foreground">
                    {asset.description}
                  </span>
                  <Badge variant="outline" size="xs">
                    {asset.integration}
                  </Badge>
                </span>
              </span>
            </button>
          );
        })}

        <div className="absolute bottom-4 left-4 z-20 flex flex-col rounded-md border bg-background shadow-sm">
          <Button variant="ghost" size="icon-sm" aria-label="Zoom in">
            <Plus />
          </Button>
          <Separator />
          <Button variant="ghost" size="icon-sm" aria-label="Zoom out">
            <span className="text-base leading-none">−</span>
          </Button>
        </div>
      </div>
    </div>
  );
}

function RootDropOverlay({
  palette,
  onDrop,
  onDragEnd,
}: {
  palette: PaletteAsset;
  onDrop: (palette: PaletteAsset) => void;
  onDragEnd: () => void;
}) {
  const allowed = canDropAsset(palette.kind, "root");
  return (
    <div
      className={cn(
        "absolute inset-3 z-20 flex items-center justify-center rounded-xl border-2 border-dashed bg-background/90 backdrop-blur-sm",
        allowed ? "border-primary/60" : "border-destructive/60",
      )}
      onDragOver={(event) => {
        if (!allowed) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = "copy";
      }}
      onDrop={(event) => {
        event.preventDefault();
        if (allowed) onDrop(palette);
        onDragEnd();
      }}
      onDragLeave={(event) => {
        if (event.currentTarget === event.target) onDragEnd();
      }}
    >
      <div className="flex max-w-sm flex-col items-center gap-3 text-center">
        <span
          className={cn("flex size-12 items-center justify-center rounded-2xl", palette.accent)}
        >
          <palette.icon className="size-6" />
        </span>
        <div>
          <p className="font-medium">
            {allowed ? `Add ${palette.label} as a lineage root` : `${palette.label} needs an asset`}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">
            {allowed
              ? "Drop anywhere on the canvas. The generated file remains the source of truth."
              : "Tests attach to a selected asset and are not materialized canvas nodes."}
          </p>
        </div>
      </div>
    </div>
  );
}

export function AdhocQuerySurface() {
  return (
    <div className="grid h-full min-h-0 w-full min-w-0 grid-rows-[minmax(220px,42%)_1fr] bg-background">
      <div className="min-h-0 overflow-auto bg-violet-500/[0.035] p-4 font-mono text-xs leading-6">
        <pre>{`SELECT\n  plan,\n  count(*) AS accounts,\n  avg(health_score) AS avg_health\nFROM analytics.customer_health\nGROUP BY plan\nORDER BY accounts DESC`}</pre>
      </div>
      <div className="min-h-0 overflow-auto border-t bg-background">
        <div className="flex h-9 shrink-0 items-center border-b px-3">
          <span className="text-xs font-semibold">Results</span>
          <span className="ml-auto text-[10px] text-muted-foreground">3 rows · 82 ms</span>
        </div>
        <MockResultTable />
      </div>
    </div>
  );
}

function MockResultTable() {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>plan</TableHead>
          <TableHead className="text-right">accounts</TableHead>
          <TableHead className="text-right">avg_health</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {[
          ["professional", "48,291", "82.4"],
          ["starter", "39,105", "68.7"],
          ["enterprise", "12,912", "91.3"],
        ].map((row) => (
          <TableRow key={row[0]}>
            <TableCell>{row[0]}</TableCell>
            <TableCell className="text-right font-mono">{row[1]}</TableCell>
            <TableCell className="text-right font-mono">{row[2]}</TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

export function DataPreviewSurface({
  table,
  databaseName,
  schemaName,
  connection,
  fitContent = false,
  pinned,
  onTogglePinned,
  onViewChange,
  onMessage,
}: {
  table: BrowserTable;
  databaseName?: string;
  schemaName?: string;
  connection: BrowserConnection;
  fitContent?: boolean;
  pinned: boolean;
  onTogglePinned: () => void;
  onViewChange: (view: BuildView) => void;
  onMessage: (message: string) => void;
}) {
  const [detailTab, setDetailTab] = useState("preview");
  const [columnQuery, setColumnQuery] = useState("");
  const localFile = table.kind === "file";
  const qualifiedName = [databaseName, schemaName || "default", table.name]
    .filter(Boolean)
    .join(localFile ? "/" : ".");
  const objectKind =
    table.kind === "materialized_view"
      ? "Materialized view"
      : table.kind === "external_table"
        ? "External data"
        : table.kind === "file"
          ? "Local file"
          : table.kind === "view"
            ? "View"
            : "Table";
  const visibleColumns = table.columns.filter((column) => {
    const query = columnQuery.trim().toLowerCase();
    return (
      !query ||
      column.name.toLowerCase().includes(query) ||
      column.type.toLowerCase().includes(query) ||
      column.description?.toLowerCase().includes(query) ||
      column.tags?.some((tag) => tag.toLowerCase().includes(query))
    );
  });

  useEffect(() => {
    setDetailTab("preview");
    setColumnQuery("");
  }, [connection.id, databaseName, schemaName, table.name]);

  return (
    <Tabs
      value={detailTab}
      onValueChange={setDetailTab}
      className={cn(
        "min-h-0 min-w-0 gap-0 bg-background",
        fitContent ? "max-h-[min(76dvh,640px)]" : "flex-1",
      )}
    >
      <header className="shrink-0 border-b bg-background px-4 pt-4 sm:px-5 sm:pt-5">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-start">
          <div className="flex min-w-0 flex-1 items-start gap-3">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-blue-500/15 text-blue-600 dark:text-blue-300">
              {localFile ? <FileText className="size-5" /> : <Table2 className="size-5" />}
            </div>
            <div className="min-w-0 flex-1">
              <p className="truncate font-mono text-[10px] text-muted-foreground">
                {[connection.name, databaseName, schemaName || "default"]
                  .filter(Boolean)
                  .join(" / ")}
              </p>
              <div className="mt-0.5 flex flex-wrap items-center gap-2">
                <h2 className="min-w-0 truncate font-mono text-lg font-semibold">{table.name}</h2>
                <Badge variant="outline">{objectKind}</Badge>
                {table.authoredAsset ? (
                  <Badge variant="default">Managed by Renart</Badge>
                ) : (
                  <Badge variant="outline">Observed only</Badge>
                )}
              </div>
              <p className="mt-1 max-w-2xl text-xs text-muted-foreground">
                {table.description ?? "Data object available through this source."}
              </p>
            </div>
          </div>
          <div className="flex shrink-0 flex-wrap gap-2">
            <Button variant="outline" onClick={() => onViewChange("adhoc")}>
              <Code2 data-icon="inline-start" />
              Query
            </Button>
            {!table.authoredAsset ? (
              <Button onClick={() => onMessage("Source asset import review opened")}>
                <ArrowDownToLine data-icon="inline-start" />
                Import source asset
              </Button>
            ) : (
              <Button onClick={() => onMessage(`Opened ${table.authoredAsset}`)}>
                Open asset
                <ArrowRight data-icon="inline-end" />
              </Button>
            )}
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button size="icon" variant="outline" aria-label="More data object actions">
                  <MoreHorizontal />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-52">
                <DropdownMenuLabel>Use this object</DropdownMenuLabel>
                <DropdownMenuItem onSelect={onTogglePinned}>
                  {pinned ? <PinOff /> : <Pin />}
                  {pinned ? "Remove from pinned" : "Pin for this session"}
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => onMessage(`${qualifiedName} copied`)}>
                  <Copy />
                  Copy reference
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => onMessage("SELECT statement copied")}>
                  <Code2 />
                  Copy SELECT statement
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => onMessage("Notebook source picker opened")}>
                  <BookOpen />
                  Add to notebook
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => onMessage("Load asset setup opened")}>
                  <ArrowDownToLine />
                  Use as Load input
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>

        <dl className="mt-4 grid min-w-0 grid-cols-2 border-y text-[10px] [&>div:nth-child(odd)]:border-r sm:grid-cols-4 sm:[&>div]:border-r sm:[&>div:last-child]:border-r-0">
          <ObjectDatum label="Rows" value={table.rows} />
          <ObjectDatum label="Size" value={table.size ?? "Unknown"} />
          <ObjectDatum label="Observed" value={table.freshness.replace(/^Observed /, "")} />
          <ObjectDatum label="Access" value="Read only" />
        </dl>

        <TabsList variant="line" className="mt-2">
          <TabsTrigger value="preview">Data preview</TabsTrigger>
          <TabsTrigger value="columns">Columns</TabsTrigger>
          <TabsTrigger value="usage">Usage</TabsTrigger>
        </TabsList>
      </header>

      <DataPreviewScroller fitContent={fitContent}>
        <div className="mx-auto w-full max-w-5xl p-4 sm:p-5">
          <TabsContent value="preview" className="m-0 space-y-3">
            {!table.authoredAsset ? (
              <div className="flex items-start gap-2 rounded-lg border border-amber-500/30 bg-amber-500/10 p-3 text-xs">
                <TriangleAlert className="mt-0.5 size-4 shrink-0 text-amber-600 dark:text-amber-400" />
                <div>
                  <p className="font-medium">Observed in the source, not managed in Git</p>
                  <p className="mt-0.5 text-muted-foreground">
                    Previewing is read-only. Importing starts a reviewable source-asset change.
                  </p>
                </div>
              </div>
            ) : null}
            <DelimitedCard>
              <DelimitedCardHeader>
                <DelimitedCardTitle>Sample rows</DelimitedCardTitle>
                <span className="ml-auto text-[10px] text-muted-foreground">
                  Explicit query · first 100 rows
                </span>
                <Button
                  size="icon-xs"
                  variant="ghost"
                  aria-label="Refresh data preview"
                  onClick={() => onMessage("Preview refreshed")}
                >
                  <RefreshCw />
                </Button>
              </DelimitedCardHeader>
              <div className="overflow-auto">
                <Table>
                  <TableHeader>
                    <TableRow>
                      {table.columns.slice(0, 6).map((column) => (
                        <TableHead key={column.name}>{column.name}</TableHead>
                      ))}
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {[0, 1, 2, 3, 4].map((index) => (
                      <TableRow key={index}>
                        {table.columns.slice(0, 6).map((column, columnIndex) => (
                          <TableCell
                            key={column.name}
                            className="max-w-52 truncate font-mono text-xs"
                          >
                            {sampleValue(column.name, index, columnIndex)}
                          </TableCell>
                        ))}
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </DelimitedCard>
          </TabsContent>

          <TabsContent value="columns" className="m-0">
            <DelimitedCard>
              <DelimitedCardHeader className="gap-2">
                <DelimitedCardTitle>Columns</DelimitedCardTitle>
                <Badge variant="muted" size="xs">
                  {table.columns.length}
                </Badge>
                <div className="relative ml-auto w-56 max-w-full">
                  <Search className="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    className="h-7 pl-8"
                    placeholder="Find a column"
                    value={columnQuery}
                    onChange={(event) => setColumnQuery(event.target.value)}
                  />
                </div>
              </DelimitedCardHeader>
              <DelimitedCardContent className="p-0">
                {visibleColumns.map((column) => (
                  <div
                    key={column.name}
                    className="grid gap-1 border-b px-3 py-2.5 last:border-0 sm:grid-cols-[minmax(10rem,1fr)_9rem_minmax(12rem,2fr)] sm:items-center"
                  >
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="truncate font-mono text-xs font-medium">{column.name}</span>
                      {column.key ? (
                        <Badge variant="outline" size="xs">
                          {column.key === "primary" ? "PK" : "FK"}
                        </Badge>
                      ) : null}
                      {column.tags?.map((tag) => (
                        <Badge key={tag} variant="destructive" size="xs">
                          {tag}
                        </Badge>
                      ))}
                    </div>
                    <span className="font-mono text-[10px] text-muted-foreground">
                      {column.type}
                    </span>
                    <span className="text-[10px] text-muted-foreground">
                      {column.description ??
                        (column.nullable === false ? "Required" : "No description")}
                    </span>
                  </div>
                ))}
                {visibleColumns.length === 0 ? (
                  <p className="p-5 text-center text-xs text-muted-foreground">
                    No columns match this search.
                  </p>
                ) : null}
              </DelimitedCardContent>
            </DelimitedCard>
          </TabsContent>

          <TabsContent value="usage" className="m-0 space-y-3">
            <DelimitedCard>
              <DelimitedCardHeader>
                <GitCommitHorizontal className="size-4 text-muted-foreground" />
                <DelimitedCardTitle>Renart coverage</DelimitedCardTitle>
              </DelimitedCardHeader>
              <DelimitedCardContent className="p-0">
                {table.authoredAsset ? (
                  <button
                    type="button"
                    className="flex w-full items-center gap-3 px-3 py-3 text-left hover:bg-muted/50"
                    onClick={() => onMessage(`Opened ${table.authoredAsset}`)}
                  >
                    <FileCode2 className="size-4 shrink-0 text-emerald-600" />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate font-mono text-xs font-medium">
                        {table.authoredAsset}
                      </span>
                      <span className="block text-[10px] text-muted-foreground">
                        Managed source of truth · warehouse freshness is shown separately
                      </span>
                    </span>
                    <ArrowRight className="size-3.5" />
                  </button>
                ) : (
                  <div className="flex items-start gap-3 px-3 py-3 text-xs">
                    <TriangleAlert className="mt-0.5 size-4 shrink-0 text-amber-500" />
                    <div>
                      <p className="font-medium">No managed source asset</p>
                      <p className="mt-0.5 text-muted-foreground">
                        Import this positive warehouse observation before depending on it in a
                        deployable pipeline.
                      </p>
                    </div>
                  </div>
                )}
              </DelimitedCardContent>
            </DelimitedCard>
            <DelimitedCard>
              <DelimitedCardHeader>
                <ListTree className="size-4 text-muted-foreground" />
                <DelimitedCardTitle>Known usage</DelimitedCardTitle>
                <Badge className="ml-auto" variant="muted" size="xs">
                  {table.usedBy?.length ?? 0}
                </Badge>
              </DelimitedCardHeader>
              <DelimitedCardContent className="p-0">
                {table.usedBy?.length ? (
                  table.usedBy.map((consumer) => (
                    <button
                      type="button"
                      key={consumer}
                      className="flex w-full items-center gap-2 border-b px-3 py-2.5 text-left text-xs last:border-0 hover:bg-muted/50"
                      onClick={() => onMessage(`Opened ${consumer}`)}
                    >
                      <ArrowRight className="size-3 text-muted-foreground" />
                      <span className="min-w-0 flex-1 truncate">{consumer}</span>
                    </button>
                  ))
                ) : (
                  <p className="p-4 text-xs text-muted-foreground">
                    No workspace asset, notebook, dashboard, or report currently references this
                    object.
                  </p>
                )}
              </DelimitedCardContent>
            </DelimitedCard>
          </TabsContent>
        </div>
      </DataPreviewScroller>
    </Tabs>
  );
}

function DataPreviewScroller({
  fitContent,
  children,
}: {
  fitContent: boolean;
  children: ReactNode;
}) {
  if (fitContent) {
    return (
      <div className="max-h-[calc(min(76dvh,640px)-12rem)] overflow-y-auto bg-muted/20">
        {children}
      </div>
    );
  }
  return (
    <ScrollArea className="min-h-0 flex-1 bg-muted/20" showHorizontalScrollBar={false}>
      {children}
    </ScrollArea>
  );
}

function ObjectDatum({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 px-3 py-2">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="truncate font-medium">{value}</dd>
    </div>
  );
}

export function ConnectionDiscoverySurface({ connection }: { connection: BrowserConnection }) {
  const status = connection.discovery.status;
  return (
    <div className="flex min-h-0 flex-1 items-center justify-center bg-muted/30 p-5">
      <DelimitedCard className="w-full max-w-lg">
        <DelimitedCardHeader>
          {status === "discovering" || status === "refreshing" ? (
            <RefreshCw className="size-4 animate-spin text-sky-500" />
          ) : status === "error" || status === "partial" ? (
            <TriangleAlert className="size-4 text-amber-500" />
          ) : (
            <CheckCircle2 className="size-4 text-emerald-500" />
          )}
          <DelimitedCardTitle>{connection.name}</DelimitedCardTitle>
          <Badge className="ml-auto" variant="outline">
            {status}
          </Badge>
        </DelimitedCardHeader>
        <DelimitedCardContent className="space-y-3 text-xs">
          <p>
            {status === "discovering"
              ? "The connection is saved. Renart is discovering the schemas and tables visible to this role."
              : status === "empty"
                ? "The connection works, but the selected role currently exposes no tables."
                : connection.discovery.detail ||
                  "Select a table in the Data Browser to preview it."}
          </p>
          <div className="rounded-lg bg-muted/50 p-3 text-muted-foreground">
            <p>{connection.discovery.scope}</p>
            <p className="mt-1">Last refresh: {connection.discovery.lastRefreshed}</p>
          </div>
          <p className="text-muted-foreground">
            Metadata discovery reads catalog information only. A row preview runs only after you
            select a table.
          </p>
        </DelimitedCardContent>
      </DelimitedCard>
    </div>
  );
}

function sampleValue(name: string, row: number, column: number) {
  const lower = name.toLowerCase();
  if (lower.includes("id")) return `8c1f…${row}${column}a`;
  if (lower.includes("time") || lower.includes("at"))
    return `2026-08-${String(21 + row).padStart(2, "0")} 09:4${row}`;
  if (lower.includes("plan")) return ["professional", "starter", "enterprise"][row % 3];
  if (lower.includes("name"))
    return ["Acme Labs", "Northwind", "Prism GmbH", "Fjord", "Orbit"][row];
  return ["active", "updated", "trial", "renewed", "paused"][row];
}

export function OperateSurface({
  view,
  onViewChange,
  onMessage,
}: {
  view: OperateView;
  onViewChange: (view: OperateView) => void;
  onMessage: (message: string) => void;
}) {
  if (view === "run-detail") {
    return <RunDetailView onBack={() => onViewChange("runs")} onMessage={onMessage} />;
  }

  return (
    <div className="flex h-full min-h-0 flex-col bg-muted/30">
      <ScrollArea className="min-h-0 flex-1" showHorizontalScrollBar={false}>
        <div className="flex min-h-full flex-col gap-3 p-1.5 sm:p-3">
          {view === "overview" ? (
            <OperateOverview onViewChange={onViewChange} onMessage={onMessage} />
          ) : view === "deployments" ? (
            <DeploymentsView onMessage={onMessage} />
          ) : view === "schedules" ? (
            <SchedulesView onMessage={onMessage} />
          ) : (
            <RunsView onOpenRun={() => onViewChange("run-detail")} onMessage={onMessage} />
          )}
        </div>
      </ScrollArea>
    </div>
  );
}

function OperateOverview({
  onViewChange,
  onMessage,
}: {
  onViewChange: (view: OperateView) => void;
  onMessage: (message: string) => void;
}) {
  const hours = ["06:00", "08:00", "10:00", "12:00", "14:00", "16:00", "18:00", "20:00"];
  return (
    <>
      <div className="grid gap-px overflow-hidden border-y bg-border sm:grid-cols-2 sm:rounded-md sm:border xl:grid-cols-4">
        <OperationalReadout
          label="Active deployment"
          value="v42"
          detail="main · 8f01b4a"
          icon={Rocket}
        />
        <OperationalReadout
          label="Next projected run"
          value="in 18 min"
          detail="revenue-model · hourly"
          icon={Clock3}
        />
        <OperationalReadout
          label="Runs today"
          value="31 / 32"
          detail="1 waiting on source"
          icon={History}
        />
        <OperationalReadout
          label="Environment"
          value="production"
          detail="Protected · deployed only"
          icon={LockKeyhole}
        />
      </div>

      <section className="overflow-hidden rounded-xl border bg-background shadow-sm">
        <div className="flex min-h-11 flex-wrap items-center gap-2 border-b px-2 py-1.5 sm:px-3">
          <div className="relative flex-1 sm:max-w-64" style={{ minWidth: 150 }}>
            <Search className="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input className="h-8 pl-8" placeholder="Filter pipelines" />
          </div>
          <div className="hidden items-center gap-2 text-[10px] text-muted-foreground lg:flex">
            <RunLegend swatch="bg-emerald-500" label="Succeeded" />
            <RunLegend swatch="bg-blue-500" label="Running" />
            <RunLegend swatch="bg-destructive" label="Failed" />
            <RunLegend
              swatch="border border-dashed border-primary bg-background"
              label="Projected"
            />
          </div>
          <div className="ml-auto flex items-center rounded-lg bg-muted p-0.5">
            {["6h", "12h", "24h"].map((range) => (
              <Button
                key={range}
                variant="ghost"
                size="xs"
                className={cn(range === "24h" && "bg-background shadow-sm")}
              >
                {range}
              </Button>
            ))}
          </div>
          <Button variant="outline" size="icon-sm" aria-label="Previous time window">
            <ChevronLeft />
          </Button>
          <Button variant="outline" size="sm">
            Now
          </Button>
          <Button variant="outline" size="icon-sm" aria-label="Next time window">
            <ChevronRight />
          </Button>
        </div>

        <div className="overflow-x-auto">
          <div style={{ minWidth: 860 }}>
            <div
              className="grid border-b bg-muted/10 text-[10px] text-muted-foreground"
              style={{ gridTemplateColumns: "220px minmax(0, 1fr)" }}
            >
              <div className="flex h-12 items-end px-3 pb-2 font-medium">Pipeline / trigger</div>
              <div className="relative grid grid-cols-8 border-l">
                <span className="absolute inset-x-0 top-1 px-2 text-center text-[9px]">
                  Sep 1, 2026
                </span>
                {hours.map((hour) => (
                  <span
                    key={hour}
                    className="flex items-end justify-center border-l pb-2 first:border-l-0"
                  >
                    {hour}
                  </span>
                ))}
              </div>
            </div>
            <CompactTimelineRow
              name="revenue-model"
              detail="hourly"
              events={[
                { position: 5, duration: 1.8, durationLabel: "15 min", type: "success" },
                { position: 12, duration: 1.5, durationLabel: "13 min", type: "success" },
                { position: 19, duration: 2.1, durationLabel: "18 min", type: "success" },
                { position: 26, duration: 1.4, durationLabel: "12 min", type: "success" },
                { position: 33, duration: 2.4, durationLabel: "20 min", type: "success" },
                { position: 40, duration: 1.7, durationLabel: "14 min", type: "success" },
                { position: 47, duration: 1.9, durationLabel: "16 min", type: "success" },
                { position: 54, duration: 2.5, durationLabel: "21 min so far", type: "running" },
                {
                  position: 61,
                  duration: 1.8,
                  durationLabel: "15 min expected",
                  type: "projected",
                },
                {
                  position: 68,
                  duration: 1.8,
                  durationLabel: "15 min expected",
                  type: "projected",
                },
                {
                  position: 75,
                  duration: 1.8,
                  durationLabel: "15 min expected",
                  type: "projected",
                },
                {
                  position: 82,
                  duration: 1.8,
                  durationLabel: "15 min expected",
                  type: "projected",
                },
                {
                  position: 89,
                  duration: 1.8,
                  durationLabel: "15 min expected",
                  type: "projected",
                },
              ]}
              onOpen={() => onViewChange("run-detail")}
            />
            <CompactTimelineRow
              name="product-events"
              detail="every 4 hours"
              badge="Waiting"
              events={[
                { position: 10, duration: 3.2, durationLabel: "27 min", type: "success" },
                { position: 34, duration: 2.8, durationLabel: "24 min", type: "success" },
                { position: 58, duration: 4.5, durationLabel: "waiting 38 min", type: "waiting" },
                { position: 82, duration: 3, durationLabel: "25 min expected", type: "projected" },
              ]}
              onOpen={() => onMessage("Waiting prerequisite opened")}
            />
            <CompactTimelineRow
              name="finance-close"
              detail="manual + monthly"
              badge="1 failed"
              events={[
                { position: 28, duration: 5, durationLabel: "42 min", type: "manual" },
                { position: 72, duration: 3.4, durationLabel: "29 min", type: "failed" },
              ]}
              onOpen={() => onViewChange("run-detail")}
            />
          </div>
        </div>
      </section>

      <div className="grid gap-px overflow-hidden border-y bg-border sm:rounded-md sm:border lg:grid-cols-[1.2fr_1fr]">
        <section className="bg-background">
          <div className="flex min-h-10 items-center gap-2 border-b px-3 py-2">
            <h3 className="text-xs font-semibold">Needs attention</h3>
            <Badge className="ml-auto" variant="destructive" size="xs">
              2
            </Badge>
          </div>
          <div className="divide-y">
            <AttentionRow
              icon={AlertCircle}
              title="finance-close failed"
              detail="Materialization error · 16 minutes ago"
              action="Open run"
              onClick={() => onViewChange("run-detail")}
            />
            <AttentionRow
              icon={CircleDashed}
              title="product-events is waiting"
              detail="Prerequisite source watermark has not advanced"
              action="Inspect wait"
              onClick={() => onMessage("Waiting prerequisite opened")}
            />
          </div>
        </section>
        <section className="bg-background">
          <div className="flex min-h-10 items-center gap-2 border-b px-3 py-2">
            <h3 className="text-xs font-semibold">Deployment readiness</h3>
            <Badge className="ml-auto" variant="outline" size="xs">
              Workspace drift
            </Badge>
          </div>
          <div className="flex flex-col gap-3 p-3">
            <div className="flex items-start gap-2 text-xs">
              <GitCommitHorizontal className="mt-0.5 size-4 text-amber-500" />
              <div>
                <p className="font-medium">3 files changed after v42</p>
                <p className="text-muted-foreground">
                  The active tables are not affected until a reviewed deployment is run.
                </p>
              </div>
            </div>
            <Button
              className="self-start"
              variant="outline"
              onClick={() => onViewChange("deployments")}
            >
              Compare with deployment
              <ArrowRight data-icon="inline-end" />
            </Button>
          </div>
        </section>
      </div>
    </>
  );
}

function OperationalReadout({
  label,
  value,
  detail,
  icon: Icon,
}: {
  label: string;
  value: string;
  detail: string;
  icon: LucideIcon;
}) {
  return (
    <div className="flex min-w-0 items-start gap-2.5 bg-background px-3 py-3">
      <Icon className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
      <div className="min-w-0">
        <p className="text-[10px] font-medium uppercase tracking-[0.08em] text-muted-foreground">
          {label}
        </p>
        <p className="mt-0.5 truncate text-sm font-semibold tabular-nums">{value}</p>
        <p className="truncate text-[10px] text-muted-foreground">{detail}</p>
      </div>
    </div>
  );
}

function RunLegend({ swatch, label }: { swatch: string; label: string }) {
  return (
    <span className="flex items-center gap-1">
      <span className={cn("h-2.5 w-4 rounded-sm", swatch)} />
      {label}
    </span>
  );
}

function CompactTimelineRow({
  name,
  detail,
  badge,
  events,
  onOpen,
}: {
  name: string;
  detail: string;
  badge?: string;
  events: Array<{
    position: number;
    duration: number;
    durationLabel: string;
    type: "success" | "running" | "failed" | "projected" | "manual" | "waiting";
  }>;
  onOpen: () => void;
}) {
  return (
    <div
      className="grid min-h-12 border-b last:border-b-0"
      style={{ gridTemplateColumns: "220px minmax(0, 1fr)" }}
    >
      <button
        type="button"
        className="flex min-w-0 items-center gap-2 px-3 text-left hover:bg-muted/30"
        onClick={onOpen}
      >
        <div className="min-w-0 flex-1">
          <p className="truncate text-xs font-medium">{name}</p>
          <p className="truncate text-[10px] text-muted-foreground">{detail}</p>
        </div>
        {badge ? (
          <Badge variant={badge.includes("failed") ? "destructive" : "outline"} size="xs">
            {badge}
          </Badge>
        ) : null}
        <ChevronRight className="size-3 text-muted-foreground" />
      </button>
      <div className="relative min-h-12 border-l">
        <div className="pointer-events-none absolute inset-0 grid grid-cols-8">
          {Array.from({ length: 8 }, (_, index) => (
            <span key={index} className="border-l first:border-l-0" />
          ))}
        </div>
        <span
          className="pointer-events-none absolute inset-y-0 z-10 border-l border-foreground/50"
          style={{ left: "56%" }}
        />
        {events.map((event, index) => (
          <button
            key={`${event.position}-${index}`}
            type="button"
            aria-label={`${event.type} run, ${event.durationLabel}`}
            title={`${event.type} · ${event.durationLabel}`}
            style={{ left: `${event.position}%`, width: `${event.duration}%` }}
            className={cn(
              "absolute top-1/2 z-20 flex h-4 min-w-1 -translate-y-1/2 items-center justify-center overflow-hidden rounded-sm border border-transparent shadow-sm transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              event.type === "success" && "bg-emerald-500",
              event.type === "running" && "animate-pulse bg-blue-500",
              event.type === "failed" && "bg-destructive",
              event.type === "projected" && "border-dashed border-primary bg-primary/5 shadow-none",
              event.type === "manual" && "bg-violet-500",
              event.type === "waiting" && "border-dashed border-amber-500 bg-amber-500/15",
            )}
            onClick={onOpen}
          >
            {event.type === "manual" ? <Play className="size-1.5 fill-white text-white" /> : null}
          </button>
        ))}
      </div>
    </div>
  );
}

function AttentionRow({
  icon: Icon,
  title,
  detail,
  action,
  onClick,
}: {
  icon: LucideIcon;
  title: string;
  detail: string;
  action: string;
  onClick: () => void;
}) {
  return (
    <div className="flex min-h-12 items-center gap-2 px-3 py-2">
      <Icon className="size-4 shrink-0 text-destructive" />
      <div className="min-w-0 flex-1">
        <p className="truncate text-xs font-medium">{title}</p>
        <p className="truncate text-[10px] text-muted-foreground">{detail}</p>
      </div>
      <Button variant="ghost" size="sm" onClick={onClick}>
        {action}
      </Button>
    </div>
  );
}

const mockRunSteps = [
  { asset: "raw.accounts", left: 0, width: 13, duration: "9s" },
  { asset: "raw.regions", left: 0, width: 8, duration: "5s" },
  { asset: "staging.accounts", left: 15, width: 29, duration: "20s" },
  { asset: "staging.subscriptions", left: 18, width: 35, duration: "24s" },
  { asset: "analytics.customer_health", left: 55, width: 34, duration: "23s" },
  { asset: "analytics.retention_daily", left: 59, width: 27, duration: "18s" },
] as const;

function RunDetailView({
  onBack,
  onMessage,
}: {
  onBack: () => void;
  onMessage: (message: string) => void;
}) {
  const [tab, setTab] = useState("events");
  return (
    <div
      className="flex h-full min-h-0 flex-col bg-muted/30"
      data-testid="navigation-lab-run-detail"
    >
      <div className="flex min-h-12 shrink-0 flex-wrap items-center gap-3 px-3 py-1.5">
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-base font-semibold tracking-tight">
            Run 7b0f1a64-2c8e-4c5a-b6d2-1a3e29f9b74c
          </h2>
          <p className="truncate text-xs text-muted-foreground">
            Run of revenue-model · production · deployment v42 · 6 execution units · 1m 08s
          </p>
        </div>
        <Button variant="ghost" size="icon-sm" aria-label="Back to runs" onClick={onBack}>
          <ArrowLeft />
        </Button>
        <StatusPill status="success" />
        <Button size="sm" onClick={() => onMessage("Exact plan re-execution review opened")}>
          <RotateCw data-icon="inline-start" />
          <span className="hidden sm:inline">Re-execute exact plan</span>
          <span className="sm:hidden">Re-execute</span>
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 px-3 pb-2 text-xs text-muted-foreground">
        <span>
          Replay source <span className="font-medium text-foreground">deployment v42</span>
        </span>
        <span aria-hidden>·</span>
        <span>
          Environment <span className="font-medium text-foreground">production</span>
        </span>
        <span aria-hidden>·</span>
        <span>Recorded window Sep 1, 21:00 → 22:00</span>
        <span aria-hidden>·</span>
        <span>
          Mode <span className="font-medium text-foreground">exact downstream plan · 6 units</span>
        </span>
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-3 px-3 pb-3">
        <MockRunTimeline onAssetOpen={(asset) => onMessage(`${asset} run events selected`)} />
        <section className="min-h-0 flex-1 overflow-hidden rounded-xl border bg-background shadow-sm">
          <Tabs
            value={tab}
            onValueChange={setTab}
            className="flex h-full min-h-0 flex-col gap-0 overflow-hidden"
          >
            <div className="border-b px-2 py-1">
              <TabsList>
                <TabsTrigger value="events">
                  <Play /> Events
                </TabsTrigger>
                <TabsTrigger value="plan">
                  <ListTree /> Plan
                </TabsTrigger>
                <TabsTrigger value="output">
                  <Terminal /> Output
                </TabsTrigger>
              </TabsList>
            </div>
            <TabsContent value="events" className="m-0 min-h-0 flex-1 overflow-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Time</TableHead>
                    <TableHead>Asset</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Event</TableHead>
                    <TableHead className="text-right">Duration</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {[
                    ["22:14:08", "raw.accounts", "Source snapshot completed", "9s"],
                    ["22:14:13", "raw.regions", "Seed materialized", "5s"],
                    ["22:14:28", "staging.accounts", "View replaced", "20s"],
                    ["22:14:37", "staging.subscriptions", "Python asset completed", "24s"],
                    ["22:14:54", "analytics.customer_health", "Table materialized", "23s"],
                    ["22:15:03", "analytics.retention_daily", "Table materialized", "18s"],
                  ].map(([time, asset, event, duration]) => (
                    <TableRow key={asset}>
                      <TableCell className="font-mono text-xs text-muted-foreground">
                        {time}
                      </TableCell>
                      <TableCell className="font-mono text-xs font-medium">{asset}</TableCell>
                      <TableCell>
                        <StatusPill status="success" />
                      </TableCell>
                      <TableCell>{event}</TableCell>
                      <TableCell className="text-right font-mono text-muted-foreground">
                        {duration}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TabsContent>
            <TabsContent value="plan" className="m-0 min-h-0 flex-1 overflow-auto p-3">
              <div className="overflow-hidden rounded-md border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Execution unit</TableHead>
                      <TableHead>Stage</TableHead>
                      <TableHead>Fidelity</TableHead>
                      <TableHead>Connection</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {mockRunSteps.map((step, index) => (
                      <TableRow key={step.asset}>
                        <TableCell className="font-mono text-xs">{step.asset}</TableCell>
                        <TableCell>{index < 2 ? "Extract" : "Materialize"}</TableCell>
                        <TableCell>
                          <Badge variant="secondary">Exact</Badge>
                        </TableCell>
                        <TableCell>duckdb-default</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </TabsContent>
            <TabsContent
              value="output"
              className="m-0 min-h-0 flex-1 overflow-auto bg-zinc-950 p-3"
            >
              <pre className="font-console whitespace-pre-wrap text-xs leading-5 text-zinc-100">
                {`22:14:04 INFO  execution started · revenue-model · deployment v42
22:14:08 INFO  raw.accounts snapshot completed
22:14:37 INFO  staging.subscriptions completed · 12,912 rows
22:14:54 INFO  analytics.customer_health materialized
22:15:12 INFO  run completed successfully · 6/6 units`}
              </pre>
            </TabsContent>
          </Tabs>
        </section>
      </div>
    </div>
  );
}

function MockRunTimeline({ onAssetOpen }: { onAssetOpen: (asset: string) => void }) {
  return (
    <section className="grid shrink-0 grid-cols-1 overflow-hidden rounded-xl border bg-background shadow-sm lg:grid-cols-[minmax(0,1fr)_18rem]">
      <div className="grid grid-cols-[minmax(7rem,12rem)_minmax(0,1fr)] items-center gap-x-3 p-3">
        <span aria-hidden />
        <div className="flex h-5 items-center text-[11px] text-muted-foreground">
          {["0s", "17s", "34s", "51s", "1m 08s"].map((tick) => (
            <span key={tick} className="min-w-0 flex-1 font-mono">
              {tick}
            </span>
          ))}
        </div>
        {mockRunSteps.map((step) => (
          <MockRunTimelineStep key={step.asset} step={step} onOpen={onAssetOpen} />
        ))}
      </div>
      <div className="border-t p-2 lg:border-l lg:border-t-0">
        {[
          ["Preparing", "0"],
          ["Executing", "0"],
          ["Errored", "0"],
          ["Succeeded", "6"],
          ["Cancelled", "0"],
        ].map(([label, count]) => (
          <div
            key={label}
            className="flex h-9 items-center justify-between rounded-md px-2 text-xs hover:bg-muted/60"
          >
            <span className="font-medium">{label}</span>
            <span className="font-mono text-muted-foreground">{count}</span>
          </div>
        ))}
      </div>
    </section>
  );
}

function MockRunTimelineStep({
  step,
  onOpen,
}: {
  step: (typeof mockRunSteps)[number];
  onOpen: (asset: string) => void;
}) {
  return (
    <>
      <button
        type="button"
        className="truncate rounded-sm text-left font-mono text-[11px] leading-4 hover:bg-muted/60"
        onClick={() => onOpen(step.asset)}
      >
        {step.asset}
      </button>
      <button
        type="button"
        className="relative h-5 rounded bg-muted/40"
        onClick={() => onOpen(step.asset)}
        aria-label={`${step.asset}, success, ${step.duration}`}
      >
        <span
          className="absolute inset-y-0 rounded bg-emerald-500"
          style={{ left: `${step.left}%`, width: `${step.width}%` }}
        />
      </button>
    </>
  );
}

function DeploymentsView({ onMessage }: { onMessage: (message: string) => void }) {
  return (
    <>
      <PageIntro
        title="Deployments"
        description="Reviewed, immutable source snapshots. A deployment does not materialize data until it is run."
        action={
          <Button onClick={() => onMessage("Deployment review opened")}>
            <Plus data-icon="inline-start" />
            Create deployment
          </Button>
        }
      />
      <div className="grid gap-3 xl:grid-cols-[1fr_320px]">
        <div className="overflow-hidden border-y bg-background sm:rounded-md sm:border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Version</TableHead>
                <TableHead>Commit</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Schedule bindings</TableHead>
                <TableHead>Created</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {[
                ["v42", "8f01b4a", "Active", "2 schedules", "Today, 09:12"],
                ["v41", "59cb812", "Superseded", "—", "Yesterday, 16:40"],
                ["v40", "a192ef0", "Superseded", "—", "Aug 25, 11:03"],
              ].map((row, index) => (
                <TableRow key={row[0]} className={index === 0 ? "bg-primary/[0.04]" : undefined}>
                  <TableCell className="font-medium">{row[0]}</TableCell>
                  <TableCell className="font-mono text-xs">{row[1]}</TableCell>
                  <TableCell>
                    <Badge variant={index === 0 ? "default" : "muted"}>{row[2]}</Badge>
                  </TableCell>
                  <TableCell>{row[3]}</TableCell>
                  <TableCell className="text-muted-foreground">{row[4]}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
        <DelimitedCard>
          <DelimitedCardHeader>
            <DelimitedCardTitle>v42 bindings</DelimitedCardTitle>
            <Badge className="ml-auto" variant="outline">
              production
            </Badge>
          </DelimitedCardHeader>
          <DelimitedCardContent className="flex flex-col gap-3">
            <BindingRow title="revenue-hourly" detail="0 * * * * · Europe/Berlin" />
            <BindingRow title="daily-health" detail="15 6 * * * · Europe/Berlin" />
            <Button variant="outline" onClick={() => onMessage("Create schedule for v42")}>
              <CalendarClock data-icon="inline-start" />
              Assign or create schedule
            </Button>
          </DelimitedCardContent>
        </DelimitedCard>
      </div>
    </>
  );
}

function BindingRow({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="rounded-lg border p-2">
      <div className="flex items-center gap-2">
        <CheckCircle2 className="size-3.5 text-emerald-500" />
        <p className="text-xs font-medium">{title}</p>
      </div>
      <p className="mt-1 pl-5 text-[10px] text-muted-foreground">{detail}</p>
    </div>
  );
}

function SchedulesView({ onMessage }: { onMessage: (message: string) => void }) {
  return (
    <>
      <PageIntro
        title="Schedules"
        description="Git-tracked cadence and policy, bound locally to a reviewed deployment in each environment."
        action={
          <Button onClick={() => onMessage("New schedule dialog opened")}>
            <Plus data-icon="inline-start" />
            New schedule
          </Button>
        }
      />
      <div className="divide-y overflow-hidden border-y bg-background sm:rounded-md sm:border">
        {[
          {
            name: "revenue-hourly",
            pipeline: "revenue-model",
            cadence: "Every hour at :00",
            binding: "production → v42",
            next: "in 18 minutes",
            healthy: true,
          },
          {
            name: "daily-health",
            pipeline: "revenue-model",
            cadence: "Daily at 06:15",
            binding: "production → v42",
            next: "tomorrow at 06:15",
            healthy: true,
          },
          {
            name: "product-events",
            pipeline: "product-events",
            cadence: "Every 4 hours",
            binding: "staging → v18",
            next: "waiting for source",
            healthy: false,
          },
        ].map((schedule) => (
          <div
            key={schedule.name}
            className="grid gap-3 px-3 py-3 transition-colors hover:bg-muted/20 md:grid-cols-[minmax(180px,1.2fr)_minmax(160px,1fr)_minmax(160px,1fr)_auto] md:items-center"
          >
            <div className="flex min-w-0 items-start gap-2.5">
              <span
                className={cn(
                  "mt-1 size-2 shrink-0 rounded-full",
                  schedule.healthy ? "bg-emerald-500" : "bg-amber-500",
                )}
              />
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-2">
                  <p className="truncate text-xs font-semibold">{schedule.name}</p>
                  <span className="text-[10px] text-muted-foreground">
                    {schedule.healthy ? "Enabled" : "Waiting"}
                  </span>
                </div>
                <p className="truncate text-[10px] text-muted-foreground">{schedule.pipeline}</p>
              </div>
            </div>
            <ScheduleDatum label="Cadence" value={schedule.cadence} />
            <ScheduleDatum
              label={schedule.healthy ? "Next occurrence" : "Current state"}
              value={schedule.next}
              detail={schedule.binding}
            />
            <Button
              className="justify-self-start md:justify-self-end"
              size="icon-sm"
              variant="ghost"
              aria-label={`Open ${schedule.name}`}
              onClick={() => onMessage(`Opened ${schedule.name}`)}
            >
              <ArrowRight />
            </Button>
          </div>
        ))}
      </div>
    </>
  );
}

function RunsView({
  onOpenRun,
  onMessage,
}: {
  onOpenRun: () => void;
  onMessage: (message: string) => void;
}) {
  return (
    <>
      <PageIntro
        title="Runs"
        description="Every scheduled, manual, backfill, and sensor-triggered execution across this project."
        action={
          <Button variant="outline" onClick={() => onMessage("Run filters opened")}>
            <ListFilter data-icon="inline-start" />
            Filters
          </Button>
        }
      />
      <div className="overflow-hidden border-y bg-background sm:rounded-md sm:border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Status</TableHead>
              <TableHead>Pipeline</TableHead>
              <TableHead>Trigger</TableHead>
              <TableHead>Deployment</TableHead>
              <TableHead>Started</TableHead>
              <TableHead>Duration</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {[
              ["success", "revenue-model", "Scheduled", "v42", "10:00", "48s"],
              ["success", "product-events", "Scheduled", "v18", "08:00", "2m 12s"],
              ["failed", "finance-close", "Manual", "v9", "07:31", "18s"],
              ["success", "revenue-model", "Backfill", "v42", "07:15", "1m 08s"],
              ["success", "revenue-model", "Scheduled", "v42", "07:00", "51s"],
            ].map((row) => (
              <TableRow key={`${row[1]}-${row[4]}`} className="cursor-pointer" onClick={onOpenRun}>
                <TableCell>
                  <StatusPill status={row[0]} />
                </TableCell>
                <TableCell className="font-medium">{row[1]}</TableCell>
                <TableCell>{row[2]}</TableCell>
                <TableCell>
                  <Badge variant="outline">{row[3]}</Badge>
                </TableCell>
                <TableCell>{row[4]}</TableCell>
                <TableCell className="text-muted-foreground">{row[5]}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </>
  );
}

function ScheduleDatum({
  label,
  value,
  detail,
}: {
  label: string;
  value: string;
  detail?: string;
}) {
  return (
    <div className="min-w-0 pl-4 md:border-l">
      <p className="text-[9px] font-medium uppercase tracking-[0.08em] text-muted-foreground">
        {label}
      </p>
      <p className="truncate text-xs">{value}</p>
      {detail ? <p className="truncate text-[10px] text-muted-foreground">{detail}</p> : null}
    </div>
  );
}

function PageIntro({
  title,
  description,
  action,
}: {
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-3 sm:flex-row sm:items-start">
      <div className="min-w-0 flex-1">
        <h2 className="text-lg font-semibold">{title}</h2>
        <p className="max-w-2xl text-xs text-muted-foreground">{description}</p>
      </div>
      {action}
    </div>
  );
}

export function ExploreSurface({
  view,
  assets,
  onMessage,
}: {
  view: ExploreView;
  assets: AppLineageCanvasAsset[];
  onViewChange: (view: ExploreView) => void;
  onMessage: (message: string) => void;
}) {
  if (view === "catalog") {
    return <CatalogView assets={assets} onMessage={onMessage} />;
  }

  return (
    <div className="flex h-full min-h-0 flex-col bg-muted/30">
      <ScrollArea className="min-h-0 flex-1" showHorizontalScrollBar={false}>
        <div className="mx-auto flex max-w-6xl flex-col gap-4 p-3 sm:p-5">
          {view === "dashboards" ? (
            <PresentationGrid kind="dashboard" onMessage={onMessage} />
          ) : (
            <PresentationGrid kind="report" onMessage={onMessage} />
          )}
        </div>
      </ScrollArea>
    </div>
  );
}

const workspaceCatalogSupplement: AppLineageCanvasAsset[] = [
  {
    id: "events-api",
    name: "events.raw_events",
    displayName: "raw_events",
    prefix: "events",
    group: "events",
    pipelineId: "product-events",
    kind: "source",
    integration: "Postgres",
    description: "Product event stream",
    status: "success",
    materializedAt: "External source",
    x: 0,
    y: 0,
  },
  {
    id: "events-sessions",
    name: "events.sessions",
    displayName: "sessions",
    prefix: "events",
    group: "events",
    pipelineId: "product-events",
    kind: "sql",
    integration: "DuckDB",
    description: "Sessionized product events",
    status: "success",
    materializedAt: "18 min ago",
    upstreams: ["events-api"],
    x: 0,
    y: 0,
  },
  {
    id: "events-adoption",
    name: "events.account_adoption",
    displayName: "account_adoption",
    prefix: "events",
    group: "events",
    pipelineId: "product-events",
    kind: "sql",
    integration: "DuckDB",
    description: "Feature adoption by account",
    status: "success",
    materializedAt: "18 min ago",
    upstreams: ["events-sessions", "source-accounts"],
    x: 0,
    y: 0,
  },
  {
    id: "finance-invoices",
    name: "finance.invoices",
    displayName: "invoices",
    prefix: "finance",
    group: "finance",
    pipelineId: "finance-close",
    kind: "source",
    integration: "Postgres",
    description: "Billing source records",
    status: "success",
    materializedAt: "External source",
    x: 0,
    y: 0,
  },
  {
    id: "finance-monthly",
    name: "finance.monthly_revenue",
    displayName: "monthly_revenue",
    prefix: "finance",
    group: "finance",
    pipelineId: "finance-close",
    kind: "sql",
    integration: "DuckDB",
    description: "Closed monthly revenue",
    status: "success",
    materializedAt: "Yesterday, 23:41",
    upstreams: ["finance-invoices", "mart-health"],
    x: 0,
    y: 0,
  },
];

function CatalogView({
  assets,
  onMessage,
}: {
  assets: AppLineageCanvasAsset[];
  onMessage: (message: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [selectedAssetId, setSelectedAssetId] = useState<string | undefined>();
  const catalogAssets = [
    ...assets.map((asset) => ({ ...asset, pipelineId: asset.pipelineId ?? "revenue-model" })),
    ...workspaceCatalogSupplement,
  ];
  const normalizedQuery = query.trim().toLowerCase();
  const filteredAssets = normalizedQuery
    ? catalogAssets.filter((asset) =>
        [asset.name, asset.displayName, asset.pipelineId, asset.integration]
          .filter(Boolean)
          .some((value) => value?.toLowerCase().includes(normalizedQuery)),
      )
    : catalogAssets;
  const pipelineCount = new Set(catalogAssets.map((asset) => asset.pipelineId)).size;

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 bg-muted/30 p-1.5 sm:p-3">
      <section className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-xl border bg-background shadow-sm">
        <div className="flex min-h-11 flex-wrap items-center gap-2 border-b px-2 py-1.5 sm:px-3">
          <Button
            variant="outline"
            size="sm"
            onClick={() => onMessage("Catalog asset type filters opened")}
          >
            <ListFilter data-icon="inline-start" />
            Filter
          </Button>
          <div className="relative min-w-36 flex-1">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="h-8 pl-8"
              placeholder="Filter workspace assets by name"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </div>
          <div className="hidden items-center gap-1.5 text-[10px] text-muted-foreground sm:flex">
            <Badge variant="muted">{pipelineCount} pipelines</Badge>
            <Badge variant="muted">{filteredAssets.length} assets</Badge>
          </div>
          <Button
            variant="outline"
            size="icon-sm"
            aria-label="Reload workspace catalog"
            onClick={() => onMessage("Workspace catalog reloaded")}
          >
            <RefreshCw />
          </Button>
        </div>
        <div className="min-h-0 flex-1">
          <AppLineageCanvas
            assets={filteredAssets}
            selectedAssetId={selectedAssetId}
            onAssetSelect={setSelectedAssetId}
            onRunAsset={(assetId) => onMessage(`Run review opened for ${assetId}`)}
            onGoToAsset={(assetId) => onMessage(`Opened ${assetId} in Build`)}
            goToLabel="Open in build"
          />
        </div>
      </section>
    </div>
  );
}

function PresentationGrid({
  kind,
  onMessage,
}: {
  kind: "dashboard" | "report";
  onMessage: (message: string) => void;
}) {
  const items =
    kind === "dashboard"
      ? ["Customer health", "Revenue pulse", "Retention cohorts", "Product adoption"]
      : ["Weekly operating review", "Customer risk brief", "August close"];
  return (
    <>
      <PageIntro
        title={kind === "dashboard" ? "Dashboards" : "Reports"}
        description="Git-backed presentation definitions, statically checked against producer pipeline schemas."
        action={
          <Button onClick={() => onMessage(`New ${kind} created`)}>
            <Plus data-icon="inline-start" />
            New {kind}
          </Button>
        }
      />
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        {items.map((item, index) => (
          <DelimitedCard
            key={item}
            className="cursor-pointer transition-colors hover:border-primary/40"
            onClick={() => onMessage(`Opened ${item}`)}
          >
            <DelimitedCardContent>
              <div className="mb-4 flex h-28 items-end gap-2 rounded-lg bg-muted/60 p-3">
                {kind === "dashboard" ? (
                  [45, 72, 58, 88, 66, 92, 78].map((height, bar) => (
                    <span
                      key={bar}
                      className="flex-1 rounded-t bg-primary/60"
                      style={{ height: `${height}%` }}
                    />
                  ))
                ) : (
                  <div className="flex w-full flex-col gap-2">
                    <span className="h-2 w-1/3 rounded bg-foreground/70" />
                    <span className="h-1.5 w-full rounded bg-muted-foreground/20" />
                    <span className="h-1.5 w-5/6 rounded bg-muted-foreground/20" />
                    <span className="mt-2 h-12 rounded bg-primary/10" />
                  </div>
                )}
              </div>
              <div className="flex items-start gap-2">
                <div className="min-w-0 flex-1">
                  <p className="truncate font-medium">{item}</p>
                  <p className="text-[10px] text-muted-foreground">
                    Updated {index + 1} day{index ? "s" : ""} ago
                  </p>
                </div>
                <Badge variant="muted">
                  <ShieldCheck className="size-2.5" />
                  Valid
                </Badge>
              </div>
            </DelimitedCardContent>
          </DelimitedCard>
        ))}
      </div>
    </>
  );
}

export function NotebookSurface({
  onMessage,
  embedded = false,
}: {
  onMessage: (message: string) => void;
  embedded?: boolean;
}) {
  return (
    <div className="flex h-full min-h-0 w-full flex-col bg-muted/20">
      {!embedded ? (
        <div className="flex h-11 shrink-0 items-center gap-2 border-b bg-background px-3">
          <BookOpen className="size-4 text-primary" />
          <div className="min-w-0">
            <p className="truncate font-semibold">Cohort explorer</p>
            <p className="hidden text-[10px] text-muted-foreground sm:block">
              notebooks/cohort-explorer
            </p>
          </div>
          <Badge className="ml-2" variant="muted">
            Saved
          </Badge>
          <Button
            className="ml-auto"
            variant="outline"
            onClick={() => onMessage("Notebook AI opened")}
          >
            <Sparkles data-icon="inline-start" />
            Ask AI
          </Button>
          <Button onClick={() => onMessage("Notebook run started")}>
            <Play data-icon="inline-start" />
            Run all
          </Button>
        </div>
      ) : null}
      <ScrollArea className="min-h-0 flex-1" showHorizontalScrollBar={false}>
        <div className="mx-auto flex max-w-4xl flex-col gap-3 p-3 sm:p-6">
          <div className="rounded-lg px-3 py-4 hover:bg-background hover:shadow-sm">
            <h1 className="text-2xl font-semibold">Retention cohort explorer</h1>
            <p className="mt-2 text-sm text-muted-foreground">
              Explore how account retention changes by signup cohort and plan. Data is snapshotted
              into the notebook DuckDB session.
            </p>
          </div>
          <NotebookCell number={1} label="SQL" icon={Code2} accent="text-amber-500">
            <pre className="whitespace-pre-wrap font-mono text-xs leading-5">{`select cohort_month, day_number, retained_accounts\nfrom analytics.retention_daily\nwhere plan = {{ plan }}`}</pre>
          </NotebookCell>
          <NotebookCell number={2} label="Visualization" icon={LineChart} accent="text-violet-500">
            <div className="flex h-40 items-end gap-2 rounded-lg bg-muted/30 p-4">
              {[82, 72, 64, 58, 49, 44, 39, 35, 32, 29, 27].map((height, index) => (
                <span
                  key={index}
                  className="flex-1 rounded-t bg-violet-500/70"
                  style={{ height: `${height}%` }}
                />
              ))}
            </div>
          </NotebookCell>
          <NotebookCell number={3} label="Control" icon={ListFilter} accent="text-teal-500">
            <div className="flex items-center gap-3">
              <span className="text-xs font-medium">Plan</span>
              <div className="rounded-md border bg-background px-3 py-1.5 text-xs">
                Professional
              </div>
              <span className="text-[10px] text-muted-foreground">Updates dependent cells</span>
            </div>
          </NotebookCell>
        </div>
      </ScrollArea>
    </div>
  );
}

function NotebookCell({
  number,
  label,
  icon: Icon,
  accent,
  children,
}: {
  number: number;
  label: string;
  icon: LucideIcon;
  accent: string;
  children: ReactNode;
}) {
  return (
    <DelimitedCard className="border-transparent bg-background/70 shadow-none hover:border-border hover:shadow-sm">
      <DelimitedCardHeader className="min-h-9 border-0 bg-transparent py-1">
        <span className="w-5 text-right font-mono text-[10px] text-muted-foreground">{number}</span>
        <Icon className={cn("size-3.5", accent)} />
        <DelimitedCardTitle className="text-xs">{label}</DelimitedCardTitle>
        <Button className="ml-auto" variant="ghost" size="icon-sm" aria-label={`Run ${label} cell`}>
          <Play />
        </Button>
      </DelimitedCardHeader>
      <DelimitedCardContent className="pt-0">{children}</DelimitedCardContent>
    </DelimitedCard>
  );
}

export function SettingsSurface({
  view,
  section,
  selectedConnection,
  environment,
  onMessage,
}: {
  view: SettingsView;
  section: SettingsSection;
  selectedConnection: BrowserConnection;
  environment: string;
  onMessage: (message: string) => void;
}) {
  const title =
    view === "connections"
      ? "Connections"
      : view === "environments"
        ? "Environments"
        : view === "pipeline"
          ? "Pipeline settings"
          : "Project settings";
  return (
    <div className="flex h-full min-h-0 flex-col bg-muted/30">
      <div className="flex h-11 shrink-0 items-center border-b bg-background px-3">
        <p className="font-semibold">{title}</p>
        <Badge className="ml-2" variant="outline">
          Design study
        </Badge>
      </div>
      <ScrollArea className="min-h-0 flex-1" showHorizontalScrollBar={false}>
        <div className="mx-auto flex max-w-4xl flex-col gap-4 p-3 sm:p-6">
          {view === "connections" ? (
            <ConnectionsSettings connection={selectedConnection} onMessage={onMessage} />
          ) : view === "environments" ? (
            <EnvironmentSettings environment={environment} onMessage={onMessage} />
          ) : view === "pipeline" ? (
            <PipelineSettings section={section} onMessage={onMessage} />
          ) : (
            <ProjectSettings section={section} />
          )}
        </div>
      </ScrollArea>
    </div>
  );
}

function ConnectionsSettings({
  connection,
  onMessage,
}: {
  connection: BrowserConnection;
  onMessage: (message: string) => void;
}) {
  const tableCount = connection.schemas.reduce((count, schema) => count + schema.tables.length, 0);
  const localFiles = connection.type === "Local files";
  return (
    <>
      <PageIntro
        title={connection.name}
        description={`${connection.type} connection. Credentials remain server-side and write-only; this workbench receives safe identity and capability metadata only.`}
        action={
          <Button onClick={() => onMessage(`Editing ${connection.name}`)}>Edit connection</Button>
        }
      />
      <div className="grid gap-3 md:grid-cols-2">
        <DelimitedCard>
          <DelimitedCardHeader>
            <DelimitedCardTitle>Identity and discovery</DelimitedCardTitle>
            <Badge className="ml-auto" variant="muted">
              Connected
            </Badge>
          </DelimitedCardHeader>
          <DelimitedCardContent className="grid gap-4 sm:grid-cols-2">
            <Property label="Type" value={connection.type} />
            <Property label="Safe ID" value={connection.id} />
            <Property
              label={localFiles ? "Folders" : "Schemas / catalogs"}
              value={String(connection.schemas.length)}
            />
            <Property
              label={localFiles ? "Discovered files" : "Discovered tables"}
              value={String(tableCount)}
            />
          </DelimitedCardContent>
        </DelimitedCard>
        <DelimitedCard>
          <DelimitedCardHeader>
            <DelimitedCardTitle>Capabilities</DelimitedCardTitle>
          </DelimitedCardHeader>
          <DelimitedCardContent className="flex flex-wrap gap-2">
            <Badge variant="outline">Read-only browse</Badge>
            <Badge variant="outline">Ad-hoc query</Badge>
            <Badge variant="outline">Schema discovery</Badge>
            <Badge variant="outline">
              {connection.type === "S3" || localFiles ? "Source import" : "Materialization"}
            </Badge>
          </DelimitedCardContent>
        </DelimitedCard>
        <DelimitedCard className="md:col-span-2">
          <DelimitedCardHeader>
            <DelimitedCardTitle>Used by</DelimitedCardTitle>
          </DelimitedCardHeader>
          <DelimitedCardContent className="grid gap-2 sm:grid-cols-3">
            {["revenue-model", "product-events", "Cohort explorer"].map((consumer, index) => (
              <button
                key={consumer}
                type="button"
                className="rounded-lg border p-3 text-left hover:bg-muted/50"
                onClick={() => onMessage(`${consumer} opened`)}
              >
                <p className="text-xs font-medium">{consumer}</p>
                <p className="mt-1 text-[10px] text-muted-foreground">
                  {index === 2 ? "Notebook data source" : "Pipeline connection"}
                </p>
              </button>
            ))}
          </DelimitedCardContent>
        </DelimitedCard>
      </div>
    </>
  );
}

function EnvironmentSettings({
  environment,
  onMessage,
}: {
  environment: string;
  onMessage: (message: string) => void;
}) {
  const selected =
    labEnvironments.find((candidate) => candidate.id === environment) ?? labEnvironments[0];
  return (
    <>
      <PageIntro
        title={selected.id}
        description={selected.detail}
        action={
          <Button onClick={() => onMessage(`Editing ${selected.id}`)}>Edit environment</Button>
        }
      />
      <div className="grid gap-3 md:grid-cols-2">
        <DelimitedCard>
          <DelimitedCardHeader>
            <DelimitedCardTitle>Execution policy</DelimitedCardTitle>
            <Badge
              className="ml-auto"
              variant={selected.policy === "Protected" ? "destructive" : "muted"}
            >
              {selected.policy}
            </Badge>
          </DelimitedCardHeader>
          <DelimitedCardContent className="grid gap-4 sm:grid-cols-2">
            <Property label="Environment" value={selected.id} />
            <Property
              label="Execution source"
              value={selected.policy === "Protected" ? "Deployment only" : "Working tree"}
            />
            <Property
              label="Destructive changes"
              value={selected.policy === "Protected" ? "Confirmation required" : "Allowed"}
            />
            <Property label="Default time zone" value="Europe/Berlin" />
          </DelimitedCardContent>
        </DelimitedCard>
        <DelimitedCard>
          <DelimitedCardHeader>
            <DelimitedCardTitle>Connection overrides</DelimitedCardTitle>
          </DelimitedCardHeader>
          <DelimitedCardContent className="flex flex-col gap-3">
            <Property label="Default connection" value={selected.connection} />
            <Property
              label="Pipeline overrides"
              value={selected.id === "default" ? "None" : "2 configured"}
            />
            <Button
              className="self-start"
              variant="outline"
              onClick={() => onMessage("Connection overrides opened")}
            >
              Manage overrides
            </Button>
          </DelimitedCardContent>
        </DelimitedCard>
      </div>
    </>
  );
}

function PipelineSettings({
  section,
  onMessage,
}: {
  section: SettingsSection;
  onMessage: (message: string) => void;
}) {
  const sectionTitle =
    section === "execution"
      ? "Execution"
      : section === "python"
        ? "Python dependencies"
        : section === "variables"
          ? "Variables"
          : section === "hooks"
            ? "Pre and post hooks"
            : "General";
  return (
    <>
      <PageIntro
        title={`revenue-model · ${sectionTitle}`}
        description="Pipeline-level settings remain reviewable in pipeline.yml."
        action={<Button onClick={() => onMessage("Pipeline settings saved")}>Save changes</Button>}
      />
      {section === "execution" ? (
        <div className="grid gap-3 md:grid-cols-2">
          <DelimitedCard>
            <DelimitedCardHeader>
              <DelimitedCardTitle>Run defaults</DelimitedCardTitle>
            </DelimitedCardHeader>
            <DelimitedCardContent className="grid gap-4 sm:grid-cols-2">
              <Property label="Time zone" value="Europe/Berlin" />
              <Property label="Start date" value="2026-01-01" />
              <Property label="Concurrency" value="4 assets" />
              <Property label="Retries" value="2 attempts" />
            </DelimitedCardContent>
          </DelimitedCard>
          <DelimitedCard>
            <DelimitedCardHeader>
              <DelimitedCardTitle>Execution behavior</DelimitedCardTitle>
            </DelimitedCardHeader>
            <DelimitedCardContent className="grid gap-4 sm:grid-cols-2">
              <Property label="Sensor mode" value="Wait" />
              <Property label="Full refresh" value="Off by default" />
              <Property label="Failure policy" value="Stop downstreams" />
              <Property label="Pool" value="default" />
            </DelimitedCardContent>
          </DelimitedCard>
        </div>
      ) : section === "python" ? (
        <DelimitedCard>
          <DelimitedCardHeader>
            <DelimitedCardTitle>Python runtime</DelimitedCardTitle>
            <Badge className="ml-auto" variant="muted">
              uv
            </Badge>
          </DelimitedCardHeader>
          <DelimitedCardContent className="grid gap-4 sm:grid-cols-2">
            <Property label="Python version" value="3.12" />
            <Property label="Isolation" value="Pipeline environment" />
            <Property label="Packages" value="polars>=1, requests>=2" />
            <Property label="Lock state" value="Resolved locally" />
            <Button
              className="sm:col-span-2 sm:justify-self-start"
              variant="outline"
              onClick={() => onMessage("Python dependencies editor opened")}
            >
              Edit dependencies
            </Button>
          </DelimitedCardContent>
        </DelimitedCard>
      ) : section === "variables" ? (
        <DelimitedCard>
          <DelimitedCardHeader>
            <DelimitedCardTitle>Pipeline variables</DelimitedCardTitle>
            <Button
              className="ml-auto"
              size="sm"
              variant="outline"
              onClick={() => onMessage("Variable row added")}
            >
              <Plus data-icon="inline-start" />
              Add variable
            </Button>
          </DelimitedCardHeader>
          <DelimitedCardContent className="flex flex-col gap-2">
            {[
              ["reporting_currency", "EUR"],
              ["lookback_days", "30"],
              ["feature_schema", "analytics"],
            ].map(([name, value]) => (
              <div key={name} className="grid gap-2 sm:grid-cols-[1fr_1fr_auto]">
                <Input aria-label={`${name} name`} value={name} readOnly />
                <Input aria-label={`${name} value`} value={value} readOnly />
                <Button size="sm" variant="ghost" onClick={() => onMessage(`${name} removed`)}>
                  Remove
                </Button>
              </div>
            ))}
          </DelimitedCardContent>
        </DelimitedCard>
      ) : section === "hooks" ? (
        <div className="grid gap-3 md:grid-cols-2">
          {[
            ["Pre hook", "set timezone = 'Europe/Berlin';"],
            ["Post hook", "analyze analytics.customer_health;"],
          ].map(([label, sql]) => (
            <DelimitedCard key={label}>
              <DelimitedCardHeader>
                <DelimitedCardTitle>{label}</DelimitedCardTitle>
                <Badge className="ml-auto" variant="outline">
                  SQL
                </Badge>
              </DelimitedCardHeader>
              <DelimitedCardContent>
                <pre className="overflow-x-auto rounded-lg bg-muted p-3 font-mono text-xs">
                  {sql}
                </pre>
              </DelimitedCardContent>
            </DelimitedCard>
          ))}
        </div>
      ) : (
        <div className="grid gap-3 md:grid-cols-2">
          <DelimitedCard>
            <DelimitedCardHeader>
              <DelimitedCardTitle>Identity</DelimitedCardTitle>
            </DelimitedCardHeader>
            <DelimitedCardContent className="grid gap-4 sm:grid-cols-2">
              <Property label="Pipeline name" value="revenue-model" />
              <Property label="Path" value="pipelines/revenue" />
              <Property label="Owner" value="data-platform" />
              <Property label="Asset count" value="6" />
            </DelimitedCardContent>
          </DelimitedCard>
          <DelimitedCard>
            <DelimitedCardHeader>
              <DelimitedCardTitle>Defaults</DelimitedCardTitle>
            </DelimitedCardHeader>
            <DelimitedCardContent className="grid gap-4 sm:grid-cols-2">
              <Property label="Default connection" value="duckdb-default" />
              <Property label="Default environment" value="default" />
              <Property label="Validation" value="Blocking" />
              <Property label="Schedule ownership" value="Project" />
            </DelimitedCardContent>
          </DelimitedCard>
        </div>
      )}
    </>
  );
}

function ProjectSettings({ section }: { section: SettingsSection }) {
  const sectionTitle =
    section === "git"
      ? "Git and project paths"
      : section === "appearance"
        ? "Appearance"
        : section === "security"
          ? "Credentials and vault"
          : "General";
  return (
    <>
      <PageIntro
        title={`Growth data platform · ${sectionTitle}`}
        description="Project-wide settings stay separate from pipeline-specific configuration."
      />
      {section === "git" ? (
        <div className="grid gap-3 md:grid-cols-2">
          <DelimitedCard>
            <DelimitedCardHeader>
              <DelimitedCardTitle>Repository</DelimitedCardTitle>
            </DelimitedCardHeader>
            <DelimitedCardContent className="grid gap-4 sm:grid-cols-2">
              <Property label="Repository" value="renart-example" />
              <Property label="Current branch" value="main" />
              <Property label="Workspace root" value="/workspace/growth" />
              <Property label="Uncommitted files" value="2" />
            </DelimitedCardContent>
          </DelimitedCard>
          <DelimitedCard>
            <DelimitedCardHeader>
              <DelimitedCardTitle>Project paths</DelimitedCardTitle>
            </DelimitedCardHeader>
            <DelimitedCardContent className="grid gap-4 sm:grid-cols-2">
              <Property label="Pipelines" value="pipelines/" />
              <Property label="Notebooks" value="notebooks/" />
              <Property label="Presentations" value="presentations/" />
              <Property label="Renart state" value=".renart/" />
            </DelimitedCardContent>
          </DelimitedCard>
        </div>
      ) : section === "appearance" ? (
        <DelimitedCard>
          <DelimitedCardHeader>
            <DelimitedCardTitle>Workbench appearance</DelimitedCardTitle>
          </DelimitedCardHeader>
          <DelimitedCardContent className="grid gap-4 sm:grid-cols-2">
            <Property label="Theme" value="System" />
            <Property label="Editor font" value="Inconsolata · 13px" />
            <Property label="Canvas density" value="Comfortable" />
            <Property label="Motion" value="Follow system" />
          </DelimitedCardContent>
        </DelimitedCard>
      ) : section === "security" ? (
        <div className="grid gap-3 md:grid-cols-2">
          <DelimitedCard>
            <DelimitedCardHeader>
              <DelimitedCardTitle>Local vault</DelimitedCardTitle>
              <Badge className="ml-auto" variant="muted">
                Unlocked
              </Badge>
            </DelimitedCardHeader>
            <DelimitedCardContent className="grid gap-4 sm:grid-cols-2">
              <Property label="Storage" value="Encrypted local vault" />
              <Property label="Credential exposure" value="Write-only" />
            </DelimitedCardContent>
          </DelimitedCard>
          <DelimitedCard>
            <DelimitedCardHeader>
              <DelimitedCardTitle>Execution boundary</DelimitedCardTitle>
            </DelimitedCardHeader>
            <DelimitedCardContent className="grid gap-4 sm:grid-cols-2">
              <Property label="Remote access" value="Disabled" />
              <Property label="Filesystem access" value="Workspace-scoped" />
            </DelimitedCardContent>
          </DelimitedCard>
        </div>
      ) : (
        <DelimitedCard>
          <DelimitedCardContent className="grid gap-4 sm:grid-cols-2">
            <Property label="Project name" value="Growth data platform" />
            <Property label="Repository" value="renart-example" />
            <Property label="Default environment" value="default" />
            <Property label="Workspace catalog" value="11 authored assets" />
          </DelimitedCardContent>
        </DelimitedCard>
      )}
    </>
  );
}

export function ProjectsSurface({ onOpenProject }: { onOpenProject: () => void }) {
  return (
    <div className="flex h-full min-h-0 flex-col bg-muted/30">
      <ScrollArea className="min-h-0 flex-1" showHorizontalScrollBar={false}>
        <div className="mx-auto flex max-w-5xl flex-col gap-5 p-4 sm:p-8">
          <PageIntro
            title="Projects"
            description="Each project is a Git repository with its own pipelines, connections, environments, and operating state."
          />
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            {[
              ["Growth data platform", "3 pipelines · 11 assets", "main · 2 changes"],
              ["Finance analytics", "4 pipelines · 41 assets", "main · clean"],
              ["Product telemetry", "2 pipelines · 18 assets", "feature/events · 1 change"],
            ].map(([name, detail, git], index) => (
              <DelimitedCard
                key={name}
                className="cursor-pointer transition-colors hover:border-primary/40"
                onClick={onOpenProject}
              >
                <DelimitedCardContent className="flex flex-col gap-4">
                  <span className="flex size-10 items-center justify-center rounded-xl bg-primary/10 text-primary">
                    <Boxes className="size-5" />
                  </span>
                  <div>
                    <p className="font-medium">{name}</p>
                    <p className="text-xs text-muted-foreground">{detail}</p>
                  </div>
                  <div className="flex items-center text-[10px] text-muted-foreground">
                    <GitCommitHorizontal className="mr-1 size-3" />
                    {git}
                    <span className="ml-auto">
                      {index === 0 ? "Open now" : "Open"} <ArrowRight className="inline size-3" />
                    </span>
                  </div>
                </DelimitedCardContent>
              </DelimitedCard>
            ))}
          </div>
        </div>
      </ScrollArea>
    </div>
  );
}

export function PaletteGrid({ onDragStart }: { onDragStart: (palette: PaletteAsset) => void }) {
  return (
    <div className="grid grid-cols-2 gap-2">
      {paletteAssets.map((palette) => (
        <button
          key={palette.kind}
          type="button"
          draggable
          className="group flex min-w-0 items-center gap-2 rounded-lg border bg-background p-2 text-left transition hover:border-primary/40 hover:bg-muted/40 active:cursor-grabbing"
          onDragStart={(event: DragEvent<HTMLButtonElement>) => {
            event.dataTransfer.setData("application/x-renart-asset-kind", palette.kind);
            event.dataTransfer.effectAllowed = "copyLink";
            onDragStart(palette);
          }}
        >
          <span
            className={cn(
              "flex size-7 shrink-0 items-center justify-center rounded-lg",
              palette.accent,
            )}
          >
            <palette.icon className="size-3.5" />
          </span>
          <span className="min-w-0">
            <span className="block truncate text-[11px] font-medium">{palette.label}</span>
            <span className="block truncate text-[9px] text-muted-foreground">
              {palette.description}
            </span>
          </span>
        </button>
      ))}
    </div>
  );
}
