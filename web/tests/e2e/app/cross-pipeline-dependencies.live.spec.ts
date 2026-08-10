import { expect, type APIRequestContext } from "@playwright/test";
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
    test.setTimeout(90_000);
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

    await expect(
      page.locator(`[data-testid="lineage-asset"][data-asset-id="${producerAssetID}"]`),
    ).toBeVisible({ timeout: 15_000 });
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
    expect(
      await readFile(
        join(liveApp.workspaceDir, "cross-consumer", "assets", "analytics", "orders.sql"),
        "utf8",
      ),
    ).toContain("uri: duckdb://warehouse/raw/orders");
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

async function deploy(liveApp: LiveApp, request: APIRequestContext, pipelineID: string) {
  const response = await request.post(`${liveApp.baseURL}/api/pipelines/${pipelineID}/deploy`, {
    data: {},
  });
  expect(response.ok(), await response.text()).toBe(true);
  return ((await response.json()) as { snapshot: { version_id: string } }).snapshot.version_id;
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
