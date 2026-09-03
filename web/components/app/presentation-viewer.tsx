"use client";

import { Link, useLocation, useNavigate } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import { AlertTriangle, ArrowLeft, Check, Eye, Loader2, Pencil, RefreshCw } from "lucide-react";
import type { CSSProperties, ReactNode } from "react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import ReactMarkdown from "react-markdown";

import { AuthoredControlValueField } from "@/components/app/authored-control";
import { NotebookVisualizationRenderer } from "@/components/app/notebook-viz";
import { normalizeVisualizationDefinition } from "@/components/app/notebook-visualization-block";
import { AppPage, PageHeader } from "@/components/app/app-primitives";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Skeleton } from "@/components/ui/skeleton";
import type { NotebookCellRunResult } from "@/lib/api-notebooks";
import {
  getPresentation,
  type PresentationArtifact,
  type PresentationDatasetResult,
  type PresentationDocument,
  type PresentationFilter,
  type PresentationVisualization,
  runPresentation,
} from "@/lib/api-presentations";
import { workspaceAtom } from "@/lib/atoms/domains/workspace";
import { authoredControlOptions } from "@/lib/authored-controls";
import { markdownContentClassName } from "@/lib/markdown-content";
import { cn } from "@/lib/utils";

import { PresentationLibrarySidebar } from "./presentation-library-sidebar";
import type { PresentationKind } from "./presentation-page";
import { WorkbenchPortal, useWorkbench } from "./workbench/workbench-slots";

type FilterValues = Record<string, unknown>;

