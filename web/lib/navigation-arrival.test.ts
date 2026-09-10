import { describe, expect, it, vi } from "vitest";
import {
  NavigationArrivalTracker,
  highlightNavigationElement,
  navigationArrivalState,
  navigationTargetKey,
} from "./navigation-arrival";

describe("committed navigation arrivals", () => {
  it("normalizes locator property order while keeping projects independent", () => {
    const target = {
      kind: "asset-section",
      asset_id: "a",
      section: "source",
      line: 1,
      end_line: 1,
      source_fingerprint: "fnv1a64:0000000000000000",
    } as const;
    const first = { v: 1, environment: "default", target } as const;
    const { source_fingerprint, ...location } = target;
    const reordered = {
      ...first,
      target: { source_fingerprint, ...location },
    };
    expect(navigationTargetKey("one", first)).toBe(navigationTargetKey("one", reordered));
    expect(navigationTargetKey("one", first)).not.toBe(navigationTargetKey("two", first));
  });
  it("distinguishes repeated explicit visits and ignores unrelated state updates", () => {
    const tracker = new NavigationArrivalTracker();
    const initial = { visit: "1", target: "column", silent: false, traversal: false };
    expect(tracker.advance(initial)).toBe("1");
    const pending = tracker.arrival;
    expect(tracker.advance(initial)).toBeUndefined();
    expect(tracker.advance({ ...initial, visit: "2" })).toBeUndefined();
    expect(tracker.arrival).toBe(pending);
    expect(tracker.advance({ ...initial, visit: "3", intent: "click" })).toBe("2");
    expect(tracker.advance({ ...initial, visit: "4", intent: "click-again" })).toBe("3");
  });
  it("keeps local reflection silent but highlights history and cold arrivals", () => {
    const tracker = new NavigationArrivalTracker();
    const local = { visit: "1", target: "column", silent: true, traversal: false };
    expect(tracker.advance(local)).toBeUndefined();
    expect(tracker.advance({ ...local, visit: "2", traversal: true })).toBe("1");
    expect(new NavigationArrivalTracker().advance({ ...local, silent: false })).toBe("1");
    expect(tracker.advance({ ...local, visit: "3", target: "" })).toBeUndefined();
    expect(tracker.arrival).toBeUndefined();
  });
  it("cleans up highlights without letting an old visit erase a newer one", () => {
    vi.useFakeTimers();
    const attributes = new Map<string, string>();
    const element = {
      offsetWidth: 100,
      setAttribute: (key: string, value: string) => attributes.set(key, value),
      getAttribute: (key: string) => attributes.get(key),
      removeAttribute: (key: string) => attributes.delete(key),
    } as unknown as HTMLElement;
    try {
      const first = highlightNavigationElement(element);
      vi.advanceTimersByTime(100);
      const second = highlightNavigationElement(element);
      first();
      expect(attributes.get("data-navigation-arrival")).toBe("true");
      vi.advanceTimersByTime(650);
      expect(attributes.get("data-navigation-arrival")).toBe("true");
      vi.advanceTimersByTime(100);
      expect(attributes.size).toBe(0);
      second();
      expect(vi.getTimerCount()).toBe(0);
    } finally {
      vi.useRealTimers();
    }
  });
  it("creates independent intent tokens without requiring secure-context randomUUID", () => {
    const first = navigationArrivalState();
    const second = navigationArrivalState();
    expect(first.resourceArrival).not.toBe(second.resourceArrival);
    expect(first.resourceDocument).toBe(second.resourceDocument);
    expect(first.resourceReflection).toBeUndefined();
  });
});
