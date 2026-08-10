import { AlertTriangle, ArrowUpRight, Download, Play, Plus, Trash2 } from "lucide-react";
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
} from "react";
import {
  Background,
  Controls,
  Handle,
  Position,
  ReactFlow,
  useReactFlow,
  useStore,
  useStoreApi,
  type Edge,
  type Node,
  type NodeProps,
  type NodeTypes,
  type ReactFlowInstance,
} from "reactflow";
import "reactflow/dist/style.css";

import { Button } from "@/components/ui/button";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  computeAppLineageLayout,
  initialCenteredViewportX,
  type AppLineageLayoutEdge,
} from "@/lib/app-lineage-layout";
import { assetNameParts } from "@/lib/asset-presentation";
import { cn } from "@/lib/utils";

import { kindMeta, type AppAsset } from "./app-data";
import { AssetNode, AssetNodeMenuItems, type AssetNodeAction } from "./app-primitives";

export type AppLineageCanvasAsset = AppAsset & {
  displayName?: string;
  prefix?: string;
  pipelineId?: string;
  isMaterialized?: boolean;
  upstreams?: string[];
  provisionalUpstreams?: string[];
  readOnly?: boolean;
};

type AssetNodeData = {
  asset: AppLineageCanvasAsset;
  selected: boolean;
  highlighted: boolean;
  dimmed: boolean;
  onSelect?: (assetId: string) => void;
  onCreateDownstream?: (assetId: string) => void;
  onOpenConnection?: (assetId: string) => void;
  onReviewFailedCheck?: (assetId: string) => void;
  actions?: AssetNodeAction[];
};

type PrefixGroupNodeData = {
  label: string;
  count: number;
  width: number;
  height: number;
};

const overviewZoomThreshold = 0.55;

export { assetNameParts } from "@/lib/asset-presentation";

export function assetGroupName(asset: AppLineageCanvasAsset) {
  return asset.prefix || asset.group || "root";
}

export function assetDisplayName(asset: AppLineageCanvasAsset) {
  return asset.displayName || assetNameParts(asset.name).title;
}

function PrefixGroupFlowNode({ data }: NodeProps<PrefixGroupNodeData>) {
  const overview = useStore((state) => state.transform[2] < overviewZoomThreshold);
  return (
    <div
      className="pointer-events-none relative rounded-2xl border bg-background/50"
      style={{ width: data.width, height: data.height }}
    >
      {!overview ? (
        <div className="absolute left-3 top-2.5 flex items-center gap-2">
          <span className="font-mono text-xs font-semibold">{data.label}</span>
          <span className="rounded-full bg-primary/10 px-1.5 text-[10px] text-primary">
            {data.count}
          </span>
        </div>
      ) : null}
    </div>
  );
}

function AssetFlowNode({ data }: NodeProps<AssetNodeData>) {
  const overview = useStore((state) => state.transform[2] < overviewZoomThreshold);
  const displayAsset = {
    ...data.asset,
    name: assetDisplayName(data.asset),
  };
  const actions = data.actions;

  const card = (
    <div
      role="button"
      tabIndex={0}
      data-testid="lineage-asset"
      data-asset-id={data.asset.id}
      className="cursor-pointer text-left outline-none"
      onClick={() => data.onSelect?.(data.asset.id)}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          data.onSelect?.(data.asset.id);
        }
      }}
    >
      {overview ? (
        <AssetOverviewNode asset={displayAsset} selected={data.selected} />
      ) : (
        <AssetNode
          asset={displayAsset}
          selected={data.selected}
          actions={actions}
          onOpenConnection={
            data.onOpenConnection ? () => data.onOpenConnection?.(data.asset.id) : undefined
          }
          onReviewFailedCheck={
            data.onReviewFailedCheck ? () => data.onReviewFailedCheck?.(data.asset.id) : undefined
          }
        />
      )}
    </div>
  );

  return (
    <div
      data-sql-hover-highlight={data.highlighted ? "true" : undefined}
      className={cn(
        "group relative rounded-xl transition-opacity",
        data.highlighted && "asset-canvas-sql-hover-highlight",
      )}
      style={{ opacity: data.dimmed ? 0.18 : 1 }}
    >
      <Handle className="asset-node-hidden-handle" type="target" position={Position.Left} />
      {actions && actions.length > 0 ? (
        <ContextMenu>
          <ContextMenuTrigger>{card}</ContextMenuTrigger>
          <ContextMenuContent>
            <ContextMenuItem disabled className="font-mono text-xs">
              {assetDisplayName(data.asset)}
            </ContextMenuItem>
            <ContextMenuSeparator />
            <AssetNodeMenuItems
              actions={actions}
              ItemComponent={ContextMenuItem}
              SeparatorComponent={ContextMenuSeparator}
            />
          </ContextMenuContent>
        </ContextMenu>
      ) : (
        card
      )}
      <Handle className="asset-node-hidden-handle" type="source" position={Position.Right} />
      {data.onCreateDownstream ? (
        <button
          type="button"
          title="Create downstream asset"
          aria-label="Create downstream asset"
          className="absolute -right-3.5 top-1/2 hidden size-7 -translate-y-1/2 items-center justify-center rounded-full border bg-background text-muted-foreground shadow-sm transition-colors hover:bg-primary hover:text-primary-foreground group-hover:flex"
          onClick={(event) => {
            event.stopPropagation();
            data.onCreateDownstream?.(data.asset.id);
          }}
        >
          <Plus className="size-4" />
        </button>
      ) : null}
    </div>
  );
}

