import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type Dispatch,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";

import type { AppRouteNavigation, AppToolId } from "../app-navigation-model";
import {
  useWorkbenchSessionState,
  type WorkbenchSessionAction,
  type WorkbenchSessionState,
} from "./workbench-session-state";

export type WorkbenchSlotName = "context" | "inspector";

type WorkbenchContextValue = {
  navigation: AppRouteNavigation | null;
  session: WorkbenchSessionState;
  dispatch: Dispatch<WorkbenchSessionAction>;
  contextHost: HTMLElement | null;
  inspectorHost: HTMLElement | null;
  setContextHost: (host: HTMLElement | null) => void;
  setInspectorHost: (host: HTMLElement | null) => void;
  hasContextSlot: boolean;
  hasInspectorSlot: boolean;
  registerSlot: (slot: WorkbenchSlotName) => () => void;
  registerToolAction: (tool: AppToolId, action: () => void) => () => void;
  hasToolAction: (tool: AppToolId) => boolean;
  invokeToolAction: (tool: AppToolId) => boolean;
  mobileNavigationOpen: boolean;
  setMobileNavigationOpen: (open: boolean) => void;
};

const WorkbenchContext = createContext<WorkbenchContextValue | null>(null);

export function WorkbenchProvider({
  navigation,
  projectId,
  children,
}: {
  navigation: AppRouteNavigation | null;
  projectId: string;
  children: ReactNode;
}) {
  const { state: session, dispatch } = useWorkbenchSessionState(projectId, navigation);
  const [contextHost, setContextHost] = useState<HTMLElement | null>(null);
  const [inspectorHost, setInspectorHost] = useState<HTMLElement | null>(null);
  const [slotCounts, setSlotCounts] = useState<Record<WorkbenchSlotName, number>>({
    context: 0,
    inspector: 0,
  });
  const [mobileNavigationOpen, setMobileNavigationOpen] = useState(false);
  const [toolActions, setToolActions] = useState<Partial<Record<AppToolId, () => void>>>({});

  useEffect(() => {
    if (!navigation) return;
    dispatch({ type: "route-entered", mode: navigation.mode, tool: navigation.tool });
  }, [dispatch, navigation?.mode, navigation?.tool]);

  useEffect(() => {
    setMobileNavigationOpen(false);
  }, [navigation?.mode]);

  const registerSlot = useCallback((slot: WorkbenchSlotName) => {
    setSlotCounts((current) => ({ ...current, [slot]: current[slot] + 1 }));
    return () => {
      setSlotCounts((current) => ({ ...current, [slot]: Math.max(0, current[slot] - 1) }));
    };
  }, []);
  const registerToolAction = useCallback((tool: AppToolId, action: () => void) => {
    setToolActions((current) => ({ ...current, [tool]: action }));
    return () => {
      setToolActions((current) => {
        if (current[tool] !== action) return current;
        const next = { ...current };
        delete next[tool];
        return next;
      });
    };
  }, []);
  const hasToolAction = useCallback((tool: AppToolId) => Boolean(toolActions[tool]), [toolActions]);
  const invokeToolAction = useCallback(
    (tool: AppToolId) => {
      const action = toolActions[tool];
      if (!action) return false;
      action();
      return true;
    },
    [toolActions],
  );

  const value = useMemo<WorkbenchContextValue>(
    () => ({
      navigation,
      session,
      dispatch,
      contextHost,
      inspectorHost,
      setContextHost,
      setInspectorHost,
      hasContextSlot: slotCounts.context > 0,
      hasInspectorSlot: slotCounts.inspector > 0,
      registerSlot,
      registerToolAction,
      hasToolAction,
      invokeToolAction,
      mobileNavigationOpen,
      setMobileNavigationOpen,
    }),
    [
      contextHost,
      inspectorHost,
      mobileNavigationOpen,
      navigation,
      hasToolAction,
      invokeToolAction,
      registerSlot,
      registerToolAction,
      session,
      slotCounts.context,
      slotCounts.inspector,
    ],
  );

  return <WorkbenchContext.Provider value={value}>{children}</WorkbenchContext.Provider>;
}

export function WorkbenchToolAction({ tool, action }: { tool: AppToolId; action: () => void }) {
  const { registerToolAction } = useWorkbench();
  const actionRef = useRef(action);
  actionRef.current = action;

  useEffect(() => registerToolAction(tool, () => actionRef.current()), [registerToolAction, tool]);

  return null;
}

export function useWorkbench() {
  const context = useContext(WorkbenchContext);
  if (!context) {
    throw new Error("useWorkbench must be used inside WorkbenchProvider");
  }
  return context;
}

export function WorkbenchPortal({
  slot,
  children,
}: {
  slot: WorkbenchSlotName;
  children: ReactNode;
}) {
  const { contextHost, inspectorHost, registerSlot } = useWorkbench();
  const host = slot === "context" ? contextHost : inspectorHost;

  useEffect(() => registerSlot(slot), [registerSlot, slot]);

  return host ? createPortal(children, host) : null;
}
