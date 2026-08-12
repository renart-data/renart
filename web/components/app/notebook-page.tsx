"use client";

import { useNavigate } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import {
  AlertTriangle,
  AreaChart,
  ArrowUpFromLine,
  BarChart3,
  BookOpen,
  Check,
  ChevronRight,
  Database,
  Hash,
  LineChart,
  Loader2,
  MoreHorizontal,
  Package,
  Pencil,
  PieChart,
  Play,
  Plus,
  RotateCw,
  Square,
  Table2,
  Trash2,
} from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";

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
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import {
  cancelNotebookRun,
  closeNotebookSession,
  createNotebookCell,
  deleteNotebook,
  deleteNotebookCell,
  getNotebook,
  getNotebookRuntime,
  joinCellContent,
  NotebookCellRunResult,
  promoteNotebookCell,
  renameNotebookCell,
  runNotebook,
  setNotebookSettings,
  splitCellContent,
  updateNotebookBlocks,
  updateNotebookCell,
  updateNotebookDependencies,
  VizKind,
} from "@/lib/api-notebooks";
import { notebookRuntimeEventsAtom } from "@/lib/atoms/domains/results";
import {
  selectedEnvironmentAtom,
  selectedExecutionTimeWindowAtom,
  workspaceAtom,
} from "@/lib/atoms/domains/workspace";
import { usesPythonSource } from "@/lib/asset-types";
import { addDependency, missingPythonImports } from "@/lib/notebook-python-deps";
import { WebAsset, WebNotebook, WebNotebookBlock } from "@/lib/types";
import { cn } from "@/lib/utils";

import { MissingPythonDepsBanner } from "./missing-python-deps";
import { NewNotebookDialog } from "./new-notebook-dialog";
import { buildNotebookSchemaTables, NotebookCellMonaco } from "./notebook-cell-editor";
import { applyVizKind } from "./notebook-viz-directive";
import { NotebookVizRenderer } from "./notebook-viz";
import { PageHeader, AppPage, AppPanel, SimpleTable } from "./app-primitives";

const VIZ_KIND_ICONS: Record<VizKind, typeof Table2> = {
  table: Table2,
  bar: BarChart3,
  line: LineChart,
  area: AreaChart,
  pie: PieChart,
  kpi: Hash,
};

const ReactMarkdown = lazy(() => import("react-markdown"));

const RESULT_DISPLAY_CAP = 50;
// How long to wait after the last keystroke before auto-committing a cell's
// draft. The save marks the cell stale on the server, which drives recompute.
const AUTO_COMMIT_DEBOUNCE_MS = 350;
const NOTEBOOK_CELL_JUMP_HIGHLIGHT_MS = 1600;
const NOTEBOOK_BLOCK_ENTER_ANIMATION =
  "animate-in fade-in-0 slide-in-from-bottom-2 duration-300 motion-reduce:animate-none";

type PendingNotebookBlockKind = "sql" | "python" | "markdown";
type NotebookCellDeleteTarget = { id: string; name: string };

