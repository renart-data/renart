import { AlertTriangle, Plus } from "lucide-react";
import { useEffect, useRef } from "react";

import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { QuickCreateDraft, QuickCreateKind, QuickCreateState } from "@/lib/quick-create";
import { cn } from "@/lib/utils";

export type QuickCreateCanvasController = {
  state: QuickCreateState;
  // Why the current name cannot be created, shown before any request.
  validationError: string | null;
  onEdit: (patch: Partial<Pick<QuickCreateDraft, "name" | "kind">>) => void;
  onSubmit: () => void;
  onCancel: () => void;
  // Continues in the full creation dialog with the draft carried over.
  onMoreOptions: () => void;
};

const kinds: { id: QuickCreateKind; label: string }[] = [
  { id: "sql", label: "SQL" },
  { id: "python", label: "Python" },
  { id: "load", label: "Load" },
];

// Selects the part of the name after its prefix, so typing replaces only
// the placeholder and the asset stays in its group.
function selectLeaf(input: HTMLInputElement) {
  input.setSelectionRange(input.value.lastIndexOf(".") + 1, input.value.length);
}

// The pending asset on the canvas: named inline next to its sources, then
// shown as created until the workspace update replaces it with the real node.
export function QuickCreateNode({ controller }: { controller: QuickCreateCanvasController }) {
  const { state } = controller;
  const inputRef = useRef<HTMLInputElement>(null);
  const editable = state.status === "editing" || state.status === "failed";
  const opened = state.status === "editing";

  // Escape discards the card wherever focus is, unless another surface that
  // handles Escape itself (a dialog, menu, list or the code editor) has it.
  const onCancel = controller.onCancel;
  useEffect(() => {
    if (!editable) return;
    const cancelOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return;
      const target = event.target;
      if (
        target instanceof Element &&
        target.closest(
          '[role="dialog"], [role="alertdialog"], [role="menu"], [role="listbox"], .monaco-editor',
        )
      ) {
        return;
      }
      event.preventDefault();
      onCancel();
    };
    document.addEventListener("keydown", cancelOnEscape);
    return () => document.removeEventListener("keydown", cancelOnEscape);
  }, [editable, onCancel]);

  useEffect(() => {
    if (!opened) return;
    const input = inputRef.current;
    if (!input || document.activeElement === input) return;
    // Wait a frame so the canvas has placed the node before focusing it.
    const frame = window.requestAnimationFrame(() => {
      input.focus({ preventScroll: true });
      selectLeaf(input);
    });
    return () => window.cancelAnimationFrame(frame);
  }, [opened]);

  if (state.status === "idle") return null;
  const { draft } = state;
  const joining = draft.sourceIds.length > 1;
  const failed = state.status === "failed";
  const busy = state.status === "creating" || state.status === "created";
  const needsDialog = draft.kind === "load";
  const nameError = editable ? controller.validationError : null;

  return (
    <div
      data-testid="quick-create-node"
      data-state={state.status}
      role="group"
      aria-label={joining ? "New joined asset" : "New downstream asset"}
      className={cn(
        "nodrag nopan nowheel flex w-58 flex-col gap-2 rounded-xl border-2 border-dashed bg-card p-2.5 text-left shadow-md",
        failed ? "border-destructive" : "border-primary",
      )}
    >
      <div className="flex min-w-0 items-center justify-between gap-2">
        <span className="flex min-w-0 items-center gap-1 text-[11px] font-medium text-muted-foreground">
          <Plus className="size-3 shrink-0 text-primary" />
          <span className="truncate">{joining ? "Join" : "New asset"}</span>
        </span>
        <ToggleGroup
          type="single"
          size="sm"
          variant="outline"
          spacing={0}
          value={draft.kind}
          aria-label="Asset kind"
          disabled={!editable}
          onValueChange={(value) => {
            if (value) controller.onEdit({ kind: value as QuickCreateKind });
          }}
        >
          {kinds.map((kind) => (
            <ToggleGroupItem
              key={kind.id}
              value={kind.id}
              aria-label={kind.label}
              disabled={joining && kind.id !== "sql"}
              className="h-5 px-1.5 text-[10px]"
            >
              {kind.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      </div>
      <input
        ref={inputRef}
        aria-label="Asset name"
        aria-invalid={Boolean(nameError) || undefined}
        spellCheck={false}
        autoComplete="off"
        value={draft.name}
        disabled={!editable}
        onChange={(event) => controller.onEdit({ name: event.target.value })}
        onKeyDown={(event) => {
          if (event.key !== "Enter") return;
          event.preventDefault();
          if (needsDialog) controller.onMoreOptions();
          else if (!nameError) controller.onSubmit();
        }}
        className={cn(
          "h-7 w-full min-w-0 rounded-md border bg-background px-2 font-mono text-xs outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/40 disabled:opacity-70",
          nameError && "border-destructive",
        )}
      />
      {failed ? (
        <p
          role="alert"
          className="line-clamp-3 flex gap-1 text-[11px] leading-snug text-destructive"
          title={state.error}
        >
          <AlertTriangle className="mt-px size-3 shrink-0" />
          <span className="min-w-0">{state.error}</span>
        </p>
      ) : nameError && draft.name.trim() ? (
        <p className="truncate text-[11px] text-destructive" title={nameError}>
          {nameError}
        </p>
      ) : needsDialog ? (
        <p className="truncate text-[11px] text-muted-foreground">Choose the destination next.</p>
      ) : null}
      <div className="flex min-w-0 items-center justify-between gap-1">
        {busy ? (
          <span className="flex min-w-0 items-center gap-1.5 text-[11px] text-muted-foreground">
            <Spinner className="size-3" />
            {state.status === "creating" ? "Creating…" : "Opening…"}
          </span>
        ) : (
          <Button
            type="button"
            variant="link"
            size="xs"
            className="h-6 px-0 text-[11px]"
            onClick={controller.onMoreOptions}
          >
            More options…
          </Button>
        )}
        {editable ? (
          <div className="flex shrink-0 items-center gap-1">
            <Button
              type="button"
              variant="ghost"
              size="xs"
              className="h-6 px-1.5 text-[11px]"
              onClick={controller.onCancel}
            >
              {failed ? "Discard" : "Cancel"}
            </Button>
            <Button
              type="button"
              size="xs"
              className="h-6 px-2 text-[11px]"
              disabled={!needsDialog && Boolean(nameError)}
              onClick={needsDialog ? controller.onMoreOptions : controller.onSubmit}
            >
              {needsDialog ? "Continue…" : failed ? "Retry" : "Create"}
            </Button>
          </div>
        ) : null}
      </div>
    </div>
  );
}
