import { describe, expect, it } from "vitest";
import {
  detailSearch,
  parseDetail,
  normalizeResourceSearch,
  resolveColumn,
} from "./resource-navigation";

const detail = {
  v: 1 as const,
  environment: "dev",
  target: {
    kind: "asset-column" as const,
    asset_id: "encoded-asset",
    column: "Total ä.?",
    field: "type" as const,
  },
};

describe("resource navigation", () => {
  it("round trips structured targets without changing the primary search", () => {
    const search = detailSearch(
      { result: "query", editor: "adhoc", custom: "kept" },
      "project-a",
      detail,
    );
    expect(search).toMatchObject({
      result: "query",
      editor: "adhoc",
      custom: "kept",
      project: "project-a",
    });
    expect(parseDetail(JSON.parse(JSON.stringify(search.detail)))).toEqual(detail);
  });
  it("rejects malformed, oversized and unsupported destinations", () => {
    for (const value of [
      null,
      "bad",
      { ...detail, v: 2 },
      { ...detail, environment: "" },
      { ...detail, target: { ...detail.target, kind: "arbitrary-route" } },
      { ...detail, target: { ...detail.target, field: "password" } },
      { ...detail, target: { ...detail.target, column: "x".repeat(1025) } },
    ]) {
      expect(() => parseDetail(value)).toThrow();
    }
    expect(normalizeResourceSearch({ project: "p", detail })).toEqual({ project: "p", detail });
  });
  it("requires explicit project scope for a cold detail link", () => {
    expect(() => normalizeResourceSearch({ detail })).toThrow(/project/i);
  });
  it("never focuses a different or ambiguous column", () => {
    expect(resolveColumn([{ name: "ID" }], "id")).toBeUndefined();
    expect(resolveColumn([{ name: "id" }, { name: "id" }], "id")).toBeUndefined();
    expect(resolveColumn([{ name: "ID" }, { name: "id" }], "id")).toEqual({ name: "id" });
  });
});
