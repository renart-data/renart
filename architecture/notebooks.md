# Notebooks — current architecture

Status: current state, August 2026. A notebook is
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
cell references, identity-bearing markdown blocks, parameter-backed control
references, and structured visualization blocks. It also stores typed parameter
definitions and defaults. SQL and Python
cells remain ordinary Bruin assets with `class: notebook`. A `.source.yml` file
is a small Renart-owned source definition that
also has a durable cell ID and produces one relation.

- Cell IDs survive filename/name changes. Newly created cells use concise
  adjective-noun names such as `quiet_river`, with collision checks and a
  suffix fallback.
- Markdown and visualization blocks have their own stable IDs. Cell IDs double
  as the block ID for executable/source cells; control identity is the
  namespaced parameter ID.
- Merely loading a legacy manifest does not rewrite it. An explicit upgrade
  deterministically assigns presentation IDs. Legacy `@viz` comments remain
  readable until the user runs the migration operation.
- `SnapshotRevision` hashes the manifest and every authored block file. This
  notebook-wide revision is the compare-and-swap boundary for compound edits.
- `model.ArtifactIndex` projects notebooks, cells, sources, controls, and visualizations
  beside pipeline assets. It records containment, derived capabilities, and
  relation/column dependencies without putting presentation components into a
  run DAG or weakening Bruin's pipeline asset model.

The filesystem remains authoritative. Runtime session files and transfer
artifacts live under `.renart` and are not authored state.

## 2. Semantic changes and transactions

`service.NotebookChangeSet` is an ordered batch of operations addressed by
durable IDs, never caller-owned paths. Supported domain operations cover
manifest upgrade; cell create/update/rename/delete/source configuration and
source-preserving SQL relation rename, column qualification, or relation alias;
file/HTTP/object source create/update; markdown and visualization
create/update; parameter replacement; ordered control create/update/delete;
legacy visualization migration; and block move/delete.

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
introducing a second state system. The notebook's shared **Add** rail exposes
SQL, Python, Markdown, every typed control, and all visualization types from any
scroll position. Clicking a rail item appends it; quiet insertion points between
blocks accept the same items from a menu or drag operation. Both paths use the
same positional semantic operations. Insertion anchors are durable raw
cell/block IDs, while prefixed React keys remain UI-only.

## 3. Run graph and execution roles

Only data-producing cells enter the notebook DAG. Markdown, controls, and
visualizations are ordered presentation components, not executable assets.

The runner delegates connection-bound warehouse SQL and Renart-owned source
definitions through `NotebookBlockExecutor` and moves their external data
through `NotebookTransferService`. Local DuckDB SQL and Python retain dedicated
session/broker paths because their inputs and publication lifecycle are not a
remote-source execution contract. Every transferable result is a
`TabularArtifact` with a physical schema, row/byte count, complete/sample state,
and credential-free provenance. The runner owns DAG order, cancellation,
serialized session access, preview creation, validation, and the atomic swap
into the durable logical cell object.

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

1. compatible pipeline-relation imports already in a local DuckDB file use
   the attach/copy path; a connection-backed query against a local DuckDB file
   uses a native read-only DuckDB query-to-Parquet export, then the same bounded
   snapshot publication path as every other source;
2. non-DuckDB pipeline-asset references and explicit database/object/file
   sources use the shared hardened Sling
   launcher to create typed Parquet in a mode-`0700` staging directory;
   warehouse SQL uses a private replication document with an explicit `sql`
   entry so Sling cannot confuse the query file with a file-data source;
3. there is no JSON/map-row or inferred-type fallback. A connection that cannot
   produce a typed Parquet snapshot is unsupported for notebook transfer;
4. unsupported, cancelled, timed-out, or over-budget work fails and keeps the
   previous good snapshot.

Credentials are resolved for one operation and passed in scoped process
environment. They are absent from argv, source provenance, browser DTOs, and
MCP results. The shared process limiter, context cancellation, time limit, and
byte/row monitors apply to source transfers. Temporary Parquet is deleted after
publication.

