import { fetchJSON, fetchJSONWithBody } from "@/lib/api-core";
import type {
  NotebookChangeApplyResult,
  NotebookChangePlan,
  NotebookChangeSet,
  NotebookParameter,
  NotebookSourceDefinition,
  PresentationDatasetResult,
} from "@/lib/generated/api-types";
import { WebNotebook, WebNotebookBlock } from "@/lib/types";

export type NotebookImportRecord = {
  ref: string;
  object_name: string;
  imported_at: string;
  row_count: number;
  complete: boolean;
};

export type VizKind = "table" | "bar" | "line" | "area" | "pie" | "kpi";

export type VizDirective = {
  kind: VizKind;
  options: Record<string, string | number | boolean | string[]>;
};

export type VizDiagnostic = {
  message: string;
  severity: "error" | "warning";
  line: number;
  col: number;
  end_col: number;
};

export type VisualizationFieldEncoding = {
  field: string;
  label?: string;
  format?: string;
};

export type NotebookVisualizationDefinition = {
  version: number;
  type: "table" | "kpi" | "bar" | "line" | "area" | "scatter" | "pie" | "donut";
  title?: string;
  palette?: VisualizationPalette;
  encoding?: {
    x?: VisualizationFieldEncoding;
    y?: VisualizationFieldEncoding[];
    series?: VisualizationFieldEncoding;
    color?: VisualizationFieldEncoding;
    tooltip?: VisualizationFieldEncoding[];
  };
  columns?: VisualizationFieldEncoding[];
  value?: VisualizationFieldEncoding;
  compare?: VisualizationFieldEncoding;
  stacked?: boolean;
  show_legend?: boolean;
  require_complete?: boolean;
  presentation_limit?: number;
};

export type VisualizationPalette =
  | "default"
  | "ocean"
  | "sunset"
  | "forest"
  | "berry"
  | "monochrome";

export type NotebookBlockPosition = {
  position?: "start" | "end" | "after";
  after_block_id?: string;
};

export type PresentationResolvedColumn = {
  name: string;
  physical_type: string;
  semantic_type:
    | "unknown"
    | "numeric"
    | "temporal"
    | "categorical"
    | "boolean"
    | "binary"
    | "semi_structured"
    | "geospatial";
  nullable?: boolean;
};

export type PresentationFinding = {
  code: string;
  severity: "error" | "warning";
  message: string;
  path?: string;
  field?: string;
  physical_type?: string;
};

export type NotebookVisualizationCheckResult = {
  status: "ok";
  source: string;
  definition?: Record<string, unknown>;
  definition_yaml?: string;
  schema: {
    source: { kind: string; artifact_id: string; component_id?: string };
    columns: PresentationResolvedColumn[];
    complete: boolean;
    sampled: boolean;
  };
  findings: PresentationFinding[];
  can_apply: boolean;
};

export type NotebookCellRunResult = {
  cell_id: string;
  name: string;
  object_name: string;
  status: "ok" | "error" | "blocked";
  error?: string;
  columns: string[];
  rows: unknown[][];
  total_rows: number;
  materialized: "view" | "table";
  imports?: NotebookImportRecord[];
  column_types?: string[];
  sampled?: boolean;
  snapshot?: {
    block_id: string;
    object_name: string;
    source_kind: string;
    environment?: string;
    connection?: string;
    definition_fingerprint: string;
    imported_at: string;
    row_count: number;
    byte_count: number;
    complete: boolean;
    sampled: boolean;
    schema: Array<{ name: string; type: string; nullable?: boolean }>;
    warnings?: string[];
  };
  rewritten_sql?: string;
  logs?: string;
  duration_ms: number;
  performance?: {
    request_total_ms?: number;
    request_setup_ms?: number;
    batch_run_ms?: number;
    session_open_ms?: number;
    materialize_ms?: number;
    preview_query_ms?: number;
    metadata_write_ms?: number;
    runtime_sync_ms?: number;
    session_bytes?: number;
    transfer_bytes?: number;
    python_startup_ms?: number;
  };
  viz?: VizDirective | null;
  viz_diagnostics?: VizDiagnostic[];
};

