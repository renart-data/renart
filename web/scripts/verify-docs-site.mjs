// Run against the built Dockerfile.docs image, bound to loopback with test legal
// values and RENART_UMAMI_WEBSITE_ID. No analytics request leaves this browser.
import { chromium, expect } from "@playwright/test";
import { mkdir, readdir } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const baseURL = process.argv[2] ?? "http://127.0.0.1:18187";
const outputDir = path.resolve(
  process.env.RENART_DOCS_OUTPUT_DIR ?? path.join(root, ".test-artifacts/docs-site"),
);
await mkdir(outputDir, { recursive: true });
if (new URL(baseURL).hostname !== "127.0.0.1")
  throw new Error("Only a local docs review server is allowed");
const files = (await readdir(path.join(root, "docs/src/content/docs"), { recursive: true })).filter(
  (p) => p.endsWith(".mdx"),
);
const routes = files.map((p) => "/" + p.replace(/(?:\/index)?\.mdx$/, "") + "/");
for (const route of ["/", ...routes, "/privacy/", "/legal-notice/"]) {
  const response = await fetch(baseURL + route);
  const html = await response.text();
  if (
    response.status !== 200 ||
    !response.headers.get("content-security-policy")?.includes("frame-ancestors 'none'") ||
    html.includes("[[RENART")
  )
    throw new Error(`Caddy response failed: ${route} (${response.status})`);
}
const browser = await chromium.launch();
const errors = [];
const browserOptions = {
  userAgent:
    "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36",
  colorScheme: "dark",
};
try {
  for (const viewport of [
    { width: 1440, height: 1000 },
    { width: 390, height: 844 },
  ]) {
    const context = await browser.newContext({ ...browserOptions, viewport });
    const page = await context.newPage();
    page.on("pageerror", (error) => errors.push(`${page.url()}: ${error.message}`));
    for (const route of ["/", ...routes]) {
      await page.goto(baseURL + route, { waitUntil: "load" });
      await page.evaluate(() => document.fonts.ready);
      const size = await page.evaluate(() => ({
        full: document.documentElement.scrollWidth,
        viewport: innerWidth,
      }));
      if (size.full > size.viewport + 1)
        throw new Error(`${route} overflows at ${viewport.width}px (${size.full}px)`);
      if (route.startsWith("/docs/")) await expect(page.locator("main h1")).toHaveCount(1);
      if (route.includes("http-api-assets"))
        await expect(page.locator("main")).toContainText("{{ start_timestamp }}");
      if (route.includes("notebooks/overview"))
        await expect(page.locator("main")).toContainText("{{ parameter.region }}");
      if (route.includes("supported-platforms")) {
        for (const name of ["Trino", "StarRocks", "ClickHouse"])
          await expect(page.locator("main")).toContainText(name);
        await page.screenshot({
          path: path.join(outputDir, `platforms-${viewport.width}.png`),
          fullPage: true,
        });
      }
    }
    await context.close();
    console.log(`PASS ${routes.length + 1} routes at ${viewport.width}px`);
  }
  const context = await browser.newContext({
    ...browserOptions,
    viewport: { width: 1440, height: 1000 },
  });
  await context.addInitScript(() =>
    Object.defineProperty(navigator, "webdriver", { get: () => false }),
  );
  let trackerRequests = 0;
  await context.route("https://umami.getrenart.com/**", async (route) => {
    trackerRequests++;
    await route.fulfill({
      status: 200,
      contentType: "text/javascript",
      body: "/* Local consent test: no analytics is sent. */",
    });
  });
  const page = await context.newPage();
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(baseURL, { waitUntil: "load" });
  const policy = await page.evaluate(async () => {
    await WebAssembly.compile(new Uint8Array([0, 97, 115, 109, 1, 0, 0, 0]));
    try {
      (0, eval)("1 + 1");
      return { evalBlocked: false };
    } catch {
      return { evalBlocked: true };
    }
  });
  if (!policy.evalBlocked) throw new Error("CSP must keep JavaScript eval disabled");
  await expect(page.getByRole("button", { name: "Only essential", exact: true })).toBeVisible();
  if (trackerRequests) throw new Error("Analytics loaded before consent");
  await page.getByRole("button", { name: "Only essential", exact: true }).click();
  await page.reload({ waitUntil: "load" });
  if (trackerRequests) throw new Error("Analytics loaded after decline");
  await page.locator("[data-open-privacy-settings]").click();
  await page.getByRole("button", { name: "Accept all", exact: true }).click();
  await expect.poll(() => trackerRequests).toBe(1);
  await expect(page.locator("script[data-renart-umami]")).toHaveCount(1);
  await page.locator("[data-open-privacy-settings]").click();
  await page.getByRole("button", { name: "Only essential", exact: true }).click();
  await page.waitForLoadState("load");
  await expect(page.locator("script[data-renart-umami]")).toHaveCount(0);
  const dismiss = page.getByRole("button", { name: "Dismiss Discord invitation", exact: true });
  if (await dismiss.isVisible()) await dismiss.click();
  await page.reload({ waitUntil: "load" });
  await expect(page.locator("[data-discord-invite-card]")).toBeHidden();
  await expect(
    page.getByRole("link", { name: "Join Renart on Discord", exact: true }),
  ).toHaveAttribute("href", "https://discord.gg/jTH758KNP8");
  await context.close();
  if (errors.length) throw new Error(`Browser errors: ${errors.join("; ")}`);
  console.log(
    "PASS Caddy headers, WebAssembly with JS eval blocked, literal Jinja, consent/decline/revocation, Discord persistence; zero page errors",
  );
} finally {
  await browser.close();
}
