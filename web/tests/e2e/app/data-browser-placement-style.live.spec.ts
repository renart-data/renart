import { expect, type Locator, type Page } from "@playwright/test";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { liveTest as test, type LiveApp } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

// Compare canvas-space geometry, independent of the viewport's fit/zoom animation.
async function nodePositions(nodes: Locator) {
  return nodes.evaluateAll((elements) =>
    elements
      .map((element) => {
        const node = element as HTMLElement;
        return {
          id: node.dataset.id,
          transform: node.style.transform,
          width: node.offsetWidth,
          height: node.offsetHeight,
        };
      })
      .sort((a, b) => (a.id ?? "").localeCompare(b.id ?? "")),
  );
}

async function openBrowser(page: Page, mobile: boolean) {
  if (mobile) await page.getByRole("tab", { name: "Data", exact: true }).click();
  else if (!(await page.getByRole("textbox", { name: "Search data browser" }).isVisible()))
    await page.getByRole("button", { name: "Data Browser", exact: true }).click();
  await expect(page.locator('[data-transfer-label="duckdb-output"]')).toBeVisible();
}

async function prepare(page: Page, liveApp: LiveApp, mobile: boolean) {
  const result = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
    data: {
      environment_name: "default",
      name: "duckdb-output",
      type: "duckdb",
      values: { path: "duckdb-files/output.db" },
    },
  });
  expect(result.ok(), await result.text()).toBe(true);
  await page.goto(
    `${liveApp.baseURL}/pipelines/${Buffer.from("analytics").toString("base64url")}/canvas?result=inspect`,
  );
  await openBrowser(page, mobile);
}

// Native drag layering; touch placement is verified in the following test.
test("expanded destination drops stack above asset cards and drag-end cleans up @desktop-only", async ({
  page,
  liveApp,
}, info) => {
  await writeFile(
    join(liveApp.workspaceDir, "analytics/assets/landing.sql"),
    "/* @bruin\nname: landing.anchor\ntype: duckdb.sql\nconnection: duckdb-default\n@bruin */\nselect 1 as id\n",
  );
  await prepare(page, liveApp, false);
  await page.getByTestId("lineage-asset").filter({ hasText: "anchor" }).click();
  await expect(page).toHaveURL(
    new RegExp(Buffer.from("analytics/assets/landing.sql").toString("base64url")),
  );
  const row = page.locator('[data-transfer-label="duckdb-output"]');
  const transfer = await page.evaluateHandle(() => new DataTransfer());
  await row.dispatchEvent("dragstart", { dataTransfer: transfer });
  const target = page.getByRole("button", {
    name: "Create Load after analytics.customers",
    exact: true,
  });
  await expect(target).toBeVisible();
  const nodes = page.locator(".react-flow__node-lineageAsset");
  const positions = await nodePositions(nodes);
  const bounds = (await target.boundingBox())!;
  await target.dispatchEvent("dragover", {
    dataTransfer: transfer,
    clientX: bounds.x + 5,
    clientY: bounds.y + bounds.height / 2,
  });
  await expect(target).toHaveAttribute("data-proximity", "near");
  await expect
    .poll(() =>
      target.evaluate((element) => {
        const node = element.closest(".react-flow__node")!;
        const otherLayers = [...document.querySelectorAll(".react-flow__node-lineageAsset")]
          .filter((other) => other !== node)
          .map((other) => Number(getComputedStyle(other).zIndex) || 0);
        return Number(getComputedStyle(node).zIndex) > Math.max(...otherLayers);
      }),
    )
    .toBe(true);
  expect(await nodePositions(nodes)).toEqual(positions);
  await expect(
    page.getByRole("button", { name: "Cancel canvas placement", exact: true }),
  ).toHaveCount(0);
  await page.screenshot({ path: info.outputPath("expanded-load-layer.png") });
  await row.dispatchEvent("dragend", { dataTransfer: transfer });
  await expect(page.getByTestId("data-browser-drop-target")).toHaveCount(0);
});

test("keyboard and touch placement retain Cancel and Escape without creating assets", async ({
  page,
  liveApp,
  isMobile,
}) => {
  await prepare(page, liveApp, Boolean(isMobile));
  const choose = page.getByRole("button", { name: "Use duckdb-output in canvas", exact: true });
  await choose.click();
  const cancel = page.getByRole("button", { name: "Cancel canvas placement", exact: true });
  await expect(cancel).toBeVisible();
  await cancel.click();
  await expect(page.getByTestId("data-browser-drop-target")).toHaveCount(0);
  await openBrowser(page, Boolean(isMobile));
  await choose.click();
  await expect(cancel).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByTestId("data-browser-drop-target")).toHaveCount(0);
  await expect(page.getByRole("dialog", { name: /New.*asset/ })).toHaveCount(0);
  const workspace = await (await page.request.get(`${liveApp.baseURL}/api/workspace`)).json();
  expect(workspace.pipelines[0].assets).toHaveLength(2);
});
