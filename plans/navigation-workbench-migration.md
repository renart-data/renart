# Navigation workbench production migration

Status: the Workbench migration release train (Slices 0–10) is implemented,
converged, and release-gated on the feature branch as of 2026-09-02. The
approved Workbench rail is the only production target; Lifecycle and Project
Studio remain design studies. Slice 11 now has a production foundation with
the `/data` route, Build overlay, mobile shadcn tool tabs, lazy warehouse/local
file hierarchy, and bounded preview. Its live warehouse matrix, reviewed
handoffs, Usage view, and durable discovery work remain open; structure-aware
canvas creation remains a separate Slice 13 feature.

Related design study:
[navigation-information-architecture-mocks.md](navigation-information-architecture-mocks.md).
That document owns the product rationale, prior-art research, and interactive
mock findings. This document owns the executable migration sequence.

Focused expansion plan:
[data-browser.md](data-browser.md). It owns the production object identity,
discovery, preview, handoff, performance, safety, and verification design. The
Data Browser sections below remain the release-train boundary summary.

Final migration evidence:

- `GOMAXPROCS=2 corepack pnpm check`: formatting, lint, 153 unit tests, API
  contracts, TypeScript, production build, and bundle budgets passed;
- `GOMAXPROCS=2 make go-test`: the complete serial Go suite passed using a
  non-repository home-cache `TMPDIR`;
- Playwright live E2E with one worker and `--retries=0`: 305 passed, 65
  intentionally skipped, 0 failed, and 0 flaky in 32.6 minutes;
- the formerly timing-sensitive virtual result-grid scenario additionally
  passed five consecutive Chromium runs and one mobile Chromium run without a
  retry.

## 1. Outcome

Renart should become one project workbench with three stable top-level modes:

- **Build** for Git-backed pipeline and notebook authoring;
- **Run** for deployments, schedules, projected work, and executions;
- **Explore** for workspace-wide lineage, dashboards, reports, and connected
  data discovery.

The header retains those three top-level links. On desktop, every mode uses one
rounded left workbench made from a narrow mode-aware rail and one collapsible
context sidebar. The central surface remains the place where the selected
resource is edited or inspected, and a right inspector is shown only when the
selected resource has editable or investigative detail. On mobile, Build, Run,
and Explore become three bottom destinations. The desktop rail is replaced by
a horizontally scrollable, mode-aware tool-tab strip directly below the global
header; the drawer contains only the contextual hierarchy for the selected
tool.

The migration is complete when:

1. the six existing header destinations have become Build, Run, and Explore;
2. all existing URLs and browser history behavior remain valid;
3. every current production capability remains reachable by mouse, keyboard,
   command palette, direct URL, and mobile navigation;
4. Build uses the existing editor, canvas, result panel, save barriers, and
   asset inspector inside the new shell;
5. Run has an overview, deployments, schedules, the full runs list, and the
   existing run-detail investigation surface;
6. Explore contains the real workspace-wide lineage canvas plus the existing
   dashboard and report builders;
7. Connections, Environments, Pipeline settings, Data Browser, Ad-hoc Query,
   and Notebooks have the point-of-need placement shown by the mock;
8. the old shell-specific navigation and the design-lab routes can be removed;
9. current-state behavior is documented in `architecture/frontend.md` and this
   plan is deleted.

## 2. Scope boundaries

### 2.1 In scope

- top-level information architecture and responsive app-shell geometry;
- route metadata and route-derived mode/tool selection;
- the narrow desktop rail, contextual sidebar, mobile tool tabs/drawer, and
  shell slots;
- moving existing pages into the shell without changing their domain meaning;
- a versioned session-only workbench state for layout continuity;
- Build document tabs and the Ad-hoc/Notebook placement established by the
  mock;
- the Run overview composition and the workspace-wide Explore Catalog;
- reusable contextual connection/environment/settings presentation;
- a production Data Browser after the shell migration is stable;
- structure-aware canvas creation only after a backend capability contract
  exists;
- accessibility, responsive, deep-link, performance, and live-E2E coverage.

### 2.2 Explicitly out of scope for the shell migration

- changing Bruin project file formats;
- changing execution, deployment, schedule, or freshness semantics;
- replacing Jotai, TanStack Router, React Flow, Monaco, or the existing SSE
  synchronization model;
- persisting UI layout state to project files;
- treating warehouse objects discovered by the Data Browser as authored Renart
  assets without explicit import;
- redesigning the internals of the run-detail page, notebook editor, dashboard
  builder, report builder, or asset metadata editor beyond grouping its
  existing fields into the agreed tabs;
- an all-project warehouse catalog. The project launcher remains above the
  selected project shell;
- permanent support for two user-facing shells.

### 2.3 Two release trains, not one oversized change

The work is deliberately split into two release trains:

1. **Workbench migration:** rearrange existing production behavior. It should
   require no new backend contract.
2. **Workbench expansion:** add the Data Browser and structure-aware canvas
   drops. These require small, explicit backend contracts and ship after the
   shell has converged.

The first release train must not wait for the Data Browser backend. Conversely,
the product must not expose a non-functional Data Browser rail item merely to
make the migrated shell look complete.

## 3. Audited current baseline

This plan was checked against the feature worktree at `c116fae4` on
2026-09-01.