export function AppPresentationViewerPage({
  kind,
  presentationId,
  filterSearch,
}: {
  kind: PresentationKind;
  presentationId: string;
  filterSearch?: FilterValues;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const selectedEnvironment = workspace?.selected_environment || "default";
  const navigate = useNavigate();
  const location = useLocation();
  const { navigation } = useWorkbench();
  const workbenchEnabled = Boolean(navigation?.workbench);
  const [document, setDocument] = useState<PresentationDocument | null>(null);
  const [filterValues, setFilterValues] = useState<FilterValues>({});
  const [results, setResults] = useState<Record<string, PresentationDatasetResult>>({});
  const [optionResults, setOptionResults] = useState<Record<string, PresentationDatasetResult>>({});
  const [loadingIDs, setLoadingIDs] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const requestSequence = useRef(0);
  const latestRequestByVisualization = useRef<Record<string, number>>({});
  const pendingRefresh = useRef<Set<string>>(new Set());
  const refreshTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const observedWorkspaceRevision = useRef("");
  const filterSearchRef = useRef(filterSearch);
  const filterSearchKey = comparableValue(filterSearch ?? {});
  const filterSearchKeyRef = useRef(filterSearchKey);
  const pendingFilterSearchKeyRef = useRef("");

  const execute = useCallback(
    async (
      artifact: PresentationArtifact,
      values: FilterValues,
      visualizationIDs: string[],
      includeOptions = false,
    ) => {
      const sequence = ++requestSequence.current;
      for (const id of visualizationIDs) latestRequestByVisualization.current[id] = sequence;
      setLoadingIDs((current) => new Set([...current, ...visualizationIDs]));
      setError("");
      try {
        const response = await runPresentation(presentationId, {
          environment: selectedEnvironment,
          filter_values: values,
          visualization_ids: visualizationIDs,
          include_options: includeOptions,
        });
        setResults((current) => {
          const next = { ...current };
          for (const [id, result] of Object.entries(response.visualizations ?? {})) {
            if (latestRequestByVisualization.current[id] === sequence) next[id] = result;
          }
          return next;
        });
        if (includeOptions) setOptionResults(response.options ?? {});
        setFilterValues((current) =>
          Object.keys(current).length === 0 ? (response.filter_values ?? current) : current,
        );
        if (response.artifact_revision !== artifact.revision) {
          setError(
            "This presentation changed while its data was loading. Refresh to use the latest definition.",
          );
        }
      } catch (nextError) {
        setError(errorMessage(nextError));
      } finally {
        setLoadingIDs((current) => {
          const next = new Set(current);
          for (const id of visualizationIDs) {
            if (latestRequestByVisualization.current[id] === sequence) next.delete(id);
          }
          return next;
        });
      }
    },
    [presentationId, selectedEnvironment],
  );

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const nextDocument = await getPresentation(presentationId);
      const nextValues = initialFilterValues(
        nextDocument.artifact.filters ?? [],
        filterSearchRef.current,
      );
      setDocument(nextDocument);
      setFilterValues(nextValues);
      setResults({});
      setOptionResults({});
      await execute(
        nextDocument.artifact,
        nextValues,
        (nextDocument.artifact.visualizations ?? []).map((item) => item.id),
        true,
      );
    } catch (nextError) {
      setError(errorMessage(nextError));
    } finally {
      setLoading(false);
    }
  }, [execute, presentationId]);

  useEffect(() => {
    void load();
  }, [load]);

  const workspaceArtifact = workspace?.presentations?.find(
    (artifact) => artifact.workspace_id === presentationId,
  );
  useEffect(() => {
    const revision = workspaceArtifact?.revision ?? "";
    if (!revision || observedWorkspaceRevision.current === revision) return;
    observedWorkspaceRevision.current = revision;
    if (document && revision !== document.artifact.revision) void load();
  }, [document, load, workspaceArtifact?.revision]);

  useEffect(
    () => () => {
      if (refreshTimer.current) clearTimeout(refreshTimer.current);
    },
    [],
  );

  useEffect(() => {
    if (!document || filterSearchKeyRef.current === filterSearchKey) return;
    filterSearchRef.current = filterSearch;
    filterSearchKeyRef.current = filterSearchKey;
    if (pendingFilterSearchKeyRef.current === filterSearchKey) {
      pendingFilterSearchKeyRef.current = "";
      return;
    }
    const next = initialFilterValues(document.artifact.filters ?? [], filterSearch);
    const changed = (document.artifact.filters ?? [])
      .filter(
        (filter) => comparableValue(next[filter.id]) !== comparableValue(filterValues[filter.id]),
      )
      .map((filter) => filter.id);
    if (changed.length === 0) return;
    setFilterValues(next);
    const affected = (document.artifact.visualizations ?? [])
      .filter((visualization) =>
        (visualization.filter_bindings ?? []).some((binding) => changed.includes(binding.filter)),
      )
      .map((visualization) => visualization.id);
    if (affected.length > 0) void execute(document.artifact, next, affected);
  }, [document, execute, filterSearch, filterSearchKey, filterValues]);

  const scheduleAffectedRefresh = (
    artifact: PresentationArtifact,
    filterID: string,
    values: FilterValues,
  ) => {
    for (const visualization of artifact.visualizations ?? []) {
      if ((visualization.filter_bindings ?? []).some((binding) => binding.filter === filterID)) {
        pendingRefresh.current.add(visualization.id);
      }
    }
    if (refreshTimer.current) clearTimeout(refreshTimer.current);
    refreshTimer.current = setTimeout(() => {
      const ids = [...pendingRefresh.current];
      pendingRefresh.current.clear();
      if (ids.length > 0) void execute(artifact, values, ids);
    }, 250);
  };

  const changeFilter = (filterID: string, value: unknown) => {
    if (!document) return;
    const next = { ...filterValues, [filterID]: value };
    setFilterValues(next);
    const filters = changedFilterValues(document.artifact.filters ?? [], next);
    pendingFilterSearchKeyRef.current = comparableValue(filters ?? {});
    void navigate({
      to: location.pathname as never,
      replace: true,
      search: { filters } as never,
    });
    scheduleAffectedRefresh(document.artifact, filterID, next);
  };

  const meta = kind === "dashboard" ? "Dashboards" : "Reports";
  const kindMismatch = document && document.artifact.kind !== kind;
  const allVisualizationIDs = (document?.artifact.visualizations ?? []).map((item) => item.id);
  const viewerActions = (
    <>
      <Button asChild variant="ghost" size="sm">
        <Link to={kind === "dashboard" ? "/dashboards" : "/reports"}>
          <ArrowLeft />
          {meta}
        </Link>
      </Button>
      {document ? (
        <Button asChild variant="outline" size="sm">
          <Link
            to={kind === "dashboard" ? "/dashboards/$presentationId" : "/reports/$presentationId"}
            params={{ presentationId }}
          >
            <Pencil /> Edit
          </Link>
        </Button>
      ) : null}
      <Button
        size="sm"
        disabled={!document || loadingIDs.size > 0}
        onClick={() =>
          document && void execute(document.artifact, filterValues, allVisualizationIDs, true)
        }
      >
        {loadingIDs.size > 0 ? <Loader2 className="animate-spin" /> : <RefreshCw />}
        Refresh
      </Button>
    </>
  );

  return (
    <AppPage>
      {workbenchEnabled ? (
        <WorkbenchPortal slot="context">
          <PresentationLibrarySidebar kind={kind} activePresentationId={presentationId} />
        </WorkbenchPortal>
      ) : (
        <PageHeader
          title={document?.artifact.title ?? `Loading ${kind}…`}
          subtitle={
            document
              ? `${document.artifact.path} · ${selectedEnvironment}`
              : "Rendered from the Git-tracked definition"
          }
          actions={viewerActions}
        />
      )}

      {loading && !document ? (
        <PresentationViewerSkeleton />
      ) : kindMismatch ? (
        <div className="m-auto w-full max-w-xl p-4">
          <Alert variant="destructive">
            <AlertTriangle />
            <AlertTitle>Presentation kind does not match this route</AlertTitle>
            <AlertDescription>This file is a {document?.artifact.kind}.</AlertDescription>
          </Alert>
        </div>
      ) : document ? (
        <ScrollArea className="min-h-0 flex-1">
          <div className="mx-auto w-full max-w-7xl space-y-4 p-4 sm:p-6">
            <div className="flex flex-wrap items-start gap-2">
              <div className="min-w-0 flex-1">
                {workbenchEnabled ? (
                  <h1 className="truncate text-lg font-semibold tracking-tight">
                    {document.artifact.title}
                  </h1>
                ) : null}
                <div
                  className={cn("flex flex-wrap items-center gap-2", workbenchEnabled && "mt-1")}
                >
                  <Badge variant="secondary" className="gap-1 font-normal">
                    <Eye className="size-3" /> Live view
                  </Badge>
                  {document.artifact.problems?.length ? (
                    <Badge variant="outline" className="border-amber-500/30 text-amber-700">
                      <AlertTriangle /> {document.artifact.problems.length} definition problem
                      {document.artifact.problems.length === 1 ? "" : "s"}
                    </Badge>
                  ) : (
                    <Badge variant="outline" className="gap-1 font-normal text-emerald-700">
                      <Check className="size-3" /> Definition checked
                    </Badge>
                  )}
                </div>
              </div>
              {workbenchEnabled ? (
                <div className="flex shrink-0 items-center gap-1">{viewerActions}</div>
              ) : null}
            </div>

            {(document.artifact.filters ?? []).length > 0 ? (
              <div className="flex flex-wrap items-end gap-3 rounded-xl border bg-card p-3">
                {(document.artifact.filters ?? []).map((filter) => (
                  <PresentationFilterControl
                    key={filter.id}
                    filter={filter}
                    value={filterValues[filter.id]}
                    optionResult={optionResults[filter.id]}
                    onChange={(value) => changeFilter(filter.id, value)}
                  />
                ))}
              </div>
            ) : null}

            {error ? (
              <Alert variant="destructive">
                <AlertTriangle />
                <AlertTitle>Could not render this {kind}</AlertTitle>
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            ) : null}

            {kind === "dashboard" ? (
              <DashboardViewer
                artifact={document.artifact}
                results={results}
                loadingIDs={loadingIDs}
              />
            ) : (
              <ReportViewer
                artifact={document.artifact}
                results={results}
                loadingIDs={loadingIDs}
              />
            )}
          </div>
        </ScrollArea>
      ) : (
        <div className="m-auto w-full max-w-xl p-4">
          <Alert variant="destructive">
            <AlertTriangle />
            <AlertTitle>Could not load {kind}</AlertTitle>
            <AlertDescription>{error || "The presentation is unavailable."}</AlertDescription>
          </Alert>
          <Button className="mt-3" size="sm" variant="outline" onClick={() => void load()}>
            Try again
          </Button>
        </div>
      )}
    </AppPage>
  );
}

