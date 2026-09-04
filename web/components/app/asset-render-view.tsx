"use client";

import type { Monaco } from "@monaco-editor/react";
import type { editor } from "monaco-editor";
import { AlertTriangle, Check, Copy, FileCode2, GitCompareArrows, ShieldCheck } from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { ScrollArea } from "@/components/ui/scroll-area";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";
import type {
  AssetRenderFidelity,
  AssetRenderStageComparison,
  AssetRenderResult,
  AssetRenderStage,
  AssetRenderStageStatus,
  PipelineAssetRenderComparison,
} from "@/lib/api-asset-render";
import { comparePipelineAssetRenders } from "@/lib/api-asset-render";
import { copyTextToClipboard } from "@/lib/copy-to-clipboard";
import { listSnapshots, type SnapshotSummary } from "@/lib/api-deploy";
import { loadMonacoEditorModule } from "@/lib/load-monaco-editor";
import { defineBruinMonacoThemes } from "@/lib/monaco-theme";
import { cn } from "@/lib/utils";
import type { DiffAnnotation } from "@/lib/deployment-diff-annotations";

const MonacoEditor = lazy(async () => {
  const module = await loadMonacoEditorModule();
  return { default: module.default };
});

const MonacoDiffEditor = lazy(async () => {
  const module = await loadMonacoEditorModule();
  return { default: module.DiffEditor };
});

