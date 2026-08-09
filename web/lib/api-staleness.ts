import { fetchJSON, type MaterializeStreamPayload } from "@/lib/api-core";
import { streamMaterialization, type StreamAssetEvent } from "@/lib/api-streams";

export type AssetStalenessStatus =
  | "fresh"
  | "stale_edited"
  | "stale_deployment"
  | "stale_upstream"
  | "partial"
  | "volatile"
  | "external"
  | "never_built"
  | "missing";

export type StalenessInterval = {
  start: string;
  end: string;
};

export type TargetFidelity = "exact" | "runtime_only" | "unresolved" | "legacy";

export type LatestPhysicalOutput = {
  target_identity: string;
  target_generation: number;
  writer_asset_id: string;
  writer_environment: string;
  fingerprint: string;
  vars_hash: string;
  run_id?: string;
  snapshot_version_id?: string;
  materialized_at: string;
  completion_id: string;
  completion_ordinal: number;
  ambiguous: boolean;
};

export type FailedQualityCheck = {
  kind: "custom" | "column";
  name: string;
  column?: string;
  blocking?: boolean;
};

export type AssetStaleness = {
  asset_id: string;
  asset_name: string;
  status: AssetStalenessStatus;
  fingerprint: string;
  interval_aware: boolean;
  backfill_safe: boolean;
  volatile?: boolean;
  covered_seconds?: number;
  total_seconds?: number;
  gaps?: StalenessInterval[];
  last_materialized_at?: string;
  // Most recent run attempt, orthogonal to `status`. Together they distinguish an
  // untested edit from an edit that was run and failed, and surface unchanged code
  // whose last run failed. `last_run_on_current_content` is true when that run was
  // on the content currently on disk.
  last_run_status?: "succeeded" | "failed" | "cancelled";
  last_run_at?: string;
  last_run_on_current_content?: boolean;
  // Post-write assertions are independent of freshness. A successful write can
  // remain fresh while its latest checks fail.
  quality_status?: "passed" | "failed";
  failed_checks?: FailedQualityCheck[];
  quality_run_id?: string;
  quality_checked_at?: string;
  quality_on_current_content?: boolean;
  // The selected physical output and the latest durable fact about what is
  // present there. Runtime-only and unresolved targets deliberately omit a
  // reusable target identity/output.
  target_fidelity: TargetFidelity;
  target_identity?: string;
  latest_output?: LatestPhysicalOutput;
};

export type PipelineStalenessResponse = {
  pipeline_id: string;
  pipeline_uuid: string;
  environment: string;
  data_state_token: string;
  assets: AssetStaleness[];
};

export type StalenessUpdatedEvent = {
  type: "staleness.updated";
  pipeline_id: string;
  pipeline_uuid: string;
  environment: string;
  start?: string;
  end?: string;
  data_state_token: string;
  assets: AssetStaleness[];
};

// buildStalePipelineStream runs the server-side "build stale assets"
// operation: the server recomputes the stale set for this selection and
// rebuilds it in one streamed run (topological order, single combined log).
export async function buildStalePipelineStream(
  pipelineId: string,
  handlers: {
    onChunk?: (chunk: string) => void;
    onDone?: (payload: MaterializeStreamPayload) => void;
    onAssetEvent?: (event: StreamAssetEvent) => void;
  },
  options: { environment: string; start?: string; end?: string },
) {
  const params = new URLSearchParams();
  if (options.environment) params.set("environment", options.environment);
  if (options.start) params.set("start", options.start);
  if (options.end) params.set("end", options.end);
  const query = params.toString();
  return streamMaterialization(
    `/api/pipelines/${pipelineId}/build-stale/stream${query ? `?${query}` : ""}`,
    handlers,
    "Stale build stream ended unexpectedly.",
  );
}

export async function getPipelineStaleness(
  pipelineId: string,
  options: { environment?: string; start?: string; end?: string } = {},
): Promise<PipelineStalenessResponse> {
  const params = new URLSearchParams();
  if (options.environment) params.set("environment", options.environment);
  if (options.start) params.set("start", options.start);
  if (options.end) params.set("end", options.end);
  const query = params.toString();
  return fetchJSON<PipelineStalenessResponse>(
    `/api/pipelines/${pipelineId}/staleness${query ? `?${query}` : ""}`,
    { cache: "no-store" },
  );
}
