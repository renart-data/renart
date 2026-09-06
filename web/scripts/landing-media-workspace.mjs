// Demo "acme analytics" workspace used for the landing-page media.
// Three phases, applied at different points of the capture pipeline:
//   1. createAcmeWorkspace  — the acme pipeline (raw → staging → mart), committed.
//   2. addMarketingPipeline — a second, hourly pipeline so lists don't look empty.
//   3. addStalenessEdits    — applied LAST, after all runs: an edited staging
//      asset (stale_edited → downstream marts stale_upstream) and a brand-new
//      mart asset (never_built), so the canvas shows every freshness badge.
import { mkdir, readFile, writeFile, rm } from "node:fs/promises";
import { execFileSync } from "node:child_process";
import path from "node:path";

// --- deterministic pseudo-random data --------------------------------------
function mulberry32(seed) {
  return function () {
    seed |= 0;
    seed = (seed + 0x6d2b79f5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const customers = [
  [1, "Ada Lovelace", "ada@example.com", "GB", "2025-11-02 09:15:00"],
  [2, "Grace Hopper", "grace@example.com", "US", "2025-11-14 11:30:00"],
  [3, "Katherine Johnson", "katherine@example.com", "US", "2025-12-01 16:45:00"],
  [4, "Margaret Hamilton", "margaret@example.com", "US", "2026-01-08 10:05:00"],
  [5, "Hedy Lamarr", "hedy@example.com", "AT", "2026-02-19 14:22:00"],
  [6, "Radia Perlman", "radia@example.com", "US", "2026-03-03 08:40:00"],
  [7, "Barbara Liskov", "barbara@example.com", "US", "2026-04-11 17:55:00"],
  [8, "Annie Easley", "annie@example.com", "US", "2026-05-27 12:10:00"],
];

const products = [
  [101, "Aurora Desk Lamp", "lighting", 89.0],
  [102, "Meridian Notebook", "stationery", 24.5],
  [103, "Cascade Water Bottle", "outdoors", 32.0],
  [104, "Summit Backpack", "outdoors", 148.0],
  [105, "Ember Mug", "kitchen", 79.95],
  [106, "Drift Throw Blanket", "home", 112.0],
  [107, "Pinnacle Pen Set", "stationery", 58.0],
  [108, "Haven Candle", "home", 36.0],
  [109, "Atlas Travel Kit", "outdoors", 94.5],
  [110, "Lumen Desk Mat", "office", 42.0],
];

// 1-2 orders/day over the ~5.5 weeks ending yesterday, so the daily-revenue
// chart in the notebook always looks current no matter when this runs.
function generateOrders() {
  const rand = mulberry32(20260710);
  const pick = (arr) => arr[Math.floor(rand() * arr.length)];
  const statuses = ["paid", "paid", "paid", "paid", "shipped", "shipped", "pending", "refunded"];
  const orders = [];
  const orderItems = [];
  let orderId = 1001;
  let itemId = 5001;
  const today = new Date();
  const dayMs = 24 * 60 * 60 * 1000;
  for (let offset = 39; offset >= 1; offset--) {
    const date = new Date(today.getTime() - offset * dayMs);
    const iso = date.toISOString().slice(0, 10);
    const n = 1 + Math.floor(rand() * 2);
    for (let k = 0; k < n; k++) {
      const cust = pick(customers);
      const status = pick(statuses);
      const numItems = 1 + Math.floor(rand() * 3);
      let total = 0;
      for (let j = 0; j < numItems; j++) {
        const prod = pick(products);
        const qty = 1 + Math.floor(rand() * 3);
        total += prod[3] * qty;
        orderItems.push([itemId++, orderId, prod[0], qty, prod[3]]);
      }
      orders.push([orderId++, cust[0], iso, status, Math.round(total * 100) / 100]);
    }
  }
  return { orders, orderItems };
}

function acmeFiles() {
  const { orders, orderItems } = generateOrders();
  const ordersValues = orders
    .map(([id, cid, d, s, t]) => `  (${id}, ${cid}, DATE '${d}', '${s}', ${t.toFixed(2)})`)
    .join(",\n");
  const customersValues = customers
    .map(
      ([id, name, email, cc, ts]) => `  (${id}, '${name}', '${email}', '${cc}', TIMESTAMP '${ts}')`,
    )
    .join(",\n");
  const productsValues = products
    .map(([id, name, cat, price]) => `  (${id}, '${name}', '${cat}', ${price.toFixed(2)})`)
    .join(",\n");
  const itemsValues = orderItems
    .map(([id, oid, pid, qty, price]) => `  (${id}, ${oid}, ${pid}, ${qty}, ${price.toFixed(2)})`)
    .join(",\n");

  return {
    ".bruin.yml": `default_environment: default
environments:
  default:
    connections:
      duckdb:
        - name: "duckdb-default"
          path: "acme.db"
  production:
    connections:
      duckdb:
        - name: "duckdb-default"
          path: "acme_prod.db"
`,

    "acme/pipeline.yml": `name: acme
schedule: "daily"
start_date: "2026-01-01"
default_environment: default
default:
  interval_modifiers:
    start: -5d
    end: 1d
`,

    "acme/assets/raw/orders.sql": `/* @bruin
name: raw.orders
type: duckdb.sql
owner: data-platform@acme.dev
tags:
  - ingestion
  - orders
materialization:
  type: table
columns:
  - name: order_id
    type: INTEGER
    description: "Unique order identifier"
    primary_key: true
    checks:
      - name: not_null
      - name: unique
  - name: customer_id
    type: INTEGER
    description: "FK to raw.customers"
    checks:
      - name: not_null
  - name: order_date
    type: DATE
    description: "Date the order was placed"
  - name: status
    type: VARCHAR
    description: "pending, paid, shipped, or refunded"
  - name: total_amount
    type: DOUBLE
    description: "Order total in USD"
    checks:
      - name: not_null
      - name: positive
@bruin */

SELECT *
FROM (VALUES
${ordersValues}
) AS orders(order_id, customer_id, order_date, status, total_amount)
`,

    "acme/assets/raw/customers.sql": `/* @bruin
name: raw.customers
type: duckdb.sql
owner: data-platform@acme.dev
tags:
  - ingestion
  - pii
materialization:
  type: table
columns:
  - name: customer_id
    type: INTEGER
    description: "Unique customer identifier"
    primary_key: true
    checks:
      - name: not_null
      - name: unique
  - name: customer_name
    type: VARCHAR
    description: "Full name of the customer"
  - name: email
    type: VARCHAR
    description: "Customer email address"
    checks:
      - name: not_null
      - name: unique
  - name: country
    type: VARCHAR
    description: "ISO country code"
  - name: created_at
    type: TIMESTAMP
    description: "Account creation timestamp"
@bruin */

SELECT *
FROM (VALUES
${customersValues}
) AS customers(customer_id, customer_name, email, country, created_at)
`,

    "acme/assets/raw/products.sql": `/* @bruin
name: raw.products
type: duckdb.sql
owner: data-platform@acme.dev
tags:
  - ingestion
  - catalog
materialization:
  type: table
columns:
  - name: product_id
    type: INTEGER
    description: "Unique product identifier"
    primary_key: true
    checks:
      - name: not_null
      - name: unique
  - name: product_name
    type: VARCHAR
    description: "Display name of the product"
  - name: category
    type: VARCHAR
    description: "Merchandising category"
  - name: unit_price
    type: DOUBLE
    description: "List price in USD"
    checks:
      - name: positive
@bruin */

SELECT *
FROM (VALUES
${productsValues}
) AS products(product_id, product_name, category, unit_price)
`,

    "acme/assets/raw/order_items.sql": `/* @bruin
name: raw.order_items
type: duckdb.sql
owner: data-platform@acme.dev
tags:
  - ingestion
  - orders
materialization:
  type: table
columns:
  - name: order_item_id
    type: INTEGER
    description: "Unique line-item identifier"
    primary_key: true
    checks:
      - name: not_null
      - name: unique
  - name: order_id
    type: INTEGER
    description: "FK to raw.orders"
    checks:
      - name: not_null
  - name: product_id
    type: INTEGER
    description: "FK to raw.products"
    checks:
      - name: not_null
  - name: quantity
    type: INTEGER
    description: "Units ordered"
    checks:
      - name: positive
  - name: unit_price
    type: DOUBLE
    description: "Price per unit at order time"
@bruin */

SELECT *
FROM (VALUES
${itemsValues}
) AS order_items(order_item_id, order_id, product_id, quantity, unit_price)
`,

    "acme/assets/staging/orders.sql": `/* @bruin
name: staging.orders
type: duckdb.sql
owner: analytics@acme.dev
tags:
  - staging
  - orders
materialization:
  type: table
depends:
  - raw.orders
  - raw.customers
columns:
  - name: order_id
    type: INTEGER
    description: "Unique order identifier"
    primary_key: true
    checks:
      - name: not_null
      - name: unique
  - name: customer_id
    type: INTEGER
    description: "FK to customers"
    checks:
      - name: not_null
  - name: customer_name
    type: VARCHAR
    description: "Denormalized customer name"
  - name: country
    type: VARCHAR
    description: "Customer country at order time"
  - name: order_date
    type: DATE
    description: "Date the order was placed"
  - name: status
    type: VARCHAR
    description: "Order status"
    checks:
      - name: accepted_values
        value: ["pending", "paid", "shipped", "refunded"]
  - name: total_amount
    type: DOUBLE
    description: "Order total in USD"
    checks:
      - name: positive
@bruin */

SELECT
    o.order_id,
    o.customer_id,
    c.customer_name,
    c.country,
    o.order_date,
    o.status,
    o.total_amount
FROM raw.orders o
JOIN raw.customers c USING (customer_id)
WHERE o.status != 'refunded'
`,

    "acme/assets/staging/order_items.sql": `/* @bruin
name: staging.order_items
type: duckdb.sql
owner: analytics@acme.dev
tags:
  - staging
  - orders
materialization:
  type: table
depends:
  - raw.order_items
  - raw.products
  - raw.orders
columns:
  - name: order_item_id
    type: INTEGER
    description: "Unique line-item identifier"
    primary_key: true
    checks:
      - name: not_null
      - name: unique
  - name: order_id
    type: INTEGER
    description: "FK to orders"
  - name: order_date
    type: DATE
    description: "Date of the parent order"
  - name: product_name
    type: VARCHAR
    description: "Denormalized product name"
  - name: category
    type: VARCHAR
    description: "Product category"
  - name: quantity
    type: INTEGER
    description: "Units ordered"
    checks:
      - name: positive
  - name: line_total
    type: DOUBLE
    description: "quantity * unit_price"
    checks:
      - name: positive
@bruin */

SELECT
    i.order_item_id,
    i.order_id,
    o.order_date,
    p.product_name,
    p.category,
    i.quantity,
    i.quantity * i.unit_price AS line_total
FROM raw.order_items i
JOIN raw.products p USING (product_id)
JOIN raw.orders o USING (order_id)
WHERE o.status != 'refunded'
`,

    "acme/assets/staging/customers.sql": `/* @bruin
name: staging.customers
type: duckdb.sql
owner: analytics@acme.dev
tags:
  - staging
  - pii
materialization:
  type: table
depends:
  - raw.customers
  - raw.orders
columns:
  - name: customer_id
    type: INTEGER
    description: "Unique customer identifier"
    primary_key: true
    checks:
      - name: not_null
      - name: unique
  - name: customer_name
    type: VARCHAR
    description: "Full name"
  - name: country
    type: VARCHAR
    description: "ISO country code"
  - name: first_order_date
    type: DATE
    description: "Date of the customer's first order"
  - name: lifetime_orders
    type: BIGINT
    description: "Orders placed to date"
@bruin */

SELECT
    c.customer_id,
    c.customer_name,
    c.country,
    MIN(o.order_date) AS first_order_date,
    COUNT(o.order_id) AS lifetime_orders
FROM raw.customers c
LEFT JOIN raw.orders o USING (customer_id)
GROUP BY c.customer_id, c.customer_name, c.country
`,

    "acme/assets/mart/daily_revenue.sql": `/* @bruin
name: mart.daily_revenue
type: duckdb.sql
owner: analytics@acme.dev
tags:
  - mart
  - finance
meta:
  web_chart_type: line
  web_chart_x: order_date
  web_chart_series: revenue,order_count
  web_chart_title: Daily revenue
materialization:
  type: table
depends:
  - staging.orders
columns:
  - name: order_date
    type: DATE
    description: "Calendar date"
    primary_key: true
    checks:
      - name: not_null
      - name: unique
  - name: order_count
    type: BIGINT
    description: "Number of orders"
    checks:
      - name: positive
  - name: revenue
    type: DOUBLE
    description: "Total revenue in USD"
    checks:
      - name: not_null
  - name: avg_order_value
    type: DOUBLE
    description: "Average order value"
@bruin */

SELECT
    order_date,
    COUNT(*) AS order_count,
    SUM(total_amount) AS revenue,
    AVG(total_amount) AS avg_order_value
FROM staging.orders
GROUP BY order_date
`,

    "acme/assets/mart/customer_ltv.sql": `/* @bruin
name: mart.customer_ltv
type: duckdb.sql
owner: analytics@acme.dev
tags:
  - mart
  - finance
materialization:
  type: table
depends:
  - staging.orders
  - staging.customers
columns:
  - name: customer_id
    type: INTEGER
    description: "Unique customer identifier"
    primary_key: true
    checks:
      - name: not_null
      - name: unique
  - name: customer_name
    type: VARCHAR
    description: "Full name"
  - name: country
    type: VARCHAR
    description: "ISO country code"
  - name: total_spent
    type: DOUBLE
    description: "Lifetime revenue in USD"
    checks:
      - name: positive
  - name: order_count
    type: BIGINT
    description: "Lifetime orders"
  - name: avg_order_value
    type: DOUBLE
    description: "Average order value"
@bruin */

SELECT
    sc.customer_id,
    sc.customer_name,
    sc.country,
    SUM(so.total_amount) AS total_spent,
    COUNT(*) AS order_count,
    AVG(so.total_amount) AS avg_order_value
FROM staging.orders so
JOIN staging.customers sc USING (customer_id)
GROUP BY sc.customer_id, sc.customer_name, sc.country
ORDER BY total_spent DESC
`,

    "acme/assets/mart/top_products.sql": `/* @bruin
name: mart.top_products
type: duckdb.sql
owner: analytics@acme.dev
tags:
  - mart
  - merchandising
materialization:
  type: table
depends:
  - staging.order_items
columns:
  - name: product_name
    type: VARCHAR
    description: "Product display name"
    primary_key: true
  - name: category
    type: VARCHAR
    description: "Merchandising category"
  - name: units_sold
    type: BIGINT
    description: "Total units sold"
    checks:
      - name: positive
  - name: revenue
    type: DOUBLE
    description: "Total revenue in USD"
    checks:
      - name: positive
@bruin */

SELECT
    product_name,
    category,
    CAST(SUM(quantity) AS BIGINT) AS units_sold,
    SUM(line_total) AS revenue
FROM staging.order_items
GROUP BY product_name, category
ORDER BY revenue DESC
`,
  };
}

const marketingFiles = {
  "marketing/pipeline.yml": `name: marketing
schedule: "hourly"
start_date: "2026-03-01"
default_environment: default
`,

  "marketing/assets/raw_campaigns.sql": `/* @bruin
name: raw.campaigns
type: duckdb.sql
owner: growth@acme.dev
tags:
  - ingestion
  - marketing
materialization:
  type: table
columns:
  - name: campaign_id
    type: INTEGER
    description: "Unique campaign identifier"
    primary_key: true
    checks:
      - name: not_null
      - name: unique
  - name: campaign_name
    type: VARCHAR
    description: "Campaign display name"
  - name: channel
    type: VARCHAR
    description: "Acquisition channel"
  - name: daily_budget
    type: DOUBLE
    description: "Daily budget in USD"
@bruin */

SELECT *
FROM (VALUES
  (1, 'Summer Sale', 'search', 250.00),
  (2, 'Newsletter Push', 'email', 40.00),
  (3, 'Creator Collab', 'social', 180.00),
  (4, 'Retargeting Q3', 'display', 95.00)
) AS campaigns(campaign_id, campaign_name, channel, daily_budget)
`,

  "marketing/assets/campaign_performance.sql": `/* @bruin
name: mart.campaign_performance
type: duckdb.sql
owner: growth@acme.dev
tags:
  - mart
  - marketing
materialization:
  type: table
depends:
  - raw.campaigns
columns:
  - name: campaign_name
    type: VARCHAR
    description: "Campaign display name"
    primary_key: true
  - name: channel
    type: VARCHAR
    description: "Acquisition channel"
  - name: spend_to_date
    type: DOUBLE
    description: "Total spend in USD"
@bruin */

SELECT
    campaign_name,
    channel,
    daily_budget * 30 AS spend_to_date
FROM raw.campaigns
`,
};

const weeklySummarySQL = `/* @bruin
name: mart.weekly_summary
type: duckdb.sql
owner: analytics@acme.dev
tags:
  - mart
  - finance
materialization:
  type: table
depends:
  - staging.orders
columns:
  - name: week_start
    type: DATE
    description: "Monday of the ISO week"
    primary_key: true
  - name: order_count
    type: BIGINT
    description: "Orders in the week"
  - name: revenue
    type: DOUBLE
    description: "Weekly revenue in USD"
@bruin */

SELECT
    CAST(DATE_TRUNC('week', order_date) AS DATE) AS week_start,
    COUNT(*) AS order_count,
    SUM(total_amount) AS revenue
FROM staging.orders
GROUP BY week_start
ORDER BY week_start
`;

function git(root, ...args) {
  execFileSync("git", ["-c", "user.email=demo@acme.dev", "-c", "user.name=Demo", ...args], {
    cwd: root,
  });
}

async function writeAll(root, files) {
  for (const [rel, content] of Object.entries(files)) {
    const abs = path.join(root, rel);
    await mkdir(path.dirname(abs), { recursive: true });
    await writeFile(abs, content);
  }
}

export async function createAcmeWorkspace(root) {
  await rm(root, { recursive: true, force: true });
  await writeAll(root, acmeFiles());
  git(root, "init", "-q");
  git(root, "add", "-A");
  git(root, "commit", "-q", "-m", "acme analytics pipeline");
}

export async function addMarketingPipeline(root) {
  await writeAll(root, marketingFiles);
  git(root, "add", "-A");
  git(root, "commit", "-q", "-m", "marketing pipeline");
}

// Applied after everything has been materialized: makes staging.orders
// stale_edited (its marts stale_upstream) and adds a never_built mart.
export async function addStalenessEdits(root) {
  const stagingOrders = path.join(root, "acme", "assets", "staging", "orders.sql");
  const content = await readFile(stagingOrders, "utf8");
  const needle = "WHERE o.status != 'refunded'";
  if (!content.includes(needle)) {
    throw new Error(`staleness edit: expected '${needle}' in ${stagingOrders}`);
  }
  await writeFile(
    stagingOrders,
    content.replace(needle, "WHERE o.status NOT IN ('refunded', 'pending')"),
  );
  await writeFile(
    path.join(root, "acme", "assets", "mart", "weekly_summary.sql"),
    weeklySummarySQL,
  );
}
