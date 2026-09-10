import { expect, type APIRequestContext } from "@playwright/test";

import { liveTest as test } from "../live-app-fixture";

async function createNotebook(request: APIRequestContext, baseURL: string, title: string) {
  const response = await request.post(`${baseURL}/api/notebooks`, { data: { title } });
  expect(response.ok()).toBe(true);
  return (await response.json()).notebook as { id: string };
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
  const notebook = (await response.json()).notebook as {
    cells: Array<{ cell_id: string; name: string }>;
  };
  return notebook.cells.find((cell) => cell.name === name)!.cell_id;
}

async function setPy(
  request: APIRequestContext,
  baseURL: string,
  notebookId: string,
  cellId: string,
  body: string,
) {
  expect(
    (
      await request.put(`${baseURL}/api/notebooks/${notebookId}/cells/${cellId}`, {
        data: { content: `${body}\n` },
      })
    ).ok(),
  ).toBe(true);
}

test.describe("notebook python logs", () => {
  test.use({ fixtureName: "configured-workspace" });

  test("python stdout is captured and shown as collapsible output", async ({ liveApp, page }) => {
    const { request } = page;
    const notebook = await createNotebook(request, liveApp.baseURL, "Py Logs");
    const cell = await addPythonCell(request, liveApp.baseURL, notebook.id, "printer");
    await setPy(
      request,
      liveApp.baseURL,
      notebook.id,
      cell,
      "import pandas as pd\n\n\ndef materialize():\n    print('notebook stdout marker')\n    return pd.DataFrame({'x': [1, 2]})",
    );

    await page.goto(`${liveApp.baseURL}/notebooks/${notebook.id}`);
    await expect(page.getByText("Py Logs").first()).toBeVisible({ timeout: 15000 });

    // First run installs the venv via uv, so allow generous time.
    const runResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === `/api/notebooks/${notebook.id}/run` &&
        response.request().method() === "POST" &&
        response.ok(),
      { timeout: 180000 },
    );
    await page.getByRole("button", { name: "Run all" }).click();
    const payload = (await (await runResponse).json()) as {
      results: Array<{ name: string; status: string; logs?: string; error?: string }>;
    };
    const result = payload.results.find((entry) => entry.name === "printer")!;
    expect(result.status, `run error: ${result.error ?? ""}`).toBe("ok");
    expect(result.logs ?? "").toContain("notebook stdout marker");

    // Stdout is contextual cell UI: an unselected cell contributes no visible
    // disclosure or one-sided spacing to the document. Selecting the editor
    // fades the disclosure in, still collapsed by default for a successful run.
    const cellBlock = page.locator(`[data-notebook-cell-id="${cell}"]`);
    const disclosure = cellBlock.getByTestId("notebook-cell-logs-disclosure");
    const toggle = cellBlock.getByRole("button", { name: "Output" });
    await expect(toggle).toBeHidden();
    expect(await disclosure.evaluate((element) => element.getBoundingClientRect().height)).toBe(0);

    await cellBlock.locator('[data-slot="notebook-cell-editor-shell"]').click();
    await expect(toggle).toBeVisible({ timeout: 15000 });
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(page.getByTestId("cell-logs")).toHaveCount(0);
    expect(
      await cellBlock.getByTestId("notebook-cell-logs-spacing").evaluate((element) => {
        const style = getComputedStyle(element);
        return style.paddingTop === style.paddingBottom;
      }),
    ).toBe(true);

    // Expanding reveals the captured stdout.
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(page.getByTestId("cell-logs")).toContainText("notebook stdout marker");
  });
});
