# Notebook platform: release readiness, multi-source data, visualizations, and local agents

> **Status (2026-08-12): proposed.** The current implementation is documented
> in [`architecture/notebooks.md`](../architecture/notebooks.md). This plan
> replaces the narrower agent-only proposal and deliberately treats data access,
> execution, presentation, reproducibility, and agent tooling as one notebook
> product.

## 1. Decision summary

The recommended notebook architecture is:

1. **Keep one local DuckDB file as the notebook's integration warehouse.** A
   notebook SQL or Python transform always produces its durable session object
   there. Initial releases do not create temporary notebook schemas or tables in
   users' remote warehouses.
2. **Let source queries execute on any configured query connection.** A
   connection-bound SQL cell uses that connection's native dialect and pushes
   filtering/aggregation to the source. Renart then copies the typed result into
   the notebook DuckDB as an explicit snapshot. Downstream local cells can join
   snapshots from different warehouses, files, and APIs.
3. **Never silently turn partial data into a normal table.** Preview row limits
   apply only to displayed results. A data-producing snapshot is either complete,
   explicitly sampled, or failed. The existing implicit 50,000-row truncation is
   not release-safe.
4. **Add data-source blocks, not every pipeline asset type.** The useful notebook
   contract is “produce a typed local relation.” Warehouse SQL, local/object
   files, HTTP APIs, and the source half of load-like ingestion fit. Sensors,
   hooks, checks, and remote load targets do not; durable side effects should be
   promoted to a pipeline or invoked as an explicit export action.
5. **Replace `@viz` authoring with durable visualization blocks.** Each block
   references a data-producing block and stores a versioned Renart definition in
   `notebook.yml`. The normal UI is a visual builder; a **Visual / Definition**
   switch exposes the exact same definition in an editor. Neither view rewrites
   SQL comments.
6. **Model notebooks and presentations as artifacts, not fake pipeline
   assets.** Notebooks, dashboards, and reports are first-class workspace graph
   nodes. Cells/datasets/visualizations/filters are stable addressable child
   components. Only executable data-producing components enter their host's run
   graph; the broader artifact graph carries containment, lineage, column usage,
   diagnostics, and deployment/version capabilities.
7. **Type-check presentation artifacts before they reach production.** One
   backend presentation checker resolves notebook outputs and pipeline assets,
   verifies referenced fields and their semantic types, validates filter
   bindings/defaults/options, and feeds both editor diagnostics and
   `renart type-check`. A known-bad dashboard or report is a deployment blocker.
8. **Start agentic notebooks as a local MCP developer preview.** `renart mcp`
   runs over stdio for locally installed coding agents. It exposes notebook-only
   semantic tools backed by the Go server; it does not expose paths, a shell,
   Git, credentials, or generic REST forwarding. Codex, Claude Code, and OpenCode
   can all launch local stdio MCP servers today.
9. **Use one notebook-wide revision and transaction model.** The visual builder,
   ordinary UI edits, and MCP change sets must share the same semantic mutation,
   validation, conflict, and SSE reconciliation path. The filesystem remains the
   source of truth and accepted changes remain ordinary Git diffs.

This is a hybrid model, but not a dual-target model:

```text
Postgres / Snowflake / BigQuery / Databricks / ...
            source-native read-only query
                         |
                         v
              typed snapshot transfer
                         |
files / object storage ->+-> notebook DuckDB <- HTTP API
                                |       |
                           local SQL   Python
                                |       |
                                +---+---+
                                    |
                           tables and visualizations
```

The remote warehouse does the work it is good at; DuckDB provides one cheap,
disposable place where unlike sources can be combined. A future opt-in remote
scratch target is possible, but it should be justified by measured workloads
rather than built into the first release.

## 2. What exists and what blocks a release

### 2.1 Strong foundations to preserve

The current notebook implementation already has more than a prototype UI:

- notebooks and cells are plain Git-tracked files with durable IDs;
- logical cell names are independent from physical `cell_<id>` objects;
- cell dependencies form a DAG and SQL cells get notebook-aware LSP context;
- one serialized DuckDB session per notebook supports cancellation;
- Python runs through the credential-free Renart broker and stages typed
  Parquet output back into the live session;
- cell saves use content revisions, and server-owned auto-recompute publishes
  runtime state over the existing workspace SSE channel;
- imports, promotion, rename invariance, result previews, and a Recharts-based
  renderer already have unit/service/live coverage.

The plan should extend these seams rather than build a parallel notebook state
system.

### 2.2 Data access is currently unsafe as a general notebook contract

`internal/web/notebook/run.go` and
`internal/web/service/notebook_service.go` already import referenced pipeline
assets. The DuckDB `ATTACH` fast path is sound. The generic path is not:

- only a reference that resolves to a pipeline asset can be imported; a raw
  warehouse relation is left for DuckDB to reject;
- the connection query is buffered as JSON maps, which loses the source's
  precise type contract;
- DuckDB types are guessed from observed values and collapse to
  `BOOLEAN | BIGINT | DOUBLE | VARCHAR`;
- the fetch asks for 50,001 rows, stores at most 50,000, records
  `complete: false`, and still exposes that table to downstream queries;
- a query result containing no non-null values can therefore become `VARCHAR`
  even when the remote schema is numeric, temporal, decimal, binary, nested, or
  otherwise known;
- import provenance is only `ref`, timestamp, row count, and completeness, so
  it cannot explain which environment, connection, query, or source revision
  produced the snapshot.

The current behavior is useful proof that local integration works, but it must
not be marketed as complete cross-warehouse notebook support.

### 2.3 Connection semantics are implicit

- SQL cell execution is always DuckDB, even if its asset type implies another
  dialect.
- Python's notebook broker accepts only the synthetic `renart-notebook`
  connection.
- `notebook.yml` has a `target` field, but no warehouse-backed target is
  implemented.
- The UI accurately says that the notebook runs locally, but it provides no
  explicit “query here, snapshot locally” source model.
- Remote schemas and the local notebook DAG need different LSP contexts. A
  source-native cell should see the selected connection's catalog; a local
  transform should see notebook outputs. Pretending both namespaces are one
  database produces misleading completion and diagnostics.

### 2.4 The block and runtime models need hardening

- The loader recognizes only `.sql` and `.py` executable cells.
- Markdown blocks have no durable ID.
- A cell has a revision, but the notebook has no revision covering manifest
  order plus all block files, so a coherent multi-block update cannot use one
  compare-and-swap boundary.
