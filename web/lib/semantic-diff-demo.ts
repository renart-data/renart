import type { PipelinePlanSemanticImpact } from "./generated/api-types";

export type SemanticDiffDemoScenarioId =
  | "propagated-type"
  | "formatting-only"
  | "behavior-change"
  | "contract-break";

export type SemanticDiffDemoTone = "safe" | "warning";

export type SemanticDiffDemoSide = {
  label: string;
  version: string;
  sql: string;
  canonicalSql: string;
  upstreamContract: string;
  outputContract: string;
  highlightedLines: number[];
};

export type SemanticDiffDemoTrace = {
  label: string;
  before: string;
  after: string;
  state: "stable" | "normalized" | "changed";
};

export type SemanticDiffDemoScenario = {
  id: SemanticDiffDemoScenarioId;
  sequence: string;
  label: string;
  eyebrow: string;
  title: string;
  description: string;
  result: string;
  tone: SemanticDiffDemoTone;
  before: SemanticDiffDemoSide;
  after: SemanticDiffDemoSide;
  trace: SemanticDiffDemoTrace[];
  takeaway: string;
  impact: PipelinePlanSemanticImpact;
};

const baselineVersion = "42f0c8ab9e71d2e4";

export const semanticDiffDemoScenarios: SemanticDiffDemoScenario[] = [
  {
    id: "propagated-type",
    sequence: "01",
    label: "Propagated type",
    eyebrow: "Same SQL, different contract",
    title: "The query did not change. Its meaning did.",
    description:
      "An upstream column widens from INTEGER to DOUBLE. The downstream aggregation is byte-identical, but its inferred result type changes.",
    result: "Deployment warning",
    tone: "warning",
    before: {
      label: "Deployed",
      version: "deployment 42",
      sql: "SELECT SUM(total_amount) AS total\nFROM analytics.lineitems;",
      canonicalSql: "SELECT SUM(total_amount) AS total FROM analytics.lineitems",
      upstreamContract: "lineitems.total_amount · INTEGER · non null",
      outputContract: "revenue.total · HUGEINT · nullable",
      highlightedLines: [],
    },
    after: {
      label: "Candidate",
      version: "saved working tree",
      sql: "SELECT SUM(total_amount) AS total\nFROM analytics.lineitems;",
      canonicalSql: "SELECT SUM(total_amount) AS total FROM analytics.lineitems",
      upstreamContract: "lineitems.total_amount · DOUBLE · non null",
      outputContract: "revenue.total · DOUBLE · nullable",
      highlightedLines: [],
    },
    trace: [
      {
        label: "Upstream input",
        before: "INTEGER",
        after: "DOUBLE",
        state: "changed",
      },
      {
        label: "Canonical query",
        before: "same fingerprint",
        after: "same fingerprint",
        state: "stable",
      },
      {
        label: "Inferred output",
        before: "HUGEINT",
        after: "DOUBLE",
        state: "changed",
      },
    ],
    takeaway:
      "A textual Git diff would stay empty here. The semantic report still surfaces the downstream contract change and marks it as propagated.",
    impact: {
      version: "v1",
      digest: "v1:demo-propagated-type",
      status: "available",
      baseline_version_id: baselineVersion,
      complete: true,
      assets: [
        {
          name: "analytics.revenue",
          dialect: "duckdb",
          change: "modified",
          source_change: "unchanged",
          origin: "propagated",
          severity: "warning",
          complete: true,
          before_canonical_fingerprint: "v1:query-revenue",
          after_canonical_fingerprint: "v1:query-revenue",
          columns: [
            {
              index: 0,
              before: { name: "total", type: "HUGEINT", nullability: "nullable" },
              after: { name: "total", type: "DOUBLE", nullability: "nullable" },
              name_changed: false,
              type_changed: true,
              nullability_changed: false,
            },
          ],
        },
      ],
      summary: {
        added: 0,
        removed: 0,
        modified: 1,
        formatting_only: 0,
        behavior_changes: 0,
        schema_changes: 1,
        incomplete: 0,
        warnings: 1,
      },
    },
  },
  {
    id: "formatting-only",
    sequence: "02",
    label: "Formatting noise",
    eyebrow: "Different bytes, same query",
    title: "Whitespace should not become deployment risk.",
    description:
      "Case, line breaks, spacing, and an ordinary comment all change. Canonical SQL and the inferred output contract stay identical.",
    result: "No semantic warning",
    tone: "safe",
    before: {
      label: "Deployed",
      version: "deployment 42",
      sql: "SELECT SUM(total_amount) AS total FROM analytics.lineitems;",
      canonicalSql: "SELECT SUM(total_amount) AS total FROM analytics.lineitems",
      upstreamContract: "lineitems.total_amount · INTEGER · non null",
      outputContract: "revenue.total · HUGEINT · nullable",
      highlightedLines: [1],
    },
    after: {
      label: "Candidate",
      version: "saved working tree",
      sql: "-- make the aggregation easier to scan\nselect\n  sum( total_amount ) as total\nfrom analytics.lineitems;",
      canonicalSql: "SELECT SUM(total_amount) AS total FROM analytics.lineitems",
      upstreamContract: "lineitems.total_amount · INTEGER · non null",
      outputContract: "revenue.total · HUGEINT · nullable",
      highlightedLines: [1, 2, 3, 4],
    },
    trace: [
      {
        label: "Source bytes",
        before: "one line",
        after: "four lines + comment",
        state: "changed",
      },
      {
        label: "Canonical query",
        before: "same fingerprint",
        after: "same fingerprint",
        state: "normalized",
      },
      {
        label: "Inferred output",
        before: "HUGEINT",
        after: "HUGEINT",
        state: "stable",
      },
    ],
    takeaway:
      "The byte-level change remains visible for review, but presentation comments and formatting do not inflate the semantic warning count.",
    impact: {
      version: "v1",
      digest: "v1:demo-formatting-only",
      status: "available",
      baseline_version_id: baselineVersion,
      complete: true,
      assets: [
        {
          name: "analytics.revenue",
          dialect: "duckdb",
          change: "modified",
          source_change: "formatting_only",
          origin: "direct",
          severity: "info",
          complete: true,
          before_canonical_fingerprint: "v1:query-revenue",
          after_canonical_fingerprint: "v1:query-revenue",
          columns: [],
        },
      ],
      summary: {
        added: 0,
        removed: 0,
        modified: 1,
        formatting_only: 1,
        behavior_changes: 0,
        schema_changes: 0,
        incomplete: 0,
        warnings: 0,
      },
    },
  },
  {
    id: "behavior-change",
    sequence: "03",
    label: "Behavior change",
    eyebrow: "Stable columns, wider predicate",
    title: "The schema stayed stable. The population changed.",
    description:
      "Refunded orders now enter a revenue model. Column names and types are unchanged, but the filter component carries a different behavior fingerprint.",
    result: "Deployment warning",
    tone: "warning",
    before: {
      label: "Deployed",
      version: "deployment 42",
      sql: "SELECT customer_id, total_amount\nFROM analytics.orders\nWHERE status = 'paid';",
      canonicalSql: "SELECT customer_id, total_amount FROM analytics.orders WHERE status = 'paid'",
      upstreamContract: "orders.status · VARCHAR · non null",
      outputContract: "customer_id BIGINT · total_amount DECIMAL(18,2)",
      highlightedLines: [3],
    },
    after: {
      label: "Candidate",
      version: "saved working tree",
      sql: "SELECT customer_id, total_amount\nFROM analytics.orders\nWHERE status IN ('paid', 'refunded');",
      canonicalSql:
        "SELECT customer_id, total_amount FROM analytics.orders WHERE status IN ('paid', 'refunded')",
      upstreamContract: "orders.status · VARCHAR · non null",
      outputContract: "customer_id BIGINT · total_amount DECIMAL(18,2)",
      highlightedLines: [3],
    },
    trace: [
      {
        label: "Relation",
        before: "analytics.orders",
        after: "analytics.orders",
        state: "stable",
      },
      {
        label: "Filter component",
        before: "paid only",
        after: "paid + refunded",
        state: "changed",
      },
      {
        label: "Inferred output",
        before: "2 stable columns",
        after: "2 stable columns",
        state: "stable",
      },
    ],
    takeaway:
      "An output-schema checker alone would call this safe. Component-level query facts keep behavior changes visible even when the contract is stable.",
    impact: {
      version: "v1",
      digest: "v1:demo-behavior-change",
      status: "available",
      baseline_version_id: baselineVersion,
      complete: true,
      assets: [
        {
          name: "analytics.customer_revenue",
          dialect: "duckdb",
          change: "modified",
          source_change: "changed",
          origin: "direct",
          severity: "warning",
          complete: true,
          before_canonical_fingerprint: "v1:filter-paid",
          after_canonical_fingerprint: "v1:filter-paid-refunded",
          columns: [],
        },
      ],
      summary: {
        added: 0,
        removed: 0,
        modified: 1,
        formatting_only: 0,
        behavior_changes: 1,
        schema_changes: 0,
        incomplete: 0,
        warnings: 1,
      },
    },
  },
  {
    id: "contract-break",
    sequence: "04",
    label: "Contract break",
    eyebrow: "Output column removed",
    title: "A downstream-facing column disappeared.",
    description:
      "The query still runs, but removing currency changes the ordered output contract. Renart shows the exact missing position before deployment.",
    result: "Deployment warning",
    tone: "warning",
    before: {
      label: "Deployed",
      version: "deployment 42",
      sql: "SELECT\n  customer_id,\n  total_amount,\n  currency\nFROM analytics.orders;",
      canonicalSql: "SELECT customer_id, total_amount, currency FROM analytics.orders",
      upstreamContract: "orders · customer_id, total_amount, currency",
      outputContract: "customer_id · total_amount · currency",
      highlightedLines: [4],
    },
    after: {
      label: "Candidate",
      version: "saved working tree",
      sql: "SELECT\n  customer_id,\n  total_amount\nFROM analytics.orders;",
      canonicalSql: "SELECT customer_id, total_amount FROM analytics.orders",
      upstreamContract: "orders · customer_id, total_amount, currency",
      outputContract: "customer_id · total_amount",
      highlightedLines: [3],
    },
    trace: [
      {
        label: "Projection",
        before: "3 columns",
        after: "2 columns",
        state: "changed",
      },
      {
        label: "Removed position",
        before: "#3 currency",
        after: "not present",
        state: "changed",
      },
      {
        label: "Known consumers",
        before: "2 references",
        after: "review required",
        state: "changed",
      },
    ],
    takeaway:
      "The diff is positional and typed, so reviewers see a contract break rather than having to infer impact from a deleted SELECT expression.",
    impact: {
      version: "v1",
      digest: "v1:demo-contract-break",
      status: "available",
      baseline_version_id: baselineVersion,
      complete: true,
      assets: [
        {
          name: "analytics.order_facts",
          dialect: "duckdb",
          change: "modified",
          source_change: "changed",
          origin: "direct",
          severity: "warning",
          complete: true,
          before_canonical_fingerprint: "v1:projection-with-currency",
          after_canonical_fingerprint: "v1:projection-without-currency",
          columns: [
            {
              index: 2,
              before: { name: "currency", type: "VARCHAR", nullability: "non_null" },
              name_changed: true,
              type_changed: true,
              nullability_changed: true,
            },
          ],
        },
      ],
      summary: {
        added: 0,
        removed: 0,
        modified: 1,
        formatting_only: 0,
        behavior_changes: 1,
        schema_changes: 1,
        incomplete: 0,
        warnings: 1,
      },
    },
  },
];

export function getSemanticDiffDemoScenario(id: string | undefined) {
  return (
    semanticDiffDemoScenarios.find((scenario) => scenario.id === id) ?? semanticDiffDemoScenarios[0]
  );
}
