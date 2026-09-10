import { expect, type Page } from "@playwright/test";
import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { liveTest as test, type LiveApp } from "../live-app-fixture";
import { storageTest, storageSecretChanges } from "../live-storage-app-fixture";
import type { WebNotebook } from "../../../lib/types";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
test.setTimeout(90000);

async function notebook(page: Page, app: LiveApp) {
  const response = await page.request.post(`${app.baseURL}/api/notebooks`, {
    data: { title: "Browser drops" },
  });
  expect(response.ok(), await response.text()).toBe(true);
  return (await response.json()).notebook as WebNotebook;
}
async function current(page: Page, app: LiveApp, id: string): Promise<WebNotebook> {
  return (await (await page.request.get(`${app.baseURL}/api/notebooks/${id}`)).json()).notebook;
}
async function openBrowser(page: Page, mobile: boolean) {
  await (
    mobile
      ? page.getByRole("tab", { name: "Data", exact: true })
      : page.getByRole("button", { name: "Data Browser", exact: true })
  ).click();
  return page.getByRole("textbox", { name: "Search data browser" });
}
async function choose(page: Page, label: string, position: string) {
  await page.getByRole("button", { name: `Add ${label} to notebook`, exact: true }).click();
  await page
    .locator(`[data-notebook-insertion-point="${position}"]`)
    .getByRole("button", { name: "Add source here", exact: true })
    .click();
}

test("reviews table drops, cancels without writing, inserts in order and runs only explicitly", async ({
  page,
  liveApp,
  isMobile,
}, info) => {
  for (const query of [
    "create schema if not exists raw",
    "create table raw.drop_orders as select 42::integer as order_id",
  ]) {
    const response = await page.request.post(`${liveApp.baseURL}/api/sql/query`, {
      data: { connection: "duckdb-default", environment: "default", query },
    });
    expect((await response.json()).status).toBe("ok");
  }
  const nb = await notebook(page, liveApp);
  const manifest = join(liveApp.workspaceDir, nb.path, "notebook.yml");
  const before = await readFile(manifest, "utf8");
  const errors: string[] = [],
    commands: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("request", (req) => {
    if (
      req.method() === "POST" &&
      /\/(run|preview|materialize|query)$/.test(new URL(req.url()).pathname)
    )
      commands.push(req.url());
  });
  await page.goto(`${liveApp.baseURL}/notebooks/${nb.id}`);
  let input = await openBrowser(page, isMobile);
  await input.fill("duckdb-default.local.raw.");
  await choose(page, "drop_orders", "start");
  const dialog = page.getByRole("dialog", { name: "Add source to notebook" });
  await expect(dialog.getByRole("button", { name: "Add source", exact: true })).toBeEnabled();
  expect(await readFile(manifest, "utf8")).toBe(before);
  expect(commands).toEqual([]);
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  expect(await readFile(manifest, "utf8")).toBe(before);
  if (isMobile) input = await openBrowser(page, true);
  await expect(input).toHaveValue("duckdb-default.local.raw.");
  if (isMobile) await choose(page, "drop_orders", `after:${nb.cells[0].cell_id}`);
  else {
    const row = page.locator(
      '[data-testid="data-browser-transfer-item"][data-transfer-label="drop_orders"]',
    );
    const target = page.locator(`[data-notebook-insertion-point="after:${nb.cells[0].cell_id}"]`);
    await target.scrollIntoViewIfNeeded();
    await row.dragTo(target);
  }
  await expect(dialog.getByRole("button", { name: "Add source", exact: true })).toBeEnabled();
  await dialog.getByLabel("Block name", { exact: true }).fill("imported_orders");
  await dialog.getByRole("radio", { name: "Sample", exact: true }).click();
  await dialog.getByLabel("Maximum rows", { exact: true }).fill("10");
  await dialog.getByRole("button", { name: "Review source", exact: true }).click();
  await expect(dialog.getByRole("button", { name: "Add source", exact: true })).toBeEnabled();
  await page.screenshot({ path: info.outputPath("source-review.png") });
  expect(await readFile(manifest, "utf8")).toBe(before);
  await dialog.getByRole("button", { name: "Add source", exact: true }).click();
  await expect(dialog).toBeHidden();
  const added = await current(page, liveApp, nb.id);
  const source = added.cells.find((cell) => cell.name === "imported_orders")!;
  expect(source.connection).toBe("duckdb-default");
  expect(source.content).toContain('select * from "local"."raw"."drop_orders"');
  expect(added.blocks.map((block) => block.cell)).toEqual([nb.cells[0].cell_id, source.cell_id]);
  expect(commands).toEqual([]);
  await expect(page.locator(`[data-notebook-cell-id="${source.cell_id}"]`)).toBeInViewport();
  await page.screenshot({ path: info.outputPath("source-inserted.png") });
  const run = await page.request.post(`${liveApp.baseURL}/api/notebooks/${nb.id}/run`, {
    data: { cells: [source.cell_id], environment: "default" },
  });
  expect(run.ok(), await run.text()).toBe(true);
  const results = (await run.json()).results;
  expect(
    results.find((result: { cell_id: string }) => result.cell_id === source.cell_id),
  ).toMatchObject({ status: "ok", columns: ["order_id"] });
  for (const reference of ['"local"."raw"."drop_orders"', '"raw"."drop_orders"']) {
    const response = await page.request.post(`${liveApp.baseURL}/api/sql/lsp/diagnostics`, {
      data: {
        asset_id: source.id,
        content: `select * from ${reference}`,
        connection: source.connection,
        environment: "default",
      },
    });
    expect(response.ok(), await response.text()).toBe(true);
    const { diagnostics } = await response.json();
    expect(
      (diagnostics ?? []).filter(
        (diagnostic: { code: string }) => diagnostic.code === "unresolved-relation",
      ),
    ).toEqual([]);
  }
  await page.waitForFunction(
    (id) =>
      (window as any).monaco?.editor
        .getModels()
        .some((model: any) => model.uri.toString().includes(`/notebook/${id}.`)),
    source.cell_id,
  );
  await expect
    .poll(() =>
      page.evaluate((id) => {
        const monaco = (window as any).monaco;
        const model = monaco.editor
          .getModels()
          .find((model: any) => model.uri.toString().includes(`/notebook/${id}.`));
        return monaco.editor
          .getModelMarkers({ resource: model.uri })
          .filter((marker: any) => (marker.code?.value ?? marker.code) === "unresolved-relation");
      }, source.cell_id),
    )
    .toEqual([]);
  expect(errors).toEqual([]);
});

