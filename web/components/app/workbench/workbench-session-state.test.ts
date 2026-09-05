import { describe, expect, it } from "vitest";

import {
  createWorkbenchSessionState,
  parseWorkbenchSession,
  reconcileWorkbenchSessionRoute,
  workbenchSessionReducer,
} from "./workbench-session-state";

describe("workbench session state", () => {
  it("does not switch or reopen an independent Build sidebar when opening an asset", () => {
    const selected = workbenchSessionReducer(createWorkbenchSessionState("p"), {
      type: "tool-selected",
      mode: "build",
      tool: "data",
    });
    const before = workbenchSessionReducer(selected, {
      type: "active-tool-toggled",
      mode: "build",
      tool: "data",
    });
    const after = workbenchSessionReducer(before, {
      type: "route-entered",
      mode: "build",
      tool: "resources",
    });
    expect(after.modes.build).toEqual(before.modes.build);
  });
  it("toggles the active tool without forgetting its selection", () => {
    const initial = workbenchSessionReducer(createWorkbenchSessionState("project-a"), {
      type: "route-entered",
      mode: "explore",
      tool: "catalog",
    });
    const collapsed = workbenchSessionReducer(initial, {
      type: "active-tool-toggled",
      mode: "explore",
      tool: "catalog",
    });

    expect(collapsed.modes.explore).toMatchObject({
      activeTool: "catalog",
      sidebarOpen: false,
    });
    expect(
      workbenchSessionReducer(collapsed, {
        type: "active-tool-toggled",
        mode: "explore",
        tool: "catalog",
      }).modes.explore.sidebarOpen,
    ).toBe(true);
  });

  it("partitions mode history", () => {
    const withRun = workbenchSessionReducer(createWorkbenchSessionState("project-a"), {
      type: "tool-selected",
      mode: "run",
      tool: "runs",
    });
    const withExplore = workbenchSessionReducer(withRun, {
      type: "tool-selected",
      mode: "explore",
      tool: "reports",
    });

    expect(withExplore.modes.run.activeTool).toBe("runs");
    expect(withExplore.modes.explore.activeTool).toBe("reports");
    expect(withExplore.modes.build.activeTool).toBe("resources");
  });

  it("reconciles the editor-dependent tool when restoring a route", () => {
    const persisted = workbenchSessionReducer(createWorkbenchSessionState("project-a"), {
      type: "tool-selected",
      mode: "build",
      tool: "ad-hoc",
    });
    const reconciled = reconcileWorkbenchSessionRoute(persisted, {
      mode: "build",
      tool: "resources",
    });

    expect(reconciled.modes.build).toMatchObject({
      activeTool: "resources",
      sidebarOpen: true,
    });
  });

  it("falls back for corrupt, oversized, or cross-project state", () => {
    expect(parseWorkbenchSession("not json", "project-a").projectId).toBe("project-a");
    expect(parseWorkbenchSession("x".repeat(33 * 1024), "project-a").modes.run.activeTool).toBe(
      "overview",
    );
    expect(
      parseWorkbenchSession(JSON.stringify(createWorkbenchSessionState("project-b")), "project-a")
        .projectId,
    ).toBe("project-a");
  });

  it("bounds restored sidebar state", () => {
    const serialized = JSON.stringify({
      ...createWorkbenchSessionState("project-a"),
      modes: {
        ...createWorkbenchSessionState("project-a").modes,
        explore: {
          activeTool: "catalog",
          sidebarOpen: false,
          sidebarWidth: 10000,
          expandedTreeNodes: Array.from({ length: 250 }, (_, index) => `asset-${index}`),
          sidebarScrollTop: -10,
        },
      },
    });

    const restored = parseWorkbenchSession(serialized, "project-a");
    expect(restored.modes.explore.sidebarWidth).toBe(420);
    expect(restored.modes.explore.expandedTreeNodes).toHaveLength(200);
    expect(restored.modes.explore.sidebarScrollTop).toBe(0);
  });
});
