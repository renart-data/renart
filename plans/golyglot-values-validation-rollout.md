# Golyglot `VALUES` validation rollout

Status: upstream fix prepared locally; Renart still pins Golyglot
`v0.1.0-alpha.4` until a newer upstream prerelease is published.

## Failure and ownership

The notebook seed query is valid DuckDB SQL, but Golyglot alpha.4 reports
`SEMANTIC_EMPTY_PROJECTION` for the `VALUES` body of its CTE. `VALUES` query
roots intentionally have `ValuesRows` instead of a normal `Projections` list;
set operations over `VALUES` use an additional wrapper node with left and right
query branches. The validator treated both representations as empty `SELECT`
statements and attached the diagnostic to the full query-node span, which is
why Monaco rendered a large underline.

This is parser/validator behavior owned by Golyglot. Renart should consume the
upstream correction rather than suppressing the diagnostic or special-casing
`VALUES` in `internal/sqllsp`.

## Upstream release

1. In `/home/lukas/git/golyglot`, retain focused public-API regressions for a
   standalone `VALUES` query, `VALUES` on both sides of a set operation, and
   the exact DuckDB seed CTE shape used by the notebook.
2. Run the focused package test, `make release-check`, and `make docs-build`.
   If the parser or a hot path changes beyond this validator guard, also run
   the revision benchmark workflow against the previous release commit.
3. Merge the fix to Golyglot `main` and wait for both required CI jobs (`verify`
   and `docs`) on the exact commit that will be tagged.
4. Tag the next Golyglot prerelease on that green commit, publish its GitHub
   release, and confirm that a fresh module download resolves through the Go
   proxy. Also compile/test the downloaded module with `CGO_ENABLED=0` so the
   pure-Go distribution contract is covered.

## Renart dependency bump

1. Update `github.com/renart-data/golyglot` in `go.mod` and `go.sum` to the
   published prerelease; do not use a local `replace` for the final change.
2. Add a Renart regression around the exact seed query in
   `internal/sqllsp/analyzer_test.go`. It should assert that the native
   Golyglot diagnostic adapter emits no error for the valid CTE while existing
   invalid-`SELECT` coverage still reports syntax/semantic errors.
3. Run the focused SQL-intelligence/LSP tests, `go mod verify`, and
   `make release-check`.
4. Build with `make build` before stopping or replacing the running Renart
   instance. Then smoke the release-demo notebook and confirm both that the
   seed cell executes and that Monaco no longer shows the large underline.

## Completion

This follow-up is complete only when Renart depends on the published Golyglot
version, the Renart regression and release checks pass, and the notebook smoke
is clean. Then fold any lasting validator-contract detail into
`architecture/sql-lsp.md` and remove this plan.
