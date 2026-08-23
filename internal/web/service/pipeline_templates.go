package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// PipelineTemplateInfo describes one starter offered by the in-workspace
// "New pipeline" flow. The backend owns both this catalog and the emitted
// files so the editor never has to duplicate scaffold contents.
type PipelineTemplateInfo struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Category      string   `json:"category"`
	Offline       bool     `json:"offline"`
	SuggestedPath string   `json:"suggested_path"`
	AssetNames    []string `json:"asset_names"`
	Features      []string `json:"features"`
}

// renart:web
type PipelineTemplatesResponse struct {
	Status    string                 `json:"status"`
	Templates []PipelineTemplateInfo `json:"templates"`
}

type pipelineTemplate struct {
	info                 PipelineTemplateInfo
	duckdbFile           string
	files                func(pipelineName string) map[string]string
	environmentSchedules func(primaryEnvironment string) map[string]templateEnvironmentSchedule
}

const (
	PipelineTemplateBlank          = "blank"
	PipelineTemplateProductDemo    = "demo:product"
	PipelineTemplateOperationsDemo = "demo:operations"
	PipelineTemplateEarthquakeDemo = "demo:earthquakes"
	PipelineTemplatePythonDemo     = "demo:python"
	PipelineTemplateJinjaDemo      = "demo:jinja"

	PipelineTemplateCategoryStart      = "Start"
	PipelineTemplateCategoryAnalytics  = "Analytics"
	PipelineTemplateCategoryOperations = "Operations"
	PipelineTemplateCategoryExplore    = "Explore features"
)

