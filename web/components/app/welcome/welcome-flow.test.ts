import { describe, expect, it } from "vitest";

import type { CreateProjectResponse, ProjectInfo } from "@/lib/generated/api-types";

import {
  initialWelcomeState,
  recentProjects,
  shortPath,
  stepsForPath,
  templateCanvasAssets,
  welcomeReducer,
  type WelcomeAction,
  type WelcomeState,
} from "./welcome-flow";

const created: CreateProjectResponse = {
  status: "ok",
  project: {
    id: "p1",
    name: "product-demo",
    path: "/tmp/product-demo",
    type: "",
    last_opened_at: "",
    open: true,
    exists: true,
    default: false,
  },
  pipeline_id: "cHJvZHVjdA",
  pipeline_path: "product_analytics",
  files: ["product_analytics/pipeline.yml"],
  git_initialized: true,
};

function run(actions: WelcomeAction[], from: WelcomeState = initialWelcomeState()) {
  return actions.reduce(welcomeReducer, from);
}

describe("welcome flow", () => {
  it("walks a demo from choosing to a finished first run", () => {
    let state = run([
      { type: "select-template", templateId: "demo:product" },
      { type: "choose-path", path: "demo", inPlace: false },
    ]);
    expect(state.step).toBe("setup");
    expect(state.projectName).toBe("product-demo");

    state = run(
      [
        { type: "create-started" },
        { type: "created", response: created },
        { type: "run-started", assetNames: ["product.users", "product.events"], now: 1000 },
        { type: "asset-event", assetName: "product.users", status: "running" },
        { type: "asset-event", assetName: "product.users", status: "succeeded" },
        { type: "asset-event", assetName: "product.events", status: "running" },
        { type: "run-finished", now: 3800 },
      ],
      state,
    );
    expect(state.step).toBe("run");
    expect(state.run.phase).toBe("succeeded");
    expect(state.run.assets).toEqual({
      "product.users": "succeeded",
      "product.events": "succeeded",
    });
    expect(state.run.finishedAt! - state.run.startedAt!).toBe(2800);
  });

  it("keeps a typed project name when the demo changes", () => {
    const state = run([
      { type: "select-template", templateId: "demo:product" },
      { type: "choose-path", path: "demo", inPlace: false },
      { type: "set-project-name", name: "my-analytics" },
      { type: "select-template", templateId: "demo:jinja" },
    ]);
    expect(state.projectName).toBe("my-analytics");
  });

  it("follows the selected demo until the name is edited", () => {
    const state = run([
      { type: "select-template", templateId: "demo:product" },
      { type: "choose-path", path: "demo", inPlace: false },
      { type: "select-template", templateId: "demo:jinja" },
    ]);
    expect(state.projectName).toBe("jinja-demo");
  });

  it("returns to setup when creating the project fails", () => {
    const state = run([
      { type: "choose-path", path: "demo", inPlace: false },
      { type: "create-started" },
      { type: "failed", at: "create", error: "directory already exists" },
    ]);
    expect(state.step).toBe("setup");
    expect(state.error).toBe("directory already exists");
    expect(state.run.phase).toBe("idle");
  });

  it("marks the running asset failed when the run fails", () => {
    const state = run([
      { type: "choose-path", path: "demo", inPlace: false },
      { type: "create-started" },
      { type: "created", response: created },
      { type: "run-started", assetNames: ["a", "b"], now: 0 },
      { type: "asset-event", assetName: "a", status: "running" },
      { type: "failed", at: "run", error: "boom", now: 10 },
    ]);
    expect(state.run.phase).toBe("failed");
    expect(state.run.failedAt).toBe("run");
    expect(state.run.assets).toEqual({ a: "failed", b: "waiting" });
    // A failed run can go back; a running one cannot.
    expect(welcomeReducer(state, { type: "back", inPlace: false }).step).toBe("choose");
  });

  it("connects straight away from an open, empty workspace", () => {
    expect(run([{ type: "choose-path", path: "import", inPlace: true }]).step).toBe("connect");
    const state = run([
      { type: "choose-path", path: "import", inPlace: false },
      { type: "create-started" },
      { type: "created", response: created },
    ]);
    expect(state.step).toBe("connect");
  });

  it("lists the steps of each path", () => {
    expect(stepsForPath("demo", false).map((step) => step.label)).toEqual([
      "Choose",
      "Set up",
      "Run",
    ]);
    expect(stepsForPath("import", true).map((step) => step.label)).toEqual([
      "Choose",
      "Connect",
      "Pick tables",
    ]);
    expect(stepsForPath("empty", false).map((step) => step.label)).toEqual(["Choose", "Set up"]);
  });
});

describe("welcome helpers", () => {
  it("draws template assets as never built, then fresh", () => {
    const assets = [
      { name: "product.events", type: "duckdb.sql", depends: [] },
      { name: "product.daily_active_users", type: "duckdb.sql", depends: ["product.events"] },
    ];
    const [events, daily] = templateCanvasAssets(assets, {
      "product.events": "succeeded",
      "product.daily_active_users": "running",
    });
    expect(events.staleness?.status).toBe("fresh");
    expect(events.displayName).toBe("events");
    expect(events.prefix).toBe("product");
    expect(daily.status).toBe("pending");
    expect(daily.staleness?.status).toBe("never_built");
    expect(daily.upstreams).toEqual(["product.events"]);
  });

  it("orders recent projects and drops missing or current ones", () => {
    const project = (id: string, lastOpened: string, exists = true): ProjectInfo => ({
      id,
      name: id,
      path: `/p/${id}`,
      type: "",
      last_opened_at: lastOpened,
      open: false,
      exists,
      default: false,
    });
    const recent = recentProjects(
      [
        project("old", "2026-09-01T00:00:00Z"),
        project("current", "2026-10-01T00:00:00Z"),
        project("gone", "2026-09-30T00:00:00Z", false),
        project("new", "2026-09-20T00:00:00Z"),
        { ...project("recreated", "2026-09-25T00:00:00Z"), path: "/p/current" },
      ],
      { id: "current", path: "/p/current" },
    );
    expect(recent.map((entry) => entry.id)).toEqual(["new", "old"]);
  });

  it("shortens paths to their tail", () => {
    expect(shortPath("/home/ada/git/product-demo")).toBe("…/git/product-demo");
    expect(shortPath("/srv")).toBe("/srv");
  });
});