| Current area        | Production owner                                                 | Important behavior to preserve                                                                                            | Migration pressure                                                                   |
| ------------------- | ---------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| Global shell        | `web/components/app/app-shell.tsx`                               | one workspace SSE subscription, project switcher, execution context, vault, Git review, command palette, mobile safe area | header and six-item mobile navigation are flat; shell has no contextual slots        |
| Navigation model    | `web/components/app/app-navigation-model.ts`                     | typed Build/Run/Explore grouping added in `d1ec184d`                                                                      | still maps paths by prefixes and exports a six-item compatibility header             |
| Build               | `web/components/app/build-page.tsx`                              | route-owned Code/Split/Canvas, editor drafts, save barrier, canvas state, results, run/deploy review, real inspector      | page owns its own explorer, top bar, desktop grid, and two mobile sheets             |
| Build routes        | `web/src/routes/_shell/pipelines/...`                            | route-selected asset/view and validated result/editor search                                                              | no shell metadata; Ad-hoc is encoded as editor search state                          |
| Catalog             | `web/components/app/catalog-page.tsx`                            | real all-pipeline `AppLineageCanvas`, resolved asset-ID edges, staleness and materialization state                        | page duplicates header/filter/panel chrome and has no contextual catalog hierarchy   |
| Runs                | `web/components/app/runs-page.tsx`                               | paged/filterable history and production run detail with replay context, duration timeline, Events/Plan/Output             | no Run overview; list and detail are standalone pages                                |
| Schedules           | `web/components/app/schedules-layout.tsx`, `schedules-page.tsx`  | desired schedules, immutable deployment pins, projected/actual timeline, local scheduler state                            | separate top subnavigation and metadata-heavy timeline composition                   |
| Project settings    | `web/components/app/settings-pages.tsx`                          | real environment/connection forms and write-only secret handling                                                          | owns a second settings sidebar and full-page navigation                              |
| Connection creation | `web/components/app/workspace-connection-dialog.tsx`             | reusable create/verify flow and server-described fields                                                                   | can already support point-of-need creation; edit mode still lives in settings page   |
| Notebooks           | `web/components/app/notebook-page.tsx`                           | document controller, runtime/agent events, notebook tools sidebar, block inspector, mobile sheets                         | its authoring sidebars must integrate without lifting notebook state into `AppShell` |
| Presentations       | `web/components/app/presentation-builder/*`                      | shared authoring shell, builder sidebar, right inspector, responsive sheets                                               | same nested-sidebar problem as notebooks                                             |
| SQL discovery       | `web/lib/api-sql-discovery.ts`, `web/lib/atoms/sql-discovery.ts` | databases, tables, columns, bounded SQL query, process-session cache                                                      | enough for metadata prototypes but not a truthful revisioned Data Browser contract   |

The existing feature branch already contains the correct first foundation:
`appNavigationModes`, destination IDs, compatibility navigation, path-to-mode
tests, and a `data-app-mode` marker. That code should be evolved rather than
replaced.

## 4. Non-negotiable architecture rules

### 4.1 Sources of truth

- Workspace assets, pipelines, notebooks, presentations, connections, and
  environments continue to come from the backend workspace/config APIs and SSE
  reconciliation.
- The active asset, notebook, run, presentation, and shareable filters remain
  URL state.
- Monaco drafts remain owned by the current editor/document controllers.
- Running execution, notebook, schedule, and agent state remains owned by the
  existing hooks and result atoms.
- The workbench store contains only disposable presentation state. It must not
  cache credentials, file contents, materialization facts, schedule state, or
  warehouse rows.

### 4.2 One shell, migrated route by route

Do not create `AppShellV2` and copy every page into it. Extend `AppShell` with
workbench primitives that can wrap one migrated route at a time. A route
descriptor decides whether the outlet uses legacy page chrome or workbench
chrome during the transition. Removing a route from the migrated allowlist must
be a safe rollback that does not change its URL or domain component.

The temporary allowlist is an implementation migration mechanism, not a user
feature flag. It is deleted once all shell routes converge.

### 4.3 Pages keep domain state; the shell provides placement

Build, notebooks, and presentations have substantial local state. Moving that
state into `AppShell` would create a new monolith and introduce async races.
Instead, the shell owns named render slots:

- context-sidebar;
- page/document command bar;
- main work surface;
- right inspector;
- transient contextual overlay.

A page can contribute its existing sidebar or inspector to a slot while the
component remains inside the page's React ownership tree. A small portal/slot
contract is preferable to callback-registering dozens of page actions in the
shell. Only one contributor may own a slot at a time; development builds should
warn on collisions.

### 4.4 Do not keep hidden heavy pages mounted

Switching Build, Run, and Explore should not leave Monaco editors, React Flow
canvases, notebook runtimes, or presentation previews invisibly mounted.
Continuity comes from URLs, editor view-state restoration, and session UI state,
not from rendering every mode at once. The exception is a contextual Build
utility: it remains on the current Build route so the current canvas/editor is
still mounted behind the changed sidebar or bounded overlay.

### 4.5 Authored and observed state stay distinct

- Build and Catalog show Git-backed Renart assets.
- Data Browser shows observed warehouse/object-store objects.
- Previewing or pinning an observed table is session-only.
- Importing a source asset uses the existing reviewed backend mutation and only
  becomes authored after SSE reconciliation.
- Deployment state is not data freshness, and schedule declaration state is
  not the same record as its local deployment binding or run history.

## 5. Target shell contract

### 5.1 Desktop geometry

The workbench area below the global header is one shrink-safe row:

```text
┌ rail + contextual sidebar ┐  ┌ central work surface ┐  ┌ inspector ┐
│ one rounded outer surface │  │ page-owned contents  │  │ optional  │
└───────────────────────────┘  └───────────────────────┘  └───────────┘
```

- The rail and wide sidebar share one outer border, radius, background, and
  clipping context. They must never look like two adjacent cards.
- Gaps between the left workbench, central surface, and right inspector use one
  small shell spacing token.
- The context sidebar is bounded and resizable only after its collapse and
  React Flow/Monaco resize behavior is proven.
- The right inspector spans the same available workbench height as the combined
  left sidebar.
- Central surfaces decide their own internal vertical split; the shell does not
  own the Build result panel or notebook scrolling.
- Tabs are part of the central editor surface's top edge rather than a separate
  card floating above the editor.

### 5.2 Rail behavior

- Rail items are mode-specific and have stable string IDs.
- Clicking an inactive item selects it and expands the context sidebar.
- Clicking the active item a second time collapses the context sidebar.
- Reopening restores the same tool, selected object, expanded tree branches,
  width, and scroll position.
- There is no separate bottom “collapse sidebar” button.
- Every icon has an accessible label and tooltip; selected state does not rely
  on color alone.
- Common items appear first. Project-wide utilities stay grouped at the bottom
  and change with the current mode rather than becoming a permanent catalog of
  every Renart feature.

Recommended initial rail:

