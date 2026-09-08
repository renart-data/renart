# SQL discovery coverage

Audit of the pinned Bruin v0.11.700 clients and Renart's service adapters,
2026-09-08. `TestSQLWarehouseDiscoveryCoverage` covers every connection type
advertised for SQL assets and forces an explicit review when that set changes.
This is a source/contract audit, not a claim that every cloud warehouse was
tested with live credentials.

The Data Browser and SQL connection pickers share `SQLService.Databases` and
`Tables`. `sqlDiscoveryAdapter` adds the missing interfaces around the existing
native client; it does not create a second connection or depend on Ingestr.
Trino uses `SHOW CATALOGS` and a catalog-qualified `information_schema.tables`
query, retaining `catalog.schema.table`. MySQL, StarRocks and Doris use
`SHOW DATABASES` and `SHOW TABLES FROM` with one quoted database identifier.
Vitess/PlanetScale share this MySQL adapter when used as connections. Names are
deduplicated/sorted; quoted identifiers, empty responses and driver errors have
regression coverage. Metadata queries do not issue `USE` or mutate data.

| Platforms | Database/table methods | Scope and limitations |
| --- | --- | --- |
| Trino | Renart adapter | Catalogs, schemas and fully qualified tables |
| MySQL, StarRocks, Doris | Renart adapter | Databases and tables in the current catalog; no cross-catalog StarRocks/Doris tree |
| BigQuery | Native | Datasets/tables in the configured project, not project discovery |
| DuckDB, MotherDuck | Native | Schemas represented as databases; not an attached-catalog tree; empty schemas are omitted |
| Postgres, Redshift | Native shared Postgres client | Databases listed, but information_schema can only enumerate the connected database's tables |
| Snowflake | Native | Databases and schemas; upstream metadata identifier interpolation needs hardening |
| ClickHouse, Athena | Native | Databases and tables |
| SQL Server, Synapse | Native shared SQL Server client | Upstream uses USE and omits table schemas; needs a non-session-mutating, schema-preserving adapter |
| Databricks | Native | SHOW DATABASES is schema discovery in the current catalog; catalog discovery and identifier quoting need follow-up |
| Vertica | Native | Database plus schema-qualified tables |
| Fabric, Dremio, Oracle, Sail, Spark | Missing | Select is available, but neither database nor table discovery is implemented |

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
[StarRocks tables](https://docs.starrocks.io/docs/sql-reference/sql-statements/table_bucket_part_index/SHOW_TABLES/),
[MySQL tables](https://dev.mysql.com/doc/refman/8.4/en/show-tables.html).
