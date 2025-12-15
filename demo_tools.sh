#!/bin/bash
cd /Users/v/Projects/labstack/fanout

SID=$(curl -s -i -X POST http://localhost:7520/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' \
  | grep 'Mcp-Session-Id' | cut -d' ' -f2 | tr -d '\r')

call_tool() {
  curl -s -X POST http://localhost:7520/mcp \
    -H "Content-Type: application/json" \
    -H "Mcp-Session-Id: $SID" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$1\",\"arguments\":$2}}" \
    | grep '^data:' | sed 's/^data: //' | jq -r '.result.content[0].text'
}

echo "═══════════════════════════════════════════════════════════════"
echo "1. STATUS - System health at a glance"
echo "═══════════════════════════════════════════════════════════════"
call_tool "status" "{}" | jq '.'

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "2. DIAGNOSE - Deep-dive: checkoutservice"
echo "═══════════════════════════════════════════════════════════════"
call_tool "diagnose" '{"service":"checkoutservice"}' | jq '{service, status, p95_ms, error_rate, dependencies, slow_ops: (.slow_ops // [])[:2]}'

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "3. FIND - Search: slow spans"
echo "═══════════════════════════════════════════════════════════════"
call_tool "find" '{"status":"slow"}' | jq '{span_count, log_count, suggestion, sample: (.spans // [])[:2]}'

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "4. TRACE - Follow a request journey"
echo "═══════════════════════════════════════════════════════════════"
# Get a trace ID from find results
TRACE_ID=$(call_tool "find" '{"status":"error"}' | jq -r '.spans[0].trace_id // empty')
if [ -n "$TRACE_ID" ]; then
  call_tool "trace" "{\"trace_id\":\"$TRACE_ID\"}" | jq '{trace_id, span_count, total_duration_ms, services, root_cause, critical_path}'
else
  echo "No error traces found"
fi

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "5. TIMELINE - Time series with anomaly detection"
echo "═══════════════════════════════════════════════════════════════"
call_tool "timeline" '{"window":30}' | jq '{window_minutes, bucket_count: (.buckets | length), anomalies, avg_p95_ms}'

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "6. TOPOLOGY - Service dependency map"
echo "═══════════════════════════════════════════════════════════════"
call_tool "topology" "{}" | jq '{service_count, edge_count, nodes: [.nodes[:5][] | {name, status, span_count, p95_ms}], edges: (.edges // [])[:3]}'

echo ""
echo "═══════════════════════════════════════════════════════════════"
echo "7. QUERY - Raw SQL power"
echo "═══════════════════════════════════════════════════════════════"
call_tool "query" '{"sql":"SELECT \"name=service_name\" as svc, count(*) as cnt, avg(\"name=duration_ms\") as avg_ms FROM read_parquet('\''lake/spans/**/*.parquet'\'') GROUP BY 1 ORDER BY cnt DESC LIMIT 5"}' | jq '.'