| Mode    | Primary tools                              | Contextual utilities                                                         |
| ------- | ------------------------------------------ | ---------------------------------------------------------------------------- |
| Build   | Project resources, Ad-hoc Query, Notebooks | Data Browser, Connections, Environments, Pipeline settings, Project settings |
| Run     | Overview, Deployments, Schedules, Runs     | environment context and project settings only where relevant                 |
| Explore | Catalog, Dashboards, Reports               | Data Browser and project settings                                            |

### 5.3 Context sidebar behavior

- The sidebar always explains the selected rail item; there is never a second
  unrelated hierarchy beside it.
- Master lists and trees live here: pipelines/assets, notebooks, connections,
  schemas/tables, deployments, schedules, recent runs, dashboards, and reports.
- Data Browser uses two explicit levels: connection overview, then one selected
  connection's hierarchy. Selecting a connection replaces the overview and
  introduces a Back action.
- Loading, empty, partial, error-with-cache, and unavailable states use shared
  sidebar primitives so one mode cannot silently blank the tree.
- Sidebar rows have stable focus targets and reveal hover-only actions on
  keyboard focus and touch.

### 5.4 Mobile translation

- The global mobile bottom navigation contains only Build, Run, and Explore.
- The narrow rail exists only at the desktop breakpoint. On mobile, its same
  mode-aware tool registry renders as a horizontally scrollable tab strip
  directly below the global header.
- Tool tabs and Build document tabs are separate layers: tool tabs choose
  Resources, Query, Notebooks, Data, or configuration; document tabs identify
  the open asset/query/notebook documents beneath them.
- Direct work surfaces such as Ad-hoc Query, Notebooks, Run views, dashboards,
  and reports switch in place. Dialog-backed utilities open their dialog.
- Selecting the active context-backed tool toggles one left Sheet containing
  only that tool's hierarchy. Selecting Resources from another Build tool
  opens that hierarchy immediately.
- The drawer has no redundant “Navigation and resources” visual header; its
  accessible title remains screen-reader-only, and it never repeats the mobile
  tool tabs as a second menu.
- “Back to workspace” closes the drawer when at the root; inside a Data Browser
  connection it first returns to the connection overview.
- Opening a dialog closes the drawer first. Closing the dialog restores focus
  to its initiating control.
- The active tab is indicated by text, icon, and underline; the tab strip is
  keyboard reachable and automatically scrolls the selected item into view.
- The current fixed 3.5rem row plus safe-area inset behavior remains unchanged.
- Mobile never renders a desktop rail and context sidebar side by side.

## 6. Routing and durable navigation state

### 6.1 Route descriptors

Evolve `app-navigation-model.ts` from prefix matching to a typed route
descriptor consumed from the deepest TanStack route match:

```ts
type AppRouteNavigation = {
  mode: "build" | "run" | "explore";
  tool: AppToolId;
  sidebar: AppSidebarKind;
  mobileLabel: string;
  workbench: boolean;
};
```

Use TanStack route `staticData` (with an augmented static-data type) or one
exhaustive route-ID registry if generated route typing makes `staticData`
unnecessarily fragile. Do not fall back to scattered
`pathname.startsWith(...)` checks. Unit tests must fail when a new shell route
has no descriptor or two tools claim the same stable ID.

Prefix matching remains only as a short-lived compatibility fallback until all
existing shell routes declare exact ownership.

### 6.2 URL map

Existing URLs remain valid. Add only one new first-class route during the shell
migration: `/run` for the Run overview. Do not repurpose `/runs`, because it is
already the canonical searchable history and run-detail parent.

| URL                          | Mode/tool                                   | Target surface                                   | Compatibility rule                      |
| ---------------------------- | ------------------------------------------- | ------------------------------------------------ | --------------------------------------- |
| `/`                          | Build/resources                             | redirect to first pipeline as today              | unchanged                               |
| `/pipelines/$pipelineId/...` | Build/resources or contextual Build utility | current canvas/editor/results                    | unchanged path and asset/view semantics |
| `/notebooks`                 | Build/notebooks                             | notebook list                                    | unchanged                               |
| `/notebooks/$notebookId`     | Build/notebooks                             | current notebook authoring surface               | unchanged                               |
| `/run`                       | Run/overview                                | new compact operational overview                 | new route and Run header default        |
| `/runs`                      | Run/runs                                    | complete searchable run history                  | unchanged                               |
| `/runs/$runId`               | Run/runs                                    | current run detail                               | unchanged                               |
| `/schedules`                 | Run/schedules                               | desired schedules and bindings                   | unchanged                               |
| `/schedules/deployments`     | Run/deployments                             | immutable deployment history                     | unchanged initially                     |
| `/schedules/timeline`        | Run/overview compatibility                  | redirect or link to `/run` once parity is proven | preserve query state while migrating    |
| `/catalog`                   | Explore/catalog                             | workspace-wide lineage canvas                    | unchanged                               |
| `/dashboards...`             | Explore/dashboards                          | current library/builder                          | unchanged                               |
| `/reports...`                | Explore/reports                             | current library/builder                          | unchanged                               |
| `/project/...`               | Build/project settings by default           | full-page canonical management                   | unchanged and reachable globally        |
| `/data`                      | Explore/data                                | production Data Browser                          | added only when functional              |

### 6.3 Contextual Build utilities without losing the canvas

Connections, Environments, Pipeline settings, and the Build entry to Data
Browser must not navigate away from the current pipeline. Extend the validated
Build search model with a small, explicit workbench context:

```text
tool=resources | data | connections | environments | pipeline-settings
connection=<safe connection identity>
database=<safe discovered identity>
schema=<safe discovered identity>
table=<server-issued or safely encoded discovered identity>
settingsSection=<known section ID>
```

Only shareable selection belongs in the URL. Sidebar width, expanded branches,
scroll offsets, and dialog openness do not. Opening a contextual utility keeps
the current asset/view/result/editor search values intact. Closing it removes
the utility keys and restores the same canvas/editor viewport.

`/project/connections` and `/project/environments` remain canonical management
pages for direct links. Contextual Build uses the same extracted domain
controller/forms in a sidebar plus overlay; it does not render the full settings
page behind the canvas.

