# Adding assets from the canvas

Status: proposed, 2 October 2026. Phase 1, the independent fixes, shipped:
prefix-aware names with the leaf preselected, a runnable SQL starter, an
Inspect info card for an empty body, **Create downstream asset** in the node
menus and on keyboard focus, and files without injected `secrets:` (see
`architecture/asset-editing.md` and `architecture/frontend.md`). Phases 2 and 3
are not started and need the decisions listed at the end.

## How it works today

Observed in a `renart init --template product` project (five DuckDB SQL assets,
one built run) in Chromium at 1440×900, plus the code paths behind each step.

| Entry point | Result |
| --- | --- |
| Hover a node, click the round **+** on its right edge | **New downstream asset** dialog: SQL, Python or Load; SQL preselected; name `prefix.<source>_downstream` |
| Right-click empty canvas | One-item menu, **New asset** (or **New asset in `prefix`** inside a group box), opens the six-kind dialog |
| Explorer pipeline row, file-plus icon | Same six-kind dialog |
| Empty pipeline panel | **New asset** button, same dialog |
| Data Browser drag / **Use in canvas** | Reviewed Source or Load creation onto group and destination targets |
| Type-check external relation node | Reviewed import dialog |
| Post-import callout | `createAssetFromSources`: downstream of the first source, other sources listed in a comment |
| Ad-hoc query | **New asset** with the draft as the body |

Not available: the command palette (navigation only), drawing an edge
(`nodesConnectable={false}`), and multi-select.

After **Create**, `revealCreatedAsset` waits for the SSE workspace update and
opens the asset's source section, switching a canvas-only route to split view.
The bottom panel auto-runs Inspect for the new asset.

## Remaining findings

1. **Creating pulls focus away from the graph.** A canvas-only view becomes split
   view. With the explorer and inspector open, the canvas shrinks from about
   750 px to 370 px wide and the new node sits at its edge. The DAG the user was
   extending is mostly off screen.
2. **Creation feels slow although the server is fast.** `POST …/assets` returns in
   about 170 ms. The dialog stays open 1–3 s longer, and the new file becomes
   editable about 3 s after clicking **Create**. Within those seconds the client
   issues 5 running-runs polls, 3 type-checks, 7 `sql/parse-context` and
   3 diagnostics requests. Each dialog open also refetches the 184 KB
   `asset-creation-profile` (36–376 ms observed).
3. **The downstream starter ignores what Renart already knows.** It always writes
   lowercase `select * from product.events`, even when the upstream's columns are
   declared or inferred (`WebColumn[]` is in the workspace DTO). The templates use
   uppercase keywords and explicit column lists. An explicit list gives column
   lineage and a better editing start.
4. **Joins and existing dependencies cannot be expressed on the canvas.** There is
   no way to start from two assets. `sourcesStarterQuery` only selects from the
   first source and lists the rest in a comment. Connecting two existing assets
   requires editing SQL or the inspector's Lineage tab.

## Phase 2: create in place on the canvas

Make the common case one gesture without leaving the graph.

- **Quick create:** the **+** (and **Create downstream asset**) places a pending
  node to the right of its source with an inline name field, leaf preselected,
  and a SQL/Python/Load switch. Enter creates it, Escape cancels it, and
  **More options…** opens the existing dialog with the draft carried over. The
  dialog stays the path for API, Seed, Sensor and standalone Load.
- **Optimistic reveal:** show the pending node immediately and replace it when
  the SSE update arrives. On failure, keep the node in an error state with the
  message and a retry, rather than closing a dialog first.
- **Stay in context:** keep the user's layout. In canvas-only view, open the new
  asset's source in a side panel or peek, not a forced split. Keep the new node
  centred and briefly highlighted until the user moves the viewport.
- **Better starter:** when upstream columns are known, write an explicit,
  formatted column list in the project's keyword style. Fall back to `SELECT *`
  otherwise.
- **Quieter post-create work:** coalesce the running-runs polling and type-check
  refreshes triggered by one creation, and cache the creation profile per
  pipeline/environment with revalidation instead of refetching on every open.
- **Commands:** add **New asset** and **Add downstream of selected** to the
  command palette, with keyboard shortcuts.

## Phase 3: the canvas as an authoring surface

Each item changes how lineage is authored, so each needs the decisions below.

- **Drag from a node's output to empty space** opens quick create (same as **+**).
- **Multi-select** (Shift-click or marquee) then **Create downstream** writes a
  join skeleton over the selected assets. This replaces the comment-only
  multi-source starter.
- **Drag from one node to another** offers explicit choices: for SQL, insert a
  reference (join skeleton) or add a manual dependency (`renart_dep_add`); for
  Python and Load, add a dependency.
- **Richer pane menu:** list the kinds directly (SQL, Python, Seed, Load from
  table, Import table) so a right-click skips the kind tiles.

## Decisions needed

- Edge drawing for SQL: SQL stays the source of truth, so is a manual dependency
  acceptable, or must a drawn edge always edit the query?
- Whether quick create replaces the downstream dialog entirely or only fronts it.
- Shortcut keys; they must not collide with Monaco or the Alt+Left/Right history
  handling.

## Verification

- Unit: starter SQL from known columns and the pending-node state machine
  (pending, created, failed, cancelled).
- Live e2e (`web/tests/e2e`): quick create from the **+** and from a node menu;
  the new node is visible and selected; a canvas-only layout is preserved; a
  failed create keeps the pending node with its error.
- Measure click-to-editable time on the product template before and after
  Phase 2; the target is under 500 ms to a visible pending node.
