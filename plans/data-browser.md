# Data Browser

Status: the first production MVP slice is implemented on the Workbench feature
branch as of 2026-09-02. It includes the revision-bound discovery service,
`/data`, the Build overlay, point-of-need connection creation, project-local
files, and explicit bounded Preview/Columns views. Live adapter coverage,
reviewed handoffs, Usage, durable discovery, and object stores remain open.

Related plans:

- [navigation-workbench-migration.md](navigation-workbench-migration.md) owns
  the surrounding application shell and ordered release train;
- [navigation-information-architecture-mocks.md](navigation-information-architecture-mocks.md)
  records the broader design study and why only the Workbench rail proceeds to
  production;
- [object-storage-assets.md](object-storage-assets.md) owns the deeper asset and
  execution semantics for object stores.

### Implemented checkpoint

The first slice deliberately stops short of pretending the complete plan has
shipped:

- configured query-capable connections are exposed as name/type/capability
  summaries without credential values;
- `/api/data-browser/connections`, lazy `children`, object description, and a
  server-built preview endpoint use revision-bound object references;
- local CSV, Parquet, JSON, JSONL, and NDJSON discovery is confined to visible
  paths below the project root and rejects traversal, symlink escapes, hidden
  paths, and generated dependency directories;
- `/data` and the Build overlay share one React controller, the existing
  connection dialog and connection icons, and the shared virtual result grid;
- the mode-aware mobile tools now use the shadcn `Tabs` primitives below the
  global header; the drawer contains only the selected contextual hierarchy;
- rows remain unloaded until **Preview rows** is invoked, then the server caps
  the request at 200 rows and reports truncation and elapsed time.

The current hierarchy adapts Renart's existing SQL discovery service. It has
been exercised against DuckDB and project files; PostgreSQL and a true
three-level warehouse still need live contract coverage before a broader
support claim. Search is currently scoped to the visible connection or
hierarchy level. Usage matching, Ad-hoc/Notebook/Load/import handoffs,
cancellation, cached partial states, pagination, and remote search remain later
phases below.

## 1. Outcome

Renart should provide one project-scoped Data Browser where a user can:

1. see the warehouse connections and local project files available in the
   selected environment;
2. add a supported warehouse or register a supported local file at the point
   of need;
3. navigate source → source-native namespace → object without fetching rows;
4. search already loaded metadata, including columns, with honest scope;
5. inspect an object's sample rows, columns, types, observed metadata, and known
   Renart usage;
6. open the object in Ad-hoc Query or a notebook;
7. use it as an input to a Load asset;
8. turn an observed relation into a reviewed, Git-backed source asset;
9. understand whether information is live, cached, partial, stale, empty, or
   unavailable;
10. do all of this without exposing connection secrets or confusing warehouse
    observations with authored pipeline truth.

The canonical full-page route is `/data`. Build and Explore expose contextual
entry points into the same controller and state. In the approved Workbench
shell, Build opens the browser in the bounded overlay so the current canvas and
editor state remain mounted. Explore can navigate to the full route.

## 2. Product principles

### 2.1 Browse first, query deliberately

Expanding a connection, database, schema, or object loads metadata only. Row
access starts only through an explicit Preview or Query action. This keeps
remote catalog browsing inexpensive and makes the boundary between metadata
and data access legible.

### 2.2 Catalog and Data Browser are different trust domains

- **Workspace Catalog:** authored Renart assets and lineage from project files.
- **Data Browser:** positively observed warehouse, object-store, or
  project-scoped local-file objects.
- **Linked object:** an observed object whose canonical physical identity maps
  to a managed Renart asset.
- **Imported object:** an observed object that became an authored asset only
  after the user reviewed and confirmed a repository change.

Previewing, pinning, querying, or adding an object to a notebook must never
silently create an asset. Absence from a partial or stale observation must
never be interpreted as proof that an object does not exist.

### 2.3 Progressive disclosure over a giant tree

The browser starts with connections plus a small pinned/recent section. Once a
connection is selected, the wide sidebar replaces the overview with that
connection's hierarchy and a Back action. It does not render every connection
and every object in one tree.

