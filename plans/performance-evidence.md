# Measurement-driven performance follow-ups

Status: evidence gathering, not an approved rewrite. Generated API contracts,
capability profiles, focused UI controllers, backend domain seams, immutable
workspace snapshots, SSE recovery guards, request limits, and bundle budgets
are implemented. Their owners are documented in
[backend](../architecture/backend.md), [frontend](../architecture/frontend.md),
[notebooks](../architecture/notebooks.md), and [testing](../architecture/testing.md).

## Evidence needed before changing architecture

| Boundary | Collect | Change only if justified |
| --- | --- | --- |
| Live E2E | Per-attempt setup/body/teardown time, memory, skips, retries, and coverage by device/warehouse | Shard isolated cohorts or remove proven duplicate work; never hide failures or weaken mobile coverage by default |
| Workspace snapshots/SSE | Refresh/clone duration, payload sizes, fan-out, backlog/drop counters at representative workspace sizes | Set budgets first; introduce deltas only after the existing protocol exceeds them |
| Project runtimes | Open runtime count, active jobs/subscriptions, file descriptors, memory, idle duration | Add explicit close/idle eviction only with active-use guards and deterministic reopen behavior |
| Frontend | Cold-start and first-interaction profiles alongside current chunk budgets | Change lazy boundaries for the measured owner, not arbitrary file-size targets |
| Python broker | Source query, transfer, Arrow encoding, broker hop, startup, and result publication separately | Evaluate Arrow Flight only if the loopback transport is a demonstrated bottleneck |
| SQL lineage | Wide projections and recursive CTE workloads with parse/inference cost separated | Extend batching only through a reusable released Golyglot API and parity tests |

## Constraints

- Keep Git/filesystem ownership, one workspace authority, and the existing SSE
  reconciliation protocol. No polling, microservices, or parallel state model.
- Keep integration-heavy executor/notebook adapters together until feature work
  exposes a cohesive lower port. Reducing line counts is not an extraction goal.
- Use the existing timing reporter and serial memory-bounded runner; preserve
  interrupted/red runs. Workspaces and servers remain test-isolated.
- Require equivalent behavior, measured improvement, and bounded memory before
  changing fixture isolation, connection reuse, runtime lifetime, or defaults.
- Record machine/configuration, commit and dirty state, inputs, cold/warm scope,
  durations, and attempts. Do not turn one local result into a platform claim.

## Closure

Select one measured bottleneck at a time and give it an explicit acceptance
budget. Fold a completed constraint into architecture/testing rather than
growing another audit history. Park unproven optimizations without treating
them as release blockers.
