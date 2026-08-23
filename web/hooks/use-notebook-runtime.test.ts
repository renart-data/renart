import { describe, expect, it } from "vitest";

import type { NotebookCellRunResult, NotebookRuntimeSnapshot } from "@/lib/api-notebooks";

import {
  createNotebookRuntimeState,
  deriveNotebookRuntime,
  notebookRuntimeReducer,
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

describe("notebook runtime model", () => {
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
