#!/usr/bin/env bash
set -euo pipefail

# Writes the "Upgrade notes" section that release.yml puts above GitHub's
# generated changelog. It compares the tag with the previous published release
# and marks, from the source rather than from memory:
#
# - a batch format change, which the new release cannot read across, so stored
#   telemetry must be moved aside (internal/telemetry batchMetadataVersion);
# - new control database migrations, which run forward at startup, so rolling
#   back means restoring a backup (internal/db/migrations);
# - breaking changes, from Conventional Commit subjects marked with `!`.
#
# Every release states its storage impact, so a missing marker is never
# mistaken for "no change". The script fails rather than guessing when the
# format version cannot be read.
#
# Usage: release-notes.sh <tag> < published-tags
#
# stdin lists the tags of published (non-draft) GitHub Releases, one per line.
# Git tags alone are not enough: a tag whose release workflow failed has no
# release, and comparing with it would hide a change it introduced.

readonly RELEASE_TAG_PATTERN='^v([0-9]{4})\.([1-9]|1[0-2])\.(0|[1-9][0-9]*)(-(alpha|beta|rc))?$'
readonly FORMAT_SOURCE=internal/telemetry/parquet.go
readonly MIGRATIONS_DIR=internal/db/migrations
readonly UPGRADE_GUIDE=https://pitot.dev/docs/guides/upgrade

die() {
  printf 'release-notes: %s\n' "$1" >&2
  exit 1
}

# Reads published release tags on stdin and prints the newest one older than $1.
previous_release_tag() {
  local current=$1
  local tag
  local best=""
  local best_key=-1
  local key
  local current_key

  [[ "$current" =~ $RELEASE_TAG_PATTERN ]] || die "release tag must match v{YYYY}.{M}.{N}[-alpha|-beta|-rc]: $current"
  current_key=$(((10#${BASH_REMATCH[1]} * 100 + BASH_REMATCH[2]) * 1000000 + BASH_REMATCH[3]))
  while IFS= read -r tag; do
    [[ "$tag" =~ $RELEASE_TAG_PATTERN ]] || continue
    key=$(((10#${BASH_REMATCH[1]} * 100 + BASH_REMATCH[2]) * 1000000 + BASH_REMATCH[3]))
    if ((key < current_key && key > best_key)); then
      best=$tag
      best_key=$key
    fi
  done
  printf '%s\n' "$best"
}

# Prints the batch format version declared at a revision.
batch_format_at() {
  local rev=$1
  local version
  version=$(git show "${rev}:${FORMAT_SOURCE}" 2>/dev/null |
    sed -n 's/^[[:space:]]*batchMetadataVersion[[:space:]]*=[[:space:]]*\([0-9][0-9]*\)[[:space:]]*$/\1/p')
  [[ "$version" =~ ^[0-9]+$ ]] || die "cannot read batchMetadataVersion from ${FORMAT_SOURCE} at ${rev}"
  printf '%s\n' "$version"
}

# Prints the control migrations added between two revisions, one per line.
added_migrations() {
  git diff --name-only --diff-filter=A "$1" "$2" -- "${MIGRATIONS_DIR}/*.sql" |
    sed 's|.*/||'
}

# Prints commit subjects marked as breaking (`type!:` or `type(scope)!:`).
breaking_subjects() {
  git log --format=%s "$1..$2" | grep -E '^[a-z]+(\([^)]*\))?!:' || true
}

# Renders the section from already-collected facts so it can be tested
# without a repository.
render_upgrade_notes() {
  local previous=$1
  local old_format=$2
  local new_format=$3
  local migrations=$4
  local breaking=$5
  local count

  printf '## Upgrade notes\n\n'
  if [ -z "$previous" ]; then
    printf 'First release: there is no earlier release to compare storage formats with.\n'
    return 0
  fi

  if [ "$old_format" != "$new_format" ]; then
    printf '> [!CAUTION]\n'
    printf '> **Format change: batch format %s to %s.** This release cannot read telemetry written by %s. ' "$old_format" "$new_format" "$previous"
    printf 'Stored telemetry must be moved out of the data directory before starting it. '
    printf 'Follow [when a release changes the batch format](%s#when-a-release-changes-the-batch-format).\n\n' "$UPGRADE_GUIDE"
  fi

  if [ -n "$migrations" ]; then
    count=$(printf '%s\n' "$migrations" | wc -l | tr -d ' ')
    printf '> [!IMPORTANT]\n'
    if [ "$count" = 1 ]; then
      printf '> **Control schema migration.** 1 new control database migration runs forward at startup. '
    else
      printf '> **Control schema migration.** %s new control database migrations run forward at startup. ' "$count"
    fi
    printf 'Take a cold backup first: rolling back means restoring that backup with %s. ' "$previous"
    printf 'See [upgrade an instance](%s#upgrade-an-instance).\n>\n' "$UPGRADE_GUIDE"
    # shellcheck disable=SC2016 # Literal Markdown backticks.
    printf '%s\n' "$migrations" | sed 's/^/> - `/; s/$/`/'
    printf '\n'
  fi

  if [ "$old_format" = "$new_format" ] && [ -z "$migrations" ]; then
    printf '**Storage:** batch format %s is unchanged and no control database migrations were added since %s.\n\n' "$new_format" "$previous"
  fi

  if [ -n "$breaking" ]; then
    printf '### Breaking changes\n\n'
    printf '%s\n' "$breaking" | sed 's/^/- /'
    printf '\n'
  fi
}

main() {
  local tag=${1:-}
  local previous
  local old_format=""
  local new_format
  local migrations=""
  local breaking=""

  [ -n "$tag" ] || die "usage: release-notes.sh <tag> < published-tags"
  git rev-parse --verify --quiet "refs/tags/${tag}" >/dev/null || die "tag not found: $tag"
  previous=$(previous_release_tag "$tag") || exit 1
  if [ -n "$previous" ]; then
    git rev-parse --verify --quiet "refs/tags/${previous}" >/dev/null ||
      die "previous release tag is not fetched: $previous"
  else
    # "First release" is only true when no older release tag exists at all. An
    # empty or truncated release list must not hide every earlier change.
    local older
    older=$(git tag --list 'v*.*.*' | previous_release_tag "$tag") || exit 1
    [ -z "$older" ] || die "no published release precedes $tag, but tag $older does; check the release list"
  fi
  new_format=$(batch_format_at "$tag") || exit 1
  if [ -n "$previous" ]; then
    old_format=$(batch_format_at "$previous") || exit 1
    migrations=$(added_migrations "$previous" "$tag") || exit 1
    breaking=$(breaking_subjects "$previous" "$tag") || exit 1
  fi
  render_upgrade_notes "$previous" "$old_format" "$new_format" "$migrations" "$breaking"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
