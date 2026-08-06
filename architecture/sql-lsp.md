# SQL language server — current architecture

Status: current state (August 2026).
A Go implementation in `internal/sqllsp` serving two frontends: a stdio
JSON-RPC LSP server (`renart debug sql-lsp`) for external editors, and HTTP
endpoints (`/api/sql/lsp/*`) consumed by the web UI's Monaco editors —
including notebook cells.

The implementation is Go around an embedded parser-backed Polyglot SQL WASM
engine. A tolerant Go analyzer remains additive for incomplete SQL and for LSP
features such as navigation, completion, and code actions.

## 1. Layering

```
Graph/provider sources
  web WorkspaceState                  filesystem Bruin/dbt loaders
        ↓                                      ↓
Canonical graph + schema confidence + saved asset diagnostics
        ↓
shared SQL validator (internal/sqlintelligence, embedded Polyglot WASM)
        + tolerant/policy analyzer (internal/sqllsp)
        ↓                                      ↓
HTTP/Monaco adapter                     stdio JSON-RPC adapter
```

- **Canonical graph** (`types.go`): `CanonicalGraph{Assets, Relations,
  Schemas, …}` with per-node `Provenance`. Declared schema columns retain
  nullability, primary-key, and foreign-key metadata; inferred columns never
  manufacture constraints. Renart/Bruin/dbt are provenance, not core concepts
  — the engine never branches on provider.
- **Diagnostic contract** (`internal/authoringdiag`): stable codes, source,
  severity, byte offsets, scope, and confidence are shared by pipeline
  type-check, HTTP diagnostics, and stdio diagnostics. A registry classifies
  every type-check code as document, asset/header, or pipeline-only delivery.
- **Shared semantic validator** (`internal/sqlintelligence`): validates the
  unsaved document against the canonical schema with the embedded Polyglot
  WASM runtime. Type-check calls the same validator for rendered SQL. Strict
  identifier, expression-type, and reference checks are enabled; declared
  column constraints are sent in the schema payload. Unknown schema confidence
  suppresses only findings that depend on that relation. Heuristic join-quality
  warnings remain lint-policy candidates rather than default type errors.
- **Engine** (`analyzer.go`): stateless over an immutable graph plus an
  in-memory index. Its tolerant analyzer remains the fallback when semantic
  validation cannot run and supplies additive LSP policy checks such as
  circular and cross-connection references. Overlapping findings deduplicate
  by stable code and range.
- **Native Polyglot FFI** (`polyglot_ffi.go`) is an optional syntax fallback;
  it is not required for schema-aware diagnostics or offline operation.
- **Templates**: the HTTP/Monaco adapter resolves the owning pipeline and asset,
  renders the unsaved document with the same variable defaults, macros,
  platform built-ins, and `this` value as preview/type-check plus a
  schedule-derived preview window, then attaches a request-local
  `ProjectRenderedSQL` source map. Polyglot and every tolerant LSP feature
  therefore analyze rendered SQL while ranges map back to the template buffer.
  If an in-progress template cannot render, the core engine falls back to its
  length-aware `{{ ref(...) }}` / `{{ source(...) }}` expansion rather than
  disabling the LSP. The filesystem-only stdio adapter uses that lightweight
  expansion because it has no web workspace resolver. Rename refuses templated
  documents with `ErrRenameTemplated` — edits against rendered SQL cannot be
  mapped back safely — and both frontends surface the reason (LSP
  `RequestFailed`, Monaco `rejectReason`).
  SQL symbol definitions continue through the LSP. Pipeline variables are the
  deliberate exception: the shared Jinja Monaco provider uses the renderer's
  variable metadata and a custom editor URI so the standard go-to-definition
  gesture opens the guided pipeline Variables settings and highlights
  `var.name`; an LSP file location would incorrectly bypass that UI target.

## 2. Web service: state → graph, caching

`SQLLSPService` (`internal/web/service/sql_lsp.go`) builds the graph from the
coordinator's `WorkspaceState` rather than the filesystem:

- Every pipeline asset becomes an asset node + relation; declared
  `model.Column`s become a `declared` schema layer, including nullable,
  primary-key, and foreign-key metadata. The provider-backed authoring resolver
  also supplies pure API response-field and Load passthrough declarations.
- Query sensors are SQL documents even though their definition path ends in
  `.asset.yml`: the HTTP/LSP adapter and pipeline type-check both project
  `parameters.query`, assign the dialect from the sensor provider, and validate
  or search references against that SQL rather than the YAML definition.
