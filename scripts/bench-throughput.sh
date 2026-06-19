#!/usr/bin/env bash
# Throughput benchmark: provisions a Hetzner private network + two VMs of the
# SAME type — a fanout-under-test and a separate load driver — ships the current
# git HEAD, ramps loadgen through rate steps under an SLO gate to find the
# ingest CEILING, then certifies a sustained RATED CAPACITY with a 15-min soak.
# Both numbers are reported in achieved server-side rows/s. All cloud resources
# are deleted on exit (trap), including on failure or Ctrl-C.
#
# Usage:  scripts/bench-throughput.sh [TYPE] [SSH_KEY] [LOCATION]
# Example: scripts/bench-throughput.sh cpx32 hetzner fsn1
# Env:    PART_CAP (max allowed lake_partitions, default 800)
#
# Requires: hcloud CLI with an authenticated context; a PASSPHRASELESS ssh key
# at $SSH_IDENTITY (default ~/.ssh/hetzner) whose public half is uploaded to
# Hetzner as $SSH_KEY (default "hetzner") — the agent is bypassed, so a
# passphrase-protected key will NOT work. HEAD is shipped via git archive.
set -uo pipefail
# shellcheck disable=SC2164
cd "$(dirname "$0")/.."

TYPE="${1:-cpx32}"   # both VMs use the same instance type
# shellcheck disable=SC2034
SSH_KEY="${2:-hetzner}"               # Hetzner-side key name injected into the VMs
LOC="${3:-fsn1}"
# Dedicated PASSPHRASELESS identity used for ssh auth. A 40-minute run makes
# hundreds of signing requests; the macOS launchd ssh-agent wedges under that
# load ("agent refused operation"), so we sign from this on-disk key directly
# and disable the agent entirely (see SSHOPTS below).
SSH_IDENTITY="${SSH_IDENTITY:-$HOME/.ssh/hetzner}"
# shellcheck disable=SC2034
GOVER="1.26.4"
PART_CAP="${PART_CAP:-800}"
RUN="fanout-tput-$$"
HEAD_SHA="$(git rev-parse --short HEAD)"   # captured at launch — this is what git archive ships

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

SSHOPTS=(-o ConnectTimeout=8 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o BatchMode=yes -o IdentitiesOnly=yes -o IdentityAgent=none -i "$SSH_IDENTITY")
# shellcheck disable=SC2029
ssh_to()  { local ip="$1"; shift; ssh "${SSHOPTS[@]}" "root@$ip" "$@"; }
scp_to()  { local ip="$1" src="$2" dst="$3"; scp "${SSHOPTS[@]}" -q "$src" "root@$ip:$dst"; }

echo "throughput-bench run $RUN | type=$TYPE (×2) loc=$LOC part_cap=$PART_CAP"

make_network() {
  hcloud network create --name "$RUN-net" --ip-range 10.10.0.0/16 >/dev/null
  hcloud network add-subnet "$RUN-net" --network-zone eu-central --type cloud --ip-range 10.10.1.0/24 >/dev/null
  register_network "$RUN-net"
}

# provision NAME TYPE → echoes "PUBLIC_IP PRIVATE_IP".
# NOTE: this runs in a subshell via `read < <(provision …)`, so it must NOT
# register the server — array mutations in a subshell are lost. The caller
# registers the name in the main shell BEFORE calling provision (see below),
# so the trap can tear the server down even if create/ip/describe fails here.
provision() {
  local name="$1" type="$2"
  hcloud server create --name "$name" --type "$type" --image ubuntu-24.04 \
    --location "$LOC" --ssh-key "$SSH_KEY" --network "$RUN-net" \
    --label purpose=fanout-throughput-bench >/dev/null
  local pub priv
  pub=$(hcloud server ip "$name")
  priv=$(hcloud server describe "$name" -o format='{{(index .PrivateNet 0).IP}}')
  echo "$pub $priv"
}

