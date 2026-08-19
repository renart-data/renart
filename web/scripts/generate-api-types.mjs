import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(__dirname, "..", "..");
const outputPath = resolve(repoRoot, "web", "lib", "generated", "api-types.ts");

const sources = [
  {
    file: resolve(repoRoot, "internal", "web", "model", "artifact.go"),
    types: [
      "ArtifactIndex",
      "ArtifactDescriptor",
      "ArtifactComponent",
      "ArtifactRef",
      "ArtifactContainment",
      "ArtifactColumnUsage",
      "ArtifactDependency",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "model", "presentation.go"),
    types: [
      "PresentationArtifact",
      "PresentationDataset",
      "PresentationFilterOptions",
      "PresentationFilter",
      "PresentationFilterBinding",
      "PresentationVisualization",
      "PresentationLayoutItem",
      "PresentationSection",
      "PresentationFinding",
      "PresentationRunRequest",
      "PresentationRunResult",
      "PresentationDatasetResult",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "model", "dto.go"),
    types: [
      "MaterializationCapability",
      "AssetAuthoringCapability",
      "ColumnInferenceSource",
      "ColumnSchemaDriftItem",
      "ColumnSchemaDrift",
      "ColumnInferencePreview",
      "ColumnSchemaSourceSnapshot",
      "ColumnSchemaMergeRow",
      "ColumnSchemaSyncResult",
      "ColumnSchemaResolution",
      "AssetDependency",
      "WorkspaceDependencyDiagnostic",
      "NotebookSourceSnapshot",
      "NotebookSourceRequest",
      "NotebookSourceResponse",
      "NotebookSourceDefinition",
      "ColumnCheck",
      "CustomCheck",
      "ColumnReference",
      "Column",
      "Asset",
      "Pipeline",
      "NotebookVisualization",
      "NotebookParameterOptions",
      "NotebookParameter",
      "NotebookBlock",
      "Notebook",
      "EnvironmentPolicy",
      "WorkspaceQueryConnection",
      "WorkspaceState",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "workspace_coordinator.go"),
    types: ["WorkspaceEvent"],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "notebook_changes.go"),
    types: [
      "NotebookOperation",
      "NotebookChangeSet",
      "NotebookChangeDiff",
      "NotebookChangePlan",
      "NotebookChangeApplyResult",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "presentation_service.go"),
    types: [
      "PresentationDocument",
      "CreatePresentationRequest",
      "UpdatePresentationRequest",
      "ReplacePresentationRequest",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "config.go"),
    types: [
      "WorkspaceConfigFieldDef",
      "WorkspaceConfigSecretField",
      "WorkspaceConfigConnectionType",
      "WorkspaceLocalVault",
      "WorkspaceConfigConnection",
      "WorkspaceConfigEnvironment",
      "WorkspaceRetentionWindow",
      "WorkspaceRetentionSettings",
      "WorkspaceConfigResponse",
      "WorkspaceEnvironmentPolicyResponse",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "httpapi", "projects.go"),
    types: [
      "ProjectInfo",
      "ProjectListResponse",
      "OpenProjectResponse",
      "CreateProjectRequest",
      "CreateProjectResponse",
      "BrowseDirEntry",
      "BrowseDirsResponse",
      "CreateDirectoryRequest",
      "CreateDirectoryResponse",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "project_scaffold.go"),
    types: ["ProjectTemplateInfo", "ProjectTemplatesResponse"],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "pipeline_templates.go"),
    types: ["PipelineTemplateInfo", "PipelineTemplatesResponse"],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "onboarding.go"),
    types: [
      "OnboardingImportFormState",
      "OnboardingImportResultState",
      "OnboardingSessionState",
      "OnboardingDiscoveryResult",
      "OnboardingPathSuggestionsResult",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "suggestions.go"),
    types: ["SuggestionItem", "IngestrSuggestionsResult", "SQLPathSuggestionsResult"],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "api_openapi_suggestions.go"),
    types: [
      "OpenAPIEndpointSuggestion",
      "OpenAPIQueryParameterSuggestion",
      "OpenAPIRecordsPathSuggestion",
      "OpenAPIResponsePathSuggestion",
      "OpenAPISuggestionsResult",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "api_asset.go"),
    types: ["APIRecordsPathSample", "APIInferResult"],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "sql.go"),
    types: [
      "SQLDiscoveryTableItem",
      "SQLDatabaseDiscoveryResult",
      "SQLTableDiscoveryResult",
      "SQLTableColumnsResult",
      "SQLColumn",
      "SQLQueryResult",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "parse_context.go"),
    types: [
      "ParseContextRange",
      "ParseContextPart",
      "ParseContextTable",
      "ParseContextColumn",
      "ParseContextDiagnostic",
      "ParseContextResult",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "asset.go"),
    types: ["FormatSQLAssetResponse", "AssetMutationResponse"],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "asset_creation_profile.go"),
    types: [
      "AssetCreationProfile",
      "AssetCreationKindProfile",
      "AssetCreationRoleProfile",
      "AssetCreationConnection",
      "AssetCreationCandidate",
      "AssetCreationDefault",
      "AssetCreationPortabilityWarning",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "execution.go"),
    types: ["PipelineMaterializationState", "PipelineMaterializationResponse"],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "asset_render.go"),
    types: [
      "AssetRenderRequest",
      "AssetRenderSource",
      "AssetRenderVariableProvenance",
      "AssetRenderContext",
      "AssetRenderProvenance",
      "AssetRenderTarget",
      "AssetRenderWriteResource",
      "AssetRenderAsset",
      "AssetRenderStage",
      "AssetRenderRedaction",
      "AssetRenderIssue",
      "AssetRenderResult",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "pipeline_asset_render.go"),
    types: [
      "PipelineAssetRenderRequest",
      "PipelineAssetRenderComparisonRequest",
      "AssetRenderStageComparison",
      "AssetRenderComparisonSummary",
      "PipelineAssetRenderComparison",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "typecheck.go"),
    types: [
      "TypeCheckResolutionTransaction",
      "TypeCheckResolutionAction",
      "TypeCheckResolution",
      "TypeCheckFinding",
      "TypeCheckAsset",
      "TypeCheckPresentationFinding",
      "TypeCheckPresentation",
      "TypeCheckSummary",
      "TypeCheckExternalRelation",
      "TypeCheckCrossPipelineReference",
      "TypeCheckReport",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "asset_transactions.go"),
    types: ["TransactionDependency"],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "external_relation_import.go"),
    types: [
      "ExternalRelationImportRequest",
      "ExternalRelationImportAsset",
      "ExternalRelationImportWarning",
      "ExternalRelationImportResult",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "service", "pipeline_plan.go"),
    types: [
      "PipelinePlanSourceRequest",
      "PipelinePlanSelectionRequest",
      "PipelinePlanRequest",
      "PipelinePlanConfirmRequest",
      "PipelinePlanReviewedIdentity",
      "PipelinePlanContext",
      "PipelinePlanIssue",
      "PipelinePlanReadiness",
      "PipelinePlanSelection",
      "PipelinePlanRender",
      "PipelinePlanAsset",
      "PipelinePlanExecutionUnit",
      "PipelinePlanPrerequisite",
      "PipelinePlanResourceClaim",
      "PipelinePlanResources",
      "PipelinePlanExecutionContract",
      "PipelinePlanSummary",
      "PipelinePlan",
    ],
  },
  {
    file: resolve(repoRoot, "internal", "web", "model", "dto.go"),
    types: [
      "InspectResult",
      "InferColumnsResult",
      "OperationMetadata",
      "PipelineConfigConnection",
      "PipelineReferencedConnection",
      "PipelineConfigNotification",
      "PipelineConfigDefaults",
      "PipelineConfigVariable",
      "PipelineConfigResponse",
      "UpdatePipelineConfigRequest",
      "PipelinePythonDependenciesResponse",
      "UpdatePipelinePythonDependenciesRequest",
    ],
  },
];

