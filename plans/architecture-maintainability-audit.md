# Architecture and maintainability audit

Status: implementation in progress, 2026-08-23. The native-runtime fact guard,
annotated Go-AST API contract generator, and backend connection capability
registry have shipped locally. This is not a proposal for a rewrite. Each
accepted implementation slice should remain small, preserve Renart's
filesystem/SSE/runtime contracts, and be folded into the relevant document
under [`architecture/`](../architecture/) when it ships.

## 1. Executive assessment

Renart's architecture is fundamentally sound for the product it is becoming.
The important invariants are visible in both code and documentation: the Git
working tree is authoritative, writes go through the Go server, SSE reconciles
frontend state, inspect is read-only, and Bruin remains the compatibility
runtime. The repository also has unusually broad backend and live-flow test
coverage for a project at this stage.

The main maintainability risk is not a missing framework or a bad top-level
design. It is **convergence debt** caused by rapid feature growth:

- the same warehouse/asset capability knowledge is encoded in several places;
- the API type generator still needs a hand-maintained list and is followed by
  manually duplicated frontend response types;
- a few frontend pages and backend packages own too many independent workflows;
- the SQL runtime has moved to Golyglot, while names, comments, docs, and some
  secondary analyzers still describe or reproduce the old Polyglot/WASM shape;
- the live E2E suite proves valuable system behavior but pays for too much of
  that proof twice and serially;
- workspace refresh currently republishes a full snapshot and exposes aliased
  state internally, which is acceptable today but needs measurement and a
  stronger ownership contract before workspace size grows further.

The recommended direction is therefore **consolidation before abstraction**:
establish one capability registry and one API-contract path, extract stateful
controllers from the largest UI surfaces, give backend domains compiler-visible
boundaries, and add performance budgets before changing protocols.

## 2. Scope and evidence

This audit reviewed the current `main` checkout after the native Golyglot
migration and the initial semantic-editor/column-impact work. It covered:

- the current-state documents in [`architecture/`](../architecture/) and the
  plan index plus the largest retained plans;
- backend composition, service boundaries, workspace coordination, HTTP request
  decoding, SQL intelligence, fingerprinting, execution, and scheduler code;
- frontend routing/state synchronization, the largest authoring surfaces, API
  clients/type generation, production chunking, and component/unit coverage;
- CI, release checks, the live Playwright fixture, project configuration, and
  test distribution;
- a production frontend build and a point-in-time local workspace/SSE payload
  measurement against the example project.

Repository inventory at this point:

| Area | Tracked files | Lines | Notes |
| --- | ---: | ---: | --- |
| Go | 606 | 194,321 | 257 test files / 69,200 test lines |
| TypeScript | 179 | 47,851 | includes API clients, state, editor adapters, and tests |
| TSX | 173 | 51,327 | React application and component tests |
| Live Playwright | 29 specs | 16,680 | 202 declared tests across desktop and mobile projects |
| Plan directory before this report | 15 Markdown files | 6,188 | several contain substantial shipped history |

These are inventory numbers, not quality metrics. A large file is only called
out below where it also combines separate state machines or domain concerns.

## 3. What should be preserved

The cleanup should explicitly retain these strengths:

1. **The runtime model is coherent.** The frontend performs an initial workspace
   load, opens one `EventSource`, merges lite workspace events by revision, and
   performs one reconciliation load after reconnect. There is no hidden
   workspace polling to replace.
2. **The backend has a useful transport/domain split.** HTTP handlers generally
   depend on narrow consumer interfaces, services return structured errors, and
   path resolution is centralized.
3. **Generated and persistent artifacts have ownership rules.** Git-authored
   files, `.renart/state.db`, generated frontend types, and runtime caches are
   distinguishable rather than being mixed into one state store.
4. **SQL intelligence has one native semantic foundation.** Golyglot is already
   in process and source-backed. Cleanup should finish that convergence instead
   of introducing a second parser.
5. **System tests exercise real lifecycle boundaries.** The live fixture copies
   a Git workspace, starts the actual Renart binary, observes filesystem/SSE
   reconciliation, and cleans it up. That evidence must not be replaced with
   mocks.
6. **Release checks are broad.** Go tests/vet/vulnerability checks, frontend and
   docs builds/audits, license checks, extension typechecking, and live E2E cover
   the important delivery surfaces.

## 4. Priority map

