# Physical output names independent of asset names

Status: design plan — no implementation; requires a Bruin file-format/runtime decision

## Goal

Let a stable logical asset such as `analytics.customer_rollup` read from or
materialize to a differently named warehouse relation such as
`reporting.customer_rollup_v2`, without losing Bruin CLI compatibility,
lineage, SQL intelligence, inspect, freshness, or execution safety.

This is deliberately a plan rather than a Renart-only metadata field. In the
pinned Bruin runtime, `pipeline.Asset.Name` is the physical target consumed by
SQL materializers, seed/load/ingestr operators, schema inspection, checks, and
database metadata updates. Renart also currently uses that same value when it
resolves physical targets and resource claims. A private alias understood only
by Renart would make the same project behave differently under `bruin run`.

## Current constraints

- `name` is simultaneously the DAG identity, SQL relation name, and database
  write target throughout Bruin.
- Database-target Load assets and Renart's Sling seed operator pass the asset
  name as the destination object. File/object-storage Load targets are the
  existing exception and use `parameters.destination_object`.
- Environment `schema_prefix` rewrites schemas at runtime. It is not a logical
  asset rename and must compose with, rather than be duplicated by, an output
  alias.
- Renart's LSP graph, inspect paths, current-table schema evidence, freshness
  recorder, render preview, and execution resource claims all need the exact
  physical identity. Falling back to a logical name in any one of those places
  creates false freshness or unsafe concurrent writes.
- `bruin import database` can create source-placeholder assets for existing
  tables, but today those assets also use the physical `schema.table` as their
  asset name.

## Required upstream contract

Bruin should first gain one canonical way to resolve an asset's output. The
exact YAML spelling needs a Bruin decision; Renart should not invent it. Two
reasonable shapes are a relation field under `materialization` or a typed
top-level output object. The important API is independent of that spelling:

```go
type OutputTarget struct {
    Kind       string // relation, object, file, none
    Connection string
    Identifier string // unprefixed physical relation or object URI
}

func ResolveOutputTarget(asset *pipeline.Asset, pipeline *pipeline.Pipeline, env *config.Environment) (OutputTarget, error)
```

The resolver must be used by every Bruin operator instead of reading
`asset.Name` as a target. Its rules should be:

1. An explicit output identifier wins; absent means the current `asset.Name`
   behavior.
2. `schema_prefix` applies once to the schema portion of a relation target.
3. Connection/database defaults and dialect quoting are resolved centrally.
4. Object/file outputs remain typed targets, not strings accidentally treated
   as SQL relations.
5. Source-placeholder assets can point at an existing relation without
   claiming they materialize it.

Bruin must also provide a logical-to-physical relation mapping to query
rendering. Downstream SQL should continue to refer to the stable asset name;
the runtime renderer rewrites references to the resolved physical relation.
Otherwise users would have to leak environment-specific physical names into
their version-controlled SQL and lineage would become ambiguous.

## Renart design after the upstream seam exists

### One target resolver

Replace the conservative name-based branches in
`internal/web/service/asset_physical_target.go` with an adapter around Bruin's
resolver. The same resolved structure must feed:

- render/plan output and destructive-operation review;
- direct and CLI execution;
- inspect and ad-hoc relation selection;
- current-table schema evidence and output-schema drift warnings;
- freshness facts, target-write observation, and execution resource claims;
- Load/Seed/API/Python output handling;
- catalog links and any “open table” actions.

No consumer should independently concatenate schema prefixes or infer a target
from the logical name.

### Canonical graph

Keep `asset.name` as the canonical DAG key. Add the resolved physical relation
as provenance/alias data on the relation node rather than as a second asset.
Resolution order should be:

1. exact logical asset name;
2. query-local aliases/CTEs;
3. physical relation aliases for the active connection/environment;
4. purely remote tables.

Logical assets always win collisions. Navigation from either logical or
physical spelling should open the asset, while persisted dependencies stay
logical.

### Editing and migration UX

Add an “Output relation” field near materialization, defaulting to “Same as
asset name”. Show both identities anywhere the distinction matters:

```text
Asset       analytics.customer_rollup
Writes to   reporting.customer_rollup_v2
Environment dev -> dev_reporting.customer_rollup_v2
```

Changing the output is a reviewed migration, not an ordinary text edit. Before
saving, Renart should show the old/new physical targets and whether the change
will create a new relation, leave the old one behind, or collide with another
asset. Renaming an asset must not implicitly rename its physical output, and
renaming an output must not rewrite logical dependency names.

### Source placeholders

Reuse Bruin's database-import capability rather than inventing a Renart-only
external node format. The non-interactive Renart service should call a library
API extracted from `bruin import database`, scoped to the selected
connection/environment/table, and persist the generated source-placeholder
asset through the normal transaction/write path. The canvas can show an
ephemeral remote node before import; after import it becomes an ordinary
version-controlled asset whose logical name may differ from its physical
relation.

## Validation matrix

- SQL table/view materialization for DuckDB, Postgres, BigQuery, Snowflake,
  Databricks/Sail, and one catalog-qualified engine.
- Seed, Load, API, and Python table outputs.
- Logical references rewritten through one and several downstream assets.
- Same physical target claimed by two logical assets is rejected before run.
- `schema_prefix` + explicit output target applies exactly once in render,
  inspect, materialize, schema sync, and freshness.
- Quoted/case-sensitive identifiers and database/schema/table qualification.
- Asset rename, output rename, environment switch, and rollback.
- `bruin run`, `bruin render`, and Renart produce the same physical operations.

## Rollout

1. Agree on the Bruin YAML and `ResolveOutputTarget` contract; add upstream
   parser/formatter/materializer tests while preserving name-as-target default.
2. Add logical-reference rewriting and import-library support upstream.
3. Replace Renart's target resolution and resource-claim paths; add contract
   tests against the Bruin resolver.
4. Extend the canonical graph and SQL LSP aliases without changing persisted
   logical dependencies.
5. Add the reviewed editor migration flow and live multi-warehouse tests.
6. Document the shipped syntax, fold the final architecture into current-state
   docs, and delete this plan.

## Decisions required before implementation

1. Bruin YAML spelling and whether non-relation outputs share the same object.
2. Whether physical spellings in authored SQL are accepted as aliases or
   warned in favor of logical asset names.
3. Whether changing an output can optionally move/rename an existing table, or
   initially only changes future writes and leaves cleanup manual.
4. How source-placeholder logical names are proposed when importing a table
   whose physical name collides with an existing asset.
