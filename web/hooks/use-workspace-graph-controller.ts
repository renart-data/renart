"use client";

import { useMemo, useRef, useState } from "react";
import {
  NodeTypes,
  ReactFlowInstance,
  useEdgesState,
  useNodesState,
} from "reactflow";

import { AssetNode } from "@/components/asset-node";
import { NewAssetNode } from "@/components/new-asset-node";
import { useAssetPreviews } from "@/hooks/use-asset-previews";
import { useGraphViewportFocus } from "@/hooks/use-graph-viewport-focus";
import { usePersistedNodePositions } from "@/hooks/use-persisted-node-positions";
import { getTablePreviewLimit } from "@/lib/asset-visualization";
import {
  buildFlowFromPipeline,
  computeGraphLayoutPositions,
} from "@/lib/graph";
import { WebAsset } from "@/lib/types";

type UseWorkspaceGraphControllerParams = {
  asset: WebAsset | null;
  enrichedPipeline: Parameters<typeof buildFlowFromPipeline>[0];
  selectedAssetId: string | null;
  selectedInspectRows?: Record<string, unknown>[] | null;
  visualAssets: WebAsset[];
  materializingAssetIds?: Set<string>;
};

export function useWorkspaceGraphController({
  asset,
  enrichedPipeline,
  selectedAssetId,
  selectedInspectRows,
  visualAssets,
  materializingAssetIds,
}: UseWorkspaceGraphControllerParams) {
  const [nodes, setNodes, onNodesChange] = useNodesState([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState([]);
  const [reactFlowInstance, setReactFlowInstance] =
    useState<ReactFlowInstance | null>(null);
  const [storedNodePositions, setStoredNodePositions] =
    usePersistedNodePositions();
  const [recomputeVersion, setRecomputeVersion] = useState(0);
  const canvasContainerRef = useRef<HTMLDivElement | null>(null);

  const nodeTypes = useMemo<NodeTypes>(
    () => ({ assetNode: AssetNode, newAssetNode: NewAssetNode }),
    []
  );

  const {
    inspectByAssetId,
    inspectLoadingByAssetId,
    canLoadMoreByAssetId,
    loadMorePreviewRows,
    clearPreviewForAsset,
    getRowsForAsset,
  } = useAssetPreviews(visualAssets);

  const areVisualPreviewsReady = useMemo(() => {
    if (visualAssets.length === 0) {
      return true;
    }

    return visualAssets.every((visualAsset) =>
      Boolean(inspectByAssetId[visualAsset.id])
    );
  }, [inspectByAssetId, visualAssets]);

  const assetPreviewRows = useMemo(() => {
    if (!asset) {
      return [] as Record<string, unknown>[];
    }

    const limit = getTablePreviewLimit(asset.meta, 25);
    return getRowsForAsset(asset.id, limit) ?? selectedInspectRows ?? [];
  }, [asset, getRowsForAsset, selectedInspectRows]);

  const graph = useMemo(
    () =>
      buildFlowFromPipeline(
        enrichedPipeline,
        inspectByAssetId,
        inspectLoadingByAssetId,
        storedNodePositions,
        canLoadMoreByAssetId,
        loadMorePreviewRows,
        materializingAssetIds
      ),
    [
      canLoadMoreByAssetId,
      enrichedPipeline,
      inspectByAssetId,
      inspectLoadingByAssetId,
      loadMorePreviewRows,
      materializingAssetIds,
      storedNodePositions,
    ]
  );

  const connectedNodeIDs = useMemo(() => {
    const ids = new Set<string>();
    for (const edge of graph.edges) {
      ids.add(edge.source);
      ids.add(edge.target);
    }
    return ids;
  }, [graph.edges]);

  useGraphViewportFocus({
    reactFlowInstance,
    activePipelineId: enrichedPipeline?.id ?? null,
    recomputeVersion,
    graphNodes: graph.nodes,
    graphEdges: graph.edges,
    selectedAssetId,
    storedNodePositions,
    canvasContainerRef,
  });

  const handleRecomputeGraph = () => {
    if (!enrichedPipeline) {
      return;
    }

    const recomputedPositions = computeGraphLayoutPositions(
      enrichedPipeline,
      inspectByAssetId,
      inspectLoadingByAssetId
    );

    setStoredNodePositions((previous) => ({
      ...previous,
      ...recomputedPositions,
    }));
    setRecomputeVersion((previous) => previous + 1);
  };

  return {
    assetPreviewRows,
    areVisualPreviewsReady,
    canLoadMoreByAssetId,
    canvasContainerRef,
    clearPreviewForAsset,
    connectedNodeIDs,
    edges,
    getRowsForAsset,
    graph,
    handleRecomputeGraph,
    inspectByAssetId,
    inspectLoadingByAssetId,
    loadMorePreviewRows,
    nodeTypes,
    nodes,
    onEdgesChange,
    onNodesChange,
    reactFlowInstance,
    setEdges,
    setNodes,
    setReactFlowInstance,
    setStoredNodePositions,
    storedNodePositions,
  };
}
