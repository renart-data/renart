import {
  createContext,
  useContext,
  useEffect,
  useRef,
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
import type { AppLineageCanvasAsset } from "../lineage-canvas";

type DropContext = {
  eligible: Set<string>;
  drop: (assetId: string | undefined, event?: DragEvent) => void;
};
const CanvasDropContext = createContext<DropContext | null>(null);

export function DataBrowserCanvas({
  children,
  pipelineId,
  assets,
  onSource,
  onLoad,
}: {
  children: ReactNode;
  pipelineId: string;
  assets: AppLineageCanvasAsset[];
  onSource: (objectId: string, environment: string) => void;
  onLoad: (assetId: string, destination: string) => void;
}) {
  const [transfer, setTransfer] = useAtom(dataBrowserTransferAtom);
  const environment = useAtomValue(selectedEnvironmentAtom) ?? "default";
  const active = acceptsDataBrowserTransfer(transfer, pipelineId, getPinnedProjectId(), environment)
    ? transfer
    : null;
  const { profile, loading, error } = useAssetCreationProfile(
    pipelineId,
    active?.kind === "connection",
  );
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
    active?.kind === "connection"
      ? assets
          .filter((asset) => canLoadDataBrowserConnection(asset, active.id, sources, destinations))
          .map((asset) => asset.id)
      : [],
  );
  const container = useRef<HTMLDivElement>(null);
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
  }, [active?.token, active?.method, eligible.size]);
  const drop = (assetId: string | undefined, event?: DragEvent) => {
    if (!active) return;
    if (event) {
      event.preventDefault();
      event.stopPropagation();
      if (!matchesDataBrowserTransfer(active, event.dataTransfer.getData(DATA_BROWSER_MIME)))
        return;
    } else if (active.method !== "choose") return;
    if (active.kind === "table" && !assetId) onSource(active.id, active.environment);
    else if (active.kind === "connection" && assetId && eligible.has(assetId))
      onLoad(assetId, active.id);
    else return;
    setTransfer(null);
  };
  return (
    <CanvasDropContext.Provider value={active ? { eligible, drop } : null}>
      <div ref={container} className="relative h-full min-h-0 min-w-0">
        {children}
        {active ? (
          <div className="absolute top-3 left-3 z-20 flex max-w-[calc(100%-1.5rem)] items-center gap-2 rounded-xl border bg-background p-2 shadow-sm">
            {active.kind === "table" ? (
              <Button
                variant="outline"
                className="h-auto min-w-0 border-dashed border-primary px-3 py-2 text-left"
                data-testid="data-browser-drop-target"
                onDragOver={allowDataBrowserDrop}
                onDrop={(event) => drop(undefined, event)}
                onClick={() => drop(undefined)}
              >
                <Database data-icon="inline-start" />
                <span className="min-w-0">
                  <span className="block">Create source asset</span>
                  <span className="block truncate text-xs font-normal text-muted-foreground">
                    {active.label} · review before saving
                  </span>
                </span>
              </Button>
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
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label="Cancel canvas placement"
              onClick={() => setTransfer(null)}
            >
              <X />
            </Button>
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

export function DataBrowserLoadDropTarget({ assetId, label }: { assetId: string; label: string }) {
  const context = useContext(CanvasDropContext);
  if (!context?.eligible.has(assetId)) return null;
  return (
    <Button
      variant="outline"
      size="sm"
      className="nodrag nopan absolute top-1/2 left-full z-10 ml-2 w-20 -translate-y-1/2 border-dashed border-primary bg-background text-primary shadow-sm"
      data-testid="data-browser-drop-target"
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
  );
}
