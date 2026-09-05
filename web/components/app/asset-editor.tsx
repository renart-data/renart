"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertTriangle } from "lucide-react";

import { AssetCodeEditor } from "@/components/asset-code-editor";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { useAssetContentEditing } from "@/hooks/use-asset-content-editing";
import { useAssetMonaco } from "@/hooks/use-asset-monaco";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";
import { addAssetPythonDependency, AssetPythonDeps, getAssetPythonDeps } from "@/lib/api-assets";
import { usesPythonSource } from "@/lib/asset-types";
import { missingPythonImports } from "@/lib/notebook-python-deps";
import { WebAsset } from "@/lib/types";

import { MissingPythonDepsBanner } from "./missing-python-deps";
import { useResourceNavigation } from "@/hooks/use-resource-navigation";
import { sourceAnchorFingerprint } from "@/lib/deployment-diff-annotations";

/**
 * Monaco editor for a real workspace asset inside the app build page.
 * Reuses the same editing hooks as the classic workspace editor pane, so
 * intellisense, formatting, debounced saves, and go-to-definition behave
 * identically. Requires the build page to keep `routeSelectionAtom` in sync
 * with the asset being shown.
 */
export function AppAssetEditor({
  asset,
  pipelineId,
  onInspect,
  onGoToAsset,
  onImportExternalRelation,
  onGoToJinjaVariable,
}: {
  asset: WebAsset;
  pipelineId: string;
  onInspect?: () => void;
  onGoToAsset?: (pipelineId: string, assetId: string) => void;
  onImportExternalRelation?: (relationId: string) => void;
  onGoToJinjaVariable?: (variableName: string) => void;
}) {
  const { monacoTheme } = useWorkspaceTheme();
  const [lspActionError, setLSPActionError] = useState<string | null>(null);
  const { editorDisplayValue, editorValue, handleEditorChange, handleSaveSelectedAsset } =
    useAssetContentEditing({ asset, pipelineId });
  const {
    editorInstance,
    editorModelPath,
    formatSQL,
    handleBeforeMount,
    handleMount,
    isSqlAsset,
    shortcutLabel,
  } = useAssetMonaco({
    asset,
    editorValue,
    onGoToAsset,
    onImportExternalRelation,
    onLSPActionError: setLSPActionError,
    onGoToJinjaVariable,
    onInspect,
    onSave: handleSaveSelectedAsset,
  });
  const resource = useResourceNavigation();
  const target = resource.detail?.target;
  const sourceTarget =
    target?.kind === "asset-section" && target.section === "source" && target.asset_id === asset.id
      ? target
      : undefined;
  const sourceToken = JSON.stringify(sourceTarget);
  const staleSource = Boolean(
    sourceTarget?.source_fingerprint &&
    sourceTarget.source_fingerprint !== sourceAnchorFingerprint(editorValue),
  );
  useEffect(() => {
    if (!sourceTarget || !editorInstance || staleSource) return;
    editorInstance.focus();
    if (sourceTarget.line) {
      editorInstance.setSelection({
        startLineNumber: sourceTarget.line,
        startColumn: 1,
        endLineNumber: sourceTarget.end_line ?? sourceTarget.line,
        endColumn: 1,
      });
      editorInstance.revealLineInCenter(sourceTarget.line);
    }
  }, [editorInstance, sourceToken, staleSource]);

  const isPythonAsset = usesPythonSource(asset);
  const { missingImports, addDependency } = useAssetPythonDeps(
    asset.id,
    isPythonAsset,
    editorValue,
  );

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {staleSource ? (
        <p role="alert" className="p-2 text-xs">
          Source changed since this diagnostic. The old location is not highlighted.
        </p>
      ) : null}
      <AssetCodeEditor
        asset={asset}
        containerClassName="min-h-0 flex-1"
        editorModelPath={editorModelPath}
        editorValue={editorDisplayValue}
        editorHighlighted={false}
        helpMode={false}
        isSqlAsset={isSqlAsset}
        formatShortcutLabel={shortcutLabel}
        mobile={false}
        monacoTheme={monacoTheme}
        onChange={handleEditorChange}
        onBeforeMount={handleBeforeMount}
        onFormat={formatSQL}
        onMount={handleMount}
      />
      {missingImports.length > 0 ? (
        <div className="shrink-0 border-t p-2">
          <MissingPythonDepsBanner
            missingImports={missingImports}
            onAddDependency={addDependency}
          />
        </div>
      ) : null}
      {lspActionError ? (
        <Alert variant="destructive" className="m-2 shrink-0 w-auto">
          <AlertTriangle />
          <AlertTitle>Suggested change failed</AlertTitle>
          <AlertDescription>{lspActionError}</AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}

/**
 * Tracks a Python asset's declared dependencies and installed modules, deriving
 * the imports that are not yet covered. Mirrors the notebook hint so the same
 * "imported but not declared" banner and add-to-requirements flow work here.
 */
function useAssetPythonDeps(assetId: string, enabled: boolean, source: string) {
  const [deps, setDeps] = useState<AssetPythonDeps | null>(null);

  const refresh = useCallback(
    (signal?: AbortSignal) => {
      if (!enabled) {
        setDeps(null);
        return;
      }
      getAssetPythonDeps(assetId, signal)
        .then((result) => {
          if (!signal?.aborted) {
            setDeps(result);
          }
        })
        .catch(() => {
          if (!signal?.aborted) {
            setDeps(null);
          }
        });
    },
    [assetId, enabled],
  );

  useEffect(() => {
    const controller = new AbortController();
    refresh(controller.signal);
    return () => controller.abort();
  }, [refresh]);

  const addDependency = useCallback(
    async (pkg: string) => {
      try {
        const result = await addAssetPythonDependency(assetId, pkg);
        setDeps(result);
      } catch {
        // Leave the banner in place; the user can retry.
      }
    },
    [assetId],
  );

  const missingImports = useMemo(() => {
    if (!enabled || !deps) {
      return [];
    }
    return missingPythonImports(source, deps.dependencies, deps.installed_modules);
  }, [enabled, deps, source]);

  return { missingImports, addDependency };
}