export function AssetRenderView({
  result,
  loading,
  error,
  onRetry,
  pipelineId,
}: {
  result: AssetRenderResult | null;
  loading: boolean;
  error: string | null;
  onRetry: () => void;
  pipelineId?: string;
}) {
  const [selectedStage, setSelectedStage] = useState("");
  const [copied, setCopied] = useState(false);
  const [comparisonOpen, setComparisonOpen] = useState(false);
  const [comparison, setComparison] = useState<PipelineAssetRenderComparison | null>(null);
  const [comparisonLoading, setComparisonLoading] = useState(false);
  const [comparisonError, setComparisonError] = useState<string | null>(null);
  const [snapshots, setSnapshots] = useState<SnapshotSummary[]>([]);
  const [selectedSnapshot, setSelectedSnapshot] = useState("latest");
  const [selectedComparisonStage, setSelectedComparisonStage] = useState("");
  const comparisonRequestId = useRef(0);
  const snapshotRequestId = useRef(0);
  const stageKeys = useMemo(
    () => result?.stages.map((stage, index) => `${index}:${stage.kind}`) ?? [],
    [result],
  );

  useEffect(() => {
    setSelectedStage(
      stageKeys.find((key, index) => result?.stages[index]?.content) ?? stageKeys[0] ?? "",
    );
    setCopied(false);
  }, [result?.asset.id, result?.provenance.source.merkle_root, stageKeys]);

  const resultIdentity = result
    ? `${result.asset.name}:${result.provenance.source.kind}:${result.provenance.source.merkle_root}`
    : "";
  useEffect(() => {
    comparisonRequestId.current += 1;
    snapshotRequestId.current += 1;
    setComparisonOpen(false);
    setComparison(null);
    setComparisonError(null);
    setComparisonLoading(false);
    setSnapshots([]);
    setSelectedSnapshot("latest");
    setSelectedComparisonStage("");
  }, [resultIdentity, pipelineId]);

  useEffect(() => {
    if (!comparisonOpen || !pipelineId) return;
    const requestId = ++snapshotRequestId.current;
    void listSnapshots(pipelineId)
      .then(({ snapshots: availableSnapshots }) => {
        if (snapshotRequestId.current === requestId) setSnapshots(availableSnapshots);
      })
      .catch(() => {
        if (snapshotRequestId.current === requestId) setSnapshots([]);
      });
  }, [comparisonOpen, pipelineId]);

  const loadComparison = useCallback(
    async (snapshotVersion: string) => {
      if (!pipelineId || !result) return;
      const requestId = ++comparisonRequestId.current;
      setComparisonLoading(true);
      setComparisonError(null);
      try {
        const context = result.provenance.context;
        const next = await comparePipelineAssetRenders(pipelineId, {
          asset_name: result.asset.name,
          snapshot_version_id: snapshotVersion === "latest" ? undefined : snapshotVersion,
          environment: context.environment,
          start_date: context.start_date,
          end_date: context.end_date,
          execution_time: context.execution_time,
          full_refresh: context.requested_full_refresh,
        });
        if (comparisonRequestId.current === requestId) setComparison(next);
      } catch (cause) {
        if (comparisonRequestId.current === requestId) {
          setComparisonError(
            cause instanceof Error ? cause.message : "Rendered operation comparison failed.",
          );
        }
      } finally {
        if (comparisonRequestId.current === requestId) setComparisonLoading(false);
      }
    },
    [pipelineId, result],
  );

  useEffect(() => {
    const comparisonStages = comparison?.stages ?? [];
    setSelectedComparisonStage(
      comparisonStages.find(
        (item) =>
          item.status !== "unchanged" &&
          Boolean(item.working_tree?.content || item.deployment?.content),
      )?.key ??
        comparisonStages.find((item) => item.working_tree?.content || item.deployment?.content)
          ?.key ??
        comparisonStages[0]?.key ??
        "",
    );
  }, [comparison]);

  const selectedIndex = stageKeys.indexOf(selectedStage);
  const stage = selectedIndex >= 0 ? result?.stages[selectedIndex] : result?.stages[0];
  const canCompare = Boolean(
    pipelineId && result?.provenance.source.kind === "working_tree" && !loading,
  );
  const copyStage = useCallback(async () => {
    if (!stage?.content || !(await copyTextToClipboard(stage.content))) return;
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1400);
  }, [stage?.content]);

  if (loading && !result) {
    return <RenderCentered loading message="Rendering saved asset…" />;
  }
  if (error && !result) {
    return (
      <div className="flex h-full min-h-0 items-center justify-center overflow-auto p-3">
        <Alert variant="destructive" className="max-w-xl">
          <AlertTriangle />
          <AlertTitle>Render failed</AlertTitle>
          <AlertDescription className="flex items-center justify-between gap-3">
            <span>{error}</span>
            <Button variant="outline" size="xs" onClick={onRetry}>
              Retry
            </Button>
          </AlertDescription>
        </Alert>
      </div>
    );
  }
  if (!result) {
    return <RenderCentered message="Render an asset to preview its saved operations here." />;
  }

  const context = result.provenance.context;
  const sourceLabel =
    result.provenance.source.kind === "working_tree"
      ? `Saved workspace · ${result.provenance.source.merkle_root.slice(0, 8)}`
      : `Deployment · ${result.provenance.source.merkle_root.slice(0, 8)}`;
  const configurationTitle = context.configuration_digest
    ? `Configuration ${context.configuration_digest.slice(0, 8)} · ${context.configuration_fidelity}`
    : context.configuration_message || "Configuration identity is only available at runtime";
  const variableProvenance = context.variable_provenance ?? [];
  const variableTitle = variableProvenance
    .map((variable) => `${variable.name} — ${variableSourceLabel(variable.source)}`)
    .join("\n");
  const fingerprintTitle = result.asset.fingerprint
    ? `Asset/DAG fingerprint ${result.asset.fingerprint}`
    : "Asset/DAG fingerprint unavailable";
  const target = result.asset.target;
  const targetTitle = [
    target.object ? `${target.kind}: ${target.object}` : target.kind,
    target.identity ? `Physical target ${target.identity}` : target.message,
  ]
    .filter(Boolean)
    .join("\n");

  return (
    <div
      className="flex h-full min-h-0 flex-col bg-background"
      data-testid="asset-render-view"
      aria-busy={loading}
    >
      <ScrollArea
        className="min-h-0 shrink border-b bg-muted/20"
        horizontalScrollBarClassName="hidden"
      >
        <div className="px-2 py-1.5">
          <div className="flex min-w-0 flex-wrap items-center gap-1.5 text-[11px]">
            <Badge variant="outline" size="xs">
              Preview — not executed
            </Badge>
            <span className="max-w-48 truncate font-mono font-medium" title={result.asset.name}>
              {result.asset.name}
            </span>
            <span className="text-muted-foreground">·</span>
            <span className="truncate font-medium" title={result.provenance.source.merkle_root}>
              {sourceLabel}
            </span>
            <span className="text-muted-foreground">·</span>
            <span title={configurationTitle}>{context.environment || "default"}</span>
            <span className="text-muted-foreground">·</span>
            <span>{formatRenderWindow(context.start_date, context.end_date)}</span>
            <span className="text-muted-foreground">·</span>
            <span>{context.full_refresh ? "full refresh" : "incremental"}</span>
            {result.asset.dialect ? (
              <>
                <span className="text-muted-foreground">·</span>
                <span>{result.asset.dialect}</span>
              </>
            ) : null}
            {result.asset.connection_name ? (
              <>
                <span className="text-muted-foreground">·</span>
                <span className="truncate font-mono">{result.asset.connection_name}</span>
              </>
            ) : null}
            {result.asset.fingerprint ? (
              <Badge variant="muted" size="xs" title={fingerprintTitle}>
                DAG {result.asset.fingerprint.slice(0, 8)}
              </Badge>
            ) : null}
            {target.kind !== "none" || target.fidelity !== "exact" ? (
              <Badge
                variant={target.fidelity === "exact" ? "secondary" : "muted"}
                size="xs"
                title={targetTitle}
              >
                Target {target.identity ? target.identity.slice(0, 8) : "runtime-only"}
              </Badge>
            ) : null}
            {variableProvenance.length > 0 ? (
              <Badge variant="muted" size="xs" title={variableTitle}>
                {variableProvenance.length} pipeline{" "}
                {variableProvenance.length === 1 ? "variable" : "variables"}
              </Badge>
            ) : null}
            {context.configuration_fidelity === "runtime_only" ? (
              <Badge variant="muted" size="xs" title={configurationTitle}>
                Config runtime-only
              </Badge>
            ) : null}
            {result.redactions.length > 0 ? (
              <Badge
                variant="muted"
                size="xs"
                title="Known credential values were masked or omitted"
              >
                <ShieldCheck className="size-3" data-icon="inline-start" /> Credentials redacted
              </Badge>
            ) : null}
            {pipelineId && result.provenance.source.kind === "working_tree" ? (
              <Button
                variant={comparisonOpen ? "secondary" : "outline"}
                size="xs"
                className="ml-auto shrink-0"
                disabled={!canCompare}
                onClick={() => {
                  if (comparisonOpen) {
                    comparisonRequestId.current += 1;
                    setComparisonOpen(false);
                    setComparisonLoading(false);
                    return;
                  }
                  setComparisonOpen(true);
                  void loadComparison(selectedSnapshot);
                }}
              >
                <GitCompareArrows data-icon="inline-start" />
                {comparisonOpen ? "Close comparison" : "Compare deployment"}
              </Button>
            ) : null}
            {loading ? <Spinner className="ml-auto size-3.5" /> : null}
          </div>
          {error ? (
            <div
              className="mt-1 flex items-center gap-1.5 text-[11px] text-destructive"
              role="alert"
            >
              <AlertTriangle className="size-3" />
              <span className="min-w-0 truncate">Refresh failed: {error}</span>
            </div>
          ) : null}
          {result.issues.length > 0 ? (
            <div
              className={cn(
                "mt-1 truncate text-[11px]",
                result.issues.some((issue) => issue.severity === "error")
                  ? "text-destructive"
                  : "text-amber-700 dark:text-amber-300",
              )}
              title={result.issues.map((issue) => issue.message).join("\n")}
            >
              {result.issues.map((issue) => issue.message).join(" · ")}
            </div>
          ) : null}
          {comparisonOpen ? (
            <AssetRenderComparisonToolbar
              comparison={comparison}
              loading={comparisonLoading}
              snapshots={snapshots}
              selectedSnapshot={selectedSnapshot}
              onSnapshotChange={(version) => {
                setSelectedSnapshot(version);
                void loadComparison(version);
              }}
              selectedStage={selectedComparisonStage}
              onStageChange={setSelectedComparisonStage}
            />
          ) : (
            <div className="mt-1.5 flex min-w-0 items-center gap-1.5">
              <div className="min-w-0 flex-1 overflow-x-auto pb-px">
                <ToggleGroup
                  type="single"
                  value={stageKeys.includes(selectedStage) ? selectedStage : stageKeys[0]}
                  onValueChange={(value) => value && setSelectedStage(value)}
                  variant="outline"
                  size="sm"
                  spacing={0}
                  aria-label="Rendered operation"
                >
                  {result.stages.map((item, index) => (
                    <ToggleGroupItem
                      key={stageKeys[index]}
                      value={stageKeys[index]}
                      title={item.message || assetRenderStageLabel(item)}
                    >
                      {assetRenderStageLabel(item)}
                    </ToggleGroupItem>
                  ))}
                </ToggleGroup>
              </div>
              {stage ? <StageStatusBadge status={stage.status} fidelity={stage.fidelity} /> : null}
              <Button
                variant="outline"
                size="xs"
                className="shrink-0"
                onClick={() => void copyStage()}
                disabled={!stage?.content}
                aria-label="Copy rendered operation"
              >
                {copied ? <Check data-icon="inline-start" /> : <Copy data-icon="inline-start" />}
                {copied ? "Copied" : "Copy"}
              </Button>
            </div>
          )}
          {!comparisonOpen && stage?.message ? (
            <p className="mt-1 truncate text-[11px] text-muted-foreground" title={stage.message}>
              {stage.message}
            </p>
          ) : null}
        </div>
      </ScrollArea>

      <div className="min-h-20 flex-1">
        {comparisonOpen ? (
          <AssetRenderComparisonView
            comparison={comparison}
            loading={comparisonLoading}
            error={comparisonError}
            selectedStage={selectedComparisonStage}
            onRetry={() => void loadComparison(selectedSnapshot)}
          />
        ) : stage?.content ? (
          <ReadOnlyRenderedOperation
            content={stage.content}
            language={stage.language || "sql"}
            modelKey={`${result.asset.id ?? result.asset.name}:${result.provenance.source.merkle_root}:${selectedStage}`}
          />
        ) : (
          <RenderCentered
            message={stage?.message || "This operation cannot be rendered statically yet."}
          />
        )}
      </div>
    </div>
  );
}

