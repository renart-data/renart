# AGENTS.md

Orientation for agents working in this repository. This is a map and a set of
rules — depth lives in [`architecture/`](architecture/) (current state) and
[`plans/`](plans/) (in-flight work). Read the relevant doc there before making
non-trivial changes.

## Positioning

Renart is the all-in-one **data pipeline IDE**, and it runs entirely inside the
user's Git repository. It gives version-controlled data pipelines a visual
editing environment — a pipeline canvas, DAG-aware SQL intelligence, notebooks,
inspect/materialize, and per-environment schedules — while keeping the
filesystem and Git as the source of truth. It is local-first: no hosted control
plane, no sign-up, no data leaving the user's environment. Pipelines run against
the user's own platform (DuckDB, Postgres, Snowflake, BigQuery, Redshift, …) via
the open-source Bruin execution engine.

When making product or UX decisions, preserve this direction:

- Present assets, dependencies, lineage, and data flow on a canvas instead of
  forcing users to reason only from YAML and SQL files.
- Be a fast visual way to build pipelines that stay version-controllable — every
  visual change is a plain, reviewable diff.
- Help users move quickly; don't add ceremony, and don't collapse the product
  into a generic CRUD dashboard.
- Keep it legible for data engineers, analytics engineers, and technical data
  users working in Git-backed projects.

Renart and the Bruin CLI work on the same project files; the CLI stays the
right tool for terminal-first, code-first work. Don't position Renart as a
replacement project format or a separate runtime.

### Where it's headed

The longer-term goal is for Renart to be not just the IDE but a complete,
local-first data platform: pipelines, notebooks, pipeline documentation,
dashboards, reports, and AI agents, all grounded in the same version-controlled
files. Let this direction inform architecture and internal design decisions.

**But be humble — underpromise and overdeliver.** User-facing surfaces (the
landing page, `docs/`, `README.md`, in-app copy) describe **only** what already
works and is established. Never mention planned, aspirational, or partially
working capabilities to users. In-flight work lives in [`plans/`](plans/); it
does not surface to users until it ships and is solid.

## Repo layout

```
main.go, cmd/           urfave/cli entrypoint + command wiring
internal/web/           the Go server: httpapi, service, scheduler, events,
                        watch, plus intelligence/staleness/notebook packages
internal/sqllsp/        SQL language-server core
web/                    static React frontend, embedded into the Go binary
docs/                   user-facing docs (Astro + Starlight)
architecture/           current-state architecture docs (see below)
plans/                  in-flight design docs / proposals
example/                sample workspaces used by tooling and tests
```

## Runtime model (the non-negotiables)

- **The filesystem is authoritative.** Frontend state exists for responsiveness;
  workspace state from the backend wins on conflict. Don't treat Jotai as
  persistent truth.
- **One server, one workspace root**, started inside a Git repository. Don't
  loosen the Git-backed-project assumption to make a flow look easier.
- **Sync is SSE, not polling.** The frontend loads `/api/workspace`, subscribes
  to `/api/events`, and file-changing actions go through Go endpoints under
  `/api/...`; SSE reconciles the final state after writes, CLI usage, or outside
  edits. Never add polling for workspace refresh.
- **All filesystem writes go through the Go server.** Don't bypass it, and don't
  add Node.js-only API routes — there is no Node runtime in production.
- **Inspect is a safe preview path.** Plain-SQL inspect/query must stay read-only
  (single `SELECT`); materialization is the path for side-effecting SQL. When the
  direct execution path can't confidently match Bruin behavior, fall back to CLI
  semantics.

## Stack at a glance

- **Backend:** Go HTTP server (transport `httpapi` → domain `service` → Bruin
  packages), River + SQLite scheduler, SSE hub, fsnotify watcher. Durable state
  in `.renart/state.db`; user-authored files stay plain Bruin files. SQL
  intelligence and formatting use the native pure-Go Golyglot package; Python
  intelligence runs as an embedded WASM engine under wazero.
  Full detail: [architecture/backend.md](architecture/backend.md).
- **Frontend:** React 19 + TypeScript, TanStack Router, Vite, Tailwind + shadcn,
  React Flow canvas, Monaco editor, Jotai/SWR state. Full detail:
  [architecture/frontend.md](architecture/frontend.md).

## Where things are documented

**[`architecture/`](architecture/)** — how it works today. Read before changing
the corresponding subsystem:

| Doc | Covers |
| --- | --- |
| [backend.md](architecture/backend.md) | Go backend: layering, runtime model, execution, conventions |
| [frontend.md](architecture/frontend.md) | Web app: stack, routing, app shell, hooks, libs, layout rules |
| [staleness.md](architecture/staleness.md) | Fingerprints, materialization facts/coverage, staleness, deploy snapshots, per-env schedules |
| [notebooks.md](architecture/notebooks.md) | Notebook folder format, sessions, rename engine, `@viz`, promotion |
| [asset-editing.md](architecture/asset-editing.md) | Asset workbench: ownership model, `assetmeta` provenance, reconciliation, transaction API |
| [sql-lsp.md](architecture/sql-lsp.md) | SQL language server: canonical graph, engine, caching, column inference |
| [docs.md](architecture/docs.md) | Authoring contract for the user docs under `docs/` |

**[`plans/`](plans/)** — proposals and implementation plans for work that has
**not** shipped (or only partially). When a plan lands, fold the as-built reality
into the relevant `architecture/` doc and delete the plan; git history keeps the
original. Check `plans/README.md` for the current index and status.

## Working guidance

- Prefer updating existing hooks/components/services over introducing parallel
  state systems; keep changes small and concrete.
- Use Bruin Go packages and Renart service helpers for filesystem-changing
  operations rather than shelling out, unless a flow intentionally falls back to
  CLI semantics.
- Keep the one-DTO-set / one-error-type / one-response-envelope conventions
  (see backend.md §5). When a Go DTO changes, regenerate the web API types.
- If a change touches both full inspect views and node previews, update both. If
  it changes asset creation or execution semantics, verify frontend behavior and
  backend results.
- Keep layouts shrink-safe (`min-w-0`, truncation, overflow control). Prefer
  `pnpm` over `npm`. Favor user-facing clarity over internal cleverness in
  Renart-specific docs and UI copy.
- Commit when asked, but **never push** — leave pushing to the maintainer.

## Local tooling

- Go is at `/usr/local/go`. If `go` is not on `PATH`, use `/usr/local/go/bin/go`.
- **Hot-reload dev:** `make dev` (or `scripts/dev.sh [workspace]`) runs the Go
  backend under [`air`](https://github.com/air-verse/air) (rebuild + restart on
  any `.go` change, `.air.toml`) and the Vite frontend with HMR together. Open
  the printed Vite URL (`:5173`); it proxies `/api` to the backend (`:3000`).
  The backend builds with `-tags webdev` so it needs no `web/dist` — see
  `web/embed_dev.go`. Defaults to the `example/example` workspace; override with
  `make dev WORKSPACE=path` or `BACKEND_PORT` / `FRONTEND_PORT` env vars. air is
  installed on first run if missing.

## Validation

- Backend: `go build ./...`, `go vet ./...`, `go test ./...`.
- Frontend: `pnpm build` in `web/`.
- Docs: `pnpm build` in `docs/` when touching the Starlight site.
- Live flow (workspace sync / canvas / inspect / materialize / Monaco):
  `go build .` in the repo root and `corepack pnpm test:e2e:live` in `web/`.