### 2.4 One object surface, capability-dependent actions

Every selected object uses the same object-detail composition:

- a compact identity header;
- kind, authored/observed status, and observation scope;
- small operational facts such as approximate rows, size, and observed time;
- **Data preview**, **Columns**, and **Usage** tabs;
- primary Query and Import/Open Asset actions;
- less frequent actions in an overflow menu.

Unsupported actions are absent or disabled with a reason. They do not fail only
after the user has opened a workflow.

### 2.5 Dense workbench UI, not a dashboard of cards

Use hierarchy, compact metadata strips, tables, tabs, and status notices.
Reserve cards for a genuinely independent object or decision. The preview
dialog should size itself to its content and viewport rather than placing a
table at the top of a tall, mostly empty modal.

## 3. Current Renart baseline

The first implementation should reuse existing behavior instead of inventing a
parallel system.

| Concern                  | Existing owner                                           | Reusable capability                                                                  | Gap                                                                                  |
| ------------------------ | -------------------------------------------------------- | ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ |
| SQL discovery client     | `web/lib/api-sql-discovery.ts`                           | databases, tables, columns, bounded query                                            | three calls do not expose one revisioned browser state                               |
| SQL discovery service    | `internal/web/service/sql.go`                            | runtime connection resolution and warehouse-aware discovery                          | preview still accepts frontend SQL rather than an object identity                    |
| Remote metadata cache    | `internal/web/service/remote_catalog.go`                 | bounded single-flight refresh, 60-second TTL, positive observations, SSE ready event | process-local snapshot lacks browser-facing state/provenance and durable preferences |
| Connection setup         | `web/components/app/workspace-connection-dialog.tsx`     | server-described fields, write-only secrets, Verify and Create                       | browser must return to and select the newly created connection                       |
| Local dataset sources    | seed/load editors, notebook file sources, `workspacefs`  | CSV, Parquet, JSON/JSONL paths and project-root filesystem boundary                  | no shared lazy file index, schema description, or Data Browser preview contract      |
| External relation import | `web/components/app/external-relation-import-dialog.tsx` | reviewed source-asset preview and import                                             | needs a canonical browser object identity as its entry point                         |
| Connection visuals       | shared `ConnectionTypeIcon`                              | recognizable database/warehouse marks                                                | object-store and unsupported-capability treatments need coverage                     |
| Results                  | shared result-table primitives                           | selection, keyboard navigation, copy, virtualization                                 | preview needs a read-only bounded mode and compact empty/error states                |
| Workbench shell          | approved rail/context/overlay structure                  | Build context remains mounted; mobile drawer exists                                  | `/data`, controller, and production context adapter do not exist                     |

The current mock goes further than production in presentation: it includes
connection onboarding, pinned/recent objects, two-level connection navigation,
object-kind filtering, loaded-metadata search, explicit preview, authored versus
observed status, Columns and Usage tabs, and partial/cache/error examples. Those
are design requirements, not evidence that the backend exists.

## 4. Target user journeys

### 4.1 First open

1. Open Data Browser from the Build rail/tool tab or Explore context.
2. See configured connections plus the project-local file source for the
   current environment.
3. See at most six safe pinned/recent object identities above them.
4. Search across metadata already loaded in this project session.
5. If no useful connection exists, choose one of the common connection types
   or All types.

The browser must say when search covers only loaded metadata. A remote search
option appears only when an adapter can provide it.

### 4.2 Add a warehouse

1. Choose a database or warehouse type using the shared connection icon.
2. Reuse `WorkspaceConnectionDialog` with the type preselected.
3. Enter non-secret settings and write-only secrets.
4. Verify the connection explicitly.
5. Create it.
6. Return to Data Browser with that connection selected and discovery started.

Creation is environment-scoped. The browser never receives saved credentials.
If verification succeeds but the later write fails, retain the form and show
the write error rather than asking for credentials again.

### 4.3 Add or browse a local file

1. Select **Project files** to browse only the active project root and any
   explicitly configured read-only roots.
2. Navigate folder → file; supported tabular files show format, size, modified
   time, inferred columns, and known Renart usage.
