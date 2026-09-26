import { expect, type APIRequestContext } from "@playwright/test";
import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

async function createCell(request: APIRequestContext, api: string, name: string, sql: string) {
  const response = await request.post(`${api}/cells`, { data: { name } });
  expect(response.ok(), await response.text()).toBe(true);
  const cell = (await response.json()).notebook.cells.find(
    (item: { name: string }) => item.name === name,
  );
  const saved = await request.put(`${api}/cells/${cell.cell_id}`, {
    data: { content: `/* @bruin\ntype: duckdb.sql\n@bruin */\n${sql}\n` },
  });
  expect(saved.ok(), await saved.text()).toBe(true);
  return cell.cell_id as string;
}

test("notebook charts honor saved row limits and preview tables share one selection", async ({
  page,
  liveApp,
  isMobile,
}, info) => {
  test.setTimeout(120000);
  const created = await page.request.post(`${liveApp.baseURL}/api/notebooks`, {
    data: { title: "Notebook polish" },
  });
  expect(created.ok()).toBe(true);
  const notebook = (await created.json()).notebook;
  const api = `${liveApp.baseURL}/api/notebooks/${notebook.id}`;
  await page.request.put(`${api}/settings`, {
    data: { auto_recompute: false, environment: "default" },
  });
  const sampleId = await createCell(
    page.request,
    api,
    "sample",
    "select 'Long category label ' || (range % 18)::varchar as category, range as value from range(518)",
  );
  const otherId = await createCell(
    page.request,
    api,
    "other",
    "select 'Other' as category, 42 as value",
  );
  const run = await page.request.post(`${api}/run`, {
    data: { cells: [sampleId, otherId], environment: "default" },
  });
  expect(run.ok(), await run.text()).toBe(true);
  const initial = (await run.json()).results.find(
    (result: { cell_id: string }) => result.cell_id === sampleId,
  );
  expect(initial.rows).toHaveLength(100);
  expect(initial.total_rows).toBe(518);
  const current = (await (await page.request.get(api)).json()).notebook;
  const prepare = await page.request.post(`${api}/changes/prepare`, {
    data: {
      base_revision: current.revision,
      operations: [
        {
          kind: "visualization.create",
          position: "end",
          visualization: {
            id: "",
            source: sampleId,
            definition: {
              version: 1,
              type: "scatter",
              title: "Categories and values",
              encoding: { x: { field: "category" }, y: [{ field: "value" }] },
              presentation_limit: 1000,
            },
          },
        },
        {
          kind: "visualization.create",
          position: "end",
          visualization: {
            id: "",
            source: sampleId,
            definition: {
              version: 1,
              type: "line",
              title: "Long legend",
              encoding: {
                x: { field: "value" },
                y: [{ field: "value" }],
                series: { field: "category" },
              },
              show_legend: true,
              presentation_limit: 1000,
            },
          },
        },
      ],
    },
  });
  expect(prepare.ok(), await prepare.text()).toBe(true);
  const plan = await prepare.json();
  expect(plan.can_apply, JSON.stringify(plan.blocking_problems)).toBe(true);
  const applied = await page.request.post(`${api}/changes/apply`, { data: plan.change_set });
  expect(applied.ok(), await applied.text()).toBe(true);
  const limits: number[] = [],
    writes: string[] = [],
    errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("request", (request) => {
    if (request.url().endsWith(`/cells/${sampleId}/preview`))
      limits.push(request.postDataJSON().limit);
    if (request.method() === "POST" && /\/(run|materialize|import)(?:\?|$)/.test(request.url()))
      writes.push(request.url());
  });
  await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
  const chart = page.getByRole("figure", { name: /Categories and values/ });
  await expect(chart).toBeVisible({ timeout: 20000 });
  await expect.poll(() => limits).toContain(1000);
  await expect(chart.locator(".recharts-scatter-symbol")).toHaveCount(518);
  await expect(chart).not.toContainText("Previewing 100");
  const legend = page.getByRole("list", { name: "Chart legend" });
  await expect(legend.getByRole("listitem")).toHaveCount(18);
  await expect.poll(() => legend.evaluate((el) => el.clientHeight)).toBeLessThanOrEqual(80);
  expect(await legend.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true);

  const sample = page.getByRole("grid", { name: "sample result preview" });
  const other = page.getByRole("grid", { name: "other result preview" });
  await expect(sample).toHaveAttribute("aria-rowcount", "101");
  expect(await sample.evaluate((el) => getComputedStyle(el).fontFamily)).toMatch(
    /mono|Inconsolata/i,
  );
  await sample.getByRole("button", { name: "Select row 1", exact: true }).click();
  await expect(sample.locator('td[aria-selected="true"]')).toHaveCount(2);
  if (!isMobile) {
    const start = await sample
      .getByRole("button", { name: "Select row 1", exact: true })
      .boundingBox();
    const end = await sample
      .getByRole("button", { name: "Select row 3", exact: true })
      .boundingBox();
    if (!start || !end) throw new Error("Row handles not visible");
    await page.mouse.move(start.x + start.width / 2, start.y + start.height / 2);
    await page.mouse.down();
    await page.mouse.move(end.x + end.width / 2, end.y + end.height / 2, { steps: 8 });
    await page.mouse.up();
    await expect(sample.locator('td[aria-selected="true"]')).toHaveCount(6);
  }
  await other.getByRole("button", { name: "Select row 1", exact: true }).click();
  await expect(other.locator('td[aria-selected="true"]')).toHaveCount(2);
  await expect(sample.locator('td[aria-selected="true"]')).toHaveCount(0);
  // A harmless pointer click outside the tables clears the active table selection.
  await page
    .getByRole("tablist", { name: "Open authoring documents" })
    .click({ position: { x: 5, y: 5 } });
  await expect(other.locator('td[aria-selected="true"]')).toHaveCount(0);
  await sample.scrollIntoViewIfNeeded();
  await sample.evaluate((el) => {
    const viewport = el.closest('[data-slot="scroll-area-viewport"]') as HTMLElement;
    viewport.scrollTop = viewport.scrollHeight;
    viewport.dispatchEvent(new Event("scroll", { bubbles: true }));
  });
  await expect(sample).toHaveAttribute("aria-rowcount", "201");
  const cell = page.locator(`[data-notebook-cell-id="${sampleId}"]`);
  const collapse = cell.getByRole("button", { name: "Collapse sample result table" });
  const more = cell.getByRole("button", { name: "Load more rows", exact: true });
  if (!isMobile) {
    const [left, right] = await Promise.all([collapse.boundingBox(), more.boundingBox()]);
    expect(Math.abs(left!.y - right!.y)).toBeLessThan(8);
  }
  await collapse.click();
  await expect(sample).toBeHidden();
  await cell.getByRole("button", { name: "Expand sample result table" }).click();
  await expect(sample).toBeVisible();
  expect(writes).toEqual([]);
  expect(errors).toEqual([]);
  await chart.scrollIntoViewIfNeeded();
  await page.screenshot({ path: info.outputPath("categorical-scatter.png") });
  if ((page.viewportSize()?.width ?? 0) < 1280)
    await page.getByRole("tab", { name: "Notebooks", exact: true }).click();
  await page.getByRole("tab", { name: "Add", exact: true }).click();
  const sqlTile = page.locator('[data-authoring-icon-tone="sql"]').first();
  const pythonTile = page.locator('[data-authoring-icon-tone="python"]').first();
  await expect(sqlTile).toBeVisible();
  expect(await sqlTile.evaluate((el) => getComputedStyle(el).color)).not.toBe(
    await pythonTile.evaluate((el) => getComputedStyle(el).color),
  );
  await page.screenshot({ path: info.outputPath("quiet-tiles-light.png") });
  await page.evaluate(() => document.documentElement.classList.add("dark"));
  await page.screenshot({ path: info.outputPath("quiet-tiles-dark.png") });
});

