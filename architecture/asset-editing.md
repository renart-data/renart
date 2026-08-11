# Asset editing — workbench, provenance, reconciliation

Status: current state (built on the `asset-editing-workbench` branch,
June 2026), with the not-yet-built pieces called out in §7.

## 1. Product thesis

Renart behaves like a **Bruin-native asset workbench**, not a visual wrapper
around YAML. The committed artifact remains a normal Bruin asset file — for
SQL assets the definition stays embedded between `/* @bruin` and `@bruin */`
in the same `.sql` file (Bruin treats a SQL file plus a sibling YAML as two
unrelated assets, so definitions are never split across files). Renart adds
editing surfaces, inference, reconciliation, and guardrails **without
creating a second source of truth**:

```
Bruin file        = canonical artifact
Renart UI         = editing and guidance layer
renart_* meta keys = compact user intent + provenance (exceptions only)
```

Design goals: Bruin compatibility (a repo edited through renart stays usable
by the `bruin` CLI, git, and external editors), high developer control, guided
onboarding, safe generation (inferred deps/columns never silently destroy
user-authored metadata), and clean git diffs.

## 2. Core model

Three concepts, of which only the first is committed:

1. **Final Bruin definition** — the YAML Bruin sees.
2. **Generated projection** — what renart infers from SQL AST, file path,
   asset type, or warehouse introspection.
3. **User intent** — manual additions, suppressions, overrides, ownership.

Physically: `final Bruin definition + compact provenance keys`. Existing-column
provenance lives in that Bruin column's own `meta` map; intent that refers to an
absent column or to the asset as a whole stays in the asset `meta`. Ownership is
**field-level**: column names are hard-generated from SQL, column types are
soft-generated (user-overridable, recorded as owned), descriptions/checks/
materialization are user-owned, `depends` is inference + manual additions.
Ownership is enforced by the server-side reconciler, not by editor
decorations.

## 3. Provenance storage (`internal/web/service/assetmeta`)

Both asset and column `meta` are Bruin `map[string]string` values. Provenance
therefore uses flat string keys, keeping every committed file loadable by plain
Bruin. Schema v3 places provenance beside an existing column when possible:

```
column.meta.renart_manual  true for a manually added column
column.meta.renart_owned   generated fields the user owns (field|field)
column.meta.renart_source  non-default type source (m or l)

renart_v         schema version
renart_g         generator version
renart_sig_deps  checksum of the renart-managed dependency projection
renart_sig_cols  checksum of the renart-managed column projection
renart_dep_add   manual dependencies (keys: a:<asset>#<mode> / u:<uri>#<mode>)
renart_dep_drop  inferred dependencies the user suppressed
renart_col_drop  inferred columns the user omitted
renart_col_map   rename memory (e:<exprhash>:col); optional
```

Only _exceptions_ are stored — inferred things are never listed; the file's
real `depends:`/`columns:` plus these keys reconstruct intent on the next
reconcile. SQL/definition inference is the implicit type source and therefore
costs no metadata. `renart_source` records only exceptions: `m` means the
materialized/current table supplied the saved type and `l` means a live response
did. Schema v2's asset-level `renart_col_add`, `renart_col_own`, and
`renart_col_src` are read for compatibility and migrate losslessly to column
metadata on the next Renart write. `renart_col_drop` and `renart_col_map` remain
asset-level because an omitted column cannot carry metadata. The key strings and
source codes are stable; changing one is a file-format migration. All
`assetmeta` functions are pure and unit-tested.

## 4. Reconciliation

**Dependencies** (`service/asset_dependencies.go`): inferred from the embedded
Polyglot WASM SQL AST, then

```
final depends = (inferred − renart_dep_drop) + renart_dep_add
```

**Columns** (`service/asset_columns_reconcile.go`): inferred columns are
merged into the asset's declared columns preserving user-authored metadata by
column name; `ReconcileAssetColumns` returns the merged set plus
`ReconcileItem`s for situations it cannot resolve automatically (a column with
user metadata no longer inferred → the UI asks map / keep manually / remove).

**Checksums / external edits**: on every canonical write the managed
dependency and column projections are hashed into `renart_sig_deps` /
`renart_sig_cols`. On load, a signature mismatch means an external edit:
unknown dependencies are adopted as manual (`dep_add`), missing inferred ones
as suppressed (`dep_drop`), externally changed generated fields become
user-owned, unknown columns become manual. External VS Code edits are safe by
default.

