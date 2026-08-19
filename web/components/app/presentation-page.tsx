"use client";

import type { Monaco } from "@monaco-editor/react";
import type * as MonacoNS from "monaco-editor";
import { Link, Outlet, useNavigate } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import {
  AlertTriangle,
  ArrowLeft,
  ChevronRight,
  FileText,
  Eye,
  LayoutDashboard,
  Loader2,
  Plus,
  RotateCcw,
  Save,
} from "lucide-react";
import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  createPresentation,
  getPresentation,
  PresentationArtifact,
  PresentationDocument,
  PresentationFinding,
  replacePresentation,
  updatePresentation,
} from "@/lib/api-presentations";
import { workspaceAtom } from "@/lib/atoms/domains/workspace";
import { loadMonacoEditorModule } from "@/lib/load-monaco-editor";
import { defineBruinMonacoThemes } from "@/lib/monaco-theme";
import { cn } from "@/lib/utils";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";

import { AppPage, PageHeader } from "./app-primitives";
import { PresentationVisualEditor } from "./presentation-visual-editor";

const MonacoEditor = lazy(async () => {
  const module = await loadMonacoEditorModule();
  return { default: module.default };
});

export type PresentationKind = "dashboard" | "report";

const presentationMeta = {
  dashboard: {
    plural: "Dashboards",
    singular: "dashboard",
    description: "Version-controlled views over pipeline data.",
    icon: LayoutDashboard,
  },
  report: {
    plural: "Reports",
    singular: "report",
    description: "Version-controlled narrative and visualization documents.",
    icon: FileText,
  },
} as const;

export function AppPresentationsLayout() {
  return (
    <div className="flex h-full min-h-0 flex-col bg-muted/40">
      <nav
        aria-label="Presentations"
        className="flex h-10 shrink-0 items-center gap-1 border-b bg-background px-3"
      >
        <Button asChild size="sm" variant="ghost">
          <Link
            to="/dashboards"
            activeOptions={{ exact: false }}
            activeProps={{ className: "bg-muted text-foreground" }}
          >
            <LayoutDashboard data-icon="inline-start" />
            Dashboards
          </Link>
        </Button>
        <Button asChild size="sm" variant="ghost">
          <Link
            to="/reports"
            activeOptions={{ exact: false }}
            activeProps={{ className: "bg-muted text-foreground" }}
          >
            <FileText data-icon="inline-start" />
            Reports
          </Link>
        </Button>
      </nav>
      <div className="min-h-0 flex-1">
        <Outlet />
      </div>
    </div>
  );
}

export function AppPresentationsIndexPage({ kind }: { kind: PresentationKind }) {
  const workspace = useAtomValue(workspaceAtom);
  const navigate = useNavigate();
  const [createOpen, setCreateOpen] = useState(false);
  const items = (workspace?.presentations ?? []).filter((item) => item.kind === kind);
  const meta = presentationMeta[kind];
  const Icon = meta.icon;

  return (
    <AppPage>
      <PageHeader
        title={meta.plural}
        subtitle={meta.description}
        actions={
          <Button size="sm" onClick={() => setCreateOpen(true)}>
            <Plus className="size-3.5" />
            New {meta.singular}
          </Button>
        }
      />
      <ScrollArea className="min-h-0 flex-1 px-3 pb-3">
        <div className="mx-auto flex min-h-full w-full max-w-3xl flex-col py-6">
          {items.length === 0 ? (
            <div className="m-auto w-full max-w-md rounded-xl border border-dashed bg-background p-8 text-center">
              <Icon className="mx-auto mb-3 size-8 text-muted-foreground" />
              <div className="text-sm font-medium">No {meta.plural.toLowerCase()} yet</div>
              <p className="mt-1 text-xs text-muted-foreground">
                Definitions live in Git and are checked against pipeline schemas before use.
              </p>
              <Button size="sm" className="mt-4" onClick={() => setCreateOpen(true)}>
                <Plus className="size-3.5" />
                New {meta.singular}
              </Button>
            </div>
          ) : (
            <div className="divide-y overflow-hidden rounded-xl border bg-card">
              {items.map((item) => (
                <button
                  key={item.workspace_id}
                  type="button"
                  onClick={() =>
                    void navigate({
                      to:
                        kind === "dashboard"
                          ? "/dashboards/$presentationId"
                          : "/reports/$presentationId",
                      params: { presentationId: item.workspace_id },
                    })
                  }
                  className="flex w-full min-w-0 items-center gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/50"
                >
                  <Icon className="size-4 shrink-0 text-primary" />
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium">{item.title}</span>
                    <span className="block truncate font-mono text-[11px] text-muted-foreground">
                      {item.path}
                    </span>
                  </span>
                  {item.problems?.length ? (
                    <span className="inline-flex shrink-0 items-center gap-1 text-[11px] text-amber-600 dark:text-amber-400">
                      <AlertTriangle className="size-3" />
                      {item.problems.length}
                    </span>
                  ) : null}
                  <span className="shrink-0 text-[11px] text-muted-foreground">
                    {item.visualizations?.length ?? 0} visualization
                    {(item.visualizations?.length ?? 0) === 1 ? "" : "s"}
                  </span>
                  <ChevronRight className="size-4 shrink-0 text-muted-foreground" />
                </button>
              ))}
            </div>
          )}
        </div>
      </ScrollArea>
      <NewPresentationDialog kind={kind} open={createOpen} onOpenChange={setCreateOpen} />
    </AppPage>
  );
}

