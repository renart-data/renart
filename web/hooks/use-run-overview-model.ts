import { useAtomValue } from "jotai";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { useEnvSchedules } from "@/hooks/use-env-schedules";
import { usePipelineRuns } from "@/hooks/use-pipeline-runs";
import { getDeployStatus, type DeployStatus } from "@/lib/api-deploy";
import type { EnvSchedule } from "@/lib/api-env-schedules";
import { selectedEnvironmentAtom, workspaceAtom } from "@/lib/atoms/domains/workspace";
import {
  scheduleExpectedSlots,
  scheduleTimelineAxis,
  scheduleTimelineWindow,
  type ScheduleTimelineBucket,
  type ScheduleTimelineDensity,
  type ScheduleTimelineSlot,
} from "@/lib/schedule-timeline-model";
import type { PipelineRun, WebPipeline } from "@/lib/types";

export type RunOverviewHealth = "healthy" | "running" | "waiting" | "failed" | "idle";

export type RunOverviewPipeline = {
  id: string;
  name: string;
  health: RunOverviewHealth;
  healthLabel: string;
  latestRun?: PipelineRun;
  scheduleCount: number;
  deployment?: DeployStatus;
};

export type RunOverviewProjection = ScheduleTimelineSlot & {
  schedule: EnvSchedule;
};

export type RunOverviewTimelineRow = {
  pipeline: WebPipeline;
  cadence: string;
  schedules: EnvSchedule[];
  projections: RunOverviewProjection[];
  runs: PipelineRun[];
};

export type RunOverviewIssue = {
  id: string;
  tone: "warning" | "destructive" | "info";
  title: string;
  detail: string;
  runId?: string;
  pipelineId?: string;
};

export type RunOverviewReadout = {
  label: string;
  value: string;
  detail: string;
};

export type RunOverviewModel = {
  environment: string;
  pipelines: RunOverviewPipeline[];
  selectedPipelineId?: string;
  selectedPipeline?: WebPipeline;
  timelineWindow: ReturnType<typeof scheduleTimelineWindow>;
  timelineAxis: ReturnType<typeof scheduleTimelineAxis>;
  timelineRows: RunOverviewTimelineRow[];
  readouts: RunOverviewReadout[];
  attention: RunOverviewIssue[];
  readiness: RunOverviewIssue[];
  runsToday: number;
  nextRunAt?: string;
};

type BuildRunOverviewInput = {
  pipelines: WebPipeline[];
  schedules: EnvSchedule[];
  runs: PipelineRun[];
  deployments: Record<string, DeployStatus | undefined>;
  selectedPipelineId?: string;
  environment: string;
  bucket: ScheduleTimelineBucket;
  density: ScheduleTimelineDensity;
  now: number;
};