- Last results and staleness live in server memory. The DuckDB objects can
  survive a restart, but their useful run metadata cannot be reconstructed.
- SQL auto-recompute is deliberately stronger than Python recompute; the UI
  needs to make that execution policy obvious rather than implying a shared
  kernel model.
- `@viz` is parsed from a comment and the UI edits that comment. The syntax is
  compact but makes presentation configuration compete with SQL formatting,
  code editing, diagnostics, and source control.

## 3. Product model

### 3.1 One notebook dataflow, several block roles

Every block gets a durable ID. Every data-producing block also gets a concise
logical name and produces exactly one typed relation in the notebook session.

| Block                | Runs where                                   | Produces a local relation | Initial support               |
| -------------------- | -------------------------------------------- | ------------------------: | ----------------------------- |
| Local SQL transform  | Notebook DuckDB                              |                       yes | existing, retained            |
| Warehouse SQL source | Selected named connection, then snapshot     |                       yes | add first                     |
| Python transform     | Local uv process through the notebook broker |                       yes | existing, retained            |
| File/object source   | Source adapter, then snapshot                |                       yes | add after warehouse SQL       |
| HTTP source          | Native API asset fetcher, then snapshot      |                       yes | add after warehouse SQL       |
| Markdown             | nowhere                                      |                        no | existing, add durable ID      |
| Visualization        | browser renderer over a referenced relation  |                        no | replace `@viz` authoring      |
| Parameter/input      | browser + render context                     |                        no | later release-readiness phase |

The UI should describe these as user jobs—**SQL**, **Python**, **Add data**,
**Text**, and **Visualization**—rather than presenting the full pipeline asset
type registry inside a notebook.

### 3.2 Asset types that do not belong in a notebook

- **Load targets and remote materializations:** they create durable side effects
  and need lifecycle, environment, and deployment semantics. Offer “Promote to
  pipeline” and explicit CSV/Parquet download first.
- **Sensors:** they wait for external state rather than produce exploratory
  data.
- **Hooks and checks:** attach them to a promoted pipeline asset; a notebook can
  show diagnostics without making them executable cells.
- **Source-only metadata assets:** useful as selectable inputs, but the notebook
  consumes their relation through a warehouse source block instead of copying
  the metadata asset into the notebook.
- **Autonomous prompt cells:** nondeterministic output, refresh, cost, and
  lineage semantics remain unresolved. Local MCP is the agent surface for this
  plan.

### 3.3 Environment and connection behavior

- A notebook has one selected runtime environment at a time. This selection is
  local runtime state, not committed into the notebook definition.
- A local SQL cell has no project connection and always uses DuckDB.
- A warehouse SQL source cell declares a project connection. The backend
  derives and validates its asset type/dialect from that connection; the UI
  must not maintain a separate editable dialect setting.
- A source query is a read-only single `SELECT`. The selected connection's
  database permissions remain the real data-access boundary.
- A warehouse source cannot reference local notebook cells in the first
  release. Uploading local intermediates into every possible warehouse is a
  separate, side-effecting feature. The user instead creates a local SQL cell
  to join source snapshots.
- Python continues to query the local notebook session. Remote Python reads
  should use an explicit source cell first, making data movement, lineage, and
  sampling visible. Direct `query(connection=...)` can be considered later
  under the same connection policy as pipeline Python assets.
- Missing, renamed, or environment-incompatible connections are definition
  diagnostics before execution, not late generic query failures.

### 3.4 Source refresh and sampling

Each source output is a snapshot with an explicit mode:

- `full`: produce a complete result within configured time/disk/row budgets;
  exceeding a budget fails and preserves the previous good snapshot;
- `sample`: an intentional row-limited result whose sampled state is visible on
  the source block and every downstream result derived from it;
- `preview`: a bounded result returned to the UI and never registered as a
  downstream relation.

Manual source refresh is the understandable default. “Run all” refreshes stale
source definitions, while “Refresh sources and run all” re-reads unchanged
sources too. A snapshot cache key includes notebook ID, block ID, environment,
connection, canonical source definition/query, and relevant source asset
fingerprint when one exists.

Do not infer that a remote table is unchanged merely because the query text is
unchanged. The UI must always show the snapshot timestamp, connection,
environment, row count, byte size, and `complete | sampled` state.

### 3.5 Workspace artifacts and component nodes

It is useful to treat notebooks, dashboards, and reports as asset-like objects,
but not to encode them as `pipeline.Asset`. In Renart today, “asset” carries
Bruin execution assumptions: an asset has pipeline membership, dependency and
materialization behavior, a connection/target, and materialization freshness.
A chart, filter, narrative section, or notebook container has none of those
semantics. Making those fields optional everywhere would weaken the execution
model and produce misleading states such as “chart never materialized.”

Add a broader, read-only projection beside the existing pipeline and notebook
state instead:

```go
// WorkspaceArtifact = pipeline asset | notebook | dashboard | report.
type ArtifactDescriptor struct {
    ID         string
    Kind       ArtifactKind // pipeline_asset | notebook | dashboard | report
    Path       string
    Title      string
    Components []ArtifactComponent
}

type ArtifactComponent struct {
    ID               string
    Kind             ComponentKind // cell | source | dataset | visualization | filter | section
    Name             string
    ProducesRelation bool
}

type ArtifactRef struct {
    Kind        ArtifactKind
    ArtifactID  string
    ComponentID string // empty when the reference targets the container
}

type ArtifactDependency struct {
    Producer ArtifactRef
    Consumer ArtifactRef
    Columns  []ColumnUsage
}
```

An `ArtifactIndex` derives descriptors, capabilities, containment, and
dependencies from the authoritative files. Do not persist editable capability
booleans. Derive capabilities such as `executable`, `produces_relation`,
`has_schema`, `presentation`, `versioned`, and `deployable` from the artifact or
component kind. `WorkspaceArtifact` is a union/projection, not a common mutable
base class. Pipeline assets remain Bruin-native and can be projected into the
same lineage index without changing their stored or Go domain type.

Use three related graphs rather than one graph with vague node semantics:

1. **Host run graphs** contain executable, data-producing nodes only: Bruin
   assets in a pipeline DAG, cells/sources in a notebook DAG, and later named
   dataset refreshes in a dashboard plan.
2. **The workspace artifact graph** connects all producers and consumers,
   including asset column -> dataset -> visualization and filter -> dataset
   parameter edges. It powers impact analysis, navigation, diagnostics, and
   “used by” views; it is not itself an execution plan.
