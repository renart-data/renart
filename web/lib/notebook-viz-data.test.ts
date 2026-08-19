import { describe, expect, it } from "vitest";

import {
  boundedRowsToObjects,
  NOTEBOOK_CHART_SERIES_CAP,
  pivotChartSeries,
} from "@/lib/notebook-viz-data";

describe("notebook visualization data budgets", () => {
  it("converts only the rows inside the presentation budget", () => {
    expect(
      boundedRowsToObjects(
        ["category", "value"],
        [
          ["first", 1],
          ["second", 2],
          ["third", 3],
        ],
        2,
      ),
    ).toEqual([
      { category: "first", value: 1 },
      { category: "second", value: 2 },
    ]);
  });

  it("caps pivoted chart series without hiding the full series count", () => {
    const rows = Array.from({ length: NOTEBOOK_CHART_SERIES_CAP + 3 }, (_, index) => ({
      month: "august",
      provider: `provider-${String(index).padStart(2, "0")}`,
      value: index,
    }));

    const result = pivotChartSeries(rows, "month", "value", "provider");

    expect(result.series).toHaveLength(NOTEBOOK_CHART_SERIES_CAP);
    expect(result.totalSeries).toBe(NOTEBOOK_CHART_SERIES_CAP + 3);
    expect(Object.keys(result.data[0])).toHaveLength(NOTEBOOK_CHART_SERIES_CAP + 1);
    expect(result.data[0]).not.toHaveProperty(`provider-${NOTEBOOK_CHART_SERIES_CAP}`);
  });
});
