"use client";

import type { Monaco } from "@monaco-editor/react";
import type * as MonacoNS from "monaco-editor";
import { useAtomValue, useSetAtom } from "jotai";
import { useCallback, useEffect, useMemo, useState } from "react";

import { useAssetEditorSchemaInference } from "@/hooks/use-asset-editor-schema-inference";
import { useJinjaIntellisense } from "@/hooks/use-jinja-intellisense";
import { usePythonIntellisense } from "@/hooks/use-python-intellisense";
import { usePythonQueryIntellisense } from "@/hooks/use-python-query-intellisense";
import { useSQLFormatting } from "@/hooks/use-sql-formatting";
import { useSQLCanvasHover } from "@/hooks/use-sql-canvas-hover";
import { useSQLIntellisense } from "@/hooks/use-sql-intellisense";
import { useSQLLSP } from "@/hooks/use-sql-lsp";
import { useYAMLIntellisense } from "@/hooks/use-yaml-intellisense";
import {
  registerAssetColumnsAtom,
  selectedAssetSchemaSuggestionTablesAtom,
  selectedAssetSchemaTablesAtom,
} from "@/lib/atoms/domains/suggestions";
import { selectedEnvironmentAtom } from "@/lib/atoms/domains/workspace";
import { InspectDiagnosticSnapshot } from "@/lib/inspect-diagnostics";
import { isQuerySensorAssetType } from "@/lib/asset-types";
import { defineBruinMonacoThemes } from "@/lib/monaco-theme";
import { WebAsset } from "@/lib/types";

/**
 * Wires a Monaco editor for a Bruin asset: theme registration, language
 * intellisense (SQL/Jinja/Python/YAML), SQL formatting, upstream schema
 * inference, and the save/inspect keyboard shortcuts.
 *
 * Schema tables and the selected environment come from the global selection
 * atoms, so the host page must keep `routeSelectionAtom` pointed at the
 * asset shown in the editor.
 */
export function useAssetMonaco({
  asset,
  editorValue,
  inspectDiagnosticSnapshot = null,
  onGoToAsset,
  onImportExternalRelation,
  onGoToJinjaVariable,
  onInspect,
  onSave,
}: {
  asset: WebAsset | null;
  editorValue: string;
  inspectDiagnosticSnapshot?: InspectDiagnosticSnapshot | null;
  onGoToAsset?: (pipelineId: string, assetId: string) => void;
  onImportExternalRelation?: (relationId: string) => void;
  onGoToJinjaVariable?: (variableName: string) => void;
  onInspect?: () => void;
  onSave?: () => void | Promise<unknown>;
}) {
  const [monacoInstance, setMonacoInstance] = useState<Monaco | null>(null);
  const [editorInstance, setEditorInstance] =
    useState<MonacoNS.editor.IStandaloneCodeEditor | null>(null);

  const schemaTables = useAtomValue(selectedAssetSchemaTablesAtom);
  const schemaSuggestionTables = useAtomValue(selectedAssetSchemaSuggestionTablesAtom);
  const registerAssetColumns = useSetAtom(registerAssetColumnsAtom);
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);

  useAssetEditorSchemaInference({
    asset,
    schemaSuggestionTables,
    onRegisterAssetColumns: registerAssetColumns,
  });

  useSQLIntellisense(
    monacoInstance,
    editorInstance,
    asset,
    editorValue,
    schemaTables,
    asset?.upstreams ?? [],
    selectedEnvironment,
    onGoToAsset,
    inspectDiagnosticSnapshot,
    {
      registerGlobalProviders: false,
      registerLegacyDiagnosticProviders: false,
      registerParseContextMarkers: false,
      registerSemanticDecorations: false,
    },
  );
  useSQLLSP(monacoInstance, editorInstance, asset, editorValue, schemaTables, onGoToAsset, undefined, {
    onImportExternalRelation,
  });
  useSQLCanvasHover(monacoInstance, editorInstance, asset);
  useJinjaIntellisense(monacoInstance, editorInstance, asset, editorValue, onGoToJinjaVariable);
  usePythonIntellisense(monacoInstance, editorInstance, asset, editorValue);
  usePythonQueryIntellisense(
    monacoInstance,
    editorInstance,
    asset,
    editorValue,
    schemaTables,
    onGoToAsset,
  );
  useYAMLIntellisense(monacoInstance, editorInstance, asset);

  const { formatSQL, isSqlAsset, shortcutLabel } = useSQLFormatting(
    asset,
    editorInstance,
    monacoInstance,
  );

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

  useEffect(() => {
    if (!editorInstance || !monacoInstance) {
      return;
    }

    const subscription = editorInstance.onKeyDown((event) => {
      const ctrlOrCmd = event.ctrlKey || event.metaKey;
      if (!ctrlOrCmd) {
        return;
      }

      if (event.keyCode === monacoInstance.KeyCode.KeyS) {
        event.preventDefault();
        event.stopPropagation();
        void onSave?.();
        return;
      }

      if (event.keyCode === monacoInstance.KeyCode.Enter) {
        event.preventDefault();
        event.stopPropagation();
        onInspect?.();
      }
    });

    return () => {
      subscription.dispose();
    };
  }, [editorInstance, monacoInstance, onInspect, onSave]);

  const editorModelPath = useMemo(() => {
    if (!asset) {
      return "inmemory://bruin/no-selection.sql";
    }

    const extension = isQuerySensorAssetType(asset.type)
      ? "sql"
      : (asset.path.split(".").pop()?.toLowerCase() ?? "sql");
    return `inmemory://bruin/assets/${asset.id}.${extension}`;
  }, [asset]);

  return {
    editorInstance,
    editorModelPath,
    formatSQL,
    handleBeforeMount,
    handleMount,
    isSqlAsset,
    monacoInstance,
    shortcutLabel,
  };
}
