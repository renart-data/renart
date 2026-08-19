# Notebooks — current architecture

Status: current state on `codex/notebook-platform`, August 2026. A notebook is
a Git-native ordered document whose data-producing blocks share one local
DuckDB integration warehouse. Remote systems are read-only sources; Renart
transfers typed snapshots into the local session before downstream work runs.

## 1. Definition, identity, and artifacts

A version-2 notebook is a folder:

```text
notebooks/revenue/
  notebook.yml             # notebook identity and ordered blocks
  calm_river.sql           # local or named-warehouse SQL cell
  quiet_fox.py             # local Python transform
  accounts.source.yml      # file, object-storage, or HTTP source
```

`notebook.yml` gives the notebook a durable UUID and stores an ordered list of
cell references, identity-bearing markdown blocks, and structured visualization
blocks. It also stores typed parameter definitions and defaults. SQL and Python
cells remain ordinary Bruin assets with `class: notebook`. A `.source.yml` file
is a small Renart-owned source definition that
also has a durable cell ID and produces one relation.

- Cell IDs survive filename/name changes. Newly created cells use concise
  adjective-noun names such as `quiet_river`, with collision checks and a
  suffix fallback.
- Markdown and visualization blocks have their own stable IDs. Cell IDs double
  as the block ID for executable/source cells.
- Merely loading a legacy manifest does not rewrite it. An explicit upgrade
  deterministically assigns presentation IDs. Legacy `@viz` comments remain
  readable until the user runs the migration operation.
- `SnapshotRevision` hashes the manifest and every authored block file. This
  notebook-wide revision is the compare-and-swap boundary for compound edits.
- `model.ArtifactIndex` projects notebooks, cells, sources, and visualizations
  beside pipeline assets. It records containment, derived capabilities, and
  relation/column dependencies without putting presentation components into a
  run DAG or weakening Bruin's pipeline asset model.

The filesystem remains authoritative. Runtime session files and transfer
artifacts live under `.renart` and are not authored state.

## 2. Semantic changes and transactions

`service.NotebookChangeSet` is an ordered batch of operations addressed by
durable IDs, never caller-owned paths. Supported domain operations cover
manifest upgrade; cell create/update/rename/delete/source configuration;
file/HTTP/object source create/update; markdown and visualization
create/update; parameter replacement; legacy visualization migration; and block
move/delete.

`PrepareChangeSet` copies the current notebook into private staging, applies
and normalizes the operations there, reloads after every step, runs structural
and visualization validation, and returns the exact normalized change set plus
the authored-file diff. Generated IDs and the resulting expected revision are
therefore reviewable before any write.

`ApplyChangeSet` requires that exact normalized change set. It re-prepares it,
checks the base revision, expected result revision, and current authored bytes,
then writes all affected files through a recoverable journal. Startup recovery
completes or rolls back an interrupted journal. A successful transaction emits
one logical workspace update; watcher/SSE reconciliation remains the final
frontend authority.

Ordinary single-cell typing keeps its faster per-file revision/save queue. The
compound transaction is shared by multi-block UI actions and MCP rather than
introducing a second state system.

## 3. Run graph and execution roles

Only data-producing cells enter the notebook DAG. Markdown and visualizations
are ordered presentation components, not executable assets.

The runner delegates block behavior through `NotebookBlockExecutor` and moves
external data through `NotebookTransferService`. Every transferable result is
a `TabularArtifact` with a physical schema, row/byte count, complete/sample
state, and credential-free provenance. The runner owns DAG order,
cancellation, serialized session access, preview creation, validation, and the
atomic swap into the durable logical cell object.

Current roles are:

- local SQL transform: DuckDB dialect, executes in the notebook session;
- warehouse SQL source: connection-derived dialect, read-only single `SELECT`
  at the named connection, then a typed local snapshot;
- Python transform: local uv process using the credential-free Renart broker,
  returning typed Parquet;
- local file source;
- S3-compatible and Google Cloud Storage object source using configured
  credentials;
- HTTP source with a validated request and response record selection.

A remote source cannot refer to local notebook cells. Users instead snapshot
the source and join it from a local SQL/Python transform, which makes data
movement and lineage visible.

