# Plans

Plans contain only unfinished work. Current implementation belongs in
[architecture](../architecture/); Git history retains completed designs and
release scratchpads. Reviewed against the local working tree on 9 September
2026. Implemented does not mean release-certified.

The groups below distinguish follow-ups, evidence collection, and unselected
ideas. They are not authorization to implement every item. Keep required
decisions and dependencies in each plan; there is no global questions scratchpad.

## Active follow-ups

| Plan | Remaining boundary / prerequisite |
| --- | --- |
| [Navigation arrival feedback](navigation-arrival-feedback.md) | Shared lifecycle and initial owners implemented; presentation/run and further section adapters remain |
| [Notebook Data Browser drops](notebook-data-browser-drops.md) | Reviewed source-block insertion using existing notebook transactions; plan only |
| [Shared preview row loading](preview-row-loading.md) | Inspect, Data Browser, notebook and query previews implemented; authored table presentation adapters remain |
| [Workspace command handoff](workspace-command-handoff.md) | Stateful CLI delegation, short-lived authority, safe launcher handoff; highest-priority correctness boundary |
| [Local secret lifecycle](secret-management.md) | Sensitive-file leases, migration/crash evidence, remaining subprocess boundaries; local vault already exists |
| [Data Browser](data-browser.md) | Positive Usage matching, notebook handoff, pagination/cache provenance, shared observations; browsing/search/drops already exist |
| [Object-storage assets](object-storage-assets.md) | Richer discovery, schema preview, URI freshness/write claims, GCS tree; Seed requires a Bruin-compatible source contract |
| [Asset name/path independence](asset-name-path-independence.md) | Consistent create/rename/file identity; does not alias or rename a physical Source table |
| [Materialization rename safety](materialization-target-lifecycle.md) | Collision preflight and separately confirmed orphan cleanup after name/path work |
| [Advanced materialization](materialization-reach.md) | Guided advanced strategies, coverage-gap UX, static Python materialize diagnostic |
| [Execution operator audits](execution-parallelism.md) | Additional proven resource families and meaningful wait visibility; shared unit scheduling already exists |
| [Semantic deployment impact](semantic-deployment-impact.md) | Component facts, exact producer-pinned worlds, compatibility policy, retained reports and inference evidence |

## Verification and measurement

These need evidence or a selected bottleneck, not another implementation of
their already-built core.

| Plan | Required evidence |
| --- | --- |
| [Notebook release evidence](notebook-platform.md) | Transfer fidelity, restart/concurrency, platform budgets, accessibility, version-specific authenticated clients |
| [Performance evidence](performance-evidence.md) | E2E timings, workspace/SSE size and fan-out, runtime resources, cold interactions, broker/lineage profiles before optimization |

Use [the local verification workflow](../architecture/testing.md) for durable,
scoped results. Record failures, skips and interruptions; a focused pass is not
the full release gate.

## Parked proposals

These need an explicit product, policy, or trust decision before implementation.
Revalidate historical external research when selecting one.

| Plan | Decision / dependency |
| --- | --- |
| [Python query connection policy](python-cross-connection-policy.md) | Opt-in read scopes; separate from preventing writes on read-only connections |
| [Team and hosted secret providers](secret-providers.md) | Select provider and identity/lease model; hosted work requires a separate trust architecture |
| [Open-project links](open-project-links.md) | Safe launcher, reviewed clone/trust model, then native protocol packaging |
| [Distributed freshness journal](distributed-freshness-log.md) | Accept receipt/trust/divergence contract; local state remains authoritative |
| [dbt assets](dbt-assets.md) | Select dbt project support; refresh compilation/artifact assumptions and use native Golyglot |

## Closing a plan

1. Verify implementation and evidence against current code, not old checkboxes.
2. Fold only missing as-built contracts and decisions into architecture.
3. Keep unresolved work in a focused plan with prerequisites and acceptance.
4. Delete the completed plan; do not create a permanent plans archive.

Completed connection/environment navigation, navigation/migration studies, the broad architecture audit, the Python
SDK implementation plan, empty questions, and v0.5.1 release scratchpads have
been retired. Navigation rationale is in frontend architecture, SDK/runtime
contracts are in backend/notebook architecture, and retained local release
evidence is described in testing. Prototype-code removal is verified separately
from documentation cleanup; the semantic impact playground is not retired.
