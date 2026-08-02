#!/usr/bin/env bash
# Compare two builds from one source archive: instrumentation disabled at link
# time versus enabled. The server is deleted on every exit path.
#
# Usage: scripts/bench-overhead-hetzner.sh [SERVER_TYPE] [SSH_KEY] [LOCATION] [OUTPUT_DIR]
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT" || exit 1

TYPE="${1:-cpx32}"
SSH_KEY="${2:-${HCLOUD_SSH_KEY:-}}"
LOCATION="${3:-fsn1}"
# Default to /tmp: benchmark output is diagnostic, often large, and must not be
# committed. Copy the summary you actually want to keep by hand.
OUTPUT_DIR="${4:-/tmp/fanout-overhead-$(date -u +%Y%m%dT%H%M%SZ)}"
GO_VERSION="${GO_VERSION:-1.26.5}"
SCREEN_WARMUP="${SCREEN_WARMUP:-1m}"
SCREEN_DURATION="${SCREEN_DURATION:-5m}"
NAME="fanout-overhead-$(date -u +%Y%m%d%H%M%S)-$$"

[ -n "$SSH_KEY" ] || { echo "SSH key required: pass as \$2 or set HCLOUD_SSH_KEY" >&2; exit 1; }
command -v hcloud >/dev/null || { echo "hcloud CLI required" >&2; exit 1; }
command -v ssh >/dev/null || { echo "ssh required" >&2; exit 1; }
command -v scp >/dev/null || { echo "scp required" >&2; exit 1; }
hcloud server-type describe "$TYPE" >/dev/null 2>&1 || { echo "unknown server type: $TYPE" >&2; exit 1; }
if [ -e "$OUTPUT_DIR" ]; then
  echo "output directory already exists: $OUTPUT_DIR" >&2
  exit 1
fi

PACKAGE_DIR=$(mktemp -d /tmp/fanout-linux-screen.XXXXXX)
SERVER_ID=""
cleanup() {
  if [ -n "$SERVER_ID" ]; then
    echo "── deleting Hetzner server $NAME ──"
    hcloud server delete "$SERVER_ID" >/dev/null 2>&1 || echo "WARNING: delete failed for server id $SERVER_ID" >&2
  fi
  rm -rf "$PACKAGE_DIR"
}
trap cleanup EXIT INT TERM

HEAD_SHA=$(git rev-parse HEAD)
# Include the current implementation and its new source directories, but never
# unrelated workspace data or generated output. Both A/B binaries use this
# exact archive, which makes measurement mode the only source-level variable.
COPYFILE_DISABLE=1 tar --no-xattrs -czf "$PACKAGE_DIR/fanout-src.tgz" cmd internal go.mod go.sum
SOURCE_SHA=$(shasum -a 256 "$PACKAGE_DIR/fanout-src.tgz" | awk '{print $1}')

echo "── provisioning $TYPE in $LOCATION ($NAME) ──"
CREATE_JSON=$(hcloud server create \
  --name "$NAME" \
  --type "$TYPE" \
  --image ubuntu-24.04 \
  --location "$LOCATION" \
  --ssh-key "$SSH_KEY" \
  --label purpose=fanout-bench \
  --label stage=measurement-overhead \
  -o json)
SERVER_ID=$(printf '%s' "$CREATE_JSON" | jq -r '.server.id')
SERVER_IP=$(printf '%s' "$CREATE_JSON" | jq -r '.server.public_net.ipv4.ip')
if [ -z "$SERVER_ID" ] || [ "$SERVER_ID" = null ] || [ -z "$SERVER_IP" ] || [ "$SERVER_IP" = null ]; then
  echo "could not resolve created server identity" >&2
  exit 1
fi
echo "  server id: $SERVER_ID | IPv4: $SERVER_IP"