When `/data` ships, Explore uses it as the full Data Browser route. Build uses
the same Data Browser controller and API state through the contextual search
keys above. This gives one domain implementation without forcing the Build
canvas to be recreated as a fake background route.

### 6.4 State ownership table

| State                                         | Owner                               | Persistence                               |
| --------------------------------------------- | ----------------------------------- | ----------------------------------------- |
| mode, tool, active resource, shareable filter | TanStack route/path/search          | browser history and deep link             |
| current project                               | existing project context            | per browser tab as today                  |
| assets/config/notebooks/presentations         | backend DTO + SSE/Jotai             | authoritative server/filesystem           |
| run/schedule/runtime facts                    | existing APIs, hooks, event atoms   | backend/local state DB                    |
| active draft and pending saves                | existing editor/document controller | current session, server after save        |
| rail open, sidebar width, last tool per mode  | `useWorkbenchSessionState`          | versioned project-scoped `sessionStorage` |
| expanded tree nodes and sidebar scroll        | sidebar-specific session record     | capped/versioned `sessionStorage`         |
| open Build document IDs and order             | Build document controller           | capped project-scoped `sessionStorage`    |
| Monaco cursor/selection/scroll                | editor view-state cache             | in-memory session; no contents            |
| pinned Data Browser previews                  | Data Browser session controller     | capped project-scoped `sessionStorage`    |
| hover, drag, temporary menu, dialog           | local component state               | none                                      |

The workbench session record needs a schema version and safe parser. Invalid,
oversized, or stale data falls back to defaults. Use the existing per-tab
project identity; do not use a global key shared by unrelated project roots.

## 7. Frontend component architecture

### 7.1 Shell primitives

Add a focused folder such as `web/components/app/workbench/`:

```text
workbench/
  workbench-layout.tsx
  workbench-rail.tsx
  workbench-context-sidebar.tsx
  workbench-mobile-tool-tabs.tsx
  workbench-mobile-drawer.tsx
  workbench-slots.tsx
  workbench-session-state.ts
  workbench-session-state.test.ts
  workbench-route-model.test.ts
```

Responsibilities:

- `AppHeader` is extracted from `AppShell` and renders the project context,
  three modes, execution selector, vault, Git, and command palette.
- `AppWorkbenchLayout` owns geometry, responsive hosts, the combined rounded
  left surface, and optional inspector column.
- `AppWorkbenchRail` renders the active mode's tool registry and implements the
  second-click collapse rule.
- `AppWorkbenchMobileToolTabs` renders the same registry below the header only
  on mobile; it switches direct surfaces and delegates context-backed tools to
  the drawer without duplicating the tool list inside it.
- `AppContextSidebarFrame` provides shared title/search/back/loading/error/empty
  behavior but receives domain content.
- `WorkbenchSlot` / `WorkbenchPortal` lets the routed page contribute live
  domain components without lifting their state.
- `useWorkbenchSessionState(projectId)` wraps one reducer and versioned
  `sessionStorage`; it exposes semantic actions rather than individual setters.
- `AppMobileWorkbenchNavigation` renders the same registry as the rail and
  points the contextual slot at one Sheet host.

Avoid a general-purpose dock/window framework. Renart needs these named slots,
not arbitrary user-arranged panels.

### 7.2 Workbench reducer

Use explicit actions:

```text
route-entered
mode-entered
tool-selected
active-tool-toggled
sidebar-resized
resource-selected
tree-branch-toggled
document-opened
document-closed
document-reordered
preview-pinned
state-restored
project-changed
```

Reducer invariants:

- exactly one active mode and tool;
- an active tool can have its sidebar collapsed without losing selection;
- state is partitioned per mode and project;
- entering a route updates mode/tool but does not overwrite unrelated mode
  history;
- project changes cannot retain resource IDs from the previous Git root;
- closing the active document chooses an adjacent valid document or its mode's
  default surface;
- removed workspace resources are pruned only after authoritative workspace
  reconciliation, not on a transient loading response.

### 7.3 Slot contract for stateful pages

Build, notebooks, and presentations should render their sidebar/inspector
through portal components located inside their current controller. This keeps:

- Build selection, inspector focus, review dialogs, and save barriers in
  `AppBuildPage`;
- notebook outline/data/add/AI state in `AppNotebookLivePage`;
- dashboard/report selection, preview, undo, and inspector state in
  `PresentationBuilder`.

The shell slot API should expose only placement and basic chrome metadata. It
must not accept page-specific callbacks such as `runNotebook`, `saveAsset`, or
`updateVisualization`.

On legacy routes or when a slot host is absent, page adapters can render the
same content in their current local position. This makes each route migration
reversible while the transition is active.

### 7.4 Shared visual primitives

Add Renart-specific primitives rather than composing every area from generic
cards:

- connected operational readout strip;
- compact master-detail row;
- workbench section heading;
- status-aware timeline row and duration bar;
- resource-tree row;
- workbench tab and tab overflow menu;
- sidebar async-state notice;
- inspector section.

Use cards only for independent objects or previews. Catalog, run history,
schedules, deployment lists, and attention items default to continuous tables,
rows, and dividers.

## 8. Production migration by area

### 8.1 Build workbench

#### Layout migration

1. Extract the existing `Explorer` into a Build resource-sidebar component
   without changing its workspace or selection inputs.
2. Render it through the shell context-sidebar slot. The narrow rail owns
   whether Resources, Ad-hoc, Notebooks, Data Browser, or Settings is active.
3. Remove the Build-owned desktop outer grid only after the slot version is
   live. Keep the current vertical `PanelGroup`, result panel, and canvas/editor
   outlet untouched.
4. Render the existing real asset inspector through the shell inspector slot.
   Preserve every guided metadata section, dependency link, quality check, and
   action; the mock inspector is not production code.
5. Remove Build's duplicate explorer/inspector mobile Sheets after the shared
   mobile shell covers both.
6. Let the shell own equal-height columns and small outer gaps. Build continues
   to own the internal editor/result resize handle.

#### Tabbed metadata inspector

The production asset metadata editor should follow the mock experiment only
after its domain state is separated from its current long-form presentation:

