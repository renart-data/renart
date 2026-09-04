import type {
  SemanticDiffDraftAnalysis,
  SemanticDiffInlineFinding,
  SemanticDiffInlineTone,
} from "./semantic-diff-inline";
import type { SemanticDiffDemoScenario } from "./semantic-diff-demo";

export type SemanticImpactLensPosture = "ready" | "review_required";
export type SemanticImpactLensStageState =
  | "stable"
  | "changed"
  | "normalized"
  | "protected"
  | "unknown";
export type SemanticImpactLensEvidence = "observed" | "canonical" | "inferred" | "lineage";

export type SemanticImpactLensStage = {
  id: "cause" | "query" | "effect" | "consumers";
  eyebrow: string;
  title: string;
  before: string;
  after: string;
  state: SemanticImpactLensStageState;
  evidence: SemanticImpactLensEvidence;
};

export type SemanticImpactLensConsumer = {
  name: string;
  kind: "model" | "dashboard" | "export";
  depth: number;
  field: string;
  effect: string;
  action: string;
  tone: SemanticDiffInlineTone;
};

export type SemanticImpactLensPreset = {
  id: string;
  label: string;
  description: string;
  sql: string;
  tone: SemanticDiffInlineTone;
};

export type SemanticImpactLens = {
  posture: SemanticImpactLensPosture;
  postureLabel: string;
  postureDetail: string;
  focusedFinding: string;
  focusedDetail: string;
  affectedConsumerCount: number;
  hopCount: number;
  scopeLabel: string;
  validationLabel: string;
  stages: SemanticImpactLensStage[];
  consumers: SemanticImpactLensConsumer[];
  presets: SemanticImpactLensPreset[];
  currentPresetId?: string;
};

export function buildSemanticImpactLens(
  scenario: SemanticDiffDemoScenario,
  draftSql: string,
  analysis: SemanticDiffDraftAnalysis,
  focusedFindingId?: string,
): SemanticImpactLens {
  const focusedFinding =
    analysis.findings.find((finding) => finding.id === focusedFindingId) ?? analysis.findings[0];

  switch (scenario.id) {
    case "propagated-type":
      return propagatedTypeLens(scenario, draftSql, analysis, focusedFinding);
    case "formatting-only":
      return formattingLens(scenario, draftSql, analysis, focusedFinding);
    case "behavior-change":
      return behaviorLens(scenario, draftSql, analysis, focusedFinding);
    case "contract-break":
      return contractLens(scenario, draftSql, analysis, focusedFinding);
  }
}

