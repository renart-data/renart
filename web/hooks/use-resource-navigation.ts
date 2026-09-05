import { useLocation, useNavigate } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import { workspaceAtom, selectedEnvironmentAtom } from "@/lib/atoms/workspace";
import { getPinnedProjectId } from "@/lib/project-context";
import { useWorkspaceSettingsData } from "./use-workspace-settings-data";
import { parseDetail, type ResourceSearch } from "@/lib/resource-navigation";
import { resourceDestination } from "@/lib/ui-navigation";
import type { ResourceTarget } from "@/lib/generated/api-types";

export function useResourceNavigation() {
  const location = useLocation();
  const navigate = useNavigate();
  const workspace = useAtomValue(workspaceAtom);
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const { workspaceConfig } = useWorkspaceSettingsData();
  const project = getPinnedProjectId() ?? workspaceConfig?.project_id;
  const detail = (location.search as ResourceSearch).detail;
  const destination = (target: ResourceTarget, environment?: string) => {
    if (!workspace || !project) return undefined;
    return resourceDestination(
      location,
      project,
      parseDetail({
        v: 1,
        environment: environment || selectedEnvironment || workspaceConfig?.default_environment,
        target,
      }),
      workspace,
    );
  };
  return {
    detail,
    destination,
    open: (target: ResourceTarget, environment?: string, replace = false) => {
      const next = destination(target, environment);
      if (next) return navigate({ to: next.pathname, search: next.search, replace });
    },
    clear: () => navigate({ to: ".", search: (s) => ({ ...s, detail: undefined }), replace: true }),
  };
}
