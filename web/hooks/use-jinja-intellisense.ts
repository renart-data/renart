"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import type * as MonacoNS from "monaco-editor";
import { useAtomValue } from "jotai";

import {
  JinjaRenderResponse,
  parseJinjaSpans,
  registerJinjaProviders,
  renderJinjaAsset,
} from "@/lib/jinja-intellisense";
import { selectedExecutionTimeWindowAtom } from "@/lib/atoms/domains/workspace";
import { isAPIAssetType, isQuerySensorAssetType, isSqlAssetType } from "@/lib/asset-types";
import { WebAsset } from "@/lib/types";

// One global Jinja provider set per Monaco, shared across editors. Each editor
// registers its render-result getter by model URI; the providers resolve the
// getter for the model they run on. Registering per editor (as before) meant N
// mounted cell editors produced N duplicated Jinja suggestions.
const jinjaRenderGetters = new Map<string, () => JinjaRenderResponse | null>();
const jinjaVariableDefinitionHandlers = new Map<string, (variableName: string) => void>();
const jinjaProviderRegistry = new Map<
  typeof MonacoNS,
  { disposable: MonacoNS.IDisposable; refs: number }
>();

function acquireJinjaProviders(monaco: typeof MonacoNS): () => void {
  let registration = jinjaProviderRegistry.get(monaco);
  if (!registration) {
    registration = {
      disposable: registerJinjaProviders(
        monaco,
        (model) => jinjaRenderGetters.get(model.uri.toString())?.() ?? null,
        (model, variableName) =>
          jinjaVariableDefinitionHandlers.get(model.uri.toString())?.(variableName),
      ),
      refs: 0,
    };
    jinjaProviderRegistry.set(monaco, registration);
  }
  registration.refs += 1;
  return () => {
    const current = jinjaProviderRegistry.get(monaco);
    if (!current) {
      return;
    }
    current.refs -= 1;
    if (current.refs <= 0) {
      current.disposable.dispose();
      jinjaProviderRegistry.delete(monaco);
    }
  };
}

export function useJinjaIntellisense(
  monaco: typeof MonacoNS | null,
  editor: MonacoNS.editor.IStandaloneCodeEditor | null,
  asset: WebAsset | null,
  content: string,
  onGoToVariable?: (variableName: string) => void,
  namespaces?: Record<string, JinjaRenderResponse["variables"]>,
  parameterValues?: Record<string, unknown>,
) {
  const [renderResult, setRenderResult] = useState<JinjaRenderResponse | null>(null);
  const selectedExecutionTimeWindow = useAtomValue(selectedExecutionTimeWindowAtom);
  const renderResultRef = useRef<JinjaRenderResponse | null>(null);
  const goToVariableRef = useRef(onGoToVariable);
  const namespacesRef = useRef(namespaces);
  renderResultRef.current = renderResult;
  goToVariableRef.current = onGoToVariable;
  namespacesRef.current = namespaces;
  const assetId = asset?.id ?? null;
  const isSqlAsset =
    asset !== null &&
    (asset.path.toLowerCase().endsWith(".sql") ||
      isSqlAssetType(asset.type) ||
      isQuerySensorAssetType(asset.type));
  const supportsJinjaPreview = isSqlAsset || (asset !== null && isAPIAssetType(asset.type));
  const spanKey = useMemo(
    () =>
      JSON.stringify(
        parseJinjaSpans(content).map((span) => [span.startOffset, span.endOffset, span.content]),
      ),
    [content],
  );

  useEffect(() => {
    if (!monaco || !editor) return;
    const model = editor.getModel();
    if (!model) return;
    const uri = model.uri.toString();
    jinjaRenderGetters.set(uri, () => {
      const currentNamespaces = namespacesRef.current;
      const rendered = renderResultRef.current;
      if (rendered) {
        return { ...rendered, namespaces: currentNamespaces };
      }
      if (currentNamespaces && Object.keys(currentNamespaces).length > 0) {
        return {
          status: "ok",
          spans: [],
          variables: [],
          macros: [],
          namespaces: currentNamespaces,
        };
      }
      return null;
    });
    jinjaVariableDefinitionHandlers.set(uri, (variableName) =>
      goToVariableRef.current?.(variableName),
    );
    const release = acquireJinjaProviders(monaco);
    return () => {
      jinjaRenderGetters.delete(uri);
      jinjaVariableDefinitionHandlers.delete(uri);
      release();
    };
  }, [monaco, editor]);

  useEffect(() => {
    if (!assetId || !supportsJinjaPreview || !content.includes("{")) {
      setRenderResult(null);
      return;
    }

    const controller = new AbortController();
    const timer = window.setTimeout(async () => {
      try {
        const response = await renderJinjaAsset({
          assetId,
          content,
          timeWindow: selectedExecutionTimeWindow,
          parameterValues,
          signal: controller.signal,
        });
        if (!controller.signal.aborted) {
          setRenderResult(response);
        }
      } catch {
        if (!controller.signal.aborted) {
          setRenderResult(null);
        }
      }
    }, 350);

    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [assetId, content, parameterValues, selectedExecutionTimeWindow, supportsJinjaPreview]);

  useEffect(() => {
    if (!editor || !monaco || !supportsJinjaPreview) return;

    let decorations: string[] = [];
    let updatingDecorations = false;
    const update = () => {
      if (updatingDecorations) return;
      updatingDecorations = true;
      try {
        const model = editor.getModel();
        if (!model) return;
        const position = editor.getPosition();
        const cursorOffset = position ? model.getOffsetAt(position) : -1;
        const spans = parseJinjaSpans(model.getValue());
        const renderSpans = renderResultRef.current?.spans ?? [];

        decorations = editor.deltaDecorations(
          decorations,
          spans.map((span) => {
            const inside = cursorOffset >= span.startOffset && cursorOffset <= span.endOffset;
            const rendered = renderSpans.find(
              (candidate) =>
                candidate.start_line === span.startLine &&
                candidate.start_column === span.startColumn,
            );
            const afterContent =
              !inside && span.type === "expression" && rendered?.rendered_text
                ? `  → ${rendered.rendered_text}`
                : "";
            return {
              range: new monaco.Range(
                span.startLine,
                span.startColumn,
                span.endLine,
                span.endColumn,
              ),
              options: {
                inlineClassName:
                  span.type === "expression"
                    ? "bruin-jinja-expression"
                    : "bruin-jinja-statement",
                after: afterContent
                  ? {
                      content:
                        afterContent.length > 80 ? `${afterContent.slice(0, 77)}...` : afterContent,
                      inlineClassName: "bruin-jinja-rendered-ghost",
                    }
                  : undefined,
              },
            };
          }),
        );
      } finally {
        updatingDecorations = false;
      }
    };

    update();
    const disposables = [
      editor.onDidChangeCursorPosition(update),
      editor.onDidChangeModelContent(update),
    ];

    return () => {
      decorations = editor.deltaDecorations(decorations, []);
      disposables.forEach((disposable) => disposable.dispose());
    };
  }, [editor, monaco, renderResult, spanKey, supportsJinjaPreview]);
}
