import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useLocation, useRouter, useRouterState } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import { workspaceAtom } from "@/lib/atoms/workspace";
import { resourceDestination } from "@/lib/ui-navigation";
import { parseDetail, type ResourceSearch } from "@/lib/resource-navigation";
import {
  NavigationArrivalTracker,
  navigationDocument,
  highlightNavigationElement,
  navigationTargetKey,
} from "@/lib/navigation-arrival";

const ArrivalContext = createContext<{ id: string; target: string } | undefined>(undefined);

export function NavigationArrivalProvider({ children }: { children: ReactNode }) {
  const router = useRouter();
  const location = useRouterState({ select: (state) => state.resolvedLocation });
  const tracker = useRef(new NavigationArrivalTracker());
  const action = useRef("");
  const handledVisit = useRef("");
  const [arrival, setArrival] = useState<{ id: string; target: string }>();
  useEffect(
    () =>
      router.history.subscribe(({ action: next }) => {
        action.current = next.type;
      }),
    [router],
  );
  const state = location?.state as
    | {
        __TSR_key?: string;
        key?: string;
        resourceArrival?: string;
        resourceReflection?: string;
        resourceDocument?: string;
      }
    | undefined;
  const rawDetail = (location?.search as ResourceSearch | undefined)?.detail;
  let detail: ResourceSearch["detail"];
  try {
    detail = rawDetail ? parseDetail(rawDetail) : undefined;
  } catch {
    /* Route validation owns invalid links. */
  }
  const project = (location?.search as ResourceSearch | undefined)?.project;
  const workspace = useAtomValue(workspaceAtom);
  let ownerReady = !rawDetail;
  if (detail && project && workspace && location) {
    try {
      // resolvedLocation retains raw search values; mounted route owners see
      // parsed ones. Compare the validated locator, not JSON property order.
      const current = { ...location, search: { ...location.search, detail } };
      const owner = resourceDestination(current, project, detail, workspace);
      ownerReady =
        owner.pathname === location.pathname &&
        JSON.stringify(owner.search) === JSON.stringify(current.search);
    } catch {
      /* Unavailable owners are reported by ResourceNavigation. */
    }
  }
  const target = detail ? navigationTargetKey(project, detail) : "";
  const visit = JSON.stringify([
    location?.href,
    state?.__TSR_key ?? state?.key,
    state?.resourceArrival,
    state?.resourceReflection,
  ]);
  const traversal = ["GO", "BACK", "FORWARD"].includes(action.current);
  const silent =
    Boolean(state?.resourceReflection && state.resourceDocument === navigationDocument) &&
    !traversal;
  useEffect(() => {
    if (!ownerReady) return;
    if (handledVisit.current === visit) return;
    handledVisit.current = visit;
    tracker.current.advance({
      visit,
      target,
      intent: state?.resourceArrival,
      silent,
      traversal,
    });
    // Default search normalization must not discard an arrival while a lazy
    // owner (e.g. Monaco) is still loading. Keep its ID, without retriggering it.
    setArrival(tracker.current.arrival);
  }, [visit, target, ownerReady]);
  return (
    <ArrivalContext.Provider
      value={ownerReady && !silent && arrival?.target === target ? arrival : undefined}
    >
      {children}
    </ArrivalContext.Provider>
  );
}

export function useNavigationArrival(detail: ResourceSearch["detail"]) {
  const arrival = useContext(ArrivalContext);
  const project = useLocation({
    select: (location) => (location.search as ResourceSearch).project,
  });
  return detail && arrival?.target === navigationTargetKey(project, detail)
    ? arrival.id
    : undefined;
}

// Owners call this only after their normal reveal/focus lifecycle succeeds.
// No selectors, polling or hidden copies of editors are registered globally.
export function useArrivalHighlight(token?: string) {
  const lifecycle = useMemo(() => ({ cleanup: undefined as (() => void) | undefined }), [token]);
  useEffect(() => () => lifecycle.cleanup?.(), [lifecycle]);
  return useCallback(
    (element: HTMLElement) => {
      if (!token) return;
      lifecycle.cleanup?.();
      lifecycle.cleanup = highlightNavigationElement(element);
    },
    [token, lifecycle],
  );
}
