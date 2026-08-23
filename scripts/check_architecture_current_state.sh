#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

declare -a retired_patterns=(
  'embedded wasm formatter'
  'SQL intelligence and SQL formatting run as embedded wasm'
  'current state on `codex/'
  "sqlglot's duckdb dialect"
)

status=0
for pattern in "${retired_patterns[@]}"; do
  if grep -RInF -- "$pattern" \
    "$repo_root/AGENTS.md" \
    "$repo_root/architecture" \
    "$repo_root/internal"; then
    printf 'retired architecture statement found: %s\n' "$pattern" >&2
    status=1
  fi
done

exit "$status"
