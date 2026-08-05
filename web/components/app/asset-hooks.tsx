"use client";

import type { Monaco } from "@monaco-editor/react";
import { useAtomValue } from "jotai";
import { lazy, Suspense, useCallback, useState } from "react";
import { Pencil, Plus, Trash2 } from "lucide-react";
import type * as MonacoNS from "monaco-editor";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldLabel } from "@/components/ui/field";
import { useSQLLSP } from "@/hooks/use-sql-lsp";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";
import { applyAssetTransaction } from "@/lib/api-asset-transactions";
import { selectedAssetSchemaTablesAtom } from "@/lib/atoms/domains/suggestions";
import { loadMonacoEditorModule } from "@/lib/load-monaco-editor";
import { defineBruinMonacoThemes } from "@/lib/monaco-theme";
import type { WebAsset } from "@/lib/types";

const MonacoEditor = lazy(async () => {
  const module = await loadMonacoEditorModule();
  return { default: module.default };
});

type HookPhase = "pre" | "post";

type HookDraft = {
  phase: HookPhase;
  index?: number;
  query: string;
};

export function AssetHooks({ asset }: { asset: WebAsset }) {
  const [editing, setEditing] = useState<HookDraft | null>(null);
  const [error, setError] = useState("");

  const edit = (phase: HookPhase, index?: number) => {
    const hooks = phase === "pre" ? (asset.pre_hooks ?? []) : (asset.post_hooks ?? []);
    setEditing({
      phase,
      index,
      query:
        index === undefined
          ? `-- Runs ${phase === "pre" ? "before" : "after"} this asset is materialized\n`
          : (hooks[index] ?? ""),
    });
    setError("");
  };

  const remove = async (phase: HookPhase, index: number) => {
    setError("");
    try {
      await applyAssetTransaction(asset.id, {
        type: "hook.remove",
        hook_phase: phase,
        hook_index: index,
      });
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "The SQL hook could not be removed.");
    }
  };

  return (
    <div data-testid="asset-hooks" className="space-y-3">
      <p className="text-[11px] text-muted-foreground">
        Run ordered SQL statements immediately before or after this asset is materialized.
      </p>
      <HookPhaseList phase="pre" hooks={asset.pre_hooks ?? []} onEdit={edit} onRemove={remove} />
      <HookPhaseList phase="post" hooks={asset.post_hooks ?? []} onEdit={edit} onRemove={remove} />
      {error ? <p className="text-[11px] text-destructive">{error}</p> : null}
      <HookDialog
        asset={asset}
        draft={editing}
        onDraftChange={setEditing}
        onClose={() => setEditing(null)}
      />
    </div>
  );
}

