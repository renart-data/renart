import { describe, expect, it } from "vitest";
import {
  detailSearch,
  parseDetail,
  normalizeResourceSearch,
  resolveColumn,
  resourceHref,
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
  it("addresses every editable column field, not just diagnostic type fixes", () => {
    for (const field of ["type", "description", "primary_key", "update_on_merge", "merge_sql"])
      expect(parseDetail({ ...detail, target: { ...detail.target, field } }).target.field).toBe(
        field,
      );
  });
  it("builds a cold-tab href without losing the primary location", () => {
    const url = new URL(
      resourceHref("https://renart.local/pipelines/p?editor=adhoc&result=query", "project", detail),
    );
    expect(url.pathname).toBe("/pipelines/p");
    expect(url.searchParams.get("editor")).toBe("adhoc");
    expect(parseDetail(JSON.parse(url.searchParams.get("detail")!))).toEqual(detail);
    expect(() => resourceHref("javascript:alert(1)", "p", detail)).toThrow();
  });
  it("accepts each registered handler and never addresses blocks by index", () => {
    for (const target of [
      { kind: "asset-section", asset_id: "a", section: "dependencies" },
      {
        kind: "asset-section",
        asset_id: "a",
        section: "checks",
        column: "Total",
        check_name: "not_null",
      },
      { kind: "connection", connection: "pg", field: "host" },
      { kind: "notebook-cell", notebook_id: "n", cell_id: "stable_cell" },
      { kind: "presentation", presentation_id: "p", block_id: "stable_plot" },
      { kind: "presentation", presentation_id: "p" },
    ])
      expect(parseDetail({ ...detail, target }).target).toEqual(target);
    expect(() =>
      parseDetail({ ...detail, target: { kind: "notebook-cell", notebook_id: "n", cell_id: 0 } }),
    ).toThrow();
    expect(() =>
      parseDetail({
        ...detail,
        target: { kind: "presentation", presentation_id: "p", block_id: 2 },
      }),
    ).toThrow();
  });
  it("validates snapshot guards rather than accepting arbitrary source positions", () => {
    const source = {
      kind: "asset-section",
      asset_id: "a",
      section: "source",
      source_fingerprint: "fnv1a64:a430d84680aabd0b",
      line: 1,
      end_line: 2,
    };
    expect(parseDetail({ ...detail, target: source }).target).toEqual(source);
    for (const change of [
      { line: 0 },
      { end_line: -1 },
      { source_fingerprint: "any" },
      { section: "checks" },
    ])
      expect(() => parseDetail({ ...detail, target: { ...source, ...change } })).toThrow();
  });
  it("round trips durable data addresses and rejects mixed identities", () => {
    const address = {
      source_kind: "warehouse",
      connection: "dev",
      connection_type: "postgres",
      database: "db",
      schema: "Mixed.Schema",
      name: "Order.Items",
    };
    const data = {
      ...detail,
      target: { kind: "data-object", address, section: "schema", column: "Total ä" },
    };
    expect(parseDetail(data)).toEqual(data);
    expect(() =>
      parseDetail({
        ...data,
        target: { ...data.target, address: { ...address, path: "../secret" } },
      }),
    ).toThrow();
    for (const path of [
      "../file.csv",
      "/file.csv",
      ".git/file.csv",
      "data/../file.csv",
      "node_modules/file.csv",
    ]) {
      expect(() =>
        parseDetail({
          ...data,
          target: {
            kind: "data-object",
            section: "schema",
            address: { source_kind: "local_files", path },
          },
        }),
      ).toThrow();
    }
    expect(
      parseDetail({
        ...data,
        target: {
          kind: "data-object",
          section: "rows",
          address: { source_kind: "local_files", path: "data/ä.csv" },
        },
      }).target.kind,
    ).toBe("data-object");
  });
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
