#!/bin/sh
# Install Fanout, a single-binary observability server.
#
#   curl -fsSL https://raw.githubusercontent.com/labstack/fanout/main/scripts/install.sh | sh
#
# Environment:
#   FANOUT_VERSION  install this tag instead of the latest release (e.g. v2026.08.1)
#   FANOUT_PREFIX   install into this directory instead of the default
set -eu

REPO="labstack/fanout"
BIN="fanout"

die() {
	printf 'fanout: %s\n' "$1" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

need uname
need tar
need mktemp
need sed
need head
need grep

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
	fetch_stdout() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO "$2" "$1"; }
	fetch_stdout() { wget -qO- "$1"; }
else
	die "curl or wget is required"
fi

os="$(uname -s)"
case "$os" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "unsupported operating system: $os (Linux and macOS are supported)" ;;
esac

arch="$(uname -m)"
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "unsupported architecture: $arch (amd64 and arm64 are supported)" ;;
esac

version="${FANOUT_VERSION:-}"
if [ -z "$version" ]; then
	version="$(fetch_stdout "https://api.github.com/repos/${REPO}/releases/latest" |
		sed -n 's/.*"tag_name" *: *"\([^"]*\)".*/\1/p' | head -n 1)"
	[ -n "$version" ] || die "could not resolve the latest release; set FANOUT_VERSION to pin one"
fi

archive="${BIN}_${version}_${os}_${arch}.tar.gz"
base="https://github.com/${REPO}/releases/download/${version}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

printf 'fanout: downloading %s %s/%s\n' "$version" "$os" "$arch"
fetch "${base}/${archive}" "${tmp}/${archive}" ||
	die "no release asset ${archive} — check https://github.com/${REPO}/releases"
fetch "${base}/SHA256SUMS" "${tmp}/SHA256SUMS" || die "could not download SHA256SUMS"

# Verify before extracting, never after.
(
	cd "$tmp"
	grep " ${archive}\$" SHA256SUMS > expected.sha256 ||
		die "SHA256SUMS does not list ${archive}"
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum -c expected.sha256 >/dev/null
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 -c expected.sha256 >/dev/null
	else
		die "sha256sum or shasum is required to verify the download"
	fi
) || die "checksum verification failed for ${archive}"

tar -xzf "${tmp}/${archive}" -C "$tmp" "$BIN" || die "archive did not contain ${BIN}"
chmod +x "${tmp}/${BIN}"

prefix="${FANOUT_PREFIX:-}"
if [ -z "$prefix" ]; then
	if [ -w /usr/local/bin ] 2>/dev/null; then
		prefix=/usr/local/bin
	else
		prefix="${HOME}/.local/bin"
	fi
fi
mkdir -p "$prefix" || die "could not create ${prefix}"
[ -w "$prefix" ] || die "${prefix} is not writable; set FANOUT_PREFIX or re-run with sudo"

mv "${tmp}/${BIN}" "${prefix}/${BIN}" || die "could not install into ${prefix}"

printf 'fanout: %s installed to %s/%s\n' "$version" "$prefix" "$BIN"
# The $PATH in the hint below is deliberately literal: it is text to paste.
# shellcheck disable=SC2016
case ":${PATH}:" in
*":${prefix}:"*) ;;
*) printf 'fanout: %s is not on your PATH — add it with:\n  export PATH="%s:$PATH"\n' "$prefix" "$prefix" ;;
esac
printf 'fanout: next, see https://github.com/labstack/fanout#quick-start\n'