## 4. Typed transfer and source policy

The transfer boundary has no JSON-row-map fallback for ordinary warehouse
snapshots:

1. compatible local DuckDB sources use the attach/copy path;
2. supported database/object/file sources use the shared hardened Sling
   launcher to create typed Parquet in a mode-`0700` staging directory;
3. a small typed direct-query adapter is allowed only inside strict memory
   limits when the connection cannot use Sling;
4. unsupported or over-budget work fails and keeps the previous good snapshot.

Credentials are resolved for one operation and passed in scoped process
environment. They are absent from argv, source provenance, browser DTOs, and
MCP results. The shared process limiter, context cancellation, time limit, and
byte/row monitors apply to source transfers. Temporary Parquet is deleted after
publication.

Snapshot policy is explicit:

- `full` must complete within configured budgets;
- `sample` has an authored positive row limit and propagates its sampled state
  through every downstream result;
- browser previews are separately bounded and never become downstream
  relations.

Publication creates a temporary session object, validates its schema and
provenance, then swaps it under the durable `src_<block-id>`/`cell_<cell-id>`
identity while holding the serialized notebook session.

## 5. Session, runtime metadata, and exports

Each notebook UUID owns `.renart/notebooks/<uuid>.duckdb` and one in-process
serialization lock. SQL cells materialize views by default; an explicit
materialization directive may pin a table. Python and source blocks publish
tables. Closing/deleting a notebook removes its session, and startup sweeping
removes orphaned session files.

Versioned session tables retain source snapshot provenance and successful cell
run summaries: fingerprints, timestamps, schemas, row counts, durations,
materialization kind, and source snapshot IDs. On restart, Renart reconstructs
result summaries, verifies the current definition fingerprints, and queries
only a bounded preview from live session objects. Definitions remain Git state;
runtime observations do not.

Completed, current-fingerprint relations can be exported through the server as
CSV or Parquet. Export is serialized with the session, uses a private
`.renart/notebook-exports` staging directory, and rejects stale/missing results.
Only backend-generated DuckDB `COPY` statements may bypass the authored-query
filesystem restriction, and only for that server-owned destination.

## 6. SQL/Python intelligence

The SQL editor uses two honest contexts:

- a local transform uses DuckDB plus sibling/source output schemas from the
  notebook DAG;
- a named-warehouse source uses the selected connection's dialect and remote
  catalog and does not pretend local sibling relations exist there.

Source output schema combines declared columns, static/connection analysis,
and the last successful snapshot observation through the existing schema
derivation model. Runtime columns can fill gaps for SQL that is difficult to
infer and arbitrary Python output. The corresponding artifact components also
carry these schemas for downstream completion and presentation checking.

Python stays in Python Monaco mode. SQL string literals passed to `query(...)`
receive projected SQL diagnostics and completion against the local notebook
session. Python processes get a token-scoped broker function, not a DuckDB path
or warehouse credential. pandas and PyArrow ship in the embedded SDK; adding
other packages creates/updates the notebook's Python project through uv.

## 7. Typed parameters

Version-2 manifests can declare text, number, boolean, select, multi-select,
date, and date-range parameters. Each declaration has a stable lowercase ID, a
typed Git-tracked default, an optional label, and optional static choices. The
shared `presentation` checker validates IDs, types, defaults, choices, and the
filter/dataset bindings future dashboard and report hosts will reuse.

Current values are local runtime state. They are exposed in the initial runtime
snapshot and `notebook.runtime` SSE events, but changing a value never rewrites
`notebook.yml`. A definition change resets local values to the new defaults.
Changing any current value conservatively marks every data-producing cell stale
and enters the effective typed value map into cell/source fingerprints, run
records, restart recovery, and export eligibility.

SQL uses `{{ parameter.id }}` for a safely quoted SQL literal. The same values
are available as typed `{{ parameters.id }}` values in Jinja conditions and
source templates; exact HTTP body placeholders preserve booleans, numbers, and
lists rather than stringifying them. Monaco completes both namespaces. Python
receives the typed map through `renart.context.vars`. Warehouse SQL, local SQL,
file/object URIs, HTTP requests, Python, and auto-recompute all receive one
validated runtime snapshot per run.