export type RunNotebookResponse = {
  status: "ok" | "error" | "cancelled";
  results: NotebookCellRunResult[];
};

// The server's auto-recompute state for a notebook: which cells are stale,
// which of those it will refresh on its own, and the last result per cell.
export type NotebookRuntimeSnapshot = {
  auto_recompute: boolean;
  parameter_values: Record<string, unknown>;
  stale: string[];
  auto_pending: string[];
  running: string[];
  results: Record<string, NotebookCellRunResult>;
};

// Pushed on the SSE stream when a notebook's recompute state changes.
export type NotebookRuntimeEvent = {
  type: "notebook.runtime";
  notebook_id: string;
  auto_recompute: boolean;
  parameter_values: Record<string, unknown>;
  stale: string[];
  auto_pending: string[];
  running: string[];
  results?: Record<string, NotebookCellRunResult>;
};

export type NotebookAgentMode = "ask" | "edit";

export type NotebookAgentProvider = {
  id: "codex" | "claude" | "opencode";
  label: string;
  available: boolean;
};

export type NotebookAgentReference = {
  kind: "cell" | "asset";
  id: string;
  label: string;
  detail?: string;
};

export type NotebookAgentMessage = {
  id: string;
  turn_id: string;
  role: "user" | "assistant";
  content: string;
  references?: NotebookAgentReference[];
  status: "streaming" | "complete" | "error" | "cancelled";
  created_at: string;
};

export type NotebookAgentActivity = {
  id: string;
  turn_id: string;
  kind: string;
  title: string;
  detail?: string;
  status: "running" | "complete" | "error" | "cancelled";
  started_at: string;
  finished_at?: string;
};

export type NotebookAgentQuestionOption = {
  value: string;
  label: string;
  description?: string;
  recommended?: boolean;
};

export type NotebookAgentQuestion = {
  id: string;
  kind: "single_choice" | "multiple_choice" | "text";
  prompt: string;
  description?: string;
  required?: boolean;
  options?: NotebookAgentQuestionOption[];
};

export type NotebookAgentQuestionAnswer = {
  question_id: string;
  values?: string[];
  text?: string;
};

export type NotebookAgentConnectionCapability = "discover" | "sample_query";

export type NotebookAgentConnectionAccessRequest = {
  title: string;
  description?: string;
  connection_name?: string;
  connection_type?: string;
  capabilities?: NotebookAgentConnectionCapability[];
};

export type NotebookAgentQueryConnection = {
  name: string;
  connection_type: string;
  asset_type: string;
  dialect: string;
  environment: string;
  capabilities: NotebookAgentConnectionCapability[];
  granted: boolean;
};

export type NotebookAgentInteraction = {
  id: string;
  turn_id: string;
  kind: "questionnaire" | "connection_access";
  status: "pending" | "answered" | "declined" | "cancelled";
  title: string;
  description?: string;
  questions?: NotebookAgentQuestion[];
  answers?: NotebookAgentQuestionAnswer[];
  connection_request?: NotebookAgentConnectionAccessRequest;
  connection?: NotebookAgentQueryConnection;
  created_at: string;
  finished_at?: string;
};

export type NotebookAgentSnapshot = {
  type: "notebook.agent";
  notebook_id: string;
  revision: number;
  status: "idle" | "running" | "cancelling" | "cancelled" | "error";
  provider?: NotebookAgentProvider["id"];
  mode?: NotebookAgentMode;
  messages: NotebookAgentMessage[];
  activities: NotebookAgentActivity[];
  interaction?: NotebookAgentInteraction;
  error?: string;
  started_at?: string;
  finished_at?: string;
};

