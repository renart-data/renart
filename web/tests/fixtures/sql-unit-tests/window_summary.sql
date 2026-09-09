/* @bruin
type: duckdb.sql
description: One summary row for each selected execution window.
materialization:
  type: table
  strategy: time_interval
  incremental_key: window_start
  time_granularity: timestamp
depends:
  - earthquakes.events
columns:
  - name: window_start
    type: timestamp
  - name: window_end
    type: timestamp
  - name: earthquake_count
    type: bigint
  - name: average_magnitude
    type: double
  - name: maximum_magnitude
    type: double
hooks:
  pre:
    - query: |-
        CREATE TABLE IF NOT EXISTS earthquakes.window_summary (
          window_start TIMESTAMP,
          window_end TIMESTAMP,
          earthquake_count BIGINT,
          average_magnitude DOUBLE,
          maximum_magnitude DOUBLE
        )
@bruin */

SELECT
  CAST('{{ start_timestamp }}' AS TIMESTAMP) AS window_start,
  CAST('{{ end_timestamp }}' AS TIMESTAMP) AS window_end,
  COUNT(*) AS earthquake_count,
  COALESCE(ROUND(AVG(magnitude), 2), 0) AS average_magnitude,
  COALESCE(MAX(magnitude), 0) AS maximum_magnitude
FROM earthquakes.events
WHERE
  observed_at_ms >= EPOCH(CAST('{{ start_timestamp }}' AS TIMESTAMP)) * 1000
  AND observed_at_ms < EPOCH(CAST('{{ end_timestamp }}' AS TIMESTAMP)) * 1000
