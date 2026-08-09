# Staleness stack — fingerprints, facts, snapshots, schedules, protection

Status: current state (July 2026). Interlocking subsystems together answer
"what source will run, what is built, what is stale, what runs on a schedule,
and what is protected".

```
saved source ──► fingerprints/render ──► readiness + read-only plan
      │                                      │
      └──► snapshots/deploy ──► schedules ───┤
                                             v
                                  run/RunSpec/unit ledger
                                             │
                                             v
                              execution ──► completion outbox
                                             │
                                             v
                            target-aware facts/coverage ──► staleness/UI

environment policy ──► plan/admission and pre-side-effect checks
```

## 1. Identity and events

- Pipelines self-assign an `id: <uuid>` in `pipeline.yml` on first load
  (`internal/web/identity`). Asset identity is `pipeline_uuid:asset_name` —
  renaming an asset orphans its history (accepted; a content-hash rename
  heuristic can be added later if it hurts).
- One event bus (`internal/web/bus`) emits `RunCompleted`, `AssetSaved`, and
  `TargetWriteChanged`; the fact recorder and staleness service attach here.
  Target-write events invalidate freshness as soon as an exact physical output
  is claimed or becomes uncertain, without waiting for a successful run.
- All durable tables live in the scheduler's SQLite DB (`.renart/state.db`)
  under the `renart_` prefix, migrated by the goose runner (goose's version
  table is the schema-version ledger). WAL + `busy_timeout=5000`.

## 2. Fingerprints (`internal/web/fingerprint`, `renart fp`)

Deterministic, versioned (`v1:` prefix) content identity per asset:

```
SQL:    H(fp_version ‖ canonical_sql ‖ config_hash ‖ consumed_vars_hash ‖ sorted(upstream_fps))
Python: H(fp_version ‖ file_bytes ‖ lockfile_hash ‖ shared_dir_hash ‖ config_hash ‖ upstream_fps)
```

- **SQL canonicalization runs through the embedded wasm formatter**: comments
  stripped, statement formatted per-asset-dialect (`internal/sqlformat`),
  whitespace collapsed — format-on-save, keyword-case edits, and trailing
  commas never change a fingerprint; identifier case stays significant. A
  format call costs ~66 ms, so results are cached by content hash (content
  only ever formats one way; the cache cannot go stale): cold DAG on 20 assets
  ≈ 1.3 s, warm ≈ 0.4 ms. The server pre-warms all pipelines at startup.
  Statements the formatter can't parse (e.g. Jinja in identifier position)
  fall back to the stripped canonical form, deterministically.
- **Consumed variables are detected textually** (`var.NAME` references
  intersected with declared variables), not by instrumenting the Jinja
  renderer. Over-approximates → over-invalidates, the safe direction.
- **Python hashes raw bytes** + nearest lockfile (`uv.lock`,
  `requirements.txt`, `pyproject.toml`) + the shared dir, and assumes all
  variables consumed. Comment-only edits over-invalidate until the deferred
  hardening lands (§8).
- **Escape hatches live under `meta:`** (`meta.fingerprint_version` replaces
  the content hash; `meta.depends_on_files` globs get hashed in) because
  Bruin's asset schema has no free top-level keys.
- **The engine recomputes on every call** — full recompute is O(pipeline)
  cheap hashing and cannot go stale; only disk-derived inputs are cached,
  validated by stat. `Engine.Invalidate` exists as API.
- **Stability guard:** `fingerprint/golden_test.go` fingerprints the committed
  fixture project against `testdata/fixture-golden.json` on every test run.
  Intentional algorithm changes bump `fingerprint.Version` and regenerate with
  `-update-golden`; everything goes stale once — correct and self-healing.

## 3. Materialization facts and coverage (`internal/web/matlog`)

Every run writes an immutable fact row (asset, environment, fingerprint,
vars_hash, optional interval, run_id, timestamp); a compacted coverage table
merges overlapping/adjacent intervals into one row per contiguous range, so
freshness lookups are O(gaps). Assets without a real execution-window contract
hold a single "built" marker row (`interval_start = NULL`). SQL
`time_interval` and API requests that reference Renart's start/end variables
stamp their run window. Replay-safe API merge (with a primary key) and SQL
`time_interval` union independent windows; other windowed API writes replace
their prior coverage with the latest window so replace/append modes cannot
claim data they may no longer contain. Load's Sling max-key state is not a
Renart run window, and dormant `incremental_key` metadata never makes an asset
interval-aware. `BackfillSafe` is the narrower union-safe contract used by the
scheduler before enabling catch-up and returned with each asset's staleness
status. The editor uses that same backend fact for its explicit Backfill range
action; the execution endpoint requires a complete UTC range and revalidates the
asset before dispatch. Startup/daily housekeeping prunes raw facts according
to the tracked project retention policy (default 90 days); coverage is the
durable summary and is never age-pruned.