3. **Containment** records notebook -> block and dashboard/report -> component
   structure. Child components have stable identities and can be linked,
   selected, edited, and type-checked without becoming permanent top-level
   canvas nodes. A parent can expand its components when that detail helps.

The top-level artifact is the Git/versioning and ownership unit. Dashboards and
reports are also the eventual deployment unit. Notebook cells and source blocks
that already round-trip as Bruin notebook-class assets keep doing so, but the
workspace model presents them as components of their notebook rather than
pretending that the notebook container is a materializable table.

Each host retains honest lifecycle language:

- pipeline assets have Renart-observed materialization freshness;
- notebook data blocks have definition staleness and snapshot/run age;
- dashboards and reports have upstream-data recency plus validation,
  render/cache, and deployment state;
- visualizations, filters, and narrative sections are never labeled fresh,
  stale, built, or materialized on their own.

References use stable `(kind, artifact ID, component ID)` identity internally,
not a path or overloaded asset name. The existing asset/URI resolver can later
project these typed references for cross-artifact links without forcing Bruin
CLI to understand Renart-only presentation files. This is an incremental index
and resolver addition, not a prerequisite to refactor every current pipeline
service around a new universal base class.

## 4. Versioned notebook definition

### 4.1 Manifest v2

Keep SQL and Python cells as ordinary cell files. Add durable identity to all
manifest-owned blocks and add visualization blocks directly to the ordered
manifest:

```yaml
version: 2
id: nb_01...
title: Revenue exploration
blocks:
  - markdown:
      id: block_01...
      content: |
        ## Question
        How does revenue change by month?
  - cell: cell_01...
  - visualization:
      id: viz_01...
      source: cell_01...
      definition:
        version: 1
        type: line
        title: Monthly revenue
        encoding:
          x:
            field: month
          y:
            - field: revenue
```

Cell IDs can continue to serve as their block identity. Markdown and
visualization entries need their own IDs because order and content must be
addressable by semantic UI and MCP operations.

Non-SQL source blocks use a small Renart-owned `<name>.source.yml` file with a
stable ID. The first schema supports:

```yaml
id: source_01...
kind: file
connection: object-storage-default # omitted for a local file
uri: s3://analytics/events/2026-08.parquet
format: parquet
snapshot:
  mode: full
```

and:

```yaml
id: source_02...
kind: http
request:
  url: https://example.test/events
  method: POST
  body:
    after: "{{ start_datetime }}"
records_path: data.items
snapshot:
  mode: sample
  row_limit: 10000
```

The exact HTTP and object-storage subdocuments should reuse Renart's existing
validated request/storage DTOs instead of defining a second connector schema.
Promotion translates a source block into the appropriate ordinary pipeline
asset and presents the resulting Git diff.

### 4.2 SQL source cells

A connection-bound SQL cell remains a `.sql` asset so Monaco, formatting,
Jinja, declared columns, and promotion continue to work:

```sql
/* @bruin
id: cell_01...
type: pg.sql
class: notebook
connection: postgres-other
@bruin */

select customer_id, created_at
from public.customers
where created_at >= {{ start_datetime }}
```

For a notebook-class asset, `connection` means “execute this read-only source
query there and import the result.” A connectionless SQL cell remains a local
DuckDB transform. The editor labels the field **Source connection** so the
context-specific meaning is not hidden.

The **Add data** picker can create this cell from a browsed relation by
generating `select * from <qualified relation>`. It is then ordinary editable
SQL; users can project and aggregate at the source before transferring data.

### 4.3 Compatibility and migration

- Readers support the current manifest and `@viz` comments throughout the
  migration window.
- Merely opening a notebook does not rewrite it. The first v2-only edit can
  offer one deterministic “Upgrade notebook” diff, or the user can run an
  explicit migration action.
- Migration assigns IDs to markdown blocks, creates visualization blocks after
  their source cells, removes only the parsed `@viz` comment, and leaves SQL
  otherwise byte-for-byte unchanged.
- Existing `target` is not revived as a warehouse destination. Keep reading and
  round-tripping it until migration; report that it has no effect and omit it
  from newly authored v2 notebooks.
- Keep legacy `@viz` rendering read-only for at least one release after the
  builder ships. New UI and MCP tools never write a visualization comment.

## 5. Execution and transfer architecture

### 5.1 Domain interfaces

Replace the runner's SQL/Python branch and narrow `SourceFetcher` with explicit
domain contracts:

```go
type NotebookBlockExecutor interface {
    Analyze(ctx context.Context, input AnalyzeBlockInput) (BlockAnalysis, error)
    Execute(ctx context.Context, input ExecuteBlockInput) (BlockOutput, error)
}

type NotebookTransferService interface {
    Snapshot(ctx context.Context, request SnapshotRequest) (TabularArtifact, error)
}

type TabularArtifact struct {
    Path       string
    Schema     []Column
    RowCount   int64
    ByteCount  int64
    Complete   bool
    Sampled    bool
    Provenance SnapshotProvenance
}
```

`BlockOutput` is either a tabular artifact/local relation or a non-tabular
result. The runner remains responsible for DAG ordering, status, cancellation,
atomic publication into the session, and preview creation. Executors do not
write notebook files or decide which environment/connection is allowed.

Initial executor implementations:

- `LocalSQLExecutor`
- `WarehouseSQLSourceExecutor`
- `PythonExecutor`
- `FileSourceExecutor`
- `HTTPSourceExecutor`

This interface is internal Go code, not an MCP or file-format dependency.

### 5.2 Typed transfer adapters

Use an adapter chain, selected by source capability:

1. **Local DuckDB:** keep the existing `ATTACH; CTAS; DETACH` path.
2. **Database query to Parquet:** reuse Renart's hardened Sling launcher,
   connection-to-URI bridge, credential-in-environment handling, cancellation,
   and shared process limiter. Sling accepts a SQL file as a source stream and
   a local Parquet path as a target, so a source-native query can remain off the
   command line and retain a columnar schema.
3. **Small typed direct result:** allow a bounded `SelectWithSchema` to Parquet
   fallback only when Sling does not support a query connection and the result
   stays below a strict in-memory budget. Do not route it through JSON or infer
   types from values.
4. **Unsupported:** fail with the connection type and a concrete suggestion to
   aggregate/sample at source or use a supported file export. Never fall back to
   an unbounded row-map buffer.

