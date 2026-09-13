import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  NavigationArrivalTracker,
  highlightNavigationElement,
  navigationArrivalState,
  navigationTargetKey,
  whenNavigationElementReady,
  revealNavigationElement,
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

describe("owner-local arrival readiness", () => {
  let resized: () => void;
  const disconnect = vi.fn();
  beforeEach(() => {
    vi.useFakeTimers();
    disconnect.mockClear();
    vi.stubGlobal("requestAnimationFrame", (callback: () => void) => setTimeout(callback, 16));
    vi.stubGlobal("cancelAnimationFrame", (id: number) => clearTimeout(id));
    vi.stubGlobal("getComputedStyle", () => ({ visibility: "visible" }));
    vi.stubGlobal(
      "ResizeObserver",
      class {
        constructor(callback: () => void) {
          resized = callback;
        }
        observe() {}
        disconnect = disconnect;
      },
    );
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });
  const element = (visible = true) =>
    ({
      isConnected: true,
      getClientRects: vi.fn(() => (visible ? [{}] : [])),
      closest: vi.fn(() => null),
    }) as unknown as HTMLElement;

  it("acknowledges once after a hidden owner becomes visible without polling", () => {
    const target = element(false);
    const ready = vi.fn();
    whenNavigationElementReady(target, ready);
    vi.runAllTimers();
    expect(ready).not.toHaveBeenCalled();
    expect(vi.getTimerCount()).toBe(0);
    vi.mocked(target.getClientRects).mockReturnValue([{}] as unknown as DOMRectList);
    resized();
    vi.runAllTimers();
    expect(ready).toHaveBeenCalledExactlyOnceWith(target);
    expect(disconnect).toHaveBeenCalledOnce();
    resized();
    vi.runAllTimers();
    expect(ready).toHaveBeenCalledOnce();
  });
  it("cancels obsolete refs before they can focus an owner", () => {
    const ready = vi.fn();
    const cancel = whenNavigationElementReady(element(), ready);
    cancel();
    vi.runAllTimers();
    expect(ready).not.toHaveBeenCalled();
    const cancelHidden = whenNavigationElementReady(element(false), ready);
    vi.runAllTimers();
    cancelHidden();
    resized();
    vi.runAllTimers();
    expect(ready).not.toHaveBeenCalled();
    expect(disconnect).toHaveBeenCalledOnce();
  });
  it("waits for a responsive panel to finish entering, but cancels late completion", async () => {
    let finish!: () => void;
    let running = true;
    const target = element();
    const animation = {
      get playState() {
        return running ? "running" : "finished";
      },
      finished: new Promise<void>((resolve) => {
        finish = resolve;
      }),
    };
    vi.mocked(target.closest).mockReturnValue({
      getAnimations: () => [animation],
    } as unknown as HTMLElement);
    const ready = vi.fn();
    const cancel = whenNavigationElementReady(target, ready);
    await vi.runAllTimersAsync();
    expect(ready).not.toHaveBeenCalled();
    cancel();
    running = false;
    finish();
    await vi.runAllTimersAsync();
    expect(ready).not.toHaveBeenCalled();
    whenNavigationElementReady(target, ready);
    await vi.runAllTimersAsync();
    expect(ready).toHaveBeenCalledExactlyOnceWith(target);
  });
  it("reveals only the owner's viewport and leaves already visible sections still", () => {
    const viewport = {
      scrollTop: 40,
      getBoundingClientRect: () => ({ top: 100, bottom: 400, height: 300 }),
    };
    const target = {
      focus: vi.fn(),
      closest: vi.fn(() => viewport),
      getBoundingClientRect: vi.fn(() => ({ top: 150, bottom: 350, height: 200 })),
    };
    revealNavigationElement(target as unknown as HTMLElement);
    expect(target.focus).toHaveBeenCalledWith({ preventScroll: true });
    expect(viewport.scrollTop).toBe(40);
    target.getBoundingClientRect.mockReturnValue({ top: 500, bottom: 1500, height: 1000 });
    revealNavigationElement(target as unknown as HTMLElement);
    expect(viewport.scrollTop).toBe(440);
  });
});