Immutable facts and coverage carry
a secret-free `target_identity` plus a monotonic `target_generation`, and
`renart_latest_successful_writers` retains the global winning writer for each
non-empty physical target independently of raw-fact retention. `Store.Record`
updates the writer, fact, and current-generation coverage atomically; changing
the writer asset/environment or fingerprint/full-variables pair advances the
generation, so an A -> B -> A sequence cannot reactivate A's old coverage.
Stable completion IDs and ordinals make replay ordering deterministic and older
completions are ignored. A scheduled fact-key replay is accepted only when all
persisted target, source, window, timestamp, and completion evidence agrees and
the stored generation matches the target-aware/legacy class; conflicts fail
before the writer can change. Equal-time
independent completions mark the writer ambiguous and suppress current coverage
until a strictly newer write establishes a new generation. Legacy and
runtime-only target calls remain explicitly at empty target, generation zero,
with no writer row.

Immediately before physical execution, the direct runner captures a versioned,
secret-free snapshot of the full parsed graph: stable pipeline/asset identity,
target identity and fidelity, fingerprints, dependency edges, coverage mode,
variables hash, and refresh restriction. Scheduler-backed runs persist this
snapshot before their first step; interactive asset, scoped, Build-needed,
legacy `/api/run`, and quickstart builds carry the same evidence directly into
their completion envelope. At each main-task start Renart captures the exact
latest-writer read set for its in-pipeline upstream targets, then claims its own
exact output before warehouse work can begin. Declared Python tables are the
evidence-required exception: the intended target is captured up front, but the
operator creates its durable claim only after `materialize()` returned data and
immediately before the Go-side loader writes it. The recorder requires that
claim (or the already-committed matching fact during replay) before granting
coverage. A failed or cancelled claimed write becomes `dirty`; active and dirty
claims make the previous writer unavailable so freshness fails closed.

A full refresh remains paired with its requested run window. For an
interval-aware asset it replaces prior interval coverage with that window; it
does not create a universal built marker for a query that may be window-filtered.
For a non-windowed table it replaces the marker. Asset-level and selected-
environment refresh restrictions run configured strategies and therefore keep
normal union/marker behavior.

Notes: every current user-facing non-dry execution path creates a durable run,
so its facts carry that `run_id`; empty IDs remain only in pre-ledger facts and
low-level compatibility inputs. Every new completion also has a stable
`completion_id`, and multi-window runs derive a distinct ID per execution unit.
A partial unique index keeps one fact per `(asset, environment, non-empty
run_id)` while allowing legacy empty IDs; recorder inserts are no-ops when crash
recovery replays a fact that already committed. New runs record only
their pre-execution captured fingerprint/target evidence, so a source or
configuration edit during a run cannot be mistaken for what executed.
Version-two completion evidence is
self-contained for recovery and includes the captured dependency graph and
upstream writers. Legacy version-one evidence is accepted only where current
source still matches and every in-pipeline upstream needed by a successful
asset also succeeded; otherwise it fails closed. Pipeline execution collects
terminal asset events even when the overall run fails: completed assets record
success facts, the failing asset records a failed attempt, and assets the
executor never reached record nothing.

**Last run attempt and quality.** Facts only capture successes, so the recorder
also upserts `renart_asset_runs` — one row per `(asset, environment)` with the
target fingerprint, `succeeded|failed|cancelled`, timestamp, and the optional
runtime quality outcome. Main-task and quality status are orthogonal: a
successful write records coverage and can remain `fresh` while a later custom
or column check is `failed`. Only stable failed-check identities are stored;
check SQL and runtime errors remain in the run log. A later run overwrites the
row, so its main and quality outcomes replace the prior attempt together. The
upsert is monotonic by timestamp: an older or equal-time recovery event cannot
replace a newer attempt.
Interactive, stale-plan, and full
pipeline materialization all emit `RunCompleted` for the assets they actually
attempted, including terminal failures.

Bruin JSON run logs under `logs/runs/` are diagnostic artifacts, not application
state. Freshness, materialization timestamps, and latest attempts are restored
from `.renart/state.db`; transient running state comes from scheduler steps and
SSE. In particular, a terminal run-log snapshot may still call an untouched
asset `pending`, and Renart never persists that value as asset state.

