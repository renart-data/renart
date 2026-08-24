/* @bruin
name: retail.current_orders
type: duckdb.sql
description: Current-day operational shortlist. It is replaced on every run and is not historical.
depends:
  - retail.order_events
materialization:
  type: table
  strategy: truncate+insert
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
@bruin */

select *
from retail.order_events
where order_date = current_date