function propagatedTypeLens(
  scenario: SemanticDiffDemoScenario,
  draftSql: string,
  analysis: SemanticDiffDraftAnalysis,
  finding: SemanticDiffInlineFinding | undefined,
) {
  const contained = analysis.findings.some((item) => item.id === "propagated-type-contained");
  const attached = analysis.findings.some(
    (item) => item.id === "propagated-total-amount-type" || item.id === "propagated-type-contained",
  );
  const consumers: SemanticImpactLensConsumer[] = [
    {
      name: "finance.monthly_revenue",
      kind: "model",
      depth: 1,
      field: "revenue.total",
      effect: contained
        ? "HUGEINT input contract remains stable"
        : attached
          ? "Numeric input widens to DOUBLE"
          : "Draft impact must be re-inferred",
      action: contained
        ? "No change"
        : attached
          ? "Review arithmetic and casts"
          : "Re-run analysis",
      tone: contained ? "safe" : "warning",
    },
    {
      name: "reporting.executive_kpis",
      kind: "dashboard",
      depth: 2,
      field: "monthly_revenue.total",
      effect: contained
        ? "Upstream numeric contract is protected"
        : attached
          ? "Metric formatter receives a DOUBLE"
          : "Transitive impact is not resolved yet",
      action: contained ? "No change" : attached ? "Preview KPI formatting" : "Re-run analysis",
      tone: contained ? "safe" : "warning",
    },
  ];

  return finalizeLens(
    draftSql,
    analysis,
    finding,
    [
      stage(
        "cause",
        "Upstream fact",
        "lineitems.total_amount",
        "INTEGER",
        "DOUBLE",
        "changed",
        "observed",
      ),
      stage(
        "query",
        "Current asset",
        "analytics.revenue",
        "SUM(total_amount)",
        contained ? "CAST(SUM(...) AS HUGEINT)" : attached ? "SUM(total_amount)" : "edited draft",
        contained ? "protected" : analysis.canonicalSame ? "stable" : "changed",
        "canonical",
      ),
      stage(
        "effect",
        "Inferred output",
        "revenue.total",
        "HUGEINT",
        contained ? "HUGEINT" : attached ? "DOUBLE" : "re-infer draft",
        contained ? "protected" : attached ? "changed" : "unknown",
        "inferred",
      ),
      consumerStage(consumers),
    ],
    consumers,
    [
      preset(
        "accept-widened-type",
        "Keep widened type",
        "Leave the SQL untouched and review the downstream DOUBLE contract.",
        scenario.after.sql,
        "warning",
      ),
      preset(
        "pin-output-contract",
        "Pin HUGEINT output",
        "Contain the upstream change with an explicit contract boundary.",
        "SELECT CAST(SUM(total_amount) AS HUGEINT) AS total\nFROM analytics.lineitems;",
        "safe",
      ),
    ],
    contained ? "Contract protected at analytics.revenue" : "Review the inferred type boundary",
    contained
      ? "The source still widens, but the explicit output cast stops the change before known consumers."
      : "The SQL is executable, but two known consumers inherit a different numeric contract.",
    contained ? "No downstream validation suggested" : "Preview 2 consumers before deploy",
  );
}

function formattingLens(
  scenario: SemanticDiffDemoScenario,
  draftSql: string,
  analysis: SemanticDiffDraftAnalysis,
  finding: SemanticDiffInlineFinding | undefined,
) {
  const safe = analysis.tone === "safe";
  const consumers = stableRevenueConsumers(
    safe,
    "Canonical query and output contract are unchanged",
    "Candidate semantics changed; recompute output and behavior",
  );

  return finalizeLens(
    draftSql,
    analysis,
    finding,
    [
      stage(
        "cause",
        "Source bytes",
        "analytics.revenue.sql",
        "one line",
        "comment + layout",
        "changed",
        "observed",
      ),
      stage(
        "query",
        "Canonical query",
        "query fingerprint",
        "same fingerprint",
        safe ? "same fingerprint" : "different fingerprint",
        safe ? "normalized" : "changed",
        "canonical",
      ),
      stage(
        "effect",
        "Inferred output",
        "revenue.total",
        "HUGEINT",
        safe ? "HUGEINT" : "re-infer draft",
        safe ? "stable" : "unknown",
        "inferred",
      ),
      consumerStage(consumers),
    ],
    consumers,
    [
      preset(
        "formatting-only",
        "Formatting only",
        "Keep the presentation edit and collapse it semantically.",
        scenario.after.sql,
        "safe",
      ),
      preset(
        "change-aggregation",
        "Try SUM → AVG",
        "Turn a visual-looking edit into a real aggregation change.",
        "SELECT AVG(total_amount) AS total FROM analytics.lineitems;",
        "warning",
      ),
    ],
    safe ? "No semantic action" : "Review the changed query",
    safe
      ? "Formatting remains visible in Git, while canonical behavior and known consumers stay untouched."
      : "The draft no longer normalizes to the deployed query, so downstream inference must run again.",
    safe ? "No downstream validation suggested" : "Re-infer contract and preview consumers",
  );
}

