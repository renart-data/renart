# Data Browser follow-ups

Status: active follow-ups; hierarchy, bounded preview, lazy path search,
catalog-aware addresses, reviewed canvas handoffs, keyboard navigation, and
S3/SFTP/project-file browsing are implemented in the current working tree.
This plan does not certify a release or every warehouse adapter.

Current behavior belongs in [frontend](../architecture/frontend.md),
[backend](../architecture/backend.md), and
[SQL discovery](../architecture/sql-discovery.md). Storage execution, provider
reach, and Seed compatibility belong in [object-storage-assets.md](object-storage-assets.md).
There is one browser service/controller, not a second tree for each consumer.

## 1. Positive usage matching

Add a server-owned physical-object identity matcher across connection identity,
environment, catalog/database/schema, object name, and kind. Reuse the existing
catalog-aware addresses and effective materialization targets; do not compare
display labels or assume a Source asset owns the observed relation.

Return only positively established references from assets, Load inputs,
notebook sources, and presentation datasets. Ambiguous matches remain observed
objects with an explanation. Absence from a partial listing never proves that
an object or its usage does not exist.

Expose a compact Usage view and links to the actual owning UI through the
existing navigation-target contract. Reading Usage must not import an asset,
run SQL, or create metadata files.

Acceptance: identical labels in different catalogs/environments do not match;
quoted identifiers survive; ambiguous ownership stays explicit; cold-tab links
reach the real owner without resetting unrelated workbench state.

## 2. Notebook handoff and restoration

The shared Data Browser is available beside an open notebook. That navigation
does not itself import a notebook source; the reviewed handoff below remains open.

Add a notebook-source handoff through the existing source approval/import
workflow. Carry a server-issued object identity and selected environment, not
a client-built SQL string or credentials. Opening the flow is not approval to
execute or copy remote data.

Query and reviewed Source/Load creation already exist. Extend those adapters
rather than adding another creation system. Consider bounded recent/pinned
objects only with explicit project/environment/revision invalidation.

Acceptance: cancel creates nothing; approval uses the normal notebook source
contract; read-only connections remain valid sources, not destinations; a stale
object identity is re-resolved or rejected rather than silently redirected.

## 3. Pagination and truthful cached states

Row-preview loading is a separate contract in
[shared preview row loading](preview-row-loading.md); this section concerns
namespace/object listings, not query results.

The current children contract returns a cap and truncation flag, not a cursor.
S3 prefix refinement avoids enumerating ancestors and refetches only when a
capped result cannot answer the narrower filter. It is not pagination.

Introduce opaque, scope-bound continuation tokens only for adapters that can
support them safely. Keep connection, environment, revision, and literal prefix
in the scope. Show exactly what was searched; no recursive whole-warehouse scan
or false completeness claim for capped/unsupported adapters.

Define last-known-good observations, partial reasons, source provenance, and
refresh-error-with-cache before presenting stale entries as navigable. Keep
cached observations separate from authored assets. Cross-restart caching is
optional and needs retention/invalidation rules before implementation.

Acceptance: pages have no silent gaps/duplicates under the documented mutation
policy; changing scope rejects an old cursor; offline refresh identifies stale
data; Retry is bounded; cache entries contain no credentials.

## 4. Shared observations and adapter evidence

Converge browser and SQL-intelligence discovery through normalized positive
observations where their scope contracts agree. Reuse single-flight, deadlines,
and bounded fan-out. If targeted discovery SSE is needed, add it to the current
event channel; never poll workspace state or create a second synchronization
loop.

Measure expensive catalog levels before expanding limits. Maintain a matrix
for each engine's namespace shape, empty/permission-limited catalogs, quoted
identifiers, preview bounds, and cancellation. A simulated connector boundary
does not replace managed-service evidence.

## Order and closure

Deliver Usage, notebook handoff, pagination/cache contracts, and observation
sharing separately. Each slice needs domain/DTO tests plus relevant desktop and
mobile live tests against disposable Git workspaces; authoring tests verify the
actual files and SSE outcome. Include nested roots, disconnected sources, and
late replies without using private developer projects.

When these slices ship, fold their contracts into architecture and remove this
plan. Provider expansion and storage lifecycle remain owned by the storage plan.
