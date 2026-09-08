import type { json } from "monaco-editor";
import type { SQLUnitTestContext, WebColumn } from "./generated/api-types";

type Schema = json.JSONSchema;

export function fixtureRowSchema(columns: WebColumn[]): Schema {
  const properties: Record<string, Schema> = {};
  for (const column of columns) {
    const sql = (column.type ?? "").toLowerCase();
    let type: string | undefined;
    if (/\[\]|^array\b/.test(sql)) type = "array";
    else if (/^(struct|map|record)\b/.test(sql)) type = "object";
    else if (/^(u?(tiny|small|medium|big|huge)?int\d*|integer|long|short|byte)\b/.test(sql))
      type = "integer";
    else if (/^(decimal|numeric|number|float\d*|double|real|bignumeric)\b/.test(sql))
      type = "number";
    else if (/^bool/.test(sql)) type = "boolean";
    else if (/^(var)?char|^text|^string|^date|^time|^uuid/.test(sql)) type = "string";
    properties[column.name] = {
      ...(type ? { type: column.nullable === false ? type : [type, "null"] } : {}),
      description: `${column.type || "Unknown type"}${column.description ? ` — ${column.description}` : ""}`,
    };
  }
  return { type: "object", properties, additionalProperties: columns.length === 0 };
}

function expectation(columns: WebColumn[]): Schema {
  return {
    type: "object",
    additionalProperties: false,
    properties: {
      rows: {
        type: ["array", "null"],
        maxItems: 500,
        items: fixtureRowSchema(columns),
        description: "Expected output rows. Use null for a count-only assertion.",
      },
      count: { type: "integer", minimum: 0, maximum: 5000 },
      match: { enum: ["subset", "exact"], description: "Exact also rejects extra rows." },
      order: {
        enum: ["any", "strict"],
        description: "Use strict only with a deterministic SQL ORDER BY.",
      },
    },
    anyOf: [{ required: ["rows"] }, { required: ["count"] }],
  };
}

export function sqlUnitTestSchema(context: SQLUnitTestContext): Schema {
  const expected = expectation(context.output ?? []);
  expected.properties!.ctes = {
    type: "object",
    additionalProperties: expectation([]),
    description: "Assertions on named CTEs. CTE row types are not inferred here.",
  };
  expected.anyOf!.push({ required: ["ctes"] });
  const input = (asset?: string, columns: WebColumn[] = []): Schema => ({
    type: "object",
    additionalProperties: false,
    required: ["asset", "rows"],
    properties: {
      asset: asset ? { enum: [asset] } : { type: "string", minLength: 1 },
      rows: { type: "array", maxItems: 500, items: fixtureRowSchema(columns) },
    },
  });
  return {
    type: "object",
    additionalProperties: false,
    required: ["name", "expected"],
    properties: {
      name: { type: "string", minLength: 1, maxLength: 128 },
      description: { type: "string" },
      inputs: {
        type: "array",
        items: context.inputs.length
          ? { oneOf: context.inputs.map((entry) => input(entry.asset, entry.columns)) }
          : input(),
      },
      fixtures: { type: "array", uniqueItems: true, items: { enum: context.fixtures } },
      variables: { type: "object" },
      execution_time: {
        type: "string",
        description: "Freeze supported SQL clocks and template dates (ISO date or timestamp).",
      },
      expected,
    },
  };
}
