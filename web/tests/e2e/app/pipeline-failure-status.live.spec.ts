import { expect, type APIRequestContext } from "@playwright/test";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { liveTest as test, timeoutForRetry, type LiveApp } from "../live-app-fixture";

const pipelinePath = "failure-flow";
const pipelineId = Buffer.from(pipelinePath).toString("base64url");
const firstAssetName = "failure_flow.first";
const gateAssetName = "failure_flow.gate";
const childAssetName = "failure_flow.child";
const gateAssetId = Buffer.from(`${pipelinePath}/assets/failure_flow/gate.py`).toString(
  "base64url",
);
const childAssetId = Buffer.from(`${pipelinePath}/assets/failure_flow/child.sql`).toString(
  "base64url",
);

type TriggerResponse = {
  status: "ok" | "error";
  run: { id: string };
};

type RunDetailResponse = {
  status: "ok" | "error";
  run: { id: string; status: string };
  steps?: Array<{ asset: string; status: string }>;
};

type AssetStaleness = {
  asset_name: string;
  status: string;
  last_run_status?: string;
  last_run_on_current_content?: boolean;
};

test.describe("pipeline failure asset status live", () => {
  test.use({ fixtureName: "configured-workspace" });

  // Desktop-only: Canvas status transitions are a desktop affordance.
  test("keeps an unreached child out of pending and preserves exact-target freshness @desktop-only", async ({
    liveApp,
    page,
    request,
  }) => {
    test.setTimeout(timeoutForRetry(test.info(), 240000, 60000));

    const { sentinelPath, releasePath } = await addFailurePipeline(liveApp);
    await waitForPipelineAssets(liveApp, request);

    // Establish successful attempts for all three assets. SQL outputs have
    // exact physical targets and become fresh; the Python gate remains
    // runtime-only because its user code does not report a durable target.
    const baselineRunId = await triggerPipeline(liveApp, request);
    const baseline = await waitForTerminalRun(liveApp, request, baselineRunId);
    expect(baseline.run.status).toBe("success");
    expect(stepStatus(baseline, firstAssetName)).toBe("success");
    expect(stepStatus(baseline, gateAssetName)).toBe("success");
    expect(stepStatus(baseline, childAssetName)).toBe("success");

    await page.goto(`${liveApp.baseURL}/catalog?asset=${gateAssetId}`);
    const gateNode = page.getByTestId(`rf__node-${gateAssetId}`);
    const childNode = page.getByTestId(`rf__node-${childAssetId}`);
    await expect(gateNode.locator('[title="Staleness: Never built"]')).toBeVisible({
      timeout: 20000,
    });
    await expect(childNode.locator('[title="Staleness: Fresh"]')).toBeVisible();
    await expect(gateNode.locator("[data-last-run]")).toHaveCount(0);

    // The source and fingerprints stay identical; only the external condition
    // changes. While the gate sleeps, it alone is Running. Its downstream child
    // has not started and must never inherit pipeline-level pending state.
    await rm(sentinelPath);
    const failedRunId = await triggerPipeline(liveApp, request);
    await waitForStepStatus(
      liveApp,
      request,
      failedRunId,
      gateAssetName,
      "running",
      timeoutForRetry(test.info(), 90000, 30000),
    );
    await expect(gateNode.getByText("Running", { exact: true })).toBeVisible({
      timeout: timeoutForRetry(test.info(), 15000),
    });
    await expect(childNode.getByText("Running", { exact: true })).toHaveCount(0);
    await writeFile(releasePath, "release\n", "utf8");

    const failed = await waitForTerminalRun(liveApp, request, failedRunId);
    expect(failed.run.status).toBe("failed");
    expect(stepStatus(failed, firstAssetName)).toBe("success");
    expect(stepStatus(failed, gateAssetName)).toBe("failed");
    expect(failed.steps?.some((step) => step.asset === childAssetName)).toBe(false);

    await expect
      .poll(
        async () => {
          const statuses = await getPipelineStaleness(liveApp, request);
          const gate = statuses.find((asset) => asset.asset_name === gateAssetName);
          const child = statuses.find((asset) => asset.asset_name === childAssetName);
          return {
            gate: `${gate?.status}/${gate?.last_run_status}/${gate?.last_run_on_current_content}`,
            child: `${child?.status}/${child?.last_run_status}/${child?.last_run_on_current_content}`,
          };
        },
        { timeout: 30000 },
      )
      .toEqual({
        gate: "never_built/failed/true",
        child: "fresh/succeeded/true",
      });

    await expect(gateNode.getByText("Running", { exact: true })).toHaveCount(0, {
      timeout: 30000,
    });
    await expect(gateNode.locator('[title="Staleness: Never built"]')).toBeVisible();
    await expect(gateNode.locator('[data-last-run="failed"]')).toHaveText("Build failed");
    await expect(childNode.getByText("Running", { exact: true })).toHaveCount(0);
    await expect(childNode.locator("[data-last-run]")).toHaveCount(0);

    // A page reload discards all transient React state and rehydrates only from
    // the canonical state database. The same independent statuses must remain.
    await page.reload();
    const reloadedGateNode = page.getByTestId(`rf__node-${gateAssetId}`);
    const reloadedChildNode = page.getByTestId(`rf__node-${childAssetId}`);
    await expect(reloadedGateNode.locator('[title="Staleness: Never built"]')).toBeVisible({
      timeout: 20000,
    });
    await expect(reloadedGateNode.locator('[data-last-run="failed"]')).toHaveText("Build failed");
    await expect(reloadedChildNode.getByText("Running", { exact: true })).toHaveCount(0);
    await expect(reloadedChildNode.locator("[data-last-run]")).toHaveCount(0);
  });
});

