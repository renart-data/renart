import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readdirSync, statSync, readFileSync } from "node:fs";
import { basename, dirname, join } from "node:path";

import { expect } from "@playwright/test";

import { liveTest as test, timeoutForRetry, type LiveApp } from "../live-app-fixture";

type ProjectListResponse = {
  status: string;
  default_project_id: string;
  projects: Array<{ id: string; name: string; path: string; open: boolean }>;
};

type WorkspaceResponse = {
  selected_environment: string;
  pipelines: Array<{
    id: string;
    name: string;
    path: string;
    assets: Array<{ id: string; name: string; type: string }>;
  }>;
};

type StalenessResponse = {
  assets: Array<{ asset_name: string; status: string }>;
};

function gitLog(dir: string): string {
  return execFileSync("git", ["-C", dir, "log", "--format=%s"], { encoding: "utf8" });
}

test.describe("first-run onboarding", () => {
  test.use({ fixtureName: "empty-workspace" });

  test("redirects an empty workspace to the welcome screen", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/`);

    await expect(page).toHaveURL(/\/welcome/, { timeout: 15000 });
    await expect(page.getByRole("heading", { name: "Welcome to Renart" })).toBeVisible();
    await expect(page.getByRole("button", { name: /Explore a demo/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /Connect your data/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /Start from scratch/ })).toBeVisible();

    await page.getByRole("button", { name: /Explore a demo/ }).click();
    // The offline Product analytics demo is preselected and fits the list.
    await expect(page.getByRole("radio", { name: /Product analytics/ })).toHaveAttribute(
      "aria-checked",
      "true",
    );
    for (const demo of [
      "Product analytics",
      "Operations monitoring",
      "Earthquake monitoring",
      "Python risk scoring",
      "Jinja workshop",
      "Chess performance",
      "Retail analytics",
    ]) {
      await expect(page.getByRole("radio", { name: new RegExp(demo) })).toBeInViewport();
    }
  });

  test("creates an empty project in place with git init and initial commit", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/welcome`);

    await page.getByRole("button", { name: /Start from scratch/ }).click();
    // In-place setup names the folder it creates, with no name input.
    await expect(page.getByText("in this workspace", { exact: false })).toBeVisible();
    await expect(page.getByLabel("Project name")).toHaveCount(0);
    await page.getByRole("button", { name: "Create project" }).click();

    // The empty project opens straight on its canvas, without a reload.
    await expect(page).toHaveURL(/\/pipelines\/.+\/canvas/, { timeout: 30000 });

    expect(existsSync(join(liveApp.workspaceDir, "analytics", "pipeline.yml"))).toBe(true);
    expect(existsSync(join(liveApp.workspaceDir, "analytics", "assets", "example.sql"))).toBe(true);
    expect(readFileSync(join(liveApp.workspaceDir, ".gitignore"), "utf8")).toContain(
      "duckdb-files/",
    );
    expect(readFileSync(join(liveApp.workspaceDir, ".bruin.yml"), "utf8")).toContain(
      "duckdb-default",
    );
    expect(gitLog(liveApp.workspaceDir)).toContain("Initialize renart project");

    await assertRegisteredProject(liveApp, liveApp.workspaceDir);
  });

  test("creates the default demo and runs it on the first-run canvas", async ({
    liveApp,
    page,
  }) => {
    test.setTimeout(timeoutForRetry(test.info(), 240000, 60000));
    await page.goto(`${liveApp.baseURL}/welcome`);

    await page.getByRole("button", { name: /Explore a demo/ }).click();
    const createResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === "/api/projects" &&
        response.request().method() === "POST",
      { timeout: timeoutForRetry(test.info(), 60000) },
    );
    await page.getByRole("button", { name: "Create and run" }).click();
    expect((await createResponse).ok()).toBe(true);

    // The checklist reports real steps while the preview canvas runs.
    const steps = page.getByRole("list", { name: "Setup steps" });
    await expect(steps.getByText("Project files")).toBeVisible();
    await expect(steps.getByText(/files, including product_analytics\/pipeline.yml/)).toBeVisible({
      timeout: timeoutForRetry(test.info(), 60000),
    });

    // On success the canvas expands into the workspace without a reload.
    await expect(page).toHaveURL(/\/pipelines\/.+\/canvas/, {
      timeout: timeoutForRetry(test.info(), 180000, 60000),
    });

    for (const relPath of [
      "product_analytics/pipeline.yml",
      "product_analytics/assets/product/users.sql",
      "product_analytics/assets/product/events.sql",
      "product_analytics/assets/product/user_journeys.sql",
      "product_analytics/assets/product/activation_funnel.sql",
      "product_analytics/assets/product/daily_active_users.sql",
    ]) {
      expect(existsSync(join(liveApp.workspaceDir, relPath)), relPath).toBe(true);
    }

    // The demo tables landed in the local DuckDB file.
    const duckdbPath = join(liveApp.workspaceDir, "duckdb-files", "product_analytics.duckdb");
    expect(existsSync(duckdbPath)).toBe(true);
    expect(statSync(duckdbPath).size).toBeGreaterThan(10000);

    const workspaceResponse = await page.request.get(`${liveApp.baseURL}/api/workspace`);
    expect(workspaceResponse.ok()).toBe(true);
    const workspace = (await workspaceResponse.json()) as WorkspaceResponse;
    expect(workspace.selected_environment).toBe("default");
    const product = workspace.pipelines.find((pipeline) => pipeline.path === "product_analytics");
    expect(product).toBeTruthy();

    await expect
      .poll(
        async () => {
          const response = await page.request.get(
            `${liveApp.baseURL}/api/pipelines/${product!.id}/staleness?environment=${encodeURIComponent(workspace.selected_environment)}`,
          );
          if (!response.ok()) return [];
          const staleness = (await response.json()) as StalenessResponse;
          return staleness.assets.map((asset) => `${asset.asset_name}:${asset.status}`).sort();
        },
        { timeout: timeoutForRetry(test.info(), 30000) },
      )
      .toEqual(product!.assets.map((asset) => `${asset.name}:fresh`).sort());

    for (const asset of product!.assets) {
      await expect(
        page.getByTestId(`rf__node-${asset.id}`).locator('[title="Staleness: Fresh"]'),
      ).toBeVisible({ timeout: timeoutForRetry(test.info(), 30000) });
    }
  });

  test("keeps an existing project directory warning beside the create action", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/welcome?new=1`);

    await page.getByRole("button", { name: /Explore a demo/ }).click();
    await page.getByRole("button", { name: "Change" }).click();
    await page.getByLabel("Project name").fill(basename(liveApp.workspaceDir));
    await expect(page.getByRole("button", { name: "Choose project location" })).toContainText(
      dirname(liveApp.workspaceDir),
    );

    const createResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === "/api/projects" &&
        response.request().method() === "POST",
    );
    await page.getByRole("button", { name: "Create and run" }).click();
    expect((await createResponse).status()).toBe(400);

    // Creation failed, so the flow stays on setup with the fields open.
    const warning = page.getByRole("alert");
    await expect(warning).toContainText("already exists");
    await expect(warning).toBeInViewport();
    await expect(page.getByLabel("Project name")).toBeVisible();
    await expect(page.getByRole("button", { name: "Create and run" })).toBeEnabled();
  });

  test("creates a new project directory from the New project flow", async ({ liveApp, page }) => {
    const parentDir = join(liveApp.workspaceDir, "projects");
    mkdirSync(parentDir, { recursive: true });
    const selectedParentDir = join(parentDir, "onboarding-projects");

    await page.goto(`${liveApp.baseURL}/welcome?new=1`);

    await page.getByRole("button", { name: /Start from scratch/ }).click();
    await page.getByRole("button", { name: "Change" }).click();
    await page.getByLabel("Project name").fill("my-new-project");
    const locationButton = page.getByRole("button", { name: "Choose project location" });
    await expect(locationButton).toContainText(dirname(liveApp.workspaceDir));
    await locationButton.click();

    const picker = page.getByRole("dialog", { name: "Choose project location" });
    await picker.getByRole("button", { name: basename(liveApp.workspaceDir), exact: true }).click();
    await picker.getByRole("button", { name: "projects", exact: true }).click();
    await picker.getByRole("button", { name: "New folder" }).click();
    await picker.getByLabel("New folder name").fill("onboarding-projects");
    await picker.getByRole("button", { name: "Create", exact: true }).click();
    await expect(picker.getByTitle(selectedParentDir)).toBeVisible();
    await picker.getByRole("button", { name: "Use this directory" }).click();
    await expect(locationButton).toContainText(selectedParentDir);
    await page.getByRole("button", { name: "Create project" }).click();

    // A new project changes the tab's project scope, so it loads the canvas.
    await expect(page).toHaveURL(/\/pipelines\/.+\/canvas/, { timeout: 30000 });

    const projectDir = join(selectedParentDir, "my-new-project");
    expect(existsSync(join(projectDir, "analytics", "pipeline.yml"))).toBe(true);
    expect(existsSync(join(projectDir, ".git"))).toBe(true);
    expect(readFileSync(join(projectDir, ".gitignore"), "utf8")).toContain("duckdb-files/");
    expect(gitLog(projectDir)).toContain("Initialize renart project");

    await assertRegisteredProject(liveApp, projectDir);
  });
});

test.describe("import onboarding", () => {
  test.use({
    fixtureName: "empty-workspace-postgres",
    liveAppEnv: { RENART_E2E_ONBOARDING_POSTGRES_PASSWORD: "postgres" },
  });

  test("imports postgres tables as source assets through the welcome flow", async ({
    liveApp,
    livePostgres,
    page,
  }) => {
    test.skip(!livePostgres, "Postgres via docker is required for the import flow.");
    const postgres = livePostgres!;

    await page.goto(`${liveApp.baseURL}/welcome`);
    await page.getByRole("button", { name: /Connect your data/ }).click();
    await expect(page.getByRole("heading", { name: "Connect your database" })).toBeVisible();

    // Product names lead; only documented platforms are offered by default.
    await page.getByLabel("Database", { exact: true }).click();
    await expect(page.getByRole("option", { name: "Google BigQuery", exact: true })).toBeVisible();
    await expect(page.getByRole("option", { name: "Apache Doris" })).toHaveCount(0);
    await page.getByRole("option", { name: "PostgreSQL", exact: true }).click();
    await page.getByLabel(/^host/).fill(postgres.host);
    await page.getByLabel(/^port/).fill(String(postgres.port));
    await page.getByLabel(/^username/).fill(postgres.user);
    await page.getByRole("radio", { name: "Environment" }).click();
    await page
      .getByRole("textbox", { name: "password", exact: true })
      .fill("RENART_E2E_ONBOARDING_POSTGRES_PASSWORD");
    await page.getByLabel(/^database/).fill(postgres.database);
    await page.getByRole("button", { name: "Test connection" }).click();

    await expect(page.getByRole("status")).toContainText("Connected", { timeout: 60000 });
    await page.getByRole("button", { name: "Choose tables" }).click();
    await expect(page.getByRole("heading", { name: "Pick tables to import" })).toBeVisible({
      timeout: 60000,
    });
    // Tables are grouped by schema.
    await expect(page.getByRole("group", { name: "Schema analytics" })).toBeVisible();
    await page.getByRole("checkbox", { name: "analytics.orders", exact: true }).click();
    await page.getByRole("checkbox", { name: "analytics.customers", exact: true }).click();
    await page.getByRole("button", { name: "Import 2 tables" }).click();

    // The import opens the new pipeline's canvas.
    await expect(page).toHaveURL(/\/pipelines\/.+\/canvas/, { timeout: 60000 });

    const assetsDir = join(liveApp.workspaceDir, "analytics", "assets");
    expect(existsSync(assetsDir)).toBe(true);
    const assetFiles = readdirSync(assetsDir, { recursive: true }).map(String);
    expect(assetFiles.some((file) => file.endsWith("orders.asset.yml"))).toBe(true);
    expect(assetFiles.some((file) => file.endsWith("customers.asset.yml"))).toBe(true);
  });
});

async function assertRegisteredProject(liveApp: LiveApp, projectPath: string) {
  const response = await fetch(`${liveApp.baseURL}/api/projects`);
  expect(response.ok).toBe(true);
  const directory = (await response.json()) as ProjectListResponse;
  const project = directory.projects.find((entry) => entry.path === projectPath);
  expect(project, `project at ${projectPath} must be registered`).toBeTruthy();
  expect(project?.open).toBe(true);
}
