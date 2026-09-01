import { expect } from "@playwright/test";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";

import { liveTest as test } from "../live-app-fixture";

type WorkspaceResponse = {
  query_connections?: Array<{
    name: string;
    connection_type: string;
    asset_type: string;
    dialect: string;
  }>;
  pipelines: Array<{
    id: string;
    name?: string;
    path?: string;
    assets: Array<{
      id: string;
      name: string;
      type: string;
      content: string;
      explicit_connection?: string;
    }>;
  }>;
};

const pipelineId = Buffer.from("analytics").toString("base64url");
const customersAssetId = Buffer.from("analytics/assets/analytics/customers.sql").toString(
  "base64url",
);
const ordersAssetId = Buffer.from("analytics/assets/analytics/orders.sql").toString("base64url");
const pythonAssetId = Buffer.from("analytics/assets/analytics/py_metric.py").toString("base64url");

test.describe("app build actions live", () => {
  test.use({ fixtureName: "configured-workspace" });

  test("centers a fitting DAG and opens the first asset selection in split view", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "The full Build canvas is a desktop affordance.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/canvas`);
    const flow = page.locator(".react-flow").first();
    await expect(flow).toBeVisible({ timeout: 15000 });
    const assetNodes = page.locator('[data-testid="lineage-asset"]');
    await expect(assetNodes).toHaveCount(2, { timeout: 15000 });

    await expect
      .poll(async () => {
        const flowBox = await flow.boundingBox();
        const nodeBoxes = await assetNodes.evaluateAll((nodes) =>
          nodes.map((node) => {
            const box = node.getBoundingClientRect();
            return { left: box.left, right: box.right };
          }),
        );
        if (!flowBox || nodeBoxes.length === 0) return Number.POSITIVE_INFINITY;
        const graphLeft = Math.min(...nodeBoxes.map((box) => box.left));
        const graphRight = Math.max(...nodeBoxes.map((box) => box.right));
        return Math.abs((graphLeft + graphRight) / 2 - (flowBox.x + flowBox.width / 2));
      })
      .toBeLessThan(3);

    await page
      .locator(`[data-testid="lineage-asset"][data-asset-id="${customersAssetId}"]`)
      .click();
    await expect(page).toHaveURL(
      new RegExp(`/pipelines/${pipelineId}/assets/${customersAssetId}/split(?:[?].*)?$`),
    );
    const splitFlow = page.locator(".react-flow").first();
    const selectedNode = splitFlow.locator(
      `[data-testid="lineage-asset"][data-asset-id="${customersAssetId}"]`,
    );
    await expect(selectedNode).toBeVisible();
    await expect
      .poll(async () => {
        const flowBox = await splitFlow.boundingBox();
        const nodeBox = await selectedNode.boundingBox();
        if (!flowBox || !nodeBox) return false;
        return (
          nodeBox.x >= flowBox.x &&
          nodeBox.y >= flowBox.y &&
          nodeBox.x + nodeBox.width <= flowBox.x + flowBox.width &&
          nodeBox.y + nodeBox.height <= flowBox.y + flowBox.height
        );
      })
      .toBe(true);

    await expect(page.locator(".view-lines").first()).toContainText("customer_id", {
      timeout: 15000,
    });
    const ordersNode = splitFlow.locator(
      `[data-testid="lineage-asset"][data-asset-id="${ordersAssetId}"]`,
    );
    await ordersNode.click();
    await expect(page.locator(".view-lines").first()).toContainText("order_id", {
      timeout: 15000,
    });
    await expect(page).toHaveURL(
      new RegExp(`/pipelines/${pipelineId}/assets/${ordersAssetId}/split(?:[?].*)?$`),
    );

    await selectedNode.click();
    await expect(page.locator(".view-lines").first()).toContainText("customer_id", {
      timeout: 15000,
    });
    await expect(page).toHaveURL(
      new RegExp(`/pipelines/${pipelineId}/assets/${customersAssetId}/split(?:[?].*)?$`),
    );

    await page.getByRole("link", { name: "Canvas view" }).click();
    await expect(page).toHaveURL(
      new RegExp(`/pipelines/${pipelineId}/assets/${customersAssetId}/canvas(?:[?].*)?$`),
    );
    await page.locator(`[data-testid="lineage-asset"][data-asset-id="${ordersAssetId}"]`).click();
    await expect(page).toHaveURL(
      new RegExp(`/pipelines/${pipelineId}/assets/${ordersAssetId}/canvas(?:[?].*)?$`),
    );
  });

  test("materialize and inspect buttons run the real asset", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".view-lines").first()).toContainText("customer_id", {
      timeout: 15000,
    });
    const materializeResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${customersAssetId}/materialize/stream`) &&
        response.ok(),
      { timeout: 30000 },
    );
    await page.getByRole("button", { name: "Materialize", exact: true }).click();
    await materializeResponse;

    await expect
      .poll(
        async () => {
          const response = await page.request.get(
            `${liveApp.baseURL}/api/runs?pipeline_id=${pipelineId}&limit=5`,
          );
          if (!response.ok()) return "";
          const body = (await response.json()) as { runs?: Array<{ trigger?: string }> };
          return body.runs?.[0]?.trigger ?? "";
        },
        { timeout: 15000 },
      )
      .toBe("manual");

    // The results panel switches to the materialize tab and shows run output.
    await expect(page.locator("pre.font-console").first()).toContainText(/\S/, {
      timeout: 15000,
    });

    const inspectResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${customersAssetId}/inspect`) && response.ok(),
      { timeout: 30000 },
    );
    await page.getByRole("button", { name: "Inspect", exact: true }).click();
    await inspectResponse;

    await expect(page.getByText("Ada").first()).toBeVisible({ timeout: 15000 });

    // The query that actually ran is shown as a collapsible line above the table.
    const disclosure = page.getByTestId("rendered-query-disclosure");
    await expect(disclosure).toBeVisible({ timeout: 15000 });
    await expect(disclosure).toContainText(/select/i);
    await disclosure.getByRole("button", { expanded: false }).click();
    await expect(disclosure.locator("pre")).toContainText(/select/i);
  });

  test("records a canvas context-menu run as manual", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "The canvas context menu is a desktop affordance.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/canvas`);
    const assetNode = page.locator(
      `[data-testid="lineage-asset"][data-asset-id="${customersAssetId}"]`,
    );
    await expect(assetNode).toBeVisible({ timeout: 15000 });

    const materializeResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/assets/${customersAssetId}/materialize/stream`) &&
        response.ok(),
      { timeout: 30000 },
    );
    await assetNode.click({ button: "right" });
    await page.getByRole("menuitem", { name: "Run", exact: true }).click();
    await materializeResponse;

    await expect
      .poll(
        async () => {
          const response = await page.request.get(
            `${liveApp.baseURL}/api/runs?pipeline_id=${pipelineId}&limit=5`,
          );
          if (!response.ok()) return "";
          const body = (await response.json()) as { runs?: Array<{ trigger?: string }> };
          return body.runs?.[0]?.trigger ?? "";
        },
        { timeout: 15000 },
      )
      .toBe("manual");
  });

  test("renders saved execution SQL without running the asset", async ({ liveApp, page }) => {
    await writeFile(
      join(liveApp.workspaceDir, "analytics", "assets", "analytics", "customers.sql"),
      `/* @bruin
type: duckdb.sql
materialization:
  type: view
columns:
  - name: customer_id
    type: integer
    checks:
      - name: not_null
@bruin */

select 1 as customer_id,'Ada' as customer_name union all select 2 as customer_id,'Grace' as customer_name
`,
    );
    const deployResponse = await page.request.post(
      `${liveApp.baseURL}/api/pipelines/${pipelineId}/deploy`,
    );
    expect(deployResponse.ok()).toBe(true);
    const deployedVersion = ((await deployResponse.json()) as { snapshot: { version_id: string } })
      .snapshot.version_id;
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".view-lines").first()).toContainText("customer_id", {
      timeout: 15000,
    });
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

    const renderResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${pipelineId}/assets/render`) && response.ok(),
      { timeout: 30000 },
    );
    await page.getByRole("button", { name: "Render saved asset", exact: true }).click();
    const response = await renderResponse;
    expect(response.request().postDataJSON()).toMatchObject({
      asset_name: "analytics.customers",
      source: { kind: "working_tree" },
      environment: "default",
      full_refresh: false,
    });

    const payload = (await response.json()) as {
      provenance: { source: { kind: string; merkle_root: string } };
      asset: {
        fingerprint: string;
        target: { identity?: string; fidelity: string; kind: string; object?: string };
      };
      stages: Array<{ kind: string; label?: string; content?: string; fidelity: string }>;
    };
    expect(payload.provenance.source).toMatchObject({ kind: "working_tree" });
    expect(payload.provenance.source.merkle_root).toMatch(/^[a-f0-9]{64}$/);
    expect(payload.asset.fingerprint).toMatch(/^v3:[a-f0-9]{64}$/);
    expect(payload.asset.target).toMatchObject({
      fidelity: "exact",
      kind: "relation",
      object: "analytics.customers",
    });
    expect(payload.asset.target.identity).toMatch(/^[a-f0-9]{64}$/);
    expect(payload.stages.find((stage) => stage.kind === "compiled_query")).toMatchObject({
      fidelity: "exact",
    });
    const executionSQL = payload.stages.find((stage) => stage.kind === "execution_sql")?.content;
    expect(executionSQL).toMatch(/create(?:\s+or\s+replace)?\s+view/i);
    const notNullCheck = payload.stages.find((stage) => stage.kind === "check");
    expect(notNullCheck).toMatchObject({
      label: "customer_id · not_null",
      fidelity: "exact",
    });
    expect(notNullCheck?.content).toMatch(/customer_id\s+is\s+null/i);

    const preview = page.getByTestId("asset-render-view");
    await expect(preview).toBeVisible({ timeout: 15000 });
    await expect(preview).toContainText("Preview — not executed");
    await expect(preview).toContainText("Saved workspace");
    await expect(preview).toContainText("DAG v3:");
    await expect(preview).toContainText("Target");
    await expect(preview.getByRole("radio", { name: "Compiled query" })).toBeChecked();
    await preview.getByRole("radio", { name: "Execution SQL" }).click();
    await expect(preview.getByRole("radio", { name: "Execution SQL" })).toBeChecked();
    await expect(preview.locator(".view-lines").first()).toContainText(
      /create(?:\s+or\s+replace)?\s+view/i,
    );
    await preview.getByRole("radio", { name: "customer_id · not_null" }).click();
    await expect(preview.getByRole("radio", { name: "customer_id · not_null" })).toBeChecked();
    await expect(preview).toContainText("Blocking column check");
    await expect(preview.getByRole("button", { name: "Copy rendered operation" })).toBeEnabled();
    expect(executionRequests).toEqual([]);

    const rerenderResponse = page.waitForResponse(
      (candidate) =>
        candidate.url().includes(`/api/pipelines/${pipelineId}/assets/render`) && candidate.ok(),
      { timeout: 30000 },
    );
    const savedWorkspaceRefresh = page.waitForResponse(
      (candidate) =>
        candidate.url().includes(`/api/pipelines/${pipelineId}/type-check`) && candidate.ok(),
      { timeout: 30000 },
    );
    const assetEditor = page.locator(".monaco-editor").first();
    await assetEditor.click();
    await page.keyboard.press("ControlOrMeta+End");
    const savedDraftMarker = "-- render saved draft";
    await page.keyboard.type(`\n${savedDraftMarker}`);
    const rerenderPayload = (await (await rerenderResponse).json()) as {
      stages: Array<{ kind: string; content?: string }>;
    };
    expect(
      rerenderPayload.stages.find((stage) => stage.kind === "compiled_query")?.content,
    ).toContain(savedDraftMarker);

    await expect
      .poll(
        async () => {
          const workspaceResponse = await page.request.get(`${liveApp.baseURL}/api/workspace`);
          if (!workspaceResponse.ok()) return "";
          const workspace = (await workspaceResponse.json()) as WorkspaceResponse;
          return (
            workspace.pipelines
              .flatMap((pipeline) => pipeline.assets)
              .find((asset) => asset.id === customersAssetId)?.content ?? ""
          );
        },
        { timeout: 30000 },
      )
      .toContain(savedDraftMarker);
    await savedWorkspaceRefresh;
    await expect(preview).toBeVisible({ timeout: 15000 });

    const comparisonResponse = page.waitForResponse(
      (candidate) =>
        candidate.url().includes(`/api/pipelines/${pipelineId}/assets/render/compare`) &&
        candidate.ok(),
      { timeout: 30000 },
    );
    await preview.getByRole("button", { name: "Compare deployment", exact: true }).click();
    const compared = await comparisonResponse;
    expect(compared.request().postDataJSON()).toMatchObject({
      asset_name: "analytics.customers",
    });
    const comparisonPayload = (await compared.json()) as {
      snapshot: { version_id: string };
      summary: { changed: number };
      current?: { stages: Array<{ kind: string; content?: string }> };
    };
    expect(comparisonPayload.snapshot.version_id).toBe(deployedVersion);
    expect(comparisonPayload.summary.changed).toBeGreaterThan(0);
    expect(
      comparisonPayload.current?.stages.find((stage) => stage.kind === "compiled_query")?.content,
    ).toContain(savedDraftMarker);
    const comparison = page.getByTestId("asset-render-comparison");
    await expect(comparison).toBeVisible({ timeout: 15000 });
    await expect(comparison).toContainText(/Deployment #\d+/);
    await expect(comparison).toContainText("Saved workspace");
    await expect(comparison.locator(".monaco-diff-editor")).toBeVisible({ timeout: 15000 });

    const savedWorkspaceResponse = await page.request.get(`${liveApp.baseURL}/api/workspace`);
    expect(savedWorkspaceResponse.ok()).toBe(true);
    const savedWorkspace = (await savedWorkspaceResponse.json()) as WorkspaceResponse;
    const savedContent = savedWorkspace.pipelines
      .flatMap((pipeline) => pipeline.assets)
      .find((asset) => asset.id === customersAssetId)?.content;
    expect(savedContent).toContain(savedDraftMarker);
    const externalMarker = "-- external workspace change";
    const externalRenderResponse = page.waitForResponse(
      (candidate) =>
        candidate.url().includes(`/api/pipelines/${pipelineId}/assets/render`) && candidate.ok(),
      { timeout: 30000 },
    );
    const externalUpdate = await page.request.put(
      `${liveApp.baseURL}/api/pipelines/${pipelineId}/assets/${customersAssetId}`,
      {
        data: {
          content: `${savedContent?.trimEnd()}\n${externalMarker}\n`,
        },
      },
    );
    expect(externalUpdate.ok()).toBe(true);
    const externalRenderPayload = (await (await externalRenderResponse).json()) as {
      stages: Array<{ kind: string; content?: string }>;
    };
    expect(
      externalRenderPayload.stages.find((stage) => stage.kind === "compiled_query")?.content,
    ).toContain(externalMarker);
    await expect(preview).toBeVisible({ timeout: 15000 });

    await assetEditor.click();
    await page.keyboard.press("ControlOrMeta+End");
    const newestMarker = "-- newer unsaved render intent";
    const newestRenderResponse = page.waitForResponse(
      (candidate) =>
        candidate.url().includes(`/api/pipelines/${pipelineId}/assets/render`) && candidate.ok(),
      { timeout: 30000 },
    );
    await page.keyboard.type(`\n${newestMarker}`);
    const newestRenderPayload = (await (await newestRenderResponse).json()) as {
      stages: Array<{ kind: string; content?: string }>;
    };
    expect(
      newestRenderPayload.stages.find((stage) => stage.kind === "compiled_query")?.content,
    ).toContain(newestMarker);
    await expect(preview).toBeVisible({ timeout: 15000 });
  });

  test("pipeline run button triggers a scheduler run", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".view-lines").first()).toContainText("customer_id", {
      timeout: 15000,
    });

    let saveFinished = false;
    let saveFinishedWhenPlanned: boolean | undefined;
    await page.route(`**/api/pipelines/${pipelineId}/assets/${customersAssetId}`, async (route) => {
      if (route.request().method() !== "PUT") {
        await route.continue();
        return;
      }
      await new Promise((resolve) => setTimeout(resolve, 400));
      const response = await route.fetch();
      saveFinished = true;
      await route.fulfill({ response });
    });
    page.on("request", (request) => {
      if (
        request.method() === "POST" &&
        request.url().endsWith(`/api/pipelines/${pipelineId}/plan`)
      ) {
        saveFinishedWhenPlanned = saveFinished;
      }
    });

    const editor = page.locator(".monaco-editor").first();
    await editor.click();
    await page.keyboard.press("Control+End");
    await page.keyboard.type("\n-- save barrier e2e");

    const runButton = page.getByRole("button", { name: /^Review run/ });
    await expect(runButton).toHaveAttribute("title", /Review the saved source/);
    await expect(page.getByRole("button", { name: /^Readiness:/ })).toHaveCount(0);

    const planResponse = page.waitForResponse(
      (response) => response.url().endsWith(`/api/pipelines/${pipelineId}/plan`) && response.ok(),
      { timeout: 30000 },
    );
    await runButton.click();
    const planned = await planResponse;
    expect(planned.request().postDataJSON()).toMatchObject({
      source: { kind: "working_tree" },
      selection: { mode: "all" },
    });
    expect(saveFinishedWhenPlanned).toBe(true);

    const planSheet = page.getByTestId("pipeline-plan-sheet");
    await expect(planSheet).toBeVisible();
    await expect(planSheet).toHaveAttribute("data-slot", "dialog-content");
    await expect(planSheet).toContainText("Saved working tree");
    await expect(planSheet.getByRole("tablist")).toHaveCount(0);
    const reviewViewport = planSheet
      .getByTestId("pipeline-plan-scroll")
      .locator(':scope > [data-slot="scroll-area-viewport"]');
    await expect(
      reviewViewport.getByRole("heading", { name: "Review pipeline run" }),
    ).toBeVisible();
    await expect(planSheet.getByRole("button", { name: /Execution details/ })).toBeVisible();
    await expect(reviewViewport.getByRole("heading", { name: "Execution order" })).toHaveCount(0);
    await expect(planSheet.getByText("Run options", { exact: true })).toBeVisible();
    await planSheet.getByRole("button", { name: /Run options/ }).click();
    await expect(planSheet.getByLabel("Scope")).toBeVisible();
    await expect(planSheet.getByLabel("Sensors")).toBeVisible();
    await expect(planSheet.getByRole("switch", { name: "Full refresh" })).toBeVisible();
    const confirmButton = planSheet.getByRole("button", {
      name: /^Run \d+ assets? from working tree$/,
    });
    await expect(confirmButton).toBeEnabled();

    const passingCheck = planSheet.getByLabel("All code checks passed").first();
    await expect(passingCheck).toBeVisible();
    await expect(planSheet.getByText("No findings", { exact: true })).toHaveCount(0);

    const renderedPlanResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/pipelines/${pipelineId}/plan`) &&
        response.request().postDataJSON().include_stage_content === true &&
        response.ok(),
      { timeout: 30000 },
    );
    await planSheet.getByRole("button", { name: /Execution details/ }).click();
    const renderedPlan = await renderedPlanResponse;
    expect(renderedPlan.request().postDataJSON()).toMatchObject({
      include_stage_content: true,
      source: { kind: "working_tree" },
      selection: { mode: "all" },
    });
    await expect(reviewViewport.getByRole("heading", { name: "Execution order" })).toBeVisible();
    await expect(reviewViewport).toContainText("shown in stable plan order");
    await expect(reviewViewport).toContainText("Assets will run one at a time for this pipeline.");
    await expect(reviewViewport.getByText("Sequential", { exact: true })).toBeVisible();
    await expect(
      reviewViewport.getByRole("heading", { name: "Rendered operations" }),
    ).toBeVisible();
    await expect(planSheet.getByText("Preview — not executed")).toBeVisible();
    const operationSelect = planSheet.getByRole("combobox", { name: "Operation" });
    await expect(planSheet.locator(".view-lines").first()).toContainText("save barrier e2e", {
      timeout: 15000,
    });
    await operationSelect.click();
    await page.getByRole("option", { name: "analytics.customers · Execution SQL" }).click();
    await expect(planSheet.locator(".view-lines").first()).toContainText(/create.*view/i, {
      timeout: 15000,
    });

    const confirmResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/pipelines/${pipelineId}/plan/confirm`) &&
        response.request().method() === "POST",
      { timeout: 30000 },
    );
    await confirmButton.click();
    const confirmed = await confirmResponse;
    const confirmBody = await confirmed.text();
    expect(confirmed.ok(), confirmBody).toBe(true);
    expect(confirmed.request().postDataJSON()).toMatchObject({
      plan_id: expect.stringMatching(/^[a-f0-9]{64}$/),
      plan: { source: { kind: "working_tree" }, selection: { mode: "all" } },
      reviewed: {
        pipeline_uuid: expect.any(String),
        source: { kind: "working_tree" },
        selection: { mode: "all" },
        execution_units: expect.any(Array),
      },
    });

    const output = page.locator("pre.font-console").first();
    await expect(output).toContainText("Analyzed the pipeline 'analytics'", {
      timeout: 30000,
    });
    await expect(output).not.toContainText(/Queued manual River run|Run started\.|Run queued\./);
  });

  test("previews assets matched by a custom selector", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".view-lines").first()).toContainText("customer_id", {
      timeout: 15000,
    });

    await page.getByRole("button", { name: /^Review run/ }).click();
    const planSheet = page.getByTestId("pipeline-plan-sheet");
    await expect(planSheet).toBeVisible();

    const wildcardResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/pipelines/${pipelineId}/plan`) &&
        response.request().postDataJSON().selection?.mode === "selector" &&
        response.request().postDataJSON().selection?.selector === "*" &&
        response.ok(),
      { timeout: 30000 },
    );
    await planSheet.getByRole("button", { name: /Run options/ }).click();
    await planSheet.getByLabel("Scope").click();
    await page.getByRole("option", { name: "Matching selector", exact: true }).click();
    await wildcardResponse;

    const selectorInput = planSheet.getByLabel("Asset selector");
    await expect(selectorInput).toHaveValue("*");
    await selectorInput.fill("analytics.customers");
    await expect(
      planSheet.getByText("Apply the expression to validate it and preview its assets."),
    ).toBeVisible();

    const selectorResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/pipelines/${pipelineId}/plan`) &&
        response.request().postDataJSON().selection?.selector === "analytics.customers" &&
        response.ok(),
      { timeout: 30000 },
    );
    await planSheet.getByRole("button", { name: "Apply", exact: true }).click();
    const selectedPlan = (await (await selectorResponse).json()) as {
      selection: { mode: string; selector?: string };
      assets: Array<{ name: string }>;
    };
    expect(selectedPlan.selection).toMatchObject({
      mode: "selector",
      selector: "analytics.customers",
    });
    expect(selectedPlan.assets.map((asset) => asset.name)).toEqual(["analytics.customers"]);
    await expect(planSheet.getByText("1 asset selected.", { exact: false })).toBeVisible();
  });

  test("shows deployment file diffs beneath collapsible file rows", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".view-lines").first()).toContainText("customer_id", {
      timeout: 15000,
    });

    await page.getByRole("button", { name: "Deploy", exact: true }).click();
    const planDialog = page.getByTestId("pipeline-plan-sheet");
    await expect(planDialog).toBeVisible();
    await expect(planDialog.getByRole("tablist")).toHaveCount(0);
    await expect(planDialog.getByRole("heading", { name: "Source changes" })).toBeVisible();

    const fileDisclosure = planDialog
      .locator('section[aria-labelledby="pipeline-deploy-source-changes"]')
      .locator('[data-slot="collapsible"]')
      .first();
    await expect(fileDisclosure).toBeVisible({ timeout: 15000 });
    const fileDiff = fileDisclosure.locator('[data-slot="collapsible-content"]');
    await expect(fileDiff).toBeHidden();
    await fileDisclosure.locator('[data-slot="collapsible-trigger"]').click();
    await expect(fileDiff).toBeVisible();
    await expect(fileDiff).toContainText("Current deployment");
    await expect(fileDiff).toContainText("Saved workspace");
    await expect(fileDiff.locator(".monaco-diff-editor")).toBeVisible({ timeout: 15000 });
    const insertedLine = fileDiff.locator(".monaco-diff-editor .line-insert").first();
    await expect(insertedLine).toBeVisible({ timeout: 15000 });
    await expect
      .poll(async () =>
        insertedLine.evaluate((element) => getComputedStyle(element).backgroundColor),
      )
      .not.toBe("rgba(0, 0, 0, 0)");

    await fileDisclosure.locator('[data-slot="collapsible-trigger"]').click();
    await expect(fileDiff).toBeHidden();
  });

  test("keeps valid sibling previews when an asset definition is incomplete", async ({
    liveApp,
    page,
  }) => {
    await writeFile(
      join(liveApp.workspaceDir, "analytics", "assets", "analytics", "incomplete.asset.yml"),
      "name: analytics.incomplete\ntype: load\nparameters:\n\tobject: unfinished\n",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".view-lines").first()).toContainText("customer_id", {
      timeout: 15000,
    });

    const planResponse = page.waitForResponse(
      (response) => response.url().endsWith(`/api/pipelines/${pipelineId}/plan`) && response.ok(),
      { timeout: 30000 },
    );
    await page.getByRole("button", { name: /^Review run/ }).click();
    const response = await planResponse;
    const plan = (await response.json()) as {
      status: string;
      readiness: { blockers: Array<{ asset_name?: string; message: string }> };
      assets: Array<{ name: string; renders: unknown[] }>;
    };
    expect(plan.status).toBe("blocked");
    expect(plan.readiness.blockers).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          asset_name: "analytics.incomplete",
          message: expect.stringContaining("Asset definition could not be parsed"),
        }),
      ]),
    );
    expect(plan.assets.find((asset) => asset.name === "analytics.customers")?.renders.length).toBe(
      1,
    );
    expect(plan.assets.find((asset) => asset.name === "analytics.incomplete")?.renders).toEqual([]);

    const planSheet = page.getByTestId("pipeline-plan-sheet");
    await expect(planSheet).toContainText("Asset definition could not be parsed");
    await expect(planSheet.getByRole("button", { name: /^Run \d+ assets?/ })).toBeDisabled();
  });

  test("shows an actionable deployed-only blocker when no deployment exists", async ({
    liveApp,
    page,
  }) => {
    const policyResponse = await page.request.put(
      `${liveApp.baseURL}/api/config/environment-policies/default`,
      { data: { protected: false, deployed_only: true, confirm_destructive: false } },
    );
    expect(policyResponse.ok()).toBe(true);

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".view-lines").first()).toContainText("customer_id", {
      timeout: 15000,
    });

    const planResponse = page.waitForResponse(
      (response) => response.url().endsWith(`/api/pipelines/${pipelineId}/plan`) && response.ok(),
      { timeout: 30000 },
    );
    await page.getByRole("button", { name: /^Review run/ }).click();
    const plan = (await (await planResponse).json()) as {
      status: string;
      source: { kind: string };
      readiness: { blockers: Array<{ code: string }> };
    };
    expect(plan).toMatchObject({
      status: "blocked",
      source: { kind: "snapshot" },
      readiness: {
        blockers: expect.arrayContaining([
          expect.objectContaining({ code: "deployment_required" }),
        ]),
      },
    });

    const planSheet = page.getByTestId("pipeline-plan-sheet");
    await expect(planSheet).toContainText("deploy the pipeline first");
    await expect(
      planSheet.getByRole("button", { name: /^Run 0 assets from deployment$/ }),
    ).toBeDisabled();
  });

  test("requires the environment name before confirming a destructive plan", async ({
    liveApp,
    page,
  }) => {
    const policyResponse = await page.request.put(
      `${liveApp.baseURL}/api/config/environment-policies/default`,
      { data: { protected: false, deployed_only: false, confirm_destructive: true } },
    );
    expect(policyResponse.ok()).toBe(true);

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".view-lines").first()).toContainText("customer_id", {
      timeout: 15000,
    });
    await page.getByRole("button", { name: /^Review run/ }).click();
    const planSheet = page.getByTestId("pipeline-plan-sheet");
    await expect(planSheet).toBeVisible();

    const destructivePlanResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/pipelines/${pipelineId}/plan`) &&
        response.request().postDataJSON().full_refresh === true &&
        response.ok(),
      { timeout: 30000 },
    );
    await planSheet.getByRole("button", { name: /Run options/ }).click();
    await planSheet.getByRole("switch", { name: "Full refresh" }).click();
    const destructivePlan = (await (await destructivePlanResponse).json()) as {
      context: { destructive: boolean; environment: string };
    };
    expect(destructivePlan.context).toMatchObject({ destructive: true, environment: "default" });

    const confirmButton = planSheet.getByRole("button", {
      name: /^Run \d+ assets? from working tree$/,
    });
    const confirmation = planSheet.getByLabel(/Type default to confirm destructive operations/);
    await expect(confirmButton).toBeDisabled();
    await confirmation.fill("production");
    await expect(confirmButton).toBeDisabled();
    await confirmation.fill("default");
    await expect(confirmButton).toBeEnabled();

    const confirmResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/pipelines/${pipelineId}/plan/confirm`) && response.ok(),
      { timeout: 30000 },
    );
    await confirmButton.click();
    const confirmed = await confirmResponse;
    expect(confirmed.request().postDataJSON()).toMatchObject({
      confirmed_environment: "default",
      plan: { full_refresh: true },
    });
  });

  test("runs the reviewed Needed units and shows their durable plan", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".view-lines").first()).toContainText("customer_id", {
      timeout: 15000,
    });

    await page.getByRole("button", { name: /^Review run/ }).click();
    const planSheet = page.getByTestId("pipeline-plan-sheet");
    await expect(planSheet).toBeVisible();

    const neededResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/pipelines/${pipelineId}/plan`) &&
        response.request().postDataJSON().selection?.mode === "needed" &&
        response.ok(),
      { timeout: 30000 },
    );
    await planSheet.getByRole("button", { name: /Run options/ }).click();
    await planSheet.getByLabel("Scope").click();
    await page.getByRole("option", { name: "Needed assets" }).click();
    const neededPlanResponse = await neededResponse;
    const neededPlan = (await neededPlanResponse.json()) as {
      id: string;
      context: { environment?: string; start_date: string; end_date: string };
      selection: { mode: string; data_state_token?: string };
      execution_units: Array<{
        asset_name: string;
        start_date: string;
        end_date: string;
        reason: string;
      }>;
      summary: { execution_units: number };
    };
    expect(neededPlan.selection.mode).toBe("needed");
    expect(neededPlan.selection.data_state_token).toMatch(/^renart-data-state-v2:[a-f0-9]{64}$/);
    expect(neededPlan.summary.execution_units).toBeGreaterThan(0);
    expect(neededPlan.execution_units).toHaveLength(neededPlan.summary.execution_units);

    const confirmResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/pipelines/${pipelineId}/plan/confirm`) && response.ok(),
      { timeout: 30000 },
    );
    await planSheet.getByRole("button", { name: /^Run \d+ assets? from working tree$/ }).click();
    const confirmed = await confirmResponse;
    expect(confirmed.request().postDataJSON()).toMatchObject({
      plan_id: neededPlan.id,
      plan: { selection: { mode: "needed" } },
      reviewed: {
        selection: {
          mode: "needed",
          data_state_token: neededPlan.selection.data_state_token,
        },
        execution_units: neededPlan.execution_units,
      },
    });
    const confirmation = (await confirmed.json()) as {
      run: { id: string };
      preview_units_omitted: number;
    };
    expect(confirmation.preview_units_omitted).toBe(0);

    let terminalDetail:
      | {
          run: { status: string; trigger?: string };
          plan?: { plan_id: string; selection: { mode: string }; execution_units: unknown[] };
          units?: Array<{ position: number; asset_name: string; status: string; reason: string }>;
          reexecution?: { mode: string; selection?: string; execution_units?: number };
        }
      | undefined;
    await expect
      .poll(
        async () => {
          const response = await page.request.get(
            `${liveApp.baseURL}/api/runs/${encodeURIComponent(confirmation.run.id)}`,
          );
          if (!response.ok()) return "unavailable";
          terminalDetail = (await response.json()) as typeof terminalDetail;
          return terminalDetail?.run.status ?? "unavailable";
        },
        { timeout: 90000 },
      )
      .toMatch(/^(success|failed|cancelled)$/);
    expect(terminalDetail?.run.status).toBe("success");
    expect(terminalDetail?.plan?.selection.mode).toBe("needed");
    expect(terminalDetail?.units).toHaveLength(neededPlan.execution_units.length);
    expect(terminalDetail?.units?.every((unit) => unit.status === "success")).toBe(true);
    expect(terminalDetail?.reexecution).toEqual({
      mode: "exact",
      selection: "needed",
      execution_units: neededPlan.execution_units.length,
    });

    await page.goto(`${liveApp.baseURL}/runs/${encodeURIComponent(confirmation.run.id)}`);
    await expect(page.getByRole("button", { name: "Re-execute exact plan" })).toBeVisible();
    await expect(page.getByTestId("run-again-context")).toContainText(
      `Mode exact needed plan · ${neededPlan.execution_units.length}`,
    );
    await page.getByRole("tab", { name: "Plan" }).click();
    const retainedPlan = page.getByTestId("run-plan-panel");
    await expect(retainedPlan).toBeVisible({ timeout: 15000 });
    await expect(retainedPlan.getByTestId("run-plan-unit")).toHaveCount(
      neededPlan.execution_units.length,
    );
    await expect(retainedPlan).toContainText("Final asset and window order admitted for this run");
    await expect(retainedPlan).toContainText("Planned stages");

    const reexecutionResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/runs/${confirmation.run.id}/reexecute`) &&
        response.status() === 202,
      { timeout: 30000 },
    );
    await page.getByRole("button", { name: "Re-execute exact plan" }).click();
    const reexecutionAccepted = await reexecutionResponse;
    expect(reexecutionAccepted.request().postDataJSON()).toEqual({});
    const reexecution = (await reexecutionAccepted.json()) as { run: { id: string } };
    await expect(page).toHaveURL(new RegExp(`/runs/${encodeURIComponent(reexecution.run.id)}$`));

    let replayDetail: typeof terminalDetail;
    await expect
      .poll(
        async () => {
          const response = await page.request.get(
            `${liveApp.baseURL}/api/runs/${encodeURIComponent(reexecution.run.id)}`,
          );
          if (!response.ok()) return "unavailable";
          replayDetail = (await response.json()) as typeof terminalDetail;
          return replayDetail?.run.status ?? "unavailable";
        },
        { timeout: 90000 },
      )
      .toMatch(/^(success|failed|cancelled)$/);
    expect(replayDetail?.run).toMatchObject({ status: "success", trigger: "manual" });
    expect(replayDetail?.plan?.plan_id).toBe(terminalDetail?.plan?.plan_id);
    expect(replayDetail?.plan?.selection.mode).toBe("needed");
    expect(replayDetail?.units).toHaveLength(neededPlan.execution_units.length);
    expect(replayDetail?.units?.every((unit) => unit.status === "success")).toBe(true);

    const stalenessQuery = new URLSearchParams({
      environment: neededPlan.context.environment || "default",
      start: neededPlan.context.start_date,
      end: neededPlan.context.end_date,
    });
    await expect
      .poll(
        async () => {
          const response = await page.request.get(
            `${liveApp.baseURL}/api/pipelines/${pipelineId}/staleness?${stalenessQuery}`,
          );
          if (!response.ok()) return [];
          const body = (await response.json()) as { assets: Array<{ status: string }> };
          return body.assets.map((asset) => asset.status);
        },
        { timeout: 30000 },
      )
      .toEqual(expect.arrayContaining(["fresh", "fresh"]));
  });

  test("keeps the primary run action clear when freshness is unavailable", async ({
    liveApp,
    page,
  }) => {
    await page.route(`**/api/pipelines/${pipelineId}/staleness**`, async (route) => {
      await route.fulfill({
        status: 503,
        contentType: "application/json",
        body: JSON.stringify({
          status: "error",
          error: { code: "staleness_unavailable", message: "staleness store unavailable" },
        }),
      });
    });

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".view-lines").first()).toContainText("customer_id", {
      timeout: 15000,
    });

    await expect(page.getByRole("button", { name: /^Readiness:/ })).toHaveCount(0);
    await expect(page.getByRole("button", { name: /^Review run/ })).toBeVisible();
  });

  test("links a rejected pipeline trigger to the already active run", async ({ liveApp, page }) => {
    const activeRunId = "active-run-id";
    await page.route(`**/api/pipelines/${pipelineId}/plan/confirm`, async (route) => {
      await route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          status: "error",
          error: {
            code: "pipeline_run_active",
            message: `pipeline ${pipelineId} already has active run ${activeRunId}`,
            details: { pipeline_id: pipelineId, active_run_id: activeRunId },
          },
        }),
      });
    });

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await expect(page.locator(".view-lines").first()).toContainText("customer_id", {
      timeout: 15000,
    });

    await page.getByRole("button", { name: /^Review run/ }).click();
    const planSheet = page.getByTestId("pipeline-plan-sheet");
    await expect(planSheet).toBeVisible();
    await planSheet.getByRole("button", { name: /^Run \d+ assets? from working tree$/ }).click();
    await expect(planSheet.getByText("Another run was admitted first.")).toBeVisible();
    await expect(planSheet.getByRole("link", { name: "Open active run" })).toHaveAttribute(
      "href",
      `/runs/${activeRunId}`,
    );
  });

  test("explorer creation actions live at the workspace and pipeline scopes", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "The explorer action toolbar is desktop-only.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);

    await page.getByRole("button", { name: "New pipeline", exact: true }).click();
    await expect(page.getByRole("dialog", { name: "New pipeline" })).toBeVisible();
    await page
      .getByRole("dialog", { name: "New pipeline" })
      .getByRole("button", { name: "Cancel" })
      .click();

    const newAsset = page.getByRole("button", { name: /^New asset in / });
    const newFolder = page.getByRole("button", { name: /^New folder in / });
    await expect(newAsset).toBeVisible();
    await expect(newFolder).toBeVisible();

    await newAsset.click();
    const newAssetDialog = page.getByRole("dialog", { name: "New asset" });
    await expect(newAssetDialog).toBeVisible();
    await expect(newAssetDialog.getByLabel("Target connection")).toBeVisible();
    await newAssetDialog.getByRole("button", { name: "Cancel" }).click();

    await newFolder.click();
    await expect(page.getByRole("dialog", { name: "New folder" })).toBeVisible();
    await page
      .getByRole("dialog", { name: "New folder" })
      .getByRole("button", { name: "Cancel" })
      .click();
  });

  test("creates a feature demo from the new pipeline flow", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "The explorer action toolbar is desktop-only.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await page.getByRole("button", { name: "New pipeline", exact: true }).click();

    const dialog = page.getByRole("dialog", { name: "New pipeline" });
    const productStarter = dialog.getByRole("radio", { name: /Product analytics/ });
    await expect(productStarter).toBeVisible();
    await productStarter.click();
    await expect(dialog.getByLabel("Directory")).toHaveValue("product_analytics");
    await expect(dialog.getByLabel("Pipeline name (optional)")).toHaveValue("Product analytics");

    const createResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/pipelines") &&
        response.request().method() === "POST" &&
        (response.request().postData() ?? "").includes('"template":"demo:product"'),
    );
    await dialog.getByRole("button", { name: "Create", exact: true }).click();
    await expect((await createResponse).status()).toBe(201);

    let createdPipelineID = "";
    await expect
      .poll(async () => {
        const response = await page.request.get(`${liveApp.baseURL}/api/workspace`);
        if (!response.ok()) return [];
        const workspace = (await response.json()) as WorkspaceResponse;
        const created = workspace.pipelines.find(
          (pipeline) => pipeline.path === "product_analytics",
        );
        createdPipelineID = created?.id ?? "";
        return (created?.assets ?? []).map((asset) => asset.name).sort();
      })
      .toEqual(
        [
          "product.activation_funnel",
          "product.daily_active_users",
          "product.events",
          "product.user_journeys",
          "product.users",
        ].sort(),
      );
    await expect(page).toHaveURL(new RegExp(`/pipelines/${createdPipelineID}/canvas`));
  });

  test("ad hoc editor uses Monaco with SQL intellisense and runs queries", async ({
    liveApp,
    page,
  }) => {
    // Asserts the explorer entry and the top-bar "Ad-hoc" link highlight in
    // tandem; both are desktop chrome (the explorer is a drawer on mobile and the
    // top-bar link is hidden below lg).
    test.skip(
      test.info().project.name.includes("mobile"),
      "Explorer + top-bar ad-hoc affordances are desktop-only.",
    );

    await writeFile(
      join(liveApp.workspaceDir, ".bruin.yml"),
      `environments:
  default:
    connections:
      duckdb:
        - name: duckdb-default
          path: duckdb-files/local.db
        - name: duckdb-adhoc
          path: duckdb-files/adhoc.db
