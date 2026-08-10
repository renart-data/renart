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
  transaction?: AssetTransaction;
  action?: PipelineTypeCheckResolutionAction;
};

export type PipelineTypeCheckResolutionAction =
  | {
      type: "import-external-relation";
      relation_id: string;
    }
  | {
      type: "open-asset";
      pipeline_id: string;
      asset_id: string;
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
  external_relations?: PipelineTypeCheckExternalRelation[];
  cross_pipeline_references?: PipelineTypeCheckCrossPipelineReference[];
  summary: { assets: number; errors: number; warnings: number };
};

export type PipelineTypeCheckExternalRelation = {
  id: string;
  connection: string;
  environment?: string;
  qualified_name: string;
  schema_name?: string;
  name: string;
  columns: Array<{ name: string; type: string }>;
  columns_known: boolean;
  observed_at?: string;
  stale?: boolean;
  referenced_by_asset_ids: string[];
  referenced_by_asset_names: string[];
};

export type PipelineTypeCheckCrossPipelineReference = {
  id: string;
  status: "declarable" | "producer_uri_missing" | "connection_unknown" | "connection_mismatch";
  relation: string;
  consumer_asset_id: string;
  consumer_asset_name: string;
  producer_asset_id: string;
  producer_asset_name: string;
  producer_pipeline_id: string;
  producer_pipeline_name: string;
  producer_uri?: string;
};

export type ExternalRelationImportResult = {
  status: string;
  preview: boolean;
  relation: PipelineTypeCheckExternalRelation;
  asset: {
    name: string;
    path: string;
    type: string;
    columns: Array<{ name: string; type: string }>;
  };
  include_columns: boolean;
  warnings: Array<{ table: string; warning: string }>;
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

export async function previewExternalRelationImport(
  pipelineId: string,
  relationId: string,
  includeColumns = true,
) {
  return fetchJSONWithBody<ExternalRelationImportResult>(
    `/api/pipelines/${pipelineId}/external-relations/import/preview`,
    "POST",
    { relation_id: relationId, include_columns: includeColumns },
  );
}

export async function importExternalRelation(
  pipelineId: string,
  relationId: string,
  includeColumns = true,
) {
  return fetchJSONWithBody<ExternalRelationImportResult>(
    `/api/pipelines/${pipelineId}/external-relations/import`,
    "POST",
    { relation_id: relationId, include_columns: includeColumns },
  );
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