The server flags `--notebook-snapshot-max-bytes` (default 2 GiB) and
`--notebook-snapshot-timeout` (default 30 minutes) configure the byte and time
budgets for every explicit source and implicit pipeline-asset transfer. Values
must be positive. Preview limits remain separate and do not change publication
completeness.

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

An open notebook run reuses one ADBC database connection for its serialized
statements instead of reopening the driver for each query. Server-owned exports
use a separate lazy connection so authored filesystem restrictions cannot leak
into the trusted staging path. Session-manifest migrations are idempotent and
versioned; a process-local version cache skips repeated DDL after the first open
while a restart or recreated database runs the migrations again.

Versioned session tables retain source snapshot provenance and successful cell
run summaries: fingerprints, timestamps, schemas, row counts, durations,
materialization kind, and source snapshot IDs. On restart, Renart reconstructs
result summaries, verifies the current definition fingerprints, and queries
only a bounded preview from live session objects. Definitions remain Git state;
runtime observations do not.

SQL fingerprints use the canonical ID-resolved query. Python fingerprints are
deliberately conservative: they hash the exact cell bytes, typed parameter
values, execution metadata, sibling/external references, and the effective uv
dependency input. Dependency discovery matches execution precedence from the
cell directory to the workspace root (`requirements.txt`, otherwise
`pyproject.toml` plus its `uv.lock`). A successful run captures that environment
again after uv exits, so a lockfile created or updated by the run describes the
result that was actually published.

Source blocks and connection-backed SQL cells render the same compact snapshot
summary from that runtime record: complete/sample state, capture time,
connection, environment, row count, and byte size. A changed source definition
is distinguished from an older but intentionally cached snapshot. Result
previews remain bounded and expose a cell-specific accessible table name.
Captured cell logs and source/cell failures use the shared ANSI renderer. It
accepts both native escape bytes and escaped control-picture sequences, so
command output keeps terminal colors without exposing raw control glyphs.

An ordinary **Run all** reuses a source snapshot only when its rendered source
definition, typed parameter values, environment, connection, and full/sample
policy still match the published manifest. A changed Jinja execution window or
source definition refreshes it. Running a source cell directly remains an
explicit refresh, and **Refresh sources and run all** bypasses both source and
external-import reuse. Cache reuse never guesses that the remote data itself is
unchanged; the original capture timestamp stays visible.

The in-process runtime tracks the cell IDs owned by every active manual run and
the current auto-recompute wave. Both full SSE snapshots and
`GET /api/notebooks/{id}/runtime` expose their union, so a newly opened tab can
show and cancel work that began before it subscribed. Concurrent runs are
reference-safe: finishing one run does not clear cells still owned by another.

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

## 7. Authored controls and typed parameters

Version-2 manifests can declare text, number, slider, boolean, select,
multi-select, date, and date-range parameters. Sliders add checked numeric
`min`, `max`, and `step` metadata. The UI presents these as **controls** alongside
the equivalent dashboard/report inputs. Each declaration has a stable
lowercase ID, a typed Git-tracked default, an optional label, and optional
static choices. The shared `presentation` checker validates IDs, types,
defaults, choices, and the filter/dataset bindings dashboard and report hosts
reuse.

The frontend also uses one authored-control editor, default-value contract,
option resolver, typed value field, draggable type palette, and contextual
inspector across notebook and presentation hosts. A notebook keeps each typed
definition under `parameters:` and places it in document order with a
`control: <parameter-id>` block. This avoids duplicating runtime declarations.
Legacy/unplaced parameters render as individual control cells ahead of the
ordered document until they are migrated; there is no separate top-level
control strip. Presentation files retain `filters:` for their binding contract.
This is one UI/domain primitive with host-specific storage adapters, not a
second runtime state system.

Notebook `select` and `multi_select` controls can source choices from a
data-producing cell by declaring its durable cell ID plus `value_field` and an
optional `label_field`. The editor writes durable IDs for new selections,
continues to resolve legacy cell-name references, and offers the producer's
last runtime columns (falling back to declared columns) in the field pickers.
Notebook validation reports option sources that no longer resolve to a cell.

