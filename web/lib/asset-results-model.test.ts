import { describe, expect, it } from "vitest";

import type { PipelineRun } from "@/lib/types";

import {
  assetResultsReducer,
  createMaterializeEntry,
  deriveAssetResults,
  initialAssetResultsState,
  terminalSchedulerRun,
  type AssetResultsState,
} from "./asset-results-model";

function entry({
  id,
  updatedAt,
  assetId = id,
  runId = `run-${id}`,
  output = "",
  status = null,
  loading = true,
}: {
  id: string;
  updatedAt: number;
  assetId?: string;
  runId?: string;
  output?: string;
  status?: "ok" | "error" | null;
  loading?: boolean;
}) {
  return createMaterializeEntry({
    id,
    kind: "asset",
    label: id,
    assetId,
    runId,
    output,
    status,
    loading,
    createdAt: updatedAt,
    updatedAt,
  });
}

describe("asset results model", () => {
  it("upserts, sorts, and selects materialization entries atomically", () => {
    const older = entry({ id: "older", updatedAt: 10 });
    const newer = entry({ id: "newer", updatedAt: 20 });
    let state = assetResultsReducer(initialAssetResultsState, {
      type: "materialize_entry_upserted",
      entry: older,
    });
    state = assetResultsReducer(state, {
      type: "materialize_entry_upserted",
      entry: newer,
    });

    expect(state).toMatchObject({
      resultTab: "materialize",
      selectedMaterializeEntryId: "newer",
    });
    expect(state.materializeHistory.map((item) => item.id)).toEqual(["newer", "older"]);

    const updatedOlder = { ...older, output: "latest", updatedAt: 30 };
    state = assetResultsReducer(state, {
      type: "materialize_entry_upserted",
      entry: updatedOlder,
    });
    expect(state.materializeHistory.map((item) => item.id)).toEqual(["older", "newer"]);
    expect(state.materializeHistory[0]?.output).toBe("latest");

    state = assetResultsReducer(state, {
      type: "materialize_entry_prepended",
      entry: entry({ id: "tutorial", updatedAt: 1 }),
    });
    expect(state.materializeHistory.map((item) => item.id)).toEqual(["tutorial", "older", "newer"]);
  });

  it("appends raw scheduler chunks and ignores events after a terminal result", () => {
    let state: AssetResultsState = {
      ...initialAssetResultsState,
      materializeHistory: [entry({ id: "current", updatedAt: 10, output: "first\n" })],
    };
    state = assetResultsReducer(state, {
      type: "scheduler_run_observed",
      event: {
        type: "run.log",
        run: { run_id: "run-current", log: { at: "2026-08-23T12:00:00Z", line: "second\n" } },
      },
      observedAt: 20,
    });
    expect(state.materializeHistory[0]).toMatchObject({
      output: "first\nsecond\n",
      loading: true,
      updatedAt: 20,
    });

    state = assetResultsReducer(state, {
      type: "terminal_run_applied",
      terminal: { runId: "run-current", status: "ok", error: "" },
      observedAt: 30,
    });
    state = assetResultsReducer(state, {
      type: "scheduler_run_observed",
      event: {
        type: "run.log",
        run: { run_id: "run-current", log: { at: "2026-08-23T12:00:01Z", line: "late\n" } },
      },
      observedAt: 40,
    });
    expect(state.materializeHistory[0]).toMatchObject({
      output: "first\nsecond\n",
      status: "ok",
      loading: false,
      updatedAt: 30,
    });
  });

  it("applies canonical terminal output without changing unrelated runs", () => {
    const state: AssetResultsState = {
      ...initialAssetResultsState,
      materializeHistory: [
        entry({ id: "target", updatedAt: 10, output: "live", runId: "run-target" }),
        entry({ id: "other", updatedAt: 10, runId: "run-other" }),
      ],
    };
    const next = assetResultsReducer(state, {
      type: "terminal_run_applied",
      terminal: {
        runId: "run-target",
        status: "error",
        error: "failed",
        output: "canonical",
      },
      observedAt: 50,
    });

    expect(next.materializeHistory[0]).toMatchObject({
      output: "canonical",
      status: "error",
      error: "failed",
      loading: false,
      updatedAt: 50,
    });
    expect(next.materializeHistory[1]).toBe(state.materializeHistory[1]);
  });

  it("removes deleted-asset history and keeps a valid selection", () => {
    const first = entry({ id: "first", assetId: "asset-a", updatedAt: 30 });
    const second = entry({ id: "second", assetId: "asset-b", updatedAt: 20 });
    const third = entry({ id: "third", assetId: "asset-c", updatedAt: 10 });
    const state: AssetResultsState = {
      resultTab: "materialize",
      selectedMaterializeEntryId: "first",
      materializeHistory: [first, second, third],
    };
    const next = assetResultsReducer(state, {
      type: "asset_results_removed",
      assetId: "asset-a",
    });

    expect(next.materializeHistory.map((item) => item.id)).toEqual(["second", "third"]);
    expect(next.selectedMaterializeEntryId).toBe("second");
  });

  it("derives the visible output tab and terminal scheduler status", () => {
    const materializeEntry = entry({ id: "entry", updatedAt: 10 });
    const state: AssetResultsState = {
      resultTab: "inspect",
      selectedMaterializeEntryId: "entry",
      materializeHistory: [materializeEntry],
    };
    expect(
      deriveAssetResults(state, {
        hasInspectData: false,
        inspectLoading: false,
        materializeLoading: false,
      }),
    ).toMatchObject({
      effectiveResultTab: "materialize",
      hasResultData: true,
      selectedMaterializeEntry: materializeEntry,
    });

    expect(
      terminalSchedulerRun({ id: "run", status: "success" } as PipelineRun, "stored log"),
    ).toEqual({ runId: "run", status: "ok", error: "", output: "stored log" });
    expect(terminalSchedulerRun({ id: "run", status: "running" } as PipelineRun)).toBeNull();
  });
});
