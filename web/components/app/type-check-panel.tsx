import { Link } from "@tanstack/react-router";
import { ResourceLink } from "./resource-link";
import { useState } from "react";
import {
  AlertTriangle,
  Bell,
  CheckCircle2,
  Download,
  ExternalLink,
  Link2,
  Loader2,
  RotateCw,
  Trash2,
  XCircle,
} from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
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
import { ScrollArea } from "@/components/ui/scroll-area";
import { Spinner } from "@/components/ui/spinner";
import { applyAssetTransaction } from "@/lib/api-asset-transactions";
import type {
  PipelineTypeCheckFinding,
  PipelineTypeCheckReport,
  PipelineTypeCheckResolution,
} from "@/lib/api-pipelines";

export function TypeCheckPanel({
  report,
  loading,
  error,
  onRun,
  onSelectAsset,
  onResolutionAction,
}: {
  report: PipelineTypeCheckReport | null;
  loading: boolean;
  error: string | null;
  onRun?: () => void;
  onSelectAsset?: (assetId: string) => void;
  onResolutionAction?: (action: NonNullable<PipelineTypeCheckResolution["action"]>) => void;
}) {
  const [resolving, setResolving] = useState("");
  const [resolutionError, setResolutionError] = useState<string | null>(null);

  const resolveFinding = async (
    assetId: string,
    finding: PipelineTypeCheckFinding,
    resolution: PipelineTypeCheckResolution,
  ) => {
    const key = `${assetId}:${finding.code}:${resolution.id}`;
    setResolving(key);
    setResolutionError(null);
    try {
      if (!resolution.transaction) {
        throw new Error("This suggested metadata edit is unavailable.");
      }
      await applyAssetTransaction(assetId, resolution.transaction);
      onRun?.();
    } catch (cause) {
      setResolutionError(
        cause instanceof Error ? cause.message : "The suggested change could not be applied.",
      );
    } finally {
      setResolving("");
    }
  };

  if (loading && !report) {
    return <TypeCheckLoading />;
  }
  if (!report) {
    return (
      <div className="flex h-full min-h-0 flex-col items-center justify-center gap-3 bg-background p-3 text-xs text-muted-foreground">
        {error ? (
          <Alert variant="destructive" className="max-w-lg">
            <AlertTriangle />
            <AlertTitle>Type check failed</AlertTitle>
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : (
          <span>Type check assets for column and type errors.</span>
        )}
        <Button size="sm" variant="outline" onClick={onRun}>
          <Bell />
          {error ? "Retry type check" : "Run type check"}
        </Button>
      </div>
    );
  }

  const flagged = report.assets.filter((asset) => asset.findings.length > 0);
  const flaggedPresentations = (report.presentations ?? []).filter(
    (artifact) => artifact.findings.length > 0,
  );
  const checkedAt = report.start_date ? new Date(report.start_date) : null;

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <div className="flex shrink-0 items-center gap-2 border-b px-3 py-1.5 text-xs">
        <span className="inline-flex items-center gap-1 text-red-600 dark:text-red-400">
          <XCircle className="size-3.5" />
          {report.summary.errors}
        </span>
        <span className="inline-flex items-center gap-1 text-amber-600 dark:text-amber-400">
          <AlertTriangle className="size-3.5" />
          {report.summary.warnings}
        </span>
        <span className="text-muted-foreground">
          {report.summary.assets} asset{report.summary.assets === 1 ? "" : "s"} checked
          {(report.summary.presentations ?? 0) > 0
            ? ` · ${report.summary.presentations} presentation${report.summary.presentations === 1 ? "" : "s"}`
            : ""}
        </span>
        {checkedAt ? (
          <span className="hidden text-muted-foreground/70 sm:inline">
            · window {checkedAt.toISOString().slice(0, 10)}
          </span>
        ) : null}
        <Button size="xs" variant="outline" className="ml-auto" onClick={onRun} disabled={loading}>
          {loading ? <Loader2 className="animate-spin" /> : <RotateCw />}
          Re-run
        </Button>
      </div>
      {error ? (
        <Alert variant="destructive" className="m-2 shrink-0 w-auto">
          <AlertTriangle />
          <AlertTitle>Latest type check failed</AlertTitle>
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      ) : null}
      {resolutionError ? (
        <Alert variant="destructive" className="m-2 shrink-0 w-auto">
          <AlertTriangle />
          <AlertTitle>Resolution failed</AlertTitle>
          <AlertDescription>{resolutionError}</AlertDescription>
        </Alert>
      ) : null}
      <ScrollArea
        className="min-h-0 flex-1"
        viewportClassName="h-full"
        data-testid="type-check-scroll-area"
      >
        <div className="p-2">
          {flagged.length === 0 && flaggedPresentations.length === 0 ? (
            <div className="flex items-center gap-2 px-2 py-3 text-xs text-emerald-600 dark:text-emerald-400">
              <CheckCircle2 className="size-4" />
              No type errors found across {report.summary.assets} asset
              {report.summary.assets === 1 ? "" : "s"}
              {(report.summary.presentations ?? 0) > 0
                ? ` and ${report.summary.presentations} presentation${report.summary.presentations === 1 ? "" : "s"}`
                : ""}
              .
            </div>
          ) : (
            <div className="flex flex-col gap-2">
              {flagged.map((asset) => (
                <div key={asset.name} className="rounded-md border">
                  <button
                    type="button"
                    className="flex w-full items-center gap-2 border-b bg-muted/30 px-2.5 py-1.5 text-left text-xs hover:bg-muted disabled:cursor-default"
                    onClick={() => asset.id && onSelectAsset?.(asset.id)}
                    disabled={!asset.id}
                  >
                    {asset.status === "error" ? (
                      <XCircle className="size-3.5 shrink-0 text-red-500" />
                    ) : (
                      <AlertTriangle className="size-3.5 shrink-0 text-amber-500" />
                    )}
                    <span className="min-w-0 flex-1 truncate font-mono font-medium">
                      {asset.name}
                    </span>
                    <span className="shrink-0 text-[10px] text-muted-foreground">{asset.type}</span>
                  </button>
                  <ul className="divide-y">
                    {asset.findings.map((finding, index) => (
                      <li key={index} className="flex items-start gap-2 px-2.5 py-1.5 text-xs">
                        {finding.severity === "error" ? (
                          <XCircle className="mt-0.5 size-3.5 shrink-0 text-red-500" />
                        ) : (
                          <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-amber-500" />
                        )}
                        <div className="min-w-0 flex-1">
                          <p>
                            {finding.message}
                            {finding.target ? <ResourceLink target={finding.target} /> : null}
                          </p>
                          {finding.resolutions?.length ? (
                            <div className="mt-1.5 flex flex-wrap gap-1.5">
                              {finding.resolutions.map((resolution) => {
                                const resolutionKey = `${asset.id ?? asset.name}:${finding.code}:${resolution.id}`;
                                if (resolution.action?.type === "import-external-relation") {
                                  return (
                                    <Button
                                      key={resolution.id}
                                      type="button"
                                      variant="outline"
                                      size="xs"
                                      disabled={!onResolutionAction}
                                      onClick={() => onResolutionAction?.(resolution.action!)}
                                    >
                                      <Download />
                                      {resolution.title}
                                    </Button>
                                  );
                                }
                                if (resolution.action?.type === "open-asset") {
                                  return (
                                    <Button
                                      key={resolution.id}
                                      type="button"
                                      variant="outline"
                                      size="xs"
                                      disabled={!onResolutionAction}
                                      onClick={() => onResolutionAction?.(resolution.action!)}
                                    >
                                      <ExternalLink data-icon="inline-start" />
                                      {resolution.title}
                                    </Button>
                                  );
                                }
                                if (!asset.id || !resolution.transaction) return null;
                                if (resolution.transaction.type === "dependency.manual.add") {
                                  return (
                                    <Button
                                      key={resolution.id}
                                      type="button"
                                      variant="outline"
                                      size="xs"
                                      disabled={Boolean(resolving)}
                                      onClick={() =>
                                        void resolveFinding(asset.id!, finding, resolution)
                                      }
                                    >
                                      {resolving === resolutionKey ? (
                                        <Loader2
                                          className="animate-spin"
                                          data-icon="inline-start"
                                        />
                                      ) : (
                                        <Link2 data-icon="inline-start" />
                                      )}
                                      {resolution.title}
                                    </Button>
                                  );
                                }
                                return (
                                  <AlertDialog key={resolution.id}>
                                    <AlertDialogTrigger asChild>
                                      <Button
                                        type="button"
                                        variant="outline"
                                        size="xs"
                                        disabled={Boolean(resolving)}
                                      >
                                        {resolving === resolutionKey ? (
                                          <Loader2 className="animate-spin" />
                                        ) : (
                                          <Trash2 />
                                        )}
                                        {resolution.title}
                                      </Button>
                                    </AlertDialogTrigger>
                                    <AlertDialogContent size="sm">
                                      <AlertDialogHeader>
                                        <AlertDialogTitle>
                                          Delete inactive metadata?
                                        </AlertDialogTitle>
                                        <AlertDialogDescription>
                                          This removes the saved settings described by this warning.
                                          You can add them again later if the materialization
                                          changes.
                                        </AlertDialogDescription>
                                      </AlertDialogHeader>
                                      <AlertDialogFooter>
                                        <AlertDialogCancel>Cancel</AlertDialogCancel>
                                        <AlertDialogAction
                                          variant="destructive"
                                          onClick={() =>
                                            void resolveFinding(asset.id!, finding, resolution)
                                          }
                                        >
                                          Delete
                                        </AlertDialogAction>
                                      </AlertDialogFooter>
                                    </AlertDialogContent>
                                  </AlertDialog>
                                );
                              })}
                            </div>
                          ) : null}
                        </div>
                        {finding.line ? (
                          <span className="shrink-0 font-mono text-[10px] text-muted-foreground">
                            L{finding.line}:C{finding.column}
                          </span>
                        ) : null}
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
              {flaggedPresentations.map((artifact) => (
                <div key={`${artifact.kind}:${artifact.id}`} className="rounded-md border">
                  <Link
                    to={
                      artifact.kind === "dashboard"
                        ? "/dashboards/$presentationId"
                        : "/reports/$presentationId"
                    }
                    params={{ presentationId: artifact.workspace_id }}
                    className="flex w-full items-center gap-2 border-b bg-muted/30 px-2.5 py-1.5 text-left text-xs hover:bg-muted"
                  >
                    {artifact.status === "error" ? (
                      <XCircle className="size-3.5 shrink-0 text-red-500" />
                    ) : (
                      <AlertTriangle className="size-3.5 shrink-0 text-amber-500" />
                    )}
                    <span className="min-w-0 flex-1 truncate font-medium">{artifact.title}</span>
                    <span className="shrink-0 text-[10px] text-muted-foreground">
                      {artifact.kind}
                    </span>
                  </Link>
                  <ul className="divide-y">
                    {artifact.findings.map((finding, index) => (
                      <li
                        key={`${finding.code}:${finding.path ?? ""}:${index}`}
                        className="flex items-start gap-2 px-2.5 py-1.5 text-xs"
                      >
                        {finding.severity === "error" ? (
                          <XCircle className="mt-0.5 size-3.5 shrink-0 text-red-500" />
                        ) : (
                          <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-amber-500" />
                        )}
                        <div className="min-w-0 flex-1">
                          <p>{finding.message}</p>
                          {finding.path ? (
                            <p className="mt-0.5 truncate font-mono text-[10px] text-muted-foreground">
                              {finding.path}
                            </p>
                          ) : null}
                        </div>
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
            </div>
          )}
        </div>
      </ScrollArea>
    </div>
  );
}

function TypeCheckLoading() {
  return (
    <div className="flex h-full min-h-0 items-center justify-center bg-background">
      <div className="flex items-center gap-2 text-xs opacity-80">
        <Spinner />
        <span>Type checking pipeline…</span>
      </div>
    </div>
  );
}