Choices are route-local snapshots, not authored values. Renart reads distinct,
deterministically ordered values from the producer's existing local DuckDB
relation, caps the result at 1,000 choices, and never executes the producer or
queries a source warehouse for this operation. A refresh requires a successful,
current producer result. Users can refresh explicitly; a new successful result
delta for that producer also refreshes affected controls through the existing
SSE runtime stream. Opening a notebook, editing a control, and changing a
control value do not query for choices. When a producer becomes stale, the last
snapshot remains usable and visible, but refresh stays disabled until the
producer runs again. Request sequencing prevents an older response from
replacing a newer definition or route state.

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
donut presentations, including axes/field encodings, multiple series, named
color palettes, stacking, legends, labels, formatting, and a presentation row
limit. Relational
work such as joins and aggregation stays in an explicit SQL/Python cell.
The shared renderer gives every chart, table, and KPI a stable accessible name;
all chart families enable the Recharts keyboard/screen-reader layer. It converts
only rows inside the presentation budget and caps rendered series at twenty,
with a visible status message when rows or series are omitted. The same budget
and semantics apply in notebooks, dashboards, reports, and audience viewers.

Notebook Markdown is visual-first: a shared Tiptap editor parses the authored
Markdown, serializes edits back to Markdown, and keeps an explicit source mode
for exact repair. Markdown, SQL, source, and visualization blocks use a quiet
document treatment when idle; their boundary and contextual controls become
visible on hover, focus, or selection. The Add rail uses code-native previews
for SQL, Python, text, controls, and visualization types instead of generic
glyphs. Contextual visualization settings use a compact four-column chart
picker so the Add rail remains visually distinct. Dragging targets only the
small insertion gaps between blocks; the whole notebook never becomes a drop
surface.

Notebook authoring uses the same responsive rail primitive as presentations.
Its **Outline**, **Data**, **Add**, and **AI** tabs replace separate header and
agent-panel entry points: Outline navigates durable blocks, Data summarizes
results and opens source import, Add owns block/chart creation, and AI embeds
the notebook-scoped conversation. The rail remains visible on wide screens and
moves into one left Sheet on narrower screens so the notebook canvas stays the
primary surface.

The Add data dialog is a presenter over a focused source controller. A pure
reducer owns its warehouse/file/HTTP form transitions, table discovery state,
request normalization, and the policy that distinguishes local DuckDB/file
sources from imports requiring review. The controller waits for pending cell
saves and then uses the ordinary notebook change APIs; it does not become a
second document store. The server response and subsequent workspace/runtime
events remain authoritative.

Visualization blocks keep the rendered chart in that primary document flow.
Selecting a chart opens its Visual/Definition controls in a contextual right
inspector on wide screens or a right Sheet on narrower screens; the outline and
new-chart flow select the same durable block. Draft source/definition state
continues to live with the mounted block, so breakpoint changes and workspace
SSE updates do not discard unapplied edits. The canvas card reflects the draft,
shows compact validation/draft status, and returns to quiet block chrome when
the inspector closes.

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
inherits their declared/derived columns, and statically infers query-dataset
outputs when their inputs are represented by the Git-authored workspace graph.
Explicit query-dataset columns remain the durable override for queries whose
schema cannot be inferred. Inferred columns are exposed separately from the
authored DTO fields so visual authoring can use them without silently persisting
derived metadata. The checker strictly validates visualization fields/types plus filter options and
bindings. The workspace DTO exposes these authored artifacts under
`presentations`. `ArtifactIndex` registers dashboard/report containers and their
dataset, filter, visualization, and section components, including asset and
column-level lineage. These components do not enter a pipeline or notebook run
graph.

The artifact index also projects conservative cross-host column impact. For SQL
assets and notebook SQL cells, Golyglot maps known producer columns to declared
consumer outputs without rewriting the query; single-source wildcard projections
and direct `WHERE`/join references are included. Asset-backed presentation
datasets carry identity mappings into filter and visualization roles. Renart
then follows those mappings transitively and publishes
`breaking_column_impacts`: positive evidence that removing or renaming a column
would break a downstream output or authored presentation use. Unparseable Jinja,
unknown schemas, relation-only edges, nested predicate scopes, and ambiguous
short relation names deliberately remain unknown rather than becoming guessed
lineage.

