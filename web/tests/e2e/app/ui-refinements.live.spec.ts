import { expect, type Locator, type Page } from "@playwright/test";
import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
test.setTimeout(90000);
const pipelineId = Buffer.from("analytics").toString("base64url");

async function dragHandle(page: Page, handle: Locator, target: Locator, touch: boolean) {
  const from = await handle.boundingBox();
  const to = await target.boundingBox();
  expect(from).not.toBeNull();
  expect(to).not.toBeNull();
  const start = { x: from!.x + from!.width / 2, y: from!.y + from!.height / 2 };
  const end = { x: to!.x + to!.width / 2, y: to!.y + to!.height / 2 };
  if (touch) {
    const client = await page.context().newCDPSession(page);
    await client.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [start] });
    for (let step = 1; step <= 8; step++) {
      await client.send("Input.dispatchTouchEvent", {
        type: "touchMove",
        touchPoints: [
          {
            x: start.x + ((end.x - start.x) * step) / 8,
            y: start.y + ((end.y - start.y) * step) / 8,
          },
        ],
      });
    }
    await client.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
    await client.detach();
  } else {
    await page.mouse.move(start.x, start.y);
    await page.mouse.down();
    await page.mouse.move(end.x, end.y, { steps: 8 });
    await page.mouse.up();
  }
}

test("resizes preview selections with mouse or touch and opens full values without querying", async ({
  page,
  liveApp,
}, info) => {
  const api = `${liveApp.baseURL}/api/notebooks`;
  const notebook = (
    await (await page.request.post(api, { data: { title: "Selection review" } })).json()
  ).notebook;
  const updated = (
    await (
      await page.request.post(`${api}/${notebook.id}/cells`, { data: { name: "sample" } })
    ).json()
  ).notebook;
  const cellId = updated.cells.find((cell: { name: string }) => cell.name === "sample").cell_id;
  await page.request.put(`${api}/${notebook.id}/settings`, {
    data: { auto_recompute: false, environment: "default" },
  });
  await page.request.put(`${api}/${notebook.id}/cells/${cellId}`, {
    data: {
      content:
        "/* @bruin\ntype: duckdb.sql\n@bruin */\nselect range as id, 'value-' || range as label from range(250)",
    },
  });
  const run = await page.request.post(`${api}/${notebook.id}/run`, {
    data: { cells: [cellId], environment: "default" },
  });
  expect(run.ok(), await run.text()).toBeTruthy();
  const queries: string[] = [],
    errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("request", (request) => {
    if (request.method() === "POST" && /\/run$|\/preview$|\/sql\/query/.test(request.url()))
      queries.push(request.url());
  });
  await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
  const card = page.locator(`[data-notebook-cell-id="${cellId}"]`);
  const grid = card.getByRole("grid", { name: "sample result preview" });
  const cell = (row: number, column = 0) =>
    grid.locator(`[data-grid-row-index="${row}"][data-grid-column-index="${column}"]`);
  await cell(0).click();
  const end = card.getByRole("button", { name: "Resize selection end", exact: true });
  await expect(end).toBeVisible();
  // A tap on a handle must not grow the range by one row/column.
  await end.click();
  await expect(grid.locator('td[aria-selected="true"]')).toHaveCount(1);
  const mobile = info.project.name.includes("mobile");
  if (mobile) expect((await end.boundingBox())!.width).toBeGreaterThanOrEqual(44);
  await dragHandle(page, end, cell(2), mobile);
  await expect(grid.locator('td[aria-selected="true"]')).toHaveCount(3);
  await dragHandle(page, end, cell(1), mobile);
  await expect(grid.locator('td[aria-selected="true"]')).toHaveCount(2);
  await expect(card.getByRole("button", { name: /Adjust selection/ })).toHaveCount(0);
  await card.screenshot({ path: info.outputPath("selection-handles.png") });
  await card.getByRole("button", { name: "View selection full screen" }).click();
  const dialog = page.getByRole("dialog", { name: "Selected cells" });
  await expect(dialog.getByRole("table")).toBeVisible();
  await expect
    .poll(async () => (await dialog.boundingBox())!.width)
    .toBeGreaterThanOrEqual(page.viewportSize()!.width - 2);
  await expect
    .poll(async () => (await dialog.boundingBox())!.height)
    .toBeGreaterThanOrEqual(page.viewportSize()!.height - 2);
  await dialog.getByRole("button", { name: "Close", exact: true }).click();
  await expect(grid.locator('td[aria-selected="true"]')).toHaveCount(2);
  // A sparse selection retains its holes in the viewer and exposes no rectangle handles.
  await cell(0).click();
  await cell(2, 1).click({ modifiers: ["Control"] });
  await expect(end).toHaveCount(0);
  await card.getByRole("button", { name: "View selection full screen" }).click();
  const values = dialog.getByRole("table");
  await expect(values).toContainText("value-2");
  await expect(values).not.toContainText("value-0");
  await page.screenshot({ path: info.outputPath("selection-full-screen.png") });
  await dialog.getByRole("button", { name: "Close", exact: true }).click();
  await expect(cell(2, 1)).toBeFocused();
  // Hit testing uses logical rows after virtualization, and holding an edge
  // near the viewport boundary scrolls through already-loaded data only.
  await grid.evaluate((table) => {
    const viewport = table.closest('[data-slot="scroll-area-viewport"]')!;
    viewport.scrollTop = 40 * 27;
    viewport.dispatchEvent(new Event("scroll", { bubbles: true }));
  });
  await expect(cell(42)).toBeInViewport();
  await cell(42).click();
  const from = (await end.boundingBox())!;
  const viewport = await grid.evaluate((table) => {
    const viewport = table.closest('[data-slot="scroll-area-viewport"]')!;
    return { bottom: viewport.getBoundingClientRect().bottom, scroll: viewport.scrollTop };
  });
  const start = { x: from.x + from.width / 2, y: from.y + from.height / 2 };
  const destination = { x: start.x, y: viewport.bottom - 4 };
  const client = mobile ? await page.context().newCDPSession(page) : null;
  if (client) {
    await client.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [start] });
    await client.send("Input.dispatchTouchEvent", {
      type: "touchMove",
      touchPoints: [destination],
    });
  } else {
    await page.mouse.move(start.x, start.y);
    await page.mouse.down();
    await page.mouse.move(destination.x, destination.y, { steps: 5 });
  }
  try {
    await expect
      .poll(() =>
        grid.evaluate((table) => table.closest('[data-slot="scroll-area-viewport"]')!.scrollTop),
      )
      .toBeGreaterThan(viewport.scroll + 100);
  } finally {
    if (client) {
      await client.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
      await client.detach();
    } else await page.mouse.up();
  }
  expect(await grid.locator("[data-row-index]").count()).toBeLessThan(50);
  expect(queries).toEqual([]);
  expect(errors).toEqual([]);
});

