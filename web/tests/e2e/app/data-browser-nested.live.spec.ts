import { expect } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { liveTest as test } from "../live-app-fixture";
import type { DataBrowserConnectionsResponse } from "../../../lib/generated/api-types";

test.use({
  fixtureName: "nested-browser-workspace",
  workspaceSubdirectory: "child",
  isolateUserConfig: true,
});

test("a child project lists parent connections without opening storage or including sibling settings", async ({
  page,
  liveApp,
  isMobile,
}) => {
  const config = join(liveApp.workspaceDir, "..", ".bruin.yml");
  const before = await readFile(config, "utf8");
  const response = await page.request.get(
    `${liveApp.baseURL}/api/data-browser/connections?environment=default`,
    { timeout: 10000 },
  );
  expect(response.ok(), await response.text()).toBe(true);
  const connections = ((await response.json()) as DataBrowserConnectionsResponse).connections;
  expect(connections.map((connection) => connection.name).sort()).toEqual([
    "Project files",
    "inherited-storage",
    "inherited-warehouse",
  ]);
  const pipeline = Buffer.from(".").toString("base64url");
  await page.goto(`${liveApp.baseURL}/pipelines/${pipeline}/canvas`);
  await (
    isMobile
      ? page.getByRole("tab", { name: "Data", exact: true })
      : page.getByRole("button", { name: "Data Browser", exact: true })
  ).click();
  await expect(page.getByRole("textbox", { name: "Search data browser" })).toBeFocused();
  await page.getByRole("button", { name: /inherited-warehouse.*DuckDB/ }).click();
  await expect(page.getByRole("button", { name: "parent Default", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Back", exact: true }).click();
  await page.getByRole("button", { name: /Project files.*Files inside this project/ }).click();
  await expect(page.getByRole("button", { name: "data", exact: true })).toBeVisible();
  await expect(page.getByText("parent-only.csv", { exact: true })).toHaveCount(0);
  await expect(page.getByTestId("data-browser-loading")).toHaveCount(0);
  expect(await readFile(config, "utf8")).toBe(before);
  await expect(readFile(join(liveApp.workspaceDir, ".bruin.yml"), "utf8")).rejects.toMatchObject({
    code: "ENOENT",
  });
});