- **General** contains identity, owner, description, tags, URI, connection,
  materialization, and type-specific hooks/options;
- **Lineage** contains navigable inferred/explicit dependencies and manual
  dependency creation;
- **Columns** contains schema derivation, editable columns, types,
  descriptions, and column behavior;
- **Checks** contains asset-level SQL checks and column checks.

The tab bar is a presentation boundary, not four independent forms. One asset
metadata controller owns pending values, validation, provenance, and save state
across tabs. Switching tabs must not save, reset, or discard an unfinished
field. Asset-type capabilities determine whether Columns or Checks are present;
relationless assets do not receive empty schema tabs merely for visual
consistency.

Programmatic inspector entry points must select the correct tab before focusing
their target. In particular, failed-check navigation opens Checks, schema
findings open Columns, and dependency links/open-add actions open Lineage. Tab
badges may show counts or issue state, but they must not imply that unloaded
metadata is an empty list. Radix tab keyboard behavior, mobile Sheet rendering,
and accessible tab/panel relationships are required.

#### Document bar and tabs

Replace `BuildTopBar` in a separate slice after shell geometry is stable:

- the document tab row and Code/Split/Canvas icon controls share one rounded
  central top surface;
- remove the extra per-editor filename header where the tab already identifies
  the document;
- keep Deploy and Review run as actions for asset/pipeline documents;
- icon-only Code/Split/Canvas controls retain tooltips and accessible names;
- an Ad-hoc Query is one pipeline-scoped special document;
- notebook documents use the same tab strip when navigated within Build;
- notebook-specific Run all/Stop actions replace asset actions while a notebook
  tab is active;
- mobile shows one active-document control plus an open-document picker, not a
  squeezed desktop tab strip.

The route remains the active-document authority. The tab controller stores only
IDs/order and derives labels/kinds from the reconciled workspace. It never owns
file text.

#### Editor safety

- Persist Monaco view state before route/tab change and restore it on return.
- Initially mount only the active Monaco editor; do not keep one editor per tab.
- Await the existing workspace save barrier before closing a dirty tab, running,
  deploying, or switching to an operation that requires saved source.
- A save failure keeps the document open and visibly failed; discard requires
  explicit confirmation.
- Late inspect/render/materialize responses remain keyed by asset/run identity
  and cannot appear under a newly selected tab.
- React Flow viewport restoration remains owned by the lineage canvas; sidebar
  collapse triggers a resize/fit check but never an unconditional `fitView`.

#### Ad-hoc and notebooks

- Ad-hoc Query and Notebooks are rail tools because they replace the central
  canvas/editor composition.
- Opening either creates/selects a document tab and navigates to its existing
  route representation.
- Data Browser and settings tools are different: they change the wide sidebar
  and may open a bounded overlay while leaving the current Build document
  mounted.
- Ad-hoc Query must use the central surface directly, with no nested explanatory
  card around its editor/results.
- Notebook tools should occupy the shared wide-sidebar slot. Notebook block
  inspector remains the right inspector slot. No notebook controller state is
  moved into the shell.

### 8.2 Connections, environments, and settings

Start by extracting presentation, not data logic:

- retain `useWorkspaceSettingsData`, `useWorkspaceConnectionForm`,
  `WorkspaceConnectionFormFields`, and server-described connection types;
- reuse `WorkspaceConnectionDialog` for point-of-need creation;
- extend the reusable connection editor to support edit mode before removing
  the settings-page-only `ConnectionSheet`;
- reuse the current environment form/policy controller in a contextual detail
  overlay;
- adapt `PipelineSettingsDialog` content to an embeddable body/controller while
  retaining its current reducer, dirty confirmation, schedule link, and fixed
  full-height behavior;
- keep `/project/*` full management pages as wrappers around the same extracted
  surfaces;
- remove `SettingsShell`'s duplicate sidebar only after the shared rail/context
  sidebar handles those routes.

On create/update success, wait for the mutation response and matching workspace
reconciliation before selecting the new/renamed connection. Secret fields stay
write-only and must never be copied into workbench session or route state.

### 8.3 Run

#### Navigation

The Run rail contains Overview, Deployments, Schedules, and Runs. Its context
sidebar changes accordingly:

- Overview: pipelines, health, and active context;
- Deployments: version list grouped by pipeline/environment;
- Schedules: desired schedule/binding rows;
- Runs: filters and recent run list, with the selected run highlighted on the
  detail route.

Remove the standalone schedule subnavigation after rail parity is complete.
Do not add another Run/Explore page header bar; common actions belong in the
sidebar or the surface's compact command row.

#### Overview

Add `/run` and a `useRunOverviewModel` view model that composes existing
schedule, deployment, run, and workspace data without changing their contracts.
The surface contains:

1. the compact connected readouts for active deployment, next projected run,
   runs today, and selected environment;
2. one compact projected/actual timeline;
3. Needs attention and Deployment readiness rows.

Timeline rules:

- projected occurrences use a dashed/outlined treatment;
- actual runs retain status color, border/fill encoding, and non-color labels;
- actual bar width represents duration on the time axis;
- manual runs are shown even when they do not align with a schedule;
- very short runs retain a minimum hit target while their true duration remains
  available in the tooltip/detail;
- overlapping runs use deterministic lanes rather than hiding one another;
- row text stays sparse: pipeline plus cadence/context, not the complete
  schedule metadata block from today's Schedules page;
- clicking an actual run goes to `/runs/$runId`; clicking a projection opens
  its schedule/deployment context.

Reuse/extract the schedule timeline calculations from `schedules-page.tsx`
rather than implementing a second time-axis model. Keep projected occurrences
and actual runs as different typed records until the final render step.

If composing existing endpoints creates measurable request fan-out, add one
read-only aggregate overview endpoint in a later performance slice. Do not
preemptively duplicate scheduler truth in a frontend cache.

#### Deployments, schedules, runs, and run detail

- Keep their current hooks and mutation flows.
- Recompose deployment and schedule master/detail views inside the shell.
- Schedule creation remains available from Schedules and becomes an explicit
  post-deploy/deployment-detail action.