SSH_OPTIONS=(-o ConnectTimeout=8 -o ServerAliveInterval=15 -o ServerAliveCountMax=4 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o BatchMode=yes)
ssh_vm() { ssh "${SSH_OPTIONS[@]}" "root@$SERVER_IP" "$@"; }
ready=0
for _ in $(seq 1 60); do
  if ssh_vm true 2>/dev/null; then
    ready=1
    break
  fi
  sleep 5
done
if [ "$ready" -ne 1 ]; then
  echo "SSH did not become ready" >&2
  exit 1
fi

echo "── installing Go $GO_VERSION and build tools ──"
ssh_vm "GO_VERSION='$GO_VERSION' bash -s" <<'REMOTE_SETUP'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null
apt-get install -y -qq build-essential curl ca-certificates >/dev/null 2>&1
cd /tmp
curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o go.tgz
rm -rf /usr/local/go
tar -C /usr/local -xzf go.tgz
/usr/local/go/bin/go version
REMOTE_SETUP

echo "── shipping and building same-source disabled/enabled pair ──"
scp "${SSH_OPTIONS[@]}" -q "$PACKAGE_DIR/fanout-src.tgz" "root@$SERVER_IP:/root/"
ssh_vm "HEAD_SHA='$HEAD_SHA' SOURCE_SHA='$SOURCE_SHA' bash -s" <<'REMOTE_BUILD'
set -euo pipefail
export PATH="/usr/local/go/bin:$PATH" CGO_ENABLED=1
mkdir -p /root/source /root/bin
tar -xzf /root/fanout-src.tgz -C /root/source
cd /root/source
go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=measurement-disabled-${SOURCE_SHA} -X github.com/labstack/fanout/internal/metrics.dataPlaneInstrumentation=disabled" -o /root/bin/fanout-control ./cmd/fanout
go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=measurement-enabled-${SOURCE_SHA}" -o /root/bin/fanout-instrumented ./cmd/fanout
go build -trimpath -buildvcs=false -ldflags "-s -w -X main.version=linux-screen-v2" -o /root/bin/bench ./cmd/bench
printf '%s  fanout-src.tgz\n' "$SOURCE_SHA" >/root/source-sha256.txt
REMOTE_BUILD

echo "── installing detached Linux overhead gate (warm-up $SCREEN_WARMUP, trials $SCREEN_DURATION) ──"
ssh_vm "tee /root/run-overhead-gate.sh >/dev/null" <<'REMOTE_BENCH'
#!/usr/bin/env bash
set -uo pipefail
export GOMAXPROCS=4
TYPE=${TYPE:?}
LOCATION=${LOCATION:?}
HEAD_SHA=${HEAD_SHA:?}
SOURCE_SHA=${SOURCE_SHA:?}
SCREEN_WARMUP=${SCREEN_WARMUP:?}
SCREEN_DURATION=${SCREEN_DURATION:?}
EVIDENCE=/root/evidence
mkdir -p "$EVIDENCE"
MEMORY_LIMIT_BYTES=$(awk '/MemTotal/ {printf "%.0f", $2 * 1024}' /proc/meminfo)
FANOUT_PID=0
stop_fanout() {
  if [ "$FANOUT_PID" -gt 0 ]; then
    kill -TERM "$FANOUT_PID" 2>/dev/null || true
    wait "$FANOUT_PID" 2>/dev/null || true
    FANOUT_PID=0
  fi
}
trap stop_fanout EXIT INT TERM

