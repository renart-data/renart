# Renart notebooks after Hex: product gap analysis and staged plan

Status: first navigation and focus slice implemented on the feature branch; later phases proposed.

## 1. Method and product lens

This comparison was checked on 2026-08-26 against Renart's current code and
architecture plus Hex's public product, pricing, and Learn documentation. It is
not a hands-on audit of a private Hex workspace, so details that only appear in
an authenticated deployment may be missing.

The goal is not to make Renart a smaller hosted Hex. Renart's advantage is that
the notebook is part of a real, local Git repository beside the pipelines it
uses. The filesystem remains authoritative, changes are ordinary reviewable
diffs, execution happens in the user's environment, and the same Bruin project
works from Renart or the CLI. Improvements should deepen that model.

## 2. Where the products differ

| Area                     | Renart today                                                                                                      | Hex today                                                                                                                              | Direction for Renart                                                                                                              |
| ------------------------ | ----------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| Source of truth          | Ordered SQL, Python, Markdown, control, and visualization blocks stored in Git-native notebook folders            | Projects live in Hex; Git export writes YAML on publish, is one-way, and is incompatible with protected or review-required branches    | Keep Git authoritative and make notebook diffs, navigation, and review substantially better                                       |
| SQL and pipeline context | Type-checked SQL, cross-pipeline lineage, warehouse-aware intelligence, Bruin execution, and local DuckDB interop | Warehouse SQL and DuckDB dataframe SQL, chained SQL, caching, and query mode that can push compatible downstream work to the warehouse | Preserve stronger pipeline context; add an explicit scale-aware preview/query contract later                                      |
| Mixed-language execution | SQL and isolated Python cells interoperate through typed tabular results; safe SQL can auto-recompute             | SQL, Python, and R share a reactive project DAG backed by a persistent configurable kernel                                             | Keep deterministic SQL as the default; offer an opt-in persistent local Python session only with clear reproducibility boundaries |
| Navigation               | Ordered outline and data inventory                                                                                | Search/replace, variable-rich outline, sections, and a visual dependency graph                                                         | Ship notebook-wide search, dependency flow, and code folding first; add authored sections next                                    |
| No-code exploration      | Declarative tables, KPIs, charts, controls, reports, and dashboards                                               | Rich charts, pivots, maps, table formatting, drill-down, cross-filter outputs, and project filters                                     | Add declarative pivot/filter/map blocks and chart-produced filters without hiding generated logic                                 |
| Sharing and review       | Git branches and pull requests are the review mechanism; reports and dashboards stay version-controlled           | Realtime editing, comments, in-product reviews, published apps, saved views, schedules, alerts, and embedding                          | Build Git-aware notebook review and local publication rather than a parallel hosted permission system                             |
| Agents                   | Local coding agents can propose semantic notebook changes without direct warehouse credentials                    | An integrated agent edits notebook logic and app layouts with broader workspace context                                                | Lead on auditable plans, typed lineage context, least-privilege execution, and reviewable change sets                             |
| Deployment and privacy   | Local-first; no hosted control plane and no project data needs to leave the environment                           | Primarily multi-tenant SaaS, with paid enterprise deployment/privacy controls; AI context can include schema, code, and output content | Keep local execution and explicit agent boundaries as first-class product features                                                |

### What Hex does especially well

- Long projects are easier to understand through project search, sections, and
  the cell dependency graph.
- Query mode makes the cost of moving large warehouse results into memory
  explicit and lets compatible charts and transformations remain pushed down.
- UI-first pivots, chart interactions, drill-down, maps, and saved app views
  make exploration accessible without abandoning downstream composability.
- Publishing, schedules, comments, reviews, and realtime presence form a mature
  hosted collaboration loop.

### What Renart should defend

- Git is the actual bidirectional source of truth, not an audit export. Hex's
  documentation explicitly says its export is one-way and cannot use branch
  protection or required review.
- Notebook logic shares type information and lineage with real pipelines and
  can graduate into Bruin assets without changing project formats.
- Local DuckDB, filesystem ownership, and credential-blind agent change sets
  give users a much smaller trust boundary.
- Declarative controls and visualizations remain readable, hand-editable files
  instead of opaque application state.

## 3. Prioritized roadmap

### Phase 0 — navigation and focus (implemented in this branch)

1. Search the whole notebook from the Outline tab. Match cell names and code,
   Markdown, controls, visualization titles/sources, connections, external
   references, and known columns; show a useful snippet and jump to the block.
2. Add a Flow tab derived from the same dependency metadata used by the
   notebook runtime. Show internal upstream/downstream relationships, external
   references, depth, and runtime state; selecting a node jumps to the block.
3. Let users collapse code-cell bodies individually or all at once. This is
   route-local display state, not authored notebook state, and a collapsed
   Monaco editor stays mounted so unsaved drafts are not discarded.

