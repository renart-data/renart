import type { WebAsset, WorkspaceState } from "@/lib/types";

export function mergeWorkspaceWithPreservedContent(
  current: WorkspaceState | null,
  incoming: WorkspaceState,
  changedAssetIds: string[] = [],
): WorkspaceState {
  if (!current) {
    return incoming;
  }

  const changedAssetIdSet = new Set(changedAssetIds);
  const currentAssetById = new Map<string, WebAsset>();
  for (const pipeline of current.pipelines ?? []) {
    for (const asset of pipeline.assets ?? []) {
      if (asset.id) {
        currentAssetById.set(asset.id, asset);
      }
    }
  }

  const nextPipelines = (incoming.pipelines ?? []).map((pipeline) => ({
    ...pipeline,
    assets: (pipeline.assets ?? []).map((asset) => {
      const currentAsset = currentAssetById.get(asset.id);
      if (!currentAsset) {
        return asset;
      }

      const isChangedAsset = changedAssetIdSet.has(asset.id);

      return {
        ...currentAsset,
        ...asset,
        content: asset.content || currentAsset.content,
        meta: asset.meta ?? (isChangedAsset ? asset.meta : currentAsset.meta),
        columns: asset.columns ?? (isChangedAsset ? asset.columns : currentAsset.columns),
        // These clear to empty (e.g. removing the last tag, blanking the owner,
        // or fixing a syntax error so the asset parses again). The backend omits
        // empty values, so for a changed asset we must take the incoming (absent)
        // value rather than let the spread keep the stale one.
        tags: asset.tags ?? (isChangedAsset ? asset.tags : currentAsset.tags),
        owner: asset.owner ?? (isChangedAsset ? asset.owner : currentAsset.owner),
        incremental_key:
          asset.incremental_key ??
          (isChangedAsset ? asset.incremental_key : currentAsset.incremental_key),
        parse_error:
          asset.parse_error ?? (isChangedAsset ? asset.parse_error : currentAsset.parse_error),
      };
    }),
  }));

  return {
    ...incoming,
    pipelines: nextPipelines,
  };
}