function NewPresentationDialog({
  kind,
  open,
  onOpenChange,
}: {
  kind: PresentationKind;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const navigate = useNavigate();
  const [title, setTitle] = useState("");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!open) return;
    setTitle("");
    setError("");
  }, [open]);

  const submit = async () => {
    if (!title.trim() || saving) return;
    setSaving(true);
    setError("");
    try {
      const created = await createPresentation({ kind, title: title.trim() });
      onOpenChange(false);
      await navigate({
        to: kind === "dashboard" ? "/dashboards/$presentationId" : "/reports/$presentationId",
        params: { presentationId: created.artifact.workspace_id },
      });
    } catch (nextError) {
      setError(errorMessage(nextError));
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>New {presentationMeta[kind].singular}</DialogTitle>
          <DialogDescription>
            Renart creates a reviewable {kind === "dashboard" ? "*.dashboard.yml" : "*.report.yml"}{" "}
            file in this project.
          </DialogDescription>
        </DialogHeader>
        <FieldGroup>
          <Field>
            <FieldLabel htmlFor="new-presentation-title">Title</FieldLabel>
            <Input
              id="new-presentation-title"
              autoFocus
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter") void submit();
              }}
              placeholder={kind === "dashboard" ? "Sales overview" : "Weekly performance"}
            />
          </Field>
        </FieldGroup>
        {error ? (
          <Alert variant="destructive">
            <AlertTriangle />
            <AlertTitle>Could not create {kind}</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button disabled={!title.trim() || saving} onClick={() => void submit()}>
            {saving ? <Loader2 className="animate-spin" /> : <Plus />}
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function AppPresentationLivePage({
  kind,
  presentationId,
}: {
  kind: PresentationKind;
  presentationId: string;
}) {
  const workspace = useAtomValue(workspaceAtom);
  const [document, setDocument] = useState<PresentationDocument | null>(null);
  const [visualDraft, setVisualDraft] = useState<PresentationArtifact | null>(null);
  const [definitionDraft, setDefinitionDraft] = useState("");
  const [mode, setMode] = useState<"visual" | "definition">("visual");
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [externalRevision, setExternalRevision] = useState("");
  const observedWorkspaceRevision = useRef("");

  const visualDirty = Boolean(
    document &&
    visualDraft &&
    authoredPresentationJSON(document.artifact) !== authoredPresentationJSON(visualDraft),
  );
  const definitionDirty = Boolean(document && definitionDraft !== document.content);
  const dirty = visualDirty || definitionDirty;

  const acceptDocument = useCallback((next: PresentationDocument) => {
    setDocument(next);
    setVisualDraft(cloneArtifact(next.artifact));
    setDefinitionDraft(next.content);
    setExternalRevision("");
  }, []);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      acceptDocument(await getPresentation(presentationId));
    } catch (nextError) {
      setError(errorMessage(nextError));
    } finally {
      setLoading(false);
    }
  }, [acceptDocument, presentationId]);

  useEffect(() => {
    void load();
  }, [load]);

  const workspaceArtifact = workspace?.presentations?.find(
    (artifact) => artifact.workspace_id === presentationId,
  );
  useEffect(() => {
    const revision = workspaceArtifact?.revision ?? "";
    if (!revision || observedWorkspaceRevision.current === revision) return;
    observedWorkspaceRevision.current = revision;
    if (!document || revision === document.artifact.revision) return;
    if (dirty) {
      setExternalRevision(revision);
      return;
    }
    void load();
  }, [dirty, document, load, workspaceArtifact?.revision]);

  const save = async () => {
    if (!document || !visualDraft || saving) return;
    setSaving(true);
    setError("");
    try {
      const next =
        mode === "visual"
          ? await replacePresentation(presentationId, document.artifact.revision, visualDraft)
          : await updatePresentation(presentationId, {
              expected_revision: document.artifact.revision,
              content: definitionDraft,
            });
      acceptDocument(next);
    } catch (nextError) {
      setError(errorMessage(nextError));
    } finally {
      setSaving(false);
    }
  };

  const resetActiveDraft = () => {
    if (!document) return;
    if (mode === "visual") setVisualDraft(cloneArtifact(document.artifact));
    else setDefinitionDraft(document.content);
    setError("");
  };

  const meta = presentationMeta[kind];
  const kindMismatch = document && document.artifact.kind !== kind;
  const activeDirty = mode === "visual" ? visualDirty : definitionDirty;

  return (
    <AppPage>
      <PageHeader
        title={document?.artifact.title ?? `Loading ${meta.singular}…`}
        subtitle={document?.artifact.path ?? meta.description}
        actions={
          <>
            <Button asChild variant="ghost" size="sm">
              <Link to={kind === "dashboard" ? "/dashboards" : "/reports"}>
                <ArrowLeft />
                {meta.plural}
              </Link>
            </Button>
            {document ? (
              <Button asChild variant="outline" size="sm">
                <Link
                  to={
                    kind === "dashboard"
                      ? "/dashboards/$presentationId/view"
                      : "/reports/$presentationId/view"
                  }
                  params={{ presentationId }}
                  search={{ filters: undefined }}
                >
                  <Eye /> Preview
                </Link>
              </Button>
            ) : null}
            <Button
              variant="outline"
              size="sm"
              disabled={!activeDirty || saving}
              onClick={resetActiveDraft}
            >
              <RotateCcw />
              Discard
            </Button>
            <Button size="sm" disabled={!activeDirty || saving} onClick={() => void save()}>
              {saving ? <Loader2 className="animate-spin" /> : <Save />}
              Save
            </Button>
          </>
        }
      />

      {loading ? (
        <PresentationEditorSkeleton />
      ) : error && !document ? (
        <div className="m-auto w-full max-w-xl p-4">
          <Alert variant="destructive">
            <AlertTriangle />
            <AlertTitle>Could not load {meta.singular}</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
          <Button className="mt-3" size="sm" variant="outline" onClick={() => void load()}>
            Try again
          </Button>
        </div>
      ) : kindMismatch ? (
        <div className="m-auto w-full max-w-xl p-4">
          <Alert variant="destructive">
            <AlertTriangle />
            <AlertTitle>Presentation kind does not match this route</AlertTitle>
            <AlertDescription>
              This file is a {document?.artifact.kind}. Open it from the corresponding page.
            </AlertDescription>
          </Alert>
        </div>
      ) : document && visualDraft ? (
        <Tabs
          value={mode}
          onValueChange={(value) => setMode(value as "visual" | "definition")}
          className="min-h-0 flex-1 gap-0"
        >
          <div className="flex shrink-0 items-center gap-2 border-y bg-background px-3 py-1.5">
            <TabsList>
              <TabsTrigger value="visual" disabled={definitionDirty}>
                Visual
              </TabsTrigger>
              <TabsTrigger value="definition" disabled={visualDirty}>
                Definition
              </TabsTrigger>
            </TabsList>
            {activeDirty ? (
              <span className="text-[11px] text-muted-foreground">
                Save or discard this draft before switching editors.
              </span>
            ) : null}
            {document.artifact.problems?.length ? (
              <Badge
                variant="outline"
                className="ml-auto border-amber-500/30 text-amber-700 dark:text-amber-300"
              >
                <AlertTriangle />
                {document.artifact.problems.length} problem
                {document.artifact.problems.length === 1 ? "" : "s"}
              </Badge>
            ) : (
              <Badge variant="outline" className="ml-auto font-normal text-emerald-700">
                Definition valid
              </Badge>
            )}
          </div>

          {externalRevision ? (
            <Alert className="mx-3 mt-3 shrink-0 border-amber-500/30 bg-amber-500/5">
              <AlertTriangle />
              <AlertTitle>This file changed outside the editor</AlertTitle>
              <AlertDescription>
                Your draft is still here. Reload the latest file before saving, then reconcile the
                changes you want to keep.
              </AlertDescription>
              <Button size="sm" variant="outline" className="mt-2" onClick={() => void load()}>
                Reload latest
              </Button>
            </Alert>
          ) : null}
          {error ? (
            <Alert variant="destructive" className="mx-3 mt-3 shrink-0">
              <AlertTriangle />
              <AlertTitle>Could not save {meta.singular}</AlertTitle>
              <AlertDescription>{error}</AlertDescription>
            </Alert>
          ) : null}

          <TabsContent value="visual" className="min-h-0 overflow-hidden">
            <ScrollArea className="h-full px-3">
              <div className="mx-auto w-full max-w-6xl py-4">
                <PresentationVisualEditor
                  artifact={visualDraft}
                  workspace={workspace}
                  onChange={setVisualDraft}
                />
                <PresentationProblems findings={document.artifact.problems ?? []} />
              </div>
            </ScrollArea>
          </TabsContent>
          <TabsContent value="definition" className="min-h-0 overflow-hidden p-3">
            <PresentationDefinitionEditor
              presentationId={presentationId}
              value={definitionDraft}
              onChange={setDefinitionDraft}
            />
          </TabsContent>
        </Tabs>
      ) : null}
    </AppPage>
  );
}

