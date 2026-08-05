# Renart web frontend — current architecture

Status: current state. The frontend is a static React app built by Vite and
embedded into the Go binary; the Go server serves it and owns every
filesystem-changing action. There is no Node.js runtime in production.

## 1. Stack

- **React 19.2 + TypeScript 7**
- **Routing:** TanStack Router (file-based, generated route tree)
- **Build:** Vite 8 via `rolldown-vite`
- **Styling:** Tailwind CSS v4 + shadcn/ui + Radix primitives + Base UI where used
- **Canvas / DAG:** React Flow
- **Editor:** Monaco via `@monaco-editor/react`
- **State:** Jotai for shared domain state, with component-local React state for
  transient view state
- **Charts:** Recharts
- **Panels:** `react-resizable-panels`
- **Command UI:** `cmdk`
- **Icons:** `lucide-react`
- **Markdown:** `react-markdown`
- **Quality tooling:** Oxlint, Oxfmt, and Vitest
- **Realtime sync:** Server-Sent Events (see [backend.md](backend.md) §2).
  Scheduler lifecycle messages are held in a bounded, sequenced frontend
  buffer so adjacent unit, step, and log events cannot be coalesced into a
  single last-value update before consumers observe them.

## 2. Dev server

- Frontend dev server runs on **5173**; Vite proxies `/api` to the Go server on
  **http://127.0.0.1:3000**. See [`../web/vite.config.ts`](../web/vite.config.ts).
- Production output is static and must stay compatible with Go embedding.

## 3. App shape

Paths below are relative to `web/`.

### Entry points

- [src/main.tsx](../web/src/main.tsx) mounts the app.
- [src/providers.tsx](../web/src/providers.tsx) wires app-level providers (`AppProviders`).
- [src/router.tsx](../web/src/router.tsx) builds the TanStack Router from the
  generated route tree (`AppRouter`).

### Routing

File-based routes under [src/routes](../web/src/routes):

- [__root.tsx](../web/src/routes/__root.tsx) → [_shell.tsx](../web/src/routes/_shell.tsx),
  a pathless layout route that renders the app shell.
- Pages live under [src/routes/_shell](../web/src/routes/_shell): the build IDE at
  `/pipelines/$pipelineId/...`, plus `catalog`, `notebooks`, `runs`, `schedules`,
  and `project` (settings). `/` waits for the workspace, then redirects to the
  first pipeline's canvas — or to `/welcome` when the workspace has no
  pipelines.
