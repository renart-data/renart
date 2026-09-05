import { expect } from "@playwright/test";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import type { TypeCheckReport, ProjectListResponse } from "../../../lib/generated/api-types";
import { liveTest as test } from "../live-app-fixture";

const pipelineId = Buffer.from("analytics").toString("base64url");
const ordersId = Buffer.from("analytics/assets/analytics/orders.sql").toString("base64url");
const diagnosticId = Buffer.from("analytics/assets/analytics/diagnostic.sql").toString("base64url");

test.describe("routed UI navigation live", () => {
  test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

  test("addresses the existing run output tab without starting another run", async ({
    liveApp,
    page,
  }) => {
    const response = await page.request.post(
      `${liveApp.baseURL}/api/pipelines/${pipelineId}/trigger`,
      { data: { source: "working_tree", environment: "default" } },
    );
    expect(response.ok()).toBe(true);
    const { run } = await response.json();
    const commands: string[] = [];
    page.on("request", (request) => {
      if (/\/(trigger|reexecute|cancel)$/.test(new URL(request.url()).pathname))
        commands.push(request.url());
    });
    await page.goto(`${liveApp.baseURL}/runs/${run.id}`);
    const output = page.getByRole("tab", { name: "Output", exact: true });
    await expect(output).toBeVisible({ timeout: 20000 });
    await output.click();
    expect(new URL(page.url()).searchParams.get("run_tab")).toBe("output");
    await page.reload();
    await expect(output).toHaveAttribute("data-state", "active");
    await page.getByRole("tab", { name: "Events", exact: true }).click();
    await page.goBack();
    await expect(output).toHaveAttribute("data-state", "active");
    expect(commands).toEqual([]);
  });

  test("keeps Inspect and collapsed results while normal property navigation becomes a cold-tab link", async ({
    liveApp,
    page,
    browser,
  }) => {
    await writeFile(
      join(liveApp.workspaceDir, "analytics/assets/analytics/orders.sql"),
      `/* @bruin\nname: analytics.orders\ntype: duckdb.sql\ncolumns:\n  - name: total_amount\n    type: INTEGER\n@bruin */\nselect 42 as total_amount\n`,
    );
    await page.goto(
      `${liveApp.baseURL}/pipelines/${pipelineId}/assets/${ordersId}/code?result=inspect&editor=asset`,
    );
    const collapse = page.getByRole("button", { name: "Collapse results panel" });
    await expect(collapse).toBeVisible({ timeout: 20000 });
    await page
      .locator(".monaco-editor")
      .first()
      .evaluate((element) => element.setAttribute("data-same-asset-editor", "retained"));
    await collapse.click();
    if (test.info().project.name.includes("mobile"))
      await page.getByRole("button", { name: "Asset properties", exact: true }).click();
    const properties = page.getByTestId("asset-inspector").filter({ visible: true });
    await properties.getByRole("tab", { name: "Columns", exact: true }).click();
    await properties.getByRole("button", { name: "Edit column total_amount", exact: true }).click();
    await expect(properties.getByRole("textbox", { name: "Type", exact: true })).toBeFocused();
    await properties.getByRole("textbox", { name: "Description", exact: true }).click();
    await expect(
      properties.getByRole("textbox", { name: "Description", exact: true }),
    ).toBeFocused();
    const address = new URL(page.url());
    expect(JSON.parse(address.searchParams.get("detail")!).target.field).toBe("description");
    await expect(page.locator('[data-same-asset-editor="retained"]')).toHaveCount(1);
    expect(address.searchParams.get("result")).toBe("inspect");
    expect(address.searchParams.get("editor")).toBe("asset");
    expect(JSON.parse(address.searchParams.get("detail")!).target.column).toBe("total_amount");
    await expect(
      page.getByRole("button", { name: "Expand results panel", includeHidden: true }),
    ).toHaveCount(1);
    await expect(page.getByTestId("routed-column-definition")).toHaveCount(0);
    const context = await browser.newContext(test.info().project.use);
    try {
      const fresh = await context.newPage();
      await fresh.goto(address.href);
      await expect(
        fresh
          .getByTestId("asset-inspector")
          .filter({ visible: true })
          .getByRole("textbox", { name: "Description", exact: true }),
      ).toBeFocused({ timeout: 20000 });
      expect(new URL(fresh.url()).searchParams.get("result")).toBe("inspect");
    } finally {
      await context.close();
    }
  });

  test("opens saved notebook cells and presentation blocks in their actual editors", async ({
    liveApp,
    page,
  }) => {
    const directory = await (await page.request.get(`${liveApp.baseURL}/api/projects`)).json();
    const config = await (await page.request.get(`${liveApp.baseURL}/api/config`)).json();
    const created = await page.request.post(`${liveApp.baseURL}/api/notebooks`, {
      data: { title: "Routed notebook" },
    });
    expect(created.ok()).toBe(true);
    const notebook = (await created.json()).notebook;
    const cellResponse = await page.request.post(
      `${liveApp.baseURL}/api/notebooks/${notebook.id}/cells`,
      { data: { name: "stable_cell" } },
    );
    expect(cellResponse.ok()).toBe(true);
    const cell = (await cellResponse.json()).notebook.cells.find(
      (c: { name: string }) => c.name === "stable_cell",
    );
    await writeFile(
      join(liveApp.workspaceDir, "routed.dashboard.yml"),
      `version: 1\nid: routed\ntitle: Routed dashboard\nvisualizations:\n  - id: stable_plot\n    dataset: missing\n    definition:\n      version: 1\n      type: table\nlayout:\n  - visualization: stable_plot\n`,
    );
    const writes: string[] = [];
    page.on("request", (r) => {
      if (/\/(run|execute|materialize|preview)$/.test(new URL(r.url()).pathname))
        writes.push(r.url());
    });
    for (const target of [
      { kind: "notebook-cell", notebook_id: notebook.id, cell_id: cell.cell_id },
      {
        kind: "presentation",
        presentation_id: Buffer.from("routed.dashboard.yml").toString("base64url"),
        block_id: "stable_plot",
      },
    ]) {
      const url = new URL(`${liveApp.baseURL}/schedules/deployments`);
      url.searchParams.set("project", directory.default_project_id);
      url.searchParams.set(
        "detail",
        JSON.stringify({ v: 1, environment: config.default_environment, target }),
      );
      await page.goto(url.href);
      if (target.kind === "notebook-cell") {
        await expect(page.locator(`[data-notebook-cell-id="${cell.cell_id}"]`)).toBeFocused({
          timeout: 20000,
        });
        await expect(page.locator(".monaco-editor").first()).toBeVisible();
        expect(new URL(page.url()).pathname).toBe(`/notebooks/${notebook.id}`);
      } else {
        await expect(
          page
            .getByTestId("presentation-inspector")
            .filter({ visible: true })
            .getByRole("textbox", { name: "Visualization ID", exact: true }),
        ).toHaveValue("stable_plot", { timeout: 20000 });
        expect(new URL(page.url()).pathname).toContain("/dashboards/");
        if (test.info().project.name.includes("mobile")) await page.keyboard.press("Escape");
        await page.getByRole("tab", { name: "Definition", exact: true }).click();
        expect(new URL(page.url()).searchParams.get("presentation_editor")).toBe("definition");
        await page.reload();
        await expect(page.getByRole("tab", { name: "Definition", exact: true })).toHaveAttribute(
          "data-state",
          "active",
        );
      }
      url.searchParams.set(
        "detail",
        JSON.stringify({
          v: 1,
          environment: config.default_environment,
          target:
            target.kind === "notebook-cell"
              ? { ...target, cell_id: "deleted_cell" }
              : { ...target, block_id: "deleted_plot" },
        }),
      );
      await page.goto(url.href);
      await expect(page.getByRole("alert").filter({ hasText: "The linked" })).toContainText(
        "missing or ambiguous",
        { timeout: 10000 },
      );
    }
    expect(writes).toEqual([]);
  });

  test("resolves a data column in a cold tab without a preview and preserves the bookmark across revisions", async ({
    liveApp,
    page,
    browser,
  }) => {
    test.setTimeout(60000);
    await writeFile(join(liveApp.workspaceDir, "addressed.csv"), "id,Total\n1,42\n");
    const projects = await (await page.request.get(`${liveApp.baseURL}/api/projects`)).json();
    const config = await (await page.request.get(`${liveApp.baseURL}/api/config`)).json();
    const environment = config.default_environment;
    const target = {
      kind: "data-object",
      address: { source_kind: "local_files", path: "addressed.csv" },
      section: "schema",
      column: "Total",
    };
    const url = new URL(`${liveApp.baseURL}/pipelines/${pipelineId}/assets/${ordersId}/code`);
    url.searchParams.set("project", projects.default_project_id);
    url.searchParams.set("detail", JSON.stringify({ v: 1, environment, target }));
    const queries: string[] = [];
    page.on("request", (req) => {
      if (req.url().includes("data-browser/preview")) queries.push(req.url());
    });
    await page.goto(url.href);
    const detail = page.getByTestId("routed-data-object");
    await expect(detail.locator('[data-focused-column="true"]')).toBeFocused({ timeout: 20000 });
    await expect(detail.locator('[data-focused-column="true"]')).toContainText("Total");
    expect(queries).toEqual([]);
    const link = detail.getByRole("link", { name: "Total", exact: true });
    const href = await link.getAttribute("href");
    await writeFile(join(liveApp.workspaceDir, "revision-change.csv"), "id\n2\n");
    const context = await browser.newContext(test.info().project.use);
    try {
      const fresh = await context.newPage();
      fresh.on("request", (req) => {
        if (req.url().includes("data-browser/preview")) queries.push(req.url());
      });
      await fresh.goto(new URL(href!, liveApp.baseURL).href);
      await expect(fresh.locator('[data-focused-column="true"]')).toBeFocused({ timeout: 20000 });
      expect(queries).toEqual([]);
      const canonical = new URL(fresh.url());
      canonical.pathname = "/data";
      await fresh.goto(canonical.href);
      await expect(fresh.getByTestId("routed-data-object")).toHaveCount(1);
      await expect(fresh.locator('[data-focused-column="true"]')).toBeFocused();
      await fresh.getByRole("button", { name: "Preview rows", exact: true }).click();
      await expect(fresh.getByRole("grid", { name: "addressed.csv preview" })).toBeVisible();
      expect(queries).toHaveLength(1);
      await fresh.goBack();
      await expect(fresh.getByRole("tab", { name: /Columns/ })).toHaveAttribute(
        "data-state",
        "active",
      );
    } finally {
      await context.close();
    }
  });

  test("opens a connection field and cancels leaving its unsaved form", async ({
    liveApp,
    page,
  }) => {
    const projects = await (await page.request.get(`${liveApp.baseURL}/api/projects`)).json();
    const config = await (await page.request.get(`${liveApp.baseURL}/api/config`)).json();
    const env = config.environments.find(
      (e: { name: string }) => e.name === config.default_environment,
    );
    const connection = env.connections.find((c: { type: string }) => c.type === "duckdb");
    const type = config.connection_types.find(
      (t: { type_name: string }) => t.type_name === connection.type,
    );
    const field = type.fields.find(
      (f: { type: string; is_sensitive: boolean }) => f.type === "string" && !f.is_sensitive,
    );
    const url = new URL(`${liveApp.baseURL}/schedules/deployments`);
    url.searchParams.set("project", projects.default_project_id);
    url.searchParams.set(
      "detail",
      JSON.stringify({
        v: 1,
        environment: env.name,
        target: { kind: "connection", connection: connection.name, field: field.name },
      }),
    );
    const writes: string[] = [];
    page.on("request", (req) => {
      if (req.method() !== "GET" && req.url().includes("/api/") && !req.url().includes("resolve"))
        writes.push(req.url());
    });
    await page.goto(url.href);
    const form = page.getByRole("dialog", { name: connection.name, exact: true });
    const input = form.getByRole("textbox", { name: field.name, exact: true });
    await expect(input).toBeFocused({ timeout: 15000 });
    expect(writes).toEqual([]);
    await input.fill("unsaved-change");
    page.once("dialog", (dialog) => dialog.dismiss());
    await page.keyboard.press("Escape");
    await expect(form).toBeVisible();
    await expect(input).toHaveValue("unsaved-change");
    expect(new URL(page.url()).searchParams.has("detail")).toBe(true);
    page.once("dialog", (dialog) => dialog.accept());
    await page.keyboard.press("Escape");
    await expect(form).toHaveCount(0);
    expect(writes).toEqual([]);
  });

  test("opens the real column properties while preserving independent views and survives a cold tab", async ({
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
    const detail = page.getByTestId("asset-inspector").filter({ visible: true });
    await expect(detail).toBeVisible();
    await expect(detail.getByRole("textbox", { name: "Type", exact: true })).toBeFocused();
    await expect(detail.getByRole("textbox", { name: "Type", exact: true })).toHaveValue("VARCHAR");
    expect(new URL(page.url()).pathname).toContain(`/assets/${diagnosticId}/code`);
    expect(new URL(page.url()).searchParams.get("result")).toBe("typecheck");
    await expect(page.getByTestId("routed-column-definition")).toHaveCount(0);
    // Another asset owns another editor model; the original draft must return on Back.
    expect(
      await page.evaluate(() =>
        Object.entries(sessionStorage).filter(([key]) => key.startsWith("renart.workbench.")),
      ),
    ).toEqual(sidebarBefore);

    await page.goBack();
    expect(new URL(page.url()).pathname).toContain(`/assets/${ordersId}/code`);
    await expect(editor).toContainText("keep this editor");
    await page.goForward();
    await expect(detail.getByRole("textbox", { name: "Type", exact: true })).toBeFocused();
    // No navigation may invoke a metadata edit or schema inference command.
    expect(writeRequests.filter((url) => /transactions|columns/.test(url))).toEqual([]);
    await page.screenshot({ path: test.info().outputPath("routed-column.png"), fullPage: true });
    if (test.info().project.name.includes("mobile")) {
      await page.keyboard.press("Escape");
      await expect(page.getByRole("dialog", { name: "Asset properties" })).toHaveCount(0);
      expect(new URL(page.url()).searchParams.has("detail")).toBe(false);
    }

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
        fresh
          .getByTestId("asset-inspector")
          .filter({ visible: true })
          .getByRole("textbox", { name: "Type", exact: true }),
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
        runPage
          .getByTestId("asset-inspector")
          .filter({ visible: true })
          .getByRole("textbox", { name: "Type", exact: true }),
      ).toBeFocused({ timeout: 20000 });
      await expect(runPage.locator('[data-app-mode="build"]')).toBeVisible();
      await expect(runPage.locator(".monaco-editor").first()).toBeVisible();
      await runPage.close();
      const stale = new URL(href!, liveApp.baseURL);
      const staleDetail = JSON.parse(stale.searchParams.get("detail")!);
      staleDetail.target.column = "renamed_column";
      stale.searchParams.set("detail", JSON.stringify(staleDetail));
      await fresh.goto(stale.href);
      await expect(
        fresh.getByRole("alert").filter({ hasText: "missing or ambiguous" }),
      ).toBeVisible();
      await expect(
        fresh
          .getByTestId("asset-inspector")
          .filter({ visible: true })
          .getByRole("textbox", { name: "Type", exact: true }),
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
    const type = page
      .getByTestId("asset-inspector")
      .filter({ visible: true })
      .getByRole("textbox", { name: "Type", exact: true });
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
