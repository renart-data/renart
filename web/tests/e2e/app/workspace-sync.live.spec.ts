import { expect } from "@playwright/test";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { liveTest as test } from "../live-app-fixture";

const pipelineId = Buffer.from("analytics").toString("base64url");
const addedAssetPath = "analytics/assets/analytics/late_snapshot.sql";
const addedAssetId = Buffer.from(addedAssetPath).toString("base64url");
const workspaceURL = /\/api\/(?:projects\/[^/]+\/)?workspace(?:\?.*)?$/;

test.describe("workspace snapshot reconciliation live", () => {
  test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

  test("keeps a newer SSE asset when initial HTTP snapshots arrive late", async ({
    liveApp,
    page,
  }) => {
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
    let releaseSnapshots!: () => void;
    const snapshotsReleased = new Promise<void>((resolve) => {
      releaseSnapshots = resolve;
    });
    let capturedSnapshots = 0;
    let finishedSnapshots = 0;
    let delaying = true;
    page.on("requestfinished", (request) => {
      if (workspaceURL.test(request.url())) finishedSnapshots += 1;
    });
    await page.route(workspaceURL, async (route) => {
      if (!delaying) {
        await route.continue();
        return;
      }
      // Keep the real server's response and revision, only reorder delivery.
      const response = await route.fetch();
      expect(response.ok()).toBe(true);
      capturedSnapshots += 1;
      await snapshotsReleased;
      await route.fulfill({ response });
    });

    try {
      const route = `${liveApp.baseURL}/pipelines/${pipelineId}/canvas?result=inspect&editor=asset`;
      await page.goto(route);
      // Both the optimistic request and the post-subscription reconciliation
      // are held, so the canvas initially receives its state only through SSE.
      await expect.poll(() => capturedSnapshots).toBeGreaterThanOrEqual(2);
      await expect(page.getByTestId("lineage-asset")).toHaveCount(2);

      await writeFile(
        join(liveApp.workspaceDir, addedAssetPath),
        `/* @bruin
name: analytics.late_snapshot
type: duckdb.sql
materialization:
  type: view
@bruin */

select 1 as value
`,
        "utf8",
      );
      const addedNode = page.getByTestId(`rf__node-${addedAssetId}`);
      await expect(addedNode).toHaveCount(1, { timeout: 20000 });

      const expectedSnapshots = capturedSnapshots;
      delaying = false;
      releaseSnapshots();
      await expect.poll(() => finishedSnapshots).toBeGreaterThanOrEqual(expectedSnapshots);
      // Allow the delivered response promises and React's next render to
      // settle before asserting that an obsolete snapshot was not applied.
      await page.evaluate(
        () =>
          new Promise<void>((resolve) =>
            requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
          ),
      );
      await expect(addedNode).toHaveCount(1);
      await expect(page.getByTestId("lineage-asset")).toHaveCount(3);
      await expect(page).toHaveURL(route);
      expect(errors).toEqual([]);
    } finally {
      delaying = false;
      releaseSnapshots();
      await page.unrouteAll({ behavior: "wait" });
    }
  });
});
