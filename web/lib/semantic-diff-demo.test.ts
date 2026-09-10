import { describe, expect, it } from "vitest";

import { getSemanticDiffDemoScenario, semanticDiffDemoScenarios } from "./semantic-diff-demo";

describe("semantic diff demonstrator scenarios", () => {
  it("keeps every curated scenario addressable and falls back deterministically", () => {
    expect(semanticDiffDemoScenarios.map((scenario) => scenario.id)).toEqual([
      "propagated-type",
      "formatting-only",
      "behavior-change",
      "contract-break",
    ]);
    expect(getSemanticDiffDemoScenario("behavior-change").id).toBe("behavior-change");
    expect(getSemanticDiffDemoScenario("missing").id).toBe("propagated-type");
  });

  it("shows the defining unchanged-SQL propagated-type case", () => {
    const scenario = getSemanticDiffDemoScenario("propagated-type");
    expect(scenario.before.sql).toBe(scenario.after.sql);
    expect(scenario.before.upstreamContract).not.toBe(scenario.after.upstreamContract);

    const revenue = scenario.impact.assets.find((asset) => asset.name === "analytics.revenue");
    expect(revenue?.source_change).toBe("unchanged");
    expect(revenue?.origin).toBe("propagated");
    expect(revenue?.columns[0]).toMatchObject({ type_changed: true });
  });

  it("distinguishes formatting noise from behavior and contract changes", () => {
    const formatting = getSemanticDiffDemoScenario("formatting-only");
    expect(formatting.before.sql).not.toBe(formatting.after.sql);
    expect(formatting.impact.summary).toMatchObject({ formatting_only: 1, warnings: 0 });

    const behavior = getSemanticDiffDemoScenario("behavior-change");
    expect(behavior.impact.summary).toMatchObject({ behavior_changes: 1, schema_changes: 0 });

    const contract = getSemanticDiffDemoScenario("contract-break");
    expect(contract.impact.summary).toMatchObject({ schema_changes: 1, warnings: 1 });
    expect(contract.impact.assets[0]?.columns.some((column) => !column.after)).toBe(true);
  });
});
