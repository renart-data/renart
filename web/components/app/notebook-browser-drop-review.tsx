import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
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
import { Input } from "@/components/ui/input";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import type { NotebookBrowserDrop } from "@/hooks/use-notebook-browser-drop";

export function NotebookBrowserDropReview({ controller }: { controller: NotebookBrowserDrop }) {
  const { review } = controller;
  const operation = review?.plan?.change_set.operations[0];
  const [name, setName] = useState("");
  const [mode, setMode] = useState("full");
  const [limit, setLimit] = useState(10000);
  useEffect(() => {
    if (!operation) return;
    setName(operation?.name || "");
    setMode(operation?.source?.snapshot.mode || operation?.snapshot_mode || "full");
    setLimit(operation?.source?.snapshot.row_limit || operation?.row_limit || 10000);
  }, [operation]);
  const changed = Boolean(
    operation &&
    (name !== operation.name ||
      mode !== (operation.source?.snapshot.mode || operation.snapshot_mode || "full") ||
      (mode === "sample" &&
        limit !== (operation.source?.snapshot.row_limit || operation.row_limit))),
  );
  return (
    <Dialog
      open={Boolean(review)}
      onOpenChange={(open) => {
        if (!open && review?.busy !== "apply") controller.close();
      }}
    >
      <DialogContent className="max-h-[85dvh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Add source to notebook</DialogTitle>
          <DialogDescription>
            Review the source block. Data is only read when you run it.
          </DialogDescription>
        </DialogHeader>
        {review ? (
          <>
            <div className="min-w-0 text-sm">
              <p className="break-all font-medium">{review.label}</p>
              <p className="text-xs text-muted-foreground">
                {operation?.connection ||
                  operation?.source?.connection ||
                  (operation ? "Project files" : "Source")}{" "}
                · {review.request.environment} ·{" "}
                {review.request.position === "start"
                  ? "At the beginning"
                  : "At the selected insertion point"}
              </p>
            </div>
            {review.busy === "prepare" ? (
              <p role="status" className="flex items-center gap-2 text-sm text-muted-foreground">
                <Loader2 className="size-4 animate-spin" />
                Preparing source…
              </p>
            ) : null}
            {review.error ? (
              <Alert variant="destructive">
                <AlertDescription>{review.error}</AlertDescription>
              </Alert>
            ) : null}
            {operation ? (
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="notebook-drop-name">Block name</FieldLabel>
                  <Input
                    id="notebook-drop-name"
                    value={name}
                    onChange={(event) => setName(event.target.value)}
                    disabled={Boolean(review.busy)}
                  />
                </Field>
                <Field>
                  <FieldLabel id="notebook-drop-snapshot">Snapshot</FieldLabel>
                  <ToggleGroup
                    type="single"
                    value={mode}
                    onValueChange={(value) => {
                      if (value) setMode(value);
                    }}
                    aria-labelledby="notebook-drop-snapshot"
                    disabled={Boolean(review.busy)}
                  >
                    <ToggleGroupItem value="full">Full data</ToggleGroupItem>
                    <ToggleGroupItem value="sample">Sample</ToggleGroupItem>
                  </ToggleGroup>
                  <FieldDescription>
                    Copied into the notebook’s local session on Run, within its transfer limits.
                  </FieldDescription>
                </Field>
                {mode === "sample" ? (
                  <Field>
                    <FieldLabel htmlFor="notebook-drop-limit">Maximum rows</FieldLabel>
                    <Input
                      id="notebook-drop-limit"
                      type="number"
                      min={1}
                      value={limit}
                      onChange={(event) => setLimit(Number(event.target.value))}
                      disabled={Boolean(review.busy)}
                    />
                  </Field>
                ) : null}
                <details className="min-w-0 text-xs">
                  <summary className="cursor-pointer text-muted-foreground">
                    Source definition
                  </summary>
                  <pre className="mt-2 max-h-48 overflow-auto rounded-md bg-muted p-3">
                    {operation.content}
                  </pre>
                </details>
              </FieldGroup>
            ) : null}
            <DialogFooter>
              <Button
                variant="outline"
                disabled={review.busy === "apply"}
                onClick={controller.close}
              >
                Cancel
              </Button>
              {changed || !review.plan ? (
                <Button
                  disabled={
                    Boolean(review.busy) ||
                    (mode === "sample" && (!Number.isSafeInteger(limit) || limit <= 0))
                  }
                  onClick={() =>
                    void controller.prepare(
                      {
                        ...review.request,
                        name: name || undefined,
                        snapshot_mode: mode,
                        row_limit: mode === "sample" ? limit : undefined,
                      },
                      review.label,
                    )
                  }
                >
                  Review source
                </Button>
              ) : (
                <Button
                  disabled={Boolean(review.busy) || !review.plan.can_apply}
                  onClick={() => void controller.apply()}
                >
                  {review.busy === "apply" ? (
                    <Loader2 data-icon="inline-start" className="animate-spin" />
                  ) : null}
                  Add source
                </Button>
              )}
            </DialogFooter>
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
