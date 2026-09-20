import { parseDetail, type ResourceDetail } from "./resource-navigation";

export function navigationTargetKey(project: string | undefined, detail: ResourceDetail) {
  return JSON.stringify({ project, detail: parseDetail(detail) });
}

export type ArrivalVisit = {
  visit: string;
  target: string;
  intent?: string;
  silent: boolean;
  traversal: boolean;
};

// Committed navigation only. A locator describes a place; an arrival describes
// a visit to that place, including another explicit visit to the same URL.
export class NavigationArrivalTracker {
  private previous?: ArrivalVisit;
  private sequence = 0;
  arrival?: { id: string; target: string };
  advance(next: ArrivalVisit): string | undefined {
    const previous = this.previous;
    if (previous?.visit === next.visit) return undefined;
    this.previous = next;
    if (!next.target || (next.silent && !next.traversal)) {
      this.arrival = undefined;
      return undefined;
    }
    if (
      !previous ||
      next.target !== previous.target ||
      next.intent !== previous.intent ||
      next.traversal
    ) {
      const id = String(++this.sequence);
      this.arrival = { id, target: next.target };
      return id;
    }
    return undefined;
  }
}

// getRandomValues also works when Renart is opened over plain HTTP on a LAN.
// These IDs identify navigation events; they never grant access or authority.
export const navigationToken = () =>
  Array.from(crypto.getRandomValues(new Uint32Array(4)), (value) => value.toString(16)).join("-");
export const navigationDocument = navigationToken();
export const navigationArrivalState = () => ({
  resourceArrival: navigationToken(),
  resourceReflection: undefined,
  resourceDocument: navigationDocument,
});

// A local owner ref can mount before its tab or responsive Sheet is visible.
// Observe only that element, then disconnect as soon as it is ready. This is
// event-driven: no document registry, DOM-wide observer or retry polling.
export function whenNavigationElementReady(
  element: HTMLElement,
  ready: (element: HTMLElement) => void,
): () => void {
  let disposed = false;
  let frame = 0;
  let observer: ResizeObserver | undefined;
  let waitingForPanel = false;
  const cleanup = () => {
    disposed = true;
    cancelAnimationFrame(frame);
    observer?.disconnect();
  };
  const schedule = () => {
    if (!disposed && !frame && !waitingForPanel) frame = requestAnimationFrame(reveal);
  };
  const reveal = () => {
    frame = 0;
    if (disposed || !element.isConnected) return;
    if (!element.getClientRects().length || getComputedStyle(element).visibility === "hidden") {
      if (!observer) {
        observer = new ResizeObserver(schedule);
        observer.observe(element);
      }
      return;
    }
    const panel = element.closest<HTMLElement>(
      '[data-slot="sheet-content"], [data-slot="dialog-content"]',
    );
    const entering = panel
      ?.getAnimations()
      .filter(
        (animation) =>
          animation.playState === "running" &&
          animation.effect?.getComputedTiming().endTime !== Infinity,
      );
    if (entering?.length) {
      waitingForPanel = true;
      void Promise.all(entering.map((animation) => animation.finished.catch(() => undefined))).then(
        () => {
          waitingForPanel = false;
          schedule();
        },
      );
      return;
    }
    cleanup();
    ready(element);
  };
  schedule();
  return cleanup;
}

export type NavigationRevealOptions = { block?: "center" };

export function revealNavigationElement(
  element: HTMLElement,
  { block }: NavigationRevealOptions = {},
) {
  element.focus({ preventScroll: true });
  const viewport = element.closest<HTMLElement>('[data-slot="scroll-area-viewport"]');
  if (!viewport) return;
  const target = element.getBoundingClientRect();
  const bounds = viewport.getBoundingClientRect();
  // Compact run rows need surrounding context (including the timeline axis
  // for the first row), without scrolling any outer workbench panels.
  if (block === "center" && target.height < bounds.height) {
    viewport.scrollTop = Math.max(
      0,
      viewport.scrollTop + target.top - bounds.top - (bounds.height - target.height) / 2,
    );
    return;
  }
  // Reveal only the destination's own scroll area. A tall section needs its
  // heading, not its footer, in view; an already visible target need not move.
  if (target.top < bounds.top || target.top >= bounds.bottom || target.height > bounds.height)
    viewport.scrollTop += target.top - bounds.top;
  else if (target.bottom > bounds.bottom) viewport.scrollTop += target.bottom - bounds.bottom;
}

export function highlightNavigationElement(element: HTMLElement): () => void {
  const id = navigationToken();
  element.removeAttribute("data-navigation-arrival");
  // Restart the same short CSS animation on a repeated arrival.
  void element.offsetWidth;
  element.setAttribute("data-navigation-arrival", "true");
  element.setAttribute("data-navigation-arrival-id", id);
  const cleanup = () => {
    if (element.getAttribute("data-navigation-arrival-id") !== id) return;
    element.removeAttribute("data-navigation-arrival");
    element.removeAttribute("data-navigation-arrival-id");
  };
  const timer = setTimeout(cleanup, 750);
  return () => {
    clearTimeout(timer);
    cleanup();
  };
}
