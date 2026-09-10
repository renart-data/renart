import { expect } from "@playwright/test";
import { createServer } from "node:http";
import { liveTest as test } from "../live-app-fixture";

test.use({ fixtureName: "configured-workspace", isolateUserConfig: true });

// Protocol fixture, not a warehouse simulation: real Renart HTTP handlers and
// the pinned Trino Go driver execute against deterministic metadata responses.
test("catalog browsing stays lazy and retains identity through links and previews", async ({
  page,
  liveApp,
  isMobile,
}, info) => {
  test.setTimeout(90000);
  const queries: string[] = [];
  const unexpected: string[] = [];
  const headers: string[] = [];
  const results = new Map<string, unknown>();
  const server = createServer(async (req, res) => {
    res.setHeader("Content-Type", "application/json");
    if (req.method === "GET") {
      const result = results.get(req.url!);
      results.delete(req.url!);
      res.end(JSON.stringify(result));
      return;
    }
    if (req.method === "DELETE") {
      res.end("{}");
      return;
    }
    let sql = "";
    for await (const chunk of req) sql += chunk.toString();
    sql = sql.trim().replace(/;$/, "");
    queries.push(sql);
    headers.push(String(req.headers["x-trino-catalog"] ?? ""));
    let names = ["name"];
    let data: unknown[][] = [];
    if (/^SHOW CATALOGS$/i.test(sql)) data = [["lake"], ["warehouse"]];
    else if (/^SHOW SCHEMAS FROM "(lake|warehouse)"$/i.test(sql)) data = [["empty"], ["sales"]];
    else if (/^SHOW TABLES FROM "(lake|warehouse)"\."sales"$/i.test(sql)) data = [["orders"]];
    else if (/^SHOW TABLES FROM "(lake|warehouse)"\."empty"$/i.test(sql)) data = [];
    else if (/information_schema.views/i.test(sql)) names = ["view_definition"];
    else if (/\bfrom\s+"?(lake|warehouse)"?\."?sales"?\."?orders"?/i.test(sql)) {
      const catalog = /\bfrom\s+"?(lake|warehouse)/i.exec(sql)![1];
      names = [`${catalog}_id`];
      data = [[catalog === "lake" ? "11" : "22"]];
    } else {
      unexpected.push(sql);
    }
    const resultPath = `/result/${queries.length}`;
    results.set(resultPath, {
      id: `catalog-test-${queries.length}`,
      infoUri: "http://localhost/fixture",
      stats: { state: "FINISHED" },
      columns: names.map((name) => ({
        name,
        type: "varchar",
        typeSignature: { rawType: "varchar", arguments: [] },
      })),
      data,
    });
    res.end(
      JSON.stringify({
        id: `catalog-test-${queries.length}`,
        nextUri: `http://${req.headers.host}${resultPath}`,
        stats: { state: "RUNNING" },
      }),
    );
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const port = (server.address() as { port: number }).port;
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  const pipelineId = Buffer.from("analytics").toString("base64url");
  try {
    const created = await page.request.post(`${liveApp.baseURL}/api/config/connections`, {
      data: {
        name: "catalog-demo",
        type: "trino",
        environment_name: "default",
        values: { host: "127.0.0.1", port, username: "fixture", catalog: "lake", schema: "sales" },
      },
    });
    expect(created.ok(), await created.text()).toBe(true);
    await page.goto(
      `${liveApp.baseURL}/pipelines/${pipelineId}/canvas?result=inspect&editor=asset`,
    );
    if (isMobile) await page.getByRole("tab", { name: "Data", exact: true }).click();
    else await page.getByRole("button", { name: "Data Browser", exact: true }).click();
    await page.getByRole("button", { name: /catalog-demo.*Trino/ }).click();
    const lake = page.getByRole("button", { name: "lake Default", exact: true });
    await expect(lake).toBeVisible();
    expect(queries).toEqual(["SHOW CATALOGS"]);
    await lake.click();
    await expect(page.getByRole("button", { name: "empty", exact: true })).toBeVisible();
    expect(queries).toEqual(["SHOW CATALOGS", 'SHOW SCHEMAS FROM "lake"']);
    await page.getByRole("button", { name: "sales", exact: true }).click();
    const orders = page.getByRole("link", { name: "orders", exact: true });
    await expect(orders).toBeVisible();
    expect(queries).toHaveLength(3);
    const href = await orders.getAttribute("href");
    expect(href).toBeTruthy();
    await orders.click();
    await expect(page.getByText("lake_id", { exact: true }).first()).toBeVisible();
    expect(new URL(page.url()).searchParams.get("result")).toBe("inspect");
    const reopened = await page.context().newPage();
    await reopened.goto(new URL(href!, liveApp.baseURL).href);
    await expect(reopened.getByText("lake_id", { exact: true }).first()).toBeVisible();
    await reopened.close();

    const resolve = await page.request.post(`${liveApp.baseURL}/api/data-browser/resolve`, {
      data: {
        environment: "default",
        address: {
          source_kind: "warehouse",
          connection: "catalog-demo",
          connection_type: "trino",
          catalog: "warehouse",
          schema: "sales",
          name: "orders",
        },
      },
    });
    expect(resolve.ok(), await resolve.text()).toBe(true);
    const { object } = await resolve.json();
    expect(object.reference_text).toBe("warehouse.sales.orders");
    expect(object.columns[0].name).toBe("warehouse_id");
    const preview = await page.request.post(`${liveApp.baseURL}/api/data-browser/preview`, {
      data: { object_id: object.id, environment: "default" },
    });
    expect(preview.ok(), await preview.text()).toBe(true);
    expect((await preview.json()).rows[0].warehouse_id).toBe("22");
    const source = await page.request.post(
      `${liveApp.baseURL}/api/pipelines/${pipelineId}/data-browser/sources/preview`,
      { data: { object_id: object.id, environment: "default" } },
    );
    // Catalog discovery must not advertise authoring capabilities absent from
    // the native engine. Source creation is covered with native DuckDB below
    // and StarRocks service tests, not invented for Trino by this fixture.
    expect(source.status()).toBe(400);
    expect((await source.json()).error.code).toBe("source_connection_unsupported");
    expect(queries.every((sql) => !/^(USE|SET)\b/i.test(sql))).toBe(true);
    expect(headers.every((catalog) => catalog === "lake")).toBe(true);
    expect(unexpected).toEqual([]);
    expect(errors).toEqual([]);
    await page.screenshot({ path: info.outputPath("catalog-browser.png") });
  } finally {
    await info.attach("trino-protocol", {
      body: JSON.stringify({ queries, unexpected, headers }, null, 2),
      contentType: "application/json",
    });
    server.closeAllConnections();
    await new Promise<void>((resolve) => server.close(() => resolve()));
  }
});
