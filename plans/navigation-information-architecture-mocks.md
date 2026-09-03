# Navigation and workspace information architecture — mock study

Status: interactive design study complete; the approved production execution
sequence lives in
[navigation-workbench-migration.md](navigation-workbench-migration.md) and has
started with the route/navigation-model foundation. The design-lab routes remain
frontend-only and cannot change workspace files.

## 1. Problem

Renart currently presents Build, Catalog, Notebooks, Present, Runs, and
Schedules as six peers in the top bar. Project switching, execution context,
Git, vault state, appearance, connections, and environments compete for the
remaining header space. That works while each surface is small, but it does not
express the product lifecycle:

1. connect data and author a pipeline;
2. inspect, query, and materialize while iterating;
3. freeze that work as a deployment and assign schedules;
4. observe projected and actual runs;
5. discover and present trusted outputs.

The current Build explorer already mixes pipelines, assets, ad-hoc SQL,
notebooks, and pipeline settings. Meanwhile, connections and environments are
several navigation layers away even though they are prerequisites for building.
Schedules, deployments, and run history are implemented as separate pages even
though users reason about them as one operating workflow.

## 2. Design objectives

- Keep a small set of durable top-level pages visible in the header rather than
  moving primary navigation into a sidebar.
- Make Build and Run recognizable workflows, not collections of pages.
- Keep the canvas and editor as the main Build surface.
- Put project resources and contextual tools in a left workbench while the
  global header retains the page-level navigation.
- Make connections and environments reachable from Build without turning them
  into Build-only settings.
- Treat the Data Browser as a useful working surface that can replace the
  resource sidebar, not as another cramped accordion inside it.
- Make deployment, schedule creation, projected runs, and actual runs read as
  one continuous operating flow.
- Keep Catalog, dashboards, and reports close without claiming they are the
  same task.
- Preserve deep links and a viable mobile model. Mobile replaces only the
  narrow desktop rail with contextual tool tabs below the header and shows one
  contextual sheet at a time; it never squeezes two desktop sidebars beside the
  editor.
- Keep every visual change compatible with Git-backed, local-first Renart.

## 3. Prior-art lessons

The mocks borrow interaction patterns, not product structure or styling:

