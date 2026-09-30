import { useAtomValue } from "jotai";
import { useCallback, useEffect, useRef, useState } from "react";

import {
  deployPipeline,
  getDeployStatus,
  type DeployResponse,
  type DeployStatus,
} from "@/lib/api-deploy";
import type { EnvSchedulePinSelection } from "@/lib/api-env-schedules";
import { workspaceAtom } from "@/lib/atoms/domains/workspace";
import { awaitWorkspaceSaves } from "@/lib/workspace-save-barrier";

export type PipelineDeployState = {
  status: DeployStatus | null;
  loading: boolean;
  error: string | null;
  deploying: boolean;
  deploy: (
    expectedSourceMerkle?: string,
    schedules?: EnvSchedulePinSelection[],
  ) => Promise<DeployResponse>;
  refresh: () => Promise<void>;
  driftedFileCount: number;
};

// usePipelineDeploy tracks drift between the working tree and the latest
// deployed snapshot, refreshing whenever the workspace revision changes
// (i.e. after any save).
export function usePipelineDeploy(pipelineId: string | undefined): PipelineDeployState {
  const workspace = useAtomValue(workspaceAtom);
  const [resolved, setResolved] = useState<{
    pipelineId: string | undefined;
    status: DeployStatus | null;
    loading: boolean;
    error: string | null;
  }>({ pipelineId: undefined, status: null, loading: false, error: null });
  const [deployingPipelineIds, setDeployingPipelineIds] = useState<Set<string>>(() => new Set());
  const deployingPipelineIdsRef = useRef<Set<string>>(new Set());
  const requestId = useRef(0);
  const currentPipelineId = useRef(pipelineId);
  currentPipelineId.current = pipelineId;

  const current =
    resolved.pipelineId === pipelineId
      ? resolved
      : { pipelineId, status: null, loading: Boolean(pipelineId), error: null };

  const refreshPipeline = useCallback(async (targetPipelineId: string | undefined) => {
    // A callback captured by the previous route may still finish after the
    // user has opened another pipeline. It must not invalidate or replace the
    // request owned by the current route.
    if (currentPipelineId.current !== targetPipelineId) {
      return;
    }
    const nextRequestId = ++requestId.current;
    if (!targetPipelineId) {
      setResolved({ pipelineId: targetPipelineId, status: null, loading: false, error: null });
      return;
    }
    setResolved((previous) => ({
      pipelineId: targetPipelineId,
      status: previous.pipelineId === targetPipelineId ? previous.status : null,
      loading: true,
      error: null,
    }));
    try {
      const status = await getDeployStatus(targetPipelineId);
      if (currentPipelineId.current === targetPipelineId && requestId.current === nextRequestId) {
        setResolved({ pipelineId: targetPipelineId, status, loading: false, error: null });
      }
    } catch (cause) {
      if (currentPipelineId.current === targetPipelineId && requestId.current === nextRequestId) {
        setResolved({
          pipelineId: targetPipelineId,
          status: null,
          loading: false,
          error: cause instanceof Error ? cause.message : "Failed to load deployment status.",
        });
      }
    }
  }, []);

  const refresh = useCallback(
    async () => refreshPipeline(pipelineId),
    [pipelineId, refreshPipeline],
  );

  useEffect(() => {
    void refresh();
  }, [refresh, workspace?.revision]);

  const deploy = useCallback(
    async (expectedSourceMerkle?: string, schedules?: EnvSchedulePinSelection[]) => {
      if (!pipelineId) {
        throw new Error("Pipeline is required to deploy.");
      }
      if (deployingPipelineIdsRef.current.has(pipelineId)) {
        throw new Error("A deployment is already in progress.");
      }
      const targetPipelineId = pipelineId;
      deployingPipelineIdsRef.current.add(targetPipelineId);
      setDeployingPipelineIds(new Set(deployingPipelineIdsRef.current));
      try {
        await awaitWorkspaceSaves();
        const deployed = await deployPipeline(targetPipelineId, expectedSourceMerkle, schedules);
        await refreshPipeline(targetPipelineId);
        return deployed;
      } finally {
        deployingPipelineIdsRef.current.delete(targetPipelineId);
        setDeployingPipelineIds(new Set(deployingPipelineIdsRef.current));
      }
    },
    [pipelineId, refreshPipeline],
  );

  const driftedFileCount =
    (current.status?.changed_files?.length ?? 0) +
    (current.status?.added_files?.length ?? 0) +
    (current.status?.removed_files?.length ?? 0);

  return {
    status: current.status,
    loading: current.loading,
    error: current.error,
    deploying: Boolean(pipelineId && deployingPipelineIds.has(pipelineId)),
    deploy,
    refresh,
    driftedFileCount,
  };
}