After an unclean server stop, scheduler startup first marks the orphaned run,
every open step, and every running execution unit failed; queued units become
skipped. It then re-emits only attempted terminal work through the same
synchronous `RunCompleted` bus. Unit-backed runs retain each unit's exact
window, position-derived completion ID, and completion ordinal, while legacy
runs without a unit ledger retain aggregate step replay. Prior successes remain
successes, the interrupted unit is failed, and unreached assets remain absent.
Pending recoveries first close any unit rows left open by older Renart builds,
so an upgrade can repair the ledger before replaying derived state.
The run row stores the requested execution modes at admission, then atomically
replaces them with the effective environment, window, full-refresh/backfill
mode, and sensor mode immediately before the first asset starts. If that write
fails, execution does not begin. Recovery therefore applies the same coverage
replacement semantics as the interrupted executor, including environment-level
full-refresh restrictions and default windows. Rows interrupted by a legacy
build before that effective-context write existed remain explicitly unresolved:
their River arguments are request diagnostics only, so startup acknowledges and
counts the skipped replay without emitting materialization facts. Those assets
remain stale rather than risking false coverage from an inferred environment,
window, or refresh mode.
New scheduler-backed admissions also persist a private versioned RunSpec. For a
spec-backed run, recovery never overwrites requested modes from empty or
conflicting River arguments; the spec remains authoritative while fact replay
continues to use only the persisted effective execution context. New manual
run/spec/job/link admission is atomic. River-argument link and mode recovery is
retained only for pre-upgrade jobs, and an unknown or structurally incompatible
spec fails closed rather than falling back to legacy semantics. The spec's
stable pipeline UUID is independently bound to durable admission state before
execution: legacy/conservative runs use path plus UUID slot aliases, while
resource-isolated reviewed runs bind the path and UUID in their claim set. The
UUID travels through scheduler execution into snapshot resolution, so a
pipeline path rename cannot redirect a queued snapshot through a newly resolved
identity.
Synchronous full-pipeline materialization now uses the same ledger with an
`inline_streaming` dispatch. Its effective window, environment, modes, source,
and server-authenticated API/CLI origin are admitted before Bruin starts;
targets and steps are persisted synchronously, and terminalization releases the
same pipeline-global slot. A crash marks the jobless inline run failed and
replays only its retained terminal facts. One-asset and scoped materialization
use RunSpec v2 to retain the exact ordered asset paths, common window, scope,
anchor, and inclusion reasons; the same transaction creates queued execution
units, and the inline executor transitions each unit around its physical call.
Build-needed uses the same v2 selection after recomputing freshness server-side:
each topologically ordered asset/gap window is a durable unit with its staleness
reason and run-derived completion identity. Earlier successful windows remain
successful if a later window fails; remaining windows and downstream assets are
durably skipped. Asset-level steps stay aggregated for compatibility while the
unit ledger remains the exact execution record.
An unreviewed queued run can likewise discover parallel full-pipeline work only
after parsing its working tree or pinned deployment. It already owns the
conservative pipeline slot; after persisting the effective context, the worker
atomically inserts the runtime-derived units before target capture or the first
unit transition. The private RunSpec remains the original all-assets request,
while the unit ledger records the exact work that actually became executable.
An exact re-execution is a new manual run admitted from a terminal run's
retained private RunSpec and immutable plan. Before admission Renart revalidates
current policy, the original source Merkle, and the selected secret-free
configuration digest; source or configuration drift changes the available UI
action to an explicitly non-exact current-settings run. Exact replay retains
the original source, execution time, context, selection, and units, but receives
new run-derived completion identities and never inherits schedule occurrence or
watermark authority. Its successful facts therefore pass through the normal
latest-writer and coverage rules rather than copying facts from the old run.
For a deployed run it materializes the run's exact pinned snapshot while the
recorder fingerprints it, then deletes the temp directory. This is derived-state
recovery only—asset code and textual logs are never replayed. The fact and
latest-attempt writes above make the path safe if the original completion event
committed immediately before the process died. A durable pending flag is cleared
only after replay returns, so another stop during startup retries safely; its
migration also queues interrupted runs reconciled by older builds for one-time
backfill.

Completion delivery uses a durable SQLite outbox. Physical execution enqueues a
self-contained completion before reporting success; recorder or staleness
subscriber failures leave the envelope pending for startup/housekeeping replay
without relabelling the already-finished warehouse operation as failed. Delivery
and acknowledgement are idempotent, including concurrent replay attempts.

Target-claim recovery is fenced separately from River ownership. Every non-dry
execution holds a shared per-workspace OS lease from before target capture
through durable completion hand-off. Startup takes the corresponding exclusive
lease before converting orphaned active claims to dirty; embedded/headless
invocations skip reconciliation while a live executor owns a shared lease. A
primary lock outside the worktree survives `git clean`, while
`.renart/execution.lock` keeps processes with different runtime-cache settings
in the same lock domain. This applies to pipeline, asset/scope, Build-needed,
legacy workspace, and onboarding quickstart materialization paths.

The workspace scheduler lock is acquired before any River worker starts, so a
River job still marked `running` at that point belongs to the stopped process.
Recovery cancels admitted pipeline and housekeeping jobs in the same SQLite
transaction that closes Renart's run records. A queued run remains queued only
when it still has an available, pending, retryable, or scheduled River job;
terminal-linked and truly jobless queued rows fail and release their active
slot. Pre-upgrade runnable jobs are relinked from their run ID. A claimed
scheduled compatibility signal with no admitted run is instead returned to
River with its exact arguments and interval intact. Startup writes a structured
summary with reconciled-run, cancelled-job, requeued-signal, replay, and
replay-failure counts; cancelled queue rows retain the interruption as an
attempt error. Otherwise-live Renart run and schedule-signal jobs whose retry
timestamp was written in a pre-canonical Go or RFC3339 encoding are requeued at
the current time in River's exact sortable SQLite format. Their arguments and
attempt count are preserved, so an old snoozed occurrence cannot remain
permanently `available` but ineligible for River's lexical due-time comparison.

