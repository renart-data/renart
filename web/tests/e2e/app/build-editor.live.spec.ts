import { expect } from "@playwright/test";
import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { liveTest as test } from "../live-app-fixture";

type WorkspaceResponse = {
  pipelines: Array<{
    id: string;
    assets: Array<{ id: string; name: string; content: string }>;
  }>;
};

const pipelineId = Buffer.from("analytics").toString("base64url");
const customersAssetId = Buffer.from("analytics/assets/analytics/customers.sql").toString(
  "base64url",
);
const customerStatsAssetId = Buffer.from("analytics/assets/analytics/customer_stats.sql").toString(
  "base64url",
);

test.describe("app build editor live", () => {
  test.use({ fixtureName: "configured-workspace" });

  test("edits an asset in Monaco and persists the change", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);

    const editor = page.locator(".monaco-editor").first();
    await expect(editor).toBeVisible({ timeout: 15000 });
    await expect(page.locator(".view-lines").first()).toContainText("customer_id", {
      timeout: 15000,
    });

    await editor.click();
    await page.keyboard.press("ControlOrMeta+End");
    const marker = "-- app editor smoke";
    await page.keyboard.insertText(`\n${marker}`);
    await expect(page.locator(".view-lines").first()).toContainText(marker);

    await expect
      .poll(
        async () => {
          const workspaceResponse = await page.request.get(`${liveApp.baseURL}/api/workspace`);
          expect(workspaceResponse.ok()).toBe(true);
          const workspace = (await workspaceResponse.json()) as WorkspaceResponse;
          return (
            workspace.pipelines
              .flatMap((pipeline) => pipeline.assets)
              .find((asset) => asset.id === customersAssetId)?.content ?? ""
          );
        },
        { timeout: 30000 },
      )
      .toContain(marker);
  });

  test("switches between light, dark, and system appearance", async ({ liveApp, page }) => {
    await page.emulateMedia({ colorScheme: "light" });
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);

    const openAppearanceMenu = async () => {
      const trigger = page.getByTestId("project-switcher-trigger");
      await expect(trigger).toBeVisible({ timeout: 15000 });
      // On mobile the header can still shift while Monaco and inspect results
      // settle. Keyboard activation avoids a coordinate click landing on the
      // adjacent Search control during that late layout movement.
      await trigger.focus();
      await trigger.press("Enter");
      await expect(page.getByRole("menuitemradio", { name: "Dark" })).toBeVisible({
        timeout: 10000,
      });
    };

    await openAppearanceMenu();
    await page.getByRole("menuitemradio", { name: "Dark" }).click();
    await expect(page.locator("html")).toHaveClass(/dark/);
    expect(await page.evaluate(() => localStorage.getItem("renart-theme"))).toBe("dark");

    await openAppearanceMenu();
    await page.getByRole("menuitemradio", { name: "System" }).click();
    await expect(page.locator("html")).not.toHaveClass(/dark/);
    expect(await page.evaluate(() => localStorage.getItem("renart-theme"))).toBe("system");

    await openAppearanceMenu();
    await page.getByRole("menuitemradio", { name: "Light" }).click();
    await expect(page.locator("html")).not.toHaveClass(/dark/);
    expect(await page.evaluate(() => localStorage.getItem("renart-theme"))).toBe("light");
  });

  test("ctrl+click on an upstream table opens that asset", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Ctrl+click navigation is a desktop mouse/keyboard affordance.",
    );

    await writeFile(
      join(liveApp.workspaceDir, "analytics", "assets", "analytics", "customer_stats.sql"),
      `/* @bruin
type: duckdb.sql
materialization:
  type: view
@bruin */

select customer_id, customer_name from analytics.customers
`,
      "utf8",
    );

    // Wait for the watcher to pick the new asset up so the initial workspace
    // load already contains it with full content.
    await expect
      .poll(
        async () => {
          const response = await page.request.get(`${liveApp.baseURL}/api/workspace`);
          if (!response.ok()) {
            return "";
          }
          const workspace = (await response.json()) as WorkspaceResponse;
          return (
            workspace.pipelines
              .flatMap((pipeline) => pipeline.assets)
              .find((asset) => asset.id === customerStatsAssetId)?.content ?? ""
          );
        },
        { timeout: 30000 },
      )
      .toContain("analytics.customers");

    await page.goto(
      `${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customerStatsAssetId}/code`,
    );

    const viewLines = page.locator(".view-lines").first();
    await expect(viewLines).toContainText("analytics.customers", {
      timeout: 15000,
    });

    const upstreamToken = viewLines.locator("span", { hasText: "customers" }).last();
    await expect
      .poll(
        async () => {
          await upstreamToken.click({ modifiers: ["ControlOrMeta"] });
          return page.url();
        },
        { timeout: 15000 },
      )
      .toContain(`/assets/${customersAssetId}/code`);

    await expect(page.locator(".view-lines").first()).toContainText("customer_name", {
      timeout: 15000,
    });
  });

  test("pipeline settings tags and domains use chips that allow commas", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Pipeline settings dialog coverage is desktop-only.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });

    await page.getByRole("button", { name: "Pipeline settings" }).click();
    const dialog = page.getByRole("dialog", { name: /Pipeline settings/ });
    await expect(dialog).toBeVisible({ timeout: 15000 });
    const settingsSidebar = dialog.getByRole("tablist", {
      name: "Pipeline settings sections",
    });
    await expect(settingsSidebar).toBeVisible();
    expect((await dialog.boundingBox())?.width).toBeGreaterThan(700);
    await expect(settingsSidebar.getByRole("tab", { name: "General" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(dialog.getByTestId("pipeline-settings-content")).toHaveAttribute(
      "data-slot",
      "scroll-area",
    );
    await settingsSidebar.getByRole("tab", { name: "Execution" }).click();
    await expect(dialog.getByRole("button", { name: "Manage schedules" })).toBeVisible();
    await expect(
      dialog.getByRole("spinbutton", { name: "Overlapping pipeline runs" }),
    ).toBeVisible();
    await expect(dialog.getByRole("spinbutton", { name: "Maximum active steps" })).toHaveAttribute(
      "placeholder",
      "1",
    );
    await expect(dialog).toContainText("Leave blank to run one asset at a time.");
    await settingsSidebar.getByRole("tab", { name: "General" }).click();

    await dialog.getByRole("textbox", { name: "Tags" }).fill("finance, north");
    await dialog.getByRole("textbox", { name: "Tags" }).press("Enter");
    await expect(dialog.getByText("finance, north")).toBeVisible();

    await dialog.getByRole("textbox", { name: "Domains" }).fill("sales, enterprise");
    await dialog.getByRole("textbox", { name: "Domains" }).press("Enter");
    await expect(dialog.getByText("sales, enterprise")).toBeVisible();

    const saveResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/config`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    await page.getByRole("button", { name: "Save changes" }).click();
    await saveResponse;

    const configResponse = await page.request.get(
      `${liveApp.baseURL}/api/pipelines/${pipelineId}/config`,
    );
    expect(configResponse.ok()).toBe(true);
    const config = (await configResponse.json()) as { tags?: string[]; domains?: string[] };
    expect(config.tags).toContain("finance, north");
    expect(config.domains).toContain("sales, enterprise");
  });

  test("pipeline connection defaults only offer configured platform and name pairs", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Pipeline settings connection picker coverage is desktop-only.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });
    await page.getByRole("button", { name: "Pipeline settings" }).click();

    const dialog = page.getByRole("dialog", { name: /Pipeline settings/ });
    await dialog.getByRole("tab", { name: "Connections" }).click();
    await expect(dialog).toContainText("Connection choices reflect default");
    const platform = dialog.getByRole("combobox", { name: "Platform" });
    const connection = dialog.getByRole("combobox", { name: "Connection" });
    await expect(platform).toContainText("duckdb");
    await expect(connection).toContainText("duckdb-default");
    await expect(dialog.getByRole("textbox", { name: "Platform" })).toHaveCount(0);
    await expect(dialog.getByRole("textbox", { name: "Connection" })).toHaveCount(0);

    await platform.click();
    await expect(page.getByRole("option", { name: "duckdb" })).toBeVisible();
    await expect(page.getByRole("option")).toHaveCount(1);
    await page.getByRole("option", { name: "duckdb" }).click();

    await connection.click();
    await expect(page.getByRole("option", { name: "duckdb-default" })).toBeVisible();
    await expect(page.getByRole("option")).toHaveCount(1);
    await page.getByRole("option", { name: "duckdb-default" }).click();
    await expect(dialog.getByRole("button", { name: "Add connection" })).toBeDisabled();

    await dialog.getByRole("button", { name: "Remove connection" }).click();
    await expect(dialog.getByRole("button", { name: "Add connection" })).toBeEnabled();
    await dialog.getByRole("button", { name: "Add connection" }).click();
    await expect(dialog.getByRole("combobox", { name: "Platform" })).toContainText("duckdb");
    await expect(dialog.getByRole("combobox", { name: "Connection" })).toContainText(
      "duckdb-default",
    );
  });

  test("pipeline settings keep a fixed full-height layout and manage Python dependencies", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Pipeline settings dialog layout coverage is desktop-only.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });
    await page.getByRole("button", { name: "Pipeline settings" }).click();

    const dialog = page.getByRole("dialog", { name: /Pipeline settings/ });
    await expect(dialog).toBeVisible({ timeout: 15000 });
    // The lazy-loading placeholder and the settings dialog share the same
    // accessible name. Wait for authored settings content before measuring so
    // the placeholder cannot be replaced between boundingBox() calls.
    await expect(dialog.getByRole("textbox", { name: "Pipeline name" })).toBeVisible({
      timeout: 15000,
    });
    await expect(dialog.getByRole("tab", { name: "Notifications" })).toHaveCount(0);
    await expect(dialog.getByText("Microsoft Teams")).toHaveCount(0);

    // boundingBox() includes the dialog's opening zoom transform. Wait for
    // that animation to finish before comparing the fixed-height sections.
    await expect
      .poll(() =>
        dialog.evaluate((element) =>
          element.getAnimations().every((animation) => animation.playState === "finished"),
        ),
      )
      .toBe(true);

    const tablist = dialog.getByRole("tablist", { name: "Pipeline settings sections" });
    const sidebar = dialog.getByTestId("pipeline-settings-navigation");
    const content = dialog.getByTestId("pipeline-settings-content");
    const generalDialogBounds = await dialog.boundingBox();
    const sidebarBounds = await sidebar.boundingBox();
    const contentBounds = await content.boundingBox();
    expect(generalDialogBounds).not.toBeNull();
    expect(sidebarBounds).not.toBeNull();
    expect(contentBounds).not.toBeNull();
    expect(Math.abs(sidebarBounds!.height - contentBounds!.height)).toBeLessThan(3);

    await tablist.getByRole("tab", { name: "Python" }).click();
    await expect(dialog.getByRole("textbox", { name: "Packages" })).toBeVisible();
    const pythonDialogBounds = await dialog.boundingBox();
    expect(pythonDialogBounds).not.toBeNull();
    expect(Math.abs(pythonDialogBounds!.height - generalDialogBounds!.height)).toBeLessThan(3);

    await dialog.getByRole("textbox", { name: "Packages" }).fill("polars>=1");
    await dialog.getByRole("textbox", { name: "Packages" }).press("Enter");
    await expect(dialog.getByText("polars>=1")).toBeVisible();

    const dependenciesResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/python-dependencies`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    await page.getByRole("button", { name: "Save changes" }).click();
    await dependenciesResponse;
    await expect(dialog).toBeHidden();

    const pyproject = await readFile(
      join(liveApp.workspaceDir, "analytics", "pyproject.toml"),
      "utf8",
    );
    expect(pyproject).toContain("polars>=1");
    expect(pyproject).toContain("renart-pipeline");
  });

  test("pipeline settings keep compact horizontal navigation on mobile", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      !test.info().project.name.includes("mobile"),
      "The desktop pipeline settings navigation is covered separately.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });
    await page.getByRole("button", { name: "Open explorer" }).click();
    await page.getByRole("button", { name: "Pipeline settings" }).click();

    const dialog = page.getByRole("dialog", { name: /Pipeline settings/ });
    await expect(dialog).toBeVisible({ timeout: 15000 });
    const navigation = dialog.getByRole("tablist", { name: "Pipeline settings sections" });
    await expect(navigation).toBeVisible();
    await navigation.getByRole("tab", { name: "Execution" }).click();
    await expect(dialog.getByRole("button", { name: "Manage schedules" })).toBeVisible();
    await expect(dialog.getByRole("textbox", { name: "Start date" })).toBeVisible();
  });

  test("pipeline settings open the filtered Renart schedules page", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Pipeline settings schedule navigation coverage is desktop-only.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });
    await page.getByRole("button", { name: "Pipeline settings" }).click();

    const dialog = page.getByRole("dialog", { name: /Pipeline settings/ });
    await expect(dialog.getByRole("tab", { name: "Schedule", exact: true })).toHaveCount(0);
    await dialog.getByRole("tab", { name: "Advanced" }).click();
    await expect(dialog).toContainText(
      "It does not create or update a Renart environment schedule.",
    );
    await dialog.getByRole("tab", { name: "Execution" }).click();
    await dialog.getByRole("button", { name: "Manage schedules" }).click();

    await expect(page).toHaveURL(/\/schedules[?].*pipeline=analytics/);
    await expect(page.getByRole("textbox", { name: "Filter schedules" })).toHaveValue("analytics");
  });

  test("pipeline settings guard unsaved changes", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Pipeline settings discard confirmation coverage is desktop-only.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });
    await page.getByRole("button", { name: "Pipeline settings" }).click();

    const dialog = page.getByRole("dialog", { name: /Pipeline settings/ });
    await dialog.getByRole("textbox", { name: "Owner" }).fill("data@example.com");
    await dialog.getByRole("button", { name: "Cancel" }).click();

    const confirmation = page.getByRole("alertdialog", {
      name: "Discard unsaved pipeline settings?",
    });
    await expect(confirmation).toBeVisible();
    await confirmation.getByRole("button", { name: "Keep editing" }).click();
    await expect(dialog.getByRole("textbox", { name: "Owner" })).toHaveValue("data@example.com");

    await dialog.getByRole("button", { name: "Cancel" }).click();
    await confirmation.getByRole("button", { name: "Discard changes" }).click();
    await expect(dialog).toBeHidden();
  });

  test("inferred pipeline defaults are shown and link to the project connection", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Pipeline settings dialog coverage is desktop-only.",
    );

    await writeFile(
      join(liveApp.workspaceDir, "analytics", "pipeline.yml"),
      `id: 693a3341-9762-42b5-a35f-c2a9efe94203
name: analytics
`,
      "utf8",
    );

    await expect
      .poll(
        async () => {
          const response = await page.request.get(
            `${liveApp.baseURL}/api/pipelines/${pipelineId}/config`,
          );
          if (!response.ok()) return [];
          const config = (await response.json()) as {
            inferred_default_connections?: Array<{ platform: string; name: string }>;
          };
          return config.inferred_default_connections ?? [];
        },
        { timeout: 30000 },
      )
      .toEqual([{ platform: "duckdb", name: "duckdb-default" }]);

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/canvas`);
    const connectionButton = page
      .getByTestId(`rf__node-${customersAssetId}`)
      .locator('button[aria-label^="Connection duckdb-default"]');
    await expect(connectionButton).toBeVisible({ timeout: 15000 });
    await connectionButton.click();

    const dialog = page.getByRole("dialog", { name: /Pipeline settings/ });
    await expect(dialog).toBeVisible({ timeout: 15000 });
    const inferred = dialog.getByTestId("inferred-default-connection");
    await expect(inferred).toContainText("duckdb");
    await expect(inferred).toContainText("duckdb-default");
    await expect(inferred).toContainText("Inferred");
    const referenced = dialog
      .getByTestId("referenced-pipeline-connection")
      .filter({ hasText: "duckdb-default" });
    await expect(referenced).toBeVisible();
    await expect(referenced).toContainText("analytics.customers");

    await inferred
      .getByRole("link", { name: "Open duckdb-default in project connection settings" })
      .click();
    await expect(page).toHaveURL(/\/project\/connections[?].*connection=duckdb-default/);
    await expect(page.getByRole("heading", { name: "duckdb-default" })).toBeVisible({
      timeout: 15000,
    });
  });
});
