import { effectiveConnectionForAsset, SchemaColumn, SchemaTable } from "@/lib/sql-schema";
import { IngestrSuggestion, WebAsset, WorkspaceState } from "@/lib/types";

import {
  ConnectionSuggestionEntry,
  SuggestionCatalogState,
  SuggestionTableState,
} from "./suggestion-types";

export function mergeRemoteSuggestions(
  left: IngestrSuggestion[],
  right: IngestrSuggestion[],
): IngestrSuggestion[] {
  const merged = new Map<string, IngestrSuggestion>();

  for (const item of [...left, ...right]) {
    const key = normalizeTableName(item.value).toLowerCase();
    const current = merged.get(key);

    if (!current) {
      merged.set(key, item);
      continue;
    }

    merged.set(key, {
      value: current.value || item.value,
      kind: current.kind ?? item.kind,
      detail: current.detail ?? item.detail,
    });
  }

  return Array.from(merged.values()).sort((a, b) => a.value.localeCompare(b.value));
}

export function getConnectionSuggestions(
  catalog: SuggestionCatalogState,
): ConnectionSuggestionEntry[] {
  return catalog.connections.map((connection) => ({
    name: connection.name,
    type: connection.type,
    databaseName: connection.databaseName,
  }));
}

export function getDatabaseSuggestions(
  catalog: SuggestionCatalogState,
  options: {
    connectionName: string;
    environment?: string;
    prefix?: string;
  },
): string[] {
  const prefix = options.prefix?.trim().toLowerCase() ?? "";

  return catalog.databases
    .filter((database) => {
      if (database.connectionName !== options.connectionName) {
        return false;
      }

      const matchingSource = database.sources.some(
        (source) =>
          source.method === "connection-database-discovery" &&
          (!options.environment || source.environment === options.environment),
      );
      if (!matchingSource) {
        return false;
      }

      if (!prefix) {
        return true;
      }

      return database.name.toLowerCase().includes(prefix);
    })
    .map((database) => database.name)
    .sort((left, right) => left.localeCompare(right));
}

export function getSelectedAssetSuggestionTable(
  catalog: SuggestionCatalogState,
  assetId: string | null | undefined,
): SuggestionTableState | null {
  if (!assetId) {
    return null;
  }

  return catalog.tables.find((table) => table.assetId === assetId) ?? null;
}

export function getSelectedAssetColumnEntries(
  table: SuggestionTableState | null,
): Array<{ name?: string }> {
  return (table?.columns ?? []).map((column) => ({ name: column.name }));
}

export function getSelectedAssetInspectColumns(table: SuggestionTableState | null): string[] {
  if (!table) {
    return [];
  }

  return table.columns
    .filter((column) => column.sourceMethods.includes("asset-inspect"))
    .map((column) => column.name);
}

export function getSchemaSuggestionTablesForAsset(
  workspace: WorkspaceState | null,
  catalog: SuggestionCatalogState,
  asset: WebAsset | null,
): SuggestionTableState[] {
  if (!workspace || !asset) {
    return [];
  }

  const currentConnection = effectiveConnectionForAsset(asset);
  if (!currentConnection) {
    return [];
  }

  return catalog.tables.filter((table) => table.connectionName === currentConnection);
}