Run transfers in a mode-`0700` directory under `.renart`, pass credentials only
through the existing scoped connection environment, and delete temporary
artifacts after DuckDB has imported them. A size monitor and context deadline
cancel transfers that exceed policy. Parquet metadata supplies row count and
schema before publication.

Import into a temporary session object, validate schema/count/provenance, then
swap it into `src_<block_id>` in one serialized session operation. Cancellation,
process failure, malformed output, or a budget violation leaves the previous
good snapshot intact.

### 5.3 Runtime metadata

Version the import table and add a cell-run manifest inside the session DB:

```text
__renart_imports_v2
  block_id, source_kind, environment, connection, definition_fingerprint,
  source_fingerprint, imported_at, row_count, byte_count, complete, sampled,
  schema_json

__renart_cell_runs
  cell_id, cell_fingerprint, finished_at, status, materialized_as,
  row_count, schema_json, duration_ms, source_snapshot_ids
```

On server restart, reconstruct successful result summaries from these tables,
verify that referenced objects still exist, and compare saved fingerprints with
current files. Raw preview rows need not be duplicated in SQLite; query the
session object for a bounded preview when the notebook is opened.

Runtime metadata stays under `.renart` and is not Git-tracked. Definition files
remain the only authored state.

### 5.4 LSP and type checking

Use two explicit contexts:

- **local cell:** DuckDB dialect; sibling/source output schemas from the
  notebook DAG; local filesystem policy; no remote catalog pretending to be
  locally queryable;
- **warehouse source cell:** connection-derived dialect and remote catalog;
  remote-table diagnostics/intellisense; no sibling relations unless a later
  remote-upload feature makes them real there.

The output schema of a warehouse source cell comes from, in order:

1. declared columns;
2. source query analysis / connection describe result;
3. last complete snapshot schema.

These are observations in the existing schema-derivation model, not competing
copies of the cell definition. Conflicts are shown with provenance. A sampled
snapshot is a valid runtime observation but cannot prove completeness or
nullability.

## 6. Visualization definitions and builder

### 6.1 One definition, two editors

The visualization block owns a small, versioned Renart schema. The visual
builder and definition editor are two views over the same parsed value:

```text
Visualization block
  source cell combobox
  [ Visual | Definition ]

  Visual                       Definition
  chart type                   YAML editor with schema completion
  x / y / series fields        exact persisted fragment
  labels / number formats      parse + validation diagnostics
  sort / top-N / appearance    deterministic formatting on apply

  shared live preview below or beside the active editor
```

On a wide layout, builder/editor and preview may sit side by side; on a narrow
layout they stack without horizontal scrolling. Use the existing shadcn
`Chart`/Recharts renderer, `Tabs` or `ToggleGroup` for the two modes,
`FieldGroup`/`Field` for controls, searchable `Combobox` field pickers, semantic
theme tokens, and `ScrollArea` only when the builder genuinely exceeds its
available height.

Visual edits update an in-memory typed definition and serialize it
deterministically. Definition edits remain a draft until they parse and pass
schema validation; applying them updates the builder. Switching modes never
changes the definition by itself.

### 6.2 Initial visualization grammar

Support the chart families already close to Renart's renderer, plus scatter:

- table;
- KPI;
- bar;
- line;
- area;
- scatter;
- pie/donut.

The definition covers data reference, field encodings, labels, formatting,
sorting, stacking, axes, legend, color series, and a presentation row limit.
It does not duplicate each field's database type: the schema resolver supplies
that. A later explicit `interpret_as` escape hatch may narrow an ambiguous
physical type, but the checker must validate it rather than trusting it.
It does **not** hide relational transformations in the chart. Joins,
aggregation, calculated columns, and business filters stay in a visible SQL or
Python block. The builder can offer “Create aggregate cell” later instead of
silently changing data semantics.

Field pickers come from the referenced block's known schema. A removed or
renamed column produces a precise visualization diagnostic and keeps the saved
definition editable. More than one visualization may reference the same
result, eliminating the current one-comment-per-cell restriction.

### 6.3 Static presentation type checking

Visualization correctness is a backend contract, not just a field-filtering
convenience in the builder. Add a shared package such as
`internal/web/presentation` with two narrow services:

```go
type PresentationSchemaResolver interface {
    ResolveSource(ctx context.Context, ref DataSourceRef) (ResolvedSchema, error)
}

type PresentationTypeChecker interface {
    CheckVisualization(ctx context.Context, definition VisualizationDefinition) []Finding
    CheckArtifact(ctx context.Context, artifact PresentationArtifact) []Finding
}
```

`ResolvedSchema` keeps the physical warehouse type and maps it into a small
semantic lattice:

```text
numeric | temporal | categorical | boolean | binary | semi_structured |
geospatial | unknown
```

The checker validates at least:

- the source asset, notebook block, or named dataset resolves;
- every encoded, sorted, formatted, tooltip, and filter field exists;
- line/area/bar measures are numeric, scatter axes are numeric or temporal,
  pie values are numeric, and category/series fields are representable;
- numeric/date formatting is applied only to compatible values;
- multiple series and comparison fields have compatible types;
- any calculation/aggregation added in a later schema has a valid input and a
  known output type;
- a visualization does not depend on a sampled notebook source when the
  artifact declares that complete data is required.

Rules operate on canonical types, not engine-specific spellings such as
`INT8`, `BIGINT`, or `NUMBER(38, 0)`. Keep the original physical type in the
finding so users can understand a mismatch.

Resolution differs by host without changing the visualization definition:

- a notebook visualization resolves `source: <block-id>` through static SQL
  inference, declared columns, and the last complete runtime schema;
- a dashboard/report dataset resolves an asset through the canonical workspace
  dependency resolver and the asset schema-derivation service;
- a dashboard/report SQL dataset is analyzed with its connection dialect and
  must expose an inferred or declared output schema.

Notebook exploration may retain a visualization with an `unknown` source type
and show a warning until the source runs or columns are declared. A
dashboard/report validation or deployment cannot rely on runtime luck: any
referenced field whose existence or required type remains unknown is a blocker
with an action to declare/derive the missing schema.

Wire the checker into:

- `renart type-check` and the HTTP type-check endpoint;
- Monaco/YAML definition diagnostics and quick navigation to the source asset;
- dashboard/report deployment readiness;
- reverse-impact findings when an asset rename, column removal, or type change
  breaks a presentation artifact.

