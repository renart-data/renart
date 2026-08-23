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
import type {
  ExternalRelationImportResult as GeneratedExternalRelationImportResult,
  TypeCheckAsset,
  TypeCheckCrossPipelineReference,
  TypeCheckExternalRelation,
  TypeCheckFinding,
  TypeCheckPresentation,
  TypeCheckPresentationFinding,
  TypeCheckReport,
  TypeCheckResolution,
} from "@/lib/generated/api-types";

export type PipelineTypeCheckResolution = Omit<TypeCheckResolution, "transaction" | "action"> & {
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

export type PipelineTypeCheckFinding = Omit<
  TypeCheckFinding,
  "severity" | "scope" | "confidence" | "resolutions"
> & {
  severity: "error" | "warning";
  scope?: "document" | "asset" | "pipeline";
  confidence?: "high" | "medium" | "low";
  resolutions?: PipelineTypeCheckResolution[];
};

export type PipelineTypeCheckAsset = Omit<TypeCheckAsset, "status" | "findings"> & {
  status: "ok" | "warning" | "error";
  findings: PipelineTypeCheckFinding[];
};

export type PipelineTypeCheckPresentationFinding = Omit<
  TypeCheckPresentationFinding,
  "severity"
> & {
  severity: "error" | "warning";
};

export type PipelineTypeCheckPresentation = Omit<
  TypeCheckPresentation,
  "kind" | "status" | "findings"
> & {
  kind: "dashboard" | "report";
  status: "ok" | "warning" | "error";
  findings: PipelineTypeCheckPresentationFinding[];
};

export type PipelineTypeCheckReport = Omit<
  TypeCheckReport,
  "status" | "assets" | "presentations" | "external_relations" | "cross_pipeline_references"
> & {
  status: "ok" | "warning" | "error";
  assets: PipelineTypeCheckAsset[];
  presentations?: PipelineTypeCheckPresentation[];
  external_relations?: PipelineTypeCheckExternalRelation[];
  cross_pipeline_references?: PipelineTypeCheckCrossPipelineReference[];
};

export type PipelineTypeCheckExternalRelation = TypeCheckExternalRelation;

export type PipelineTypeCheckCrossPipelineReference = Omit<
  TypeCheckCrossPipelineReference,
  "status"
> & {
  status: "declarable" | "producer_uri_missing" | "connection_unknown" | "connection_mismatch";
};

export type ExternalRelationImportResult = Omit<
  GeneratedExternalRelationImportResult,
  "relation"
> & { relation: PipelineTypeCheckExternalRelation };

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