test("settings share a readable hierarchy in light and dark themes", async ({
  page,
  liveApp,
}, info) => {
  const created = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
    data: {
      environment_name: "default",
      type: "postgres",
      name: "analytics-postgres",
      values: { host: "localhost", port: 5432, database: "analytics", username: "demo" },
    },
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  await page.request.post(`${liveApp.baseURL}/api/config/environments`, {
    data: { name: "production" },
  });
  for (const theme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: theme });
    for (const section of ["connections", "environments"] as const) {
      await page.goto(
        `${liveApp.baseURL}/project/${section}?environment=${section === "connections" ? "default&connection=analytics-postgres" : "production"}`,
      );
      const editor = page.getByRole("region", {
        name: section === "connections" ? "analytics-postgres" : "production",
        exact: true,
      });
      await expect(editor).toBeVisible();
      expect(
        await editor
          .getByRole("heading", { level: 1 })
          .evaluate((node) => getComputedStyle(node).fontSize),
      ).toBe("16px");
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
      expect(overflow).toBe(false);
      await page.screenshot({ path: info.outputPath(`${section}-${theme}.png`) });
      if (info.project.name.includes("mobile")) {
        await page
          .getByRole("tab", {
            name: section === "connections" ? "Connections" : "Environments",
            exact: true,
          })
          .click();
        await expect(page.getByTestId("settings-navigator")).toBeVisible();
        await page.screenshot({ path: info.outputPath(`${section}-navigation-${theme}.png`) });
      }
    }
  }
});

