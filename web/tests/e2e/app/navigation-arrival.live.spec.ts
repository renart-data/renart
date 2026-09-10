import { expect } from "@playwright/test";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
test.setTimeout(90000);
const pipelineId = Buffer.from("analytics").toString("base64url");

test("selected document tabs are revealed on external navigation without scrolling the page", async ({
  page,
  liveApp,
}) => {
  const assets = Array.from({ length: 8 }, (_, index) => {
    const name = `navigation_tab_${index}`;
    const path = `analytics/assets/analytics/${name}.sql`;
    return { name, path, id: Buffer.from(path).toString("base64url") };
  });
  await Promise.all(
    assets.map((asset) =>
      writeFile(
        join(liveApp.workspaceDir, asset.path),
        `/* @bruin\nname: analytics.${asset.name}\ntype: duckdb.sql\n@bruin */\nselect 1 as value\n`,
      ),
    ),
  );
  for (const asset of assets) {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${asset.id}/code`);
    await expect(page.getByRole("tab", { name: `${asset.name}.sql`, exact: true })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  }
  const strip = page.getByRole("tablist", { name: "Open authoring documents" });
  const last = strip.getByRole("tab", { name: `${assets.at(-1)!.name}.sql`, exact: true });
  await expect(last).toBeInViewport();
  await strip.getByRole("tab", { name: `${assets[0].name}.sql`, exact: true }).click();
  await expect(
    strip.getByRole("tab", { name: `${assets[0].name}.sql`, exact: true }),
  ).toHaveAttribute("aria-selected", "true");
  await strip.evaluate((element) => {
    element.scrollLeft = 0;
  });
  await page.goBack();
  await expect(last).toHaveAttribute("aria-selected", "true");
  await expect(last).toBeInViewport();
  expect(await page.evaluate(() => window.scrollY)).toBe(0);
});

test("failed and last-built badges expose run links only inside their popovers", async ({
  page,
  liveApp,
}, info) => {
  const path = "analytics/assets/analytics/badge_probe.sql";
  const assetId = Buffer.from(path).toString("base64url");
  for (const [sql, status] of [
    ["select cast('not-a-number' as integer) as value", "failed"],
    ["select 42 as value", "succeeded"],
  ]) {
    await writeFile(
      join(liveApp.workspaceDir, path),
      `/* @bruin\nname: analytics.badge_probe\ntype: duckdb.sql\n@bruin */\n${sql}\n`,
    );
    await expect
      .poll(async () =>
        (await (await page.request.get(`${liveApp.baseURL}/api/workspace`)).json()).pipelines
          .flatMap((p: any) => p.assets)
          .some((a: any) => a.id === assetId && a.content.includes(sql)),
      )
      .toBe(true);
    await page.request.post(
      `${liveApp.baseURL}/api/assets/${assetId}/materialize/stream?environment=default`,
      { timeout: 30000 },
    );
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/canvas`);
    const node = page.getByTestId(`rf__node-${assetId}`);
    const badge = node.locator('button[aria-label$="run details for analytics.badge_probe"]');
    await expect(badge).toBeVisible();
    if (status === "failed") await expect(badge).toContainText(/failed/i);
    else await expect(badge).not.toContainText(/failed/i);
    await expect(node.getByRole("link", { name: /run/ })).toHaveCount(0);
    await badge.click();
    expect(new URL(page.url()).pathname).toContain("/canvas");
    const link = page.getByRole("link", { name: "Open run", exact: true });
    await expect(link).toBeVisible();
    const href = await link.getAttribute("href");
    const runId = new URL(href!, liveApp.baseURL).pathname.split("/").at(-1);
    expect(new URL(href!, liveApp.baseURL).searchParams.get("run_asset")).toBe(
      "analytics.badge_probe",
    );
    const run = (await (await page.request.get(`${liveApp.baseURL}/api/runs/${runId}`)).json()).run;
    expect(run.status).toBe(status === "succeeded" ? "success" : status);
    await page.screenshot({ path: info.outputPath(`${status}-popover.png`) });
    await page.keyboard.press("Escape");
    await badge.press("Enter");
    await expect(link).toBeVisible();
    expect(new URL(page.url()).pathname).toContain("/canvas");
    await page.keyboard.press("Escape");
  }
});
