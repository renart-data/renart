"use client";

import type { DiffOnMount, Monaco } from "@monaco-editor/react";
import { ArrowRight, Loader2, RotateCcw } from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type * as MonacoNS from "monaco-editor";

import { SemanticImpactPeek } from "@/components/app/semantic-impact-peek";
import { Button } from "@/components/ui/button";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";
import {
  analyzeSemanticDiffDraft,
  type SemanticDiffInlineAnchor,
  type SemanticDiffInlineFinding,
} from "@/lib/semantic-diff-inline";
import type { SemanticDiffDemoScenario } from "@/lib/semantic-diff-demo";
import {
  buildSemanticImpactLens,
  type SemanticImpactLensPreset,
} from "@/lib/semantic-impact-playground";
import { loadMonacoEditorModule } from "@/lib/load-monaco-editor";
import { defineBruinMonacoThemes } from "@/lib/monaco-theme";

const MonacoDiffEditor = lazy(async () => {
  const module = await loadMonacoEditorModule();
  return { default: module.DiffEditor };
});

type MountedDiffEditor = MonacoNS.editor.IStandaloneDiffEditor;

export function SemanticDiffInlineEditor({
  scenario,
  initialDraft,
  onDraftChange,
}: {
  scenario: SemanticDiffDemoScenario;
  initialDraft?: string;
  onDraftChange?: (sql: string) => void;
}) {
  const initialSqlRef = useRef(initialDraft ?? scenario.after.sql);
  const onDraftChangeRef = useRef(onDraftChange);
  onDraftChangeRef.current = onDraftChange;
  const [draftSql, setDraftSql] = useState(initialSqlRef.current);
  const [mountedEditor, setMountedEditor] = useState<MountedDiffEditor | null>(null);
  const [monaco, setMonaco] = useState<Monaco | null>(null);
  const [activeFindingId, setActiveFindingId] = useState<string>();
  const [impactOpen, setImpactOpen] = useState(false);
  const modelListenerRef = useRef<MonacoNS.IDisposable | null>(null);
  const originalDecorationIdsRef = useRef<string[]>([]);
  const modifiedDecorationIdsRef = useRef<string[]>([]);
  const { monacoTheme } = useWorkspaceTheme();
  const analysis = useMemo(
    () => analyzeSemanticDiffDraft(scenario, draftSql),
    [draftSql, scenario],
  );
  const impactLens = useMemo(
    () => buildSemanticImpactLens(scenario, draftSql, analysis, activeFindingId),
    [activeFindingId, analysis, draftSql, scenario],
  );

  const beforeMount = useCallback((instance: Monaco) => defineBruinMonacoThemes(instance), []);
  const onMount = useCallback<DiffOnMount>((editor, instance) => {
    modelListenerRef.current?.dispose();
    const modifiedEditor = editor.getModifiedEditor();
    const model = modifiedEditor.getModel();

    if (model) {
      setDraftSql(model.getValue());
      modelListenerRef.current = model.onDidChangeContent(() => {
        setDraftSql(model.getValue());
        onDraftChangeRef.current?.(model.getValue());
      });
    }

    setMountedEditor(editor);
    setMonaco(instance);
  }, []);

  useEffect(
    () => () => {
      modelListenerRef.current?.dispose();
      modelListenerRef.current = null;
    },
    [],
  );

  useEffect(() => {
    setDraftSql(initialSqlRef.current);
    setImpactOpen(false);
    setActiveFindingId(undefined);
    if (!mountedEditor) return;

    const originalModel = mountedEditor.getOriginalEditor().getModel();
    const modifiedModel = mountedEditor.getModifiedEditor().getModel();
    if (originalModel?.getValue() !== scenario.before.sql) {
      originalModel?.setValue(scenario.before.sql);
    }
    if (modifiedModel?.getValue() !== initialSqlRef.current) {
      modifiedModel?.setValue(initialSqlRef.current);
    }
  }, [mountedEditor, scenario]);

  useEffect(() => {
    setActiveFindingId((current) =>
      analysis.findings.some((finding) => finding.id === current)
        ? current
        : impactOpen
          ? analysis.findings[0]?.id
          : undefined,
    );
  }, [analysis.findings, impactOpen]);

  useEffect(() => {
    if (!mountedEditor || !monaco) return;

    const originalEditor = mountedEditor.getOriginalEditor();
    const modifiedEditor = mountedEditor.getModifiedEditor();
    originalDecorationIdsRef.current = originalEditor.deltaDecorations(
      originalDecorationIdsRef.current,
      decorationsForFindings(monaco, originalEditor, analysis.findings, "before", activeFindingId),
    );
    modifiedDecorationIdsRef.current = modifiedEditor.deltaDecorations(
      modifiedDecorationIdsRef.current,
      decorationsForFindings(monaco, modifiedEditor, analysis.findings, "after", activeFindingId),
    );

    return () => {
      originalDecorationIdsRef.current = originalEditor.deltaDecorations(
        originalDecorationIdsRef.current,
        [],
      );
      modifiedDecorationIdsRef.current = modifiedEditor.deltaDecorations(
        modifiedDecorationIdsRef.current,
        [],
      );
    };
  }, [activeFindingId, analysis.findings, monaco, mountedEditor]);

  useEffect(() => {
    if (!mountedEditor) return;
    const listeners = (["before", "after"] as const).map((side) => {
      const editor =
        side === "before" ? mountedEditor.getOriginalEditor() : mountedEditor.getModifiedEditor();
      return editor.onMouseDown((event) => {
        if (!event.target.element?.closest(".semantic-diff-lens")) return;
        const finding = analysis.findings.find(
          (item) => item[side]?.lineNumber === event.target.position?.lineNumber,
        );
        if (finding) {
          setActiveFindingId(finding.id);
          setImpactOpen(true);
        }
      });
    });
    return () => listeners.forEach((listener) => listener.dispose());
  }, [analysis.findings, mountedEditor]);

  const applyDraft = (sql: string) => {
    const model = mountedEditor?.getModifiedEditor().getModel();
    if (model) model.setValue(sql);
    setDraftSql(sql);
    onDraftChangeRef.current?.(sql);
  };

  const resetDraft = () => applyDraft(scenario.after.sql);

  const applyPreset = (preset: SemanticImpactLensPreset) => applyDraft(preset.sql);

  const revealFinding = (finding: SemanticDiffInlineFinding) => {
    setActiveFindingId(finding.id);
    setImpactOpen(true);
    if (!mountedEditor) return;
    const side = finding.after ? "after" : "before";
    const anchor = finding.after ?? finding.before;
    if (!anchor) return;
    const editor =
      side === "after" ? mountedEditor.getModifiedEditor() : mountedEditor.getOriginalEditor();
    editor.revealLineInCenter(anchor.lineNumber);
    editor.setPosition({ lineNumber: anchor.lineNumber, column: anchor.startColumn });
    editor.focus();
  };

  return (
    <div className="overflow-hidden bg-background">
      <div className="flex items-center justify-between gap-2 border-b bg-muted/8 px-5 py-1.5 sm:px-6">
        <div className="flex flex-wrap items-center gap-2 text-[0.625rem] text-muted-foreground">
          <span>Deployed</span>
          <ArrowRight className="size-3" />
          <span>Working draft · editable</span>
        </div>
        <Button
          variant="ghost"
          size="xs"
          disabled={analysis.matchesSavedCandidate}
          onClick={resetDraft}
        >
          <RotateCcw /> Reset
        </Button>
      </div>

      <div className="h-[12rem] min-w-0">
        <Suspense
          fallback={
            <div className="flex h-full items-center justify-center gap-2 text-xs text-muted-foreground">
              <Loader2 className="size-3.5 animate-spin" />
              Loading interactive SQL editor…
            </div>
          }
        >
          <MonacoDiffEditor
            original={scenario.before.sql}
            modified={initialSqlRef.current}
            originalLanguage="sql"
            modifiedLanguage="sql"
            originalModelPath={`inmemory://renart/semantic-diff/${scenario.id}/deployed.sql`}
            modifiedModelPath={`inmemory://renart/semantic-diff/${scenario.id}/candidate.sql`}
            keepCurrentOriginalModel
            keepCurrentModifiedModel
            theme={monacoTheme}
            beforeMount={beforeMount}
            onMount={onMount}
            options={{
              accessibilitySupport: "auto",
              automaticLayout: true,
              diffAlgorithm: "advanced",
              diffCodeLens: false,
              enableSplitViewResizing: true,
              folding: false,
              fontSize: 12,
              glyphMargin: true,
              hideUnchangedRegions: { enabled: false },
              ignoreTrimWhitespace: false,
              lineDecorationsWidth: 14,
              lineHeight: 21,
              lineNumbersMinChars: 2,
              minimap: { enabled: false },
              originalAriaLabel: "Deployed SQL",
              originalEditable: false,
              padding: { top: 12, bottom: 12 },
              readOnly: false,
              renderMarginRevertIcon: false,
              renderOverviewRuler: false,
              renderSideBySide: true,
              scrollBeyondLastLine: false,
              stickyScroll: { enabled: false },
              useInlineViewWhenSpaceIsLimited: true,
              wordWrap: "on",
            }}
          />
        </Suspense>
      </div>

      {analysis.findings.length > 1 ? (
        <div className="flex flex-wrap gap-2 border-t px-5 py-2">
          {analysis.findings.map((finding) => (
            <button
              key={finding.id}
              type="button"
              onClick={() => revealFinding(finding)}
              className="text-xs underline decoration-dotted underline-offset-4"
            >
              {finding.title}
            </button>
          ))}
        </div>
      ) : null}

      <SemanticImpactPeek
        lens={impactLens}
        open={impactOpen}
        onOpenChange={setImpactOpen}
        onApplyPreset={applyPreset}
      />
    </div>
  );
}

