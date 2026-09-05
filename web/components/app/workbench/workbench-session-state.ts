import { useEffect, useMemo, useReducer } from "react";

import type { AppModeId, AppToolId } from "../app-navigation-model";

export const WORKBENCH_SESSION_VERSION = 1;
const MAX_SESSION_BYTES = 32 * 1024;
const MIN_SIDEBAR_WIDTH = 240;
const MAX_SIDEBAR_WIDTH = 420;

export type WorkbenchModeSession = {
  activeTool: AppToolId;
  sidebarOpen: boolean;
  sidebarWidth: number;
  expandedTreeNodes: string[];
  sidebarScrollTop: number;
};

export type WorkbenchSessionState = {
  version: typeof WORKBENCH_SESSION_VERSION;
  projectId: string;
  activeMode: AppModeId;
  modes: Record<AppModeId, WorkbenchModeSession>;
};

type WorkbenchRouteSelection = {
  mode: AppModeId;
  tool: AppToolId;
};

export type WorkbenchSessionAction =
  | { type: "route-entered"; mode: AppModeId; tool: AppToolId }
  | { type: "tool-selected"; mode: AppModeId; tool: AppToolId }
  | { type: "active-tool-toggled"; mode: AppModeId; tool: AppToolId }
  | { type: "sidebar-resized"; mode: AppModeId; width: number }
  | { type: "tree-branch-toggled"; mode: AppModeId; resourceId: string }
  | { type: "sidebar-scrolled"; mode: AppModeId; scrollTop: number }
  | { type: "state-restored"; state: WorkbenchSessionState }
  | { type: "project-changed"; projectId: string };

const defaultTools: Record<AppModeId, AppToolId> = {
  build: "resources",
  run: "overview",
  explore: "catalog",
};
const validTools = new Set<AppToolId>([
  "resources",
  "ad-hoc",
  "notebooks",
  "data",
  "connections",
  "environments",
  "pipeline-settings",
  "project-settings",
  "overview",
  "deployments",
  "schedules",
  "runs",
  "catalog",
  "dashboards",
  "reports",
]);

function defaultModeState(mode: AppModeId): WorkbenchModeSession {
  return {
    activeTool: defaultTools[mode],
    sidebarOpen: true,
    sidebarWidth: 288,
    expandedTreeNodes: [],
    sidebarScrollTop: 0,
  };
}

export function createWorkbenchSessionState(
  projectId: string,
  activeMode: AppModeId = "build",
): WorkbenchSessionState {
  return {
    version: WORKBENCH_SESSION_VERSION,
    projectId,
    activeMode,
    modes: {
      build: defaultModeState("build"),
      run: defaultModeState("run"),
      explore: defaultModeState("explore"),
    },
  };
}

export function workbenchSessionReducer(
  state: WorkbenchSessionState,
  action: WorkbenchSessionAction,
): WorkbenchSessionState {
  if (action.type === "state-restored") {
    return action.state;
  }
  if (action.type === "project-changed") {
    return createWorkbenchSessionState(action.projectId, state.activeMode);
  }

  const modeState = state.modes[action.mode];
  if (action.type === "route-entered") {
    const sharedBuildContext =
      action.mode === "build" &&
      ["resources", "ad-hoc"].includes(action.tool) &&
      modeState.activeTool === "data";
    return updateMode(state, action.mode, {
      ...modeState,
      activeTool: sharedBuildContext ? modeState.activeTool : action.tool,
      sidebarOpen: modeState.sidebarOpen,
    });
  }
  if (action.type === "tool-selected") {
    return updateMode(state, action.mode, {
      ...modeState,
      activeTool: action.tool,
      sidebarOpen: true,
    });
  }
  if (action.type === "active-tool-toggled") {
    return updateMode(state, action.mode, {
      ...modeState,
      activeTool: action.tool,
      sidebarOpen: modeState.activeTool === action.tool ? !modeState.sidebarOpen : true,
    });
  }
  if (action.type === "sidebar-resized") {
    return updateMode(state, action.mode, {
      ...modeState,
      sidebarWidth: clampSidebarWidth(action.width),
    });
  }
  if (action.type === "tree-branch-toggled") {
    const expanded = new Set(modeState.expandedTreeNodes);
    if (expanded.has(action.resourceId)) {
      expanded.delete(action.resourceId);
    } else if (expanded.size < 200) {
      expanded.add(action.resourceId);
    }
    return updateMode(state, action.mode, {
      ...modeState,
      expandedTreeNodes: [...expanded],
    });
  }
  return updateMode(state, action.mode, {
    ...modeState,
    sidebarScrollTop: Math.max(0, Math.round(action.scrollTop)),
  });
}

