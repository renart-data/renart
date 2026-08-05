"use client";

import type * as MonacoNS from "monaco-editor";
import { useSetAtom } from "jotai";
import { useEffect } from "react";

import { getSQLLSPDefinition } from "@/lib/api-sql-lsp";
import { isQuerySensorAssetType, isSqlAssetType } from "@/lib/asset-types";
import { sqlHoveredAssetAtom } from "@/lib/atoms/domains/editor";
import type { WebAsset } from "@/lib/types";

const relationHoverDelay = 120;

/**
 * Resolves the SQL symbol under the pointer through the same definition
 * endpoint used by Monaco navigation, then exposes asset-backed relations to
 * the adjacent pipeline canvas. Moving within one token is deliberately
 * deduplicated so ordinary pointer motion does not spam the LSP endpoint.
 */
export function useSQLCanvasHover(
  monaco: typeof MonacoNS | null,
  editor: MonacoNS.editor.IStandaloneCodeEditor | null,
  asset: WebAsset | null,
  options?: { documentContext?: "asset" | "adhoc" | "custom_check" },
) {
  const setHoveredAsset = useSetAtom(sqlHoveredAssetAtom);

  useEffect(() => {
    const sqlAsset = asset && (isSqlAssetType(asset.type) || isQuerySensorAssetType(asset.type));
    const model = editor?.getModel();
    if (!monaco || !editor || !asset || !sqlAsset || !model) {
      setHoveredAsset(null);
      return;
    }

    let timer = 0;
    let requestSerial = 0;
    let lastTokenKey = "";

    const clear = (resetToken = true) => {
      window.clearTimeout(timer);
      requestSerial += 1;
      if (resetToken) lastTokenKey = "";
      setHoveredAsset(null);
    };

    const mouseMove = editor.onMouseMove((event) => {
      // Monaco clamps CONTENT_EMPTY positions after a short line to the
      // line's final column. Calling getWordAtPosition there therefore returns
      // the last relation token even when the pointer is far to its right.
      // Only actual painted text is eligible for canvas highlighting.
      if (event.target.type !== monaco.editor.MouseTargetType.CONTENT_TEXT) {
        if (lastTokenKey) clear();
        return;
      }
      const position = event.target.position;
      const word = position ? model.getWordAtPosition(position) : null;
      if (!position || !word) {
        if (lastTokenKey) clear();
        return;
      }

      const tokenKey = `${model.getVersionId()}:${position.lineNumber}:${word.startColumn}:${word.endColumn}`;
      if (tokenKey === lastTokenKey) return;
      lastTokenKey = tokenKey;
      clear(false);
      const serial = requestSerial;

      timer = window.setTimeout(() => {
        void getSQLLSPDefinition({
          asset_id: asset.id,
          content: model.getValue(),
          connection: asset.connection?.trim() || undefined,
          document_context: options?.documentContext ?? "asset",
          position: { line: position.lineNumber - 1, character: position.column - 1 },
        })
          .then((response) => {
            if (serial !== requestSerial) return;
            const location = (response.locations ?? []).find((candidate) => candidate.asset_id);
            setHoveredAsset(location?.asset_id ?? null);
          })
          .catch(() => {
            if (serial === requestSerial) setHoveredAsset(null);
          });
      }, relationHoverDelay);
    });
    const mouseLeave = editor.onMouseLeave(() => clear());
    const blur = editor.onDidBlurEditorWidget(() => clear());
    const modelChange = editor.onDidChangeModelContent(() => clear());

    return () => {
      clear();
      mouseMove.dispose();
      mouseLeave.dispose();
      blur.dispose();
      modelChange.dispose();
    };
  }, [asset, editor, monaco, options?.documentContext, setHoveredAsset]);
}
