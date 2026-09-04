import { describe, expect, it } from "vitest";

import { analyzeSemanticDiffDraft, canonicalizeDemoSql } from "./semantic-diff-inline";
import { getSemanticDiffDemoScenario } from "./semantic-diff-demo";

describe("semantic diff inline analysis", () => {
  it("anchors a propagated type change in byte-identical SQL", () => {
    const scenario = getSemanticDiffDemoScenario("propagated-type");
    const analysis = analyzeSemanticDiffDraft(scenario, scenario.after.sql);

    expect(analysis).toMatchObject({
      tone: "warning",
      canonicalSame: true,
      matchesSavedCandidate: true,
    });
    expect(analysis.findings).toHaveLength(1);
    expect(analysis.findings[0]).toMatchObject({
      id: "propagated-total-amount-type",
      title: "Upstream type propagated",
      before: { lineNumber: 1, label: "HUGEINT" },
      after: { lineNumber: 1, label: "DOUBLE ↑" },
    });
  });

  it("recognizes an explicit cast that contains a propagated type change", () => {
    const scenario = getSemanticDiffDemoScenario("propagated-type");
    const analysis = analyzeSemanticDiffDraft(
      scenario,
      "SELECT CAST(SUM(total_amount) AS HUGEINT) AS total\nFROM analytics.lineitems;",
    );

    expect(analysis).toMatchObject({ tone: "safe", canonicalSame: false });
    expect(analysis.findings).toHaveLength(1);
    expect(analysis.findings[0]).toMatchObject({
      id: "propagated-type-contained",
      title: "Output contract pinned",
      after: { lineNumber: 1, label: "HUGEINT ✓" },
    });
  });

  it("collapses comments, case, and whitespace into one formatting-only signal", () => {
    const scenario = getSemanticDiffDemoScenario("formatting-only");
    const draft = `/* presentation only */
      select sum ( total_amount ) as total
      from analytics.lineitems ;`;
    const analysis = analyzeSemanticDiffDraft(scenario, draft);

    expect(canonicalizeDemoSql(draft)).toBe(canonicalizeDemoSql(scenario.before.sql));
    expect(analysis).toMatchObject({ tone: "safe", canonicalSame: true });
    expect(analysis.findings[0]).toMatchObject({
      id: "formatting-only",
      title: "Formatting only",
      after: { lineNumber: 2, label: "same canonical query" },
    });
  });

  it("turns a substantive edit in the formatting case into a warning", () => {
    const scenario = getSemanticDiffDemoScenario("formatting-only");
    const analysis = analyzeSemanticDiffDraft(
      scenario,
      "SELECT AVG(total_amount) AS total FROM analytics.lineitems;",
    );

    expect(analysis).toMatchObject({ tone: "warning", canonicalSame: false });
    expect(analysis.findings[0]).toMatchObject({
      id: "query-semantics-changed",
      title: "Query semantics changed",
    });
  });

  it("updates a filter lens from the currently edited predicate", () => {
    const scenario = getSemanticDiffDemoScenario("behavior-change");
    const draft = `SELECT customer_id, total_amount
FROM analytics.orders
WHERE status IN ('paid', 'chargeback');`;
    const analysis = analyzeSemanticDiffDraft(scenario, draft);

    expect(analysis.findings[0]).toMatchObject({
      id: "status-filter-change",
      title: "Filter widened",
      before: { lineNumber: 3, label: "paid only" },
      after: { lineNumber: 3, label: "+ chargeback" },
    });
  });

  it("moves a removed output column into an inline contract warning and clears it when restored", () => {
    const scenario = getSemanticDiffDemoScenario("contract-break");
    const removed = analyzeSemanticDiffDraft(scenario, scenario.after.sql);

    expect(removed).toMatchObject({ tone: "warning", canonicalSame: false });
    expect(removed.findings[0]).toMatchObject({
      id: "removed-output-currency",
      title: "Output column removed",
      before: { lineNumber: 4, label: "VARCHAR" },
      after: { lineNumber: 3, label: "− currency" },
    });

    const restored = analyzeSemanticDiffDraft(scenario, scenario.before.sql);
    expect(restored).toMatchObject({ tone: "safe", canonicalSame: true });
    expect(restored.findings[0]).toMatchObject({
      id: "output-contract-restored",
      title: "Output contract restored",
      after: { lineNumber: 4, label: "currency ✓" },
    });
  });
});
