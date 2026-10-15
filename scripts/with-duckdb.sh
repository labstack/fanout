#!/usr/bin/env bash
# Use the same pinned engine and headers for every local/CI/release Go command.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version=v2.0.0-alpha46057
case "$(uname -s)/$(uname -m)" in
Darwin/arm64)
	platform=osx-arm64
	checksum=e78e6a3ab5c35c1451650cdd234e9f42c8c9485412aaf063dfaae995f0dcda6a
	system_libs='-lc++ -Wl,-no_warn_duplicate_libraries'
	;;
Darwin/x86_64)
	platform=osx-amd64
	checksum=a1dd2b83754484dcd0980ed1c46d5726ec2a2fd26090d43341dc74f1060abff3
	system_libs='-lc++ -Wl,-no_warn_duplicate_libraries'
	;;
Linux/x86_64)
	platform=linux-amd64
	checksum=4cb2df2e767f2a08fb38a0c39b8dadd1ad0eb07fd5cd6431092e709be41a323e
	system_libs='-lstdc++ -lm -ldl -pthread'
	;;
Linux/aarch64)
	platform=linux-arm64
	checksum=81cf834f18a7004d412862d25bbbd334c1e335432c94a2cbcb6c0f560ebbc3eb
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
			url="https://github.com/labstack/fanout/releases/download/duckdb-$version/duckdb-static-libs-$platform.tar.gz"
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
