import { describe, expect, it } from "vitest";

import {
  authoredControlDefinitionsProblem,
  authoredControlOptions,
  defaultAuthoredControlValue,
  normalizeAuthoredControlList,
} from "./authored-controls";

describe("authored controls", () => {
  it("uses one typed default contract across document hosts", () => {
    const today = "2026-08-14";
    expect(defaultAuthoredControlValue("text", today)).toBe("");
    expect(defaultAuthoredControlValue("number", today)).toBe(0);
    expect(defaultAuthoredControlValue("slider", today)).toBe(50);
    expect(defaultAuthoredControlValue("boolean", today)).toBe(false);
    expect(defaultAuthoredControlValue("multi_select", today)).toEqual([]);
    expect(defaultAuthoredControlValue("date", today)).toBe(today);
    expect(defaultAuthoredControlValue("date_range", today)).toEqual([today, today]);
  });

  it("normalizes lists and validates stable control IDs", () => {
    expect(normalizeAuthoredControlList(" north, south, , west ")).toEqual([
      "north",
      "south",
      "west",
    ]);
    expect(
      authoredControlDefinitionsProblem([
        { id: "region", type: "select", default: "north" },
        { id: "region", type: "text", default: "" },
      ]),
    ).toContain("more than once");
    expect(
      authoredControlDefinitionsProblem([{ id: "Region", type: "text", default: "" }]),
    ).toContain("lowercase id");
  });

  it("resolves static and dataset-backed options without duplicate values", () => {
    expect(
      authoredControlOptions({
        id: "region",
        type: "select",
        default: "eu",
        options: { values: ["eu", "us"] },
      }),
    ).toEqual([
      { value: "eu", label: "eu" },
      { value: "us", label: "us" },
    ]);
    expect(
      authoredControlOptions(
        {
          id: "region",
          type: "select",
          default: "eu",
          options: { dataset: "regions", value_field: "code", label_field: "name" },
        },
        {
          status: "ok",
          columns: ["code", "name"],
          rows: [
            ["eu", "Europe"],
            ["eu", "Europe duplicate"],
            ["us", "United States"],
          ],
        },
      ),
    ).toEqual([
      { value: "eu", label: "Europe" },
      { value: "us", label: "United States" },
    ]);
  });
});