const scalarMap = new Map([
  ["string", "string"],
  ["bool", "boolean"],
  ["int", "number"],
  ["int64", "number"],
  ["float64", "number"],
  ["time.Time", "string"],
  ["any", "unknown"],
  ["policy.EnvironmentPolicy", "EnvironmentPolicy"],
  ["model.NotebookVisualization", "NotebookVisualization"],
  ["model.NotebookSourceDefinition", "NotebookSourceDefinition"],
  ["model.NotebookParameter", "NotebookParameter"],
  ["model.Notebook", "WebNotebook"],
  ["model.PresentationArtifact", "PresentationArtifact"],
  ["AssetRenderStatus", '"ok" | "partial" | "unsupported" | "error"'],
  ["AssetRenderStageStatus", '"ok" | "unsupported" | "error"'],
  ["AssetRenderFidelity", '"exact" | "semantic" | "runtime_only" | "unsupported"'],
]);

const renameMap = new Map([
  ["Asset", "WebAsset"],
  ["ColumnCheck", "WebColumnCheck"],
  ["CustomCheck", "WebCustomCheck"],
  ["Pipeline", "WebPipeline"],
  ["Notebook", "WebNotebook"],
  ["NotebookBlock", "WebNotebookBlock"],
  ["OnboardingDiscoveryResult", "OnboardingDiscoveryResponse"],
  ["OnboardingPathSuggestionsResult", "OnboardingPathSuggestionsResponse"],
  ["SuggestionItem", "IngestrSuggestion"],
  ["IngestrSuggestionsResult", "IngestrSuggestionsResponse"],
  ["SQLPathSuggestionsResult", "SqlPathSuggestionsResponse"],
  ["SQLDatabaseDiscoveryResult", "SqlDiscoveryDatabasesResponse"],
  ["SQLTableDiscoveryResult", "SqlDiscoveryTablesResponse"],
  ["SQLTableColumnsResult", "SqlDiscoveryTableColumnsResponse"],
  ["SQLDiscoveryTableItem", "SqlDiscoveryTable"],
  ["SQLQueryResult", "SqlQueryResponse"],
  ["ParseContextRange", "SqlParseContextRange"],
  ["ParseContextPart", "SqlParseContextPart"],
  ["ParseContextTable", "SqlParseContextTable"],
  ["ParseContextColumn", "SqlParseContextColumn"],
  ["ParseContextDiagnostic", "SqlParseContextDiagnostic"],
  ["ParseContextResult", "SqlParseContextResponse"],
  ["InspectResult", "AssetInspectResponse"],
  ["InferColumnsResult", "InferColumnsResponse"],
  ["Column", "WebColumn"],
  ["WorkspaceColumn", "WebColumn"],
  ["PipelineConfigResponse", "WebPipelineConfigResponse"],
  ["UpdatePipelineConfigRequest", "WebUpdatePipelineConfigRequest"],
]);

