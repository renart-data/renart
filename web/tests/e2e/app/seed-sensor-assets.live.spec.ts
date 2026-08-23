import { expect, type Locator, type Page } from "@playwright/test";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { liveTest as test, type LiveApp } from "../live-app-fixture";

const pipelineId = Buffer.from("analytics").toString("base64url");
const ordersAssetId = Buffer.from("analytics/assets/analytics/orders.sql").toString("base64url");
const seedPath = "analytics/assets/analytics/regional_customers.asset.yml";
const seedAssetId = Buffer.from(seedPath).toString("base64url");
const sensorPath = "analytics/assets/analytics/orders_ready.asset.yml";
const sensorAssetId = Buffer.from(sensorPath).toString("base64url");

type WorkspaceAsset = {
  id: string;
  name: string;
  type: string;
  parameters?: Record<string, string>;
  meta?: Record<string, string>;
  columns?: Array<{ name: string; type?: string }>;
};

type WorkspaceResponse = {
  pipelines: Array<{ id: string; assets: WorkspaceAsset[] }>;
};

test.describe("seed and sensor assets live", () => {
  test.use({ fixtureName: "configured-workspace" });

  test("creates, edits, and runs a seed and a sensor from the workbench", async ({
    liveApp,
    page,
  }) => {
    test.setTimeout(120000);
    test.skip(
      test.info().project.name.includes("mobile"),
      "The canvas creation flow is desktop-only.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${ordersAssetId}/canvas`);
    let dialog = await openNewAssetDialog(page);
    await dialog.getByRole("radio", { name: "Seed", exact: true }).click();
    await dialog.getByLabel("Asset name").fill("analytics.regional_customers");
    await expect(dialog.getByLabel("Target connection")).toContainText(
      "Pipeline default — duckdb-default",
    );
    await dialog.locator('input[type="file"]').setInputFiles({
      name: "regional_customers.csv",
      mimeType: "text/csv",
      buffer: Buffer.from("customer_id,customer_name\n10,Seed Ada\n", "utf8"),
    });
    await dialog.getByRole("switch", { name: "Enforce schema" }).click();

    const seedCreatedResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets`) &&
        response.request().method() === "POST",
      { timeout: 30000 },
    );
    await dialog.getByRole("button", { name: "Create", exact: true }).click();
    const seedCreate = await seedCreatedResponse;
    expect(seedCreate.ok(), await seedCreate.text()).toBe(true);

    const seed = await pollAsset(liveApp, page, "analytics.regional_customers", (asset) =>
      Boolean(asset.meta?.renart_seed_file),
    );
    expect(seed.type).toBe("duckdb.seed");
    expect(seed.parameters).toMatchObject({
      path: "./regional_customers.csv",
      file_type: "csv",
      enforce_schema: "false",
    });
    expect(seed.meta?.renart_seed_file).toBe("regional_customers.csv");
    expect(
      await readFile(
        join(liveApp.workspaceDir, "analytics/assets/analytics/regional_customers.csv"),
      ),
    ).toEqual(Buffer.from("customer_id,customer_name\n10,Seed Ada\n", "utf8"));
    const seedDefinition = await readFile(join(liveApp.workspaceDir, seedPath), "utf8");
    expect(seedDefinition).toContain("renart_seed_file: regional_customers.csv");
    expect(seedDefinition).toContain("path: ./regional_customers.csv");

    await page.getByRole("link", { name: "Code view" }).click();
    await expect(page.getByRole("button", { name: "Materialize", exact: true })).toBeVisible({
      timeout: 15000,
    });
    const seedEditor = page.getByTestId("semantic-parameters-editor");
    await expect(seedEditor).toHaveAttribute("data-asset-kind", "seed");
    await expect(seedEditor.locator(".monaco-editor")).toHaveCount(0);
    await expect(seedEditor.getByLabel("Seed path")).toHaveValue("./regional_customers.csv");
    await expect(seedEditor.getByLabel("Seed file format")).toContainText("csv");
    const seedInput = seedEditor.getByTestId("seed-replacement-input");
    await expect(seedInput).toBeVisible();
    await expect(seedInput.getByLabel("Seed data")).toHaveValue(
      "customer_id,customer_name\n10,Seed Ada\n",
    );
    await expect(seedInput).toContainText("Loaded the current CSV seed");
    const [seedInputBox, seedPathBox, seedFileTypeBox, enforceSchemaBox] = await Promise.all([
      seedInput.boundingBox(),
      seedEditor.getByLabel("Seed path").boundingBox(),
      seedEditor.getByLabel("Seed file format").boundingBox(),
      seedEditor.getByLabel("Enforce seed schema").boundingBox(),
    ]);
    expect(seedInputBox).not.toBeNull();
    expect(seedPathBox).not.toBeNull();
    expect(seedFileTypeBox).not.toBeNull();
    expect(enforceSchemaBox).not.toBeNull();
    expect(seedInputBox!.width).toBeLessThanOrEqual(768);
    expect(seedFileTypeBox!.y).toBeGreaterThanOrEqual(seedPathBox!.y + seedPathBox!.height);
    expect(enforceSchemaBox!.y).toBeGreaterThanOrEqual(
      seedFileTypeBox!.y + seedFileTypeBox!.height,
    );
    expect(seedInputBox!.y).toBeGreaterThanOrEqual(enforceSchemaBox!.y + enforceSchemaBox!.height);
    await expect(
      seedEditor.getByText("Columns and checks are configured in Properties."),
    ).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "Seed file" })).toHaveCount(0);

    const executionRequests: string[] = [];
    page.on("request", (request) => {
      const url = request.url();
      if (
        request.method() === "POST" &&
        (url.includes("/materialize/stream") ||
          url.includes("/trigger") ||
          url.endsWith("/api/run"))
      ) {
        executionRequests.push(url);
      }
    });
    const seedRenderResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets/render`) && response.ok(),
      { timeout: 30000 },
    );
    await page.getByRole("button", { name: "Render saved asset", exact: true }).click();
    const seedRenderPayload = (await (await seedRenderResponse).json()) as {
      stages: Array<{ content?: string; fidelity: string }>;
    };
    expect(seedRenderPayload.stages).toContainEqual(
      expect.objectContaining({
        content: expect.stringContaining('"operation": "sling_load"'),
        fidelity: "semantic",
      }),
    );
    await expect(page.getByTestId("asset-render-view")).toContainText("Preview — not executed", {
      timeout: 15000,
    });
    expect(executionRequests).toEqual([]);

    const seedProperties = await openAssetProperties(page);
    const seedColumns = seedProperties.locator("section").filter({ hasText: "Columns" }).first();
    await expect(seedColumns.getByRole("heading", { name: "Columns" })).toBeVisible({
      timeout: 15000,
    });
    const initialSyncResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${seedAssetId}/columns/sync`) &&
        response.request().method() === "POST",
      { timeout: 30000 },
    );
    await seedColumns.getByRole("button", { name: "Sync schema", exact: true }).click();
    const initialSync = await initialSyncResponse;
    expect(initialSync.ok(), await initialSync.text()).toBe(true);
    const initialSyncBody = (await initialSync.json()) as { status: string };
    expect(initialSyncBody.status).toBe("applied");
    await expect(seedColumns.getByText(/Safe changes were applied automatically/)).toBeVisible();
    await pollAsset(
      liveApp,
      page,
      "analytics.regional_customers",
      (asset) =>
        asset.columns?.some((column) => column.name === "customer_id") === true &&
        asset.columns?.some((column) => column.name === "customer_name") === true,
    );

    await seedEditor.getByTestId("seed-file-drop-target").evaluate((element) => {
      const transfer = new DataTransfer();
      transfer.items.add(
        new File(["customer_id,customer_name\n99,Dropped but cancelled\n"], "dropped.csv", {
          type: "text/csv",
        }),
      );
      element.dispatchEvent(
        new DragEvent("drop", { bubbles: true, cancelable: true, dataTransfer: transfer }),
      );
    });
    const droppedConfirmation = page.getByRole("alertdialog", {
      name: "Replace the seed file?",
    });
    await expect(droppedConfirmation).toContainText("the dropped file");
    await droppedConfirmation.getByRole("button", { name: "Cancel", exact: true }).click();

    const replacement = Buffer.from(
      "customer_id,customer_name,segment\n20,Replacement Grace,enterprise\n",
      "utf8",
    );
    const seedUploadResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${seedAssetId}/seed-file`) &&
        response.request().method() === "POST",
      { timeout: 30000 },
    );
    const replacementRefreshResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${seedAssetId}/columns/refresh-from-definition`) &&
        response.request().method() === "POST",
      { timeout: 30000 },
    );
    await seedEditor.getByLabel("Upload seed file").setInputFiles({
      name: "regional_customers_v2.csv",
      mimeType: "text/csv",
      buffer: replacement,
    });
    const replacementDialog = page.getByRole("alertdialog", { name: "Replace the seed file?" });
    await expect(replacementDialog).toBeVisible();
    await expect(replacementDialog).toContainText("./regional_customers.csv");
    await replacementDialog.getByRole("button", { name: "Replace file", exact: true }).click();
    const [seedUpload, replacementRefresh] = await Promise.all([
      seedUploadResponse,
      replacementRefreshResponse,
    ]);
    expect(seedUpload.ok(), await seedUpload.text()).toBe(true);
    expect(replacementRefresh.ok(), await replacementRefresh.text()).toBe(true);
    await expect(
      seedEditor.getByText("regional_customers_v2.csv uploaded and columns refreshed."),
    ).toBeVisible({ timeout: 15000 });

    const replacedSeed = await pollAsset(
      liveApp,
      page,
      "analytics.regional_customers",
      (asset) =>
        asset.parameters?.path === "./regional_customers_v2.csv" &&
        asset.meta?.renart_seed_file === "regional_customers_v2.csv" &&
        asset.columns?.some((column) => column.name === "segment") === true,
    );
    expect(replacedSeed.parameters?.file_type).toBe("csv");
    expect(
      await readFile(
        join(liveApp.workspaceDir, "analytics/assets/analytics/regional_customers_v2.csv"),
      ),
    ).toEqual(replacement);
    let oldSeedError: NodeJS.ErrnoException | undefined;
    try {
      await readFile(
        join(liveApp.workspaceDir, "analytics/assets/analytics/regional_customers.csv"),
      );
    } catch (error) {
      oldSeedError = error as NodeJS.ErrnoException;
    }
    expect(oldSeedError?.code).toBe("ENOENT");
    const replacedDefinition = await readFile(join(liveApp.workspaceDir, seedPath), "utf8");
    expect(replacedDefinition).toContain("path: ./regional_customers_v2.csv");
    expect(replacedDefinition).toContain("renart_seed_file: regional_customers_v2.csv");
    expect(replacedDefinition).toContain("name: segment");
    await expect(seedEditor.getByLabel("Seed path")).toHaveValue("./regional_customers_v2.csv");
    await expect(seedInput.getByLabel("Seed data")).toHaveValue(replacement.toString("utf8"));

    const seedMaterializeResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${seedAssetId}/materialize/stream`) && response.ok(),
      { timeout: 30000 },
    );
    await page.getByRole("button", { name: "Materialize", exact: true }).click();
    const seedMaterialize = await readMaterializeResult(await seedMaterializeResponse);
    expect(
      seedMaterialize.status,
      seedMaterialize.error || seedMaterialize.output || "seed materialization failed",
    ).toBe("ok");
    await expect(page.locator("pre.font-console").first()).toContainText(/regional_customers/i, {
      timeout: 30000,
    });

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${ordersAssetId}/canvas`);
    const ordersMaterialize = await materializeAsset(page, liveApp.baseURL, ordersAssetId);
    expect(
      ordersMaterialize.status,
      ordersMaterialize.error || ordersMaterialize.output || "orders materialization failed",
    ).toBe("ok");

    dialog = await openNewAssetDialog(page);
    await dialog.getByRole("radio", { name: "Sensor", exact: true }).click();
    await dialog.getByLabel("Asset name").fill("analytics.orders_ready");
    await expect(dialog.getByLabel("Connection to check")).toContainText(
      "Pipeline default — duckdb-default",
    );
    await dialog.getByLabel("Ready condition query").fill("select true");

    const sensorCreatedResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets`) &&
        response.request().method() === "POST",
      { timeout: 30000 },
    );
    await dialog.getByRole("button", { name: "Create", exact: true }).click();
    const sensorCreate = await sensorCreatedResponse;
    expect(sensorCreate.ok(), await sensorCreate.text()).toBe(true);

    const sensor = await pollAsset(liveApp, page, "analytics.orders_ready", (asset) =>
      Boolean(asset.parameters?.query),
    );
    expect(sensor.type).toBe("duckdb.sensor.query");
    expect(sensor.parameters).toMatchObject({
      query: "select true",
      poke_interval: "30",
      timeout: "24h",
    });
    const sensorDefinition = await readFile(join(liveApp.workspaceDir, sensorPath), "utf8");
    expect(sensorDefinition).toContain("poke_interval: 30");
    expect(sensorDefinition).toContain("timeout: 24h");

    await page.getByRole("link", { name: "Code view" }).click();
    await expect(page.getByRole("button", { name: "Check now", exact: true })).toBeVisible({
      timeout: 15000,
    });
    const sensorEditor = page.getByTestId("semantic-parameters-editor");
    await expect(sensorEditor).toHaveAttribute("data-asset-kind", "sensor-query");
    await expect(sensorEditor.locator(".monaco-editor")).toBeVisible({ timeout: 15000 });
    await expect.poll(() => monacoEditorValue(page)).toBe("select true");
    await expect(
      sensorEditor.getByText("Columns and checks are configured in Properties."),
    ).toHaveCount(0);
    await expect(page.getByRole("heading", { name: "Sensor condition" })).toHaveCount(0);

    const completionQuery = "select count(*) > 0\nfrom analytics.orders o\nwhere o. > 0";
    const completionResponse = page.waitForResponse(
      (response) =>
        response.url().includes("/api/sql/lsp/completions") &&
        response.request().method() === "POST" &&
        (response.request().postData() ?? "").includes(sensorAssetId),
      { timeout: 15000 },
    );
    await setMonacoContentAndCursor(page, completionQuery, "where o.");
    await page.keyboard.press("ControlOrMeta+Space");
    await completionResponse;
    const suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible({ timeout: 15000 });
    const orderIDCompletion = suggestWidget
      .locator(".monaco-list-row")
      .filter({ hasText: "order_id" })
      .first();
    await expect(orderIDCompletion).toBeVisible();
    const querySaveResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets/${sensorAssetId}`) &&
        response.request().method() === "PUT" &&
        (response.request().postData() ?? "").includes("order_id") &&
        response.ok(),
      { timeout: 15000 },
    );
    await orderIDCompletion.click();
    await querySaveResponse;
    await expect.poll(() => monacoEditorValue(page)).toContain("where o.order_id > 0");
    await pollAsset(
      liveApp,
      page,
      "analytics.orders_ready",
      (asset) => asset.parameters?.query?.includes("where o.order_id > 0") === true,
    );

    const timeoutInput = sensorEditor.getByLabel("Sensor timeout");
    await timeoutInput.fill("2h");
    const timeoutResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets/${sensorAssetId}`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    await timeoutInput.press("Enter");
    await timeoutResponse;
    await pollAsset(
      liveApp,
      page,
      "analytics.orders_ready",
      (asset) => asset.parameters?.timeout === "2h",
    );

    const sensorProperties = await openAssetProperties(page);
    await expect(sensorProperties.getByRole("heading", { name: "Identity" })).toBeVisible({
      timeout: 15000,
    });
    await expect(sensorProperties.getByRole("heading", { name: "Columns" })).toHaveCount(0);
    await expect(sensorProperties.getByRole("heading", { name: "Quality checks" })).toHaveCount(0);

    const sensorRunResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${sensorAssetId}/materialize/stream`) && response.ok(),
      { timeout: 30000 },
    );
    await page.getByRole("button", { name: "Check now", exact: true }).click();
    const sensorRun = await readMaterializeResult(await sensorRunResponse);
    expect(sensorRun.status, sensorRun.error || sensorRun.output || "sensor check failed").toBe(
      "ok",
    );
    await expect
      .poll(
        async () => {
          const response = await page.request.get(
            `${liveApp.baseURL}/api/pipelines/${pipelineId}/staleness?environment=default`,
          );
          const body = (await response.json()) as {
            assets: Array<{
              asset_name: string;
              status: string;
              volatile?: boolean;
              last_run_status?: string;
            }>;
          };
          return body.assets.find((asset) => asset.asset_name === "analytics.orders_ready");
        },
        { timeout: 30000 },
      )
      .toMatchObject({ status: "volatile", volatile: true, last_run_status: "succeeded" });
  });

  test("creates a seed from the workspace picker and keeps asset choices aligned", async ({
    liveApp,
    page,
  }) => {
    test.setTimeout(120000);
    test.skip(
      test.info().project.name.includes("mobile"),
      "The canvas creation flow is desktop-only.",
    );

    const workspaceSeedPath = join(liveApp.workspaceDir, "data", "workspace_customers.csv");
    await mkdir(join(liveApp.workspaceDir, "data"), { recursive: true });
    await writeFile(workspaceSeedPath, "customer_id,customer_name\n20,Workspace Grace\n", "utf8");

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${ordersAssetId}/canvas`);
    const dialog = await openNewAssetDialog(page);
    expect((await dialog.boundingBox())?.width).toBeGreaterThan(700);
    await expect(dialog.getByRole("button", { name: "Create", exact: true })).toHaveCSS(
      "cursor",
      "pointer",
    );

    const assetKinds = ["SQL", "Python", "HTTP API", "Seed", "Sensor", "Load"];
    const selectorBoxes = await Promise.all(
      assetKinds.map(async (name) => {
        const selector = dialog.getByRole("radio", { name, exact: true });
        await expect(selector).toBeVisible();
        await expect(selector.locator('[data-slot="asset-kind-label"]')).toHaveCSS(
          "white-space",
          "nowrap",
        );
        await expect(selector.locator('[data-slot="asset-kind-description"]')).toHaveCSS(
          "white-space",
          "nowrap",
        );
        expect(
          await selector.evaluate((element) => element.scrollHeight <= element.clientHeight + 1),
        ).toBe(true);
        const box = await selector.boundingBox();
        expect(box).not.toBeNull();
        return { width: Math.round(box!.width), height: Math.round(box!.height) };
      }),
    );
    expect(new Set(selectorBoxes.map(({ width }) => width)).size).toBe(1);
    expect(new Set(selectorBoxes.map(({ height }) => height)).size).toBe(1);

    await dialog.getByRole("radio", { name: "Seed", exact: true }).click();
    await expect(dialog.getByLabel("Target connection")).toBeVisible();
    await expect(dialog).not.toContainText("duckdb.seed");
    await expect(dialog.getByRole("button", { name: "Change type", exact: true })).toBeVisible();
    await expect(dialog.getByRole("radio", { name: "Seed", exact: true })).toBeHidden();
    expect(await dialog.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(
      true,
    );
    await dialog.getByLabel("Asset name").fill("analytics.workspace_customers");

    await dialog.getByLabel("Target connection").click();
    await page.getByRole("option", { name: "New connection…", exact: true }).click();
    const connectionDialog = page.getByRole("dialog", { name: "New connection" });
    await expect(connectionDialog).toBeVisible();
    await expect(connectionDialog.getByLabel("Environment")).toBeDisabled();
    await expect(connectionDialog.getByLabel("Environment")).toContainText("default");
    await connectionDialog.getByLabel("Name").fill("seed-secondary");
    await connectionDialog.getByLabel("Type").click();
    await page.getByRole("option", { name: "duckdb", exact: true }).click();
    await connectionDialog.getByLabel("path").fill("duckdb-files/seed-secondary.db");
    await connectionDialog.getByRole("button", { name: "Create connection" }).click();
    await expect(connectionDialog).toBeHidden({ timeout: 15000 });

    await expect(dialog.getByLabel("Asset name")).toHaveValue("analytics.workspace_customers");
    await expect(dialog.getByLabel("Target connection")).toContainText("seed-secondary");
    await dialog.getByRole("radio", { name: "Workspace", exact: true }).click();
    await dialog.getByRole("button", { name: "Choose workspace seed file" }).click();

    const pathInput = page.getByPlaceholder("Type a path…");
    await expect(pathInput).toBeVisible();
    await pathInput.fill("./data/");
    await page.getByRole("option", { name: "./data/workspace_customers.csv", exact: true }).click();
    await expect(dialog.getByRole("button", { name: "Choose workspace seed file" })).toContainText(
      "./data/workspace_customers.csv",
    );

    const seedCreatedResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets`) &&
        response.request().method() === "POST",
      { timeout: 30000 },
    );
    await dialog.getByRole("button", { name: "Create", exact: true }).click();
    const seedCreate = await seedCreatedResponse;
    expect(seedCreate.ok(), await seedCreate.text()).toBe(true);

    const seed = await pollAsset(liveApp, page, "analytics.workspace_customers", (asset) =>
      Boolean(asset.parameters?.path),
    );
    expect(seed.type).toBe("duckdb.seed");
    expect(seed.parameters).toMatchObject({
      path: "../../../data/workspace_customers.csv",
      file_type: "csv",
      enforce_schema: "true",
    });

    const definition = await readFile(
      join(liveApp.workspaceDir, "analytics/assets/analytics/workspace_customers.asset.yml"),
      "utf8",
    );
    expect(definition).toContain("path: ../../../data/workspace_customers.csv");
    expect(definition).toContain("connection: seed-secondary");
    expect(definition).not.toContain("workspace_path");
  });

  test("creates and replaces a seed from pasted clipboard data", async ({ liveApp, page }) => {
    test.setTimeout(120000);
    test.skip(
      test.info().project.name.includes("mobile"),
      "The canvas creation flow is desktop-only.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${ordersAssetId}/canvas`);
    const dialog = await openNewAssetDialog(page);
    await dialog.getByRole("radio", { name: "Seed", exact: true }).click();
    await expect(dialog.getByLabel("Target connection")).toBeVisible();
    await expect(dialog).not.toContainText("duckdb.seed");
    await dialog.getByLabel("Asset name").fill("analytics.pasted_customers");
    await dialog.getByRole("radio", { name: "Paste", exact: true }).click();
    await dialog.getByLabel("Pasted data").fill("customer_id,customer_name\n30,Clipboard Lin\n");
    await expect(dialog.getByLabel("Pasted format")).toContainText("Auto (CSV)");

    const createdResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets`) &&
        response.request().method() === "POST",
      { timeout: 30000 },
    );
    await dialog.getByRole("button", { name: "Create", exact: true }).click();
    const created = await createdResponse;
    expect(created.ok(), await created.text()).toBe(true);

    const pastedPath = "analytics/assets/analytics/pasted_customers.asset.yml";
    const pastedAssetId = Buffer.from(pastedPath).toString("base64url");
    const pasted = await pollAsset(liveApp, page, "analytics.pasted_customers", (asset) =>
      Boolean(asset.meta?.renart_seed_file),
    );
    expect(pasted.parameters).toMatchObject({
      path: "./pasted_customers.csv",
      file_type: "csv",
    });
    expect(
      await readFile(
        join(liveApp.workspaceDir, "analytics/assets/analytics/pasted_customers.csv"),
        "utf8",
      ),
    ).toBe("customer_id,customer_name\n30,Clipboard Lin\n");

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${pastedAssetId}/code`);
    const seedEditor = page.getByTestId("semantic-parameters-editor");
    await expect(seedEditor).toHaveAttribute("data-asset-kind", "seed");
    const seedInput = seedEditor.getByTestId("seed-replacement-input");
    await expect(seedInput).toBeVisible();
    await expect(seedInput.getByLabel("Seed data")).toHaveValue(
      "customer_id,customer_name\n30,Clipboard Lin\n",
    );
    await seedInput
      .getByLabel("Seed data")
      .fill('{"customer_id":31,"customer_name":"Clipboard Mei"}\n');
    await expect(seedInput.getByLabel("Pasted format")).toContainText("Auto (JSON)");
    await seedInput.getByRole("button", { name: "Save", exact: true }).click();

    const confirmation = page.getByRole("alertdialog", { name: "Replace the seed file?" });
    await expect(confirmation).toContainText("pasted JSON data");
    const uploadResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${pastedAssetId}/seed-file`) &&
        response.request().method() === "POST",
      { timeout: 30000 },
    );
    await confirmation.getByRole("button", { name: "Replace file", exact: true }).click();
    const upload = await uploadResponse;
    expect(upload.ok(), await upload.text()).toBe(true);

    const replaced = await pollAsset(
      liveApp,
      page,
      "analytics.pasted_customers",
      (asset) => asset.parameters?.path === "./pasted_customers.json",
    );
    expect(replaced.parameters?.file_type).toBe("json");
    expect(
      await readFile(
        join(liveApp.workspaceDir, "analytics/assets/analytics/pasted_customers.json"),
        "utf8",
      ),
    ).toBe('{"customer_id":31,"customer_name":"Clipboard Mei"}\n');
  });
});

async function openNewAssetDialog(page: Page) {
  await page.getByRole("button", { name: "New asset" }).first().click();
  const dialog = page.getByRole("dialog", { name: "New asset" });
  await expect(dialog).toBeVisible({ timeout: 15000 });
  return dialog;
}

async function openAssetProperties(page: Page): Promise<Locator> {
  const inspector = page.locator('[data-testid="asset-inspector"]:visible').first();
  if (!(await inspector.isVisible().catch(() => false))) {
    const trigger = page
      .getByRole("button", { name: "Asset properties" })
      .or(page.getByRole("button", { name: "Show properties" }))
      .first();
    await expect(trigger).toBeVisible({ timeout: 15000 });
    await trigger.click();
  }
  await expect(inspector).toBeVisible({ timeout: 15000 });
  return inspector;
}

async function pollAsset(
  liveApp: LiveApp,
  page: Page,
  assetName: string,
  predicate: (asset: WorkspaceAsset) => boolean,
) {
  let found: WorkspaceAsset | undefined;
  await expect
    .poll(
      async () => {
        const response = await page.request.get(`${liveApp.baseURL}/api/workspace`);
        const workspace = (await response.json()) as WorkspaceResponse;
        found = workspace.pipelines
          .flatMap((pipeline) => pipeline.assets)
          .find((asset) => asset.name === assetName);
        return found ? predicate(found) : false;
      },
      { timeout: 30000 },
    )
    .toBe(true);
  if (!found) throw new Error(`asset ${assetName} was not found`);
  return found;
}

async function materializeAsset(page: Page, baseURL: string, assetId: string) {
  const response = await page.request.post(
    `${baseURL}/api/assets/${assetId}/materialize/stream?environment=default`,
  );
  return readMaterializeResult(response);
}

async function readMaterializeResult(response: { ok(): boolean; text(): Promise<string> }) {
  const stream = await response.text();
  expect(response.ok(), stream).toBe(true);
  const doneLine = stream
    .split(/\r?\n/)
    .reverse()
    .find((line) => line.startsWith("data: ") && line.includes('"status"'));
  if (!doneLine) throw new Error(`materialize stream did not contain a done event:\n${stream}`);
  return JSON.parse(doneLine.slice("data: ".length)) as {
    status: string;
    output?: string;
    error?: string;
  };
}

async function setMonacoContentAndCursor(page: Page, content: string, cursorAfter: string) {
  await page.waitForFunction(
    () => {
      const monaco = (window as typeof window & { monaco?: any }).monaco;
      return Boolean(monaco?.editor.getEditors?.()[0]?.getModel?.());
    },
    undefined,
    { timeout: 15000 },
  );
  await page.evaluate(
    ({ content, cursorAfter }) => {
      const monaco = (window as typeof window & { monaco?: any }).monaco;
      const editor = monaco?.editor.getEditors?.()[0];
      const model = editor?.getModel?.();
      if (!editor || !model) throw new Error("Monaco editor is not ready");
      const cursorOffset = content.indexOf(cursorAfter);
      if (cursorOffset < 0) throw new Error(`cursor text ${cursorAfter} was not found`);
      model.setValue(content);
      editor.focus();
      editor.setPosition(model.getPositionAt(cursorOffset + cursorAfter.length));
    },
    { content, cursorAfter },
  );
}

async function monacoEditorValue(page: Page) {
  return page.evaluate(() => {
    const monaco = (window as typeof window & { monaco?: any }).monaco;
    return monaco?.editor.getEditors?.()[0]?.getModel?.()?.getValue?.() ?? "";
  });
}