| Order | Workstream | Impact | Effort | Why now |
| ---: | --- | --- | --- | --- |
| 0 | Correct post-Golyglot docs and add cheap drift gates | Medium | Small | Current architecture text contradicts the shipped runtime |
| 1 | Make API contracts generated, closed, and CI-verified | High | Medium | Backend/frontend drift is currently easy and partially hidden |
| 2 | Create one asset/connection capability registry | High | Medium | Every new warehouse or variant crosses several divergent switches |
| 3 | Batch repeated semantic analysis and consolidate SQL helpers | High | Medium | Column-impact work can analyze the same query once per output column |
| 4 | Extract controllers from notebook/build/review UI | High | Medium–large | These pages combine unrelated state machines and are hard to test locally |
| 5 | Introduce compiler-visible backend domain seams | High | Large, incremental | `internal/web/service` is now a shared namespace for most product domains |
| 6 | Rebalance the test pyramid and shard live E2E | High | Medium | CI proof is strong but slow, serial, and duplicated across viewports |
| 7 | Make workspace snapshots immutable and instrument refresh/SSE | Medium | Medium | Aliasing is unenforced and full snapshots will scale with every feature |
| 8 | Standardize bounded strict request decoding | Medium | Small–medium | Mutation endpoints currently have inconsistent limits and JSON semantics |
| 9 | Add frontend bundle and interaction budgets | Medium | Small first | Existing route splitting works, but large chunks can regress unnoticed |
| 10 | Retire shipped plan history and finish composition-root hygiene | Medium | Ongoing | The current-state/plans distinction is starting to erode |

Order 0–3 are the best near-term return. Workstreams 4–7 should be done as
feature-adjacent slices, not as a development freeze.

## 5. Detailed findings

### 5.1 API contracts are generated from an open-ended allowlist

#### Evidence

At audit time, the deleted `web/scripts/generate-api-types.mjs` contained a
hand-maintained `sources` array naming every Go struct to export. It found
struct bodies and fields using string searches and regular expressions. That
had three consequences:

- a new response type is invisible until someone remembers to add it;
- Go syntax that exceeds the script's small grammar can be silently
  misrepresented or require a special case;
- semantic enums such as severity, status, and action type become plain
  `string` in generated TypeScript.

[`web/lib/api-pipelines.ts`](../web/lib/api-pipelines.ts) then re-declares the
type-check report, assets, findings, presentations, external relations, and
cross-pipeline references to recover narrower unions. The generated equivalents
already exist in
[`web/lib/generated/api-types.ts`](../web/lib/generated/api-types.ts), so there
are two frontend descriptions of the same response.

`pnpm check` runs the generator before typechecking/building, but CI does not
verify that generation leaves the worktree unchanged. A stale generated file is
therefore repaired inside CI rather than reported as a failure, and an omitted
allowlist entry is not observable at all.

#### Implemented result

[`internal/tools/apitypes`](../internal/tools/apitypes) now scans explicit
`// renart:web` roots, follows referenced internal DTOs transitively through the
Go AST, emits named string-constant unions, rejects incompatible TypeScript-name
collisions, and supports a check-only mode. Frontend typechecking uses that
mode, so stale generated output fails locally and in CI. The complete manually
duplicated pipeline type-check/import responses now derive from generated
contracts and retain only small UI-specific union refinements.

#### Residual risk

This is a correctness boundary, not just code style. A backend field can ship
without a typed frontend representation, and a manually narrowed union can
drift from the server without the compiler seeing both sides.

#### Original recommendation

1. Move all public request/response/event contracts into
   `internal/web/model` or a new `internal/web/contracts` package. Services may
   use richer internal models and map once at the transport edge.
2. Mark exported contracts explicitly, for example with a stable source
   annotation such as `//renart:web`, rather than listing every type in a JS
   file.
3. Replace regex parsing with a small Go `go/packages`/`go/ast` generator. Start
   from annotated roots and follow referenced named types transitively.
4. Teach the generator about named string types/constants so important unions
   stay narrow. Keep truly open strings open.
5. Add `generate:api-types:check`: generate into memory or a temporary file,
   compare byte-for-byte, and fail CI on drift.
6. Delete duplicate response shapes. Where UI code needs a narrower view,
   derive it from the generated type with `Pick`, `Omit`, intersections, or a
   runtime assertion rather than copying the complete DTO.

