import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type DragEvent,
  type ReactNode,
} from "react";
import { useAtom, useAtomValue } from "jotai";
import { Database, Plus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useAssetCreationProfile } from "@/hooks/use-asset-creation-profile";
import { selectedEnvironmentAtom } from "@/lib/atoms/workspace";
import {
  acceptsDataBrowserTransfer,
  canLoadDataBrowserConnection,
  DATA_BROWSER_MIME,
  dataBrowserTransferAtom,
  matchesDataBrowserTransfer,
} from "@/lib/data-browser-transfer";
import { getPinnedProjectId } from "@/lib/project-context";
import { getDataBrowserObject } from "@/lib/api-data-browser";
import type { DataBrowserObject } from "@/lib/generated/api-types";
import { nearestDropTarget } from "@/lib/drop-target-proximity";
import { assetNameParts } from "@/lib/asset-presentation";
import { cn } from "@/lib/utils";
import type { AppLineageCanvasAsset } from "../lineage-canvas";

type DropContext = {
  eligible: Set<string>;
  drop: (assetId: string | undefined, event?: DragEvent, group?: string) => void;
  registerTarget: (id: string, element: HTMLElement | null) => void;
  nearTarget: string | null;
  sourceGroups: Set<string>;
  fileSource: boolean;
  sourceLabel: string;
};
const CanvasDropContext = createContext<DropContext | null>(null);