function behaviorLens(
  scenario: SemanticDiffDemoScenario,
  draftSql: string,
  analysis: SemanticDiffDraftAnalysis,
  finding: SemanticDiffInlineFinding | undefined,
) {
  const safe = analysis.tone === "safe";
  const afterPopulation = safe ? "paid only" : statusPopulation(draftSql);
  const consumers: SemanticImpactLensConsumer[] = [
    {
      name: "finance.refund_rate",
      kind: "model",
      depth: 1,
      field: "customer_revenue.total_amount",
      effect: safe ? "Revenue population remains paid-only" : "Refunded rows enter a revenue input",
      action: safe ? "No change" : "Review denominator overlap",
      tone: safe ? "safe" : "warning",
    },
    {
      name: "reporting.customer_ltv",
      kind: "dashboard",
      depth: 2,
      field: "customer_revenue",
      effect: safe ? "Cohort population remains stable" : "Revenue population widens by status",
      action: safe ? "No change" : "Preview affected cohorts",
      tone: safe ? "safe" : "warning",
    },
  ];

  return finalizeLens(
    draftSql,
    analysis,
    finding,
    [
      stage(
        "cause",
        "Predicate fact",
        "orders.status",
        "paid",
        safe ? "paid" : "paid + other",
        safe ? "stable" : "changed",
        "observed",
      ),
      stage(
        "query",
        "Current asset",
        "analytics.customer_revenue",
        "status = 'paid'",
        safe ? "status = 'paid'" : "edited status predicate",
        safe ? "stable" : "changed",
        "canonical",
      ),
      stage(
        "effect",
        "Behavior component",
        "Eligible row population",
        "paid only",
        afterPopulation,
        safe ? "protected" : "changed",
        "inferred",
      ),
      consumerStage(consumers),
    ],
    consumers,
    [
      preset(
        "include-refunds",
        "Include refunded",
        "Use the saved wider population and inspect its blast radius.",
        scenario.after.sql,
        "warning",
      ),
      preset(
        "paid-only",
        "Keep paid only",
        "Restore the deployed population without changing the schema.",
        scenario.before.sql,
        "safe",
      ),
    ],
    safe ? "Behavior matches deployment" : "Review the wider row population",
    safe
      ? "The predicate and downstream population match the deployed behavior again."
      : "The output columns are stable, but two consumers see a broader business population.",
    safe ? "No downstream validation suggested" : "Preview 2 population-sensitive consumers",
  );
}

function contractLens(
  scenario: SemanticDiffDemoScenario,
  draftSql: string,
  analysis: SemanticDiffDraftAnalysis,
  finding: SemanticDiffInlineFinding | undefined,
) {
  const safe = analysis.tone === "safe";
  const consumers: SemanticImpactLensConsumer[] = [
    {
      name: "finance.order_exports",
      kind: "export",
      depth: 1,
      field: "currency",
      effect: safe ? "Required field remains available" : "Required export field is missing",
      action: safe ? "No change" : "Restore or migrate",
      tone: safe ? "safe" : "warning",
    },
    {
      name: "reporting.order_mix",
      kind: "dashboard",
      depth: 1,
      field: "currency",
      effect: safe ? "Grouping field remains available" : "Grouping field is unavailable",
      action: safe ? "No change" : "Restore or migrate",
      tone: safe ? "safe" : "warning",
    },
  ];

  return finalizeLens(
    draftSql,
    analysis,
    finding,
    [
      stage(
        "cause",
        "Projection fact",
        "SELECT list",
        "3 columns",
        safe ? "3 columns" : "2 columns",
        safe ? "stable" : "changed",
        "observed",
      ),
      stage(
        "query",
        "Current asset",
        "analytics.order_facts",
        "currency at #3",
        safe ? "currency at #3" : "position #3 removed",
        safe ? "protected" : "changed",
        "canonical",
      ),
      stage(
        "effect",
        "Output contract",
        "order_facts.currency",
        "VARCHAR",
        safe ? "VARCHAR" : "absent",
        safe ? "protected" : "changed",
        "inferred",
      ),
      consumerStage(consumers),
    ],
    consumers,
    [
      preset(
        "remove-currency",
        "Remove currency",
        "Use the saved contract break and inspect direct consumers.",
        scenario.after.sql,
        "warning",
      ),
      preset(
        "restore-currency",
        "Restore currency",
        "Put the field back at its deployed output position.",
        scenario.before.sql,
        "safe",
      ),
    ],
    safe ? "Output contract restored" : "Resolve the missing output field",
    safe
      ? "The ordered contract once again satisfies both known consumers."
      : "Two direct consumers reference currency and need either restoration or an explicit migration.",
    safe ? "No downstream validation suggested" : "Validate 2 direct consumers",
  );
}

