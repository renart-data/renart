# Materialization target lifecycle safety

Status: investigated — upstream Bruin contract and Renart review UX proposed

## Goal

Make a materialized asset's physical target lifecycle predictable when:

- an incremental strategy runs before its target exists;
- an asset name changes and the old target becomes unowned; or
- the declared object kind changes between `table` and `view`.

The filesystem declaration remains authoritative, but a declaration alone
cannot prove the current state of every environment's warehouse. Renart should
therefore distinguish declared intent, positively observed target state, and
unknown state rather than inferring absence from a failed or partial catalog
lookup.

## Bruin behavior today

This audit covers Renart's pinned Bruin `v0.11.691` and the corresponding code
on Bruin `main` as checked in August 2026.

### A missing target is not bootstrapped for incremental strategies

Bruin's generic materializer changes a table strategy to `create+replace` only
when the run explicitly requests full refresh (except `ddl`, and except the
generic materializers when `refresh_restricted` is set). Without full refresh:

| Strategy | First-run behavior when target is absent |
| --- | --- |
| `create+replace` / default table | Creates the target |
| `ddl` | Uses `CREATE TABLE IF NOT EXISTS` |
| `append` | Starts with `INSERT INTO`; target must exist |
| `merge` | Starts with `MERGE`; target must exist |
| `delete+insert` | Deletes/inserts against the target; target must exist |
| `truncate+insert` | Starts with `TRUNCATE TABLE`; target must exist |
| `time_interval` | Deletes/inserts against the target; target must exist |
| SCD2 incremental paths | Read/merge the target; documented initial run is full refresh |

Ingestr/Load destinations are a separate runtime: the destination connector
owns its own create/replace/incremental behavior and must not be silently routed
through the SQL bootstrap described here.

The immediate workaround is therefore an explicit **Full refresh** for the
first SQL-table run. Automatically retrying a failed incremental statement as
create-and-replace is not safe: it relies on warehouse-specific error strings,
can hide permission or qualification failures, and may rerun non-idempotent
source SQL.

### Object-kind replacement is only partially handled

- Snowflake inspects `INFORMATION_SCHEMA.TABLES` and drops a mismatched table or
  view only when the run is a full refresh.
- BigQuery similarly deletes a target with a type (or partition/cluster)
  mismatch only during full refresh.
- Databricks' view materializer explicitly drops a table before creating the
  view, while its table create/replace path drops a table and does not
  symmetrically remove an existing view.
- Other materializers commonly emit `CREATE OR REPLACE VIEW`, CTAS, or a
  drop/create table sequence without a shared relation-kind preflight. Whether
  that can replace the opposite object kind is left to the warehouse.

Consequently, saving a table/view change is not sufficient to make an ordinary
materialization portable. Renart's render view already exposes the Snowflake
and BigQuery full-refresh-only compatibility stages, but there is no common
execution contract.

## Proposed runtime contract

Add a provider-neutral target inspection result to Bruin's materialization
operator boundary:

```text
TargetState {
  existence: present | absent | unknown
  kind: table | view | other | unknown
  qualified_name
}
```

Each warehouse adapter supplies the metadata lookup and kind-specific drop or
replacement operation. An unavailable/unauthorized lookup returns `unknown`,
not `absent`. Renart consumes the same contract through Bruin instead of
maintaining a second matrix of information-schema queries.

### Missing-target bootstrap

For an ordinary table strategy whose target is positively absent, Bruin should
select a strategy-specific bootstrap before executing any incremental
statement:

- `append`, `merge`, `delete+insert`, `truncate+insert`, and `time_interval`:
  create the table from that run's rendered SELECT, using the adapter's safe
  create/replace implementation;
- SCD2: use the existing SCD2 full-refresh initializer so its bookkeeping
  columns and semantics are preserved;
- `ddl`: retain its idempotent DDL behavior;
- `refresh_restricted`: allow creation of a positively absent target, but never
  replace an existing one.

This must be an upstream Bruin capability, not a Renart error retry, so CLI,
scheduler, dry-run/render, and browser execution agree. The plan/review output
must identify the conditional bootstrap. An existence race should be handled
by the adapter's atomic/idempotent primitive where available; otherwise return
a specific target-state conflict and require a re-plan.

### Table/view transitions

Use **Full refresh** as the explicit portable transition action:

1. Ordinary materialization against a positively observed opposite kind stops
   before DDL and reports that full refresh is required.
2. Full refresh inspects the target, emits the correct kind-specific drop, and
   creates the declared object kind.
3. `unknown` state keeps today's execution semantics but the review identifies
   that compatibility could not be verified.
4. The rendered operation list includes the conditional lookup/drop; it must
   not imply an atomic swap on adapters that cannot provide one.

Where an adapter supports transactional or rename/swap replacement, it may keep
the prior object available until the new object is ready. The portable fallback
is a drop/create window, so the full-refresh confirmation must describe that
availability risk.

## Asset-name changes and orphaned targets

An effective Bruin asset-name change also changes the default physical target.
It should remain a Git/source refactor, not an implicit cross-environment DDL
operation.

Before saving a rename, Renart should preview:

- old and new effective targets after the selected environment's schema prefix;
- whether the asset owns a materialized target at all (source placeholders do
  not);
- a positive current-environment observation of the old target and any object
  already occupying the new target; and
- the explicit warning that every other environment is unknown and the old
  target will remain there.

A positively observed new-target collision blocks the rename by default.
Unknown/partial catalog state produces a warning, never a false “available”
claim. The source refactor updates dependencies but does not drop the old
target.

After the renamed asset has materialized successfully in one environment,
Renart may offer a separate **Clean up old target** action for that environment.
It must re-inspect the relation kind, show the exact drop, require typed
confirmation, and record the outcome. Cleanup is never fanned out implicitly
to other environments. This extends the name/path work in
[asset-name-path-independence.md](asset-name-path-independence.md); it does not
introduce a separate physical output alias.

## Delivery order

1. Propose the target-state/bootstrap interface upstream in Bruin with adapter
   contract tests for DuckDB/Postgres, Snowflake, BigQuery, and Databricks.
2. Make the Bruin render result expose conditional bootstrap and kind-transition
   operations; retain CLI/full-refresh parity.
3. Add Renart run-review blockers/warnings and live tests for absent target,
   table→view, and view→table on the local/live warehouse matrix.
4. Add rename preflight using positive current-environment observations, then a
   separately confirmed post-success cleanup action.
5. Fold the shipped contract into `architecture/backend.md` and the rename UX
   into `architecture/asset-editing.md`.

## Acceptance checks

- Every SQL incremental strategy has an absent-target test and never executes
  its destructive/incremental statement before bootstrap.
- A first SCD2 run creates the SCD2 bookkeeping schema, not a plain CTAS table.
- Both object-kind transition directions either succeed under explicit full
  refresh or fail before destructive work with an actionable message.
- A rename cannot silently overwrite a positively observed current-environment
  relation and never claims other environments are clear.
- Failed creation of the new renamed target leaves cleanup unavailable; a
  successful new target never causes automatic deletion of the old one.
