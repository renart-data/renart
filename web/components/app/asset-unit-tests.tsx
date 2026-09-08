import { useBlocker } from "@tanstack/react-router";
import { useAtomValue } from "jotai";
import { Pencil, Play, Plus, Trash2 } from "lucide-react";
import type { Monaco } from "@monaco-editor/react";
import type { editor } from "monaco-editor";
import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";
import { applyAssetTransaction } from "@/lib/api-asset-transactions";
import { fetchJSON, fetchJSONWithBody } from "@/lib/api-core";
import { selectedEnvironmentAtom } from "@/lib/atoms/workspace";
import type {
  SQLUnitTest,
  SQLUnitTestContext,
  SQLUnitTestResult,
  SQLUnitTestRunResponse,
  WebAsset,
} from "@/lib/generated/api-types";
import { loadMonacoEditorModule } from "@/lib/load-monaco-editor";
import { defineBruinMonacoThemes } from "@/lib/monaco-theme";
import { sqlUnitTestSchema } from "@/lib/sql-unit-test-schema";
import { parseUnitTestYAML, stringifyUnitTestYAML } from "@/lib/sql-unit-test-yaml";
import { registerUnitTestYAML } from "@/lib/monaco-unit-test-yaml";
import { awaitWorkspaceSaves } from "@/lib/workspace-save-barrier";

const MonacoEditor = lazy(async () => ({ default: (await loadMonacoEditorModule()).default }));
const message = (error: unknown) =>
  error instanceof Error ? error.message : "The request failed.";

