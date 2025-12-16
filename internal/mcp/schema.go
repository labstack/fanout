package mcp

import (
	"context"

	"github.com/labstack/fanout/internal/query"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type SchemaIn struct{}

type SchemaOut struct {
	Schema   string   `json:"schema"`
	Examples []string `json:"examples"`
}

func (s *Server) schema(ctx context.Context, req *mcp.CallToolRequest, in SchemaIn) (*mcp.CallToolResult, SchemaOut, error) {
	return nil, SchemaOut{
		Schema: query.GetSchema(),
		Examples: []string{
			// Recent errors
			`SELECT "name=service_name", "name=name", "name=status_msg", "name=duration_ms"
FROM read_parquet('lake/spans/**/*.parquet')
WHERE "name=status_code" = 'STATUS_CODE_ERROR'
  AND "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 900) * 1000000000
ORDER BY "name=start_unix_nano" DESC
LIMIT 20`,

			// Service latency percentiles
			`SELECT "name=service_name",
       COUNT(*) as count,
       ROUND(AVG("name=duration_ms"), 2) as avg_ms,
       ROUND(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms"), 2) as p95_ms,
       ROUND(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY "name=duration_ms"), 2) as p99_ms
FROM read_parquet('lake/spans/**/*.parquet')
WHERE "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 900) * 1000000000
GROUP BY "name=service_name"
ORDER BY count DESC`,

			// Error rate by service
			`SELECT "name=service_name",
       COUNT(*) as total,
       SUM(CASE WHEN "name=status_code" = 'STATUS_CODE_ERROR' THEN 1 ELSE 0 END) as errors,
       ROUND(100.0 * SUM(CASE WHEN "name=status_code" = 'STATUS_CODE_ERROR' THEN 1 ELSE 0 END) / COUNT(*), 2) as error_pct
FROM read_parquet('lake/spans/**/*.parquet')
WHERE "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 900) * 1000000000
GROUP BY "name=service_name"
HAVING COUNT(*) > 10
ORDER BY error_pct DESC`,

			// Logs with errors
			`SELECT "name=service_name", "name=severity", "name=body"
FROM read_parquet('lake/logs/**/*.parquet')
WHERE "name=severity" IN ('ERROR', 'FATAL')
  AND "name=time_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 900) * 1000000000
ORDER BY "name=time_unix_nano" DESC
LIMIT 50`,

			// Service dependencies from spans
			`SELECT DISTINCT "name=service_name" as caller,
       json_extract_string("name=attributes_json", '$.peer.service') as callee
FROM read_parquet('lake/spans/**/*.parquet')
WHERE "name=kind" = 'SPAN_KIND_CLIENT'
  AND json_extract_string("name=attributes_json", '$.peer.service') IS NOT NULL
  AND "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 3600) * 1000000000`,

			// Timeline (requests per minute)
			`SELECT ts_min, service_name, span_count,
       ROUND(error_rate * 100, 2) as error_pct,
       ROUND(p95_ms, 2) as p95
FROM svc_minute
WHERE ts_min >= NOW() - INTERVAL '60 minutes'
ORDER BY ts_min DESC, span_count DESC`,
		},
	}, nil
}
