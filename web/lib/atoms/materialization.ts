import { atom } from "jotai";

import { WebAsset, WebPipeline } from "@/lib/types";

import { pipelineAtom, resolvedSelectedAssetAtom } from "./selection";

export type MaterializationState = {
  is_materialized: boolean;
  freshness_status?: "fresh" | "stale";
  materialized_as?: string;
  row_count?: number;
  connection?: string;
  materialization_type?: string;
};

export type MaterializationByAssetId = Record<string, MaterializationState>;

export const materializationByAssetIdAtom = atom<MaterializationByAssetId>({});
export const materializingAssetIdsAtom = atom<Set<string>>(new Set<string>());

export const enrichedPipelineAtom = atom<WebPipeline | null>((get) => {
  const pipeline = get(pipelineAtom);
  const materializationByAssetId = get(materializationByAssetIdAtom);

  if (!pipeline) {
    return null;
  }

  return {
    ...pipeline,
    assets: pipeline.assets.map((pipelineAsset) => {
      const lazy = materializationByAssetId[pipelineAsset.id];
      if (!lazy) {
        return pipelineAsset;
      }

      return {
        ...pipelineAsset,
        is_materialized: lazy.is_materialized,
        freshness_status:
          lazy.freshness_status ?? pipelineAsset.freshness_status,
        materialized_as: lazy.materialized_as ?? pipelineAsset.materialized_as,
        row_count:
          lazy.row_count === undefined
            ? pipelineAsset.row_count
            : lazy.row_count,
        connection: lazy.connection ?? pipelineAsset.connection,
        materialization_type:
          lazy.materialization_type ?? pipelineAsset.materialization_type,
      };
    }),
  };
});

export const enrichedSelectedAssetAtom = atom<WebAsset | null>((get) => {
  const pipeline = get(enrichedPipelineAtom);
  const selectedAsset = get(resolvedSelectedAssetAtom);

  if (!pipeline || !selectedAsset) {
    return null;
  }

  return pipeline.assets.find((asset) => asset.id === selectedAsset) ?? null;
});
