# Addressable diagnostic targets and independent detail navigation

Status: implementation started, 2026-09-05; first column-type vertical slice implemented.
Audited baseline: `codex/semantic-impact`, `1d7fd1b7`, directly above `v0.5.0`.

## Implementation checkpoint — 2026-09-05

- Implemented the shared validated `project`/versioned `detail` URL contract,
  project bootstrap before workspace effects, and runtime scope when storage
  is unavailable. The server identity locks an already loaded legacy workspace.
- `declared-column-type-drift` supplies a structured column/field subject.
  Typecheck and readiness aggregation preserve its exact resource target;
  TypeCheckPanel and deployment review expose real navigation links.
- The lazy independent right detail outlet reuses the existing Columns form,
  expands the matching row and focuses/highlights Type. It uses the target's
  environment without changing the global selection. Mobile uses a full-width
  Sheet. The ordinary inspector host stays mounted but hidden and inert.
- Back/Forward restores the URL target. Explicit Close/Escape removes only
  `detail` with replace, including after a cold arrival. This initial policy
  deliberately does not guess whether a previous browser entry is an opener.
- Desktop and mobile live tests cover Typecheck and deployment links, saved
  type corrections, unchanged primary Monaco instance/draft, a collapsed Data
  Browser sidebar, wrong existing project pin, empty-storage Run-context links,
  stale columns and unavailable projects. The Run-context target does not mount
  Monaco or a hidden Build page.
- Still pending: the complete diagnostic coverage inventory/registry, canonical
  builders for other resources, Data Browser durable-address resolution (slice
  2), other repair surfaces (slice 3), and broader source/revision targets. LSP
  inline actions and schema-name/nullability warnings are not migrated by this
  first slice. Cross-project in-app cache switching remains unsupported; use a
  full document navigation/new tab. Column names are not immutable identities;
  renamed or ambiguous names produce a visible fallback, not an inferred match.