export type NotebookAgentState = {
  conversation: NotebookAgentSnapshot;
  providers: NotebookAgentProvider[];
};

export async function getNotebookAgent(notebookId: string) {
  const payload = await fetchJSON<{ status: "ok"; agent: NotebookAgentState }>(
    `/api/notebooks/${notebookId}/agent`,
    { cache: "no-store" },
  );
  return payload.agent;
}

export async function startNotebookAgentTurn(
  notebookId: string,
  input: {
    provider: NotebookAgentProvider["id"];
    mode: NotebookAgentMode;
    message: string;
    references?: Array<Pick<NotebookAgentReference, "kind" | "id">>;
  },
) {
  const payload = await fetchJSONWithBody<{
    status: "ok";
    conversation: NotebookAgentSnapshot;
  }>(`/api/notebooks/${notebookId}/agent/messages`, "POST", input);
  return payload.conversation;
}

export async function cancelNotebookAgentTurn(notebookId: string) {
  const payload = await fetchJSONWithBody<{
    status: "ok";
    conversation: NotebookAgentSnapshot;
  }>(`/api/notebooks/${notebookId}/agent/cancel`, "POST", {});
  return payload.conversation;
}

export async function resetNotebookAgent(notebookId: string) {
  const payload = await fetchJSON<{
    status: "ok";
    conversation: NotebookAgentSnapshot;
  }>(`/api/notebooks/${notebookId}/agent`, { method: "DELETE" });
  return payload.conversation;
}

export async function answerNotebookAgentInteraction(
  notebookId: string,
  interactionId: string,
  input: {
    answers?: NotebookAgentQuestionAnswer[];
    connection_name?: string;
    declined?: boolean;
  },
) {
  const payload = await fetchJSONWithBody<{
    status: "ok";
    conversation: NotebookAgentSnapshot;
  }>(
    `/api/notebooks/${notebookId}/agent/interactions/${encodeURIComponent(interactionId)}/answer`,
    "POST",
    input,
  );
  return payload.conversation;
}

export async function getNotebookRuntime(notebookId: string) {
  return fetchJSON<NotebookRuntimeSnapshot>(`/api/notebooks/${notebookId}/runtime`, {
    cache: "no-store",
  });
}

export async function refreshNotebookControlOptions(notebookId: string, controlId: string) {
  const payload = await fetchJSONWithBody<{
    status: "ok";
    result: PresentationDatasetResult;
  }>(
    `/api/notebooks/${encodeURIComponent(notebookId)}/controls/${encodeURIComponent(controlId)}/options/refresh`,
    "POST",
    {},
  );
  return payload.result;
}

export async function setNotebookSettings(
  notebookId: string,
  input: {
    auto_recompute: boolean;
    environment?: string;
    parameter_values?: Record<string, unknown>;
  },
) {
  return fetchJSONWithBody<{ status: string }>(
    `/api/notebooks/${notebookId}/settings`,
    "PUT",
    input,
  );
}

export async function cancelNotebookRun(notebookId: string) {
  return fetchJSONWithBody<{ status: string }>(`/api/notebooks/${notebookId}/cancel`, "POST", {});
}

type NotebookEnvelope = {
  status: "ok" | "error";
  notebook: WebNotebook;
};

export async function getNotebook(notebookId: string, signal?: AbortSignal) {
  const payload = await fetchJSON<NotebookEnvelope>(`/api/notebooks/${notebookId}`, {
    cache: "no-store",
    signal,
  });
  return payload.notebook;
}

export async function createNotebook(input: { title: string; path?: string }) {
  const payload = await fetchJSONWithBody<NotebookEnvelope>("/api/notebooks", "POST", input);
  return payload.notebook;
}

export async function deleteNotebook(notebookId: string) {
  return fetchJSON<{ status: string }>(`/api/notebooks/${notebookId}`, { method: "DELETE" });
}

