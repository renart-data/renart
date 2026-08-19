"use client";

import { Check, Loader2, Save, SlidersHorizontal, Trash2, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";

import {
  AuthoredControlEditor,
  AuthoredControlValueField,
} from "@/components/app/authored-control";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import type { NotebookParameter } from "@/lib/generated/api-types";
import { cn } from "@/lib/utils";

export function NotebookControlBlock({
  control,
  value,
  selected,
  busy,
  inspectorTarget,
  onSelect,
  onCloseInspector,
  onValueChange,
  onSave,
  onDelete,
}: {
  control: NotebookParameter;
  value: unknown;
  selected: boolean;
  busy: boolean;
  inspectorTarget: HTMLElement | null;
  onSelect: () => void;
  onCloseInspector: () => void;
  onValueChange: (value: unknown) => void;
  onSave: (control: NotebookParameter) => Promise<boolean>;
  onDelete: () => Promise<void>;
}) {
  const signature = JSON.stringify(control);
  const initial = useMemo(() => JSON.parse(signature) as NotebookParameter, [signature]);
  const [draft, setDraft] = useState(initial);
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);

  useEffect(() => setDraft(initial), [initial]);

  const dirty = JSON.stringify(draft) !== signature;
  const title = control.label?.trim() || control.id;
  const inspector = (
    <div data-testid="notebook-control-inspector" className="flex min-w-0 flex-col gap-4 p-3">
      <div className="flex min-w-0 items-start gap-2">
        <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md bg-sky-500/10 text-sky-600 dark:text-sky-300">
          <SlidersHorizontal className="size-3.5" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2">
            <p className="truncate text-sm font-medium">{title}</p>
            {dirty ? (
              <Badge variant="outline" className="shrink-0 font-normal">
                Draft
              </Badge>
            ) : null}
          </div>
          <p className="mt-0.5 text-[11px] leading-relaxed text-muted-foreground">
            Configure the typed input and its default value.
          </p>
        </div>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Close inspector"
          onClick={onCloseInspector}
        >
          <X />
        </Button>
      </div>
      <Separator />
      <AuthoredControlEditor
        control={draft}
        idPrefix="notebook-control-inspector"
        onChange={setDraft}
        onRename={(id) => setDraft((current) => ({ ...current, id }))}
        onDelete={() => {
          if (deleting || busy) return;
          setDeleting(true);
          void onDelete().finally(() => setDeleting(false));
        }}
      />
      <div className="flex items-center justify-end gap-2 border-t pt-3">
        {dirty ? (
          <span className="mr-auto text-[11px] text-muted-foreground">
            Changes stay local until applied.
          </span>
        ) : null}
        <Button
          size="sm"
          disabled={!dirty || saving || deleting || busy}
          onClick={() => {
            setSaving(true);
            void onSave(draft).finally(() => setSaving(false));
          }}
        >
          {saving ? <Loader2 className="animate-spin" /> : dirty ? <Save /> : <Check />}
          {dirty ? "Apply control" : "Saved"}
        </Button>
      </div>
    </div>
  );

  return (
    <>
      <section
        tabIndex={0}
        aria-label={`Control: ${title}`}
        className={cn(
          "group/notebook-control rounded-xl border border-transparent bg-transparent px-3 py-3 outline-none transition-colors hover:border-sky-500/25 hover:bg-sky-500/[0.025] focus-within:border-sky-500/25",
          selected && "border-sky-500/35 bg-sky-500/[0.035] ring-1 ring-sky-500/15",
        )}
        onClick={onSelect}
        onFocus={(event) => {
          if (event.target === event.currentTarget) onSelect();
        }}
      >
        <div className="flex min-w-0 items-end gap-3">
          <AuthoredControlValueField
            control={control}
            value={value}
            idScope={`notebook-control-block-${control.id}`}
            className="min-w-0 flex-1"
            onChange={onValueChange}
          />
          <div className="mb-0.5 ml-auto flex shrink-0 items-center opacity-0 transition-opacity group-hover/notebook-control:opacity-100 group-focus-within/notebook-control:opacity-100">
            <Button
              size="icon-sm"
              variant="ghost"
              aria-label={`Edit control ${title}`}
              onClick={(event) => {
                event.stopPropagation();
                onSelect();
              }}
            >
              <SlidersHorizontal />
            </Button>
            <Button
              size="icon-sm"
              variant="ghost"
              disabled={busy || deleting}
              aria-label={`Delete control ${title}`}
              onClick={(event) => {
                event.stopPropagation();
                if (busy || deleting) return;
                setDeleting(true);
                void onDelete().finally(() => setDeleting(false));
              }}
            >
              {deleting ? <Loader2 className="animate-spin" /> : <Trash2 />}
            </Button>
          </div>
        </div>
      </section>
      {selected && inspectorTarget ? createPortal(inspector, inspectorTarget) : null}
    </>
  );
}
