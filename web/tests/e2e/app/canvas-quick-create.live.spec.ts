import { expect, type Locator, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace" });

const encode = (value: string) => Buffer.from(value).toString("base64url");
const pipelineId = encode("analytics");
const assetId = (leaf: string) => encode(`analytics/assets/analytics/${leaf}.sql`);
const customersAssetId = assetId("customers");
const ordersAssetId = assetId("orders");

function canvasCard(page: Page, id: string) {
  return page.locator(`[data-testid="lineage-asset"][data-asset-id="${id}"]`);
}

async function readAsset(workspaceDir: string, leaf: string) {
  return readFile(join(workspaceDir, "analytics", "assets", "analytics", `${leaf}.sql`), "utf8");
}

// Revealing a new asset centers it, which can move other cards out of a
// small viewport.
async function fitView(page: Page) {
  await page.getByRole("button", { name: "fit view" }).click();
  await page.waitForTimeout(300);
}

// Drags from a card's + onto a point; React Flow starts the drag on the handle.
async function dragFromPlus(page: Page, source: Locator, to: { x: number; y: number }) {
  await source.hover();
  const plus = source.getByRole("button", { name: "Create downstream asset" });
  const box = await plus.boundingBox();
  if (!box) throw new Error("the + is not rendered");
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(to.x, to.y, { steps: 12 });
  await page.mouse.up();
}

test.describe("canvas quick create", () => {
  // Desktop-only: the canvas + and drag affordances need the wide canvas.
  test("creates a downstream asset in place and keeps a canvas-only layout @desktop-only", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/canvas`);
    const node = page.getByTestId(`rf__node-${customersAssetId}`);
    await expect(node).toBeVisible({ timeout: 15000 });
    await node.hover();
    await node.getByRole("button", { name: "Create downstream asset" }).click();

    const pending = page.getByTestId("quick-create-node");
    await expect(pending).toBeVisible();
    const name = pending.getByLabel("Asset name");
    await expect(name).toBeFocused();
    await expect(name).toHaveValue("analytics.customers_downstream");
    // The leaf is selected, so typing keeps the asset in its group.
    await page.keyboard.type("customer_names");
    await expect(name).toHaveValue("analytics.customer_names");
    await page.keyboard.press("Enter");

    const createdId = assetId("customer_names");
    await expect(page.getByTestId(`rf__node-${createdId}`)).toBeVisible({ timeout: 15000 });
    await expect(pending).toHaveCount(0);
    await expect(page).toHaveURL(new RegExp(`/assets/${createdId}/canvas`));
    const peek = page.getByTestId("asset-source-peek");
    await expect(peek).toContainText("analytics.customer_names");
    await expect(peek).toContainText("analytics.customers");

    const content = await readAsset(liveApp.workspaceDir, "customer_names");
    // The project writes lowercase SQL, so the starter does too.
    expect(content).toMatch(/\nfrom analytics\.customers\n/);
    expect(content).toContain("- analytics.customers");
    expect(content).not.toContain("secrets:");

    await peek.getByRole("button", { name: "Edit source" }).click();
    await expect(page).toHaveURL(new RegExp(`/assets/${createdId}/split`));
  });

  test("keeps a failed create on the canvas with its error @desktop-only", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/canvas`);
    const card = canvasCard(page, ordersAssetId);
    await expect(card).toBeVisible({ timeout: 15000 });
    await card.click({ button: "right", position: { x: 30, y: 14 } });
    await page.getByRole("menuitem", { name: "Create downstream asset" }).click();

    const pending = page.getByTestId("quick-create-node");
    const name = pending.getByLabel("Asset name");
    await expect(name).toBeFocused();
    await name.fill("analytics.customers");
    await expect(pending).toContainText("analytics.customers already exists.");
    await expect(pending.getByRole("button", { name: "Create" })).toBeDisabled();

    await name.fill("analytics.order_totals");
    await page.route("**/api/pipelines/*/assets", (route) =>
      route.request().method() === "POST"
        ? route.fulfill({
            status: 500,
            json: { error: { code: "asset_write_failed", message: "Disk is full." } },
          })
        : route.continue(),
    );
    await pending.getByRole("button", { name: "Create" }).click();
    await expect(pending).toHaveAttribute("data-state", "failed");
    await expect(pending.getByRole("alert")).toHaveText("Disk is full.");
    await expect(name).toHaveValue("analytics.order_totals");

    await page.unroute("**/api/pipelines/*/assets");
    await pending.getByRole("button", { name: "Retry" }).click();
    await expect(page.getByTestId(`rf__node-${assetId("order_totals")}`)).toBeVisible({
      timeout: 15000,
    });
    await expect(pending).toHaveCount(0);

    // Escape cancels and returns focus to the card the user started from.
    await fitView(page);
    await page.getByTestId(`rf__node-${ordersAssetId}`).hover();
    await page
      .getByTestId(`rf__node-${ordersAssetId}`)
      .getByRole("button", { name: "Create downstream asset" })
      .click();
    await expect(pending).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(pending).toHaveCount(0);
    await expect(card).toBeFocused();
  });

  test("carries the draft into the creation dialog @desktop-only", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/canvas`);
    const node = page.getByTestId(`rf__node-${customersAssetId}`);
    await expect(node).toBeVisible({ timeout: 15000 });
    await node.hover();
    await node.getByRole("button", { name: "Create downstream asset" }).click();
    const pending = page.getByTestId("quick-create-node");
    await page.keyboard.type("customer_scores");
    await pending.getByRole("radio", { name: "Python" }).click();
    await pending.getByRole("button", { name: "More options…" }).click();

    const dialog = page.getByRole("dialog", { name: "New downstream asset" });
    await expect(dialog).toBeVisible();
    await expect(pending).toHaveCount(0);
    await expect(dialog.getByLabel("Asset name", { exact: true })).toHaveValue(
      "analytics.customer_scores",
    );
    await expect(dialog.getByRole("radio", { name: "Python", exact: true })).toHaveAttribute(
      "aria-checked",
      "true",
    );
  });

  test("joins an asset into a query by drawing a link @desktop-only", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/canvas`);
    const source = page.getByTestId(`rf__node-${customersAssetId}`);
    const target = page.getByTestId(`rf__node-${ordersAssetId}`);
    await expect(source).toBeVisible({ timeout: 15000 });
    const targetBox = await target.boundingBox();
    if (!targetBox) throw new Error("the target card is not rendered");
    await dragFromPlus(page, source, {
      x: targetBox.x + 30,
      y: targetBox.y + 14,
    });

    const menu = page.getByRole("menu", { name: "Connect assets" });
    await expect(menu).toContainText("customers → orders");
    await expect(menu.getByRole("menuitem", { name: /Add as dependency/ })).toBeVisible();
    await menu.getByRole("menuitem", { name: /Join in query/ }).click();
    await expect(menu).toHaveCount(0, { timeout: 15000 });

    const content = await readAsset(liveApp.workspaceDir, "orders");
    expect(content).toMatch(/with orders as \(\n {4}select 100 as order_id/);
    expect(content).toContain("analytics.customers as customers");
    expect(content).toContain("- analytics.customers");
    await expect(
      page.locator(`[data-testid^="rf__edge-${customersAssetId}-${ordersAssetId}"]`),
    ).toHaveCount(1, { timeout: 15000 });

    // The same link again is explained instead of offered. The new edge moved
    // the cards, so measure the target again.
    await fitView(page);
    const movedBox = await target.boundingBox();
    if (!movedBox) throw new Error("the target card is not rendered");
    await dragFromPlus(page, source, { x: movedBox.x + 30, y: movedBox.y + 14 });
    await expect(menu).toContainText("orders already depends on customers.");
    await page.keyboard.press("Escape");
    await expect(menu).toHaveCount(0);
  });

  test("joins shift-selected assets into a new asset @desktop-only", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/canvas`);
    await expect(canvasCard(page, customersAssetId)).toBeVisible({ timeout: 15000 });
    await canvasCard(page, customersAssetId).click({
      modifiers: ["Shift"],
      position: { x: 30, y: 14 },
    });
    await canvasCard(page, ordersAssetId).click({
      modifiers: ["Shift"],
      position: { x: 30, y: 14 },
    });
    // Shift-click selects without opening the asset.
    await expect(page).toHaveURL(new RegExp(`/pipelines/${pipelineId}/canvas`));

    const toolbar = page.getByRole("toolbar", { name: "Selected assets" });
    await expect(toolbar).toContainText("2 assets selected");
    await toolbar.getByRole("button", { name: "Join into new asset" }).click();
    const pending = page.getByTestId("quick-create-node");
    await expect(pending.getByLabel("Asset name")).toHaveValue("analytics.customers_orders");
    await expect(pending.getByRole("radio", { name: "Python" })).toBeDisabled();
    await page.keyboard.press("Enter");
    await expect(page.getByTestId(`rf__node-${assetId("customers_orders")}`)).toBeVisible({
      timeout: 15000,
    });

    const content = await readAsset(liveApp.workspaceDir, "customers_orders");
    expect(content).toContain("from analytics.customers as customers");
    expect(content).toContain("analytics.orders as orders");
    expect(content).toContain("- analytics.customers");
    expect(content).toContain("- analytics.orders");
  });

  test("offers asset creation in the command palette and on N and D @desktop-only", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${customersAssetId}/canvas`);
    await expect(page.getByTestId(`rf__node-${customersAssetId}`)).toBeVisible({ timeout: 15000 });

    await page.keyboard.press("Control+k");
    const palette = page.getByRole("dialog", { name: "Search" });
    const actions = palette.getByRole("group", { name: "Actions" });
    await expect(actions.getByRole("option", { name: /New asset/ })).toBeVisible();
    await actions.getByRole("option", { name: /Add downstream of selected/ }).click();
    const pending = page.getByTestId("quick-create-node");
    await expect(pending.getByLabel("Asset name")).toHaveValue("analytics.customers_downstream");
    await page.keyboard.press("Escape");
    await expect(pending).toHaveCount(0);

    await page.keyboard.press("d");
    await expect(pending).toBeVisible();
    await page.keyboard.press("Escape");
    await page.keyboard.press("n");
    await expect(page.getByRole("dialog", { name: "New asset" })).toBeVisible();
  });
});
