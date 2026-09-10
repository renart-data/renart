import { useAtom } from "jotai";
import { useCallback, useEffect, useRef, useState, type DragEvent } from "react";
import {
  applyNotebookBrowserSource,
  getNotebook,
  prepareNotebookBrowserSource,
} from "@/lib/api-notebooks";
import {
  acceptsDataBrowserTransfer,
  DATA_BROWSER_MIME,
  dataBrowserTransferAtom,
  matchesDataBrowserTransfer,
} from "@/lib/data-browser-transfer";
import { getPinnedProjectId } from "@/lib/project-context";
import type { NotebookBrowserSourceRequest, NotebookChangePlan } from "@/lib/generated/api-types";
import type { WebNotebook } from "@/lib/types";

export type NotebookSourcePlacement = {
  position: "start" | "end" | "after";
  after_block_id?: string;
};
type Review = {
  request: NotebookBrowserSourceRequest;
  label: string;
  plan: NotebookChangePlan | null;
  busy: "prepare" | "apply" | null;
  error: string;
};

export function useNotebookBrowserDrop({
  notebookId,
  environment,
  awaitSavedChanges,
  mutateOrThrow,
  onCreated,
}: {
  notebookId: string;
  environment: string;
  awaitSavedChanges: () => Promise<void>;
  mutateOrThrow: (operation: () => Promise<WebNotebook>) => Promise<WebNotebook>;
  onCreated: (cellId: string) => void;
}) {
  const [transfer, setTransfer] = useAtom(dataBrowserTransferAtom);
  const projectId = getPinnedProjectId();
  const scope = JSON.stringify([projectId, notebookId, environment]);
  const currentScope = useRef(scope);
  currentScope.current = scope;
  const [review, setReview] = useState<Review | null>(null);
  const sequence = useRef(0);
  const pending = useRef<AbortController | null>(null);
  const active = acceptsDataBrowserTransfer(
    transfer,
    { kind: "notebook", id: notebookId },
    projectId,
    environment,
  )
    ? transfer
    : null;
  const close = useCallback(() => {
    ++sequence.current;
    pending.current?.abort();
    setReview(null);
  }, []);
  useEffect(() => {
    close();
    return () => {
      ++sequence.current;
      pending.current?.abort();
      setTransfer((current) =>
        current?.destination.kind === "notebook" &&
        current.destination.id === notebookId &&
        current.projectId === projectId
          ? null
          : current,
      );
    };
  }, [scope, close, notebookId, projectId, setTransfer]);
  useEffect(() => {
    if (!active) return;
    const cancel = (event: KeyboardEvent) => {
      if (event.key === "Escape") setTransfer(null);
    };
    window.addEventListener("keydown", cancel);
    return () => window.removeEventListener("keydown", cancel);
  }, [active, setTransfer]);
  useEffect(() => {
    if (active?.method !== "choose") return;
    const frame = requestAnimationFrame(() =>
      document
        .querySelector<HTMLButtonElement>('[data-testid="notebook-source-drop-target"]')
        ?.focus(),
    );
    return () => cancelAnimationFrame(frame);
  }, [active?.token, active?.method]);

  const prepare = async (request: NotebookBrowserSourceRequest, label: string) => {
    const visit = ++sequence.current;
    pending.current?.abort();
    const controller = new AbortController();
    pending.current = controller;
    const current = () =>
      sequence.current === visit &&
      currentScope.current === scope &&
      getPinnedProjectId() === projectId;
    setReview({ request, label, plan: null, busy: "prepare", error: "" });
    try {
      await awaitSavedChanges();
      if (!current()) return;
      const notebook = await getNotebook(notebookId);
      if (!current()) return;
      const plan = await prepareNotebookBrowserSource(
        notebookId,
        { ...request, base_revision: notebook.revision },
        controller.signal,
      );
      if (!current()) return;
      setReview({
        request,
        label,
        plan,
        busy: null,
        error: plan.can_apply
          ? ""
          : plan.blocking_problems?.join("; ") || "This source cannot be inserted.",
      });
    } catch (error) {
      if (current())
        setReview({
          request,
          label,
          plan: null,
          busy: null,
          error: error instanceof Error ? error.message : String(error),
        });
    }
  };
  const drop = (placement: NotebookSourcePlacement, event?: DragEvent) => {
    if (!active || review || active.kind === "connection") return;
    if (
      event
        ? !matchesDataBrowserTransfer(active, event.dataTransfer.getData(DATA_BROWSER_MIME))
        : active.method !== "choose"
    )
      return;
    event?.preventDefault();
    event?.stopPropagation();
    setTransfer(null);
    void prepare(
      {
        object_id: active.id,
        environment: active.environment,
        ...placement,
        snapshot_mode: "full",
      },
      active.label,
    );
  };
  const apply = async () => {
    if (!review?.plan?.can_apply || review.busy) return;
    const visit = ++sequence.current;
    const current = () =>
      sequence.current === visit &&
      currentScope.current === scope &&
      getPinnedProjectId() === projectId;
    setReview({ ...review, busy: "apply", error: "" });
    try {
      await awaitSavedChanges();
      if (!current()) return;
      const result = await mutateOrThrow(
        async () =>
          (
            await applyNotebookBrowserSource(notebookId, {
              object_id: review.request.object_id,
              environment,
              change_set: review.plan!.change_set,
            })
          ).notebook,
      );
      if (!current()) return;
      const cellId = review.plan.change_set.operations[0].cell_id;
      close();
      if (cellId && result.cells.some((cell) => cell.cell_id === cellId)) onCreated(cellId);
    } catch (error) {
      if (current())
        setReview({
          ...review,
          plan: null,
          busy: null,
          error: error instanceof Error ? error.message : String(error),
        });
    }
  };
  return { active, review, drop, close, apply, prepare };
}

export type NotebookBrowserDrop = ReturnType<typeof useNotebookBrowserDrop>;
