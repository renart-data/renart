import { useAtomValue } from "jotai";

import { workspaceAtom } from "@/lib/atoms/workspace";
import type { WorkspaceConfigConnectionType, WorkspaceConfigResponse } from "@/lib/types";

/**
 * Creating new Ingestr connections requires explicit project opt-in. Existing
 * assets and connections remain editable; their presence is not a recommendation
 * to create more. Loaded config takes precedence over an older workspace snapshot.
 */
export function useIngestrEnabled(
  workspaceConfig?: Pick<WorkspaceConfigResponse, "features"> | null,
): boolean {
  const workspace = useAtomValue(workspaceAtom);
  return ingestrCreationEnabled(workspaceConfig, workspace);
}

export function ingestrCreationEnabled(
  config?: Pick<WorkspaceConfigResponse, "features"> | null,
  workspace?: Pick<WorkspaceConfigResponse, "features"> | null,
): boolean {
  return Boolean((config ?? workspace)?.features?.ingestr);
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
