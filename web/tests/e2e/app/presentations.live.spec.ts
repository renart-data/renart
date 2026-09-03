import { expect, type Locator, type Page } from "@playwright/test";

import { liveTest as test } from "../live-app-fixture";

const analyticsPipelineId = Buffer.from("analytics").toString("base64url");

type PresentationEnvelope = {
  document: {
    artifact: {
      id: string;
      workspace_id: string;
      revision: string;
      kind: string;
      title: string;
      datasets?: Array<{
        id: string;
        asset?: string;
        connection?: string;
        query?: string;
        columns?: Array<{ name: string; type?: string }>;
        resolved_columns?: Array<{ name: string; type?: string }>;
      }>;
      filters?: Array<{ id: string; type: string }>;
      visualizations?: Array<{
        id: string;
        dataset: string;
        definition?: { type?: string };
        filter_bindings?: Array<{ filter: string; column: string; operator: string }>;
      }>;
      layout?: Array<{ visualization: string; x?: number; y?: number; width?: number }>;
      sections?: Array<{
        id: string;
        title?: string;
        markdown?: string;
        visualization?: string;
        page_break?: boolean;
      }>;
      problems?: Array<{ code: string; message: string }>;
    };
    content: string;
  };
};

async function dragWithDataTransfer(page: Page, source: Locator, target: Locator) {
  const dataTransfer = await page.evaluateHandle(() => new DataTransfer());
  try {
    await source.dispatchEvent("dragstart", { dataTransfer });
    await target.dispatchEvent("dragenter", { dataTransfer });
    await target.dispatchEvent("dragover", { dataTransfer });
    await target.dispatchEvent("drop", { dataTransfer });
    await source.dispatchEvent("dragend", { dataTransfer });
  } finally {
    await dataTransfer.dispose();
  }
}

function builderToolsDialog(page: Page, kind: "dashboard" | "report") {
  return page.getByRole("dialog", {
    name:
      kind === "dashboard"
        ? /^(Builder tools|Dashboards navigation)$/
        : /^(Report outline|Reports navigation)$/,
  });
}

function tallDashboardDefinition() {
  const visualizations = Array.from(
    { length: 12 },
    (_, index) => `  - id: rows_${index + 1}
    dataset: values
    definition:
      version: 1
      type: table
      title: Result ${index + 1}`,
  ).join("\n");
  const layout = Array.from(
    { length: 12 },
    (_, index) => `  - visualization: rows_${index + 1}
    x: 0
    y: ${index * 4}
    width: 12
    height: 4`,
  ).join("\n");
  return `version: 1
id: tall_dashboard
title: Tall dashboard
datasets:
  values:
    connection: duckdb-default
    query: |
      SELECT 1 AS value
    columns:
      - name: value
        type: integer
visualizations:
${visualizations}
layout:
${layout}
`;
}

function tallReportDefinition() {
  const sections = Array.from(
    { length: 30 },
    (_, index) => `  - id: section_${index + 1}
    title: Section ${index + 1}
    markdown: This is a deliberately tall report section used to verify internal scrolling.`,
  ).join("\n");
  return `version: 1
id: tall_report
title: Tall report
sections:
${sections}
`;
}