func pipelineTemplates() []pipelineTemplate {
	return []pipelineTemplate{
		{
			info: PipelineTemplateInfo{
				ID:            PipelineTemplateBlank,
				Title:         "Blank pipeline",
				Description:   "Just pipeline.yml and an empty assets folder, ready for your own model.",
				Category:      PipelineTemplateCategoryStart,
				Offline:       true,
				SuggestedPath: "my_pipeline",
				AssetNames:    []string{},
				Features:      []string{"Empty canvas"},
			},
			files: func(pipelineName string) map[string]string {
				return map[string]string{"pipeline.yml": basicPipelineYAML(pipelineName)}
			},
		},
		{
			info: PipelineTemplateInfo{
				ID:            PipelineTemplateProductDemo,
				Title:         "Product analytics",
				Description:   "Model synthetic product events into user journeys, an activation funnel, and daily activity charts.",
				Category:      PipelineTemplateCategoryAnalytics,
				Offline:       true,
				SuggestedPath: "product_analytics",
				AssetNames: []string{
					"product.users",
					"product.events",
					"product.user_journeys",
					"product.activation_funnel",
					"product.daily_active_users",
				},
				Features: []string{"SQL DAG", "Variables", "Data checks", "Charts"},
			},
			duckdbFile: "product_analytics.duckdb",
			files: func(pipelineName string) map[string]string {
				return map[string]string{
					"pipeline.yml":                          productPipelineYAML(pipelineName),
					"assets/product/users.sql":              productUsersSQL(),
					"assets/product/events.sql":             productEventsSQL(),
					"assets/product/user_journeys.sql":      productUserJourneysSQL(),
					"assets/product/activation_funnel.sql":  productActivationFunnelSQL(),
					"assets/product/daily_active_users.sql": productDailyActiveUsersSQL(),
				}
			},
		},
		{
			info: PipelineTemplateInfo{
				ID:            ProjectTemplateRetailDemo,
				Title:         "Retail analytics",
				Description:   "Load bundled CSV seeds and turn them into customer and daily revenue models.",
				Category:      PipelineTemplateCategoryAnalytics,
				Offline:       true,
				SuggestedPath: "retail_analytics",
				AssetNames: []string{
					"raw.customers",
					"raw.orders",
					"analytics.customer_orders",
					"analytics.daily_revenue",
				},
				Features: []string{"Seed files", "Schema metadata", "SQL lineage", "Tables"},
			},
			duckdbFile: "retail.duckdb",
			files: func(pipelineName string) map[string]string {
				files := projectTemplateByIDFiles(ProjectTemplateRetailDemo)
				files["pipeline.yml"] = retailTemplatePipelineYAML(pipelineName)
				return files
			},
		},
		{
			info: PipelineTemplateInfo{
				ID:            PipelineTemplateOperationsDemo,
				Title:         "Operations monitoring",
				Description:   "Gate a device-health model with a query sensor, then surface fleet health and an incident queue.",
				Category:      PipelineTemplateCategoryOperations,
				Offline:       true,
				SuggestedPath: "operations_monitoring",
				AssetNames: []string{
					"ops.device_events",
					"ops.events_ready",
					"ops.device_health",
					"ops.incident_queue",
					"ops.fleet_overview",
				},
				Features: []string{"Query sensor", "SQL DAG", "Data checks", "Charts"},
			},
			duckdbFile: "operations_monitoring.duckdb",
			files: func(pipelineName string) map[string]string {
				return map[string]string{
					"pipeline.yml":                      operationsPipelineYAML(pipelineName),
					"assets/ops/device_events.sql":      operationsDeviceEventsSQL(),
					"assets/ops/events_ready.asset.yml": operationsEventsReadyYAML(),
					"assets/ops/device_health.sql":      operationsDeviceHealthSQL(),
					"assets/ops/incident_queue.sql":     operationsIncidentQueueSQL(),
					"assets/ops/fleet_overview.sql":     operationsFleetOverviewSQL(),
				}
			},
		},
		{
			info: PipelineTemplateInfo{
				ID:            PipelineTemplateEarthquakeDemo,
				Title:         "Earthquake monitoring",
				Description:   "Build retained USGS event history, replay-safe window trends, and an append-only run audit while exercising five materialization modes.",
				Category:      PipelineTemplateCategoryOperations,
				Offline:       false,
				SuggestedPath: "earthquake_monitoring",
				AssetNames: []string{
					"earthquakes.events",
					"earthquakes.notable_events",
					"earthquakes.window_summary",
					"earthquakes.magnitude_bands",
					"earthquakes.run_log",
				},
				Features: []string{"HTTP API", "Run windows", "4 table strategies", "Schedules"},
			},
			duckdbFile:           "earthquake_monitoring.duckdb",
			environmentSchedules: earthquakeTemplateSchedules,
			files: func(pipelineName string) map[string]string {
				return map[string]string{
					"pipeline.yml":                           earthquakePipelineYAML(pipelineName),
					"assets/earthquakes/events.asset.yml":    earthquakeEventsAPIYAML(),
					"assets/earthquakes/notable_events.sql":  earthquakeNotableEventsSQL(),
					"assets/earthquakes/window_summary.sql":  earthquakeWindowSummarySQL(),
					"assets/earthquakes/magnitude_bands.sql": earthquakeMagnitudeBandsSQL(),
					"assets/earthquakes/run_log.sql":         earthquakeRunLogSQL(),
				}
			},
		},
		{
			info: PipelineTemplateInfo{
				ID:            PipelineTemplatePythonDemo,
				Title:         "Python risk scoring",
				Description:   "Build account features in SQL, score them with renart.query in Python, and aggregate the result back in SQL.",
				Category:      PipelineTemplateCategoryExplore,
				Offline:       false,
				SuggestedPath: "python_risk_scoring",
				AssetNames: []string{
					"risk.transactions",
					"risk.account_features",
					"risk.scored_accounts",
					"risk.portfolio_summary",
				},
				Features: []string{"Seed file", "Python SDK", "SQL + Python", "Cross-language DAG"},
			},
			duckdbFile: "python_risk_scoring.duckdb",
			files: func(pipelineName string) map[string]string {
				return map[string]string{
					"pipeline.yml":                       pythonDemoPipelineYAML(pipelineName),
					"assets/risk/transactions.asset.yml": riskTransactionsSeedYAML(),
					"assets/risk/transactions.csv":       riskTransactionsCSV(),
					"assets/risk/account_features.sql":   riskAccountFeaturesSQL(),
					"assets/risk/scored_accounts.py":     riskScoredAccountsPython(),
					"assets/risk/portfolio_summary.sql":  riskPortfolioSummarySQL(),
				}
			},
		},
		{
			info: PipelineTemplateInfo{
				ID:            PipelineTemplateJinjaDemo,
				Title:         "Jinja workshop",
				Description:   "Progress from date and variable expressions to conditionals and generated SQL loops.",
				Category:      PipelineTemplateCategoryExplore,
				Offline:       true,
				SuggestedPath: "jinja_workshop",
				AssetNames: []string{
					"jinja.orders",
					"jinja.windowed_orders",
					"jinja.conditional_orders",
					"jinja.channel_pivot",
				},
				Features: []string{"Variables", "Date windows", "Conditionals", "Generated columns"},
			},
			duckdbFile: "jinja_workshop.duckdb",
			files: func(pipelineName string) map[string]string {
				return map[string]string{
					"pipeline.yml":                        jinjaWorkshopPipelineYAML(pipelineName),
					"assets/jinja/orders.sql":             jinjaOrdersSQL(),
					"assets/jinja/windowed_orders.sql":    jinjaWindowedOrdersSQL(),
					"assets/jinja/conditional_orders.sql": jinjaConditionalOrdersSQL(),
					"assets/jinja/channel_pivot.sql":      jinjaChannelPivotSQL(),
				}
			},
		},
		{
			info: PipelineTemplateInfo{
				ID:            ProjectTemplateChessDemo,
				Title:         "Chess API analytics",
				Description:   "Iterate over Chess.com API endpoints and compare player results, ratings, and openings.",
				Category:      PipelineTemplateCategoryExplore,
				Offline:       false,
				SuggestedPath: "chess_api_analytics",
				AssetNames: []string{
					"chess.players",
					"chess.games",
					"chess.game_results",
					"chess.player_performance",
					"chess.opening_repertoire",
				},
				Features: []string{"HTTP API", "Iteration", "SQL lineage", "Charts"},
			},
			duckdbFile: "chess_playground.duckdb",
			files: func(pipelineName string) map[string]string {
				files := projectTemplateByIDFiles(ProjectTemplateChessDemo)
				files["pipeline.yml"] = chessTemplatePipelineYAML(pipelineName)
				return files
			},
		},
	}
}