- Keep Runs as the complete paged/filterable table.
- Wrap `AppRunDetailPage`; do not redesign its replay context, asset-duration
  timeline, Events, Plan, Output, cancellation, or exact re-execution behavior.
- Preserve run search state when moving list → detail → Back.

### 8.4 Explore

#### Workspace Catalog

- Reuse the production `AppLineageCanvas` and its real workspace-wide resolved
  asset-ID edges.
- Move Catalog filtering and pipeline/asset hierarchy into the Explore context
  sidebar and keep compact canvas actions in the central surface.
- Preserve `?asset=` deep links, selected-node focus, staleness, live run state,
  asset actions, and Open in Build.
- Show all workspace pipelines on one canvas, including valid cross-pipeline
  dependencies.
- Do not merge warehouse-only observed tables into this canvas. Data Browser
  remains a separate Explore tool.

#### Dashboards and reports

- Index routes contribute dashboard/report lists to the context sidebar.
- Artifact routes contribute the existing builder sidebar to the same slot.
- The current `DocumentAuthoringShell`, undo/redo, preview, dataset state,
  filter/control editors, and right inspector stay owned by
  `PresentationBuilder`.
- Remove duplicate page/title/view bars only after their actions have a clear
  home in the shared document command bar.
- Preserve scrollability for canvases larger than the viewport.
- Keep type-check/deployment-safety links back into Build and deployment review.

## 9. Data Browser expansion

This starts only after the shell and contextual Build utility model are stable.

### 9.1 MVP behavior

- show configured connections for the selected environment;
- offer six to eight common warehouse shortcuts using the real shared
  connection-type icons and an All types path;
- create a connection through `WorkspaceConnectionDialog`;
- enter one selected connection and browse database/catalog → schema → table;
- load metadata lazily per expanded level;
- show columns and safe connection/environment identity;
- offer explicit Preview, Open in Ad-hoc Query, Create source asset, Use as Load
  input, and Add to notebook actions when supported;
- never query rows merely because a tree node was expanded;
- keep a capped session list of pinned/recent objects.

### 9.2 Minimal backend contract required for preview

Existing discovery endpoints can bootstrap hierarchy loading, but a production
table preview should not ask React to concatenate warehouse identifiers into
SQL. Add a small server-owned preview operation:

```text
POST /api/data-browser/preview
```

It accepts the selected environment/connection plus a server-resolved or
validated discovered-object identity and a bounded row limit. The service owns
engine-aware quoting and read-only validation, reuses the shared query result
envelope, and returns truncation, elapsed time, observation scope, and source
identity. No credential value enters the response.

### 9.3 Durable discovery contract

After MVP evidence, converge the three discovery calls into a service-level
contract that can truthfully represent:

```text
idle | discovering | ready | refreshing | partial |
error-with-cache | error-empty | empty
```

The response includes revision, observation time, requested scope, cache/live
provenance, partial findings, and capabilities. A refresh keeps last-known-good
nodes visible. Configuration changes invalidate the exact connection identity;
targeted SSE completion events replace polling.

Object stores use adapters behind the same hierarchy/capability envelope.
Listing, schema inspection, and row preview are separate capabilities.

## 10. Structure-aware canvas creation expansion

The mock's drag matrix becomes production behavior only after the backend emits
and validates an asset capability record:

```text
produces_relation
accepts_upstreams
root_preferred
execution_gate
validation_attachment
supports_source_role
supports_sink_role
```

Implementation rules:

- New Asset dialog and canvas palette consume the same capability model.
- Drop targets appear only where the requested relationship is meaningful.
- The backend revalidates every create request; frontend drag state is not
  authorization or schema truth.
- Source/seed/root assets, relation-producing transforms, Load sinks, sensors,
  and unit tests have distinct target semantics.
- Gate/test attachment must not create fake materialized lineage nodes.
- Keyboard and menu actions provide Add upstream, Add downstream, Attach gate,
  and Add test equivalents.
- Invalid drops explain why and do not silently coerce the asset type.

This expansion should not block the visual shell migration.

## 11. Ordered implementation slices

Each slice is intended to be independently reviewable and revertible. A slice
may use several commits, but unrelated route migration, backend discovery, and
canvas mutation must not share one commit.

### Slice 0 — Baseline and migration harness (complete foundation, small)

- Keep `d1ec184d` as the starting registry.
- Add route-ownership exhaustiveness tests and a migration allowlist.
- Add stable test IDs/accessible names only where current E2E cannot address a
  shell element semantically.
- Record current desktop/mobile screenshots and bundle chunks for Build,
  Notebook, Catalog, Schedules, Runs, Run detail, Dashboard, and Report.

Exit: no visible production change; tests can identify legacy versus migrated
routes.

### Slice 1 — Shell primitives behind the allowlist (medium)

- Extract `AppHeader` and three-mode navigation without switching the visible
  compatibility header yet.
- Add workbench layout, rail, context slot, inspector slot, mobile Sheet host,
  reducer, and session parser.
- Prove same-click toggle, project partitioning, focus restoration, and corrupt
  session fallback in unit/component tests.

Exit: a test route can use the real shell primitives; current production routes
remain visually unchanged.

### Slice 2 — Read-only pilot: Explore Catalog (medium)

- Mark `/catalog` migrated.
- Move its filter/resource hierarchy to the context sidebar slot.
- Render the real Catalog canvas in the central rounded surface.
- Verify selected asset deep links, React Flow resize, collapse/expand, mobile
  drawer, and Open in Build.

Exit: the first production route proves shell geometry without editor or
mutation risk. Rollback is removing one route from the allowlist.

### Slice 3 — Investigation pilot: Runs and run detail (medium)

- Mark `/runs` and `/runs/$runId` migrated.
- Put filters/recent runs in the context sidebar while keeping the full history
  table and run detail domain components.
- Preserve search state, Back, cancellation, rerun, live events, terminal
  follow-scroll, and mobile table behavior.

Exit: a stateful SSE-backed page proves that the shell does not duplicate or
lose live updates.

### Slice 4 — Run overview and schedule/deployment navigation (large)

- Add `/run`, `useRunOverviewModel`, compact readouts, timeline, and attention
  rows.
