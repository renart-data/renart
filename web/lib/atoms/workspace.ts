import { atom } from "jotai";

import { WorkspaceState } from "@/lib/types";
import { ExecutionTimeWindow } from "@/lib/execution-time";
import { mergeWorkspaceWithPreservedContent } from "@/lib/workspace-reconciliation";

export type WorkspaceSyncMethod = "workspace-load" | "workspace-event";

export type WorkspaceSyncSource = {
  method: WorkspaceSyncMethod;
  recordedAt: string;
  revision?: number;
  eventType?: string;
  eventPath?: string;
  lite?: boolean;
  changedAssetIds?: string[];
  connectionSequence?: number;
};

export const workspaceAtom = atom<WorkspaceState | null>(null);
export const workspaceSyncSourceAtom = atom<WorkspaceSyncSource | null>(null);
// Includes the first connection: runtime results may complete after the initial
// HTTP snapshot but before the SSE subscription exists.
export const workspaceConnectionSequenceAtom = atom<number>(0);
// EventSource does not replay missed events. Increment this after every SSE
// reconnect so consumers backed by independent snapshots (notably freshness)
// can reconcile through their canonical HTTP endpoint.
export const workspaceReconnectSequenceAtom = atom<number>(0);

export type SQLCatalogReadyEvent = {
  type: "sql.catalog-ready";
  connection: string;
  environment?: string;
  database?: string;
  relation?: string;
};

export const sqlCatalogReadyEventAtom = atom<{
  sequence: number;
  event: SQLCatalogReadyEvent | null;
}>({ sequence: 0, event: null });

// Tracks whether the Go server is reachable. The SSE stream is the signal:
// `onopen` means connected, a sustained error means the server went away. We
// start optimistic so the offline overlay only appears once we actually lose
// the connection.
export const serverOnlineAtom = atom<boolean>(true);
export const selectedEnvironmentOverrideAtom = atom<string | undefined>(undefined);
export const selectedEnvironmentAtom = atom<string | undefined>(
  (get) =>
    get(selectedEnvironmentOverrideAtom) || get(workspaceAtom)?.selected_environment || undefined,
);
export const selectedExecutionTimeWindowAtom = atom<ExecutionTimeWindow | null>(null);

export type WorkspaceSyncUpdate = {
  workspace: WorkspaceState;
  source: WorkspaceSyncSource;
  connectionSequence: number;
};

export const receiveWorkspaceUpdateAtom = atom(null, (get, set, update: WorkspaceSyncUpdate) => {
  // HTTP and SSE share one admission boundary. A connection is a revision
  // epoch: server restart may reset its counter, but old in-flight requests
  // must never populate a new connection's projection.
  if (update.connectionSequence !== get(workspaceConnectionSequenceAtom)) return;
  const current = get(workspaceAtom);
  const previousSource = get(workspaceSyncSourceAtom);
  if (previousSource?.connectionSequence === update.connectionSequence) {
    const currentRevision = current?.revision ?? -1;
    const incomingRevision = update.workspace.revision ?? currentRevision + 1;
    if (incomingRevision < currentRevision) return;
    // A full HTTP response may hydrate omitted content from an equal-revision
    // lite event. Duplicate SSE events cannot improve that snapshot.
    if (incomingRevision === currentRevision && update.source.method === "workspace-event") return;
  }
  set(
    workspaceAtom,
    update.source.lite
      ? mergeWorkspaceWithPreservedContent(current, update.workspace, update.source.changedAssetIds)
      : update.workspace,
  );
  set(workspaceSyncSourceAtom, { ...update.source, connectionSequence: update.connectionSequence });
});
