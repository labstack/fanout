#!/usr/bin/env bash
# Exits 0 when CI's push run on main for exactly this commit succeeded,
# including its Gate job; exits 1 otherwise.
#
# A release tag can only point at a main commit, and main only advances
# through pull requests whose Gate passed, so a successful push run on the
# same commit verified the same tree the release builds. The release reuses
# that result instead of running the suites again.
#
#   ci-passed.sh [--wait] <sha>
#
# --wait follows a run that is still in progress instead of failing.
set -euo pipefail

wait=0
if [ "${1:-}" = --wait ]; then
  wait=1
  shift
fi
sha=${1:?usage: ci-passed.sh [--wait] <sha>}
[[ "$sha" =~ ^[0-9a-f]{40}$ ]] || {
  echo "ci-passed: ${sha} is not a full commit SHA" >&2
  exit 1
}
repo=${GITHUB_REPOSITORY:-$(gh repo view --json nameWithOwner --jq .nameWithOwner)}

# Seconds to wait for CI to register the push run before giving up.
registration=300

while :; do
  runs=$(gh api "repos/${repo}/actions/workflows/ci.yml/runs?head_sha=${sha}&event=push&branch=main&per_page=20" \
    --jq ".workflow_runs[] | select(.head_sha == \"${sha}\" and .head_branch == \"main\" and .event == \"push\") | \"\(.id) \(.status) \(.conclusion)\"")

  passed=$(awk '$2 == "completed" && $3 == "success" { print $1; exit }' <<<"$runs")
  if [ -n "$passed" ]; then
    gate=$(gh api "repos/${repo}/actions/runs/${passed}/jobs?per_page=100" \
      --jq '[.jobs[] | select(.name == "Gate") | .conclusion] | join(" ")')
    if [ "$gate" = success ]; then
      echo "ci-passed: CI run ${passed} passed for ${sha}" >&2
      exit 0
    fi
    echo "ci-passed: CI run ${passed} succeeded but its Gate reads '${gate}'" >&2
    exit 1
  fi

  pending=$(awk '$2 != "completed" { print $1; exit }' <<<"$runs")
  if [ "$wait" = 1 ] && [ -n "$pending" ]; then
    echo "ci-passed: waiting for CI run ${pending} on ${sha}" >&2
    # A watch that fails at once (an API error) must not turn into a busy loop.
    gh run watch "$pending" --repo "$repo" --interval 30 >/dev/null || sleep 30
    continue
  fi
  # Just after a push, CI may not have registered the run yet.
  if [ "$wait" = 1 ] && [ -z "$runs" ] && ((registration > 0)); then
    echo "ci-passed: waiting for CI to start on ${sha}" >&2
    sleep 15
    registration=$((registration - 15))
    continue
  fi

  echo "ci-passed: no successful CI push run on main for ${sha}" >&2
  exit 1
done
