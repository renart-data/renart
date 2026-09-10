import { parseDocument, stringify } from "yaml";
import type { SQLUnitTest } from "./generated/api-types";

export function stringifyUnitTestYAML(value: unknown): string {
  return stringify(value, { indent: 2, lineWidth: 0, aliasDuplicateObjects: false });
}

export function parseUnitTestYAML(source: string): SQLUnitTest {
  if (new TextEncoder().encode(source).length > 256 * 1024)
    throw new Error("The test fixture must fit within 256 KiB.");
  const document = parseDocument(source, { version: "1.2", intAsBigInt: true, uniqueKeys: true });
  const problem = document.errors[0] ?? document.warnings[0];
  if (problem) throw new Error(problem.message);
  const value: unknown = document.toJS({ maxAliasCount: 100 });
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("Write one YAML mapping containing the test name, inputs and expected result.");
  // The API carries JSON values. Never silently turn NaN into null or round a
  // large YAML integer while crossing that boundary; cycles also fail here.
  const json = JSON.stringify(value, (_key, item: unknown) => {
    if (typeof item === "bigint") {
      const number = Number(item);
      if (!Number.isSafeInteger(number))
        throw new Error("Fixture integers must be safely representable as JSON numbers.");
      return number;
    }
    if (typeof item === "number" && !Number.isFinite(item))
      throw new Error("Fixture numbers must be finite; NaN and Infinity are not supported.");
    return item;
  });
  return JSON.parse(json) as SQLUnitTest;
}
