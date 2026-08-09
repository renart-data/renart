import { fetchJSON, fetchJSONWithBody } from "@/lib/api-core";
import type { PipelineRun } from "@/lib/types";
import { awaitWorkspaceSaves } from "@/lib/workspace-save-barrier";

export type CatchupPolicy = "skip" | "run_once" | "backfill";
export type EnvScheduleStatus = "active" | "paused" | "archived" | "delegated";
export type SchedulerOwnershipState = "owner" | "follower" | "unavailable";

export type SchedulerOwnership = {
  state: SchedulerOwnershipState;
  message?: string;
};

export type EnvSchedule = {
  pipeline_uuid: string;
  environment: string;
  snapshot_version_id?: string;
  snapshot_ordinal?: number;
  cron: string;
  timezone: string;
  variable_names?: string[];
  secret_reference_names?: string[];
  declaration_managed: boolean;
  catchup_policy: CatchupPolicy;
  status: EnvScheduleStatus;
  archived_reason?: string;
  next_run_at?: string;
  created_at: string;
  updated_at: string;
  pipeline_id?: string;
  pipeline_name?: string;
  last_run?: PipelineRun;
  deferred_occurrence?: {
    interval_start: string;
    interval_end: string;
    attempt_count: number;
    status: "pending" | "waiting_prerequisites";
    prerequisite_deadline?: string;
    prerequisite_reason?: string;
  };
};

export type EnvSchedulesResponse = {
  status: "ok" | "error";
  scheduler: SchedulerOwnership;
  schedules: EnvSchedule[];
  archived: EnvSchedule[];
};

type EnvScheduleSourceInput =
  | { snapshot_version_id: string; deploy_now?: never; preserve_snapshot?: never }
  | { deploy_now: true; snapshot_version_id?: never; preserve_snapshot?: never }
  | { preserve_snapshot: true; snapshot_version_id?: never; deploy_now?: never };

export type UpsertEnvScheduleInput = {
  cron: string;
  timezone?: string;
  vars?: Record<string, unknown>;
  secret_refs?: Record<string, string>;
  preserve_variables?: boolean;
  catchup_policy?: CatchupPolicy;
  paused?: boolean;
} & EnvScheduleSourceInput;

export async function getEnvSchedules(): Promise<EnvSchedulesResponse> {
  return fetchJSON<EnvSchedulesResponse>("/api/env-schedules", { cache: "no-store" });
}

export async function upsertEnvSchedule(
  pipelineId: string,
  environment: string,
  input: UpsertEnvScheduleInput,
): Promise<{ status: string; schedule: EnvSchedule }> {
  // deploy_now snapshots the working tree. Flush every mounted editor before
  // asking the backend to resolve that source, just like the standalone Deploy
  // action does.
  if (input.deploy_now) {
    await awaitWorkspaceSaves();
  }
  return fetchJSONWithBody(
    `/api/pipelines/${pipelineId}/env-schedules/${encodeURIComponent(environment)}`,
    "PUT",
    input,
  );
}

export type EnvSchedulePinSelection = {
  environment: string;
  expected_snapshot_version_id: string;
};

export async function promoteEnvSchedules(
  pipelineId: string,
  snapshotVersionId: string,
  schedules: EnvSchedulePinSelection[],
): Promise<{ status: string; schedules: EnvSchedule[] }> {
  return fetchJSONWithBody(`/api/pipelines/${pipelineId}/env-schedules/promote`, "POST", {
    snapshot_version_id: snapshotVersionId,
    schedules,
  });
}

export async function setEnvScheduleStatus(
  pipelineId: string,
  environment: string,
  status: "active" | "paused",
): Promise<{ status: string }> {
  return fetchJSONWithBody(
    `/api/pipelines/${pipelineId}/env-schedules/${encodeURIComponent(environment)}/status`,
    "POST",
    { status },
  );
}

export async function archiveEnvSchedule(
  pipelineId: string,
  environment: string,
): Promise<{ status: string }> {
  return fetchJSON(
    `/api/pipelines/${pipelineId}/env-schedules/${encodeURIComponent(environment)}`,
    {
      method: "DELETE",
    },
  );
}

export async function triggerEnvSchedule(
  pipelineId: string,
  environment: string,
): Promise<{ status: string; run: PipelineRun }> {
  return fetchJSON(
    `/api/pipelines/${pipelineId}/env-schedules/${encodeURIComponent(environment)}/run`,
    { method: "POST" },
  );
}
