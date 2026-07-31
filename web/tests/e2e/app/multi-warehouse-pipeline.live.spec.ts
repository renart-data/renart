import { expect, type Page } from "@playwright/test";
import { createServer, type Server } from "node:http";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";

import { liveTest, type LiveApp } from "../live-app-fixture";
import { createLiveWarehouseMatrix, type LiveWarehouseMatrix } from "../live-warehouse-matrix";

type WarehouseVariant = {
  name: "duckdb" | "ducklake" | "postgres" | "trino" | "clickhouse" | "starrocks" | "databricks";
  connection: string;
  defaultConnectionType: string;
  seedType: string;
  sensorType: string;
  sqlType: string;
  supportsIncrementalSQL?: boolean;
};

type WorkspaceResponse = {
  pipelines: Array<{
    id: string;
    name: string;
    assets: Array<{ id: string; name: string; type: string; connection?: string }>;
  }>;
};

type FinalRow = {
  activity_score: number;
  api_multiplier: number;
  customer_count: number;
  gross_amount: number;
  order_count: number;
  region_name: string;
  segment: string;
  weighted_amount: number;
};

const pipelineName = "warehouse_matrix";
const pipelinePath = "warehouse-matrix";
const finalAssetPath = `${pipelinePath}/assets/analytics/final_report.sql`;
const finalAssetId = Buffer.from(finalAssetPath).toString("base64url");

const runWindows = [
  {
    start: "2026-02-03T00:00:00Z",
    end: "2026-02-04T00:00:00Z",
    date: "2026-02-03",
    fullRefresh: true,
  },
  {
    start: "2026-03-10T00:00:00Z",
    end: "2026-03-11T00:00:00Z",
    date: "2026-03-10",
    fullRefresh: false,
  },
] as const;

const variants: WarehouseVariant[] = [
  {
    name: "duckdb",
    connection: "duckdb-matrix",
    defaultConnectionType: "duckdb",
    seedType: "duckdb.seed",
    sensorType: "duckdb.sensor.query",
    sqlType: "duckdb.sql",
  },
  {
    name: "ducklake",
    connection: "ducklake-matrix",
    defaultConnectionType: "duckdb",
    seedType: "duckdb.seed",
    sensorType: "duckdb.sensor.query",
    sqlType: "duckdb.sql",
  },
  {
    name: "postgres",
    connection: "postgres-matrix",
    defaultConnectionType: "postgres",
    seedType: "pg.seed",
    sensorType: "pg.sensor.query",
    sqlType: "pg.sql",
  },
  {
    name: "trino",
    connection: "trino-matrix",
    defaultConnectionType: "trino",
    seedType: "trino.seed",
    sensorType: "trino.sensor.query",
    sqlType: "trino.sql",
    // The lightweight memory catalog exercises Trino queries but deliberately
    // has no data-lake table properties. Bruin's Trino table materializer
    // targets Hive/Iceberg-style catalogs and emits `format = 'PARQUET'`.
    supportsIncrementalSQL: false,
  },
  {
    name: "clickhouse",
    connection: "clickhouse-matrix",
    defaultConnectionType: "clickhouse",
    seedType: "clickhouse.seed",
    sensorType: "clickhouse.sensor.query",
    sqlType: "clickhouse.sql",
  },
  {
    name: "starrocks",
    connection: "starrocks-matrix",
    defaultConnectionType: "starrocks",
    seedType: "starrocks.seed",
    sensorType: "starrocks.sensor.query",
    sqlType: "starrocks.sql",
  },
  {
    name: "databricks",
    connection: "databricks-matrix",
    defaultConnectionType: "databricks",
    seedType: "databricks.seed",
    sensorType: "databricks.sensor.query",
    sqlType: "databricks.sql",
  },
];