test("load parallelism saves through the editor and invalid values cannot overwrite it", async ({
  page,
  liveApp,
}) => {
  const path = "analytics/assets/analytics/parallel_load.asset.yml";
  await writeFile(
    join(liveApp.workspaceDir, path),
    "name: analytics.parallel_load\ntype: load\nconnection: duckdb-default\nparameters:\n  source_connection: duckdb-default\n  source_table: analytics.customers\nmaterialization:\n  type: table\n  strategy: create+replace\n",
  );
  const assetId = Buffer.from(path).toString("base64url");
  await expect
    .poll(async () =>
      (await (await page.request.get(`${liveApp.baseURL}/api/workspace`)).json()).pipelines
        .flatMap((p: any) => p.assets)
        .some((a: any) => a.id === assetId),
    )
    .toBe(true);
  await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${assetId}/code`);
  const workers = page.getByRole("spinbutton", { name: "Load parallelism" });
  await workers.fill("4");
  await workers.press("Enter");
  await expect
    .poll(() => readFile(join(liveApp.workspaceDir, path), "utf8"))
    .toContain('parallelism: "4"');
  await workers.fill("0");
  await workers.press("Enter");
  await expect(page.getByRole("alert")).toContainText("between 1 and 32");
  const invalid = await page.request.put(
    `${liveApp.baseURL}/api/pipelines/${pipelineId}/assets/${assetId}`,
    { data: { parameters: { parallelism: "0" } } },
  );
  expect(invalid.status()).toBe(400);
  expect(await readFile(join(liveApp.workspaceDir, path), "utf8")).toContain('parallelism: "4"');
  await page.reload();
  await expect(workers).toHaveValue("4");
});

test("run details can cancel an active foreground sensor and retain its history", async ({
  page,
  liveApp,
}, info) => {
  const path = "analytics/assets/analytics/cancel_probe.asset.yml";
  await writeFile(
    join(liveApp.workspaceDir, path),
    "name: analytics.cancel_probe\ntype: duckdb.sensor.query\nconnection: duckdb-default\nparameters:\n  query: select false\n  poke_interval: 30\n  timeout: 120s\n",
  );
  const assetId = Buffer.from(path).toString("base64url");
  await expect
    .poll(async () =>
      (await (await page.request.get(`${liveApp.baseURL}/api/workspace`)).json()).pipelines
        .flatMap((p: any) => p.assets)
        .some((a: any) => a.id === assetId),
    )
    .toBe(true);
  // API request context survives the browser navigation, just like an open execution tab.
  const pending = page.request
    .post(
      `${liveApp.baseURL}/api/assets/${assetId}/materialize/stream?environment=default&sensor_mode=wait`,
      { timeout: 85000 },
    )
    .then(async (response) => ({ ok: response.ok(), body: await response.text() }))
    .catch((error) => ({ ok: false, body: String(error) }));
  let runId = "";
  try {
    await expect
      .poll(
        async () => {
          const body = await (
            await page.request.get(`${liveApp.baseURL}/api/runs?limit=20`)
          ).json();
          const run = body.runs?.find((r: any) => r.status === "running");
          runId = run?.id ?? "";
          return Boolean(runId);
        },
        { timeout: 20000 },
      )
      .toBe(true);
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/canvas`);
    const running = page.getByTestId(`rf__node-${assetId}`).getByRole("button", {
      name: "Running: run details for analytics.cancel_probe",
      exact: true,
    });
    await expect(running).toBeVisible({ timeout: 20000 });
    await running.click();
    expect(new URL(page.url()).pathname).toContain("/canvas");
    const runLink = page.getByRole("link", { name: "Open run", exact: true });
    await expect(runLink).toHaveAttribute("href", new RegExp(`/runs/${runId}`));
    await runLink.click();
    await page.getByRole("button", { name: "Abort run", exact: true }).click();
    const confirmation = page.getByRole("alertdialog", { name: "Abort this run?" });
    await expect(confirmation).toBeVisible();
    await confirmation.getByRole("button", { name: "Keep running" }).click();
    expect(
      (await (await page.request.get(`${liveApp.baseURL}/api/runs/${runId}`)).json()).run.status,
    ).toBe("running");
    await page.getByRole("button", { name: "Abort run", exact: true }).click();
    await confirmation.getByRole("button", { name: "Abort run", exact: true }).press("Enter");
    await expect
      .poll(
        async () =>
          (await (await page.request.get(`${liveApp.baseURL}/api/runs/${runId}`)).json()).run
            .status,
        { timeout: 20000 },
      )
      .toBe("cancelled");
    await expect(page.getByRole("button", { name: "Abort run", exact: true })).toHaveCount(0);
    const stream = await pending;
    expect(stream.ok, stream.body).toBe(true);
    await page.screenshot({ path: info.outputPath("cancelled-run.png") });
  } finally {
    if (runId)
      await page.request.post(`${liveApp.baseURL}/api/runs/${runId}/cancel`).catch(() => {});
  }
});
