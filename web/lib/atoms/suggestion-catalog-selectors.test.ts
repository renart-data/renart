import { describe, expect, it } from "vitest";

import { toSchemaTables } from "./suggestion-catalog-selectors";
import type { SuggestionObservationMethod, SuggestionTableState } from "./suggestion-types";

function suggestionTable({
  key,
  name,
  shortName,
  connectionName,
  isWorkspaceAsset,
  columns,
}: {
  key: string;
  name: string;
  shortName: string;
  connectionName?: string;
  isWorkspaceAsset: boolean;
  columns: Array<{ name: string; type?: string; source: SuggestionObservationMethod }>;
}): SuggestionTableState {
  const tableSource: SuggestionObservationMethod = isWorkspaceAsset
    ? "workspace-load"
    : "connection-table-discovery";
  return {
    key,
    name,
    shortName,
    connectionName,
    assetId: isWorkspaceAsset ? key : undefined,
    isWorkspaceAsset,
    sourceMethods: [tableSource],
    sources: [{ method: tableSource, recordedAt: "2026-09-01T00:00:00Z" }],
    columns: columns.map((column) => ({
      key: `${key}:${column.name}`,
      name: column.name,
      type: column.type,
      tableKey: key,
      sourceMethods: [column.source],
      sources: [{ method: column.source, recordedAt: "2026-09-01T00:00:00Z" }],
    })),
  };
}

describe("toSchemaTables", () => {
  it("merges matching remote columns on the same connection", () => {
    const tables = toSchemaTables([
      suggestionTable({
        key: "workspace-orders",
        name: "analytics.orders",
        shortName: "orders",
        connectionName: "warehouse",
        isWorkspaceAsset: true,
        columns: [{ name: "order_id", source: "asset-sql-definition" }],
      }),
      suggestionTable({
        key: "remote-orders",
        name: "ANALYTICS.ORDERS",
        shortName: "ORDERS",
        connectionName: "warehouse",
        isWorkspaceAsset: false,
        columns: [
          { name: "order_id", type: "INTEGER", source: "connection-column-discovery" },
          { name: "amount", type: "DOUBLE", source: "connection-column-discovery" },
        ],
      }),
    ]);

    expect(tables[0].columns).toEqual([
      {
        name: "order_id",
        type: "INTEGER",
        description: undefined,
        primaryKey: undefined,
        sourceMethods: ["asset-sql-definition", "connection-column-discovery"],
      },
      {
        name: "amount",
        type: "DOUBLE",
        description: undefined,
        primaryKey: undefined,
        sourceMethods: ["connection-column-discovery"],
      },
    ]);
  });

  it("matches unqualified names without mixing connections", () => {
    const tables = toSchemaTables([
      suggestionTable({
        key: "workspace-orders",
        name: "catalog.analytics.orders",
        shortName: "orders",
        connectionName: "primary",
        isWorkspaceAsset: true,
        columns: [],
      }),
      suggestionTable({
        key: "primary-orders",
        name: "public.orders",
        shortName: "orders",
        connectionName: "primary",
        isWorkspaceAsset: false,
        columns: [{ name: "primary_column", source: "connection-column-discovery" }],
      }),
      suggestionTable({
        key: "secondary-orders",
        name: "catalog.analytics.orders",
        shortName: "orders",
        connectionName: "secondary",
        isWorkspaceAsset: false,
        columns: [{ name: "secondary_column", source: "connection-column-discovery" }],
      }),
    ]);

    expect(tables[0].columns.map((column) => column.name)).toEqual(["primary_column"]);
  });

  it("keeps catalog order when duplicate remote observations match", () => {
    const tables = toSchemaTables([
      suggestionTable({
        key: "workspace-orders",
        name: "analytics.orders",
        shortName: "orders",
        connectionName: "warehouse",
        isWorkspaceAsset: true,
        columns: [],
      }),
      suggestionTable({
        key: "first-orders",
        name: "analytics.orders",
        shortName: "orders",
        connectionName: "warehouse",
        isWorkspaceAsset: false,
        columns: [{ name: "order_id", type: "INTEGER", source: "connection-column-discovery" }],
      }),
      suggestionTable({
        key: "second-orders",
        name: "analytics.orders",
        shortName: "orders",
        connectionName: "warehouse",
        isWorkspaceAsset: false,
        columns: [
          { name: "order_id", type: "BIGINT", source: "connection-column-discovery" },
          { name: "amount", type: "DOUBLE", source: "connection-column-discovery" },
        ],
      }),
    ]);

    expect(tables[0].columns.map(({ name, type }) => ({ name, type }))).toEqual([
      { name: "order_id", type: "INTEGER" },
      { name: "amount", type: "DOUBLE" },
    ]);
  });
});
