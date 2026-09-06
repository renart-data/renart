import { access, readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import sharp from "sharp";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const content = path.join(root, "src/content/docs");
const files = (await readdir(content, { recursive: true })).filter((file) => /\.mdx?$/.test(file));
const failures = [];
const exists = (file) =>
  access(file).then(
    () => true,
    () => false,
  );
const imageSizes = new Map();
for (const file of files) {
  const text = await readFile(path.join(content, file), "utf8");
  const prose = text.replace(/```[^\n]*\n[\s\S]*?```/g, "");
  const fail = (message) => failures.push(`${file}: ${message}`);
  if (!/^---\n[\s\S]*?\btitle: [^\n]+\n[\s\S]*?\bdescription: [^\n]+\n[\s\S]*?---/.test(text))
    fail("title and description frontmatter are required");
  if (/^# /m.test(prose)) fail("Starlight supplies the H1; do not duplicate it");
  if (/bruin/i.test(text)) fail("Use Renart terminology and UI-authored examples");
  if (
    /six main views|\*\*Build stale\*\*|\*\*Test response\*\*|\*\*Apply inferred columns\*\*/i.test(
      prose,
    )
  )
    fail("Stale UI label");
  for (const match of text.matchAll(/(?:\]\(|href=["'])(\/docs\/[^\s)"']*)/g)) {
    const url = new URL(match[1], "https://getrenart.com");
    const relative = decodeURIComponent(url.pathname).replace(/^\//, "").replace(/\/$/, "");
    if (
      !(await exists(path.join(content, `${relative}.mdx`))) &&
      !(await exists(path.join(content, relative, "index.mdx")))
    )
      fail(`Broken docs link: ${match[1]}`);
  }
  for (const match of text.matchAll(/<img\s[\s\S]*?\/?\s*>/g)) {
    const tag = match[0];
    const source = tag.match(/src="([^"]+)"/)?.[1];
    if (!source?.startsWith("/")) continue;
    if (!/alt="[^"\n]+"/.test(tag)) fail(`${source}: descriptive alt text required`);
    try {
      if (!imageSizes.has(source))
        imageSizes.set(source, await sharp(path.join(root, "public", source)).metadata());
      const metadata = imageSizes.get(source);
      if (
        Number(tag.match(/width="(\d+)"/)?.[1]) !== metadata.width ||
        Number(tag.match(/height="(\d+)"/)?.[1]) !== metadata.height
      )
        fail(`${source}: dimensions must match ${metadata.width}x${metadata.height}`);
    } catch (error) {
      fail(`${source}: ${error.message}`);
    }
  }
}

// Run with --check-code in a full checkout. The static Docker build only copies docs.
if (process.argv.includes("--check-code")) {
  const source = await readFile(path.join(root, "../internal/web/service/direct_run.go"), "utf8");
  const registry = source.split("var directRunAssetTypes =")[1]?.split("\n}")[0] ?? "";
  const labels = {
    DuckDB: "DuckDB",
    Motherduck: "MotherDuck",
    Postgres: "PostgreSQL",
    Redshift: "Amazon Redshift",
    Bigquery: "Google BigQuery",
    Athena: "Amazon Athena",
    Databricks: "Databricks",
    Fabric: "Microsoft Fabric",
    MySQL: "MySQL",
    Snowflake: "Snowflake",
    MsSQL: "Microsoft SQL Server",
    Synapse: "Azure Synapse",
    ClickHouse: "ClickHouse",
    StarRocks: "StarRocks",
    Trino: "Trino",
    Vertica: "Vertica",
    Oracle: "Oracle",
  };
  const platforms = await readFile(
    path.join(content, "docs/reference/supported-platforms.mdx"),
    "utf8",
  );
  const types = [...registry.matchAll(/pipeline\.AssetType(\w+Query|ClickHouse):\s*\{\}/g)].map(
    (m) => m[1].replace(/Query$/, ""),
  );
  if (!types.length) failures.push("Could not find the SQL executor registry");
  for (const type of types) {
    if (!labels[type] || !platforms.includes(`| ${labels[type]} |`))
      failures.push(`SQL platform missing from reference: ${type}`);
  }
  const server = await readFile(path.join(root, "../cmd/server.go"), "utf8");
  const cli = await readFile(path.join(content, "docs/reference/cli.mdx"), "utf8");
  if (
    /Name:\s*"scheduler",\s*Value:\s*true/.test(server) &&
    (cli.match(/\| `--scheduler` \| on \|/g)?.length ?? 0) !== 2
  )
    failures.push("Scheduler defaults drifted from the CLI");
}

if (failures.length) {
  console.error(failures.join("\n"));
  process.exitCode = 1;
} else
  console.log(
    `Docs content checks passed: ${files.length} pages, ${imageSizes.size} images${process.argv.includes("--check-code") ? ", SQL registry and CLI defaults" : ""}.`,
  );
