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
  Globe2,
  Loader2,
  MoreHorizontal,
  Package,
  Pencil,
  Play,
  Plus,
  RotateCw,
  SlidersHorizontal,
  Square,
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
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
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
  createNotebookMarkdown,
  createNotebookVisualization,
  createNotebookCell,
  createNotebookSource,
  createNotebookWarehouseSource,
  deleteNotebookBlock,
  deleteNotebook,
  deleteNotebookCell,
  getNotebook,
  getNotebookRuntime,
  joinCellContent,
  migrateLegacyNotebookVisualization,
  notebookCellExportURL,
  NotebookCellRunResult,
  promoteNotebookCell,
  replaceNotebookParameters,
  renameNotebookCell,
  runNotebook,
  setNotebookSettings,
  splitCellContent,
  updateNotebookCell,
  updateNotebookBlocks,
  updateNotebookDependencies,
  updateNotebookMarkdown,
  updateNotebookVisualization,
  upgradeNotebookManifest,
} from "@/lib/api-notebooks";
import { notebookRuntimeEventsAtom } from "@/lib/atoms/domains/results";
import {
  selectedEnvironmentAtom,
  selectedExecutionTimeWindowAtom,
  workspaceAtom,
} from "@/lib/atoms/domains/workspace";
import { sqlDiscoveryTablesAtom } from "@/lib/atoms/sql-discovery";
import { usesPythonSource } from "@/lib/asset-types";
import { addDependency, missingPythonImports } from "@/lib/notebook-python-deps";
import { WebAsset, WebNotebook, WebNotebookBlock, WorkspaceQueryConnection } from "@/lib/types";
import { cn } from "@/lib/utils";

import { MissingPythonDepsBanner } from "./missing-python-deps";
import { NewNotebookDialog } from "./new-notebook-dialog";
import { buildNotebookSchemaTables, NotebookCellMonaco } from "./notebook-cell-editor";
import { NotebookVizRenderer } from "./notebook-viz";
import { NotebookVisualizationBlockCard } from "./notebook-visualization-block";
import { NotebookParametersDialog } from "./notebook-parameters-dialog";
import { LoadStreamPicker } from "./load-stream-picker";
import { PageHeader, AppPage, AppPanel, SimpleTable } from "./app-primitives";

const ReactMarkdown = lazy(() => import("react-markdown"));

const RESULT_DISPLAY_CAP = 50;
// How long to wait after the last keystroke before auto-committing a cell's
// draft. The save marks the cell stale on the server, which drives recompute.
const AUTO_COMMIT_DEBOUNCE_MS = 350;
const NOTEBOOK_CELL_JUMP_HIGHLIGHT_MS = 1600;
const NOTEBOOK_BLOCK_ENTER_ANIMATION =
  "animate-in fade-in-0 slide-in-from-bottom-2 duration-300 motion-reduce:animate-none";

type PendingNotebookBlockKind = "sql" | "python" | "markdown" | "visualization";
type NotebookCellDeleteTarget = { id: string; name: string };

