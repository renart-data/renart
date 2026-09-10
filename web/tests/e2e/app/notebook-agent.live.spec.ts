import { expect, type Page } from "@playwright/test";
import { resolve } from "node:path";

import { liveTest as test } from "../live-app-fixture";

const fakeCodex = resolve(__dirname, "..", "..", "fixtures", "fake-codex-notebook-agent");

async function openNotebookAssistant(page: Page) {
  if ((page.viewportSize()?.width ?? 0) < 1280) {
    await page.getByRole("tab", { name: "Notebooks", exact: true }).click();
  }
  await page.getByRole("tab", { name: "AI", exact: true }).click();
}

test.use({
  fixtureName: "basic-workspace",
  liveAppEnv: { RENART_CODEX_BINARY: fakeCodex },
});

test.describe("notebook agent chat live", () => {
  test("streams a scoped local-agent turn and restores it after navigation", async ({
    page,
    liveApp,
  }) => {
    const created = await page.request.post(`${liveApp.baseURL}/api/notebooks`, {
      data: { title: "Agent workspace" },
    });
    expect(created.ok()).toBe(true);
    const payload = (await created.json()) as {
      notebook: { id: string; cells: Array<{ cell_id?: string; name: string }> };
    };
    const referencedCell = payload.notebook.cells[0];
    expect(referencedCell?.cell_id).toBeTruthy();
    const workspaceResponse = await page.request.get(`${liveApp.baseURL}/api/workspace`);
    expect(workspaceResponse.ok()).toBe(true);
    const workspacePayload = (await workspaceResponse.json()) as {
      pipelines: Array<{ assets: Array<{ id: string; name: string }> }>;
    };
    const referencedAsset = workspacePayload.pipelines[0]?.assets[0];
    expect(referencedAsset).toBeTruthy();

    await page.goto(`${liveApp.baseURL}/notebooks/${payload.notebook.id}`);
    await openNotebookAssistant(page);

    await expect(page.getByText("Work on this notebook together")).toBeVisible();
    const composer = page.getByPlaceholder("Ask about this notebook…");
    const send = page.getByRole("button", { name: "Send message" });
    const [composerBounds, sendBounds] = await Promise.all([
      composer.boundingBox(),
      send.boundingBox(),
    ]);
    expect(composerBounds).not.toBeNull();
    expect(sendBounds).not.toBeNull();
    expect(Math.abs(sendBounds!.width - sendBounds!.height)).toBeLessThan(2);
    expect(sendBounds!.x).toBeGreaterThan(composerBounds!.x + composerBounds!.width / 2);
    await page.getByRole("button", { name: "Reference" }).click();
    await page.getByRole("option", { name: new RegExp(referencedCell.name) }).click();
    await page.getByRole("button", { name: "Reference" }).click();
    await page.getByRole("option", { name: new RegExp(referencedAsset.name) }).click();
    await expect(page.getByTitle(`Remove cell reference ${referencedCell.name}`)).toBeVisible();
    await expect(page.getByTitle(`Remove asset reference ${referencedAsset.name}`)).toBeVisible();

    const messageRequest = page.waitForRequest(
      (request) => request.url().includes("/agent/messages") && request.method() === "POST",
    );
    await composer.fill("Summarize this notebook.");
    await composer.press("Enter");
    const submitted = (await messageRequest).postDataJSON() as {
      references?: Array<{ kind: string; id: string }>;
    };
    expect(submitted.references).toEqual([
      { kind: "cell", id: referencedCell.cell_id },
      { kind: "asset", id: referencedAsset.id },
    ]);

    await expect(page.getByText("Summarize this notebook.")).toBeVisible();
    const assistant = page.getByRole("tabpanel", { name: "AI" });
    await expect(assistant.getByText(referencedCell.name, { exact: true })).toBeVisible();
    await expect(assistant.getByText(referencedAsset.name, { exact: true })).toBeVisible();
    await expect(page.getByText("Reading the notebook outline")).toBeVisible();
    await expect(
      page.getByText("This notebook has one SQL cell and is ready to explore."),
    ).toBeVisible();

    await page.goto(`${liveApp.baseURL}/notebooks`);
    await page.getByRole("button", { name: /^Agent workspace .* 1 cell$/ }).click();
    await openNotebookAssistant(page);

    await expect(page.getByText("Summarize this notebook.")).toBeVisible();
    const restoredAssistant = page.getByRole("tabpanel", { name: "AI" });
    await expect(restoredAssistant.getByText(referencedCell.name, { exact: true })).toBeVisible();
    await expect(restoredAssistant.getByText(referencedAsset.name, { exact: true })).toBeVisible();
    await expect(
      page.getByText("This notebook has one SQL cell and is ready to explore."),
    ).toBeVisible();
  });

  test("pauses a native turn for a questionnaire and resumes with the answer", async ({
    page,
    liveApp,
  }) => {
    const created = await page.request.post(`${liveApp.baseURL}/api/notebooks`, {
      data: { title: "Questionnaire workspace" },
    });
    expect(created.ok()).toBe(true);
    const payload = (await created.json()) as { notebook: { id: string } };

    await page.goto(`${liveApp.baseURL}/notebooks/${payload.notebook.id}`);
    await openNotebookAssistant(page);

    const composer = page.getByPlaceholder("Ask about this notebook…");
    await composer.fill("Ask me which metric the chart should use.");
    await composer.press("Enter");

    const questionnaire = page.getByTestId("notebook-agent-questionnaire");
    await expect(questionnaire.getByText("Choose a metric", { exact: true })).toBeVisible();
    await expect(questionnaire.getByText("Which metric should the chart use?")).toBeVisible();
    await expect(composer).toBeDisabled();

    await page.goto(`${liveApp.baseURL}/notebooks`);
    await page.getByRole("button", { name: /^Questionnaire workspace .* 1 cell$/ }).click();
    await openNotebookAssistant(page);
    await expect(questionnaire.getByText("Choose a metric", { exact: true })).toBeVisible();

    await questionnaire.getByRole("radio", { name: /Revenue/ }).check();
    const answerResponse = page.waitForResponse(
      (response) => response.url().includes("/agent/interactions/") && response.ok(),
    );
    await questionnaire.getByRole("button", { name: "Send answer" }).click();
    await answerResponse;

    await expect(page.getByText("You answered", { exact: true })).toBeVisible();
    await expect(page.getByText("Continuing with revenue.", { exact: true })).toBeVisible();
    await expect(composer).toBeEnabled();
  });

  test("creates and grants a credential-blind connection for one Edit turn", async ({
    page,
    liveApp,
  }) => {
    const created = await page.request.post(`${liveApp.baseURL}/api/notebooks`, {
      data: { title: "Connection approval workspace" },
    });
    expect(created.ok()).toBe(true);
    const payload = (await created.json()) as { notebook: { id: string } };

    await page.goto(`${liveApp.baseURL}/notebooks/${payload.notebook.id}`);
    await openNotebookAssistant(page);
    await page.getByRole("radio", { name: "Edit", exact: true }).click();

    const composer = page.getByPlaceholder("Describe what to change in this notebook…");
    await composer.fill("Connect me to a new notebook data source.");
    await composer.press("Enter");

    const approval = page.getByTestId("notebook-agent-connection-access");
    await expect(approval.getByText("Create a notebook source connection")).toBeVisible();
    await expect(approval).toContainText("credentials write-only");
    await expect(approval).toContainText("Approval expires when this Edit turn ends");
    await expect(composer).toBeDisabled();

    await approval.getByRole("button", { name: "New connection" }).click();
    const dialog = page.getByRole("dialog", { name: "New connection" });
    await expect(dialog.getByLabel("Name")).toHaveValue("agent-source");
    await expect(dialog.getByLabel("Type")).toContainText("duckdb");
    await dialog.getByLabel("path").fill("duckdb-files/agent-source.db");
    const answerResponse = page.waitForResponse(
      (response) => response.url().includes("/agent/interactions/") && response.ok(),
    );
    await dialog.getByRole("button", { name: "Create connection" }).click();
    await answerResponse;

    await expect(dialog).toBeHidden();
    await expect(page.getByText("Connection approved", { exact: true })).toBeVisible();
    await expect(page.getByText("Previewing approved source data", { exact: true })).toBeVisible();
    await expect(
      page.getByText("Connected to agent-source and previewed 42 without receiving credentials.", {
        exact: true,
      }),
    ).toBeVisible();
    await expect(composer).toBeEnabled();

    const workspaceResponse = await page.request.get(`${liveApp.baseURL}/api/workspace`);
    const workspace = (await workspaceResponse.json()) as {
      query_connections?: Array<{ name: string; connection_type: string }>;
    };
    expect(workspace.query_connections).toContainEqual({
      name: "agent-source",
      connection_type: "duckdb",
      asset_type: "duckdb.sql",
      dialect: "duckdb",
    });
  });
});
