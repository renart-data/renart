#!/usr/bin/env bash

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
output="$(mktemp)"
trap 'rm -f "${output}"' EXIT
go_command="${GO:-}"

cd "${root}"

if [[ -z "${go_command}" ]]; then
	if command -v go >/dev/null 2>&1; then
		go_command="go"
	elif [[ -x /usr/local/go/bin/go ]]; then
		go_command="/usr/local/go/bin/go"
	else
		echo "Go was not found on PATH or at /usr/local/go/bin/go; install Go or set GO to its path" >&2
		exit 1
	fi
fi

if ! command -v "${go_command}" >/dev/null 2>&1; then
	echo "Go executable ${go_command} was not found; install Go or set GO to its path" >&2
	exit 1
fi

# go-licenses v1.6.0 misidentifies these three module roots even though each
# contains a LICENSE file. The deterministic notice generator independently
# requires and bundles those exact files, so ignoring them here only bypasses
# the classifier bug; it does not omit their notices.
if ! GOTOOLCHAIN="${GOTOOLCHAIN:-go1.26.6}" "${go_command}" run github.com/google/go-licenses@v1.6.0 check . \
  --disallowed_types=forbidden,unknown \
  --ignore=github.com/DATA-DOG/go-sqlmock \
  --ignore=github.com/segmentio/asm \
  --ignore=modernc.org/mathutil >"${output}" 2>&1; then
  cat "${output}" >&2
  exit 1
fi

GO="${go_command}" node scripts/generate-third-party-notices.mjs --check
printf 'Go dependency license classifications passed.\n'
