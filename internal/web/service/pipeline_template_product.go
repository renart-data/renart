package service

import (
	"fmt"

	"github.com/google/uuid"
)

// productProjectFiles gives a new Product analytics project a notebook and a
// dashboard on the pipeline's tables, so analysis is part of the demo from the
// start. Both read product_analytics' DuckDB connection and never write to it.
func productProjectFiles() map[string]string {
	return map[string]string{
		"notebooks/product-activity/notebook.yml":       productNotebookYAML(uuid.NewString()),
		"notebooks/product-activity/daily_activity.sql": productNotebookDailyActivitySQL(),
		"notebooks/product-activity/plan_journeys.sql":  productNotebookPlanJourneysSQL(),
		"dashboards/product-overview.dashboard.yml":     productDashboardYAML(),
	}
}

func productNotebookYAML(id string) string {
	return fmt.Sprintf(`version: 2
id: %s
title: Product activity
parameters:
  - id: plan
    label: Plan
    type: select
    default: all
    options:
      values:
        - all
        - free
        - team
        - enterprise
blocks:
  - markdown:
      id: intro
      content: |-
        # Product activity

        This notebook reads the tables the product_analytics pipeline builds. Each SQL cell copies its result into a local DuckDB session, so exploring here never changes the pipeline's tables.
  - cell: daily_activity
  - visualization:
      id: daily_activity_chart
      source: daily_activity
      definition:
        encoding:
          x:
            field: activity_date
            label: Day
          "y":
            - field: daily_active_users
              label: Active users
        title: Daily active users
        type: line
        version: 1
  - markdown:
      id: journeys_intro
      content: |-
        ## Users by plan

        Choose a plan to filter the journeys below. The control's value is passed to the query as a typed SQL literal.
  - control: plan
  - cell: plan_journeys
`, id)
}

func productNotebookDailyActivitySQL() string {
	return `/* @bruin
id: daily_activity
type: duckdb.sql
class: notebook
connection: duckdb-default
meta:
  renart_notebook_snapshot_mode: sample
  renart_notebook_snapshot_row_limit: "10000"
@bruin */

select activity_date, daily_active_users, sessions, events
from product.daily_active_users
order by activity_date
`
}

func productNotebookPlanJourneysSQL() string {
	return `/* @bruin
id: plan_journeys
type: duckdb.sql
class: notebook
connection: duckdb-default
meta:
  renart_notebook_snapshot_mode: sample
  renart_notebook_snapshot_row_limit: "10000"
@bruin */

select user_name, plan, country, event_count, session_count, is_activated
from product.user_journeys
where {{ parameter.plan }} = 'all' or plan = {{ parameter.plan }}
order by event_count desc
`
}

func productDashboardYAML() string {
	return `version: 1
id: product_overview
title: Product overview
datasets:
  daily_activity:
    asset: product.daily_active_users
  activation_funnel:
    asset: product.activation_funnel
visualizations:
  - id: daily_active_users
    dataset: daily_activity
    definition:
      encoding:
        x:
          field: activity_date
          label: Day
        "y":
          - field: daily_active_users
            label: Active users
      title: Daily active users
      type: line
      version: 1
  - id: activation_funnel
    dataset: activation_funnel
    definition:
      encoding:
        x:
          field: step
          label: Step
        "y":
          - field: users
            label: Users
      title: Activation funnel
      type: bar
      version: 1
layout:
  - visualization: daily_active_users
    width: 12
    height: 4
  - visualization: activation_funnel
    "y": 4
    width: 12
    height: 4
`
}
