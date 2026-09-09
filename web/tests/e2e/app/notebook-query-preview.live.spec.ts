import { expect, type Page } from "@playwright/test";
import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

test("notebook expansion retains values and selection without running cells", async ({
  page,
  liveApp,
}, info) => {
  test.setTimeout(90000);
  const api = `${liveApp.baseURL}/api/notebooks`;
  const notebook = (
    await (await page.request.post(api, { data: { title: "Saved preview" } })).json()
  ).notebook;
  const updated = (
    await (
      await page.request.post(`${api}/${notebook.id}/cells`, { data: { name: "sample" } })
    ).json()
  ).notebook;
  const cellId = updated.cells.find((cell: { name: string }) => cell.name === "sample").cell_id;
  expect(
    (
      await page.request.put(`${api}/${notebook.id}/settings`, {
        data: { auto_recompute: false, environment: "default" },
      })
    ).ok(),
  ).toBeTruthy();
  expect(
    (
      await page.request.put(`${api}/${notebook.id}/cells/${cellId}`, {
        data: {
          content:
            "/* @bruin\ntype: duckdb.sql\n@bruin */\nselect range as id, random() as value from range(250)",
        },
      })
    ).ok(),
  ).toBeTruthy();
  const run = await page.request.post(`${api}/${notebook.id}/run`, {
    data: { cells: [cellId], environment: "default" },
  });
  expect(run.ok(), await run.text()).toBeTruthy();
  const initial = (await run.json()).results.find(
    (result: { cell_id: string }) => result.cell_id === cellId,
  );
  expect(initial.preview.continuation).toBe("snapshot");
  const writes: string[] = [],
    limits: number[] = [],
    errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("request", (request) => {
    if (/\/run(?:\?|$)|\/materialize|\/import/.test(request.url()) && request.method() === "POST")
      writes.push(request.url());
    if (request.url().endsWith(`/cells/${cellId}/preview`))
      limits.push(request.postDataJSON().limit);
  });
  let failNext = false;
  await page.route(`**/cells/${cellId}/preview`, async (route) => {
    if (failNext) {
      failNext = false;
      await route.fulfill({
        status: 503,
        json: { error: { message: "Temporary saved preview failure" } },
      });
    } else await route.continue();
  });
  await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
  const card = page.locator(`[data-notebook-cell-id="${cellId}"]`);
  const grid = card.getByRole("grid", { name: "sample result preview" });
  await expect(grid).toHaveAttribute("aria-rowcount", "101", { timeout: 20000 });
  const first = grid.getByRole("button", { name: "id, row 1: 0", exact: true });
  await first.click();
  const more = card.getByRole("button", { name: "Load more rows", exact: true });
  await more.focus();
  await page.keyboard.press("Enter");
  await expect(grid).toHaveAttribute("aria-rowcount", "201");
  await expect(grid.locator('[aria-selected="true"]')).not.toHaveCount(0);
  await expect(
    grid.getByRole("button", { name: `value, row 1: ${initial.rows[0][1]}`, exact: true }),
  ).toBeVisible();
  failNext = true;
  await more.click();
  await expect(card.getByRole("alert")).toContainText("Temporary saved preview failure");
  await expect(grid).toHaveAttribute("aria-rowcount", "201");
  await more.click();
  await expect(grid).toHaveAttribute("aria-rowcount", "251");
  await expect(more).toHaveCount(0);
  await expect(card.getByRole("status")).toContainText("Showing 250 rows · all rows");
  expect(limits).toEqual([200, 250, 250]);
  expect(writes).toEqual([]);
  expect(errors).toEqual([]);
  expect(await grid.locator("[data-row-index]").count()).toBeLessThan(50);
  await card.screenshot({ path: info.outputPath("notebook-preview.png") });
  await page.request.post(`${api}/${notebook.id}/run`, {
    data: { cells: [cellId], environment: "default" },
  });
  const expired = await page.request.post(`${api}/${notebook.id}/cells/${cellId}/preview`, {
    data: { result_id: initial.preview.result_id, environment: "default", limit: 200 },
  });
  expect(expired.status()).toBe(409);
});

