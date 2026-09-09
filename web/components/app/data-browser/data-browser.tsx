"use client";

import { useAtomValue } from "jotai";
import { useLocation, useNavigate } from "@tanstack/react-router";
import { ResourceLink } from "../resource-link";
import { resolveColumn, type DataTarget, type ResourceSearch } from "@/lib/resource-navigation";
import {
  AlertCircle,
  ArrowLeft,
  ChevronRight,
  Columns3,
  Database,
  File,
  FileCode2,
  Folder,
  Plus,
  RefreshCw,
  Rows3,
  Table2,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { VirtualDataTable } from "@/components/virtual-data-table";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { selectedEnvironmentAtom } from "@/lib/atoms/domains/workspace";
import {
  getDataBrowserChildren,
  getDataBrowserConnections,
  resolveDataBrowserObject,
  previewDataBrowserObject,
} from "@/lib/api-data-browser";
import type {
  DataBrowserConnection,
  DataBrowserNode,
  DataBrowserObject,
  DataBrowserPreviewResponse,
} from "@/lib/generated/api-types";
import { cn } from "@/lib/utils";
import { getPinnedProjectId } from "@/lib/project-context";
import { useWorkspaceSettingsData } from "@/hooks/use-workspace-settings-data";

import {
  ConnectionTypeIcon,
  friendlyConnectionType,
  normalizeConnectionType,
} from "../connection-type-icon";
import { WorkspaceConnectionDialog } from "../workspace-connection-dialog-lazy";
import { DataBrowserTransferItem } from "./data-browser-transfer-item";
import { DataBrowserLoading } from "./data-browser-loading";
import { SqlPreview } from "../sql-preview";
import {
  AppContextSidebarTransition,
  type AppContextSidebarTransitionDirection,
} from "../workbench/workbench-context-sidebar";
import { WorkbenchPortal } from "../workbench/workbench-slots";
import { useDataBrowserSearch } from "@/hooks/use-data-browser-search";
import { connectionSearchPrefix, quoteBrowserSegment } from "@/lib/data-browser-search";
import { DataBrowserSearchInput } from "./data-browser-search-input";

type BrowserLevel = {
  parentId?: string;
  label: string;
  nodes: DataBrowserNode[];
  truncated?: boolean;
};

// A mobile Sheet unmounts its content when closed for canvas placement. Preserve
// navigation (not authority) across that transition and revalidate the connection
// revision before reusing cached object references. Bounded, same-tab cache only.
type BrowserNavigation = { connection: DataBrowserConnection; levels: BrowserLevel[] };
const browserNavigationCache = new Map<string, BrowserNavigation>();
const browserSearchCache = new Map<string, string>();

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

export function AppDataBrowserSidebar({
  pipelineId,
  onChooseForCanvas,
}: {
  pipelineId?: string;
  onChooseForCanvas?: () => void;
}) {
  return (
    <DataBrowserWorkspace
      presentation="sidebar-dialog"
      pipelineId={pipelineId}
      onChooseForCanvas={onChooseForCanvas}
    />
  );
}

function DataBrowserWorkspace({
  presentation,
  pipelineId,
  onChooseForCanvas,
}: {
  presentation: "page" | "sidebar-dialog";
  pipelineId?: string;
  onChooseForCanvas?: () => void;
}) {
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const detail = (useLocation().search as ResourceSearch).detail;
  const settings = useWorkspaceSettingsData();
  const environment = selectedEnvironment || settings.fallbackConfigEnvironment || "default";
  const browser = useDataBrowser(environment, true);
  const [connectionDialogOpen, setConnectionDialogOpen] = useState(false);
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
    await browser.openNode(node);
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
      key={JSON.stringify([getPinnedProjectId(), environment])}
      pipelineId={pipelineId}
      environment={environment}
      onChooseForCanvas={onChooseForCanvas}
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
            {detail?.target.kind === "data-object" ? (
              <DataObjectDetail target={detail.target} environment={detail.environment} />
            ) : (
              <DataBrowserDetail
                browser={{
                  selectedObject: null,
                  objectLoading: false,
                  preview: null,
                  previewLoading: false,
                  runPreview: async () => {},
                }}
              />
            )}
          </div>
        </>
      ) : (
        navigator
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
  const scope = JSON.stringify([getPinnedProjectId(), environment]);
  const restored = browserNavigationCache.get(scope);
  const requestID = useRef(0);
  const pending = useRef<AbortController | null>(null);
  const [connections, setConnections] = useState<DataBrowserConnection[]>([]);
  const [selectedConnection, setSelectedConnection] = useState<DataBrowserConnection | null>(
    restored?.connection ?? null,
  );
  const [levels, setLevels] = useState<BrowserLevel[]>(restored?.levels ?? []);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const cancelRequest = useCallback(() => {
    ++requestID.current;
    pending.current?.abort();
    pending.current = null;
    setLoading(false);
  }, []);
  const beginRequest = useCallback(() => {
    cancelRequest();
    const controller = new AbortController();
    pending.current = controller;
    setLoading(true);
    setError(null);
    return { id: requestID.current, signal: controller.signal };
  }, [cancelRequest]);
  useEffect(
    () => () => {
      ++requestID.current;
      pending.current?.abort();
    },
    [scope],
  );

  useEffect(
    () => () => {
      if (!selectedConnection || !levels.length) {
        browserNavigationCache.delete(scope);
        return;
      }
      browserNavigationCache.delete(scope);
      browserNavigationCache.set(scope, { connection: selectedConnection, levels });
      if (browserNavigationCache.size > 12)
        browserNavigationCache.delete(browserNavigationCache.keys().next().value!);
    },
    [scope, selectedConnection, levels],
  );

  const loadChildren = useCallback(
    async (
      connection: DataBrowserConnection,
      signal: AbortSignal,
      parentId?: string,
      label = connection.name,
    ) => {
      const response = await getDataBrowserChildren(
        {
          connectionId: connection.id,
          parentId,
          environment,
        },
        signal,
      );
      return {
        parentId,
        label,
        nodes: response.nodes,
        truncated: response.truncated,
      } satisfies BrowserLevel;
    },
    [environment],
  );

  const selectConnection = useCallback(
    async (connection: DataBrowserConnection) => {
      const request = beginRequest();
      setSelectedConnection(connection);
      setLevels([]);
      try {
        const level = await loadChildren(connection, request.signal);
        if (requestID.current === request.id) setLevels([level]);
      } catch (cause) {
        if (requestID.current === request.id)
          setError(cause instanceof Error ? cause.message : "Could not browse this data source.");
      } finally {
        if (requestID.current === request.id) setLoading(false);
      }
    },
    [beginRequest, loadChildren],
  );

  const reloadConnections = useCallback(
    async (selectName?: string, restore?: BrowserNavigation) => {
      const request = beginRequest();
      setConnections([]);
      try {
        const response = await getDataBrowserConnections(environment, request.signal);
        if (requestID.current !== request.id) return;
        setConnections(response.connections);

        setLevels([]);
        const selectedName = selectName ?? restore?.connection.name;
        const nextConnection = selectedName
          ? response.connections.find((item) => item.name === selectedName)
          : null;
        setSelectedConnection(nextConnection ?? null);
        if (nextConnection) {
          if (nextConnection.id === restore?.connection.id) {
            setLevels(restore.levels);
            return;
          }
          const level = await loadChildren(nextConnection, request.signal);
          if (requestID.current === request.id) setLevels([level]);
        }
      } catch (cause) {
        if (requestID.current === request.id) {
          setError(cause instanceof Error ? cause.message : "Could not load data sources.");
        }
      } finally {
        if (requestID.current === request.id) setLoading(false);
      }
    },
    [beginRequest, environment, loadChildren],
  );

  useEffect(() => {
    if (!enabled) return;
    void reloadConnections(undefined, browserNavigationCache.get(scope));
  }, [enabled, reloadConnections, scope]);

  const openNode = useCallback(
    async (node: DataBrowserNode) => {
      if (!selectedConnection || node.node_type !== "namespace") return;
      const request = beginRequest();
      try {
        const level = await loadChildren(selectedConnection, request.signal, node.id, node.label);
        if (request.id === requestID.current) {
          setLevels((current) => [...current, level]);
        }
      } catch (cause) {
        if (request.id === requestID.current)
          setError(cause instanceof Error ? cause.message : "Could not browse this data source.");
      } finally {
        if (request.id === requestID.current) setLoading(false);
      }
    },
    [beginRequest, loadChildren, selectedConnection],
  );

  const back = () => {
    cancelRequest();
    setError(null);

    if (levels.length > 1) {
      setLevels((current) => current.slice(0, -1));
      return;
    }
    setLevels([]);
    setSelectedConnection(null);
  };

  return {
    connections,
    selectedConnection,
    levels,
    currentLevel: levels.at(-1) ?? null,
    loading,
    error,
    selectConnection,
    openNode,
    back,
    reloadConnections,
  };
}

type DataBrowserController = ReturnType<typeof useDataBrowser>;

function DataBrowserNavigator({
  pipelineId,
  environment,
  onChooseForCanvas,
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
  pipelineId?: string;
  environment: string;
  onChooseForCanvas?: () => void;
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
  const navigator = useRef<HTMLDivElement>(null);
  const keyboardNavigation = useRef(false);
  const rows = () => [
    ...(navigator.current?.querySelectorAll<HTMLElement>("[data-browser-row]") ?? []),
  ];
  const focusFilter = () =>
    navigator.current
      ?.querySelector<HTMLInputElement>('input[aria-label="Search data browser"]')
      ?.focus({ preventScroll: true });
  const focusRow = (direction: "first" | "last") => {
    const targets = rows();
    (direction === "first" ? targets[0] : targets.at(-1))?.focus();
  };
  const searchScope = JSON.stringify([getPinnedProjectId(), environment]);
  const [query, setQuery] = useState(() => browserSearchCache.get(searchScope) ?? "");
  const search = useDataBrowserSearch(
    query,
    browser.connections,
    browser.selectedConnection
      ? {
          connection: browser.selectedConnection,
          parts: browser.levels.slice(1).map((level) => level.label),
          nodes: browser.currentLevel?.nodes ?? [],
          truncated: browser.currentLevel?.truncated,
        }
      : undefined,
    environment,
  );
  useEffect(() => {
    browserSearchCache.delete(searchScope);
    if (query) browserSearchCache.set(searchScope, query);
    if (browserSearchCache.size > 12)
      browserSearchCache.delete(browserSearchCache.keys().next().value!);
  }, [query, searchScope]);
  const activeSearch = query.length > 0;
  const selectedConnection = activeSearch ? search.connection : browser.selectedConnection;
  const filteredConnections = activeSearch ? search.connections : browser.connections;
  const filteredNodes = activeSearch ? search.nodes : (browser.currentLevel?.nodes ?? []);
  const loading = browser.loading || (activeSearch && search.loading);
  const error = activeSearch ? (search.error ?? browser.error) : browser.error;
  const truncated = activeSearch ? search.truncated : browser.currentLevel?.truncated;
  const openSearchNode = (node: DataBrowserNode) => {
    if (!activeSearch) return onOpenNode(node);
    const separator = selectedConnection?.source_kind === "warehouse" ? "." : "/";
    setQuery(
      search.prefix +
        (separator === "." ? quoteBrowserSegment(node.label) : node.label) +
        separator,
    );
  };
  const viewKey = browser.selectedConnection
    ? [
        browser.selectedConnection.id,
        ...browser.levels.slice(1).map((level) => level.parentId ?? level.label),
      ].join(":")
    : "sources";

  useEffect(() => {
    if (!keyboardNavigation.current || loading) return;
    keyboardNavigation.current = false;
    if (error || rows().length === 0) focusFilter();
    else focusRow("first");
  }, [loading, error, viewKey, query, filteredNodes, filteredConnections]);

  return (
    <div
      ref={navigator}
      className="flex h-full min-h-0 flex-col overflow-hidden bg-card"
      onKeyDown={(event) => {
        if (
          event.nativeEvent.isComposing ||
          event.altKey ||
          event.ctrlKey ||
          event.metaKey ||
          event.shiftKey
        )
          return;
        const target =
          event.target instanceof Element
            ? event.target.closest<HTMLElement>("[data-browser-row]")
            : null;
        if (!target) return;
        const items = rows();
        const index = items.indexOf(target);
        if (index < 0) return;
        if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
          event.preventDefault();
          if (event.key === "ArrowUp" && index === 0) focusFilter();
          else
            items[
              event.key === "Home"
                ? 0
                : event.key === "End"
                  ? items.length - 1
                  : Math.max(
                      0,
                      Math.min(items.length - 1, index + (event.key === "ArrowDown" ? 1 : -1)),
                    )
            ]?.focus();
        } else if (event.key === "Enter" || event.key === "ArrowRight") {
          event.preventDefault();
          keyboardNavigation.current = true;
          target.click();
        } else if (event.key === "ArrowLeft" && selectedConnection) {
          event.preventDefault();
          keyboardNavigation.current = false;
          if (activeSearch) setQuery(search.back);
          else onBack();
          focusFilter();
        }
      }}
    >
      <div
        data-slot="workbench-context-header"
        className="flex h-10 shrink-0 items-center gap-2 border-b px-3 pr-12 md:pr-3"
      >
        {selectedConnection ? (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Back"
            onClick={() => (activeSearch ? setQuery(search.back) : onBack())}
          >
            <ArrowLeft />
          </Button>
        ) : (
          <Database className="size-4 text-primary" />
        )}
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-xs font-semibold">
            {activeSearch
              ? search.label
              : (browser.currentLevel?.label ?? browser.selectedConnection?.name ?? "Data Browser")}
          </h2>
          <p className="truncate text-[10px] text-muted-foreground">
            {selectedConnection
              ? friendlyConnectionType(selectedConnection.type)
              : "Warehouses and local files"}
          </p>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Refresh data sources"
          onClick={() => {
            search.refresh();
            void onReload();
          }}
          disabled={loading}
        >
          <RefreshCw />
        </Button>
      </div>
      <div className="shrink-0 border-b p-2">
        <DataBrowserSearchInput
          value={query}
          onChange={setQuery}
          completions={loading || error ? [] : search.completions}
          pathSyntax={search.pathSyntax}
          onNavigateResults={focusRow}
          placeholder={selectedConnection ? "Filter objects…" : "Filter sources…"}
        />
      </div>
      <AppContextSidebarTransition
        viewKey={activeSearch ? "search-results" : viewKey}
        direction={activeSearch ? "replace" : navigationDirection}
        className="min-h-0 flex-1"
      >
        <ScrollArea className="min-h-0 flex-1" showHorizontalScrollBar={false}>
          <div className="p-2">
            {error ? (
              <Alert variant="destructive" className="mb-2">
                <AlertCircle />
                <AlertTitle>Data Browser needs attention</AlertTitle>
                <AlertDescription>
                  {error}
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() => {
                      search.refresh();
                      void onReload();
                    }}
                  >
                    Retry
                  </Button>
                </AlertDescription>
              </Alert>
            ) : null}
            {truncated && !loading ? (
              <p role="status" className="mb-2 px-2 text-xs text-muted-foreground">
                Showing the first 500 objects.
                {selectedConnection?.type === "s3"
                  ? " Keep typing the name prefix to narrow the S3 results (case-sensitive)."
                  : selectedConnection?.source_kind === "storage"
                    ? " Enter a more specific prefix ending in / to browse it directly."
                    : " Choose a smaller namespace to see more specific results."}
              </p>
            ) : null}
            {loading ? (
              <DataBrowserLoading
                label={
                  selectedConnection
                    ? `Loading ${selectedConnection.name}…`
                    : "Loading data sources…"
                }
              />
            ) : error ? null : selectedConnection ? (
              <NodeList
                nodes={filteredNodes}
                onOpen={openSearchNode}
                pipelineId={pipelineId}
                environment={environment}
                onChooseForCanvas={onChooseForCanvas}
              />
            ) : (
              <>
                <NavigatorSection label="Connected sources">
                  {filteredConnections.map((connection) => (
                    <DataBrowserTransferItem
                      key={connection.id}
                      pipelineId={pipelineId}
                      environment={environment}
                      onChoose={onChooseForCanvas}
                      item={
                        (connection.source_kind === "warehouse" ||
                          connection.source_kind === "storage") &&
                        connection.access_mode !== "read_only"
                          ? { kind: "connection", id: connection.name, label: connection.name }
                          : undefined
                      }
                    >
                      <NavigatorRow
                        icon={<ConnectionTypeIcon connectionType={connection.type} />}
                        label={connection.name}
                        description={
                          connection.source_kind === "local_files"
                            ? "Files inside this project"
                            : friendlyConnectionType(connection.type)
                        }
                        trailing={
                          <span className="flex items-center gap-1">
                            {connection.access_mode === "read_only" ? (
                              <span className="text-[10px] text-muted-foreground">Read-only</span>
                            ) : null}
                            <ChevronRight className="size-3.5" />
                          </span>
                        }
                        onClick={() =>
                          activeSearch
                            ? setQuery(connectionSearchPrefix(connection))
                            : void onSelectConnection(connection)
                        }
                      />
                    </DataBrowserTransferItem>
                  ))}
                  {filteredConnections.length === 0 ? (
                    <p className="px-2 py-6 text-center text-xs text-muted-foreground">
                      No matching data sources.
                    </p>
                  ) : null}
                </NavigatorSection>
                {!activeSearch && quickWarehouseTypes.length > 0 ? (
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
                {!activeSearch && quickFileSystemTypes.length > 0 ? (
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
    </div>
  );
}

function NodeList({
  nodes,
  onOpen,
  pipelineId,
  environment,
  onChooseForCanvas,
}: {
  nodes: DataBrowserNode[];
  onOpen: (node: DataBrowserNode) => void | Promise<void>;
  pipelineId?: string;
  environment: string;
  onChooseForCanvas?: () => void;
}) {
  if (nodes.length === 0) {
    return <p className="px-2 py-10 text-center text-xs text-muted-foreground">No objects here.</p>;
  }
  return (
    <div className="space-y-0.5">
      {nodes.map((node) => {
        const Icon =
          node.node_type === "namespace" ? Folder : node.object_kind === "file" ? File : Table2;
        const content = (
          <>
            <Icon className="size-4 shrink-0 text-muted-foreground group-hover:text-primary" />
            <span className="min-w-0 flex-1 truncate text-xs font-medium">{node.label}</span>
            {node.is_default ? <Badge variant="outline">Default</Badge> : null}
            {node.format ? <Badge variant="secondary">{node.format}</Badge> : null}
            {node.has_children ? <ChevronRight className="size-3.5 text-muted-foreground" /> : null}
          </>
        );
        const className =
          "group flex w-full items-center gap-2 rounded-lg px-2 py-2 text-left hover:bg-accent focus-visible:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset";
        const row =
          node.address && node.node_type !== "namespace" ? (
            <ResourceLink
              data-browser-row
              key={node.id}
              target={{ kind: "data-object", address: node.address, section: "schema" }}
              className={className}
              draggable={false}
            >
              {content}
            </ResourceLink>
          ) : (
            <button
              data-browser-row
              key={node.id}
              type="button"
              className={className}
              onClick={() => void onOpen(node)}
            >
              {content}
            </button>
          );
        return (
          <DataBrowserTransferItem
            key={node.id}
            pipelineId={pipelineId}
            environment={environment}
            onChoose={onChooseForCanvas}
            item={
              node.address?.source_kind === "warehouse" &&
              node.object_kind === "table" &&
              node.reference_text
                ? {
                    kind: "table",
                    id: node.id,
                    label: node.label,
                    referenceText: node.reference_text,
                  }
                : node.address?.source_kind === "storage"
                  ? { kind: "storage", id: node.id, label: node.label }
                  : node.address?.source_kind === "local_files" && node.object_kind === "file"
                    ? { kind: "file", id: node.id, label: node.label }
                    : undefined
            }
          >
            {row}
          </DataBrowserTransferItem>
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
      data-browser-row
      type="button"
      className="flex w-full items-center gap-2 rounded-lg px-2 py-2 text-left transition-colors hover:bg-accent focus-visible:bg-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
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
  section = "schema",
  onSectionChange,
  focusedColumn,
}: {
  browser: {
    selectedObject: DataBrowserObject | null;
    objectLoading: boolean;
    preview: DataBrowserPreviewResponse | null;
    previewLoading: boolean;
    runPreview: () => Promise<void>;
  };
  className?: string;
  section?: DataTarget["section"];
  onSectionChange?: (section: DataTarget["section"]) => void;
  focusedColumn?: string;
}) {
  const object = browser.selectedObject;
  const lastFocus = useRef("");
  const focusKey = `${object?.id}:${focusedColumn}:${section}`;
  return (
    <section className={cn("flex min-h-0 min-w-0 flex-col bg-background", className)}>
      {browser.objectLoading ? (
        <DataBrowserLoading label="Describing object…" table />
      ) : !object ? (
        <Empty className="border-0">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <Database />
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
                <Badge variant="secondary">{object.view_definition ? "view" : object.kind}</Badge>
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
              <AlertTitle>Some metadata is unavailable</AlertTitle>
              <AlertDescription>{object.warning}</AlertDescription>
            </Alert>
          ) : null}
          {object.address?.source_kind === "storage" ? (
            <div className="p-4 text-sm text-muted-foreground">
              {object.kind === "prefix" ? "Storage prefix" : "Storage object"} · metadata only. Use
              it in the pipeline canvas to create a Load asset. Browsing does not read or transfer
              its contents.
            </div>
          ) : (
            <Tabs
              value={section}
              onValueChange={(value) => onSectionChange?.(value as DataTarget["section"])}
              className="min-h-0 flex-1 gap-0"
            >
              <div className="flex min-h-11 shrink-0 flex-wrap items-center justify-between gap-2 border-b px-3">
                <TabsList variant="line" className="h-10 rounded-none p-0">
                  <TabsTrigger value="rows" className="rounded-none">
                    <Rows3 /> Preview
                  </TabsTrigger>
                  <TabsTrigger value="schema" className="rounded-none">
                    <Columns3 /> Columns
                    <Badge variant="secondary" className="ml-0.5">
                      {object.columns.length}
                    </Badge>
                  </TabsTrigger>
                  {object.view_definition || section === "definition" ? (
                    <TabsTrigger value="definition" className="rounded-none">
                      <FileCode2 /> SQL
                    </TabsTrigger>
                  ) : null}
                </TabsList>
                <Button
                  size="sm"
                  onClick={() => {
                    onSectionChange?.("rows");
                    void browser.runPreview();
                  }}
                  disabled={!object.capabilities.preview_rows || browser.previewLoading}
                >
                  <Rows3 />
                  Preview rows
                </Button>
              </div>
              <TabsContent value="definition" className="min-h-0 flex-1 overflow-auto p-0">
                {object.view_definition ? (
                  <div data-testid="data-browser-view-definition" className="h-full">
                    <SqlPreview
                      query={object.view_definition}
                      className="h-full max-h-none border-0 p-4"
                    />
                  </div>
                ) : (
                  <p className="p-4 text-sm text-muted-foreground">
                    No view definition is available. This may be a table, an unsupported warehouse,
                    or a definition hidden by database permissions.
                  </p>
                )}
              </TabsContent>
              <TabsContent value="rows" className="min-h-0 flex-1 p-0">
                {browser.previewLoading ? (
                  <DataBrowserLoading label="Loading preview rows…" table />
                ) : browser.preview ? (
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
              <TabsContent value="schema" className="min-h-0 flex-1 overflow-auto p-0">
                {object.columns.length > 0 ? (
                  <div className="divide-y">
                    {object.columns.map((column, index) => (
                      <div
                        key={`${column.name}:${index}`}
                        tabIndex={focusedColumn === column.name ? -1 : undefined}
                        data-focused-column={focusedColumn === column.name || undefined}
                        ref={(element) => {
                          if (
                            element &&
                            focusedColumn === column.name &&
                            lastFocus.current !== focusKey
                          ) {
                            lastFocus.current = focusKey;
                            element.focus({ preventScroll: true });
                            element.scrollIntoView({ block: "nearest" });
                          }
                        }}
                        className="grid grid-cols-[minmax(0,1fr)_minmax(7rem,auto)] gap-3 px-4 py-2 text-xs data-[focused-column=true]:bg-primary/10 data-[focused-column=true]:ring-inset data-[focused-column=true]:ring-1 data-[focused-column=true]:ring-primary"
                      >
                        <span className="truncate font-mono">
                          {object.address ? (
                            <ResourceLink
                              target={{
                                kind: "data-object",
                                address: object.address,
                                section: "schema",
                                column: column.name,
                              }}
                              environment={object.environment}
                              className="hover:underline"
                            >
                              {column.name}
                            </ResourceLink>
                          ) : (
                            column.name
                          )}
                        </span>
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
          )}
        </>
      )}
    </section>
  );
}

// Independent from the navigator: opening a bookmark must not reset its tree,
// connection selection or the primary editor. Late responses cannot win.
export function DataObjectDetail({
  target,
  environment,
}: {
  target: DataTarget;
  environment: string;
}) {
  const navigate = useNavigate();
  const [retry, setRetry] = useState(0);
  const [object, setObject] = useState<DataBrowserObject | null>(null);
  const [preview, setPreview] = useState<DataBrowserPreviewResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [previewLoading, setPreviewLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const request = useRef<AbortController | null>(null);
  const addressKey = JSON.stringify(target.address);
  useEffect(() => {
    const controller = new AbortController();
    request.current = controller;
    setObject(null);
    setPreview(null);
    setError(null);
    setLoading(true);
    setPreviewLoading(false);
    void resolveDataBrowserObject(
      { address: JSON.parse(addressKey), environment },
      controller.signal,
    )
      .then((response) => {
        if (!controller.signal.aborted) setObject(response.object);
      })
      .catch((cause) => {
        if (!controller.signal.aborted)
          setError(cause instanceof Error ? cause.message : "Could not resolve this data object.");
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [addressKey, environment, retry]);
  const runPreview = async () => {
    const controller = request.current;
    if (!object || !controller || previewLoading) return;
    setPreviewLoading(true);
    setError(null);
    try {
      const result = await previewDataBrowserObject(
        { object_id: object.id, environment, limit: 100 },
        controller.signal,
      );
      if (!controller.signal.aborted) setPreview(result);
    } catch (cause) {
      if (!controller.signal.aborted)
        setError(cause instanceof Error ? cause.message : "Could not preview this object.");
    } finally {
      if (!controller.signal.aborted) setPreviewLoading(false);
    }
  };
  const column = target.column ? resolveColumn(object?.columns ?? [], target.column) : undefined;
  return (
    <div className="flex h-full min-h-0 min-w-0 flex-1 flex-col" data-testid="routed-data-object">
      {error ? (
        <div role="alert" className="p-3 text-sm">
          {error}
          <Button variant="outline" size="sm" onClick={() => setRetry((v) => v + 1)}>
            Refresh metadata
          </Button>
        </div>
      ) : null}
      {object && target.column && !column ? (
        <p role="alert" className="p-3 text-sm">
          The linked column is missing or ambiguous. No other column has been selected.
        </p>
      ) : null}
      <DataBrowserDetail
        className="flex-1"
        browser={{
          selectedObject: object,
          objectLoading: loading,
          preview,
          previewLoading,
          runPreview,
        }}
        section={target.section}
        focusedColumn={column?.name}
        onSectionChange={(section) =>
          void navigate({
            to: ".",
            search: (search) => ({
              ...search,
              detail: { v: 1, environment, target: { ...target, section } },
            }),
          })
        }
      />
    </div>
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
