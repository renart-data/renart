"use client";

import {
  Check,
  Loader2,
  Monitor,
  PanelLeft,
  Redo2,
  RefreshCw,
  Smartphone,
  SlidersHorizontal,
  Tablet,
  TriangleAlert,
  Undo2,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import {
  previewPresentation,
  type PresentationArtifact,
  type PresentationDatasetResult,
  type PresentationFinding,
  type PresentationLayoutItem,
  type PresentationPreviewResult,
  type PresentationSection,
  type PresentationVisualization,
} from "@/lib/api-presentations";
import type { WorkspaceState } from "@/lib/types";
import {
  AUTHORED_CONTROL_TYPE_LABELS,
  defaultAuthoredControlRange,
  defaultAuthoredControlValue,
  type AuthoredControlType,
} from "@/lib/authored-controls";

import type { ChartType } from "../chart-type-picker";
import { DocumentAuthoringCommandBar, DocumentAuthoringShell } from "../document-authoring-shell";
import { datasetColumns, nextID, workspaceAssetChoices } from "../presentation-visual-editor";
import { initialFilterValues } from "../presentation-viewer";
import { AddVisualizationDialog } from "./add-visualization-dialog";
import { AddFilterDialog } from "./add-filter-dialog";
import { DashboardCanvas } from "./dashboard-canvas";
import { DashboardFilterStrip } from "./dashboard-filter-strip";
import {
  generatedPresentationID,
  presentationFindingTarget,
  type PresentationBuilderSelection,
  type PresentationPreviewMode,
  type VisualizationSuggestion,
  visualizationSuggestionForType,
} from "./presentation-builder-model";
import { PresentationInspector } from "./presentation-inspector";
import { PresentationSidebar } from "./presentation-sidebar";
import { ReportCanvas } from "./report-canvas";
import { usePresentationDraft } from "./use-presentation-draft";

type FilterValues = Record<string, unknown>;

export function PresentationBuilder({
  presentationId,
  artifact: externalArtifact,
  workspace,
  paused = false,
  navigation,
  modeControl,
  documentActions,
  banner,
  onChange,
}: {
  presentationId: string;
  artifact: PresentationArtifact;
  workspace: WorkspaceState | null;
  paused?: boolean;
  navigation: ReactNode;
  modeControl: ReactNode;
  documentActions: ReactNode;
  banner?: ReactNode;
  onChange: (artifact: PresentationArtifact) => void;
}) {
  const { artifact, replace, commit, undo, redo, canUndo, canRedo } = usePresentationDraft(
    externalArtifact,
    onChange,
  );
  const [selection, setSelection] = useState<PresentationBuilderSelection>({ kind: "artifact" });
  const [previewMode, setPreviewMode] = useState<PresentationPreviewMode>("desktop");
  const [dataOpen, setDataOpen] = useState(false);
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const [addFilterOpen, setAddFilterOpen] = useState(false);
  const [preferredType, setPreferredType] = useState<string>();
  const [pendingReportIndex, setPendingReportIndex] = useState<number | null>(null);
  const [filterValues, setFilterValues] = useState<FilterValues>(() =>
    initialFilterValues(artifact.filters ?? []),
  );
  const [results, setResults] = useState<Record<string, PresentationDatasetResult>>({});
  const [optionResults, setOptionResults] = useState<Record<string, PresentationDatasetResult>>({});
  const [previewFindings, setPreviewFindings] = useState<PresentationFinding[]>(
    artifact.problems ?? [],
  );
  const [previewResolved, setPreviewResolved] = useState(false);
  const [loadingIDs, setLoadingIDs] = useState<Set<string>>(new Set());
  const [previewError, setPreviewError] = useState("");
  const [previewStale, setPreviewStale] = useState(true);
  const [pendingFindingPath, setPendingFindingPath] = useState<string | null>(null);
  const wideBuilder = useWideBuilder();
  const selectedEnvironment = workspace?.selected_environment || "default";
  const assetChoices = useMemo(() => workspaceAssetChoices(workspace), [workspace]);
  const artifactRef = useRef(artifact);
  const filterValuesRef = useRef(filterValues);
  const previewSequence = useRef(0);
  const previewAbort = useRef<AbortController | null>(null);
  const previewCache = useRef(new Map<string, PresentationPreviewResult>());

  useEffect(() => {
    artifactRef.current = artifact;
  }, [artifact]);
  useEffect(() => {
    filterValuesRef.current = filterValues;
  }, [filterValues]);

  useEffect(() => {
    setFilterValues((current) => {
      const defaults = initialFilterValues(artifact.filters ?? []);
      const next: FilterValues = {};
      for (const filter of artifact.filters ?? [])
        next[filter.id] = Object.hasOwn(current, filter.id)
          ? current[filter.id]
          : defaults[filter.id];
      return next;
    });
  }, [artifact.filters]);

  useEffect(() => {
    if (selectionExists(artifact, selection)) return;
    setSelection({ kind: "artifact" });
  }, [artifact, selection]);

  const runPreview = useCallback(
    async ({
      visualizationIDs,
      includeOptions = false,
      values = filterValuesRef.current,
      replaceResults = false,
    }: {
      visualizationIDs?: string[];
      includeOptions?: boolean;
      values?: FilterValues;
      replaceResults?: boolean;
    } = {}) => {
      if (paused) return;
      const current = artifactRef.current;
      const ids =
        visualizationIDs ?? (current.visualizations ?? []).map((visualization) => visualization.id);
      if (ids.length === 0 && !includeOptions) {
        setPreviewFindings(current.problems ?? []);
        setPreviewResolved(true);
        setPreviewStale(false);
        return;
      }
      previewAbort.current?.abort();
      const sequence = ++previewSequence.current;
      const cacheKey = JSON.stringify({
        revision: current.revision,
        environment: selectedEnvironment,
        draft: previewDataSignature(current),
        values,
        visualization_ids: [...ids].sort(),
        include_options: includeOptions,
      });
      const applyResponse = (response: PresentationPreviewResult) => {
        if (sequence !== previewSequence.current) return;
        setPreviewFindings(response.findings ?? []);
        if (response.status === "invalid") {
          setPreviewResolved(true);
          setPreviewError("Fix the highlighted definition problems before running this draft.");
          setPreviewStale(true);
          return;
        }
        setResults((previous) =>
          replaceResults
            ? (response.visualizations ?? {})
            : { ...previous, ...(response.visualizations ?? {}) },
        );
        if (includeOptions) setOptionResults(response.options ?? {});
        setPreviewResolved(true);
        setPreviewError("");
        setPreviewStale(false);
      };
      const cached = previewCache.current.get(cacheKey);
      if (cached) {
        setLoadingIDs(new Set());
        applyResponse(cached);
        return;
      }
      const controller = new AbortController();
      previewAbort.current = controller;
      setLoadingIDs(new Set(ids));
      setPreviewError("");
      try {
        const response = await previewPresentation(
          presentationId,
          {
            expected_revision: current.revision,
            artifact: current,
            environment: selectedEnvironment,
            filter_values: values,
            visualization_ids: ids,
            include_options: includeOptions,
          },
          { signal: controller.signal },
        );
        previewCache.current.set(cacheKey, response);
        while (previewCache.current.size > 40) {
          const oldest = previewCache.current.keys().next().value;
          if (oldest === undefined) break;
          previewCache.current.delete(oldest);
        }
        applyResponse(response);
      } catch (error) {
        if (controller.signal.aborted) return;
        setPreviewError(errorMessage(error));
        setPreviewStale(true);
      } finally {
        if (sequence === previewSequence.current) {
          setLoadingIDs(new Set());
        }
      }
    },
    [paused, presentationId, selectedEnvironment],
  );

  const dataSignature = previewDataSignature(artifact);
  useEffect(() => {
    previewAbort.current?.abort();
    previewSequence.current += 1;
    setLoadingIDs(new Set());
    setPreviewError("");
    setPreviewStale(false);
  }, [artifact.revision]);
  useEffect(() => {
    if (!paused) return;
    previewAbort.current?.abort();
    previewSequence.current += 1;
    setLoadingIDs(new Set());
  }, [paused]);
  useEffect(() => {
    if (paused) return;
    setPreviewStale(true);
    setPreviewResolved(false);
    const timer = window.setTimeout(
      () => void runPreview({ includeOptions: true, replaceResults: true }),
      550,
    );
    return () => window.clearTimeout(timer);
  }, [dataSignature, paused, runPreview]);
  useEffect(() => () => previewAbort.current?.abort(), []);

  useEffect(() => {
    if (!wideBuilder) return;
    setDataOpen(false);
    setInspectorOpen(false);
  }, [wideBuilder]);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (!(event.metaKey || event.ctrlKey)) return;
      const target = event.target as HTMLElement | null;
      if (
        target?.matches("input, textarea, [contenteditable='true']") ||
        target?.closest("[role='dialog']")
      )
        return;
      if (event.key.toLowerCase() === "z" && !event.shiftKey) {
        event.preventDefault();
        undo();
      } else if (
        event.key.toLowerCase() === "y" ||
        (event.key.toLowerCase() === "z" && event.shiftKey)
      ) {
        event.preventDefault();
        redo();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [redo, undo]);

  const changeArtifact = useCallback(
    (next: PresentationArtifact, options?: { coalesceKey?: string }) => replace(next, options),
    [replace],
  );
  const showInspector = () => {
    if (wideBuilder) return;
    setDataOpen(false);
    setInspectorOpen(true);
  };

  const addDataset = () => {
    const datasets = artifact.datasets ?? [];
    const id = nextID(
      "dataset",
      datasets.map((dataset) => dataset.id),
    );
    const firstConnection = workspace?.query_connections?.[0]?.name;
    const dataset = assetChoices[0]
      ? { id, asset: assetChoices[0].value }
      : { id, connection: firstConnection ?? "", query: "select *\nfrom " };
    changeArtifact({ ...artifact, datasets: [...datasets, dataset] });
    setSelection({ kind: "dataset", id });
    showInspector();
  };

  const addBlankFilter = (type: AuthoredControlType = "text") => {
    const filters = artifact.filters ?? [];
    const id = nextID(
      "filter",
      filters.map((filter) => filter.id),
    );
    changeArtifact({
      ...artifact,
      filters: [
        ...filters,
        {
          id,
          label: AUTHORED_CONTROL_TYPE_LABELS[type],
          type,
          default: defaultAuthoredControlValue(type),
          ...defaultAuthoredControlRange(type),
          options: type === "select" || type === "multi_select" ? { values: [] } : undefined,
        },
      ],
    });
    setSelection({ kind: "filter", id });
    showInspector();
  };

  const openAddFilter = () => {
    if (!wideBuilder) {
      setDataOpen(false);
      setInspectorOpen(false);
    }
    setAddFilterOpen(true);
  };

  const openAddVisualization = (nextPreferredType?: string, reportIndex?: number) => {
    if (!wideBuilder) {
      setDataOpen(false);
      setInspectorOpen(false);
    }
    setPreferredType(nextPreferredType);
    setPendingReportIndex(reportIndex ?? null);
    setAddOpen(true);
  };

  const addVisualization = (
    datasetID: string,
    suggestion: VisualizationSuggestion,
    placement?: {
      dashboard?: { x: number; y: number; width: number; height: number };
      reportIndex?: number;
    },
  ) => {
    const visualizations = artifact.visualizations ?? [];
    const id = generatedPresentationID(
      suggestion.title,
      "visualization",
      visualizations.map((visualization) => visualization.id),
    );
    const visualization: PresentationVisualization = {
      id,
      dataset: datasetID,
      definition: suggestion.definition,
    };
    if (artifact.kind === "dashboard") {
      const layout = artifact.layout ?? [];
      const bottom = layout.reduce(
        (maximum, item) => Math.max(maximum, (item.y ?? 0) + (item.height || 4)),
        0,
      );
      const width =
        placement?.dashboard?.width ??
        (suggestion.type === "kpi" ? 3 : suggestion.type === "table" ? 12 : 6);
      const x = Math.min(Math.max(placement?.dashboard?.x ?? 0, 0), Math.max(0, 12 - width));
      changeArtifact({
        ...artifact,
        visualizations: [...visualizations, visualization],
        layout: [
          ...layout,
          {
            visualization: id,
            x,
            y: Math.max(0, placement?.dashboard?.y ?? bottom),
            width,
            height: placement?.dashboard?.height ?? (suggestion.type === "kpi" ? 3 : 4),
          },
        ],
      });
    } else {
      const sections = [...(artifact.sections ?? [])];
      const sectionID = generatedPresentationID(
        suggestion.title,
        "chart",
        sections.map((section) => section.id),
      );
      const index = placement?.reportIndex ?? pendingReportIndex ?? sections.length;
      sections.splice(index, 0, { id: sectionID, visualization: id });
      changeArtifact({ ...artifact, visualizations: [...visualizations, visualization], sections });
    }
    setPendingReportIndex(null);
    setSelection({ kind: "visualization", id });
    showInspector();
  };

  const addDroppedVisualization = (
    type: ChartType,
    placement?: {
      dashboard?: { x: number; y: number; width: number; height: number };
      reportIndex?: number;
    },
  ) => {
    const datasets = artifact.datasets ?? [];
    const dataset =
      (selection.kind === "dataset"
        ? datasets.find((candidate) => candidate.id === selection.id)
        : undefined) ?? datasets[0];
    if (!dataset) return;
    const columns = datasetColumns(dataset.id, datasets, assetChoices);
    addVisualization(dataset.id, visualizationSuggestionForType(columns, type), placement);
  };

  const updateVisualization = (next: PresentationVisualization) => {
    changeArtifact({
      ...artifact,
      visualizations: (artifact.visualizations ?? []).map((visualization) =>
        visualization.id === next.id ? next : visualization,
      ),
    });
  };

  const deleteVisualization = (visualizationID: string) => {
    changeArtifact({
      ...artifact,
      visualizations: (artifact.visualizations ?? []).filter(
        (visualization) => visualization.id !== visualizationID,
      ),
      layout: artifact.layout?.filter((item) => item.visualization !== visualizationID),
      sections: artifact.sections?.filter((section) => section.visualization !== visualizationID),
    });
    setSelection({ kind: "artifact" });
  };

  const duplicateVisualization = (visualizationID: string) => {
    const source = (artifact.visualizations ?? []).find(
      (visualization) => visualization.id === visualizationID,
    );
    if (!source) return;
    const id = generatedPresentationID(
      `${source.id} copy`,
      "visualization",
      (artifact.visualizations ?? []).map((visualization) => visualization.id),
    );
    const copy = JSON.parse(JSON.stringify({ ...source, id })) as PresentationVisualization;
    if (artifact.kind === "dashboard") {
      const sourceLayout = artifact.layout?.find((item) => item.visualization === visualizationID);
      const bottom = (artifact.layout ?? []).reduce(
        (maximum, item) => Math.max(maximum, (item.y ?? 0) + (item.height || 4)),
        0,
      );
      changeArtifact({
        ...artifact,
        visualizations: [...(artifact.visualizations ?? []), copy],
        layout: [
          ...(artifact.layout ?? []),
          {
            visualization: id,
            x: sourceLayout?.x ?? 0,
            y: bottom,
            width: sourceLayout?.width || 6,
            height: sourceLayout?.height || 4,
          },
        ],
      });
    } else {
      const sections = [...(artifact.sections ?? [])];
      const sourceIndex = sections.findIndex(
        (section) => section.visualization === visualizationID,
      );
      sections.splice(sourceIndex < 0 ? sections.length : sourceIndex + 1, 0, {
        id: generatedPresentationID(
          `${id} section`,
          "chart",
          sections.map((section) => section.id),
        ),
        visualization: id,
      });
      changeArtifact({
        ...artifact,
        visualizations: [...(artifact.visualizations ?? []), copy],
        sections,
      });
    }
    setSelection({ kind: "visualization", id });
  };

  const addTextSection = (index = (artifact.sections ?? []).length) => {
    const sections = [...(artifact.sections ?? [])];
    const id = nextID(
      "text",
      sections.map((section) => section.id),
    );
    sections.splice(index, 0, {
      id,
      title: "Section",
      markdown: "Write the report narrative here.",
    });
    changeArtifact({ ...artifact, sections });
    setSelection({ kind: "section", id });
  };

  const insertReportBlock = (index: number, kind: "text" | "visualization" | "page_break") => {
    if (kind === "text") {
      addTextSection(index);
      return;
    }
    if (kind === "visualization") {
      openAddVisualization(undefined, index);
      return;
    }
    const sections = [...(artifact.sections ?? [])];
    if (index > 0 && sections[index - 1]) {
      sections[index - 1] = { ...sections[index - 1], page_break: true };
      changeArtifact({ ...artifact, sections });
    }
  };

  const moveSection = (from: number, to: number) => {
    const sections = [...(artifact.sections ?? [])];
    const [section] = sections.splice(from, 1);
    if (!section) return;
    sections.splice(to, 0, section);
    commit({ type: "sections.commit", sections });
  };

  const updateSection = (next: PresentationSection) => {
    const sections = (artifact.sections ?? []).map((section) =>
      section.id === next.id || section.id === selectionID(selection) ? next : section,
    );
    changeArtifact({ ...artifact, sections }, { coalesceKey: `section:${next.id}:content` });
    if (selection.kind === "section" && selection.id !== next.id)
      setSelection({ kind: "section", id: next.id });
  };

  const handleFilterValue = (filterID: string, value: unknown) => {
    const values = { ...filterValuesRef.current, [filterID]: value };
    setFilterValues(values);
    const affected = (artifact.visualizations ?? [])
      .filter((visualization) =>
        visualization.filter_bindings?.some((binding) => binding.filter === filterID),
      )
      .map((visualization) => visualization.id);
    if (affected.length > 0) void runPreview({ visualizationIDs: affected, values });
  };

  const activeFindings = previewResolved ? previewFindings : (artifact.problems ?? []);
  const loading = loadingIDs.size > 0;
  const previewLabel = loading
    ? "Refreshing data"
    : previewStale
      ? "Preview out of date"
      : "Draft data current";
  const focusFinding = (finding: PresentationFinding) => {
    const target = presentationFindingTarget(artifact, finding);
    setSelection(target.selection);
    setPendingFindingPath(target.path);
    if (!wideBuilder) {
      setDataOpen(false);
      setInspectorOpen(true);
    }
  };

  const renderSidebar = () => (
    <PresentationSidebar
      artifact={artifact}
      assetChoices={assetChoices}
      selection={selection}
      onSelect={(next) => {
        setSelection(next);
        if (!wideBuilder) {
          setDataOpen(false);
          if (next.kind === "dataset") setInspectorOpen(true);
        }
      }}
      onAddDataset={addDataset}
      onAddFilter={openAddFilter}
      onAddControl={addBlankFilter}
      onAddVisualization={(type) => openAddVisualization(type)}
      onAddText={() => addTextSection()}
    />
  );
  const renderInspector = () => (
    <PresentationInspector
      artifact={artifact}
      workspace={workspace}
      assetChoices={assetChoices}
      selection={selection}
      findings={activeFindings}
      focusPath={pendingFindingPath}
      onFocusPathHandled={() => setPendingFindingPath(null)}
      onSelect={setSelection}
      onChange={changeArtifact}
      onDeleteVisualization={deleteVisualization}
    />
  );

  const commandBar = (
    <DocumentAuthoringCommandBar
      navigation={
        <>
          {navigation}
          <Button
            size="icon-sm"
            variant="ghost"
            className="xl:hidden"
            aria-label="Open builder tools"
            onClick={() => setDataOpen(true)}
          >
            <PanelLeft data-icon="inline-start" />
          </Button>
        </>
      }
      identity={
        <Input
          aria-label="Presentation title"
          data-testid="presentation-title"
          value={artifact.title}
          className="h-8 w-full border-transparent bg-transparent px-2 text-sm font-medium shadow-none hover:border-input focus-visible:border-input"
          onChange={(event) =>
            changeArtifact(
              { ...artifact, title: event.target.value },
              { coalesceKey: "artifact:title" },
            )
          }
        />
      }
      mode={modeControl}
      status={
        <>
          <Badge
            variant="outline"
            className="hidden shrink-0 font-normal text-muted-foreground lg:inline-flex"
          >
            {loading ? (
              <Loader2 className="animate-spin" />
            ) : previewStale ? (
              <TriangleAlert />
            ) : (
              <Check />
            )}
            {previewLabel}
          </Badge>
          {activeFindings.length > 0 ? (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button
                  size="sm"
                  variant="outline"
                  aria-label={`Review ${activeFindings.length} definition ${activeFindings.length === 1 ? "finding" : "findings"}`}
                >
                  <TriangleAlert data-icon="inline-start" />
                  {activeFindings.length}
                  <span className="hidden xl:inline">Review</span>
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="max-h-80 w-[min(24rem,calc(100vw-2rem))]">
                {activeFindings.map((finding, index) => (
                  <DropdownMenuItem
                    key={`${finding.code}:${finding.path ?? ""}:${index}`}
                    className="items-start"
                    onSelect={() => focusFinding(finding)}
                  >
                    <TriangleAlert
                      className={
                        finding.severity === "error" ? "text-destructive" : "text-muted-foreground"
                      }
                    />
                    <span className="min-w-0">
                      <span className="block whitespace-normal text-xs leading-snug">
                        {finding.message}
                      </span>
                      {finding.path ? (
                        <span className="mt-0.5 block truncate font-mono text-[10px] text-muted-foreground">
                          {finding.path}
                        </span>
                      ) : null}
                    </span>
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          ) : null}
        </>
      }
      history={
        <>
          <Button
            size="icon-sm"
            variant="ghost"
            disabled={!canUndo}
            aria-label="Undo"
            onClick={undo}
          >
            <Undo2 data-icon="inline-start" />
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            disabled={!canRedo}
            aria-label="Redo"
            onClick={redo}
          >
            <Redo2 data-icon="inline-start" />
          </Button>
        </>
      }
      tools={
        <>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label="Refresh draft data"
            disabled={loading || paused}
            onClick={() => void runPreview({ includeOptions: true, replaceResults: true })}
          >
            {loading ? (
              <Loader2 data-icon="inline-start" className="animate-spin" />
            ) : (
              <RefreshCw data-icon="inline-start" />
            )}
          </Button>
          <Button
            size="icon-sm"
            variant="ghost"
            className="xl:hidden"
            aria-label="Open inspector"
            onClick={() => setInspectorOpen(true)}
          >
            <SlidersHorizontal data-icon="inline-start" />
          </Button>
        </>
      }
      actions={documentActions}
    />
  );

  return (
    <div data-testid="presentation-builder" className="h-full min-h-0">
      <DocumentAuthoringShell commandBar={commandBar} banner={banner} className="bg-muted/30">
        <div className="flex min-h-0 flex-1">
          {wideBuilder ? (
            <aside className="w-60 shrink-0 border-r bg-background">{renderSidebar()}</aside>
          ) : null}
          <main className="min-w-0 flex-1">
            <ScrollArea className="h-full">
              <div
                className="min-h-full p-3 sm:p-5"
                style={{
                  backgroundImage:
                    artifact.kind === "dashboard"
                      ? "radial-gradient(color-mix(in srgb, var(--border) 70%, transparent) 0.7px, transparent 0.7px)"
                      : undefined,
                  backgroundSize: artifact.kind === "dashboard" ? "18px 18px" : undefined,
                }}
              >
                {previewError ? (
                  <Alert className="mx-auto mb-3 max-w-5xl border-amber-500/35 bg-background/95">
                    <TriangleAlert />
                    <AlertTitle>Draft preview needs attention</AlertTitle>
                    <AlertDescription>{previewError}</AlertDescription>
                  </Alert>
                ) : null}
                {artifact.kind === "dashboard" ? (
                  <div className="mx-auto flex w-full max-w-[94rem] flex-col gap-3">
                    <div className="flex justify-end">
                      <ToggleGroup
                        type="single"
                        value={previewMode}
                        onValueChange={(value) => {
                          const next = value as PresentationPreviewMode | undefined;
                          if (next) setPreviewMode(next);
                        }}
                        variant="outline"
                        size="sm"
                        spacing={0}
                        aria-label="Dashboard preview size"
                      >
                        <ToggleGroupItem value="desktop" aria-label="Desktop preview">
                          <Monitor />
                        </ToggleGroupItem>
                        <ToggleGroupItem value="tablet" aria-label="Tablet preview">
                          <Tablet />
                        </ToggleGroupItem>
                        <ToggleGroupItem value="mobile" aria-label="Mobile preview">
                          <Smartphone />
                        </ToggleGroupItem>
                      </ToggleGroup>
                    </div>
                    <DashboardFilterStrip
                      filters={artifact.filters ?? []}
                      values={filterValues}
                      optionResults={optionResults}
                      selection={selection}
                      onValueChange={handleFilterValue}
                      onSelect={setSelection}
                      onAdd={openAddFilter}
                      onDropControl={addBlankFilter}
                    />
                    <DashboardCanvas
                      artifact={artifact}
                      results={results}
                      loadingIDs={loadingIDs}
                      selection={selection}
                      previewMode={previewMode}
                      findings={activeFindings}
                      onSelect={setSelection}
                      onLayoutCommit={(layout: PresentationLayoutItem[]) =>
                        commit({ type: "layout.commit", layout })
                      }
                      onVisualizationChange={updateVisualization}
                      onDuplicate={duplicateVisualization}
                      onDelete={deleteVisualization}
                      onAdd={() => openAddVisualization()}
                      onDropVisualization={(type, dashboard) =>
                        addDroppedVisualization(type, dashboard ? { dashboard } : undefined)
                      }
                    />
                  </div>
                ) : (
                  <div className="mx-auto flex w-full max-w-[94rem] flex-col gap-3">
                    <DashboardFilterStrip
                      filters={artifact.filters ?? []}
                      values={filterValues}
                      optionResults={optionResults}
                      selection={selection}
                      onValueChange={handleFilterValue}
                      onSelect={setSelection}
                      onAdd={openAddFilter}
                      onDropControl={addBlankFilter}
                    />
                    <ReportCanvas
                      artifact={artifact}
                      results={results}
                      loadingIDs={loadingIDs}
                      selection={selection}
                      findings={activeFindings}
                      onSelect={setSelection}
                      onSectionChange={updateSection}
                      onMove={moveSection}
                      onDelete={(sectionID) => {
                        changeArtifact({
                          ...artifact,
                          sections: (artifact.sections ?? []).filter(
                            (section) => section.id !== sectionID,
                          ),
                        });
                        setSelection({ kind: "artifact" });
                      }}
                      onInsert={insertReportBlock}
                      onDropVisualization={(reportIndex, type) =>
                        addDroppedVisualization(type, { reportIndex })
                      }
                    />
                  </div>
                )}
              </div>
            </ScrollArea>
          </main>
          {wideBuilder ? (
            <aside className="w-[clamp(22rem,25vw,28rem)] min-w-0 shrink-0 overflow-hidden border-l bg-background">
              <ScrollArea className="h-full">{renderInspector()}</ScrollArea>
            </aside>
          ) : null}
        </div>
      </DocumentAuthoringShell>
      {!wideBuilder ? (
        <>
          <Sheet open={dataOpen} onOpenChange={setDataOpen}>
            <SheetContent side="left" className="w-[min(22rem,90vw)] p-0">
              <SheetHeader className="border-b p-4">
                <SheetTitle>
                  {artifact.kind === "report" ? "Report outline" : "Builder tools"}
                </SheetTitle>
                <SheetDescription>
                  Add components and manage presentation datasets.
                </SheetDescription>
              </SheetHeader>
              <div className="min-h-0 flex-1">{dataOpen ? renderSidebar() : null}</div>
            </SheetContent>
          </Sheet>
          <Sheet open={inspectorOpen} onOpenChange={setInspectorOpen}>
            <SheetContent
              side="right"
              className="w-[min(26rem,92vw)] max-w-full overflow-hidden p-0"
            >
              <SheetHeader className="border-b p-4">
                <SheetTitle>Inspector</SheetTitle>
                <SheetDescription>Edit only the selected presentation component.</SheetDescription>
              </SheetHeader>
              <ScrollArea className="min-h-0 flex-1">
                {inspectorOpen ? renderInspector() : null}
              </ScrollArea>
            </SheetContent>
          </Sheet>
        </>
      ) : null}
      <AddVisualizationDialog
        open={addOpen}
        datasets={artifact.datasets ?? []}
        assetChoices={assetChoices}
        preferredType={preferredType}
        onOpenChange={(open) => {
          setAddOpen(open);
          if (!open) setPendingReportIndex(null);
        }}
        onAdd={addVisualization}
      />
      <AddFilterDialog
        open={addFilterOpen}
        datasets={artifact.datasets ?? []}
        visualizations={artifact.visualizations ?? []}
        assetChoices={assetChoices}
        existingIDs={(artifact.filters ?? []).map((filter) => filter.id)}
        onOpenChange={setAddFilterOpen}
        onAdd={(filter, bindings) => {
          changeArtifact({
            ...artifact,
            filters: [...(artifact.filters ?? []), filter],
            visualizations: (artifact.visualizations ?? []).map((visualization) => {
              const binding = bindings[visualization.id];
              return binding
                ? {
                    ...visualization,
                    filter_bindings: [...(visualization.filter_bindings ?? []), binding],
                  }
                : visualization;
            }),
          });
          setSelection({ kind: "filter", id: filter.id });
          showInspector();
        }}
        onAddBlank={() => addBlankFilter()}
      />
    </div>
  );
}

function previewDataSignature(artifact: PresentationArtifact) {
  return JSON.stringify({
    datasets: artifact.datasets ?? [],
    filters: artifact.filters ?? [],
    visualizations: (artifact.visualizations ?? []).map((visualization) => ({
      id: visualization.id,
      dataset: visualization.dataset,
      definition: previewDefinitionSignature(visualization.definition),
      filter_bindings: visualization.filter_bindings ?? [],
    })),
  });
}

function previewDefinitionSignature(definition: Record<string, unknown>) {
  return {
    type: definition.type,
    encoding: definition.encoding,
    columns: definition.columns,
    value: definition.value,
    compare: definition.compare,
    presentation_limit: definition.presentation_limit,
    require_complete: definition.require_complete,
  };
}

function selectionExists(artifact: PresentationArtifact, selection: PresentationBuilderSelection) {
  if (selection.kind === "artifact") return true;
  if (selection.kind === "dataset")
    return (artifact.datasets ?? []).some((dataset) => dataset.id === selection.id);
  if (selection.kind === "filter")
    return (artifact.filters ?? []).some((filter) => filter.id === selection.id);
  if (selection.kind === "visualization")
    return (artifact.visualizations ?? []).some(
      (visualization) => visualization.id === selection.id,
    );
  return (artifact.sections ?? []).some((section) => section.id === selection.id);
}

function selectionID(selection: PresentationBuilderSelection) {
  return selection.kind === "artifact" ? "" : selection.id;
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "The preview request failed.";
}

function useWideBuilder() {
  const [wide, setWide] = useState(() =>
    typeof window === "undefined" ? false : window.matchMedia("(min-width: 1280px)").matches,
  );

  useEffect(() => {
    const query = window.matchMedia("(min-width: 1280px)");
    const update = () => setWide(query.matches);
    update();
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);

  return wide;
}
