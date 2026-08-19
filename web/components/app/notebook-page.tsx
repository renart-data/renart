"use client";

import { useNavigate } from "@tanstack/react-router";
import { useAtomValue, useSetAtom } from "jotai";
import {
  AlertTriangle,
  ArrowUpFromLine,
  BookOpen,
  Check,
  ChevronRight,
  Database,
  Download,
  FileInput,
  Gauge,
  Globe2,
  ListTree,
  Loader2,
  MoreHorizontal,
  Package,
  PanelLeft,
  Pencil,
  Play,
  Plus,
  RotateCw,
  SlidersHorizontal,
  Square,
  Trash2,
} from "lucide-react";
import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from "react";

import { AnsiOutput } from "@/components/ansi-output";
import { ConnectionSelect } from "@/components/app/connection-select";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  DelimitedCardContent,
  DelimitedCardHeader,
  DelimitedCardTitle,
} from "@/components/ui/delimited-card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { HoverCard, HoverCardContent, HoverCardTrigger } from "@/components/ui/hover-card";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  cancelNotebookRun,
  closeNotebookSession,
  configureNotebookCellSource,
  createNotebookControl,
  createNotebookCellAt,
  createNotebookMarkdown,
  createNotebookVisualization,
  createNotebookSource,
  createNotebookWarehouseSource,
  deleteNotebookBlock,
  deleteNotebookControl,
  deleteNotebook,
  deleteNotebookCell,
  getNotebook,
  getNotebookRuntime,
  joinCellContent,
  migrateLegacyNotebookVisualization,
  notebookCellExportURL,
  NotebookCellRunResult,
  planNotebookCellPromotion,
  promoteNotebookCell,
  refreshNotebookControlOptions,
  type PromoteCellPlan,
  replaceNotebookParameters,
  renameNotebookCell,
  runNotebook,
  setNotebookSettings,
  splitCellContent,
  updateNotebookCell,
  updateNotebookControl,
  updateNotebookBlocks,
  updateNotebookDependencies,
  updateNotebookMarkdown,
  updateNotebookVisualization,
  upgradeNotebookManifest,
} from "@/lib/api-notebooks";
import type { NotebookBlockPosition } from "@/lib/api-notebooks";
import { notebookAgentEventsAtom, notebookRuntimeEventsAtom } from "@/lib/atoms/domains/results";
import {
  selectedEnvironmentAtom,
  selectedExecutionTimeWindowAtom,
  workspaceAtom,
} from "@/lib/atoms/domains/workspace";
import { sqlDiscoveryTablesAtom } from "@/lib/atoms/sql-discovery";
import { usesPythonSource } from "@/lib/asset-types";
import { addDependency, missingPythonImports } from "@/lib/notebook-python-deps";
import {
  AUTHORED_CONTROL_TYPE_LABELS,
  AUTHORED_CONTROL_TYPES,
  defaultAuthoredControlRange,
  defaultAuthoredControlValue,
  type AuthoredControlType,
} from "@/lib/authored-controls";
import type { NotebookParameter, PresentationDatasetResult } from "@/lib/generated/api-types";
import { WebAsset, WebNotebook, WebNotebookBlock, WorkspaceQueryConnection } from "@/lib/types";
import { cn } from "@/lib/utils";
import {
  VirtualDataTable,
  type VirtualTableRenderMeasurement,
} from "@/components/virtual-data-table";

import { hasAuthoringDragItem, readAuthoringDragItem } from "./authoring-drag";
import { CHART_TYPE_OPTIONS, ChartTypePicker, type ChartType } from "./chart-type-picker";
import { ControlTypePicker } from "./control-type-picker";
import { DocumentAuthoringSidebar, type DocumentAuthoringTab } from "./document-authoring-sidebar";
import { MissingPythonDepsBanner } from "./missing-python-deps";
import { NotebookAgentChat } from "./notebook-agent-panel";
import { NotebookBlockTypePicker } from "./notebook-block-type-picker";
import { NotebookControlBlock } from "./notebook-control-block";
import type { AuthoredControlDataset } from "./authored-control";
import { MarkdownEditor } from "./markdown-editor";
import { NewNotebookDialog } from "./new-notebook-dialog";
import { buildNotebookSchemaTables, NotebookCellMonaco } from "./notebook-cell-editor";
import { NotebookVizRenderer } from "./notebook-viz";
import { NotebookVisualizationBlockCard } from "./notebook-visualization-block";
import { NotebookParametersDialog } from "./notebook-parameters-dialog";
import { LoadStreamPicker } from "./load-stream-picker";
import { PageHeader, AppPage, AppPanel } from "./app-primitives";
import {
  semanticTypeForPhysicalType,
  visualizationSuggestionForType,
} from "./presentation-builder/presentation-builder-model";

// How long to wait after the last keystroke before auto-committing a cell's
// draft. The save marks the cell stale on the server, which drives recompute.
const AUTO_COMMIT_DEBOUNCE_MS = 350;
const NOTEBOOK_CELL_JUMP_HIGHLIGHT_MS = 1600;
const NOTEBOOK_BLOCK_ENTER_ANIMATION =
  "animate-in fade-in-0 slide-in-from-bottom-2 duration-300 motion-reduce:animate-none";
const NOTEBOOK_BLOCK_CARD_CLASS =
  "group/notebook-block border-transparent bg-transparent shadow-none transition-colors hover:border-border hover:bg-card hover:shadow-sm focus-within:border-border focus-within:bg-card focus-within:shadow-sm";
const NOTEBOOK_BLOCK_HEADER_CLASS =
  "border-transparent bg-transparent transition-colors group-hover/notebook-block:border-border group-hover/notebook-block:bg-muted/20 group-focus-within/notebook-block:border-border group-focus-within/notebook-block:bg-muted/20";

type PendingNotebookBlockKind = "sql" | "python" | "markdown" | "visualization" | "control";
type NotebookBlockPlacement = Required<Pick<NotebookBlockPosition, "position">> & {
  after_block_id?: string;
};
type NotebookBlockCreateOptions = {
  placement?: NotebookBlockPlacement;
  visualizationType?: ChartType;
  controlType?: AuthoredControlType;
};
type NotebookCellDeleteTarget = { id: string; name: string };
type NotebookControlOptionSnapshot = {
  signature: string;
  result: PresentationDatasetResult;
  refreshedAt: number;
};

function notebookControlOptionSignature(control: NotebookParameter): string {
  return JSON.stringify({
    type: control.type,
    dataset: control.options?.dataset?.trim() ?? "",
    valueField: control.options?.value_field?.trim() ?? "",
    labelField: control.options?.label_field?.trim() ?? "",
  });
}

function notebookControlProducer(
  control: NotebookParameter,
  cells: WebAsset[],
): WebAsset | undefined {
  const dataset = control.options?.dataset?.trim();
  if (!dataset) return undefined;
  return (
    cells.find((cell) => cell.cell_id === dataset) ??
    cells.find((cell) => cell.name.toLowerCase() === dataset.toLowerCase())
  );
}

function notebookBlockKey(block: WebNotebookBlock, index: number) {
  if (block.cell) {
    return `cell:${block.cell}`;
  }
  if (block.control) {
    return `control:${block.control}`;
  }
  if (block.id) {
    return `block:${block.id}`;
  }
  return `legacy-markdown:${index}`;
}

function notebookBlockStableID(block: WebNotebookBlock): string | undefined {
  if (block.cell) return block.cell;
  if (block.control) return `control:${block.control}`;
  return block.id;
}

function notebookPlacementKey(placement: NotebookBlockPlacement): string {
  return placement.position === "start" ? "start" : `after:${placement.after_block_id ?? "end"}`;
}

export function AppNotebooksIndexPage() {
  const workspace = useAtomValue(workspaceAtom);
  const navigate = useNavigate();
  const notebooks = workspace?.notebooks ?? [];
  const [newNotebookOpen, setNewNotebookOpen] = useState(false);

  return (
    <AppPage>
      <PageHeader
        title="Notebooks"
        actions={
          <Button size="sm" onClick={() => setNewNotebookOpen(true)}>
            <Plus className="size-3.5" />
            New notebook
          </Button>
        }
      />
      <div className="min-h-0 flex-1 overflow-auto px-3 pb-3">
        {/* my-auto centers the (usually short) content vertically; long lists
            grow past the viewport and scroll normally. */}
        <div className="flex min-h-full flex-col">
          {notebooks.length === 0 ? (
            <div className="mx-auto my-auto w-full max-w-md rounded-xl border border-dashed p-8 text-center">
              <BookOpen className="mx-auto mb-3 size-8 text-muted-foreground" />
              <div className="text-sm font-medium">No notebooks yet</div>
              <p className="mt-1 text-xs text-muted-foreground">
                Notebooks are folders of SQL cells that run in a disposable local DuckDB session.
              </p>
              <Button size="sm" className="mt-4" onClick={() => setNewNotebookOpen(true)}>
                <Plus className="size-3.5" />
                New notebook
              </Button>
            </div>
          ) : (
            <div className="mx-auto my-auto w-full max-w-2xl py-6">
              <p className="mb-3 px-1 text-xs text-muted-foreground">
                Exploratory SQL against a local DuckDB session — promote cells to pipelines when
                ready.
              </p>
              <div className="divide-y overflow-hidden rounded-xl border bg-card">
                {notebooks.map((notebook) => (
                  <button
                    key={notebook.id}
                    type="button"
                    onClick={() =>
                      void navigate({
                        to: "/notebooks/$notebookId",
                        params: { notebookId: notebook.id },
                      })
                    }
                    className="flex w-full items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/50"
                  >
                    <BookOpen className="size-4 shrink-0 text-primary" />
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-sm font-medium">{notebook.title}</span>
                      <span className="block truncate font-mono text-[11px] text-muted-foreground">
                        {notebook.path}
                      </span>
                    </span>
                    {notebook.problems?.length ? (
                      <span className="inline-flex shrink-0 items-center gap-1 text-[11px] text-amber-600 dark:text-amber-400">
                        <AlertTriangle className="size-3" />
                        {notebook.problems.length}
                      </span>
                    ) : null}
                    <span className="shrink-0 text-[11px] text-muted-foreground">
                      {notebook.cells.length} cell{notebook.cells.length === 1 ? "" : "s"}
                    </span>
                    <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
                  </button>
                ))}
              </div>
              <button
                type="button"
                onClick={() => setNewNotebookOpen(true)}
                className="mt-3 flex h-9 w-full items-center justify-center gap-2 rounded-xl border border-dashed text-xs text-muted-foreground transition-colors hover:bg-muted/50"
              >
                <Plus className="size-3.5" /> New notebook
              </button>
            </div>
          )}
        </div>
      </div>
      <NewNotebookDialog open={newNotebookOpen} onOpenChange={setNewNotebookOpen} />
    </AppPage>
  );
}