function AssetOverviewNode({ asset, selected }: { asset: AppAsset; selected: boolean }) {
  const Icon = kindMeta[asset.kind].icon;
  const hasError = Boolean(asset.parseError || asset.hasTypeCheckError);
  return (
    <div
      data-testid="lineage-asset-overview"
      className={cn(
        "relative flex h-28 w-58 items-center justify-center overflow-hidden rounded-xl border-2 bg-card shadow-sm",
        hasError ? "border-amber-400" : selected ? "border-primary" : "border-border",
      )}
    >
      <div
        className={cn(
          "flex size-14 items-center justify-center rounded-2xl border bg-muted/60 text-muted-foreground",
          selected && "border-primary/50 bg-primary/10 text-primary",
        )}
      >
        <Icon className="size-7" />
      </div>
      {hasError ? (
        <span className="absolute right-3 top-3 flex size-6 items-center justify-center rounded-full bg-amber-100 text-amber-700 dark:bg-amber-500/20 dark:text-amber-300">
          <AlertTriangle className="size-3.5" />
        </span>
      ) : null}
    </div>
  );
}

const nodeTypes = {
  prefixGroup: PrefixGroupFlowNode,
  lineageAsset: AssetFlowNode,
} satisfies NodeTypes;

// React Flow hides controlled nodes until both dimensions are initialized.
// Status-only updates replace our derived node objects, so carry forward the
// measured dimensions (or the layout defaults on first render). Otherwise React
// Flow marks every replacement visibility:hidden until it manages to remeasure.
const assetNodeWidth = 232;
const assetNodeHeight = 112;

// Pans/zooms the viewport onto an asset when it is targeted from outside the
// canvas (e.g. routing here from the build view). Runs only when the id changes
// so it never fights the user's own panning.
function ViewportFocus({ assetId, nodes }: { assetId?: string; nodes: Node[] }) {
  const { setCenter } = useReactFlow();
  const store = useStoreApi();
  const lastFocused = useRef<string | null>(null);

  useEffect(() => {
    if (!assetId) {
      lastFocused.current = null;
      return;
    }
    if (lastFocused.current === assetId) {
      return;
    }
    const node = nodes.find((candidate) => candidate.id === assetId);
    if (!node) {
      return;
    }
    const width = node.width ?? assetNodeWidth;
    const height = node.height ?? assetNodeHeight;
    let secondFrame = 0;
    const firstFrame = window.requestAnimationFrame(() => {
      secondFrame = window.requestAnimationFrame(() => {
        lastFocused.current = assetId;
        const state = store.getState();
        const [translateX, translateY, zoom] = state.transform;
        const padding = 40;
        const left = node.position.x * zoom + translateX;
        const top = node.position.y * zoom + translateY;
        const right = left + width * zoom;
        const bottom = top + height * zoom;
        const inView =
          left >= padding &&
          top >= padding &&
          right <= state.width - padding &&
          bottom <= state.height - padding;
        if (inView) {
          return;
        }
        void setCenter(node.position.x + width / 2, node.position.y + height / 2, {
          zoom,
          duration: 500,
        });
      });
    });

    return () => {
      window.cancelAnimationFrame(firstFrame);
      window.cancelAnimationFrame(secondFrame);
    };
  }, [assetId, nodes, setCenter, store]);

  return null;
}

