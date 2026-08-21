# Enabling dbt assets in renart — evaluation

Status: evaluation (in-depth, no implementation). Question: can renart open an
**existing dbt project** and light up the features it already has for bruin
assets — canvas/lineage, column inference, IntelliSense, type checking, runs,
staleness — on dbt models? Short answer: **yes, with a hybrid architecture
that uses the dbt CLI as the compilation oracle and renart's existing wasm SQL
engine for everything keystroke-latency.** The hard parts are Jinja fidelity
and diagnostic span mapping, not the graph or the UI.

## 1. Product framing

The pitch is "point renart at your dbt repo and it becomes your dbt IDE" —
not "migrate to bruin". That implies:

- dbt files remain canonical and dbt-runnable at all times (the same
  compatibility promise renart makes for bruin files).
- renart must be useful **read-only first**; writing into a dbt repo
  (schema.yml sync) comes later and cautiously — dbt users have strong
  opinions about their YAML.
- `dbt` itself (or a managed copy) does the running and the authoritative
  compiling. Renart never reimplements dbt's execution semantics.

## 2. What a dbt project is, from renart's perspective

| dbt artifact | Contents renart needs |
| --- | --- |
| `dbt_project.yml` | project name, model paths, folder-level configs (materialization, schema) |
| `models/**/*.sql` | Jinja-SQL: `{{ ref() }}`, `{{ source() }}`, `{{ config() }}`, `{{ var() }}`, macros |
| `models/**/*.yml` (schema files) | model/column descriptions, tests, constraints — the metadata layer |
| `sources`, `seeds`, `snapshots`, `analyses` | additional node types |
| `macros/`, `packages/` (`dbt_utils`, …) | arbitrary Jinja that can generate SQL — the fidelity problem |
| `profiles.yml` (usually `~/.dbt/`) | connections: adapter type + credentials per target |
| `target/manifest.json` (from `dbt parse`) | **the entire graph**: nodes, `parent_map`, raw + compiled SQL, per-node checksums, `depends_on` (incl. macros), configs, columns |
| `target/catalog.json` (from `dbt docs generate`) | warehouse-observed schemas |
| `target/run_results.json` | per-node run outcomes/timings |

Key observation: **manifest.json alone answers most of what renart's
workspace parse answers for bruin pipelines**, and it's produced by
`dbt parse`, which needs a valid `profiles.yml` but does **not** connect to
the warehouse and is fast (partial parsing on re-runs).

## 3. The central problem and the strategy space

dbt SQL is Jinja with a Python macro ecosystem. Three ways to deal with it:

- **(A) Artifact-only.** Consume `manifest.json` (+ compiled SQL from
  `dbt compile`). Perfect fidelity, zero reimplementation — but compiled
  output is only as fresh as the last compile, and per-keystroke features
  can't wait 1–10 s for dbt.
- **(B) Native reimplementation.** Parse dbt-Jinja ourselves (bruin's
  `pkg/jinja` exists, but dbt macros are a moving target). Fast, but fidelity
  on macro-heavy projects is a permanent losing battle. Rejected as the
  primary path.
- **(C) Hybrid — recommended.**
  - `dbt parse` (watched, debounced) is the **graph oracle**: nodes, deps,
    configs, checksums.
  - `dbt compile --select <model>` (async, on save) is the **SQL oracle**:
    authoritative compiled SQL per model, cached in `target/compiled/`.
  - For keystroke-latency intelligence, a **local substitution renderer**
    handles the 95% case: replace `{{ ref('x') }}` / `{{ source('a','b') }}`
    / `{{ var('v') }}` with their manifest-resolved relation names/values and
    strip `{{ config(...) }}`. These substitutions are *localized*, so
    editor positions survive with a simple offset map. Models whose SQL still
    contains unresolved Jinja after substitution (macro-generated SQL) fall
    back to the last `dbt compile` output — correct but positionally coarse.

dbt's new Fusion engine (Rust, fast static parsing) could later replace the
python CLI as the oracle; treat it as a drop-in upgrade of the seam, not a
dependency (license is source-available, not Apache — don't link it, shell
out if ever).

## 4. Mapping dbt onto renart's model