function notebookBlockKey(block: WebNotebookBlock, index: number) {
  return block.cell ? `cell:${block.cell}` : `markdown:${index}`;
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
    setMutated(null);
  }, [stateNotebook]);

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
  } | null>(null);
  const [enteringBlockKey, setEnteringBlockKey] = useState<string | null>(null);
  const [jumpHighlightedCellId, setJumpHighlightedCellId] = useState<string | null>(null);
  const [scrollRevision, setScrollRevision] = useState(0);
  const [cellToDelete, setCellToDelete] = useState<NotebookCellDeleteTarget | null>(null);
  const [deletingCell, setDeletingCell] = useState(false);
  const [depsOpen, setDepsOpen] = useState(false);
  const [promoting, setPromoting] = useState<WebAsset | null>(null);
  const notebookViewportRef = useRef<HTMLDivElement>(null);
  const pendingBlockSequenceRef = useRef(0);
  const jumpHighlightFrameRef = useRef<number | null>(null);
  const jumpHighlightTimerRef = useRef<number | null>(null);
  const [autoRecompute, setAutoRecompute] = useState(
    () =>
      typeof window === "undefined" ||
      window.localStorage.getItem("renart-notebook-autorecompute") !== "off",
  );
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
    if (jumpHighlightFrameRef.current !== null) {
      window.cancelAnimationFrame(jumpHighlightFrameRef.current);
      jumpHighlightFrameRef.current = null;
    }
    if (jumpHighlightTimerRef.current !== null) {
      window.clearTimeout(jumpHighlightTimerRef.current);
      jumpHighlightTimerRef.current = null;
    }
  }, [notebookId]);

  useEffect(
    () => () => {
      if (jumpHighlightFrameRef.current !== null) {
        window.cancelAnimationFrame(jumpHighlightFrameRef.current);
      }
      if (jumpHighlightTimerRef.current !== null) {
        window.clearTimeout(jumpHighlightTimerRef.current);
      }
    },
    [],
  );

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
    }) => {
      setStaleCells(new Set(runtime.stale));
      setAutoPending(new Set(runtime.auto_pending));
      if (runtime.running) {
        setRunningCells(new Set(runtime.running));
      }
      if (runtime.results && Object.keys(runtime.results).length > 0) {
        setResults((current) => ({ ...current, ...runtime.results }));
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

  const confirmDeleteCell = useCallback(async () => {
    if (!cellToDelete || deletingCell) {
      return;
    }
    setDeletingCell(true);
    await mutateWithResult(() => deleteNotebookCell(notebookId, cellToDelete.id));
    setDeletingCell(false);
    setCellToDelete(null);
  }, [cellToDelete, deletingCell, mutateWithResult, notebookId]);

  const createBlockAtBottom = useCallback(
    async (kind: PendingNotebookBlockKind) => {
      if (!notebook || pendingBlock) {
        return;
      }

      const id = ++pendingBlockSequenceRef.current;
      const existingCellIDs = new Set(
        notebook.cells.map((cell) => cell.cell_id).filter((cellID): cellID is string => !!cellID),
      );
      setPendingBlock({ id, kind });
      setEnteringBlockKey(null);
      setScrollRevision((current) => current + 1);

      const updated = await mutateWithResult(() => {
        if (kind === "markdown") {
          const blocks: WebNotebookBlock[] = [...notebook.blocks, { markdown: "## Notes" }];
          return updateNotebookBlocks(notebookId, blocks);
        }
        return createNotebookCell(notebookId, kind === "python" ? { language: "python" } : {});
      });

      if (updated) {
        if (kind === "markdown") {
          setEnteringBlockKey(`markdown:${updated.blocks.length - 1}`);
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
      setScrollRevision((current) => current + 1);
    },
    [mutateWithResult, notebook, notebookId, pendingBlock],
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
                  <DropdownMenuCheckboxItem
                    checked={autoRecompute}
                    onCheckedChange={(checked) => setAutoRecompute(checked === true)}
                    onSelect={(event) => event.preventDefault()}
                  >
                    Auto-recompute stale cells
                  </DropdownMenuCheckboxItem>
                  <DropdownMenuSeparator />
                  <DropdownMenuItem
                    disabled={busy}
                    onSelect={() =>
                      void runRequest({ all: true, refresh_imports: true }, allCellIds)
                    }
                  >
                    <RotateCw className="size-4" />
                    Run all, refresh imports
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
                    <Database className="size-4" />
                    Reset session (delete local DB)
                  </DropdownMenuItem>
                  <DropdownMenuSeparator />
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
                    <Trash2 className="size-4" />
                    Delete notebook
                  </DropdownMenuItem>
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

      <PromoteCellDialog
        cell={promoting}
        cells={notebook.cells}
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
      <ScrollArea
        className="min-h-0 flex-1"
        viewportClassName="px-3 pb-24"
        viewportRef={notebookViewportRef}
        onViewportScroll={(event) => {
          const nextScrolled = event.currentTarget.scrollTop > 0;
          setNotebookScrolled((current) => (current === nextScrolled ? current : nextScrolled));
        }}
      >
        <div className="mx-auto flex max-w-5xl flex-col gap-3">
          {notebook.blocks.map((block, index) => {
            const blockKey = notebookBlockKey(block, index);
            const entering = blockKey === enteringBlockKey;
            return block.cell ? (
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
                    <NotebookCellCard
                      cell={cell}
                      cells={notebook.cells}
                      dependencies={dependencies}
                      installedModules={installedModules}
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
                      onDelete={() => setCellToDelete({ id: block.cell ?? "", name: cell.name })}
                      onRename={(name) =>
                        mutate(() => renameNotebookCell(notebookId, block.cell ?? "", name))
                      }
                      onPromote={() => void promoteCell(cell)}
                      onSaveBody={(body, baseRevision) => saveCellBody(cell, body, baseRevision)}
                      autoCommit={autoRecompute}
                      pendingAuto={autoPending.has(block.cell ?? "")}
                      onGoToAsset={goToAsset}
                      onGoToCell={goToCell}
                    />
                  </div>
                );
              })()
            ) : (
              <div
                key={`md-${index}`}
                data-notebook-markdown-index={index}
                data-notebook-block-entering={entering || undefined}
                className={cn(entering && NOTEBOOK_BLOCK_ENTER_ANIMATION)}
              >
                <MarkdownBlockCard
                  markdown={block.markdown ?? ""}
                  onSave={(markdown) => {
                    const blocks: WebNotebookBlock[] = notebook.blocks.map(
                      (candidate, candidateIndex) =>
                        candidateIndex === index ? { markdown } : candidate,
                    );
                    void mutate(() => updateNotebookBlocks(notebookId, blocks));
                  }}
                  onDelete={() => {
                    const blocks = notebook.blocks.filter(
                      (_, candidateIndex) => candidateIndex !== index,
                    );
                    void mutate(() => updateNotebookBlocks(notebookId, blocks));
                  }}
                />
              </div>
            );
          })}

          {pendingBlock ? <PendingNotebookBlock kind={pendingBlock.kind} /> : null}

          <div className="flex gap-2" aria-busy={pendingBlock !== null}>
            <Button
              variant="outline"
              size="sm"
              disabled={pendingBlock !== null}
              onClick={() => void createBlockAtBottom("sql")}
            >
              <Plus className="size-3.5" />
              SQL cell
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={pendingBlock !== null}
              onClick={() => void createBlockAtBottom("python")}
            >
              <Plus className="size-3.5" />
              Python cell
            </Button>
            <Button
              variant="outline"
              size="sm"
              disabled={pendingBlock !== null}
              onClick={() => void createBlockAtBottom("markdown")}
            >
              <Plus className="size-3.5" />
              Markdown
            </Button>
          </div>
        </div>
      </ScrollArea>
    </AppPage>
  );
}