test.describe("app presentations live", () => {
  test.use({ fixtureName: "basic-workspace" });

  test("keeps tall dashboard and report builders scrollable", async ({ liveApp, page }) => {
    for (const kind of ["dashboard", "report"] as const) {
      const createResponse = await page.request.post(`${liveApp.baseURL}/api/presentations`, {
        data: { kind, title: `Tall ${kind}` },
      });
      expect(createResponse.ok()).toBe(true);
      const created = (await createResponse.json()) as PresentationEnvelope;
      const presentationId = created.document.artifact.workspace_id;
      const definition = kind === "dashboard" ? tallDashboardDefinition() : tallReportDefinition();
      const updateResponse = await page.request.put(
        `${liveApp.baseURL}/api/presentations/${presentationId}`,
        {
          data: {
            expected_revision: created.document.artifact.revision,
            content: definition,
          },
        },
      );
      expect(updateResponse.ok()).toBe(true);

      await page.goto(
        `${liveApp.baseURL}/${kind === "dashboard" ? "dashboards" : "reports"}/${presentationId}`,
      );
      const viewport = page
        .getByTestId("presentation-builder-scroll")
        .locator('[data-slot="scroll-area-viewport"]');
      await expect(viewport).toBeVisible({ timeout: 15000 });
      await expect
        .poll(() =>
          viewport.evaluate((element) => ({
            clientHeight: element.clientHeight,
            scrollHeight: element.scrollHeight,
          })),
        )
        .toMatchObject({ clientHeight: expect.any(Number), scrollHeight: expect.any(Number) });
      await expect
        .poll(() =>
          viewport.evaluate((element) => element.scrollHeight > element.clientHeight + 200),
        )
        .toBe(true);
      await viewport.evaluate((element) => {
        element.scrollTop = element.scrollHeight;
        element.dispatchEvent(new Event("scroll", { bubbles: true }));
      });
      await expect.poll(() => viewport.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
      await expect(page.getByRole("button", { name: "Save", exact: true })).toBeVisible();
    }
  });

  test("creates and visually edits a Git-native dashboard", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/dashboards`);
    await expect(page.getByRole("heading", { name: "Dashboards", level: 1 })).toBeVisible();

    await page.getByRole("button", { name: "New dashboard" }).first().click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Title").fill("Sales overview");
    const createResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/presentations") &&
        response.request().method() === "POST" &&
        response.ok(),
    );
    await dialog.getByRole("button", { name: "Create" }).click();
    const created = (await (await createResponse).json()) as PresentationEnvelope;
    await expect(page).toHaveURL(
      new RegExp(`/dashboards/${created.document.artifact.workspace_id}$`),
    );

    const narrowBuilder = (page.viewportSize()?.width ?? 1280) < 1280;
    if (narrowBuilder) {
      await page.getByRole("button", { name: "Open builder tools" }).click();
      await page
        .getByRole("dialog", { name: /^(Builder tools|Dashboards navigation)$/ })
        .getByRole("button", { name: "Add dataset", exact: true })
        .click();
      const inspector = page.getByRole("dialog", { name: "Inspector" });
      await expect(inspector.getByLabel("Dataset ID")).toHaveValue("dataset");
      await expectInspectorToFit(page);
      await inspector.getByRole("button", { name: "Close" }).click();
      await page.getByRole("button", { name: "Open builder tools" }).click();
      const builderTools = builderToolsDialog(page, "dashboard");
      await builderTools.getByRole("tab", { name: "Add", exact: true }).click();
      await builderTools.getByRole("button", { name: "Add visualization", exact: true }).click();
    } else {
      await page.getByRole("button", { name: "Add dataset", exact: true }).first().click();
      await expect(page.getByLabel("Dataset ID")).toHaveValue("dataset");
      await expectInspectorToFit(page);
      await page.getByRole("button", { name: "Add visualization", exact: true }).first().click();
    }
    await page
      .getByRole("dialog")
      .getByRole("button", { name: /Data table/ })
      .click();
    const visualizationInspector = narrowBuilder
      ? page.getByRole("dialog", { name: "Inspector" })
      : page;
    await expect(visualizationInspector.getByLabel("Visualization ID")).toHaveValue("data_table");
    await expectInspectorToFit(page);
    if (narrowBuilder) await visualizationInspector.getByRole("button", { name: "Close" }).click();
    const dashboardVisualization = page.getByTestId("dashboard-visualization-data_table");
    await dashboardVisualization.focus();
    await expect(dashboardVisualization).toBeFocused();
    if (narrowBuilder) await page.getByRole("button", { name: "Open inspector" }).click();
    await visualizationInspector.getByRole("combobox", { name: "Visualization width" }).click();
    await page.getByRole("option", { name: "6/12 columns" }).click();
    await visualizationInspector.getByLabel("Height").fill("5");
    const moveRight = visualizationInspector.getByRole("button", { name: "Move right" });
    await moveRight.focus();
    await moveRight.press("Enter");
    if (narrowBuilder) await visualizationInspector.getByRole("button", { name: "Close" }).click();
    if (narrowBuilder) {
      await page.getByRole("button", { name: "Open builder tools" }).click();
      const builderTools = builderToolsDialog(page, "dashboard");
      await builderTools.getByRole("tab", { name: "Add" }).click();
      await builderTools.getByRole("button", { name: "Add control", exact: true }).click();
    } else {
      await page
        .getByLabel("Add")
        .getByRole("button", { name: "Add control", exact: true })
        .click();
    }
    const filterDialog = page.getByRole("dialog", { name: "Add a control" });
    await filterDialog.getByRole("button", { name: "Add control", exact: true }).click();
    const filterInspector = narrowBuilder ? page.getByRole("dialog", { name: "Inspector" }) : page;
    const filterID = await filterInspector.getByLabel("Control ID").inputValue();
    expect(filterID).not.toBe("");
    const invalidPreview = page.waitForResponse(
      (response) => response.url().includes("/preview") && response.ok(),
    );
    await filterInspector.getByLabel("Control ID").fill("Invalid");
    await invalidPreview;
    if (narrowBuilder) await filterInspector.getByRole("button", { name: "Close" }).click();

    await page.getByRole("button", { name: /Review \d+ definition findings?/ }).click();
    await page.getByRole("menuitem").filter({ hasText: "lowercase letter" }).click();
    const focusedFilterInspector = narrowBuilder
      ? page.getByRole("dialog", { name: "Inspector" })
      : page;
    await expect(focusedFilterInspector.getByLabel("Control ID")).toBeFocused();
    await focusedFilterInspector.getByLabel("Control ID").fill(filterID);
    if (narrowBuilder) {
      await focusedFilterInspector.getByRole("button", { name: "Close" }).click();
    }
    await page.getByLabel("Presentation title").fill("Sales performance");

    const saveResponse = page.waitForResponse(
      (response) =>
        response
          .url()
          .endsWith(`/api/presentations/${created.document.artifact.workspace_id}/definition`) &&
        response.request().method() === "PUT" &&
        response.ok(),
    );
    await page.getByRole("button", { name: "Save", exact: true }).click();
    const saved = (await (await saveResponse).json()) as PresentationEnvelope;
    expect(saved.document.artifact.title).toBe("Sales performance");
    expect(saved.document.artifact.datasets).toHaveLength(1);
    expect(saved.document.artifact.visualizations).toEqual([
      expect.objectContaining({
        id: "data_table",
        dataset: "dataset",
        filter_bindings: [expect.objectContaining({ filter: filterID, operator: "equals" })],
      }),
    ]);
    expect(saved.document.artifact.filters).toEqual([expect.objectContaining({ id: filterID })]);
    expect(saved.document.artifact.layout).toEqual([
      expect.objectContaining({ visualization: "data_table", x: 1, width: 6, height: 5 }),
    ]);

    await page.getByLabel("Presentation title").fill("Keyboard-saved title");
    const shortcutSaveResponse = page.waitForResponse(
      (response) =>
        response
          .url()
          .endsWith(`/api/presentations/${created.document.artifact.workspace_id}/definition`) &&
        response.request().method() === "PUT" &&
        response.ok(),
    );
    await page.keyboard.press("Control+s");
    const shortcutSaved = (await (await shortcutSaveResponse).json()) as PresentationEnvelope;
    expect(shortcutSaved.document.artifact.title).toBe("Keyboard-saved title");

    await page.getByLabel("Presentation title").fill("Unsaved title");
    await page.getByRole("link", { name: "Back to dashboards", exact: true }).click();
    const leaveDialog = page.getByRole("dialog", { name: "Leave this unsaved draft?" });
    await expect(leaveDialog).toBeVisible();
    await leaveDialog.getByRole("button", { name: "Keep editing" }).click();
    await expect(page).toHaveURL(
      new RegExp(`/dashboards/${created.document.artifact.workspace_id}$`),
    );
    await page.getByRole("button", { name: "Discard", exact: true }).click();
    await expect(page.getByLabel("Presentation title")).toHaveValue("Keyboard-saved title");

    await page.getByRole("tab", { name: "Definition" }).click();
    await expect(page.getByLabel("Presentation definition YAML")).toBeVisible();
    await expect
      .poll(() =>
        page.evaluate(() => {
          const monaco = (window as typeof window & { monaco?: any }).monaco;
          const model = monaco?.editor
            .getModels?.()
            .find((candidate: any) => candidate.uri.toString().includes("/presentation/"));
          return model?.getValue?.() ?? "";
        }),
      )
      .toContain("title: Keyboard-saved title");
  });

  test("query datasets use connection-aware SQL intelligence", async ({ liveApp, page }) => {
    const createResponse = await page.request.post(`${liveApp.baseURL}/api/presentations`, {
      data: { kind: "dashboard", title: "Query intelligence" },
    });
    expect(createResponse.ok()).toBeTruthy();
    const created = (await createResponse.json()) as PresentationEnvelope;
    const presentationId = created.document.artifact.workspace_id;

    await page.goto(`${liveApp.baseURL}/dashboards/${presentationId}`);
    await expect(page.getByLabel("Presentation title")).toHaveValue("Query intelligence", {
      timeout: 15000,
    });

    const narrowBuilder = (page.viewportSize()?.width ?? 1280) < 1280;
    if (narrowBuilder) {
      await page.getByRole("button", { name: "Open builder tools" }).click();
      await page
        .getByRole("dialog", { name: /^(Builder tools|Dashboards navigation)$/ })
        .getByRole("button", { name: "Add dataset", exact: true })
        .click();
    } else {
      await page.getByRole("button", { name: "Add dataset", exact: true }).first().click();
    }
    const inspector = narrowBuilder ? page.getByRole("dialog", { name: "Inspector" }) : page;
    await inspector.getByRole("combobox", { name: "Dataset source" }).click();
    await page.getByRole("option", { name: "Query" }).click();
    await expect(inspector.getByTestId("presentation-query-editor")).toBeVisible({
      timeout: 15000,
    });

    const intelligenceReady = page.waitForResponse(
      (response) => {
        if (!response.url().endsWith("/api/sql/lsp/diagnostics")) return false;
        const request = response.request();
        if (request.method() !== "POST") return false;
        const payload = request.postDataJSON() as {
          connection?: string;
          document_context?: string;
        };
        return (
          payload.connection === "duckdb-default" &&
          payload.document_context === "presentation_query"
        );
      },
      { timeout: 15000 },
    );
    await setPresentationQuery(page, "select o.\nfrom analytics.orders o", "select o.");
    expect((await intelligenceReady).ok()).toBe(true);
    const completionResponse = page.waitForResponse(
      (response) => {
        if (!response.url().endsWith("/api/sql/lsp/completions")) return false;
        const payload = response.request().postDataJSON() as {
          connection?: string;
          document_context?: string;
        };
        return (
          payload.connection === "duckdb-default" &&
          payload.document_context === "presentation_query"
        );
      },
      { timeout: 15000 },
    );
    await triggerPresentationQueryCompletion(page);
    expect((await completionResponse).ok()).toBe(true);
    await expect(
      page.locator(".suggest-widget .monaco-list-row").filter({ hasText: "order_id" }).first(),
    ).toBeVisible({ timeout: 15000 });

    await dismissPresentationQueryCompletion(page);
    await setPresentationQuery(page, "select *\nfrom analytics.orders", "analytics.orders");
    if (narrowBuilder) {
      await inspector.getByRole("button", { name: "Close" }).click();
      await expect(page.getByRole("dialog", { name: "Inspector" })).toBeHidden();
    }
    const saveResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/presentations/${presentationId}/definition`) &&
        response.request().method() === "PUT" &&
        response.ok(),
    );
    await page.getByRole("button", { name: "Save", exact: true }).click();
    const saved = (await (await saveResponse).json()) as PresentationEnvelope;
    const queryDataset = saved.document.artifact.datasets?.[0];
    expect(queryDataset?.columns ?? []).toEqual([]);
    expect(queryDataset?.resolved_columns).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ name: "order_id" }),
        expect.objectContaining({ name: "customer_id" }),
        expect.objectContaining({ name: "total_amount" }),
      ]),
    );
    expect(
      saved.document.artifact.problems?.some(
        (problem) => problem.code === "presentation-dataset-schema-unresolved",
      ) ?? false,
    ).toBe(false);
  });

  test("drags chart previews into dashboard and report canvases", async ({ liveApp, page }) => {
    test.skip((page.viewportSize()?.width ?? 0) < 1280, "Drag authoring uses the desktop rail");

    const dashboardResponse = await page.request.post(`${liveApp.baseURL}/api/presentations`, {
      data: { kind: "dashboard", title: "Dragged dashboard" },
    });
    expect(dashboardResponse.ok()).toBe(true);
    const dashboard = (await dashboardResponse.json()) as PresentationEnvelope;
    await page.goto(`${liveApp.baseURL}/dashboards/${dashboard.document.artifact.workspace_id}`);
    await page.getByRole("button", { name: "Add dataset", exact: true }).first().click();
    await expect(page.getByLabel("Dataset ID")).toHaveValue("dataset");
    const chartPreview = page.waitForResponse(
      (response) =>
        response
          .url()
          .endsWith(`/api/presentations/${dashboard.document.artifact.workspace_id}/preview`) &&
        response.request().method() === "POST" &&
        response.ok(),
      { timeout: 15000 },
    );
    await page
      .getByLabel("Add")
      .getByRole("button", { name: "Bar", exact: true })
      .dragTo(page.getByTestId("dashboard-canvas"), {
        targetPosition: { x: 360, y: 180 },
      });
    await expect(page.getByLabel("Visualization ID")).toBeVisible();
    await chartPreview;
    const controlPreview = page.waitForResponse(
      (response) =>
        response
          .url()
          .endsWith(`/api/presentations/${dashboard.document.artifact.workspace_id}/preview`) &&
        response.request().method() === "POST" &&
        response.ok(),
      { timeout: 15000 },
    );
    await dragWithDataTransfer(
      page,
      page.getByLabel("Add").getByRole("button", { name: "Switch", exact: true }),
      page.getByTestId("presentation-control-strip"),
    );
    await expect(page.getByLabel("Control ID")).toHaveValue("filter");
    await controlPreview;

    const dashboardSave = page.waitForResponse(
      (response) =>
        response
          .url()
          .endsWith(`/api/presentations/${dashboard.document.artifact.workspace_id}/definition`) &&
        response.request().method() === "PUT" &&
        response.ok(),
    );
    await page.getByRole("button", { name: "Save", exact: true }).click();
    const savedDashboard = (await (await dashboardSave).json()) as PresentationEnvelope;
    expect(savedDashboard.document.artifact.visualizations).toEqual([
      expect.objectContaining({
        dataset: "dataset",
        definition: expect.objectContaining({ type: "bar" }),
      }),
    ]);
    expect(savedDashboard.document.artifact.layout?.[0]).toEqual(
      expect.objectContaining({ width: 6 }),
    );
    expect(savedDashboard.document.artifact.filters).toEqual([
      expect.objectContaining({ id: "filter", type: "boolean", default: false }),
    ]);

    const reportResponse = await page.request.post(`${liveApp.baseURL}/api/presentations`, {
      data: { kind: "report", title: "Dragged report" },
    });
    expect(reportResponse.ok()).toBe(true);
    const report = (await reportResponse.json()) as PresentationEnvelope;
    await page.goto(`${liveApp.baseURL}/reports/${report.document.artifact.workspace_id}`);
    await page.getByRole("tab", { name: "Data", exact: true }).click();
    await page.getByRole("button", { name: "Add dataset", exact: true }).click();
    await expect(page.getByLabel("Dataset ID")).toHaveValue("dataset");
    await page.getByRole("tab", { name: "Add", exact: true }).click();
    await page
      .getByLabel("Add")
      .getByRole("button", { name: "Area", exact: true })
      .dragTo(page.getByTestId("report-canvas"), {
        targetPosition: { x: 320, y: 280 },
      });
    await expect(page.getByLabel("Visualization ID")).toBeVisible();

    const reportSave = page.waitForResponse(
      (response) =>
        response
          .url()
          .endsWith(`/api/presentations/${report.document.artifact.workspace_id}/definition`) &&
        response.request().method() === "PUT" &&
        response.ok(),
    );
    await page.getByRole("button", { name: "Save", exact: true }).click();
    const savedReport = (await (await reportSave).json()) as PresentationEnvelope;
    expect(savedReport.document.artifact.visualizations).toEqual([
      expect.objectContaining({
        dataset: "dataset",
        definition: expect.objectContaining({ type: "area" }),
      }),
    ]);
    expect(savedReport.document.artifact.sections?.[0]?.visualization).toBe(
      savedReport.document.artifact.visualizations?.[0]?.id,
    );
  });

  test("renders typed URL filters and refreshes only affected visualizations", async ({
    liveApp,
    page,
  }) => {
    const createResponse = await page.request.post(`${liveApp.baseURL}/api/presentations`, {
      data: { kind: "dashboard", title: "Customer regions" },
    });
    expect(createResponse.ok()).toBeTruthy();
    const created = (await createResponse.json()) as PresentationEnvelope;
    const presentationId = created.document.artifact.workspace_id;
    const definition = `version: 1
id: customer_regions
title: Customer regions
datasets:
  customers:
    connection: duckdb-default
    query: |
      SELECT *
      FROM (VALUES ('eu', 'Ada'), ('us', 'Grace')) AS customers(region, customer_name)
    columns:
      - name: region
        type: varchar
      - name: customer_name
        type: varchar
filters:
  - id: region
    label: Region
    type: select
    default: eu
    options:
      values: [eu, us]
visualizations:
  - id: by_region
    dataset: customers
    definition:
      version: 1
      type: table
      title: Selected region
    filter_bindings:
      - filter: region
        column: region
        operator: equals
  - id: all_customers
    dataset: customers
    definition:
      version: 1
      type: table
      title: All customers
layout:
  - visualization: by_region
    width: 6
    height: 4
  - visualization: all_customers
    width: 6
    height: 4
`;
    const updateResponse = await page.request.put(
      `${liveApp.baseURL}/api/presentations/${presentationId}`,
      {
        data: {
          expected_revision: created.document.artifact.revision,
          content: definition,
        },
      },
    );
    expect(updateResponse.ok()).toBeTruthy();

    const runBodies: Array<{
      visualization_ids?: string[];
      filter_values?: Record<string, unknown>;
    }> = [];
    page.on("request", (request) => {
      if (
        request.url().endsWith(`/api/presentations/${presentationId}/run`) &&
        request.method() === "POST"
      ) {
        runBodies.push(request.postDataJSON());
      }
    });
    const initialRun = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/presentations/${presentationId}/run`) &&
        response.request().method() === "POST" &&
        response.ok(),
    );
    await page.goto(
      `${liveApp.baseURL}/dashboards/${presentationId}/view?filters=${encodeURIComponent(
        JSON.stringify({ region: "us" }),
      )}`,
    );
    await initialRun;

    const filtered = page.getByTestId("presentation-visualization-by_region");
    const unfiltered = page.getByTestId("presentation-visualization-all_customers");
    await expect(filtered).toContainText("Selected region");
    await expect(filtered).toContainText("Grace");
    await expect(filtered).not.toContainText("Ada");
    await expect(unfiltered).toContainText("Ada");
    await expect(unfiltered).toContainText("Grace");

    const affectedRun = page.waitForRequest((request) => {
      if (
        !request.url().endsWith(`/api/presentations/${presentationId}/run`) ||
        request.method() !== "POST"
      ) {
        return false;
      }
      const body = request.postDataJSON() as { visualization_ids?: string[] };
      return body.visualization_ids?.length === 1 && body.visualization_ids[0] === "by_region";
    });
    await page.getByRole("combobox", { name: "Region" }).click();
    await page.getByRole("option", { name: "eu", exact: true }).click();
    await affectedRun;
    await expect(filtered).toContainText("Ada");
    await expect(filtered).not.toContainText("Grace");
    await expect(unfiltered).toContainText("Grace");
    await expect.poll(() => new URL(page.url()).searchParams.has("filters")).toBe(false);
    await page.waitForTimeout(400);
    expect(runBodies).toEqual([
      expect.objectContaining({
        filter_values: { region: "us" },
        visualization_ids: ["by_region", "all_customers"],
      }),
      expect.objectContaining({
        filter_values: { region: "eu" },
        visualization_ids: ["by_region"],
      }),
    ]);
  });

  test("authors a narrative report on the document canvas", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/reports`);
    await page.getByRole("button", { name: "New report" }).first().click();
    const dialog = page.getByRole("dialog");
    await dialog.getByLabel("Title").fill("Weekly narrative");
    const createResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith("/api/presentations") &&
        response.request().method() === "POST" &&
        response.ok(),
    );
    await dialog.getByRole("button", { name: "Create" }).click();
    const created = (await (await createResponse).json()) as PresentationEnvelope;
    const presentationId = created.document.artifact.workspace_id;
    const narrowBuilder = (page.viewportSize()?.width ?? 1280) < 1280;

    await page.getByRole("button", { name: "Add text", exact: true }).last().click();
    await page.getByLabel("Section title").fill("Executive summary");
    const markdown = page.getByLabel("Section markdown");
    await expect(markdown).toHaveAttribute("contenteditable", "true");
    await expect(markdown).toHaveAttribute("spellcheck", "false");
    await markdown.focus();
    await expect(markdown).toHaveAttribute("spellcheck", "true");
    await page.getByLabel("Section title").focus();
    await expect(markdown).toHaveAttribute("spellcheck", "false");
    await markdown.fill("");
    await markdown.type("Revenue remained ");
    await markdown.press("Control+b");
    await markdown.type("healthy");
    await markdown.press("Control+b");
    await markdown.type(" while customer growth accelerated.");
    await markdown.hover();
    await page.getByRole("button", { name: "Edit Markdown source" }).click();
    await page
      .getByLabel("Markdown source")
      .fill(
        "Revenue remained **healthy** while customer growth accelerated.\n\n- New subscriptions\n- Renewals\n\n1. Review results\n2. Share the report",
      );

    if (narrowBuilder) {
      await page.getByRole("button", { name: "Open builder tools" }).click();
      const tools = builderToolsDialog(page, "report");
      await tools.getByRole("tab", { name: "Data" }).click();
      await tools.getByRole("button", { name: "Add dataset", exact: true }).click();
      const inspector = page.getByRole("dialog", { name: "Inspector" });
      await expect(inspector.getByLabel("Dataset ID")).toHaveValue("dataset");
      await inspector.getByRole("button", { name: "Close" }).click();
      await page.getByRole("button", { name: "Open builder tools" }).click();
      const addTools = builderToolsDialog(page, "report");
      await addTools.getByRole("tab", { name: "Add", exact: true }).click();
      await addTools.getByRole("button", { name: "Add visualization", exact: true }).click();
    } else {
      await page.getByRole("tab", { name: "Data" }).click();
      await page.getByRole("button", { name: "Add dataset", exact: true }).click();
      await expect(page.getByLabel("Dataset ID")).toHaveValue("dataset");
      await page.getByRole("tab", { name: "Add", exact: true }).click();
      await page.getByRole("button", { name: "Add visualization", exact: true }).click();
    }
    await page
      .getByRole("dialog")
      .getByRole("button", { name: /Data table/ })
      .click();
    if (narrowBuilder) {
      const inspector = page.getByRole("dialog", { name: "Inspector" });
      await expect(inspector.getByLabel("Visualization ID")).toHaveValue("data_table");
      await inspector.getByRole("button", { name: "Close" }).click();
    }
    const reportSection = page.getByRole("region", {
      name: "Report section Executive summary",
    });
    await reportSection.focus();
    await expect(reportSection).toBeFocused();
    if (narrowBuilder) {
      await page.getByRole("button", { name: "Open inspector" }).click();
      await expect(
        page.getByRole("dialog", { name: "Inspector" }).getByLabel("Section ID"),
      ).toHaveValue("text");
    } else {
      const moveDown = page.getByRole("button", { name: "Move section down" }).first();
      await moveDown.focus();
      await moveDown.press("Enter");
      const moveUp = page.getByRole("button", { name: "Move section up" }).last();
      await moveUp.focus();
      await moveUp.press("Enter");
    }
    if (!narrowBuilder) {
      await page.getByRole("tab", { name: "Outline" }).click();
      await page.getByRole("button", { name: "Executive summary 1", exact: true }).click();
    }
    const sectionInspector = narrowBuilder ? page.getByRole("dialog", { name: "Inspector" }) : page;
    await sectionInspector
      .getByRole("checkbox", { name: "Start a new printed page after this block" })
      .click();
    if (narrowBuilder) await sectionInspector.getByRole("button", { name: "Close" }).click();

    const saveResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/presentations/${presentationId}/definition`) &&
        response.request().method() === "PUT" &&
        response.ok(),
    );
    await page.getByRole("button", { name: "Save", exact: true }).click();
    const saved = (await (await saveResponse).json()) as PresentationEnvelope;
    expect(saved.document.artifact.sections).toEqual([
      expect.objectContaining({
        id: "text",
        title: "Executive summary",
        markdown:
          "Revenue remained **healthy** while customer growth accelerated.\n\n- New subscriptions\n- Renewals\n\n1. Review results\n2. Share the report",
        page_break: true,
      }),
      expect.objectContaining({ id: "data_table", visualization: "data_table" }),
    ]);
    expect(saved.document.artifact.visualizations).toEqual([
      expect.objectContaining({ id: "data_table", dataset: "dataset" }),
    ]);

    await page.reload();
    await expect(page.getByRole("heading", { name: "Executive summary" })).toBeVisible();
    await expect(page.getByText("Revenue remained healthy")).toBeVisible();

    await page.goto(`${liveApp.baseURL}/reports/${presentationId}/view`);
    const report = page.getByRole("article");
    await expect(report.getByText("New subscriptions")).toBeVisible({ timeout: 15000 });
    await expect(report.locator("ul > li")).toHaveCount(2);
    await expect(report.locator("ol > li")).toHaveCount(2);
    await expect
      .poll(() =>
        report.locator("ul").evaluate((element) => getComputedStyle(element).listStyleType),
      )
      .toBe("disc");
    await expect
      .poll(() =>
        report.locator("ol").evaluate((element) => getComputedStyle(element).listStyleType),
      )
      .toBe("decimal");
  });

  test("blocks only the consumed pipeline until presentation errors are repaired", async ({
    liveApp,
    page,
  }) => {
    const createResponse = await page.request.post(`${liveApp.baseURL}/api/presentations`, {
      data: { kind: "dashboard", title: "Customer quality" },
    });
    expect(createResponse.ok()).toBeTruthy();
    const created = (await createResponse.json()) as PresentationEnvelope;
    const presentationId = created.document.artifact.workspace_id;
    const definition = (field: string) => `version: 1