3. Choose **Add local file** to select or enter a path within an allowed root.
4. Resolve and validate the path server-side before returning a file identity.
5. Preview directly, add it to a notebook, or open a reviewed Seed/Load source
   workflow; browsing alone never writes a project definition.

CSV, Parquet, JSON, and JSONL are the first tabular formats. Unsupported files
may appear as files with identity metadata but do not advertise schema,
preview, Query, or import capabilities. The UI says **Folders** and **Files**
for this source instead of pretending they are schemas and tables.

### 4.4 Browse a source

1. Select a connection or local-file source; the context sidebar becomes its
   detail view.
2. Show its environment, access mode, discovery state, scope, refresh action,
   object counts, and hierarchy.
3. Expand only the requested metadata level.
4. Distinguish tables, views, materialized views, external datasets, and local
   files.
5. Show a small Asset marker only where physical identity mapping is positive.

For engines that expose both catalogs and databases, preserve the adapter's
real hierarchy labels. Do not force every system into a misleading
`database.schema.table` vocabulary; the API carries ordered namespace segments
and display labels.

### 4.5 Search

Search has two clearly named scopes:

- **Loaded metadata:** immediate client-side filtering over loaded connection,
  namespace, object, column, type, description, and tag fields.
- **Search this source:** an explicit server request when the adapter supports
  bounded remote metadata search.

Search results include the qualified identity, connection icon, object kind,
and column count. Selecting one hydrates the hierarchy path and opens that
object. Results from an older connection revision are discarded.

### 4.6 Inspect an object

The preview opens as a replaceable overlay or full-page detail, not as another
nested card. Its header contains:

- connection / namespace breadcrumb;
- object name and kind;
- Managed by Renart or Observed only;
- a short description when available;
- compact row estimate, size, observed time, and read-only status;
- Query and Import source asset or Open asset;
- overflow actions for pin, copy qualified name, copy SELECT, notebook, and
  Load input.

The tabs behave as follows:

- **Data preview:** no query until this tab is opened or refreshed; at most 100
  rows initially; result reports elapsed time and truncation.
- **Columns:** name, warehouse type, normalized type when known, nullability,
  keys, tags, description, and a local filter.
- **Usage:** authored-asset match and known workspace consumers from assets,
  notebooks, dashboards, and reports. This is Renart-known usage, not a claim of
  complete warehouse query history.

The dialog uses a content-aware height capped by the viewport. Short results do
not leave a large blank lower half. Long previews scroll inside the content
region while the identity and tab bar remain stable.

### 4.7 Act on an object

| Action              | SQL relation | View/materialized view | Local tabular file      | Object-store dataset    | Requirement                               |
| ------------------- | ------------ | ---------------------- | ----------------------- | ----------------------- | ----------------------------------------- |
| Preview             | yes          | yes                    | supported format        | adapter-dependent       | explicit read-only request                |
| Query               | yes          | yes                    | DuckDB-capable format   | only with query adapter | open Ad-hoc with server reference         |
| Import source asset | yes          | yes                    | Seed/Load review        | adapter-dependent       | reviewed repository mutation              |
| Open asset          | linked only  | linked only            | linked only             | linked only             | positive authored match                   |
| Add to notebook     | yes          | yes                    | yes for supported files | adapter-dependent       | connection access approval remains intact |
| Use as Load input   | yes          | yes                    | yes for supported files | yes where supported     | destination/source capability check       |
| Copy reference      | yes          | yes                    | project-relative path   | URI/path equivalent     | server-provided display/reference text    |

### 4.8 Mobile

- The mode-aware **Data** tool is visible in the mobile tool-tab strip directly
  below the global header; it is not hidden inside the navigation drawer.
- Data Browser uses the one contextual drawer or a route/full-height overlay as
  a content host. The drawer never repeats the tool picker.
- Selecting a connection replaces, rather than nests beside, the connection
  list.
- Selecting an object closes the drawer and opens a full-height bottom sheet or
  route-level detail.
