import { describe, expect, it } from "vitest";

import type { AssetStaleness } from "@/lib/api-staleness";

import {
  gettingStartedItems,
  gettingStartedProgress,
  observedEdit,
  observedOwnConnection,
  observedRebuild,
  observedRouteItem,
} from "./getting-started";

const staleness = (overrides: Partial<AssetStaleness>): AssetStaleness => ({
  asset_id: "a",
  asset_name: "a",
  status: "fresh",
  fingerprint: "",
  interval_aware: false,
  backfill_safe: false,
  target_fidelity: "exact",
  ...overrides,
});

describe("getting started observations", () => {
  it("sees an edit once an asset is marked Edited", () => {
    expect(observedEdit([staleness({ status: "stale_upstream" })])).toBe(false);
    expect(observedEdit([staleness({ status: "stale_edited" })])).toBe(true);
  });

  it("sees a rebuild only after the edit", () => {
    const editedAt = "2026-10-02T10:00:00Z";
    const before = staleness({ last_run_status: "succeeded", last_run_at: "2026-10-02T09:00:00Z" });
    const after = staleness({ last_run_status: "succeeded", last_run_at: "2026-10-02T10:05:00Z" });
    const failed = staleness({ last_run_status: "failed", last_run_at: "2026-10-02T10:05:00Z" });
    expect(observedRebuild([before], editedAt)).toBe(false);
    expect(observedRebuild([failed], editedAt)).toBe(false);
    expect(observedRebuild([after], undefined)).toBe(false);
    expect(observedRebuild([before, after], editedAt)).toBe(true);
  });

  it("counts any non-DuckDB connection as the user's own data", () => {
    const duckdb = { name: "duckdb-default", type: "duckdb" } as never;
    const postgres = { name: "postgres-default", type: "postgres" } as never;
    expect(observedOwnConnection([{ name: "default", connections: [duckdb] }])).toBe(false);
    expect(observedOwnConnection([{ name: "default", connections: [duckdb, postgres] }])).toBe(
      true,
    );
  });

  it("maps notebook and dashboard pages to their items", () => {
    expect(observedRouteItem("/notebooks/abc")).toBe("notebook");
    expect(observedRouteItem("/notebooks")).toBeNull();
    expect(observedRouteItem("/dashboards/product_overview")).toBe("dashboard");
    expect(observedRouteItem("/pipelines/x/canvas")).toBeNull();
  });

  it("reports progress over every item", () => {
    expect(
      gettingStartedProgress({
        version: 1,
        startedAt: "",
        dismissed: false,
        done: { inspect: "t", edit: "t" },
      }),
    ).toEqual({ done: 2, total: gettingStartedItems.length });
  });
});
