import { expect } from "@playwright/test";

import { liveTest as test } from "../live-app-fixture";

test.use({ isolateUserConfig: true });

test("keeps the project settings page scrollable", async ({ page, liveApp }) => {
  await page.goto(`${liveApp.baseURL}/project/general`);

  const viewport = page
    .getByTestId("project-settings-scroll")
    .locator(':scope > [data-slot="scroll-area-viewport"]');
  await expect(viewport).toBeVisible();

  const scrollMetrics = await viewport.evaluate((element) => {
    const start = element.scrollTop;
    element.scrollTop = element.scrollHeight;
    return {
      start,
      end: element.scrollTop,
      clientHeight: element.clientHeight,
      scrollHeight: element.scrollHeight,
    };
  });

  expect(scrollMetrics.scrollHeight).toBeGreaterThan(scrollMetrics.clientHeight);
  expect(scrollMetrics.end).toBeGreaterThan(scrollMetrics.start);
});

test("sets up the encrypted vault and offers it for connection credentials", async ({
  page,
  liveApp,
}, testInfo) => {
  await page.goto(`${liveApp.baseURL}/project/connections`);
  await page.getByRole("button", { name: "Set up", exact: true }).click();

  const setupDialog = page.getByRole("dialog", { name: "Set up encrypted vault" });
  await setupDialog.getByLabel("Passphrase", { exact: true }).fill("correct horse battery staple");
  await setupDialog.getByLabel("Confirm passphrase").fill("correct horse battery staple");
  await setupDialog.getByRole("button", { name: "Set up vault" }).click();

  await expect(setupDialog).toBeHidden();
  await expect(page.getByText("Unlocked", { exact: true })).toBeVisible();

  const unlockedButton = page.getByRole("button", { name: "Encrypted vault unlocked" });
  await expect(unlockedButton).toBeVisible();
  await unlockedButton.click();

  let vaultOverlay = page.getByRole("dialog", { name: "Encrypted vault" });
  await expect(vaultOverlay).toBeVisible();
  await expect(vaultOverlay).toHaveAttribute(
    "data-slot",
    testInfo.project.name.includes("mobile") ? "dialog-content" : "popover-content",
  );
  await vaultOverlay.getByRole("button", { name: "Lock vault" }).click();

  const lockedButton = page.getByRole("button", { name: "Encrypted vault locked" });
  await expect(lockedButton).toBeVisible();
  await lockedButton.click();
  vaultOverlay = page.getByRole("dialog", { name: "Encrypted vault" });
  await vaultOverlay.getByLabel("Passphrase").fill("correct horse battery staple");
  await vaultOverlay.getByRole("button", { name: "Unlock vault" }).click();
  await expect(unlockedButton).toBeVisible();

  await page.getByRole("button", { name: "New connection", exact: true }).click();
  const connectionSheet = page.getByRole("region", { name: "New connection" });
  await expect(
    connectionSheet.getByRole("radio", { name: "Encrypted vault" }).first(),
  ).toBeEnabled();
});

test("keeps long connection forms scrollable and puts tuning fields last", async ({
  page,
  liveApp,
}) => {
  await page.goto(`${liveApp.baseURL}/project/connections`);
  await page.getByRole("button", { name: "New connection", exact: true }).click();

  const sheet = page.getByRole("region", { name: "New connection" });
  await expect(sheet).toBeVisible();
  await expect(sheet.getByText("query_results_path", { exact: true })).toBeVisible();

  await sheet.getByRole("button", { name: "Advanced", exact: true }).click();
  const formViewport = page
    .getByTestId("project-settings-scroll")
    .locator(':scope > [data-slot="scroll-area-viewport"]');
  const formText = await formViewport.innerText();
  expect(formText.indexOf("query_results_path")).toBeGreaterThanOrEqual(0);
  expect(formText.indexOf("access_key_id")).toBeGreaterThan(formText.indexOf("query_results_path"));
  expect(formText.indexOf("max_concurrent_assets")).toBeGreaterThan(
    formText.indexOf("access_key_id"),
  );

  const scrollMetrics = await formViewport.evaluate((element) => {
    const start = element.scrollTop;
    element.scrollTop = element.scrollHeight;
    return {
      start,
      end: element.scrollTop,
      clientHeight: element.clientHeight,
      scrollHeight: element.scrollHeight,
    };
  });
  expect(scrollMetrics.scrollHeight).toBeGreaterThan(scrollMetrics.clientHeight);
  expect(scrollMetrics.end).toBeGreaterThan(scrollMetrics.start);

  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
    ),
  ).toBe(true);

  const box = await sheet.boundingBox();
  expect(box).not.toBeNull();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(page.viewportSize()!.width);
});

test("creates an environment with an explained schema prefix", async ({ page, liveApp }) => {
  await page.goto(`${liveApp.baseURL}/project/environments`);
  await page.getByRole("button", { name: "New environment" }).click();

  const sheet = page.getByRole("region", { name: "New environment" });
  await expect(sheet).toContainText(
    "dev_ turns analytics.orders into dev_analytics.orders while the asset name stays unchanged.",
  );
  await sheet.getByPlaceholder("prod").fill("dev");
  await sheet.getByPlaceholder("analytics_").fill("dev_");
  await sheet.getByRole("button", { name: "Create environment" }).click();

  await expect(sheet).toBeHidden();
  const environment = page.getByRole("region", { name: "dev", exact: true });
  await expect(environment).toBeVisible();
  await expect(environment.getByLabel("Schema prefix", { exact: true })).toHaveValue("dev_");

  const response = await page.request.get(`${liveApp.baseURL}/api/config`);
  expect(response.ok()).toBe(true);
  const config = (await response.json()) as {
    environments: Array<{ name: string; schema_prefix?: string }>;
  };
  expect(config.environments.find((candidate) => candidate.name === "dev")?.schema_prefix).toBe(
    "dev_",
  );
});
