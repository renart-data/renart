#!/usr/bin/env bash

set -euo pipefail

target="${1:?missing target triple}"
lockdir="${HOME}/.cache/renart-rustsqlparser-release.lock"

mkdir -p "$(dirname "${lockdir}")"

while ! mkdir "${lockdir}" 2>/dev/null; do
	sleep 1
done

cleanup() {
	rmdir "${lockdir}"
}

trap cleanup EXIT

if ! command -v rustup >/dev/null 2>&1; then
	curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --profile minimal --default-toolchain stable
fi

if [ -f "${HOME}/.cargo/env" ]; then
	# shellcheck source=/dev/null
	. "${HOME}/.cargo/env"
fi

if ! command -v cargo >/dev/null 2>&1; then
	echo "cargo not found after Rust setup" >&2
	exit 1
fi

bruin_version="$(go list -m -f '{{.Version}}' github.com/bruin-data/bruin 2>/dev/null || true)"
if [ -z "${bruin_version}" ]; then
	bruin_version="v0.11.528"
fi

go mod download "github.com/bruin-data/bruin@${bruin_version}"

module_dir="$(go list -m -f '{{.Dir}}' github.com/bruin-data/bruin 2>/dev/null || true)"

vendor_dir="$(pwd)/vendor/github.com/bruin-data/bruin"
if [ -f "${vendor_dir}/pkg/sqlparser/rustffi/Cargo.toml" ]; then
	module_dir="${vendor_dir}"
elif [ -z "${module_dir}" ] || [ ! -d "${module_dir}" ]; then
	module_dir="$(go env GOMODCACHE)/github.com/bruin-data/bruin@${bruin_version}"
fi

rustffi_dir="${module_dir}/pkg/sqlparser/rustffi"

if [ ! -f "${rustffi_dir}/Cargo.toml" ]; then
	echo "unable to locate Bruin rustffi sources at ${rustffi_dir}" >&2
	exit 1
fi

if [ "${target}" = "x86_64-pc-windows-gnu" ]; then
	mingw_include_dir="/usr/x86_64-w64-mingw32/include"
	for header in KnownFolders.h ShlObj.h Propkey.h; do
		lower_header="$(printf '%s' "${header}" | tr '[:upper:]' '[:lower:]')"
		if [ -f "${mingw_include_dir}/${lower_header}" ] && [ ! -e "${mingw_include_dir}/${header}" ]; then
			ln -s "${mingw_include_dir}/${lower_header}" "${mingw_include_dir}/${header}"
		fi
	done
fi

chmod -R u+w "${rustffi_dir}" || true
rm -f "${rustffi_dir}/target/release/libbruin_rustsqlparser.a"

rustup target add "${target}"
cargo build --release --manifest-path "${rustffi_dir}/Cargo.toml" --target "${target}" --target-dir "${rustffi_dir}/target"

target_archive="${rustffi_dir}/target/${target}/release/libbruin_rustsqlparser.a"
generic_archive_dir="${rustffi_dir}/target/release"

test -f "${target_archive}"
mkdir -p "${generic_archive_dir}"
cp "${target_archive}" "${generic_archive_dir}/libbruin_rustsqlparser.a"