function DashboardViewer({
  artifact,
  results,
  loadingIDs,
}: {
  artifact: PresentationArtifact;
  results: Record<string, PresentationDatasetResult>;
  loadingIDs: Set<string>;
}) {
  const visualizations = new Map(
    (artifact.visualizations ?? []).map((visualization) => [visualization.id, visualization]),
  );
  const layout = [...(artifact.layout ?? [])].sort(
    (left, right) =>
      (left.y ?? 0) - (right.y ?? 0) ||
      (left.x ?? 0) - (right.x ?? 0) ||
      left.visualization.localeCompare(right.visualization),
  );
  const placed = new Set(layout.map((item) => item.visualization));
  for (const visualization of artifact.visualizations ?? []) {
    if (!placed.has(visualization.id))
      layout.push({ visualization: visualization.id, width: 6, height: 4 });
  }
  if (layout.length === 0) return <EmptyPresentation />;
  return (
    <div className="grid grid-cols-12 gap-4">
      {layout.map((item) => {
        const visualization = visualizations.get(item.visualization);
        if (!visualization) return null;
        const span = Math.max(1, Math.min(12, item.width || 6));
        return (
          <div
            key={visualization.id}
            style={{ "--presentation-span": span } as CSSProperties}
            className="col-span-12 min-w-0 md:[grid-column:span_var(--presentation-span)]"
          >
            <PresentationVisualizationCard
              visualization={visualization}
              result={results[visualization.id]}
              loading={loadingIDs.has(visualization.id)}
              minHeight={Math.max(16, (item.height || 4) * 4)}
            />
          </div>
        );
      })}
    </div>
  );
}