export async function closeNotebookSession(notebookId: string) {
  return fetchJSON<{ status: string }>(`/api/notebooks/${notebookId}/session`, {
    method: "DELETE",
  });
}

export function notebookCellExportURL(
  notebookId: string,
  cellId: string,
  format: "csv" | "parquet",
) {
  return `/api/notebooks/${encodeURIComponent(notebookId)}/cells/${encodeURIComponent(cellId)}/export?format=${format}`;
}

export async function createNotebookCell(
  notebookId: string,
  input: { name?: string; language?: "sql" | "python" } = {},
) {
  const payload = await fetchJSONWithBody<NotebookEnvelope>(
    `/api/notebooks/${notebookId}/cells`,
    "POST",
    input,
  );
  return payload.notebook;
}

export async function createNotebookCellAt(
  notebookId: string,
  input: NotebookBlockPosition & { language?: "sql" | "python" } = {},
) {
  return prepareAndApplyNotebookChange(notebookId, [
    {
      kind: "cell.create",
      language: input.language ?? "sql",
      position: input.position ?? (input.after_block_id ? "after" : "end"),
      after_block_id: input.after_block_id,
    },
  ]);
}

export async function updateNotebookCell(
  notebookId: string,
  cellId: string,
  content: string,
  baseRevision?: string,
) {
  const payload = await fetchJSONWithBody<NotebookEnvelope>(
    `/api/notebooks/${notebookId}/cells/${cellId}`,
    "PUT",
    { content, base_revision: baseRevision },
  );
  return payload.notebook;
}

export async function renameNotebookCell(notebookId: string, cellId: string, name: string) {
  const payload = await fetchJSONWithBody<NotebookEnvelope>(
    `/api/notebooks/${notebookId}/cells/${cellId}/rename`,
    "POST",
    { name },
  );
  return payload.notebook;
}

export async function deleteNotebookCell(notebookId: string, cellId: string) {
  const payload = await fetchJSON<NotebookEnvelope>(
    `/api/notebooks/${notebookId}/cells/${cellId}`,
    {
      method: "DELETE",
    },
  );
  return payload.notebook;
}

// Compatibility path for manifest v1 notebooks, whose markdown blocks do not
// have stable IDs. Manifest v2 UI mutations use semantic change operations.
export async function updateNotebookBlocks(notebookId: string, blocks: WebNotebookBlock[]) {
  const payload = await fetchJSONWithBody<NotebookEnvelope>(
    `/api/notebooks/${notebookId}/blocks`,
    "PUT",
    { blocks },
  );
  return payload.notebook;
}

export async function upgradeNotebookManifest(notebookId: string, baseRevision: string) {
  const payload = await fetchJSONWithBody<NotebookEnvelope>(
    `/api/notebooks/${notebookId}/upgrade`,
    "POST",
    { base_revision: baseRevision },
  );
  return payload.notebook;
}

export async function prepareNotebookChangeSet(notebookId: string, changeSet: NotebookChangeSet) {
  return fetchJSONWithBody<NotebookChangePlan>(
    `/api/notebooks/${notebookId}/changes/prepare`,
    "POST",
    changeSet,
  );
}

export async function applyNotebookChangeSet(notebookId: string, changeSet: NotebookChangeSet) {
  return fetchJSONWithBody<NotebookChangeApplyResult>(
    `/api/notebooks/${notebookId}/changes/apply`,
    "POST",
    changeSet,
  );
}

async function prepareAndApplyNotebookChange(
  notebookId: string,
  operations: NotebookChangeSet["operations"],
) {
  const current = await getNotebook(notebookId);
  const plan = await prepareNotebookChangeSet(notebookId, {
    base_revision: current.revision,
    operations,
  });
  if (!plan.can_apply) {
    throw new Error(plan.blocking_problems?.join("; ") || "Notebook change is not valid.");
  }
  const result = await applyNotebookChangeSet(notebookId, plan.change_set);
  return result.notebook;
}

