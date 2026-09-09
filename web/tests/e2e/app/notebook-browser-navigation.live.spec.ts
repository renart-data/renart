import { expect } from "@playwright/test";
import { copyFile, mkdir } from "node:fs/promises";
import { join } from "node:path";
import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

// Desktop-only: edits use the real Monaco model, not a mocked notebook controller.
test("notebook navigation retains a draft after a failed save @desktop-only", async ({
  page,
  request,
  liveApp,
}) => {
  const create = async (title: string) => {
    const response = await request.post(`${liveApp.baseURL}/api/notebooks`, { data: { title } });
    expect(response.ok()).toBe(true);
    return (await response.json()).notebook as { id: string };
  };
  const first = await create("Unsaved notebook");
  const second = await create("Other notebook");
  const response = await request.post(`${liveApp.baseURL}/api/notebooks/${first.id}/cells`, {
    data: { name: "draft" },
  });
  expect(response.ok()).toBe(true);
  const cell = (await response.json()).notebook.cells[0] as { cell_id: string };
  await page.goto(`${liveApp.baseURL}/notebooks/${first.id}`);
  await expect(
    page.locator(`[data-notebook-cell-id="${cell.cell_id}"] .monaco-editor`).first(),
  ).toBeVisible();
  let failSave = true;
  let saves = 0;
  await page.route(`**/notebooks/${first.id}/cells/${cell.cell_id}`, async (route) => {
    if (route.request().method() !== "PUT") return route.continue();
    saves++;
    if (failSave)
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ status: "error", error: "Fixture save failure" }),
      });
    else await route.continue();
  });
  const edit = async (sql: string) =>
    page.evaluate(
      ({ cellId, sql }) => {
        const monaco = (window as typeof window & { monaco?: any }).monaco;
        const model = monaco?.editor
          .getModels()
          .find((m: any) => m.uri.toString().includes(`/notebook/${cellId}.`));
        if (!model) throw new Error("Notebook SQL model missing");
        model.setValue(sql);
      },
      { cellId: cell.cell_id, sql },
    );
  await edit("select 42 as retained_draft");
  await expect.poll(() => saves).toBe(1);
  await page.getByRole("button", { name: "All notebooks", exact: true }).click();
  const other = page.getByRole("button", { name: /Other notebook.*notebooks/ });
  await other.click();
  await expect(
    page.getByLabel(/Could not switch documents: Could not save notebook changes/),
  ).toBeVisible();
  expect(new URL(page.url()).pathname).toBe(`/notebooks/${first.id}`);
  failSave = false;
  await edit("select 43 as retained_draft");
  await expect.poll(() => saves).toBe(2);
  await other.click();
  await expect(page).toHaveURL(new RegExp(`/notebooks/${second.id}`));
  const saved = await request.get(`${liveApp.baseURL}/api/notebooks/${first.id}`);
  expect((await saved.json()).notebook.cells[0].content).toContain("select 43 as retained_draft");
});

test("notebook library and Data Browser navigate beside the active document", async ({
  page,
  request,
  liveApp,
  isMobile,
}, info) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const create = async (title: string) => {
    const response = await request.post(`${liveApp.baseURL}/api/notebooks`, { data: { title } });
    expect(response.ok()).toBe(true);
    return (await response.json()).notebook as { id: string };
  };
  const first = await create("First notebook");
  const second = await create("Second notebook");
  await page.goto(`${liveApp.baseURL}/notebooks/${first.id}`);
  const tools = page.getByRole(isMobile ? "tablist" : "complementary", { name: "build tools" });
  const openNotebooks = async () => {
    if (isMobile) await tools.getByRole("tab", { name: "Notebooks", exact: true }).click();
  };
  const tabs = page.getByRole("tablist", { name: "Open authoring documents" });
  await expect(tabs.getByRole("tab", { name: "First notebook" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await openNotebooks();
  await page.getByRole("button", { name: "All notebooks", exact: true }).click();
  expect(new URL(page.url()).pathname).toBe(`/notebooks/${first.id}`);
  const libraryURL = page.url();
  expect(new URL(libraryURL).searchParams.get("notebook_nav")).toBe("library");
  const freshTab = await page.context().newPage();
  try {
    await freshTab.goto(libraryURL);
    if (isMobile) await freshTab.getByRole("tab", { name: "Notebooks", exact: true }).click();
    await expect(
      freshTab.getByRole("button", { name: /Second notebook.*notebooks/ }),
    ).toBeVisible();
  } finally {
    await freshTab.close();
  }
  await page.screenshot({ path: info.outputPath("notebook-library.png") });
  await page.getByRole("button", { name: /Second notebook.*notebooks/ }).click();
  await expect(page).toHaveURL(new RegExp(`/notebooks/${second.id}`));
  await expect(tabs.getByRole("tab")).toHaveCount(2);
  await tabs.getByRole("tab", { name: "First notebook" }).click();
  await expect(page).toHaveURL(new RegExp(`/notebooks/${first.id}`));
  await tools
    .getByRole(isMobile ? "tab" : "button", {
      name: isMobile ? "Data" : "Data Browser",
      exact: true,
    })
    .click();
  const input = page.getByRole("textbox", { name: "Search data browser" });
  await expect(input).toBeFocused();
  expect(new URL(page.url()).pathname).toBe(`/notebooks/${first.id}`);
  await page.getByRole("button", { name: /Project files.*Files inside this project/ }).click();
  await expect(input).toHaveValue('"Project files"./');
  await expect(page.getByRole("button", { name: "analytics", exact: true })).toBeVisible();
  await page.screenshot({ path: info.outputPath("notebook-data-browser.png") });
  if (isMobile) await page.getByRole("button", { name: "Close", exact: true }).click();
  await tools.getByRole(isMobile ? "tab" : "button", { name: "Notebooks", exact: true }).click();
  await expect(page.getByRole("button", { name: "All notebooks", exact: true })).toBeVisible();
  if (isMobile) await page.getByRole("button", { name: "Close", exact: true }).click();
  await page.getByRole("button", { name: "Close First notebook", exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/notebooks/${second.id}`));
  await expect(tabs.getByRole("tab")).toHaveCount(1);
  expect(errors).toEqual([]);
});

test("clicked sources, folders and files use the completed search path", async ({
  page,
  liveApp,
  isMobile,
}) => {
  await mkdir(join(liveApp.workspaceDir, "data"));
  await copyFile(
    join(__dirname, "../../fixtures/data-browser/project-orders.csv"),
    join(liveApp.workspaceDir, "data/orders.csv"),
  );
  await page.goto(`${liveApp.baseURL}/data`);
  const open = async () => {
    if (isMobile) await page.getByRole("tab", { name: "Data", exact: true }).click();
  };
  await open();
  const input = page.getByRole("textbox", { name: "Search data browser" });
  await page.getByRole("button", { name: /Project files.*Files inside this project/ }).click();
  await expect(input).toHaveValue('"Project files"./');
  await page.getByRole("button", { name: "data", exact: true }).click();
  await expect(input).toHaveValue('"Project files"./data/');
  await expect(page.getByRole("link", { name: /orders.csv/ }).first()).toBeVisible();
  await input.press("ArrowDown");
  await expect(page.getByRole("link", { name: /orders.csv/ }).first()).toBeFocused();
  await page.keyboard.press("Enter");
  if (isMobile && !(await input.isVisible())) await open();
  await expect(input).toHaveValue('"Project files"./data/orders.csv');
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await expect(input).toHaveValue('"Project files"./');
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await expect(input).toHaveValue("");
  await expect(page.getByRole("button", { name: /duckdb-default.*DuckDB/ })).toBeVisible();
});