- Extract/reuse timeline calculations.
- Migrate deployments and schedules into Run rail/context sidebars.
- Keep the old schedule subnav until direct-link and mobile parity passes, then
  remove it.

Exit: every existing Run/Schedule capability is reachable from the Run rail and
the overview distinguishes projected versus actual runs with duration.

### Slice 5 — Build outer shell and real inspector (large, high risk)

- Move Explorer and Inspector through slots.
- Keep Build's route layout, central Outlet, result panel, dialogs, and save
  barriers unchanged.
- Replace Build-owned outer desktop grid and mobile sheets with shared shell
  geometry.
- Introduce the tabbed metadata presentation over the existing guided editor
  controller; preserve cross-tab drafts and route failed-check/schema/dependency
  focus to the correct tab.
- Verify canvas viewport, hover-to-node highlighting, selected asset, result
  tabs, inspector check focus, and protected-environment actions.

Exit: the existing single-document Build experience is functionally identical
inside the new shell.

### Slice 6 — Build document controller and tabs (large, high risk)

- Add capped project-scoped open-document state.
- Integrate tabs with the existing active route.
- Restore Monaco view state; keep only the active editor mounted.
- Merge tabs and view/action controls into one central top surface.
- Remove the redundant editor filename header.
- Preserve late-response identity and save-failure behavior.

Exit: multiple assets can be opened/closed/navigated without losing drafts,
cursor position, canvas selection, or browser history.

### Slice 7 — Ad-hoc and Notebook Build documents (large)

- Make Ad-hoc and Notebooks rail tools and document tabs.
- Keep Ad-hoc on its existing pipeline/search semantics initially.
- Contribute notebook tools and block inspector through workbench slots.
- Remove duplicate notebook mobile sheets only after shared drawer/inspector
  parity.
- Preserve notebook runtime, cancellation, agent chat, outline jumps, cell
  selection, and source approvals.

Exit: assets, Ad-hoc Query, and notebooks behave as peer Build documents while
their current controllers remain authoritative.

### Slice 8 — Contextual settings (medium to large)

- Add Build search-state utility context.
- Embed connection/environment/pipeline-settings controllers in sidebar and
  bounded overlays.
- Extend reusable connection editing and retain canonical project settings
  routes.
- Remove duplicate `SettingsShell` navigation after parity.

Exit: a user can create/edit prerequisites at the point of need and return to
the unchanged Build document.

### Slice 9 — Explore presentations (large)

- Migrate dashboard/report indexes and live builders.
- Portal existing builder sidebars/inspectors into shared slots.
- Remove redundant bars and preserve all visual/definition, undo/redo,
  responsive preview, dataset, filter, and type-check behavior.

Exit: Explore contains Catalog, Dashboards, and Reports with one coherent
navigation hierarchy and no nested page shell.

### Slice 10 — Switch global header and mobile navigation (small to medium)

- Render Build, Run, and Explore from `appNavigationModes`.
- Point Run to `/run`; preserve direct links to every former header item through
  rails, context sidebars, command palette, and URLs.
- Replace six mobile items with three.
- Replace the hidden mobile rail with the context-sensitive tool-tab strip and
  make the Sheet a content host rather than a nested navigation menu.
- Remove compatibility `currentHeaderNavItems` only after the route coverage
  test proves every old destination is reachable.

Exit: the user-facing navigation is the approved three-mode design.

### Slice 11 — Data Browser MVP (large, separate backend commit)

- Add `/data`, controller, sidebars, hierarchy, connection chooser, creation,
  explicit preview, and contextual Build adapter.
- Add the safe preview endpoint.
- Reuse shared result table and authored/observed identity matching.
- Test DuckDB, project-scoped local files, credential-gated PostgreSQL, and one
  three-level warehouse.

Exit: the rail item is visible only when its complete MVP works.

Implementation checkpoint (2026-09-02): the route, shared controller, Build
overlay, connection chooser/creation, revision-bound hierarchy, DuckDB and
project-file discovery, Preview/Columns UI, shared result grid, and mobile tool
tabs are implemented. Authored/observed matching, Usage, downstream handoffs,
abort propagation, and the PostgreSQL/three-level live matrix are still needed
before this slice reaches its exit criterion.

### Slice 12 — Durable discovery and object stores (large, optional release)

- Add revisioned discovery contract, partial/cache states, targeted SSE, and
  adapter capabilities.
- Add object-storage hierarchy where supported.
- Add recent/pinned restoration and invalidation.

Exit: the browser can truthfully survive refresh failures and expensive remote
catalogs without pretending stale data is live.

### Slice 13 — Canvas capability contract and drops (large, separate feature)

- Add backend capability DTO and validation.
- Wire palette, targets, dialogs, keyboard actions, and reviewed mutations.
- Add matrix unit tests and live file/lineage assertions.

Exit: drag/drop is an accelerator over the same valid create flow, not a second
asset-creation system.

### Slice 14 — Convergence and deletion (medium)

- Remove route allowlist, path-prefix compatibility, legacy shell branches,
  duplicate sidebars/sheets/subnav, and dead design-only adapters.
- Run unused export/dependency checks.
- Fold the as-built shell into `architecture/frontend.md`.
- Delete `/navigation-lab` routes and mock-only data after final visual review.
- Delete this plan and the mock study after their remaining expansion work is
  either shipped or split into focused plans.

Exit: there is one production shell and one documented implementation.

## 12. Dependency graph and recommended commit boundaries

```text
route descriptors
  └─ shell primitives
      ├─ Catalog pilot
      ├─ Runs/detail pilot ─ Run overview ─ schedules/deployments
      └─ Build shell ─ document tabs ─ Ad-hoc/Notebooks ─ contextual settings
                                     └─ presentation slot pattern

contextual settings + shell stable
  └─ Data Browser MVP ─ durable discovery

backend asset capabilities
  └─ structure-aware canvas drops

all migrated areas
  └─ three-item header/mobile switch ─ convergence cleanup
```

Recommended commit boundaries within a slice:

1. pure model/reducer/tests;
2. reusable shell or domain extraction with no visual change;
3. route adapter and visible composition;
4. live-E2E/accessibility fixes;
5. deletion of replaced code only after verification.