function AssetRenderComparisonToolbar({
  comparison,
  loading,
  snapshots,
  selectedSnapshot,
  onSnapshotChange,
  selectedStage,
  onStageChange,
}: {
  comparison: PipelineAssetRenderComparison | null;
  loading: boolean;
  snapshots: SnapshotSummary[];
  selectedSnapshot: string;
  onSnapshotChange: (version: string) => void;
  selectedStage: string;
  onStageChange: (stage: string) => void;
}) {
  return (
    <div className="mt-1.5 flex min-w-0 flex-wrap items-center gap-1.5">
      <span className="text-[11px] text-muted-foreground">Against</span>
      <Select value={selectedSnapshot} onValueChange={onSnapshotChange} disabled={loading}>
        <SelectTrigger size="sm" className="max-w-52" aria-label="Deployment to compare">
          <SelectValue placeholder="Latest deployment" />
        </SelectTrigger>
        <SelectContent>
          <SelectGroup>
            <SelectItem value="latest">Latest deployment</SelectItem>
            {snapshots.map((snapshot) => (
              <SelectItem key={snapshot.version_id} value={snapshot.version_id}>
                Deployment #{snapshot.ordinal} · {snapshot.merkle_root.slice(0, 8)}
              </SelectItem>
            ))}
          </SelectGroup>
        </SelectContent>
      </Select>
      {comparison?.stages.length ? (
        <Select value={selectedStage} onValueChange={onStageChange} disabled={loading}>
          <SelectTrigger size="sm" className="min-w-36 max-w-64" aria-label="Operation to compare">
            <SelectValue placeholder="Rendered operation" />
          </SelectTrigger>
          <SelectContent>
            <SelectGroup>
              {comparison.stages.map((item) => (
                <SelectItem key={item.key} value={item.key}>
                  {comparisonStageLabel(item)} · {item.status}
                </SelectItem>
              ))}
            </SelectGroup>
          </SelectContent>
        </Select>
      ) : null}
      {comparison ? (
        <span className="min-w-0 truncate text-[11px] text-muted-foreground">
          {formatComparisonSummary(comparison)}
        </span>
      ) : null}
      {loading ? <Spinner className="size-3.5" /> : null}
    </div>
  );
}

