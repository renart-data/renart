import { describe, expect, it } from "vitest";

import {
  buildSelectionLayoutReducer,
  createBuildSelectionLayoutState,
} from "./use-build-selection-layout";

describe("build selection and layout model", () => {
  it("starts from the routed asset and falls back to the first asset", () => {
    expect(createBuildSelectionLayoutState("routed", "first").visualSelectedAssetId).toBe("routed");
    expect(createBuildSelectionLayoutState(undefined, "first").visualSelectedAssetId).toBe("first");
  });

  it("reconciles route changes without resetting independent layout state", () => {
    const state = {
      ...createBuildSelectionLayoutState("old", "first"),
      explorerCollapsed: true,
      resultsCollapsed: true,
    };
    const next = buildSelectionLayoutReducer(state, {
      type: "route_selection_changed",
      routedAssetId: "new",
      firstAssetId: "first",
    });
    expect(next).toMatchObject({
      visualSelectedAssetId: "new",
      explorerCollapsed: true,
      resultsCollapsed: true,
    });
  });

  it("closes the mobile explorer when an asset is picked", () => {
    const state = {
      ...createBuildSelectionLayoutState("first"),
      explorerOpen: true,
    };
    expect(
      buildSelectionLayoutReducer(state, { type: "asset_picked", assetId: "second" }),
    ).toMatchObject({ visualSelectedAssetId: "second", explorerOpen: false });
  });

  it("keeps mobile sheets and desktop collapsed panels independent", () => {
    let state = createBuildSelectionLayoutState("first");
    state = buildSelectionLayoutReducer(state, {
      type: "explorer_open_changed",
      open: true,
    });
    state = buildSelectionLayoutReducer(state, { type: "explorer_collapsed_toggled" });
    state = buildSelectionLayoutReducer(state, {
      type: "inspector_open_changed",
      open: true,
    });
    state = buildSelectionLayoutReducer(state, { type: "inspector_collapsed_toggled" });
    state = buildSelectionLayoutReducer(state, {
      type: "results_collapsed_changed",
      collapsed: true,
    });

    expect(state).toMatchObject({
      explorerOpen: true,
      explorerCollapsed: true,
      inspectorOpen: true,
      inspectorCollapsed: true,
      resultsCollapsed: true,
    });
  });
});