const requestedVariants = new Set(
  (process.env.RENART_E2E_WAREHOUSES ?? "")
    .split(",")
    .map((name) => name.trim())
    .filter(Boolean),
);
const selectedVariants =
  requestedVariants.size === 0
    ? variants
    : variants.filter((variant) => requestedVariants.has(variant.name));

const test = liveTest.extend<{ liveWarehouseMatrix: LiveWarehouseMatrix }>({
  liveWarehouseMatrix: [
    async (_fixtures, use) => {
      const matrix = await createLiveWarehouseMatrix(
        selectedVariants.map((variant) => variant.name),
      );
      try {
        await use(matrix);
      } finally {
        await matrix.dispose();
      }
    },
    { timeout: 10 * 60_000 },
  ],
  liveAppEnv: async ({ liveWarehouseMatrix }, use) => {
    await use(
      liveWarehouseMatrix.databricksTrustBundle
        ? {
            SSL_CERT_FILE: liveWarehouseMatrix.databricksTrustBundle,
            UV_NATIVE_TLS: "true",
          }
        : {},
    );
  },
});

const expectedRows: FinalRow[] = [
  {
    activity_score: 13,
    api_multiplier: 2,
    customer_count: 2,
    gross_amount: 220,
    order_count: 3,
    region_name: "North",
    segment: "enterprise",
    weighted_amount: 440,
  },
  {
    activity_score: 6,
    api_multiplier: 3,
    customer_count: 1,
    gross_amount: 90,
    order_count: 1,
    region_name: "South",
    segment: "enterprise",
    weighted_amount: 270,
  },
  {
    activity_score: 15,
    api_multiplier: 3,
    customer_count: 2,
    gross_amount: 180,
    order_count: 3,
    region_name: "South",
    segment: "self_serve",
    weighted_amount: 540,
  },
];

test.describe("multi-warehouse pipeline live", () => {
  test.use({ fixtureName: "empty-workspace", isolateUserConfig: true });
  test.skip(
    ({ isMobile }) => Boolean(isMobile),
    "The backend warehouse matrix only needs one browser project.",
  );

  test("produces identical results across the live warehouse matrix", async ({
    liveApp,
    liveWarehouseMatrix,
    page,
  }) => {
    test.setTimeout(30 * 60_000);
    const api = await startRegionsAPI();
    try {
      await writeConnections(liveApp, liveWarehouseMatrix);

      const results = new Map<string, FinalRow[]>();
      for (const variant of selectedVariants) {
        await writePipelineVariant(liveApp, variant, api.url);
        const pipeline = await waitForPipelineVariant(page, liveApp.baseURL, variant);
        for (const window of runWindows) {
          const done = await materializePipeline(page, liveApp.baseURL, pipeline.id, window);
          expect(
            done.status,
            `${variant.name} ${window.date}: ${done.error ?? done.output}\n${done.raw}`,
          ).toBe("ok");
          const expectedAssetNames = [
            "analytics.branch_a",
            "analytics.branch_b",
            "analytics.customers_seed",
            "analytics.customer_activity",
            "analytics.regions_api",
            "analytics.enriched_orders",
            "analytics.orders_ready",
            "analytics.segment_metrics",
            "analytics.final_report",
          ];
          if (variant.supportsIncrementalSQL !== false) {
            expectedAssetNames.push("analytics.run_audit", "analytics.window_metrics");
          }
          for (const assetName of expectedAssetNames) {
            expect(
              done.output,
              `${variant.name} ${window.date} did not report ${assetName}`,
            ).toContain(assetName);
          }
        }

        const rows = await inspectFinalRows(page, liveApp.baseURL);
        expect(rows, `${variant.name} produced unexpected rows`).toEqual(expectedRows);
        if (variant.supportsIncrementalSQL !== false) {
          const expectedDates = runWindows.map((window) => window.date);
          expect(
            await inspectMaterializationDates(
              page,
              liveApp.baseURL,
              variant.connection,
              "analytics.run_audit",
              "run_date",
            ),
            `${variant.name} append materialization did not retain both run dates`,
          ).toEqual(expectedDates);
          expect(
            await inspectMaterializationDates(
              page,
              liveApp.baseURL,
              variant.connection,
              "analytics.window_metrics",
              "window_date",
            ),
            `${variant.name} windowed incremental materialization did not retain both windows`,
          ).toEqual(expectedDates);
        }
        results.set(variant.name, rows);
      }

      expect(Object.fromEntries(results)).toEqual(
        Object.fromEntries(selectedVariants.map((variant) => [variant.name, expectedRows])),
      );
      expect(api.requests).toBe(selectedVariants.length * runWindows.length);
    } finally {
      await new Promise<void>((resolveClose) => api.server.close(() => resolveClose()));
    }
  });
});

