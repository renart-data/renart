import { describe, expect, it } from "vitest";

import { buildSuggestedAssetName, suggestedAssetPrefix } from "@/lib/workspace-shell-helpers";

describe("buildSuggestedAssetName", () => {
  it("uses the pipeline prefix and first available sequence number", () => {
    expect(
      buildSuggestedAssetName(
        "sql",
        new Set(["sales_ops.my_sql_asset_1", "sales_ops.my_sql_asset_2"]),
        "Sales Ops",
      ),
    ).toBe("sales_ops.my_sql_asset_3");
  });

  it("uses a stable default prefix when the pipeline name has no slug characters", () => {
    expect(buildSuggestedAssetName("api", new Set(), "///")).toBe("default.my_api_asset_1");
  });
});

describe("suggestedAssetPrefix", () => {
  it("joins the group the user pointed at", () => {
    expect(
      buildSuggestedAssetName("sql", new Set(["product.events"]), "analytics", "staging"),
    ).toBe("staging.my_sql_asset_1");
  });

  it("joins the pipeline's most common prefix instead of starting a new group", () => {
    expect(
      buildSuggestedAssetName(
        "python",
        new Set(["product.events", "product.users", "raw.orders"]),
        "product_analytics",
      ),
    ).toBe("product.my_python_asset_1");
  });

  it("breaks ties alphabetically and keeps nested prefixes", () => {
    expect(suggestedAssetPrefix(["b.orders", "a.b.users"], "analytics")).toBe("a.b");
  });

  it("falls back to the pipeline name when no asset has a prefix", () => {
    expect(suggestedAssetPrefix(["orders"], "Sales Ops")).toBe("sales_ops");
  });
});