// PipelineTemplates lists every starter accepted by PipelineService.Create.
func PipelineTemplates() []PipelineTemplateInfo {
	templates := pipelineTemplates()
	infos := make([]PipelineTemplateInfo, 0, len(templates))
	for _, template := range templates {
		infos = append(infos, template.info)
	}
	return infos
}

func pipelineTemplateByID(id string) (pipelineTemplate, bool) {
	for _, template := range pipelineTemplates() {
		if template.info.ID == strings.TrimSpace(id) {
			return template, true
		}
	}
	return pipelineTemplate{}, false
}

func projectTemplateByIDFiles(id string) map[string]string {
	template, ok := projectTemplateByID(id)
	if !ok {
		return map[string]string{}
	}
	return template.files()
}

func quotedYAMLString(value string) string {
	return strconv.Quote(strings.TrimSpace(value))
}

func basicPipelineYAML(name string) string {
	return fmt.Sprintf("name: %s\n", quotedYAMLString(name))
}

func retailTemplatePipelineYAML(name string) string {
	return fmt.Sprintf(`name: %s
schedule: daily
start_date: "2024-01-01"

default_connections:
  duckdb: duckdb-default
`, quotedYAMLString(name))
}

func chessTemplatePipelineYAML(name string) string {
	return fmt.Sprintf(`name: %s
concurrency: 1

default_connections:
  duckdb: duckdb-default
`, quotedYAMLString(name))
}

func productPipelineYAML(name string) string {
	return fmt.Sprintf(`name: %s
schedule: daily
start_date: "2024-01-01"

default_connections:
  duckdb: duckdb-default

variables:
  activation_events_required:
    type: integer
    default: 3
`, quotedYAMLString(name))
}

