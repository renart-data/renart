import { readFileSync, statSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";

const webRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const distRoot = resolve(webRoot, "dist");
const manifestPath = resolve(distRoot, ".vite/manifest.json");
const budgetsPath = resolve(webRoot, "bundle-budgets.json");

const manifest = readJSON(manifestPath);
const budgets = readJSON(budgetsPath);
const violations = [];
const initialKeys = Object.entries(manifest)
  .filter(([, chunk]) => chunk.isEntry)
  .map(([key]) => key);
if (initialKeys.length === 0) {
  fail(`No Vite entry was found in ${relative(webRoot, manifestPath)}.`);
}

const initialFiles = collectStaticFiles(manifest, initialKeys);
const initial = measureFiles(initialFiles);
const families = {};

checkBudget("initial JavaScript", initial.javascript.rawBytes, budgets.initial.maxJavaScriptBytes);
checkBudget("initial CSS", initial.css.rawBytes, budgets.initial.maxCssBytes);

for (const [name, budget] of Object.entries(budgets.families)) {
  const roots = Object.entries(manifest)
    .filter(
      ([key, chunk]) =>
        chunk.isDynamicEntry && budget.matches.some((pattern) => key.includes(pattern)),
    )
    .map(([key]) => key);
  if (roots.length === 0) {
    violations.push(`${name}: no dynamic manifest entries matched ${budget.matches.join(", ")}`);
    continue;
  }
  const familyFiles = collectStaticFiles(manifest, roots);
  for (const file of initialFiles) {
    familyFiles.delete(file);
  }
  const measured = measureFiles(familyFiles);
  families[name] = { roots, ...measured };
  checkBudget(
    `${name} incremental JavaScript`,
    measured.javascript.rawBytes,
    budget.maxJavaScriptBytes,
  );
  checkBudget(`${name} incremental CSS`, measured.css.rawBytes, budget.maxCssBytes);
}

const chunks = Object.values(manifest)
  .map((chunk) => chunk.file)
  .filter((file) => file?.endsWith(".js"))
  .filter((file, index, files) => files.indexOf(file) === index)
  .map((file) => measureFile(file))
  .sort((left, right) => right.rawBytes - left.rawBytes);
for (const chunk of chunks) {
  checkBudget(`JavaScript chunk ${chunk.file}`, chunk.rawBytes, budgets.maxJavaScriptChunkBytes);
}

const report = {
  generatedAt: new Date().toISOString(),
  budgets,
  initial,
  families,
  chunks,
  violations,
};
writeFileSync(
  resolve(distRoot, "bundle-report.json"),
  `${JSON.stringify(report, null, 2)}\n`,
  "utf8",
);
writeFileSync(resolve(distRoot, "bundle-report.md"), renderMarkdown(report), "utf8");

process.stdout.write(renderConsoleSummary(report));
if (violations.length > 0) {
  fail(`Bundle budgets failed:\n- ${violations.join("\n- ")}`);
}

function collectStaticFiles(entries, roots) {
  const visited = new Set();
  const files = new Set();
  const visit = (key) => {
    if (visited.has(key)) {
      return;
    }
    visited.add(key);
    const chunk = entries[key];
    if (!chunk) {
      violations.push(`Manifest import ${key} could not be resolved.`);
      return;
    }
    if (chunk.file) {
      files.add(chunk.file);
    }
    for (const cssFile of chunk.css ?? []) {
      files.add(cssFile);
    }
    for (const imported of chunk.imports ?? []) {
      visit(imported);
    }
  };
  for (const root of roots) {
    visit(root);
  }
  return files;
}

function measureFiles(files) {
  const measured = [...files].map((file) => measureFile(file));
  return {
    javascript: summarize(measured.filter((file) => file.file.endsWith(".js"))),
    css: summarize(measured.filter((file) => file.file.endsWith(".css"))),
    files: measured.sort((left, right) => right.rawBytes - left.rawBytes),
  };
}

function measureFile(file) {
  const absolute = resolve(distRoot, file);
  const localPath = relative(distRoot, absolute);
  if (localPath === ".." || localPath.startsWith(`..${sep}`) || isAbsolute(localPath)) {
    fail(`Manifest file escaped dist: ${file}`);
  }
  const contents = readFileSync(absolute);
  return {
    file,
    rawBytes: statSync(absolute).size,
    gzipBytes: gzipSync(contents, { level: 9 }).length,
  };
}

function summarize(files) {
  return {
    fileCount: files.length,
    rawBytes: files.reduce((sum, file) => sum + file.rawBytes, 0),
    gzipBytes: files.reduce((sum, file) => sum + file.gzipBytes, 0),
  };
}

function checkBudget(name, actual, maximum) {
  if (!Number.isFinite(maximum) || maximum <= 0) {
    violations.push(`${name}: invalid budget ${maximum}`);
    return;
  }
  if (actual > maximum) {
    violations.push(`${name}: ${formatBytes(actual)} exceeds ${formatBytes(maximum)}`);
  }
}

function renderConsoleSummary(report) {
  const lines = [
    `Bundle initial: ${formatBytes(report.initial.javascript.rawBytes)} JS / ${formatBytes(report.initial.css.rawBytes)} CSS`,
  ];
  for (const [name, family] of Object.entries(report.families)) {
    lines.push(
      `Bundle ${name}: +${formatBytes(family.javascript.rawBytes)} JS / +${formatBytes(family.css.rawBytes)} CSS`,
    );
  }
  lines.push(
    `Largest chunk: ${report.chunks[0]?.file ?? "none"} (${formatBytes(report.chunks[0]?.rawBytes ?? 0)})`,
  );
  return `${lines.join("\n")}\n`;
}

function renderMarkdown(report) {
  const lines = [
    "# Frontend bundle report",
    "",
    "Raw minified bytes are budgeted; gzip sizes are recorded for diagnosis.",
    "Lazy-family values exclude the initial static dependency closure.",
    "",
    "| Surface | JavaScript | JS budget | CSS | CSS budget |",
    "| --- | ---: | ---: | ---: | ---: |",
    `| Initial shell | ${formatBytes(report.initial.javascript.rawBytes)} | ${formatBytes(report.budgets.initial.maxJavaScriptBytes)} | ${formatBytes(report.initial.css.rawBytes)} | ${formatBytes(report.budgets.initial.maxCssBytes)} |`,
  ];
  for (const [name, family] of Object.entries(report.families)) {
    const budget = report.budgets.families[name];
    lines.push(
      `| ${name} incremental | ${formatBytes(family.javascript.rawBytes)} | ${formatBytes(budget.maxJavaScriptBytes)} | ${formatBytes(family.css.rawBytes)} | ${formatBytes(budget.maxCssBytes)} |`,
    );
  }
  lines.push(
    "",
    "## Largest JavaScript chunks",
    "",
    "| Chunk | Raw | Gzip |",
    "| --- | ---: | ---: |",
  );
  for (const chunk of report.chunks.slice(0, 15)) {
    lines.push(
      `| ${chunk.file} | ${formatBytes(chunk.rawBytes)} | ${formatBytes(chunk.gzipBytes)} |`,
    );
  }
  if (report.violations.length > 0) {
    lines.push("", "## Violations", "", ...report.violations.map((item) => `- ${item}`));
  }
  return `${lines.join("\n")}\n`;
}

function formatBytes(value) {
  if (value < 1024) {
    return `${value} B`;
  }
  return `${(value / 1024).toFixed(1)} KiB`;
}

function readJSON(path) {
  try {
    return JSON.parse(readFileSync(path, "utf8"));
  } catch (error) {
    fail(`Could not read ${relative(webRoot, path)}: ${error.message}`);
  }
}

function fail(message) {
  process.stderr.write(`${message}\n`);
  process.exit(1);
}