Do not introduce a full OpenAPI framework solely for this. Renart's current
generator can remain lightweight once its roots and parser are reliable.

#### Completion criteria

- adding an annotated Go response automatically brings its reachable types;
- generated output cannot change during `pnpm check` without CI failing;
- no complete backend response is manually duplicated in `web/lib/api-*.ts`;
- contract-generator fixtures cover embedding, pointers, maps, named enums,
  omitted fields, and nested generic-looking syntax.

### 5.2 Warehouse and asset capabilities are duplicated

#### Implemented result

[`internal/bruincompat/capabilities.go`](../internal/bruincompat/capabilities.go)
now owns connection aliases/families, Bruin-derived preferred query and source
types, and explicit parser, analyzer, formatter, and fingerprint dialects.
Service connection normalization/asset selection, SQL LSP graph loading,
parse-context/typecheck, format-on-save, fingerprinting, and brokered Python SQL
consume that registry. A parity test enumerates Bruin's asset-to-connection
mapping and requires each connection family to be supported or explicitly
classified as outside the SQL registry.

Two historical v3 canonicalization differences remain visible rather than
being silently changed: MotherDuck and Vertica format with DuckDB/PostgreSQL
respectively, while their existing fingerprints use generic formatting.
Unifying those fields requires an explicit fingerprint-version migration and
is intentionally deferred for a product decision.

#### Evidence

Warehouse knowledge currently appears in several independent forms:

- [`internal/bruincompat/dialect.go`](../internal/bruincompat/dialect.go) maps
  Bruin asset types to parser dialects;
- [`internal/sqllsp/graph_loader.go`](../internal/sqllsp/graph_loader.go) has its
  own string-based asset-type-to-dialect switch;
- [`internal/web/service/asset_format.go`](../internal/web/service/asset_format.go)
  translates again into formatter dialects;
- [`internal/web/fingerprint/fingerprint.go`](../internal/web/fingerprint/fingerprint.go)
  explicitly says it mirrors the format-on-save mapping;
- [`internal/web/service/python_operator.go`](../internal/web/service/python_operator.go)
  maps connection types for brokered SQL safety;
- [`internal/web/service/asset_type.go`](../internal/web/service/asset_type.go)
  normalizes connection aliases and derives query/source asset types;
- frontend helpers and creation-profile presentation add another view of asset
  kinds and capabilities.

The strings are not all supposed to be identical: Bruin connection names,
Golyglot analyzer dialects, formatter dialects, display families, source asset
types, and query asset types are different dimensions. That is precisely why
one flat `map[string]string` is not enough.

#### Risk

Adding a warehouse, a legacy alias, a sensor variant, or a source type can make
formatting, fingerprinting, LSP diagnostics, notebook queries, creation UI, and
execution disagree. These failures often appear only in one lifecycle stage.

#### Recommendation

Create a small backend-owned capability registry, probably under
`internal/bruincompat`:

```go
type ConnectionProfile struct {
    Family              ConnectionFamily
    Aliases             []string
    QueryAssetType      pipeline.AssetType
    SourceAssetType     pipeline.AssetType
    AnalyzerDialect     string
    FormatterDialect    string
    SupportsDiscovery   bool
    SupportsReadQuery   bool
    SupportsWriteTarget bool
    SupportsSource      bool
}
```

The exact fields should follow existing call sites; this is a shape, not a
required API. Asset variants such as sensors should resolve through the same
profile without pretending that every connection supports every role.

Then:

1. make fingerprinting and format-on-save consume the same formatter field;
2. make the LSP, typechecker, and Python broker consume the same analyzer field;
3. derive source/query creation candidates from capability flags plus Bruin's
   authoritative mappings;
4. serialize the relevant public profile into the existing asset-creation or
   connection-settings response so the frontend does not recreate it;
5. add a table-driven parity test that enumerates Bruin mappings and requires an
   explicit supported/unsupported decision for each type.

Avoid making frontend icon/color selection part of the Go registry. The backend
owns semantic capability; the frontend may map a stable family ID to visual
treatment.

### 5.3 The native Golyglot migration is complete at runtime but not in shape

#### Evidence

[`architecture/backend.md`](../architecture/backend.md) and
[`architecture/sql-lsp.md`](../architecture/sql-lsp.md) correctly say that SQL
parse, validation, lineage, and formatting now call typed Golyglot Go APIs.
There is no SQL WASM runtime.

