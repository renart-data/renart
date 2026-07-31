"use client";

import { useEffect, useRef } from "react";
import type * as MonacoNS from "monaco-editor";
import { useAtomValue, useSetAtom } from "jotai";

import {
  getSQLLSPCodeActions,
  getSQLLSPCompletions,
  getSQLLSPDefinition,
  getSQLLSPDiagnostics,
  getSQLLSPDocumentSymbols,
  getSQLLSPFormatting,
  getSQLLSPHover,
  getSQLLSPReferences,
  getSQLLSPRename,
  getSQLLSPSemanticTokens,
  getSQLLSPSignatureHelp,
  SQLLSPCodeAction,
  SQLLSPCompletionItem,
  SQLLSPDocumentSymbol,
  SQLLSPRange,
  SQLLSPSignatureHelp,
  SQLLSPWorkspaceEdit,
} from "@/lib/api-sql-lsp";
import { getSQLPathSuggestions } from "@/lib/api-sql-discovery";
import { isQuerySensorAssetType, isSqlAssetType } from "@/lib/asset-types";
import { selectedEnvironmentAtom, workspaceAtom } from "@/lib/atoms/domains/workspace";
import { sqlDiscoveryColumnsAtom, sqlDiscoveryTablesAtom } from "@/lib/atoms/sql-discovery";
import { useSQLParseContext } from "@/hooks/use-sql-parse-context";
import { fetchJSON } from "@/lib/api-core";
import {
  isInsideSingleQuotedSQLString,
  provideLocalSQLCompletionItems,
  schemaTablesReferencedAtPosition,
} from "@/lib/monaco-sql-providers";
import {
  effectiveConnectionForAsset,
  effectiveConnectionTypeForAsset,
  SchemaTable,
} from "@/lib/sql-schema";
import { WebAsset, WorkspaceState } from "@/lib/types";

const SQL_LSP_MARKER_OWNER = "renart-sql-lsp";

