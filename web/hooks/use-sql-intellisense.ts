"use client";

import { useEffect, useMemo, useRef } from "react";
import type * as MonacoNS from "monaco-editor";

import { getSQLPathSuggestions } from "@/lib/api-sql-discovery";
import {
  registerSQLProviders,
  resolveTableAtPosition,
  RemoteSQLResolver,
  SQLProviderEntry,
} from "@/lib/monaco-sql-providers";
import {
  effectiveConnectionForAsset,
  effectiveConnectionTypeForAsset,
  findTableByIdentifier,
  SchemaTable,
} from "@/lib/sql-schema";
import { SqlParseContextDiagnostic, WebAsset, WorkspaceState } from "@/lib/types";
import { useAtomValue, useSetAtom } from "jotai";

import { workspaceAtom } from "@/lib/atoms/domains/workspace";
import {
  sqlDiscoveryCacheAtom,
  SQLDiscoveryCacheState,
  sqlDiscoveryColumnsAtom,
  sqlDiscoveryTablesAtom,
} from "@/lib/atoms/sql-discovery";
import { useSQLParseContext } from "@/hooks/use-sql-parse-context";
import { useSQLSemanticDecorations } from "@/hooks/use-sql-semantic-decorations";
import { fetchJSON } from "@/lib/api-core";
import { buildInspectDiagnosticMarker, InspectDiagnosticSnapshot } from "@/lib/inspect-diagnostics";

function buildValueSuggestionQuery(
  connectionType: string | null,
  quotedTable: string,
  quotedColumn: string,
  trimmedPrefix: string,
) {
  const escapedPrefix = trimmedPrefix.replaceAll("'", "''");

  if (connectionType !== "duckdb") {
    return "";
  }
  return trimmedPrefix
    ? `select distinct ${quotedColumn} as value from ${quotedTable} where lower(cast(${quotedColumn} as varchar)) like lower('%${escapedPrefix}%') order by 1 limit 10`
    : `select distinct ${quotedColumn} as value from ${quotedTable} order by 1 limit 10`;
}

function quoteSQLIdentifier(identifier: string) {
  return identifier
    .split(".")
    .map(
      (part) =>
        `"${part
          .trim()
          .replace(/^[[\]"'`]+|[[\]"'`]+$/g, "")
          .replaceAll('"', '""')}"`,
    )
    .join(".");
}

