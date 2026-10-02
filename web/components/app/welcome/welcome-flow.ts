import type { AppLineageCanvasAsset } from "@/components/app/lineage-canvas";
import { assetKindForType, assetNameParts } from "@/lib/asset-presentation";
import type { AssetStaleness } from "@/lib/api-staleness";
import type { CreateProjectResponse, ProjectInfo, TemplateAsset } from "@/lib/generated/api-types";

// The welcome flow is a small step machine. Each path walks its own steps;
// the step indicator, the preview pane and the primary action all derive
// from this state, so the page component only wires effects to actions.

export type WelcomePath = "demo" | "import" | "empty";

export type WelcomeStep = "choose" | "setup" | "run" | "connect" | "tables";

export type AssetRunStatus = "waiting" | "running" | "succeeded" | "failed" | "skipped";

export type RunPhase =
  | "idle"
  | "creating"
  | "preparing"
  | "downloading-driver"
  | "running"
  | "succeeded"
  | "failed";

export type WelcomeState = {
  step: WelcomeStep;
  path: WelcomePath;
  templateId: string;
  projectName: string;
  // The user typed a name, so switching demos keeps it.
  projectNameEdited: boolean;
  error: string | null;
  created: CreateProjectResponse | null;
  run: {
    phase: RunPhase;
    // Which step failed: creating the project, preparing DuckDB or the run.
    failedAt: "create" | "prepare" | "run" | null;
    assets: Record<string, AssetRunStatus>;
    log: string;
    startedAt: number | null;
    finishedAt: number | null;
  };
};

export type WelcomeAction =
  | { type: "choose-path"; path: WelcomePath; inPlace: boolean }
  | { type: "back"; inPlace: boolean }
  | { type: "select-template"; templateId: string }
  | { type: "set-project-name"; name: string }
  | { type: "set-error"; error: string | null }
  | { type: "create-started" }
  | { type: "created"; response: CreateProjectResponse }
  | { type: "prepare-started"; downloading: boolean }
  | { type: "run-started"; assetNames: string[]; now: number }
  | { type: "asset-event"; assetName: string; status: string }
  | { type: "log"; chunk: string }
  | { type: "run-finished"; now: number }
  | { type: "failed"; at: "create" | "prepare" | "run"; error: string; now?: number }
  | { type: "retry-run" };

const LOG_LIMIT = 20000;

const idleRun: WelcomeState["run"] = {
  phase: "idle",
  failedAt: null,
  assets: {},
  log: "",
  startedAt: null,
  finishedAt: null,
};

export function initialWelcomeState(): WelcomeState {
  return {
    step: "choose",
    path: "demo",
    templateId: "",
    projectName: "",
    projectNameEdited: false,
    error: null,
    created: null,
    run: idleRun,
  };
}

export function suggestedProjectName(path: WelcomePath, templateId: string) {
  if (path !== "demo") return "analytics";
  const id = templateId.replace(/^demo:/, "");
  return id ? `${id}-demo` : "demo";
}

export function templateIdForPath(path: WelcomePath, demoTemplateId: string) {
  if (path === "demo") return demoTemplateId;
  return path === "import" ? "bare" : "empty";
}

export function welcomeReducer(state: WelcomeState, action: WelcomeAction): WelcomeState {
  switch (action.type) {
    case "choose-path": {
      // Connecting from an open, empty workspace needs no project setup.
      const step = action.path === "import" && action.inPlace ? "connect" : "setup";
      return {
        ...state,
        path: action.path,
        step,
        error: null,
        projectName: state.projectNameEdited
          ? state.projectName
          : suggestedProjectName(action.path, state.templateId),
      };
    }
    case "back": {
      if (state.step === "tables") return { ...state, step: "connect", error: null };
      if (state.step === "connect" && !state.created && !action.inPlace) {
        return { ...state, step: "setup", error: null };
      }
      if (state.step === "run" && state.run.phase !== "failed") return state;
      return { ...state, step: "choose", error: null };
    }
    case "select-template":
      return {
        ...state,
        templateId: action.templateId,
        error: null,
        projectName: state.projectNameEdited
          ? state.projectName
          : suggestedProjectName(state.path, action.templateId),
      };
    case "set-project-name":
      return { ...state, projectName: action.name, projectNameEdited: true, error: null };
    case "set-error":
      return { ...state, error: action.error };
    case "create-started":
      return {
        ...state,
        error: null,
        step: state.path === "demo" ? "run" : state.step,
        run: { ...idleRun, phase: "creating" },
      };
    case "created":
      return {
        ...state,
        created: action.response,
        step: state.path === "import" ? "connect" : state.step,
        run: { ...state.run, phase: state.path === "demo" ? "preparing" : "succeeded" },
      };
    case "prepare-started":
      return {
        ...state,
        run: { ...state.run, phase: action.downloading ? "downloading-driver" : "preparing" },
      };
    case "run-started":
      return {
        ...state,
        error: null,
        run: {
          ...state.run,
          phase: "running",
          failedAt: null,
          log: "",
          startedAt: action.now,
          finishedAt: null,
          assets: Object.fromEntries(action.assetNames.map((name) => [name, "waiting" as const])),
        },
      };
    case "asset-event": {
      const status = assetRunStatus(action.status);
      if (!status) return state;
      return {
        ...state,
        run: { ...state.run, assets: { ...state.run.assets, [action.assetName]: status } },
      };
    }
    case "log":
      return {
        ...state,
        run: { ...state.run, log: (state.run.log + action.chunk).slice(-LOG_LIMIT) },
      };
    case "run-finished":
      return {
        ...state,
        run: {
          ...state.run,
          phase: "succeeded",
          finishedAt: action.now,
          // A clean stream finished every asset it planned.
          assets: Object.fromEntries(
            Object.entries(state.run.assets).map(([name, status]) => [
              name,
              status === "waiting" || status === "running" ? "succeeded" : status,
            ]),
          ),
        },
      };
    case "failed": {
      // A failed project creation leaves nothing to run; return to setup.
      const step = action.at === "create" && state.path === "demo" ? "setup" : state.step;
      return {
        ...state,
        step,
        error: action.error,
        run: {
          ...state.run,
          phase: action.at === "create" ? "idle" : "failed",
          failedAt: action.at === "create" ? null : action.at,
          finishedAt: action.now ?? state.run.finishedAt,
          assets: Object.fromEntries(
            Object.entries(state.run.assets).map(([name, status]) => [
              name,
              status === "running" ? "failed" : status,
            ]),
          ),
        },
      };
    }
    case "retry-run":
      return {
        ...state,
        error: null,
        run: { ...idleRun, phase: "preparing" },
      };
  }
}