- Custom assertion queries and asset pre/post hooks are explicit
  `custom_check` and `hook` document contexts. They use the owning asset's
  canonical graph and effective target-connection dialect, including for
  non-SQL assets where custom checks are supported. Reading the owning
  materialized relation is valid, so circular-self-reference and saved
  asset-body diagnostics are suppressed only for these metadata-query
  contexts.
- The graph is **cached by `WorkspaceState.Revision`** (monotonic, bumped on
  every mutation). Editing issues LSP requests per keystroke against the same
  saved state, so rebuilding per request was wasted work. `Revision == 0`
  (unmanaged/initial state) is never cached.
- The HTTP adapter has an optional process-local **remote catalog cache** keyed
  by connection and environment. Every request reads its snapshot without I/O,
  schedules a single-flight background refresh when it is cold or older than
  60 seconds, and overlays positively observed relations onto fresh relation
  and schema slices. Refreshes time out after five seconds and are bounded to
  32 databases, 2,000 relations, and 512 columns per relation; a failed refresh
  retains the previous stale snapshot, and failed/partial catalog or column
  discovery is retry-limited for ten seconds so editor requests cannot fan out
  warehouse work. Known columns participate in the same
  completion, hover, and semantic validation paths as authored schemas, while
  an observed relation whose columns have not been fetched remains resolvable
  without enabling unknown-column checks. Authored relations win exact-name
  collisions, remote completions rank below authored relations, and ambiguous
  remote short names require qualification. The stdio LSP supplies no provider
  and remains deterministic/offline. Connection secrets never enter the graph.
- Asset/header rules shared with type-check (dependency existence,
  materialization metadata, missing output declarations, render failures, and
  resilient asset-parse failures) run once per `(revision, pipeline)` through
  `CheckPipelineAssetFindings`. This pass does not rerun semantic SQL. Only the
  requested asset's findings are merged into its editor result, at the
  document header when no honest metadata range exists.
- Per-edit diagnostics always validate the current unsaved content against the
  saved revision snapshot. For Jinja documents the request additionally carries
  a source-mapped rendering made from the saved pipeline context, so raw
  delimiters never reach Polyglot and completion/hover/navigation use the same
  generated SQL. The browser aborts superseded requests and checks the Monaco
  model version and content before installing markers, so response N cannot
  overwrite markers for N+1.
- DuckDB documents get a request-local graph layer for direct local-file
  relations such as `"./data.parquet"`. Renart resolves relative paths from the
  workspace, asks DuckDB for the zero-row result schema, and caches columns by
  file size and modification time. A missing or temporarily invalid file keeps
  valid DuckDB relation syntax without becoming an unknown-table error.
- The optional native Polyglot client is shared and loaded lazily; requests
  never wait for a native download before publishing embedded-WASM results.

## 3. Notebook cells

Notebook cells (`state.Notebooks[].Cells`) are LSP targets like pipeline
assets, but their visibility mirrors the per-notebook DuckDB session
(notebooks.md §2):

- `selectedAsset` resolves cell IDs and returns the containing notebook.
- `graphForRequest` extends the cached pipeline graph **per request** with
  that notebook's cells (fresh slices — the cached graph is never mutated):
  sibling cells become relations with declared or inferred columns, and each
  cell's `ExternalRefs` (warehouse tables that are neither cells nor pipeline
  assets) become *bare* relations — they resolve without claiming columns, so
  reading a raw table is never an `unresolved-relation` error and its columns
  are never validated.
- Scoping is strict both ways: cells of other notebooks stay unresolved, and
  pipeline-asset requests never see notebook cells.
- References from a cell also search sibling cell documents.

In the editor (`notebook-cell-editor.tsx`), SQL cells use `useSQLLSP` for the
complete provider surface: diagnostics, completion, decorations, hover,
rename, and navigation. The hook supplements an LSP completion response only
with ephemeral columns from a sibling's last notebook run, which the backend
intentionally cannot see. The older schema-wide provider is not registered for
notebook SQL models, so derived `VALUES`, `DESCRIBE`, CTE, and subquery scopes
cannot be polluted by unrelated workspace columns.
The Build ad-hoc editor also uses this LSP path instead of enabling the older
global parse-context completion provider, so asset, query-sensor, and ad-hoc SQL
agree on derived query semantics. The selected pipeline asset is borrowed for
graph and Jinja scope, while the independently selected query connection is
sent on every LSP and parse-context request. The Go service clones the cached
graph and derives the document dialect from that connection's backend-owned
query asset mapping; formatting and DuckDB filesystem enrichment use the same
overridden dialect. Ad-hoc requests do not attach asset/header diagnostics, and
a reference to the context asset is not treated as the asset circularly
referencing itself.

