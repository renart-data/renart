# Snowflake import compatibility

Bruin v0.11.755 still imports `github.com/snowflakedb/gosnowflake`.
That module has no fix for GO-2026-6665 / GHSA-qqj6-54q6-cxv6. The fix
is in the official v2 module starting at v2.2.0.

Renart replaces the old import with this small, first-party adapter. All driver
implementation, SQL driver registration, configuration, context keys, and
protected logging come from `github.com/snowflakedb/gosnowflake/v2` v2.3.0.
The official module remains a versioned dependency so vulnerability tools can
continue checking it. No v1 driver source is copied or compiled.

Only the API consumed by Bruin is exposed. The sole signature adaptation is
`WithMultiStatement`: v1 returned `(context.Context, error)` with an always-nil
error; v2 returns just the context.

The root module's `internal/bruincompat` tests exercise this adapter, so the
normal backend CI includes them despite this directory being a nested module.
Remove the replacement and this directory once Bruin uses the v2 import itself.
Sling runs as a separate executable and is not linked through this module.
