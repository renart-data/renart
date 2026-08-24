/* @bruin
id: trend_data
type: duckdb.sql
class: notebook
@bruin */

select order_date, order_count, revenue
from retail.daily_sales
order by order_date
