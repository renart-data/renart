# Semantic deployment impact

Status: active follow-ups. Warning-only semantic deployment review, inferred
output-contract changes, formatting-only classification, new-column categories,
and source-backed desktop/mobile annotations are implemented. Current behavior
lives in [staleness](../architecture/staleness.md) and
[frontend](../architecture/frontend.md); this is not a branch-local prototype.

## Implemented baseline

- Deployment review compares the saved working tree with the exact latest
  deployed snapshot; first deployments return an explicit no-baseline state.
- Both worlds use Renart's existing filesystem canonical graph and bounded
  output-schema inference fixpoint.
- SQL identity has separate byte and canonical fingerprints. Formatting and
  ordinary comments are non-semantic, while optimizer directives remain part of
  the identity.
- Per-asset results distinguish direct query changes from output-contract
  changes propagated through unchanged SQL.
- Ordered column diffs include name, canonical type, and known nullability.
  Unknown parse/schema facts make the report incomplete.
- The report is read-only and warning-only. It contains fingerprints and
  contracts, never SQL source, and its digest participates in the immutable
  reviewed-plan identity.
- Deployment review renders the summary and per-column before/after contracts
  without adding a second confirmation or automatic implementation step.

## Deliberate limits

- The first slice covers SQL assets in the pipeline snapshot. Runtime catalog
  observations, query results, and external database execution are excluded.
- Cross-pipeline schemas are only visible when represented inside the
  materialized filesystem graph. Reconstructing the exact producer-deployment
  graph from dependency manifests is a follow-up.
- Templated SQL uses the deterministic filesystem rendering used by the LSP
  loader, not every possible environment/variable/time-window expansion.
- Query behavior is currently one canonical unit. Golyglot's new component
  facts (filter, grouping, relations, ordering, limit, directives, and so on)
  need a fresh check against Renart's pinned dependency before replacing this
  coarse bucket. API availability alone does not mean Renart consumes the facts.
- This is impact analysis, not a proof of relational equivalence.

## Next slices

1. Verify the released Golyglot component-facts API and integrate it so the
   review explains which query dimension changed without noisy whole-query
   annotations.
2. Rebuild exact cross-pipeline baseline/candidate worlds from the reviewed
   dependency manifests and producer deployment pins.
3. Add severity policy for contract compatibility (for example widening vs
   narrowing, removals, and nullability relaxation) while retaining a
   warning-only default.
4. Persist compact semantic reports with deployments for historical compare
   and API/CLI access.
5. Feed the Differential Type-Inference Lab's version-pinned regression corpus
   into CI before expanding to PostgreSQL and Trino.

## Acceptance evidence

- Formatting-only SQL produces equal canonical fingerprints.
- Optimizer directive changes do not disappear as formatting.
- An unchanged downstream `SUM(total_amount)` is classified as propagated
  when the upstream `total_amount` type changes.
- Incomplete facts never produce a clean-equivalence claim.
- Changing the semantic report digest changes the reviewed plan ID.
- Semantic findings can warn but never block deployment by themselves.
