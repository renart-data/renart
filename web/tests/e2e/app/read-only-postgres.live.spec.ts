import { expect } from "@playwright/test";
import { mkdir, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { liveTest as test } from "../live-app-fixture";

test.describe("Read-only PostgreSQL integration", () => {
  test.use({
    fixtureName: "notebook-postgres-workspace",
    isolateUserConfig: true,
    liveAppEnv: { RENART_READ_ONLY_TEST_PASSWORD: "isolated-test-only" },
  });
  // This tests server/driver behavior with real database grants, not device UI.
  test("copies through a SELECT-only role without changing its source @desktop-only", async ({
    page,
    liveApp,
    livePostgres,
  }) => {
    test.skip(!livePostgres, "Requires the isolated PostgreSQL Docker fixture.");
    test.setTimeout(180000);
    const sql = async (connection: string, query: string) => {
      const response = await page.request.post(`${liveApp.baseURL}/api/sql/query`, {
        data: { connection, environment: "default", query },
      });
      return response.json();
    };
    for (const statement of [
      "create table analytics.read_only_orders as select 42::integer as id",
      "create role renart_read_only_test login password 'isolated-test-only'",
      "grant usage on schema analytics to renart_read_only_test",
      "grant select on analytics.read_only_orders to renart_read_only_test",
    ]) {
      const result = await sql("postgres-orders", statement);
      expect(result.status, JSON.stringify(result)).toBe("ok");
    }
    const pg = livePostgres!;
    const source = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
      data: {
        environment_name: "default",
        name: "postgres-read-only",
        type: "postgres",
        access_mode: "read_only",
        values: {
          host: pg.host,
          port: pg.port,
          username: "renart_read_only_test",
          database: pg.database,
          schema: "analytics",
          ssl_mode: "disable",
        },
        secret_changes: {
          password: { action: "replace", binding: { ref: "env:RENART_READ_ONLY_TEST_PASSWORD" } },
        },
      },
    });
    expect(source.ok(), await source.text()).toBe(true);
    const output = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
      data: {
        environment_name: "default",
        name: "duckdb-output",
        type: "duckdb",
        values: { path: "duckdb-files/copy.db" },
      },
    });
    expect(output.ok(), await output.text()).toBe(true);
    const denied = await sql("postgres-read-only", "delete from analytics.read_only_orders");
    expect(JSON.stringify(denied)).toContain("read-only");
    const grants = await sql(
      "postgres-orders",
      "select has_table_privilege('renart_read_only_test', 'analytics.read_only_orders', 'SELECT') as can_read, has_table_privilege('renart_read_only_test', 'analytics.read_only_orders', 'INSERT,UPDATE,DELETE') as can_write, has_schema_privilege('renart_read_only_test', 'analytics', 'CREATE') as can_create",
    );
    expect(grants.status, JSON.stringify(grants)).toBe("ok");
    expect(grants.rows).toEqual([{ can_read: true, can_write: false, can_create: false }]);
    const pipeline = await page.request.post(`${liveApp.baseURL}/api/pipelines`, {
      data: { path: "readonly", name: "readonly" },
    });
    expect(pipeline.ok(), await pipeline.text()).toBe(true);
    await mkdir(join(liveApp.workspaceDir, "readonly/assets"), { recursive: true });
    const assetPath = "readonly/assets/copy.asset.yml";
    await writeFile(
      join(liveApp.workspaceDir, assetPath),
      "name: copied_orders\ntype: load\nconnection: duckdb-output\nparameters:\n  source_connection: postgres-read-only\n  source_table: analytics.read_only_orders\nmaterialization:\n  type: table\n  strategy: create+replace\n",
    );
    const id = Buffer.from(assetPath).toString("base64url");
    const run = await page.request.post(
      `${liveApp.baseURL}/api/assets/${id}/materialize/stream?environment=default`,
      { timeout: 120000 },
    );
    const stream = await run.text();
    expect(run.ok(), stream).toBe(true);
    const final = JSON.parse(
      stream
        .split(/\r?\n/)
        .reverse()
        .find((line) => line.startsWith("data: "))!
        .slice(6),
    );
    expect(final.status, stream).toBe("ok");
    expect((await sql("duckdb-output", "select id from copied_orders")).rows).toEqual([{ id: 42 }]);
    expect(
      (await sql("postgres-orders", "select id from analytics.read_only_orders")).rows,
    ).toEqual([{ id: 42 }]);
  });
});
