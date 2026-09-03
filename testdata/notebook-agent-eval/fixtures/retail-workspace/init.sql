create schema if not exists retail;

create table retail.order_events as
select *
from (values
  (1, date '2026-08-01', 101, 29.50::double, 'completed'),
  (2, date '2026-08-01', 102, 42.00::double, 'completed'),
  (3, date '2026-08-02', 101, 18.25::double, 'refunded'),
  (4, date '2026-08-02', 103, 76.00::double, 'completed'),
  (5, date '2026-08-03', 104, 51.25::double, 'completed')
) as events(order_id, order_date, customer_id, order_total, status);

create table retail.order_history as
select
  order_id,
  order_date,
  customer_id,
  order_total,
  status,
  order_date::timestamp + interval 12 hours as recorded_at
from retail.order_events;

create table retail.current_orders as
select *
from retail.order_events
where order_date = date '2026-08-03';

create table retail.daily_sales as
select
  order_date,
  count(*)::bigint as order_count,
  sum(order_total)::double as revenue
from retail.order_history
where status = 'completed'
group by order_date;

create table retail.customers as
select *
from (values
  (101, 'Ada', 'enterprise'),
  (102, 'Grace', 'startup'),
  (103, 'Linus', 'enterprise'),
  (104, 'Margaret', 'growth')
) as customers(customer_id, customer_name, segment);
