import { expect, type Page } from "@playwright/test";
import { writeFile } from "node:fs/promises";
import { join } from "node:path";
import { storageSecretChanges, storageTest as test } from "../live-storage-app-fixture";
import { observeArrivals, arrivals } from "../navigation-arrival-probe";
import type {
  DataBrowserConnectionsResponse,
  DataBrowserChildrenResponse,
  WorkspaceState,
} from "../../../lib/generated/api-types";

const pipelineId = Buffer.from("analytics").toString("base64url");
const canvas = `/pipelines/${pipelineId}/canvas?result=inspect&editor=asset`;

async function openData(page: Page) {
  await expect(page.locator(".react-flow__node").first()).toBeVisible({ timeout: 20000 });
  if (test.info().project.name.includes("mobile")) {
    await page.getByRole("tab", { name: "Data", exact: true }).click();
  } else {
    const toggle = page.getByRole("button", { name: "Data Browser", exact: true });
    if (
      !(await page.getByRole("button", { name: "Refresh data sources", exact: true }).isVisible())
    )
      await toggle.click();
  }
  await expect(
    page.getByRole("button", { name: "Refresh data sources", exact: true }),
  ).toBeVisible();
}

test.describe("Sling storage browser", () => {
  test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });
  test.setTimeout(180000);
  for (const provider of ["s3", "sftp"] as const) {
    test(`${provider} browses real prefixes and creates reviewed Loads in chosen groups and downstream destinations`, async ({
      page,
      liveApp,
      storage,
    }, info) => {
      page.setDefaultTimeout(20000);
      const errors: string[] = [];
      page.on("pageerror", (error) => errors.push(error.message));
      const name = `${provider}-browser`;
      // Seed the second group before config creation refreshes the workspace.
      await writeFile(
        join(liveApp.workspaceDir, "analytics/assets/landing.sql"),
        "/* @bruin\nname: landing.anchor\ntype: duckdb.sql\nconnection: duckdb-default\n@bruin */\nselect 1 as id\n",
      );
      const created = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
        data: {
          environment_name: "default",
          name,
          type: provider,
          values:
            provider === "s3"
              ? {
                  bucket_name: "browser",
                  endpoint_url: `http://127.0.0.1:${storage.minioPort}`,
                }
              : {
                  host: "127.0.0.1",
                  port: storage.sftpPort,
                  username: "fixture",
                },
          secret_changes: storageSecretChanges(provider),
        },
      });
      expect(created.ok(), await created.text()).toBe(true);
      const list = await page.request.get(`${liveApp.baseURL}/api/data-browser/connections`);
      const connection = ((await list.json()) as DataBrowserConnectionsResponse).connections.find(
        (item) => item.name === name,
      )!;
      expect(connection.source_kind).toBe("storage");
      const rootsResponse = await page.request.get(
        `${liveApp.baseURL}/api/data-browser/connections/${connection.id}/children`,
      );
      expect(rootsResponse.ok(), await rootsResponse.text()).toBe(true);
      const roots = (await rootsResponse.json()) as DataBrowserChildrenResponse;
      expect(roots.nodes.map((node) => node.label)).toEqual(["incoming", "outgoing"]);
      expect(JSON.stringify(roots)).not.toContain(storage.password);
      expect(JSON.stringify(roots)).not.toContain("renart-secret");
      const commands: string[] = [];
      page.on("request", (request) => {
        if (
          request.method() === "POST" &&
          /\/(materialize|run|preview)(\/stream)?$/.test(new URL(request.url()).pathname)
        )
          commands.push(request.url());
      });
      await page.goto(`${liveApp.baseURL}${canvas}`);
      await expect(
        page.locator(".react-flow__node").filter({ hasText: "anchor" }).first(),
      ).toBeVisible();
      await openData(page);
      await page
        .getByRole("button", {
          name: new RegExp(`${name}.*${provider === "s3" ? "S3" : "SFTP"}`),
        })
        .click();
      await page.getByRole("button", { name: "incoming", exact: true }).click();
      const verifyWildcardLoad = async () => {
        const pattern = "incoming/order?.csv";
        await page
          .getByRole("textbox", { name: "Search data browser" })
          .fill(`${name}./${pattern}`);
        const patternRow = page.locator('[data-transfer-label="incoming/order?.csv"]');
        await expect(patternRow).toContainText("Matching files");
        await page.screenshot({ path: info.outputPath("wildcard-source-row.png") });
        if (info.project.name.includes("mobile")) {
          await patternRow
            .getByRole("button", { name: `Use ${pattern} in canvas`, exact: true })
            .click();
          await expect(page).toHaveURL(/\/canvas(?:\?|$)/);
          expect(new URL(page.url()).searchParams.has("detail")).toBe(false);
        } else {
          const transfer = await page.evaluateHandle(() => new DataTransfer());
          await patternRow.dispatchEvent("dragstart", { dataTransfer: transfer });
          const target = page.getByRole("button", {
            name: "Create Load in landing group",
            exact: true,
          });
          await expect(target).toBeEnabled();
          await expect(page.getByRole("button", { name: /^Create Load after / })).toHaveCount(0);
          await target.dispatchEvent("drop", { dataTransfer: transfer });
        }
        if (info.project.name.includes("mobile"))
          await page
            .getByRole("button", { name: "Create Load in landing group", exact: true })
            .click();
        const patternDialog = page.getByRole("dialog", { name: "New asset", exact: true });
        await expect(patternDialog.getByLabel("Source table or object")).toHaveValue(
          new RegExp(`^${provider}://.*incoming/order\\?.csv$`),
        );
        await patternDialog.getByLabel("Asset name").fill(`landing.${provider}_pattern`);
        await patternDialog.getByLabel("Destination connection", { exact: true }).click();
        await page.getByRole("option", { name: "duckdb-default", exact: true }).click();
        await patternDialog.getByRole("button", { name: "Create", exact: true }).click();
        await expect(patternDialog).toBeHidden();
        expect(commands).toEqual([]);
        const patternWorkspace = (await (
          await page.request.get(`${liveApp.baseURL}/api/workspace`)
        ).json()) as WorkspaceState;
        const patternAsset = patternWorkspace.pipelines
          .flatMap((p) => p.assets)
          .find((a) => a.name === `landing.${provider}_pattern`)!;
        expect(patternAsset.parameters?.source_table).toMatch(/incoming\/order\?\.csv$/);
        const patternRun = await page.request.post(
          `${liveApp.baseURL}/api/assets/${patternAsset.id}/materialize/stream?environment=default&start_date=2026-09-01T00:00:00Z&end_date=2026-09-02T00:00:00Z&full_refresh=true`,
          { timeout: 90000 },
        );
        const patternOutput = await patternRun.text();
        await info.attach("wildcard-load-output", {
          body: patternOutput,
          contentType: "text/plain",
        });
        expect(patternRun.ok(), patternOutput).toBe(true);
        expect(
          JSON.parse(
            patternOutput
              .split(/\r?\n/)
              .reverse()
              .find((line) => line.startsWith("data: "))!
              .slice(6),
          ).status,
          patternOutput,
        ).toBe("ok");
        const patternQuery = await page.request.post(`${liveApp.baseURL}/api/sql/query`, {
          data: {
            connection: "duckdb-default",
            environment: "default",
            query: `select sum(amount) as total from landing.${provider}_pattern`,
          },
        });
        expect(await patternQuery.json()).toMatchObject({ status: "ok", rows: [{ total: 30 }] });
        const sync = await page.request.post(
          `${liveApp.baseURL}/api/assets/${patternAsset.id}/columns/sync`,
          { data: { environment: "default", additional_sources: ["materialized"] } },
        );
        expect(sync.ok(), await sync.text()).toBe(true);
        const synced = await sync.json();
        expect(synced.status).toBe("applied");
        expect(synced.columns.some((column: { name: string }) => column.name === "amount")).toBe(
          true,
        );
      };
      const useObject = page.getByRole("button", {
        name: "Use orders.csv in canvas",
        exact: true,
      });
      await expect(useObject).toBeVisible();
      if (info.project.name.includes("mobile")) {
        await useObject.click();
      } else {
        const transfer = await page.evaluateHandle(() => new DataTransfer());
        await page
          .locator('[data-transfer-label="orders.csv"]')
          .dispatchEvent("dragstart", { dataTransfer: transfer });
        const target = page.getByRole("button", { name: /Create Load from object/ });
        await expect(target).toBeEnabled();
        const group = page.getByRole("button", {
          name: "Create Load in landing group",
          exact: true,
        });
        const groupBounds = (await group.boundingBox())!;
        await group.dispatchEvent("dragover", {
          dataTransfer: transfer,
          clientX: groupBounds.x + 10,
          clientY: groupBounds.y + 10,
        });
        await expect(group).toHaveAttribute("data-proximity", "near");
        await expect
          .poll(() =>
            group.evaluate((element) => {
              const groupNode = element.closest(".react-flow__node")!;
              return (
                Number(getComputedStyle(groupNode).zIndex) >
                Math.max(
                  ...[...document.querySelectorAll(".react-flow__node-lineageAsset")].map(
                    (node) => Number(getComputedStyle(node).zIndex) || 0,
                  ),
                )
              );
            }),
          )
          .toBe(true);
        await page.screenshot({ path: info.outputPath("expanded-prefix-layer.png") });
        const initial = (await target.boundingBox())!;
        await page.locator("body").dispatchEvent("dragover", {
          dataTransfer: transfer,
          clientX: initial.x - 20,
          clientY: initial.y + initial.height / 2,
        });
        await expect(target).toHaveAttribute("data-proximity", "near");
        await expect
          .poll(async () => (await target.boundingBox())!.height)
          .toBeGreaterThan(initial.height + 8);
        await target.dispatchEvent("drop", { dataTransfer: transfer });
      }
      if (info.project.name.includes("mobile"))
        await page.getByRole("button", { name: /Create Load from object/ }).click();
      const dialog = page.getByRole("dialog", { name: "New asset", exact: true });
      await expect(dialog).toBeVisible();
      await expect(dialog.getByLabel("Source connection", { exact: true })).toContainText(name);
      await expect(dialog.getByLabel("Source table or object")).toHaveValue(
        new RegExp(`^${provider}://.*incoming/orders.csv$`),
      );
      const source = await dialog.getByLabel("Source table or object").inputValue();
      await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
      expect(commands).toEqual([]);
      if (info.project.name.includes("mobile")) await openData(page);
      await useObject.click();
      await expect(
        page.getByRole("button", { name: "Create Load in landing group", exact: true }),
      ).toBeVisible();
      await page.screenshot({ path: info.outputPath("file-prefix-targets.png") });
      await page.getByRole("button", { name: "Create Load in landing group", exact: true }).click();
      await expect(dialog.getByLabel("Asset name")).toHaveValue(/^landing\./);
      await dialog.getByLabel("Asset name").fill(`landing.${provider}_import`);
      await dialog.getByLabel("Destination connection", { exact: true }).click();
      await page.getByRole("option", { name: "duckdb-default", exact: true }).click();
      await dialog.getByRole("button", { name: "Create", exact: true }).click();
      await expect(dialog).toBeHidden();
      expect(commands).toEqual([]);
      const workspace = (await (
        await page.request.get(`${liveApp.baseURL}/api/workspace`)
      ).json()) as WorkspaceState;
      const asset = workspace.pipelines
        .find((p) => p.id === pipelineId)!
        .assets.find((a) => a.name === `landing.${provider}_import`)!;
      expect(asset.parameters?.source_table).toBe(source);
      expect(asset.type).toBe("load");
      const result = await page.request.post(
        `${liveApp.baseURL}/api/assets/${asset.id}/materialize/stream?environment=default&start_date=2026-09-01T00:00:00Z&end_date=2026-09-02T00:00:00Z&full_refresh=true`,
        { timeout: 90000 },
      );
      const output = await result.text();
      await info.attach("sling-transfer-output", { body: output, contentType: "text/plain" });
      expect(result.ok(), output).toBe(true);
      const done = JSON.parse(
        output
          .split(/\r?\n/)
          .reverse()
          .find((line) => line.startsWith("data: "))!
          .slice(6),
      );
      expect(done.status, output).toBe("ok");
      const query = await page.request.post(`${liveApp.baseURL}/api/sql/query`, {
        data: {
          connection: "duckdb-default",
          environment: "default",
          query: `select sum(amount) as total from landing.${provider}_import`,
        },
      });
      expect(await query.json()).toMatchObject({ status: "ok", rows: [{ total: 30 }] });

      // Fresh navigation does not need an old operation token to locate an object.
      const resolved = await page.request.post(`${liveApp.baseURL}/api/data-browser/resolve`, {
        data: {
          environment: "default",
          address: {
            source_kind: "storage",
            connection: name,
            connection_type: provider,
            path: "incoming/orders.csv",
          },
        },
      });
      expect(resolved.ok(), await resolved.text()).toBe(true);
      expect((await resolved.json()).object.capabilities).toMatchObject({
        load_source: true,
        load_destination: true,
      });
      // Mobile keeps the canvas below the asset editor; its desktop-only
      // view switch is intentionally absent.
      if (!info.project.name.includes("mobile"))
        await page.getByRole("link", { name: "Canvas view", exact: true }).click();
      await openData(page);
      // The browser retains its path after authoring, and source references
      // survive asset creation. Navigate up without refreshing connection IDs.
      await page.getByRole("textbox", { name: "Search data browser" }).fill(`${name}./`);
      await page.getByRole("button", { name: "Use outgoing in canvas", exact: true }).click();
      await page
        .getByRole("button", {
          name: `Create Load after landing.${provider}_import`,
          exact: true,
        })
        .click();
      const downstream = page.getByRole("dialog", { name: /New downstream asset/ });
      await expect(downstream.getByLabel("Destination connection", { exact: true })).toContainText(
        name,
      );
      await expect(downstream.getByLabel("Destination object", { exact: true })).toHaveValue(
        new RegExp(`/outgoing/${provider}_import.parquet$`),
      );
      await downstream.getByLabel("Asset name").fill(`analytics.${provider}_export`);
      await downstream.getByRole("button", { name: "Create", exact: true }).click();
      await expect(downstream).toBeHidden();
      const exportedWorkspace = (await (
        await page.request.get(`${liveApp.baseURL}/api/workspace`)
      ).json()) as WorkspaceState;
      const exportAsset = exportedWorkspace.pipelines
        .find((p) => p.id === pipelineId)!
        .assets.find((a) => a.name === `analytics.${provider}_export`)!;
      const exported = await page.request.post(
        `${liveApp.baseURL}/api/assets/${exportAsset.id}/materialize/stream?environment=default&start_date=2026-09-01T00:00:00Z&end_date=2026-09-02T00:00:00Z&full_refresh=true`,
        { timeout: 90000 },
      );
      const exportOutput = await exported.text();
      await info.attach("sling-export-output", { body: exportOutput, contentType: "text/plain" });
      expect(exported.ok(), exportOutput).toBe(true);
      const exportDone = JSON.parse(
        exportOutput
          .split(/\r?\n/)
          .reverse()
          .find((line) => line.startsWith("data: "))!
          .slice(6),
      );
      expect(exportDone.status, exportOutput).toBe("ok");
      const exportedObject = await page.request.post(
        `${liveApp.baseURL}/api/data-browser/resolve`,
        {
          data: {
            environment: "default",
            address: {
              source_kind: "storage",
              connection: name,
              connection_type: provider,
              path: `outgoing/${provider}_import.parquet`,
            },
          },
        },
      );
      expect(exportedObject.ok(), await exportedObject.text()).toBe(true);
      // Test the additional wildcard Load after the original placement checks,
      // so its extra canvas node cannot change their drop-proximity geometry.
      commands.length = 0;
      if (!info.project.name.includes("mobile"))
        await page.getByRole("link", { name: "Canvas view", exact: true }).click();
      await openData(page);
      await verifyWildcardLoad();
      expect(errors).toEqual([]);
      await page.screenshot({ path: info.outputPath(`${provider}-storage-canvas.png`) });
      await observeArrivals(page);
      const config = await (await page.request.get(`${liveApp.baseURL}/api/config`)).json();
      const bookmark = new URL(`${liveApp.baseURL}/data`);
      bookmark.searchParams.set("project", config.project_id);
      bookmark.searchParams.set(
        "detail",
        JSON.stringify({
          v: 1,
          environment: "default",
          target: {
            kind: "data-object",
            section: "schema",
            address: {
              source_kind: "storage",
              connection: name,
              connection_type: provider,
              path: "incoming/orders.csv",
            },
          },
        }),
      );
      await page.goto(bookmark.href);
      const metadata = page.getByLabel("Data object details", { exact: true });
      await expect(metadata).toBeFocused();
      await expect(metadata).toContainText("orders.csv");
      await expect
        .poll(() => arrivals(page))
        .toEqual([expect.objectContaining({ label: "Data object details", visible: true })]);
      expect(commands).toEqual([]);
    });
  }
});