function assetRunStatus(status: string): AssetRunStatus | null {
  switch (status) {
    case "running":
    case "succeeded":
    case "failed":
    case "skipped":
      return status;
    default:
      return null;
  }
}

export type WelcomeStepLabel = { id: WelcomeStep; label: string };

// The steps shown in the indicator for a path. "Choose" always comes first;
// connecting from an open, empty workspace skips project setup.
export function stepsForPath(path: WelcomePath, inPlace: boolean): WelcomeStepLabel[] {
  const choose = { id: "choose" as const, label: "Choose" };
  const setup = { id: "setup" as const, label: "Set up" };
  switch (path) {
    case "demo":
      return [choose, setup, { id: "run", label: "Run" }];
    case "empty":
      return [choose, setup];
    case "import":
      return [
        choose,
        ...(inPlace ? [] : [setup]),
        { id: "connect", label: "Connect" },
        { id: "tables", label: "Pick tables" },
      ];
  }
}

const neverBuilt = (name: string): AssetStaleness => ({
  asset_id: name,
  asset_name: name,
  status: "never_built",
  fingerprint: "",
  interval_aware: false,
  backfill_safe: false,
  target_fidelity: "exact",
});

// Template assets as read-only canvas nodes. The statuses mirror what the
// workspace canvas shows: Never built before the first run, Fresh after it.
export function templateCanvasAssets(
  assets: TemplateAsset[],
  statuses: Record<string, AssetRunStatus> = {},
): AppLineageCanvasAsset[] {
  return assets.map((asset) => {
    const { prefix, title } = assetNameParts(asset.name);
    const status = statuses[asset.name];
    return {
      id: asset.name,
      name: asset.name,
      displayName: title,
      prefix,
      kind: assetKindForType(asset.type),
      group: prefix ?? "ASSETS",
      integration: "duckdb-default",
      description: "",
      status: status === "running" ? "pending" : status === "failed" ? "failed" : "ok",
      materializedAt: "",
      staleness:
        status === "succeeded"
          ? { ...neverBuilt(asset.name), status: "fresh", last_run_status: "succeeded" }
          : neverBuilt(asset.name),
      upstreams: asset.depends,
      readOnly: true,
      x: 0,
      y: 0,
    };
  });
}

// Registered projects worth reopening: present on disk, not the workspace
// this screen is setting up (by ID or path, since a recreated folder gets a
// new ID), most recently opened first.
export function recentProjects(
  projects: ProjectInfo[],
  current: { id: string | null; path: string | null },
) {
  return projects
    .filter(
      (project) => project.exists && project.id !== current.id && project.path !== current.path,
    )
    .sort((left, right) => Date.parse(right.last_opened_at) - Date.parse(left.last_opened_at));
}

// The tail of a path for compact display: "…/git/renart-demo".
export function shortPath(path: string, segments = 2) {
  const parts = path.split(/[\\/]/).filter(Boolean);
  if (parts.length <= segments) return path;
  return `…/${parts.slice(-segments).join("/")}`;
}

export function relativeTime(iso: string, now = Date.now()) {
  const then = Date.parse(iso);
  if (!Number.isFinite(then) || then <= 0) return "";
  const minutes = Math.round((now - then) / 60000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  const days = Math.round(hours / 24);
  if (days < 30) return `${days} ${days === 1 ? "day" : "days"} ago`;
  return new Date(then).toLocaleDateString();
}

export function formatDuration(milliseconds: number) {
  if (milliseconds < 1000) return `${milliseconds} ms`;
  return `${(milliseconds / 1000).toFixed(1)} s`;
}
