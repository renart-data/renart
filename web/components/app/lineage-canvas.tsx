import {
  AlertTriangle,
  ArrowLeftRight,
  ArrowUpRight,
  Cpu,
  Database,
  Download,
  FileCode,
  GitMerge,
  Link2,
  MoreHorizontal,
  Play,
  Plus,
  Sprout,
  Trash2,
  X,
} from "lucide-react";
import {
  useCallback,
  useDeferredValue,
  useEffect,
  useMemo,
  useRef,
  useState,
  type MouseEvent as ReactMouseEvent,
  type TouchEvent as ReactTouchEvent,
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
  type NodeChange,
  type NodeProps,
  type NodeTypes,
  type OnConnectStartParams,
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
import { pendingNodePosition } from "@/lib/quick-create";
import { cn } from "@/lib/utils";

import { kindMeta, type AppAsset } from "./app-data";
import {
  DataBrowserLoadDropTarget,
  DataBrowserSourceDropTarget,
  useDataBrowserDropGroups,
  useDataBrowserLoadTargets,
  useDataBrowserExpandedTarget,
} from "./data-browser/data-browser-canvas";
import {
  AssetNode,
  AssetNodeMenuItems,
  useAssetNodeMenuFocus,
  resolveFreshnessDisplay,
  type AssetNodeAction,
} from "./app-primitives";
import { QuickCreateNode, type QuickCreateCanvasController } from "./quick-create-node";

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
  preview?: boolean;
  // Part of a multi-selection that can start a joined asset.
  multiSelected?: boolean;
};

type PrefixGroupNodeData = {
  label: string;
  count: number;
  width: number;
  height: number;
};

const overviewZoomThreshold = 0.55;
// Below this zoom a preview canvas swaps detailed cards for large-label ones,
// so asset names and statuses stay legible in a small pane.
const previewDetailZoomThreshold = 0.75;

// The view-transition name shared by the workspace's pipeline canvas and the
// first-run canvas on the welcome screen.
export const PIPELINE_CANVAS_TRANSITION = "pipeline-canvas";

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
      <DataBrowserSourceDropTarget group={data.label} />
    </div>
  );
}

