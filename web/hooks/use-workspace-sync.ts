"use client";

import { projectApiPath } from "@/lib/project-context";
import { useAtomValue, useSetAtom, useStore } from "jotai";
import { useEffect } from "react";

import {
  serverOnlineAtom,
  sqlCatalogReadyEventAtom,
  workspaceAtom,
  workspaceConnectionSequenceAtom,
  workspaceReconnectSequenceAtom,
  receiveWorkspaceUpdateAtom,
} from "@/lib/atoms/domains/workspace";
import { getWorkspace } from "@/lib/api-workspace";
import type { NotebookAgentSnapshot, NotebookRuntimeEvent } from "@/lib/api-notebooks";
import type { StalenessUpdatedEvent } from "@/lib/api-staleness";
import {
  ScheduleOccurrenceEvent,
  SchedulerRunEvent,
  appendSchedulerRunEventAtom,
  mergeNotebookRuntimeEvent,
  mergeNotebookAgentEvent,
  notebookAgentEventsAtom,
  notebookRuntimeEventsAtom,
  scheduleOccurrenceEventAtom,
  stalenessEventAtom,
} from "@/lib/atoms/domains/results";
import { WorkspaceEvent, WorkspaceState } from "@/lib/types";

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

function isNotebookAgentEvent(payload: unknown): payload is NotebookAgentSnapshot {
  return (
    typeof payload === "object" &&
    payload !== null &&
    "type" in payload &&
    payload.type === "notebook.agent" &&
    "notebook_id" in payload &&
    typeof payload.notebook_id === "string" &&
    "revision" in payload &&
    typeof payload.revision === "number" &&
    "messages" in payload &&
    Array.isArray(payload.messages) &&
    "activities" in payload &&
    Array.isArray(payload.activities)
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
  const store = useStore();
  const receiveWorkspaceUpdate = useSetAtom(receiveWorkspaceUpdateAtom);
  const appendSchedulerRunEvent = useSetAtom(appendSchedulerRunEventAtom);
  const setScheduleOccurrenceEvent = useSetAtom(scheduleOccurrenceEventAtom);
  const setStalenessEvent = useSetAtom(stalenessEventAtom);
  const setNotebookRuntimeEvents = useSetAtom(notebookRuntimeEventsAtom);
  const setNotebookAgentEvents = useSetAtom(notebookAgentEventsAtom);
  const setSQLCatalogReadyEvent = useSetAtom(sqlCatalogReadyEventAtom);
  const setWorkspaceConnectionSequence = useSetAtom(workspaceConnectionSequenceAtom);
  const setWorkspaceReconnectSequence = useSetAtom(workspaceReconnectSequenceAtom);
  const setServerOnline = useSetAtom(serverOnlineAtom);

  useEffect(() => {
    let mounted = true;
    let connectionSequence = store.get(workspaceConnectionSequenceAtom);
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
      const requestConnection = connectionSequence;
      getWorkspace()
        .then((data) => {
          if (!mounted) return;
          receiveWorkspaceUpdate({
            workspace: data,
            connectionSequence: requestConnection,
            source: {
              method: "workspace-load",
              recordedAt: new Date().toISOString(),
              revision: data.revision,
            },
          });
        })
        .catch(() => undefined);
    };

    const markOnline = () => {
      clearOfflineTimer();
      if (!mounted) return;
      setServerOnline(true);
      setWorkspaceConnectionSequence((sequence) => sequence + 1);
      connectionSequence = store.get(workspaceConnectionSequenceAtom);
      // Reconcile after subscription, including the first connection.
      reloadWorkspace();
      if (opened) {
        // There is no Last-Event-ID/replay contract on the workspace stream.
        // Even a reconnect shorter than the offline-overlay grace period may
        // have missed workspace or freshness events, so always reconcile.
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

        if (isNotebookAgentEvent(payload)) {
          setNotebookAgentEvents((current) => mergeNotebookAgentEvent(current, payload));
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

        receiveWorkspaceUpdate({
          workspace: payload.workspace,
          connectionSequence,
          source: {
            method: "workspace-event",
            recordedAt: new Date().toISOString(),
            revision: payload.workspace.revision,
            eventType: payload.type,
            eventPath: payload.path,
            lite: payload.lite,
            changedAssetIds: payload.changed_asset_ids,
          },
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
    setNotebookAgentEvents,
    setNotebookRuntimeEvents,
    setSQLCatalogReadyEvent,
    setServerOnline,
    setStalenessEvent,
    receiveWorkspaceUpdate,
    store,
    setWorkspaceConnectionSequence,
    setWorkspaceReconnectSequence,
  ]);

  return workspace as WorkspaceState | null;
}
