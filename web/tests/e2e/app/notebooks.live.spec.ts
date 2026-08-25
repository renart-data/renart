import { expect, type APIRequestContext, type Page } from "@playwright/test";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";

import { liveTest as test, timeoutForRetry } from "../live-app-fixture";

type NotebookEnvelope = {
  notebook: {
    id: string;
    path: string;
    revision: string;
    cells: Array<{
      id: string;
      cell_id: string;
      name: string;
      content: string;
      content_revision?: string;
      connection?: string;
      type?: string;
      notebook_source?: {
        kind: "file" | "http";
        uri?: string;
        snapshot: { mode: "full" | "sample"; row_limit?: number };
      };
    }>;
    blocks?: Array<{
      id?: string;
      cell?: string;
      markdown?: string;
      control?: string;
      visualization?: {
        id: string;
        source: string;
        definition: Record<string, unknown>;
      };
    }>;
    parameters?: Array<{
      id: string;
      label?: string;
      type: string;
      default: unknown;
      options?: {
        values?: unknown[];
        dataset?: string;
        value_field?: string;
        label_field?: string;
      };
    }>;
  };
};

async function getCellAssetId(
  request: APIRequestContext,
  baseURL: string,
  notebookId: string,
  cellId: string,
) {
  const response = await request.get(`${baseURL}/api/notebooks/${notebookId}`);
  expect(response.ok()).toBe(true);
  const notebook = ((await response.json()) as NotebookEnvelope).notebook;
  return notebook.cells.find((cell) => cell.cell_id === cellId)!.id;
}

type PythonDiagnosticsResponse = {
  status: string;
  diagnostics?: Array<{ id?: string; message: string }>;
};

async function pythonDiagnostics(
  request: APIRequestContext,
  baseURL: string,
  assetId: string,
  content: string,
) {
  const response = await request.post(`${baseURL}/api/assets/${assetId}/python-diagnostics`, {
    data: { content },
  });
  expect(response.ok()).toBe(true);
  return (await response.json()) as PythonDiagnosticsResponse;
}

async function createNotebook(request: APIRequestContext, baseURL: string, title: string) {
  const response = await request.post(`${baseURL}/api/notebooks`, { data: { title } });
  expect(response.ok()).toBe(true);
  return ((await response.json()) as NotebookEnvelope).notebook;
}

async function createNotebookControl(
  request: APIRequestContext,
  baseURL: string,
  notebookId: string,
  parameter: NonNullable<NotebookEnvelope["notebook"]["parameters"]>[number],
) {
  const currentResponse = await request.get(`${baseURL}/api/notebooks/${notebookId}`);
  expect(currentResponse.ok()).toBe(true);
  const current = (await currentResponse.json()) as NotebookEnvelope;
  const prepareResponse = await request.post(
    `${baseURL}/api/notebooks/${notebookId}/changes/prepare`,
    {
      data: {
        base_revision: current.notebook.revision,
        operations: [{ kind: "control.create", parameter, position: "end" }],
      },
    },
  );
  expect(prepareResponse.ok()).toBe(true);
  const plan = (await prepareResponse.json()) as {
    can_apply: boolean;
    blocking_problems?: string[];
    change_set: Record<string, unknown>;
  };
  expect(plan.can_apply, plan.blocking_problems?.join("; ")).toBe(true);
  const applyResponse = await request.post(`${baseURL}/api/notebooks/${notebookId}/changes/apply`, {
    data: plan.change_set,
  });
  expect(applyResponse.ok()).toBe(true);
}

async function openNotebookToolsTab(page: Page, name: "Outline" | "Data" | "Add" | "AI") {
  if ((page.viewportSize()?.width ?? 0) < 1280) {
    await page.getByRole("button", { name: "Notebook tools" }).click();
  }
  await page.getByRole("tab", { name, exact: true }).click();
}

async function addCell(
  request: APIRequestContext,
  baseURL: string,
  notebookId: string,
  name: string,
) {
  const response = await request.post(`${baseURL}/api/notebooks/${notebookId}/cells`, {
    data: { name },
  });
  expect(response.ok()).toBe(true);
  const notebook = ((await response.json()) as NotebookEnvelope).notebook;
  return notebook.cells.find((cell) => cell.name === name)!.cell_id;
}

async function setCell(
  request: APIRequestContext,
  baseURL: string,
  notebookId: string,
  cellId: string,
  body: string,
) {
  const content = `/* @bruin\ntype: duckdb.sql\n@bruin */\n${body}\n`;
  const response = await request.put(`${baseURL}/api/notebooks/${notebookId}/cells/${cellId}`, {
    data: { content },
  });
  expect(response.ok()).toBe(true);
}

async function addPythonCell(
  request: APIRequestContext,
  baseURL: string,
  notebookId: string,
  name: string,
) {
  const response = await request.post(`${baseURL}/api/notebooks/${notebookId}/cells`, {
    data: { name, language: "python" },
  });
  expect(response.ok()).toBe(true);
  const notebook = ((await response.json()) as NotebookEnvelope).notebook;
  return notebook.cells.find((cell) => cell.name === name)!.cell_id;
}

async function setPythonCell(
  request: APIRequestContext,
  baseURL: string,
  notebookId: string,
  cellId: string,
  body: string,
) {
  const response = await request.put(`${baseURL}/api/notebooks/${notebookId}/cells/${cellId}`, {
    data: { content: `${body}\n` },
  });
  expect(response.ok()).toBe(true);
}

type NotebookWithDependencies = NotebookEnvelope & { notebook: { dependencies?: string[] } };

async function getDependencies(request: APIRequestContext, baseURL: string, notebookId: string) {
  const response = await request.get(`${baseURL}/api/notebooks/${notebookId}`);
  expect(response.ok()).toBe(true);
  return ((await response.json()) as NotebookWithDependencies).notebook.dependencies ?? [];
}

async function setNotebookEditorValue(
  page: Page,
  cellId: string,
  value: string,
  options: { cursorOffset?: number; triggerSuggest?: boolean } = {},
) {
  await page.evaluate(
    ({ targetCellId, nextValue, cursorOffset, triggerSuggest }) => {
      const monaco = (window as typeof window & { monaco?: any }).monaco;
      const model = monaco?.editor
        .getModels?.()
        .find((candidate: any) => candidate.uri.toString().includes(`/notebook/${targetCellId}.`));
      const editor = monaco?.editor
        .getEditors?.()
        .find((candidate: any) => candidate.getModel?.() === model);
      if (!editor) {
        throw new Error(`Notebook editor for ${targetCellId} is not mounted`);
      }
      editor.setValue(nextValue);
      editor.setPosition(model.getPositionAt(cursorOffset ?? nextValue.length));
      editor.focus();
      if (triggerSuggest) {
        editor.trigger("test", "editor.action.triggerSuggest", {});
      }
    },
    {
      targetCellId: cellId,
      nextValue: value,
      cursorOffset: options.cursorOffset,
      triggerSuggest: options.triggerSuggest,
    },
  );
}

async function setVisualizationDefinitionValue(page: Page, blockId: string, value: string) {
  await page.evaluate(
    ({ targetBlockId, nextValue }) => {
      const monaco = (window as typeof window & { monaco?: any }).monaco;
      const model = monaco?.editor
        .getModels?.()
        .find((candidate: any) =>
          candidate.uri.toString().includes(`/notebook-visualization/${targetBlockId}.yml`),
        );
      const editor = monaco?.editor
        .getEditors?.()
        .find((candidate: any) => candidate.getModel?.() === model);
      if (!editor) throw new Error(`Visualization editor for ${targetBlockId} is not mounted`);
      editor.setValue(nextValue);
      editor.focus();
    },
    { targetBlockId: blockId, nextValue: value },
  );
}