func productUsersSQL() string {
	return `/* @bruin
name: product.users
type: duckdb.sql
materialization:
  type: table
columns:
  - name: user_id
    type: integer
    primary_key: true
    checks:
      - name: not_null
      - name: unique
  - name: user_name
    type: varchar
  - name: plan
    type: varchar
  - name: country
    type: varchar
  - name: signed_up_at
    type: date
meta:
  web_view: table
@bruin */

SELECT
    user_id,
    user_name,
    plan,
    country,
    current_date - signup_days_ago AS signed_up_at
-- Generated events cover the last 28 days. Keep every signup before that
-- window so the activation timeline remains internally consistent.
FROM (VALUES
    (1, 'Ada', 'team', 'GB', 51),
    (2, 'Grace', 'enterprise', 'US', 50),
    (3, 'Alan', 'free', 'GB', 49),
    (4, 'Katherine', 'team', 'US', 48),
    (5, 'Margaret', 'enterprise', 'US', 47),
    (6, 'Claude', 'free', 'US', 46),
    (7, 'Edsger', 'team', 'NL', 45),
    (8, 'Barbara', 'enterprise', 'US', 44),
    (9, 'Donald', 'free', 'US', 43),
    (10, 'Radia', 'team', 'US', 42),
    (11, 'Annie', 'enterprise', 'US', 41),
    (12, 'John', 'free', 'HU', 40),
    (13, 'Mary', 'team', 'US', 39),
    (14, 'Linus', 'enterprise', 'FI', 38),
    (15, 'Frances', 'free', 'US', 37),
    (16, 'Ken', 'team', 'US', 36),
    (17, 'Guido', 'enterprise', 'NL', 35),
    (18, 'Jean', 'free', 'US', 34),
    (19, 'Tim', 'team', 'GB', 33),
    (20, 'Sophie', 'enterprise', 'CA', 32),
    (21, 'James', 'free', 'US', 31),
    (22, 'Karen', 'team', 'DK', 30),
    (23, 'Martin', 'enterprise', 'GB', 29),
    (24, 'Katie', 'free', 'US', 28)
) AS users(user_id, user_name, plan, country, signup_days_ago)
`
}

func productEventsSQL() string {
	return `/* @bruin
name: product.events
type: duckdb.sql
materialization:
  type: table
columns:
  - name: event_id
    type: bigint
    primary_key: true
    checks:
      - name: not_null
  - name: user_id
    type: integer
    checks:
      - name: not_null
  - name: session_id
    type: varchar
  - name: event_date
    type: date
  - name: event_name
    type: varchar
meta:
  web_view: table
@bruin */

WITH generated AS (
    SELECT
        event_id,
        1 + ((event_id * 7) % 24)::integer AS user_id,
        ((event_id * 5) % 28)::integer AS days_ago,
        ((event_id * 11) % 4)::integer AS session_number
    FROM (SELECT unnest(range(1, 721)) AS event_id) AS ids
)

SELECT
    event_id,
    user_id,
    concat('session_', user_id, '_', days_ago, '_', session_number) AS session_id,
    current_date - days_ago AS event_date,
    CASE event_id % 6
        WHEN 0 THEN 'signed_in'
        WHEN 1 THEN 'created_project'
        WHEN 2 THEN 'created_pipeline'
        WHEN 3 THEN 'materialized_asset'
        WHEN 4 THEN 'opened_catalog'
        ELSE 'invited_teammate'
    END AS event_name
FROM generated
`
}

func productUserJourneysSQL() string {
	return `/* @bruin
name: product.user_journeys
type: duckdb.sql
materialization:
  type: table
depends:
  - product.users
  - product.events
columns:
  - name: user_id
    type: integer
    primary_key: true
    checks:
      - name: not_null
  - name: event_count
    type: bigint
    checks:
      - name: positive
  - name: user_name
    type: varchar
  - name: plan
    type: varchar
  - name: country
    type: varchar
  - name: signed_up_at
    type: date
  - name: session_count
    type: bigint
  - name: activated_at
    type: date
  - name: is_activated
    type: boolean
custom_checks:
  - name: activation date follows signup
    count: 0
    query: select * from product.user_journeys where activated_at < signed_up_at
meta:
  web_view: table
@bruin */

SELECT
    users.user_id,
    users.user_name,
    users.plan,
    users.country,
    users.signed_up_at,
    count(events.event_id) AS event_count,
    count(DISTINCT events.session_id) AS session_count,
    min(events.event_date) FILTER (
        WHERE events.event_name IN ('materialized_asset', 'invited_teammate')
    ) AS activated_at,
    count(events.event_id) >= {{ var.activation_events_required }} AS is_activated
FROM product.users AS users
LEFT JOIN product.events AS events ON events.user_id = users.user_id
GROUP BY ALL
`
}

