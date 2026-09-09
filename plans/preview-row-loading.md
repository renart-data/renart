# Shared preview row loading

Status: partially implemented, 9 September 2026. Data Browser, asset Inspect,
notebook cells and ad-hoc queries now share explicit bounded row loading.
Authored table presentations remain out of scope for the shipped adapters.

## Shipped contracts

- [Backend](../architecture/backend.md): generated preview metadata, limit+1
  exhaustion detection, 1,000-row / 2 MiB response budget, read-only replacement
  samples and guarded query continuation. Unsupported T-SQL authored bounds/set
  queries are explicit; this is not universal warehouse pagination.
- [Notebooks](../architecture/notebooks.md#10-server-owned-recompute-and-frontend-state):
  immutable saved prefixes in the existing session DB, four previews per notebook,
  30-minute expiry, generation/scope validation and no cell/import re-execution.
- [Frontend](../architecture/frontend.md): one virtual table/footer, shared request
  admission, cancellation, retained rows on failure, same-bound retry, selection
  preservation for snapshots and invalidation for replacement samples.

Local evidence: .test-artifacts/notebook-query-preview/ contains affected Go
suites, frontend units/lint/build and desktop/mobile live regressions. The prior
Inspect/Data Browser slice is recorded in .test-artifacts/preview-row-loading/.
These are focused checks, not a completed full release/e2e gate.

## Remaining: authored table presentations

Audit notebook-viz.tsx and presentation table consumers separately. Their
source/transform/sample limits describe the authored dataset, not just how many
rows the browser displays. Never silently increase those limits or re-execute
Python, source imports or materialization to fill a viewport.

1. Distinguish a client display cap from an authored dataset/transform cap.
2. Reuse the existing dataset owner and VirtualDataTable footer where a larger
   result is actually available. Do not introduce a second result cache.
3. Keep sample/full semantics and source provenance visible. Unsupported or
   expired continuation needs an explanation, not an endlessly enabled button.
4. Verify dashboard/report/notebook table variants on desktop/mobile, preserving
   chart calculations, selection, export and parameter-scoped invalidation.

When that adapter lands, fold its contract into architecture and remove this
plan; Git retains the original proposal.
