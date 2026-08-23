import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";

import {
  configureNotebookCellSource,
  createNotebookSource,
  createNotebookWarehouseSource,
} from "@/lib/api-notebooks";
import type { WebAsset, WebNotebook, WorkspaceQueryConnection } from "@/lib/types";

export type NotebookDataSourceInput =
  | {
      kind: "warehouse";
      connection: string;
      relation: string;
      snapshotMode: "full" | "sample";
      rowLimit?: number;
    }
  | {
      kind: "file";
      connection?: string;
      uri: string;
      format?: string;
      snapshotMode: "full" | "sample";
      rowLimit?: number;
    }
  | {
      kind: "http";
      url: string;
      method: string;
      body?: unknown;
      recordsPath?: string;
      snapshotMode: "full" | "sample";
      rowLimit?: number;
    };

export type NotebookDataSourceTable = { name: string; short_name: string };

export type NotebookDataSourceFormState = {
  kind: "warehouse" | "file" | "http";
  connection: string;
  relation: string;
  filter: string;
  tables: NotebookDataSourceTable[];
  fileConnection: string;
  fileURI: string;
  fileFormat: string;
  requestURL: string;
  requestMethod: string;
  requestBody: string;
  recordsPath: string;
  snapshotMode: "full" | "sample";
  rowLimit: number;
  loading: boolean;
  creating: boolean;
  error: string;
};

export type NotebookDataSourceFormEvent =
  | { type: "dialog_opened"; defaultConnection: string }
  | { type: "kind_changed"; kind: NotebookDataSourceFormState["kind"] }
  | { type: "query_connection_defaulted"; connection: string }
  | { type: "query_connection_changed"; connection: string }
  | { type: "relation_changed"; relation: string }
  | { type: "filter_changed"; filter: string }
  | { type: "tables_cleared" }
  | { type: "tables_load_started" }
  | { type: "tables_loaded"; tables: NotebookDataSourceTable[] }
  | { type: "tables_load_failed"; message: string }
  | { type: "file_connection_changed"; connection: string }
  | { type: "file_uri_changed"; uri: string }
  | { type: "file_format_changed"; format: string }
  | { type: "request_url_changed"; url: string }
  | { type: "request_method_changed"; method: string }
  | { type: "request_body_changed"; body: string }
  | { type: "records_path_changed"; recordsPath: string }
  | { type: "snapshot_mode_changed"; mode: "full" | "sample" }
  | { type: "row_limit_changed"; rowLimit: number }
  | { type: "create_started" }
  | { type: "create_finished" }
  | { type: "create_failed"; message: string };

export function createNotebookDataSourceFormState(
  defaultConnection = "",
): NotebookDataSourceFormState {
  return {
    kind: "warehouse",
    connection: defaultConnection,
    relation: "",
    filter: "",
    tables: [],
    fileConnection: "__local__",
    fileURI: "",
    fileFormat: "__auto__",
    requestURL: "",
    requestMethod: "GET",
    requestBody: "",
    recordsPath: "",
    snapshotMode: "full",
    rowLimit: 10000,
    loading: false,
    creating: false,
    error: "",
  };
}

export function notebookDataSourceFormReducer(
  state: NotebookDataSourceFormState,
  event: NotebookDataSourceFormEvent,
): NotebookDataSourceFormState {
  switch (event.type) {
    case "dialog_opened":
      return createNotebookDataSourceFormState(event.defaultConnection);
    case "kind_changed":
      return { ...state, kind: event.kind };
    case "query_connection_defaulted":
      return { ...state, connection: event.connection };
    case "query_connection_changed":
      return { ...state, connection: event.connection, relation: "" };
    case "relation_changed":
      return { ...state, relation: event.relation };
    case "filter_changed":
      return { ...state, filter: event.filter };
    case "tables_cleared":
      return { ...state, tables: [], loading: false };
    case "tables_load_started":
      return { ...state, loading: true, error: "" };
    case "tables_loaded":
      return { ...state, tables: event.tables, loading: false };
    case "tables_load_failed":
      return { ...state, tables: [], loading: false, error: event.message };
    case "file_connection_changed":
      return { ...state, fileConnection: event.connection, fileURI: "" };
    case "file_uri_changed":
      return { ...state, fileURI: event.uri };
    case "file_format_changed":
      return { ...state, fileFormat: event.format };
    case "request_url_changed":
      return { ...state, requestURL: event.url };
    case "request_method_changed":
      return { ...state, requestMethod: event.method };
    case "request_body_changed":
      return { ...state, requestBody: event.body };
    case "records_path_changed":
      return { ...state, recordsPath: event.recordsPath };
    case "snapshot_mode_changed":
      return { ...state, snapshotMode: event.mode };
    case "row_limit_changed":
      return { ...state, rowLimit: Math.max(1, event.rowLimit || 1) };
    case "create_started":
      return { ...state, creating: true, error: "" };
    case "create_finished":
      return { ...state, creating: false };
    case "create_failed":
      return { ...state, creating: false, error: event.message };
  }
}

