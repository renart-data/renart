import { describe, expect, it } from "vitest";

import type { WebAsset } from "./types";
import { classifyDependencies, columnStatus, parseAssetProvenance } from "./asset-provenance";

function webAsset(overrides: Partial<WebAsset>): WebAsset {
  return {
    id: "asset-id",
    name: "analytics.consumer",
    type: "duckdb.sql",
    path: "assets/consumer.sql",
    content: "select 1",
    upstreams: [],
    is_materialized: false,
    ...overrides,
  };
}

describe("column-local asset provenance", () => {
  it("reads the Bruin column meta key and lets it override legacy asset metadata", () => {
    const provenance = parseAssetProvenance({ renart_col_src: "customer_id:l;legacy_id:m" }, [
      { name: "customer_id", meta: { renart_source: "m", semantic_type: "identifier" } },
      { name: "email", meta: { renart_source: "l" } },
      { name: "manual_note", meta: { renart_manual: "true" } },
      { name: "amount", meta: { renart_owned: "type|description" } },
    ]);

    expect(columnStatus("customer_id", provenance)).toBe("table-inferred");
    expect(columnStatus("email", provenance)).toBe("live-inferred");
    expect(columnStatus("manual_note", provenance)).toBe("manual");
    expect(columnStatus("amount", provenance)).toBe("type-owned");
    expect(columnStatus("legacy_id", provenance)).toBe("table-inferred");
  });
});

describe("typed dependency provenance", () => {
  it("preserves URI identity, mode, and resolved producer context", () => {
    const asset = webAsset({
      upstreams: ["warehouse://orders"],
      dependencies: [
        {
          type: "uri",
          value: "warehouse://orders",
          mode: "symbolic",
          resolved_asset_id: "raw-orders",
          resolved_asset_name: "raw.orders",
          resolved_pipeline_id: "raw",
          resolved_pipeline_name: "Raw ingestion",
        },
      ],
      meta: { renart_dep_add: "u:warehouse://orders#symbolic" },
    });

    const result = classifyDependencies(asset);
    expect(result.inferred).toEqual([]);
    expect(result.manual).toEqual([
      expect.objectContaining({
        kind: "uri",
        value: "warehouse://orders",
        name: "raw.orders",
        mode: "symbolic",
        resolvedAssetId: "raw-orders",
        resolvedPipelineId: "raw",
        resolvedPipelineName: "Raw ingestion",
      }),
    ]);
  });

  it("does not conflate an asset name with the same URI value", () => {
    const asset = webAsset({
      upstreams: ["warehouse://orders", "warehouse://orders"],
      dependencies: [
        { type: "asset", value: "warehouse://orders", mode: "full" },
        { type: "uri", value: "warehouse://orders", mode: "full" },
      ],
      meta: { renart_dep_add: "u:warehouse://orders#full" },
    });

    const result = classifyDependencies(asset);
    expect(result.inferred).toEqual([
      expect.objectContaining({ kind: "asset", value: "warehouse://orders" }),
    ]);
    expect(result.manual).toEqual([
      expect.objectContaining({ kind: "uri", value: "warehouse://orders" }),
    ]);
  });
});
