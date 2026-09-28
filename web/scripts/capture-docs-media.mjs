// Regenerates the docs screenshots in docs/public/docs-media.
//
// Run with `make docs-media` (or `pnpm docs:media` in web/, which builds
// web/dist first). Shares the demo workspace, server, and staged state with
// the landing pipeline (demo-media-lib.mjs) so the docs show the same
// coherent acme project at the same quality bar: 2x DPR, webp. The eight
// workspace-tour images also emit a matching -light variant.
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
  const { withPage, goto, shot, capturedShots } = makeCapture(browser, demo.baseURL, outputDir, {
    lightShots: [
      "workspace-overview",
      "pipeline-canvas",
      "notebook",
      "dashboard-builder",
      "report-builder",
      "schedules",
      "run-detail",
      "load-asset",
    ],
  });

  // workspace-overview: the split view — explorer, editor, canvas, results,
  // workbench in one frame (interface tour, docs landing, quickstart)
  await withPage({ width: 1600, height: 1000 }, async (page) => {
    await goto(page, `/pipelines/${ACME}/assets/${STAGING_ORDERS}/split`, 6000);
    await page.locator(".react-flow__controls-fitview").first().click();
    await page.waitForTimeout(800);
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

  // A clip around one element, so a page shows its subject at a readable size.
  const clipTo = async (locator, { pad = 12, maxHeight } = {}) => {
    const box = await locator.boundingBox();
    if (!box) throw new Error("clip target is not visible");
    return {
      x: Math.max(0, box.x - pad),
      y: Math.max(0, box.y - pad),
      width: box.width + pad * 2,
      height: Math.min(box.height + pad * 2, maxHeight ?? Infinity),
    };
  };
  const assetContent = async (assetId) =>
    (await demo.api("/api/workspace")).pipelines
      .find((p) => p.id === ACME)
      .assets.find((a) => a.id === assetId).content;
  const restoreAsset = (assetId, content) =>
    demo.api(`/api/pipelines/${ACME}/assets/${assetId}`, { method: "PUT", body: { content } });

  // data-browser: an existing table offered to the canvas as a new source
  // (Browse your data). Nothing is saved; the page closes before confirming.
  await withPage({ width: 1400, height: 900 }, async (page) => {
    await goto(page, `/pipelines/${ACME}/canvas`, 5000);
    await page.getByRole("button", { name: "Data Browser", exact: true }).first().click();
    await page.waitForTimeout(1500);
    await page
      .getByPlaceholder(/Search|Filter/)
      .first()
      .fill("duckdb-default.acme.raw.");
    await page
      .getByRole("button", { name: "Use campaigns in canvas" })
      .waitFor({ state: "attached" });
    await page.getByRole("button", { name: "Use campaigns in canvas" }).click({ force: true });
    await page.getByText("Create source in new group", { exact: true }).waitFor();
    await page.waitForTimeout(1200);
    await shot(page, "data-browser");
  });

  // needed-assets: Review run opens on the assets the staged edits made stale
  // (Rebuild only what changed). Runs before the editing shots below so the
  // staleness state is exactly the staged one.
  await withPage({ width: 1400, height: 760 }, async (page) => {
    await goto(page, `/pipelines/${ACME}/canvas`, 5000);
    await page
      .getByRole("button", { name: /Review run/ })
      .first()
      .click();
    const sheet = page.getByTestId("pipeline-plan-sheet");
    await sheet.getByRole("heading", { name: /assets? will run$/ }).waitFor();
    await page.waitForTimeout(1500);
    await shot(page, "needed-assets", { clip: await clipTo(sheet, { pad: 0 }) });
  });

  // jinja-variables: a run value in a SQL filter, with the hover that shows
  // what it renders to (Variables and Jinja in SQL).
  const DAILY_REVENUE = id("acme/assets/mart/daily_revenue.sql");
  const originalDailyRevenue = await assetContent(DAILY_REVENUE);
  try {
    await withPage({ width: 1400, height: 900 }, async (page) => {
      await goto(page, `/pipelines/${ACME}/assets/${DAILY_REVENUE}/code`, 5000);
      await page.getByRole("button", { name: "Collapse results panel", exact: true }).click();
      await page.locator(".monaco-editor").getByText("GROUP BY order_date").first().click();
      await page.keyboard.press("Home");
      await page.keyboard.press("Enter");
      await page.keyboard.press("ArrowUp");
      await page.keyboard.insertText("WHERE order_date >= '{{ start_date }}'");
      await page.waitForTimeout(3500);
      // Typing opens SQL suggestions asynchronously; close them before hovering.
      await page.keyboard.press("Escape");
      await page.locator(".suggest-widget.visible").waitFor({ state: "hidden" });
      await page
        .locator(".monaco-editor .view-line span", { hasText: "start_date" })
        .first()
        .hover({ force: true });
      await page.locator(".monaco-hover", { hasText: "Rendered" }).first().waitFor();
      await page.waitForTimeout(800);
      const editor = page.locator(".monaco-editor").first();
      const box = await editor.boundingBox();
      await shot(page, "jinja-variables", {
        clip: { x: box.x, y: box.y, width: box.width, height: 330 },
      });
    });
  } finally {
    await restoreAsset(DAILY_REVENUE, originalDailyRevenue);
  }

  // type-check: a misspelled column, flagged in the editor and listed in the
  // Type check tab with its declared-columns warning (Catch errors with type checking).
  const CUSTOMER_LTV = id("acme/assets/mart/customer_ltv.sql");
  const originalCustomerLtv = await assetContent(CUSTOMER_LTV);
  try {
    await restoreAsset(CUSTOMER_LTV, originalCustomerLtv.replace("sc.country,", "sc.countrie,"));
    await withPage({ width: 1400, height: 760 }, async (page) => {
      await goto(page, `/pipelines/${ACME}/assets/${CUSTOMER_LTV}/code`, 5000);
      await page
        .getByRole("tab", { name: /Type check/ })
        .first()
        .click();
      await page.getByRole("button", { name: /Re-run/ }).click();
      await page.getByText("View source").first().waitFor({ timeout: 30000 });
      await page.waitForTimeout(1500);
      // Editor plus results panel: from the tab row above the code to the
      // bottom of the viewport, across the centre column only.
      const editor = await page.locator(".monaco-editor").first().boundingBox();
      const top = editor.y - 48;
      await shot(page, "type-check", {
        clip: { x: editor.x - 8, y: top, width: editor.width + 16, height: 760 - top - 8 },
      });
    });
  } finally {
    await restoreAsset(CUSTOMER_LTV, originalCustomerLtv);
  }

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
  await convertShotsToWebp(outputDir, capturedShots);
  console.log(`\nDocs media written to ${outputDir}`);
  console.log("If a capture changed size, update the width/height where the image is referenced.");
} finally {
  await browser?.close().catch(() => undefined);
  demo?.stop();
  await demo?.cleanup();
  if (isolatedConfig) await rm(isolatedConfig, { recursive: true, force: true });
}
