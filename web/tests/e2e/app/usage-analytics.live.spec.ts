import { expect } from "@playwright/test";
import http from "node:http";
import { liveTest } from "../live-app-fixture";

type UsageEvent = {
  name: string;
  outcome?: string;
  installation_id?: string;
  [key: string]: unknown;
};
const test = liveTest.extend<{ collector: { url: string; events: UsageEvent[] } }>({
  // Playwright requires destructuring even for a fixture without dependencies.
  // oxlint-disable-next-line no-empty-pattern
  collector: async ({}, use) => {
    const events: UsageEvent[] = [];
    const server = http.createServer(async (req, res) => {
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.from(chunk));
      const batch = JSON.parse(Buffer.concat(chunks).toString());
      events.push(...batch.events);
      res.writeHead(204).end();
    });
    await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
    const address = server.address();
    if (!address || typeof address === "string") throw new Error("No collector port");
    try {
      await use({ url: `http://127.0.0.1:${address.port}/collect`, events });
    } finally {
      await new Promise<void>((resolve, reject) =>
        server.close((error) => (error ? reject(error) : resolve())),
      );
    }
  },
  liveAppEnv: async ({ collector }, use) =>
    use({
      RENART_TELEMETRY: "on",
      RENART_TELEMETRY_ENDPOINT: collector.url,
      TELEMETRY_OPTOUT: "",
      DO_NOT_TRACK: "",
      CI: "",
    }),
});
test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

test("usage analytics requires its notice, sends only safe outcomes, and opt-out syncs across tabs", async ({
  page,
  context,
  liveApp,
  collector,
}, info) => {
  test.setTimeout(90000);
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(`${liveApp.baseURL}/project/general`);
  const notice = page.getByRole("alert").filter({ hasText: "Usage analytics" });
  await expect(notice).toBeVisible();
  await notice.screenshot({ path: info.outputPath("analytics-notice.png") });
  expect(collector.events).toEqual([]);
  const before = await (await page.request.get(`${liveApp.baseURL}/api/telemetry`)).json();
  expect(before.active).toBe(false);
  expect(before.installation_id).toBeFalsy();
  await notice.getByRole("button", { name: "Got it" }).click();
  await expect(notice).toBeHidden();
  const created = await page.request.post(`${liveApp.baseURL}/api/notebooks`, {
    data: { title: "Private customer analysis" },
  });
  expect(created.ok()).toBe(true);
  const notebook = (await created.json()).notebook;
  const settings = await page.request.put(
    `${liveApp.baseURL}/api/notebooks/${notebook.id}/settings`,
    { data: { auto_recompute: false, environment: "default" } },
  );
  expect(settings.ok()).toBe(true);
  const cellResponse = await page.request.post(
    `${liveApp.baseURL}/api/notebooks/${notebook.id}/cells`,
    { data: { name: "secret_customer_data" } },
  );
  expect(cellResponse.ok()).toBe(true);
  const cell = (await cellResponse.json()).notebook.cells.find(
    (item: { name: string }) => item.name === "secret_customer_data",
  );
  const saved = await page.request.put(
    `${liveApp.baseURL}/api/notebooks/${notebook.id}/cells/${cell.cell_id}`,
    { data: { content: "/* @bruin\ntype: duckdb.sql\n@bruin */\nselect 42 as private_revenue\n" } },
  );
  expect(saved.ok()).toBe(true);
  const run = await page.request.post(`${liveApp.baseURL}/api/notebooks/${notebook.id}/run`, {
    data: { cells: [cell.cell_id], environment: "default" },
  });
  expect(run.ok(), await run.text()).toBe(true);
  await expect
    .poll(() => collector.events.some((event) => event.name === "notebook_run_finished"), {
      timeout: 35000,
    })
    .toBe(true);
  const events = collector.events;
  expect(events.filter((event) => event.name === "workspace_session_started")).toHaveLength(1);
  expect(events.filter((event) => event.name === "notebook_run_finished")).toHaveLength(1);
  expect(events.find((event) => event.name === "notebook_run_finished")?.outcome).toBe("success");
  const payload = JSON.stringify(events);
  for (const privateValue of [
    "Private customer analysis",
    "secret_customer_data",
    "private_revenue",
    liveApp.workspaceDir,
    notebook.id,
  ])
    expect(payload).not.toContain(privateValue);
  expect(events.every((event) => !event.installation_id)).toBe(true);
  const other = await context.newPage();
  const protocol = await context.newCDPSession(other);
  await protocol.send("Network.enable");
  await other.goto(`${liveApp.baseURL}/project/general`);
  const preference = page.getByRole("switch", { name: "Allow usage analytics" });
  const otherPreference = other.getByRole("switch", { name: "Allow usage analytics" });
  await expect(otherPreference).toBeChecked();
  let releaseRead!: () => void;
  let capturedRead!: () => void;
  const release = new Promise<void>((resolve) => (releaseRead = resolve));
  const captured = new Promise<void>((resolve) => (capturedRead = resolve));
  let held = false;
  await other.route("**/api/telemetry", async (route) => {
    if (held || route.request().method() !== "GET") return route.continue();
    held = true;
    const response = await route.fetch();
    capturedRead();
    await release;
    await route.fulfill({ response });
  });
  await other.evaluate(() => window.dispatchEvent(new Event("focus")));
  await captured;
  const changed = new Promise<void>((resolve) => {
    protocol.on("Network.eventSourceMessageReceived", (event) => {
      if (JSON.parse(event.data).type === "usage.settings.updated") resolve();
    });
  });
  await preference.click();
  await changed;
  await other.evaluate(
    () => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())),
  );
  releaseRead();
  await expect(preference).not.toBeChecked();
  await expect(otherPreference).not.toBeChecked();
  await protocol.detach();
  await page.getByRole("button", { name: "Inspect example payload" }).click();
  await expect(page.getByLabel("Example usage analytics payload")).toContainText(
    "pipeline_run_finished",
  );
  await page.getByText("Usage analytics is off.", { exact: true }).scrollIntoViewIfNeeded();
  await page.screenshot({ path: info.outputPath("analytics-settings.png") });
  await page.evaluate(() => document.documentElement.classList.add("dark"));
  await page.screenshot({ path: info.outputPath("analytics-settings-dark.png") });
  await page.reload();
  await expect(page.getByRole("switch", { name: "Allow usage analytics" })).not.toBeChecked();
  const status = await (await page.request.get(`${liveApp.baseURL}/api/telemetry`)).json();
  expect(status.active).toBe(false);
  expect(status.reason).toBe("disabled");
  expect(errors).toEqual([]);
  await other.close();
});
