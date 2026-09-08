import { useEffect, useMemo, useState } from "react";
import { Link } from "@tanstack/react-router";
import { fetchJSON } from "@/lib/api-core";
import type { ConnectionAccessPreview as Preview } from "@/lib/generated/api-types";
import { getPinnedProjectId } from "@/lib/project-context";
import { usePipelineRuns } from "@/hooks/use-pipeline-runs";

export function ConnectionAccessPreview({
  environment,
  connection,
}: {
  environment: string;
  connection: string;
}) {
  const [preview, setPreview] = useState<Preview | null>(null);
  const [error, setError] = useState("");
  const query = useMemo(
    () => ({ environment, status: "running" as const, limit: 50 }),
    [environment],
  );
  const { runs } = usePipelineRuns({ runsQuery: query });
  useEffect(() => {
    const abort = new AbortController();
    const params = new URLSearchParams({ environment, connection });
    setPreview(null);
    setError("");
    void fetchJSON<Preview>(`/api/config/connections/access-preview?${params}`, {
      signal: abort.signal,
    })
      .then(setPreview)
      .catch((cause) => {
        if (!abort.signal.aborted)
          setError(cause instanceof Error ? cause.message : "Could not preview affected assets.");
      });
    return () => abort.abort();
  }, [environment, connection]);
  if (error)
    return (
      <p role="status" className="text-xs text-muted-foreground">
        {error} You can still save the restriction.
      </p>
    );
  if (!preview)
    return (
      <p role="status" className="text-xs text-muted-foreground">
        Checking affected assets…
      </p>
    );
  const pipelines = new Set(preview.assets.map((asset) => asset.pipeline));
  const active = runs.filter((run) => pipelines.has(run.pipeline));
  return (
    <div className="space-y-2 text-xs text-muted-foreground" data-testid="connection-access-impact">
      <details>
        <summary className="cursor-pointer">
          {preview.assets.length
            ? `${preview.assets.length} assets will need a writable connection`
            : "No writes found in the current pipeline definitions"}
        </summary>
        <ul className="mt-2 max-h-36 space-y-1 overflow-auto">
          {preview.assets.map((asset) => (
            <li key={`${asset.pipeline}/${asset.asset}`}>
              {asset.asset}
              {asset.scheduled ? " · pipeline defines a schedule" : ""}
            </li>
          ))}
        </ul>
        {preview.warnings.map((warning) => (
          <p key={warning}>{warning}</p>
        ))}
        <p className="mt-2">
          Preview of saved source. Deployed versions are checked again when they run.
        </p>
      </details>
      {active.map((run) => (
        <p key={run.id}>
          <Link
            to="/runs/$runId"
            params={{ runId: run.id }}
            search={{ project: getPinnedProjectId() ?? undefined }}
            className="underline"
          >
            Review or cancel the active run of {run.pipeline}
          </Link>
        </p>
      ))}
      <p>
        Operations already in progress may finish. New tasks and scheduled runs use the saved access
        mode. Cancel active runs separately if needed.
      </p>
    </div>
  );
}
