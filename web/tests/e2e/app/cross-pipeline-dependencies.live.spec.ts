import { expect, type APIRequestContext, type Page } from "@playwright/test";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { liveTest as test, type LiveApp } from "../live-app-fixture";

const producerPipelineUUID = "11111111-1111-4111-8111-111111111111";
const consumerPipelineUUID = "22222222-2222-4222-8222-222222222222";
const producerPipelineID = Buffer.from("cross-producer").toString("base64url");
const consumerPipelineID = Buffer.from("cross-consumer").toString("base64url");

type EnvScheduleResponse = {
  schedules: Array<{
    pipeline_uuid: string;
    environment: string;
    deferred_occurrence?: {
      status: string;
      prerequisite_reason?: string;
    };
    last_run?: { id: string; status: string; trigger: string };
  }>;
};

test.describe("cross-pipeline dependencies live", () => {
  test.use({ fixtureName: "basic-workspace" });

  test("reviews an undeclared SQL relation before adding the URI dependency", async ({
    liveApp,
    page,
  }) => {
    test.setTimeout(120_000);
    test.skip(
      test.info().project.name.includes("mobile"),
      "The canvas dependency review is covered in the desktop project.",
    );

    await writeCrossPipelineWorkspace(liveApp, { includeDependency: false });
    await waitForCrossPipelineWorkspace(liveApp, page.request);
    const consumerAssetID = Buffer.from("cross-consumer/assets/analytics/orders.sql").toString(
      "base64url",
    );
    const producerAssetID = Buffer.from("cross-producer/assets/raw/orders.sql").toString(
      "base64url",
    );
    await materializeAsset(liveApp, page.request, producerAssetID);
    const producerPath = join(
      liveApp.workspaceDir,
      "cross-producer",
      "assets",
      "raw",
      "orders.sql",
    );
    await writeFile(
      producerPath,
      (await readFile(producerPath, "utf8")).replace("select 1::bigint", "select 2::bigint"),
      "utf8",
    );
    await expect
      .poll(
        async () => {
          const response = await page.request.get(
            `${liveApp.baseURL}/api/pipelines/${producerPipelineID}/staleness?environment=default`,
          );
          if (!response.ok()) return null;
          const body = (await response.json()) as {
            assets: Array<{
              asset_name: string;
              status: string;
              latest_output?: { materialized_at?: string };
            }>;
          };
          const producer = body.assets.find((asset) => asset.asset_name === "raw.orders");
          return producer
            ? {
                status: producer.status,
                hasLatestOutput: Boolean(producer.latest_output?.materialized_at),
              }
            : null;
        },
        { timeout: 30_000 },
      )
      .toEqual({ status: "stale_edited", hasLatestOutput: true });
    const typeCheckResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${consumerPipelineID}/type-check`) && response.ok(),
      { timeout: 30_000 },
    );

    await page.goto(
      `${liveApp.baseURL}/pipelines/${consumerPipelineID}/assets/${consumerAssetID}/canvas`,
    );
    const report = (await (await typeCheckResponse).json()) as {
      assets: Array<{
        name: string;
        findings: Array<{ code: string; resolutions?: Array<{ title: string }> }>;
      }>;
      cross_pipeline_references?: Array<{
        producer_asset_id: string;
        consumer_asset_id: string;
        status: string;
      }>;
    };
    expect(
      report.assets
        .find((asset) => asset.name === "analytics.orders")
        ?.findings.some((finding) => finding.code === "undeclared-cross-pipeline-dependency"),
    ).toBe(true);
    expect(report.cross_pipeline_references).toEqual([
      expect.objectContaining({
        producer_asset_id: producerAssetID,
        consumer_asset_id: consumerAssetID,
        status: "declarable",
      }),
    ]);

    const producerNode = page.locator(
      `[data-testid="lineage-asset"][data-asset-id="${producerAssetID}"]`,
    );
    await expect(producerNode).toBeVisible({ timeout: 15_000 });
    await expect(producerNode.locator('[title="Staleness: Edited"]')).toBeVisible({
      timeout: 15_000,
    });
    await expect(producerNode.getByText("Running", { exact: true })).toHaveCount(0);
    await expect(producerNode.locator('[title^="Last built:"]')).toHaveCount(1);
    await expect(producerNode.locator('[title*="date unknown"]')).toHaveCount(0);
    await expect(page.locator(".react-flow__edge.asset-edge-provisional")).toHaveCount(1);

    await page.getByRole("tab", { name: /Type check/ }).click();
    const transactionRequest = page.waitForRequest(
      (request) =>
        request.method() === "POST" &&
        request.url().includes(`/api/assets/${consumerAssetID}/transactions`),
    );
    await page.getByRole("button", { name: "Add full dependency", exact: true }).click();
    expect((await transactionRequest).postDataJSON()).toEqual({
      type: "dependency.manual.add",
      dependency: { uri: "duckdb://warehouse/raw/orders", mode: "full" },
    });

    await expect
      .poll(async () => {
        const response = await page.request.get(`${liveApp.baseURL}/api/workspace`);
        if (!response.ok()) return null;
        const workspace = (await response.json()) as {
          pipelines: Array<{
            id: string;
            assets: Array<{
              id: string;
              dependencies?: Array<{
                value: string;
                resolved_asset_id?: string;
                mode: string;
              }>;
            }>;
          }>;
        };
        return workspace.pipelines
          .find((pipeline) => pipeline.id === consumerPipelineID)
          ?.assets.find((asset) => asset.id === consumerAssetID)?.dependencies?.[0];
      })
      .toEqual(
        expect.objectContaining({
          value: "duckdb://warehouse/raw/orders",
          resolved_asset_id: producerAssetID,
          mode: "full",
        }),
      );
    await expect(page.locator(".react-flow__edge.asset-edge-provisional")).toHaveCount(0, {
      timeout: 15_000,
    });
    await expect(page.locator(".react-flow__edge.asset-edge")).toHaveCount(1);
    await expect(producerNode.locator('[title="Staleness: Edited"]')).toBeVisible();
    await expect(producerNode.getByText("Running", { exact: true })).toHaveCount(0);
    await expect(producerNode.locator('[title^="Last built:"]')).toHaveCount(1);
    expect(
      await readFile(
        join(liveApp.workspaceDir, "cross-consumer", "assets", "analytics", "orders.sql"),
        "utf8",
      ),
    ).toContain("uri: duckdb://warehouse/raw/orders");
  });

  test("adds workspace dependencies directly and links resolved producers", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "The dependency picker behavior only needs one browser project.",
    );

    await writeCrossPipelineWorkspace(liveApp, { includeDependency: false });
    await waitForCrossPipelineWorkspace(liveApp, page.request);
    const consumerAssetID = Buffer.from("cross-consumer/assets/analytics/orders.sql").toString(
      "base64url",
    );
    const producerAssetID = Buffer.from("cross-producer/assets/raw/orders.sql").toString(
      "base64url",
    );

    await page.goto(
      `${liveApp.baseURL}/pipelines/${consumerPipelineID}/assets/${consumerAssetID}/code`,
    );
    await expect(page.locator(".monaco-editor").first()).toBeVisible({ timeout: 15_000 });
    const properties = await openAssetProperties(page);

    const dependencyInput = properties.getByRole("combobox", { name: "Add dependency" });
    await dependencyInput.fill("raw.orders");
    await expect(page.getByRole("option", { name: /raw\.orders/ })).toBeVisible();
    const uriTransaction = page.waitForRequest(
      (request) =>
        request.method() === "POST" &&
        request.url().includes(`/api/assets/${consumerAssetID}/transactions`),
    );
    await dependencyInput.press("Enter");
    expect((await uriTransaction).postDataJSON()).toEqual({
      type: "dependency.manual.add",
      dependency: { uri: "duckdb://warehouse/raw/orders", mode: "full" },
    });

    await expect(properties.getByRole("button", { name: "raw.orders", exact: true })).toBeVisible({
      timeout: 15_000,
    });

    await dependencyInput.fill("raw.customers");
    await expect(page.getByRole("option", { name: /raw\.customers/ })).toBeVisible();
    const nameTransaction = page.waitForRequest(
      (request) =>
        request.method() === "POST" &&
        request.url().includes(`/api/assets/${consumerAssetID}/transactions`),
    );
    await dependencyInput.press("Enter");
    expect((await nameTransaction).postDataJSON()).toEqual({
      type: "dependency.manual.add",
      dependency: { asset: "raw.customers", mode: "full" },
    });
    await expect(properties.getByText("Producer URI missing", { exact: true })).toBeVisible();
    await expect(properties).toContainText(
      "will not link across pipelines until that URI is declared",
    );

    await properties.getByRole("button", { name: "raw.orders", exact: true }).click();
    await expect(page).toHaveURL(
      new RegExp(`/pipelines/${producerPipelineID}/assets/${producerAssetID}/code(?:\\?.*)?$`),
    );
  });

  test("keeps API producer URIs after refresh and re-adds ignored relations by URI", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "The dependency picker behavior only needs one browser project.",
    );

    await writeAPICrossPipelineWorkspace(liveApp);
    await waitForCrossPipelineWorkspace(liveApp, page.request);
    const producerAssetID = Buffer.from("cross-producer/assets/raw/orders.asset.yml").toString(
      "base64url",
    );
    const consumerAssetID = Buffer.from("cross-consumer/assets/analytics/orders.sql").toString(
      "base64url",
    );
    const producerURI = "duckdb://warehouse/raw/orders";

    await page.goto(
      `${liveApp.baseURL}/pipelines/${producerPipelineID}/assets/${producerAssetID}/code`,
    );
    let properties = await openAssetProperties(page);
    const uriInput = properties.getByRole("textbox", { name: "URI", exact: true });
    const uriResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${producerAssetID}/transactions`) &&
        response.request().method() === "POST",
      { timeout: 15_000 },
    );
    await uriInput.fill(producerURI);
    await uriInput.press("Tab");
    expect((await uriResponse).ok()).toBe(true);
    await expect
      .poll(async () => {
        const asset = await workspaceAsset(liveApp, page.request, producerAssetID);
        return asset?.uri ?? "";
      })
      .toBe(producerURI);

    await page.reload();
    properties = await openAssetProperties(page);
    await expect(properties.getByRole("textbox", { name: "URI", exact: true })).toHaveValue(
      producerURI,
    );

    await page.goto(
      `${liveApp.baseURL}/pipelines/${consumerPipelineID}/assets/${consumerAssetID}/code`,
    );
    properties = await openAssetProperties(page);
    await expect(properties.getByText("Ignored", { exact: true })).toBeVisible();
    const dependencyInput = properties.getByRole("combobox", { name: "Add dependency" });
    await dependencyInput.fill("raw.orders");
    await expect(page.getByRole("option", { name: /raw\.orders/ })).toBeVisible();
    const dependencyRequest = page.waitForRequest(
      (request) =>
        request.method() === "POST" &&
        request.url().includes(`/api/assets/${consumerAssetID}/transactions`),
    );
    await dependencyInput.press("Enter");
    expect((await dependencyRequest).postDataJSON()).toEqual({
      type: "dependency.manual.add",
      dependency: { uri: producerURI, mode: "full" },
    });

    await expect
      .poll(async () => {
        const asset = await workspaceAsset(liveApp, page.request, consumerAssetID);
        return asset?.dependencies?.find((dependency) => dependency.value === producerURI);
      })
      .toEqual(
        expect.objectContaining({
          type: "uri",
          value: producerURI,
          resolved_asset_id: producerAssetID,
        }),
      );
  });

  test("waits without a run and admits the consumer after a producer succeeds", async ({
    liveApp,
    page,
    request,
  }) => {
    test.setTimeout(120_000);
    test.skip(
      test.info().project.name.includes("mobile"),
      "The real scheduler lifecycle only needs one browser project; mobile presentation is covered separately.",
    );

    await writeCrossPipelineWorkspace(liveApp);
    await waitForCrossPipelineWorkspace(liveApp, request);

    const producerDeployment = await deploy(liveApp, request, producerPipelineID);
    const consumerDeployment = await deploy(liveApp, request, consumerPipelineID);
    expect(producerDeployment).toBeTruthy();
    expect(consumerDeployment).toBeTruthy();

    // The first cadence establishes a watermark. Changing it to a minutely
    // cadence makes run_once enqueue the now-missed range immediately, without
    // making this test wait for a wall-clock minute boundary.
    const createSchedule = await request.put(
      `${liveApp.baseURL}/api/pipelines/${consumerPipelineID}/env-schedules/default`,
      {
        data: {
          cron: "0 0 * * *",
          timezone: "UTC",
          catchup_policy: "run_once",
          snapshot_version_id: consumerDeployment,
        },
      },
    );
    expect(createSchedule.ok(), await createSchedule.text()).toBe(true);
    const updateSchedule = await request.put(
      `${liveApp.baseURL}/api/pipelines/${consumerPipelineID}/env-schedules/default`,
      {
        data: {
          cron: "* * * * *",
          timezone: "UTC",
          catchup_policy: "run_once",
          preserve_snapshot: true,
          preserve_variables: true,
        },
      },
    );
    expect(updateSchedule.ok(), await updateSchedule.text()).toBe(true);

    const waiting = await waitForConsumerSchedule(liveApp, request, (schedule) =>
      schedule.deferred_occurrence?.status === "waiting_prerequisites" ? schedule : null,
    );
    expect(waiting.deferred_occurrence?.prerequisite_reason).toContain("raw.orders");
    expect(waiting.last_run).toBeUndefined();

    await page.goto(`${liveApp.baseURL}/schedules`);
    const consumerRow = page
      .getByTestId("schedule-row")
      .filter({ hasText: "cross_consumer" })
      .first();
    await expect(consumerRow.getByText("Waiting for prerequisites", { exact: true })).toBeVisible({
      timeout: 15_000,
    });

    const producerRun = await request.post(
      `${liveApp.baseURL}/api/pipelines/${producerPipelineID}/trigger`,
      { data: { source: "working_tree" } },
    );
    expect(producerRun.ok(), await producerRun.text()).toBe(true);
    const producerRunID = ((await producerRun.json()) as { run: { id: string } }).run.id;
    await waitForRun(liveApp, request, producerRunID, "success");

    const admitted = await waitForConsumerSchedule(liveApp, request, (schedule) =>
      schedule.last_run?.trigger === "schedule" && schedule.last_run.status === "success"
        ? schedule
        : null,
    );
    expect(admitted.deferred_occurrence?.status).not.toBe("waiting_prerequisites");
    expect(admitted.last_run?.id).toBeTruthy();
    await expect(consumerRow.getByText("Waiting for prerequisites", { exact: true })).toHaveCount(
      0,
      { timeout: 20_000 },
    );
  });
});

