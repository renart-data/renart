#!/usr/bin/env node

import { existsSync, mkdirSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { loadEvaluationManifest, runEvaluation } from "./notebook-agent-eval-lib.mjs";

const scriptDir = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(scriptDir, "..");

function usage() {
  process.stdout.write(`Notebook agent evaluation

Usage:
  node scripts/notebook-agent-eval.mjs [options]

Options:
  --provider <fake|codex|claude|opencode>  Provider to exercise (default: fake)
  --task <id>                              Run one task; repeat to select several
  --all                                    Run all authenticated-provider tasks
  --binary <path>                          Current Renart binary (default: ./renart)
  --agent-binary <path>                    Provider CLI override
  --output <path>                          Artifact directory under .tmp by default
  --manifest <path>                        Evaluation task manifest
  --model <label>                          Evidence label for provider-default model
  --keep-workspaces                        Keep temporary Git workspaces for debugging
  --list                                   List task IDs and exit
  --help                                   Show this help

The fake provider runs only deterministic harness scenarios. Authenticated
providers are opt-in and run the complete corpus unless --task narrows it.
`);
}

function parseArguments(argv) {
  const timestamp = new Date().toISOString().replaceAll(":", "-").replaceAll(".", "-");
  const options = {
    provider: "fake",
    taskIDs: [],
    binaryPath: resolve(repoRoot, "renart"),
    agentBinary: undefined,
    outputDir: resolve(repoRoot, ".tmp", "notebook-agent-eval", timestamp),
    manifestPath: resolve(repoRoot, "testdata", "notebook-agent-eval", "tasks.json"),
    model: undefined,
    keepWorkspaces: false,
    list: false,
    all: false,
  };
  for (let index = 0; index < argv.length; index++) {
    const argument = argv[index];
    const value = () => {
      const next = argv[++index];
      if (!next) throw new Error(`${argument} requires a value`);
      return next;
    };
    switch (argument) {
      case "--provider":
        options.provider = value();
        break;
      case "--task":
        options.taskIDs.push(value());
        break;
      case "--binary":
        options.binaryPath = resolve(value());
        break;
      case "--agent-binary":
        options.agentBinary = resolve(value());
        break;
      case "--output":
        options.outputDir = resolve(value());
        break;
      case "--manifest":
        options.manifestPath = resolve(value());
        break;
      case "--model":
        options.model = value();
        break;
      case "--keep-workspaces":
        options.keepWorkspaces = true;
        break;
      case "--all":
        options.all = true;
        break;
      case "--list":
        options.list = true;
        break;
      case "--help":
      case "-h":
        usage();
        process.exit(0);
        break;
      default:
        throw new Error(`unknown option: ${argument}`);
    }
  }
  if (!["fake", "codex", "claude", "opencode"].includes(options.provider)) {
    throw new Error("--provider must be fake, codex, claude, or opencode");
  }
  if (options.all && options.taskIDs.length > 0) {
    throw new Error("choose --all or one or more --task values, not both");
  }
  return options;
}

async function main() {
  const options = parseArguments(process.argv.slice(2));
  const manifest = loadEvaluationManifest(options.manifestPath);
  if (options.list) {
    for (const task of manifest.tasks) {
      process.stdout.write(`${task.id}\t${task.title}${task.fake_supported ? "\t[fake]" : ""}\n`);
    }
    return;
  }
  if (!existsSync(options.binaryPath)) {
    throw new Error(`Renart binary not found: ${options.binaryPath}; run make go-build or use --binary`);
  }
  if (!options.agentBinary && options.provider !== "fake") {
    options.agentBinary = options.provider;
  }
  options.repoRoot = repoRoot;
  options.staticDir = resolve(repoRoot, "web", "dist");
  options.fakeProviderPath = resolve(repoRoot, "web", "tests", "fixtures", "fake-codex-notebook-agent");
  mkdirSync(options.outputDir, { recursive: true });
  const report = await runEvaluation({
    ...options,
    onProgress: (message) => process.stdout.write(`${message}\n`),
  });
  process.stdout.write(`\n${report.passed ? "PASS" : "FAIL"}: ${options.outputDir}\n`);
  process.exitCode = report.passed ? 0 : 1;
}

main().catch((error) => {
  process.stderr.write(`${error instanceof Error ? error.stack ?? error.message : error}\n`);
  process.exitCode = 2;
});
