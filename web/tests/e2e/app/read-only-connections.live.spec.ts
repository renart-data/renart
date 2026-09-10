import { expect, type Page } from "@playwright/test";
import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { liveTest as test, type LiveApp } from "../live-app-fixture";
import type { WorkspaceConfigResponse } from "../../../lib/generated/api-types";

const pipelineId = Buffer.from("analytics").toString("base64url");
const canvasPath = `/pipelines/${pipelineId}/canvas?result=inspect&editor=asset`;

async function setAccess(page: Page, app: LiveApp, mode: "read_only" | "read_write") {
  const config: WorkspaceConfigResponse = await (
    await page.request.get(`${app.baseURL}/api/config`)
  ).json();
  const connection = config.environments
    .find((env) => env.name === "default")!
    .connections.find((conn) => conn.name === "duckdb-default")!;
  const response = await page.request.put(`${app.baseURL}/api/config/connections`, {
    data: {
      environment_name: "default",
      current_name: connection.name,
      name: connection.name,
      type: connection.type,
      values: connection.values,
      access_mode: mode,
      policy_revision: config.connection_policy_revision,
    },
  });
  expect(response.ok(), await response.text()).toBe(true);
}

async function query(page: Page, app: LiveApp, connection: string, sql: string) {
  const response = await page.request.post(`${app.baseURL}/api/sql/query`, {
    data: { connection, environment: "default", query: sql },
  });
  return { response, body: await response.json() };
}

async function openBrowser(page: Page) {
  // Tool selection and panel visibility are independent and survive reloads.
  // Select a different tool first rather than toggling an already-active Data tab.
  if (!test.info().project.name.includes("mobile"))
    await page.getByRole("button", { name: "Project resources", exact: true }).click();
  await (
    test.info().project.name.includes("mobile")
      ? page.getByRole("tab", { name: "Data", exact: true })
      : page.getByRole("button", { name: "Data Browser", exact: true })
  ).click();
  await expect(
    page.getByRole("button", { name: "Refresh data sources", exact: true }),
  ).toBeVisible();
}

async function materialize(page: Page, app: LiveApp, path: string, fullRefresh = false) {
  const id = Buffer.from(path).toString("base64url");
  const response = await page.request.post(
    `${app.baseURL}/api/assets/${id}/materialize/stream?environment=default&full_refresh=${fullRefresh}`,
    { timeout: 90000 },
  );
  const stream = await response.text();
  expect(response.ok(), stream).toBe(true);
  const final = JSON.parse(
    stream
      .split(/\r?\n/)
      .reverse()
      .find((line) => line.startsWith("data: "))!
      .slice(6),
  );
  expect(final.status, stream).toBe("ok");
}

