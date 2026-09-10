import { describe, expect, it } from "vitest";

import { analyzeSemanticDiffDraft } from "./semantic-diff-inline";
import { getSemanticDiffDemoScenario } from "./semantic-diff-demo";
import { buildSemanticImpactLens } from "./semantic-impact-playground";

function lensFor(scenarioId: string, draftSql?: string) {
  const scenario = getSemanticDiffDemoScenario(scenarioId);
  const draft = draftSql ?? scenario.after.sql;
  const analysis = analyzeSemanticDiffDraft(scenario, draft);

  return buildSemanticImpactLens(scenario, draft, analysis, analysis.findings[0]?.id);
}

describe("semantic impact playground lens", () => {
  it("traces an unchanged aggregation through its inferred type and known consumers", () => {
    const lens = lensFor("propagated-type");

    expect(lens).toMatchObject({
      posture: "review_required",
      affectedConsumerCount: 2,
      hopCount: 2,
      focusedFinding: "Upstream type propagated",
    });
    expect(lens.stages).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          id: "cause",
          title: "lineitems.total_amount",
          before: "INTEGER",
          after: "DOUBLE",
          evidence: "observed",
        }),
        expect.objectContaining({
          id: "effect",
          title: "revenue.total",
          before: "HUGEINT",
          after: "DOUBLE",
          evidence: "inferred",
        }),
      ]),
    );
    expect(lens.consumers.map((consumer) => consumer.name)).toEqual([
      "finance.monthly_revenue",
      "reporting.executive_kpis",
    ]);
  });

  it("turns an explicit boundary cast into a protected downstream contract", () => {
    const scenario = getSemanticDiffDemoScenario("propagated-type");
    const protectedSql = lensFor("propagated-type").presets.find(
      (preset) => preset.id === "pin-output-contract",
    )?.sql;

    expect(protectedSql).toBeDefined();
    const analysis = analyzeSemanticDiffDraft(scenario, protectedSql!);
    const lens = buildSemanticImpactLens(
      scenario,
      protectedSql!,
      analysis,
      analysis.findings[0]?.id,
    );

    expect(analysis).toMatchObject({ tone: "safe", canonicalSame: false });
    expect(analysis.findings[0]).toMatchObject({
      id: "propagated-type-contained",
      title: "Output contract pinned",
    });
    expect(lens).toMatchObject({ posture: "ready", affectedConsumerCount: 0 });
    expect(lens.stages.find((stage) => stage.id === "effect")).toMatchObject({
      before: "HUGEINT",
      after: "HUGEINT",
      state: "protected",
    });
    expect(lens.consumers.every((consumer) => consumer.tone === "safe")).toBe(true);
  });

  it("keeps a formatting-only edit out of the blast radius", () => {
    const lens = lensFor("formatting-only");

    expect(lens).toMatchObject({ posture: "ready", affectedConsumerCount: 0, hopCount: 0 });
    expect(lens.stages.find((stage) => stage.id === "query")).toMatchObject({
      state: "normalized",
      after: "same fingerprint",
    });
    expect(lens.consumers.every((consumer) => consumer.tone === "safe")).toBe(true);
  });

  it("widens and then contains the behavior-change blast radius as the draft changes", () => {
    const changed = lensFor("behavior-change");
    const scenario = getSemanticDiffDemoScenario("behavior-change");
    const restoredAnalysis = analyzeSemanticDiffDraft(scenario, scenario.before.sql);
    const restored = buildSemanticImpactLens(
      scenario,
      scenario.before.sql,
      restoredAnalysis,
      restoredAnalysis.findings[0]?.id,
    );

    expect(changed).toMatchObject({
      posture: "review_required",
      affectedConsumerCount: 2,
      hopCount: 2,
    });
    expect(changed.stages.find((stage) => stage.id === "effect")).toMatchObject({
      before: "paid only",
      after: "paid + refunded",
      state: "changed",
    });
    expect(restored).toMatchObject({ posture: "ready", affectedConsumerCount: 0 });
  });

  it("names the consumers of a removed output column and clears them when restored", () => {
    const broken = lensFor("contract-break");
    const scenario = getSemanticDiffDemoScenario("contract-break");
    const restoredAnalysis = analyzeSemanticDiffDraft(scenario, scenario.before.sql);
    const restored = buildSemanticImpactLens(
      scenario,
      scenario.before.sql,
      restoredAnalysis,
      restoredAnalysis.findings[0]?.id,
    );

    expect(broken).toMatchObject({ posture: "review_required", affectedConsumerCount: 2 });
    expect(broken.consumers).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          name: "finance.order_exports",
          field: "currency",
          action: "Restore or migrate",
        }),
        expect.objectContaining({ name: "reporting.order_mix", field: "currency" }),
      ]),
    );
    expect(restored).toMatchObject({ posture: "ready", affectedConsumerCount: 0 });
  });
});
