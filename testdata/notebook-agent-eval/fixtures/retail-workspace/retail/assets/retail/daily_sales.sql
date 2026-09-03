/* @bruin
name: retail.daily_sales
type: duckdb.sql
description: Replay-safe daily completed-order counts and revenue derived from retained history.
depends:
  - retail.order_history
materialization:
  type: table
  strategy: time_interval
  incremental_key: order_date
  time_granularity: date
columns:
  - name: order_date
    type: date
    primary_key: true
  - name: order_count
    type: bigint
  - name: revenue
    type: double
@bruin */

select
  order_date,
  count(*)::bigint as order_count,
  sum(order_total)::double as revenue
from retail.order_history
where status = 'completed'
group by order_date
