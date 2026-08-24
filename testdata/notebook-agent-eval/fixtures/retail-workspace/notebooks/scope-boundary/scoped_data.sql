/* @bruin
id: scoped_data
type: duckdb.sql
class: notebook
@bruin */

select count(*)::bigint as customer_count
from retail.customers
