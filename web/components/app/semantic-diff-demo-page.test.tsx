import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { SemanticDiffInlineEditor } from "./semantic-diff-inline-editor";
import { SemanticImpactPeek } from "./semantic-impact-peek";
import { getSemanticDiffDemoScenario } from "@/lib/semantic-diff-demo";
import { analyzeSemanticDiffDraft } from "@/lib/semantic-diff-inline";
import { buildSemanticImpactLens } from "@/lib/semantic-impact-playground";

describe("deployment playground progressive disclosure", () => {
  it("keeps the deep explanation out of the initial editor surface", () => {
    const scenario = getSemanticDiffDemoScenario("propagated-type");
    const markup = renderToStaticMarkup(<SemanticDiffInlineEditor scenario={scenario} />);
    expect(markup).toContain("Explain this change");
    expect(markup).toContain("2 downstream");
    expect(markup).not.toContain("finance.monthly_revenue");
    expect(markup).not.toContain("Pin HUGEINT output");
    expect(markup).not.toContain("Saved report snapshot");
  });

  it("analyzes a retained draft rather than resetting to the saved candidate on mount", () => {
    const scenario = getSemanticDiffDemoScenario("contract-break");
    const markup = renderToStaticMarkup(
      <SemanticDiffInlineEditor scenario={scenario} initialDraft={scenario.before.sql} />,
    );
    expect(markup).toContain("No downstream impact");
    expect(markup).not.toContain("2 downstream");
  });

  it("makes explanation and reversible what-if edits available when opened", () => {
    const scenario = getSemanticDiffDemoScenario("behavior-change");
    const analysis = analyzeSemanticDiffDraft(scenario, scenario.after.sql);
    const lens = buildSemanticImpactLens(scenario, scenario.after.sql, analysis);
    const markup = renderToStaticMarkup(
      <SemanticImpactPeek lens={lens} open onOpenChange={() => {}} onApplyPreset={() => {}} />,
    );
    expect(markup).toContain("Why this matters");
    expect(markup).toContain("Keep paid only");
    expect(markup).toContain("Trace &amp; consumers");
  });
});