func productActivationFunnelSQL() string {
	return `/* @bruin
name: product.activation_funnel
type: duckdb.sql
materialization:
  type: table
depends:
  - product.user_journeys
columns:
  - name: step_order
    type: integer
  - name: step
    type: varchar
  - name: users
    type: bigint
meta:
  web_view: chart
  web_chart_type: bar
  web_chart_x: step
  web_chart_series: users
  web_chart_title: Product activation funnel
@bruin */

SELECT 1 AS step_order, 'Signed up' AS step, count(*) AS users
FROM product.user_journeys
UNION ALL
SELECT 2, 'Active', count(*) FILTER (WHERE session_count > 0)
FROM product.user_journeys
UNION ALL
SELECT 3, 'Engaged', count(*) FILTER (
    WHERE event_count >= {{ var.activation_events_required }}
)
FROM product.user_journeys
UNION ALL
SELECT 4, 'Activated', count(*) FILTER (WHERE is_activated)
FROM product.user_journeys
ORDER BY step_order
`
}

func productDailyActiveUsersSQL() string {
	return `/* @bruin
name: product.daily_active_users
type: duckdb.sql
materialization:
  type: table
depends:
  - product.events
columns:
  - name: activity_date
    type: date
  - name: daily_active_users
    type: bigint
  - name: sessions
    type: bigint
  - name: events
    type: bigint
meta:
  web_view: chart
  web_chart_type: line
  web_chart_x: activity_date
  web_chart_series: daily_active_users
  web_chart_title: Daily active users
@bruin */

SELECT
    event_date AS activity_date,
    count(DISTINCT user_id) AS daily_active_users,
    count(DISTINCT session_id) AS sessions,
    count(*) AS events
FROM product.events
GROUP BY event_date
ORDER BY event_date
`
}

func operationsPipelineYAML(name string) string {
	return fmt.Sprintf(`name: %s
schedule: "*/15 * * * *"
start_date: "2024-01-01"

default_connections:
  duckdb: duckdb-default
`, quotedYAMLString(name))
}

func operationsDeviceEventsSQL() string {
	return `/* @bruin
name: ops.device_events
type: duckdb.sql
materialization:
  type: table
columns:
  - name: event_id
    type: bigint
    primary_key: true
    checks:
      - name: not_null
  - name: device_id
    type: varchar
    checks:
      - name: not_null
  - name: observed_at
    type: timestamp
  - name: temperature_c
    type: double
  - name: battery_percent
    type: integer
  - name: status
    type: varchar
meta:
  web_view: table
@bruin */

SELECT
    event_id,
    concat('device_', lpad(cast(1 + ((event_id * 7) % 12) AS varchar), 2, '0')) AS device_id,
    current_timestamp - ((event_id % 180) * INTERVAL '10 minutes') AS observed_at,
    round(18 + ((event_id * 13) % 210) / 10.0, 1) AS temperature_c,
    (5 + ((event_id * 17) % 96))::integer AS battery_percent,
    CASE
        WHEN event_id % 29 = 0 THEN 'error'
        WHEN event_id % 11 = 0 THEN 'warning'
        ELSE 'ok'
    END AS status
FROM (SELECT unnest(range(1, 361)) AS event_id) AS events
`
}

func operationsEventsReadyYAML() string {
	return `name: ops.events_ready
type: duckdb.sensor.query
depends:
  - ops.device_events
parameters:
  query: select count(*) from ops.device_events
  poke_interval: 5
  timeout: 1m
`
}

func operationsDeviceHealthSQL() string {
	return `/* @bruin
name: ops.device_health
type: duckdb.sql
materialization:
  type: table
depends:
  - ops.device_events
  - ops.events_ready
columns:
  - name: device_id
    type: varchar
    primary_key: true
    checks:
      - name: not_null
  - name: last_seen_at
    type: timestamp
  - name: average_temperature_c
    type: double
  - name: peak_temperature_c
    type: double
  - name: minimum_battery_percent
    type: integer
  - name: warnings
    type: bigint
  - name: errors
    type: bigint
  - name: health_score
    type: bigint
meta:
  web_view: table
@bruin */

SELECT
    device_id,
    max(observed_at) AS last_seen_at,
    round(avg(temperature_c), 1) AS average_temperature_c,
    max(temperature_c) AS peak_temperature_c,
    min(battery_percent) AS minimum_battery_percent,
    count(*) FILTER (WHERE status = 'warning') AS warnings,
    count(*) FILTER (WHERE status = 'error') AS errors,
    greatest(
        0,
        100
        - count(*) FILTER (WHERE status = 'warning') * 3
        - count(*) FILTER (WHERE status = 'error') * 12
        - CASE WHEN min(battery_percent) < 15 THEN 20 ELSE 0 END
    ) AS health_score
FROM ops.device_events
GROUP BY device_id
`
}

