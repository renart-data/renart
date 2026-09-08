import { expect, type Page } from "@playwright/test";
import { liveTest as test } from "../live-app-fixture";
import { createLiveStorage } from "../live-storage-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
// Set up Docker networking before opening a page. Starting it during the test
// body can interrupt Chromium's initial module requests (ERR_NETWORK_CHANGED).
const storageTest = test.extend<{ storage: Awaited<ReturnType<typeof createLiveStorage>> }>({
  storage: [
    // eslint-disable-next-line no-empty-pattern -- Playwright requires destructured fixture dependencies, even when there are none.
    async ({}, use) => {
      const storage = await createLiveStorage();
      try {
        await use(storage);
      } finally {
        await storage.dispose();
      }
    },
    { auto: true, timeout: 60000 },
  ],
});
const pipeline = Buffer.from("analytics").toString("base64url");
const canvas = `/pipelines/${pipeline}/canvas?result=inspect&editor=asset`;

async function openData(page: Page, baseURL: string, mobile: boolean) {
  await page.goto(baseURL + canvas);
  await (
    mobile
      ? page.getByRole("tab", { name: "Data", exact: true })
      : page.getByRole("button", { name: "Data Browser", exact: true })
  ).click();
  await expect(page.getByRole("button", { name: /duckdb-default.*DuckDB/ })).toBeVisible();
  return page.getByRole("textbox", { name: "Search data browser", exact: true });
}

test("path search completes warehouse names and preserves focus without recursive discovery", async ({
  page,
  liveApp,
  isMobile,
}, info) => {
  for (const query of [
    "CREATE SCHEMA search_demo",
    "CREATE TABLE search_demo.alpha_orders AS SELECT 1 AS id",
    "CREATE TABLE search_demo.beta AS SELECT 2 AS id",
    "CREATE SCHEMA unrelated",
  ]) {
    const response = await page.request.post(`${liveApp.baseURL}/api/sql/query`, {
      data: { connection: "duckdb-default", environment: "default", query },
    });
    expect(response.ok(), await response.text()).toBe(true);
    expect((await response.json()).status).toBe("ok");
  }
  const requests: string[] = [];
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  page.on("request", (request) => {
    if (/\/data-browser\/.*\/(children|prefix)/.test(request.url())) requests.push(request.url());
  });
  const input = await openData(page, liveApp.baseURL, isMobile);
  const originalURL = page.url();
  await input.fill("duckdb-def");
  await expect(page.getByTestId("data-browser-shadow-suggestion")).toHaveText("duckdb-default.");
  await page.screenshot({ path: info.outputPath("connection-completion.png") });
  expect(requests).toEqual([]);
  if (isMobile)
    await page.getByRole("button", { name: "Complete to duckdb-default.", exact: true }).click();
  else await input.press("Tab");
  await expect(input).toHaveValue("duckdb-default.");
  await expect(input).toBeFocused();
  await expect(page.getByRole("button", { name: "search_demo", exact: true })).toBeVisible();
  await input.pressSequentially("search_d");
  await expect(page.getByTestId("data-browser-shadow-suggestion")).toContainText("search_demo.");
  await input.press("Tab");
  await expect(input).toHaveValue("duckdb-default.search_demo.");
  await expect(page.getByRole("link", { name: "alpha_orders", exact: true })).toBeVisible();
  await input.pressSequentially("al");
  await expect(input).toHaveValue("duckdb-default.search_demo.al");
  await expect(input).toBeFocused();
  await expect(page.getByRole("link", { name: "beta", exact: true })).toBeHidden();
  await page.screenshot({ path: info.outputPath("table-completion.png") });
  await input.press("Escape");
  await expect(page.getByTestId("data-browser-shadow-suggestion")).toBeHidden();
  await expect(input).toHaveValue("duckdb-default.search_demo.al");
  await input.press("Tab");
  await expect(input).not.toBeFocused();
  await input.fill("duckdb-default.search_demo.alp");
  await input.press("Shift+Tab");
  await expect(input).not.toBeFocused();
  await expect(input).toHaveValue("duckdb-default.search_demo.alp");
  await input.focus();
  await input.press("Tab");
  await expect(input).toHaveValue("duckdb-default.search_demo.alpha_orders");
  await expect(page.getByTestId("data-browser-shadow-suggestion")).toBeHidden();
  expect(requests).toHaveLength(2); // connection + selected namespace only
  expect(page.url()).toBe(originalURL); // no editor/result/sidebar routing as a side effect
  await input.fill("duckb-def");
  await expect(page.getByTestId("data-browser-shadow-suggestion")).toContainText(
    "→ duckdb-default.",
  );
  await input.press("Tab");
  await expect(input).toHaveValue("duckdb-default.");
  await input.fill("duckdb-default.search_demo.al");
  await input.press("ArrowLeft");
  await expect(page.getByTestId("data-browser-shadow-suggestion")).toBeHidden();
  expect(requests).toHaveLength(2);
  expect(errors).toEqual([]);
});