- Primary actions remain visible; overflow actions stay in one menu.
- Tables reuse the touch-capable result grid and do not force the whole page to
  horizontal-scroll.
- Back first moves object → connection → connections; only then closes the
  drawer.

## 5. Identity and data contract

### 5.1 Server-issued object identity

Frontend code must not identify an object with only a connection display name
and `schema.table`. Use an opaque `object_id` issued for a discovery revision,
plus display-safe fields:

```text
DataObjectRef
  object_id
  connection_id
  connection_config_revision
  environment
  namespace[]        # ordered catalog/database/schema/prefix/folder segments
  name
  kind
  reference_text     # safe, engine-aware display/query reference
  discovery_revision
```

The server resolves `object_id` back to its exact connection and namespace.
Changing or deleting a connection invalidates identities from the old config
revision. Recent and pinned items store only this safe identity and a label;
they never store SQL or credentials.

### 5.2 Browser response

```text
DataBrowserConnection
  id, name, type, environment
  config_revision
  capabilities
  discovery
    status
    revision
    observed_at
    last_success_at
    provenance       # live | cache | mixed
    complete
    scope
    warning?
  roots[]            # lazy namespace nodes

DataBrowserNode
  id, parent_id
  node_type          # namespace | object
  label
  namespace_kind?
  object_kind?
  has_children
  authored_match?
  summary?
  capabilities?
```

Discovery status is one of:

```text
idle | discovering | ready | refreshing | partial |
error-with-cache | error-empty | empty
```

`complete` describes only the requested scope. A successful response with
permission-limited or capped findings is `partial`, not `ready`.

### 5.3 Capabilities

Capabilities are facts returned by the backend adapter, not frontend lists:

```text
list_namespaces
list_objects
describe_columns
preview_rows
query
remote_search
row_estimate
object_size
keys
descriptions
import_source_asset
notebook_source
load_source
```

The frontend derives action visibility from these flags and the current
environment policy.

## 6. HTTP API

### 6.1 MVP endpoints

```text
GET  /api/data-browser/connections?environment=...
GET  /api/data-browser/connections/{connection_id}/children
     ?environment=...&parent_id=...&revision=...
GET  /api/data-browser/objects/{object_id}
     ?environment=...&revision=...
POST /api/data-browser/preview
POST /api/data-browser/connections/{connection_id}/refresh
```

`children` returns one hierarchy level and a continuation token when needed.
The root request can internally adapt the existing databases/tables operations;
the web client should not orchestrate engine-specific fan-out.

`GET .../objects/{object_id}` loads columns and object metadata only. It does
not return rows.

### 6.2 Preview request

```json
{
  "object_id": "opaque",
  "environment": "production",
  "discovery_revision": "42",
  "limit": 100
}
```

The server:

1. resolves the object and current connection configuration;
2. rejects stale or mismatched identities with a refreshable conflict;
3. constructs engine-aware quoted SQL or invokes the adapter's preview method;
4. applies a hard maximum row limit and cancellation deadline;
5. runs through the existing operation metadata and cancellation path;
6. returns the shared query result envelope plus source identity, elapsed time,
   truncation, and observation time.

The browser does not submit arbitrary SQL to this endpoint. Ad-hoc Query remains
the explicit arbitrary-query surface.

### 6.3 Search endpoint, after MVP evidence

```text
GET /api/data-browser/connections/{connection_id}/search
    ?environment=...&query=...&types=...&limit=...&cursor=...
```

Only adapters with bounded metadata search expose it. Otherwise the UI keeps
local loaded-metadata search and says so.

### 6.4 Events

Use targeted SSE events rather than polling:

```text
data-browser.discovery.updated
  connection_id
  environment
  config_revision
  discovery_revision
  status
```

The event contains no catalog payload. The client refetches only if its selected
scope matches and ignores older revisions.

## 7. Backend architecture

### 7.1 Service boundary

Add a `DataBrowserService` between HTTP and source adapters. It owns:

- connection/environment resolution;
- opaque object IDs and revision validation;
- hierarchy normalization;
- capability aggregation;
- refresh/cache state;
- safe preview;
- authored-object matching;
- operation metadata and targeted events.

