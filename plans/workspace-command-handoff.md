# Workspace command handoff and compatibility cleanup

Status: focused follow-up — the single-workspace authority is shipped; launcher
handoff, remaining stateful CLI delegation, and eventual legacy-job retirement
are open.

The reviewed plan/render/run/deploy/schedule design that led here is now part of
[`architecture/backend.md`](../architecture/backend.md),
[`architecture/frontend.md`](../architecture/frontend.md), and
[`architecture/staleness.md`](../architecture/staleness.md). Git history keeps
the original proposal and implementation checkpoints. This plan contains only
work that has not shipped.

## 1. Fixed decision

One canonical workspace has one long-lived Renart authority. That process owns
its `state.db`, file watcher, SSE hub, River client, scheduler registrations,
and in-process warehouse sessions. One UI process may host several isolated
project runtimes, and separate processes may own different workspaces.

Renart will not add leader election, follower mutation, lock takeover, or
cross-process SSE for one local Git checkout. Short-lived commands either use
the live authority or hold exclusive workspace authority for their operation.

## 2. Shipped baseline

- A canonical, symlink-resolved workspace lease is acquired before a server
  opens state. The authoritative lease lives outside the worktree; the
  in-worktree lock remains as rolling-version compatibility.
- A served workspace publishes an authenticated discovery record. Discovery
  verifies the canonical workspace identity and health before delegation.
- `run`, `plan`, and `render` delegate to the live server by default. `ls`
  prefers its parsed workspace state.
- Physical execution also uses a per-workspace execution coordinator, so
  recovery cannot race live work.
- A second server fails before opening `state.db` and reports the owner PID/URL
  when discovery can prove them.

## 3. Remaining gaps

Current code still exposes four bounded gaps:

1. A second `web` or `standalone` launch returns an error. It does not open or
   point the desktop helper at the proven existing URL and exit successfully.
2. `deploy` resolves identity and opens `.renart/state.db` directly.
   `type-check` builds with mutation enabled and computes workspace state in the
   command process. They do not share the live server's workspace view.
3. `run`, `plan`, and `render` accept `--local`; `run` warns but can still
   perform physical work beside the live owner.
4. Headless embedded service graphs intentionally skip the long-lived server
   lease. Stateful commands therefore need a short-lived authority guard when
   no server is available, or a server can start while they are opening state.

The command inventory must classify every command before changing behavior:

| Class | Examples | Required ownership behavior |
| --- | --- | --- |
| Launcher | `web`, `standalone` | Own the workspace or hand off to a proven live owner |
| Stateful/mutating | `run`, `deploy`, secret writes | Delegate; otherwise hold short-lived exclusive authority |
| State-backed read | snapshot `plan`/`render`, run history/debug state | Prefer delegation; local fallback holds authority if it opens state |
| Filesystem-only read | truly static diagnostics | May remain local if it cannot write inferred identity/metadata or open state |

Do not infer the class from command names. Audit the invoked services for
implicit ID assignment, Git-exclude changes, cache/state writes, and pipeline
builder mutation.

## 4. Implementation slices

### 4.1 Shared owner handoff result

Expose the discovered owner details from the typed already-served error instead
of parsing its message. Build one helper that distinguishes:

- no owner;
- proven live owner for the same canonical workspace;
- stale/unverifiable discovery plus a held lease;
- another failure.

Never open an arbitrary URL from a stale discovery file.

### 4.2 Launcher UX

- A second `web` launch opens the proven existing URL unless `--no-open` is
  set, prints the URL either way, and exits successfully.
- A second `standalone` launch points the configured GUI helper at that URL. If
  the helper is unavailable, use the existing browser fallback.
- A held lease without a verified live endpoint remains an actionable error;
  do not guess an address or steal the lock.

### 4.3 Complete stateful command delegation

Inventory the CLI and add server adapters one command group at a time. Start
with `deploy` and `type-check`, whose HTTP/service paths already exist for the
UI. Keep formatting and exit-code policy in the CLI; share server DTOs rather
than duplicating domain behavior.

Secret commands require a separate audit because some operations already
address the live vault while others intentionally operate on local provider
configuration. Preserve the rule that secret values never enter discovery,
argv, response logs, or ordinary API DTOs.

### 4.4 Short-lived embedded authority

Wrap stateful local fallback in one `withWorkspaceAuthority`-style boundary:

1. canonicalize the workspace;
2. rediscover a live owner immediately before locking;
3. acquire the same authoritative workspace lease;
4. open state/services only after the lease succeeds;
5. release all services before releasing the lease.

This boundary is not the physical execution coordinator; commands that execute
also retain the narrower execution lease.

### 4.5 Close unsafe bypasses

When a live owner is proven, reject `--local` for physical execution or any
command that writes project/state metadata. Read-only local escape hatches may
remain only where their implementation is proven not to open mutable state or
write inferred files. Update help text to describe the actual contract rather
than possible lock conflicts.

## 5. Rolling scheduler compatibility

New execution jobs carry only a durable run ID, but
`scheduler.pipelineRunJobArgs` and `legacyRunSpec` still decode pre-RunSpec
River rows. Snapshot lookup also retains a path-to-UUID fallback for runs
admitted before the stable UUID was persisted.

Do not remove this compatibility based on source age alone. First define the
oldest supported direct upgrade and add a read-only migration audit that counts
queued/retryable legacy jobs and runs missing the durable fields. Removal is
safe only after:

- the support horizon excludes binaries that can create those rows;
- startup migration/audit finds no executable legacy rows, or converts them
  transactionally with tests;
- interrupted/retryable jobs across the supported upgrade path are covered;
- the release notes call out the compatibility boundary.

The support horizon is a maintainer release-policy decision. Until it is made,
keep the strict decoder and its focused tests.

## 6. Validation

- canonical path and symlink aliases find the same owner;
- different workspaces remain independently ownable;
- second `web` and `standalone` hand off only to a health-checked matching
  workspace and return success;
- stale discovery plus an unlocked lease starts normally; stale discovery plus
  a held lease does not open a URL or state;
- every stateful command delegates when an owner exists and holds exclusive
  authority during local fallback;
- a server racing a local stateful command yields one winner before either
  opens `state.db`;
- stateful `--local` bypass is rejected while read-only exceptions are tested;
- CLI output/exit codes remain stable in human and JSON modes;
- pre-RunSpec queued, retryable, malformed, and already-completed rows retain
  deterministic recovery until the compatibility removal is explicitly
  approved.

## 7. Closure

Fold the final launcher/CLI ownership contract into
[`architecture/backend.md`](../architecture/backend.md). Delete this plan when
all stateful commands obey the authority boundary and the legacy decoder has
either been retired under an accepted support horizon or split into its own
time-bounded compatibility plan.