test.describe("Read-only connections", () => {
  test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
  test.setTimeout(120000);

  test("identifies invalid access configuration and refuses queries", async ({ page, liveApp }) => {
    await writeFile(
      join(liveApp.workspaceDir, ".renart/environments.yml"),
      "environments:\n  default:\n    connections:\n      duckdb-default:\n        access_mode: invalid-mode\n",
    );
    const projects = await (await page.request.get(`${liveApp.baseURL}/api/projects`)).json();
    const url = new URL(`${liveApp.baseURL}/project/connections`);
    url.searchParams.set("project", projects.default_project_id);
    await page.goto(url.href);
    await expect(
      page.getByText("Connection access needs attention", { exact: true }),
    ).toBeVisible();
    await expect(page.getByText("Secret bindings need attention", { exact: true })).toHaveCount(0);
    const result = await query(page, liveApp, "duckdb-default", "select 1 as value");
    expect(result.body.status).not.toBe("ok");
    expect(JSON.stringify(result.body)).toContain("invalid-mode");
  });

  test("saves routed access settings and keeps a dirty draft when another tab changes policy", async ({
    page,
    liveApp,
    context,
  }, info) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    const projects = await (await page.request.get(`${liveApp.baseURL}/api/projects`)).json();
    const url = new URL(`${liveApp.baseURL}/project/connections`);
    url.searchParams.set("project", projects.default_project_id);
    url.searchParams.set("environment", "default");
    url.searchParams.set("connection", "duckdb-default");
    url.searchParams.set(
      "detail",
      JSON.stringify({
        v: 1,
        environment: "default",
        target: { kind: "connection", connection: "duckdb-default", field: "access_mode" },
      }),
    );
    await page.goto(url.href);
    const dialog = page.getByRole("region", { name: "duckdb-default", exact: true });
    const access = dialog.getByRole("combobox", { name: "Access", exact: true });
    await expect(access).toBeFocused();
    await access.click();
    await page.getByRole("option", { name: "Read-only", exact: true }).click();
    const saved = page.waitForResponse(
      (response) =>
        response.url().endsWith("/config/connections") && response.request().method() === "PUT",
    );
    await dialog.getByRole("button", { name: "Save changes", exact: true }).click();
    expect((await saved).ok()).toBe(true);
    await expect(dialog.getByRole("combobox", { name: "Access", exact: true })).toContainText(
      "Read-only",
    );
    const policy = await readFile(join(liveApp.workspaceDir, ".renart/environments.yml"), "utf8");
    expect(policy).toContain("access_mode: read_only");
    const cold = await context.newPage();
    await cold.goto(url.href);
    const coldDialog = cold.getByRole("region", { name: "duckdb-default", exact: true });
    await expect(coldDialog.getByRole("combobox", { name: "Access", exact: true })).toBeFocused();
    await expect(coldDialog.getByRole("combobox", { name: "Access", exact: true })).toContainText(
      "Read-only",
    );
    await cold.close();
    await page.goto(url.href);
    await dialog.getByRole("textbox", { name: "Name", exact: true }).fill("unsaved-source");
    await setAccess(page, liveApp, "read_write");
    // Wait for the actual config refresh, not a fixed delay. Its revision must
    // not reinitialize the open dialog or silently update its save token.
    await expect
      .poll(
        async () =>
          (await (await page.request.get(`${liveApp.baseURL}/api/config`)).json())
            .connection_policy_revision,
      )
      .toBeTruthy();
    await expect(dialog.getByRole("textbox", { name: "Name", exact: true })).toHaveValue(
      "unsaved-source",
    );
    const conflict = page.waitForResponse(
      (response) =>
        response.url().endsWith("/config/connections") && response.request().method() === "PUT",
    );
    await dialog.getByRole("button", { name: "Save changes", exact: true }).click();
    expect((await conflict).status()).toBe(409);
    await expect(
      page.getByRole("alert").filter({ hasText: /access settings changed/ }),
    ).toBeVisible();
    await expect(dialog.getByRole("textbox", { name: "Name", exact: true })).toHaveValue(
      "unsaved-source",
    );
    expect(errors).toEqual([]);
    await page.screenshot({ path: info.outputPath("read-only-settings.png") });
  });

  test("creates a read-only Source in the canvas and copies it into a writable Load destination", async ({
    page,
    liveApp,
  }, info) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    for (const sql of [
      "create schema if not exists raw",
      "create table raw.read_only_orders as select 42::integer as order_id",
    ]) {
      const result = await query(page, liveApp, "duckdb-default", sql);
      expect(result.body.status, JSON.stringify(result.body)).toBe("ok");
    }
    const output = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
      data: {
        environment_name: "default",
        name: "duckdb-output",
        type: "duckdb",
        values: { path: "duckdb-files/output.db" },
      },
    });
    expect(output.ok(), await output.text()).toBe(true);
    await setAccess(page, liveApp, "read_only");
    const denied = await query(page, liveApp, "duckdb-default", "delete from raw.read_only_orders");
    expect(JSON.stringify(denied.body)).toContain("read-only");
    expect(denied.body.status).not.toBe("ok");
    await page.goto(`${liveApp.baseURL}${canvasPath}`);
    await openBrowser(page);
    await expect(
      page.getByRole("button", { name: "Use duckdb-default in canvas", exact: true }),
    ).toHaveCount(0);
    await page.getByRole("button", { name: /duckdb-default.*DuckDB/ }).click();
    await page.getByRole("button", { name: "local Default", exact: true }).click();
    await page.getByRole("button", { name: "raw", exact: true }).click();
    await page.getByRole("button", { name: "Use read_only_orders in canvas", exact: true }).click();
    await page.getByTestId("data-browser-drop-target").click();
    const sourceDialog = page.getByRole("dialog", { name: "Create source asset" });
    await sourceDialog.getByRole("button", { name: "Create source asset", exact: true }).click();
    await expect(sourceDialog).toBeHidden();
    const sourcePath = join(
      liveApp.workspaceDir,
      "analytics/assets/local.raw/read_only_orders.asset.yml",
    );
    await expect
      .poll(async () => readFile(sourcePath, "utf8").catch(() => ""))
      .toContain("type: duckdb.source");
    await page.goto(`${liveApp.baseURL}${canvasPath}`);
    await openBrowser(page);
    while (await page.getByRole("button", { name: "Back", exact: true }).isVisible())
      await page.getByRole("button", { name: "Back", exact: true }).click();
    await page.getByRole("button", { name: "Use duckdb-output in canvas", exact: true }).click();
    await page
      .getByRole("button", { name: "Create Load after local.raw.read_only_orders", exact: true })
      .click();
    const loadDialog = page.getByRole("dialog", {
      name: /New downstream asset|New asset|Create downstream/,
    });
    await expect(loadDialog.getByLabel("Source connection", { exact: true })).toHaveValue(
      "duckdb-default",
    );
    await loadDialog.getByLabel("Asset name", { exact: true }).fill("analytics.read_only_copy");
    await loadDialog.getByRole("button", { name: "Create", exact: true }).click();
    await expect(loadDialog).toBeHidden();
    const loadPath = "analytics/assets/analytics/read_only_copy.asset.yml";
    const definition = await readFile(join(liveApp.workspaceDir, loadPath), "utf8");
    expect(definition).toContain("raw.read_only_orders");
    expect(definition).toContain("duckdb-output");
    const assetId = Buffer.from(loadPath).toString("base64url");
    const run = await page.request.post(
      `${liveApp.baseURL}/api/assets/${assetId}/materialize/stream?environment=default`,
      { timeout: 90000 },
    );
    const stream = await run.text();
    expect(run.ok(), stream).toBe(true);
    const final = JSON.parse(
      stream
        .split(/\r?\n/)
        .reverse()
        .find((line) => line.startsWith("data: "))!
        .slice(6),
    );
    expect(final.status, stream).toBe("ok");
    await materialize(page, liveApp, loadPath, true);
    const reportPath = "analytics/assets/read_only_report.sql";
    await writeFile(
      join(liveApp.workspaceDir, reportPath),
      "/* @bruin\nname: analytics.read_only_report\ntype: duckdb.sql\nconnection: duckdb-output\ndepends: [analytics.read_only_copy]\nmaterialization:\n  type: table\n@bruin */\nselect sum(order_id) as total from analytics.read_only_copy\n",
    );
    await materialize(page, liveApp, reportPath);
    expect(
      (await query(page, liveApp, "duckdb-output", "select total from analytics.read_only_report"))
        .body.rows,
    ).toEqual([{ total: 42 }]);
    const copied = await query(
      page,
      liveApp,
      "duckdb-output",
      "select order_id from analytics.read_only_copy",
    );
    expect(copied.body.rows).toEqual([{ order_id: 42 }]);
    const unchanged = await query(
      page,
      liveApp,
      "duckdb-default",
      "select order_id from raw.read_only_orders",
    );
    expect(unchanged.body.status).toBe("ok");
    expect(unchanged.body.rows).toEqual([{ order_id: 42 }]);
    expect(new URL(page.url()).searchParams.get("result")).toBe("inspect");
    expect(errors).toEqual([]);
    await page.screenshot({ path: info.outputPath("read-only-source-load.png") });
  });
});