function PendingNotebookBlock({ kind }: { kind: PendingNotebookBlockKind }) {
  const label = kind === "markdown" ? "Markdown block" : `${kind.toUpperCase()} cell`;

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

function NotebookCellCard({
  cell,
  cells,
  dependencies,
  installedModules,
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
  onGoToAsset,
  onGoToCell,
}: {
  cell: WebAsset;
  cells: WebAsset[];
  dependencies: string[];
  installedModules: string[];
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
  onGoToAsset?: (pipelineId: string, assetId: string) => void;
  onGoToCell?: (cellId: string) => void;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const schemaTables = useMemo(
    () => buildNotebookSchemaTables(workspace, cells, cell, resultColumnsByCell),
    [workspace, cells, cell, resultColumnsByCell],
  );
  const isPythonCell = usesPythonSource(cell);
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

  // Chart type is a view over the @viz directive line: changing it rewrites
  // the cell body (text stays the source of truth, invariant 3).
  const setChartKind = (kind: VizKind) => {
    const next = applyVizKind(draft, kind, result?.columns ?? []);
    setDraft(next);
    savingBodyRef.current = next;
    void onSaveBody(next, lastSavedRevisionRef.current).finally(() => {
      if (savingBodyRef.current === next) {
        savingBodyRef.current = null;
      }
    });
  };

  const vizDiagnostics = result?.viz_diagnostics ?? [];
  const rowsShown = Math.min(result?.rows?.length ?? 0, RESULT_DISPLAY_CAP);
  // Only surface staleness for cells the user must act on. A cell auto-recompute
  // is about to refresh is left unmarked — flagging it would just flicker.
  const showStale = stale && !pendingAuto;

  return (
    <AppPanel>
      <DelimitedCardHeader className={cn(showStale && "notebook-stale-hatch")}>
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
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onSelect={onDelete}>
              <Trash2 className="size-4" />
              Delete cell
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </DelimitedCardHeader>
      <DelimitedCardContent className="space-y-3">
        <NotebookCellMonaco
          cell={cell}
          value={draft}
          schemaTables={schemaTables}
          resultColumns={result?.columns ?? []}
          onChange={setDraft}
          onCommit={commit}
          onRun={onRun}
          onRename={() => setRenaming(true)}
          onGoToAsset={onGoToAsset}
          onGoToCell={onGoToCell}
        />
        <MissingPythonDepsBanner missingImports={missingDeps} onAddDependency={onAddDependency} />
        {result?.status === "error" ? (
          <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 font-mono text-xs text-red-800 dark:border-red-500/25 dark:bg-red-500/10 dark:text-red-200">
            {result.error}
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
        {result?.status === "ok" && result.columns.length > 0 && !isPythonCell ? (
          <div className="flex items-center gap-1">
            <span className="mr-1 text-[11px] text-muted-foreground">View</span>
            {(["table", "bar", "line", "area", "pie", "kpi"] as VizKind[]).map((kind) => {
              const Icon = VIZ_KIND_ICONS[kind];
              const active = (result.viz?.kind ?? "table") === kind;
              return (
                <Button
                  key={kind}
                  variant={active ? "secondary" : "ghost"}
                  size="icon-sm"
                  title={kind}
                  onClick={() => setChartKind(kind)}
                >
                  <Icon className="size-3.5" />
                </Button>
              );
            })}
          </div>
        ) : null}
        {result?.status === "ok" && result.columns.length > 0 ? (
          result.viz && result.viz.kind !== "table" ? (
            <NotebookVizRenderer result={result} />
          ) : (
            <div className="overflow-hidden rounded-lg border">
              <SimpleTable
                viewportClassName="max-h-72"
                columns={result.columns}
                rows={result.rows
                  .slice(0, RESULT_DISPLAY_CAP)
                  .map((row) =>
                    row.map((value) =>
                      value === null || value === undefined ? "" : String(value),
                    ),
                  )}
              />
              {result.rows.length > rowsShown || result.total_rows > result.rows.length ? (
                <div className="border-t bg-muted/30 px-2 py-1 text-[11px] text-muted-foreground">
                  showing {rowsShown} of {result.total_rows} rows
                </div>
              ) : null}
            </div>
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
          <pre
            data-testid="cell-logs"
            className="px-3 py-2 font-mono text-[11px] leading-5 whitespace-pre-wrap break-words"
          >
            {logs}
          </pre>
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
      <DialogContent className="max-w-lg">
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
  pipelines,
  onOpenChange,
  onPromote,
}: {
  cell: WebAsset | null;
  cells: WebAsset[];
  pipelines: Array<{ id: string; name: string }>;
  onOpenChange: (open: boolean) => void;
  onPromote: (
    cell: WebAsset,
    input: {
      pipeline_id: string;
      target_name: string;
      include_upstream: boolean;
      include_downstream: boolean;
    },
  ) => void;
}) {
  const [pipelineId, setPipelineId] = useState("");
  const [targetName, setTargetName] = useState("");
  const [includeUpstream, setIncludeUpstream] = useState(false);
  const [includeDownstream, setIncludeDownstream] = useState(false);
  const defaultPipelineId = pipelines[0]?.id ?? "";

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

  // Re-seed the form each time a new cell opens the dialog. Depend on the
  // default pipeline's stable id rather than the pipelines array: the parent
  // maps that array during render, and unrelated workspace updates must not
  // wipe choices the user already made in this open dialog.
  useEffect(() => {
    if (!cell) {
      return;
    }
    setPipelineId(defaultPipelineId);
    setTargetName(`marts.${cell.name}`);
    setIncludeUpstream(false);
    setIncludeDownstream(false);
  }, [cell, defaultPipelineId]);

  const canSubmit = !!cell && !!pipelineId && targetName.trim().length > 0;

  return (
    <Dialog open={!!cell} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <ArrowUpFromLine className="size-4 text-primary" />
            Promote to pipeline
          </DialogTitle>
          <DialogDescription>
            Move {cell ? <span className="font-mono">{cell.name}</span> : "this cell"} into a
            pipeline as a real asset. Cells left behind that referenced it are rewritten to read the
            new asset.
          </DialogDescription>
        </DialogHeader>

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
        </div>

        <DialogFooter>
          <Button size="sm" variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            size="sm"
            disabled={!canSubmit}
            onClick={() => {
              if (!cell) {
                return;
              }
              onPromote(cell, {
                pipeline_id: pipelineId,
                target_name: targetName.trim(),
                include_upstream: includeUpstream,
                include_downstream: includeDownstream,
              });
            }}
          >
            <ArrowUpFromLine className="size-3.5" />
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
  onSave: (markdown: string) => void;
  onDelete: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(markdown);
  useEffect(() => {
    setDraft(markdown);
  }, [markdown]);

  return (
    <AppPanel>
      <DelimitedCardHeader>
        <BookOpen className="size-4 text-primary" />
        <DelimitedCardTitle>Markdown</DelimitedCardTitle>
        <span className="ml-auto" />
        {editing ? (
          <Button
            variant="ghost"
            size="icon-sm"
            title="Save"
            onClick={() => {
              setEditing(false);
              if (draft !== markdown) onSave(draft);
            }}
          >
            <Check className="size-3.5" />
          </Button>
        ) : (
          <Button variant="ghost" size="icon-sm" title="Edit" onClick={() => setEditing(true)}>
            <Pencil className="size-3.5" />
          </Button>
        )}
        <Button variant="ghost" size="icon-sm" title="Delete block" onClick={onDelete}>
          <Trash2 className="size-3.5" />
        </Button>
      </DelimitedCardHeader>
      <DelimitedCardContent>
        {editing ? (
          <textarea
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            rows={Math.min(Math.max(draft.split("\n").length, 3), 16)}
            className="w-full resize-y rounded-lg border bg-background p-3 font-mono text-xs leading-5 outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        ) : (
          <article className="prose prose-sm max-w-none text-sm leading-6 text-foreground [&_h1]:mb-2 [&_h1]:text-xl [&_h1]:font-semibold [&_h2]:mb-2 [&_h2]:text-lg [&_h2]:font-semibold [&_p]:mb-2 [&_ul]:list-disc [&_ul]:pl-5">
            <Suspense fallback={<span className="text-muted-foreground">…</span>}>
              <ReactMarkdown>{markdown || "*empty*"}</ReactMarkdown>
            </Suspense>
          </article>
        )}
      </DelimitedCardContent>
    </AppPanel>
  );
}
