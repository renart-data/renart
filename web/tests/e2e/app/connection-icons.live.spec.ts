import { expect } from "@playwright/test";

import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

test("renders the StarRocks brand mark in both themes", async ({
  page,
  liveApp,
  isMobile,
}, info) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  // Rendering a configured connection must not require a running warehouse.
  const created = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
    data: {
      environment_name: "default",
      name: "starrocks-icon",
      type: "starrocks",
      values: { host: "127.0.0.1", port: 9030, username: "icon-test", database: "analytics" },
    },
  });
  expect(created.ok(), await created.text()).toBe(true);
  const pipelineId = Buffer.from("analytics").toString("base64url");
  await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/canvas`);
  await (
    isMobile
      ? page.getByRole("tab", { name: "Data", exact: true })
      : page.getByRole("button", { name: "Data Browser", exact: true })
  ).click();
  const row = page.getByRole("button", { name: /starrocks-icon.*StarRocks/ });
  const icon = row.locator('[data-connection-engine="starrocks"]');
  await expect(icon).toBeVisible();
  await expect(icon).toHaveAttribute("aria-hidden", "true");
  await expect(icon.locator("svg")).toHaveAttribute("viewBox", "0 0 36 36");
  await expect(icon.locator("path")).toHaveCount(4);
  await expect(icon.locator("rect")).toHaveCount(0);
  const colors: string[] = [];
  for (const theme of ["light", "dark"]) {
    await page.evaluate(
      (value) => document.documentElement.classList.toggle("dark", value === "dark"),
      theme,
    );
    const styles = await icon.evaluate((element) => ({
      color: getComputedStyle(element).color,
      fill: getComputedStyle(element.querySelector("path")!).fill,
      width: element.querySelector("svg")!.getBoundingClientRect().width,
    }));
    expect(styles.fill).toBe(styles.color);
    expect(styles.width).toBeGreaterThan(8);
    colors.push(styles.color);
    await row.screenshot({ path: info.outputPath(`starrocks-${theme}.png`) });
  }
  expect(colors[0]).not.toBe(colors[1]);
  expect(errors).toEqual([]);
});
