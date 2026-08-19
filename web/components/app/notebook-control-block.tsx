"use client";

import {
  AlertTriangle,
  Check,
  Loader2,
  RefreshCw,
  Save,
  SlidersHorizontal,
  Trash2,
  X,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { createPortal } from "react-dom";

import {
  AuthoredControlEditor,
  AuthoredControlValueField,
  type AuthoredControlDataset,
} from "@/components/app/authored-control";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import type { NotebookParameter } from "@/lib/generated/api-types";
import type { PresentationDatasetResult } from "@/lib/generated/api-types";
import { authoredControlOptions } from "@/lib/authored-controls";
import { cn } from "@/lib/utils";

export function NotebookControlBlock({
  control,
  value,
  selected,
  busy,
  datasets,
  optionResult,
  optionsLoading,
  optionsStale,
  inspectorTarget,
  onSelect,
  onCloseInspector,
  onValueChange,
  onRefreshOptions,
  onSave,
  onDelete,
}: {
  control: NotebookParameter;
  value: unknown;
  selected: boolean;
  busy: boolean;
  datasets: AuthoredControlDataset[];
  optionResult?: PresentationDatasetResult;
  optionsLoading: boolean;
  optionsStale: boolean;
  inspectorTarget: HTMLElement | null;
  onSelect: () => void;
  onCloseInspector: () => void;
  onValueChange: (value: unknown) => void;
  onRefreshOptions: () => void;
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
  const datasetBacked = Boolean(control.options?.dataset && control.options?.value_field);
  const runtimeOptions = useMemo(
    () => (datasetBacked ? authoredControlOptions(control, optionResult) : undefined),
    [control, datasetBacked, optionResult],
  );
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
        datasets={datasets}
        resolvedOptions={
          notebookControlOptionSourceMatches(draft, control) ? runtimeOptions : undefined
        }
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
        <div className="flex min-w-0 flex-wrap items-end gap-3">
          <AuthoredControlValueField
            control={control}
            value={value}
            options={runtimeOptions}
            idScope={`notebook-control-block-${control.id}`}
            className="min-w-48 flex-1"
            onChange={onValueChange}
          />
          {datasetBacked ? (
            <div className="mb-0.5 ml-auto flex shrink-0 items-center gap-1.5">
              {optionsStale ? (
                <span className="inline-flex items-center gap-1 text-[11px] text-amber-700 dark:text-amber-300">
                  <AlertTriangle className="size-3" /> Run source
                </span>
              ) : optionResult ? (
                <Badge variant="outline" className="font-normal">
                  {runtimeOptions?.length ?? 0} option{runtimeOptions?.length === 1 ? "" : "s"}
                  {optionResult.truncated ? " · capped" : ""}
                </Badge>
              ) : null}
              <Button
                size="sm"
                variant="ghost"
                disabled={busy || optionsLoading || optionsStale}
                onClick={(event) => {
                  event.stopPropagation();
                  onRefreshOptions();
                }}
              >
                {optionsLoading ? <Loader2 className="animate-spin" /> : <RefreshCw />}
                {optionResult ? "Refresh" : "Load options"}
              </Button>
            </div>
          ) : null}
          <div
            className={cn(
              "mb-0.5 flex shrink-0 items-center opacity-0 transition-opacity group-hover/notebook-control:opacity-100 group-focus-within/notebook-control:opacity-100",
              !datasetBacked && "ml-auto",
            )}
          >
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

function notebookControlOptionSourceMatches(
  draft: NotebookParameter,
  saved: NotebookParameter,
): boolean {
  return (
    draft.options?.dataset?.trim() === saved.options?.dataset?.trim() &&
    draft.options?.value_field?.trim() === saved.options?.value_field?.trim() &&
    draft.options?.label_field?.trim() === saved.options?.label_field?.trim()
  );
}
