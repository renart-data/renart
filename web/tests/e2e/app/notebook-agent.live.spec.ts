import { expect } from "@playwright/test";
import { resolve } from "node:path";

import { liveTest as test } from "../live-app-fixture";

const fakeCodex = resolve(__dirname, "..", "..", "fixtures", "fake-codex-notebook-agent");

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
    const payload = (await created.json()) as { notebook: { id: string } };

    await page.goto(`${liveApp.baseURL}/notebooks/${payload.notebook.id}`);
    await page.getByRole("button", { name: "Notebook assistant" }).click();

    await expect(page.getByText("Work on this notebook together")).toBeVisible();
    const composer = page.getByPlaceholder("Ask about this notebook…");
    await composer.fill("Summarize this notebook.");
    await composer.press("Enter");

    await expect(page.getByText("Summarize this notebook.")).toBeVisible();
    await expect(page.getByText("Reading the notebook outline")).toBeVisible();
    await expect(
      page.getByText("This notebook has one SQL cell and is ready to explore."),
    ).toBeVisible();

    await page.goto(`${liveApp.baseURL}/notebooks`);
    await page.getByText("Agent workspace", { exact: true }).click();
    await page.getByRole("button", { name: "Notebook assistant" }).click();

    await expect(page.getByText("Summarize this notebook.")).toBeVisible();
    await expect(
      page.getByText("This notebook has one SQL cell and is ready to explore."),
    ).toBeVisible();
  });
});
