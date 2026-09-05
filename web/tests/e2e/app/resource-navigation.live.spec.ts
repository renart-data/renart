import { expect } from "@playwright/test";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import type { TypeCheckReport, ProjectListResponse } from "../../../lib/generated/api-types";
import { liveTest as test } from "../live-app-fixture";

const pipelineId = Buffer.from("analytics").toString("base64url");
const ordersId = Buffer.from("analytics/assets/analytics/orders.sql").toString("base64url");
const diagnosticId = Buffer.from("analytics/assets/analytics/diagnostic.sql").toString("base64url");

test.describe("routed diagnostic details live", () => {
  test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

  test("opens an exact column without changing the primary editor and survives a cold tab", async ({
    liveApp,
    page,
    browser,
  }) => {
    test.setTimeout(90000);
    await writeFile(
      join(liveApp.workspaceDir, "analytics/assets/analytics/diagnostic.sql"),
      `/* @bruin
name: analytics.diagnostic
type: duckdb.sql
materialization:
  type: view
columns:
  - name: total_amount
    type: VARCHAR
    meta:
      renart_owned: type
@bruin */
select 1 as total_amount
`,
    );
    await expect
      .poll(
        async () => {
          const report = (await (
            await page.request.get(`${liveApp.baseURL}/api/pipelines/${pipelineId}/type-check`)
          ).json()) as TypeCheckReport;
          return report.assets
            ?.flatMap((asset) => asset.findings)
            .find((finding) => finding.target?.asset_id === diagnosticId)?.target?.column;
        },
        { timeout: 20000 },
      )
      .toBe("total_amount");
    const directory = (await (
      await page.request.get(`${liveApp.baseURL}/api/projects`)
    ).json()) as ProjectListResponse;
    await page.goto(
      `${liveApp.baseURL}/pipelines/${pipelineId}/assets/${ordersId}/code?result=typecheck&editor=asset`,
    );
    const link = page.getByRole("link", { name: "Edit type of total_amount" }).first();
    await expect(link).toBeVisible({ timeout: 20000 });
    const href = await link.getAttribute("href");
    expect(href).toBeTruthy();
    expect(new URL(href!, liveApp.baseURL).searchParams.get("project")).toBe(
      directory.default_project_id,
    );

    const editor = page.locator(".monaco-editor").first();
    await expect(editor).toBeVisible();
    await editor.evaluate((element) => {
      element.setAttribute("data-navigation-retained", "yes");
    });
    await editor.click();
    await page.keyboard.press("ControlOrMeta+End");
    await page.keyboard.type("\n-- keep this editor");
    if (!test.info().project.name.includes("mobile")) {
      await page.getByRole("button", { name: "Data Browser", exact: true }).click();
      await page.getByRole("button", { name: "Data Browser", exact: true }).click();
    }
    const sidebarBefore = await page.evaluate(() =>
      Object.entries(sessionStorage).filter(([key]) => key.startsWith("renart.workbench.")),
    );
    const writeRequests: string[] = [];
    page.on("request", (request) => {
      if (
        /\/api\/.*\/assets\//.test(request.url()) &&
        ["POST", "PUT", "DELETE"].includes(request.method()) &&
        !/render|inspect/.test(request.url())
      )
        writeRequests.push(request.url());
    });
    await link.click();
    const detail = page.getByTestId("routed-column-definition");
    await expect(detail).toBeVisible();
    await expect(detail.getByLabel("Type", { exact: true })).toBeFocused();
    await expect(detail.getByLabel("Type", { exact: true })).toHaveValue("VARCHAR");
    expect(new URL(page.url()).pathname).toContain(`/assets/${ordersId}/code`);
    await expect(page.locator('[data-navigation-retained="yes"]')).toHaveCount(1);
    expect(
      await page.evaluate(() =>
        Object.entries(sessionStorage).filter(([key]) => key.startsWith("renart.workbench.")),
      ),
    ).toEqual(sidebarBefore);

    await page.goBack();
    await expect(detail).toHaveCount(0);
    await expect(editor).toContainText("keep this editor");
    await page.goForward();
    await expect(detail.getByLabel("Type", { exact: true })).toBeFocused();
    // No navigation may invoke a metadata edit or schema inference command.
    expect(writeRequests.filter((url) => /transactions|columns/.test(url))).toEqual([]);
    await page.screenshot({ path: test.info().outputPath("routed-column.png"), fullPage: true });
    await page.keyboard.press("Escape");
    await expect(detail).toHaveCount(0);
    expect(new URL(page.url()).searchParams.has("detail")).toBe(false);

    const context = await browser.newContext(test.info().project.use);
    try {
      const fresh = await context.newPage();
      await fresh.addInitScript(() => {
        sessionStorage.setItem("renart.project", "wrong-project-pin");
      });
      const workspaceRequests: string[] = [];
      fresh.on("request", (request) => {
        if (/\/api\/(?:projects\/[^/]+\/)?(?:workspace|events)(?:[?]|$)/.test(request.url()))
          workspaceRequests.push(request.url());
      });
      await fresh.goto(new URL(href!, liveApp.baseURL).href);
      await expect(
        fresh.getByTestId("routed-column-definition").getByLabel("Type", { exact: true }),
      ).toBeFocused({ timeout: 20000 });
      expect(workspaceRequests.length).toBeGreaterThan(0);
      expect(
        workspaceRequests.every((url) =>
          url.includes(`/api/projects/${directory.default_project_id}/`),
        ),
      ).toBe(true);
      const runURL = new URL(href!, liveApp.baseURL);
      runURL.pathname = "/schedules/deployments";
      const runPage = await context.newPage();
      await runPage.goto(runURL.href);
      await expect(
        runPage.getByTestId("routed-column-definition").getByLabel("Type", { exact: true }),
      ).toBeFocused({ timeout: 20000 });
      await expect(runPage.locator('[data-app-mode="run"]')).toBeVisible();
      await expect(runPage.locator(".monaco-editor")).toHaveCount(0);
      await runPage.close();
      const stale = new URL(href!, liveApp.baseURL);
      const staleDetail = JSON.parse(stale.searchParams.get("detail")!);
      staleDetail.target.column = "renamed_column";
      stale.searchParams.set("detail", JSON.stringify(staleDetail));
      await fresh.goto(stale.href);
      await expect(
        fresh.getByRole("alert").filter({ hasText: "renamed, removed, or is ambiguous" }),
      ).toBeVisible();
      await expect(
        fresh.getByTestId("routed-column-definition").getByLabel("Type", { exact: true }),
      ).toHaveCount(0);
      const missing = new URL(href!, liveApp.baseURL);
      missing.searchParams.set("project", "missing-project");
      workspaceRequests.length = 0;
      await fresh.goto(missing.href);
      await expect(fresh.getByText(/project in this link is unavailable/)).toBeVisible();
      expect(workspaceRequests).toEqual([]);
    } finally {
      await context.close();
    }
  });

  test("opens a deployment warning as a link and saves the explicit type correction", async ({
    liveApp,
    page,
  }) => {
    test.setTimeout(60000);
    await writeFile(
      join(liveApp.workspaceDir, "analytics/assets/analytics/diagnostic.sql"),
      `/* @bruin
name: analytics.diagnostic
type: duckdb.sql
materialization:
  type: view
columns:
  - name: total_amount
    type: VARCHAR
    meta:
      renart_owned: type
@bruin */
select 1 as total_amount
`,
    );
    await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${ordersId}/code`);
    await expect(page.getByRole("button", { name: /^Deploy/ }).first()).toBeVisible({
      timeout: 15000,
    });
    await page
      .getByRole("button", { name: /^Deploy/ })
      .first()
      .click();
    const dialog = page.getByTestId("pipeline-plan-sheet");
    await expect(dialog).toBeVisible();
    await dialog.getByRole("button", { name: /assets\/analytics\/diagnostic.sql/ }).click();
    const link = dialog.getByRole("link", { name: "Edit type of total_amount" });
    await expect(link).toBeVisible();
    expect(await link.getAttribute("href")).toContain("detail=");
    await link.click();
    await expect(dialog).toBeHidden();
    const type = page.getByTestId("routed-column-definition").getByLabel("Type", { exact: true });
    await expect(type).toBeFocused();
    await type.fill("INTEGER");
    await type.press("Enter");
    await expect
      .poll(async () => {
        const report = (await (
          await page.request.get(`${liveApp.baseURL}/api/pipelines/${pipelineId}/type-check`)
        ).json()) as TypeCheckReport;
        return report.assets
          .find((asset) => asset.id === diagnosticId)
          ?.findings.some((finding) => finding.code === "declared-column-type-drift");
      })
      .toBe(false);
    await expect(type).toHaveValue("INTEGER");
  });
});
