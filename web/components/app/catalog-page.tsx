import { useNavigate } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import { ChevronRight, Filter, Loader2, Search } from "lucide-react";
import { useMemo, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { useAssetResults } from "@/hooks/use-asset-results";
import { usePipelinesStaleness } from "@/hooks/use-pipeline-staleness";
import { deleteAsset } from "@/lib/api-assets";
import { assetPresentationFields, type AssetKind } from "@/lib/asset-presentation";
import { workspaceAtom } from "@/lib/atoms/domains/workspace";
import type { WebAsset, WebPipeline } from "@/lib/types";
import {
  labelForAppMaterializationState,
  useAppAssetMaterializationStatus,
} from "@/hooks/use-app-asset-materialization-status";

import { kindMeta } from "./app-data";
import { AppLineageCanvas, type AppLineageCanvasAsset } from "./lineage-canvas";
import type { AppLineageLayoutEdge } from "@/lib/app-lineage-layout";
import { AppContextSidebarFrame } from "./workbench/workbench-context-sidebar";
import { WorkbenchPortal, useWorkbench } from "./workbench/workbench-slots";
import { cn } from "@/lib/utils";

function catalogAssetsForPipeline(pipeline: WebPipeline): AppLineageCanvasAsset[] {
  return pipeline.assets.map((asset) => catalogAssetFromWorkspace(asset, pipeline));
}

function catalogAssetFromWorkspace(asset: WebAsset, pipeline: WebPipeline): AppLineageCanvasAsset {
  return {
    ...assetPresentationFields(asset, pipeline),
    status: asset.is_materialized ? "success" : "pending",
    materializedAt: asset.is_materialized ? "current" : "not materialized",
    pipelineId: pipeline.id,
    isMaterialized: asset.is_materialized,
    upstreams: asset.upstreams,
    x: 0,
    y: 0,
  };
}

// Strip asset-selector decorations (++name+, -name) so the box behaves as
// a plain substring filter over asset names.
function normalizeFilterQuery(value: string) {
  return value
    .trim()
    .toLowerCase()
    .replace(/^[+-]+/, "")
    .replace(/[+-]+$/, "");
}

export type AppCatalogSearch = { asset?: string };

export function normalizeAppCatalogSearch(search: Record<string, unknown>): AppCatalogSearch {
  return {
    asset: typeof search.asset === "string" && search.asset ? search.asset : undefined,
  };
}

export function AppCatalogPage({
  selectedAssetId,
  onAssetSelect,
}: {
  selectedAssetId?: string;
  onAssetSelect?: (assetId?: string) => void;
} = {}) {
  const workspace = useAtomValue(workspaceAtom);
  const navigate = useNavigate();
  const { setMobileNavigationOpen } = useWorkbench();
  const assetResults = useAssetResults();
  const [query, setQuery] = useState("");
  const [hiddenKinds, setHiddenKinds] = useState<Set<AssetKind>>(() => new Set());

  const catalogAssets = useMemo<AppLineageCanvasAsset[]>(
    () => workspace?.pipelines.flatMap(catalogAssetsForPipeline) ?? [],
    [workspace?.pipelines],
  );
  const catalogLinks = useMemo<AppLineageLayoutEdge[]>(
    () =>
      workspace?.pipelines.flatMap((pipeline) => {
        const localAssetIDs = new Map(pipeline.assets.map((asset) => [asset.name, asset.id]));
        return pipeline.assets.flatMap((asset) => {
          if (asset.dependencies) {
            return asset.dependencies.flatMap((dependency) =>
              dependency.resolved_asset_id
                ? [{ source: dependency.resolved_asset_id, target: asset.id }]
                : [],
            );
          }
          return asset.upstreams.flatMap((upstream) => {
            const source = localAssetIDs.get(upstream);
            return source ? [{ source, target: asset.id }] : [];
          });
        });
      }) ?? [],
    [workspace?.pipelines],
  );
  const catalogPipelineIds = useMemo(
    () => [
      ...new Set(
        catalogAssets
          .map((asset) => asset.pipelineId)
          .filter((pipelineId): pipelineId is string => Boolean(pipelineId)),
      ),
    ],
    [catalogAssets],
  );
  const staleness = usePipelinesStaleness(catalogPipelineIds);
  const materializationAssets = useMemo(
    () =>
      catalogAssets.map((asset) => ({
        id: asset.id,
        name: asset.name,
        pipelineId: asset.pipelineId,
        isMaterialized:
          asset.isMaterialized ?? (asset.status === "success" || asset.status === "ok"),
        staleness: asset.pipelineId
          ? staleness.byPipelineId[asset.pipelineId]?.[asset.name]
          : undefined,
      })),
    [catalogAssets, staleness.byPipelineId],
  );
  const materializationStatusByAssetId = useAppAssetMaterializationStatus(materializationAssets);
  const displayedCatalogAssets = useMemo(
    () =>
      catalogAssets.map((asset) => {
        const assetStaleness = asset.pipelineId
          ? staleness.byPipelineId[asset.pipelineId]?.[asset.name]
          : undefined;
        return {
          ...asset,
          status: materializationStatusByAssetId[asset.id]?.status ?? asset.status,
          materializedAt:
            assetStaleness?.status === "external"
              ? "freshness not tracked"
              : labelForAppMaterializationState(materializationStatusByAssetId[asset.id]),
          staleness: assetStaleness,
        };
      }),
    [catalogAssets, materializationStatusByAssetId, staleness.byPipelineId],
  );

  const availableKinds = useMemo(() => {
    const kinds = new Set<AssetKind>();
    for (const asset of catalogAssets) {
      kinds.add(asset.kind);
    }
    return [...kinds];
  }, [catalogAssets]);

  const filteredAssets = useMemo(() => {
    const normalizedQuery = normalizeFilterQuery(query);
    return displayedCatalogAssets.filter((asset) => {
      if (hiddenKinds.has(asset.kind)) {
        return false;
      }
      if (normalizedQuery && !asset.name.toLowerCase().includes(normalizedQuery)) {
        return false;
      }
      return true;
    });
  }, [displayedCatalogAssets, hiddenKinds, query]);
  const filteredLinks = useMemo(() => {
    const displayedIDs = new Set(filteredAssets.map((asset) => asset.id));
    return catalogLinks.filter(
      (link) => displayedIDs.has(link.source) && displayedIDs.has(link.target),
    );
  }, [catalogLinks, filteredAssets]);

  const toggleKind = (kind: AssetKind) => {
    setHiddenKinds((current) => {
      const next = new Set(current);
      if (next.has(kind)) {
        next.delete(kind);
      } else {
        next.add(kind);
      }
      return next;
    });
  };

  const runAsset = (assetId: string) => {
    void assetResults.runMaterializeForAsset(assetId);
  };
  const removeAsset = async (assetId: string) => {
    const target = catalogAssets.find((asset) => asset.id === assetId);
    if (!target?.pipelineId) {
      return;
    }
    // The workspace event stream refreshes the atom once the file is gone.
    await deleteAsset(target.pipelineId, assetId);
  };
  const openInBuild = (assetId: string) => {
    const target = catalogAssets.find((asset) => asset.id === assetId);
    if (!target?.pipelineId) {
      return;
    }
    void navigate({
      to: "/pipelines/$pipelineId/assets/$assetId/canvas",
      params: { pipelineId: target.pipelineId, assetId },
    });
  };

  const filterActive = hiddenKinds.size > 0;
  const selectCatalogAsset = (assetId: string) => {
    onAssetSelect?.(assetId);
    setMobileNavigationOpen(false);
  };

  return (
    <div className="h-full min-h-0 bg-background">
      <WorkbenchPortal slot="context">
        <AppContextSidebarFrame
          title="Catalog"
          subtitle={`${catalogAssets.length} assets across ${workspace?.pipelines.length ?? 0} pipelines`}
          actions={
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  variant={filterActive ? "secondary" : "ghost"}
                  size="icon-sm"
                  aria-label="Filter asset types"
                >
                  <Filter />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-48">
                <DropdownMenuLabel>Asset type</DropdownMenuLabel>
                <DropdownMenuSeparator />
                {availableKinds.map((kind) => (
                  <DropdownMenuCheckboxItem
                    key={kind}
                    checked={!hiddenKinds.has(kind)}
                    onCheckedChange={() => toggleKind(kind)}
                    onSelect={(event) => event.preventDefault()}
                  >
                    {kindMeta[kind]?.label ?? kind}
                  </DropdownMenuCheckboxItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          }
        >
          <div className="space-y-3 p-2">
            <div className="relative">
              <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                aria-label="Filter catalog assets"
                className="h-8 pl-8 text-xs"
                placeholder="Filter assets..."
                value={query}
                onChange={(event) => setQuery(event.target.value)}
              />
            </div>
            {staleness.error ? (
              <p className="rounded-md bg-destructive/10 px-2 py-1.5 text-[10px] text-destructive">
                {staleness.error} Cached catalog metadata remains visible.
              </p>
            ) : null}
            <div className="space-y-3">
              {(workspace?.pipelines ?? []).map((pipeline) => {
                const pipelineAssets = filteredAssets.filter(
                  (asset) => asset.pipelineId === pipeline.id,
                );
                if (pipelineAssets.length === 0) return null;
                return (
                  <section key={pipeline.id}>
                    <div className="flex items-center gap-2 px-2 py-1 text-[10px] font-semibold uppercase tracking-wide text-muted-foreground">
                      <span className="min-w-0 flex-1 truncate">{pipeline.name}</span>
                      <span className="font-mono">{pipelineAssets.length}</span>
                    </div>
                    <div className="space-y-0.5">
                      {pipelineAssets.map((asset) => {
                        const Icon = kindMeta[asset.kind].icon;
                        return (
                          <button
                            key={asset.id}
                            type="button"
                            className={cn(
                              "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs hover:bg-muted",
                              selectedAssetId === asset.id && "bg-primary/10 text-primary",
                            )}
                            onClick={() => selectCatalogAsset(asset.id)}
                          >
                            <Icon className="size-3.5 shrink-0" />
                            <span className="min-w-0 flex-1 truncate font-mono">{asset.name}</span>
                            <ChevronRight className="size-3 shrink-0 text-muted-foreground" />
                          </button>
                        );
                      })}
                    </div>
                  </section>
                );
              })}
            </div>
          </div>
        </AppContextSidebarFrame>
      </WorkbenchPortal>

      {workspace ? (
        <AppLineageCanvas
          assets={filteredAssets}
          links={filteredLinks}
          selectedAssetId={selectedAssetId}
          focusAssetId={selectedAssetId}
          onAssetSelect={selectCatalogAsset}
          onRunAsset={runAsset}
          onDeleteAsset={removeAsset}
          onGoToAsset={openInBuild}
          goToLabel="Open in build"
        />
      ) : (
        <div className="flex h-full items-center justify-center">
          <Loader2 className="size-6 animate-spin text-muted-foreground" />
        </div>
      )}
    </div>
  );
}