function HookPhaseList({
  phase,
  hooks,
  onEdit,
  onRemove,
}: {
  phase: HookPhase;
  hooks: string[];
  onEdit: (phase: HookPhase, index?: number) => void;
  onRemove: (phase: HookPhase, index: number) => Promise<void>;
}) {
  const label = phase === "pre" ? "Before materialization" : "After materialization";
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between gap-2">
        <p className="text-[11px] font-medium text-foreground">{label}</p>
        <Button variant="outline" size="xs" onClick={() => onEdit(phase)}>
          <Plus />
          Add
        </Button>
      </div>
      {hooks.length === 0 ? (
        <p className="text-[11px] text-muted-foreground">No {phase}-hooks.</p>
      ) : (
        <div className="space-y-1.5">
          {hooks.map((query, index) => (
            <div key={`${phase}:${index}`} className="rounded-md border bg-muted/20 p-2">
              <div className="flex min-w-0 items-start gap-1.5">
                <pre className="max-h-16 min-w-0 flex-1 overflow-hidden whitespace-pre-wrap font-mono text-[10px] text-muted-foreground">
                  {query}
                </pre>
                <Button
                  variant="ghost"
                  size="icon-xs"
                  aria-label={`Edit ${phase}-hook ${index + 1}`}
                  onClick={() => onEdit(phase, index)}
                >
                  <Pencil />
                </Button>
                <AlertDialog>
                  <AlertDialogTrigger asChild>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      aria-label={`Remove ${phase}-hook ${index + 1}`}
                    >
                      <Trash2 />
                    </Button>
                  </AlertDialogTrigger>
                  <AlertDialogContent size="sm">
                    <AlertDialogHeader>
                      <AlertDialogTitle>Remove this SQL hook?</AlertDialogTitle>
                      <AlertDialogDescription>
                        The statement will no longer run {phase === "pre" ? "before" : "after"} the
                        asset.
                      </AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                      <AlertDialogCancel>Cancel</AlertDialogCancel>
                      <AlertDialogAction
                        variant="destructive"
                        onClick={() => void onRemove(phase, index)}
                      >
                        Remove
                      </AlertDialogAction>
                    </AlertDialogFooter>
                  </AlertDialogContent>
                </AlertDialog>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function HookDialog({
  asset,
  draft,
  onDraftChange,
  onClose,
}: {
  asset: WebAsset;
  draft: HookDraft | null;
  onDraftChange: (draft: HookDraft | null) => void;
  onClose: () => void;
}) {
  const { monacoTheme } = useWorkspaceTheme();
  const schemaTables = useAtomValue(selectedAssetSchemaTablesAtom);
  const [monacoInstance, setMonacoInstance] = useState<Monaco | null>(null);
  const [editorInstance, setEditorInstance] =
    useState<MonacoNS.editor.IStandaloneCodeEditor | null>(null);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useSQLLSP(
    monacoInstance,
    editorInstance,
    asset,
    draft?.query ?? "",
    schemaTables,
    undefined,
    undefined,
    { documentContext: "hook", allowNonSQLDocument: true },
  );

  const handleBeforeMount = useCallback((monaco: Monaco) => {
    defineBruinMonacoThemes(monaco);
  }, []);
  const handleMount = useCallback(
    (editor: MonacoNS.editor.IStandaloneCodeEditor, monaco: Monaco) => {
      defineBruinMonacoThemes(monaco);
      setEditorInstance(editor);
      setMonacoInstance(monaco);
    },
    [],
  );

  const save = async () => {
    if (!draft?.query.trim() || saving) return;
    setSaving(true);
    setError("");
    try {
      await applyAssetTransaction(asset.id, {
        type: "hook.upsert",
        hook_phase: draft.phase,
        hook_index: draft.index,
        hook_query: draft.query.trim(),
      });
      onClose();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "The SQL hook could not be saved.");
    } finally {
      setSaving(false);
    }
  };

  const phaseLabel = draft?.phase === "post" ? "post-hook" : "pre-hook";
  return (
    <Dialog open={draft !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex max-h-[calc(100vh-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-3xl">
        <DialogHeader className="border-b px-5 py-4">
          <DialogTitle>
            {draft?.index === undefined ? `Add ${phaseLabel}` : `Edit ${phaseLabel}`}
          </DialogTitle>
          <DialogDescription>
            The statement runs on the asset connection and supports the same Jinja context as the
            asset query.
          </DialogDescription>
        </DialogHeader>
        <div className="min-h-0 flex-1 px-5 py-4">
          <Field className="h-full min-h-72">
            <FieldLabel>SQL statement</FieldLabel>
            <div className="min-h-64 flex-1 overflow-hidden rounded-md border bg-background">
              <Suspense
                fallback={
                  <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
                    Loading SQL editor...
                  </div>
                }
              >
                <MonacoEditor
                  aria-label={`${phaseLabel} SQL`}
                  language="sql"
                  path={`inmemory://renart/hooks/${asset.id}/${draft?.phase ?? "pre"}/${draft?.index ?? "new"}.sql`}
                  value={draft?.query ?? ""}
                  theme={monacoTheme}
                  beforeMount={handleBeforeMount}
                  onMount={handleMount}
                  onChange={(query) => draft && onDraftChange({ ...draft, query: query ?? "" })}
                  options={{
                    automaticLayout: true,
                    fontSize: 12,
                    lineNumbersMinChars: 3,
                    minimap: { enabled: false },
                    padding: { top: 8, bottom: 8 },
                    scrollBeyondLastLine: false,
                    wordWrap: "on",
                  }}
                />
              </Suspense>
            </div>
            <FieldDescription>
              Keep each ordered hook as a separate statement so it can be reviewed independently.
            </FieldDescription>
            {error ? <p className="text-xs text-destructive">{error}</p> : null}
          </Field>
        </div>
        <DialogFooter className="border-t px-5 py-3">
          <Button type="button" variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            type="button"
            disabled={!draft?.query.trim() || saving}
            onClick={() => void save()}
          >
            {saving ? "Saving..." : "Save hook"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
