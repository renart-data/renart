# Plans

Ephemeral design documents: proposals, evaluations, and implementation plans
for work that has **not shipped** (or only partially). The current state of
what *is* built lives in [`../architecture/`](../architecture/).

When a plan is implemented, fold the as-built reality (including deviations)
into the relevant `architecture/` doc and delete the plan — git history keeps
the original.

| Doc | Status |
| --- | --- |
| [asset-name-path-independence.md](asset-name-path-independence.md) | design plan — make Bruin's explicit asset name independent from the Git definition path, without introducing a physical-output alias |
| [dbt-assets.md](dbt-assets.md) | evaluation — enabling renart intelligence on existing dbt projects |
| [distributed-freshness-log.md](distributed-freshness-log.md) | investigated proposal — opt-in append-only warehouse receipt journals for recovery and trusted cross-installation freshness |
| [execution-parallelism.md](execution-parallelism.md) | core + native DuckDB concurrency implemented — operator audits and wait telemetry remain |
| [materialization-reach.md](materialization-reach.md) | proposed reach — guided advanced SQL modes, coverage timeline, Python pre-run diagnostic |
| [materialization-target-lifecycle.md](materialization-target-lifecycle.md) | runtime target safety implemented for DuckDB/Postgres/Snowflake/BigQuery/Databricks — rename/orphan workflow pending asset-name/path independence |
| [notebook-platform.md](notebook-platform.md) | phases 0–1 + visualization/source/export core + local MCP preview + typed notebook parameters implemented — source promotion, remaining release hardening, dashboard filter hosts, and dashboards/reports remain |
| [open-project-links.md](open-project-links.md) | investigation — safe docs-to-local open intents, native protocol registration, and hosted routing |
| [object-storage-assets.md](object-storage-assets.md) | partial support + proposal — existing Load S3/GCS browsing, upstream-compatible Seed sources, schema preview, lineage, and storage write safety |
| [pipeline-readiness-and-rendering.md](pipeline-readiness-and-rendering.md) | core complete — Phases 0a/0b and 1–3 plus version-controlled schedules and bounded retention have shipped; single-workspace command handoff, resource claims, and rolling compatibility cleanup remain |
| [polyglot-typechecking.md](polyglot-typechecking.md) | evaluation — cached compact analysis, richer schema constraints, and optional SQL lint policy |
| [secret-management.md](secret-management.md) | local/env provider core and CLI administration implemented — file leases, team providers, and hosted run-scoped access remain |
| [python-asset-sdk.md](python-asset-sdk.md) | phases 1–2 + upstream refresh + PyPI publication implemented — credential-free `query()`, ingestr-free uploads, editor/notebook parity; policy and protocol reach items open |
| [python-cross-connection-policy.md](python-cross-connection-policy.md) | proposal — opt-in per-environment connection scopes for Python SDK queries |
| [questions.md](questions.md) | open questions for the maintainer |

Recently folded away (git history keeps them): `docs-alpha.md` and
`landing-page.md` → `architecture/docs.md`; `notebook-intellisense.md` →
`architecture/sql-lsp.md`; `ingestr-feature-flag.md`,
`project-settings-and-workspaces.md`, and `cli-v1.md` →
`architecture/backend.md` + `architecture/frontend.md`; `schema-derivation.md` →
`architecture/asset-editing.md` + `architecture/sql-lsp.md`;
`remote-table-intellisense.md` → `architecture/sql-lsp.md`;
`cross-pipeline-dependencies.md` → `architecture/backend.md`,
`architecture/staleness.md`, `architecture/sql-lsp.md`, and
`architecture/asset-editing.md`.
