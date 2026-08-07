import { expect, type APIRequestContext } from "@playwright/test";
import { appendFile, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { liveTest as test, type LiveApp } from "../live-app-fixture";

type ScheduleResponse = {
  status: "ok" | "error";
  schedules: Array<{ pipeline_id: string; pipeline_name: string }>;
  archived?: Array<{ environment: string; archived_reason?: string }>;
};

type TriggerResponse = {
  status: "ok" | "error";
  run: { id: string };
};

type RunDetailResponse = {
  status: "ok" | "error";
  run: {
    id: string;
    pipeline_id: string;
    status: string;
    pipeline: string;
    environment: string;
    error?: string;
    win_start?: string;
    win_end?: string;
    snapshot_version_id?: string;
    execution_context_resolved?: boolean;
  };
  logs?: Array<{ at: string; line: string }>;
  steps?: Array<{ asset: string; status: string }>;
  units?: Array<{ position: number; asset_name: string; status: string }>;
};

const analyticsPipelineId = Buffer.from("analytics").toString("base64url");

test.describe("app scheduler pages live", () => {
  test.use({ fixtureName: "basic-workspace" });

  test("shows configured schedules", async ({ liveApp, page }) => {
    await page.route("**/api/env-schedules", async (route) => {
      const response = await route.fetch();
      const body = (await response.json()) as {
        schedules?: Array<Record<string, unknown>>;
      };
      for (const schedule of body.schedules ?? []) {
        if (schedule.pipeline_name === "analytics") {
          schedule.deferred_occurrence = {
            interval_start: "2026-07-18T08:00:00Z",
            interval_end: "2026-07-18T09:00:00Z",
            attempt_count: 0,
          };
        }
      }
      await route.fulfill({ response, json: body });
    });
    await page.goto(`${liveApp.baseURL}/schedules`);

    await expect(page.getByRole("heading", { name: "Schedules" })).toBeVisible();
    await expect(page.getByText("analytics", { exact: true })).toBeVisible({ timeout: 15000 });
    await expect(page.getByText("daily", { exact: true })).toBeVisible();

    await page.getByRole("button", { name: "New schedule" }).click();
    const newScheduleDialog = page.getByRole("dialog", { name: "New schedule" });
    await expect(newScheduleDialog).toBeVisible();
    await expect(newScheduleDialog.getByTestId("new-schedule-scroll-area")).toBeVisible();
    const newScheduleBounds = await newScheduleDialog.boundingBox();
    const viewport = page.viewportSize();
    expect(newScheduleBounds).not.toBeNull();
    expect(viewport).not.toBeNull();
    expect(newScheduleBounds!.height).toBeLessThanOrEqual(viewport!.height - 16);
    if (viewport!.width >= 640) {
      expect(newScheduleBounds!.width).toBeGreaterThanOrEqual(640);
    }
    await newScheduleDialog.getByRole("button", { name: "Close" }).click();

    const scheduleRow = page.getByTestId("schedule-row").filter({ hasText: "analytics" }).first();
    const metadata = scheduleRow.getByTestId("schedule-metadata");
    await expect(metadata).toContainText("Schedule");
    await expect(metadata).toContainText("Timezone");
    await expect(metadata).toContainText("Last run");
    await expect(metadata).toContainText("Deployment");
    await expect(scheduleRow.getByTestId("schedule-run-window-context")).toContainText(
      "Skip missed runs",
    );
    await expect(metadata).toContainText("Pinned pipeline schedule");
    expect(
      await metadata.locator("[data-schedule-meta-value]").evaluateAll((elements) =>
        elements.every((element) => {
          const style = getComputedStyle(element);
          return style.whiteSpace === "normal" && style.textOverflow !== "ellipsis";
        }),
      ),
    ).toBe(true);
    await expect(scheduleRow.getByText("Needs deployment", { exact: true })).toBeVisible();
    const waitingBadge = scheduleRow.getByText("Run waiting", { exact: true });
    await expect(waitingBadge).toBeVisible();
    await waitingBadge.focus();
    await expect(page.getByRole("tooltip")).toContainText("durably retained");
    const actions = scheduleRow.getByTestId("schedule-actions");
    await expect(actions.getByRole("button", { name: "Review deployment" })).toBeVisible();
    await expect(actions.getByRole("button", { name: "Run pinned" })).toBeDisabled();
    await actions.getByRole("button", { name: /More actions for analytics/ }).click();
    await expect(page.getByRole("menuitem", { name: "Edit schedule" })).toBeVisible();
    await expect(page.getByRole("menuitem", { name: "Archive schedule" })).toBeVisible();
    await page.keyboard.press("Escape");
  });

  test("navigates deployment history and the actual run timeline as schedule subroutes", async ({
    liveApp,
    page,
    request,
  }) => {
    const deployed = await request.post(
      `${liveApp.baseURL}/api/pipelines/${analyticsPipelineId}/deploy`,
      { data: {} },
    );
    expect(deployed.ok()).toBe(true);

    await page.goto(`${liveApp.baseURL}/schedules`);
    await page.getByRole("link", { name: "Deployments", exact: true }).click();
    await expect(page).toHaveURL(/\/schedules\/deployments$/);
    await expect(page.getByRole("heading", { name: "Deployments" })).toBeVisible();
    await expect(page.getByText("analytics", { exact: true })).toBeVisible();
    await expect(page.getByText(/Deployment #\d+/).first()).toBeVisible();
    await expect(page.getByRole("button", { name: "Review", exact: true }).first()).toBeVisible();

    const now = Date.now();
    await page.route("**/api/runs?*", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          status: "ok",
          total: 1,
          limit: 500,
          offset: 0,
          runs: [
            {
              id: "schedule-subroute-run",
              pipeline_id: analyticsPipelineId,
              pipeline: "analytics",
              environment: "default",
              trigger: "schedule",
              status: "success",
              started_at: new Date(now - 60_000).toISOString(),
              finished_at: new Date(now - 30_000).toISOString(),
              execution_context_resolved: true,
            },
          ],
        }),
      });
    });
    await page.getByRole("link", { name: "Run timeline", exact: true }).click();
    await expect(page).toHaveURL(/\/schedules\/timeline$/);
    await expect(page.getByRole("heading", { name: "Run timeline" })).toBeVisible();
    await expect(page.getByRole("link", { name: "analytics", exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: /Open analytics run/ })).toBeVisible();
  });

  test("deploys and pins a schedule that has no previous deployment", async ({
    liveApp,
    page,
    request,
  }) => {
    await page.goto(`${liveApp.baseURL}/schedules`);
    const scheduleRow = page.getByTestId("schedule-row").filter({ hasText: "analytics" }).first();
    await expect(scheduleRow.getByText("Needs deployment", { exact: true })).toBeVisible({
      timeout: 15000,
    });

    await scheduleRow.getByRole("button", { name: "Review deployment" }).click();
    const planSheet = page.getByTestId("pipeline-plan-sheet");
    await expect(planSheet.getByRole("heading", { name: "Review deployment" })).toBeVisible();

    const deployResponsePromise = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/pipelines/${analyticsPipelineId}/deploy`) &&
        response.request().method() === "POST",
    );
    await planSheet.getByRole("button", { name: /^Deploy \d+ assets?$/ }).click();
    const deployResponse = await deployResponsePromise;
    expect(deployResponse.ok()).toBe(true);
    const deployedVersion = ((await deployResponse.json()) as { snapshot: { version_id: string } })
      .snapshot.version_id;

    await expect(planSheet.getByText(/not using this deployment/)).toBeVisible();
    await planSheet.getByRole("checkbox", { name: "default" }).check();
    const promotionRequestPromise = page.waitForRequest(
      (request) =>
        request.url().endsWith(`/api/pipelines/${analyticsPipelineId}/env-schedules/promote`) &&
        request.method() === "POST",
    );
    const promotionResponsePromise = page.waitForResponse(
      (response) =>
        response.url().endsWith(`/api/pipelines/${analyticsPipelineId}/env-schedules/promote`) &&
        response.request().method() === "POST",
    );
    await planSheet.getByRole("button", { name: "Update 1 schedule" }).click();

    expect((await promotionRequestPromise).postDataJSON()).toEqual({
      snapshot_version_id: deployedVersion,
      schedules: [
        {
          environment: "default",
          expected_snapshot_version_id: "",
        },
      ],
    });
    expect((await promotionResponsePromise).ok()).toBe(true);

    await expect
      .poll(
        async () => {
          const response = await request.get(`${liveApp.baseURL}/api/env-schedules`);
          if (!response.ok()) return "";
          const body = (await response.json()) as {
            schedules: Array<{ environment: string; snapshot_version_id?: string }>;
          };
          return body.schedules.find((schedule) => schedule.environment === "default")
            ?.snapshot_version_id;
        },
        { timeout: 15000 },
      )
      .toBe(deployedVersion);
  });

  test("surfaces run list and run-detail transport failures", async ({ liveApp, page }) => {
    await page.route("**/api/runs**", async (route) => {
      const pathname = new URL(route.request().url()).pathname;
      if (pathname === "/api/runs" || pathname === "/api/runs/unavailable-run") {
        await route.fulfill({
          status: 503,
          contentType: "application/json",
          body: JSON.stringify({ error: { message: "scheduler store unavailable" } }),
        });
        return;
      }
      await route.fallback();
    });

    await page.goto(`${liveApp.baseURL}/runs`);
    await expect(page.getByRole("alert")).toContainText("Runs could not be refreshed");
    await expect(page.getByRole("button", { name: "Retry" })).toBeVisible();

    await page.goto(`${liveApp.baseURL}/runs/unavailable-run`);
    await expect(page.getByRole("alert")).toContainText("Run details unavailable");
    await expect(page.getByText("Loading run details", { exact: true })).toHaveCount(0);
  });

  test("uses millisecond timeline ticks for sub-second runs", async ({ liveApp, page }) => {
    const run = {
      id: "sub-second-run",
      pipeline_id: analyticsPipelineId,
      pipeline: "analytics",
      environment: "default",
      trigger: "manual",
      status: "success",
      started_at: "2026-07-22T08:00:00.000Z",
      finished_at: "2026-07-22T08:00:00.900Z",
      execution_context_resolved: true,
    };
    await page.route("**/api/runs/sub-second-run", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          status: "ok",
          run,
          logs: [],
          steps: [
            {
              run_id: run.id,
              asset: "analytics.fast_asset",
              status: "success",
              started_at: run.started_at,
              finished_at: run.finished_at,
            },
          ],
        }),
      });
    });

    await page.goto(`${liveApp.baseURL}/runs/${run.id}`);
    await expect(page.getByTestId("run-timeline-axis").locator(":scope > div")).toHaveText([
      "0ms",
      "250ms",
      "500ms",
      "750ms",
      "1000ms",
    ]);
    await expect(page.getByTestId("run-timeline-grid")).toHaveAttribute("data-row-height", "28");
    await expect(page.getByTestId("run-timeline-scroll")).toHaveCount(0);
  });

  test("fits nineteen timeline rows before scrolling at twenty", async ({ liveApp, page }) => {
    await page.route("**/api/runs/timeline-density-*", async (route) => {
      const runId = route.request().url().split("/").pop() ?? "";
      const count = runId.endsWith("-20") ? 20 : 19;
      const startedAt = new Date("2026-07-22T08:00:00.000Z");
      const finishedAt = new Date(startedAt.getTime() + 1000);
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          status: "ok",
          run: {
            id: runId,
            pipeline_id: analyticsPipelineId,
            pipeline: "analytics",
            environment: "default",
            trigger: "manual",
            status: "success",
            started_at: startedAt.toISOString(),
            finished_at: finishedAt.toISOString(),
            execution_context_resolved: true,
          },
          logs: [],
          steps: Array.from({ length: count }, (_, index) => ({
            run_id: runId,
            asset: `analytics.asset_${String(index + 1).padStart(2, "0")}`,
            status: "success",
            started_at: new Date(startedAt.getTime() + index * 10).toISOString(),
            finished_at: new Date(startedAt.getTime() + 500 + index * 10).toISOString(),
          })),
        }),
      });
    });

    await page.goto(`${liveApp.baseURL}/runs/timeline-density-19`);
    await expect(page.getByTestId("run-timeline-grid")).toHaveAttribute("data-row-height", "12");
    await expect(page.getByTestId("run-timeline-track")).toHaveCount(19);
    await expect(page.getByTestId("run-timeline-scroll")).toHaveCount(0);

    await page.goto(`${liveApp.baseURL}/runs/timeline-density-20`);
    await expect(page.getByTestId("run-timeline-grid")).toHaveAttribute("data-row-height", "16");
    await expect(page.getByTestId("run-timeline-track")).toHaveCount(20);
    const scroll = page.getByTestId("run-timeline-scroll");
    await expect(scroll).toBeVisible();
    const timelineViewport = scroll.locator(':scope > [data-slot="scroll-area-viewport"]');
    expect(
      await timelineViewport.evaluate((viewport) => viewport.scrollHeight > viewport.clientHeight),
    ).toBe(true);
    await expect
      .poll(() =>
        timelineViewport.evaluate(
          (viewport) => viewport.scrollTop + viewport.clientHeight >= viewport.scrollHeight - 1,
        ),
      )
      .toBe(true);

    const eventsViewport = page
      .locator('[data-slot="tabs-content"][data-state="active"]')
      .locator('[data-slot="scroll-area-viewport"]');
    const firstEventRow = page
      .getByTestId("run-event-row")
      .filter({ hasText: "analytics.asset_01" })
      .first();
    await firstEventRow.locator("td").last().click();
    await expect
      .poll(() => timelineViewport.evaluate((viewport) => viewport.scrollTop))
      .toBeLessThan(24);

    const firstEventScrollTop = await eventsViewport.evaluate((viewport) => viewport.scrollTop);
    await page.getByRole("tab", { name: "Output" }).click();
    await page
      .locator('[data-testid="run-timeline-track"][data-asset="analytics.asset_20"]')
      .click();
    await expect(page.getByRole("tab", { name: "Events" })).toHaveAttribute("data-state", "active");
    await expect
      .poll(() => eventsViewport.evaluate((viewport) => viewport.scrollTop))
      .toBeGreaterThan(firstEventScrollTop + 50);
  });

  test("renders follower ownership as read-only", async ({ liveApp, page }) => {
    await page.route("**/api/env-schedules", async (route) => {
      if (route.request().method() !== "GET") {
        await route.fallback();
        return;
      }
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          status: "ok",
          scheduler: {
            state: "follower",
            message: "Schedules are managed by another Renart process.",
          },
          schedules: [],
          archived: [],
        }),
      });
    });

    await page.goto(`${liveApp.baseURL}/schedules`);
    await expect(
      page.getByText("Schedules are managed by another Renart process", { exact: true }),
    ).toBeVisible();
    await expect(page.getByRole("button", { name: "New schedule" })).toBeDisabled();
    await expect(page.getByText("Read-only", { exact: true })).toBeVisible();
  });

  test("shows and updates a schedule pinned to an older deployment", async ({
    liveApp,
    page,
    request,
  }) => {
    await appendFile(
      join(liveApp.workspaceDir, "analytics/pipeline.yml"),
      "\nvariables:\n  region:\n    type: string\n    default: eu\n",
      "utf8",
    );
    const pinResponse = await request.put(
      `${liveApp.baseURL}/api/pipelines/${analyticsPipelineId}/env-schedules/default`,
      {
        data: {
          cron: "0 0 * * *",
          timezone: "UTC",
          catchup_policy: "skip",
          vars: { region: "private-schedule-value" },
          deploy_now: true,
        },
      },
    );
    expect(pinResponse.ok()).toBe(true);
    const pinBody = (await pinResponse.json()) as {
      schedule: { snapshot_version_id: string; variable_names?: string[] };
    };
    const pinnedVersion = pinBody.schedule.snapshot_version_id;
    expect(pinnedVersion).toBeTruthy();
    expect(pinBody.schedule.variable_names).toEqual(["region"]);
    expect(JSON.stringify(pinBody)).not.toContain("private-schedule-value");
    const declaration = await readFile(join(liveApp.workspaceDir, ".renart/schedules.yml"), "utf8");
    expect(declaration).toContain("version: 1");
    expect(declaration).toContain("cron:");
    expect(declaration).toContain("0 0 * * *");
    expect(declaration).not.toContain("snapshot_version_id");
    expect(declaration).not.toContain(pinnedVersion);

    await appendFile(
      join(liveApp.workspaceDir, "analytics/assets/analytics/orders.sql"),
      "\n-- deployment mismatch regression\n",
      "utf8",
    );
    const deployResponse = await request.post(
      `${liveApp.baseURL}/api/pipelines/${analyticsPipelineId}/deploy`,
    );
    expect(deployResponse.ok()).toBe(true);
    const latestVersion = ((await deployResponse.json()) as { snapshot: { version_id: string } })
      .snapshot.version_id;
    expect(latestVersion).not.toBe(pinnedVersion);

    await page.goto(`${liveApp.baseURL}/schedules`);
    const olderBadge = page.getByText("Older deployment", { exact: true });
    await expect(olderBadge).toBeVisible({ timeout: 15000 });
    await olderBadge.hover();
    await expect(page.getByText(/Data freshness is tracked separately/)).toBeVisible();

    const scheduleRow = page.getByTestId("schedule-row").filter({ hasText: "analytics" }).first();
    const editRequest = page.waitForRequest(
      (request) =>
        request.url().endsWith(`/api/pipelines/${analyticsPipelineId}/env-schedules/default`) &&
        request.method() === "PUT",
    );
    await scheduleRow.getByRole("button", { name: /More actions for analytics/ }).click();
    await page.getByRole("menuitem", { name: "Edit schedule" }).click();
    const editDialog = page.getByRole("dialog", { name: "Edit schedule" });
    await expect(editDialog).toBeVisible();
    await expect(editDialog.getByTestId("edit-schedule-scroll-area")).toBeVisible();
    const editDialogBounds = await editDialog.boundingBox();
    const viewport = page.viewportSize();
    expect(editDialogBounds).not.toBeNull();
    expect(viewport).not.toBeNull();
    expect(editDialogBounds!.height).toBeLessThanOrEqual(viewport!.height - 16);
    if (viewport!.width >= 640) {
      expect(editDialogBounds!.width).toBeGreaterThanOrEqual(600);
    }
    const editScrollViewport = editDialog
      .getByTestId("edit-schedule-scroll-area")
      .locator(':scope > [data-slot="scroll-area-viewport"]');
    const editScrollMetrics = await editScrollViewport.evaluate((element) => ({
      clientWidth: element.clientWidth,
      overflowX: getComputedStyle(element).overflowX,
      scrollWidth: element.scrollWidth,
      horizontalScrollbars: element.parentElement?.querySelectorAll(
        '[data-slot="scroll-area-scrollbar"][data-orientation="horizontal"]',
      ).length,
    }));
    expect(editScrollMetrics.overflowX).toBe("hidden");
    expect(editScrollMetrics.horizontalScrollbars).toBe(0);
    expect(editScrollMetrics.scrollWidth).toBeLessThanOrEqual(editScrollMetrics.clientWidth + 1);
    await expect(editDialog.getByLabel("Pipeline")).toHaveValue("analytics");
    await expect(editDialog.getByLabel("Environment")).toHaveValue("default");
    await editDialog.getByLabel("Cron").fill("15 1 * * *");
    await editDialog.getByLabel("Timezone").fill("Europe/Berlin");
    await editDialog.getByLabel("Catch-up policy").click();
    await page.getByRole("option", { name: "Run once to catch up" }).click();
    await editDialog.getByRole("button", { name: "Save changes" }).click();

    const editBody = (await editRequest).postDataJSON() as Record<string, unknown>;
    expect(editBody).toMatchObject({
      cron: "15 1 * * *",
      timezone: "Europe/Berlin",
      catchup_policy: "run_once",
      preserve_snapshot: true,
      preserve_variables: true,
    });
    expect(editBody).not.toHaveProperty("snapshot_version_id");
    expect(editBody).not.toHaveProperty("vars");
    await expect(editDialog).toBeHidden();
    await expect(scheduleRow.getByTestId("schedule-cadence")).toContainText("15 1 * * *");

    const editedScheduleResponse = await request.get(`${liveApp.baseURL}/api/env-schedules`);
    expect(editedScheduleResponse.ok()).toBe(true);
    const editedScheduleBody = (await editedScheduleResponse.json()) as {
      schedules: Array<{
        environment: string;
        snapshot_version_id?: string;
        variable_names?: string[];
      }>;
    };
    const editedSchedule = editedScheduleBody.schedules.find(
      (schedule) => schedule.environment === "default",
    );
    expect(editedSchedule?.snapshot_version_id).toBe(pinnedVersion);
    expect(editedSchedule?.variable_names).toEqual(["region"]);
    expect(JSON.stringify(editedScheduleBody)).not.toContain("private-schedule-value");
    const editedDeclaration = await readFile(
      join(liveApp.workspaceDir, ".renart/schedules.yml"),
      "utf8",
    );
    expect(editedDeclaration).toContain("private-schedule-value");
    expect(editedDeclaration).toContain("Europe/Berlin");

    const pinnedRunRequest = page.waitForRequest(
      (request) =>
        request.url().endsWith(`/api/pipelines/${analyticsPipelineId}/env-schedules/default/run`) &&
        request.method() === "POST",
    );
    await page.route(
      `**/api/pipelines/${analyticsPipelineId}/env-schedules/default/run`,
      async (route) => {
        await route.fulfill({
          status: 202,
          contentType: "application/json",
          body: JSON.stringify({
            status: "ok",
            run: {
              id: "pinned-ui-check",
              pipeline_id: analyticsPipelineId,
              environment: "default",
              trigger: "manual",
              status: "queued",
            },
          }),
        });
      },
    );
    const overridesBadge = scheduleRow.getByText("Overrides", { exact: true });
    await expect(overridesBadge).toBeVisible();
    await overridesBadge.focus();
    await expect(page.getByRole("tooltip", { name: /Applied from this schedule/ })).toContainText(
      "region",
    );
    await scheduleRow.getByRole("button", { name: /Run pinned/ }).click();
    expect((await pinnedRunRequest).postData()).toBeNull();
    await page.unroute(`**/api/pipelines/${analyticsPipelineId}/env-schedules/default/run`);

    const updateResponse = page.waitForResponse(
      (response) =>
        response.url().includes(`/api/pipelines/${analyticsPipelineId}/env-schedules/promote`) &&
        response.request().method() === "POST" &&
        response.ok(),
      { timeout: 15000 },
    );
    await scheduleRow.getByRole("button", { name: "Review deployment" }).click();
    const planSheet = page.getByTestId("pipeline-plan-sheet");
    await expect(planSheet.getByRole("heading", { name: "Review deployment" })).toBeVisible();
    await expect(planSheet.getByRole("tablist")).toHaveCount(0);
    await expect(planSheet.getByRole("heading", { name: "Source changes" })).toBeVisible();
    await expect(planSheet.getByRole("button", { name: /Deployment contents/ })).toBeVisible();
    await expect(planSheet.getByRole("button", { name: /Plan identities/ })).toBeVisible();
    await expect(planSheet.getByRole("button", { name: /Execution details/ })).toBeVisible();
    await expect(planSheet.getByRole("button", { name: /Schedules/ })).toBeVisible();
    const reviewViewport = planSheet
      .getByTestId("pipeline-plan-scroll")
      .locator(':scope > [data-slot="scroll-area-viewport"]');
    await expect(reviewViewport.getByRole("heading", { name: "Review deployment" })).toBeVisible();
    await expect(reviewViewport.getByRole("heading", { name: "Source changes" })).toBeVisible();
    await planSheet.getByRole("button", { name: /^Deploy \d+ assets?$/ }).click();
    await expect(page.getByText(/not using this deployment/)).toBeVisible({
      timeout: 15000,
    });
    await page.getByRole("checkbox", { name: "default" }).check();
    await page.getByRole("button", { name: "Update 1 schedule" }).click();
    await updateResponse;

    await expect
      .poll(
        async () => {
          const response = await request.get(`${liveApp.baseURL}/api/env-schedules`);
          if (!response.ok()) return "";
          const body = (await response.json()) as {
            schedules: Array<{ environment: string; snapshot_version_id?: string }>;
          };
          return body.schedules.find((schedule) => schedule.environment === "default")
            ?.snapshot_version_id;
        },
        { timeout: 15000 },
      )
      .toBe(latestVersion);
    await expect(olderBadge).toBeHidden({ timeout: 15000 });

    await rm(join(liveApp.workspaceDir, ".renart/schedules.yml"));
    await expect
      .poll(
        async () => {
          const response = await request.get(`${liveApp.baseURL}/api/env-schedules`);
          if (!response.ok()) return "";
          const body = (await response.json()) as ScheduleResponse;
          return body.archived?.find((schedule) => schedule.environment === "default")
            ?.archived_reason;
        },
        { timeout: 15000 },
      )
      .toBe("declaration_missing");
  });

  test("runs a pinned deployment whose parallel units are resolved at runtime", async ({
    liveApp,
    request,
  }) => {
    await appendFile(
      join(liveApp.workspaceDir, "analytics/pipeline.yml"),
      "\nmax_active_steps: 2\n",
      "utf8",
    );
    const pinResponse = await request.put(
      `${liveApp.baseURL}/api/pipelines/${analyticsPipelineId}/env-schedules/default`,
      {
        data: {
          cron: "0 0 * * *",
          timezone: "UTC",
          catchup_policy: "skip",
          deploy_now: true,
        },
      },
    );
    expect(pinResponse.ok()).toBe(true);
    const pinnedVersion = (
      (await pinResponse.json()) as { schedule: { snapshot_version_id: string } }
    ).schedule.snapshot_version_id;

    const triggerResponse = await request.post(
      `${liveApp.baseURL}/api/pipelines/${analyticsPipelineId}/env-schedules/default/run`,
    );
    expect(triggerResponse.status()).toBe(202);
    const runID = ((await triggerResponse.json()) as TriggerResponse).run.id;

    await expect
      .poll(
        async () => {
          const response = await request.get(`${liveApp.baseURL}/api/runs/${runID}`);
          if (!response.ok()) return `http:${response.status()}`;
          const detail = (await response.json()) as RunDetailResponse;
          if (detail.run.status === "failed") {
            return `failed:${detail.run.error ?? "unknown error"}`;
          }
          return detail.run.status;
        },
        { timeout: 60000 },
      )
      .toBe("success");

    const detailResponse = await request.get(`${liveApp.baseURL}/api/runs/${runID}`);
    expect(detailResponse.ok()).toBe(true);
    const detail = (await detailResponse.json()) as RunDetailResponse;
    expect(detail.run.snapshot_version_id).toBe(pinnedVersion);
    expect(detail.units?.length).toBeGreaterThan(1);
    expect(detail.units?.every((unit) => unit.status === "success")).toBe(true);
  });

  test("blocks a corrupt latest pin and offers repair", async ({ liveApp, page, request }) => {
    const pinResponse = await request.put(
      `${liveApp.baseURL}/api/pipelines/${analyticsPipelineId}/env-schedules/default`,
      {
        data: {
          cron: "0 0 * * *",
          timezone: "UTC",
          catchup_policy: "skip",
          deploy_now: true,
        },
      },
    );
    expect(pinResponse.ok()).toBe(true);
    const pinnedVersion = (
      (await pinResponse.json()) as { schedule: { snapshot_version_id: string } }
    ).schedule.snapshot_version_id;

    await page.route("**/api/pipelines/**/deploy/status", async (route) => {
      const pathname = new URL(route.request().url()).pathname;
      if (pathname === `/api/pipelines/${analyticsPipelineId}/deploy/status`) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            has_snapshot: true,
            executable: false,
            integrity_error: "snapshot blob is missing",
            in_sync: false,
            version_id: pinnedVersion,
            snapshot_count: 1,
          }),
        });
        return;
      }
      await route.fallback();
    });

    await page.goto(`${liveApp.baseURL}/schedules`);
    await expect(page.getByText("Deployment needs repair", { exact: true })).toBeVisible({
      timeout: 15000,
    });
    await expect(page.getByRole("button", { name: "Review repair" })).toBeEnabled();
    const scheduleRow = page.getByTestId("schedule-row").filter({ hasText: "analytics" }).first();
    await expect(scheduleRow.getByRole("button", { name: /Run pinned/ })).toBeDisabled();
    await expect(scheduleRow.getByTestId("schedule-metadata")).toContainText(
      "Pinned pipeline schedule",
    );
  });

  test("shows triggered runs in the runs list", async ({ liveApp, page, request }) => {
    const runId = await triggerPipelineRun(liveApp, request);

    await page.goto(`${liveApp.baseURL}/runs`);

    await expect(page.getByRole("heading", { name: "Runs" })).toBeVisible();
    await expect(page.getByText(runId, { exact: true })).toBeVisible({ timeout: 15000 });
    await expect(page.getByText("analytics", { exact: true }).first()).toBeVisible();
  });

  test("runs a non-replayable original with visibly labeled current settings", async ({
    liveApp,
    page,
    request,
  }) => {
    const runId = await triggerPipelineRun(liveApp, request, {
      start: "2026-07-15T00:00:00Z",
      end: "2026-07-16T00:00:00Z",
      sensor_mode: "skip",
    });
    const original = await waitForRunDetail(
      liveApp,
      request,
      runId,
      (detail) => detail.run.execution_context_resolved === true,
    );
    expect(original.run.execution_context_resolved).toBe(true);
    expect(original.run.win_start).toBeTruthy();
    expect(original.run.win_end).toBeTruthy();
    const acceptedRun = {
      id: "default-mode-rerun",
      pipeline_id: analyticsPipelineId,
      pipeline: original.run.pipeline,
      environment: original.run.environment,
      trigger: "manual",
      status: "queued",
    };

    await page.route(`**/api/pipelines/${analyticsPipelineId}/trigger`, async (route) => {
      await route.fulfill({
        status: 202,
        contentType: "application/json",
        body: JSON.stringify({ status: "ok", run: acceptedRun }),
      });
    });
    await page.route("**/api/runs/default-mode-rerun", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ status: "ok", run: acceptedRun, logs: [], steps: [] }),
      });
    });

    await page.goto(`${liveApp.baseURL}/runs/${runId}`);
    const context = page.getByTestId("run-again-context");
    await expect(context).toBeVisible();
    await expect(context).toContainText("Run source current saved workspace");
    await expect(context).toContainText("Environment default");
    await expect(context).toContainText("Recorded window");
    await expect(context).not.toContainText("no recorded window");
    await expect(context).toContainText("Mode current settings");

    const rerunRequest = page.waitForRequest(
      (candidate) =>
        candidate.url().endsWith(`/api/pipelines/${analyticsPipelineId}/trigger`) &&
        candidate.method() === "POST",
    );
    await page.getByRole("button", { name: "Run again with current settings" }).click();
    expect((await rerunRequest).postDataJSON()).toEqual({
      source: "working_tree",
      environment: original.run.environment,
      start: original.run.win_start,
      end: original.run.win_end,
    });

    await expect(page).toHaveURL(new RegExp("/runs/default-mode-rerun$"));
    await expect(page.getByRole("heading", { name: "Run default-mode-rerun" })).toBeVisible();
  });

  test("re-executes an exact retained plan through the run-owned endpoint", async ({
    liveApp,
    page,
  }) => {
    const originalRun = {
      id: "exact-original",
      pipeline_id: analyticsPipelineId,
      pipeline: "analytics",
      environment: "default",
      trigger: "schedule",
      status: "success",
      win_start: "2026-07-15T00:00:00Z",
      win_end: "2026-07-16T00:00:00Z",
      snapshot_version_id: "deployment-id",
      snapshot_ordinal: 4,
      execution_context_resolved: true,
    };
    const acceptedRun = {
      ...originalRun,
      id: "exact-reexecution",
      trigger: "manual",
      status: "queued",
      execution_context_resolved: false,
    };
    await page.route("**/api/runs/exact-original", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          status: "ok",
          run: originalRun,
          logs: [],
          steps: [],
          units: [],
          reexecution: { mode: "exact", selection: "all", execution_units: 2 },
        }),
      });
    });
    await page.route("**/api/runs/exact-original/reexecute", async (route) => {
      await route.fulfill({
        status: 202,
        contentType: "application/json",
        body: JSON.stringify({ status: "ok", run: acceptedRun }),
      });
    });
    await page.route("**/api/runs/exact-reexecution", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          status: "ok",
          run: acceptedRun,
          logs: [],
          steps: [],
          reexecution: { mode: "current_settings", reason: "The run has not finished." },
        }),
      });
    });

    await page.goto(`${liveApp.baseURL}/runs/exact-original`);
    await expect(page.getByTestId("run-again-context")).toContainText(
      "Mode exact all plan · 2 units",
    );
    const request = page.waitForRequest(
      (candidate) =>
        candidate.url().endsWith("/api/runs/exact-original/reexecute") &&
        candidate.method() === "POST",
    );
    await page.getByRole("button", { name: "Re-execute exact plan" }).click();
    expect((await request).postDataJSON()).toEqual({});
    await expect(page).toHaveURL(new RegExp("/runs/exact-reexecution$"));
  });

  test("omits unresolved legacy environment and window from a rerun", async ({ liveApp, page }) => {
    await page.setViewportSize({ width: 900, height: 800 });
    const unresolvedRun = {
      id: "legacy-unresolved-context",
      pipeline_id: analyticsPipelineId,
      pipeline: "analytics",
      environment: "request-only-environment",
      trigger: "manual",
      status: "failed",
      win_start: "2026-07-15T00:00:00Z",
      win_end: "2026-07-16T00:00:00Z",
      execution_context_resolved: false,
    };
    const acceptedRun = {
      id: "legacy-default-rerun",
      pipeline_id: analyticsPipelineId,
      pipeline: "analytics",
      environment: "",
      trigger: "manual",
      status: "queued",
      execution_context_resolved: false,
    };
    await page.route("**/api/runs/legacy-unresolved-context", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ status: "ok", run: unresolvedRun, logs: [], steps: [] }),
      });
    });
    await page.route(`**/api/pipelines/${analyticsPipelineId}/trigger`, async (route) => {
      await route.fulfill({
        status: 202,
        contentType: "application/json",
        body: JSON.stringify({ status: "ok", run: acceptedRun }),
      });
    });
    await page.route("**/api/runs/legacy-default-rerun", async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ status: "ok", run: acceptedRun, logs: [], steps: [] }),
      });
    });

    await page.goto(`${liveApp.baseURL}/runs/legacy-unresolved-context`);
    const context = page.getByTestId("run-again-context");
    await expect(context).toContainText("Environment current default resolved at start");
    await expect(context).toContainText("current pipeline default resolved at start");
    await expect(page.getByText(/execution context unavailable/)).toBeVisible();
    const rerunButton = page.getByRole("button", {
      name: "Run again with current settings",
    });
    await expect(rerunButton).toContainText("Run current settings");

    const rerunRequest = page.waitForRequest(
      (candidate) =>
        candidate.url().endsWith(`/api/pipelines/${analyticsPipelineId}/trigger`) &&
        candidate.method() === "POST",
    );
    await rerunButton.click();
    expect((await rerunRequest).postDataJSON()).toEqual({ source: "working_tree" });
    await expect(page).toHaveURL(new RegExp("/runs/legacy-default-rerun$"));
  });

  test("opens a run with structured events and one combined output stream", async ({
    liveApp,
    page,
    request,
  }) => {
    const runId = await triggerPipelineRun(liveApp, request);
    const detail = await waitForRunDetail(
      liveApp,
      request,
      runId,
      (current) =>
        (current.steps?.length ?? 0) > 0 &&
        ["success", "failed", "cancelled"].includes(current.run.status),
    );
    const stepAsset = detail.steps?.[0]?.asset;

    await page.goto(`${liveApp.baseURL}/runs/${runId}`);

    await expect(page.getByRole("heading", { name: `Run ${runId}` })).toBeVisible({
      timeout: 15000,
    });
    await expect(page.getByText(/Run of analytics/)).toBeVisible();
    await expect(page.getByTestId("run-again-context")).toContainText("default");
    await expect(page.getByTestId("run-again-context")).toContainText("Recorded window");
    await expect(page.getByTestId("run-again-context")).toContainText("Mode current settings");
    await expect(page.getByRole("tab", { name: "Events" })).toBeVisible();
    const outputTab = page.getByRole("tab", { name: "Output" });
    await expect(outputTab).toBeVisible();
    await expect(page.getByRole("tab", { name: "stderr" })).toHaveCount(0);
    if (stepAsset) {
      const timelineLabel = page
        .locator('[data-testid="run-timeline-asset-label"]')
        .filter({ hasText: stepAsset })
        .first();
      await expect(timelineLabel).toBeVisible({
        timeout: 30000,
      });
      expect(
        await timelineLabel.evaluate((element) => ({
          overflow: getComputedStyle(element).overflow,
          textOverflow: getComputedStyle(element).textOverflow,
          whiteSpace: getComputedStyle(element).whiteSpace,
        })),
      ).toEqual({ overflow: "visible", textOverflow: "clip", whiteSpace: "normal" });

      const timelineTrack = page.locator('[data-testid="run-timeline-track"]').first();
      const timelineBar = timelineTrack.getByTestId("run-timeline-bar");
      await expect(timelineBar).toHaveAttribute("data-slot", "tooltip-trigger");
      await expect(
        timelineTrack.locator('xpath=ancestor::*[@data-slot="scroll-area-viewport"]'),
      ).toHaveCount(0);

      const eventRow = page.getByTestId("run-event-row").filter({ hasText: stepAsset }).first();
      await timelineTrack.hover();
      await expect(eventRow).toHaveAttribute("data-highlighted", "true");
      await eventRow.hover();
      await expect(timelineTrack).toHaveAttribute("data-highlighted", "true");

      const assetLink = page.getByRole("link", { name: stepAsset, exact: true }).first();
      await expect(assetLink).toHaveAttribute(
        "href",
        new RegExp(`/pipelines/${analyticsPipelineId}/assets/[^/]+/split$`),
      );
    }
    const startBadge = page.locator('[data-event-type="asset_start"]').first();
    const successBadge = page.locator('[data-event-type="asset_success"]').first();
    await expect(startBadge).toHaveAttribute("data-event-tone", "progress", { timeout: 30000 });
    await expect(successBadge).toHaveAttribute("data-event-tone", "success", {
      timeout: 30000,
    });
    expect(
      await startBadge.evaluate((element) => getComputedStyle(element).backgroundColor),
    ).not.toBe(await successBadge.evaluate((element) => getComputedStyle(element).backgroundColor));

    await outputTab.click();
    const terminal = page.locator('[data-slot="tabs-content"][data-state="active"] pre');
    await expect(terminal).toContainText("Analyzed the pipeline 'analytics'", { timeout: 30000 });
    await expect(terminal).toContainText("bruin run completed successfully", { timeout: 30000 });
    const output = (await terminal.innerText()).replace(/\r\n/g, "\n");
    expect(output.match(/Analyzed the pipeline 'analytics'/g)).toHaveLength(1);
    expect(output).toMatch(/PASS analytics\.[^\n]+\nPASS analytics\./);
  });

  test("keeps a terminal failure in the combined output", async ({ liveApp, page, request }) => {
    await writeFile(
      join(liveApp.workspaceDir, "analytics/assets/analytics/orders.sql"),
      `/* @bruin
type: duckdb.sql
materialization:
  type: view
@bruin */

select * from analytics.table_that_does_not_exist
`,
      "utf8",
    );

    const runId = await triggerPipelineRun(liveApp, request);
    const detail = await waitForRunDetail(
      liveApp,
      request,
      runId,
      (current) => current.run.status === "failed",
    );
    expect(detail.run.error).toBeTruthy();

    await page.goto(`${liveApp.baseURL}/runs/${runId}`);
    await expect(page.getByRole("tab", { name: "stderr" })).toHaveCount(0);
    await expect(page.locator('[data-event-tone="failure"]').first()).toBeVisible({
      timeout: 30000,
    });
    await page.getByRole("tab", { name: "Output" }).click();

    const terminal = page.locator('[data-slot="tabs-content"][data-state="active"] pre');
    await expect(terminal).toContainText(detail.run.error!, { timeout: 30000 });
  });

  test("rejects a missing dependency before any asset starts", async ({ liveApp, request }) => {
    await writeFile(
      join(liveApp.workspaceDir, "analytics/assets/analytics/orders.sql"),
      `/* @bruin
type: duckdb.sql
depends:
  - analytics.missing
materialization:
  type: view
@bruin */

select 1 as id
`,
      "utf8",
    );

    const runId = await triggerPipelineRun(liveApp, request);
    const detail = await waitForRunDetail(
      liveApp,
      request,
      runId,
      (current) => current.run.status === "failed",
    );
    const output = detail.logs?.map((line) => line.line).join("") ?? "";

    expect(detail.run.error).toContain("pipeline dependency validation failed with 1 issue");
    expect(output).toContain("Dependency 'analytics.missing' does not exist");
    expect(output).toContain("(dependency-exists)");
    expect(output).not.toContain("Starting the pipeline execution");
    expect(detail.steps ?? []).toEqual([]);
  });
});

async function triggerPipelineRun(
  liveApp: LiveApp,
  request: APIRequestContext,
  input: { start?: string; end?: string; sensor_mode?: "once" | "wait" | "skip" } = {},
) {
  const scheduleResponse = await request.get(`${liveApp.baseURL}/api/schedules`);
  expect(scheduleResponse.ok()).toBe(true);
  const schedules = (await scheduleResponse.json()) as ScheduleResponse;
  const pipeline =
    schedules.schedules.find((item) => item.pipeline_name === "analytics") ??
    schedules.schedules[0];
  expect(pipeline).toBeTruthy();

  const triggerResponse = await request.post(
    `${liveApp.baseURL}/api/pipelines/${encodeURIComponent(pipeline.pipeline_id)}/trigger`,
    {
      data: { source: "working_tree", ...input },
    },
  );
  expect(triggerResponse.ok()).toBe(true);
  const triggered = (await triggerResponse.json()) as TriggerResponse;
  expect(triggered.run.id).toBeTruthy();

  await waitForRunDetail(liveApp, request, triggered.run.id, (detail) =>
    ["success", "failed", "running"].includes(detail.run.status),
  );
  return triggered.run.id;
}

async function waitForRunDetail(
  liveApp: LiveApp,
  request: APIRequestContext,
  runId: string,
  predicate: (detail: RunDetailResponse) => boolean,
) {
  const deadline = Date.now() + 60000;
  let lastDetail: RunDetailResponse | null = null;
  while (Date.now() < deadline) {
    const response = await request.get(`${liveApp.baseURL}/api/runs/${encodeURIComponent(runId)}`);
    if (response.ok()) {
      const detail = (await response.json()) as RunDetailResponse;
      lastDetail = detail;
      if (detail.status === "ok" && predicate(detail)) {
        return detail;
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  throw new Error(`Timed out waiting for run ${runId}: ${JSON.stringify(lastDetail)}`);
}