function ReportViewer({
  artifact,
  results,
  loadingIDs,
}: {
  artifact: PresentationArtifact;
  results: Record<string, PresentationDatasetResult>;
  loadingIDs: Set<string>;
}) {
  const visualizations = new Map(
    (artifact.visualizations ?? []).map((visualization) => [visualization.id, visualization]),
  );
  if ((artifact.sections ?? []).length === 0) return <EmptyPresentation />;
  return (
    <article className="mx-auto max-w-4xl space-y-6 rounded-xl border bg-card px-5 py-6 shadow-xs sm:px-8">
      {(artifact.sections ?? []).map((section) => {
        const visualization = section.visualization
          ? visualizations.get(section.visualization)
          : undefined;
        return (
          <section
            key={section.id}
            className={cn("min-w-0", section.page_break && "print:break-before-page")}
          >
            {section.title ? <h2 className="mb-3 text-lg font-semibold">{section.title}</h2> : null}
            {section.markdown ? (
              <div
                className={cn(
                  "prose prose-sm max-w-none text-foreground dark:prose-invert",
                  markdownContentClassName,
                )}
              >
                <ReactMarkdown>{section.markdown}</ReactMarkdown>
              </div>
            ) : visualization ? (
              <PresentationVisualizationCard
                visualization={visualization}
                result={results[visualization.id]}
                loading={loadingIDs.has(visualization.id)}
              />
            ) : null}
          </section>
        );
      })}
    </article>
  );
}

