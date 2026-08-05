import { atom } from "jotai";

import { WorkspaceState } from "@/lib/types";
import { ExecutionTimeWindow } from "@/lib/execution-time";

export type WorkspaceSyncMethod = "workspace-load" | "workspace-event";

export type WorkspaceSyncSource = {
  method: WorkspaceSyncMethod;
  recordedAt: string;
  revision?: number;
  eventType?: string;
  eventPath?: string;
  lite?: boolean;
  changedAssetIds?: string[];
};

export const workspaceAtom = atom<WorkspaceState | null>(null);
export const workspaceSyncSourceAtom = atom<WorkspaceSyncSource | null>(null);
// EventSource does not replay missed events. Increment this after every SSE
// reconnect so consumers backed by independent snapshots (notably freshness)
// can reconcile through their canonical HTTP endpoint.
export const workspaceReconnectSequenceAtom = atom<number>(0);

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