async function writeConnections(liveApp: LiveApp, warehouses: LiveWarehouseMatrix) {
  const duckLakeCatalogDir = join(liveApp.workspaceDir, "duckdb-files");
  await mkdir(duckLakeCatalogDir, { recursive: true });
  const duckLakeCatalogPath = join(duckLakeCatalogDir, "ducklake-catalog.duckdb").replaceAll(
    "\\",
    "/",
  );
  await writeFile(
    join(liveApp.workspaceDir, ".bruin.yml"),
    `default_environment: default
environments:
  default:
    connections:
      duckdb:
        - name: duckdb-matrix
          path: duckdb-files/warehouse-matrix.duckdb
        - name: ducklake-matrix
          path: duckdb-files/ducklake-matrix.duckdb
          lakehouse:
            format: ducklake
            catalog:
              type: duckdb
              path: ${JSON.stringify(duckLakeCatalogPath)}
            storage:
              type: s3
              path: s3://renart-ducklake/warehouse
              region: us-east-1
              endpoint: 127.0.0.1:${warehouses.minioPort}
              url_style: path
              use_ssl: false
              auth:
                access_key: renart
                secret_key: renart-secret
      postgres:
        - name: postgres-matrix
          host: 127.0.0.1
          port: ${warehouses.postgresPort}
          username: postgres
          password: postgres
          database: renart_postgres
          schema: analytics
          ssl_mode: disable
        - name: postgres-load-source
          host: 127.0.0.1
          port: ${warehouses.postgresPort}
          username: postgres
          password: postgres
          database: renart_source
          schema: analytics
          ssl_mode: disable
      trino:
        - name: trino-matrix
          host: 127.0.0.1
          port: ${warehouses.trinoPort}
          username: renart
          catalog: memory
          schema: analytics
      clickhouse:
        - name: clickhouse-matrix
          host: 127.0.0.1
          port: ${warehouses.clickhouseNativePort}
          http_port: ${warehouses.clickhouseHTTPPort}
          username: renart
          password: renart
          database: analytics
          secure: 0
      starrocks:
        - name: starrocks-matrix
          host: 127.0.0.1
          port: ${warehouses.starrocksMySQLPort}
          # The all-in-one image advertises its backend on container port 8040.
          # Use the mapped backend endpoint directly so Stream Load does not
          # follow a container-internal redirect from the host test process.
          http_port: ${warehouses.starrocksStreamLoadPort}
          username: root
          database: analytics
          replication_num: 1
      databricks:
        - name: databricks-matrix
          token: local-e2e-token
          host: 127.0.0.1
          port: ${warehouses.databricksPort}
          path: /sql/1.0/warehouses/renart-e2e
          catalog: sail
          schema: analytics
`,
    "utf8",
  );
}

