import { expect, type Locator, type Page } from "@playwright/test";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";

import { liveTest as test, timeoutForRetry, type LiveApp } from "../live-app-fixture";

test.use({
  liveAppEnv: {
    RENART_E2E_REVIEW_POSTGRES_PASSWORD: "renart",
  },
});

const pipelineId = Buffer.from("analytics").toString("base64url");
const customersAssetId = Buffer.from("analytics/assets/analytics/customers.sql").toString(
  "base64url",
);
const ordersAssetId = Buffer.from("analytics/assets/analytics/orders.sql").toString("base64url");
const downstreamAssetId = Buffer.from("analytics/assets/analytics/downstream.sql").toString(
  "base64url",
);
const loadAssetPath = "analytics/assets/analytics/orders_load.asset.yml";
const loadAssetId = Buffer.from(loadAssetPath).toString("base64url");

type WorkspaceAsset = {
  id: string;
  name: string;
  type?: string;
  content?: string;
  connection?: string;
  explicit_connection?: string;
  parameters?: Record<string, string>;
  upstreams: string[];
  meta?: Record<string, string>;
  tags?: string[];
  materialization_strategy?: string;
  incremental_key?: string;
  time_granularity?: string;
  columns?: Array<{
    name: string;
    type?: string;
    primary_key?: boolean;
    update_on_merge?: boolean;
    merge_sql?: string;
    meta?: Record<string, string>;
  }>;
  custom_checks?: Array<{
    name: string;
    description?: string;
    value: number;
    count?: number;
    blocking?: boolean;
    query: string;
  }>;
  pre_hooks?: string[];
  post_hooks?: string[];
};
type WorkspaceResponse = { pipelines: Array<{ id: string; assets: WorkspaceAsset[] }> };

async function fetchAsset(
  liveApp: LiveApp,
  request: { get: (url: string) => Promise<{ json(): Promise<unknown> }> },
  assetName: string,
): Promise<WorkspaceAsset | undefined> {
  const response = await request.get(`${liveApp.baseURL}/api/workspace`);
  const workspace = (await response.json()) as WorkspaceResponse;
  return workspace.pipelines.flatMap((p) => p.assets).find((a) => a.name === assetName);
}

async function pollAsset(
  liveApp: LiveApp,
  request: { get: (url: string) => Promise<{ json(): Promise<unknown> }> },
  assetName: string,
  predicate: (asset: WorkspaceAsset) => boolean,
): Promise<WorkspaceAsset> {
  let found: WorkspaceAsset | undefined;
  await expect
    .poll(
      async () => {
        found = await fetchAsset(liveApp, request, assetName);
        return found ? predicate(found) : false;
      },
      { timeout: timeoutForRetry(test.info(), 30000) },
    )
    .toBe(true);
  if (!found) throw new Error(`asset ${assetName} never satisfied predicate`);
  return found;
}

async function openAssetProperties(page: Page): Promise<Locator> {
  const inspector = page.locator('[data-testid="asset-inspector"]:visible').first();
  const trigger = page
    .getByRole("button", { name: "Asset properties" })
    .or(page.getByRole("button", { name: "Show properties" }))
    .first();
  const timeout = timeoutForRetry(test.info(), 15000);

  // Desktop renders the inspector directly, while compact layouts render its
  // trigger. Wait for either route-dependent state before deciding to click.
  await expect
    .poll(async () => (await inspector.isVisible()) || (await trigger.isVisible()), { timeout })
    .toBe(true);
  if (!(await inspector.isVisible().catch(() => false))) {
    await trigger.click();
  }
  await expect(inspector).toBeVisible({ timeout });
  return inspector;
}

async function expectCompactAssetDescription(page: Page, description: string) {
  const describedNode = page.getByTestId(`rf__node-${customersAssetId}`);
  const baselineNode = page.getByTestId(`rf__node-${ordersAssetId}`);
  await expect(describedNode).toBeVisible({ timeout: 15000 });
  await expect(baselineNode).toBeVisible({ timeout: 15000 });

  const metadata = describedNode.locator('[data-slot="asset-node-metadata"]');
  const connection = metadata.locator('[data-slot="asset-node-connection"]');
  const descriptionElement = metadata.locator('[data-slot="asset-node-description"]');
  await expect(connection).toBeVisible();
  await expect(descriptionElement).toHaveText(description);
  await expect(metadata).toHaveCSS("display", "flex");
  await expect(metadata).toHaveCSS("align-items", "center");
  const metadataOrder = await metadata
    .locator(":scope > [data-slot]")
    .evaluateAll((elements) => elements.map((element) => element.getAttribute("data-slot")));
  expect(metadataOrder).toEqual(["asset-node-description", "asset-node-connection"]);
  const [descriptionX, connectionX] = await Promise.all(
    [descriptionElement, connection].map((element) =>
      element.evaluate((node) => node.getBoundingClientRect().x),
    ),
  );
  expect(descriptionX).toBeLessThan(connectionX);
  await expect(descriptionElement).toHaveCSS("overflow", "hidden");
  await expect(descriptionElement).toHaveCSS("text-overflow", "ellipsis");
  await expect(descriptionElement).toHaveCSS("white-space", "nowrap");

  const descriptionOverflows = await descriptionElement.evaluate(
    (element) => element.scrollWidth > element.clientWidth,
  );
  expect(descriptionOverflows).toBe(true);

  const [describedHeight, baselineHeight] = await Promise.all(
    [describedNode, baselineNode].map((node) =>
      node
        .locator('[data-slot="asset-node"]')
        .evaluate((element) => element.getBoundingClientRect().height),
    ),
  );
  expect(Math.abs(describedHeight - baselineHeight)).toBeLessThanOrEqual(1);
}

