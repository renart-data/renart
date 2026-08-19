import { describe, expect, it } from "vitest";

import type { NotebookCellRunResult, NotebookRuntimeEvent } from "@/lib/api-notebooks";
import {
  appendSchedulerRunEvent,
  mergeNotebookRuntimeEvent,
  type SchedulerRunEvent,
  type SchedulerRunEventBuffer,
} from "@/lib/atoms/domains/results";

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

function runtimeEvent(overrides: Partial<NotebookRuntimeEvent> = {}): NotebookRuntimeEvent {
  return {
    type: "notebook.runtime",
    notebook_id: "notebook-1",
    auto_recompute: true,
    parameter_values: {},
    stale: [],
    auto_pending: [],
    running: [],
    ...overrides,
  };
}

describe("mergeNotebookRuntimeEvent", () => {
  it("retains result deltas when a state-only event follows in the same update batch", () => {
    const withResult = mergeNotebookRuntimeEvent(
      {},
      runtimeEvent({ results: { cell_a: result("cell_a", 222) } }),
    );
    const settled = mergeNotebookRuntimeEvent(withResult, runtimeEvent());

    expect(settled["notebook-1"]?.results?.cell_a.rows).toEqual([[222]]);
  });

  it("replaces a cell result with its newest delta", () => {
    const first = mergeNotebookRuntimeEvent(
      {},
      runtimeEvent({ results: { cell_a: result("cell_a", 111) } }),
    );
    const second = mergeNotebookRuntimeEvent(
      first,
      runtimeEvent({ results: { cell_a: result("cell_a", 222) } }),
    );

    expect(second["notebook-1"]?.results?.cell_a.rows).toEqual([[222]]);
  });
});

describe("appendSchedulerRunEvent", () => {
  it("retains adjacent unit, step, and log events in order", () => {
    const events: SchedulerRunEvent[] = [
      {
        type: "run.unit",
        run: {
          run_id: "run-1",
          unit: {
            position: 0,
            asset_id: "asset-1",
            asset_name: "analytics.orders",
            start_date: "2026-07-27T00:00:00Z",
            end_date: "2026-07-28T00:00:00Z",
            render_index: 0,
            reason: "selected",
            status: "running",
          },
        },
      },
      {
        type: "run.step",
        run: {
          run_id: "run-1",
          asset: "analytics.orders",
          status: "running",
        },
      },
      {
        type: "run.log",
        run: {
          run_id: "run-1",
          log: { at: "2026-07-27T20:00:00Z", line: "Running analytics.orders\n" },
        },
      },
    ];
    const initial: SchedulerRunEventBuffer = { sequence: 0, events: [] };
    const buffered = events.reduce(appendSchedulerRunEvent, initial);

    expect(buffered.sequence).toBe(3);
    expect(buffered.events.map((entry) => entry.sequence)).toEqual([1, 2, 3]);
    expect(buffered.events.map((entry) => entry.event.type)).toEqual([
      "run.unit",
      "run.step",
      "run.log",
    ]);
  });
});
