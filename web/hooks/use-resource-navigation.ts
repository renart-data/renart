import { useEffect, useRef } from "react";
import { useLocation, useNavigate, useRouter } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import { workspaceAtom, selectedEnvironmentAtom } from "@/lib/atoms/workspace";
import { getPinnedProjectId } from "@/lib/project-context";
import { useWorkspaceSettingsData } from "./use-workspace-settings-data";
import { detailSearch, parseDetail, type ResourceSearch } from "@/lib/resource-navigation";
import { LocalResourceReflection } from "@/lib/local-resource-reflection";
import { resourceDestination } from "@/lib/ui-navigation";
import type { ResourceTarget } from "@/lib/generated/api-types";

type ReflectionState = { resourceReflection?: string };

export function useResourceNavigation() {
  const location = useLocation();
  const navigate = useNavigate();
  const router = useRouter();
  const reflection = useRef(new LocalResourceReflection());
  useEffect(
    () =>
      router.history.subscribe(({ action, location }) => {
        reflection.current.observe(
          action.type,
          (location.state as ReflectionState).resourceReflection,
        );
      }),
    [router],
  );
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
    isLocalReflection: reflection.current.active,
    destination,
    // The owner is already visible. Only reflect its locator; do not apply the
    // destination's reveal policy (editor modes, focus, scroll or open panels).
    reflect: (target: ResourceTarget, environment?: string) => {
      if (!project) return;
      const nextDetail = parseDetail({
        v: 1,
        environment: environment || selectedEnvironment || workspaceConfig?.default_environment,
        target,
      });
      const token = crypto.randomUUID();
      reflection.current.begin(token);
      return navigate({
        to: ".",
        search: (search) => detailSearch(search, project, nextDetail),
        state: (state) => ({ ...state, resourceReflection: token }),
        replace: true,
        resetScroll: false,
      });
    },
    open: (target: ResourceTarget, environment?: string, replace = false) => {
      const next = destination(target, environment);
      if (next)
        return navigate({
          to: next.pathname,
          search: next.search,
          state: (state) => ({ ...state, resourceReflection: undefined }),
          replace,
        });
    },
    clear: () => navigate({ to: ".", search: (s) => ({ ...s, detail: undefined }), replace: true }),
  };
}
