# Local validation and live-test evidence

The production frontend is built before the Go binary embeds it. Live tests run
against that binary in disposable Git-backed workspaces, not against the user's
project. Each test keeps its own server and workspace; database fixture locks
and single-worker execution preserve external-resource isolation.

Storage browser tests use `live-storage-app-fixture.ts` to start S3/SFTP before
the browser and supply credentials through environment secret references. They
must not depend on the developer's unlocked desktop keyring. Regression SQL
belongs in tracked `web/tests/fixtures/`, not the untracked example workspace.

## Local entry points

`scripts/release-check-local.sh` records logs, phase exit codes, the source Git
state, and a completion manifest under a unique `.test-artifacts/release-*/`
directory. It does not publish, tag, or remove previous runs.

| Command | Coverage |
| --- | --- |
| `bash scripts/release-check-local.sh` | Production build and `make release-check` |
| `bash scripts/release-check-local.sh --live` | The above plus the entire live desktop/mobile suite |
| `bash scripts/release-check-local.sh --live-only` | Production build and the entire live suite, without the other release gates |
| `bash scripts/release-check-local.sh --notebooks` | Frontend check (lint, units, type drift, production build/bundle budgets), Go API-generator/notebook/service tests, architecture gate, then notebook/workspace/freshness live tests on both devices, without retries |

The notebook profile is a development feedback loop, **not a full release
gate**. It retains notebook creation/authoring, runtime recompute, cancellation,
Python logs, agent wiring, cross-tab workspace synchronization, and freshness
failure coverage. Use the complete gate for release candidates and changes
outside this boundary.

Phases run serially, with one live worker. When a systemd user session is
available, the script re-enters a scope with a hard **4 GiB process-tree memory
limit and no swap**; `RENART_CHECK_MEMORY_MAX` can override the limit. Go and
Node also have heap targets. Without systemd, the script warns that these heap
targets are not a hard cap on native allocations or child processes. An absent
completion record means interrupted, not passed.

## Timing and interruption recovery

`live-app-fixture.ts` attaches workspace-copy, server-start, test-body, and
teardown timings. `live-timing-reporter.ts` appends each completed attempt to
`live-timings.jsonl` immediately. If the process is killed, completed attempts
remain inspectable; an in-flight test may have only its streamed log. Only
`onEnd` writes the final `live-timings.json` and Markdown summary, with the run's
actual status. Beginning a new run clears this reporter's prior summaries and
journal so old success cannot masquerade as a completed new run.

Keep output and scratch paths in the run's persistent artifact directory. A
JSONL checkpoint is partial evidence, never proof that the whole suite passed.

## Device exclusions before expensive setup

Tests for desktop-only editor/keyboard interactions declare `@desktop-only` in
their title, with the reason beside the declaration. The automatic
`deviceContract` fixture skips those cases on `isMobile` before lazy browser,
server, and database fixtures. They remain visible as skipped in the report;
the desktop case still runs normally. `--grep @desktop-only` selects this cohort
for verification. Do not add the marker merely to shorten a slow test run.

This follows Playwright's documented
[automatic-fixture ordering](https://playwright.dev/docs/test-fixtures#execution-order)
and [title tags](https://playwright.dev/docs/test-annotations#tag-tests).

The September 7 full-run timing baseline had 410 attempts for 408 cases. Test
bodies accounted for about 1,734 seconds; copying workspaces took less than one
second in total. Of 67 skipped attempts, 63 still started a server, and skipped
attempts consumed about 38 seconds overall. Moving 59 existing immutable mobile
exclusions earlier addresses that concrete waste without sharing server state,
removing assertions, or reducing mobile coverage. It does not make the whole
suite dramatically faster: the matched 59 attempts fell from 35,335 ms in the
baseline to 76 ms in a focused no-binary skip check (summed attempt durations,
not a controlled full-suite wall-clock comparison). Further sharding, fixture
sharing, or warehouse matrix reduction requires new measurements and an
isolation review.

## Retained release evidence

The former v0.5.1 preparation and release-note plans were historical scratchpads,
not current release gates. Their original contents remain in Git history,
including the v0.5.1 tree. The completed local gate at
`.test-artifacts/release-20260906-184404-tuSN2z/manifest.txt` records its exact
dirty candidate and completion on 6 September; the separate
`.test-artifacts/2026-09-06-release-prep/e2e-authoring-complete.log` records eight
passing authoring cases. The artifacts remain local and ignored, not bundled
into Git or copied into a permanent plans archive.

Those results do not establish that the historical full live suite, a later
candidate, a remote CI run, or packaged release artifacts passed. Use a fresh
manifest for the exact candidate being released. Video artifacts and unrelated
marketing proposals are not part of these validation records.