Never combine a large component move with changed API semantics in the same
commit. That makes regressions difficult to classify and rollback unsafe.

## 13. Verification strategy

### 13.1 Unit and component coverage

- exhaustive route → mode/tool/sidebar mapping;
- workbench reducer transitions and project partitioning;
- session-state versioning, bounds, corrupt input, and removed resources;
- second-click rail collapse and restored selection;
- document open/close/reorder/adjacent fallback;
- Build search normalization retaining result/editor/view context;
- projected/actual timeline lane and duration calculations;
- Data Browser state machine, capability filtering, and identity invalidation;
- asset drop capability matrix;
- no-color status labels and accessible names.

### 13.2 Live E2E matrix

Add one focused `navigation-workbench.live.spec.ts` for shell behavior and keep
domain scenarios in their existing files.

For every migrated route verify:

- direct load and refresh;
- browser Back/Forward;
- rail and context selection;
- sidebar collapse/reopen and same-item toggle;
- project and environment switch;
- command-palette navigation;
- mobile tool-tab overflow, active-state restoration, drawer
  open/toggle/back/close, and focus return;
- no document-level horizontal overflow;
- no duplicate workspace fetch loop or EventSource;
- SSE updates still reach the selected surface;
- legacy URL remains valid.

High-risk existing suites that must remain green:

- `build-editor.live.spec.ts` and `build-actions.live.spec.ts`;
- `asset-editing.live.spec.ts` and SQL intellisense suites;
- `notebooks.live.spec.ts`, notebook cancellation, agent, and autorecompute;
- `scheduler.live.spec.ts` and run recovery;
- presentation builder/type-check live coverage;
- freshness/catalog failure coverage;
- mobile Chromium variants.

### 13.3 Responsive and visual review

Review at 360, 412, 768, 1024, 1280, and 1440 CSS pixels:

- combined left sidebars remain one rounded surface;
- small gaps and rounded central/inspector surfaces are consistent;
- tabs and editor are one surface;
- right inspector height matches the workbench region;
- no nested card around Ad-hoc Query;
- no redundant Run/Explore page header bars;
- large reports/dashboards and run tables remain scrollable;
- bottom navigation respects Android/iOS safe areas;
- dark mode retains status and selected-state contrast.

Use real example-workspace data for screenshots. Mock-only data is not evidence
that the production shell handles long names, empty states, parse errors,
protected environments, or live execution updates.

### 13.4 Performance gates

Capture before/after traces for Build, Notebook, Catalog, and Run detail:

- initial JS/CSS chunks and route lazy-loading;
- React commit count during rail/sidebar toggles;
- number of workspace/config/run/discovery requests;
- number of EventSource connections;
- Monaco model/editor count after opening several document tabs;
- React Flow viewport stability during sidebar and inspector resize;
- time and request fan-out for Run overview and Data Browser expansion.

Required outcomes:

- no second workspace synchronization loop;
- no hidden editor/canvas per inactive mode;
- no unbounded document or preview list;
- Data Browser code stays lazy outside its route/context;
- existing bundle budgets remain green;
- performance regressions are explained and fixed before removing the legacy
  composition for that route.

### 13.5 Release gate per migrated route

1. focused unit/component tests;
2. formatting, lint, generated API types, TypeScript, and production web build;
3. relevant live E2E on desktop and mobile;
4. browser screenshot pass in light and dark mode;
5. direct-link/back-forward/manual keyboard pass;
6. clean bundle budget;
7. one release with the migrated route before deleting its legacy wrapper;
8. full `make release-check` before convergence.

## 14. Rollback and failure containment

- Route migration is controlled by one descriptor/allowlist. A failing route
  returns to legacy composition without changing URL or backend state.
- Domain component extraction happens before visual relocation and stays usable
  in both placements until parity passes.
- Session-state versions can be invalidated without data loss because they hold
  no domain content.
- A Data Browser failure leaves the last-known-good hierarchy visible and never
  affects Build workspace state.
- Run overview aggregation is read-only; failure cannot block Runs, Schedules,
  Deployments, or direct run detail.
- Canvas drag/drop remains disabled until the backend contract is available;
  New Asset dialog remains the complete fallback.
- Do not delete old navigation, sheets, or subnav in the same commit that first
  enables their replacement.

## 15. Locked decisions and deferred refinements

### Locked for implementation

- Build, Run, Explore stay in the top header.
- The rail and context sidebar form one rounded left surface.
- The active rail item toggles its sidebar on a second click.
- `/run` is the overview; `/runs` remains complete history.
- Run detail remains functionally the current Renart page.
- Explore Catalog is the real all-pipeline workspace lineage canvas.
- Build settings/Data Browser context does not unmount the current canvas.
- Ad-hoc Query and Notebooks are peer Build documents and do replace it.
- Pages contribute stateful sidebars/inspectors through slots; their domain
  state is not lifted into `AppShell`.
- Actual run bars encode status and duration; projections remain visually
  distinct.
- The Data Browser and Catalog remain different trust domains.
- Mobile uses one contextual drawer and three bottom modes.

### Defer until evidence exists

- user-resizable sidebar width persistence beyond the initial bounded desktop
  implementation;
- a replaceable italic preview tab for Data Browser objects;
- a Run overview aggregate backend endpoint;
- object-storage browsing beyond adapters with demonstrated metadata support;
- remote catalog search when a warehouse adapter cannot do it cheaply;
- saved Git-backed query documents separate from notebooks/assets;
- customizable rail order or user-created shell layouts.

These refinements must not block the core migration or turn the shell into a
general window manager.

## 16. First implementation checkpoint

The next implementation work should stop after Slices 0–3 and review the result
before touching Build:

1. route descriptors and workbench reducer;
2. shell/slot/mobile primitives behind the allowlist;
3. real Catalog migrated as a read-only pilot;
4. real Runs list and Run detail migrated as an SSE-backed pilot;
5. desktop/mobile browser evidence plus the complete frontend check.

That checkpoint validates the architecture with both a React Flow surface and
a live operational surface. Only then should the migration move the much larger
Build, Notebook, and Presentation controllers.
