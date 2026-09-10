import { describe, expect, it } from "vitest";
import type {
  DataBrowserChildrenResponse,
  DataBrowserConnection,
  DataBrowserNode,
} from "./generated/api-types";
import {
  nodeSearchCompletion,
  planDataBrowserSearch,
  searchRequestKey,
  type BrowserSearchRequest,
} from "./data-browser-search";

const connection = (name: string, source_kind = "warehouse") =>
  ({
    id: name,
    name,
    source_kind,
    type: source_kind === "storage" ? "s3" : "duckdb",
  }) as DataBrowserConnection;
const duck = connection("duckdb-default");
const lake = connection("my-s3-connection", "storage");
const namespace = (label: string) =>
  ({ id: label, label, node_type: "namespace", has_children: true }) as DataBrowserNode;
const table = (label: string) =>
  ({ id: label, label, node_type: "object", has_children: false }) as DataBrowserNode;
const cache = new Map<string, DataBrowserChildrenResponse>();
const put = (request: BrowserSearchRequest, nodes: DataBrowserNode[], truncated = false) =>
  cache.set(searchRequestKey(request), { nodes, truncated } as DataBrowserChildrenResponse);
const plan = (query: string) => planDataBrowserSearch(query, [duck, lake], undefined, cache);

describe("lazy Data Browser path search", () => {
  it("matches leaf wildcards locally only for complete listings", () => {
    cache.clear();
    put({ connectionId: lake.id, prefix: "table/" }, [
      table("part-01.parquet"),
      table("part-02.csv"),
      table("Part-03.parquet"),
    ]);
    const result = plan("my-s3-connection./table/part-??.parquet");
    expect(result.request).toBeUndefined();
    expect(result.nodes.map((node) => node.label)).toEqual(["part-01.parquet"]);
    put({ connectionId: lake.id, prefix: "table/" }, [], true);
    expect(plan("my-s3-connection./table/part*.parquet").request).toEqual({
      connectionId: lake.id,
      prefix: "table/",
      pattern: "table/part*.parquet",
    });
  });
  it("keeps wildcard paths distinct from literal cached parent listings", () => {
    cache.clear();
    const request = {
      connectionId: lake.id,
      prefix: "table/",
      pattern: "table/day=*/part*.parquet",
    };
    expect(plan("my-s3-connection./table/day=*/part*.parquet").request).toEqual(request);
    put(request, [table("day=2026/part-01.parquet")]);
    expect(plan("my-s3-connection./table/day=*/part*.parquet").nodes).toHaveLength(1);
    expect(plan("my-s3-connection./table/").request).toEqual({
      connectionId: lake.id,
      prefix: "table/",
    });
    expect(plan("my-s3-connection./table/**/x").error).toContain("recursive **");
    expect(plan("my-s3-connection./../*").error).toContain("parent traversal");
  });
  it("uses the same canonical paths for clicked items and Tab completion", () => {
    expect(nodeSearchCompletion('"warehouse.prod".', namespace('sales."eu'), ".").value).toBe(
      '"warehouse.prod"."sales.""eu".',
    );
    expect(nodeSearchCompletion("duckdb-default.main.", table("order.items"), ".").value).toBe(
      'duckdb-default.main."order.items"',
    );
    expect(nodeSearchCompletion("lake./day=2026/", namespace("with spaces"), "/").value).toBe(
      "lake./day=2026/with spaces/",
    );
    expect(nodeSearchCompletion('"Project files"./data/', table("orders.csv"), "/").value).toBe(
      '"Project files"./data/orders.csv',
    );
  });
  it("traverses catalog and schema lazily without mixing identical names", () => {
    cache.clear();
    const catA = { ...namespace("lake"), id: "catalog:lake", namespace_kind: "catalog" };
    const catB = { ...namespace("warehouse"), id: "catalog:warehouse", namespace_kind: "catalog" };
    put({ connectionId: duck.id }, [catA, catB]);
    expect(plan("duckdb-default.lak").completions[0].value).toBe("duckdb-default.lake.");
    expect(plan("duckdb-default.lake.sales.or").request).toEqual({
      connectionId: duck.id,
      parentId: catA.id,
    });
    put({ connectionId: duck.id, parentId: catA.id }, [
      { ...namespace("sales"), id: "lake:sales" },
    ]);
    expect(plan("duckdb-default.lake.sales.or").request).toEqual({
      connectionId: duck.id,
      parentId: "lake:sales",
    });
    put({ connectionId: duck.id, parentId: "lake:sales" }, [table("orders")]);
    expect(plan("duckdb-default.lake.sales.or").completions[0].value).toBe(
      "duckdb-default.lake.sales.orders",
    );
    expect(plan("duckdb-default.warehouse.sales.or").request).toEqual({
      connectionId: duck.id,
      parentId: catB.id,
    });
  });
  it("suggests canonical connections without discovering any children", () => {
    const result = plan("duckdb-def");
    expect(result.completions[0].value).toBe("duckdb-default.");
    expect(result.request).toBeUndefined();
    expect(plan("duckb-def").completions[0].value).toBe("duckdb-default.");
  });
  it("only asks for the selected connection, not every matching connection", () => {
    cache.clear();
    expect(plan("duckdb-default.a").request).toEqual({ connectionId: duck.id });
    expect(plan("duckdb-default.an").request).toEqual({ connectionId: duck.id });
  });
  it("resolves a pasted warehouse path one level at a time", () => {
    cache.clear();
    put({ connectionId: duck.id }, [namespace("main"), namespace("unrelated")]);
    expect(plan("duckdb-default.main.or").request).toEqual({
      connectionId: duck.id,
      parentId: "main",
    });
    expect(plan("duckdb-default.main.or").nodes).toEqual([]);
    put({ connectionId: duck.id, parentId: "main" }, [table("orders"), table("users")]);
    const result = plan("duckdb-default.main.or");
    expect(result.nodes.map((node) => node.label)).toEqual(["orders"]);
    expect(result.completions[0].value).toBe("duckdb-default.main.orders");
    expect(result.request).toBeUndefined();
    expect(plan("duckdb-default.main.orders.").error).toContain("not a namespace");
  });
  it("completes schemas with dots but never invents a namespace", () => {
    cache.clear();
    put({ connectionId: duck.id }, [namespace("analytics"), namespace("archive")]);
    expect(plan("duckdb-default.a").nodes).toHaveLength(2);
    expect(plan("duckdb-default.anal").completions[0].value).toBe("duckdb-default.analytics.");
    expect(plan("duckdb-default.unknown.x").request).toBeUndefined();
    expect(plan("duckdb-default.unknown.x").error).toContain("not found");
  });
  it("does not interpret dots inside quoted names as path delimiters", () => {
    cache.clear();
    const dotted = connection("warehouse.prod");
    put({ connectionId: dotted.id }, [namespace("sales.eu")]);
    put({ connectionId: dotted.id, parentId: "sales.eu" }, [table("order.items")]);
    const result = planDataBrowserSearch(
      '"warehouse.prod"."sales.eu".ord',
      [dotted],
      undefined,
      cache,
    );
    expect(result.completions[0].value).toBe('"warehouse.prod"."sales.eu"."order.items"');
  });
  it("lists a typed storage parent directly even if its ancestors were never listed", () => {
    cache.clear();
    expect(plan("my-s3-connection./some/deep/pa").request).toEqual({
      connectionId: lake.id,
      prefix: "some/deep/",
    });
    put({ connectionId: lake.id, prefix: "some/deep/" }, [namespace("path"), table("part.01.csv")]);
    expect(plan("my-s3-connection./some/deep/pa").completions[0].value).toBe(
      "my-s3-connection./some/deep/path/",
    );
    expect(plan("my-s3-connection./some/deep/part.").completions[0].value).toBe(
      "my-s3-connection./some/deep/part.01.csv",
    );
  });
  it("shows children of an exactly typed storage prefix even without its final slash", () => {
    cache.clear();
    put({ connectionId: lake.id, prefix: "some/" }, [namespace("path")]);
    expect(plan("my-s3-connection./some/path").request).toEqual({
      connectionId: lake.id,
      prefix: "some/path/",
    });
    put({ connectionId: lake.id, prefix: "some/path/" }, [table("orders.csv")]);
    const result = plan("my-s3-connection./some/path");
    expect(result.nodes[0].label).toBe("orders.csv");
    expect(result.completions[0].value).toBe("my-s3-connection./some/path/");
  });
  it("supports explicit prefixes outside a truncated ancestor listing", () => {
    cache.clear();
    put({ connectionId: lake.id, prefix: "" }, [namespace("first")], true);
    expect(plan("my-s3-connection./unlisted/deep/").request).toEqual({
      connectionId: lake.id,
      prefix: "unlisted/deep/",
    });
  });
  it("narrows capped S3 listings at the provider, then reuses complete subsets", () => {
    cache.clear();
    const parent = { connectionId: lake.id, prefix: "my_table/" };
    put(parent, [namespace("day=2024-01-01")], true);
    const query = "my-s3-connection./my_table/";
    expect(plan(query + "day=").request).toEqual({ ...parent, namePrefix: "day=" });
    put({ ...parent, namePrefix: "day=" }, [], true);
    expect(plan(query + "day=2026-09").request).toEqual({ ...parent, namePrefix: "day=2026-09" });
    put({ ...parent, namePrefix: "day=2026-09" }, [
      namespace("day=2026-09-01"),
      namespace("day=2026-09-20"),
    ]);
    const narrowed = plan(query + "day=2026-09-2");
    expect(narrowed.request).toBeUndefined();
    expect(narrowed.nodes.map((n) => n.label)).toEqual(["day=2026-09-20"]);
    expect(narrowed.truncated).toBe(false);
    expect(plan(query + "day=2026-0").request).toEqual({ ...parent, namePrefix: "day=2026-0" });
    expect(plan(query + "DAY=2026-09").request).toEqual({ ...parent, namePrefix: "DAY=2026-09" });
    expect(plan(query + "day=2026-09-20").request).toEqual({
      connectionId: lake.id,
      prefix: "my_table/day=2026-09-20/",
    });
    // Eviction of the parent must not cause a broad reload if a complete subset covers the input.
    cache.delete(searchRequestKey(parent));
    expect(plan(query + "day=2026-09-2").request).toBeUndefined();
  });
  it("does not refetch complete S3 listings, including the manually opened folder", () => {
    cache.clear();
    const nodes = [table("day=2026-09-01.csv"), table("day=2026-08-01.csv")];
    const base = { connection: lake, parts: ["my_table"], nodes, truncated: false };
    const local = planDataBrowserSearch("day=2026-09", [lake], base, cache);
    expect(local.request).toBeUndefined();
    expect(local.nodes.map((n) => n.label)).toEqual(["day=2026-09-01.csv"]);
    put({ connectionId: lake.id, prefix: "my_table/" }, nodes);
    expect(plan("my-s3-connection./my_table/day=2026-09").request).toBeUndefined();
    expect(plan("my-s3-connection./my_table/2026-09").nodes).toHaveLength(1);
  });
  it("keeps SFTP filtering local and does not reuse a subset from another path", () => {
    cache.clear();
    const sftp = { ...lake, type: "sftp" };
    put({ connectionId: lake.id, prefix: "my_table/" }, [table("first.csv")], true);
    expect(
      planDataBrowserSearch("my-s3-connection./my_table/day=", [sftp], undefined, cache).request,
    ).toBeUndefined();
    put({ connectionId: lake.id, prefix: "elsewhere/", namePrefix: "day=" }, []);
    expect(plan("my-s3-connection./my_table/day=").request).toEqual({
      connectionId: lake.id,
      prefix: "my_table/",
      namePrefix: "day=",
    });
  });
  it("refuses unsafe storage paths without starting discovery", () => {
    for (const path of [
      "../secret/",
      "foo//bar/",
      "foo/**/",
      "s3://elsewhere/",
      "foo\\bar/",
      "foo\nbar/",
    ]) {
      const result = plan(`my-s3-connection./${path}`);
      expect(result.request).toBeUndefined();
      expect(result.error).toBeTruthy();
    }
  });
  it("preserves local filtering and can switch connections from inside a folder", () => {
    cache.clear();
    const base = { connection: duck, parts: ["main"], nodes: [table("orders"), table("users")] };
    expect(
      planDataBrowserSearch("ord", [duck, lake], base, cache).nodes.map((n) => n.label),
    ).toEqual(["orders"]);
    expect(planDataBrowserSearch("ord", [duck, lake], base, cache).completions[0].value).toBe(
      "orders",
    );
    expect(planDataBrowserSearch("orders", [duck, lake], base, cache).completions).toEqual([]);
    expect(
      planDataBrowserSearch("my-s3-connection./a/", [duck, lake], base, cache).request,
    ).toEqual({ connectionId: lake.id, prefix: "a/" });
  });
  it("reports ambiguous names rather than silently entering one", () => {
    cache.clear();
    put({ connectionId: duck.id }, [namespace("Sales"), namespace("SALES")]);
    expect(plan("duckdb-default.sales.a").error).toContain("ambiguous");
    expect(
      planDataBrowserSearch("Lake.a", [connection("lake"), connection("LAKE")], undefined, cache)
        .error,
    ).toContain("ambiguous");
  });
});
