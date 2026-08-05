import { useAtomValue } from "jotai";
import { useEffect, useMemo, useRef, useState } from "react";

import { getPipelineStaleness, type AssetStaleness } from "@/lib/api-staleness";
import { stalenessEventAtom } from "@/lib/atoms/domains/results";
import {
  selectedEnvironmentAtom,
  selectedExecutionTimeWindowAtom,
  workspaceReconnectSequenceAtom,
} from "@/lib/atoms/domains/workspace";

export type PipelineStaleness = {
  byAssetName: Record<string, AssetStaleness>;
  staleAssets: AssetStaleness[];
  dataStateToken: string | null;
  loading: boolean;
  error: string | null;
};

export type PipelinesStaleness = {
  byPipelineId: Record<string, Record<string, AssetStaleness>>;
  dataStateTokenByPipelineId: Record<string, string>;
  loading: boolean;
  error: string | null;
};

type StalenessSnapshot = {
  selectionKey: string;
  assetsByPipelineId: Record<string, AssetStaleness[]>;
  dataStateTokenByPipelineId: Record<string, string>;
};

type StalenessRequestState = {
  selectionKey: string;
  pendingPipelineIds: string[];
  errorsByPipelineId: Record<string, string>;
};

type StalenessEventVersions = {
  selectionKey: string;
  byPipelineId: Record<string, number>;
};

const staleStatuses = new Set([
  "stale_edited",
  "stale_deployment",
  "stale_upstream",
  "partial",
  "volatile",
  "never_built",
  "missing",
]);

export function isStaleStatus(status: AssetStaleness["status"]) {
  return staleStatuses.has(status);
}

// sameInstant compares two timestamps that may differ in serialization
// (Go RFC3339 vs Date.toISOString milliseconds); both absent also matches.
function sameInstant(a?: string, b?: string) {
  if (!a && !b) return true;
  if (!a || !b) return false;
  return new Date(a).getTime() === new Date(b).getTime();
}