wait_ssh() { local ip="$1"; for _ in $(seq 1 40); do ssh_to "$ip" true 2>/dev/null && return 0; sleep 5; done; return 1; }

echo "── creating private network $RUN-net ──"
make_network
echo "── provisioning target + driver ($TYPE × 2) ──"
# Register in the MAIN shell before provisioning — provision() runs in a
# process-substitution subshell and cannot mutate SERVERS itself.
register_server "$RUN-target"
register_server "$RUN-driver"
read -r TARGET_PUB TARGET_PRIV < <(provision "$RUN-target" "$TYPE")
read -r DRIVER_PUB DRIVER_PRIV < <(provision "$RUN-driver" "$TYPE")
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
echo "── shipping HEAD ($HEAD_SHA) + building ──"
git archive --format=tar.gz HEAD -o /tmp/fanout-src.tgz
ship_and_build "$TARGET_PUB" target
ship_and_build "$DRIVER_PUB" driver
rm -f /tmp/fanout-src.tgz

# snap METRIC → integer sum across label series, scraped over the private net.
# The curl runs on the driver (double-quoted, remote); the awk runs locally in a
# single-quoted program, so its quotes are NOT escaped.
snap() { ssh_to "$DRIVER_PUB" "curl -s -m3 http://$TARGET_PRIV:7520/-/metrics" | awk -v k="$1" '$0 ~ "^"k {s+=$2} END{printf "%d", s+0}'; }

boot_fanout() {
  ssh_to "$TARGET_PUB" "bash -s" <<'REMOTE'
set -e
cd /root/fanout
set -a; . ./.env; set +a
DATA_DIR=/root/fanout/data PUBLIC_READ=true ENV=development \
  OTLP_GRPC_ADDR=:4317 HTTP_ADDR=:7520 \
  FLUSH_SECONDS=15 ROLLUP_EVERY=60 DUCKLAKE_MAINTENANCE_EVERY_SECONDS=60 \
  nohup ./bin/fanout >/root/fanout/f.log 2>&1 &
echo "started fanout pid $!"
REMOTE
  echo "── waiting for fanout /healthz ──"
  for _ in $(seq 1 40); do
    ssh_to "$DRIVER_PUB" "curl -fsS -m2 http://$TARGET_PRIV:7520/healthz" >/dev/null 2>&1 && { echo "✓ fanout healthy"; return 0; }
    sleep 2
  done
  echo "fanout never became healthy" >&2; ssh_to "$TARGET_PUB" "tail -30 /root/fanout/f.log" >&2; return 1
}

boot_fanout

ROWS_PER_TRACE=4.3   # 2 spans + 0.15*2 messaging spans + 1 log + 1 metric, at the default data shape

