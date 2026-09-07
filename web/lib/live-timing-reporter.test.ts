import { afterEach, describe, expect, it } from "vitest";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { FullConfig, Reporter, Suite, TestCase, TestResult } from "@playwright/test/reporter";
import LiveTimingReporter from "../tests/e2e/live-timing-reporter";

const directories: string[] = [];
afterEach(() => {
  for (const path of directories.splice(0)) rmSync(path, { recursive: true, force: true });
});
const testCase = {
  titlePath: () => ["", "test"],
  location: { file: "test.live.spec.ts" },
  parent: { project: () => ({ name: "chromium-live" }) },
} as unknown as TestCase;
const result = {
  attachments: [],
  duration: 12,
  retry: 0,
  status: "passed",
} as unknown as TestResult;
function setup() {
  const outputDir = mkdtempSync(join(tmpdir(), "renart-timing-test-"));
  directories.push(outputDir);
  const reporter: Reporter = new LiveTimingReporter({ outputDir });
  reporter.onBegin?.({} as FullConfig, {} as Suite);
  return { reporter, outputDir };
}

describe("live timing checkpoints", () => {
  it("retains completed attempts even if the process never reaches onEnd", () => {
    const { reporter, outputDir } = setup();
    reporter.onTestEnd?.(testCase, result);
    const journal = join(outputDir, "live-timings.jsonl");
    expect(existsSync(journal)).toBe(true);
    expect(JSON.parse(readFileSync(journal, "utf8").trim())).toMatchObject({
      status: "passed",
      totalMs: 12,
      project: "chromium-live",
    });
    expect(existsSync(join(outputDir, "live-timings.json"))).toBe(false);
  });

  it("starts a new journal without retaining a previous run's completion reports", async () => {
    const { reporter, outputDir } = setup();
    reporter.onTestEnd?.(testCase, result);
    await reporter.onEnd?.({ status: "passed", startTime: new Date(), duration: 12 });
    expect(existsSync(join(outputDir, "live-timings.json"))).toBe(true);
    const next: Reporter = new LiveTimingReporter({ outputDir });
    next.onBegin?.({} as FullConfig, {} as Suite);
    expect(existsSync(join(outputDir, "live-timings.jsonl"))).toBe(true);
    expect(readFileSync(join(outputDir, "live-timings.jsonl"), "utf8")).toBe("");
    expect(existsSync(join(outputDir, "live-timings.json"))).toBe(false);
    expect(existsSync(join(outputDir, "live-timings.md"))).toBe(false);
  });
});
