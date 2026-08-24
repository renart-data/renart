# Notebook agent evaluation, interaction, and notebook data-grid UX

> **Status (2026-08-24): approved direction, implementation pending.** This
> plan covers the next focused notebook release slice. Current behavior remains
> documented in [`architecture/notebooks.md`](../architecture/notebooks.md).

## 1. Outcome

Make the native notebook assistant dependable on realistic analysis work, let
it ask the user structured questions and request credential-blind access to a
new or existing connection, and make notebook results behave like a compact,
keyboard-friendly data grid. Complete the adjacent release-polish items that
share these surfaces: Markdown proofing, notebook cell chrome, agent composer,
chart contrast, and dashboard/report scrolling.

The implementation must preserve Renart's existing boundaries:

- authored notebook state stays in Git-backed files and changes only through
  revision-checked semantic notebook operations;
- agents use the notebook MCP surface, never the workspace filesystem, shell,
  Git, raw HTTP, or secrets;
- the browser creates or edits connections through the existing write-only
  connection form;
- the server resolves credentials and runs bounded operations; credentials are
  never returned to the browser, agent process, MCP payloads, logs, or authored
  notebook files;
- non-DuckDB sources retain the existing explicit first-import/refresh review;
- workspace refresh continues through SSE rather than polling.

## 2. Current state and gaps

### 2.1 Agent runtime

The native Ask/Edit chat already launches Codex, Claude Code, or OpenCode in an
isolated directory and exposes only notebook-scoped semantic MCP tools. It can
reference cells and assets, search the credential-free workspace catalog,
prepare/apply revisioned change sets, and explicitly run cells in Edit mode.
The external `renart mcp` command and native chat share this semantic contract.

The missing pieces are:

- no first-class way for an agent to pause and ask the user a structured
  question;
- no way to request a missing connection without asking for credentials in
  prose;
- no turn-scoped live discovery/query access for data which is configured but
  not represented by pipeline assets;
- no repeatable authenticated evaluation harness or checked-in realistic task
  corpus. CI currently proves the protocol with a fake provider, but not model
  effectiveness.

### 2.2 Result tables

`VirtualDataTable` is shared by notebook result previews and asset inspect
views, but each cell is currently a hover-card trigger. There is no persistent
cell selection, range selection, keyboard navigation, or selected-region copy.
The table visualization renderer used by notebooks, dashboards, and reports
still has a separate plain table implementation.

### 2.3 Editing and presentation polish

- Tiptap and Markdown-source controls leave browser spellchecking active while
  an unfocused Markdown block is being read.
- SQL/Python cards rely on `focus-within` but have no durable selected-block
  state. The editor has a nested rounded border inside the outer block card.
- the chat send control uses an icon-only `InputGroupButton` with inline-icon
  metadata, producing inconsistent sizing/alignment.
- the workspace/default chart palette has insufficient series contrast.
- dashboard/report authoring can grow beyond its viewport without scrolling.
  The central builder flex item is missing a complete `h-full min-h-0`
  constraint chain, so the Radix viewport is not reliably the scroll owner.

## 3. Product decisions

### 3.1 Native interactions are turn-scoped MCP capabilities

Add two native-only MCP tools:

1. `ask_user` presents one small questionnaire containing one to three prompts.
   A prompt can be single choice, multiple choice, or short text. It includes a
   concise reason and an optional recommended choice.
2. `request_connection_access` explains the required data and optionally names
   a connection type or existing connection. The UI either asks the user to
   approve an existing connection or opens the existing connection-creation
   dialog. Successful creation/approval returns only the connection name, type,
   environment, and granted capabilities.

The tool call waits while the browser shows the interaction. The active turn's
30-minute context and cancellation own that wait. Answer, dismiss, reset,
disconnect, or turn cancellation resolves it exactly once; there is no polling.
Pending interaction state is part of the complete notebook-agent SSE snapshot,
so reconnects restore it.

These tools are not exposed by the ordinary external `renart mcp` command. The
native runner gives its child MCP process an opaque, random, single-turn token.
The owning server binds that token to the notebook, turn, mode, and expiration.
The token grants interaction routing only; it is not a workspace API token or a
credential. It is passed through the isolated process environment rather than
rendered into authored files or provider prompts.