function updateMode(
  state: WorkbenchSessionState,
  mode: AppModeId,
  nextMode: WorkbenchModeSession,
): WorkbenchSessionState {
  return {
    ...state,
    activeMode: mode,
    modes: { ...state.modes, [mode]: nextMode },
  };
}

function clampSidebarWidth(width: number) {
  if (!Number.isFinite(width)) return 288;
  return Math.min(MAX_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, Math.round(width)));
}

function sessionKey(projectId: string) {
  return `renart.workbench.v${WORKBENCH_SESSION_VERSION}.${encodeURIComponent(projectId)}`;
}

export function parseWorkbenchSession(
  serialized: string | null,
  projectId: string,
): WorkbenchSessionState {
  const fallback = createWorkbenchSessionState(projectId);
  if (!serialized || serialized.length > MAX_SESSION_BYTES) return fallback;
  try {
    const value = JSON.parse(serialized) as Partial<WorkbenchSessionState>;
    if (
      value.version !== WORKBENCH_SESSION_VERSION ||
      value.projectId !== projectId ||
      !isMode(value.activeMode) ||
      !value.modes
    ) {
      return fallback;
    }
    return {
      version: WORKBENCH_SESSION_VERSION,
      projectId,
      activeMode: value.activeMode,
      modes: {
        build: parseMode(value.modes.build, "build"),
        run: parseMode(value.modes.run, "run"),
        explore: parseMode(value.modes.explore, "explore"),
      },
    };
  } catch {
    return fallback;
  }
}

function parseMode(value: unknown, mode: AppModeId): WorkbenchModeSession {
  const fallback = defaultModeState(mode);
  if (!value || typeof value !== "object") return fallback;
  const candidate = value as Partial<WorkbenchModeSession>;
  return {
    activeTool: isTool(candidate.activeTool) ? candidate.activeTool : fallback.activeTool,
    sidebarOpen:
      typeof candidate.sidebarOpen === "boolean" ? candidate.sidebarOpen : fallback.sidebarOpen,
    sidebarWidth: clampSidebarWidth(candidate.sidebarWidth ?? fallback.sidebarWidth),
    expandedTreeNodes: Array.isArray(candidate.expandedTreeNodes)
      ? candidate.expandedTreeNodes
          .filter((item): item is string => typeof item === "string")
          .slice(0, 200)
      : [],
    sidebarScrollTop:
      typeof candidate.sidebarScrollTop === "number"
        ? Math.max(0, Math.round(candidate.sidebarScrollTop))
        : 0,
  };
}

function isMode(value: unknown): value is AppModeId {
  return value === "build" || value === "run" || value === "explore";
}

function isTool(value: unknown): value is AppToolId {
  return typeof value === "string" && validTools.has(value as AppToolId);
}

function loadWorkbenchSession(projectId: string) {
  if (typeof window === "undefined") return createWorkbenchSessionState(projectId);
  try {
    return parseWorkbenchSession(window.sessionStorage.getItem(sessionKey(projectId)), projectId);
  } catch {
    return createWorkbenchSessionState(projectId);
  }
}

export function reconcileWorkbenchSessionRoute(
  state: WorkbenchSessionState,
  route: WorkbenchRouteSelection | null,
) {
  if (!route) return state;
  return workbenchSessionReducer(state, {
    type: "route-entered",
    mode: route.mode,
    tool: route.tool,
  });
}

function loadWorkbenchSessionForRoute({
  projectId,
  route,
}: {
  projectId: string;
  route: WorkbenchRouteSelection | null;
}) {
  return reconcileWorkbenchSessionRoute(loadWorkbenchSession(projectId), route);
}

export function useWorkbenchSessionState(
  projectId: string,
  route: WorkbenchRouteSelection | null = null,
) {
  const [state, dispatch] = useReducer(
    workbenchSessionReducer,
    { projectId, route },
    loadWorkbenchSessionForRoute,
  );

  useEffect(() => {
    if (state.projectId === projectId) return;
    dispatch({
      type: "state-restored",
      state: loadWorkbenchSessionForRoute({ projectId, route }),
    });
  }, [projectId, route, state.projectId]);

  useEffect(() => {
    if (typeof window === "undefined" || state.projectId !== projectId) return;
    try {
      window.sessionStorage.setItem(sessionKey(projectId), JSON.stringify(state));
    } catch {
      // Session storage is optional. Private browsing and quota failures must
      // not prevent the workbench from rendering.
    }
  }, [projectId, state]);

  return useMemo(() => ({ state, dispatch }), [state]);
}