However:

- the repository-level `AGENTS.md` still describes SQL intelligence and
  formatting as embedded WASM;
- [`architecture/staleness.md`](../architecture/staleness.md) still calls SQL
  formatting embedded WASM and records the old approximately 66 ms cost;
- [`architecture/notebooks.md`](../architecture/notebooks.md) still names the
  implementation worktree in its status line;
- [`internal/sqlintelligence/polyglot.go`](../internal/sqlintelligence/polyglot.go)
  retains legacy `polyglot*` response/schema/token names and JSON tags even
  though native Golyglot now supplies the parsed representation;
- [`internal/sqlintelligence/schema.go`](../internal/sqlintelligence/schema.go)
  still describes a WASM boundary;
- the LSP's [`internal/sqllsp/analyzer.go`](../internal/sqllsp/analyzer.go)
  still contains a large secondary SQL scanner for scopes, CTEs, subqueries,
  projections, identifiers, and clause position.

Some legacy-named helpers are still actively used by the native adapter, and
the local LSP scanner still supplies editor-specific recovery/range behavior.
They are not safe to delete mechanically.

#### Risk

Stale terminology misdirects contributors. More importantly, maintaining two
different structural understandings of SQL makes completion, diagnostics,
rename, and typechecking diverge at syntax edges.

#### Recommendation

Split this into two bounded changes:

1. **Truthful names/docs now.** Correct the current-state docs and comments.
   Rename internal transport-shaped types to engine-neutral terms where no
   public compatibility is involved. Keep the external diagnostic source
   `polyglot` until there is an explicit compatibility decision, because tests
   and clients may observe it.
2. **One structural analysis over time.** Add missing cursor/scope facts to
   Golyglot's typed analysis where they are generally useful. Let the LSP own
   only document projection, Monaco/LSP response shaping, and deliberately
   tolerant editor fallbacks. Replace manual CTE/subquery/projection parsing one
   tested slice at a time.

Keep Bruin's remaining CPython/SQLGlot materialization compatibility behind its
existing narrow adapter. Its dependency and third-party notice cannot be
removed merely because Renart's own SQL path is native.

### 5.4 Column-impact lineage repeats semantic work

#### Implemented result

Artifact column lineage now computes schema-aware compact analysis once and
reuses its output/projection facts for every direct physical-table projection.
It also treats a positively resolved single-source wildcard as a completed
identity mapping instead of asking Golyglot for the same star lineage once per
declared output. Complex CTE, derived, set-operation, ambiguous, and unresolved
cases retain the existing recursive lineage fallback.

`BenchmarkArtifactColumnLineageWideProjection` pins 10-, 50-, and 200-column
direct queries plus 50-column CTE and 200-column wildcard cases. On the audit
machine, the 200-column direct case moved from roughly 118–139 ms, 53.4 MB, and
224k allocations to roughly 3.6–4.6 ms, 1.1 MB, and 6.4k allocations. The
200-column wildcard case is roughly 3.0 ms. The remaining 50-column recursive
CTE case is roughly 26.8 ms and is the evidence for a future reusable analyzed-
query/batch-lineage API in a released Golyglot version; Renart does not add a
workspace-lifetime cache or duplicate Golyglot's recursive semantics locally.

#### Evidence

[`internal/web/service/artifact_column_lineage.go`](../internal/web/service/artifact_column_lineage.go)
first calls `OutputColumnsWithSchema`, then calls `LineageWithSchema` once for
every declared output column. A failed/unresolved result may call schema-free
`Lineage` again, and single-source wildcard handling separately calls
`AnalyzeQuery`.

The shared query-analysis cache helps identical `AnalyzeQuery` calls, but the
public lineage functions can still repeat parse/validation/traversal work for a
wide projection. Cost therefore scales with output-column count even though the
SQL text and schema are constant.

#### Risk

The new column-impact calculation runs on pipeline/artifact paths where users
expect quick planning and editing. Wide models and transitive graph walks can
turn a valuable safety feature into latency that is hard to attribute.

#### Recommendation

1. Add a benchmark fixture with narrow, 50-column, and 200-column queries plus
   one CTE and one wildcard case.
2. Add a Golyglot batch API that returns output columns and lineage for all
   outputs from one parsed/validated analysis, or expose a reusable immutable
   analyzed-query object.
