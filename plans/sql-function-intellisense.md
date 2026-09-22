# Dialect-aware SQL function IntelliSense

Status: initial DuckDB/PostgreSQL/ClickHouse slice and 22 September follow-up
implemented. Golyglot alpha.12 is published and pinned; no local module override
is required. As-built behavior is in
[SQL LSP architecture](../architecture/sql-lsp.md). This plan tracks extensions,
not a promise of complete dialect/function coverage.

The 22 September follow-up includes functions at empty prefixes and nested
argument positions while keeping columns ranked first. It also coalesces
concurrent inference for the same workspace revision and excludes canceled
inference from the graph cache. Immutable function descriptions are prepared
once per dialect, not for every completion request.
It also includes verified COALESCE/NULLIF (plus PostgreSQL GREATEST/LEAST),
named-option parameter highlighting for DuckDB table functions, and the SAMPLE
CTE recovery regression. The isolated oracle now checks 39 fixed observations
and an explicit CSV named-option/schema case.

## Ownership

Golyglot owns portable dialect facts: callable names, kinds, overloads, argument
and result shapes, and syntax context. Its catalog stays pure Go, deterministic,
offline, and independent of databases, credentials, LSP, or Monaco.

Renart owns lexical editor scope, candidate ranking, insertion behavior,
LSP/Monaco presentation, and future connection-specific UDF/extension overlays.
SQL editors and SQL embedded in Python must consume the same backend catalog.
Verification tools may query disposable engines; typing must never execute SQL.

## Remaining work

1. Extend engine probes beyond the current 39 fixed observations and CSV case. Check
   argument-dependent table schemas, optional/named arguments and variadic
   edge cases; do not promote the smoke corpus to blanket type coverage. Add
   argument-type-aware overload selection (currently arity-based). Expand
   named-argument support beyond DuckDB table options using verified metadata.
2. Extend verified parser-level special forms beyond the shipped conditional
   expressions, and add connection-specific UDF/extension overlays using the
   existing bounded catalog/SSE lifecycle.
3. Expand dialects independently: SQLite, MySQL/MariaDB, Trino, StarRocks and
   other locally verifiable engines. PostgreSQL wire compatibility alone does
   not authorize PostgreSQL builtin metadata.
4. Extend live coverage to checks/hooks and presentation datasets. Asset editors,
   notebook SQL, embedded Python and notebook-runtime column merging now have
   focused live checks; this is not exhaustive editor-surface coverage.
5. Continue the adversarial query matrix: quoted column replacement/escaping,
   recursive forward-reference rules by dialect, nested lambdas, lateral table
   functions and malformed multi-statement edits. Fix only reproduced failures.

## Safety and acceptance

- Never invent concrete return types for polymorphic calls, file-dependent
  table schemas or extension-only functions. Keep existing inference rules
  authoritative until replacement has equivalence tests.
- Test exact cursor SQL, dialect, wanted and forbidden candidates, ranking,
  catalog isolation/copy safety, and Function-kind transport. Confirm actual
  Monaco ordering in a focused browser test, including runtime notebook columns.
- Run disposable engines serially with resource limits and no user data. Probe
  only curated, side-effect-free calls; do not execute arbitrary discovered
  functions. Network/file functions need local fixtures or catalog-only evidence.
- Persist logs in repository artifacts. Do not overlap warehouse containers
  with memory-heavy release builds.
- A temporary local module replacement is for validation only. A merge-ready
  Renart dependency must point to a published Golyglot version.

## Evidence and references

The baseline `Engine.Complete` had no function provider; both web adapters also
filtered out Function-kind items. Signature help covered INSERT values only.
FROM completion omitted lexical CTEs. Initial red evidence is retained locally
in `.test-artifacts/function-intellisense/01-red-shapes.log`.

- [DuckDB function inventory](https://duckdb.org/docs/current/sql/meta/duckdb_table_functions):
  names, kinds, parameters, parameter types, varargs and scalar/aggregate result
  types are introspectable with `duckdb_functions()`.
- [PostgreSQL pg_proc](https://www.postgresql.org/docs/current/catalog-pg-proc.html):
  overloads, argument modes/defaults, variadics, scalar/set return shape.
- [ClickHouse functions](https://clickhouse.com/docs/reference/system-tables/functions)
  and [table functions](https://clickhouse.com/docs/reference/system-tables/table_functions):
  separate inventories. Documentation strings are not machine-verified types.
