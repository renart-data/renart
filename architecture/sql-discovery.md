# SQL discovery coverage

Audit of the pinned Bruin v0.11.700 clients and Renart's service adapters,
2026-09-09. `TestSQLWarehouseDiscoveryCoverage` covers every connection type
advertised for SQL assets and forces an explicit review when that set changes.
This is a source/contract audit, not a claim that every cloud warehouse was
tested with live credentials.

The Data Browser uses `SQLService.NamespaceChildren` for engines with native
catalogs. The lower `sqlnamespace` package describes typed catalog/database/schema
scopes and returns only their immediate children; it owns no credentials, pools,
HTTP handlers or caches. The service supplies the existing native connection and
feeds table observations into the same remote catalog cache used by SQL/LSP.
Empty schemas remain visible without listing their tables. Expanding one catalog
never enumerates its siblings or changes the connection's default catalog.

SQL pickers retain the compatibility `Databases`/`Tables` API. Its
`sqlDiscoveryAdapter` fills gaps in the pinned clients, without Ingestr. Trino's
compatibility API calls catalogs "databases" and can list all tables in one
catalog; the new browser does not use that eager path. MySQL/Vitess/PlanetScale
remain two-level database/table trees. StarRocks/Doris compatibility discovery is
explicitly qualified against the connection default. Renart's metadata adapters
quote identifiers individually and do not issue `USE` or mutate data; upstream
native limitations below are unchanged.

| Platforms | Database/table methods | Scope and limitations |
| --- | --- | --- |
| StarRocks, Doris | Renart namespace provider | Catalog → database → table; configured/default catalog is labelled, not a visibility restriction |
| Trino | Renart namespace provider | Catalog → schema → table; lazy native SHOW queries |
| Databricks | Renart namespace provider | Unity Catalog → schema → table; native SHOW CATALOGS / SCHEMAS IN / TABLES IN |
| DuckDB, MotherDuck | Renart namespace provider | Attached catalog → schema → table; excludes internal system/temp catalogs, includes empty schemas |
| MySQL, Vitess, PlanetScale | Renart adapter | Databases and tables; no artificial catalog layer |
| BigQuery | Native | Datasets/tables in the configured project, not project discovery |
| Postgres, Redshift | Native shared Postgres client | Databases listed, but information_schema can only enumerate the connected database's tables |
| Snowflake | Native | Databases and schemas; upstream metadata identifier interpolation needs hardening |
| ClickHouse, Athena | Native | Databases and tables |
| SQL Server, Synapse | Native shared SQL Server client | Upstream uses USE and omits table schemas; needs a non-session-mutating, schema-preserving adapter |
| Vertica | Native | Database plus schema-qualified tables |
| Fabric, Dremio, Oracle, Sail, Spark | Missing | Select is available, but neither database nor table discovery is implemented |

## Catalog identity and connection defaults

`dataaddress.Address.catalog` is optional for old links but explicit on new
catalog-aware objects, revision-bound tokens and namespace nodes. Resolution
re-lists the exact parent and requires an exact match; it never searches every
catalog or silently falls back after permission errors. Legacy StarRocks/Doris
database addresses use the configured default; Trino's old database field maps
to catalog, while DuckDB/Databricks' old database field maps to schema. A missing
default that cannot be resolved produces an error rather than selecting a
similarly named object elsewhere.

Columns, view definitions, bounded previews, SQL references and supported Source
imports retain the catalog. Names with literal dots are individually quoted;
names that cannot safely be represented as Bruin asset names can still be
browsed but are rejected for Source creation. Catalog-qualified Source imports
revalidate one table in its parent namespace instead of using the connection's
default `GetDatabaseSummary`. Asset names and files retain the catalog prefix;
preview remains read-only and confirmation refuses overwrites. Native engine
authoring capabilities remain authoritative: catalog discovery does not add
`trino.source`, for example, or enable external-catalog materializations.

The pinned StarRocks client drops `Config.Catalog` from its native DSN. Renart
keeps the concrete `*starrocks.Client` required by Bruin's operator but supplies
a corrected DSN configuration: `catalog.database` in the MySQL initial-database
field, or the `catalog` session variable when no database is configured. The
driver applies that setting to **every physical connection**, not just one
pooled session. The catalog-only form requires StarRocks 3.2.4 or later. This
does not rewrite persisted settings; browser navigation uses fully qualified
queries and leaves these defaults untouched. Physical target identity includes
the same configured/default catalog, preventing cross-catalog collisions.

## Verification

Provider contract tests cover all six catalog-aware engine types, native result
column differences (Doris/Databricks), empty namespaces, default selection,
escaping and permission failures. Service tests cover StarRocks DSN defaults,
concrete-client compatibility, catalog-qualified source files/columns and remote
observation identity. Browser tests cover lazy discovery, search, durable links,
stale-token resolution and source/load authoring. Native DuckDB and a lightweight
Trino protocol fixture exercise the live UI/API/driver path; the fixture is not
proof of real external-catalog permissions or a live cloud-warehouse matrix.

## Follow-up boundaries

Missing providers require dialect-specific namespace mapping and tests, not a
generic `SHOW DATABASES` fallback. In particular Oracle owners, Dremio nested
folders and Spark/Sail catalogs are not interchangeable SQL databases. Until
implemented, the service returns `connection_type_not_supported`; it must not
pretend an empty successful listing means the warehouse has no tables.

Existing interfaces also do not guarantee safe/comprehensive discovery. The
scope and quoting issues above are tracked explicitly rather than classifying
every method-bearing client as fully supported. Remote permissions remain the
authority on visible catalogs/tables.

SQL references: [Trino catalogs](https://trino.io/docs/current/sql/show-catalogs.html),
[StarRocks default catalog](https://docs.starrocks.io/docs/data_source/catalog/default_catalog/),
[StarRocks catalog session variable](https://docs.starrocks.io/docs/sql-reference/System_variable/#catalog),
[StarRocks tables](https://docs.starrocks.io/docs/sql-reference/sql-statements/table_bucket_part_index/SHOW_TABLES/),
[MySQL tables](https://dev.mysql.com/doc/refman/8.4/en/show-tables.html).
