/* @bruin
id: daily_totals
type: duckdb.sql
class: notebook
@bruin */

select
  order_date,
  count(*)::bigint as order_count,
  sum(order_total)::double as revenue
from retail.order_event
where status = 'completed'
group by order_date
order by order_date
