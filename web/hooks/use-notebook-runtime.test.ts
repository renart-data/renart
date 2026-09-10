import { describe, expect, it } from "vitest";

import type {
  NotebookCellRunResult,
  NotebookRuntimeEvent,
  NotebookRuntimeSnapshot,
} from "@/lib/api-notebooks";

import {
  createNotebookRuntimeState,
  deriveNotebookRuntime,
  notebookRuntimeReducer,
  reconcileInitialNotebookRuntime,
} from "./use-notebook-runtime";

function result(cellId: string, value: number): NotebookCellRunResult {
  return {
    cell_id: cellId,
    name: cellId,
    object_name: cellId,
    status: "ok",
    columns: ["value"],
    rows: [[value]],
    total_rows: 1,
    materialized: "view",
    duration_ms: 1,
  };
}

function runtime(overrides: Partial<NotebookRuntimeSnapshot> = {}): NotebookRuntimeSnapshot {
  return {
    auto_recompute: true,
    parameter_values: {},
    stale: [],
    auto_pending: [],
    running: [],
    results: {},
    ...overrides,
  };
}

function event(overrides: Partial<NotebookRuntimeEvent> = {}): NotebookRuntimeEvent {
  return { ...runtime(), type: "notebook.runtime", notebook_id: "notebook-a", ...overrides };
}

describe("initial notebook runtime reconciliation", () => {
  it("accepts the complete snapshot when no newer event arrived", () => {
    const snapshot = runtime({ results: { doubled: result("doubled", 20) } });
    expect(reconcileInitialNotebookRuntime("notebook-a", snapshot, null, null)).toBe(snapshot);
  });

  it("keeps initial results when a state-only SSE event wins the loading race", () => {
    const snapshot = runtime({ results: { doubled: result("doubled", 20) } });
    const latest = event({
      results: undefined,
      stale: ["base"],
      auto_pending: ["base"],
      running: ["base"],
      parameter_values: { year: 2026 },
    });
    expect(reconcileInitialNotebookRuntime("notebook-a", snapshot, null, latest)).toEqual({
      ...snapshot,
      stale: ["base"],
      auto_pending: ["base"],
      running: ["base"],
      parameter_values: { year: 2026 },
    });
  });

  it("lets newer result deltas win without dropping other snapshot results", () => {
    const snapshot = runtime({
      results: { base: result("base", 10), doubled: result("doubled", 20) },
    });
    const updated = result("base", 21);
    const latest = event({ results: { base: updated } });
    expect(reconcileInitialNotebookRuntime("notebook-a", snapshot, null, latest)?.results).toEqual({
      base: updated,
      doubled: snapshot.results.doubled,
    });
  });

  it("does not let results cached before the request replace the fresh snapshot", () => {
    const oldBase = result("base", 1);
    const oldDoubled = result("doubled", 2);
    const atRequest = event({ results: { base: oldBase, doubled: oldDoubled } });
    const snapshot = runtime({
      results: { base: result("base", 10), doubled: result("doubled", 20) },
    });
    // The SSE atom retains object identity for accumulated, untouched results.
    const latest = event({ results: { base: oldBase, doubled: result("doubled", 42) } });
    expect(
      reconcileInitialNotebookRuntime("notebook-a", snapshot, atRequest, latest)?.results,
    ).toEqual({
      base: snapshot.results.base,
      doubled: latest.results?.doubled,
    });
  });

  it("ignores events belonging to another notebook", () => {
    const snapshot = runtime({ results: { doubled: result("doubled", 20) } });
    expect(
      reconcileInitialNotebookRuntime(
        "notebook-a",
        snapshot,
        null,
        event({ notebook_id: "notebook-b" }),
      ),
    ).toBe(snapshot);
  });
});