function AssetFlowNode({ data }: NodeProps<AssetNodeData>) {
  const overview = useStore((state) => state.transform[2] < overviewZoomThreshold);
  const compactPreview = useStore(
    (state) => Boolean(data.preview) && state.transform[2] < previewDetailZoomThreshold,
  );
  const placingInGroup = useDataBrowserDropGroups().has(assetGroupName(data.asset));
  const menuFocus = useAssetNodeMenuFocus();
  const displayAsset = {
    ...data.asset,
    name: assetDisplayName(data.asset),
  };
  const actions = data.actions;
  const canCreateDownstream = Boolean(data.onCreateDownstream) && !placingInGroup;

  const card = (
    <div
      role="button"
      tabIndex={0}
      data-testid="lineage-asset"
      data-asset-id={data.asset.id}
      inert={placingInGroup || undefined}
      className={cn(
        "cursor-pointer rounded-xl text-left outline-none",
        data.multiSelected && "ring-2 ring-primary ring-offset-2 ring-offset-background",
      )}
      onClick={(event) => {
        // Shift-click adds the card to a multi-selection instead of opening it.
        if (event.shiftKey) return;
        data.onSelect?.(data.asset.id);
      }}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          data.onSelect?.(data.asset.id);
        }
      }}
    >
      {compactPreview ? (
        <AssetPreviewNode asset={displayAsset} selected={data.selected} />
      ) : overview ? (
        <AssetOverviewNode asset={displayAsset} selected={data.selected} />
      ) : (
        <AssetNode
          asset={displayAsset}
          runAssetName={data.asset.name}
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
      <Handle
        className="asset-node-hidden-handle"
        type="target"
        position={Position.Left}
        isConnectableStart={false}
      />
      {actions && actions.length > 0 ? (
        <ContextMenu>
          <ContextMenuTrigger>{card}</ContextMenuTrigger>
          <ContextMenuContent onCloseAutoFocus={menuFocus.onCloseAutoFocus}>
            <ContextMenuItem disabled className="font-mono text-xs">
              {assetDisplayName(data.asset)}
            </ContextMenuItem>
            <ContextMenuSeparator />
            <AssetNodeMenuItems
              actions={actions}
              ItemComponent={ContextMenuItem}
              SeparatorComponent={ContextMenuSeparator}
              onActionSelect={menuFocus.onActionSelect}
            />
          </ContextMenuContent>
        </ContextMenu>
      ) : (
        card
      )}
      <Handle
        className="asset-node-hidden-handle"
        type="source"
        position={Position.Right}
        isConnectable={false}
      />
      {/* The + is a second output handle (edges anchor on the first one): a
          click creates a downstream asset, a drag connects this asset to
          another one or to empty space. */}
      {canCreateDownstream ? (
        <Handle
          id={createHandleId}
          type="source"
          position={Position.Right}
          className="asset-node-plus-handle"
          role="button"
          tabIndex={0}
          title="Create downstream asset (drag to connect)"
          aria-label="Create downstream asset"
          onClick={(event) => {
            event.stopPropagation();
            data.onCreateDownstream?.(data.asset.id);
          }}
          onKeyDown={(event) => {
            if (event.key !== "Enter" && event.key !== " ") return;
            event.preventDefault();
            event.stopPropagation();
            data.onCreateDownstream?.(data.asset.id);
          }}
        >
          <Plus className="pointer-events-none size-4" />
        </Handle>
      ) : null}
      <DataBrowserLoadDropTarget assetId={data.asset.id} label={data.asset.name} />
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

function AssetPreviewNode({ asset, selected }: { asset: AppAsset; selected: boolean }) {
  const Icon = kindMeta[asset.kind].icon;
  const running = asset.status === "pending";
  const failed = asset.status === "failed";
  const freshness = asset.staleness ? resolveFreshnessDisplay(asset.staleness) : null;
  return (
    <div
      data-slot="asset-node"
      data-testid="lineage-asset-preview"
      className={cn(
        "flex h-28 w-58 flex-col justify-center gap-3 overflow-hidden rounded-xl border-2 bg-card px-4 shadow-sm",
        failed ? "border-destructive" : running || selected ? "border-primary" : "border-border",
      )}
    >
      <div className="flex min-w-0 items-center gap-2.5">
        <Icon className="size-6 shrink-0 text-muted-foreground" />
        <span className="min-w-0 truncate font-mono text-xl font-semibold">{asset.name}</span>
      </div>
      <div
        className="flex min-w-0 items-center gap-2 text-base text-muted-foreground"
        data-staleness={running || failed ? undefined : asset.staleness?.status}
      >
        <span
          className={cn(
            "size-2.5 shrink-0 rounded-full",
            failed
              ? "bg-destructive"
              : running
                ? "animate-pulse bg-primary"
                : (freshness?.dotClassName ?? "bg-zinc-400"),
          )}
        />
        <span className="truncate">
          {failed ? "Failed" : running ? "Running" : (freshness?.label ?? "Asset")}
        </span>
      </div>
    </div>
  );
}

type QuickCreateNodeData = { controller: QuickCreateCanvasController };

function QuickCreateFlowNode({ data }: NodeProps<QuickCreateNodeData>) {
  return (
    <div className="relative">
      <Handle
        className="asset-node-hidden-handle"
        type="target"
        position={Position.Left}
        isConnectable={false}
      />
      <QuickCreateNode controller={data.controller} />
    </div>
  );
}

const quickCreateNodeId = "quick-create";
const createHandleId = "create";

const nodeTypes = {
  prefixGroup: PrefixGroupFlowNode,
  lineageAsset: AssetFlowNode,
  quickCreate: QuickCreateFlowNode,
} satisfies NodeTypes;

// React Flow hides controlled nodes until both dimensions are initialized.
// Status-only updates replace our derived node objects, so carry forward the
// measured dimensions (or the layout defaults on first render). Otherwise React
// Flow marks every replacement visibility:hidden until it manages to remeasure.
const assetNodeWidth = 232;
const assetNodeHeight = 112;
// Room the pending quick-create card takes when placing it.
const pendingNodeHeight = 148;

function pointerPosition(event: ReactMouseEvent | ReactTouchEvent | MouseEvent | TouchEvent) {
  if ("changedTouches" in event) {
    const touch = event.changedTouches[0] ?? event.touches[0];
    return { x: touch?.clientX ?? 0, y: touch?.clientY ?? 0 };
  }
  return { x: event.clientX, y: event.clientY };
}

// Pans/zooms the viewport onto an asset when it is targeted from outside the
// canvas (e.g. routing here from the build view). Runs only when the focus key
// changes so it never fights the user's own panning. A centered focus (a newly
// created asset) always centers the node; otherwise it pans only when the node
// is out of view.
function ViewportFocus({
  assetId,
  focusKey = assetId,
  nodes,
  center = false,
}: {
  assetId?: string;
  focusKey?: string;
  nodes: Node[];
  center?: boolean;
}) {
  const { setCenter, setViewport } = useReactFlow();
  const store = useStoreApi();
  const lastFocused = useRef<string | null>(null);

  useEffect(() => {
    if (!assetId || !focusKey) {
      lastFocused.current = null;
      return;
    }
    if (lastFocused.current === focusKey) {
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
        lastFocused.current = focusKey;
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
        if (inView && !center) {
          return;
        }
        if (center) {
          void setCenter(node.position.x + width / 2, node.position.y + height / 2, {
            zoom,
            duration: 500,
          });
          return;
        }
        // Pan just far enough to show the node, so what the user was looking
        // at (such as the source of a new asset) stays in view where it can.
        const dx =
          left < padding
            ? padding - left
            : right > state.width - padding
              ? state.width - padding - right
              : 0;
        const dy =
          top < padding
            ? padding - top
            : bottom > state.height - padding
              ? state.height - padding - bottom
              : 0;
        void setViewport({ x: translateX + dx, y: translateY + dy, zoom }, { duration: 300 });
      });
    });

    return () => {
      window.cancelAnimationFrame(firstFrame);
      window.cancelAnimationFrame(secondFrame);
    };
  }, [assetId, center, focusKey, nodes, setCenter, setViewport, store]);

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

