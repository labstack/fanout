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

make_network() {
  hcloud network create --name "$RUN-net" --ip-range 10.10.0.0/16 >/dev/null
  hcloud network add-subnet "$RUN-net" --network-zone eu-central --type cloud --ip-range 10.10.1.0/24 >/dev/null
  register_network "$RUN-net"
}

# provision NAME TYPE → echoes "PUBLIC_IP PRIVATE_IP"
provision() {
  local name="$1" type="$2"
  hcloud server create --name "$name" --type "$type" --image ubuntu-24.04 \
    --location "$LOC" --ssh-key "$SSH_KEY" --network "$RUN-net" \
    --label purpose=fanout-fair-bench >/dev/null
  register_server "$name"
  local pub priv
  pub=$(hcloud server ip "$name")
  priv=$(hcloud server describe "$name" -o format='{{(index .PrivateNet 0).IP}}')
  echo "$pub $priv"
}

wait_ssh() { local ip="$1"; for _ in $(seq 1 40); do ssh_to "$ip" true 2>/dev/null && return 0; sleep 5; done; return 1; }

echo "── creating private network $RUN-net ──"
make_network
echo "── provisioning target ($TARGET_TYPE) + driver ($DRIVER_TYPE) ──"
read -r TARGET_PUB TARGET_PRIV < <(provision "$RUN-target" "$TARGET_TYPE")
read -r DRIVER_PUB DRIVER_PRIV < <(provision "$RUN-driver" "$DRIVER_TYPE")
echo "  target: pub=$TARGET_PUB priv=$TARGET_PRIV"
echo "  driver: pub=$DRIVER_PUB priv=$DRIVER_PRIV"
wait_ssh "$TARGET_PUB" || { echo "target SSH never came up" >&2; exit 1; }
wait_ssh "$DRIVER_PUB" || { echo "driver SSH never came up" >&2; exit 1; }
echo "✓ both VMs reachable"

setup_toolchain() {
  local ip="$1"
  ssh_to "$ip" "GOVER='$GOVER' bash -s" <<'REMOTE'
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null && apt-get install -y -qq build-essential git curl >/dev/null 2>&1
cd /tmp && curl -fsSL "https://go.dev/dl/go${GOVER}.linux-amd64.tar.gz" -o go.tgz
rm -rf /usr/local/go && tar -C /usr/local -xzf go.tgz
echo "  $(/usr/local/go/bin/go version) | $(nproc) cores, $(free -g | awk '/Mem/{print $2}')GB RAM"
REMOTE
}

# ship_and_build PUBLIC_IP ROLE   (ROLE = target | driver)
ship_and_build() {
  local ip="$1" role="$2"
  scp_to "$ip" /tmp/fanout-src.tgz /root/fanout-src.tgz
  ssh_to "$ip" "ROLE='$role' bash -s" <<'REMOTE'
set -e
export PATH=$PATH:/usr/local/go/bin CGO_ENABLED=1
mkdir -p /root/fanout && tar -xzf /root/fanout-src.tgz -C /root/fanout && cd /root/fanout
if [ "$ROLE" = "target" ]; then
  cat > .env <<'ENV'
JWT_SECRET=0123456789abcdef0123456789abcdef
JWT_REFRESH_SECRET=abcdef0123456789abcdef0123456789
SMTP_HOST=localhost
SMTP_USER=x
SMTP_PASS=x
SMTP_FROM=fanout@example.com
AI_API_KEY=dummy
AI_PROVIDER=anthropic
ENV
  go build -o bin/fanout ./cmd/fanout
else
  go build -o bin/loadgen ./cmd/loadgen
fi
echo "  built $ROLE binary"
REMOTE
}

echo "── installing toolchain on both VMs ──"
setup_toolchain "$TARGET_PUB"
setup_toolchain "$DRIVER_PUB"
echo "── shipping HEAD ($(git rev-parse --short HEAD)) + building ──"
git archive --format=tar.gz HEAD -o /tmp/fanout-src.tgz
ship_and_build "$TARGET_PUB" target
ship_and_build "$DRIVER_PUB" driver
rm -f /tmp/fanout-src.tgz
