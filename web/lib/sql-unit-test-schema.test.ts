import { describe, expect, it } from "vitest";
import { fixtureRowSchema, sqlUnitTestSchema } from "./sql-unit-test-schema";

describe("SQL unit test fixture typing", () => {
  it("uses column names, SQL scalar types and nullability", () => {
    const schema = fixtureRowSchema([
      { name: "id", type: "INTEGER", nullable: false },
      { name: "amount", type: "DECIMAL(10,2)" },
      { name: "label", type: "VARCHAR" },
    ]);
    expect(schema.additionalProperties).toBe(false);
    expect(schema.properties?.id).toMatchObject({ type: "integer" });
    expect(schema.properties?.amount).toMatchObject({ type: ["number", "null"] });
    expect(schema.properties?.label).toMatchObject({ type: ["string", "null"] });
    expect(fixtureRowSchema([]).additionalProperties).toBe(true);
  });
  it("binds mock rows to the selected upstream and expected rows to output", () => {
    const schema = sqlUnitTestSchema({
      status: "ok",
      tests: [],
      revision: "v1",
      fixtures: ["orders"],
      inputs: [{ asset: "raw.orders", columns: [{ name: "amount", type: "DOUBLE" }] }],
      output: [{ name: "total", type: "DOUBLE" }],
    });
    expect(JSON.stringify(schema)).toContain("raw.orders");
    expect(schema.properties?.expected).toMatchObject({
      properties: {
        rows: {
          items: {
            properties: { total: { type: ["number", "null"] } },
            additionalProperties: false,
          },
        },
      },
    });
  });
});
