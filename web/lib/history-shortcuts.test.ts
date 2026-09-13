import { describe, expect, it, vi } from "vitest";
import { handleHistoryShortcut } from "./history-shortcuts";

function event(key: string, overrides = {}) {
  return {
    key,
    altKey: true,
    ctrlKey: false,
    metaKey: false,
    shiftKey: false,
    isComposing: false,
    defaultPrevented: false,
    preventDefault: vi.fn(),
    ...overrides,
  };
}

describe("history shortcuts", () => {
  it("navigates once through router history, suppressing native double navigation", () => {
    const history = { back: vi.fn(), forward: vi.fn() };
    for (const [key, action] of [
      ["ArrowLeft", "back"],
      ["ArrowRight", "forward"],
    ] as const) {
      const e = event(key);
      handleHistoryShortcut(e, history);
      expect(history[action]).toHaveBeenCalledTimes(1);
      expect(e.preventDefault).toHaveBeenCalledOnce();
    }
  });
  it("leaves editing, modified keys, composition and consumed events alone", () => {
    const history = { back: vi.fn(), forward: vi.fn() };
    for (const overrides of [
      { altKey: false },
      { ctrlKey: true },
      { metaKey: true },
      { shiftKey: true },
      { isComposing: true },
      { defaultPrevented: true },
      { key: "ArrowDown" },
    ]) {
      const e = event("ArrowLeft", overrides);
      handleHistoryShortcut(e, history);
      expect(e.preventDefault).not.toHaveBeenCalled();
    }
    expect(history.back).not.toHaveBeenCalled();
    expect(history.forward).not.toHaveBeenCalled();
  });
});