It should reuse `SQLService` discovery helpers initially, then move shared
engine-aware logic into small internal helpers. Do not make `SQLService`, the
LSP remote catalog, and Data Browser three independent warehouse crawlers.

### 7.2 Adapter interface

Use a narrow internal adapter selected from the resolved Bruin connection:

```go
type DataBrowserAdapter interface {
    Capabilities() DataBrowserCapabilities
    RootNodes(context.Context, Scope) (Page[Node], error)
    ChildNodes(context.Context, Scope, NodeRef) (Page[Node], error)
    Describe(context.Context, ObjectRef) (ObjectDetail, error)
    Preview(context.Context, ObjectRef, int) (QueryResult, error)
}
```

Optional behavior is represented by capabilities and optional internal
interfaces, not dummy results. SQL engines can initially adapt
`GetDatabases`, `GetTablesWithSchemas`, and the existing column inference.
Local files use a separate adapter from the first MVP; object stores get their
own adapter later. `LocalFileBrowserAdapter` lists only canonical paths under
the active project root or an explicit allowlist and returns file capabilities
per format. It does not reuse arbitrary SQL text as a filesystem API.

### 7.3 Discovery cache

Reuse the useful invariants of `RemoteCatalogCache`:

- single-flight refresh;
- bounded deadlines and fan-out;
- positive observations;
- last-known-good data remains visible on refresh error;
- configurable database, relation, and column caps;
- no secrets in keys or snapshots.

Expose a browser-oriented snapshot with revision, provenance, requested scope,
partial reasons, and error-with-cache. Keep the process cache for MVP. Durable
cross-restart metadata is optional later and must have an explicit retention
and invalidation model before introduction.

The LSP and Data Browser should consume the same normalized observations. A
successful browser discovery can warm editor intelligence without another
warehouse round trip, and an LSP observation can seed a partial browser node.

### 7.4 Authored matching and usage

Build one canonical physical-relation identity per engine from:

- connection configuration identity;
- environment;
- ordered namespace;
- object name and kind.

Match this against pipeline asset materialization targets. Return positive
matches only; ambiguous matches are surfaced as a warning and remain observed
only. Known usage comes from Renart's artifact index and lineage graph:

- managed assets and their dependents;
- notebook dataset/source definitions;
- dashboard and report datasets;
- Load asset sources.

Warehouse query-history lineage is not part of the first implementation.

## 8. Frontend architecture

### 8.1 Route and controller

Add `/data` as a real lazy route after the complete MVP is functional. A
project-scoped `useDataBrowserController` owns:

- environment and selected connection;
- hierarchy expansion and per-node loading;
- discovery revision reconciliation;
- selected object/detail/preview state;
- loaded-metadata index;
- session recent/pinned identities;
- cancellation and stale-response protection;
- actions that hand off to Ad-hoc, notebooks, import, assets, and Load.

Build overlay and Explore route consume the same controller. They do not fork
their own fetch/cache logic.

### 8.2 Component boundaries

```text
DataBrowserRoot
  DataBrowserConnectionList
  DataBrowserRecentObjects
  AddSourceLauncher

DataBrowserConnectionPane
  DiscoveryStatusNotice
  LoadedMetadataSearch
  ObjectKindFilter
  DataBrowserHierarchy

DataObjectDetail
  DataObjectHeader
  DataObjectMetadataStrip
  DataPreviewTab
  DataColumnsTab
  DataUsageTab
```

Reuse:

- `WorkspaceConnectionDialog` for creation;
- `ExternalRelationImportDialog` for reviewed import;
- shared `ConnectionTypeIcon`;
- the shared virtual/selectable result table in read-only preview mode;
- standard operation/error presentation and environment selector;
- Workbench overlay and mobile sheet primitives.

No domain request should live inside a presentational tree row.

### 8.3 Session preferences

Store a versioned, project-scoped, capped list in `sessionStorage`:

- up to six recent objects;
- up to twelve pinned objects;
- last selected connection and expanded namespaces.

Restore only identities whose connection config revision still matches. Pinned
objects are UI convenience, not project configuration, and must not be written
to Git. Cross-session favorites can be considered after observing real use.