### 3.2 Questionnaire UI

Use shadcn's Base UI Questionnaire composition rather than creating a parallel
form language. Render the pending questionnaire inside the chat timeline, keep
the composer disabled while a required answer is pending, and show the chosen
answer as a compact user event after submission. Connection requests use the
same visual language but embed the existing `WorkspaceConnectionDialog` for
secret entry.

The agent never receives intermediate form values. It receives only the final
answer or a typed declined/cancelled result.

### 3.3 Connection grants and live data access

Add native Edit-mode tools available only through an active interaction token:

- `list_query_connections`: credential-free connection names, normalized type,
  SQL dialect, environment, and whether the current turn has a grant;
- `discover_connection_catalog`: bounded database/table/column discovery for a
  granted connection, reusing `SQLService` and its remote-catalog observations;
- `query_connection_sample`: one parsed, read-only result-producing statement
  against a granted connection, with a server-enforced row limit, byte limit,
  and timeout.

Local notebook DuckDB is implicitly available and needs no questionnaire.
Every non-DuckDB connection grant is explicit and expires with the active turn.
Creating a connection does not automatically import data; it only grants
credential-blind read access to the active turn. The agent adds a notebook data
source through the ordinary `cell.create` semantic operation. Existing source
approval still blocks its first non-DuckDB import/refresh until the user runs it
in Renart.

The query tool defaults to 50 rows and caps at 100 rows, 128 KiB serialized
output, 8 KiB per value, and 30 seconds. The server validates exactly one
read-only result-producing query with Golyglot using the resolved connection
dialect, applies the limit before warehouse execution, and redacts errors with
the resolved connection's redactor. The generic browser ad-hoc endpoint is not
made part of the MCP contract.

### 3.4 Evaluation is opt-in, reproducible, and scored

Keep fake-provider protocol tests in ordinary CI. Add an opt-in authenticated
evaluation command which copies a fixture into a temporary Git repository,
starts Renart, drives a native agent turn through public HTTP/SSE contracts,
and records:

- fixture and task ID;
- Renart commit/version, agent client/version, provider/model;
- initial and final notebook outline, diagnostics, and run results;
- MCP tool sequence, interaction answers, retries, duration, and terminal
  status;
- exact Git diff and objective assertions;
- secrets/redaction scan results.

The harness never commits or pushes. Its timestamped run artifacts live outside
the fixture and are ignored by Git. Checked-in task manifests and objective
assertions are deterministic; model prose is not golden-tested.

Initial corpus:

1. explain a referenced failing cell in Ask mode without mutation/run tools;
2. repair an existing SQL cell and run only its dependency cone;
3. add a checked visualization to an existing notebook;
4. choose a retained-history asset over a current/truncate-replace asset using
   catalog materialization and lineage metadata;
5. build a useful notebook from an empty notebook using existing pipeline
   assets;
6. build a useful notebook from an empty notebook when the data exists only in
   an ephemeral Postgres fixture, including a connection request, user grant,
   discovery, bounded query, source creation, and reviewed import;
7. ask a clarifying questionnaire before making an ambiguous chart/metric
   choice;
8. preserve a concurrent human edit through a revision conflict and corrected
   retry;
9. cancel while waiting for a questionnaire and while a provider/run is active;
10. prove the resulting changes are limited to exact reviewable notebook files
    and contain no credentials or agent commits.

Each task reports pass/fail per objective, tool-policy violations, invalid
change-set attempts, and human interventions. Establish a baseline with Codex
first; Claude Code and OpenCode are only claimed after their own authenticated
corpus runs.

### 3.5 Result-grid interaction follows spreadsheet conventions

Extend `VirtualDataTable` with a controlled-capable selection model identified
by row and column index:

- click selects one cell;
- drag or Shift+click extends a rectangular range;
- Ctrl/Cmd+click toggles cells without discarding the primary range;
- arrow keys move the active cell; Shift+arrow extends the range;
- Space toggles the active cell, Escape clears, and Ctrl/Cmd+A selects all
  loaded cells;
- Ctrl/Cmd+C copies the selected rectangle as TSV and HTML, leaving empty
  fields for holes in a disjoint selection;
