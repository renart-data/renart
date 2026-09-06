#!/usr/bin/env bash
# Durable, resource-bounded release checks. Never publishes or tags a release.
set -euo pipefail

live=0
checks=1
case "${1:-}" in
  --live) live=1 ;;
  --live-only) live=1; checks=0 ;;
  --help|-h)
    printf 'Usage: bash scripts/release-check-local.sh [--live|--live-only]\n\nDefault: production build and make release-check.\n--live: also run the entire live desktop/mobile suite, serially.\n--live-only: build and run the entire live suite without other release gates.\nLogs, traces and a phase-status manifest stay in .test-artifacts/release-*/.\nNo publishing, tagging or cleanup is performed.\n'
    exit 0 ;;
  '') ;;
  *) printf 'Unknown option: %s\n' "$1" >&2; exit 2 ;;
esac
if (( $# > 1 )); then printf 'Only one option is accepted.\n' >&2; exit 2; fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"
mkdir -p "$repo_root/.test-artifacts"
artifacts="$(mktemp -d "$repo_root/.test-artifacts/release-$(date +%Y%m%d-%H%M%S)-XXXXXX")"
export GOMAXPROCS="${GOMAXPROCS:-2}"
export GOMEMLIMIT="${GOMEMLIMIT:-768MiB}"
export NODE_OPTIONS="${NODE_OPTIONS:---max-old-space-size=2048}"
export RENART_E2E_LIVE_WORKERS=1
export RENART_E2E_OUTPUT_DIR="$artifacts/browser-results"
export RENART_E2E_TEMP_DIR="$artifacts/tmp"
export PWTEST_CACHE_DIR="$artifacts/playwright-cache"
# Keep linker paths stable between runs: a unique -L path invalidates Go's
# entire cgo build cache even when the archive itself has not changed.
export RENART_BRUIN_SQLPARSER_STUB_DIR="${RENART_BRUIN_SQLPARSER_STUB_DIR:-${XDG_CACHE_HOME:-${HOME}/.cache}/renart/bruin-sqlparser-stub}"
export BRUIN_E2E_BINARY="$artifacts/renart"
mkdir -p "$RENART_E2E_TEMP_DIR" "$PWTEST_CACHE_DIR"

{
  printf 'Started: %s\n' "$(date -Is)"
  printf 'Commit: '; git rev-parse HEAD
  printf 'Branch: '; git branch --show-current
  printf 'Working tree:\n'; git status --short
  printf '\nAn absent completion record means interrupted, not passed.\n'
} > "$artifacts/manifest.txt"
printf 'phase\tstarted\tfinished\texit_code\n' > "$artifacts/phases.tsv"
printf 'Release-check artifacts: %s\n' "$artifacts"

run_phase() {
  local phase="$1" started exit_code
  shift
  started="$(date -Is)"
  printf 'Starting %s; output goes to %s/%s.log\n' "$phase" "$artifacts" "$phase"
  printf 'Running: %s (%s)\n' "$phase" "$started" >> "$artifacts/manifest.txt"
  if "$@" > "$artifacts/$phase.log" 2>&1; then exit_code=0; else exit_code=$?; fi
  printf '%s\t%s\t%s\t%s\n' "$phase" "$started" "$(date -Is)" "$exit_code" >> "$artifacts/phases.tsv"
  if (( exit_code != 0 )); then
    printf 'Failed: %s (exit %s). See %s/%s.log\n' "$phase" "$exit_code" "$artifacts" "$phase" >&2
    exit "$exit_code"
  fi
}

run_phase frontend-install env CI=true corepack pnpm --dir web install --frozen-lockfile
run_phase frontend-build corepack pnpm --dir web build
target="$(go env GOOS)-$(go env GOARCH)"
run_phase link-shim bash scripts/build_bruin_sqlparser_stub.sh "$target"
stub_dir="$RENART_BRUIN_SQLPARSER_STUB_DIR/$target/release"
printf 'Link shim cache: %s\n' "$stub_dir" >> "$artifacts/manifest.txt"
if (( checks )); then
  # CI mode lets pnpm reconcile generated node_modules without a TTY. Frozen
  # lockfiles still prevent dependency resolution changes. Keep this local to
  # the gate so it does not implicitly change Playwright's retry policy.
  run_phase release-check env CI=true make release-check "BRUIN_SQLPARSER_STUB_LIB_DIR=$stub_dir"
fi
if (( live )); then
  run_phase backend-build env "CGO_LDFLAGS=-L$stub_dir ${CGO_LDFLAGS:-}" go build -p 1 -o "$BRUIN_E2E_BINARY" .
  run_phase live-e2e corepack pnpm --dir web exec playwright test --config=playwright.live.config.ts
fi
printf 'Completed requested phases: %s\n' "$(date -Is)" >> "$artifacts/manifest.txt"
printf 'Requested checks passed. Review skips/flakes in the retained logs: %s\n' "$artifacts"
