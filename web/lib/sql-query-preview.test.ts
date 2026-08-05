import { describe, expect, it } from "vitest";

import { withSQLPreviewLimit } from "@/lib/sql-query-preview";

describe("withSQLPreviewLimit", () => {
  it("adds the effective preview limit after a statement", () => {
    expect(withSQLPreviewLimit("select * from events;", 500)).toBe(
      "select * from events\nLIMIT 500",
    );
  });

  it("clamps a larger trailing limit without creating invalid duplicate limits", () => {
    expect(withSQLPreviewLimit("select * from events limit 1000", 500)).toBe(
      "select * from events LIMIT 500",
    );
  });

  it("preserves a smaller explicit limit", () => {
    expect(withSQLPreviewLimit("select * from events limit 25", 500)).toBe(
      "select * from events limit 25",
    );
  });
});