function decorationsForFindings(
  monaco: Monaco,
  editor: MonacoNS.editor.IStandaloneCodeEditor,
  findings: SemanticDiffInlineFinding[],
  side: "before" | "after",
  activeFindingId: string | undefined,
) {
  const model = editor.getModel();
  if (!model) return [];

  return findings.flatMap((finding) => {
    const anchor = finding[side];
    if (!anchor || anchor.lineNumber > model.getLineCount()) return [];
    return decorationsForAnchor(monaco, model, anchor, finding, finding.id === activeFindingId);
  });
}

function decorationsForAnchor(
  monaco: Monaco,
  model: MonacoNS.editor.ITextModel,
  anchor: SemanticDiffInlineAnchor,
  finding: SemanticDiffInlineFinding,
  active: boolean,
): MonacoNS.editor.IModelDeltaDecoration[] {
  const lineLength = model.getLineLength(anchor.lineNumber);
  const startColumn = Math.min(Math.max(1, anchor.startColumn), lineLength + 1);
  const endColumn = Math.min(Math.max(startColumn, anchor.endColumn), lineLength + 1);
  const lineEndColumn = lineLength + 1;
  const classSuffix = finding.tone === "warning" ? "warning" : "safe";
  const hoverMessage = {
    value: `**${finding.title}**\n\n${finding.detail}`,
  };

  return [
    {
      range: new monaco.Range(anchor.lineNumber, startColumn, anchor.lineNumber, endColumn),
      options: {
        inlineClassName: `semantic-diff-token-${classSuffix}`,
        hoverMessage,
        stickiness: monaco.editor.TrackedRangeStickiness.NeverGrowsWhenTypingAtEdges,
      },
    },
    {
      range: new monaco.Range(anchor.lineNumber, 1, anchor.lineNumber, lineEndColumn),
      options: {
        className: active ? `semantic-diff-line-active-${classSuffix}` : undefined,
        after: {
          content: `  ${active ? "◆" : "◇"} ${anchor.label}`,
          inlineClassName: `semantic-diff-lens semantic-diff-lens-${classSuffix}`,
          inlineClassNameAffectsLetterSpacing: true,
          cursorStops: monaco.editor.InjectedTextCursorStops.None,
        },
        hoverMessage,
        linesDecorationsClassName: `semantic-diff-gutter-${classSuffix}`,
        stickiness: monaco.editor.TrackedRangeStickiness.NeverGrowsWhenTypingAtEdges,
      },
    },
  ];
}
