# Sail-backed Databricks E2E endpoint

This test-only helper adapts the HTTPS Thrift calls made by Databricks SQL
clients to Sail's Flight SQL server. The multi-warehouse Playwright test builds
and starts it automatically when the `databricks` variant is selected.

It validates Renart's Databricks asset lifecycle through the real client
boundary while using Sail for Spark SQL execution. It does **not** emulate
Databricks authentication, Unity Catalog permissions, Cloud Fetch, proprietary
protocol extensions, or the behavior of a managed Databricks runtime. Those
remain live-workspace test concerns.

Sling emits `USING DELTA` for Databricks destinations. Sail Flight SQL 0.6.6
uses a relative managed-table warehouse path that its Delta provider rejects,
so this adapter rewrites that storage clause to `USING PARQUET`. The E2E test
therefore covers SQL/materialization behavior, not Delta-specific storage.

The helper also adapts two protocol-boundary differences needed by Sling: it
renders Databricks positional parameters because Sail Flight SQL does not expose
prepared statements, and maps Sling's `information_schema.columns` lookup to
Spark's `DESCRIBE TABLE` result. Sail also lacks table rename, so the adapter
reproduces Databricks' full-refresh staging-table swap with CTAS plus a staging
table drop. For the same reason, `TRUNCATE` and predicate `DELETE` rebuild the
small E2E table with the rows that Databricks would retain.

The unusual nested module path is intentional: it lets this E2E-only binary use
the exact generated TCLIService types bundled inside the pinned Databricks Go
driver without adding test-emulator dependencies to Renart's production module.