function LinkMenuItem({
  icon: Icon,
  label,
  description,
  working,
  disabled,
  onSelect,
}: {
  icon: typeof Plus;
  label: string;
  description: string;
  working: boolean;
  disabled: boolean;
  onSelect: () => void;
}) {
  return (
    <button
      type="button"
      role="menuitem"
      disabled={disabled}
      className="flex w-full items-start gap-2 rounded-sm px-2 py-1.5 text-left text-xs hover:bg-accent hover:text-accent-foreground disabled:opacity-60"
      onClick={onSelect}
    >
      {working ? (
        <span className="mt-0.5 size-3.5 shrink-0 animate-spin rounded-full border-2 border-current border-t-transparent" />
      ) : (
        <Icon className="mt-0.5 size-3.5 shrink-0" />
      )}
      <span className="grid min-w-0 gap-0.5">
        <span className="font-medium">{label}</span>
        <span className="truncate text-[11px] text-muted-foreground">{description}</span>
      </span>
    </button>
  );
}

export type CanvasCreateKind = "sql" | "python" | "seed" | "load" | "import";

export type CanvasAssetLink = {
  sourceId: string;
  targetId: string;
  mode: "join" | "dependency";
};

type LinkMenuState = {
  x: number;
  y: number;
  sourceId: string;
  targetId: string;
  working?: CanvasAssetLink["mode"];
  error?: string;
};