## 9. Performance and large catalogs

Large catalogs make eager introspection both slow and operationally rude. The
implementation must therefore:

- request one hierarchy level at a time;
- fetch columns only on object open;
- never fetch rows during discovery;
- cap and paginate every fan-out;
- cancel work when environment, connection, or scope changes;
- discard responses from older revisions;
- virtualize long object and column lists;
- update the loaded search index incrementally;
- retain last-known-good nodes during refresh;
- expose when caps or permissions make results partial;
- lazy-load Data Browser code outside `/data` and its contextual overlay.

Initial performance budgets, measured on local DuckDB plus a remote PostgreSQL
fixture:

| Interaction                                 | Target                                      |
| ------------------------------------------- | ------------------------------------------- |
| Open browser from warm UI state             | visible shell under 100 ms                  |
| Render cached connection root               | under 150 ms                                |
| Expand cached namespace                     | under 100 ms                                |
| Start feedback for remote discovery/preview | under 100 ms                                |
| Search 10,000 loaded objects                | under 50 ms per committed query             |
| Keep scrolling 10,000 objects               | no long task above 50 ms in the tested path |

Remote warehouse time is not hidden behind a false frontend target. Record
time-to-first-feedback and backend operation duration separately.

## 10. Safety and policy

- Secrets remain write-only in the connection form and never enter browser
  responses, logs, object IDs, storage, or SSE.
- Local paths are canonicalized server-side and must remain within the active
  project root or an explicitly configured read-only root. Reject traversal,
  absolute-path escape, and symlink escape; do not expose arbitrary host
  filesystem browsing.
- File discovery is extension-allowlisted and bounded by entry, depth, and
  metadata-size caps. Opening a folder never reads complete file contents.
- Local file previews use the same row, byte, timeout, and cancellation limits
  as warehouse previews and never write beside the source file.
- Preview is read-only, server-constructed, bounded, cancellable, and auditable.
- The backend validates engine quoting and current connection revision.
- Protected environments can require an additional preview confirmation or
  disable previews by policy while still allowing metadata browsing.
- Error messages remove credentials and sensitive DSNs but retain an operation
  ID and actionable adapter detail.
- Column descriptions/tags may contain sensitive metadata; show only what the
  active connection is authorized to expose.
- Import remains a previewed repository mutation and follows existing save,
  conflict, and SSE reconciliation behavior.
- The browser does not add generic DDL/DML actions. Object creation, edit, and
  deletion remain outside this plan.

## 11. Delivery plan

Each phase is independently reviewable. Backend contracts, frontend shell
wiring, and repository mutations should not be bundled into one large commit.

### Phase 0 — Contract fixtures and adapter audit

- Inventory which configured connection types implement databases, schemas,
  tables, columns, preview, and object-store listing.
- Define object identity, capabilities, discovery states, and API fixtures.
- Add contract tests for two-level, three-level, schema-less, empty, partial,
  permission-limited, stale-cache, and folder/file sources.
- Decide exact connection-type MVP gate from demonstrated support rather than
  icon availability.

Exit: frontend fixtures and backend tests share one explicit contract; no
production route is visible.

Suggested commits:

1. `test(data-browser): define discovery contract fixtures`
2. `docs(data-browser): record adapter capability matrix`

### Phase 1 — Read-only hierarchy MVP

- Add `DataBrowserService` and the connection/children/object endpoints.
- Adapt existing SQL discovery and remote-catalog observations.
- Add the `/data` lazy route behind an internal feature gate.
- Build connection root, selected-connection hierarchy, loaded search, kind
  filter, and discovery-state UI.
- Reuse real connection icons and point-of-need creation.
- Support DuckDB, PostgreSQL, and project-scoped local CSV/Parquet/JSON/JSONL
  first; add one proven three-level warehouse.

Exit: a user can create/select a supported connection or local file and inspect
truthful metadata without any row query or repository write.

Suggested commits:

1. `feat(data-browser): add revisioned metadata service`
2. `feat(web): add data browser hierarchy and connection flow`

