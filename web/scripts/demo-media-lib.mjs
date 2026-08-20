// Shared machinery for the landing-page and docs media pipelines: builds the
// demo "acme analytics" workspace, starts a renart server against it, stages
// realistic state via the HTTP API, and provides capture/convert helpers.
//
// Used by capture-landing-media.mjs (make landing-media) and
// capture-docs-media.mjs (make docs-media).
import { execFile, spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdtemp, readFile, rm, unlink, writeFile } from "node:fs/promises";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import {
  createAcmeWorkspace,
  addMarketingPipeline,
  addStalenessEdits,
} from "./landing-media-workspace.mjs";

export const repoRoot = path.resolve(import.meta.dirname, "..", "..");
export const webRoot = path.resolve(import.meta.dirname, "..");

export const id = (repoRelPath) => Buffer.from(repoRelPath).toString("base64url");
export const ACME = id("acme");
export const MARKETING = id("marketing");
export const STAGING_ORDERS = id("acme/assets/staging/orders.sql");

// sharp lives in docs/ (the docs site needs it anyway).
const docsRequire = createRequire(path.join(repoRoot, "docs", "package.json"));
const execFileAsync = promisify(execFile);

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function buildSQLParserLinkShim(goBinary) {
  const { stdout: goEnv } = await execFileAsync(goBinary, ["env", "GOOS", "GOARCH"], {
    cwd: repoRoot,
  });
  const [goos, goarch] = goEnv.trim().split(/\s+/);
  if (!goos || !goarch) {
    throw new Error(`could not determine the Go host target from: ${JSON.stringify(goEnv)}`);
  }

  const { stdout } = await execFileAsync(
    path.join(repoRoot, "scripts", "build_bruin_sqlparser_stub.sh"),
    [`${goos}-${goarch}`],
    { cwd: repoRoot, env: process.env },
  );
  const libDir = stdout.trim().split(/\r?\n/).at(-1);
  if (!libDir) {
    throw new Error("Bruin SQL parser link shim did not report its library directory");
  }
  return libDir;
}

