# Data Browser authoring and release preparation

2026-09-06. Main worktree, branch `codex/data-browser-release-prep`, based on
`codex/semantic-impact` (`45754793`). Preserve other worktrees and main.

## Implementation sequence

1. Audit Ingestr visibility. Centralize connection creation filtering and test
   default, opt-in, and legacy editing. Ordinary users must not be offered
   Ingestr. Check templates, completion, creation profiles, and runtime startup.
2. Add credential-free Data Browser -> canvas authoring. A table offers Source
   creation; a destination connection offers Load creation downstream of an
   eligible asset. Reveal contextual targets only for eligible drags. Reuse
   canonical Go introspection/creation and existing dialogs. Drop opens a draft;
   cancellation writes nothing and no data is executed. Preserve independent
   navigation; include a non-drag alternative.
3. Review a small relevant set of Awesome lists under
   `/home/lukas/git/awesome-repos`. Read contribution rules and templates,
   including inherited policies, and check duplicates. Skip explicit AI bans.
   Tailor factual one-line entries and PRs disclosing creator affiliation and
   assistance. Push only contributor forks of these external lists, not Renart.
4. Run focused regression tests, production build, API parity and release gates.
   Persist logs/artifacts in `.test-artifacts/2026-09-06-release-prep/`, never
   `/tmp` or memory-backed storage. Bound concurrency; inspect long-suite logs
   after completion. Record skipped/blocked checks separately from passing ones.
5. Commit verified cohesive changes, update as-built architecture, and prepare
   release notes/checklist. Do not publish/tag/push Renart yet.

## Release scope confirmed

The user requested **v0.5.1**, with `release/v0.5.1` pushed before tagging.
Include the Docs refresh and semantic annotation changes; keep all video files
and video-specific capture/edit scripts local in the original worktree. Shared
media helpers needed for reproducible documentation screenshots are included.
The source worktrees remain untouched. Local and remote gates must pass on the
final candidate before the release tag is pushed.

## Progress

- [x] Inspected Git/worktrees and created the isolated branch in main worktree.
- [x] Identified unfiltered shared New connection dialog.
- [x] Cloned four initial relevant Awesome candidates.
- [x] Ingestr unit/runtime audit; shared creation filter and SFTP classification.
- [x] Data Browser canvas authoring and real-backend browser tests.
- [x] Awesome proposals and compliant PRs.
- [x] Persistent release verification and handoff checklist.

## Awesome submissions

- https://github.com/igorbarinov/awesome-data-engineering/pull/357
- https://github.com/pditommaso/awesome-pipeline/pull/245
- https://github.com/gunnarmorling/awesome-opensource-data-engineering/pull/91

All three are open, contain one new entry, and disclose creator affiliation and
AI assistance. The fourth candidate, `pawl/awesome-etl`, is held back because its
author-submission rules require clear third-party adoption; stars/forks alone
are not presented as production use. Local proposals and rule review:
`/home/lukas/git/awesome-repos/renart-proposals-2026-09-06.md`.

## Known pre-existing release blockers

The retained semantic-impact E2E log was previously triaged in
`intro-video-docs-refresh/plans/semantic-impact-e2e-triage-2026-09-06.md`:
10 failures / 315 passes / 67 skips / 2 flakes (37.8 minutes). Several assertions
predate routed URL state, and there are documented creation-navigation,
notebook-collapse and mobile report-focus regressions. Do not advertise the
full release as green based only on this task's focused tests.

## Verified implementation

- Source placement uses server-owned table identity, environment and observed
  columns; creation requires explicit confirmation and refuses collisions.
- Destination placement uses the server's eligible Load roles and preselects
  the upstream asset and target connection. Normal downstream creation is
  unchanged. Contextual controls appear only during placement.
- Keyboard/touch placement, Escape/cancel, empty pipelines and mobile browser
  folder restoration are covered. Foreign drag payloads are rejected.
- New connection creation requires explicit Ingestr opt-in, even with legacy
  assets present; editing existing legacy connections remains available.
- Asset creation navigation waits for the canonical owner to arrive via SSE
  and preserves Inspect. Navigating elsewhere cancels the pending reveal.

Verification on this branch (artifacts under
`.test-artifacts/2026-09-06-release-prep/`):

- Full Go suite passed (`go-all.log`).
- Frontend: 51 unit files / 217 tests passed; lint and production build passed,
  including generated API parity and unchanged bundle budgets.
- New real-server live cases: **8/8 passed**, desktop and mobile, no retries
  (52.8 s; `e2e-authoring-complete.log`). Page-error listeners and canonical-file
  assertions included. Desktop/mobile placement screenshots inspected.
- Existing desktop Load and Seed/sensor creation regressions: **2/2 passed**,
  no retries (27.9 s; `e2e-creation-regressions.log`). The previously documented
  creation-navigation failure is fixed; the other historical failures are not
  implied to be resolved by this result.
- Architecture boundaries and `go mod verify` passed.
- Earlier red runs retained, including the exposed creation-navigation race
  and empty-pipeline context regression; not discarded or counted as passing.
- Complete local `make release-check` passed: Go tests and vet, frontend
  formatting/lint/units/production build, docs checks/build, extension typecheck,
  license classification/notices and all three package audits (no known
  vulnerabilities). Logs and phase statuses:
  `.test-artifacts/release-20260906-184404-tuSN2z/`.
  The runner now keeps the link-shim cache path stable to avoid repeating that
  run's unnecessary full cgo rebuild. Logs and browser artifacts remain unique
  and durable per run; frozen dependency installs work without a TTY.

## Release handoff

Use `bash scripts/release-check-local.sh` for production frontend build followed
by `make release-check`. Add `--live` to include the entire serial live suite;
`--live-only` builds both sides and runs only the live gate. Each run gets a
unique, ignored `.test-artifacts/release-*/` directory with commit/dirty-tree
identity, per-phase logs, browser output and a phase completion manifest.
Interrupted runs remain explicitly incomplete. The runner never publishes,
tags or cleans up artifacts. Concurrency and memory are bounded by default.

Before publishing:

- Docs and semantic annotations are included; videos remain excluded.
- Review the completed release gate results below, including skips/failures.
- Re-run the full live suite on the integrated candidate and resolve the
  remaining old notebook/report/test-expectation failures from the triage.
- Inspect the final Git diff and release notes. Push `release/v0.5.1`, then
  require CI, live E2E and cross-platform release snapshot checks on that exact
  candidate before creating `v0.5.1`. Push/tag/release is now explicitly approved.

The Awesome data-engineering maintainer was thanked, and PR #357 was rebased
onto upstream `master` (`50cc98a`), preserving both neighbouring additions.
The fork was updated with an exact-SHA force-with-lease; GitHub confirmed the
one-line Renart addition is mergeable.
