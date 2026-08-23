"use client";

import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";

import {
  cancelNotebookRun,
  closeNotebookSession,
  getNotebookRuntime,
  runNotebook,
  type NotebookCellRunResult,
  type NotebookRuntimeEvent,
  type NotebookRuntimeSnapshot,
} from "@/lib/api-notebooks";

export type NotebookRunRequest = {
  all?: boolean;
  from?: string;
  cells?: string[];
  refresh_imports?: boolean;
};

export type NotebookRuntimeState = {
  notebookId: string;
  results: Record<string, NotebookCellRunResult>;
  staleCells: Set<string>;
  autoPending: Set<string>;
  serverRunningCells: Set<string>;
  optimisticRunningCells: Set<string>;
  runBusy: boolean;
  stopping: boolean;
};

export type NotebookRuntimeModelEvent =
  | { type: "notebook_changed"; notebookId: string }
  | {
      type: "runtime_received";
      notebookId: string;
      runtime: NotebookRuntimeSnapshot | NotebookRuntimeEvent;
    }
  | { type: "run_started"; notebookId: string; targetIds: string[] }
  | {
      type: "run_results_received";
      notebookId: string;
      results: NotebookCellRunResult[];
    }
  | { type: "run_finished"; notebookId: string }
  | { type: "stop_started"; notebookId: string }
  | { type: "stop_finished"; notebookId: string }
  | { type: "session_reset"; notebookId: string; cellIds: string[] };

export function createNotebookRuntimeState(notebookId: string): NotebookRuntimeState {
  return {
    notebookId,
    results: {},
    staleCells: new Set(),
    autoPending: new Set(),
    serverRunningCells: new Set(),
    optimisticRunningCells: new Set(),
    runBusy: false,
    stopping: false,
  };
}

export function notebookRuntimeReducer(
  state: NotebookRuntimeState,
  event: NotebookRuntimeModelEvent,
): NotebookRuntimeState {
  if (event.type === "notebook_changed") {
    return createNotebookRuntimeState(event.notebookId);
  }
  if (event.notebookId !== state.notebookId) {
    return state;
  }

  switch (event.type) {
    case "runtime_received":
      return {
        ...state,
        staleCells: new Set(event.runtime.stale),
        autoPending: new Set(event.runtime.auto_pending),
        serverRunningCells: new Set(event.runtime.running),
        results: event.runtime.results
          ? { ...state.results, ...event.runtime.results }
          : state.results,
      };
    case "run_started":
      return {
        ...state,
        runBusy: true,
        optimisticRunningCells: new Set(event.targetIds),
      };
    case "run_results_received": {
      const results = { ...state.results };
      for (const result of event.results) {
        results[result.cell_id] = result;
      }
      return { ...state, results };
    }
    case "run_finished":
      return {
        ...state,
        runBusy: false,
        optimisticRunningCells: new Set(),
      };
    case "stop_started":
      return { ...state, stopping: true };
    case "stop_finished":
      return { ...state, stopping: false };
    case "session_reset":
      return {
        ...state,
        results: {},
        staleCells: new Set(event.cellIds),
      };
  }
}

export function deriveNotebookRuntime(state: NotebookRuntimeState) {
  const runningCells = new Set([...state.serverRunningCells, ...state.optimisticRunningCells]);
  const manualStaleCells = [...state.staleCells].filter((cellId) => !state.autoPending.has(cellId));
  return {
    results: state.results,
    staleCells: state.staleCells,
    autoPending: state.autoPending,
    runningCells,
    manualStaleCells,
    staleCount: manualStaleCells.length,
    runBusy: state.runBusy,
    stopping: state.stopping,
    busy: state.runBusy || state.stopping,
  };
}

export function useNotebookRuntime({
  notebookId,
  runtimeEvent,
  cellIds,
  environment,
  executionWindow,
  flushPendingSaves,
  onParameterValues,
  onError,
}: {
  notebookId: string;
  runtimeEvent: NotebookRuntimeEvent | null;
  cellIds: string[];
  environment?: string;
  executionWindow?: { start?: string; end?: string } | null;
  flushPendingSaves: () => Promise<void>;
  onParameterValues: (values: Record<string, unknown>) => void;
  onError: (message: string) => void;
}) {
  const [state, dispatch] = useReducer(
    notebookRuntimeReducer,
    createNotebookRuntimeState(notebookId),
  );
  const runtimeEventRef = useRef(runtimeEvent);
  const onParameterValuesRef = useRef(onParameterValues);
  runtimeEventRef.current = runtimeEvent;
  onParameterValuesRef.current = onParameterValues;
  const runAbortRef = useRef<{ notebookId: string; controller: AbortController } | null>(null);

  useEffect(() => {
    dispatch({ type: "notebook_changed", notebookId });
    let cancelled = false;
    const runtimeEventAtRequest = runtimeEventRef.current;
    void getNotebookRuntime(notebookId)
      .then((snapshot) => {
        if (cancelled || runtimeEventRef.current !== runtimeEventAtRequest) return;
        dispatch({ type: "runtime_received", notebookId, runtime: snapshot });
        onParameterValuesRef.current(snapshot.parameter_values);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [notebookId]);

  useEffect(() => {
    if (runtimeEvent?.notebook_id !== notebookId) return;
    dispatch({ type: "runtime_received", notebookId, runtime: runtimeEvent });
    onParameterValuesRef.current(runtimeEvent.parameter_values);
  }, [notebookId, runtimeEvent]);

  const run = useCallback(
    async (input: NotebookRunRequest, targetIds: string[]) => {
      const controller = new AbortController();
      runAbortRef.current = { notebookId, controller };
      dispatch({ type: "run_started", notebookId, targetIds });
      onError("");
      try {
        await flushPendingSaves();
        const response = await runNotebook(
          notebookId,
          {
            ...input,
            environment,
            start_date: executionWindow?.start,
            end_date: executionWindow?.end,
          },
          controller.signal,
        );
        dispatch({
          type: "run_results_received",
          notebookId,
          results: response.results,
        });
      } catch (error) {
        if (!controller.signal.aborted) onError(String(error));
      } finally {
        if (runAbortRef.current?.controller === controller) {
          runAbortRef.current = null;
        }
        dispatch({ type: "run_finished", notebookId });
      }
    },
    [
      environment,
      executionWindow?.end,
      executionWindow?.start,
      flushPendingSaves,
      notebookId,
      onError,
    ],
  );

  const cancel = useCallback(() => {
    if (state.notebookId === notebookId && state.stopping) return;
    dispatch({ type: "stop_started", notebookId });
    const activeRun = runAbortRef.current;
    if (activeRun?.notebookId === notebookId) activeRun.controller.abort();
    void cancelNotebookRun(notebookId)
      .catch((error) => onError(String(error)))
      .finally(() => dispatch({ type: "stop_finished", notebookId }));
  }, [notebookId, onError, state.notebookId, state.stopping]);

  const resetSession = useCallback(async () => {
    onError("");
    try {
      await closeNotebookSession(notebookId);
      dispatch({ type: "session_reset", notebookId, cellIds });
    } catch (error) {
      onError(String(error));
    }
  }, [cellIds, notebookId, onError]);

  const scopedState = useMemo(
    () => (state.notebookId === notebookId ? state : createNotebookRuntimeState(notebookId)),
    [notebookId, state],
  );
  return {
    ...deriveNotebookRuntime(scopedState),
    run,
    cancel,
    resetSession,
  };
}
