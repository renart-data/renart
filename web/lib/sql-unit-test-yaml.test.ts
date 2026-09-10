import { describe, expect, it } from "vitest";
import { parseUnitTestYAML, stringifyUnitTestYAML } from "./sql-unit-test-yaml";

describe("SQL unit test YAML", () => {
  it("reads natural YAML fixtures, comments and multiline descriptions", () => {
    const parsed = parseUnitTestYAML(`name: Two orders
description: |
  Mocked upstream rows.
  No production inputs.
inputs:
  - asset: raw.orders
    rows:
      - amount: 10 # a number, not a string
      - amount: 20
execution_time: 2026-09-08
expected:
  rows:
    - total: 30
  match: exact
`);
    expect(parsed).toMatchObject({
      name: "Two orders",
      execution_time: "2026-09-08",
      inputs: [{ asset: "raw.orders", rows: [{ amount: 10 }, { amount: 20 }] }],
      expected: { rows: [{ total: 30 }], match: "exact" },
    });
    expect(parseUnitTestYAML(stringifyUnitTestYAML(parsed))).toEqual(parsed);
  });
  it("rejects ambiguous or non-JSON-safe documents without silently coercing values", () => {
    for (const text of [
      "name: a\nname: b",
      "name: a\n---\nname: b",
      "[]",
      "name: .nan",
      "name: .inf",
      "name: !custom value",
      "name: &self [*self]",
      "name: large\nvariables: {value: 9007199254740993}",
    ])
      expect(() => parseUnitTestYAML(text), text).toThrow();
  });
  it("keeps numbers, booleans and quoted text distinct", () => {
    expect(
      parseUnitTestYAML(
        'name: Types\nvariables: {number: 12, text: "12", enabled: true, missing: null}',
      ),
    ).toMatchObject({ variables: { number: 12, text: "12", enabled: true, missing: null } });
  });
});
