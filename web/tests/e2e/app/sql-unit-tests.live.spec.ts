import { expect, type Locator } from "@playwright/test";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { stringify } from "yaml";
import { liveTest as test } from "../live-app-fixture";

const pipeline = Buffer.from("analytics").toString("base64url");
const asset = Buffer.from("analytics/assets/analytics/orders.sql").toString("base64url");
const fixture = {
  name: "Two orders",
  inputs: [{ asset: "analytics.customers", rows: [{ customer_id: 1 }, { customer_id: 2 }] }],
  expected: { rows: [{ total: 3 }], match: "exact" },
};

async function editFixture(dialog: Locator, value: unknown) {
  await dialog.locator(".monaco-editor").click();
  await dialog.page().keyboard.press("ControlOrMeta+a");
  await dialog.getByRole("textbox", { name: "SQL unit test fixture", exact: true }).evaluate(
    (element, text) => {
      const data = new DataTransfer();
      data.setData("text/plain", text);
      element.dispatchEvent(
        new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }),
      );
    },
    typeof value === "string"
      ? value
      : `# Fixture values are checked against SQL column types.\n${stringify(value)}`,
  );
  await expect(dialog.locator(".view-lines")).toContainText("Two orders");
}

test.describe("SQL unit test editor", () => {
  test.setTimeout(90000);
  test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
  test("types fixtures, persists canonical tests, executes mocks and restores a cold Tests link", async ({
    page,
    browser,
    liveApp,
  }, info) => {
    await writeFile(
      join(liveApp.workspaceDir, "analytics/assets/analytics/orders.sql"),
      `/* @bruin\nname: analytics.orders\ntype: duckdb.sql\ncolumns:\n  - name: total\n    type: HUGEINT\n@bruin */\nselect sum(customer_id) as total from analytics.customers\n`,
    );
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    await page.goto(
      `${liveApp.baseURL}/pipelines/${pipeline}/assets/${asset}/code?result=inspect&editor=asset`,
    );
    if (info.project.name.includes("mobile"))
      await page.getByRole("button", { name: "Asset properties", exact: true }).click();
    const properties = page.getByTestId("asset-inspector").filter({ visible: true });
    await properties.getByRole("tab", { name: "Tests", exact: true }).click();
    const schemaResponse = await page.request.get(
      `${liveApp.baseURL}/api/assets/${asset}/unit-tests`,
    );
    expect(schemaResponse.ok(), await schemaResponse.text()).toBe(true);
    await info.attach("fixture-schema", {
      body: await schemaResponse.text(),
      contentType: "application/json",
    });
    await properties.getByRole("button", { name: "Add test", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "Add SQL unit test", exact: true });
    await editFixture(dialog, "name: Two orders\nexpected:\n  rows:\n    - to");
    await page.keyboard.press("ControlOrMeta+Space");
    await expect(dialog.locator(".suggest-widget.visible")).toContainText("total");
    await page.keyboard.press("Escape");
    await editFixture(dialog, { ...fixture, expected: { rows: [{ total: "not a number" }] } });
    await expect(dialog.getByRole("button", { name: "Save test", exact: true })).toBeDisabled();
    await expect(dialog.locator(".squiggly-error, .squiggly-warning").first()).toBeVisible();
    await page.screenshot({ path: info.outputPath("fixture-type-error.png") });
    await editFixture(dialog, fixture);
    await expect(dialog.getByRole("button", { name: "Save test", exact: true })).toBeEnabled();
    await page.keyboard.press("ControlOrMeta+Home");
    await page.screenshot({ path: info.outputPath("fixture-editor.png") });
    await dialog.getByRole("button", { name: "Save test", exact: true }).click();
    await expect(dialog).toBeHidden();
    await properties.getByRole("button", { name: "Run tests", exact: true }).click();
    await expect(properties.getByRole("status")).toContainText("Passed", { timeout: 30000 });
    expect(new URL(page.url()).searchParams.get("result")).toBe("inspect");

    const context = await browser.newContext(info.project.use);
    try {
      const fresh = await context.newPage();
      await fresh.goto(page.url());
      await expect(fresh.getByTestId("asset-unit-tests")).toBeVisible();
      await expect(fresh.getByText("Two orders", { exact: true })).toBeVisible();
    } finally {
      await context.close();
    }
    await properties.getByRole("button", { name: "Edit test Two orders", exact: true }).click();
    const edit = page.getByRole("dialog", { name: "Edit SQL unit test", exact: true });
    await editFixture(edit, { ...fixture, expected: { rows: [{ total: 4 }], match: "exact" } });
    await edit.getByRole("button", { name: "Save test", exact: true }).click();
    await expect(edit).toBeHidden();
    await properties.getByRole("button", { name: "Run tests", exact: true }).click();
    await expect(properties.getByRole("status")).toContainText("Assertion failed", {
      timeout: 30000,
    });
    expect(errors).toEqual([]);
    await page.screenshot({ path: info.outputPath("sql-unit-tests.png") });
  });
});
