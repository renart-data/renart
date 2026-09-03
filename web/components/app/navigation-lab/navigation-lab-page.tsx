import { Link } from "@tanstack/react-router";
import {
  Activity,
  AlertTriangle,
  ArrowDownToLine,
  BookOpen,
  Boxes,
  Cable,
  CalendarClock,
  Check,
  ChevronLeft,
  ChevronDown,
  ChevronRight,
  CircleDot,
  Code2,
  Compass,
  Database,
  Eye,
  FileCode2,
  FileText,
  FolderOpen,
  FolderGit2,
  GitBranch,
  Hammer,
  History,
  LayoutDashboard,
  ListFilter,
  Loader2,
  LockKeyhole,
  Menu,
  Network,
  Palette,
  PanelLeft,
  Pin,
  Play,
  Plus,
  RefreshCw,
  Rocket,
  Search,
  Settings2,
  SlidersHorizontal,
  Table2,
  TerminalSquare,
  X,
  type LucideIcon,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useIsMobile } from "@/hooks/use-mobile";
import { cn } from "@/lib/utils";

import { ConnectionTypeIcon } from "../connection-type-icon";

import type { SpecialDocumentTab } from "./navigation-lab-asset-workbench";
import {
  browserConnections,
  createDroppedAsset,
  initialLabAssets,
  labEnvironments,
  variantMeta,
  type BrowserConnection,
  type BrowserDiscoveryStatus,
  type BrowserObjectKind,
  type BrowserSchema,
  type BrowserTable,
  type BuildView,
  type ExploreView,
  type LabArea,
  type LabVariant,
  type OperateView,
  type PaletteAsset,
  type SettingsView,
  type SettingsSection,
  type UtilityPane,
} from "./navigation-lab-data";
import {
  BuildSurface,
  ConnectionDiscoverySurface,
  DataPreviewSurface,
  ExploreSurface,
  NotebookSurface,
  OperateSurface,
  PaletteGrid,
  ProjectsSurface,
  SettingsSurface,
} from "./navigation-lab-surfaces";

type LabMode = Exclude<LabArea, "notebooks">;
type DataBrowserLevel = "connections" | "connection";
type BuildOverlay = "data" | "settings" | "table";

function modeForArea(area: LabArea): LabMode {
  return area === "notebooks" ? "build" : area;
}

function browserTableKey(connectionId: string, schemaName: string, tableName: string) {
  return `${connectionId}/${schemaName}/${tableName}`;
}