async function writeCrossPipelineWorkspace(
  liveApp: LiveApp,
  { includeDependency = true }: { includeDependency?: boolean } = {},
) {
  const producerAssets = join(liveApp.workspaceDir, "cross-producer", "assets", "raw");
  const consumerAssets = join(liveApp.workspaceDir, "cross-consumer", "assets", "analytics");
  await mkdir(producerAssets, { recursive: true });
  await mkdir(consumerAssets, { recursive: true });
  await writeFile(
    join(liveApp.workspaceDir, "cross-producer", "pipeline.yml"),
    `id: ${producerPipelineUUID}
name: cross_producer
start_date: "2024-01-01"

default_connections:
  duckdb: duckdb-default
`,
    "utf8",
  );
  await writeFile(
    join(producerAssets, "orders.sql"),
    `/* @bruin
type: duckdb.sql
uri: duckdb://warehouse/raw/orders
materialization:
  type: table
@bruin */

select 1::bigint as order_id, 'ready'::varchar as status
`,
    "utf8",
  );
  await writeFile(
    join(producerAssets, "customers.sql"),
    `/* @bruin
type: duckdb.sql
materialization:
  type: table
@bruin */

select 1::bigint as customer_id
`,
    "utf8",
  );
  await writeFile(
    join(liveApp.workspaceDir, "cross-consumer", "pipeline.yml"),
    `id: ${consumerPipelineUUID}
name: cross_consumer
start_date: "2024-01-01"

default_connections:
  duckdb: duckdb-default
`,
    "utf8",
  );
  await writeFile(
    join(consumerAssets, "orders.sql"),
    `/* @bruin
type: duckdb.sql
${includeDependency ? "depends:\n  - uri: duckdb://warehouse/raw/orders" : ""}
materialization:
  type: view
@bruin */

select * from raw.orders
`,
    "utf8",
  );
}

