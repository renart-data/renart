"use client";

import { ArrowRight, Check, ChevronRight, FileCode2, Moon, Rocket, Sun } from "lucide-react";
import { useState } from "react";
import { SemanticDiffInlineEditor } from "@/components/app/semantic-diff-inline-editor";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui/dialog";
import { useWorkspaceTheme } from "@/hooks/use-workspace-theme";
import {
  semanticDiffDemoScenarios,
  type SemanticDiffDemoScenarioId,
} from "@/lib/semantic-diff-demo";
import { analyzeSemanticDiffDraft } from "@/lib/semantic-diff-inline";
import { cn } from "@/lib/utils";

const fileNames: Record<SemanticDiffDemoScenarioId, string> = {
  "propagated-type": "revenue.sql",
  "formatting-only": "revenue_formatting.sql",
  "behavior-change": "customer_revenue.sql",
  "contract-break": "order_facts.sql",
};

export function SemanticDiffDemoPage() {
  const [open, setOpen] = useState(true);
  const [activeId, setActiveId] = useState<SemanticDiffDemoScenarioId | null>("propagated-type");
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [reviewed, setReviewed] = useState(false);
  const { theme, setTheme } = useWorkspaceTheme();
  const warningCount = semanticDiffDemoScenarios.filter(
    (scenario) =>
      analyzeSemanticDiffDraft(scenario, drafts[scenario.id] ?? scenario.after.sql).tone ===
      "warning",
  ).length;

  return (
    <main className="flex min-h-dvh flex-col bg-muted/25 text-foreground">
      <header className="flex h-12 items-center justify-between border-b px-5">
        <span className="text-xs font-medium">
          renart{" "}
          <span className="ml-2 font-normal text-muted-foreground">/ semantic playground</span>
        </span>
        <span className="text-[0.6875rem] text-muted-foreground">Deployment review concept</span>
      </header>
      <div className="flex flex-1 flex-col items-center justify-center gap-3 p-6">
        <Rocket className="size-6 text-muted-foreground" />
        <h1 className="text-lg font-medium">A quieter deployment review.</h1>
        <p className="max-w-sm text-center text-sm text-muted-foreground">
          Four SQL examples. Open a file, inspect its signal, try an edit.
        </p>
        <Button onClick={() => setOpen(true)}>Open deployment review</Button>
      </div>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent
          className="flex max-h-[calc(100dvh-2rem)] w-[calc(100%-2rem)] flex-col gap-0 overflow-hidden p-0 sm:max-w-[960px]"
          aria-describedby="deployment-concept-description"
        >
          <header className="shrink-0 border-b px-5 py-4 sm:px-6">
            <div className="flex items-center gap-2 pr-8">
              <DialogTitle className="text-base font-semibold">Review deployment</DialogTitle>
              <span className="rounded bg-muted px-1.5 py-0.5 text-[0.625rem] text-muted-foreground">
                Playground
              </span>
              <Button
                variant="ghost"
                size="icon-sm"
                className="ml-auto"
                aria-label={`Use ${theme === "dark" ? "light" : "dark"} theme`}
                onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
              >
                {theme === "dark" ? <Sun /> : <Moon />}
              </Button>
            </div>
            <DialogDescription
              id="deployment-concept-description"
              className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs"
            >
              <span className="font-medium text-foreground">analytics</span>
              <ArrowRight className="size-3" />
              <span>default</span>
              <span className="text-border">/</span>
              <span>Deployment #42 → working tree</span>
            </DialogDescription>
          </header>
          <div className="min-h-0 overflow-y-auto overscroll-contain">
            <div className="flex items-center justify-between gap-3 px-5 py-3 sm:px-6">
              <h2 className="text-xs font-medium">
                Changes & impact <span className="ml-1 text-muted-foreground">4</span>
              </h2>
              <span
                className="flex items-center gap-1.5 text-[0.6875rem] text-muted-foreground"
                role="status"
              >
                <span
                  className={cn(
                    "size-1.5 rounded-full",
                    warningCount ? "bg-warning" : "bg-primary",
                  )}
                />
                {warningCount ? `${warningCount} to review` : "No semantic warnings"}
              </span>
            </div>
            <div className="border-y">
              {semanticDiffDemoScenarios.map((scenario) => {
                const active = activeId === scenario.id;
                const sql = drafts[scenario.id] ?? scenario.after.sql;
                const analysis = analyzeSemanticDiffDraft(scenario, sql);
                const warning = analysis.tone === "warning";
                return (
                  <section key={scenario.id} className="border-b last:border-b-0">
                    <button
                      type="button"
                      aria-expanded={active}
                      aria-controls={active ? `review-file-${scenario.id}` : undefined}
                      onClick={() => setActiveId(active ? null : scenario.id)}
                      className={cn(
                        "flex w-full min-w-0 items-center gap-2 px-5 py-3 text-left outline-none hover:bg-muted/35 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40 sm:px-6",
                        active && "bg-muted/25",
                      )}
                    >
                      <ChevronRight
                        className={cn(
                          "size-3 shrink-0 text-muted-foreground transition-transform",
                          active && "rotate-90",
                        )}
                      />
                      <FileCode2 className="size-3.5 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1 truncate font-mono text-xs">
                        {fileNames[scenario.id]}
                      </span>
                      {sql !== scenario.after.sql ? (
                        <span className="text-[0.625rem] text-muted-foreground">edited</span>
                      ) : null}
                      <span
                        className={cn(
                          "max-w-[45%] truncate text-[0.6875rem]",
                          warning ? "text-foreground" : "text-muted-foreground",
                        )}
                      >
                        <span
                          className={cn(
                            "mr-1.5 inline-block size-1.5 rounded-full",
                            warning ? "bg-warning" : "bg-primary",
                          )}
                        />
                        {analysis.findings[0]?.title}
                      </span>
                    </button>
                    {active ? (
                      <div id={`review-file-${scenario.id}`} className="border-t">
                        <SemanticDiffInlineEditor
                          key={scenario.id}
                          scenario={scenario}
                          initialDraft={sql}
                          onDraftChange={(value) => {
                            setDrafts((previous) =>
                              previous[scenario.id] === value
                                ? previous
                                : { ...previous, [scenario.id]: value },
                            );
                            setReviewed(false);
                          }}
                        />
                      </div>
                    ) : null}
                  </section>
                );
              })}
            </div>
            <details className="group px-5 py-3 sm:px-6">
              <summary className="flex cursor-pointer list-none items-center gap-1.5 text-[0.6875rem] text-muted-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring/40 [&::-webkit-details-marker]:hidden">
                <ChevronRight className="size-3 transition-transform group-open:rotate-90" />
                Deployment details
              </summary>
              <div className="mt-3 space-y-2 border-l pl-4 text-xs leading-5 text-muted-foreground">
                <p>
                  Deploying captures pipeline definitions. Data execution and schedule promotion are
                  separate actions.
                </p>
                <p>
                  These four independent examples use curated DuckDB contracts and consumer
                  relationships. SQL edits stay in this playground; the analyzer is a demonstration,
                  not a full SQL validator.
                </p>
                <p>
                  The deployed side is fixed. Working drafts are retained while you move between
                  files.
                </p>
              </div>
            </details>
          </div>
          <footer className="flex shrink-0 flex-wrap items-center justify-between gap-3 border-t bg-muted/15 px-5 py-3 sm:px-6">
            <p className="text-[0.6875rem] text-muted-foreground" role="status">
              {reviewed
                ? "Review previewed. Nothing was deployed."
                : "Prototype · no deployment or SQL execution"}
            </p>
            <div className="ml-auto flex items-center gap-2">
              <Button variant="ghost" onClick={() => setOpen(false)}>
                Close
              </Button>
              <Button onClick={() => setReviewed(true)}>
                {reviewed ? <Check /> : <Rocket />}
                {reviewed ? "Review previewed" : "Preview deployment"}
              </Button>
            </div>
          </footer>
        </DialogContent>
      </Dialog>
    </main>
  );
}