- [VS Code's workbench model](https://code.visualstudio.com/api/ux-guidelines/overview)
  couples an Activity Bar to a contextual Primary Sidebar and reserves the
  Secondary Sidebar for auxiliary work. Its own
  [sidebar guidance](https://code.visualstudio.com/api/ux-guidelines/sidebars)
  recommends few view containers and roughly three to five views per sidebar.
  Renart should adopt the separation of global page and contextual resource,
  but keep its global pages in the header rather than copying VS Code's Activity
  Bar placement. It should not copy VS Code's configurability or density.
- Shadcn's [`sidebar-09`](https://ui.shadcn.com/blocks/sidebar#sidebar-09)
  composes a collapsible icon rail and a second contextual sidebar within one
  provider. It validates the structural basis of variant A and, importantly,
  collapses the composition on mobile. Renart should reuse its shared state
  model, but translate the rail into visible tool tabs and reserve the drawer
  for the selected tool's contents, while keeping the existing Mira primitives
  and avoiding a generated overwrite of shared components.
- [Hex's Data Browser](https://learn.hex.tech/docs/explore-data/data-browser)
  is available inside an authoring project, can expand to a wider surface, and
  offers search, preview, query, recent, and favorite actions. Renart should
  similarly let a table become an ad-hoc query, source asset, Load input, or
  notebook source from one place.
- [Databricks' workspace browser](https://docs.databricks.com/gcp/en/workspace/workspace-browser)
  keeps a stable tree while the editor changes and supports a focused authoring
  context. Its broader
  [workspace navigation](https://docs.databricks.com/gcp/en/workspace/navigate-workspace)
  also demonstrates the failure mode Renart should avoid: a sidebar can become
  another long product catalog when every capability earns a permanent item.
- [Prefect deployments and schedules](https://docs.prefect.io/v3/how-to-guides/deployments/create-schedules)
  place schedule creation on a deployment and show upcoming runs there. This
  matches Renart's immutable deployment model better than treating Schedules as
  an unrelated top-level destination.
- [Airflow's current UI](https://airflow.apache.org/docs/apache-airflow/stable/ui.html)
  separates a compact overview/grid from a complete sortable Runs view. Renart
  should keep both: a small projected/actual timeline for orientation and a
  dedicated run list for investigation.
- [Postman's workbench](https://learning.postman.com/docs/getting-started/basics/navigating-postman)
  keeps the header, switchable sidebar contents, central workbench, and
  element-specific right sidebar conceptually separate. It also reuses an
  unedited preview tab instead of opening an unlimited stream of tabs. Renart
  should preserve those distinct responsibilities and eventually consider the
  same preview-versus-pinned behavior for browsed tables, without adopting
  Postman's customizable product-catalog sidebar.
- [Bruno](https://docs.usebruno.com/v2/introduction/what-is-bruno) reinforces
  the product-level constraint that developer work stays as ordinary files in
  the repository and collaboration happens through Git. Renart's navigation
  may make those files easier to work with, but must not obscure which state is
  authored and reviewable versus local or observed.
- [Insomnia documents](https://developer.konghq.com/insomnia/documents/) group
  design, debug, and test around one selected artifact. This supports keeping
  Renart's canvas, code, inspect, and type-check workflows inside Build rather
  than turning each tool into another global destination.
- [Snowflake's Database Explorer](https://docs.snowflake.com/en/en/user-guide/ui-snowsight-data)
  uses a conventional database → schema → object hierarchy and only exposes
  objects visible to the active role. Renart should keep that recognizable
  hierarchy while clearly naming the active connection/environment and treating
  an empty tree as potentially permission- or refresh-limited, not automatically
  as an empty warehouse.
- Hex lets users [create a connection from the Data Browser](https://learn.hex.tech/docs/connect-to-data/data-connections/data-connections-introduction)
  while retaining a central management surface. Its
  [Data Browser](https://learn.hex.tech/docs/explore-data/data-browser) also
  combines scoped search, preview/query actions, recent items, favorites,
  metadata, and explicit schema refresh. Renart should copy the point-of-need
  entry and feedback loop, not Hex's cloud ownership or permission model.

## 4. Shared information architecture under test

### Abstract target model

Renart is one project shell with three stable modes in the header:

- **Build** changes version-controlled project files and provides the visual
  pipeline workbench.
- **Run** turns reviewed project state into deployments, schedules, and
  observable executions.
- **Explore** finds and presents authored outputs and safely observes connected
  data systems.

The desktop shell then has four layers with deliberately different jobs:

1. The **header** changes the top-level mode and holds global project context:
   selected project, environment, search/commands, and Git state.
2. The **narrow rail** changes the active tool within that mode. Its contents
   are contextual: authoring and configuration tools in Build, operational
   entities in Run, and discovery/presentation tools in Explore.
3. The **wide sidebar** is the master list or hierarchy for the selected rail
   tool. It shows assets, tables, configured connections, deployments,
   schedules, reports, and similar concrete things—not another list of pages.
4. The **central work surface** edits, previews, compares, or observes the
   selected thing. A right inspector is optional object detail and never a
   third navigation hierarchy.

Inside a pipeline Build workspace, the lineage canvas is the persistent spatial
anchor. Selecting Data Browser, connections, environments, or pipeline settings
changes the contextual sidebar without replacing the canvas. Ad-hoc queries and
notebooks are peer work surfaces that replace the central canvas until the user
returns to project resources. Transient table previews and detailed
configuration open in a bounded overlay over the current work surface.

This is a master-detail workbench rather than a dashboard menu. Switching the
header changes the task domain; switching the rail changes the tool; switching
the wide sidebar changes the object. Returning to a mode should restore its
last rail tool and selection so the contextual rail does not destroy spatial
memory. Deep links should encode the meaningful mode/tool/object state without
making temporary hover or preview state part of the URL.

Mobile keeps the same concepts but not the desktop geometry. Build, Run, and
Explore become bottom navigation; one contextual drawer combines the current
rail and wide-sidebar level; the selected work surface stays full width. A user
must never traverse two nested drawers to reach a common action.

### UX rules for the production iteration

- **One active hierarchy:** the wide sidebar must always explain the selected
  rail icon. Avoid simultaneously showing a pipeline tree and a connection
  settings surface, even if keeping both mounted would be technically easier.
- **Preserve Build context:** contextual utilities such as Data Browser and
  settings must not unmount or replace the pipeline canvas. Ad-hoc Query and
  Notebooks deliberately replace it as peer work surfaces; returning to project
  resources restores the same DAG state.
- **Drill down, then back:** a Data Browser connection overview and one selected
  connection's catalog are separate sidebar levels. Never squeeze the complete
  connection list and the selected schema/table tree into the same pane.
- **Restore, do not reset:** remember the last tool, object, scroll position,
  and open branches separately for Build, Run, and Explore. A contextual rail
  is only efficient when returning to a mode feels spatially stable.
- **Preview before multiplying state:** a single-click table or artifact can
  reuse one preview surface; an explicit edit, query, or pin action makes it
  durable. This avoids a workbench full of accidental tabs or panels.
- **Return to the point of need:** creating a connection, schedule, asset, or
  deployment from a contextual flow should return to that flow with the new
  object selected. Management pages remain available but are not mandatory
  detours.
- **Name the state boundary:** authored, deployed, local-only, observed, cached,
  and live data need distinct language and visual treatment. A remote table
  does not become a Renart asset merely because it was previewed.
- **Expose scope and cost:** connection, environment, role/identity, metadata
  refresh time, and preview limits remain visible near data actions. Schema
  refresh and table preview can contact expensive production systems and must
  not look like free local filtering.
- **Progressive disclosure:** show common actions and connectors first; search,
  advanced configuration, destructive management, and exhaustive catalogs are
  secondary. The narrow rail must not become a new miscellaneous drawer.
- **Workbench before dashboard:** use continuous work surfaces, status strips,
  timelines, tables, and master-detail rows as the default composition. A card
  should represent a genuinely independent object, preview, or decision—not
  merely provide a rounded container for every KPI or settings group. Operational
  numbers belong in compact readouts beside the workflow they explain.
- **Keyboard and URL parity:** every rail item and sidebar row needs a stable
  accessible name, visible focus, command-palette entry, and meaningful deep
  link where the state is shareable. Hover-only actions also appear on focus.
- **Visible async state:** discovery, refresh, connection verification, and SSE
  reconciliation distinguish idle, in progress, partial, failed, and stale.
  Never replace a last-known-good tree with an unexplained blank panel.
- **Responsive parity:** mobile can collapse navigation layers, but it cannot
  omit Build tools or turn common actions into nested drawers. The desktop rail
  becomes a visible tool-tab strip below the header; close the contextual
  drawer before opening a modal and restore focus to the initiating action
  afterward.
- **Do not depend on brand marks:** bundled marks speed recognition, but every
  connector keeps a text label, high-contrast selected state, and screen-reader
  name. Connection aliases remain more prominent than their engine after setup.

### Architecture-derived constraints

The existing architecture adds several distinctions that the shell must not
flatten for visual simplicity:

- **Workspace Catalog is not the Data Browser.** The Catalog is Renart's
  filesystem-backed artifact and lineage index: pipeline assets, notebook
  components, datasets, visualizations, dashboards, and reports. The Data
  Browser observes configured warehouses, object stores, and project-scoped
  local files and may discover data that has no authored Renart asset. The UI
  can cross-link them, but the labels and trust states remain distinct.
- **Deployment is source readiness, not data freshness.** A deployment freezes
  reviewed files. A working-tree or deployed run produces data. Build and
  Run may be visually adjacent, but “Deploy” must not imply that tables were
  materialized.
- **A schedule has desired and local state.** `.renart/schedules.yml` is the
  Git-tracked cadence/policy declaration; the machine-local database owns the
  deployment pin, watermark, projected occurrence, waiting state, and history.
  Run must show their binding rather than collapsing them into one editable
  row.
- **Source and sensor states are not ordinary freshness.** External source
  assets are dependency anchors without a Renart build state. Sensors remain
  volatile readiness gates. Build and Run should use those labels instead
  of “Never built” or stale coloring.
- **Notebooks are authoring documents.** They contain Git-native SQL/Python,
  sources, text, controls, visualizations, and an authoring rail; remote data is
  transferred into a local DuckDB session. Build is therefore their strongest
  home. Explore can still index and link notebook outputs and visualizations.
- **Presentations participate in engineering safety.** Dashboard/report
  definitions are type-checked against producer pipelines and can block a
  deployment. Explore is their primary discovery/authoring destination, while
  Build and deployment review need direct links to affected artifacts.
- **Projects sit above a project shell.** One Renart process can mount multiple
  Git projects and each tab pins one project API context. An all-projects page
  belongs to the launcher/project switcher level, not as a peer of assets inside
  one project.
- **Environment is active context and policy.** Protected, deployed-only, and
  destructive-confirmation policies change available actions. Keep the compact
  current environment visible globally, while management lives in the
  contextual/global utility navigation.
- **Credentials stay server-side.** Data browsing and inline connection
  creation may expose safe identities, schemas, and capabilities, but reuse the
  write-only connection boundary and never turn the new sidebar into a secret
  state store.

### Global layer

- Project switcher and an all-projects overview outside the selected project's
  workbench.
- Universal search / command palette.
- Git state and execution context.
- Three durable top-level pages — **Build**, **Run**, and **Explore** — remain
  visible in the header in every desktop variant. Mobile translates the same
  three destinations into the bottom navigation.
- Notebooks are an authored resource inside Build rather than a fourth global
  destination.
- Connections, environments, and project settings remain globally owned but
  get contextual shortcuts from Build and the Data Browser.

### Build

- Pipeline and asset explorer.
- Canvas, split editor, code-only editor, Guided Asset Properties, and the
  inspect/render/materialize/query/type-check result panel.
- Multiple open asset documents as a workbench convenience. The selected tab is
  still one real Git-backed asset file; tabs do not invent a second source of
  truth or split a SQL asset's Bruin header into a fictional YAML document.
- Ad-hoc queries and notebooks as first-class thin-rail tools rather than rows
  hidden beneath the pipeline tree.
- Data Browser with connection → catalog/schema → table navigation and actions.
- Fast routes to connections, environments, and pipeline settings.
- Deployment review as the transition into Run, not a second Build mode.
- Problems from type-check, schema derivation, presentation compatibility, and
  current-content quality results remain one contextual panel rather than four
  navigation destinations.

### Run

- **Overview:** compact operational readouts, a projected + actual timeline,
  active incidents, deployment readiness, and next scheduled work. The readouts
  frame the timeline; they are not repeated inside every schedule row.
- **Deployments:** immutable versions, comparison, promotion, and schedule
  assignment. Creating a schedule belongs in the post-deploy flow and on a
  deployment detail.
- **Schedules:** desired schedule definitions and their deployment bindings.
- **Runs:** complete list including scheduled, manual, backfill, and sensor
  runs. A run detail remains its own deep link and keeps the existing Renart
  structure: replay context, per-asset duration timeline, Events, Plan, and
  Output.

Run also needs explicit states for protected/deployed-only environments,
workspace-vs-deployment drift, missing or older pins, waiting prerequisites,
slot waits, retries, scheduler ownership, and exact-plan replay. These are
status facets inside the four views, not additional sidebar destinations.

The overview timeline visually distinguishes:

- projected run: outlined/dashed;
- queued or running run: active semantic state;
- successful/failed actual run: solid historical state;
- manual run: explicit play trigger marker.

Actual and projected executions are intervals, not identical event dots. Their
horizontal position represents the start and their width represents actual or
expected duration. Status remains encoded independently through fill, border,
and motion, so duration never replaces success/failure/waiting semantics. Exact
duration remains available in the event label and detail interaction.

### Explore

- Workspace-wide asset catalog and lineage canvas across every pipeline, with
  filtering and direct handoff to the producer's Build view.
- Dashboards and reports.
- A project-wide Data Browser entry point.
- Notebooks live in Build in every variant, while Explore can index and link
  their published outputs and visualizations.

Explore labels the authored **Workspace Catalog** separately from the live
**Data Browser**. Selecting a positively observed remote table can preview it,
start an ad-hoc query, or enter the reviewed source-asset import flow; it does
not silently make the table canonical workspace state. Project-local tabular
files appear in that same Data Browser with folder/file language and explicit
Seed, Load, query, or notebook handoffs.

### Data Browser connection onboarding

Adding a warehouse is a point-of-need action in the Data Browser and a
project-wide resource after creation:

1. **Add data warehouse** opens a compact chooser with the eight common direct
   query targets: PostgreSQL, Snowflake, BigQuery, Redshift, Databricks, Trino,
   ClickHouse, and DuckDB. Exact bundled brand marks make the choices
   recognizable; names remain visible and accessible, so color/logo is never
   the only signal.
2. **Add local file** is a sibling point-of-need action, not a warehouse type.
   It browses only the active project root and explicitly allowed read-only
   roots, supports CSV/Parquet/JSON/JSONL first, and keeps folder/file language
   instead of presenting paths as databases and schemas.
3. A secondary **All connection types** action covers object storage and less
   common connectors without making the happy path into a catalog of every
   Bruin driver.
4. Choosing a connector continues into the existing backend-described,
   connection-specific form for the current environment. Type-specific fields
   and compatibility must not be duplicated in the frontend.
5. Sensitive values use the existing write-only secret boundary. The flow
   offers verification before creation and never echoes credentials back into
   the browser.
6. After creation, Renart returns to the Data Browser, selects the new
   connection, and exposes bounded discovery progress, partial/error state,
   and an explicit retry. A connection with no visible objects must distinguish
   successful empty discovery from missing permissions and failed refresh.
7. Schema refresh is explicit and potentially costly. The browser should show
   last refresh time and scope, later adding recent/favorite objects and scoped
   search without silently polling the warehouse.

The chooser is intentionally not the complete Connections settings page. The
Data Browser optimizes first connection and immediate exploration; Connections
owns editing, secret state, environment coverage, usage, health, and deletion.

## 5. Initial canvas drop semantics

This is a design hypothesis for the mocks, not an execution contract. The
canvas should accept drops only when the resulting relationship has a useful,
predictable meaning.

| Palette item          | Empty canvas       | On an asset / downstream edge | Rationale                                                                                                  |
| --------------------- | ------------------ | ----------------------------- | ---------------------------------------------------------------------------------------------------------- |
| Source table          | Yes                | No by default                 | Represents an externally maintained root.                                                                  |
| Seed                  | Yes                | No by default                 | A versioned input is normally a root, not a transform.                                                     |
| HTTP / ingestr source | Yes                | No by default                 | External ingestion starts a lineage branch.                                                                |
| SQL transform         | Yes                | Yes                           | Can be standalone or consume selected upstreams.                                                           |
| Python transform      | Yes                | Yes                           | Same graph semantics as a SQL transform.                                                                   |
| Load / replication    | Yes                | Yes                           | May ingest a source or publish a selected upstream to a sink. The create flow must ask which role applies. |
| Sensor                | Yes                | Attach as readiness condition | A sensor gates execution; presenting it as a normal data-producing downstream is misleading.               |
| Unit test             | Attach to an asset | Attach to selected asset      | It validates an asset and should not look like a materialized relation.                                    |

The production implementation will need an asset-capability model rather than
hard-coded UI conditionals: `produces_relation`, `accepts_upstreams`,
`root_preferred`, `execution_gate`, `validation_attachment`, and
`supports_sink_target` are a plausible starting vocabulary.

## 6. The three mock variants

### A. Workbench rail

- Shared header navigation: Build, Run, and Explore.
- A narrow, mode-dependent rail exposes the most important tools for the
  current top-level page rather than carrying one static list throughout the
  product.
- The wide sidebar is the second navigation level for the selected rail item:
  resources, live data, configuration sections, operational entities, or
  presentation documents. It is not a second copy of the top-level pages.
- Build puts project resources, Data Browser, connections, environments, and
  pipeline settings at the top, with Ad-hoc Query and Notebooks alongside the
  authoring tools. Run replaces them with overview, deployments, schedules, and
  runs. Explore replaces them with Workspace Catalog, dashboards, reports, and
  Data Browser.
- The wide sidebar is collapsible. Hiding it never changes the active tool or
  canvas selection, and the thin rail retains the control that restores it.
- Project settings remains the one stable bottom action. It is global to the
  selected project rather than part of a specific workflow.
- Mobile keeps the same information architecture in one textual Sheet; it does
  not reproduce the narrow and wide sidebars beside each other.
- Most faithful to an IDE and best for frequent tool and resource switching.
- Risk: changing rail contents reduces spatial memory. Tooltips, stable icon
  order within a mode, and preserving the current selection on return must make
  that contextual behavior explicit.

The resulting desktop master-detail relationship is:

| Header mode | Narrow rail                                                                                            | Wide sidebar after selection                                                                                                       |
| ----------- | ------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------- |
| Build       | Project resources, Ad-hoc Query, Notebooks, Data Browser, Connections, Environments, Pipeline settings | Pipeline/assets, notebook list, connection overview or selected-connection catalog, environment list, or pipeline setting sections |
| Run         | Overview, Deployments, Schedules, Runs                                                                 | Pipeline health, deployment versions, schedule definitions/bindings, or run filters and history                                    |
| Explore     | Workspace Catalog, Dashboards, Reports, Data Browser                                                   | Artifact filters, dashboard list, report list, or live connection/schema/table tree                                                |

### B. Lifecycle navigation

- Shared header navigation: Build, Run, and Explore.
- One wide left sidebar whose contents change by lifecycle stage.
- Build's sidebar has local tabs for Project and Data; Run directly exposes
  Overview, Deployments, Schedules, and Runs.
- Notebooks live in Build as another authored project resource.
- Clearest explanation of how work progresses through Renart.
- Risk: experienced users may find a lifecycle hierarchy slower than a stable
  resource hierarchy.

### C. Project studio

- Shared header navigation: Build, Run, and Explore.
- Persistent project/resource sidebar: pipelines, notebooks, presentations.
- A compact local toolbar changes the selected resource between Design, Query,
  Deploy, and Observe.
- Data Browser opens as a secondary working pane; global utilities are grouped
  at the bottom of the project sidebar.
- Strongest filesystem/project mental model and least mode switching.
- Risk: deployment and observation can become too resource-scoped; cross-project
  operational overview needs a deliberate escape hatch.

## 7. Prototype scope

The branch will add dedicated `/navigation-lab/...` routes outside the current
application shell. The mocks may read the current workspace DTO for realistic
pipeline names, but they do not write files or require new APIs. They will:

- reuse Renart's lineage data and asset-node visual language in a lab-local
  canvas surface. A direct second import of the production canvas was tested,
  but caused the bundler to hoist roughly 50 KiB of otherwise lazy dependencies
  into the initial chunk and fail the bundle budget; the production migration
  can reuse the original canvas because it replaces rather than duplicates the
  current Build route;
- use mock tables, runs, deployments, and schedules where backend composition
  would add noise;
- make mode, sidebar, Data Browser, asset selection, and mobile navigation
  interactive;
- include obvious “Design study” labeling so screenshots cannot be mistaken for
  shipped UI;
- remain isolated from production navigation and route behavior.

## 8. Evaluation criteria

Each variant will be checked at desktop and mobile widths for:

- time to reach a pipeline, asset, ad-hoc query, connection, and environment;
- time to create a deployment and assign or create a schedule;
- clarity between projected and actual runs;
- ability to browse a connection without losing authoring context;
- header density and horizontal overflow;
- one-handed mobile access to Build, Run, Explore, and contextual tools;
- whether Notebooks feel discoverable without becoming another permanent global
  item.

The implementation recommendation should be based on this comparison rather
than automatically choosing the variant closest to today's shell.

## 9. Implemented design lab

The branch exposes three isolated, frontend-only routes:

- `/navigation-lab/workbench` — variant A;
- `/navigation-lab/lifecycle` — variant B;
- `/navigation-lab/studio` — variant C.

The routes share realistic Renart surfaces and state, but do not call write
endpoints. The prototype includes:

- a faithful lineage canvas and asset-node composition using the same domain
  semantics as the production canvas, isolated so the design-only route does
  not change the normal app's lazy-loading boundaries;
- Code, Split, and Canvas layouts aligned with the current Build workbench;
- one rounded document/editor surface where SQL, Python, source YAML, Ad-hoc
  Query, and Notebook documents coexist as closable tabs in its top edge;
  opening a tab changes the work surface without adding another nested page
  header or a second card around the editor;
- an asset editor that demonstrates SQL with its inline Bruin header, Python
  with its triple-quoted header, raw source YAML, and the structured parameter
  editors used by seed/API/load/sensor-style assets;
- a distinct, collapsible Inspect/Render/Materialize/Query/Type-check results
  panel without a redundant filename/action row above the editor;
- a tabbed Guided Asset Properties inspector that keeps the production-shaped
  controls but separates General, Lineage, Columns, and Checks so the panel no
  longer reads as one long metadata form;
- draggable asset kinds with root, downstream, readiness-gate, and test drop
  targets derived from the matrix above;
- a live Data Browser hierarchy with authored-versus-observed trust states,
  preview, query, and reviewed source-import actions;
- a point-of-need warehouse chooser with eight bundled engine marks, a
  backend-shaped write-only connection form, verification, creation, and
  return-to-browser discovery feedback;
- explicit ready, discovering, refreshing, partial, failed-with-cache, and
  successfully-empty metadata states without replacing last-known-good trees;
- one reusable table preview plus explicitly pinned previews, working scoped
  schema/table search, refresh scope, and last-refresh information;
- per-mode workbench state so Build, Run, and Explore restore their last rail
  tool rather than resetting to their default whenever the header changes;
- a persistent Build canvas beneath Data Browser and settings navigation, with
  table previews and detailed settings in overlays;
- Ad-hoc Query and Notebook as peer tabbed work surfaces that replace the canvas
  while retaining its state for the return transition;
- a shared rounded shell around the narrow rail and its contextual wide sidebar,
  plus compact spacing and rounded cards around the editor/canvas, results, and
  right inspector so the two left navigation levels still read as one object;
- a full-height right inspector aligned with the combined left-sidebar shell;
- a contextual wide sidebar toggled by its active rail item: selecting a tool
  opens or switches the wide pane, while selecting the same active tool again
  collapses it without requiring a separate collapse button;
- a Data Browser navigation stack that starts with connections and replaces that
  overview with one selected connection's metadata/schema/table detail plus Back;
- connections and environment settings reachable from the authoring context;
- a mode-dependent Workbench rail whose wide sidebar drills into the selected
  Build, Run, or Explore tool;
- master-detail connection, environment, pipeline-settings, and
  project-settings surfaces instead of a generic settings index;
- a compact, timeline-first Run overview between Renart's visual language and
  a dense orchestration timeline: projected, scheduled, manual, running,
  successful, failed, and waiting executions retain status encoding while bar
  width communicates duration; compact operational readouts, incidents, and
  deployment readiness remain outside the deliberately sparse timeline;
- separate deployment, schedule, complete run-list, and production-shaped run
  detail views;
- deployment-to-schedule bindings and workspace-drift messaging;
- a project launcher above the selected project shell;
- a workspace-wide multi-pipeline Catalog canvas plus representative dashboard,
  report, and notebook surfaces;
- a mobile-only, horizontally scrollable tool-tab strip below the global
  header, sourced from the same mode registry as the desktop rail;
- one contextual mobile Sheet without a redundant visible introduction or a
  repeated tool menu; selecting Data Browser or settings opens the appropriate
  bounded working dialog directly, while Back returns to and reveals the
  workspace;
- a safe-area-aware bottom navigation;
- no redundant local page header above Run or Explore: their rail/sidebar
  already names and switches the active view.

The common “Navigation study” bar is lab chrome. It would not ship with the
production shell.

## 10. Comparative findings

| Criterion                 | A — Workbench rail                                                                                    | B — Lifecycle                                                    | C — Project studio                                                |
| ------------------------- | ----------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------- | ----------------------------------------------------------------- |
| Header clarity            | Shared Build / Run / Explore navigation                                                               | Shared Build / Run / Explore navigation                          | Shared Build / Run / Explore navigation                           |
| Fast expert switching     | Best; each mode keeps its frequent tools in the rail                                                  | Good, with fewer controls                                        | Good within one resource                                          |
| First-use explanation     | Good when rail tooltips and wide-sidebar titles agree                                                 | Best                                                             | Good for Git/project-oriented users                               |
| Double-sidebar use        | Native master-detail navigation                                                                       | Possible as a Build-local expansion                              | Native; Data becomes a secondary pane                             |
| Notebook placement        | Authored resource inside Build                                                                        | Authored resource inside Build                                   | Authored resource in the persistent project tree                  |
| Cross-pipeline operations | Strong through Run-specific overview and entity lists                                                 | Strongest and easiest to explain                                 | Weakest; local phase controls imply resource scope                |
| Mobile translation        | Shared pages become bottom navigation; rail tools become header-adjacent tabs; context uses one Sheet | Shared pages become bottom navigation; context becomes one Sheet | Shared pages become bottom navigation; resources become one Sheet |
| Main risk                 | Contextual rail changes can weaken spatial memory                                                     | Context changes more than the other variants                     | Local phases can hide project-wide operations                     |

All variants were exercised at 1440×900 and 412×915. The pages stayed exactly
within the viewport, their mobile sheets remained scrollable, and no runtime or
console errors were observed. The Build canvas intentionally pans rather than
shrinking its nodes into an unreadable mobile overview.

## 11. Recommendation

Use a deliberate hybrid, led by **variant A's nested workbench shell**:

1. Keep the global header for project, environment, search/commands, Git, and
   other truly global context, and keep **Build**, **Run**, and **Explore** there
   as persistent page links.
2. Keep variant A's narrow rail and make it explicitly mode-dependent. Build
   gets project resources, Ad-hoc Query, Notebooks, Data Browser, and
   configuration; Run gets operational entities; Explore gets
   discovery/presentation entities. This keeps frequent tools high in the rail
   while the header remains the stable page-level navigation.
3. Move **Notebooks into Build** as a first-class authored project resource.
   Notebook outputs remain discoverable from Explore.
4. Treat the wide contextual sidebar as the selected rail item's detail level:
   pipeline/assets, live schemas/tables, configured connections, environments,
   settings sections, deployment versions, schedules, runs, catalog filters,
   dashboards, or reports. Do not leave the previous context tree visible when
   the central surface belongs to a different rail item.
5. Keep the asset inspector as a right contextual pane. It is fundamentally
   different from navigation and should not migrate into the left workbench.
6. On mobile, expose Build, Run, and Explore in the bottom navigation, replace
   the narrow rail with mode-aware tool tabs directly below the global header,
   and show exactly one left Sheet containing only the selected contextual
   hierarchy. Never reproduce the desktop nested sidebars side by side or put
   the tool picker inside the Sheet.
7. Keep variant B's Run information architecture unchanged: Overview,
   Deployments, Schedules, and Runs. The overview owns the compact combined
   projected/actual timeline; Runs remains the complete investigative list.

This choice retains Renart's IDE identity and efficient canvas workflow without
making users infer the lifecycle from six unrelated destinations. It also
provides a stable location for Data Browser, connections, and environments
without pretending they are pipeline files.

## 12. Production implementation plan

This section records the design-level implementation decision. The audited,
slice-by-slice migration contract, including component ownership, route state,
test gates, rollback, and convergence, is maintained in
[navigation-workbench-migration.md](navigation-workbench-migration.md).

### 12.1 Chosen direction and boundaries

Production follows variant A's mode-aware workbench rail. Build, Run, and
Explore remain real, stable top-level links in the header; the rail and wide
sidebar are contextual navigation inside the selected mode. This is not a
second app shell and must not introduce another source of workspace truth.

The migration preserves these boundaries:

- existing user-facing URLs continue to work throughout the migration;
- route files own durable navigation state; components do not branch on raw
  pathname strings to simulate subroutes;
- the filesystem, backend workspace DTO, SSE stream, and current Jotai domain
  atoms remain authoritative for project data;
- workbench selection, expanded tree nodes, preview tabs, and scroll positions
  are disposable UI state and may live in a small project-scoped session store;
- connection fields continue to be described by the backend and credentials
  continue through the current write-only secret boundary;
- discovery is explicit and event/reconciliation driven. The Data Browser does
  not poll warehouses in the background;
- the existing right asset inspector remains object detail. It is not folded
  into the navigation hierarchy;
- production canvas drop behavior waits for a backend capability contract; the
  mock's matrix must not become scattered frontend type checks.

### 12.2 Route and mode ownership

The current URLs can be projected into the new modes without an immediate URL
rewrite:

| Mode    | Existing routes retained                                                               | Contextual tools                                                              |
| ------- | -------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------- |
| Build   | `/`, `/pipelines/$pipelineId/...`, `/notebooks`, `/notebooks/$notebookId`              | Project resources, Data Browser, Connections, Environments, pipeline settings |
| Run     | `/runs`, `/runs/$runId`, `/schedules`, `/schedules/deployments`, `/schedules/timeline` | Overview, Deployments, Schedules, Runs                                        |
| Explore | `/catalog`, `/dashboards`, `/dashboards/$path`, `/reports`, `/reports/$path`           | Workspace Catalog, Dashboards, Reports, Data Browser                          |
| Global  | `/project/general`, `/project/environments`, `/project/connections`                    | Project settings, reachable from every mode                                   |

Add a canonical `/data` route when the production Data Browser lands. Build
and Explore both open that same route while passing a route-safe return context;
there are not separate Build and Explore catalogs. Existing settings URLs stay
canonical even when their content is presented inside the workbench.

Every shell-owned route declares static navigation metadata on its TanStack
route definition: mode, rail tool, optional sidebar kind, and mobile label. The
shell derives active state from the deepest matched route. This replaces
component-local `pathname.startsWith(...)` checks and lets route tests detect a
new page that was not assigned to a mode.

### 12.3 Frontend structure

Introduce the production pieces incrementally:

1. `app-navigation-model.ts` owns the typed Build/Run/Explore registry, route
   ownership helpers, default destinations, labels, icons, and exhaustive
   tests. It contains no React state and performs no navigation.
2. `AppModeNavigation` renders the three header links from that model.
   Transitional child actions keep every current destination reachable until
   its contextual sidebar ships.
3. `AppWorkbenchLayout` owns the desktop rail/sidebar geometry and the mobile
   tool-tab plus one-drawer translation. It receives route-derived descriptors and slots;
   individual pages do not each recreate a rail.
4. `AppWorkbenchRail` renders only tools valid for the active mode. Stable item
   IDs—not array positions—key focus, tooltips, command-palette entries, and
   persisted selection.
5. `AppContextSidebar` is a collapsible shell slot. Build, Run, Explore, Data
   Browser, and settings provide their own resource hierarchy while sharing
   frame, search, Back navigation, loading, empty, and error primitives. Build
   utility selection does not replace the mounted canvas.
6. `useWorkbenchSessionState(projectId)` remembers the last tool, selected
   object, expanded branches, sidebar width, and scroll restoration separately
   for each mode. It uses versioned `sessionStorage`; corrupt or obsolete state
   falls back safely. It never stores assets, schemas, credentials, freshness,
   or other domain data.
7. The mobile shell maps the global modes to three bottom destinations and the
   active mode's rail registry to a horizontal tool-tab strip below the header.
   One Sheet hosts only contextual content. Opening a dialog closes the Sheet
   first and restores focus when the dialog closes.

The workbench state should be represented by a reducer with explicit actions
(`mode-entered`, `tool-selected`, `object-selected`, `preview-pinned`,
`tree-expanded`, `state-restored`) rather than unrelated booleans in
`AppShell`. Unit tests can then cover restoration and cross-mode transitions
without rendering Monaco or React Flow.

### 12.3.1 Asset editor and document-tab contract

The production migration should retain the existing Build editor semantics
rather than replacing them with a generic text area:

- **Code, Split, and Canvas remain route-owned layouts.** A direct asset route
  defaults to Split. Once users explicitly choose Code, Split, or Canvas,
  selecting another asset preserves that layout. Ad-hoc Query and Notebooks are
  separate central work surfaces; returning to an asset restores its previous
  Build layout.
- **One tab represents one authored asset document.** SQL and Python keep their
  Bruin metadata header in the same `.sql` or `.py` tab. Source assets expose
  their real `.source.yml`; seed, API, Load, sensor, and unit-test assets retain
  the specialized editors that update their actual `.asset.yml`/`.test.yml`
  documents. The currently hidden expert metadata-YAML editor does not become a
  second public tab merely to make the mock look more IDE-like.
- **The route identifies the active document.** The bounded open-tab list,
  order, and last focused document are project-scoped `sessionStorage` UI state.
  They are not serialized into every URL and never contain file contents.
  Browser back/forward changes the active tab without discarding other open
  documents.
- **Workspace/SSE data owns names and kinds.** A rename updates the tab label
  from the reconciled workspace DTO; deleted or moved files become explicit
  missing-document tabs until dismissed instead of silently opening a stale
  copy.
- **Drafts remain per asset and use the existing save barrier.** Switching tabs
  must flush or safely retain the current debounced draft. Closing a dirty tab
  waits for the participant to retire; failed saves keep a visible failed/dirty
  state and require retry or explicit discard. The tab strip itself never owns
  an independent document buffer.
- **Only the active Monaco instance should remain mounted initially.** Persist
  each asset's editor view state (cursor, selection, and scroll) before changing
  tabs and restore it on return. This avoids multiplying Monaco, LSP, and model
  memory for a long tab list while preserving spatial continuity.
- **Document tabs and result tabs are different hierarchies.** Assets, Ad-hoc
  Query, and Notebooks live in one document bar above the entire central work
  surface. An asset tab reveals the editor/canvas/result composition; a special
  document tab swaps that composition for its own work surface. Inspect, Render,
  Materialize, Query, and Type check stay in the existing resizable bottom panel
  and follow only the active asset. An execution result must carry its asset/run
  identity so a late response cannot appear under a newly selected tab.
- **Overflow is bounded and accessible.** Tabs shrink to a useful filename,
  scroll horizontally, expose a close action, support middle-click and standard
  next/previous/close keyboard commands, and offer an overflow list. Mobile
  shows the active document plus a compact open-document picker rather than a
  squeezed desktop strip.
- **Preview tabs remain separate future work.** Data Browser single-click
  previews may later reuse one replaceable italic preview tab, but authored
  asset tabs are durable for the session until closed. A preview becomes an
  authored tab only through an explicit Query, Import, or Create Asset action.

The first production slice can add tabs around today's `EditorWorkspace`
without moving editor ownership into the navigation shell: a small
project/pipeline-scoped document controller supplies the active asset route and
open IDs, while `BuildPage` continues to own drafts, execution results, and the
right inspector. This keeps the feature independently releasable and avoids a
second editor architecture during the larger shell migration.

### 12.4 Connection creation and return-to-need

Do not duplicate `WorkspaceConnectionFormFields` or the logic in
`useWorkspaceConnectionForm`. Extract the current settings Sheet into a
reusable connection editor controller/surface with these inputs:

- create or edit mode;
- initial environment and optional connection type;
- compact-dialog or management-sheet presentation;
- optional `onCreated({ environment, connection })` callback;
- optional cancel/return context.

The Data Browser adds the common eight-connector chooser in front of that
surface. **All connection types** opens the same controller with no type
preselection. Verify uses `POST /api/config/connections/test`; create uses the
existing config mutation. After the response and SSE reconciliation agree, the
flow returns to `/data`, selects the exact environment/connection identity, and
starts one explicit metadata discovery. Failure leaves the verified form open;
successful creation never makes the user rediscover the new alias manually.

Connection identity is the tuple `(project, environment, connection name,
connection type/config identity)`, not the display name alone. Renaming or
changing config invalidates cached browser metadata and pinned previews for the
old identity.

### 12.5 Data Browser data contract

The first production version can reuse the existing read-only SQL discovery
operations:

- `GET /api/sql/databases`;
- `GET /api/sql/tables`;
- `GET /api/sql/table-columns`;
- `POST /api/sql/query` for an explicitly requested preview;
- the existing remote-catalog observer, which warms LSP/type-check knowledge
  after positive table or column observations.

Those endpoints are sufficient for a thin first slice, but not for the final
tree contract. They do not expose cache provenance, last refresh, partial
results, permission-limited success, object-storage hierarchy, or a stable
single refresh identity. Add a service-level Data Browser endpoint rather than
encoding these states in React:

```text
GET  /api/data-browser/connections?environment=...
POST /api/data-browser/connections/{connection}/refresh
GET  /api/data-browser/connections/{connection}/catalog
POST /api/data-browser/preview
```

The catalog response contains safe connection identity, hierarchy nodes,
capabilities, observation time, cache state, refresh state, scope, partial
findings, and a monotonic revision. It contains no secret values. A refresh may
return cached nodes together with a partial or failed status; the frontend must
not blank the last-known-good tree. Connection-config SSE invalidates the
identity, while discovery completion emits a targeted catalog event instead of
requiring polling.

`POST /api/data-browser/preview` accepts a server-issued discovered-object
identity and a bounded row limit. The server constructs and quotes the
read-only query for the resolved warehouse capability. The frontend must not
concatenate an arbitrary table name into SQL. The response reuses the shared
result-table model and declares truncation, elapsed time, source connection,
environment, and operation metadata.

Object storage uses the same browser node/result envelope but a different
service adapter. Prefix listing, Parquet/CSV schema inspection, and row preview
are capabilities, not assumptions; unsupported actions remain absent rather
than failing after click.

Project-local files also use the shared node/result envelope through a separate
local-file adapter. It canonicalizes server-side paths, rejects traversal and
symlink escape, lists only bounded allowed roots, and reads file contents only
for an explicit schema/preview request. A project-relative file reference is
server-issued just like a warehouse object identity.

### 12.6 Browser state model

Model discovery explicitly as `idle`, `discovering`, `ready`, `refreshing`,
`partial`, `error-with-cache`, `error-empty`, or `empty`. Each result also
records:

- last successful observation time;
- requested connection/environment/database/prefix scope;
- whether rows, metadata only, or an object listing were read;
- whether displayed nodes are live, cached, or partially refreshed;
- actionable retry or permissions detail;
- the connection configuration identity used for the observation.

Opening the Data Browser first shows configured connections and pinned/recent
objects. Choosing one connection replaces that overview with its own metadata
and catalog hierarchy plus a Back action; the two levels are never rendered
together. Opening a connection performs no row query. Expanding a
catalog/database/schema requests only the next metadata level. Selecting an
explicit Preview action reuses one unpinned preview overlay. Pinning promotes it
to project-session UI state; it does not create an asset or write a repository
file. Recent and pinned items store only safe object identities, are capped, and
disappear when their connection identity is no longer valid.

Scoped search filters loaded nodes immediately and offers an explicit remote
metadata search only when the adapter supports it. The UI always identifies
which behavior is active. It never gives a local filter the appearance of a
warehouse-wide search.

### 12.7 Authored, observed, and imported relations

The Data Browser resolves authored coverage by connection identity plus the
warehouse-aware canonical relation identity. It must reuse the same qualified
name rules as SQL discovery and external-relation import; matching only a short
`schema.table` label would reintroduce three-level-name and multi-connection
collisions.

An authored match links to its asset and shows its Renart freshness separately
from warehouse observation time. An unmatched positive observation offers the
existing reviewed source-asset import flow. Previewing, querying, or pinning
never silently persists an asset. After import and SSE reconciliation, the
browser row changes from observed to authored without losing the selected
object.

### 12.8 Run and Explore migration

Run keeps the existing route resources but changes their composition:

- Overview combines a compact projected/actual timeline, active failures, and
  next work. It reads the existing schedule and run APIs; projected and actual
  occurrences remain distinguishable records.
- Deployments retains immutable versions and adds schedule creation/assignment
  as a post-deploy and deployment-detail action.
- Schedules owns desired definitions and deployment bindings.
- Runs remains the full sortable history and keeps `/runs/$runId` as the
  investigation deep link. The shared shell must wrap, not redesign, the
  existing run detail's replay context, asset-duration timeline, Events, Plan,
  and Output views.

Explore first wraps today's Catalog, Dashboard, and Report pages in the shared
workbench without merging their domain components. Catalog keeps its existing
workspace-wide lineage canvas over assets from all pipelines. Notebook outputs
can link into Explore, while notebook authoring remains in Build.

### 12.9 Canvas creation capabilities

Before production drag/drop, add one backend-generated asset capability record
to the workspace/create-asset contract:

```text
produces_relation
accepts_upstreams
root_preferred
execution_gate
validation_attachment
supports_source_role
supports_sink_role
```

The create dialog and canvas consume the same capability record. The backend
still validates the requested relationship when writing files, so a stale
frontend cannot create an invalid graph. Keyboard users receive an equivalent
“Add upstream/downstream/gate/test” action; drag/drop is an accelerator, not the
only path.

### 12.10 Staged delivery

1. **Navigation foundation:** add the typed mode/tool registry and tests;
   refactor today's flat `navItems` to derive from it without changing visible
   navigation. This is the first production slice on this branch.
2. **Shell primitives:** add route static metadata, the mode header, rail,
   contextual sidebar frame, reducer, mobile tool tabs, and content-only drawer
   behind an internal shell switch. Preserve all current destinations during
   comparison.
3. **Build migration:** keep the existing canvas mounted as the Build anchor.
   Move Ad-hoc Query, Notebooks, Data Browser, connections, environments, and
   pipeline settings into the rail. Ad-hoc Query and Notebooks replace the
   central canvas; Data Browser and settings keep it visible and use the wide
   sidebar plus bounded detail overlays. Retain canonical routes.
4. **Run migration:** route Overview, Deployments, Schedules, Runs, and run
   details through the shared shell; consolidate only the overview composition,
   not their service contracts.
5. **Explore migration:** route Catalog, Dashboards, and Reports through the
   shell and add cross-links for notebook/presentation outputs.
6. **Thin Data Browser:** ship `/data` using current config and SQL discovery
   APIs, reusable connection editor, safe table preview, authored/observed
   mapping, and explicit user-triggered loading.
7. **Durable browser contract:** add cached/partial discovery, object-storage
   adapters, revisioned SSE events, scope/cost metadata, pin/recent restoration,
   and server-built previews.
8. **Canvas capabilities:** add the backend contract and only then enable
   structure-aware production drops.
9. **Convergence:** remove transitional menus/old shell paths after desktop,
   mobile, keyboard, command palette, and deep-link parity pass. Fold the
   as-built design into `architecture/frontend.md` and delete this plan.

Each stage is independently releasable and keeps the prior shell reachable
until its replacement passes live E2E coverage. Do not mix route migration,
Data Browser backend work, and canvas mutation semantics in one change.

### 12.11 Verification and rollout gates

Minimum coverage for every shell stage:

- unit tests for route-to-mode/tool exhaustiveness and workbench reducer state;
- component tests for rail focus, restored mode selection, preview replacement,
  and mobile Sheet-to-dialog focus return;
- live E2E for all current destinations, direct deep links, refresh/SSE
  reconciliation, project and environment switching, protected-environment
  policy, and browser back/forward;
- Data Browser live E2E with DuckDB and project-local file fixtures plus
  credential-gated matrix coverage for PostgreSQL and one three-level
  warehouse; object storage gets its own fixture;
- accessibility checks for names, focus order, expanded state, non-color status
  signals, reduced motion, and keyboard-equivalent canvas creation;
- 360, 412, 768, 1280, and 1440 px layout checks with no document-level
  horizontal overflow;
- initial bundle and lazy-route budget checks. The production shell may reuse
  Build dependencies because it replaces the old route, but Data Browser must
  remain lazy outside its route;
- full `make release-check` before removing the transitional shell.

### 12.12 Known risks and decisions deferred to implementation slices

- **Sidebar width versus editor space:** start with the mock's fixed rail and a
  bounded, collapsible/resizable wide sidebar; collapsing preserves selection
  and canvas viewport. Persist width only after resize behavior is verified with
  Monaco and React Flow.
- **One Data Browser, two entry modes:** `/data` is canonical. Return context is
  transient navigation state, not a duplicated Build/Explore route tree.
- **Preview persistence:** session-only pinning is the default. Git-tracked
  saved queries, assets, notebooks, dashboards, and reports already cover
  durable authored state.
- **Warehouse metadata caching:** begin request-scoped with existing endpoints;
  add process-local revisioned cache only with the durable contract so stale
  data is never presented as live by accident.
- **All-project operations:** keep them in the project launcher. The selected
  project shell must not mix identities from multiple Git roots.
- **Feature flag lifetime:** an internal comparison switch may protect shell
  composition during migration, but it must be deleted at convergence rather
  than becoming a permanent two-shell architecture.

## 13. Prototype verification

- `pnpm lint` passes for the complete frontend.
- `pnpm build` passes, including API type generation, TypeScript, Vite, and all
  existing bundle budgets.
- A production Go binary was built with the generated web artifacts embedded
  and served the lab route against the example Git workspace.
- Headless Chromium exercised all three variants at 1440×900 and 412×860 with
  no page/console errors and no document-level horizontal or vertical overflow.
- Interaction coverage exercised desktop Data Browser replacement, observed
  table preview, Query handoff, mode-dependent Workbench rail navigation,
  connection/environment/settings master-detail selection, Run deployment and
  schedule lists, Explore dashboard/report lists, capability-aware asset drop,
  Lifecycle → Run → Schedules, the all-projects launcher, and the mobile
  project-tree → Data Browser and Pipeline settings Sheet transitions. The
  warehouse chooser was separately exercised with all eight bundled marks,
  selection/continuation, Sheet-to-dialog focus transfer, and viewport checks
  at desktop and mobile sizes.
- The completed browser-flow iteration additionally exercised backend-shaped
  connection fields, verification, create-and-return discovery, ready/partial/
  failed-with-cache/empty states, explicit refresh, scoped search, preview
  pinning, and Build → Run → Build tool restoration. Chromium reported no
  console errors or document overflow at 1440×900 and 412×860.
- The persistent-canvas iteration exercised connection overview → catalog
  detail → explicit table preview, Data Browser Back navigation, wide-sidebar
  collapse/restore, Ad-hoc Query and Notebook overlays, and settings handoff on
  desktop and mobile. The production bundle stayed within budget; Chromium
  reported no page/console errors or document-level horizontal overflow.
- The follow-up state correction replaces those two overlays with full-surface
  Ad-hoc Query and Notebook transitions. Formatting, linting, type checking,
  unit tests, and the production bundle pass; repeat the interactive browser pass
  when a browser runtime is available.
- The editor-parity iteration adds Code/Split/Canvas switching, three concurrent
  SQL/Python/YAML asset documents, type-specific structured editors, the Guided
  Properties hierarchy, and the independent bottom result panel. Formatting,
  linting, type checking, unit tests, and the production bundle are the required
  automated gate; repeat desktop/mobile interaction and overflow checks when a
  browser runtime is available.
- The rounded-workbench iteration moves all asset, Ad-hoc Query, and Notebook
  documents into the shared top bar, removes redundant editor/special-surface
  headers, and keeps the rail plus contextual sidebar inside one rounded shell.
  Chromium exercised asset/special-tab switching and closing at 1357×557 and
  412×860; active tabs scroll into view and the document never overflows the
  viewport horizontally.
- The follow-up geometry iteration joins the tab bar and active document into
  one rounded surface, reduces Code/Split/Canvas to accessible icon controls,
  aligns the right asset inspector to the full sidebar height, and makes every
  active rail item toggle its own wide pane. Chromium covered the reported
  697×638, 910×478, 1050×638, and 1668×478 layouts plus 412×860 mobile without
  document-level horizontal overflow.
- The metadata-inspector iteration separates General, Lineage, Columns, and
  Checks into keyboard-accessible tabs while retaining the real section content
  and count context. This tests whether the narrower inspector can stay
  understandable without removing editing capability.
