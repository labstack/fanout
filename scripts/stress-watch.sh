#!/usr/bin/env bash
# Watch the stress-relevant metrics on a running fanout — the signals that
# flagged the prod incident: file/snapshot growth, ingest backpressure, drops,
# rollup freshness, flush rate.
# Usage: scripts/stress-watch.sh [HOST:PORT]   (default localhost:7520)
set -uo pipefail
ADDR="${1:-localhost:7520}"
exec watch -n5 "curl -s ${ADDR}/-/metrics | grep -E '^(fanout_lake_partitions|fanout_lake_size_bytes|fanout_ingest_queue_depth|fanout_rows_dropped_total|fanout_rollup_last_success_timestamp|fanout_flush_duration_seconds_count)'"