test.describe("app asset editing workbench live", () => {
  test.use({ fixtureName: "configured-workspace" });

  test("keeps downstream SQL on the upstream warehouse", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "The canvas downstream affordance is desktop-only.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/canvas`);
    const node = page.getByTestId(`rf__node-${customersAssetId}`);
    await node.hover();
    await node.getByRole("button", { name: "Create downstream asset" }).click();

    const dialog = page.getByRole("dialog", { name: "New downstream asset" });
    await expect(dialog).toBeVisible({ timeout: 15000 });
    await expect(dialog.getByLabel("Target connection")).toHaveCount(0);
    await expect(dialog.getByText(/bruin/i)).toHaveCount(0);

    await dialog.getByRole("radio", { name: "Python", exact: true }).click();
    await expect(dialog.getByLabel("Target connection")).toBeVisible();
    await dialog.getByRole("button", { name: "Change type" }).click();
    await dialog.getByRole("radio", { name: "SQL", exact: true }).click();
    await expect(dialog.getByLabel("Target connection")).toHaveCount(0);
  });

  test("reviews cross-engine connection migrations and keeps type read-only", async ({
    liveApp,
    page,
  }) => {
    const createConnection = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
      data: {
        environment_name: "default",
        name: "review-postgres",
        type: "postgres",
        values: {
          host: "127.0.0.1",
          port: 5432,
          username: "renart",
          database: "analytics",
        },
        secret_changes: {
          password: {
            action: "replace",
            binding: { ref: "env:RENART_E2E_REVIEW_POSTGRES_PASSWORD" },
          },
        },
      },
    });
    expect(createConnection.ok()).toBe(true);

    const connectDownstream = await page.request.put(
      `${liveApp.baseURL}/api/pipelines/${pipelineId}/assets/${ordersAssetId}`,
      {
        data: {
          content: `/* @bruin
type: duckdb.sql
materialization:
  type: view
@bruin */

select customer_id from analytics.customers
`,
        },
      },
    );
    expect(connectDownstream.ok()).toBe(true);
    await pollAsset(liveApp, page.request, "analytics.orders", (asset) =>
      asset.upstreams.includes("analytics.customers"),
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    const properties = await openAssetProperties(page);
    const type = properties.getByRole("textbox", { name: "Type", exact: true });
    await expect(type).toHaveValue("duckdb.sql");
    await expect(type).toHaveAttribute("readonly", "");
    await expect(properties.getByRole("combobox", { name: "Type" })).toHaveCount(0);

    const connection = properties.getByRole("combobox", { name: "Connection" });
    await connection.click();
    await page.getByRole("option", { name: "review-postgres", exact: true }).click();

    const migration = page.getByTestId("asset-connection-migration-dialog");
    await expect(migration).toBeVisible();
    await expect(migration).toContainText("duckdb.sql");
    await expect(migration).toContainText("pg.sql");
    const lineageWarning = migration.getByTestId("asset-connection-lineage-warning");
    await expect(lineageWarning).toContainText("Pure SQL cannot query across connections");
    await expect(lineageWarning).toContainText("analytics.orders");
    await migration.getByRole("button", { name: "Cancel" }).click();

    const unchanged = await fetchAsset(liveApp, page.request, "analytics.customers");
    expect(unchanged?.type).toBe("duckdb.sql");

    await connection.click();
    await page.getByRole("option", { name: "review-postgres", exact: true }).click();
    const updateResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets/${customersAssetId}`) &&
        response.request().method() === "PUT",
      { timeout: 15000 },
    );
    await migration.getByRole("button", { name: "Change engine" }).click();
    const response = await updateResponse;
    expect(response.ok()).toBe(true);
    expect(response.request().postDataJSON()).toMatchObject({
      connection_selection: {
        connection: "review-postgres",
        expected_asset_type: "duckdb.sql",
        confirm_type_migration: true,
      },
    });

    const migrated = await pollAsset(
      liveApp,
      page.request,
      "analytics.customers",
      (asset) => asset.type === "pg.sql" && asset.explicit_connection === "review-postgres",
    );
    expect(migrated.connection).toBe("review-postgres");
    await expect(type).toHaveValue("pg.sql", { timeout: 15000 });
  });

  test("opens the effective connection from asset metadata", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    const properties = await openAssetProperties(page);

    await properties.getByRole("button", { name: "Go to connection duckdb-default" }).click();

    await expect(page).toHaveURL(
      /\/project\/connections\?environment=default&connection=duckdb-default$/,
    );
    await expect(page.getByRole("dialog", { name: "duckdb-default" })).toBeVisible({
      timeout: 15000,
    });
  });

  test("long connection names do not make asset properties scroll horizontally", async ({
    liveApp,
    page,
  }) => {
    const connectionName =
      "duckdb-connection-with-an-intentionally-long-name-for-the-asset-properties-pane";
    const createConnection = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
      data: {
        environment_name: "default",
        name: connectionName,
        type: "duckdb",
        values: { path: "duckdb-files/long-name.db" },
      },
    });
    expect(createConnection.ok()).toBe(true);
    const update = await page.request.put(
      `${liveApp.baseURL}/api/pipelines/${pipelineId}/assets/${customersAssetId}`,
      { data: { connection: connectionName } },
    );
    expect(update.ok()).toBe(true);
    await pollAsset(
      liveApp,
      page.request,
      "analytics.customers",
      (asset) => asset.explicit_connection === connectionName,
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    const properties = await openAssetProperties(page);
    const connection = properties.getByRole("combobox", { name: "Connection" });
    await expect(connection).toContainText(connectionName, { timeout: 15000 });

    const overflow = await connection.evaluate((trigger) => {
      const viewport = trigger.closest('[data-slot="scroll-area-viewport"]');
      if (!(viewport instanceof HTMLElement)) {
        throw new Error("Asset properties scroll viewport was not found");
      }
      return {
        viewportClientWidth: viewport.clientWidth,
        viewportScrollWidth: viewport.scrollWidth,
        triggerWidth: trigger.getBoundingClientRect().width,
      };
    });
    expect(overflow.viewportScrollWidth).toBeLessThanOrEqual(overflow.viewportClientWidth + 1);
    expect(overflow.triggerWidth).toBeLessThanOrEqual(overflow.viewportClientWidth);
  });

  test("keeps asset descriptions left of connections on both canvases", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "The lineage canvas is a desktop affordance.",
    );

    const description = "Customer profile records";
    const update = await page.request.put(
      `${liveApp.baseURL}/api/pipelines/${pipelineId}/assets/${customersAssetId}`,
      { data: { meta: { description } } },
    );
    expect(update.ok()).toBe(true);
    await pollAsset(
      liveApp,
      page.request,
      "analytics.customers",
      (asset) => asset.meta?.description === description,
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/canvas`);
    await expectCompactAssetDescription(page, description);

    await page.goto(`${liveApp.baseURL}/catalog?asset=${customersAssetId}`);
    await expectCompactAssetDescription(page, description);
  });

  test("guided cards render and adding a manual dependency persists provenance", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });

    const properties = await openAssetProperties(page);

    // The guided metadata panel renders its focused cards.
    await expect(properties.getByRole("heading", { name: "Identity" })).toBeVisible({
      timeout: 15000,
    });
    await expect(properties.getByRole("heading", { name: "Materialization" })).toBeVisible();
    await expect(properties.getByRole("heading", { name: "Dependencies" })).toBeVisible();
    await expect(properties.getByRole("heading", { name: "Columns" })).toBeVisible();

    const identity = properties.getByRole("heading", { name: "Identity" }).locator("../..");
    await expect(identity.getByRole("textbox", { name: "Type", exact: true })).toHaveValue(
      "duckdb.sql",
    );
    await expect(identity.getByRole("textbox", { name: "Type", exact: true })).toHaveAttribute(
      "readonly",
      "",
    );
    await identity.getByRole("combobox", { name: "Connection" }).click();
    await expect(
      page.getByRole("option", { name: /Pipeline default — duckdb-default/ }),
    ).toBeVisible();
    await page.keyboard.press("Escape");

    const descriptionInput = properties.getByPlaceholder("What this asset produces");
    await descriptionInput.fill("Customer profile records");
    const descriptionResponse = page.waitForResponse(
      (r) =>
        r.url().includes(`/api/pipelines/${pipelineId}/assets/${customersAssetId}`) &&
        r.request().method() === "PUT" &&
        r.ok(),
      { timeout: 15000 },
    );
    await descriptionInput.press("Enter");
    await descriptionResponse;
    const withDescription = await pollAsset(
      liveApp,
      page.request,
      "analytics.customers",
      (a) => a.meta?.description === "Customer profile records",
    );
    expect(withDescription.meta?.description).toBe("Customer profile records");

    const tagInput = properties.getByPlaceholder("Add tag");
    await tagInput.fill("finance, north");
    const tagResponse = page.waitForResponse(
      (r) =>
        r.url().includes(`/api/pipelines/${pipelineId}/assets/${customersAssetId}`) &&
        r.request().method() === "PUT" &&
        r.ok(),
      { timeout: 15000 },
    );
    await tagInput.press("Enter");
    await tagResponse;
    const withCommaTag = await pollAsset(liveApp, page.request, "analytics.customers", (a) =>
      (a.tags ?? []).includes("finance, north"),
    );
    expect(withCommaTag.tags).toContain("finance, north");

    // Add a manual dependency via the Dependencies card.
    const txResponse = page.waitForResponse(
      (r) => r.url().includes(`/api/assets/${customersAssetId}/transactions`) && r.ok(),
      { timeout: 15000 },
    );
    const input = properties.getByPlaceholder("Add dependency (asset name)");
    await input.fill("analytics.orders");
    await input.press("Enter");
    await txResponse;

    // It surfaces under Manual and is written to the asset's provenance.
    await expect(properties.getByText("Manual", { exact: true })).toBeVisible({ timeout: 15000 });
    await expect(properties.getByText("analytics.orders").first()).toBeVisible();

    const customers = await pollAsset(liveApp, page.request, "analytics.customers", (a) =>
      a.upstreams.includes("analytics.orders"),
    );
    expect(customers.meta?.renart_dep_add).toContain("a:analytics.orders#full");
  });

  test("merge metadata is editable through the guided form", async ({ liveApp, page }) => {
    const declareColumns = await page.request.put(
      `${liveApp.baseURL}/api/assets/${customersAssetId}/columns`,
      {
        data: {
          columns: [
            { name: "customer_id", type: "integer" },
            { name: "customer_name", type: "varchar" },
          ],
        },
      },
    );
    expect(declareColumns.ok()).toBe(true);

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });
    const properties = await openAssetProperties(page);
    const materialization = properties
      .getByRole("heading", { name: "Materialization" })
      .locator("../..");

    const strategyResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets/${customersAssetId}`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    await materialization.getByRole("combobox").click();
    await page.getByRole("option", { name: "Merge by key" }).click();
    await strategyResponse;
    await expect(
      materialization.getByText(/Merge needs at least one primary-key column/),
    ).toBeVisible({ timeout: 15000 });

    const invalidTypeCheck = await page.request.get(
      `${liveApp.baseURL}/api/pipelines/${pipelineId}/type-check`,
    );
    expect(invalidTypeCheck.ok()).toBe(true);
    const invalidReport = (await invalidTypeCheck.json()) as {
      assets: Array<{ name: string; findings: Array<{ message: string }> }>;
    };
    expect(
      invalidReport.assets
        .find((asset) => asset.name === "analytics.customers")
        ?.findings.some((finding) => finding.message.includes("primary-key")),
    ).toBe(true);

    const primaryKeyResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${customersAssetId}/columns`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    await properties.getByRole("button", { name: "Set customer_id as primary key" }).click();
    await primaryKeyResponse;

    const updateOnMergeResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${customersAssetId}/columns`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    await properties.getByRole("button", { name: "Update customer_name on merge" }).click();
    await updateOnMergeResponse;

    const mergeSQLResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${customersAssetId}/columns`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    const mergeSQL = properties.getByPlaceholder("merge SQL (optional)").nth(1);
    await mergeSQL.fill("COALESCE(source.customer_name, target.customer_name)");
    await mergeSQL.press("Enter");
    await mergeSQLResponse;

    const configured = await pollAsset(liveApp, page.request, "analytics.customers", (asset) => {
      const id = asset.columns?.find((column) => column.name === "customer_id");
      const name = asset.columns?.find((column) => column.name === "customer_name");
      return (
        asset.materialization_strategy === "merge" &&
        id?.primary_key === true &&
        name?.update_on_merge === true &&
        name.merge_sql === "COALESCE(source.customer_name, target.customer_name)"
      );
    });
    expect(configured.materialization_strategy).toBe("merge");

    await expect(properties.getByRole("button", { name: "YAML", exact: true })).toHaveCount(0);
    await expect(properties.getByRole("button", { name: "Form", exact: true })).toHaveCount(0);
    await expect(
      properties
        .getByRole("textbox", { name: "Name" })
        .locator('xpath=ancestor::*[@data-slot="field"]'),
    ).toHaveAttribute("data-orientation", "vertical");
    await expect(
      properties
        .getByRole("textbox", { name: "Type", exact: true })
        .locator('xpath=ancestor::*[@data-slot="field"]'),
    ).toHaveAttribute("data-orientation", "vertical");
    await expect(properties.getByRole("textbox", { name: "Type", exact: true })).toHaveAttribute(
      "readonly",
      "",
    );
    await expect(properties.getByRole("combobox", { name: "Type" })).toHaveCount(0);

    const unsetResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${customersAssetId}/columns`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    await properties.getByRole("button", { name: "Unset customer_id as primary key" }).click();
    await unsetResponse;
    await expect(
      properties.getByRole("button", { name: "Set customer_id as primary key" }),
    ).toBeVisible({ timeout: 15000 });

    const replacementKeyResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${customersAssetId}/columns`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    await properties.getByRole("button", { name: "Set customer_name as primary key" }).click();
    await replacementKeyResponse;

    const replacementConfigured = await pollAsset(
      liveApp,
      page.request,
      "analytics.customers",
      (asset) => {
        const keys = (asset.columns ?? [])
          .filter((column) => column.primary_key)
          .map((column) => column.name);
        return keys.length === 1 && keys[0] === "customer_name";
      },
    );
    expect(
      (replacementConfigured.columns ?? [])
        .filter((column) => column.primary_key)
        .map((column) => column.name),
    ).toEqual(["customer_name"]);
  });

  test("time-interval metadata defaults granularity from the selected key", async ({
    liveApp,
    page,
  }) => {
    const declareColumns = await page.request.put(
      `${liveApp.baseURL}/api/assets/${customersAssetId}/columns`,
      {
        data: {
          columns: [
            { name: "customer_id", type: "integer" },
            { name: "event_date", type: "date" },
          ],
        },
      },
    );
    expect(declareColumns.ok()).toBe(true);

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });
    const properties = await openAssetProperties(page);
    const materialization = properties
      .getByRole("heading", { name: "Materialization" })
      .locator("../..");

    const strategyResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets/${customersAssetId}`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    await materialization.getByRole("combobox").first().click();
    await page.getByRole("option", { name: "Incremental (time interval)" }).click();
    await strategyResponse;

    const keyResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets/${customersAssetId}`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    const incrementalKey = materialization.getByRole("combobox").nth(1);
    await incrementalKey.click();
    await page.getByPlaceholder("Search columns…").fill("event");
    await page.getByRole("option", { name: "event_date" }).click();
    await keyResponse;

    const configured = await pollAsset(
      liveApp,
      page.request,
      "analytics.customers",
      (asset) =>
        asset.materialization_strategy === "time_interval" &&
        asset.incremental_key === "event_date" &&
        asset.time_granularity === "date",
    );
    expect(configured.time_granularity).toBe("date");
    await expect(materialization.getByRole("combobox").nth(2)).toContainText("Date");

    await expect(properties.getByRole("button", { name: "YAML", exact: true })).toHaveCount(0);
  });

  test("load asset editors only offer Sling-compatible materializations", async ({
    liveApp,
    page,
  }) => {
    await writeFile(
      join(liveApp.workspaceDir, loadAssetPath),
      `name: analytics.orders_load
type: load
connection: duckdb-default
parameters:
  source_connection: duckdb-default
  source_table: analytics.orders
materialization:
  type: table
  strategy: create+replace
`,
      "utf8",
    );
    await pollAsset(liveApp, page.request, "analytics.orders_load", () => true);

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${loadAssetId}/code`);
    const properties = await openAssetProperties(page);
    const materialization = properties
      .getByRole("heading", { name: "Materialization" })
      .locator("../..");

    const expectSlingOptions = async () => {
      await expect(page.getByRole("option", { name: "Table (replace)" })).toBeVisible();
      await expect(page.getByRole("option", { name: "Table (truncate)" })).toBeVisible();
      await expect(page.getByRole("option", { name: "Append rows" })).toBeVisible();
      await expect(page.getByRole("option", { name: "Merge by key" })).toBeVisible();
      await expect(page.getByRole("option", { name: "None (run only)" })).toHaveCount(0);
      await expect(page.getByRole("option", { name: "View" })).toHaveCount(0);
      await expect(page.getByRole("option", { name: "Incremental (time interval)" })).toHaveCount(
        0,
      );
    };

    await materialization.getByRole("combobox").click();
    await expectSlingOptions();
    const mergeResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets/${loadAssetId}`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    await page.getByRole("option", { name: "Merge by key" }).click();
    await mergeResponse;

    const emptyUpdateKey = materialization.getByRole("combobox").nth(1);
    await emptyUpdateKey.click();
    await expect(page.getByText("No declared columns. Add or infer columns first.")).toBeVisible();
    await page.keyboard.press("Escape");

    const declareColumns = await page.request.put(
      `${liveApp.baseURL}/api/assets/${loadAssetId}/columns`,
      {
        data: {
          columns: [
            { name: "id", type: "integer" },
            { name: "updated_at", type: "timestamp" },
          ],
        },
      },
    );
    expect(declareColumns.ok()).toBe(true);
    await pollAsset(
      liveApp,
      page.request,
      "analytics.orders_load",
      (asset) => (asset.columns ?? []).length === 2,
    );
    await expect(properties.getByRole("button", { name: "Set id as primary key" })).toBeVisible({
      timeout: 15000,
    });

    const updateKeyResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets/${loadAssetId}`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    const updateKey = materialization.getByRole("combobox").nth(1);
    await updateKey.click();
    await page.getByPlaceholder("Search columns…").fill("updated");
    await page.getByRole("option", { name: "updated_at" }).click();
    await updateKeyResponse;
    const configured = await pollAsset(
      liveApp,
      page.request,
      "analytics.orders_load",
      (asset) =>
        asset.materialization_strategy === "merge" && asset.incremental_key === "updated_at",
    );
    expect(configured.incremental_key).toBe("updated_at");

    await expect(updateKey).toContainText("updated_at");
    await expect(properties.getByRole("button", { name: "YAML", exact: true })).toHaveCount(0);
  });

  test("creates a canonical Load asset, navigates to its source, and edits target connections", async ({
    liveApp,
    page,
  }) => {
    test.setTimeout(timeoutForRetry(test.info(), 90000, 60000));
    test.skip(
      test.info().project.name.includes("mobile"),
      "The canvas creation flow is desktop-only.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${ordersAssetId}/canvas`);
    await page.getByRole("button", { name: "New asset" }).first().click();
    const dialog = page.getByRole("dialog", { name: "New asset" });
    await expect(dialog).toBeVisible({ timeout: timeoutForRetry(test.info(), 15000) });
    await dialog.getByRole("radio", { name: /^Load/ }).click();
    await dialog.getByLabel("Asset name").fill("analytics.orders_copy");

    await dialog.getByLabel("Source connection").click();
    await page.getByRole("option", { name: "local", exact: true }).click();
    const sourceFilePicker = dialog.getByRole("button", { name: "Choose source file" });
    await expect(sourceFilePicker).toBeVisible();
    await sourceFilePicker.click();
    const sourcePathInput = page.getByPlaceholder("Type a path…");
    await expect(sourcePathInput).toBeVisible();
    await sourcePathInput.fill("data/orders.csv");
    await sourcePathInput.press("Enter");
    await expect(sourceFilePicker).toContainText("data/orders.csv");

    await dialog.getByLabel("Source connection").click();
    await page.getByRole("option", { name: "duckdb-default", exact: true }).click();
    await dialog.getByLabel("Source table or object").fill("analytics.orders");

    const createdResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets`) &&
        response.request().method() === "POST",
      { timeout: 15000 },
    );
    await dialog.getByRole("button", { name: "Create" }).click();
    expect((await createdResponse).ok()).toBe(true);

    const created = await pollAsset(
      liveApp,
      page.request,
      "analytics.orders_copy",
      (asset) =>
        asset.parameters?.source_connection === "duckdb-default" &&
        asset.parameters?.source_table === "analytics.orders",
    );
    expect(created.materialization_strategy).toBe("create+replace");
    expect(created.upstreams).toContain("analytics.orders");
    expect(created.parameters).not.toHaveProperty("destination_connection");
    expect(created.parameters).not.toHaveProperty("destination_table");
    expect(created.parameters).not.toHaveProperty("mode");
    expect(created.content).toContain("strategy: create+replace");
    expect(created.content).not.toContain("destination_table:");

    await page.getByRole("link", { name: "Code view" }).click();
    await expect(page.getByRole("button", { name: "Go to analytics.orders" })).toBeVisible({
      timeout: 15000,
    });
    await page.getByRole("button", { name: "Go to analytics.orders" }).click();
    await expect(page).toHaveURL(new RegExp(`/assets/${ordersAssetId}/`));

    const createdLoadId = Buffer.from("analytics/assets/analytics/orders_copy.asset.yml").toString(
      "base64url",
    );
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${createdLoadId}/code`);
    const loadProperties = await openAssetProperties(page);
    const loadIdentity = loadProperties.getByRole("heading", { name: "Identity" }).locator("../..");
    await loadIdentity
      .getByText("Connection", { exact: true })
      .locator("..")
      .getByRole("combobox")
      .click();
    await page.getByRole("option", { name: "duckdb-default", exact: true }).click();
    const loadWithExplicitTarget = await pollAsset(
      liveApp,
      page.request,
      "analytics.orders_copy",
      (asset) => asset.explicit_connection === "duckdb-default",
    );
    expect(loadWithExplicitTarget.connection).toBe("duckdb-default");

    const pythonCreate = await page.request.post(
      `${liveApp.baseURL}/api/pipelines/${pipelineId}/assets`,
      { data: { name: "analytics.python_target", type: "python" } },
    );
    expect(pythonCreate.ok()).toBe(true);
    const pythonAssetId = Buffer.from("analytics/assets/analytics/python_target.py").toString(
      "base64url",
    );
    await pollAsset(liveApp, page.request, "analytics.python_target", () => true);
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${pythonAssetId}/code`);
    const pythonProperties = await openAssetProperties(page);
    const pythonIdentity = pythonProperties
      .getByRole("heading", { name: "Identity" })
      .locator("../..");
    await pythonIdentity
      .getByText("Connection", { exact: true })
      .locator("..")
      .getByRole("combobox")
      .click();
    await page.getByRole("option", { name: "duckdb-default", exact: true }).click();
    const pythonWithExplicitTarget = await pollAsset(
      liveApp,
      page.request,
      "analytics.python_target",
      (asset) => asset.explicit_connection === "duckdb-default",
    );
    expect(pythonWithExplicitTarget.connection).toBe("duckdb-default");
  });

  test("materializing an asset on a stale upstream warns before building", async ({
    liveApp,
    page,
  }) => {
    const { request } = page;
    // An upstream that is never built (so it reads as stale) and a downstream
    // that selects from it.
    const upstream = await request.post(`${liveApp.baseURL}/api/pipelines/${pipelineId}/assets`, {
      data: {
        name: "analytics.warn_up",
        type: "duckdb.sql",
        content: `/* @bruin\ntype: duckdb.sql\nmaterialization:\n  type: table\n@bruin */\n\nselect 1 as x\n`,
      },
    });
    expect(upstream.ok()).toBe(true);
    const downstream = await request.post(`${liveApp.baseURL}/api/pipelines/${pipelineId}/assets`, {
      data: {
        name: "analytics.warn_down",
        type: "duckdb.sql",
        content: `/* @bruin\ntype: duckdb.sql\nmaterialization:\n  type: table\n@bruin */\n\nselect x from analytics.warn_up\n`,
      },
    });
    expect(downstream.ok()).toBe(true);
    await pollAsset(liveApp, request, "analytics.warn_down", (a) =>
      a.upstreams.includes("analytics.warn_up"),
    );

    const warnDownId = Buffer.from("analytics/assets/analytics/warn_down.sql").toString(
      "base64url",
    );
    const staleness = page.waitForResponse(
      (r) => r.url().includes(`/api/pipelines/${pipelineId}/staleness`) && r.ok(),
      { timeout: 15000 },
    );
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${warnDownId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });
    await staleness;

    // Materializing warns: the build would read the un-built upstream's table, so
    // the asset would stay stale (the §9 achieved-vs-target rule).
    await page.getByRole("button", { name: "Materialize" }).click();
    await expect(page.getByText("Upstream is out of date")).toBeVisible({ timeout: 15000 });
    await expect(page.getByText("analytics.warn_up").first()).toBeVisible();

    // The user can back out without building.
    await page.getByRole("button", { name: "Cancel" }).click();
    await expect(page.getByText("Upstream is out of date")).toBeHidden();
  });

  test("ignoring an inferred dependency persists as a drop and survives reconcile", async ({
    liveApp,
    request,
  }) => {
    // Create a downstream asset via the API so the SQL reconcile runs and infers
    // analytics.customers as an inferred (not manual) dependency.
    const create = await request.post(`${liveApp.baseURL}/api/pipelines/${pipelineId}/assets`, {
      data: {
        name: "analytics.downstream",
        type: "duckdb.sql",
        content: `/* @bruin
type: duckdb.sql
materialization:
  type: view
@bruin */

