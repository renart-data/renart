import { expect, type Page } from "@playwright/test";
import { appendFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import type { NavigableTarget } from "../../../lib/resource-navigation";
import { liveTest as test } from "../live-app-fixture";
import { observeArrivals, arrivals, clearArrivals } from "../navigation-arrival-probe";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
test.setTimeout(90000);
const pipelineId = Buffer.from("analytics").toString("base64url");

test.afterEach(async ({ page }, info) => {
  if (info.status === info.expectedStatus || page.isClosed()) return;
  await info.attach("arrival-state", {
    body: JSON.stringify({
      arrivals: await arrivals(page),
      focused: await page.evaluate(() => document.activeElement?.outerHTML.slice(0, 1000)),
    }),
    contentType: "application/json",
  });
});

function watchCommands(page: Page) {
  const commands: string[] = [];
  page.on("request", (request) => {
    if (
      request.method() !== "GET" &&
      /\/(trigger|reexecute|cancel|run|execute|materialize|preview|transactions)(\/stream)?$/.test(
        new URL(request.url()).pathname,
      )
    )
      commands.push(request.url());
  });
  return commands;
}

async function targetURL(page: Page, baseURL: string, target: NavigableTarget) {
  const config = await (await page.request.get(`${baseURL}/api/config`)).json();
  const url = new URL(`${baseURL}/schedules/deployments`);
  url.searchParams.set("project", config.project_id);
  url.searchParams.set("detail", JSON.stringify({ v: 1, environment: "default", target }));
  return url.href;
}

test("a view-definition arrival reveals saved SQL without fetching preview rows", async ({
  page,
  liveApp,
}) => {
  const ordersId = Buffer.from("analytics/assets/analytics/orders.sql").toString("base64url");
  const result = await page.request.post(
    `${liveApp.baseURL}/api/assets/${ordersId}/materialize/stream?environment=default`,
    { timeout: 30000 },
  );
  expect(result.ok(), await result.text()).toBe(true);
  await observeArrivals(page);
  const commands = watchCommands(page);
  await page.goto(
    await targetURL(page, liveApp.baseURL, {
      kind: "data-object",
      section: "definition",
      address: {
        source_kind: "warehouse",
        connection: "duckdb-default",
        connection_type: "duckdb",
        schema: "analytics",
        name: "orders",
      },
    }),
  );
  const definition = page
    .getByTestId("routed-data-object")
    .getByRole("tabpanel", { name: "SQL", exact: true });
  await expect(definition).toBeFocused({ timeout: 20000 });
  await expect(page.getByTestId("data-browser-view-definition")).toContainText("total_amount");
  await expect.poll(() => arrivals(page)).toHaveLength(1);
  expect(commands).toEqual([]);
});

test("whole Data Browser sections highlight on arrival and history, but local tabs and missing columns stay quiet", async ({
  page,
  liveApp,
}, info) => {
  await writeFile(join(liveApp.workspaceDir, "arrival.csv"), "id,Total\n1,42\n");
  await observeArrivals(page);
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const commands = watchCommands(page);
  const target = {
    kind: "data-object" as const,
    address: { source_kind: "local_files" as const, path: "arrival.csv" },
    section: "schema" as const,
  };
  await page.goto(await targetURL(page, liveApp.baseURL, target));
  const detail = page.getByTestId("routed-data-object");
  const schema = detail.getByRole("tabpanel", { name: /Columns/ });
  await expect(schema).toBeFocused({ timeout: 20000 });
  await expect.poll(() => arrivals(page)).toHaveLength(1);
  await expect(schema).not.toHaveAttribute("data-navigation-arrival", "true");
  await detail.getByRole("link", { name: "Total", exact: true }).click();
  await expect(detail.locator('[data-focused-column="true"]')).toBeFocused();
  await expect.poll(() => arrivals(page)).toHaveLength(2);
  await page.goBack();
  await expect.poll(() => arrivals(page)).toHaveLength(3);
  await expect(schema).toBeFocused();
  await expect(schema).not.toHaveAttribute("data-navigation-arrival", "true");
  await clearArrivals(page);
  await detail.getByRole("tab", { name: "Preview", exact: true }).click();
  await expect(detail.getByText("No rows loaded", { exact: true })).toBeVisible();
  await expect(detail.getByRole("tab", { name: "Preview", exact: true })).toBeFocused();
  await expect
    .poll(async () => JSON.parse(new URL(page.url()).searchParams.get("detail")!).target.section)
    .toBe("rows");
  expect(await arrivals(page)).toEqual([]);
  await page.reload();
  const rows = detail.getByRole("tabpanel", { name: "Preview", exact: true });
  await expect(rows).toBeFocused();
  await expect.poll(() => arrivals(page)).toHaveLength(1);
  await page.screenshot({ path: info.outputPath("data-object-arrival.png") });
  await page.goto(await targetURL(page, liveApp.baseURL, { ...target, column: "deleted" }));
  await expect(page.getByRole("alert")).toContainText("linked column");
  expect(await arrivals(page)).toEqual([]);
  await page.goto(await targetURL(page, liveApp.baseURL, { ...target, section: "definition" }));
  await expect(page.getByText(/No view definition is available/)).toBeVisible();
  expect(await arrivals(page)).toEqual([]);
  expect(commands).toEqual([]);
  expect(errors).toEqual([]);
});

test("whole connections highlight their loaded header; local editing and missing fields do not", async ({
  page,
  liveApp,
}, info) => {
  await observeArrivals(page);
  await page.emulateMedia({ reducedMotion: "reduce", colorScheme: "dark" });
  const commands = watchCommands(page);
  const target = { kind: "connection" as const, connection: "duckdb-default" };
  await page.goto(await targetURL(page, liveApp.baseURL, target));
  const header = page.getByRole("group", { name: "Connection details", exact: true });
  await expect(header).toBeFocused({ timeout: 20000 });
  await expect
    .poll(() => arrivals(page))
    .toEqual([
      expect.objectContaining({ label: "Connection details", visible: true, animation: "none" }),
    ]);
  await page.screenshot({ path: info.outputPath("connection-arrival-reduced-motion.png") });
  await expect(header).not.toHaveAttribute("data-navigation-arrival", "true");
  await expect(header).toHaveCSS("outline-style", "none");
  expect(await arrivals(page)).toEqual([
    expect.objectContaining({ label: "Connection details", visible: true }),
  ]);
  await clearArrivals(page);
  const editor = page.getByRole("region", { name: "duckdb-default", exact: true });
  await editor.getByLabel("path", { exact: true }).focus();
  await expect
    .poll(async () => JSON.parse(new URL(page.url()).searchParams.get("detail")!).target.field)
    .toBe("path");
  await expect(editor.getByLabel("path", { exact: true })).toBeFocused();
  expect(await arrivals(page)).toEqual([]);
  await page.goto(await targetURL(page, liveApp.baseURL, { ...target, field: "deleted" }));
  await expect(page.getByRole("alert")).toContainText("linked field no longer exists");
  expect(await arrivals(page)).toEqual([]);
  expect(commands).toEqual([]);
});

test("presentation component arrivals use the real visible inspector without executing previews", async ({
  page,
  liveApp,
  isMobile,
}, info) => {
  // Cover the intermediate desktop width as well as the mobile Sheet. The
  // existing resource-navigation spec covers the wide docked inspector.
  if (!isMobile) await page.setViewportSize({ width: 1000, height: 800 });
  await writeFile(
    join(liveApp.workspaceDir, "arrival.report.yml"),
    `version: 1
id: arrival
title: Arrival report
datasets:
  values:
    connection: duckdb-default
    query: select 42 as value
filters:
  - id: region
    label: Region
    type: select
    options:
      values: [eu, us]
visualizations:
  - id: stable_plot
    dataset: values
    definition:
      version: 1
      type: table
sections:
  - id: summary
    title: Summary
    markdown: A saved report block.
`,
  );
  await observeArrivals(page);
  const commands = watchCommands(page);
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const presentation_id = Buffer.from("arrival.report.yml").toString("base64url");
  for (const [section, block_id, label] of [
    ["artifact", undefined, "Presentation settings"],
    ["dataset", "values", "Dataset settings"],
    ["filter", "region", "Control settings"],
    ["visualization", "stable_plot", "Visualization settings"],
    ["section", "summary", "Report block settings"],
  ] as const) {
    await page.goto(
      await targetURL(page, liveApp.baseURL, {
        kind: "presentation",
        presentation_id,
        section,
        block_id,
      }),
    );
    const owner = page.getByTestId("presentation-inspector").filter({ visible: true });
    const destination = owner.getByRole("group", { name: label, exact: true });
    await expect(destination).toBeFocused({ timeout: 20000 });
    await expect
      .poll(() => arrivals(page))
      .toEqual([expect.objectContaining({ label, visible: true })]);
    await expect(destination).not.toHaveAttribute("data-navigation-arrival", "true");
    await expect(destination).toHaveCSS("outline-style", "none");
    if (section === "visualization")
      await page.screenshot({ path: info.outputPath("presentation-arrival.png") });
    {
      await clearArrivals(page);
      await page.keyboard.press("Escape");
      await page.getByRole("button", { name: "Open inspector", exact: true }).click();
      await expect(destination).toBeVisible();
      await expect(destination).not.toHaveAttribute("data-navigation-arrival", "true");
      expect(await arrivals(page)).toEqual([]);
    }
  }
  await page.goto(
    await targetURL(page, liveApp.baseURL, {
      kind: "presentation",
      presentation_id,
      block_id: "deleted",
    }),
  );
  await expect(page.getByRole("alert").filter({ hasText: "The linked" })).toContainText(
    "missing or ambiguous",
  );
  expect(await arrivals(page)).toEqual([]);
  expect(commands).toEqual([]);
  expect(errors).toEqual([]);
});

test("run arrivals wait for real rows, repeat, follow history and leave Output alone for timeline links", async ({
  page,
  liveApp,
}, info) => {
  const response = await page.request.post(
    `${liveApp.baseURL}/api/pipelines/${pipelineId}/trigger`,
    { data: { source: "working_tree", environment: "default" } },
  );
  expect(response.ok(), await response.text()).toBe(true);
  const { run } = await response.json();
  await expect
    .poll(
      async () =>
        (await (await page.request.get(`${liveApp.baseURL}/api/runs/${run.id}`)).json()).run.status,
      { timeout: 30000 },
    )
    .toMatch(/success|failed|cancelled/);
  await observeArrivals(page);
  const commands = watchCommands(page);
  let release!: () => void;
  const ready = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route(`**/api/runs/${run.id}`, async (route) => {
    await ready;
    await route.continue();
  });
  const url = new URL(`${liveApp.baseURL}/runs/${run.id}`);
  url.searchParams.set("run_asset", "analytics.orders");
  url.searchParams.set("run_focus", "events");
  url.searchParams.set("run_tab", "output");
  await page.goto(url.href);
  await expect(page.getByText("Loading run details", { exact: true })).toBeVisible();
  expect(await arrivals(page)).toEqual([]);
  release();
  const event = page.getByTestId("run-event-row").filter({ hasText: "analytics.orders" }).first();
  const timeline = page
    .getByTestId("run-timeline-asset-label")
    .filter({ hasText: "analytics.orders" });
  await expect(event).toBeFocused({ timeout: 20000 });
  await expect(page.getByRole("tab", { name: "Events", exact: true })).toHaveAttribute(
    "data-state",
    "active",
  );
  await expect.poll(() => arrivals(page)).toHaveLength(1);
  await expect(event).not.toHaveAttribute("data-navigation-arrival", "true");
  for (let repeat = 0; repeat < 2; repeat++) {
    await clearArrivals(page);
    await timeline.click();
    await expect(event).toBeFocused();
    await expect.poll(() => arrivals(page)).toHaveLength(1);
    await expect(event).not.toHaveAttribute("data-navigation-arrival", "true");
  }
  await event.press("Enter");
  await expect(timeline).toBeFocused();
  await expect.poll(() => arrivals(page)).toHaveLength(2);
  await expect(timeline).not.toHaveAttribute("data-navigation-arrival", "true");
  await page.goBack();
  await expect(event).toBeFocused();
  await expect.poll(() => arrivals(page)).toHaveLength(3);
  await page.getByRole("tab", { name: "Output", exact: true }).click();
  await clearArrivals(page);
  await appendFile(
    join(liveApp.workspaceDir, "analytics/assets/analytics/orders.sql"),
    "\n-- unrelated workspace event\n",
  );
  await expect(page.getByRole("tab", { name: "Output", exact: true })).toHaveAttribute(
    "data-state",
    "active",
  );
  expect(await arrivals(page)).toEqual([]);
  url.searchParams.set("run_focus", "timeline");
  await page.goto(url.href);
  await expect(timeline).toBeFocused();
  await expect.poll(() => arrivals(page)).toHaveLength(1);
  await expect(page.getByRole("tab", { name: "Output", exact: true })).toHaveAttribute(
    "data-state",
    "active",
  );
  expect(await page.evaluate(() => window.scrollY)).toBe(0);
  await page.screenshot({ path: info.outputPath("run-timeline-arrival.png") });
  url.searchParams.set("run_asset", "deleted");
  await page.goto(url.href);
  await expect(page.getByRole("alert")).toContainText("linked asset has no timing");
  expect(await arrivals(page)).toEqual([]);
  expect(commands).toEqual([]);
});
