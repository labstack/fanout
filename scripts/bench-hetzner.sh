#!/usr/bin/env bash
# Provision a Hetzner VM (default cpx32 — the reference target), build the
# current branch on it, run the ingest stress test, and tear the VM down.
# The VM is ALWAYS deleted on exit (trap), including on failure or Ctrl-C.
#
# Usage:  scripts/bench-hetzner.sh [SERVER_TYPE] [SSH_KEY] [LOCATION]
# Example: scripts/bench-hetzner.sh cpx32 v@labstack.com fsn1
#
# Requires: hcloud CLI with an authenticated context, an uploaded SSH key whose
# private key is loaded in your agent, and a clean git tree (HEAD is shipped).
set -uo pipefail
cd "$(dirname "$0")/.."

TYPE="${1:-cpx32}"
SSH_KEY="${2:-v@labstack.com}"
LOC="${3:-fsn1}"
NAME="fanout-bench-$$"
GOVER="1.26.4"

command -v hcloud >/dev/null || { echo "hcloud CLI required" >&2; exit 1; }
hcloud server-type describe "$TYPE" >/dev/null 2>&1 || { echo "unknown server type: $TYPE" >&2; exit 1; }

CREATED=""
cleanup() {
  if [ -n "$CREATED" ]; then
    echo "── tearing down $NAME ──"
    hcloud server delete "$NAME" >/dev/null 2>&1 && echo "✓ $NAME deleted" || echo "⚠ delete $NAME FAILED — remove it manually!"
  fi
}
trap cleanup EXIT INT TERM

echo "── provisioning $TYPE ($NAME) in $LOC ──"
hcloud server create --name "$NAME" --type "$TYPE" --image ubuntu-24.04 \
  --location "$LOC" --ssh-key "$SSH_KEY" --label purpose=fanout-bench >/dev/null
CREATED=1
IP=$(hcloud server ip "$NAME")
echo "  IP: $IP"

ssh_vm() { ssh -o ConnectTimeout=8 -o StrictHostKeyChecking=accept-new -o BatchMode=yes "root@$IP" "$@"; }
for _ in $(seq 1 40); do ssh_vm true 2>/dev/null && break; sleep 5; done

echo "── installing toolchain (gcc + Go $GOVER) ──"
ssh_vm "bash -s" <<REMOTE
set -e
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null && apt-get install -y -qq build-essential git curl >/dev/null 2>&1
cd /tmp && curl -fsSL https://go.dev/dl/go${GOVER}.linux-amd64.tar.gz -o go.tgz
rm -rf /usr/local/go && tar -C /usr/local -xzf go.tgz
echo "  $(/usr/local/go/bin/go version) | $(nproc) cores, $(free -g | awk '/Mem/{print \$2}')GB RAM"
REMOTE

echo "── shipping current branch ($(git rev-parse --short HEAD)) + building ──"
git archive --format=tar.gz HEAD -o /tmp/fanout-src.tgz
scp -q -o BatchMode=yes /tmp/fanout-src.tgz "root@$IP:/root/"
rm -f /tmp/fanout-src.tgz
ssh_vm "bash -s" <<'REMOTE'
set -e
export PATH=$PATH:/usr/local/go/bin CGO_ENABLED=1
mkdir -p /root/fanout && tar -xzf /root/fanout-src.tgz -C /root/fanout && cd /root/fanout
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
go build -o bin/fanout ./cmd/fanout && go build -o bin/loadgen ./cmd/loadgen
REMOTE

echo "── stress test (self-sizing DuckDB defaults) ──"
ssh_vm "bash -s" <<'REMOTE'
set -uo pipefail
cd /root/fanout
set -a; . ./.env; set +a
DATA_DIR=/root/fanout/data PUBLIC_READ=true OTLP_GRPC_ADDR=:4317 HTTP_ADDR=:7520 ENV=development \
  FLUSH_SECONDS=5 ROLLUP_EVERY=15 ./bin/fanout >/root/fanout/f.log 2>&1 &
FPID=$!
for i in $(seq 1 40); do curl -fsS -m2 localhost:7520/healthz >/dev/null 2>&1 && break; sleep 1; done
snap(){ curl -s -m3 localhost:7520/-/metrics | awk -v k="$1" '$0 ~ "^"k {s+=$2} END{printf "%d",s+0}'; }
./bin/loadgen -endpoint localhost:4317 -duration 8s -rate 5000 -workers 8 -services 30 >/dev/null 2>&1
rows0=$(snap fanout_ingest_rows_total); t0=$(date +%s)
pids=()
for n in 1 2; do ./bin/loadgen -endpoint localhost:4317 -duration 30s -rate 15000 -workers 12 -services 50 -messaging-ratio 0.15 >/root/fanout/lg$n.log 2>&1 & pids+=($!); done
wait "${pids[@]}"
t1=$(date +%s); rows1=$(snap fanout_ingest_rows_total); dt=$((t1-t0))
echo
echo "rows accepted   : $((rows1-rows0)) in ${dt}s = $(( (rows1-rows0)/dt )) rows/s"
echo "drops           : $(snap fanout_rows_dropped_total)"
echo "lake_partitions : $(snap fanout_lake_partitions) files | $(( $(snap fanout_lake_size_bytes)/1048576 )) MB"
echo "rollups         : $(curl -s -m3 localhost:7520/-/metrics | awk '/rollup_duration_seconds_count/{c=$2}/rollup_duration_seconds_sum/{s=$2} END{printf "%d done, avg %.0fms",c,(c>0?s/c*1000:0)}')"
echo "fanout health   : OOM=$(grep -ciE 'out of memory' f.log) ERR=$(grep -cE 'level\":\"ERROR' f.log) RSS=$(ps -o rss= -p $FPID | awk '{printf "%.2f GB",$1/1024/1024}')"
kill $FPID 2>/dev/null
REMOTE
# trap deletes the VM
