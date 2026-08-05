# Object-storage assets and browsing

Status: partial support shipped — Seed and lifecycle integration proposed

## Goal

Treat configured object storage as a first-class, credential-safe source and
destination across asset creation, editing, schema import, lineage, preview,
and execution while keeping Bruin CLI and Renart behavior identical.

## Current state

The Load path is substantially implemented already:

- configured `s3` and `gcs` connections are classified as storage sources and
  destinations (SFTP is a file transport);
- Load source and target pickers call Sling discovery with the selected
  environment and configured credentials;
- database targets use the asset relation, while storage/file targets use
  `parameters.destination_object`;
- runtime connection URIs are passed through environment variables rather than
  exposing credentials in process arguments;
- SQL path completion can browse configured S3 paths for DuckDB.

The missing core is Seed. Renart's Sling seed operator accepts local files and
HTTP(S) URLs only and deliberately rejects `s3://`/`gs://`. A Seed has only its
target connection; there is no committed source-connection identity. The
pinned Bruin seed operator has the same local/HTTP assumption, so adding a
Renart-only `source_connection` parameter would produce a project the Bruin CLI
cannot execute.

## Required Bruin contract

Add an optional credential-bearing source connection to Seed while preserving
the existing `path` default:

```yaml
type: duckdb.seed
parameters:
  source_connection: raw-object-store
  path: incoming/customers/2026-08-05.parquet
  file_type: parquet
```

The exact field name needs an upstream decision. When present, Bruin resolves
the path relative to the configured connection root/bucket and passes the
credentialed source to Sling without writing secrets to YAML, logs, argv, or
Renart state. When absent, current local/HTTP semantics remain unchanged.

The same connection-to-Sling URI builder should serve Seed and Load. Avoid a
second provider-specific credential translator.

## Shared object browser

Extract the existing Load stream picker behind a provider-neutral API:

```go
type ObjectEntry struct {
    URI, Name, Kind, ETag string
    Size                  int64
    ModifiedAt            time.Time
}

type ObjectBrowser interface {
    List(ctx context.Context, connection, environment, prefix string) ([]ObjectEntry, error)
}
```

- Back it with Sling discovery where Sling has the required connector.
- Keep connection/environment explicit on every request.
- Return bounded, paginated results and distinguish objects from prefixes.
- Never return credential material to the browser.
- Preserve manually entered paths for connectors that cannot list.

Use the same picker in:

- Seed create/edit source selection;
- Load source selection;
- Load storage/file destination prefix selection (with write semantics made
  clear rather than pretending a destination must already exist);
- explicit schema-import and preview dialogs.

## Lifecycle work beyond a picker

### Formats and paths

Define a shared supported-format table for CSV, Parquet, JSON/JSONL, and Avro,
including compression, glob/partition behavior, and whether a directory/prefix
is valid. Seed and Load should not advertise combinations their pinned Sling
runtime cannot execute.

### Schema and preview

Remote reads are explicit I/O. Browsing must not make workspace load, type
check, or LSP contact storage. A user-triggered “Import schema”/preview action
may sample or inspect the selected object, records source connection + object
identity in schema evidence, and persists accepted columns through the existing
schema-resolution flow.

Large, binary, or multi-object sources should show bounded metadata/rows rather
than loading the full object into Monaco. Cache explicit observations by
connection, environment, object identifier, ETag/version, and size.

### Lineage and freshness

Represent a selected object/prefix as a URI dependency so the canvas can show
an external source node. For versioned/immutable objects, record ETag/version as
the source fingerprint. For mutable prefixes, be conservative: an unknown or
unbounded listing must not be reported fresh merely because the path string is
unchanged.

Storage destinations need exact write-resource claims. Two assets targeting an
overlapping object/prefix must serialize or fail planning; a database-style
table claim is insufficient.

### Safety

- Validate schemes and normalize paths without allowing a configured root to be
  escaped.
- Apply existing environment policy and protected-environment confirmation to
  object writes.
- Preview/list endpoints are read-only and bounded; destination overwrite or
  prefix replacement requires an explicit reviewed action.
- Redact signed URLs, tokens, access keys, and credential-bearing query strings
  from logs, SSE events, plans, and saved state.

## Provider reach

Start with the connection types Bruin currently exposes and Renart already
maps for Load: S3-compatible storage and GCS. S3-compatible endpoints cover
MinIO/R2-style deployments when their Bruin connection is configured
accordingly. Azure Blob/ADLS and additional Sling file connectors should be
added only after Bruin has a first-class connection type and the same secret,
discovery, and test contracts.

## What else belongs in scope

Besides Seed source and Load source/target, complete support needs:

- create-dialog browsing, not only post-create editing;
- explicit remote schema import and bounded preview;
- URI lineage nodes and remote-source freshness;
- destination collision/resource-claim handling;
- format/compression/glob/partition UX;
- docs plus credential-safe S3/GCS live tests (MinIO and a GCS emulator where
  feasible).

API/Python assets writing arbitrary object files and SQL `COPY` outputs are
separate output-target features. They should build on the physical-output
contract in `physical-output-names.md`, not be smuggled into Seed/Load syntax.

## Rollout

1. Land the optional Seed source-connection contract and shared Sling URI
   builder in Bruin, with CLI tests for local/HTTP/S3/GCS compatibility.
2. Extract and harden the shared object-browser endpoint/picker; reuse it for
   existing Load source and destination fields.
3. Add Seed creation/editing, explicit schema import, and bounded preview.
4. Add URI lineage, fingerprints, and exact storage write claims.
5. Add emulator-backed live tests and user documentation, then fold shipped
   behavior into architecture docs and delete this plan.

## Decisions required before implementation

1. The upstream Seed source-connection field and path-relative semantics.
2. Whether a Load destination picker chooses an exact object, a prefix, or both
   per materialization mode.
3. Initial provider scope beyond S3/GCS.
4. Freshness semantics for mutable prefixes and wildcard sources.
