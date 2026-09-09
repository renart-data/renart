import { expect } from "@playwright/test";
import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
const pipeline = Buffer.from("analytics").toString("base64url");

test("production routes share one workbench and legacy bookmarks retain their state", async ({
  page,
  liveApp,
}) => {
  for (const [path, mode] of [
    [`/pipelines/${pipeline}/canvas?result=inspect&editor=asset`, "build"],
    ["/run", "run"],
    ["/catalog", "explore"],
    ["/project/connections", "build"],
    ["/notebooks", "build"],
    ["/schedules", "run"],
  ]) {
    await page.goto(`${liveApp.baseURL}${path}`);
    await expect(page.locator(`[data-app-mode="${mode}"]`)).toHaveAttribute(
      "data-workbench-route",
      "workbench",
    );
    await expect(page.locator("main")).toBeVisible();
  }

  // These bookmarks are supported compatibility, not a removable design lab.
  await page.goto(
    `${liveApp.baseURL}/redesign/pipelines/${pipeline}/canvas?result=inspect&editor=asset`,
  );
  await expect(page).toHaveURL(
    `${liveApp.baseURL}/pipelines/${pipeline}/canvas?result=inspect&editor=asset`,
  );
  await expect(page.locator(".react-flow__node").first()).toBeVisible();
  await expect(page.locator('[data-app-mode="build"]')).toHaveAttribute(
    "data-workbench-route",
    "workbench",
  );

  const workspace = await (await page.request.get(`${liveApp.baseURL}/api/workspace`)).json();
  expect(workspace.pipelines[0].assets).toHaveLength(2);
});

test("the semantic impact playground remains available after navigation lab retirement", async ({
  page,
  liveApp,
}) => {
  await page.goto(`${liveApp.baseURL}/semantic-diff`);
  await expect(page.getByText("/ semantic playground", { exact: true })).toBeVisible();
  await expect(page).toHaveTitle("Semantic Diff · renart");
});
