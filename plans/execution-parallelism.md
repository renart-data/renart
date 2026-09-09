# Execution operator audits and wait visibility

Status: active follow-up. The shared execution-unit DAG, durable admission,
bounded workspace/run/connection capacity, failure/cancellation semantics, and
audited native DuckDB concurrency are implemented. Current contracts live in
[backend](../architecture/backend.md) and [staleness](../architecture/staleness.md).
Do not rebuild separate execution loops or infer safe concurrency from an icon.

## 1. Promote only audited operator families

Before narrowing conservative resource claims for another operator:

1. Audit direct and fallback paths for staging tables/files, schema-level
   writes, metadata work, hooks, connection-manager concurrency, and subprocesses.
2. Resolve canonical, credential-free physical identities after environment
   routing. Reuse the same resource contract for cross-run admission and
   within-run dispatch; sort and deduplicate multi-resource acquisition.
3. Prove same-target exclusion and distinct-target overlap. A connection limit
   of one must still win even for distinct relations.
4. Run the relevant warehouse/operator matrix across supported replace, append,
   merge, and incremental windows, including cancellation and recovery.
5. Promote that family only, with its evidence recorded. Unknown targets,
   arbitrary Python, hooks, dynamic routing, and unclassified CLI fallbacks
   stay conservative.

Native DuckDB promotion must join the existing shared in-process session and
preserve its whole-file lease against subprocess/fallback access. A native
relation claim does not make a second process safe. Storage destinations need
the exact overwrite/resource semantics in
[object-storage-assets.md](object-storage-assets.md).

## 2. Explain material waits

Add restrained queued-state explanations for dependencies, workspace capacity,
run step capacity, connection limits, and write-resource conflicts. Publish
only meaningful transitions after a threshold, not an event for every queue
scan. Keep physical paths and credentials out of UI labels and event payloads.

Measure time in each wait class separately from operator execution, plus
effective peak concurrency. Use existing durable unit states and SSE; do not
introduce a second scheduler model or polling.

## 3. Acceptance and rollout

- Channel/barrier tests prove overlap and ordering; use wall-clock measurements
  only as coarse performance checks with generous tolerances.
- Completion/freshness persistence precedes downstream admission.
- Independent branches continue after failure; affected downstream units skip.
- Multiple windows of the same asset and conflicting physical targets serialize.
- Cancellation stops admission, closes queued/running units, and releases native
  sessions, subprocesses, and claims.
- Legacy plans and effective parallelism of one preserve compatibility.
- Live timeline/review checks cover real overlap and truthful wait labels on
  desktop/mobile, including reconnect and terminal state.
- Benchmark four independent units at limits one/four and report operator time,
  waits, process count, memory, and cancellation latency separately.

Do not raise demo defaults or weaken mobile/warehouse coverage to make timings
look better. Test-suite optimization belongs in
[performance-evidence.md](performance-evidence.md).

Close this plan after the selected operator promotions and wait visibility are
documented with their evidence. Further operator reach should be a scoped
feature change, not a permanent concurrency rewrite.