3. Have artifact lineage consume that single result. Preserve the current
   conservative rule: ambiguous, unknown, or unparseable mappings remain
   unknown rather than becoming guessed impacts.
4. Cache only deterministic analysis inputs (`SQL + dialect + normalized
   schema/constraints`) and keep cancellation/failures out of the cache.

This should be measured before and after. Do not add an unrelated service-level
cache that can outlive workspace revisions.

### 5.5 The largest frontend files are application controllers and views at once

#### Evidence

The clearest examples are:

| Surface | Lines | Local state calls | Effect calls |
| --- | ---: | ---: | ---: |
| `notebook-page.tsx` | 4,451 | 67 | 28 |
| `build-page.tsx` | 3,446 | 34 | 11 |
| `pipeline-plan-sheet.tsx` | 2,257 | 27 | 5 |

The issue is not the raw count. `notebook-page.tsx`, for example, coordinates
document selection, runtime events, source approval/import, cell execution,
drag/drop insertion, agent chat, visualization/control editing, dialogs, result
previews, and responsive sidebars. Several of those have independent async
states and revision rules. Similar mixing exists between selection/layout/run
output in `build-page.tsx` and between planning/data loading/review presentation
in `pipeline-plan-sheet.tsx`.

At the same time, `web/components/app` has over 41,000 authored lines and only a
small number of colocated model/component tests. Most behavioral confidence is
therefore paid for through the live binary.

#### Risk

Effects become order-dependent, async stale closures are difficult to reason
about, and small visual changes require live E2E because the state transition
cannot be exercised independently. The files are also natural merge-conflict
hotspots.

#### Recommendation

Extract by **state machine and ownership**, not by visual fragment:

1. `useNotebookController` (or a reducer plus focused hooks) owns selected
   block, draft/revision reconciliation, mutation lifecycle, run lifecycle, and
   runtime-event reconciliation.
2. Source discovery/import/approval becomes a notebook data-source controller
   with pure policy functions and one presenter.
3. Agent-chat transport/activity state remains separate from notebook document
   mutation; applying an agent change still goes through the same notebook
   transaction controller.
4. `build-page` gets separate selection/layout and run/output controllers. The
   existing follow-output-scroll hook is the right model for extracting a
   behavior with a focused test.
5. Pipeline planning exposes a headless plan/review model consumed by deploy,
   review-run, and any future CLI-facing view, while the sheet stays a presenter.

Use reducers when events can be named (`save_started`, `workspace_reconciled`,
`run_finished`) and normal hooks for independent server resources. Do not add a
second global state framework or move transient editor drafts into Jotai simply
to shorten a file.

#### Completion criteria

- async/revision transitions are unit-tested without a browser/server;
- view components no longer call unrelated mutation APIs directly;
- workspace state remains authoritative after an SSE event;
- the same controller drives full and responsive presentations where behavior
  is shared;
- live tests remain for filesystem, SSE, execution, Monaco, and drag/drop
  integration rather than every local toggle.

### 5.6 `internal/web/service` no longer provides a meaningful boundary

#### Evidence

The package contains roughly 150 production files and 61,000 production lines,
with asset editing, execution, planning, SQL discovery, notebooks, agents,
presentations, onboarding, source control, project scaffolding, typechecking,
staleness, and configuration sharing one namespace. It defines hundreds of
exported structs and service receiver methods.

There are already good internal seams—`assetmeta`, `notebook`, `fingerprint`,
`executiongraph`, `scheduler`, `secretstore`, `policy`, and others—but the
remaining service namespace can freely reach across every domain. The
composition root in [`cmd/server.go`](../cmd/server.go) is also 1,576 lines and
constructs most of this graph directly.

[`architecture/backend.md`](../architecture/backend.md) already records the
large flat service package as an open item. Given the addition of notebooks,
presentations, cross-pipeline dependencies, and richer planning, the package is
now large enough that compiler-enforced direction is worth scheduling.

#### Risk

Conceptual boundaries exist only in file names and reviewer memory. Shared DTOs
and convenience calls pull domain logic back into `service`, and initialization
changes accumulate in one high-conflict composition file.

#### Recommendation

Use a strangler-style extraction while preserving the current HTTP interfaces:

1. extract the capability registry and API contracts first, because every later
   package can depend on them without cycles;