export function NavigationLabPage({ variant }: { variant: LabVariant }) {
  const isMobile = useIsMobile();
  const [area, setArea] = useState<LabArea>("build");
  const [buildView, setBuildView] = useState<BuildView>("split");
  const [operateView, setOperateView] = useState<OperateView>("overview");
  const [exploreView, setExploreView] = useState<ExploreView>("catalog");
  const [utilityPaneByMode, setUtilityPaneByMode] = useState<Record<LabMode, UtilityPane>>({
    build: "context",
    operate: "context",
    explore: "context",
  });
  const [settingsView, setSettingsView] = useState<SettingsView>("connections");
  const [settingsSection, setSettingsSection] = useState<SettingsSection>("general");
  const [specialSurface, setSpecialSurface] = useState<"projects" | null>(null);
  const [selectedAssetId, setSelectedAssetId] = useState("mart-health");
  const [openAssetIds, setOpenAssetIds] = useState([
    "source-accounts",
    "stg-subscriptions",
    "mart-health",
  ]);
  const [openSpecialTabs, setOpenSpecialTabs] = useState<SpecialDocumentTab[]>([]);
  const [assets, setAssets] = useState(() => [...initialLabAssets]);
  const [dragKind, setDragKind] = useState<PaletteAsset | null>(null);
  const [connections, setConnections] = useState<BrowserConnection[]>(() => [
    ...browserConnections,
  ]);
  const [connectionId, setConnectionId] = useState(browserConnections[0].id);
  const [schemaName, setSchemaName] = useState(browserConnections[0].schemas[0].name);
  const [tableName, setTableName] = useState(
    browserConnections[0].schemas[0].tables[1]?.name ??
      browserConnections[0].schemas[0].tables[0].name,
  );
  const [environment, setEnvironment] = useState("production");
  const [mobileNavigationOpen, setMobileNavigationOpen] = useState(false);
  const [wideSidebarOpen, setWideSidebarOpen] = useState(true);
  const [dataBrowserLevel, setDataBrowserLevel] = useState<DataBrowserLevel>("connections");
  const [buildOverlay, setBuildOverlay] = useState<BuildOverlay | null>(null);
  const [warehouseDialogOpen, setWarehouseDialogOpen] = useState(false);
  const [pinnedTables, setPinnedTables] = useState<string[]>([
    browserTableKey("warehouse", "ANALYTICS", "CUSTOMER_HEALTH"),
  ]);
  const [recentTables, setRecentTables] = useState<string[]>([
    browserTableKey("postgres-production", "public", "accounts"),
    browserTableKey("warehouse", "ANALYTICS", "RETENTION_DAILY"),
  ]);
  const [message, setMessage] = useState<string | null>(null);
  const [sequence, setSequence] = useState(1);
  const refreshTimers = useRef<number[]>([]);

  const utilityPane = utilityPaneByMode[modeForArea(area)];

  const setUtilityPane = (pane: UtilityPane) => {
    const mode = modeForArea(area);
    setUtilityPaneByMode((current) => ({ ...current, [mode]: pane }));
  };

  useEffect(() => {
    if (!message) return;
    const timeout = window.setTimeout(() => setMessage(null), 3200);
    return () => window.clearTimeout(timeout);
  }, [message]);

  useEffect(
    () => () => {
      refreshTimers.current.forEach((timer) => window.clearTimeout(timer));
    },
    [],
  );

  const selectedConnection = useMemo(
    () => connections.find((connection) => connection.id === connectionId) ?? connections[0],
    [connectionId, connections],
  );
  const selectedSchema = useMemo(
    () =>
      selectedConnection?.schemas.find((schema) => schema.name === schemaName) ??
      selectedConnection?.schemas[0],
    [schemaName, selectedConnection],
  );
  const selectedTable = useMemo(
    () =>
      tableName ? selectedSchema?.tables.find((table) => table.name === tableName) : undefined,
    [selectedSchema, tableName],
  );

  const selectArea = (next: LabArea) => {
    setArea(next);
    setBuildOverlay(null);
    if (next === "notebooks" && variant === "workbench") {
      setUtilityPaneByMode((current) => ({ ...current, build: "context" }));
      setWideSidebarOpen(true);
    }
    setSpecialSurface(null);
    setMobileNavigationOpen(false);
  };

  const openContext = () => {
    if (modeForArea(area) === "build") {
      setArea("build");
      setBuildView("canvas");
      setBuildOverlay(null);
    }
    setUtilityPane("context");
    setWideSidebarOpen(true);
    setSpecialSurface(null);
    setMobileNavigationOpen(false);
  };

  const changeBuildView = (view: BuildView) => {
    setArea("build");
    setBuildView(view);
    if (view === "adhoc") {
      setOpenSpecialTabs((current) =>
        current.includes("adhoc") ? current : [...current, "adhoc"],
      );
    }
    setUtilityPaneByMode((current) => ({ ...current, build: "context" }));
    setBuildOverlay(null);
    setSpecialSurface(null);
    setMobileNavigationOpen(false);
  };

  const changeOperateView = (view: OperateView) => {
    setArea("operate");
    setOperateView(view);
    setUtilityPaneByMode((current) => ({ ...current, operate: "context" }));
    setBuildOverlay(null);
    setWideSidebarOpen(true);
    setSpecialSurface(null);
    setMobileNavigationOpen(false);
  };

  const changeExploreView = (view: ExploreView) => {
    setArea("explore");
    setExploreView(view);
    setUtilityPaneByMode((current) => ({ ...current, explore: "context" }));
    setBuildOverlay(null);
    setWideSidebarOpen(true);
    setSpecialSurface(null);
    setMobileNavigationOpen(false);
  };

  const openDataBrowser = () => {
    const buildMode = modeForArea(area) === "build";
    if (buildMode) {
      setArea("build");
      setBuildView("canvas");
      setBuildOverlay(variant === "workbench" && isMobile ? "data" : null);
    }
    setUtilityPane("data");
    setDataBrowserLevel("connections");
    setWideSidebarOpen(true);
    setSpecialSurface(null);
    setMobileNavigationOpen(false);
  };

  const openSettings = (view: SettingsView) => {
    const buildMode = modeForArea(area) === "build";
    if (buildMode) {
      setArea("build");
      setBuildView("canvas");
      setBuildOverlay(variant === "workbench" && isMobile ? "settings" : null);
    }
    setSettingsView(view);
    setSettingsSection("general");
    setUtilityPane("settings");
    setWideSidebarOpen(true);
    setSpecialSurface(null);
    setMobileNavigationOpen(false);
  };

  const openWarehouseDialog = () => {
    setMobileNavigationOpen(false);
    setWarehouseDialogOpen(true);
  };

  const openProjects = () => {
    setBuildOverlay(null);
    setSpecialSurface("projects");
    setMobileNavigationOpen(false);
  };

  const chooseConnection = (connection: BrowserConnection) => {
    const firstSchema = connection.schemas[0];
    setConnectionId(connection.id);
    setSchemaName(firstSchema?.name ?? "");
    setTableName("");
    if (utilityPane === "data") {
      setDataBrowserLevel("connection");
    } else if (variant === "workbench" && modeForArea(area) === "build") {
      setBuildOverlay("settings");
      setMobileNavigationOpen(false);
    }
    if (connection.schemas.length === 0) {
      setMobileNavigationOpen(false);
    }
  };

  const chooseTable = (connection: BrowserConnection, schema: string, table: BrowserTable) => {
    const key = browserTableKey(connection.id, schema, table.name);
    setConnectionId(connection.id);
    setSchemaName(schema);
    setTableName(table.name);
    setRecentTables((current) => [key, ...current.filter((item) => item !== key)].slice(0, 6));
    if (variant === "workbench" && modeForArea(area) === "build") {
      setBuildOverlay("table");
    }
    setSpecialSurface(null);
    setMobileNavigationOpen(false);
  };

  const openNotebook = () => {
    setArea("notebooks");
    setOpenSpecialTabs((current) =>
      current.includes("notebook") ? current : [...current, "notebook"],
    );
    setUtilityPaneByMode((current) => ({ ...current, build: "context" }));
    setBuildOverlay(null);
    setSpecialSurface(null);
    setMobileNavigationOpen(false);
  };

  const openAssetDocument = (assetId: string) => {
    setArea("build");
    setBuildOverlay(null);
    setUtilityPaneByMode((current) => ({ ...current, build: "context" }));
    setSelectedAssetId(assetId);
    setOpenAssetIds((current) => (current.includes(assetId) ? current : [...current, assetId]));
    if (buildView === "adhoc" || buildView === "data" || area === "notebooks") {
      setBuildView("split");
    }
    setSpecialSurface(null);
    setMobileNavigationOpen(false);
  };

  const closeAssetDocument = (assetId: string) => {
    const closingIndex = openAssetIds.indexOf(assetId);
    const remaining = openAssetIds.filter((id) => id !== assetId);
    setOpenAssetIds(remaining);
    if (assetId !== selectedAssetId) return;
    const nextAssetId = remaining[Math.min(Math.max(closingIndex, 0), remaining.length - 1)];
    if (nextAssetId) {
      setSelectedAssetId(nextAssetId);
      return;
    }
    setBuildView("canvas");
  };

  const selectSpecialDocument = (tab: SpecialDocumentTab) => {
    if (tab === "adhoc") {
      changeBuildView("adhoc");
      return;
    }
    openNotebook();
  };

  const closeSpecialDocument = (tab: SpecialDocumentTab) => {
    setOpenSpecialTabs((current) => current.filter((item) => item !== tab));
    const isActive =
      (tab === "adhoc" && area === "build" && buildView === "adhoc") ||
      (tab === "notebook" && area === "notebooks");
    if (!isActive) return;
    setArea("build");
    setBuildView(openAssetIds.length > 0 ? "split" : "canvas");
    setBuildOverlay(null);
    setUtilityPaneByMode((current) => ({ ...current, build: "context" }));
  };

  const togglePinnedTable = (
    connection: BrowserConnection,
    schema: string,
    table: BrowserTable,
  ) => {
    const key = browserTableKey(connection.id, schema, table.name);
    setPinnedTables((current) =>
      current.includes(key) ? current.filter((item) => item !== key) : [...current, key],
    );
  };

  const updateConnectionStatus = (id: string, status: BrowserDiscoveryStatus, detail?: string) => {
    setConnections((current) =>
      current.map((connection) =>
        connection.id === id
          ? {
              ...connection,
              discovery: {
                ...connection.discovery,
                status,
                detail,
                lastRefreshed:
                  status === "ready" || status === "empty"
                    ? "just now"
                    : connection.discovery.lastRefreshed,
              },
            }
          : connection,
      ),
    );
  };

  const refreshConnection = (id: string) => {
    const connection = connections.find((item) => item.id === id);
    if (!connection) return;
    updateConnectionStatus(
      id,
      "refreshing",
      "Refreshing metadata; last-known-good objects remain visible.",
    );
    const timer = window.setTimeout(() => {
      updateConnectionStatus(
        id,
        connection.schemas.length === 0 ? "empty" : "ready",
        connection.schemas.length === 0
          ? "The connection succeeded, but this role currently sees no tables."
          : undefined,
      );
    }, 1100);
    refreshTimers.current.push(timer);
  };

  const dropAsset = (palette: PaletteAsset, target: "root" | "downstream" | "gate" | "test") => {
    const selected = assets.find((asset) => asset.id === selectedAssetId);
    if (target === "gate") {
      setMessage(`${palette.label} attached as a readiness condition on ${selected?.name}`);
      setDragKind(null);
      return;
    }
    if (target === "test") {
      setMessage(`${palette.label} attached to ${selected?.name}; no materialized node was added`);
      setDragKind(null);
      return;
    }
    const next = createDroppedAsset(
      palette,
      sequence,
      target === "downstream" ? selected : undefined,
    );
    setAssets((current) => [...current, next]);
    setSelectedAssetId(next.id);
    setOpenAssetIds((current) => [...current, next.id]);
    setBuildView("split");
    setSequence((current) => current + 1);
    setDragKind(null);
    setMessage(
      target === "root"
        ? `${palette.label} added as a lineage root`
        : `${palette.label} added downstream of ${selected?.name}`,
    );
  };

  const sidebarProps: LabSidebarProps = {
    area,
    buildView,
    operateView,
    exploreView,
    selectedAssetId,
    connections,
    selectedConnection,
    selectedSchemaName: selectedSchema?.name,
    selectedTable,
    pinnedTables,
    recentTables,
    dataBrowserLevel,
    wideSidebarOpen,
    utilityPane,
    settingsView,
    settingsSection,
    environment,
    onSelectArea: selectArea,
    onOpenContext: openContext,
    onBuildViewChange: changeBuildView,
    onOperateViewChange: changeOperateView,
    onExploreViewChange: changeExploreView,
    onAssetSelect: openAssetDocument,
    onDragStart: setDragKind,
    onOpenNotebook: openNotebook,
    onOpenData: openDataBrowser,
    onBackToConnections: () => setDataBrowserLevel("connections"),
    onSidebarOpenChange: setWideSidebarOpen,
    onAddConnection: openWarehouseDialog,
    onOpenSettings: openSettings,
    onSettingsSectionChange: (section) => {
      setSettingsSection(section);
      if (variant === "workbench" && modeForArea(area) === "build") {
        setBuildOverlay("settings");
        setMobileNavigationOpen(false);
      }
    },
    onEnvironmentChange: (nextEnvironment) => {
      setEnvironment(nextEnvironment);
      if (variant === "workbench" && modeForArea(area) === "build") {
        setBuildOverlay("settings");
        setMobileNavigationOpen(false);
      }
    },
    onChooseConnection: chooseConnection,
    onChooseTable: chooseTable,
    onRefreshConnection: refreshConnection,
    onTogglePinnedTable: togglePinnedTable,
    onMessage: setMessage,
  };

  const workbenchBuild = variant === "workbench" && modeForArea(area) === "build";
  const activeDocument: "asset" | SpecialDocumentTab =
    area === "notebooks" ? "notebook" : buildView === "adhoc" ? "adhoc" : "asset";
  const buildSurface = (
    <BuildSurface
      assets={assets}
      selectedAssetId={selectedAssetId}
      openAssetIds={openAssetIds}
      openSpecialTabs={openSpecialTabs}
      activeDocument={activeDocument}
      view={workbenchBuild && buildView === "data" ? "canvas" : buildView}
      selectedTable={selectedTable}
      selectedDatabaseName={selectedSchema?.database}
      selectedSchemaName={selectedSchema?.name}
      selectedConnection={selectedConnection}
      tablePinned={Boolean(
        selectedSchema &&
        selectedTable &&
        pinnedTables.includes(
          browserTableKey(selectedConnection.id, selectedSchema.name, selectedTable.name),
        ),
      )}
      dragKind={dragKind}
      onAssetSelect={openAssetDocument}
      onCloseAsset={closeAssetDocument}
      onSpecialTabSelect={selectSpecialDocument}
      onCloseSpecialTab={closeSpecialDocument}
      onViewChange={changeBuildView}
      onDropAsset={dropAsset}
      onDragEnd={() => setDragKind(null)}
      onOpenSettings={openSettings}
      onToggleTablePinned={() => {
        if (selectedConnection && selectedSchema && selectedTable) {
          togglePinnedTable(selectedConnection, selectedSchema.name, selectedTable);
        }
      }}
      onMessage={setMessage}
    />
  );

  const surface =
    specialSurface === "projects" ? (
      <ProjectsSurface onOpenProject={() => setSpecialSurface(null)} />
    ) : workbenchBuild ? (
      buildSurface
    ) : utilityPane === "settings" ? (
      <SettingsSurface
        view={settingsView}
        section={settingsSection}
        selectedConnection={selectedConnection}
        environment={environment}
        onMessage={setMessage}
      />
    ) : utilityPane === "data" ? (
      selectedTable ? (
        <DataPreviewSurface
          table={selectedTable}
          databaseName={selectedSchema?.database}
          schemaName={selectedSchema?.name}
          connection={selectedConnection}
          pinned={
            selectedSchema
              ? pinnedTables.includes(
                  browserTableKey(selectedConnection.id, selectedSchema.name, selectedTable.name),
                )
              : false
          }
          onTogglePinned={() => {
            if (selectedSchema) {
              togglePinnedTable(selectedConnection, selectedSchema.name, selectedTable);
            }
          }}
          onViewChange={changeBuildView}
          onMessage={setMessage}
        />
      ) : (
        <ConnectionDiscoverySurface connection={selectedConnection} />
      )
    ) : area === "build" ? (
      buildSurface
    ) : area === "operate" ? (
      <OperateSurface view={operateView} onViewChange={changeOperateView} onMessage={setMessage} />
    ) : area === "explore" ? (
      <ExploreSurface
        view={exploreView}
        assets={assets}
        onViewChange={changeExploreView}
        onMessage={setMessage}
      />
    ) : (
      <NotebookSurface onMessage={setMessage} />
    );

  const buildOverlayTitle =
    buildOverlay === "data"
      ? "Data Browser"
      : buildOverlay === "settings"
        ? "Settings"
        : "Data preview";

  const closeBuildOverlay = () => {
    setBuildOverlay(null);
  };

  const buildOverlayContent =
    buildOverlay === "data" ? (
      <DataBrowserSidebar {...sidebarProps} />
    ) : buildOverlay === "settings" ? (
      <SettingsSurface
        view={settingsView}
        section={settingsSection}
        selectedConnection={selectedConnection}
        environment={environment}
        onMessage={setMessage}
      />
    ) : buildOverlay === "table" && selectedTable ? (
      <DataPreviewSurface
        table={selectedTable}
        databaseName={selectedSchema?.database}
        schemaName={selectedSchema?.name}
        connection={selectedConnection}
        fitContent
        pinned={Boolean(
          selectedSchema &&
          pinnedTables.includes(
            browserTableKey(selectedConnection.id, selectedSchema.name, selectedTable.name),
          ),
        )}
        onTogglePinned={() => {
          if (selectedSchema) {
            togglePinnedTable(selectedConnection, selectedSchema.name, selectedTable);
          }
        }}
        onViewChange={changeBuildView}
        onMessage={setMessage}
      />
    ) : null;

  return (
    <div className="flex h-dvh min-h-0 flex-col overflow-hidden bg-background text-foreground">
      <LabSwitcher variant={variant} />
      <LabHeader
        area={area}
        environment={environment}
        onEnvironmentChange={setEnvironment}
        onAreaChange={selectArea}
        onProjects={openProjects}
        onMobileNavigation={
          variant === "workbench" ? undefined : () => setMobileNavigationOpen(true)
        }
        onMessage={setMessage}
      />
      {variant === "workbench" ? (
        <MobileWorkbenchToolTabs
          {...sidebarProps}
          mobileNavigationOpen={mobileNavigationOpen}
          onMobileNavigationOpenChange={setMobileNavigationOpen}
        />
      ) : null}

      <main
        className={cn(
          "flex min-h-0 flex-1 overflow-hidden",
          variant === "workbench" && "bg-muted/30",
        )}
      >
        {variant === "workbench" ? (
          <>
            <div className="my-1.5 ml-1.5 hidden shrink-0 overflow-hidden rounded-xl border bg-card shadow-sm md:flex">
              <WorkbenchRail {...sidebarProps} />
              {wideSidebarOpen ? (
                <DesktopSidebar embedded variant={variant} {...sidebarProps} />
              ) : null}
            </div>
            <div className="min-w-0 flex-1">{surface}</div>
          </>
        ) : variant === "lifecycle" ? (
          <>
            <DesktopSidebar variant={variant} {...sidebarProps} />
            <div className="min-w-0 flex-1">{surface}</div>
          </>
        ) : (
          <>
            <StudioSidebar {...sidebarProps} />
            {utilityPane === "data" ? (
              <div className="hidden w-80 shrink-0 border-r bg-card lg:block">
                <DataBrowserSidebar {...sidebarProps} />
              </div>
            ) : null}
            <div className="flex min-w-0 flex-1 flex-col">
              <StudioPhaseBar
                area={area}
                buildView={buildView}
                operateView={operateView}
                onBuildViewChange={changeBuildView}
                onOperateViewChange={changeOperateView}
              />
              <div className="min-h-0 flex-1">{surface}</div>
            </div>
          </>
        )}
      </main>

      <Dialog
        open={workbenchBuild && buildOverlayContent !== null}
        onOpenChange={(open) => {
          if (open) return;
          closeBuildOverlay();
        }}
      >
        <DialogContent
          className={cn(
            "flex max-w-[calc(100%-1rem)] flex-col overflow-visible p-0 sm:w-[min(92vw,1120px)] sm:max-w-[min(92vw,1120px)]",
            buildOverlay === "table" ? "max-h-[min(76dvh,640px)]" : "h-[min(86dvh,780px)]",
          )}
          showCloseButton={false}
        >
          <DialogHeader className="sr-only">
            <DialogTitle>{buildOverlayTitle}</DialogTitle>
            <DialogDescription>
              Temporary Build utility overlay. Closing it returns to the current work surface.
            </DialogDescription>
          </DialogHeader>
          <div
            className={cn(
              "min-h-0 overflow-hidden rounded-xl",
              buildOverlay !== "table" && "flex-1",
            )}
          >
            {buildOverlayContent}
          </div>
          <Button
            className="absolute right-2 top-2 z-20 rounded-full border bg-background shadow-sm sm:-right-5 sm:-top-4"
            size="icon-sm"
            variant="ghost"
            aria-label="Close"
            onClick={closeBuildOverlay}
          >
            <X />
          </Button>
        </DialogContent>
      </Dialog>

      <MobileBottomNavigation area={area} onAreaChange={selectArea} />
      <Sheet open={mobileNavigationOpen} onOpenChange={setMobileNavigationOpen}>
        <SheetContent side="left" className="w-[min(90vw,360px)] p-0 sm:max-w-none">
          <SheetHeader className="sr-only">
            <SheetTitle>Navigation and resources</SheetTitle>
            <SheetDescription>
              {variantMeta[variant].label} · one contextual pane on mobile
            </SheetDescription>
          </SheetHeader>
          <div className="min-h-0 flex-1">
            {utilityPane === "data" ? (
              <DataBrowserSidebar {...sidebarProps} />
            ) : utilityPane === "settings" ? (
              <SettingsSidebar {...sidebarProps} />
            ) : variant === "studio" ? (
              <StudioResources {...sidebarProps} />
            ) : variant === "workbench" ? (
              <MobileWorkbenchContextSidebar {...sidebarProps} />
            ) : (
              <ContextSidebar variant={variant} {...sidebarProps} />
            )}
          </div>
        </SheetContent>
      </Sheet>

      <AddWarehouseDialog
        open={warehouseDialogOpen}
        environment={environment}
        onOpenChange={setWarehouseDialogOpen}
        onCreate={(draft) => {
          const id = `${draft.connector.type}-${sequence}`;
          const connection = createMockBrowserConnection(id, draft);
          setConnections((current) => [...current, connection]);
          setConnectionId(id);
          setSchemaName("");
          setTableName("");
          setSequence((current) => current + 1);
          setWarehouseDialogOpen(false);
          setUtilityPane("data");
          setDataBrowserLevel("connection");
          setBuildOverlay(null);
          setSpecialSurface(null);
          if (isMobile) {
            setMobileNavigationOpen(true);
          }
          setMessage(`${connection.name} created · discovering metadata`);

          const timer = window.setTimeout(() => {
            const discovered = discoverMockConnection(connection);
            setConnections((current) =>
              current.map((item) => (item.id === connection.id ? discovered : item)),
            );
            const firstSchema = discovered.schemas[0];
            setSchemaName(firstSchema?.name ?? "");
            setTableName("");
            setMessage(
              discovered.discovery.status === "empty"
                ? `${connection.name} connected · no visible tables`
                : `${connection.name} is ready to browse`,
            );
          }, 1400);
          refreshTimers.current.push(timer);
        }}
        onViewAll={() => {
          setWarehouseDialogOpen(false);
          openSettings("connections");
        }}
      />

      {message ? (
        <div className="pointer-events-none fixed inset-x-3 bottom-[calc(4rem+env(safe-area-inset-bottom))] z-[60] flex justify-center md:bottom-5">
          <div className="pointer-events-auto flex max-w-md items-center gap-2 rounded-lg border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-lg">
            <Check className="size-3.5 text-emerald-500" />
            <span className="min-w-0 flex-1">{message}</span>
            <Button
              size="icon-xs"
              variant="ghost"
              aria-label="Dismiss message"
              onClick={() => setMessage(null)}
            >
              <X />
            </Button>
          </div>
        </div>
      ) : null}
    </div>
  );
}

type WarehouseConnector = {
  type: string;
  label: string;
  description: string;
};

type WarehouseConnectionDraft = {
  connector: WarehouseConnector;
  name: string;
  endpoint: string;
  database: string;
};

const warehouseConnectors: WarehouseConnector[] = [
  { type: "postgres", label: "PostgreSQL", description: "Operational and analytical Postgres" },
  { type: "snowflake", label: "Snowflake", description: "Cloud data warehouse" },
  { type: "bigquery", label: "BigQuery", description: "Google Cloud analytics" },
  { type: "redshift", label: "Redshift", description: "AWS data warehouse" },
  { type: "databricks", label: "Databricks", description: "Lakehouse SQL warehouses" },
  { type: "trino", label: "Trino", description: "Distributed SQL query engine" },
  { type: "clickhouse", label: "ClickHouse", description: "Real-time columnar analytics" },
  { type: "duckdb", label: "DuckDB", description: "Local analytical database" },
];