// usePipelineStaleness fetches the staleness map for the current selection
// (environment + time range) and keeps it live through staleness.updated
// SSE events pushed after saves and run completions.
export function usePipelinesStaleness(pipelineIds: string[]): PipelinesStaleness {
  const selectedEnvironment = useAtomValue(selectedEnvironmentAtom);
  const selectedTimeWindow = useAtomValue(selectedExecutionTimeWindowAtom);
  const workspaceReconnectSequence = useAtomValue(workspaceReconnectSequenceAtom);
  const stalenessEvent = useAtomValue(stalenessEventAtom);
  // Atom values retain the last push. Only consume a newly delivered value;
  // changing selection must not replay an old event ahead of its fresh HTTP
  // request.
  const processedStalenessEventRef = useRef(stalenessEvent);
  const pipelineKey = [...new Set(pipelineIds.filter(Boolean))].sort().join("\n");
  const stablePipelineIds = useMemo(
    () => (pipelineKey ? pipelineKey.split("\n") : []),
    [pipelineKey],
  );
  const selectionKey = useMemo(
    () =>
      JSON.stringify([
        pipelineKey,
        selectedEnvironment ?? "",
        selectedTimeWindow?.start ?? "",
        selectedTimeWindow?.end ?? "",
      ]),
    [pipelineKey, selectedEnvironment, selectedTimeWindow?.end, selectedTimeWindow?.start],
  );
  const [snapshot, setSnapshot] = useState<StalenessSnapshot | null>(null);
  const [requestState, setRequestState] = useState<StalenessRequestState>({
    selectionKey: "",
    pendingPipelineIds: [],
    errorsByPipelineId: {},
  });
  // A freshness push is newer than any HTTP request that was already in
  // flight. Remember its sequence so a late response cannot replace the
  // authoritative SSE snapshot or re-introduce a recovered request error.
  const eventVersionsRef = useRef<StalenessEventVersions>({
    selectionKey: "",
    byPipelineId: {},
  });

  useEffect(() => {
    if (stablePipelineIds.length === 0) {
      setSnapshot(null);
      setRequestState({ selectionKey, pendingPipelineIds: [], errorsByPipelineId: {} });
      return;
    }
    let cancelled = false;
    if (eventVersionsRef.current.selectionKey !== selectionKey) {
      eventVersionsRef.current = { selectionKey, byPipelineId: {} };
    }
    setRequestState({
      selectionKey,
      pendingPipelineIds: stablePipelineIds,
      errorsByPipelineId: {},
    });

    for (const pipelineId of stablePipelineIds) {
      const eventVersionAtStart = eventVersionsRef.current.byPipelineId[pipelineId] ?? 0;
      void getPipelineStaleness(pipelineId, {
        environment: selectedEnvironment,
        start: selectedTimeWindow?.start,
        end: selectedTimeWindow?.end,
      })
        .then((response) => {
          if (cancelled) return;
          const currentEventVersions = eventVersionsRef.current;
          const supersededByEvent =
            currentEventVersions.selectionKey === selectionKey &&
            (currentEventVersions.byPipelineId[pipelineId] ?? 0) > eventVersionAtStart;
          if (!supersededByEvent) {
            setSnapshot((current) => ({
              selectionKey,
              assetsByPipelineId: {
                ...(current?.selectionKey === selectionKey ? current.assetsByPipelineId : {}),
                [pipelineId]: response.assets ?? [],
              },
              dataStateTokenByPipelineId: {
                ...(current?.selectionKey === selectionKey
                  ? current.dataStateTokenByPipelineId
                  : {}),
                [pipelineId]: response.data_state_token,
              },
            }));
          }
          setRequestState((current) => {
            if (current.selectionKey !== selectionKey) return current;
            const errorsByPipelineId = { ...current.errorsByPipelineId };
            delete errorsByPipelineId[pipelineId];
            return {
              ...current,
              pendingPipelineIds: current.pendingPipelineIds.filter((id) => id !== pipelineId),
              errorsByPipelineId,
            };
          });
        })
        .catch((cause) => {
          if (cancelled) return;
          const currentEventVersions = eventVersionsRef.current;
          const supersededByEvent =
            currentEventVersions.selectionKey === selectionKey &&
            (currentEventVersions.byPipelineId[pipelineId] ?? 0) > eventVersionAtStart;
          setRequestState((current) => {
            if (current.selectionKey !== selectionKey) return current;
            const errorsByPipelineId = { ...current.errorsByPipelineId };
            if (supersededByEvent) {
              delete errorsByPipelineId[pipelineId];
            } else {
              errorsByPipelineId[pipelineId] =
                cause instanceof Error && cause.message
                  ? cause.message
                  : "Freshness could not be loaded.";
            }
            return {
              ...current,
              pendingPipelineIds: current.pendingPipelineIds.filter((id) => id !== pipelineId),
              errorsByPipelineId,
            };
          });
        });
    }
    return () => {
      cancelled = true;
    };
  }, [
    selectionKey,
    stablePipelineIds,
    selectedEnvironment,
    selectedTimeWindow?.start,
    selectedTimeWindow?.end,
    workspaceReconnectSequence,
  ]);

  useEffect(() => {
    if (!stalenessEvent || processedStalenessEventRef.current === stalenessEvent) return;
    processedStalenessEventRef.current = stalenessEvent;
    if (!stablePipelineIds.includes(stalenessEvent.pipeline_id)) return;
    // Discard pushes computed for a selection we have moved away from:
    // the fetch effect covers the new selection.
    if ((stalenessEvent.environment || "") !== (selectedEnvironment || "")) return;
    if (
      !sameInstant(stalenessEvent.start, selectedTimeWindow?.start) ||
      !sameInstant(stalenessEvent.end, selectedTimeWindow?.end)
    )
      return;
    if (eventVersionsRef.current.selectionKey !== selectionKey) {
      eventVersionsRef.current = { selectionKey, byPipelineId: {} };
    }
    eventVersionsRef.current.byPipelineId[stalenessEvent.pipeline_id] =
      (eventVersionsRef.current.byPipelineId[stalenessEvent.pipeline_id] ?? 0) + 1;
    setSnapshot((current) => ({
      selectionKey,
      assetsByPipelineId: {
        ...(current?.selectionKey === selectionKey ? current.assetsByPipelineId : {}),
        [stalenessEvent.pipeline_id]: stalenessEvent.assets ?? [],
      },
      dataStateTokenByPipelineId: {
        ...(current?.selectionKey === selectionKey ? current.dataStateTokenByPipelineId : {}),
        [stalenessEvent.pipeline_id]: stalenessEvent.data_state_token,
      },
    }));
    setRequestState((current) => {
      const requestIsCurrent = current.selectionKey === selectionKey;
      const errorsByPipelineId = requestIsCurrent ? { ...current.errorsByPipelineId } : {};
      delete errorsByPipelineId[stalenessEvent.pipeline_id];
      return {
        selectionKey,
        // An event authoritatively completes its pipeline, but it must not hide
        // unresolved requests or failures for the other pipelines in a catalog.
        pendingPipelineIds: (requestIsCurrent
          ? current.pendingPipelineIds
          : stablePipelineIds
        ).filter((id) => id !== stalenessEvent.pipeline_id),
        errorsByPipelineId,
      };
    });
  }, [
    selectionKey,
    stablePipelineIds,
    selectedEnvironment,
    selectedTimeWindow?.start,
    selectedTimeWindow?.end,
    stalenessEvent,
  ]);

  return useMemo(() => {
    const assetsByPipelineId =
      snapshot?.selectionKey === selectionKey ? snapshot.assetsByPipelineId : {};
    const dataStateTokenByPipelineId =
      snapshot?.selectionKey === selectionKey ? snapshot.dataStateTokenByPipelineId : {};
    const requestIsCurrent = requestState.selectionKey === selectionKey;
    const requestErrors = requestIsCurrent ? Object.values(requestState.errorsByPipelineId) : [];
    const byPipelineId: Record<string, Record<string, AssetStaleness>> = {};
    for (const pipelineId of stablePipelineIds) {
      const byAssetName: Record<string, AssetStaleness> = {};
      for (const asset of assetsByPipelineId[pipelineId] ?? []) {
        byAssetName[asset.asset_name] = asset;
      }
      byPipelineId[pipelineId] = byAssetName;
    }
    return {
      byPipelineId,
      dataStateTokenByPipelineId,
      loading:
        stablePipelineIds.length > 0 &&
        (!requestIsCurrent || requestState.pendingPipelineIds.length > 0),
      error:
        requestErrors.length === 0
          ? null
          : requestErrors.length === 1
            ? requestErrors[0]
            : `Freshness could not be loaded for ${requestErrors.length} pipelines.`,
    };
  }, [requestState, selectionKey, snapshot, stablePipelineIds]);
}

export function usePipelineStaleness(pipelineId: string | undefined): PipelineStaleness {
  const pipelines = usePipelinesStaleness(pipelineId ? [pipelineId] : []);
  return useMemo(() => {
    const byAssetName = pipelineId ? (pipelines.byPipelineId[pipelineId] ?? {}) : {};
    return {
      byAssetName,
      staleAssets: Object.values(byAssetName).filter((asset) => isStaleStatus(asset.status)),
      dataStateToken: pipelineId
        ? (pipelines.dataStateTokenByPipelineId[pipelineId] ?? null)
        : null,
      loading: pipelines.loading,
      error: pipelines.error,
    };
  }, [
    pipelineId,
    pipelines.byPipelineId,
    pipelines.dataStateTokenByPipelineId,
    pipelines.error,
    pipelines.loading,
  ]);
}
