import { expect, Page } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { liveTest as test } from "../live-app-fixture";

// The app asset editor drives SQL intellisense through the server LSP
// (useSQLLSP). Diagnostics (unresolved relation/column, circular dependency,
// syntax, rendered-template) and completions (relations in the FROM clause,
// alias columns) all come from the LSP; the diagnostic wording is the LSP's
// (e.g. "Unresolved column: x", not a "Did you mean" quick-fix phrasing).
// Column-not-materialized warnings and inspect-error markers are intentionally
// not surfaced in this editor.

const analyticsPipelineId = Buffer.from("analytics").toString("base64url");
const customersAssetId = Buffer.from("analytics/assets/analytics/customers.sql").toString(
  "base64url",
);
const ordersAssetId = Buffer.from("analytics/assets/analytics/orders.sql").toString("base64url");

test.describe("sql intellisense live", () => {
  test.use({ fixtureName: "configured-workspace" });

  test("requests parser-backed intellisense context from the live server", async ({
    liveApp,
    page,
  }) => {
    await openCustomersEditor(page, liveApp.baseURL);
    await replaceEditorContent(page, "select o.order_id\nfrom analytics.orders as o");

    let body: unknown = null;
    await expect
      .poll(async () => {
        body = await page.evaluate(async () => {
          const response = await fetch("/api/sql/parse-context", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({
              asset_id: "YW5hbHl0aWNzL2Fzc2V0cy9hbmFseXRpY3MvY3VzdG9tZXJzLnNxbA",
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
      expect.arrayContaining([expect.objectContaining({ name: "analytics.orders", alias: "o" })]),
    );
    expect(parseContext.columns).toEqual(
      expect.arrayContaining([expect.objectContaining({ qualifier: "o", name: "o.order_id" })]),
    );
  });

  test("navigates to the referenced asset on Ctrl+click", async ({ liveApp, page }) => {
    await openCustomersEditor(page, liveApp.baseURL);
    await replaceEditorContent(page, "select * from analytics.orders\n");

    const viewLines = page.locator(".view-lines").first();
    const upstreamToken = viewLines.locator("span", { hasText: "orders" }).last();
    await expect
      .poll(
        async () => {
          await upstreamToken.click({ modifiers: ["ControlOrMeta"] });
          return page.url();
        },
        { timeout: 15000 },
      )
      .toContain(`/assets/${ordersAssetId}/code`);

    await expect(page.locator(".view-lines").first()).toContainText("order_id", {
      timeout: 15000,
    });
  });

  test("shows quoted workspace path suggestions for DuckDB SQL", async ({ liveApp, page }) => {
    await writeFile(
      join(liveApp.workspaceDir, "duckdb-files", "customers.csv"),
      "customer_id,customer_name\n1,Ada\n",
      "utf8",
    );

    await openCustomersEditor(page, liveApp.baseURL);

    // The path provider is queried per directory segment (the returned range
    // covers the segment, so Monaco filters the trailing prefix client-side).
    // Assert on the directory-prefix request that actually fires, then that the
    // filtered widget still surfaces the file for the fully-typed prefix.
    const pathSuggestionsResponse = page.waitForResponse(
      (response) =>
        response.url().includes("/sql-path-suggestions") &&
        response.request().method() === "GET" &&
        response.url().includes(`prefix=${encodeURIComponent("./duckdb-files/")}`),
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
      ]),
    );

    const suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible();
    await expect(
      suggestWidget.getByText("./duckdb-files/customers.csv", { exact: true }),
    ).toBeVisible();
  });

  test("executes relative DuckDB file queries from the workspace root", async ({
    liveApp,
    request,
  }) => {
    await writeFile(
      join(liveApp.workspaceDir, "workspace-relative.csv"),
      "customer_id,customer_name\n41,Workspace Root\n",
      "utf8",
    );

    const response = await request.post(`${liveApp.baseURL}/api/sql/query`, {
      data: {
        connection: "duckdb-default",
        environment: "default",
        query: 'select customer_id, customer_name from "./workspace-relative.csv"',
      },
    });
    expect(response.ok(), await response.text()).toBe(true);
    expect(await response.json()).toMatchObject({
      status: "ok",
      columns: ["customer_id", "customer_name"],
      rows: [{ customer_id: 41, customer_name: "Workspace Root" }],
    });
  });

  test("does not report DuckDB file paths as unresolved tables", async ({ liveApp, page }) => {
    await writeFile(
      join(liveApp.workspaceDir, "duckdb-files", "customers.csv"),
      "customer_id,customer_name\n1,Ada\n",
      "utf8",
    );

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
              asset_id: "YW5hbHl0aWNzL2Fzc2V0cy9hbmFseXRpY3MvY3VzdG9tZXJzLnNxbA",
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
      ]),
    );
  });

  test("warns when a Bruin-defined column is missing from discovered table columns", async ({
    liveApp,
    page,
  }) => {
    await openCustomersEditor(page, liveApp.baseURL);

    const body = await page.evaluate(async () => {
      const response = await fetch("/api/sql/parse-context", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          asset_id: "YW5hbHl0aWNzL2Fzc2V0cy9hbmFseXRpY3MvY3VzdG9tZXJzLnNxbA",
          content:
            "with blub as (select *, 1 as schabla from quickstart.range_100)\n\n" +
            "select range, schabla, (select test from quickstart.test), bla from blub",
          schema: [
            {
              name: "quickstart.range_100",
              columns: [
                {
                  name: "range",
                  type: "bigint",
                  source_methods: ["workspace-load"],
                },
                {
                  name: "bla",
                  type: "integer",
                  source_methods: ["asset-sql-definition"],
                },
              ],
            },
            {
              name: "range_100",
              columns: [
                {
                  name: "range",
                  type: "bigint",
                  source_methods: ["connection-column-discovery"],
                },
              ],
            },
            {
              name: "quickstart.range_100",
              columns: [
                {
                  name: "bla",
                  type: "integer",
                  source_methods: ["asset-inspect"],
                },
              ],
            },
            {
              name: "quickstart.test",
              columns: [
                {
                  name: "test",
                  type: "integer",
                  source_methods: ["workspace-load", "connection-column-discovery"],
                },
              ],
            },
          ],
        }),
      });
      return await response.json();
    });

    const diagnostics =
      (
        body as {
          status?: string;
          diagnostics?: Array<{ message?: string; severity?: string }>;
        }
      ).diagnostics ?? [];

    expect((body as { status?: string }).status).toBe("ok");
    expect(diagnostics).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          severity: "warning",
          message:
            "Column 'bla' is defined in the asset 'quickstart.range_100', but it has not been materialized yet.",
        }),
      ]),
    );
    expect(diagnostics).not.toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          severity: "error",
          message: "Unresolved column: bla",
        }),
      ]),
    );
  });

  test("shows parser syntax errors as Monaco diagnostics", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Monaco diagnostics are only stable in the desktop editor.",
    );

    await openCustomersEditor(page, liveApp.baseURL);
    await replaceEditorContentByInsertText(
      page,
      [
        "SELECT",
        "  customer_id",
        "FROM analytics.customers",
        "WHERE",
        "  customer_id = 1 AND customer_id = 1",
        "  >   -- dangling comparison operator",
      ].join("\n"),
    );

    await expect
      .poll(async () => getEditorMarkers(page), { timeout: 15000 })
      .toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            message: expect.stringContaining("Unexpected token: Gt"),
            severity: 8,
          }),
        ]),
      );
  });

  test("uses SQL LSP completions in the Monaco SQL editor", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Desktop suggest widget exposes stable Monaco completion DOM.",
    );

    await openCustomersEditor(page, liveApp.baseURL);
    await replaceEditorContentByInsertText(page, "select o.\nfrom analytics.orders o");
    await setEditorPositionAfterText(page, "o.");

    const completionResponse = await page.request.post(
      `${liveApp.baseURL}/api/sql/lsp/completions`,
      {
        data: {
          asset_id: "YW5hbHl0aWNzL2Fzc2V0cy9hbmFseXRpY3MvY3VzdG9tZXJzLnNxbA",
          content: "select o.\nfrom analytics.orders o",
          position: { line: 0, character: "select o.".length },
        },
      },
    );
    await page.keyboard.press("ControlOrMeta+Space");
    const body = (await completionResponse.json()) as {
      status?: string;
      completions?: Array<{ label?: string }>;
    };

    expect(body.status).toBe("ok");
    expect(body.completions ?? []).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ label: "total_amount" }),
        expect.objectContaining({ label: "order_id" }),
      ]),
    );
    await expectVisibleSuggestText(page, "total_amount");
  });

  test("completes VALUES aliases and DESCRIBE result columns", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Desktop suggest widget exposes stable Monaco completion DOM.",
    );

    await openCustomersEditor(page, liveApp.baseURL);
    await replaceEditorContentByInsertText(page, "select \nfrom (values (1, 2), (3, 4)) n(a, b)");
    await setEditorPositionAfterText(page, "select ");
    await page.keyboard.press("ControlOrMeta+Space");
    await expectVisibleSuggestText(page, "a");
    await expectVisibleSuggestText(page, "b");
    await page.keyboard.press("Escape");

    await replaceEditorContentByInsertText(
      page,
      "select \nfrom (describe analytics.orders) described",
    );
    await setEditorPositionAfterText(page, "select ");
    await page.keyboard.press("ControlOrMeta+Space");
    for (const column of ["column_name", "column_type", "null", "key", "default", "extra"]) {
      await expectVisibleSuggestText(page, column);
    }
    const suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget.getByText("order_id", { exact: true })).toHaveCount(0);
    await expect(suggestWidget.getByText("total_amount", { exact: true })).toHaveCount(0);
  });

  test("highlights the canvas asset referenced under the SQL pointer", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "The split canvas is a desktop affordance.",
    );

    await page.goto(
      `${liveApp.baseURL}/pipelines/${analyticsPipelineId}/assets/${customersAssetId}/split`,
    );
    await waitForEditorReady(page, "customer_id");
    await replaceEditorContentByInsertText(page, "select * from analytics.orders");
    const upstreamToken = page
      .locator(".view-lines")
      .first()
      .locator("span", { hasText: "orders" })
      .last();
    const highlightedNode = page.locator('[data-sql-hover-highlight="true"]').filter({
      has: page.locator(`[data-testid="lineage-asset"][data-asset-id="${ordersAssetId}"]`),
    });

    await expect
      .poll(async () => {
        await upstreamToken.hover();
        return highlightedNode.count();
      })
      .toBe(1);

    const editorBounds = await page.locator(".monaco-editor").first().boundingBox();
    const tokenBounds = await upstreamToken.boundingBox();
    expect(editorBounds).not.toBeNull();
    expect(tokenBounds).not.toBeNull();
    await page.mouse.move(
      editorBounds!.x + editorBounds!.width - 8,
      tokenBounds!.y + tokenBounds!.height / 2,
    );
    await expect(highlightedNode).toHaveCount(0);

    await page.getByRole("link", { name: "Build", exact: true }).first().hover();
    await expect(highlightedNode).toHaveCount(0);
  });

  test("offers only in-scope aliases in join conditions and chains into columns", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Desktop suggest widget exposes stable Monaco completion DOM.",
    );

    const content = "select * from analytics.customers as x join analytics.orders as y on ";
    await openCustomersEditor(page, liveApp.baseURL);
    await replaceEditorContentByInsertText(page, content);

    const completionResponse = await page.request.post(
      `${liveApp.baseURL}/api/sql/lsp/completions`,
      {
        data: {
          asset_id: customersAssetId,
          content,
          position: { line: 0, character: content.length },
        },
      },
    );
    const body = (await completionResponse.json()) as {
      status?: string;
      completions?: Array<{ label?: string; kind?: number; insertText?: string }>;
    };
    expect(body.status).toBe("ok");
    expect(body.completions).toEqual([
      expect.objectContaining({ label: "x.*", kind: 5, insertText: "x." }),
      expect.objectContaining({ label: "y.*", kind: 5, insertText: "y." }),
    ]);

    await setEditorPositionAfterText(page, content);
    await page.keyboard.press("ControlOrMeta+Space");
    await expectVisibleSuggestText(page, "x.*");
    await expectVisibleSuggestText(page, "y.*");
    await page.keyboard.press("Enter");

    await expect
      .poll(async () => getFirstEditorValue(page), { timeout: 10000 })
      .toBe(`${content}x.`);
    await expectVisibleSuggestText(page, "customer_id");
  });

  test("suggests SQL keywords in a general statement position", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Desktop suggest widget exposes stable Monaco completion DOM.",
    );

    await openCustomersEditor(page, liveApp.baseURL);
    // A fresh line after the FROM clause is a general position where the LSP
    // offers clause keywords; the "wher" prefix filters down to "where".
    await replaceEditorContentByInsertText(page, "select 1\nfrom analytics.orders\nwher");
    await setEditorPositionAfterText(page, "wher");
    await page.keyboard.press("ControlOrMeta+Space");

    const suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible();
    await expect(suggestWidget.getByText("where", { exact: true })).toBeVisible();
  });

  test("maps SQL LSP rendered-template diagnostics back into Monaco", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Monaco diagnostics are only stable in the desktop editor.",
    );

    await openCustomersEditor(page, liveApp.baseURL);

    const diagnosticsResponse = page.waitForResponse(
      (response) =>
        response.url().includes("/api/sql/lsp/diagnostics") &&
        response.request().method() === "POST" &&
        (response.request().postData() ?? "").includes("missing_orders"),
      { timeout: 15000 },
    );
    await replaceEditorContentByInsertText(page, 'select *\nfrom {{ ref("missing_orders") }} m');
    const response = await diagnosticsResponse;
    expect(response.ok()).toBe(true);

    await expect
      .poll(async () => getEditorMarkers(page), { timeout: 15000 })
      .toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            message: "Unresolved table: missing_orders",
            startLineNumber: 2,
            startColumn: 6,
          }),
        ]),
      );
  });

  test("flags unresolved columns against a relation's known columns", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Monaco diagnostics are only stable in the desktop editor.",
    );

    await openAssetEditor(page, liveApp.baseURL, {
      assetId: ordersAssetId,
      contentToken: "order_id",
    });

    await replaceEditorContentByInsertText(
      page,
      "select c.custmer_name\nfrom analytics.customers as c",
    );

    await expect
      .poll(async () => getEditorMarkerMessages(page), { timeout: 15000 })
      .toEqual(
        expect.arrayContaining([expect.stringContaining("Unresolved column: custmer_name")]),
      );
  });

  test("flags unqualified unresolved columns in Monaco", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Monaco diagnostics are only stable in the desktop editor.",
    );

    await openCustomersEditor(page, liveApp.baseURL);
    await replaceEditorContentByInsertText(
      page,
      "select order_id, missing_order_column\nfrom analytics.orders",
    );

    await expect
      .poll(async () => getEditorMarkers(page), { timeout: 15000 })
      .toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            message: "Unresolved column: missing_order_column",
            severity: 8,
          }),
        ]),
      );
  });

  test("does not let stale diagnostic responses replace newer markers", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Monaco diagnostics are only stable in the desktop editor.",
    );

    await openCustomersEditor(page, liveApp.baseURL);

    let sawStaleRequest = false;
    let releaseStaleResponse: () => void = () => {};
    const staleGate = new Promise<void>((resolve) => {
      releaseStaleResponse = resolve;
    });
    await page.route("**/api/sql/lsp/diagnostics", async (route) => {
      const request = route.request().postDataJSON() as { content?: string };
      const content = request.content ?? "";
      if (content.includes("stale_missing_column")) {
        sawStaleRequest = true;
        await staleGate;
        await route
          .fulfill({
            json: {
              status: "ok",
              diagnostics: [
                {
                  range: {
                    start: { line: 0, character: 7 },
                    end: { line: 0, character: 27 },
                  },
                  severity: 1,
                  code: "unresolved-column",
                  source: "polyglot",
                  message: "Unresolved column: stale_missing_column",
                },
              ],
            },
          })
          .catch(() => undefined);
        return;
      }
      if (content.includes("current_missing_column")) {
        await route.fulfill({
          json: {
            status: "ok",
            diagnostics: [
              {
                range: {
                  start: { line: 0, character: 7 },
                  end: { line: 0, character: 29 },
                },
                severity: 1,
                code: "unresolved-column",
                source: "polyglot",
                message: "Unresolved column: current_missing_column",
              },
            ],
          },
        });
        return;
      }
      await route.continue();
    });

    await replaceEditorContentByInsertText(
      page,
      "select stale_missing_column\nfrom analytics.orders",
    );
    await expect.poll(() => sawStaleRequest, { timeout: 15000 }).toBe(true);

    await replaceEditorContentByInsertText(
      page,
      "select current_missing_column\nfrom analytics.orders",
    );
    await expect
      .poll(async () => getEditorMarkerMessages(page), { timeout: 15000 })
      .toEqual(
        expect.arrayContaining([
          expect.stringContaining("Unresolved column: current_missing_column"),
        ]),
      );

    releaseStaleResponse();
    await page.waitForTimeout(500);
    const messages = await getEditorMarkerMessages(page);
    expect(messages).toEqual(
      expect.arrayContaining([
        expect.stringContaining("Unresolved column: current_missing_column"),
      ]),
    );
    expect(messages).not.toEqual(
      expect.arrayContaining([expect.stringContaining("Unresolved column: stale_missing_column")]),
    );
  });

  test("reports self references as circular dependencies", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Monaco diagnostics are only stable in the desktop editor.",
    );

    await openCustomersEditor(page, liveApp.baseURL);

    await replaceEditorContentByInsertText(page, "select *\nfrom analytics.customers");

    await expect
      .poll(async () => getEditorMarkerMessages(page), { timeout: 15000 })
      .toEqual(
        expect.arrayContaining([
          expect.stringContaining(
            "Circular dependency: asset 'analytics.customers' references itself.",
          ),
        ]),
      );

    await expect
      .poll(async () => getEditorMarkerMessages(page), { timeout: 15000 })
      .not.toEqual(
        expect.arrayContaining([expect.stringContaining("Unresolved table: analytics.customers")]),
      );
  });

  test("flags unresolved relations in the FROM clause", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Monaco diagnostics are only stable in the desktop editor.",
    );

    // The app editor drives SQL diagnostics through the server LSP, which
    // reports the misspelled table as "Unresolved table: <name>". The old
    // parse-context editor phrased this as a "Did you mean ...?" quick fix; that
    // suggestion wording is not (yet) surfaced by the LSP path.
    await openCustomersEditor(page, liveApp.baseURL);

    await replaceEditorContentByInsertText(page, "select *\nfrom analytics.ordrs");

    await expect
      .poll(async () => getEditorMarkerMessages(page), { timeout: 15000 })
      .toEqual(
        expect.arrayContaining([expect.stringContaining("Unresolved table: analytics.ordrs")]),
      );
  });

  test("renders Jinja ghost text and completions in the SQL editor", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Monaco injected text DOM is only stable in the desktop editor.",
    );

    await writeFile(
      join(liveApp.workspaceDir, "analytics", "pipeline.yml"),
      [
        "name: analytics",
        "schedule: daily",
        'start_date: "2024-01-01"',
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
      "utf8",
    );

    await openCustomersEditor(page, liveApp.baseURL);

    const renderResponse = page.waitForResponse(
      (response) =>
        response.url().includes("/api/assets/") &&
        response.url().includes("/render-jinja") &&
        response.request().method() === "POST",
    );

    await replaceEditorContentByInsertText(
      page,
      "select * from analytics.orders\nwhere dt = '{{ end_date }}'\nand mode = '{{ var.run_mode }}'",
    );
    const response = await renderResponse;
    const body = await response.json();
    expect(body.status).toBe("ok");
    expect(body.spans).toEqual(
      expect.arrayContaining([
        expect.objectContaining({
          expression: "end_date",
          rendered_text: expect.stringMatching(/\d{4}-\d{2}-\d{2}/),
        }),
        expect.objectContaining({ expression: "var.run_mode", rendered_text: "incremental" }),
      ]),
    );

    await expect(page.locator(".bruin-jinja-rendered-ghost").first()).toBeVisible({
      timeout: 10000,
    });
    await expect(
      page
        .locator(".bruin-jinja-rendered-ghost")
        .filter({ hasText: /\d{4}-\d{2}-\d{2}/ })
        .first(),
    ).toBeVisible();
    await expect
      .poll(
        async () => {
          return await page.evaluate(() => {
            const monaco = (
              window as typeof window & {
                monaco?: any;
              }
            ).monaco;
            const editor = monaco?.editor.getEditors?.()[0];
            const model = editor?.getModel();
            if (!monaco || !model) return [];
            return monaco.editor
              .getModelMarkers({ resource: model.uri })
              .map((marker: { message: string }) => marker.message);
          });
        },
        { timeout: 10000 },
      )
      .not.toEqual(expect.arrayContaining([expect.stringContaining("syntax error")]));

    await replaceEditorContentAndWaitForJinja(page, "select '{{ var. }}'");
    await setEditorPositionAfterText(page, "var.");
    await openSuggestUntilText(page, "run_mode");

    await replaceEditorContentAndWaitForJinja(page, "select '{{ var.run_mode }}'");
    await setEditorPositionAfterText(page, "var.run_mode");
    const modifier = process.platform === "darwin" ? "Meta" : "Control";
    const variableToken = page
      .locator(".view-lines")
      .first()
      .locator("span", { hasText: "run_mode" })
      .last();
    await page.keyboard.down(modifier);
    await variableToken.hover();
    const variableLink = page
      .locator(".monaco-editor .goto-definition-link")
      .filter({ hasText: "run_mode" })
      .first();
    await expect(variableLink).toBeVisible({ timeout: 10000 });
    await expect
      .poll(async () => {
        return variableLink.evaluate((element) => {
          const style = window.getComputedStyle(element);
          return {
            cursor: style.cursor,
            decoration: style.textDecorationLine,
          };
        });
      })
      .toEqual({
        cursor: "pointer",
        decoration: "underline",
      });
    await page.keyboard.up(modifier);
    await page.keyboard.press("F12");

    const settings = page.getByRole("dialog", { name: /Pipeline settings/ });
    await expect(settings).toBeVisible({ timeout: 10000 });
    await expect(settings.getByRole("tab", { name: "Variables" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    const variable = settings.locator('[data-pipeline-variable="run_mode"]');
    await expect(variable).toBeVisible();
    await expect(variable).toHaveClass(/ring-2/);
    await expect(variable.getByRole("combobox", { name: "Type" })).toContainText("string");
    await expect(variable.getByRole("textbox", { name: "Default" })).toHaveValue("incremental");
  });

  test("keeps SQL suggestion focus across workspace SSE updates", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Desktop suggest widget exposes stable Monaco completion DOM.",
    );

    await openCustomersEditor(page, liveApp.baseURL);
    await replaceEditorContent(page, "select * from analytics.");
    await page.keyboard.press("ControlOrMeta+Space");

    const suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible();
    await page.keyboard.press("ArrowDown");
    const focusedBefore = await getFocusedSuggestText(page);
    expect(focusedBefore).toBeTruthy();

    const revisionBefore = await getWorkspaceRevision(page);
    const update = await page.request.put(
      `${liveApp.baseURL}/api/pipelines/${analyticsPipelineId}/assets/${ordersAssetId}`,
      { data: { meta: { description: `SSE focus check ${Date.now()}` } } },
    );
    expect(update.ok(), await update.text()).toBe(true);

    await expect
      .poll(async () => getWorkspaceRevision(page), { timeout: 15000 })
      .toBeGreaterThan(revisionBefore);
    await expect.poll(async () => getFocusedSuggestText(page)).toBe(focusedBefore);
  });

  test("does not fetch remote columns for partial qualified Bruin asset names", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Desktop suggest widget exposes stable Monaco completion DOM.",
    );

    const assetDir = join(liveApp.workspaceDir, "analytics", "assets", "simple");
    await mkdir(assetDir, { recursive: true });
    await writeFile(
      join(assetDir, "small.sql"),
      `/* @bruin
type: duckdb.sql
materialization:
  type: view
@bruin */

select 1 as small
`,
      "utf8",
    );
    await writeFile(
      join(assetDir, "query_small.sql"),
      `/* @bruin
type: duckdb.sql
materialization:
  type: view
@bruin */

select * from simple.small
`,
      "utf8",
    );

    const tableColumnRequests: string[] = [];
    page.on("request", (request) => {
      const url = request.url();
      if (url.includes("/api/sql/table-columns")) {
        tableColumnRequests.push(url);
      }
    });

    await openCustomersEditor(page, liveApp.baseURL);
    await waitForWorkspaceAsset(page, "simple.query_small");
    await replaceEditorContent(page, "select * from simple.");
    await page.keyboard.press("ControlOrMeta+Space");

    const suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible();
    await expect(suggestWidget.getByText("simple.small", { exact: true })).toBeVisible();
    await page.waitForTimeout(500);

    expect(
      tableColumnRequests.some(
        (url) => url.includes("table=simple") || url.includes("table=%22simple%22"),
      ),
    ).toBe(false);
  });

  test("suggests Jinja expressions inside statement blocks", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Desktop suggest widget exposes stable Monaco completion DOM.",
    );

    await writeFile(
      join(liveApp.workspaceDir, "analytics", "pipeline.yml"),
      [
        "name: analytics",
        "schedule: daily",
        'start_date: "2024-01-01"',
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
      "utf8",
    );

    await openCustomersEditor(page, liveApp.baseURL);

    await replaceEditorContentByInsertText(page, "{% if start_date |  %}\nselect 1\n{% endif %}");
    await setEditorPositionAfterText(page, "| ");
    await page.keyboard.press("ControlOrMeta+Space");
    let suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible();
    await expect(suggestWidget.getByText("add_days", { exact: true })).toBeVisible();
    await page.keyboard.press("Escape");

    await replaceEditorContentAndWaitForJinja(page, "{% if var. %}\nselect 1\n{% endif %}");
    await setEditorPositionAfterText(page, "var.");
    await openSuggestUntilText(page, "run_mode");
    await expectVisibleSuggestText(page, "days");
    await page.keyboard.press("Escape");

    await replaceEditorContentByInsertText(
      page,
      "{% for day in  %}\nselect {{ day }}\n{% endfor %}",
    );
    await setEditorPositionAfterText(page, "in ");
    await page.keyboard.press("ControlOrMeta+Space");
    suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible();
    await expect(suggestWidget.getByText("var.days", { exact: true })).toBeVisible();
  });
});