`,
      "utf8",
    );
    await expect
      .poll(async () => {
        const response = await page.request.get(`${liveApp.baseURL}/api/workspace`);
        if (!response.ok()) return [];
        const workspace = (await response.json()) as WorkspaceResponse;
        return (workspace.query_connections ?? []).map((connection) => connection.name);
      })
      .toEqual(["duckdb-adhoc", "duckdb-default"]);

    // Bare asset URLs open the split editor/canvas view by default.
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}`);
    await expect(page).toHaveURL(
      new RegExp(`/pipelines/${pipelineId}/assets/${customersAssetId}/split$`),
    );
    await expect(page.locator(".react-flow").first()).toBeVisible({ timeout: 15000 });

    // Filtering narrows the current pipeline's assets and can be cleared.
    const filter = page.getByRole("textbox", { name: "Filter assets" });
    await filter.fill("orders");
    await expect(page.getByRole("button", { name: /orders\.sql/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /customers\.sql/ })).toHaveCount(0);
    await filter.fill("no-such-asset");
    await expect(page.getByText("No matching assets.")).toBeVisible();
    await page.getByRole("button", { name: "Clear asset filter" }).click();
    await expect(page.getByRole("button", { name: /customers\.sql/ })).toBeVisible();

    // Opening ad hoc from a split view keeps the split layout.
    await page.getByRole("button", { name: "Ad-hoc query" }).click();
    await expect(page).toHaveURL(
      new RegExp(
        `/pipelines/${pipelineId}/assets/${customersAssetId}/split[?].*editor=adhoc(?:&|$)`,
      ),
    );

    const canvasCustomer = page
      .locator(`[data-testid="lineage-asset"][data-asset-id="${customersAssetId}"]`)
      .locator('[data-slot="asset-node"]');
    const selectedBorderClass = /(?:^|\s)border-primary(?:\s|$)/;
    await expect(canvasCustomer).not.toHaveClass(selectedBorderClass);

    const scratchWorkspace = page.getByTestId("adhoc-editor-workspace");
    await expect(scratchWorkspace).toHaveClass(/bg-primary\/5/);

    const editor = page.locator(".monaco-editor").first();
    await expect(editor).toBeVisible({ timeout: 15000 });
    await expect(page.getByText("Ad-hoc query").first()).toBeVisible();

    // Both the explorer entry and the top-bar button highlight the ad hoc mode.
    await expect(page.locator("button", { hasText: "Ad-hoc query" }).first()).toHaveClass(
      /ring-primary/,
    );
    await expect(page.getByRole("link", { name: "Ad-hoc" }).first()).toHaveClass(/ring-primary/);

    // Selecting even the same context asset leaves scratch mode and restores
    // the repository-backed asset editor and canvas selection.
    await page.getByRole("button", { name: /customers\.sql/ }).click();
    await expect.poll(() => new URL(page.url()).searchParams.get("editor")).toBe("asset");
    await expect(scratchWorkspace).toHaveCount(0);
    await expect(canvasCustomer).toHaveClass(selectedBorderClass);

    // Conversely, the Query result tab owns the scratch editor and opens it
    // automatically while clearing the asset selection.
    await page.getByRole("tab", { name: "Query", exact: true }).click();
    await expect.poll(() => new URL(page.url()).searchParams.get("result")).toBe("query");
    await expect.poll(() => new URL(page.url()).searchParams.get("editor")).toBe("adhoc");
    await expect(scratchWorkspace).toBeVisible();
    await expect(canvasCustomer).not.toHaveClass(selectedBorderClass);

    const connectionSelect = page.getByRole("combobox", { name: "Ad-hoc connection" });
    await expect(connectionSelect).toContainText("duckdb-default");
    await connectionSelect.click();
    await page.getByRole("option", { name: "duckdb-adhoc", exact: true }).click();
    await expect(connectionSelect).toContainText("duckdb-adhoc");

    // The selected asset supplies dialect/graph context, but this document is
    // not the asset itself. Querying it must not manufacture a self-cycle.
    const selfQueryDiagnostics = page.waitForResponse(
      (response) => {
        if (!response.url().includes("/api/sql/lsp/diagnostics") || !response.ok()) return false;
        const body = response.request().postDataJSON() as {
          connection?: string;
          content?: string;
          document_context?: string;
        };
        return (
          body.connection === "duckdb-adhoc" &&
          body.document_context === "adhoc" &&
          body.content?.includes("analytics.customers") === true
        );
      },
      { timeout: 15000 },
    );
    await editor.click();
    await page.keyboard.press("ControlOrMeta+a");
    await page.keyboard.type("select * from analytics.customers");
    const selfQueryPayload = (await (await selfQueryDiagnostics).json()) as {
      diagnostics?: Array<{ code?: string }>;
    };
    expect(selfQueryPayload.diagnostics ?? []).not.toContainEqual(
      expect.objectContaining({ code: "circular-dependency" }),
    );

    // Replace the default draft with a marker query that needs Jinja rendering.
    // Its output deliberately differs from the selected asset declaration: an
    // ad-hoc document borrows graph context, not the asset's output contract.
    const scratchDiagnostics = page.waitForResponse(
      (response) => {
        if (!response.url().includes("/api/sql/lsp/diagnostics") || !response.ok()) return false;
        const body = response.request().postDataJSON() as {
          content?: string;
          document_context?: string;
        };
        return body.document_context === "adhoc" && body.content?.includes("adhoc_ok") === true;
      },
      { timeout: 15000 },
    );
    await editor.click();
    await page.keyboard.press("ControlOrMeta+a");
    await page.keyboard.type("select 'adhoc_ok' as marker, '{{ start_date }}' as win_start");
    const scratchDiagnosticPayload = (await (await scratchDiagnostics).json()) as {
      diagnostics?: Array<{ code?: string }>;
    };
    const scratchDiagnosticCodes = (scratchDiagnosticPayload.diagnostics ?? []).map(
      (diagnostic) => diagnostic.code,
    );
    expect(scratchDiagnosticCodes).not.toContain("declared-output-schema-drift");
    expect(scratchDiagnosticCodes).not.toContain("declared-column-type-drift");
    expect(scratchDiagnosticCodes).not.toContain("declared-column-nullability-drift");

    // The ad hoc editor reuses the SQL parse-context intellisense.
    const parseContextSeen = page.waitForResponse(
      (response) => {
        if (!response.url().includes("/api/sql/parse-context") || !response.ok()) return false;
        const body = response.request().postDataJSON() as { connection?: string; content?: string };
        return body.connection === "duckdb-adhoc" && body.content?.includes("adhoc_ok") === true;
      },
      { timeout: 15000 },
    );
    await parseContextSeen;

    const queryResponse = page.waitForResponse(
      (response) => response.url().includes("/api/sql/query") && response.ok(),
      { timeout: 30000 },
    );
    await page.getByTitle("Run (⌘ + ↵)").click();
    const queryRequestBody = (await queryResponse).request().postDataJSON() as {
      connection: string;
      query: string;
    };
    expect(queryRequestBody.connection).toBe("duckdb-adhoc");
    // The Jinja template was rendered before execution.
    expect(queryRequestBody.query).not.toContain("{{");
    expect(queryRequestBody.query).toMatch(/\d{4}-\d{2}-\d{2}/);
    const queryPayload = (await (await queryResponse).json()) as {
      status: string;
      columns: string[];
    };
    expect(queryPayload.status).toBe("ok");
    expect(queryPayload.columns).toContain("marker");

    await expect(page.getByText("adhoc_ok").first()).toBeVisible({
      timeout: 15000,
    });

    // The rendered query is shown collapsibly above the results.
    const disclosure = page.getByTestId("rendered-query-disclosure");
    await expect(disclosure).toBeVisible();
    await expect(disclosure).toContainText("adhoc_ok");
    await expect(disclosure).not.toContainText("{{");

    // Truncation is represented compactly in the rendered-query strip instead
    // of obscuring the result table with a modal-style warning overlay.
    await editor.click();
    await page.keyboard.press("ControlOrMeta+a");
    await page.keyboard.insertText("select range as value from range(0, 501)");
    const truncatedResponse = page.waitForResponse(
      (response) => response.url().includes("/api/sql/query") && response.ok(),
      { timeout: 30000 },
    );
    await page.getByTitle("Run (⌘ + ↵)").click();
    const truncatedPayload = (await (await truncatedResponse).json()) as {
      rows: unknown[];
      truncated?: boolean;
    };
    expect(truncatedPayload.truncated).toBe(true);
    expect(truncatedPayload.rows).toHaveLength(500);
    await expect(disclosure.getByLabel("Result limited to the first 500 rows")).toBeVisible();
    await expect(disclosure).toContainText("LIMIT 500");
    await expect(page.getByTestId("inspect-warning-banner")).toHaveCount(0);

    const queryIcon = disclosure.getByTestId("rendered-query-icon");
    await expect(queryIcon).toHaveAttribute("stroke-width", "1.5");
    await disclosure.getByRole("button", { expanded: false }).click();
    await expect(queryIcon).toHaveAttribute("stroke-width", "2.75");
  });

  test("converts an ad hoc query to an asset and a notebook cell", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "The desktop Build header exposes the ad-hoc conversion actions.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await page.getByRole("button", { name: "Ad-hoc query" }).click();
    const editor = page.locator(".monaco-editor").first();
    await expect(editor).toBeVisible({ timeout: 15000 });
    const query = "select 42 as converted_marker";
    await editor.click();
    await page.keyboard.press("ControlOrMeta+a");
    await page.keyboard.insertText(query);

    await page.getByRole("button", { name: "Convert to asset" }).click();
    const assetDialog = page.getByRole("dialog", { name: "New asset" });
    await expect(assetDialog).toBeVisible();
    await assetDialog.getByLabel("Asset name").fill("analytics.adhoc_converted");
    await assetDialog.getByRole("button", { name: "Create", exact: true }).click();
    await expect(assetDialog).toBeHidden({ timeout: 15000 });

    await expect
      .poll(
        async () => {
          const response = await page.request.get(`${liveApp.baseURL}/api/workspace`);
          const workspace = (await response.json()) as WorkspaceResponse;
          return (
            workspace.pipelines
              .flatMap((pipeline) => pipeline.assets)
              .find((asset) => asset.name === "analytics.adhoc_converted")?.content ?? ""
          );
        },
        { timeout: 30000 },
      )
      .toContain(query);

    const convertedResponse = await page.request.get(`${liveApp.baseURL}/api/workspace`);
    const convertedWorkspace = (await convertedResponse.json()) as WorkspaceResponse;
    const convertedAsset = convertedWorkspace.pipelines
      .flatMap((pipeline) => pipeline.assets)
      .find((asset) => asset.name === "analytics.adhoc_converted");
    expect(convertedAsset?.type).toBe("duckdb.sql");
    expect(convertedAsset?.explicit_connection).toBe("duckdb-default");

    await page.getByRole("button", { name: "Ad-hoc query" }).click();
    await expect(page.locator(".view-lines").first()).toContainText("converted_marker", {
      timeout: 15000,
    });
    await page.getByRole("button", { name: "Convert to notebook cell" }).click();
    const notebookDialog = page.getByRole("dialog", { name: "Convert to notebook cell" });
    await expect(notebookDialog).toBeVisible();
    await notebookDialog.getByRole("combobox", { name: "Notebook", exact: true }).click();
    await page.getByRole("option", { name: "New notebook…" }).click();
    await notebookDialog.getByLabel("Notebook title").fill("Converted exploration");
    await notebookDialog.getByLabel("Cell name").fill("adhoc_query");
    await notebookDialog.getByRole("button", { name: "Create cell" }).click();

    await expect(page).toHaveURL(/\/notebooks\//, { timeout: 15000 });
    await expect(page.getByText("Converted exploration").first()).toBeVisible({ timeout: 15000 });
    await expect(page.locator(".view-lines").first()).toContainText("converted_marker", {
      timeout: 15000,
    });
  });

  test("ad hoc mode adds a split editor to canvas and preserves full-size code", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "The top-bar ad-hoc affordance is hidden below lg.",
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/canvas`);
    await page.getByRole("link", { name: "Ad-hoc" }).click();
    await expect(page).toHaveURL(
      new RegExp(
        `/pipelines/${pipelineId}/assets/${customersAssetId}/split[?].*editor=adhoc(?:&|$)`,
      ),
    );

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/code`);
    await page.getByRole("link", { name: "Ad-hoc" }).click();
    await expect(page).toHaveURL(
      new RegExp(
        `/pipelines/${pipelineId}/assets/${customersAssetId}/code[?].*editor=adhoc(?:&|$)`,
      ),
    );
  });

  test("python assets never call the SQL parse-context endpoint", async ({ liveApp, page }) => {
    await writeFile(
      join(liveApp.workspaceDir, "analytics", "assets", "analytics", "py_metric.py"),
      `""" @bruin
name: analytics.py_metric
type: python
@bruin """

print("hello")
`,
      "utf8",
    );

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
              .find((asset) => asset.id === pythonAssetId)?.content ?? ""
          );
        },
        { timeout: 30000 },
      )
      .toContain("print");

    const parseContextRequests: string[] = [];
    page.on("request", (request) => {
      if (request.url().includes("/api/sql/parse-context")) {
        parseContextRequests.push(request.url());
      }
    });

    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${pythonAssetId}/code`);
    const editor = page.locator(".monaco-editor").first();
    await expect(editor).toBeVisible({ timeout: 15000 });
    await expect(page.locator(".view-lines").first()).toContainText("print", {
      timeout: 15000,
    });

    // Type into the editor and give the 350 ms parse-context debounce plenty
    // of time to fire if the guard were broken.
    await editor.click();
    await page.keyboard.press("ControlOrMeta+End");
    await page.keyboard.type("\n# comment");
    await page.waitForTimeout(1500);

    expect(parseContextRequests).toEqual([]);
  });
});
