export type ChartAxisKind = "numeric" | "temporal" | "categorical";

export function chartAxisKind(physicalType: string | undefined, values: unknown[]): ChartAxisKind {
  const type = (physicalType ?? "").toLowerCase();
  if (/^(date|timestamp|datetime)/.test(type)) return "temporal";
  if (
    /^(u?(tiny|small|big|huge)?int|int[248]|integer|decimal|numeric|number|real|float|double)/.test(
      type,
    )
  )
    return "numeric";
  // Do not coerce identifiers such as "00123" into quantities.
  if (
    !type &&
    values.some((value) => typeof value === "number") &&
    values.every((value) => value == null || (typeof value === "number" && Number.isFinite(value)))
  )
    return "numeric";
  return "categorical";
}

export function chartAxisValue(value: unknown, kind: ChartAxisKind): unknown {
  if (kind !== "temporal" || value == null) return value;
  if (typeof value === "number") return Number.isFinite(value) ? value : null;
  const text = String(value);
  const timestamp = Date.parse(
    /(?:Z|[+-]\d{2}:?\d{2})$/.test(text)
      ? text
      : `${text.replace(" ", "T")}${text.length > 10 ? "Z" : ""}`,
  );
  return Number.isFinite(timestamp) ? timestamp : null;
}

export function formatChartTick(value: unknown, kind: ChartAxisKind): string {
  if (value == null) return "";
  if (kind === "numeric") {
    const numeric = Number(value);
    if (Number.isFinite(numeric))
      return new Intl.NumberFormat(undefined, {
        notation: Math.abs(numeric) >= 10_000 ? "compact" : "standard",
        ...(numeric !== 0 && Math.abs(numeric) < 0.01
          ? { maximumSignificantDigits: 3 }
          : { maximumFractionDigits: 2 }),
      }).format(numeric);
  }
  if (kind === "temporal") {
    const numeric = chartAxisValue(value, kind);
    if (typeof numeric === "number")
      return new Intl.DateTimeFormat(undefined, {
        month: "short",
        day: "numeric",
        timeZone: "UTC",
        ...(numeric % 86_400_000 !== 0
          ? { hour: "2-digit", minute: "2-digit", hourCycle: "h23" as const }
          : {}),
      }).format(numeric);
  }
  const text = String(value);
  return text.length > 22 ? `${text.slice(0, 21)}…` : text;
}

export function formatChartValue(value: unknown, kind: ChartAxisKind): string {
  if (kind === "temporal") {
    const timestamp = chartAxisValue(value, kind);
    if (typeof timestamp === "number")
      return new Date(timestamp).toISOString().replace("T", " ").replace(/Z$/, " UTC");
  }
  return String(value ?? "");
}
