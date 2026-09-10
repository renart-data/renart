import { expect, type Page } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { liveTest as test } from "../live-app-fixture";

const pythonAssetPath = "analytics/assets/analytics/typecheck_task.py";
const pythonAssetName = "analytics.typecheck_task";
const pipelineId = Buffer.from("analytics").toString("base64url");
const pythonAssetId = Buffer.from(pythonAssetPath).toString("base64url");

test.describe("python intellisense live", () => {
  test.use({ fixtureName: "configured-workspace" });

  test("shows ty diagnostics and formats Python assets in Monaco", async ({ liveApp, page }) => {
    await writeFile(
      join(liveApp.workspaceDir, pythonAssetPath),
      `"""@bruin
name: ${pythonAssetName}
type: python
image: python:3.11
@bruin"""

import pandas as pd

def returns_int(value:str)->int:
 return value

df = pd.DataFrame({"a": [1]})
`,
      "utf8",
    );
    await writeFile(
      join(liveApp.workspaceDir, "analytics", "assets", "analytics", "requirements.txt"),
      "pandas\n",
      "utf8",
    );
    const fakePandasPath = join(
      liveApp.workspaceDir,
      ".venv",
      "lib",
      "python3.11",
      "site-packages",
      "pandas",
    );
    await mkdir(fakePandasPath, { recursive: true });
    await writeFile(
      join(fakePandasPath, "__init__.py"),
      "from pandas.core.api import (\n    Series,\n    DataFrame,\n    Index,\n)\n",
      "utf8",
    );
    await mkdir(join(fakePandasPath, "core"), { recursive: true });
    await writeFile(
      join(fakePandasPath, "core", "api.py"),
      "from pandas.core.frame import DataFrame\nfrom pandas.core.indexes.base import Index\n",
      "utf8",
    );
    await mkdir(join(fakePandasPath, "core", "indexes"), { recursive: true });
    await writeFile(
      join(fakePandasPath, "core", "frame.py"),
      'class DataFrame:\n    def __init__(self, data=None, index=None, columns=None, dtype=None, copy=None): ...\n    columns = properties.AxisProperty(\n        doc="""\n        Returns\n        -------\n        pandas.Index\n            The column labels.\n        """\n    )\n    def head(self): ...\n    def merge(self): ...\n',
      "utf8",
    );
    await writeFile(
      join(fakePandasPath, "core", "indexes", "base.py"),
      "class Index:\n    name = None\n    def to_list(self): ...\n    def unique(self): ...\n",
      "utf8",
    );

    await waitForWorkspaceAsset(page, liveApp.baseURL, pythonAssetName);
    await openAssetEditor(page, liveApp.baseURL, {
      assetId: pythonAssetId,
      contentToken: "returns_int",
    });

    await expect
      .poll(async () => await getPythonTyMarkers(page), { timeout: 15000 })
      .toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            owner: "renart-python-ty",
            message: expect.stringMatching(/str|int|assignable|return/i),
          }),
        ]),
      );

    const markerMessages = await getPythonTyMarkers(page);
    expect(markerMessages.map((marker) => marker.message).join("\n")).not.toContain(
      "Cannot resolve imported module `pandas`",
    );

    await formatDocument(page);

    await expect
      .poll(async () => normalizeTrailingWhitespace(await getEditorValue(page)), {
        timeout: 10000,
      })
      .toContain("import pandas as pd\n\n\ndef returns_int(value: str) -> int:\n    return value");

    await expectPythonCompletion(page, "returns_", "returns_int");
    await expectPythonCompletion(page, "pd.", "DataFrame");
  });

  // Desktop-only: Monaco suggestion and marker APIs are only stable in the desktop editor.
  test("projects plain query string literals into the SQL language server @desktop-only", async ({
    liveApp,
    page,
  }) => {
    await writeFile(
      join(liveApp.workspaceDir, pythonAssetPath),
      `"""@bruin
name: ${pythonAssetName}
type: python
@bruin"""

from renart import query

result = query("select 1")
`,
      "utf8",
    );
    await waitForWorkspaceAsset(page, liveApp.baseURL, pythonAssetName);
    await openAssetEditor(page, liveApp.baseURL, {
      assetId: pythonAssetId,
      contentToken: "result",
    });

    await replaceEditorContent(
      page,
      ["from renart import query", "", 'result = query("select * from analytics'].join("\n"),
    );
    await page.keyboard.type(".");
    await expect(
      page.locator(".bruin-python-sql-keyword").filter({ hasText: "select" }).first(),
    ).toBeVisible({ timeout: 15000 });
    await expect(
      page
        .locator(".suggest-widget .monaco-list-row")
        .filter({ hasText: "analytics.orders" })
        .first(),
    ).toBeVisible({ timeout: 15000 });

    await page.keyboard.press("Escape");
    const closedQuery = [
      "from renart import query",
      "",
      'result = query("select * from analy")',
    ].join("\n");
    await replaceEditorContent(page, closedQuery, 'analy")', false);
    await page.keyboard.type("t");
    await expect(
      page
        .locator(".suggest-widget .monaco-list-row")
        .filter({ hasText: "analytics.orders" })
        .first(),
    ).toBeVisible({ timeout: 15000 });

    await page.keyboard.press("Escape");
    const projectionQuery = [
      "from renart import query",
      "",
      'result = query("select *,from analytics.orders")',
    ].join("\n");
    await replaceEditorContent(page, projectionQuery);
    await page.evaluate(() => {
      const monaco = (window as typeof window & { monaco?: any }).monaco;
      const editor = monaco?.editor.getEditors?.()[0];
      const model = editor?.getModel();
      if (!editor || !model) return;
      const offset = model.getValue().indexOf("*,from") + 2;
      editor.setPosition(model.getPositionAt(offset));
      editor.focus();
    });
    const projectionCompletions = page.waitForResponse(
      (response) => {
        if (!response.url().includes("/api/sql/lsp/completions") || !response.ok()) return false;
        const body = response.request().postDataJSON() as {
          content?: string;
          position?: unknown;
        };
        return body.content === "select *, from analytics.orders" && body.position !== undefined;
      },
      { timeout: 15000 },
    );
    await page.keyboard.type(" ");
    const projectionPayload = (await (await projectionCompletions).json()) as {
      completions?: Array<{ label?: string; kind?: number }>;
    };
    expect(projectionPayload.completions ?? []).toContainEqual(
      expect.objectContaining({ label: "order_id", kind: 5 }),
    );
    await expect(
      page.locator(".suggest-widget .monaco-list-row").filter({ hasText: "order_id" }).first(),
    ).toBeVisible({ timeout: 15000 });

    await replaceEditorContent(
      page,
      [
        "from renart import query",
        "",
        'result = query("select * from analytics.does_not_exist")',
      ].join("\n"),
    );
    await expect
      .poll(async () => await getPythonQuerySQLMarkers(page), { timeout: 15000 })
      .toEqual(
        expect.arrayContaining([
          expect.objectContaining({
            owner: "renart-python-query-sql",
            code: "unresolved-relation",
            message: expect.stringContaining("analytics.does_not_exist"),
          }),
        ]),
      );

    // Dynamic SQL remains Python-only: interpolated strings cannot be mapped
    // safely back to one stable SQL document.
    await replaceEditorContent(
      page,
      [
        "from renart import query",
        "column = 'order_id'",
        'result = query(f"select {column} from analytics.orders")',
      ].join("\n"),
    );
    await expect
      .poll(async () => await getPythonQuerySQLMarkers(page), { timeout: 10000 })
      .toEqual([]);
  });

  // Desktop-only: Monaco marker APIs are only stable in the desktop editor.
  test("uses renart.query's explicit connection for SQL diagnostics @desktop-only", async ({
    liveApp,
    page,
  }) => {
    const postgresAssetName = "analytics.postgres_orders";
    await writeFile(
      join(liveApp.workspaceDir, "analytics/assets/analytics/postgres_orders.sql"),
      `/* @bruin
name: ${postgresAssetName}
type: pg.sql
connection: postgres-default
materialization:
  type: view
@bruin */

select 1 as order_id
`,
      "utf8",
    );
    await writeFile(
      join(liveApp.workspaceDir, pythonAssetPath),
      `"""@bruin
name: ${pythonAssetName}
type: python
connection: duckdb-default
@bruin"""

from renart import query

result = query("select * from ${postgresAssetName}", connection="postgres-default")
`,
      "utf8",
    );
    await waitForWorkspaceAsset(page, liveApp.baseURL, postgresAssetName);
    await waitForWorkspaceAsset(page, liveApp.baseURL, pythonAssetName);
    await openAssetEditor(page, liveApp.baseURL, {
      assetId: pythonAssetId,
      contentToken: "postgres_orders",
    });

    await expect
      .poll(
        async () =>
          (await getPythonQuerySQLMarkers(page)).filter(
            (marker) => marker.code === "cross-connection-reference",
          ),
        { timeout: 15000 },
      )
      .toEqual([]);

    await replaceEditorContent(
      page,
      [
        "from renart import query",
        "",
        `result = query("select * from ${postgresAssetName}", connection="duckdb-default")`,
      ].join("\n"),
    );
    await expect
      .poll(
        async () =>
          (await getPythonQuerySQLMarkers(page)).some(
            (marker) => marker.code === "cross-connection-reference",
          ),
        { timeout: 15000 },
      )
      .toBe(true);

    await replaceEditorContent(
      page,
      [
        "from renart import query",
        "",
        `result = query("select * from ${postgresAssetName}", connection="postgres-default")`,
      ].join("\n"),
    );
    await expect
      .poll(
        async () =>
          (await getPythonQuerySQLMarkers(page)).filter(
            (marker) => marker.code === "cross-connection-reference",
          ),
        { timeout: 15000 },
      )
      .toEqual([]);
  });
});

