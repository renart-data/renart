# Asset names independent of path-derived names

Status: design plan — Bruin syntax exists; Renart creation and rename semantics need alignment

## Goal

Allow a Bruin asset's explicit `name:` to differ from the name Bruin would
infer from its file path. For example, this should remain an ordinary,
CLI-compatible asset:

```text
pipelines/analytics/assets/imports/customer_rollup.sql
```

```sql
/* @bruin
name: analytics.customer_rollup
type: duckdb.sql
@bruin */
select ...
```

This plan does **not** introduce a separate physical output name. The effective
Bruin asset name remains its DAG identity, SQL relation name, and default
materialization target. The change only separates that name from the file path
used to store and identify the definition in Git.

## Current state

Bruin already supports explicit asset names, and Renart's loaders and
node-preserving writers already honor them. Renart also records whether a file
originally contained `name:` so reconciliation does not add it to
path-inferred assets unnecessarily.

The remaining behavior is inconsistent:

- creation normally derives `assets/<prefix>/<leaf>.<ext>` from the requested
  name and omits `name:` for SQL assets;
- the create API accepts both `name` and `path`, but default SQL generation
  still omits `name:` when those values differ, so the next load can silently
  use the path-derived name;
- renaming an inferred asset moves its file to the newly inferred path and
  removes `name:`; renaming an already explicit asset preserves the path;
- a custom path is rejected unless the path itself infers a prefixed name,
  even when a valid explicit prefixed name would make the file unambiguous;
- the UI presents asset name as identity but has no separate, reviewed file
  rename action.

The result is that explicit names work for hand-authored files, while Renart's
own creation and rename flows still assume name and path must move together.

## Contract

Use one rule everywhere:

```text
effective asset name = explicit `name:` when present
                     = path-derived name otherwise
```

The two identities have different jobs:

- **asset name**: Bruin DAG identity, dependency target, relation name, and
  materialization target;
- **asset path / encoded asset ID**: stable Git file and editor/navigation
  identity.

Changing an asset name updates dependencies and SQL references but does not
implicitly move the file. Moving or renaming the file does not change an
explicit asset name. An inferred asset becomes explicit the first time its name
is changed independently of its path.

## Backend changes

### Creation

Centralize definition generation around `(requested name, chosen path)`:

1. Validate the requested name using the existing prefixed-name rules.
2. Validate that the path is safe, has a supported asset extension, and lives
   below the pipeline's `assets/` tree.
3. Compare the requested name with `inferredAssetNameFromPath(path)`.
4. Persist `name:` when they differ; omit it when they match.

Apply the rule to SQL, Python, Seed, Sensor, HTTP API, and Load generation.
Per-kind writers must not independently decide whether to strip `name:`.

When the caller supplies only a name, retain the current clean default path and
omit redundant metadata. When the caller supplies only a path, inference
continues to require a valid prefixed name.

### Rename and file movement

Change the semantic asset-name transaction so it:

- persists the new explicit name in the existing file;
- refactors direct dependencies and supported SQL/Jinja references exactly as
  today;
- keeps the encoded path-based asset ID stable; and
- never deletes or recreates the file as a side effect.

Add a separate `asset.file.move` operation for users who intentionally want the
path to follow the name. It must preview the new path, reject collisions, move
owned Seed sidecars or Python project companions where applicable, preserve an
explicit name unless the user explicitly chooses to return to inference, and
emit one workspace mutation/SSE reconciliation.

### Persistence and reconciliation

Replace the scattered “preserve inferred name” decisions with a small policy
helper that receives the existing definition and destination path. All
transaction, dependency, column, hook, and schema-sync writers use it so an
explicit name is never stripped by an unrelated metadata edit.

Asset lookup remains path/ID based for mutations and name based for DAG
resolution. Duplicate effective names remain invalid regardless of path.

## UI

- Keep **Asset name** in the identity card and explain that it is the Bruin
  relation/DAG name.
- Show the repository-relative **File path** separately as read-only text with
  a dedicated “Move file” action.
- On rename, state that references will be updated and the file will stay in
  place.
- In creation flows that expose a custom location, preview both the resulting
  asset name and path. Ordinary creation keeps the current name-derived path
  without extra ceremony.
- After a file move, navigate using the returned new asset ID; after an
  asset-name change, keep the current route/model alive.

## Interactions

- Environment schema prefixes still transform the effective asset relation at
  runtime; they have no effect on the definition path.
- Remote-table import can choose a readable logical asset name while storing
  the generated file in a separately chosen folder by writing Bruin's native
  `name:` field.
- Freshness, resource claims, schema evidence, inspect, and materialization
  continue to use the effective Bruin asset name. No physical-target resolver
  or SQL rewrite layer is introduced by this work.
- Git history for an asset is more stable because a logical rename no longer
  forces a file move; an intentional file move remains an explicit reviewable
  diff.
- Because the effective asset name is also the default physical target, a
  semantic rename must preview the old/new targets, warn that the old target is
  left behind in every environment, and block a positively observed collision
  in the selected environment. Any later warehouse cleanup is a separate,
  environment-scoped destructive action; see
  [materialization-target-lifecycle.md](materialization-target-lifecycle.md).

## Validation

- Create every Renart-supported asset kind with matching and differing
  name/path pairs; reload with both Renart and the pinned Bruin parser.
- Rename inferred and explicit SQL/YAML/Python assets and verify the path and
  encoded asset ID remain stable while dependencies update.
- Move a file with and without an explicit name; verify effective identity is
  unchanged and collisions are rejected before writing.
- Run unrelated column, hook, connection, and materialization edits afterward
  and prove they retain the explicit name.
- Cover Seed owned sidecars, API/Load node-preserving YAML, notebook promotion,
  source imports, quoted names, and case-insensitive duplicate detection.
- Add a live flow that renames, reloads, materializes, inspects, and type-checks
  the asset through both Renart and Bruin CLI semantics.

## Rollout

1. Add cross-kind tests for the effective-name policy and fix creation when a
   caller supplies a non-matching path.
2. Make semantic asset rename path-stable and remove the implicit file move.
3. Add the explicit file-move transaction and UI.
4. Cover import/notebook promotion and update current-state architecture docs.
5. Document the behavior once the UI and Bruin compatibility tests ship, then
   delete this plan.

## Decision before implementation

The recommended default is that changing **Asset name** preserves the file
path, with file movement available only through the separate action. Confirm
that product behavior before changing the existing rename semantics.
