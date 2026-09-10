// Regenerates the docs screenshots in docs/public/docs-media.
//
// Run with `make docs-media` (or `pnpm docs:media` in web/, which builds
// web/dist first). Shares the demo workspace, server, and staged state with
// the landing pipeline (demo-media-lib.mjs) so the docs show the same
// coherent acme project at the same quality bar: dark theme, 2x DPR, webp.
//
// Env overrides: RENART_DOCS_MEDIA_DIR (output dir), RENART_DOCS_MEDIA_PORT,
// GO_BIN, RENART_KEEP_LANDING_WORKSPACE=1.
import { chromium, expect } from "@playwright/test";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import {
  ACME,
  STAGING_ORDERS,
  convertShotsToWebp,
  id,
  launchStagedDemo,
  makeCapture,
  repoRoot,
} from "./demo-media-lib.mjs";

const outputDir = path.resolve(
  process.env.RENART_DOCS_MEDIA_DIR ?? path.join(repoRoot, "docs", "public", "docs-media"),
);
const port = Number(process.env.RENART_DOCS_MEDIA_PORT ?? "18184");

let demo;
let browser;
let isolatedConfig;

try {
  await mkdir(outputDir, { recursive: true });
  if (process.env.RENART_DOCS_MEDIA_SOURCE_JSON) {
    // Reuse only a disposable stage created by intro-video/stage.mjs.
    const source = JSON.parse(await readFile(process.env.RENART_DOCS_MEDIA_SOURCE_JSON, "utf8"));
    if (
      new URL(source.baseURL).hostname !== "127.0.0.1" ||
      !path.basename(path.dirname(source.workspaceDir)).startsWith("renart-demo-media-")
    )
      throw new Error("Expected a local disposable media stage");
    demo = {
      ...source,
      stop() {},
      async cleanup() {},
      async api(url, options = {}) {
        const response = await fetch(source.baseURL + url, {
          method: options.method ?? "GET",
          headers: { Origin: source.baseURL, "Content-Type": "application/json" },
          body: options.body ? JSON.stringify(options.body) : undefined,
        });
        if (!response.ok) throw new Error(`${url}: ${response.status} ${await response.text()}`);
        return response.json();
      },
    };
  } else {
    isolatedConfig = await mkdtemp(path.join(tmpdir(), "renart-docs-config-"));
    process.env.XDG_CONFIG_HOME = isolatedConfig;
    demo = await launchStagedDemo({ port });
  }

  console.log("capturing screenshots…");
  browser = await chromium.launch();
  const { withPage, goto, shot } = makeCapture(browser, demo.baseURL, outputDir);

  // workspace-overview: the split view — explorer, editor, canvas, results,
  // workbench in one frame (interface tour, docs landing, quickstart)
  await withPage({ width: 1600, height: 1000 }, async (page) => {
    await goto(page, `/pipelines/${ACME}/assets/${STAGING_ORDERS}/split`, 6000);
    await shot(page, "workspace-overview");
  });

  // pipeline-canvas: the full DAG with all four freshness badges
  await withPage({ width: 1400, height: 900 }, async (page) => {
    await goto(
      page,
      `/pipelines/${ACME}/assets/${id("acme/assets/mart/customer_ltv.sql")}/canvas`,
      5000,
    );
    await page.waitForTimeout(1000);
    const properties = page.getByRole("button", { name: "Hide properties", exact: true });
    if (await properties.isVisible()) await properties.click();
    await page.waitForTimeout(600);
    await page
      .getByRole("button", { name: "Collapse results panel" })
      .click()
      .catch(() => {});
    await page.waitForTimeout(600);
    await page
      .locator(".react-flow__controls-fitview")
      .first()
      .click()
      .catch(() => {});
    await page.waitForTimeout(1200);
    await shot(page, "pipeline-canvas");
  });

  // asset-editor: code view with the completion popup over upstream columns.
  // Typing autosaves; the demo workspace is disposable but restore anyway so
  // later shots (if reordered) see the staged content.
  const originalOrders = (await demo.api("/api/workspace")).pipelines
    .find((p) => p.id === ACME)
    .assets.find((a) => a.id === STAGING_ORDERS).content;
  try {
    await withPage({ width: 1400, height: 900 }, async (page) => {
      await goto(page, `/pipelines/${ACME}/assets/${STAGING_ORDERS}/code`, 5000);
      await page.getByRole("button", { name: "Collapse results panel", exact: true }).click();
      await page.getByText("o.total_amount").first().click();
      await page.keyboard.press("End");
      await page.keyboard.type(",");
      await page.keyboard.press("Enter");
      await page.keyboard.type("    c.", { delay: 60 });
      await page.waitForTimeout(400);
      await page.keyboard.press("Control+Space");
      await page.waitForSelector(".suggest-widget.visible", { timeout: 10000 });
      await page.waitForTimeout(1200);
      await shot(page, "asset-editor");
    });
  } finally {
    await demo.api(`/api/pipelines/${ACME}/assets/${STAGING_ORDERS}`, {
      method: "PUT",
      body: { content: originalOrders },
    });
  }

  // notebook: authored text and controls, typed result blocks, a durable
  // visualization, and its shared settings inspector.
  await withPage({ width: 1500, height: 960 }, async (page) => {
    await goto(page, `/notebooks/${demo.notebookId}`, 5000);
    const run = page.getByRole("button", { name: "Run all", exact: true });
    await run.click();
    await expect(run).toBeEnabled({ timeout: 60000 });
    const chart = page.locator("[data-notebook-visualization-id]", {
      hasText: "Revenue trend",
    });
    await chart.scrollIntoViewIfNeeded();
    await chart.locator(".recharts-surface").first().waitFor({ timeout: 30000 });
    await chart.getByRole("region", { name: "Visualization: Revenue trend", exact: true }).click();
    await expect(page.getByRole("button", { name: "Close inspector", exact: true })).toBeVisible();
    await page.waitForTimeout(1200);
    await shot(page, "notebook");
  });

  // notebook-agent: the notebook-scoped local agent composer in its safe
  // default Ask mode. No provider is invoked during media generation.
  await withPage({ width: 1500, height: 960 }, async (page) => {
    await goto(page, `/notebooks/${demo.notebookId}`, 5000);
    const run = page.getByRole("button", { name: "Run all", exact: true });
    await run.click();
    await expect(run).toBeEnabled({ timeout: 60000 });
    await page.getByRole("grid").first().waitFor({ timeout: 30000 });
    await page.getByRole("tab", { name: "AI", exact: true }).click();
    await page.getByText("Notebook assistant", { exact: true }).waitFor({ timeout: 15000 });
    await page.waitForTimeout(1200);
    await shot(page, "notebook-agent");
  });

  // dashboard-builder: a populated, checked dashboard with its Add rail,
  // filter strip, responsive canvas, and visualization inspector.
  await withPage({ width: 1500, height: 960 }, async (page) => {
    await goto(page, `/dashboards/${demo.dashboardId}`, 8000);
    await page.getByTestId("presentation-builder").waitFor({ timeout: 15000 });
    await page
      .getByRole("tab", { name: "Add", exact: true })
      .click()
      .catch(() => {});
    await page
      .getByTestId("dashboard-visualization-revenue_trend")
      .click()
      .catch(() => {});
    await page.waitForTimeout(1500);
    await shot(page, "dashboard-builder");
  });

  // report-builder: a narrative document with text and visual blocks, its
  // outline, and the shared inspector visible together.
  await withPage({ width: 1500, height: 960 }, async (page) => {
    await goto(page, `/reports/${demo.reportId}`, 8000);
    await page.getByTestId("presentation-builder").waitFor({ timeout: 15000 });
    await page
      .getByRole("tab", { name: "Outline", exact: true })
      .click()
      .catch(() => {});
    await page
      .getByTestId("report-canvas")
      .getByText("Revenue over time", { exact: true })
      .click()
      .catch(() => {});
    await page.waitForTimeout(1500);
    await shot(page, "report-builder");
  });

  // schedules: the schedule list with the run timeline
  await withPage({ width: 1400, height: 760 }, async (page) => {
    await goto(page, "/schedules", 3500);
    await shot(page, "schedules");
  });

  // The source review uses the saved workspace, not a fabricated diff.
  await withPage({ width: 1400, height: 900 }, async (page) => {
    await goto(page, `/pipelines/${ACME}/assets/${STAGING_ORDERS}/code`, 3500);
    await page
      .getByRole("button", { name: /^(Redeploy|Deploy)/ })
      .first()
      .click();
    await page.getByTestId("deployment-review").waitFor();
    await page
      .getByTestId("pipeline-plan-sheet")
      .getByRole("button", { name: /assets\/staging\/orders.sql/ })
      .click();
    const editor = page.getByTestId("deployment-file-diff").locator(".monaco-editor").last();
    await editor.waitFor();
    await editor.hover();
    await page.mouse.wheel(0, 500);
    await page.waitForTimeout(1000);
    await shot(page, "deployment-review");
  });

  // run-detail: the failed run — per-asset gantt + the error in the event log
  await withPage({ width: 1400, height: 900 }, async (page) => {
    await goto(page, `/runs/${demo.failedRunId}`, 3500);
    await shot(page, "run-detail");
  });

  // catalog: cross-pipeline lineage with daily_revenue's upstream path lit
  await withPage({ width: 1400, height: 900 }, async (page) => {
    await goto(page, "/catalog", 4000);
    await page.getByText("daily_revenue", { exact: true }).first().click();
    await page.waitForTimeout(1800);
    await shot(page, "catalog");
  });

  // --- per-asset-type editor shots ------------------------------------------
  // The staged acme project is all SQL, so the Python/Load/API shots first
  // create their assets through the same API the UI's "New asset" flow uses.
  // This happens after the canvas/catalog shots so the DAG captures stay
  // unchanged.
  console.log("creating asset-type demo assets…");
  await mkdir(path.join(demo.workspaceDir, "acme", "data"), { recursive: true });
  await writeFile(
    path.join(demo.workspaceDir, "acme", "data", "exchange_rates.csv"),
    [
      "day,currency,rate_to_usd",
      "2026-07-08,EUR,1.09",
      "2026-07-08,GBP,1.27",
      "2026-07-09,EUR,1.08",
      "2026-07-09,GBP,1.28",
      "",
    ].join("\n"),
  );

  const pythonAsset = await demo.api(`/api/pipelines/${ACME}/assets`, {
    method: "POST",
    body: {
      name: "mart.customer_segments",
      type: "python",
      path: "assets/mart/customer_segments.py",
    },
  });
  await demo.api(`/api/pipelines/${ACME}/assets/${pythonAsset.asset_id}`, {
    method: "PUT",
    body: {
      content: [
        "def materialize():",
        "    return [",
        '        {"segment": "Enterprise", "min_orders": 12, "discount": 0.15},',
        '        {"segment": "Regular", "min_orders": 4, "discount": 0.05},',
        '        {"segment": "Occasional", "min_orders": 0, "discount": 0.0},',
        "    ]",
        "",
      ].join("\n"),
    },
  });

  // Load asset: created semantically so the backend writes the canonical
  // single-file definition (local CSV -> the warehouse connection).
  const loadAsset = await demo.api(`/api/pipelines/${ACME}/assets`, {
    method: "POST",
    body: {
      name: "raw.exchange_rates",
      type: "load",
      path: "assets/raw/exchange_rates.asset.yml",
      connection: "duckdb-default",
      parameters: {
        source_connection: "local",
        source_table: "data/exchange_rates.csv",
      },
    },
  });

  // API asset: created without content so the backend writes its OpenAPI
  // starter skeleton (weather alerts request + records_path).
  const apiAsset = await demo.api(`/api/pipelines/${ACME}/assets`, {
    method: "POST",
    body: {
      name: "raw.weather_alerts",
      type: "api",
      path: "assets/raw/weather_alerts.asset.yml",
    },
  });

  // Let the workspace model pick up the three new assets before capturing.
  {
    const deadline = Date.now() + 60_000;
    for (;;) {
      const workspace = await demo.api("/api/workspace").catch(() => null);
      const names = JSON.stringify(workspace ?? {});
      if (
        names.includes("customer_segments") &&
        names.includes("exchange_rates") &&
        names.includes("weather_alerts")
      ) {
        break;
      }
      if (Date.now() > deadline) {
        throw new Error("new asset-type assets were not discovered in time");
      }
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
  }

  // sql-asset: a mart query in the code view with the workbench alongside
  await withPage({ width: 1400, height: 900 }, async (page) => {
    await goto(
      page,
      `/pipelines/${ACME}/assets/${id("acme/assets/mart/daily_revenue.sql")}/code`,
      5000,
    );
    await shot(page, "sql-asset");
  });

  // The three new assets have never been built, so the results panel would
  // have no persisted output; focus these shots on editing the definition.
  const collapseResults = async (page) => {
    await page
      .getByRole("button", { name: "Collapse results panel" })
      .click()
      .catch(() => {});
    await page.waitForTimeout(600);
  };

  // python-asset: the materialize() contract in the code view
  await withPage({ width: 1400, height: 900 }, async (page) => {
    await goto(page, `/pipelines/${ACME}/assets/${pythonAsset.asset_id}/code`, 5000);
    await collapseResults(page);
    await shot(page, "python-asset");
  });

  // load-asset: the form editor with a local source and name-derived destination
  await withPage({ width: 1400, height: 900 }, async (page) => {
    await goto(page, `/pipelines/${ACME}/assets/${loadAsset.asset_id}/code`, 5000);
    await collapseResults(page);
    await shot(page, "load-asset");
  });

  // api-asset: the OpenAPI starter in the API editor
  await withPage({ width: 1400, height: 900 }, async (page) => {
    await goto(page, `/pipelines/${ACME}/assets/${apiAsset.asset_id}/code`, 5000);
    await collapseResults(page);
    await shot(page, "api-asset");
  });

  await browser.close();
  browser = undefined;

  console.log("converting to webp…");
  await convertShotsToWebp(outputDir, [
    "workspace-overview",
    "pipeline-canvas",
    "asset-editor",
    "notebook",
    "notebook-agent",
    "dashboard-builder",
    "report-builder",
    "schedules",
    "deployment-review",
    "run-detail",
    "catalog",
    "sql-asset",
    "python-asset",
    "load-asset",
    "api-asset",
  ]);
  console.log(`\nDocs media written to ${outputDir}`);
  console.log("If a capture changed size, update the width/height where the image is referenced.");
} finally {
  await browser?.close().catch(() => undefined);
  demo?.stop();
  await demo?.cleanup();
  if (isolatedConfig) await rm(isolatedConfig, { recursive: true, force: true });
}
