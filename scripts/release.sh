#!/usr/bin/env bash
set -euo pipefail

# Change product maturity (alpha, beta, rc, or empty for GA) when the product graduates.
readonly PRODUCT_MATURITY=alpha

die() {
  printf 'release: %s\n' "$1" >&2
  exit 1
}

next_release_number() {
  local series=$1
  local tag
  local number=0
  local published

  while IFS= read -r tag; do
    [[ "$tag" =~ ^v([0-9]{4})\.([1-9]|1[0-2])\.(0|[1-9][0-9]*)(-(alpha|beta|rc))?$ ]] || continue
    [ "v${BASH_REMATCH[1]}.${BASH_REMATCH[2]}" = "$series" ] || continue
    published=${BASH_REMATCH[3]}
    if ((published >= number)); then
      number=$((published + 1))
    fi
  done
  printf '%s\n' "$number"
}

create_release_tag() {
  local repo=$1
  local head=$2
  local series=$3
  local number=$4
  local response
  local attempts=0
  local candidate

  while ((attempts < 1000)); do
    candidate="${series}.${number}${PRODUCT_MATURITY:+-${PRODUCT_MATURITY}}"
    if response=$(gh api --silent --method POST "repos/${repo}/git/refs" \
      -f "ref=refs/tags/${candidate}" -f "sha=${head}" 2>&1); then
      git fetch origin "refs/tags/${candidate}:refs/tags/${candidate}" >/dev/null
      printf 'release: created %s at %s\n' "$candidate" "$head" >&2
      printf '%s\n' "$candidate"
      return 0
    fi

    case "$response" in
    *"Reference update failed"*"HTTP 422"* | *"Reference already exists"*"HTTP 422"*)
      printf 'release: %s is unavailable; trying the next number\n' "$candidate" >&2
      number=$((number + 1))
      attempts=$((attempts + 1))
      ;;
    *)
      printf '%s\n' "$response" >&2
      return 1
      ;;
    esac
  done

  printf 'release: no available tag found after 1000 attempts\n' >&2
  return 1
}

main() {
  command -v gh >/dev/null 2>&1 || die "gh is required"
  [ "$(git rev-parse --abbrev-ref HEAD)" = "main" ] || die "run from main"
  [ -z "$(git status --porcelain)" ] || die "the worktree must be clean"

  git fetch origin --tags main
  local head_before
  local origin_before
  head_before=$(git rev-parse HEAD)
  origin_before=$(git rev-parse origin/main)
  [ "$head_before" = "$origin_before" ] || die "main is not up to date with origin/main"

  # A second tag while the prior release is running can race the moving `latest`
  # image and GitHub's latest-release selection.
  local active
  active=$(gh run list --workflow release.yml --limit 20 \
    --json status --jq '[.[] | select(.status != "completed")] | length')
  [ "$active" = "0" ] || die "another release workflow is still active"

  just check
  just test-race

  [ -z "$(git status --porcelain)" ] || die "verification changed the worktree"
  [ "$(git rev-parse HEAD)" = "$head_before" ] || die "HEAD changed during verification"
  git fetch origin --tags main
  [ "$(git rev-parse origin/main)" = "$origin_before" ] || die "origin/main changed during verification"

  local year
  local month
  local series
  local number
  local repo
  local created_tag
  year=$(date +%Y)
  month=$(date +%m)
  month=${month#0}
  series="v${year}.${month}"
  number=$(git tag --list "${series}.*" | next_release_number "$series")
  repo=$(gh repo view --json nameWithOwner --jq .nameWithOwner)

  printf 'release: searching from %s.%s%s at %s\n' \
    "$series" "$number" "${PRODUCT_MATURITY:+-${PRODUCT_MATURITY}}" "$head_before"
  created_tag=$(create_release_tag "$repo" "$head_before" "$series" "$number") ||
    die "could not create a release tag"
  printf 'release: %s is ready\n' "$created_tag"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
