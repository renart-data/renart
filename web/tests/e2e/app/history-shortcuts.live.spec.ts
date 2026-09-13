import { expect } from "@playwright/test";
import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

test("Alt arrows navigate the webview history once, including from inputs @desktop-only", async ({
  page,
  liveApp,
}) => {
  // The native webview has no browser toolbar. The app owns these shortcuts;
  // assert preventDefault as well as navigation so Chromium's built-in shortcut
  // cannot make this regression pass without our handler.
  await page.goto(`${liveApp.baseURL}/project/connections`);
  await page.getByRole("button", { name: "New connection", exact: true }).click();
  const newURL = page.url();
  const input = page
    .getByRole("region", { name: "New connection", exact: true })
    .getByLabel("Name", { exact: true });
  await input.focus();
  await page.evaluate(() => {
    window.addEventListener("keydown", (event) => {
      if (event.altKey && event.key === "ArrowLeft")
        document.documentElement.dataset.historyShortcutHandled = String(event.defaultPrevented);
    });
  });
  await page.keyboard.press("Alt+ArrowLeft");
  await expect(page).not.toHaveURL(newURL);
  await expect(page.locator("html")).toHaveAttribute("data-history-shortcut-handled", "true");
  await expect(page.getByRole("region", { name: "New connection", exact: true })).toBeHidden();
  await page.keyboard.press("Alt+ArrowRight");
  await expect(page).toHaveURL(newURL);
  await expect(input).toBeVisible();
});