export function buildRunOverviewModel({
  pipelines,
  schedules,
  runs,
  deployments,
  selectedPipelineId,
  environment,
  bucket,
  density,
  now,
}: BuildRunOverviewInput): RunOverviewModel {
  const selectedPipeline = pipelines.find((pipeline) => pipeline.id === selectedPipelineId);
  const effectivePipelineId = selectedPipeline?.id;
  const scopedPipelines = effectivePipelineId
    ? pipelines.filter((pipeline) => pipeline.id === effectivePipelineId)
    : pipelines;
  const scopedPipelineIds = new Set(scopedPipelines.map((pipeline) => pipeline.id));
  const scopedSchedules = schedules.filter(
    (schedule) =>
      schedule.environment === environment &&
      Boolean(schedule.pipeline_id && scopedPipelineIds.has(schedule.pipeline_id)),
  );
  const scopedRuns = runs.filter(
    (run) =>
      scopedPipelineIds.has(run.pipeline_id) &&
      normalizeEnvironment(run.environment) === environment,
  );
  const window = scheduleTimelineWindow(bucket, density, now);
  const timelineRows = scopedPipelines
    .map((pipeline): RunOverviewTimelineRow => {
      const pipelineSchedules = scopedSchedules.filter(
        (schedule) => schedule.pipeline_id === pipeline.id,
      );
      const pipelineRuns = scopedRuns
        .filter(
          (run) =>
            run.pipeline_id === pipeline.id &&
            runOverlapsWindow(run, window.start, window.end, now),
        )
        .sort(compareRunStarts);
      const projections = pipelineSchedules
        .flatMap((schedule) =>
          scheduleExpectedSlots(
            {
              schedule: schedule.cron,
              timezone: schedule.timezone,
              enabled: schedule.status === "active",
              next_run_at: schedule.status === "active" ? schedule.next_run_at : undefined,
            },
            window,
            now,
          ).map((slot) => ({ ...slot, schedule })),
        )
        .sort((left, right) => left.time - right.time);
      return {
        pipeline,
        schedules: pipelineSchedules,
        projections,
        runs: pipelineRuns,
        cadence: timelineCadence(pipelineSchedules),
      };
    })
    .filter((row) => row.schedules.length > 0 || row.runs.length > 0)
    .sort((left, right) => left.pipeline.name.localeCompare(right.pipeline.name));

  const pipelineModels = pipelines
    .map((pipeline) => {
      const pipelineRuns = runs.filter(
        (run) =>
          run.pipeline_id === pipeline.id && normalizeEnvironment(run.environment) === environment,
      );
      const pipelineSchedules = schedules.filter(
        (schedule) => schedule.pipeline_id === pipeline.id && schedule.environment === environment,
      );
      const latestRun = pipelineRuns.sort(compareRunStarts)[0];
      const deployment = deployments[pipeline.id];
      const { health, label } = pipelineHealth(latestRun, pipelineSchedules, deployment);
      return {
        id: pipeline.id,
        name: pipeline.name,
        health,
        healthLabel: label,
        latestRun,
        scheduleCount: pipelineSchedules.length,
        deployment,
      } satisfies RunOverviewPipeline;
    })
    .sort((left, right) => left.name.localeCompare(right.name));

  const todayStart = new Date(now);
  todayStart.setHours(0, 0, 0, 0);
  const runsToday = scopedRuns.filter((run) => {
    const startedAt = Date.parse(run.started_at ?? "");
    return Number.isFinite(startedAt) && startedAt >= todayStart.getTime() && startedAt <= now;
  }).length;
  const nextRunAt = scopedSchedules
    .filter((schedule) => schedule.status === "active" && schedule.next_run_at)
    .map((schedule) => schedule.next_run_at as string)
    .filter((value) => Date.parse(value) >= now)
    .sort((left, right) => Date.parse(left) - Date.parse(right))[0];
  const scopedDeployments = scopedPipelines
    .map((pipeline) => deployments[pipeline.id])
    .filter((status): status is DeployStatus => Boolean(status));
  const activeDeploymentValue = selectedPipeline
    ? deploymentValue(deployments[selectedPipeline.id])
    : `${scopedDeployments.filter((status) => status.has_snapshot && status.executable).length} / ${scopedPipelines.length}`;
  const activeDeploymentDetail = selectedPipeline
    ? deploymentDetail(deployments[selectedPipeline.id])
    : "pipelines with an executable deployment";

  const attention = collectAttention(scopedSchedules, scopedRuns, deployments);
  const readiness = collectReadiness(scopedPipelines, deployments);
  return {
    environment,
    pipelines: pipelineModels,
    selectedPipelineId: effectivePipelineId,
    selectedPipeline,
    timelineWindow: window,
    timelineAxis: scheduleTimelineAxis(window),
    timelineRows,
    readouts: [
      {
        label: "Active deployment",
        value: activeDeploymentValue,
        detail: activeDeploymentDetail,
      },
      {
        label: "Next projected run",
        value: nextRunAt ? relativeTime(nextRunAt, now) : "Not scheduled",
        detail: nextRunAt ? formatOverviewDate(nextRunAt) : "No active schedule in this scope",
      },
      {
        label: "Runs today",
        value: String(runsToday),
        detail: `${scopedRuns.filter((run) => run.status === "running" || run.status === "queued").length} active now`,
      },
      {
        label: "Environment",
        value: environment,
        detail: selectedPipeline?.name ?? "All workspace pipelines",
      },
    ],
    attention,
    readiness,
    runsToday,
    nextRunAt,
  };
}

