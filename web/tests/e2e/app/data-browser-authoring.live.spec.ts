import { expect, type Page } from "@playwright/test";
import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { liveTest as test } from "../live-app-fixture";

const pipelineId = Buffer.from("analytics").toString("base64url");
const canvasPath = `/pipelines/${pipelineId}/canvas?result=inspect&editor=asset`;
const MIME = "application/x-renart-data-browser";

async function openBrowser(page: Page, heading = "Data Browser") {
  const tool = test.info().project.name.includes("mobile")
    ? page.getByRole("tab", { name: "Data", exact: true })
    : page.getByRole("button", { name: "Data Browser", exact: true });
  await tool.click();
  await expect(page.getByRole("heading", { name: heading, exact: true })).toBeVisible();
}

test.describe("Data Browser authoring", () => {
  test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
  test.setTimeout(90000);

  test("creates a reviewed source without execution and cancels without writing", async ({
    page,
    liveApp,
  }, info) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    for (const query of [
      "create schema if not exists raw",
      "create table raw.browser_orders as select 42::integer as order_id",
    ]) {
      const setup = await page.request.post(`${liveApp.baseURL}/api/sql/query`, {
        data: { connection: "duckdb-default", environment: "default", query },
      });
      expect(setup.ok(), await setup.text()).toBe(true);
      expect((await setup.json()).status).toBe("ok");
    }
    const before = await (await page.request.get(`${liveApp.baseURL}/api/workspace`)).json();
    const commands: string[] = [];
    page.on("request", (request) => {
      if (
        request.method() === "POST" &&
        /\/(trigger|materialize|run|preview)$/.test(new URL(request.url()).pathname) &&
        !request.url().includes("/sources/preview")
      )
        commands.push(request.url());
    });
    await page.goto(`${liveApp.baseURL}${canvasPath}`);
    await openBrowser(page);
    await page.getByRole("button", { name: /duckdb-default.*DuckDB/ }).click();
    // This fixture's DuckDB catalog exposes the raw schema directly.
    await page.getByRole("button", { name: "raw", exact: true }).click();
    const useTable = page.getByRole("button", {
      name: "Use browser_orders in canvas",
      exact: true,
    });
    await expect(useTable).toBeVisible();
    if (info.project.name.includes("mobile")) {
      await useTable.click();
      await page.getByTestId("data-browser-drop-target").click();
    } else {
      const transfer = await page.evaluateHandle(() => new DataTransfer());
      const row = page.locator(
        '[data-testid="data-browser-transfer-item"][data-transfer-label="browser_orders"]',
      );
      await row.dispatchEvent("dragstart", { dataTransfer: transfer });
      const target = page.getByTestId("data-browser-drop-target");
      await expect(target).toBeVisible();
      expect(await transfer.evaluate((value) => value.types)).toContain(MIME);
      const foreign = await page.evaluateHandle((mime) => {
        const data = new DataTransfer();
        data.setData(mime, "another-window");
        return data;
      }, MIME);
      await target.dispatchEvent("drop", { dataTransfer: foreign });
      await expect(page.getByRole("dialog", { name: "Create source asset" })).toHaveCount(0);
      await target.dispatchEvent("dragover", { dataTransfer: transfer });
      await target.dispatchEvent("drop", { dataTransfer: transfer });
      await row.dispatchEvent("dragend", { dataTransfer: transfer });
    }
    const dialog = page.getByRole("dialog", { name: "Create source asset" });
    await expect(dialog.getByText("order_id", { exact: true })).toBeVisible();
    await expect(dialog.getByRole("button", { name: "Create source asset" })).toBeEnabled();
    await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
    const cancelled = await (await page.request.get(`${liveApp.baseURL}/api/workspace`)).json();
    expect(cancelled.pipelines.find((p: { id: string }) => p.id === pipelineId).assets.length).toBe(
      before.pipelines.find((p: { id: string }) => p.id === pipelineId).assets.length,
    );
    if (info.project.name.includes("mobile")) await openBrowser(page, "raw");
    await useTable.click();
    await page.getByTestId("data-browser-drop-target").click();
    await expect(dialog.getByRole("button", { name: "Create source asset" })).toBeEnabled();
    await dialog.getByRole("button", { name: "Create source asset" }).click();
    await expect(dialog).toBeHidden();
    await expect(
      page.getByTestId("lineage-asset").filter({ hasText: "browser_orders" }),
    ).toBeVisible();
    const source = await readFile(
      join(liveApp.workspaceDir, "analytics/assets/raw/browser_orders.asset.yml"),
      "utf8",
    );
    expect(source).toContain("type: duckdb.source");
    expect(source).toContain("order_id");
    expect(new URL(page.url()).searchParams.get("result")).toBe("inspect");
    expect(commands).toEqual([]);
    expect(errors).toEqual([]);
    await page.screenshot({ path: info.outputPath("source-created.png") });
  });

  test("prefills a downstream Load from a destination connection", async ({
    page,
    liveApp,
  }, info) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    const connection = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
      data: {
        environment_name: "default",
        name: "duckdb-output",
        type: "duckdb",
        values: { path: "duckdb-files/output.db" },
      },
    });
    expect(connection.ok(), await connection.text()).toBe(true);
    await page.goto(`${liveApp.baseURL}${canvasPath}`);
    await openBrowser(page);
    const target = page.getByRole("button", {
      name: "Create Load after analytics.customers",
      exact: true,
    });
    if (info.project.name.includes("mobile")) {
      await page.getByRole("button", { name: "Use duckdb-output in canvas", exact: true }).click();
      await expect(target).toBeVisible();
      await page.screenshot({ path: info.outputPath("load-placement.png") });
      await target.click();
    } else {
      const transfer = await page.evaluateHandle(() => new DataTransfer());
      const row = page.locator(
        '[data-testid="data-browser-transfer-item"][data-transfer-label="duckdb-output"]',
      );
      await row.dispatchEvent("dragstart", { dataTransfer: transfer });
      await expect(target).toBeVisible();
      await page.screenshot({ path: info.outputPath("load-placement.png") });
      await target.dispatchEvent("dragover", { dataTransfer: transfer });
      await target.dispatchEvent("drop", { dataTransfer: transfer });
      await row.dispatchEvent("dragend", { dataTransfer: transfer });
    }
    const dialog = page.getByRole("dialog", {
      name: /New downstream asset|New asset|Create downstream/,
    });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByLabel("Source connection", { exact: true })).toHaveValue(
      "duckdb-default",
    );
    await expect(dialog.getByText("duckdb-output", { exact: true })).toBeVisible();
    await dialog.getByLabel("Asset name", { exact: true }).fill("analytics.browser_load");
    await dialog.getByRole("button", { name: "Create", exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect
      .poll(async () => {
        const workspace = await (await page.request.get(`${liveApp.baseURL}/api/workspace`)).json();
        return workspace.pipelines
          .find((p: { id: string }) => p.id === pipelineId)
          .assets.find((a: { name: string }) => a.name === "analytics.browser_load");
      })
      .toBeTruthy();
    const asset = await readFile(
      join(liveApp.workspaceDir, "analytics/assets/analytics/browser_load.asset.yml"),
      "utf8",
    );
    expect(asset).toContain("type: load");
    expect(asset).toContain("duckdb-output");
    expect(asset).toContain("analytics.customers");
    const createdId = Buffer.from("analytics/assets/analytics/browser_load.asset.yml").toString(
      "base64url",
    );
    await expect(page).toHaveURL(new RegExp(`/assets/${createdId}/`));
    expect(new URL(page.url()).searchParams.get("result")).toBe("inspect");
    expect(errors).toEqual([]);
    await page.screenshot({ path: info.outputPath("load-created.png") });
  });

  test("offers source placement in an empty pipeline without leaving it", async ({
    page,
    liveApp,
  }) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    const created = await page.request.post(`${liveApp.baseURL}/api/pipelines`, {
      data: { path: "empty", name: "empty" },
    });
    expect(created.ok(), await created.text()).toBe(true);
    const table = await page.request.post(`${liveApp.baseURL}/api/sql/query`, {
      data: {
        connection: "duckdb-default",
        environment: "default",
        query: "create table empty_source as select 1::integer as id",
      },
    });
    expect(table.ok(), await table.text()).toBe(true);
    const emptyId = Buffer.from("empty").toString("base64url");
    await page.goto(`${liveApp.baseURL}/pipelines/${emptyId}/canvas?result=inspect&editor=asset`);
    await expect(page.getByRole("heading", { name: "No assets yet" })).toBeVisible();
    await openBrowser(page);
    await expect(page).toHaveURL(new RegExp(`/pipelines/${emptyId}/canvas`));
    await page.getByRole("button", { name: /duckdb-default.*DuckDB/ }).click();
    await page.getByRole("button", { name: "main", exact: true }).click();
    const useTable = page.getByRole("button", { name: "Use empty_source in canvas", exact: true });
    await useTable.click();
    await expect(page.getByTestId("data-browser-drop-target")).toBeVisible();
    // Keyboard users can leave placement without a dialog or a file write.
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("data-browser-drop-target")).toHaveCount(0);
    if (test.info().project.name.includes("mobile")) await openBrowser(page, "main");
    await useTable.click();
    await page.getByTestId("data-browser-drop-target").click();
    const dialog = page.getByRole("dialog", { name: "Create source asset" });
    await expect(dialog.getByText("id", { exact: true })).toBeVisible();
    await dialog.getByRole("button", { name: "Create source asset" }).click();
    await expect(dialog).toBeHidden();
    await expect(
      page.getByTestId("lineage-asset").filter({ hasText: "empty_source" }),
    ).toBeVisible();
    expect(new URL(page.url()).searchParams.get("result")).toBe("inspect");
    expect(errors).toEqual([]);
  });

  test("hides Ingestr creation for legacy workspaces but keeps SFTP and explicit opt-in", async ({
    page,
    liveApp,
  }) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await writeFile(
      join(liveApp.workspaceDir, "analytics/assets/analytics/legacy.asset.yml"),
      "name: analytics.legacy\ntype: ingestr\nparameters:\n  source_connection: duckdb-default\n  source_table: raw.legacy\n  destination: duckdb\n",
    );
    await page.goto(`${liveApp.baseURL}/data`);
    if (test.info().project.name.includes("mobile"))
      await page.getByRole("tab", { name: "Data", exact: true }).click();
    await page.getByRole("button", { name: "Other connection", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "New connection", exact: true });
    await dialog.getByLabel("Type", { exact: true }).click();
    await expect(page.getByRole("option", { name: "stripe", exact: true })).toHaveCount(0);
    await expect(page.getByRole("option", { name: "sftp", exact: true })).toBeAttached();
    await page.keyboard.press("Escape");
    await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
    await page.goto(`${liveApp.baseURL}/project/general`);
    await page.getByRole("switch", { name: "Enable ingestr sources" }).click();
    await expect(page.getByRole("switch", { name: "Enable ingestr sources" })).toBeChecked();
    await page.goto(`${liveApp.baseURL}/data`);
    if (test.info().project.name.includes("mobile"))
      await page.getByRole("tab", { name: "Data", exact: true }).click();
    await page.getByRole("button", { name: "Other connection", exact: true }).click();
    await dialog.getByLabel("Type", { exact: true }).click();
    await expect(page.getByRole("option", { name: "stripe", exact: true })).toBeAttached();
    expect(errors).toEqual([]);
  });
});