async function writePipelineVariant(liveApp: LiveApp, variant: WarehouseVariant, apiURL: string) {
  const assetDir = join(liveApp.workspaceDir, pipelinePath, "assets", "analytics");
  await mkdir(assetDir, { recursive: true });
  await writeFile(
    join(liveApp.workspaceDir, pipelinePath, "pipeline.yml"),
    `id: 30f978bb-0697-47ea-a243-75f550279718
name: ${pipelineName}
schedule: daily
start_date: "2026-01-01"
concurrency: 3
max_active_steps: 4

default_connections:
  ${variant.defaultConnectionType}: ${variant.connection}
`,
    "utf8",
  );
  for (const branch of ["a", "b"]) {
    await writeFile(
      join(assetDir, `branch_${branch}.sql`),
      `/* @bruin
name: analytics.branch_${branch}
type: ${variant.sqlType}
connection: ${variant.connection}
materialization:
  type: view
@bruin */

select '${branch}' as branch_name, ${branch === "a" ? 1 : 2} as branch_value
`,
      "utf8",
    );
  }
  await writeFile(
    join(assetDir, "customers.csv"),
    `customer_id,customer_name,segment,region_id
1,Ada,enterprise,10
2,Grace,self_serve,20
3,Lin,enterprise,10
4,Turing,self_serve,20
5,Hopper,enterprise,20
`,
    "utf8",
  );
  await writeFile(
    join(assetDir, "customers_seed.asset.yml"),
    `name: analytics.customers_seed
type: ${variant.seedType}
connection: ${variant.connection}

parameters:
  path: ./customers.csv
  file_type: csv
  enforce_schema: true

materialization:
  type: table
  strategy: truncate+insert

columns:
  - name: customer_id
    type: integer
    primary_key: true
  - name: customer_name
    type: string
  - name: segment
    type: string
  - name: region_id
    type: integer
`,
    "utf8",
  );
  await writeFile(
    join(assetDir, "customer_activity.asset.yml"),
    `name: analytics.customer_activity
type: load
connection: ${variant.connection}
depends:
  - analytics.customers_seed

parameters:
  source_connection: postgres-load-source
  source_table: analytics.customer_activity_source

materialization:
  type: table
  strategy: truncate+insert
`,
    "utf8",
  );
  await writeFile(
    join(assetDir, "regions_api.asset.yml"),
    `name: analytics.regions_api
type: api
connection: ${variant.connection}

parameters:
  request:
    url: ${apiURL}/regions
    method: GET
  response:
    records_path: data
    fields:
      region_id: region_id
      region_name: region_name
      multiplier: multiplier

columns:
  - name: region_id
    type: integer
    primary_key: true
  - name: region_name
    type: string
  - name: multiplier
    type: integer

materialization:
  type: table
  strategy: truncate+insert
`,
    "utf8",
  );
  await writeFile(
    join(assetDir, "enriched_orders.sql"),
    `/* @bruin
name: analytics.enriched_orders
type: ${variant.sqlType}
connection: ${variant.connection}
depends:
  - analytics.customers_seed
  - analytics.customer_activity
  - analytics.regions_api
materialization:
  type: view
@bruin */

select
  orders.order_id as order_id,
  customers.customer_id as customer_id,
  customers.segment as segment,
  customers.region_id as region_id,
  regions.region_name as region_name,
  activity.activity_score as activity_score,
  orders.amount as amount,
  regions.multiplier as multiplier
from (
  select 1001 as order_id, 1 as customer_id, 100 as amount
  union all select 1002, 1, 50
  union all select 1003, 2, 80
  union all select 1004, 3, 70
  union all select 1005, 4, 40
  union all select 1006, 4, 60
  union all select 1007, 5, 90
) as orders
join analytics.customers_seed as customers on customers.customer_id = orders.customer_id
join analytics.customer_activity as activity on activity.customer_id = customers.customer_id
join analytics.regions_api as regions on regions.region_id = customers.region_id
`,
    "utf8",
  );
  await writeFile(
    join(assetDir, "orders_ready.asset.yml"),
    `name: analytics.orders_ready
type: ${variant.sensorType}
connection: ${variant.connection}
depends:
  - analytics.enriched_orders

parameters:
  query: select count(*) from analytics.enriched_orders
  poke_interval: 1
  timeout: 30s
`,
    "utf8",
  );
  await writeFile(
    join(assetDir, "segment_metrics.py"),
    `""" @bruin
name: analytics.segment_metrics
type: python
connection: ${variant.connection}
depends:
  - analytics.customers_seed
  - analytics.customer_activity
  - analytics.regions_api
  - analytics.enriched_orders
  - analytics.orders_ready
materialization:
  type: table
  strategy: truncate+insert
@bruin """

from collections import defaultdict

import pyarrow as pa
from renart import query


def materialize():
    customers = query(
        "select cast(customer_id as bigint) as customer_id_value, segment, "
        "cast(region_id as bigint) as region_id_value "
        "from analytics.customers_seed order by customer_id",
        connection="${variant.connection}",
    ).to_pylist()
    activity = query(
        "select cast(customer_id as bigint) as customer_id_value, "
        "cast(activity_score as bigint) as activity_score_value "
        "from analytics.customer_activity order by customer_id",
        connection="${variant.connection}",
    ).to_pylist()
    regions = query(
        "select cast(region_id as bigint) as region_id_value, region_name, "
        "cast(multiplier as bigint) as multiplier_value "
        "from analytics.regions_api order by region_id",
        connection="${variant.connection}",
    ).to_pylist()
    orders = query(
        "select cast(order_id as bigint) as order_id_value, "
        "cast(customer_id as bigint) as customer_id_value, segment, "
        "cast(region_id as bigint) as region_id_value, region_name, "
        "cast(activity_score as bigint) as activity_score_value, "
        "cast(amount as bigint) as amount_value, "
        "cast(multiplier as bigint) as multiplier_value "
        "from analytics.enriched_orders order by order_id",
        connection="${variant.connection}",
    ).to_pylist()

    region_by_id = {
        int(row["region_id_value"]): (
            str(row["region_name"]),
            int(row["multiplier_value"]),
        )
        for row in regions
    }
    activity_by_customer = {
        int(row["customer_id_value"]): int(row["activity_score_value"])
        for row in activity
    }
    customers_by_group = defaultdict(set)
    for row in customers:
        region_name, _ = region_by_id[int(row["region_id_value"])]
        customers_by_group[(str(row["segment"]), region_name)].add(
            int(row["customer_id_value"])
        )

    metrics = defaultdict(
        lambda: {
            "order_count": 0,
            "gross_amount": 0,
            "weighted_amount": 0,
            "activity_score": 0,
        }
    )
    for row in orders:
        key = (str(row["segment"]), str(row["region_name"]))
        metrics[key]["order_count"] += 1
        metrics[key]["gross_amount"] += int(row["amount_value"])
        metrics[key]["weighted_amount"] += int(row["amount_value"]) * int(
            row["multiplier_value"]
        )
        metrics[key]["activity_score"] += activity_by_customer[
            int(row["customer_id_value"])
        ]

    keys = sorted(metrics)
    return pa.table(
        {
            "segment": pa.array([key[0] for key in keys], type=pa.string()),
            "region_name": pa.array([key[1] for key in keys], type=pa.string()),
            "customer_count": pa.array(
                [len(customers_by_group[key]) for key in keys], type=pa.int64()
            ),
            "order_count": pa.array(
                [metrics[key]["order_count"] for key in keys], type=pa.int64()
            ),
            "gross_amount": pa.array(
                [metrics[key]["gross_amount"] for key in keys], type=pa.int64()
            ),
            "weighted_amount": pa.array(
                [metrics[key]["weighted_amount"] for key in keys], type=pa.int64()
            ),
            "activity_score": pa.array(
                [metrics[key]["activity_score"] for key in keys], type=pa.int64()
            ),
        }
    )
`,
    "utf8",
  );
  const runAuditPath = join(assetDir, "run_audit.sql");
  const windowMetricsPath = join(assetDir, "window_metrics.sql");
  if (variant.supportsIncrementalSQL === false) {
    await Promise.all([rm(runAuditPath, { force: true }), rm(windowMetricsPath, { force: true })]);
  } else {
    // StarRocks 3.5 rejects the predicates currently emitted by Bruin's
    // time_interval and delete+insert materializers. Its native primary-key
    // merge is the supported incremental replacement path for this variant.
    const windowMaterialization =
      variant.name === "starrocks"
        ? "  strategy: merge"
        : "  strategy: time_interval\n  incremental_key: window_date\n  time_granularity: date";
    await writeFile(
      runAuditPath,
      `/* @bruin
name: analytics.run_audit
type: ${variant.sqlType}
connection: ${variant.connection}
materialization:
  type: table
  strategy: append
columns:
  - name: run_date
    type: date
    primary_key: true
  - name: completed_batch
    type: integer
@bruin */

select
  cast('{{ start_date }}' as date) as run_date,
  1 as completed_batch
`,
      "utf8",
    );
    await writeFile(
      windowMetricsPath,
      `/* @bruin
name: analytics.window_metrics
type: ${variant.sqlType}
connection: ${variant.connection}
materialization:
  type: table
${windowMaterialization}
columns:
  - name: window_date
    type: date
    primary_key: true
  - name: materialized_rows
    type: integer
@bruin */

select
  cast('{{ start_date }}' as date) as window_date,
  1 as materialized_rows
`,
      "utf8",
    );
  }
  await writeFile(
    join(assetDir, "final_report.sql"),
    `/* @bruin
name: analytics.final_report
type: ${variant.sqlType}
connection: ${variant.connection}
depends:
  - analytics.segment_metrics
  - analytics.regions_api
  - analytics.branch_a
  - analytics.branch_b
materialization:
  type: view
@bruin */

select
  metrics.segment as segment,
  metrics.region_name as region_name,
  metrics.customer_count as customer_count,
  metrics.order_count as order_count,
  metrics.gross_amount as gross_amount,
  metrics.weighted_amount as weighted_amount,
  metrics.activity_score as activity_score,
  regions.multiplier as api_multiplier
from analytics.segment_metrics as metrics
join analytics.regions_api as regions on regions.region_name = metrics.region_name
order by metrics.segment, metrics.region_name
`,
    "utf8",
  );
}

