#!/usr/bin/env bash
# Local ingest benchmark for fanout. Boots a throwaway fanout (tokenless via
# public read/ingest/metrics, isolated ports + temp data dir), drives it with N parallel
# cmd/bench drivers, and reports the SERVER-side accepted-rows rate plus
# the health signals that matter (drops, file growth, rollup time, RSS).
#
# Usage:  scripts/bench.sh [GENERATORS] [RATE_PER_GEN] [DURATION_SEC]
# Example: scripts/bench.sh 3 25000 30
#
# Requires CGO (DuckDB). Run from the repo root.
set -uo pipefail
cd "$(dirname "$0")/.."

# Auto-scale the load to the host: one generator per CPU core saturates the
# machine (bench + fanout share the cores), so the result reflects THIS
# machine's max. Override any of gens/rate/duration positionally.
detect_cores() { command -v nproc >/dev/null 2>&1 && nproc || sysctl -n hw.ncpu 2>/dev/null || echo 4; }
CORES=$(detect_cores)
GENS="${1:-$CORES}"
RATE="${2:-50000}" # high per-gen rate so the generator's pacing isn't the cap
DUR="${3:-30}"
GHTTP=:17520
GGRPC=:14317

command -v go >/dev/null || { echo "go not found" >&2; exit 1; }
[ -f .env ] || { echo "need a .env (SMTP/AI/auth) to boot fanout — copy fanout/.env.example" >&2; exit 1; }

TMPD=$(mktemp -d)
FPID=""
SAMPLER=""
cleanup() {
  [ -n "$SAMPLER" ] && kill "$SAMPLER" 2>/dev/null
  if [ -n "$FPID" ]; then
    kill "$FPID" 2>/dev/null
    wait "$FPID" 2>/dev/null || true # let fanout finish flushing before we rm its data dir
  fi
  rm -rf "$TMPD"
}
trap cleanup EXIT INT TERM

# Peak total CPU sampler (sum of all processes' %cpu; / cores / 100 = utilization).
# Portable across macOS/Linux ps. Records the peak seen during the measured run.
echo 0 > "$TMPD/cpupeak"
sample_cpu() {
  while :; do
    tot=$(ps -A -o %cpu= 2>/dev/null | awk '{s+=$1} END{printf "%d", s}')
    [ "${tot:-0}" -gt "$(cat "$TMPD/cpupeak")" ] && echo "$tot" > "$TMPD/cpupeak"
    sleep 1
  done
}

echo "building fanout + bench…"
CGO_ENABLED=1 go build -o "$TMPD/fanout" ./cmd/fanout || exit 1
CGO_ENABLED=1 go build -o "$TMPD/bench" ./cmd/bench || exit 1

set -a; . ./.env; set +a
DATA_DIR="$TMPD/data" PUBLIC_READ=true PUBLIC_INGEST=true METRICS_PUBLIC=true OTLP_GRPC_ADDR="$GGRPC" HTTP_ADDR="$GHTTP" ENV=development \
  FLUSH_SECONDS=5 ROLLUP_EVERY=15 DUCKDB_MAX_CONNS=4 "$TMPD/fanout" >"$TMPD/fanout.log" 2>&1 &
FPID=$!
for _ in $(seq 1 40); do curl -fsS -m2 "localhost${GHTTP}/healthz" >/dev/null 2>&1 && break; sleep 1; done

snap() { curl -s -m3 "localhost${GHTTP}/-/metrics" | awk -v k="$1" '$0 ~ "^"k {s+=$2} END{printf "%d", s+0}'; }

echo "warmup…"; "$TMPD/bench" -endpoint "localhost${GGRPC}" -duration 8s -rate 5000 -workers 12 -services 30 >/dev/null 2>&1

rows0=$(snap fanout_ingest_rows_total); t0=$(date +%s)
echo "running: $GENS generators (=${CORES} cores) × $RATE traces/s for ${DUR}s + query-under-load — driving the machine to saturation…"
sample_cpu & SAMPLER=$!
pids=()
for n in $(seq 1 "$GENS"); do
  # The first generator also drives read load (query-under-load) and writes a
  # JSON report so we can read its query latency percentiles.
  q=()
  [ "$n" = "1" ] && q=(-query-url "http://localhost${GHTTP}" -query-workers 4 -query-rate 40 -report "$TMPD/lg1.json")
  "$TMPD/bench" -endpoint "localhost${GGRPC}" -duration "${DUR}s" -rate "$RATE" -workers 20 \
    -services 50 -messaging-ratio 0.15 "${q[@]}" >"$TMPD/lg$n.log" 2>&1 &
  pids+=($!)
