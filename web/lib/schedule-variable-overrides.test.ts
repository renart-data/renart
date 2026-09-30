import { describe, expect, it } from "vitest";

import { scheduleVariableInput } from "./schedule-variable-overrides";

const variables = [
  { name: "region", type: "string", default_value: "EU" },
  { name: "lookback_days", type: "integer", default_value: 3 },
  { name: "dry_run", type: "boolean", default_value: false },
  { name: "countries", type: "array", default_value: [] },
  { name: "api_token", type: "string", default_value: "" },
];

describe("schedule variable overrides", () => {
  it("stores typed values and environment references, and omits defaults", () => {
    expect(
      scheduleVariableInput(variables, {
        region: { mode: "value", text: "US" },
        lookback_days: { mode: "value", text: "7" },
        dry_run: { mode: "value", text: "true" },
        countries: { mode: "value", text: '["DE", "FR"]' },
        api_token: { mode: "environment", text: "API_TOKEN" },
      }),
    ).toEqual({
      vars: { region: "US", lookback_days: 7, dry_run: true, countries: ["DE", "FR"] },
      secret_refs: { api_token: "env:API_TOKEN" },
    });
    expect(scheduleVariableInput(variables, { region: { mode: "default", text: "US" } })).toEqual({
      vars: {},
      secret_refs: {},
    });
  });

  it("names the variable whose value does not match its type", () => {
    expect(() =>
      scheduleVariableInput(variables, { lookback_days: { mode: "value", text: "1.5" } }),
    ).toThrow("lookback_days: enter a whole number.");
    expect(() =>
      scheduleVariableInput(variables, { countries: { mode: "value", text: '{"a":1}' } }),
    ).toThrow("countries: enter a JSON array.");
    expect(() =>
      scheduleVariableInput(variables, { api_token: { mode: "environment", text: "api-token" } }),
    ).toThrow("api_token: enter an environment variable name");
  });
});
