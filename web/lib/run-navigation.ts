export type RunLocation = {
  run_tab?: "events" | "plan" | "output";
  run_asset?: string;
  run_focus?: "events" | "timeline";
};

export function normalizeRunLocation(search: Record<string, unknown>): RunLocation {
  const location: RunLocation = {
    run_tab: ["events", "plan", "output"].includes(String(search.run_tab))
      ? (search.run_tab as RunLocation["run_tab"])
      : undefined,
    run_asset:
      typeof search.run_asset === "string" &&
      search.run_asset.trim() &&
      search.run_asset.length <= 4096
        ? search.run_asset
        : undefined,
    run_focus:
      search.run_focus === "events" || search.run_focus === "timeline"
        ? search.run_focus
        : undefined,
  };
  if (location.run_asset && location.run_focus === "events") location.run_tab = "events";
  return location;
}

// Runs already have their own canonical locator. Adapt it to the shared arrival
// lifecycle without a second URL contract or coupling independent output tabs.
export function runNavigationTargetKey(
  project: string | undefined,
  pathname: string,
  search: Record<string, unknown>,
) {
  if (!/^\/runs\/[^/]+$/.test(pathname)) return "";
  const { run_asset, run_focus } = normalizeRunLocation(search);
  return run_asset && run_focus
    ? JSON.stringify({ project, run: pathname, asset: run_asset, focus: run_focus })
    : "";
}

export function runAssetLocation(
  current: RunLocation,
  asset: string,
  target: "events" | "timeline",
): RunLocation {
  return {
    ...current,
    run_asset: asset,
    run_focus: target,
    ...(target === "events" ? { run_tab: "events" as const } : {}),
  };
}
