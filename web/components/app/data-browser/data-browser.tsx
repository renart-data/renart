"use client";

import { useAtomValue } from "jotai";
import {
  AlertCircle,
  ArrowLeft,
  ChevronRight,
  Columns3,
  Database,
  File,
  Folder,
  FolderOpen,
  Plus,
  RefreshCw,
  Rows3,
  Search,
  Table2,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { VirtualDataTable } from "@/components/virtual-data-table";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Spinner } from "@/components/ui/spinner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { selectedEnvironmentAtom } from "@/lib/atoms/domains/workspace";
import {
  getDataBrowserChildren,
  getDataBrowserConnections,
  getDataBrowserObject,
  previewDataBrowserObject,
} from "@/lib/api-data-browser";
import type {
  DataBrowserConnection,
  DataBrowserNode,
  DataBrowserObject,
  DataBrowserPreviewResponse,
} from "@/lib/generated/api-types";
import { cn } from "@/lib/utils";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";
import { useIsMobile } from "@/hooks/use-mobile";

import {
  ConnectionTypeIcon,
  friendlyConnectionType,
  normalizeConnectionType,
} from "../connection-type-icon";
import { WorkspaceConnectionDialog } from "../workspace-connection-dialog";
import {
  AppContextSidebarTransition,
  type AppContextSidebarTransitionDirection,
} from "../workbench/workbench-context-sidebar";
import { WorkbenchPortal, useWorkbench } from "../workbench/workbench-slots";

type BrowserLevel = {
  parentId?: string;
  label: string;
  nodes: DataBrowserNode[];
};

const preferredWarehouseTypes = [
  "postgres",
  "duckdb",
  "trino",
  "bigquery",
  "snowflake",
  "databricks",
  "clickhouse",
  "mysql",
];
const preferredFileSystemTypes = ["s3", "gcs", "sftp"];

export function AppDataBrowserPage() {
  return <DataBrowserWorkspace presentation="page" />;
}

export function AppDataBrowserSidebar() {
  return <DataBrowserWorkspace presentation="sidebar-dialog" />;
}