export function toSchemaTables(
  tables: SuggestionTableState[],
  currentAssetId?: string | null,
): SchemaTable[] {
  const schemaTables: SchemaTable[] = tables.map((table) => ({
    name: table.name,
    shortName: table.shortName,
    columns: table.columns
      .filter(
        (column) =>
          table.assetId !== currentAssetId ||
          !column.sourceMethods.every((method) => method === "asset-sql-definition"),
      )
      .map((column) => ({
        name: column.name,
        type: column.type,
        description: column.description,
        primaryKey: column.primaryKey,
        sourceMethods: column.sourceMethods,
      })),
    isWorkspaceAsset: table.isWorkspaceAsset,
    isMaterialized: table.isMaterialized,
    assetId: table.assetId,
    pipelineId: table.pipelineId,
    assetPath: table.assetPath,
    connectionName: table.connectionName ?? undefined,
    connectionType: table.connectionType ?? undefined,
    databaseName: table.databaseName ?? undefined,
    sourceMethods: table.sourceMethods,
  }));

  const externalTablesByConnection = new Map<
    string | undefined,
    {
      byName: Map<string, number[]>;
      byShortName: Map<string, number[]>;
    }
  >();
  schemaTables.forEach((table, index) => {
    if (table.isWorkspaceAsset) {
      return;
    }

    let connectionTables = externalTablesByConnection.get(table.connectionName);
    if (!connectionTables) {
      connectionTables = { byName: new Map(), byShortName: new Map() };
      externalTablesByConnection.set(table.connectionName, connectionTables);
    }
    appendTableIndex(connectionTables.byName, table.name, index);
    appendTableIndex(connectionTables.byShortName, table.shortName, index);
  });

  for (const table of schemaTables) {
    if (!table.isWorkspaceAsset) {
      continue;
    }

    const connectionTables = externalTablesByConnection.get(table.connectionName);
    if (!connectionTables) {
      continue;
    }
    const matchingIndexes = new Set([
      ...(connectionTables.byName.get(table.name.toLowerCase()) ?? []),
      ...(connectionTables.byShortName.get(table.shortName.toLowerCase()) ?? []),
    ]);
    // Preserve catalog order when multiple observations describe the same
    // relation; mergeSchemaColumns intentionally keeps the earliest value.
    for (const index of [...matchingIndexes].sort((left, right) => left - right)) {
      const externalTable = schemaTables[index];
      table.columns = mergeSchemaColumns(table.columns, externalTable.columns);
    }
  }

  return schemaTables;
}

function appendTableIndex(index: Map<string, number[]>, value: string, tableIndex: number) {
  const key = value.toLowerCase();
  index.set(key, [...(index.get(key) ?? []), tableIndex]);
}

function mergeSchemaColumns(left: SchemaColumn[], right: SchemaColumn[]) {
  const merged = new Map<string, SchemaColumn>();

  for (const column of [...left, ...right]) {
    const key = column.name.toLowerCase();
    const current = merged.get(key);
    if (!current) {
      merged.set(key, { ...column, sourceMethods: column.sourceMethods ?? [] });
      continue;
    }

    merged.set(key, {
      ...current,
      type: current.type || column.type,
      description: current.description || column.description,
      primaryKey: current.primaryKey || column.primaryKey,
      sourceMethods: mergeSourceMethods(current.sourceMethods, column.sourceMethods),
    });
  }

  return Array.from(merged.values());
}

function mergeSourceMethods(left?: string[], right?: string[]) {
  return Array.from(new Set([...(left ?? []), ...(right ?? [])].filter(Boolean)));
}

export function getIngestrTableSuggestionsFromCatalog(
  catalog: SuggestionCatalogState,
  options: {
    connectionName: string;
    environment?: string;
    prefix?: string;
  },
): IngestrSuggestion[] {
  const prefix = options.prefix?.trim().toLowerCase() ?? "";

  return catalog.tables
    .filter((table) => {
      if (table.isWorkspaceAsset || table.connectionName !== options.connectionName) {
        return false;
      }

      const matchingSource = table.sources.some(
        (source) =>
          source.method === "ingestr-suggestions" &&
          (!options.environment || source.environment === options.environment) &&
          doesIngestrSourceMatchPrefix(source.prefix, prefix),
      );

      if (!matchingSource) {
        return false;
      }

      if (!prefix) {
        return true;
      }

      return table.name.toLowerCase().includes(prefix);
    })
    .map((table) => ({
      value: table.name,
      kind: table.remoteSuggestionKind,
      detail: table.remoteSuggestionDetail,
    }))
    .sort((left, right) => left.value.localeCompare(right.value));
}

function normalizeTableName(value: string): string {
  return value.trim().replace(/^['"`]+|['"`]+$/g, "");
}

function doesIngestrSourceMatchPrefix(
  sourcePrefix: string | undefined,
  requestedPrefix: string,
): boolean {
  const normalizedSourcePrefix = sourcePrefix?.trim().toLowerCase() ?? "";

  if (requestedPrefix === "") {
    return normalizedSourcePrefix === "";
  }

  if (normalizedSourcePrefix === requestedPrefix) {
    return true;
  }

  if (requestedPrefix.endsWith("/")) {
    return false;
  }

  return normalizedSourcePrefix.endsWith("/") && requestedPrefix.startsWith(normalizedSourcePrefix);
}
