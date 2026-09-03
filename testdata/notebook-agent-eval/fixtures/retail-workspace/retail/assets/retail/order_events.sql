/* @bruin
name: retail.order_events
type: duckdb.sql
description: Raw order events retained as an append-only operational event stream.
materialization:
  type: table
  strategy: append
columns:
  - name: order_id
    type: bigint
    primary_key: true
    description: Durable order identifier.
  - name: order_date
    type: date
  - name: customer_id
    type: bigint
  - name: order_total
    type: double
  - name: status
    type: varchar
@bruin */

select * from retail.order_events