function DataBrowserWorkspace({ presentation }: { presentation: "page" | "sidebar-dialog" }) {
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const isMobile = useIsMobile();
  const { setMobileNavigationOpen } = useWorkbench();
  const settings = useWorkspaceSettingsData();
  const environment = selectedEnvironment || settings.fallbackConfigEnvironment || "default";
  const browser = useDataBrowser(environment, true);
  const [connectionDialogOpen, setConnectionDialogOpen] = useState(false);
  const [objectDialogOpen, setObjectDialogOpen] = useState(false);
  const [requestedConnectionType, setRequestedConnectionType] = useState<string>();
  const [navigationDirection, setNavigationDirection] =
    useState<AppContextSidebarTransitionDirection>("replace");
  const connectionTypes = settings.workspaceConfig?.connection_types ?? [];
  const quickWarehouseTypes = useMemo(() => {
    const warehouseTypes = connectionTypes.filter((item) => item.category === "warehouse");
    return preferredWarehouseTypes
      .map((preferred) =>
        warehouseTypes.find((item) => normalizeConnectionType(item.type_name) === preferred),
      )
      .filter((item): item is (typeof warehouseTypes)[number] => Boolean(item));
  }, [connectionTypes]);
  const quickFileSystemTypes = useMemo(
    () =>
      preferredFileSystemTypes
        .map((preferred) =>
          connectionTypes.find(
            (item) =>
              normalizeConnectionType(item.type_name) === preferred &&
              (item.category === "storage" || preferred === "sftp"),
          ),
        )
        .filter((item): item is (typeof connectionTypes)[number] => Boolean(item)),
    [connectionTypes],
  );

  const beginConnectionCreation = (connectionType?: string) => {
    setRequestedConnectionType(connectionType);
    setConnectionDialogOpen(true);
  };

  const openNode = async (node: DataBrowserNode) => {
    if (node.node_type === "namespace") setNavigationDirection("forward");
    if (presentation === "sidebar-dialog" && node.node_type === "object") {
      // Keep the mobile context sheet mounted beneath the nested dialog so its
      // browse state survives while the object details are open.
      setObjectDialogOpen(true);
    }
    await browser.openNode(node);
    if (presentation === "page" && isMobile && node.node_type === "object") {
      setMobileNavigationOpen(false);
    }
  };

  const selectConnection = async (connection: DataBrowserConnection) => {
    setNavigationDirection("forward");
    await browser.selectConnection(connection);
  };

  const navigateBack = () => {
    setNavigationDirection("back");
    browser.back();
  };

  const reloadConnections = async () => {
    setNavigationDirection("replace");
    await browser.reloadConnections();
  };

  const navigator = (
    <DataBrowserNavigator
      browser={browser}
      quickWarehouseTypes={quickWarehouseTypes.map((item) => item.type_name)}
      quickFileSystemTypes={quickFileSystemTypes.map((item) => item.type_name)}
      onAddConnection={beginConnectionCreation}
      onOpenNode={openNode}
      onSelectConnection={selectConnection}
      onBack={navigateBack}
      onReload={reloadConnections}
      navigationDirection={navigationDirection}
    />
  );

  return (
    <>
      {presentation === "page" ? (
        <>
          <WorkbenchPortal slot="context">{navigator}</WorkbenchPortal>
          <div className="flex h-full min-h-0 min-w-0 bg-muted/30 p-1.5 md:p-2">
            <DataBrowserDetail
              browser={browser}
              className="h-full flex-1 overflow-hidden rounded-xl border bg-card shadow-sm"
            />
          </div>
        </>
      ) : (
        <>
          {navigator}
          <Dialog open={objectDialogOpen} onOpenChange={setObjectDialogOpen}>
            <DialogContent className="h-[min(48rem,calc(100dvh-1rem))] min-h-0 gap-0 overflow-hidden p-0 sm:max-w-5xl">
              <DialogHeader className="sr-only">
                <DialogTitle>Data object details</DialogTitle>
                <DialogDescription>
                  Inspect the selected table or file schema and request a bounded data preview.
                </DialogDescription>
              </DialogHeader>
              <DataBrowserDetail browser={browser} className="h-full overflow-hidden" />
            </DialogContent>
          </Dialog>
        </>
      )}
      {connectionTypes.length > 0 ? (
        <WorkspaceConnectionDialog
          key={`${environment}:${requestedConnectionType ?? "any"}:${connectionDialogOpen}`}
          open={connectionDialogOpen}
          onOpenChange={setConnectionDialogOpen}
          environment={environment}
          connectionTypes={connectionTypes}
          requestedConnectionType={requestedConnectionType}
          onCreated={async (connectionName) => {
            setNavigationDirection("forward");
            await browser.reloadConnections(connectionName);
          }}
        />
      ) : null}
    </>
  );
}

