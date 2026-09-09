# Shared preview row loading

Status: partially implemented, 9 September 2026. Data Browser and asset Inspect
now share bounded replacement loading. Notebook, ad-hoc query and presentation
adapters below remain unfinished; this is not universal result pagination.

## Implemented foundation

- Generated `PreviewMetadata`, backend limit+1 exhaustion detection, 1,000-row
  ceiling and 2 MiB row-payload budget. No count query or unordered page appends.
- One compact VirtualDataTable footer with explicit load-more. Shared pending
  request admission, cancellation, duplicate-click coalescing, retained rows on
  failure, same-bound retry and replacement-selection invalidation.
- Data Browser warehouse/local-file previews and the existing shared Inspect
  owner use the contract. Storage remains metadata-only. Successful Inspect raw
  output is bounded too; the row budget does not promise bounded driver memory.

Current contracts live in [backend](../architecture/backend.md) and
[frontend](../architecture/frontend.md). Tests cover the budget, shared consumers,
late replies, retry, exact-limit exhaustion and virtualized rendering.

## Outcome

Every tabular preview should say how much data is visible and offer one quiet
**Load more rows** action when more can safely be obtained. Loading more must not
execute a notebook cell, materialize an asset, or restart an external import.
Unsupported continuation should be explicit, not an endlessly enabled button.

## Existing pieces to reuse

| Surface | Current owner / gap |
| --- | --- |
| Table rendering | [VirtualDataTable](../web/components/virtual-data-table.tsx) already owns windowing, selection, copy, scroll retention, and optional load-more controls. Keep it. |
| Asset inspect | [use-asset-inspect](../web/hooks/use-asset-inspect.ts) owns shared requested limits and results; load-more uses reliable backend metadata and replaces the sample. |
| Data Browser | [service](../internal/web/databrowser/service.go) fetches limit + 1. The UI starts at 100 and grows to at most 1,000 rows under the shared byte budget. S3/SFTP remain metadata-only. |
| Notebook cells | Runtime owns the bounded result and local materialized relations. UI renders all returned preview rows, but a cell run is not a paging endpoint. See [notebook state](../architecture/notebooks.md#10-server-owned-recompute-and-frontend-state). |
| Query and presentations | Audit the consumers of VirtualDataTable and SQL result DTOs; distinguish ad-hoc query samples, retained notebook results, and table visualization limits. A chart/data transformation limit is not just a display limit. |

Do not add another table library, per-screen infinite-scroll hooks, or a second
query cache. Extend these owners and share the continuation state machine.

## Contract before UI

Use generated Go DTOs for preview metadata: returned rows, whether more is known
to exist, an optional exact total, the active result identity, and continuation
mode. Keep two different operations explicit:

- **Bounded replacement:** request a larger sample and replace the old result.
  This is the existing inspect model. Arbitrary warehouse SELECTs can change or
  reorder between requests, so do not append OFFSET pages or pretend the sample
  is a snapshot. Show “Showing N rows” and a short refresh explanation when needed.
- **Snapshot page:** append only from an immutable, server-owned result identity.
  For notebooks, read a retained local relation/result generation; never call
  Run to obtain more rows. Bind the token to project, environment, notebook,
  cell, result generation, projection/order, and expiry. A replaced/reset result
  invalidates the continuation. Reuse existing runtime locking and artifact
  cleanup rather than creating another database/session manager.
- **No continuation:** exhausted, byte/row ceiling, unsupported adapter, or expired
  result. Return a reason; offer a deliberate refresh/query/export only where
  supported. Metadata listings and their S3 continuation tokens are a separate
  contract in [Data Browser](data-browser.md#3-pagination-and-truthful-cached-states).

Use limit + 1 or an adapter's reliable exhaustion signal, not `rows.length ===
limit`. Do not run expensive COUNT queries just for the footer. Notebook exact
counts may reuse runtime metadata. Preview permission and read-only SQL guards
apply to every request, including continuation; tokens never authorize writes.

## Shared frontend controller

Extract the small request/continuation reducer from inspect into a reusable
preview controller. Domain adapters retain their existing API/cache ownership.
Its input is an identity key and adapter; its output feeds VirtualDataTable:
rows, columns, loading state, load-more action, completeness and local error.

The identity includes project, environment, connection, target or query revision,
execution window, result generation and projection where applicable. Admit only
the newest request for that identity; cancel on replacement/unmount. Deduplicate
double clicks and concurrent consumers. Shared canvas/full-inspect requests
should coalesce to the largest requested bound rather than multiplying queries.

Retain rows and scroll when the next request fails. Retry the same continuation;
never clear a useful table or append duplicate pages. Clear stale selection if a
replacement changes row identity, but preserve it for genuine append pages.
Do not put rows, SQL text or tokens in session storage or route parameters.

## Remaining delivery sequence

1. **Notebook result endpoint.** Add a read-only endpoint in the existing notebook
   service/httpapi adapter. Validate generation under the session lock, page stable
   results, and return a recoverable expiry response after reset/recompute.
   Persisting a new immutable result snapshot needs an explicit disk/TTL budget.
   The existing cell-run manifest and ExportCell session-lock validation are the
   reuse points. A cell fingerprint alone is insufficient: rerunning identical
   SQL changes its result generation, and retained views can change when an
   upstream is replaced. Do not claim immutable paging over those views.
2. **Ad-hoc query + table presentations.** Reuse the adapters; preserve authored
   visualization row limits and full/sample semantics. No implicit re-execution
   of Python, remote transfer, or source assets.
   The existing Query action accepts more than read-only SELECTs; attaching its
   Run callback to load-more would risk repeating side effects. Introduce a
   separately guarded read-result adapter first. Table visualizations in
   `notebook-viz.tsx` consume authored presentation datasets; their transform or
   sample limits must not be silently increased by a display control.

## Acceptance and evidence

- Empty, below-limit, exactly-limit, over-limit, unknown totals and wide rows.
- Simultaneous canvas/full-view consumers; rapid clicks; scope changes; late
  responses; failure/retry without lost rows; bounded mounted DOM and heap.
- Unordered/mutating warehouse data is never appended as a stable snapshot.
- Notebook continuation makes no Run/import/materialization requests. Reset,
  recompute, restart, missing relation and changed schema invalidate old tokens.
- Read-only connections, quoted/catalog-qualified relations, nested project
  paths, permission failures and cancellation retain current safety boundaries.
- Desktop/mobile keyboard/copy/selection/scroll tests; retained low-memory logs
  using [the verification workflow](../architecture/testing.md).

Deliver each slice independently. Fold the shipped contract into architecture;
keep unfinished adapters here rather than claiming universal pagination early.