export function notebookDataSourceCanSubmit(state: NotebookDataSourceFormState) {
  return (
    (state.snapshotMode === "full" || state.rowLimit > 0) &&
    ((state.kind === "warehouse" && Boolean(state.connection && state.relation.trim())) ||
      (state.kind === "file" && Boolean(state.fileURI.trim())) ||
      (state.kind === "http" && Boolean(state.requestURL.trim())))
  );
}

export function notebookDataSourceInput(
  state: NotebookDataSourceFormState,
): NotebookDataSourceInput {
  const snapshot = {
    snapshotMode: state.snapshotMode,
    rowLimit: state.snapshotMode === "sample" ? state.rowLimit : undefined,
  } as const;
  if (state.kind === "warehouse") {
    return {
      kind: "warehouse",
      connection: state.connection,
      relation: state.relation.trim(),
      ...snapshot,
    };
  }
  if (state.kind === "file") {
    return {
      kind: "file",
      connection: state.fileConnection === "__local__" ? undefined : state.fileConnection,
      uri: state.fileURI.trim(),
      format: state.fileFormat === "__auto__" ? undefined : state.fileFormat,
      ...snapshot,
    };
  }

  let body: unknown;
  if (state.requestBody.trim()) {
    try {
      body = JSON.parse(state.requestBody);
    } catch {
      throw new Error("Request body must be valid JSON.");
    }
  }
  return {
    kind: "http",
    url: state.requestURL.trim(),
    method: state.requestMethod,
    body,
    recordsPath: state.recordsPath.trim() || undefined,
    ...snapshot,
  };
}

