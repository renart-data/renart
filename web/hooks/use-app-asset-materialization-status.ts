import { useAtomValue } from "jotai";
import { useEffect, useMemo, useRef, useState } from "react";

import { useSchedulerRunEvents } from "@/hooks/use-scheduler-run-events";
import { getRun, getRuns } from "@/lib/api-scheduler";
import type { AssetStaleness } from "@/lib/api-staleness";
import { selectedEnvironmentAtom } from "@/lib/atoms/domains/workspace";
import type { PipelineRunStep } from "@/lib/types";

export type AppAssetMaterializationStatus = "unknown" | "pending" | "success" | "failed";

export type AppAssetMaterializationDisplayState = {
  status: AppAssetMaterializationStatus;
  materializedAt?: string;
  loading: boolean;
};

export type AppMaterializationAsset = {
  id: string;
  name: string;
  pipelineId?: string;
  isMaterialized?: boolean;
  staleness?: AssetStaleness;
};

type AssetStatusEntry = {
  status: Exclude<AppAssetMaterializationStatus, "unknown">;
  recordedAt: number;
  materializedAt?: string;
  runId: string;
};

type StatusByKey = Record<string, AssetStatusEntry>;

type RunContextById = Record<string, { pipelineId: string; environment: string }>;

const maxRememberedFinishedRuns = 128;

function rememberFinishedRun(cache: Set<string>, runId: string) {
  cache.delete(runId);
  cache.add(runId);
  while (cache.size > maxRememberedFinishedRuns) {
    const oldestRunId = cache.values().next().value;
    if (!oldestRunId) break;
    cache.delete(oldestRunId);
  }
}

function statusForStep(
  status: PipelineRunStep["status"],
): Exclude<AppAssetMaterializationStatus, "unknown"> {
  if (status === "success") return "success";
  if (status === "failed" || status === "cancelled") return "failed";
  return "pending";
}

function stepTimestamp(step: PipelineRunStep) {
  return new Date(step.finished_at ?? step.started_at ?? 0).getTime() || Date.now();
}

function applyStep(
  current: StatusByKey,
  step: PipelineRunStep,
  keys: string[] = [step.asset],
): StatusByKey {
  let next = current;
  for (const key of keys) {
    next = applyStepForKey(next, step, key);
  }
  return next;
}

function applyStepForKey(current: StatusByKey, step: PipelineRunStep, key: string): StatusByKey {
  const recordedAt = stepTimestamp(step);
  const existing = current[key];
  const nextStatus = statusForStep(step.status);
  if (
    existing &&
    existing.recordedAt > recordedAt &&
    !(existing.status === "pending" && nextStatus !== "pending")
  ) {
    return current;
  }
  return {
    ...current,
    [key]: {
      status: nextStatus,
      recordedAt,
      materializedAt:
        nextStatus === "success" ? (step.finished_at ?? step.started_at) : existing?.materializedAt,
      runId: step.run_id,
    },
  };
}

function matchesSelectedEnvironment(
  environment: string | undefined,
  selectedEnvironment: string | undefined,
) {
  if (!selectedEnvironment) return true;
  if (selectedEnvironment === "default") return !environment || environment === "default";
  return environment === selectedEnvironment;
}

function keysForStepAsset(
  stepAsset: string,
  assets: AppMaterializationAsset[],
  pipelineId?: string,
) {
  const shortStepName = stepAsset.split(".").pop() ?? stepAsset;
  const keys = new Set<string>();
  for (const asset of assets) {
    if (pipelineId && asset.pipelineId && asset.pipelineId !== pipelineId) continue;
    const shortAssetName = asset.name.split(".").pop() ?? asset.name;
    if (
      asset.id === stepAsset ||
      asset.name === stepAsset ||
      asset.name === shortStepName ||
      shortAssetName === stepAsset ||
      shortAssetName === shortStepName ||
      stepAsset.endsWith(`.${asset.name}`)
    ) {
      keys.add(asset.id);
      keys.add(asset.name);
      keys.add(stepAsset);
    }
  }
  return [...keys];
}

function formatAppMaterializedAt(value?: string) {
  if (!value) return "date unknown";
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value));
}

export function labelForAppMaterializationState(state?: AppAssetMaterializationDisplayState) {
  if (!state || (state.loading && state.status === "unknown")) return "checking";
  if (state.status === "pending") return "running";
  if (state.status === "success") return formatAppMaterializedAt(state.materializedAt);
  if (state.status === "failed")
    return state.materializedAt ? formatAppMaterializedAt(state.materializedAt) : "failed";
  return "no run yet";
}

