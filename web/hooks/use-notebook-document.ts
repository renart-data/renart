"use client";

import { useCallback, useEffect, useMemo, useReducer, useRef } from "react";

import {
  getNotebook,
  joinCellContent,
  splitCellContent,
  updateNotebookCell,
} from "@/lib/api-notebooks";
import type { WebAsset, WebNotebook } from "@/lib/types";

export type NotebookDocumentState = {
  notebookId: string;
  mutationNotebook: WebNotebook | null;
  loadError: string;
  actionError: string;
};

export type NotebookDocumentEvent =
  | { type: "notebook_changed"; notebookId: string }
  | { type: "workspace_observed"; notebookId: string; revision: string }
  | {
      type: "authoritative_notebook_loaded";
      notebookId: string;
      notebook: WebNotebook;
      workspaceRevision?: string;
    }
  | { type: "notebook_load_failed"; notebookId: string; message: string }
  | { type: "mutation_started"; notebookId: string }
  | { type: "mutation_applied"; notebookId: string; notebook: WebNotebook }
  | { type: "action_error_reported"; notebookId: string; message: string };

export function createNotebookDocumentState(notebookId: string): NotebookDocumentState {
  return {
    notebookId,
    mutationNotebook: null,
    loadError: "",
    actionError: "",
  };
}

export function notebookDocumentReducer(
  state: NotebookDocumentState,
  event: NotebookDocumentEvent,
): NotebookDocumentState {
  if (event.type === "notebook_changed") {
    return createNotebookDocumentState(event.notebookId);
  }
  if (event.notebookId !== state.notebookId) {
    return state;
  }

  switch (event.type) {
    case "workspace_observed":
      return state.mutationNotebook?.revision === event.revision
        ? { ...state, mutationNotebook: null, loadError: "" }
        : state;
    case "authoritative_notebook_loaded":
      return {
        ...state,
        mutationNotebook:
          event.workspaceRevision === event.notebook.revision ? null : event.notebook,
        loadError: "",
      };
    case "notebook_load_failed":
      return { ...state, loadError: event.message };
    case "mutation_started":
      return { ...state, actionError: "" };
    case "mutation_applied":
      return {
        ...state,
        mutationNotebook: event.notebook,
        loadError: "",
        actionError: "",
      };
    case "action_error_reported":
      return { ...state, actionError: event.message };
  }
}