The frontend consumes structured findings with definition paths/spans; it does
not reimplement type rules in TypeScript. The visual builder may filter choices
to compatible columns, but hand-authored definitions receive the exact same
backend checks.

### 6.4 Shared dashboard/report contract and filters

Define the renderer around three portable inputs from the start:

```text
VisualizationDefinition + DataFrame/row source + ParameterValues
```

That contract supports separate Git-native dashboard and report artifacts
without making a notebook itself a dashboard:

- notebooks remain ordered exploratory computation plus narrative;
- dashboards add a responsive layout, filters, and refresh policy;
- reports add sections, page/print layout, and frozen run provenance.

The initial cross-surface data reference is deliberately asset-centric. A
dashboard definition names datasets, and a dataset normally references a
pipeline asset using the same stable local/cross-pipeline resolution rules as
dependencies. Charts reference a dataset and fields, not an untyped result
blob:

```yaml
version: 1
id: sales_overview
datasets:
  monthly_sales:
    asset: analytics.monthly_sales
filters:
  - id: region
    type: select
    default: eu
    options:
      values: [eu, us]
visualizations:
  - id: revenue_by_month
    dataset: monthly_sales
    type: line
    encoding:
      x:
        field: month
      y:
        - field: revenue
    filter_bindings:
      - filter: region
        column: region
        operator: equals
```

This is illustrative, not a frozen schema. The important contract is that data
references and filter bindings are explicit enough to type-check without
running production queries.

Support these shared typed filter definitions:

- select and multi-select;
- date and date range;
- number;
- text;
- boolean.

A filter has a stable ID, label, type, typed default, optional allowed values,
and URL-serializable runtime value. Option lists may be static or backed by a
named dataset plus `value_field` and optional `label_field`; avoid embedding an
untracked ad-hoc query directly inside the control. The checker verifies:

- defaults and URL values match the filter type;
- static defaults belong to constrained options;
- option dataset/value/label fields exist and have compatible types;
- every binding references a declared filter and dataset column;
- the binding operator accepts both types (`between` for a date range, `in`
  for multi-select, numeric comparisons for numbers, and so on);
- filter-driven query parameters use bound parameters where a connection
  supports them, otherwise server-owned dialect-aware typed literal rendering;
  user values never become raw SQL fragments.

Dashboard filter changes re-run only affected datasets/visualizations and put
validated state in the URL so a view can be shared. Notebook parameters reuse
the same primitive types, but a notebook visualization need not adopt the full
dashboard filter runtime in its first release. Reports use typed parameters and
frozen/default values unless an interactive report mode is designed later.

The shared definition, renderer, schema resolver, type checker,
parameter/filter types, and findings belong in the notebook foundation.
Git-native dashboard and report artifacts are a later phase of this plan, after
notebook visualizations prove the contract; they must reuse it rather than fork
a second chart language.

### 6.5 Licensing boundary

The definition, builder, renderer adapters, examples, tests, and documentation
must be designed from Renart's requirements and written originally. Do not copy
source, schemas, assets, tests, or distinctive text from incompatibly licensed
dashboard implementations. Any new runtime dependency must be compatible with
Renart's Apache-2.0 distribution; the existing shadcn Chart/Recharts stack is
the default, so a new visualization engine is not required for v1.

## 7. Local MCP agent integration

### 7.1 Product boundary

The initial agent feature is not a built-in model provider or chat system. It
is a local developer integration:

```text
Codex / Claude Code / OpenCode
             |
        MCP over stdio
             |
        renart mcp
             |
  notebook domain service + running Renart server
             |
     notebook files / session / LSP
```

All three requested clients support launching local stdio MCP servers. Stdio is
preferable to a second listening HTTP server because the client owns process
lifecycle, stdout is the protocol, no port is exposed, and Renart can pin the
process to one workspace at startup.

`renart mcp --workspace <path>` should:

1. resolve and validate the Git-backed workspace;
2. discover the owning Renart process through `.renart/server.json` and the
   existing bounded health check;
3. use `internal/clientapi` with the discovery token without ever returning the
   token to the MCP client;
4. start an embedded single-workspace service only when no Renart server owns
   the workspace, matching existing CLI delegation behavior;
5. reserve stdout for JSON-RPC and send logs only to stderr.

Use the official Go MCP SDK unless an implementation spike finds a concrete
protocol/compliance blocker. Start with stdio only; remote Streamable HTTP,
OAuth, and hosted agents are out of scope.

### 7.2 Notebook domain contract before protocol

MCP adapts ordinary Go services; it is not the internal architecture. Add:

- `NotebookSnapshot`: manifest, block metadata, fingerprints, and one
  deterministic notebook-wide revision;
- `NotebookOperation`: typed create/update/rename/reorder operations addressed
  by durable block ID, never a caller-provided path;
- `NotebookChangeSet`: base revision plus an ordered semantic operation batch;
- `NotebookChangeSetService`: normalize, overlay-load, validate, diff, and
  transactionally apply a change set;
- `NotebookFileTransaction`: write all affected files through a recoverable
  journal, publish one logical change, then let watcher/SSE reconcile.

This foundation also fixes visual-builder and ordinary multi-block edits. MCP
tools never call the current file CRUD endpoints independently for a compound
change.

### 7.3 Initial resources and tools

Version tool schemas from the first preview. Suggested v1 surface:

Read-only resources/tools:

- `list_notebooks`
- `get_notebook_outline`
- `get_notebook_block`
- `get_notebook_graph`
- `get_notebook_diagnostics`
- `get_notebook_result_schema`
- `get_notebook_result_sample` with a small explicit row/byte cap
- `list_notebook_sources` and source schema/provenance

Change tools:

- `prepare_notebook_change_set`: stage SQL, Python, markdown, source, and
  visualization create/update/reorder operations against a base revision;
- `validate_notebook_change_set`: overlay-load it and return structural, SQL,
  Python, source, and visualization diagnostics plus a unified diff;
- `apply_notebook_change_set`: recheck the notebook revision and atomically
  apply the exact prepared change set;
- `discard_notebook_change_set`.

Execution tools:

- `run_notebook_cells` with explicit environment and source-refresh choice;
- `cancel_notebook_run`;
- `get_notebook_run_status`.

Do not expose arbitrary SQL execution, raw filesystem reads/writes, shell, Git,
promotion, connection configuration, secrets, generic HTTP requests, or a tool
that forwards arbitrary Renart API paths. A remote query must exist as a
reviewable source cell or already-defined source block.

