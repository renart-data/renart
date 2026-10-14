import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { expect } from "@playwright/test";
import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "empty-workspace", isolateUserConfig: true });

test("imports existing DuckDB tables after selecting a schema", async ({ liveApp, page }) => {
  test.setTimeout(90000);
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const databasePath = join(liveApp.workspaceDir, "existing data.ddb");
  const config = await (await page.request.get(`${liveApp.baseURL}/api/config`)).json();
  const connection = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
    data: {
      environment_name: "default",
      name: "existing-data",
      type: "duckdb",
      values: { path: databasePath },
      policy_revision: config.connection_policy_revision,
    },
  });
  expect(connection.ok(), await connection.text()).toBe(true);
  for (const query of [
    "create table orders as select 42::integer as id",
    "create view recent_orders as select * from orders",
    "create schema analytics",
    "create table analytics.customers as select 7::integer as id",
  ]) {
    const response = await page.request.post(`${liveApp.baseURL}/api/sql/query`, {
      data: { connection: "existing-data", environment: "default", query },
    });
    expect(response.ok(), await response.text()).toBe(true);
    expect((await response.json()).status).toBe("ok");
  }

  await page.goto(`${liveApp.baseURL}/welcome`);
  await page.getByRole("button", { name: /Connect your data/ }).click();
  await page.getByLabel("Database", { exact: true }).click();
  await page.getByRole("option", { name: "DuckDB", exact: true }).click();
  const path = page.getByRole("textbox", { name: "path", exact: true });
  await expect(path).toHaveAttribute("placeholder", "Required");
  await path.fill(databasePath);
  await page.getByRole("button", { name: "Test connection" }).click();

  // DuckDB discovery lists schemas; the filesystem path must not become one.
  await page.locator("#connect-data-database").click();
  await page.getByRole("option", { name: "analytics", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("1 table in analytics");
  await page.getByRole("button", { name: "Choose tables" }).click();
  await expect(page.getByRole("heading", { name: "Pick tables to import" })).toBeVisible();
  await page.getByRole("checkbox", { name: "analytics.customers", exact: true }).click();
  await page.getByRole("button", { name: "Import 1 table", exact: true }).click();
  await expect(page).toHaveURL(/\/pipelines\/.+\/canvas/, { timeout: 60000 });

  const assetPath = join(liveApp.workspaceDir, "analytics/assets/analytics/customers.asset.yml");
  expect(existsSync(assetPath)).toBe(true);
  expect(readFileSync(assetPath, "utf8")).toContain("name: analytics.customers");
  expect(readFileSync(join(liveApp.workspaceDir, ".bruin.yml"), "utf8")).toContain(databasePath);
  expect(errors).toEqual([]);
});
