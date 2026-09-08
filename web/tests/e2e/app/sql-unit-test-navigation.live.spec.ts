import { expect } from "@playwright/test";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { liveTest as test } from "../live-app-fixture";

const pipeline = Buffer.from("analytics").toString("base64url");
const orders = Buffer.from("analytics/assets/analytics/orders.sql").toString("base64url");

test.describe("SQL unit test state ownership", () => {
  test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
  // The regression exercises the persistent desktop inspector and explorer.
  // Mobile closes its property sheet before opening the navigation surface.
  test("keeps a late deletion response on its original asset @desktop-only", async ({
    page,
    liveApp,
  }) => {
    await writeFile(
      join(liveApp.workspaceDir, "analytics/assets/analytics/orders.sql"),
      `/* @bruin\nname: analytics.orders\ntype: duckdb.sql\nunit_tests:\n  - name: Delete me\n    expected: {count: 1}\n  - name: Remaining orders fixture\n    expected: {count: 1}\n@bruin */\nselect 1 as order_id\n`,
    );
    await writeFile(
      join(liveApp.workspaceDir, "analytics/assets/analytics/customers.sql"),
      `/* @bruin\nname: analytics.customers\ntype: duckdb.sql\nunit_tests:\n  - name: Customer fixture\n    expected: {count: 1}\n@bruin */\nselect 1 as customer_id\n`,
    );
    await page.goto(
      `${liveApp.baseURL}/pipelines/${pipeline}/assets/${orders}/code?result=inspect&editor=asset`,
    );
    const properties = page.getByTestId("asset-inspector").filter({ visible: true });
    await properties.getByRole("tab", { name: "Tests", exact: true }).click();
    await expect(properties.getByText("Delete me", { exact: true })).toBeVisible();

    let release!: () => void;
    let received!: () => void;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    const intercepted = new Promise<void>((resolve) => {
      received = resolve;
    });
    await page.route(`**/api/assets/${orders}/transactions`, async (route) => {
      const response = await route.fetch();
      received();
      await held;
      await route.fulfill({ response });
    });
    try {
      page.once("dialog", (dialog) => void dialog.accept());
      await properties.getByRole("button", { name: "Remove test Delete me", exact: true }).click();
      await intercepted;
      await page
        .getByRole("complementary", { name: "Build navigation", exact: true })
        .getByRole("button", { name: /^customers\.sql\b/ })
        .click();
      await expect(properties.getByText("Customer fixture", { exact: true })).toBeVisible();
      const response = page.waitForResponse((response) =>
        response.url().endsWith(`/api/assets/${orders}/transactions`),
      );
      release();
      await (await response).finished();
      await expect(
        properties.getByRole("button", { name: "Run tests", exact: true }),
      ).toBeEnabled();
      await page.evaluate(
        () =>
          new Promise<void>((resolve) =>
            requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
          ),
      );
      await expect(properties.getByText("Customer fixture", { exact: true })).toBeVisible();
      await expect(properties.getByText("Remaining orders fixture", { exact: true })).toHaveCount(
        0,
      );
    } finally {
      release();
    }
  });
});