Wide direct projections reuse one schema-aware Golyglot `AnalyzeQuery` result
for output discovery and every positively resolved table-column mapping;
single-source wildcard identity mappings likewise bypass per-output lineage
calls. CTE, derived-table, set-operation, and unresolved projections retain the
full recursive schema-aware/schema-free lineage fallback. The benchmark in
`artifact_column_lineage_benchmark_test.go` covers 10/50/200 direct columns plus
a CTE and wildcard case so future semantic changes keep both conservative
correctness and scaling visible.

The `/dashboards` and `/reports` routes provide list/create flows plus a shared
canvas-first builder. List headers contain the compact dashboard/report switch;
detail routes use one responsive document-authoring command bar for navigation,
title, Visual/Definition mode, validation and preview state, history, refresh,
preview, discard, and save. The file path and duplicated presentation title are
not editor chrome. The dashboard viewport control sits with the canvas preview,
and creation remains owned by the Add rail. Their rail and the notebook rail are
composed from one document-authoring sidebar primitive, while each host supplies
its own canvas behavior. The center pane is the live draft: dashboards use a
12-column drag/resize grid and reports use an ordered document canvas with
inline Markdown, insert points, and page breaks. A compact left rail contains
the component palette, report outline, and asset- or query-backed datasets; the
right inspector exposes only the selected dataset, filter, visualization, or
section. Narrow layouts keep the canvas primary and move both rails into
Sheets. Inspector forms switch to compact, shrink-safe field layouts and hide
horizontal overflow rather than widening the page or Sheet. Dashboard
tablet/mobile modes are deterministic previews derived from the one authored
desktop layout rather than hidden breakpoint state.

Report text sections use the shared visual-first Markdown editor and retain an
exact source-mode escape hatch. The document canvas selects a visualization
directly when it is clicked, and the inspector can reopen that visualization's
full settings from its containing report section. Dashboard and report Add
rails share the same large chart and typed-control previews with notebooks.
Their contextual settings share the compact chart picker and checked palette
selector.

Visualization creation is schema-aware. The builder deterministically suggests
tables, KPIs, and compatible charts from known dataset columns, generates
stable collision-safe IDs, and renders the draft through the same component as
the audience viewer. Dashboard filters stay visible above the grid and rerun
only visualizations with matching bindings. Pointer layout changes commit once
at drag/resize end; width and directional controls provide a non-pointer
fallback and disable directions that would leave the grid. Dashboard
visualizations and report sections are focusable canvas units, so keyboard
focus selects the component and reveals its contextual controls. Report
reorders announce the resulting position to assistive technology. The shared
Add rail offers all eight chart families as large previews;
they can be clicked or dragged onto a dashboard grid or a report insertion
line. A drop uses the selected dataset when possible and creates the same
schema-aware definition as the dialog path. Report blocks also support drag and
explicit up/down controls.

Query-backed datasets use an embedded Monaco editor with the selected
connection's dialect, the canonical workspace graph, remote-catalog enrichment,
and DuckDB file-relation support. The editor document is presentation-scoped
and has no pipeline output relation or declared asset contract. The presentation
checker runs the same pure output-schema inference over Git-known workspace
relations; live remote-catalog observations remain editor-only and never become
deploy-time type evidence. Authors declare output columns only when static
inference is incomplete or when they want an explicit durable override.

The browser keeps a bounded route-local undo/redo history, coalesces text edits,
and never autosaves. Save and Discard remain explicit Git boundaries;
Cmd/Ctrl+S uses the same save path and navigation with a dirty draft requires
confirmation. Definition mode still edits the complete strict YAML document in
Monaco. Both modes write through the Go service, share one content revision,
retain drafts on conflicts, and let workspace SSE reconcile outside changes.
Typed snapshots are serialized deterministically by the server; malformed YAML
never replaces the last valid file. Structural/schema findings remain visible
after a save because invalid Git state must stay repairable rather than becoming
impossible to author.

