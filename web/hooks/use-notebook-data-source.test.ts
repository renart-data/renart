import { describe, expect, it } from "vitest";

import type { WebAsset, WorkspaceQueryConnection } from "@/lib/types";

import {
  createNotebookDataSourceFormState,
  isDuckDBNotebookConnection,
  notebookDataSourceCanSubmit,
  notebookDataSourceFormReducer,
  notebookDataSourceInput,
  notebookSourceRequiresImportReview,
} from "./use-notebook-data-source";

const queryConnections = [
  {
    name: "duckdb-local",
    connection_type: "duckdb",
    asset_type: "duckdb.sql",
    dialect: "duckdb",
  },
  {
    name: "postgres-other",
    connection_type: "postgres",
    asset_type: "pg.sql",
    dialect: "postgres",
  },
] as WorkspaceQueryConnection[];

describe("notebook data source model", () => {
  it("resets the dialog to a clean warehouse source", () => {
    const dirty = {
      ...createNotebookDataSourceFormState("old"),
      kind: "http" as const,
      requestURL: "https://example.test",
      creating: true,
      error: "failed",
    };
    const state = notebookDataSourceFormReducer(dirty, {
      type: "dialog_opened",
      defaultConnection: "postgres-other",
    });
    expect(state).toEqual(createNotebookDataSourceFormState("postgres-other"));
  });

  it("keeps table discovery transitions and relation selection deterministic", () => {
    let state = createNotebookDataSourceFormState("postgres-other");
    state = notebookDataSourceFormReducer(state, {
      type: "tables_load_started",
    });
    expect(state).toMatchObject({ loading: true, error: "" });
    state = notebookDataSourceFormReducer(state, {
      type: "tables_loaded",
      tables: [{ name: "public.accounts", short_name: "accounts" }],
    });
    state = notebookDataSourceFormReducer(state, {
      type: "relation_changed",
      relation: "public.accounts",
    });
    state = notebookDataSourceFormReducer(state, {
      type: "query_connection_changed",
      connection: "duckdb-local",
    });
    expect(state).toMatchObject({
      connection: "duckdb-local",
      relation: "",
      loading: false,
      tables: [{ name: "public.accounts", short_name: "accounts" }],
    });
  });

  it("builds normalized warehouse, file, and HTTP source requests", () => {
    const warehouse = {
      ...createNotebookDataSourceFormState("postgres-other"),
      relation: "  public.accounts  ",
      snapshotMode: "sample" as const,
      rowLimit: 250,
    };
    expect(notebookDataSourceCanSubmit(warehouse)).toBe(true);
    expect(notebookDataSourceInput(warehouse)).toEqual({
      kind: "warehouse",
      connection: "postgres-other",
      relation: "public.accounts",
      snapshotMode: "sample",
      rowLimit: 250,
    });

    const file = {
      ...createNotebookDataSourceFormState(),
      kind: "file" as const,
      fileURI: "  data/events.parquet ",
    };
    expect(notebookDataSourceInput(file)).toEqual({
      kind: "file",
      connection: undefined,
      uri: "data/events.parquet",
      format: undefined,
      snapshotMode: "full",
      rowLimit: undefined,
    });

    const http = {
      ...createNotebookDataSourceFormState(),
      kind: "http" as const,
      requestURL: " https://example.test/events ",
      requestMethod: "POST",
      requestBody: '{"active":true}',
      recordsPath: " data.items ",
    };
    expect(notebookDataSourceInput(http)).toEqual({
      kind: "http",
      url: "https://example.test/events",
      method: "POST",
      body: { active: true },
      recordsPath: "data.items",
      snapshotMode: "full",
      rowLimit: undefined,
    });
    expect(() => notebookDataSourceInput({ ...http, requestBody: "{" })).toThrow(
      "Request body must be valid JSON.",
    );
  });

  it("requires review only for imports outside the local DuckDB boundary", () => {
    expect(isDuckDBNotebookConnection("DUCKDB-LOCAL", undefined, queryConnections)).toBe(true);
    expect(isDuckDBNotebookConnection("postgres-other", undefined, queryConnections)).toBe(false);

    expect(
      notebookSourceRequiresImportReview(
        { connection: "duckdb-local", type: "duckdb.sql" } as WebAsset,
        queryConnections,
      ),
    ).toBe(false);
    expect(
      notebookSourceRequiresImportReview(
        { connection: "postgres-other", type: "pg.sql" } as WebAsset,
        queryConnections,
      ),
    ).toBe(true);
    expect(
      notebookSourceRequiresImportReview(
        { notebook_source: { kind: "http" } } as WebAsset,
        queryConnections,
      ),
    ).toBe(true);
    expect(
      notebookSourceRequiresImportReview(
        { notebook_source: { kind: "file", uri: "file://data/events.parquet" } } as WebAsset,
        queryConnections,
      ),
    ).toBe(false);
  });
});