## 5. Transactions (`service/asset_transactions.go`)

UI surfaces never write YAML. Every edit is a semantic `AssetTransaction`
(asset URI set/clear, dependency.manual.add/remove/mode.set,
dependency.inferred.ignore/restore,
column.check.add, column.description.set, column ownership, SQL hook
upsert/remove, and safe inactive-materialization cleanup) POSTed to
`/api/assets/{assetID}/transactions`. The handler read-locks the file (a
per-file lock serializes concurrent read-modify-write — fast editing used to
race and drop content), parses the current definition, applies the
transaction, reconciles against fresh inference, renders the header
deterministically, and writes atomically. One enforcement layer for
ownership, checksums, formatting, and validation.

Deterministic rendering keeps git diffs clean: stable field order, inferred
dependencies in SQL appearance order then manual ones, columns in SELECT-list
order then manual/preserved ones, no timestamps or UI state in committed
metadata (the node-preserving YAML codec in `service/asset_yaml_codec.go`
round-trips unknown fields).

The workspace API preserves Bruin dependency type and mode alongside the
compatibility `upstreams[]` list. A shared resolver keeps bare asset names local
to their pipeline and resolves exact URIs across the workspace. The guided and
YAML-shaped editors use the same creatable combobox. It accepts free text and
workspace assets directly, writes a bare name for the current pipeline, and
substitutes the producer URI for sibling-pipeline choices. Sibling assets
without a URI remain selectable but surface a warning that the name cannot
resolve across pipelines. Duplicate URIs remain editable while surfacing an
asset-addressed error. A manual row owns its full/symbolic mode, which can be
changed atomically after the dependency is added.

## 6. UI (`web/components/app/`)

- **Asset creation:** the Build view's creation dialog presents SQL, Python,
  HTTP API, Seed, Sensor, and Load as equal-size intent choices. The second axis
  is always a backend-profiled connection role rather than another platform or
  SQL-dialect selector: SQL/Python/Seed use a target, API uses a destination,
  Sensor uses a connection to check, and Load has source plus destination. The
  selected connection determines the concrete asset type and SQL dialect on the
  server; those implementation details are not repeated as a second summary in
  the dialog. A pipeline-default option is selectable only when the backend
  resolves one compatible connection; ambiguous, missing, and incompatible
  defaults explain why they are unavailable. Partially supported engines stay
  out of ordinary creation.

  Each picker can open a role-filtered, environment-locked New connection
  dialog without unmounting the asset draft. It reuses the project-settings
  connection form and Verify action, refreshes the creation profile after save,
  then selects the new connection. Full connection management remains an
  explicit navigation to Project settings. The creation dialog describes the
  resulting Renart asset without exposing runtime-engine terminology. HTTP API
  creation defaults to a custom OpenAPI starter, requires a spec URL, and writes
  that URL into the saved YAML so the normal editor intelligence is available
  immediately.

  Existing assets use the same profile-backed connection field. Their concrete
  Type is read-only in both the guided inspector and the currently hidden expert
  editor. A go-to action beside the selector opens that connection in the active
  environment's project settings. A same-engine connection change is an ordinary
  metadata update; a cross-engine choice opens a confirmation that names the old
  and new types, then sends one semantic mutation that persists Type and
  connection together.
  The request includes the type the browser reviewed, so a concurrent filesystem
  edit fails with a reload message instead of being overwritten. Unknown or no
  longer compatible hand-authored connections remain visible for repair. Direct
  API attempts to change Type, or to pair the old Type with a different engine,
  are rejected in favor of this reviewed path. When a SQL asset has inferred
  upstream or downstream relations, the migration confirmation names those
  connected assets and warns that the new cross-connection SQL edges will not
  execute; Renart does not silently migrate the rest of the graph.

  Detail forms use the plain `Field` variant so inputs are grouped by spacing
  and labels without a border around every field. Selecting a kind animates the
  tile grid into a compact selected-kind summary with a Change type action,
  leaving more room for long Seed and Sensor forms. Converting an ad-hoc query
  retains its effective connection. Downstream SQL stays on the source asset's
  effective warehouse because pure SQL cannot cross connections; downstream
  Python starts there but may select another compatible target, and a downstream
  Load fixes that warehouse as its source while asking for its destination. An
  incompatible carried value remains visible and must be changed explicitly;
  creation never silently falls back to another dialect. Generated downstream
  Python uses the runner-injected `renart` SDK. Seed
  workspace paths use the shared file picker; the request carries a
  workspace-root-relative selection, while the saved Bruin definition remains
  portable with a path relative to the asset file.
  Seeds can also be pasted as CSV, TSV, JSON, JSON Lines, or plain text. Auto
  detection remains an explicit, overridable format choice; TSV and text are
  normalized to CSV while JSON and JSON Lines keep their native formats.
