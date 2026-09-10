import { createStore } from "jotai";
import { describe, expect, it } from "vitest";
import type { WorkspaceState } from "@/lib/types";
import {
  receiveWorkspaceUpdateAtom,
  selectedEnvironmentOverrideAtom,
  workspaceAtom,
  workspaceConnectionSequenceAtom,
  workspaceSyncSourceAtom,
  type WorkspaceSyncUpdate,
} from "./workspace";

function snapshot(revision: number, content = "select 1"): WorkspaceState {
  return {
    revision,
    connections: {},
    selected_environment: "default",
    errors: [],
    metadata: {},
    updated_at: "2026-09-07T00:00:00Z",
    pipelines: [
      {
        id: "pipeline",
        name: "analytics",
        path: "analytics",
        assets: [
          {
            id: "asset",
            name: "analytics.source",
            path: "analytics/source.sql",
            type: "duckdb.sql",
            content,
            upstreams: [],
            is_materialized: false,
          },
        ],
      },
    ],
  };
}

function update(
  revision: number,
  method: "workspace-load" | "workspace-event" = "workspace-event",
  connectionSequence = 1,
): WorkspaceSyncUpdate {
  return {
    workspace: snapshot(revision),
    connectionSequence,
    source: { method, revision, recordedAt: "2026-09-07T00:00:00Z" },
  };
}

function connectedStore() {
  const store = createStore();
  store.set(workspaceConnectionSequenceAtom, 1);
  return store;
}

describe("workspace snapshot admission", () => {
  it("does not let a delayed HTTP snapshot replace a newer SSE revision or provenance", () => {
    const store = connectedStore();
    const newest = update(12);
    store.set(receiveWorkspaceUpdateAtom, newest);
    const provenance = store.get(workspaceSyncSourceAtom);
    store.set(receiveWorkspaceUpdateAtom, update(11, "workspace-load"));
    expect(store.get(workspaceAtom)).toBe(newest.workspace);
    expect(store.get(workspaceSyncSourceAtom)).toBe(provenance);
  });

  it.each(["workspace-load", "workspace-event"] as const)(
    "ignores %s from a previous connection",
    (method) => {
      const store = connectedStore();
      store.set(receiveWorkspaceUpdateAtom, update(12));
      store.set(workspaceConnectionSequenceAtom, 2);
      const restarted = update(2, "workspace-load", 2);
      store.set(receiveWorkspaceUpdateAtom, restarted);
      store.set(receiveWorkspaceUpdateAtom, update(99, method, 1));
      expect(store.get(workspaceAtom)).toBe(restarted.workspace);
      expect(store.get(workspaceSyncSourceAtom)?.connectionSequence).toBe(2);
    },
  );

  it("accepts the first revision after server restart even when the counter reset", () => {
    const store = connectedStore();
    store.set(receiveWorkspaceUpdateAtom, update(100));
    store.set(workspaceConnectionSequenceAtom, 2);
    const restarted = update(1, "workspace-event", 2);
    store.set(receiveWorkspaceUpdateAtom, restarted);
    expect(store.get(workspaceAtom)).toBe(restarted.workspace);
    store.set(receiveWorkspaceUpdateAtom, update(0, "workspace-load", 2));
    expect(store.get(workspaceAtom)).toBe(restarted.workspace);
  });

  it("hydrates an equal-revision lite SSE snapshot from the full HTTP response", () => {
    const store = connectedStore();
    const lite = update(12);
    lite.source.lite = true;
    lite.workspace.pipelines[0].assets[0].content = "";
    store.set(receiveWorkspaceUpdateAtom, lite);
    const full = update(12, "workspace-load");
    store.set(receiveWorkspaceUpdateAtom, full);
    expect(store.get(workspaceAtom)).toBe(full.workspace);
  });

  it("keeps provenance when ignoring a duplicate or stale SSE event", () => {
    const store = connectedStore();
    store.set(receiveWorkspaceUpdateAtom, update(12, "workspace-load"));
    const provenance = store.get(workspaceSyncSourceAtom);
    store.set(receiveWorkspaceUpdateAtom, update(12));
    store.set(receiveWorkspaceUpdateAtom, update(11));
    expect(store.get(workspaceSyncSourceAtom)).toBe(provenance);
  });

  it("preserves cached lite content but clears removed metadata on changed assets", () => {
    const store = connectedStore();
    const full = update(12, "workspace-load");
    full.workspace.pipelines[0].assets[0].owner = "removed";
    full.workspace.pipelines[0].assets[0].tags = ["removed"];
    store.set(receiveWorkspaceUpdateAtom, full);
    store.set(selectedEnvironmentOverrideAtom, "production");
    const lite = update(13);
    lite.source.lite = true;
    lite.source.changedAssetIds = ["asset"];
    lite.workspace.pipelines[0].assets[0].content = "";
    store.set(receiveWorkspaceUpdateAtom, lite);
    expect(store.get(workspaceAtom)?.pipelines[0].assets[0]).toMatchObject({ content: "select 1" });
    expect(store.get(workspaceAtom)?.pipelines[0].assets[0].owner).toBeUndefined();
    expect(store.get(workspaceAtom)?.pipelines[0].assets[0].tags).toBeUndefined();
    expect(store.get(selectedEnvironmentOverrideAtom)).toBe("production");
    expect(full.workspace.pipelines[0].assets[0].owner).toBe("removed");
  });
});
