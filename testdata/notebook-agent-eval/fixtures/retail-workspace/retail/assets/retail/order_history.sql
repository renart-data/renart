/* @bruin
name: retail.order_history
type: duckdb.sql
description: Retained append-only order history for longitudinal analysis and replay.
depends:
  - retail.order_events
materialization:
  type: table
  strategy: append
columns:
  - name: order_id
    type: bigint
    primary_key: true
  - name: order_date
    type: date
  - name: customer_id
    type: bigint
  - name: order_total
    type: double
  - name: status
    type: varchar
  - name: recorded_at
    type: timestamp
@bruin */

select
  order_id,
  order_date,
  customer_id,
  order_total,
  status,
  order_date::timestamp + interval 12 hours as recorded_at
from retail.order_events
