import { useAtomValue } from "jotai";

import { isIngestrAssetType } from "@/lib/asset-types";
import { workspaceAtom } from "@/lib/atoms/workspace";
import type { WorkspaceConfigConnectionType, WorkspaceConfigResponse } from "@/lib/types";

/**
 * Whether ingestr surfaces (source connection types, asset options) are
 * visible: either the project opted in via the `ingestr` feature flag in
 * .renart/project.yml, or the workspace already contains ingestr assets —
 * those must keep working and stay visible regardless of the flag.
 */
export function useIngestrEnabled(
  workspaceConfig?: Pick<WorkspaceConfigResponse, "features"> | null,
): boolean {
  const workspace = useAtomValue(workspaceAtom);
  if (workspaceConfig?.features?.ingestr || workspace?.features?.ingestr) {
    return true;
  }
  return (workspace?.pipelines ?? []).some((pipeline) =>
    pipeline.assets.some((asset) => isIngestrAssetType(asset.type)),
  );
}

/**
 * Connection types offered in settings: warehouse and object-storage types
 * always; ingestr/SaaS source types only when the ingestr feature is enabled.
 */
export function visibleConnectionTypes(
  types: WorkspaceConfigConnectionType[],
  ingestrEnabled: boolean,
): WorkspaceConfigConnectionType[] {
  if (ingestrEnabled) {
    return types;
  }
  return types.filter((type) => type.category !== "source");
}