const paneCreateItems: { kind: CanvasCreateKind; label: string; icon: typeof Plus }[] = [
  { kind: "sql", label: "SQL asset", icon: FileCode },
  { kind: "python", label: "Python asset", icon: Cpu },
  { kind: "seed", label: "Seed from a file", icon: Sprout },
  { kind: "load", label: "Load from a table", icon: ArrowLeftRight },
  { kind: "import", label: "Import tables…", icon: Database },
];

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
  quickCreate,
  onQuickCreate,
  onLinkAssets,
  revealAssetId,
  goToLabel,
  preview = false,
  viewTransitionName,
}: {
  assets: AppLineageCanvasAsset[];
  links?: AppLineageLayoutEdge[];
  selectedAssetId?: string;
  focusAssetId?: string;
  highlightAssetId?: string;
  onAssetSelect?: (assetId: string) => void;
  onCreateDownstream?: (assetId: string) => void;
  // Right-click on the canvas lists the asset kinds; when the click lands
  // inside a prefix group box, that prefix is passed along as the default.
  // No kind means the full creation dialog.
  onCreateAsset?: (options: { prefix?: string; kind?: CanvasCreateKind }) => void;
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
  // The pending asset shown while quick create is open.
  quickCreate?: QuickCreateCanvasController;
  // Opens quick create downstream of these assets; a position (in flow
  // coordinates) places the pending node where a drag ended.
  onQuickCreate?: (sourceIds: string[], position?: { x: number; y: number }) => void;
  // A drag from one asset onto another: join it in the target's query or add
  // a manual dependency. Rejects with a message the link menu shows.
  onLinkAssets?: (link: CanvasAssetLink) => Promise<void>;
  // A newly created asset to center and highlight until the user moves.
  revealAssetId?: string;
  goToLabel?: string;
  // A fitted, non-scrolling canvas for previews outside the workspace (the
  // welcome screen). Edges into running assets animate so a run reads as
  // data flowing through the graph.
  preview?: boolean;
  // Pairs this canvas with another one across a view transition, such as the
  // first-run canvas expanding into the workspace canvas.
  viewTransitionName?: string;
}) {
  const [lineageAssetId, setLineageAssetId] = useState<string | null>(null);
  const [pendingDelete, setPendingDelete] = useState<{ id: string; name: string } | null>(null);
  const [deleteLoading, setDeleteLoading] = useState(false);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance | null>(null);
  const [paneMenu, setPaneMenu] = useState<{ x: number; y: number; prefix?: string } | null>(null);
  // Shift-click or Shift-drag selection, in selection order.
  const [multiSelection, setMultiSelection] = useState<string[]>([]);
  const [revealHighlightId, setRevealHighlightId] = useState<string | null>(null);
  const [linkMenu, setLinkMenu] = useState<LinkMenuState | null>(null);
  const connectStartRef = useRef<{ nodeId: string; x: number; y: number } | null>(null);
  // The editor content is the primary response to asset navigation. Let React
  // paint that urgent update before reconciling the selected canvas card; the
  // graph remains interactive and catches up immediately afterward.
  const deferredSelectedAssetId = useDeferredValue(selectedAssetId);
  const dropGroups = useDataBrowserDropGroups();
  const loadTargets = useDataBrowserLoadTargets();
  const expandedTarget = useDataBrowserExpandedTarget();

  useEffect(() => {
    setLineageAssetId((current) => (current && current !== selectedAssetId ? null : current));
  }, [selectedAssetId]);
  // A new asset stays highlighted for a few seconds, or until the user pans,
  // zooms or selects another asset.
  useEffect(() => {
    setRevealHighlightId(revealAssetId ?? null);
    if (!revealAssetId) return;
    const timer = window.setTimeout(() => setRevealHighlightId(null), 6000);
    return () => window.clearTimeout(timer);
  }, [revealAssetId]);
  useEffect(() => {
    setRevealHighlightId((current) =>
      current && selectedAssetId && selectedAssetId !== current ? null : current,
    );
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
    onQuickCreate,
    onRunAsset,
    onDeleteAsset,
    onGoToAsset,
    onAssetConnectionClick,
    onReviewFailedCheck,
    onImportExternalRelation,
    onLinkAssets,
  });
  callbacksRef.current = {
    selectedAssetId,
    onAssetSelect,
    onCreateDownstream,
    onQuickCreate,
    onRunAsset,
    onDeleteAsset,
    onGoToAsset,
    onAssetConnectionClick,
    onReviewFailedCheck,
    onImportExternalRelation,
    onLinkAssets,
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
  // The + and the menu item create in place when the page offers quick
  // create, and open the creation dialog otherwise.
  const handleCreateDownstream = useCallback((assetId: string) => {
    const { onQuickCreate: quick, onCreateDownstream: dialog } = callbacksRef.current;
    if (quick) quick([assetId]);
    else dialog?.(assetId);
  }, []);
  const handleOpenConnection = useCallback(
    (assetId: string) => callbacksRef.current.onAssetConnectionClick?.(assetId),
    [],
  );
  const handleReviewFailedCheck = useCallback(
    (assetId: string) => callbacksRef.current.onReviewFailedCheck?.(assetId),
    [],
  );

  const hasCreateDownstream = Boolean(onCreateDownstream || onQuickCreate);
  const hasConnectionClick = Boolean(onAssetConnectionClick);
  const hasQualityReview = Boolean(onReviewFailedCheck);
  const hasRun = Boolean(onRunAsset);
  const hasGoTo = Boolean(onGoToAsset);
  const hasDelete = Boolean(onDeleteAsset);
  const hasExternalImport = Boolean(onImportExternalRelation);

  // Selection, hover, and lineage highlighting update frequently while the
  // graph topology does not. Keep the comparatively expensive layered layout
  // and prefix-group geometry out of that interaction path.
  const graphGeometry = useMemo(() => {
    const graphEdges = derivedEdges(assets, links);
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
          opensDialog: true,
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
      // The same action as the card's hover button, for keyboard and touch.
      if (!asset.readOnly && hasCreateDownstream) {
        actions.push({
          key: "create-downstream",
          label: "Create downstream asset",
          icon: Plus,
          opensDialog: true,
          onSelect: () => handleCreateDownstream(asset.id),
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
          opensDialog: true,
          onSelect: () => setPendingDelete({ id: asset.id, name: assetDisplayName(asset) }),
        });
      }
      return {
        id: asset.id,
        type: "lineageAsset",
        selectable: !preview && !asset.readOnly && !asset.isExternal,
        // React Flow turns pointer events off for nodes that can be neither
        // selected nor dragged; cards stay clickable either way.
        style: { pointerEvents: "all" },
        position: layout.positions.get(asset.id) ?? { x: asset.x, y: asset.y },
        width: measured?.width ?? assetNodeWidth,
        height: measured?.height ?? assetNodeHeight,
        data: {
          asset,
          selected: false,
          highlighted: false,
          dimmed: false,
          onSelect: asset.readOnly
            ? () => setLineageAssetId((current) => (current === asset.id ? null : asset.id))
            : handleSelect,
          onCreateDownstream:
            !asset.readOnly && hasCreateDownstream ? handleCreateDownstream : undefined,
          onOpenConnection: hasConnectionClick ? handleOpenConnection : undefined,
          onReviewFailedCheck: hasQualityReview ? handleReviewFailedCheck : undefined,
          actions: actions.length > 0 ? actions : undefined,
          preview,
        },
        zIndex: 2,
      };
    });

    const runningAssetIds = new Set(
      preview ? assets.filter((asset) => asset.status === "pending").map((asset) => asset.id) : [],
    );
    const edges: Edge[] = graphEdges.map((edge) => {
      const flowing = !edge.provisional && runningAssetIds.has(edge.target);
      return {
        id: `${edge.source}-${edge.target}`,
        source: edge.source,
        target: edge.target,
        type: "default",
        className: edge.provisional
          ? "asset-edge-provisional"
          : flowing
            ? "asset-edge-active"
            : "asset-edge",
        animated: flowing,
        style: {
          stroke: edge.provisional || flowing ? undefined : "#a1a1aa",
          strokeWidth: edge.provisional || flowing ? undefined : 1.5,
          opacity: 1,
        },
      };
    });

    return { graphEdges, nodes: [...groupNodes, ...assetNodes], edges };
  }, [
    assets,
    goToLabel,
    flowInstance,
    links,
    preview,
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

  const { nodes, edges } = useMemo(() => {
    const lineage = lineageAssetId ? lineageFor(lineageAssetId, graphGeometry.graphEdges) : null;
    const visuallySelectedAssetId = deferredSelectedAssetId ?? lineageAssetId ?? undefined;
    const multiSelected = new Set(multiSelection);
    const showMultiSelection = multiSelection.length >= 2;
    const nodes = graphGeometry.nodes.map((node) => {
      if (node.type !== "lineageAsset") {
        return node;
      }
      const data = node.data as AssetNodeData;
      const selected = data.asset.id === visuallySelectedAssetId;
      const highlighted = data.asset.id === highlightAssetId || data.asset.id === revealHighlightId;
      const dimmed = Boolean(lineage && !lineage.all.has(data.asset.id));
      const inSelection = multiSelected.has(node.id);
      if (!selected && !highlighted && !dimmed && !inSelection) {
        return node;
      }
      return {
        ...node,
        selected: inSelection,
        data: {
          ...data,
          selected,
          highlighted,
          dimmed,
          multiSelected: showMultiSelection && inSelection,
        },
      };
    });
    const edges = !lineage
      ? graphGeometry.edges
      : graphGeometry.edges.map((edge) => {
          const provisional = edge.className === "asset-edge-provisional";
          const active = lineage.all.has(edge.source) && lineage.all.has(edge.target);
          return {
            ...edge,
            className: provisional
              ? "asset-edge-provisional"
              : active
                ? "asset-edge-active"
                : "asset-edge",
            animated: active && !provisional,
            style: {
              stroke: active || provisional ? undefined : "#a1a1aa",
              strokeWidth: active || provisional ? undefined : 1.5,
              opacity: active ? 1 : 0.12,
            },
          };
        });

    return { nodes, edges };
  }, [
    deferredSelectedAssetId,
    graphGeometry,
    highlightAssetId,
    lineageAssetId,
    multiSelection,
    revealHighlightId,
  ]);

  // The pending node sits beside its sources, or where a drag ended, with
  // provisional edges from each source.
  const quickCreateState = quickCreate?.state;
  const pendingDraft =
    quickCreateState && quickCreateState.status !== "idle" ? quickCreateState.draft : null;
  const pendingPlacement = useMemo(() => {
    if (!pendingDraft) return null;
    const boxes = graphGeometry.nodes
      .filter((node) => node.type === "lineageAsset")
      .map((node) => ({
        id: node.id,
        x: node.position.x,
        y: node.position.y,
        width: node.width ?? assetNodeWidth,
        height: node.height ?? assetNodeHeight,
      }));
    const sourceIds = pendingDraft.sourceIds.filter((id) => boxes.some((box) => box.id === id));
    const position =
      pendingDraft.position ??
      pendingNodePosition(
        boxes.filter((box) => sourceIds.includes(box.id)),
        boxes,
        { width: assetNodeWidth, height: pendingNodeHeight },
      );
    return {
      position,
      sourceIds,
      focusKey: `${pendingDraft.sourceIds.join(",")}@${position.x},${position.y}`,
    };
  }, [graphGeometry.nodes, pendingDraft]);
  const flowNodes = useMemo(() => {
    if (!pendingPlacement || !quickCreate) return nodes;
    const pendingNode: Node<QuickCreateNodeData> = {
      id: quickCreateNodeId,
      type: "quickCreate",
      position: pendingPlacement.position,
      width: assetNodeWidth,
      height: pendingNodeHeight,
      data: { controller: quickCreate },
      draggable: false,
      selectable: false,
      connectable: false,
      style: { pointerEvents: "all" },
      zIndex: 10,
    };
    return [...nodes, pendingNode];
  }, [nodes, pendingPlacement, quickCreate]);
  const flowEdges = useMemo(() => {
    if (!pendingPlacement) return edges;
    return [
      ...edges,
      ...pendingPlacement.sourceIds.map((sourceId) => ({
        id: `${sourceId}-${quickCreateNodeId}`,
        source: sourceId,
        target: quickCreateNodeId,
        type: "default",
        className: "asset-edge-provisional",
      })),
    ];
  }, [edges, pendingPlacement]);

  // Multi-selection follows React Flow's own select events (Shift-click,
  // Shift-drag and plain clicks); only authored assets can be selected.
  const handleNodesChange = useCallback((changes: NodeChange[]) => {
    setMultiSelection((current) => {
      let next = current;
      for (const change of changes) {
        if (change.type !== "select") continue;
        if (change.selected && !next.includes(change.id)) next = [...next, change.id];
        if (!change.selected && next.includes(change.id)) {
          next = next.filter((id) => id !== change.id);
        }
      }
      return next;
    });
  }, []);
  const selectedSources = useMemo(
    () =>
      multiSelection.filter((id) =>
        assets.some((asset) => asset.id === id && !asset.readOnly && !asset.isExternal),
      ),
    [assets, multiSelection],
  );

  const handleConnectStart = useCallback(
    (event: ReactMouseEvent | ReactTouchEvent, params: OnConnectStartParams) => {
      connectStartRef.current =
        params.nodeId && params.handleType === "source"
          ? { nodeId: params.nodeId, ...pointerPosition(event) }
          : null;
    },
    [],
  );
  // A drag from an output ends on another asset (link it) or on empty space
  // (create downstream there). A click on the + barely moves and is left to
  // its own click handler.
  const handleConnectEnd = useCallback(
    (event: MouseEvent | TouchEvent) => {
      const start = connectStartRef.current;
      connectStartRef.current = null;
      const container = containerRef.current;
      if (!start || !container) return;
      const point = pointerPosition(event);
      if (Math.hypot(point.x - start.x, point.y - start.y) < 8) return;
      const element = document.elementFromPoint(point.x, point.y);
      if (!element || !container.contains(element)) return;
      const nodeId = element.closest<HTMLElement>(".react-flow__node")?.dataset.id;
      if (nodeId === quickCreateNodeId || nodeId === start.nodeId) return;
      const target = nodeId ? assets.find((asset) => asset.id === nodeId) : undefined;
      if (target) {
        if (!callbacksRef.current.onLinkAssets) return;
        const bounds = container.getBoundingClientRect();
        setLinkMenu({
          x: point.x - bounds.left,
          y: point.y - bounds.top,
          sourceId: start.nodeId,
          targetId: target.id,
        });
        return;
      }
      const flowPosition = flowInstance?.screenToFlowPosition(point);
      callbacksRef.current.onQuickCreate?.(
        [start.nodeId],
        flowPosition ? { x: flowPosition.x, y: flowPosition.y - pendingNodeHeight / 2 } : undefined,
      );
    },
    [assets, flowInstance],
  );

  const linkChoice = useMemo(() => {
    if (!linkMenu) return null;
    const source = assets.find((asset) => asset.id === linkMenu.sourceId);
    const target = assets.find((asset) => asset.id === linkMenu.targetId);
    if (!source || !target) return null;
    const sourceName = assetDisplayName(source);
    const targetName = assetDisplayName(target);
    const graphEdges = graphGeometry.graphEdges;
    let blocked: string | null = null;
    if (target.readOnly || target.isExternal) {
      blocked = `${targetName} is not an asset of this pipeline.`;
    } else if (graphEdges.some((edge) => edge.source === source.id && edge.target === target.id)) {
      blocked = `${targetName} already depends on ${sourceName}.`;
    } else if (lineageFor(source.id, graphEdges).upstream.has(target.id)) {
      blocked = `${sourceName} already depends on ${targetName}, so this would make a cycle.`;
    }
    return { sourceName, targetName, blocked, canJoin: target.kind === "sql" };
  }, [assets, graphGeometry.graphEdges, linkMenu]);
  const chooseLink = async (mode: CanvasAssetLink["mode"]) => {
    const menu = linkMenu;
    const link = callbacksRef.current.onLinkAssets;
    if (!menu || !link || menu.working) return;
    setLinkMenu({ ...menu, working: mode, error: undefined });
    try {
      await link({ sourceId: menu.sourceId, targetId: menu.targetId, mode });
      setLinkMenu(null);
    } catch (cause) {
      setLinkMenu({
        ...menu,
        working: undefined,
        error: cause instanceof Error ? cause.message : String(cause),
      });
    }
  };

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
    if (preview) {
      const frame = window.requestAnimationFrame(() => {
        centeredGraphRef.current = graphKey;
        void flowInstance.fitView({ padding: 0.12, maxZoom: 0.9 });
      });
      return () => window.cancelAnimationFrame(frame);
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
  }, [flowInstance, nodes, preview]);

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
    <div
      ref={containerRef}
      className="relative h-full min-h-0 bg-muted/40"
      data-canvas-preview={preview || undefined}
      onKeyDown={(event) => {
        if (event.key !== "Escape" || event.defaultPrevented) return;
        if (linkMenu && !linkMenu.working) setLinkMenu(null);
        else if (paneMenu) setPaneMenu(null);
        else if (multiSelection.length > 0) setMultiSelection([]);
      }}
      style={viewTransitionName ? { viewTransitionName } : undefined}
    >
      <ReactFlow
        nodes={
          dropGroups.size === 0 && !expandedTarget
            ? flowNodes
            : flowNodes.map((node) => {
                if (node.type === "prefixGroup") {
                  const group = (node.data as PrefixGroupNodeData).label;
                  return dropGroups.has(group)
                    ? { ...node, zIndex: expandedTarget === `source:${group}` ? 1002 : 1000 }
                    : node;
                }
                const expanded = expandedTarget === `load:${node.id}`;
                if (!loadTargets.has(node.id) || (!dropGroups.size && !expanded)) return node;
                // The nearest expanded target wins stacking, without moving
                // cards. For file placement, only destination handles catch
                // pointers above a group's source drop area, not the card body.
                return {
                  ...node,
                  zIndex: expanded ? 1003 : 1001,
                  ...(dropGroups.size
                    ? { focusable: false, style: { ...node.style, pointerEvents: "none" as const } }
                    : {}),
                };
              })
        }
        elevateNodesOnSelect={dropGroups.size === 0 && !expandedTarget}
        edges={flowEdges}
        nodeTypes={nodeTypes}
        nodesDraggable={false}
        nodesConnectable={!preview && Boolean(onQuickCreate || onLinkAssets)}
        connectOnClick={false}
        onConnectStart={handleConnectStart}
        onConnectEnd={handleConnectEnd}
        elementsSelectable={!preview}
        onNodesChange={handleNodesChange}
        multiSelectionKeyCode="Shift"
        selectionKeyCode="Shift"
        deleteKeyCode={null}
        panActivationKeyCode={null}
        proOptions={{ hideAttribution: true }}
        minZoom={0.15}
        onInit={setFlowInstance}
        zoomOnScroll={!preview}
        zoomOnPinch={!preview}
        zoomOnDoubleClick={!preview}
        panOnDrag={!preview}
        preventScrolling={!preview}
        onPaneContextMenu={handlePaneContextMenu}
        onPaneClick={() => {
          setPaneMenu(null);
          setLinkMenu(null);
        }}
        onMoveStart={(event) => {
          setPaneMenu(null);
          // Programmatic moves (centering the new asset) have no event.
          if (event) setRevealHighlightId(null);
        }}
      >
        <Background
          gap={22}
          size={1.2}
          color="var(--muted-foreground)"
          className="opacity-40 dark:opacity-50"
        />
        {preview ? null : <Controls position="bottom-left" />}
        <ViewportFocus
          assetId={focusAssetId === revealAssetId ? undefined : focusAssetId}
          nodes={nodes}
        />
        <ViewportFocus
          assetId={revealAssetId}
          focusKey={revealAssetId ? `reveal:${revealAssetId}` : undefined}
          nodes={nodes}
          center
        />
        <ViewportFocus
          assetId={pendingPlacement ? quickCreateNodeId : undefined}
          focusKey={pendingPlacement?.focusKey}
          nodes={flowNodes}
        />
      </ReactFlow>
      {selectedSources.length >= 2 && onQuickCreate ? (
        <div
          role="toolbar"
          aria-label="Selected assets"
          className="absolute left-1/2 top-12 z-30 flex max-w-[calc(100%-2rem)] -translate-x-1/2 items-center gap-2 rounded-lg border bg-background/95 py-1 pl-3 pr-1 text-xs shadow-md backdrop-blur"
        >
          <span className="truncate text-muted-foreground">
            {selectedSources.length} assets selected
          </span>
          <Button
            type="button"
            size="sm"
            onClick={() => {
              onQuickCreate(selectedSources);
              setMultiSelection([]);
            }}
          >
            <GitMerge className="size-3.5" />
            Join into new asset
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label="Clear selection"
            title="Clear selection (Esc)"
            onClick={() => setMultiSelection([])}
          >
            <X className="size-3.5" />
          </Button>
        </div>
      ) : null}
      {linkMenu && linkChoice ? (
        <>
          <div
            className="absolute inset-0 z-30"
            onClick={() => !linkMenu.working && setLinkMenu(null)}
            onContextMenu={(event) => {
              event.preventDefault();
              if (!linkMenu.working) setLinkMenu(null);
            }}
          />
          <div
            role="menu"
            aria-label="Connect assets"
            className="absolute z-40 w-64 rounded-md border bg-popover p-1 text-popover-foreground shadow-md"
            style={{ left: linkMenu.x, top: linkMenu.y }}
          >
            <div className="truncate px-2 py-1.5 text-[11px] text-muted-foreground">
              <span className="font-mono">{linkChoice.sourceName}</span> →{" "}
              <span className="font-mono">{linkChoice.targetName}</span>
            </div>
            {linkChoice.blocked ? (
              <p className="px-2 pb-2 text-xs">{linkChoice.blocked}</p>
            ) : (
              <>
                {linkChoice.canJoin ? (
                  <LinkMenuItem
                    icon={GitMerge}
                    label="Join in query"
                    description={`Edits ${linkChoice.targetName}'s SQL to read it`}
                    working={linkMenu.working === "join"}
                    disabled={Boolean(linkMenu.working)}
                    onSelect={() => void chooseLink("join")}
                  />
                ) : null}
                <LinkMenuItem
                  icon={Link2}
                  label="Add as dependency"
                  description="Runs after it; the code stays as it is"
                  working={linkMenu.working === "dependency"}
                  disabled={Boolean(linkMenu.working)}
                  onSelect={() => void chooseLink("dependency")}
                />
              </>
            )}
            {linkMenu.error ? (
              <p role="alert" className="px-2 py-1.5 text-xs text-destructive">
                {linkMenu.error}
              </p>
            ) : null}
          </div>
        </>
      ) : null}
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
            role="menu"
            aria-label={paneMenu.prefix ? `New asset in ${paneMenu.prefix}` : "New asset"}
            className="absolute z-40 min-w-48 rounded-md border bg-popover p-1 text-popover-foreground shadow-md"
            style={{ left: paneMenu.x, top: paneMenu.y }}
          >
            <div className="truncate px-2 py-1 text-[11px] text-muted-foreground">
              {paneMenu.prefix ? (
                <>
                  New in <span className="font-mono">{paneMenu.prefix}</span>
                </>
              ) : (
                "New"
              )}
            </div>
            {paneCreateItems.map((item) => (
              <button
                key={item.kind}
                type="button"
                role="menuitem"
                className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-xs hover:bg-accent hover:text-accent-foreground"
                onClick={() => {
                  const prefix = paneMenu.prefix;
                  setPaneMenu(null);
                  onCreateAsset({ prefix, kind: item.kind });
                }}
              >
                <item.icon className="size-3.5" />
                {item.label}
              </button>
            ))}
            <div className="my-1 h-px bg-border" />
            <button
              type="button"
              role="menuitem"
              className="flex w-full items-center gap-2 rounded-sm px-2 py-1.5 text-left text-xs hover:bg-accent hover:text-accent-foreground"
              onClick={() => {
                const prefix = paneMenu.prefix;
                setPaneMenu(null);
                onCreateAsset({ prefix });
              }}
            >
              <MoreHorizontal className="size-3.5" />
              {paneMenu.prefix ? (
                <span className="min-w-0 truncate">
                  New asset in <span className="font-mono">{paneMenu.prefix}</span>…
                </span>
              ) : (
                "More asset types…"
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