async function addFailurePipeline(liveApp: LiveApp) {
  const pipelineDir = join(liveApp.workspaceDir, pipelinePath);
  const assetsDir = join(pipelineDir, "assets", "failure_flow");
  const sentinelPath = join(liveApp.workspaceDir, "failure-flow-sentinel.txt");
  const releasePath = join(liveApp.workspaceDir, "failure-flow-release.txt");
  await mkdir(assetsDir, { recursive: true });
  await writeFile(sentinelPath, "present\n", "utf8");
  await Promise.all([
    writeFile(
      join(pipelineDir, "pipeline.yml"),
      `id: 7ee9675a-df3f-4521-bdf5-7f59ec1bf6d3
name: failure-flow
schedule: daily
start_date: "2024-01-01"

default_connections:
  duckdb: duckdb-default
`,
      "utf8",
    ),
    writeFile(
      join(assetsDir, "first.sql"),
      `/* @bruin
name: ${firstAssetName}
type: duckdb.sql
materialization:
  type: table
@bruin */

select 1 as value
`,
      "utf8",
    ),
    writeFile(
      join(assetsDir, "gate.py"),
      `""" @bruin
name: ${gateAssetName}
type: python
depends:
  - ${firstAssetName}
@bruin """
import os
import time

if not os.path.exists(${JSON.stringify(sentinelPath)}):
    deadline = time.monotonic() + 120
    while not os.path.exists(${JSON.stringify(releasePath)}) and time.monotonic() < deadline:
        time.sleep(0.05)
assert os.path.exists(${JSON.stringify(sentinelPath)}), "sentinel missing"
print("gate open")
`,
      "utf8",
    ),
    writeFile(
      join(assetsDir, "child.sql"),
      `/* @bruin
name: ${childAssetName}
type: duckdb.sql
materialization:
  type: table
depends:
  - ${gateAssetName}
@bruin */

select 3 as value
`,
      "utf8",
    ),
  ]);
  return { sentinelPath, releasePath };
}

async function waitForPipelineAssets(liveApp: LiveApp, request: APIRequestContext) {
  await expect
    .poll(
      async () => {
        const response = await request.get(`${liveApp.baseURL}/api/workspace`);
        if (!response.ok()) return [];
        const body = (await response.json()) as {
          pipelines: Array<{ id: string; assets: Array<{ name: string }> }>;
        };
        return (
          body.pipelines
            .find((pipeline) => pipeline.id === pipelineId)
            ?.assets.map((asset) => asset.name)
            .sort() ?? []
        );
      },
      { timeout: 30000 },
    )
    .toEqual([childAssetName, firstAssetName, gateAssetName].sort());
}

async function triggerPipeline(liveApp: LiveApp, request: APIRequestContext) {
  const response = await request.post(
    `${liveApp.baseURL}/api/pipelines/${encodeURIComponent(pipelineId)}/trigger`,
    { data: { environment: "default" } },
  );
  expect(response.ok()).toBe(true);
  const body = (await response.json()) as TriggerResponse;
  expect(body.status).toBe("ok");
  expect(body.run.id).toBeTruthy();
  return body.run.id;
}

async function waitForTerminalRun(liveApp: LiveApp, request: APIRequestContext, runId: string) {
  await expect
    .poll(
      async () => {
        const detail = await getRun(liveApp, request, runId);
        return detail?.run.status;
      },
      { timeout: 90000 },
    )
    .toMatch(/^(success|failed|cancelled)$/);
  const detail = await getRun(liveApp, request, runId);
  if (!detail) throw new Error(`Run ${runId} disappeared after reaching a terminal state.`);
  return detail;
}

async function waitForStepStatus(
  liveApp: LiveApp,
  request: APIRequestContext,
  runId: string,
  assetName: string,
  status: string,
  timeout: number,
) {
  await expect
    .poll(
      async () => {
        const detail = await getRun(liveApp, request, runId);
        return detail ? stepStatus(detail, assetName) : undefined;
      },
      { timeout },
    )
    .toBe(status);
}

async function getRun(liveApp: LiveApp, request: APIRequestContext, runId: string) {
  const response = await request.get(`${liveApp.baseURL}/api/runs/${encodeURIComponent(runId)}`);
  if (!response.ok()) return null;
  const body = (await response.json()) as RunDetailResponse;
  return body.status === "ok" ? body : null;
}

async function getPipelineStaleness(liveApp: LiveApp, request: APIRequestContext) {
  const response = await request.get(
    `${liveApp.baseURL}/api/pipelines/${pipelineId}/staleness?environment=default`,
  );
  expect(response.ok()).toBe(true);
  const body = (await response.json()) as { assets: AssetStaleness[] };
  return body.assets;
}

function stepStatus(detail: RunDetailResponse, assetName: string) {
  return detail.steps?.find((step) => step.asset === assetName)?.status;
}