export function useRunOverviewModel({
  pipelineId,
  bucket,
  density,
}: {
  pipelineId?: string;
  bucket: ScheduleTimelineBucket;
  density: ScheduleTimelineDensity;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom) || "default";
  const envSchedules = useEnvSchedules();
  const runsQuery = useMemo(() => ({ limit: 500 }), []);
  const runsState = usePipelineRuns({ runsQuery });
  const [deployments, setDeployments] = useState<Record<string, DeployStatus | undefined>>({});
  const [deploymentsLoading, setDeploymentsLoading] = useState(true);
  const [deploymentsError, setDeploymentsError] = useState<string | null>(null);
  const [clock, setClock] = useState(() => Date.now());
  const deploymentRequest = useRef(0);
  const pipelines = workspace?.pipelines ?? [];
  const pipelineKey = pipelines.map((pipeline) => pipeline.id).join("\u0000");

  const refreshDeployments = useCallback(async () => {
    const request = ++deploymentRequest.current;
    if (pipelines.length === 0) {
      setDeployments({});
      setDeploymentsError(null);
      setDeploymentsLoading(false);
      return;
    }
    setDeploymentsLoading(true);
    const entries = await Promise.all(
      pipelines.map(async (pipeline) => {
        try {
          return [pipeline.id, await getDeployStatus(pipeline.id), null] as const;
        } catch (cause) {
          return [
            pipeline.id,
            undefined,
            cause instanceof Error ? cause.message : "Deployment status could not be loaded.",
          ] as const;
        }
      }),
    );
    if (request !== deploymentRequest.current) return;
    setDeployments(Object.fromEntries(entries.map(([id, status]) => [id, status])));
    const failures = entries.filter(([, , error]) => error).length;
    setDeploymentsError(
      failures > 0
        ? `${failures} deployment status${failures === 1 ? "" : "es"} could not be loaded.`
        : null,
    );
    setDeploymentsLoading(false);
  }, [pipelineKey, workspace?.revision]);

  useEffect(() => {
    void refreshDeployments();
    return () => {
      deploymentRequest.current += 1;
    };
  }, [refreshDeployments]);

  useEffect(() => {
    const timer = window.setInterval(() => setClock(Date.now()), 60_000);
    return () => window.clearInterval(timer);
  }, []);

  const model = useMemo(
    () =>
      buildRunOverviewModel({
        pipelines,
        schedules: envSchedules.schedules,
        runs: runsState.runs,
        deployments,
        selectedPipelineId: pipelineId,
        environment: selectedEnvironment,
        bucket,
        density,
        now: clock,
      }),
    [
      bucket,
      clock,
      density,
      deployments,
      envSchedules.schedules,
      pipelineId,
      pipelines,
      runsState.runs,
      selectedEnvironment,
    ],
  );

  const refresh = useCallback(async () => {
    setClock(Date.now());
    await Promise.all([envSchedules.refresh(), runsState.refreshRuns(), refreshDeployments()]);
  }, [envSchedules.refresh, refreshDeployments, runsState.refreshRuns]);

  return {
    model,
    loading: envSchedules.loading || runsState.loading || deploymentsLoading,
    error: runsState.runsError || deploymentsError,
    schedulerOwnership: envSchedules.ownership,
    canMutateSchedules: envSchedules.canMutate,
    refresh,
  };
}

function normalizeEnvironment(environment: string | undefined) {
  return environment || "default";
}

function compareRunStarts(left: PipelineRun, right: PipelineRun) {
  return Date.parse(right.started_at ?? "") - Date.parse(left.started_at ?? "");
}

function runOverlapsWindow(run: PipelineRun, start: number, end: number, now: number) {
  const runStart = Date.parse(run.started_at ?? "");
  const runEnd = run.finished_at ? Date.parse(run.finished_at) : now;
  return Number.isFinite(runStart) && Number.isFinite(runEnd) && runEnd >= start && runStart <= end;
}

function timelineCadence(schedules: EnvSchedule[]) {
  if (schedules.length === 0) return "Manual and API runs";
  if (schedules.length === 1) {
    const schedule = schedules[0];
    return `${schedule.cron} · ${schedule.environment}`;
  }
  return `${schedules.length} schedules`;
}

function pipelineHealth(
  latestRun: PipelineRun | undefined,
  schedules: EnvSchedule[],
  deployment: DeployStatus | undefined,
): { health: RunOverviewHealth; label: string } {
  if (latestRun?.status === "failed") return { health: "failed", label: "Failed" };
  if (latestRun?.status === "running" || latestRun?.status === "queued") {
    return { health: "running", label: latestRun.status === "running" ? "Running" : "Queued" };
  }
  if (deployment?.has_snapshot && !deployment.executable) {
    return { health: "failed", label: "Needs repair" };
  }
  if (schedules.some((schedule) => !schedule.snapshot_version_id)) {
    return { health: "waiting", label: "Needs deployment" };
  }
  if (latestRun?.status === "success") return { health: "healthy", label: "Healthy" };
  return { health: "idle", label: "No recent run" };
}

