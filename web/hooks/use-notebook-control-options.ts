import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";
import { refreshNotebookControlOptions, type NotebookRuntimeEvent } from "@/lib/api-notebooks";
import type { NotebookParameter, PresentationDatasetResult } from "@/lib/generated/api-types";
import type { WebAsset, WebNotebook } from "@/lib/types";

type OptionSnapshot = { signature: string; result: PresentationDatasetResult; refreshedAt: number };
export type NotebookControlOptionsState = {
  notebookId: string;
  snapshots: Record<string, OptionSnapshot>;
  requests: Record<string, number>;
  error: string | null;
};
type OptionsEvent =
  | { type: "opened"; notebookId: string }
  | { type: "started"; notebookId: string; controlId: string; token: number; silent: boolean }
  | {
      type: "received";
      notebookId: string;
      controlId: string;
      token: number;
      snapshot: OptionSnapshot;
    }
  | {
      type: "failed";
      notebookId: string;
      controlId: string;
      token: number;
      message: string;
      silent: boolean;
    };

export function createNotebookControlOptionsState(notebookId: string): NotebookControlOptionsState {
  return { notebookId, snapshots: {}, requests: {}, error: null };
}

export function notebookControlOptionsReducer(
  state: NotebookControlOptionsState,
  event: OptionsEvent,
): NotebookControlOptionsState {
  if (event.type === "opened") return createNotebookControlOptionsState(event.notebookId);
  if (event.notebookId !== state.notebookId) return state;
  if (event.type === "started") {
    return {
      ...state,
      requests: { ...state.requests, [event.controlId]: event.token },
      error: event.silent ? state.error : null,
    };
  }
  if (state.requests[event.controlId] !== event.token) return state;
  const requests = { ...state.requests };
  delete requests[event.controlId];
  return event.type === "received"
    ? { ...state, requests, snapshots: { ...state.snapshots, [event.controlId]: event.snapshot } }
    : { ...state, requests, error: event.silent ? state.error : event.message };
}

export function notebookControlOptionSignature(control: NotebookParameter): string {
  return JSON.stringify({
    type: control.type,
    dataset: control.options?.dataset?.trim() ?? "",
    valueField: control.options?.value_field?.trim() ?? "",
    labelField: control.options?.label_field?.trim() ?? "",
  });
}

export function notebookControlProducer(
  control: NotebookParameter,
  cells: WebAsset[],
): WebAsset | undefined {
  const dataset = control.options?.dataset?.trim();
  if (!dataset) return undefined;
  return (
    cells.find((cell) => cell.cell_id === dataset) ??
    cells.find((cell) => cell.name.toLowerCase() === dataset.toLowerCase())
  );
}

// Runtime events are merged deltas: unchanged result references are not new
// executions. Opening a notebook or receiving status alone never runs a query.
export function controlsWithNewResults(
  notebookId: string,
  controls: NotebookParameter[],
  cells: WebAsset[],
  previous: NotebookRuntimeEvent | null,
  incoming: NotebookRuntimeEvent | null,
): NotebookParameter[] {
  if (!incoming || incoming.notebook_id !== notebookId) return [];
  const changed = new Set(
    Object.entries(incoming.results ?? {})
      .filter(([id, result]) => result.status === "ok" && previous?.results?.[id] !== result)
      .map(([id]) => id),
  );
  return controls.filter((control) => {
    const producer = notebookControlProducer(control, cells);
    return Boolean(producer?.cell_id && changed.has(producer.cell_id));
  });
}

// Owns only the option-query projection. Authored definitions, parameter
// values, notebook results, and navigation retain their existing authorities.
export function useNotebookControlOptions({
  notebookId,
  notebook,
  runtimeEvent,
  onError,
}: {
  notebookId: string;
  notebook: WebNotebook | null;
  runtimeEvent: NotebookRuntimeEvent | null;
  onError: (message: string) => void;
}) {
  const [state, dispatch] = useReducer(
    notebookControlOptionsReducer,
    notebookId,
    createNotebookControlOptionsState,
  );
  const requestSequence = useRef(0);
  const previousEvent = useRef(runtimeEvent);
  const latestEvent = useRef(runtimeEvent);
  latestEvent.current = runtimeEvent;
  const current = useMemo(
    () => (state.notebookId === notebookId ? state : createNotebookControlOptionsState(notebookId)),
    [state, notebookId],
  );

  useEffect(() => {
    dispatch({ type: "opened", notebookId });
    previousEvent.current = latestEvent.current;
  }, [notebookId]);

  useEffect(() => {
    if (current.error !== null) onError(current.error);
  }, [current.error, onError]);

  const refreshControlOptions = useCallback(
    async (control: NotebookParameter, options: { silent?: boolean } = {}) => {
      if (!control.options?.dataset?.trim() || !control.options.value_field?.trim()) return;
      const token = ++requestSequence.current;
      const silent = options.silent ?? false;
      dispatch({ type: "started", notebookId, controlId: control.id, token, silent });
      if (!silent) onError("");
      try {
        const result = await refreshNotebookControlOptions(notebookId, control.id);
        dispatch({
          type: "received",
          notebookId,
          controlId: control.id,
          token,
          snapshot: {
            signature: notebookControlOptionSignature(control),
            result,
            refreshedAt: Date.now(),
          },
        });
      } catch (error) {
        dispatch({
          type: "failed",
          notebookId,
          controlId: control.id,
          token,
          message: String(error),
          silent,
        });
      }
    },
    [notebookId, onError],
  );

  useEffect(() => {
    const previous = previousEvent.current;
    previousEvent.current = runtimeEvent;
    for (const control of controlsWithNewResults(
      notebookId,
      notebook?.parameters ?? [],
      notebook?.cells ?? [],
      previous,
      runtimeEvent,
    )) {
      void refreshControlOptions(control, { silent: true });
    }
  }, [notebook, notebookId, runtimeEvent, refreshControlOptions]);

  const loadingControlOptions = useMemo(
    () => new Set(Object.keys(current.requests)),
    [current.requests],
  );
  return {
    controlOptionSnapshots: current.snapshots,
    loadingControlOptions,
    refreshControlOptions,
  };
}
