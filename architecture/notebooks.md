# Notebooks — current architecture

Status: current state (built on the `redesign` branch, July 2026). Cells are
ordinary Bruin assets in a notebook namespace; results live in an ephemeral
per-notebook DuckDB session; recompute is server-driven.

## 1. File format and identity

Notebook-as-folder; everything travels through git:

```
notebooks/
  revenue-exploration/
    notebook.yml          # notebook id, title, ordered blocks (cell | markdown)
    clean_sales.sql       # ordinary Bruin asset file; frontmatter id: + class
    by_month.sql
```

- Filename = cell name; the frontmatter `id:` is authoritative and survives
  renames (the manifest references cells by id, so rename = rewrite sibling
  files + move the cell file, no manifest edit).
- Newly created cells receive concise two-word identifier names such as
  `quiet_river`. Generation starts at a cryptographically random pair, checks
  sibling and pipeline-asset collisions, and has an exhaustive suffix fallback;
  explicit names and all existing notebook files remain unchanged. Short logical
  names keep sibling relations legible in completion lists while physical object
  identity remains the durable cell id.
- Prose blocks live in `notebook.yml`; they are not assets and have no
  fingerprints.
- Cells may be SQL (`.sql`) or Python (`.py`). Python cells participate in the
  dependency DAG and manual runs but stay excluded from auto-recompute while
  Python fingerprint hardening remains open (staleness.md §8).
- Loading: `service/notebooks.go` folds notebooks into `ComputeState`; every
  asset carries `class: pipeline | notebook`.

## 2. Core invariants (and how each is enforced)

