import { expect } from "@playwright/test";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

test("Data Browser grows a bounded sample, keeps rows on failure, and stops at exhaustion", async ({
  page,
  liveApp,
}, info) => {
  test.setTimeout(90000);
  await writeFile(
    join(liveApp.workspaceDir, "sample.csv"),
    "id\n" + Array.from({ length: 250 }, (_, i) => i).join("\n") + "\n",
  );
  const projects = await (await page.request.get(`${liveApp.baseURL}/api/projects`)).json();
  const url = new URL(`${liveApp.baseURL}/data`);
  url.searchParams.set("project", projects.default_project_id);
  url.searchParams.set(
    "detail",
    JSON.stringify({
      v: 1,
      environment: "default",
      target: {
        kind: "data-object",
        address: { source_kind: "local_files", path: "sample.csv" },
        section: "rows",
      },
    }),
  );
  const errors: string[] = [];
  const limits: number[] = [];
  const writes: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("request", (request) => {
    if (request.url().includes("/data-browser/preview")) limits.push(request.postDataJSON().limit);
    if (
      /\/materialize|\/run(?:\?|$|\/)|\/imports/.test(request.url()) &&
      request.method() !== "GET"
    )
      writes.push(request.url());
  });
  let failNext = false;
  await page.route("**/data-browser/preview", async (route) => {
    if (failNext) {
      failNext = false;
      await route.fulfill({
        status: 503,
        json: { status: "error", error: { code: "offline", message: "Temporary preview failure" } },
      });
    } else await route.continue();
  });
  await page.goto(url.href);
  const detail = page.getByTestId("routed-data-object");
  const previewButton = detail.getByRole("button", { name: "Preview rows", exact: true });
  await expect(previewButton).toBeEnabled({ timeout: 20000 });
  expect(limits).toEqual([]);
  await previewButton.click();
  const grid = detail.getByRole("grid", { name: "sample.csv preview" });
  await expect(grid).toHaveAttribute("aria-rowcount", "101");
  const more = detail.getByRole("button", { name: "Load more rows", exact: true });
  await expect(more).toBeVisible();
  // Keyboard activation works on desktop and touch-sized layouts alike.
  await more.focus();
  await page.keyboard.press("Enter");
  await expect(grid).toHaveAttribute("aria-rowcount", "201");
  await grid.getByRole("button", { name: "id, row 1: 0", exact: true }).click();
  failNext = true;
  await more.click();
  await expect(detail.getByRole("alert")).toContainText("Temporary preview failure");
  await expect(grid).toHaveAttribute("aria-rowcount", "201");
  await expect(more).toBeEnabled();
  await more.click();
  await expect(grid).toHaveAttribute("aria-rowcount", "251");
  await expect(detail.getByRole("status")).toContainText("Showing 250 rows · all rows");
  await expect(more).toHaveCount(0);
  await expect(detail.getByRole("alert")).toHaveCount(0);
  await expect(grid.locator('[aria-selected="true"]')).toHaveCount(0);
  expect(await grid.locator("[data-row-index]").count()).toBeLessThan(50);
  expect(limits).toEqual([100, 200, 400, 400]);
  expect(writes).toEqual([]);
  expect(errors).toEqual([]);
  await page.screenshot({ path: info.outputPath("preview-complete.png") });
});

test("Inspect uses lookahead without executing assets and stops at the row ceiling", async ({
  page,
  liveApp,
}) => {
  test.setTimeout(90000);
  const path = "analytics/assets/analytics/preview_rows.sql";
  const id = Buffer.from(path).toString("base64url");
  await writeFile(
    join(liveApp.workspaceDir, path),
    "/* @bruin\nname: analytics.preview_rows\ntype: duckdb.sql\n@bruin */\nselect range as id from range(1250)\n",
  );
  await expect
    .poll(
      async () => {
        const res = await page.request.get(`${liveApp.baseURL}/api/workspace`);
        return (await res.text()).includes("analytics.preview_rows");
      },
      { timeout: 20000 },
    )
    .toBe(true);
  const read = async (limit: number) => {
    const response = await page.request.get(
      `${liveApp.baseURL}/api/assets/${id}/inspect?limit=${limit}`,
    );
    expect(response.ok(), await response.text()).toBe(true);
    return response.json();
  };
  const initial = await read(100);
  expect(initial.rows).toHaveLength(100);
  expect(initial.preview.next_limit).toBe(200);
  const capped = await read(999999);
  expect(capped.rows).toHaveLength(1000);
  expect(capped.preview.reason).toBe("row_limit");
  expect(capped.preview.continuation).toBe("none");
  expect(capped.preview.has_more).toBe(true);
  expect(capped.preview.result_id).not.toBe(initial.preview.result_id);
  const pipeline = Buffer.from("analytics").toString("base64url");
  await page.goto(`${liveApp.baseURL}/pipelines/${pipeline}/assets/${id}/split?result=inspect`);
  const more = page.getByRole("button", { name: "Load more rows", exact: true });
  await expect(more).toBeEnabled({ timeout: 20000 });
  const loaded = page.waitForResponse(
    (response) =>
      response.url().includes(`/assets/${id}/inspect?`) &&
      new URL(response.url()).searchParams.get("limit") === "400",
  );
  await more.click();
  expect((await (await loaded).json()).rows).toHaveLength(400);
  await expect(page.getByRole("status").filter({ hasText: "Showing" })).toContainText(
    "Showing 400 rows",
  );
});