export function DataBrowserCanvas({
  children,
  pipelineId,
  assets,
  onSource,
  onLoad,
  onStorage,
}: {
  children: ReactNode;
  pipelineId: string;
  assets: AppLineageCanvasAsset[];
  onSource: (objectId: string, environment: string) => void;
  onLoad: (assetId: string, destination: string) => void;
  onStorage: (object: DataBrowserObject, upstreamId?: string, prefix?: string) => void;
}) {
  const [transfer, setTransfer] = useAtom(dataBrowserTransferAtom);
  const environment = useAtomValue(selectedEnvironmentAtom) ?? "default";
  const active = acceptsDataBrowserTransfer(transfer, pipelineId, getPinnedProjectId(), environment)
    ? transfer
    : null;
  const isLoadObject = active?.kind === "storage" || active?.kind === "file";
  const { profile, loading, error } = useAssetCreationProfile(
    pipelineId,
    active?.kind === "connection" || isLoadObject,
  );
  const [resolvedObject, setResolvedObject] = useState<DataBrowserObject | null>(null);
  const [objectError, setObjectError] = useState("");
  useEffect(() => {
    setResolvedObject(null);
    setObjectError("");
    if (!active || !isLoadObject) return;
    const abort = new AbortController();
    void getDataBrowserObject({ objectId: active.id, environment }, abort.signal)
      .then((response) => {
        if (!abort.signal.aborted) setResolvedObject(response.object);
      })
      .catch((cause) => {
        if (!abort.signal.aborted)
          setObjectError(
            cause instanceof Error ? cause.message : "Could not resolve this data object.",
          );
      });
    return () => abort.abort();
  }, [active?.id, isLoadObject, environment]);
  const storageObject = isLoadObject && resolvedObject?.id === active?.id ? resolvedObject : null;
  // Placement must appear immediately, not wait for a warehouse schema query.
  // This is only a hint; the reviewed import resolves the opaque ID again.
  const sourceReference = active?.kind === "table" ? active.referenceText : undefined;
  const sourceGroup = sourceReference ? (assetNameParts(sourceReference).prefix ?? "root") : null;
  const fileSource = Boolean(storageObject?.capabilities.load_source);
  const sourceGroups = new Set(
    fileSource
      ? assets.map((asset) => asset.prefix || asset.group || "root")
      : sourceGroup !== null
        ? [sourceGroup]
        : [],
  );
  const newSourceGroup =
    sourceGroup !== null &&
    !assets.some((asset) => (asset.prefix || asset.group || "root") === sourceGroup);
  const destination = isLoadObject ? storageObject?.connection_name : active?.id;
  const load =
    profile?.environment === environment
      ? profile.kinds.find((kind) => kind.kind === "load")
      : undefined;
  const sources =
    load?.roles.find((role) => role.role === "source")?.connections.map((item) => item.name) ?? [];
  const destinations =
    load?.roles.find((role) => role.role === "destination")?.connections.map((item) => item.name) ??
    [];
  const eligible = new Set(
    (active?.kind === "connection" || storageObject?.capabilities.load_destination) && destination
      ? assets
          .filter((asset) =>
            canLoadDataBrowserConnection(asset, destination, sources, destinations),
          )
          .map((asset) => asset.id)
      : [],
  );
  const container = useRef<HTMLDivElement>(null);
  const targets = useRef(new Map<string, HTMLElement>());
  const [nearTarget, setNearTarget] = useState<string | null>(null);
  const registerTarget = useCallback((id: string, element: HTMLElement | null) => {
    if (element) targets.current.set(id, element);
    else targets.current.delete(id);
  }, []);
  useEffect(() => {
    setNearTarget(null);
    if (active?.method !== "drag") return;
    const approach = (event: globalThis.DragEvent) => {
      if (!event.dataTransfer?.types.includes(DATA_BROWSER_MIME)) return;
      const bounds = [...targets.current].flatMap(([id, element]) => {
        if (!element.isConnected) return [];
        // Keep the expanded hit area open while the pointer is inside it,
        // including at high canvas zoom. The layout anchor itself never moves.
        const hit = event.target instanceof Node && element.contains(event.target);
        const { left, right, top, bottom } =
          (hit ? element.firstElementChild : null)?.getBoundingClientRect() ??
          element.getBoundingClientRect();
        return [{ id, left, right, top, bottom }];
      });
      setNearTarget(nearestDropTarget(bounds, event.clientX, event.clientY));
    };
    const leave = (event: globalThis.DragEvent) => {
      if (
        !event.relatedTarget &&
        (event.target === document || event.target === document.documentElement)
      )
        setNearTarget(null);
    };
    window.addEventListener("dragover", approach, true);
    window.addEventListener("dragleave", leave);
    return () => {
      window.removeEventListener("dragover", approach, true);
      window.removeEventListener("dragleave", leave);
    };
  }, [active?.token, active?.method]);
  useEffect(() => {
    if (!active) return;
    const cancel = (event: KeyboardEvent) => {
      if (event.key === "Escape") setTransfer(null);
    };
    window.addEventListener("keydown", cancel);
    return () => window.removeEventListener("keydown", cancel);
  }, [active, setTransfer]);
  useEffect(() => {
    if (active?.method !== "choose") return;
    const frame = requestAnimationFrame(() =>
      container.current
        ?.querySelector<HTMLButtonElement>('[data-testid="data-browser-drop-target"]')
        ?.focus(),
    );
    return () => cancelAnimationFrame(frame);
  }, [active?.token, active?.method, eligible.size, storageObject?.id, sourceGroup]);
  const drop = (assetId: string | undefined, event?: DragEvent, group?: string) => {
    if (!active) return;
    if (group !== undefined && !sourceGroups.has(group)) return;
    if (event) {
      event.preventDefault();
      event.stopPropagation();
      if (!matchesDataBrowserTransfer(active, event.dataTransfer.getData(DATA_BROWSER_MIME)))
        return;
    } else if (active.method !== "choose") return;
    if (active.kind === "table" && sourceReference && !assetId)
      onSource(active.id, active.environment);
    else if (
      isLoadObject &&
      storageObject &&
      ((!assetId && storageObject.capabilities.load_source) || (assetId && eligible.has(assetId)))
    )
      onStorage(storageObject, assetId, group);
    else if (active.kind === "connection" && assetId && eligible.has(assetId))
      onLoad(assetId, active.id);
    else return;
    setTransfer(null);
  };
  return (
    <CanvasDropContext.Provider
      value={
        active
          ? {
              eligible,
              drop,
              registerTarget,
              nearTarget,
              sourceGroups,
              fileSource,
              sourceLabel: sourceReference ?? active.label,
            }
          : null
      }
    >
      <div ref={container} className="relative h-full min-h-0 min-w-0">
        {children}
        {newSourceGroup ? (
          <div className="absolute bottom-4 left-4 z-20 h-28 w-64 max-w-[calc(100%-2rem)] rounded-2xl border bg-background shadow-sm">
            <DataBrowserSourceDropTarget group={sourceGroup} isNew />
          </div>
        ) : null}
        {active ? (
          <div className="absolute top-3 left-3 z-20 flex max-w-[calc(100%-1.5rem)] items-center gap-2 rounded-xl border bg-background p-2 shadow-sm">
            {active.kind === "table" ? (
              <p role="status" className="min-w-0 px-1 text-xs">
                {objectError ||
                  (sourceGroup === null
                    ? "Checking source table…"
                    : `${active.method === "drag" ? "Drop in" : "Choose"} the ${sourceGroup} group · review before saving`)}
              </p>
            ) : isLoadObject ? (
              <div
                ref={(element) =>
                  registerTarget(
                    "storage:source",
                    storageObject?.capabilities.load_source ? element : null,
                  )
                }
                className="relative h-12 w-64 min-w-0"
              >
                <Button
                  variant="outline"
                  className="absolute h-auto min-w-0 border-dashed border-primary px-3 py-2 text-left transition-[inset] duration-150 motion-reduce:transition-none"
                  style={{ inset: nearTarget === "storage:source" ? -8 : 0 }}
                  data-testid="data-browser-drop-target"
                  data-proximity={nearTarget === "storage:source" ? "near" : "far"}
                  onDragOver={allowDataBrowserDrop}
                  onDrop={(event) => drop(undefined, event)}
                  onClick={() => drop(undefined)}
                  disabled={!storageObject?.capabilities.load_source}
                >
                  <Database data-icon="inline-start" />
                  <span className="min-w-0">
                    <span className="block">
                      {active.kind === "file" ? "Create Load from file" : "Create Load from object"}
                    </span>
                    <span className="block truncate text-xs font-normal text-muted-foreground">
                      {objectError ||
                        (!storageObject
                          ? "Checking data object…"
                          : `${active.label} · review before saving`)}
                    </span>
                  </span>
                </Button>
              </div>
            ) : (
              <p role="status" className="min-w-0 px-1 text-xs">
                {loading
                  ? "Checking compatible targets…"
                  : error
                    ? "Could not check compatible targets. Try again."
                    : eligible.size
                      ? `${active.method === "drag" ? "Drop" : "Choose"} beside an asset to load into ${active.label}`
                      : "No compatible upstream asset for this connection."}
              </p>
            )}
            {isLoadObject && eligible.size > 0 ? (
              <p role="status" className="sr-only text-xs text-muted-foreground sm:not-sr-only">
                Or drop beside an asset to use this as its destination.
              </p>
            ) : null}
            {active.method === "choose" ? (
              <Button
                variant="ghost"
                size="icon-xs"
                aria-label="Cancel canvas placement"
                onClick={() => setTransfer(null)}
              >
                <X />
              </Button>
            ) : null}
          </div>
        ) : null}
      </div>
    </CanvasDropContext.Provider>
  );
}