- focus uses a roving tab stop, scrolls virtualized rows into view, and exposes
  `role=grid`, row/column counts, and `aria-selected` state.

Only the active selected cell can open its full-value detail popover, using
Enter, Space, or a second pointer activation. Plain hover never opens full
content. Keep the separate whole-table Copy action.

The selection reducer and clipboard serializer are pure functions with unit
tests. Use the enhanced table in notebook outputs, asset inspect, and structured
table visualizations in notebooks/dashboards/reports. Other bespoke tables are
not migrated unless their row-action semantics fit the same grid contract.

### 3.6 Notebook block selection and visual polish

Introduce one durable selected block ID in the notebook page. Pointer/focus on
SQL, Python, Markdown, control, or visualization blocks selects that block;
selection drives the existing inspector where applicable. An inactive block is
nearly frameless. Hover gets a light boundary; selected/focus-within gets a
clearer outer primary boundary and subtle surface. Remove the extra rounded
border around Monaco while retaining the resize separator and error/result
boundaries.

Set browser spellcheck on the Tiptap contenteditable and Markdown source
textarea only while that editor has focus. This affects the shared Markdown
editor in notebooks and report text sections.

Fix the chat composer by following the shadcn Input Group pattern: keep the
reference control at bottom-left, use a square primary send button at
bottom-right, remove inline-start metadata from the icon-only Send icon, and
make loading/disabled/stop states occupy the same aligned action slot.

Increase the default palette's visual separation in both light and dark themes
and verify at least five adjacent series. Do not use color as the only state
signal.

### 3.7 Dashboard/report scrolling

Make the presentation route, tabs content, builder shell, central `<main>`, and
ScrollArea form one explicit `h-full min-h-0` chain. The visual builder's canvas
owns vertical scrolling while the command bar and wide sidebars remain fixed.
The definition editor retains its own Monaco viewport. Audience dashboard and
report views keep their existing page ScrollArea. Avoid document/body scrolling
inside the fixed app shell and avoid horizontal scrolling at narrow widths.

## 4. Contracts

### 4.1 Agent snapshot

Add an optional interaction to `NotebookAgentSnapshot`:

```text
interaction:
  id: opaque interaction id
  turn_id: active turn id
  kind: questionnaire | connection_access
  status: pending | answered | declined | cancelled
  title: short user-facing title
  description: short reason
  questions: bounded typed questionnaire prompts
  connection_request: optional requested type/name/capabilities
  created_at: RFC3339
```

Answers are not accepted in the general start-turn request. Add a narrow route:

```text
POST /api/notebooks/{notebookID}/agent/interactions/{interactionID}/answer
```

The request contains either typed questionnaire answers, an approved connection
name, or `declined: true`. The service verifies notebook, active turn,
interaction ID, allowed option values, and connection existence before waking
the MCP call. Duplicate/stale answers return a conflict without changing state.

### 4.2 Native MCP session

Extend MCP `Policy` with a native interaction capability and opaque turn token.
Only then register the five native tools. The child MCP backend calls the owning
server through `clientapi`; an embedded or external MCP server returns a typed
capability-unavailable error rather than opening another service graph.

Update the hardcoded Claude allowed-tool list and provider activity labels at
the same time. Agent prompt text should say that questions are for genuine
missing user intent and connection access, not a substitute for catalog/notebook
inspection.

### 4.3 Connection capability

A grant record is process-local and contains only:

```text
notebook id, turn id, connection name, environment,
capabilities: discover | sample_query, expires_at
```

It never persists to state.db or Git. Reset, cancellation, completion, timeout,
or service shutdown removes it. SQL execution uses a dedicated service method
with a purpose-specific secret-store context and result/error redaction.

## 5. Implementation phases

### Phase A — immediate presentation and editor polish

1. Fix the presentation height/overflow chain and add tall dashboard/report E2E
   coverage at desktop and mobile viewport sizes.
2. Fix the agent send action alignment using the shadcn Input Group pattern.
3. Make Markdown spellcheck focus-scoped.
4. Add durable notebook block selection, simplify Monaco/card borders, and
   strengthen the selected outer state.
5. Replace the default chart palette with a higher-contrast theme-aware set and
   add renderer/palette coverage.

### Phase B — shared selectable data grid