function AssetRenderComparisonView({
  comparison,
  loading,
  error,
  selectedStage,
  onRetry,
}: {
  comparison: PipelineAssetRenderComparison | null;
  loading: boolean;
  error: string | null;
  selectedStage: string;
  onRetry: () => void;
}) {
  if (loading && !comparison) {
    return <RenderCentered loading message="Comparing rendered operations…" />;
  }
  if (error && !comparison) {
    return (
      <div className="flex h-full min-h-0 items-center justify-center overflow-auto p-3">
        <Alert variant="destructive" className="max-w-xl">
          <AlertTriangle />
          <AlertTitle>Comparison failed</AlertTitle>
          <AlertDescription className="flex items-center justify-between gap-3">
            <span>{error}</span>
            <Button variant="outline" size="xs" onClick={onRetry}>
              Retry
            </Button>
          </AlertDescription>
        </Alert>
      </div>
    );
  }
  if (!comparison) {
    return <RenderCentered message="Choose a deployment to compare rendered operations." />;
  }

  const stage =
    comparison.stages.find((item) => item.key === selectedStage) ?? comparison.stages[0];
  if (!stage) {
    return (
      <RenderCentered message="Neither source produced a rendered operation for this asset." />
    );
  }
  const deployment = stage.deployment;
  const workingTree = stage.working_tree;
  const language = workingTree?.language || deployment?.language || "sql";
  const sourceLabel = comparison.snapshot.deployment_ordinal
    ? `Deployment #${comparison.snapshot.deployment_ordinal}`
    : `Deployment ${comparison.snapshot.merkle_root.slice(0, 8)}`;
  const hasContent = Boolean(deployment?.content || workingTree?.content);

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="asset-render-comparison">
      <div className="grid shrink-0 grid-cols-2 border-b bg-muted/20 text-[11px]">
        <div className="min-w-0 truncate border-r px-2 py-1 font-medium" title={sourceLabel}>
          {sourceLabel}
        </div>
        <div className="flex min-w-0 items-center gap-2 px-2 py-1 font-medium">
          <span className="min-w-0 flex-1 truncate">Saved workspace</span>
          <ComparisonStatusBadge status={stage.status} />
        </div>
      </div>
      <div className="min-h-0 flex-1">
        {hasContent ? (
          <ReadOnlyRenderedOperationDiff
            original={deployment?.content ?? ""}
            modified={workingTree?.content ?? ""}
            language={language}
            modelKey={`${comparison.asset_name}:${comparison.snapshot.version_id ?? comparison.snapshot.merkle_root}:${stage.key}`}
          />
        ) : (
          <RenderCentered
            message={
              workingTree?.message ||
              deployment?.message ||
              "This operation is semantic or runtime-dependent and has no textual SQL diff."
            }
          />
        )}
      </div>
    </div>
  );
}

