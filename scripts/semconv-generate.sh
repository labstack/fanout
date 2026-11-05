#!/usr/bin/env bash
# Regenerate internal/telemetry/semconv/keys_gen.go from the pinned semantic
# conventions tag, resolved by the weaver release that tag itself pins. Needs
# network access; builds and tests never run it.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tag=v1.44.0
archive_sha256=3afc6489d348fba740e0d3646c5cb91e47a057b4e4cef60701c8636d4330fde9
weaver=0.25.1
case "$(uname -s)/$(uname -m)" in
Darwin/arm64)
	target=aarch64-apple-darwin
	weaver_sha256=d9e0c301077648c83c22bd17d0bfc7688ec134085ec5f673c0db69f3052a9ec5
	;;
Darwin/x86_64)
	target=x86_64-apple-darwin
	weaver_sha256=4185ff7b57e9de46ad602df2412d3ada12d67be35d97e2c2f52007e302b7fc90
	;;
Linux/aarch64)
	target=aarch64-unknown-linux-gnu
	weaver_sha256=c304b535794f36ab718e73244acc27c127653e09354d5836291c8d99f8cecad5
	;;
Linux/x86_64)
	target=x86_64-unknown-linux-gnu
	weaver_sha256=a24f8fc17f120c3bca8ef540b907984a93875a4c4d5c9fc0604d1317be08b7bf
	;;
*)
	echo 'semconv generation requires macOS or Linux on arm64 or x86_64.' >&2
	exit 1
	;;
esac

sha256() {
	if command -v sha256sum >/dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | awk '{print $1}'
}

fetch() {
	curl --fail --location --silent --show-error --retry 3 "$1" -o "$2"
	[[ "$(sha256 "$2")" == "$3" ]] || {
		echo "Checksum mismatch: $1" >&2
		exit 1
	}
}

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fetch "https://github.com/open-telemetry/semantic-conventions/archive/refs/tags/$tag.tar.gz" "$work/semconv.tar.gz" "$archive_sha256"
fetch "https://github.com/open-telemetry/weaver/releases/download/v$weaver/weaver-$target.tar.xz" "$work/weaver.tar.xz" "$weaver_sha256"
mkdir "$work/semconv" "$work/weaver"
tar -xzf "$work/semconv.tar.gz" -C "$work/semconv" --strip-components 1
tar -xJf "$work/weaver.tar.xz" -C "$work/weaver" --strip-components 1
[[ "$("$work/weaver/weaver" --version)" == "weaver $weaver" ]] || {
	echo "Unexpected weaver version" >&2
	exit 1
}
(cd "$work/semconv" && "$work/weaver/weaver" registry resolve --registry model --format json --skip-policies --quiet --output "$work/resolved.json")
cd "$root"
go run ./internal/cmd/semconvgen -in "$work/resolved.json" -tag "$tag" -weaver "$weaver" -sha256 "$archive_sha256"