func operationsIncidentQueueSQL() string {
	return `/* @bruin
name: ops.incident_queue
type: duckdb.sql
materialization:
  type: table
depends:
  - ops.device_health
columns:
  - name: device_id
    type: varchar
    checks:
      - name: not_null
  - name: health_score
    type: bigint
  - name: minimum_battery_percent
    type: integer
  - name: peak_temperature_c
    type: double
  - name: warnings
    type: bigint
  - name: errors
    type: bigint
  - name: priority
    type: varchar
  - name: recommended_action
    type: varchar
meta:
  web_view: table
  web_table_dense: "false"
@bruin */

SELECT
    device_id,
    health_score,
    minimum_battery_percent,
    peak_temperature_c,
    warnings,
    errors,
    CASE
        WHEN errors >= 2 OR health_score < 50 THEN 'critical'
        WHEN errors > 0 OR minimum_battery_percent < 20 THEN 'warning'
        ELSE 'watch'
    END AS priority,
    CASE
        WHEN minimum_battery_percent < 20 THEN 'Replace battery'
        WHEN peak_temperature_c > 35 THEN 'Inspect cooling'
        ELSE 'Review error logs'
    END AS recommended_action
FROM ops.device_health
WHERE errors > 0 OR minimum_battery_percent < 25 OR peak_temperature_c > 35
ORDER BY health_score, device_id
`
}

func operationsFleetOverviewSQL() string {
	return `/* @bruin
name: ops.fleet_overview
type: duckdb.sql
materialization:
  type: table
depends:
  - ops.device_health
columns:
  - name: health_band
    type: varchar
  - name: devices
    type: bigint
  - name: average_health_score
    type: double
meta:
  web_view: chart
  web_chart_type: bar
  web_chart_x: health_band
  web_chart_series: devices
  web_chart_title: Fleet health
@bruin */

SELECT
    CASE
        WHEN health_score >= 80 THEN 'Healthy'
        WHEN health_score >= 50 THEN 'Needs attention'
        ELSE 'Critical'
    END AS health_band,
    count(*) AS devices,
    round(avg(health_score), 1) AS average_health_score
FROM ops.device_health
GROUP BY health_band
ORDER BY min(health_score) DESC
`
}

func pythonDemoPipelineYAML(name string) string {
	return fmt.Sprintf(`name: %s
schedule: daily
start_date: "2024-01-01"

default_connections:
  duckdb: duckdb-default
`, quotedYAMLString(name))
}

func riskTransactionsSeedYAML() string {
	return `name: risk.transactions
type: duckdb.seed
description: Bundled transactions for the SQL and Python risk-scoring demo.
meta:
  renart_seed_file: transactions.csv
parameters:
  path: ./transactions.csv
  file_type: csv
  enforce_schema: true
columns:
  - name: transaction_id
    type: integer
  - name: account_id
    type: integer
  - name: transacted_at
    type: timestamp
  - name: amount
    type: decimal(10,2)
  - name: country
    type: string
  - name: channel
    type: string
  - name: declined
    type: boolean
`
}

