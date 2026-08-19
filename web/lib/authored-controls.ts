export const AUTHORED_CONTROL_TYPES = [
  "text",
  "number",
  "slider",
  "boolean",
  "select",
  "multi_select",
  "date",
  "date_range",
] as const;

export type AuthoredControlType = (typeof AUTHORED_CONTROL_TYPES)[number];

export type AuthoredControlOptions = {
  values?: unknown[];
  dataset?: string;
  value_field?: string;
  label_field?: string;
};

export type AuthoredControlDefinition = {
  id: string;
  label?: string;
  type: string;
  default: unknown;
  min?: number;
  max?: number;
  step?: number;
  options?: AuthoredControlOptions;
};

export type AuthoredControlOption = {
  value: unknown;
  label: string;
};

export const AUTHORED_CONTROL_TYPE_LABELS: Record<AuthoredControlType, string> = {
  text: "Text",
  number: "Number",
  slider: "Slider",
  boolean: "Switch",
  select: "Select",
  multi_select: "Multi-select",
  date: "Date",
  date_range: "Date range",
};

export function authoredControlType(type: string): AuthoredControlType {
  return AUTHORED_CONTROL_TYPES.includes(type as AuthoredControlType)
    ? (type as AuthoredControlType)
    : "text";
}

export function defaultAuthoredControlValue(
  type: string,
  today = new Date().toISOString().slice(0, 10),
): unknown {
  switch (authoredControlType(type)) {
    case "number":
      return 0;
    case "slider":
      return 50;
    case "boolean":
      return false;
    case "multi_select":
      return [];
    case "date":
      return today;
    case "date_range":
      return [today, today];
    default:
      return "";
  }
}

export function defaultAuthoredControlRange(
  type: string,
): Pick<AuthoredControlDefinition, "min" | "max" | "step"> {
  return authoredControlType(type) === "slider" ? { min: 0, max: 100, step: 1 } : {};
}

export function authoredSliderBounds(
  control: Pick<AuthoredControlDefinition, "min" | "max" | "step">,
) {
  const min = Number.isFinite(control.min) ? (control.min ?? 0) : 0;
  const authoredMax = Number.isFinite(control.max) ? (control.max ?? 100) : 100;
  const max = authoredMax > min ? authoredMax : min + 100;
  const authoredStep = Number.isFinite(control.step) ? (control.step ?? 1) : 1;
  const step = authoredStep > 0 ? authoredStep : 1;
  return { min, max, step };
}

export function normalizeAuthoredControlList(value: string): string[] {
  return value
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

export function authoredControlDefinitionsProblem(controls: AuthoredControlDefinition[]): string {
  const seen = new Set<string>();
  for (const control of controls) {
    if (!/^[a-z][a-z0-9_]*$/.test(control.id)) {
      return `“${control.id || "Untitled"}” needs a lowercase id using letters, numbers, and underscores.`;
    }
    if (seen.has(control.id)) {
      return `Control id “${control.id}” is used more than once.`;
    }
    seen.add(control.id);
  }
  return "";
}

export function authoredControlOptions(
  control: AuthoredControlDefinition,
  result?: { status: string; columns: string[]; rows: unknown[][] },
): AuthoredControlOption[] {
  if (control.options?.values !== undefined) {
    return control.options.values.map((value) => ({ value, label: String(value) }));
  }
  if (!result || result.status !== "ok" || !control.options?.value_field) return [];

  const valueIndex = result.columns.findIndex(
    (column) => column.toLowerCase() === control.options?.value_field?.toLowerCase(),
  );
  const labelIndex = control.options.label_field
    ? result.columns.findIndex(
        (column) => column.toLowerCase() === control.options?.label_field?.toLowerCase(),
      )
    : valueIndex;
  if (valueIndex < 0) return [];

  const seen = new Set<string>();
  return result.rows.flatMap((row) => {
    const value = row[valueIndex];
    const key = comparableAuthoredControlValue(value);
    if (seen.has(key)) return [];
    seen.add(key);
    return [{ value, label: String(row[labelIndex >= 0 ? labelIndex : valueIndex] ?? value) }];
  });
}

export function comparableAuthoredControlValue(value: unknown): string {
  return typeof value === "string" ? value : JSON.stringify(value);
}