async function waitForPipelineVariant(page: Page, baseURL: string, variant: WarehouseVariant) {
  const expectedTypes = [
    "api",
    "load",
    "python",
    variant.seedType,
    variant.sensorType,
    variant.sqlType,
    variant.sqlType,
    variant.sqlType,
    variant.sqlType,
  ].sort();
  if (variant.supportsIncrementalSQL !== false) {
    expectedTypes.push(variant.sqlType, variant.sqlType);
    expectedTypes.sort();
  }
  let found: WorkspaceResponse["pipelines"][number] | undefined;
  await expect
    .poll(
      async () => {
        const response = await page.request.get(`${baseURL}/api/workspace`);
        if (!response.ok()) return "";
        const workspace = (await response.json()) as WorkspaceResponse;
        found = workspace.pipelines.find((pipeline) => pipeline.name === pipelineName);
        if (!found) return "";
        const types = found.assets.map((asset) => asset.type).sort();
        const connections = new Set(
          found.assets
            .map((asset) => asset.connection)
            .filter((value): value is string => Boolean(value)),
        );
        return JSON.stringify({ types, connections: [...connections].sort() });
      },
      { timeout: 60_000 },
    )
    .toBe(JSON.stringify({ types: expectedTypes, connections: [variant.connection] }));
  return found!;
}

