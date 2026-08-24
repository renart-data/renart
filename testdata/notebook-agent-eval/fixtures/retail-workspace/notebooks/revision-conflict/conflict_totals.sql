/* @bruin
id: conflict_totals
type: duckdb.sql
class: notebook
@bruin */

select order_date, sum(order_total)::double as revenue
from retail.order_event
group by order_date
