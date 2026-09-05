export type RunLocation = {
  run_tab?: "events" | "plan" | "output";
  run_asset?: string;
  run_focus?: "events" | "timeline";
};

export function normalizeRunLocation(search: Record<string, unknown>): RunLocation {
  return {
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