// Builds the demo workspace, boots a renart server on `port`, and stages the
// full demo state. Returns a handle with API helpers and `stop()`.
export async function launchStagedDemo({ port }) {
  const baseURL = `http://127.0.0.1:${port}`;
  const goBinary =
    process.env.GO_BIN ?? (existsSync("/usr/local/go/bin/go") ? "/usr/local/go/bin/go" : "go");
  const sqlParserLibDir = await buildSQLParserLinkShim(goBinary);
  const cgoLdflags = [`-L${sqlParserLibDir}`, process.env.CGO_LDFLAGS].filter(Boolean).join(" ");
  // nested so the project switcher in the shots shows "acme-ws", not a temp name
  const tempRoot = await mkdtemp(path.join(tmpdir(), "renart-demo-media-"));
  const workspaceDir = path.join(tempRoot, "acme-ws");

  await createAcmeWorkspace(workspaceDir);

  const server = spawn(
    goBinary,
    [
      "run",
      ".",
      "web",
      workspaceDir,
      "--host",
      "127.0.0.1",
      "--port",
      String(port),
      "--static-dir",
      path.join(webRoot, "dist"),
      "--watch-mode",
      "poll",
      "--no-open",
    ],
    {
      cwd: repoRoot,
      detached: true,
      env: { ...process.env, CGO_LDFLAGS: cgoLdflags },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  let serverOutput = "";
  let serverExit;
  server.stdout.on("data", (chunk) => (serverOutput += chunk.toString()));
  server.stderr.on("data", (chunk) => (serverOutput += chunk.toString()));
  server.once("error", (error) => {
    serverExit = `failed to start: ${error.message}`;
  });
  server.once("exit", (code, signal) => {
    serverExit = signal ? `exited from signal ${signal}` : `exited with code ${code}`;
  });

  async function api(pathname, { method = "GET", body } = {}) {
    const response = await fetch(baseURL + pathname, {
      method,
      headers: { Origin: baseURL, "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    const text = await response.text();
    if (!response.ok) {
      throw new Error(`${method} ${pathname} -> ${response.status}: ${text.slice(0, 300)}`);
    }
    return text ? JSON.parse(text) : {};
  }

  // Drains the materialize SSE stream and fails on a non-success final event.
  async function materializeStream(pathname) {
    const response = await fetch(baseURL + pathname, {
      method: "POST",
      headers: { Origin: baseURL },
    });
    if (!response.ok) {
      throw new Error(`POST ${pathname} -> ${response.status}`);
    }
    const raw = await response.text();
    const doneEvent = raw
      .split("\n\n")
      .filter((event) => event.includes("event: done"))
      .at(-1);
    if (!doneEvent || !/"status"\s*:\s*"(ok|success)"/.test(doneEvent)) {
      throw new Error(`materialization did not succeed:\n${raw.slice(-800)}`);
    }
  }

  async function waitForPipeline(name) {
    const deadline = Date.now() + 60_000;
    while (Date.now() < deadline) {
      const workspace = await api("/api/workspace").catch(() => null);
      if (workspace?.pipelines?.some((pipeline) => pipeline.name === name)) {
        return;
      }
      await sleep(1000);
    }
    throw new Error(`pipeline ${name} was not discovered in time`);
  }

  async function triggerRun(pipelineId, environment) {
    const before = (await api("/api/runs")).runs?.length ?? 0;
    await api(`/api/pipelines/${pipelineId}/trigger`, {
      method: "POST",
      body: { environment },
    });
    const deadline = Date.now() + 120_000;
    while (Date.now() < deadline) {
      const { runs = [] } = await api("/api/runs");
      const settled = runs.every((run) => !["running", "pending", "queued"].includes(run.status));
      if (runs.length > before && settled) {
        return;
      }
      await sleep(1000);
    }
    throw new Error(`run of ${pipelineId} (${environment}) did not settle in time`);
  }

  // wait for the server
  const deadline = Date.now() + 180_000;
  for (;;) {
    if (serverExit) {
      await rm(tempRoot, { recursive: true, force: true }).catch(() => undefined);
      throw new Error(`renart server ${serverExit} before startup.\n${serverOutput}`);
    }
    if (Date.now() > deadline) {
      throw new Error(`renart server did not start in time.\n${serverOutput}`);
    }
    const ok = await fetch(`${baseURL}/api/workspace`, { headers: { Origin: baseURL } })
      .then((response) => response.ok)
      .catch(() => false);
    if (ok) {
      break;
    }
    await sleep(500);
  }
  await waitForPipeline("acme");

  // --- staged state ---------------------------------------------------------

  console.log("staging: materializing acme…");
  await materializeStream(`/api/pipelines/${ACME}/materialize/stream`);

  // A believable run history: scheduled + manual + production runs, and one
  // failed scheduled run (a briefly broken column reference in
  // staging.order_items) whose binder error shows in the run-detail event log.
  console.log("staging: recording run history…");
  await triggerRun(ACME, "default");
  await triggerRun(ACME, "default");
  await triggerRun(ACME, "default");
  await triggerRun(ACME, "production");
  const orderItems = path.join(workspaceDir, "acme", "assets", "staging", "order_items.sql");
  const orderItemsOriginal = await readFile(orderItems, "utf8");
  const breakNeedle = "i.quantity * i.unit_price AS line_total";
  if (!orderItemsOriginal.includes(breakNeedle)) {
    throw new Error(`failed-run edit: expected '${breakNeedle}' in ${orderItems}`);
  }
  await writeFile(
    orderItems,
    orderItemsOriginal.replace(breakNeedle, "i.quantity * i.unit_pricee AS line_total"),
  );
  await sleep(3000); // let the poll watcher pick up the broken file
  await triggerRun(ACME, "default");
  await writeFile(orderItems, orderItemsOriginal);
  await sleep(3000);
  await triggerRun(ACME, "default"); // leave everything fresh again

  console.log("staging: creating env schedules…");
  await api(`/api/pipelines/${ACME}/env-schedules/default`, {
    method: "PUT",
    body: {
      cron: "0 6 * * *",
      timezone: "Europe/Berlin",
      catchup_policy: "skip",
      deploy_now: true,
    },
  });
  // Every environment schedule pins an exact deployed snapshot.
  await api(`/api/pipelines/${ACME}/env-schedules/production`, {
    method: "PUT",
    body: {
      cron: "30 5 * * 1-5",
      timezone: "Europe/Berlin",
      catchup_policy: "run_once",
      deploy_now: true,
    },
  });

  console.log("staging: building notebook…");
  const notebookId = await buildNotebook(api);

  console.log("staging: building presentations…");
  const { dashboardId, reportId } = await buildPresentations(api);

  console.log("staging: adding marketing pipeline…");
  await addMarketingPipeline(workspaceDir);
  await waitForPipeline("marketing");
  await materializeStream(`/api/pipelines/${MARKETING}/materialize/stream`);
  await api(`/api/pipelines/${MARKETING}/env-schedules/default`, {
    method: "PUT",
    body: {
      cron: "15 * * * *",
      timezone: "Europe/Berlin",
      catchup_policy: "skip",
      deploy_now: true,
    },
  });
  await triggerRun(MARKETING, "default");
  await triggerRun(MARKETING, "default");

  console.log("staging: applying staleness edits…");
  await addStalenessEdits(workspaceDir);
  await waitForStalenessSettled(api);

  const { runs = [] } = await api("/api/runs");
  const failedRun = runs.find((run) => run.status === "failed" || run.status === "error");
  if (!failedRun) {
    throw new Error("expected a failed run in the staged history");
  }

  return {
    baseURL,
    workspaceDir,
    api,
    notebookId,
    dashboardId,
    reportId,
    failedRunId: failedRun.id,
    stop() {
      if (!server.pid) {
        return;
      }
      try {
        process.kill(-server.pid, "SIGTERM");
      } catch {
        try {
          server.kill("SIGTERM");
        } catch {
          // already gone
        }
      }
    },
    async cleanup() {
      if (process.env.RENART_KEEP_LANDING_WORKSPACE === "1") {
        console.log(`Kept demo workspace at ${workspaceDir}`);
      } else {
        await rm(tempRoot, { recursive: true, force: true }).catch(() => undefined);
      }
    },
  };
}

async function buildNotebook(api) {
  const created = await api("/api/notebooks", {
    method: "POST",
    body: { title: "Revenue deep-dive" },
  });
  const notebookId = created.notebook.id;
  const cells = async () => (await api(`/api/notebooks/${notebookId}`)).notebook.cells;
  const cellId = async (name) => {
    const cell = (await cells()).find((candidate) => candidate.name === name);
    if (!cell) {
      throw new Error(`notebook cell ${name} not found`);
    }
    return cell.cell_id; // cell endpoints take the short id, not the base64 path id
  };

  const firstCell = (await cells())[0];
  await api(`/api/notebooks/${notebookId}/cells/${firstCell.cell_id}/rename`, {
    method: "POST",
    body: { name: "recent_revenue" },
  });
  await api(`/api/notebooks/${notebookId}/cells/${await cellId("recent_revenue")}`, {
    method: "PUT",
    body: {
      content: `SELECT
    order_date,
    order_count,
    ROUND(revenue, 2) AS revenue,
    ROUND(avg_order_value, 2) AS avg_order_value
FROM mart.daily_revenue
ORDER BY order_date DESC
LIMIT 10`,
    },
  });

  // note: @viz arguments use "key: value", not "key=value"
  await api(`/api/notebooks/${notebookId}/cells`, {
    method: "POST",
    body: { name: "revenue_trend", language: "sql" },
  });
  await api(`/api/notebooks/${notebookId}/cells/${await cellId("revenue_trend")}`, {
    method: "PUT",
    body: {
      content: `SELECT order_date, ROUND(revenue, 2) AS revenue
FROM mart.daily_revenue
WHERE revenue >= {{ parameter.minimum_revenue }}
ORDER BY order_date`,
    },
  });

  await api(`/api/notebooks/${notebookId}/cells`, {
    method: "POST",
    body: { name: "category_mix", language: "sql" },
  });
  await api(`/api/notebooks/${notebookId}/cells/${await cellId("category_mix")}`, {
    method: "PUT",
    body: {
      content: `SELECT category, ROUND(SUM(line_total), 2) AS revenue
FROM staging.order_items
GROUP BY category
ORDER BY revenue DESC`,
    },
  });

  let notebook = await applyNotebookOperations(api, notebookId, [
    {
      kind: "markdown.create",
      content:
        "# Revenue deep-dive\n\nExplore recent performance, adjust the revenue threshold, and keep the analysis beside the pipeline.",
      position: "start",
    },
  ]);
  const introBlock = notebook.blocks?.find((block) => block.markdown?.startsWith("# Revenue"));
  if (!introBlock?.id) {
    throw new Error("notebook intro block was not created");
  }

  await applyNotebookOperations(api, notebookId, [
    {
      kind: "control.create",
      parameter: {
        id: "minimum_revenue",
        label: "Minimum daily revenue",
        type: "slider",
        default: 0,
        min: 0,
        max: 1000,
        step: 50,
      },
      position: "after",
      after_block_id: introBlock.id,
    },
  ]);

  const result = await api(`/api/notebooks/${notebookId}/run`, {
    method: "POST",
    body: { all: true },
  });
  const failed = (result.results ?? []).filter((cell) => !["ok", "success"].includes(cell.status));
  if (failed.length > 0) {
    throw new Error(`notebook cells failed: ${JSON.stringify(failed).slice(0, 400)}`);
  }

  const recentRevenue = await cellId("recent_revenue");
  const revenueTrend = await cellId("revenue_trend");
  const categoryMix = await cellId("category_mix");
  await applyNotebookOperations(api, notebookId, [
    {
      kind: "visualization.create",
      visualization: {
        source: recentRevenue,
        definition: {
          version: 1,
          type: "table",
          title: "Recent daily revenue",
          presentation_limit: 10,
          columns: [
            { field: "order_date", label: "Date" },
            { field: "order_count", label: "Orders" },
            { field: "revenue", label: "Revenue" },
            { field: "avg_order_value", label: "Average order" },
          ],
        },
      },
      position: "after",
      after_block_id: recentRevenue,
    },
    {
      kind: "visualization.create",
      visualization: {
        source: revenueTrend,
        definition: {
          version: 1,
          type: "line",
          title: "Revenue trend",
          palette: "forest",
          presentation_limit: 200,
          encoding: {
            x: { field: "order_date" },
            y: [{ field: "revenue", label: "Revenue" }],
          },
        },
      },
      position: "after",
      after_block_id: revenueTrend,
    },
    {
      kind: "visualization.create",
      visualization: {
        source: categoryMix,
        definition: {
          version: 1,
          type: "bar",
          title: "Revenue by category",
          palette: "ocean",
          presentation_limit: 50,
          encoding: {
            x: { field: "category" },
            y: [{ field: "revenue", label: "Revenue" }],
          },
        },
      },
      position: "after",
      after_block_id: categoryMix,
    },
  ]);

  return notebookId;
}

async function applyNotebookOperations(api, notebookId, operations) {
  const current = await api(`/api/notebooks/${notebookId}`);
  const plan = await api(`/api/notebooks/${notebookId}/changes/prepare`, {
    method: "POST",
    body: {
      base_revision: current.notebook.revision,
      operations,
    },
  });
  if (!plan.can_apply) {
    throw new Error(
      `notebook change could not be applied: ${JSON.stringify(plan.blocking_problems ?? [])}`,
    );
  }
  const applied = await api(`/api/notebooks/${notebookId}/changes/apply`, {
    method: "POST",
    body: plan.change_set,
  });
  return applied.notebook;
}

async function buildPresentations(api) {
  const dashboardCreated = await api("/api/presentations", {
    method: "POST",
    body: { kind: "dashboard", title: "Revenue overview" },
  });
  const dashboard = dashboardCreated.document.artifact;
  const dashboardContent = `version: 1
id: ${dashboard.id}
title: Revenue overview
datasets:
  daily_revenue:
    asset: mart.daily_revenue
  top_products:
    asset: mart.top_products
filters:
  - id: category
    label: Category
    type: select
    default: outdoors
    options:
      dataset: top_products
      value_field: category
visualizations:
  - id: revenue_trend
    dataset: daily_revenue
    definition:
      version: 1
      type: line
      title: Daily revenue
      palette: forest
      presentation_limit: 200
      encoding:
        x:
          field: order_date
        y:
          - field: revenue
            label: Revenue
  - id: latest_revenue
    dataset: daily_revenue
    definition:
      version: 1
      type: kpi
      title: Latest daily revenue
      palette: forest
      presentation_limit: 1
      value:
        field: revenue
  - id: product_revenue
    dataset: top_products
    definition:
      version: 1
      type: bar
      title: Product revenue
      palette: ocean
      presentation_limit: 20
      encoding:
        x:
          field: product_name
        y:
          - field: revenue
    filter_bindings:
      - filter: category
        column: category
        operator: equals
  - id: products
    dataset: top_products
    definition:
      version: 1
      type: table
      title: Top products
      presentation_limit: 10
      columns:
        - field: product_name
          label: Product
        - field: category
          label: Category
        - field: units_sold
          label: Units
        - field: revenue
          label: Revenue
layout:
  - visualization: revenue_trend
    width: 8
    height: 5
  - visualization: latest_revenue
    x: 8
    width: 4
    height: 2
  - visualization: product_revenue
    x: 8
    y: 2
    width: 4
    height: 3
  - visualization: products
    y: 5
    width: 12
    height: 4
`;
  const dashboardUpdated = await api(`/api/presentations/${dashboard.workspace_id}`, {
    method: "PUT",
    body: {
      expected_revision: dashboard.revision,
      content: dashboardContent,
    },
  });

  const reportCreated = await api("/api/presentations", {
    method: "POST",
    body: { kind: "report", title: "Weekly commerce brief" },
  });
  const report = reportCreated.document.artifact;
  const reportContent = `version: 1
id: ${report.id}
title: Weekly commerce brief
datasets:
  daily_revenue:
    asset: mart.daily_revenue
  top_products:
    asset: mart.top_products
visualizations:
  - id: revenue_trend
    dataset: daily_revenue
    definition:
      version: 1
      type: area
      title: Revenue over time
      palette: forest
      presentation_limit: 200
      encoding:
        x:
          field: order_date
        y:
          - field: revenue
  - id: products
    dataset: top_products
    definition:
      version: 1
      type: table
      title: Leading products
      presentation_limit: 8
      columns:
        - field: product_name
          label: Product
        - field: category
          label: Category
        - field: units_sold
          label: Units sold
        - field: revenue
          label: Revenue
sections:
  - id: introduction
    title: Commerce pulse
    markdown: |
      Revenue remained active across the period. This report keeps the trend and
      product mix beside the pipeline definition that produced them.
  - id: trend
    visualization: revenue_trend
  - id: products_note
    title: Product mix
    markdown: The leading products below are calculated from fulfilled order items.
  - id: products
    visualization: products
`;
  const reportUpdated = await api(`/api/presentations/${report.workspace_id}`, {
    method: "PUT",
    body: {
      expected_revision: report.revision,
      content: reportContent,
    },
  });

  for (const document of [dashboardUpdated.document, reportUpdated.document]) {
    const result = await api(`/api/presentations/${document.artifact.workspace_id}/run`, {
      method: "POST",
      body: { environment: "default", include_options: true },
    });
    if (result.status !== "ok") {
      throw new Error(
        `presentation ${document.artifact.title} failed: ${JSON.stringify(result).slice(0, 600)}`,
      );
    }
  }

  return {
    dashboardId: dashboardUpdated.document.artifact.workspace_id,
    reportId: reportUpdated.document.artifact.workspace_id,
  };
}

// The staleness watcher and the workspace content model update on separate
// paths; the editor renders workspace `content`, so wait for both before
// capturing or shots show the pre-edit file.
async function waitForStalenessSettled(api) {
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    const staleness = await api(`/api/pipelines/${ACME}/staleness?environment=default`).catch(
      () => null,
    );
    const states = JSON.stringify(staleness ?? {});
    const workspace = await api("/api/workspace").catch(() => null);
    const acme = workspace?.pipelines?.find((pipeline) => pipeline.name === "acme");
    const contentCurrent = acme?.assets
      ?.find((asset) => asset.name === "staging.orders")
      ?.content?.includes("NOT IN ('refunded', 'pending')");
    const weeklyDiscovered = acme?.assets?.some((asset) => asset.name === "mart.weekly_summary");
    if (
      states.includes("stale_edited") &&
      states.includes("never_built") &&
      contentCurrent &&
      weeklyDiscovered
    ) {
      return;
    }
    await sleep(1000);
  }
  throw new Error("staleness states did not settle in time");
}

// --- capture helpers ---------------------------------------------------------

// Returns { withPage, goto, shot } bound to a browser + base URL + output dir.
export function makeCapture(browser, baseURL, outputDir) {
  async function withPage(viewport, fn) {
    const ctx = await browser.newContext({ viewport, colorScheme: "dark", deviceScaleFactor: 2 });
    await ctx.addInitScript(() => localStorage.setItem("renart-theme", "dark"));
    const page = await ctx.newPage();
    page.on("pageerror", (err) => console.log("PAGEERROR:", err.message));
    try {
      await fn(page);
    } finally {
      await ctx.close();
    }
  }

  // networkidle never fires (the SSE /api/events connection stays open), so
  // navigation always uses domcontentloaded plus a fixed settle.
  async function goto(page, url, settle) {
    await page.goto(baseURL + url, { waitUntil: "domcontentloaded" });
    await page.waitForTimeout(settle);
  }

  async function shot(page, name, options = {}) {
    await page.screenshot({ ...options, path: path.join(outputDir, `${name}.png`) });
    console.log("captured", name);
  }

  return { withPage, goto, shot };
}

export function sharp() {
  return docsRequire("sharp");
}

// Converts `<name>.png` files in outputDir to webp (q92) and deletes the PNGs.
export async function convertShotsToWebp(outputDir, names) {
  const sharpLib = sharp();
  for (const name of names) {
    const png = path.join(outputDir, `${name}.png`);
    const info = await sharpLib(png)
      .webp({ quality: 92 })
      .toFile(path.join(outputDir, `${name}.webp`));
    await unlink(png);
    console.log(`${name}.webp ${info.width}x${info.height} ${info.size} bytes`);
  }
}
