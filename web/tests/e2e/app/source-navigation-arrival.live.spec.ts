import { expect } from "@playwright/test";
import { sourceAnchorFingerprint } from "../../../lib/deployment-diff-annotations";
import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

test("source arrivals highlight validated ranges, not stale or out-of-file locations", async ({
  page,
  liveApp,
}, info) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  const workspace = await (await page.request.get(`${liveApp.baseURL}/api/workspace`)).json();
  const asset = workspace.pipelines[0].assets.find(
    (a: { name: string }) => a.name === "analytics.customers",
  );
  const config = await (await page.request.get(`${liveApp.baseURL}/api/config`)).json();
  const fingerprint = sourceAnchorFingerprint(asset.content);
  const open = async (line: number, sourceFingerprint: string) => {
    const url = new URL(`${liveApp.baseURL}/schedules/deployments`);
    url.searchParams.set("project", config.project_id);
    url.searchParams.set(
      "detail",
      JSON.stringify({
        v: 1,
        environment: "default",
        target: {
          kind: "asset-section",
          asset_id: asset.id,
          section: "source",
          line,
          end_line: line,
          source_fingerprint: sourceFingerprint,
        },
      }),
    );
    await page.goto(url.href);
  };
  await open(1, fingerprint);
  const mark = page.locator(".navigation-arrival-range");
  await expect(mark).toHaveCount(1);
  await expect(mark).toHaveCSS("animation-name", "none");
  await page.screenshot({ path: info.outputPath("source-arrival.png") });
  await expect(mark).toHaveCount(0);
  await open(1, sourceAnchorFingerprint("old source"));
  await expect(
    page.getByRole("alert").filter({ hasText: "Source changed since this diagnostic" }),
  ).toBeVisible();
  await expect(mark).toHaveCount(0);
  await open(999999, fingerprint);
  await expect(
    page.getByRole("alert").filter({ hasText: "outside the current file" }),
  ).toBeVisible();
  await expect(mark).toHaveCount(0);
});