done
# Collect each generator's exit code (a bare `wait "${pids[@]}"` discards them).
# bench's own gate catches client-side failures the server-side metric scrape
# below cannot see — export RPCs failing while fanout stays healthy (drops=0,
# no OOM/errors) would otherwise read as PASS.
genfail=0
for pid in "${pids[@]}"; do wait "$pid" || genfail=1; done
kill "$SAMPLER" 2>/dev/null; SAMPLER=""
t1=$(date +%s); rows1=$(snap fanout_ingest_rows_total); dt=$((t1 - t0))
peakcpu=$(cat "$TMPD/cpupeak"); util=$(( peakcpu / CORES ))
rps=$(( (rows1 - rows0) / dt ))
drops=$(snap fanout_rows_dropped_total)
oom=$(grep -ciE 'out of memory' "$TMPD/fanout.log")
errs=$(grep -cE 'level":"ERROR' "$TMPD/fanout.log")
qp95=$(awk -F'[:,]' '/"query_latency_ms"/{f=1} f&&/"p95_ms"/{gsub(/[^0-9.]/,"",$2);print int($2);exit}' "$TMPD/lg1.json" 2>/dev/null)

echo
echo "── fanout ingest+query benchmark (max utilization) ─────────"
echo "machine         : ${CORES} cores | $GENS generators driving it"
echo "peak CPU         : ~${util}% of ${CORES} cores (${peakcpu}% summed)"
echo "rows accepted   : $((rows1 - rows0)) in ${dt}s = ${rps} rows/s"
echo "drops           : ${drops}"
echo "query p95       : ${qp95:-n/a} ms (mixed observability HTTP/MCP kernel — SLO 1500ms)"
awk '/^  (overview|topology|performance|trace|logs)[[:space:]]/ {print "  query/op       :" substr($0,3)}' "$TMPD/lg1.log"
echo "lake_partitions : $(snap fanout_lake_partitions) files | $(( $(snap fanout_lake_size_bytes) / 1048576 )) MB"
echo "rollups         : $(curl -s -m3 localhost${GHTTP}/-/metrics | awk '/rollup_duration_seconds_count/{c=$2}/rollup_duration_seconds_sum/{s=$2} END{printf "%d done, avg %.0fms", c, (c>0?s/c*1000:0)}')"
echo "fanout health   : OOM=${oom} ERR=${errs} RSS=$(ps -o rss= -p $FPID | awk '{printf "%.2f GB", $1/1024/1024}')"

# Pass/fail gate: zero drops, no OOM/errors, query p95 within SLO, throughput
# above MIN_ROWS (env, default 0 = skip). Non-zero exit so it can gate releases.
fail=""
# Liveness FIRST: snap() returns "0" for a dead/unreachable server, so without
# this a crashed (or OOM-killed, which leaves no app log) fanout reads as PASS.
kill -0 "$FPID" 2>/dev/null || fail="${fail} fanout-died;"
curl -fsS -m3 "localhost${GHTTP}/healthz" >/dev/null 2>&1 || fail="${fail} healthz-unreachable;"
[ "${drops:-0}" -gt 0 ] && fail="${fail} drops=${drops};"
[ "${oom:-0}" -gt 0 ] && fail="${fail} OOM;"
[ "${errs:-0}" -gt 0 ] && fail="${fail} server-errors=${errs};"
# Query load always runs here, so a missing/empty p95 means the read path broke
# (no successful queries / report not written) — fail, don't skip.
if [ -z "${qp95:-}" ]; then fail="${fail} query-p95-missing;"; elif [ "${qp95}" -gt 1500 ]; then fail="${fail} query-p95=${qp95}ms>1500;"; fi
[ "${rps:-0}" -lt "${MIN_ROWS:-0}" ] && fail="${fail} throughput=${rps}<${MIN_ROWS};"
# A non-zero generator exit means bench's own gate tripped (zero successful
# exports, send/query errors, or server-side drops it observed) — see lg*.log.
[ "${genfail:-0}" -ne 0 ] && fail="${fail} bench-gate-failed(see lg*.log);"
if [ -n "$fail" ]; then
  echo "RESULT          : ✗ FAIL —${fail}"
  echo "────────────────────────────────────────────────────────────"
  exit 1
fi
echo "RESULT          : ✓ PASS"
echo "────────────────────────────────────────────────────────────"
