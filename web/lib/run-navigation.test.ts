import { expect, it } from "vitest";
import { normalizeRunLocation, runAssetLocation, runNavigationTargetKey } from "./run-navigation";

it("a timeline location does not switch the independent output panel", () => {
  expect(runAssetLocation({ run_tab: "output" }, "analytics.orders", "timeline")).toEqual({
    run_tab: "output",
    run_asset: "analytics.orders",
    run_focus: "timeline",
  });
  expect(runAssetLocation({ run_tab: "output" }, "analytics.orders", "events").run_tab).toBe(
    "events",
  );
});
it("only accepts implemented run views and bounded asset identities", () => {
  expect(normalizeRunLocation({ run_tab: "execute", run_asset: "a".repeat(4097) })).toEqual({
    run_tab: undefined,
    run_asset: undefined,
    run_focus: undefined,
  });
  expect(
    normalizeRunLocation({ run_tab: "plan", run_asset: "analytics.orders", run_focus: "timeline" })
      .run_tab,
  ).toBe("plan");
});

it("event links reveal Events while timeline links preserve the independent tab", () => {
  expect(
    normalizeRunLocation({ run_tab: "output", run_asset: "analytics.orders", run_focus: "events" })
      .run_tab,
  ).toBe("events");
  expect(normalizeRunLocation({ run_tab: "output", run_focus: "events" }).run_tab).toBe("output");
});

it("arrival identity includes the project, run, asset and target, not independent filters", () => {
  const search = { run_asset: "analytics.orders", run_focus: "timeline", run_tab: "output" };
  const key = runNavigationTargetKey("project-a", "/runs/run-1", search);
  expect(key).not.toBe("");
  expect(
    runNavigationTargetKey("project-a", "/runs/run-1", { ...search, run_tab: "plan", q: "a" }),
  ).toBe(key);
  for (const [project, path, target] of [
    ["project-b", "/runs/run-1", search],
    ["project-a", "/runs/run-2", search],
    ["project-a", "/runs/run-1", { ...search, run_asset: "analytics.other" }],
    ["project-a", "/runs/run-1", { ...search, run_focus: "events" }],
  ] as const)
    expect(runNavigationTargetKey(project, path, target)).not.toBe(key);
});

it("does not invent an arrival for invalid or non-run locations", () => {
  const search = { run_asset: "analytics.orders", run_focus: "timeline" };
  for (const path of ["/runs", "/runs/", "/runs/run-1/other", "/pipelines/run-1/canvas"])
    expect(runNavigationTargetKey("project-a", path, search)).toBe("");
  for (const invalid of [
    {},
    { run_asset: "a" },
    { ...search, run_focus: "unknown" },
    { ...search, run_asset: "a".repeat(4097) },
  ])
    expect(runNavigationTargetKey("project-a", "/runs/run-1", invalid)).toBe("");
});
