import { expect, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { liveTest as test, type LiveApp } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

async function openNavigator(page: Page, section: "Connections" | "Environments") {
  const navigator = page.getByTestId("settings-navigator");
  if (test.info().project.name.includes("mobile")) {
    await expect(page.getByTestId("project-settings-scroll")).toBeVisible();
    if (!(await navigator.isVisible()))
      await page.getByRole("tab", { name: section, exact: true }).click();
  }
  await expect(navigator).toBeVisible();
  return navigator;
}

async function addEnvironment(page: Page, liveApp: LiveApp, name: string) {
  const response = await page.request.post(`${liveApp.baseURL}/api/config/environments`, {
    data: { name },
  });
  expect(response.ok(), await response.text()).toBe(true);
}

test("navigates connections in the sidebar, protects a draft and saves before continuing", async ({
  page,
  liveApp,
}, info) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const created = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
    data: {
      environment_name: "default",
      name: "duckdb-second",
      type: "duckdb",
      values: { path: "duckdb-files/second.db" },
    },
  });
  expect(created.ok(), await created.text()).toBe(true);
  await page.goto(
    `${liveApp.baseURL}/project/connections?environment=default&connection=duckdb-default&result=inspect&editor=adhoc`,
  );
  const editor = page.getByRole("region", { name: "duckdb-default", exact: true });
  await expect(editor).toBeVisible();
  await editor.getByLabel("Name", { exact: true }).fill("renamed-warehouse");
  // Reflecting a field's location must not reset the draft or open a leave prompt.
  await editor.getByLabel("path", { exact: true }).focus();
  await expect(page.getByRole("alertdialog")).toHaveCount(0);
  await expect(editor.getByLabel("Name", { exact: true })).toHaveValue("renamed-warehouse");
  let nav = await openNavigator(page, "Connections");
  await nav.getByLabel("Filter connections").fill("second");
  // Keep the selected connection visible even while the list is filtered.
  await expect(nav.getByRole("link", { name: /duckdb-default/ })).toBeVisible();
  await nav.getByRole("link", { name: /duckdb-second/ }).click();
  const guard = page.getByRole("alertdialog", { name: "Unsaved changes" });
  await expect(guard).toBeVisible();
  await guard.getByRole("button", { name: "Stay", exact: true }).click();
  await expect(page).toHaveURL(/connection=duckdb-default/);
  nav = await openNavigator(page, "Connections");
  await nav.getByRole("link", { name: /duckdb-second/ }).click();
  await guard.getByRole("button", { name: "Save and continue" }).click();
  await expect(page.getByRole("region", { name: "duckdb-second", exact: true })).toBeVisible();
  if (info.project.name.includes("mobile")) await expect(nav).toBeHidden();
  expect(new URL(page.url()).searchParams.get("result")).toBe("inspect");
  expect(new URL(page.url()).searchParams.get("editor")).toBe("adhoc");
  expect(await readFile(join(liveApp.workspaceDir, ".bruin.yml"), "utf8")).toContain(
    "renamed-warehouse",
  );
  await page.screenshot({ path: info.outputPath("connections-editor.png") });
  expect(errors).toEqual([]);
});

test("failed saves keep the route and draft, and browser Back can discard explicitly", async ({
  page,
  liveApp,
}) => {
  await page.goto(`${liveApp.baseURL}/project/connections`);
  const nav = await openNavigator(page, "Connections");
  await nav.getByRole("link", { name: /duckdb-default/ }).click();
  const editor = page.getByRole("region", { name: "duckdb-default", exact: true });
  await editor.getByLabel("Name", { exact: true }).fill("unsaved");
  await page.route("**/api/config/connections", async (route) => {
    if (route.request().method() === "PUT")
      await route.fulfill({
        status: 409,
        json: { status: "error", error: "A concurrent configuration change needs review." },
      });
    else await route.continue();
  });
  // History navigation is the same guard as item selection, not a second prompt.
  await page.evaluate(() => history.back());
  const guard = page.getByRole("alertdialog", { name: "Unsaved changes" });
  await guard.getByRole("button", { name: "Save and continue" }).click();
  await expect(guard).toContainText("Could not save");
  await guard.getByRole("button", { name: "Stay", exact: true }).click();
  await expect(editor.getByLabel("Name", { exact: true })).toHaveValue("unsaved");
  await expect(page).toHaveURL(/connection=duckdb-default/);
  await page.evaluate(() => history.back());
  await guard.getByRole("button", { name: "Discard", exact: true }).click();
  await expect(editor).toBeHidden();
});

