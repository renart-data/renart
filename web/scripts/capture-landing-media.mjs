// Regenerates all landing-page media in docs/public/landing.
//
// Run with `make landing-media` (or `pnpm landing:media` in web/, which builds
// web/dist first). The demo workspace, server, and staged state come from
// demo-media-lib.mjs (shared with make docs-media); this script captures the
// seven landing shots, converts them to webp (q92), emits responsive webp
// variants for the site, and renders the 1200x675 og-image.
//
// Env overrides: RENART_LANDING_MEDIA_DIR (output dir),
// RENART_LANDING_MEDIA_PORT, GO_BIN, RENART_KEEP_LANDING_WORKSPACE=1.
import { chromium } from "@playwright/test";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import {
  ACME,
  STAGING_ORDERS,
  convertShotsToWebp,
  launchStagedDemo,
  makeCapture,
  repoRoot,
  sharp,
} from "./demo-media-lib.mjs";

const outputDir = path.resolve(
  process.env.RENART_LANDING_MEDIA_DIR ?? path.join(repoRoot, "docs", "public", "landing"),
);
const port = Number(process.env.RENART_LANDING_MEDIA_PORT ?? "18183");
const responsiveWidths = {
  "hero-workspace": [480, 768, 1280, 1920],
  "lifecycle-build": [480, 768, 1280],
  "lifecycle-notebook": [480, 768, 1280],
  "lifecycle-schedules": [480, 768, 1280],
  "lifecycle-staleness": [480, 768, 1280],
  "feature-runs": [480, 768, 1280, 1920],
  "feature-catalog": [480, 768, 1280, 1920],
};

async function writeResponsiveVariants() {
  const sharpLib = sharp();
  for (const [name, widths] of Object.entries(responsiveWidths)) {
    const source = path.join(outputDir, `${name}.webp`);
    for (const width of widths) {
      const info = await sharpLib(source)
        .resize({ width, withoutEnlargement: true })
        .webp({ quality: 86, smartSubsample: true, effort: 6 })
        .toFile(path.join(outputDir, `${name}-${width}.webp`));
      console.log(`${name}-${width}.webp ${info.width}x${info.height} ${info.size} bytes`);
    }
  }
}

let demo;
let browser;

