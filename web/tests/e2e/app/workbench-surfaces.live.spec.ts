import { expect } from "@playwright/test";
import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true, colorScheme: "dark" });
const pipeline = Buffer.from("analytics").toString("base64url");

// Fixed viewports reproduce the reported desktop and narrow-screen geometry.
test("rounded workbench panels have no rectangular backdrop @desktop-only", async ({
  page,
  liveApp,
}, info) => {
  await page.setViewportSize({ width: 937, height: 818 });
  await page.goto(`${liveApp.baseURL}/pipelines/${pipeline}/canvas?result=inspect&editor=asset`);
  await expect(page.locator(".react-flow__node").first()).toBeVisible();
  const cards = page.locator("section [data-slot=delimited-card]");
  await expect(cards).toHaveCount(2);
  for (const card of await cards.all()) {
    const surface = await card.evaluate((element) => {
      const style = getComputedStyle(element);
      const backdrops: string[] = [];
      for (
        let parent = element.parentElement;
        parent && parent.tagName !== "SECTION";
        parent = parent.parentElement
      ) {
        const background = getComputedStyle(parent).backgroundColor;
        if (background !== "rgba(0, 0, 0, 0)" && background !== "transparent")
          backdrops.push(background);
      }
      return { radius: parseFloat(style.borderTopLeftRadius), overflow: style.overflow, backdrops };
    });
    expect(surface.radius).toBeGreaterThan(0);
    expect(surface.overflow).toBe("hidden");
    expect(surface.backdrops).toEqual([]);
  }
  await page.screenshot({ path: info.outputPath("rounded-workbench.png"), animations: "disabled" });
});

test("mobile navigation close aligns with compact header actions", async ({
  page,
  liveApp,
}, info) => {
  await page.setViewportSize({ width: 675, height: 818 });
  await page.goto(`${liveApp.baseURL}/pipelines/${pipeline}/canvas?result=inspect&editor=asset`);
  const resources = page.getByRole("tab", { name: "Resources", exact: true });
  await resources.click();
  const sheet = page.getByRole("dialog", { name: "Build navigation", exact: true });
  const close = sheet.getByRole("button", { name: "Close", exact: true });
  const create = sheet.getByRole("button", { name: "New pipeline", exact: true });
  await expect(close).toBeVisible();
  await expect
    .poll(async () => {
      const [a, b] = await Promise.all([close.boundingBox(), create.boundingBox()]);
      return a && b ? Math.abs(a.y + a.height / 2 - b.y - b.height / 2) : Infinity;
    })
    .toBeLessThanOrEqual(1);
  await page.screenshot({
    path: info.outputPath("aligned-navigation-close.png"),
    animations: "disabled",
  });
  await close.click();
  await expect(sheet).toBeHidden();
  expect(new URL(page.url()).searchParams.get("result")).toBe("inspect");
  await resources.click();
  await expect(sheet).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(sheet).toBeHidden();
});