### Phase 2 — Safe preview and object detail

- Add opaque object identity resolution and safe preview endpoint.
- Implement compact object header plus Preview, Columns, and Usage tabs.
- Reuse the shared result grid with bounded read-only behavior.
- Add cancellation, truncation, elapsed time, refresh, copy, and mobile layout.
- Match authored assets and expose known Renart usage.

Exit: preview never accepts a frontend-built object reference as SQL; desktop
and mobile object detail pass visual, keyboard, and screen-reader checks.

Suggested commits:

1. `feat(data-browser): add bounded object preview`
2. `feat(web): add object detail and usage views`

### Phase 3 — Reviewed handoffs

- Query opens an Ad-hoc document tab with a server-provided quoted reference.
- Add to notebook opens the existing source approval flow.
- Import source asset reuses preview/import and returns to the same object after
  SSE reconciliation.
- Open asset deep-links to the exact asset.
- Use as Load input opens the existing asset creation flow with a typed source
  reference.
- Add capped recent/pinned session state and invalidation.

Exit: every visible action either completes its handoff or explains why its
capability/policy is unavailable.

Suggested commit: `feat(data-browser): connect reviewed object handoffs`

### Phase 4 — Workbench convergence

- Enable the Build contextual overlay and Explore rail entry using the shared
  controller.
- Preserve Build editor/canvas drafts and viewport while the overlay is open.
- Implement mobile connection → hierarchy → object Back behavior.
- Add command-palette and deep-link entries.
- Remove any mock-only Data Browser path once production parity is demonstrated.

Exit: one controller serves `/data`, Build, and Explore with stable history and
no duplicated request state.

Suggested commit: `feat(navigation): integrate data browser into workbench`

### Phase 5 — Durable discovery and remote search

- Expose cache provenance, partial reasons, revision, and exact refresh scope.
- Add targeted discovery SSE and error-with-cache behavior.
- Add pagination and optional bounded remote metadata search.
- Converge LSP and browser observations behind the normalized cache.
- Measure and tune large-catalog limits before enabling broader adapters.

Exit: refresh failures preserve safe cached navigation; no UI claims complete
search when it has only partial metadata.

Suggested commit: `feat(data-browser): harden revisioned discovery`

### Phase 6 — Object stores and advanced metadata

- Add S3/GCS-style prefix and dataset adapters where existing object-storage
  plans prove listing and schema semantics.
- Add explicit format/partition metadata and adapter-dependent previews.
- Add descriptions, keys, tags, and row/size estimates for sources that expose
  them cheaply.
- Consider opt-in profiling and cross-session favorites only after usage data
  justifies them.

Exit: object stores use the same capability envelope without pretending a
bucket prefix is a SQL schema or table. Local files are already covered by the
MVP and do not wait for this phase.

Suggested commit: `feat(data-browser): add object storage adapters`

## 12. Verification strategy

### 12.1 Backend unit and service tests

- canonical identities for two- and three-level names;
- quoting per supported SQL engine;
- stale object/revision conflict;
- no secret fields in JSON, errors, cache keys, or events;
- exact capability mapping;
- canonical local-path containment, traversal/symlink escape rejection, format
  allowlists, and discovery caps;
- cache single-flight, TTL, partial, cap, retry, and error-with-cache behavior;
- cancellation and hard preview limits;
- positive authored matching and ambiguous-match rejection.

### 12.2 Frontend tests

- reducer/controller transitions for environment, connection, revision, and
  stale response races;
- loaded search over object, column, description, type, and tag;
- capability-driven actions;
- pinned/recent caps and invalidation;
- connection creation returns to discovery;
- object tabs and preview fetch timing;
- mobile Back ladder and overlay restoration;
- keyboard navigation, focus restoration, names, and live statuses.

### 12.3 Live E2E

- local DuckDB hierarchy, columns, preview, query handoff, and authored link;
- project-local CSV, Parquet, JSON, and JSONL folder navigation, schema/preview,
  notebook handoff, and reviewed Seed/Load handoff;