test.describe("app notebooks live", () => {
  test.use({ fixtureName: "configured-workspace" });

  test("uses LSP-derived columns for a VALUES source", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Desktop suggest widget exposes stable Monaco completion DOM.",
    );
    const notebook = await createNotebook(page.request, liveApp.baseURL, "VALUES IntelliSense");
    const baseCellId = await addCell(page.request, liveApp.baseURL, notebook.id, "runtime_base");
    await setCell(
      page.request,
      liveApp.baseURL,
      notebook.id,
      baseCellId,
      "select 7 as unrelated_runtime",
    );
    const runResponse = await page.request.post(
      `${liveApp.baseURL}/api/notebooks/${notebook.id}/run`,
      { data: { cells: [baseCellId] } },
    );
    expect(runResponse.ok()).toBe(true);
    const cellId = await addCell(page.request, liveApp.baseURL, notebook.id, "values_query");
    await setCell(page.request, liveApp.baseURL, notebook.id, cellId, "select 1 as placeholder");
    const assetId = await getCellAssetId(page.request, liveApp.baseURL, notebook.id, cellId);
    const query = "select *,  from (values (1), (2)) x(a)";
    const cursorOffset = "select *, ".length;

    const lspResponse = await page.request.post(`${liveApp.baseURL}/api/sql/lsp/completions`, {
      data: {
        asset_id: assetId,
        content: query,
        position: { line: 0, character: cursorOffset },
      },
    });
    expect(lspResponse.ok()).toBe(true);
    const lspPayload = (await lspResponse.json()) as {
      completions?: Array<{ label: string }>;
    };
    expect(lspPayload.completions?.map((completion) => completion.label)).toContain("a");

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.locator(`[data-notebook-cell-id="${cellId}"] .monaco-editor`)).toBeVisible({
      timeout: 15000,
    });
    await setNotebookEditorValue(page, cellId, query, { cursorOffset, triggerSuggest: true });

    const suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible({ timeout: 15000 });
    await expect(suggestWidget.getByText("a", { exact: true }).first()).toBeVisible({
      timeout: 15000,
    });
    await expect(suggestWidget.getByText("customer_id", { exact: true })).toHaveCount(0);
    await expect(suggestWidget.getByText("unrelated_runtime", { exact: true })).toHaveCount(0);
  });

  test("resolves CTE columns after a leading viz directive", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Desktop suggest widget exposes stable Monaco completion DOM.",
    );
    const notebook = await createNotebook(page.request, liveApp.baseURL, "CTE IntelliSense");
    const cellId = await addCell(page.request, liveApp.baseURL, notebook.id, "cte_query");
    await setCell(page.request, liveApp.baseURL, notebook.id, cellId, "select 1 as placeholder");
    const assetId = await getCellAssetId(page.request, liveApp.baseURL, notebook.id, cellId);
    const query = [
      "/* @viz(line, x: count, y: count_star()) */",
      "with preagg as (",
      "  select 1::bigint as count, 2::bigint as count_star",
      ")",
      "select ",
      "from preagg",
    ].join("\n");
    const cursorOffset = query.indexOf("\nfrom preagg");

    const diagnosticsResponse = await page.request.post(
      `${liveApp.baseURL}/api/sql/lsp/diagnostics`,
      { data: { asset_id: assetId, content: query } },
    );
    expect(diagnosticsResponse.ok()).toBe(true);
    const diagnostics = (await diagnosticsResponse.json()) as {
      diagnostics?: Array<{ code: string; message: string }>;
    };
    expect(
      (diagnostics.diagnostics ?? []).some(
        (diagnostic) =>
          diagnostic.code === "unresolved-relation" && diagnostic.message.includes("preagg"),
      ),
    ).toBe(false);
    const completionResponse = await page.request.post(
      `${liveApp.baseURL}/api/sql/lsp/completions`,
      {
        data: {
          asset_id: assetId,
          content: query,
          position: { line: 4, character: "select ".length },
        },
      },
    );
    expect(completionResponse.ok()).toBe(true);
    const completionPayload = (await completionResponse.json()) as {
      completions?: Array<{ label: string }>;
    };
    expect(completionPayload.completions?.map((completion) => completion.label)).toEqual(
      expect.arrayContaining(["count", "count_star"]),
    );

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.locator(`[data-notebook-cell-id="${cellId}"] .monaco-editor`)).toBeVisible({
      timeout: 15000,
    });
    await setNotebookEditorValue(page, cellId, query, { cursorOffset, triggerSuggest: true });

    const suggestWidget = page.locator(".suggest-widget.visible").first();
    await expect(suggestWidget).toBeVisible({ timeout: 15000 });
    await expect(suggestWidget.getByText("count", { exact: true }).first()).toBeVisible();
    await expect(suggestWidget.getByText("count_star", { exact: true }).first()).toBeVisible();
  });

  test("Jinja in a SQL cell is rendered when the cell runs", async ({ liveApp, page }) => {
    const { request } = page;
    const notebook = await createNotebook(request, liveApp.baseURL, "Jinja Run");
    const cell = await addCell(request, liveApp.baseURL, notebook.id, "templated");
    await setCell(request, liveApp.baseURL, notebook.id, cell, "select '{{ start_date }}' as d");

    // Run with an explicit execution window; the rendered SQL must substitute
    // start_date instead of executing the literal "{{ start_date }}".
    const response = await request.post(`${liveApp.baseURL}/api/notebooks/${notebook.id}/run`, {
      data: { all: true, start_date: "2024-01-15T00:00:00Z", end_date: "2024-01-16T00:00:00Z" },
    });
    expect(response.ok()).toBe(true);
    const payload = (await response.json()) as {
      status: string;
      results: Array<{ name: string; status: string; rows: unknown[][]; error?: string }>;
    };
    expect(payload.status).toBe("ok");
    const result = payload.results.find((entry) => entry.name === "templated")!;
    expect(result.status).toBe("ok");
    expect(result.rows[0][0]).toBe("2024-01-15");
  });

  test("typed notebook parameters keep defaults in Git and values in runtime", async ({
    liveApp,
    page,
  }) => {
    const notebook = await createNotebook(page.request, liveApp.baseURL, "Typed Parameters");
    const cell = notebook.cells[0];
    await setCell(
      page.request,
      liveApp.baseURL,
      notebook.id,
      cell.cell_id,
      "select {{ parameter.region }} as region",
    );

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    const narrowNotebook = (page.viewportSize()?.width ?? 1280) < 1280;
    if (narrowNotebook) {
      await page.getByRole("button", { name: "Notebook tools" }).click();
    }
    const tools = narrowNotebook
      ? page.getByRole("dialog", { name: "Notebook tools" }).getByLabel("Notebook authoring tools")
      : page.getByLabel("Notebook authoring tools");
    await tools.getByRole("tab", { name: "Add" }).click();
    await tools.getByRole("button", { name: "Manage controls" }).click();
    const dialog = page.getByRole("dialog", { name: "Notebook controls" });
    await dialog.getByRole("button", { name: "Add control" }).click();
    await dialog.getByLabel("Control ID").fill("region");
    await dialog.getByLabel("Label").fill("Region");
    await dialog.getByLabel("Default value").fill("eu");
    const definitionResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/notebooks/${notebook.id}/changes/apply`) && response.ok(),
    );
    await dialog.getByRole("button", { name: "Save controls" }).click();
    await definitionResponse;

    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).toBeHidden();
    const controlBlock = page.getByRole("region", { name: "Control: Region" });
    const regionInput = controlBlock.getByRole("textbox", { name: "Region", exact: true });
    await expect(regionInput).toHaveValue("eu");
    const settingsResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/notebooks/${notebook.id}/settings`) && response.ok(),
    );
    await regionInput.fill("us");
    await settingsResponse;

    const runResponse = page.waitForResponse(
      (response) => response.url().endsWith(`/api/notebooks/${notebook.id}/run`) && response.ok(),
    );
    await page.getByRole("button", { name: "Run all" }).click();
    const runPayload = (await (await runResponse).json()) as {
      results: Array<{ cell_id: string; rows: unknown[][] }>;
    };
    expect(runPayload.results.find((result) => result.cell_id === cell.cell_id)?.rows).toEqual([
      ["us"],
    ]);

    const definition = (await (
      await page.request.get(`${liveApp.baseURL}/api/notebooks/${notebook.id}`)
    ).json()) as NotebookEnvelope & {
      notebook: { parameters?: Array<{ id: string; default: unknown }> };
    };
    expect(definition.notebook.parameters).toEqual([
      expect.objectContaining({ id: "region", default: "eu" }),
    ]);
    const runtime = (await (
      await page.request.get(`${liveApp.baseURL}/api/notebooks/${notebook.id}/runtime`)
    ).json()) as { parameter_values: Record<string, unknown> };
    expect(runtime.parameter_values).toEqual({ region: "us" });

    if (!test.info().project.name.includes("mobile")) {
      const query = "select {{ parameter. }} as region";
      await setNotebookEditorValue(page, cell.cell_id, query, {
        cursorOffset: query.indexOf(" }}"),
        triggerSuggest: true,
      });
      const suggestWidget = page.locator(".suggest-widget.visible").first();
      await expect(suggestWidget.getByText("region", { exact: true }).first()).toBeVisible({
        timeout: 15000,
      });
    }
  });

  test("dataset-backed notebook controls refresh from a successful local cell result", async ({
    liveApp,
    page,
  }) => {
    await page.addInitScript(() => {
      window.localStorage.setItem("renart-notebook-autorecompute", "off");
    });
    const notebook = await createNotebook(page.request, liveApp.baseURL, "Dataset Controls");
    const disableAutoRecompute = await page.request.put(
      `${liveApp.baseURL}/api/notebooks/${notebook.id}/settings`,
      { data: { auto_recompute: false, environment: "default" } },
    );
    expect(disableAutoRecompute.ok()).toBe(true);
    const source = notebook.cells[0];
    await setCell(
      page.request,
      liveApp.baseURL,
      notebook.id,
      source.cell_id,
      "select * from (values ('de', 'Germany'), ('us', 'United States'), ('de', 'Germany')) as regions(code, label)",
    );
    await createNotebookControl(page.request, liveApp.baseURL, notebook.id, {
      id: "region",
      label: "Region",
      type: "select",
      default: "de",
      options: {
        dataset: source.cell_id,
        value_field: "code",
        label_field: "label",
      },
    });

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    const control = page.getByRole("combobox", { name: "Region", exact: true });
    await expect(control).toBeDisabled();
    const controlBlock = page.getByRole("region", { name: "Control: Region" });
    await expect(controlBlock.getByRole("button", { name: "Load options" })).toBeDisabled();

    const optionsResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/notebooks/${notebook.id}/controls/region/options/refresh`) &&
        response.ok(),
      { timeout: 15000 },
    );
    const runResponse = page.waitForResponse(
      (response) => response.url().endsWith(`/api/notebooks/${notebook.id}/run`) && response.ok(),
      { timeout: 30000 },
    );
    await page.getByRole("button", { name: "Run all" }).click();
    const runPayload = (await (await runResponse).json()) as {
      results: Array<{ cell_id: string; status: string }>;
    };
    expect(runPayload.results).toContainEqual(
      expect.objectContaining({ cell_id: source.cell_id, status: "ok" }),
    );

    // A new successful producer result streams over SSE and refreshes its
    // dataset-backed controls without a second user action.
    const response = await optionsResponse;
    const payload = (await response.json()) as {
      result: { columns: string[]; rows: unknown[][]; total_rows: number; truncated?: boolean };
    };
    expect(payload.result.columns).toEqual(["code", "label"]);
    expect(payload.result.rows).toEqual([
      ["de", "Germany"],
      ["us", "United States"],
    ]);
    expect(payload.result.total_rows).toBe(2);
    expect(payload.result.truncated).toBeFalsy();

    await expect(control).toBeEnabled();
    await expect(controlBlock.getByRole("button", { name: "Refresh" })).toBeVisible();
    await control.click();
    await expect(page.getByRole("option", { name: "Germany" })).toBeVisible();
    await expect(page.getByRole("option", { name: "United States" })).toBeVisible();
  });

  test("create, edit, and run a notebook against the local session", async ({ liveApp, page }) => {
    test.setTimeout(timeoutForRetry(test.info(), 90000, 60000));
    const { request } = page;
    const notebook = await createNotebook(request, liveApp.baseURL, "Revenue Exploration");

    const baseCell = await addCell(request, liveApp.baseURL, notebook.id, "base");
    await setCell(
      request,
      liveApp.baseURL,
      notebook.id,
      baseCell,
      "select 10 as amount union all select 20",
    );
    const doubledCell = await addCell(request, liveApp.baseURL, notebook.id, "doubled");
    await setCell(
      request,
      liveApp.baseURL,
      notebook.id,
      doubledCell,
      "select amount * 2 as doubled from base order by 1",
    );

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Revenue Exploration").first()).toBeVisible({
      timeout: timeoutForRetry(test.info(), 15000),
    });
    // Both cells render with their names.
    await expect(page.getByText("base", { exact: true }).first()).toBeVisible();
    await expect(page.getByText("doubled", { exact: true }).first()).toBeVisible();

    const runResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === `/api/notebooks/${notebook.id}/run` &&
        response.request().method() === "POST" &&
        response.ok(),
      { timeout: timeoutForRetry(test.info(), 30000) },
    );
    await page.getByRole("button", { name: "Run all" }).click();
    const payload = (await (await runResponse).json()) as {
      status: string;
      results: Array<{ name: string; status: string; rows: unknown[][] }>;
    };
    expect(payload.status).toBe("ok");
    const doubled = payload.results.find((result) => result.name === "doubled")!;
    expect(doubled.status).toBe("ok");
    expect(doubled.rows).toEqual([[20], [40]]);

    // The result table shows the computed values in the UI.
    await expect(page.getByText("40", { exact: true }).first()).toBeVisible({
      timeout: timeoutForRetry(test.info(), 15000),
    });
  });

  test("virtualizes large result previews and exposes local performance", async ({
    liveApp,
    page,
  }) => {
    const notebook = await createNotebook(page.request, liveApp.baseURL, "Large Result Preview");
    const cellId = await addCell(page.request, liveApp.baseURL, notebook.id, "many_rows");
    await setCell(
      page.request,
      liveApp.baseURL,
      notebook.id,
      cellId,
      "select range as row_number from range(1000)",
    );

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Large Result Preview").first()).toBeVisible({ timeout: 15000 });

    const runResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === `/api/notebooks/${notebook.id}/run` &&
        response.request().method() === "POST" &&
        response.ok(),
      { timeout: timeoutForRetry(test.info(), 30000) },
    );
    await page.getByRole("button", { name: "Run all" }).click();
    const payload = (await (await runResponse).json()) as {
      results: Array<{
        name: string;
        rows: unknown[][];
        total_rows: number;
        performance?: {
          request_total_ms?: number;
          request_setup_ms?: number;
          batch_run_ms?: number;
          session_open_ms?: number;
          materialize_ms?: number;
          preview_query_ms?: number;
          metadata_write_ms?: number;
          runtime_sync_ms?: number;
          session_bytes?: number;
        };
      }>;
    };
    const result = payload.results.find((candidate) => candidate.name === "many_rows")!;
    expect(result.rows).toHaveLength(100);
    expect(result.total_rows).toBe(1000);
    expect(result.performance?.request_total_ms).toBeGreaterThanOrEqual(0);
    expect(result.performance?.request_setup_ms).toBeGreaterThanOrEqual(0);
    expect(result.performance?.batch_run_ms).toBeGreaterThanOrEqual(0);
    expect(result.performance?.session_open_ms).toBeGreaterThanOrEqual(0);
    expect(result.performance?.materialize_ms).toBeGreaterThanOrEqual(0);
    expect(result.performance?.preview_query_ms).toBeGreaterThanOrEqual(0);
    expect(result.performance?.metadata_write_ms).toBeGreaterThanOrEqual(0);
    expect(result.performance?.runtime_sync_ms).toBeGreaterThanOrEqual(0);
    expect(result.performance?.session_bytes).toBeGreaterThan(0);

    const table = page.getByRole("grid", { name: "many_rows result preview" });
    await expect(table).toHaveAttribute("aria-rowcount", "101");
    await expect(table.locator("tbody")).toHaveAttribute("data-virtualized", "true");
    await expect(table.locator("[data-row-index]")).toHaveCount(17);
    await expect(page.getByText("showing 100 of 1,000 rows", { exact: true })).toBeVisible();

    const cell = page.locator(`[data-notebook-cell-id="${cellId}"]`);
    const card = cell.locator(':scope > [data-slot="delimited-card"]');
    const cardHeader = card.locator('[data-slot="delimited-card-header"]');
    const editorShell = cell.locator('[data-slot="notebook-cell-editor-shell"]');
    const resizeHandle = cell.getByRole("separator", { name: "Resize many_rows cell" });
    const resultPreview = cell.getByTestId("notebook-result-preview");
    const nameBadge = cell.getByTestId("notebook-cell-name-badge");
    const performanceButton = cell.locator(
      'button[aria-label="Show local performance measurements"]',
    );

    await expect(nameBadge.getByRole("button", { name: "Rename cell many_rows" })).toBeVisible();
    await expect(cell.getByLabel("SQL cell")).toBeHidden();
    await expect(cell.getByText(/1000 rows · \d+ ms/)).toBeHidden();
    await expect(performanceButton).toBeHidden();
    expect(
      await card.evaluate((element) => ({
        background: getComputedStyle(element).backgroundColor,
        borderWidth: getComputedStyle(element).borderTopWidth,
      })),
    ).toEqual({ background: "rgba(0, 0, 0, 0)", borderWidth: "1px" });
    expect(
      await cardHeader.evaluate((element) => element.getBoundingClientRect().height),
    ).toBeLessThanOrEqual(36);
    expect(
      await cell
        .locator(".monaco-editor")
        .evaluate((element) => getComputedStyle(element).backgroundColor),
    ).toBe("rgba(0, 0, 0, 0)");
    expect(await resultPreview.evaluate((element) => getComputedStyle(element).clipPath)).not.toBe(
      "none",
    );
    const editorResultEdges = await Promise.all([
      resizeHandle.evaluate((element) => element.getBoundingClientRect().bottom),
      resultPreview.evaluate((element) => element.getBoundingClientRect().top),
    ]);
    expect(Math.abs(editorResultEdges[0] - editorResultEdges[1])).toBeLessThanOrEqual(1);
    expect(await resizeHandle.evaluate((element) => getComputedStyle(element).borderTopWidth)).toBe(
      "0px",
    );

    await editorShell.click();
    await expect(cell.getByLabel("SQL cell")).toBeVisible();
    await expect(cell.getByText(/1000 rows · \d+ ms/)).toBeVisible();
    await nameBadge.getByRole("button", { name: "Rename cell many_rows" }).click();
    await expect(nameBadge.getByRole("textbox", { name: "Rename cell many_rows" })).toBeVisible();
    await nameBadge.getByRole("textbox", { name: "Rename cell many_rows" }).press("Escape");

    await table.evaluate((element) => {
      const viewport = element.closest('[data-slot="scroll-area-viewport"]');
      if (!(viewport instanceof HTMLElement)) throw new Error("Result viewport is missing");
      viewport.scrollTop = 50 * 27;
      viewport.dispatchEvent(new Event("scroll", { bubbles: true }));
    });
    await expect(table.locator('[data-row-index="50"]')).toBeAttached();
    expect(await table.locator("[data-row-index]").count()).toBeLessThan(30);

    const cell50 = table.locator('[data-grid-row-index="50"][data-grid-column-index="0"]');
    const cell51 = table.locator('[data-grid-row-index="51"][data-grid-column-index="0"]');
    const cell52 = table.locator('[data-grid-row-index="52"][data-grid-column-index="0"]');
    await cell50.click();
    await expect(cell50.locator("..")).toHaveAttribute("aria-selected", "true");
    if (test.info().project.name.includes("mobile")) {
      const selectionControls = page.getByTestId("mobile-table-selection-controls");
      await expect(selectionControls).toBeVisible();
      await selectionControls.getByRole("button", { name: "Adjust selection down" }).click();
      await expect(table.locator('td[aria-selected="true"]')).toHaveCount(2);
      await selectionControls.getByRole("button", { name: "Adjust selection up" }).click();
      await expect(table.locator('td[aria-selected="true"]')).toHaveCount(1);
      await selectionControls.getByRole("button", { name: "Clear selection" }).click();
      await expect(table.locator('td[aria-selected="true"]')).toHaveCount(0);
      await cell50.click();
    } else {
      await cell51.hover();
      await page.waitForTimeout(150);
      await expect(page.locator('[data-slot="hover-card-content"]')).toBeHidden();
      await cell50.hover();
      await expect(page.locator('[data-slot="hover-card-content"]')).toBeVisible();
    }

    await cell52.click({ modifiers: ["Shift"] });
    await expect(table.locator('td[aria-selected="true"]')).toHaveCount(3);
    await cell52.press("Shift+ArrowDown");
    await expect(table.locator('td[aria-selected="true"]')).toHaveCount(4);
    await cell51.click({ modifiers: ["Control"] });
    await expect(table.locator('td[aria-selected="true"]')).toHaveCount(3);
    await cell52.press("Control+c");
    await expect(page.getByText("Copied", { exact: true })).toBeVisible();
    await cell52.press("Escape");
    await expect(table.locator('td[aria-selected="true"]')).toHaveCount(0);
    await cell52.click();
    await page.keyboard.press("Control+a");
    await expect(page.getByRole("button", { name: "Copy selected cells" })).toContainText(
      "Copy 100",
    );
    await page.keyboard.press("Escape");
    await expect(table.locator('td[aria-selected="true"]')).toHaveCount(0);

    await expect(performanceButton).toBeVisible();
    if (!test.info().project.name.includes("mobile")) {
      await performanceButton.hover();
      const details = page.getByText("Local performance", { exact: true });
      await expect(details).toBeVisible();
      await expect(page.getByText("Preview render", { exact: true })).toBeVisible();
      await expect(page.getByText("Mounted rows", { exact: true })).toBeVisible();
    }
  });

  test("adds a local file source and joins its typed snapshot", async ({ liveApp, page }) => {
    test.setTimeout(timeoutForRetry(test.info(), 120000, 60000));
    writeFileSync(
      join(liveApp.workspaceDir, "notebook-events.csv"),
      "event_id,amount\n1,10\n2,20\n",
      "utf8",
    );
    const notebook = await createNotebook(page.request, liveApp.baseURL, "File Sources");
    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("File Sources").first()).toBeVisible({ timeout: 15000 });

    await openNotebookToolsTab(page, "Data");
    await page.getByRole("button", { name: "Add data" }).click();
    const dialog = page.getByRole("dialog", { name: "Add data" });
    await dialog.getByRole("tab", { name: "File" }).click();
    await dialog.getByLabel("File or object").fill("notebook-events.csv");
    await dialog.getByRole("button", { name: "Add source" }).click();
    await expect(dialog).toBeHidden({ timeout: 15000 });

    const snapshot = (await (
      await page.request.get(`${liveApp.baseURL}/api/notebooks/${notebook.id}`)
    ).json()) as NotebookEnvelope;
    const source = snapshot.notebook.cells.find((cell) => cell.notebook_source?.kind === "file");
    expect(source?.notebook_source?.uri).toBe("notebook-events.csv");
    const sourceCard = page.locator(`[data-notebook-cell-id="${source!.cell_id}"]`);
    await expect(sourceCard.getByText("notebook-events.csv")).toBeVisible();
    const sourceRun = page.waitForResponse(
      (response) => response.url().endsWith(`/api/notebooks/${notebook.id}/run`) && response.ok(),
      { timeout: 90000 },
    );
    await sourceCard.getByTitle("Refresh source").click();
    const sourcePayload = (await (await sourceRun).json()) as {
      results: Array<{
        cell_id: string;
        status: string;
        column_types?: string[];
        total_rows: number;
        snapshot?: {
          imported_at: string;
          row_count: number;
          byte_count: number;
          complete: boolean;
          sampled: boolean;
        };
      }>;
    };
    expect(sourcePayload.results[0]).toMatchObject({
      cell_id: source!.cell_id,
      status: "ok",
      total_rows: 2,
    });
    expect(sourcePayload.results[0].column_types?.length).toBe(2);
    expect(sourcePayload.results[0].snapshot).toMatchObject({
      row_count: 2,
      complete: true,
      sampled: false,
    });
    expect(sourcePayload.results[0].snapshot?.byte_count).toBeGreaterThan(0);
    const snapshotDetails = sourceCard.getByLabel(`${source!.name} snapshot details`);
    await expect(snapshotDetails).toContainText("Complete");
    await expect(snapshotDetails).toContainText("default");
    await expect(snapshotDetails.locator("time")).toHaveAttribute(
      "datetime",
      sourcePayload.results[0].snapshot!.imported_at,
    );
    await expect(
      sourceCard.getByRole("grid", { name: `${source!.name} result preview` }),
    ).toBeVisible();

    const totalCell = await addCell(page.request, liveApp.baseURL, notebook.id, "file_total");
    await setCell(
      page.request,
      liveApp.baseURL,
      notebook.id,
      totalCell,
      `select sum(amount) as total from ${source!.name}`,
    );
    const joined = await page.request.post(`${liveApp.baseURL}/api/notebooks/${notebook.id}/run`, {
      data: { cells: [totalCell] },
      timeout: 90000,
    });
    expect(joined.ok()).toBe(true);
    const joinedPayload = (await joined.json()) as {
      results: Array<{ cell_id: string; status: string; rows: unknown[][] }>;
    };
    expect(joinedPayload.results.at(-1)).toMatchObject({
      cell_id: totalCell,
      status: "ok",
      rows: [[30]],
    });

    const csvExport = await page.request.get(
      `${liveApp.baseURL}/api/notebooks/${notebook.id}/cells/${totalCell}/export?format=csv`,
    );
    expect(csvExport.ok()).toBe(true);
    expect(csvExport.headers()["content-disposition"]).toContain("file_total.csv");
    expect(await csvExport.text()).toContain("total\n30");

    const parquetExport = await page.request.get(
      `${liveApp.baseURL}/api/notebooks/${notebook.id}/cells/${totalCell}/export?format=parquet`,
    );
    expect(parquetExport.ok()).toBe(true);
    const parquet = await parquetExport.body();
    expect(parquet.subarray(0, 4).toString()).toBe("PAR1");
    expect(parquet.subarray(-4).toString()).toBe("PAR1");

    // A full local source can be reviewed and promoted directly to the
    // destination warehouse's Seed type. The remaining transform follows the
    // new pipeline asset name.
    await sourceCard.getByRole("button", { name: "Source actions" }).click();
    await page.getByRole("menuitem", { name: "Promote to pipeline" }).click();
    const promotionDialog = page.getByRole("dialog", { name: "Promote to pipeline" });
    await expect(promotionDialog.getByText("duckdb.seed", { exact: true })).toBeVisible({
      timeout: 15000,
    });
    await expect(promotionDialog.getByText("duckdb-default", { exact: true })).toBeVisible();
    await expect(promotionDialog.getByText(/^\d+ file changes$/)).toBeVisible();
    const promotionResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname ===
          `/api/notebooks/${notebook.id}/cells/${source!.cell_id}/promote` &&
        response.request().method() === "POST" &&
        response.ok(),
      { timeout: 30000 },
    );
    await promotionDialog.getByRole("button", { name: "Promote", exact: true }).click();
    const promotion = (await (await promotionResponse).json()) as {
      promoted_count: number;
      asset_path: string;
      notebook: NotebookEnvelope["notebook"];
    };
    expect(promotion.promoted_count).toBe(1);
    expect(promotion.asset_path).toContain(`assets/marts/${source!.name}.asset.yml`);
    expect(
      promotion.notebook.cells.find((candidate) => candidate.cell_id === totalCell)?.content,
    ).toContain(`from marts.${source!.name}`);
  });

  test("highlights a sibling cell after definition navigation", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Ctrl+click definition navigation is a desktop editor interaction.",
    );

    const notebook = await createNotebook(page.request, liveApp.baseURL, "Definition Highlight");
    const baseCell = await addCell(page.request, liveApp.baseURL, notebook.id, "base");
    await setCell(page.request, liveApp.baseURL, notebook.id, baseCell, "select 1 as value");
    const readerCell = await addCell(page.request, liveApp.baseURL, notebook.id, "reader");
    await setCell(page.request, liveApp.baseURL, notebook.id, readerCell, "select value from base");

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Definition Highlight").first()).toBeVisible({ timeout: 15000 });

    const targetCard = page.locator(`[data-notebook-cell-id="${baseCell}"]`);
    await expect(
      page.locator(`[data-notebook-cell-id="${readerCell}"] .monaco-editor`),
    ).toBeVisible({
      timeout: 15000,
    });
    const relationPoint = await page.evaluate(() => {
      const monaco = (window as typeof window & { monaco?: any }).monaco;
      const editor = monaco?.editor
        .getEditors?.()
        .find(
          (candidate: any) =>
            candidate.getModel?.()?.getValue().trim() === "select value from base",
        );
      const model = editor?.getModel?.();
      const domNode = editor?.getDomNode?.();
      if (!monaco || !editor || !model || !domNode) {
        throw new Error("reader Monaco editor is not ready");
      }
      const relationOffset = model.getValue().lastIndexOf("base");
      if (relationOffset < 0) {
        throw new Error("base relation was not found in the reader model");
      }
      // Aim at the second character, rather than estimating a character from a
      // Monaco DOM span whose box can include whitespace and nested tokens.
      const position = model.getPositionAt(relationOffset + 1);
      editor.revealPositionInCenterIfOutsideViewport(position);
      const visiblePosition = editor.getScrolledVisiblePosition(position);
      if (!visiblePosition) {
        throw new Error("base relation is outside the reader editor viewport");
      }
      const editorRect = domNode.getBoundingClientRect();
      const fontInfo = editor.getOption(monaco.editor.EditorOption.fontInfo);
      return {
        x: editorRect.left + visiblePosition.left + fontInfo.typicalHalfwidthCharacterWidth / 2,
        y: editorRect.top + visiblePosition.top + visiblePosition.height / 2,
      };
    });
    const modifier = process.platform === "darwin" ? "Meta" : "Control";
    const definitionResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/sql/lsp/definition") &&
        response.request().method() === "POST" &&
        response.ok(),
      { timeout: timeoutForRetry(test.info(), 15000) },
    );
    await page.keyboard.down(modifier);
    await page.mouse.click(relationPoint.x, relationPoint.y);
    await page.keyboard.up(modifier);
    await definitionResponse;
    await expect(targetCard).toHaveAttribute("data-notebook-cell-jump-highlight", "true", {
      timeout: timeoutForRetry(test.info(), 3000),
    });
    await expect(targetCard).not.toHaveAttribute("data-notebook-cell-jump-highlight", "true", {
      timeout: timeoutForRetry(test.info(), 3000),
    });
  });

  test("serializes autosaves so a delayed response cannot erase newer typing", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Monaco keyboard editing is only stable in the desktop notebook layout.",
    );

    const notebook = await createNotebook(page.request, liveApp.baseURL, "Save Ordering");
    const cellId = await addCell(page.request, liveApp.baseURL, notebook.id, "typing");
    await setCell(page.request, liveApp.baseURL, notebook.id, cellId, "select 1 as value");

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Save Ordering").first()).toBeVisible({ timeout: 15000 });
    const card = page.locator(`[data-notebook-cell-id="${cellId}"]`);
    const editor = card.locator(".monaco-editor").first();
    await expect(editor).toBeVisible({ timeout: 15000 });

    let releaseFirstResponse = () => {};
    const firstResponseGate = new Promise<void>((resolve) => {
      releaseFirstResponse = resolve;
    });
    let markFirstProcessed = () => {};
    const firstProcessed = new Promise<void>((resolve) => {
      markFirstProcessed = resolve;
    });
    const savedRequests: Array<{ content: string; baseRevision: string }> = [];
    const updateRoute = `**/notebooks/${notebook.id}/cells/${cellId}`;
    await page.route(updateRoute, async (route) => {
      if (route.request().method() !== "PUT") {
        await route.continue();
        return;
      }
      const payload = route.request().postDataJSON() as {
        content?: string;
        base_revision?: string;
      };
      savedRequests.push({
        content: payload.content ?? "",
        baseRevision: payload.base_revision ?? "",
      });
      if (savedRequests.length !== 1) {
        await route.continue();
        return;
      }

      // Let the server commit the first edit, but hold its response away from
      // React. Without the per-cell queue a second PUT overtakes this response,
      // then the stale first notebook snapshot replaces the newer draft.
      const response = await route.fetch();
      markFirstProcessed();
      await firstResponseGate;
      await route.fulfill({ response });
    });

    let released = false;
    try {
      await setNotebookEditorValue(page, cellId, "select 2 as value");
      await firstProcessed;

      const secondSuffix = " union all select 3";
      const releaseTimer = setTimeout(() => {
        releaseFirstResponse();
        released = true;
      }, 20);
      await page.keyboard.type(secondSuffix, { delay: 8 });
      clearTimeout(releaseTimer);
      if (!released) {
        releaseFirstResponse();
        released = true;
      }
      await expect.poll(() => savedRequests.length, { timeout: 15000 }).toBe(2);
      expect(savedRequests[0].baseRevision).toMatch(/^[0-9a-f]{64}$/);
      expect(savedRequests[1].baseRevision).toMatch(/^[0-9a-f]{64}$/);
      expect(savedRequests[1].baseRevision).not.toBe(savedRequests[0].baseRevision);
      await expect
        .poll(
          () =>
            page.evaluate((targetCellId) => {
              const monaco = (window as typeof window & { monaco?: any }).monaco;
              const model = monaco?.editor
                .getModels?.()
                .find((candidate: any) =>
                  candidate.uri.toString().includes(`/notebook/${targetCellId}.`),
                );
              return model?.getValue() ?? "";
            }, cellId),
          { timeout: 15000 },
        )
        .toBe("select 2 as value union all select 3");
      await expect
        .poll(
          () =>
            page.evaluate((targetCellId) => {
              const monaco = (window as typeof window & { monaco?: any }).monaco;
              const model = monaco?.editor
                .getModels?.()
                .find((candidate: any) =>
                  candidate.uri.toString().includes(`/notebook/${targetCellId}.`),
                );
              const editor = monaco?.editor
                .getEditors?.()
                .find((candidate: any) => candidate.getModel?.() === model);
              const position = editor?.getPosition();
              return model && position ? model.getOffsetAt(position) : -1;
            }, cellId),
          { timeout: 15000 },
        )
        .toBe("select 2 as value union all select 3".length);

      await expect
        .poll(async () => {
          const response = await page.request.get(
            `${liveApp.baseURL}/api/notebooks/${notebook.id}`,
          );
          const payload = (await response.json()) as NotebookEnvelope;
          return payload.notebook.cells.find((cell) => cell.cell_id === cellId)?.content ?? "";
        })
        .toContain("select 2 as value union all select 3");
    } finally {
      if (!released) {
        releaseFirstResponse();
      }
      await page.unroute(updateRoute);
    }
  });

  test("keeps a local draft when a peer saves the same cell first", async ({ liveApp, page }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Monaco keyboard editing is only stable in the desktop notebook layout.",
    );

    const notebook = await createNotebook(page.request, liveApp.baseURL, "Peer Save Conflict");
    const cellId = await addCell(page.request, liveApp.baseURL, notebook.id, "shared");
    await setCell(page.request, liveApp.baseURL, notebook.id, cellId, "select 1 as baseline");

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Peer Save Conflict").first()).toBeVisible({ timeout: 15000 });
    await expect(
      page.locator(`[data-notebook-cell-id="${cellId}"] .monaco-editor`).first(),
    ).toBeVisible({ timeout: 15000 });

    const snapshotResponse = await page.request.get(
      `${liveApp.baseURL}/api/notebooks/${notebook.id}`,
    );
    expect(snapshotResponse.ok()).toBe(true);
    const snapshot = (await snapshotResponse.json()) as NotebookEnvelope;
    const baseRevision = snapshot.notebook.cells.find(
      (candidate) => candidate.cell_id === cellId,
    )?.content_revision;
    expect(baseRevision).toMatch(/^[0-9a-f]{64}$/);

    let releaseLocalRequest = () => {};
    const localRequestGate = new Promise<void>((resolve) => {
      releaseLocalRequest = resolve;
    });
    let markLocalRequestStarted = () => {};
    const localRequestStarted = new Promise<void>((resolve) => {
      markLocalRequestStarted = resolve;
    });
    let localBaseRevision = "";
    const updateRoute = `**/notebooks/${notebook.id}/cells/${cellId}`;
    await page.route(updateRoute, async (route) => {
      if (route.request().method() !== "PUT") {
        await route.continue();
        return;
      }
      const payload = route.request().postDataJSON() as { base_revision?: string };
      localBaseRevision = payload.base_revision ?? "";
      markLocalRequestStarted();
      await localRequestGate;
      await route.continue();
    });

    let released = false;
    try {
      const localDraft = "select 2 as unsaved_local_draft";
      await setNotebookEditorValue(page, cellId, localDraft);
      await localRequestStarted;
      expect(localBaseRevision).toBe(baseRevision);

      const peerContent = "/* @bruin\ntype: duckdb.sql\n@bruin */\nselect 3 as peer_save\n";
      const peerResponse = await page.request.put(
        `${liveApp.baseURL}/api/notebooks/${notebook.id}/cells/${cellId}`,
        { data: { content: peerContent, base_revision: baseRevision } },
      );
      expect(peerResponse.ok()).toBe(true);

      const conflictResponse = page.waitForResponse(
        (response) =>
          response.url().endsWith(`/notebooks/${notebook.id}/cells/${cellId}`) &&
          response.request().method() === "PUT" &&
          response.status() === 409,
        { timeout: 15000 },
      );
      releaseLocalRequest();
      released = true;
      await conflictResponse;

      await expect
        .poll(
          () =>
            page.evaluate((targetCellId) => {
              const monaco = (window as typeof window & { monaco?: any }).monaco;
              return (
                monaco?.editor
                  .getModels?.()
                  .find((candidate: any) =>
                    candidate.uri.toString().includes(`/notebook/${targetCellId}.`),
                  )
                  ?.getValue() ?? ""
              );
            }, cellId),
          { timeout: 15000 },
        )
        .toBe(localDraft);

      await expect
        .poll(async () => {
          const response = await page.request.get(
            `${liveApp.baseURL}/api/notebooks/${notebook.id}`,
          );
          const payload = (await response.json()) as NotebookEnvelope;
          return payload.notebook.cells.find((candidate) => candidate.cell_id === cellId)?.content;
        })
        .toContain("select 3 as peer_save");
    } finally {
      if (!released) {
        releaseLocalRequest();
      }
      await page.unroute(updateRoute);
    }
  });

  test("formats cells through the revision-checked save queue", async ({ liveApp, page }) => {
    const notebook = await createNotebook(page.request, liveApp.baseURL, "Format Queue");
    const cellId = await addCell(page.request, liveApp.baseURL, notebook.id, "format_queue");
    await setCell(
      page.request,
      liveApp.baseURL,
      notebook.id,
      cellId,
      "select a,b from (select 1 as a,2 as b)",
    );
    const assetId = await getCellAssetId(page.request, liveApp.baseURL, notebook.id, cellId);

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    const cell = page.locator(`[data-notebook-cell-id="${cellId}"]`);
    const editor = cell.locator(".monaco-editor");
    await expect(editor).toBeVisible({ timeout: 15000 });

    const formatRequest = page.waitForRequest(
      (request) =>
        request.url().endsWith(`/api/assets/${assetId}/format-sql`) && request.method() === "POST",
      { timeout: 15000 },
    );
    const formatResponse = page.waitForResponse(
      (response) => response.url().endsWith(`/api/assets/${assetId}/format-sql`) && response.ok(),
      { timeout: 15000 },
    );
    const saveResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/notebooks/${notebook.id}/cells/${cellId}`) &&
        response.request().method() === "PUT",
      { timeout: 15000 },
    );
    await cell.getByRole("button", { name: /^Format SQL/ }).click();
    expect((await formatRequest).postDataJSON()).toMatchObject({ persist: false });
    await formatResponse;

    await expect
      .poll(() =>
        page.evaluate((targetCellId) => {
          const monaco = (window as typeof window & { monaco?: any }).monaco;
          return (
            monaco?.editor
              .getModels?.()
              .find((candidate: any) =>
                candidate.uri.toString().includes(`/notebook/${targetCellId}.`),
              )
              ?.getValue() ?? ""
          );
        }, cellId),
      )
      .toContain("SELECT\n");

    await page.getByText("Format Queue", { exact: true }).first().click();
    expect((await saveResponse).status()).toBe(200);

    await expect
      .poll(async () => {
        const response = await page.request.get(`${liveApp.baseURL}/api/notebooks/${notebook.id}`);
        const payload = (await response.json()) as NotebookEnvelope;
        return payload.notebook.cells.find((candidate) => candidate.cell_id === cellId)?.content;
      })
      .toContain("SELECT\n");
  });

  test("cell editors can be resized vertically", async ({ liveApp, page }) => {
    const notebook = await createNotebook(page.request, liveApp.baseURL, "Resizable Cells");
    const cellId = await addCell(page.request, liveApp.baseURL, notebook.id, "resizable");
    await setCell(page.request, liveApp.baseURL, notebook.id, cellId, "select 1 as value");

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    const cell = page.locator(`[data-notebook-cell-id="${cellId}"]`);
    await expect(cell.locator(".monaco-editor")).toBeVisible({ timeout: 15000 });

    const shortEditorScrollMetrics = await page.evaluate((targetCellId) => {
      const monaco = (window as typeof window & { monaco?: any }).monaco;
      const target = monaco?.editor
        .getEditors?.()
        .find((candidate: any) =>
          candidate.getModel?.()?.uri.toString().includes(`/notebook/${targetCellId}.`),
        );
      return target
        ? {
            layoutHeight: target.getLayoutInfo().height,
            scrollHeight: target.getScrollHeight(),
          }
        : null;
    }, cellId);
    expect(shortEditorScrollMetrics).not.toBeNull();
    expect(shortEditorScrollMetrics!.scrollHeight).toBeLessThanOrEqual(
      shortEditorScrollMetrics!.layoutHeight + 1,
    );

    const editor = cell.locator('[data-slot="notebook-cell-editor"]');
    const handle = cell.getByRole("separator", { name: "Resize resizable cell" });
    await expect(handle).toBeVisible();
    const initialEditorHeight = await editor.evaluate(
      (element) => element.getBoundingClientRect().height,
    );
    const initialCellHeight = await cell.evaluate(
      (element) => element.getBoundingClientRect().height,
    );
    const handleBox = await handle.boundingBox();
    expect(handleBox).not.toBeNull();

    await page.mouse.move(
      handleBox!.x + handleBox!.width / 2,
      handleBox!.y + handleBox!.height / 2,
    );
    await page.mouse.down();
    await page.mouse.move(
      handleBox!.x + handleBox!.width / 2,
      handleBox!.y + handleBox!.height / 2 + 140,
      { steps: 5 },
    );
    await page.mouse.up();

    await expect
      .poll(() => editor.evaluate((element) => element.getBoundingClientRect().height))
      .toBeGreaterThan(initialEditorHeight + 100);
    await expect
      .poll(() => cell.evaluate((element) => element.getBoundingClientRect().height))
      .toBeGreaterThan(initialCellHeight + 100);

    const draggedHeight = await editor.evaluate(
      (element) => element.getBoundingClientRect().height,
    );
    await handle.focus();
    await page.keyboard.press("ArrowUp");
    await expect
      .poll(() => editor.evaluate((element) => element.getBoundingClientRect().height))
      .toBeLessThan(draggedHeight);

    await handle.dblclick();
    await expect
      .poll(() => editor.evaluate((element) => element.getBoundingClientRect().height))
      .toBe(initialEditorHeight);
  });

  test("new cells show a pending card, animate in, and keep the notebook at the bottom", async ({
    liveApp,
    page,
  }) => {
    const notebook = await createNotebook(page.request, liveApp.baseURL, "Cell Creation");
    const existingCellIDs = new Set(notebook.cells.map((cell) => cell.cell_id));
    for (let index = 0; index < 8; index += 1) {
      existingCellIDs.add(
        await addCell(page.request, liveApp.baseURL, notebook.id, `existing_${index + 1}`),
      );
    }

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Cell Creation").first()).toBeVisible({ timeout: 15000 });

    const viewport = page
      .getByTestId("notebook-scroll-area")
      .locator(':scope > [data-slot="scroll-area-viewport"]');
    await openNotebookToolsTab(page, "Add");
    const addSQLCell = page.getByTitle("Drag SQL between notebook blocks, or click to add");
    await addSQLCell.scrollIntoViewIfNeeded();

    let releaseRequest = () => {};
    const requestGate = new Promise<void>((resolve) => {
      releaseRequest = resolve;
    });
    let markRequestStarted = () => {};
    const requestStarted = new Promise<void>((resolve) => {
      markRequestStarted = resolve;
    });
    let routeContinuation = Promise.resolve();
    const createRoute = `**/notebooks/${notebook.id}/changes/apply`;
    await page.route(createRoute, async (route) => {
      if (route.request().method() !== "POST") {
        await route.continue();
        return;
      }
      routeContinuation = (async () => {
        markRequestStarted();
        await requestGate;
        await route.continue();
      })();
      await routeContinuation;
    });

    const createResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/notebooks/${notebook.id}/changes/apply`) &&
        response.request().method() === "POST",
      { timeout: 15000 },
    );
    let released = false;
    try {
      await addSQLCell.click();
      await requestStarted;

      const pending = page.getByRole("status", { name: "Adding SQL cell" });
      await expect(pending).toBeVisible();
      await expect(pending).toHaveAttribute("data-notebook-block-pending", "sql");
      await expect(pending).toHaveClass(/animate-in/);
      await expect(
        page.getByRole("button", { name: "Insert notebook block here" }).first(),
      ).toBeDisabled();
      await expect
        .poll(() =>
          viewport.evaluate(
            (element) => element.scrollHeight - element.clientHeight - element.scrollTop,
          ),
        )
        .toBeLessThanOrEqual(2);

      releaseRequest();
      released = true;
      const response = await createResponse;
      expect(response.ok()).toBe(true);
      const updatedNotebook = ((await response.json()) as NotebookEnvelope).notebook;
      const generatedCell = updatedNotebook.cells.find(
        (cell) => !existingCellIDs.has(cell.cell_id),
      );
      expect(generatedCell?.name).toMatch(/^[a-z]+_[a-z]+$/);
      await expect(pending).toBeHidden();

      const created = page.locator("[data-notebook-cell-id]").last();
      await expect(created).toHaveAttribute("data-notebook-block-entering", "true");
      await expect(created).toHaveClass(/animate-in/);
      await expect
        .poll(() =>
          viewport.evaluate(
            (element) => element.scrollHeight - element.clientHeight - element.scrollTop,
          ),
        )
        .toBeLessThanOrEqual(2);
    } finally {
      if (!released) {
        releaseRequest();
      }
      await routeContinuation;
      await page.unroute(createRoute);
    }
  });

  test("markdown cells can be added and visually edited", async ({ liveApp, page }) => {
    const notebook = await createNotebook(page.request, liveApp.baseURL, "Markdown Creation");

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Markdown Creation").first()).toBeVisible({ timeout: 15000 });

    await openNotebookToolsTab(page, "Add");
    const applyResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/notebooks/${notebook.id}/changes/apply`) &&
        response.request().method() === "POST",
      { timeout: 15000 },
    );
    await page.getByTitle("Drag Text between notebook blocks, or click to add").click();
    expect((await applyResponse).ok()).toBe(true);

    const editor = page.getByLabel("Markdown cell");
    await expect(editor).toHaveAttribute("contenteditable", "true");
    await expect(editor).toHaveAttribute("spellcheck", "false");
    await editor.focus();
    await expect(editor).toHaveAttribute("spellcheck", "true");
    await expect(page.locator("[data-notebook-markdown-index]").last()).toHaveAttribute(
      "data-notebook-block-selected",
      "true",
    );
    await editor.fill("A ");
    await page.getByRole("button", { name: "Bold", exact: true }).click();
    await editor.pressSequentially("visual");
    await page.getByRole("button", { name: "Bold", exact: true }).click();
    await editor.pressSequentially(" note");

    const saveResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/notebooks/${notebook.id}/changes/apply`) &&
        response.request().method() === "POST",
      { timeout: 15000 },
    );
    await page.getByRole("heading", { name: "Markdown Creation" }).first().click();
    expect((await saveResponse).ok()).toBe(true);
    await expect(editor).toHaveAttribute("spellcheck", "false");

    const response = await page.request.get(`${liveApp.baseURL}/api/notebooks/${notebook.id}`);
    expect(response.ok()).toBe(true);
    const updated = ((await response.json()) as NotebookEnvelope).notebook;
    expect(
      updated.blocks?.some(
        (block) => block.id && block.markdown?.trim() === "## A **visual** note",
      ),
    ).toBe(true);

    await page.reload();
    await expect(page.getByRole("heading", { name: "Markdown Creation" }).first()).toBeVisible({
      timeout: 15000,
    });
    await expect(page.getByLabel("Markdown cell")).toContainText("A visual note");
    await expect(page.getByText("Write a note…", { exact: true })).toBeHidden();

    await page.getByLabel("Markdown cell").hover();
    await page.getByRole("button", { name: "Edit Markdown source" }).click();
    const source = page.getByLabel("Markdown source");
    await source.fill(
      "## A **visual** note\n\n- First bullet\n- Second bullet\n\n1. First step\n2. Second step",
    );
    const listSaveResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/notebooks/${notebook.id}/changes/apply`) &&
        response.request().method() === "POST",
      { timeout: 15000 },
    );
    await page.getByRole("button", { name: "Use visual Markdown editor" }).click();
    expect((await listSaveResponse).ok()).toBe(true);

    const visualEditor = page.getByLabel("Markdown cell");
    await expect(visualEditor.locator("ul > li")).toHaveCount(2);
    await expect(visualEditor.locator("ol > li")).toHaveCount(2);
    await expect
      .poll(() =>
        visualEditor.locator("ul").evaluate((element) => getComputedStyle(element).listStyleType),
      )
      .toBe("disc");
    await expect
      .poll(() =>
        visualEditor.locator("ol").evaluate((element) => getComputedStyle(element).listStyleType),
      )
      .toBe("decimal");
  });

  test("cell deletion uses an in-app confirmation dialog", async ({ liveApp, page }) => {
    const notebook = await createNotebook(page.request, liveApp.baseURL, "Cell Deletion");
    const cellId = await addCell(page.request, liveApp.baseURL, notebook.id, "delete_me");
    const nativeDialogs: string[] = [];
    page.on("dialog", async (dialog) => {
      nativeDialogs.push(dialog.type());
      await dialog.dismiss();
    });

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Cell Deletion").first()).toBeVisible({ timeout: 15000 });

    const cell = page.locator(`[data-notebook-cell-id="${cellId}"]`);
    await cell.getByRole("button", { name: "Cell actions" }).click();
    await page.getByRole("menuitem", { name: "Delete cell" }).click();

    const dialog = page.getByRole("alertdialog");
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText("Delete delete_me?", { exact: true })).toBeVisible();
    expect(nativeDialogs).toEqual([]);

    await dialog.getByRole("button", { name: "Cancel" }).click();
    await expect(dialog).toBeHidden();
    await expect(cell).toBeVisible();

    await cell.getByRole("button", { name: "Cell actions" }).click();
    await page.getByRole("menuitem", { name: "Delete cell" }).click();
    let releaseRequest = () => {};
    const requestGate = new Promise<void>((resolve) => {
      releaseRequest = resolve;
    });
    let markRequestStarted = () => {};
    const requestStarted = new Promise<void>((resolve) => {
      markRequestStarted = resolve;
    });
    const deleteRoute = `**/notebooks/${notebook.id}/cells/${cellId}`;
    await page.route(deleteRoute, async (route) => {
      if (route.request().method() !== "DELETE") {
        await route.continue();
        return;
      }
      markRequestStarted();
      await requestGate;
      await route.continue();
    });
    const deleteResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/notebooks/${notebook.id}/cells/${cellId}`) &&
        response.request().method() === "DELETE",
      { timeout: 15000 },
    );
    let released = false;
    try {
      const confirmDelete = dialog.getByRole("button", { name: "Delete cell" });
      await confirmDelete.click();
      await requestStarted;
      await expect(dialog).toBeVisible();
      await expect(confirmDelete).toBeDisabled();
      await expect(dialog.getByRole("status", { name: "Deleting cell" })).toBeVisible();

      releaseRequest();
      released = true;
      expect((await deleteResponse).ok()).toBe(true);
    } finally {
      if (!released) {
        releaseRequest();
      }
      await page.unroute(deleteRoute);
    }

    await expect(dialog).toBeHidden();
    await expect(cell).toHaveCount(0);
    expect(nativeDialogs).toEqual([]);
  });

  test("a Python cell queries an upstream cell through the renart SDK", async ({
    liveApp,
    page,
  }) => {
    test.setTimeout(120000);
    const { request } = page;
    const notebook = await createNotebook(request, liveApp.baseURL, "Python Query");

    const baseCell = await addCell(request, liveApp.baseURL, notebook.id, "base");
    await setCell(
      request,
      liveApp.baseURL,
      notebook.id,
      baseCell,
      "select 10 as amount union all select 20",
    );
    const pythonCell = await addPythonCell(request, liveApp.baseURL, notebook.id, "doubled");
    await setPythonCell(
      request,
      liveApp.baseURL,
      notebook.id,
      pythonCell,
      [
        "import os",
        "import pyarrow as pa",
        "",
        "from renart import query",
        "",
        "",
        "def materialize():",
        '    assert "RENART_NOTEBOOK_INPUTS" not in os.environ',
        '    doubled = query("select amount * 2 as doubled from base order by 1")',
        "    assert isinstance(doubled, pa.Table)",
        "    return doubled",
      ].join("\n"),
    );

    const response = await request.post(`${liveApp.baseURL}/api/notebooks/${notebook.id}/run`, {
      data: { all: true },
      timeout: 110000,
    });
    expect(response.ok()).toBe(true);
    const payload = (await response.json()) as {
      status: string;
      results: Array<{
        name: string;
        status: string;
        rows: unknown[][];
        error?: string;
        logs?: string;
      }>;
    };
    const doubled = payload.results.find((result) => result.name === "doubled")!;
    expect(doubled.status, `${doubled.error ?? ""}\n${doubled.logs ?? ""}`).toBe("ok");
    expect(doubled.rows).toEqual([[20], [40]]);
    expect(existsSync(join(liveApp.workspaceDir, notebook.path, "pyproject.toml"))).toBe(false);
  });

  test("offers sibling SQL completion inside a Python notebook query literal", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Monaco suggestions are only stable in the desktop notebook layout.",
    );

    const notebook = await createNotebook(page.request, liveApp.baseURL, "Python SQL IntelliSense");
    const baseCell = await addPythonCell(page.request, liveApp.baseURL, notebook.id, "base");
    await setPythonCell(
      page.request,
      liveApp.baseURL,
      notebook.id,
      baseCell,
      [
        "import pyarrow as pa",
        "",
        "",
        "def materialize():",
        '    return pa.table({"runtime_amount": [10], "runtime_customer": ["Ada"]})',
      ].join("\n"),
    );
    const staticCell = await addCell(page.request, liveApp.baseURL, notebook.id, "some_cell");
    await setCell(
      page.request,
      liveApp.baseURL,
      notebook.id,
      staticCell,
      "select 1 as col_identifier, 'Ada' as col_name",
    );
    const pythonCell = await addPythonCell(page.request, liveApp.baseURL, notebook.id, "reader");
    const savedPythonBody = [
      "from renart import query",
      "",
      'result = query("select * from base")',
    ].join("\n");
    await setPythonCell(page.request, liveApp.baseURL, notebook.id, pythonCell, savedPythonBody);

    // Python output columns are not statically available to the SQL LSP. They
    // enter the notebook's schema context only through the last run result, so
    // this completion proves the embedded query adapter consumes runtime data.
    const runResponse = await page.request.post(
      `${liveApp.baseURL}/api/notebooks/${notebook.id}/run`,
      {
        data: { cells: [baseCell] },
        timeout: 110000,
      },
    );
    expect(runResponse.ok()).toBe(true);
    const runPayload = (await runResponse.json()) as {
      results: Array<{ cell_id: string; status: string; columns: string[]; error?: string }>;
    };
    expect(runPayload.results.find((result) => result.cell_id === baseCell)).toMatchObject({
      status: "ok",
      columns: ["runtime_amount", "runtime_customer"],
    });

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Python SQL IntelliSense").first()).toBeVisible({ timeout: 15000 });
    await expect(
      page.locator(`[data-notebook-cell-id="${pythonCell}"] .monaco-editor`),
    ).toBeVisible({
      timeout: 15000,
    });
    const unfinishedBody = [
      "from renart import query",
      "",
      'result = query("select * from base as b where b',
    ].join("\n");
    await setNotebookEditorValue(page, pythonCell, unfinishedBody, {
      cursorOffset: unfinishedBody.length,
    });
    await page.keyboard.type(".");

    await expect(
      page
        .locator(".suggest-widget .monaco-list-row")
        .filter({ hasText: "runtime_amount" })
        .first(),
    ).toBeVisible({ timeout: 15000 });
    await expect(
      page
        .locator(`[data-notebook-cell-id="${pythonCell}"] .bruin-python-sql-keyword`)
        .filter({ hasText: "select" })
        .first(),
    ).toBeVisible({ timeout: 15000 });

    // Unqualified columns should be inferred statically from a never-run SQL
    // sibling, not only offered after an alias dot or a prior runtime result.
    // Keep the literal closed to exercise the normal mid-string cursor mapping
    // used while editing an existing query.
    await page.keyboard.press("Escape");
    const whereBody = [
      "from renart import query",
      "",
      'result = query("select * from some_cell where col_")',
    ].join("\n");
    await setNotebookEditorValue(page, pythonCell, whereBody, {
      cursorOffset: whereBody.lastIndexOf('"'),
    });
    await page.keyboard.type("n");
    await expect(
      page.locator(".suggest-widget .monaco-list-row").filter({ hasText: "col_name" }).first(),
    ).toBeVisible({ timeout: 15000 });

    await page.keyboard.press("Escape");
    const selectBody = [
      "from renart import query",
      "",
      'result = query("select runtime_ from base")',
    ].join("\n");
    await setNotebookEditorValue(page, pythonCell, selectBody, {
      cursorOffset: selectBody.indexOf(" from base"),
    });
    await page.keyboard.type("a");
    await expect(
      page
        .locator(".suggest-widget .monaco-list-row")
        .filter({ hasText: "runtime_amount" })
        .first(),
    ).toBeVisible({ timeout: 15000 });

    // The projection supports both SDK spellings and raw/triple-quoted SQL,
    // which is common for readable multi-line Python queries.
    await page.keyboard.press("Escape");
    const tripleBody = [
      "import renart",
      "",
      'result = renart.query(r"""select * from base as b where b.runtime_""")',
    ].join("\n");
    await setNotebookEditorValue(page, pythonCell, tripleBody, {
      cursorOffset: tripleBody.lastIndexOf('"""'),
    });
    await page.keyboard.type("a");
    await expect(
      page
        .locator(".suggest-widget .monaco-list-row")
        .filter({ hasText: "runtime_amount" })
        .first(),
    ).toBeVisible({ timeout: 15000 });

    await page.keyboard.press("Escape");
    const closedBody = ["from renart import query", "", 'result = query("select * from ba")'].join(
      "\n",
    );
    await setNotebookEditorValue(page, pythonCell, closedBody, {
      cursorOffset: closedBody.lastIndexOf('"'),
    });
    await page.keyboard.type("s");
    await expect(
      page.locator(".suggest-widget .monaco-list-row").filter({ hasText: "base" }).first(),
    ).toBeVisible({ timeout: 15000 });
  });

  test("rename is reference-rewriting and visualization blocks have a checked definition editor", async ({
    liveApp,
    page,
  }) => {
    await page.addInitScript(() => {
      window.localStorage.setItem("renart-notebook-autorecompute", "off");
    });
    const notebook = await createNotebook(page.request, liveApp.baseURL, "Viz And Rename");
    const disableAutoRecompute = await page.request.put(
      `${liveApp.baseURL}/api/notebooks/${notebook.id}/settings`,
      { data: { auto_recompute: false, environment: "default" } },
    );
    expect(disableAutoRecompute.ok()).toBe(true);
    const baseCell = await addCell(page.request, liveApp.baseURL, notebook.id, "base");
    await setCell(
      page.request,
      liveApp.baseURL,
      notebook.id,
      baseCell,
      "select 'jan' as month, 10 as revenue union all select 'feb', 20",
    );
    const chartCell = await addCell(page.request, liveApp.baseURL, notebook.id, "chart");
    await setCell(
      page.request,
      liveApp.baseURL,
      notebook.id,
      chartCell,
      "select month, revenue from base order by 1",
    );

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    const runSeen = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === `/api/notebooks/${notebook.id}/run` &&
        response.request().method() === "POST" &&
        response.ok(),
      {
        timeout: 30000,
      },
    );
    await page.getByRole("button", { name: "Run all" }).click();
    await runSeen;
    // Result table renders the column.
    await expect(page.getByText("revenue", { exact: true }).first()).toBeVisible({
      timeout: 15000,
    });

    // A new visualization is a durable manifest block, not a SQL comment.
    const createSeen = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/notebooks/${notebook.id}/changes/apply`) &&
        response.request().method() === "POST" &&
        response.ok(),
      { timeout: 15000 },
    );
    await openNotebookToolsTab(page, "Add");
    const linePreview = page.getByRole("button", { name: "Line", exact: true });
    if ((page.viewportSize()?.width ?? 0) >= 1280) {
      await linePreview.dragTo(
        page.locator(`[data-notebook-insertion-point="after:${chartCell}"]`),
      );
    } else {
      await linePreview.click();
    }
    await createSeen;
    const afterCreate = (await (
      await page.request.get(`${liveApp.baseURL}/api/notebooks/${notebook.id}`)
    ).json()) as NotebookEnvelope;
    const visualization = afterCreate.notebook.blocks?.find((block) => block.visualization);
    if (!visualization?.id || !visualization.visualization) {
      throw new Error("Visualization block was not persisted");
    }
    expect(visualization.visualization.source).toBe(chartCell);
    expect(visualization.visualization.definition.type).toBe("line");
    expect(
      afterCreate.notebook.cells.find((cell) => cell.cell_id === chartCell)?.content,
    ).not.toContain("@viz");

    const visualizationCard = page.locator(
      `[data-notebook-visualization-id="${visualization.id}"]`,
    );
    await expect(visualizationCard).toBeVisible({ timeout: 15000 });
    expect(
      await visualizationCard
        .locator('[data-slot="delimited-card"]')
        .evaluate((element) => getComputedStyle(element).backgroundColor),
    ).toBe("rgba(0, 0, 0, 0)");
    const visualizationInspector = page.getByTestId("notebook-visualization-inspector");
    await expect(visualizationInspector).toBeVisible({ timeout: 15000 });
    const inspectorWidth = await visualizationInspector.evaluate((element) => ({
      client: element.clientWidth,
      scroll: element.scrollWidth,
    }));
    expect(inspectorWidth.scroll).toBeLessThanOrEqual(inspectorWidth.client + 1);
    const definitionTab = visualizationInspector.getByRole("tab", { name: "Definition" });
    await expect(definitionTab).toBeEnabled({ timeout: 15000 });
    await definitionTab.click();
    await expect(
      visualizationInspector.getByRole("textbox", { name: "Editor content" }),
    ).toBeVisible({ timeout: 15000 });
    const checked = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/notebooks/${notebook.id}/visualizations/check`) &&
        response.request().method() === "POST" &&
        response.ok(),
      { timeout: 15000 },
    );
    await setVisualizationDefinitionValue(
      page,
      visualization.id,
      [
        "version: 1",
        "type: bar",
        "palette: ocean",
        "title: Monthly revenue",
        "encoding:",
        "  x:",
        "    field: month",
        "  y:",
        "    - field: revenue",
      ].join("\n"),
    );
    await checked;
    const applySeen = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/notebooks/${notebook.id}/changes/apply`) &&
        response.request().method() === "POST" &&
        response.ok(),
      { timeout: 15000 },
    );
    const applyButton = visualizationInspector.getByRole("button", {
      name: "Apply visualization",
    });
    await expect(applyButton).toBeEnabled({ timeout: 15000 });
    await applyButton.click();
    await applySeen;
    await visualizationInspector.getByRole("button", { name: "Close inspector" }).click();
    await expect(visualizationCard.getByText("Monthly revenue", { exact: true })).toBeVisible();
    const editVisualization = visualizationCard.getByRole("button", {
      name: "Edit visualization Monthly revenue",
    });
    await expect(editVisualization).toBeHidden();
    await visualizationCard.getByRole("region", { name: "Visualization: Monthly revenue" }).click();
    if ((page.viewportSize()?.width ?? 0) >= 1280) {
      await expect(editVisualization).toBeVisible();
      await editVisualization.click();
    }
    await expect(visualizationInspector).toBeVisible();
    await visualizationInspector.getByRole("button", { name: "Close inspector" }).click();

    const afterViz = (await (
      await page.request.get(`${liveApp.baseURL}/api/notebooks/${notebook.id}`)
    ).json()) as NotebookEnvelope;
    const savedVisualization = afterViz.notebook.blocks?.find(
      (block) => block.id === visualization.id,
    );
    expect(savedVisualization?.visualization?.definition.type).toBe("bar");
    expect(savedVisualization?.visualization?.definition.palette).toBe("ocean");

    // Rename base → revenue and confirm the chart cell's reference updated.
    const renamed = await page.request.post(
      `${liveApp.baseURL}/api/notebooks/${notebook.id}/cells/${baseCell}/rename`,
      {
        data: { name: "revenue" },
      },
    );
    expect(renamed.ok()).toBe(true);

    const final = (await (
      await page.request.get(`${liveApp.baseURL}/api/notebooks/${notebook.id}`)
    ).json()) as NotebookEnvelope;
    const chartFinal = final.notebook.cells.find((cell) => cell.cell_id === chartCell)!;
    expect(chartFinal.content).toContain("from revenue");
    expect(chartFinal.content).not.toContain("@viz");
    expect(final.notebook.cells.find((cell) => cell.cell_id === baseCell)!.name).toBe("revenue");
  });

  test("drags a typed control between notebook blocks and inserts text at the same gap", async ({
    liveApp,
    page,
  }) => {
    test.skip(
      test.info().project.name.includes("mobile"),
      "Desktop drag and drop keeps the Add rail and notebook insertion target visible together.",
    );
    const notebook = await createNotebook(page.request, liveApp.baseURL, "Ordered Controls");
    const firstCell = notebook.cells[0].cell_id;
    const secondCell = await addCell(page.request, liveApp.baseURL, notebook.id, "second");

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await openNotebookToolsTab(page, "Add");
    const insertion = page.locator(`[data-notebook-insertion-point="after:${firstCell}"]`);
    await expect(insertion).toBeVisible();
    const controlApply = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/notebooks/${notebook.id}/changes/apply`) && response.ok(),
    );
    await page.getByRole("button", { name: "Slider", exact: true }).dragTo(insertion);
    await controlApply;

    const afterControl = (await (
      await page.request.get(`${liveApp.baseURL}/api/notebooks/${notebook.id}`)
    ).json()) as NotebookEnvelope;
    const control = afterControl.notebook.blocks?.find((block) => block.control);
    expect(control?.control).toBeTruthy();
    expect(
      afterControl.notebook.parameters?.find((parameter) => parameter.id === control?.control),
    ).toMatchObject({ type: "slider", default: 50, min: 0, max: 100, step: 1 });
    expect(afterControl.notebook.blocks?.map((block) => block.cell || block.control)).toEqual([
      firstCell,
      control?.control,
      secondCell,
    ]);
    const controlInspector = page.getByTestId("notebook-control-inspector");
    await expect(controlInspector).toBeVisible();
    await controlInspector.getByRole("button", { name: "Close inspector" }).click();
    await expect(controlInspector).toBeHidden();
    const renderedControl = page.locator(`[data-notebook-control-id="${control?.control}"]`);
    await renderedControl.getByRole("slider").press("ArrowRight");
    await expect(controlInspector).toBeHidden();

    const afterControlInsertion = page.locator(
      `[data-notebook-insertion-point="after:control:${control?.control}"]`,
    );
    await afterControlInsertion.getByRole("button", { name: "Insert notebook block here" }).click();
    const insertionPicker = page.getByTestId("notebook-insert-picker");
    await expect(insertionPicker).toBeVisible();
    const sqlChoice = insertionPicker.locator('[aria-label="SQL"]');
    const pythonChoice = insertionPicker.locator('[aria-label="Python"]');
    const textChoice = insertionPicker.locator('[aria-label="Text"]');
    await expect(sqlChoice).toBeVisible();
    await expect(pythonChoice).toBeVisible();
    const sqlPreview = sqlChoice.locator("[aria-hidden=true]").first();
    const chartPreview = insertionPicker
      .getByRole("button", { name: "Chart", exact: true })
      .locator("svg")
      .first();
    expect(
      Math.abs(
        (await sqlPreview.evaluate((element) => element.getBoundingClientRect().height)) -
          (await chartPreview.evaluate((element) => element.getBoundingClientRect().height)),
      ),
    ).toBeLessThanOrEqual(1);
    await insertionPicker.getByRole("button", { name: "Control", exact: true }).click();
    await expect(insertionPicker.locator('[aria-label="Slider"] svg')).toBeVisible();
    await insertionPicker.getByRole("button", { name: "Chart", exact: true }).click();
    await expect(insertionPicker.locator('[aria-label="Line"] svg')).toBeVisible();
    const textApply = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/notebooks/${notebook.id}/changes/apply`) && response.ok(),
    );
    await textChoice.click();
    await textApply;

    const afterText = (await (
      await page.request.get(`${liveApp.baseURL}/api/notebooks/${notebook.id}`)
    ).json()) as NotebookEnvelope;
    expect(
      afterText.notebook.blocks?.map((block) => block.cell || block.control || block.markdown),
    ).toEqual([firstCell, control?.control, "## Notes", secondCell]);

    const deleteControl = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/notebooks/${notebook.id}/changes/apply`) && response.ok(),
    );
    const controlCell = page.locator(`[data-notebook-control-id="${control?.control}"]`);
    const controlWidths = await Promise.all([
      controlCell.evaluate((element) => element.getBoundingClientRect().width),
      controlCell
        .locator('[data-slot="slider"]')
        .evaluate((element) => element.getBoundingClientRect().width),
    ]);
    expect(controlWidths[1]).toBeGreaterThanOrEqual(controlWidths[0] - 26);
    await controlCell.hover();
    await controlCell.getByRole("button", { name: /Delete control/ }).click();
    await deleteControl;
    await expect(controlCell).toHaveCount(0);
  });

  test("promote a cell into a pipeline asset and rewrite remaining references", async ({
    liveApp,
    page,
  }) => {
    const notebook = await createNotebook(page.request, liveApp.baseURL, "Promotion");
    const baseCell = await addCell(page.request, liveApp.baseURL, notebook.id, "base");
    await setCell(
      page.request,
      liveApp.baseURL,
      notebook.id,
      baseCell,
      "select 1 as id, 10 as amount",
    );
    const childCell = await addCell(page.request, liveApp.baseURL, notebook.id, "child");
    await setCell(
      page.request,
      liveApp.baseURL,
      notebook.id,
      childCell,
      "select sum(amount) as total from base",
    );

    const pipelineId = Buffer.from("analytics").toString("base64url");
    const response = await page.request.post(
      `${liveApp.baseURL}/api/notebooks/${notebook.id}/cells/${baseCell}/promote`,
      { data: { pipeline_id: pipelineId, target_name: "marts.promoted_base" } },
    );
    expect(response.ok()).toBe(true);
    const result = (await response.json()) as {
      status: string;
      asset_path: string;
      notebook: NotebookEnvelope["notebook"];
    };
    expect(result.status).toBe("ok");
    expect(result.asset_path).toContain("analytics/assets/");

    // The promoted asset now shows up as a pipeline asset, class=pipeline.
    const workspace = (await (
      await page.request.get(`${liveApp.baseURL}/api/workspace`)
    ).json()) as {
      pipelines: Array<{ assets: Array<{ name: string; class?: string }> }>;
    };
    const promoted = workspace.pipelines
      .flatMap((p) => p.assets)
      .find((asset) => asset.name === "marts.promoted_base");
    expect(promoted?.class).toBe("pipeline");

    // The remaining child cell references the promoted pipeline asset.
    const child = result.notebook.cells.find((cell) => cell.cell_id === childCell)!;
    expect(child.content).toContain("from marts.promoted_base");
    expect(result.notebook.cells.some((cell) => cell.cell_id === baseCell)).toBe(false);
  });

  test("manage Python dependencies via the dialog and the missing-import suggestion", async ({
    liveApp,
    page,
  }) => {
    const { request } = page;
    const notebook = await createNotebook(request, liveApp.baseURL, "Deps");
    const fetchCell = await addPythonCell(request, liveApp.baseURL, notebook.id, "fetch");
    await setPythonCell(
      request,
      liveApp.baseURL,
      notebook.id,
      fetchCell,
      "import os\nimport requests\n\n\ndef materialize():\n    return requests.get(os.environ['URL']).json()",
    );

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Deps").first()).toBeVisible({ timeout: 15000 });

    // The cell imports `requests`, which is not declared → a suggestion appears.
    await expect(page.getByText("Imported but not in dependencies:")).toBeVisible({
      timeout: 15000,
    });
    const importButton = page.getByRole("button", { name: "requests" });
    await expect(importButton).toBeVisible();

    // Clicking it searches PyPI and offers candidate packages; picking one adds
    // it to the dependencies and clears the suggestion.
    await importButton.click();
    const candidate = page.getByRole("menuitem", { name: /requests/i }).first();
    await expect(candidate).toBeVisible({ timeout: 20000 });
    const addSaved = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/notebooks/${notebook.id}/dependencies`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    await candidate.click();
    await addSaved;
    expect(await getDependencies(request, liveApp.baseURL, notebook.id)).toContain("requests");
    await expect(page.getByText("Imported but not in dependencies:")).toBeHidden({
      timeout: 15000,
    });

    // The Dependencies dialog edits the dependency list directly.
    await page.getByRole("button", { name: "Dependencies" }).click();
    const editor = page.getByLabel("dependencies", { exact: true });
    await expect(editor).toBeVisible();
    await editor.fill("pandas\nrequests\nnumpy");
    const dialogSaved = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/notebooks/${notebook.id}/dependencies`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    await page.getByRole("button", { name: "Save" }).click();
    await dialogSaved;

    const dependencies = await getDependencies(request, liveApp.baseURL, notebook.id);
    expect(dependencies).toContain("numpy");
    expect(dependencies).toContain("pandas");
  });

  test("resolves imports against the venv so import≠package names are not flagged", async ({
    liveApp,
    page,
  }) => {
    const { request } = page;
    const notebook = await createNotebook(request, liveApp.baseURL, "Resolve");

    // Simulate an installed package whose import name differs from its PyPI
    // name (skimage is provided by scikit-image) by dropping it into the
    // notebook's venv site-packages.
    const venvSite = join(
      liveApp.workspaceDir,
      ".renart",
      "notebooks",
      "venvs",
      basename(notebook.path),
      "lib",
      "python3.11",
      "site-packages",
      "skimage",
    );
    mkdirSync(venvSite, { recursive: true });
    writeFileSync(join(venvSite, "__init__.py"), "# skimage\n");

    const cell = await addPythonCell(request, liveApp.baseURL, notebook.id, "imps");
    await setPythonCell(
      request,
      liveApp.baseURL,
      notebook.id,
      cell,
      "import skimage\nimport totally_made_up_pkg\n\n\ndef materialize():\n    return None",
    );

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Resolve").first()).toBeVisible({ timeout: 15000 });

    // The unresolved import is flagged; the installed one (import≠package) is not.
    await expect(page.getByText("Imported but not in dependencies:")).toBeVisible({
      timeout: 15000,
    });
    await expect(page.getByRole("button", { name: "totally_made_up_pkg" })).toBeVisible();
    await expect(page.getByRole("button", { name: "skimage" })).toHaveCount(0);
  });

  test("the Python language server resolves notebook cell imports against the notebook venv", async ({
    liveApp,
    page,
  }) => {
    const { request } = page;
    const notebook = await createNotebook(request, liveApp.baseURL, "LSP");

    // Drop a package into the notebook's per-notebook venv site-packages so the
    // language server (ty) can resolve it. The venv lives outside the notebook
    // folder, so this exercises the notebook-venv search root specifically.
    const venvSite = join(
      liveApp.workspaceDir,
      ".renart",
      "notebooks",
      "venvs",
      basename(notebook.path),
      "lib",
      "python3.11",
      "site-packages",
      "marshmallow",
    );
    mkdirSync(venvSite, { recursive: true });
    writeFileSync(join(venvSite, "__init__.py"), "value = 1\n");

    const cellId = await addPythonCell(request, liveApp.baseURL, notebook.id, "lsp");
    const assetId = await getCellAssetId(request, liveApp.baseURL, notebook.id, cellId);

    // The installed package resolves (no unresolved-import); a missing one does
    // not — the language server is wired to the notebook cell and its venv.
    const diagnostics = await pythonDiagnostics(
      request,
      liveApp.baseURL,
      assetId,
      "import marshmallow\nimport totally_made_up_pkg\n",
    );
    expect(diagnostics.status).toBe("ok");
    const messages = (diagnostics.diagnostics ?? []).map((diagnostic) => diagnostic.message);
    expect(messages.join("\n")).toContain("totally_made_up_pkg");
    expect(messages.join("\n")).not.toContain("marshmallow");
  });

  test("promote dialog can pull in downstream assets", async ({ liveApp, page }) => {
    test.setTimeout(timeoutForRetry(test.info(), 60000, 60000));
    const { request } = page;
    const notebook = await createNotebook(request, liveApp.baseURL, "Promote Chain");
    const baseCell = await addCell(request, liveApp.baseURL, notebook.id, "base");
    await setCell(request, liveApp.baseURL, notebook.id, baseCell, "select 1 as id, 10 as amount");
    const childCell = await addCell(request, liveApp.baseURL, notebook.id, "child");
    await setCell(
      request,
      liveApp.baseURL,
      notebook.id,
      childCell,
      "select sum(amount) as total from base",
    );

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Promote Chain").first()).toBeVisible({ timeout: 15000 });

    // Open the base cell's actions menu and start a promotion.
    const baseCard = page.locator(`[data-notebook-cell-id="${baseCell}"]`);
    await baseCard.getByRole("button", { name: "Cell actions" }).click();
    await page.getByRole("menuitem", { name: "Promote to pipeline" }).click();

    // The dialog offers to also promote the downstream cell.
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText("Promote to pipeline")).toBeVisible();
    const downstreamCheckbox = dialog.getByRole("checkbox", { name: /Downstream assets/ });
    await downstreamCheckbox.check();
    await expect(downstreamCheckbox).toBeChecked();
    // A notebook/workspace refresh re-renders the parent and rebuilds its
    // pipeline options. Keep the user's dialog choice through that refresh.
    const refreshedChildSQL = "select sum(amount) as total from base where amount >= 0";
    await setCell(request, liveApp.baseURL, notebook.id, childCell, refreshedChildSQL);
    await page.waitForFunction(
      (expectedSQL) => {
        const monaco = (window as typeof window & { monaco?: any }).monaco;
        return monaco?.editor
          .getEditors?.()
          .some((editor: any) => editor.getModel?.()?.getValue().trim() === expectedSQL);
      },
      refreshedChildSQL,
      { timeout: 15000 },
    );
    await expect(downstreamCheckbox).toBeChecked();
    await expect(dialog.getByText("marts.child", { exact: true })).toBeVisible({ timeout: 15000 });
    const promoteRequest = page.waitForRequest(
      (request) =>
        new URL(request.url()).pathname.endsWith(`/cells/${baseCell}/promote`) &&
        request.method() === "POST",
      { timeout: 30000 },
    );
    const promoteResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname.endsWith(`/cells/${baseCell}/promote`) && response.ok(),
      { timeout: 30000 },
    );
    await dialog.getByRole("button", { name: "Promote", exact: true }).click();
    expect((await promoteRequest).postDataJSON()).toMatchObject({ include_downstream: true });
    const result = (await (await promoteResponse).json()) as { promoted_count: number };
    expect(result.promoted_count).toBe(2);

    // Both cells became pipeline assets in the same schema; the downstream
    // asset reads its upstream by the new name.
    const workspace = (await (await request.get(`${liveApp.baseURL}/api/workspace`)).json()) as {
      pipelines: Array<{ assets: Array<{ name: string; content: string; class?: string }> }>;
    };
    const assets = workspace.pipelines.flatMap((pipeline) => pipeline.assets);
    expect(assets.find((asset) => asset.name === "marts.base")?.class).toBe("pipeline");
    const child = assets.find((asset) => asset.name === "marts.child");
    expect(child?.class).toBe("pipeline");
    expect(child?.content).toContain("from marts.base");
  });

  test("notebooks appear in the index and a notebook cell never reaches the catalog", async ({
    liveApp,
    page,
  }) => {
    const notebook = await createNotebook(page.request, liveApp.baseURL, "Catalog Isolation");
    const cellId = await addCell(page.request, liveApp.baseURL, notebook.id, "scratch");
    await setCell(page.request, liveApp.baseURL, notebook.id, cellId, "select 1 as x");

    // Index lists the notebook.
    await page.goto(`${liveApp.baseURL}/notebooks`);
    await expect(page.getByText("Catalog Isolation").first()).toBeVisible({ timeout: 15000 });

    // The workspace payload tags the cell as a notebook asset and keeps it
    // out of the pipelines list.
    const workspace = (await (
      await page.request.get(`${liveApp.baseURL}/api/workspace`)
    ).json()) as {
      pipelines: Array<{ assets: Array<{ name: string; class?: string }> }>;
      notebooks?: Array<{ title: string; cells: Array<{ name: string; class?: string }> }>;
    };
    const pipelineAssetNames = workspace.pipelines.flatMap((p) => p.assets.map((a) => a.name));
    expect(pipelineAssetNames).not.toContain("scratch");
    const isolation = workspace.notebooks?.find((nb) => nb.title === "Catalog Isolation");
    expect(isolation).toBeTruthy();
    expect(isolation!.cells[0].class).toBe("notebook");
  });
});

test.describe("notebook warehouse snapshots live", () => {
  test.use({ fixtureName: "notebook-postgres-workspace" });

  test("joins complete typed snapshots from two named Postgres sources", async ({
    liveApp,
    livePostgres,
    page,
  }) => {
    test.skip(!livePostgres, "Postgres via docker is required for the notebook transfer flow.");
    test.setTimeout(timeoutForRetry(test.info(), 240000, 120000));

    const notebook = await createNotebook(page.request, liveApp.baseURL, "Postgres Sources");
    const prepareResponse = await page.request.post(
      `${liveApp.baseURL}/api/notebooks/${notebook.id}/changes/prepare`,
      {
        data: {
          base_revision: notebook.revision,
          operations: [
            {
              kind: "cell.create",
              language: "sql",
              connection: "postgres-orders",
              snapshot_mode: "full",
              content: "select order_id, order_total from analytics.orders order by order_id",
              position: "end",
            },
            {
              kind: "cell.create",
              language: "sql",
              connection: "postgres-customers",
              snapshot_mode: "full",
              content:
                "select customer_id, customer_name from analytics.customers order by customer_id",
              position: "end",
            },
          ],
        },
      },
    );
    expect(prepareResponse.ok()).toBe(true);
    const plan = (await prepareResponse.json()) as {
      can_apply: boolean;
      blocking_problems?: string[];
      change_set: Record<string, unknown>;
    };
    expect(plan.can_apply, plan.blocking_problems?.join("; ")).toBe(true);
    const applyResponse = await page.request.post(
      `${liveApp.baseURL}/api/notebooks/${notebook.id}/changes/apply`,
      { data: plan.change_set },
    );
    expect(applyResponse.ok()).toBe(true);
    const configured = ((await applyResponse.json()) as NotebookEnvelope).notebook;
    const orders = configured.cells.find((cell) => cell.connection === "postgres-orders");
    const customers = configured.cells.find((cell) => cell.connection === "postgres-customers");
    expect(orders?.type).toBe("pg.sql");
    expect(customers?.type).toBe("pg.sql");

    const joinedCell = await addCell(page.request, liveApp.baseURL, notebook.id, "joined_sources");
    await setCell(
      page.request,
      liveApp.baseURL,
      notebook.id,
      joinedCell,
      `select o.order_id, o.order_total, c.customer_name
