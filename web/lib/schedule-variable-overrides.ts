import type { PipelineConfigVariable } from "@/lib/generated/api-types";

export type ScheduleVariableMode = "default" | "value" | "environment";

export type ScheduleVariableRow = { mode: ScheduleVariableMode; text: string };

export type ScheduleVariableRows = Record<string, ScheduleVariableRow>;

const environmentVariableName = /^[A-Za-z_][A-Za-z0-9_]*$/;

// scheduleVariableInput turns the per-variable rows into the literal overrides
// and env:NAME references the schedule API stores. Rows left on the pipeline
// default are omitted, so an all-default form clears every override.
export function scheduleVariableInput(
  variables: PipelineConfigVariable[],
  rows: ScheduleVariableRows,
): { vars: Record<string, unknown>; secret_refs: Record<string, string> } {
  const vars: Record<string, unknown> = {};
  const secretRefs: Record<string, string> = {};
  for (const variable of variables) {
    const row = rows[variable.name];
    if (!row || row.mode === "default") continue;
    const text = row.text.trim();
    if (row.mode === "environment") {
      const name = text.replace(/^env:/, "");
      if (!environmentVariableName.test(name)) {
        throw new Error(`${variable.name}: enter an environment variable name such as API_TOKEN.`);
      }
      secretRefs[variable.name] = `env:${name}`;
      continue;
    }
    vars[variable.name] = parseVariableValue(variable, row.text);
  }
  return { vars, secret_refs: secretRefs };
}

function parseVariableValue(variable: PipelineConfigVariable, raw: string): unknown {
  const text = raw.trim();
  switch (variable.type) {
    case "integer":
    case "number": {
      const value = Number(text);
      if (
        !text ||
        Number.isNaN(value) ||
        (variable.type === "integer" && !Number.isInteger(value))
      ) {
        throw new Error(
          `${variable.name}: enter ${variable.type === "integer" ? "a whole number" : "a number"}.`,
        );
      }
      return value;
    }
    case "boolean":
      if (text !== "true" && text !== "false") {
        throw new Error(`${variable.name}: enter true or false.`);
      }
      return text === "true";
    case "array":
    case "object":
      try {
        const value: unknown = JSON.parse(text);
        if (variable.type === "array" ? !Array.isArray(value) : !isPlainObject(value)) {
          throw new Error();
        }
        return value;
      } catch {
        throw new Error(`${variable.name}: enter a JSON ${variable.type}.`);
      }
    default:
      return raw;
  }
}

function isPlainObject(value: unknown) {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

export function variableDefaultLabel(variable: PipelineConfigVariable) {
  if (variable.default_value === undefined || variable.default_value === null) return "no default";
  if (variable.default_value === "") return "empty";
  return typeof variable.default_value === "string"
    ? variable.default_value
    : JSON.stringify(variable.default_value);
}
