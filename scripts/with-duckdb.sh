#!/usr/bin/env bash
# Use the same pinned engine and headers for every local/CI/release Go command.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version=v2.0.0
case "$(uname -s)/$(uname -m)" in
Darwin/arm64)
	platform=osx-arm64
	checksum=c3a571e6d4a0da97534c631df463ab13c07f8cf78049625f5df936b7beaab9ce
	system_libs='-lc++ -Wl,-no_warn_duplicate_libraries'
	;;
Darwin/x86_64)
	platform=osx-amd64
	checksum=7e6101ddc8de73e8dcc66f790e658e07891165d026992b40ceba20e50b01f8ec
	system_libs='-lc++ -Wl,-no_warn_duplicate_libraries'
	;;
Linux/x86_64)
	platform=linux-amd64
	checksum=112d62c980f5447cd303a70b3dacbc09acd41ccb4ef6d154e8629f543a0f8568
	system_libs='-lstdc++ -lm -ldl -pthread'
	;;
Linux/aarch64)
	platform=linux-arm64
	checksum=8171b4842e68a85b2e82b342d3bf7b542a88fd6666e0c18438d7b459012494ee
	system_libs='-lstdc++ -lm -ldl -pthread'
	;;
*)
	echo 'DuckDB builds require a supported native macOS/Linux runner.' >&2
	exit 1
	;;
esac

libs="$root/.cache/duckdb/$version/$platform"
if [[ ! -f "$libs/.verified" ]]; then
	mkdir -p "$(dirname "$libs")"
	lock="$libs.lock"
	acquired=false
	for ((attempt = 0; attempt < 300; attempt++)); do
		if mkdir "$lock" 2>/dev/null; then
			acquired=true
			break
		fi
		if [[ -f "$libs/.verified" ]]; then break; fi
		sleep 1
	done
	if [[ "$acquired" == false && ! -f "$libs/.verified" ]]; then
		echo "Timed out waiting for DuckDB cache lock: $lock" >&2
		exit 1
	fi
	if [[ "$acquired" == true ]]; then
		trap 'rmdir "$lock"' EXIT
		if [[ ! -f "$libs/.verified" ]]; then
			stage="$(mktemp -d "$(dirname "$libs")/.download.XXXXXX")"
			trap 'rm -rf "$stage"; rmdir "$lock"' EXIT
			url="https://github.com/duckdb/duckdb/releases/download/$version/duckdb-static-libs-$platform.tar.gz"
			curl --fail --location --silent --show-error "$url" -o "$stage/archive.tar.gz"
			if [[ "$platform" == osx-* ]]; then
				actual="$(shasum -a 256 "$stage/archive.tar.gz" | awk '{print $1}')"
			else
				actual="$(sha256sum "$stage/archive.tar.gz" | awk '{print $1}')"
			fi
			[[ "$actual" == "$checksum" ]] || {
				echo 'DuckDB archive checksum mismatch.' >&2
				exit 1
			}
			mkdir "$stage/lib"
			tar -xzf "$stage/archive.tar.gz" -C "$stage/lib"
			for file in duckdb.h duckdb_static_extension.h libduckdb_static.a libcore_functions_extension.a libjson_extension.a libparquet_extension.a libicu_extension.a; do
				[[ -s "$stage/lib/$file" ]] || {
					echo "DuckDB archive lacks $file" >&2
					exit 1
				}
			done
			printf '%s\n' "$checksum" >"$stage/lib/.verified"
			mv "$stage/lib" "$libs"
			rm -rf "$stage"
		fi
		rmdir "$lock"
		trap - EXIT
	fi
fi

[[ "$(cat "$libs/.verified")" == "$checksum" ]] || {
	echo 'DuckDB cache pin mismatch.' >&2
	exit 1
}

# Go's CGO flag parser understands double-quoted arguments, including spaces.
escaped_libs="${libs//\\/\\\\}"
escaped_libs="${escaped_libs//\"/\\\"}"

export CGO_ENABLED=1
source "$root/scripts/go-build-tags.sh"
duckdb_build_tags "$@"
export CGO_CPPFLAGS="${CGO_CPPFLAGS:-} -DDUCKDB_STATIC_BUILD \"-I$escaped_libs\""
export CGO_LDFLAGS="${CGO_LDFLAGS:-} \"$escaped_libs/libcore_functions_extension.a\" \"$escaped_libs/libjson_extension.a\" \"$escaped_libs/libparquet_extension.a\" \"$escaped_libs/libicu_extension.a\" \"$escaped_libs/libduckdb_static.a\" $system_libs"
exec "${duckdb_go_args[@]}"