test("cached table drops survive notebook autosaves and earlier source insertions", async ({
  page,
  liveApp,
  isMobile,
}) => {
  for (const query of [
    "create schema if not exists raw",
    "create table raw.first_source as select 1::integer as id",
    "create table raw.second_source as select 2::integer as id",
  ]) {
    const response = await page.request.post(`${liveApp.baseURL}/api/sql/query`, {
      data: { connection: "duckdb-default", environment: "default", query },
    });
    expect((await response.json()).status).toBe("ok");
  }
  const nb = await notebook(page, liveApp);
  await page.goto(`${liveApp.baseURL}/notebooks/${nb.id}`);
  const cellId = nb.cells[0].cell_id!;
  await page.waitForFunction(
    (id) =>
      (window as any).monaco?.editor
        .getModels()
        .some((model: any) => model.uri.toString().includes(`/notebook/${id}.`)),
    cellId,
  );
  const input = await openBrowser(page, isMobile);
  await input.fill("duckdb-default.local.raw.");
  await expect(
    page.getByRole("button", { name: "Add first_source to notebook", exact: true }),
  ).toBeVisible();
  // Finish a real editor autosave after the browser listed the tables. The
  // source reference must remain valid after crossing the notebook save barrier.
  const saved = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/cells/${cellId}`) && response.request().method() === "PUT",
  );
  await page.evaluate((id) => {
    const monaco = (window as any).monaco;
    const model = monaco.editor
      .getModels()
      .find((model: any) => model.uri.toString().includes(`/notebook/${id}.`));
    monaco.editor
      .getEditors()
      .find((editor: any) => editor.getModel() === model)
      .setValue("select 314 as saved_before_drop");
  }, cellId);
  expect((await saved).ok()).toBe(true);
  const dialog = page.getByRole("dialog", { name: "Add source to notebook" });
  for (const label of ["first_source", "second_source"]) {
    if (label === "second_source" && isMobile) await openBrowser(page, true);
    if (isMobile) await choose(page, label, `after:${cellId}`);
    else {
      const row = page.locator(
        `[data-testid="data-browser-transfer-item"][data-transfer-label="${label}"]`,
      );
      const target = page.locator(`[data-notebook-insertion-point="after:${cellId}"]`);
      await target.scrollIntoViewIfNeeded();
      await row.dragTo(target);
    }
    await expect(dialog.getByRole("button", { name: "Add source", exact: true })).toBeEnabled();
    await dialog.getByRole("button", { name: "Add source", exact: true }).click();
    await expect(dialog).toBeHidden();
  }
  const result = await current(page, liveApp, nb.id);
  expect(result.cells).toHaveLength(3);
  expect(result.cells.find((cell) => cell.cell_id === cellId)?.content).toContain(
    "saved_before_drop",
  );
  expect(result.cells.filter((cell) => cell.connection === "duckdb-default")).toHaveLength(2);
});

test("project file placement survives a new tab and rejects a notebook changed during review", async ({
  page,
  liveApp,
  isMobile,
  context,
}, info) => {
  await writeFile(join(liveApp.workspaceDir, "drop-file.csv"), "id,name\n1,Ada\n2,Linus\n");
  const nb = await notebook(page, liveApp);
  let expiredOnce = false;
  await page.route("**/api/data-browser/connections/*/children?*", async (route) => {
    if (!expiredOnce) {
      expiredOnce = true;
      await route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          status: "error",
          error: { code: "data_browser_revision_stale", message: "Refresh this data source." },
        }),
      });
    } else await route.continue();
  });
  await page.goto(`${liveApp.baseURL}/notebooks/${nb.id}`);
  let input = await openBrowser(page, isMobile);
  await input.fill("Project files.");
  await choose(page, "drop-file.csv", "start");
  expect(expiredOnce).toBe(true);
  const dialog = page.getByRole("dialog", { name: "Add source to notebook" });
  await expect(dialog.getByRole("button", { name: "Add source", exact: true })).toBeEnabled();
  const altered = await page.request.post(`${liveApp.baseURL}/api/notebooks/${nb.id}/cells`, {
    data: { name: "concurrent_edit" },
  });
  expect(altered.ok()).toBe(true);
  await dialog.getByRole("button", { name: "Add source", exact: true }).click();
  await expect(dialog.getByRole("alert")).toBeVisible();
  expect((await current(page, liveApp, nb.id)).cells).toHaveLength(2);
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  if (isMobile) input = await openBrowser(page, true);
  await input.fill("");
  await input.fill("Project files.");
  // Reopen from the URL to get a fresh browser token after the other edit.
  const second = await context.newPage();
  await second.goto(`${liveApp.baseURL}/notebooks/${nb.id}`);
  const secondInput = await openBrowser(second, isMobile);
  await secondInput.fill("Project files.");
  await choose(second, "drop-file.csv", "start");
  const secondDialog = second.getByRole("dialog", { name: "Add source to notebook" });
  await expect(secondDialog.getByRole("button", { name: "Add source", exact: true })).toBeEnabled();
  await secondDialog.getByRole("button", { name: "Add source", exact: true }).click();
  await expect(secondDialog).toBeHidden();
  const result = await current(second, liveApp, nb.id);
  const source = result.cells.find((cell) => cell.notebook_source)!;
  expect(source.notebook_source).toMatchObject({
    kind: "file",
    uri: "drop-file.csv",
    snapshot: { mode: "full" },
  });
  expect(result.blocks[0].cell).toBe(source.cell_id);
  const run = await second.request.post(`${liveApp.baseURL}/api/notebooks/${nb.id}/run`, {
    data: { cells: [source.cell_id], environment: "default" },
  });
  expect(run.ok(), await run.text()).toBe(true);
  expect((await run.json()).results).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ cell_id: source.cell_id, status: "ok", columns: ["id", "name"] }),
    ]),
  );
  await second.screenshot({ path: info.outputPath("file-source.png") });
  await second.close();
});

test("failed editor saves block source preparation without losing the draft", async ({
  page,
  liveApp,
  isMobile,
}) => {
  await writeFile(join(liveApp.workspaceDir, "blocked-drop.csv"), "id\n1\n");
  const nb = await notebook(page, liveApp);
  await page.goto(`${liveApp.baseURL}/notebooks/${nb.id}`);
  const cellId = nb.cells[0].cell_id!;
  await page.waitForFunction(
    (id) =>
      (window as any).monaco?.editor
        .getModels()
        .some((model: any) => model.uri.toString().includes(`/notebook/${id}.`)),
    cellId,
  );
  await page.route(`**/api/notebooks/${nb.id}/cells/${cellId}`, async (route) => {
    if (route.request().method() === "PUT")
      await route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({
          status: "error",
          error: { code: "notebook_edit_conflict", message: "Save blocked for regression test." },
        }),
      });
    else await route.continue();
  });
  const failedSave = page.waitForResponse(
    (response) =>
      response.url().endsWith(`/cells/${cellId}`) &&
      response.request().method() === "PUT" &&
      response.status() === 409,
  );
  await page.evaluate((id) => {
    const monaco = (window as any).monaco;
    const model = monaco.editor
      .getModels()
      .find((model: any) => model.uri.toString().includes(`/notebook/${id}.`));
    monaco.editor
      .getEditors()
      .find((editor: any) => editor.getModel() === model)
      .setValue("select 123456 as keep_this_draft");
  }, cellId);
  await failedSave;
  const prepared: string[] = [];
  page.on("request", (request) => {
    if (request.url().includes("/data-browser/prepare")) prepared.push(request.url());
  });
  const input = await openBrowser(page, isMobile);
  await input.fill("Project files.");
  await choose(page, "blocked-drop.csv", "start");
  const dialog = page.getByRole("dialog", { name: "Add source to notebook" });
  await expect(dialog.getByRole("alert")).toContainText("Could not save notebook changes");
  expect(prepared).toEqual([]);
  expect((await current(page, liveApp, nb.id)).cells).toHaveLength(1);
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  expect(
    await page.evaluate(
      (id) =>
        (window as any).monaco.editor
          .getModels()
          .find((model: any) => model.uri.toString().includes(`/notebook/${id}.`))
          .getValue(),
      cellId,
    ),
  ).toContain("keep_this_draft");
});

storageTest.describe("notebook object sources", () => {
  storageTest.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
  storageTest(
    "authors an S3 source without copying data and runs a typed snapshot explicitly",
    async ({ page, liveApp, isMobile, storage }, info) => {
      storageTest.setTimeout(90000);
      const created = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
        data: {
          name: "s3-notebook",
          type: "s3",
          environment_name: "default",
          values: { bucket_name: "browser", endpoint_url: `http://127.0.0.1:${storage.minioPort}` },
          secret_changes: storageSecretChanges("s3"),
        },
      });
      expect(created.ok(), await created.text()).toBe(true);
      const nb = await notebook(page, liveApp);
      await page.goto(`${liveApp.baseURL}/notebooks/${nb.id}`);
      const input = await openBrowser(page, isMobile);
      await input.fill("s3-notebook./incoming/");
      await choose(page, "orders.csv", "start");
      const dialog = page.getByRole("dialog", { name: "Add source to notebook" });
      await expect(dialog.getByRole("button", { name: "Add source", exact: true })).toBeEnabled();
      await dialog.getByRole("button", { name: "Add source", exact: true }).click();
      await expect(dialog).toBeHidden();
      const added = await current(page, liveApp, nb.id);
      const source = added.cells.find((cell) => cell.notebook_source)!;
      expect(source.notebook_source).toMatchObject({
        connection: "s3-notebook",
        uri: "s3://browser/incoming/orders.csv",
        snapshot: { mode: "full" },
      });
      const run = await page.request.post(`${liveApp.baseURL}/api/notebooks/${nb.id}/run`, {
        data: { cells: [source.cell_id], environment: "default" },
      });
      expect(run.ok(), await run.text()).toBe(true);
      expect((await run.json()).results).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ cell_id: source.cell_id, status: "ok" }),
        ]),
      );
      await page.screenshot({ path: info.outputPath("s3-source.png") });
    },
  );
});