test("changing the search path cancels stale discovery and leaves normal navigation usable", async ({
  page,
  liveApp,
  isMobile,
}) => {
  let release!: () => void;
  const waiting = new Promise<void>((done) => {
    release = done;
  });
  let started!: () => void;
  const requested = new Promise<void>((done) => {
    started = done;
  });
  await page.route("**/api/data-browser/connections/*/children*", async (route) => {
    const token = decodeURIComponent(new URL(route.request().url()).pathname.split("/").at(-2)!);
    const ref = JSON.parse(Buffer.from(token, "base64url").toString());
    if (ref.c !== "duckdb-default") return route.continue();
    started();
    await waiting;
    await route
      .fulfill({ json: { nodes: [{ id: "late", label: "Stale result", node_type: "namespace" }] } })
      .catch(() => {});
  });
  const input = await openData(page, liveApp.baseURL, isMobile);
  try {
    await input.fill("duckdb-default.main.a");
    await requested;
    await input.fill('"Project files"./');
    await expect(page.getByRole("button", { name: "analytics", exact: true })).toBeVisible();
  } finally {
    release();
  }
  await expect(page.getByRole("button", { name: "Stale result", exact: true })).toBeHidden();
  await expect(input).toHaveValue('"Project files"./');
  await page.getByRole("button", { name: "Clear data browser search", exact: true }).click();
  await expect(page.getByRole("button", { name: /duckdb-default.*DuckDB/ })).toBeVisible();
  await page.getByRole("button", { name: /Project files.*Files inside this project/ }).click();
  await expect(page.getByRole("button", { name: "analytics", exact: true })).toBeVisible();
});

storageTest(
  "S3 and SFTP search list only typed prefixes and complete folder and object names",
  async ({ page, liveApp, isMobile, storage }, info) => {
    test.setTimeout(120000);
    for (const provider of ["s3", "sftp"]) {
      const response = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
        data: {
          environment_name: "default",
          name: `${provider}-search`,
          type: provider,
          values:
            provider === "s3"
              ? { bucket_name: "browser", endpoint_url: `http://127.0.0.1:${storage.minioPort}` }
              : { host: "127.0.0.1", port: storage.sftpPort, username: "fixture" },
          secret_changes:
            provider === "s3"
              ? {
                  access_key_id: { action: "replace", value: "renart" },
                  secret_access_key: { action: "replace", value: "renart-secret" },
                }
              : { password: { action: "replace", value: storage.password } },
        },
      });
      expect(response.ok(), await response.text()).toBe(true);
    }
    const requests: URL[] = [];
    page.on("request", (request) => {
      if (/\/data-browser\/.*\/(children|prefix)/.test(request.url()))
        requests.push(new URL(request.url()));
    });
    const input = await openData(page, liveApp.baseURL, isMobile);
    await input.fill("s3-search./inc");
    await expect(page.getByTestId("data-browser-shadow-suggestion")).toContainText("incoming/");
    if (isMobile)
      await page
        .getByRole("button", { name: "Complete to s3-search./incoming/", exact: true })
        .click();
    else await input.press("Tab");
    await expect(input).toHaveValue("s3-search./incoming/");
    await expect(page.getByRole("link", { name: /orders.csv/ })).toBeVisible();
    await input.fill("s3-search./incoming");
    await expect(page.getByRole("link", { name: /orders.csv/ })).toBeVisible();
    await expect(page.getByTestId("data-browser-shadow-suggestion")).toContainText("incoming/");
    await input.press("Tab");
    await input.pressSequentially("ord");
    await expect(page.getByTestId("data-browser-shadow-suggestion")).toContainText("orders.csv");
    await page.screenshot({ path: info.outputPath("storage-completion.png") });
    await input.press("Tab");
    await expect(input).toHaveValue("s3-search./incoming/orders.csv");
    await expect(page.getByTestId("data-browser-shadow-suggestion")).toBeHidden();
    await input.fill("sftp-search./incoming/");
    await expect(page.getByRole("link", { name: /orders.csv/ })).toBeVisible();
    expect(requests.map((url) => url.searchParams.get("path") ?? "")).toEqual([
      "",
      "incoming/",
      "incoming/",
    ]);
    expect(requests.every((url) => url.pathname.endsWith("/prefix"))).toBe(true);
    const missing = page.waitForResponse((response) => {
      const url = new URL(response.url());
      return (
        url.pathname.endsWith("/prefix") &&
        url.searchParams.get("path") === "not-in-root-list/nested/"
      );
    });
    await input.fill("s3-search./not-in-root-list/nested/");
    const missingResponse = await missing;
    // Providers may report an empty prefix or a listing error. Neither is a
    // reason to show the old folder, or to enumerate missing ancestors.
    if (missingResponse.ok())
      await expect(page.getByText("No objects here.", { exact: true })).toBeVisible();
    else {
      await expect(page.getByRole("alert")).toContainText(
        (await missingResponse.json()).error.message,
      );
      await expect(page.getByText("No objects here.", { exact: true })).toBeHidden();
    }
    await expect(page.getByRole("link", { name: /orders.csv/ })).toBeHidden();
    expect(requests.at(-1)!.searchParams.get("path")).toBe("not-in-root-list/nested/");
    expect(requests).toHaveLength(4);
  },
);