2. extract an `execution` domain around plan/render/run contracts and adapters;
3. extract notebook application services after the frontend/controller
   boundaries are stable;
4. extract presentation application services if dashboards/reports continue to
   grow independently;
5. retain a thin `service` facade during each move so handlers and tests migrate
   incrementally;
6. split server construction into small constructors such as project runtime,
   execution stack, scheduler, notebook/presentation services, and HTTP routes.

Add a dependency-direction test or lint using `go list -deps` so domain packages
cannot import `httpapi` or `cmd`, and foundational packages cannot import the
service facade.

Do not create microservices, dependency-injection reflection, or one package per
file. The desired result is a few cohesive application domains in the same
binary.

### 5.7 Live E2E is high-value but pays a serial, duplicated cost

#### Evidence

[`web/playwright.live.config.ts`](../web/playwright.live.config.ts) sets one
worker, disables full parallelism, retries failures, and executes the whole live
suite for both desktop Chromium and Pixel 7. CI gives the single job 60 minutes.

The `liveApp` fixture in
[`web/tests/e2e/live-app-fixture.ts`](../web/tests/e2e/live-app-fixture.ts)
correctly creates an isolated Git workspace and starts/stops a real Renart
process per test. Some warehouse fixtures intentionally serialize access to
shared Docker resources. Those are good correctness choices, but they make each
test expensive, and many backend-heavy flows are repeated unchanged for the
mobile project.

The frontend has 67 Vitest tests across 22 files, while the live suite declares
202 tests and includes substantial setup/assertion logic. This is an inverted
cost distribution for local UI behavior.

#### Recommendation

1. Add a timing reporter that records per-test setup, server startup, body, and
   teardown durations. Publish the slowest list as a CI artifact.
2. Define explicit test groups: core authoring/SSE, notebooks/presentations,
   scheduler/freshness, and warehouse/runtime matrix.
3. Run those groups as separate CI jobs. Keep one worker inside groups that
   share Docker/state constraints; do not simply turn on global parallelism.
4. Tag genuinely responsive behavior and run only that contract on Pixel 7.
   Backend/API/materialization tests should run once unless viewport affects
   their path.
5. Move reducer, combobox, dialog-state, output-scroll, and schema/display logic
   into Vitest/component tests. Keep one live path per important filesystem/SSE
   contract.
6. Keep the multi-warehouse matrix isolated with its own timeout/artifacts so it
   cannot consume the feedback budget for authoring regressions.
7. Add targeted `go test -race` jobs for workspace coordination, scheduler,
   execution graph, and secret storage. A full repository race run is not
   necessary on every PR.

A reasonable first target is a reliable under-35-minute live gate with the same
runtime coverage, not a particular test count.

### 5.8 Workspace snapshots need ownership and measurements before deltas

#### Implemented result

`WorkspaceCoordinator` now owns an immutable whole-state snapshot. `SetState`
clones caller-owned input, `CurrentState` returns a caller-owned deep copy, and
the fast asset-content update clones before changing and replacing state. The
clone preserves concrete values inside `any` fields without a JSON round trip
and fails closed if a future DTO introduces mutable unsupported runtime types.
Tests mutate nested maps, slices, pointers, notebook definitions, and
presentation definitions on both sides of the coordinator boundary and prove
later reads remain unchanged.

`BenchmarkCloneWorkspaceState` records roughly 0.08 ms for 10, 0.8 ms for 100,
and 7–8 ms for 1,000 synthetic two-column assets on the audit machine. Full
refresh/SSE payload instrumentation and budgets remain the next evidence step;
these numbers do not justify a delta protocol.

#### Evidence

[`internal/web/service/workspace_coordinator.go`](../internal/web/service/workspace_coordinator.go)
protects assignment with an `RWMutex`, but `CurrentState()` returns the struct by
value while its slices, maps, and nested pointers still alias coordinator-owned
state. Consumers are read-only by convention only.

Every normal `PushUpdate` reparses the workspace and publishes a content-stripped
full workspace snapshot. The frontend correctly rejects old revisions and
preserves omitted content for lite events.

On one local example-workspace sample, `/api/workspace` was about 270 KB and
served in roughly 4 ms. That is healthy and is not evidence that a delta protocol
is currently needed. It is a baseline showing that payload size, parse time, and
fan-out should be observed as the workspace grows.