function splitFields(body) {
  return body
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line && !line.startsWith("//"));
}

function extractStructBody(content, typeName) {
  const typeIndex = content.indexOf(`type ${typeName} struct {`);
  if (typeIndex < 0) {
    throw new Error(`Type ${typeName} not found`);
  }

  const bodyStart = content.indexOf("{", typeIndex) + 1;
  let depth = 1;
  let index = bodyStart;
  while (index < content.length && depth > 0) {
    const char = content[index];
    if (char === "{") depth += 1;
    if (char === "}") depth -= 1;
    index += 1;
  }

  return content.slice(bodyStart, index - 1);
}

function jsonNameFromTag(tag, fieldName) {
  const tagMatch = tag?.match(/json:"([^,"]+)/);
  if (tagMatch?.[1]) {
    return tagMatch[1];
  }

  return fieldName.replace(/([a-z0-9])([A-Z])/g, "$1_$2").toLowerCase();
}

function isOptionalTag(tag) {
  return Boolean(tag?.includes(",omitempty"));
}

function goTypeToTs(goType) {
  let value = goType.trim();
  if (value.startsWith("[]")) {
    return `${goTypeToTs(value.slice(2))}[]`;
  }
  if (value.startsWith("map[")) {
    const match = value.match(/^map\[[^\]]+\](.+)$/);
    return `Record<string, ${goTypeToTs(match[1])}>`;
  }
  if (value.startsWith("*")) {
    return goTypeToTs(value.slice(1));
  }

  return renameMap.get(value) ?? scalarMap.get(value) ?? value;
}

function parseField(line) {
  const fieldMatch = line.match(/^(\w+)\s+([^`]+?)(?:\s+`([^`]*)`)?$/);
  if (!fieldMatch) {
    return null;
  }

  const [, fieldName, rawType, rawTag] = fieldMatch;
  if (jsonNameFromTag(rawTag, fieldName) === "-") {
    return null;
  }
  return {
    fieldName,
    propertyName: jsonNameFromTag(rawTag, fieldName),
    tsType: goTypeToTs(rawType),
    optional: isOptionalTag(rawTag),
  };
}

function renderType(typeName, fields) {
  const mappedName = renameMap.get(typeName) ?? typeName;

  const body = fields
    .map((field) => `  ${field.propertyName}${field.optional ? "?" : ""}: ${field.tsType};`)
    .join("\n");

  return `export type ${mappedName} = {\n${body}\n};`;
}

const blocks = [];
for (const source of sources) {
  const content = await readFile(source.file, "utf8");
  for (const typeName of source.types) {
    const body = extractStructBody(content, typeName);
    const fields = splitFields(body).map(parseField).filter(Boolean);
    blocks.push(renderType(typeName, fields));
  }
}

const output = `// Code generated by web/scripts/generate-api-types.mjs. DO NOT EDIT.\n\n${blocks.join("\n\n")}\n`;
await mkdir(dirname(outputPath), { recursive: true });
await writeFile(outputPath, output, "utf8");