test("document tabs can be reordered with keyboard and drag and retain order", async ({
  page,
  liveApp,
  isMobile,
}) => {
  test.setTimeout(90000);
  const ids: string[] = [];
  for (const title of ["First notebook", "Second notebook", "Third notebook"]) {
    const response = await page.request.post(`${liveApp.baseURL}/api/notebooks`, {
      data: { title },
    });
    expect(response.ok()).toBe(true);
    ids.push((await response.json()).notebook.id);
  }
  for (const id of ids) {
    await page.goto(`${liveApp.baseURL}/notebooks/${id}`);
    await expect(page.getByRole("tablist", { name: "Open authoring documents" })).toBeVisible();
  }
  const tabs = page.getByRole("tablist", { name: "Open authoring documents" });
  const names = () => tabs.getByRole("tab").allTextContents();
  await expect.poll(names).toEqual(["First notebook", "Second notebook", "Third notebook"]);
  await tabs.getByRole("tab", { name: "Third notebook", exact: true }).press("Alt+Shift+ArrowLeft");
  await expect.poll(names).toEqual(["First notebook", "Third notebook", "Second notebook"]);
  if (!isMobile) {
    const transfer = await page.evaluateHandle(() => new DataTransfer());
    const source = tabs.getByRole("tab", { name: "Second notebook", exact: true }).locator("..");
    const target = tabs.getByRole("tab", { name: "First notebook", exact: true }).locator("..");
    await source.dispatchEvent("dragstart", { dataTransfer: transfer });
    await target.dispatchEvent("dragover", { dataTransfer: transfer });
    await target.dispatchEvent("drop", { dataTransfer: transfer });
    await source.dispatchEvent("dragend", { dataTransfer: transfer });
    await transfer.dispose();
    await expect.poll(names).toEqual(["Second notebook", "First notebook", "Third notebook"]);
  }
  const before = await names();
  await page.reload();
  await expect.poll(names).toEqual(before);
});

