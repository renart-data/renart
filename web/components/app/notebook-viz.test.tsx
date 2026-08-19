import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { NotebookVisualizationRenderer } from "@/components/app/notebook-viz";
import type { NotebookCellRunResult } from "@/lib/api-notebooks";

const result: NotebookCellRunResult = {
  cell_id: "cell-revenue",
  name: "revenue",
  object_name: "cell_revenue",
  status: "ok",
  columns: ["month", "revenue"],
  column_types: ["VARCHAR", "BIGINT"],
  rows: [
    ["jan", 10],
    ["feb", 20],
  ],
  total_rows: 2,
  materialized: "view",
  duration_ms: 4,
};

describe("NotebookVisualizationRenderer accessibility", () => {
  it("names definition-backed tables from their title, fields, and row count", () => {
    const markup = renderToStaticMarkup(
      <NotebookVisualizationRenderer
        definition={{
          version: 1,
          type: "table",
          title: "Monthly revenue",
          columns: [{ field: "month" }, { field: "revenue" }],
        }}
        result={result}
      />,
    );

    expect(markup).toContain(
      'aria-label="Monthly revenue. table visualization. Fields: month, revenue. 2 rows"',
    );
  });

  it("gives charts an accessible figure name", () => {
    const markup = renderToStaticMarkup(
      <NotebookVisualizationRenderer
        definition={{
          version: 1,
          type: "line",
          title: "Monthly revenue",
          encoding: { x: { field: "month" }, y: [{ field: "revenue" }] },
        }}
        result={result}
      />,
    );

    expect(markup).toContain(
      '<figure aria-label="Monthly revenue. line visualization. Fields: month, revenue. 2 rows"',
    );
  });
});
