"use client";

import type { Monaco } from "@monaco-editor/react";
import type * as MonacoNS from "monaco-editor";
import { useCallback, useLayoutEffect, useMemo, useRef, useState } from "react";

import { AssetCodeEditor } from "@/components/asset-code-editor";
import { Textarea } from "@/components/ui/textarea";
import { useSQLLSP } from "@/hooks/use-sql-lsp";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";
import { getSQLLSPFormatting } from "@/lib/api-sql-lsp";
import { defineBruinMonacoThemes } from "@/lib/monaco-theme";
import { applyExternalModelValue } from "@/lib/monaco-model-sync";
import { buildSchemaForAsset } from "@/lib/sql-schema";
import type { WebAsset, WorkspaceQueryConnection, WorkspaceState } from "@/lib/types";

const PRESENTATION_QUERY_EDITOR_OPTIONS: MonacoNS.editor.IStandaloneEditorConstructionOptions = {
  ariaLabel: "Dataset SQL query",
  folding: false,
  glyphMargin: false,
  lineNumbersMinChars: 3,
  overviewRulerLanes: 0,
  scrollBeyondLastLine: false,
  scrollbar: { alwaysConsumeMouseWheel: false },
  wordWrap: "on",
};

export function PresentationQueryEditor({
  presentationId,
  datasetId,
  value,
  connection,
  workspace,
  compact = false,
  onChange,
}: {
  presentationId: string;
  datasetId: string;
  value: string;
  connection: WorkspaceQueryConnection | null;
  workspace: WorkspaceState | null;
  compact?: boolean;
  onChange: (value: string) => void;
}) {
  const { monacoTheme } = useWorkspaceTheme();
  const [monacoInstance, setMonacoInstance] = useState<Monaco | null>(null);
  const [editorInstance, setEditorInstance] =
    useState<MonacoNS.editor.IStandaloneCodeEditor | null>(null);
  const applyingExternalValueRef = useRef(false);
  const localModelValuesRef = useRef(new Set<string>());

  const queryAsset = useMemo<WebAsset | null>(() => {
    if (!connection) return null;
    return {
      id: `presentation-query:${presentationId}:${datasetId}`,
      name: "",
      type: connection.asset_type,
      path: `presentation-query-${datasetId || "dataset"}.sql`,
      content: "",
      upstreams: [],
      connection: connection.name,
      is_materialized: false,
    };
  }, [connection, datasetId, presentationId]);
  const schemaTables = useMemo(
    () => (workspace && queryAsset ? buildSchemaForAsset(workspace, queryAsset) : []),
    [queryAsset, workspace],
  );

  useSQLLSP(monacoInstance, editorInstance, queryAsset, value, schemaTables, undefined, undefined, {
    documentContext: "presentation_query",
  });

  useLayoutEffect(() => {
    if (!editorInstance || !monacoInstance) return;
    const model = editorInstance.getModel();
    if (!model) return;
    if (model.getValue() === value) {
      localModelValuesRef.current.clear();
      return;
    }
    if (localModelValuesRef.current.delete(value)) return;

    applyingExternalValueRef.current = true;
    try {
      applyExternalModelValue(editorInstance, monacoInstance, value);
      localModelValuesRef.current.clear();
    } finally {
      applyingExternalValueRef.current = false;
    }
  }, [editorInstance, monacoInstance, value]);

  const formatSQL = useCallback(() => {
    if (!editorInstance || !queryAsset || !connection) return;
    void getSQLLSPFormatting({
      asset_id: queryAsset.id,
      content: editorInstance.getValue(),
      connection: connection.name,
      document_context: "presentation_query",
    })
      .then((response) => {
        if (response.status !== "ok") return;
        const formatted = Object.values(response.edit?.changes ?? {})[0]?.[0]?.newText;
        if (formatted !== undefined) onChange(formatted);
      })
      .catch(() => undefined);
  }, [connection, editorInstance, onChange, queryAsset]);

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

  if (!queryAsset || !connection) {
    return (
      <Textarea
        aria-label="Dataset SQL query"
        value={value}
        className="min-h-28 resize-y font-mono text-xs"
        onChange={(event) => onChange(event.target.value)}
      />
    );
  }

  const modelID = `${encodeURIComponent(presentationId)}/${encodeURIComponent(datasetId)}`;
  return (
    <div
      data-testid="presentation-query-editor"
      className={
        compact
          ? "h-52 min-w-0 overflow-hidden rounded-md border"
          : "h-60 min-w-0 overflow-hidden rounded-md border"
      }
    >
      <AssetCodeEditor
        asset={queryAsset}
        containerClassName="h-full"
        editorModelPath={`inmemory://bruin/presentation-query/${modelID}.sql`}
        editorOptions={PRESENTATION_QUERY_EDITOR_OPTIONS}
        editorValue={value}
        editorValueMode="initial"
        editorHighlighted={false}
        helpMode={false}
        isSqlAsset
        formatShortcutLabel="⌘ + ⇧ + I"
        mobile={false}
        monacoTheme={monacoTheme}
        onChange={(next) => {
          const nextValue = next ?? "";
          if (applyingExternalValueRef.current) return;
          localModelValuesRef.current.add(nextValue);
          onChange(nextValue);
        }}
        onBeforeMount={handleBeforeMount}
        onFormat={formatSQL}
        onMount={handleMount}
      />
    </div>
  );
}
