# Renart

Build data workflows you can maintain.

Renart is an open source data platform for moving, transforming and analyzing data across your data stack. Build and run SQL and Python pipelines alongside notebooks and dashboards in one visual workspace, with type checking, scheduling and everything defined as code in Git.

Public alpha · Apache 2.0 · Runs locally

```bash
curl -LsSf getrenart.com/install.sh | sh
```

[Build your first pipeline](https://getrenart.com/docs/quickstart.md) · [Installation options](https://getrenart.com/docs/installation.md) · [Source on GitHub](https://github.com/renart-data/renart)

Renart is in public alpha. Expect rough edges and changes before the first stable release. [What to expect](https://getrenart.com/docs/alpha-status.md)

## From getting data in to putting it to work.

- **Bring the data together.** Load from databases, files, object storage and HTTP APIs. Move data into and out of supported warehouses as part of the pipeline. [Data loading](https://getrenart.com/docs/asset-types/load-assets.md)
- **Build with SQL and Python.** Develop transformations with type checking and intelligent completion. Connect the assets, inspect the results and schedule the work. [Pipeline development](https://getrenart.com/docs/editing-assets/asset-editor.md)
- **Make the results useful.** Explore data in notebooks and build dashboards and reports. Keep the analysis and its definitions alongside the pipelines in Git. [Notebooks and analysis](https://getrenart.com/docs/notebooks/overview.md)

## Make changes with context.

A pipeline is easier to maintain when the code, dependencies and results are in view together.

- **Catch mistakes while you edit.** Get SQL and Python diagnostics and completion in the editor. Check known columns and types before running against your database.
- **See what a change affects.** Follow dependencies on the canvas. Staleness highlights changed definitions and downstream assets that may need rebuilding.
- **Review before you run.** Preview SQL results with Inspect, review the file diff and choose what to materialize. Scheduled runs use a deployed snapshot.

## Build, explore and run in one workspace.

Move between pipelines, notebooks and scheduled runs with the same assets, schemas and dependencies.

- **Load: Bring data into the workflow.** Choose a source, destination and load mode in the workspace. Make the transfer part of the pipeline, alongside its transformations. [Load assets](https://getrenart.com/docs/asset-types/load-assets.md)
- **Build: See the pipeline while you write it.** Keep the Explorer, pipeline-aware editor, live canvas, data preview, and asset workbench in one view. [Tour of the interface](https://getrenart.com/docs/workspace/interface-tour.md)
- **Lineage: Follow the effect of a change.** See which assets depend on each other. Review changed definitions and downstream staleness before choosing what to rebuild. [The pipeline canvas](https://getrenart.com/docs/workspace/pipeline-canvas.md)
- **Notebook: Explore before you make it permanent.** Mix SQL, Python, text, controls, and charts locally, then promote useful work into a pipeline asset. [Notebooks](https://getrenart.com/docs/notebooks/overview.md)
- **Dashboard: Turn typed datasets into a dashboard.** Compose KPIs, tables, charts, and controls visually while keeping the definition version-controlled. [Dashboards & reports](https://getrenart.com/docs/presentations/overview.md)
- **Report: Keep the narrative beside the evidence.** Combine Markdown, checked visualizations, and tables in a report stored with the rest of the project. [Dashboards & reports](https://getrenart.com/docs/presentations/overview.md)
- **Schedule: Run a reviewed snapshot on a schedule.** Choose the pipeline, environment, cron, timezone, and catch-up policy, then keep that deployment pinned until you advance it. [Deployments & schedules](https://getrenart.com/docs/scheduling/overview.md)
- **Runs: Understand what happened in a run.** Inspect per-asset timing, status, events, output, and the exact error attached to a failed run. [Runs & history](https://getrenart.com/docs/workspace/runs-and-history.md)

## Your infrastructure. Your repository.

- **Work with the stack you have.** Start locally with DuckDB or connect to your databases and warehouses. Bring in files, object storage and HTTP API data where the workflow needs it. Includes DuckDB, Postgres, ClickHouse, StarRocks, Trino, Snowflake, BigQuery, Redshift, Databricks, S3 / GCS and HTTP APIs. [Connections and environments](https://getrenart.com/docs/connections-environments/managing-connections.md)
- **Keep the work reviewable.** Pipelines, notebooks, dashboards, reports and schedule definitions are plain files. Visual edits become changes you can review, commit and revert in Git. [How Renart stores your work](https://getrenart.com/docs/how-it-works.md)
- **Run it in your environment.** No Renart account or hosted control plane is required. You choose the systems Renart connects to. Credentials and machine-local run history stay separate from authored definitions. [Security and privacy](https://getrenart.com/docs/reference/security-and-privacy.md)

## Put Renart to work.

Open a Git repository, build a small pipeline and see how the workflow fits. [Follow the quickstart](https://getrenart.com/docs/quickstart.md)

- [Documentation](https://getrenart.com/docs.md)
- [Documentation index for language models](https://getrenart.com/llms.txt)
- [Compare Renart with other tools](https://getrenart.com/compare.md)
- [Discord](https://discord.gg/jTH758KNP8)