function derivedEdges(assets: AppLineageCanvasAsset[], links?: AppLineageLayoutEdge[]) {
  if (links) return links;
  const assetByName = new Map<string, AppLineageCanvasAsset>();
  const assetById = new Map<string, AppLineageCanvasAsset>();
  assets.forEach((asset) => {
    assetByName.set(asset.name, asset);
    assetById.set(asset.id, asset);
  });
  return assets.flatMap((asset) =>
    (asset.upstreams ?? [])
      .map((upstream) => assetByName.get(upstream) ?? assetById.get(upstream))
      .filter((source): source is AppLineageCanvasAsset => Boolean(source))
      .map((source) => ({
        source: source.id,
        target: asset.id,
        provisional: (asset.provisionalUpstreams ?? []).some(
          (upstream) => upstream === source.id || upstream === source.name,
        ),
      })),
  );
}

function lineageFor(assetId: string, edges: AppLineageLayoutEdge[]) {
  const preds = new Map<string, string[]>();
  const succs = new Map<string, string[]>();
  edges.forEach((edge) => {
    preds.set(edge.target, [...(preds.get(edge.target) ?? []), edge.source]);
    succs.set(edge.source, [...(succs.get(edge.source) ?? []), edge.target]);
  });

  const walk = (start: string, adjacency: Map<string, string[]>) => {
    const seen = new Set<string>();
    const stack = [start];
    while (stack.length) {
      const id = stack.pop();
      if (!id) continue;
      for (const next of adjacency.get(id) ?? []) {
        if (seen.has(next)) continue;
        seen.add(next);
        stack.push(next);
      }
    }
    return seen;
  };

  const upstream = walk(assetId, preds);
  const downstream = walk(assetId, succs);
  return { upstream, downstream, all: new Set([assetId, ...upstream, ...downstream]) };
}