test("dashboard view respects the same saved grid as the editor", async ({
  page,
  liveApp,
  isMobile,
}, info) => {
  test.setTimeout(90000);
  if (!isMobile) await page.setViewportSize({ width: 1600, height: 1000 });
  const created = await page.request.post(`${liveApp.baseURL}/api/presentations`, {
    data: { kind: "dashboard", title: "Layout check" },
  });
  expect(created.ok()).toBe(true);
  const artifact = (await created.json()).document.artifact;
  // Sanitized reproduction of the growth dashboard: authored array order differs from placement.
  const definition = {
    version: 1,
    id: "layout_check",
    title: "Layout check",
    datasets: {
      values: {
        connection: "duckdb-default",
        query: "SELECT 42 AS value",
        columns: [{ name: "value", type: "integer" }],
      },
    },
    visualizations: ["last", "right", "left"].map((id) => ({
      id,
      dataset: "values",
      definition: { version: 1, type: "kpi", title: id, value: { field: "value" } },
    })),
    layout: [
      { visualization: "left", x: 0, y: 0, width: 6, height: 2 },
      { visualization: "right", x: 6, y: 0, width: 6, height: 2 },
      { visualization: "last", x: 0, y: 2, width: 12, height: 4 },
    ],
  };
  const saved = await page.request.put(
    `${liveApp.baseURL}/api/presentations/${artifact.workspace_id}`,
    { data: { expected_revision: artifact.revision, content: JSON.stringify(definition) } },
  );
  expect(saved.ok(), await saved.text()).toBe(true);
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  for (const suffix of ["", "/view"]) {
    await page.goto(`${liveApp.baseURL}/dashboards/${artifact.workspace_id}${suffix}`);
    const left = page.getByTestId("presentation-visualization-left");
    const right = page.getByTestId("presentation-visualization-right");
    const last = page.getByTestId("presentation-visualization-last");
    await expect(left).toBeVisible({ timeout: 20000 });
    await expect(left.getByText("42", { exact: true })).toBeVisible({ timeout: 20000 });
    await expect
      .poll(async () => {
        const l = await left.boundingBox(),
          r = await right.boundingBox(),
          b = await last.boundingBox();
        return Boolean(
          l &&
          r &&
          b &&
          (isMobile ? r.y > l.y && b.y > r.y : Math.abs(l.y - r.y) < 3 && b.y > l.y + l.height - 3),
        );
      })
      .toBe(true);
    const [l, r, b] = await Promise.all([
      left.boundingBox(),
      right.boundingBox(),
      last.boundingBox(),
    ]);
    if (!isMobile) {
      expect(Math.abs(l!.y - r!.y)).toBeLessThan(3);
      expect(r!.x).toBeGreaterThan(l!.x + l!.width - 3);
      expect(b!.y).toBeGreaterThan(l!.y + l!.height - 3);
      expect(Math.abs(l!.height - 156)).toBeLessThan(5);
    } else {
      expect(r!.y).toBeGreaterThan(l!.y);
      expect(b!.y).toBeGreaterThan(r!.y);
    }
    await page.screenshot({
      path: info.outputPath(suffix ? "dashboard-view.png" : "dashboard-edit.png"),
    });
  }
  expect(errors).toEqual([]);
});