The custom-check and pre/post-hook dialogs use the same hook for completion and
diagnostics. Their Monaco models are independent from the asset body, so
markers point at the assertion or hook SQL itself. Pipeline type-check renders
every saved custom check with the asset's Jinja context and validates it
against the same schema snapshot. CLI findings repeat the check name but remain
range-less because a line in an embedded metadata block is not the same
coordinate space as the asset SQL body.

Python assets and Python notebook cells project static SQL passed as the first
argument to `query("...")` or `renart.query("...")` through
`use-python-query-intellisense.ts`. The host document stays Python. A small
literal scanner decodes ordinary, raw, and triple-quoted strings and keeps a
UTF-16 source map, then the adapter translates completion, diagnostics, hover,
definition/navigation, signature-help, and semantic-token positions to and
from the existing SQL LSP. Interpolated strings, bytes, variables,
concatenation, and other runtime expressions remain Python-only because they do
not represent one stable SQL document. A static connection supplied as the
second positional argument or `connection="..."` keyword is projected too. The
Go service clones the cached canonical graph for that request and replaces only
the host document's effective connection, so cross-connection diagnostics match
the query's runtime target without mutating workspace state. Dynamic connection
expressions fall back to the Python asset's saved graph identity. Notebook
queries still see only sibling cells and the same pipeline relations as SQL
cells.

Completion has two inputs, matching native notebook SQL cells: canonical graph
suggestions from the LSP and the editor's `schemaTables` context. The latter
adds schemas discovered by the client and a sibling cell's last successful run
columns, which are intentionally ephemeral and therefore absent from
`WorkspaceState`. LSP suggestions win when both sources describe the same
item. Arbitrary Python output consequently gains column completion after that
cell has run, without moving runtime state into the canonical graph.
Completion is also triggered at SQL separators inside the string (including a
space after `SELECT *,`) so an empty column prefix offers the same schema-aware
list as typing its first letter.

The adapter also projects Monaco's SQL lexical tokens into decorations inside
the Python string, then overlays LSP semantic relation tokens when they arrive.
This keeps SQL syntax highlighting immediate for both closed strings and the
unfinished plain literals produced while a user is typing.

## 4. Shared schema inference: AST first, tolerant fallback

`InferSchemaSnapshot` is the one output-schema pipeline used by type-check and
both LSP graph adapters. Declared/provider columns are applied first. For an
undeclared SQL relation, Polyglot AST/type annotation is the fast path for
explicit projections. If projection names are incomplete (most importantly,
`SELECT *`), a bounded compact-analysis cache keyed by SQL, normalized dialect,
and deterministic schema payload supplies scope-aware expansion through joins
and CTEs. The tolerant Go projection analyzer is the final fallback for
incomplete or unsupported SQL. Every schema layer records completeness and
confidence explicitly; a partial layer contributes known columns but does not
make the relation's full schema authoritative for unresolved-column checks.
For DuckDB table functions, the AST fast path also reconciles direct integer
arithmetic against known operand widths. This corrects narrow literal-led
annotations such as `range * 2` from `INTEGER` to the `BIGINT` produced by
DuckDB's `range()` relation without overriding unresolved or non-integer
expressions.

Non-SQL definition schemas enter that same snapshot through the schema-evidence
provider registry and its central asset-kind policy. Explicit columns are
authoritative contract evidence; local HTTP response fields and Load assets
that mirror an upstream declaration are automatic pure providers. SQL inference
and Load passthrough participate in one bounded feedback fixpoint, so a chain
such as SQL -> Load -> SQL has the same columns in type-check, HTTP LSP, and
filesystem/stdio LSP. The schema-provider request policy used by all three
consumers disallows network, warehouse, file inspection, remote-file, and
user-code access (DuckDB's separately gated local-file relation enrichment
remains the explicit LSP exception described above).
In particular, an external OpenAPI URL is not fetched while loading a workspace
or typing; users explicitly import that schema first. Local Seed inspection is
also explicit and content-fingerprint/single-flight cached, never launched by an
authoring request. Runtime/materialized evidence therefore cannot make the
revision-cached authoring graph nondeterministic. Any relation-producing asset whose committed definition has
neither an explicit schema nor a supported derivation is an asset-level error
in type-check, HTTP diagnostics, and the stdio LSP. This includes persisted
Seed and Python outputs: runtime sampling is deliberately not used to make a
revision-cached authoring graph nondeterministic.