test.describe("sql intellisense ranking live", () => {
  test.use({ fixtureName: "sql-intellisense-ranking-workspace" });

  test("completes matching assets across pipelines in the FROM clause", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Desktop suggest widget exposes stable Monaco completion DOM.",
    );

    // Both analytics.dependencies and marts.dependencies are workspace assets, so
    // the LSP offers them as relation completions when a FROM prefix matches —
    // no materialization required.
    await openCustomersEditor(page, liveApp.baseURL);
    await replaceEditorContent(page, "select * from dependen");
    await page.keyboard.press("ControlOrMeta+Space");

    const suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible();
    await expect(suggestWidget.getByText("analytics.dependencies", { exact: true })).toBeVisible();
    await expect(suggestWidget.getByText("marts.dependencies", { exact: true })).toBeVisible();
  });
});

async function openCustomersEditor(page: Page, baseURL: string) {
  await openAssetEditor(page, baseURL, {
    assetId: customersAssetId,
    contentToken: "customer_id",
  });
}

async function openAssetEditor(
  page: Page,
  baseURL: string,
  options: { pipelineId?: string; assetId: string; contentToken: string },
) {
  const pipelineId = options.pipelineId ?? analyticsPipelineId;
  await page.goto(`${baseURL}/pipelines/${pipelineId}/assets/${options.assetId}/code`);
  await waitForEditorReady(page, options.contentToken);
}