Acceptance criteria:

- Search results preserve notebook order, are keyboard accessible, and update
  without a server round trip.
- Flow construction is deterministic and cycle-safe, and distinguishes
  unresolved/external dependencies from internal cells.
- Selecting a collapsed search/flow result expands it before navigation.
- Folding never changes notebook files, results, or execution semantics.

### Phase 1 — authored structure and review

- Add stable, nestable sections with collapse state and section-level run
  actions. Section membership must be an ordered, reviewable v2 block change.
- Add a notebook-aware diff view that groups renamed/moved blocks and renders
  semantic control/visualization changes cleanly in pull-request workflows.
- Link notebook findings and agent change sets to Git commits/PRs instead of
  creating a second comment and approval system.

### Phase 2 — scale-aware execution

- Introduce an explicit result contract: local dataframe/snapshot versus lazy
  warehouse query, including row/memory estimates and compatibility warnings.
- Compile compatible downstream SQL and visual aggregations into the source
  warehouse while keeping read-only inspect guarantees.
- Add per-cell run policies and content-addressed result caching with visible
  provenance. Keep deterministic SQL auto-recompute as the safe default.
- Evaluate an opt-in persistent Python kernel only after restart behavior,
  dependency locking, cancellation, and non-replayable state are designed.

### Phase 3 — composable exploration blocks

- Add Git-declarative pivot and filter blocks that emit ordinary typed tables.
- Add maps, richer table formatting, chart calculations/facets, and selection
  outputs that can filter downstream blocks.
- Expose generated SQL and cost boundaries for every UI-first transform.

### Phase 4 — local publishing and reusable views

- Publish a notebook selection into the existing presentation/report model,
  with parameterized saved views stored as plain files.
- Reuse Renart schedules and snapshots for refreshes and static/shareable
  artifacts; treat hosted embedding and multi-user runtime sessions as optional
  deployment concerns, not notebook fundamentals.

### Phase 5 — agent depth

- Give agents the same search and flow model as humans, plus typed impact and
  execution-cost previews before a change set is applied.
- Add local evaluation fixtures for notebook plans and a durable, Git-addressed
  audit trail without exposing warehouse write credentials.

## 4. Explicit non-goals

- Rebuilding Hex's hosted workspace, seat, RBAC, and realtime-presence model.
- Making opaque UI state authoritative over notebook files.
- Auto-running arbitrary Python after every upstream edit.
- Claiming warehouse pushdown where Renart cannot match Bruin semantics.

## 5. Risks and measures

- Navigation derived from partial parser metadata can be misleading. Unresolved
  references remain visible and are never silently presented as internal edges.
- More block types can create configuration sprawl. Every new transform must
  have a typed file representation, generated-query inspection, and focused
  migration tests.
- Persistent kernels improve iteration but weaken replayability. Measure cold
  and warm run time, restart recovery, memory ceilings, and deterministic replay
  before choosing a default.
- Track search-to-jump latency, time to locate a dependency, accidental full
  result transfers, notebook replay success, and review turnaround rather than
  feature-count parity.

## 6. Sources

Renart current-state references:

- [`../architecture/notebooks.md`](../architecture/notebooks.md)
- [`../architecture/sql-lsp.md`](../architecture/sql-lsp.md)
- [`notebook-platform.md`](notebook-platform.md)

Hex primary sources checked on 2026-08-26:

- [Projects and notebook model](https://learn.hex.tech/docs/explore-data/projects/projects-introduction)
- [Developing notebooks](https://learn.hex.tech/docs/explore-data/notebook-view/develop-your-notebook)
- [Graph view](https://learn.hex.tech/docs/explore-data/projects/project-execution/graph-view)
- [Sections](https://learn.hex.tech/docs/explore-data/notebook-view/sections)
- [SQL cells, query mode, and reactive execution](https://learn.hex.tech/docs/explore-data/cells/sql-cells/sql-cells-introduction)
- [Chart cells and interactive outputs](https://learn.hex.tech/docs/explore-data/cells/visualization-cells/chart-cells)
- [Pivot cells](https://learn.hex.tech/docs/explore-data/cells/transform-cells/pivot-cells)
- [Project kernels](https://learn.hex.tech/docs/explore-data/projects/environment-configuration/project-kernels)
- [Notebook agent](https://learn.hex.tech/docs/explore-data/notebook-view/notebook-agent)
- [Published apps](https://learn.hex.tech/docs/share-insights/apps/publish-and-share-apps)
- [Saved views](https://learn.hex.tech/docs/share-insights/apps/saved-views)
- [Reviews](https://learn.hex.tech/docs/collaborate/reviews)
- [Git export](https://learn.hex.tech/docs/explore-data/projects/git-export)
- [AI data privacy](https://learn.hex.tech/docs/trust/ai-data-privacy)
- [Pricing and deployment options](https://hex.tech/pricing/)
