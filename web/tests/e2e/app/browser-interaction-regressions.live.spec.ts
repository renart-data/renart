import { expect } from "@playwright/test";
import { mkdir, readFile, rename, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
const pipeline = Buffer.from("analytics").toString("base64url");

test("mobile Query activation runs once and keeps its tab selected", async ({
  page,
  liveApp,
  isMobile,
}) => {
  test.skip(!isMobile, "This regression concerns the mobile tool tabs.");
  await page.goto(`${liveApp.baseURL}/pipelines/${pipeline}/canvas?result=inspect&editor=asset`);
  const query = page
    .getByRole("tablist", { name: "build tools" })
    .getByRole("tab", { name: "Query", exact: true });
  await query.click();
  await expect(query).toHaveAttribute("aria-selected", "true");
  await expect(page.getByTestId("adhoc-editor-workspace")).toBeVisible();
  await query.press("Enter");
  await expect(query).toHaveAttribute("aria-selected", "true");
  expect(new URL(page.url()).searchParams.get("editor")).toBe("adhoc");
  expect(new URL(page.url()).searchParams.get("result")).toBe("inspect");
  await query.click();
  await expect(query).toHaveAttribute("aria-selected", "true");
  await expect(page.getByTestId("adhoc-editor-workspace")).toBeVisible();
});

test("uses skeletons while the Data Browser loads connections", async ({
  page,
  liveApp,
  isMobile,
}, info) => {
  let release!: () => void;
  const waiting = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/api/data-browser/connections*", async (route) => {
    await waiting;
    await route.continue();
  });
  await page.goto(`${liveApp.baseURL}/pipelines/${pipeline}/canvas`);
  await (
    isMobile
      ? page.getByRole("tab", { name: "Data", exact: true })
      : page.getByRole("button", { name: "Data Browser", exact: true })
  ).click();
  const loading = page.getByTestId("data-browser-loading");
  try {
    await expect(loading).toBeVisible();
    await expect(loading).toHaveAttribute("aria-busy", "true");
    await expect(loading.locator('[data-slot="skeleton"]')).toHaveCount(15);
    await expect(loading.locator('[data-slot="spinner"]')).toHaveCount(0);
    await page.screenshot({ path: info.outputPath("browser-skeletons.png") });
  } finally {
    release();
  }
  await expect(loading).toBeHidden();
  await expect(page.getByRole("button", { name: /duckdb-default.*DuckDB/ })).toBeVisible();
});

test("previews a saved DuckDB view over a project-relative Parquet file", async ({
  page,
  liveApp,
  isMobile,
}, info) => {
  test.setTimeout(60000);
  const sql = async (query: string) => {
    const response = await page.request.post(`${liveApp.baseURL}/api/sql/query`, {
      data: { connection: "duckdb-default", environment: "default", query },
    });
    expect(response.ok(), await response.text()).toBe(true);
    const result = await response.json();
    expect(result.status, JSON.stringify(result)).toBe("ok");
    return result;
  };
  const path = join(liveApp.workspaceDir, "browser-view.parquet").replaceAll("'", "''");
  await sql(`COPY (SELECT 42 AS value) TO '${path}' (FORMAT PARQUET)`);
  await sql("CREATE VIEW main.relative_view AS SELECT * FROM 'browser-view.parquet'");
  await page.goto(`${liveApp.baseURL}/pipelines/${pipeline}/canvas`);
  await (
    isMobile
      ? page.getByRole("tab", { name: "Data", exact: true })
      : page.getByRole("button", { name: "Data Browser", exact: true })
  ).click();
  await page.getByRole("button", { name: /duckdb-default.*DuckDB/ }).click();
  await page.getByRole("button", { name: "main", exact: true }).click();
  await page.getByRole("link", { name: "relative_view", exact: true }).click();
  const response = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/data-browser/preview") &&
      response.request().method() === "POST",
  );
  await page.getByRole("button", { name: "Preview rows", exact: true }).click();
  const preview = await response;
  expect(preview.ok(), await preview.text()).toBe(true);
  expect((await preview.json()).rows).toEqual([{ value: 42 }]);
  await expect(page.getByRole("gridcell", { name: "value, row 1: 42", exact: true })).toBeVisible();
  await page.getByRole("tab", { name: "SQL", exact: true }).click();
  const definition = page.getByTestId("data-browser-view-definition");
  await expect(definition).toContainText("browser-view.parquet");
  await expect(definition.locator("pre span").first()).toBeVisible();
  const bookmark = page.url();
  expect(JSON.parse(new URL(bookmark).searchParams.get("detail")!).target.section).toBe(
    "definition",
  );
  await rename(
    join(liveApp.workspaceDir, "browser-view.parquet"),
    join(liveApp.workspaceDir, "browser-view.parquet.moved"),
  );
  const previews: string[] = [];
  page.on("request", (request) => {
    if (request.url().endsWith("/api/data-browser/preview")) previews.push(request.url());
  });
  await page.goto(bookmark);
  await expect(page.getByRole("tab", { name: "SQL", exact: true })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(definition).toContainText("browser-view.parquet");
  await expect(page.getByRole("alert")).toContainText("active project root");
  await expect(page.getByRole("alert")).toContainText(liveApp.workspaceDir);
  expect(previews).toEqual([]);
  await page.screenshot({ path: info.outputPath("view-definition-missing-file.png") });
});

test("opens earthquake SQL test fixtures without secure-context UUID support", async ({
  page,
  liveApp,
  isMobile,
}) => {
  const definition = await readFile(
    resolve(__dirname, "../../fixtures/sql-unit-tests/window_summary.sql"),
    "utf8",
  );
  await mkdir(join(liveApp.workspaceDir, "analytics/assets/earthquakes"), { recursive: true });
  await writeFile(
    join(liveApp.workspaceDir, "analytics/assets/earthquakes/window_summary.sql"),
    definition,
  );
  // HTTP on a LAN host lacks this secure-context API; localhost masks the bug.
  await page.addInitScript(() =>
    Object.defineProperty(Crypto.prototype, "randomUUID", { value: undefined, configurable: true }),
  );
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const asset = Buffer.from("analytics/assets/earthquakes/window_summary.sql").toString(
    "base64url",
  );
  await page.goto(
    `${liveApp.baseURL}/pipelines/${pipeline}/assets/${asset}/code?result=inspect&editor=asset`,
  );
  if (isMobile) await page.getByRole("button", { name: "Asset properties", exact: true }).click();
  const properties = page.getByTestId("asset-inspector").filter({ visible: true });
  await properties.getByRole("tab", { name: "Tests", exact: true }).click();
  await properties.getByRole("button", { name: "Add test", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Add SQL unit test", exact: true });
  await expect(dialog).toBeVisible();
  await expect(dialog.locator(".monaco-editor")).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Save test", exact: true })).toBeEnabled();
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(properties.getByTestId("asset-unit-tests")).toBeVisible();
  expect(errors).toEqual([]);
});
