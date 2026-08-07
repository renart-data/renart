"use client";

import { projectApiPath } from "@/lib/project-context";
import { useAtomValue, useSetAtom } from "jotai";
import { useEffect } from "react";

import {
  serverOnlineAtom,
  sqlCatalogReadyEventAtom,
  workspaceAtom,
  workspaceReconnectSequenceAtom,
  workspaceSyncSourceAtom,
} from "@/lib/atoms/domains/workspace";
import { getWorkspace } from "@/lib/api-workspace";
import type { NotebookRuntimeEvent } from "@/lib/api-notebooks";
import type { StalenessUpdatedEvent } from "@/lib/api-staleness";
import {
  ScheduleOccurrenceEvent,
  SchedulerRunEvent,
  appendSchedulerRunEventAtom,
  mergeNotebookRuntimeEvent,
  notebookRuntimeEventsAtom,
  scheduleOccurrenceEventAtom,
  stalenessEventAtom,
} from "@/lib/atoms/domains/results";
import { WebAsset, WorkspaceEvent, WorkspaceState } from "@/lib/types";

function mergeWorkspaceWithPreservedContent(
  current: WorkspaceState | null,
  incoming: WorkspaceState,
  changedAssetIds: string[] = [],
): WorkspaceState {
  if (!current) {
    return incoming;
  }

  const changedAssetIdSet = new Set(changedAssetIds);
  const currentAssetById = new Map<string, WebAsset>();
  for (const pipeline of current.pipelines ?? []) {
    for (const asset of pipeline.assets ?? []) {
      if (asset.id) {
        currentAssetById.set(asset.id, asset);
      }
    }
  }

  const nextPipelines = (incoming.pipelines ?? []).map((pipeline) => ({
    ...pipeline,
    assets: (pipeline.assets ?? []).map((asset) => {
      const currentAsset = currentAssetById.get(asset.id);
      if (!currentAsset) {
        return asset;
      }

      const isChangedAsset = changedAssetIdSet.has(asset.id);

      return {
        ...currentAsset,
        ...asset,
        content: asset.content || currentAsset.content,
        meta: asset.meta ?? (isChangedAsset ? asset.meta : currentAsset.meta),
        columns: asset.columns ?? (isChangedAsset ? asset.columns : currentAsset.columns),
        // These clear to empty (e.g. removing the last tag, blanking the owner,
        // or fixing a syntax error so the asset parses again). The backend omits
        // empty values, so for a changed asset we must take the incoming (absent)
        // value rather than let the spread keep the stale one.
        tags: asset.tags ?? (isChangedAsset ? asset.tags : currentAsset.tags),
        owner: asset.owner ?? (isChangedAsset ? asset.owner : currentAsset.owner),
        incremental_key:
          asset.incremental_key ??
          (isChangedAsset ? asset.incremental_key : currentAsset.incremental_key),
        parse_error:
          asset.parse_error ?? (isChangedAsset ? asset.parse_error : currentAsset.parse_error),
      };
    }),
  }));

  return {
    ...incoming,
    pipelines: nextPipelines,
  };
}

function isSchedulerRunEvent(payload: unknown): payload is SchedulerRunEvent {
  return (
    typeof payload === "object" &&
    payload !== null &&
    "type" in payload &&
    typeof payload.type === "string" &&
    payload.type.startsWith("run.")
  );
}

function isScheduleOccurrenceEvent(payload: unknown): payload is ScheduleOccurrenceEvent {
  return (
    typeof payload === "object" &&
    payload !== null &&
    "type" in payload &&
    payload.type === "schedule.occurrence" &&
    "pipeline_uuid" in payload &&
    typeof payload.pipeline_uuid === "string" &&
    "environment" in payload &&
    typeof payload.environment === "string"
  );
}

function isWorkspaceEvent(payload: unknown): payload is WorkspaceEvent {
  return (
    typeof payload === "object" &&
    payload !== null &&
    "workspace" in payload &&
    Boolean(payload.workspace)
  );
}

function isStalenessEvent(payload: unknown): payload is StalenessUpdatedEvent {
  return (
    typeof payload === "object" &&
    payload !== null &&
    "type" in payload &&
    payload.type === "staleness.updated"
  );
}

function isNotebookRuntimeEvent(payload: unknown): payload is NotebookRuntimeEvent {
  return (
    typeof payload === "object" &&
    payload !== null &&
    "type" in payload &&
    payload.type === "notebook.runtime" &&
    "notebook_id" in payload &&
    typeof payload.notebook_id === "string" &&
    "stale" in payload &&
    Array.isArray(payload.stale) &&
    "auto_pending" in payload &&
    Array.isArray(payload.auto_pending) &&
    "running" in payload &&
    Array.isArray(payload.running)
  );
}

function isSQLCatalogReadyEvent(payload: unknown): payload is {
  type: "sql.catalog-ready";
  connection: string;
  environment?: string;
  database?: string;
  relation?: string;
} {
  return (
    typeof payload === "object" &&
    payload !== null &&
    "type" in payload &&
    payload.type === "sql.catalog-ready" &&
    "connection" in payload &&
    typeof payload.connection === "string"
  );
}

