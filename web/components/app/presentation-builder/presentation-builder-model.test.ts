import { describe, expect, it } from "vitest";

import type { PresentationArtifact } from "@/lib/api-presentations";

import {
  derivedDashboardSpan,
  generatedPresentationID,
  normalizedDashboardLayout,
  presentationLayoutFromGrid,
  presentationFindingTarget,
  semanticTypeForPhysicalType,
  updateDashboardLayoutItem,
  visualizationSuggestionForType,
  visualizationSuggestions,
} from "./presentation-builder-model";
import { initialPresentationDraftState, presentationDraftReducer } from "./use-presentation-draft";

function artifact(overrides: Partial<PresentationArtifact> = {}): PresentationArtifact {
  return {
    id: "sales",
    workspace_id: "dashboards/sales.dashboard.yml",
    kind: "dashboard",
    version: 1,
    revision: "revision-1",
    title: "Sales",
    path: "dashboards/sales.dashboard.yml",
    datasets: [],
    filters: [],
    visualizations: [],
    layout: [],
    sections: [],
    ...overrides,
  };
}

describe("presentation draft history", () => {
  it("coalesces consecutive text edits and keeps structural commands separate", () => {
    const initial = artifact();
    let state = initialPresentationDraftState(initial);
    state = presentationDraftReducer(state, {
      type: "commit",
      artifact: { ...initial, title: "Sales o" },
      coalesceKey: "artifact:title",
      timestamp: 100,
    });
    state = presentationDraftReducer(state, {
      type: "commit",
      artifact: { ...initial, title: "Sales overview" },
      coalesceKey: "artifact:title",
      timestamp: 200,
    });
    state = presentationDraftReducer(state, {
      type: "commit",
      artifact: {
        ...initial,
        title: "Sales overview",
        filters: [{ id: "region", type: "text", default: "" }],
      },
      coalesceKey: "",
      timestamp: 300,
    });

    expect(state.past.map((entry) => entry.title)).toEqual(["Sales", "Sales overview"]);
    state = presentationDraftReducer(state, { type: "undo" });
    expect(state.present.title).toBe("Sales overview");
    expect(state.present.filters).toEqual([]);
    state = presentationDraftReducer(state, { type: "undo" });
    expect(state.present.title).toBe("Sales");
    state = presentationDraftReducer(state, { type: "redo" });
    expect(state.present.title).toBe("Sales overview");
  });
});

describe("dashboard layout model", () => {
  it("normalizes invalid positions and serializes in deterministic visual order", () => {
    const dashboard = artifact({
      visualizations: [
        { id: "later", dataset: "dataset", definition: {} },
        { id: "first", dataset: "dataset", definition: {} },
      ],
      layout: [
        { visualization: "later", x: 10, y: 8, width: 6, height: 1 },
        { visualization: "first", x: -2, y: 0, width: 20, height: 4 },
      ],
    });

    const normalized = normalizedDashboardLayout(dashboard);
    expect(normalized.find((item) => item.i === "first")).toMatchObject({ x: 0, w: 12, h: 4 });
    expect(normalized.find((item) => item.i === "later")).toMatchObject({ x: 6, w: 6, h: 2 });
    expect(presentationLayoutFromGrid(normalized).map((item) => item.visualization)).toEqual([
      "first",
      "later",
    ]);
  });

  it("keeps keyboard layout changes within the twelve-column grid", () => {
    const dashboard = artifact({
      visualizations: [{ id: "chart", dataset: "dataset", definition: {} }],
      layout: [{ visualization: "chart", x: 6, width: 6, height: 4 }],
    });
    const updated = updateDashboardLayoutItem(normalizedDashboardLayout(dashboard), "chart", {
      x: 11,
      w: 8,
    });
    expect(updated[0]).toMatchObject({ visualization: "chart", x: 4, width: 8 });
    expect(derivedDashboardSpan(9, "tablet")).toBe(2);
    expect(derivedDashboardSpan(9, "mobile")).toBe(1);
  });
});

describe("presentation finding navigation", () => {
  it("maps checker paths to stable builder selections", () => {
    const dashboard = artifact({
      datasets: [{ id: "sales", asset: "analytics.sales" }],
      filters: [{ id: "region", type: "text", default: "" }],
      visualizations: [{ id: "revenue", dataset: "sales", definition: {} }],
      layout: [{ visualization: "revenue", width: 6, height: 4 }],
      sections: [{ id: "summary", markdown: "Hello" }],
    });

    expect(
      presentationFindingTarget(dashboard, { path: "datasets.sales.connection" }).selection,
    ).toEqual({ kind: "dataset", id: "sales" });
    expect(presentationFindingTarget(dashboard, { path: "filters[0].default" }).selection).toEqual({
      kind: "filter",
      id: "region",
    });
    expect(
      presentationFindingTarget(dashboard, {
        path: "visualizations[0].definition.encoding.x.field",
      }).selection,
    ).toEqual({ kind: "visualization", id: "revenue" });
    expect(presentationFindingTarget(dashboard, { path: "sections[0].id" }).selection).toEqual({
      kind: "section",
      id: "summary",
    });
    expect(presentationFindingTarget(dashboard, { path: "layout[0].width" }).selection).toEqual({
      kind: "visualization",
      id: "revenue",
    });
    expect(presentationFindingTarget(dashboard, { path: "title" }).selection).toEqual({
      kind: "artifact",
    });
  });
});

describe("schema-aware suggestions", () => {
  it("offers deterministic compatible charts and collision-safe IDs", () => {
    const suggestions = visualizationSuggestions([
      { name: "occurred_at", physical_type: "timestamp", semantic_type: "temporal" },
      { name: "region", physical_type: "varchar", semantic_type: "categorical" },
      { name: "revenue", physical_type: "decimal", semantic_type: "numeric" },
      { name: "orders", physical_type: "bigint", semantic_type: "numeric" },
    ]);

    expect(suggestions.map((suggestion) => suggestion.type)).toEqual([
      "table",
      "kpi",
      "bar",
      "line",
      "area",
      "scatter",
      "pie",
      "donut",
    ]);
    expect(generatedPresentationID("Revenue by region", "visualization", [])).toBe(
      "revenue_by_region",
    );
    expect(
      generatedPresentationID("Revenue by region", "visualization", [
        "revenue_by_region",
        "revenue_by_region_2",
      ]),
    ).toBe("revenue_by_region_3");
  });

  it("derives warehouse types and never assigns categorical columns to numeric encodings", () => {
    expect(semanticTypeForPhysicalType("BIGINT")).toBe("numeric");
    expect(semanticTypeForPhysicalType("VARCHAR(255)")).toBe("categorical");
    expect(semanticTypeForPhysicalType("TIMESTAMPTZ")).toBe("temporal");

    const line = visualizationSuggestionForType(
      [{ name: "archive_url", physical_type: "VARCHAR", semantic_type: "categorical" }],
      "line",
    );
    expect(line.definition).toMatchObject({ type: "line" });
    expect(line.definition).not.toHaveProperty("encoding.y");

    const bar = visualizationSuggestionForType(
      [
        { name: "archive_url", physical_type: "VARCHAR", semantic_type: "categorical" },
        { name: "downloads", physical_type: "BIGINT", semantic_type: "numeric" },
      ],
      "bar",
    );
    expect(bar.definition).toMatchObject({
      encoding: { x: { field: "archive_url" }, y: [{ field: "downloads" }] },
    });
  });
});