function AddWarehouseDialog({
  open,
  environment,
  onOpenChange,
  onCreate,
  onViewAll,
}: {
  open: boolean;
  environment: string;
  onOpenChange: (open: boolean) => void;
  onCreate: (draft: WarehouseConnectionDraft) => void;
  onViewAll: () => void;
}) {
  const [selectedType, setSelectedType] = useState<string | null>(null);
  const [step, setStep] = useState<"choose" | "details">("choose");
  const [name, setName] = useState("");
  const [endpoint, setEndpoint] = useState("");
  const [database, setDatabase] = useState("");
  const [credential, setCredential] = useState("");
  const [testState, setTestState] = useState<"idle" | "testing" | "verified">("idle");
  const selectedConnector = warehouseConnectors.find(
    (connector) => connector.type === selectedType,
  );

  useEffect(() => {
    if (!open) return;
    setSelectedType(null);
    setStep("choose");
    setName("");
    setEndpoint("");
    setDatabase("");
    setCredential("");
    setTestState("idle");
  }, [open]);

  const continueToDetails = () => {
    if (!selectedConnector) return;
    setName(`${selectedConnector.type}-analytics`);
    setEndpoint(
      selectedConnector.type === "duckdb"
        ? ".renart/data/analytics.duckdb"
        : `${selectedConnector.type}.internal.example`,
    );
    setDatabase(selectedConnector.type === "snowflake" ? "ANALYTICS" : "analytics");
    setStep("details");
  };

  const verify = () => {
    setTestState("testing");
    window.setTimeout(() => setTestState("verified"), 750);
  };

  const endpointLabel =
    selectedConnector?.type === "duckdb"
      ? "Database file"
      : selectedConnector?.type === "bigquery"
        ? "Project ID"
        : selectedConnector?.type === "snowflake"
          ? "Account"
          : "Host or endpoint";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[90dvh] min-w-0 flex-col overflow-hidden sm:max-w-2xl">
        <DialogHeader>
          <div className="flex items-center gap-2">
            <ConnectionTypeIcon
              connectionType={selectedConnector?.type ?? "default"}
              className="size-8"
            />
            <div className="min-w-0">
              <DialogTitle>
                {step === "choose"
                  ? "Connect a data warehouse"
                  : `Configure ${selectedConnector?.label}`}
              </DialogTitle>
              <DialogDescription>
                {step === "choose" ? "Choose a common connector" : "Connection details"} for{" "}
                <span className="font-mono">{environment}</span>.
              </DialogDescription>
            </div>
          </div>
        </DialogHeader>
        {step === "choose" ? (
          <>
            <ScrollArea className="min-h-0 flex-1" showHorizontalScrollBar={false}>
              <div className="grid grid-cols-2 gap-2 p-1 sm:grid-cols-4">
                {warehouseConnectors.map((connector) => (
                  <Button
                    key={connector.type}
                    type="button"
                    variant={selectedType === connector.type ? "secondary" : "outline"}
                    aria-pressed={selectedType === connector.type}
                    className="h-28 min-w-0 flex-col items-start justify-between gap-2 whitespace-normal p-3 text-left"
                    onClick={() => setSelectedType(connector.type)}
                  >
                    <span className="flex w-full items-start justify-between gap-2">
                      <ConnectionTypeIcon connectionType={connector.type} className="size-9" />
                      {selectedType === connector.type ? (
                        <Badge variant="default" size="xs">
                          Selected
                        </Badge>
                      ) : null}
                    </span>
                    <span className="min-w-0">
                      <span className="block truncate font-semibold">{connector.label}</span>
                      <span className="mt-0.5 line-clamp-2 block text-[10px] font-normal text-muted-foreground">
                        {connector.description}
                      </span>
                    </span>
                  </Button>
                ))}
              </div>
            </ScrollArea>
            <p className="text-[10px] text-muted-foreground">
              Object storage and less common connectors remain available in Connections.
            </p>
            <DialogFooter className="sm:justify-between">
              <Button type="button" variant="ghost" onClick={onViewAll}>
                All connection types
              </Button>
              <Button type="button" disabled={!selectedConnector} onClick={continueToDetails}>
                Continue{selectedConnector ? ` with ${selectedConnector.label}` : ""}
                <ChevronRight data-icon="inline-end" />
              </Button>
            </DialogFooter>
          </>
        ) : selectedConnector ? (
          <>
            <ScrollArea className="min-h-0 flex-1" showHorizontalScrollBar={false}>
              <div className="grid gap-4 p-1 sm:grid-cols-2">
                <label className="flex min-w-0 flex-col gap-1.5 text-xs">
                  <span className="font-medium">Connection name</span>
                  <Input value={name} onChange={(event) => setName(event.target.value)} />
                  <span className="text-[10px] text-muted-foreground">
                    The alias assets and queries use in this environment.
                  </span>
                </label>
                <label className="flex min-w-0 flex-col gap-1.5 text-xs">
                  <span className="font-medium">Environment</span>
                  <Input value={environment} readOnly className="font-mono" />
                  <span className="text-[10px] text-muted-foreground">
                    Environment coverage can be expanded later in Connections.
                  </span>
                </label>
                <label className="flex min-w-0 flex-col gap-1.5 text-xs">
                  <span className="font-medium">{endpointLabel}</span>
                  <Input
                    value={endpoint}
                    onChange={(event) => {
                      setEndpoint(event.target.value);
                      setTestState("idle");
                    }}
                  />
                </label>
                <label className="flex min-w-0 flex-col gap-1.5 text-xs">
                  <span className="font-medium">
                    {selectedConnector.type === "snowflake" ? "Warehouse / database" : "Database"}
                  </span>
                  <Input
                    value={database}
                    onChange={(event) => {
                      setDatabase(event.target.value);
                      setTestState("idle");
                    }}
                  />
                </label>
                {selectedConnector.type !== "duckdb" ? (
                  <label className="flex min-w-0 flex-col gap-1.5 text-xs sm:col-span-2">
                    <span className="font-medium">Credential</span>
                    <Input
                      type="password"
                      autoComplete="new-password"
                      placeholder="Write-only secret"
                      value={credential}
                      onChange={(event) => {
                        setCredential(event.target.value);
                        setTestState("idle");
                      }}
                    />
                    <span className="text-[10px] text-muted-foreground">
                      Stored through Renart's secret boundary and never returned to the browser.
                    </span>
                  </label>
                ) : null}
              </div>
            </ScrollArea>
            <div
              className={cn(
                "flex items-start gap-2 rounded-lg border p-3 text-xs",
                testState === "verified"
                  ? "border-emerald-500/30 bg-emerald-500/10"
                  : "bg-muted/30",
              )}
            >
              {testState === "testing" ? (
                <Loader2 className="mt-0.5 size-4 shrink-0 animate-spin" />
              ) : testState === "verified" ? (
                <Check className="mt-0.5 size-4 shrink-0 text-emerald-600" />
              ) : (
                <LockKeyhole className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
              )}
              <div>
                <p className="font-medium">
                  {testState === "testing"
                    ? "Testing the connection"
                    : testState === "verified"
                      ? "Connection verified"
                      : "Verify before creating"}
                </p>
                <p className="mt-0.5 text-muted-foreground">
                  {testState === "verified"
                    ? "Renart can connect. Metadata discovery starts after creation."
                    : "The test resolves only this connection and does not retain returned data."}
                </p>
              </div>
            </div>
            <DialogFooter className="sm:justify-between">
              <Button type="button" variant="ghost" onClick={() => setStep("choose")}>
                Back
              </Button>
              <div className="flex flex-col-reverse gap-2 sm:flex-row">
                <Button
                  type="button"
                  variant="outline"
                  disabled={
                    testState === "testing" ||
                    !name.trim() ||
                    !endpoint.trim() ||
                    (selectedConnector.type !== "duckdb" && !credential.trim())
                  }
                  onClick={verify}
                >
                  {testState === "testing" ? <Loader2 className="animate-spin" /> : null}
                  Test connection
                </Button>
                <Button
                  type="button"
                  disabled={testState !== "verified"}
                  onClick={() =>
                    onCreate({
                      connector: selectedConnector,
                      name: name.trim(),
                      endpoint: endpoint.trim(),
                      database: database.trim(),
                    })
                  }
                >
                  Create and browse
                  <ChevronRight data-icon="inline-end" />
                </Button>
              </div>
            </DialogFooter>
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function createMockBrowserConnection(
  id: string,
  draft: WarehouseConnectionDraft,
): BrowserConnection {
  return {
    id,
    name: draft.name,
    type: draft.connector.label === "PostgreSQL" ? "Postgres" : draft.connector.label,
    accent: "bg-primary",
    discovery: {
      status: "discovering",
      lastRefreshed: "not yet",
      scope: `${draft.database || draft.endpoint} · metadata only`,
      detail: "Connection saved. Renart is discovering visible schemas and tables.",
    },
    schemas: [],
  };
}

function discoverMockConnection(connection: BrowserConnection): BrowserConnection {
  if (connection.type === "ClickHouse") {
    return {
      ...connection,
      discovery: {
        ...connection.discovery,
        status: "empty",
        lastRefreshed: "just now",
        detail: "The connection succeeded, but this role currently sees no tables.",
      },
    };
  }

  const schema = connection.type === "Snowflake" ? "ANALYTICS" : "public";
  return {
    ...connection,
    discovery: {
      ...connection.discovery,
      status: "ready",
      lastRefreshed: "just now",
      detail: undefined,
    },
    schemas: [
      {
        name: schema,
        tables: [
          {
            name: connection.type === "Snowflake" ? "ORDERS" : "orders",
            rows: "24k",
            freshness: "Observed just now",
            columns: [
              { name: "order_id", type: "bigint" },
              { name: "customer_id", type: "bigint" },
              { name: "ordered_at", type: "timestamp" },
              { name: "amount", type: "decimal(12,2)" },
            ],
          },
        ],
      },
    ],
  };
}

function LabSwitcher({ variant }: { variant: LabVariant }) {
  return (
    <div className="flex h-8 shrink-0 items-center gap-2 border-b bg-primary/[0.04] px-2 text-[10px] sm:px-3">
      <Badge variant="outline" size="xs">
        Navigation study
      </Badge>
      <span className="hidden truncate text-muted-foreground sm:inline">
        Frontend-only comparison · no workspace files are changed
      </span>
      <div className="ml-auto flex items-center gap-0.5 rounded-md border bg-background p-0.5">
        {(Object.keys(variantMeta) as LabVariant[]).map((key) => (
          <Button
            key={key}
            asChild
            size="xs"
            variant={key === variant ? "secondary" : "ghost"}
            className="h-5 px-1.5"
          >
            <Link to="/navigation-lab/$variant" params={{ variant: key }}>
              <span className="font-semibold">{variantMeta[key].short}</span>
              <span className="hidden lg:inline">{variantMeta[key].label}</span>
            </Link>
          </Button>
        ))}
      </div>
    </div>
  );
}

function LabHeader({
  area,
  environment,
  onEnvironmentChange,
  onAreaChange,
  onProjects,
  onMobileNavigation,
  onMessage,
}: {
  area: LabArea;
  environment: string;
  onEnvironmentChange: (environment: string) => void;
  onAreaChange: (area: LabArea) => void;
  onProjects: () => void;
  onMobileNavigation?: () => void;
  onMessage: (message: string) => void;
}) {
  return (
    <header className="flex h-12 shrink-0 items-center border-b border-zinc-800 bg-zinc-950 px-2 text-zinc-100 sm:px-3">
      {onMobileNavigation ? (
        <Button
          className="mr-1 text-zinc-300 hover:bg-zinc-800 hover:text-white md:hidden"
          variant="ghost"
          size="icon-sm"
          aria-label="Open navigation"
          onClick={onMobileNavigation}
        >
          <Menu />
        </Button>
      ) : null}
      <div className="flex items-center gap-2 pr-2">
        <img src="/icons/icon.svg" alt="" aria-hidden className="size-7 rounded-lg" />
        <span className="hidden font-semibold tracking-tight sm:inline">renart</span>
      </div>
      <ProjectMenu onProjects={onProjects} />

      <nav aria-label="Primary navigation" className="ml-3 hidden h-full items-center md:flex">
        {(
          [
            { area: "build", label: "Build", icon: Hammer },
            { area: "operate", label: "Run", icon: Play },
            { area: "explore", label: "Explore", icon: Compass },
          ] satisfies Array<{ area: LabArea; label: string; icon: LucideIcon }>
        ).map((item) => {
          const active =
            item.area === "build" ? area === "build" || area === "notebooks" : area === item.area;
          return (
            <button
              key={item.area}
              type="button"
              className={cn(
                "relative flex h-full items-center gap-1.5 px-3 text-xs text-zinc-400 transition hover:text-zinc-100",
                active &&
                  "text-white after:absolute after:inset-x-2 after:bottom-0 after:h-0.5 after:rounded-full after:bg-emerald-400",
              )}
              onClick={() => onAreaChange(item.area)}
            >
              <item.icon className="size-3.5" />
              {item.label}
            </button>
          );
        })}
      </nav>

      <div className="flex-1" />
      <Button
        variant="ghost"
        size="sm"
        className="hidden max-w-52 text-zinc-400 hover:bg-zinc-800 hover:text-white lg:flex"
        onClick={() => onMessage("Command palette opened")}
      >
        <Search data-icon="inline-start" />
        Search
        <span className="ml-4 rounded border border-zinc-700 px-1 text-[9px]">⌘ K</span>
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            className="mx-1 border-zinc-700 bg-zinc-950 text-zinc-200 hover:bg-zinc-800 hover:text-white"
            variant="outline"
            size="sm"
          >
            <span
              className={cn(
                "size-2 rounded-full",
                environment === "production"
                  ? "bg-red-400"
                  : environment === "staging"
                    ? "bg-amber-400"
                    : "bg-emerald-400",
              )}
            />
            <span className="hidden sm:inline">{environment}</span>
            <ChevronDown data-icon="inline-end" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-48">
          <DropdownMenuLabel>Execution environment</DropdownMenuLabel>
          {[
            ["default", "Interactive"],
            ["staging", "Shared validation"],
            ["production", "Protected · deployed only"],
          ].map(([name, detail]) => (
            <DropdownMenuItem key={name} onSelect={() => onEnvironmentChange(name)}>
              <span
                className={cn(
                  "size-2 rounded-full",
                  name === "production"
                    ? "bg-red-400"
                    : name === "staging"
                      ? "bg-amber-400"
                      : "bg-emerald-400",
                )}
              />
              <span className="min-w-0 flex-1">
                <span className="block capitalize">{name}</span>
                <span className="block truncate text-[9px] text-muted-foreground">{detail}</span>
              </span>
              {environment === name ? <Check /> : null}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
      <Button
        className="text-zinc-300 hover:bg-zinc-800 hover:text-white"
        variant="ghost"
        size="sm"
        onClick={() => onMessage("Source control opened")}
      >
        <GitBranch data-icon="inline-start" />
        <span className="hidden sm:inline">main</span>
        <Badge className="border-zinc-700 bg-zinc-800 text-zinc-300" variant="outline" size="xs">
          2
        </Badge>
      </Button>
    </header>
  );
}

function ProjectMenu({ onProjects }: { onProjects: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="sm"
          className="max-w-56 text-zinc-200 hover:bg-zinc-800 hover:text-white"
        >
          <FolderGit2 data-icon="inline-start" />
          <span className="truncate">Growth data platform</span>
          <ChevronDown data-icon="inline-end" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent className="w-64">
        <DropdownMenuLabel>Projects</DropdownMenuLabel>
        {[
          ["Growth data platform", "3 pipelines · current"],
          ["Finance analytics", "4 pipelines"],
          ["Product telemetry", "2 pipelines"],
        ].map(([name, detail], index) => (
          <DropdownMenuItem key={name} onSelect={index === 0 ? undefined : () => {}}>
            <span className="flex size-7 items-center justify-center rounded-md bg-primary/10 text-primary">
              <Boxes className="size-3.5" />
            </span>
            <span className="min-w-0 flex-1">
              <span className="block truncate">{name}</span>
              <span className="block text-[9px] text-muted-foreground">{detail}</span>
            </span>
            {index === 0 ? <Check /> : null}
          </DropdownMenuItem>
        ))}
        <DropdownMenuSeparator />
        <DropdownMenuItem onSelect={onProjects}>
          <LayoutDashboard />
          All projects
          <ArrowDownToLine className="ml-auto rotate-[-90deg]" />
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

type LabSidebarProps = {
  area: LabArea;
  buildView: BuildView;
  operateView: OperateView;
  exploreView: ExploreView;
  selectedAssetId: string;
  connections: BrowserConnection[];
  selectedConnection: BrowserConnection;
  selectedSchemaName?: string;
  selectedTable?: BrowserTable;
  pinnedTables: string[];
  recentTables: string[];
  dataBrowserLevel: DataBrowserLevel;
  wideSidebarOpen: boolean;
  utilityPane: UtilityPane;
  settingsView: SettingsView;
  settingsSection: SettingsSection;
  environment: string;
  onSelectArea: (area: LabArea) => void;
  onOpenContext: () => void;
  onBuildViewChange: (view: BuildView) => void;
  onOperateViewChange: (view: OperateView) => void;
  onExploreViewChange: (view: ExploreView) => void;
  onAssetSelect: (assetId: string) => void;
  onDragStart: (palette: PaletteAsset) => void;
  onOpenNotebook: () => void;
  onOpenData: () => void;
  onBackToConnections: () => void;
  onSidebarOpenChange: (open: boolean) => void;
  onAddConnection: () => void;
  onOpenSettings: (view: SettingsView) => void;
  onSettingsSectionChange: (section: SettingsSection) => void;
  onEnvironmentChange: (environment: string) => void;
  onChooseConnection: (connection: BrowserConnection) => void;
  onChooseTable: (connection: BrowserConnection, schema: string, table: BrowserTable) => void;
  onRefreshConnection: (connectionId: string) => void;
  onTogglePinnedTable: (connection: BrowserConnection, schema: string, table: BrowserTable) => void;
  onMessage: (message: string) => void;
};

function WorkbenchRail(props: LabSidebarProps) {
  const buildMode = props.area === "build" || props.area === "notebooks";
  const selectTool = (active: boolean, onSelect: () => void) => {
    if (active) {
      props.onSidebarOpenChange(!props.wideSidebarOpen);
      return;
    }
    onSelect();
    props.onSidebarOpenChange(true);
  };

  return (
    <aside
      className={cn(
        "flex w-14 shrink-0 flex-col items-center bg-card py-2",
        props.wideSidebarOpen && "border-r",
      )}
    >
      <div className="flex flex-col gap-1">
        {buildMode ? (
          <>
            <RailButton
              label="Project resources"
              icon={PanelLeft}
              active={
                props.area === "build" &&
                props.utilityPane === "context" &&
                props.buildView !== "adhoc"
              }
              onClick={() =>
                selectTool(
                  props.area === "build" &&
                    props.utilityPane === "context" &&
                    props.buildView !== "adhoc",
                  props.onOpenContext,
                )
              }
            />
            <RailButton
              label="Ad-hoc query"
              icon={TerminalSquare}
              active={props.area === "build" && props.buildView === "adhoc"}
              onClick={() =>
                selectTool(props.area === "build" && props.buildView === "adhoc", () =>
                  props.onBuildViewChange("adhoc"),
                )
              }
            />
            <RailButton
              label="Notebooks"
              icon={BookOpen}
              active={props.area === "notebooks"}
              onClick={() => selectTool(props.area === "notebooks", props.onOpenNotebook)}
            />
            <RailButton
              label="Data Browser"
              icon={Database}
              active={props.utilityPane === "data"}
              onClick={() => selectTool(props.utilityPane === "data", props.onOpenData)}
            />
            <RailButton
              label="Connections"
              icon={Cable}
              active={props.utilityPane === "settings" && props.settingsView === "connections"}
              onClick={() =>
                selectTool(
                  props.utilityPane === "settings" && props.settingsView === "connections",
                  () => props.onOpenSettings("connections"),
                )
              }
            />
            <RailButton
              label="Environments"
              icon={CircleDot}
              active={props.utilityPane === "settings" && props.settingsView === "environments"}
              onClick={() =>
                selectTool(
                  props.utilityPane === "settings" && props.settingsView === "environments",
                  () => props.onOpenSettings("environments"),
                )
              }
            />
            <RailButton
              label="Pipeline settings"
              icon={SlidersHorizontal}
              active={props.utilityPane === "settings" && props.settingsView === "pipeline"}
              onClick={() =>
                selectTool(
                  props.utilityPane === "settings" && props.settingsView === "pipeline",
                  () => props.onOpenSettings("pipeline"),
                )
              }
            />
          </>
        ) : props.area === "operate" ? (
          <>
            <RailButton
              label="Run overview"
              icon={Activity}
              active={props.utilityPane === "context" && props.operateView === "overview"}
              onClick={() =>
                selectTool(
                  props.utilityPane === "context" && props.operateView === "overview",
                  () => props.onOperateViewChange("overview"),
                )
              }
            />
            <RailButton
              label="Deployments"
              icon={Rocket}
              active={props.utilityPane === "context" && props.operateView === "deployments"}
              onClick={() =>
                selectTool(
                  props.utilityPane === "context" && props.operateView === "deployments",
                  () => props.onOperateViewChange("deployments"),
                )
              }
            />
            <RailButton
              label="Schedules"
              icon={CalendarClock}
              active={props.utilityPane === "context" && props.operateView === "schedules"}
              onClick={() =>
                selectTool(
                  props.utilityPane === "context" && props.operateView === "schedules",
                  () => props.onOperateViewChange("schedules"),
                )
              }
            />
            <RailButton
              label="Runs"
              icon={History}
              active={
                props.utilityPane === "context" &&
                (props.operateView === "runs" || props.operateView === "run-detail")
              }
              onClick={() =>
                selectTool(
                  props.utilityPane === "context" &&
                    (props.operateView === "runs" || props.operateView === "run-detail"),
                  () => props.onOperateViewChange("runs"),
                )
              }
            />
          </>
        ) : (
          <>
            <RailButton
              label="Workspace Catalog"
              icon={Network}
              active={props.utilityPane === "context" && props.exploreView === "catalog"}
              onClick={() =>
                selectTool(props.utilityPane === "context" && props.exploreView === "catalog", () =>
                  props.onExploreViewChange("catalog"),
                )
              }
            />
            <RailButton
              label="Dashboards"
              icon={LayoutDashboard}
              active={props.utilityPane === "context" && props.exploreView === "dashboards"}
              onClick={() =>
                selectTool(
                  props.utilityPane === "context" && props.exploreView === "dashboards",
                  () => props.onExploreViewChange("dashboards"),
                )
              }
            />
            <RailButton
              label="Reports"
              icon={FileText}
              active={props.utilityPane === "context" && props.exploreView === "reports"}
              onClick={() =>
                selectTool(props.utilityPane === "context" && props.exploreView === "reports", () =>
                  props.onExploreViewChange("reports"),
                )
              }
            />
            <RailButton
              label="Data Browser"
              icon={Database}
              active={props.utilityPane === "data"}
              onClick={() => selectTool(props.utilityPane === "data", props.onOpenData)}
            />
          </>
        )}
      </div>
      <div className="mt-auto flex flex-col gap-1">
        <RailButton
          label="Project settings"
          icon={Settings2}
          active={props.utilityPane === "settings" && props.settingsView === "project"}
          onClick={() =>
            selectTool(props.utilityPane === "settings" && props.settingsView === "project", () =>
              props.onOpenSettings("project"),
            )
          }
        />
      </div>
    </aside>
  );
}

type MobileWorkbenchTool = {
  id: string;
  label: string;
  icon: LucideIcon;
  active: boolean;
  context?: boolean;
  onSelect: () => void;
};

function MobileWorkbenchToolTabs({
  mobileNavigationOpen,
  onMobileNavigationOpenChange,
  ...props
}: LabSidebarProps & {
  mobileNavigationOpen: boolean;
  onMobileNavigationOpenChange: (open: boolean) => void;
}) {
  const buildMode = props.area === "build" || props.area === "notebooks";
  const modeLabel = buildMode ? "Build" : props.area === "operate" ? "Run" : "Explore";
  const tools: MobileWorkbenchTool[] = buildMode
    ? [
        {
          id: "resources",
          label: "Resources",
          icon: PanelLeft,
          active:
            props.area === "build" &&
            props.utilityPane === "context" &&
            props.buildView !== "adhoc",
          context: true,
          onSelect: props.onOpenContext,
        },
        {
          id: "adhoc",
          label: "Query",
          icon: TerminalSquare,
          active: props.area === "build" && props.buildView === "adhoc",
          onSelect: () => props.onBuildViewChange("adhoc"),
        },
        {
          id: "notebooks",
          label: "Notebooks",
          icon: BookOpen,
          active: props.area === "notebooks",
          onSelect: props.onOpenNotebook,
        },
        {
          id: "data",
          label: "Data",
          icon: Database,
          active: props.utilityPane === "data",
          onSelect: props.onOpenData,
        },
        {
          id: "connections",
          label: "Connections",
          icon: Cable,
          active: props.utilityPane === "settings" && props.settingsView === "connections",
          onSelect: () => props.onOpenSettings("connections"),
        },
        {
          id: "environments",
          label: "Environments",
          icon: CircleDot,
          active: props.utilityPane === "settings" && props.settingsView === "environments",
          onSelect: () => props.onOpenSettings("environments"),
        },
        {
          id: "pipeline",
          label: "Pipeline",
          icon: SlidersHorizontal,
          active: props.utilityPane === "settings" && props.settingsView === "pipeline",
          onSelect: () => props.onOpenSettings("pipeline"),
        },
        {
          id: "project",
          label: "Project",
          icon: Settings2,
          active: props.utilityPane === "settings" && props.settingsView === "project",
          onSelect: () => props.onOpenSettings("project"),
        },
      ]
    : props.area === "operate"
      ? [
          {
            id: "overview",
            label: "Overview",
            icon: Activity,
            active: props.utilityPane === "context" && props.operateView === "overview",
            context: true,
            onSelect: () => props.onOperateViewChange("overview"),
          },
          {
            id: "deployments",
            label: "Deployments",
            icon: Rocket,
            active: props.utilityPane === "context" && props.operateView === "deployments",
            context: true,
            onSelect: () => props.onOperateViewChange("deployments"),
          },
          {
            id: "schedules",
            label: "Schedules",
            icon: CalendarClock,
            active: props.utilityPane === "context" && props.operateView === "schedules",
            context: true,
            onSelect: () => props.onOperateViewChange("schedules"),
          },
          {
            id: "runs",
            label: "Runs",
            icon: History,
            active:
              props.utilityPane === "context" &&
              (props.operateView === "runs" || props.operateView === "run-detail"),
            context: true,
            onSelect: () => props.onOperateViewChange("runs"),
          },
          {
            id: "project",
            label: "Project",
            icon: Settings2,
            active: props.utilityPane === "settings" && props.settingsView === "project",
            onSelect: () => props.onOpenSettings("project"),
          },
        ]
      : [
          {
            id: "catalog",
            label: "Catalog",
            icon: Network,
            active: props.utilityPane === "context" && props.exploreView === "catalog",
            context: true,
            onSelect: () => props.onExploreViewChange("catalog"),
          },
          {
            id: "dashboards",
            label: "Dashboards",
            icon: LayoutDashboard,
            active: props.utilityPane === "context" && props.exploreView === "dashboards",
            context: true,
            onSelect: () => props.onExploreViewChange("dashboards"),
          },
          {
            id: "reports",
            label: "Reports",
            icon: FileText,
            active: props.utilityPane === "context" && props.exploreView === "reports",
            context: true,
            onSelect: () => props.onExploreViewChange("reports"),
          },
          {
            id: "data",
            label: "Data",
            icon: Database,
            active: props.utilityPane === "data",
            onSelect: props.onOpenData,
          },
          {
            id: "project",
            label: "Project",
            icon: Settings2,
            active: props.utilityPane === "settings" && props.settingsView === "project",
            onSelect: () => props.onOpenSettings("project"),
          },
        ];
  const activeToolId = tools.find((tool) => tool.active)?.id;
  const activeToolRef = useRef<HTMLButtonElement | null>(null);

  useEffect(() => {
    activeToolRef.current?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [activeToolId]);

  const selectTool = (tool: MobileWorkbenchTool) => {
    if (tool.context && tool.active) {
      onMobileNavigationOpenChange(!mobileNavigationOpen);
      return;
    }
    tool.onSelect();
    if (tool.context && tool.id === "resources") {
      onMobileNavigationOpenChange(true);
      return;
    }
    if (tool.id === "data" && props.area === "explore") {
      onMobileNavigationOpenChange(true);
    }
  };

  return (
    <nav
      aria-label={`${modeLabel} tools`}
      className="no-scrollbar flex h-10 shrink-0 overflow-x-auto border-b bg-background px-1 md:hidden"
    >
      {tools.map((tool) => (
        <button
          key={tool.id}
          ref={tool.active ? activeToolRef : undefined}
          type="button"
          aria-current={tool.active ? "page" : undefined}
          className={cn(
            "relative flex h-full shrink-0 items-center gap-1.5 px-3 text-[11px] text-muted-foreground transition-colors hover:text-foreground",
            tool.active &&
              "font-medium text-foreground after:absolute after:inset-x-2 after:bottom-0 after:h-0.5 after:rounded-full after:bg-primary",
          )}
          onClick={() => selectTool(tool)}
        >
          <tool.icon className="size-3.5" />
          <span>{tool.label}</span>
        </button>
      ))}
    </nav>
  );
}

function RailButton({
  label,
  icon: Icon,
  active,
  onClick,
}: {
  label: string;
  icon: LucideIcon;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          className={cn(
            "relative size-10",
            active && "bg-primary/10 text-primary hover:bg-primary/15",
          )}
          variant="ghost"
          size="icon-lg"
          aria-label={label}
          onClick={onClick}
        >
          <Icon />
          {active ? <span className="absolute -left-2 h-5 w-0.5 rounded-r bg-primary" /> : null}
        </Button>
      </TooltipTrigger>
      <TooltipContent side="right">{label}</TooltipContent>
    </Tooltip>
  );
}

function DesktopSidebar({
  variant,
  embedded = false,
  ...props
}: LabSidebarProps & { variant: LabVariant; embedded?: boolean }) {
  return (
    <aside className={cn("hidden w-72 shrink-0 bg-card md:block", !embedded && "border-r")}>
      {props.utilityPane === "data" ? (
        <DataBrowserSidebar {...props} />
      ) : props.utilityPane === "settings" ? (
        variant === "workbench" ? (
          <WorkbenchSettingsSidebar {...props} />
        ) : (
          <SettingsSidebar {...props} />
        )
      ) : (
        <ContextSidebar variant={variant} {...props} />
      )}
    </aside>
  );
}

function ContextSidebar({ variant, ...props }: LabSidebarProps & { variant: LabVariant }) {
  if (props.area === "operate")
    return variant === "workbench" ? (
      <OperateSidebar {...props} />
    ) : (
      <RunNavigationSidebar {...props} />
    );
  if (props.area === "explore")
    return variant === "workbench" ? (
      <ExploreSidebar {...props} />
    ) : (
      <ExploreNavigationSidebar {...props} />
    );
  if (props.area === "notebooks") return <NotebookSidebar {...props} />;
  return <BuildSidebar variant={variant} {...props} />;
}

function MobileWorkbenchContextSidebar(props: LabSidebarProps) {
  if (props.area === "operate") return <OperateSidebar {...props} />;
  if (props.area === "explore") return <ExploreSidebar {...props} />;
  if (props.area === "notebooks") return <NotebookSidebar {...props} />;
  return <BuildSidebar variant="workbench" {...props} />;
}

function SidebarFrame({
  title,
  subtitle,
  actions,
  children,
}: {
  title: string;
  subtitle?: string;
  actions?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div
        data-slot="navigation-lab-sidebar-header"
        className="flex min-h-12 shrink-0 items-center gap-2 border-b px-3 pr-12"
      >
        <div className="min-w-0 flex-1">
          <p className="truncate text-xs font-semibold">{title}</p>
          {subtitle ? (
            <p className="truncate text-[9px] text-muted-foreground">{subtitle}</p>
          ) : null}
        </div>
        {actions}
      </div>
      <ScrollArea className="min-h-0 flex-1" showHorizontalScrollBar={false}>
        {children}
      </ScrollArea>
    </div>
  );
}

function BuildSidebar({ variant, ...props }: LabSidebarProps & { variant: LabVariant }) {
  return (
    <SidebarFrame
      title="Build"
      subtitle="growth / revenue-model"
      actions={
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="Create asset"
          onClick={() => props.onMessage("Create asset dialog opened")}
        >
          <Plus />
        </Button>
      }
    >
      {variant === "lifecycle" ? (
        <div className="grid grid-cols-2 gap-1 border-b p-2">
          <Button variant="secondary" size="sm">
            <FolderGit2 data-icon="inline-start" />
            Project
          </Button>
          <Button variant="ghost" size="sm" onClick={props.onOpenData}>
            <Database data-icon="inline-start" />
            Data
          </Button>
        </div>
      ) : null}
      <div className="flex flex-col gap-4 p-2">
        <SidebarSection label="Pipeline">
          <TreeItem
            icon={Boxes}
            label="revenue-model"
            active={props.buildView === "canvas"}
            onClick={() => props.onBuildViewChange("canvas")}
            trailing={
              <Badge variant="muted" size="xs">
                6
              </Badge>
            }
          />
          <div className="ml-3 border-l pl-2">
            {[
              ["source-accounts", "raw.accounts", Table2],
              ["seed-regions", "raw.regions", FileText],
              ["stg-accounts", "staging.accounts", FileCode2],
              ["stg-subscriptions", "staging.subscriptions", Code2],
              ["mart-health", "analytics.customer_health", FileCode2],
              ["mart-retention", "analytics.retention_daily", FileCode2],
            ].map(([id, label, Icon]) => (
              <TreeItem
                key={id as string}
                icon={Icon as LucideIcon}
                label={label as string}
                active={props.selectedAssetId === id}
                onClick={() => props.onAssetSelect(id as string)}
                compact
              />
            ))}
          </div>
        </SidebarSection>
        {variant !== "workbench" ? (
          <SidebarSection label="Build tools">
            <TreeItem
              icon={TerminalSquare}
              label="Ad-hoc query"
              active={props.buildView === "adhoc"}
              onClick={() => props.onBuildViewChange("adhoc")}
              trailing={
                <Badge variant="outline" size="xs">
                  local
                </Badge>
              }
            />
            <TreeItem
              icon={BookOpen}
              label="Notebooks"
              active={props.area === "notebooks"}
              onClick={props.onOpenNotebook}
              trailing={
                <Badge variant="muted" size="xs">
                  4
                </Badge>
              }
            />
          </SidebarSection>
        ) : null}
        <SidebarSection label="Add to canvas" description="Drag onto a valid target">
          <PaletteGrid onDragStart={props.onDragStart} />
        </SidebarSection>
        {variant !== "workbench" ? (
          <SidebarSection label="Project utilities">
            <TreeItem icon={Database} label="Data Browser" onClick={props.onOpenData} />
            <TreeItem
              icon={Cable}
              label="Connections"
              onClick={() => props.onOpenSettings("connections")}
            />
            <TreeItem
              icon={CircleDot}
              label="Environments"
              onClick={() => props.onOpenSettings("environments")}
            />
            <TreeItem
              icon={SlidersHorizontal}
              label="Pipeline settings"
              onClick={() => props.onOpenSettings("pipeline")}
            />
            <TreeItem
              icon={Settings2}
              label="Project settings"
              onClick={() => props.onOpenSettings("project")}
            />
          </SidebarSection>
        ) : null}
      </div>
    </SidebarFrame>
  );
}

function RunNavigationSidebar(props: LabSidebarProps) {
  const items: Array<{ view: OperateView; label: string; icon: LucideIcon; count: string }> = [
    { view: "overview", label: "Overview", icon: Activity, count: "2" },
    { view: "deployments", label: "Deployments", icon: Rocket, count: "v42" },
    { view: "schedules", label: "Schedules", icon: CalendarClock, count: "3" },
    { view: "runs", label: "Runs", icon: History, count: "32" },
  ];
  return (
    <SidebarFrame title="Run" subtitle="Deploy, schedule, and observe">
      <div className="flex flex-col gap-4 p-2">
        <SidebarSection label="Project operations">
          {items.map((item) => (
            <TreeItem
              key={item.view}
              icon={item.icon}
              label={item.label}
              active={
                props.operateView === item.view ||
                (item.view === "runs" && props.operateView === "run-detail")
              }
              onClick={() => props.onOperateViewChange(item.view)}
              trailing={
                <Badge variant={item.view === "overview" ? "destructive" : "muted"} size="xs">
                  {item.count}
                </Badge>
              }
            />
          ))}
        </SidebarSection>
        <SidebarSection label="Current environment">
          <div className="rounded-lg border bg-muted/30 p-3 text-xs">
            <p className="font-medium">production · protected</p>
            <p className="mt-1 text-[10px] text-muted-foreground">
              Deployment v42 · 2 schedule bindings
            </p>
          </div>
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function ExploreNavigationSidebar(props: LabSidebarProps) {
  return (
    <SidebarFrame title="Explore" subtitle="Authored artifacts and observed data">
      <div className="flex flex-col gap-4 p-2">
        <SidebarSection label="Workspace">
          <TreeItem
            icon={Network}
            label="Workspace Catalog"
            active={props.exploreView === "catalog" && props.utilityPane === "context"}
            onClick={() => props.onExploreViewChange("catalog")}
            trailing={
              <Badge variant="muted" size="xs">
                39
              </Badge>
            }
          />
          <TreeItem
            icon={LayoutDashboard}
            label="Dashboards"
            active={props.exploreView === "dashboards" && props.utilityPane === "context"}
            onClick={() => props.onExploreViewChange("dashboards")}
            trailing={
              <Badge variant="muted" size="xs">
                4
              </Badge>
            }
          />
          <TreeItem
            icon={FileText}
            label="Reports"
            active={props.exploreView === "reports" && props.utilityPane === "context"}
            onClick={() => props.onExploreViewChange("reports")}
            trailing={
              <Badge variant="muted" size="xs">
                3
              </Badge>
            }
          />
        </SidebarSection>
        <SidebarSection label="Live systems">
          <TreeItem
            icon={Database}
            label="Data Browser"
            active={props.utilityPane === "data"}
            onClick={props.onOpenData}
            trailing={<ChevronRight />}
          />
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function OperateSidebar(props: LabSidebarProps) {
  if (props.operateView === "deployments") return <DeploymentsSidebar {...props} />;
  if (props.operateView === "schedules") return <SchedulesSidebar {...props} />;
  if (props.operateView === "runs" || props.operateView === "run-detail") {
    return <RunsSidebar {...props} />;
  }
  return (
    <SidebarFrame
      title="Run overview"
      subtitle="production · protected"
      actions={
        <Button size="icon-sm" variant="ghost" aria-label="Refresh operations">
          <Activity />
        </Button>
      }
    >
      <div className="flex flex-col gap-4 p-2">
        <SidebarSection label="Pipelines">
          <TreeItem
            icon={Boxes}
            label="revenue-model"
            active
            onClick={() => props.onMessage("Revenue run context opened")}
            trailing={
              <Badge variant="muted" size="xs">
                healthy
              </Badge>
            }
          />
          <TreeItem
            icon={Boxes}
            label="product-events"
            onClick={() => props.onMessage("Waiting run context opened")}
            trailing={
              <Badge variant="outline" size="xs">
                waiting
              </Badge>
            }
          />
          <TreeItem
            icon={Boxes}
            label="finance-close"
            onClick={() => props.onMessage("Failed run context opened")}
            trailing={
              <Badge variant="destructive" size="xs">
                failed
              </Badge>
            }
          />
        </SidebarSection>
        <SidebarSection label="Active context">
          <div className="rounded-lg border bg-muted/30 p-3 text-xs">
            <div className="flex items-center gap-2">
              <span className="size-2 rounded-full bg-emerald-500" />
              <span className="font-medium">Deployment v42</span>
            </div>
            <p className="mt-1 text-[10px] text-muted-foreground">
              main · 8f01b4a · 2 schedule bindings
            </p>
          </div>
        </SidebarSection>
        <SidebarSection label="Quick actions">
          <Button
            variant="outline"
            className="w-full justify-start"
            onClick={() => props.onMessage("Manual run review opened")}
          >
            <Play data-icon="inline-start" />
            Review manual run
          </Button>
          <Button
            variant="outline"
            className="w-full justify-start"
            onClick={() => props.onMessage("Deployment comparison opened")}
          >
            <Rocket data-icon="inline-start" />
            Compare deployment
          </Button>
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function DeploymentsSidebar(props: LabSidebarProps) {
  return (
    <SidebarFrame
      title="Deployments"
      subtitle="Reviewed, immutable source snapshots"
      actions={
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="Create deployment"
          onClick={() => props.onMessage("New deployment review opened")}
        >
          <Plus />
        </Button>
      }
    >
      <div className="flex flex-col gap-4 p-2">
        <SidebarSection label="revenue-model">
          <TreeItem
            icon={Rocket}
            label="v42 · main"
            active
            onClick={() => props.onMessage("Deployment v42 opened")}
            trailing={
              <Badge variant="muted" size="xs">
                active
              </Badge>
            }
          />
          <TreeItem
            icon={Rocket}
            label="v41 · main"
            onClick={() => props.onMessage("Deployment v41 opened")}
            trailing={
              <Badge variant="outline" size="xs">
                2 days
              </Badge>
            }
          />
          <TreeItem
            icon={Rocket}
            label="v40 · main"
            onClick={() => props.onMessage("Deployment v40 opened")}
            trailing={
              <Badge variant="outline" size="xs">
                5 days
              </Badge>
            }
          />
        </SidebarSection>
        <SidebarSection label="Other pipelines">
          <TreeItem
            icon={Rocket}
            label="product-events · v18"
            onClick={() => props.onMessage("Product deployment opened")}
          />
          <TreeItem
            icon={Rocket}
            label="finance-close · v9"
            onClick={() => props.onMessage("Finance deployment opened")}
          />
        </SidebarSection>
        <SidebarSection label="Review">
          <TreeItem
            icon={GitBranch}
            label="Compare workspace with v42"
            onClick={() => props.onMessage("Deployment comparison opened")}
            trailing={
              <Badge variant="outline" size="xs">
                3 files
              </Badge>
            }
          />
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function SchedulesSidebar(props: LabSidebarProps) {
  return (
    <SidebarFrame
      title="Schedules"
      subtitle="Desired cadence and deployment bindings"
      actions={
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="Create schedule"
          onClick={() => props.onMessage("New schedule dialog opened")}
        >
          <Plus />
        </Button>
      }
    >
      <div className="flex flex-col gap-4 p-2">
        <SidebarSection label="Enabled">
          <TreeItem
            icon={CalendarClock}
            label="revenue-hourly"
            active
            onClick={() => props.onMessage("Revenue schedule opened")}
            trailing={
              <Badge variant="muted" size="xs">
                18m
              </Badge>
            }
          />
          <TreeItem
            icon={CalendarClock}
            label="daily-health"
            onClick={() => props.onMessage("Daily schedule opened")}
            trailing={
              <Badge variant="muted" size="xs">
                06:15
              </Badge>
            }
          />
        </SidebarSection>
        <SidebarSection label="Needs attention">
          <TreeItem
            icon={CalendarClock}
            label="product-events"
            onClick={() => props.onMessage("Waiting schedule opened")}
            trailing={
              <Badge variant="outline" size="xs">
                waiting
              </Badge>
            }
          />
        </SidebarSection>
        <SidebarSection label="Binding context">
          <div className="rounded-lg border bg-muted/30 p-3 text-xs">
            <p className="font-medium">production · Europe/Berlin</p>
            <p className="mt-1 text-[10px] text-muted-foreground">
              revenue-model is pinned to deployment v42.
            </p>
          </div>
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function RunsSidebar(props: LabSidebarProps) {
  return (
    <SidebarFrame
      title="Runs"
      subtitle="Scheduled, manual, backfill, and sensor runs"
      actions={
        <Button size="icon-sm" variant="ghost" aria-label="Refresh runs">
          <Activity />
        </Button>
      }
    >
      <div className="flex flex-col gap-4 p-2">
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input className="pl-8" placeholder="Filter runs" />
        </div>
        <SidebarSection label="Status">
          <TreeItem
            icon={History}
            label="All runs"
            active={props.operateView === "runs"}
            onClick={() => props.onOperateViewChange("runs")}
            trailing={
              <Badge variant="muted" size="xs">
                32
              </Badge>
            }
          />
          <TreeItem
            icon={Activity}
            label="Running or queued"
            onClick={() => props.onMessage("Active runs filtered")}
            trailing={
              <Badge variant="outline" size="xs">
                1
              </Badge>
            }
          />
          <TreeItem
            icon={CircleDot}
            label="Failed"
            onClick={() => props.onMessage("Failed runs filtered")}
            trailing={
              <Badge variant="destructive" size="xs">
                1
              </Badge>
            }
          />
        </SidebarSection>
        <SidebarSection label="Recent">
          <TreeItem
            icon={Play}
            label="revenue-model · 14:00"
            active={props.operateView === "run-detail"}
            onClick={() => props.onOperateViewChange("run-detail")}
            compact
          />
          <TreeItem
            icon={Play}
            label="finance-close · 12:02"
            onClick={() => props.onOperateViewChange("run-detail")}
            compact
          />
          <TreeItem
            icon={Play}
            label="product-events · 10:00"
            onClick={() => props.onMessage("Product run opened")}
            compact
          />
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function ExploreSidebar(props: LabSidebarProps) {
  if (props.exploreView === "dashboards") return <DashboardsSidebar {...props} />;
  if (props.exploreView === "reports") return <ReportsSidebar {...props} />;
  return (
    <SidebarFrame title="Workspace Catalog" subtitle="Authored artifacts and lineage">
      <div className="flex flex-col gap-4 p-2">
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input className="pl-8" placeholder="Search the catalog" />
        </div>
        <SidebarSection label="Artifact types">
          <TreeItem
            icon={Network}
            label="Pipeline assets"
            active
            onClick={() => props.onMessage("Pipeline assets filtered")}
            trailing={
              <Badge variant="muted" size="xs">
                11
              </Badge>
            }
          />
          <TreeItem
            icon={BookOpen}
            label="Notebooks"
            onClick={() => props.onMessage("Notebooks filtered")}
            trailing={
              <Badge variant="muted" size="xs">
                4
              </Badge>
            }
          />
          <TreeItem
            icon={Table2}
            label="Datasets"
            onClick={() => props.onMessage("Datasets filtered")}
            trailing={
              <Badge variant="muted" size="xs">
                5
              </Badge>
            }
          />
          <TreeItem
            icon={LayoutDashboard}
            label="Visualizations"
            onClick={() => props.onMessage("Visualizations filtered")}
            trailing={
              <Badge variant="muted" size="xs">
                9
              </Badge>
            }
          />
        </SidebarSection>
        <SidebarSection label="Lineage groups">
          <TreeItem
            icon={Boxes}
            label="revenue-model"
            onClick={() => props.onMessage("Revenue lineage selected")}
          />
          <TreeItem
            icon={Boxes}
            label="product-events"
            onClick={() => props.onMessage("Product lineage selected")}
          />
          <TreeItem
            icon={Boxes}
            label="finance-close"
            onClick={() => props.onMessage("Finance lineage selected")}
          />
        </SidebarSection>
        <SidebarSection label="Recently viewed">
          <TreeItem
            icon={Table2}
            label="analytics.customer_health"
            onClick={() => props.onMessage("Catalog asset opened")}
            compact
          />
          <TreeItem
            icon={LayoutDashboard}
            label="Customer health"
            onClick={() => props.onExploreViewChange("dashboards")}
            compact
          />
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function DashboardsSidebar(props: LabSidebarProps) {
  return (
    <SidebarFrame
      title="Dashboards"
      subtitle="Interactive, type-checked presentations"
      actions={
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="New dashboard"
          onClick={() => props.onMessage("New dashboard opened")}
        >
          <Plus />
        </Button>
      }
    >
      <div className="flex flex-col gap-4 p-2">
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input className="pl-8" placeholder="Filter dashboards" />
        </div>
        <SidebarSection label="Published">
          <TreeItem
            icon={LayoutDashboard}
            label="Customer health"
            active
            onClick={() => props.onMessage("Customer health dashboard opened")}
            trailing={
              <Badge variant="muted" size="xs">
                valid
              </Badge>
            }
          />
          <TreeItem
            icon={LayoutDashboard}
            label="Revenue pulse"
            onClick={() => props.onMessage("Revenue dashboard opened")}
          />
          <TreeItem
            icon={LayoutDashboard}
            label="Product adoption"
            onClick={() => props.onMessage("Product dashboard opened")}
          />
        </SidebarSection>
        <SidebarSection label="Drafts">
          <TreeItem
            icon={LayoutDashboard}
            label="Retention experiments"
            onClick={() => props.onMessage("Draft dashboard opened")}
            trailing={
              <Badge variant="outline" size="xs">
                draft
              </Badge>
            }
          />
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function ReportsSidebar(props: LabSidebarProps) {
  return (
    <SidebarFrame
      title="Reports"
      subtitle="Narrative, version-controlled analysis"
      actions={
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="New report"
          onClick={() => props.onMessage("New report opened")}
        >
          <Plus />
        </Button>
      }
    >
      <div className="flex flex-col gap-4 p-2">
        <SidebarSection label="Published">
          <TreeItem
            icon={FileText}
            label="Weekly revenue review"
            active
            onClick={() => props.onMessage("Revenue report opened")}
            trailing={
              <Badge variant="muted" size="xs">
                valid
              </Badge>
            }
          />
          <TreeItem
            icon={FileText}
            label="Customer health brief"
            onClick={() => props.onMessage("Customer report opened")}
          />
        </SidebarSection>
        <SidebarSection label="Drafts">
          <TreeItem
            icon={FileText}
            label="Q3 planning"
            onClick={() => props.onMessage("Planning report opened")}
            trailing={
              <Badge variant="outline" size="xs">
                draft
              </Badge>
            }
          />
        </SidebarSection>
        <SidebarSection label="Sources">
          <TreeItem
            icon={Network}
            label="4 referenced assets"
            onClick={() => props.onMessage("Report dependencies opened")}
          />
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function NotebookSidebar(props: LabSidebarProps) {
  return (
    <SidebarFrame
      title="Notebooks"
      subtitle="Git-backed analysis documents"
      actions={
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="New notebook"
          onClick={() => props.onMessage("New notebook dialog opened")}
        >
          <Plus />
        </Button>
      }
    >
      <div className="flex flex-col gap-4 p-2">
        <Button
          className="-ml-1 self-start md:hidden"
          size="sm"
          variant="ghost"
          onClick={props.onOpenContext}
        >
          <ChevronLeft data-icon="inline-start" />
          Back to pipeline
        </Button>
        <SidebarSection label="Project notebooks">
          <TreeItem
            icon={BookOpen}
            label="Cohort explorer"
            active
            onClick={props.onOpenNotebook}
            trailing={
              <Badge variant="muted" size="xs">
                3 cells
              </Badge>
            }
          />
          <TreeItem icon={BookOpen} label="Revenue deep-dive" onClick={props.onOpenNotebook} />
          <TreeItem icon={BookOpen} label="Product adoption" onClick={props.onOpenNotebook} />
          <TreeItem icon={BookOpen} label="Scratch analysis" onClick={props.onOpenNotebook} />
        </SidebarSection>
        <SidebarSection label="Notebook tools">
          <TreeItem icon={Database} label="Add data source" onClick={props.onOpenData} />
          <TreeItem
            icon={Search}
            label="Search workspace catalog"
            onClick={() => props.onMessage("Catalog search opened")}
          />
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

type ResolvedBrowserTable = {
  key: string;
  connection: BrowserConnection;
  schema: BrowserSchema;
  table: BrowserTable;
};

type BrowserObjectFilter = "all" | "table" | "view" | "external";

function allBrowserTables(connections: BrowserConnection[]): ResolvedBrowserTable[] {
  return connections.flatMap((connection) =>
    connection.schemas.flatMap((schema) =>
      schema.tables.map((table) => ({
        key: browserTableKey(connection.id, schema.name, table.name),
        connection,
        schema,
        table,
      })),
    ),
  );
}

function qualifiedBrowserTable(
  connection: BrowserConnection,
  schema: BrowserSchema,
  table: BrowserTable,
) {
  return [schema.database, schema.name, table.name]
    .filter(Boolean)
    .join(connection.type === "Local files" ? "/" : ".");
}

function browserTableMatches(item: ResolvedBrowserTable, query: string) {
  if (!query) return true;
  return [
    item.connection.name,
    item.connection.type,
    item.schema.database,
    item.schema.name,
    item.table.name,
    item.table.description,
    ...item.table.columns.flatMap((column) => [
      column.name,
      column.type,
      column.description,
      ...(column.tags ?? []),
    ]),
  ].some((value) => value?.toLowerCase().includes(query));
}

function browserTableMatchesKind(table: BrowserTable, filter: BrowserObjectFilter) {
  if (filter === "all") return true;
  if (filter === "external") return table.kind === "external_table" || table.kind === "file";
  if (filter === "view") return table.kind === "view" || table.kind === "materialized_view";
  return !table.kind || table.kind === "table";
}

function BrowserObjectIcon({ kind, className }: { kind?: BrowserObjectKind; className?: string }) {
  if (kind === "file") {
    return <FileText className={cn("size-3 shrink-0", className)} />;
  }
  if (kind === "view" || kind === "materialized_view") {
    return <Eye className={cn("size-3 shrink-0", className)} />;
  }
  if (kind === "external_table") {
    return <Boxes className={cn("size-3 shrink-0", className)} />;
  }
  return <Table2 className={cn("size-3 shrink-0", className)} />;
}

function BrowserObjectRow({
  item,
  marker,
  onChoose,
}: {
  item: ResolvedBrowserTable;
  marker: "pinned" | "recent" | "search";
  onChoose: () => void;
}) {
  return (
    <button
      type="button"
      className="group flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-muted"
      onClick={onChoose}
    >
      {marker === "pinned" ? (
        <Pin className="size-3 shrink-0 text-primary" />
      ) : marker === "recent" ? (
        <History className="size-3 shrink-0 text-muted-foreground" />
      ) : (
        <BrowserObjectIcon kind={item.table.kind} className="text-muted-foreground" />
      )}
      <span className="min-w-0 flex-1">
        <span className="block truncate font-mono text-[10px]">
          {qualifiedBrowserTable(item.connection, item.schema, item.table)}
        </span>
        <span className="block truncate text-[9px] text-muted-foreground">
          {item.connection.name}
          {marker === "search" ? ` · ${item.table.columns.length} columns` : ""}
        </span>
      </span>
      <ConnectionTypeIcon connectionType={item.connection.type} className="size-4" />
    </button>
  );
}

function DataBrowserSidebar(props: LabSidebarProps) {
  const [query, setQuery] = useState("");
  const [objectFilter, setObjectFilter] = useState<BrowserObjectFilter>("all");
  const normalizedQuery = query.trim().toLowerCase();
  const allTables = allBrowserTables(props.connections);
  const tableByKey = new Map(allTables.map((item) => [item.key, item]));
  const pinned = props.pinnedTables.flatMap((key) => {
    const item = tableByKey.get(key);
    return item ? [item] : [];
  });
  const recent = props.recentTables.flatMap((key) => {
    const item = tableByKey.get(key);
    return item && !props.pinnedTables.includes(key) ? [item] : [];
  });
  const objectMatches = normalizedQuery
    ? allTables.filter((item) => browserTableMatches(item, normalizedQuery)).slice(0, 8)
    : [];
  const filteredConnections = props.connections.filter(
    (connection) =>
      !normalizedQuery ||
      connection.name.toLowerCase().includes(normalizedQuery) ||
      connection.type.toLowerCase().includes(normalizedQuery),
  );
  const filteredSchemas = props.selectedConnection.schemas
    .map((schema) => ({
      ...schema,
      tables: schema.tables.filter((table) => {
        const item = { connection: props.selectedConnection, schema, table, key: "" };
        return (
          browserTableMatchesKind(table, objectFilter) && browserTableMatches(item, normalizedQuery)
        );
      }),
    }))
    .filter((schema) => schema.tables.length > 0 || (!normalizedQuery && objectFilter === "all"));

  if (props.dataBrowserLevel === "connections") {
    return (
      <SidebarFrame
        title="Data Browser"
        subtitle="Warehouses, local files, and loaded metadata"
        actions={
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Add data warehouse"
            onClick={props.onAddConnection}
          >
            <Plus />
          </Button>
        }
      >
        <div className="flex flex-col gap-4 p-2">
          <Button
            className="-ml-1 self-start md:hidden"
            size="sm"
            variant="ghost"
            onClick={props.onOpenContext}
          >
            <ChevronLeft data-icon="inline-start" />
            Back to workspace
          </Button>
          <div>
            <div className="relative">
              <Search className="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                className="pl-8"
                placeholder="Search tables, files, or columns"
                value={query}
                onChange={(event) => setQuery(event.target.value)}
              />
            </div>
            <p className="mt-1 px-1 text-[9px] text-muted-foreground">
              Searches metadata already loaded in this project. No row query is run.
            </p>
          </div>
          {normalizedQuery ? (
            <SidebarSection label="Objects" description={`${objectMatches.length} loaded matches`}>
              {objectMatches.map((item) => (
                <BrowserObjectRow
                  key={item.key}
                  item={item}
                  marker="search"
                  onChoose={() =>
                    props.onChooseTable(item.connection, item.schema.name, item.table)
                  }
                />
              ))}
              {objectMatches.length === 0 ? (
                <p className="px-2 py-3 text-center text-[10px] text-muted-foreground">
                  No loaded table or column matches. Open a connection to refresh its scope.
                </p>
              ) : null}
            </SidebarSection>
          ) : (
            <>
              {pinned.length > 0 ? (
                <SidebarSection label="Pinned" description="Project session">
                  {pinned.map((item) => (
                    <BrowserObjectRow
                      key={item.key}
                      item={item}
                      marker="pinned"
                      onChoose={() =>
                        props.onChooseTable(item.connection, item.schema.name, item.table)
                      }
                    />
                  ))}
                </SidebarSection>
              ) : null}
              {recent.length > 0 ? (
                <SidebarSection label="Recent">
                  {recent.slice(0, 4).map((item) => (
                    <BrowserObjectRow
                      key={item.key}
                      item={item}
                      marker="recent"
                      onChoose={() =>
                        props.onChooseTable(item.connection, item.schema.name, item.table)
                      }
                    />
                  ))}
                </SidebarSection>
              ) : null}
            </>
          )}
          <SidebarSection label="Connections">
            {filteredConnections.map((connection) => {
              const localFiles = connection.type === "Local files";
              const objectCount = connection.schemas.reduce(
                (count, schema) => count + schema.tables.length,
                0,
              );
              const databaseCount = new Set(
                connection.schemas.map((schema) => schema.database ?? "default"),
              ).size;
              return (
                <button
                  key={connection.id}
                  type="button"
                  className="group flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-xs hover:bg-muted"
                  onClick={() => {
                    setQuery("");
                    props.onChooseConnection(connection);
                  }}
                >
                  <ConnectionTypeIcon connectionType={connection.type} className="size-5" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate font-medium">{connection.name}</span>
                    <span className="block truncate text-[9px] text-muted-foreground">
                      {connection.type} · {databaseCount} {localFiles ? "root" : "source"}
                      {databaseCount === 1 ? "" : "s"} · {objectCount}{" "}
                      {localFiles ? "files" : "objects"}
                    </span>
                  </span>
                  <DiscoveryStatusIcon status={connection.discovery.status} />
                  <ChevronRight className="size-3 text-muted-foreground group-hover:text-foreground" />
                </button>
              );
            })}
            {filteredConnections.length === 0 ? (
              <p className="px-2 py-4 text-center text-[10px] text-muted-foreground">
                No matching connections.
              </p>
            ) : null}
            <Button
              className="w-full justify-start"
              size="sm"
              variant="ghost"
              onClick={props.onAddConnection}
            >
              <Plus data-icon="inline-start" />
              Add data warehouse
            </Button>
            <Button
              className="w-full justify-start"
              size="sm"
              variant="ghost"
              onClick={() => props.onMessage("Local file picker opened")}
            >
              <FileText data-icon="inline-start" />
              Add local file
            </Button>
          </SidebarSection>
        </div>
      </SidebarFrame>
    );
  }

  const tableCount = props.selectedConnection.schemas.reduce(
    (count, schema) => count + schema.tables.length,
    0,
  );
  const databaseCount = new Set(
    props.selectedConnection.schemas.map((schema) => schema.database ?? "default"),
  ).size;
  const browsingLocalFiles = props.selectedConnection.type === "Local files";
  const schemasByDatabase = filteredSchemas.reduce<Map<string, BrowserSchema[]>>(
    (groups, schema) => {
      const database = schema.database ?? "Default";
      groups.set(database, [...(groups.get(database) ?? []), schema]);
      return groups;
    },
    new Map(),
  );

  return (
    <SidebarFrame
      title={props.selectedConnection.name}
      subtitle={`${props.selectedConnection.type} · ${props.environment}`}
      actions={
        <div className="flex items-center">
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={`Configure ${props.selectedConnection.name} discovery scope`}
            onClick={() => props.onMessage("Discovery scope picker opened")}
          >
            <SlidersHorizontal />
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={`Refresh ${props.selectedConnection.name} metadata`}
            disabled={
              props.selectedConnection.discovery.status === "refreshing" ||
              props.selectedConnection.discovery.status === "discovering"
            }
            onClick={() => props.onRefreshConnection(props.selectedConnection.id)}
          >
            <RefreshCw
              className={cn(
                (props.selectedConnection.discovery.status === "refreshing" ||
                  props.selectedConnection.discovery.status === "discovering") &&
                  "animate-spin",
              )}
            />
          </Button>
        </div>
      }
    >
      <div className="flex flex-col gap-3 p-2">
        <Button
          className="-ml-1 self-start"
          size="sm"
          variant="ghost"
          onClick={() => {
            setQuery("");
            setObjectFilter("all");
            props.onBackToConnections();
          }}
        >
          <ChevronLeft data-icon="inline-start" />
          All connections
        </Button>
        <div className="grid grid-cols-3 gap-px overflow-hidden rounded-md border bg-border text-center">
          <DataBrowserMeta
            label={browsingLocalFiles ? "Roots" : "Sources"}
            value={String(databaseCount)}
          />
          <DataBrowserMeta
            label={browsingLocalFiles ? "Files" : "Objects"}
            value={String(tableCount)}
          />
          <DataBrowserMeta label="Access" value="Read only" />
        </div>
        <div className="overflow-hidden rounded-md border">
          <ConnectionDiscoveryNotice connection={props.selectedConnection} />
        </div>
        <div className="flex gap-1">
          <div className="relative min-w-0 flex-1">
            <Search className="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="pl-8"
              placeholder={
                browsingLocalFiles ? "Search files or columns" : "Search objects or columns"
              }
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </div>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                size="icon-sm"
                variant={objectFilter === "all" ? "outline" : "secondary"}
                aria-label={browsingLocalFiles ? "Filter files" : "Filter database objects"}
              >
                <ListFilter />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-44">
              <DropdownMenuLabel>
                {browsingLocalFiles ? "File type" : "Object type"}
              </DropdownMenuLabel>
              <DropdownMenuRadioGroup
                value={objectFilter}
                onValueChange={(value) => setObjectFilter(value as BrowserObjectFilter)}
              >
                <DropdownMenuRadioItem value="all">
                  {browsingLocalFiles ? "All files" : "All objects"}
                </DropdownMenuRadioItem>
                {!browsingLocalFiles ? (
                  <>
                    <DropdownMenuRadioItem value="table">Tables</DropdownMenuRadioItem>
                    <DropdownMenuRadioItem value="view">Views</DropdownMenuRadioItem>
                  </>
                ) : null}
                <DropdownMenuRadioItem value="external">
                  {browsingLocalFiles ? "Tabular files" : "External data"}
                </DropdownMenuRadioItem>
              </DropdownMenuRadioGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
        <p className="-mt-2 px-1 text-[9px] text-muted-foreground">
          Loaded metadata · columns are fetched when an object is opened
        </p>
        <div className="overflow-hidden rounded-md border bg-background">
          {Array.from(schemasByDatabase.entries()).map(([database, schemas]) => (
            <div key={database} className="border-b last:border-0">
              <div className="flex items-center gap-1.5 border-b bg-muted/40 px-2 py-1.5 text-[9px] font-semibold uppercase tracking-wide text-muted-foreground">
                {browsingLocalFiles ? (
                  <FolderOpen className="size-3" />
                ) : (
                  <Database className="size-3" />
                )}
                <span className="min-w-0 flex-1 truncate">{database}</span>
                <span>{schemas.reduce((count, schema) => count + schema.tables.length, 0)}</span>
              </div>
              {schemas.map((schema) => (
                <details key={`${database}.${schema.name}`} className="group/schema" open>
                  <summary
                    className="flex cursor-pointer list-none items-center gap-1.5 border-b px-2 py-1.5 text-[10px] font-medium text-muted-foreground hover:bg-muted/50"
                    title={schema.description}
                  >
                    <ChevronRight className="size-3 transition-transform group-open/schema:rotate-90" />
                    <span className="min-w-0 flex-1 truncate">{schema.name}</span>
                    <span>{schema.tables.length}</span>
                  </summary>
                  {schema.tables.map((table) => (
                    <div
                      key={`${database}.${schema.name}.${table.name}`}
                      className={cn(
                        "group flex w-full items-center gap-1 border-b pl-5 pr-1 last:border-0 hover:bg-muted/70",
                        props.selectedSchemaName === schema.name &&
                          props.selectedTable?.name === table.name &&
                          "bg-primary/10 text-primary",
                      )}
                    >
                      <button
                        type="button"
                        className="flex min-w-0 flex-1 items-center gap-2 py-2 text-left text-xs"
                        onClick={() =>
                          props.onChooseTable(props.selectedConnection, schema.name, table)
                        }
                      >
                        <BrowserObjectIcon kind={table.kind} />
                        <span className="min-w-0 flex-1 truncate font-mono text-[10px]">
                          {table.name}
                        </span>
                        {table.authoredAsset ? (
                          <span className="rounded bg-emerald-500/15 px-1 text-[8px] font-medium text-emerald-700 dark:text-emerald-300">
                            asset
                          </span>
                        ) : null}
                        {props.pinnedTables.includes(
                          browserTableKey(props.selectedConnection.id, schema.name, table.name),
                        ) ? (
                          <Pin
                            className="size-3 shrink-0 text-primary"
                            aria-label="Pinned preview"
                          />
                        ) : null}
                      </button>
                      <Button
                        className="opacity-70 group-hover:opacity-100"
                        size="icon-xs"
                        variant="ghost"
                        aria-label={`Preview ${schema.name}${browsingLocalFiles ? "/" : "."}${table.name}`}
                        onClick={() =>
                          props.onChooseTable(props.selectedConnection, schema.name, table)
                        }
                      >
                        <Eye />
                      </Button>
                    </div>
                  ))}
                </details>
              ))}
            </div>
          ))}
          {filteredSchemas.length === 0 ? (
            <div className="p-4 text-center text-[10px] text-muted-foreground">
              {props.selectedConnection.schemas.length === 0
                ? "This connection currently exposes no objects."
                : "No loaded objects match this search and filter."}
            </div>
          ) : null}
        </div>
        <div className="border-l-2 border-l-emerald-500 px-3 py-1 text-[10px] text-muted-foreground">
          <p className="font-medium text-foreground">
            {browsingLocalFiles
              ? "Project files and Git-managed assets stay distinct"
              : "Warehouse truth and Git truth stay distinct"}
          </p>
          <p className="mt-1">
            {browsingLocalFiles
              ? "A file can feed a managed Renart asset without becoming one. Previewing never edits the file or creates a definition."
              : "The asset marker links an observed object to its managed Renart definition. Previewing never imports or edits that definition."}
          </p>
        </div>
      </div>
    </SidebarFrame>
  );
}

function DataBrowserMeta({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 bg-background px-1 py-2">
      <p className="truncate text-[9px] text-muted-foreground">{label}</p>
      <p className="truncate text-[10px] font-medium">{value}</p>
    </div>
  );
}

function DiscoveryStatusIcon({ status }: { status: BrowserDiscoveryStatus }) {
  if (status === "discovering" || status === "refreshing") {
    return <Loader2 className="size-3 shrink-0 animate-spin text-sky-500" aria-label={status} />;
  }
  if (status === "partial") {
    return <AlertTriangle className="size-3 shrink-0 text-amber-500" aria-label="partial" />;
  }
  if (status === "error") {
    return <X className="size-3 shrink-0 text-red-500" aria-label="error" />;
  }
  return (
    <span
      className={cn(
        "size-2 shrink-0 rounded-full",
        status === "ready" ? "bg-emerald-500" : "bg-zinc-400",
      )}
      aria-label={status}
      role="img"
    />
  );
}

function ConnectionDiscoveryNotice({ connection }: { connection: BrowserConnection }) {
  const status = connection.discovery.status;
  return (
    <div
      className={cn(
        "border-b px-2 py-2 text-[9px] text-muted-foreground",
        status === "partial" && "bg-amber-500/10",
        status === "error" && "bg-red-500/10",
        (status === "discovering" || status === "refreshing") && "bg-sky-500/10",
      )}
    >
      <div className="flex items-center gap-1.5">
        <DiscoveryStatusIcon status={status} />
        <span className="font-medium text-foreground">
          {status === "ready"
            ? `Refreshed ${connection.discovery.lastRefreshed}`
            : status === "empty"
              ? "Connected · no visible objects"
              : status === "error"
                ? "Refresh failed · showing cached metadata"
                : status === "partial"
                  ? "Partial metadata"
                  : status === "discovering"
                    ? "Discovering visible objects"
                    : "Refreshing metadata"}
        </span>
      </div>
      <p className="mt-1">{connection.discovery.scope}</p>
      {connection.discovery.detail ? <p className="mt-1">{connection.discovery.detail}</p> : null}
    </div>
  );
}

function WorkbenchSettingsSidebar(props: LabSidebarProps) {
  if (props.settingsView === "connections") return <ConnectionsSidebar {...props} />;
  if (props.settingsView === "environments") return <EnvironmentsSidebar {...props} />;
  if (props.settingsView === "pipeline") return <PipelineSettingsSidebar {...props} />;
  return <ProjectSettingsSidebar {...props} />;
}

function ConnectionsSidebar(props: LabSidebarProps) {
  const connectionGroup = (type: "warehouse" | "object" | "local") =>
    props.connections.filter((connection) =>
      type === "object"
        ? connection.type === "S3"
        : type === "local"
          ? connection.type === "Local files"
          : connection.type !== "S3" && connection.type !== "Local files",
    );
  return (
    <SidebarFrame
      title="Connections"
      subtitle={`${props.connections.length} configured · credentials stay server-side`}
      actions={
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="Add data warehouse"
          onClick={props.onAddConnection}
        >
          <Plus />
        </Button>
      }
    >
      <div className="flex flex-col gap-4 p-2">
        <div className="relative">
          <Search className="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input className="pl-8" placeholder="Filter connections" />
        </div>
        <SidebarSection label="Warehouses">
          {connectionGroup("warehouse").map((connection) => (
            <ConnectionTreeItem
              key={connection.id}
              connection={connection}
              active={props.selectedConnection.id === connection.id}
              onClick={() => props.onChooseConnection(connection)}
            />
          ))}
        </SidebarSection>
        <SidebarSection label="Object storage">
          {connectionGroup("object").map((connection) => (
            <ConnectionTreeItem
              key={connection.id}
              connection={connection}
              active={props.selectedConnection.id === connection.id}
              onClick={() => props.onChooseConnection(connection)}
            />
          ))}
        </SidebarSection>
        <SidebarSection label="Local files">
          {connectionGroup("local").map((connection) => (
            <ConnectionTreeItem
              key={connection.id}
              connection={connection}
              active={props.selectedConnection.id === connection.id}
              onClick={() => props.onChooseConnection(connection)}
            />
          ))}
        </SidebarSection>
        <SidebarSection label="Actions">
          <TreeItem
            icon={Activity}
            label="Test all connections"
            onClick={() => props.onMessage("Connection checks started")}
          />
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function EnvironmentsSidebar(props: LabSidebarProps) {
  return (
    <SidebarFrame
      title="Environments"
      subtitle="Execution policy and connection overrides"
      actions={
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label="New environment"
          onClick={() => props.onMessage("New environment dialog opened")}
        >
          <Plus />
        </Button>
      }
    >
      <div className="flex flex-col gap-4 p-2">
        <SidebarSection label="Project environments">
          {labEnvironments.map((environment) => (
            <TreeItem
              key={environment.id}
              icon={CircleDot}
              label={environment.id}
              active={props.environment === environment.id}
              onClick={() => props.onEnvironmentChange(environment.id)}
              trailing={
                <Badge
                  variant={environment.policy === "Protected" ? "destructive" : "muted"}
                  size="xs"
                >
                  {environment.policy}
                </Badge>
              }
            />
          ))}
        </SidebarSection>
        <SidebarSection label="Selected policy">
          <div className="rounded-lg border bg-muted/30 p-3 text-xs">
            <p className="font-medium capitalize">{props.environment}</p>
            <p className="mt-1 text-[10px] text-muted-foreground">
              {labEnvironments.find((environment) => environment.id === props.environment)?.detail}
            </p>
          </div>
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function PipelineSettingsSidebar(props: LabSidebarProps) {
  const items: Array<{ section: SettingsSection; label: string; icon: LucideIcon }> = [
    { section: "general", label: "General", icon: SlidersHorizontal },
    { section: "execution", label: "Execution", icon: Play },
    { section: "python", label: "Python dependencies", icon: Code2 },
    { section: "variables", label: "Variables", icon: FileCode2 },
    { section: "hooks", label: "Pre and post hooks", icon: TerminalSquare },
  ];
  return (
    <SidebarFrame title="Pipeline settings" subtitle="growth / revenue-model / pipeline.yml">
      <div className="flex flex-col gap-4 p-2">
        <SidebarSection label="Configuration">
          {items.map((item) => (
            <TreeItem
              key={item.section}
              icon={item.icon}
              label={item.label}
              active={props.settingsSection === item.section}
              onClick={() => props.onSettingsSectionChange(item.section)}
            />
          ))}
        </SidebarSection>
        <SidebarSection label="Version control">
          <div className="rounded-lg border border-dashed p-3 text-[10px] text-muted-foreground">
            Changes are written to <span className="font-mono text-foreground">pipeline.yml</span>{" "}
            and remain reviewable in Git.
          </div>
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function ProjectSettingsSidebar(props: LabSidebarProps) {
  const items: Array<{ section: SettingsSection; label: string; icon: LucideIcon }> = [
    { section: "general", label: "General", icon: Settings2 },
    { section: "git", label: "Git and project paths", icon: GitBranch },
    { section: "appearance", label: "Appearance", icon: Palette },
    { section: "security", label: "Credentials and vault", icon: LockKeyhole },
  ];
  return (
    <SidebarFrame title="Project settings" subtitle="Growth data platform">
      <div className="flex flex-col gap-4 p-2">
        <SidebarSection label="Project">
          {items.map((item) => (
            <TreeItem
              key={item.section}
              icon={item.icon}
              label={item.label}
              active={props.settingsSection === item.section}
              onClick={() => props.onSettingsSectionChange(item.section)}
            />
          ))}
        </SidebarSection>
        <SidebarSection label="Source of truth">
          <div className="rounded-lg border bg-muted/30 p-3 text-[10px] text-muted-foreground">
            Repository files remain authoritative. Local secrets and scheduler state stay outside
            Git.
          </div>
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function SettingsSidebar(props: LabSidebarProps) {
  return (
    <SidebarFrame title="Project utilities" subtitle="Shared across Build, Run, and Explore">
      <div className="flex flex-col gap-4 p-2">
        <Button
          className="-ml-1 self-start md:hidden"
          size="sm"
          variant="ghost"
          onClick={props.onOpenContext}
        >
          <ChevronLeft data-icon="inline-start" />
          Back to workspace
        </Button>
        <SidebarSection label="Data and execution">
          <TreeItem
            icon={Database}
            label="Connections"
            active={props.settingsView === "connections"}
            onClick={() => props.onOpenSettings("connections")}
            trailing={
              <Badge variant="muted" size="xs">
                4
              </Badge>
            }
          />
          <TreeItem
            icon={CircleDot}
            label="Environments"
            active={props.settingsView === "environments"}
            onClick={() => props.onOpenSettings("environments")}
            trailing={
              <Badge variant="muted" size="xs">
                3
              </Badge>
            }
          />
          <TreeItem
            icon={SlidersHorizontal}
            label="Pipeline settings"
            active={props.settingsView === "pipeline"}
            onClick={() => props.onOpenSettings("pipeline")}
          />
        </SidebarSection>
        <SidebarSection label="Project">
          <TreeItem
            icon={Settings2}
            label="General"
            active={props.settingsView === "project"}
            onClick={() => props.onOpenSettings("project")}
          />
          <TreeItem
            icon={GitBranch}
            label="Source control"
            onClick={() => props.onMessage("Source control opened")}
          />
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function StudioSidebar(props: LabSidebarProps) {
  return (
    <aside className="hidden w-72 shrink-0 border-r bg-card md:block">
      <StudioResources {...props} />
    </aside>
  );
}

function StudioResources(props: LabSidebarProps) {
  return (
    <SidebarFrame
      title="Growth data platform"
      subtitle="Project studio · main"
      actions={
        <Button size="icon-sm" variant="ghost" aria-label="Collapse project sidebar">
          <PanelLeft />
        </Button>
      }
    >
      <div className="flex flex-col gap-4 p-2">
        <SidebarSection label="Pipelines">
          <TreeItem
            icon={Boxes}
            label="revenue-model"
            active={props.area === "build" || props.area === "operate"}
            onClick={() => props.onSelectArea("build")}
            trailing={
              <Badge variant="muted" size="xs">
                6
              </Badge>
            }
          />
          <TreeItem
            icon={Boxes}
            label="product-events"
            onClick={() => props.onMessage("Product events pipeline opened")}
            trailing={
              <Badge variant="muted" size="xs">
                8
              </Badge>
            }
          />
          <TreeItem
            icon={Boxes}
            label="finance-close"
            onClick={() => props.onMessage("Finance close pipeline opened")}
            trailing={
              <Badge variant="muted" size="xs">
                5
              </Badge>
            }
          />
        </SidebarSection>
        <SidebarSection label="Documents">
          <TreeItem
            icon={BookOpen}
            label="Notebooks"
            active={props.area === "notebooks"}
            onClick={props.onOpenNotebook}
            trailing={
              <Badge variant="muted" size="xs">
                4
              </Badge>
            }
          />
          <TreeItem
            icon={LayoutDashboard}
            label="Dashboards"
            active={props.area === "explore" && props.exploreView === "dashboards"}
            onClick={() => props.onExploreViewChange("dashboards")}
            trailing={
              <Badge variant="muted" size="xs">
                4
              </Badge>
            }
          />
          <TreeItem
            icon={FileText}
            label="Reports"
            active={props.area === "explore" && props.exploreView === "reports"}
            onClick={() => props.onExploreViewChange("reports")}
            trailing={
              <Badge variant="muted" size="xs">
                3
              </Badge>
            }
          />
        </SidebarSection>
        <SidebarSection label="Discover">
          <TreeItem
            icon={Network}
            label="Workspace Catalog"
            onClick={() => props.onExploreViewChange("catalog")}
          />
          <TreeItem
            icon={Database}
            label="Data Browser"
            active={props.utilityPane === "data"}
            onClick={props.onOpenData}
            trailing={<ChevronRight />}
          />
        </SidebarSection>
        <SidebarSection label="Project utilities">
          <TreeItem
            icon={Database}
            label="Connections"
            onClick={() => props.onOpenSettings("connections")}
          />
          <TreeItem
            icon={CircleDot}
            label="Environments"
            onClick={() => props.onOpenSettings("environments")}
          />
          <TreeItem
            icon={Settings2}
            label="Settings"
            onClick={() => props.onOpenSettings("project")}
          />
        </SidebarSection>
      </div>
    </SidebarFrame>
  );
}

function StudioPhaseBar({
  area,
  buildView,
  operateView,
  onBuildViewChange,
  onOperateViewChange,
}: {
  area: LabArea;
  buildView: BuildView;
  operateView: OperateView;
  onBuildViewChange: (view: BuildView) => void;
  onOperateViewChange: (view: OperateView) => void;
}) {
  const phase =
    area === "operate"
      ? operateView === "overview" || operateView === "runs" || operateView === "run-detail"
        ? "observe"
        : "deploy"
      : buildView === "adhoc"
        ? "query"
        : "design";
  const items: Array<{ key: string; label: string; icon: LucideIcon; onClick: () => void }> = [
    { key: "design", label: "Design", icon: Boxes, onClick: () => onBuildViewChange("canvas") },
    {
      key: "query",
      label: "Query",
      icon: TerminalSquare,
      onClick: () => onBuildViewChange("adhoc"),
    },
    {
      key: "deploy",
      label: "Deploy",
      icon: Rocket,
      onClick: () => onOperateViewChange("deployments"),
    },
    {
      key: "observe",
      label: "Observe",
      icon: Activity,
      onClick: () => onOperateViewChange("overview"),
    },
  ];
  return (
    <div className="hidden h-10 shrink-0 items-center gap-1 border-b bg-background px-3 md:flex">
      <span className="mr-2 text-[10px] font-medium uppercase tracking-wider text-muted-foreground">
        revenue-model
      </span>
      {items.map((item) => (
        <Button
          key={item.key}
          variant={phase === item.key ? "secondary" : "ghost"}
          size="sm"
          onClick={item.onClick}
        >
          <item.icon data-icon="inline-start" />
          {item.label}
        </Button>
      ))}
    </div>
  );
}

function SidebarSection({
  label,
  description,
  children,
}: {
  label: string;
  description?: string;
  children: ReactNode;
}) {
  return (
    <section>
      <div className="mb-1 flex items-end gap-2 px-2">
        <p className="text-[9px] font-semibold uppercase tracking-wider text-muted-foreground">
          {label}
        </p>
        {description ? (
          <p className="ml-auto text-[8px] text-muted-foreground">{description}</p>
        ) : null}
      </div>
      <div className="flex flex-col gap-0.5">{children}</div>
    </section>
  );
}

function ConnectionTreeItem({
  connection,
  active,
  onClick,
}: {
  connection: BrowserConnection;
  active?: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className={cn(
        "flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition hover:bg-muted",
        active && "bg-primary/10 font-medium text-primary hover:bg-primary/15",
      )}
      onClick={onClick}
    >
      <ConnectionTypeIcon connectionType={connection.type} className="size-5" />
      <span className="min-w-0 flex-1 truncate">{connection.name}</span>
      <Badge variant="muted" size="xs">
        {connection.type}
      </Badge>
    </button>
  );
}

function TreeItem({
  icon: Icon,
  label,
  active,
  compact,
  trailing,
  onClick,
}: {
  icon: LucideIcon;
  label: string;
  active?: boolean;
  compact?: boolean;
  trailing?: ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className={cn(
        "flex w-full min-w-0 items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition hover:bg-muted",
        compact && "py-1 text-[10px]",
        active && "bg-primary/10 font-medium text-primary hover:bg-primary/15",
      )}
      onClick={onClick}
    >
      <Icon className={cn("size-3.5 shrink-0", compact && "size-3")} />
      <span className={cn("min-w-0 flex-1 truncate", compact && "font-mono")}>{label}</span>
      {trailing ? <span className="flex shrink-0 items-center">{trailing}</span> : null}
    </button>
  );
}

function MobileBottomNavigation({
  area,
  onAreaChange,
}: {
  area: LabArea;
  onAreaChange: (area: LabArea) => void;
}) {
  const items: Array<{ area: LabArea; label: string; icon: LucideIcon }> = [
    { area: "build", label: "Build", icon: Hammer },
    { area: "operate", label: "Run", icon: Play },
    { area: "explore", label: "Explore", icon: Compass },
  ];
  return (
    <nav
      aria-label="Primary navigation"
      className="grid h-[calc(3.5rem+env(safe-area-inset-bottom))] shrink-0 grid-cols-3 border-t bg-background pb-[env(safe-area-inset-bottom)] md:hidden"
    >
      {items.map((item) => (
        <button
          key={item.area}
          type="button"
          className={cn(
            "flex flex-col items-center justify-center gap-0.5 text-[10px] text-muted-foreground",
            (area === item.area || (item.area === "build" && area === "notebooks")) &&
              "text-primary",
          )}
          onClick={() => onAreaChange(item.area)}
        >
          <item.icon className="size-4" />
          <span>{item.label}</span>
        </button>
      ))}
    </nav>
  );
}