Current implementation details live in [frontend architecture](../architecture/frontend.md#addressable-diagnostic-details).

## 1. Decision

Make actionable diagnostics point to **typed resource locations**, not to
buttons, DOM selectors, or imperative sequences of UI changes. Render those
locations as real links with complete, validated URLs.

Keep TanStack Router. Introduce one independent, URL-driven **detail outlet**
beside the primary route. An error link changes the detail target, not the
primary document or the left Workbench navigation. The same URL must reconstruct
the target after reload, paste, middle-click, or opening a fresh browser tab.

Use Angular auxiliary routes as the conceptual reference, not as a reason to
change frameworks or reproduce Angular's URL parser. Start with one auxiliary
detail target; do not build a general-purpose parallel router or a modal stack.

The initial vertical slice is:

> A declared/inferred column-type mismatch → “Open declaration” → the exact
> column's type control, with the Columns section and column row expanded.

The existing SQL editor, draft, canvas, selected left-sidebar tool, sidebar
width, collapse state, expanded branches, and scroll position remain intact.
Opening that link in a new tab reconstructs the **saved** target and background
route; it does not transfer unsaved editor buffers or private session chrome.

## 2. Pre-implementation audit

| Area | Current evidence | Consequence |
| --- | --- | --- |
| Routing | [build-route-model.ts](../web/components/app/build-route-model.ts) validates only `result` and `editor`; [data.tsx](../web/src/routes/_shell/data.tsx) has no selection search contract | New field/detail keys need a shared parent contract and deliberate preservation during navigation |
| Workbench | [workbench-slots.tsx](../web/components/app/workbench/workbench-slots.tsx) supplies `context` and `inspector` React portals; route mode/tool changes dispatch `route-entered` | Portals provide placement, not addressability; they are not auxiliary routes |
| Sidebar coupling | [build-page.tsx](../web/components/app/build-page.tsx), `selectAsset`, dispatches `tool-selected(resources)`; `reviewFailedCheck` calls it | Reusing this handler for diagnostic navigation changes the large left sidebar |
| Properties | [asset-guided-cards.tsx](../web/components/app/asset-guided-cards.tsx), `AssetGuidedCards`, stores the active tab locally; `ColumnRow` uses an uncontrolled collapsible and `useId()` input IDs | Neither the Columns tab nor a specific expanded column/field can currently be restored from a URL |
| Diagnostic data | [output_drift.go](../internal/sqlintelligence/output_drift.go) knows each column and both types, but emits those details in message text; [Diagnostic](../internal/authoringdiag/diagnostic.go) has no structured subject | Add structured facts at the producer; never extract column names from translated/error prose |
| Diagnostic actions | [TypeCheckPanel](../web/components/app/type-check-panel.tsx) uses callback buttons for asset/action navigation; [contracts.go](../internal/web/typecheck/contracts.go) separates transactions and actions only partially | Navigation needs a real `href`; mutation commands must remain explicit buttons |
| Aggregation | [planner.go](../internal/web/execution/planner.go), `appendCodeCheckIssues`, copies message/severity into generic `code_check_*` issues | Preserve original diagnostic identity and targets through planning, aggregation, and deduplication |
| Data Browser | [data-browser.tsx](../web/components/app/data-browser/data-browser.tsx) stores connection, breadcrumb levels, selected object and dialog openness locally | Opening an object depends on prior clicks; a detail target needs an independent load-by-address path |
| Data identities | [databrowser/service.go](../internal/web/databrowser/service.go), `objectRef`, `resolveScope`, `revisionToken` | Object/connection IDs embed a configuration revision and are rejected when stale; they are not durable bookmark identities |
| Project identity | [project-context.ts](../web/lib/project-context.ts) pins a project in `sessionStorage`; [project-switcher.tsx](../web/components/app/project-switcher.tsx) pins then reloads `/` | A fresh tab can otherwise open the right relative asset in the wrong project |
| Existing precedent | [project/connections.tsx](../web/src/routes/_shell/project/connections.tsx) already validates environment/connection search | Reuse this approach and existing forms rather than adding a second settings implementation |

The older [Workbench migration plan](navigation-workbench-migration.md), §6.3,
proposed URL-selected utilities. That is not the actual current Build search
contract. This proposal supersedes its suggestion to use `tool=` for diagnostic
details: opening a repair target must not select a left-sidebar tool. Its
rule against serializing disposable dialogs also needs this distinction:
resource detail surfaces are addressable; menus and confirmation prompts are not.

## 3. Research and options

Angular's named outlets maintain separate branches of the navigation tree and
serialize primary and secondary destinations together. That is the useful
property: changing a detail branch need not replace the primary route.
[Angular RouterOutlet](https://angular.dev/api/router/RouterOutlet),
[named-outlet guide](https://angular.dev/guide/routing/show-routes-with-outlets).

TanStack supports structured JSON search parameters, validation, and inherited
parent search contracts. These are sufficient to represent one typed detail
target using the existing router/history, without another routing engine.
[TanStack search parameters](https://tanstack.com/router/latest/docs/guide/search-params).

| Option | Assessment |
| --- | --- |
| Primary child routes for every detail | Good for canonical resource pages; insufficient alone when a repair target must open over an unrelated Run/Build/Data page without replacing it |
| Local state or event bus | Cannot reconstruct the destination in a fresh tab; reject as navigation authority |
| Hash containing a DOM ID | Too dependent on mounted/expanded UI and generated IDs; may be a final scroll mechanism, not the resource address |
| Route masking/background-location state | Useful presentation technique, but not the correctness basis: hidden location state does not travel with copied URLs |
| Full Angular-style parallel route trees | More routing infrastructure than Renart needs; reject for the first implementation |
| Existing primary route + validated detail search | Recommended: complete href, independent placement, native browser history, incremental adoption |

TanStack route masking stores hidden locations in history state; shared URLs
lose that information. It could be an optional later cosmetic feature only if
the visible URL still resolves the complete target independently.
[TanStack route masking](https://tanstack.com/router/latest/docs/guide/route-masking).

## 4. Three contracts, with clear ownership

### 4.1 Diagnostic subject: owned by the diagnosing domain

Extend the core diagnostic with structured subjects/facts, for example a
declared column's identity, the affected aspect (`type` or `nullability`), and
the inferred counterpart. For aggregate schema drift, retain the actual lists
of missing and undeclared columns instead of retaining only a formatted string.

`internal/authoringdiag` remains independent of HTTP, React, and URL paths.
SQL intelligence reports facts such as “declared column total_amount, type”; it
does not know about an inspector, tab label, CSS selector, or routing library.

### 4.2 Resource target: a shared, serializable web-domain value

Introduce a small leaf contract package, provisionally
`internal/web/navigationtarget`, with no service/httpapi imports. Generate its
public types through the existing API type generator. Web service adapters
resolve domain subjects to resource identities and attach navigation links.

Illustrative target variants, not a final wire schema:

```ts
type ResourceTarget =
  | { kind: 'asset-column'; pipelineId: string; assetId: string;
      column: ColumnKey; field?: 'type' | 'nullable' | 'description' }
  | { kind: 'asset-section'; pipelineId: string; assetId: string;
      section: 'general' | 'columns' | 'checks' | 'lineage' }
  | { kind: 'asset-source'; pipelineId: string; assetId: string;
      range?: SourceRange; sourceFingerprint?: string }
  | { kind: 'data-object'; address: DataObjectAddress;
      column?: ColumnKey; section?: 'schema' | 'rows' }
  | { kind: 'connection-field'; connection: ConnectionKey; field?: string };
```

Each resolved link also carries authoritative project/environment scope and,
where relevant, an expected resource/source revision. The Go wire contract is
the authority; do not maintain a second handwritten API DTO union in the client.
The TypeScript sketch above explains semantics, not a commitment to field casing.

Add stable link identities and a role such as `declaration`, `source`, or
`upstream`. Keep navigation links separate from mutation resolutions. The
existing `transaction` path still performs edits through the Go server after
an explicit user action. No URL represents “apply fix”, “deploy”, or “run query”.

### 4.3 Route destination: owned exclusively by the frontend

A small registry in `web/lib/navigation/` maps supported target variants to:

- validated canonical route/search options;
- contextual detail-route options;
- a lazy detail renderer and supported field/section locations;
- resource resolution and meaningful unavailable-target presentation.

Diagnostic components consume a shared `DiagnosticLinks`/`ResourceLink`
component, not custom switch statements, `window.open`, or click-event chains.
It produces a genuine TanStack `Link`/anchor. Ordinary click, context menu,
copy address, modifier-click, and middle-click all use the same href.

Do not put URLs into SQL parser diagnostics, and do not build a generic
`openUI(componentName, props)` mechanism that can instantiate arbitrary UI.

## 5. URL and state model

Keep existing primary paths. Add a shared scope/detail search contract at the
appropriate root/pathless layout boundary. Proposed human-readable decoded form:

```text
/pipelines/<pipeline>/assets/<asset>/split
  ?project=<project-id>
  &result=typecheck
  &editor=asset
  &detail={"v":1,"environment":"default","target":{
    "kind":"asset-column","pipelineId":"<pipeline>","assetId":"<asset>",
    "column":{"name":"total_amount"},"field":"type"}}
```

TanStack serializes/escapes the `detail` object; callers never concatenate JSON
or manually double-encode IDs. The versioned object avoids a growing collection
of conflicting `modal=`, `tab=`, `column=`, `focus=` conventions. An analogous
link from a run keeps `/runs/<run>` as its primary path. A data-object target can
open over Build without navigating to `/data` or activating its left rail tool.

Provide two builders: `targetToDetailLocation(current, target)` for contextual
repair links, and `targetToCanonicalLocation(target)` for standalone resource
navigation. Both encode all target-relevant state in the visible URL. A normal
new-tab action uses the same contextual href, not a different click handler.

| State | Authority | Detail-navigation behavior |
| --- | --- | --- |
| Project scope | Validated URL for new links; legacy tab pin only for old URLs | Resolve before workspace-scoped requests; never silently fall back to another project |
| Primary resource/view/result | Existing primary path/search | Unchanged when opening a contextual detail |
| Detail resource, field/section, detail environment | Validated `detail` search | Reconstruct completely on cold load; presence means open |
| Left tool, open state, width, tree expansion/scroll | Existing Workbench/session controller | No `tool-selected`, route-mode change, forced reveal, or reset |
| Form draft, pending saves, editor/canvas state | Existing domain controllers | Remain owned by the page/resource; never put contents in a URL |
| Hover, animation, focus acknowledgement | Local ephemeral state | May use a small readiness/ref registry, never as the target source of truth |

Every search update must deliberately preserve compatible primary/scope/detail
keys. Audit the existing `search: buildSearch` and reconstructed two-key objects;
simply adding `validateSearch` does not fix those writers. Primary resource
navigation has an explicit policy: clear unrelated details, or preserve an
independently addressed detail when requested. Do not accidentally retarget it
to whichever asset happens to be selected later.

### Scope bootstrap is a prerequisite, not polish

Before mounting workspace-consuming routes/providers, validate the URL project
against the existing project directory and establish the corresponding API/SSE
scope. Scope comes from the link, never a guessed filesystem path. This should
be a root bootstrap/before-load boundary, not a child `useEffect` after requests
have already started. Unknown/unavailable projects stop with a useful error.

Preserve legacy URL behavior when `project` is absent, but all newly generated
diagnostic links include the actual project ID, including the server's default.
Scope caches and subscriptions by project. A cross-project transition must use
the existing save/leave boundary; do not replace the main document's project
merely to show a detail from another project. Initially, use canonical navigation
or an explicit new-tab action for cross-project targets.

The detail environment is explicit and does not mutate the global execution
environment selector. Forms/loaders need a scoped environment input instead of
implicitly reading only `selectedEnvironmentAtom`. Existing workspaces can keep
their ordinary execution context while inspecting a diagnostic for another env.

## 6. Detail outlet and reaching the exact control

Add a small routed-detail coordinator beside the primary outlet. Desktop uses
the right detail region; mobile uses one routed Sheet/Dialog. Closing it changes
the URL. It must work on Build, Run, Data, and settings pages, not only inside
`build-page.tsx`.

Reuse the current Workbench layout and domain components. Separate placement
from ownership: extract an asset-properties controller/surface from the Build
coupling, and a Data Browser detail controller from its navigator. Do not mount
an entire hidden Build page to display one column or a complete hidden `/data`
page to show a table schema.

There must be one owner of the right-hand region. When a routed detail takes
precedence over the page's ordinary inspector, keep the existing inspector host
stable and preserve its local state; do not move portals between DOM hosts or
render two competing editable forms for the same asset. Reuse the existing
resource controller when appropriate; an unrelated target loads independently.
Closing restores the previous page inspector without replaying sidebar actions.

Exact column focus is a deterministic lifecycle:

1. Resolve the project, resource, environment, revision, and column identity.
2. Activate the controlled `columns` section.
3. Expand the target row through controlled collapsible state.
4. Wait for its actual input/ref (or virtualized row) to mount.
5. Scroll the detail's own scroll container, focus the type control, and add a
   restrained transient highlight. Respect reduced motion and screen readers.

Use semantic row/field keys for this handshake. `useId()` remains suitable for
HTML label/input associations, but is never stored in links. No timeout-based
click simulation or global `document.querySelector(message-derived-selector)`.
Focus runs once per navigation entry/target, not again on every SSE refresh;
stale async resolution cannot steal focus from a newer navigation or typed edit.
Repeated activation of the same link can re-reveal the target locally without
putting a timestamp/token into the URL or creating duplicate history entries.

Column names are today's persisted identity, not immutable UUIDs. Preserve
dialect-sensitive quoted identities and the backend's canonical matching rules;
never use array index alone or casually lowercase names in the client. If a
column was renamed/deleted, or identity is ambiguous, show the Columns section
with an explicit unavailable-target notice rather than selecting a different
column. Durable rename-following IDs are a separate future data-model decision.

An inferred-only column has no declared row: offer the SQL/provenance target or
the Columns reconciliation section, not a fabricated “edit declaration” field.

## 7. Data Browser: durable address versus execution token

Current `objectRef` IDs contain source kind, environment, connection, namespace,
object/path **and a revision**. `resolveScope` intentionally rejects a stale
revision. Keep that protection for API operations; do not weaken it for links.

Introduce a durable, credential-free `DataObjectAddress` alongside the current
revision-bound IDs:

- warehouse: configured connection identity/type plus environment and structured
  catalog/database/schema/object segments, preserving quoted/case semantics;
- project file: a project-relative, supported tabular-file path;
- optional column identity and detail section.

Do not bookmark generated SQL, credentials, signed URLs, absolute local paths,
the ephemeral object token, or dot-joined names that lose identifier boundaries.
Configured connection names are the current identity: renaming one invalidates
old links unless a future migration explicitly introduces durable IDs.

Add a bounded read-only resolver under the existing Data Browser API/service.
It validates the address against the chosen project's configured connections,
current environment and path rules, resolves the live object, and returns fresh
existing operation IDs plus safe detail/breadcrumb metadata. Clients must not
mint or decode server object tokens themselves. A configuration revision change
refreshes through the address; a removed connection/table remains an explicit
error, never a silently substituted source.

Load a linked object's schema directly without requiring connection/tree click
history. The navigator can lazily reveal that address **only on explicit user
request**; opening the object detail itself must not overwrite its selection,
breadcrumbs, filter, expansion or scroll state. Separate shared resource caches
from independent navigator/detail selections.

Selecting the Rows section is addressable, but executing Preview remains an
explicit button action. Navigation may perform bounded metadata reads; it must
not automatically fetch table rows, execute arbitrary SQL, create connections,
write definitions, deploy, or activate schedules.

## 8. Diagnostic coverage and UI policy

| Diagnostic | Primary link | Optional related target |
| --- | --- | --- |
| Declared/inferred type or nullability drift | Exact declared column field | SQL projection if its source mapping is reliable |
| Missing declared/extra inferred columns | Matching declared rows or Columns reconciliation | SQL/source for outputs with no declaration |
| Missing/invalid dependency | Asset Lineage/dependency location | Producer asset or observed data object |
| Invalid materialization config | Exact known setting; otherwise General/materialization section | Relevant column such as update key |
| Missing connection/credential/config field | Scoped connection form and known field | Environment's connection assignment |
| Failed quality check | Exact custom/column check identity | Source/result evidence with an explicit version context |
| Data Browser stale/offline/missing object | Current stable object address or scoped connection form | Explicit refresh/retry command, separate from navigation |
| Notebook/presentation problem | Persisted cell/block/dataset identity and field | Source asset; later slice using the same contracts |

“The error location” and “where the user can repair it” are different concepts.
Allow a small number of related targets rather than assuming every error points
to its SQL range. A type mismatch does not prove that changing the declaration
is the correct fix; use “Open declaration”, not “Fix automatically”.

Provide one low-noise primary text link near a diagnostic and, only when useful,
one secondary source link. Aggregate schema errors keep a compact disclosure of
affected columns; do not emit a wall of action buttons. The semantic deployment
review's lenses can use these same targets without importing the demo analyzer.

To make this systematic rather than best-effort:

- Extend the existing diagnostic-code registry with explicit remediation
  coverage: exact target, owner/section target, or `no-ui-target` with a reason.
- Require typed subjects for first-party actionable diagnostics. New registered
  codes without a declared policy fail CI; fixtures verify actual emitted
  targets, not just registry entries. Known repairable failures must not pass
  by returning an empty target list.
- Add exhaustive client target-handler tests and use one link renderer across
  type checks, plan issues, settings validation, runtime findings and toasts.
- Preserve target metadata/identity through adapters and plan aggregation;
  deduplicate by diagnostic/resource identity without dropping richer targets.
- Audit existing imperative navigation buttons. Add focused lint/import rules
  for migrated diagnostic surfaces and a review rule for new actionable copy.
- Unknown third-party/free-text errors use only a verified owner-context link
  or an explicit unlinked fallback. Do not infer exact fields using regex/LLMs.

This enforces coverage for structured first-party diagnostics; no type system
can prove that every arbitrary prose string in the application is actionable.
The initial inventory and ongoing UI/code-review policy close that gap.

## 9. History, drafts, failures, and safety

- Open a detail with one history push. Back closes it; Forward restores it.
  Explicit section/field navigation can push; normalization and repeated-target
  activation do not add redundant entries.
- Close uses Back only if a tracked entry really belongs to this detail flow;
  on a cold link, remove the detail keys with replace instead of navigating the
  user out of Renart. Historical state may optimize closing, never resolve the target.
- Reuse save barriers. A detail-only change must not unmount or discard the main
  draft; changing/closing a dirty detail needs its own leave guard. Router
  blockers distinguish primary identity changes from auxiliary changes.
  [TanStack navigation blocking](https://tanstack.com/router/latest/docs/guide/navigation-blocking).
- Do not key the whole app/editor/provider by `location.href` or the detail
  object. Keep controllers/cache keys tied to resource identity, not incidental
  focus. Avoid hidden heavyweight page trees as a persistence workaround.
- Changing an auxiliary target cancels/ignores old resource requests. Error
  boundaries belong to each region, so a missing target cannot destroy the
  primary editor, and an unavailable background resource need not hide a valid detail.
- Validate enum values, union variants, nesting/length limits, project scope,
  field paths and identifiers. Use server path validation and existing
  authorization; a target is never a permission/capability token.
- Never serialize secret values, SQL text, row values, error stacks, or drafts.
  A secret-field name can be navigable without disclosing its value.
- Historical deployment/run targets must identify their revision. Read-only
  history and editable current definitions are different destinations. Where
  historical metadata UI is not supported, explicitly label a current-definition
  fallback; do not imply it is the exact historical resource.
- SQL ranges require a matching source identity; stale or unsaved-buffer
  diagnostics degrade to the owner/source section. Fresh tabs show saved data.
- Keep the URL as the only detail selection authority. Local readiness refs,
  draft controllers, and optional session preferences are not a second router.

## 10. Incremental implementation plan

### Slice 0 — contracts and cold-link correctness

Inventory all first-party actionable diagnostic producers/surfaces. Introduce
target/scope contracts, versioned detail codec, canonical/contextual builders,
and coverage policy. Add project bootstrap before workspace requests. Audit
search writers and existing project/connection links. No UI-wide redesign.

Gate: a synthetic target survives serialize/parse, copied href, fresh-tab load
with empty storage, and Back/Forward; invalid project never fetches default data.

### Slice 1 — column-type mismatch end to end

Preserve structured column facts in `output_drift.go`; map them through core →
typecheck → planner without parsing messages. Implement the independent detail
outlet and controlled Columns tab/row/field focus using the existing forms.
Replace the relevant callback actions with real links in TypeCheckPanel and the
deployment review. No mutation is performed on arrival.

Gate: the user's exact mismatch example works from Build and a Run-context
link, on desktop/mobile, while the left sidebar and main draft remain unchanged.

### Slice 2 — Data Browser addresses and resolver

Add durable addresses alongside existing operation tokens; implement the scoped
read-only resolver. Separate navigator selection from object-detail state and
make `/data` and contextual details use the same resource controller.

Gate: a schema/column bookmark survives reload and a connection revision change,
does not change the Build sidebar, and performs no row preview automatically.

### Slice 3 — other repair surfaces

Migrate connection/environment fields, materialization, dependencies and quality
checks. Then adopt the same target model for persisted notebook cells,
presentation blocks and runtime errors. Keep mutation resolutions unchanged.
Expand coverage gates as each producer family migrates; publish the remaining
explicit gaps rather than claiming universal coverage prematurely.

### Slice 4 — convergence

Remove obsolete callback/event-focus paths and competing local selection state
from migrated surfaces. Document the settled contracts in frontend,
asset-editing, SQL-LSP and backend architecture docs; retire this plan after
implementation. Update old navigation-plan guidance where it conflicts.

## 11. Acceptance tests

1. URL codec/property tests: supported variants, Unicode/quoted names, escaping,
   invalid/oversized input, incompatible params, canonical round-trip and v1 handling.
2. Producer/adapter tests: exact column/field identity, aggregate errors,
   missing/renamed/ambiguous targets, registry completeness, metadata survives
   planner aggregation and generated API type checks.
3. Workbench invariant tests: detail open/close/back/forward leaves active tool,
   sidebar open/width/tree/scroll and primary params/result/editor unchanged.
4. Cold browser context: paste href with empty session/local storage and no
   warm atoms; correct project/environment and exact expanded control appear.
   Also test a conflicting project pin, modifier-click and middle-click.
5. Editor continuity: dirty Monaco buffer and canvas viewport survive detail
   navigation; canceled leave does not change URL; no auto-save/mutation is
   triggered by opening the target. New tab explicitly has saved content only.
6. Detail lifecycle: close/reopen, same-link reactivation, rapid target changes,
   stale async responses, SSE refresh, removed asset/column, collapsed or
   virtualized rows, unavailable primary context and independent error boundaries.
7. Data Browser: direct resolution without tree history, environment mismatch,
   revision invalidation, offline/missing connection, removed table, local-file
   traversal rejection, and no preview/SQL-execution requests on navigation.
8. Accessibility/responsiveness: keyboard link behavior, input focus and focus
   restoration, readable highlight, reduced motion, one mobile detail sheet,
   no altered left drawer state or competing focus traps.

Run focused unit/API tests first; then the existing frontend build/API-type,
lint and bundle checks and bounded live Playwright cases with one worker.
No new heavyweight background process or parallel warehouse test matrix is
needed to validate this architecture.

## 12. Recommended first milestone

Implement slices 0 and 1 together as one narrow vertical capability. Demonstrate
a copied error link opening `total_amount`'s type field in a fresh tab while an
existing tab keeps its collapsed Data Browser sidebar and unsaved SQL intact.
Only then generalize the now-proven contract to the Data Browser resolver and
the remaining diagnostic families.
