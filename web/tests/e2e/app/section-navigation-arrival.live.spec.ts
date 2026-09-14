import { expect, type Page } from "@playwright/test";
import { appendFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import type { NavigableTarget } from "../../../lib/resource-navigation";
import { liveTest as test } from "../live-app-fixture";
import { observeArrivals, arrivals, clearArrivals } from "../navigation-arrival-probe";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
test.setTimeout(90000);
const pipelineId = Buffer.from("analytics").toString("base64url");
const missingPath = "analytics/assets/analytics/missing.asset.yml";
const missingId = Buffer.from(missingPath).toString("base64url");
const typedPath = "analytics/assets/analytics/typed.sql";
const typedId = Buffer.from(typedPath).toString("base64url");

const inspector = (page: Page) => page.getByTestId("asset-inspector").filter({ visible: true });

test("real missing-column links highlight their section on first, repeated and cold arrivals", async ({
  page,
  liveApp,
  browser,
  isMobile,
}, info) => {
  await writeFile(
    join(liveApp.workspaceDir, missingPath),
    "name: analytics.missing\ntype: load\nconnection: duckdb-default\nparameters:\n  source_connection: duckdb-default\n  source_table: external_source\nmaterialization:\n  type: table\n  strategy: create+replace\n",
  );
  await observeArrivals(page);
  const commands: string[] = [];
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("request", (request) => {
    if (
      request.method() !== "GET" &&
      /\/(trigger|materialize|run|transactions|sync)(\/stream)?$/.test(
        new URL(request.url()).pathname,
      )
    )
      commands.push(request.url());
  });
  await page.goto(
    `${liveApp.baseURL}/pipelines/${pipelineId}/assets/${missingId}/code?result=typecheck&editor=asset`,
  );
  const link = page.getByRole("link", { name: "Open columns", exact: true }).first();
  await expect(link).toBeVisible({ timeout: 20000 });
  const href = new URL((await link.getAttribute("href"))!, liveApp.baseURL).href;
  expect(JSON.parse(new URL(href).searchParams.get("detail")!).target).toEqual({
    kind: "asset-section",
    asset_id: missingId,
    section: "columns",
  });
  for (let visit = 0; visit < 2; visit++) {
    await clearArrivals(page);
    await link.click();
    const section = inspector(page).locator('[data-navigation-section="columns"]');
    await expect
      .poll(() => arrivals(page))
      .toEqual([expect.objectContaining({ section: "columns", visible: true })]);
    await expect(section).toBeFocused();
    await expect(section).toContainText("No columns. Add one manually");
    if (visit === 0)
      await page.screenshot({ path: info.outputPath("missing-columns-arrival.png") });
    await expect(section).not.toHaveAttribute("data-navigation-arrival", "true");
    await expect(section).toHaveCSS("outline-style", "none");
    expect(await arrivals(page)).toEqual([
      expect.objectContaining({ section: "columns", visible: true }),
    ]);
    expect(new URL(page.url()).searchParams.get("result")).toBe("typecheck");
    if (isMobile) await page.keyboard.press("Escape");
  }
  const context = await browser.newContext(info.project.use);
  try {
    const fresh = await context.newPage();
    await observeArrivals(fresh);
    await fresh.emulateMedia({ reducedMotion: "reduce", colorScheme: "dark" });
    await fresh.goto(href);
    const section = inspector(fresh).locator('[data-navigation-section="columns"]');
    await expect
      .poll(() => arrivals(fresh))
      .toEqual([expect.objectContaining({ section: "columns", visible: true, animation: "none" })]);
    await fresh.screenshot({ path: info.outputPath("missing-columns-reduced-motion.png") });
    await expect(section).not.toHaveAttribute("data-navigation-arrival", "true");
    await expect(section).toHaveCSS("outline-style", "none");
    expect(await arrivals(fresh)).toHaveLength(1);
  } finally {
    await context.close();
  }
  expect(errors).toEqual([]);
  expect(commands).toEqual([]);
});

test("metadata sections highlight once after reveal while local tabs, SSE and missing fields stay quiet", async ({
  page,
  liveApp,
}) => {
  await writeFile(
    join(liveApp.workspaceDir, typedPath),
    "/* @bruin\nname: analytics.typed\ntype: duckdb.sql\nmaterialization:\n  type: view\ncolumns:\n  - name: total_amount\n    type: INTEGER\n    checks:\n      - name: not_null\n@bruin */\nselect 1 as total_amount\n",
  );
  await observeArrivals(page);
  const config = await (await page.request.get(`${liveApp.baseURL}/api/config`)).json();
  const urlFor = (target: NavigableTarget) => {
    const url = new URL(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${typedId}/code`);
    url.searchParams.set("project", config.project_id);
    url.searchParams.set("result", "inspect");
    url.searchParams.set("detail", JSON.stringify({ v: 1, environment: "default", target }));
    return url.href;
  };
  for (const [section, tab] of [
    ["identity", "General"],
    ["materialization", "General"],
    ["columns", "Columns"],
    ["dependencies", "Lineage"],
    ["checks", "Checks"],
    ["tests", "Tests"],
  ] as const) {
    await page.goto(urlFor({ kind: "asset-section", asset_id: typedId, section }));
    const properties = inspector(page);
    await expect(properties.getByRole("tab", { name: tab, exact: true })).toHaveAttribute(
      "data-state",
      "active",
    );
    const card = properties.locator(`[data-navigation-section="${section}"]`);
    await expect
      .poll(() => arrivals(page))
      .toEqual([expect.objectContaining({ section, visible: true })]);
    await expect(card).toBeFocused();
    await expect(card).not.toHaveAttribute("data-navigation-arrival", "true");
    expect(await arrivals(page)).toEqual([expect.objectContaining({ section, visible: true })]);
    expect(new URL(page.url()).searchParams.get("result")).toBe("inspect");
    expect(await page.evaluate(() => window.scrollY)).toBe(0);
  }

  await clearArrivals(page);
  await inspector(page).getByRole("tab", { name: "Columns", exact: true }).click();
  await expect(inspector(page).getByRole("tab", { name: "Columns", exact: true })).toHaveAttribute(
    "data-state",
    "active",
  );
  await appendFile(join(liveApp.workspaceDir, typedPath), "-- external workspace update\n");
  await expect
    .poll(
      async () =>
        (await (await page.request.get(`${liveApp.baseURL}/api/workspace`)).json()).pipelines
          .flatMap((pipeline: { assets: { id: string; content: string }[] }) => pipeline.assets)
          .find((asset: { id: string }) => asset.id === typedId)?.content,
    )
    .toContain("-- external workspace update");
  // Observe the full treatment window, including any background workspace updates.
  await page.waitForTimeout(1000);
  expect(await arrivals(page)).toEqual([]);

  await page.goBack();
  await expect.poll(async () => (await arrivals(page)).at(-1)?.section).toBe("checks");
  await expect(inspector(page).locator('[data-navigation-section="checks"]')).toBeFocused();
  await page.goForward();
  await expect.poll(async () => (await arrivals(page)).at(-1)?.section).toBe("columns");
  await expect(inspector(page).locator('[data-navigation-section="columns"]')).toBeFocused();

  await page.goto(
    urlFor({ kind: "asset-column", asset_id: typedId, column: "total_amount", field: "type" }),
  );
  await expect
    .poll(() => arrivals(page))
    .toEqual([expect.objectContaining({ tag: "INPUT", section: null, visible: true })]);
  await expect(inspector(page).getByRole("textbox", { name: "Type", exact: true })).toBeFocused();
  expect(await arrivals(page)).toEqual([
    expect.objectContaining({ tag: "INPUT", section: null, visible: true }),
  ]);
  await page.goto(
    urlFor({
      kind: "asset-section",
      asset_id: typedId,
      section: "checks",
      column: "total_amount",
      check_name: "not_null",
    }),
  );
  const check = inspector(page).locator('[data-column-check="total_amount:not_null"]');
  await expect
    .poll(() => arrivals(page))
    .toEqual([expect.objectContaining({ tag: "SPAN", section: null, visible: true })]);
  await expect(check).toBeFocused();
  await expect(check).not.toHaveAttribute("data-navigation-arrival", "true");
  expect(await arrivals(page)).toEqual([
    expect.objectContaining({ tag: "SPAN", section: null, visible: true }),
  ]);
  await page.goto(
    urlFor({ kind: "asset-column", asset_id: typedId, column: "deleted", field: "type" }),
  );
  await expect(inspector(page).getByRole("alert")).toContainText("missing or ambiguous");
  await page.waitForTimeout(1000);
  expect(await arrivals(page)).toEqual([]);
  await page.goto(
    urlFor({
      kind: "asset-section",
      asset_id: typedId,
      section: "checks",
      column: "total_amount",
      check_name: "deleted",
    }),
  );
  await expect(inspector(page).getByRole("alert")).toContainText("linked check is missing");
  await page.waitForTimeout(1000);
  expect(await arrivals(page)).toEqual([]);
});