function allowDataBrowserDrop(event: DragEvent) {
  if (!event.dataTransfer.types.includes(DATA_BROWSER_MIME)) return;
  event.preventDefault();
  event.stopPropagation();
  event.dataTransfer.dropEffect = "copy";
}

const noDropGroups = new Set<string>();
export function useDataBrowserDropGroups() {
  return useContext(CanvasDropContext)?.sourceGroups ?? noDropGroups;
}

export function useDataBrowserLoadTargets() {
  return useContext(CanvasDropContext)?.eligible ?? noDropGroups;
}

export function useDataBrowserExpandedTarget() {
  return useContext(CanvasDropContext)?.nearTarget ?? null;
}

export function DataBrowserSourceDropTarget({
  group,
  isNew = false,
}: {
  group: string;
  isNew?: boolean;
}) {
  const context = useContext(CanvasDropContext);
  if (!context?.sourceGroups.has(group)) return null;
  const id = `source:${group}`;
  const near = context.nearTarget === id;
  return (
    <div
      ref={(element) => context.registerTarget(id, element)}
      className="pointer-events-auto absolute inset-0"
    >
      <Button
        variant={isNew ? "outline" : "ghost"}
        className="nodrag nopan pointer-events-auto absolute flex flex-col gap-2 rounded-2xl border-dashed border-primary/60 transition-[inset,box-shadow] duration-150 motion-reduce:transition-none"
        style={{ inset: near ? -12 : -1, width: "auto", height: "auto" }}
        data-testid="data-browser-drop-target"
        data-source-group={group}
        data-new-group={isNew ? "true" : "false"}
        data-proximity={near ? "near" : "far"}
        aria-label={`Create ${context.fileSource ? "Load" : "source"} in ${group} group`}
        onDragOver={allowDataBrowserDrop}
        onDrop={(event) => context.drop(undefined, event, group)}
        onClick={(event) => {
          event.stopPropagation();
          context.drop(undefined, undefined, group);
        }}
      >
        {isNew ? <span className="absolute top-3 left-3 font-mono">{group}</span> : null}
        {context.fileSource ? (
          <span
            className="absolute right-3 top-2 flex items-center gap-1.5 rounded-md border bg-background px-2 py-1 text-xs shadow-sm"
            title={context.sourceLabel}
          >
            <Plus className="size-3.5" /> Load here
          </span>
        ) : (
          <span
            className={cn(
              "flex max-w-full flex-col items-center gap-2",
              !isNew && "rounded-lg border bg-background p-3 shadow-sm",
            )}
          >
            <span className="flex max-w-full items-center gap-2">
              <Database data-icon="inline-start" />
              <span className="truncate">{context.sourceLabel}</span>
            </span>
            <span className="text-xs font-normal text-muted-foreground">
              {isNew ? "Create source in new group" : "Add source to this group"}
            </span>
          </span>
        )}
      </Button>
    </div>
  );
}

