import { expect, test } from "@playwright/test";

import {
  computeAppLineageLayout,
  initialCenteredViewportX,
  type AppLineageLayoutEdge,
  type AppLineageLayoutNode,
} from "../../../lib/app-lineage-layout";

function node(id: string): AppLineageLayoutNode {
  const [layer, ...nameParts] = id.split(".");
  return { id, layer, name: nameParts.join(".") || id };
}

function edge(source: string, target: string): AppLineageLayoutEdge {
  return { source, target };
}

test.describe("app lineage layout engine", () => {
  test("centers a fitting graph in the initial viewport", () => {
    const x = initialCenteredViewportX({
      viewportWidth: 1000,
      graphMinX: 100,
      graphMaxX: 500,
    });

    expect(x).toBe(200);
  });

  test("leaves an overflowing graph at its existing horizontal position", () => {
    expect(
      initialCenteredViewportX({
        viewportWidth: 1000,
        graphMinX: 0,
        graphMaxX: 953,
      }),
    ).toBeNull();
  });

  test("recommends strict layers for cleanly layered DAGs", () => {
    const layout = computeAppLineageLayout({
      nodes: [node("raw.orders"), node("staging.orders"), node("core.orders")],
      edges: [edge("raw.orders", "staging.orders"), edge("staging.orders", "core.orders")],
    });

    expect(layout.recommendation?.id).toBe("strict");
    expect(layout.layoutId).toBe("strict");
    expect(layout.analysis.layerLinearizable).toBe(true);
    expect(layout.analysis.skipEdges).toHaveLength(0);
    expect(layout.analysis.intraEdges).toHaveLength(0);
    expect(layout.analysis.backEdges).toHaveLength(0);
    expect(layout.positions.get("raw.orders")!.x).toBeLessThan(
      layout.positions.get("staging.orders")!.x,
    );
    expect(layout.positions.get("staging.orders")!.x).toBeLessThan(
      layout.positions.get("core.orders")!.x,
    );
  });

  test("recommends layer bands when layer order is cyclic but the asset graph is a DAG", () => {
    const layout = computeAppLineageLayout({
      nodes: [
        node("raw.orders"),
        node("staging.orders"),
        node("core.orders"),
        node("marts.churn_features"),
        node("ml.churn_scores"),
        node("core.customers_scored"),
      ],
      edges: [
        edge("raw.orders", "staging.orders"),
        edge("staging.orders", "core.orders"),
        edge("core.orders", "marts.churn_features"),
        edge("marts.churn_features", "ml.churn_scores"),
        edge("ml.churn_scores", "core.customers_scored"),
      ],
    });

    expect(layout.recommendation?.id).toBe("bands");
    expect(layout.layoutId).toBe("bands");
    expect(layout.analysis.layerLinearizable).toBe(false);
    expect(layout.analysis.cyclicNodes).toHaveLength(0);
  });

  test("recommends force layout for graph cycles", () => {
    const layout = computeAppLineageLayout({
      nodes: [node("raw.a"), node("raw.b")],
      edges: [edge("raw.a", "raw.b"), edge("raw.b", "raw.a")],
    });

    expect(layout.recommendation?.id).toBe("force");
    expect(layout.layoutId).toBe("force");
    expect(layout.analysis.cyclicNodes.sort()).toEqual(["raw.a", "raw.b"]);
    expect(Number.isFinite(layout.positions.get("raw.a")?.x)).toBe(true);
    expect(Number.isFinite(layout.positions.get("raw.a")?.y)).toBe(true);
  });

  test("can explicitly compute layer bands without using dependency-rank layout", () => {
    const layout = computeAppLineageLayout({
      layoutId: "bands",
      nodes: [
        node("raw.orders"),
        node("staging.orders"),
        node("staging.items"),
        node("core.orders"),
      ],
      edges: [
        edge("raw.orders", "staging.orders"),
        edge("raw.orders", "staging.items"),
        edge("staging.orders", "core.orders"),
        edge("staging.items", "core.orders"),
      ],
    });

    expect(layout.layoutId).toBe("bands");
    expect(layout.recommendation?.id).not.toBe("topo");
    expect(layout.positions.get("raw.orders")!.x).toBeLessThan(
      layout.positions.get("core.orders")!.x,
    );
    expect(layout.positions.get("staging.orders")!.y).not.toBe(
      layout.positions.get("raw.orders")!.y,
    );
  });

  test("reserves a deterministic lane for same-band edges that skip a dependency rank", () => {
    const layout = computeAppLineageLayout({
      nodes: [
        node("ops.device_events"),
        node("ops.events_ready"),
        node("ops.device_health"),
        node("ops.incident_queue"),
        node("ops.fleet_overview"),
      ],
      edges: [
        edge("ops.device_events", "ops.events_ready"),
        edge("ops.device_events", "ops.device_health"),
        edge("ops.events_ready", "ops.device_health"),
        edge("ops.device_health", "ops.incident_queue"),
        edge("ops.device_health", "ops.fleet_overview"),
      ],
    });

    const events = layout.positions.get("ops.device_events")!;
    const sensor = layout.positions.get("ops.events_ready")!;
    const health = layout.positions.get("ops.device_health")!;

    expect(layout.layoutId).toBe("bands");
    expect(events.y).toBe(health.y);
    expect(sensor.y).toBeGreaterThan(events.y + 96);
  });

  test("reserves an upstream prefix block before every node in a downstream prefix", () => {
    const nodes = [
      node("example.report"),
      node("public.accounts"),
      node("example.remote"),
      node("public.account_status"),
    ];
    const edges = [
      edge("public.accounts", "public.account_status"),
      edge("public.accounts", "example.remote"),
      edge("example.remote", "example.report"),
    ];
    const layout = computeAppLineageLayout({ layoutId: "bands", nodes, edges });
    const permuted = computeAppLineageLayout({
      layoutId: "bands",
      nodes: nodes.slice().reverse(),
      edges: edges.slice().reverse(),
    });

    const publicX = ["public.accounts", "public.account_status"].map(
      (id) => layout.positions.get(id)!.x,
    );
    const exampleX = ["example.remote", "example.report"].map((id) => layout.positions.get(id)!.x);
    expect(Math.max(...publicX)).toBeLessThan(Math.min(...exampleX));
    for (const asset of nodes) {
      expect(permuted.positions.get(asset.id)).toEqual(layout.positions.get(asset.id));
    }
  });
});