## 8. Structured visualizations

A visualization block references one data-producing block and owns a versioned
Renart definition in `notebook.yml`. The frontend's **Visual / Definition**
views edit the same value: form changes serialize deterministically, while an
invalid YAML draft stays local and editable until it parses and validates.
Changing views alone performs no write.

The current grammar supports table, KPI, bar, line, area, scatter, pie, and
donut presentations, including axes/field encodings, multiple series,
stacking, legends, labels, formatting, and a presentation row limit. Relational
work such as joins and aggregation stays in an explicit SQL/Python cell.

`internal/web/presentation` maps physical warehouse types into a small semantic
lattice and checks field existence and chart compatibility. The notebook
checker resolves declared, static, and last-runtime schemas. Unknown notebook
types are warnings where exploration can continue; known missing or
incompatible fields are errors. The same backend result feeds the visual
builder, YAML editor, notebook problems, artifact index, HTTP endpoint, and
type-check surfaces.

Legacy `@viz` parsing/rendering is compatibility-only. Migration creates a
stable visualization block and removes only the recognized comment; new UI and
MCP operations never author the directive.

## 9. Git-native presentations

Renart discovers `dashboard.yml`, `report.yml`, and named
`*.dashboard.yml`/`*.report.yml` files outside hidden/generated trees. The
version-1 definition contract is original to Renart and contains stable IDs,
named asset- or query-backed datasets, typed filters, visualization definitions,
dashboard layout entries, and ordered report sections. Loading uses strict YAML
field checking and a content revision; malformed files surface as workspace
errors, while structurally valid definitions remain visible with structured,
path-addressed problems.

The backend presentation checker resolves unique pipeline asset names or URIs,
inherits their declared/derived columns, accepts declared schemas for query
datasets, and strictly checks visualization fields/types plus filter options and
bindings. The workspace DTO exposes these authored artifacts under
`presentations`. `ArtifactIndex` registers dashboard/report containers and their
dataset, filter, visualization, and section components, including asset and
column-level lineage. These components do not enter a pipeline or notebook run
graph.

The `/dashboards` and `/reports` routes provide list/create flows plus a shared
artifact editor. Its Visual mode edits asset- or query-backed datasets, declared
query columns, typed filters and bindings, the shared visualization definition,
responsive dashboard spans, and ordered report sections. Definition mode edits
the complete strict YAML document in Monaco. Both modes write through the Go
service, share one content revision, retain drafts on conflicts, and let
workspace SSE reconcile outside changes. Typed snapshots are serialized
deterministically by the server; malformed YAML never replaces the last valid
file. Structural/schema findings remain visible after a save because invalid
Git state must stay repairable rather than becoming impossible to author.

`POST /api/presentations/{id}/run` executes only the requested visualizations
and option datasets. Asset datasets resolve their physical relation and
connection for the selected environment; query datasets execute as one
read-only result query on their declared connection. Typed filter values are
validated by the shared parameter checker and rendered as escaped,
dialect-aware predicates. One visualization cannot bind a filter to a sibling
dataset. Results are capped at 1,000 rows, report truncation explicitly, and
are keyed per visualization so two charts can reuse one dataset with different
bindings.

Nested `/dashboards/{id}/view` and `/reports/{id}/view` routes render these
bounded results with the same visualization renderer as notebooks. Filters use
validated JSON URL state, retain authored defaults outside the URL, and rerun
only affected visualizations. Each visualization has independent loading/error
state and a request sequence guard, so a late response cannot overwrite a
newer selection. Workspace changes reconcile through SSE; viewers do not poll.

Pipeline type-check reports include dashboards/reports that resolve an
asset-backed dataset to the checked pipeline. The CLI, HTTP bottom panel, and
deployment review all consume the same strict findings. Errors block that
producer pipeline's deployment and link back to the presentation editor;
invalid presentations that consume only another pipeline do not block it.
Repairing the artifact clears the blocker through the normal filesystem/SSE
reconciliation path. Query-only datasets are checked but do not create a
pipeline deployment edge because they have no declared producer asset.

Publication is not implemented. The current viewer is a local, live workspace
surface, not a hosted or access-controlled BI runtime.

