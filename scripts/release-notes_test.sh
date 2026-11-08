#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=scripts/release-notes.sh
source "$(dirname "$0")/release-notes.sh"

fail() {
  printf 'release-notes_test: %s\n' "$1" >&2
  exit 1
}

contains() {
  [[ "$1" == *"$2"* ]] || fail "$3: output does not contain '$2':"$'\n'"$1"
}

lacks() {
  [[ "$1" != *"$2"* ]] || fail "$3: output unexpectedly contains '$2':"$'\n'"$1"
}

test_previous_release_tag() {
  local got
  got=$(previous_release_tag v2026.11.0-alpha <<< $'v2026.10.9-rc\nv2026.10.24-alpha\nv2026.10.100-preview\nv2026.11.0-alpha\nv2026.11.1-alpha\nv2025.12.99')
  [ "$got" = v2026.10.24-alpha ] || fail "previous of v2026.11.0-alpha = $got"
  got=$(previous_release_tag v2027.1.0 <<< $'v2026.12.3-rc\nv2026.9.40')
  [ "$got" = v2026.12.3-rc ] || fail "previous across a year = $got"
  got=$(previous_release_tag v2026.11.0-alpha <<< $'v2026.11.0-alpha\nnot-a-tag')
  [ -z "$got" ] || fail "first release has a previous tag: $got"
  if (previous_release_tag v2026.11.0-preview <<< '' 2>/dev/null); then
    fail "an invalid tag was accepted"
  fi
}

test_render() {
  local out
  out=$(render_upgrade_notes v2026.10.24-alpha 4 5 $'a.sql\nb.sql' 'feat!: rename a route (#1)')
  contains "$out" "**Format change: batch format 4 to 5.**" format-change
  contains "$out" "cannot read telemetry written by v2026.10.24-alpha" format-change
  contains "$out" "#when-a-release-changes-the-batch-format" format-change
  contains "$out" "2 new control database migrations run forward" migrations
  contains "$out" "> - \`b.sql\`" migrations
  contains "$out" "- feat!: rename a route (#1)" breaking
  lacks "$out" "**Storage:**" format-change

  out=$(render_upgrade_notes v2026.11.1-alpha 5 5 'a.sql' '')
  contains "$out" "1 new control database migration runs forward" one-migration
  lacks "$out" "Format change" one-migration
  lacks "$out" "Breaking changes" one-migration

  out=$(render_upgrade_notes v2026.11.1-alpha 5 5 '' '')
  contains "$out" "**Storage:** batch format 5 is unchanged and no control database migrations were added since v2026.11.1-alpha." unchanged

  out=$(render_upgrade_notes '' '' 5 '' '')
  contains "$out" "First release" first
}

# Builds a throwaway repository with two releases and runs the script end to end.
test_main() (
  local repo
  local out
  # Git hooks export GIT_DIR and friends; without clearing them, `git init`
  # below would reinitialize the repository running the hook.
  # shellcheck disable=SC2046 # One variable name per word.
  unset $(git rev-parse --local-env-vars)
  # Nor may a contributor's global or system configuration, hooks or templates
  # take part.
  export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
  repo=$(mktemp -d)
  trap 'rm -rf "$repo"' EXIT
  cd "$repo"
  if git rev-parse --git-dir >/dev/null 2>&1; then
    fail "the temporary directory is already inside a repository"
  fi
  g() { git -c user.name=test -c user.email=test@example.com -c commit.gpgsign=false -c tag.gpgsign=false "$@"; }
  g init -q
  mkdir -p internal/telemetry internal/db/migrations
  printf 'const (\n\tbatchMetadataVersion = 4\n)\n' > internal/telemetry/parquet.go
  printf -- '-- +goose Up\n' > internal/db/migrations/20260101000000_init.sql
  g add .
  g commit -qm 'feat: initial'
  g tag v2026.1.0-alpha
  printf 'const (\n\tbatchMetadataVersion = 5\n)\n' > internal/telemetry/parquet.go
  g commit -qam 'feat: batch format 5 (#2)'
  printf -- '-- +goose Up\n' > internal/db/migrations/20260201000000_next.sql
  g add .
  g commit -qm 'refactor(api)!: rename routes (#3)'
  g tag v2026.2.0-alpha
  g commit -q --allow-empty -m 'fix: unrelated (#4)'
  g tag v2026.2.1-alpha

  local published=$'v2026.1.0-alpha\nv2026.2.0-alpha\nv2026.2.1-alpha'
  out=$(main v2026.2.0-alpha <<< "$published")
  contains "$out" "**Format change: batch format 4 to 5.**" main-format
  contains "$out" "\`20260201000000_next.sql\`" main-migration
  lacks "$out" "20260101000000_init.sql" main-migration
  contains "$out" "- refactor(api)!: rename routes (#3)" main-breaking
  lacks "$out" "fix: unrelated" main-breaking

  out=$(main v2026.2.1-alpha <<< "$published")
  contains "$out" "**Storage:** batch format 5 is unchanged" main-unchanged

  out=$(main v2026.1.0-alpha <<< "$published")
  contains "$out" "First release" main-first

  # v2026.2.0-alpha was tagged but its release failed, so it was never
  # published: v2026.2.1-alpha must carry its format change and migration.
  out=$(main v2026.2.1-alpha <<< $'v2026.1.0-alpha\nv2026.2.1-alpha')
  contains "$out" "**Format change: batch format 4 to 5.**" main-unpublished
  contains "$out" "cannot read telemetry written by v2026.1.0-alpha" main-unpublished
  contains "$out" "\`20260201000000_next.sql\`" main-unpublished
  contains "$out" "- refactor(api)!: rename routes (#3)" main-unpublished
  lacks "$out" "**Storage:**" main-unpublished

  # A published tag that was not fetched must stop the release.
  if (main v2026.2.1-alpha <<< $'v2026.1.5-alpha\nv2026.2.1-alpha' >/dev/null 2>&1); then
    fail "a missing previous tag was accepted"
  fi

  # A renamed or missing constant must stop the release, not hide a format change.
  printf 'const (\n\tbatchFormat = 6\n)\n' > internal/telemetry/parquet.go
  g commit -qam 'refactor: rename the constant (#5)'
  g tag v2026.3.0-alpha
  if (main v2026.3.0-alpha <<< "$published" >/dev/null 2>&1); then
    fail "an unreadable format version was accepted"
  fi
)

test_previous_release_tag
test_render
test_main
printf 'release-notes_test: all tests passed\n'
