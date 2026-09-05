# Plans

Ephemeral design documents: proposals, evaluations, and implementation plans
for work that has **not shipped** (or only partially). The current state of
what _is_ built lives in [`../architecture/`](../architecture/).

When a plan is implemented, fold the as-built reality (including deviations)
into the relevant `architecture/` doc and delete the plan — git history keeps
the original.

| Doc                                                                                          | Status                                                                                                                                                                       |
| -------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [architecture-maintainability-audit.md](architecture-maintainability-audit.md)               | investigation — prioritized convergence, boundary, test-cost, and scaling cleanup roadmap                                                                                    |
| [asset-name-path-independence.md](asset-name-path-independence.md)                           | design plan — make Bruin's explicit asset name independent from the Git definition path, without introducing a physical-output alias                                         |
| [dbt-assets.md](dbt-assets.md)                                                               | evaluation — enabling renart intelligence on existing dbt projects                                                                                                           |
| [data-browser.md](data-browser.md)                                                           | detailed implementation plan — observed connection discovery, safe object preview, reviewed handoffs, and Workbench integration                                              |
| [diagnostic-navigation-targets.md](diagnostic-navigation-targets.md) | researched proposal — typed error destinations, cold-tab/project-safe links, independent routed details, and durable Data Browser addresses |
| [distributed-freshness-log.md](distributed-freshness-log.md)                                 | investigated proposal — opt-in append-only warehouse receipt journals for recovery and trusted cross-installation freshness                                                  |
| [execution-parallelism.md](execution-parallelism.md)                                         | core + native DuckDB concurrency implemented — operator audits and wait telemetry remain                                                                                     |
| [materialization-reach.md](materialization-reach.md)                                         | proposed reach — guided advanced SQL modes, coverage timeline, Python pre-run diagnostic                                                                                     |
| [materialization-target-lifecycle.md](materialization-target-lifecycle.md)                   | runtime target safety implemented for DuckDB/Postgres/Snowflake/BigQuery/Databricks — rename/orphan workflow pending asset-name/path independence                            |
| [navigation-information-architecture-mocks.md](navigation-information-architecture-mocks.md) | design lab complete — nested workbench selected and verified in an interactive mock                                                                                          |
| [navigation-workbench-migration.md](navigation-workbench-migration.md)                       | release train 1 implemented on feature branch — convergence/regression gate pending; Data Browser and structure-aware drops remain separate expansion slices                 |
| [notebook-platform.md](notebook-platform.md)                                                 | core platform implemented — focused release evidence for transfer fidelity, restart/concurrency, performance, accessibility, and authenticated agent-client corpus remains   |
| [open-project-links.md](open-project-links.md)                                               | investigation — safe docs-to-local open intents, native protocol registration, and hosted routing                                                                            |
| [object-storage-assets.md](object-storage-assets.md)                                         | partial support + proposal — existing Load S3/GCS browsing, upstream-compatible Seed sources, schema preview, lineage, and storage write safety                              |
| [secret-management.md](secret-management.md)                                                 | local/env provider core and CLI administration implemented — file leases, team providers, and hosted run-scoped access remain                                                |
| [python-asset-sdk.md](python-asset-sdk.md)                                                   | phases 1–2 + upstream refresh + PyPI publication implemented — credential-free `query()`, ingestr-free uploads, editor/notebook parity; policy and protocol reach items open |
| [python-cross-connection-policy.md](python-cross-connection-policy.md)                       | proposal — opt-in per-environment connection scopes for Python SDK queries                                                                                                   |
| [questions.md](questions.md)                                                                 | open questions for the maintainer                                                                                                                                            |
| [workspace-command-handoff.md](workspace-command-handoff.md)                                 | focused follow-up — launcher handoff, remaining stateful CLI delegation, and eventual legacy-job retirement                                                                  |
| [semantic-deployment-impact.md](semantic-deployment-impact.md) | first warning-only vertical slice implemented — component-level behavior facts, exact cross-pipeline worlds, compatibility policy, and history remain |

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
