import { describe, expect, it } from "vitest";

import type { ArtifactIndex } from "./generated/api-types";
import { columnImpactsForAsset } from "./artifact-column-impact";

describe("artifact column impact", () => {
  it("projects and orders the known downstream uses for one workspace asset", () => {
    const index: ArtifactIndex = {
      revision: "v1:test",
      artifacts: [
        {
          id: "pipeline:raw.orders",
          kind: "pipeline_asset",
          workspace_id: "orders-workspace-id",
          path: "assets/orders.sql",
          title: "raw.orders",
          capabilities: ["has_schema"],
        },
        {
          id: "pipeline:analytics.summary",
          kind: "pipeline_asset",
          workspace_id: "summary-workspace-id",
          path: "assets/summary.sql",
          title: "analytics.summary",
          capabilities: ["has_schema"],
        },
        {
          id: "sales",
          kind: "dashboard",
          path: "dashboards/sales.dashboard.yml",
          title: "Sales",
          capabilities: ["presentation"],
          components: [
            {
              id: "visualization:revenue",
              kind: "visualization",
              name: "Revenue by month",
              capabilities: ["presentation"],
            },
          ],
        },
      ],
      breaking_column_impacts: [
        {
          producer: { kind: "pipeline_asset", artifact_id: "pipeline:raw.orders" },
          column: "amount",
          consumer: {
            kind: "dashboard",
            artifact_id: "sales",
            component_id: "visualization:revenue",
          },
          role: "encoding.y[0].field",
          distance: 3,
        },
        {
          producer: { kind: "pipeline_asset", artifact_id: "pipeline:raw.orders" },
          column: "Amount",
          consumer: { kind: "pipeline_asset", artifact_id: "pipeline:analytics.summary" },
          consumer_column: "revenue",
          distance: 1,
        },
        {
          producer: { kind: "pipeline_asset", artifact_id: "another" },
          column: "amount",
          consumer: { kind: "pipeline_asset", artifact_id: "pipeline:analytics.summary" },
          consumer_column: "revenue",
          distance: 1,
        },
      ],
    };

    expect(columnImpactsForAsset(index, "orders-workspace-id").get("amount")).toEqual([
      expect.objectContaining({
        label: "analytics.summary",
        useLabel: "output column revenue",
        distance: 1,
      }),
      expect.objectContaining({
        label: "Sales / Revenue by month",
        useLabel: "encoding.y[0].field",
        distance: 3,
      }),
    ]);
  });

  it("returns no impact when the workspace asset is absent", () => {
    expect(columnImpactsForAsset({ revision: "v1:none", artifacts: [] }, "missing").size).toBe(0);
  });
});