async function writeAPICrossPipelineWorkspace(liveApp: LiveApp) {
  const producerAssets = join(liveApp.workspaceDir, "cross-producer", "assets", "raw");
  const consumerAssets = join(liveApp.workspaceDir, "cross-consumer", "assets", "analytics");
  await mkdir(producerAssets, { recursive: true });
  await mkdir(consumerAssets, { recursive: true });
  await writeFile(
    join(liveApp.workspaceDir, "cross-producer", "pipeline.yml"),
    `id: ${producerPipelineUUID}
name: cross_producer
start_date: "2024-01-01"

default_connections:
  duckdb: duckdb-default
`,
    "utf8",
  );
  await writeFile(
    join(producerAssets, "orders.asset.yml"),
    `name: raw.orders
type: api
connection: duckdb-default
materialization:
  type: table
  strategy: create+replace
parameters:
  request:
    url: https://api.example.com/orders
    method: GET
  response:
    records_path: data
`,
    "utf8",
  );
  await writeFile(
    join(liveApp.workspaceDir, "cross-consumer", "pipeline.yml"),
    `id: ${consumerPipelineUUID}
name: cross_consumer
start_date: "2024-01-01"

default_connections:
  duckdb: duckdb-default
`,
    "utf8",
  );
  await writeFile(
    join(consumerAssets, "orders.sql"),
    `/* @bruin
type: duckdb.sql
meta:
  renart_dep_drop: a:raw.orders#full
materialization:
  type: view
@bruin */

select * from raw.orders
`,
    "utf8",
  );
}

