# Object-storage assets and browsing

Status: Load pickers and S3/SFTP Data Browser handoffs shipped — Seed, richer metadata and lifecycle integration proposed

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
- the same free-text picker is used during Load creation and later editing;
  local paths use the workspace file picker, while configured database and
  object-storage connections expose discovered streams;
- destination browsing distinguishes existing objects from the manually typed
  new destination path;
- discovery responses are capped at 500 entries and never serialize raw Sling
  output, which may contain echoed connection details;
- database targets use the asset relation, while storage/file targets use
  `parameters.destination_object`;
- runtime connection URIs are passed through environment variables rather than
  exposing credentials in process arguments;
- SQL path completion can browse configured S3 paths for DuckDB.

The Data Browser also has a metadata-only S3/SFTP adapter, including prefixes,
objects, durable connection-relative addresses, revision checks, and reviewed
canvas drops into Load sources/destinations. Read-only connections cannot be
destinations. Native Sling credentials are shared with execution; Ingestr is not
used. Real MinIO and in-memory SFTP import/export tests cover desktop and mobile.
See `architecture/backend.md` for the as-built adapter and its bounds.

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

The shared React picker has shipped for Load. Separately, Data Browser consumes
`StorageListing` through its existing capability envelope for S3/SFTP. That path
is prefix-aware and capped, not paginated, and does not expose ETags, sizes or
remote previews. The older Load picker still uses stream discovery. Further
consumers should extend/converge these existing adapters, not create a second
credential stack or another independent browser.

A richer listing contract for the remaining consumers could add:

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

Data Browser currently covers S3-compatible storage and Sling-backed SFTP;
GCS remains available through the existing Load pickers, not the new tree.
S3-compatible endpoints cover
MinIO/R2-style deployments when their Bruin connection is configured
accordingly. Azure Blob/ADLS and additional Sling file connectors should be
added only after Bruin has a first-class connection type and the same secret,
discovery, and test contracts.

## What else belongs in scope

Beyond the shipped Load source/target browser, complete support needs:

- explicit remote schema import and bounded preview;
- URI lineage nodes and remote-source freshness;
- destination collision/resource-claim handling;
- format/compression/glob/partition UX;
- GCS Data Browser reach and emulator-backed tests where feasible (MinIO and
  SFTP transfer tests and the corresponding Load docs already exist).

API/Python assets writing arbitrary object files and SQL `COPY` outputs are
separate output-target features and should not be smuggled into Seed/Load
syntax. Asset-name/path independence does not define those runtime targets.

## Rollout

1. Land the optional Seed source-connection contract and shared Sling URI
   builder in Bruin, with CLI tests for local/HTTP/S3/GCS compatibility.
2. **Partial:** bounded Load pickers and the S3/SFTP Data Browser tree/canvas
   handoffs are implemented. Pagination, richer object metadata and convergence
   of the two listing surfaces remain.
3. Add Seed creation/editing, explicit schema import, and bounded preview.
4. Add URI lineage, fingerprints, and exact storage write claims.
5. Add emulator-backed live tests and user documentation, then fold shipped
   behavior into architecture docs and delete this plan.

## Decisions required before implementation

1. The upstream Seed source-connection field and path-relative semantics.
2. Broader prefix-replacement/write semantics: current canvas drops accept an
   exact object or suggest an output filename inside a selected prefix.
3. Provider scope beyond the shipped S3/SFTP tree and existing GCS Load picker.
4. Freshness semantics for mutable prefixes and wildcard sources.
