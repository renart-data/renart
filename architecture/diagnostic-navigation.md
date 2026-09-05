# Diagnostic navigation — current architecture

Renart uses TanStack Router's primary route plus one versioned `detail` search
value. This independent region does not need Angular auxiliary routes or a
second router. Following a diagnostic does not select a Workbench tool, expand
the left sidebar, replace the main document, or execute the proposed fix.

## Contract and ownership

The URL contains `project=<actual-project-id>` and JSON
`detail={v:1,environment,target}`. `web/lib/resource-navigation.ts` validates
version, variants, identifier bounds and source anchors. `ResourceLink` renders
real router anchors; `resourceHref` supplies the same scoped address to Monaco's
clickable diagnostic codes. Copied links and modified/new-tab clicks require no
event bus. Unknown or invalid URL variants fail visibly.

| Target | Exact identity | Detail surface |
| --- | --- | --- |
| `asset-column` | Asset ID, column spelling, `field:type` | Existing Columns form, expanded row and focused Type |
| `asset-section` | Asset ID, section; optional column/check | Columns, Checks, Dependencies, Identity, Materialization or saved Source |
| `connection` | Environment, connection name; optional field | Existing settings controller/form; environment and type locked |
| `data-object` | Durable address, schema/rows section; optional column | Shared Data Browser object view |
| `notebook-cell` | Notebook workspace ID and persisted cell ID | Current saved cell, without notebook runtime |
| `presentation` | Presentation workspace ID; optional visualization ID | Current saved artifact/block, without preview/runtime |

Go `internal/web/navigationtarget` owns the generated public DTO and diagnostic
policy. Core `authoringdiag.Subject` carries semantic facts, never URLs, DOM
selectors or identifiers extracted from translated prose. `dataaddress` is a
shared leaf contract, so navigation does not depend on the Data Browser service.

## Scope and lifecycle

Root `beforeLoad` validates explicit projects before workspace consumers/SSE
mount. A runtime pin works without session storage; a conflicting stored pin
cannot override the URL. A mounted app does not hot-switch project caches under
an unsaved document: another project requires a new tab/document navigation.
The detail environment is an explicit controller input, not a write to the
global execution selection.

The Workbench lazily mounts one right-hand detail region (Sheet on mobile).
Its normal inspector host remains mounted but hidden/inert. Resource/request
errors are local notices; a region error boundary protects the main workspace.
Main editors and drafts are not keyed by the detail target. Opening/tab changes
push history, repeated identical links replace, Back/Forward restores the URL,
and Close/Escape removes only `detail` with replace. Cold arrivals therefore
never navigate out of Renart on close. Exact targets use semantic refs and do
not refocus on unrelated primary query changes; the connected opener regains
focus on close.

Connection details reuse `useWorkspaceConnectionForm` and
`WorkspaceConnectionFormFields`. Their initial configuration snapshot stays
stable while editing. Verify/Save are explicit commands; navigation, close and
browser unload guard unsaved connection changes. Successful saves refresh the
form snapshot, and rename updates the URL. Asset cards retain their existing
explicit/blur-commit transaction semantics instead of adding a second draft store.

## Durable Data Browser addresses

Warehouse addresses contain connection name/type and separate database, schema
and object names. Local addresses contain a project-relative file path. Neither
contains revision-bound operation IDs, SQL, connection values or secrets.
`POST /api/data-browser/resolve` is a read-only metadata endpoint with a strict
16 KiB JSON boundary. It verifies environment/connection, rediscovers the exact
object, rejects duplicate matches and issues a fresh operation token. Requested
identifiers are not dot-split or used to construct SQL. Quoted dots/quotes in
discovered relation names preserve identifier boundaries during preview quoting.

Old operation tokens still fail when the configuration revision changes; durable
bookmarks can resolve again without weakening that protection. Local paths keep
containment, symlink, hidden/generated-path and supported-format checks. Local
schema discovery may issue bounded DuckDB DESCRIBE, but row previews require the
explicit Preview rows command and remain server-capped.

Navigator tree/connection state and object/preview state have independent
owners. Object nodes and columns are links: no competing `objectDialogOpen` or
local selected-object state remains in the navigator. `/data` uses the same
routed controller as its primary content, never a duplicate outlet. Changing
address/environment aborts requests and ignores late responses. Preview data
never enters the URL. Stale preview errors offer metadata refresh, not automatic
query re-execution.

## Coverage and deliberate boundaries

`navigationtarget.DiagnosticPolicy` classifies every registered pipeline
type-check code. Tests cover both registry completeness and actual producers.
Exact type and NOT-NULL targets require structured subjects; aggregate schema
drift opens Columns, and other codes use a verified section owner. Custom-check
SQL keeps Checks as owner even when it shares ordinary SQL diagnostic codes.

Pipeline type checks, deployment readiness and HTTP LSP preserve the targets.
Generated `SQLDiagnosticLink` records refer to diagnostics in the same response,
not persisted positional identities. Borrowed ad-hoc, hook, presentation-query
and custom-check document IDs do not acquire an incorrect asset-body destination.
Core/stdin LSP preserves subjects without depending on web navigation.
Presentation visualization IDs come directly from producer traversal, not from
parsing indexed display paths. Notebook runtime errors link to their verified
saved-cell owner without parsing warehouse/Python prose.

Source details explicitly show **current saved source**, not historical snapshots
or unsaved editor text. Line highlights require a matching normalized source
fingerprint; stale links show a notice and no old highlight. Renamed, missing or
ambiguous columns/checks/objects/blocks never focus a guessed replacement.
Notebook and presentation details are read-only; their explicit editor links
open the owner editor, not a hidden editor clone.

Coverage is intentionally bounded: unregistered plugin diagnostics do not get
guessed targets (`navigation_unavailable_reason` records this for asset findings).
Free-text toasts, generic infrastructure errors and process policies without an
editable field are not automatically linked. Existing settings-page links and
language Go to Definition remain ordinary navigation, not competing state for
the diagnostic detail region. Snapshot-specific editing, automatic rename
following, deep presentation-field editing and migration of arbitrary prose are
not implemented.

New actionable diagnostics must supply a stable code and semantic subject,
select a tested exact target or honest owner fallback, preserve it through
adapters and render the shared link separately from mutation buttons. Do not add
message parsing, `window.location` sequences or tool-selection callbacks to
reveal a repair field. Add producer, codec and cold-tab/live regressions when
extending a target family.

## Verification

Unit gates cover URL variants, bounds, unsafe paths, quoted identifiers, stale
tokens, duplicate identities, HTTP decoding, diagnostic parity and propagation.
`resource-navigation.live.spec.ts` and the scoped Data Browser test in
`build-editor.live.spec.ts` cover desktop/mobile cold links, conflicting storage,
exact focus, unchanged editor/sidebar, history, explicit saves, connection leave
cancellation, saved document views and absence of automatic preview/runtime
requests. Run one browser worker locally. Lazy handlers stay within the existing
bundle budgets.
