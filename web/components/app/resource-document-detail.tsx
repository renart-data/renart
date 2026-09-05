import { Link } from "@tanstack/react-router";
import { useEffect, useState } from "react";
import { getNotebook } from "@/lib/api-notebooks";
import { getPresentation } from "@/lib/api-presentations";
import type { DocumentTarget } from "@/lib/resource-navigation";
import { ScrollArea } from "@/components/ui/scroll-area";

// Saved definitions only: no notebook runtime, preview or presentation execution
// is mounted just to follow a diagnostic. Persisted IDs survive block reordering.
export function ResourceDocumentDetail({ target }: { target: DocumentTarget }) {
  const [value, setValue] = useState<{
    title: string;
    content: string;
    kind: string;
    missing: boolean;
  } | null>(null);
  const [error, setError] = useState("");
  const key = JSON.stringify(target);
  useEffect(() => {
    const abort = new AbortController();
    setValue(null);
    setError("");
    const t: DocumentTarget = JSON.parse(key);
    const load = async () => {
      if (t.kind === "notebook-cell") {
        const notebook = await getNotebook(t.notebook_id, abort.signal);
        const cells = notebook.cells.filter((c) => c.cell_id === t.cell_id);
        return {
          title: cells.length === 1 ? cells[0].name : notebook.title,
          content: cells.length === 1 ? cells[0].content : "",
          kind: "notebook",
          missing: cells.length !== 1,
        };
      }
      const document = await getPresentation(t.presentation_id, abort.signal);
      const blocks = document.artifact.visualizations?.filter((b) => b.id === t.block_id) ?? [];
      const block = t.block_id && blocks.length === 1 ? blocks[0] : undefined;
      return {
        title: block?.id ?? document.artifact.title,
        content: JSON.stringify(block ?? document.artifact, null, 2),
        kind: document.artifact.kind,
        missing: Boolean(t.block_id && !block),
      };
    };
    void load()
      .then((result) => {
        if (!abort.signal.aborted) setValue(result);
      })
      .catch((cause) => {
        if (!abort.signal.aborted)
          setError(cause instanceof Error ? cause.message : "Could not load saved definition.");
      });
    return () => abort.abort();
  }, [key]);
  if (error)
    return (
      <p role="alert" className="p-3">
        {error}
      </p>
    );
  if (!value)
    return (
      <p role="status" className="p-3">
        Loading saved definition…
      </p>
    );
  return (
    <ScrollArea className="min-h-0 flex-1">
      <div className="space-y-3 p-3" data-testid="routed-document">
        <h3 className="font-medium">{value.title}</h3>
        <p className="text-xs text-muted-foreground">
          Current saved definition. No data has been executed by opening this detail.
        </p>
        {value.missing ? (
          <p role="alert" className="text-sm">
            The linked block is missing or ambiguous. No other block has been selected.
          </p>
        ) : null}
        {target.kind === "notebook-cell" ? (
          <Link
            to="/notebooks/$notebookId"
            params={{ notebookId: target.notebook_id }}
            search={(s) => ({ ...s, detail: undefined })}
            className="text-sm text-primary underline"
          >
            Open notebook editor
          </Link>
        ) : (
          <Link
            to={
              value.kind === "dashboard"
                ? "/dashboards/$presentationId"
                : "/reports/$presentationId"
            }
            params={{ presentationId: target.presentation_id }}
            search={(s) => ({ ...s, detail: undefined })}
            className="text-sm text-primary underline"
          >
            Open presentation editor
          </Link>
        )}
        <pre className="overflow-auto text-xs">{value.content}</pre>
      </div>
    </ScrollArea>
  );
}
