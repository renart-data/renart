package service

import (
	"fmt"

	"renart/internal/web/scheduler"
)

func earthquakeTemplateSchedules(
	primaryEnvironment string,
) map[string]templateEnvironmentSchedule {
	secondaryEnvironment := "production"
	if primaryEnvironment == secondaryEnvironment {
		secondaryEnvironment = "development"
	}
	return map[string]templateEnvironmentSchedule{
		primaryEnvironment: {
			duckdbFile: "earthquake_monitoring.duckdb",
			declaration: scheduler.ScheduleDeclaration{
				Cron:          "0 */6 * * *",
				Timezone:      "UTC",
				CatchupPolicy: scheduler.CatchupSkip,
				Variables: map[string]any{
					"min_magnitude":     3,
					"notable_magnitude": 5,
				},
			},
		},
		secondaryEnvironment: {
			duckdbFile: "earthquake_monitoring_" + secondaryEnvironment + ".duckdb",
			declaration: scheduler.ScheduleDeclaration{
				Cron:          "15 * * * *",
				Timezone:      "UTC",
				CatchupPolicy: scheduler.CatchupRunOnce,
				Variables: map[string]any{
					"min_magnitude":     4,
					"notable_magnitude": 6,
				},
			},
		},
	}
}

func earthquakePipelineYAML(name string) string {
	return fmt.Sprintf(`name: %s
schedule: "@hourly"
start_date: "2024-01-01"

default_connections:
  duckdb: duckdb-default

variables:
  min_magnitude:
    type: integer
    minimum: 0
    default: 3
  notable_magnitude:
    type: integer
    minimum: 0
    default: 5
`, quotedYAMLString(name))
}

func earthquakeEventsAPIYAML() string {
	return `name: earthquakes.events
type: api
connection: duckdb-default
description: Retained event history built by merging USGS earthquakes from every selected run window.
tags:
  - history
  - event-history

materialization:
  type: table
  strategy: merge

parameters:
  request:
    url: https://earthquake.usgs.gov/fdsnws/event/1/query
    method: GET
    headers:
      Accept: application/geo+json
      User-Agent: renart-earthquake-demo
    params:
      format: geojson
      starttime: "{{ start_timestamp }}"
      endtime: "{{ end_timestamp }}"
      minmagnitude: "{{ var.min_magnitude }}"
      eventtype: earthquake
      orderby: time-asc
      limit: "20000"
  response:
    records_path: features
    fields:
      event_id: id
      magnitude: properties.mag
      place: properties.place
      observed_at_ms: properties.time
      updated_at_ms: properties.updated
      significance: properties.sig
      status: properties.status
      tsunami: properties.tsunami
      detail_url: properties.url

columns:
  - name: event_id
    type: varchar
    primary_key: true
  - name: magnitude
    type: double
  - name: place
    type: varchar
  - name: observed_at_ms
    type: bigint
  - name: updated_at_ms
    type: bigint
  - name: significance
    type: integer
  - name: status
    type: varchar
  - name: tsunami
    type: integer
  - name: detail_url
    type: varchar
`
}

func earthquakeNotableEventsSQL() string {
	return `/* @bruin
name: earthquakes.notable_events
type: duckdb.sql
description: Current notable-event shortlist, replaced on every run; use window_summary or run_log for historical analysis.
tags:
  - current-snapshot
  - shortlist
depends:
  - earthquakes.events
materialization:
  type: table
  strategy: truncate+insert
hooks:
  pre:
    - query: |
        CREATE TABLE IF NOT EXISTS earthquakes.notable_events (
          event_id VARCHAR,
          magnitude DOUBLE,
          place VARCHAR,
          observed_at_ms BIGINT,
          significance INTEGER,
          detail_url VARCHAR
        )
columns:
  - name: event_id
    type: varchar
    primary_key: true
  - name: magnitude
    type: double
  - name: place
    type: varchar
  - name: observed_at_ms
    type: bigint
  - name: significance
    type: integer
  - name: detail_url
    type: varchar
meta:
  web_view: table
@bruin */

SELECT
    event_id,
    magnitude,
    place,
    observed_at_ms,
    significance,
    detail_url
FROM earthquakes.events
WHERE magnitude >= {{ var.notable_magnitude }}
ORDER BY magnitude DESC, observed_at_ms DESC
`
}

