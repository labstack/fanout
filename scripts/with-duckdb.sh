#!/usr/bin/env bash
# Use the same pinned engine and headers for every local/CI/release Go command.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version=v2.0.0-alpha43763
commit=96063b9e39
case "$(uname -s)/$(uname -m)" in
Darwin/arm64)
	platform=osx-arm64
	checksum=42320cd312d1a55f2fbe2e527b4f95aed6e0b9ee77e64092fa2a5b0879024206
	system_libs='-lc++ -Wl,-no_warn_duplicate_libraries'
	;;
Darwin/x86_64)
	platform=osx-amd64
	checksum=c13adabde6b7aac99281d745da2bdfad0c482b306e1b3d3d7391ef806e967368
	system_libs='-lc++ -Wl,-no_warn_duplicate_libraries'
	;;
Linux/x86_64)
	platform=linux-amd64
	checksum=5d62c5bca5474722e172d19c51ba4c7b3711444b70f6478b3b38b94bfb5681b2
	system_libs='-lstdc++ -lm -ldl -pthread'
	;;
Linux/aarch64)
	platform=linux-arm64
	checksum=af0ed15848a76a013764419b4f47020ae0aa7e71396f2365aa61a95ecba456ee
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
			url="https://duckdb-staging.duckdb.org/$commit/$version/duckdb/duckdb/github_release/duckdb-static-libs-$platform.tar.gz"
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
export GOFLAGS="${GOFLAGS:-} -tags=duckdb_use_static_lib"
export CGO_CPPFLAGS="${CGO_CPPFLAGS:-} -DDUCKDB_STATIC_BUILD \"-I$escaped_libs\""
export CGO_LDFLAGS="${CGO_LDFLAGS:-} \"$escaped_libs/libcore_functions_extension.a\" \"$escaped_libs/libjson_extension.a\" \"$escaped_libs/libparquet_extension.a\" \"$escaped_libs/libicu_extension.a\" \"$escaped_libs/libduckdb_static.a\" $system_libs"
exec "$@"