function useDataBrowser(environment: string, enabled: boolean) {
  const requestID = useRef(0);
  const [connections, setConnections] = useState<DataBrowserConnection[]>([]);
  const [selectedConnection, setSelectedConnection] = useState<DataBrowserConnection | null>(null);
  const [levels, setLevels] = useState<BrowserLevel[]>([]);
  const [selectedObject, setSelectedObject] = useState<DataBrowserObject | null>(null);
  const [preview, setPreview] = useState<DataBrowserPreviewResponse | null>(null);
  const [loading, setLoading] = useState(false);
  const [objectLoading, setObjectLoading] = useState(false);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const loadChildren = useCallback(
    async (connection: DataBrowserConnection, parentId?: string, label = connection.name) => {
      setLoading(true);
      setError(null);
      try {
        const response = await getDataBrowserChildren({
          connectionId: connection.id,
          parentId,
          environment,
        });
        return { parentId, label, nodes: response.nodes } satisfies BrowserLevel;
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : "Could not browse this data source.");
        return null;
      } finally {
        setLoading(false);
      }
    },
    [environment],
  );

  const selectConnection = useCallback(
    async (connection: DataBrowserConnection) => {
      const nextRequest = ++requestID.current;
      setSelectedConnection(connection);
      setSelectedObject(null);
      setPreview(null);
      setLevels([]);
      const level = await loadChildren(connection);
      if (level && requestID.current === nextRequest) setLevels([level]);
    },
    [loadChildren],
  );

  const reloadConnections = useCallback(
    async (selectName?: string) => {
      const nextRequest = ++requestID.current;
      setLoading(true);
      setError(null);
      try {
        const response = await getDataBrowserConnections(environment);
        if (requestID.current !== nextRequest) return;
        setConnections(response.connections);
        setSelectedObject(null);
        setPreview(null);
        setLevels([]);
        const nextConnection = selectName
          ? response.connections.find((item) => item.name === selectName)
          : null;
        setSelectedConnection(nextConnection ?? null);
        if (nextConnection) {
          const level = await loadChildren(nextConnection);
          if (level && requestID.current === nextRequest) setLevels([level]);
        }
      } catch (cause) {
        if (requestID.current === nextRequest) {
          setConnections([]);
          setError(cause instanceof Error ? cause.message : "Could not load data sources.");
        }
      } finally {
        if (requestID.current === nextRequest) setLoading(false);
      }
    },
    [environment, loadChildren],
  );

  useEffect(() => {
    if (!enabled) return;
    void reloadConnections();
  }, [enabled, reloadConnections]);

  const openNode = useCallback(
    async (node: DataBrowserNode) => {
      if (!selectedConnection) return;
      setError(null);
      setPreview(null);
      if (node.node_type === "namespace") {
        const level = await loadChildren(selectedConnection, node.id, node.label);
        if (level) {
          setLevels((current) => [...current, level]);
          setSelectedObject(null);
        }
        return;
      }
      setObjectLoading(true);
      try {
        const response = await getDataBrowserObject({ objectId: node.id, environment });
        setSelectedObject(response.object);
      } catch (cause) {
        setError(cause instanceof Error ? cause.message : "Could not describe this object.");
      } finally {
        setObjectLoading(false);
      }
    },
    [environment, loadChildren, selectedConnection],
  );

  const back = () => {
    setError(null);
    setSelectedObject(null);
    setPreview(null);
    if (levels.length > 1) {
      setLevels((current) => current.slice(0, -1));
      return;
    }
    setLevels([]);
    setSelectedConnection(null);
  };

  const runPreview = useCallback(async () => {
    if (!selectedObject) return;
    setPreviewLoading(true);
    setError(null);
    try {
      setPreview(
        await previewDataBrowserObject({
          object_id: selectedObject.id,
          environment,
          limit: 100,
        }),
      );
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Could not preview this object.");
    } finally {
      setPreviewLoading(false);
    }
  }, [environment, selectedObject]);

  return {
    connections,
    selectedConnection,
    levels,
    currentLevel: levels.at(-1) ?? null,
    selectedObject,
    preview,
    loading,
    objectLoading,
    previewLoading,
    error,
    selectConnection,
    openNode,
    back,
    runPreview,
    reloadConnections,
  };
}

type DataBrowserController = ReturnType<typeof useDataBrowser>;

