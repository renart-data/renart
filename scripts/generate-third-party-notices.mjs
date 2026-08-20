#!/usr/bin/env node

import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import {
  existsSync,
  mkdtempSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(scriptDir, "..");
const checkOnly = process.argv.includes("--check");
const noticesPath = path.join(root, "THIRD_PARTY_NOTICES.md");
const licensesDir = path.join(root, "third_party", "licenses");
const goExecutable =
  process.env.GO || (existsSync("/usr/local/go/bin/go") ? "/usr/local/go/bin/go" : "go");

const allowedWebLicenseExpressions = new Set([
  "(MPL-2.0 OR Apache-2.0)",
  "0BSD",
  "Apache-2.0",
  "BSD-2-Clause",
  "BSD-3-Clause",
  "BlueOak-1.0.0",
  "CC-BY-4.0",
  "ISC",
  "MIT",
  "MIT AND ISC",
  "Python-2.0",
  "Unlicense",
]);

const npmLicenseOverrides = new Map([
  ["react-remove-scroll-bar", path.join(root, "third_party", "npm", "react-remove-scroll-bar", "LICENSE")],
  ["victory-vendor", path.join(root, "third_party", "npm", "victory-vendor", "LICENSE")],
]);

const staticEmbeddedComponents = [
  {
    name: "shadcn/ui generated components",
    version: "shadcn CLI 4.13.1",
    source: "https://github.com/shadcn-ui/ui",
    files: [path.join(root, "third_party", "embedded", "shadcn", "LICENSE.md")],
  },
  {
    name: "Ruff Python intelligence engine",
    version: "commit 7287ad8623d11935a64d65ce0eb0a0755a5d87ca",
    source: "https://github.com/astral-sh/ruff",
    files: [path.join(root, "third_party", "embedded", "ruff", "LICENSE")],
  },
  {
    name: "Polyglot SQL WASM engine",
    version: "v0.6.2",
    source: "https://github.com/tobilg/polyglot",
    files: [path.join(root, "third_party", "embedded", "polyglot", "LICENSE")],
  },
  {
    name: "Inconsolata variable font",
    version: "@fontsource-variable/inconsolata 5.3.0",
    source: "https://fontsource.org/fonts/inconsolata",
    files: [path.join(root, "third_party", "embedded", "inconsolata", "OFL.txt")],
  },
];

function run(command, args, cwd = root, extraEnv = {}) {
  return execFileSync(command, args, {
    cwd,
    encoding: "utf8",
    env: { ...process.env, GOTOOLCHAIN: process.env.GOTOOLCHAIN || "go1.26.6", ...extraEnv },
    maxBuffer: 128 * 1024 * 1024,
    stdio: ["ignore", "pipe", "inherit"],
  });
}

function goModuleDetails(moduleName) {
  const output = run(goExecutable, ["list", "-m", "-f", "{{.Version}}\t{{.Dir}}", moduleName]).trim();
  const [version, directory] = output.split("\t");
  if (!version || !directory) throw new Error(`unable to resolve Go module ${moduleName}`);
  return { version, directory };
}

function normalizeLicenseText(value) {
  return value
    .replaceAll("\r\n", "\n")
    .replaceAll("\r", "\n")
    .replace(/[ \t]+$/gm, "")
    .replace(/\s*$/, "\n");
}

function findLicenseFiles(directory) {
  if (!directory || !existsSync(directory)) return [];
  return readdirSync(directory)
    .filter((name) => /^(licen[cs]e|copying|notice|copyright)(?:[._-].*)?$/i.test(name))
    .map((name) => path.join(directory, name))
    .filter((name) => statSync(name).isFile())
    .sort((a, b) => a.localeCompare(b));
}

function markdown(value) {
  return String(value).replaceAll("|", "\\|").replaceAll("\n", " ");
}

const generatedLicenses = new Map();

function registerLicense(file) {
  if (!existsSync(file)) throw new Error(`missing license file: ${path.relative(root, file)}`);
  const text = normalizeLicenseText(readFileSync(file, "utf8"));
  const hash = createHash("sha256").update(text).digest("hex");
  generatedLicenses.set(`${hash}.txt`, text);
  return `third_party/licenses/${hash}.txt`;
}

function collectGoModules() {
  const targets = [
    ["linux", "amd64"],
    ["linux", "arm64"],
    ["darwin", "amd64"],
    ["darwin", "arm64"],
    ["windows", "amd64"],
  ];
  const rows = new Set();
  for (const [goos, goarch] of targets) {
    const output = run(
      goExecutable,
      [
        "list",
        "-deps",
        // Release metadata is generated before web/dist exists. The webdev
        // variant only replaces the embed wrapper and keeps the Go dependency
        // graph used by the inventory unchanged.
        "-tags=no_duckdb_arrow,webdev",
        "-f",
        "{{if .Module}}{{if not .Module.Main}}{{.Module.Path}}\t{{.Module.Version}}\t{{.Module.Dir}}{{end}}{{end}}",
        ".",
      ],
      root,
      { GOOS: goos, GOARCH: goarch, CGO_ENABLED: "1" },
    );
    for (const row of output.split("\n").filter(Boolean)) rows.add(row);
  }
  return [...rows]
    .map((row) => {
      const [name, version, directory] = row.split("\t");
      const files = findLicenseFiles(directory);
      if (files.length === 0) throw new Error(`Go module ${name}@${version} has no root license or notice file`);
      return { name, version, licenses: files.map(registerLicense) };
    })
    .sort((a, b) => a.name.localeCompare(b.name) || a.version.localeCompare(b.version));
}

function collectEmbeddedComponents() {
  const bruin = goModuleDetails("github.com/bruin-data/bruin");
  const embeddedPython = goModuleDetails("github.com/kluctl/go-embed-python");

  const sqlglotData = path.join(bruin.directory, "internal", "data", "linux-amd64");
  const sqlglotDistInfo = readdirSync(sqlglotData).find((name) =>
    /^sqlglot-[0-9].*\.dist-info$/.test(name),
  );
  if (!sqlglotDistInfo) throw new Error(`unable to locate embedded SQLGlot metadata under ${sqlglotData}`);
  const sqlglotVersion = sqlglotDistInfo.slice("sqlglot-".length, -".dist-info".length);

  const pythonLib = path.join(embeddedPython.directory, "python", "internal", "data", "linux-amd64", "lib");
  const pythonVersionDir = readdirSync(pythonLib).find((name) => /^python[0-9]+\.[0-9]+$/.test(name));
  if (!pythonVersionDir) throw new Error(`unable to locate embedded CPython license under ${pythonLib}`);
  const pythonVersion = embeddedPython.version.match(/^v0\.0\.0-([0-9]+\.[0-9]+\.[0-9]+)-/)?.[1];

  return [
    ...staticEmbeddedComponents,
    {
      name: "SQLGlot embedded SQL engine",
      version: `${sqlglotVersion} (Bruin ${bruin.version})`,
      source: "https://github.com/tobymao/sqlglot",
      files: [path.join(sqlglotData, sqlglotDistInfo, "licenses", "LICENSE")],
    },
    {
      name: "CPython embedded runtime",
      version: `${pythonVersion || pythonVersionDir.slice("python".length)} (${embeddedPython.version})`,
      source: "https://www.python.org/",
      files: [path.join(pythonLib, pythonVersionDir, "LICENSE.txt")],
    },
  ];
}

function collectWebPackages() {
  const output = run("corepack", ["pnpm", "licenses", "list", "--prod", "--json"], path.join(root, "web"));
  const report = JSON.parse(output.slice(output.indexOf("{")));
  const packages = [];
  for (const [expression, entries] of Object.entries(report)) {
    if (!allowedWebLicenseExpressions.has(expression)) {
      throw new Error(`web dependency uses an unreviewed license expression: ${expression}`);
    }
    for (const entry of entries) {
      let files = findLicenseFiles(entry.paths?.[0]);
      if (files.length === 0 && npmLicenseOverrides.has(entry.name)) {
        files = [npmLicenseOverrides.get(entry.name)];
      }
      if (files.length === 0) {
        throw new Error(`web package ${entry.name}@${entry.versions?.join(",")} has no packaged license file or reviewed override`);
      }
      packages.push({
        name: entry.name,
        version: entry.versions?.join(", ") || "unknown",
        expression,
        licenses: files.map(registerLicense),
      });
    }
  }
  return packages.sort((a, b) => a.name.localeCompare(b.name) || a.version.localeCompare(b.version));
}

function licenseLinks(files) {
  return [...new Set(files)].map((file) => `[license](${file})`).join(", ");
}

function renderNotices(goModules, webPackages, embeddedComponents) {
  const lines = [
    "# Third-party notices",
    "",
    "Renart is licensed under Apache-2.0. This generated inventory records the",
    "license and notice texts shipped with or referenced by its release artifacts.",
    "Run `node scripts/generate-third-party-notices.mjs` after dependency changes;",
    "CI verifies that the checked-in inventory is current.",
    "",
    "## Embedded assets",
    "",
  ];
  for (const component of embeddedComponents) {
    const licenses = component.files.map(registerLicense);
    lines.push(`- [${markdown(component.name)}](${component.source}) ${markdown(component.version)} — ${licenseLinks(licenses)}`);
  }
  lines.push("", "## Go modules", "", "| Module | Version | License and notice texts |", "| --- | --- | --- |");
  for (const module of goModules) {
    lines.push(`| ${markdown(module.name)} | ${markdown(module.version)} | ${licenseLinks(module.licenses)} |`);
  }
  lines.push("", "## Web application packages", "", "| Package | Version | Declared license | License and notice texts |", "| --- | --- | --- | --- |");
  for (const pkg of webPackages) {
    lines.push(`| ${markdown(pkg.name)} | ${markdown(pkg.version)} | ${markdown(pkg.expression)} | ${licenseLinks(pkg.licenses)} |`);
  }
  lines.push("");
  return lines.join("\n");
}

function expectedFiles(notices) {
  return new Map([
    [path.relative(root, noticesPath), notices],
    ...[...generatedLicenses.entries()].map(([name, text]) => [path.join("third_party", "licenses", name), text]),
  ]);
}

function verify(files) {
  const differences = [];
  for (const [relative, expected] of files) {
    const absolute = path.join(root, relative);
    if (!existsSync(absolute) || readFileSync(absolute, "utf8") !== expected) differences.push(relative);
  }
  const expectedLicenseNames = new Set([...generatedLicenses.keys()]);
  if (existsSync(licensesDir)) {
    for (const name of readdirSync(licensesDir)) {
      if (!expectedLicenseNames.has(name)) differences.push(path.join("third_party", "licenses", name));
    }
  }
  if (differences.length > 0) {
    throw new Error(`third-party notices are stale:\n${[...new Set(differences)].sort().map((item) => `  ${item}`).join("\n")}\nrun node scripts/generate-third-party-notices.mjs`);
  }
}

function write(files) {
  const staging = mkdtempSync(path.join(tmpdir(), "renart-notices-"));
  try {
    for (const [relative, contents] of files) {
      const target = path.join(staging, relative);
      mkdirSync(path.dirname(target), { recursive: true });
      writeFileSync(target, contents);
    }
    rmSync(licensesDir, { recursive: true, force: true });
    for (const [relative, contents] of files) {
      const target = path.join(root, relative);
      mkdirSync(path.dirname(target), { recursive: true });
      writeFileSync(target, contents);
    }
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }
}

const goModules = collectGoModules();
const webPackages = collectWebPackages();
const embeddedComponents = collectEmbeddedComponents();
const notices = renderNotices(goModules, webPackages, embeddedComponents);
const files = expectedFiles(notices);

if (checkOnly) {
  verify(files);
  console.log(
    `Third-party notices are current (${goModules.length} Go modules, ${webPackages.length} web packages).`,
  );
} else {
  write(files);
  console.log(
    `Generated third-party notices for ${goModules.length} Go modules and ${webPackages.length} web packages.`,
  );
}
