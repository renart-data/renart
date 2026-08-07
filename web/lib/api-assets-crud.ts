import { fetchJSON, fetchJSONWithBody } from "@/lib/api-core";
import {
  AssetCreationProfile,
  AssetMutationResponse,
  FormatPythonAssetResponse,
  FormatSQLAssetResponse,
  PythonCompletionsResponse,
  PythonDiagnosticsResponse,
  PythonGotoDefinitionResponse,
  PythonHoverResponse,
  PythonSignatureHelpResponse,
} from "@/lib/types";

export type CreateAssetInput = {
  name?: string;
  kind?: "sql" | "python" | "api" | "load" | "seed" | "sensor";
  type?: string;
  path?: string;
  content?: string;
  executable_content?: string;
  connection?: string;
  environment?: string;
  use_pipeline_default?: boolean;
  variant?: string;
  parameters?: Record<string, string>;
  source_asset_id?: string;
  seed_file_name?: string;
  seed_file_content?: string;
};

export async function createAsset(
  pipelineId: string,
  input: CreateAssetInput,
  options?: { seedFile?: File },
) {
  if (options?.seedFile) {
    const body = new FormData();
    body.set("request", JSON.stringify(input));
    body.set("file", options.seedFile, options.seedFile.name);
    return fetchJSON<AssetMutationResponse>(`/api/pipelines/${pipelineId}/assets`, {
      method: "POST",
      body,
    });
  }
  return fetchJSONWithBody<AssetMutationResponse>(
    `/api/pipelines/${pipelineId}/assets`,
    "POST",
    input,
  );
}

export async function getAssetCreationProfile(
  pipelineId: string,
  environment: string,
  signal?: AbortSignal,
) {
  const query = new URLSearchParams();
  if (environment.trim()) query.set("environment", environment.trim());
  const suffix = query.size > 0 ? `?${query.toString()}` : "";
  return fetchJSON<AssetCreationProfile>(
    `/api/pipelines/${pipelineId}/asset-creation-profile${suffix}`,
    signal ? { signal } : undefined,
  );
}

export async function updateAsset(
  pipelineId: string,
  assetId: string,
  input: {
    name?: string;
    type?: string;
    content?: string;
    connection?: string;
    connection_selection?: {
      environment?: string;
      connection?: string;
      use_pipeline_default?: boolean;
      expected_asset_type?: string;
      confirm_type_migration?: boolean;
    };
    materialization_type?: string;
    materialization_strategy?: string;
    incremental_key?: string;
    partition_by?: string;
    cluster_by?: string[];
    time_granularity?: string;
    owner?: string;
    tags?: string[];
    meta?: Record<string, string>;
    upstreams?: string[];
    parameters?: Record<string, string>;
  },
) {
  return fetchJSONWithBody<AssetMutationResponse>(
    `/api/pipelines/${pipelineId}/assets/${assetId}`,
    "PUT",
    input,
  );
}

export async function replaceSeedAssetFile(assetId: string, file: File) {
  const body = new FormData();
  body.set("file", file, file.name);
  return fetchJSON<{ status: string; asset_id?: string; asset_path?: string }>(
    `/api/assets/${assetId}/seed-file`,
    { method: "POST", body },
  );
}

export type SeedFilePreview = {
  status: "ok";
  asset_id: string;
  file_type?: string;
  size_bytes?: number;
  displayable: boolean;
  content?: string;
  unavailable_reason?:
    | "missing_path"
    | "runtime_path"
    | "remote"
    | "binary_format"
    | "too_large"
    | "non_utf8";
};

export async function getSeedAssetFilePreview(assetId: string, signal?: AbortSignal) {
  return fetchJSON<SeedFilePreview>(
    `/api/assets/${assetId}/seed-file`,
    signal ? { signal } : undefined,
  );
}

export async function deleteAsset(pipelineId: string, assetId: string) {
  return fetchJSON<Record<string, string>>(`/api/pipelines/${pipelineId}/assets/${assetId}`, {
    method: "DELETE",
  });
}

export async function formatSQLAsset(
  assetId: string,
  content: string,
  options?: { persist?: boolean },
) {
  return fetchJSONWithBody<FormatSQLAssetResponse>(`/api/assets/${assetId}/format-sql`, "POST", {
    content,
    persist: options?.persist,
  });
}

export async function formatPythonAsset(assetId: string, content: string) {
  return fetchJSONWithBody<FormatPythonAssetResponse>(
    `/api/assets/${assetId}/format-python`,
    "POST",
    { content },
  );
}

export type AssetPythonDeps = {
  status: "ok" | "error";
  asset_id: string;
  dependencies: string[];
  installed_modules: string[];
};

/** Declared dependencies and installed import names for a Python asset. */
export async function getAssetPythonDeps(assetId: string, signal?: AbortSignal) {
  return fetchJSON<AssetPythonDeps>(
    `/api/assets/${assetId}/python-deps`,
    signal ? { signal } : undefined,
  );
}

/** Add a package to the Python asset's pyproject.toml; returns refreshed deps. */
export async function addAssetPythonDependency(assetId: string, pkg: string) {
  return fetchJSONWithBody<AssetPythonDeps>(`/api/assets/${assetId}/python-deps`, "POST", {
    package: pkg,
  });
}

export async function getPythonDiagnostics(assetId: string, content: string, signal?: AbortSignal) {
  return fetchJSONWithBody<PythonDiagnosticsResponse>(
    `/api/assets/${assetId}/python-diagnostics`,
    "POST",
    { content },
    signal ? { signal } : undefined,
  );
}

export async function getPythonCompletions(
  assetId: string,
  input: {
    content: string;
    line: number;
    column: number;
    snippets: boolean;
  },
  signal?: AbortSignal,
) {
  return fetchJSONWithBody<PythonCompletionsResponse>(
    `/api/assets/${assetId}/python-completions`,
    "POST",
    input,
    signal ? { signal } : undefined,
  );
}

export async function getPythonHover(
  assetId: string,
  input: { content: string; line: number; column: number },
  signal?: AbortSignal,
) {
  return fetchJSONWithBody<PythonHoverResponse>(
    `/api/assets/${assetId}/python-hover`,
    "POST",
    input,
    signal ? { signal } : undefined,
  );
}

export async function getPythonSignatureHelp(
  assetId: string,
  input: { content: string; line: number; column: number },
  signal?: AbortSignal,
) {
  return fetchJSONWithBody<PythonSignatureHelpResponse>(
    `/api/assets/${assetId}/python-signature-help`,
    "POST",
    input,
    signal ? { signal } : undefined,
  );
}

export async function getPythonGotoDefinition(
  assetId: string,
  input: { content: string; line: number; column: number },
  signal?: AbortSignal,
) {
  return fetchJSONWithBody<PythonGotoDefinitionResponse>(
    `/api/assets/${assetId}/python-goto-definition`,
    "POST",
    input,
    signal ? { signal } : undefined,
  );
}