export function useAppAssetMaterializationStatus(assets: AppMaterializationAsset[]) {
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const [statusByKey, setStatusByKey] = useState<StatusByKey>({});
  const runContextById = useRef<RunContextById>({});
  const finishedRunIds = useRef(new Set<string>());
  const [loading, setLoading] = useState(true);
  const pipelineIds = useMemo(
    () =>
      new Set(
        assets.map((asset) => asset.pipelineId).filter((value): value is string => Boolean(value)),
      ),
    [assets],
  );

  useEffect(() => {
    let cancelled = false;

    async function loadActiveStepStatuses() {
      setLoading(true);
      const runsResponse = await getRuns({
        limit: 20,
        environment: selectedEnvironment,
        status: "running",
      }).catch(() => null);
      if (!runsResponse || runsResponse.status !== "ok") {
        if (!cancelled) setLoading(false);
        return;
      }
      const relevantRuns = (runsResponse.runs ?? []).filter(
        (run) =>
          (pipelineIds.size === 0 || pipelineIds.has(run.pipeline_id)) &&
          matchesSelectedEnvironment(run.environment, selectedEnvironment),
      );
      const details = await Promise.all(
        relevantRuns.map((run) => getRun(run.id).catch(() => null)),
      );
      if (cancelled) return;

      const contexts: RunContextById = {};
      for (const detail of details) {
        if (!detail || detail.status !== "ok") continue;
        // A running-runs request can have captured this row immediately before
        // its finish event. Do not let the late detail resurrect pending steps.
        if (finishedRunIds.current.has(detail.run.id)) continue;
        contexts[detail.run.id] = {
          pipelineId: detail.run.pipeline_id,
          environment: detail.run.environment,
        };
        setStatusByKey((current) => {
          if (finishedRunIds.current.has(detail.run.id)) return current;
          let next = current;
          for (const step of detail.steps ?? []) {
            const keys = keysForStepAsset(step.asset, assets, detail.run.pipeline_id);
            if (keys.length > 0) next = applyStep(next, step, keys);
          }
          return next;
        });
      }
      runContextById.current = contexts;
      setLoading(false);
    }

    void loadActiveStepStatuses();
    return () => {
      cancelled = true;
    };
  }, [assets, pipelineIds, selectedEnvironment]);

  useSchedulerRunEvents((schedulerRunEvent) => {
    if (schedulerRunEvent.type === "run.unit") return;

    const eventRunId =
      schedulerRunEvent.type === "run.log" || schedulerRunEvent.type === "run.step"
        ? schedulerRunEvent.run.run_id
        : schedulerRunEvent.run.id;
    if (schedulerRunEvent.type === "run.finished") {
      rememberFinishedRun(finishedRunIds.current, eventRunId);
    } else if (finishedRunIds.current.has(eventRunId)) {
      return;
    }

    if (schedulerRunEvent.type === "run.queued" || schedulerRunEvent.type === "run.started") {
      const run = schedulerRunEvent.run;
      if (pipelineIds.size > 0 && !pipelineIds.has(run.pipeline_id)) return;
      if (!matchesSelectedEnvironment(run.environment, selectedEnvironment)) return;
      runContextById.current = {
        ...runContextById.current,
        [run.id]: { pipelineId: run.pipeline_id, environment: run.environment },
      };
      return;
    }

    if (schedulerRunEvent.type === "run.finished") {
      const run = schedulerRunEvent.run;
      setStatusByKey((current) => {
        if (!Object.values(current).some((entry) => entry.runId === run.id)) return current;
        return Object.fromEntries(
          Object.entries(current).filter(([, entry]) => entry.runId !== run.id),
        );
      });
      if (run.id in runContextById.current) {
        const next = { ...runContextById.current };
        delete next[run.id];
        runContextById.current = next;
      }
      return;
    }

    if (schedulerRunEvent.type !== "run.step") return;

    const step = schedulerRunEvent.run;
    const runContext = runContextById.current[step.run_id];
    if (runContext) {
      if (pipelineIds.size > 0 && !pipelineIds.has(runContext.pipelineId)) return;
      if (!matchesSelectedEnvironment(runContext.environment, selectedEnvironment)) return;
    }
    const keys = keysForStepAsset(step.asset, assets, runContext?.pipelineId);
    if (keys.length === 0) return;
    setStatusByKey((current) => applyStep(current, step, keys));
  });

  return useMemo(() => {
    const result: Record<string, AppAssetMaterializationDisplayState> = {};
    for (const asset of assets) {
      const entry = statusByKey[asset.id] ?? statusByKey[asset.name];
      const materializedAt =
        entry?.materializedAt ??
        asset.staleness?.last_materialized_at ??
        asset.staleness?.latest_output?.materialized_at;
      const canonicalStatus: AppAssetMaterializationStatus =
        asset.staleness?.last_run_status === "failed"
          ? "failed"
          : asset.staleness?.last_run_status === "succeeded" ||
              materializedAt ||
              asset.isMaterialized
            ? "success"
            : "unknown";
      result[asset.id] = {
        status: entry?.status ?? canonicalStatus,
        materializedAt,
        loading,
      };
    }
    return result;
  }, [assets, loading, statusByKey]);
}
