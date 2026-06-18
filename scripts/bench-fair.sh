#!/usr/bin/env bash
# Fair throughput benchmark: provisions a Hetzner private network + two VMs
# (a cpx32 fanout-under-test and a larger cpx41 load driver), ships the current
# git HEAD, ramps loadgen through rate steps under an SLO gate to find the
# ingest CEILING, then certifies a sustained RATED CAPACITY with a 15-min soak.
# Both numbers are reported in achieved server-side rows/s. All cloud resources
# are deleted on exit (trap), including on failure or Ctrl-C.
#
# Usage:  scripts/bench-fair.sh [TARGET_TYPE] [DRIVER_TYPE] [SSH_KEY] [LOCATION]
# Example: scripts/bench-fair.sh cpx32 cpx41 v@labstack.com fsn1
# Env:    PART_CAP (max allowed lake_partitions, default 800)
#
# Requires: hcloud CLI with an authenticated context, an uploaded SSH key whose
# private key is a default identity (or loaded in the agent), and a clean build
# of HEAD (HEAD is shipped via git archive).
set -uo pipefail
# shellcheck disable=SC2164
cd "$(dirname "$0")/.."

TARGET_TYPE="${1:-cpx32}"
DRIVER_TYPE="${2:-cpx41}"
# shellcheck disable=SC2034
SSH_KEY="${3:-v@labstack.com}"
LOC="${4:-fsn1}"
# shellcheck disable=SC2034
GOVER="1.26.4"
PART_CAP="${PART_CAP:-800}"
RUN="fanout-fair-$$"

command -v hcloud >/dev/null || { echo "hcloud CLI required" >&2; exit 1; }

SERVERS=()
NETWORKS=()
register_server()  { SERVERS+=("$1"); }
register_network() { NETWORKS+=("$1"); }

cleanup() {
  for s in "${SERVERS[@]:-}"; do
    [ -n "$s" ] || continue
    echo "── deleting server $s ──"
    hcloud server delete "$s" >/dev/null 2>&1 && echo "✓ $s deleted" || echo "⚠ delete server $s FAILED — remove it manually!"
  done
  for n in "${NETWORKS[@]:-}"; do
    [ -n "$n" ] || continue
    echo "── deleting network $n ──"
    hcloud network delete "$n" >/dev/null 2>&1 && echo "✓ $n deleted" || echo "⚠ delete network $n FAILED — remove it manually!"
  done
}
trap cleanup EXIT INT TERM

SSHOPTS=(-o ConnectTimeout=8 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o BatchMode=yes)
# shellcheck disable=SC2029
ssh_to()  { local ip="$1"; shift; ssh "${SSHOPTS[@]}" "root@$ip" "$@"; }
scp_to()  { local ip="$1" src="$2" dst="$3"; scp "${SSHOPTS[@]}" -q "$src" "root@$ip:$dst"; }

echo "fair-bench run $RUN | target=$TARGET_TYPE driver=$DRIVER_TYPE loc=$LOC part_cap=$PART_CAP"
