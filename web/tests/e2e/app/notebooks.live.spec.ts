import { expect, type APIRequestContext, type Page } from "@playwright/test";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { basename, join } from "node:path";

import { liveTest as test, timeoutForRetry } from "../live-app-fixture";

type NotebookEnvelope = {
  notebook: {
    id: string;
    path: string;
    cells: Array<{
      id: string;
      cell_id: string;
      name: string;
      content: string;
      content_revision?: string;
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

    await cell.getByRole("button", { name: "format_queue", exact: true }).click();
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

    const viewport = page.locator('[data-slot="scroll-area-viewport"]');
    const addSQLCell = page.getByRole("button", { name: "SQL cell" });
    await addSQLCell.scrollIntoViewIfNeeded();

    let releaseRequest = () => {};
    const requestGate = new Promise<void>((resolve) => {
      releaseRequest = resolve;
    });
    let markRequestStarted = () => {};
    const requestStarted = new Promise<void>((resolve) => {
      markRequestStarted = resolve;
    });
    const createRoute = `**/notebooks/${notebook.id}/cells`;
    await page.route(createRoute, async (route) => {
      if (route.request().method() !== "POST") {
        await route.continue();
        return;
      }
      markRequestStarted();
      await requestGate;
      await route.continue();
    });

    const createResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/notebooks/${notebook.id}/cells`) &&
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
      await expect(addSQLCell).toBeDisabled();
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
      await page.unroute(createRoute);
    }
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

  test("rename is reference-rewriting and the chart type writes a @viz directive", async ({
    liveApp,
    page,
  }) => {
    const notebook = await createNotebook(page.request, liveApp.baseURL, "Viz And Rename");
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
    await page.getByRole("button", { name: "Run all" }).click();
    await page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === `/api/notebooks/${notebook.id}/run` &&
        response.request().method() === "POST" &&
        response.ok(),
      {
        timeout: 30000,
      },
    );
    // Result table renders the column.
    await expect(page.getByText("revenue", { exact: true }).first()).toBeVisible({
      timeout: 15000,
    });

    // Switching to a bar chart writes a @viz directive into the chart cell.
    const updateSeen = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/notebooks/${notebook.id}/cells/${chartCell}`) &&
        response.request().method() === "PUT" &&
        response.ok(),
      { timeout: 15000 },
    );
    await page.getByRole("button", { name: "bar", exact: true }).last().click();
    await updateSeen;
    // The directive landed in the cell file.
    const afterViz = (await (
      await page.request.get(`${liveApp.baseURL}/api/notebooks/${notebook.id}`)
    ).json()) as NotebookEnvelope;
    const chartContent = afterViz.notebook.cells.find((cell) => cell.cell_id === chartCell)!;
    expect(chartContent.content).toContain("@viz(bar");

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
    expect(final.notebook.cells.find((cell) => cell.cell_id === baseCell)!.name).toBe("revenue");
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
    const baseCard = page
      .locator('[data-slot="delimited-card"]')
      .filter({ has: page.getByRole("button", { name: "base", exact: true }) });
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
    const promoteRequest = page.waitForRequest(
      (request) =>
        request.url().includes(`/cells/${baseCell}/promote`) && request.method() === "POST",
      { timeout: 30000 },
    );
    const promoteResponse = page.waitForResponse(
      (response) => response.url().includes(`/cells/${baseCell}/promote`) && response.ok(),
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