## 10. Server-owned recompute and frontend state

The server owns definition staleness, last results, active runs, and the
auto-recompute closure. Editing a SQL cell marks it and descendants stale,
coalesces saves, validates each dependency wave with the same parse context as
Monaco, and publishes `notebook.runtime` deltas over workspace SSE. Python and
remote source execution are never triggered merely by opening a notebook.

Manual and automatic runs register before entering the shared session. Cancel
is a barrier: it cancels DuckDB, transfers, and Python work and waits for the
session to be released before returning. The frontend merges result deltas and
retains drafts on revision conflicts; it does not poll or treat Jotai as
persistent truth.

Preview tables stay bounded, block editors grow with short content before
using their internal scroll area, and output panes retain user scroll position
unless the user is already following the end.

## 11. Local MCP developer preview

`renart mcp --workspace <root>` serves the official MCP stdio transport. It
discovers the owning Renart server through `.renart/server.json` and keeps the
discovery token inside `clientapi`; without a live owner it creates the same
headless notebook service graph in-process. Stdout is reserved for JSON-RPC and
logs go to stderr.

`internal/notebookmcp` exposes 15 schema-versioned, high-level tools:

- notebook list/outline/block/graph/diagnostics;
- result schema and a sample capped at 50 rows/64 KiB;
- credential-redacted source definitions and snapshot provenance;
- prepare, revalidate, apply, and discard change sets;
- asynchronous run, cancel, and status.

Prepared changes are random opaque process-local handles, limited in count and
payload and expired after 30 minutes. Apply uses the exact hidden normalized
change set associated with the handle; returned semantic operations redact
source request headers/parameters/body and raw before/after file bytes. MCP v1
allows create/update/rename/reorder/configuration and explicit legacy migration
but not delete, promotion, cross-notebook writes, source updates that could
drop unknown keys, or Git operations.

Runs return an opaque run ID. Selecting Python without `allow_python: true` is
rejected, including a conservatively resolved Python ancestor. Status contains
bounded result summaries, not logs or unbounded rows. Tool annotations mark
reads, authored writes, open-world execution, destructiveness, and idempotence
for client approval UX; backend revision/policy checks remain authoritative.

There is no arbitrary path read/write, shell, Git, secret/configuration,
generic REST/HTTP, or free-form SQL execution tool. This constrains Renart's
integration, not a separately configured coding agent that may have its own
shell or filesystem authority.

## 12. Promotion and current limits

Single SQL/Python cell promotion uses the existing rename/reference engine,
changes `class` to `pipeline`, previews dialect consequences, and preserves the
durable asset ID. Source-block-to-pipeline promotion remains follow-up work;
users can currently export completed relations and create the durable pipeline
asset explicitly.

Still parked or incomplete:

- dashboard/report publication (Git-native CRUD, visual/definition authoring,
  local execution, viewers, typed URL filters, type-check, and producer-scoped
  deployment gates are implemented);
- remote scratch targets and direct remote reads from Python;
- persistent Python kernels and Python auto-recompute;
- cross-notebook data references;
- native provider/chat UI (local MCP is the shipped agent surface);
- selective acceptance within one dependent change set.

## Test surface

`internal/web/notebook` covers loader/migration, DAGs, typed source execution,
snapshot fidelity, session manifests, cancellation, exports, typed parameter
fingerprints, Python broker,
rename invariance, and legacy directives. `internal/web/service` covers
workspace/artifact projection, semantic transactions and recovery, runtime
hydration, source adapters/policy, presentation checks, promotion, and
auto-recompute. `internal/notebookmcp` drives the real SDK protocol in memory
and verifies the exact tool catalog, annotations, redaction, payload bounds,
revision conflict, exact apply, Python approval, asynchronous status, and
cancellation. Playwright live tests exercise warehouse/file/HTTP source chains,
visual editing/migration, typed parameter editing/execution, export bytes, and
race-sensitive notebook UI paths. Presentation live tests cover dashboard
creation, typed visual editing, deterministic save, definition-mode
round-tripping, URL-backed filters, dependency-aware viewer refresh, and a
presentation error blocking/clearing its consumed pipeline deployment.