| dbt | renart |
| --- | --- |
| project | one pipeline (v1); folder-grouping on the canvas reuses the existing layer convention |
| model | asset with `flavor: dbt` (new field alongside `class`; `class` stays `pipeline`) |
| `ref()` edges (manifest `parent_map`) | `depends` |
| source | external/unmanaged asset node (exists conceptually for lineage already) |
| seed | seed asset (renart has the concept) |
| snapshot | asset, read-only v1 |
| ephemeral model | non-materialized node; compiled SQL inlines it (lineage still shows it via manifest) |
| materialization (`view/table/incremental/ephemeral`) | displayed via the existing materialization DTO fields; **read-only v1** (writing means editing `config()` blocks) |
| schema.yml columns/descriptions/tests | asset columns + checks (display v1, sync later) |
| `profiles.yml` target | environment; adapter credentials map to bruin connection types |

The `flavor` dimension is the load-bearing change in the core model: loaders,
executors, renderers, and editors dispatch on it, while everything
graph-shaped (canvas, catalog, staleness service, run views) stays
flavor-agnostic. This mirrors how `class: notebook` was introduced.

## 5. Feature-by-feature evaluation

### 5.1 Workspace parse, canvas, catalog, lineage — **easy**

Detect `dbt_project.yml` during workspace scan → run the managed
`dbt parse` → translate manifest nodes/edges into `model.Asset`s. The watcher
already exists; dbt file events trigger a debounced re-parse (dbt's partial
parsing keeps this seconds-scale even on large projects; renart's own parse
of the manifest is milliseconds). Tests from schema files render as checks in
the catalog.

### 5.2 Column inference — **medium, works**

Renart's inference runs the native Golyglot engine over SQL plus upstream
schemas (`sqlintelligence/lineage.go`). For dbt: feed it the **compiled**
(or substitution-rendered) SQL and build the schema map from, in priority
order: schema.yml declarations → renart's own inference cascade over
upstream models → `catalog.json` / live warehouse introspection (the
`fill-columns-from-db` path, once profiles are mapped). Ephemeral models are
already inlined in compiled SQL. Macro-generated column lists
(`dbt_utils.star`) are exactly why the compile oracle, not the substitution
renderer, is the source for this feature.

### 5.3 IntelliSense — **medium; one genuinely new component**

- `ref('…')` / `source('…','…')` completion from the manifest: trivial and
  high-value.
- Column completion + unresolved-column diagnostics: reuse
  `ParseContextService` with a dbt resolver — parse the
  substitution-rendered SQL against upstream schemas. **Span mapping** back
  to the raw editor buffer is the new component: substitutions are local, so
  a monotone offset map suffices for the common case; for macro-heavy models,
  degrade to model-level (not token-level) diagnostics from the compile
  oracle rather than showing wrong squiggles.