try {
  await mkdir(outputDir, { recursive: true });
  demo = await launchStagedDemo({ port });

  console.log("capturing screenshots…");
  browser = await chromium.launch();
  const { withPage, goto, shot } = makeCapture(browser, demo.baseURL, outputDir);

  // Supporting shots are shown in narrower landing-page columns. Keep their
  // desktop viewport for faithful app layout, then crop in CSS pixels around
  // the story being told. The shared 2x device scale still emits retina media.

  // hero: split view of a staging asset — editor, canvas, results, workbench
  await withPage({ width: 1920, height: 1080 }, async (page) => {
    await goto(page, `/pipelines/${ACME}/assets/${STAGING_ORDERS}/split`, 6000);
    await shot(page, "hero-workspace");
  });

  // notebook: a typed chart block with the authored notebook around it.
  await withPage({ width: 1400, height: 900 }, async (page) => {
    await goto(page, `/notebooks/${demo.notebookId}`, 5000);
    const chart = page.locator("[data-notebook-visualization-id]", {
      hasText: "Revenue trend",
    });
    await chart.scrollIntoViewIfNeeded();
    await page.waitForTimeout(1200);
    await shot(page, "lifecycle-notebook", {
      clip: { x: 190, y: 160, width: 1008, height: 648 },
    });
  });

  // schedules: populated environment schedules with their pinned deployment
  // metadata and projected run timeline. The crop keeps the schedule identity
  // and useful timeline detail legible instead of shrinking the entire shell.
  await withPage({ width: 1400, height: 900 }, async (page) => {
    await goto(page, "/schedules", 3500);
    await page.waitForFunction(
      () => document.querySelectorAll('[data-testid="schedule-row"]').length >= 3,
    );
    await page.waitForTimeout(800);
    await shot(page, "lifecycle-schedules", {
      clip: { x: 0, y: 48, width: 1120, height: 720 },
    });
  });

  // staleness: an unobstructed full canvas with all four freshness badges
  await withPage({ width: 1400, height: 900 }, async (page) => {
    await goto(page, `/pipelines/${ACME}/canvas`, 5000);
    await page
      .getByRole("button", { name: "Hide explorer" })
      .click()
      .catch(() => {});
    await page.waitForTimeout(600);
    await page
      .getByRole("button", { name: "Hide properties" })
      .click()
      .catch(() => {});
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
    await page.evaluate(() => {
      for (const element of Array.from(document.querySelectorAll("button"))) {
        if (element.textContent?.includes("New asset")) {
          element.remove();
        }
      }
    });
    await shot(page, "lifecycle-staleness", {
      clip: { x: 112, y: 105, width: 1176, height: 756 },
    });
  });

  // runs: the failed run's detail — per-asset gantt + the binder error events
  await withPage({ width: 1200, height: 675 }, async (page) => {
    await goto(page, `/runs/${demo.failedRunId}`, 3500);
    await shot(page, "feature-runs");
  });

  // catalog: cross-pipeline lineage with daily_revenue's upstream path lit
  await withPage({ width: 1200, height: 675 }, async (page) => {
    await goto(page, "/catalog", 4000);
    await page.getByText("daily_revenue", { exact: true }).first().click();
    await page.waitForTimeout(1800);
    await shot(page, "feature-catalog");
  });

  // build: completion popup over upstream columns. Typing in the editor
  // AUTOSAVES to disk, so this shot runs last and restores the file after.
  const stagingOrdersFile = path.join(demo.workspaceDir, "acme", "assets", "staging", "orders.sql");
  const stagingOrdersContent = await readFile(stagingOrdersFile, "utf8");
  try {
    const buildCaptureContent = stagingOrdersContent.replace(
      "    o.total_amount\nFROM raw.orders o",
      `    o.total_amount,
    DATE_TRUNC('week', o.order_date) AS order_week,
    CASE
        WHEN o.total_amount >= 200 THEN 'high_value'
        WHEN o.total_amount >= 100 THEN 'mid_value'
        ELSE 'standard'
    END AS order_segment
FROM raw.orders o`,
    );
    if (buildCaptureContent === stagingOrdersContent) {
      throw new Error("build capture could not enrich staging.orders");
    }
    await writeFile(stagingOrdersFile, buildCaptureContent);
    await new Promise((resolve) => setTimeout(resolve, 1800));

    await withPage({ width: 1400, height: 900 }, async (page) => {
      await goto(page, `/pipelines/${ACME}/assets/${STAGING_ORDERS}/code`, 5000);
      await page
        .getByRole("button", { name: "Hide properties" })
        .click()
        .catch(() => {});
      await page.waitForTimeout(600);
      await page.getByText("o.total_amount").first().click();
      await page.keyboard.press("End");
      await page.keyboard.press("Enter");
      await page.keyboard.type("    c.", { delay: 60 });
      await page.waitForTimeout(400);
      await page.keyboard.press("Control+Space");
      await page.waitForSelector(".suggest-widget.visible", { timeout: 5000 }).catch(() => {});
      await page.waitForTimeout(1200);
      // hide the card the half-typed query triggers
      await page.evaluate(() => {
        for (const el of Array.from(document.querySelectorAll("div"))) {
          if (
            el.textContent?.startsWith("Preview failed") &&
            el.clientHeight > 0 &&
            el.clientHeight < 300
          ) {
            el.style.visibility = "hidden";
            break;
          }
        }
      });
      await shot(page, "lifecycle-build", {
        clip: { x: 250, y: 80, width: 910, height: 585 },
      });
    });
  } finally {
    await writeFile(stagingOrdersFile, stagingOrdersContent);
  }

  await browser.close();
  browser = undefined;

  console.log("converting to webp + og-image…");
  const ogInfo = await sharp()(path.join(outputDir, "hero-workspace.png"))
    .resize(1200, 675, { fit: "cover" })
    .png({ compressionLevel: 9 })
    .toFile(path.join(outputDir, "og-image.png"));
  console.log(`og-image.png ${ogInfo.width}x${ogInfo.height} ${ogInfo.size} bytes`);
  await convertShotsToWebp(outputDir, [
    "hero-workspace",
    "lifecycle-build",
    "lifecycle-notebook",
    "lifecycle-schedules",
    "lifecycle-staleness",
    "feature-runs",
    "feature-catalog",
  ]);
  await writeResponsiveVariants();
  console.log(`\nLanding media written to ${outputDir}`);
  console.log(
    "If a capture changed size, update the <img> width/height in docs/src/pages/index.astro.",
  );
} finally {
  await browser?.close().catch(() => undefined);
  demo?.stop();
  await demo?.cleanup();
}