async function setQuery(page: Page, query: string) {
  await page.evaluate((value) => {
    const monaco = (window as typeof window & { monaco?: any }).monaco;
    const model = monaco?.editor
      .getModels()
      .find((candidate: any) => candidate.uri.toString().includes("/adhoc/"));
    if (!model) throw new Error("ad hoc editor not mounted");
    model.setValue(value);
  }, query);
}

test("query expansion uses the executed SQL, retries safely, and keeps authored limits", async ({
  page,
  liveApp,
}, info) => {
  test.setTimeout(90000);
  const pipelineId = Buffer.from("analytics").toString("base64url");
  const assetId = Buffer.from("analytics/assets/analytics/customers.sql").toString("base64url");
  const executions: string[] = [],
    previews: string[] = [],
    errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("request", (request) => {
    if (request.url().endsWith("/sql/query")) executions.push(request.postDataJSON().query);
    if (request.url().endsWith("/sql/preview")) previews.push(request.postDataJSON().query);
  });
  let failNext = false;
  await page.route("**/sql/preview", async (route) => {
    if (failNext) {
      failNext = false;
      await route.fulfill({
        status: 503,
        json: { error: { message: "Temporary query preview failure" } },
      });
    } else await route.continue();
  });
  await page.goto(
    `${liveApp.baseURL}/pipelines/${pipelineId}/assets/${assetId}/code?editor=adhoc&result=query`,
  );
  await expect(page.getByTestId("adhoc-editor-workspace").locator(".monaco-editor")).toBeVisible({
    timeout: 20000,
  });
  const query = "select range as id from range(1500) limit 750; -- preserve this bound";
  await setQuery(page, query);
  await page.getByTitle("Run (⌘ + ↵)").click();
  const panel = page.getByRole("tabpanel", { name: "Query", exact: true });
  const grid = panel.getByRole("grid");
  await expect(grid).toHaveAttribute("aria-rowcount", "501", { timeout: 20000 });
  await setQuery(page, "create table must_not_exist as select 1");
  failNext = true;
  const more = panel.getByRole("button", { name: "Load more rows", exact: true });
  await more.click();
  await expect(panel.getByRole("alert")).toContainText("Temporary query preview failure");
  await expect(grid).toHaveAttribute("aria-rowcount", "501");
  await more.click();
  await expect(grid).toHaveAttribute("aria-rowcount", "751");
  await expect(more).toHaveCount(0);
  await expect(panel.getByRole("status")).toContainText("Showing 750 rows · all rows");
  expect(executions).toEqual([query]);
  expect(previews).toEqual([query, query]);
  expect(errors).toEqual([]);
  const rejected = await page.request.post(`${liveApp.baseURL}/api/sql/preview`, {
    data: {
      connection: "duckdb-default",
      query: "create table must_not_exist as select 1",
      limit: 100,
    },
  });
  expect((await rejected.json()).status).toBe("error");
  await panel.screenshot({ path: info.outputPath("query-preview.png") });

  // A late continuation cannot overwrite a newer explicit Run.
  await setQuery(page, "select range as id from range(1500)");
  await page.getByTitle("Run (⌘ + ↵)").click();
  await expect(grid).toHaveAttribute("aria-rowcount", "501");
  let release!: () => void;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  let received = false;
  await page.route("**/sql/preview", async (route) => {
    received = true;
    await held;
    await route
      .fulfill({ json: { status: "ok", columns: ["obsolete"], rows: [{ obsolete: 0 }] } })
      .catch(() => undefined);
  });
  await more.click();
  await expect.poll(() => received).toBe(true);
  await setQuery(page, "select 99 as replacement");
  await page.getByTitle("Run (⌘ + ↵)").click();
  await expect(grid).toHaveAttribute("aria-rowcount", "2");
  release();
  await expect(
    grid.getByRole("button", { name: "replacement, row 1: 99", exact: true }),
  ).toBeVisible();
  await expect(panel.getByRole("alert")).toHaveCount(0);
  expect(errors).toEqual([]);
});
