import { atom } from "jotai";

import type { AssetStaleness } from "@/lib/api-staleness";
import type { WorkspaceConfigEnvironment } from "@/lib/types";

// "Getting started" is a short checklist for a project created on the welcome
// screen. Each item completes when the app observes it happening (a preview
// with rows, an Edited badge, a schedule), never by ticking it off. Progress is
// per project and lives only in this browser: nothing is written to the
// project's files.

export type GettingStartedItemId =
  | "inspect"
  | "edit"
  | "rebuild"
  | "notebook"
  | "dashboard"
  | "schedule"
  | "connect";

export type GettingStartedItem = {
  id: GettingStartedItemId;
  title: string;
  description: string;
  docsPath: string;
};

export const gettingStartedItems: GettingStartedItem[] = [
  {
    id: "inspect",
    title: "Inspect an asset's rows",
    description: "Select an asset on the canvas and read its output in Inspect.",
    docsPath: "/docs/workspace/pipeline-canvas/",
  },
  {
    id: "edit",
    title: "Edit a query",
    description: "Change an asset's SQL. It and everything downstream are marked as changed.",
    docsPath: "/docs/workspace/rebuild-what-changed/",
  },
  {
    id: "rebuild",
    title: "Rebuild only what changed",
    description:
      "Review run lists the out-of-date assets; run them to make the pipeline fresh again.",
    docsPath: "/docs/workspace/rebuild-what-changed/",
  },
  {
    id: "notebook",
    title: "Explore the data in a notebook",
    description:
      "Notebooks query the pipeline's tables in a local session, with charts and controls.",
    docsPath: "/docs/notebooks/overview/",
  },
  {
    id: "dashboard",
    title: "Open a dashboard",
    description: "Dashboards chart pipeline tables and live next to the pipeline as plain files.",
    docsPath: "/docs/presentations/overview/",
  },
  {
    id: "schedule",
    title: "Deploy and schedule the pipeline",
    description: "Pin the current files as a deployment and run them on a schedule.",
    docsPath: "/docs/scheduling/overview/",
  },
  {
    id: "connect",
    title: "Connect your own database",
    description: "Add a database or warehouse and import its tables as source assets.",
    docsPath: "/docs/connections-environments/managing-connections/",
  },
];

export const DOCS_ORIGIN = "https://getrenart.com";

export type GettingStartedRecord = {
  version: 1;
  startedAt: string;
  dismissed: boolean;
  done: Partial<Record<GettingStartedItemId, string>>;
};

const storageKey = (projectId: string) => `renart.getting-started.v1.${projectId}`;

export function readGettingStarted(projectId: string): GettingStartedRecord | null {
  try {
    const raw = window.localStorage.getItem(storageKey(projectId));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<GettingStartedRecord>;
    if (parsed.version !== 1 || typeof parsed.startedAt !== "string") return null;
    return {
      version: 1,
      startedAt: parsed.startedAt,
      dismissed: parsed.dismissed === true,
      done: parsed.done && typeof parsed.done === "object" ? parsed.done : {},
    };
  } catch {
    return null;
  }
}

export function writeGettingStarted(projectId: string, record: GettingStartedRecord) {
  try {
    window.localStorage.setItem(storageKey(projectId), JSON.stringify(record));
  } catch {
    // Without storage the checklist still works for this page load.
  }
}

// Called by the welcome screen when it creates a project.
export function startGettingStarted(projectId: string, now = new Date()) {
  if (!projectId || readGettingStarted(projectId)) return;
  writeGettingStarted(projectId, {
    version: 1,
    startedAt: now.toISOString(),
    dismissed: false,
    done: {},
  });
}

export function gettingStartedProgress(record: GettingStartedRecord) {
  const done = gettingStartedItems.filter((item) => record.done[item.id]).length;
  return { done, total: gettingStartedItems.length };
}

// The checklist of the project open in this tab, or null when it has none.
export const gettingStartedAtom = atom<{
  projectId: string;
  record: GettingStartedRecord;
} | null>(null);

export const markGettingStartedAtom = atom(
  null,
  (get, set, update: { id: GettingStartedItemId; at?: string }) => {
    const current = get(gettingStartedAtom);
    if (!current || current.record.done[update.id]) return;
    const record = {
      ...current.record,
      done: { ...current.record.done, [update.id]: update.at ?? new Date().toISOString() },
    };
    writeGettingStarted(current.projectId, record);
    set(gettingStartedAtom, { projectId: current.projectId, record });
  },
);

export const dismissGettingStartedAtom = atom(null, (get, set, dismissed: boolean) => {
  const current = get(gettingStartedAtom);
  if (!current) return;
  const record = { ...current.record, dismissed };
  writeGettingStarted(current.projectId, record);
  set(gettingStartedAtom, { projectId: current.projectId, record });
});

// ---- Observations ------------------------------------------------------------
// Pure rules over app state, so they are easy to test.

// An Edited badge appears when an asset's code changes after its last build.
export function observedEdit(assets: AssetStaleness[]) {
  return assets.some((asset) => asset.status === "stale_edited");
}

// After an edit, a successful run rebuilds what changed.
export function observedRebuild(assets: AssetStaleness[], editedAt: string | undefined) {
  if (!editedAt) return false;
  const edited = Date.parse(editedAt);
  return assets.some(
    (asset) =>
      asset.last_run_status === "succeeded" &&
      Boolean(asset.last_run_at) &&
      Date.parse(asset.last_run_at as string) > edited,
  );
}

// Any connection other than local DuckDB counts as the user's own data.
export function observedOwnConnection(environments: WorkspaceConfigEnvironment[]) {
  return environments.some((environment) =>
    environment.connections.some((connection) => connection.type !== "duckdb"),
  );
}

export function observedRouteItem(pathname: string): GettingStartedItemId | null {
  if (/^\/notebooks\/[^/]+/.test(pathname)) return "notebook";
  if (/^\/dashboards\/[^/]+/.test(pathname)) return "dashboard";
  return null;
}