async function materializePipeline(
  page: Page,
  baseURL: string,
  pipelineId: string,
  window: (typeof runWindows)[number],
) {
  const query = new URLSearchParams({
    environment: "default",
    sensor_mode: "once",
    start_date: window.start,
    end_date: window.end,
    full_refresh: String(window.fullRefresh),
  });
  const response = await page.request.post(
    `${baseURL}/api/pipelines/${pipelineId}/materialize/stream?${query.toString()}`,
    { timeout: 12 * 60_000 },
  );
  const text = await response.text();
  expect(response.ok(), text).toBe(true);
  const dataLine = text
    .split(/\r?\n/)
    .reverse()
    .find((line) => line.startsWith("data: "));
  if (!dataLine) throw new Error(`Pipeline stream did not contain a done event:\n${text}`);
  return {
    ...(JSON.parse(dataLine.slice("data: ".length)) as {
      status: string;
      output: string;
      error?: string;
    }),
    raw: text,
  };
}

async function inspectMaterializationDates(
  page: Page,
  baseURL: string,
  connection: string,
  relation: string,
  field: string,
): Promise<string[]> {
  const response = await page.request.post(`${baseURL}/api/sql/query`, {
    data: {
      connection,
      environment: "default",
      query: `select ${field} from ${relation} order by ${field}`,
      limit: 20,
    },
    timeout: 120_000,
  });
  const body = (await response.json()) as {
    status: string;
    rows?: Array<Record<string, unknown>>;
    error?: string;
  };
  expect(response.ok(), body.error).toBe(true);
  expect(body.status, body.error).toBe("ok");
  return (body.rows ?? [])
    .map((row) => String(row[field]).slice(0, 10))
    .sort((left, right) => left.localeCompare(right));
}

