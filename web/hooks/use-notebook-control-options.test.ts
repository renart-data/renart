import { describe, expect, it } from "vitest";
import type { NotebookRuntimeEvent, NotebookCellRunResult } from "@/lib/api-notebooks";
import type { NotebookParameter, PresentationDatasetResult } from "@/lib/generated/api-types";
import type { WebAsset } from "@/lib/types";
import {
  controlsWithNewResults,
  createNotebookControlOptionsState,
  notebookControlOptionsReducer as reduce,
  notebookControlOptionSignature,
  notebookControlProducer,
} from "./use-notebook-control-options";

const control = {
  id: "region",
  type: "select",
  options: { dataset: "source", value_field: "region" },
} as NotebookParameter;
const cells = [
  { cell_id: "source", name: "regions" },
  { cell_id: "other", name: "source" },
] as WebAsset[];
const snapshot = {
  signature: notebookControlOptionSignature(control),
  result: { rows: [["EU"]] } as PresentationDatasetResult,
  refreshedAt: 1,
};
const start = (token: number, notebookId = "a") => ({
  type: "started" as const,
  notebookId,
  controlId: "region",
  token,
  silent: false,
});
const received = (token: number, notebookId = "a") => ({
  type: "received" as const,
  notebookId,
  controlId: "region",
  token,
  snapshot,
});
const result = { status: "ok" } as NotebookCellRunResult;
const event = (results: Record<string, NotebookCellRunResult>, notebookId = "a") =>
  ({ notebook_id: notebookId, results }) as NotebookRuntimeEvent;

describe("notebook control option projection", () => {
  it("only accepts the newest response and keeps a newer request loading", () => {
    let state = reduce(createNotebookControlOptionsState("a"), start(1));
    state = reduce(state, start(2));
    expect(reduce(state, received(1))).toBe(state);
    state = reduce(state, received(2));
    expect(state.snapshots.region).toBe(snapshot);
    expect(state.requests).toEqual({});
    expect(reduce(state, received(1))).toBe(state);
  });

  it("invalidates old completions even after leaving and returning to the same notebook", () => {
    let state = reduce(createNotebookControlOptionsState("a"), start(1));
    state = reduce(state, { type: "opened", notebookId: "b" });
    expect(reduce(state, received(1))).toBe(state);
    state = reduce(state, { type: "opened", notebookId: "a" });
    expect(reduce(state, received(1))).toBe(state);
    expect(state.snapshots).toEqual({});
  });

  it("ignores superseded errors and releases loading for a current failure", () => {
    const state = reduce(reduce(createNotebookControlOptionsState("a"), start(1)), start(2));
    const failure = { ...start(1), type: "failed" as const, message: "failed" };
    expect(reduce(state, failure)).toBe(state);
    expect(reduce(state, { ...failure, token: 2 })).toMatchObject({
      error: "failed",
      requests: {},
    });
    expect(reduce(state, { ...failure, token: 2, silent: true })).toMatchObject({
      error: null,
      requests: {},
    });
  });

  it("resolves producer IDs before names and trims/case-folds names", () => {
    expect(notebookControlProducer(control, cells)).toBe(cells[0]);
    expect(notebookControlProducer({ ...control, options: { dataset: " REGIONS " } }, cells)).toBe(
      cells[0],
    );
    expect(notebookControlProducer({ ...control, options: undefined }, cells)).toBeUndefined();
  });

  it("invalidates a snapshot when its option definition changes, not whitespace", () => {
    expect(
      notebookControlOptionSignature({
        ...control,
        options: { dataset: " source ", value_field: " region " },
      }),
    ).toBe(snapshot.signature);
    expect(
      notebookControlOptionSignature({
        ...control,
        options: { ...control.options, value_field: "country" },
      }),
    ).not.toBe(snapshot.signature);
  });

  it("does not query for initial, state-only, failed or foreign runtime updates", () => {
    const baseline = event({ source: result });
    expect(controlsWithNewResults("a", [control], cells, baseline, baseline)).toEqual([]);
    expect(
      controlsWithNewResults("a", [control], cells, baseline, event({ source: result })),
    ).toEqual([]);
    expect(controlsWithNewResults("a", [control], cells, baseline, event({}))).toEqual([]);
    expect(
      controlsWithNewResults(
        "a",
        [control],
        cells,
        baseline,
        event({ source: { ...result, status: "error" } }),
      ),
    ).toEqual([]);
    expect(
      controlsWithNewResults(
        "a",
        [control],
        cells,
        baseline,
        event({ source: { ...result } }, "b"),
      ),
    ).toEqual([]);
  });

  it("refreshes only controls whose producer has a new successful result", () => {
    const baseline = event({ source: result });
    const unrelated = {
      ...control,
      id: "other",
      options: { dataset: "missing", value_field: "region" },
    };
    expect(
      controlsWithNewResults(
        "a",
        [control, unrelated],
        cells,
        baseline,
        event({ source: { ...result } }),
      ),
    ).toEqual([control]);
  });
});
