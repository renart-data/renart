import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { DeploymentFileChanges } from "./pipeline-plan-sheet";
import type { PipelinePlan } from "@/lib/generated/api-types";
import type { DeployStatus } from "@/lib/api-deploy";

const plan = {
  status: "warning",
  source: { pipeline_path: "analytics", merkle_root: "source" },
  assets: [
    { id: "revenue", name: "revenue", workspace_asset_id: btoa("analytics/assets/revenue.sql") },
  ],
  readiness: {
    blockers: [],
    warnings: [],
    code_checks: {
      assets: [
        {
          name: "revenue",
          findings: [
            { severity: "warning", code: "schema", message: "The precise schema warning" },
          ],
        },
      ],
    },
  },
} as unknown as PipelinePlan;
const status = {
  has_snapshot: true,
  source_merkle: "source",
  changed_files: ["assets/revenue.sql"],
} as unknown as DeployStatus;

describe("production deployment review disclosure", () => {
  it("shows one file signal without flooding the initial review with detail", () => {
    const html = renderToStaticMarkup(
      <DeploymentFileChanges pipelineId="pipeline" plan={plan} status={status} />,
    );
    expect(html).toContain("Changes &amp; impact");
    expect(html).toContain("1 to review");
    expect(html).toContain("1 warning");
    expect(html).toContain('aria-expanded="false"');
    expect(html).not.toContain("The precise schema warning");
    expect(html).not.toContain("What-if");
  });
  it("does not equate missing semantic coverage with safety", () => {
    const html = renderToStaticMarkup(
      <DeploymentFileChanges pipelineId="pipeline" plan={plan} status={status} />,
    );
    expect(html).toContain("Semantic analysis is unavailable");
    expect(html).not.toContain("No semantic warnings");
  });
});