func riskTransactionsCSV() string {
	var result strings.Builder
	result.Grow(16 << 10)
	result.WriteString("transaction_id,account_id,transacted_at,amount,country,channel,declined\n")
	start := time.Date(2024, time.January, 1, 8, 0, 0, 0, time.UTC)
	countries := []string{"US", "GB", "DE", "CA", "NL"}
	channels := []string{"card", "bank_transfer", "wallet"}
	for sequence := 1; sequence <= 240; sequence++ {
		accountID := 1 + (sequence*7)%18
		transactedAt := start.Add(time.Duration((sequence*37)%1440) * time.Hour)
		amountCents := 500 + (sequence*97)%48_000
		declined := (sequence*13)%17 == 0
		_, _ = fmt.Fprintf(
			&result,
			"%d,%d,%s,%d.%02d,%s,%s,%t\n",
			sequence,
			accountID,
			transactedAt.Format("2006-01-02 15:04:05"),
			amountCents/100,
			amountCents%100,
			countries[(sequence*3)%len(countries)],
			channels[(sequence*5)%len(channels)],
			declined,
		)
	}
	return result.String()
}

func riskAccountFeaturesSQL() string {
	return `/* @bruin
name: risk.account_features
type: duckdb.sql
materialization:
  type: table
depends:
  - risk.transactions
columns:
  - name: account_id
    type: integer
    primary_key: true
    checks:
      - name: not_null
  - name: transaction_count
    type: bigint
  - name: total_amount
    type: double
  - name: average_amount
    type: double
  - name: maximum_amount
    type: double
  - name: declined_transactions
    type: bigint
  - name: countries
    type: bigint
  - name: channels
    type: bigint
  - name: first_transaction_at
    type: timestamp
  - name: latest_transaction_at
    type: timestamp
meta:
  web_view: table
@bruin */

SELECT
    account_id,
    count(*) AS transaction_count,
    round(cast(sum(amount) AS double), 2) AS total_amount,
    round(cast(avg(amount) AS double), 2) AS average_amount,
    cast(max(amount) AS double) AS maximum_amount,
    count(*) FILTER (WHERE declined) AS declined_transactions,
    count(DISTINCT country) AS countries,
    count(DISTINCT channel) AS channels,
    min(transacted_at) AS first_transaction_at,
    max(transacted_at) AS latest_transaction_at
FROM risk.transactions
GROUP BY account_id
`
}

func riskScoredAccountsPython() string {
	return `""" @bruin
name: risk.scored_accounts
type: python
materialization:
  type: table
depends:
  - risk.account_features
columns:
  - name: account_id
    type: integer
  - name: transaction_count
    type: bigint
  - name: total_amount
    type: double
  - name: average_amount
    type: double
  - name: maximum_amount
    type: double
  - name: declined_transactions
    type: bigint
  - name: countries
    type: bigint
  - name: channels
    type: bigint
  - name: first_transaction_at
    type: timestamp
  - name: latest_transaction_at
    type: timestamp
  - name: risk_score
    type: double
  - name: risk_band
    type: varchar
@bruin """

from renart import query


def materialize():
    accounts = query("select * from risk.account_features").to_pandas()
    decline_rate = accounts["declined_transactions"] / accounts["transaction_count"]
    amount_signal = (accounts["maximum_amount"] / 50).clip(upper=45)
    geography_signal = ((accounts["countries"] - 1).clip(lower=0) * 12).clip(upper=36)

    accounts["risk_score"] = (
        decline_rate * 100 * 0.45 + amount_signal + geography_signal
    ).clip(upper=100).round(1)
    accounts["risk_band"] = accounts["risk_score"].map(
        lambda score: "high" if score >= 65 else "medium" if score >= 35 else "low"
    )
    return accounts.sort_values(["risk_score", "account_id"], ascending=[False, True])
`
}

func riskPortfolioSummarySQL() string {
	return `/* @bruin
name: risk.portfolio_summary
type: duckdb.sql
materialization:
  type: table
depends:
  - risk.scored_accounts
columns:
  - name: risk_band
    type: varchar
    checks:
      - name: accepted_values
        value: [low, medium, high]
  - name: accounts
    type: bigint
  - name: average_risk_score
    type: double
  - name: total_transaction_value
    type: double
meta:
  web_view: chart
  web_chart_type: bar
  web_chart_x: risk_band
  web_chart_series: accounts
  web_chart_title: Account risk distribution
@bruin */

SELECT
    risk_band,
    count(*) AS accounts,
    round(avg(risk_score), 1) AS average_risk_score,
    round(sum(total_amount), 2) AS total_transaction_value
FROM risk.scored_accounts
GROUP BY risk_band
ORDER BY CASE risk_band WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END
`
}
