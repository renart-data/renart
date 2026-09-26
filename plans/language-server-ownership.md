# Language intelligence ownership and keyword recovery

Status: proposed follow-up; the notebook inferred-column bug is fixed separately in this branch. Audited 22 September 2026 against Golyglot alpha.12.

## Ownership rule

SQL meaning belongs in the Go language-server core. Go services provide workspace, connection, catalog, Jinja and runtime context. Monaco adapts protocol ranges, paints the UI, and applies explicit user actions. The server must not depend on a browser being open to produce the same semantic answer.

## Current inventory

| Responsibility | Current location | Target / action |
| --- | --- | --- |
| Parsing, dialect syntax, typed expressions and parser recovery | `internal/sqlintelligence/golyglot_*.go`, upstream Golyglot | Keep grammar and parser facts upstream; Renart must not maintain a competing SQL grammar |
| Scope, canonical relations, inferred schemas, completion, hover, rename, references, diagnostics, actions | `internal/sqllsp/` | Keep here and extend with shared regression fixtures |
| Project/env/connection context, catalog enrichment, authoring graph | `internal/web/service/sql_lsp*.go`, `asset_schema_authoring_graph.go` | Keep IO/authority in services; supply immutable request-local graph layers |
| Notebook SQL | `notebook-cell-editor.tsx` → `use-sql-lsp.ts` | Already uses the LSP. Preserve pipeline inferred layers when adding notebook cells; this branch fixes both destructive filtering points |
| Asset, ad-hoc, checks, hooks and presentation-query SQL | `use-asset-monaco.ts`, `adhoc-editor.tsx`, `asset-custom-checks.tsx`, `asset-hooks.tsx`, `presentation-query-editor.tsx` | Already wired to the LSP; test parity instead of replacing them again |
| Runtime notebook columns | `use-sql-lsp.ts`, `sql-schema.ts`, `provideLocalSQLCompletionItems` | Move semantic completion to a scoped runtime-schema layer supplied by the notebook service; runtime observations must never be written into authored metadata or global WorkspaceState |
| Legacy scope/alias, clause detection, lexical suppression, keyword and semantic-token fallbacks | `web/lib/monaco-sql-providers.ts`, `use-sql-intellisense.ts` | Audit actual registrations (some are already disabled for LSP models). Port still-active semantic decisions and then remove dead provider paths |
| Static SQL embedded in Python strings | `use-python-query-intellisense.ts` | SQL semantics already use LSP; move reusable literal extraction/source maps to a server document-projection adapter where useful to non-browser clients. Keep Monaco decorations/editor lifecycle client-side |
| Path/value/catalog suggestions | `api-sql-discovery.ts`, `use-sql-lsp.ts`, legacy resolver | Services authorize and bound IO; LSP decides where suggestions belong. Never query arbitrary data merely because a completion popup opened |
| Monaco widgets, range conversion, cancellation, stale-response guards, navigation | `use-sql-lsp.ts`, `sql-lsp-completions.ts` | Keep in frontend; these are editor integration, not SQL semantics |
| Python semantics | Existing Python intelligence/WASM service | Preserve that engine; do not reimplement Python inside the SQL server |

## `orer by` → `order by`

The core already uses Golyglot's `SyntacticContextAt` for context-sensitive keywords and has Renart quick fixes for unresolved relations/aliases/columns. It needs a separate syntax-recovery action; a column-name fuzzy match is the wrong mechanism.

1. Add the exact reported query as a parser/LSP regression. Inspect recovered tokens and expected syntax at the misspelling and immediately before `BY`.
2. If Golyglot loses the clause boundary or expected `ORDER` token, fix and test recovery there first. It should return spans/expected grammar, not Renart-specific action labels.
3. In `sqllsp`, offer **Replace with ORDER BY** only when the parser expects that clause, the token is a close spelling, and the edit removes or improves the syntax failure. Replace only `orer`, retaining the existing `by`, case preference and whitespace. Never produce `ORDER BY by`.
4. Expose the existing code-action response through every SQL editor and embedded Python projection. No automatic rewriting while typing.
5. Negative tests: a real column named `orer`, quoted identifiers, strings/comments, aliases, nested subqueries, Jinja, valid dialect-specific clauses, non-ASCII offsets and an unrelated typo. Verify both completion and quick-fix ranges.

A local probe on 23 September 2026 used the exact reported DuckDB query with typed input columns. At the start of `orer`, after the complete misspelling, and after the existing `by`, Golyglot alpha.12 reported `ORDER` among the expected tokens. Its replacement range after the misspelling correctly covered `orer`. Renart returned `order by` before the misspelling and after `by`, but no matching completion immediately after `orer`; the complete malformed query produced no diagnostic or code action in this probe.

The first implementation step is therefore in Renart: preserve the parser's expected-keyword evidence through typo-aware completion and a range-safe correction. This probe does not establish an upstream parser defect or full recovery correctness across dialects. Test malformed-statement diagnostics separately, and involve Golyglot only if the expanded fixtures show missing grammar/recovery facts. In particular, inserting an ordinary `order by` completion after the existing `by` is not a correction: the action must edit the original misspelling without duplicating the clause. Local evidence is retained in `.test-artifacts/notebook-polish/keyword-probe.jsonl` in the main checkout; the eventual regression should live alongside the LSP tests.

## Staged plan

1. **Parity baseline (1 day):** map enabled providers per surface; capture completion/diagnostic/action snapshots for the same documents, including CTEs, aliases, joins, raw tables, Jinja and notebook inferred/runtime columns.
2. **Runtime schema contract (1–2 days):** attach observations by workspace, environment, connection, notebook, cell, revision and run result. Invalidate on rerun, connection switch, cell deletion and session expiry. Keep requests immutable and other notebooks invisible.
3. **Remove duplicate semantic fallbacks (2–3 days):** migrate one active provider at a time with browser-vs-core parity tests, bounded catalog lookups and cancellation. Retain presentation-only adapters.
4. **Keyword recovery (1–2 days):** turn the exact-query probe into a regression and implement the action above; an upstream release is a separate dependency if Golyglot needs a change.
5. Fold established ownership into `architecture/sql-lsp.md`; delete completed plan sections.

## Acceptance

Identical source + schema + dialect + document context gives equivalent semantics in notebook, asset, ad-hoc, hook/check, presentation and static Python-query surfaces. No schema pollution across projects/notebooks/connections, no repeated full-graph inference on keystrokes, no stale edit applied after the document changes, and no loss of UTF-16/Jinja source mapping. Keep the existing focused LSP tests and add browser coverage only for adapter behavior.
