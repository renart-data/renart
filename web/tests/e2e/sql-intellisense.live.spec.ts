import { expect, Page } from "@playwright/test";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";

import { liveTest as test } from "./live-app-fixture";

test.describe("sql intellisense live", () => {
  test.use({ fixtureName: "configured-workspace" });

  test("requests parser-backed intellisense context from the live server", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/`);

    await openCustomersEditor(page, liveApp.baseURL);
    await replaceEditorContent(
      page,
      "select o.order_id\nfrom analytics.orders as o"
    );

    let body: unknown = null;
    await expect
      .poll(async () => {
        body = await page.evaluate(async () => {
          const response = await fetch("/api/sql/parse-context", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              asset_id: "YW5hbHl0aWNzL2Fzc2V0cy9jdXN0b21lcnMuc3Fs",
              content: "select o.order_id\nfrom analytics.orders as o",
              schema: [],
            }),
          });
          return await response.json();
        });

        return (body as { status?: string } | null)?.status ?? null;
      })
      .toBe("ok");

    const parseContext = body as {
      status: string;
      tables?: Array<{ name?: string; alias?: string }>;
      columns?: Array<{ qualifier?: string; name?: string }>;
    };

    expect(parseContext.tables).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ name: "analytics.orders", alias: "o" }),
      ])
    );
    expect(parseContext.columns).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ qualifier: "o", name: "o.order_id" }),
      ])
    );
  });

  test("shows resolved upstream columns in the SQL debug panel", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/`);

    await openCustomersEditor(page, liveApp.baseURL);
    const saveResponse = page.waitForResponse(
      (response) =>
        response.url().includes("/api/pipelines/") &&
        response.url().includes("/assets/YW5hbHl0aWNzL2Fzc2V0cy9jdXN0b21lcnMuc3Fs") &&
        response.request().method() === "PUT" &&
        (response.request().postData() ?? "").includes("from analytics.orders")
    );
    await replaceEditorContent(page, "select *\nfrom analytics.orders");
    await page.keyboard.press("ControlOrMeta+S");
    await saveResponse;
    await waitForWorkspaceAssetUpstreams(page, "analytics.customers", ["analytics.orders"]);
    await reopenCustomersEditor(page, liveApp.baseURL);
    await page.getByText("SQL column debug", { exact: true }).click();

    const debugPanel = page.locator("details").last();
    await expect(debugPanel.getByText("analytics.orders", { exact: true }).last()).toBeVisible();
    await expect(
      debugPanel.getByText(/analytics\.orders -> analytics\.orders · (declared|resolved-without-columns)/)
    ).toBeVisible();
    const hasResolvedColumns = await debugPanel
      .getByText("customer_id, order_id, total_amount", { exact: true })
      .count();
    if (hasResolvedColumns > 0) {
      await expect(
        debugPanel.getByText("customer_id, order_id, total_amount", { exact: true }).last()
      ).toBeVisible();
    } else {
      await expect(debugPanel.getByText("(resolved, but no columns)", { exact: true })).toBeVisible();
    }
  });

  test("navigates to the referenced asset on Ctrl+click", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/`);

    await openCustomersEditor(page, liveApp.baseURL);
    await replaceEditorContent(page, "select * from analytics.orders\n");

    const modifier = process.platform === "darwin" ? "Meta" : "Control";
    await page.keyboard.down(modifier);
    await clickEditorText(page, "analytics.orders");
    await page.keyboard.up(modifier);

    await expect(page.getByTestId("editor-asset-name")).toHaveText("analytics.orders");
    await expect(page.getByTestId("editor-asset-path")).toHaveText(
      "analytics/assets/orders.sql"
    );
  });

  test("shows quoted workspace path suggestions for DuckDB SQL", async ({
    liveApp,
    page,
  }) => {
    await writeFile(
      join(liveApp.workspaceDir, "duckdb-files", "customers.csv"),
      "customer_id,customer_name\n1,Ada\n",
      "utf8"
    );

    await page.goto(`${liveApp.baseURL}/`);
    await openCustomersEditor(page, liveApp.baseURL);

    const pathSuggestionsResponse = page.waitForResponse(
      (response) =>
        response.url().includes("/sql-path-suggestions") &&
        response.request().method() === "GET" &&
        response.url().includes(`prefix=${encodeURIComponent("./duckdb-files/cu")}`)
    );

    await replaceEditorContent(page, 'select * from "./duckdb-files/cu');

    const response = await pathSuggestionsResponse;
    const body = await response.json();

    expect(body.status).toBe("ok");
    expect(body.suggestions).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          value: "./duckdb-files/customers.csv",
          kind: "file",
        }),
      ])
    );

    await page.keyboard.press("ControlOrMeta+Space");

    const suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible();
    await expect(suggestWidget.getByText("./duckdb-files/customers.csv", { exact: true })).toBeVisible();
  });

  test("does not report DuckDB file paths as unresolved tables", async ({
    liveApp,
    page,
  }) => {
    await writeFile(
      join(liveApp.workspaceDir, "duckdb-files", "customers.csv"),
      "customer_id,customer_name\n1,Ada\n",
      "utf8"
    );

    await page.goto(`${liveApp.baseURL}/`);
    await openCustomersEditor(page, liveApp.baseURL);

    await replaceEditorContent(page, 'select * from "./duckdb-files/customers.csv"');

    let body: unknown = null;
    await expect
      .poll(async () => {
        body = await page.evaluate(async () => {
          const response = await fetch("/api/sql/parse-context", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              asset_id: "YW5hbHl0aWNzL2Fzc2V0cy9jdXN0b21lcnMuc3Fs",
              content: 'select * from "./duckdb-files/customers.csv"',
              schema: [],
            }),
          });
          return await response.json();
        });

        return (body as { status?: string } | null)?.status ?? null;
      })
      .toBe("ok");

    const parseContext = body as {
      diagnostics?: Array<{ message?: string }>;
    };

    expect(parseContext.diagnostics ?? []).not.toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          message: "Unresolved table: ./duckdb-files/customers.csv",
        }),
      ])
    );
  });

  test("shows latest inspect SQL error as Monaco diagnostics while content is unchanged", async ({
    liveApp,
    page,
  }) => {
    await page.goto(`${liveApp.baseURL}/`);
    await openCustomersEditor(page, liveApp.baseURL);

    await replaceEditorContent(
      page,
      'select * from finances.raw_downstream_downstream_downstream'
    );
    const saveResponse = page.waitForResponse(
      (response) =>
        response.url().includes("/api/pipelines/") &&
        response.url().includes("/assets/") &&
        response.request().method() === "PUT"
    );
    await page.keyboard.press("ControlOrMeta+S");
    await saveResponse;
    const inspectResponse = page.waitForResponse(async (response) => {
      if (
        !response.url().includes("/api/assets/") ||
        !response.url().includes("/inspect") ||
        response.request().method() !== "GET"
      ) {
        return false;
      }

      try {
        const body = await response.json();
        return body.status === "error";
      } catch {
        return false;
      }
    });
    await page.keyboard.press("ControlOrMeta+Enter");

    const response = await inspectResponse;
    const body = await response.json();

    expect(body.status).toBe("error");
    expect(body.raw_output).toContain("Catalog Error");
    expect(body.raw_output).toContain("LINE 1:");

    await expect
      .poll(async () => {
        return await page.evaluate(() => {
          const monaco = (window as typeof window & {
            monaco?: {
              editor: {
                getModels(): Array<{
                  uri: { toString(): string };
                }>;
                getModelMarkers(args: { resource?: { toString(): string } }): Array<{ message: string }>;
              };
            };
          }).monaco;

          if (!monaco) {
            return [];
          }

          const models = monaco.editor.getModels();
          for (const model of models) {
            const markers = monaco.editor.getModelMarkers({ resource: model.uri });
            if (markers.length > 0) {
              return markers.map((marker) => marker.message);
            }
          }

          return [];
        });
      }, { timeout: 15000 })
      .toEqual(expect.arrayContaining([expect.stringContaining("Catalog Error")]));
  });

  test("renders Jinja ghost text and completions in the SQL editor", async ({
    liveApp,
    page,
  }) => {
    test.skip(test.info().project.name.includes("mobile"), "Monaco injected text DOM is only stable in the desktop editor.");

    await writeFile(
      join(liveApp.workspaceDir, "analytics", "pipeline.yml"),
      [
        "name: analytics",
        "schedule: daily",
        "start_date: \"2024-01-01\"",
        "",
        "default_connections:",
        "  duckdb: duckdb-default",
        "",
        "variables:",
        "  run_mode:",
        "    type: string",
        "    default: incremental",
        "",
      ].join("\n"),
      "utf8"
    );

    await page.goto(`${liveApp.baseURL}/`);
    await openCustomersEditor(page, liveApp.baseURL);

    const renderResponse = page.waitForResponse(
      (response) =>
        response.url().includes("/api/assets/") &&
        response.url().includes("/render-jinja") &&
        response.request().method() === "POST"
    );

    await replaceEditorContentByInsertText(
      page,
      "select * from analytics.orders\nwhere dt = '{{ end_date }}'\nand mode = '{{ var.run_mode }}'"
    );
    const response = await renderResponse;
    const body = await response.json();
    expect(body.status).toBe("ok");
    expect(body.spans).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ expression: "end_date", rendered_text: expect.stringMatching(/\d{4}-\d{2}-\d{2}/) }),
        expect.objectContaining({ expression: "var.run_mode", rendered_text: "incremental" }),
      ])
    );

    await expect(page.locator(".bruin-jinja-rendered-ghost").first()).toBeVisible({ timeout: 10000 });
    await expect(page.locator(".bruin-jinja-rendered-ghost").filter({ hasText: /\d{4}-\d{2}-\d{2}/ }).first()).toBeVisible();
    await expect
      .poll(async () => {
        return await page.evaluate(() => {
          const monaco = (window as typeof window & {
            monaco?: any;
          }).monaco;
          const editor = monaco?.editor.getEditors?.()[0];
          const model = editor?.getModel();
          if (!monaco || !model) return [];
          return monaco.editor.getModelMarkers({ resource: model.uri }).map((marker: { message: string }) => marker.message);
        });
      }, { timeout: 10000 })
      .not.toEqual(expect.arrayContaining([expect.stringContaining("syntax error")]));

    await replaceEditorContentByInsertText(page, "select '{{ var. }}'");
    await setEditorPositionAfterText(page, "var.");
    await page.keyboard.press("ControlOrMeta+Space");
    const suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible();
    await expect(suggestWidget.getByText("run_mode", { exact: true })).toBeVisible();
  });

  test("suggests Jinja expressions inside statement blocks", async ({
    liveApp,
    page,
  }) => {
    test.skip(test.info().project.name.includes("mobile"), "Desktop suggest widget exposes stable Monaco completion DOM.");

    await writeFile(
      join(liveApp.workspaceDir, "analytics", "pipeline.yml"),
      [
        "name: analytics",
        "schedule: daily",
        "start_date: \"2024-01-01\"",
        "",
        "default_connections:",
        "  duckdb: duckdb-default",
        "",
        "variables:",
        "  days:",
        "    type: array",
        "    default: [1, 3, 7]",
        "  run_mode:",
        "    type: string",
        "    default: incremental",
        "",
      ].join("\n"),
      "utf8"
    );

    await page.goto(`${liveApp.baseURL}/`);
    await openCustomersEditor(page, liveApp.baseURL);

    await replaceEditorContentByInsertText(page, "{% if start_date |  %}\nselect 1\n{% endif %}");
    await setEditorPositionAfterText(page, "| ");
    await page.keyboard.press("ControlOrMeta+Space");
    let suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible();
    await expect(suggestWidget.getByText("add_days", { exact: true })).toBeVisible();
    await page.keyboard.press("Escape");

    await replaceEditorContentByInsertText(page, "{% if var. %}\nselect 1\n{% endif %}");
    await setEditorPositionAfterText(page, "var.");
    await page.keyboard.press("ControlOrMeta+Space");
    suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible();
    await expect(suggestWidget.getByText("run_mode", { exact: true })).toBeVisible();
    await expect(suggestWidget.getByText("days", { exact: true })).toBeVisible();
    await page.keyboard.press("Escape");

    await replaceEditorContentByInsertText(page, "{% for day in  %}\nselect {{ day }}\n{% endfor %}");
    await setEditorPositionAfterText(page, "in ");
    await page.keyboard.press("ControlOrMeta+Space");
    suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible();
    await expect(suggestWidget.getByText("var.days", { exact: true })).toBeVisible();
  });
});

test.describe("sql intellisense ranking live", () => {
  test.use({ fixtureName: "sql-intellisense-ranking-workspace" });

  test("collapses matching table and asset suggestions into one combined entry", async ({
    liveApp,
    page,
  }) => {
    test.skip(test.info().project.name.includes("mobile"), "Desktop suggest widget exposes stable combined-entry metadata.");

    await page.goto(`${liveApp.baseURL}/?pipeline=YW5hbHl0aWNz`);

    if (test.info().project.name.includes("mobile")) {
      await page.goto(`${liveApp.baseURL}/?pipeline=YW5hbHl0aWNz&asset=YW5hbHl0aWNzL2Fzc2V0cy9kZXBlbmRlbmNpZXMuc3Fs`);
      const editorDialog = page.getByRole("dialog", { name: "Asset Editor" });
      if (!(await editorDialog.isVisible().catch(() => false))) {
        await page.getByRole("button", { name: "Edit asset" }).click();
      }
    } else {
      await page.getByRole("link", { name: "analytics.dependencies" }).click();
    }
    await page.getByRole("button", { name: "Materialize", exact: true }).click();
    await expect(page.getByText("Asset: analytics.dependencies", { exact: true })).toBeVisible({
      timeout: 15000,
    });

    if (test.info().project.name.includes("mobile")) {
      await page.goto(`${liveApp.baseURL}/?pipeline=bWFydHM&asset=bWFydHMvYXNzZXRzL2RlcGVuZGVuY2llcy5zcWw`);
      const editorDialog = page.getByRole("dialog", { name: "Asset Editor" });
      if (!(await editorDialog.isVisible().catch(() => false))) {
        await page.getByRole("button", { name: "Edit asset" }).click();
      }
    } else {
      await page.getByRole("link", { name: "marts" }).click();
      await page.getByRole("link", { name: "marts.dependencies" }).click();
    }
    await page.getByRole("button", { name: "Materialize", exact: true }).click();
    await expect(page.getByText("Asset: marts.dependencies", { exact: true })).toBeVisible({
      timeout: 15000,
    });

    await openCustomersEditor(page, liveApp.baseURL);
    await replaceEditorContent(page, "select * from dependen");
    await page.keyboard.press("ControlOrMeta+Space");

    const suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible();

    await expect(suggestWidget.getByText("analytics.dependencies", { exact: true })).toHaveCount(1);
    await expect(
      suggestWidget.getByRole("listitem", {
        name: /analytics\.dependencies, Table \+ Asset \(analytics\.dependencies\), Class/,
      }),
    ).toBeVisible();
    await expect(
      suggestWidget.getByRole("listitem", {
        name: /marts\.dependencies, (Asset|Table \+ Asset) \(marts\.dependencies\), Module/,
      }),
    ).toBeVisible();
  });
});

async function openCustomersEditor(page: Page, baseURL: string) {
  const isMobile = test.info().project.name.includes("mobile");
  if (isMobile) {
    await page.goto(`${baseURL}/?pipeline=YW5hbHl0aWNz&asset=YW5hbHl0aWNzL2Fzc2V0cy9jdXN0b21lcnMuc3Fs`);
    await expect(page).toHaveTitle("analytics.customers · analytics · Renart");
    const editorDialog = page.getByRole("dialog", { name: "Asset Editor" });
    if (!(await editorDialog.isVisible().catch(() => false))) {
      const editButton = page.getByRole("button", { name: "Edit asset" });
      if (await editButton.isVisible().catch(() => false)) {
        await editButton.click();
      }
    }
    await expect(editorDialog).toBeVisible();
    await expect(page.getByTestId("editor-asset-name")).toHaveText("analytics.customers");
    await waitForEditorReady(page);
  } else {
    await page.goto(`${baseURL}/?pipeline=YW5hbHl0aWNz&asset=YW5hbHl0aWNzL2Fzc2V0cy9jdXN0b21lcnMuc3Fs`);
    const editorAssetName = page.getByTestId("editor-asset-name");
    if (!(await editorAssetName.isVisible().catch(() => false))) {
      const analyticsLink = page.getByRole("link", { name: "analytics", exact: true });
      await expect(analyticsLink).toBeVisible({ timeout: 15000 });
      await analyticsLink.click();
    }

    await expect(editorAssetName).toHaveText("analytics.customers", { timeout: 15000 });
    await waitForEditorReady(page);
  }
}

async function reopenCustomersEditor(page: Page, baseURL: string) {
  if (test.info().project.name.includes("mobile")) {
    await openCustomersEditor(page, baseURL);
    return;
  }

  await page.getByRole("link", { name: "analytics.orders" }).click();
  await openCustomersEditor(page, baseURL);
}

async function replaceEditorContentByInsertText(
  page: Page,
  content: string
) {
  const editor = await waitForEditorReady(page);
  await editor.click();
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.insertText(content);
}

async function setEditorPositionAfterText(page: Page, text: string) {
  await page.evaluate((needle) => {
    const monaco = (window as typeof window & {
      monaco?: any;
    }).monaco;
    const editor = monaco?.editor.getEditors?.()[0];
    const model = editor?.getModel();
    if (!monaco || !editor || !model) return;
    const value = model.getValue();
    const offset = value.indexOf(needle);
    if (offset < 0) return;
    const position = model.getPositionAt(offset + needle.length);
    editor.focus();
    editor.setPosition(position);
  }, text);
}

async function replaceEditorContent(
  page: Page,
  content: string
) {
  const editor = await waitForEditorReady(page);
  await editor.click();
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type(content);
}

async function waitForEditorReady(page: Page) {
  const editor = page.locator(".monaco-editor").first();
  await expect(page.getByTestId("editor-asset-name")).toHaveText(/analytics\./, { timeout: 15000 });
  await expect(editor).toBeVisible({ timeout: 15000 });
  await expect(page.locator(".view-lines").first()).toBeVisible({ timeout: 15000 });
  return editor;
}

async function waitForWorkspaceAssetUpstreams(
  page: Page,
  assetName: string,
  expectedUpstreams: string[]
) {
  const sortedExpected = [...expectedUpstreams].sort();
  await expect
    .poll(async () => {
      const upstreams = await page.evaluate(async (targetAssetName) => {
        const response = await fetch("/api/workspace", { cache: "no-store" });
        const workspace = (await response.json()) as {
          pipelines?: Array<{
            assets?: Array<{ name?: string; upstreams?: string[] }>;
          }>;
        };

        for (const pipeline of workspace.pipelines ?? []) {
          for (const asset of pipeline.assets ?? []) {
            if (asset.name === targetAssetName) {
              return asset.upstreams ?? [];
            }
          }
        }

        return null;
      }, assetName);

      return upstreams ? [...upstreams].sort() : null;
    }, { timeout: 15000 })
    .toEqual(sortedExpected);
}

async function clickEditorLine(page: Page, text: string) {
  const line = page.locator(".view-line").filter({ hasText: text }).first();
  await line.click();
}

async function clickEditorText(page: Page, text: string) {
  const line = page.locator(".view-line").filter({ hasText: text }).first();
  await line.click();
}