from ${orders!.name} o
join ${customers!.name} c on c.customer_id = o.order_id
order by o.order_id`,
    );
    const runResponse = await page.request.post(
      `${liveApp.baseURL}/api/notebooks/${notebook.id}/run`,
      { data: { cells: [joinedCell] }, timeout: 210000 },
    );
    if (!runResponse.ok()) {
      throw new Error(`Notebook run failed: ${await runResponse.text()}`);
    }
    const payload = (await runResponse.json()) as {
      results: Array<{
        cell_id: string;
        status: string;
        rows: unknown[][];
        column_types?: string[];
        snapshot?: {
          connection: string;
          environment: string;
          row_count: number;
          byte_count: number;
          complete: boolean;
          sampled: boolean;
        };
      }>;
    };
    expect(payload.results).toHaveLength(3);
    const ordersResult = payload.results.find((result) => result.cell_id === orders!.cell_id)!;
    const customersResult = payload.results.find(
      (result) => result.cell_id === customers!.cell_id,
    )!;
    for (const [result, connection] of [
      [ordersResult, "postgres-orders"],
      [customersResult, "postgres-customers"],
    ] as const) {
      expect(result.status).toBe("ok");
      expect(result.snapshot).toMatchObject({
        connection,
        environment: "default",
        row_count: 2,
        complete: true,
        sampled: false,
      });
      expect(result.snapshot!.byte_count).toBeGreaterThan(0);
    }
    expect(ordersResult.column_types?.[0]).toMatch(/INTEGER|BIGINT/i);
    expect(ordersResult.column_types?.[1]).not.toMatch(/VARCHAR/i);
    expect(customersResult.column_types).toEqual(
      expect.arrayContaining([expect.stringMatching(/INTEGER|BIGINT/i), "VARCHAR"]),
    );
    const joined = payload.results.at(-1)!;
    expect(joined).toMatchObject({ cell_id: joinedCell, status: "ok" });
    expect(joined.rows.map((row) => [Number(row[0]), Number(row[1]), row[2]])).toEqual([
      [1, 10.5, "Ada"],
      [2, 22, "Grace"],
    ]);
  });
});
