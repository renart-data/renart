import { useAtomValue } from "jotai";
import { AlertTriangle, GitCommitHorizontal, Package, RefreshCw, Rocket } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useEnvSchedules } from "@/hooks/use-env-schedules";
import { usePipelineDeploy } from "@/hooks/use-pipeline-deploy";
import {
  getDeployStatus,
  listSnapshots,
  type DeployStatus,
  type SnapshotSummary,
} from "@/lib/api-deploy";
import { selectedEnvironmentAtom, workspaceAtom } from "@/lib/atoms/domains/workspace";
import { deploymentLabel } from "@/lib/deployment-label";
import type { WebPipeline } from "@/lib/types";

import { AppPage, AppPanel, PageHeader } from "./app-primitives";
import { PipelinePlanSheet } from "./pipeline-plan-sheet";

type PipelineDeployments = {
  pipeline: WebPipeline;
  status: DeployStatus | null;
  snapshots: SnapshotSummary[];
  error: string | null;
};

export function AppDeploymentsPage() {
  const workspace = useAtomValue(workspaceAtom);
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const envSchedules = useEnvSchedules();
  const pipelines = workspace?.pipelines ?? [];
  const pipelineKey = pipelines.map((pipeline) => pipeline.id).join("\u0000");
  const [records, setRecords] = useState<PipelineDeployments[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshError, setRefreshError] = useState<string | null>(null);
  const [reviewing, setReviewing] = useState<WebPipeline | null>(null);
  const requestSerial = useRef(0);

  const refresh = useCallback(async () => {
    const serial = ++requestSerial.current;
    if (pipelines.length === 0) {
      setRecords([]);
      setRefreshError(null);
      setLoading(false);
      return;
    }
    setLoading(true);
    const next = await Promise.all(
      pipelines.map(async (pipeline): Promise<PipelineDeployments> => {
        try {
          const [status, snapshots] = await Promise.all([
            getDeployStatus(pipeline.id),
            listSnapshots(pipeline.id),
          ]);
          return { pipeline, status, snapshots: snapshots.snapshots ?? [], error: null };
        } catch (cause) {
          return {
            pipeline,
            status: null,
            snapshots: [],
            error:
              cause instanceof Error ? cause.message : "Deployment history could not be loaded.",
          };
        }
      }),
    );
    if (serial !== requestSerial.current) return;
    setRecords(next);
    const failures = next.filter((record) => record.error).length;
    setRefreshError(
      failures > 0
        ? `${failures} pipeline${failures === 1 ? "" : "s"} could not be refreshed.`
        : null,
    );
    setLoading(false);
  }, [pipelineKey, workspace?.revision]);

  useEffect(() => {
    void refresh();
    return () => {
      requestSerial.current += 1;
    };
  }, [refresh]);

  const pinCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const schedule of envSchedules.schedules) {
      if (!schedule.snapshot_version_id) continue;
      counts.set(schedule.snapshot_version_id, (counts.get(schedule.snapshot_version_id) ?? 0) + 1);
    }
    return counts;
  }, [envSchedules.schedules]);

  return (
    <AppPage>
      <PageHeader
        title="Deployments"
        subtitle="Immutable pipeline versions retained in .renart/state.db"
        actions={
          <Button variant="outline" size="sm" disabled={loading} onClick={() => void refresh()}>
            <RefreshCw data-icon="inline-start" className={loading ? "animate-spin" : undefined} />
            Refresh
          </Button>
        }
      />
      {refreshError ? (
        <div className="px-3 pb-2">
          <Alert variant="destructive">
            <AlertTriangle />
            <AlertTitle>Some deployments could not be refreshed</AlertTitle>
            <AlertDescription>
              {refreshError} Existing readable history remains visible.
            </AlertDescription>
          </Alert>
        </div>
      ) : null}
      <div className="min-h-0 flex-1 px-3 pb-3">
        <AppPanel className="flex h-full min-h-0 flex-col overflow-hidden">
          {loading && records.length === 0 ? (
            <div className="flex flex-col gap-3 p-3">
              {Array.from({ length: 4 }, (_, index) => (
                <Skeleton key={index} className="h-10 w-full" />
              ))}
            </div>
          ) : pipelines.length === 0 ? (
            <Empty>
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <Package />
                </EmptyMedia>
                <EmptyTitle>No pipelines to deploy</EmptyTitle>
                <EmptyDescription>Create a pipeline before reviewing deployments.</EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <ScrollArea className="min-h-0 flex-1" viewportClassName="h-full">
              <Table className="min-w-[980px]">
                <TableHeader className="sticky top-0 z-10 bg-card">
                  <TableRow>
                    <TableHead>Pipeline</TableHead>
                    <TableHead>Deployment</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Created</TableHead>
                    <TableHead>Git revision</TableHead>
                    <TableHead className="text-right">Files</TableHead>
                    <TableHead className="text-right">Actions</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {records.flatMap((record) => {
                    if (record.error) {
                      return [
                        <TableRow key={`${record.pipeline.id}:error`}>
                          <TableCell className="font-mono">{record.pipeline.name}</TableCell>
                          <TableCell
                            colSpan={5}
                            className="max-w-xl whitespace-normal text-destructive"
                          >
                            {record.error}
                          </TableCell>
                          <TableCell className="text-right">
                            <Button
                              size="xs"
                              variant="outline"
                              onClick={() => setReviewing(record.pipeline)}
                            >
                              <Rocket data-icon="inline-start" />
                              Review
                            </Button>
                          </TableCell>
                        </TableRow>,
                      ];
                    }
                    if (record.snapshots.length === 0) {
                      return [
                        <TableRow key={`${record.pipeline.id}:empty`}>
                          <TableCell className="font-mono">{record.pipeline.name}</TableCell>
                          <TableCell colSpan={5} className="text-muted-foreground">
                            No deployment has been created for this pipeline.
                          </TableCell>
                          <TableCell className="text-right">
                            <Button
                              size="xs"
                              variant="outline"
                              onClick={() => setReviewing(record.pipeline)}
                            >
                              <Rocket data-icon="inline-start" />
                              Review & deploy
                            </Button>
                          </TableCell>
                        </TableRow>,
                      ];
                    }
                    return record.snapshots.map((snapshot, index) => {
                      const latest = snapshot.version_id === record.status?.version_id;
                      const pins = pinCounts.get(snapshot.version_id) ?? 0;
                      return (
                        <TableRow key={snapshot.version_id}>
                          <TableCell className="font-mono">
                            {index === 0 ? record.pipeline.name : ""}
                          </TableCell>
                          <TableCell>
                            <Tooltip>
                              <TooltipTrigger asChild>
                                <span className="font-mono">
                                  {deploymentLabel(snapshot.ordinal, snapshot.version_id)}
                                </span>
                              </TooltipTrigger>
                              <TooltipContent className="font-mono">
                                {snapshot.version_id}
                              </TooltipContent>
                            </Tooltip>
                          </TableCell>
                          <TableCell>
                            <div className="flex flex-wrap items-center gap-1">
                              {latest ? (
                                <Badge>Latest</Badge>
                              ) : (
                                <Badge variant="muted">Retained</Badge>
                              )}
                              {latest && record.status && !record.status.executable ? (
                                <Badge variant="destructive">Needs repair</Badge>
                              ) : null}
                              {latest && record.status?.executable ? (
                                <Badge variant={record.status.in_sync ? "secondary" : "outline"}>
                                  {record.status.in_sync
                                    ? "Matches workspace"
                                    : "Workspace changed"}
                                </Badge>
                              ) : null}
                              {pins > 0 ? (
                                <Badge variant="outline">
                                  {pins} schedule{pins === 1 ? "" : "s"}
                                </Badge>
                              ) : null}
                            </div>
                          </TableCell>
                          <TableCell>{formatDeploymentDate(snapshot.created_at)}</TableCell>
                          <TableCell>
                            <div className="flex items-center gap-1.5">
                              <GitCommitHorizontal className="size-3.5 text-muted-foreground" />
                              <span className="font-mono">
                                {snapshot.git_sha ? snapshot.git_sha.slice(0, 8) : "unavailable"}
                                {snapshot.git_dirty ? " + changes" : ""}
                              </span>
                            </div>
                          </TableCell>
                          <TableCell className="text-right font-mono">
                            {snapshot.file_count}
                          </TableCell>
                          <TableCell className="text-right">
                            {index === 0 ? (
                              <Button
                                size="xs"
                                variant="ghost"
                                onClick={() => setReviewing(record.pipeline)}
                              >
                                <Rocket data-icon="inline-start" />
                                Review
                              </Button>
                            ) : null}
                          </TableCell>
                        </TableRow>
                      );
                    });
                  })}
                </TableBody>
              </Table>
            </ScrollArea>
          )}
        </AppPanel>
      </div>
      <DeploymentReview
        pipeline={reviewing}
        environment={selectedEnvironment || "default"}
        onOpenChange={(open) => {
          if (!open) setReviewing(null);
        }}
        onDeployed={refresh}
        onSchedulesChanged={envSchedules.refresh}
      />
    </AppPage>
  );
}

function DeploymentReview({
  pipeline,
  environment,
  onOpenChange,
  onDeployed,
  onSchedulesChanged,
}: {
  pipeline: WebPipeline | null;
  environment: string;
  onOpenChange: (open: boolean) => void;
  onDeployed: () => Promise<void>;
  onSchedulesChanged: () => void | Promise<void>;
}) {
  const deployState = usePipelineDeploy(pipeline?.id);
  return (
    <PipelinePlanSheet
      open={Boolean(pipeline)}
      onOpenChange={onOpenChange}
      pipelineId={pipeline?.id ?? ""}
      pipelineName={pipeline?.name ?? "Pipeline"}
      environment={environment}
      intent="deploy"
      onDeploy={async (expectedSourceMerkle) => {
        const response = await deployState.deploy(expectedSourceMerkle);
        await onDeployed();
        return response;
      }}
      onSchedulesChanged={onSchedulesChanged}
    />
  );
}

function formatDeploymentDate(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(
    date,
  );
}
