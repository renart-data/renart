import {
  buildQueryString,
  fetchJSON,
  fetchJSONWithBody,
  MaterializeStreamPayload,
} from "@/lib/api-core";
import { streamMaterialization } from "@/lib/api-streams";
import {
  PipelineConfigResponse,
  PipelineMaterializationResponse,
  PipelinePythonDependenciesResponse,
  PipelineTemplatesResponse,
  UpdatePipelineConfigRequest,
  UpdatePipelinePythonDependenciesRequest,
} from "@/lib/types";
import type { AssetTransaction } from "@/lib/api-asset-transactions";

export type PipelineTypeCheckResolution = {
  id: string;
  title: string;
  transaction: AssetTransaction;
};

export async function getPipelineTemplates() {
  return fetchJSON<PipelineTemplatesResponse>("/api/pipelines/templates", {
    cache: "no-store",
  });
}

export async function createPipeline(input: {
  path: string;
  name?: string;
  content?: string;
  template?: string;
}) {
  return fetchJSONWithBody<Record<string, string>>("/api/pipelines", "POST", input);
}

export async function deletePipeline(pipelineId: string) {
  return fetchJSON<Record<string, string>>(`/api/pipelines/${pipelineId}`, {
    method: "DELETE",
  });
}

export async function updatePipeline(input: { id: string; name?: string; content?: string }) {
  return fetchJSONWithBody<Record<string, string>>("/api/pipelines", "PUT", input);
}

export async function getPipelineConfig(pipelineId: string) {
  return fetchJSON<PipelineConfigResponse>(`/api/pipelines/${pipelineId}/config`, {
    method: "GET",
    cache: "no-store",
  });
}

export type PipelineTypeCheckFinding = {
  code: string;
  source: string;
  severity: "error" | "warning";
  message: string;
  line?: number;
  column?: number;
  end_line?: number;
  end_column?: number;
  scope?: "document" | "asset" | "pipeline";
  confidence?: "high" | "medium" | "low";
  resolutions?: PipelineTypeCheckResolution[];
};

export type PipelineTypeCheckAsset = {
  id?: string;
  name: string;
  type: string;
  dialect?: string;
  status: "ok" | "warning" | "error";
  findings: PipelineTypeCheckFinding[];
};

export type PipelineTypeCheckReport = {
  status: "ok" | "warning" | "error";
  pipeline_id?: string;
  pipeline_name: string;
  start_date?: string;
  end_date?: string;
  assets: PipelineTypeCheckAsset[];
  summary: { assets: number; errors: number; warnings: number };
};

/** Type-checks every asset in a pipeline (SQL columns/types + missing column declarations). */
export async function typeCheckPipeline(
  pipelineId: string,
  options?: { startDate?: string; endDate?: string },
) {
  const query = buildQueryString({
    start_date: options?.startDate,
    end_date: options?.endDate,
  });
  return fetchJSON<PipelineTypeCheckReport>(`/api/pipelines/${pipelineId}/type-check${query}`, {
    method: "GET",
    cache: "no-store",
  });
}

export async function updatePipelineConfig(pipelineId: string, input: UpdatePipelineConfigRequest) {
  return fetchJSONWithBody<PipelineConfigResponse>(
    `/api/pipelines/${pipelineId}/config`,
    "PUT",
    input,
  );
}

export async function getPipelinePythonDependencies(pipelineId: string) {
  return fetchJSON<PipelinePythonDependenciesResponse>(
    `/api/pipelines/${pipelineId}/python-dependencies`,
    { method: "GET", cache: "no-store" },
  );
}

export async function updatePipelinePythonDependencies(
  pipelineId: string,
  input: UpdatePipelinePythonDependenciesRequest,
) {
  return fetchJSONWithBody<PipelinePythonDependenciesResponse>(
    `/api/pipelines/${pipelineId}/python-dependencies`,
    "PUT",
    input,
  );
}

export async function materializePipelineStream(
  pipelineId: string,
  handlers: {
    onChunk?: (chunk: string) => void;
    onDone?: (payload: MaterializeStreamPayload) => void;
  },
  options?: {
    environment?: string;
    dryRun?: boolean;
    fullRefresh?: boolean;
    backfill?: boolean;
    timeWindow?: { start: string; end: string };
    confirmedEnvironment?: string;
  },
) {
  return streamMaterialization(
    `/api/pipelines/${pipelineId}/materialize/stream${buildQueryString({
      environment: options?.environment,
      dry_run: options?.dryRun ? "true" : undefined,
      full_refresh: options?.fullRefresh ? "true" : undefined,
      backfill: options?.backfill ? "true" : undefined,
      start_date: options?.timeWindow?.start,
      end_date: options?.timeWindow?.end,
      confirmed_environment: options?.confirmedEnvironment,
    })}`,
    handlers,
    "Pipeline materialization stream ended unexpectedly.",
  );
}

export async function getPipelineMaterialization(
  pipelineId: string,
  options?: { environment?: string },
) {
  return fetchJSON<PipelineMaterializationResponse>(
    `/api/pipelines/${pipelineId}/materialization${buildQueryString({
      environment: options?.environment,
    })}`,
    {
      method: "GET",
      cache: "no-store",
    },
  );
}
