import type { DeploymentReviewRow } from "./deployment-review";
import type { PipelinePlanSemanticSourceRange } from "./generated/api-types";

export type DiffAnnotation = {
  range: PipelinePlanSemanticSourceRange;
  label: string;
  severity: "info" | "warning" | "error";
};

// Matches Go SourceAnchorFingerprint. This is only a stale-annotation guard,
// never a deployment/security digest. Works on local-network HTTP too.
export function sourceAnchorFingerprint(source: string): string {
  let hash = BigInt("0xcbf29ce484222325");
  const prime = BigInt("0x100000001b3");
  for (const byte of new TextEncoder().encode(source.replace(/\r\n/g, "\n"))) {
    hash = BigInt.asUintN(64, (hash ^ BigInt(byte)) * prime);
  }
  return `fnv1a64:${hash.toString(16).padStart(16, "0")}`;
}

function validRange(source: string, range: PipelinePlanSemanticSourceRange): boolean {
  const { line, column, end_line, end_column } = range;
  if (![line, column, end_line, end_column].every((n) => Number.isInteger(n) && n > 0))
    return false;
  const lines = source.split(/\r?\n/);
  return (
    line <= lines.length &&
    end_line <= lines.length &&
    column <= lines[line - 1].length + 1 &&
    end_column <= lines[end_line - 1].length + 1 &&
    (end_line > line || (end_line === line && end_column > column))
  );
}

export function deploymentDiffAnnotations(
  row: DeploymentReviewRow | undefined,
  before: string,
  after: string,
): { original: DiffAnnotation[]; modified: DiffAnnotation[] } {
  const result = { original: [] as DiffAnnotation[], modified: [] as DiffAnnotation[] };
  if (!row) return result;
  const fingerprints = {
    original: sourceAnchorFingerprint(before),
    modified: sourceAnchorFingerprint(after),
  };
  const semantic = row.semantic;
  if (semantic?.severity === "warning") {
    for (const [side, source, anchors] of [
      ["original", before, semantic.before_source],
      ["modified", after, semantic.after_source],
    ] as const) {
      if (!anchors || anchors.fingerprint !== fingerprints[side]) continue;
      for (const column of semantic.columns) {
        if (!(side === "original" ? column.before : column.after)) continue;
        const index = side === "original" ? column.before_index : column.after_index;
        const range = anchors.projections[index ?? column.index];
        if (!range || !validRange(source, range)) continue;
        const name = column.after?.name || column.before?.name || "Output";
        const changes: string[] = [];
        if (!column.before) {
          result[side].push({
            range,
            label: semantic.complete
              ? `New column: ${name}`
              : `${name}: output detected (analysis incomplete)`,
            severity: semantic.complete ? "info" : "warning",
          });
          continue;
        } else if (!column.after) changes.push("output removed");
        else {
          if (column.name_changed) changes.push(`${column.before.name} → ${column.after.name}`);
          if (column.type_changed)
            changes.push(`${column.before.type || "unknown"} → ${column.after.type || "unknown"}`);
          if (column.nullability_changed)
            changes.push(
              `${column.before.nullability || "unknown"} → ${column.after.nullability || "unknown"}`,
            );
          if (column.position_changed)
            changes.push(
              `position ${(column.before_index ?? column.index) + 1} → ${(column.after_index ?? column.index) + 1}`,
            );
        }
        result[side].push({ range, label: `${name}: ${changes.join(" · ")}`, severity: "warning" });
      }
      // Query-only changes already have native text diffs and a row summary.
      // A statement-wide warning would drown out precisely located findings.
    }
  }
  for (const finding of row.findings) {
    if (!finding.source_fingerprint || finding.source_fingerprint !== fingerprints.modified)
      continue;
    const range = {
      line: finding.line!,
      column: finding.column!,
      end_line: finding.end_line!,
      end_column: finding.end_column!,
    };
    if (!validRange(after, range) || !["warning", "error"].includes(finding.severity)) continue;
    result.modified.push({
      range,
      label: finding.message,
      severity: finding.severity === "error" ? "error" : "warning",
    });
  }
  // If several lenses share a line, an actual diagnostic takes priority over
  // an informational addition. Keep the addition visible in the row summary.
  const priority = { error: 0, warning: 1, info: 2 };
  for (const side of ["original", "modified"] as const)
    result[side].sort((a, b) => priority[a.severity] - priority[b.severity]);
  return result;
}
