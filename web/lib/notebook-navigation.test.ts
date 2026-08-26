import { describe, expect, it } from "vitest";

import type { WebAsset, WebNotebook } from "@/lib/types";
import {
  buildNotebookFlowModel,
  buildNotebookNavigationEntries,
  filterNotebookNavigationEntries,
  notebookFlowContext,
} from "@/lib/notebook-navigation";

function cell(
  input: Partial<WebAsset> & Pick<WebAsset, "id" | "name" | "type" | "path">,
): WebAsset {
  return {
    content: "",
    upstreams: [],
    is_materialized: false,
    ...input,
  };
}

function notebook(): WebNotebook {
  const source = cell({
    id: "events-source",
    cell_id: "events",
    name: "earthquake_events",
    type: "duckdb.sql",
    path: "notebooks/earthquakes/events.sql",
    content: "select * from earthquakes.events",
    external_refs: ["earthquakes.events"],
    columns: [{ name: "magnitude", type: "DOUBLE" }],
  });
  const python = cell({
    id: "summarize",
    cell_id: "summary",
    name: "summarize_events",
    type: "python",
    path: "notebooks/earthquakes/summary.py",
    content: "import pandas as pd\nresult = earthquake_events.groupby('region').size()",
    upstreams: ["earthquake_events"],
  });

  return {
    id: "earthquakes",
    manifest_version: 2,
    revision: "rev-1",
    title: "Earthquakes",
    path: "notebooks/earthquakes",
    cells: [source, python],
    parameters: [
      {
        id: "minimum_magnitude",
        label: "Minimum magnitude",
        type: "slider",
        default: 4,
        min: 0,
        max: 10,
      },
    ],
    blocks: [
      { id: "intro", markdown: "# Regional overview\nRecent seismic activity." },
      { cell: "events" },
      { cell: "summary" },
      {
        id: "regional-chart",
        visualization: {
          id: "regional-chart",
          source: "summarize_events",
          definition: { type: "bar", title: "Events by region" },
        },
      },
    ],
  };
}

describe("notebook navigation", () => {
  it("searches code, columns, Markdown, controls, and visualization metadata", () => {
    const entries = buildNotebookNavigationEntries(notebook());

    expect(filterNotebookNavigationEntries(entries, "pandas").map((entry) => entry.title)).toEqual([
      "summarize_events",
    ]);
    expect(
      filterNotebookNavigationEntries(entries, "magnitude double").map((entry) => entry.title),
    ).toEqual(["earthquake_events"]);
    expect(filterNotebookNavigationEntries(entries, "seismic").map((entry) => entry.title)).toEqual(
      ["Regional overview"],
    );
    expect(
      filterNotebookNavigationEntries(entries, "minimum slider").map((entry) => entry.title),
    ).toEqual(["Minimum magnitude"]);
    expect(
      filterNotebookNavigationEntries(entries, "events region bar").map((entry) => entry.title),
    ).toEqual(["Events by region"]);
  });

  it("keeps search results in notebook outline order and includes a useful snippet", () => {
    const matches = filterNotebookNavigationEntries(
      buildNotebookNavigationEntries(notebook()),
      "earthquake",
    );

    expect(matches.map((entry) => entry.title)).toEqual(["earthquake_events", "summarize_events"]);
    expect(matches[1].snippet).toContain("earthquake_events.groupby");
  });
});

describe("notebook flow", () => {
  it("builds internal downstream edges and keeps external references visible", () => {
    const model = buildNotebookFlowModel(notebook());
    const source = model.nodes.find((node) => node.cellId === "events")!;
    const python = model.nodes.find((node) => node.cellId === "summary")!;
    const chart = model.nodes.find((node) => node.key === "block:regional-chart")!;

    expect(source.externalReferences).toEqual(["earthquakes.events"]);
    expect(python.upstreamNodeIds).toEqual([source.key]);
    expect(chart.upstreamNodeIds).toEqual([python.key]);
    expect(source.downstreamNodeIds).toEqual([python.key]);
    expect([source.depth, python.depth, chart.depth]).toEqual([0, 1, 2]);

    const context = notebookFlowContext(model, python.key);
    expect([...context.ancestors]).toEqual([source.key]);
    expect([...context.descendants]).toEqual([chart.key]);
  });

  it("is cycle-safe when incomplete parser metadata contains a loop", () => {
    const value = notebook();
    value.cells[0].upstreams = ["summarize_events"];

    const model = buildNotebookFlowModel(value);

    expect(model.nodes.find((node) => node.cellId === "events")?.depth).toBeTypeOf("number");
    expect(model.nodes.find((node) => node.cellId === "summary")?.depth).toBeTypeOf("number");
    const context = notebookFlowContext(model, "cell:events");
    expect(context.ancestors.has("cell:summary")).toBe(true);
    expect(context.ancestors.has("cell:events")).toBe(false);
  });
});