Before the unique run-slot migration is applied, a legacy database can contain
multiple queued/running rows for one pipeline path because old admission was
not atomic. Migration keeps one deterministic queued-first survivor, marks the
other rows failed, closes their open steps, and writes the recovery reason to
their logs. The survivor then enters normal startup recovery and receives the
legacy path-only slot; old rows did not retain the stable UUID needed to
reconstruct a UUID alias.

## 4. Staleness service and UI (`internal/web/staleness`)

In-memory status map per current selection (env, range, vars), exposed at
`/api/pipelines/{id}/staleness` and pushed over SSE. The frontend tracks loading
and failures per pipeline for the exact selection. A matching SSE snapshot is
authoritative for that pipeline: it resolves that request/error and prevents an
older in-flight HTTP response from replacing the pushed state, without hiding
unresolved sibling pipelines. Each response/SSE snapshot includes a
`data_state_token` over the selection-relevant physical generations, coverage,
claims, and ambiguity. Each asset also exposes its selected target
fidelity/identity and the current `latest_output` writer metadata when that
target is trustworthy. Equivalent reruns in the same generation do not churn
the token; generation changes, coverage expansion, ambiguity, and active/dirty
claims do. Quality is delivered in the same snapshot but deliberately does not
enter `data_state_token`, because assertions do not change which physical data
exists. The quality fields include the originating run/time and whether its
fingerprint still matches the current asset, so an old failed assertion is not
presented as a failure of newly edited SQL. Recompute triggers: selection change
(batched coverage query),
`AssetSaved` (invalidate + recompute the downstream cone), `RunCompleted` (flip
the touched assets), and `TargetWriteChanged` (publish the fail-closed claim
state).

The workspace EventSource has no replay/`Last-Event-ID` contract. On every SSE
reconnect after the initial open, the browser therefore reloads the canonical
workspace snapshot and increments a reconnect sequence consumed by the
staleness hook. Freshness then refetches its canonical HTTP snapshot even when
the disconnect was shorter than the offline-overlay grace period, so a missed
`staleness.updated` event cannot leave canvas badges stale until a page reload.

