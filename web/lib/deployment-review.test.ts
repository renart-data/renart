import { describe, expect, it } from "vitest";
import type { PipelinePlan, PipelinePlanSemanticAssetImpact } from "./generated/api-types";
import type { DeployStatus } from "./api-deploy";
import {
  buildDeploymentReview,
  deploymentAssetPath,
  deploymentRowSummary,
  newColumnSummary,
} from "./deployment-review";

function fixture() {
  const plan = {
    source: { pipeline_path: "example/pipeline.yml" },
    assets: [
      {
        id: "a",
        name: "analytics.revenue",
        workspace_asset_id: btoa("example/assets/revenue.sql"),
      },
    ],
    readiness: {
      blockers: [],
      warnings: [],
      code_checks: { assets: [], presentations: [] },
    },
  } as unknown as PipelinePlan;
  const status = {
    has_snapshot: true,
    changed_files: ["assets/revenue.sql"],
    added_files: [],
    removed_files: [],
  } as unknown as DeployStatus;
  return { plan, status };
}
const impact: PipelinePlanSemanticAssetImpact = {
  name: "analytics.revenue",
  change: "modified",
  source_change: "unchanged",
  origin: "upstream",
  severity: "warning",
  complete: true,
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
};

describe("deployment review", () => {
  it("categorizes added outputs separately from general query and contract changes", () => {
    const added = {
      ...impact,
      source_change: "changed",
      columns: [{ ...impact.columns[0], before: undefined }],
    };
    const row = { key: "revenue", change: "changed" as const, findings: [], semantic: added };
    expect(newColumnSummary(added)).toBe("New column");
    expect(deploymentRowSummary(row)).toBe("New column");
    expect(
      deploymentRowSummary({
        ...row,
        semantic: { ...added, columns: [...added.columns, { ...added.columns[0], index: 1 }] },
      }),
    ).toBe("2 new columns");
    expect(
      deploymentRowSummary({
        ...row,
        findings: [{ code: "schema", message: "Undeclared output", severity: "warning" }],
      }),
    ).toBe("New column · 1 warning");
    expect(
      deploymentRowSummary({
        ...row,
        findings: [{ code: "sql", message: "Invalid SQL", severity: "error" }],
      }),
    ).toBe("1 error");
    expect(
      newColumnSummary({ ...added, columns: [...added.columns, ...impact.columns] }),
    ).toBeUndefined();
    expect(newColumnSummary({ ...added, change: "added" })).toBeUndefined();
    expect(deploymentRowSummary({ ...row, semantic: { ...added, complete: false } })).toBe(
      "Analysis incomplete",
    );
    expect(newColumnSummary({ ...added, columns: [] })).toBeUndefined();
  });
  it("merges source, type impact and duplicate readiness findings into one row", () => {
    const { plan, status } = fixture();
    plan.semantic_impact = {
      status: "available",
      assets: [impact],
    } as PipelinePlan["semantic_impact"];
    plan.readiness.code_checks.assets = [
      {
        name: "analytics.revenue",
        type: "sql",
        status: "warning",
        findings: [
          { code: "schema", source: "sql", severity: "warning", message: "Output changed" },
        ],
      },
    ];
    plan.readiness.warnings = [
      {
        code: "code_check_warning",
        severity: "warning",
        asset_name: "analytics.revenue",
        message: "Output changed",
      },
    ];
    const review = buildDeploymentReview(plan, status);
    expect(review.rows).toHaveLength(1);
    expect(review.rows[0].findings).toHaveLength(1);
    expect(review.rows[0].semantic).toEqual(impact);
    expect(review.warnings).toEqual([]);
  });
  it("includes unchanged source with propagated contracts", () => {
    const { plan, status } = fixture();
    status.changed_files = [];
    plan.semantic_impact = {
      status: "available",
      assets: [impact],
    } as PipelinePlan["semantic_impact"];
    const row = buildDeploymentReview(plan, status).rows[0];
    expect(row.path).toBe("assets/revenue.sql");
    expect(row.change).toBe("unchanged");
    expect(deploymentRowSummary(row)).toBe("Upstream type impact");
  });

  it("deduplicates semantic summaries only when a report explains them", () => {
    const { plan, status } = fixture();
    const warning = { code: "semantic_impact_detected", severity: "warning", message: "2 changes" };
    plan.readiness.warnings = [warning];
    expect(buildDeploymentReview(plan, status).warnings).toEqual([warning]);
    plan.semantic_impact = {
      status: "available",
      complete: true,
      assets: [impact],
    } as PipelinePlan["semantic_impact"];
    expect(buildDeploymentReview(plan, status).warnings).toEqual([]);
    plan.readiness.blockers = [{ ...warning, severity: "error" }];
    expect(buildDeploymentReview(plan, status).blockers).toHaveLength(1);
  });
  it("keeps global blockers and unmapped assets visible without guessing a file", () => {
    const { plan, status } = fixture();
    plan.readiness.blockers = [{ code: "bad", severity: "error", message: "Source invalid" }];
    plan.semantic_impact = {
      status: "available",
      assets: [{ ...impact, name: "removed.asset", change: "removed" }],
    } as PipelinePlan["semantic_impact"];
    const review = buildDeploymentReview(plan, status);
    expect(review.blockers).toHaveLength(1);
    expect(review.rows.find((row) => row.name === "removed.asset")?.path).toBeUndefined();
  });
  it("preserves runtime-only notices as details and never hides an error", () => {
    const { plan, status } = fixture();
    const runtime = {
      code: "python_execution_runtime_only",
      severity: "warning",
      message: "3 Python assets resolve at runtime",
    };
    plan.readiness.warnings = [runtime];
    plan.readiness.blockers = [{ ...runtime, severity: "error" }];
    const review = buildDeploymentReview(plan, status);
    expect(review.notices).toEqual([runtime]);
    expect(review.blockers).toHaveLength(1);
  });
  it("retains all added/removed/config files and does not mark unknown analysis safe", () => {
    const { plan, status } = fixture();
    status.added_files = ["pipeline.yml"];
    status.removed_files = ["assets/old.sql"];
    const review = buildDeploymentReview(plan, status);
    expect(review.rows.map((row) => row.path)).toEqual([
      "pipeline.yml",
      "assets/revenue.sql",
      "assets/old.sql",
    ]);
    expect(deploymentRowSummary(review.rows[1])).toBe("Changed");
    expect(
      deploymentRowSummary({ ...review.rows[1], semantic: { ...impact, complete: false } }),
    ).toContain("incomplete");
  });
  it("maps only verified workspace paths inside the pipeline", () => {
    expect(deploymentAssetPath(btoa("example/assets/a.sql"), "example")).toBe("assets/a.sql");
    expect(deploymentAssetPath(btoa("example/assets/a.sql"), "example/pipeline.yml")).toBe(
      "assets/a.sql",
    );
    expect(deploymentAssetPath(btoa("assets/a.sql"), "pipeline.yml")).toBe("assets/a.sql");
    expect(deploymentAssetPath(btoa("examples/assets/a.sql"), "example")).toBeUndefined();
    expect(deploymentAssetPath(btoa("example/../secret"), "example")).toBeUndefined();
    expect(deploymentAssetPath("%%%", "example")).toBeUndefined();
  });
});
