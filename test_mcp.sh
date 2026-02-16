#!/bin/bash
# Test MCP tools

SID=$(curl -s -i -X POST https://fanout.test/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' \
  | grep 'Mcp-Session-Id' | cut -d' ' -f2 | tr -d '\r')

echo "Session: $SID"
echo ""

call_tool() {
  local tool=$1
  local args=$2
  curl -s -X POST https://fanout.test/mcp \
    -H "Content-Type: application/json" \
    -H "Mcp-Session-Id: $SID" \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"$tool\",\"arguments\":$args}}" \
    | grep '^data:' | sed 's/^data: //' | jq '.result.structuredContent'
}

echo "=== 1. STATUS ==="
call_tool "status" "{}"

echo ""
echo "=== 2. TOPOLOGY ==="
call_tool "topology" "{}" | jq '{service_count, edge_count, nodes: [.nodes[:3][] | {name, status, span_count}]}'

echo ""
echo "=== 3. DIAGNOSE (postgres) ==="
call_tool "diagnose" '{"service":"postgres"}' | jq '{service, status, span_count, p95_ms}'

echo ""
echo "=== 4. FIND (errors) ==="
call_tool "find" '{"status":"error"}' | jq '{span_count, log_count, suggestion}'

echo ""
echo "=== 5. TIMELINE ==="
call_tool "timeline" "{}" | jq '{window_minutes, buckets: (.buckets | length), anomalies: (.anomalies | length)}'