export function useNotebookDocument({
  notebookId,
  workspaceNotebook,
}: {
  notebookId: string;
  workspaceNotebook: WebNotebook | null;
}) {
  const [state, dispatch] = useReducer(
    notebookDocumentReducer,
    createNotebookDocumentState(notebookId),
  );
  const scopedState = useMemo(
    () => (state.notebookId === notebookId ? state : createNotebookDocumentState(notebookId)),
    [notebookId, state],
  );
  const notebook = scopedState.mutationNotebook ?? workspaceNotebook;

  useEffect(() => {
    dispatch({ type: "notebook_changed", notebookId });
  }, [notebookId]);

  useEffect(() => {
    const mutationNotebook = scopedState.mutationNotebook;
    if (!mutationNotebook || !workspaceNotebook) return;
    if (mutationNotebook.revision === workspaceNotebook.revision) {
      dispatch({
        type: "workspace_observed",
        notebookId,
        revision: workspaceNotebook.revision,
      });
      return;
    }

    let cancelled = false;
    void getNotebook(notebookId)
      .then((loaded) => {
        if (cancelled) return;
        dispatch({
          type: "authoritative_notebook_loaded",
          notebookId,
          notebook: loaded,
          workspaceRevision: workspaceNotebook.revision,
        });
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [notebookId, scopedState.mutationNotebook, workspaceNotebook]);

  useEffect(() => {
    if (workspaceNotebook || scopedState.mutationNotebook) return;
    let cancelled = false;
    void getNotebook(notebookId)
      .then((loaded) => {
        if (cancelled) return;
        dispatch({
          type: "authoritative_notebook_loaded",
          notebookId,
          notebook: loaded,
        });
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          dispatch({ type: "notebook_load_failed", notebookId, message: String(error) });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [notebookId, scopedState.mutationNotebook, workspaceNotebook]);

  const reportActionError = useCallback(
    (message: string) => {
      dispatch({ type: "action_error_reported", notebookId, message });
    },
    [notebookId],
  );

  const adoptNotebook = useCallback(
    (updated: WebNotebook) => {
      dispatch({ type: "mutation_applied", notebookId, notebook: updated });
    },
    [notebookId],
  );

  const mutateWithResult = useCallback(
    async (operation: () => Promise<WebNotebook>) => {
      dispatch({ type: "mutation_started", notebookId });
      try {
        const updated = await operation();
        dispatch({ type: "mutation_applied", notebookId, notebook: updated });
        return updated;
      } catch (error) {
        reportActionError(String(error));
        return null;
      }
    },
    [notebookId, reportActionError],
  );

  const mutateOrThrow = useCallback(
    async (operation: () => Promise<WebNotebook>) => {
      dispatch({ type: "mutation_started", notebookId });
      try {
        const updated = await operation();
        dispatch({ type: "mutation_applied", notebookId, notebook: updated });
        return updated;
      } catch (error) {
        reportActionError(String(error));
        throw error;
      }
    },
    [notebookId, reportActionError],
  );

  const mutate = useCallback(
    async (operation: () => Promise<WebNotebook>) => {
      await mutateWithResult(operation);
    },
    [mutateWithResult],
  );

  // Save responses are serialized per notebook cell. Each request carries the
  // revision acknowledged by the previous response so external edits become a
  // conflict instead of a silent last-writer-wins overwrite.
  const pendingSavesRef = useRef<Map<string, { notebookId: string; promise: Promise<void> }>>(
    new Map(),
  );
  const saveSeqRef = useRef<Map<string, number>>(new Map());
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
      const queueKey = `${notebookId}\u0000${cellId}`;
      const seq = (saveSeqRef.current.get(queueKey) ?? 0) + 1;
      saveSeqRef.current.set(queueKey, seq);
      const draftRevision = baseRevision || cell.content_revision || "";
      let queue = saveQueuesRef.current.get(queueKey);
      if (!queue) {
        queue = {
          tail: Promise.resolve(),
          pending: 0,
          revision: draftRevision,
          knownRevisions: new Set(draftRevision ? [draftRevision] : []),
        };
        saveQueuesRef.current.set(queueKey, queue);
      } else if (
        queue.pending === 0 &&
        draftRevision &&
        draftRevision !== queue.revision &&
        !queue.knownRevisions.has(draftRevision)
      ) {
        queue.revision = draftRevision;
        queue.knownRevisions.add(draftRevision);
      }
      queue.pending += 1;

      const previous = queue.tail;
      const promise = previous.then(async () => {
        try {
          const requestRevision = queue.revision;
          if (requestRevision) queue.knownRevisions.add(requestRevision);
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
          if (saveSeqRef.current.get(queueKey) === seq) {
            dispatch({ type: "mutation_applied", notebookId, notebook: updated });
          }
        } catch (error) {
          reportActionError(String(error));
        } finally {
          queue.pending = Math.max(0, queue.pending - 1);
          if (saveSeqRef.current.get(queueKey) === seq) {
            pendingSavesRef.current.delete(queueKey);
          }
        }
      });
      queue.tail = promise;
      pendingSavesRef.current.set(queueKey, { notebookId, promise });
      return promise;
    },
    [notebookId, reportActionError],
  );

  const flushPendingSaves = useCallback(async () => {
    const pending = [...pendingSavesRef.current.values()]
      .filter((entry) => entry.notebookId === notebookId)
      .map((entry) => entry.promise);
    if (pending.length > 0) await Promise.allSettled(pending);
  }, [notebookId]);

  return {
    notebook,
    loadError: scopedState.loadError,
    actionError: scopedState.actionError,
    reportActionError,
    adoptNotebook,
    mutateWithResult,
    mutateOrThrow,
    mutate,
    saveCellBody,
    flushPendingSaves,
  };
}