export function PresentationVisualizationCard({
  visualization,
  result,
  loading,
  minHeight = 18,
  header,
}: {
  visualization: PresentationVisualization;
  result?: PresentationDatasetResult;
  loading: boolean;
  minHeight?: number;
  header?: ReactNode;
}) {
  const definition = normalizeVisualizationDefinition(visualization.definition);
  return (
    <div
      className="relative h-full min-w-0 overflow-hidden rounded-xl border bg-card p-3 shadow-xs"
      style={{ minHeight: `${minHeight}rem` }}
      data-testid={`presentation-visualization-${visualization.id}`}
    >
      <div className="mb-2 flex items-center justify-between gap-2">
        {header ?? (
          <span className="truncate text-sm font-medium">
            {definition.title || visualization.id}
          </span>
        )}
        {result?.truncated ? <Badge variant="outline">First 1,000 rows</Badge> : null}
      </div>
      {result?.status === "error" ? (
        <Alert variant="destructive">
          <AlertTriangle />
          <AlertTitle>Dataset failed</AlertTitle>
          <AlertDescription>{result.error}</AlertDescription>
        </Alert>
      ) : result ? (
        <NotebookVisualizationRenderer definition={definition} result={toNotebookResult(result)} />
      ) : (
        <div className="flex min-h-40 items-center justify-center text-xs text-muted-foreground">
          Waiting for data…
        </div>
      )}
      {loading ? (
        <div className="absolute inset-0 flex items-center justify-center bg-background/60 backdrop-blur-[1px]">
          <Loader2 className="size-5 animate-spin text-primary" />
        </div>
      ) : null}
    </div>
  );
}

export function PresentationFilterControl({
  filter,
  value,
  optionResult,
  onChange,
}: {
  filter: PresentationFilter;
  value: unknown;
  optionResult?: PresentationDatasetResult;
  onChange: (value: unknown) => void;
}) {
  const options = useMemo(
    () => authoredControlOptions(filter, optionResult),
    [filter, optionResult],
  );
  return (
    <AuthoredControlValueField
      control={filter}
      value={value}
      options={options}
      idScope="presentation-control-runtime"
      compact
      onChange={onChange}
    />
  );
}

export function initialFilterValues(
  filters: PresentationFilter[],
  overrides?: FilterValues,
): FilterValues {
  const defaults = Object.fromEntries(filters.map((filter) => [filter.id, filter.default]));
  return overrides ? { ...defaults, ...overrides } : defaults;
}

function changedFilterValues(
  filters: PresentationFilter[],
  values: FilterValues,
): FilterValues | undefined {
  const changed: FilterValues = {};
  for (const filter of filters) {
    if (comparableValue(values[filter.id]) !== comparableValue(filter.default)) {
      changed[filter.id] = values[filter.id];
    }
  }
  return Object.keys(changed).length > 0 ? changed : undefined;
}

export function normalizePresentationViewerSearch(search: Record<string, unknown>): {
  filters?: FilterValues;
} {
  let filters = search.filters;
  if (typeof filters === "string") {
    try {
      filters = JSON.parse(filters);
    } catch {
      filters = undefined;
    }
  }
  return {
    filters:
      filters && typeof filters === "object" && !Array.isArray(filters)
        ? (filters as FilterValues)
        : undefined,
  };
}

function comparableValue(value: unknown): string {
  return JSON.stringify(value) ?? String(value ?? "");
}

function toNotebookResult(result: PresentationDatasetResult): NotebookCellRunResult {
  return {
    cell_id: result.dataset,
    name: result.dataset,
    object_name: result.dataset,
    status: result.status === "ok" ? "ok" : "error",
    error: result.error,
    columns: result.columns,
    column_types: result.column_types,
    rows: result.rows,
    total_rows: result.total_rows,
    materialized: "view",
    duration_ms: result.duration_ms,
  };
}

function EmptyPresentation() {
  return (
    <div className="flex min-h-64 items-center justify-center rounded-xl border border-dashed bg-muted/20 text-sm text-muted-foreground">
      Add a visualization in the editor to render this presentation.
    </div>
  );
}

function PresentationViewerSkeleton() {
  return (
    <div className="min-h-0 flex-1 space-y-4 p-6">
      <Skeleton className="h-16 w-full rounded-xl" />
      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <Skeleton className="h-72 rounded-xl" />
        <Skeleton className="h-72 rounded-xl" />
      </div>
    </div>
  );
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