function notebookBlockKey(block: WebNotebookBlock, index: number) {
  if (block.cell) {
    return `cell:${block.cell}`;
  }
  if (block.id) {
    return `block:${block.id}`;
  }
  return `legacy-markdown:${index}`;
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
  const [parametersOpen, setParametersOpen] = useState(false);
  const [addDataOpen, setAddDataOpen] = useState(false);
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
  const [parameterValues, setParameterValues] = useState<Record<string, unknown>>({});
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
    setParameterValues({});
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
      const existingBlockIDs = new Set(notebook.blocks.map((block) => block.id).filter(Boolean));
      setPendingBlock({ id, kind });
      setEnteringBlockKey(null);
      setScrollRevision((current) => current + 1);

      const updated = await mutateWithResult(() => {
        if (kind === "markdown") {
          if (notebook.manifest_version < 2) {
            return updateNotebookBlocks(notebookId, [...notebook.blocks, { markdown: "## Notes" }]);
          }
          const lastBlock = notebook.blocks.at(-1);
          return createNotebookMarkdown(notebookId, {
            content: "## Notes",
            after_block_id: lastBlock
              ? notebookBlockKey(lastBlock, notebook.blocks.length - 1)
              : undefined,
          });
        }
        if (kind === "visualization") {
          const sourceBlock = [...notebook.blocks].reverse().find((block) => block.cell);
          if (!sourceBlock?.cell) {
            throw new Error("Add a data-producing cell before creating a visualization.");
          }
          return createNotebookVisualization(notebookId, {
            source: sourceBlock.cell,
            definition: { version: 1, type: "table", presentation_limit: 200 },
            after_block_id: sourceBlock.cell,
          });
        }
        return createNotebookCell(notebookId, kind === "python" ? { language: "python" } : {});
      });

      if (updated) {
        if (kind === "markdown") {
          const blockIndex = updated.blocks.length - 1;
          setEnteringBlockKey(notebookBlockKey(updated.blocks[blockIndex], blockIndex));
        } else if (kind === "visualization") {
          const createdBlock = updated.blocks.find(
            (block) => block.visualization && block.id && !existingBlockIDs.has(block.id),
          );
          if (createdBlock?.id) setEnteringBlockKey(`block:${createdBlock.id}`);
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
              <Button variant="outline" size="sm" onClick={() => setAddDataOpen(true)}>
                <Database className="size-3.5" />
                <span className="hidden sm:inline">Add data</span>
              </Button>
              <Button
                variant="outline"
                size="sm"
                aria-label="Notebook parameters"
                onClick={() => setParametersOpen(true)}
              >
                <SlidersHorizontal className="size-3.5" />
                <span className="hidden sm:inline">Parameters</span>
                {(notebook.parameters?.length ?? 0) > 0 ? (
                  <span className="text-[10px] text-muted-foreground">
                    {notebook.parameters?.length}
                  </span>
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
                    {cell.notebook_source ? (
                      <NotebookSourceCard
                        notebookId={notebookId}
                        cell={cell}
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
                        onDelete={() => setCellToDelete({ id: block.cell ?? "", name: cell.name })}
                        onRename={(name) =>
                          mutate(() => renameNotebookCell(notebookId, block.cell ?? "", name))
                        }
                        onPromote={() => void promoteCell(cell)}
                        onSaveBody={(body, baseRevision) => saveCellBody(cell, body, baseRevision)}
                        autoCommit={autoRecompute}
                        pendingAuto={autoPending.has(block.cell ?? "")}
                        queryConnections={workspace?.query_connections ?? []}
                        onConfigureSource={(input) => configureCellSource(block.cell ?? "", input)}
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
                          if (migrated?.id) setEnteringBlockKey(`block:${migrated.id}`);
                        }}
                        onGoToAsset={goToAsset}
                        onGoToCell={goToCell}
                      />
                    )}
                  </div>
                );
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
                  onSave={(markdown) => {
                    if (!block.id) {
                      const blocks: WebNotebookBlock[] = notebook.blocks.map(
                        (candidate, candidateIndex) =>
                          candidateIndex === index ? { ...candidate, markdown } : candidate,
                      );
                      void mutate(() => updateNotebookBlocks(notebookId, blocks));
                      return;
                    }
                    void mutate(() => updateNotebookMarkdown(notebookId, block.id!, markdown));
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
            <Button
              variant="outline"
              size="sm"
              disabled={
                pendingBlock !== null ||
                notebook.cells.length === 0 ||
                notebook.manifest_version < 2
              }
              onClick={() => void createBlockAtBottom("visualization")}
            >
              <Plus className="size-3.5" />
              Visualization
            </Button>
          </div>
        </div>
      </ScrollArea>
    </AppPage>
  );
}

function PendingNotebookBlock({ kind }: { kind: PendingNotebookBlockKind }) {
  const label =
    kind === "markdown"
      ? "Markdown block"
      : kind === "visualization"
        ? "visualization"
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
                    <Select
                      value={connection}
                      onValueChange={(value) => {
                        setConnection(value);
                        setRelation("");
                      }}
                    >
                      <SelectTrigger id="notebook-source-connection" className="w-full">
                        <SelectValue placeholder="Choose a connection" />
                      </SelectTrigger>
                      <SelectContent>
                        {queryConnections.map((candidate) => (
                          <SelectItem key={candidate.name} value={candidate.name}>
                            {candidate.name} · {candidate.connection_type}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
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
                <Select
                  value={fileConnection}
                  onValueChange={(value) => {
                    setFileConnection(value);
                    setFileURI("");
                  }}
                >
                  <SelectTrigger id="notebook-file-connection" className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="__local__">Workspace file</SelectItem>
                    {storageConnections.map(([name, type]) => (
                      <SelectItem key={name} value={name}>
                        {name} · {type}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
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

function downloadNotebookCell(notebookId: string, cellId: string, format: "csv" | "parquet") {
  const anchor = document.createElement("a");
  anchor.href = notebookCellExportURL(notebookId, cellId, format);
  anchor.click();
}

function NotebookSourceCard({
  notebookId,
  cell,
  result,
  stale,
  running,
  busy,
  onRun,
  onCancel,
  onRunFromHere,
  onDelete,
  onRename,
}: {
  notebookId: string;
  cell: WebAsset;
  result?: NotebookCellRunResult;
  stale: boolean;
  running: boolean;
  busy: boolean;
  onRun: () => void;
  onCancel: () => void;
  onRunFromHere: () => void;
  onDelete: () => void;
  onRename: (name: string) => Promise<void>;
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
  const rowsShown = Math.min(result?.rows?.length ?? 0, RESULT_DISPLAY_CAP);

  return (
    <AppPanel className="border-cyan-500/25 border-l-2 border-l-cyan-500/70 bg-cyan-500/[0.025]">
      <DelimitedCardHeader className={cn(stale && "notebook-stale-hatch")}>
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
        {result?.snapshot ? (
          <Badge
            variant="outline"
            className="text-[10px] text-cyan-700 dark:text-cyan-300"
            title={result.snapshot.imported_at}
          >
            {result.snapshot.row_count.toLocaleString()} rows ·{" "}
            {formatNotebookBytes(result.snapshot.byte_count)}
          </Badge>
        ) : null}
        <span className="ml-auto text-[11px] text-muted-foreground">
          {running ? "refreshing…" : result?.status === "ok" ? `${result.duration_ms} ms` : null}
        </span>
        {running ? (
          <Button variant="ghost" size="icon-sm" onClick={onCancel} title="Stop refresh">
            <Square className="size-3.5 fill-current" />
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
        {result?.snapshot?.warnings?.map((warning) => (
          <p
            key={warning}
            className="rounded-lg border border-amber-500/25 bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-200"
          >
            {warning}
          </p>
        ))}
        {result?.status === "error" || result?.status === "blocked" ? (
          <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 font-mono text-xs text-red-800 dark:border-red-500/25 dark:bg-red-500/10 dark:text-red-200">
            {result.error}
          </div>
        ) : null}
        {result?.status === "ok" && result.columns.length > 0 ? (
          <div className="overflow-hidden rounded-lg border">
            <SimpleTable
              viewportClassName="max-h-72"
              columns={result.columns}
              rows={result.rows
                .slice(0, RESULT_DISPLAY_CAP)
                .map((row) => row.map((value) => (value == null ? "" : String(value))))}
            />
            {result.total_rows > rowsShown ? (
              <div className="border-t bg-muted/30 px-2 py-1 text-[11px] text-muted-foreground">
                showing {rowsShown} of {result.total_rows} rows
              </div>
            ) : null}
          </div>
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
  const rowsShown = Math.min(result?.rows?.length ?? 0, RESULT_DISPLAY_CAP);
  // Only surface staleness for cells the user must act on. A cell auto-recompute
  // is about to refresh is left unmarked — flagging it would just flicker.
  const showStale = stale && !pendingAuto;

  return (
    <AppPanel
      className={cn(
        sourceConnection && "border-primary/25 border-l-2 border-l-primary/60 bg-primary/[0.02]",
      )}
    >
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
        {!isPythonCell && queryConnections.length > 0 ? (
          <Select
            value={sourceConnection || "__local__"}
            disabled={busy || sourceChanging}
            onValueChange={(value) => {
              const connection = value === "__local__" ? undefined : value;
              setSourceChanging(true);
              void onConfigureSource({
                connection,
                snapshot_mode: connection ? snapshotMode : undefined,
                row_limit: connection && snapshotMode === "sample" ? snapshotRowLimit : undefined,
              }).finally(() => setSourceChanging(false));
            }}
          >
            <SelectTrigger size="sm" aria-label="Source connection" className="max-w-52">
              {sourceChanging ? <Loader2 className="animate-spin" /> : <Database />}
              <SelectValue />
            </SelectTrigger>
            <SelectContent align="start">
              <SelectGroup>
                <SelectLabel>Execution context</SelectLabel>
                <SelectItem value="__local__">Local notebook DuckDB</SelectItem>
                {sourceConnection &&
                !queryConnections.some((connection) => connection.name === sourceConnection) ? (
                  <SelectItem value={sourceConnection}>{sourceConnection} (unavailable)</SelectItem>
                ) : null}
                {queryConnections.map((connection) => (
                  <SelectItem key={connection.name} value={connection.name}>
                    {connection.name}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
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
        {result?.snapshot ? (
          <span
            title={`${result.snapshot.environment ?? "default"} · ${result.snapshot.row_count.toLocaleString()} rows · ${formatNotebookBytes(result.snapshot.byte_count)} · ${result.snapshot.imported_at}`}
            className="rounded bg-primary/10 px-1.5 py-0.5 text-[10px] text-primary"
          >
            {result.snapshot.sampled ? "sampled" : "snapshotted"} ·{" "}
            {formatNotebookBytes(result.snapshot.byte_count)}
          </span>
        ) : result?.sampled ? (
          <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-[10px] text-amber-700 dark:text-amber-200">
            derived from sample
          </span>
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
  }, [cell, pipelines]);

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