async function inspectFinalRows(page: Page, baseURL: string): Promise<FinalRow[]> {
  const response = await page.request.get(
    `${baseURL}/api/assets/${finalAssetId}/inspect?environment=default&limit=20`,
    { timeout: 120_000 },
  );
  const body = (await response.json()) as {
    status: string;
    rows?: Array<Record<string, unknown>>;
    error?: string;
  };
  expect(response.ok(), body.error).toBe(true);
  expect(body.status, body.error).toBe("ok");
  return (body.rows ?? [])
    .map((row) => ({
      activity_score: numericInspectField(row, "activity_score"),
      api_multiplier: numericInspectField(row, "api_multiplier"),
      customer_count: numericInspectField(row, "customer_count"),
      gross_amount: numericInspectField(row, "gross_amount"),
      order_count: numericInspectField(row, "order_count"),
      region_name: String(row.region_name),
      segment: String(row.segment),
      weighted_amount: numericInspectField(row, "weighted_amount"),
    }))
    .sort((left, right) =>
      `${left.segment}:${left.region_name}`.localeCompare(`${right.segment}:${right.region_name}`),
    );
}

function numericInspectField(row: Record<string, unknown>, field: string) {
  const numeric = Number(row[field]);
  if (Number.isNaN(numeric)) {
    throw new Error(
      `Inspect field ${field} is not numeric: ${JSON.stringify(row[field])}; row=${JSON.stringify(row)}`,
    );
  }
  return numeric;
}

async function startRegionsAPI(): Promise<{ server: Server; url: string; requests: number }> {
  const state = { requests: 0 };
  const server = createServer((request, response) => {
    if (request.url !== "/regions") {
      response.writeHead(404).end();
      return;
    }
    state.requests += 1;
    response.setHeader("content-type", "application/json");
    response.end(
      JSON.stringify({
        data: [
          { region_id: 10, region_name: "North", multiplier: 2 },
          { region_id: 20, region_name: "South", multiplier: 3 },
          { region_id: 30, region_name: "West", multiplier: 4 },
        ],
      }),
    );
  });
  await new Promise<void>((resolveListen) => server.listen(0, "127.0.0.1", resolveListen));
  const address = server.address();
  if (!address || typeof address === "string") {
    throw new Error("Regions API did not start on a TCP port.");
  }
  return {
    server,
    url: `http://127.0.0.1:${address.port}`,
    get requests() {
      return state.requests;
    },
  };
}
