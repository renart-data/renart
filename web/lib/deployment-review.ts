import type { DeployStatus } from "./api-deploy";
import type {
  PipelinePlan,
  PipelinePlanIssue,
  PipelinePlanSemanticAssetImpact,
  TypeCheckFinding,
} from "./generated/api-types";

export type DeploymentReviewRow = {
  key: string;
  path?: string;
  name?: string;
  change: "added" | "changed" | "removed" | "unchanged";
  findings: (PipelinePlanIssue & Partial<TypeCheckFinding>)[];
  semantic?: PipelinePlanSemanticAssetImpact;
};

// Workspace IDs are encoded workspace-relative paths. Never infer a path from
// an SQL relation name: removed assets and unavailable identities stay unbound.
export function deploymentAssetPath(id: string | undefined, pipelinePath: string) {
  if (!id) return undefined;
  try {
    const path = new TextDecoder("utf-8", { fatal: true }).decode(
      Uint8Array.from(atob(id.replace(/-/g, "+").replace(/_/g, "/")), (char) => char.charCodeAt(0)),
    );
    const root = pipelinePath
      .replace(/\\/g, "/")
      .replace(/^\.\//, "")
      .replace(/(^|\/)pipeline\.ya?ml$/, "")
      .replace(/\/$/, "");
    const relative =
      root === "." || root === ""
        ? path
        : path.startsWith(`${root}/`)
          ? path.slice(root.length + 1)
          : undefined;
    if (
      !relative ||
      relative.startsWith("/") ||
      relative.split("/").some((part) => !part || part === ".." || part === ".")
    )
      return undefined;
    return relative;
  } catch {
    return undefined;
  }
}

export function buildDeploymentReview(plan: PipelinePlan, status: DeployStatus | null) {
  const rows = new Map<string, DeploymentReviewRow>();
  for (const [change, paths] of [
    ["added", status?.added_files],
    ["changed", status?.changed_files],
    ["removed", status?.removed_files],
  ] as const) {
    for (const path of paths ?? []) rows.set(path, { key: path, path, change, findings: [] });
  }
  const assetRow = (name: string, id?: string) => {
    const asset = plan.assets.find((item) => item.name === name || (id && item.id === id));
    const path = deploymentAssetPath(asset?.workspace_asset_id, plan.source.pipeline_path);
    const key = path ?? `asset:${name || id}`;
    const row = rows.get(key) ?? {
      key,
      path,
      name: asset?.name || name || id,
      change: "unchanged" as const,
      findings: [],
    };
    row.name = asset?.name || name || id;
    rows.set(key, row);
    return row;
  };
  for (const asset of plan.readiness.code_checks.assets) {
    if (!asset.findings.length) continue;
    const row = assetRow(asset.name);
    row.findings.push(...asset.findings.map((finding) => ({ ...finding, asset_name: asset.name })));
  }
  for (const semantic of plan.semantic_impact?.assets ?? []) {
    assetRow(semantic.name).semantic = semantic;
  }
  const blockers: PipelinePlanIssue[] = [];
  const warnings: PipelinePlanIssue[] = [];
  const notices: PipelinePlanIssue[] = [];
  for (const [issues, target] of [
    [plan.readiness.blockers, blockers],
    [plan.readiness.warnings, warnings],
  ] as const) {
    for (const issue of issues) {
      // The report already explains these aggregate warnings via row signals
      // and its coverage notice. Without a report, keep the original warning.
      if (
        target === warnings &&
        plan.semantic_impact &&
        ((issue.code === "semantic_impact_detected" && plan.semantic_impact.assets.length > 0) ||
          (issue.code === "semantic_impact_incomplete" && !plan.semantic_impact.complete) ||
          (issue.code === "semantic_impact_unavailable" &&
            plan.semantic_impact.status !== "available"))
      )
        continue;
      if (target === warnings && issue.code === "python_execution_runtime_only") {
        notices.push(issue);
      } else if (issue.asset_name || issue.asset_id) {
        const row = assetRow(issue.asset_name ?? "", issue.asset_id);
        if (
          !row.findings.some(
            (finding) => finding.message === issue.message && finding.severity === issue.severity,
          )
        )
          row.findings.push(issue);
      } else {
        target.push(issue);
      }
    }
  }
  return { rows: [...rows.values()], blockers, warnings, notices };
}

export function deploymentRowTone(row: DeploymentReviewRow): "error" | "warning" | "neutral" {
  if (row.findings.some((finding) => finding.severity === "error")) return "error";
  if (
    row.findings.length ||
    row.semantic?.severity === "warning" ||
    row.semantic?.complete === false
  )
    return "warning";
  return "neutral";
}

export function deploymentRowSummary(row: DeploymentReviewRow) {
  const errors = row.findings.filter((finding) => finding.severity === "error").length;
  if (errors) return `${errors} ${errors === 1 ? "error" : "errors"}`;
  const added = newColumnSummary(row.semantic);
  if (row.findings.length)
    return `${added ? `${added} · ` : ""}${row.findings.length} ${row.findings.length === 1 ? "warning" : "warnings"}`;
  const impact = row.semantic;
  if (impact?.complete === false) return "Analysis incomplete";
  if (impact?.change === "removed") return "Asset removed";
  if (impact?.change === "added") return "Asset added";
  if (added) return added;
  if (impact?.columns.length)
    return impact.source_change === "unchanged"
      ? "Upstream type impact"
      : "Output contract changed";
  if (impact?.source_change === "formatting_only") return "Formatting only";
  if (impact?.source_change === "changed") return "Query changed";
  return row.change === "unchanged"
    ? "Needs review"
    : row.change[0].toUpperCase() + row.change.slice(1);
}

// A rename, removal, mixed contract change or new asset is not just a new column.
// Classification comes from backend output contracts, never SQL text heuristics.
export function newColumnSummary(impact?: PipelinePlanSemanticAssetImpact) {
  if (
    impact?.change !== "modified" ||
    !impact.complete ||
    !impact.columns.length ||
    !impact.columns.every((column) => !column.before && column.after)
  )
    return undefined;
  return impact.columns.length === 1 ? "New column" : `${impact.columns.length} new columns`;
}