- [welcome.tsx](../web/src/routes/welcome.tsx) (`/welcome`, outside the shell)
  is the first-run onboarding and new-project wizard
  ([welcome-page.tsx](../web/components/app/welcome-page.tsx)): demo / import /
  empty flows against `POST /api/projects`, with `?new=1` (the project
  switcher's "New project...") forcing creation of a fresh directory instead of
  scaffolding into the current empty workspace. The project-directory response
  marks launcher bootstrap mode, which also forces a fresh project directory
  instead of scaffolding into the server's temporary welcome runtime.
  New-project location uses the
  server-backed directory picker shared with the project switcher; it starts at
  the effective suggested parent and can create child folders. Creation
  conflicts such as an existing target directory stay visible beside the
  create action instead of rendering above a long, scrolled target form, and
  clear when the user changes the project name or location. Demo selection
  reuses the categorized template catalog from the Build view: every starter
  remains visible in one scrollable list while the adjacent pane describes the
  selected starter's connectivity, features, and assets. Demo creation
  bootstraps the workspace with the `build-stale/stream` run (fresh assets are
  all `never_built`) and renders its per-asset SSE progress and ANSI-colored
  output.
- `redesign.$.tsx` / `redesign.index.tsx` redirect legacy `/redesign/*` bookmarks
  to the root paths — the only place the old "redesign" name survives.
- The route tree is generated into
  [src/routeTree.gen.ts](../web/src/routeTree.gen.ts) **by the build** — never edit
  it by hand. After changing route files, rerun the web build so it matches the
  filesystem routes. TanStack Router's automatic code splitting keeps route
  components out of the initial application chunk.

For hierarchical URLs that should not visually nest parent pages, use pathful
layout routes (`route.tsx` renders `<Outlet />`) with leaf `index.tsx` files —
not underscore-flattened route hacks.

### App shell + primary views

- [components/app/app-shell.tsx](../web/components/app/app-shell.tsx) (`AppShell`):
  top nav (Build / Catalog / Notebooks / Runs / Schedules, from
  [app-data.ts](../web/components/app/app-data.ts)), the
  [project switcher](../web/components/app/project-switcher.tsx), including the
  persisted Light / Dark / System appearance selector, the
  [command palette](../web/components/app/app-command-palette.tsx), and the routed
  `<Outlet />`. Once an encrypted local vault exists, the header shows its
  lock state beside the environment and time-range selector. The session can
  be locked or unlocked from a compact desktop popover or a mobile dialog;
  setup and passphrase changes remain in Project settings. The control uses the
  shared workspace-config state, so it reflects the same process-local vault
  session used by connection execution. The source-control sheet renders
  worktree and staged changes
  with Monaco's inline diff editor;
  SQL, Python, YAML asset definitions, JSON, Markdown, and ordinary project
  files select syntax highlighting from their path. Notebook-folder selections
  remain one review unit but render one inline diff per changed cell/file. Each
  file or notebook row makes its complete non-action area the diff target, so
  the path, icon, and status behave as one control rather than as separate
  click hotspots.
- [components/app/build-page.tsx](../web/components/app/build-page.tsx): the primary
  IDE — the interactive lineage canvas
  ([lineage-canvas.tsx](../web/components/app/lineage-canvas.tsx), React Flow)
  beside the asset editor. Bare asset URLs default to this split view; ad-hoc
  queries preserve code/split layout and add the editor beside a canvas-only
  view. The ad-hoc editor can copy its current draft into a new or existing
  notebook cell, or open the New asset dialog as a SQL asset with the draft
  prefilled. A shadcn connection selector is populated from the backend's
  `query_connections` workspace contract rather than a frontend warehouse
  allowlist. Its pipeline-scoped selection controls execution, schema
  discovery, parse context, formatting, the complete SQL LSP surface, and the
  connection preselected when converting to an asset; the selected pipeline
  asset supplies only graph and Jinja scope. Both conversions use the normal Go
  mutation APIs and keep the in-memory draft intact. Ad-hoc results keep the
  effective rendered query in a compact disclosure above the table. When the
  response is capped, that strip shows a warning icon and appends the effective
  row cap as `LIMIT <n>` to the preview; the table remains unobscured. The
  terminal icon indicates expanded state through its stroke weight, and the
  disclosure hover surface includes its copy action. The first asset selection
  from a pipeline-only canvas opens the split view; after an asset is present in
  the route, later selections
  preserve the explicit code/split/canvas layout. A DAG that fits at the default
  zoom is horizontally centered on initial render, while a wider DAG keeps its layout
  origin so it remains predictable to pan. Layer-band layout reserves deterministic
  empty row slots for same-prefix dependencies that skip intermediate ranks; when
  such a lane is unambiguous, intermediate asset nodes cannot sit directly on the
  rendered edge. After that initial positioning, a
  routed selection is smoothly brought into view only when it falls outside
  the current viewport; this preserves the selected node when the canvas is
  resized into split view without fighting user panning. Hovering an
  asset-backed relation in the SQL editor resolves it through the definition
  endpoint and applies a transient, reduced-motion-safe highlight to the same
  canvas node. Only Monaco's painted text targets are eligible; empty space to
  the right of a short line cannot inherit the line's final relation token and
  spuriously animate a node. Build and Catalog share the same
  farther-out zoom range; below the detail threshold, fixed-size asset cards
  become icon-only overview nodes and group labels disappear so unreadable text
  does not clutter a whole-DAG view. The explorer's asset filter searches names,
  groups, paths, types, and connections. The toolbar keeps Deploy as a separate
  secondary action and makes **Review run** the primary pipeline action. Type
  checks live in the results panel, which scrolls through a shadcn ScrollArea;
  failing assets also receive a warning marker on their canvas node. Assets
  whose latest successful write has failed runtime assertions receive a
  separate **Checks failed** badge only while that outcome matches the current
  asset content. Selecting it opens the asset properties and scrolls to and
  briefly highlights the exact failed custom or column check; the quality card
  lists every failure when more than one check failed. This does not replace the
  asset's independent freshness or last-build state. The Render
  result keeps its wrapping provenance and comparison controls in a shrinkable
  ScrollArea so the operation or side-by-side diff retains visible height on
  narrow screens. The shared
  run/deploy/redeploy review surface is a wide, height-bounded dialog rather
  than a side sheet, so rendered operations and deployment comparisons retain
  useful width. It defaults to the entire pipeline and names the exact
  saved working tree or immutable deployment, environment, UTC interval,
  refresh/sensor mode, asset and execution-unit counts, checks, blockers,
  warnings, and source/configuration/variable identities. Run review is one
  linear reading path: readiness issues and code-check findings, followed by a
  shared, initially collapsed Execution details section containing the exact
  ordered asset/window units and their rendered operation/check sequence. The
  deployment review uses the same section for representative execution.
  Runtime-only Python notices are aggregated across affected assets. Run options stay
  visible in a compact responsive row: scope receives the most room, sensor
  policy stays secondary, full refresh is an explicit switch, and the selector
  editor only appears when that scope needs it. Source identities, resource
  claims, and write isolation remain available under Plan details without
  competing with the decision. The dialog heading, context, options, and review
  body share one vertical ScrollArea so the scrollbar never changes the width
  between its upper and middle sections; confirmation remains fixed beneath it.
  Successful code checks use one compact green result while assets with
  findings retain their expanded messages. Opening Execution details lazily
  requests redacted
  stage content and shows compiled queries, generated materialization SQL,
  checks, and semantic/runtime-only operations in read-only Monaco with
  `Preview — not executed`. The initial review context stays stable while
  background workspace/deployment refreshes arrive; confirmation still
  revalidates every identity server-side and replaces a stale plan for another
  review. Entire-pipeline and Needed plans execute their exact reviewed
  asset/window units. Needed confirmation may visibly omit units that became
  fresh, but never adds or widens work without another review. Destructive
  policy requires typing the exact environment. Active-run blockers and
  admission races link to the canonical run. A `deployed_only` environment with
  no executable deployment opens the same review surface with an actionable
  blocker instead of guessing a source. A temporarily invalid asset definition
  appears as an asset-scoped blocker while renderable siblings remain visible.
  Pipeline and asset execution plus Deploy await every mounted editor's
  pending/in-flight save, so the saved source named by the action includes
  visible Monaco edits.
  **Deploy** opens the same dialog in a definition-only deployment mode rather
  than mutating immediately. It reviews the entire saved working tree, keeps
  execution policy/data freshness out of the gate, and follows one linear
  reading path instead of dividing the decision across tabs: source changes and
  code checks come first, followed by compact disclosures for deployment
  contents, runtime checks, plan identities, representative execution, and
  schedules. Exact added/changed/removed files are collapsible rows whose
  deployed/workspace comparison opens directly beneath that file. Each
  comparison uses Monaco's real DiffEditor, including its native
  inserted/deleted line and character highlighting, and the final write remains
  bound to the reviewed source Merkle. Afterward the schedules disclosure opens
  and offers an
  unchecked list of older schedule pins; only explicitly selected rows move.
  Type-check does the same; transport/save failures remain visible in the bell
  and results panel without erasing the last successful report. Every supported
  SQL, Python, seed, Load, API, ingestr, and sensor asset also exposes a
  read-only `Render` action after the same save barrier. The result tab shows
  exact compiled/execution SQL where available, semantic JSON operations for
  non-SQL work, or a runtime-only description for Python. It labels every
  stage's fidelity and redaction, gives each runtime quality check its own
  column/custom-check label and SQL tab when renderable, and always identifies
  the saved source, environment, interval, and `Preview — not executed` status.
  While the Render tab is open, changing the selected asset, saved-intent
  content, environment, or interval automatically loads the matching latest
  render after a short typing debounce; users do not have to press Render again.
  For a working-tree preview, **Compare deployment** defaults to the latest
  deployment and allows selecting an older snapshot. The server renders both
  sources with one context and aligns semantic stages; Build presents the
  selected operation in Monaco's side-by-side read-only diff editor with
  explicit Deployment/Saved workspace labels and added/removed/changed status.
  Compact badges show the asset/DAG fingerprint and either the opaque exact
  physical-target identity or its runtime-only state; full target context stays
  in the badge title without exposing endpoint coordinates.
  Build freshness retains only the last successful response for the exact
  environment/window selection. Requests are tracked per pipeline; a matching
  SSE freshness event authoritatively resolves only that pipeline and cannot be
  overwritten by an older HTTP response. A transport failure disables the
  action as **Freshness unavailable** instead of replacing unknown state with
  **Fresh**, while a later matching event clears the recovered pipeline's
  error without hiding unresolved sibling pipelines.
  Structured `pipeline_run_active` conflicts retain the backend's active run ID
  and link Build output, schedule actions, and rerun errors to that run. Build
  materialization output and asset materialization status ignore late scheduler
  events after `run.finished`, so a very fast worker cannot turn those finished
  results back into queued. Terminal events are remembered until the trigger
  response associates the run ID with the Build result, then reconciled with
  the canonical stored log; this also covers runs that finish before the trigger
  response arrives.
- [components/app/asset-editor.tsx](../web/components/app/asset-editor.tsx): the
  Monaco editor plus guided metadata cards
  ([asset-guided-cards.tsx](../web/components/app/asset-guided-cards.tsx)); the
  metadata inspector currently keeps its raw YAML view hidden. It wires intellisense through
  [use-asset-monaco.ts](../web/hooks/use-asset-monaco.ts). Load, seed, and
  non-query sensor assets replace Monaco with compact YAML-like parameter
  editors in the same main pane. Query sensors project `parameters.query` into
  the normal SQL Monaco surface, including LSP diagnostics, completion,
  navigation, Jinja support, and formatting; `poke_interval` and `timeout` stay
  in a compact footer below it. Seed editors show `path`, `file_type`, and
  `enforce_schema` before one replacement field that accepts pasted text,
  dragged files, or file-picker selections. Every replacement requires an
  explicit confirmation before the browser uploads through the Go server and
  refreshes columns from the new local file. Pasted CSV, TSV, JSON, JSON Lines,
  and text share one auto-detection/override utility with creation.
  Generic identity and dependency metadata remains in the inspector, along with
  columns and checks for relation-producing assets; sensors omit columns and
  checks because they do not materialize a relation. Custom SQL checks remain
  available for other supported asset types even when they do not expose a
  column workbench. The Columns card's
  **Sync schema** action automatically uses the asset definition and places
  backend-advertised observed sources beside it as optional checkboxes (for
  example **Live request** and **Current table**). Safe additions and type fills
  finish inline. Conflicts open a wide, scrollable shadcn dialog whose table
  compares each source, saved metadata, and the chosen result without replacing
  the inspector with asset-kind-specific controls. The same card can add a
  declared column manually; that edit uses the semantic transaction API and
  column-local provenance rather than relying on the hidden raw YAML editor.
- The Build view's **New pipeline** dialog loads the backend template catalog
  and presents the blank option plus feature-focused runnable starters in the
  same compact catalog used by onboarding. Category headings organize one
  scrollable, single-click list; an adjacent detail pane shows only the selected
  starter's description, assets, feature tags, and offline/network requirement,
  so adding starters does not turn the dialog into a long card grid or a nested
  menu. Selecting a starter fills its suggested directory and name without
  overwriting a user's custom values. The height-bounded dialog sends only the
  selected template ID and names; generated files remain a backend concern and
  the workspace SSE update remains the navigation signal.
- The Build view's New asset dialog loads the selected pipeline/environment's
  secret-free `asset-creation-profile`. The six equal-size choices are authoring
  intents; `AssetConnectionField` presents only compatible configured
  connections (two roles for Load), a resolved pipeline default, portability
  warnings, and no separate dialect or concrete-type choice. It never derives a
  SQL type in TypeScript. `useAssetCreationProfile` is shared by creation and
  existing-asset editors, and the backend profile also supplies the candidates
  behind each creatable connection type. `WorkspaceConnectionDialog` composes
  the existing connection hook and form in a wide, independently scrollable
  shadcn dialog. It locks the current environment, filters connection types to
  the opening role, preserves the mounted asset draft, refreshes the profile
  after save, and selects the new connection. Manage connections is the explicit
  exit to the complete settings surface. Seed and Sensor detail fields continue to
  consume the workspace's backend-provided `asset_capabilities` contract
  ([semantic-asset-create-fields.tsx](../web/components/app/semantic-asset-create-fields.tsx)).
  Its six top-level asset-kind choices use one fixed tile size and animate into
  a compact selected-kind summary once chosen, while the creation fields use the
  plain `Field` variant instead of nesting a bordered card around each input.
  The dialog is shrink-safe, keeps focus rings inset inside its shadcn
  ScrollArea, and suppresses horizontal overflow around long horizontal
  fields. HTTP API creation defaults to the custom OpenAPI template and requires
  the user's spec URL; that URL is persisted into the starter and immediately
  drives endpoint, parameter, record-path, and response-field suggestions.
  When opened from the ad-hoc editor, SQL creation sends the draft as
  executable content so the backend composes it with the canonical generated
  asset header instead of treating the query as a complete asset file. SQL
  creation carries the ad-hoc editor's effective connection. Downstream SQL is
  fixed to the source target and therefore omits a target picker; downstream
  Python initializes from that connection but can choose another compatible
  target, while downstream Load fixes it as the source connection. Unsupported
  carried values remain visible for an explicit correction rather than falling
  back to another dialect. It offers
  upload/paste/workspace-file/URL seed sources, renders the parameters required by
  each sensor variant, and sends uploaded bytes through the multipart asset API.
  The workspace-file source uses the shared path combobox, restricted to
  supported files below the workspace root. The resulting seed and sensor assets
  have distinct canvas/catalog classifications while remaining ordinary asset
  files in the workspace.
- The guided identity card shows asset Type as a read-only value and uses
  `AssetConnectionEditor` for SQL, Python, API, Load, Seed, and Sensor assets.
  The editor narrows the same backend profile to the existing Seed/Sensor
  variant, preserves invalid current values, and asks for confirmation before a
  connection change that also changes the concrete type. Its semantic update
  includes the expected current type and saves both fields through the Go
  server. For SQL assets with inferred upstreams or downstreams, that
  confirmation also names the connected assets and warns that pure SQL cannot
  span the resulting connection boundary; it remains an explicit migration
  rather than silently rewriting the graph. The Load source editor now consumes
  the profile's source role and destination category as well; the old client-side
  SQL-type and Load-category compatibility tables have been removed. The raw YAML editor is not exposed,
  but its Type and connection identity fields are also static so re-enabling it
  cannot bypass the reviewed migration.
- Other pages: [catalog-page.tsx](../web/components/app/catalog-page.tsx),
  [notebook-page.tsx](../web/components/app/notebook-page.tsx),
  [runs-page.tsx](../web/components/app/runs-page.tsx),
  [schedules-page.tsx](../web/components/app/schedules-page.tsx),
  [settings-pages.tsx](../web/components/app/settings-pages.tsx),
  [welcome-page.tsx](../web/components/app/welcome-page.tsx).
  Project **General** settings expose the effective tracked retention policy
  from `.renart/project.yml`: age windows for runs, logs, raw materialization
  facts, schedule history, deployments, and abandoned temporary directories,
  plus the per-pipeline run/log/deployment floors. Integer validation happens
  in both the form and Go service; saving replaces the complete policy.
  Connection sheets consume backend-provided `is_sensitive` and
  `is_sensitive_file` metadata. Sensitive inputs never populate browser state
  with a saved value. They show configured/missing/unavailable status and the
  safe provider/reference descriptor, with explicit keep, replace, and clear
  behavior. Ordinary credentials default to **Credential store**; the
  two-choice source control can instead save only an environment-variable name
  for headless/CI use. Environment mode validates `env:NAME` without accepting
  a value, while local replacement remains a write-only password input.
  Credential-file fields use the same control for a write-only path or an
  environment-supplied path. Leaving an existing field unchanged means keep.
  Provider/manifest parse failures remain visible as shadcn alerts instead of
  silently resetting the form. The same write-only form contract is used by
  inline asset connection creation and onboarding database import.
  Pipeline settings use a vertical, icon-labelled shadcn tab menu at desktop
  widths and retain the compact horizontally scrollable section buttons on
  mobile. The dialog has one fixed viewport-relative height, its desktop sidebar
  stretches through the available body, and the shared ScrollArea absorbs
  section-length changes instead of resizing the dialog. The Python section
  edits the pipeline-root `pyproject.toml` dependency list; Microsoft Teams has
  no settings surface, while any existing unsupported notification fields are
  preserved when another section is saved. General settings distinguish
  **Overlapping pipeline runs** (`concurrency`) from **Maximum active steps**
  (`max_active_steps`). A blank step value explains the sequential default;
  values above one opt safe independent assets into bounded overlap.
  Connections select platform and connection defaults from the configured
  pairs in the active environment, prevent duplicate platform rows, and show
  unavailable legacy values until the user explicitly corrects them. The same
  section includes a read-only, asset-grouped inventory of every effective
  source/target connection resolved by the backend. Variables use JSON-schema
  type choices and type-aware default controls (numeric, boolean, string-list,
  or JSON object) so saved defaults retain their value types. Resolved
  `var.name` references use Monaco's native Ctrl/Cmd-hover link affordance; go
  to definition opens this Variables section, scrolls to the matching
  definition, and highlights it.
  Run details use semantic event badges, link current-workspace asset events
  back to the split Build view, and render timeline asset names in a dedicated
  column with full-name tooltips. Timeline rows contract from 28px to 12px as
  the run grows so as many as 19 assets remain visible without an inner
  scrollbar; 20 or more use a fixed 16px row and an independent ScrollArea.
  Hovering an asset timeline row or one of its event rows highlights the
  matching rows in both views. A running timeline follows newly appended rows
  to the bottom. Clicking either an event or a timeline row selects the asset
  and scrolls the counterpart view to its matching row; timeline clicks also
  return the lower panel to its Events tab.
  Queue-backed active runs expose a destructive, confirmed Abort run action.
  A running cancellation shows `Stopping` from River's durable request state
  until the terminal SSE event replaces it; queued cancellation becomes
  terminal immediately.
  Runs admitted from a reviewed plan add a Plan tab with the immutable final
  unit order and statuses, inclusion reasons and exact windows, safe Needed
  preview omissions, source/configuration/data identities, and retained
  redacted stage metadata. Run review states the effective active-step limit,
  keeps units in stable plan order, and explains that dependencies,
  per-connection limits, and conservative/shared targets can lower effective
  overlap. `run.unit` SSE updates that ledger live without
  teaching unrelated asset-result consumers to treat units as pipeline steps.
  Duration tooltips anchor to the duration bars rather than the full timeline
  tracks.
  Schedule rows keep cadence, timezone, last-run result, deployment, catch-up,
  and runtime-window context in a wrapping metadata area rather than one
  truncated status line. Timeline and actions have dedicated columns: `Run
  pinned #N` is the primary action, reviewed deployment repair/update is
  secondary, and edit/archive are in the row's overflow menu. The edit dialog
  keeps pipeline/environment identity fixed, edits the version-controlled
  cadence and lifecycle fields, and defaults to preserving server-private
  overrides; replacing or clearing them is an explicit mode. The displayed
  deployment identifies the exact pin
  used by the run; pinless rows show `Needs deployment`, and rows with stored
  variable overrides show an `Overrides` badge and value-free name tooltip.
  Each row identifies whether its definition comes from
  `.renart/schedules.yml` or is a local legacy row. Schedule creation accepts
  optional JSON objects for literal overrides and typed `env:NAME` or
  `local:alias` secret references, validates the resolved context against the
  exact chosen deployment, writes only references into desired state, and
  explicitly chooses an existing
  executable deployment or reviews and deploys the saved workspace before
  pinning the returned version locally. Declaration-removal tombstones explain
  that the project file must be re-added instead of presenting a nonfunctional
  Restore action. A due interval waiting for planning or the pipeline slot is
  exposed as `Run waiting`; after a failed/cancelled attempt it becomes `Retry
  waiting`. Its tooltip shows only the retained interval, and a dedicated
  `schedule.occurrence` SSE event refreshes the schedule response without
  polling or exposing the private occurrence key. `Run pinned #N` calls the
  row-owned endpoint, so the browser never has to resend private values.
  Standalone deployment never changes
  existing pins implicitly; explicit multi-schedule promotion is one
  compare-and-swap batch. The
  page reports whether the scheduler is available. A supported workspace
  runtime is its sole server and therefore its scheduler owner. The follower
  state is a fail-closed compatibility safeguard, not a hot standby; follower
  and unavailable instances keep schedules and timelines readable but disable
  creation, pin changes,
  pause/resume/archive/restore, and queued runs with an explanatory alert;
  ownership is unknown and therefore fail-closed while the request loads. Run
  details display the recorded source and use the backend's dynamic
  re-execution descriptor. Eligible retained plans show `Re-execute exact plan`
  plus selection and unit count; the run-owned endpoint receives no execution
  context from the browser and the accepted manual run opens immediately.
  Legacy, blocked, incomplete, or source/configuration-drifted rows instead
  show `Run again with current settings`, including the safe reason exact replay
  is unavailable. That fallback names its source and reuses environment/window
  only when the backend marks the execution context resolved; legacy or
  pre-execution-failed rows omit request-only values so current defaults are
  resolved at start. Its copy explicitly says that modes, variables,
  authorization, selection, and schedule-only context are not replayed.

Project connection routes accept an environment/connection search target so
pipeline default-connection links can open the exact editable connection sheet.

All feature UI lives under `components/app/`; shared primitives under
`components/ui/`. Prefer the shared shadcn card primitives
([components/ui/card.tsx](../web/components/ui/card.tsx)) for panelized UI rather
than hand-rolled `div` shells.

## 4. Key hooks

- [use-workspace-sync.ts](../web/hooks/use-workspace-sync.ts): fetches
  `/api/workspace`, subscribes to `/api/events` (SSE), reconciles workspace state,
  preserves asset `content` on lite SSE updates when appropriate, and dispatches
  run, schedule, staleness, and per-notebook runtime events to their Jotai
  domains. The app shell owns this single browser SSE connection.
- [use-asset-content-editing.ts](../web/hooks/use-asset-content-editing.ts): editor
  draft state, display-value sync, and the Ctrl/Cmd+S save path.
- [use-debounced-asset-save.ts](../web/hooks/use-debounced-asset-save.ts): debounced
  writes back to the backend.
- [use-asset-monaco.ts](../web/hooks/use-asset-monaco.ts): mounts Monaco for the
  asset editor and wires the SQL / Python / YAML / Jinja intellisense hooks.
- [use-sql-lsp.ts](../web/hooks/use-sql-lsp.ts): SQL intellisense via the Go LSP
  (`/api/sql/lsp/*`) — completions, diagnostics, definition, hover, rename. See
  [sql-lsp.md](sql-lsp.md).
- [use-python-query-intellisense.ts](../web/hooks/use-python-query-intellisense.ts):
  maps static SQL string literals in Python `query(...)` calls onto the same Go
  LSP, merges schema-aware client completions (including notebook run columns),
  and translates SQL completions, diagnostics, navigation, and highlighting
  back into the Python Monaco model. A static second positional connection or
  `connection="..."` keyword accompanies every projected LSP request; dynamic
  connection expressions deliberately remain runtime-only.
- [use-asset-results.ts](../web/hooks/use-asset-results.ts): inspect and materialize
  flows, including API-asset full refresh, scheduler-run links, and terminal
  event/trigger-response correlation for very fast runs.
- [use-app-asset-materialization-status.ts](../web/hooks/use-app-asset-materialization-status.ts):
  freshness / materialization enrichment with a post-terminal event guard.
- [use-pipeline-staleness.ts](../web/hooks/use-pipeline-staleness.ts): per-pipeline
  request state and selection-matched SSE/HTTP ordering.
- [use-pipeline-runs.ts](../web/hooks/use-pipeline-runs.ts),
  [use-env-schedules.ts](../web/hooks/use-env-schedules.ts),
  [use-pipeline-deploy.ts](../web/hooks/use-pipeline-deploy.ts),
  [use-source-control.ts](../web/hooks/use-source-control.ts): run, per-environment
  schedule, deploy, and VCS surfaces. Run state does not load or mutate the
  retired single-environment schedule API.

## 5. Libraries / helpers

- [lib/api-\*.ts](../web/lib/api-core.ts): per-domain clients (`api-assets`,
  `api-pipelines`, `api-config`, `api-sql-discovery`, `api-sql-lsp`,
  `api-scheduler`, `api-source-control`, …) are the frontend surface for Go
  endpoints. Callers import the owning domain directly; there is no
  application-wide API barrel.
- [lib/types.ts](../web/lib/types.ts): shared web-side types; re-exports the
  generated API types. The generated types come from the Go DTOs via
  `web/scripts/generate-api-types.mjs` (see [backend.md](backend.md) §5) — don't
  hand-edit `web/lib/generated/api-types.ts`.
- [lib/atoms/](../web/lib/atoms): Jotai atoms split by domain (`workspace`,
  `selection`, `editor`, `results`, `materialization`, `sql-discovery`, suggestion
  catalog).
- [lib/app-lineage-layout.ts](../web/lib/app-lineage-layout.ts): lineage canvas
  layout engine.
- [lib/asset-visualization.ts](../web/lib/asset-visualization.ts): visualization
  metadata parsing.
- [lib/project-context.ts](../web/lib/project-context.ts): per-tab project pin
  (sessionStorage) — `projectApiPath` rewrites `/api/...` onto the pinned
  project's `/api/projects/{id}/...` mount in one place; no pin means the
  server's default project via the unprefixed alias. Two tabs can work on
  different projects against one server.
- [lib/features.ts](../web/lib/features.ts): project-scoped feature flags.
  Ingestr surfaces (source connection types, asset kinds) render only when
  `.renart/project.yml` sets `features.ingestr` or the workspace already
  contains ingestr assets (see [backend.md](backend.md) §2).
- [lib/sql-schema.ts](../web/lib/sql-schema.ts): schema context for SQL
  intellisense. It scopes tables using only the effective connection resolved by
  the backend; it never guesses a connection from an asset type or selects an
  arbitrary same-platform connection.
- [lib/api-asset-templates.ts](../web/lib/api-asset-templates.ts): the four
  pattern-focused HTTP API starters used by the New asset dialog. The default
  starter accepts the user's OpenAPI URL rather than embedding a demo service;
  another starter demonstrates a nested JSON POST body. The guided editor finds
  the `parameters:` span by the next plausible top-level YAML key, so malformed
  pasted JSON braces remain visible and repairable instead of being hidden in an
  uneditable file tail.
  API assets also have sampled response inference, OpenAPI/path diagnostics,
  relative `response.fields` completion below `records_path`, and persisted
  cursor controls in the guided editor; see
  [http-api-assets.md](http-api-assets.md).

## 6. Visualization metadata

Inspect/preview rendering is driven by asset metadata keys. Common ones:
`web_view`, `web_chart_type`, `web_chart_x`, `web_chart_series`,
`web_chart_title`, `web_table_columns`, `web_table_limit`, `web_table_dense`,
`web_markdown_column`, `web_markdown_template`. When changing visualization
behavior, keep the full inspect view and the asset-node preview in sync.

## 7. Layout notes

The right editor pane is sensitive to flexbox overflow bugs. When touching
editor-pane layout, tabs, or visualization settings:

- flex children that must shrink use `min-w-0`;
- avoid width rules that preserve expanded sizes after resize;
- prefer truncation over overflow for tab labels and compact controls;
- validate both expansion and shrinking of the resizable pane.

Relevant files: [asset-editor.tsx](../web/components/app/asset-editor.tsx),
[build-page.tsx](../web/components/app/build-page.tsx),
[components/ui/tabs.tsx](../web/components/ui/tabs.tsx).

## 8. Validation

Run `pnpm check` from `web/` to verify the embedded wasm build, Oxfmt, Oxlint,
Vitest, TypeScript, and the Vite production build (prefer `pnpm` over `npm`).
For behavior that touches workspace sync, canvas interactions,
inspect/materialize, or Monaco, run the live e2e suite:
`corepack pnpm test:e2e:live` in `web/`.