Declared SQL schemas are also an output contract. On the final executable
query, the shared validator infers the projection with Polyglot. When every
output name is explicit, it compares the declared and inferred name sets and
emits `declared-output-schema-drift` for missing or undeclared columns; name-only
declarations participate in this check. The annotated AST remains the fast
path. Compact analysis runs only when names are incomplete, a declared type has
no fast-path inferred type, or the asset declares a `nullable: false` contract.
It fills transitive types through nested CTEs and can expand stars when every
physical source has a complete schema. `SELECT *` name-set comparisons over a
partial graph schema stay silent.

Same-name columns are compared by type. Polyglot's standalone data-type parser
canonicalizes dialect spellings (`INT`/`INTEGER`, `TEXT`/`VARCHAR`,
`NUMERIC`/`DECIMAL`, timestamp timezone spellings, and parameterized types), so
only meaningful differences produce `declared-column-type-drift` warnings.
Schema-evidence reconciliation uses the strict form of that comparison:
precision, scale, string length, nested element structure, and timezone
structure cannot be discarded as aliases. Native Bruin type fields are retained
for display and round-tripping; the logical form is not persisted as parallel
metadata.
Compact analysis also carries schema nullability through expressions, CTEs,
and outer joins. If a projection is provably nullable while its declared
contract is `nullable: false`, the validator emits
`declared-column-nullability-drift`. Unknown nullability and the safe inverse
(a non-null expression written to a nullable column) stay silent. All drift
checks run against the current unsaved LSP document as well as rendered
type-check SQL; because declaration metadata has no honest SQL token range,
the warnings are asset-scoped at the document header.

Inference runs a **topologically ordered fixpoint** (capped at five rounds):

1. Order the undeclared assets upstream-first (`topoOrderInferenceAssets`,
   Kahn's algorithm over the declared upstreams; cyclic leftovers keep their
   original order).
2. Infer each asset against the current graph, applying every result and its
   completeness immediately so later assets in the same round see it.
3. If any result changed, rebuild `inferred-ast` / `inferred-tolerant` layers
   and repeat.
4. Stop when a round changes nothing or the cap is hit.

Derived-table inference is deliberately syntax-tolerant. Parenthesized
`SELECT`, `WITH`, `VALUES`, and DuckDB `DESCRIBE` bodies are scoped without
requiring a complete parser AST. An explicit derived-table column list such as
`n(a, b)` overrides the body's inferred names, including for `VALUES` rows.
`DESCRIBE` exposes its result relation (`column_name`, `column_type`, `null`,
`key`, `default`, `extra`) rather than leaking the described table's columns.
Monaco asks the LSP for local alias columns before falling back to live
warehouse discovery, preserving that distinction for qualified aliases too.

With truthful edges, step 2 walks a DAG in one round regardless of chain
depth; round two confirms stability. The fixpoint loop stays as the
correctness net for when the edges lie.

Design notes, in decreasing order of importance:

