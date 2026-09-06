import { describe, expect, it, vi } from "vitest";
import { revealDiffAnnotation } from "./reveal-diff-annotation";

function fixture(ready = false) {
  let listener: (() => void) | undefined;
  const revealLineInCenter = vi.fn();
  const dispose = vi.fn(() => (listener = undefined));
  const diff = {
    getLineChanges: () => (ready ? [] : null),
    getModifiedEditor: () => ({ revealLineInCenter }),
    onDidUpdateDiff: (callback: () => void) => {
      listener = callback;
      return { dispose };
    },
  };
  return {
    diff,
    revealLineInCenter,
    dispose,
    update(computed = true) {
      ready = computed;
      listener?.();
    },
  };
}

describe("initial diff annotation reveal", () => {
  it("waits for the diff and its synchronous layout updates", async () => {
    const f = fixture();
    const cleanup = revealDiffAnnotation(f.diff, 18, 1);
    expect(f.revealLineInCenter).not.toHaveBeenCalled();
    f.update(false);
    await Promise.resolve();
    expect(f.revealLineInCenter).not.toHaveBeenCalled();
    f.update();
    expect(f.revealLineInCenter).not.toHaveBeenCalled();
    await Promise.resolve();
    expect(f.revealLineInCenter).toHaveBeenCalledExactlyOnceWith(18, 1);
    f.update();
    await Promise.resolve();
    expect(f.revealLineInCenter).toHaveBeenCalledTimes(1);
    expect(f.dispose).toHaveBeenCalled();
    cleanup();
  });

  it("reveals annotations even when ready SQL has no text differences", async () => {
    const f = fixture(true);
    const cleanup = revealDiffAnnotation(f.diff, 7, 1);
    await Promise.resolve();
    expect(f.revealLineInCenter).toHaveBeenCalledExactlyOnceWith(7, 1);
    cleanup();
  });

  it.each([false, true])("cancels on cleanup with queued reveal %s", async (queued) => {
    const f = fixture();
    const cleanup = revealDiffAnnotation(f.diff, 18, 1);
    if (queued) f.update();
    cleanup();
    f.update();
    await Promise.resolve();
    expect(f.revealLineInCenter).not.toHaveBeenCalled();
    expect(f.dispose).toHaveBeenCalled();
  });
});