function collectAttention(
  schedules: EnvSchedule[],
  runs: PipelineRun[],
  deployments: Record<string, DeployStatus | undefined>,
) {
  const issues: RunOverviewIssue[] = [];
  for (const run of runs.filter((candidate) => candidate.status === "failed").slice(0, 3)) {
    issues.push({
      id: `run:${run.id}`,
      tone: "destructive",
      title: `${run.pipeline} failed`,
      detail:
        run.error || `${formatOverviewDate(run.finished_at ?? run.started_at)} · ${run.trigger}`,
      runId: run.id,
      pipelineId: run.pipeline_id,
    });
  }
  for (const schedule of schedules) {
    if (!schedule.snapshot_version_id) {
      issues.push({
        id: `schedule:${schedule.pipeline_uuid}:${schedule.environment}:deployment`,
        tone: "warning",
        title: `${schedule.pipeline_name || schedule.pipeline_uuid} needs a deployment`,
        detail: `${schedule.environment} schedule cannot run until a deployment is pinned.`,
        pipelineId: schedule.pipeline_id,
      });
    }
    if (schedule.deferred_occurrence) {
      issues.push({
        id: `schedule:${schedule.pipeline_uuid}:${schedule.environment}:deferred`,
        tone: "warning",
        title: `${schedule.pipeline_name || schedule.pipeline_uuid} is waiting`,
        detail:
          schedule.deferred_occurrence.prerequisite_reason ||
          "A scheduled occurrence is waiting for execution capacity.",
        pipelineId: schedule.pipeline_id,
      });
    }
    const deployment = schedule.pipeline_id ? deployments[schedule.pipeline_id] : undefined;
    if (
      deployment?.version_id &&
      schedule.snapshot_version_id &&
      deployment.version_id !== schedule.snapshot_version_id
    ) {
      issues.push({
        id: `schedule:${schedule.pipeline_uuid}:${schedule.environment}:outdated`,
        tone: "info",
        title: `${schedule.pipeline_name || schedule.pipeline_uuid} uses an older deployment`,
        detail: `${schedule.environment} remains pinned until it is explicitly promoted.`,
        pipelineId: schedule.pipeline_id,
      });
    }
  }
  return issues.slice(0, 6);
}

function collectReadiness(
  pipelines: WebPipeline[],
  deployments: Record<string, DeployStatus | undefined>,
) {
  const issues: RunOverviewIssue[] = [];
  for (const pipeline of pipelines) {
    const deployment = deployments[pipeline.id];
    if (!deployment) continue;
    if (!deployment.has_snapshot) {
      issues.push({
        id: `readiness:${pipeline.id}:missing`,
        tone: "warning",
        title: `${pipeline.name} has not been deployed`,
        detail: "Review and create its first immutable deployment.",
        pipelineId: pipeline.id,
      });
    } else if (!deployment.executable) {
      issues.push({
        id: `readiness:${pipeline.id}:corrupt`,
        tone: "destructive",
        title: `${pipeline.name} deployment needs repair`,
        detail: deployment.integrity_error || "The retained deployment is not executable.",
        pipelineId: pipeline.id,
      });
    } else if (!deployment.in_sync) {
      issues.push({
        id: `readiness:${pipeline.id}:drift`,
        tone: "info",
        title: `${pipeline.name} has workspace changes`,
        detail: `${deployment.changed_files?.length ?? 0} changed, ${deployment.added_files?.length ?? 0} added, ${deployment.removed_files?.length ?? 0} removed files.`,
        pipelineId: pipeline.id,
      });
    }
  }
  return issues.slice(0, 6);
}

function deploymentValue(status: DeployStatus | undefined) {
  if (!status?.has_snapshot) return "Not deployed";
  if (!status.executable) return "Needs repair";
  return status.ordinal ? `#${status.ordinal}` : status.version_id?.slice(0, 8) || "Available";
}

function deploymentDetail(status: DeployStatus | undefined) {
  if (!status?.has_snapshot) return "No retained deployment";
  if (!status.executable) return status.integrity_error || "Retained files are incomplete";
  return status.in_sync ? "Matches the saved workspace" : "Workspace has newer changes";
}

function relativeTime(value: string, now: number) {
  const difference = Date.parse(value) - now;
  if (!Number.isFinite(difference)) return "Unknown";
  const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: "always", style: "short" });
  const minutes = Math.round(difference / 60_000);
  if (Math.abs(minutes) < 60) return formatter.format(minutes, "minute");
  const hours = Math.round(minutes / 60);
  if (Math.abs(hours) < 48) return formatter.format(hours, "hour");
  return formatter.format(Math.round(hours / 24), "day");
}

function formatOverviewDate(value?: string) {
  if (!value) return "Time unavailable";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(
    date,
  );
}