# run_step TARGET_TRACES DUR_SEC  → sets STEP_ACHIEVED_RPS / STEP_VERDICT / STEP_REASON
run_step() {
  local traces="$1" dur="$2"
  local rows0 rows1 drops0 drops1 t0 t1 part rts age rss errs
  rows0=$(snap fanout_ingest_rows_total); drops0=$(snap fanout_rows_dropped_total); t0=$(date +%s)

  # Driver fires loadgen at the target's PRIVATE ip, query-under-load on, query-p95
  # gate armed. loadgen runs on the driver but its stdout/stderr stream back over
  # SSH — capture to a LOCAL file so we can grep its verdict locally. loadgen
  # exits non-zero on any SLO/error failure (the `|| true` keeps the harness alive
  # so we can classify it ourselves).
  local steplog="/tmp/$RUN-step-$traces.log"
  ssh_to "$DRIVER_PUB" "bash -s" <<REMOTE >"$steplog" 2>&1 || true
cd /root/fanout
./bin/loadgen -endpoint $TARGET_PRIV:4317 -rate $traces -duration ${dur}s -workers 48 \
  -services 50 -attr-cardinality 200 -error-rate 0.05 -messaging-ratio 0.15 \
  -metrics-url http://$TARGET_PRIV:7520/-/metrics \
  -query-url http://$TARGET_PRIV:7520 -query-workers 4 -query-rate 20 \
  -max-query-p95-ms 1500 -report /root/fanout/step-$traces.json
REMOTE

  t1=$(date +%s); rows1=$(snap fanout_ingest_rows_total); drops1=$(snap fanout_rows_dropped_total)
  part=$(snap fanout_lake_partitions)
  rts=$(snap fanout_rollup_last_success_timestamp); age=$(( rts > 0 ? t1 - rts : 999 ))
  rss=$(ssh_to "$TARGET_PUB" "ps -C fanout -o rss= 2>/dev/null | awk '{printf \"%d\", \$1/1024}'")
  errs=$(ssh_to "$TARGET_PUB" "grep -cE 'level\":\"ERROR' /root/fanout/f.log")
  # Capture the actual ERROR text now — the target is torn down on exit, so a
  # bare count is undiagnosable after the run. Pulled only when errs>0.
  local errsample=""
  if [ "${errs:-0}" -gt 0 ]; then
    errsample=$(ssh_to "$TARGET_PUB" "grep -E 'level\":\"ERROR' /root/fanout/f.log | tail -3")
  fi

  local secs=$(( t1 - t0 )); [ "$secs" -lt 1 ] && secs=1
  STEP_ACHIEVED_RPS=$(( (rows1 - rows0) / secs ))
  local target_rps; target_rps=$(awk -v t="$traces" -v r="$ROWS_PER_TRACE" 'BEGIN{printf "%d", t*r}')

  # loadgen prints "FAIL: ..." to stderr (now in the local steplog) on any SLO
  # breach, send error, or query error — grep the LOCAL capture, not the driver.
  # grep -c prints "0" AND exits 1 on no-match; a `|| echo 0` would then append a
  # second "0", yielding "0\n0" and breaking the -gt test. Capture the count and
  # default an empty result (missing file) to 0 instead.
  local lg_fail; lg_fail=$(grep -c '^FAIL:' "$steplog" 2>/dev/null); lg_fail=${lg_fail:-0}

  STEP_VERDICT=pass; STEP_REASON=""
  if [ "$(( drops1 - drops0 ))" -gt 0 ]; then STEP_VERDICT=fail; STEP_REASON="drops=$(( drops1 - drops0 ))"
  elif [ "$lg_fail" -gt 0 ]; then STEP_VERDICT=fail; STEP_REASON="loadgen SLO/errors (see step log)"
  elif [ "$age" -gt 240 ]; then STEP_VERDICT=fail; STEP_REASON="rollup age ${age}s>240s"
  elif [ "${part:-0}" -gt "$PART_CAP" ]; then STEP_VERDICT=fail; STEP_REASON="partitions ${part}>${PART_CAP}"
  elif [ "${errs:-0}" -gt 0 ]; then STEP_VERDICT=fail; STEP_REASON="ERROR logs=${errs}"
  elif [ "$STEP_ACHIEVED_RPS" -lt "$(( target_rps * 95 / 100 ))" ]; then STEP_VERDICT=inconclusive; STEP_REASON="achieved ${STEP_ACHIEVED_RPS}<95% of target ${target_rps} rps — driver may be capped"
  fi
  printf "  step %6d tr/s → achieved %7d rows/s | part=%s ageS=%s rssMB=%s | %s %s\n" \
    "$traces" "$STEP_ACHIEVED_RPS" "$part" "$age" "$rss" "$STEP_VERDICT" "$STEP_REASON"
  [ -n "$errsample" ] && printf '      server ERROR sample:\n%s\n' "$errsample" | sed 's/^/      /'
}

# ── Ramp: find the ceiling (first SLO break) and the last passing step ────────
RAMP_STEPS=(6000 9000 12000 16000 20000 28000)
STEP_DUR=180
RESULTS=()
CEILING_RPS=0
LASTPASS_RPS=0