export function useSQLLSP(
  monaco: typeof MonacoNS | null,
  editor: MonacoNS.editor.IStandaloneCodeEditor | null,
  asset: WebAsset | null,
  sqlContent: string,
  schemaTables: SchemaTable[],
  onGoToAsset?: (pipelineId: string, assetId: string) => void,
  // Notebook cells resolve to sibling cells too; those have no pipeline, so
  // navigation goes through this callback (scroll/focus the cell card).
  onGoToCell?: (cellId: string) => void,
  options?: {
    includeNotebookRuntimeColumns?: boolean;
    documentContext?: "asset" | "adhoc" | "custom_check";
    allowNonSQLDocument?: boolean;
  },
) {
  const workspace = useAtomValue(workspaceAtom);
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const loadRemoteTables = useSetAtom(sqlDiscoveryTablesAtom);
  const loadRemoteColumns = useSetAtom(sqlDiscoveryColumnsAtom);
  const parseContext = useSQLParseContext(asset, sqlContent, schemaTables);
  const connectionName = asset && workspace ? effectiveConnectionForAsset(asset) : null;
  const includeNotebookRuntimeColumns = options?.includeNotebookRuntimeColumns ?? false;
  const documentContext = options?.documentContext ?? "asset";
  const allowNonSQLDocument = options?.allowNonSQLDocument ?? false;

  // The Monaco providers below are registered once per (editor, asset) and read
  // their live inputs through this ref. Keeping them off the effect's dependency
  // list means an SSE workspace update (which changes `workspace`,
  // `parseContext`, `schemaTables`, …) refreshes the data the providers see
  // without disposing and re-registering them — re-registration would drop the
  // selection in an open suggestion widget.
  const providerStateRef = useRef({
    asset,
    workspace,
    connectionName,
    schemaTables,
    selectedEnvironment,
    parseContext,
    onGoToAsset,
    onGoToCell,
  });
  providerStateRef.current = {
    asset,
    workspace,
    connectionName,
    schemaTables,
    selectedEnvironment,
    parseContext,
    onGoToAsset,
    onGoToCell,
  };

  useEffect(() => {
    if (!monaco || !editor || !asset || (!isSQLAsset(asset) && !allowNonSQLDocument)) {
      return;
    }
    const model = editor.getModel();
    if (!model) {
      return;
    }
    const modelURI = model.uri.toString();

    const completion = monaco.languages.registerCompletionItemProvider("sql", {
      triggerCharacters: [".", "/", '"', "'"],
      async provideCompletionItems(currentModel, position) {
        if (currentModel.uri.toString() !== modelURI) {
          return { suggestions: [] };
        }
        const { asset, connectionName, parseContext, schemaTables, selectedEnvironment } =
          providerStateRef.current;
        if (!asset) {
          return { suggestions: [] };
        }
        const pathContext = getQuotedPathContext(
          currentModel.getLineContent(position.lineNumber),
          position,
        );
        if (pathContext) {
          const response = await getSQLPathSuggestions({
            assetId: asset.id,
            prefix: pathContext.prefix,
            environment: selectedEnvironment,
          }).catch(() => null);
          return {
            suggestions: (response?.suggestions ?? []).map((suggestion) => ({
              label: suggestion.value,
              kind:
                suggestion.kind === "directory"
                  ? monaco.languages.CompletionItemKind.Folder
                  : monaco.languages.CompletionItemKind.File,
              detail: suggestion.detail,
              insertText: suggestion.value,
              range: pathContext.range,
              sortText: suggestion.kind === "directory" ? "0" : "1",
            })),
          };
        }
        if (isInsideJinjaBlock(currentModel.getValue(), currentModel.getOffsetAt(position))) {
          return { suggestions: [] };
        }
        const textBeforeCursor = currentModel
          .getLineContent(position.lineNumber)
          .slice(0, position.column - 1);
        const valueContext = parseEqualityValueContext(textBeforeCursor);
        const connectionType = providerStateRef.current.workspace
          ? effectiveConnectionTypeForAsset(providerStateRef.current.workspace, asset)
          : null;
        if (valueContext && connectionName && parseContext && connectionType === "duckdb") {
          const resolvedTable = resolveValueSuggestionTable(
            parseContext,
            schemaTables,
            valueContext.tableIdentifier,
            valueContext.columnName,
          );
          if (resolvedTable) {
            const values = await fetchColumnValues({
              columnName: valueContext.columnName,
              connectionName,
              connectionType,
              environment: selectedEnvironment,
              prefix: valueContext.prefix,
              resolvedTable,
            }).catch(() => []);
            if (values.length > 0) {
              return {
                suggestions: values.map((value, index) => ({
                  label: String(value ?? "NULL"),
                  kind: monaco.languages.CompletionItemKind.Value,
                  detail: `${resolvedTable}.${valueContext.columnName}`,
                  insertText: formatSQLValueCompletion(value, valueContext.insideQuotes),
                  range: new monaco.Range(
                    position.lineNumber,
                    position.column - valueContext.prefix.length,
                    position.lineNumber,
                    position.column,
                  ),
                  sortText: `0${index}`,
                })),
              };
            }
          }
        }
        if (
          isInsideSingleQuotedSQLString(currentModel.getValue(), currentModel.getOffsetAt(position))
        ) {
          return { suggestions: [] };
        }
        const dotPrefix = parseDotPrefix(textBeforeCursor);
        const response = await getSQLLSPCompletions({
          asset_id: asset.id,
          content: currentModel.getValue(),
          document_context: documentContext,
          position: monacoPositionToLSP(position),
        });
        const word = currentModel.getWordUntilPosition(position);
        const range = new monaco.Range(
          position.lineNumber,
          word.startColumn,
          position.lineNumber,
          word.endColumn,
        );
        // Keep column (5), relation/asset (18) and keyword (2) completions from
        // the LSP; drop anything else. Keywords sort last (see the engine's "z"
        // SortText) so schema-aware suggestions stay on top.
        const lspSuggestions = (response.completions ?? [])
          .filter((item) => item.kind === 5 || item.kind === 18 || item.kind === 2)
          .map((item) => completionToMonaco(monaco, item, range));
        if (includeNotebookRuntimeColumns) {
          const runtimeTables = schemaTables.filter((table) =>
            table.columns.some((column) => column.sourceMethods?.includes("notebook-run")),
          );
          const runtimeSources = schemaTablesReferencedAtPosition(
            currentModel,
            position,
            runtimeTables,
          );
          const runtimeCompletionTables = runtimeSources.hasSource
            ? runtimeSources.tables
            : runtimeTables;
          if (runtimeCompletionTables.length > 0) {
            lspSuggestions.push(
              ...provideLocalSQLCompletionItems(monaco, currentModel, position, {
                getTables: () => runtimeCompletionTables,
                getUpstreamNames: () => asset.upstreams ?? [],
                getTableSuggestionContext: () => undefined,
              }).suggestions,
            );
          }
        }
        // Derived aliases (CTEs, subqueries, VALUES and DESCRIBE) belong to the
        // local query scope. Prefer their LSP columns before consulting the
        // warehouse; resolving the alias back to its source relation would
        // otherwise leak the source table's columns into a derived result.
        const hasLocalColumnSuggestions = (response.completions ?? []).some(
          (item) => item.kind === 5,
        );
        // A `schema.` in a FROM/JOIN position is a relation qualifier, not an
        // alias whose columns we should fetch from the warehouse.
        if (
          dotPrefix &&
          connectionName &&
          !hasLocalColumnSuggestions &&
          !isRelationIdentifierContext(currentModel, position)
        ) {
          const remoteTableName =
            resolveRemoteTableName(dotPrefix.tablePart, schemaTables, parseContext) ??
            dotPrefix.tablePart;
          const columns = await loadRemoteColumns({
            connection: connectionName,
            table: remoteTableName,
            environment: selectedEnvironment,
          }).catch(() => []);
          const normalizedPrefix = dotPrefix.columnPrefix.trim().toLowerCase();
          lspSuggestions.push(
            ...columns
              .filter(
                (column) =>
                  !normalizedPrefix || column.name.toLowerCase().includes(normalizedPrefix),
              )
              .map((column) => ({
                label: column.name,
                kind: monaco.languages.CompletionItemKind.Field,
                detail: column.type
                  ? `${remoteTableName}.${column.name} (${column.type})`
                  : `${remoteTableName}.${column.name}`,
                insertText: column.name,
                range: new monaco.Range(
                  position.lineNumber,
                  position.column - dotPrefix.columnPrefix.length,
                  position.lineNumber,
                  position.column,
                ),
                sortText: "1",
              })),
          );
        }
        if (connectionName && isTableCompletionContext(currentModel, position)) {
          const remoteTables = await loadRemoteTables({
            connection: connectionName,
            environment: selectedEnvironment,
          }).catch(() => []);
          const normalizedPrefix = currentModel.getValueInRange(range).trim().toLowerCase();
          const knownLocalNames = new Set(
            schemaTables.flatMap((table) => [
              table.name.toLowerCase(),
              table.shortName.toLowerCase(),
            ]),
          );
          lspSuggestions.push(
            ...remoteTables
              .filter(
                (table) =>
                  !normalizedPrefix ||
                  table.name.toLowerCase().includes(normalizedPrefix) ||
                  table.short_name.toLowerCase().includes(normalizedPrefix),
              )
              .filter((table) => !knownLocalNames.has(table.name.toLowerCase()))
              .map((table) => ({
                label: {
                  label: table.name,
                  description: "Remote table",
                },
                kind: monaco.languages.CompletionItemKind.Struct,
                detail: "Remote table",
                insertText: table.name,
                range,
                sortText: `22${table.name.toLowerCase()}`,
              })),
          );
        }
        return { suggestions: dedupeSQLCompletions(lspSuggestions) };
      },
    });

    const definition = monaco.languages.registerDefinitionProvider("sql", {
      async provideDefinition(currentModel, position) {
        if (currentModel.uri.toString() !== modelURI) {
          return [];
        }
        const { asset, workspace } = providerStateRef.current;
        if (!asset) {
          return [];
        }
        if (isInsideJinjaBlock(currentModel.getValue(), currentModel.getOffsetAt(position))) {
          return [];
        }
        const response = await getSQLLSPDefinition({
          asset_id: asset.id,
          content: currentModel.getValue(),
          position: monacoPositionToLSP(position),
        });
        const locations: MonacoNS.languages.Location[] = [];
        for (const location of response.locations ?? []) {
          if (location.asset_id) {
            const target = findWorkspaceAsset(workspace, location.asset_id);
            if (!target) {
              continue;
            }
            locations.push({
              uri: ensureAssetPreviewModel(monaco, target.asset),
              range: lspRangeToMonaco(monaco, location.range),
            });
            continue;
          }
          locations.push({
            uri: currentModel.uri,
            range: lspRangeToMonaco(monaco, location.range),
          });
        }
        return locations;
      },
    });

    const ctrlClickNavigation = editor.onMouseDown((event) => {
      if (!event.event.leftButton || (!event.event.ctrlKey && !event.event.metaKey)) {
        return;
      }
      const position = event.target.position;
      if (!position) {
        return;
      }
      const { asset, workspace, onGoToAsset, onGoToCell } = providerStateRef.current;
      if (!asset) {
        return;
      }
      if (isInsideJinjaBlock(model.getValue(), model.getOffsetAt(position))) {
        return;
      }
      event.event.preventDefault();
      event.event.stopPropagation();
      void getSQLLSPDefinition({
        asset_id: asset.id,
        content: model.getValue(),
        position: monacoPositionToLSP(position),
      })
        .then((response) => {
          const assetLocation = (response.locations ?? []).find((location) => location.asset_id);
          if (!assetLocation?.asset_id) {
            return;
          }
          const target = findWorkspaceAsset(workspace, assetLocation.asset_id);
          if (!target || target.asset.id === asset.id) {
            return;
          }
          if (target.pipeline) {
            onGoToAsset?.(target.pipeline.id, target.asset.id);
          } else if (target.asset.cell_id) {
            onGoToCell?.(target.asset.cell_id);
          }
        })
        .catch(() => undefined);
    });

    const references = monaco.languages.registerReferenceProvider("sql", {
      async provideReferences(currentModel, position, context) {
        if (currentModel.uri.toString() !== modelURI) {
          return [];
        }
        const { asset, workspace } = providerStateRef.current;
        if (!asset) {
          return [];
        }
        const response = await getSQLLSPReferences({
          asset_id: asset.id,
          content: currentModel.getValue(),
          position: monacoPositionToLSP(position),
          include_declaration: context.includeDeclaration,
        });
        return (response.locations ?? []).flatMap((location) =>
          locationToMonacoLocations(monaco, currentModel, workspace, asset, location),
        );
      },
    });

    const rename = monaco.languages.registerRenameProvider("sql", {
      async provideRenameEdits(currentModel, position, newName) {
        if (currentModel.uri.toString() !== modelURI) {
          return { edits: [], rejectReason: "Rename is only available in the active SQL asset." };
        }
        const response = await getSQLLSPRename({
          asset_id: asset.id,
          content: currentModel.getValue(),
          position: monacoPositionToLSP(position),
          new_name: newName,
        });
        if (response.error) {
          return { edits: [], rejectReason: response.error };
        }
        const edit = lspWorkspaceEditToMonaco(monaco, currentModel, response.edit);
        if (!edit.edits.length) {
          return { edits: [], rejectReason: "This SQL symbol cannot be renamed here." };
        }
        return edit;
      },
    });

    const codeActions = monaco.languages.registerCodeActionProvider("sql", {
      async provideCodeActions(currentModel, _range, context) {
        if (currentModel.uri.toString() !== modelURI) {
          return { actions: [], dispose: () => undefined };
        }
        const response = await getSQLLSPCodeActions({
          asset_id: asset.id,
          content: currentModel.getValue(),
        });
        const markerRanges = new Set(context.markers.map(markerRangeKey));
        const actions = (response.code_actions ?? [])
          .filter((action) =>
            action.diagnostics?.some((diagnostic) => {
              return markerRanges.has(lspRangeKey(lspRangeToMarker(diagnostic.range)));
            }),
          )
          .map((action) => codeActionToMonaco(monaco, currentModel, action, context.markers));
        return { actions, dispose: () => undefined };
      },
    });

    const hover = monaco.languages.registerHoverProvider("sql", {
      async provideHover(currentModel, position) {
        if (currentModel.uri.toString() !== modelURI) {
          return null;
        }
        if (isInsideJinjaBlock(currentModel.getValue(), currentModel.getOffsetAt(position))) {
          return null;
        }
        const response = await getSQLLSPHover({
          asset_id: asset.id,
          content: currentModel.getValue(),
          position: monacoPositionToLSP(position),
        });
        if (!response.hover) {
          return null;
        }
        return {
          contents: [{ value: response.hover.contents }],
          range: response.hover.range ? lspRangeToMonaco(monaco, response.hover.range) : undefined,
        };
      },
    });

    const semanticTokens = monaco.languages.registerDocumentSemanticTokensProvider("sql", {
      getLegend() {
        return {
          tokenTypes: ["schema", "table", "column", "alias"],
          tokenModifiers: [],
        };
      },
      async provideDocumentSemanticTokens(currentModel) {
        if (currentModel.uri.toString() !== modelURI) {
          return { data: new Uint32Array() };
        }
        const response = await getSQLLSPSemanticTokens({
          asset_id: asset.id,
          content: currentModel.getValue(),
        }).catch(() => null);
        return { data: new Uint32Array(response?.tokens?.data ?? []) };
      },
      releaseDocumentSemanticTokens() {},
    });

    const documentSymbols = monaco.languages.registerDocumentSymbolProvider("sql", {
      async provideDocumentSymbols(currentModel) {
        if (currentModel.uri.toString() !== modelURI) {
          return [];
        }
        const response = await getSQLLSPDocumentSymbols({
          asset_id: asset.id,
          content: currentModel.getValue(),
        }).catch(() => null);
        return (response?.symbols ?? []).map((symbol) => documentSymbolToMonaco(monaco, symbol));
      },
    });

    const formatting = monaco.languages.registerDocumentFormattingEditProvider("sql", {
      async provideDocumentFormattingEdits(currentModel, options) {
        if (currentModel.uri.toString() !== modelURI) {
          return [];
        }
        const response = await getSQLLSPFormatting({
          asset_id: asset.id,
          content: currentModel.getValue(),
          formatting_options: {
            tabSize: options.tabSize,
            insertSpaces: options.insertSpaces,
          },
        }).catch(() => null);
        return workspaceEditToTextEdits(monaco, currentModel, response?.edit);
      },
    });

    const signature = monaco.languages.registerSignatureHelpProvider("sql", {
      signatureHelpTriggerCharacters: ["(", ","],
      signatureHelpRetriggerCharacters: [","],
      async provideSignatureHelp(currentModel, position) {
        if (currentModel.uri.toString() !== modelURI) {
          return null;
        }
        if (isInsideJinjaBlock(currentModel.getValue(), currentModel.getOffsetAt(position))) {
          return null;
        }
        const response = await getSQLLSPSignatureHelp({
          asset_id: asset.id,
          content: currentModel.getValue(),
          position: monacoPositionToLSP(position),
        }).catch(() => null);
        if (!response?.signature) {
          return null;
        }
        return {
          value: signatureHelpToMonaco(response.signature),
          dispose: () => {},
        };
      },
    });

    return () => {
      completion.dispose();
      definition.dispose();
      ctrlClickNavigation.dispose();
      references.dispose();
      rename.dispose();
      codeActions.dispose();
      hover.dispose();
      semanticTokens.dispose();
      documentSymbols.dispose();
      formatting.dispose();
      signature.dispose();
    };
    // Register once per (editor, asset); live inputs (workspace, parseContext,
    // schemaTables, connection, environment, callbacks) are read from
    // providerStateRef so an SSE update does not re-register the providers.
  }, [
    monaco,
    editor,
    asset?.id,
    includeNotebookRuntimeColumns,
    allowNonSQLDocument,
    documentContext,
    loadRemoteColumns,
    loadRemoteTables,
  ]);

  useEffect(() => {
    if (!monaco || !editor || !asset || (!isSQLAsset(asset) && !allowNonSQLDocument)) {
      return;
    }
    const model = editor.getModel();
    if (!model) {
      return;
    }
    const controller = new AbortController();
    const timer = window.setTimeout(async () => {
      const requestVersion = model.getVersionId();
      const requestContent = model.getValue();
      try {
        const response = await getSQLLSPDiagnostics(
          {
            asset_id: asset.id,
            content: requestContent,
            document_context: documentContext,
          },
          controller.signal,
        );
        if (
          controller.signal.aborted ||
          model.isDisposed() ||
          editor.getModel() !== model ||
          model.getVersionId() !== requestVersion ||
          model.getValue() !== requestContent
        ) {
          return;
        }
        monaco.editor.setModelMarkers(
          model,
          SQL_LSP_MARKER_OWNER,
          (response.diagnostics ?? []).map((diagnostic) => ({
            message: diagnostic.message,
            code: diagnostic.code,
            source: diagnostic.source,
            severity:
              diagnostic.severity === 1
                ? monaco.MarkerSeverity.Error
                : monaco.MarkerSeverity.Warning,
            ...lspRangeToMarker(diagnostic.range),
          })),
        );
      } catch {
        // Keep the last-known markers on a transient fetch failure rather than
        // clearing them, which would make diagnostics flicker away on a blip.
      }
    }, 250);

    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [asset, documentContext, editor, monaco, sqlContent]);
}

function isSQLAsset(asset: WebAsset) {
  return isSqlAssetType(asset.type) || isQuerySensorAssetType(asset.type);
}

function findWorkspaceAsset(workspace: WorkspaceState | null | undefined, assetID: string) {
  for (const pipeline of workspace?.pipelines ?? []) {
    const asset = pipeline.assets.find((candidate) => candidate.id === assetID);
    if (asset) {
      return { pipeline, asset };
    }
  }
  // Notebook cells are LSP targets too (sibling-cell definitions and
  // references); they have no pipeline, so navigation callers must check it.
  for (const notebook of workspace?.notebooks ?? []) {
    const asset = (notebook.cells ?? []).find((candidate) => candidate.id === assetID);
    if (asset) {
      return { pipeline: null, asset };
    }
  }
  return null;
}

function ensureAssetPreviewModel(monaco: typeof MonacoNS, asset: WebAsset) {
  const basename = asset.name.trim() || asset.path.split("/").pop() || asset.id;
  const content = isQuerySensorAssetType(asset.type)
    ? (asset.parameters?.query ?? "")
    : asset.content;
  const uri = monaco.Uri.from({
    scheme: "renart-asset",
    path: `/${basename.endsWith(".sql") ? basename : `${basename}.sql`}`,
  });
  const existing = monaco.editor.getModel(uri);
  if (existing) {
    if (existing.getValue() !== content) {
      existing.setValue(content);
    }
    return uri;
  }
  monaco.editor.createModel(content, "sql", uri);
  return uri;
}

function locationToMonacoLocations(
  monaco: typeof MonacoNS,
  currentModel: MonacoNS.editor.ITextModel,
  workspace: WorkspaceState | null | undefined,
  currentAsset: WebAsset,
  location: { uri: string; range: SQLLSPRange; asset_id?: string },
): MonacoNS.languages.Location[] {
  if (location.asset_id) {
    const target = findWorkspaceAsset(workspace, location.asset_id);
    if (!target) {
      return [];
    }
    return [
      {
        uri: ensureAssetPreviewModel(monaco, target.asset),
        range: lspRangeToMonaco(monaco, location.range),
      },
    ];
  }
  return [
    {
      uri: currentModel.uri,
      range: lspRangeToMonaco(monaco, location.range),
    },
  ];
}

function lspWorkspaceEditToMonaco(
  monaco: typeof MonacoNS,
  currentModel: MonacoNS.editor.ITextModel,
  edit: SQLLSPWorkspaceEdit | undefined,
): MonacoNS.languages.WorkspaceEdit {
  const edits: MonacoNS.languages.WorkspaceEdit["edits"] = [];
  for (const [uri, textEdits] of Object.entries(edit?.changes ?? {})) {
    const resource = uri === currentModel.uri.toString() ? currentModel.uri : monaco.Uri.parse(uri);
    for (const textEdit of textEdits) {
      edits.push({
        resource,
        versionId:
          resource.toString() === currentModel.uri.toString()
            ? currentModel.getVersionId()
            : undefined,
        textEdit: {
          range: lspRangeToMonaco(monaco, textEdit.range),
          text: textEdit.newText,
        },
      });
    }
  }
  return { edits };
}

function workspaceEditToTextEdits(
  monaco: typeof MonacoNS,
  currentModel: MonacoNS.editor.ITextModel,
  edit: SQLLSPWorkspaceEdit | undefined,
): MonacoNS.languages.TextEdit[] {
  const result: MonacoNS.languages.TextEdit[] = [];
  for (const [uri, textEdits] of Object.entries(edit?.changes ?? {})) {
    if (uri !== currentModel.uri.toString()) {
      continue;
    }
    for (const textEdit of textEdits) {
      result.push({
        range: lspRangeToMonaco(monaco, textEdit.range),
        text: textEdit.newText,
      });
    }
  }
  return result;
}

function documentSymbolToMonaco(
  monaco: typeof MonacoNS,
  symbol: SQLLSPDocumentSymbol,
): MonacoNS.languages.DocumentSymbol {
  return {
    name: symbol.name,
    detail: symbol.detail ?? "",
    kind: symbol.kind,
    range: lspRangeToMonaco(monaco, symbol.range),
    selectionRange: lspRangeToMonaco(monaco, symbol.selectionRange),
    tags: [],
    children: (symbol.children ?? []).map((child) => documentSymbolToMonaco(monaco, child)),
  };
}

function signatureHelpToMonaco(help: SQLLSPSignatureHelp): MonacoNS.languages.SignatureHelp {
  return {
    signatures: help.signatures.map((signature) => ({
      label: signature.label,
      documentation: signature.documentation
        ? { value: signature.documentation, isTrusted: false }
        : undefined,
      parameters: (signature.parameters ?? []).map((parameter) => ({
        label: parameter.label,
        documentation: parameter.documentation,
      })),
      activeParameter: signature.activeParameter,
    })),
    activeSignature: help.activeSignature ?? 0,
    activeParameter: help.activeParameter ?? 0,
  };
}

function codeActionToMonaco(
  monaco: typeof MonacoNS,
  currentModel: MonacoNS.editor.ITextModel,
  action: SQLLSPCodeAction,
  markers: MonacoNS.editor.IMarkerData[],
): MonacoNS.languages.CodeAction {
  return {
    title: action.title,
    kind: action.kind ?? "quickfix",
    diagnostics: markers,
    edit: lspWorkspaceEditToMonaco(monaco, currentModel, action.edit),
    isPreferred: action.isPreferred,
  };
}

function markerRangeKey(marker: MonacoNS.editor.IMarkerData) {
  return lspRangeKey({
    startLineNumber: marker.startLineNumber,
    startColumn: marker.startColumn,
    endLineNumber: marker.endLineNumber,
    endColumn: marker.endColumn,
  });
}

function lspRangeKey(range: ReturnType<typeof lspRangeToMarker>) {
  return `${range.startLineNumber}:${range.startColumn}:${range.endLineNumber}:${range.endColumn}`;
}

function getQuotedPathContext(
  lineContent: string,
  position: MonacoNS.Position,
): { prefix: string; range: MonacoNS.IRange } | null {
  const cursorIndex = position.column - 1;
  let activeQuote: "'" | '"' | null = null;
  let quoteStart = -1;

  for (let index = 0; index < cursorIndex; index += 1) {
    const current = lineContent[index];
    const next = lineContent[index + 1];

    if (!activeQuote && current === "-" && next === "-") {
      break;
    }

    if (!activeQuote) {
      if (current === "'" || current === '"') {
        activeQuote = current;
        quoteStart = index;
      }
      continue;
    }

    if (current !== activeQuote) {
      continue;
    }

    if (next === activeQuote) {
      index += 1;
      continue;
    }

    activeQuote = null;
    quoteStart = -1;
  }

  if (!activeQuote || quoteStart < 0) {
    return null;
  }

  const prefix = lineContent.slice(quoteStart + 1, cursorIndex);
  if (!prefix.startsWith("s3://") && !prefix.startsWith("./") && !prefix.startsWith("/")) {
    return null;
  }

  return {
    prefix,
    range: {
      startLineNumber: position.lineNumber,
      endLineNumber: position.lineNumber,
      startColumn: quoteStart + 2,
      endColumn: position.column,
    },
  };
}

function parseDotPrefix(
  textBeforeCursor: string,
): { tablePart: string; columnPrefix: string } | null {
  const match = textBeforeCursor.match(/([\w."]+)\.\s*([\w]*)$/);
  if (!match) {
    return null;
  }
  return { tablePart: match[1].replace(/"/g, ""), columnPrefix: match[2] };
}

function parseEqualityValueContext(textBeforeCursor: string): {
  tableIdentifier: string | null;
  columnName: string;
  prefix: string;
  insideQuotes: boolean;
} | null {
  const normalized = textBeforeCursor.replace(/\s+/g, " ");
  const patterns = [
    /(?:(["\w.]+)\.)?(["\w]+)\s*(?:=|!=|<>|<|>|<=|>=|like|ilike|in)\s*'([^']*)$/i,
    /(?:(["\w.]+)\.)?(["\w]+)\s*(?:=|!=|<>|<|>|<=|>=|like|ilike|in)\s*"([^"]*)$/i,
    /(?:(["\w.]+)\.)?(["\w]+)\s*(?:=|!=|<>|<|>|<=|>=|like|ilike|in)\s*([^\s,'")\]]*)$/i,
  ];
  for (const pattern of patterns) {
    const match = normalized.match(pattern);
    if (!match) {
      continue;
    }
    return {
      tableIdentifier: match[1]?.replace(/"/g, "") ?? null,
      columnName: match[2].replace(/"/g, ""),
      prefix: match[3] ?? "",
      insideQuotes: pattern !== patterns[2],
    };
  }
  return null;
}

function resolveRemoteTableName(
  identifier: string,
  schemaTables: SchemaTable[],
  parseContext: ReturnType<typeof useSQLParseContext>,
) {
  const normalized = identifier.trim().toLowerCase();
  const localTable = schemaTables.find(
    (table) =>
      table.name.toLowerCase() === normalized || table.shortName.toLowerCase() === normalized,
  );
  if (localTable) {
    return localTable.name;
  }
  const parsedTable = (parseContext?.tables ?? []).find(
    (table) =>
      table.alias?.trim().toLowerCase() === normalized ||
      table.name.trim().toLowerCase() === normalized ||
      table.resolved_name?.trim().toLowerCase() === normalized,
  );
  return parsedTable?.resolved_name ?? parsedTable?.name ?? null;
}

function resolveValueSuggestionTable(
  parseContext: ReturnType<typeof useSQLParseContext>,
  schemaTables: SchemaTable[],
  tableIdentifier: string | null,
  columnName: string,
) {
  if (!tableIdentifier) {
    const resolvedTables = new Set<string>();
    for (const column of parseContext?.columns ?? []) {
      const columnPart = column.parts.findLast((part) => part.kind === "column");
      if (columnPart?.name.toLowerCase() === columnName.toLowerCase() && column.resolved_table) {
        resolvedTables.add(column.resolved_table);
      }
    }
    if (resolvedTables.size === 1) {
      return Array.from(resolvedTables)[0] ?? null;
    }

    const referencedTables = (parseContext?.tables ?? [])
      .map((table) => table.resolved_name ?? table.name)
      .filter(Boolean);
    const matchingTables = schemaTables.filter((table) => {
      if (
        referencedTables.length > 0 &&
        !referencedTables.some(
          (name) => identifiersEqual(name, table.name) || identifiersEqual(name, table.shortName),
        )
      ) {
        return false;
      }
      return table.columns.some((column) => column.name.toLowerCase() === columnName.toLowerCase());
    });
    return matchingTables.length === 1 ? matchingTables[0].name : null;
  }

  const matchingColumn = (parseContext?.columns ?? []).find((column) => {
    const columnPart = column.parts.findLast((part) => part.kind === "column");
    return (
      column.qualifier?.toLowerCase() === tableIdentifier.toLowerCase() &&
      columnPart?.name.toLowerCase() === columnName.toLowerCase() &&
      column.resolved_table
    );
  });
  return matchingColumn?.resolved_table ?? null;
}

function identifiersEqual(left: string, right: string) {
  return (
    left
      .trim()
      .replace(/^["'`]+|["'`]+$/g, "")
      .toLowerCase() ===
    right
      .trim()
      .replace(/^["'`]+|["'`]+$/g, "")
      .toLowerCase()
  );
}

async function fetchColumnValues(options: {
  columnName: string;
  connectionName: string;
  connectionType: string;
  environment?: string;
  prefix: string;
  resolvedTable: string;
}) {
  const query = buildValueSuggestionQuery(
    options.connectionType,
    quoteSQLIdentifier(options.resolvedTable),
    quoteSQLIdentifier(options.columnName),
    options.prefix.trim(),
  );
  if (!query) {
    return [];
  }
  const payload = await fetchJSON<{ values?: Array<string | number | boolean | null> }>(
    "/api/sql/column-values",
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      cache: "no-store",
      body: JSON.stringify({
        connection: options.connectionName,
        environment: options.environment ?? "",
        query,
      }),
    },
  );
  return payload.values ?? [];
}

function buildValueSuggestionQuery(
  connectionType: string,
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

function formatSQLValueCompletion(value: string | number | boolean | null, insideQuotes: boolean) {
  if (typeof value === "string") {
    return insideQuotes ? value.replaceAll("'", "''") : `'${value.replaceAll("'", "''")}'`;
  }
  return String(value ?? "NULL");
}

function isRelationIdentifierContext(
  model: MonacoNS.editor.ITextModel,
  position: MonacoNS.Position,
) {
  const textBeforeCursor = model
    .getValueInRange({
      startLineNumber: 1,
      startColumn: 1,
      endLineNumber: position.lineNumber,
      endColumn: position.column,
    })
    .replace(/'[^']*'|"[^"]*"/g, " ");
  return /\b(?:from|join|into|update)\s+[\w.]+\s*$/i.test(textBeforeCursor);
}

function isTableCompletionContext(model: MonacoNS.editor.ITextModel, position: MonacoNS.Position) {
  const textBeforeCursor = model.getValueInRange({
    startLineNumber: 1,
    startColumn: 1,
    endLineNumber: position.lineNumber,
    endColumn: position.column,
  });
  const tokens =
    textBeforeCursor.replace(/'[^']*'|"[^"]*"/g, " ").match(/\b[a-zA-Z_][\w]*\b/g) ?? [];
  const last = tokens.at(-1)?.toLowerCase();
  return last === "from" || last === "join" || last === "into" || last === "update";
}

function isInsideJinjaBlock(text: string, offset: number) {
  const before = text.slice(0, offset);
  const openExpression = before.lastIndexOf("{{");
  const closeExpression = before.lastIndexOf("}}");
  if (openExpression > closeExpression) {
    return true;
  }

  const openStatement = before.lastIndexOf("{%");
  const closeStatement = before.lastIndexOf("%}");
  if (openStatement > closeStatement) {
    return true;
  }

  const openComment = before.lastIndexOf("{#");
  const closeComment = before.lastIndexOf("#}");
  return openComment > closeComment;
}

function monacoPositionToLSP(position: MonacoNS.Position) {
  return {
    line: position.lineNumber - 1,
    character: position.column - 1,
  };
}

function lspRangeToMonaco(monaco: typeof MonacoNS, range: SQLLSPRange) {
  return new monaco.Range(
    range.start.line + 1,
    range.start.character + 1,
    range.end.line + 1,
    range.end.character + 1,
  );
}

function lspRangeToMarker(range: SQLLSPRange) {
  return {
    startLineNumber: range.start.line + 1,
    startColumn: range.start.character + 1,
    endLineNumber: range.end.line + 1,
    endColumn: range.end.character + 1,
  };
}

function completionToMonaco(
  monaco: typeof MonacoNS,
  item: SQLLSPCompletionItem,
  range: MonacoNS.IRange,
): MonacoNS.languages.CompletionItem {
  const insertText = item.insertText || item.label;
  return {
    label: item.label,
    kind: completionKindToMonaco(monaco, item.kind),
    detail: item.detail,
    documentation: item.documentation ? { value: item.documentation } : undefined,
    insertText,
    range,
    sortText: item.sortText ? `lsp-${item.sortText}` : `lsp-${item.label}`,
    command: insertText.endsWith(".")
      ? { id: "editor.action.triggerSuggest", title: "Show column suggestions" }
      : undefined,
  };
}

function dedupeSQLCompletions(
  suggestions: MonacoNS.languages.CompletionItem[],
): MonacoNS.languages.CompletionItem[] {
  const seen = new Set<string>();
  return suggestions.filter((suggestion) => {
    const label = typeof suggestion.label === "string" ? suggestion.label : suggestion.label.label;
    const key = `${label.toLowerCase()}::${suggestion.insertText.toLowerCase()}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

function completionKindToMonaco(monaco: typeof MonacoNS, kind?: number) {
  switch (kind) {
    case 2:
      return monaco.languages.CompletionItemKind.Keyword;
    case 5:
      return monaco.languages.CompletionItemKind.Field;
    case 18:
      return monaco.languages.CompletionItemKind.Reference;
    default:
      return monaco.languages.CompletionItemKind.Text;
  }
}