- **The edges are cheap and reliable**: pipeline `depends:` is auto-reconciled
  on every asset save (`reconcileSQLAssetDependencies` uses the shared embedded
  Polyglot WASM table scan and persists the result; async retry while mid-edit
  SQL doesn't parse), and notebook cell upstreams are derived at load time by
  the same used-tables policy (in memory only — cell files are never rewritten).
  Both arrive in `model.Asset.Upstreams`, name-keyed like the graph's
  relations, so ordering needs no extra SQL parsing. This is why topo ordering
  is worth having *inside* the fixpoint but not as a replacement: on its own
  it would inherit every staleness window of those edges.
- **Every round re-infers all undeclared assets**, not just still-empty ones:
  partial results can grow (`select a.x, * from …` yields `x` before
  the `*` resolves).
- **Determinism**: the result is the least fixpoint, independent of asset
  iteration order — ordering affects how fast it converges, never what it
  converges to. (The original code rebuilt the engine per asset inside the
  loop with no outer loop, so chaining depended on iteration order and the
  whole thing was O(N²).)
- **Cycles** converge naturally — a cycle simply stops producing new columns —
  so no cycle detection is needed; Kahn's leftovers just run last.
- **Failure mode is benign**: only when edges are missing/stale *and* the
  chain is deeper than the cap do columns go missing. An empty column set
  suppresses unknown-column diagnostics, so the worst case is "no
  completions", never false errors.
- **Cost placement**: pipeline inference runs in the revision-cached graph
  build. Notebook augmentation runs over that notebook's cells only.

## 5. stdio server

`renart debug sql-lsp` (`cmd/sqllsp.go`) uses `LoadSQLLSPGraph`: the canonical
filesystem loader indexes Bruin and dbt projects, then the Bruin adapter adds
the same cached-style asset/header findings and provider-backed pure declaration
fixpoint used by the web service. The
configured loader is reused on `workspace/didChangeWatchedFiles`, so reloads do
not silently lose metadata diagnostics. A missing graph degrades to local
syntax/tolerant analysis. Message size is capped at 64 MiB.
The stdio command and web server both expose
`--enable-filesystem-access` (default `true`). When disabled, the LSP does not
open local files and replaces unknown-table noise with the stable
`duckdb-filesystem-access-disabled` diagnostic on each file relation.

## 6. Completion & diagnostic surface (web editor)

The app's Monaco asset editors (`web/components/app/asset-editor.tsx`), the
query-sensor editor, custom-check dialog, and pre/post-hook dialogs drive SQL
intellisense **entirely through the LSP**
(`web/hooks/use-sql-lsp.ts`); the older client-side parse-context providers are
deliberately disabled, so the LSP is the single source of truth. Query sensors
use an in-memory `.sql` Monaco model while persisting edits and formatting back
to `parameters.query`, never to the raw YAML content.

- **Completions** by context: column fields (after an alias `.`), relations —
  workspace assets and, in a `from schema.` position,
  `relationCompletionsInSchema` returns schema-stripped inserts — and clause
  **keywords** (`keywordCompletions`, sorted last via a `z` SortText so
  schema-aware items win). In a `JOIN ... ON` condition, the engine offers only
  relations visible in that query scope, rendered as field items such as `x.*`;
  it inserts the effective alias plus `.` and Monaco immediately opens the
  corresponding column suggestions. It never substitutes the connection's full
  relation list or the underlying qualified name when an alias exists. The
  client keeps only kinds it renders (columns, relations, keywords). Column,
  relation, and keyword completion is suppressed while the cursor is inside a
  single-quoted SQL string; explicit path and data-value suggestion flows retain
  their narrower behavior.
  Purely-remote warehouse tables (no backing asset) come from the optional
  backend catalog overlay. The browser temporarily retains its older live
  discovery fallback for a cold first request; warm relation and column results
  are served by the LSP and deduplicate ahead of that fallback. External canvas
  nodes and source-asset import remain in
  `plans/remote-table-intellisense.md`.
- **Diagnostics**: unresolved relation / alias / column (column checks only fire
  when the relation's columns are known from asset SQL or declared metadata),
  ambiguous unqualified columns in multi-relation scopes, Polyglot expression
  type incompatibilities, declared-versus-inferred output name, type, and
  unsafe nullability drift warnings,
  **circular self-reference** (a used relation that resolves to the current
  asset), **cross-connection asset references** (warning when both assets have
  known, different effective connection names), template-projected diagnostics,
  and parser syntax errors. Qualified and unqualified column resolution comes
  from the same semantic validator as pipeline type-check. Saved asset/header
  findings are merged without manufacturing SQL-token ranges. Static Python
  `query()` dependency declarations are delivered by the Python editor adapter,
  which owns the correct host-language range. Inspect-error markers remain a
  separate execution concern.
- **Monaco gotcha**: the completion registry is shared across languages, so the
  python/yaml completion providers must register once (keyed on `asset?.id`,
  live state via a ref) — re-registering them on a workspace/SSE update
  re-triggers any open SQL suggestion widget and drops its selection.

## 7. Embedded Polyglot runtime lifetime

The semantic core and formatter use the exact WASM artifact shipped by
`@polyglot-sql/sdk` 0.6.2. `web/scripts/sync-polyglot-wasm.mjs --check` fails CI
when the checked-in artifact differs from the pinned package, and a runtime
test verifies the module's exported version.

Schema validation enables Polyglot's opt-in expression type checks. Syntax
diagnostics consume the structured byte offsets returned by the WASM response;
the older line/column text parser remains only as compatibility fallback.
Standalone data-type parses are cached by dialect and normalized type text so
live drift checks do not repeatedly cross the WASM boundary for common types.
Compact query analysis is likewise cached by SQL, normalized dialect, and the
deterministic schema-plus-constraint payload. It is not added to ordinary
explicit-projection edits unless the faster inference is missing a fact needed
by a declared output contract.

Wazero uses the optimizing compiler plus an on-disk compilation cache, with an
interpreter only as a cold-start bridge. Module instances live in a bounded
channel pool (at most four), each has a 256 MiB hard memory limit, and an
instance is discarded only if a call leaves more than 16 MiB of extra linear
memory retained. Healthy modules are reused indefinitely instead of being
re-created after a call count. Cancellation closes an executing module; closed
modules are never returned to the pool. The interpreter is retired once the
compiled engine is ready.