The builder command bar groups active checker findings into one review menu.
Choosing a finding selects the referenced dataset, control, visualization,
layout item, report section, or artifact, opens the contextual inspector on
narrow screens, and focuses the closest editable field identified by the
finding path. The same structured path therefore explains a problem in the
definition editor and provides a direct repair path in visual mode.

`POST /api/presentations/{id}/preview` accepts an unsaved artifact plus the
saved revision, normalizes and checks it, and runs requested visualizations via
the bounded read-only runtime without writing a file or publishing a workspace
event. The builder debounces data-definition changes, cancels superseded
requests, rejects stale responses, preserves the last result while marked
stale, and tracks loading per visualization. Invalid drafts return structured
findings without execution. Preview and Save both reject a changed base
revision rather than rebasing a browser draft silently.

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
auto-recompute closure. Editing an execution cell marks it and descendants
stale; changing Python dependencies marks every Python cell and its descendants
stale in one transition. Renart coalesces saves, validates each safe SQL
dependency wave with the same parse context as Monaco, and publishes
`notebook.runtime` deltas over workspace SSE. Runtime snapshots also compare
the latest filesystem fingerprints with the last attempted definitions, so
direct Git/editor changes converge without polling or a server restart. Python
and remote source execution are never triggered merely by opening a notebook;
Python remains manual even when auto-recompute is enabled.

Manual and automatic runs register before entering the shared session. Cancel
is a barrier: it cancels DuckDB, transfers, and Python work and waits for the
session to be released before returning. The frontend merges result deltas and
retains drafts on revision conflicts; it does not poll or treat Jotai as
persistent truth. A semantic mutation response remains the rendered notebook
until SSE reaches the same revision. If a differing workspace snapshot races
that response, the frontend resolves it once against the authoritative notebook
endpoint instead of briefly dropping newly authored blocks.

The frontend runtime controller models the initial snapshot, SSE deltas, manual
run, cancellation, and session reset as notebook-scoped events. Server-reported
running cells and request-local optimistic targets are separate sets, so an HTTP
request finishing cannot erase newer SSE state. Switching notebooks resets the
local projection and late results from the previous notebook are ignored. A run
still crosses the pending-save barrier before calling the server; the reducer is
only a view projection, not runtime authority.

Preview tables stay bounded, block editors grow with short content before
using their internal scroll area, and output panes retain user scroll position
unless the user is already following the end. The shared result table switches
to fixed-row windowing above fifty loaded rows: only the viewport plus a small
overscan is mounted, spacer rows retain the complete scroll geometry, and ARIA
row counts/indexes retain the logical table position. Notebook cells show every
row in the server's bounded preview instead of applying a second frontend cap.

Local SQL previews append `count(*) over ()` to the bounded query and remove the
final bookkeeping column by position, so exact row count and preview require one
scan without exposing an internal column. Every cell result may also carry
local-only performance observations: request setup/total/runtime-sync, batch and
session-open duration, cell materialization/preview/metadata-write duration,
notebook DuckDB file/WAL bytes, transferred snapshot or Python Parquet bytes,
and time until a Python wrapper starts. The browser adds its preview render
duration and mounted-row count. These measurements are shown in the result's
**Performance** hover card, are never authored into the notebook, and are not
sent to an external telemetry service. Restart restoration can recompute session
size, source transfer size, and preview query time; ephemeral request, Python
startup, and materialization observations are intentionally absent after
restart.

## 11. Local notebook agents

Renart has two local-agent entry points backed by the same semantic notebook
contract: the native notebook chat and the external `renart mcp` developer
integration. Neither path sends workspace data to a Renart-hosted service;
model selection, authentication, and network use belong to the user's locally
installed agent client.

### 11.1 Native Ask/Edit chat

The shared notebook rail exposes chat under its **AI** tab; on narrow layouts
the whole authoring rail uses the notebook-tools Sheet. Renart
discovers Codex, Claude Code, and OpenCode on the server `PATH`; unavailable
clients remain visible but disabled. It launches the selected client in a
private non-repository directory, passes the prompt over stdin, and installs a
per-process Renart MCP configuration. Provider sessions resume independently
per notebook, provider, and capability mode.