export async function configureNotebookCellSource(
  notebookId: string,
  cellId: string,
  input: { connection?: string; snapshot_mode?: "full" | "sample"; row_limit?: number },
) {
  return prepareAndApplyNotebookChange(notebookId, [
    {
      kind: "cell.source.configure",
      cell_id: cellId,
      connection: input.connection,
      snapshot_mode: input.snapshot_mode,
      row_limit: input.row_limit,
    },
  ]);
}

export async function createNotebookWarehouseSource(
  notebookId: string,
  input: {
    connection: string;
    query: string;
    snapshot_mode: "full" | "sample";
    row_limit?: number;
  },
) {
  return prepareAndApplyNotebookChange(notebookId, [
    {
      kind: "cell.create",
      language: "sql",
      content: input.query,
      connection: input.connection,
      snapshot_mode: input.snapshot_mode,
      row_limit: input.row_limit,
      position: "end",
    },
  ]);
}

export async function createNotebookSource(
  notebookId: string,
  input: Omit<NotebookSourceDefinition, "id" | "version">,
) {
  return prepareAndApplyNotebookChange(notebookId, [
    {
      kind: "source.create",
      source: { ...input, id: "", version: 1 },
      position: "end",
    },
  ]);
}

export async function checkNotebookVisualization(
  notebookId: string,
  input: {
    source: string;
    definition?: Record<string, unknown>;
    definition_yaml?: string;
  },
) {
  return fetchJSONWithBody<NotebookVisualizationCheckResult>(
    `/api/notebooks/${notebookId}/visualizations/check`,
    "POST",
    input,
  );
}

export async function createNotebookVisualization(
  notebookId: string,
  input: NotebookBlockPosition & { source: string; definition: Record<string, unknown> },
) {
  return prepareAndApplyNotebookChange(notebookId, [
    {
      kind: "visualization.create",
      visualization: { id: "", source: input.source, definition: input.definition },
      position: input.position ?? (input.after_block_id ? "after" : "end"),
      after_block_id: input.after_block_id,
    },
  ]);
}

export async function updateNotebookVisualization(
  notebookId: string,
  blockId: string,
  input: { source: string; definition: Record<string, unknown> },
) {
  return prepareAndApplyNotebookChange(notebookId, [
    {
      kind: "visualization.update",
      block_id: blockId,
      visualization: { id: blockId, source: input.source, definition: input.definition },
    },
  ]);
}

export async function deleteNotebookBlock(notebookId: string, blockId: string) {
  return prepareAndApplyNotebookChange(notebookId, [{ kind: "block.delete", block_id: blockId }]);
}

export async function createNotebookMarkdown(
  notebookId: string,
  input: NotebookBlockPosition & { content: string },
) {
  return prepareAndApplyNotebookChange(notebookId, [
    {
      kind: "markdown.create",
      content: input.content,
      position: input.position ?? (input.after_block_id ? "after" : "end"),
      after_block_id: input.after_block_id,
    },
  ]);
}

export async function updateNotebookMarkdown(notebookId: string, blockId: string, content: string) {
  return prepareAndApplyNotebookChange(notebookId, [
    { kind: "markdown.update", block_id: blockId, content },
  ]);
}

export async function migrateLegacyNotebookVisualization(notebookId: string, cellId: string) {
  return prepareAndApplyNotebookChange(notebookId, [
    { kind: "visualization.migrate_legacy", cell_id: cellId },
  ]);
}

export async function replaceNotebookParameters(
  notebookId: string,
  parameters: NotebookParameter[],
) {
  return prepareAndApplyNotebookChange(notebookId, [{ kind: "parameters.replace", parameters }]);
}

export async function createNotebookControl(
  notebookId: string,
  parameter: NotebookParameter,
  position: NotebookBlockPosition = {},
) {
  return prepareAndApplyNotebookChange(notebookId, [
    {
      kind: "control.create",
      parameter,
      position: position.position ?? (position.after_block_id ? "after" : "end"),
      after_block_id: position.after_block_id,
    },
  ]);
}