- **Guided cards** (`asset-guided-cards.tsx`), rendered in the inspector
  sidebar next to the SQL editor: identity, materialization, dependencies
  (inferred / manual / ignored, with ignore/restore/remove actions), a column
  workbench (status markers for inferred/manual/stale/type-overridden,
  checks, descriptions, and direct manual-column creation), custom SQL checks,
  and reconcile prompts. Column rows show a compact name/type/description/key/
  provenance summary and expand into labeled editing controls, keeping large
  schemas scannable without hiding editability. A manually added column records
  its provenance on the Bruin column's own `meta` map and remains present across
  later inference reconciliation. Custom
  checks open in a focused Monaco SQL dialog with named row-count or scalar
  expectations, descriptions, and blocking behavior; add/edit/remove actions
  use semantic asset transactions and keep the ordinary asset file as the only
  source of truth. The dialog uses the canonical SQL LSP for schema-aware
  completion and live diagnostics. A check may query its owning materialized
  asset without triggering a circular-dependency error, and non-SQL assets
  inherit the SQL dialect of their effective target connection. Runtime failure
  evidence is not written back into that asset file. Instead, the state database
  retains only the failed check identity for
  the latest run; a current-content canvas warning can open this card and
  highlight the matching custom or column check without exposing query or error
  text in workspace metadata. SQL assets also expose their ordered pre- and
  post-materialization hooks in focused Monaco SQL dialogs. Hook statements use
  the owning asset's dialect, graph, Jinja context, and the same semantic
  transaction/write path as the rest of the guided editor. Merge editing includes
  column-scoped primary keys, `update_on_merge`, custom `merge_sql`, and a
  column-backed update-key combobox where the active execution path supports
  one. The backend-provided per-asset capability profile drives the available
  modes and their prerequisites, including warehouse-specific SQL differences,
  native versus Sling-backed Python writes, and Load/API's replace, truncate,
  append, and merge subset. SQL `time_interval` exposes its incremental key and
  date/timestamp granularity; warehouse renderers that use table layout metadata
  also expose partition and cluster expressions. Unsupported hand-authored strategies are shown as
  custom values without being reinterpreted; assets with dedicated non-generic
  runtime configuration omit this section. API, Python, and Load assets share
  the same top-level target
  connection control, including an explicit Auto state. The Load editor keeps
  only source fields in `parameters`, derives database destinations from the
  asset name, shows `destination_object` for file/storage targets, and offers a
  go-to-source action when the source resolves to an upstream asset. Load
  creation and editing reuse one free-text stream picker: configured database,
  S3/GCS, and file connections can list existing tables/objects through Sling
  with the selected environment's backend-only credentials, while local paths
  use the workspace file picker. Destination browsing is labeled as existing
  objects and still accepts a new object path. Listings are capped at 500
  entries, and raw Sling output is server-only because a connector may echo
  connection details. Every edit
  flows through the transaction/API write paths; the workspace SSE stream
  refreshes the asset. Seed and non-query sensor runtime parameters use
  dedicated compact, YAML-like editors in the main pane where Monaco normally
  appears, driven by the same backend capability contract as creation: seed
  source, file type, and schema enforcement remain editable, while table/S3-key
  sensors expose their condition, `poke_interval`, and `timeout`. Query sensors
  edit `parameters.query` in the same SQL Monaco editor and intellisense stack as
  SQL assets, with their timing controls in a compact footer. Saves and format
  actions update the YAML parameter through the Go server rather than treating
  the query as the `.asset.yml` file content. The inspector retains their generic
  identity and dependencies without duplicating those runtime controls.
  The internal `renart_seed_file` ownership marker is
  preserved without becoming a guided user setting. Owned local seeds show
  `path`, `file_type`, and `enforce_schema` before one replacement textarea that
  accepts pasted text, dragged files, and file-picker selections. For local
  UTF-8 CSV/JSON/JSONL/NDJSON files up to 256 KiB, that textarea first loads the
  existing content through the path-derived `GET /api/assets/{assetID}/seed-file`
  contract. Remote, runtime-rendered, binary, non-UTF-8, and larger seeds stay
  replaceable without being read into the browser. Replacing the file uses
  `POST /api/assets/{assetID}/seed-file`, updates `path`, `file_type`,
  and the ownership marker in one server-side write, preserves the remaining
  definition, and removes the previous owned sidecar. A file with the same name
  as an unrelated workspace file is rejected instead of overwritten. Every
  replacement passes through a destructive confirmation that names the current
  seed before any upload starts. Pasted data exposes the same format detection
  and override contract as creation. Relation-producing
  assets retain generic columns and checks in the inspector; sensors omit both
  because they gate execution without producing a relation. Asset Type is a
  static identity field; compatible connection options and reviewed migrations
  come from the backend profile instead of client-maintained SQL/seed/sensor or
  Load connection maps.
