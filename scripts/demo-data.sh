#!/usr/bin/env bash
set -euo pipefail

data_dir="${DATA_DIR:-./data}"
endpoint="${DEMO_OTLP_ENDPOINT:-demo.fanout.test:4317}"
marker="$data_dir/.demo-data-v1"

if [ -f "$marker" ]; then
  echo "demo telemetry already present ($marker)"
  exit 0
fi

mkdir -p "$data_dir"
if [ ! -x ./bin/bench ]; then
  CGO_ENABLED=1 go build -o ./bin/bench ./cmd/bench
fi

args=(
  -endpoint "$endpoint"
  -duration "${DEMO_DURATION:-12s}"
  -rate "${DEMO_RATE:-30}"
  -workers "${DEMO_WORKERS:-4}"
  -services "${DEMO_SERVICES:-12}"
  -namespaces "${DEMO_NAMESPACES:-1}"
  -error-rate "${DEMO_ERROR_RATE:-0.08}"
  -messaging-ratio "${DEMO_MESSAGING_RATIO:-0.35}"
  -backfill-hours "${DEMO_BACKFILL_HOURS:-0.75}"
)

if [ -n "${DEMO_INGEST_TOKEN:-}" ]; then
  args+=(-token "$DEMO_INGEST_TOKEN")
fi

report="$(mktemp -t fanout-demo-data.XXXXXX)"
trap 'rm -f "$report"' EXIT
if ! ./bin/bench "${args[@]}" 2>&1 | tee "$report"; then
  # A timed load can report a few deadline-exceeded sends while workers stop.
  # The seed is still valid when at least one trace was accepted; a connection
  # or authentication failure reports zero and remains a hard failure.
  if ! grep -Eq 'sent[[:space:]]+traces=[1-9][0-9]*' "$report"; then
    exit 1
  fi
  echo "load completed with shutdown-time export errors; accepted telemetry is usable"
fi
touch "$marker"
echo "demo telemetry loaded into $endpoint"
