import { expect, type APIRequestContext, type Locator, type Page } from "@playwright/test";

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

async function setAutoRecompute(
  request: APIRequestContext,
  baseURL: string,
  notebookId: string,
  enabled: boolean,
) {
  const response = await request.put(`${baseURL}/api/notebooks/${notebookId}/settings`, {
    data: { auto_recompute: enabled },
  });
  expect(response.ok()).toBe(true);
}

function resultCell(card: Locator, column: string, row: number, value: string) {
  return card.getByRole("button", {
    name: `${column}, row ${row}: ${value}`,
    exact: true,
  });
}

function notebookCell(page: Page, cellId: string) {
  return page.locator(`[data-notebook-cell-id="${cellId}"]`);
}

async function expectNotebookStaleCount(page: Page, count: number) {
  const badge = page.getByText(`${count} stale`, { exact: true });
  await expect(badge).toHaveCount(1, { timeout: 15000 });
  if ((page.viewportSize()?.width ?? 1280) >= 640) {
    await expect(badge).toBeVisible();
  }
}

async function expectNoNotebookStaleCount(page: Page) {
  await expect(page.getByText(/\d+ stale/)).toHaveCount(0, { timeout: 15000 });
}

async function replaceEditorContent(page: Page, card: Locator, content: string) {
  // Click the rendered code line, not Monaco's outer shell (whose center can
  // be blank) or its intentionally zero-width native input proxy.
  await card.locator(".monaco-editor .view-line").first().click();
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type(content);
}

