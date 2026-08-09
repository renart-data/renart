# Cross-pipeline dependencies

Status: in progress — Phases 1 and 2 plus Phase 3 deployment binding and
snapshot prerequisite resolution are implemented; durable scheduled waiting
remains

## Goal

Let one Bruin asset in the current Renart workspace depend on an asset in a
different pipeline while preserving all of Renart's stronger guarantees:

- files remain ordinary Bruin files and work with the Bruin CLI;
- the Catalog and Build canvases show an unambiguous workspace-wide edge;
- typecheck reports duplicate or unresolved producers before execution;
- freshness cascades through a full dependency but not a symbolic lineage edge;
- a scheduled downstream run starts only after the required upstream data is
  current for the downstream interval;
- reviewed runs, deployments, recovery, and run history retain the exact
  prerequisite identity they used.

The first version is intentionally **same workspace, same Renart environment**.
Cross-repository resolution needs an external registry/control plane and does
not fit Renart's one-server, one-Git-workspace runtime model.

## Bruin contract and prior art

Bruin already defines the portable file format Renart should use:

```yaml
# producer pipeline
name: raw.orders
uri: duckdb://warehouse/raw/orders

# consumer pipeline
name: analytics.orders
depends:
  - uri: duckdb://warehouse/raw/orders
```

