#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=scripts/release.sh
source "$(dirname "$0")/release.sh"

fail() {
  printf 'release_test: %s\n' "$1" >&2
  exit 1
}

assert_next_tag() (
  local name=$1
  local expected=$2
  local tags=$3
  local number
  local created

  gh() {
    [[ "$*" == *"ref=refs/tags/${expected} "* ]] || fail "$name: unexpected API call: $*"
  }

  git() {
    [ "$*" = "fetch origin refs/tags/${expected}:refs/tags/${expected}" ] ||
      fail "$name: unexpected fetch: $*"
  }

  number=$(next_release_number v2026.10 <<< "$tags")
  created=$(create_release_tag labstack/fanout deadbeef v2026.10 "$number")
  [ "$created" = "$expected" ] || fail "$name: created $created, want $expected"
)

test_next_release_tags() {
  assert_next_tag first-of-month v2026.10.0-alpha $'v2026.9.12-alpha\nv2025.10.4\nv2026.11.3-rc'
  assert_next_tag bare-tag v2026.10.1-alpha v2026.10.0
  assert_next_tag suffixed-tags v2026.10.3-alpha $'v2026.10.0-alpha\nv2026.10.1-alpha\nv2026.10.2-alpha'
  assert_next_tag mixed-suffixes v2026.10.5-alpha $'v2026.10.3-rc\nv2026.10.1-beta\nv2026.10.4\nv2026.10.2-alpha'
  assert_next_tag numeric-order v2026.10.11-alpha $'v2026.10.9-rc\nv2026.10.10-alpha\nv2026.10.2-beta'
  assert_next_tag malformed-tags v2026.10.1-alpha $'v2026.10.0\nv2026.10.01-alpha\nv2026.10.90-preview\nv2026.10.91-alpha.1\nv2026.10.92-ALPHA\nv2026.10.93-extra\nv2026.10.94.0\nv2026.010.95\nv2026x10.96\nv2026.10.-1\n2026.10.97\nv20260.10.98'
}

# Exercise the actual workflow block with local function stubs, without git writes or network.
workflow_validation=$(awk '
  /- name: Validate CalVer tag and lineage/ { step = 1; next }
  step && /^      - / { exit }
  step && /^          / { sub(/^          /, ""); print }
' "$(dirname "$0")/../.github/workflows/release.yml")
[ -n "$workflow_validation" ] || fail "workflow validation block not found"

assert_workflow_validation() (
  local ref=$1
  local tags=$2
  local expected_error=${3:-}
  local output
  local status=0

  # Invoked by the workflow block in the child Bash via export -f.
  # shellcheck disable=SC2329
  git() {
    case "$*" in
    'fetch origin main --tags' | 'merge-base --is-ancestor deadbeef origin/main') return 0 ;;
    'tag --list v*.*.*') printf '%s\n' "$tags" ;;
    *) fail "unexpected workflow git call: $*" ;;
    esac
  }

  export GITHUB_REF_NAME=$ref GITHUB_SHA=deadbeef
  export tags
  export -f git fail
  # A separate Bash preserves errexit behavior even when failure is expected.
  output=$(bash -c "$workflow_validation" 2>&1) || status=$?
  if [ -z "$expected_error" ]; then
    [ "$status" = 0 ] || fail "workflow rejected $ref: $output"
  else
    [ "$status" != 0 ] || fail "workflow accepted $ref, want $expected_error"
    [[ "$output" == *"$expected_error"* ]] || fail "workflow error for $ref: $output"
  fi
)

test_workflow_validation() {
  local suffix
  for suffix in '' -alpha -beta -rc; do
    assert_workflow_validation "v2026.10.10${suffix}" $'v2026.9.99-rc\nv2025.12.99\nv2026.10.9-rc\nv2026.10.100-preview'
  done
  assert_workflow_validation v2026.10.1-alpha $'v2026.10.0\nv2026.10.1-alpha'
  assert_workflow_validation v2026.10.9-rc v2026.10.10-alpha 'not the newest release tag'
  assert_workflow_validation v2026.9.99 v2026.10.0-alpha 'not the newest release tag'
  assert_workflow_validation v2025.12.99 v2026.1.0-alpha 'not the newest release tag'
  assert_workflow_validation v2026.10.1-alpha v2026.10.1 'shares the release number'
  assert_workflow_validation v2026.10.1-alpha v2026.10.1-beta 'shares the release number'
  for ref in v2026.10.01-alpha v2026.010.1 v2026.0.1 v2026.13.1 v2026.10.1-preview v2026.10.1-alpha.1; do
    assert_workflow_validation "$ref" '' 'release tag must match'
  done
}

test_reserved_tags_are_skipped() (
  local created

  gh() {
    case "$*" in
    *refs/tags/v2026.8.0-alpha* | *refs/tags/v2026.8.1-alpha*)
      printf 'gh: Reference update failed (HTTP 422)\n' >&2
      return 1
      ;;
    *refs/tags/v2026.8.2-alpha*) return 0 ;;
    *) return 64 ;;
    esac
  }

  git() {
    [ "$*" = "fetch origin refs/tags/v2026.8.2-alpha:refs/tags/v2026.8.2-alpha" ] ||
      fail "unexpected fetch: $*"
  }

  created=$(create_release_tag labstack/fanout deadbeef v2026.8 0)
  [ "$created" = "v2026.8.2-alpha" ] || fail "created $created, want v2026.8.2-alpha"
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

test_next_release_tags
test_workflow_validation
test_reserved_tags_are_skipped
test_unrelated_errors_are_not_retried
printf 'release_test: all tests passed\n'