func earthquakeWindowSummarySQL() string {
	return `/* @bruin
name: earthquakes.window_summary
type: duckdb.sql
description: Replay-safe historical time series with one summary row per execution window; preferred for trend analysis.
tags:
  - history
  - time-series
  - recommended-analysis
depends:
  - earthquakes.events
materialization:
  type: table
  strategy: time_interval
  incremental_key: window_start
  time_granularity: timestamp
hooks:
  pre:
    - query: |
        CREATE TABLE IF NOT EXISTS earthquakes.window_summary (
          window_start TIMESTAMP,
          window_end TIMESTAMP,
          earthquake_count BIGINT,
          average_magnitude DOUBLE,
          maximum_magnitude DOUBLE
        )
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
meta:
  web_view: chart
  web_chart_type: line
  web_chart_x: window_start
  web_chart_series: earthquake_count
  web_chart_title: Earthquakes by run window
@bruin */

SELECT
    CAST('{{ start_timestamp }}' AS TIMESTAMP) AS window_start,
    CAST('{{ end_timestamp }}' AS TIMESTAMP) AS window_end,
    count(*) AS earthquake_count,
    coalesce(round(avg(magnitude), 2), 0) AS average_magnitude,
    coalesce(max(magnitude), 0) AS maximum_magnitude
FROM earthquakes.events
WHERE observed_at_ms >= epoch(CAST('{{ start_timestamp }}' AS TIMESTAMP)) * 1000
  AND observed_at_ms < epoch(CAST('{{ end_timestamp }}' AS TIMESTAMP)) * 1000
`
}

func earthquakeMagnitudeBandsSQL() string {
	return `/* @bruin
name: earthquakes.magnitude_bands
type: duckdb.sql
description: Current aggregate view over the retained earthquake event history.
tags:
  - historical-aggregate
  - current-view
depends:
  - earthquakes.events
materialization:
  type: view
columns:
  - name: magnitude_band
    type: varchar
  - name: earthquakes
    type: bigint
  - name: maximum_magnitude
    type: double
meta:
  web_view: chart
  web_chart_type: bar
  web_chart_x: magnitude_band
  web_chart_series: earthquakes
  web_chart_title: Collected earthquakes by magnitude
@bruin */

SELECT
    CASE
        WHEN magnitude >= 7 THEN '7.0+'
        WHEN magnitude >= 6 THEN '6.0–6.9'
        WHEN magnitude >= 5 THEN '5.0–5.9'
        WHEN magnitude >= 4 THEN '4.0–4.9'
        ELSE 'below 4.0'
    END AS magnitude_band,
    count(*) AS earthquakes,
    max(magnitude) AS maximum_magnitude
FROM earthquakes.events
GROUP BY magnitude_band
ORDER BY maximum_magnitude DESC
`
}

func earthquakeRunLogSQL() string {
	return `/* @bruin
name: earthquakes.run_log
type: duckdb.sql
description: Append-only execution audit history with the key metrics observed in each completed run window.
tags:
  - history
  - append-only
  - execution-audit
depends:
  - earthquakes.window_summary
materialization:
  type: table
  strategy: append
hooks:
  pre:
    - query: |
        CREATE TABLE IF NOT EXISTS earthquakes.run_log (
          run_id VARCHAR,
          window_start TIMESTAMP,
          window_end TIMESTAMP,
          earthquake_count BIGINT,
          average_magnitude DOUBLE,
          maximum_magnitude DOUBLE
        )
columns:
  - name: run_id
    type: varchar
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
@bruin */

SELECT
    '{{ run_id }}' AS run_id,
    window_start,
    window_end,
    earthquake_count,
    average_magnitude,
    maximum_magnitude
FROM earthquakes.window_summary
WHERE window_start = CAST('{{ start_timestamp }}' AS TIMESTAMP)
`
}
