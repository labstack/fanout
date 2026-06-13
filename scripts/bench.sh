#!/usr/bin/env bash
# Local ingest benchmark for fanout. Boots a throwaway fanout (tokenless via
# PUBLIC_READ, isolated ports + temp data dir), drives it with N parallel
# cmd/loadgen generators, and reports the SERVER-side accepted-rows rate plus
# the health signals that matter (drops, file growth, rollup time, RSS).
#
# Usage:  scripts/bench.sh [GENERATORS] [RATE_PER_GEN] [DURATION_SEC]
# Example: scripts/bench.sh 3 25000 30
#
# Requires CGO (DuckDB). Run from the repo root.
set -uo pipefail
cd "$(dirname "$0")/.."

GENS="${1:-3}"
RATE="${2:-25000}"
DUR="${3:-20}"
GHTTP=:17520
GGRPC=:14317

command -v go >/dev/null || { echo "go not found" >&2; exit 1; }
[ -f .env ] || { echo "need a .env (SMTP/AI/JWT) to boot fanout — copy fanout/.env.example" >&2; exit 1; }

TMPD=$(mktemp -d)
FPID=""
cleanup() { [ -n "$FPID" ] && kill "$FPID" 2>/dev/null; rm -rf "$TMPD"; }
trap cleanup EXIT INT TERM

echo "building fanout + loadgen…"
CGO_ENABLED=1 go build -o "$TMPD/fanout" ./cmd/fanout || exit 1
CGO_ENABLED=1 go build -o "$TMPD/loadgen" ./cmd/loadgen || exit 1

set -a; . ./.env; set +a
DATA_DIR="$TMPD/data" PUBLIC_READ=true OTLP_GRPC_ADDR="$GGRPC" HTTP_ADDR="$GHTTP" ENV=development \
  FLUSH_SECONDS=5 ROLLUP_EVERY=15 DUCKDB_MAX_CONNS=4 "$TMPD/fanout" >"$TMPD/fanout.log" 2>&1 &
FPID=$!
for _ in $(seq 1 40); do curl -fsS -m2 "localhost${GHTTP}/healthz" >/dev/null 2>&1 && break; sleep 1; done

snap() { curl -s -m3 "localhost${GHTTP}/-/metrics" | awk -v k="$1" '$0 ~ "^"k {s+=$2} END{printf "%d", s+0}'; }

echo "warmup…"; "$TMPD/loadgen" -endpoint "localhost${GGRPC}" -duration 8s -rate 5000 -workers 12 -services 30 >/dev/null 2>&1

rows0=$(snap fanout_ingest_rows_total); t0=$(date +%s)
echo "running: $GENS generators × $RATE traces/s for ${DUR}s…"
pids=()
for n in $(seq 1 "$GENS"); do
  "$TMPD/loadgen" -endpoint "localhost${GGRPC}" -duration "${DUR}s" -rate "$RATE" -workers 20 \
    -services 50 -messaging-ratio 0.15 >"$TMPD/lg$n.log" 2>&1 &
  pids+=($!)
done
wait "${pids[@]}"
t1=$(date +%s); rows1=$(snap fanout_ingest_rows_total); dt=$((t1 - t0))

echo
echo "── fanout ingest benchmark ─────────────────────────────────"
echo "rows accepted   : $((rows1 - rows0)) in ${dt}s = $(( (rows1 - rows0) / dt )) rows/s"
echo "drops           : $(snap fanout_rows_dropped_total)"
echo "lake_partitions : $(snap fanout_lake_partitions) files | $(( $(snap fanout_lake_size_bytes) / 1048576 )) MB"
echo "rollups         : $(curl -s -m3 localhost${GHTTP}/-/metrics | awk '/rollup_duration_seconds_count/{c=$2}/rollup_duration_seconds_sum/{s=$2} END{printf "%d done, avg %.0fms", c, (c>0?s/c*1000:0)}')"
echo "fanout health   : OOM=$(grep -ciE 'out of memory' "$TMPD/fanout.log") ERR=$(grep -cE 'level":"ERROR' "$TMPD/fanout.log") RSS=$(ps -o rss= -p $FPID | awk '{printf "%.2f GB", $1/1024/1024}')"
echo "────────────────────────────────────────────────────────────"