1. **No logical name ever enters a fingerprint.** `CellFingerprint` resolves
   sibling names → `cell_<id>` via the identifier splice (never the SQL
   parser's `RenameTables`, which fails on Jinja and re-prints the statement),
   then canonicalizes. Rename is therefore free: zero fingerprints change.
   Guarded permanently by `TestRenameChangesNoFingerprint` (5 referencing
   cells incl. Jinja, a string literal, a self-join, a comment mention).
2. **Class is first-class; dependency direction is policed.**
   `validateAssetClassDirection` makes a pipeline asset depending on a
   notebook cell a state-level *error*. Catalog/lineage read
   `state.Pipelines` only, so cells never leak into production surfaces.
3. **Presentation lives in comments, outside the fingerprint.** `@viz`
   directives are comment lines; canonicalization strips them
   (`TestVizIsOutsideFingerprint`). Any directive that *should* affect
   execution semantics must go into asset config instead — that rule lives in
   the directive parser's doc comment.
4. **Physical objects are machine-named.** Cells materialize as `cell_<id>`,
   imports as `src_<sanitized ref>`, inside
   `.renart/notebooks/<uuid>.duckdb`. Logical names exist only in the editor.

## 3. Sessions, imports, cleanup (`notebook/session.go`, `run.go`)

- One `.duckdb` file per notebook UUID, serialized by a per-UUID in-process
  mutex. Cells materialize as views by default; `@materialize(table)` pins a
  table (Python cells always materialize tables).
- Session statements use a narrow ADBC adapter that retains each native
  statement handle and bridges Go context cancellation to the thread-safe ADBC
  `AdbcStatementCancel` operation. This is necessary because the upstream Go
  driver-manager execution wrapper does not itself propagate `ExecuteQuery`'s
  context into the C call; cancelling only the HTTP request would otherwise
  leave a DuckDB query and the serialized notebook session running.
- **Import resolver:** a cell referencing a pipeline asset gets the data
  brought into the session. Fast path for DuckDB-backed assets is a zero-copy
  batched `ATTACH; CTAS; DETACH` (ATTACH visibility is per-connection, so it's
  one batch); everything else falls back to a row-capped generic `Fetch`
  through the connection. That named-connection fetch runs through the shared
  operation-scoped connection factory with the `notebook_query` secret purpose;
  provider values remain in Go and never enter the notebook file, browser, or
  Python process. `SourceFetcher` is the swappable seam for a future cloud
  gateway. Unknown refs (`ErrUnknownSource`) are left untouched so the session
  yields a clear missing-table error. Provenance is tracked in a
  `__renart_imports` table inside each session DB.
- **Cleanup = delete the file.** Close-notebook and delete-notebook remove the
  session file; startup `SweepSessions` removes files whose notebook no longer
  exists (covers kill -9). No warehouse objects, no janitor edge cases.
  Protected environments fall out for free: a notebook reads prod via the
  import resolver and writes only the local file.
- Cell runs do **not** emit facts into matlog; staleness/results are runtime
  state (see §6), honest for the ephemeral per-session model.
- Python cells query the already-open live session through their token-scoped
  Renart SDK broker. The runner rewrites logical sibling names to `cell_<id>`
  before executing the read-only query on the in-process session handle; no
  database path or credential crosses into Python, and no upstream snapshot is
  copied. `materialize()` stages one Parquet file which the runner loads
  directly into the same session with `read_parquet`.
- The embedded SDK wheel supplies `renart`, pandas, and PyArrow. A notebook with
  no additional packages runs without creating a `pyproject.toml`; the
  Dependencies surface creates one only when the user adds packages. Python is
  still a fresh process per cell run. SDK queries return PyArrow Tables by
  default; pandas conversion is explicit through `.to_pandas()` or
  `format="pandas"`. The verified uv path is cached in the Go process, while
  `uv run` remains responsible for locking and syncing explicit project
  dependencies.

## 4. Rename engine (`notebook/rename.go`)

A hand-written identifier-splice tokenizer (not the parser's `RenameTables`,
which would uppercase keywords and destroy user formatting) walks
code/string/comment/quoted-identifier/Jinja states and replaces only bare or
double-quoted identifier tokens: `'base'`, `-- base`, and `{{ base }}` are
left alone while `from base` and `base.id` are rewritten; a name preceded by
`.` (`schema.base`) is skipped. The same splice resolves names for the
fingerprint (it never fails on templated SQL). Validation before applying:
identifier charset, collisions against sibling cells, pipeline asset names,
and reserved words.

## 5. Viz directives (`notebook/viz.go`, `notebook-viz*.tsx`)

`-- @viz(kind, key: value, …)` with kinds `table | bar | line | area | pie |
kpi`, parsed by a real tiny parser producing a typed config or a
span-carrying diagnostic. First directive wins; duplicates warn. The Recharts
renderer row-caps per kind and degrades gracefully on missing columns. The
chart settings popover parses and rewrites the directive line — text is the
single source of truth. `@viz` is the first member of a general
`-- @word(args)` comment-directive family (`@materialize` is another); all
directives are comments and therefore outside fingerprints by construction.
The `@viz` syntax and behavior are explicitly experimental and the user docs
warn that they are likely to change.

Cell code is edited in Monaco. Its initial height follows the cell's content;
each cell also has an independent vertical resize handle with pointer and
keyboard controls. Resizing is presentation-only and stays in frontend state.

`buildNotebookSchemaTables` supplies sibling relations to both SQL cells and
plain SQL string literals inside Python `query(...)` calls. Native SQL cells
register only the canonical LSP provider; the provider merges the narrow set of
ephemeral `notebook-run` columns into the LSP response rather than registering
the older schema-wide completion provider beside it. A sibling's last
successful run columns take precedence over declared columns, so outputs that
cannot be inferred statically (including arbitrary Python materializations)
become available to column completion after a run. The Python adapter keeps the
Monaco document in Python mode, projects completion ranges into the embedded
SQL string, and renders SQL lexical plus semantic decorations there. Notebook
result DTOs remove Sling's transport-only `_sling_loaded_at` column (and the
corresponding row value) before results reach the table, visualizations, or
runtime completion schema.

Build's ad-hoc query editor can copy its current SQL draft into either an
existing notebook or a newly created one. Existing notebooks receive a new SQL
cell; a new notebook's seeded example cell is renamed and replaced so the
conversion leaves one intentional cell rather than an unrelated starter. The
draft remains available in Build after the filesystem mutation.

## 6. Server-driven auto-recompute (`service/notebook_autorecompute.go`)

The server owns staleness and recompute; the client owns only "what the user
is typing" (a typing→save debounce) and rendering.

- Per-notebook in-memory `notebookRuntime` (stale set, last results,
  `autoFailed` memory, the auto-recompute toggle, import environment) held on
  `NotebookService`. Lost on restart, by design.
