"use client";

import type { Monaco } from "@monaco-editor/react";
import type * as MonacoNS from "monaco-editor";
import { atom, useAtom, useAtomValue } from "jotai";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { AssetCodeEditor } from "@/components/asset-code-editor";
import { useJinjaIntellisense } from "@/hooks/use-jinja-intellisense";
import { useSQLIntellisense } from "@/hooks/use-sql-intellisense";
import { useSQLLSP } from "@/hooks/use-sql-lsp";
import { useSQLCanvasHover } from "@/hooks/use-sql-canvas-hover";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";
import { getSQLLSPFormatting } from "@/lib/api-sql-lsp";
import { selectedEnvironmentAtom, workspaceAtom } from "@/lib/atoms/domains/workspace";
import { defineBruinMonacoThemes } from "@/lib/monaco-theme";
import { buildSchemaForAsset } from "@/lib/sql-schema";
import { WebAsset, WorkspaceQueryConnection } from "@/lib/types";

// Ad hoc query drafts keyed by pipeline id, so switching between the asset
// and ad hoc editors (or between pipelines) keeps the query text around.
const adhocDraftsAtom = atom<Record<string, string>>({});
const adhocConnectionsAtom = atom<Record<string, string>>({});

const DEFAULT_ADHOC_QUERY = "select 1";

export function useAdhocQueryDraft(pipelineId: string) {
  const [drafts, setDrafts] = useAtom(adhocDraftsAtom);
  const value = drafts[pipelineId] ?? DEFAULT_ADHOC_QUERY;
  const setValue = useCallback(
    (nextValue: string) => {
      setDrafts((previous) => ({ ...previous, [pipelineId]: nextValue }));
    },
    [pipelineId, setDrafts],
  );
  return [value, setValue] as const;
}

export function useAdhocConnectionSelection(pipelineId: string) {
  const [connections, setConnections] = useAtom(adhocConnectionsAtom);
  const value = connections[pipelineId] ?? null;
  const setValue = useCallback(
    (connection: string) => {
      setConnections((previous) => ({ ...previous, [pipelineId]: connection }));
    },
    [pipelineId, setConnections],
  );
  return [value, setValue] as const;
}

/**
 * Monaco editor for ad hoc SQL inside the app build page. Borrows the
 * pipeline graph and Jinja scope from a real asset, while the selected query
 * connection supplies the SQL asset type, dialect, and schema scope. The
 * content is a local draft that is never saved to disk.
 */