- **Run-scoped full refresh:** supported table assets expose a Full refresh
  action without mutating their saved strategy. The destructive dialog names
  the selected environment and current execution window; environments with
  `confirm_destructive` require typing the exact environment name. The same
  confirmation is enforced again in the backend for HTTP and CLI callers.
- **Replay-safe backfill:** assets whose backend staleness contract reports
  `backfill_safe` expose a Backfill range action beside full refresh. The dialog
  accepts an exact UTC start/end range and uses the same destructive confirmation;
  the execution service independently rejects missing ranges, multi-asset scopes,
  and materializations whose windows cannot be safely accumulated.
- **Provenance classification client-side** (`lib/asset-provenance.ts`)
  mirrors the flat-key schema for display (source chips: SQL inferred / table
  inferred / live inferred / manual / type owned).
- **Expert YAML mode** (`asset-yaml-editor.tsx`) remains implemented but is not
  currently exposed in the asset metadata inspector. The inspector shows only
  the guided form while the raw mode is being held back from the product UI.
- **Pipeline connection context**: a canvas asset's connection badge opens the
  pipeline settings Connections section. The config response keeps explicit
  `pipeline.yml` overrides separate from read-only defaults Bruin infers from
  asset types, and each resolved connection links to that exact environment
  connection under project settings.
- The backend advertises each asset's available column sources through
  `column_inference_sources`, so schema synchronization is capability-driven
  instead of branching on asset kinds in the inspector. One backend provider
  registry, backed by one asset-kind policy table, owns contract, SQL, API,
  Load, local-Seed, sampled-live, and materialized-table capabilities. The same
  providers serve explicit sync and the no-I/O authoring resolver; asset-specific
  providers only collect normalized observations, while `asset_column_sync.go`
  remains the single owner of precedence, freshness, and collision policy. Each observation
  carries stage, completeness, confidence, environment/connection/relation
  scope, asset revision, output identity, and observation time. Definition sources
  (SQL output, Load upstream, local seed file, or API fields/OpenAPI) are selected
  automatically. External OpenAPI documents are fetched only by an explicit
  network-enabled schema observation; workspace loading, type-check, and LSP
  graph construction never fetch them. Observed sources (a sampled API request and the current
  materialized table) appear as optional advisory checkboxes. Sampled sources
  declare that they may omit columns, so a missing optional API field is not
  mistaken for deletion evidence. Relation/driver metadata remains complete
  even when `SELECT ... WHERE 1 = 0` returns no rows; row-derived API evidence
  remains partial regardless of the sample's row count.
- Bruin's full authored column contract round-trips through the workspace and
  transaction DTOs: `source_column`, `mask`, `default`, precision/scale/length,
  collation, governance fields, checks, and column-local `meta` are preserved.
  Logical type normalization is comparison-only and ephemeral; native Bruin
  fields remain the persisted representation. Alias comparison uses Polyglot's
  data-type parser, but precision, scale, length, nested element types, and
  timezone structure must match losslessly before two evidence sources count as
  the same known type.