- gated PostgreSQL connection creation/discovery/preview/import;
- one gated three-level warehouse to catch catalog/database/schema mistakes;
- slow refresh followed by cancellation;
- error with warm cache and error without cache;
- permission-limited partial source;
- desktop and mobile Workbench overlay preserving an unsaved Build document;
- no network preview request on connection/schema expansion.

Run live warehouse cases serially or with bounded workers to avoid recreating
the existing OOM and Docker-fixture pressure.

### 12.4 Visual checks

Capture desktop, narrow desktop, and mobile screenshots for:

- connection overview and add-warehouse chooser;
- selected connection with multi-database hierarchy;
- cached/partial/error/empty notices;
- short preview result and long scrolling result;
- Columns and Usage tabs;
- mobile connection, hierarchy, and object levels.

The short preview is a release check: its table must not sit at the top of an
oversized empty dialog.

## 13. Diagnostics

Emit structured server timings for:

- connection resolution;
- namespace/object discovery per adapter and scope;
- cache hit/miss/stale state;
- describe and preview duration;
- returned and truncated row/object counts;
- cancellation and adapter error class.

Expose operation IDs to the UI for actionable failures. Product analytics are
not required for the local app. If opt-in product analytics are added later,
measure workflows such as preview-to-import without recording object names,
query text, schemas, or connection identifiers.

## 14. Decisions and deferred questions

The following choices are sufficiently clear to implement:

- one Data Browser controller with `/data`, Build, and Explore adapters;
- metadata-only expansion and explicit row preview;
- server-issued opaque identities and server-owned quoting;
- session-only pinned/recent objects initially;
- explicit manual refresh plus bounded background TTL, not scheduled full
  crawls;
- descriptions, keys, tags, and sizes only when adapters provide them cheaply;
- no automatic profiling;
- no warehouse DDL/DML actions;
- no object-store support in the SQL MVP;
- project-scoped local-file support is part of the MVP and uses a dedicated
  adapter rather than pretending to be a SQL connection;
- no promise of complete warehouse lineage in Usage.

Defer until evidence exists:

- durable cross-session or team-shared favorites;
- scheduled catalog refresh outside an active Renart process;
- warehouse query-history lineage;
- column value distributions and profiling;
- editable descriptions/tags pushed back to a warehouse;
- all-project discovery across multiple Renart projects;
- automatic source-asset creation.

These deferrals do not block Phases 0–4.

## 15. Prior-art lessons applied

- [Hex Data Browser](https://learn.hex.tech/docs/explore-data/data-browser):
  searchable database/schema/table/column metadata, favorites/recent objects,
  explicit sample preview, and direct query/copy actions.
- [DataGrip introspection](https://www.jetbrains.com/help/datagrip/introspection.html):
  scope and incremental introspection matter because large catalogs make eager
  metadata loading expensive.
- [Databricks Catalog Explorer](https://learn.microsoft.com/en-us/azure/databricks/catalog-explorer/):
  object details, columns, sample data, and relationships are related but
  distinct views with different access requirements.
- [Snowflake object explorer](https://docs.snowflake.com/en/user-guide/ui-snowsight-data):
  preserve source-native hierarchy and show only objects the current identity
  can access.

Renart should adopt the useful interaction rules, not their complete product
scope or visual language.

## 16. Definition of done

The Data Browser expansion is complete when:

1. `/data` and contextual Workbench entry points use one controller;
2. supported connections expose truthful, lazy, revisioned hierarchy states;
3. adding a connection or choosing a supported project-local file is possible
   from the browser without exposing secrets or the host filesystem;
4. opening hierarchy nodes never queries rows;
5. object preview is explicit, bounded, read-only, cancellable, and
   server-constructed;
6. Columns and Usage distinguish observed metadata from managed Renart truth;
7. Query, notebook, import/open asset, and Load handoffs work where capabilities
   permit;
8. stale/partial/error/empty behavior preserves last-known-good information and
   never overstates completeness;
9. large-catalog budgets and desktop/mobile accessibility checks pass;
10. the mock-only Data Browser implementation is removed, as-built behavior is
    documented in `architecture/frontend.md`, and this plan is deleted.
