# Remote-table intelligence and external source nodes

Status: catalog overlay, warnings, external canvas nodes, and reviewed import
shipped — catalog-ready invalidation and live coverage remain

## Goal

Surface warehouse relations that have no backing Bruin asset everywhere users
need them:

- relation and column completion in SQL editors;
- presence-aware diagnostics without treating an unavailable catalog as proof
  that a table is missing;
- automatically inferred external-source nodes on the asset canvas; and
- a type-check resolution that imports the relation as a version-controlled
  Bruin source-placeholder asset.

Defined assets always take precedence over live catalog relations. Discovery
is optional and must never block or disable asset-only LSP behavior.

## Current state (August 2026)

### What is already shipped

- `/api/sql/databases`, `/api/sql/tables`, and `/api/sql/table-columns` perform live
  discovery for an explicit connection/environment.
- `sqlDiscoveryTablesAtom` and `sqlDiscoveryColumnsAtom` deduplicate in-flight
  requests and cache successful results for the browser session.
- `use-sql-lsp.ts` supplements LSP results client-side:
  - remote tables are added in a narrow bare `FROM`/`JOIN` completion context,
    ranked below known schema tables;
  - after `table.`, live columns are fetched when the LSP has no local-scope
    column result;
  - asset/schema suggestions win deduplication.
- The older parse-context provider also consumes the same discovery cache on
  surfaces that still enable it.
- Every SQL LSP request carries an effective or explicitly selected connection
  and the selected environment.
- The canonical LSP graph is revision-cached and intentionally contains only
  deterministic workspace/notebook relations (plus the explicitly gated
  DuckDB local-file layer).
- A process-local server catalog cache discovers remote databases/tables in
  single-flight background work, scoped by connection and environment. It uses
  a 60-second TTL, five-second timeout, and caps of 32 databases, 2,000
  relations, and 512 columns per relation. Failed refreshes retain stale
  positive observations, while failed or partial catalog/column refreshes are
  retry-limited for ten seconds.
- Each HTTP LSP request clones relation/schema slices and overlays the immediate
  snapshot with `remote_catalog` provenance. Authored exact-name collisions
  win; remote relations rank below authored relations; ambiguous short names
  require qualification; known columns feed diagnostics/completion/hover and
  unknown schemas remain non-authoritative.
- Column discovery is lazy for relations referenced by the current document.
  The stdio LSP has no provider and remains offline.
- Positively observed remote references produce a warning in Monaco and in the
  interactive pipeline type-check report. Type-check consumes only an existing
  snapshot and never initiates warehouse I/O; CLI validation stays offline and
  deterministic.

### Gaps

- The first remote completion awaits live discovery and can feel stalled.
- The browser session cache still has no TTL because it remains as a
  transitional cold-first-completion fallback. Remove it only after live parity
  proves the server refresh reliably wakes every editor surface.
- A cold LSP request returns asset-only results immediately; no catalog-ready
  event currently asks Monaco to rerun an unchanged diagnostic request.
- Remote navigation has no authored definition target, and observation age is
  not yet shown in completion/hover detail.
- External nodes currently appear only after an interactive pipeline type-check
  has consumed a positive cached observation. A catalog refresh does not yet
  rerun that report or unchanged Monaco diagnostics automatically.
- Observation timestamps are carried in the report but are not yet presented
  in the canvas node or import review.
- Stdio LSP has no connection manager and must remain deterministic/offline.

## Shipped foundation and proposed product layers

### 1. Optional non-blocking catalog provider — shipped

Introduce a provider independent from the LSP engine:

```go
type RemoteCatalogScope struct {
    Connection  string
    Environment string
    Database    string
}

type RemoteCatalogRelation struct {
    QualifiedName string
    Schema        string
    Name          string
    Columns       []SQLColumn
    ColumnsKnown  bool
}

type RemoteCatalogSnapshot struct {
    Relations  []RemoteRelation
    Complete   bool
    ObservedAt time.Time
    Stale      bool
}

type RemoteCatalogProvider interface {
    Snapshot(scope RemoteCatalogScope) RemoteCatalogSnapshot // immediate, no I/O
    Refresh(ctx context.Context, scope RemoteCatalogScope)    // schedules background work
    RefreshColumns(ctx context.Context, scope RemoteCatalogScope, relation string)
}
```

The implementation reuses the existing backend connection discovery, keyed by
connection/environment/database. It returns cached-or-empty immediately and
starts at most one bounded refresh for a missing/stale scope. Failures retain
the previous snapshot and retries are rate-limited. Entries have a TTL, maximum
relation/column counts, and explicit completeness; a partial catalog can prove
presence but never absence.

No provider means current asset-only behavior. The stdio server uses no
provider unless a future host explicitly supplies one. The first version spans
all discovered databases for the connection and uses fixed safe caps rather
than prefix pagination.

### 2. Per-request graph overlay — shipped, cleanup remaining

After cloning the revision-cached canonical graph, `SQLLSPService` resolves the
request's effective connection/environment and overlays the latest catalog
snapshot:

1. Skip any remote relation colliding case-insensitively with a logical asset
   relation or query-local relation.
2. Tag nodes with `Provenance.Provider = remote_catalog`, the safe connection
   name, and low confidence; observation age is still a follow-up.
3. Rank remote relations below assets but above generic keywords.
4. Apply known remote columns with live/low-confidence provenance. Unknown
   columns keep the relation resolvable without enabling unresolved-column
   checks.