export function useNotebookDataSourceForm({
  open,
  defaultQueryConnection,
  environment,
  loadTables,
  onCreate,
}: {
  open: boolean;
  defaultQueryConnection: string;
  environment: string;
  loadTables: (input: {
    connection: string;
    environment: string;
  }) => Promise<NotebookDataSourceTable[]>;
  onCreate: (input: NotebookDataSourceInput) => Promise<void>;
}) {
  const [state, dispatch] = useReducer(
    notebookDataSourceFormReducer,
    createNotebookDataSourceFormState(defaultQueryConnection),
  );
  const wasOpen = useRef(false);

  useEffect(() => {
    const justOpened = open && !wasOpen.current;
    wasOpen.current = open;
    if (justOpened) {
      dispatch({ type: "dialog_opened", defaultConnection: defaultQueryConnection });
    }
  }, [defaultQueryConnection, open]);

  useEffect(() => {
    if (open && !state.connection && defaultQueryConnection) {
      dispatch({ type: "query_connection_defaulted", connection: defaultQueryConnection });
    }
  }, [defaultQueryConnection, open, state.connection]);

  useEffect(() => {
    if (!open || state.kind !== "warehouse" || !state.connection) {
      dispatch({ type: "tables_cleared" });
      return;
    }
    let cancelled = false;
    dispatch({ type: "tables_load_started" });
    void loadTables({ connection: state.connection, environment })
      .then((tables) => {
        if (!cancelled) dispatch({ type: "tables_loaded", tables });
      })
      .catch((cause: unknown) => {
        if (!cancelled) {
          dispatch({
            type: "tables_load_failed",
            message: cause instanceof Error ? cause.message : "Could not browse this connection.",
          });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [environment, loadTables, open, state.connection, state.kind]);

  const visibleTables = useMemo(() => {
    const filter = state.filter.trim().toLowerCase();
    return state.tables.filter((table) => table.name.toLowerCase().includes(filter));
  }, [state.filter, state.tables]);

  const submit = useCallback(async () => {
    if (state.creating) return;
    dispatch({ type: "create_started" });
    try {
      await onCreate(notebookDataSourceInput(state));
      dispatch({ type: "create_finished" });
    } catch (cause) {
      dispatch({
        type: "create_failed",
        message: cause instanceof Error ? cause.message : "Could not add the data source.",
      });
    }
  }, [onCreate, state]);

  return {
    state,
    visibleTables,
    canSubmit: notebookDataSourceCanSubmit(state),
    submit,
    setKind: (kind: NotebookDataSourceFormState["kind"]) =>
      dispatch({ type: "kind_changed", kind }),
    setConnection: (connection: string) =>
      dispatch({ type: "query_connection_changed", connection }),
    setRelation: (relation: string) => dispatch({ type: "relation_changed", relation }),
    setFilter: (filter: string) => dispatch({ type: "filter_changed", filter }),
    setFileConnection: (connection: string) =>
      dispatch({ type: "file_connection_changed", connection }),
    setFileURI: (uri: string) => dispatch({ type: "file_uri_changed", uri }),
    setFileFormat: (format: string) => dispatch({ type: "file_format_changed", format }),
    setRequestURL: (url: string) => dispatch({ type: "request_url_changed", url }),
    setRequestMethod: (method: string) => dispatch({ type: "request_method_changed", method }),
    setRequestBody: (body: string) => dispatch({ type: "request_body_changed", body }),
    setRecordsPath: (recordsPath: string) =>
      dispatch({ type: "records_path_changed", recordsPath }),
    setSnapshotMode: (mode: "full" | "sample") => dispatch({ type: "snapshot_mode_changed", mode }),
    setRowLimit: (rowLimit: number) => dispatch({ type: "row_limit_changed", rowLimit }),
  };
}

export function useNotebookSourceImport({
  notebookId,
  notebook,
  flushPendingSaves,
  mutateWithResult,
  onCreated,
  onClose,
}: {
  notebookId: string;
  notebook: WebNotebook | null;
  flushPendingSaves: () => Promise<void>;
  mutateWithResult: (operation: () => Promise<WebNotebook>) => Promise<WebNotebook | null>;
  onCreated: (cellId: string) => void;
  onClose: () => void;
}) {
  const configureCellSource = useCallback(
    async (
      cellId: string,
      input: { connection?: string; snapshot_mode?: "full" | "sample"; row_limit?: number },
    ) => {
      await flushPendingSaves();
      await mutateWithResult(() => configureNotebookCellSource(notebookId, cellId, input));
    },
    [flushPendingSaves, mutateWithResult, notebookId],
  );

  const createDataSource = useCallback(
    async (input: NotebookDataSourceInput) => {
      if (!notebook) return;
      await flushPendingSaves();
      const existing = new Set(
        notebook.cells.map((cell) => cell.cell_id).filter((id): id is string => Boolean(id)),
      );
      const updated = await mutateWithResult(() => {
        if (input.kind === "warehouse") {
          return createNotebookWarehouseSource(notebookId, {
            connection: input.connection,
            query: `select * from ${input.relation}\n`,
            snapshot_mode: input.snapshotMode,
            row_limit: input.rowLimit,
          });
        }
        return createNotebookSource(
          notebookId,
          input.kind === "file"
            ? {
                kind: "file",
                connection: input.connection,
                uri: input.uri,
                format: input.format,
                snapshot: { mode: input.snapshotMode, row_limit: input.rowLimit },
              }
            : {
                kind: "http",
                request: { url: input.url, method: input.method, body: input.body },
                response: { records_path: input.recordsPath },
                snapshot: { mode: input.snapshotMode, row_limit: input.rowLimit },
              },
        );
      });
      const created = updated?.cells.find((cell) => cell.cell_id && !existing.has(cell.cell_id));
      if (created?.cell_id) onCreated(created.cell_id);
      onClose();
    },
    [flushPendingSaves, mutateWithResult, notebook, notebookId, onClose, onCreated],
  );

  return { configureCellSource, createDataSource };
}

export function isDuckDBNotebookConnection(
  connection: string,
  assetType: string | undefined,
  queryConnections: WorkspaceQueryConnection[],
) {
  const normalized = connection.trim().toLowerCase();
  const configured = queryConnections.find(
    (candidate) => candidate.name.trim().toLowerCase() === normalized,
  );
  return (
    configured?.connection_type.trim().toLowerCase() === "duckdb" ||
    configured?.asset_type.trim().toLowerCase().startsWith("duckdb.") === true ||
    assetType?.trim().toLowerCase().startsWith("duckdb.") === true
  );
}

export function notebookSourceRequiresImportReview(
  cell: WebAsset,
  queryConnections: WorkspaceQueryConnection[],
) {
  const source = cell.notebook_source;
  if (!source) {
    const connection = cell.connection?.trim() ?? "";
    return (
      Boolean(connection) && !isDuckDBNotebookConnection(connection, cell.type, queryConnections)
    );
  }
  const connection = source.connection?.trim() ?? "";
  if (connection) {
    return !isDuckDBNotebookConnection(connection, cell.type, queryConnections);
  }
  if (source.kind === "http" || source.kind === "object" || source.kind === "object_storage") {
    return true;
  }
  const uri = source.uri?.trim().toLowerCase() ?? "";
  return uri.includes("://") && !uri.startsWith("file://");
}