async function openAssetEditor(
  page: Page,
  baseURL: string,
  options: { assetId: string; contentToken: string },
) {
  await page.goto(`${baseURL}/pipelines/${pipelineId}/assets/${options.assetId}/code`);
  await waitForEditorReady(page, options.contentToken);
}

async function waitForWorkspaceAsset(page: Page, baseURL: string, assetName: string) {
  await expect
    .poll(
      async () => {
        const response = await page.request.get(`${baseURL}/api/workspace`);
        if (!response.ok()) {
          return false;
        }
        const workspace = (await response.json()) as {
          pipelines?: Array<{ assets?: Array<{ name?: string }> }>;
        };
        return (workspace.pipelines ?? []).some((pipeline) =>
          (pipeline.assets ?? []).some((asset) => asset.name === assetName),
        );
      },
      { timeout: 15000 },
    )
    .toBe(true);
}

async function waitForEditorReady(page: Page, contentToken: string) {
  const editor = page.locator(".monaco-editor").first();
  await expect(editor).toBeVisible({ timeout: 15000 });
  await expect(page.locator(".view-lines").first()).toContainText(contentToken, {
    timeout: 15000,
  });
  return editor;
}

async function getPythonTyMarkers(page: Page) {
  return await page.evaluate<Array<{ owner?: string; message: string; severity: number }>>(() => {
    const monaco = (window as typeof window & { monaco?: any }).monaco;
    const editor = monaco?.editor.getEditors?.()[0];
    const model = editor?.getModel();
    if (!monaco || !model) return [];
    return monaco.editor
      .getModelMarkers({ resource: model.uri })
      .filter((marker: { owner?: string }) => marker.owner === "renart-python-ty")
      .map((marker: { owner?: string; message: string; severity: number }) => ({
        owner: marker.owner,
        message: marker.message,
        severity: marker.severity,
      }));
  });
}