This makes partial prefixes, `FROM schema.`, aliases, hover, and semantic tokens
use one relation model. Once live parity is complete, remove both client-side
remote table and column merge branches from `use-sql-lsp.ts`; the browser keeps
the discovery APIs for catalog/settings/object pickers.

Only positive observations affect diagnostics. A relation found in a snapshot
may satisfy `unresolved-relation`; a relation absent from a stale, partial, or
unavailable snapshot must not create a “table does not exist” error. LSP detail
and hover should label live catalog evidence and its age.

### 3. External source nodes — shipped

The interactive pipeline type-check response carries an ephemeral
external-relation DTO rather than pretending a remote table is an authored
asset:

```text
(connection, environment, qualified relation)
    -> referenced by asset IDs
    -> latest catalog observation/schema confidence
    -> optional imported asset ID
```

It is built from SQL relation references joined with positive provider
observations. The Build canvas renders a distinct read-only source node and
edges it to every referencing asset. It is derived from the latest report, is
not written to Jotai as authoritative workspace state, does not participate in
execution selection, and disappears when the reference or observation
disappears. Local assets still win collisions.

The node exposes its connection and physical relation plus an “Import as asset”
action. Import writes reconcile through the ordinary workspace SSE event. A
future catalog-ready event should rerun the report without polling; observation
age display remains follow-up work.

### 4. Type-check warning and import resolution — shipped

When a SQL reference is confirmed by the provider but has no asset, add a
non-blocking warning such as:

```text
External relation public.events exists on warehouse-prod but is not represented by an asset.
```

This is a catalog/source-governance warning, not an unresolved-relation error.
It should be emitted only from an already available snapshot; type-check must
not initiate warehouse I/O. Offline/CLI results therefore remain deterministic
and simply omit this live enrichment.

The structured “Import source asset” resolution uses Renart's native
`HybridBruinExecutor.ImportDatabase`, which already calls Bruin connection discovery,
supports a selected table, and writes a source-placeholder asset with columns
unless `DisableColumns` is set. Reuse that service behind a scoped preview and
confirm endpoint; do not shell out or reimplement warehouse-specific import
rules. The endpoint must:

- pin connection, environment, schema, and one table;
- preview the proposed file/name/columns and reject collisions;
- write through the Go server under the workspace lease;
- emit the ordinary workspace SSE update; and
- rerun type-check so the warning resolves and the ephemeral node becomes an
  authored asset.

The type-check resolution payload now distinguishes semantic asset
transactions from typed server actions; file creation is never encoded as an
`AssetTransaction`. The preview/import endpoints rerun interactive type-check
to bind the request to a still-referenced positive observation, reject logical
name and file-path collisions, and repeat discovery on confirm. Columns are
enabled by default and can be disabled in the review dialog.

Bruin's importer currently proposes the physical `schema.table` as the asset
name. A different logical name can use Bruin's native explicit `name:` while
the generated file stays in a separately chosen folder; see
`asset-name-path-independence.md`. This does not create a second physical
output identity.

## Resilience and security

- nil provider, cold cache, timeout, auth failure, or unsupported connector:
  asset-only LSP continues unchanged;
- one refresh per scope, short timeout, bounded entries, cancellation on
  server shutdown, and no unbounded goroutines;
- connection secrets stay backend-only and never enter graph provenance;
- environment changes use a distinct cache scope and invalidate visible
  external nodes;
- a stale snapshot may support completion with an age label but never proves
  non-existence;
- imported files require normal conflict checks and protected-environment
  policy; discovery itself is read-only.

## Validation

- provider unit tests: TTL, single-flight, timeout/error retention, bounds,
  completeness, environment/database separation;
- LSP tests: asset collision precedence, ranking, partial prefix,
  `FROM schema.`, alias columns, unknown-column suppression, nil provider;
- freshness of overlay: cached base graph is never mutated across requests;
- canvas tests: one external node shared by multiple consumers, removal after
  import/reference deletion, environment switch (live coverage remains);
- import tests: exact proposed Bruin source asset, default columns, explicit
  no-columns mode, collision refusal, and stale observation refusal are covered;
  live SSE reconciliation and warning removal remain;
- live DuckDB/Postgres test with a table created outside the pipeline, plus one
  credential failure proving asset completions stay responsive.

## Rollout

1. **Done:** add the bounded provider/cache and tests.
2. **Mostly done:** overlay remote relations/columns per request and emit live
   warning diagnostics. Add a
   catalog-ready invalidation/live test, then remove client-side merge paths.
3. **Done:** add the pipeline-scoped external-relation report DTO and read-only
   canvas nodes.
4. **Done:** add a scoped preview/confirm wrapper around the native single-table
   importer and a structured type-check resolution, importing columns by
   default.
5. Add catalog-ready invalidation and live tests, then remove the transitional
   client-side completion merge and delete this plan.

## Decisions for the remaining phases

The catalog foundation uses the accepted starting defaults: 60-second TTL,
five-second timeout, stale-positive retention, explicit caps, and all discovered
databases within the cap. Prefix pagination remains follow-up work.

1. **Decided:** external nodes appear only after a positive catalog observation
   in the first release; SQL text alone never creates an unverified node.
2. **Decided:** “Import source asset” imports columns by default, with a review
   preview and a no-columns escape hatch.