- `POST /columns/sync` observes the selected sources and owns the conservative
  merge policy. New columns and an unknown saved type becoming known are applied
  immediately through the provenance-aware reconciler. Deleting a saved column,
  changing any known type, or finding incompatible source observations returns a
  non-mutating merge model instead. The inspector opens one scrollable resolver
  table with a column per source, saved metadata, and the selected result. Every
  cell repeats `column:type`; unknown values use `column:?` and absent values use
  a struck-through `column:------` so rows remain understandable without tracing
  back to the first column. The initial resolution is evidence-aware: a known
  definition change is preferred over stale saved metadata, while a complete,
  fresh materialized observation is preferred when otherwise comparable
  sources disagree. The user must still explicitly apply every conflict;
  `POST /columns/sync/apply` persists those choices atomically while retaining
  descriptions, checks, manual columns, ignored columns, and type ownership.
  Selecting an observed source keeps the field generated and records its compact
  source code instead of claiming manual ownership. Later ordinary syncs
  re-observe only those recorded exception sources. If SQL can infer only an
  unknown type, a known table/live-derived saved type is retained without a
  conflict. A materialized observation's freshness is evaluated at sync time and
  is never persisted: a stale observation is classified as historical and
  excluded before it can collide with the current definition. Differently scoped
  environments, connections, revisions, and output identities are excluded for
  the same reason; the response and notes explain every exclusion. An
  unverifiable observation remains advisory and conservative. When the selected
  configuration can prove a physical output, `OutputIdentity` is the existing
  secret-free physical-target digest used by execution and staleness. Connection
  aliases are never used as a substitute identity; unsupported/runtime-only
  targets leave it unknown. Schema evidence is not retained in a second history
  store: current sync snapshots plus existing bounded materialization facts are
  the explanation boundary.
  Current-table observations for Load/API assets ignore the legacy Sling
  `_sling_loaded_at` column. DuckDB observations use logical catalog types, so a
  stored `JSON` column is not presented as `VARCHAR` merely because of the query
  result transport.
  Editing SQL or API source does not implicitly rewrite column metadata; users
  choose when to run **Sync schema**, so an autosave cannot invalidate an
  already-open run or deployment review.
  Local Seed definition inspection is content-fingerprinted, bounded in memory,
  and single-flight: concurrent requests for the same bytes share one Sling
  process, unchanged content reuses the result, and a content change creates a
  new observation. Type-check and both LSP transports never launch Sling; a
  local or remote Seed without committed columns therefore remains a
  missing-declaration finding until an explicit import persists the schema.
  Sensors expose no schema source. `/columns/preview`, `/columns/reconcile`,
  `/columns/refresh-from-definition`, and SQL-specific `/fill-columns-from-db`
  remain compatibility routes; automatic seed replacement refreshes still use
  the definition reconciler.
- **Type-check resolutions:** findings may carry backend-authored semantic
  resolutions instead of asking the browser to infer a YAML edit. The shipped
  destructive resolutions remove inactive `partition_by`, `cluster_by`, or
  column merge-only metadata after an explicit confirmation, apply through the
  asset transaction endpoint, and immediately rerun the report. Findings
  without a proven safe edit remain explanatory only. The distinct external
  relation resolution is a typed server action rather than an asset transaction:
  it opens a preview of the native single-table source-asset import, defaults to
  persisting observed columns, and writes only after confirmation.

## 7. Not built (still intent, from the original concept)

- **Draft persistence layer** (browser/IndexedDB journal recovering unsaved
  typing across reloads). Canonical autosave exists; the volatile draft
  journal does not.
- **Raw / detached mode** — granular "renart stops managing this field/asset"
  detachment. Column `meta.renart_owned` covers field-level type ownership; whole-path
  detachment is not implemented.
- **Broader semantic diff prompts** outside schema-source synchronization;
  the schema resolver shows source and saved-metadata drift, while other
  generated changes still rely on safe auto-apply plus reconcile items for
  conflicts.
- **Command palette metadata actions** beyond what the cards expose.
- Expression-hash rename memory (`renart_col_map`) is defined in the schema
  but rename suggestions are not yet surfaced in the UI.

The full original design exploration (UI sketches, autosave matrix) lives in
git history: `architecture/renart-asset-editing-concept.md` before this file
replaced it.