test("cancelling a tool navigation keeps the active sidebar and mobile tab", async ({
  page,
  liveApp,
}, info) => {
  await page.goto(
    `${liveApp.baseURL}/project/connections?environment=default&connection=duckdb-default`,
  );
  const editor = page.getByRole("region", { name: "duckdb-default", exact: true });
  await editor.getByLabel("Name", { exact: true }).fill("draft");
  const mobile = info.project.name.includes("mobile");
  await page.getByRole(mobile ? "tab" : "link", { name: "Environments", exact: true }).click();
  const guard = page.getByRole("alertdialog", { name: "Unsaved changes" });
  await guard.getByRole("button", { name: "Stay", exact: true }).click();
  await expect(editor.getByLabel("Name", { exact: true })).toHaveValue("draft");
  await expect(
    page.getByRole(mobile ? "tab" : "link", { name: "Connections", exact: true }),
  ).toHaveAttribute(mobile ? "aria-selected" : "aria-current", mobile ? "true" : "page");
  await page.getByRole(mobile ? "tab" : "link", { name: "Environments", exact: true }).click();
  await guard.getByRole("button", { name: "Discard", exact: true }).click();
  await expect(page).toHaveURL(/\/project\/environments/);
});

test("environment editing is addressable, keeps execution independent and saves guardrails", async ({
  page,
  liveApp,
  context,
}, info) => {
  await addEnvironment(page, liveApp, "production");
  await page.goto(`${liveApp.baseURL}/project/environments?environment=production`);
  const editor = page.getByRole("region", { name: "production", exact: true });
  await expect(editor).toBeVisible();
  await expect(page.getByRole("button", { name: "Execution context" })).toContainText("default");
  await editor.getByLabel("Schema prefix", { exact: true }).fill("prod_");
  await editor.getByRole("switch", { name: "Protected", exact: true }).check();
  const saved = page.waitForResponse(
    (response) =>
      response.url().endsWith("/config/environments") && response.request().method() === "PUT",
  );
  await editor.getByRole("button", { name: "Save changes", exact: true }).click();
  expect((await saved).ok()).toBe(true);
  await expect(editor.getByLabel("Schema prefix", { exact: true })).toHaveValue("prod_");
  expect(await readFile(join(liveApp.workspaceDir, ".renart/environments.yml"), "utf8")).toContain(
    "protected: true",
  );
  const cold = await context.newPage();
  await cold.goto(page.url());
  await expect(
    cold
      .getByRole("region", { name: "production", exact: true })
      .getByLabel("Schema prefix", { exact: true }),
  ).toHaveValue("prod_");
  await expect(cold.getByRole("button", { name: "Execution context" })).toContainText("default");
  await cold.close();
  await editor.getByRole("button", { name: "Use for execution" }).click();
  await expect(page.getByRole("button", { name: "Execution context" })).toContainText("production");
  await page.screenshot({ path: info.outputPath("environment-editor.png") });
  await editor.getByRole("button", { name: "Clone", exact: true }).click();
  const clone = page.getByRole("region", { name: "Clone production", exact: true });
  await clone.getByLabel("Name", { exact: true }).fill("staging");
  await clone.getByRole("button", { name: "Clone environment", exact: true }).click();
  await expect(page.getByRole("region", { name: "staging", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Execution context" })).toContainText("production");
  const policy = await (
    await page.request.get(`${liveApp.baseURL}/api/config/environment-policies/staging`)
  ).json();
  expect(policy.policy.protected).toBe(true);
});

test("creates a connection in the main editor and keeps it selected after save", async ({
  page,
  liveApp,
}, info) => {
  await page.goto(`${liveApp.baseURL}/project/connections`);
  await page.getByRole("button", { name: "New connection", exact: true }).click();
  const editor = page.getByRole("region", { name: "New connection", exact: true });
  await editor.getByLabel("Type", { exact: true }).click();
  await page.getByRole("option", { name: "duckdb", exact: true }).click();
  await editor.getByLabel("Name", { exact: true }).fill("scratch");
  await editor.getByLabel("path", { exact: true }).fill("duckdb-files/scratch.db");
  await editor.getByRole("button", { name: "Create connection", exact: true }).click();
  const saved = page.getByRole("region", { name: "scratch", exact: true });
  await expect(saved).toBeVisible();
  await expect(saved.getByLabel("path", { exact: true })).toHaveValue("duckdb-files/scratch.db");
  expect(await readFile(join(liveApp.workspaceDir, ".bruin.yml"), "utf8")).toContain(
    "name: scratch",
  );
  await page.emulateMedia({ colorScheme: "dark" });
  await expect(page.locator("html")).toHaveClass(/dark/);
  await page.screenshot({ path: info.outputPath("settings-dark.png"), animations: "disabled" });
});

test("a settings load failure stops retrying until the user retries", async ({ page, liveApp }) => {
  let failed = true;
  let requests = 0;
  await page.route("**/api/config", async (route) => {
    requests++;
    if (failed)
      await route.fulfill({
        status: 503,
        json: { status: "error", error: "Fixture config unavailable." },
      });
    else await route.continue();
  });
  await page.goto(`${liveApp.baseURL}/project/connections`);
  const retry = page.getByRole("button", { name: "Retry loading settings" });
  await expect(retry).toBeVisible();
  // Rendering the failure is stable: the retry remains usable and no spinner loops.
  await expect(page.getByText("Fixture config unavailable.", { exact: true })).toBeVisible();
  expect(requests).toBe(1);
  failed = false;
  await retry.click();
  await expect(page.getByRole("button", { name: "New connection", exact: true })).toBeVisible();
  expect(requests).toBe(2);
});

test("opens a cold field link inside a collapsed section without a settings dialog", async ({
  page,
  liveApp,
  context,
}) => {
  const detail = {
    v: 1,
    environment: "default",
    target: { kind: "connection", connection: "duckdb-default", field: "max_concurrent_assets" },
  };
  const url = new URL(`${liveApp.baseURL}/project/connections`);
  const config = await (await page.request.get(`${liveApp.baseURL}/api/config`)).json();
  url.searchParams.set("project", config.project_id);
  url.searchParams.set("detail", JSON.stringify(detail));
  await page.goto(url.href);
  await expect(
    page
      .getByRole("region", { name: "duckdb-default", exact: true })
      .getByLabel("max_concurrent_assets", { exact: true }),
  ).toBeFocused();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const cold = await context.newPage();
  await cold.goto(page.url());
  await expect(cold.getByLabel("max_concurrent_assets", { exact: true })).toBeFocused();
  await cold.close();
});

test("reports ambiguous and missing identities without selecting another connection", async ({
  page,
  liveApp,
}) => {
  await addEnvironment(page, liveApp, "production");
  const response = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
    data: {
      environment_name: "production",
      name: "duckdb-default",
      type: "duckdb",
      values: { path: "prod.db" },
    },
  });
  expect(response.ok(), await response.text()).toBe(true);
  await page.goto(`${liveApp.baseURL}/project/connections?connection=duckdb-default`);
  await expect(
    page.getByText("The linked connection is missing or ambiguous.", { exact: false }),
  ).toBeVisible();
  await expect(page.getByLabel("Name", { exact: true })).toHaveCount(0);
  await page.goto(
    `${liveApp.baseURL}/project/connections?environment=absent&connection=duckdb-default`,
  );
  await expect(
    page.getByText("The linked connection is no longer available in this environment."),
  ).toBeVisible();
});

test.describe("inherited configuration", () => {
  test.use({ fixtureName: "nested-browser-workspace", workspaceSubdirectory: "child" });
  test("requires deliberate opt-in before saving the shared parent file", async ({
    page,
    liveApp,
  }) => {
    const config = await (await page.request.get(`${liveApp.baseURL}/api/config`)).json();
    expect(config.configuration_inherited).toBe(true);
    expect(config.configuration_path).toBe("../.bruin.yml");
    const connection = config.environments[0].connections[0].name;
    await page.goto(
      `${liveApp.baseURL}/project/connections?environment=default&connection=${encodeURIComponent(connection)}`,
    );
    const editor = page.getByRole("region", { name: connection, exact: true });
    const original = await readFile(join(liveApp.workspaceDir, "../.bruin.yml"), "utf8");
    await expect(editor.getByRole("button", { name: "Save changes", exact: true })).toBeDisabled();
    await editor.getByRole("button", { name: "Allow editing shared configuration" }).click();
    await expect(editor.getByRole("button", { name: "Save changes", exact: true })).toBeEnabled();
    expect(await readFile(join(liveApp.workspaceDir, "../.bruin.yml"), "utf8")).toBe(original);
  });
});
