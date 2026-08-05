# Remote-table intelligence and external source nodes

Status: partial client support shipped — server/LSP integration proposed

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

- `/api/sql/databases`, `/api/sql/tables`, and `/api/sql/columns` perform live
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
- Every SQL LSP request already carries an effective or explicitly selected
  connection. The workspace carries the selected environment.
- The canonical LSP graph is revision-cached and intentionally contains only
  deterministic workspace/notebook relations (plus the explicitly gated
  DuckDB local-file layer). Pure remote warehouse tables are not graph nodes.

### Gaps

- The first remote completion awaits live discovery and can feel stalled.
- The session cache has no TTL, invalidation, bounded catalog size, or stale
  provenance.
- Partial prefixes and `FROM schema.` do not consistently use remote tables;
  completion, hover, diagnostics, and navigation see different relation sets.
- Remote columns are a client-only fallback, so SQL validation cannot use a
  confirmed live schema.
- The canvas and type-check report have no external-relation model or import
  action.
- Stdio LSP has no connection manager and must remain deterministic/offline.

## Proposed architecture

### 1. Optional non-blocking catalog provider

Introduce a provider independent from the LSP engine:

```go
type CatalogScope struct {
    Connection  string
    Environment string
    Database    string
}

type RemoteRelation struct {
    QualifiedName string
    Schema        string
    Name          string
    Columns       []RemoteColumn
    ColumnsKnown  bool
}

type RemoteCatalogSnapshot struct {
    Relations  []RemoteRelation
    Complete   bool
    ObservedAt time.Time
    Stale      bool
}

type RemoteCatalogProvider interface {
    Snapshot(scope CatalogScope) RemoteCatalogSnapshot // immediate, no I/O
    Refresh(scope CatalogScope)                           // single-flight background work
}
```

The implementation reuses the existing backend connection discovery, keyed by
connection/environment/database. It returns cached-or-empty immediately and
starts at most one bounded refresh for a missing/stale scope. Failures retain
the previous snapshot and are rate-limited in logs. Entries have a TTL, maximum
relation/column counts, and explicit completeness; a partial catalog can prove
presence but never absence.

No provider means current asset-only behavior. The stdio server uses no
provider unless a future host explicitly supplies one.

### 2. Per-request graph overlay

After cloning the revision-cached canonical graph, `SQLLSPService` resolves the
request's effective connection/environment and overlays the latest catalog
snapshot:

1. Skip any remote relation colliding case-insensitively with a logical asset
   relation or query-local relation.
2. Tag nodes `Provenance.Kind = remote_catalog` plus connection, environment,
   observation time, and schema completeness.
3. Rank remote relations below assets but above generic keywords.
4. Apply known remote columns with live/low-confidence provenance. Unknown
   columns keep the relation resolvable without enabling unresolved-column
   checks.

This makes partial prefixes, `FROM schema.`, aliases, hover, and semantic tokens
use one relation model. Once the overlay is complete, remove both client-side
remote table and column merge branches from `use-sql-lsp.ts`; the browser keeps
the discovery APIs for catalog/settings/object pickers.

Only positive observations affect diagnostics. A relation found in a snapshot
may satisfy `unresolved-relation`; a relation absent from a stale, partial, or
unavailable snapshot must not create a “table does not exist” error. LSP detail
and hover should label live catalog evidence and its age.

### 3. External source nodes

Add a workspace-level, ephemeral external-relation DTO rather than pretending a
remote table is an authored asset:

```text
(connection, environment, qualified relation)
    -> referenced by asset IDs
    -> latest catalog observation/schema confidence
    -> optional imported asset ID
```

Build it from unresolved SQL relation references joined with positive provider
observations. The canvas renders a distinct read-only source node and edges it
to every referencing asset. It is not written to Jotai as authoritative state,
does not participate in execution selection, and disappears when the reference
or observation disappears. Local assets still win collisions.

The node should expose connection, physical relation, observation age, and an
“Import as asset” action. Catalog state changes arrive through normal workspace
SSE reconciliation or a dedicated event, never polling.

### 4. Type-check warning and import resolution

When a SQL reference is confirmed by the provider but has no asset, add a
non-blocking warning such as:

```text
External relation public.events exists on warehouse-prod but is not represented by an asset.
```

This is a catalog/source-governance warning, not an unresolved-relation error.
It should be emitted only from an already available snapshot; type-check must
not initiate warehouse I/O. Offline/CLI results therefore remain deterministic
and simply omit this live enrichment.

Offer a structured resolution, “Import source asset”. Bruin already provides
`bruin import database --connection ... --schema ...` and generates
source-placeholder assets with optional columns. Extract that logic behind a
Bruin library API and call it from a scoped Renart endpoint; do not shell out or
reimplement warehouse-specific import rules. The endpoint must:

- pin connection, environment, schema, and one table;
- preview the proposed file/name/columns and reject collisions;
- write through the Go server under the workspace lease;
- emit the ordinary workspace SSE update; and
- rerun type-check so the warning resolves and the ephemeral node becomes an
  authored asset.

The current type-check resolution payload supports semantic edits to one asset.
Extend it with a separately typed server action before adding import; do not
encode file creation as an `AssetTransaction`.

Bruin's importer currently names a placeholder after the physical
`schema.table`. Supporting a different logical name depends on
`physical-output-names.md` and should not be improvised here.

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
  import/reference deletion, environment switch;
- import tests: exact proposed Bruin source asset, columns, collision refusal,
  SSE reconciliation, and warning removal;
- live DuckDB/Postgres test with a table created outside the pipeline, plus one
  credential failure proving asset completions stay responsive.

## Rollout

1. Add the provider/cache and observability with no LSP consumers.
2. Overlay remote relations/columns per request and remove client-side merge
   paths after parity tests pass.
3. Add the external-relation workspace DTO and canvas nodes.
4. Extract Bruin's single-table import library API; add preview/confirm endpoint
   and structured type-check resolution.
5. Add live tests, document shipped behavior, fold it into
   `architecture/sql-lsp.md`, and delete this plan.

## Decisions required before implementation

1. Catalog TTL, result caps, and whether stale entries remain visible by
   default. Suggested starting point: 60 seconds, 5-second refresh timeout, and
   an explicit cap with prefix-filtered follow-up discovery.
2. Default database scope for engines exposing multiple databases/catalogs.
3. Whether external nodes appear immediately from SQL text as “unverified” or
   only after a positive catalog observation. Recommended: positive observation
   only for the first release.
4. Whether “Import source asset” imports columns by default. Recommended: yes,
   with a review preview and a no-columns escape hatch.