describe("notebook runtime model", () => {
  it("reconciles a connection snapshot without resetting output or a pending manual run", () => {
    let state = createNotebookRuntimeState("notebook-a");
    state = notebookRuntimeReducer(state, {
      type: "run_results_received",
      notebookId: "notebook-a",
      results: [result("base", 10)],
    });
    state = notebookRuntimeReducer(state, {
      type: "run_started",
      notebookId: "notebook-a",
      targetIds: ["manual"],
    });
    state = notebookRuntimeReducer(state, {
      type: "runtime_received",
      notebookId: "notebook-a",
      runtime: runtime({ results: { doubled: result("doubled", 20) } }),
    });
    expect(state.results).toEqual({ base: result("base", 10), doubled: result("doubled", 20) });
    expect(state.runBusy).toBe(true);
    expect([...state.optimisticRunningCells]).toEqual(["manual"]);
  });

  it("replaces authoritative runtime sets while merging result deltas", () => {
    let state = createNotebookRuntimeState("notebook-a");
    state = notebookRuntimeReducer(state, {
      type: "runtime_received",
      notebookId: "notebook-a",
      runtime: runtime({
        stale: ["cell-a", "cell-b"],
        auto_pending: ["cell-b"],
        running: ["cell-a"],
        results: { "cell-a": result("cell-a", 1) },
      }),
    });
    state = notebookRuntimeReducer(state, {
      type: "runtime_received",
      notebookId: "notebook-a",
      runtime: runtime({ results: { "cell-b": result("cell-b", 2) } }),
    });

    expect([...state.staleCells]).toEqual([]);
    expect([...state.autoPending]).toEqual([]);
    expect([...state.serverRunningCells]).toEqual([]);
    expect(Object.keys(state.results).sort()).toEqual(["cell-a", "cell-b"]);
  });

  it("does not erase newer server running state when a request finishes", () => {
    let state = createNotebookRuntimeState("notebook-a");
    state = notebookRuntimeReducer(state, {
      type: "run_started",
      notebookId: "notebook-a",
      targetIds: ["cell-a"],
    });
    state = notebookRuntimeReducer(state, {
      type: "runtime_received",
      notebookId: "notebook-a",
      runtime: runtime({ running: ["cell-a", "cell-b"] }),
    });
    state = notebookRuntimeReducer(state, {
      type: "run_finished",
      notebookId: "notebook-a",
    });

    const derived = deriveNotebookRuntime(state);
    expect(derived.runBusy).toBe(false);
    expect([...derived.runningCells].sort()).toEqual(["cell-a", "cell-b"]);
  });

  it("ignores late run responses after navigating to another notebook", () => {
    let state = createNotebookRuntimeState("notebook-a");
    state = notebookRuntimeReducer(state, {
      type: "notebook_changed",
      notebookId: "notebook-b",
    });
    const afterLateResult = notebookRuntimeReducer(state, {
      type: "run_results_received",
      notebookId: "notebook-a",
      results: [result("old-cell", 1)],
    });
    const afterLateFinish = notebookRuntimeReducer(afterLateResult, {
      type: "run_finished",
      notebookId: "notebook-a",
    });

    expect(afterLateFinish).toBe(state);
    expect(afterLateFinish.results).toEqual({});
  });

  it("derives manual stale cells independently from automatic recompute", () => {
    let state = createNotebookRuntimeState("notebook-a");
    state = notebookRuntimeReducer(state, {
      type: "runtime_received",
      notebookId: "notebook-a",
      runtime: runtime({
        stale: ["manual", "automatic"],
        auto_pending: ["automatic"],
      }),
    });

    expect(deriveNotebookRuntime(state).manualStaleCells).toEqual(["manual"]);
  });

  it("clears session results and marks every current cell stale", () => {
    let state = createNotebookRuntimeState("notebook-a");
    state = notebookRuntimeReducer(state, {
      type: "run_results_received",
      notebookId: "notebook-a",
      results: [result("cell-a", 1)],
    });
    state = notebookRuntimeReducer(state, {
      type: "session_reset",
      notebookId: "notebook-a",
      cellIds: ["cell-a", "cell-b"],
    });

    expect(state.results).toEqual({});
    expect([...state.staleCells]).toEqual(["cell-a", "cell-b"]);
  });
});