run_suite() {
  binary="$1"
  fanout_version="$2"
  suite_dir="$3"
  data_dir="$4"
  log_file="$5"

  env ENV=benchmark \
    HTTP_ADDR=127.0.0.1:7520 \
    OTLP_GRPC_ADDR=127.0.0.1:4317 \
    DATA_DIR="$data_dir" \
    FLUSH_SECONDS=15 \
    FLUSH_BATCH_SIZE=50000 \
    ROLLUP_EVERY=60 \
    DUCKLAKE_MERGE_EVERY_SECONDS=60 \
    DUCKLAKE_MAINTENANCE_EVERY_SECONDS=3600 \
    RETENTION_DAYS=30 \
    DUCKDB_MAX_CONNS=4 \
    DUCKDB_MEMORY=6GB \
    DUCKDB_THREADS=4 \
    MCP_ENABLED=false \
    ALERT_ENABLED=false \
    AUTH_MODE=local \
    SMTP_HOST=benchmark.invalid \
    SMTP_PORT=587 \
    SMTP_USER=benchmark \
    SMTP_PASS=benchmark-placeholder \
    SMTP_FROM=benchmark@invalid.example \
    AUTH_CODE_SECRET=benchmark-placeholder-secret-32-bytes-minimum \
    AI_PROVIDER=anthropic \
    AI_API_KEY=benchmark-placeholder \
    PUBLIC_INGEST=true \
    PUBLIC_READ=true \
    METRICS_PUBLIC=true \
    "$binary" >"$log_file" 2>&1 &
  FANOUT_PID=$!

  server_ready=0
  for _ in $(seq 1 90); do
    if curl -fsS --max-time 2 http://127.0.0.1:7520/healthz >/dev/null 2>&1; then
      server_ready=1
      break
    fi
    sleep 1
  done
  if [ "$server_ready" -ne 1 ]; then
    echo "Fanout readiness failed for $fanout_version" >&2
    tail -50 "$log_file" >&2
    stop_fanout
    return 1
  fi

  # One warm-up (discarded) then three measured trials. Comparing medians of
  # three is the whole protocol; there is no analyzer, because a verdict is only
  # meaningful once a same-binary null run on this host shows a small spread.
  mkdir -p "$suite_dir"
  bench_flags=(
    -endpoint 127.0.0.1:4317
    -rate 1000 -workers 8 -services 30 -namespaces 1
    -attr-cardinality 100 -error-rate 0.05 -messaging-ratio 0.2
    -logs=true -metrics=true
    -metrics-url http://127.0.0.1:7520/-/metrics
    -query-url http://127.0.0.1:7520 -query-workers 2 -query-rate 5
    -max-query-p95-ms 1500 -seed 42
    -fanout-version "$fanout_version"
  )
  suite_status=0
  /root/bin/bench "${bench_flags[@]}" -duration "$SCREEN_WARMUP" \
    -report "$suite_dir/warmup.json" || suite_status=1
  for ordinal in 1 2 3; do
    /root/bin/bench "${bench_flags[@]}" -duration "$SCREEN_DURATION" \
      -report "$(printf '%s/run-%02d.json' "$suite_dir" "$ordinal")" || suite_status=1
  done
  stop_fanout
  return "$suite_status"
}

status=0
run_suite /root/bin/fanout-control "measurement-disabled-${SOURCE_SHA}" "$EVIDENCE/control-suite" /root/control-data /root/control.log || status=1
run_suite /root/bin/fanout-instrumented "measurement-enabled-${SOURCE_SHA}" "$EVIDENCE/instrumented-suite" /root/instrumented-data /root/instrumented.log || status=1

