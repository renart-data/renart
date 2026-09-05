import { describe, expect, it } from "vitest";
import { resourceDestination } from "./ui-navigation";
import { parseDetail } from "./resource-navigation";

const owners = {
  pipelines: [{ id: "p", assets: [{ id: "a" }, { id: "b" }] }],
  presentations: [{ id: "authored-report-name", workspace_id: "report", kind: "report" }],
};
const column = parseDetail({
  v: 1,
  environment: "dev",
  target: {
    kind: "asset-column",
    asset_id: "a",
    column: "total",
    field: "type",
  },
});

describe("minimal UI navigation", () => {
  it("opens the real asset page without changing independent results or editor state", () => {
    for (const result of ["inspect", "render", "materialize", "query", "typecheck"]) {
      const search = { result, editor: "adhoc", custom: "preserved" };
      const destination = resourceDestination(
        { pathname: "/pipelines/p/assets/b/canvas", search },
        "project",
        column,
        owners,
      );
      expect(destination.pathname).toBe("/pipelines/p/assets/a/canvas");
      expect(destination.search).toMatchObject(search);
      expect(destination.search.detail).toEqual(column);
    }
  });
  it("only source navigation reveals an otherwise hidden repository editor", () => {
    const source = parseDetail({
      ...column,
      target: { kind: "asset-section", asset_id: "a", section: "source" },
    });
    const destination = resourceDestination(
      { pathname: "/pipelines/p/assets/a/canvas", search: { editor: "adhoc", result: "render" } },
      "project",
      source,
      owners,
    );
    expect(destination.pathname).toBe("/pipelines/p/assets/a/split");
    expect(destination.search).toMatchObject({ editor: "asset", result: "render" });
  });
  it("maps each destination to its existing owner, never to a parallel detail page", () => {
    for (const [target, pathname] of [
      [{ kind: "connection", connection: "pg", field: "host" }, "/project/connections"],
      [{ kind: "notebook-cell", notebook_id: "n", cell_id: "stable" }, "/notebooks/n"],
      [{ kind: "presentation", presentation_id: "report", block_id: "plot" }, "/reports/report"],
      [
        {
          kind: "data-object",
          address: { source_kind: "local_files", path: "a.csv" },
          section: "schema",
        },
        "/data",
      ],
    ] as const) {
      const destination = resourceDestination(
        { pathname: "/run", search: { custom: "kept" } },
        "project",
        parseDetail({ ...column, target }),
        owners,
      );
      expect(destination.pathname).toBe(pathname);
      expect(destination.search.custom).toBe("kept");
    }
  });
  it("never guesses an owner for missing or duplicate identities", () => {
    expect(() =>
      resourceDestination({ pathname: "/run", search: {} }, "p", column, { pipelines: [] }),
    ).toThrow(/available/);
    expect(() =>
      resourceDestination({ pathname: "/run", search: {} }, "p", column, {
        pipelines: [...owners.pipelines, ...owners.pipelines],
      }),
    ).toThrow(/available/);
  });
});
