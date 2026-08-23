#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
edge_file="$(mktemp)"
trap 'rm -f "${edge_file}"' EXIT

go_command="${GO:-}"
if [[ -z "${go_command}" ]]; then
	if command -v go >/dev/null 2>&1; then
		go_command="go"
	elif [[ -x /usr/local/go/bin/go ]]; then
		go_command="/usr/local/go/bin/go"
	else
		printf 'Go was not found on PATH or at /usr/local/go/bin/go; install Go or set GO to its path\n' >&2
		exit 1
	fi
fi

cd "${repo_root}"
module_path="$(${go_command} list -m)"

# Production imports are enough for the direction contract. Tests may import a
# package externally to verify its public surface without changing runtime
# ownership. Keep this check cheap and resource-bounded because it runs before
# build artifacts exist in CI.
GOMAXPROCS="${GOMAXPROCS:-2}" "${go_command}" list \
	-f '{{ $pkg := .ImportPath }}{{ range .Imports }}{{ printf "%s\t%s\n" $pkg . }}{{ end }}' \
	./internal/... ./cmd/... >"${edge_file}"

status=0
while IFS=$'\t' read -r importer imported; do
	[[ -n "${importer}" && -n "${imported}" ]] || continue

	case "${imported}" in
	"${module_path}/internal/web/httpapi"|"${module_path}/internal/web/httpapi/"*)
		case "${importer}" in
		"${module_path}/cmd"|"${module_path}/cmd/"*|"${module_path}/internal/web/httpapi"|"${module_path}/internal/web/httpapi/"*) ;;
		*)
			printf 'dependency boundary violation: %s imports HTTP transport %s\n' "${importer}" "${imported}" >&2
			status=1
			;;
		esac
		;;
	"${module_path}/internal/web/service"|"${module_path}/internal/web/service/"*)
		case "${importer}" in
		"${module_path}/cmd"|"${module_path}/cmd/"*|\
		"${module_path}/internal/clientapi"|"${module_path}/internal/clientapi/"*|\
		"${module_path}/internal/notebookmcp"|"${module_path}/internal/notebookmcp/"*|\
		"${module_path}/internal/web/httpapi"|"${module_path}/internal/web/httpapi/"*|\
		"${module_path}/internal/web/service"|"${module_path}/internal/web/service/"*) ;;
		*)
			printf 'dependency boundary violation: %s imports the service facade %s\n' "${importer}" "${imported}" >&2
			status=1
			;;
		esac
		;;
	"${module_path}/cmd"|"${module_path}/cmd/"*)
		printf 'dependency boundary violation: %s imports the composition root %s\n' "${importer}" "${imported}" >&2
		status=1
		;;
	esac
done <"${edge_file}"

if [[ "${status}" -eq 0 ]]; then
	printf 'Go dependency directions passed.\n'
fi
exit "${status}"
