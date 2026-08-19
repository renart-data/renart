import { atom } from "jotai";

import type { StalenessUpdatedEvent } from "@/lib/api-staleness";
import type { NotebookAgentSnapshot, NotebookRuntimeEvent } from "@/lib/api-notebooks";
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

export type SchedulerRunEvent =
  | {
      type: "run.queued" | "run.started" | "run.finished" | "run.cancellation_requested";
      run: PipelineRun;
    }
  | { type: "run.log"; run: { run_id: string; log: PipelineRunLogLine } }
  | { type: "run.step"; run: PipelineRunStep }
  | { type: "run.unit"; run: { run_id: string; unit: PipelineRunUnit } };

export type SequencedSchedulerRunEvent = {
  sequence: number;
  event: SchedulerRunEvent;
};

export type SchedulerRunEventBuffer = {
  sequence: number;
  events: SequencedSchedulerRunEvent[];
};

export type ScheduleOccurrenceEvent = {
  type: "schedule.occurrence";
  pipeline_uuid: string;
  environment: string;
};

export type AssetResultsState = {
  resultTab: AssetResultTab;
  selectedMaterializeEntryId: string | null;
  materializeHistory: MaterializeHistoryEntry[];
};

export const assetResultsAtom = atom<AssetResultsState>({
  resultTab: "inspect",
  selectedMaterializeEntryId: null,
  materializeHistory: [],
});

export const changedAssetIdsAtom = atom<Set<string>>(new Set<string>());

const maxBufferedSchedulerRunEvents = 2048;

export function appendSchedulerRunEvent(
  current: SchedulerRunEventBuffer,
  event: SchedulerRunEvent,
): SchedulerRunEventBuffer {
  const sequence = current.sequence + 1;
  return {
    sequence,
    events: [...current.events, { sequence, event }].slice(-maxBufferedSchedulerRunEvents),
  };
}

export const schedulerRunEventsAtom = atom<SchedulerRunEventBuffer>({
  sequence: 0,
  events: [],
});

export const appendSchedulerRunEventAtom = atom(null, (get, set, event: SchedulerRunEvent) => {
  set(schedulerRunEventsAtom, appendSchedulerRunEvent(get(schedulerRunEventsAtom), event));
});

export const scheduleOccurrenceEventAtom = atom<ScheduleOccurrenceEvent | null>(null);

export const stalenessEventAtom = atom<StalenessUpdatedEvent | null>(null);

export type NotebookRuntimeEvents = Record<string, NotebookRuntimeEvent>;

export function mergeNotebookRuntimeEvent(
  current: NotebookRuntimeEvents,
  event: NotebookRuntimeEvent,
): NotebookRuntimeEvents {
  const previous = current[event.notebook_id];
  return {
    ...current,
    [event.notebook_id]: {
      ...event,
      // Runtime events carry result deltas. Keep the accumulated snapshot so a
      // following state-only event cannot erase a result before React observes
      // it (several recompute events can arrive in one browser task).
      results: {
        ...(previous?.results ?? {}),
        ...(event.results ?? {}),
      },
    },
  };
}

export const notebookRuntimeEventsAtom = atom<NotebookRuntimeEvents>({});

export type NotebookAgentEvents = Record<string, NotebookAgentSnapshot>;

export function mergeNotebookAgentEvent(
  current: NotebookAgentEvents,
  event: NotebookAgentSnapshot,
): NotebookAgentEvents {
  const previous = current[event.notebook_id];
  if (previous && previous.revision > event.revision) {
    return current;
  }
  return { ...current, [event.notebook_id]: event };
}

export const notebookAgentEventsAtom = atom<NotebookAgentEvents>({});
