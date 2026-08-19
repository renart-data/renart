import { expect } from "@playwright/test";

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
      datasets?: Array<{ id: string; asset?: string }>;
      visualizations?: Array<{ id: string; dataset: string }>;
      layout?: Array<{ visualization: string; width?: number }>;
    };
    content: string;
  };
};

test.describe("app presentations live", () => {
  test.use({ fixtureName: "basic-workspace" });

  test("creates and visually edits a Git-native dashboard", async ({ liveApp, page }) => {
    await page.goto(`${liveApp.baseURL}/dashboards`);
    await expect(page.getByRole("heading", { name: "Dashboards" })).toBeVisible();

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

    await page.getByRole("button", { name: "Add dataset", exact: true }).first().click();
    await expect(page.getByLabel("Dataset ID")).toHaveValue("dataset");
    await page.getByRole("button", { name: "Add visualization", exact: true }).first().click();
    await expect(page.getByLabel("Visualization ID")).toHaveValue("visualization");
    await page.getByLabel("Title").first().fill("Sales performance");

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
      expect.objectContaining({ id: "visualization", dataset: "dataset" }),
    ]);
    expect(saved.document.artifact.layout).toEqual([
      expect.objectContaining({ visualization: "visualization", width: 6 }),
    ]);

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
      .toContain("title: Sales performance");
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