1. Extract and test the pure selection/range/clipboard model.
2. Add pointer, keyboard, focus, accessibility, and selected-only detail UI to
   `VirtualDataTable` without regressing virtualization or scroll restoration.
3. Add notebook live E2E coverage for multi-selection, navigation, toggling,
   copy, and tooltip behavior.
4. Reuse the component for structured table visualizations and verify notebook,
   dashboard/report, and asset-inspect consumers.

### Phase C — interaction protocol and Questionnaire

1. Add the shadcn Questionnaire component through the registry CLI.
2. Add typed interaction DTOs, service state machine, answer endpoint, SSE
   replay, cancellation, and race tests.
3. Add the opaque native-turn token and native-only `ask_user` MCP tool.
4. Render/answer questionnaires in the chat and cover reconnect, validation,
   decline, cancellation, and mobile layout.

### Phase D — credential-blind connection access

1. Add connection request/approval UI around `WorkspaceConnectionDialog`.
2. Add ephemeral turn grants and native MCP tools for listing, discovery, and
   bounded sample queries.
3. Reuse SQL discovery and read-only parsing while adding pre-execution limits,
   deadlines, byte/value caps, and redaction.
4. Use the existing semantic `cell.create` source recipe and preserve first
   remote-import review.
5. Cover missing connection, denied access, unsupported discovery, query parse,
   timeout, cancellation, stale token, secret redaction, and successful local
   DuckDB/Postgres paths.

### Phase E — realistic agent evaluation

1. Add fixture workspaces and task manifests for the ten-case corpus.
2. Add an opt-in harness with fake-provider mode for deterministic harness CI
   tests and authenticated-provider mode for local/release evidence.
3. Record objective results and exact diffs in a machine-readable report plus a
   concise Markdown summary.
4. Run the corpus with local Codex, inspect failures, and improve tool schemas,
   prompts, and validation based on observed evidence rather than adding broad
   capabilities.
5. Document the command and evidence format for release operators without
   marketing unverified provider support.

### Phase F — closure

1. Regenerate frontend API types for DTO changes.
2. Update `architecture/notebooks.md` and `architecture/frontend.md` with the
   as-built interaction, grants, data-grid, and scroll ownership boundaries.
3. Run focused Go/React tests, frontend build, production-shaped verify flow,
   and `make release-check` with constrained parallelism on this host.
4. Fold completed work out of this plan; keep only unimplemented evaluation or
   platform evidence in `notebook-platform.md`.

## 6. Acceptance criteria

- A running native agent can ask a structured question, the user can answer it
  in chat, and the same tool call resumes without a second agent turn.
- An agent that needs an unconfigured Postgres source can prompt the user,
  continue with the newly named connection, discover/query bounded data without
  seeing credentials, add a sampled source, and stop at the ordinary import
  review boundary.
- External MCP cannot call native interaction or live-connection tools.
- Reset/cancel/timeout/reconnect cannot leave a blocked interaction or a usable
  grant, and no secret appears in transcripts, SSE, MCP, logs, diffs, or argv.
- The authenticated corpus produces a repeatable objective report and exact Git
  diff; fake-provider coverage remains deterministic in CI.
- Notebook result cells support pointer and keyboard multi-selection and copy;
  full values appear only for selected cells. Asset inspect and table
  visualizations use the same behavior.
- Unfocused Markdown has `spellcheck=false`; focused visual/source editing has
  `spellcheck=true`.
- Notebook cards have one clear selection boundary without a nested Monaco
  frame, and the default palette remains distinguishable in light/dark themes.
- Tall dashboards and reports scroll inside the application at desktop and
  mobile sizes while headers and sidebars remain usable.
- `go test ./...`, frontend test/typecheck/build, relevant live E2E tests, and
  `make release-check` pass without requiring a high-parallelism build on the
  memory-constrained host.

## 7. Non-goals

- giving agents raw credentials, shell/filesystem/Git access, or a generic HTTP
  client;
- persisting connection grants or silently approving a remote source import;
- turning model-output prose into brittle golden tests;
- background/cloud agent turns or shared/persistent chat history;
- migrating every action-oriented table in Renart to the result-grid contract;
- remote notebook scratch warehouses, cross-notebook references, or hosted
  dashboard publication.
