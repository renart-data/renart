import { CheckCircle2, ChevronRight, Circle, LoaderCircle, Play, XCircle } from "lucide-react";
import { useState, type ReactNode } from "react";

import { AnsiOutput } from "@/components/ansi-output";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { useFollowOutputScroll } from "@/hooks/use-follow-output-scroll";
import { cn } from "@/lib/utils";

import { formatDuration, type WelcomeState } from "./welcome-flow";

type ItemState = "pending" | "active" | "done" | "failed";

// Each line reports a real step of creating and running the project, in the
// order the page performs them; none is simulated.
export function WelcomeRun({
  state,
  driverDownloaded,
  onRetry,
  onOpenAnyway,
}: {
  state: WelcomeState;
  // The first run downloaded DuckDB's driver.
  driverDownloaded: boolean;
  onRetry: () => void;
  onOpenAnyway: () => void;
}) {
  const [showOutput, setShowOutput] = useState(false);
  const { run, created } = state;
  const outputScroll = useFollowOutputScroll(run.log, created?.pipeline_id);
  const assetCount = Object.keys(run.assets).length;
  const builtCount = Object.values(run.assets).filter((status) => status === "succeeded").length;
  const failedAssets = Object.entries(run.assets)
    .filter(([, status]) => status === "failed")
    .map(([name]) => name);
  const phase = run.phase;
  const createState: ItemState = created ? "done" : phase === "creating" ? "active" : "pending";
  const prepareState: ItemState =
    run.failedAt === "prepare"
      ? "failed"
      : phase === "preparing" || phase === "downloading-driver"
        ? "active"
        : created && phase !== "creating"
          ? "done"
          : "pending";
  const runState: ItemState =
    run.failedAt === "run"
      ? "failed"
      : phase === "running"
        ? "active"
        : phase === "succeeded"
          ? "done"
          : "pending";
  const elapsed =
    run.startedAt !== null && run.finishedAt !== null ? run.finishedAt - run.startedAt : null;

  return (
    <>
      <div>
        <h2 className="text-lg font-semibold tracking-tight">
          {phase === "succeeded"
            ? "Your pipeline is ready"
            : phase === "failed"
              ? "The first run stopped"
              : "Creating your project"}
        </h2>
        <p className="mt-1 text-sm text-muted-foreground">
          {phase === "succeeded"
            ? "Opening the workspace..."
            : phase === "failed"
              ? "The files are in place. Retry the run, or open the workspace and run assets from there."
              : "Renart writes the files, then builds every asset once so you land on real data."}
        </p>
      </div>

      <ol className="grid gap-0.5 rounded-xl border bg-background p-2" aria-label="Setup steps">
        <ChecklistItem
          state={createState}
          title="Project files"
          detail={
            created
              ? `${created.files.length} files, including ${created.pipeline_path}/pipeline.yml`
              : "Writing the pipeline, its assets and the project config"
          }
        />
        <ChecklistItem
          state={createState}
          title="Local DuckDB connection"
          detail={<code className="font-mono">duckdb-default</code>}
        />
        <ChecklistItem
          state={createState}
          title="Git"
          detail={
            created
              ? created.git_initialized
                ? "New repository with a first commit"
                : "Uses the repository this folder is in"
              : "Every file is plain text you can review and commit"
          }
        />
        <ChecklistItem
          state={prepareState}
          title="DuckDB"
          detail={
            phase === "downloading-driver"
              ? "Downloading DuckDB's driver, first time only (about 70 MB)"
              : prepareState === "failed"
                ? "The driver could not be downloaded"
                : driverDownloaded
                  ? "Driver downloaded; later runs reuse it"
                  : prepareState === "done"
                    ? "Ready"
                    : "Checking the local engine"
          }
        />
        <ChecklistItem
          state={runState}
          title="First run"
          detail={
            runState === "done"
              ? `${builtCount} of ${assetCount} assets built${elapsed !== null ? ` in ${formatDuration(elapsed)}` : ""}`
              : runState === "failed"
                ? failedAssets.length > 0
                  ? `Failed at ${failedAssets.join(", ")}`
                  : "The run did not finish"
                : runState === "active"
                  ? `${builtCount} of ${assetCount} assets built`
                  : "Builds every asset in dependency order"
          }
        />
      </ol>

      {state.error ? (
        <p
          role="alert"
          className="rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-xs text-destructive"
        >
          {state.error}
        </p>
      ) : null}

      {run.log ? (
        <div className="grid gap-2">
          <button
            type="button"
            aria-expanded={showOutput}
            onClick={() => setShowOutput((value) => !value)}
            className="flex w-fit items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
          >
            <ChevronRight
              className={cn(
                "size-3.5 transition-transform duration-150",
                showOutput && "rotate-90",
              )}
            />
            {showOutput ? "Hide output" : "Show output"}
          </button>
          {showOutput ? (
            <ScrollArea
              className="h-56 rounded-md border bg-zinc-950"
              viewportClassName="max-h-56"
              viewportRef={outputScroll.viewportRef}
              onViewportScroll={outputScroll.onViewportScroll}
            >
              <AnsiOutput
                output={run.log}
                className="whitespace-pre-wrap p-3 font-mono text-[11px] leading-relaxed text-zinc-300"
              />
            </ScrollArea>
          ) : null}
        </div>
      ) : null}

      {phase === "failed" ? (
        <div className="flex flex-wrap justify-end gap-2">
          <Button variant="outline" onClick={onOpenAnyway}>
            Open anyway
          </Button>
          <Button onClick={onRetry}>
            <Play data-icon="inline-start" />
            Retry
          </Button>
        </div>
      ) : null}
    </>
  );
}

function ChecklistItem({
  state,
  title,
  detail,
}: {
  state: ItemState;
  title: string;
  detail: ReactNode;
}) {
  return (
    <li
      className={cn(
        "flex min-w-0 items-start gap-3 rounded-lg px-2.5 py-2 transition-colors duration-150",
        state === "active" && "bg-muted/50",
      )}
    >
      <span className="mt-0.5 shrink-0">
        {state === "done" ? (
          <CheckCircle2 className="size-4 text-primary motion-safe:animate-in motion-safe:zoom-in-50 motion-safe:duration-150" />
        ) : state === "active" ? (
          <LoaderCircle className="size-4 animate-spin text-primary" />
        ) : state === "failed" ? (
          <XCircle className="size-4 text-destructive" />
        ) : (
          <Circle className="size-4 text-muted-foreground/40" />
        )}
      </span>
      <span className="min-w-0">
        <span
          className={cn(
            "block text-sm",
            state === "pending" ? "text-muted-foreground" : "font-medium",
          )}
        >
          {title}
        </span>
        <span className="block truncate text-xs text-muted-foreground">{detail}</span>
      </span>
    </li>
  );
}
