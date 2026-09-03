export type AppBuildView = "canvas" | "split" | "code";
export type AppResultTab = "inspect" | "render" | "materialize" | "query" | "typecheck";
export type AppEditorMode = "asset" | "adhoc";

export type AppBuildSearch = {
  result?: AppResultTab;
  editor?: AppEditorMode;
};

export const appResultTabs: readonly AppResultTab[] = [
  "inspect",
  "render",
  "materialize",
  "query",
  "typecheck",
];
const editorModes: readonly AppEditorMode[] = ["asset", "adhoc"];

export function normalizeAppBuildSearch(search: Record<string, unknown>): AppBuildSearch {
  return {
    result: appResultTabs.includes(search.result as AppResultTab)
      ? (search.result as AppResultTab)
      : undefined,
    editor: editorModes.includes(search.editor as AppEditorMode)
      ? (search.editor as AppEditorMode)
      : undefined,
  };
}

export function appAssetViewPath(view: AppBuildView) {
  if (view === "split") return "/pipelines/$pipelineId/assets/$assetId/split" as const;
  if (view === "code") return "/pipelines/$pipelineId/assets/$assetId/code" as const;
  return "/pipelines/$pipelineId/assets/$assetId/canvas" as const;
}

export function appBuildViewFromPath(pathname: string): AppBuildView {
  if (pathname.endsWith("/split")) return "split";
  if (pathname.endsWith("/code")) return "code";
  return "canvas";
}