**Ask** launches a notebook-scoped MCP server with only the nine bounded read
tools. Change-set and run tools do not exist in that process. **Edit** grants
the complete semantic change-set and run catalog for the turn; choosing Edit
is the UI authority to apply and verify notebook work. The panel starts in Ask
each time it opens rather than persisting the stronger capability. Both modes reject access
to another notebook. Claude Code and OpenCode launch without their built-in
filesystem/shell tools; Codex uses a private working directory and read-only
sandbox. If any normalized provider stream reports a generic shell,
filesystem, or web tool, Renart stops the process and reports the blocked
activity. This is a narrow local integration boundary, not a general-purpose
OS sandbox.

Notebook mutations stay scoped to the selected notebook, while the read catalog
also offers bounded workspace-wide metadata search. Search projects the current
`ArtifactIndex`: pipeline assets, notebook components, dashboards, reports,
datasets, and visualizations can be matched by title, type, connection,
capability, description, tag, materialization policy, direct neighbor, or
column. Pipeline results carry their declared materialization type/strategy and
incremental fields plus direct upstream/downstream artifact references. Each
lineage direction is capped at twelve references while retaining the full
count and an explicit truncation flag. Results contain no filesystem path,
asset URI, source credential, or live warehouse query. A relation-producing
pipeline asset includes a conservative sampled `cell.create` recipe so the
agent can add it through the ordinary semantic change-set path instead of
inventing connector details. The native Edit prompt tells the agent to compare
these facts and not mistake a truncate-and-replace snapshot for retained
history.

Edit's prepare tool publishes the exact supported dotted operation kinds as a
JSON Schema enum with field-specific source descriptions. Validation errors
repeat the valid values, and the prompt tells clients to correct from that
contract rather than probe aliases. Its visualization field projects the real
versioned definition shape, including the chart-type enum, singular `encoding`
object, and array-valued `y`/`tooltip` fields instead of exposing an untyped
map. For the supported SQL transformations, `cell.sql.refactor` parses the
current cell and applies Golyglot source-span edits; the reviewed diff preserves
comments, whitespace, quoting, and all unrelated text byte for byte. The Edit
prompt prefers that bounded operation over sending a complete replacement cell.
A source definition alone never transfers data. MCP-started execution
rejects the first import, a changed definition, or an explicit refresh for
every non-DuckDB source until the user reviews the query, sampling policy, and
row limit and runs it in Renart. Once that approved snapshot is current, the
agent may run downstream work without refreshing it.
The native runner stops a turn after six consecutive failed change preparations
(a successful preparation resets the counter), preventing an invalid client
loop from flooding the progress feed.

The service keeps a bounded in-memory transcript and normalized activity list,
not hidden reasoning. Every update publishes a complete monotonic
`notebook.agent` snapshot over workspace SSE. The panel uses the normal GET
state endpoint after mount or SSE reconnect, ignores older revisions, follows
new output only while the user is at the live edge, and retains the transcript
across notebook navigation. **New chat** removes provider session directories
and state. A Renart server restart also clears chats; transcripts are not
authored or durable state.

The composer can attach notebook cells and current-workspace pipeline assets
as structured references. The browser sends only a kind and opaque ID; the Go
service resolves each target against the current filesystem-backed notebook or
workspace snapshot, rejects out-of-scope IDs, and stores a canonical,
credential-free label/type/connection summary with the user message. Provider
prompts name those exact targets and require the agent to resolve their details
through the scoped semantic tools. References remain visible with the turn and
survive the same navigation/SSE restoration as the transcript.

The HTTP surface is notebook-scoped:

- `GET /api/notebooks/{id}/agent` returns the current snapshot and discovered
  providers;
- `POST /api/notebooks/{id}/agent/messages` starts one turn;
- `POST /api/notebooks/{id}/agent/cancel` terminates the provider process tree;
- `DELETE /api/notebooks/{id}/agent` starts a fresh chat.