The apply and run tools should carry accurate MCP read-only/destructive
annotations so clients can present approvals, while treating annotations as UX
hints rather than authorization.

### 7.4 Change review and authority

The external coding agent may prepare and validate changes without modifying
files. `apply_notebook_change_set` is a separate call so the host can display an
approval. Renart then enforces:

- exact workspace and notebook scope;
- notebook-wide revision equality;
- valid durable IDs and generated safe filenames;
- DAG and definition invariants;
- a bounded operation count and payload size;
- one recoverable filesystem transaction;
- no commit, stage, push, or implicit canonical run.

The first mutation set should support create/update/reorder for SQL, Python,
markdown, source, and visualization blocks. Reuse the existing rename engine
after the batch transaction is proven. Delete, promotion, and cross-notebook
mutation can follow later and should be marked destructive individually.

### 7.5 Security proportional to a local developer feature

Keep the boundary useful without building a hosted control plane:

- stdio process scoped to one resolved workspace;
- server discovery token kept inside the bridge;
- high-level IDs instead of paths;
- existing read-only SQL validation and environment/connection policy;
- no credentials or raw connection configuration in tool results;
- bounded source/result samples, tool payloads, run duration, and transfer
  bytes;
- explicit Python execution tool, because generated Python is arbitrary local
  code;
- redacted structural logs rather than retained result data;
- cancellation propagated to DuckDB, transfers, and Python.

This is a **cooperative least-privilege integration**, not containment. A
general coding agent may independently have shell and filesystem tools and can
bypass the MCP workflow if the user grants them. Renart can provide client
instructions that say “modify notebooks only through Renart tools,” but it must
not claim to sandbox the whole external process. Enforceable containment would
require the user to disable those tools or run the agent in a separate sandbox.

### 7.6 Client setup and evaluation

Document copyable project-local configuration for Codex, Claude Code, and
OpenCode using `renart mcp --workspace .`. Do not automatically write or commit
agent configuration in the first version. Each guide must explain:

- which local binary is launched;
- that the agent/model provider is selected and governed by that client;
- which Renart tools read data, execute code, or write notebook files;
- that users should keep mutating-tool approval enabled;
- how to remove the integration.

Test the same fixed task corpus with all three clients, but keep ordinary CI
deterministic with an MCP protocol client and fake agent. Compatibility smoke
tests are optional and credentialed.

### 7.7 Parked agent directions

- A native Renart Ask/Edit panel may later consume the same snapshot,
  diagnostics, change-set, and execution services.
- Small inline “explain/fix this cell” actions may later be another client of
  the same service.
- Provider adapters, thread storage, token streaming, billing, and a full chat
  UI are not prerequisites for the local MCP preview.
- Persistent agent/prompt cells, autonomous background runs, Git mutation, and
  scheduled agents remain out of scope.

## 8. Professional notebook UX and operational behavior

### 8.1 Data-source workflow

Add one **Add data** entry point with:

- configured warehouse connections and a searchable catalog browser;
- pipeline/source assets grouped by connection;
- local file picker and configured object-storage browser;
- HTTP request source;
- a clear full-versus-sample choice before a potentially large transfer.

Creating a warehouse source opens its SQL cell with the connection shown in the
cell header. Source status shows connection, environment, snapshot age, rows,
bytes, completeness, and refresh action. Local transforms look visibly
different from source-native cells without turning every cell into a large
metadata form.

### 8.2 Results and execution

- Keep preview limits separate from materialized result size and say exactly
  when only the first rows are shown.
- Restore result schema/count/provenance after a server restart from session
  metadata.
- Add CSV and Parquet download for a completed local relation.
- Virtualize or page large preview tables; never render an unbounded result in
  React.
- Preserve scroll position while logs/results update and auto-follow only when
  the user is already near the end.
- Show queued/running/cancelling/failed/blocked states per block, with one
  notebook-level Stop action.
- Distinguish a stale local transform from a source snapshot that is merely old
  but still intentionally cached.
- Keep Python process-per-run initially. Surface environment provisioning and
  dependency installation progress; do not introduce a hidden persistent
  kernel as part of multi-connection work.

### 8.3 Editing and reproducibility

- Add stable IDs for markdown and visualization blocks.
- Add notebook-wide CAS for manifest and compound edits while retaining the
  current fast per-cell save queue for ordinary typing.
- Make cell/source/visualization definition diagnostics addressable by block ID
  and source span.
- Persist deterministic serialization and avoid rewriting unrelated YAML/SQL.
- Add typed notebook parameters only after the v2 block model lands. Parameters
  should have stable IDs, type/default/current runtime value, safe Jinja/Python
  access, and eventually feed visualization/dashboard filters.
- Keep auto-recompute opt-in state local. A Git checkout must not start remote
  queries merely because a notebook is opened.

### 8.4 Promotion

Extend promotion by block role:

- local SQL/Python transform -> ordinary pipeline SQL/Python asset;
- warehouse source SQL -> SQL asset on that connection, or a reviewed
  source-plus-load pair when the selected target differs;
- file/object/HTTP source -> Seed/API/Load asset as appropriate;
- visualization -> remains in the notebook initially; later may be copied into
  a dashboard/report artifact;
- markdown -> optional asset/notebook documentation, never executable.

Promotion must preview dialect/connection/materialization consequences and
write through one server transaction. It must not silently turn an exploratory
sample into a production source.

## 9. Delivery plan

### Phase 0 — contracts and correctness

- Introduce manifest v2 readers/writers, stable markdown/visualization IDs, and
  compatibility fixtures without auto-rewriting notebooks on open.
- Define the incremental `ArtifactIndex`, stable artifact/component references,
  derived capabilities, containment edges, and column-aware dependencies beside
  the current pipeline and notebook state. Initially register notebooks, their
  components, and existing pipeline assets without changing either run graph.
- Add notebook-wide snapshot revisions and semantic/recoverable multi-file
  transactions.
- Define `NotebookBlockExecutor`, `NotebookTransferService`, `TabularArtifact`,
  source provenance, versioned visualization DTOs, canonical presentation
  types, and the backend presentation checker contract.
- Split preview limits from snapshot completeness immediately. Until typed full
  transfer ships, reject an over-cap implicit import instead of publishing a
  truncated table.
- Add deterministic migration tests for current notebooks and `@viz` comments.

Exit: old notebooks load unchanged; a compound edit conflicts or applies
atomically; no downstream relation can masquerade as complete after truncation.

