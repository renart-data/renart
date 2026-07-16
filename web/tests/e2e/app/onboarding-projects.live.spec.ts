import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readdirSync, statSync, readFileSync } from "node:fs";
import { basename, dirname, join } from "node:path";

import { expect } from "@playwright/test";

import { liveTest as test, type LiveApp } from "../live-app-fixture";

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
    assets: Array<{ id: string; name: string }>;
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
    await expect(page.getByRole("heading", { name: "Welcome to renart" })).toBeVisible();
    await expect(page.getByRole("button", { name: /Start from a demo/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /Import existing tables/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /Start empty/ })).toBeVisible();
  });

  test("creates an empty project in place with git init and initial commit", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/welcome`);

    await page.getByRole("button", { name: /Start empty/ }).click();
    // In-place setup: the target step shows the workspace path, no name input.
    await expect(
      page.getByText("Files are created in the workspace", { exact: false }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Create project" }).click();

    await expect(page.getByRole("heading", { name: "You're all set" })).toBeVisible({
      timeout: 30000,
    });

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

    await page.getByRole("button", { name: "Open workspace" }).click();
    await expect(page).toHaveURL(/\/pipelines\/.+\/canvas/, { timeout: 30000 });
  });

  test("creates and materializes the offline retail demo", async ({ liveApp, page }) => {
    test.setTimeout(240000);

    await page.goto(`${liveApp.baseURL}/welcome`);

    await page.getByRole("button", { name: /Start from a demo/ }).click();
    await page.getByRole("button", { name: /Retail analytics/ }).click();
    await page.getByRole("button", { name: "Create project" }).click();

    // Fast local runs can finish before the intermediate materializing screen
    // paints. The terminal state only renders after the run stream returns ok.
    await expect(page.getByRole("heading", { name: "You're all set" })).toBeVisible({
      timeout: 180000,
    });

    for (const relPath of [
      "retail/pipeline.yml",
      "retail/assets/raw/customers.sql",
      "retail/assets/raw/orders.sql",
      "retail/assets/analytics/customer_orders.sql",
      "retail/assets/analytics/daily_revenue.sql",
    ]) {
      expect(existsSync(join(liveApp.workspaceDir, relPath)), relPath).toBe(true);
    }
    expect(gitLog(liveApp.workspaceDir)).toContain("Initialize renart project");

    // The demo tables landed in the local DuckDB file.
    const duckdbPath = join(liveApp.workspaceDir, "duckdb-files", "retail.duckdb");
    expect(existsSync(duckdbPath)).toBe(true);
    expect(statSync(duckdbPath).size).toBeGreaterThan(10000);

    const workspaceResponse = await page.request.get(`${liveApp.baseURL}/api/workspace`);
    expect(workspaceResponse.ok()).toBe(true);
    const workspace = (await workspaceResponse.json()) as WorkspaceResponse;
    expect(workspace.selected_environment).toBe("default");
    const retail = workspace.pipelines.find((pipeline) => pipeline.name === "retail");
    expect(retail).toBeTruthy();

    await expect
      .poll(
        async () => {
          const response = await page.request.get(
            `${liveApp.baseURL}/api/pipelines/${retail!.id}/staleness?environment=${encodeURIComponent(workspace.selected_environment)}`,
          );
          if (!response.ok()) return [];
          const staleness = (await response.json()) as StalenessResponse;
          return staleness.assets.map((asset) => `${asset.asset_name}:${asset.status}`).sort();
        },
        { timeout: 30000 },
      )
      .toEqual(retail!.assets.map((asset) => `${asset.name}:fresh`).sort());

    await page.getByRole("button", { name: "Open workspace" }).click();
    await expect(page).toHaveURL(/\/pipelines\/.+\/canvas/, { timeout: 30000 });
    for (const asset of retail!.assets) {
      await expect(
        page.getByTestId(`rf__node-${asset.id}`).locator('[title="Staleness: Fresh"]'),
      ).toBeVisible({ timeout: 30000 });
    }
  });

  test("creates a new project directory from the New project flow", async ({ liveApp, page }) => {
    const parentDir = join(liveApp.workspaceDir, "projects");
    mkdirSync(parentDir, { recursive: true });
    const selectedParentDir = join(parentDir, "onboarding-projects");

    await page.goto(`${liveApp.baseURL}/welcome?new=1`);

    await page.getByRole("button", { name: /Start empty/ }).click();
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

    await expect(page.getByRole("heading", { name: "You're all set" })).toBeVisible({
      timeout: 30000,
    });

    const projectDir = join(selectedParentDir, "my-new-project");
    expect(existsSync(join(projectDir, "analytics", "pipeline.yml"))).toBe(true);
    expect(existsSync(join(projectDir, ".git"))).toBe(true);
    expect(readFileSync(join(projectDir, ".gitignore"), "utf8")).toContain("duckdb-files/");
    expect(gitLog(projectDir)).toContain("Initialize renart project");

    await assertRegisteredProject(liveApp, projectDir);
  });
});

test.describe("import onboarding", () => {
  test.use({ fixtureName: "empty-workspace-postgres" });

  test("imports postgres tables as source assets through the welcome flow", async ({
    liveApp,
    livePostgres,
    page,
  }) => {
    test.skip(!livePostgres, "Postgres via docker is required for the import flow.");
    const postgres = livePostgres!;

    await page.goto(`${liveApp.baseURL}/welcome`);
    await page.getByRole("button", { name: /Import existing tables/ }).click();
    await expect(page.getByRole("heading", { name: "Connect your database" })).toBeVisible();

    await page.getByRole("combobox").click();
    await page.getByRole("option", { name: "postgres", exact: true }).click();
    await page.getByLabel(/^host/).fill(postgres.host);
    await page.getByLabel(/^port/).fill(String(postgres.port));
    await page.getByLabel(/^username/).fill(postgres.user);
    await page.getByLabel(/^password/).fill(postgres.password);
    await page.getByLabel(/^database/).fill(postgres.database);
    await page.getByRole("button", { name: "Connect" }).click();

    await expect(page.getByRole("heading", { name: "Pick tables to import" })).toBeVisible({
      timeout: 60000,
    });
    await page.getByText(`${postgres.database}.analytics.orders`).click();
    await page.getByText(`${postgres.database}.analytics.customers`).click();
    await page.getByRole("button", { name: "Import 2 tables" }).click();

    await expect(page.getByRole("heading", { name: "You're all set" })).toBeVisible({
      timeout: 60000,
    });
    await expect(page.getByText("Imported 2 source assets", { exact: false })).toBeVisible();

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
