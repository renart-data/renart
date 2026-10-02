# Onboarding flow: follow-ups

Status: the welcome redesign, Getting started, the in-shell connect flow and the
Product demo's notebook and dashboard shipped in October 2026. The as-built
design is in [frontend.md](../architecture/frontend.md) (welcome, Connect your
data, Getting started) and [backend.md](../architecture/backend.md) (project
templates, DuckDB driver step). This plan keeps only what is left.

Decisions made on 1 October 2026 that still apply:

- Default demo: Product analytics. Retail keeps Sling and is marked as needing
  a download.
- Getting started progress stays in browser storage.
- Onboarding steps become usage-analytics events (below).

## 1. Onboarding analytics

Not started. The usage contract is a closed allowlist on both sides:
`internal/web/telemetry/event.go` in Renart and `internal/collector/event.go`
`Validate` in `renart-collector`. The collector rejects a **whole batch** that
contains an unknown event, so a release that sends new events before the
collector accepts them loses that installation's other events too. Collector
deploys are manual in Coolify (automatic deploys are disabled).

Proposed events, to confirm before implementing:

| Event | Outcome | Surface | Buckets |
| --- | --- | --- | --- |
| `onboarding_project_created` | success, failed | `demo`, `import`, `empty` | duration from opening the welcome screen; item count = assets or imported tables |
| `onboarding_first_run_finished` | success, failed, cancelled | `pipeline` | run duration, including the DuckDB driver download |
| `getting_started_step_completed` | success | one value per item (needs a closed `step` field or new surfaces) | none |

Open points:

- The browser can't send usage events itself; a write-only local endpoint has
  to accept these observations and pass them to the telemetry client.
- Whether a step identifier becomes a new closed field (schema change in both
  repositories) or reuses `surface`.
- Order: collector change and deploy first, or send onboarding events in their
  own batches so a rejection drops only them.
- Update the telemetry and privacy pages in `docs/` and
  `renart telemetry sample` with the new payloads.

## 2. Smaller follow-ups

- **Retail's Sling download as its own step.** The welcome run checklist shows
  the DuckDB driver download as a step, but Retail's first run still downloads
  Sling (about 267 MB, through uv) inside the "First run" step.
- **Row counts in the table picker.** Discovery returns tables without sizes,
  so the import picker can't show them.
- **Golyglot parse bug.** In the DuckDB dialect, `WITH e AS (SELECT 1 FROM t
  WHERE a < b) SELECT c > 10 AS n FROM e` fails strict parsing with "Expected
  RParen, got end of input": an identifier `<` identifier inside a CTE followed
  by a later `>` misparses. `a <= b` or `a < 3` parse. The Product demo's
  `product.events` avoids it; fix it in Golyglot and add the case to its tests.
- **Docs media.** The Quickstart has no screenshots of the welcome screen; add
  them to `make docs-media` if the flow stays stable.