export function AppNotebookLivePage({ notebookId }: { notebookId: string }) {
  const workspace = useAtomValue(workspaceAtom);
  const notebookRuntimeEvent = useAtomValue(notebookRuntimeEventsAtom)[notebookId] ?? null;
  const notebookAgentEvent = useAtomValue(notebookAgentEventsAtom)[notebookId] ?? null;
  const notebookRuntimeEventRef = useRef(notebookRuntimeEvent);
  notebookRuntimeEventRef.current = notebookRuntimeEvent;
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const selectedExecutionTimeWindow = useAtomValue(selectedExecutionTimeWindowAtom);
  const navigate = useNavigate();

  const stateNotebook = useMemo(
    () => workspace?.notebooks?.find((candidate) => candidate.id === notebookId) ?? null,
    [notebookId, workspace?.notebooks],
  );
  // Mutations return the fresh notebook before the SSE state catches up;
  // prefer the newer of the two.
  const [mutated, setMutated] = useState<WebNotebook | null>(null);
  const [loadError, setLoadError] = useState("");
  const notebook = mutated ?? stateNotebook;

  useEffect(() => {
    if (!mutated || !stateNotebook) {
      return;
    }
    if (mutated.revision === stateNotebook.revision) {
      setMutated(null);
      return;
    }

    // Workspace SSE can overtake a just-completed mutation with a snapshot
    // that was assembled before the filesystem watcher observed that write.
    // Resolve differing revisions against the authoritative notebook endpoint
    // instead of briefly replacing the mutation response with stale blocks.
    let cancelled = false;
    getNotebook(notebookId)
      .then((loaded) => {
        if (cancelled) return;
        setMutated(loaded.revision === stateNotebook.revision ? null : loaded);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [mutated?.revision, notebookId, stateNotebook]);

  useEffect(() => {
    if (stateNotebook || mutated) {
      return;
    }
    let cancelled = false;
    getNotebook(notebookId)
      .then((loaded) => {
        if (!cancelled) {
          setMutated(loaded);
        }
      })
      .catch((error) => {
        if (!cancelled) {
          setLoadError(String(error));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [mutated, notebookId, stateNotebook]);

  // Staleness, results, the running set, and which stale cells will auto-update
  // are all owned by the server now; the client renders what the runtime SSE
  // stream (and the initial snapshot) report. See
  // architecture/notebooks.md.
  const [results, setResults] = useState<Record<string, NotebookCellRunResult>>({});
  const [staleCells, setStaleCells] = useState<Set<string>>(new Set());
  const [autoPending, setAutoPending] = useState<Set<string>>(new Set());
  const [runningCells, setRunningCells] = useState<Set<string>>(new Set());
  const [runBusy, setRunBusy] = useState(false);
  const [stopping, setStopping] = useState(false);
  const busy = runBusy || stopping;
  const [actionError, setActionError] = useState("");
  const [notebookScrolled, setNotebookScrolled] = useState(false);
  const [pendingBlock, setPendingBlock] = useState<{
    id: number;
    kind: PendingNotebookBlockKind;
    placement: NotebookBlockPlacement;
  } | null>(null);
  const [enteringBlockKey, setEnteringBlockKey] = useState<string | null>(null);
  const [jumpHighlightedCellId, setJumpHighlightedCellId] = useState<string | null>(null);
  const [scrollRevision, setScrollRevision] = useState(0);
  const [cellToDelete, setCellToDelete] = useState<NotebookCellDeleteTarget | null>(null);
  const [deletingCell, setDeletingCell] = useState(false);
  const [depsOpen, setDepsOpen] = useState(false);
  const [parametersOpen, setParametersOpen] = useState(false);
  const [addDataOpen, setAddDataOpen] = useState(false);
  const [promoting, setPromoting] = useState<WebAsset | null>(null);
  const [toolsOpen, setToolsOpen] = useState(false);
  const [toolsTab, setToolsTab] = useState("outline");
  const [selectedVisualizationID, setSelectedVisualizationID] = useState<string | null>(null);
  const [selectedControlID, setSelectedControlID] = useState<string | null>(null);
  const [visualizationInspectorOpen, setVisualizationInspectorOpen] = useState(false);
  const [visualizationInspectorTarget, setVisualizationInspectorTarget] =
    useState<HTMLDivElement | null>(null);
  const notebookViewportRef = useRef<HTMLDivElement>(null);
  const pendingBlockSequenceRef = useRef(0);
  const jumpHighlightFrameRef = useRef<number | null>(null);
  const jumpHighlightTimerRef = useRef<number | null>(null);
  const parameterSaveTimerRef = useRef<number | null>(null);
  const parameterValuesRef = useRef<Record<string, unknown>>({});
  const [autoRecompute, setAutoRecompute] = useState(
    () =>
      typeof window === "undefined" ||
      window.localStorage.getItem("renart-notebook-autorecompute") !== "off",
  );
  const [parameterValues, setParameterValues] = useState<Record<string, unknown>>({});
  const [controlOptionSnapshots, setControlOptionSnapshots] = useState<
    Record<string, NotebookControlOptionSnapshot>
  >({});
  const [loadingControlOptions, setLoadingControlOptions] = useState<Set<string>>(new Set());
  const controlOptionRequestSequenceRef = useRef(0);
  const controlOptionRequestTokensRef = useRef<Map<string, number>>(new Map());
  const controlOptionRuntimeEventRef = useRef(notebookRuntimeEvent);
  const wideNotebookTools = useWideNotebookTools();
  useEffect(() => {
    window.localStorage.setItem("renart-notebook-autorecompute", autoRecompute ? "on" : "off");
  }, [autoRecompute]);

  // Mirror the toggle (and import environment) to the server, which owns the
  // recompute loop. Runs on load and whenever either changes.
  useEffect(() => {
    void setNotebookSettings(notebookId, {
      auto_recompute: autoRecompute,
      environment: selectedEnvironment,
    }).catch(() => undefined);
  }, [notebookId, autoRecompute, selectedEnvironment]);

  useEffect(() => {
    setNotebookScrolled(false);
    setPendingBlock(null);
    setEnteringBlockKey(null);
    setJumpHighlightedCellId(null);
    setCellToDelete(null);
    setDeletingCell(false);
    setAddDataOpen(false);
    setParametersOpen(false);
    setToolsOpen(false);
    setToolsTab("outline");
    setSelectedVisualizationID(null);
    setSelectedControlID(null);
    setVisualizationInspectorOpen(false);
    setVisualizationInspectorTarget(null);
    setParameterValues({});
    setControlOptionSnapshots({});
    setLoadingControlOptions(new Set());
    controlOptionRequestTokensRef.current.clear();
    controlOptionRuntimeEventRef.current = notebookRuntimeEventRef.current;
    parameterValuesRef.current = {};
    if (parameterSaveTimerRef.current !== null) {
      window.clearTimeout(parameterSaveTimerRef.current);
      parameterSaveTimerRef.current = null;
    }
    if (jumpHighlightFrameRef.current !== null) {
      window.cancelAnimationFrame(jumpHighlightFrameRef.current);
      jumpHighlightFrameRef.current = null;
    }
    if (jumpHighlightTimerRef.current !== null) {
      window.clearTimeout(jumpHighlightTimerRef.current);
      jumpHighlightTimerRef.current = null;
    }
  }, [notebookId]);

  useEffect(() => {
    if (!selectedVisualizationID || !notebook) return;
    if (
      notebook.blocks.some(
        (block) => block.id === selectedVisualizationID && Boolean(block.visualization),
      )
    ) {
      return;
    }
    setSelectedVisualizationID(null);
    setVisualizationInspectorOpen(false);
  }, [notebook, selectedVisualizationID]);

  useEffect(() => {
    if (!selectedControlID || !notebook) return;
    if (notebook.parameters?.some((parameter) => parameter.id === selectedControlID)) {
      return;
    }
    setSelectedControlID(null);
    setVisualizationInspectorOpen(false);
  }, [notebook, selectedControlID]);

  useEffect(() => {
    if (wideNotebookTools) setVisualizationInspectorOpen(false);
  }, [wideNotebookTools]);

  useEffect(
    () => () => {
      if (jumpHighlightFrameRef.current !== null) {
        window.cancelAnimationFrame(jumpHighlightFrameRef.current);
      }
      if (jumpHighlightTimerRef.current !== null) {
        window.clearTimeout(jumpHighlightTimerRef.current);
      }
      if (parameterSaveTimerRef.current !== null) {
        window.clearTimeout(parameterSaveTimerRef.current);
      }
    },
    [],
  );

  useEffect(() => {
    parameterValuesRef.current = parameterValues;
  }, [parameterValues]);

  // Adding a block changes the scroll height twice: once for the immediate
  // pending card, then again when the real editor replaces it. Scroll after
  // both commits so the newly appended block and add controls stay in view.
  useEffect(() => {
    if (scrollRevision === 0) {
      return;
    }
    const frame = window.requestAnimationFrame(() => {
      const viewport = notebookViewportRef.current;
      if (!viewport) {
        return;
      }
      const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
      viewport.scrollTo({
        top: viewport.scrollHeight,
        behavior: reducedMotion ? "auto" : "smooth",
      });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [scrollRevision]);

  const cellsById = useMemo(() => {
    const map = new Map<string, WebAsset>();
    for (const cell of notebook?.cells ?? []) {
      if (cell.cell_id) {
        map.set(cell.cell_id, cell);
      }
    }
    return map;
  }, [notebook?.cells]);

  // name → downstream cell ids, for client-side stale propagation.
  // Merge run results into the local map for immediate feedback after a manual
  // run. Staleness is reconciled by the runtime SSE stream, not here.
  const applyResults = useCallback((runResults: NotebookCellRunResult[]) => {
    setResults((current) => {
      const next = { ...current };
      for (const result of runResults) {
        next[result.cell_id] = result;
      }
      return next;
    });
  }, []);

  // Apply a runtime snapshot/event from the server: the authoritative stale,
  // auto-pending, and running sets, plus any result deltas.
  const applyRuntime = useCallback(
    (runtime: {
      stale: string[];
      auto_pending: string[];
      running?: string[];
      results?: Record<string, NotebookCellRunResult>;
      parameter_values?: Record<string, unknown>;
    }) => {
      setStaleCells(new Set(runtime.stale));
      setAutoPending(new Set(runtime.auto_pending));
      if (runtime.running) {
        setRunningCells(new Set(runtime.running));
      }
      if (runtime.results && Object.keys(runtime.results).length > 0) {
        setResults((current) => ({ ...current, ...runtime.results }));
      }
      if (runtime.parameter_values) {
        setParameterValues(runtime.parameter_values);
      }
    },
    [],
  );

  // In-flight cell saves, keyed by cell id. A run must wait for these to land
  // on disk first: otherwise the backend reloads the cell before the save's
  // write completes and runs stale SQL (the "run twice for @viz" bug).
  const pendingSavesRef = useRef<Map<string, Promise<void>>>(new Map());
  const saveSeqRef = useRef<Map<string, number>>(new Map());
  // Full-document saves are serialized per cell. Besides fixing response
  // reordering in one editor, each request carries the revision acknowledged
  // by the previous response, so another tab or a filesystem edit becomes an
  // explicit conflict instead of a silent last-writer-wins overwrite.
  const saveQueuesRef = useRef<
    Map<
      string,
      {
        tail: Promise<void>;
        pending: number;
        revision: string;
        knownRevisions: Set<string>;
      }
    >
  >(new Map());

  const saveCellBody = useCallback(
    (cell: WebAsset, body: string, baseRevision: string): Promise<void> => {
      const { header } = splitCellContent(cell.content);
      const cellId = cell.cell_id ?? "";
      const seq = (saveSeqRef.current.get(cellId) ?? 0) + 1;
      saveSeqRef.current.set(cellId, seq);
      const draftRevision = baseRevision || cell.content_revision || "";
      let queue = saveQueuesRef.current.get(cellId);
      if (!queue) {
        queue = {
          tail: Promise.resolve(),
          pending: 0,
          revision: draftRevision,
          knownRevisions: new Set(draftRevision ? [draftRevision] : []),
        };
        saveQueuesRef.current.set(cellId, queue);
      } else if (
        queue.pending === 0 &&
        draftRevision &&
        draftRevision !== queue.revision &&
        !queue.knownRevisions.has(draftRevision)
      ) {
        // The cell card only advances its draft revision when it adopts the
        // corresponding server body. A different revision here is therefore a
        // clean external snapshot, not merely a delayed echo of our last save.
        queue.revision = draftRevision;
        queue.knownRevisions.add(draftRevision);
      }
      queue.pending += 1;

      const previous = queue.tail;
      const promise = previous.then(async () => {
        try {
          const requestRevision = queue.revision;
          if (requestRevision) {
            queue.knownRevisions.add(requestRevision);
          }
          // Saving marks the cell + descendants stale on the server, which then
          // drives auto-recompute and pushes the new state over SSE.
          const updated = await updateNotebookCell(
            notebookId,
            cellId,
            joinCellContent(header, body),
            requestRevision,
          );
          const updatedCell = updated.cells.find((candidate) => candidate.cell_id === cellId);
          if (updatedCell?.content_revision) {
            queue.revision = updatedCell.content_revision;
            queue.knownRevisions.add(updatedCell.content_revision);
          }
          // A queued newer save owns the visible mutation result. Applying an
          // intermediate response here would recreate the stale echo that can
          // replace the tail of an actively edited Monaco document.
          if (saveSeqRef.current.get(cellId) === seq) {
            setMutated(updated);
          }
        } catch (error) {
          setActionError(String(error));
        } finally {
          queue.pending = Math.max(0, queue.pending - 1);
          // Only the most recent save for this cell clears the pending slot,
          // so a slower earlier save cannot drop a newer one.
          if (saveSeqRef.current.get(cellId) === seq) {
            pendingSavesRef.current.delete(cellId);
          }
        }
      });
      queue.tail = promise;
      pendingSavesRef.current.set(cellId, promise);
      return promise;
    },
    [notebookId],
  );

  const flushPendingSaves = useCallback(async () => {
    const pending = [...pendingSavesRef.current.values()];
    if (pending.length > 0) {
      await Promise.allSettled(pending);
    }
  }, []);

  // The in-flight request is aborted immediately when Stop is pressed, while
  // the explicit cancellation endpoint provides the durable server-side
  // barrier that waits for DuckDB to release the notebook session.
  const runAbortRef = useRef<AbortController | null>(null);
  const runRequest = useCallback(
    async (
      input: { all?: boolean; from?: string; cells?: string[]; refresh_imports?: boolean },
      targetIds: string[],
    ) => {
      const controller = new AbortController();
      runAbortRef.current = controller;
      setRunBusy(true);
      setActionError("");
      setRunningCells(new Set(targetIds));
      try {
        // Make sure any unsaved cell edits have landed before the backend
        // reloads the notebook, so the run sees the latest SQL and directives.
        await flushPendingSaves();
        const response = await runNotebook(
          notebookId,
          {
            ...input,
            environment: selectedEnvironment,
            // Render Jinja against the same execution window the editor previews.
            start_date: selectedExecutionTimeWindow?.start,
            end_date: selectedExecutionTimeWindow?.end,
          },
          controller.signal,
        );
        applyResults(response.results);
      } catch (error) {
        if (!controller.signal.aborted) {
          setActionError(String(error));
        }
        // On abort the server parks the cells (via the cancel call below); the
        // runtime SSE stream reconciles staleness, so nothing to do here.
      } finally {
        if (runAbortRef.current === controller) {
          runAbortRef.current = null;
        }
        setRunBusy(false);
        setRunningCells(new Set());
      }
    },
    [applyResults, flushPendingSaves, notebookId, selectedEnvironment, selectedExecutionTimeWindow],
  );

  // Stop both manual and automatic work. Keep the notebook busy until the
  // server confirms every run has unwound and released the session lock.
  const cancelRun = useCallback(() => {
    if (stopping) return;
    setStopping(true);
    runAbortRef.current?.abort();
    void cancelNotebookRun(notebookId)
      .catch((error) => setActionError(String(error)))
      .finally(() => setStopping(false));
  }, [notebookId, stopping]);

  const allCellIds = useMemo(
    () => (notebook?.cells ?? []).map((cell) => cell.cell_id ?? "").filter(Boolean),
    [notebook?.cells],
  );

  // Seed from the server's current runtime. Live updates arrive through the
  // app shell's single workspace SSE connection.
  useEffect(() => {
    let cancelled = false;
    const runtimeEventAtRequest = notebookRuntimeEventRef.current;
    getNotebookRuntime(notebookId)
      .then((snapshot) => {
        if (!cancelled && notebookRuntimeEventRef.current === runtimeEventAtRequest) {
          applyRuntime(snapshot);
        }
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [notebookId, applyRuntime]);

  useEffect(() => {
    if (notebookRuntimeEvent?.notebook_id === notebookId) {
      applyRuntime(notebookRuntimeEvent);
    }
  }, [applyRuntime, notebookId, notebookRuntimeEvent]);

  // Each cell's last successful run columns, so a cell that reads from a sibling
  // gets that sibling's real output columns for intellisense and parse-context.
  const resultColumnsByCell = useMemo(() => {
    const map = new Map<string, string[]>();
    for (const [cellId, result] of Object.entries(results)) {
      if (result?.status === "ok" && result.columns.length > 0) {
        map.set(cellId, result.columns);
      }
    }
    return map;
  }, [results]);

  const controlDatasets = useMemo<AuthoredControlDataset[]>(
    () =>
      (notebook?.cells ?? []).map((cell) => {
        const result = cell.cell_id ? results[cell.cell_id] : undefined;
        const columns =
          result?.status === "ok" && result.columns.length > 0
            ? result.columns.map((name, index) => ({
                name,
                detail: result.column_types?.[index] || undefined,
              }))
            : (cell.columns ?? []).map((column) => ({
                name: column.name,
                detail: column.type || undefined,
              }));
        return {
          id: cell.cell_id || cell.name,
          label: cell.name,
          columns,
        };
      }),
    [notebook?.cells, results],
  );

  const refreshControlOptions = useCallback(
    async (control: NotebookParameter, options: { silent?: boolean } = {}) => {
      if (!control.options?.dataset?.trim() || !control.options.value_field?.trim()) return;

      const requestKey = `${notebookId}\u0000${control.id}`;
      const requestToken = ++controlOptionRequestSequenceRef.current;
      controlOptionRequestTokensRef.current.set(requestKey, requestToken);
      setLoadingControlOptions((current) => new Set(current).add(control.id));
      if (!options.silent) setActionError("");

      try {
        const result = await refreshNotebookControlOptions(notebookId, control.id);
        if (controlOptionRequestTokensRef.current.get(requestKey) !== requestToken) return;
        setControlOptionSnapshots((current) => ({
          ...current,
          [control.id]: {
            signature: notebookControlOptionSignature(control),
            result,
            refreshedAt: Date.now(),
          },
        }));
      } catch (error) {
        if (
          !options.silent &&
          controlOptionRequestTokensRef.current.get(requestKey) === requestToken
        ) {
          setActionError(String(error));
        }
      } finally {
        if (controlOptionRequestTokensRef.current.get(requestKey) === requestToken) {
          controlOptionRequestTokensRef.current.delete(requestKey);
          setLoadingControlOptions((current) => {
            const next = new Set(current);
            next.delete(control.id);
            return next;
          });
        }
      }
    },
    [notebookId],
  );

  // Runtime SSE messages contain result deltas. Refresh dataset-backed control
  // snapshots only when their producer publishes a new successful result; an
  // initial runtime read or a state-only event must never issue a query.
  useEffect(() => {
    const previous = controlOptionRuntimeEventRef.current;
    controlOptionRuntimeEventRef.current = notebookRuntimeEvent;
    if (!notebookRuntimeEvent || notebookRuntimeEvent.notebook_id !== notebookId) {
      return;
    }

    const changedSuccessfulCells = new Set<string>();
    for (const [cellID, result] of Object.entries(notebookRuntimeEvent.results ?? {})) {
      if (result.status === "ok" && previous?.results?.[cellID] !== result) {
        changedSuccessfulCells.add(cellID);
      }
    }
    if (changedSuccessfulCells.size === 0) return;

    for (const control of notebook?.parameters ?? []) {
      const producer = notebookControlProducer(control, notebook?.cells ?? []);
      if (producer?.cell_id && changedSuccessfulCells.has(producer.cell_id)) {
        void refreshControlOptions(control, { silent: true });
      }
    }
  }, [notebook, notebookId, notebookRuntimeEvent, refreshControlOptions]);

  const mutateWithResult = useCallback(async (operation: () => Promise<WebNotebook>) => {
    setActionError("");
    try {
      const updated = await operation();
      setMutated(updated);
      return updated;
    } catch (error) {
      setActionError(String(error));
      return null;
    }
  }, []);
  const mutate = useCallback(
    async (operation: () => Promise<WebNotebook>) => {
      await mutateWithResult(operation);
    },
    [mutateWithResult],
  );

  const selectVisualization = useCallback(
    (visualizationID: string) => {
      setSelectedVisualizationID(visualizationID);
      setSelectedControlID(null);
      if (!wideNotebookTools) setVisualizationInspectorOpen(true);
    },
    [wideNotebookTools],
  );

  const selectControl = useCallback(
    (controlID: string) => {
      setSelectedControlID(controlID);
      setSelectedVisualizationID(null);
      if (!wideNotebookTools) setVisualizationInspectorOpen(true);
    },
    [wideNotebookTools],
  );

  const confirmDeleteCell = useCallback(async () => {
    if (!cellToDelete || deletingCell) {
      return;
    }
    setDeletingCell(true);
    await mutateWithResult(() => deleteNotebookCell(notebookId, cellToDelete.id));
    setDeletingCell(false);
    setCellToDelete(null);
  }, [cellToDelete, deletingCell, mutateWithResult, notebookId]);

  const createNotebookBlock = useCallback(
    async (kind: PendingNotebookBlockKind, options: NotebookBlockCreateOptions = {}) => {
      if (!notebook || pendingBlock) {
        return;
      }

      const placement = options.placement ?? { position: "end" };
      const visualizationType = options.visualizationType ?? "table";
      const controlType = options.controlType ?? "text";
      const id = ++pendingBlockSequenceRef.current;
      const existingCellIDs = new Set(
        notebook.cells.map((cell) => cell.cell_id).filter((cellID): cellID is string => !!cellID),
      );
      const existingBlockIDs = new Set(notebook.blocks.map((block) => block.id).filter(Boolean));
      const existingControlIDs = new Set(
        notebook.blocks.map((block) => block.control).filter(Boolean),
      );
      setPendingBlock({ id, kind, placement });
      setEnteringBlockKey(null);
      if (placement.position === "end") setScrollRevision((current) => current + 1);

      const updated = await mutateWithResult(() => {
        if (kind === "markdown") {
          if (notebook.manifest_version < 2) {
            const next = [...notebook.blocks];
            const afterIndex = placement.after_block_id
              ? next.findIndex(
                  (candidate) => notebookBlockStableID(candidate) === placement.after_block_id,
                )
              : -1;
            const insertionIndex =
              placement.position === "start"
                ? 0
                : placement.position === "after" && afterIndex >= 0
                  ? afterIndex + 1
                  : next.length;
            next.splice(insertionIndex, 0, { markdown: "## Notes" });
            return updateNotebookBlocks(notebookId, next);
          }
          return createNotebookMarkdown(notebookId, {
            content: "## Notes",
            ...placement,
          });
        }
        if (kind === "visualization") {
          const insertionIndex =
            placement.position === "start"
              ? -1
              : placement.position === "end"
                ? notebook.blocks.length - 1
                : notebook.blocks.findIndex(
                    (candidate) => notebookBlockStableID(candidate) === placement.after_block_id,
                  );
          const sourceBlock =
            notebook.blocks
              .slice(0, insertionIndex + 1)
              .reverse()
              .find((block) => block.cell) ?? notebook.blocks.find((block) => block.cell);
          if (!sourceBlock?.cell) {
            throw new Error("Add a data-producing cell before creating a visualization.");
          }
          const sourceResult = results[sourceBlock.cell];
          const columns = (sourceResult?.columns ?? []).map((name, index) => ({
            name,
            physical_type: sourceResult?.column_types?.[index] ?? "",
            semantic_type: semanticTypeForPhysicalType(sourceResult?.column_types?.[index] ?? ""),
          }));
          const suggestion = visualizationSuggestionForType(columns, visualizationType);
          return createNotebookVisualization(notebookId, {
            source: sourceBlock.cell,
            definition: suggestion.definition,
            ...placement,
          });
        }
        if (kind === "control") {
          const existing = new Set((notebook.parameters ?? []).map((parameter) => parameter.id));
          let controlID = "control";
          let suffix = 2;
          while (existing.has(controlID)) {
            controlID = `control_${suffix}`;
            suffix += 1;
          }
          const parameter: NotebookParameter = {
            id: controlID,
            label: AUTHORED_CONTROL_TYPE_LABELS[controlType],
            type: controlType,
            default: defaultAuthoredControlValue(controlType),
            ...defaultAuthoredControlRange(controlType),
            options:
              controlType === "select" || controlType === "multi_select"
                ? { values: [] }
                : undefined,
          };
          return createNotebookControl(notebookId, parameter, placement);
        }
        return createNotebookCellAt(notebookId, {
          language: kind === "python" ? "python" : "sql",
          ...placement,
        });
      });

      if (updated) {
        if (kind === "markdown") {
          const createdIndex = updated.blocks.findIndex(
            (block) => block.id && !existingBlockIDs.has(block.id),
          );
          if (createdIndex >= 0) {
            setEnteringBlockKey(notebookBlockKey(updated.blocks[createdIndex], createdIndex));
          }
        } else if (kind === "visualization") {
          const createdBlock = updated.blocks.find(
            (block) => block.visualization && block.id && !existingBlockIDs.has(block.id),
          );
          if (createdBlock?.id) {
            setEnteringBlockKey(`block:${createdBlock.id}`);
            selectVisualization(createdBlock.id);
          }
        } else if (kind === "control") {
          const createdBlock = updated.blocks.find(
            (block) => block.control && !existingControlIDs.has(block.control),
          );
          if (createdBlock?.control) {
            setEnteringBlockKey(`control:${createdBlock.control}`);
            selectControl(createdBlock.control);
          }
        } else {
          const createdBlock = updated.blocks.find(
            (block) => block.cell && !existingCellIDs.has(block.cell),
          );
          if (createdBlock?.cell) {
            setEnteringBlockKey(`cell:${createdBlock.cell}`);
          }
        }
      }
      setPendingBlock((current) => (current?.id === id ? null : current));
      if (placement.position === "end") setScrollRevision((current) => current + 1);
    },
    [
      mutateWithResult,
      notebook,
      notebookId,
      pendingBlock,
      results,
      selectControl,
      selectVisualization,
    ],
  );

  const configureCellSource = useCallback(
    async (
      cellId: string,
      input: { connection?: string; snapshot_mode?: "full" | "sample"; row_limit?: number },
    ) => {
      await flushPendingSaves();
      await mutateWithResult(() => configureNotebookCellSource(notebookId, cellId, input));
    },
    [flushPendingSaves, mutateWithResult, notebookId],
  );

  const createWarehouseSource = useCallback(
    async (input: {
      connection: string;
      relation: string;
      snapshotMode: "full" | "sample";
      rowLimit?: number;
    }) => {
      if (!notebook) return;
      await flushPendingSaves();
      const existing = new Set(
        notebook.cells.map((cell) => cell.cell_id).filter((id): id is string => Boolean(id)),
      );
      const updated = await mutateWithResult(() =>
        createNotebookWarehouseSource(notebookId, {
          connection: input.connection,
          query: `select * from ${input.relation}\n`,
          snapshot_mode: input.snapshotMode,
          row_limit: input.rowLimit,
        }),
      );
      const created = updated?.cells.find((cell) => cell.cell_id && !existing.has(cell.cell_id));
      if (created?.cell_id) {
        setEnteringBlockKey(`cell:${created.cell_id}`);
        setScrollRevision((current) => current + 1);
      }
      setAddDataOpen(false);
    },
    [flushPendingSaves, mutateWithResult, notebook, notebookId],
  );

  const createDataSource = useCallback(
    async (
      input:
        | {
            kind: "warehouse";
            connection: string;
            relation: string;
            snapshotMode: "full" | "sample";
            rowLimit?: number;
          }
        | {
            kind: "file";
            connection?: string;
            uri: string;
            format?: string;
            snapshotMode: "full" | "sample";
            rowLimit?: number;
          }
        | {
            kind: "http";
            url: string;
            method: string;
            body?: unknown;
            recordsPath?: string;
            snapshotMode: "full" | "sample";
            rowLimit?: number;
          },
    ) => {
      if (input.kind === "warehouse") {
        await createWarehouseSource(input);
        return;
      }
      if (!notebook) return;
      await flushPendingSaves();
      const existing = new Set(
        notebook.cells.map((cell) => cell.cell_id).filter((id): id is string => Boolean(id)),
      );
      const updated = await mutateWithResult(() =>
        createNotebookSource(
          notebookId,
          input.kind === "file"
            ? {
                kind: "file",
                connection: input.connection,
                uri: input.uri,
                format: input.format,
                snapshot: { mode: input.snapshotMode, row_limit: input.rowLimit },
              }
            : {
                kind: "http",
                request: {
                  url: input.url,
                  method: input.method,
                  body: input.body,
                },
                response: { records_path: input.recordsPath },
                snapshot: { mode: input.snapshotMode, row_limit: input.rowLimit },
              },
        ),
      );
      const created = updated?.cells.find((cell) => cell.cell_id && !existing.has(cell.cell_id));
      if (created?.cell_id) {
        setEnteringBlockKey(`cell:${created.cell_id}`);
        setScrollRevision((current) => current + 1);
      }
      setAddDataOpen(false);
    },
    [createWarehouseSource, flushPendingSaves, mutateWithResult, notebook, notebookId],
  );

  const dependencies = useMemo(() => notebook?.dependencies ?? [], [notebook?.dependencies]);
  const installedModules = useMemo(
    () => notebook?.installed_modules ?? [],
    [notebook?.installed_modules],
  );
  const hasPythonCell = useMemo(
    () => (notebook?.cells ?? []).some((cell) => usesPythonSource(cell)),
    [notebook?.cells],
  );
  const updateDependencies = useCallback(
    (next: string[]) => mutate(() => updateNotebookDependencies(notebookId, next)),
    [mutate, notebookId],
  );
  const saveParameterDefinitions = useCallback(
    async (next: NonNullable<WebNotebook["parameters"]>) => {
      setActionError("");
      try {
        const updated = await replaceNotebookParameters(notebookId, next);
        setMutated(updated);
        setParameterValues(
          Object.fromEntries(
            (updated.parameters ?? []).map((parameter) => [parameter.id, parameter.default]),
          ),
        );
      } catch (error) {
        setActionError(String(error));
        throw error;
      }
    },
    [notebookId],
  );
  const saveParameterValues = useCallback(
    async (next: Record<string, unknown>) => {
      setActionError("");
      try {
        await setNotebookSettings(notebookId, {
          auto_recompute: autoRecompute,
          environment: selectedEnvironment,
          parameter_values: next,
        });
        setParameterValues(next);
      } catch (error) {
        setActionError(String(error));
        throw error;
      }
    },
    [autoRecompute, notebookId, selectedEnvironment],
  );
  const queueParameterValue = useCallback(
    (id: string, value: unknown) => {
      const next = { ...parameterValuesRef.current, [id]: value };
      parameterValuesRef.current = next;
      setParameterValues(next);
      if (parameterSaveTimerRef.current !== null) {
        window.clearTimeout(parameterSaveTimerRef.current);
      }
      parameterSaveTimerRef.current = window.setTimeout(() => {
        parameterSaveTimerRef.current = null;
        void saveParameterValues(next);
      }, 350);
    },
    [saveParameterValues],
  );

  // Ctrl+Click / F12 targets: pipeline assets open in the build page, sibling
  // cells scroll into view within this notebook.
  const goToAsset = useCallback(
    (pipelineId: string, assetId: string) => {
      void navigate({
        to: "/pipelines/$pipelineId/assets/$assetId/canvas",
        params: { pipelineId, assetId },
      });
    },
    [navigate],
  );
  const goToCell = useCallback((cellId: string) => {
    const target = document.querySelector<HTMLElement>(`[data-notebook-cell-id="${cellId}"]`);
    if (!target) {
      return;
    }

    const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    target.scrollIntoView({ behavior: reducedMotion ? "auto" : "smooth", block: "center" });

    // Toggle the attribute off for one frame so jumping to the same definition
    // twice restarts the animation instead of leaving an already-finished one.
    setJumpHighlightedCellId(null);
    if (jumpHighlightFrameRef.current !== null) {
      window.cancelAnimationFrame(jumpHighlightFrameRef.current);
    }
    if (jumpHighlightTimerRef.current !== null) {
      window.clearTimeout(jumpHighlightTimerRef.current);
    }
    jumpHighlightFrameRef.current = window.requestAnimationFrame(() => {
      setJumpHighlightedCellId(cellId);
      jumpHighlightFrameRef.current = null;
      jumpHighlightTimerRef.current = window.setTimeout(() => {
        setJumpHighlightedCellId(null);
        jumpHighlightTimerRef.current = null;
      }, NOTEBOOK_CELL_JUMP_HIGHLIGHT_MS);
    });
  }, []);
  const goToBlock = useCallback(
    (block: WebNotebookBlock) => {
      if (block.visualization && block.id) {
        selectVisualization(block.id);
      } else if (block.control) {
        selectControl(block.control);
      } else {
        setSelectedVisualizationID(null);
        setSelectedControlID(null);
        setVisualizationInspectorOpen(false);
      }
      if (block.cell) {
        goToCell(block.cell);
      } else if (block.id || block.control) {
        const selector = block.control
          ? `[data-notebook-control-id="${CSS.escape(block.control)}"]`
          : `[data-notebook-block-id="${CSS.escape(block.id ?? "")}"]`;
        document.querySelector<HTMLElement>(selector)?.scrollIntoView({
          behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches
            ? "auto"
            : "smooth",
          block: "center",
        });
      }
      if (!wideNotebookTools) setToolsOpen(false);
    },
    [goToCell, selectControl, selectVisualization, wideNotebookTools],
  );

  const pipelines = workspace?.pipelines ?? [];
  const promoteCell = useCallback(
    (cell: WebAsset) => {
      if (pipelines.length === 0) {
        setActionError("No pipeline to promote into; create one first.");
        return;
      }
      setActionError("");
      setPromoting(cell);
    },
    [pipelines],
  );

  const runPromote = useCallback(
    async (
      cell: WebAsset,
      input: {
        pipeline_id: string;
        target_name: string;
        include_upstream: boolean;
        include_downstream: boolean;
        base_revision: string;
      },
    ) => {
      setActionError("");
      try {
        const response = await promoteNotebookCell(notebookId, cell.cell_id ?? "", input);
        setMutated(response.notebook);
        setPromoting(null);
        if (response.dialect_warning) {
          const where =
            response.promoted_count > 1 ? `${response.promoted_count} assets` : response.asset_path;
          setActionError(`Promoted ${where}. ${response.dialect_warning}`);
        }
      } catch (error) {
        setActionError(String(error));
      }
    },
    [notebookId],
  );

  if (!notebook) {
    return (
      <AppPage>
        <div className="flex h-full items-center justify-center text-sm text-muted-foreground">
          {loadError ? `Failed to load notebook: ${loadError}` : "Loading notebook..."}
        </div>
      </AppPage>
    );
  }

  // Only cells the user must act on: those auto-recompute won't refresh on its
  // own. When auto-recompute is off, the server reports no auto-pending cells,
  // so this is every stale cell.
  const manualStaleCells = [...staleCells].filter((id) => !autoPending.has(id));
  const staleCount = manualStaleCells.length;
  const placedControlIDs = new Set(
    notebook.blocks.map((block) => block.control).filter((id): id is string => Boolean(id)),
  );
  const unplacedControls = (notebook.parameters ?? []).filter(
    (parameter) => !placedControlIDs.has(parameter.id),
  );
  const renderNotebookControl = (control: NotebookParameter, entering = false) => {
    const snapshot = controlOptionSnapshots[control.id];
    const optionResult =
      snapshot?.signature === notebookControlOptionSignature(control) ? snapshot.result : undefined;
    const producer = notebookControlProducer(control, notebook.cells);
    const producerResult = producer?.cell_id ? results[producer.cell_id] : undefined;
    const optionsStale = Boolean(
      !producer?.cell_id || staleCells.has(producer.cell_id) || producerResult?.status !== "ok",
    );

    return (
      <div
        key={`control:${control.id}`}
        data-notebook-control-id={control.id}
        data-notebook-block-entering={entering || undefined}
        className={cn(entering && NOTEBOOK_BLOCK_ENTER_ANIMATION)}
      >
        <NotebookControlBlock
          control={control}
          value={parameterValues[control.id] ?? control.default}
          busy={busy}
          datasets={controlDatasets}
          optionResult={optionResult}
          optionsLoading={loadingControlOptions.has(control.id)}
          optionsStale={optionsStale}
          selected={selectedControlID === control.id}
          inspectorTarget={visualizationInspectorTarget}
          onSelect={() => selectControl(control.id)}
          onCloseInspector={() => {
            setSelectedControlID(null);
            setVisualizationInspectorOpen(false);
          }}
          onValueChange={(value) => queueParameterValue(control.id, value)}
          onRefreshOptions={() => void refreshControlOptions(control)}
          onSave={async (nextControl) => {
            const updated = await mutateWithResult(() =>
              updateNotebookControl(notebookId, control.id, nextControl),
            );
            if (!updated) return false;
            if (nextControl.id !== control.id) {
              setParameterValues((current) => {
                const next = { ...current };
                delete next[control.id];
                next[nextControl.id] = nextControl.default;
                parameterValuesRef.current = next;
                return next;
              });
              setSelectedControlID(nextControl.id);
            }
            return true;
          }}
          onDelete={async () => {
            await mutateWithResult(() => deleteNotebookControl(notebookId, control.id));
            setSelectedControlID(null);
            setVisualizationInspectorOpen(false);
          }}
        />
      </div>
    );
  };
  const agentRunning =
    notebookAgentEvent?.status === "running" || notebookAgentEvent?.status === "cancelling";
  const renderNotebookTools = (onClose?: () => void) => (
    <NotebookAuthoringSidebar
      notebook={notebook}
      notebookId={notebookId}
      results={results}
      activeTab={toolsTab}
      agentRunning={agentRunning}
      addingBlock={pendingBlock !== null}
      onTabChange={setToolsTab}
      onSelectBlock={goToBlock}
      onAddData={() => {
        setAddDataOpen(true);
        onClose?.();
      }}
      onManageControls={() => {
        setParametersOpen(true);
        onClose?.();
      }}
      onAddBlock={(kind, options) => {
        void createNotebookBlock(kind, options);
        onClose?.();
      }}
      onClose={onClose}
    />
  );

  return (
    <AppPage>
      <div
        className={cn("relative z-10 shrink-0 transition-shadow", notebookScrolled && "shadow-sm")}
      >
        <PageHeader
          title={notebook.title}
          subtitle={`Notebook · ${notebook.path} · runs in a local DuckDB session`}
          actions={
            <div className="flex items-center gap-2">
              {staleCount > 0 ? (
                <div className="flex items-center gap-1">
                  <Badge
                    variant="outline"
                    className="border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-200"
                  >
                    <AlertTriangle className="size-3" />
                    {staleCount} stale
                  </Badge>
                  <Button
                    size="sm"
                    variant="outline"
                    aria-label="Recompute"
                    disabled={busy}
                    onClick={() => void runRequest({ cells: manualStaleCells }, manualStaleCells)}
                  >
                    <RotateCw className="size-3.5" />
                    <span className="hidden sm:inline">Recompute</span>
                  </Button>
                </div>
              ) : null}
              <Button
                variant="outline"
                size="sm"
                className="xl:hidden"
                aria-label="Notebook tools"
                onClick={() => setToolsOpen(true)}
              >
                <PanelLeft data-icon="inline-start" />
                <span className="hidden sm:inline">Tools</span>
                {agentRunning ? (
                  <span
                    aria-label="Agent is working"
                    className="size-1.5 rounded-full bg-primary motion-safe:animate-pulse"
                  />
                ) : null}
              </Button>
              {hasPythonCell ? (
                <Button
                  variant="outline"
                  size="sm"
                  aria-label="Dependencies"
                  onClick={() => setDepsOpen(true)}
                >
                  <Package className="size-3.5" />
                  <span className="hidden sm:inline">Dependencies</span>
                </Button>
              ) : null}
              {busy || runningCells.size > 0 ? (
                <Button size="sm" variant="outline" disabled={stopping} onClick={cancelRun}>
                  {stopping ? (
                    <Loader2 className="size-3.5 animate-spin" />
                  ) : (
                    <Square className="size-3.5 fill-current" />
                  )}
                  {stopping ? "Stopping…" : "Stop"}
                </Button>
              ) : (
                <Button
                  size="sm"
                  disabled={allCellIds.length === 0}
                  onClick={() => void runRequest({ all: true }, allCellIds)}
                >
                  <Play className="size-3.5" />
                  Run all
                </Button>
              )}
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="outline" size="icon-sm">
                    <MoreHorizontal className="size-3.5" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-64">
                  <DropdownMenuGroup>
                    <DropdownMenuCheckboxItem
                      checked={autoRecompute}
                      onCheckedChange={(checked) => setAutoRecompute(checked === true)}
                      onSelect={(event) => event.preventDefault()}
                    >
                      Auto-recompute stale cells
                    </DropdownMenuCheckboxItem>
                  </DropdownMenuGroup>
                  <DropdownMenuSeparator />
                  <DropdownMenuGroup>
                    <DropdownMenuItem
                      disabled={busy}
                      onSelect={() =>
                        void runRequest({ all: true, refresh_imports: true }, allCellIds)
                      }
                    >
                      <RotateCw />
                      Refresh sources and run all
                    </DropdownMenuItem>
                    <DropdownMenuItem
                      onSelect={() => {
                        void closeNotebookSession(notebookId)
                          .then(() => {
                            setResults({});
                            setStaleCells(new Set(allCellIds));
                          })
                          .catch((error) => setActionError(String(error)));
                      }}
                    >
                      <Database />
                      Reset session (delete local DB)
                    </DropdownMenuItem>
                    {notebook.manifest_version < 2 ? (
                      <DropdownMenuItem
                        onSelect={() =>
                          void mutate(() => upgradeNotebookManifest(notebookId, notebook.revision))
                        }
                      >
                        <ArrowUpFromLine />
                        Upgrade notebook format
                      </DropdownMenuItem>
                    ) : null}
                  </DropdownMenuGroup>
                  <DropdownMenuSeparator />
                  <DropdownMenuGroup>
                    <DropdownMenuItem
                      variant="destructive"
                      onSelect={() => {
                        if (!window.confirm(`Delete notebook "${notebook.title}" and its files?`)) {
                          return;
                        }
                        void deleteNotebook(notebookId)
                          .then(() => navigate({ to: "/" }))
                          .catch((error) => setActionError(String(error)));
                      }}
                    >
                      <Trash2 />
                      Delete notebook
                    </DropdownMenuItem>
                  </DropdownMenuGroup>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          }
        />
      </div>

      <NotebookDependenciesDialog
        open={depsOpen}
        onOpenChange={setDepsOpen}
        dependencies={dependencies}
        onSave={updateDependencies}
      />

      <NotebookParametersDialog
        open={parametersOpen}
        onOpenChange={setParametersOpen}
        parameters={notebook.parameters ?? []}
        values={parameterValues}
        onSaveDefinitions={saveParameterDefinitions}
        onSaveValues={saveParameterValues}
      />

      <NotebookAddDataDialog
        open={addDataOpen}
        onOpenChange={setAddDataOpen}
        queryConnections={workspace?.query_connections ?? []}
        connections={workspace?.connections ?? {}}
        environment={selectedEnvironment ?? ""}
        onCreate={createDataSource}
      />

      <PromoteCellDialog
        cell={promoting}
        cells={notebook.cells}
        notebookId={notebookId}
        notebookRevision={notebook.revision}
        pipelines={pipelines.map((pipeline) => ({ id: pipeline.id, name: pipeline.name }))}
        onOpenChange={(open) => {
          if (!open) {
            setPromoting(null);
          }
        }}
        onPromote={runPromote}
      />

      <DeleteNotebookCellDialog
        cell={cellToDelete}
        deleting={deletingCell}
        onOpenChange={(open) => {
          if (!open && !deletingCell) {
            setCellToDelete(null);
          }
        }}
        onConfirm={() => void confirmDeleteCell()}
      />

      {notebook.problems?.length ? (
        <div className="mx-3 mb-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-800 dark:border-red-500/25 dark:bg-red-500/10 dark:text-red-200">
          {notebook.problems.map((problem) => (
            <div key={problem}>{problem}</div>
          ))}
        </div>
      ) : null}
      {actionError ? (
        <div className="mx-3 mb-2 rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-800 dark:border-red-500/25 dark:bg-red-500/10 dark:text-red-200">
          {actionError}
        </div>
      ) : null}
      <div className="flex min-h-0 min-w-0 flex-1">
        {wideNotebookTools ? (
          <aside className="w-[clamp(18rem,23vw,27rem)] min-w-0 shrink-0 border-r bg-background">
            {renderNotebookTools()}
          </aside>
        ) : null}
        <ScrollArea
          data-testid="notebook-scroll-area"
          className="min-h-0 min-w-0 flex-1"
          viewportClassName="px-3 pb-24"
          viewportRef={notebookViewportRef}
          onViewportScroll={(event) => {
            const nextScrolled = event.currentTarget.scrollTop > 0;
            setNotebookScrolled((current) => (current === nextScrolled ? current : nextScrolled));
          }}
        >
          <div
            data-testid="notebook-canvas"
            className="mx-auto flex min-h-full max-w-5xl flex-col rounded-xl"
          >
            {unplacedControls.map((control) => renderNotebookControl(control))}
            <NotebookInsertionPoint
              placement={{ position: "start" }}
              disabled={pendingBlock !== null}
              pendingKind={
                pendingBlock &&
                notebookPlacementKey(pendingBlock.placement) ===
                  notebookPlacementKey({ position: "start" })
                  ? pendingBlock.kind
                  : undefined
              }
              onInsert={(kind, options) =>
                void createNotebookBlock(kind, {
                  ...options,
                  placement: { position: "start" },
                })
              }
            />
            {notebook.blocks.map((block, index) => {
              const blockKey = notebookBlockKey(block, index);
              const entering = blockKey === enteringBlockKey;
              const renderedBlock = block.cell ? (
                (() => {
                  const cell = cellsById.get(block.cell);
                  if (!cell) {
                    return null;
                  }
                  return (
                    <div
                      key={block.cell}
                      data-notebook-cell-id={block.cell}
                      data-notebook-block-entering={entering || undefined}
                      data-notebook-cell-jump-highlight={
                        jumpHighlightedCellId === block.cell || undefined
                      }
                      className={cn(entering && NOTEBOOK_BLOCK_ENTER_ANIMATION)}
                    >
                      {cell.notebook_source ? (
                        <NotebookSourceCard
                          notebookId={notebookId}
                          cell={cell}
                          queryConnections={workspace?.query_connections ?? []}
                          result={results[block.cell]}
                          stale={staleCells.has(block.cell)}
                          running={runningCells.has(block.cell)}
                          busy={busy}
                          onRun={() =>
                            void runRequest({ cells: [block.cell ?? ""] }, [block.cell ?? ""])
                          }
                          onCancel={cancelRun}
                          onRunFromHere={() =>
                            void runRequest({ from: block.cell }, [block.cell ?? ""])
                          }
                          onDelete={() =>
                            setCellToDelete({ id: block.cell ?? "", name: cell.name })
                          }
                          onRename={(name) =>
                            mutate(() => renameNotebookCell(notebookId, block.cell ?? "", name))
                          }
                          onPromote={() => void promoteCell(cell)}
                        />
                      ) : (
                        <NotebookCellCard
                          notebookId={notebookId}
                          cell={cell}
                          cells={notebook.cells}
                          dependencies={dependencies}
                          installedModules={installedModules}
                          parameters={notebook.parameters ?? []}
                          onAddDependency={(pkg) =>
                            updateDependencies(addDependency(dependencies, pkg))
                          }
                          resultColumnsByCell={resultColumnsByCell}
                          result={results[block.cell]}
                          stale={staleCells.has(block.cell)}
                          running={runningCells.has(block.cell)}
                          busy={busy}
                          onRun={() =>
                            void runRequest({ cells: [block.cell ?? ""] }, [block.cell ?? ""])
                          }
                          onCancel={cancelRun}
                          onRunFromHere={() =>
                            void runRequest({ from: block.cell }, [block.cell ?? ""])
                          }
                          onDelete={() =>
                            setCellToDelete({ id: block.cell ?? "", name: cell.name })
                          }
                          onRename={(name) =>
                            mutate(() => renameNotebookCell(notebookId, block.cell ?? "", name))
                          }
                          onPromote={() => void promoteCell(cell)}
                          onSaveBody={(body, baseRevision) =>
                            saveCellBody(cell, body, baseRevision)
                          }
                          autoCommit={autoRecompute}
                          pendingAuto={autoPending.has(block.cell ?? "")}
                          queryConnections={workspace?.query_connections ?? []}
                          onConfigureSource={(input) =>
                            configureCellSource(block.cell ?? "", input)
                          }
                          onMigrateLegacyViz={async () => {
                            await flushPendingSaves();
                            const existingBlockIDs = new Set(
                              notebook.blocks.map((candidate) => candidate.id).filter(Boolean),
                            );
                            const updated = await mutateWithResult(() =>
                              migrateLegacyNotebookVisualization(notebookId, block.cell ?? ""),
                            );
                            const migrated = updated?.blocks.find(
                              (candidate) =>
                                candidate.visualization &&
                                candidate.id &&
                                !existingBlockIDs.has(candidate.id),
                            );
                            if (migrated?.id) {
                              setEnteringBlockKey(`block:${migrated.id}`);
                              selectVisualization(migrated.id);
                            }
                          }}
                          onGoToAsset={goToAsset}
                          onGoToCell={goToCell}
                        />
                      )}
                    </div>
                  );
                })()
              ) : block.control ? (
                (() => {
                  const control = notebook.parameters?.find(
                    (parameter) => parameter.id === block.control,
                  );
                  if (!control) return null;
                  return renderNotebookControl(control, entering);
                })()
              ) : block.visualization && block.id ? (
                <div
                  key={block.id}
                  data-notebook-block-id={block.id}
                  data-notebook-visualization-id={block.id}
                  data-notebook-block-entering={entering || undefined}
                  className={cn(entering && NOTEBOOK_BLOCK_ENTER_ANIMATION)}
                >
                  <NotebookVisualizationBlockCard
                    notebookId={notebookId}
                    blockId={block.id}
                    visualization={block.visualization}
                    cells={notebook.cells}
                    results={results}
                    busy={busy}
                    selected={selectedVisualizationID === block.id}
                    inspectorTarget={visualizationInspectorTarget}
                    onSelect={() => selectVisualization(block.id ?? "")}
                    onCloseInspector={() => {
                      setSelectedVisualizationID(null);
                      setVisualizationInspectorOpen(false);
                    }}
                    onSave={async (source, definition) => {
                      await flushPendingSaves();
                      const updated = await mutateWithResult(() =>
                        updateNotebookVisualization(notebookId, block.id ?? "", {
                          source,
                          definition,
                        }),
                      );
                      return Boolean(updated);
                    }}
                    onDelete={async () => {
                      await flushPendingSaves();
                      await mutateWithResult(() => deleteNotebookBlock(notebookId, block.id ?? ""));
                      if (selectedVisualizationID === block.id) {
                        setSelectedVisualizationID(null);
                        setVisualizationInspectorOpen(false);
                      }
                    }}
                  />
                </div>
              ) : (
                <div
                  key={block.id ?? `legacy-md-${index}`}
                  data-notebook-block-id={block.id || undefined}
                  data-notebook-markdown-index={index}
                  data-notebook-block-entering={entering || undefined}
                  className={cn(entering && NOTEBOOK_BLOCK_ENTER_ANIMATION)}
                >
                  <MarkdownBlockCard
                    markdown={block.markdown ?? ""}
                    onSave={async (markdown) => {
                      if (!block.id) {
                        const blocks: WebNotebookBlock[] = notebook.blocks.map(
                          (candidate, candidateIndex) =>
                            candidateIndex === index ? { ...candidate, markdown } : candidate,
                        );
                        return Boolean(
                          await mutateWithResult(() => updateNotebookBlocks(notebookId, blocks)),
                        );
                      }
                      return Boolean(
                        await mutateWithResult(() =>
                          updateNotebookMarkdown(notebookId, block.id!, markdown),
                        ),
                      );
                    }}
                    onDelete={() => {
                      if (!block.id) {
                        const blocks = notebook.blocks.filter(
                          (_, candidateIndex) => candidateIndex !== index,
                        );
                        void mutate(() => updateNotebookBlocks(notebookId, blocks));
                        return;
                      }
                      void mutate(() => deleteNotebookBlock(notebookId, block.id!));
                    }}
                  />
                </div>
              );
              const stableID = notebookBlockStableID(block);
              const placement: NotebookBlockPlacement = stableID
                ? { position: "after", after_block_id: stableID }
                : { position: "end" };
              return (
                <Fragment key={blockKey}>
                  {renderedBlock}
                  {stableID ? (
                    <NotebookInsertionPoint
                      placement={placement}
                      disabled={pendingBlock !== null}
                      pendingKind={
                        pendingBlock &&
                        notebookPlacementKey(pendingBlock.placement) ===
                          notebookPlacementKey(placement)
                          ? pendingBlock.kind
                          : undefined
                      }
                      onInsert={(kind, options) =>
                        void createNotebookBlock(kind, { ...options, placement })
                      }
                    />
                  ) : null}
                </Fragment>
              );
            })}
            {pendingBlock?.placement.position === "end" ? (
              <div className="py-1.5">
                <PendingNotebookBlock kind={pendingBlock.kind} />
              </div>
            ) : null}
          </div>
        </ScrollArea>
        {wideNotebookTools && (selectedVisualizationID || selectedControlID) ? (
          <aside className="w-[clamp(20rem,24vw,26rem)] min-w-0 shrink-0 overflow-hidden border-l bg-background">
            <ScrollArea className="h-full">
              <div ref={setVisualizationInspectorTarget} />
            </ScrollArea>
          </aside>
        ) : null}
      </div>
      {!wideNotebookTools ? (
        <>
          <Sheet open={toolsOpen} onOpenChange={setToolsOpen}>
            <SheetContent side="left" className="w-[min(28rem,94vw)] max-w-full p-0">
              <SheetHeader className="sr-only">
                <SheetTitle>Notebook tools</SheetTitle>
                <SheetDescription>Outline, data, blocks, and notebook assistant.</SheetDescription>
              </SheetHeader>
              <div className="min-h-0 flex-1">
                {toolsOpen ? renderNotebookTools(() => setToolsOpen(false)) : null}
              </div>
            </SheetContent>
          </Sheet>
          <Sheet
            open={
              Boolean(selectedVisualizationID || selectedControlID) && visualizationInspectorOpen
            }
            onOpenChange={setVisualizationInspectorOpen}
          >
            <SheetContent
              side="right"
              showCloseButton={false}
              className="w-[min(26rem,94vw)] max-w-full overflow-hidden p-0"
            >
              <SheetHeader className="sr-only">
                <SheetTitle>Block inspector</SheetTitle>
                <SheetDescription>Configure the selected notebook block.</SheetDescription>
              </SheetHeader>
              <ScrollArea className="min-h-0 flex-1">
                <div ref={setVisualizationInspectorTarget} />
              </ScrollArea>
            </SheetContent>
          </Sheet>
        </>
      ) : null}
    </AppPage>
  );
}

function NotebookAuthoringSidebar({
  notebook,
  notebookId,
  results,
  activeTab,
  agentRunning,
  addingBlock,
  onTabChange,
  onSelectBlock,
  onAddData,
  onManageControls,
  onAddBlock,
  onClose,
}: {
  notebook: WebNotebook;
  notebookId: string;
  results: Record<string, NotebookCellRunResult>;
  activeTab: string;
  agentRunning: boolean;
  addingBlock: boolean;
  onTabChange: (tab: string) => void;
  onSelectBlock: (block: WebNotebookBlock) => void;
  onAddData: () => void;
  onManageControls: () => void;
  onAddBlock: (kind: PendingNotebookBlockKind, options?: NotebookBlockCreateOptions) => void;
  onClose?: () => void;
}) {
  const cells = new Map(
    notebook.cells
      .filter((cell): cell is WebAsset & { cell_id: string } => Boolean(cell.cell_id))
      .map((cell) => [cell.cell_id, cell]),
  );
  const tabs: DocumentAuthoringTab[] = [
    {
      value: "outline",
      label: "Outline",
      content: (
        <NotebookOutline
          blocks={notebook.blocks}
          cells={cells}
          parameters={notebook.parameters ?? []}
          onSelect={onSelectBlock}
        />
      ),
    },
    {
      value: "data",
      label: "Data",
      content: (
        <NotebookDataList
          cells={notebook.cells}
          results={results}
          onAddData={onAddData}
          onSelect={(cell) => {
            const block = notebook.blocks.find((candidate) => candidate.cell === cell.cell_id);
            if (block) onSelectBlock(block);
          }}
        />
      ),
    },
    {
      value: "add",
      label: "Add",
      content: (
        <NotebookAddPalette
          canVisualize={notebook.manifest_version >= 2 && notebook.cells.length > 0}
          canAddControls={notebook.manifest_version >= 2}
          disabled={addingBlock}
          onManageControls={onManageControls}
          onAdd={onAddBlock}
        />
      ),
    },
    {
      value: "ai",
      label: (
        <span className="flex min-w-0 items-center gap-1.5">
          AI
          {agentRunning ? (
            <span
              aria-label="Agent is working"
              className="size-1.5 rounded-full bg-primary motion-safe:animate-pulse"
            />
          ) : null}
        </span>
      ),
      content: <NotebookAgentChat notebookId={notebookId} onClose={onClose} />,
      scroll: false,
    },
  ];

  return (
    <DocumentAuthoringSidebar
      label="Notebook authoring tools"
      tabs={tabs}
      value={activeTab}
      defaultValue="outline"
      onValueChange={onTabChange}
    />
  );
}

function NotebookOutline({
  blocks,
  cells,
  parameters,
  onSelect,
}: {
  blocks: WebNotebookBlock[];
  cells: Map<string, WebAsset>;
  parameters: NotebookParameter[];
  onSelect: (block: WebNotebookBlock) => void;
}) {
  const placedControlIDs = new Set(
    blocks.map((block) => block.control).filter((id): id is string => Boolean(id)),
  );
  const outlineBlocks: WebNotebookBlock[] = [
    ...parameters
      .filter((parameter) => !placedControlIDs.has(parameter.id))
      .map((parameter) => ({ control: parameter.id })),
    ...blocks,
  ];
  return (
    <div className="flex flex-col gap-1 p-2">
      {outlineBlocks.map((block, index) => {
        const cell = block.cell ? cells.get(block.cell) : undefined;
        const type = cell
          ? cell.notebook_source
            ? "Source"
            : usesPythonSource(cell)
              ? "Python"
              : "SQL"
          : block.visualization
            ? "Chart"
            : block.control
              ? "Control"
              : "Text";
        const title = block.control
          ? (parameters.find((parameter) => parameter.id === block.control)?.label ?? block.control)
          : notebookBlockTitle(block, cell, index);
        return (
          <button
            key={block.id || block.cell || `block-${index}`}
            type="button"
            className="flex min-w-0 items-center gap-2 rounded-md px-2 py-2 text-left transition-colors hover:bg-accent"
            onClick={() => onSelect(block)}
          >
            <ListTree className="size-3.5 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate text-xs font-medium">{title}</span>
            <span className="shrink-0 text-[9px] font-medium uppercase tracking-wide text-muted-foreground">
              {type}
            </span>
          </button>
        );
      })}
    </div>
  );
}

function NotebookDataList({
  cells,
  results,
  onAddData,
  onSelect,
}: {
  cells: WebAsset[];
  results: Record<string, NotebookCellRunResult>;
  onAddData: () => void;
  onSelect: (cell: WebAsset) => void;
}) {
  return (
    <div className="flex flex-col gap-2 p-2">
      <Button size="sm" variant="outline" className="w-full" onClick={onAddData}>
        <Plus /> Add data
      </Button>
      {cells.map((cell) => {
        const result = cell.cell_id ? results[cell.cell_id] : undefined;
        const source = cell.notebook_source;
        return (
          <button
            key={cell.cell_id || cell.name}
            type="button"
            className="flex min-w-0 flex-col gap-1.5 rounded-lg border bg-card p-2.5 text-left transition-colors hover:bg-accent"
            onClick={() => onSelect(cell)}
          >
            <span className="flex min-w-0 items-center gap-2">
              <Database className="size-3.5 shrink-0 text-primary" />
              <span className="min-w-0 flex-1 truncate text-xs font-medium">{cell.name}</span>
              {result?.status === "ok" ? (
                <Badge variant="outline" className="shrink-0 font-normal">
                  {result.total_rows} rows
                </Badge>
              ) : null}
            </span>
            <span className="truncate text-[10px] text-muted-foreground">
              {source?.connection || (usesPythonSource(cell) ? "Python output" : "Notebook DuckDB")}
            </span>
            {result?.columns.length ? (
              <span className="flex min-w-0 flex-wrap gap-1">
                {result.columns.slice(0, 5).map((column) => (
                  <span
                    key={column}
                    className="max-w-full truncate rounded bg-muted px-1.5 py-0.5 font-mono text-[9px] text-muted-foreground"
                  >
                    {column}
                  </span>
                ))}
              </span>
            ) : null}
          </button>
        );
      })}
    </div>
  );
}

function NotebookAddPalette({
  canVisualize,
  canAddControls,
  disabled,
  onManageControls,
  onAdd,
}: {
  canVisualize: boolean;
  canAddControls: boolean;
  disabled: boolean;
  onManageControls: () => void;
  onAdd: (kind: PendingNotebookBlockKind, options?: NotebookBlockCreateOptions) => void;
}) {
  return (
    <div className="flex flex-col gap-3 p-2">
      <NotebookBlockTypePicker
        draggable
        disabled={disabled}
        onValueChange={(type) => onAdd(type)}
      />
      <div className="flex flex-col gap-2">
        <p className="px-1 text-[11px] font-medium text-muted-foreground">Visualizations</p>
        <ChartTypePicker
          compact
          draggable
          disabled={disabled || !canVisualize}
          onValueChange={(type) => onAdd("visualization", { visualizationType: type })}
        />
        <p className="px-1 text-[10px] leading-relaxed text-muted-foreground">
          Drag a preview onto the notebook, or click it to add after the latest data result.
        </p>
      </div>
      <div className="flex flex-col gap-2">
        <div className="flex items-center justify-between px-1">
          <p className="text-[11px] font-medium text-muted-foreground">Controls</p>
          <Button size="xs" variant="ghost" aria-label="Manage controls" onClick={onManageControls}>
            Manage
          </Button>
        </div>
        <ControlTypePicker
          draggable
          disabled={disabled || !canAddControls}
          onValueChange={(controlType) => onAdd("control", { controlType })}
        />
        <p className="px-1 text-[10px] leading-relaxed text-muted-foreground">
          Drag a typed input between notebook blocks, or click to append it.
        </p>
      </div>
    </div>
  );
}

function NotebookInsertionPoint({
  placement,
  disabled,
  pendingKind,
  onInsert,
}: {
  placement: NotebookBlockPlacement;
  disabled: boolean;
  pendingKind?: PendingNotebookBlockKind;
  onInsert: (kind: PendingNotebookBlockKind, options?: NotebookBlockCreateOptions) => void;
}) {
  const [dropActive, setDropActive] = useState(false);
  if (pendingKind) {
    return (
      <div className="py-1.5">
        <PendingNotebookBlock kind={pendingKind} />
      </div>
    );
  }
  return (
    <div
      data-notebook-insertion-point={notebookPlacementKey(placement)}
      className={cn(
        "group/notebook-insert relative flex h-8 items-center justify-center transition-colors",
        dropActive && "h-12 rounded-lg bg-primary/5 ring-1 ring-primary/25",
      )}
      onDragEnter={(event) => {
        if (disabled || !hasAuthoringDragItem(event)) return;
        event.preventDefault();
        event.stopPropagation();
        setDropActive(true);
      }}
      onDragOver={(event) => {
        if (disabled || !hasAuthoringDragItem(event)) return;
        event.preventDefault();
        event.stopPropagation();
        event.dataTransfer.dropEffect = "copy";
      }}
      onDragLeave={(event) => {
        if (event.currentTarget.contains(event.relatedTarget as Node | null)) return;
        setDropActive(false);
      }}
      onDrop={(event) => {
        const item = readAuthoringDragItem(event);
        if (disabled || !item) return;
        event.preventDefault();
        event.stopPropagation();
        setDropActive(false);
        if (item.kind === "visualization") {
          onInsert("visualization", { visualizationType: item.chartType });
        } else if (item.kind === "control") {
          onInsert("control", { controlType: item.controlType });
        } else {
          onInsert(item.blockType);
        }
      }}
    >
      <div
        className={cn(
          "absolute inset-x-2 top-1/2 h-px bg-border opacity-0 transition-opacity group-hover/notebook-insert:opacity-100 group-focus-within/notebook-insert:opacity-100",
          dropActive && "opacity-100",
        )}
      />
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            type="button"
            size="icon-xs"
            variant="outline"
            disabled={disabled}
            aria-label="Insert notebook block here"
            className={cn(
              "relative z-10 rounded-full bg-background opacity-0 shadow-xs transition-opacity group-hover/notebook-insert:opacity-100 group-focus-within/notebook-insert:opacity-100",
              dropActive && "opacity-100",
            )}
          >
            <Plus />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="center" className="w-44">
          <DropdownMenuItem onSelect={() => onInsert("sql")}>
            <Database /> SQL cell
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => onInsert("python")}>
            <FileInput /> Python cell
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => onInsert("markdown")}>
            <BookOpen /> Text
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuSub>
            <DropdownMenuSubTrigger>
              <SlidersHorizontal /> Control
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent className="w-40">
              {AUTHORED_CONTROL_TYPES.map((type) => (
                <DropdownMenuItem
                  key={type}
                  onSelect={() => onInsert("control", { controlType: type })}
                >
                  {AUTHORED_CONTROL_TYPE_LABELS[type]}
                </DropdownMenuItem>
              ))}
            </DropdownMenuSubContent>
          </DropdownMenuSub>
          <DropdownMenuSub>
            <DropdownMenuSubTrigger>
              <SlidersHorizontal /> Visualization
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent className="w-40">
              {CHART_TYPE_OPTIONS.map((option) => (
                <DropdownMenuItem
                  key={option.value}
                  onSelect={() => onInsert("visualization", { visualizationType: option.value })}
                >
                  {option.label}
                </DropdownMenuItem>
              ))}
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

function notebookBlockTitle(block: WebNotebookBlock, cell: WebAsset | undefined, index: number) {
  if (cell) return cell.name;
  if (block.control) return block.control;
  if (block.visualization) {
    const title = block.visualization.definition?.title;
    return typeof title === "string" && title.trim() ? title : "Visualization";
  }
  const firstLine = block.markdown
    ?.split("\n", 1)[0]
    ?.replace(/^#+\s*/, "")
    .trim();
  return firstLine || `Text ${index + 1}`;
}

function PendingNotebookBlock({ kind }: { kind: PendingNotebookBlockKind }) {
  const label =
    kind === "markdown"
      ? "Markdown block"
      : kind === "visualization"
        ? "visualization"
        : kind === "control"
          ? "control"
          : `${kind.toUpperCase()} cell`;

  return (
    <div
      role="status"
      aria-live="polite"
      aria-label={`Adding ${label}`}
      data-notebook-block-pending={kind}
      className={NOTEBOOK_BLOCK_ENTER_ANIMATION}
    >
      <AppPanel>
        <DelimitedCardHeader>
          <Spinner
            role="presentation"
            aria-hidden="true"
            className="size-3.5 text-muted-foreground"
          />
          <DelimitedCardTitle>Adding {label}…</DelimitedCardTitle>
          <Badge variant="secondary" className="font-mono text-[10px]">
            {kind}
          </Badge>
        </DelimitedCardHeader>
        <DelimitedCardContent>
          <div className="space-y-2 rounded-lg border p-3">
            <Skeleton className="h-3 w-2/5" />
            <Skeleton className="h-3 w-3/4" />
            <Skeleton className="h-3 w-1/2" />
          </div>
        </DelimitedCardContent>
      </AppPanel>
    </div>
  );
}

type NotebookDataSourceInput =
  | {
      kind: "warehouse";
      connection: string;
      relation: string;
      snapshotMode: "full" | "sample";
      rowLimit?: number;
    }
  | {
      kind: "file";
      connection?: string;
      uri: string;
      format?: string;
      snapshotMode: "full" | "sample";
      rowLimit?: number;
    }
  | {
      kind: "http";
      url: string;
      method: string;
      body?: unknown;
      recordsPath?: string;
      snapshotMode: "full" | "sample";
      rowLimit?: number;
    };

function NotebookAddDataDialog({
  open,
  onOpenChange,
  queryConnections,
  connections,
  environment,
  onCreate,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  queryConnections: WorkspaceQueryConnection[];
  connections: Record<string, string>;
  environment: string;
  onCreate: (input: NotebookDataSourceInput) => Promise<void>;
}) {
  const loadTables = useSetAtom(sqlDiscoveryTablesAtom);
  const [kind, setKind] = useState<"warehouse" | "file" | "http">("warehouse");
  const [connection, setConnection] = useState("");
  const [relation, setRelation] = useState("");
  const [filter, setFilter] = useState("");
  const [tables, setTables] = useState<Array<{ name: string; short_name: string }>>([]);
  const [fileConnection, setFileConnection] = useState("__local__");
  const [fileURI, setFileURI] = useState("");
  const [fileFormat, setFileFormat] = useState("__auto__");
  const [requestURL, setRequestURL] = useState("");
  const [requestMethod, setRequestMethod] = useState("GET");
  const [requestBody, setRequestBody] = useState("");
  const [recordsPath, setRecordsPath] = useState("");
  const [snapshotMode, setSnapshotMode] = useState<"full" | "sample">("full");
  const [rowLimit, setRowLimit] = useState(10000);
  const [loading, setLoading] = useState(false);
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState("");
  const storageConnections = useMemo(
    () =>
      Object.entries(connections)
        .filter(([, type]) => ["s3", "gcs"].includes(type.trim().toLowerCase()))
        .sort(([left], [right]) => left.localeCompare(right)),
    [connections],
  );

  useEffect(() => {
    if (!open) return;
    setConnection((current) =>
      queryConnections.some((candidate) => candidate.name === current)
        ? current
        : (queryConnections[0]?.name ?? ""),
    );
    setKind("warehouse");
    setRelation("");
    setFilter("");
    setFileConnection("__local__");
    setFileURI("");
    setFileFormat("__auto__");
    setRequestURL("");
    setRequestMethod("GET");
    setRequestBody("");
    setRecordsPath("");
    setSnapshotMode("full");
    setRowLimit(10000);
    setError("");
  }, [open, queryConnections]);

  useEffect(() => {
    if (!open || kind !== "warehouse" || !connection) {
      setTables([]);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError("");
    void loadTables({ connection, environment })
      .then((result) => {
        if (!cancelled) setTables(result);
      })
      .catch((cause: unknown) => {
        if (!cancelled) {
          setTables([]);
          setError(cause instanceof Error ? cause.message : "Could not browse this connection.");
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [connection, environment, kind, loadTables, open]);

  const visibleTables = tables.filter((table) =>
    table.name.toLowerCase().includes(filter.trim().toLowerCase()),
  );
  const submit = async () => {
    if (creating) return;
    setCreating(true);
    setError("");
    try {
      const snapshot = {
        snapshotMode,
        rowLimit: snapshotMode === "sample" ? rowLimit : undefined,
      };
      if (kind === "warehouse") {
        await onCreate({ kind, connection, relation: relation.trim(), ...snapshot });
      } else if (kind === "file") {
        await onCreate({
          kind,
          connection: fileConnection === "__local__" ? undefined : fileConnection,
          uri: fileURI.trim(),
          format: fileFormat === "__auto__" ? undefined : fileFormat,
          ...snapshot,
        });
      } else {
        let body: unknown;
        if (requestBody.trim()) {
          try {
            body = JSON.parse(requestBody);
          } catch {
            throw new Error("Request body must be valid JSON.");
          }
        }
        await onCreate({
          kind,
          url: requestURL.trim(),
          method: requestMethod,
          body,
          recordsPath: recordsPath.trim() || undefined,
          ...snapshot,
        });
      }
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Could not add the data source.");
    } finally {
      setCreating(false);
    }
  };
  const canSubmit =
    (snapshotMode === "full" || rowLimit > 0) &&
    ((kind === "warehouse" && Boolean(connection && relation.trim())) ||
      (kind === "file" && Boolean(fileURI.trim())) ||
      (kind === "http" && Boolean(requestURL.trim())));

  return (
    <Dialog open={open} onOpenChange={(next) => !creating && onOpenChange(next)}>
      <DialogContent className="max-h-[min(90vh,52rem)] overflow-hidden sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Add data</DialogTitle>
          <DialogDescription>
            Create a typed local snapshot that SQL and Python cells can join with every other
            notebook source.
          </DialogDescription>
        </DialogHeader>
        <ScrollArea viewportClassName="max-h-[calc(min(90vh,52rem)-11rem)] pr-3">
          <Tabs value={kind} onValueChange={(value) => setKind(value as typeof kind)}>
            <TabsList className="grid w-full grid-cols-3">
              <TabsTrigger value="warehouse">
                <Database />
                Warehouse
              </TabsTrigger>
              <TabsTrigger value="file">
                <FileInput />
                File
              </TabsTrigger>
              <TabsTrigger value="http">
                <Globe2 />
                HTTP
              </TabsTrigger>
            </TabsList>

            <TabsContent value="warehouse" className="grid min-w-0 gap-4 pt-2">
              {queryConnections.length === 0 ? (
                <div className="rounded-lg border border-dashed p-4 text-sm text-muted-foreground">
                  No query-capable connections are configured in this environment.
                </div>
              ) : (
                <>
                  <div className="grid gap-1.5">
                    <Label htmlFor="notebook-source-connection">Source connection</Label>
                    <ConnectionSelect
                      value={connection}
                      groups={[
                        {
                          label: "Query connections",
                          options: queryConnections.map((candidate) => ({
                            value: candidate.name,
                            label: candidate.name,
                            connectionType: candidate.connection_type,
                            detail: candidate.dialect,
                          })),
                        },
                      ]}
                      onValueChange={(value) => {
                        setConnection(value);
                        setRelation("");
                      }}
                      id="notebook-source-connection"
                      className="w-full"
                    />
                  </div>

                  <div className="grid min-w-0 gap-1.5">
                    <Label htmlFor="notebook-source-relation">Table or relation</Label>
                    <Input
                      id="notebook-source-relation"
                      value={relation}
                      onChange={(event) => setRelation(event.target.value)}
                      placeholder="catalog.schema.table"
                      className="font-mono"
                    />
                    <div className="overflow-hidden rounded-lg border">
                      <div className="border-b p-2">
                        <Input
                          value={filter}
                          onChange={(event) => setFilter(event.target.value)}
                          placeholder="Filter discovered tables…"
                          className="h-7"
                        />
                      </div>
                      <ScrollArea className="h-48" viewportClassName="max-h-48">
                        {loading ? (
                          <div className="flex items-center gap-2 p-3 text-xs text-muted-foreground">
                            <Loader2 className="size-3.5 animate-spin" /> Discovering tables…
                          </div>
                        ) : visibleTables.length > 0 ? (
                          <div className="grid">
                            {visibleTables.map((table) => (
                              <button
                                type="button"
                                key={table.name}
                                className={cn(
                                  "min-w-0 border-b px-3 py-2 text-left font-mono text-xs last:border-b-0 hover:bg-muted/50",
                                  relation === table.name && "bg-primary/10 text-primary",
                                )}
                                onClick={() => setRelation(table.name)}
                              >
                                <span className="block truncate">{table.name}</span>
                              </button>
                            ))}
                          </div>
                        ) : (
                          <p className="p-3 text-xs text-muted-foreground">
                            {error ||
                              "No tables discovered. You can enter a relation manually above."}
                          </p>
                        )}
                      </ScrollArea>
                    </div>
                  </div>
                </>
              )}
            </TabsContent>

            <TabsContent value="file" className="grid min-w-0 gap-4 pt-2">
              <div className="grid gap-1.5">
                <Label htmlFor="notebook-file-connection">Location</Label>
                <ConnectionSelect
                  value={fileConnection}
                  groups={[
                    {
                      label: "File locations",
                      options: [
                        {
                          value: "__local__",
                          label: "Workspace file",
                          connectionType: "file",
                          detail: "This Git workspace",
                        },
                        ...storageConnections.map(([name, type]) => ({
                          value: name,
                          label: name,
                          connectionType: type,
                        })),
                      ],
                    },
                  ]}
                  onValueChange={(value) => {
                    setFileConnection(value);
                    setFileURI("");
                  }}
                  id="notebook-file-connection"
                  className="w-full"
                />
              </div>
              <div className="grid min-w-0 gap-1.5">
                <Label htmlFor="notebook-file-uri">File or object</Label>
                {fileConnection === "__local__" ? (
                  <Input
                    id="notebook-file-uri"
                    value={fileURI}
                    onChange={(event) => setFileURI(event.target.value)}
                    placeholder="data/events.parquet"
                    className="font-mono"
                  />
                ) : (
                  <LoadStreamPicker
                    id="notebook-file-uri"
                    variant="field"
                    value={fileURI}
                    connection={fileConnection}
                    environment={environment}
                    placeholder="bucket/path/events.parquet"
                    ariaLabel="File or object"
                    onCommit={setFileURI}
                  />
                )}
                <p className="text-xs text-muted-foreground">
                  Local paths must stay inside this Git workspace. Credentials never enter the
                  notebook file.
                </p>
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="notebook-file-format">Format</Label>
                <Select value={fileFormat} onValueChange={setFileFormat}>
                  <SelectTrigger id="notebook-file-format" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="__auto__">Infer from file name</SelectItem>
                    {["csv", "parquet", "json", "jsonl", "avro"].map((format) => (
                      <SelectItem key={format} value={format}>
                        {format}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </TabsContent>

            <TabsContent value="http" className="grid min-w-0 gap-4 pt-2">
              <div className="grid gap-1.5">
                <Label htmlFor="notebook-http-url">Request URL</Label>
                <Input
                  id="notebook-http-url"
                  value={requestURL}
                  onChange={(event) => setRequestURL(event.target.value)}
                  placeholder="https://api.example.com/events"
                  className="font-mono"
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="notebook-http-method">Method</Label>
                <Select value={requestMethod} onValueChange={setRequestMethod}>
                  <SelectTrigger id="notebook-http-method" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {["GET", "POST", "PUT", "PATCH"].map((method) => (
                      <SelectItem key={method} value={method}>
                        {method}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="notebook-http-records">Records path</Label>
                <Input
                  id="notebook-http-records"
                  value={recordsPath}
                  onChange={(event) => setRecordsPath(event.target.value)}
                  placeholder="data.items (optional)"
                  className="font-mono"
                />
              </div>
              <div className="grid gap-1.5">
                <Label htmlFor="notebook-http-body">JSON request body</Label>
                <textarea
                  id="notebook-http-body"
                  value={requestBody}
                  onChange={(event) => setRequestBody(event.target.value)}
                  rows={5}
                  spellCheck={false}
                  placeholder={'{\n  "after": "{{ start_datetime }}"\n}'}
                  className="w-full resize-y rounded-lg border bg-background p-3 font-mono text-xs leading-5 outline-none focus-visible:ring-2 focus-visible:ring-ring"
                />
              </div>
            </TabsContent>

            <div className="mt-4 grid gap-1.5">
              <Label>Snapshot</Label>
              <div className="flex min-w-0 gap-2">
                <Select
                  value={snapshotMode}
                  onValueChange={(value: "full" | "sample") => setSnapshotMode(value)}
                >
                  <SelectTrigger className="w-44">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="full">Complete result</SelectItem>
                    <SelectItem value="sample">Explicit sample</SelectItem>
                  </SelectContent>
                </Select>
                {snapshotMode === "sample" ? (
                  <Input
                    type="number"
                    min={1}
                    max={10000000}
                    value={rowLimit}
                    onChange={(event) => setRowLimit(Math.max(1, Number(event.target.value) || 1))}
                    aria-label="Sample row limit"
                    className="w-40"
                  />
                ) : null}
              </div>
              <p className="text-xs text-muted-foreground">
                A complete snapshot fails if it exceeds Renart&apos;s transfer budget. A sample
                stays marked as sampled in downstream results.
              </p>
            </div>
          </Tabs>
        </ScrollArea>
        {error ? <p className="text-sm text-destructive">{error}</p> : null}
        <DialogFooter>
          <Button variant="outline" disabled={creating} onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={creating || !canSubmit} onClick={() => void submit()}>
            {creating ? <Loader2 className="animate-spin" /> : <Database />}
            {creating ? "Adding…" : "Add source"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function DeleteNotebookCellDialog({
  cell,
  deleting,
  onOpenChange,
  onConfirm,
}: {
  cell: NotebookCellDeleteTarget | null;
  deleting: boolean;
  onOpenChange: (open: boolean) => void;
  onConfirm: () => void;
}) {
  return (
    <AlertDialog open={cell !== null} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Delete {cell?.name ?? "this cell"}?</AlertDialogTitle>
          <AlertDialogDescription>
            This deletes the cell file and removes it from the notebook. This action cannot be
            undone.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={deleting}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            disabled={deleting}
            onClick={(event) => {
              event.preventDefault();
              onConfirm();
            }}
          >
            {deleting ? (
              <Spinner data-icon="inline-start" aria-label="Deleting cell" />
            ) : (
              <Trash2 data-icon="inline-start" />
            )}
            Delete cell
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function statusDotClass(result: NotebookCellRunResult | undefined, stale: boolean) {
  if (result?.status === "error") return "bg-red-500";
  if (result?.status === "blocked") return "bg-amber-500";
  if (stale) return "bg-amber-400";
  if (result?.status === "ok") return "bg-emerald-500";
  return "bg-muted-foreground/40";
}

function formatNotebookBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const exponent = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / 1024 ** exponent;
  return `${value >= 10 || exponent === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[exponent]}`;
}

function formatNotebookTimestamp(value: string) {
  const timestamp = new Date(value);
  if (Number.isNaN(timestamp.getTime())) return value || "Unknown";
  return new Intl.DateTimeFormat(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(timestamp);
}

function NotebookSnapshotSummary({
  snapshot,
  stale,
  fallbackConnection,
  label,
}: {
  snapshot: NonNullable<NotebookCellRunResult["snapshot"]>;
  stale: boolean;
  fallbackConnection?: string;
  label: string;
}) {
  const state = stale ? "Definition changed" : snapshot.sampled ? "Sampled" : "Complete";
  const connection = snapshot.connection?.trim() || fallbackConnection?.trim() || "Workspace";
  const captured = formatNotebookTimestamp(snapshot.imported_at);

  return (
    <dl
      aria-label={label}
      className="grid min-w-0 grid-cols-2 gap-x-4 gap-y-2 rounded-lg bg-muted/25 px-3 py-2 text-xs sm:grid-cols-3 lg:grid-cols-6"
    >
      <div className="min-w-0">
        <dt className="text-[10px] uppercase tracking-wide text-muted-foreground">Snapshot</dt>
        <dd className="mt-0.5 flex items-center gap-1.5 font-medium">
          <span
            className={cn(
              "size-1.5 shrink-0 rounded-full",
              stale ? "bg-amber-500" : snapshot.sampled ? "bg-sky-500" : "bg-emerald-500",
            )}
          />
          {state}
        </dd>
      </div>
      <div className="min-w-0">
        <dt className="text-[10px] uppercase tracking-wide text-muted-foreground">Captured</dt>
        <dd className="mt-0.5 truncate" title={snapshot.imported_at}>
          <time dateTime={snapshot.imported_at}>{captured}</time>
        </dd>
      </div>
      <div className="min-w-0">
        <dt className="text-[10px] uppercase tracking-wide text-muted-foreground">Connection</dt>
        <dd className="mt-0.5 truncate font-mono" title={connection}>
          {connection}
        </dd>
      </div>
      <div className="min-w-0">
        <dt className="text-[10px] uppercase tracking-wide text-muted-foreground">Environment</dt>
        <dd className="mt-0.5 truncate font-mono">{snapshot.environment || "default"}</dd>
      </div>
      <div className="min-w-0">
        <dt className="text-[10px] uppercase tracking-wide text-muted-foreground">Rows</dt>
        <dd className="mt-0.5 tabular-nums">{snapshot.row_count.toLocaleString()}</dd>
      </div>
      <div className="min-w-0">
        <dt className="text-[10px] uppercase tracking-wide text-muted-foreground">Size</dt>
        <dd className="mt-0.5 tabular-nums">{formatNotebookBytes(snapshot.byte_count)}</dd>
      </div>
    </dl>
  );
}

function formatNotebookDuration(milliseconds: number | undefined) {
  if (milliseconds === undefined || !Number.isFinite(milliseconds)) return "Unknown";
  if (milliseconds < 1) return "<1 ms";
  if (milliseconds < 1_000) {
    return `${milliseconds < 10 ? milliseconds.toFixed(1) : Math.round(milliseconds)} ms`;
  }
  return `${(milliseconds / 1_000).toFixed(2)} s`;
}

function NotebookPerformanceDetails({
  result,
  renderMeasurement,
}: {
  result: NotebookCellRunResult;
  renderMeasurement?: VirtualTableRenderMeasurement;
}) {
  const metrics: Array<[string, string]> = [];
  if (result.performance?.request_total_ms !== undefined) {
    metrics.push(["Request total", formatNotebookDuration(result.performance.request_total_ms)]);
  }
  if (result.performance?.request_setup_ms !== undefined) {
    metrics.push(["Request setup", formatNotebookDuration(result.performance.request_setup_ms)]);
  }
  if (result.performance?.batch_run_ms !== undefined) {
    metrics.push(["Batch execution", formatNotebookDuration(result.performance.batch_run_ms)]);
  }
  if (result.performance?.session_open_ms !== undefined) {
    metrics.push(["Session open", formatNotebookDuration(result.performance.session_open_ms)]);
  }
  metrics.push(["Cell execution", formatNotebookDuration(result.duration_ms)]);
  if (result.performance?.materialize_ms !== undefined) {
    metrics.push(["Materialize", formatNotebookDuration(result.performance.materialize_ms)]);
  }
  if (result.performance?.preview_query_ms !== undefined) {
    metrics.push(["Preview query", formatNotebookDuration(result.performance.preview_query_ms)]);
  }
  if (result.performance?.metadata_write_ms !== undefined) {
    metrics.push(["Run metadata", formatNotebookDuration(result.performance.metadata_write_ms)]);
  }
  if (result.performance?.runtime_sync_ms !== undefined) {
    metrics.push(["Runtime sync", formatNotebookDuration(result.performance.runtime_sync_ms)]);
  }
  if (renderMeasurement) {
    metrics.push(["Preview render", formatNotebookDuration(renderMeasurement.durationMs)]);
    metrics.push([
      "Mounted rows",
      `${renderMeasurement.renderedRows.toLocaleString()} of ${renderMeasurement.totalRows.toLocaleString()}`,
    ]);
  }
  if (result.performance?.transfer_bytes) {
    metrics.push(["Transferred", formatNotebookBytes(result.performance.transfer_bytes)]);
  }
  if (result.performance?.session_bytes) {
    metrics.push(["Notebook storage", formatNotebookBytes(result.performance.session_bytes)]);
  }
  if (result.performance?.python_startup_ms !== undefined) {
    metrics.push(["Python startup", formatNotebookDuration(result.performance.python_startup_ms)]);
  }

  return (
    <HoverCard closeDelay={100} openDelay={200}>
      <HoverCardTrigger asChild>
        <Button aria-label="Show local performance measurements" size="xs" variant="ghost">
          <Gauge data-icon="inline-start" />
          Performance
        </Button>
      </HoverCardTrigger>
      <HoverCardContent align="end" className="w-64 p-3">
        <p className="text-sm font-medium">Local performance</p>
        <p className="mt-0.5 text-xs text-muted-foreground">
          Cell timings plus shared request measurements observed by this Renart process and browser.
        </p>
        <dl className="mt-3 grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1.5 text-xs">
          {metrics.map(([label, value]) => (
            <Fragment key={label}>
              <dt className="text-muted-foreground">{label}</dt>
              <dd className="text-right font-mono tabular-nums">{value}</dd>
            </Fragment>
          ))}
        </dl>
      </HoverCardContent>
    </HoverCard>
  );
}

function NotebookResultPreview({
  cellName,
  result,
}: {
  cellName: string;
  result: NotebookCellRunResult;
}) {
  const [renderMeasurement, setRenderMeasurement] = useState<VirtualTableRenderMeasurement>();
  const rows = useMemo(
    () =>
      result.rows.map((row) =>
        Object.fromEntries(row.map((value, index) => [`column_${index}`, value])),
      ),
    [result.rows],
  );
  const columnKeys = useMemo(
    () => result.columns.map((_, index) => `column_${index}`),
    [result.columns],
  );
  useEffect(() => setRenderMeasurement(undefined), [rows]);

  const rowsShown = result.rows.length;
  const truncated = result.total_rows > rowsShown;

  return (
    <div className="overflow-hidden rounded-lg border">
      <VirtualDataTable
        ariaLabel={`${cellName} result preview`}
        columnKeys={columnKeys}
        columns={result.columns}
        frameless
        height={288}
        onRenderMeasured={setRenderMeasurement}
        rows={rows}
        scrollKey={`notebook:${result.cell_id}:preview`}
        viewportClassName="max-h-72"
      />
      <div className="flex min-h-8 items-center justify-between gap-2 border-t bg-muted/30 px-2 text-[11px] text-muted-foreground">
        <span>
          {truncated
            ? `showing ${rowsShown.toLocaleString()} of ${result.total_rows.toLocaleString()} rows`
            : `${rowsShown.toLocaleString()} rows`}
        </span>
        <NotebookPerformanceDetails result={result} renderMeasurement={renderMeasurement} />
      </div>
    </div>
  );
}

function isDuckDBNotebookConnection(
  connection: string,
  assetType: string | undefined,
  queryConnections: WorkspaceQueryConnection[],
) {
  const normalized = connection.trim().toLowerCase();
  const configured = queryConnections.find(
    (candidate) => candidate.name.trim().toLowerCase() === normalized,
  );
  return (
    configured?.connection_type.trim().toLowerCase() === "duckdb" ||
    configured?.asset_type.trim().toLowerCase().startsWith("duckdb.") === true ||
    assetType?.trim().toLowerCase().startsWith("duckdb.") === true
  );
}

function notebookSourceRequiresImportReview(
  cell: WebAsset,
  queryConnections: WorkspaceQueryConnection[],
) {
  const source = cell.notebook_source;
  if (!source) {
    const connection = cell.connection?.trim() ?? "";
    return (
      Boolean(connection) && !isDuckDBNotebookConnection(connection, cell.type, queryConnections)
    );
  }
  const connection = source.connection?.trim() ?? "";
  if (connection) {
    return !isDuckDBNotebookConnection(connection, cell.type, queryConnections);
  }
  if (source.kind === "http" || source.kind === "object" || source.kind === "object_storage") {
    return true;
  }
  const uri = source.uri?.trim().toLowerCase() ?? "";
  return uri.includes("://") && !uri.startsWith("file://");
}

function downloadNotebookCell(notebookId: string, cellId: string, format: "csv" | "parquet") {
  const anchor = document.createElement("a");
  anchor.href = notebookCellExportURL(notebookId, cellId, format);
  anchor.click();
}

function NotebookSourceCard({
  notebookId,
  cell,
  queryConnections,
  result,
  stale,
  running,
  busy,
  onRun,
  onCancel,
  onRunFromHere,
  onDelete,
  onRename,
  onPromote,
}: {
  notebookId: string;
  cell: WebAsset;
  queryConnections: WorkspaceQueryConnection[];
  result?: NotebookCellRunResult;
  stale: boolean;
  running: boolean;
  busy: boolean;
  onRun: () => void;
  onCancel: () => void;
  onRunFromHere: () => void;
  onDelete: () => void;
  onRename: (name: string) => Promise<void>;
  onPromote: () => void;
}) {
  const source = cell.notebook_source;
  const [renaming, setRenaming] = useState(false);
  const [nameDraft, setNameDraft] = useState(cell.name);
  useEffect(() => setNameDraft(cell.name), [cell.name]);
  if (!source) return null;

  const commitRename = () => {
    const trimmed = nameDraft.trim();
    setRenaming(false);
    if (trimmed && trimmed !== cell.name) void onRename(trimmed);
    else setNameDraft(cell.name);
  };
  const sourceLabel =
    source.kind === "http"
      ? `${source.request?.method || "GET"} ${source.request?.url || ""}`
      : source.uri || "";
  const requiresImportReview =
    notebookSourceRequiresImportReview(cell, queryConnections) && (!result?.snapshot || stale);

  return (
    <AppPanel
      className={cn(
        NOTEBOOK_BLOCK_CARD_CLASS,
        "border-l-2 border-l-cyan-500/55 hover:bg-cyan-500/[0.025]",
      )}
    >
      <DelimitedCardHeader
        className={cn(NOTEBOOK_BLOCK_HEADER_CLASS, stale && "notebook-stale-hatch")}
      >
        <span className={cn("size-2 rounded-full", statusDotClass(result, stale))} />
        {renaming ? (
          <input
            autoFocus
            value={nameDraft}
            spellCheck={false}
            onChange={(event) => setNameDraft(event.target.value)}
            onBlur={commitRename}
            onKeyDown={(event) => {
              if (event.key === "Enter") commitRename();
              if (event.key === "Escape") {
                setNameDraft(cell.name);
                setRenaming(false);
              }
            }}
            className="w-40 rounded border bg-background px-1.5 py-0.5 font-mono text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        ) : (
          <button
            type="button"
            className="rounded font-mono text-sm font-medium hover:bg-muted"
            onClick={() => setRenaming(true)}
          >
            {cell.name}
          </button>
        )}
        <Badge variant="secondary" className="font-mono text-[10px]">
          {source.kind === "http" ? <Globe2 /> : <FileInput />}
          {source.connection ? "object" : source.kind}
        </Badge>
        <Badge variant="outline" className="text-[10px]">
          {source.snapshot.mode === "sample"
            ? `sample ${source.snapshot.row_limit?.toLocaleString() ?? ""}`
            : "complete snapshot"}
        </Badge>
        {requiresImportReview ? (
          <Badge
            variant="outline"
            className="border-amber-500/35 bg-amber-500/10 text-[10px] text-amber-800 dark:text-amber-200"
          >
            <AlertTriangle />
            Review required
          </Badge>
        ) : null}
        <span className="ml-auto text-[11px] text-muted-foreground">
          {running ? "refreshing…" : result?.status === "ok" ? `${result.duration_ms} ms` : null}
        </span>
        {running ? (
          <Button variant="ghost" size="icon-sm" onClick={onCancel} title="Stop refresh">
            <Square className="size-3.5 fill-current" />
          </Button>
        ) : requiresImportReview ? (
          <Button variant="outline" size="sm" disabled={busy} onClick={onRun}>
            <Play />
            Review &amp; import
          </Button>
        ) : (
          <Button
            variant="ghost"
            size="icon-sm"
            disabled={busy}
            onClick={onRun}
            title="Refresh source"
          >
            <RotateCw className="size-3.5" />
          </Button>
        )}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon-sm" aria-label="Source actions">
              <MoreHorizontal />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-52">
            <DropdownMenuItem disabled={busy} onSelect={onRunFromHere}>
              <Play />
              Refresh and run downstream
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setRenaming(true)}>
              <Pencil />
              Rename source
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onPromote}>
              <ArrowUpFromLine />
              Promote to pipeline
            </DropdownMenuItem>
            <DropdownMenuItem
              disabled={result?.status !== "ok" || stale}
              onSelect={() => downloadNotebookCell(notebookId, cell.cell_id ?? "", "csv")}
            >
              <Download />
              Download CSV
            </DropdownMenuItem>
            <DropdownMenuItem
              disabled={result?.status !== "ok" || stale}
              onSelect={() => downloadNotebookCell(notebookId, cell.cell_id ?? "", "parquet")}
            >
              <Download />
              Download Parquet
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={onDelete}>
              <Trash2 />
              Delete source
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </DelimitedCardHeader>
      <DelimitedCardContent className="space-y-3">
        <div className="grid min-w-0 gap-2 rounded-lg border bg-background/70 p-3 text-xs sm:grid-cols-[minmax(0,1fr)_auto]">
          <div className="min-w-0">
            <p className="text-muted-foreground">Source</p>
            <p className="truncate font-mono" title={sourceLabel}>
              {sourceLabel}
            </p>
          </div>
          <div className="min-w-0 sm:text-right">
            <p className="text-muted-foreground">Context</p>
            <p className="truncate font-mono">
              {source.connection ||
                source.format ||
                (source.kind === "http" ? "HTTP" : "workspace")}
            </p>
          </div>
          {source.kind === "http" && source.response?.records_path ? (
            <div className="min-w-0 sm:col-span-2">
              <span className="text-muted-foreground">Records path </span>
              <span className="font-mono">{source.response.records_path}</span>
            </div>
          ) : null}
        </div>
        {result?.snapshot ? (
          <NotebookSnapshotSummary
            snapshot={result.snapshot}
            stale={stale}
            fallbackConnection={
              source.connection || source.format || (source.kind === "http" ? "HTTP" : "Workspace")
            }
            label={`${cell.name} snapshot details`}
          />
        ) : null}
        {result?.snapshot?.warnings?.map((warning) => (
          <p
            key={warning}
            className="rounded-lg border border-amber-500/25 bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-200"
          >
            {warning}
          </p>
        ))}
        {result?.status === "error" || result?.status === "blocked" ? (
          <div className="overflow-hidden rounded-lg border border-red-200 bg-red-50 text-red-800 dark:border-red-500/25 dark:bg-red-500/10 dark:text-red-200">
            <AnsiOutput
              output={result.error}
              className="max-h-72 overflow-auto px-3 py-2 font-mono text-xs leading-5 whitespace-pre-wrap break-words"
            />
          </div>
        ) : null}
        {result?.status === "ok" && result.columns.length > 0 ? (
          <NotebookResultPreview cellName={cell.name} result={result} />
        ) : null}
      </DelimitedCardContent>
    </AppPanel>
  );
}

function NotebookCellCard({
  notebookId,
  cell,
  cells,
  dependencies,
  installedModules,
  parameters,
  onAddDependency,
  resultColumnsByCell,
  result,
  stale,
  running,
  busy,
  onRun,
  onCancel,
  onRunFromHere,
  onDelete,
  onRename,
  onPromote,
  onSaveBody,
  autoCommit,
  pendingAuto,
  queryConnections,
  onConfigureSource,
  onMigrateLegacyViz,
  onGoToAsset,
  onGoToCell,
}: {
  notebookId: string;
  cell: WebAsset;
  cells: WebAsset[];
  dependencies: string[];
  installedModules: string[];
  parameters: NonNullable<WebNotebook["parameters"]>;
  onAddDependency: (pkg: string) => void;
  resultColumnsByCell: Map<string, string[]>;
  result?: NotebookCellRunResult;
  stale: boolean;
  running: boolean;
  busy: boolean;
  onRun: () => void;
  onCancel: () => void;
  onRunFromHere: () => void;
  onDelete: () => void;
  onRename: (name: string) => Promise<void>;
  onPromote: () => void;
  onSaveBody: (body: string, baseRevision: string) => Promise<void>;
  /** Save the draft on a typing debounce (drives auto-recompute without a blur). */
  autoCommit: boolean;
  /** Stale, but auto-recompute will refresh it on its own — don't flag it stale. */
  pendingAuto: boolean;
  queryConnections: WorkspaceQueryConnection[];
  onConfigureSource: (input: {
    connection?: string;
    snapshot_mode?: "full" | "sample";
    row_limit?: number;
  }) => Promise<void>;
  onMigrateLegacyViz: () => Promise<void>;
  onGoToAsset?: (pipelineId: string, assetId: string) => void;
  onGoToCell?: (cellId: string) => void;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const schemaTables = useMemo(
    () => buildNotebookSchemaTables(workspace, cells, cell, resultColumnsByCell),
    [workspace, cells, cell, resultColumnsByCell],
  );
  const isPythonCell = usesPythonSource(cell);
  const sourceConnection = cell.connection?.trim() ?? "";
  const snapshotMode = cell.meta?.renart_notebook_snapshot_mode === "sample" ? "sample" : "full";
  const snapshotRowLimit = Number(cell.meta?.renart_notebook_snapshot_row_limit ?? 10000);
  const [sourceChanging, setSourceChanging] = useState(false);
  const [migratingViz, setMigratingViz] = useState(false);
  const { body } = useMemo(() => splitCellContent(cell.content), [cell.content]);
  const [draft, setDraft] = useState(body);

  const missingDeps = useMemo(
    () => (isPythonCell ? missingPythonImports(draft, dependencies, installedModules) : []),
    [isPythonCell, draft, dependencies, installedModules],
  );
  const lastSavedRef = useRef(body);
  // This is the snapshot the current draft actually branched from. It advances
  // only when the matching server body is adopted; an SSE update arriving
  // underneath unsaved typing must not silently rebase that draft.
  const lastSavedRevisionRef = useRef(cell.content_revision ?? "");
  const savingBodyRef = useRef<string | null>(null);
  useEffect(() => {
    // Adopt the incoming body, but never clobber unsaved local edits: with
    // auto-commit the cell saves mid-typing, and the save's echo (or any other
    // refresh) must not overwrite characters the user typed while it was in
    // flight. Only reset when the draft matches what we last persisted or the
    // incoming server body has caught up to the current draft.
    setDraft((current) => {
      if (savingBodyRef.current === body) {
        savingBodyRef.current = null;
      }
      if (current !== lastSavedRef.current && current !== body) {
        return current;
      }
      lastSavedRef.current = body;
      lastSavedRevisionRef.current = cell.content_revision ?? "";
      return body;
    });
  }, [body, cell.content_revision]);

  const [renaming, setRenaming] = useState(false);
  const [nameDraft, setNameDraft] = useState(cell.name);
  useEffect(() => {
    setNameDraft(cell.name);
  }, [cell.name]);

  const commitRename = () => {
    const trimmed = nameDraft.trim();
    setRenaming(false);
    if (trimmed && trimmed !== cell.name) {
      void onRename(trimmed);
    } else {
      setNameDraft(cell.name);
    }
  };

  const commit = () => {
    if (draft !== lastSavedRef.current && draft !== savingBodyRef.current) {
      savingBodyRef.current = draft;
      void onSaveBody(draft, lastSavedRevisionRef.current).finally(() => {
        if (savingBodyRef.current === draft) {
          savingBodyRef.current = null;
        }
      });
    }
  };

  // Auto-commit while typing: when enabled, persist the draft a beat after the
  // user pauses, so staleness and auto-recompute kick in without needing a blur.
  // Debounced on the draft alone (via a ref for the save callback) so unrelated
  // re-renders can't keep resetting the timer and starving the save. A blur
  // still commits immediately; broken in-progress SQL stays put because
  // auto-recompute only runs cells the parser reports as clean.
  const onSaveBodyRef = useRef(onSaveBody);
  onSaveBodyRef.current = onSaveBody;
  useEffect(() => {
    if (!autoCommit || draft === lastSavedRef.current || draft === savingBodyRef.current) {
      return;
    }
    const timer = window.setTimeout(() => {
      if (draft !== lastSavedRef.current && draft !== savingBodyRef.current) {
        savingBodyRef.current = draft;
        void onSaveBodyRef.current(draft, lastSavedRevisionRef.current).finally(() => {
          if (savingBodyRef.current === draft) {
            savingBodyRef.current = null;
          }
        });
      }
    }, AUTO_COMMIT_DEBOUNCE_MS);
    return () => window.clearTimeout(timer);
  }, [autoCommit, draft]);

  const vizDiagnostics = result?.viz_diagnostics ?? [];
  // Only surface staleness for cells the user must act on. A cell auto-recompute
  // is about to refresh is left unmarked — flagging it would just flicker.
  const showStale = stale && !pendingAuto;
  const requiresImportReview =
    Boolean(sourceConnection) &&
    !isDuckDBNotebookConnection(sourceConnection, cell.type, queryConnections) &&
    (!result?.snapshot || stale);

  return (
    <AppPanel
      className={cn(
        NOTEBOOK_BLOCK_CARD_CLASS,
        sourceConnection && "border-l-2 border-l-primary/50 hover:bg-primary/[0.02]",
      )}
    >
      <DelimitedCardHeader
        className={cn(NOTEBOOK_BLOCK_HEADER_CLASS, showStale && "notebook-stale-hatch")}
      >
        <span className={cn("size-2 rounded-full", statusDotClass(result, showStale))} />
        {renaming ? (
          <input
            autoFocus
            value={nameDraft}
            spellCheck={false}
            onChange={(event) => setNameDraft(event.target.value)}
            onBlur={commitRename}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                commitRename();
              } else if (event.key === "Escape") {
                event.preventDefault();
                setNameDraft(cell.name);
                setRenaming(false);
              }
            }}
            className="w-40 rounded border bg-background px-1.5 py-0.5 font-mono text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        ) : (
          <button
            type="button"
            className="rounded font-mono text-sm font-medium hover:bg-muted"
            title="Rename cell (F2)"
            onClick={() => setRenaming(true)}
          >
            {cell.name}
          </button>
        )}
        <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
          {isPythonCell ? "python" : "sql"}
        </span>
        {!isPythonCell && queryConnections.length > 0 ? (
          <ConnectionSelect
            value={sourceConnection || "__local__"}
            groups={[
              {
                label: "Execution context",
                options: [
                  {
                    value: "__local__",
                    label: "Local notebook DuckDB",
                    connectionType: "duckdb",
                    detail: "Notebook session",
                  },
                  ...(sourceConnection &&
                  !queryConnections.some((connection) => connection.name === sourceConnection)
                    ? [
                        {
                          value: sourceConnection,
                          label: sourceConnection,
                          badge: "unavailable",
                          badgeVariant: "destructive" as const,
                        },
                      ]
                    : []),
                  ...queryConnections.map((connection) => ({
                    value: connection.name,
                    label: connection.name,
                    connectionType: connection.connection_type,
                    detail: connection.dialect,
                  })),
                ],
              },
            ]}
            disabled={busy || sourceChanging}
            loading={sourceChanging}
            onValueChange={(value) => {
              const connection = value === "__local__" ? undefined : value;
              setSourceChanging(true);
              void onConfigureSource({
                connection,
                snapshot_mode: connection ? snapshotMode : undefined,
                row_limit: connection && snapshotMode === "sample" ? snapshotRowLimit : undefined,
              }).finally(() => setSourceChanging(false));
            }}
            size="sm"
            ariaLabel="Source connection"
            className="max-w-52"
            contentAlign="start"
          />
        ) : null}
        {sourceConnection ? (
          <Select
            value={snapshotMode}
            disabled={busy || sourceChanging}
            onValueChange={(value: "full" | "sample") => {
              setSourceChanging(true);
              void onConfigureSource({
                connection: sourceConnection,
                snapshot_mode: value,
                row_limit: value === "sample" ? snapshotRowLimit : undefined,
              }).finally(() => setSourceChanging(false));
            }}
          >
            <SelectTrigger size="sm" aria-label="Snapshot mode">
              <SelectValue />
            </SelectTrigger>
            <SelectContent align="start">
              <SelectItem value="full">Full snapshot</SelectItem>
              <SelectItem value="sample">
                Sample {snapshotRowLimit.toLocaleString()} rows
              </SelectItem>
            </SelectContent>
          </Select>
        ) : null}
        {result?.materialized === "table" ? (
          <span className="rounded bg-sky-50 px-1.5 py-0.5 text-[10px] text-sky-700 dark:bg-sky-500/15 dark:text-sky-300">
            table
          </span>
        ) : null}
        {result?.imports?.map((imported) => (
          <span
            key={imported.ref}
            title={`imported ${imported.imported_at}${imported.complete ? "" : " · truncated"}`}
            className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground"
          >
            {imported.ref}
            {imported.complete ? "" : " ⚠"}
          </span>
        ))}
        {!result?.snapshot && result?.sampled ? (
          <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] text-amber-700 dark:text-amber-200">
            derived from sample
          </span>
        ) : null}
        {requiresImportReview ? (
          <Badge
            variant="outline"
            className="border-amber-500/35 bg-amber-500/10 text-[10px] text-amber-800 dark:text-amber-200"
          >
            <AlertTriangle />
            Review required
          </Badge>
        ) : null}
        <span className="ml-auto text-[11px] text-muted-foreground">
          {running
            ? "running…"
            : result?.status === "ok"
              ? `${result.total_rows} rows · ${result.duration_ms} ms`
              : null}
        </span>
        {running ? (
          <Button
            variant="ghost"
            size="icon-sm"
            className="group"
            onClick={onCancel}
            title="Stop cell"
          >
            <Loader2 className="size-3.5 animate-spin group-hover:hidden" />
            <Square className="hidden size-3.5 fill-current group-hover:block" />
          </Button>
        ) : requiresImportReview ? (
          <Button variant="outline" size="sm" disabled={busy} onClick={onRun}>
            <Play />
            Review &amp; import
          </Button>
        ) : (
          <Button variant="ghost" size="icon-sm" disabled={busy} onClick={onRun} title="Run cell">
            <Play className="size-3.5" />
          </Button>
        )}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon-sm" aria-label="Cell actions">
              <MoreHorizontal className="size-3.5" />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-52">
            <DropdownMenuItem disabled={busy} onSelect={onRunFromHere}>
              <Play className="size-4" />
              Run from here
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => setRenaming(true)}>
              <Pencil className="size-4" />
              Rename cell
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={onPromote}>
              <ArrowUpFromLine className="size-4" />
              Promote to pipeline
            </DropdownMenuItem>
            <DropdownMenuItem
              disabled={result?.status !== "ok" || showStale}
              onSelect={() => downloadNotebookCell(notebookId, cell.cell_id ?? "", "csv")}
            >
              <Download className="size-4" />
              Download CSV
            </DropdownMenuItem>
            <DropdownMenuItem
              disabled={result?.status !== "ok" || showStale}
              onSelect={() => downloadNotebookCell(notebookId, cell.cell_id ?? "", "parquet")}
            >
              <Download className="size-4" />
              Download Parquet
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={onDelete}>
              <Trash2 className="size-4" />
              Delete cell
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </DelimitedCardHeader>
      <DelimitedCardContent className="space-y-3">
        {result?.snapshot ? (
          <NotebookSnapshotSummary
            snapshot={result.snapshot}
            stale={showStale}
            fallbackConnection={sourceConnection}
            label={`${cell.name} snapshot details`}
          />
        ) : null}
        <NotebookCellMonaco
          cell={cell}
          value={draft}
          schemaTables={schemaTables}
          onChange={setDraft}
          onCommit={commit}
          onRun={onRun}
          onRename={() => setRenaming(true)}
          onGoToAsset={onGoToAsset}
          onGoToCell={onGoToCell}
          parameters={parameters}
        />
        <MissingPythonDepsBanner missingImports={missingDeps} onAddDependency={onAddDependency} />
        {result?.status === "error" ? (
          <div className="overflow-hidden rounded-lg border border-red-200 bg-red-50 text-red-800 dark:border-red-500/25 dark:bg-red-500/10 dark:text-red-200">
            <AnsiOutput
              output={result.error}
              className="max-h-72 overflow-auto px-3 py-2 font-mono text-xs leading-5 whitespace-pre-wrap break-words"
            />
          </div>
        ) : null}
        {result?.status === "blocked" ? (
          <div className="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:border-amber-500/25 dark:bg-amber-500/10 dark:text-amber-200">
            {result.error}
          </div>
        ) : null}
        {result?.logs ? (
          <NotebookCellLogs logs={result.logs} isError={result.status === "error"} />
        ) : null}
        {vizDiagnostics.length > 0 ? (
          <div className="space-y-1">
            {vizDiagnostics.map((diagnostic, index) => (
              <div
                key={index}
                className={cn(
                  "rounded border px-2 py-1 text-[11px]",
                  diagnostic.severity === "error"
                    ? "border-red-200 bg-red-50 text-red-700 dark:border-red-500/25 dark:bg-red-500/10 dark:text-red-300"
                    : "border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-500/25 dark:bg-amber-500/10 dark:text-amber-300",
                )}
              >
                @viz: {diagnostic.message}
              </div>
            ))}
          </div>
        ) : null}
        {result?.status === "ok" && result.columns.length > 0 ? (
          result.viz ? (
            <div className="space-y-2">
              <div className="flex items-center gap-2 text-[11px] text-muted-foreground">
                <span>
                  Legacy <span className="font-mono">@viz</span> preview
                </span>
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  disabled={busy || migratingViz}
                  onClick={() => {
                    setMigratingViz(true);
                    void onMigrateLegacyViz().finally(() => setMigratingViz(false));
                  }}
                >
                  {migratingViz ? <Loader2 className="animate-spin" /> : <RotateCw />}
                  Move to visualization block
                </Button>
              </div>
              <NotebookVizRenderer result={result} />
            </div>
          ) : (
            <NotebookResultPreview cellName={cell.name} result={result} />
          )
        ) : null}
      </DelimitedCardContent>
    </AppPanel>
  );
}

// NotebookCellLogs shows a Python cell's captured stdout/stderr, collapsed by
// default and auto-expanded when the run errored.
function NotebookCellLogs({ logs, isError }: { logs: string; isError: boolean }) {
  const [open, setOpen] = useState(isError);
  // Re-apply the default whenever a new run lands (the logs/error may change).
  useEffect(() => {
    setOpen(isError);
  }, [logs, isError]);

  return (
    <div className="overflow-hidden rounded-lg border bg-muted/30">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
        className="flex w-full items-center gap-1.5 px-3 py-1.5 text-left text-xs font-medium text-muted-foreground hover:text-foreground"
      >
        <ChevronRight className={cn("size-3.5 transition-transform", open && "rotate-90")} />
        Output
      </button>
      {open ? (
        <ScrollArea viewportClassName="max-h-72" className="border-t">
          <div data-testid="cell-logs">
            <AnsiOutput
              output={logs}
              className="px-3 py-2 font-mono text-[11px] leading-5 whitespace-pre-wrap break-words"
            />
          </div>
        </ScrollArea>
      ) : null}
    </div>
  );
}

function NotebookDependenciesDialog({
  open,
  onOpenChange,
  dependencies,
  onSave,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  dependencies: string[];
  onSave: (dependencies: string[]) => void;
}) {
  const saved = useMemo(() => dependencies.join("\n"), [dependencies]);
  const [draft, setDraft] = useState(saved);
  // Re-sync the draft whenever the dialog opens or the saved value changes.
  useEffect(() => {
    if (open) {
      setDraft(saved);
    }
  }, [open, saved]);

  const count = useMemo(() => draft.split("\n").filter((line) => line.trim()).length, [draft]);
  const dirty = draft !== saved;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Package className="size-4 text-primary" />
            Python dependencies
          </DialogTitle>
          <DialogDescription>
            One package per line. Managed with uv in the notebook&apos;s pyproject.toml and
            installed on the next run.
          </DialogDescription>
        </DialogHeader>
        <textarea
          autoFocus
          value={draft}
          spellCheck={false}
          aria-label="dependencies"
          placeholder="pandas&#10;duckdb&#10;requests==2.31.0"
          onChange={(event) => setDraft(event.target.value)}
          rows={10}
          className="w-full resize-y rounded-lg border bg-background p-3 font-mono text-xs leading-5 outline-none focus-visible:ring-2 focus-visible:ring-ring"
        />
        <DialogFooter className="items-center sm:justify-between">
          <span className="text-[11px] text-muted-foreground">
            {count} package{count === 1 ? "" : "s"}
          </span>
          <div className="flex gap-2">
            <Button size="sm" variant="outline" onClick={() => onOpenChange(false)}>
              Close
            </Button>
            <Button
              size="sm"
              disabled={!dirty}
              onClick={() => {
                onSave(
                  draft
                    .split("\n")
                    .map((line) => line.trim())
                    .filter(Boolean),
                );
                onOpenChange(false);
              }}
            >
              <Check className="size-3.5" />
              Save
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function PromoteCellDialog({
  cell,
  cells,
  notebookId,
  notebookRevision,
  pipelines,
  onOpenChange,
  onPromote,
}: {
  cell: WebAsset | null;
  cells: WebAsset[];
  notebookId: string;
  notebookRevision: string;
  pipelines: Array<{ id: string; name: string }>;
  onOpenChange: (open: boolean) => void;
  onPromote: (
    cell: WebAsset,
    input: {
      pipeline_id: string;
      target_name: string;
      include_upstream: boolean;
      include_downstream: boolean;
      base_revision: string;
    },
  ) => Promise<void>;
}) {
  const [pipelineId, setPipelineId] = useState("");
  const [targetName, setTargetName] = useState("");
  const [includeUpstream, setIncludeUpstream] = useState(false);
  const [includeDownstream, setIncludeDownstream] = useState(false);
  const [plan, setPlan] = useState<PromoteCellPlan | null>(null);
  const [planError, setPlanError] = useState("");
  const [planning, setPlanning] = useState(false);
  const [applying, setApplying] = useState(false);

  // Whether the cell has upstream/downstream sibling cells, so the options are
  // only offered when they would actually pull anything in.
  const { hasUpstream, hasDownstream } = useMemo(() => {
    if (!cell) {
      return { hasUpstream: false, hasDownstream: false };
    }
    const cellNames = new Set(cells.map((candidate) => candidate.name.toLowerCase()));
    const up = (cell.upstreams ?? []).some((upstream) => cellNames.has(upstream.toLowerCase()));
    const down = cells.some(
      (candidate) =>
        candidate.cell_id !== cell.cell_id &&
        (candidate.upstreams ?? []).some(
          (upstream) => upstream.toLowerCase() === cell.name.toLowerCase(),
        ),
    );
    return { hasUpstream: up, hasDownstream: down };
  }, [cell, cells]);

  // Re-seed the form each time a new cell opens the dialog.
  useEffect(() => {
    if (!cell) {
      return;
    }
    setPipelineId(pipelines[0]?.id ?? "");
    setTargetName(`marts.${cell.name}`);
    setIncludeUpstream(false);
    setIncludeDownstream(false);
    setPlan(null);
    setPlanError("");
    setApplying(false);
  }, [cell, pipelines]);

  useEffect(() => {
    const cellId = cell?.cell_id;
    const normalizedTarget = targetName.trim();
    if (!cellId || !pipelineId || !normalizedTarget) {
      setPlan(null);
      setPlanError("");
      setPlanning(false);
      return;
    }

    let cancelled = false;
    setPlan(null);
    setPlanError("");
    setPlanning(true);
    const timer = window.setTimeout(() => {
      void planNotebookCellPromotion(notebookId, cellId, {
        pipeline_id: pipelineId,
        target_name: normalizedTarget,
        include_upstream: includeUpstream,
        include_downstream: includeDownstream,
        base_revision: notebookRevision,
      })
        .then((nextPlan) => {
          if (!cancelled) setPlan(nextPlan);
        })
        .catch((error) => {
          if (!cancelled) setPlanError(error instanceof Error ? error.message : String(error));
        })
        .finally(() => {
          if (!cancelled) setPlanning(false);
        });
    }, 250);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [
    cell?.cell_id,
    includeDownstream,
    includeUpstream,
    notebookId,
    notebookRevision,
    pipelineId,
    targetName,
  ]);

  const canSubmit = !!cell && !!plan?.can_apply && !planning && !applying;

  return (
    <Dialog open={!!cell} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ArrowUpFromLine className="size-4 text-primary" />
            Promote to pipeline
          </DialogTitle>
          <DialogDescription>
            Move {cell ? <span className="font-mono">{cell.name}</span> : "this block"} into a
            pipeline as a durable asset. Review the generated asset type, connection, and
            materialization before applying the Git-tracked file changes.
          </DialogDescription>
        </DialogHeader>

        <ScrollArea className="max-h-[min(65vh,36rem)]" viewportClassName="pr-3">
          <div className="space-y-4">
            {pipelines.length > 1 ? (
              <div className="space-y-1.5">
                <Label htmlFor="promote-pipeline">Pipeline</Label>
                <select
                  id="promote-pipeline"
                  value={pipelineId}
                  onChange={(event) => setPipelineId(event.target.value)}
                  className="w-full rounded-lg border bg-background px-3 py-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  {pipelines.map((pipeline) => (
                    <option key={pipeline.id} value={pipeline.id}>
                      {pipeline.name}
                    </option>
                  ))}
                </select>
              </div>
            ) : null}

            <div className="space-y-1.5">
              <Label htmlFor="promote-name">Target asset name</Label>
              <input
                id="promote-name"
                autoFocus
                value={targetName}
                spellCheck={false}
                placeholder="schema.table"
                onChange={(event) => setTargetName(event.target.value)}
                className="w-full rounded-lg border bg-background px-3 py-2 font-mono text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
              />
            </div>

            {hasUpstream || hasDownstream ? (
              <div className="space-y-2 rounded-lg border bg-muted/30 p-3">
                <div className="text-[11px] font-medium text-muted-foreground">
                  Also promote connected cells
                </div>
                {hasUpstream ? (
                  <label className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={includeUpstream}
                      onCheckedChange={(value) => setIncludeUpstream(value === true)}
                    />
                    Upstream assets (its sources)
                  </label>
                ) : null}
                {hasDownstream ? (
                  <label className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={includeDownstream}
                      onCheckedChange={(value) => setIncludeDownstream(value === true)}
                    />
                    Downstream assets (what depends on it)
                  </label>
                ) : null}
                <p className="text-[11px] text-muted-foreground">
                  Connected cells are named in the same schema (e.g.{" "}
                  <span className="font-mono">marts.&lt;cell&gt;</span>).
                </p>
              </div>
            ) : null}

            <div className="space-y-2" aria-label="Promotion preview">
              <div className="flex items-center justify-between text-[11px] font-medium text-muted-foreground">
                <span>Promotion preview</span>
                {plan ? <span>{plan.files.length} file changes</span> : null}
              </div>
              {planning ? (
                <div className="flex min-h-20 items-center justify-center gap-2 rounded-lg border bg-muted/20 text-xs text-muted-foreground">
                  <Spinner aria-label="Planning promotion" />
                  Resolving pipeline consequences…
                </div>
              ) : planError ? (
                <div className="rounded-lg border border-red-500/25 bg-red-500/10 px-3 py-2 text-xs text-red-700 dark:text-red-200">
                  {planError}
                </div>
              ) : plan ? (
                <div className="space-y-2">
                  <div className="divide-y rounded-lg border bg-muted/20">
                    {plan.assets.map((asset) => (
                      <div
                        key={asset.cell_id}
                        className="grid min-w-0 gap-1 px-3 py-2 text-xs sm:grid-cols-[minmax(0,1fr)_auto]"
                      >
                        <div className="min-w-0">
                          <p className="truncate font-mono font-medium" title={asset.target_name}>
                            {asset.target_name}
                          </p>
                          <p
                            className="truncate text-[11px] text-muted-foreground"
                            title={asset.path}
                          >
                            {asset.path}
                          </p>
                        </div>
                        <div className="flex flex-wrap items-center gap-1 sm:justify-end">
                          <Badge variant="secondary" className="font-mono text-[10px]">
                            {asset.asset_type}
                          </Badge>
                          {asset.source_connection ? (
                            <Badge variant="outline" className="max-w-56 font-mono text-[10px]">
                              <span className="truncate">
                                {asset.source_connection} → {asset.connection}
                              </span>
                            </Badge>
                          ) : asset.connection ? (
                            <Badge variant="outline" className="max-w-48 font-mono text-[10px]">
                              <span className="truncate">{asset.connection}</span>
                            </Badge>
                          ) : null}
                          <Badge variant="outline" className="text-[10px]">
                            {asset.materialization}
                          </Badge>
                        </div>
                      </div>
                    ))}
                  </div>
                  {plan.warnings?.map((warning) => (
                    <div
                      key={warning}
                      className="flex gap-2 rounded-lg border border-amber-500/25 bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-200"
                    >
                      <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
                      <span>{warning}</span>
                    </div>
                  ))}
                </div>
              ) : (
                <div className="min-h-20 rounded-lg border border-dashed bg-muted/10" />
              )}
            </div>
          </div>
        </ScrollArea>

        <DialogFooter>
          <Button
            size="sm"
            variant="outline"
            disabled={applying}
            onClick={() => onOpenChange(false)}
          >
            Cancel
          </Button>
          <Button
            size="sm"
            disabled={!canSubmit}
            onClick={() => {
              if (!cell || !plan) {
                return;
              }
              setApplying(true);
              void onPromote(cell, {
                pipeline_id: pipelineId,
                target_name: targetName.trim(),
                include_upstream: includeUpstream,
                include_downstream: includeDownstream,
                base_revision: plan.base_revision,
              }).finally(() => setApplying(false));
            }}
          >
            {applying ? (
              <Spinner aria-label="Promoting block" />
            ) : (
              <ArrowUpFromLine className="size-3.5" />
            )}
            Promote
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function MarkdownBlockCard({
  markdown,
  onSave,
  onDelete,
}: {
  markdown: string;
  onSave: (markdown: string) => Promise<boolean>;
  onDelete: () => void;
}) {
  const [draft, setDraft] = useState(markdown);
  const [saving, setSaving] = useState(false);
  const lastSavedRef = useRef(markdown);
  useEffect(() => {
    setDraft((current) => {
      if (current !== lastSavedRef.current && current !== markdown) return current;
      lastSavedRef.current = markdown;
      return markdown;
    });
  }, [markdown]);

  const commit = async () => {
    if (saving || draft === lastSavedRef.current) return;
    const submitted = draft;
    setSaving(true);
    const saved = await onSave(submitted);
    if (saved) lastSavedRef.current = submitted;
    setSaving(false);
  };

  return (
    <section className="group/markdown-block relative rounded-xl border border-transparent transition-colors hover:border-border focus-within:border-primary/30">
      <MarkdownEditor
        value={draft}
        ariaLabel="Markdown cell"
        placeholder="Write a note…"
        className="border-0 hover:border-transparent focus-within:border-transparent"
        actions={
          <>
            {draft !== lastSavedRef.current ? (
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label="Save text block"
                title="Save text"
                disabled={saving}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => void commit()}
              >
                {saving ? <Spinner aria-label="Saving text block" /> : <Check />}
              </Button>
            ) : null}
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Delete text block"
              title="Delete block"
              onMouseDown={(event) => event.preventDefault()}
              onClick={onDelete}
            >
              <Trash2 />
            </Button>
          </>
        }
        onChange={setDraft}
        onBlur={() => void commit()}
      />
    </section>
  );
}

function useWideNotebookTools() {
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
