import { expect } from "@playwright/test";
import { copyFile, mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { liveTest as test } from "../live-app-fixture";
import type { WorkspaceState } from "../../../lib/generated/api-types";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
test.setTimeout(90000);

test("project files create a reviewed local Load via drag or touch without execution", async ({
  page,
  liveApp,
}, info) => {
  await mkdir(join(liveApp.workspaceDir, "data"));
  const file = join(liveApp.workspaceDir, "data/orders.csv");
  await copyFile(join(__dirname, "../../fixtures/data-browser/project-orders.csv"), file);
  const original = await readFile(file, "utf8");
  const pipelineId = Buffer.from("analytics").toString("base64url");
  const commands: string[] = [];
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("request", (request) => {
    if (
      request.method() === "POST" &&
      /\/(trigger|materialize|run|preview)(\/stream)?$/.test(new URL(request.url()).pathname)
    )
      commands.push(request.url());
  });
  await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/canvas?result=inspect&editor=asset`);
  const mobile = info.project.name.includes("mobile");
  const openData = async () => {
    await (
      mobile
        ? page.getByRole("tab", { name: "Data", exact: true })
        : page.getByRole("button", { name: "Data Browser", exact: true })
    ).click();
  };
  await openData();
  await page.getByRole("button", { name: /Project files.*Files inside this project/ }).click();
  await page.getByRole("button", { name: "data", exact: true }).click();
  const useFile = page.getByRole("button", { name: "Use orders.csv in canvas", exact: true });
  const row = page.locator('[data-transfer-label="orders.csv"]');
  await expect(row).toHaveAttribute("draggable", "true");
  await expect(row.getByRole("link")).toHaveAttribute("draggable", "false");
  const target = page.getByRole("button", { name: /Create Load from file/ });
  if (mobile) {
    await useFile.click();
    await expect(target).toBeEnabled();
    await target.click();
  } else {
    const transfer = await page.evaluateHandle(() => new DataTransfer());
    await row.dispatchEvent("dragstart", { dataTransfer: transfer });
    await expect(target).toBeEnabled();
    expect(await transfer.evaluate((data) => data.types)).toEqual([
      "application/x-renart-data-browser",
    ]);
    expect(
      await transfer.evaluate((data) => data.getData("application/x-renart-data-browser")),
    ).not.toContain("orders.csv");
    await target.dispatchEvent("dragover", { dataTransfer: transfer });
    await target.dispatchEvent("drop", { dataTransfer: transfer });
    await row.dispatchEvent("dragend", { dataTransfer: transfer });
  }
  const dialog = page.getByRole("dialog", { name: "New asset", exact: true });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByLabel("Source connection", { exact: true })).toContainText("local");
  await expect(dialog.getByRole("button", { name: "Choose source file", exact: true })).toHaveText(
    "data/orders.csv",
  );
  await page.screenshot({ path: info.outputPath("project-file-review.png") });
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  const before = await (await page.request.get(`${liveApp.baseURL}/api/workspace`)).json();
  expect(before.pipelines.find((p: { id: string }) => p.id === pipelineId).assets).toHaveLength(2);
  expect(await readFile(file, "utf8")).toBe(original);
  if (mobile) await openData();
  await useFile.click();
  await target.click();
  await dialog.getByLabel("Asset name").fill("analytics.project_import");
  await dialog.getByLabel("Destination connection", { exact: true }).click();
  await page.getByRole("option", { name: "duckdb-default", exact: true }).click();
  await dialog.getByRole("button", { name: "Create", exact: true }).click();
  await expect(dialog).toBeHidden();
  const saved = await readFile(
    join(liveApp.workspaceDir, "analytics/assets/analytics/project_import.asset.yml"),
    "utf8",
  );
  expect(saved).toContain("type: load");
  expect(saved).toContain("source_connection: local");
  expect(saved).toContain("source_table: data/orders.csv");
  expect(saved).toContain("connection: duckdb-default");
  expect(await readFile(file, "utf8")).toBe(original);
  expect(new URL(page.url()).searchParams.get("result")).toBe("inspect");
  expect(commands).toEqual([]);
  expect(errors).toEqual([]);
  await page.screenshot({ path: info.outputPath("project-file-load.png") });
});

test("project files can be placed in a different existing prefix without changing their path", async ({
  page,
  liveApp,
  isMobile,
}) => {
  await mkdir(join(liveApp.workspaceDir, "data"));
  await copyFile(
    join(__dirname, "../../fixtures/data-browser/project-orders.csv"),
    join(liveApp.workspaceDir, "data/orders.csv"),
  );
  await writeFile(
    join(liveApp.workspaceDir, "analytics/assets/landing.sql"),
    "/* @bruin\nname: landing.anchor\ntype: duckdb.sql\nconnection: duckdb-default\n@bruin */\nselect 1 as id\n",
  );
  const pipeline = Buffer.from("analytics").toString("base64url");
  await page.goto(`${liveApp.baseURL}/pipelines/${pipeline}/canvas?result=inspect`);
  await expect(
    page.locator(".react-flow__node").filter({ hasText: "anchor" }).first(),
  ).toBeVisible();
  await (
    isMobile
      ? page.getByRole("tab", { name: "Data", exact: true })
      : page.getByRole("button", { name: "Data Browser", exact: true })
  ).click();
  await page.getByRole("button", { name: /Project files.*Files inside this project/ }).click();
  await page.getByRole("button", { name: "data", exact: true }).click();
  const target = page.getByRole("button", { name: "Create Load in landing group", exact: true });
  if (isMobile) {
    await page.getByRole("button", { name: "Use orders.csv in canvas", exact: true }).click();
    await target.click();
  } else {
    const transfer = await page.evaluateHandle(() => new DataTransfer());
    const row = page.locator('[data-transfer-label="orders.csv"]');
    await row.dispatchEvent("dragstart", { dataTransfer: transfer });
    await expect(target).toBeVisible();
    await expect(
      page.getByRole("button", { name: "Create Load in analytics group", exact: true }),
    ).toBeVisible();
    const bounds = (await target.boundingBox())!;
    await target.dispatchEvent("dragover", {
      dataTransfer: transfer,
      clientX: bounds.x + 10,
      clientY: bounds.y + 10,
    });
    await expect(target).toHaveAttribute("data-proximity", "near");
    await expect
      .poll(() =>
        target.evaluate((element) => {
          const group = element.closest(".react-flow__node")!;
          return (
            Number(getComputedStyle(group).zIndex) >
            Math.max(
              ...[...document.querySelectorAll(".react-flow__node-lineageAsset")].map(
                (node) => Number(getComputedStyle(node).zIndex) || 0,
              ),
            )
          );
        }),
      )
      .toBe(true);
    await target.dispatchEvent("drop", { dataTransfer: transfer });
    await row.dispatchEvent("dragend", { dataTransfer: transfer });
  }
  const dialog = page.getByRole("dialog", { name: "New asset", exact: true });
  await expect(dialog.getByLabel("Asset name")).toHaveValue(/^landing\./);
  const name = await dialog.getByLabel("Asset name").inputValue();
  await expect(dialog.getByRole("button", { name: "Choose source file", exact: true })).toHaveText(
    "data/orders.csv",
  );
  await dialog.getByLabel("Destination connection", { exact: true }).click();
  await page.getByRole("option", { name: "duckdb-default", exact: true }).click();
  await dialog.getByRole("button", { name: "Create", exact: true }).click();
  await expect(dialog).toBeHidden();
  const saved = await readFile(
    join(liveApp.workspaceDir, "analytics/assets", name.replaceAll(".", "/") + ".asset.yml"),
    "utf8",
  );
  expect(saved).toContain("source_table: data/orders.csv");
  // The asset name is derived from its path; generated YAML need not repeat it.
  const workspace = (await (
    await page.request.get(`${liveApp.baseURL}/api/workspace`)
  ).json()) as WorkspaceState;
  expect(
    workspace.pipelines.find((p) => p.id === pipeline)!.assets.find((a) => a.name === name),
  ).toMatchObject({
    type: "load",
    parameters: { source_connection: "local", source_table: "data/orders.csv" },
  });
  expect(new URL(page.url()).searchParams.get("result")).toBe("inspect");
});
