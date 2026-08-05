import { describe, expect, it } from "vitest";

import type { ColumnSchemaMergeRow, ColumnSchemaSourceSnapshot } from "@/lib/generated/api-types";
import { CURRENT_SCHEMA_CHOICE, defaultSchemaResolutionChoice } from "@/lib/schema-sync-resolution";

const row = (kind: string): ColumnSchemaMergeRow => ({
  column: "double_range",
  current_present: true,
  current_type: "INTEGER",
  proposed_present: true,
  proposed_type: "BIGINT",
  kind,
  detail: "",
  conflict: true,
});

const source = (
  id: string,
  type: string,
  options: Partial<ColumnSchemaSourceSnapshot> = {},
): ColumnSchemaSourceSnapshot => ({
  source: {
    id,
    label: id === "definition" ? "SQL query" : "Current table",
    description: "",
    category: id === "definition" ? "definition" : "observed",
  },
  columns: [{ name: "double_range", type }],
  ...options,
});

describe("defaultSchemaResolutionChoice", () => {
  it("prefers the edited SQL definition over stale saved metadata", () => {
    expect(
      defaultSchemaResolutionChoice(row("type_conflict"), [source("definition", "BIGINT")]),
    ).toBe("source:definition");
  });

  it("prefers complete fresh output when selected sources disagree", () => {
    expect(
      defaultSchemaResolutionChoice(row("source_conflict"), [
        source("definition", "INTEGER"),
        source("materialized", "BIGINT", {
          fresh: true,
          completeness: "complete",
          classification: "comparable",
        }),
      ]),
    ).toBe("source:materialized");
  });

  it("keeps saved metadata when conflicting output is not known fresh", () => {
    expect(
      defaultSchemaResolutionChoice(row("source_conflict"), [
        source("definition", "INTEGER"),
        source("materialized", "BIGINT", { fresh: false }),
      ]),
    ).toBe(CURRENT_SCHEMA_CHOICE);
  });
});
