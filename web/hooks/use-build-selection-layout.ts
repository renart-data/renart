import { useEffect, useReducer } from "react";

export type BuildSelectionLayoutState = {
  visualSelectedAssetId: string;
  explorerOpen: boolean;
  inspectorOpen: boolean;
  explorerCollapsed: boolean;
  inspectorCollapsed: boolean;
  resultsCollapsed: boolean;
};

export type BuildSelectionLayoutEvent =
  | { type: "route_selection_changed"; routedAssetId?: string; firstAssetId: string }
  | { type: "visual_selection_changed"; assetId: string }
  | { type: "asset_picked"; assetId: string }
  | { type: "explorer_open_changed"; open: boolean }
  | { type: "inspector_open_changed"; open: boolean }
  | { type: "explorer_collapsed_changed"; collapsed: boolean }
  | { type: "inspector_collapsed_changed"; collapsed: boolean }
  | { type: "explorer_collapsed_toggled" }
  | { type: "inspector_collapsed_toggled" }
  | { type: "results_collapsed_changed"; collapsed: boolean };

export function createBuildSelectionLayoutState(
  routedAssetId?: string,
  firstAssetId = "",
): BuildSelectionLayoutState {
  return {
    visualSelectedAssetId: routedAssetId ?? firstAssetId,
    explorerOpen: false,
    inspectorOpen: false,
    explorerCollapsed: false,
    inspectorCollapsed: false,
    resultsCollapsed: false,
  };
}

export function buildSelectionLayoutReducer(
  state: BuildSelectionLayoutState,
  event: BuildSelectionLayoutEvent,
): BuildSelectionLayoutState {
  switch (event.type) {
    case "route_selection_changed":
      return {
        ...state,
        visualSelectedAssetId: event.routedAssetId ?? event.firstAssetId,
      };
    case "visual_selection_changed":
      return { ...state, visualSelectedAssetId: event.assetId };
    case "asset_picked":
      return { ...state, visualSelectedAssetId: event.assetId, explorerOpen: false };
    case "explorer_open_changed":
      return { ...state, explorerOpen: event.open };
    case "inspector_open_changed":
      return { ...state, inspectorOpen: event.open };
    case "explorer_collapsed_changed":
      return { ...state, explorerCollapsed: event.collapsed };
    case "inspector_collapsed_changed":
      return { ...state, inspectorCollapsed: event.collapsed };
    case "explorer_collapsed_toggled":
      return { ...state, explorerCollapsed: !state.explorerCollapsed };
    case "inspector_collapsed_toggled":
      return { ...state, inspectorCollapsed: !state.inspectorCollapsed };
    case "results_collapsed_changed":
      return { ...state, resultsCollapsed: event.collapsed };
  }
}

export function useBuildSelectionLayout({
  routedAssetId,
  firstAssetId,
}: {
  routedAssetId?: string;
  firstAssetId: string;
}) {
  const [state, dispatch] = useReducer(
    buildSelectionLayoutReducer,
    createBuildSelectionLayoutState(routedAssetId, firstAssetId),
  );

  useEffect(() => {
    dispatch({ type: "route_selection_changed", routedAssetId, firstAssetId });
  }, [firstAssetId, routedAssetId]);

  return {
    ...state,
    effectiveSelectedAssetId: state.visualSelectedAssetId ?? routedAssetId ?? firstAssetId,
    pickAsset: (assetId: string) => dispatch({ type: "asset_picked", assetId }),
    setVisualSelectedAssetId: (assetId: string) =>
      dispatch({ type: "visual_selection_changed", assetId }),
    setExplorerOpen: (open: boolean) => dispatch({ type: "explorer_open_changed", open }),
    setInspectorOpen: (open: boolean) => dispatch({ type: "inspector_open_changed", open }),
    setExplorerCollapsed: (collapsed: boolean) =>
      dispatch({ type: "explorer_collapsed_changed", collapsed }),
    setInspectorCollapsed: (collapsed: boolean) =>
      dispatch({ type: "inspector_collapsed_changed", collapsed }),
    toggleExplorerCollapsed: () => dispatch({ type: "explorer_collapsed_toggled" }),
    toggleInspectorCollapsed: () => dispatch({ type: "inspector_collapsed_toggled" }),
    setResultsCollapsed: (collapsed: boolean) =>
      dispatch({ type: "results_collapsed_changed", collapsed }),
  };
}