export function AssetUnitTests({ asset }: { asset: WebAsset }) {
  const environment = useAtomValue(selectedEnvironmentAtom);
  const [context, setContext] = useState<SQLUnitTestContext>();
  const [error, setError] = useState("");
  const [results, setResults] = useState<SQLUnitTestResult[]>([]);
  const [busy, setBusy] = useState(false);
  const [editing, setEditing] = useState<{ context: SQLUnitTestContext; index: number }>();
  const execution = useRef<AbortController | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    setContext(undefined);
    setError("");
    setResults([]);
    void fetchJSON<SQLUnitTestContext>(`/api/assets/${asset.id}/unit-tests`, {
      signal: controller.signal,
    })
      .then((response) => {
        if (!controller.signal.aborted) setContext(response);
      })
      .catch((error) => {
        if (!controller.signal.aborted) setError(message(error));
      });
    return () => {
      controller.abort();
      execution.current?.abort();
    };
  }, [asset.id, asset.unit_tests_revision, asset.content, environment]);

  async function run() {
    if (!context) return;
    setBusy(true);
    setError("");
    setResults([]);
    const controller = new AbortController();
    execution.current = controller;
    try {
      await awaitWorkspaceSaves();
      const response = await fetchJSONWithBody<SQLUnitTestRunResponse>(
        `/api/assets/${asset.id}/unit-tests/run`,
        "POST",
        { environment, revision: context.revision },
        { signal: controller.signal },
      );
      if (!controller.signal.aborted) setResults(response.results);
    } catch (error) {
      if (!controller.signal.aborted) setError(message(error));
    } finally {
      setBusy(false);
    }
  }
  async function remove(index: number) {
    if (!context || !window.confirm(`Remove test “${context.tests[index].name}”?`)) return;
    setBusy(true);
    setError("");
    try {
      const tests = context.tests.filter((_, i) => i !== index);
      const result = await applyAssetTransaction(asset.id, {
        type: "unit_tests.set",
        unit_tests: tests,
        expected_unit_tests_revision: context.revision,
      });
      setContext({
        ...context,
        tests: result.unit_tests ?? tests,
        revision: result.unit_tests_revision!,
      });
      setResults([]);
    } catch (error) {
      setError(message(error));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="space-y-3 p-3" data-testid="asset-unit-tests">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-xs font-medium">SQL unit tests</p>
        <div className="flex gap-1">
          <Button
            size="xs"
            variant="outline"
            disabled={!context || busy}
            onClick={() => context && setEditing({ context, index: -1 })}
          >
            <Plus className="size-3" /> Add test
          </Button>
          <Button size="xs" disabled={!context?.tests.length || busy} onClick={() => void run()}>
            <Play className="size-3" />
            {busy ? "Working…" : "Run tests"}
          </Button>
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        Test saved SQL with small mock inputs on {environment || "the selected environment"}. No
        asset is materialized.
      </p>
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
      {!context && !error && (
        <p role="status" className="text-xs text-muted-foreground">
          Loading test schemas…
        </p>
      )}
      {context?.tests.length === 0 && (
        <p className="text-xs text-muted-foreground">
          Add inputs and the rows or count you expect. Column names and values are checked against
          the asset schemas.
        </p>
      )}
      {context?.tests.map((test, index) => {
        const result = results.find((result) => result.name === test.name);
        return (
          <div key={test.name} className="space-y-2 rounded-md border p-2.5">
            <div className="flex items-start gap-2">
              <div className="min-w-0 flex-1">
                <p className="break-words text-xs font-medium">{test.name}</p>
                {test.description && (
                  <p className="text-xs text-muted-foreground">{test.description}</p>
                )}
              </div>
              <Button
                size="icon-xs"
                variant="ghost"
                aria-label={`Edit test ${test.name}`}
                disabled={busy}
                onClick={() => setEditing({ context, index })}
              >
                <Pencil className="size-3" />
              </Button>
              <Button
                size="icon-xs"
                variant="ghost"
                aria-label={`Remove test ${test.name}`}
                disabled={busy}
                onClick={() => void remove(index)}
              >
                <Trash2 className="size-3" />
              </Button>
            </div>
            {result && (
              <div
                role="status"
                className={result.status === "passed" ? "text-xs" : "text-xs text-destructive"}
              >
                {result.status === "passed"
                  ? "Passed"
                  : result.status === "failed"
                    ? "Assertion failed"
                    : "Could not run"}
                {result.message && (
                  <pre className="mt-1 max-h-48 overflow-auto whitespace-pre-wrap break-words font-mono text-xs">
                    {result.message}
                  </pre>
                )}
              </div>
            )}
          </div>
        );
      })}
      {editing && (
        <UnitTestEditor
          key={`${asset.id}:${editing.index}`}
          asset={asset}
          context={editing.context}
          index={editing.index}
          onClose={() => setEditing(undefined)}
          onSaved={(tests, revision) => {
            setContext({ ...editing.context, tests, revision });
            setResults([]);
            setEditing(undefined);
          }}
        />
      )}
    </div>
  );
}