export async function updateNotebookControl(
  notebookId: string,
  controlId: string,
  parameter: NotebookParameter,
) {
  return prepareAndApplyNotebookChange(notebookId, [
    { kind: "control.update", control_id: controlId, parameter },
  ]);
}

export async function deleteNotebookControl(notebookId: string, controlId: string) {
  return prepareAndApplyNotebookChange(notebookId, [
    { kind: "control.delete", control_id: controlId },
  ]);
}

export async function updateNotebookDependencies(notebookId: string, dependencies: string[]) {
  const payload = await fetchJSONWithBody<NotebookEnvelope>(
    `/api/notebooks/${notebookId}/dependencies`,
    "PUT",
    { content: dependencies.join("\n") },
  );
  return payload.notebook;
}

export type PromoteCellResponse = {
  status: "ok" | "error";
  asset_path: string;
  asset_paths?: string[];
  promoted_count: number;
  dialect_warning?: string;
  notebook: WebNotebook;
};

export type PromoteCellInput = {
  pipeline_id: string;
  target_name: string;
  include_upstream?: boolean;
  include_downstream?: boolean;
  base_revision?: string;
};

export type PromoteCellPlan = {
  status: "ok";
  base_revision: string;
  assets: Array<{
    cell_id: string;
    cell_name: string;
    target_name: string;
    path: string;
    asset_type: string;
    connection?: string;
    source_connection?: string;
    materialization: string;
  }>;
  files: Array<{
    path: string;
    status: "added" | "modified" | "deleted";
  }>;
  warnings?: string[];
  can_apply: boolean;
};

export async function planNotebookCellPromotion(
  notebookId: string,
  cellId: string,
  input: PromoteCellInput,
) {
  return fetchJSONWithBody<PromoteCellPlan>(
    `/api/notebooks/${notebookId}/cells/${cellId}/promote/plan`,
    "POST",
    input,
  );
}

export async function promoteNotebookCell(
  notebookId: string,
  cellId: string,
  input: PromoteCellInput,
) {
  return fetchJSONWithBody<PromoteCellResponse>(
    `/api/notebooks/${notebookId}/cells/${cellId}/promote`,
    "POST",
    input,
  );
}

export async function runNotebook(
  notebookId: string,
  input: {
    all?: boolean;
    from?: string;
    cells?: string[];
    refresh_imports?: boolean;
    environment?: string;
    start_date?: string;
    end_date?: string;
    parameters?: Record<string, unknown>;
  },
  signal?: AbortSignal,
) {
  return fetchJSONWithBody<RunNotebookResponse>(`/api/notebooks/${notebookId}/run`, "POST", input, {
    signal,
  });
}

/**
 * Splits a cell file into its Bruin frontmatter header and the SQL body, so
 * the editor can show just the query while saves preserve the header.
 */
export function splitCellContent(content: string): { header: string; body: string } {
  const lines = content.split("\n");
  let opener = -1;
  let closer = -1;
  for (let index = 0; index < lines.length; index += 1) {
    if (!lines[index].includes("@bruin")) {
      continue;
    }
    if (opener === -1) {
      opener = index;
      continue;
    }
    closer = index;
    break;
  }
  if (opener === -1 || closer === -1) {
    return { header: "", body: content };
  }

  const header = lines.slice(0, closer + 1).join("\n");
  let bodyStart = closer + 1;
  while (bodyStart < lines.length && lines[bodyStart].trim() === "") {
    bodyStart += 1;
  }
  return { header, body: lines.slice(bodyStart).join("\n") };
}

/** Reassembles a cell file from its header and edited body. */
export function joinCellContent(header: string, body: string): string {
  if (!header) {
    return body;
  }
  return `${header}\n\n${body.replace(/\s+$/, "")}\n`;
}
