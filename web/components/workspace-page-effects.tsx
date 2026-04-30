"use client";

import { useEffect, useRef } from "react";
import { UseFormReturn } from "react-hook-form";

import { AssetConfigForm } from "@/components/workspace-editor-pane";
import { WebPipeline, WebAsset } from "@/lib/types";

type WorkspacePageEffectsProps = {
  asset: WebAsset | null;
  enrichedPipeline: WebPipeline | null;
  areVisualPreviewsReady: boolean;
  inspectByAssetId: Record<string, { rows: Record<string, unknown>[] }>;
  inspectLoadingByAssetId: Record<string, boolean>;
  storedNodePositions: Record<string, { x: number; y: number }>;
  setStoredNodePositions: (
    updater: (
      previous: Record<string, { x: number; y: number }>
    ) => Record<string, { x: number; y: number }>
  ) => void;
  computeInitialPositions: () => Record<string, { x: number; y: number }>;
  form: UseFormReturn<AssetConfigForm>;
  isMobile: boolean;
  setMobileEditorOpen: (open: boolean) => void;
  selectedAsset: string | null;
  routeSelectedAsset: string | null;
  flushAssetSave: (assetId: string) => void;
};

export function WorkspacePageEffects({
  asset,
  enrichedPipeline,
  areVisualPreviewsReady,
  storedNodePositions,
  setStoredNodePositions,
  computeInitialPositions,
  form,
  isMobile,
  setMobileEditorOpen,
  selectedAsset,
  routeSelectedAsset,
  flushAssetSave,
}: WorkspacePageEffectsProps) {
  useEffect(() => {
    if (!enrichedPipeline || enrichedPipeline.assets.length === 0) {
      return;
    }

    if (!areVisualPreviewsReady) {
      return;
    }

    const assetIds = enrichedPipeline.assets.map((currentAsset) => currentAsset.id);
    const hasStoredPositionsForPipeline = assetIds.some(
      (assetId) => storedNodePositions[assetId]
    );

    if (hasStoredPositionsForPipeline) {
      return;
    }

    const initialPositions = computeInitialPositions();

    setStoredNodePositions((previous) => ({
      ...previous,
      ...initialPositions,
    }));
  }, [
    areVisualPreviewsReady,
    computeInitialPositions,
    enrichedPipeline,
    setStoredNodePositions,
    storedNodePositions,
  ]);

  useEffect(() => {
    form.reset({
      type: asset?.type ?? "",
      materialization: asset?.materialization_type ?? "",
      custom_checks: "",
      columns: "",
    });
  }, [asset?.materialization_type, asset?.name, asset?.type, form]);

  useEffect(() => {
    if (!isMobile) {
      setMobileEditorOpen(false);
      return;
    }

    if (asset && routeSelectedAsset) {
      setMobileEditorOpen(true);
    }
  }, [asset, isMobile, routeSelectedAsset, setMobileEditorOpen]);

  const previousSelectedAssetRef = useRef<string | null>(null);

  useEffect(() => {
    const previousSelectedAsset = previousSelectedAssetRef.current;
    if (previousSelectedAsset && previousSelectedAsset !== selectedAsset) {
      flushAssetSave(previousSelectedAsset);
    }
    previousSelectedAssetRef.current = selectedAsset;
  }, [flushAssetSave, selectedAsset]);
  return null;
}
