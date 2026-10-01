# Renart

> Renart is an open source data platform for moving, transforming and analyzing data across your data stack. Build and run SQL and Python pipelines alongside notebooks and dashboards in one visual workspace, with type checking, scheduling and everything defined as code in Git.

Renart is a local application. Install the `renart` binary and start it inside a Git repository to open the workspace in a native window or a browser. There is no Renart account, hosted service or public web API. Pipelines run against the systems you choose, from embedded DuckDB to PostgreSQL, Snowflake, BigQuery and other databases and warehouses. Renart is open source under the Apache 2.0 license and is in public alpha.

## When to use Renart

Renart fits when someone wants to:

- build SQL and Python data pipelines that are stored as plain, reviewable files in their own Git repository;
- load data from databases, files, object storage or HTTP APIs and transform it in the same pipeline;
- check SQL columns, types and dependencies before running anything against a database;
- follow lineage on a canvas and rebuild only the assets that are out of date after a change;
- explore data in notebooks and build dashboards and reports from the same definitions;
- deploy a reviewed pipeline snapshot and run it on a schedule.

Renart is not the right tool when the job needs a hosted multi-user service or a public web API to integrate with: Renart runs where you start it and offers neither. During the public alpha, evaluate it on non-critical data before depending on it.

## Using Renart from an agent

- Install with `curl -LsSf getrenart.com/install.sh | sh` ([Installation](https://getrenart.com/docs/installation.md)).
- Inside a project, the `renart` CLI scaffolds, inspects and runs pipelines: `renart init`, `renart ls assets`, `renart type-check <pipeline>`, `renart plan <pipeline>` and `renart run <pipeline>` ([Work from the terminal](https://getrenart.com/docs/cli/work-from-terminal.md), [CLI reference](https://getrenart.com/docs/reference/cli.md)).
- `renart mcp --workspace .` serves notebook tools to a local coding agent over stdio. It is a developer preview ([Notebook agents](https://getrenart.com/docs/notebooks/agents.md)).
- Python assets read other project assets with `query()` from the built-in `renart` SDK ([Python assets](https://getrenart.com/docs/asset-types/python-assets.md)).
