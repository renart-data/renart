import { describe, expect, it } from "vitest";
import { deploymentDiffAnnotations, sourceAnchorFingerprint } from "./deployment-diff-annotations";
import type { DeploymentReviewRow } from "./deployment-review";

const sql = "SELECT SUM(amount) AS total FROM orders";
const range = { line: 1, column: 8, end_line: 1, end_column: 28 };
function row(): DeploymentReviewRow {
  const source = {
    fingerprint: sourceAnchorFingerprint(sql),
    query: { ...range, column: 1, end_column: sql.length + 1 },
    projections: [range],
  };
  return {
    key: "revenue",
    change: "unchanged",
    findings: [],
    semantic: {
      name: "revenue",
      change: "modified",
      source_change: "unchanged",
      origin: "propagated",
      severity: "warning",
      complete: true,
      before_source: source,
      after_source: source,
      columns: [
        {
          index: 0,
          before: { name: "total", type: "HUGEINT" },
          after: { name: "total", type: "DOUBLE" },
          type_changed: true,
          name_changed: false,
          nullability_changed: false,
        },
      ],
    },
  };
}

describe("deployment inline annotations", () => {
  it("uses a stable UTF-8 identity, including on HTTP and CRLF files", () => {
    expect(sourceAnchorFingerprint("hello")).toBe("fnv1a64:a430d84680aabd0b");
    expect(sourceAnchorFingerprint("a\r\n😀")).toBe(sourceAnchorFingerprint("a\n😀"));
  });
  it("highlights a propagated type change on both identical SQL sides", () => {
    const result = deploymentDiffAnnotations(row(), sql, sql);
    expect(result.original).toHaveLength(1);
    expect(result.modified).toHaveLength(1);
    expect(result.modified[0]).toMatchObject({
      range,
      label: "total: HUGEINT → DOUBLE",
      severity: "warning",
    });
  });
  it("does not apply stale source locations, even if the projection text still exists", () => {
    const result = deploymentDiffAnnotations(row(), sql, `${sql} WHERE amount > 0`);
    expect(result.original).toHaveLength(1);
    expect(result.modified).toHaveLength(0);
  });
  it("does not misattribute wildcards or invalid output ordinals to a projection", () => {
    const value = row();
    value.semantic!.after_source!.projections = [];
    expect(deploymentDiffAnnotations(value, sql, sql).modified).toHaveLength(0);
  });
  it("renders a source-backed diagnostic, leaving unlocated and stale findings in the list", () => {
    const value = row();
    value.semantic = undefined;
    value.findings = [
      {
        code: "type",
        severity: "error",
        message: "Wrong type",
        ...range,
        source_fingerprint: sourceAnchorFingerprint(sql),
      },
      { code: "runtime", severity: "warning", message: "Runtime-only asset" },
      { code: "stale", severity: "warning", message: "Stale", ...range, source_fingerprint: "old" },
    ];
    expect(deploymentDiffAnnotations(value, sql, sql).modified).toMatchObject([
      { label: "Wrong type", severity: "error", range },
    ]);
  });
  it("rejects invalid and zero-width ranges instead of clamping to unrelated text", () => {
    const value = row();
    value.semantic!.after_source!.projections = [{ ...range, column: 400 }];
    expect(deploymentDiffAnnotations(value, sql, sql).modified).toHaveLength(0);
    value.semantic!.after_source!.projections = [{ ...range, end_column: 8 }];
    expect(deploymentDiffAnnotations(value, sql, sql).modified).toHaveLength(0);
  });
  it("uses a query-level warning for a behavior change, not a guessed clause", () => {
    const value = row();
    value.semantic!.columns = [];
    value.semantic!.source_change = "changed";
    expect(deploymentDiffAnnotations(value, sql, sql).modified[0]?.label).toBe(
      "Query behavior changed",
    );
  });
});
