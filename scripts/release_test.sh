#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=scripts/release.sh
source "$(dirname "$0")/release.sh"

fail() {
  printf 'release_test: %s\n' "$1" >&2
  exit 1
}

test_reserved_tags_are_skipped() (
  local created

  gh() {
    case "$*" in
    *refs/tags/v2026.8.0* | *refs/tags/v2026.8.1*)
      printf 'gh: Reference update failed (HTTP 422)\n' >&2
      return 1
      ;;
    *refs/tags/v2026.8.2*) return 0 ;;
    *) return 64 ;;
    esac
  }

  git() {
    [ "$*" = "fetch origin refs/tags/v2026.8.2:refs/tags/v2026.8.2" ] ||
      fail "unexpected fetch: $*"
  }

  created=$(create_release_tag labstack/fanout deadbeef v2026.8 0)
  [ "$created" = "v2026.8.2" ] || fail "created $created, want v2026.8.2"
)

test_unrelated_errors_are_not_retried() (
  gh() {
    printf 'gh: HTTP 403: Resource not accessible\n' >&2
    return 1
  }

  git() {
    fail "git called after API failure: $*"
  }

  if create_release_tag labstack/fanout deadbeef v2026.8 0 2>/dev/null; then
    fail "permission error unexpectedly succeeded"
  fi
)

test_reserved_tags_are_skipped
test_unrelated_errors_are_not_retried
printf 'release_test: all tests passed\n'