async function openAssetProperties(page: Page) {
  const inspector = page.locator('[data-testid="asset-inspector"]:visible').first();
  const trigger = page
    .getByRole("button", { name: "Asset properties" })
    .or(page.getByRole("button", { name: "Show properties" }))
    .first();
  await expect
    .poll(async () => (await inspector.isVisible()) || (await trigger.isVisible()), {
      timeout: 15_000,
    })
    .toBe(true);
  if (!(await inspector.isVisible().catch(() => false))) await trigger.click();
  await expect(inspector).toBeVisible({ timeout: 15_000 });
  return inspector;
}

async function deploy(liveApp: LiveApp, request: APIRequestContext, pipelineID: string) {
  const response = await request.post(`${liveApp.baseURL}/api/pipelines/${pipelineID}/deploy`, {
    data: {},
  });
  expect(response.ok(), await response.text()).toBe(true);
  return ((await response.json()) as { snapshot: { version_id: string } }).snapshot.version_id;
}

async function materializeAsset(liveApp: LiveApp, request: APIRequestContext, assetID: string) {
  const response = await request.post(
    `${liveApp.baseURL}/api/assets/${assetID}/materialize/stream?environment=default`,
    { timeout: 60_000 },
  );
  const stream = await response.text();
  expect(response.ok(), stream).toBe(true);
  const doneLine = stream
    .split(/\r?\n/)
    .reverse()
    .find((line) => line.startsWith("data: ") && line.includes('"status"'));
  expect(doneLine, stream).toBeTruthy();
  expect(JSON.parse(doneLine!.slice("data: ".length)).status).toBe("ok");
}

