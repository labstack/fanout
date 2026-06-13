#!/usr/bin/env bash
# Soak test: sustained ingest for N minutes, asserting the GROWTH invariants the
# 30s benchmark can't catch — the accumulation failure mode that took prod down
# (unbounded file/snapshot growth). Compaction is run every 60s here (vs the 1h
# prod default) so a short soak can verify file-count stays bounded.
#
# Usage:  scripts/soak.sh [DURATION_MIN] [RATE]
# Example: scripts/soak.sh 15 5000
# Env:    PART_CAP (max allowed lake_partitions, default 800)
# Exits non-zero if any invariant is violated.
set -uo pipefail
cd "$(dirname "$0")/.."

DUR_MIN="${1:-10}"
RATE="${2:-5000}"
DUR_SEC=$((DUR_MIN * 60))
PART_CAP="${PART_CAP:-800}"
GHTTP=:17520
GGRPC=:14317

[ -f .env ] || { echo "need a .env to boot fanout" >&2; exit 1; }
TMPD=$(mktemp -d)
FPID=""
LGPID=""
cleanup() {
  [ -n "$LGPID" ] && kill "$LGPID" 2>/dev/null
  if [ -n "$FPID" ]; then kill "$FPID" 2>/dev/null; wait "$FPID" 2>/dev/null || true; fi
  rm -rf "$TMPD"
}
trap cleanup EXIT INT TERM

CGO_ENABLED=1 go build -o "$TMPD/fanout" ./cmd/fanout || exit 1
CGO_ENABLED=1 go build -o "$TMPD/loadgen" ./cmd/loadgen || exit 1

set -a; . ./.env; set +a
DATA_DIR="$TMPD/data" PUBLIC_READ=true OTLP_GRPC_ADDR="$GGRPC" HTTP_ADDR="$GHTTP" ENV=development \
  FLUSH_SECONDS=5 ROLLUP_EVERY=10 DUCKLAKE_MAINTENANCE_EVERY_SECONDS=60 "$TMPD/fanout" >"$TMPD/f.log" 2>&1 &
FPID=$!
for _ in $(seq 1 40); do curl -fsS -m2 "localhost${GHTTP}/healthz" >/dev/null 2>&1 && break; sleep 1; done

snap() { curl -s -m3 "localhost${GHTTP}/-/metrics" | awk -v k="$1" '$0 ~ "^"k {s+=$2} END{printf "%d", s+0}'; }

echo "soak: ${DUR_MIN}min @ ${RATE} traces/s | compaction every 60s | partition cap ${PART_CAP}"
"$TMPD/loadgen" -endpoint "localhost${GGRPC}" -duration "${DUR_SEC}s" -rate "$RATE" -workers 16 \
  -services 50 -messaging-ratio 0.15 >"$TMPD/lg.log" 2>&1 &
LGPID=$!

t0=$(date +%s)
maxpart=0 maxrss=0 stale=0
printf "  %6s %12s %10s %9s %7s\n" "t" "partitions" "rollupAge" "RSS_MB" "drops"
while kill -0 "$LGPID" 2>/dev/null; do
  sleep 30
  part=$(snap fanout_lake_partitions)
  rts=$(snap fanout_rollup_last_success_timestamp)
  now=$(date +%s)
  age=$(( rts > 0 ? now - rts : 999 ))
  rss=$(ps -o rss= -p "$FPID" 2>/dev/null | awk '{printf "%d", $1/1024}')
  drops=$(snap fanout_rows_dropped_total)
  [ "${part:-0}" -gt "$maxpart" ] && maxpart=$part
  [ "${rss:-0}" -gt "$maxrss" ] && maxrss=$rss
  [ "$age" -gt 40 ] && stale=$((stale + 1)) # > 4× ROLLUP_EVERY → rollups falling behind
  printf "  %6s %12s %9ss %9s %7s\n" "$(( now - t0 ))s" "$part" "$age" "$rss" "$drops"
done

drops=$(snap fanout_rows_dropped_total)
oom=$(grep -ciE 'out of memory' "$TMPD/f.log")
errs=$(grep -cE 'level":"ERROR' "$TMPD/f.log")

echo
echo "── soak result (${DUR_MIN}min) ─────────────────────────────"
echo "max lake_partitions : ${maxpart} (cap ${PART_CAP})"
echo "max RSS             : ${maxrss} MB"
echo "rollup stale samples: ${stale}"
echo "drops / OOM / errors: ${drops} / ${oom} / ${errs}"

fail=""
# Liveness FIRST: a kernel OOM-kill leaves no app log, and snap() returns "0"
# for an unreachable server — so without this a dead fanout reads as PASS.
kill -0 "$FPID" 2>/dev/null || fail="${fail} fanout-died;"
curl -fsS -m3 "localhost${GHTTP}/healthz" >/dev/null 2>&1 || fail="${fail} healthz-unreachable;"
[ "${drops:-0}" -gt 0 ] && fail="${fail} drops=${drops};"
[ "${oom:-0}" -gt 0 ] && fail="${fail} OOM;"
[ "${errs:-0}" -gt 0 ] && fail="${fail} errors=${errs};"
[ "${maxpart:-0}" -gt "$PART_CAP" ] && fail="${fail} partitions ${maxpart}>${PART_CAP} (compaction not keeping up);"
[ "${stale:-0}" -gt 0 ] && fail="${fail} rollup went stale ${stale}×;"
if [ -n "$fail" ]; then
  echo "RESULT              : ✗ FAIL —${fail}"
  echo "────────────────────────────────────────────────────────────"
  exit 1
fi
echo "RESULT              : ✓ PASS — growth bounded, rollups fresh, no drops"
echo "────────────────────────────────────────────────────────────"
