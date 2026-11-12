#!/usr/bin/env bash
# Splits the Go packages into shards for CI's race suite.
#
# The slowest packages each run alone; "rest" holds every other package, so
# the shards cover `go list ./...` exactly by construction. A new package lands
# in "rest" without any change here.
#
#   go-race-shards.sh json      shard names as a JSON array, for a CI matrix
#   go-race-shards.sh <shard>   that shard's import paths, one per line
set -euo pipefail

# Measured on CI's 4-vCPU runner; each takes 5 to 7 minutes under -race.
readonly isolated=(pitotql query telemetry engine mcp api)

cd "$(dirname "$0")/.."

shard=${1:?usage: go-race-shards.sh json | <shard>}

if [ "$shard" = json ]; then
  printf '['
  for name in "${isolated[@]}"; do printf '"%s",' "$name"; done
  printf '"rest"]\n'
  exit 0
fi

module=$(go list -m)
# Through the wrapper, so the listing uses the same build tags as the tests
# and never fetches the dynamic library modules they do not use.
packages=$(bash scripts/with-duckdb.sh go list ./...)

if [ "$shard" = rest ]; then
  exclude=$(printf "${module}/internal/%s\n" "${isolated[@]}")
  grep -vxF -f <(printf '%s\n' "$exclude") <<<"$packages"
  exit 0
fi

for name in "${isolated[@]}"; do
  [ "$name" = "$shard" ] || continue
  path="${module}/internal/${name}"
  grep -qxF "$path" <<<"$packages" || {
    echo "go-race-shards: ${path} is not a package; update the isolated list" >&2
    exit 1
  }
  printf '%s\n' "$path"
  exit 0
done

echo "go-race-shards: unknown shard ${shard}" >&2
exit 1