### Phase 1 — named warehouse sources and typed local snapshots

- Make connection-bound SQL cells execute read-only through the named
  connection and materialize the typed result into DuckDB.
- Implement DuckDB attach and Sling-to-Parquet adapters, with the bounded typed
  direct fallback.
- Add atomic snapshot swap, richer import/run manifests, restart
  reconstruction, and explicit full/sample modes.
- Give source cells connection-aware Monaco/LSP context and local transforms
  notebook-session context.
- Add the **Add data** warehouse relation browser and snapshot status UI.
- Preserve current implicit pipeline-asset imports as compatibility sugar, but
  route them through the same transfer service and offer a quick action to make
  the source explicit.

Exit: one notebook can query at least DuckDB and Postgres sources, join the
typed snapshots locally, restart Renart, and retain honest provenance and
results. Credentialed warehouse adapters use the same contract.

### Phase 2 — visualization blocks and additional sources

- Add the inline visual builder plus Definition editor, shared preview, schema
  validation, deterministic serialization, and accessible responsive layout.
- Register each visualization as a stable child component and project its
  dataset/field usage into the artifact graph without adding chart nodes to the
  notebook execution DAG or default pipeline canvas.
- Implement field/type visualization rules in the shared backend checker and
  expose identical findings through `renart type-check`, HTTP, and the
  definition editor.
- Add one-click `@viz` migration and stop writing new directives.
- Extend loader/executor support to local files, configured object storage, and
  HTTP sources by reusing existing storage/request DTOs and browser surfaces.
- Add CSV/Parquet result export and source-block promotion plans.

Exit: a user can build/edit the same chart visually or as YAML, create multiple
visualizations from one result, statically catch missing/incompatible columns,
and combine warehouse/file/API sources without hidden partial data.

### Phase 3 — local MCP developer preview

- Add the provider-neutral notebook snapshot/change-set service and MCP-safe DTO
  schemas.
- Add `renart mcp` using the official Go SDK and stdio transport, with server
  discovery/embedded fallback.
- Expose the bounded v1 resources/tools, change-set validation/diff/apply,
  execution status, cancellation, and result schema/sample limits.
- Publish setup instructions for Codex, Claude Code, and OpenCode plus a
  notebook-specific agent instruction template.
- Test a fixed corpus: explain a failure, add a source plus transform, repair an
  invalid query, add a visualization, conflict with a human edit, and apply an
  exact Git diff.

Exit: each client can perform the corpus using only Renart's MCP tools; tests
prove revision conflicts, no generic path/API escape in the tool surface, and
no commit/push side effect.

Phase 3 may proceed alongside Phase 1 after Phase 0. The visualization part of
Phase 2 can do the same, while its additional source adapters build on Phase 1's
transfer service. None of the tracks should invent its own mutation model.

### Phase 4 — release candidate hardening

- Add typed parameters and use them consistently in local/source SQL, Python,
  source requests, and visualization filters.
- Add the shared filter definition, default/option/binding checker, and safe
  typed value rendering needed by later dashboards.
- Complete result virtualization/accessibility, run-state recovery, source
  refresh UX, error messages, and responsive/mobile behavior.
- Harden Python fingerprints and decide whether safe Python auto-recompute is
  worth adding; do not enable it implicitly.
- Add performance budgets and telemetry visible only locally: query duration,
  transferred bytes, DuckDB size, preview rendering time, and Python startup.
- Write user documentation only for the behavior that has shipped and remove
  experimental wording once the release matrix passes.
- Fold the as-built state into `architecture/notebooks.md` and
  `architecture/sql-lsp.md`; delete this plan when no proposed work remains.

Exit: the notebook portions of the release matrix below pass without retry-only
exceptions, migrations are documented/reversible, and supported
connection/source combinations are stated precisely.

### Phase 5 — Git-native dashboards and reports

- Add original, versioned dashboard/report artifact schemas that reference
  pipeline assets or statically analyzed named datasets.
- Register dashboard/report containers and their datasets, visualizations,
  filters, and sections in the `ArtifactIndex`; keep the container as the
  versioning/deployment unit while components remain addressable type-check and
  lineage nodes.
- Reuse the visualization builder/definition editor and backend presentation
  checker; add responsive dashboard layout editing and report narrative/print
  composition as separate routes.
- Add dashboard filters with static or dataset-backed options, typed bindings,
  dependency-aware refresh, validated URL state, and clear loading/error states.
- Add reverse references from assets/columns to presentation consumers and make
  presentation errors part of deployment review and CLI type-check output.
- Require every production visualization field/type to resolve from the
  pipeline definition or declared dataset schema before deployment.
- Keep publishing, access control, and scheduled refresh out until local
  artifacts and validation are solid; do not make unshipped surfaces part of
  user-facing positioning.

Exit: removing or changing an asset column breaks type-check and deployment
review before a dashboard/report is updated in production; valid filters drive
only their affected datasets; notebook/dashboard/report renderers agree on the
same visualization fixtures.

## 10. Validation matrix

### 10.1 Backend and format

- v1 read compatibility and byte-stable no-op saves;
- explicit v1 -> v2 migration, including multiple/malformed `@viz` comments;
- stable IDs and notebook revision determinism across rename/reorder;
- multi-file transaction success, conflict, injected failure, rollback, and
  startup recovery;
- DAG validation for every data-producing block kind;
- stable artifact/component identities, derived capabilities, containment, and
  dependency aggregation without altering pipeline execution order;
- column-level producer -> dataset -> visualization/filter impact edges;
- source cache-key invalidation by environment/connection/query/definition;
- complete versus sample propagation through downstream results.

### 10.2 Transfer fidelity

For DuckDB attach, Postgres via Parquet, and each later adapter:

- null-only columns with declared source types;
- signed integers of different widths, floating point, high-precision decimal;
- date, time, timestamps with/without timezone, and intervals where supported;
- boolean, text, Unicode, binary;
- JSON, arrays, and structs with a documented fallback when DuckDB cannot
  preserve the source representation;
- duplicate/case-sensitive/reserved-word column names;
- zero rows with known schema;
- cancellation, timeout, disk/row budget, process failure, and server shutdown;
- atomic preservation of the previous snapshot on every failure;
- no secret in argv, logs, HTTP responses, provenance, or MCP results.

### 10.3 Intelligence and UI