export function useWorkspaceSync() {
  const workspace = useAtomValue(workspaceAtom);
  const setWorkspace = useSetAtom(workspaceAtom);
  const appendSchedulerRunEvent = useSetAtom(appendSchedulerRunEventAtom);
  const setScheduleOccurrenceEvent = useSetAtom(scheduleOccurrenceEventAtom);
  const setStalenessEvent = useSetAtom(stalenessEventAtom);
  const setNotebookRuntimeEvents = useSetAtom(notebookRuntimeEventsAtom);
  const setSQLCatalogReadyEvent = useSetAtom(sqlCatalogReadyEventAtom);
  const setWorkspaceSyncSource = useSetAtom(workspaceSyncSourceAtom);
  const setWorkspaceReconnectSequence = useSetAtom(workspaceReconnectSequenceAtom);
  const setServerOnline = useSetAtom(serverOnlineAtom);

  useEffect(() => {
    let mounted = true;
    // The SSE stream drives the online/offline signal. A dropped connection
    // fires `onerror` repeatedly while EventSource retries; we wait out a short
    // grace period before declaring the server offline so a quick reconnect (or
    // a server restart) doesn't flash the overlay. On reconnect we reload the
    // workspace so state that changed while we were away is picked up.
    let offlineTimer: ReturnType<typeof setTimeout> | null = null;
    let offline = false;
    let opened = false;

    const clearOfflineTimer = () => {
      if (offlineTimer) {
        clearTimeout(offlineTimer);
        offlineTimer = null;
      }
    };

    const reloadWorkspace = () => {
      getWorkspace()
        .then((data) => {
          if (!mounted) return;
          setWorkspace(data);
          setWorkspaceSyncSource({
            method: "workspace-load",
            recordedAt: new Date().toISOString(),
            revision: data.revision,
          });
        })
        .catch(() => undefined);
    };

    const markOnline = () => {
      clearOfflineTimer();
      if (!mounted) return;
      setServerOnline(true);
      if (opened) {
        // There is no Last-Event-ID/replay contract on the workspace stream.
        // Even a reconnect shorter than the offline-overlay grace period may
        // have missed workspace or freshness events, so always reconcile.
        reloadWorkspace();
        setWorkspaceReconnectSequence((sequence) => sequence + 1);
      }
      opened = true;
      offline = false;
    };

    const markOfflineSoon = () => {
      if (offline || offlineTimer) return;
      offlineTimer = setTimeout(() => {
        offlineTimer = null;
        offline = true;
        if (mounted) setServerOnline(false);
      }, 4000);
    };

    reloadWorkspace();

    const source = new EventSource(projectApiPath("/api/events"));
    source.onopen = markOnline;
    source.onerror = markOfflineSoon;
    source.onmessage = (event) => {
      try {
        const payload = JSON.parse(event.data) as unknown;

        if (isSchedulerRunEvent(payload)) {
          appendSchedulerRunEvent(payload);
          return;
        }

        if (isScheduleOccurrenceEvent(payload)) {
          setScheduleOccurrenceEvent(payload);
          return;
        }

        if (isStalenessEvent(payload)) {
          setStalenessEvent(payload);
          return;
        }

        if (isNotebookRuntimeEvent(payload)) {
          setNotebookRuntimeEvents((current) => mergeNotebookRuntimeEvent(current, payload));
          return;
        }

        if (isSQLCatalogReadyEvent(payload)) {
          setSQLCatalogReadyEvent((current) => ({
            sequence: current.sequence + 1,
            event: payload,
          }));
          return;
        }

        if (!isWorkspaceEvent(payload)) {
          return;
        }

        setWorkspaceSyncSource({
          method: "workspace-event",
          recordedAt: new Date().toISOString(),
          revision: payload.workspace?.revision,
          eventType: payload.type,
          eventPath: payload.path,
          lite: payload.lite,
          changedAssetIds: payload.changed_asset_ids,
        });

        setWorkspace((current) => {
          const currentRevision = current?.revision ?? -1;
          const incomingRevision = payload.workspace?.revision ?? currentRevision + 1;

          if (incomingRevision <= currentRevision) {
            return current;
          }

          if (payload.lite) {
            return mergeWorkspaceWithPreservedContent(
              current,
              payload.workspace,
              payload.changed_asset_ids ?? [],
            );
          }

          return payload.workspace;
        });
      } catch {
        return;
      }
    };

    return () => {
      mounted = false;
      clearOfflineTimer();
      source.close();
    };
  }, [
    appendSchedulerRunEvent,
    setNotebookRuntimeEvents,
    setSQLCatalogReadyEvent,
    setServerOnline,
    setStalenessEvent,
    setWorkspace,
    setWorkspaceReconnectSequence,
    setWorkspaceSyncSource,
  ]);

  return workspace as WorkspaceState | null;
}
