import { expect, test } from "@playwright/test";

import {
  awaitWorkspaceSaves,
  registerWorkspaceSaveParticipant,
  retireWorkspaceSaveParticipant,
  trackWorkspaceSave,
} from "../../../lib/workspace-save-barrier";

test.describe("workspace save barrier", () => {
  test("awaits a retired editor save until it completes", async () => {
    let finishSave!: () => void;
    const save = new Promise<void>((resolve) => {
      finishSave = resolve;
    });

    retireWorkspaceSaveParticipant(() => save);
    let barrierFinished = false;
    const barrier = awaitWorkspaceSaves().then(() => {
      barrierFinished = true;
    });

    await Promise.resolve();
    expect(barrierFinished).toBe(false);

    finishSave();
    await barrier;
    expect(barrierFinished).toBe(true);
  });

  test("retains a retired editor failure until an operation reports it", async () => {
    const failure = new Error("editor save failed");
    let shouldFail = true;
    retireWorkspaceSaveParticipant(() =>
      shouldFail ? Promise.reject(failure) : Promise.resolve(),
    );

    // Let the save settle before the operation starts. Its error must still be
    // available to the barrier rather than becoming an unhandled rejection.
    await Promise.resolve();

    await expect(awaitWorkspaceSaves()).rejects.toBe(failure);

    // Retired saves deliberately remain fail-closed so a later operation can
    // retry them. Let this test's participant recover instead of leaking its
    // intentional failure into the next case through the module-global queue.
    shouldFail = false;
    await awaitWorkspaceSaves();
  });

  test("retries a failed retired editor on the next operation", async () => {
    const failure = new Error("temporary editor save failure");
    let shouldFail = true;
    let calls = 0;
    retireWorkspaceSaveParticipant(async () => {
      calls += 1;
      if (shouldFail) {
        throw failure;
      }
    });

    await expect(awaitWorkspaceSaves()).rejects.toBe(failure);
    shouldFail = false;
    await awaitWorkspaceSaves();

    expect(calls).toBe(2);
  });

  test("continues to flush mounted participants", async () => {
    let calls = 0;
    const unregister = registerWorkspaceSaveParticipant(async () => {
      calls += 1;
    });

    await awaitWorkspaceSaves();
    expect(calls).toBe(1);
    unregister();
  });

  test("awaits an already-started property transaction", async () => {
    let finishSave!: () => void;
    const save = new Promise<void>((resolve) => {
      finishSave = resolve;
    });
    trackWorkspaceSave(save);

    let barrierFinished = false;
    const barrier = awaitWorkspaceSaves().then(() => {
      barrierFinished = true;
    });
    await Promise.resolve();
    expect(barrierFinished).toBe(false);

    finishSave();
    await barrier;
    expect(barrierFinished).toBe(true);
  });

  test("reports a tracked property transaction failure", async () => {
    let failSave!: (cause: Error) => void;
    const save = new Promise<void>((_resolve, reject) => {
      failSave = reject;
    });
    trackWorkspaceSave(save);
    const failure = new Error("property save failed");
    const barrier = awaitWorkspaceSaves();

    failSave(failure);
    await expect(barrier).rejects.toBe(failure);
  });
});