#### Recommendation

1. Make snapshot ownership explicit. Prefer an immutable snapshot replaced as a
   whole; otherwise deep-copy at publication boundaries. Add a test proving a
   consumer cannot mutate future coordinator reads.
2. Instrument refresh duration, snapshot byte size, event coalescing, hub client
   count, and dropped/slow-client events.
3. Add small/medium/large synthetic workspace benchmarks for parse, strip,
   marshal, and frontend merge.
4. Define budgets before designing incremental events.
5. If budgets are crossed, add typed deltas with revision/base-revision and a
   full-snapshot recovery path. Preserve reconnect reconciliation.

Do not replace SSE with polling or create a second frontend persistence model.
The current synchronization architecture is correct.

### 5.9 JSON request handling has inconsistent safety semantics

#### Evidence

[`internal/web/httpapi/request_json.go`](../internal/web/httpapi/request_json.go)
provides a good strict decoder: one non-null object, unknown-field rejection,
and trailing-JSON rejection. Several handlers also use `http.MaxBytesReader`.
Other mutation handlers still construct `json.NewDecoder(r.Body)` directly and
therefore differ in unknown-field, trailing-content, and size-limit behavior.

Renart is local-first, so this should not be framed as an internet-service
emergency. It still matters for accidental huge editor payloads and for users
who explicitly run with remote access enabled.

#### Recommendation

Create one handler-level helper:

```go
func decodeJSONObject[T any](w http.ResponseWriter, r *http.Request, max int64) (T, error)
```

It should apply `MaxBytesReader`, strict single-object decoding, stable API error
codes, and a conservative default. Editor/definition/import endpoints can opt
into documented larger limits. Migrate mutation endpoints in groups and test
unknown fields, `null`, two objects, malformed JSON, and an oversized body.

Streaming uploads and intentionally polymorphic payloads should use separate,
explicit helpers rather than weakening the default.

### 5.10 Frontend splitting exists, but there are no bundle budgets

#### Evidence

Vite route splitting and manual vendor chunks already keep notebooks,
presentations, Markdown, charts, graph rendering, and Monaco-related code partly
separate. The current production build nevertheless reports chunks over 500 KB.
The largest minified outputs in this audit were approximately:

| Chunk | Size |
| --- | ---: |
| main application | 765 KB |
| Markdown editor | 524 KB |
| chart vendor | 476 KB |
| UI vendor | 275 KB |
| presentation page | 142 KB |
| notebook page | 130 KB |

This is not automatically a defect for a local IDE, and minified bytes are not
startup time. It is a missing regression signal.

#### Recommendation

1. Produce a Vite manifest/bundle analysis artifact in CI.
2. Set budgets for the initial shell and for lazy feature families rather than
   one arbitrary global chunk limit.
3. Measure cold start to first interactive canvas/editor on the supported local
   hardware class.
4. Before changing chunks, inspect the main bundle with a visualizer. Prefer
   lazy feature boundaries over manual package-by-package fragmentation.
5. Keep Markdown, charting, Monaco, notebooks, and presentations lazy when their
   routes/features are unopened.

### 5.11 Plans and current-state docs are starting to overlap

#### Evidence

[`plans/README.md`](README.md) correctly says plans are ephemeral and shipped
reality belongs in `architecture/`. In practice:

- `pipeline-readiness-and-rendering.md` is about 1,900 lines and retains the
  history of several shipped phases for a small remaining tail;
- `execution-parallelism.md`, `secret-management.md`, `python-asset-sdk.md`, and
  `notebook-platform.md` mix current implementation records with remaining work;
- a few current-state documents still contain branch names or pre-Golyglot
  implementation facts.

#### Risk

Contributors have to reconcile two sources of truth, and old performance or
runtime statements look authoritative because they live in architecture docs.

#### Recommendation

1. Correct the known stale current-state statements immediately.
2. For each mostly shipped plan, move the as-built contract into architecture,
   retain only the genuinely open tail in a short focused plan, then delete the
   historical document. Git already preserves the narrative.
3. Add a small documentation check to release work: no current-state branch
   names, no obsolete SQL-WASM claim, valid local links, and every plan indexed.
4. Keep this audit as a prioritization document only. Once its accepted slices
   have focused plans/issues, delete it rather than letting it become another
   permanent history file.

### 5.12 Project runtime lifecycle and composition should remain measured work