Asset names are only pipeline-local. Bruin therefore uses an explicitly
authored, globally unique `uri` to find a producer across pipelines and repos.
Bruin Cloud waits for successful upstream data intervals that fully cover the
downstream interval, supports mixed schedules through coverage union, and gives
up after 12 hours. It models that wait as a prerequisite sensor rather than
silently starting another pipeline. See the official
[cross-pipeline dependency documentation](https://getbruin.com/docs/bruin/cloud/cross-pipeline.html).

The Bruin version currently used by Renart already:

- parses `Asset.URI` and `depends: [{uri: ...}]`;
- validates duplicate and unresolved URIs across a repository in
  [`ValidateCrossPipelineURIDependencies`](https://github.com/bruin-data/bruin/blob/v0.11.691/pkg/lint/rules.go#L2223-L2283);
- excludes non-asset and symbolic dependencies from the local CLI execution
  scheduler. A symbolic edge is explicitly lineage-only; it must not make a
  downstream wait. See the
  [scheduler relationship construction](https://github.com/bruin-data/bruin/blob/v0.11.691/pkg/scheduler/scheduler.go#L807-L817).

Renart should reuse the syntax and validation rules, but the local Renart
scheduler must implement the prerequisite/coverage behavior itself. That
behavior is a Bruin Cloud feature, not part of the local Bruin task scheduler.

## What exists in Renart today

There is partial format support, but not a coherent feature:

- Bruin parsing preserves asset URIs and URI upstreams.
- `assetmeta` already round-trips manual URI dependency keys (`u:<uri>#<mode>`),
  and the transaction API accepts `{uri, mode}`. The guided dependency picker
  only exposes same-pipeline asset names.
- workspace discovery parses all pipelines, and the SQL LSP already builds one
  workspace graph. SQL relation completion can therefore see assets in sibling
  pipelines.

The important gaps are:

1. `model.Asset` drops `Asset.URI`, upstream type, and upstream mode. Catalog
   lineage then joins edges by asset name across the whole workspace, which is
   ambiguous when two pipelines use the same name and cannot resolve a URI.
2. Typecheck, dependency validation, schema inference, and asset closure operate
   on one parsed pipeline. URI validation is not run against the workspace.
3. `fingerprint.Engine.DAG` is pipeline-local. A URI (or any asset outside the
   parsed pipeline) contributes only a stable `ext:<type>:<value>` token, so an
   upstream edit or materialization does not make the consumer stale.
4. Staleness caches and recomputation are keyed per pipeline. An upstream run
   completion does not invalidate downstream pipeline snapshots.
5. Build-needed ordering, reviewed selection, the execution-target snapshot,
   and captured upstream-writer evidence ignore URI producers.
6. Deployment snapshots contain one pipeline directory and no immutable record
   of which pipeline owned an external URI when deployment was reviewed.
7. Scheduled occurrences have no prerequisite state distinct from generic
   planning/run-slot waiting.

## Recommended product contract

### Identity and resolution

- A dependency that crosses a pipeline boundary **must** use Bruin's URI form.
  A bare asset name always means an asset in the same pipeline.
- URIs are compared exactly after trimming surrounding whitespace. Renart does
  not reinterpret schemes or normalize warehouse identifiers.
- A URI must resolve to exactly one pipeline asset in the current workspace.
  Duplicate producers are errors. An unresolved full dependency blocks review,
  deploy, and execution; an unresolved symbolic dependency is a warning because
  it is presentation-only.
- Pipeline assets are valid producers. Notebook cells are not: they must be
  promoted first. In the first version, sensors are also not valid producers
  because they do not own durable materialization coverage.
- Renart does not invent a hidden URI from an asset name. The editor can suggest
  a URI from the exact physical target when one is available, but the committed
  value is explicit and reviewable.

### Full versus symbolic edges

- `mode: full` participates in cycle checks, fingerprint propagation,
  staleness, run readiness, and captured read evidence.
- `mode: symbolic` appears in lineage only. It never orders execution, changes
  a fingerprint, blocks a run, or requires coverage. This matches Bruin's
  documented meaning and should also correct Renart's current tendency to hash
  symbolic in-pipeline edges like full dependencies.

### Manual and scheduled execution

- A manual review computes each full external prerequisite immediately. If an
  upstream is not ready, the plan shows an actionable blocker and links to the
  producer pipeline; an interactive run does not sit invisibly for hours.
- A scheduled occurrence may wait durably. It is admitted only when every full
  external prerequisite is ready. The schedule page and run details show
  `Waiting for prerequisites`, the producer, required interval, current
  coverage, and deadline rather than the generic `Run waiting` copy.
- The first version does **not** automatically enqueue or run the producer.
  Pipelines remain independently scheduled and failure-isolated, matching
  Bruin's prerequisite model. A future explicit orchestration policy may add
  upstream triggering, but it must never be an implicit side effect of
  `depends`.
- Build needed for a consumer pipeline never inserts assets from another
  pipeline into its execution plan. It either sees the prerequisite as ready or
  reports the producer that must be built.

### Readiness and interval coverage

For a full external dependency, ready means:

1. the URI still resolves to the deployment-bound producer pipeline;
2. the producer's selected source/configuration has an exact current
   fingerprint and target identity;
3. the latest physical writer is unambiguous and matches that producer;
4. successful coverage under that fingerprint and environment satisfies the
   consumer selection.

Coverage rules:

- interval-aware producer + interval-aware downstream: the union of successful
  producer intervals must fully cover the downstream `[start, end)`;
- non-interval producer: one current full marker is sufficient;
- interval-aware producer + non-windowed downstream: require current coverage
  for the downstream's resolved default execution interval; never treat any
  historical interval as universal;
- dirty/active write claims, runtime-only target identity, missing deployment,
  ambiguous writers, or missing coverage all fail closed.

This reuses Renart's existing materialization facts, current-generation writer
table, and interval union rather than introducing a second success ledger.

### Deployment binding

A downstream deployment must not silently follow a URI that is later reassigned
to another pipeline. During deployment review, store an external-dependency
manifest alongside the snapshot:

```text
consumer asset id
dependency URI
mode
producer pipeline UUID
producer asset URI
```

Do not copy the producer source into the consumer snapshot and do not pin the
producer to one forever-old snapshot. At occurrence planning time, resolve the
same URI inside the producer environment's currently pinned deployment and
record that exact producer snapshot, asset ID, fingerprint, target, variables
hash, and required coverage in the reviewed run prerequisite. This gives an
immutable run decision while still allowing the producer to deploy
independently.

If URI ownership moves to another pipeline, the downstream deployment becomes
invalid and must be reviewed again. Renaming a producer asset while retaining
its URI is allowed after the producer is redeployed; facts use the new stable
asset ID and old coverage is not accidentally reused.

## Proposed architecture

### 1. Workspace dependency graph

Add a small backend-owned resolver (for example
`internal/web/dependencygraph`) that consumes all parsed workspace pipelines and
produces an immutable graph revision:

```go
type Node struct {
    PipelineUUID string
    PipelineID   string
    AssetName    string
    AssetID      string
    URI          string
}

type Edge struct {
    ConsumerID string
    ProducerID string // empty only for unresolved symbolic edges
    Type       string // asset | uri
    Value      string
    Mode       pipeline.UpstreamMode
}
```

The resolver owns:

- pipeline-local name lookup;
- workspace-wide URI lookup and duplicate detection;
- full-edge cycle detection across pipeline boundaries;
- reverse edges for invalidation and navigation;
- deterministic graph and dependency-manifest hashes.

Build it once per workspace revision. Snapshot planning uses the same resolver
over materialized snapshot pipelines rather than mutating the cached working
tree graph.

Do not make `WorkspaceService`, the LSP, staleness, and the Catalog each invent
their own URI index.

### 2. API and authoring model

Extend the workspace DTO without breaking consumers that still use the flat
list:

```go
type AssetDependency struct {
    Type               string `json:"type"`
    Value              string `json:"value"`
    Mode               string `json:"mode"`
    ResolvedAssetID    string `json:"resolved_asset_id,omitempty"`
    ResolvedPipelineID string `json:"resolved_pipeline_id,omitempty"`
}

type Asset struct {
    URI          string            `json:"uri,omitempty"`
    Upstreams    []string          `json:"upstreams"` // compatibility
    Dependencies []AssetDependency `json:"dependencies,omitempty"`
}
```

The guided metadata editor should:

- expose an asset URI field with uniqueness feedback;
- group dependency candidates by pipeline;
- write a normal asset dependency for the current pipeline;
- write `{uri: ...}` for a sibling-pipeline choice;
- make full/symbolic explicit;
- explain why an asset without a URI cannot yet be selected cross-pipeline and
  offer to set one in a separate, reviewable edit.

Catalog and Build canvases consume resolved IDs from the DTO. Never infer a
workspace edge by globally matching `upstreams[]` names.

### 3. Validation and SQL intelligence

- Run Bruin's repository-wide URI validator (or the shared Renart graph's
  equivalent with mapped diagnostics) during workspace typecheck.
- Make duplicate/unresolved/cycle findings asset-addressable and show the
  producer pipeline where applicable.
- Give pipeline typecheck the workspace LSP graph instead of constructing a
  pipeline-only connection graph. SQL relation resolution remains based on SQL
  names, not dependency URIs; a URI edge is an orchestration declaration, not a
  SQL rewrite.
- Keep incomplete YAML/SQL editable. A partially typed URI should produce a
  diagnostic, not make the asset disappear from the workspace.

### 4. Global fingerprint propagation

Generalize fingerprint calculation around stable asset IDs and a dependency
resolver. A full URI producer contributes `up:<producer fingerprint>` exactly
like a local full dependency. Symbolic edges contribute nothing. The engine
must retain deterministic topological order over the workspace graph while
returning pipeline-scoped results to existing callers.

`AchievedFingerprints` and upstream-writer capture need the same resolver:

- if the external producer ran earlier in another run, use the exact latest
  successful writer captured immediately before the consumer starts;
- if it is not current/available, do not execute the consumer;
- persist the external writer in the existing read-set evidence keyed by
  producer asset ID.

When a producer definition, deployment pin, target writer, or coverage changes,
walk reverse full edges and recompute subscribed downstream pipeline snapshots.
Publish the existing SSE staleness event for each affected pipeline; no browser
polling is added.

### 5. Planner and scheduler prerequisites

Add a redacted `Prerequisites` section to the pipeline plan and persist it in
the confirmed plan/run spec. Each item contains producer identity, URI,
deployment ordinal/version, required interval, expected fingerprint/target,
coverage state, and a user-facing reason. Secret values are never included.

For scheduled occurrences, add a durable prerequisite lifecycle rather than
overloading run-slot waiting:

```text
pending -> waiting_prerequisites -> admitting -> active -> terminal
```

Persist the deadline and last evaluated reason. Wake waiting occurrences from
`RunCompleted`/target-write events using reverse URI edges, with a bounded River
snooze as crash-recovery safety. Re-evaluation must be idempotent and must not
consume a pipeline run slot while waiting.

On success, admission revalidates source/configuration/data-state identities in
one transaction, just as reviewed plans do today. On timeout or a permanently
invalid binding, mark the occurrence failed with an actionable prerequisite
error.

### 6. Deployment storage and retention

Add a versioned dependency manifest to deployed snapshots (a JSON column or a
normalized child table). Snapshot GC retains it with the owning snapshot; it
does not create a retention reference to every historical producer snapshot.
Confirmed run plans already retain the exact producer snapshot used by that
run and follow normal run-history retention.

Schedule promotion validates the downstream snapshot's dependency manifest
against each producer's same-environment deployment before changing pins. A
batch promotion remains all-or-nothing.

## Delivery phases

### Phase 1 — correct graph, files, and lineage

1. [x] Introduce the shared workspace dependency graph and diagnostics.
2. [x] Add URI/typed dependencies to DTOs and generated web types.
3. [x] Add URI editing and sibling-pipeline dependency selection.
4. [x] Resolve Catalog/Build edges by asset ID; render cross-pipeline labels.
5. [x] Add repository-wide typecheck and full-edge cycle tests.

This checkpoint shipped with execution blocked until Phase 2 added an exact
reviewed-prerequisite path.

### Phase 2 — staleness and manual readiness

1. [x] Make fingerprints and reverse invalidation workspace-aware.
2. [x] Extend target/read evidence to external producers.
3. [x] Add plan prerequisites and manual readiness blockers.
4. [x] Make Build needed and asset-closure selection dependency-aware without
   pulling external assets into the consumer run.
5. [ ] Add DuckDB live tests with two pipelines, edits, failed writes, ambiguous
   writers, and interval gaps. The fingerprint, planning, exact-writer capture,
   ambiguity, and coverage rules have focused backend integration tests; the
   browser-level two-pipeline scenario remains.

Manual working-tree execution is now supported through a reviewed plan. The
plan and durable run artifact bind exact same-environment Renart-observed writer
evidence, and execution revalidates/captures that writer immediately before the
consumer task. Snapshot-backed and scheduled execution remain explicitly
blocked pending Phase 3.

### Phase 3 — deployments and schedules

1. [x] Persist/validate deployment dependency manifests.
2. [x] Resolve prerequisites against same-environment producer deployments.
3. Add durable waiting occurrences, event-driven wakeup, timeout, and UI.
4. Cover mixed schedule intervals, restart recovery, redeploy while waiting,
   schedule promotion, and snapshot/fact retention.

### Phase 4 — reach

- explicit operator policy to trigger a missing upstream run;
- environment mapping when producer and consumer environment names differ;
- cross-repository resolution if Renart ever gains an explicit trusted registry;
- CLI import of externally observed Bruin run facts;
- richer URI suggestions from physical targets.

## Test matrix

- parser/round-trip: scalar asset, URI object, full/symbolic, incomplete YAML;
- resolver: duplicate URI, unresolved URI, same-name assets in two pipelines,
  full cycle, symbolic cycle, producer rename with stable URI;
- typecheck/LSP: workspace resolution and correct asset-addressed diagnostics;
- fingerprint: producer edit cascades, symbolic edit does not, external stale
  read never records a fresh consumer;
- target evidence: active/dirty/ambiguous producer blocks; exact writer passes;
- coverage: same schedule, mixed schedule union, partial gap, non-window marker;
- planning: manual blocker, no hidden producer units, immutable prerequisite;
- deployment: ownership movement invalidates downstream, same-pipeline rename
  resolves after redeploy, pin promotion is atomic;
- scheduler: wait/wakeup/timeout, restart while waiting, duplicate River signal,
  producer failure followed by success, no pipeline slot held while waiting;
- live E2E: author in the guided form, see a cross-pipeline Catalog edge, deploy
  both pipelines, observe waiting, run producer, and see downstream admission.

## Accepted decisions

1. **URI authoring:** use only an exact physical URI or an explicit user value.
   Do not invent a `renart://` fallback.
2. **Unresolved symbolic edges:** emit a warning and allow deployment because
   the edge cannot affect data readiness.
3. **Wait deadline:** use a 12-hour project default, eventually configurable
   within 5 minutes–7 days. Per-edge overrides are deferred.
4. **Producer schedule requirement:** accept a current manual Renart run fact
   that matches the deployed producer. A paused or missing producer schedule is
   an actionable warning, not an automatic rejection.
5. **Observed facts:** prerequisites use **only Renart-observed freshness and
   materialization facts**. Do not infer freshness from table existence and do
   not import separate Bruin CLI runs in the first version; Renart-only runtime
   guarantees require the Renart CLI/runtime.
6. **Redeploy while waiting:** freeze the producer snapshot selected when the
   occurrence first waits. Future occurrences use the new pin.
7. **Environment mapping:** require the same Renart environment name in the
   first version; defer explicit mappings until a concrete need appears.
8. **Sensor producers:** disallow them in the initial coverage-based contract.
   A future event-prerequisite feature can model sensor success separately.

## Completion

When all three core phases ship, fold the graph/validation contract into
`architecture/backend.md` and `architecture/sql-lsp.md`, the freshness and
schedule behavior into `architecture/staleness.md`, and the guided authoring UI
into `architecture/asset-editing.md`; then delete this plan.