export function AppLineageCanvas({
  assets,
  links,
  selectedAssetId,
  focusAssetId,
  highlightAssetId,
  onAssetSelect,
  onCreateDownstream,
  onCreateAsset,
  onRunAsset,
  onDeleteAsset,
  onGoToAsset,
  onAssetConnectionClick,
  onReviewFailedCheck,
  onImportExternalRelation,
  goToLabel,
}: {
  assets: AppLineageCanvasAsset[];
  links?: AppLineageLayoutEdge[];
  selectedAssetId?: string;
  focusAssetId?: string;
  highlightAssetId?: string;
  onAssetSelect?: (assetId: string) => void;
  onCreateDownstream?: (assetId: string) => void;
  // Right-click on the canvas offers "New asset"; when the click lands inside
  // a prefix group box, that prefix is passed along as the default.
  onCreateAsset?: (options: { prefix?: string }) => void;
  onRunAsset?: (assetId: string) => void;
  onDeleteAsset?: (assetId: string) => void;
  onGoToAsset?: (assetId: string) => void;
  // Clicking a node's connection badge; used to jump to the pipeline's
  // connection settings.
  onAssetConnectionClick?: (assetId: string) => void;
  // Opens the asset properties focused on the first failed quality assertion.
  onReviewFailedCheck?: (assetId: string) => void;
  // Imports an ephemeral, positively observed external relation as an authored
  // source-placeholder asset.
  onImportExternalRelation?: (relationId: string) => void;
  goToLabel?: string;
}) {
  const [lineageAssetId, setLineageAssetId] = useState<string | null>(null);
  const [pendingDelete, setPendingDelete] = useState<{ id: string; name: string } | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance | null>(null);
  const [paneMenu, setPaneMenu] = useState<{ x: number; y: number; prefix?: string } | null>(null);

  useEffect(() => {
    setLineageAssetId((current) => (current && current !== selectedAssetId ? null : current));
  }, [selectedAssetId]);

  // Parents (e.g. the split-view build page) re-render and hand us fresh
  // callback identities on every keystroke in the SQL editor. Route the
  // callbacks through a ref and depend only on their *presence* so the layout
  // memo below recomputes when the graph actually changes — not on every
  // keystroke, which otherwise re-renders every React Flow node and flickers.
  const callbacksRef = useRef({
    selectedAssetId,
    onAssetSelect,
    onCreateDownstream,
    onRunAsset,
    onDeleteAsset,
    onGoToAsset,
    onAssetConnectionClick,
    onReviewFailedCheck,
    onImportExternalRelation,
  });
  callbacksRef.current = {
    selectedAssetId,
    onAssetSelect,
    onCreateDownstream,
    onRunAsset,
    onDeleteAsset,
    onGoToAsset,
    onAssetConnectionClick,
    onReviewFailedCheck,
    onImportExternalRelation,
  };

  const handleSelect = useCallback((assetId: string) => {
    const { onAssetSelect: select, selectedAssetId: currentSelected } = callbacksRef.current;
    if (!select) {
      setLineageAssetId((current) => (current === assetId ? null : assetId));
      return;
    }
    if (assetId === currentSelected) {
      setLineageAssetId((current) => (current === assetId ? null : assetId));
      return;
    }
    setLineageAssetId(null);
    select(assetId);
  }, []);
  const handleCreateDownstream = useCallback(
    (assetId: string) => callbacksRef.current.onCreateDownstream?.(assetId),
    [],
  );
  const handleOpenConnection = useCallback(
    (assetId: string) => callbacksRef.current.onAssetConnectionClick?.(assetId),
    [],
  );
  const handleReviewFailedCheck = useCallback(
    (assetId: string) => callbacksRef.current.onReviewFailedCheck?.(assetId),
    [],
  );

  const hasCreateDownstream = Boolean(onCreateDownstream);
  const hasConnectionClick = Boolean(onAssetConnectionClick);
  const hasQualityReview = Boolean(onReviewFailedCheck);
  const hasRun = Boolean(onRunAsset);
  const hasGoTo = Boolean(onGoToAsset);
  const hasDelete = Boolean(onDeleteAsset);
  const hasExternalImport = Boolean(onImportExternalRelation);

  const { nodes, edges } = useMemo(() => {
    const graphEdges = derivedEdges(assets, links);
    const lineage = lineageAssetId ? lineageFor(lineageAssetId, graphEdges) : null;
    const visuallySelectedAssetId = selectedAssetId ?? lineageAssetId ?? undefined;
    const layout = computeAppLineageLayout({
      nodes: assets.map((asset) => ({
        id: asset.id,
        layer: assetGroupName(asset),
        name: assetDisplayName(asset),
      })),
      edges: graphEdges,
    });

    const assetsByGroup = assets.reduce<Record<string, AppLineageCanvasAsset[]>>(
      (groups, asset) => {
        const group = assetGroupName(asset);
        groups[group] = [...(groups[group] ?? []), asset];
        return groups;
      },
      {},
    );

    const groupNodes: Node<PrefixGroupNodeData>[] = Object.entries(assetsByGroup).map(
      ([group, groupAssets]) => {
        const positionedAssets = groupAssets
          .map((asset) => ({ asset, position: layout.positions.get(asset.id) }))
          .filter(
            (item): item is { asset: AppLineageCanvasAsset; position: { x: number; y: number } } =>
              Boolean(item.position),
          );
        const minX = Math.min(...positionedAssets.map(({ position }) => position.x)) - 16;
        const minY = Math.min(...positionedAssets.map(({ position }) => position.y)) - 42;
        const maxX = Math.max(...positionedAssets.map(({ position }) => position.x)) + 248;
        const maxY = Math.max(...positionedAssets.map(({ position }) => position.y)) + 128;
        return {
          id: `prefix-group-${group}`,
          type: "prefixGroup",
          position: { x: minX, y: minY },
          width: maxX - minX,
          height: maxY - minY,
          data: {
            label: group,
            count: groupAssets.length,
            width: maxX - minX,
            height: maxY - minY,
          },
          draggable: false,
          selectable: false,
          connectable: false,
          zIndex: 0,
        };
      },
    );

    const assetNodes: Node<AssetNodeData>[] = assets.map((asset) => {
      const measured = flowInstance?.getNode(asset.id);
      const actions: AssetNodeAction[] = [];
      if (asset.isExternal && hasExternalImport) {
        actions.push({
          key: "import-external-relation",
          label: "Import as asset",
          icon: Download,
          onSelect: () => callbacksRef.current.onImportExternalRelation?.(asset.id),
        });
      }
      if (!asset.readOnly && hasRun) {
        actions.push({
          key: "run",
          label: "Run",
          icon: Play,
          onSelect: () => callbacksRef.current.onRunAsset?.(asset.id),
        });
      }
      if (hasGoTo && (!asset.readOnly || !asset.isExternal)) {
        actions.push({
          key: "go-to",
          label: asset.readOnly ? "Open producer pipeline" : (goToLabel ?? "Open"),
          icon: ArrowUpRight,
          onSelect: () => callbacksRef.current.onGoToAsset?.(asset.id),
        });
      }
      if (!asset.readOnly && hasDelete) {
        actions.push({
          key: "delete",
          label: "Delete",
          icon: Trash2,
          destructive: true,
          separatorBefore: actions.length > 0,
          onSelect: () => setPendingDelete({ id: asset.id, name: assetDisplayName(asset) }),
        });
      }
      return {
        id: asset.id,
        type: "lineageAsset",
        position: layout.positions.get(asset.id) ?? { x: asset.x, y: asset.y },
        width: measured?.width ?? assetNodeWidth,
        height: measured?.height ?? assetNodeHeight,
        data: {
          asset,
          selected: asset.id === visuallySelectedAssetId,
          highlighted: asset.id === highlightAssetId,
          dimmed: Boolean(lineage && !lineage.all.has(asset.id)),
          onSelect: asset.readOnly
            ? () => setLineageAssetId((current) => (current === asset.id ? null : asset.id))
            : handleSelect,
          onCreateDownstream:
            !asset.readOnly && hasCreateDownstream ? handleCreateDownstream : undefined,
          onOpenConnection: hasConnectionClick ? handleOpenConnection : undefined,
          onReviewFailedCheck: hasQualityReview ? handleReviewFailedCheck : undefined,
          actions: actions.length > 0 ? actions : undefined,
        },
        zIndex: 2,
      };
    });

    const edges: Edge[] = graphEdges.map((edge) => {
      const active = Boolean(
        lineage && lineage.all.has(edge.source) && lineage.all.has(edge.target),
      );
      const dimmed = Boolean(lineage && !active);
      return {
        id: `${edge.source}-${edge.target}`,
        source: edge.source,
        target: edge.target,
        type: "default",
        className: edge.provisional
          ? "asset-edge-provisional"
          : active
            ? "asset-edge-active"
            : "asset-edge",
        animated: active && !edge.provisional,
        style: {
          stroke: active || edge.provisional ? undefined : "#a1a1aa",
          strokeWidth: active || edge.provisional ? undefined : 1.5,
          opacity: dimmed ? 0.12 : 1,
        },
      };
    });

    return { nodes: [...groupNodes, ...assetNodes], edges };
  }, [
    assets,
    goToLabel,
    highlightAssetId,
    lineageAssetId,
    links,
    selectedAssetId,
    flowInstance,
    hasCreateDownstream,
    hasConnectionClick,
    hasQualityReview,
    hasRun,
    hasGoTo,
    hasDelete,
    hasExternalImport,
    handleSelect,
    handleCreateDownstream,
    handleOpenConnection,
    handleReviewFailedCheck,
  ]);

  const centeredGraphRef = useRef<string | null>(null);
  useEffect(() => {
    if (!flowInstance || nodes.length === 0) {
      return;
    }
    const graphKey = nodes
      .map((node) => `${node.id}:${node.position.x}:${node.position.y}`)
      .join("|");
    if (centeredGraphRef.current === graphKey) {
      return;
    }
    const frame = window.requestAnimationFrame(() => {
      const viewportWidth = containerRef.current?.getBoundingClientRect().width ?? 0;
      const graphMinX = Math.min(...nodes.map((node) => node.position.x));
      const graphMaxX = Math.max(
        ...nodes.map((node) => node.position.x + (node.width ?? assetNodeWidth)),
      );
      const viewport = flowInstance.getViewport();
      const x = initialCenteredViewportX({
        viewportWidth,
        graphMinX,
        graphMaxX,
        zoom: viewport.zoom,
      });
      if (x === null) {
        if (viewportWidth > 0) {
          centeredGraphRef.current = graphKey;
        }
        return;
      }
      centeredGraphRef.current = graphKey;
      void flowInstance.setViewport({ ...viewport, x });
    });

    return () => window.cancelAnimationFrame(frame);
  }, [flowInstance, nodes]);

  const handlePaneContextMenu = useCallback(
    (event: ReactMouseEvent | MouseEvent) => {
      if (!onCreateAsset) {
        return;
      }
      event.preventDefault();
      const container = containerRef.current;
      if (!container) {
        return;
      }
      const bounds = container.getBoundingClientRect();
      // Hit-test the prefix group boxes to default the new asset's prefix.
      let prefix: string | undefined;
      const flowPosition = flowInstance?.screenToFlowPosition({
        x: event.clientX,
        y: event.clientY,
      });
      if (flowPosition) {
        for (const node of nodes) {
          if (node.type !== "prefixGroup") continue;
          const data = node.data as PrefixGroupNodeData;
          if (
            flowPosition.x >= node.position.x &&
            flowPosition.x <= node.position.x + data.width &&
            flowPosition.y >= node.position.y &&
            flowPosition.y <= node.position.y + data.height
          ) {
            if (data.label && data.label !== "root" && data.label !== "ASSETS") {
              prefix = data.label;
            }
            break;
          }
        }
      }
      setPaneMenu({ x: event.clientX - bounds.left, y: event.clientY - bounds.top, prefix });
    },
    [flowInstance, nodes, onCreateAsset],
  );

  return (
    <div ref={containerRef} className="relative h-full min-h-0 bg-muted/40">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        nodesDraggable={false}
        nodesConnectable={false}
        deleteKeyCode={null}
        panActivationKeyCode={null}
        proOptions={{ hideAttribution: true }}
        minZoom={0.15}
        onInit={setFlowInstance}
        onPaneContextMenu={handlePaneContextMenu}
        onPaneClick={() => setPaneMenu(null)}
        onMoveStart={() => setPaneMenu(null)}
      >
        <Background
          gap={22}
          size={1.2}
          color="var(--muted-foreground)"
          className="opacity-40 dark:opacity-50"
        />
        <Controls position="bottom-left" />
        <ViewportFocus assetId={focusAssetId} nodes={nodes} />
      </ReactFlow>
      {paneMenu && onCreateAsset ? (
        <>
          {/* Click-away layer: any interaction outside the menu dismisses it. */}
          <div
            className="absolute inset-0 z-30"
            onClick={() => setPaneMenu(null)}
            onContextMenu={(event) => {
              event.preventDefault();
              setPaneMenu(null);
            }}
          />
          <div
            className="absolute z-40 min-w-44 rounded-md border bg-popover p-1 text-popover-foreground shadow-md"
            style={{ left: paneMenu.x, top: paneMenu.y }}
          >
            <button
              type="button"
              className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-xs hover:bg-accent hover:text-accent-foreground"
              onClick={() => {
                const prefix = paneMenu.prefix;
                setPaneMenu(null);
                onCreateAsset({ prefix });
              }}
            >
              <Plus className="size-3.5" />
              {paneMenu.prefix ? (
                <span className="min-w-0 truncate">
                  New asset in <span className="font-mono">{paneMenu.prefix}</span>
                </span>
              ) : (
                "New asset"
              )}
            </button>
          </div>
        </>
      ) : null}
      <Dialog
        open={Boolean(pendingDelete)}
        onOpenChange={(open) => {
          if (!open && !deleteLoading) {
            setPendingDelete(null);
          }
        }}
      >
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete asset?</DialogTitle>
            <DialogDescription>
              This will permanently delete{" "}
              {pendingDelete ? (
                <span className="font-mono">{pendingDelete.name}</span>
              ) : (
                "this asset"
              )}{" "}
              from the pipeline.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={deleteLoading}
              onClick={() => setPendingDelete(null)}
            >
              Cancel
            </Button>
            <Button
              type="button"
              variant="destructive"
              disabled={deleteLoading}
              onClick={() => {
                if (!pendingDelete) {
                  return;
                }
                setDeleteLoading(true);
                void Promise.resolve(onDeleteAsset?.(pendingDelete.id)).finally(() => {
                  setDeleteLoading(false);
                  setPendingDelete(null);
                });
              }}
            >
              {deleteLoading ? "Deleting..." : "Delete"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
