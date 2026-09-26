import { describe, expect, it } from "vitest";
import { chartAxisKind, chartAxisValue, formatChartTick } from "./chart-axis";
describe("chart axes", () => {
  it("keeps identifiers categorical and detects typed quantities", () => {
    expect(chartAxisKind("VARCHAR", ["00123"])).toBe("categorical");
    expect(chartAxisKind("BIGINT", ["12000000"])).toBe("numeric");
    expect(chartAxisKind(undefined, [null, 3, 12])).toBe("numeric");
    expect(chartAxisKind("TIMESTAMP", [])).toBe("temporal");
  });
  it("shortens axis labels while leaving raw data available to tooltips", () => {
    expect(formatChartTick("a very long category with more detail", "categorical")).toBe(
      "a very long category …",
    );
    expect(formatChartTick(12345678, "numeric").length).toBeLessThan(9);
    expect(chartAxisValue("2026-09-22 12:00:00", "temporal")).toBe(Date.UTC(2026, 8, 22, 12));
    expect(chartAxisValue("invalid", "temporal")).toBeNull();
    expect(chartAxisValue("group A", "categorical")).toBe("group A");
  });
});