Only one turn may run per notebook. Turns have a 30-minute limit, cancellation
kills the full provider/MCP process tree, stderr and transcript buffers are
bounded, private configuration files use owner-only permissions, and local
paths are redacted from returned provider errors.

### 11.2 External MCP developer integration

`renart mcp --workspace <root>` serves the official MCP stdio transport. It
discovers the owning Renart server through `.renart/server.json` and keeps the
discovery token inside `clientapi`; without a live owner it creates the same
headless notebook service graph in-process. Stdout is reserved for JSON-RPC and
logs go to stderr.

`internal/notebookmcp` exposes 16 schema-versioned, high-level tools:

- bounded, credential-free workspace artifact/catalog search;
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
generic REST/HTTP, or free-form SQL execution tool. For an independently
configured external client this constrains Renart's tool surface, not whatever
other shell or filesystem authority the user grants that client. The native
chat additionally launches the clients with the restrictions described above.

## 12. Promotion and current limits

Promotion is a reviewed two-step operation. The plan endpoint resolves every
selected block into its target asset type, source/target connection,
materialization, destination path, warnings, and credential-free file-change
summary. Apply carries the reviewed notebook revision and re-plans under the
notebook edit lock, so a concurrent authored change conflicts instead of
silently changing the result.

Local SQL and Python become ordinary executable assets; Python keeps `.py` and
Python frontmatter. Connection-bound warehouse SQL stays on its declared
connection and warns when that differs from the destination pipeline default.
A full local file becomes the target warehouse's Seed type (or a local-source
Load where that warehouse has no Seed type), an object-storage file becomes a
Load asset, and an HTTP source becomes an API asset while retaining its request
body, auth/pagination fields, response mapping, and declared columns. An
explicitly sampled source is rejected until the user changes it to a full
snapshot, so partial exploratory data never becomes a production-looking
source implicitly.

The destination assets, remaining-cell reference rewrites, removed source/cell
files, and `notebook.yml` update commit through one workspace-wide recoverable
journal. Startup recovery restores both notebook and pipeline files after an
interrupted promotion, and one logical workspace update reconciles the result.

Still parked or incomplete:

- remote scratch targets and direct remote reads from Python;
- persistent Python kernels and Python auto-recompute;
- cross-notebook data references;
- persistent or shareable agent transcripts and background agent turns;
- selective acceptance within one dependent change set.

## Test surface

`internal/web/notebook` covers loader/migration, DAGs, typed source execution,
snapshot fidelity, session manifests, cancellation, exports, typed parameter
fingerprints, Python broker, local run-performance observations,
rename invariance, and legacy directives. `internal/web/service` covers
workspace/artifact projection, semantic transactions and recovery, runtime
hydration, source adapters/policy, presentation checks, promotion, and
auto-recompute, including source-role translation and cross-directory rollback.
`internal/notebookmcp` drives the real SDK protocol in memory
and verifies the exact tool catalog, annotations, redaction, payload bounds,
revision conflict, exact apply, Python approval, asynchronous status, and
cancellation. Native-agent service tests cover provider commands, notebook and
Ask/Edit scoping, structured reference resolution, normalized streaming,
resumption, cancellation, private configuration, and generic-tool rejection. A
deterministic fake-provider Playwright test covers desktop/mobile streaming,
cell/asset references, and transcript restoration; a
real local Codex smoke test covers launch, MCP discovery, a tool call, and
session resumption. Playwright live tests also exercise a complete two-named-
connection Postgres-to-Parquet-to-DuckDB join, warehouse/file/HTTP source chains,
visual editing/migration, typed parameter editing/execution, export bytes,
reviewed source/connected-cell promotion, header-created durable markdown, and
race-sensitive notebook UI paths. Frontend unit coverage verifies bounded
virtual row windows, semantic row positions, and duplicate-column rendering.
Presentation live tests cover dashboard
creation, typed visual editing, deterministic save, definition-mode
round-tripping, shrink-safe desktop/mobile inspectors, URL-backed filters,
dependency-aware viewer refresh, and a
presentation error blocking/clearing its consumed pipeline deployment.