| Status             | Meaning                                                                                                      |
| ------------------ | ------------------------------------------------------------------------------------------------------------ |
| `fresh`            | coverage exists for current fp + vars + range                                                                |
| `stale_edited`     | own definition changed since last build (own-content sub-hash mismatch)                                      |
| `stale_deployment` | latest output was written by a deployed snapshot whose own definition differs from the saved working tree    |
| `stale_upstream`   | inherited via the Merkle cascade — also covers variable-value changes (own-content matches, full fp doesn't) |
| `partial`          | incremental: some intervals covered (built/total surfaced as covered/total seconds)                          |
| `never_built`      | no row for this asset in this env at any fingerprint                                                         |
| `missing`          | materialization history says fresh, async verification couldn't find the table                               |
| `volatile`         | sensor check has no durable output coverage and must run again in every stale plan                           |
| `external`         | source asset describes externally maintained data; Renart does not assign it a build or freshness state      |

The `missing` downgrade only applies to assets whose output is a warehouse
object named after the asset (`verifiableByName`: SQL, seed, and database-backed
Load). Local-, file-, and object-storage-backed Load assets use an explicit
`destination_object` and rest on their exact target-aware run facts instead.
Verification distinguishes a confirmed absent relation from an unavailable
connection. A locked credential vault, missing environment secret, or
temporarily unreachable warehouse leaves durable fact-based freshness intact;
it cannot be promoted into evidence that the relation is missing. A successful
materialization clears any previously remembered missing observation for that
asset and makes the newly written output eligible for one new verification.
An omitted HTTP environment selection resolves to the workspace-selected
environment, so the default selection uses the same namespace as execution
records.
Non-materialized Python assets may write anywhere and remain
`runtime_only`/`never_built` while their latest attempt is still recorded and
displayed. Declared Python tables use their supported destination relation as
an exact target. They become fresh only when Renart's operator confirms that a
returned result was loaded; a successful `None` return remains a successful
attempt without creating coverage.

Sensors are deliberately classified as `volatile` before and after a successful
check. Their last attempt is still recorded and displayed, but they are excluded
from warehouse-object verification and never become fresh from a run fact. The
Build-needed planner therefore includes them on every requested build; interactive
execution performs one check, while scheduled execution waits according to the
sensor's configured interval and timeout.

Imported `*.source` assets are classified as `external`, excluded from Needed
selection, and never show a last-build state. For a consumer fingerprint, the
current source declaration is immediately achievable without a materialization
fact: a successful transformation can therefore become fresh, while an edit to
the source declaration still cascades to its downstreams. Renart does not infer
the external table's data freshness from its existence or modification time.

Unsaved editor buffers get a purely-frontend "modified" dot; the service only
sees saved state.

Materialization facts and the durable latest-writer row retain the deployed
snapshot version that produced them. When an older pinned schedule overwrites
the same physical target after an interactive working-tree build, Renart keeps
the output non-fresh but reports **Deployment differs** rather than implying
that the user edited the asset. This provenance is stored with the writer
instead of being joined from retained run history, so pruning old runs cannot
erase the explanation.

Each `AssetStatus` also carries the last run attempt (`last_run_status`,
`last_run_at`, `last_run_on_current_content` — the latter true when the run's
fingerprint matches the asset's current one) from `renart_asset_runs`,
orthogonal to the base `status`. The frontend renders both dimensions instead
of allowing one to replace the other: for example, unchanged built content can
show **Fresh** + **Last run failed**, while edited or never-built content whose
current version failed shows its base **Edited**/**Never built** badge + **Build
failed**. A cancelled attempt is likewise separate. This preserves the answer
to "can I use the existing data?" while also answering "what happened when I
last tried to build it?" and distinguishes an untested edit from one that was
run and failed.

Running state is transient and asset-scoped. The UI derives materialization
state from scheduler steps (initial active-run hydration plus `run.step` SSE
events); a queued or started pipeline does not mark every asset pending.
Reviewed-plan progress has a separate durable unit ledger and `run.unit` SSE
event for the run-details Plan tab. `run.finished` clears all transient entries
for that run, while the canonical terminal attempt arrives through staleness.
Assets skipped after an upstream failure therefore retain their previous
freshness and attempt state. Build remembers a terminal event even when a very
fast run finishes before the trigger response supplies its run ID, then
associates the result and reloads the canonical stored log. Late queued/running
events or active-run hydration cannot resurrect that finished run.

The read-only pipeline planner consumes the same snapshot and
`data_state_token`. `needed` selection includes every non-fresh/volatile asset
in topological order and uses exact uncovered intervals for partial assets;
asset-closure selection applies the same state to a selected asset and its
requested dependency direction. Custom selector selection delegates expression
resolution to Bruin and either includes every match or intersects those matches
with the same Needed state. The normalized expression and final explicit units
are retained together so later execution never reinterprets a reviewed selector.
Each planned asset records why it was included,
and each final asset/window becomes an explicit execution unit in the response.
The token is part of the plan identity, so confirmation regenerates against the
latest generation, coverage, claims, and ambiguity rather than trusting a UI
snapshot. `all`, Needed, and asset-closure plans persist and execute their final
ordered units. If a Needed unit became fresh between review and confirmation it
may be omitted and is retained as preview-delta evidence; new work, widened
windows, or changed windows return `plan_data_changed` instead of expanding the
run silently. Each admitted unit carries its inclusion reason and exact window,
and terminal unit state is durable even when later work is skipped.

The quick **Build needed** action is server-side: `POST
/api/pipelines/{id}/build-stale/stream` (`httpapi/build_stale.go`) recomputes
the stale set for the selection, compiles it into a plan (every non-fresh
asset; for partials exactly the uncovered gap intervals), and
`ExecutionService.MaterializeStaleAssetsStream` builds it in topological order
as one SSE-streamed, version-three execution-unit graph — one combined log,
per-asset `asset` progress events, and one `RunCompleted` bus emit per built
window. Independent safe branches may overlap up to the pipeline, workspace,
connection, and resource limits. Multiple gaps for one asset remain chained,
and selected downstreams become runnable only after their upstream completion
and freshness hand-off. Assets downstream of a failed plan member are skipped;
independent branches continue.
The whole physical plan holds the workspace execution lease, and every window
uses the same target-capture, write-claim, and durable completion path as direct
asset materialization.
This inline shortcut and a reviewed pipeline plan with `selection=needed`
consume the same staleness/gap semantics and both use the universal run/unit
ledger. Only the latter persists a reviewed pipeline-plan artifact and exposes
the full checks/render confirmation surface before admission.
The endpoint's optional `upstream_of` selector narrows that same plan to one
asset's transitive upstream closure. `renart run <asset> --refresh-upstreams`
uses this selector in delegated mode (and the same planner directly in embedded
mode), then starts the requested asset only if the upstream plan succeeds.
The CLI also exposes `--selector` and `--selector-needed`; both preview the
server-resolved matches before confirmation and persist their final units.

## 5. Snapshots and deploy (`internal/web/snapshot`, `renart deploy`)

Content-addressed store in SQLite: `renart_blobs` (hash → file bytes) +
`renart_snapshots` (version, pipeline, per-pipeline ordinal, merkle root,
manifest JSON, git SHA/dirty). Existing ordinals are backfilled oldest-first
with a deterministic version-ID tie-breaker; identical no-op deploys retain
their UUID and ordinal. Snapshots hold **source files, not rendered SQL** — rendering
depends on per-run env/vars/interval, so the executor renders at run time from
snapshot content exactly as from the working tree. Every selected snapshot is
an exact version ID owned by the target pipeline. Admission and execution
validate canonical manifest paths, blob presence, and content hashes; a
missing, wrong-pipeline, or corrupt deployment fails closed. Snapshot runs
materialize into a fresh temp directory **outside the workspace** (so pipeline
discovery doesn't pick it up) with a `ConfigPath` override on the executor.
Ordinary Build runs explicitly stay on the saved working tree even after a
deployment exists, while a `deployed_only` environment resolves the latest
deployment to an exact ID before enqueue. Deploy dedupes on identical merkle
root. A reviewed web deployment also submits the source Merkle it displayed;
if saved files changed before the snapshot write, the server returns a typed
conflict and creates nothing. Drift between working tree and the latest
deployed version is surfaced per pipeline (`/api/pipelines/{id}/deploy/status`).
Its file lists can be opened through a pipeline/version-owned comparison
endpoint that returns exact deployed and saved text up to 2 MiB per side while
withholding binary/oversized contents. The immutable snapshot-file endpoint
remains `/api/snapshots/{versionId}/file`; status also reports whether the latest
snapshot is executable so identical-but-corrupt content can be repaired by a
new Deploy instead of dead-ending the UI.

Snapshot housekeeping removes a version only after it is older than the
configured window (default 90 days), outside the newest-per-pipeline floor
(default 20), and is neither that pipeline's latest version nor referenced by
a schedule, retained run, or pending completion envelope. Blob deletion runs
in the same transaction and removes only hashes absent from every remaining
manifest. This makes a zero snapshot floor safe while keeping current and
in-flight execution reproducible.

## 6. Per-environment schedules (`renart_schedules`, `/api/env-schedules`)

Schedule identity is `(pipeline UUID, environment)` — no implicit default env.
Desired state for new and edited schedules is the version-controlled
`.renart/schedules.yml`, keyed by that stable identity. It contains cron,
timezone, catch-up policy, pause state, ordinary variable overrides, and
`env:NAME` secret references. It deliberately contains no deployment version,
watermark, run history, or next-run timestamp. Existing SQLite-only schedules
remain visible as local legacy rows rather than being copied into Git without
an explicit user edit.

The corresponding SQLite row carries the machine's pinned snapshot version,
runtime copy of the declaration, a catch-up policy
(`skip | run_once | backfill`), and a status
(`active | paused | archived | delegated` — `delegated` is reserved for cloud).
The declaration reconciler preserves an existing local pin across file edits.
A declaration with no local pin is paused and shown as needing deployment; a
pin is never inferred from Git. Removing a declaration creates an `archived`
tombstone (`declaration_missing`) while retaining its pin and history, and
re-adding the same stable key restores it while the tombstone remains inside
the configured schedule-history window. Pipeline deletion/branch switching
uses the separate `missing` tombstone and also restores when the UUID
reappears. Explicit legacy-row deletion (`user`) does not auto-restore.
River `ByArgs` uniqueness suppresses a duplicate `(pipeline UUID, environment,
interval)` signal while the first job is active. The authoritative identity is
also retained in `schedule_occurrences`: a SHA-256 key binds the stable schedule
identity to its normalized half-open interval, and a durable unique constraint
prevents a second active or already-successful execution after River forgets a
terminal job. Failed/cancelled intervals can be retried under the same
occurrence with numbered run attempts. Occurrence/attempt claim, run, RunSpec,
retained plan/units, resource-aware admission claims, and a run-ID-only River
execution job/link commit in one SQLite transaction. Conservative plans retain
the pipeline-global path plus stable-UUID slot aliases. The v2
periodic/catch-up job is only a due signal containing the captured schedule
revision and interval; physical execution reconstructs behavior exclusively
from the stored RunSpec. A slot conflict rolls the attempted admission
back, leaves the occurrence pending, and snoozes the original signal with its
exact arguments. The schedules API exposes only the pending interval and prior
attempt count, and the UI labels it **Run waiting** or **Retry waiting**; values
and the private key are not exposed. SSE occurrence events refresh this state
without polling. Migrated active rows have only a path alias because their UUID
was not persisted, so rename safety cannot be reconstructed for those rows.
Pre-v2 combined River jobs remain decodable until already-persisted work drains;
startup returns both legacy and v2 claimed signals to River unchanged.

Exact resource claims cover local files, DuckDB database files, and warehouse
relations. These kinds are part of both the reviewed-plan contract and the
SQLite admission constraint; adding a new kind requires migrating both
together so a valid plan cannot be rejected while admitting a scheduled run.

Row edits use explicit server-side preservation flags. The browser can update
cadence, timezone, catch-up, pause state, or replace overrides without
round-tripping the local deployment pin or private values. Preserving the pin
reads the current SQLite row at mutation time, so a stale browser cannot undo a
concurrent promotion. Preserving overrides reads the private literal values and
secret references on the server; it cannot be combined with replacements and
cannot create a new schedule. A paused declaration that has not been deployed
yet can still be edited while retaining its empty pin.

Only the process holding `.renart/scheduler.lock` may change rows or enqueue
runs. `GET /api/env-schedules` reports `owner`, `follower`, or `unavailable`;
mutations through a follower return `409 scheduler_not_owner` before any
deployment, `pipeline.yml`, or schedule-store write. Followers remain
read-only. In the supported topology the canonical workspace-server lease
rejects a second long-lived runtime before it opens `state.db`, so the current
process is expected to be the scheduler owner. Follower mode remains a
fail-closed compatibility safeguard, not a hot standby; automatic takeover and
cross-process handoff are intentionally not implemented.

The schedules UI compares each row's pinned snapshot with the pipeline's latest
deployed version. A differing pin is shown as **Older deployment**, independently
of data freshness and last-run status. Repair/update opens the saved-source
deployment review; after deployment the user explicitly selects zero or more
schedules not yet using it. The server validates the target deployment and
compare-and-swaps all selected rows in one transaction, so a concurrently
changed pin rejects the whole batch. A paused declaration that has never been
deployed participates with an explicit empty expected pin; omitting the expected
pin is still an invalid request. The row-level manual action is a server-owned
endpoint that loads the displayed exact pin and stored overrides; it remains a
manual run, so it cannot advance the schedule watermark. Rows without a pin
show **Needs deployment** instead of silently running the working tree.
For an actual scheduled tick, the successful run status and its environment-
scoped watermark advance commit in one SQLite transaction. A crash or write
failure therefore leaves the interval retryable instead of recording success
while silently re-enqueueing the same catch-up window later. Watermark
capability and identity come from the server-derived stored RunSpec, never a
client trigger or the mere presence of a run ID.
The same transaction updates the occurrence to `success`; every other terminal
run path updates it to `failed` or `cancelled`, so startup recovery, blocked
plans, and panic handling cannot leave the occurrence falsely active.

Schedule overrides are validated against the variable declarations in the
exact pinned deployment on create/update, resume, promotion, and reconciliation.
They are applied before assets are constructed in planning, rendering, and
execution, so rendered SQL, target/fingerprint evidence, and recorded variable
hashes share one effective value set. Each actual tick retains an immutable,
stage-content-free plan for its real interval and admission timestamp. Literal
values are private RunSpec inputs. Secret-backed values are resolved from the
server process only for validation, planning, and execution: declarations,
River signals, retained RunSpecs, schedule responses, and SSE carry only the
`env:NAME` reference or sorted variable name, never the resolved value. A
worker re-resolves references and requires them to reproduce the retained plan
identity before physical execution; rotation between admission and execution
therefore fails closed instead of silently running a different configuration.
Exact re-execution applies the same check. A blocked scheduled plan is
persisted as a failed auditable run and cannot become executable after worker
recovery.

Existing database rows from the former pinless contract are migrated once:
each non-archived row is pinned to that pipeline's then-latest deployment, or
paused when no deployment exists. Legacy `pipeline.yml` schedules follow the
same rule when first imported. Active rows with an invalid pin or overrides
that no longer validate against that pin are paused during reconciliation; new
and resumed rows fail closed until both source and variables are executable.

## 7. Protected environments (`internal/web/policy`)

Per-environment flags in `.renart/environments.yml` (kept out of `.bruin.yml`
so Bruin's own config parsing is never at risk):

```yaml
environments:
  prod:
    protected: true # no interactive build-mode execution
    deployed_only: true # only snapshot versions may execute
    confirm_destructive: true # full refresh / backfill / drop need typed confirm
```

Enforced by `policy.Check` at execution dispatch; the legacy `/api/run` path and
manual scheduler trigger apply the same check instead of bypassing it. Scheduled
snapshot runs pass as non-interactive. UI-side disabling is a hint, not
enforcement. Full refresh has a destructive confirmation dialog and sends the
typed environment through to the server; plan confirmation rejects a missing or
mismatched value before queue admission, and execution checks it again before
side effects. The CLI uses `--confirm-environment`. Explicit backfill requests
use the same contract, while an ordinary selected execution window is not
automatically mislabeled as destructive. Locally these are guardrails, not a
boundary (the user owns the credentials); the cloud permission model enforces
the same flags harder, under the same names.

## 8. Local retention and garbage collection

`.renart/project.yml` may declare `retention`; an absent section uses these
defaults:

- run metadata: 180 days and at least 100 runs per stable pipeline identity;
- full run logs: 30 days and at least 25 logged runs per pipeline;
- raw materialization facts: 90 days;
- completed schedule occurrences and archived tombstones: 180 days;
- unreferenced deployments: 90 days and at least 20 per pipeline;
- abandoned Renart temporary directories: 24 hours.

River runs housekeeping on scheduler startup and daily. Logs and run metadata
expire independently. Terminal runs are eligible only after their age and
count floor; queued/running work, recovery-pending rows, live River jobs, and
runs named by the completion outbox are excluded structurally. Removing run
metadata cascades its private RunSpec, reviewed plan, units, steps, logs, and
schedule-attempt link, and removes a matching latest-attempt UI projection.
Coverage and latest-writer rows remain because they are correctness evidence,
not history cache.

Completed occurrences and archived schedule tombstones use the independent
schedule-history cutoff. Removing an old tombstone releases its old pin before
snapshot GC, while active, paused, and recent archived pins remain protected.
Temporary cleanup examines only known `renart-*` directory prefixes directly
below the OS temp root, rejects symlinks and other users' entries, and removes
only directories older than both the configured cutoff and the current server
process. That last bound favors safety for unusually long-running work; a later
Renart process collects any abandoned directory.

## 9. Write-resource admission

Version-three reviewed run plans bind aggregate cross-run mutation claims and a
typed, secret-free execution contract per selected asset alongside the source,
configuration, context, selection, and execution units. The per-asset contract
separates mutation claims from runtime coordination and supplies the in-run
graph scheduler without reconstructing behavior from presentation JSON. The
same target resolver used by rendering and execution currently recognizes
these concurrency-safe contracts:

- a sensor without arbitrary hooks has no write claim;
- an exact local output claims its canonical file;
- an audited native local file-backed DuckDB SQL materialization claims its
  exact resolved relation; those assets share one in-process database session
  while using separate connections, and the session retains the whole-file
  lease against child-process/dynamic writers;
- unaudited DuckDB operations claim the whole canonical database file because
  their operator, hooks, routing, or separate runtime may touch several
  relations or open an independent database handle;
- audited native PostgreSQL, Trino, ClickHouse, and StarRocks SQL
  materializations claim the exact resolved warehouse relation;
- arbitrary Python, any pre/post hooks, seed/Load/API transfers, unaudited
  warehouse operators, dynamic or credential-derived routing, remote objects,
  and unsupported families retain logical-pipeline isolation. Known exact
  claims are still retained on a conservative plan so another pipeline cannot
  write those same resources.

Exact claim keys are opaque versioned digests; plans and SQLite never expose a
database or filesystem path. Atomic admission prevents two active runs from
owning the same claim even when they belong to different logical pipelines.
Distinct proven files, databases, or audited warehouse relations in one
pipeline may run concurrently, and a proven no-write plan does not block a
writer. The same relation reached through two connection aliases hashes to the
same claim without storing aliases, endpoints, principals, or credentials.
Unreviewed and legacy plans keep the path/UUID slot, and that slot conflicts
with active exact claims in the same pipeline. This preserves the old
fail-closed behavior wherever a stable operator contract is unavailable.

Immediately before the first step, execution-target snapshot v4 recomputes the
resource and connection contracts from the effective operator/configuration
and compares them with both the immutable reviewed plan and the acquired SQLite
claims. Drift fails before physical work. The snapshot still contains the full
parsed graph so upstream-writer and fingerprint evidence remains
self-contained, but this comparison is scoped to the plan's immutable
execution units; unselected graph members are evidence, not writes owned by the
reviewed run. Each selected snapshot entry must retain the planned stable asset
identity. Version-four contracts travel through the durable completion outbox
and are revalidated by its shared bus-level validator before materialization
facts are recorded, so crash replay cannot silently downgrade the captured
contract. Terminalizing a run releases its slot/claim set through the same
database transaction and trigger path. Planning's active-run warning uses the
same conflict rules for guidance; admission remains authoritative against races
after review.

## 10. Deferred and known-accepted

- The single-workspace authority is enforced for long-lived runtimes, but CLI
  handoff is not universal: `deploy`, `type-check`, and some debug paths still
  run locally, stateful embedded commands skip the long-lived lease, and a
  second server reports its owner rather than opening the existing URL. These
  are delegation cleanup items, not a multi-process scheduler roadmap.
- Scheduler followers are intentionally read-only defense. Heartbeat/fencing,
  automatic owner takeover, and cross-process schedule-mutation delivery are
  not implemented.
- Pre-v2 River signal/run decoders and duplicated compatibility fields remain
  until jobs persisted by older binaries have drained.
- Additional network relation families remain deliberately disabled until each
  direct and fallback operator proves it has no hidden shared staging state.
  Configurable warning gates, connection-backed warehouse
  validation, automatic builds, and reliable breaking/non-
  breaking impact categorization are not built.
- **Python fingerprint hardening** (Rust→wasm `renart-pyfp` on
  `ruff_python_parser`: comment/docstring-stripped hash, one-level import
  resolution, runtime-observed variables; the wasm binary's own hash feeds
  `fp_version`) — deferred until raw-byte over-invalidation itches.
- Coverage rows for abandoned fingerprints accumulate slowly; harmless, GC
  later (they enable cross-version reuse).
- Set up for later, deliberately not built: cross-environment physical reuse à
  la SQLMesh, breaking vs non-breaking change analysis via column lineage,
  notebook-cell caching on the same engine, cloud schedules (`delegated`).

## Key files

`internal/web/identity`, `internal/web/bus`, `internal/web/fingerprint`
(+ `golden_test.go`), `internal/web/matlog/{recorder,store}.go`,
`internal/web/staleness`, `internal/web/snapshot`, `internal/web/policy`,
`internal/web/scheduler`, migrations under the scheduler store.