function DataBrowserNavigator({
  browser,
  quickWarehouseTypes,
  quickFileSystemTypes,
  onAddConnection,
  onOpenNode,
  onSelectConnection,
  onBack,
  onReload,
  navigationDirection,
}: {
  browser: DataBrowserController;
  quickWarehouseTypes: string[];
  quickFileSystemTypes: string[];
  onAddConnection: (connectionType?: string) => void;
  onOpenNode: (node: DataBrowserNode) => void | Promise<void>;
  onSelectConnection: (connection: DataBrowserConnection) => void | Promise<void>;
  onBack: () => void;
  onReload: () => void | Promise<void>;
  navigationDirection: AppContextSidebarTransitionDirection;
}) {
  const [query, setQuery] = useState("");
  const filteredConnections = browser.connections.filter((connection) =>
    `${connection.name} ${connection.type}`.toLowerCase().includes(query.trim().toLowerCase()),
  );
  const filteredNodes = (browser.currentLevel?.nodes ?? []).filter((node) =>
    node.label.toLowerCase().includes(query.trim().toLowerCase()),
  );
  const viewKey = browser.selectedConnection
    ? [
        browser.selectedConnection.id,
        ...browser.levels.slice(1).map((level) => level.parentId ?? level.label),
      ].join(":")
    : "sources";

  return (
    <AppContextSidebarTransition
      viewKey={viewKey}
      direction={navigationDirection}
      className="overflow-hidden bg-card"
    >
      <div
        data-slot="workbench-context-header"
        className="flex h-10 shrink-0 items-center gap-2 border-b px-3 pr-12 md:pr-3"
      >
        {browser.selectedConnection ? (
          <Button variant="ghost" size="icon-sm" aria-label="Back" onClick={onBack}>
            <ArrowLeft />
          </Button>
        ) : (
          <Database className="size-4 text-primary" />
        )}
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-xs font-semibold">
            {browser.currentLevel?.label ?? "Data Browser"}
          </h2>
          <p className="truncate text-[10px] text-muted-foreground">
            {browser.selectedConnection
              ? friendlyConnectionType(browser.selectedConnection.type)
              : "Warehouses and local files"}
          </p>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Refresh data sources"
          onClick={() => void onReload()}
          disabled={browser.loading}
        >
          <RefreshCw className={cn(browser.loading && "animate-spin")} />
        </Button>
      </div>
      <div className="shrink-0 border-b p-2">
        <div className="relative">
          <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={browser.selectedConnection ? "Filter objects…" : "Filter sources…"}
            className="h-8 pl-8 text-xs"
          />
        </div>
      </div>
      <ScrollArea className="min-h-0 flex-1" showHorizontalScrollBar={false}>
        <div className="p-2">
          {browser.error ? (
            <Alert variant="destructive" className="mb-2">
              <AlertCircle />
              <AlertTitle>Data Browser needs attention</AlertTitle>
              <AlertDescription>{browser.error}</AlertDescription>
            </Alert>
          ) : null}
          {browser.loading && !browser.currentLevel ? (
            <div className="flex items-center justify-center gap-2 py-10 text-xs text-muted-foreground">
              <Spinner /> Loading data sources…
            </div>
          ) : browser.selectedConnection ? (
            <NodeList nodes={filteredNodes} onOpen={onOpenNode} />
          ) : (
            <>
              <NavigatorSection label="Connected sources">
                {filteredConnections.map((connection) => (
                  <NavigatorRow
                    key={connection.id}
                    icon={<ConnectionTypeIcon connectionType={connection.type} />}
                    label={connection.name}
                    description={
                      connection.source_kind === "local_files"
                        ? "Files inside this project"
                        : friendlyConnectionType(connection.type)
                    }
                    trailing={<ChevronRight className="size-3.5" />}
                    onClick={() => void onSelectConnection(connection)}
                  />
                ))}
                {filteredConnections.length === 0 ? (
                  <p className="px-2 py-6 text-center text-xs text-muted-foreground">
                    No matching data sources.
                  </p>
                ) : null}
              </NavigatorSection>
              {quickWarehouseTypes.length > 0 ? (
                <NavigatorSection label="Add a warehouse">
                  <div className="grid grid-cols-2 gap-1.5">
                    {quickWarehouseTypes.map((connectionType) => (
                      <button
                        key={connectionType}
                        type="button"
                        className="flex min-w-0 items-center gap-2 rounded-lg border bg-background px-2 py-2 text-left transition-colors hover:border-primary/30 hover:bg-accent"
                        onClick={() => onAddConnection(connectionType)}
                      >
                        <ConnectionTypeIcon connectionType={connectionType} className="size-7" />
                        <span className="truncate text-[11px] font-medium">
                          {friendlyConnectionType(connectionType)}
                        </span>
                      </button>
                    ))}
                  </div>
                  <Button
                    variant="ghost"
                    size="sm"
                    className="mt-1 w-full justify-start"
                    onClick={() => onAddConnection()}
                  >
                    <Plus /> Other connection
                  </Button>
                </NavigatorSection>
              ) : null}
              {quickFileSystemTypes.length > 0 ? (
                <NavigatorSection label="Add a file system">
                  <div className="grid grid-cols-2 gap-1.5">
                    {quickFileSystemTypes.map((connectionType) => (
                      <button
                        key={connectionType}
                        type="button"
                        className="flex min-w-0 items-center gap-2 rounded-lg border bg-background px-2 py-2 text-left transition-colors hover:border-primary/30 hover:bg-accent"
                        onClick={() => onAddConnection(connectionType)}
                      >
                        <ConnectionTypeIcon connectionType={connectionType} className="size-7" />
                        <span className="truncate text-[11px] font-medium">
                          {friendlyConnectionType(connectionType)}
                        </span>
                      </button>
                    ))}
                  </div>
                </NavigatorSection>
              ) : null}
            </>
          )}
        </div>
      </ScrollArea>
    </AppContextSidebarTransition>
  );
}