- `UpdateCell` marks the cell + descendants stale and arms a 200 ms debounce;
  the pass runs wave by wave against the session's *real* schemas,
  re-validating between waves (validation is `ParseContextService.Parse`
  injected as the `ValidateSQL` dep — identical semantics to what the client
  used to request). A new edit ctx-cancels an in-flight wave. Stop
  (`POST …/cancel`) cancels both manual and automatic work and does not return
  until each run has unwound and released the serialized notebook session;
  the client therefore cannot race a new run against the query it just stopped.
  A debounce that fires while a pass is active records a pending wake-up; the
  pass consumes it before parking, so a valid edit cannot be lost behind an
  older invalid-SQL pass. Manual `Run` folds results into the runtime and can
  unblock downstreams.
- Transport: a single `notebook.runtime` SSE event
  (stale / auto_pending / running / results-delta) tagged with the notebook
  id, via `PublishImmediate`. Endpoints: `GET …/runtime` (seed snapshot),
  `PUT …/settings` (toggle + environment), `POST …/cancel`. The app-shell
  event reducer accumulates result deltas per notebook, so a following
  state-only event cannot erase a completed result before React renders it.
- Optimistic staleness: on edit the server publishes stale cells as
  auto_pending up front so the hatch doesn't flash, then demotes any that
  won't actually refresh (Python, non-SELECT, errors).
- Cell saves are ordered and revision-checked. Each cell DTO carries a
  content-derived `content_revision` for the exact file snapshot. The client
  keeps one full-document save queue per cell, sends only one `PUT` at a time,
  and uses the last acknowledged revision as the next request's
  `base_revision`. The service serializes the compare-and-write section per
  cell and returns `409 cell_edit_conflict` when that snapshot is stale. A
  delayed response therefore cannot replace newer typing, while another Renart
  tab or revision-aware API client cannot silently overwrite a newer save.
  Unsaved drafts remain in Monaco on conflict. After the revision check, a
  normalized payload identical to the current file is a no-op; focus/blur save
  races therefore do not rewrite the file or mark the dependency closure stale.
- This is snapshot concurrency control, not collaborative text merging. The
  acknowledged snapshot is the boundary where a future OT/CRDT adapter can
  exchange operations; until then, conflicts are explicit and the filesystem
  remains authoritative.
- Eligibility logic (`computeAutoRecomputeWave` / `…Closure`) is a Go port of
  the deleted client module, covered by `notebook_autorecompute_test.go`.

## 7. Promotion (`notebook/promote.go`)

Single-cell promotion: pick target pipeline + name → dialect check → move the
file into the pipeline dir, set `class: pipeline`, assign the real target
name, rewrite references in remaining cells (same splice machinery), keep the
asset id stable. Dialect mismatch **warns** ("review the SQL") instead of
blocking with flagged expressions — Bruin's `sqlparser` exposes no transpiler;
same-dialect promotion (the common DuckDB→DuckDB case) is clean. The promoted
asset's fingerprint changes → `never_built` in pipeline envs, the correct
prompt to build it.

## 8. Not built / parked

- Rename/block-reorder don't re-trigger recompute for the *other* cells whose
  references they rewrite (a manual run or any subsequent edit recovers).
- Promote-whole-notebook (subgraph → new pipeline); Monaco squiggles/completion
  for `@viz`.
- Warehouse-backed `notebook_target` (sandbox schemas + manifest/TTL janitor);
  the DuckDB-file default with delete-on-close + startup sweep is what exists.
- Direct Python/SQL cell queries against an arbitrary named project connection;
  today non-DuckDB connections are reached only while importing referenced
  pipeline assets into the local notebook session.
- Parked by decision: Python auto-recompute, parameters/widgets,
  cross-notebook references (workaround: promotion), result persistence
  (reopen re-queries head N), notebook sharing/cloud (folders of files travel
  through git and the snapshot CAS for free).
- Reference syntax is bare names by decision; `{{ ref() }}` is not supported
  in cells.

## Test surface

`internal/web/notebook` covers loader, DAG, runner (real DuckDB + real SQL
parser), import cache + attach fast path, rename invariance, viz parser,
promotion planning. `internal/web/service` covers ComputeState loading +
direction rule, CRUD/run lifecycle, autorecompute eligibility, promotion.
`web/tests/e2e/app/notebooks.live.spec.ts` drives the real server, including a
Python cell querying and materializing an upstream SQL cell through the SDK.