id: customer_quality
title: Customer quality
datasets:
  customers:
    asset: analytics.customers
    columns:
      - name: customer_id
        type: integer
      - name: customer_name
        type: varchar
visualizations:
  - id: customer_count
    dataset: customers
    definition:
      version: 1
      type: kpi
      value:
        field: ${field}
layout:
  - visualization: customer_count
`;
    const invalidResponse = await page.request.put(
      `${liveApp.baseURL}/api/presentations/${presentationId}`,
      {
        data: {
          expected_revision: created.document.artifact.revision,
          content: definition("missing_customer"),
        },
      },
    );
    expect(invalidResponse.ok()).toBeTruthy();
    const invalid = (await invalidResponse.json()) as PresentationEnvelope;

    type CheckReport = {
      presentations?: Array<{ id: string; findings: Array<{ code: string }> }>;
    };
    await expect
      .poll(async () => {
        const response = await page.request.get(
          `${liveApp.baseURL}/api/pipelines/${analyticsPipelineId}/type-check`,
        );
        if (!response.ok()) return "";
        const report = (await response.json()) as CheckReport;
        return report.presentations?.[0]?.findings.map((finding) => finding.code).join(",") ?? "";
      })
      .toContain("visualization-field-missing");

    const deploymentRequest = {
      purpose: "deployment",
      environment: "default",
      source: { kind: "working_tree" },
      selection: { mode: "all" },
    };
    await expect
      .poll(async () => {
        const response = await page.request.post(
          `${liveApp.baseURL}/api/pipelines/${analyticsPipelineId}/plan`,
          { data: deploymentRequest },
        );
        if (!response.ok()) return "";
        const plan = (await response.json()) as {
          readiness: { blockers: Array<{ code: string }> };
        };
        return plan.readiness.blockers.map((blocker) => blocker.code).join(",");
      })
      .toContain("presentation_check_error");

    const repairedResponse = await page.request.put(
      `${liveApp.baseURL}/api/presentations/${presentationId}`,
      {
        data: {
          expected_revision: invalid.document.artifact.revision,
          content: definition("customer_id"),
        },
      },
    );
    expect(repairedResponse.ok()).toBeTruthy();

    await expect
      .poll(async () => {
        const response = await page.request.post(
          `${liveApp.baseURL}/api/pipelines/${analyticsPipelineId}/plan`,
          { data: deploymentRequest },
        );
        if (!response.ok()) return "request-error";
        const plan = (await response.json()) as {
          status: string;
          readiness: { blockers: Array<{ code: string }> };
        };
        const presentationBlocked = plan.readiness.blockers.some(
          (blocker) => blocker.code === "presentation_check_error",
        );
        return presentationBlocked ? "presentation-blocked" : plan.status;
      })
      .toBe("ready");
  });
});

async function expectInspectorToFit(page: import("@playwright/test").Page) {
  const inspector = page.getByTestId("presentation-inspector");
  await expect(inspector).toBeVisible();
  await expect
    .poll(() =>
      inspector.evaluate((element) => ({
        clientWidth: element.clientWidth,
        scrollWidth: element.scrollWidth,
      })),
    )
    .toMatchObject({
      clientWidth: expect.any(Number),
      scrollWidth: expect.any(Number),
    });
  const dimensions = await inspector.evaluate((element) => ({
    clientWidth: element.clientWidth,
    scrollWidth: element.scrollWidth,
  }));
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.clientWidth + 1);
}

async function setPresentationQuery(
  page: import("@playwright/test").Page,
  content: string,
  cursorAfter: string,
) {
  await page.waitForFunction(
    () => {
      const monaco = (window as typeof window & { monaco?: any }).monaco;
      return monaco?.editor
        .getEditors?.()
        .some(
          (editor: any) =>
            editor.getOption?.(monaco.editor.EditorOption.ariaLabel) === "Dataset SQL query" &&
            editor.getDomNode?.()?.offsetParent !== null,
        );
    },
    undefined,
    { timeout: 15000 },
  );
  const editorElement = page.getByTestId("presentation-query-editor").locator(".monaco-editor");
  await editorElement.click();
  await page.keyboard.press("ControlOrMeta+A");
  await page.keyboard.insertText(content);
  await expect
    .poll(() =>
      page.evaluate(() => {
        const monaco = (window as typeof window & { monaco?: any }).monaco;
        const editor = monaco?.editor
          .getEditors?.()
          .find(
            (candidate: any) =>
              candidate.getOption?.(monaco.editor.EditorOption.ariaLabel) === "Dataset SQL query" &&
              candidate.getDomNode?.()?.offsetParent !== null,
          );
        return editor?.getValue?.() ?? "";
      }),
    )
    .toBe(content);
  await page.evaluate(
    ({ cursorAfter }) => {
      const monaco = (window as typeof window & { monaco?: any }).monaco;
      const editor = monaco?.editor
        .getEditors?.()
        .find(
          (candidate: any) =>
            candidate.getOption?.(monaco.editor.EditorOption.ariaLabel) === "Dataset SQL query" &&
            candidate.getDomNode?.()?.offsetParent !== null,
        );
      const model = editor?.getModel?.();
      if (!model || !editor) throw new Error("Presentation query editor is not ready");
      const content = model.getValue();
      const offset = content.indexOf(cursorAfter);
      if (offset < 0) throw new Error(`cursor text ${cursorAfter} was not found`);
      editor.focus();
      editor.setPosition(model.getPositionAt(offset + cursorAfter.length));
    },
    { cursorAfter },
  );
}

async function triggerPresentationQueryCompletion(page: import("@playwright/test").Page) {
  await page.evaluate(() => {
    const monaco = (window as typeof window & { monaco?: any }).monaco;
    const editor = monaco?.editor
      .getEditors?.()
      .find(
        (candidate: any) =>
          candidate.getOption?.(monaco.editor.EditorOption.ariaLabel) === "Dataset SQL query" &&
          candidate.getDomNode?.()?.offsetParent !== null,
      );
    if (!editor) throw new Error("Presentation query editor is not ready");
    editor.focus();
    editor.trigger("playwright", "editor.action.triggerSuggest", {});
  });
}

async function dismissPresentationQueryCompletion(page: import("@playwright/test").Page) {
  await page.evaluate(() => {
    const monaco = (window as typeof window & { monaco?: any }).monaco;
    const editor = monaco?.editor
      .getEditors?.()
      .find(
        (candidate: any) =>
          candidate.getOption?.(monaco.editor.EditorOption.ariaLabel) === "Dataset SQL query" &&
          candidate.getDomNode?.()?.offsetParent !== null,
      );
    if (!editor) throw new Error("Presentation query editor is not ready");
    editor.trigger("playwright", "hideSuggestWidget", {});
  });
  await expect(page.locator(".suggest-widget")).toBeHidden();
}
