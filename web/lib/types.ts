import type {
  AssetInspectResponse as GeneratedAssetInspectResponse,
  FormatSQLAssetResponse as GeneratedFormatSQLAssetResponse,
  InferColumnsResponse as GeneratedInferColumnsResponse,
  IngestrSuggestion,
  IngestrSuggestionsResponse,
  OnboardingDiscoveryResponse as GeneratedOnboardingDiscoveryResponse,
  OnboardingImportFormState,
  OnboardingImportResultState,
  OperationMetadata,
  OnboardingPathSuggestionsResponse,
  OnboardingSessionState as GeneratedOnboardingSessionState,
  PipelineConfigConnection as GeneratedPipelineConfigConnection,
  PipelineConfigDefaults as GeneratedPipelineConfigDefaults,
  PipelineConfigNotification as GeneratedPipelineConfigNotification,
  PipelineConfigVariable as GeneratedPipelineConfigVariable,
  PipelineMaterializationResponse as GeneratedPipelineMaterializationResponse,
  SqlDiscoveryDatabasesResponse,
  SqlDiscoveryTable,
  SqlDiscoveryTableColumnsResponse,
  SqlDiscoveryTablesResponse,
  SqlParseContextColumn,
  SqlParseContextDiagnostic,
  SqlParseContextPart,
  SqlParseContextRange,
  SqlParseContextResponse as GeneratedSqlParseContextResponse,
  SqlParseContextTable,
  SqlPathSuggestionsResponse,
  WebAsset as GeneratedWebAsset,
  WebPipelineConfigResponse as GeneratedWebPipelineConfigResponse,
  WebPipeline as GeneratedWebPipeline,
  WebUpdatePipelineConfigRequest as GeneratedWebUpdatePipelineConfigRequest,
  WebColumn,
  WebColumnCheck,
  WorkspaceConfigConnection,
  WorkspaceConfigConnectionType as GeneratedWorkspaceConfigConnectionType,
  WorkspaceConfigEnvironment,
  WorkspaceConfigFieldDef as GeneratedWorkspaceConfigFieldDef,
  WorkspaceConfigResponse as GeneratedWorkspaceConfigResponse,
  WorkspaceEvent as GeneratedWorkspaceEvent,
  WorkspaceState as GeneratedWorkspaceState,
} from "@/lib/generated/api-types";

export type {
  IngestrSuggestion,
  IngestrSuggestionsResponse,
  OnboardingImportFormState,
  OnboardingImportResultState,
  OperationMetadata,
  OnboardingPathSuggestionsResponse,
  SqlDiscoveryDatabasesResponse,
  SqlDiscoveryTable,
  SqlDiscoveryTableColumnsResponse,
  SqlDiscoveryTablesResponse,
  SqlParseContextColumn,
  SqlParseContextDiagnostic,
  SqlParseContextPart,
  SqlParseContextRange,
  SqlParseContextTable,
  SqlPathSuggestionsResponse,
  WebColumn,
  WebColumnCheck,
  WorkspaceConfigConnection,
  WorkspaceConfigEnvironment,
};

export type WorkspaceConfigFieldType = "string" | "int" | "bool";

export type WorkspaceConfigFieldDef = Omit<
  GeneratedWorkspaceConfigFieldDef,
  "type"
> & {
  type: WorkspaceConfigFieldType;
};

export type WorkspaceConfigConnectionType = Omit<
  GeneratedWorkspaceConfigConnectionType,
  "fields"
> & {
  fields: WorkspaceConfigFieldDef[];
};

export type WorkspaceConfigResponse = Omit<
  GeneratedWorkspaceConfigResponse,
  "status" | "connection_types"
> & {
  status: "ok" | "error";
  connection_types: WorkspaceConfigConnectionType[];
};

export type OnboardingDiscoveryResponse = Omit<GeneratedOnboardingDiscoveryResponse, "status"> & {
  status: "ok" | "error";
};

export type SqlParseContextResponse = GeneratedSqlParseContextResponse & {
  status: "ok" | "error";
};

export type WebAsset = GeneratedWebAsset & {
  freshness_status?: "fresh" | "stale";
};

export type WebPipeline = Omit<GeneratedWebPipeline, "assets"> & {
  assets: WebAsset[];
};

export type WorkspaceState = Omit<GeneratedWorkspaceState, "pipelines"> & {
  pipelines: WebPipeline[];
};

export type WorkspaceEvent = Omit<GeneratedWorkspaceEvent, "workspace"> & {
  workspace: WorkspaceState;
};

export type OnboardingSessionState = Omit<
  GeneratedOnboardingSessionState,
  "step" | "import_result"
> & {
  step?: "start" | "connection-type" | "connection-config" | "import" | "quickstart" | "success";
  import_result?: OnboardingImportResultState | null;
};

export type OnboardingImportResponse = {
  status: "ok" | "error";
  operation?: OperationMetadata;
  output?: string;
  error?: string;
  pipeline_path?: string;
  asset_paths?: string[];
};

export type OnboardingImportSummary = {
  database?: string;
  importedTables?: number;
  mergedTables?: number;
  pipelinePath?: string;
  processedAssets?: number;
  successfulAssets?: number;
  failedAssets?: number;
  warnings: string[];
};

export type AssetInspectResponse = GeneratedAssetInspectResponse & {
  status: "ok" | "error";
  operation?: OperationMetadata;
  warning?: string;
};

export type InferColumnsResponse = GeneratedInferColumnsResponse & {
  status: "ok" | "error";
  operation?: OperationMetadata;
};

export type FormatSQLAssetResponse = GeneratedFormatSQLAssetResponse & {
  status: "ok" | "error";
  error?: string;
};

export type PipelineMaterializationResponse = Omit<GeneratedPipelineMaterializationResponse, "assets"> & {
  assets: Array<GeneratedPipelineMaterializationResponse["assets"][number] & {
    freshness_status?: "fresh" | "stale";
  }>;
};

export type PipelineConfigConnection = GeneratedPipelineConfigConnection;

export type PipelineConfigNotification = GeneratedPipelineConfigNotification;

export type PipelineConfigDefaults = GeneratedPipelineConfigDefaults;

export type PipelineConfigVariable = GeneratedPipelineConfigVariable & {
  extra?: Record<string, unknown>;
};

export type PipelineConfigResponse = Omit<
  GeneratedWebPipelineConfigResponse,
  "status" | "variables"
> & {
  status: "ok" | "error";
  variables: PipelineConfigVariable[];
};

export type UpdatePipelineConfigRequest = Omit<
  GeneratedWebUpdatePipelineConfigRequest,
  "variables"
> & {
  variables: PipelineConfigVariable[];
};

export type AssetFreshnessEntry = {
  asset_name: string;
  materialized_at?: string;
  materialized_status?: string;
  content_changed_at?: string;
};

export type AssetFreshnessResponse = {
  assets: AssetFreshnessEntry[];
};