function ComparisonStatusBadge({ status }: { status: string }) {
  return (
    <Badge
      variant={
        status === "removed" ? "destructive" : status === "unchanged" ? "muted" : "secondary"
      }
      size="xs"
      className={cn(
        "shrink-0",
        status === "added" && "text-emerald-700 dark:text-emerald-300",
        status === "changed" && "text-amber-700 dark:text-amber-300",
      )}
    >
      {status}
    </Badge>
  );
}

function comparisonStageLabel(comparison: AssetRenderStageComparison) {
  const stage = comparison.working_tree ?? comparison.deployment;
  return stage ? assetRenderStageLabel(stage) : "Rendered operation";
}

function formatComparisonSummary(comparison: PipelineAssetRenderComparison) {
  const { added, removed, changed, unchanged } = comparison.summary;
  if (comparison.status === "unchanged") return `${unchanged} unchanged`;
  return [
    added ? `${added} added` : "",
    removed ? `${removed} removed` : "",
    changed ? `${changed} changed` : "",
    unchanged ? `${unchanged} unchanged` : "",
  ]
    .filter(Boolean)
    .join(" · ");
}

function variableSourceLabel(source: string) {
  switch (source) {
    case "pipeline_default":
      return "pipeline default";
    case "schedule_override":
      return "schedule override";
    case "run_override":
      return "run override";
    default:
      return source.replaceAll("_", " ");
  }
}