async function replaceEditorContentByInsertText(page: Page, content: string) {
  const editor = await waitForEditorReady(page);
  await editor.click();
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.insertText(content);
}

async function replaceEditorContentAndWaitForJinja(page: Page, content: string) {
  const renderResponse = page.waitForResponse(
    (response) =>
      response.url().includes("/api/assets/") &&
      response.url().includes("/render-jinja") &&
      response.request().method() === "POST",
    { timeout: 15000 },
  );
  await replaceEditorContentByInsertText(page, content);
  await renderResponse;
}

async function setEditorPositionAfterText(page: Page, text: string) {
  await page.evaluate((needle) => {
    const monaco = (
      window as typeof window & {
        monaco?: any;
      }
    ).monaco;
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

async function getEditorMarkerMessages(page: Page) {
  return await page.evaluate(() => {
    const monaco = (window as typeof window & { monaco?: any }).monaco;
    const editor = monaco?.editor.getEditors?.()[0];
    const model = editor?.getModel();
    if (!monaco || !model) return [];
    return monaco.editor
      .getModelMarkers({ resource: model.uri })
      .map((marker: { message: string }) => marker.message);
  });
}

async function getFirstEditorValue(page: Page) {
  return await page.evaluate(() => {
    const monaco = (window as typeof window & { monaco?: any }).monaco;
    return monaco?.editor.getEditors?.()[0]?.getModel()?.getValue() ?? "";
  });
}

async function getEditorMarkers(page: Page) {
  return await page.evaluate(() => {
    const monaco = (window as typeof window & { monaco?: any }).monaco;
    const editor = monaco?.editor.getEditors?.()[0];
    const model = editor?.getModel();
    if (!monaco || !model) return [];
    return monaco.editor
      .getModelMarkers({ resource: model.uri })
      .map(
        (marker: {
          message: string;
          severity: number;
          startLineNumber: number;
          startColumn: number;
        }) => ({
          message: marker.message,
          severity: marker.severity,
          startLineNumber: marker.startLineNumber,
          startColumn: marker.startColumn,
        }),
      );
  });
}

async function expectVisibleSuggestText(page: Page, text: string) {
  await expect.poll(async () => getVisibleSuggestText(page), { timeout: 10000 }).toContain(text);
}

async function openSuggestUntilText(page: Page, text: string) {
  await expect
    .poll(
      async () => {
        await page.keyboard.press("Escape");
        await page.keyboard.press("ControlOrMeta+Space");
        return await getVisibleSuggestText(page);
      },
      { timeout: 15000, intervals: [250, 500, 750, 1000] },
    )
    .toContain(text);
}

async function getVisibleSuggestText(page: Page) {
  return await page.evaluate(() => {
    const widgets = Array.from(
      document.querySelectorAll(".suggest-widget.visible, [role='listbox'][aria-label='Suggest']"),
    );
    const widget = widgets.find((candidate) => {
      const rect = candidate.getBoundingClientRect();
      const style = window.getComputedStyle(candidate);
      return (
        rect.width > 0 &&
        rect.height > 0 &&
        style.visibility !== "hidden" &&
        style.display !== "none"
      );
    });
    if (!widget) {
      return "";
    }
    return Array.from(widget.querySelectorAll(".monaco-list-row"))
      .map((row) => row.textContent ?? "")
      .join("\n");
  });
}

async function replaceEditorContent(page: Page, content: string) {
  const editor = await waitForEditorReady(page);
  await editor.click();
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.type(content);
}

async function waitForEditorReady(page: Page, contentToken?: string) {
  const editor = page.locator(".monaco-editor").first();
  await expect(editor).toBeVisible({ timeout: 15000 });
  const viewLines = page.locator(".view-lines").first();
  await expect(viewLines).toBeVisible({ timeout: 15000 });
  if (contentToken) {
    await expect(viewLines).toContainText(contentToken, { timeout: 15000 });
  }
  return editor;
}

async function getWorkspaceRevision(page: Page) {
  return await page.evaluate(async () => {
    const response = await fetch("/api/workspace", { cache: "no-store" });
    const workspace = (await response.json()) as { revision?: number };
    return workspace.revision ?? 0;
  });
}

async function getFocusedSuggestText(page: Page) {
  return await page
    .locator(".suggest-widget.visible .monaco-list-row.focused")
    .first()
    .textContent()
    .catch(() => null);
}

async function waitForWorkspaceAsset(page: Page, assetName: string) {
  await expect
    .poll(
      async () => {
        return await page.evaluate(async (targetAssetName) => {
          const response = await fetch("/api/workspace", { cache: "no-store" });
          const workspace = (await response.json()) as {
            pipelines?: Array<{ assets?: Array<{ name?: string }> }>;
          };

          return (workspace.pipelines ?? []).some((pipeline) =>
            (pipeline.assets ?? []).some((asset) => asset.name === targetAssetName),
          );
        }, assetName);
      },
      { timeout: 15000 },
    )
    .toBe(true);
}