function finalizeLens(
  draftSql: string,
  analysis: SemanticDiffDraftAnalysis,
  finding: SemanticDiffInlineFinding | undefined,
  stages: SemanticImpactLensStage[],
  consumers: SemanticImpactLensConsumer[],
  presets: SemanticImpactLensPreset[],
  postureLabel: string,
  postureDetail: string,
  validationLabel: string,
): SemanticImpactLens {
  const affectedConsumerCount = consumers.filter((consumer) => consumer.tone === "warning").length;
  const posture = affectedConsumerCount > 0 ? "review_required" : "ready";
  const hopCount =
    posture === "review_required"
      ? Math.max(
          0,
          ...consumers.filter((consumer) => consumer.tone === "warning").map((item) => item.depth),
        )
      : 0;

  return {
    posture,
    postureLabel,
    postureDetail,
    focusedFinding: finding?.title ?? "Draft analyzed",
    focusedDetail: finding?.detail ?? "No inline finding is selected.",
    affectedConsumerCount,
    hopCount,
    scopeLabel:
      affectedConsumerCount === 0
        ? "No affected consumers"
        : `${affectedConsumerCount} affected · ${hopCount} ${hopCount === 1 ? "hop" : "hops"}`,
    validationLabel,
    stages,
    consumers,
    presets,
    currentPresetId: presets.find((item) => item.sql === draftSql)?.id,
  };
}

function stage(
  id: SemanticImpactLensStage["id"],
  eyebrow: string,
  title: string,
  before: string,
  after: string,
  state: SemanticImpactLensStageState,
  evidence: SemanticImpactLensEvidence,
): SemanticImpactLensStage {
  return { id, eyebrow, title, before, after, state, evidence };
}

function consumerStage(consumers: SemanticImpactLensConsumer[]) {
  const affected = consumers.filter((consumer) => consumer.tone === "warning");
  return stage(
    "consumers",
    "Known lineage",
    "Downstream consumers",
    `${consumers.length} linked`,
    affected.length === 0 ? "all protected" : `${affected.length} affected`,
    affected.length === 0 ? "protected" : "changed",
    "lineage",
  );
}

function preset(
  id: string,
  label: string,
  description: string,
  sql: string,
  tone: SemanticDiffInlineTone,
): SemanticImpactLensPreset {
  return { id, label, description, sql, tone };
}

function stableRevenueConsumers(safe: boolean, safeEffect: string, warningEffect: string) {
  return [
    {
      name: "finance.monthly_revenue",
      kind: "model" as const,
      depth: 1,
      field: "revenue.total",
      effect: safe ? safeEffect : warningEffect,
      action: safe ? "No change" : "Re-run analysis",
      tone: safe ? ("safe" as const) : ("warning" as const),
    },
    {
      name: "reporting.executive_kpis",
      kind: "dashboard" as const,
      depth: 2,
      field: "monthly_revenue.total",
      effect: safe ? safeEffect : warningEffect,
      action: safe ? "No change" : "Re-run analysis",
      tone: safe ? ("safe" as const) : ("warning" as const),
    },
  ];
}

function statusPopulation(sql: string) {
  const where = sql.match(/\bwhere\b([\s\S]*)/i)?.[1] ?? "";
  const values = Array.from(where.matchAll(/'((?:''|[^'])*)'/g), (match) =>
    match[1].replace(/''/g, "'").toLowerCase(),
  );

  return values.length === 0 ? "all statuses" : values.join(" + ");
}
