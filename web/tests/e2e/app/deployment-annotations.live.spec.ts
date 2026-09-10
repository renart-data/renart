import { expect } from "@playwright/test";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import type { PipelinePlan } from "../../../lib/generated/api-types";
import { liveTest as test } from "../live-app-fixture";

const pipelineId = Buffer.from("analytics").toString("base64url");
const assetId = Buffer.from("analytics/assets/analytics/orders.sql").toString("base64url");
const query = "SELECT\n  100 AS order_id,\n  1 AS customer_id,\n  42 AS total_amount\n";
const header =
  "/* @bruin\nname: analytics.orders\ntype: duckdb.sql\nmaterialization:\n  type: view\n";
const columns =
  "columns:\n  - name: order_id\n    type: INTEGER\n  - name: customer_id\n    type: INTEGER\n  - name: total_amount\n    type: INTEGER\n";

test.describe("deployment annotations live", () => {
  test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

  for (const scenario of ["query changed", "new column", "new undeclared column"] as const) {
    test(`reviews ${scenario} without whole-query warning underlines`, async ({
      liveApp,
      page,
    }) => {
      test.setTimeout(90_000);
      const pageErrors: string[] = [];
      page.on("pageerror", (error) => pageErrors.push(error.message));
      await page.addInitScript(() => localStorage.setItem("renart-theme", "dark"));
      const declared = scenario === "new undeclared column";
      const before = `${header}${declared ? columns : ""}@bruin */\n${query}`;
      const after =
        scenario === "query changed"
          ? `${query}WHERE 1 = 0\n`
          : query.replace("  1 AS customer_id", "  'a@example.org' AS email,\n  1 AS customer_id");
      await writeFile(join(liveApp.workspaceDir, "analytics/assets/analytics/orders.sql"), before);
      await expect
        .poll(async () => {
          const workspace = await (
            await page.request.get(`${liveApp.baseURL}/api/workspace`)
          ).json();
          return workspace.pipelines
            .flatMap((pipeline: { assets: { id: string; content: string }[] }) => pipeline.assets)
            .find((asset: { id: string }) => asset.id === assetId)?.content;
        })
        .toBe(query.trim());
      const deployed = await page.request.post(
        `${liveApp.baseURL}/api/pipelines/${pipelineId}/deploy`,
        {
          data: {},
          headers: { Origin: liveApp.baseURL },
        },
      );
      expect(deployed.ok()).toBe(true);
      const saved = await page.request.put(
        `${liveApp.baseURL}/api/pipelines/${pipelineId}/assets/${assetId}`,
        {
          data: { content: after },
          headers: { Origin: liveApp.baseURL },
        },
      );
      expect(saved.ok()).toBe(true);
      await page.goto(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${assetId}/code`);
      await expect(page.locator(".view-lines").first()).toContainText(
        scenario === "query changed" ? "WHERE" : "email",
        { timeout: 15_000 },
      );
      const planned = page.waitForResponse(
        (response) => response.url().endsWith(`/api/pipelines/${pipelineId}/plan`) && response.ok(),
      );
      await page.getByRole("button", { name: /^Redeploy/ }).click();
      const plan = (await (await planned).json()) as PipelinePlan;
      const impact = plan.semantic_impact?.assets.find(
        (asset) => asset.name === "analytics.orders",
      );
      expect(impact?.complete).toBe(true);
      const dialog = page.getByTestId("pipeline-plan-sheet");
      const row = dialog.getByRole("button", { name: /assets\/analytics\/orders.sql/ });
      await expect(row).toContainText(
        scenario === "query changed" ? "Query changed" : "New column",
      );
      await row.click();
      const diff = dialog.getByTestId("deployment-file-diff");
      await expect(diff.locator(".monaco-diff-editor")).toBeVisible({ timeout: 15_000 });
      if (scenario === "query changed") {
        expect(impact?.columns).toEqual([]);
        await expect(diff.locator(".deployment-diff-warning")).toHaveCount(0);
        await expect(diff.locator(".deployment-diff-lens")).toHaveCount(0);
        await expect(diff.locator(".line-insert").first()).toBeVisible();
      } else {
        expect(impact?.columns).toMatchObject([{ after: { name: "email" }, after_index: 1 }]);
        expect(impact?.columns).toHaveLength(1);
        expect(impact?.columns[0].before).toBeUndefined();
        await expect
          .poll(async () =>
            (await diff.locator(".deployment-diff-info").allTextContents()).join(""),
          )
          .toContain("email");
        if (declared) {
          await expect(row).toContainText("1 warning");
          // Output-schema drift is an asset-scoped finding without a source
          // range. Keep it visible; do not invent an inline warning location.
          await expect(dialog.getByRole("list", { name: "Asset findings" })).toContainText(
            'SQL produces undeclared column "email".',
          );
          await expect(diff.locator(".deployment-diff-warning")).toHaveCount(0);
        } else {
          await expect(diff.locator(".deployment-diff-warning")).toHaveCount(0);
          await expect(diff.locator(".deployment-diff-info").first()).toHaveCSS(
            "border-bottom-width",
            "0px",
          );
          // Monaco splits injected labels across spans when mobile lines wrap.
          await expect
            .poll(async () =>
              (await diff.locator(".deployment-diff-lens-info").allTextContents())
                .join("")
                .replace(/\s+/g, " "),
            )
            .toContain("New column: email");
        }
      }
      const mobile = (page.viewportSize()?.width ?? 0) < 768;
      await expect(diff).toHaveAttribute("data-diff-layout", mobile ? "inline" : "split");
      if (mobile) await expect(diff.locator(".monaco-diff-editor")).not.toHaveClass(/side-by-side/);
      await page.screenshot({ path: test.info().outputPath("deployment-annotations.png") });
      await dialog.getByRole("button", { name: "Close", exact: true }).click();
      await expect(dialog).toBeHidden();
      // Closing and reopening must detach both models before disposal.
      await page.getByRole("button", { name: /^Redeploy/ }).click();
      await row.click();
      await expect(diff.locator(".monaco-diff-editor")).toBeVisible();
      await dialog.getByRole("button", { name: "Close", exact: true }).click();
      await expect(dialog).toBeHidden();
      expect(pageErrors).toEqual([]);
    });
  }
});
