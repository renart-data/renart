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