# Median of the three measured trials per side, printed for eyeballing.
median_of() { # median_of <suite-dir> <jq-path>
  jq -s "map($2) | sort | .[1]" "$1"/run-*.json 2>/dev/null
}
{
  printf '%-28s %14s %14s\n' metric control instrumented
  for metric in \
    'avg_traces_per_sec:.avg_traces_per_sec' \
    'query_p95_ms:.query_latency_ms.p95_ms' \
    'cpu_cores:.server.cpu_cores' \
    'rss_bytes:.server.rss_bytes' \
    'alloc_bytes_per_sec:.server.alloc_bytes_per_sec'
  do
    printf '%-28s %14s %14s\n' "${metric%%:*}" \
      "$(median_of "$EVIDENCE/control-suite" "${metric#*:}")" \
      "$(median_of "$EVIDENCE/instrumented-suite" "${metric#*:}")"
  done
} | tee "$EVIDENCE/summary.txt"

{
  echo "server_type=$TYPE"
  echo "location=$LOCATION"
  echo "os=$(uname -srmo)"
  echo "cpu=$(nproc)"
  echo "memory_limit_bytes=$MEMORY_LIMIT_BYTES"
  echo "head_commit=$HEAD_SHA"
  echo "fanout_source_sha256=$SOURCE_SHA"
  echo "screen_warmup=$SCREEN_WARMUP"
  echo "screen_duration=$SCREEN_DURATION"
  /usr/local/go/bin/go version
} >"$EVIDENCE/host.txt"
cp /root/source-sha256.txt "$EVIDENCE/source-sha256.txt"
cp /root/control.log "$EVIDENCE/control.log" 2>/dev/null || true
cp /root/instrumented.log "$EVIDENCE/instrumented.log" 2>/dev/null || true
tar -czf /root/evidence.tgz -C "$EVIDENCE" .
printf '%s\n' "$status" >/root/bench.exit
exit "$status"
REMOTE_BENCH

ssh_vm "chmod 700 /root/run-overhead-gate.sh; rm -f /root/bench.exit /root/bench.pid /root/bench-run.log /root/evidence.tgz; nohup env TYPE='$TYPE' LOCATION='$LOCATION' HEAD_SHA='$HEAD_SHA' SOURCE_SHA='$SOURCE_SHA' SCREEN_WARMUP='$SCREEN_WARMUP' SCREEN_DURATION='$SCREEN_DURATION' /root/run-overhead-gate.sh >/root/bench-run.log 2>&1 </dev/null & printf '%s\\n' \$! >/root/bench.pid"

echo "── running detached Linux overhead gate ──"
REMOTE_STATUS=""
DEADLINE=$((SECONDS + 2700))
while [ "$SECONDS" -lt "$DEADLINE" ]; do
  POLL_OUTPUT=$(ssh_vm 'if test -f /root/bench.exit; then printf "DONE %s\n" "$(cat /root/bench.exit)"; elif test -f /root/bench.pid && kill -0 "$(cat /root/bench.pid)" 2>/dev/null; then tail -n 1 /root/bench-run.log; else echo LOST; fi' 2>/dev/null)
  POLL_STATUS=$?
  if [ "$POLL_STATUS" -ne 0 ]; then
    echo "  SSH poll unavailable; retrying without interrupting the remote gate"
  elif [[ "$POLL_OUTPUT" == DONE\ * ]]; then
    REMOTE_STATUS=${POLL_OUTPUT#DONE }
    echo "  remote gate completed with status $REMOTE_STATUS"
    break
  elif [ "$POLL_OUTPUT" = LOST ]; then
    echo "remote gate stopped without an exit marker" >&2
    REMOTE_STATUS=1
    break
  elif [ -n "$POLL_OUTPUT" ]; then
    echo "  $POLL_OUTPUT"
  fi
  sleep 20
done
if [ -z "$REMOTE_STATUS" ]; then
  echo "remote gate exceeded the 45-minute deadline" >&2
  REMOTE_STATUS=1
fi

mkdir -p "$OUTPUT_DIR"
COPIED=0
for _ in $(seq 1 12); do
  if scp "${SSH_OPTIONS[@]}" -q "root@$SERVER_IP:/root/evidence.tgz" "$PACKAGE_DIR/evidence.tgz"; then
    COPIED=1
    break
  fi
  sleep 5
done
if [ "$COPIED" -ne 1 ]; then
  echo "could not retrieve remote evidence" >&2
  exit 1
fi
tar -xzf "$PACKAGE_DIR/evidence.tgz" -C "$OUTPUT_DIR"
echo "evidence: $OUTPUT_DIR"
exit "$REMOTE_STATUS"
