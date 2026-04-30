import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { expect } from "@playwright/test";

import { liveTest as test } from "./live-app-fixture";

test.describe("workspace live basic flows", () => {
  test.use({ fixtureName: "basic-workspace" });

  test("loads the fixture workspace and opens an asset in the editor", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/`);

    if (test.info().project.name.includes("mobile")) {
      await openCustomersEditor(page, liveApp.baseURL);
    } else {
      await openCustomersEditor(page, liveApp.baseURL);
    }

    await expect(page).toHaveTitle("analytics.customers · analytics · Renart");
    await expect(
      page.getByText("analytics.customers", { exact: true }).last()
    ).toBeVisible();
    await expect(
      page.getByTestId("editor-asset-path")
    ).toBeVisible();
  });

  test("switches assets from the sidebar against the real server", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/`);

    await openCustomersEditor(page, liveApp.baseURL);
    if (test.info().project.name.includes("mobile")) {
      await page.goto(`${liveApp.baseURL}/?pipeline=YW5hbHl0aWNz&asset=YW5hbHl0aWNzL2Fzc2V0cy9vcmRlcnMuc3Fs`);
      await expect(page).toHaveTitle("analytics.orders · analytics · Renart");
      const editorDialog = page.getByRole("dialog", { name: "Asset Editor" });
      if (!(await editorDialog.isVisible().catch(() => false))) {
        await page.getByRole("button", { name: "Edit asset" }).click();
      }
      await expect(page.getByTestId("editor-asset-name")).toHaveText("analytics.orders");
    } else {
      await page.getByRole("link", { name: "analytics.orders" }).click();
    }

    await expect(page).toHaveTitle("analytics.orders · analytics · Renart");
    await expect(
      page.getByText("analytics.orders", { exact: true }).last()
    ).toBeVisible();
    await expect(
      page.getByTestId("editor-asset-path")
    ).toBeVisible();
  });

  test("runs inspect for the selected asset", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/`);

    await openCustomersEditor(page, liveApp.baseURL);
    await page.getByRole("button", { name: /Inspect/ }).click();

    if (test.info().project.name.includes("mobile")) {
      await expect(page.getByText("2 rows", { exact: true })).toBeVisible({ timeout: 15000 });
      await expect(page.getByText("Ada", { exact: true }).last()).toBeVisible();
      return;
    }

    await expect(page.getByRole("tab", { name: "Inspect" })).toBeVisible();
    await expect(page.getByText("2 rows", { exact: true })).toBeVisible({
      timeout: 15000,
    });
    await expect(
      page.getByRole("columnheader", { name: "customer_id", exact: true })
    ).toBeVisible();
    await expect(page.getByRole("cell", { name: "Ada", exact: true })).toBeVisible();
  });

  test("switches environments, updates the selector label, and refetches inspect", async ({
    liveApp,
    page,
  }) => {
    test.skip(test.info().project.name.includes("mobile"), "Desktop-only environment selector coverage.");

    const configPath = join(liveApp.workspaceDir, ".bruin.yml");
    const currentConfig = await readFile(configPath, "utf8");
    await writeFile(
      configPath,
      `${currentConfig.trimEnd()}\n  prod:\n    connections:\n      duckdb:\n        - name: duckdb-default\n          path: duckdb-files/local.db\n`,
      "utf8"
    );

    await page.goto(`${liveApp.baseURL}/`);
    await openCustomersEditor(page, liveApp.baseURL);

    await expect(page.getByRole("combobox", { name: "Environment" })).toContainText(
      /Environment:\s*default/
    );

    const prodInspectRequest = page.waitForRequest(
      (request) =>
        request.url().includes("/api/assets/") &&
        request.url().includes("/inspect") &&
        request.url().includes("environment=prod")
    );

    await page.getByRole("combobox", { name: "Environment" }).click();
    const prodOption = page.getByRole("option", { name: "prod", exact: true });
    await expect(prodOption).toBeVisible();
    await prodOption.dispatchEvent("click");

    await expect(page).toHaveURL(/environment=prod/);
    await expect(page.getByRole("combobox", { name: "Environment" })).toContainText(
      /Environment:\s*prod/
    );
    await prodInspectRequest;
  });

  test("reveals nested command palette matches from root search", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/`);

    await openCommandPalette(page);

    const commandInput = page.locator('[data-slot="command-input"]');
    await commandInput.fill("orders");

    await expect(page.getByRole("option", { name: "analytics.orders analytics" })).toBeVisible();
    await page.getByRole("option", { name: "analytics.orders analytics" }).click();

    await expect(page.getByTestId("editor-asset-name")).toHaveText("analytics.orders");
    await expect(page.getByTestId("editor-asset-path")).toHaveText(
      "analytics/assets/orders.sql"
    );
  });

  test("clears the command palette search when entering a nested page", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/`);

    await openCommandPalette(page);

    const commandInput = page.locator('[data-slot="command-input"]');
    await commandInput.fill("asset");
    await page.getByRole("option", { name: /Go to asset/i }).click();

    await expect(commandInput).toHaveValue("");
    await expect(page.getByRole("option", { name: "analytics.customers analytics" })).toBeVisible();
    await expect(page.getByRole("option", { name: "analytics.orders analytics" })).toBeVisible();
  });

  test("removes stale Renart inferred dependencies but preserves manual ones", async ({
    liveApp,
    page,
  }) => {
    test.skip(test.info().project.name.includes("mobile"), "No mobile canvas asset-creation interaction yet.");

    await page.goto(`${liveApp.baseURL}/`);

    await openCustomersEditor(page, liveApp.baseURL);

    const canvas = page.locator(".react-flow__pane").first();
    const box = await canvas.boundingBox();
    if (!box) {
      throw new Error("Could not locate the React Flow pane for asset creation.");
    }

    await canvas.click({
      position: {
        x: Math.round(box.width * 0.55),
        y: Math.round(box.height * 0.3),
      },
    });
    await page.getByPlaceholder("Asset name").fill("analytics.manual_seed");
    await page
      .getByTestId("rf__node-__new_asset__")
      .getByRole("button", { name: "Create", exact: true })
      .click();

    await expect(page.getByRole("link", { name: "analytics.manual_seed" })).toBeVisible();

    await openCustomersEditor(page, liveApp.baseURL);

    if (test.info().project.name.includes("mobile")) {
      test.skip(true, "No mobile canvas asset-creation interaction yet.");
    }

    await page.getByRole("tab", { name: "Dependencies" }).click();
    const dependencyInput = page.getByPlaceholder("Add dependency");
    const manualDependenciesSection = page.getByText("Manual dependencies").locator("..");
    await dependencyInput.fill("analytics.manual_seed");
    await page.getByRole("option", { name: "analytics.manual_seed" }).click();

    await expect
      .poll(async () => {
        if (!(await page.getByRole("tab", { name: "Dependencies" }).isVisible().catch(() => false))) {
          return 0;
        }
        await page.getByRole("tab", { name: "Dependencies" }).click();
        return await manualDependenciesSection
          .getByText("analytics.manual_seed", { exact: true })
          .count();
      })
      .toBe(1);

    const saveWithInferredDependency = page.waitForResponse(
      (response) =>
        response.url().includes("/api/pipelines/") &&
        response.url().includes("/assets/YW5hbHl0aWNzL2Fzc2V0cy9jdXN0b21lcnMuc3Fs") &&
        response.request().method() === "PUT" &&
        (response.request().postData() ?? "").includes("from analytics.orders")
    );
    await replaceEditorContent(page, "select *\nfrom analytics.orders\n");
    await page.keyboard.press("ControlOrMeta+S");
    await saveWithInferredDependency;
    await waitForWorkspaceAssetUpstreams(page, "analytics.customers", [
      "analytics.manual_seed",
      "analytics.orders",
    ]);
    await page.getByRole("tab", { name: "Dependencies" }).click();

    const inferredPanel = page.getByText("Automatically inferred").locator("..");
    await expect(inferredPanel.getByText("analytics.orders", { exact: true })).toBeVisible();
    await expect(
      manualDependenciesSection.getByText("analytics.manual_seed", { exact: true })
    ).toBeVisible();

    const saveWithoutInferredDependency = page.waitForResponse(
      (response) =>
        response.url().includes("/api/pipelines/") &&
        response.url().includes("/assets/YW5hbHl0aWNzL2Fzc2V0cy9jdXN0b21lcnMuc3Fs") &&
        response.request().method() === "PUT" &&
        (response.request().postData() ?? "").includes("select 1 as customer_id")
    );
    await replaceEditorContent(page, "select 1 as customer_id");
    await page.keyboard.press("ControlOrMeta+S");
    await saveWithoutInferredDependency;
    await waitForWorkspaceAssetUpstreams(page, "analytics.customers", ["analytics.manual_seed"]);
    await page.getByRole("tab", { name: "Dependencies" }).click();

    await expect
      .poll(async () => {
        return await inferredPanel.getByText("analytics.orders", { exact: true }).count();
      }, {
        timeout: 15000,
      })
      .toBe(0);

    await expect(
      manualDependenciesSection.getByText("analytics.manual_seed", { exact: true })
    ).toBeVisible();
    await expect(page.getByText("No automatically inferred dependencies for this asset.")).toBeVisible();
  });

  test("selects a dependency option by tapping it on mobile", async ({
    liveApp,
    page,
  }) => {
    test.skip(!test.info().project.name.includes("mobile"), "Mobile-only repro.");

    await page.goto(`${liveApp.baseURL}/`);
    await openCustomersEditor(page, liveApp.baseURL);
    await page.getByRole("tab", { name: "Dependencies" }).click();

    const dependencyInput = page.getByPlaceholder("Add dependency");
    await dependencyInput.fill("analytics.orders");

    const option = page.getByRole("option", { name: "analytics.orders" });
    await expect(option).toBeVisible();
    await option.dispatchEvent("pointerdown", {
      bubbles: true,
      pointerType: "touch",
      isPrimary: true,
      button: 0,
      buttons: 1,
    });
    await option.dispatchEvent("pointerup", {
      bubbles: true,
      pointerType: "touch",
      isPrimary: true,
      button: 0,
      buttons: 0,
    });
    await option.dispatchEvent("click", { bubbles: true });

    await expect(
      page.getByText("Manual dependencies").locator("..").getByText("analytics.orders", {
        exact: true,
      })
    ).toBeVisible();
  });

  test("selects visualization combobox options by tapping them on mobile", async ({
    liveApp,
    page,
  }) => {
    test.skip(!test.info().project.name.includes("mobile"), "Mobile-only repro.");

    await page.goto(`${liveApp.baseURL}/`);
    await openCustomersEditor(page, liveApp.baseURL);

    await page.getByRole("tab", { name: "Preview" }).click();

    await page.getByRole("tab", { name: /^Chart$/ }).click();
    await expect(page.getByText("X Axis Column", { exact: true })).toBeVisible();
  });

  test("opens the rename pipeline dialog from the live sidebar context menu", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/`);

    if (test.info().project.name.includes("mobile")) {
      test.skip(true, "No mobile pipeline context-menu interaction yet.");
    }

    await page
      .getByRole("link", { name: "analytics", exact: true })
      .click({ button: "right" });
    await page.getByRole("menuitem", { name: "Rename Pipeline" }).click();

    await expect(page.getByLabel("Pipeline name")).toHaveValue("analytics");
  });

  test("opens pipeline settings, saves changes, and persists pipeline.yml", async ({
    liveApp,
    page,
  }) => {
    test.skip(test.info().project.name.includes("mobile"), "No mobile pipeline context-menu interaction yet.");

    const pipelinePath = join(liveApp.workspaceDir, "analytics", "pipeline.yml");
    const originalContent = await readFile(pipelinePath, "utf8");

    try {
      await page.goto(`${liveApp.baseURL}/?pipeline=YW5hbHl0aWNz&asset=YW5hbHl0aWNzL2Fzc2V0cy9jdXN0b21lcnMuc3Fs`);

      await page
        .getByRole("link", { name: "analytics", exact: true })
        .click({ button: "right" });
      await page.getByRole("menuitem", { name: "Pipeline Settings" }).click();

      const dialog = page.getByRole("dialog");
      await expect(dialog.getByText("No unsaved changes")).toBeVisible();

      await dialog.getByRole("button", { name: "General" }).click();
      await dialog.getByLabel("Owner").fill("data-platform-renart");
      await dialog.getByPlaceholder("Add tag").fill("renart-live");
      await dialog.getByPlaceholder("Add tag").press("Enter");

      await dialog.getByRole("button", { name: "Execution" }).click();
      await dialog.getByLabel("Retries").fill("3");
      await dialog.getByLabel("Rerun Cooldown (s)").fill("120");

      await dialog.getByRole("button", { name: "Save" }).click();
      await expect(dialog.getByText("Saved changes", { exact: true })).toBeVisible();

      const updatedContent = await readFile(pipelinePath, "utf8");
      expect(updatedContent).toContain("owner: data-platform-renart");
      expect(updatedContent).toContain("tags:\n  - renart-live");
      expect(updatedContent).toContain("retries: 3");
      expect(updatedContent).toContain("rerun_cooldown: 120");

      await dialog.getByRole("button", { name: "Close", exact: true }).click();
      await expect(page).toHaveURL(/\/?pipeline=YW5hbHl0aWNz/);
    } finally {
      await writeFile(pipelinePath, originalContent, "utf8");
    }
  });

  test("materializes the selected asset and records a history entry", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/`);

    await openCustomersEditor(page, liveApp.baseURL);
    const emptyHistoryMessage = page.getByText("No materialize runs yet.");

    if (test.info().project.name.includes("mobile")) {
      await expect(page.getByRole("button", { name: "Materialize", exact: true })).toBeVisible();
      await page.getByRole("button", { name: "Materialize", exact: true }).click();
      await expect(page.getByText("Asset: analytics.customers", { exact: true })).toBeVisible({ timeout: 15000 });
      return;
    }

    await page.getByRole("tab", { name: "Materialize" }).click();
    await expect(emptyHistoryMessage).toBeVisible();
    await page.getByRole("button", { name: "Materialize", exact: true }).click();

    await expect(page.getByRole("tab", { name: "Materialize" })).toBeVisible();
    await expect(emptyHistoryMessage).toHaveCount(0);
    await expect(
      page.getByText("Asset: analytics.customers", { exact: true })
    ).toBeVisible({ timeout: 15000 });
  });

  test("runs the selected pipeline and records a pipeline history entry", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/`);

    const pipelineEntry = page.getByText("Pipeline: analytics", { exact: true });

    if (test.info().project.name.includes("mobile")) {
      await expect(page.getByRole("button", { name: "Run pipeline" })).toBeVisible();
      await page.getByRole("button", { name: "Run pipeline" }).click();
      await expect(pipelineEntry).toBeVisible({ timeout: 15000 });
      return;
    }

    await page.getByRole("tab", { name: "Materialize" }).click();
    await expect(page.getByText("No materialize runs yet.")).toBeVisible();
    await page.getByRole("button", { name: "Run pipeline" }).click();
    await expect(pipelineEntry).toBeVisible({ timeout: 15000 });
  });

  test("creates, renames, and deletes an asset in an isolated workspace", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/`);

    if (test.info().project.name.includes("mobile")) {
      test.skip(true, "No mobile canvas asset-creation interaction yet.");
    }

    await openCustomersEditor(page, liveApp.baseURL);

    const canvas = page.locator(".react-flow__pane").first();
    const box = await canvas.boundingBox();
    if (!box) {
      throw new Error("Could not locate the React Flow pane for asset creation.");
    }

    await canvas.click({
      position: {
        x: Math.round(box.width * 0.35),
        y: Math.round(box.height * 0.35),
      },
    });
    await page.getByPlaceholder("Asset name").fill("analytics.new_asset");
    await page
      .getByTestId("rf__node-__new_asset__")
      .getByRole("button", { name: "Create", exact: true })
      .click();

    await expect(page.getByRole("link", { name: "analytics.new_asset" })).toBeVisible();

    await page.getByRole("link", { name: "analytics.new_asset" }).click();
    await page.getByRole("button", { name: "Rename asset" }).click();
    await page.locator('input[value="analytics.new_asset"]').fill("analytics.renamed_asset");
    await page.getByRole("button", { name: "Save" }).first().click();

    await expect(page.getByRole("link", { name: "analytics.renamed_asset" })).toBeVisible();

    await page.getByRole("button", { name: "Delete" }).click();
    await page.getByRole("button", { name: "Delete" }).last().click();

    await expect(
      page.getByRole("link", { name: "analytics.renamed_asset" })
    ).toHaveCount(0);
  });

  test.describe("empty workspace live flows", () => {
    test.use({ fixtureName: "empty-workspace" });

    test("creates, renames, and deletes a pipeline in an isolated workspace", async ({
      liveApp,
      page,
    }) => {
      test.skip(test.info().project.name.includes("mobile"), "No mobile pipeline context-menu interaction yet.");

      await page.goto(`${liveApp.baseURL}/`);

      await expect(page.getByTestId("workspace-onboarding")).toBeVisible();
      await expect(page).toHaveURL(/\/onboarding(?:\/connection)?$/);

      await page.getByRole("button", { name: /skip for now/i }).click();

      await expect(
        page.getByRole("heading", { name: "Create your first pipeline" })
      ).toBeVisible();
      await expect(page).toHaveTitle("Workspace · Renart");
      await page.getByRole("button", { name: "Create pipeline" }).last().click();
      await page.getByLabel("Pipeline path").fill("experiments");
      await page.getByRole("button", { name: "Create Pipeline", exact: true }).click();

      await expect(page).toHaveTitle("experiments · Renart");
      await expect(page.getByRole("link", { name: "experiments", exact: true })).toBeVisible();
      await expect(page).toHaveTitle("experiments · Renart");

      await page
        .getByRole("link", { name: "experiments", exact: true })
        .click({ button: "right" });
      await page.getByRole("menuitem", { name: "Rename Pipeline" }).click();
      await page.getByLabel("Pipeline name").fill("experiments_renamed");
      await page.getByRole("button", { name: "Save" }).click();

      await expect(
        page.getByRole("link", { name: "experiments_renamed", exact: true })
      ).toBeVisible();
      await expect(page).toHaveTitle("experiments_renamed · Renart");

      await page.reload();

      await expect(
        page.getByRole("link", { name: "experiments_renamed", exact: true })
      ).toBeVisible();

      await page
        .getByRole("link", { name: "experiments_renamed", exact: true })
        .click({ button: "right" });
      await page.getByRole("menuitem", { name: "Delete Pipeline" }).click();
      await page.getByRole("button", { name: "Delete Pipeline" }).click();

      await expect(
        page.getByRole("link", { name: "experiments_renamed", exact: true })
      ).toHaveCount(0);
    });
  });
});

async function selectCustomersInWorkspace(
  page: import("@playwright/test").Page,
  baseURL: string
) {
  const isMobile = test.info().project.name.includes("mobile");
  if (isMobile) {
    await page.goto(`${baseURL}/?pipeline=YW5hbHl0aWNz&asset=YW5hbHl0aWNzL2Fzc2V0cy9jdXN0b21lcnMuc3Fs`);
    await expect(page).toHaveTitle("analytics.customers · analytics · Renart");
    return;
  }
}

async function openCustomersEditor(
  page: import("@playwright/test").Page,
  baseURL: string
) {
  const isMobile = test.info().project.name.includes("mobile");
  if (isMobile) {
    await selectCustomersInWorkspace(page, baseURL);
    const editorDialog = page.getByRole("dialog", { name: "Asset Editor" });
    if (!(await editorDialog.isVisible().catch(() => false))) {
      await page.getByRole("button", { name: "Edit asset" }).click();
    }
    await expect(editorDialog).toBeVisible();
    await expect(page.getByTestId("editor-asset-name")).toHaveText("analytics.customers");
    await waitForEditorReady(page);
  } else {
    await page.goto(`${baseURL}/?pipeline=YW5hbHl0aWNz&asset=YW5hbHl0aWNzL2Fzc2V0cy9jdXN0b21lcnMuc3Fs`);
    const editorAssetName = page.getByTestId("editor-asset-name");
    const selectedAssetName = await editorAssetName.textContent().catch(() => null);
    if (
      !(await editorAssetName.isVisible().catch(() => false)) ||
      selectedAssetName?.trim() !== "analytics.customers"
    ) {
      const analyticsLink = page.getByRole("link", { name: "analytics", exact: true });
      const customersLink = page.getByRole("link", { name: "analytics.customers", exact: true });
      if (!(await customersLink.isVisible().catch(() => false))) {
        await expect(analyticsLink).toBeVisible({ timeout: 15000 });
        await analyticsLink.click();
      }
      await expect(customersLink).toBeVisible({ timeout: 15000 });
      await customersLink.click();
    }
    await expect(editorAssetName).toHaveText("analytics.customers", { timeout: 15000 });
    await waitForEditorReady(page);
  }
}

async function reopenCustomersEditor(
  page: import("@playwright/test").Page,
  baseURL: string
) {
  if (test.info().project.name.includes("mobile")) {
    await openCustomersEditor(page, baseURL);
    return;
  }

  await page.getByRole("link", { name: "analytics.orders" }).click();
  await openCustomersEditor(page, baseURL);
}

async function openCommandPalette(page: import("@playwright/test").Page) {
  if (test.info().project.name.includes("mobile")) {
    await page.getByRole("button", { name: "Open search" }).click();
  } else {
    await page.getByRole("button", { name: "Open search" }).click();
  }

  await expect(page.locator('[data-slot="command-input"]')).toBeVisible();
}

async function replaceEditorContent(
  page: import("@playwright/test").Page,
  content: string
) {
  const editor = await waitForEditorReady(page);
  await editor.click();
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type(content);
}

async function waitForEditorReady(page: import("@playwright/test").Page) {
  const editor = page.locator(".monaco-editor").first();
  await expect(page.getByTestId("editor-asset-name")).toHaveText(/analytics\./, {
    timeout: 15000,
  });
  await expect(editor).toBeVisible({ timeout: 15000 });
  await expect(page.locator(".view-lines").first()).toBeVisible({ timeout: 15000 });
  return editor;
}

async function waitForWorkspaceAssetUpstreams(
  page: import("@playwright/test").Page,
  assetName: string,
  expectedUpstreams: string[]
) {
  const sortedExpected = [...expectedUpstreams].sort();
  await expect
    .poll(async () => {
      const upstreams = await page.evaluate(async (targetAssetName) => {
        const response = await fetch("/api/workspace", { cache: "no-store" });
        const workspace = (await response.json()) as {
          pipelines?: Array<{
            assets?: Array<{ name?: string; upstreams?: string[] }>;
          }>;
        };

        for (const pipeline of workspace.pipelines ?? []) {
          for (const asset of pipeline.assets ?? []) {
            if (asset.name === targetAssetName) {
              return asset.upstreams ?? [];
            }
          }
        }

        return null;
      }, assetName);

      return upstreams ? [...upstreams].sort() : null;
    }, { timeout: 15000 })
    .toEqual(sortedExpected);
}