export function ReadOnlyRenderedOperation({
  content,
  language,
  modelKey,
}: {
  content: string;
  language: string;
  modelKey: string;
}) {
  const { monacoTheme } = useWorkspaceTheme();
  const beforeMount = useCallback((monaco: Monaco) => defineBruinMonacoThemes(monaco), []);
  const extension = language === "sql" ? "sql" : language === "json" ? "json" : "txt";

  return (
    <Suspense fallback={<RenderCentered loading message="Loading preview…" />}>
      <MonacoEditor
        language={language}
        path={`inmemory://renart/render/${encodeURIComponent(modelKey)}.${extension}`}
        value={content}
        theme={monacoTheme}
        beforeMount={beforeMount}
        options={{
          readOnly: true,
          domReadOnly: true,
          automaticLayout: true,
          minimap: { enabled: false },
          fontSize: 12,
          folding: true,
          lineNumbersMinChars: 3,
          renderLineHighlight: "none",
          scrollBeyondLastLine: false,
          wordWrap: "on",
        }}
      />
    </Suspense>
  );
}

export function ReadOnlyRenderedOperationDiff({
  original,
  modified,
  language,
  modelKey,
  useInlineViewWhenSpaceIsLimited = false,
  inline = false,
  annotations,
}: {
  original: string;
  modified: string;
  language: string;
  modelKey: string;
  useInlineViewWhenSpaceIsLimited?: boolean;
  inline?: boolean;
  annotations?: { original: DiffAnnotation[]; modified: DiffAnnotation[] };
}) {
  const { monacoTheme } = useWorkspaceTheme();
  const beforeMount = useCallback((monaco: Monaco) => defineBruinMonacoThemes(monaco), []);
  const extension = language === "sql" ? "sql" : language === "json" ? "json" : "txt";
  const encodedKey = encodeURIComponent(modelKey);
  const [mounted, setMounted] = useState<{
    editor: editor.IStandaloneDiffEditor;
    monaco: Monaco;
  } | null>(null);
  const onMount = useCallback(
    (editor: editor.IStandaloneDiffEditor, monaco: Monaco) => setMounted({ editor, monaco }),
    [],
  );

  useEffect(() => {
    if (!mounted || !annotations) return;
    const collections: editor.IEditorDecorationsCollection[] = [];
    for (const side of ["original", "modified"] as const) {
      const codeEditor =
        side === "original"
          ? mounted.editor.getOriginalEditor()
          : mounted.editor.getModifiedEditor();
      const labelledLines = new Set<number>();
      collections.push(
        codeEditor.createDecorationsCollection(
          annotations[side].flatMap((annotation): editor.IModelDeltaDecoration[] => {
            const { line, column, end_line, end_column } = annotation.range;
            const showLabel =
              !labelledLines.has(end_line) &&
              (side === "modified" ||
                !annotations.modified.some((other) => other.label === annotation.label));
            labelledLines.add(end_line);
            const highlight: editor.IModelDeltaDecoration = {
              range: new mounted.monaco.Range(line, column, end_line, end_column),
              options: {
                inlineClassName: `deployment-diff-${annotation.severity}`,
                hoverMessage: {
                  value: annotation.label.replace(/[\\`*_{}[\]()#+.!|>~-]/g, "\\$&"),
                  isTrusted: false,
                },
                stickiness:
                  mounted.monaco.editor.TrackedRangeStickiness.NeverGrowsWhenTypingAtEdges,
              },
            };
            if (!showLabel) return [highlight];
            // Attach the explanation after SQL, not between an expression and
            // FROM/WHERE. Keep only one shared label in side-by-side reviews.
            const endOfLine = codeEditor.getModel()!.getLineMaxColumn(end_line);
            return [
              highlight,
              {
                range: new mounted.monaco.Range(end_line, endOfLine, end_line, endOfLine),
                options: {
                  showIfCollapsed: true,
                  after: {
                    content: `  ⚠ ${annotation.label.length > 90 ? `${annotation.label.slice(0, 87)}…` : annotation.label}`,
                    inlineClassName: "deployment-diff-lens",
                    inlineClassNameAffectsLetterSpacing: true,
                    cursorStops: mounted.monaco.editor.InjectedTextCursorStops.None,
                  },
                },
              },
            ];
          }),
        ),
      );
    }
    const first = annotations.modified[0];
    if (first) mounted.editor.getModifiedEditor().revealLineInCenter(first.range.line);
    return () => {
      for (const collection of collections) collection.clear();
    };
  }, [mounted, annotations]);

  return (
    <Suspense fallback={<RenderCentered loading message="Loading comparison…" />}>
      <MonacoDiffEditor
        original={original}
        modified={modified}
        language={language}
        originalModelPath={`inmemory://renart/render-diff/deployment/${encodedKey}.${extension}`}
        modifiedModelPath={`inmemory://renart/render-diff/working-tree/${encodedKey}.${extension}`}
        theme={monacoTheme}
        beforeMount={beforeMount}
        onMount={onMount}
        options={{
          automaticLayout: true,
          diffCodeLens: false,
          diffWordWrap: "on",
          domReadOnly: true,
          enableSplitViewResizing: true,
          folding: true,
          fontSize: 12,
          glyphMargin: false,
          ignoreTrimWhitespace: false,
          lineNumbersMinChars: 3,
          minimap: { enabled: false },
          originalEditable: false,
          readOnly: true,
          renderOverviewRuler: false,
          renderSideBySide: !inline,
          scrollBeyondLastLine: false,
          useInlineViewWhenSpaceIsLimited,
          wordWrap: "on",
        }}
      />
    </Suspense>
  );
}

function StageStatusBadge({
  status,
  fidelity,
}: {
  status: AssetRenderStageStatus;
  fidelity: AssetRenderFidelity;
}) {
  const label =
    status === "error"
      ? "render error"
      : status === "unsupported" || fidelity === "unsupported"
        ? "not renderable"
        : fidelity === "runtime_only"
          ? "runtime-dependent"
          : fidelity;
  return (
    <Badge
      variant={
        status === "error"
          ? "destructive"
          : status === "unsupported" || fidelity === "unsupported"
            ? "outline"
            : fidelity === "exact"
              ? "secondary"
              : "muted"
      }
      size="xs"
      className="shrink-0"
    >
      {label}
    </Badge>
  );
}

function RenderCentered({ message, loading = false }: { message: string; loading?: boolean }) {
  return (
    <div className="flex h-full min-h-0 items-center justify-center gap-2 px-4 text-center text-xs text-muted-foreground">
      {loading ? (
        <Spinner className="size-4" />
      ) : (
        <FileCode2 className="size-4" data-icon="inline-start" />
      )}
      <span>{message}</span>
    </div>
  );
}

export function assetRenderStageLabel(stage: AssetRenderStage) {
  if (stage.label) return stage.label;
  switch (stage.kind) {
    case "compiled_query":
      return "Compiled query";
    case "execution_sql":
      return "Execution SQL";
    case "schema_preparation":
      return "Schema preparation";
    default:
      return stage.kind.replaceAll("_", " ");
  }
}

function formatRenderWindow(start: string, end: string) {
  const format = (value: string) => {
    const date = new Date(value);
    return Number.isNaN(date.getTime()) ? value : date.toISOString().slice(0, 16).replace("T", " ");
  };
  return `${format(start)}–${format(end)} UTC`;
}
