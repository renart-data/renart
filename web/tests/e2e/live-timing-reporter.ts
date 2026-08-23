import { mkdirSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import type { FullResult, Reporter, TestCase, TestResult } from "@playwright/test/reporter";

type ReporterOptions = {
  outputDir?: string;
  slowest?: number;
};

type FixtureTimings = {
  fixture?: string;
  workspaceSetupMs?: number;
  serverStartupMs?: number;
  testBodyMs?: number;
  serverTeardownMs?: number;
  serverReady?: boolean;
};

type TimingEntry = FixtureTimings & {
  project: string;
  file: string;
  title: string;
  retry: number;
  status: TestResult["status"];
  totalMs: number;
  otherMs: number;
};

export default class LiveTimingReporter implements Reporter {
  private readonly outputDir: string;
  private readonly slowest: number;
  private readonly entries: TimingEntry[] = [];

  constructor(options: ReporterOptions = {}) {
    this.outputDir = resolve(options.outputDir ?? "test-results/live");
    this.slowest = positiveInteger(options.slowest, 50);
  }

  printsToStdio() {
    return false;
  }

  onTestEnd(test: TestCase, result: TestResult) {
    const fixture = parseFixtureTimings(result);
    const measuredMs =
      numberValue(fixture.workspaceSetupMs) +
      numberValue(fixture.serverStartupMs) +
      numberValue(fixture.testBodyMs) +
      numberValue(fixture.serverTeardownMs);
    const titlePath = test.titlePath();

    this.entries.push({
      ...fixture,
      project: test.parent.project()?.name ?? titlePath[0] ?? "unknown",
      file: test.location.file,
      title: titlePath.slice(1).join(" › ") || test.title,
      retry: result.retry,
      status: result.status,
      totalMs: result.duration,
      otherMs: Math.max(0, result.duration - measuredMs),
    });
  }

  onEnd(result: FullResult) {
    const entries = [...this.entries].sort((left, right) => right.totalMs - left.totalMs);
    const report = {
      generatedAt: new Date().toISOString(),
      status: result.status,
      attempts: entries.length,
      phases: summarizePhases(entries),
      slowest: entries.slice(0, this.slowest),
      entries,
    };

    mkdirSync(this.outputDir, { recursive: true });
    writeFileSync(
      resolve(this.outputDir, "live-timings.json"),
      `${JSON.stringify(report, null, 2)}\n`,
      "utf8",
    );
    writeFileSync(
      resolve(this.outputDir, "live-timings.md"),
      renderMarkdown(report.status, entries, this.slowest),
      "utf8",
    );
  }
}

function parseFixtureTimings(result: TestResult): FixtureTimings {
  const attachment = [...result.attachments]
    .reverse()
    .find((candidate) => candidate.name === "live-app-timings" && candidate.body);
  if (!attachment?.body) {
    return {};
  }
  try {
    const decoded = JSON.parse(attachment.body.toString("utf8")) as FixtureTimings;
    return {
      fixture: typeof decoded.fixture === "string" ? decoded.fixture : undefined,
      workspaceSetupMs: optionalNumber(decoded.workspaceSetupMs),
      serverStartupMs: optionalNumber(decoded.serverStartupMs),
      testBodyMs: optionalNumber(decoded.testBodyMs),
      serverTeardownMs: optionalNumber(decoded.serverTeardownMs),
      serverReady: typeof decoded.serverReady === "boolean" ? decoded.serverReady : undefined,
    };
  } catch {
    return {};
  }
}

function summarizePhases(entries: TimingEntry[]) {
  return {
    total: summarize(entries.map((entry) => entry.totalMs)),
    workspaceSetup: summarize(entries.map((entry) => entry.workspaceSetupMs)),
    serverStartup: summarize(entries.map((entry) => entry.serverStartupMs)),
    testBody: summarize(entries.map((entry) => entry.testBodyMs)),
    serverTeardown: summarize(entries.map((entry) => entry.serverTeardownMs)),
    otherFixtures: summarize(entries.map((entry) => entry.otherMs)),
  };
}

function summarize(values: Array<number | undefined>) {
  const present = values
    .filter((value): value is number => typeof value === "number" && Number.isFinite(value))
    .sort((left, right) => left - right);
  if (present.length === 0) {
    return { count: 0, totalMs: 0, medianMs: 0, p95Ms: 0, maxMs: 0 };
  }
  return {
    count: present.length,
    totalMs: present.reduce((sum, value) => sum + value, 0),
    medianMs: percentile(present, 0.5),
    p95Ms: percentile(present, 0.95),
    maxMs: present[present.length - 1],
  };
}

function percentile(sorted: number[], quantile: number) {
  return sorted[Math.min(sorted.length - 1, Math.ceil(sorted.length * quantile) - 1)];
}

function renderMarkdown(status: FullResult["status"], entries: TimingEntry[], limit: number) {
  const summary = summarizePhases(entries);
  const lines = [
    "# Live E2E timings",
    "",
    `Run status: **${status}**. Attempts: **${entries.length}**.`,
    "",
    "## Phase summary",
    "",
    "| Phase | Samples | Total | Median | p95 | Max |",
    "| --- | ---: | ---: | ---: | ---: | ---: |",
  ];
  for (const [name, phase] of Object.entries(summary)) {
    lines.push(
      `| ${name} | ${phase.count} | ${formatDuration(phase.totalMs)} | ${formatDuration(phase.medianMs)} | ${formatDuration(phase.p95Ms)} | ${formatDuration(phase.maxMs)} |`,
    );
  }
  lines.push(
    "",
    `## Slowest ${Math.min(limit, entries.length)} attempts`,
    "",
    "| Test | Project | Retry | Status | Total | Setup | Server | Body | Teardown | Other fixtures |",
    "| --- | --- | ---: | --- | ---: | ---: | ---: | ---: | ---: | ---: |",
  );
  for (const entry of entries.slice(0, limit)) {
    lines.push(
      `| ${escapeMarkdown(entry.title)} | ${escapeMarkdown(entry.project)} | ${entry.retry} | ${entry.status} | ${formatDuration(entry.totalMs)} | ${formatDuration(entry.workspaceSetupMs)} | ${formatDuration(entry.serverStartupMs)} | ${formatDuration(entry.testBodyMs)} | ${formatDuration(entry.serverTeardownMs)} | ${formatDuration(entry.otherMs)} |`,
    );
  }
  return `${lines.join("\n")}\n`;
}

function optionalNumber(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) && value >= 0 ? value : undefined;
}

function numberValue(value: number | undefined) {
  return value ?? 0;
}

function positiveInteger(value: number | undefined, fallback: number) {
  return typeof value === "number" && Number.isInteger(value) && value > 0 ? value : fallback;
}

function formatDuration(value: number | undefined) {
  if (value === undefined) {
    return "—";
  }
  if (value < 1000) {
    return `${Math.round(value)} ms`;
  }
  return `${(value / 1000).toFixed(1)} s`;
}

function escapeMarkdown(value: string) {
  return value.replaceAll("|", "\\|").replaceAll("\n", " ");
}
