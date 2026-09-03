/* @bruin
name: retail.customers
type: duckdb.sql
description: Current customer dimension used to label order activity.
materialization:
  type: view
columns:
  - name: customer_id
    type: bigint
    primary_key: true
  - name: customer_name
    type: varchar
  - name: segment
    type: varchar
@bruin */

select * from retail.customers
