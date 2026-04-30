import { expect } from "@playwright/test";
import { access, readFile } from "node:fs/promises";
import { join } from "node:path";

import { liveTest as test } from "./live-app-fixture";

test.describe("workspace onboarding live flows", () => {
  test.use({ fixtureName: "empty-workspace-postgres" });

  const countOccurrences = (source: string, needle: string) =>
    source.split(needle).length - 1;

  test("creates a postgres connection and imports real tables through onboarding", async ({
    page,
    liveApp,
    livePostgres,
  }) => {
    if (!livePostgres) {
      throw new Error("Expected live Postgres fixture to be available.");
    }

    await page.goto(`${liveApp.baseURL}/onboarding`);
    await page.evaluate(() => window.localStorage.removeItem("renart-onboarding-dismissed"));
    await page.reload();

    await expect(page.getByTestId("workspace-onboarding")).toBeVisible({ timeout: 15000 });
    await page.getByTestId("onboarding-import-choice").click();
    
    await page.getByRole("button", { name: /postgres/i }).click();
    await expect(page.getByTestId("onboarding-step-connection-config")).toBeVisible();

    await page.getByLabel("Host").fill(livePostgres.host);
    await page.getByLabel("Port").fill(String(livePostgres.port));
    await page.getByLabel("Username").fill(livePostgres.user);
    await page.getByLabel("Password").fill(livePostgres.password);
    await expect(page.getByLabel("Allow SSL")).not.toBeChecked();

    await page.getByRole("button", { name: /Validate and continue/i }).click();
    await expect(page.getByTestId("onboarding-step-import")).toBeVisible();

    await page.getByRole("button", { name: livePostgres.database }).click();
    await expect(page.getByLabel("bruin.analytics.orders")).toBeVisible();
    await expect(page.getByLabel("bruin.analytics.customers")).toBeVisible();

    const configAfterValidation = await readFile(join(liveApp.workspaceDir, ".bruin.yml"), "utf8");
    expect(configAfterValidation).not.toContain("postgres-default");

    const onboardingStateAfterValidation = await readFile(
      join(liveApp.workspaceDir, ".renart-onboarding.json"),
      "utf8"
    );
    expect(onboardingStateAfterValidation).toContain('"step": "import"');
    expect(onboardingStateAfterValidation).toContain('"selected_type": "postgres"');

    await page.getByLabel("bruin.analytics.customers").uncheck();

    const importTextboxes = page.getByTestId("onboarding-step-import").getByRole("textbox");
    await importTextboxes.nth(0).fill("analytics");
    await importTextboxes.nth(1).fill("analytics");
    await page.getByRole("button", { name: /Save connection and import/i }).click();

    await expect(page.getByTestId("onboarding-step-success")).toBeVisible({
      timeout: 30000,
    });
    await expect(page.getByTestId("onboarding-import-summary")).toContainText("Import complete");
    await expect(page.getByTestId("onboarding-imported-tables")).toContainText("1");
    await expect(page.getByTestId("onboarding-successful-assets")).toContainText("1");
    await expect(page.getByTestId("onboarding-merged-tables")).toContainText("0");
    await expect(page.getByTestId("onboarding-import-summary")).toContainText(livePostgres.database);
    await expect(page.getByTestId("onboarding-import-summary")).toContainText("analytics");

    const configAfterImport = await readFile(join(liveApp.workspaceDir, ".bruin.yml"), "utf8");
    expect(configAfterImport).toContain("postgres-default");
    expect(configAfterImport).toContain(`database: ${livePostgres.database}`);
    expect(countOccurrences(configAfterImport, "name: postgres-default")).toBe(1);

    await page.getByRole("button", { name: "Open workspace" }).click();

    const onboardingStateAfterComplete = await readFile(
      join(liveApp.workspaceDir, ".renart-onboarding.json"),
      "utf8"
    );
    expect(onboardingStateAfterComplete).toContain('"active": false');

    await expect(page).toHaveURL(/\/(?:\?.*)?$/, { timeout: 30000 });

    const importedOrderAsset = await readFile(
      join(
        liveApp.workspaceDir,
        "analytics",
        "assets",
        "analytics",
        "orders.asset.yml"
      ),
      "utf8"
    );
    expect(importedOrderAsset).toContain("analytics.orders");

    const importedCustomersAssetPath = join(
      liveApp.workspaceDir,
      "analytics",
      "assets",
      "analytics",
      "customers.asset.yml"
    );
    await expect
      .poll(async () => {
        try {
          await access(importedCustomersAssetPath);
          return true;
        } catch {
          return false;
        }
      })
      .toBe(false);
  });
});

test.describe("workspace onboarding DuckDB quickstart", () => {
  test.use({ fixtureName: "empty-workspace" });

  test("creates and materializes the DuckDB quickstart pipeline", async ({
    page,
    liveApp,
  }) => {
    await page.goto(`${liveApp.baseURL}/onboarding`);
    await page.evaluate(() => window.localStorage.removeItem("renart-onboarding-dismissed"));
    await page.reload();

    await expect(page.getByTestId("workspace-onboarding")).toBeVisible({ timeout: 15000 });
    await page.getByTestId("onboarding-quickstart-choice").click();
    await expect(page).toHaveURL(/\/(?:\?.*)?$/, { timeout: 60000 });

    const configAfterQuickstart = await readFile(join(liveApp.workspaceDir, ".bruin.yml"), "utf8");
    expect(configAfterQuickstart).toContain("duckdb-default");
    expect(configAfterQuickstart).toContain("duckdb-files/chess_playground.duckdb");
    expect(configAfterQuickstart).toContain("chess-default");

    const pipelineFile = await readFile(join(liveApp.workspaceDir, "quickstart", "pipeline.yml"), "utf8");
    expect(pipelineFile).toContain("name: quickstart");
    expect(pipelineFile).toContain("duckdb: duckdb-default");

    const playersAsset = await readFile(
      join(liveApp.workspaceDir, "quickstart", "assets", "players.asset.yml"),
      "utf8"
    );
    expect(playersAsset).toContain("quickstart.players");
    expect(playersAsset).toContain("source_connection: chess-default");

    const gamesAsset = await readFile(
      join(liveApp.workspaceDir, "quickstart", "assets", "games.asset.yml"),
      "utf8"
    );
    expect(gamesAsset).toContain("quickstart.games");
    expect(gamesAsset).toContain("source_table: games");

    const statsAsset = await readFile(
      join(liveApp.workspaceDir, "quickstart", "assets", "player_stats.sql"),
      "utf8"
    );
    expect(statsAsset).toContain("quickstart.player_stats");
    expect(statsAsset).toContain("quickstart.games");
    expect(statsAsset).toContain("players_white");
    expect(statsAsset).toContain("players_black");
    expect(statsAsset).toContain("games_white");
    expect(statsAsset).toContain("games_black");
    expect(statsAsset).not.toContain("columns:");
    expect(statsAsset).not.toContain("checks:");

    const pythonAsset = await readFile(
      join(liveApp.workspaceDir, "quickstart", "assets", "my_python_asset.py"),
      "utf8"
    );
    expect(pythonAsset).toContain("name: my_python_asset");

    await access(join(liveApp.workspaceDir, "duckdb-files", "chess_playground.duckdb"));

    await expect
      .poll(async () => {
        const response = await page.request.get(`${liveApp.baseURL}/api/workspace`);
        const workspace = await response.json();
        return JSON.stringify(workspace).includes("quickstart.player_stats");
      })
      .toBe(true);
  });
});
