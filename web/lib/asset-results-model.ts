import type {
  PipelineRun,
  PipelineRunLogLine,
  PipelineRunStep,
  PipelineRunUnit,
} from "@/lib/types";

export type AssetResultTab = "inspect" | "materialize";

export type MaterializeHistoryEntry = {
  id: string;
  kind: "asset" | "pipeline" | "batch";
  label: string;
  assetId?: string | null;
  assetName?: string | null;
  pipelineId?: string | null;
  pipelineName?: string | null;
  runId?: string | null;
  output: string;
  status: "ok" | "error" | null;
  error: string;
  warnings?: string[];
  loading: boolean;
  createdAt: number;
  updatedAt: number;
  timeWindow?: { start: string; end: string } | null;
};

export type AssetResultsState = {
  resultTab: AssetResultTab;
  selectedMaterializeEntryId: string | null;
  materializeHistory: MaterializeHistoryEntry[];
};

export type SchedulerRunEvent =
  | {
      type: "run.queued" | "run.started" | "run.finished" | "run.cancellation_requested";
      run: PipelineRun;
    }
  | { type: "run.log"; run: { run_id: string; log: PipelineRunLogLine } }
  | { type: "run.step"; run: PipelineRunStep }
  | { type: "run.unit"; run: { run_id: string; unit: PipelineRunUnit } };

export type TerminalSchedulerRun = {
  runId: string;
  status: "ok" | "error";
  error: string;
  output?: string;
};

export type AssetResultsEvent =
  | { type: "result_tab_selected"; tab: AssetResultTab }
  | { type: "materialize_entry_selected"; entryId: string }
  | { type: "materialize_entry_upserted"; entry: MaterializeHistoryEntry }
  | { type: "materialize_entry_prepended"; entry: MaterializeHistoryEntry }
  | { type: "terminal_run_applied"; terminal: TerminalSchedulerRun; observedAt: number }
  | { type: "scheduler_run_observed"; event: SchedulerRunEvent; observedAt: number }
  | { type: "asset_results_removed"; assetId: string | null };

export const initialAssetResultsState: AssetResultsState = {
  resultTab: "inspect",
  selectedMaterializeEntryId: null,
  materializeHistory: [],
};

export function assetResultsReducer(
  state: AssetResultsState,
  event: AssetResultsEvent,
): AssetResultsState {
  switch (event.type) {
    case "result_tab_selected":
      return { ...state, resultTab: event.tab };
    case "materialize_entry_selected":
      return state.materializeHistory.some((entry) => entry.id === event.entryId)
        ? { ...state, resultTab: "materialize", selectedMaterializeEntryId: event.entryId }
        : state;
    case "materialize_entry_upserted": {
      const nextHistory = [
        event.entry,
        ...state.materializeHistory.filter((entry) => entry.id !== event.entry.id),
      ].sort((left, right) => right.updatedAt - left.updatedAt);
      return {
        ...state,
        resultTab: "materialize",
        selectedMaterializeEntryId: event.entry.id,
        materializeHistory: nextHistory,
      };
    }
    case "materialize_entry_prepended":
      return {
        ...state,
        resultTab: "materialize",
        selectedMaterializeEntryId: event.entry.id,
        materializeHistory: [
          event.entry,
          ...state.materializeHistory.filter((entry) => entry.id !== event.entry.id),
        ],
      };
    case "terminal_run_applied": {
      let matched = false;
      const materializeHistory = state.materializeHistory.map((entry) => {
        if (entry.runId !== event.terminal.runId) return entry;
        matched = true;
        return {
          ...entry,
          ...(event.terminal.output === undefined ? {} : { output: event.terminal.output }),
          status: event.terminal.status,
          error: event.terminal.error,
          loading: false,
          updatedAt: event.observedAt,
        };
      });
      return matched ? { ...state, materializeHistory } : state;
    }
    case "scheduler_run_observed":
      return reduceSchedulerRunEvent(state, event.event, event.observedAt);
    case "asset_results_removed": {
      const materializeHistory = state.materializeHistory.filter(
        (entry) => entry.assetId !== event.assetId,
      );
      const selectionStillExists = materializeHistory.some(
        (entry) => entry.id === state.selectedMaterializeEntryId,
      );
      return {
        ...state,
        materializeHistory,
        selectedMaterializeEntryId: selectionStillExists
          ? state.selectedMaterializeEntryId
          : (materializeHistory[0]?.id ?? null),
      };
    }
  }
}

export function createMaterializeEntry(input: {
  id: string;
  kind: "asset" | "pipeline" | "batch";
  label: string;
  assetId?: string | null;
  assetName?: string | null;
  pipelineId?: string | null;
  pipelineName?: string | null;
  runId?: string | null;
  output?: string;
  status?: "ok" | "error" | null;
  error?: string;
  loading?: boolean;
  createdAt: number;
  updatedAt?: number;
  timeWindow?: { start: string; end: string } | null;
}): MaterializeHistoryEntry {
  return {
    id: input.id,
    kind: input.kind,
    label: input.label,
    assetId: input.assetId,
    assetName: input.assetName,
    pipelineId: input.pipelineId,
    pipelineName: input.pipelineName,
    runId: input.runId,
    output: input.output ?? "",
    status: input.status ?? null,
    error: input.error ?? "",
    loading: input.loading ?? false,
    createdAt: input.createdAt,
    updatedAt: input.updatedAt ?? input.createdAt,
    timeWindow: input.timeWindow ?? null,
  };
}

export function terminalSchedulerRun(
  run: PipelineRun,
  output?: string,
): TerminalSchedulerRun | null {
  if (run.status !== "success" && run.status !== "failed" && run.status !== "cancelled") {
    return null;
  }
  return {
    runId: run.id,
    status: run.status === "success" ? "ok" : "error",
    error: run.error ?? "",
    output,
  };
}

export function deriveAssetResults(
  state: AssetResultsState,
  {
    hasInspectData,
    inspectLoading,
    materializeLoading,
  }: { hasInspectData: boolean; inspectLoading: boolean; materializeLoading: boolean },
) {
  const selectedMaterializeEntry =
    state.materializeHistory.find((entry) => entry.id === state.selectedMaterializeEntryId) ?? null;
  return {
    selectedMaterializeEntry,
    hasResultData:
      hasInspectData ||
      state.resultTab === "materialize" ||
      state.materializeHistory.length > 0 ||
      inspectLoading ||
      materializeLoading,
    effectiveResultTab:
      state.resultTab === "inspect" && !hasInspectData && state.materializeHistory.length > 0
        ? ("materialize" as const)
        : state.resultTab,
  };
}

function reduceSchedulerRunEvent(
  state: AssetResultsState,
  event: SchedulerRunEvent,
  observedAt: number,
): AssetResultsState {
  if (event.type === "run.step" || event.type === "run.unit" || event.type === "run.finished") {
    return state;
  }

  let matched = false;
  const materializeHistory = state.materializeHistory.map((entry) => {
    if (event.type === "run.log") {
      if (entry.runId !== event.run.run_id || (entry.status !== null && !entry.loading)) {
        return entry;
      }
      matched = true;
      return {
        ...entry,
        // Log events are raw stream chunks and already include line endings.
        output: entry.output + event.run.log.line,
        loading: true,
        updatedAt: observedAt,
      };
    }

    if (entry.runId !== event.run.id || (entry.status !== null && !entry.loading)) {
      return entry;
    }
    matched = true;
    return { ...entry, loading: true, updatedAt: observedAt };
  });

  return matched ? { ...state, materializeHistory } : state;
}