select customer_id from analytics.customers
`,
      },
    });
    expect(create.ok()).toBe(true);

    // Wait until the SQL reconcile has inferred the upstream.
    await pollAsset(liveApp, request, "analytics.downstream", (a) =>
      a.upstreams.includes("analytics.customers"),
    );

    // Ignore the inferred dependency.
    const ignore = await request.post(
      `${liveApp.baseURL}/api/assets/${downstreamAssetId}/transactions`,
      {
        data: { type: "dependency.inferred.ignore", dependency_key: "a:analytics.customers#full" },
      },
    );
    expect(ignore.ok()).toBe(true);

    const ignored = await pollAsset(
      liveApp,
      request,
      "analytics.downstream",
      (a) => !a.upstreams.includes("analytics.customers"),
    );
    expect(ignored.meta?.renart_dep_drop).toContain("a:analytics.customers#full");

    // Re-saving the SQL triggers a reconcile; the dropped dependency must not
    // reappear even though the query still references analytics.customers.
    const save = await request.put(
      `${liveApp.baseURL}/api/pipelines/${pipelineId}/assets/${downstreamAssetId}`,
      {
        data: { content: "select customer_id from analytics.customers -- edited\n" },
      },
    );
    expect(save.ok()).toBe(true);

    // Give the reconcile a beat, then assert it stayed dropped.
    const afterReconcile = await pollAsset(
      liveApp,
      request,
      "analytics.downstream",
      (a) => a.meta?.renart_dep_drop?.includes("a:analytics.customers#full") ?? false,
    );
    expect(afterReconcile.upstreams).not.toContain("analytics.customers");
  });

  test("columns are derived from the asset definition, not the warehouse", async ({
    liveApp,
    request,
  }) => {
    // Declare the upstream's columns (the source of truth for downstream types).
    const declare = await request.post(
      `${liveApp.baseURL}/api/assets/${customersAssetId}/columns/reconcile`,
      {
        data: {
          columns: [
            { name: "customer_id", type: "INTEGER" },
            { name: "customer_name", type: "VARCHAR" },
          ],
        },
      },
    );
    expect(declare.ok()).toBe(true);

    // A downstream asset selecting from the upstream plus a computed column.
    const create = await request.post(`${liveApp.baseURL}/api/pipelines/${pipelineId}/assets`, {
      data: {
        name: "analytics.report",
        type: "duckdb.sql",
        content: `/* @bruin
type: duckdb.sql
materialization:
  type: view
@bruin */

