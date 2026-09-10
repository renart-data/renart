import { describe, expect, it } from "vitest";
import { completionShadow, searchPathSegments } from "./data-browser-search-presentation";

describe("Data Browser search presentation", () => {
  it("hides only the appended separator, never part of an object name", () => {
    expect(
      completionShadow("duckdb-def", {
        label: "duckdb-default",
        value: "duckdb-default.",
        separator: ".",
      }),
    ).toBe("ault");
    expect(completionShadow("lake", { label: "lake", value: "lake./", separator: "./" })).toBe("");
    expect(
      completionShadow("lake./inc", {
        label: "incoming",
        value: "lake./incoming/",
        separator: "/",
      }),
    ).toBe("oming");
    expect(
      completionShadow("duckb-def", {
        label: "duckdb-default",
        value: "duckdb-default.",
        separator: ".",
      }),
    ).toBe(" → duckdb-default");
    expect(completionShadow("lake./file", { label: "file.", value: "lake./file." })).toBe(".");
    expect(
      completionShadow('db."sales', { label: "sales.eu", value: 'db."sales.eu".', separator: "." }),
    ).toBe('.eu"');
  });
  it("marks completed warehouse segments while preserving every typed character", () => {
    const value = '"warehouse.prod"."sales.eu".ord';
    const parts = searchPathSegments(value, { connectionEnd: 17, separator: "." });
    expect(parts.map((p) => p.text).join("")).toBe(value);
    expect(parts.filter((p) => p.completed).map((p) => p.text)).toEqual([
      '"warehouse.prod"',
      '"sales.eu"',
    ]);
    expect(parts.at(-1)).toMatchObject({ text: "ord", completed: false });
    expect(
      searchPathSegments('"a""b.c".q', { connectionEnd: 0, separator: "." })
        .filter((p) => p.completed)
        .map((p) => p.text),
    ).toEqual(['"a""b.c"']);
    expect(
      searchPathSegments('main."unfinished.name', { connectionEnd: 0, separator: "." })
        .filter((p) => p.completed)
        .map((p) => p.text),
    ).toEqual(["main"]);
  });
  it("treats storage dots as names and never decorates an unresolved connection", () => {
    const value = "lake./my.table/day=2026-09/part.csv";
    const parts = searchPathSegments(value, { connectionEnd: 5, separator: "/" });
    expect(parts.map((p) => p.text).join("")).toBe(value);
    expect(parts.filter((p) => p.completed).map((p) => p.text)).toEqual([
      "lake",
      "my.table",
      "day=2026-09",
    ]);
    expect(
      searchPathSegments("my.folder/file.csv", { connectionEnd: 0, separator: "/" })
        .filter((p) => p.completed)
        .map((p) => p.text),
    ).toEqual(["my.folder"]);
    expect(searchPathSegments("duckdb-def").filter((p) => p.completed)).toEqual([]);
  });
});
