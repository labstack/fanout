#!/usr/bin/env bash
# Profile fanout under load. Boots a throwaway fanout with PPROF_ENABLED=true,
# drives it with cmd/loadgen, captures CPU/heap/alloc/mutex/block profiles at
# steady state, and prints the top nodes of each — the basis for finding
# allocation hotspots and lock contention.
#
# Usage:  scripts/profile.sh [CPU_SECONDS] [LOAD_RATE_PER_GEN] [GENERATORS]
# Example: scripts/profile.sh 30 25000 3
#
# Saved profiles land in ./bench-profiles/ for `go tool pprof` follow-up.
set -uo pipefail
cd "$(dirname "$0")/.."

CPUSEC="${1:-30}"
RATE="${2:-25000}"
GENS="${3:-3}"
GHTTP=:17520
GGRPC=:14317
OUT="bench-profiles"
mkdir -p "$OUT"

TMPD=$(mktemp -d)
FPID=""
cleanup() { [ -n "$FPID" ] && kill "$FPID" 2>/dev/null; rm -rf "$TMPD"; }
trap cleanup EXIT INT TERM

CGO_ENABLED=1 go build -o "$TMPD/fanout" ./cmd/fanout || exit 1
CGO_ENABLED=1 go build -o "$TMPD/loadgen" ./cmd/loadgen || exit 1

set -a; . ./.env; set +a
DATA_DIR="$TMPD/data" PUBLIC_READ=true PPROF_ENABLED=true OTLP_GRPC_ADDR="$GGRPC" HTTP_ADDR="$GHTTP" ENV=development \
  FLUSH_SECONDS=5 ROLLUP_EVERY=15 DUCKDB_MAX_CONNS=4 "$TMPD/fanout" >"$TMPD/fanout.log" 2>&1 &
FPID=$!
for _ in $(seq 1 40); do curl -fsS -m2 "localhost${GHTTP}/healthz" >/dev/null 2>&1 && break; sleep 1; done

pids=()
for n in $(seq 1 "$GENS"); do
  "$TMPD/loadgen" -endpoint "localhost${GGRPC}" -duration "$((CPUSEC + 20))s" -rate "$RATE" -workers 20 -services 50 >/dev/null 2>&1 &
  pids+=($!)
done
sleep 8 # reach steady state
echo "capturing profiles under load (CPU ${CPUSEC}s)…"
curl -s "http://localhost${GHTTP}/debug/pprof/profile?seconds=${CPUSEC}" -o "$OUT/cpu.prof"
for p in heap allocs mutex block goroutine; do
  curl -s "http://localhost${GHTTP}/debug/pprof/$p" -o "$OUT/$p.prof"
done
wait "${pids[@]}" 2>/dev/null

BIN="$TMPD/fanout"
echo "=== CPU (top) ==============================================="
go tool pprof -top -nodecount=20 "$BIN" "$OUT/cpu.prof" 2>/dev/null | sed -n '1,28p'
echo "=== alloc_space (GC pressure) ==============================="
go tool pprof -top -sample_index=alloc_space -nodecount=15 "$BIN" "$OUT/allocs.prof" 2>/dev/null | sed -n '1,23p'
echo "=== mutex (lock contention) ================================="
go tool pprof -top -nodecount=10 "$BIN" "$OUT/mutex.prof" 2>/dev/null | sed -n '1,18p'
echo
echo "profiles saved to $OUT/ — e.g.  go tool pprof -http=: $OUT/allocs.prof"
