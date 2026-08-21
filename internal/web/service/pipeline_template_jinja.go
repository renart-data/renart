package service

import "fmt"

func jinjaWorkshopPipelineYAML(name string) string {
	return fmt.Sprintf(`name: %s
schedule: daily
start_date: "2026-07-20"

default_connections:
  duckdb: duckdb-default

variables:
  minimum_order_value:
    type: integer
    minimum: 0
    default: 50
  include_cancelled:
    type: boolean
    default: false
  channels:
    type: array
    items:
      type: string
    default: ["web", "partner", "store"]
`, quotedYAMLString(name))
}

func jinjaOrdersSQL() string {
	return `/* @bruin
name: jinja.orders
type: duckdb.sql
description: Window-aligned source rows used by each Jinja example.
materialization:
  type: table
  strategy: create+replace
columns:
  - name: order_id
    type: integer
    primary_key: true
  - name: customer_segment
    type: varchar
  - name: channel
    type: varchar
  - name: status
    type: varchar
  - name: ordered_at
    type: date
  - name: order_value
    type: integer
@bruin */

SELECT *
FROM (VALUES
    (1, 'enterprise', 'web',     'completed', DATE '{{ start_date }}', 140),
    (2, 'self_serve', 'store',   'completed', DATE '{{ start_date }}', 35),
    (3, 'enterprise', 'partner', 'cancelled', DATE '{{ start_date }}', 220),
    (4, 'self_serve', 'web',     'completed', DATE '{{ start_date }}', 80),
    (5, 'mid_market', 'partner', 'completed', DATE '{{ end_date }}' - 1, 175),
    (6, 'mid_market', 'store',   'completed', DATE '{{ end_date }}' - 1, 95),
    (7, 'enterprise', 'web',     'completed', DATE '{{ end_date }}' - 1, 260),
    (8, 'self_serve', 'partner', 'cancelled', DATE '{{ end_date }}' - 1, 60)
) AS orders(order_id, customer_segment, channel, status, ordered_at, order_value)
`
}

func jinjaWindowedOrdersSQL() string {
	return `/* @bruin
name: jinja.windowed_orders
type: duckdb.sql
description: Basic expressions using a pipeline variable and the selected run dates.
depends:
  - jinja.orders
materialization:
  type: view
columns:
  - name: order_id
    type: integer
  - name: customer_segment
    type: varchar
  - name: channel
    type: varchar
  - name: status
    type: varchar
  - name: ordered_at
    type: date
  - name: order_value
    type: integer
custom_checks:
  - name: selected date window contains orders
    count: 0
    query: select 1 from jinja.windowed_orders having count(*) = 0
@bruin */

SELECT *
FROM jinja.orders
WHERE ordered_at >= DATE '{{ start_date }}'
  AND ordered_at < DATE '{{ end_date }}'
  AND order_value >= {{ var.minimum_order_value }}
`
}

func jinjaConditionalOrdersSQL() string {
	return `/* @bruin
name: jinja.conditional_orders
type: duckdb.sql
description: A statement block that changes the filter without duplicating the query.
depends:
  - jinja.windowed_orders
materialization:
  type: view
columns:
  - name: order_id
    type: integer
  - name: customer_segment
    type: varchar
  - name: channel
    type: varchar
  - name: status
    type: varchar
  - name: ordered_at
    type: date
  - name: order_value
    type: integer
@bruin */

SELECT *
FROM jinja.windowed_orders
{% if var.include_cancelled %}
-- Keep every status when the pipeline variable is enabled.
{% else %}
WHERE status = 'completed'
{% endif %}
`
}

func jinjaChannelPivotSQL() string {
	return `/* @bruin
name: jinja.channel_pivot
type: duckdb.sql
description: A loop that generates one revenue column per configured channel.
depends:
  - jinja.conditional_orders
materialization:
  type: table
  strategy: create+replace
columns:
  - name: customer_segment
    type: varchar
  - name: web_revenue
    type: bigint
  - name: partner_revenue
    type: bigint
  - name: store_revenue
    type: bigint
  - name: total_revenue
    type: bigint
@bruin */

SELECT
    customer_segment,
{% for channel in var.channels %}
    sum(CASE WHEN channel = '{{ channel }}' THEN order_value ELSE 0 END) AS {{ channel }}_revenue,
{% endfor %}
    sum(order_value) AS total_revenue
FROM jinja.conditional_orders
GROUP BY customer_segment
ORDER BY total_revenue DESC
`
}