function NodeList({
  nodes,
  onOpen,
}: {
  nodes: DataBrowserNode[];
  onOpen: (node: DataBrowserNode) => void | Promise<void>;
}) {
  if (nodes.length === 0) {
    return <p className="px-2 py-10 text-center text-xs text-muted-foreground">No objects here.</p>;
  }
  return (
    <div className="space-y-0.5">
      {nodes.map((node) => {
        const Icon =
          node.node_type === "namespace" ? Folder : node.object_kind === "file" ? File : Table2;
        return (
          <button
            key={node.id}
            type="button"
            className="group flex w-full items-center gap-2 rounded-lg px-2 py-2 text-left hover:bg-accent"
            onClick={() => void onOpen(node)}
          >
            <Icon className="size-4 shrink-0 text-muted-foreground group-hover:text-primary" />
            <span className="min-w-0 flex-1 truncate text-xs font-medium">{node.label}</span>
            {node.format ? <Badge variant="secondary">{node.format}</Badge> : null}
            {node.has_children ? <ChevronRight className="size-3.5 text-muted-foreground" /> : null}
          </button>
        );
      })}
    </div>
  );
}

function NavigatorSection({ label, children }: { label: string; children: ReactNode }) {
  return (
    <section className="mb-4 last:mb-0">
      <h3 className="mb-1.5 px-2 text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">
        {label}
      </h3>
      <div className="space-y-1">{children}</div>
    </section>
  );
}

function NavigatorRow({
  icon,
  label,
  description,
  trailing,
  onClick,
}: {
  icon: ReactNode;
  label: string;
  description: string;
  trailing?: ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className="flex w-full items-center gap-2 rounded-lg px-2 py-2 text-left transition-colors hover:bg-accent"
      onClick={onClick}
    >
      {icon}
      <span className="min-w-0 flex-1">
        <span className="block truncate text-xs font-medium">{label}</span>
        <span className="block truncate text-[10px] text-muted-foreground">{description}</span>
      </span>
      <span className="text-muted-foreground">{trailing}</span>
    </button>
  );
}