function schemaNameFromAssetName(name?: string | null) {
  if (!name) {
    return null;
  }

  const parts = name
    .split(".")
    .map((part) => part.trim().replace(/^['"`]+|['"`]+$/g, ""))
    .filter(Boolean);

  if (parts.length < 2) {
    return null;
  }

  return parts[parts.length - 2] ?? null;
}

type UnresolvedColumnSuggestion = {
  diagnostic: SqlParseContextDiagnostic;
  unknownColumn: string;
  replacementColumn: string;
  availableColumns: string[];
  tableIdentifier: string | null;
  range: MonacoNS.IRange;
};

type UnresolvedTableSuggestion = {
  diagnostic: SqlParseContextDiagnostic;
  unknownTable: string;
  replacementTable: string;
  replacementText: string;
  range: MonacoNS.IRange;
};

type SelfReferenceDiagnostic = {
  message: string;
  range: MonacoNS.IRange;
};

type ColumnSuggestionContext = {
  columns: string[];
  tableIdentifier: string | null;
};

type ParseContextLike =
  | {
      tables?: Array<{
        name: string;
        alias?: string;
        resolved_name?: string;
        columns?: Record<string, string>;
      }>;
    }
  | null
  | undefined;

type SQLProviderState = {
  asset: WebAsset | null;
  activeParseContext: ReturnType<typeof useSQLParseContext>;
  connectionName: string | null;
  environment?: string;
  sqlDiscoveryCache: SQLDiscoveryCacheState;
  workspace: WorkspaceState | null;
};

function useStableSQLProviderState(args: {
  activeParseContext: ReturnType<typeof useSQLParseContext>;
  asset: WebAsset | null;
  connectionName: string | null;
  currentPipelineId: string | null;
  environment?: string;
  onGoToAsset?: (pipelineId: string, assetId: string) => void;
  remoteTableNames: string[];
  sqlDiscoveryCache: SQLDiscoveryCacheState;
  tables: SchemaTable[];
  upstreamNames: string[];
  workspace: WorkspaceState | null;
}) {
  const goToAssetRef = useRef(args.onGoToAsset);
  const providerTablesRef = useRef<SchemaTable[]>([]);
  const providerUpstreamNamesRef = useRef<string[]>([]);
  const providerTableContextRef = useRef({
    currentPipelineId: null as string | null,
    currentSchemaName: null as string | null,
    currentTableName: null as string | null,
    remoteTableNames: [] as string[],
  });
  const latestStateRef = useRef<SQLProviderState>({
    asset: args.asset,
    activeParseContext: args.activeParseContext,
    connectionName: args.connectionName,
    environment: args.environment,
    sqlDiscoveryCache: args.sqlDiscoveryCache,
    workspace: args.workspace,
  });

  goToAssetRef.current = args.onGoToAsset;
  replaceStableArray(providerTablesRef.current, args.tables);
  replaceStableArray(providerUpstreamNamesRef.current, args.upstreamNames);
  replaceStableArray(providerTableContextRef.current.remoteTableNames, args.remoteTableNames);
  providerTableContextRef.current.currentPipelineId = args.currentPipelineId;
  providerTableContextRef.current.currentSchemaName = schemaNameFromAssetName(args.asset?.name);
  providerTableContextRef.current.currentTableName = args.asset?.name ?? null;
  latestStateRef.current = {
    asset: args.asset,
    activeParseContext: args.activeParseContext,
    connectionName: args.connectionName,
    environment: args.environment,
    sqlDiscoveryCache: args.sqlDiscoveryCache,
    workspace: args.workspace,
  };

  return {
    goToAssetRef,
    latestStateRef,
    providerTableContextRef,
    providerTablesRef,
    providerUpstreamNamesRef,
  };
}

function replaceStableArray<T>(target: T[], source: T[]) {
  target.splice(0, target.length, ...source);
}

function normalizeIdentifierPart(value: string) {
  return value.trim().replace(/^[`"']+|[`"']+$/g, "");
}

function extractUnresolvedColumnName(message: string) {
  const match = message.match(
    /unresolved\s+column\s*:\s*((?:[`"']?[\w$]+[`"']?\.)?[`"']?[\w$]+[`"']?)/i,
  );
  const identifier = match ? normalizeIdentifierPart(match[1]) : null;
  return identifier?.split(".").at(-1) ?? null;
}

function extractUnresolvedTableName(message: string) {
  const match = message.match(/unresolved\s+table(?:\s+or\s+alias)?\s*:\s*([^\s,;)]+)/i);
  return match ? normalizeIdentifierPart(match[1]) : null;
}

function formatUnresolvedColumnMessage(unknownColumn: string, replacementColumn: string) {
  return `Unresolved column '${unknownColumn}'. Did you mean '${replacementColumn}'?`;
}

function formatUnresolvedTableMessage(unknownTable: string, replacementTable: string) {
  return `Unresolved table '${unknownTable}'. Did you mean '${replacementTable}'?`;
}

function levenshteinDistance(left: string, right: string) {
  const rows = left.length + 1;
  const columns = right.length + 1;
  const matrix = Array.from({ length: rows }, () => new Array<number>(columns).fill(0));

  for (let row = 0; row < rows; row += 1) {
    matrix[row][0] = row;
  }
  for (let column = 0; column < columns; column += 1) {
    matrix[0][column] = column;
  }

  for (let row = 1; row < rows; row += 1) {
    for (let column = 1; column < columns; column += 1) {
      const cost = left[row - 1] === right[column - 1] ? 0 : 1;
      matrix[row][column] = Math.min(
        matrix[row - 1][column] + 1,
        matrix[row][column - 1] + 1,
        matrix[row - 1][column - 1] + cost,
      );
    }
  }

  return matrix[left.length][right.length];
}

function findSimilarColumn(unknownColumn: string, columns: string[]) {
  const normalizedUnknown = unknownColumn.toLowerCase();
  const maxDistance = Math.max(2, Math.floor(normalizedUnknown.length / 3));

  let best: { column: string; distance: number } | null = null;
  for (const column of columns) {
    const distance = levenshteinDistance(normalizedUnknown, column.toLowerCase());
    if (distance > maxDistance) {
      continue;
    }
    if (
      !best ||
      distance < best.distance ||
      (distance === best.distance && column.length < best.column.length)
    ) {
      best = { column, distance };
    }
  }

  return best?.column ?? null;
}

function findSimilarTable(unknownTable: string, tables: SchemaTable[]) {
  const normalizedUnknown = unknownTable.toLowerCase();
  const unknownShortName = normalizedUnknown.split(".").at(-1) ?? normalizedUnknown;
  const maxDistance = Math.max(2, Math.floor(unknownShortName.length / 3));

  let best: { table: SchemaTable; distance: number; candidateLength: number } | null = null;
  for (const table of tables) {
    const candidates = [table.name, table.shortName];
    for (const candidate of candidates) {
      const normalizedCandidate = candidate.toLowerCase();
      const candidateShortName = normalizedCandidate.split(".").at(-1) ?? normalizedCandidate;
      const directDistance = levenshteinDistance(normalizedUnknown, normalizedCandidate);
      const shortDistance = levenshteinDistance(unknownShortName, candidateShortName);
      const distance = Math.min(directDistance, shortDistance);
      if (distance > maxDistance) {
        continue;
      }
      if (
        !best ||
        distance < best.distance ||
        (distance === best.distance && candidate.length < best.candidateLength)
      ) {
        best = { table, distance, candidateLength: candidate.length };
      }
    }
  }

  return best?.table.name ?? null;
}

function buildAliasColumnMap(parseContext: ParseContextLike, tables: SchemaTable[]) {
  const aliasColumns = new Map<string, ColumnSuggestionContext>();
  for (const tableEntry of parseContext?.tables ?? []) {
    const tableName = tableEntry.resolved_name ?? tableEntry.name;
    const table = tables.find(
      (candidate) =>
        candidate.name.toLowerCase() === tableName.toLowerCase() ||
        candidate.shortName.toLowerCase() === tableName.toLowerCase(),
    );
    const columns = table?.columns.length
      ? table.columns.map((column) => column.name)
      : Object.keys(tableEntry.columns ?? {});
    if (columns.length === 0) {
      continue;
    }

    const tableIdentifier = table?.shortName ?? tableEntry.resolved_name ?? tableEntry.name;
    const context = { columns, tableIdentifier };
    aliasColumns.set(tableEntry.name.toLowerCase(), context);
    if (table) {
      aliasColumns.set(table.name.toLowerCase(), context);
      aliasColumns.set(table.shortName.toLowerCase(), context);
    }
    if (tableEntry.alias) {
      aliasColumns.set(tableEntry.alias.toLowerCase(), context);
    }
  }

  return aliasColumns;
}

function columnContextForDiagnostic(
  model: MonacoNS.editor.ITextModel,
  diagnostic: SqlParseContextDiagnostic,
  parseContext: ParseContextLike,
  tables: SchemaTable[],
): ColumnSuggestionContext {
  const range = diagnostic.range;
  if (!range) {
    return { columns: [], tableIdentifier: null };
  }

  const aliasColumnMap = buildAliasColumnMap(parseContext, tables);
  const linePrefix = model.getValueInRange({
    startLineNumber: range.line,
    startColumn: 1,
    endLineNumber: range.line,
    endColumn: range.col,
  });
  const qualifier = linePrefix.match(/([`"']?[\w$]+[`"']?)\.\s*$/)?.[1];
  if (qualifier) {
    return (
      aliasColumnMap.get(normalizeIdentifierPart(qualifier).toLowerCase()) ?? {
        columns: [],
        tableIdentifier: null,
      }
    );
  }

  return {
    columns: Array.from(
      new Set(tables.flatMap((table) => table.columns.map((column) => column.name))),
    ),
    tableIdentifier: null,
  };
}

function buildUnresolvedColumnSuggestions(
  model: MonacoNS.editor.ITextModel,
  diagnostics: SqlParseContextDiagnostic[],
  parseContext: ParseContextLike,
  tables: SchemaTable[],
): UnresolvedColumnSuggestion[] {
  return diagnostics.flatMap((diagnostic) => {
    if (!diagnostic.range) {
      return [];
    }

    const unknownColumn = extractUnresolvedColumnName(diagnostic.message);
    if (!unknownColumn) {
      return [];
    }

    const columnContext = columnContextForDiagnostic(model, diagnostic, parseContext, tables);
    const replacementColumn = findSimilarColumn(unknownColumn, columnContext.columns);
    if (!replacementColumn) {
      return [];
    }

    return [
      {
        diagnostic,
        unknownColumn,
        replacementColumn,
        availableColumns: columnContext.columns,
        tableIdentifier: columnContext.tableIdentifier,
        range: {
          startLineNumber: diagnostic.range.line,
          startColumn: diagnostic.range.col,
          endLineNumber: diagnostic.range.end_line,
          endColumn: diagnostic.range.end_col,
        },
      },
    ];
  });
}

function buildUnresolvedTableSuggestions(
  model: MonacoNS.editor.ITextModel,
  diagnostics: SqlParseContextDiagnostic[],
  tables: SchemaTable[],
): UnresolvedTableSuggestion[] {
  return diagnostics.flatMap((diagnostic) => {
    if (!diagnostic.range) {
      return [];
    }

    const unknownTable = extractUnresolvedTableName(diagnostic.message);
    if (!unknownTable || unknownTable.includes("/")) {
      return [];
    }

    const replacementTable = findSimilarTable(unknownTable, tables);
    const replacementSchemaTable = replacementTable
      ? findTableByIdentifier(tables, replacementTable)
      : null;
    if (!replacementTable || !replacementSchemaTable) {
      return [];
    }

    const range = {
      startLineNumber: diagnostic.range.line,
      startColumn: diagnostic.range.col,
      endLineNumber: diagnostic.range.end_line,
      endColumn: diagnostic.range.end_col,
    };
    const diagnosticText = model.getValueInRange(range);
    const replacementText = diagnosticText.includes(".")
      ? replacementTable
      : replacementSchemaTable.shortName;

    return [
      {
        diagnostic,
        unknownTable,
        replacementTable,
        replacementText,
        range,
      },
    ];
  });
}

function buildSelfReferenceDiagnostics(
  asset: WebAsset | null,
  parseContext:
    | {
        tables?: Array<{
          name: string;
          resolved_name?: string;
          parts?: Array<{ kind: string; range: SqlParseContextDiagnostic["range"] }>;
        }>;
      }
    | null
    | undefined,
): SelfReferenceDiagnostic[] {
  if (!asset?.name) {
    return [];
  }

  const assetName = asset.name.toLowerCase();
  const assetShortName = assetName.split(".").at(-1) ?? assetName;
  const diagnostics: SelfReferenceDiagnostic[] = [];
  for (const table of parseContext?.tables ?? []) {
    const tableName = (table.resolved_name || table.name || "").toLowerCase();
    const tableShortName = tableName.split(".").at(-1) ?? tableName;
    if (tableName !== assetName && tableShortName !== assetShortName) {
      continue;
    }

    const range = table.parts?.findLast((part) => part.kind === "table")?.range;
    if (!range) {
      continue;
    }
    diagnostics.push({
      message: `Circular dependency: asset '${asset.name}' references itself.`,
      range: {
        startLineNumber: range.line,
        startColumn: range.col,
        endLineNumber: range.end_line,
        endColumn: range.end_col,
      },
    });
  }

  return diagnostics;
}

function positionInRange(position: MonacoNS.Position, range: MonacoNS.IRange) {
  if (position.lineNumber < range.startLineNumber || position.lineNumber > range.endLineNumber) {
    return false;
  }
  if (position.lineNumber === range.startLineNumber && position.column < range.startColumn) {
    return false;
  }
  if (position.lineNumber === range.endLineNumber && position.column > range.endColumn) {
    return false;
  }
  return true;
}

function remoteTablesForConnection(
  tablesByScope: Record<string, Array<{ name: string; short_name: string }>>,
  connectionName: string | null,
  environment?: string,
) {
  if (!connectionName) {
    return [];
  }

  return tablesByScope[`${connectionName}::${environment ?? ""}`] ?? [];
}

// The "sql" providers are global per Monaco, so they must be registered once
// and shared — not once per editor. Each editor registers its per-model state
// in `sqlProviderEntries`; the providers resolve the entry for the model they
// are invoked on. Without this, N mounted cell editors register N provider
// sets, multiplying suggestions and leaking one cell's tables into another.
const sqlProviderEntries = new Map<string, SQLProviderEntry>();
const sqlProviderRegistry = new Map<
  typeof MonacoNS,
  { disposable: MonacoNS.IDisposable; refs: number }
>();

function acquireSQLProviders(monaco: typeof MonacoNS): () => void {
  let registration = sqlProviderRegistry.get(monaco);
  if (!registration) {
    registration = {
      disposable: registerSQLProviders(
        monaco,
        (model) => sqlProviderEntries.get(model.uri.toString()) ?? null,
      ),
      refs: 0,
    };
    sqlProviderRegistry.set(monaco, registration);
  }
  registration.refs += 1;
  return () => {
    const current = sqlProviderRegistry.get(monaco);
    if (!current) {
      return;
    }
    current.refs -= 1;
    if (current.refs <= 0) {
      current.disposable.dispose();
      sqlProviderRegistry.delete(monaco);
    }
  };
}

/**
 * React hook that registers Monaco SQL completion / definition / hover
 * providers scoped to the given schema tables.
 *
 * Providers keep stable identities while reading latest schema state from refs.
 * Re-registering completion providers while the suggest widget is open resets
 * Monaco's focused completion item on every workspace SSE event.
 */
export function useSQLIntellisense(
  monaco: typeof MonacoNS | null,
  editor: MonacoNS.editor.IStandaloneCodeEditor | null,
  asset: WebAsset | null,
  sqlContent: string,
  tables: SchemaTable[],
  upstreamNames: string[],
  environment?: string,
  onGoToAsset?: (pipelineId: string, assetId: string) => void,
  inspectDiagnosticSnapshot?: InspectDiagnosticSnapshot | null,
  options?: {
    registerGlobalProviders?: boolean;
    registerLegacyDiagnosticProviders?: boolean;
    registerParseContextMarkers?: boolean;
    registerSemanticDecorations?: boolean;
  },
) {
  const workspace = useAtomValue(workspaceAtom);
  const sqlDiscoveryCache = useAtomValue(sqlDiscoveryCacheAtom);
  const loadSQLDiscoveryColumns = useSetAtom(sqlDiscoveryColumnsAtom);
  const loadSQLDiscoveryTables = useSetAtom(sqlDiscoveryTablesAtom);
  const connectionName = asset && workspace ? effectiveConnectionForAsset(asset) : null;
  const parseContext = useSQLParseContext(asset, sqlContent, tables, connectionName ?? undefined);
  const lastGoodParseContextRef = useRef<typeof parseContext>(null);
  const parseContextHasErrors = !!parseContext?.errors?.length;
  const parseContextHasRangedDiagnostics = !!parseContext?.diagnostics?.some(
    (diagnostic) => diagnostic.range,
  );
  if (parseContext && !parseContextHasErrors) {
    lastGoodParseContextRef.current = parseContext;
  }
  const activeParseContext =
    parseContext && (!parseContextHasErrors || parseContextHasRangedDiagnostics)
      ? parseContext
      : lastGoodParseContextRef.current;
  const parseContextKey = useMemo(
    () => JSON.stringify(activeParseContext ?? null),
    [activeParseContext],
  );
  const registerGlobalProviders = options?.registerGlobalProviders ?? true;
  const registerLegacyDiagnosticProviders = options?.registerLegacyDiagnosticProviders ?? true;
  const registerParseContextMarkers = options?.registerParseContextMarkers ?? true;
  const registerSemanticDecorations = options?.registerSemanticDecorations ?? true;

  useSQLSemanticDecorations(registerSemanticDecorations ? editor : null, activeParseContext);

  const currentPipelineId = asset
    ? ((workspace?.pipelines ?? []).find((pipeline) =>
        pipeline.assets.some((candidate) => candidate.id === asset.id),
      )?.id ?? null)
    : null;
  const remoteTableNames = remoteTablesForConnection(
    sqlDiscoveryCache.tablesByScope,
    connectionName,
    environment,
  ).map((table) => table.name);

  const {
    goToAssetRef,
    latestStateRef,
    providerTableContextRef,
    providerTablesRef,
    providerUpstreamNamesRef,
  } = useStableSQLProviderState({
    activeParseContext,
    asset,
    connectionName,
    currentPipelineId,
    environment,
    onGoToAsset,
    remoteTableNames,
    sqlDiscoveryCache,
    tables,
    upstreamNames,
    workspace,
  });

  useEffect(() => {
    if (!registerGlobalProviders || !monaco || !editor) {
      return;
    }
    const model = editor.getModel();
    if (!model) {
      return;
    }

    const remoteResolver: RemoteSQLResolver = {
      getParseContext: () => {
        return latestStateRef.current.activeParseContext;
      },
      async provideTableContextSuggestions({ monaco: monacoInstance, prefix, range }) {
        const { connectionName, environment, sqlDiscoveryCache } = latestStateRef.current;
        if (!connectionName) {
          return [];
        }

        let remoteTables =
          sqlDiscoveryCache.tablesByScope[`${connectionName}::${environment ?? ""}`];
        try {
          remoteTables ??= await loadSQLDiscoveryTables({
            connection: connectionName,
            environment,
          });
        } catch {
          return [];
        }

        const normalizedPrefix = prefix.trim().toLowerCase();

        return (remoteTables ?? [])
          .filter((table) => {
            if (!normalizedPrefix) {
              return true;
            }

            return (
              table.name.toLowerCase().includes(normalizedPrefix) ||
              table.short_name.toLowerCase().includes(normalizedPrefix)
            );
          })
          .filter((table) => {
            return !providerTablesRef.current.some(
              (candidate) => candidate.name.toLowerCase() === table.name.toLowerCase(),
            );
          })
          .map((table) => {
            const matchingAsset = providerTablesRef.current.find(
              (candidate) => candidate.name.toLowerCase() === table.name.toLowerCase(),
            );
            const description = matchingAsset
              ? `Remote table + asset (${matchingAsset.assetPath ?? matchingAsset.name})`
              : "Remote table";

            const currentSchemaName =
              schemaNameFromAssetName(latestStateRef.current.asset?.name)?.toLowerCase() ?? null;
            const tableSchemaName = schemaNameFromAssetName(table.name)?.toLowerCase() ?? null;
            const sameSchema = Boolean(currentSchemaName) && currentSchemaName === tableSchemaName;
            const sameAssetName =
              latestStateRef.current.asset?.name?.toLowerCase() === table.name.toLowerCase();
            const rank = sameAssetName ? "20" : sameSchema ? "21" : "22";

            return {
              label: {
                label: table.name,
                description,
              },
              kind: monacoInstance.languages.CompletionItemKind.Struct,
              detail: description,
              insertText: table.name,
              range,
              sortText: `${rank}${table.name.toLowerCase()}`,
            };
          });
      },
      async provideColumnSuggestions({
        monaco: monacoInstance,
        tableIdentifier,
        columnPrefix,
        range,
      }) {
        const { connectionName, environment, sqlDiscoveryCache } = latestStateRef.current;
        if (!connectionName) {
          return [];
        }

        const normalizedIdentifier = tableIdentifier.trim().toLowerCase();
        const localTable = providerTablesRef.current.find(
          (table) =>
            table.name.toLowerCase() === normalizedIdentifier ||
            table.shortName.toLowerCase() === normalizedIdentifier,
        );

        const remoteTableName = localTable?.name ?? tableIdentifier;

        let columns =
          sqlDiscoveryCache.columnsByScope[
            `${connectionName}::${environment ?? ""}::${remoteTableName.toLowerCase()}`
          ];
        try {
          columns ??= await loadSQLDiscoveryColumns({
            connection: connectionName,
            table: remoteTableName,
            environment,
          });
        } catch {
          return [];
        }

        const normalizedPrefix = columnPrefix.trim().toLowerCase();

        return (columns ?? [])
          .filter((column) => {
            if (!normalizedPrefix) {
              return true;
            }

            return column.name.toLowerCase().includes(normalizedPrefix);
          })
          .map((column) => ({
            label: column.name,
            kind: monacoInstance.languages.CompletionItemKind.Field,
            detail: column.type
              ? `${remoteTableName}.${column.name} (${column.type})`
              : `${remoteTableName}.${column.name}`,
            insertText: column.name,
            range,
            sortText: "1",
          }));
      },
      async provideColumnValueSuggestions({
        monaco: monacoInstance,
        tableIdentifier,
        columnName,
        prefix,
        range,
        insideQuotes,
      }) {
        const { activeParseContext, asset, connectionName, environment, workspace } =
          latestStateRef.current;
        const connectionType =
          asset && workspace ? effectiveConnectionTypeForAsset(workspace, asset) : null;
        if (!connectionName || !activeParseContext || connectionType !== "duckdb") {
          return [];
        }

        const matchingColumn = (activeParseContext.columns ?? []).find((column) => {
          const columnPart = column.parts.findLast((part) => part.kind === "column");
          return (
            column.qualifier?.toLowerCase() === tableIdentifier.toLowerCase() &&
            columnPart?.name.toLowerCase() === columnName.toLowerCase() &&
            column.resolved_table
          );
        });

        const resolvedTable = matchingColumn?.resolved_table;
        if (!resolvedTable) {
          return [];
        }

        const trimmedPrefix = prefix.trim();
        const quotedTable = quoteSQLIdentifier(resolvedTable);
        const quotedColumn = quoteSQLIdentifier(columnName);
        const valueQuery = buildValueSuggestionQuery(
          connectionType,
          quotedTable,
          quotedColumn,
          trimmedPrefix,
        );
        if (!valueQuery) {
          return [];
        }

        try {
          const payload = await fetchJSON<{
            values?: Array<string | number | boolean | null>;
          }>(`/api/sql/column-values`, {
            method: "POST",
            headers: {
              "Content-Type": "application/json",
            },
            cache: "no-store",
            body: JSON.stringify({
              connection: connectionName,
              environment: environment ?? "",
              query: valueQuery,
            }),
          });

          return (payload.values ?? []).map((value, index) => ({
            label: String(value ?? "NULL"),
            kind: monacoInstance.languages.CompletionItemKind.Value,
            detail: `${resolvedTable}.${columnName}`,
            insertText:
              typeof value === "string"
                ? insideQuotes
                  ? String(value).replaceAll("'", "''")
                  : `'${String(value).replaceAll("'", "''")}'`
                : String(value ?? "NULL"),
            range,
            sortText: `0${index}`,
          }));
        } catch {
          return [];
        }
      },
      async providePathSuggestions({ monaco: monacoInstance, prefix, range }) {
        const { asset, environment } = latestStateRef.current;
        if (!asset?.id) {
          return [];
        }

        let response;
        try {
          response = await getSQLPathSuggestions({
            assetId: asset.id,
            prefix,
            environment,
          });
        } catch {
          return [];
        }

        return response.suggestions.map((suggestion) => ({
          label: suggestion.value,
          kind:
            suggestion.kind === "directory"
              ? monacoInstance.languages.CompletionItemKind.Folder
              : monacoInstance.languages.CompletionItemKind.File,
          detail: suggestion.detail,
          insertText: suggestion.value,
          range,
          sortText: suggestion.kind === "directory" ? "0" : "1",
        }));
      },
    };

    // Register this editor's per-model state, and share one global provider
    // set across all editors (refcounted). Getters read live refs so renames
    // and schema changes are picked up without re-registering.
    const uri = model.uri.toString();
    sqlProviderEntries.set(uri, {
      getTables: () => providerTablesRef.current,
      getUpstreamNames: () => providerUpstreamNamesRef.current,
      getTableSuggestionContext: () => providerTableContextRef.current,
      remoteResolver,
    });
    const release = acquireSQLProviders(monaco);

    return () => {
      sqlProviderEntries.delete(uri);
      release();
    };
  }, [editor, loadSQLDiscoveryColumns, loadSQLDiscoveryTables, monaco, registerGlobalProviders]);

  useEffect(() => {
    if (!editor || tables.length === 0) {
      return;
    }

    const disposable = editor.onMouseDown((event) => {
      if (!event.event.leftButton) {
        return;
      }

      if (!event.event.ctrlKey && !event.event.metaKey) {
        return;
      }

      const position = event.target.position;
      if (!position) {
        return;
      }

      const model = editor.getModel();
      if (!model) {
        return;
      }

      const table = resolveTableAtPosition(model, position, tables);
      if (!table?.assetId || !table.pipelineId) {
        return;
      }

      event.event.preventDefault();
      event.event.stopPropagation();
      goToAssetRef.current?.(table.pipelineId, table.assetId);
    });

    return () => {
      disposable.dispose();
    };
  }, [editor, tables]);

  useEffect(() => {
    if (!registerParseContextMarkers || !editor || !monaco) {
      return;
    }

    const model = editor.getModel();
    if (!model) {
      return;
    }

    const unresolvedColumnSuggestions = buildUnresolvedColumnSuggestions(
      model,
      activeParseContext?.diagnostics ?? [],
      activeParseContext,
      tables,
    );
    const unresolvedTableSuggestions = buildUnresolvedTableSuggestions(
      model,
      activeParseContext?.diagnostics ?? [],
      tables,
    );
    const selfReferenceDiagnostics = buildSelfReferenceDiagnostics(asset, activeParseContext);

    const diagnostics = (activeParseContext?.diagnostics ?? [])
      .filter((diagnostic) => diagnostic.range)
      .map((diagnostic) => {
        const unresolvedColumnSuggestion = unresolvedColumnSuggestions.find(
          (suggestion) => suggestion.diagnostic === diagnostic,
        );
        const unresolvedTableSuggestion = unresolvedTableSuggestions.find(
          (suggestion) => suggestion.diagnostic === diagnostic,
        );

        return {
          severity:
            diagnostic.severity === "warning"
              ? monaco.MarkerSeverity.Warning
              : diagnostic.severity === "info"
                ? monaco.MarkerSeverity.Info
                : monaco.MarkerSeverity.Error,
          message: unresolvedColumnSuggestion
            ? formatUnresolvedColumnMessage(
                unresolvedColumnSuggestion.unknownColumn,
                unresolvedColumnSuggestion.replacementColumn,
              )
            : unresolvedTableSuggestion
              ? formatUnresolvedTableMessage(
                  unresolvedTableSuggestion.unknownTable,
                  unresolvedTableSuggestion.replacementTable,
                )
              : diagnostic.message,
          startLineNumber: diagnostic.range!.line,
          startColumn: diagnostic.range!.col,
          endLineNumber: diagnostic.range!.end_line,
          endColumn: diagnostic.range!.end_col,
        };
      });

    const inspectDiagnostics = buildInspectDiagnosticMarker(
      model,
      inspectDiagnosticSnapshot ?? null,
    );

    monaco.editor.setModelMarkers(model, "bruin-sql-parse-context", [
      ...diagnostics,
      ...selfReferenceDiagnostics.map((diagnostic) => ({
        severity: monaco.MarkerSeverity.Error,
        message: diagnostic.message,
        startLineNumber: diagnostic.range.startLineNumber,
        startColumn: diagnostic.range.startColumn,
        endLineNumber: diagnostic.range.endLineNumber,
        endColumn: diagnostic.range.endColumn,
      })),
      ...inspectDiagnostics,
    ]);

    return () => {
      monaco.editor.setModelMarkers(model, "bruin-sql-parse-context", []);
    };
  }, [
    asset,
    editor,
    inspectDiagnosticSnapshot,
    monaco,
    parseContextKey,
    registerParseContextMarkers,
    tables,
  ]);

  useEffect(() => {
    if (!registerLegacyDiagnosticProviders || !editor || !monaco) {
      return;
    }

    const model = editor.getModel();
    if (!model) {
      return;
    }

    const disposable = monaco.languages.registerCodeActionProvider("sql", {
      provideCodeActions: (_model, _range, context) => {
        if (_model.uri.toString() !== model.uri.toString()) {
          return { actions: [], dispose: () => undefined };
        }

        const currentParseContext = latestStateRef.current.activeParseContext;
        const currentTables = providerTablesRef.current;

        const columnSuggestions = buildUnresolvedColumnSuggestions(
          model,
          currentParseContext?.diagnostics ?? [],
          currentParseContext,
          currentTables,
        );
        const tableSuggestions = buildUnresolvedTableSuggestions(
          model,
          currentParseContext?.diagnostics ?? [],
          currentTables,
        );

        const columnActions = columnSuggestions
          .filter((suggestion) =>
            context.markers.some(
              (marker) =>
                marker.startLineNumber === suggestion.range.startLineNumber &&
                marker.startColumn === suggestion.range.startColumn &&
                marker.endLineNumber === suggestion.range.endLineNumber &&
                marker.endColumn === suggestion.range.endColumn,
            ),
          )
          .map((suggestion) => ({
            title: `Change '${suggestion.unknownColumn}' to '${suggestion.replacementColumn}'`,
            kind: "quickfix",
            diagnostics: context.markers,
            edit: {
              edits: [
                {
                  resource: model.uri,
                  versionId: model.getVersionId(),
                  textEdit: {
                    range: suggestion.range,
                    text: suggestion.replacementColumn,
                  },
                },
              ],
            },
            isPreferred: true,
          }));

        const tableActions = tableSuggestions
          .filter((suggestion) =>
            context.markers.some(
              (marker) =>
                marker.startLineNumber === suggestion.range.startLineNumber &&
                marker.startColumn === suggestion.range.startColumn &&
                marker.endLineNumber === suggestion.range.endLineNumber &&
                marker.endColumn === suggestion.range.endColumn,
            ),
          )
          .map((suggestion) => ({
            title: `Change '${suggestion.unknownTable}' to '${suggestion.replacementTable}'`,
            kind: "quickfix",
            diagnostics: context.markers,
            edit: {
              edits: [
                {
                  resource: model.uri,
                  versionId: model.getVersionId(),
                  textEdit: {
                    range: suggestion.range,
                    text: suggestion.replacementText,
                  },
                },
              ],
            },
            isPreferred: true,
          }));

        return { actions: [...columnActions, ...tableActions], dispose: () => undefined };
      },
    });

    return () => disposable.dispose();
  }, [editor, monaco, registerLegacyDiagnosticProviders]);

  useEffect(() => {
    if (!registerLegacyDiagnosticProviders || !editor || !monaco) {
      return;
    }

    const model = editor.getModel();
    if (!model) {
      return;
    }

    const disposable = monaco.languages.registerHoverProvider("sql", {
      provideHover: (_model, position) => {
        if (_model.uri.toString() !== model.uri.toString()) {
          return null;
        }

        const currentParseContext = latestStateRef.current.activeParseContext;
        const currentTables = providerTablesRef.current;

        const columnSuggestion = buildUnresolvedColumnSuggestions(
          model,
          currentParseContext?.diagnostics ?? [],
          currentParseContext,
          currentTables,
        ).find((candidate) => positionInRange(position, candidate.range));

        const tableSuggestion = buildUnresolvedTableSuggestions(
          model,
          currentParseContext?.diagnostics ?? [],
          currentTables,
        ).find((candidate) => positionInRange(position, candidate.range));

        if (!columnSuggestion && !tableSuggestion) {
          return null;
        }

        if (tableSuggestion) {
          return {
            range: tableSuggestion.range,
            contents: [
              { value: `**Unresolved table** \`${tableSuggestion.unknownTable}\`` },
              { value: `Did you mean \`${tableSuggestion.replacementTable}\`` },
            ],
          };
        }

        return {
          range: columnSuggestion!.range,
          contents: [
            { value: `**Unresolved column** \`${columnSuggestion!.unknownColumn}\`` },
            {
              value: `Did you mean \`${columnSuggestion!.replacementColumn}\``,
            },
          ],
        };
      },
    });

    return () => disposable.dispose();
  }, [editor, monaco, registerLegacyDiagnosticProviders]);
}