[`architecture/backend.md`](../architecture/backend.md) notes that lazily opened
project runtimes retain their watcher, SQLite pool, and scheduler for the process
lifetime. That is harmless in the common one-project process today but will
matter as project switching becomes routine.

Add per-runtime last-used time, resource counters, and an explicit `Close`
contract first. Then implement idle eviction with active-run/subscription
guards. Keep the registry entry so reopening remains deterministic. This is
later work unless measurements show real descriptor/memory pressure.

The same principle applies to [`cmd/server.go`](../cmd/server.go): split its
composition graph for legibility and testability, but keep one process and one
workspace runtime. A large constructor is not a reason to introduce a control
plane.

## 6. Recommended delivery sequence

### Phase A — guardrails and factual cleanup

Small, low-risk work that makes subsequent changes safer:

1. correct post-Golyglot architecture/AGENTS comments and old timing claims;
2. add generated-API drift verification;
3. add a bundle manifest and Playwright timing artifact;
4. benchmark all-output column lineage;
5. add an immutable-snapshot ownership test before changing implementation.

### Phase B — shared semantic sources

1. introduce the capability registry with parity tests;
2. migrate formatter/fingerprint/LSP/Python-broker consumers;
3. expose the public capability subset to the frontend;
4. replace the API generator allowlist/regex parser with annotated AST roots;
5. remove duplicated frontend response types.

These two workstreams should land in separate commits but can share review
because together they remove the most common cross-layer drift.

### Phase C — headless application controllers

Start with the next feature or bug touching each surface:

1. notebook document/mutation/run controller plus unit tests;
2. notebook source-import controller;
3. build selection/run-output controller;
4. shared pipeline-plan/review model.

Keep rendered components and route behavior stable. This is an extraction, not
a redesign.

### Phase D — backend domain extraction

Use the newly stable contracts and capability package to extract execution,
notebook, and presentation application services. Keep the `service` facade and
move handlers one group at a time. Split server wiring alongside each domain so
the composition root shrinks naturally.

### Phase E — scale only from measurements

1. shard live E2E and reduce duplicated mobile execution;
2. make workspace snapshots immutable;
3. add runtime idle eviction if measurements justify it;
4. introduce workspace deltas only if refresh/payload budgets are exceeded;
5. tune lazy chunks only after bundle/startup profiling identifies the owner.

## 7. Suggested measurable outcomes

The cleanup is successful when it produces observable constraints, not merely
more files:

- **Contracts:** zero full API response shapes duplicated manually; generated
  drift fails CI.
- **Warehouse support:** one backend profile answers connection aliases,
  parser/formatter dialects, supported asset roles, and discovery/query
  capabilities; Bruin parity is table-tested.
- **SQL analysis:** artifact column lineage parses/analyzes each SQL/schema pair
  once; wide-query benchmark time grows near-linearly with AST/lineage size, not
  by repeated full analysis per output.
- **Frontend behavior:** revision/run/source-import transitions are covered by
  fast tests, while live tests focus on integration boundaries.
- **Backend boundaries:** extracted domain packages cannot import transport or
  the old service facade.
- **Live CI:** equivalent system coverage reliably completes in under 35 minutes
  through test grouping and a targeted mobile contract.
- **Workspace synchronization:** snapshot ownership is enforced, and refresh
  duration/payload/fan-out have recorded budgets.
- **HTTP safety:** every JSON mutation endpoint has an explicit body limit and
  consistent single-object semantics.
- **Docs:** current-state docs contain no branch-local or retired SQL-runtime
  assertions; completed plans are folded away.

## 8. Explicit non-goals

This audit does **not** recommend:

- replacing SSE with polling or WebSockets;
- replacing Jotai/TanStack Router with another frontend framework;
- rewriting `internal/web/service` in one branch;
- splitting Renart into services or introducing a hosted control plane;
- bypassing Bruin semantics in direct execution where parity is uncertain;
- removing Bruin's CPython/SQLGlot compatibility or notices before the upstream
  execution path no longer needs them;
- guessing lineage or schema facts to improve apparent coverage;
- optimizing bundle size, workspace events, or runtime eviction without a
  measured budget.

The common theme is to make existing boundaries enforceable and existing
sources of truth singular. That provides more leverage than a broad rewrite and
keeps Renart's local-first product model intact.