async function getPythonQuerySQLMarkers(page: Page) {
  return await page.evaluate<
    Array<{ owner?: string; message: string; severity: number; code?: string }>
  >(() => {
    const monaco = (window as typeof window & { monaco?: any }).monaco;
    const editor = monaco?.editor.getEditors?.()[0];
    const model = editor?.getModel();
    if (!monaco || !model) return [];
    return monaco.editor
      .getModelMarkers({ resource: model.uri })
      .filter((marker: { owner?: string }) => marker.owner === "renart-python-query-sql")
      .map((marker: { owner?: string; message: string; severity: number; code?: string }) => ({
        owner: marker.owner,
        message: marker.message,
        severity: marker.severity,
        code: marker.code?.toString(),
      }));
  });
}

async function getEditorValue(page: Page) {
  return await page.evaluate(() => {
    const monaco = (window as typeof window & { monaco?: any }).monaco;
    const editor = monaco?.editor.getEditors?.()[0];
    return editor?.getModel()?.getValue() ?? "";
  });
}

async function formatDocument(page: Page) {
  await page.evaluate(async () => {
    const monaco = (window as typeof window & { monaco?: any }).monaco;
    const editor = monaco?.editor.getEditors?.()[0];
    await editor?.getAction("editor.action.formatDocument")?.run();
  });
}

async function replaceEditorContent(
  page: Page,
  content: string,
  cursorToken?: string,
  triggerSuggest = Boolean(cursorToken),
) {
  await page.evaluate(
    ({ nextContent, token, shouldTriggerSuggest }) => {
      const monaco = (window as typeof window & { monaco?: any }).monaco;
      const editor = monaco?.editor.getEditors?.()[0];
      const model = editor?.getModel();
      if (!editor || !model) return;
      editor.setValue(nextContent);
      const cursorOffset = token
        ? nextContent.lastIndexOf(token) + token.indexOf('"')
        : nextContent.length;
      editor.setPosition(model.getPositionAt(Math.max(0, cursorOffset)));
      editor.focus();
      if (token && shouldTriggerSuggest) {
        editor.trigger("test", "editor.action.triggerSuggest", {});
      }
    },
    { nextContent: content, token: cursorToken, shouldTriggerSuggest: triggerSuggest },
  );
}

async function expectPythonCompletion(page: Page, prefix: string, expectedLabel: string) {
  await page.evaluate((completionPrefix) => {
    const monaco = (window as typeof window & { monaco?: any }).monaco;
    const editor = monaco?.editor.getEditors?.()[0];
    const model = editor?.getModel();
    if (!editor || !model) return;
    const lineNumber = model.getLineCount();
    const column = model.getLineMaxColumn(lineNumber);
    editor.executeEdits("test", [
      {
        range: new monaco.Range(lineNumber, column, lineNumber, column),
        text: `\n${completionPrefix}`,
      },
    ]);
    const insertedLines = completionPrefix.split("\n");
    editor.setPosition({
      lineNumber: lineNumber + insertedLines.length,
      column: insertedLines[insertedLines.length - 1].length + 1,
    });
    editor.trigger("test", "editor.action.triggerSuggest", {});
  }, prefix);

  await expect(
    page.locator(".suggest-widget .monaco-list-row").filter({ hasText: expectedLabel }).first(),
  ).toBeVisible({
    timeout: 15000,
  });
}

function normalizeTrailingWhitespace(value: string) {
  return value
    .split("\n")
    .map((line) => line.trimEnd())
    .join("\n");
}
