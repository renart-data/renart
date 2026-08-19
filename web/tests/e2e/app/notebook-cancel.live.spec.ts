import { expect, type APIRequestContext } from "@playwright/test";

import { liveTest as test } from "../live-app-fixture";

async function createNotebook(request: APIRequestContext, baseURL: string, title: string) {
  const response = await request.post(`${baseURL}/api/notebooks`, { data: { title } });
  expect(response.ok()).toBe(true);
  return (await response.json()).notebook as { id: string };
}

async function addCell(
  request: APIRequestContext,
  baseURL: string,
  notebookId: string,
  name: string,
) {
  const response = await request.post(`${baseURL}/api/notebooks/${notebookId}/cells`, {
    data: { name },
  });
  expect(response.ok()).toBe(true);
  const notebook = (await response.json()).notebook as {
    cells: Array<{ cell_id: string; name: string }>;
  };
  return notebook.cells.find((cell) => cell.name === name)!.cell_id;
}

async function setSql(
  request: APIRequestContext,
  baseURL: string,
  notebookId: string,
  cellId: string,
  body: string,
) {
  const content = `/* @bruin\ntype: duckdb.sql\n@bruin */\n${body}\n`;
  expect(
    (
      await request.put(`${baseURL}/api/notebooks/${notebookId}/cells/${cellId}`, {
        data: { content },
      })
    ).ok(),
  ).toBe(true);
}

// A query DuckDB cannot fold to a constant: the WHERE forces it to evaluate
// every pair of the cross product, so it keeps running until interrupted.
const HEAVY_SQL =
  "select count(*) as c from range(4000000) t1(x), range(4000000) t2(y) where (x * y) % 7 = 0";

test.describe("notebook run cancellation", () => {
  test.use({ fixtureName: "configured-workspace" });

  test("Stop interrupts a running cell and the session stays responsive", async ({
    liveApp,
    page,
  }) => {
    const { request } = page;
    // Keep auto-recompute out of it: the only runs are the ones we trigger.
    await page.addInitScript(() =>
      window.localStorage.setItem("renart-notebook-autorecompute", "off"),
    );

    const notebook = await createNotebook(request, liveApp.baseURL, "Cancel");
    const heavyCell = await addCell(request, liveApp.baseURL, notebook.id, "heavy");
    await setSql(request, liveApp.baseURL, notebook.id, heavyCell, HEAVY_SQL);

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Cancel").first()).toBeVisible({ timeout: 15000 });

    // Start the heavy run. The toolbar button flips to "Stop" as soon as the
    // request is in flight (before any result comes back).
    await page.getByRole("button", { name: "Run all" }).click();
    const stop = page.getByRole("button", { name: "Stop", exact: true });
    await expect(stop).toBeVisible({ timeout: 10000 });

    // A newly opened tab hydrates the active cell set from the runtime endpoint
    // even though it missed the SSE event that started this run.
    const observer = await page.context().newPage();
    await observer.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(observer.getByText("Cancel").first()).toBeVisible({ timeout: 15000 });
    const observerStop = observer.getByRole("button", { name: "Stop", exact: true });
    await expect(observerStop).toBeVisible({ timeout: 10000 });

    // Stop it. The request aborts, which cancels the server context and
    // interrupts the DuckDB statement; the UI returns to idle.
    await observerStop.click();
    await expect(observer.getByRole("button", { name: "Run all" })).toBeVisible({ timeout: 10000 });
    await expect(page.getByRole("button", { name: "Run all" })).toBeVisible({ timeout: 10000 });
    await observer.close();

    // Prove the interrupt reached the backend: the session lock is released, so
    // a fresh run succeeds quickly. If the heavy query were still holding the
    // session, this run could not open it and would time out.
    await setSql(request, liveApp.baseURL, notebook.id, heavyCell, "select 7 as ok");
    await page.reload();
    await expect(page.getByText("Cancel").first()).toBeVisible({ timeout: 15000 });
    const runResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === `/api/notebooks/${notebook.id}/run` &&
        response.request().method() === "POST" &&
        response.ok(),
      { timeout: 12000 },
    );
    await page.getByRole("button", { name: "Run all" }).click();
    await runResponse;
    await expect(page.getByText("7", { exact: true }).first()).toBeVisible({ timeout: 12000 });
  });
});
