import { atom } from "jotai";

import {
  initialAssetResultsState,
  type AssetResultsState,
  type SchedulerRunEvent,
} from "@/lib/asset-results-model";
import type { StalenessUpdatedEvent } from "@/lib/api-staleness";
import type { NotebookAgentSnapshot, NotebookRuntimeEvent } from "@/lib/api-notebooks";

export type {
  AssetResultTab,
  AssetResultsState,
  MaterializeHistoryEntry,
  SchedulerRunEvent,
} from "@/lib/asset-results-model";

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

export const assetResultsAtom = atom<AssetResultsState>(initialAssetResultsState);

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