function UnitTestEditor({
  asset,
  context,
  index,
  onClose,
  onSaved,
}: {
  asset: WebAsset;
  context: SQLUnitTestContext;
  index: number;
  onClose: () => void;
  onSaved: (tests: SQLUnitTest[], revision: string) => void;
}) {
  const initial = useMemo(
    () =>
      stringifyUnitTestYAML(
        index < 0
          ? {
              name: "New test",
              inputs: context.inputs.map((input) => ({ asset: input.asset, rows: [] })),
              expected: { count: 0 },
            }
          : context.tests[index],
      ),
    [context, index],
  );
  const [draft, setDraft] = useState(initial);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const [invalid, setInvalid] = useState(false);
  const [diagnostic, setDiagnostic] = useState("");
  const { monacoTheme } = useWorkspaceTheme();
  const [monaco, setMonaco] = useState<Monaco>();
  const [path] = useState(() => `renart-unit-tests://fixtures/${crypto.randomUUID()}.yaml`);
  const schema = useMemo(() => sqlUnitTestSchema(context), [context]);
  const accepted = useRef(false);
  const dirty = draft !== initial;
  useBlocker({
    shouldBlockFn: () =>
      dirty && !accepted.current && !window.confirm("Discard unsaved test changes?"),
    enableBeforeUnload: dirty && !accepted.current,
  });
  useEffect(() => {
    if (!monaco) return;
    const service = registerUnitTestYAML(monaco, path, schema);
    return () => service.dispose();
  }, [monaco, path, schema]);
  function close() {
    if (saving || (dirty && !window.confirm("Discard unsaved test changes?"))) return;
    onClose();
  }
  async function save() {
    setError("");
    const model = monaco?.editor.getModel(monaco.Uri.parse(path));
    if (
      !model ||
      monaco!.editor
        .getModelMarkers({ resource: model.uri })
        .some((marker: editor.IMarker) => marker.severity >= monaco!.MarkerSeverity.Warning)
    ) {
      setError("Fix the highlighted fixture errors before saving.");
      return;
    }
    setSaving(true);
    try {
      const definition = parseUnitTestYAML(draft);
      const tests = [...context.tests];
      if (index < 0) tests.push(definition);
      else tests[index] = definition;
      const result = await applyAssetTransaction(asset.id, {
        type: "unit_tests.set",
        unit_tests: tests,
        expected_unit_tests_revision: context.revision,
      });
      accepted.current = true;
      onSaved(result.unit_tests ?? tests, result.unit_tests_revision!);
    } catch (error) {
      setError(message(error));
    } finally {
      setSaving(false);
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) close();
      }}
    >
      <DialogContent className="flex max-h-[90dvh] flex-col sm:max-w-3xl">
        <DialogHeader>
          <DialogTitle>{index < 0 ? "Add SQL unit test" : "Edit SQL unit test"}</DialogTitle>
          <DialogDescription>
            Mock upstream rows and describe the expected output. Ctrl+Space suggests fields and
            column names.
          </DialogDescription>
        </DialogHeader>
        <FieldGroup className="min-h-0">
          <Field>
            <FieldLabel htmlFor="unit-test-fixture">Test fixture · YAML</FieldLabel>
            <div
              id="unit-test-fixture"
              className="h-[min(50dvh,26rem)] overflow-hidden rounded-md border"
              data-testid="unit-test-fixture-editor"
            >
              <Suspense
                fallback={<p className="p-3 text-xs text-muted-foreground">Loading editor…</p>}
              >
                <MonacoEditor
                  path={path}
                  language="yaml"
                  value={draft}
                  theme={monacoTheme}
                  beforeMount={defineBruinMonacoThemes}
                  onMount={(_, instance) => setMonaco(instance)}
                  onChange={(value) => setDraft(value ?? "")}
                  onValidate={(markers) => {
                    // Both syntax errors and schema/type warnings invalidate
                    // this fixture, without changing other editors' severities.
                    const first = markers.find((marker) => marker.severity >= 4);
                    setInvalid(Boolean(first));
                    setDiagnostic(first ? `Line ${first.startLineNumber}: ${first.message}` : "");
                  }}
                  options={{
                    ariaLabel: "SQL unit test fixture",
                    minimap: { enabled: false },
                    fontSize: 12,
                    scrollBeyondLastLine: false,
                    automaticLayout: true,
                    tabSize: 2,
                    readOnly: saving,
                  }}
                />
              </Suspense>
            </div>
            <FieldDescription>
              Expected rows may assert selected columns. Use match: "exact" to reject additional
              rows. Unknown upstream and CTE row types cannot be checked here.
            </FieldDescription>
          </Field>
        </FieldGroup>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        {diagnostic && (
          <p role="status" className="text-xs text-destructive">
            {diagnostic}
          </p>
        )}
        <DialogFooter>
          <Button variant="outline" disabled={saving} onClick={close}>
            Cancel
          </Button>
          <Button disabled={saving || invalid || !monaco} onClick={() => void save()}>
            {saving ? "Saving…" : "Save test"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
