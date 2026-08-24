/* @bruin
id: broken_orders
type: duckdb.sql
class: notebook
@bruin */

select
  order_date,
  sum(order_total) as revenue
from retail.missing_orders
where status = 'completed'
group by order_date
