"use client";

import { Link } from "@tanstack/react-router";
import { AlertTriangle, Play } from "lucide-react";

import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Spinner } from "@/components/ui/spinner";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { useFollowOutputScroll } from "@/hooks/use-follow-output-scroll";
import { MaterializeHistoryEntry } from "@/lib/atoms/results";

export function WorkspaceMaterializeOutputView({
  entry,
  outputHtml,
  pipelineMaterializeLoading,
}: {
  entry: MaterializeHistoryEntry | null;
  outputHtml: string;
  pipelineMaterializeLoading?: boolean;
}) {
  const materializeOutputScroll = useFollowOutputScroll(
    `${entry?.loading ?? false}:${outputHtml}`,
    entry?.id,
  );

  if (!entry) {
    return (
      <div className="flex h-full min-h-0 items-center justify-center rounded border border-dashed bg-background px-4 text-center text-sm text-muted-foreground">
        Select a materialize run from the history or run an asset to see output here.
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col gap-2">
      {entry.loading && pipelineMaterializeLoading ? (
        <div className="flex items-center gap-2 rounded border border-primary/30 bg-primary/5 px-2 py-1 text-xs text-primary">
          <Play className="size-3.5 animate-pulse fill-current" />
          Running pipeline...
        </div>
      ) : null}
      {entry.runId ? (
        <div className="flex items-center justify-between gap-2 rounded border bg-muted/30 px-2 py-1 text-xs text-muted-foreground">
          <span className="min-w-0 truncate font-mono">Run {entry.runId}</span>
          <Button asChild variant="outline" size="xs">
            <Link to="/runs/$runId" params={{ runId: entry.runId }}>
              Open run
            </Link>
          </Button>
        </div>
      ) : null}
      <div
        className={`min-h-0 flex flex-1 flex-col overflow-hidden rounded border ${
          entry.status === "error" ? "border-destructive/40 bg-destructive/5" : "bg-background"
        }`}
      >
        <ScrollArea
          className="min-h-0 flex-1"
          viewportClassName="p-2"
          viewportRef={materializeOutputScroll.viewportRef}
          onViewportScroll={materializeOutputScroll.onViewportScroll}
        >
          <div className="space-y-2">
            {entry.timeWindow ? (
              <div className="rounded border bg-muted/30 px-2 py-1 text-xs text-muted-foreground">
                Period: {new Date(entry.timeWindow.start).toLocaleString()} -{" "}
                {new Date(entry.timeWindow.end).toLocaleString()}
              </div>
            ) : null}
            {entry.status === "error" ? (
              <div className="rounded border border-destructive/40 bg-destructive/10 px-2 py-1 text-xs text-destructive">
                Materialization failed
                {entry.error ? `: ${entry.error}` : ""}
              </div>
            ) : null}
            {entry.warnings && entry.warnings.length > 0 ? (
              <Alert>
                <AlertTriangle />
                <AlertTitle>Completed with warnings</AlertTitle>
                <AlertDescription>
                  {entry.warnings.map((warning) => (
                    <p key={warning}>{warning}</p>
                  ))}
                </AlertDescription>
              </Alert>
            ) : null}
            <pre
              className="font-console whitespace-pre-wrap text-[11px]"
              dangerouslySetInnerHTML={{ __html: outputHtml }}
            />
          </div>
        </ScrollArea>
        {entry.loading ? (
          <div className="font-console flex items-center gap-2 border-t bg-muted/40 px-2 py-1 text-[11px] text-muted-foreground">
            <Spinner className="size-3.5" />
            <span>
              {entry.kind === "pipeline"
                ? "Waiting for pipeline output..."
                : "Waiting for asset output..."}
            </span>
          </div>
        ) : null}
      </div>
    </div>
  );
}
