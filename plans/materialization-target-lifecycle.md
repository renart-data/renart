# Materialization target lifecycle safety

Status: investigated — Renart-owned compatibility layer and review UX proposed

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

## Constraint and rejected approaches

Renart does not control Bruin upstream and should not depend on an accepted
Bruin patch or maintain a fork. The compatibility behavior therefore belongs
at Renart's existing direct-execution seam, while Bruin's public Go
materializers remain the source of warehouse DDL.

Do not implement this as:

- an automatic retry with `--full-refresh` after an incremental statement
  fails — the error cannot reliably distinguish absence from permissions,
  qualification, or a partial write;
- hidden pre/post hooks — they would change user hook ordering and duplicate
  warehouse materialization SQL;
- local-state inference that a target is absent — Renart materialization facts
  can be stale after external DDL; or
- vendoring/replacing the Bruin module — that is a fork under another name.

## Proposed Renart-owned runtime contract

Add a provider-neutral target inspection result inside Renart:

```text
TargetState {
  existence: present | absent | unknown
  kind: table | view | other | unknown
  qualified_name
}
```

Each Renart lifecycle adapter uses only the runtime connection's public Bruin
interfaces (`GetDatabaseSummaryForSchemas`, table discovery, and the normal
query executor where available). A small adapter registry fills gaps with a
targeted metadata query; it does not copy materialization SQL. An unavailable,
unauthorized, capped, or ambiguous lookup returns `unknown`, not `absent`.

The process-local remote-catalog cache is useful positive evidence for rename
warnings, but it is not sufficient for execution preflight: it intentionally
cannot prove absence and currently does not preserve relation kind. Execution
uses a fresh, targeted lookup with a short timeout.

Renart already owns the key integration points:

- `newDirectStringExecutionMaterializer` and
  `newDirectQueryBatchExecutionMaterializer` construct the pinned Bruin
  materializers used by both render and execution;
- planned pipeline execution builds an executor per asset/unit; and
- `runDirectTask` runs after developer-environment prefixes have been applied
  and before the Bruin operator executes.

The lifecycle preflight should run before constructing the per-asset executor,
then choose either the configured Bruin materializer or the existing Bruin
full-refresh materializer. This reuses Bruin's SCD2 initializer and
warehouse-specific create/replace SQL without changing Bruin. Single-asset
runs can use the same selection directly. The legacy multi-asset sequential
path should be normalized through the existing version-3 per-unit executor so
the decision is never shared accidentally between assets.

### Missing-target bootstrap

For an ordinary table strategy whose target is positively absent, Renart should
select Bruin's existing full-refresh materializer before executing any
incremental statement:

- `append`, `merge`, `delete+insert`, `truncate+insert`, and `time_interval`:
  use Bruin's warehouse-specific create/replace rendering for that run's
  SELECT;
- SCD2: use the existing SCD2 full-refresh initializer so its bookkeeping
  columns and semantics are preserved;
- `ddl`: retain its idempotent DDL behavior;
- `refresh_restricted`: initially stop with an actionable first-run error. A
  later adapter may opt in only when it can provide an atomic create-if-absent
  primitive; a racy create/replace would violate the restriction.

This is a pre-execution selection, not an error retry. Renart's scheduler,
direct run, dry-run/render, and review plan must all expose the conditional
bootstrap. The target is inspected again immediately before execution; where
the adapter cannot make creation atomic, the review states that an external DDL
race remains possible. Unknown state preserves the configured strategy and
emits an actionable warning instead of claiming that bootstrap is safe.

### Table/view transitions

Use **Full refresh** as the explicit portable transition action:

1. Ordinary materialization against a positively observed opposite kind stops
   before DDL and reports that full refresh is required.
2. Full refresh inspects the target. Snowflake and BigQuery continue using
   Bruin's existing mismatch handlers; other supported Renart adapters execute
   one kind-specific drop through the public connection query interface before
   selecting Bruin's full-refresh materializer.
3. `unknown` state keeps today's execution semantics but the review identifies
   that compatibility could not be verified.
4. The rendered operation list includes the conditional lookup/drop; it must
   not imply an atomic swap on adapters that cannot provide one.

Where an adapter supports transactional or rename/swap replacement, Renart may
keep the prior object available until the new object is ready. The portable
fallback is a drop/create window, so the full-refresh confirmation must describe
that availability risk. Adapters without reliable kind inspection remain
unsupported for automatic transition rather than guessing with two `DROP`
statements.

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

1. Add the Renart target-lifecycle adapter interface and contract tests. Start
   with DuckDB/Postgres and the already-special-cased Snowflake/BigQuery paths;
   add Databricks only with a real or faithful Sail-backed kind probe.
2. Feed the preflight result into the existing direct materializer construction
   seam and normalize legacy pipeline runs through per-unit execution. Make
   render/review show the conditional selection.
3. Add run-review blockers/warnings and live tests for absent target,
   table→view, and view→table on the local/live warehouse matrix.
4. Add rename preflight using positive current-environment observations, then a
   separately confirmed post-success cleanup action.
5. Fold the shipped contract into `architecture/backend.md` and the rename UX
   into `architecture/asset-editing.md`.

## Acceptance checks

- Every opted-in SQL adapter and incremental strategy has an absent-target test
  and never executes its destructive/incremental statement before bootstrap.
- An unknown or unsupported target state never silently selects full refresh.
- A first SCD2 run creates the SCD2 bookkeeping schema, not a plain CTAS table.
- Both object-kind transition directions either succeed under explicit full
  refresh or fail before destructive work with an actionable message.
- A rename cannot silently overwrite a positively observed current-environment
  relation and never claims other environments are clear.
- Failed creation of the new renamed target leaves cleanup unavailable; a
  successful new target never causes automatic deletion of the old one.