function DataBrowserDetail({
  browser,
  className,
}: {
  browser: DataBrowserController;
  className?: string;
}) {
  const object = browser.selectedObject;
  return (
    <section className={cn("flex min-h-0 min-w-0 flex-col bg-background", className)}>
      {browser.objectLoading ? (
        <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
          <Spinner /> Describing object…
        </div>
      ) : !object ? (
        <Empty className="border-0">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              {browser.selectedConnection?.source_kind === "local_files" ? (
                <FolderOpen />
              ) : (
                <Database />
              )}
            </EmptyMedia>
            <EmptyTitle>Choose a table or file</EmptyTitle>
            <EmptyDescription>
              Select a data source, browse its namespaces, and choose an object to inspect its
              schema. Rows are fetched only after you request a preview.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <>
          <div className="flex shrink-0 items-start gap-3 border-b py-3 pr-12 pl-4">
            <ConnectionTypeIcon connectionType={object.connection_type} className="mt-0.5 size-8" />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="truncate text-sm font-semibold">{object.name}</h2>
                <Badge variant="secondary">{object.kind}</Badge>
                {object.format ? <Badge variant="outline">{object.format}</Badge> : null}
              </div>
              <p className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">
                {object.reference_text}
              </p>
              <p className="mt-1 text-[10px] text-muted-foreground">
                {object.connection_name}
                {object.size_bytes ? ` · ${formatBytes(object.size_bytes)}` : ""}
                {object.modified_at ? ` · Updated ${formatDate(object.modified_at)}` : ""}
              </p>
            </div>
          </div>
          {object.warning ? (
            <Alert variant="destructive" className="m-3 mb-0">
              <AlertCircle />
              <AlertTitle>Schema discovery incomplete</AlertTitle>
              <AlertDescription>{object.warning}</AlertDescription>
            </Alert>
          ) : null}
          <Tabs defaultValue="preview" className="min-h-0 flex-1 gap-0">
            <div className="flex min-h-11 shrink-0 items-center justify-between gap-2 border-b px-3">
              <TabsList variant="line" className="h-10 rounded-none p-0">
                <TabsTrigger value="preview" className="rounded-none">
                  <Rows3 /> Preview
                </TabsTrigger>
                <TabsTrigger value="columns" className="rounded-none">
                  <Columns3 /> Columns
                  <Badge variant="secondary" className="ml-0.5">
                    {object.columns.length}
                  </Badge>
                </TabsTrigger>
              </TabsList>
              <Button
                size="sm"
                onClick={() => void browser.runPreview()}
                disabled={!object.capabilities.preview_rows || browser.previewLoading}
              >
                {browser.previewLoading ? <Spinner /> : <Rows3 />}
                Preview rows
              </Button>
            </div>
            <TabsContent value="preview" className="min-h-0 flex-1 p-0">
              {browser.preview ? (
                <div className="flex h-full min-h-0 flex-col">
                  <div className="flex shrink-0 items-center justify-between border-b px-3 py-1.5 text-[10px] text-muted-foreground">
                    <span>
                      {browser.preview.rows.length} rows · {browser.preview.elapsed_ms} ms
                    </span>
                    {browser.preview.truncated ? <span>Preview truncated</span> : null}
                  </div>
                  <div className="min-h-0 flex-1">
                    <VirtualDataTable
                      ariaLabel={`${object.name} preview`}
                      columns={browser.preview.columns}
                      rows={browser.preview.rows}
                      height="100%"
                      frameless
                    />
                  </div>
                </div>
              ) : (
                <Empty className="border-0">
                  <EmptyHeader>
                    <EmptyMedia variant="icon">
                      <Rows3 />
                    </EmptyMedia>
                    <EmptyTitle>No rows loaded</EmptyTitle>
                    <EmptyDescription>
                      Preview up to 100 rows. Renart builds the read-only query on the server.
                    </EmptyDescription>
                  </EmptyHeader>
                </Empty>
              )}
            </TabsContent>
            <TabsContent value="columns" className="min-h-0 flex-1 overflow-auto p-0">
              {object.columns.length > 0 ? (
                <div className="divide-y">
                  {object.columns.map((column, index) => (
                    <div
                      key={`${column.name}:${index}`}
                      className="grid grid-cols-[minmax(0,1fr)_minmax(7rem,auto)] gap-3 px-4 py-2 text-xs"
                    >
                      <span className="truncate font-mono">{column.name}</span>
                      <span className="truncate text-right font-mono text-muted-foreground">
                        {column.type || "unknown"}
                      </span>
                    </div>
                  ))}
                </div>
              ) : (
                <Empty className="border-0">
                  <EmptyHeader>
                    <EmptyMedia variant="icon">
                      <Columns3 />
                    </EmptyMedia>
                    <EmptyTitle>No schema available</EmptyTitle>
                    <EmptyDescription>
                      This connection could not provide column metadata for the selected object.
                    </EmptyDescription>
                  </EmptyHeader>
                </Empty>
              )}
            </TabsContent>
          </Tabs>
        </>
      )}
    </section>
  );
}

function formatBytes(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

function formatDate(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(
    date,
  );
}