select customer_id, upper(customer_name) as shout from analytics.customers
`,
      },
    });
    expect(create.ok()).toBe(true);
    const reportAssetId = Buffer.from("analytics/assets/analytics/report.sql").toString(
      "base64url",
    );

    // Deriving the downstream's columns from its definition resolves the bare
    // column type from the upstream asset and types the computed column — all
    // without touching the database.
    const refresh = await request.post(
      `${liveApp.baseURL}/api/assets/${reportAssetId}/columns/refresh-from-definition`,
    );
    expect(refresh.ok()).toBe(true);
    const body = (await refresh.json()) as { columns: Array<{ name: string; type?: string }> };
    const byName = new Map(body.columns.map((c) => [c.name, (c.type ?? "").toUpperCase()]));
    expect(byName.get("customer_id")).toBe("INTEGER");
    expect(byName.get("shout")).toBe("VARCHAR");

    const rangeCreate = await request.post(
      `${liveApp.baseURL}/api/pipelines/${pipelineId}/assets`,
      {
        data: {
          name: "analytics.range_arithmetic",
          type: "duckdb.sql",
          content: `/* @bruin
type: duckdb.sql
materialization:
  type: table
@bruin */

select
  range,
  range * 2 as double_range
from range(1, 2, 1)
`,
        },
      },
    );
    expect(rangeCreate.ok()).toBe(true);
    const rangeAssetID = Buffer.from("analytics/assets/analytics/range_arithmetic.sql").toString(
      "base64url",
    );
    const rangeRefresh = await request.post(
      `${liveApp.baseURL}/api/assets/${rangeAssetID}/columns/refresh-from-definition`,
    );
    expect(rangeRefresh.ok(), await rangeRefresh.text()).toBe(true);
    const rangeBody = (await rangeRefresh.json()) as {
      columns: Array<{ name: string; type?: string }>;
    };
    const rangeByName = new Map(
      rangeBody.columns.map((column) => [column.name, column.type?.toUpperCase()]),
    );
    expect(rangeByName.get("range")).toBe("BIGINT");
    expect(rangeByName.get("double_range")).toBe("BIGINT");
  });

  test("guided columns can add a manual column", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });
    const properties = await openAssetProperties(page);
    const columnsCard = properties.getByRole("heading", { name: "Columns" }).locator("../..");

    await columnsCard.getByRole("textbox", { name: "Add column" }).fill("manual_note");
    const transactionResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/assets/${customersAssetId}/transactions`) &&
        response.request().method() === "POST" &&
        response.request().postDataJSON().type === "column.manual.add",
      { timeout: 15000 },
    );
    await columnsCard.getByRole("button", { name: "Add column" }).click();
    const transaction = await transactionResponse;
    expect(transaction.ok(), await transaction.text()).toBe(true);

    await expect(columnsCard.getByText("manual_note", { exact: true })).toBeVisible({
      timeout: 15000,
    });
    const customers = await pollAsset(
      liveApp,
      page.request,
      "analytics.customers",
      (asset) => asset.columns?.some((column) => column.name === "manual_note") === true,
    );
    expect(customers.columns?.find((column) => column.name === "manual_note")?.meta).toMatchObject({
      renart_manual: "true",
    });
  });

  test("schema sync applies safe changes and opens the resolver for known type changes", async ({
    liveApp,
    page,
  }) => {
    const declare = await page.request.put(
      `${liveApp.baseURL}/api/assets/${customersAssetId}/columns`,
      {
        data: { columns: [{ name: "customer_id", type: "BIGINT" }] },
      },
    );
    expect(declare.ok()).toBe(true);

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });
    const properties = await openAssetProperties(page);
    const columnsCard = properties.getByRole("heading", { name: "Columns" }).locator("../..");
    await expect(columnsCard.getByRole("checkbox", { name: "Current table" })).toBeVisible();

    const syncResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/assets/${customersAssetId}/columns/sync`) &&
        response.request().method() === "POST",
      { timeout: 30000 },
    );
    await columnsCard.getByRole("button", { name: "Sync schema", exact: true }).click();
    const sync = await syncResponse;
    expect(sync.ok(), await sync.text()).toBe(true);
    expect(((await sync.json()) as { status: string }).status).toBe("conflicts");

    const resolver = page.getByRole("dialog", { name: "Resolve schema differences" });
    await expect(resolver).toBeVisible();
    await expect(resolver.getByRole("columnheader", { name: /SQL query/ })).toBeVisible();
    await expect(resolver.getByRole("columnheader", { name: "Saved metadata" })).toBeVisible();

    const customerIDRow = resolver.getByRole("row").filter({ hasText: "customer_id" });
    await expect(customerIDRow).toContainText("INTEGER");
    await expect(customerIDRow).toContainText("BIGINT");
    await expect(customerIDRow.getByRole("combobox")).toContainText("Use SQL query");
    await customerIDRow.getByRole("combobox").click();
    await page.getByRole("option", { name: "Use SQL query · customer_id: INTEGER" }).click();

    const applyResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/assets/${customersAssetId}/columns/sync/apply`) &&
        response.request().method() === "POST",
      { timeout: 30000 },
    );
    await resolver.getByRole("button", { name: "Apply resolution" }).click();
    const applied = await applyResponse;
    expect(applied.ok(), await applied.text()).toBe(true);
    await expect(resolver).toBeHidden();

    const customers = await pollAsset(
      liveApp,
      page.request,
      "analytics.customers",
      (asset) =>
        asset.columns?.some(
          (column) => column.name === "customer_id" && column.type?.toUpperCase() === "INTEGER",
        ) === true && asset.columns?.some((column) => column.name === "customer_name") === true,
    );
    expect(customers.meta?.renart_col_own ?? "").not.toContain("customer_id:type");
  });

  test("column type ownership is preserved across reconciliation", async ({ liveApp, request }) => {
    // Reconcile customers' columns from an inferred set (no declared columns yet).
    const first = await request.post(
      `${liveApp.baseURL}/api/assets/${customersAssetId}/columns/reconcile`,
      {
        data: { columns: [{ name: "customer_id", type: "integer" }] },
      },
    );
    expect(first.ok()).toBe(true);

    // Take ownership of the column's type.
    const own = await request.post(
      `${liveApp.baseURL}/api/assets/${customersAssetId}/transactions`,
      {
        data: { type: "column.field.own", column: "customer_id", field: "type" },
      },
    );
    expect(own.ok()).toBe(true);

    // A later inference saying bigint must not override the owned integer type.
    const second = await request.post(
      `${liveApp.baseURL}/api/assets/${customersAssetId}/columns/reconcile`,
      {
        data: { columns: [{ name: "customer_id", type: "bigint" }] },
      },
    );
    expect(second.ok()).toBe(true);
    const body = (await second.json()) as { columns: Array<{ name: string; type?: string }> };
    const customerId = body.columns.find((c) => c.name === "customer_id");
    expect(customerId?.type).toBe("integer");
  });

  test("guided metadata form is the only inspector mode and edits a tag", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });
    const properties = await openAssetProperties(page);

    // The guided form is the only exposed metadata surface for now.
    await expect(properties.getByRole("heading", { name: "Identity" })).toBeVisible({
      timeout: 15000,
    });
    await expect(properties.getByRole("button", { name: "YAML", exact: true })).toHaveCount(0);
    await expect(properties.getByRole("button", { name: "Form", exact: true })).toHaveCount(0);

    // Adding a tag through the guided field persists it.
    const input = properties.getByRole("textbox", { name: "Tags" });
    await input.fill("daily");
    await input.press("Enter");

    const customers = await pollAsset(liveApp, page.request, "analytics.customers", (a) =>
      (a.tags ?? []).includes("daily"),
    );
    expect(customers.tags).toContain("daily");

    // Removing the last tag must clear it from the live view, not only after a
    // refresh (the workspace SSE merge omits empty fields).
    await properties.getByRole("button", { name: "Remove daily" }).click();
    await expect(properties.getByRole("button", { name: "Remove daily" })).toBeHidden({
      timeout: 15000,
    });
    const cleared = await pollAsset(
      liveApp,
      page.request,
      "analytics.customers",
      (a) => (a.tags ?? []).length === 0,
    );
    expect(cleared.tags ?? []).not.toContain("daily");
  });

  test("quality checks card adds and removes a column check", async ({ liveApp, page }) => {
    const { request } = page;
    // Declare a column so the checks card has a target.
    const declare = await request.post(
      `${liveApp.baseURL}/api/assets/${customersAssetId}/columns/reconcile`,
      {
        data: { columns: [{ name: "customer_id", type: "INTEGER" }] },
      },
    );
    expect(declare.ok()).toBe(true);

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });
    const properties = await openAssetProperties(page);

    const card = properties.locator("section").filter({ hasText: "Quality checks" });
    await expect(card.getByRole("heading", { name: "Quality checks" })).toBeVisible({
      timeout: 15000,
    });

    // Pick the column (the check name defaults to not_null) and add the check.
    await card.getByRole("combobox").first().click();
    await page.getByRole("option", { name: "customer_id" }).click();
    const added = page.waitForResponse(
      (r) => r.url().includes(`/api/assets/${customersAssetId}/transactions`) && r.ok(),
      { timeout: 15000 },
    );
    await card.getByRole("button", { name: "Add check" }).click();
    await added;

    const removeButton = card.getByRole("button", { name: "Remove not_null from customer_id" });
    await expect(removeButton).toBeVisible({ timeout: 15000 });
    const removed = page.waitForResponse(
      (r) => r.url().includes(`/api/assets/${customersAssetId}/transactions`) && r.ok(),
      { timeout: 15000 },
    );
    await removeButton.click();
    await removed;
    await expect(card.getByRole("button", { name: "Remove not_null from customer_id" })).toBeHidden(
      { timeout: 15000 },
    );
  });

  test("SQL hooks are authored in focused pre and post editors", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });
    const properties = await openAssetProperties(page);
    const hooks = properties.getByTestId("asset-hooks");
    await expect(properties.getByRole("heading", { name: "SQL hooks" })).toBeVisible();

    const addHook = async (phase: "pre" | "post", query: string) => {
      const label = phase === "pre" ? "Before materialization" : "After materialization";
      const phaseSection = hooks.getByText(label, { exact: true }).locator("..");
      const diagnostics = page.waitForResponse(
        (response) =>
          response.url().includes("/api/sql/lsp/diagnostics") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON()?.document_context === "hook",
        { timeout: 15000 },
      );
      await phaseSection.getByRole("button", { name: "Add", exact: true }).click();
      const dialog = page.getByRole("dialog", { name: `Add ${phase}-hook` });
      await expect(dialog.locator(".monaco-editor")).toBeVisible({ timeout: 15000 });
      expect((await diagnostics).ok()).toBe(true);
      await page.evaluate(
        ({ phase, query }) => {
          const monaco = (window as typeof window & { monaco?: any }).monaco;
          const model = monaco?.editor
            .getModels?.()
            .find(
              (candidate: any) =>
                candidate.uri?.toString?.().includes(`/hooks/`) &&
                candidate.uri?.toString?.().includes(`/${phase}/`),
            );
          if (!model) throw new Error(`${phase}-hook Monaco model is not ready`);
          model.setValue(query);
        },
        { phase, query },
      );
      const saved = page.waitForResponse(
        (response) =>
          response.url().includes(`/api/assets/${customersAssetId}/transactions`) && response.ok(),
        { timeout: 15000 },
      );
      await dialog.getByRole("button", { name: "Save hook" }).click();
      await saved;
    };

    await addHook("pre", "create table if not exists hook_audit(id bigint)");
    await addHook("post", "insert into hook_audit values (1)");

    const asset = await pollAsset(
      liveApp,
      page.request,
      "analytics.customers",
      (candidate) =>
        candidate.pre_hooks?.[0] === "create table if not exists hook_audit(id bigint)" &&
        candidate.post_hooks?.[0] === "insert into hook_audit values (1)",
    );
    expect(asset.pre_hooks).toEqual(["create table if not exists hook_audit(id bigint)"]);
    expect(asset.post_hooks).toEqual(["insert into hook_audit values (1)"]);
    await expect(hooks.getByText("insert into hook_audit values (1)")).toBeVisible({
      timeout: 15000,
    });
  });

  test("quality checks card authors a custom SQL check", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15000 });
    const properties = await openAssetProperties(page);
    const customChecks = properties.getByTestId("asset-custom-checks");

    const initialDiagnostics = page.waitForResponse(
      (response) =>
        response.url().includes("/api/sql/lsp/diagnostics") &&
        response.request().method() === "POST" &&
        response.request().postDataJSON()?.document_context === "custom_check",
      { timeout: 15000 },
    );
    await customChecks.getByRole("button", { name: "Add", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Add custom check" });
    await dialog.getByLabel("Name").fill("no invalid customers");
    await dialog.getByLabel("Description").fill("Customer identifiers stay positive");
    await expect(dialog.locator(".monaco-editor")).toBeVisible({ timeout: 15000 });
    expect((await initialDiagnostics).ok()).toBe(true);

    const completionQuery = "select c.\nfrom analytics.customers c";
    const completionResponse = await page.request.post(
      `${liveApp.baseURL}/api/sql/lsp/completions`,
      {
        data: {
          asset_id: customersAssetId,
          content: completionQuery,
          document_context: "custom_check",
          position: { line: 0, character: "select c.".length },
        },
      },
    );
    expect(completionResponse.ok()).toBe(true);
    const completionPayload = (await completionResponse.json()) as {
      status?: string;
      completions?: Array<{ label?: string }>;
    };
    expect(completionPayload.status).toBe("ok");
    expect(completionPayload.completions?.map((completion) => completion.label)).toEqual(
      expect.arrayContaining(["customer_id", "customer_name"]),
    );

    if (!test.info().project.name.includes("mobile")) {
      const editorCompletion = page.waitForResponse(
        (response) =>
          response.url().includes("/api/sql/lsp/completions") &&
          response.request().method() === "POST" &&
          response.request().postDataJSON()?.document_context === "custom_check",
        { timeout: 15000 },
      );
      await page.evaluate((query) => {
        const monaco = (window as typeof window & { monaco?: any }).monaco;
        const model = monaco?.editor
          .getModels?.()
          .find((candidate: any) => candidate.uri?.toString?.().includes("/custom-check/"));
        const editor = monaco?.editor
          .getEditors?.()
          .find((candidate: any) => candidate.getModel?.() === model);
        if (!model || !editor) throw new Error("Custom check Monaco editor is not ready");
        editor.setValue(query);
        editor.setPosition(model.getPositionAt("select c.".length));
        editor.focus();
        editor.trigger("test", "editor.action.triggerSuggest", {});
      }, completionQuery);
      expect((await editorCompletion).ok()).toBe(true);
      await expect(
        page
          .locator(".suggest-widget.visible")
          .first()
          .locator(".monaco-list-row")
          .filter({ hasText: "customer_id" })
          .first(),
      ).toBeVisible({ timeout: 15000 });
    }

    await page.evaluate(() => {
      const monaco = (window as typeof window & { monaco?: any }).monaco;
      const model = monaco?.editor
        .getModels?.()
        .find((candidate: any) => candidate.uri?.toString?.().includes("/custom-check/"));
      if (!model) throw new Error("Custom check Monaco model is not ready");
      model.setValue("select * from analytics.customers where customer_id <= 0");
    });

    const saved = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${customersAssetId}/transactions`) && response.ok(),
      { timeout: 15000 },
    );
    await dialog.getByRole("button", { name: "Save check" }).click();
    await saved;

    const asset = await pollAsset(liveApp, page.request, "analytics.customers", (candidate) =>
      Boolean(candidate.custom_checks?.some((check) => check.name === "no invalid customers")),
    );
    expect(asset.custom_checks?.[0]).toMatchObject({
      name: "no invalid customers",
      count: 0,
      query: "select * from analytics.customers where customer_id <= 0",
    });
    expect(asset.custom_checks?.[0]?.blocking ?? true).toBe(true);
    await expect(customChecks.getByText("no invalid customers", { exact: true })).toBeVisible({
      timeout: 15000,
    });

    const removed = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${customersAssetId}/transactions`) && response.ok(),
      { timeout: 15000 },
    );
    await customChecks
      .getByRole("button", {
        name: "Remove custom check no invalid customers",
      })
      .click();
    await removed;
    await pollAsset(
      liveApp,
      page.request,
      "analytics.customers",
      (candidate) => (candidate.custom_checks ?? []).length === 0,
    );
  });
});
