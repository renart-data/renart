import { expect } from "@playwright/test";
import { readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { liveTest as test, type LiveApp } from "../live-app-fixture";

const pipelineId = Buffer.from("analytics").toString("base64url");

type TypeCheckFinding = {
  severity: string;
  message: string;
  line?: number;
  resolutions?: Array<{
    id: string;
    title: string;
    transaction?: { type: string; column?: string };
    action?: { type: "import-external-relation"; relation_id: string };
  }>;
};
type TypeCheckAsset = {
  id?: string;
  name: string;
  type: string;
  status: string;
  findings: TypeCheckFinding[];
};
type TypeCheckReport = {
  status: string;
  pipeline_name: string;
  assets: TypeCheckAsset[];
  external_relations?: Array<{
    id: string;
    qualified_name: string;
    referenced_by_asset_names: string[];
  }>;
  summary: { assets: number; errors: number; warnings: number };
};

async function seedTypeCheckAssets(liveApp: LiveApp) {
  const assetsDir = join(liveApp.workspaceDir, "analytics", "assets", "analytics");
  await writeFile(
    join(assetsDir, "customer_seed.csv"),
    "customer_id,customer_name\n1,Ada\n",
    "utf8",
  );
  await writeFile(
    join(assetsDir, "customer_seed.asset.yml"),
    `name: analytics.customer_seed
type: duckdb.seed
parameters:
  path: ./customer_seed.csv
columns:
  - name: customer_id
    type: integer
  - name: customer_name
    type: varchar
`,
    "utf8",
  );
  // A table-producing Python asset with no declared columns -> error.
  await writeFile(
    join(assetsDir, "py_metric.py"),
    `""" @bruin
name: analytics.py_metric
type: python
materialization:
  type: table
@bruin """

print("hello")
`,
    "utf8",
  );
  // A SQL asset that selects a column the known upstream does not have -> error.
  await writeFile(
    join(assetsDir, "bad_downstream.sql"),
    `/* @bruin
name: analytics.bad_downstream
type: duckdb.sql
materialization:
  type: view
depends:
  - analytics.customers
  - analytics.missing
@bruin */

select nonexistent_col from analytics.customers
`,
    "utf8",
  );
  await writeFile(
    join(assetsDir, "postgres_orders.sql"),
    `/* @bruin
name: analytics.postgres_orders
type: pg.sql
connection: postgres-default
materialization:
  type: table
columns:
  - name: order_id
    type: bigint
@bruin */

select 1 as order_id
`,
    "utf8",
  );
  await writeFile(
    join(assetsDir, "cross_connection.sql"),
    `/* @bruin
name: analytics.cross_connection
type: duckdb.sql
connection: duckdb-default
materialization:
  type: view
depends:
  - analytics.postgres_orders
@bruin */

select order_id from analytics.postgres_orders
`,
    "utf8",
  );
}

async function pollTypeCheck(
  liveApp: LiveApp,
  request: { get: (url: string) => Promise<{ ok(): boolean; json(): Promise<unknown> }> },
): Promise<TypeCheckReport> {
  let report: TypeCheckReport | null = null;
  await expect
    .poll(
      async () => {
        const response = await request.get(
          `${liveApp.baseURL}/api/pipelines/${pipelineId}/type-check`,
        );
        if (!response.ok()) {
          return "";
        }
        report = (await response.json()) as TypeCheckReport;
        return report.assets
          .map((asset) => asset.name)
          .sort()
          .join(",");
      },
      { timeout: 30000 },
    )
    .toContain("analytics.bad_downstream");
  if (!report) {
    throw new Error("type-check report never resolved");
  }
  return report;
}

test.describe("app pipeline type check live", () => {
  test.use({ fixtureName: "configured-workspace" });

  test("type-check endpoint reports dependency, column, and declaration findings", async ({
    liveApp,
    request,
  }) => {
    await seedTypeCheckAssets(liveApp);
    const report = await pollTypeCheck(liveApp, request);

    const byName = new Map(report.assets.map((asset) => [asset.name, asset]));

    // Undeclared table-producing Python asset -> missing-contract error.
    const py = byName.get("analytics.py_metric");
    expect(py?.status).toBe("error");
    expect(
      py?.findings.some(
        (f) => f.severity === "error" && /Output schema cannot be inferred/i.test(f.message),
      ),
    ).toBe(true);

    // Downstream selecting a non-existent column of a known upstream -> error.
    const bad = byName.get("analytics.bad_downstream");
    expect(bad?.status).toBe("error");
    expect(
      bad?.findings.some((f) => f.severity === "error" && /Unresolved column/i.test(f.message)),
    ).toBe(true);
    const missingDependency = bad?.findings.some(
      (f) =>
        f.severity === "error" && /Dependency 'analytics\.missing' does not exist/.test(f.message),
    );
    expect(missingDependency, JSON.stringify(bad?.findings)).toBe(true);

    const crossConnection = byName.get("analytics.cross_connection");
    expect(crossConnection?.status).toBe("warning");
    expect(
      crossConnection?.findings.some(
        (finding) =>
          finding.severity === "warning" && /Cross-connection reference/.test(finding.message),
      ),
    ).toBe(true);

    // A clean upstream asset reports no findings.
    const customers = byName.get("analytics.customers");
    expect(customers?.status).toBe("ok");
    expect(customers?.findings).toEqual([]);

    // Seed writes are owned by the dedicated Sling runtime, not the generic
    // materialization capability profile.
    const seed = byName.get("analytics.customer_seed");
    expect(seed?.status).toBe("ok");
    expect(seed?.findings).toEqual([]);

    expect(report.summary.errors).toBeGreaterThanOrEqual(1);
    expect(report.summary.warnings).toBeGreaterThanOrEqual(1);
    expect(report.status).toBe("error");
  });

  test("type-check results mark failing nodes and remain available in the bottom panel", async ({
    liveApp,
    page,
  }) => {
    await seedTypeCheckAssets(liveApp);
    // Make sure the server can see the new assets before we open the page.
    await pollTypeCheck(liveApp, page.request);

    const customersAssetId = Buffer.from("analytics/assets/analytics/customers.sql").toString(
      "base64url",
    );

    const typeCheckResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/type-check`) && response.ok(),
      { timeout: 30000 },
    );
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/canvas`);
    await typeCheckResponse;

    const badAssetId = Buffer.from("analytics/assets/analytics/bad_downstream.sql").toString(
      "base64url",
    );
    const badNode = page.locator(`[data-testid="lineage-asset"][data-asset-id="${badAssetId}"]`);
    await expect(badNode.getByTestId("asset-type-check-error")).toBeVisible({ timeout: 15000 });

    const zoomOut = page.locator(".react-flow__controls-zoomout").first();
    for (let step = 0; step < 5; step += 1) {
      await zoomOut.click();
    }
    await expect
      .poll(() =>
        page
          .locator(".react-flow__viewport")
          .first()
          .evaluate((element) => {
            return new DOMMatrix(getComputedStyle(element).transform).a;
          }),
      )
      .toBeLessThan(0.5);
    await expect(badNode.getByTestId("lineage-asset-overview")).toBeVisible();

    await page.getByRole("tab", { name: /Type check/ }).click();
    await expect(page.getByTestId("type-check-scroll-area")).toBeVisible();

    await expect(page.getByText("analytics.bad_downstream").first()).toBeVisible({
      timeout: 15000,
    });
    await expect(page.getByText(/Unresolved column/i).first()).toBeVisible({ timeout: 15000 });
    await expect(page.getByText(/Output schema cannot be inferred/i).first()).toBeVisible({
      timeout: 15000,
    });
  });

  test("offers and applies a safe resolution for inactive merge metadata", async ({
    liveApp,
    page,
  }) => {
    await seedTypeCheckAssets(liveApp);
    const assetsDir = join(liveApp.workspaceDir, "analytics", "assets", "analytics");
    const assetPath = join(assetsDir, "inactive_merge.sql");
    const assetId = Buffer.from("analytics/assets/analytics/inactive_merge.sql").toString(
      "base64url",
    );
    await writeFile(
      assetPath,
      `/* @bruin
name: analytics.inactive_merge
type: duckdb.sql
materialization:
  type: table
  strategy: create+replace
columns:
  - name: id
    type: bigint
    update_on_merge: true
    merge_sql: source.id
@bruin */

select 1::bigint as id
`,
      "utf8",
    );
    await pollTypeCheck(liveApp, page.request);

    const typeCheckResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/type-check`) && response.ok(),
      { timeout: 30000 },
    );
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${assetId}/canvas`);
    await typeCheckResponse;
    await page.getByRole("tab", { name: /Type check/ }).click();

    const assetSection = page
      .getByText("analytics.inactive_merge", { exact: true })
      .locator("../..");
    const resolution = assetSection.getByRole("button", {
      name: "Delete inactive merge settings",
    });
    await expect(resolution).toBeVisible({ timeout: 15000 });

    const transaction = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${assetId}/transactions`) &&
        response.request().method() === "POST",
      { timeout: 15000 },
    );
    await resolution.click();
    await page.getByRole("button", { name: "Delete", exact: true }).click();
    const transactionResponse = await transaction;
    expect(transactionResponse.ok()).toBe(true);
    expect(transactionResponse.request().postDataJSON()).toEqual({
      type: "column.merge_settings.clear",
      column: "id",
    });

    await expect
      .poll(async () => {
        const report = await pollTypeCheck(liveApp, page.request);
        return (
          report.assets
            .find((asset) => asset.name === "analytics.inactive_merge")
            ?.findings.some((finding) =>
              /Inactive materialization metadata/.test(finding.message),
            ) ?? false
        );
      })
      .toBe(false);
  });

  test("shows a positively observed external table and imports it after review", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "The desktop run covers the canvas node and reviewed import lifecycle.",
    );

    const assetsDir = join(liveApp.workspaceDir, "analytics", "assets", "analytics");
    const producerPath = join(assetsDir, "external_orders.sql");
    const producerAssetId = Buffer.from("analytics/assets/analytics/external_orders.sql").toString(
      "base64url",
    );
    await writeFile(
      producerPath,
      `/* @bruin
name: main.external_orders
type: duckdb.sql
connection: duckdb-default
materialization:
  type: table
columns:
  - name: order_id
    type: bigint
  - name: customer_name
    type: varchar
@bruin */

select 1::bigint as order_id, 'Ada'::varchar as customer_name
`,
      "utf8",
    );
    await expect
      .poll(async () => {
        const response = await page.request.get(`${liveApp.baseURL}/api/workspace`);
        const workspace = (await response.json()) as {
          pipelines: Array<{ assets: Array<{ id: string }> }>;
        };
        return workspace.pipelines.some((pipeline) =>
          pipeline.assets.some((asset) => asset.id === producerAssetId),
        );
      })
      .toBe(true);

    const materialize = await page.request.post(
      `${liveApp.baseURL}/api/assets/${producerAssetId}/materialize/stream?environment=default`,
    );
    const materializeStream = await materialize.text();
    expect(materialize.ok(), materializeStream).toBe(true);
    const doneLine = materializeStream
      .split(/\r?\n/)
      .reverse()
      .find((line) => line.startsWith("data: ") && line.includes('"status"'));
    expect(doneLine, materializeStream).toBeTruthy();
    expect(JSON.parse(doneLine!.slice("data: ".length)).status).toBe("ok");

    await rm(producerPath);
    const consumerPath = join(assetsDir, "external_report.sql");
    const consumerAssetId = Buffer.from("analytics/assets/analytics/external_report.sql").toString(
      "base64url",
    );
    const consumerSQL = `/* @bruin
name: analytics.external_report
type: duckdb.sql
connection: duckdb-default
materialization:
  type: view
columns:
  - name: order_id
    type: bigint
@bruin */

select order_id from main.external_orders
`;
    await writeFile(consumerPath, consumerSQL, "utf8");
    await expect
      .poll(
        async () => {
          const response = await page.request.get(`${liveApp.baseURL}/api/workspace`);
          const workspace = (await response.json()) as {
            pipelines: Array<{ assets: Array<{ id: string }> }>;
          };
          const assetIds = workspace.pipelines.flatMap((pipeline) =>
            pipeline.assets.map((asset) => asset.id),
          );
          return !assetIds.includes(producerAssetId) && assetIds.includes(consumerAssetId);
        },
        { timeout: 30000 },
      )
      .toBe(true);

    await expect
      .poll(
        async () => {
          const response = await page.request.post(`${liveApp.baseURL}/api/sql/lsp/diagnostics`, {
            data: {
              asset_id: consumerAssetId,
              content: consumerSQL,
              environment: "default",
            },
          });
          if (!response.ok()) return false;
          const body = (await response.json()) as {
            diagnostics?: Array<{ code?: string; severity?: number }>;
          };
          return Boolean(
            body.diagnostics?.some(
              (diagnostic) => diagnostic.code === "external-relation" && diagnostic.severity === 2,
            ),
          );
        },
        { timeout: 30000 },
      )
      .toBe(true);

    let externalRelationId = "";
    await expect
      .poll(async () => {
        const response = await page.request.get(
          `${liveApp.baseURL}/api/pipelines/${pipelineId}/type-check`,
        );
        if (!response.ok()) return "";
        const report = (await response.json()) as TypeCheckReport;
        const relation = report.external_relations?.find((candidate) =>
          candidate.referenced_by_asset_names.includes("analytics.external_report"),
        );
        externalRelationId = relation?.id ?? "";
        return relation?.qualified_name ?? "";
      })
      .toMatch(/external_orders$/);

    const initialTypeCheck = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/type-check`) && response.ok(),
      { timeout: 30000 },
    );
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${consumerAssetId}/canvas`);
    await initialTypeCheck;

    const externalNode = page.locator(
      `[data-testid="lineage-asset"][data-asset-id="${externalRelationId}"]`,
    );
    await expect(externalNode.locator('[data-external="true"]')).toBeVisible({ timeout: 15000 });
    await externalNode.getByRole("button", { name: "Asset actions" }).click();
    await page.getByRole("menuitem", { name: "Import as asset" }).click();

    const importDialog = page.getByRole("dialog", { name: "Import external relation" });
    await expect(importDialog).toBeVisible();
    await expect(
      importDialog.getByRole("checkbox", { name: "Import observed columns" }),
    ).toBeChecked();
    await expect(importDialog.getByText("order_id", { exact: true })).toBeVisible();
    await expect(importDialog.getByText("customer_name", { exact: true })).toBeVisible();
    const importResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/pipelines/${pipelineId}/external-relations/import`) &&
        response.request().method() === "POST",
      { timeout: 30000 },
    );
    await importDialog.getByRole("button", { name: "Import asset" }).click();
    expect((await importResponse).status()).toBe(201);

    const importedPath = join(
      liveApp.workspaceDir,
      "analytics",
      "assets",
      "main",
      "external_orders.asset.yml",
    );
    await expect
      .poll(async () => {
        try {
          return await readFile(importedPath, "utf8");
        } catch {
          return "";
        }
      })
      .toContain("name: order_id");
    await expect(externalNode).toHaveCount(0, { timeout: 30000 });
  });
});
