import { fetchJSON } from "@/lib/api-core";

export type SnapshotSummary = {
  version_id: string;
  pipeline_id: string;
  ordinal: number;
  merkle_root: string;
  file_count: number;
  git_sha?: string;
  git_dirty?: boolean;
  created_at: string;
  created_by?: string;
};

export type DeployStatus = {
  has_snapshot: boolean;
  executable: boolean;
  integrity_error?: string;
  in_sync: boolean;
  dependency_manifest_in_sync: boolean;
  dependency_manifest_error?: string;
  version_id?: string;
  ordinal?: number;
  source_merkle: string;
  created_at?: string;
  changed_files?: string[];
  added_files?: string[];
  removed_files?: string[];
  snapshot_count: number;
};

export type DeployResponse = {
  status: "ok" | "error";
  created: boolean;
  message: string;
  snapshot: SnapshotSummary;
};

export type DeploymentFileDiff = {
  path: string;
  status: "added" | "changed" | "removed" | "unchanged";
  before?: string;
  after?: string;
  before_exists: boolean;
  after_exists: boolean;
  binary: boolean;
  too_large: boolean;
};

export async function getDeployStatus(pipelineId: string): Promise<DeployStatus> {
  return fetchJSON<DeployStatus>(`/api/pipelines/${pipelineId}/deploy/status`, {
    cache: "no-store",
  });
}

export async function deployPipeline(
  pipelineId: string,
  expectedSourceMerkle?: string,
): Promise<DeployResponse> {
  return fetchJSON<DeployResponse>(`/api/pipelines/${pipelineId}/deploy`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ expected_source_merkle: expectedSourceMerkle }),
  });
}

export async function listSnapshots(pipelineId: string): Promise<{ snapshots: SnapshotSummary[] }> {
  return fetchJSON<{ snapshots: SnapshotSummary[] }>(`/api/pipelines/${pipelineId}/snapshots`, {
    cache: "no-store",
  });
}

export async function getDeploymentFileDiff(
  pipelineId: string,
  path: string,
  versionId?: string,
): Promise<DeploymentFileDiff> {
  const params = new URLSearchParams({ path });
  if (versionId) params.set("version_id", versionId);
  const response = await fetchJSON<{ status: string; diff: DeploymentFileDiff }>(
    `/api/pipelines/${pipelineId}/deploy/diff?${params.toString()}`,
    { cache: "no-store" },
  );
  return response.diff;
}