async function waitForCrossPipelineWorkspace(liveApp: LiveApp, request: APIRequestContext) {
  await expect
    .poll(
      async () => {
        const response = await request.get(`${liveApp.baseURL}/api/workspace`);
        if (!response.ok()) return [];
        const body = (await response.json()) as {
          pipelines: Array<{ uuid: string; assets: Array<{ name: string }> }>;
        };
        return body.pipelines
          .filter((pipeline) =>
            [producerPipelineUUID, consumerPipelineUUID].includes(pipeline.uuid),
          )
          .map((pipeline) => pipeline.uuid)
          .sort();
      },
      { timeout: 20_000 },
    )
    .toEqual([consumerPipelineUUID, producerPipelineUUID].sort());
}

async function workspaceAsset(liveApp: LiveApp, request: APIRequestContext, assetID: string) {
  const response = await request.get(`${liveApp.baseURL}/api/workspace`);
  if (!response.ok()) return undefined;
  const workspace = (await response.json()) as {
    pipelines: Array<{
      assets: Array<{
        id: string;
        uri?: string;
        dependencies?: Array<{
          type: string;
          value: string;
          resolved_asset_id?: string;
        }>;
      }>;
    }>;
  };
  return workspace.pipelines
    .flatMap((pipeline) => pipeline.assets)
    .find((asset) => asset.id === assetID);
}

async function waitForConsumerSchedule<T>(
  liveApp: LiveApp,
  request: APIRequestContext,
  select: (schedule: EnvScheduleResponse["schedules"][number]) => T | null,
) {
  const deadline = Date.now() + 30_000;
  let lastBody: EnvScheduleResponse | null = null;
  while (Date.now() < deadline) {
    const response = await request.get(`${liveApp.baseURL}/api/env-schedules`);
    if (response.ok()) {
      lastBody = (await response.json()) as EnvScheduleResponse;
      const schedule = lastBody.schedules.find(
        (candidate) =>
          candidate.pipeline_uuid === consumerPipelineUUID && candidate.environment === "default",
      );
      const selected = schedule ? select(schedule) : null;
      if (selected !== null) return selected;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`Timed out waiting for consumer schedule: ${JSON.stringify(lastBody)}`);
}

async function waitForRun(
  liveApp: LiveApp,
  request: APIRequestContext,
  runID: string,
  status: string,
) {
  await expect
    .poll(
      async () => {
        const response = await request.get(
          `${liveApp.baseURL}/api/runs/${encodeURIComponent(runID)}`,
        );
        if (!response.ok()) return "";
        return ((await response.json()) as { run: { status: string; error?: string } }).run.status;
      },
      { timeout: 60_000 },
    )
    .toBe(status);
}