test.describe("notebook auto-recompute", () => {
  test.use({ fixtureName: "configured-workspace" });

  test("an edited source cell updates its own table output", async ({ liveApp, page }) => {
    const { request } = page;
    const notebook = await createNotebook(request, liveApp.baseURL, "AutoSelf");
    const srcCell = await addCell(request, liveApp.baseURL, notebook.id, "src");
    await setSql(request, liveApp.baseURL, notebook.id, srcCell, "select 111 as n");

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("AutoSelf").first()).toBeVisible({ timeout: 15000 });

    // The cell auto-computes from the API save; wait for the baseline.
    const srcCard = notebookCell(page, srcCell);
    await expect(resultCell(srcCard, "n", 1, "111")).toBeVisible({
      timeout: 20000,
    });

    // Edit the cell itself and blur. Its own output table must update — no manual
    // run, no downstream involved.
    await replaceEditorContent(page, srcCard, "select 222 as n");
    await page.getByText("AutoSelf").first().click(); // blur → save → recompute

    await expect(resultCell(srcCard, "n", 1, "222")).toBeVisible({
      timeout: 20000,
    });
    await expect(resultCell(srcCard, "n", 1, "111")).toBeHidden({
      timeout: 20000,
    });
  });

  test("a UNION (read-only compound) cell auto-recomputes", async ({ liveApp, page }) => {
    const { request } = page;
    const notebook = await createNotebook(request, liveApp.baseURL, "AutoUnion");
    const unionCell = await addCell(request, liveApp.baseURL, notebook.id, "u");
    // A set operation is read-only but not a "single SELECT" — it must still
    // auto-recompute (regression: UNION cells were stuck stale and never ran).
    await setSql(
      request,
      liveApp.baseURL,
      notebook.id,
      unionCell,
      "select 111 as n union all select 222",
    );

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("AutoUnion").first()).toBeVisible({ timeout: 15000 });
    const card = notebookCell(page, unionCell);
    await expect(resultCell(card, "n", 1, "111")).toBeVisible({
      timeout: 20000,
    });
    await replaceEditorContent(page, card, "select 333 as n union all select 444");
    await page.getByText("AutoUnion").first().click(); // blur → save → recompute

    await expect(resultCell(card, "n", 1, "333")).toBeVisible({
      timeout: 20000,
    });
    await expect(resultCell(card, "n", 2, "444")).toBeVisible({
      timeout: 20000,
    });
    await expect(resultCell(card, "n", 1, "111")).toBeHidden({
      timeout: 20000,
    });
    // It is not flagged stale — auto-recompute handled it.
    await expectNoNotebookStaleCount(page);
  });

  test("editing an upstream auto-recomputes clean SELECT descendants", async ({
    liveApp,
    page,
  }) => {
    const { request } = page;
    const notebook = await createNotebook(request, liveApp.baseURL, "Auto");
    // Author the whole graph before starting its initial recompute. Under CI
    // load, letting the base cell run while the dependent is still being added
    // can leave the setup waiting on an intermediate result.
    await setAutoRecompute(request, liveApp.baseURL, notebook.id, false);
    const baseCell = await addCell(request, liveApp.baseURL, notebook.id, "base");
    await setSql(request, liveApp.baseURL, notebook.id, baseCell, "select 10 as amount");
    const doubledCell = await addCell(request, liveApp.baseURL, notebook.id, "doubled");
    await setSql(
      request,
      liveApp.baseURL,
      notebook.id,
      doubledCell,
      "select amount * 2 as doubled from base",
    );
    await setAutoRecompute(request, liveApp.baseURL, notebook.id, true);

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Auto").first()).toBeVisible({ timeout: 15000 });

    // The cells auto-compute from the API saves; wait for the server baseline.
    await expect(page.getByText("20", { exact: true }).first()).toBeVisible({ timeout: 20000 });

    // Edit the upstream cell in the editor (which marks base + doubled stale)
    // and then click away. No run button is pressed — the server recomputes the
    // chain and streams the new results back over SSE.
    const baseCard = notebookCell(page, baseCell);
    await replaceEditorContent(page, baseCard, "select 21 as amount");
    await page.getByText("Auto").first().click(); // blur the editor → save → stale

    // The downstream cell recomputes on its own: doubled becomes 42.
    await expect(page.getByText("42", { exact: true }).first()).toBeVisible({ timeout: 20000 });
    // And the stale banner clears once everything is recomputed.
    await expectNoNotebookStaleCount(page);
  });

  test("typing into an upstream auto-recomputes without leaving the editor", async ({
    liveApp,
    page,
  }) => {
    const { request } = page;
    const notebook = await createNotebook(request, liveApp.baseURL, "AutoType");
    await setAutoRecompute(request, liveApp.baseURL, notebook.id, false);
    const baseCell = await addCell(request, liveApp.baseURL, notebook.id, "base");
    await setSql(request, liveApp.baseURL, notebook.id, baseCell, "select 10 as amount");
    const doubledCell = await addCell(request, liveApp.baseURL, notebook.id, "doubled");
    await setSql(
      request,
      liveApp.baseURL,
      notebook.id,
      doubledCell,
      "select amount * 2 as doubled from base",
    );
    await setAutoRecompute(request, liveApp.baseURL, notebook.id, true);

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("AutoType").first()).toBeVisible({ timeout: 15000 });

    // The cells auto-compute from the API saves; wait for the server baseline.
    await expect(page.getByText("20", { exact: true }).first()).toBeVisible({ timeout: 20000 });

    // Edit the upstream and keep the caret in the editor — no blur, no run
    // button. The debounced auto-commit saves the draft on its own, which marks
    // the cells stale and lets auto-recompute pick up the chain.
    const baseCard = notebookCell(page, baseCell);
    await replaceEditorContent(page, baseCard, "select 21 as amount");

    // The downstream recomputes to 42 without the editor ever losing focus.
    await expect(page.getByText("42", { exact: true }).first()).toBeVisible({ timeout: 20000 });
    await expectNoNotebookStaleCount(page);
  });

  test("a breaking upstream column rename does not auto-recompute the downstream", async ({
    liveApp,
    page,
  }) => {
    const { request } = page;
    const notebook = await createNotebook(request, liveApp.baseURL, "AutoBreak");
    await setAutoRecompute(request, liveApp.baseURL, notebook.id, false);
    const baseCell = await addCell(request, liveApp.baseURL, notebook.id, "base");
    await setSql(request, liveApp.baseURL, notebook.id, baseCell, "select 10 as amount");
    const doubledCell = await addCell(request, liveApp.baseURL, notebook.id, "doubled");
    await setSql(
      request,
      liveApp.baseURL,
      notebook.id,
      doubledCell,
      "select amount * 2 as doubled from base",
    );
    await setAutoRecompute(request, liveApp.baseURL, notebook.id, true);

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("AutoBreak").first()).toBeVisible({ timeout: 15000 });

    // The cells auto-compute from the API saves; wait for the server baseline.
    await expect(page.getByText("20", { exact: true }).first()).toBeVisible({ timeout: 20000 });

    // Rename the upstream column amount -> renamed. The upstream is still a clean
    // SELECT (auto-recomputes fine), but it breaks `doubled`, which references
    // `amount`. The downstream must NOT be recomputed: once `base` reruns and
    // the server re-validates `doubled` against the new schema, the broken column
    // reference is detected and the downstream is held back, not run into failure.
    const baseCard = notebookCell(page, baseCell);
    await replaceEditorContent(page, baseCard, "select 100 as renamed");
    await page.getByText("AutoBreak").first().click(); // blur → save → stale

    // The upstream auto-recomputes to 100.
    await expect(page.getByText("100", { exact: true }).first()).toBeVisible({ timeout: 15000 });
    // Give auto-recompute ample time to (wrongly) fire on the downstream.
    await page.waitForTimeout(3000);

    // The downstream was never recomputed: it stays stale, still showing its old
    // output (20), and never an error from running the broken column reference.
    await expectNotebookStaleCount(page, 1);
    await expect(page.getByText("20", { exact: true }).first()).toBeVisible();

    // The stale visual is shown only on the cell that genuinely won't refresh on
    // its own: `doubled` (broken) gets the hatched header; `base`, which
    // auto-recomputed, does not.
    const doubledHeader = notebookCell(page, doubledCell)
      .locator('[data-slot="delimited-card-header"]')
      .first();
    const baseHeader = baseCard.locator('[data-slot="delimited-card-header"]').first();
    await expect(doubledHeader).toHaveClass(/notebook-stale-hatch/);
    await expect(baseHeader).not.toHaveClass(/notebook-stale-hatch/);
  });

  test("a SQL error blocks auto-recompute (stays stale)", async ({ liveApp, page }) => {
    const { request } = page;
    const notebook = await createNotebook(request, liveApp.baseURL, "AutoErr");
    const baseCell = await addCell(request, liveApp.baseURL, notebook.id, "base");
    await setSql(request, liveApp.baseURL, notebook.id, baseCell, "select 10 as amount");

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("AutoErr").first()).toBeVisible({ timeout: 15000 });
    // The cells auto-compute from the API saves; wait for the server baseline.
    await expect(page.getByText("10", { exact: true }).first()).toBeVisible({ timeout: 20000 });

    // Replace the body with SQL the parser rejects.
    const baseCard = notebookCell(page, baseCell);
    await replaceEditorContent(page, baseCard, "select fr0m where");
    await page.getByText("AutoErr").first().click(); // blur → save → stale

    // The cell stays stale and keeps the hatched header (the server won't
    // auto-run an errored SELECT), and its old output (10) is unchanged.
    await expectNotebookStaleCount(page, 1);
    const baseHeader = baseCard.locator('[data-slot="delimited-card-header"]').first();
    await expect(baseHeader).toHaveClass(/notebook-stale-hatch/, { timeout: 15000 });
    await page.waitForTimeout(3000);
    await expect(page.getByText("10", { exact: true }).first()).toBeVisible();
  });
});