function PresentationDefinitionEditor({
  presentationId,
  value,
  onChange,
}: {
  presentationId: string;
  value: string;
  onChange: (value: string) => void;
}) {
  const { monacoTheme } = useWorkspaceTheme();
  const completionDisposable = useRef<MonacoNS.IDisposable | null>(null);
  useEffect(() => () => completionDisposable.current?.dispose(), []);
  const beforeMount = useCallback((monaco: Monaco) => defineBruinMonacoThemes(monaco), []);
  const onMount = useCallback(
    (_editor: MonacoNS.editor.IStandaloneCodeEditor, monaco: Monaco) => {
      completionDisposable.current?.dispose();
      completionDisposable.current = monaco.languages.registerCompletionItemProvider("yaml", {
        provideCompletionItems: (
          model: MonacoNS.editor.ITextModel,
          position: MonacoNS.Position,
        ) => {
          if (!model.uri.path.includes(`/presentation/${presentationId}.yml`)) {
            return { suggestions: [] };
          }
          const range = new monaco.Range(
            position.lineNumber,
            position.column,
            position.lineNumber,
            position.column,
          );
          return {
            suggestions: [
              "version",
              "id",
              "title",
              "datasets",
              "asset",
              "connection",
              "query",
              "columns",
              "filters",
              "options",
              "visualizations",
              "dataset",
              "definition",
              "filter_bindings",
              "layout",
              "sections",
              "visualization",
              "markdown",
              "page_break",
            ].map((label) => ({
              label,
              kind: monaco.languages.CompletionItemKind.Property,
              insertText: `${label}: `,
              range,
            })),
          };
        },
      });
    },
    [presentationId],
  );
  return (
    <div
      role="region"
      aria-label="Presentation definition YAML"
      className="h-full min-h-80 overflow-hidden rounded-lg border bg-background"
    >
      <Suspense
        fallback={
          <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
            Loading definition editor…
          </div>
        }
      >
        <MonacoEditor
          aria-label="Presentation definition YAML"
          language="yaml"
          path={`inmemory://renart/presentation/${presentationId}.yml`}
          value={value}
          theme={monacoTheme}
          beforeMount={beforeMount}
          onMount={onMount}
          onChange={(next) => onChange(next ?? "")}
          options={{
            automaticLayout: true,
            fontSize: 12,
            lineNumbers: "on",
            lineNumbersMinChars: 3,
            minimap: { enabled: false },
            padding: { top: 10, bottom: 10 },
            scrollBeyondLastLine: false,
            tabSize: 2,
            wordWrap: "on",
          }}
        />
      </Suspense>
    </div>
  );
}