echo "── RAMP (${STEP_DUR}s/step) ──"
for traces in "${RAMP_STEPS[@]}"; do
  run_step "$traces" "$STEP_DUR"
  RESULTS+=("$traces $STEP_ACHIEVED_RPS $STEP_VERDICT ${STEP_REASON:-ok}")
  case "$STEP_VERDICT" in
    pass)         LASTPASS_RPS=$STEP_ACHIEVED_RPS ;;
    fail)         CEILING_RPS=$STEP_ACHIEVED_RPS; echo "  ceiling found at ${CEILING_RPS} rows/s (${STEP_REASON})"; break ;;
    inconclusive) echo "  ⚠ inconclusive — driver may be the bottleneck; not advancing ceiling" ;;
  esac
done

if [ "$CEILING_RPS" -eq 0 ]; then
  echo "  ⚠ top ramp step still PASSED — true ceiling is above ${LASTPASS_RPS} rows/s; extend RAMP_STEPS upward to bracket it"
fi
if [ "$LASTPASS_RPS" -eq 0 ]; then
  echo "  ⚠ even the first ramp step did not PASS — lower RAMP_STEPS; skipping soak" >&2
fi

# ── Soak: certify the rated capacity at ~80% of the last passing step ─────────
SOAK_DUR=900
RATED_RPS=0
SOAK_VERDICT=skipped
if [ "$LASTPASS_RPS" -gt 0 ]; then
  soak_traces=$(awk -v rps="$LASTPASS_RPS" -v r="$ROWS_PER_TRACE" 'BEGIN{printf "%d", (rps*0.8)/r}')
  echo "── SOAK (${SOAK_DUR}s @ ~80% of last-pass = ${soak_traces} tr/s) ──"
  run_step "$soak_traces" "$SOAK_DUR"
  SOAK_VERDICT=$STEP_VERDICT
  if [ "$STEP_VERDICT" = pass ]; then RATED_RPS=$STEP_ACHIEVED_RPS; fi
  RESULTS+=("SOAK:$soak_traces $STEP_ACHIEVED_RPS $STEP_VERDICT ${STEP_REASON:-ok}")
  # Final liveness assertion (a mid-soak OOM makes snap read 0 → would look like a pass).
  if ! ssh_to "$DRIVER_PUB" "curl -fsS -m3 http://$TARGET_PRIV:7520/healthz" >/dev/null 2>&1; then
    SOAK_VERDICT=fail; RATED_RPS=0; echo "  ✗ fanout unreachable after soak — rated capacity void"
  fi
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo
echo "════════ THROUGHPUT — ${TYPE} (HEAD $HEAD_SHA) ════════"
printf "%-14s %14s %-12s %s\n" "target tr/s" "achieved rows/s" "verdict" "reason"
for row in "${RESULTS[@]}"; do
  # shellcheck disable=SC2086
  set -- $row
  printf "%-14s %14s %-12s %s\n" "$1" "$2" "$3" "${*:4}"
done
echo "────────────────────────────────────────────────────────────"
echo "ceiling        : ${CEILING_RPS} rows/s (first SLO break)"
echo "rated capacity : ${RATED_RPS} rows/s (sustained ${SOAK_DUR}s, all SLOs held: ${SOAK_VERDICT})"
if [ "$RATED_RPS" -gt 0 ] && [ "$CEILING_RPS" -gt 0 ]; then
  awk -v c="$CEILING_RPS" -v r="$RATED_RPS" 'BEGIN{printf "headroom       : %.2fx (ceiling / rated)\n", c/r}'
fi
echo "════════════════════════════════════════════════════════════"

mkdir -p ./bench-results
ssh_to "$TARGET_PUB" "cd /root/fanout && tar czf - step-*.json 2>/dev/null" > "./bench-results/$RUN.tgz" || true
echo "per-step JSON reports: ./bench-results/$RUN.tgz"
# trap deletes both VMs + the network
