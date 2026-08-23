#!/usr/bin/env bash
set -euo pipefail

die() {
  printf 'release: %s\n' "$1" >&2
  exit 1
}

command -v gh >/dev/null 2>&1 || die "gh is required"
[ "$(git rev-parse --abbrev-ref HEAD)" = "main" ] || die "run from main"
[ -z "$(git status --porcelain)" ] || die "the worktree must be clean"

git fetch origin --tags main
head_before=$(git rev-parse HEAD)
origin_before=$(git rev-parse origin/main)
[ "$head_before" = "$origin_before" ] || die "main is not up to date with origin/main"

# A second tag while the prior release is running can race the moving `latest`
# image and GitHub's latest-release selection.
active=$(gh run list --workflow release.yml --limit 20 \
  --json status --jq '[.[] | select(.status != "completed")] | length')
[ "$active" = "0" ] || die "another release workflow is still active"

just check
just test-race

[ -z "$(git status --porcelain)" ] || die "verification changed the worktree"
[ "$(git rev-parse HEAD)" = "$head_before" ] || die "HEAD changed during verification"
git fetch origin --tags main
[ "$(git rev-parse origin/main)" = "$origin_before" ] || die "origin/main changed during verification"

year=$(date +%Y)
month=$(date +%m)
month=${month#0}
series="v${year}.${month}"
last=$(git tag --list "${series}.*" --sort=-v:refname | grep -E "^${series}\.[0-9]+$" | head -1 || true)
if [ -z "$last" ]; then
  number=0
else
  number=$(( ${last##*.} + 1 ))
fi
tag="${series}.${number}"

printf 'release: %s -> %s at %s\n' "${last:-<first this month>}" "$tag" "$head_before"
git tag "$tag"
if ! git push origin "$tag"; then
  git tag -d "$tag" >/dev/null
  die "push failed; removed local tag"
fi