- remote source dialect, catalog completion, and diagnostics;
- local sibling/source output completion after static inference and after run;
- no suggestions for local cell names inside a remote source namespace;
- connection/environment change invalidates the correct observations;
- visual builder -> YAML -> builder round trip for every supported control;
- missing fields and incompatible axis/format types produce the same finding in
  the builder, definition editor, HTTP type check, and CLI type check;
- invalid definition remains editable and cannot corrupt the manifest;
- deleted/renamed source columns produce targeted visualization diagnostics;
- child components are directly linkable and expandable from their parent but
  do not leak into the execution DAG or default canvas as top-level assets;
- source picker, transfer progress/cancel, explicit sample state, restart
  restoration, result export, keyboard navigation, and narrow viewport.

### 10.4 Presentation artifacts and filters

- canonical physical-to-semantic type mapping across supported warehouses;
- each chart kind accepts/rejects the intended field categories;
- missing asset/dataset/field, unknown required type, invalid format, and
  incompatible multi-series findings have stable codes and source spans;
- notebook unknown types warn, while dashboard/report deploy validation blocks;
- asset/column rename or removal reports every affected visualization;
- filter default, static options, dataset-backed options, URL value, operator,
  and column compatibility;
- option datasets with zero rows retain their declared schema;
- visual/definition edits cannot bypass the backend checker;
- dashboard/report/notebook fixtures render equivalently from one definition.

### 10.5 Live E2E

- DuckDB + Postgres source cells produce equivalent local schemas/results;
- two different source connections join in one DuckDB transform;
- a source query larger than the preview limit remains complete;
- an exceeded snapshot budget fails instead of creating a partial relation;
- file/object/HTTP source -> SQL -> Python -> visualization chain;
- server restart restores session result metadata;
- concurrent cell/UI/MCP edits conflict rather than overwrite;
- fake MCP client covers every tool and cancellation;
- changing a pipeline column breaks a consuming dashboard in type-check and
  deployment review, then clears after the definition is repaired;
- dashboard filter changes update URL state and refresh only affected datasets;
- optional Codex/Claude Code/OpenCode smoke tests validate configuration and
  protocol compatibility without becoming required CI.

Credentialed Snowflake, BigQuery, Redshift, Databricks, and other warehouse
checks belong in the existing live warehouse matrix when credentials are
available. A passing compatibility unit test is not called an end-to-end
warehouse guarantee.

## 11. Rejected alternatives and deferred decisions

### Rejected for the first release

- **Materialize notebook tables in arbitrary warehouses.** It adds scratch
  schema naming, permissions, cost, cleanup/TTL, view-table lifecycle, and
  cross-source upload problems while making the common local workflow slower.
- **Offer “local or remote target” as a per-cell toggle.** This makes dependency
  execution location contagious and turns every edge into a transfer planner.
- **Pretend DuckDB extensions provide universal federation.** They are useful
  optimized adapters for some systems, not a consistent connection matrix.
- **Keep the current truncated import and show a warning badge.** A warning does
  not make downstream aggregates correct.
- **Give notebook Python unrestricted named connections immediately.** It hides
  data movement and lineage inside code; explicit source cells solve the core
  job first.
- **Expose the full pipeline asset registry as notebook cell types.** Most types
  do not return a relation and side-effecting targets undermine exploratory
  safety.
- **Encode presentation artifacts as `pipeline.Asset`.** Their containment,
  presentation, validation, and deployment semantics do not match a
  materializable relation; a workspace artifact projection preserves shared
  lineage without weakening execution types.
- **Keep visualization state in SQL comments.** It prevents multiple views per
  result and makes visual editing a source-code rewrite.
- **Build a native provider/chat subsystem before proving tools.** The local MCP
  bridge validates notebook operations with far less permanent product surface.
- **Give MCP generic filesystem or shell tools.** The user's coding agent may
  already have them, but Renart should not duplicate or endorse that path.

### Deferred until evidence exists

- exact default row/byte/time budgets (measure representative local and remote
  workloads first; make them configurable and conservative);
- optional remote scratch schemas for data that cannot reasonably fit locally;
- direct remote queries from Python;
- a persistent Python kernel;
- native Ask/Edit UI and provider adapters;
- selective acceptance inside one dependent change set;
- cross-notebook data references;
- dashboard/report publication, access control, and scheduled refresh.

None of these deferred choices blocks Phases 0–3.

## 12. Research basis

The design is grounded in current Renart code plus primary documentation:

- Renart: [`architecture/notebooks.md`](../architecture/notebooks.md),
  [`architecture/sql-lsp.md`](../architecture/sql-lsp.md),
  `internal/web/notebook`, `internal/web/service/notebook_service.go`, the
  Python broker/operator, the direct connection query path, and the existing
  Sling launcher.
- Multi-source notebook behavior: Hex documents separate warehouse SQL and
  dataframe/DuckDB SQL sources, and its multi-source tutorial materializes
  sources as dataframes before joining them; Deepnote likewise executes a SQL
  block against a chosen integration and makes its result available as a
  dataframe for downstream work:
  [Hex SQL cells](https://learn.hex.tech/docs/explore-data/cells/sql-cells/sql-cells-introduction),
  [Hex merging data sources](https://learn.hex.tech/tutorials/connect-to-data/merging-data-sources),
  [Deepnote SQL blocks](https://deepnote.com/docs/sql-cells).
- Transfer feasibility: Sling's run command accepts a table, inline query, or
  SQL file as `--src-stream` and a local file as the target:
  [Sling run](https://docs.slingdata.io/sling-cli/run),
  [database to file](https://docs.slingdata.io/examples/database-to-file).
- Declarative visualization precedent: a versioned field-to-encoding document
  is a well-established way to separate data from presentation:
  [Vega-Lite overview](https://vega.github.io/vega-lite/docs/),
  [view specification](https://vega.github.io/vega-lite/docs/spec.html).
  Renart's schema remains deliberately smaller and original, rendered through
  its existing components.
- MCP: the protocol separates tools, resources, and prompts and supports local
  stdio transport; the official Go SDK is currently listed as Tier 1:
  [architecture](https://modelcontextprotocol.io/docs/2026-07-28/learn/architecture),
  [SDKs](https://modelcontextprotocol.io/docs/2026-07-28/sdk).
- Requested clients: official documentation confirms local stdio MCP setup for
  [Codex](https://learn.chatgpt.com/docs/extend/mcp?surface=cli),
  [Claude Code](https://code.claude.com/docs/en/mcp), and
  [OpenCode](https://opencode.ai/docs/mcp-servers/).
