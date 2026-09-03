# Notebook release evidence and deferred extensions

> **Status (2026-08-17): core platform implemented; evidence and explicitly
> deferred extensions remain.** The shipped architecture is documented in
> [`architecture/notebooks.md`](../architecture/notebooks.md). Git history keeps
> the original multi-phase design that this focused closure plan replaces.

## 1. Shipped baseline

The notebook platform now has the release architecture that the original plan
set out to establish:

- Git-native versioned notebooks with durable block IDs, notebook-wide
  revisioned semantic changes, recoverable multi-file writes, and SSE
  reconciliation;
- one local DuckDB integration warehouse, with explicit full/sample warehouse,
  local/object-file, and HTTP source blocks;
- native typed DuckDB query-to-Parquet and shared Sling-to-Parquet transfers;
- the same typed transfer contract for implicit non-DuckDB pipeline-asset
  references, with no JSON/map-row type reconstruction fallback;
- atomic validation/publication of complete or explicitly sampled artifacts and
  durable credential-free snapshot provenance;
- configurable `--notebook-snapshot-max-bytes` and
  `--notebook-snapshot-timeout` budgets shared by all external source roles;
- local SQL and brokered Python transforms, result restoration/export,
  performance observations, and DAG-aware SQL/Python intelligence;
- ordered Markdown, typed controls, and structured checked visualization
  blocks with shared notebook/dashboard/report rendering and inspectors;
- Git-native dashboards and reports with static presentation checking,
  preview/viewer/runtime/deployment integration, and one consolidated authoring
  command bar;
- notebook-scoped native Ask/Edit chat plus external MCP, semantic references,
  bounded catalog search, reviewed change sets, execution policy, cancellation,
  structured questions, turn-scoped credential-blind connection access, and a
  scored fake/authenticated provider evaluation corpus;
- reviewed source/transform promotion into ordinary pipeline assets.

The executor boundary is intentionally role-specific. `NotebookBlockExecutor`
covers connection-bound SQL and Renart-owned source definitions;
`NotebookTransferService` owns their typed staging artifact. Local DuckDB SQL
and Python stay on dedicated session/broker paths because forcing them through a
remote-source interface would hide rather than simplify their lifecycle.

There is deliberately no generic direct-query fallback today. A new adapter may
ship only when it can preserve the source schema and values at least as
faithfully as Parquet, enforce the same cancellation and byte/time limits, and
prove that an error cannot publish a partial relation.

## 2. Required release evidence

These items strengthen confidence; they do not require another notebook data
model.

### 2.1 Transfer fidelity matrix

Keep deterministic local coverage required in CI:

- local DuckDB native full and sampled snapshots;
- ephemeral Postgres through Sling, including two independently named source
  connections joined by one local DuckDB transform;
- a result larger than the browser preview while the published relation remains
  complete;
- byte limit, timeout, cancellation, producer failure, and invalid/partial
  artifact preservation of the previous good snapshot;
- zero-row known schemas, null-only columns, integer widths, decimal, temporal,
  boolean, Unicode, binary, JSON/array/struct behavior, reserved names, and
  duplicate-column handling where the source supports them;
- no credential in argv, logs, HTTP/MCP DTOs, or stored provenance.

Run credentialed Snowflake, BigQuery, Redshift, Databricks, and other adapters
only when CI or a release operator has real accounts. Record the exact adapter,
driver, source types, and result; never infer support for an untested warehouse
from the Postgres path.

### 2.2 Restart, concurrency, and performance

- live restart restores current result/snapshot metadata and bounded previews;
- concurrent UI/MCP edits conflict instead of overwriting;
- cancellation leaves no producer process and no half-published relation;
- calibrate the 2 GiB/30 minute defaults with small, medium, and near-budget
  sources on Linux, macOS, Windows, and NixOS;
- retain separate timings for source execution, transfer, publication, local
  materialization, preview query/render, Python startup, and session size.

The budget flags are the supported operator escape hatch while calibration is
still being collected. Do not silently raise defaults to make one test pass.

### 2.3 Agent client acceptance

Ordinary CI stays credential-free: official MCP protocol tests, service policy
tests, and the fake native provider are the gate. Before a release, run this
fixed corpus with every authenticated client version being claimed:

1. Ask mode explains a referenced failing cell without receiving mutation or
   run tools.
2. Ask mode searches the workspace catalog and compares lineage plus
   materialization policy.
3. Edit mode adds a source and local transform through one reviewed semantic
   change set.
4. Edit mode repairs an invalid query and runs only the required cells.
5. Edit mode adds a checked visualization using the typed definition grammar.
6. A concurrent human edit produces a revision conflict and preserves both
   sides.
7. Cancellation stops the provider/MCP process tree.
8. The resulting authored changes are exact reviewable Git diffs, with no agent
   commit or push.

Record client name/version, provider/model, Ask/Edit task, MCP calls observed,
result, duration, and any retry. Current evidence includes a complete Codex
tool-call/resumption smoke and Claude Code/OpenCode transport health; do not
claim the latter two as model-driven acceptance until an authenticated corpus
run succeeds.

`make notebook-agent-eval` runs the deterministic credential-free slice. For a
local authenticated client, build Renart first and run, for example:

```bash
node scripts/notebook-agent-eval.mjs --provider codex --all --binary ./renart
```

The harness copies its fixture into a temporary Git repository, creates an
ephemeral Postgres only for the remote-source case, and writes JSON/Markdown
evidence under `.tmp/`. It never commits or pushes. On 2026-08-24, Codex CLI
0.149.0 was exercised against Ask, SQL repair, checked visualization, empty
catalog, retained-history choice, Questionnaire, remote Postgres approval,
revision conflict, and scope-boundary tasks. This is local release evidence,
not a promise about every provider/model version.

### 2.4 Accessibility and responsive acceptance

- keyboard-only creation, insertion, selection, reordering, inspector editing,
  execution, and error repair;
- screen-reader names and logical positions for blocks, virtualized rows,
  charts, controls, progress, and findings;
- phone/tablet/desktop checks for the shared authoring rail, notebook chat,
  presentation command bar, inspectors, and audience views;
- charts and controls remain understandable without color alone;
- print pagination for reports is checked against representative narrative and
  visualization combinations.

## 3. Explicitly deferred product extensions

These are not release blockers and need separate focused plans before work:

- remote notebook scratch targets or uploads of local intermediates;
- direct named-warehouse reads from Python;
- persistent Python kernels or Python auto-recompute;
- cross-notebook data references and their freshness/rename contract;
- persistent/shareable agent transcripts, background turns, or provider
  account management;
- selective acceptance inside a dependent semantic change set;
- hosted dashboard/report publication, access control, and scheduled refresh.

The current decisions remain: remote data enters through explicit source
snapshots, Python is process-per-run, semantic changes are atomic, chat history
is bounded local runtime state, and hosted publication is not implied by the
local artifact format.

## 4. Closure condition

Delete this plan after the deterministic matrix, restart/concurrency checks,
platform performance calibration, accessibility pass, and authenticated client
corpus have release records. Any deferred extension that is selected for work
must first get its own plan; it should not keep this release-evidence plan open.
