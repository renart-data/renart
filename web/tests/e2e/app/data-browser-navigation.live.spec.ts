import { expect } from "@playwright/test";
import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
const pipeline = Buffer.from("analytics").toString("base64url");

test("typing immediately after Tab completion does not move the caret backwards", async ({
  page,
  liveApp,
  isMobile,
}) => {
  await page.goto(`${liveApp.baseURL}/pipelines/${pipeline}/canvas`);
  await (
    isMobile
      ? page.getByRole("tab", { name: "Data", exact: true })
      : page.getByRole("button", { name: "Data Browser", exact: true })
  ).click();
  const input = page.getByRole("textbox", { name: "Search data browser" });
  await input.fill('"Project files"./ana');
  await expect(page.getByTestId("data-browser-shadow-suggestion")).toHaveText("lytics");
  await page.clock.install();
  await page.clock.pauseAt(new Date(Date.now() + 1000));
  await input.press("Tab");
  await input.pressSequentially("a");
  await page.clock.runFor(20); // Flush a delayed animation frame after the next keystroke.
  await input.pressSequentially("ss");
  await expect(input).toHaveValue('"Project files"./analytics/ass');
  await page.clock.resume();
});

test("focuses the filter and navigates sources and namespaces with the keyboard", async ({
  page,
  liveApp,
  isMobile,
}) => {
  await page.goto(`${liveApp.baseURL}/pipelines/${pipeline}/canvas?result=inspect&editor=asset`);
  await (
    isMobile
      ? page.getByRole("tab", { name: "Data", exact: true })
      : page.getByRole("button", { name: "Data Browser", exact: true })
  ).click();
  const filter = page.getByRole("textbox", { name: "Search data browser" });
  await expect(filter).toBeFocused();
  await expect(page.getByRole("button", { name: /duckdb-default.*DuckDB/ })).toBeVisible();
  await filter.press("ArrowDown");
  await expect(page.getByRole("button", { name: /duckdb-default.*DuckDB/ })).toBeFocused();
  await page.keyboard.press("End");
  await expect(
    page.getByRole("button", { name: /Project files.*Files inside this project/ }),
  ).toBeFocused();
  await page.keyboard.press("Home");
  await page.keyboard.press("Enter");
  const catalog = page.getByRole("button", { name: "local Default", exact: true });
  await expect(catalog).toBeFocused();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("button", { name: "main", exact: true })).toBeFocused();
  await page.keyboard.press("ArrowLeft");
  await expect(filter).toBeFocused();
  await expect(catalog).toBeVisible();
  expect(new URL(page.url()).searchParams.get("result")).toBe("inspect");
});

test("going back cancels pending discovery and a stale reply cannot reopen the source", async ({
  page,
  liveApp,
  isMobile,
}) => {
  await page.goto(`${liveApp.baseURL}/pipelines/${pipeline}/canvas`);
  await (
    isMobile
      ? page.getByRole("tab", { name: "Data", exact: true })
      : page.getByRole("button", { name: "Data Browser", exact: true })
  ).click();
  let release!: () => void;
  const pending = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route("**/data-browser/connections/*/children*", async (route) => {
    await pending;
    await route.continue().catch(() => {});
  });
  try {
    await page.getByRole("button", { name: /duckdb-default.*DuckDB/ }).click();
    await expect(page.getByTestId("data-browser-loading")).toBeVisible();
    await page.getByRole("button", { name: "Back", exact: true }).click();
    await expect(page.getByTestId("data-browser-loading")).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: /Project files.*Files inside this project/ }),
    ).toBeVisible();
    release();
    await page.getByRole("button", { name: /Project files.*Files inside this project/ }).click();
    await expect(page.getByRole("heading", { name: "Project files", exact: true })).toBeVisible();
  } finally {
    release();
  }
});

test("a stalled source listing times out and can be retried", async ({
  page,
  liveApp,
  isMobile,
}) => {
  await page.goto(`${liveApp.baseURL}/pipelines/${pipeline}/canvas`);
  await page.clock.install();
  let release!: () => void;
  const stalled = new Promise<void>((resolve) => {
    release = resolve;
  });
  let requests = 0;
  await page.route("**/api/data-browser/connections?*", async (route) => {
    if (++requests === 1) {
      await stalled;
      await route.abort().catch(() => {});
    } else await route.continue();
  });
  try {
    await (
      isMobile
        ? page.getByRole("tab", { name: "Data", exact: true })
        : page.getByRole("button", { name: "Data Browser", exact: true })
    ).click();
    await expect(page.getByTestId("data-browser-loading")).toBeVisible();
    await expect.poll(() => requests).toBe(1);
    await page.clock.fastForward(30_001);
    await expect(page.getByText(/Data source discovery timed out/)).toBeVisible();
    await expect(page.getByTestId("data-browser-loading")).toHaveCount(0);
    release();
    await page.getByRole("button", { name: "Retry", exact: true }).click();
    await expect(page.getByRole("button", { name: /duckdb-default.*DuckDB/ })).toBeVisible();
    await expect(page.getByRole("alert")).toHaveCount(0);
  } finally {
    release();
  }
});
