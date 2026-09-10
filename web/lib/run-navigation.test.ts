import { expect, it } from "vitest";
import { normalizeRunLocation, runAssetLocation } from "./run-navigation";

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
