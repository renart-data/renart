import type { Page } from "@playwright/test";

type Arrival = {
  id: string;
  tag: string;
  label: string | null;
  section: string | null;
  animation: string;
  visible: boolean;
};
type ProbeWindow = Window & { arrivals: Arrival[] };

// Test-only instrumentation records the short treatment, even if it finishes
// before a slower browser assertion. Production has no document-wide observer.
export async function observeArrivals(page: Page) {
  await page.addInitScript(() => {
    const probe = window as unknown as ProbeWindow;
    probe.arrivals = [];
    new MutationObserver((records) => {
      for (const { target } of records) {
        if (!(target instanceof HTMLElement)) continue;
        const id = target.getAttribute("data-navigation-arrival-id");
        if (!id || probe.arrivals.some((arrival) => arrival.id === id)) continue;
        probe.arrivals.push({
          id,
          tag: target.tagName,
          label: target.getAttribute("aria-label"),
          section: target.getAttribute("data-navigation-section"),
          animation: getComputedStyle(target).animationName,
          visible: target.getClientRects().length > 0,
        });
      }
    }).observe(document, {
      subtree: true,
      attributes: true,
      attributeFilter: ["data-navigation-arrival-id"],
    });
  });
}

export const arrivals = (page: Page) =>
  page.evaluate(() => (window as unknown as ProbeWindow).arrivals);

export const clearArrivals = (page: Page) =>
  page.evaluate(() => {
    (window as unknown as ProbeWindow).arrivals = [];
  });
