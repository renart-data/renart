# Materialization rename and orphan-target safety

Status: active follow-up. Fresh target inspection, positive-absence bootstrap,
kind-mismatch blockers, explicit full-refresh replacement, and conditional
render/review stages are implemented for the audited adapters. Current runtime
contracts live in [backend](../architecture/backend.md).

The remaining rename flow depends on
[asset-name-path-independence.md](asset-name-path-independence.md).
A logical rename stays a Git/source refactor, never implicit cross-environment
DDL or a new physical-output alias.

## 1. Reviewed rename preflight

Before saving an effective asset-name change, show:

- old/new effective physical targets after the selected environment's prefix;
- whether the asset actually owns a materialized output (Source assets do not);
- a positive observation of the old target and any new-target collision;
- the warning that other environments are unknown and retain their old targets.

A positively observed collision blocks by default. Unknown or partial discovery
warns rather than claiming availability. Dependency/source updates follow the
name/path contract and do not drop or move warehouse data. Revalidate observed
identity at the mutation boundary; a preview is not a cross-transaction lock.

## 2. Separate old-target cleanup

Only after successful materialization of the renamed asset in the selected
environment may Renart offer a separate cleanup action. Re-inspect the exact
relation kind, show the proposed drop, require typed confirmation, enforce
connection/environment policy and resource coordination, and record the outcome.

No automatic cleanup, cross-environment fan-out, or drop inferred from a missing
cached observation. Failed creation of the new target keeps cleanup unavailable.
Never offer owned-output cleanup for a Source asset.

## 3. Adapter and release evidence

Retain contract coverage for DuckDB, PostgreSQL, Snowflake, BigQuery, and
Databricks; verify the actual selected adapters before claiming broader reach.
Real DuckDB and Sail-backed Databricks tests exercise local lifecycle boundaries.
Managed-warehouse smoke evidence is separate from simulated/contract coverage.

Additional target-kind adapters must return unknown conservatively until their
kind/existence semantics are proven. Positive absence can bootstrap through
Bruin's materializer; unknown state must never select full refresh. Preserve
restricted-refresh errors and SCD2-specific first-run bookkeeping.

Acceptance includes absent targets, both table/view transitions, permission
failure, stale preview, rename collisions, successful/failed new materialization,
and explicit old-target cleanup with exact file/warehouse effects.

## Delivery and closure

Implement path-stable name changes first, then preflight, then separately
confirmed cleanup. Adapter additions are independent reviewed slices. Fold the
rename UX into [asset editing](../architecture/asset-editing.md) and the physical
cleanup contract into backend/staleness docs before deleting this plan.