function PresentationProblems({ findings }: { findings: PresentationFinding[] }) {
  if (findings.length === 0) return null;
  return (
    <div className="space-y-2 rounded-xl border border-amber-500/25 bg-amber-500/5 p-3">
      <div className="flex items-center gap-2 text-sm font-medium">
        <AlertTriangle className="size-4 text-amber-600" />
        Definition problems
      </div>
      {findings.map((finding, index) => (
        <div
          key={`${finding.code}:${finding.path ?? ""}:${index}`}
          className={cn(
            "rounded-md border bg-background/70 px-2.5 py-2 text-xs",
            finding.severity === "error" ? "border-red-500/25" : "border-amber-500/25",
          )}
        >
          <div className="font-medium">{finding.message}</div>
          <div className="mt-0.5 flex min-w-0 gap-2 font-mono text-[10px] text-muted-foreground">
            <span>{finding.code}</span>
            {finding.path ? <span className="truncate">{finding.path}</span> : null}
          </div>
        </div>
      ))}
    </div>
  );
}

function PresentationEditorSkeleton() {
  return (
    <div className="min-h-0 flex-1 space-y-3 p-3">
      <Skeleton className="h-9 w-52" />
      <Skeleton className="h-36 w-full rounded-xl" />
      <Skeleton className="h-64 w-full rounded-xl" />
    </div>
  );
}

function cloneArtifact(artifact: PresentationArtifact): PresentationArtifact {
  return JSON.parse(JSON.stringify(artifact)) as PresentationArtifact;
}

function authoredPresentationJSON(artifact: PresentationArtifact) {
  return JSON.stringify({
    version: artifact.version,
    id: artifact.id,
    kind: artifact.kind,
    title: artifact.title,
    datasets: artifact.datasets ?? [],
    filters: artifact.filters ?? [],
    visualizations: artifact.visualizations ?? [],
    layout: artifact.layout ?? [],
    sections: artifact.sections ?? [],
  });
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : "The request failed.";
}