export function DataBrowserLoadDropTarget({ assetId, label }: { assetId: string; label: string }) {
  const context = useContext(CanvasDropContext);
  if (!context?.eligible.has(assetId)) return null;
  const targetId = `load:${assetId}`;
  const near = context.nearTarget === targetId;
  return (
    <div
      ref={(element) => context.registerTarget(targetId, element)}
      className="pointer-events-auto absolute top-1/2 left-full z-10 ml-2 flex h-8 w-20 -translate-y-1/2 items-center"
    >
      <Button
        variant="outline"
        size="sm"
        className={cn(
          "nodrag nopan pointer-events-auto relative border-dashed border-primary bg-background text-primary shadow-sm transition-[width,height,box-shadow] duration-150 motion-reduce:transition-none",
          near ? "h-16 w-36 shadow-md" : "h-8 w-20",
        )}
        data-testid="data-browser-drop-target"
        data-proximity={near ? "near" : "far"}
        aria-label={`Create Load after ${label}`}
        onDragOver={allowDataBrowserDrop}
        onDrop={(event) => context.drop(assetId, event)}
        onClick={(event) => {
          event.stopPropagation();
          context.drop(assetId);
        }}
      >
        <Plus data-icon="inline-start" /> Load
      </Button>
    </div>
  );
}