export function AppAdhocEditor({
  pipelineId,
  contextAsset,
  connection,
  onRunQuery,
  onGoToAsset,
}: {
  pipelineId: string;
  contextAsset: WebAsset;
  connection: WorkspaceQueryConnection;
  onRunQuery: () => void;
  onGoToAsset?: (pipelineId: string, assetId: string) => void;
}) {
  const { theme } = useWorkspaceTheme();
  const monacoTheme = theme === "dark" ? "bruin-adhoc-vs-dark" : "bruin-adhoc-vs";
  const [editorValue, setEditorValue] = useAdhocQueryDraft(pipelineId);
  const workspace = useAtomValue(workspaceAtom);
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const [monacoInstance, setMonacoInstance] = useState<Monaco | null>(null);
  const [editorInstance, setEditorInstance] =
    useState<MonacoNS.editor.IStandaloneCodeEditor | null>(null);

  // Keep the real asset id so parse context and Jinja rendering retain the
  // pipeline scope. The selected connection supplies the runtime identity;
  // dropping the name prevents a false self-reference diagnostic.
  const adhocAsset = useMemo<WebAsset>(
    () => ({
      ...contextAsset,
      name: "",
      path: "adhoc.sql",
      type: connection.asset_type,
      connection: connection.name,
    }),
    [connection.asset_type, connection.name, contextAsset],
  );
  const schemaTables = useMemo(
    () => (workspace ? buildSchemaForAsset(workspace, adhocAsset) : []),
    [adhocAsset, workspace],
  );

  useSQLIntellisense(
    monacoInstance,
    editorInstance,
    adhocAsset,
    editorValue,
    schemaTables,
    adhocAsset.upstreams ?? [],
    selectedEnvironment,
    onGoToAsset,
    undefined,
    {
      registerGlobalProviders: false,
      registerLegacyDiagnosticProviders: false,
      registerParseContextMarkers: false,
      registerSemanticDecorations: false,
    },
  );
  useSQLLSP(
    monacoInstance,
    editorInstance,
    adhocAsset,
    editorValue,
    schemaTables,
    onGoToAsset,
    undefined,
    { documentContext: "adhoc" },
  );
  useJinjaIntellisense(monacoInstance, editorInstance, adhocAsset, editorValue);
  useSQLCanvasHover(monacoInstance, editorInstance, adhocAsset, { documentContext: "adhoc" });

  // Monaco replaces the whole document when the `value` prop changes in a
  // post-commit effect, which can drop keystrokes if the live draft is fed
  // back. Only move the value handed to the editor for changes that did not
  // originate from the editor itself (pipeline switch, format).
  const lastEditorChangeRef = useRef<{ pipelineId: string; value: string } | null>(null);
  const displayValueRef = useRef<{ pipelineId: string | null; value: string }>({
    pipelineId: null,
    value: "",
  });
  const lastEditorChange = lastEditorChangeRef.current;
  const changeCameFromEditor =
    lastEditorChange?.pipelineId === pipelineId && lastEditorChange.value === editorValue;
  if (displayValueRef.current.pipelineId !== pipelineId || !changeCameFromEditor) {
    displayValueRef.current = { pipelineId, value: editorValue };
  }

  const formatSQL = useCallback(() => {
    if (!editorInstance) {
      return;
    }

    const content = editorInstance.getValue();
    void getSQLLSPFormatting({
      asset_id: contextAsset.id,
      content,
      connection: connection.name,
      document_context: "adhoc",
    })
      .then((response) => {
        if (response.status !== "ok") {
          return;
        }
        const formatted = Object.values(response.edit?.changes ?? {})[0]?.[0]?.newText;
        if (formatted !== undefined) {
          setEditorValue(formatted);
        }
      })
      .catch(() => undefined);
  }, [connection.name, contextAsset.id, editorInstance, setEditorValue]);

  useEffect(() => {
    if (!editorInstance || !monacoInstance) {
      return;
    }

    const subscription = editorInstance.onKeyDown((event) => {
      const ctrlOrCmd = event.ctrlKey || event.metaKey;
      if (!ctrlOrCmd) {
        return;
      }

      if (event.keyCode === monacoInstance.KeyCode.Enter) {
        event.preventDefault();
        event.stopPropagation();
        onRunQuery();
        return;
      }

      if (event.shiftKey && event.keyCode === monacoInstance.KeyCode.KeyI) {
        event.preventDefault();
        event.stopPropagation();
        formatSQL();
      }
    });

    return () => {
      subscription.dispose();
    };
  }, [editorInstance, formatSQL, monacoInstance, onRunQuery]);

  const handleBeforeMount = useCallback((monaco: Monaco) => {
    defineBruinMonacoThemes(monaco);
  }, []);

  const handleMount = useCallback(
    (editor: MonacoNS.editor.IStandaloneCodeEditor, monaco: Monaco) => {
      defineBruinMonacoThemes(monaco);
      setEditorInstance(editor);
      setMonacoInstance(monaco);
    },
    [],
  );

  return (
    <AssetCodeEditor
      asset={adhocAsset}
      containerClassName="min-h-0 flex-1"
      editorModelPath={`inmemory://bruin/adhoc/${pipelineId}.sql`}
      editorValue={displayValueRef.current.value}
      editorHighlighted={false}
      helpMode={false}
      isSqlAsset
      formatShortcutLabel="⌘ + ⇧ + I"
      mobile={false}
      monacoTheme={monacoTheme}
      onChange={(value) => {
        const nextValue = value ?? "";
        lastEditorChangeRef.current = { pipelineId, value: nextValue };
        setEditorValue(nextValue);
      }}
      onBeforeMount={handleBeforeMount}
      onFormat={formatSQL}
      onMount={handleMount}
    />
  );
}