- Jinja-aware editing (don't autocomplete SQL inside `{% %}` blocks) —
  renart already handles bruin Jinja in Monaco; dbt adds `{% macro %}`
  blocks, mostly a tokenizer concern.

### 5.4 Type checking — **medium, natural fit**

`CheckPipeline` (`service/typecheck.go`) is already
render-then-validate-per-asset with a pluggable time window; the dbt version
swaps "bruin builder + bruin Jinja render" for "manifest + compiled SQL" and
keeps the validation walk, the schema cascade, and the suppression
heuristics. The CLI (`renart type-check`) then works on dbt projects in CI —
a genuinely differentiated feature (dbt has no static type checker;
SQLMesh/sqlmesh-style checking on an existing dbt repo is the headline).

### 5.5 Runs — **easy-medium**

Execute via the dbt CLI: `dbt run --select <model>` (or `build` to include
tests), streamed into the existing run log UI; parse `run_results.json` into
run records. The executor seam is the same shape as the bruin CLI fallback.
`policy.Check` wraps dbt invocations too — target selection maps to renart
environments, so protected environments protect dbt targets identically.

### 5.6 Staleness — **medium, surprisingly good fit**

The fingerprint engine needs: content identity per node + upstream closure.
The manifest provides per-node checksums **and** `depends_on.macros`, so a
dbt fingerprint = H(node checksum ‖ hashes of the macro closure ‖ vars ‖
sorted upstream fps) — arguably *more* precise than dbt's own
`state:modified`. Facts/coverage: ingest `run_results.json` per run
(including runs the user did outside renart, by watching `target/`), write
into matlog with dbt-scoped asset ids. The staleness service and UI then work
unchanged. Incremental models' interval coverage doesn't map 1:1 (dbt
incrementals are self-referential, not window-keyed) — v1 treats them as
full-refresh "built" markers.

### 5.7 Inspect / notebooks — **medium, gated on connection mapping**

Map `profiles.yml` adapters to bruin connection types (duckdb, postgres,
snowflake, bigquery, redshift, databricks cover the overwhelming majority).
Once mapped, Inspect (read-only SELECT against the model's relation) and the
notebook import resolver work as they do for bruin assets. Adapter configs
renart can't map degrade gracefully: graph + intelligence still work, only
warehouse-touching features are disabled with a clear message.

### 5.8 Editing — **hardest to get right; phase it last**

Read-mostly first. Then, using the node-preserving YAML codec
(`asset_yaml_codec.go` — built for exactly this): an explicit **"sync columns
to schema.yml"** action writing inferred columns/descriptions into the right
schema file, and description/test editing through the same transaction
pattern as bruin assets. The assetmeta provenance model doesn't transfer
(dbt has no free meta map with stable semantics across tools — actually
`meta:` exists and is free-form, so provenance keys *could* live there, but
adopt only if reconciliation proves necessary). Editing `config()` blocks
inside model SQL: not in scope; too easy to corrupt.

## 6. The dbt toolchain: managed, not assumed

Renart already embeds Python + uv. Provision dbt the same way: a managed venv
per project (`uv venv` + `dbt-core` + the adapter inferred from
`profiles.yml`), version-pinned to a supported range, with an escape hatch to
use the user's own `dbt` from PATH (respect their lockfiles if the project
pins dbt). `dbt deps` runs on first open when `packages.yml` exists. This
avoids "works on my machine" drift and makes the compile oracle reliable.

## 7. Risks

- **Manifest schema churn** across dbt-core versions (v10–v12+). Mitigate:
  consume only stable fields (nodes, parent_map, checksums, depends_on,
  compiled_code), integration-test against the supported version range, pin
  the managed dbt.
- **Macro-heavy projects** degrade the keystroke experience to
  compile-oracle freshness. Acceptable if the degradation is honest (show
  "as of last compile" on diagnostics that came from stale compiled SQL).
- **Compile needs the warehouse** for introspective macros
  (`get_column_values`, `run_query` at parse time). `dbt parse` doesn't;
  `dbt compile` sometimes does. Requires credentials to be configured before
  full intelligence lights up — same posture as `fill-columns-from-db`.
- **Python models** (`.py` dbt models): graph + metadata yes, intelligence
  no (v1). Same parking decision as notebook Python cells.
- **Two sources of truth for runs**: users will keep running `dbt` in their
  own terminal. Watching `target/run_results.json` for external runs keeps
  staleness honest. (A web-UI terminal — dropped from the shipped CLI v1,
  design in git history at `plans/cli-v1.md` — would make running dbt
  *inside* renart natural if it ever lands.)
- **Scope creep toward "renart is a dbt replacement"**: the boundary is
  fixed — dbt executes, renart never renders dbt SQL for execution purposes.

## 8. Recommended phasing

1. **P0 — open & see** (biggest demo value): detect dbt projects, managed
   `dbt parse`, manifest → assets/edges, canvas + catalog + lineage, columns/
   descriptions/tests from schema files, read-only model editor with Jinja
   highlighting. No warehouse needed.
2. **P1 — intelligence**: substitution renderer + parse-context integration,
   ref/source completion, unresolved-column diagnostics with span mapping,
   type checking (UI + `renart type-check` on dbt projects), compile-oracle
   plumbing (`dbt compile --select` on save, async).
3. **P2 — run & trust**: dbt executor + run UI, `run_results.json` ingestion
   (incl. external runs), fingerprints from manifest checksums + macro
   closure, staleness badges, profiles→connections mapping, Inspect.
4. **P3 — edit**: schema.yml column sync + description/test editing through
   the transaction pattern; evaluate provenance-in-`meta` only if
   reconciliation conflicts appear in practice.

## 9. Verdict

Feasible and strategically strong: the graph/metadata layer is nearly free
(manifest), the intelligence layer reuses the wasm engine and type-check
machinery with two new components (substitution renderer + span mapping,
compile-oracle plumbing), and staleness maps better than expected. The
riskiest bets are Jinja fidelity on macro-heavy repos (mitigated by honest
degradation to compiled output) and dbt version churn (mitigated by the
managed toolchain). P0+P1 deliver the headline — "type checking and
IntelliSense for your existing dbt project" — without touching execution at
all.
