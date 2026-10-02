import { useAtomValue, useSetAtom } from "jotai";
import { useCallback, useEffect, useState } from "react";

import { useSchedulerRunEvents } from "@/hooks/use-scheduler-run-events";
import {
  archiveEnvSchedule,
  getEnvSchedules,
  setEnvScheduleStatus,
  upsertEnvSchedule,
  type EnvSchedule,
  type SchedulerOwnership,
  type UpsertEnvScheduleInput,
} from "@/lib/api-env-schedules";
import { scheduleOccurrenceEventAtom } from "@/lib/atoms/domains/results";
import { markGettingStartedAtom } from "@/lib/getting-started";

export function envScheduleKey(schedule: Pick<EnvSchedule, "pipeline_uuid" | "environment">) {
  return `${schedule.pipeline_uuid}::${schedule.environment}`;
}

// useEnvSchedules manages the per-(pipeline, environment) schedule rows.
export function useEnvSchedules() {
  const occurrenceEvent = useAtomValue(scheduleOccurrenceEventAtom);
  const [schedules, setSchedules] = useState<EnvSchedule[]>([]);
  const [archived, setArchived] = useState<EnvSchedule[]>([]);
  const [ownership, setOwnership] = useState<SchedulerOwnership | null>(null);
  const [loading, setLoading] = useState(true);
  const [busyKey, setBusyKey] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    try {
      const response = await getEnvSchedules();
      setSchedules(response.schedules ?? []);
      setArchived(response.archived ?? []);
      setOwnership(
        response.scheduler ?? {
          state: "unavailable",
          message: "The server did not report scheduler ownership.",
        },
      );
    } catch (cause) {
      // Keep the last readable rows but fail closed for every mutation.
      setOwnership({
        state: "unavailable",
        message:
          cause instanceof Error ? cause.message : "Scheduler ownership could not be loaded.",
      });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  // Run lifecycle events update last-run displays.
  useSchedulerRunEvents((runEvent) => {
    if (
      runEvent.type === "run.queued" ||
      runEvent.type === "run.finished" ||
      runEvent.type === "run.started"
    ) {
      void refresh();
    }
  });

  useEffect(() => {
    if (occurrenceEvent?.type === "schedule.occurrence") {
      void refresh();
    }
  }, [occurrenceEvent, refresh]);

  const markGettingStarted = useSetAtom(markGettingStartedAtom);
  useEffect(() => {
    if (schedules.length > 0) markGettingStarted({ id: "schedule" });
  }, [markGettingStarted, schedules.length]);

  const canMutate = ownership?.state === "owner";
  const ownershipReason =
    ownership?.message ??
    (ownership?.state === "follower"
      ? "Schedules are managed by another Renart process."
      : "Scheduler ownership has not been established.");

  const withBusy = useCallback(
    async (key: string, action: () => Promise<unknown>) => {
      if (!canMutate) {
        throw new Error(ownershipReason);
      }
      setBusyKey(key);
      try {
        await action();
        await refresh();
      } finally {
        setBusyKey(null);
      }
    },
    [canMutate, ownershipReason, refresh],
  );

  const upsert = useCallback(
    async (
      schedule: Pick<EnvSchedule, "pipeline_uuid" | "environment"> & { pipeline_id: string },
      input: UpsertEnvScheduleInput,
    ) => {
      await withBusy(envScheduleKey(schedule), () =>
        upsertEnvSchedule(schedule.pipeline_id, schedule.environment, input),
      );
    },
    [withBusy],
  );

  const setStatus = useCallback(
    async (schedule: EnvSchedule, status: "active" | "paused") => {
      if (!schedule.pipeline_id) return;
      await withBusy(envScheduleKey(schedule), () =>
        setEnvScheduleStatus(schedule.pipeline_id as string, schedule.environment, status),
      );
    },
    [withBusy],
  );

  const archive = useCallback(
    async (schedule: EnvSchedule) => {
      if (!schedule.pipeline_id) return;
      await withBusy(envScheduleKey(schedule), () =>
        archiveEnvSchedule(schedule.pipeline_id as string, schedule.environment),
      );
    },
    [withBusy],
  );

  return {
    schedules,
    archived,
    ownership,
    canMutate,
    ownershipReason,
    loading,
    busyKey,
    refresh,
    upsert,
    setStatus,
    archive,
  };
}
