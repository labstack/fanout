package mcp

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/query"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type SchemaIn struct{}

type SchemaOut struct {
	Schema   string   `json:"schema"`
	Examples []string `json:"examples"`
}

func (s *Server) schema(ctx context.Context, req *mcp.CallToolRequest, in SchemaIn) (*mcp.CallToolResult, SchemaOut, error) {
	lakeDir := s.cfg.LakeDir
	return nil, SchemaOut{
		Schema: query.GetSchema(),
		Examples: []string{
			// Recent errors
			fmt.Sprintf(`SELECT "name=service_name", "name=name", "name=status_msg", "name=duration_ms"
FROM read_parquet('%s/spans/**/*.parquet', union_by_name=true)
WHERE "name=status_code" = 'STATUS_CODE_ERROR'
  AND "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 900) * 1000000000
ORDER BY "name=start_unix_nano" DESC
LIMIT 20`, lakeDir),

			// Service latency percentiles
			fmt.Sprintf(`SELECT "name=service_name",
       COUNT(*) as count,
       ROUND(AVG("name=duration_ms"), 2) as avg_ms,
       ROUND(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms"), 2) as p95_ms,
       ROUND(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY "name=duration_ms"), 2) as p99_ms
FROM read_parquet('%s/spans/**/*.parquet', union_by_name=true)
WHERE "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 900) * 1000000000
GROUP BY "name=service_name"
ORDER BY count DESC`, lakeDir),

			// Error rate by service
			fmt.Sprintf(`SELECT "name=service_name",
       COUNT(*) as total,
       SUM(CASE WHEN "name=status_code" = 'STATUS_CODE_ERROR' THEN 1 ELSE 0 END) as errors,
       ROUND(100.0 * SUM(CASE WHEN "name=status_code" = 'STATUS_CODE_ERROR' THEN 1 ELSE 0 END) / COUNT(*), 2) as error_pct
FROM read_parquet('%s/spans/**/*.parquet', union_by_name=true)
WHERE "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 900) * 1000000000
GROUP BY "name=service_name"
HAVING COUNT(*) > 10
ORDER BY error_pct DESC`, lakeDir),

			// Logs with errors
			fmt.Sprintf(`SELECT "name=service_name", "name=severity", "name=body"
FROM read_parquet('%s/logs/**/*.parquet', union_by_name=true)
WHERE "name=severity" IN ('ERROR', 'FATAL')
  AND "name=time_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 900) * 1000000000
ORDER BY "name=time_unix_nano" DESC
LIMIT 50`, lakeDir),

			// Service dependencies from spans
			fmt.Sprintf(`SELECT DISTINCT "name=service_name" as caller,
       json_extract_string(from_utf8("name=attributes_json"), '$.peer.service') as callee
FROM read_parquet('%s/spans/**/*.parquet', union_by_name=true)
WHERE "name=kind" = 'SPAN_KIND_CLIENT'
  AND json_extract_string(from_utf8("name=attributes_json"), '$.peer.service') IS NOT NULL
  AND "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 3600) * 1000000000`, lakeDir),

			// Timeline (requests per minute)
			`SELECT bucket, service, spans,
       ROUND(error_rate * 100, 2) as error_pct,
       ROUND(p95_ms, 2) as p95
FROM service_rollup
WHERE bucket >= NOW() - INTERVAL '60 minutes'
ORDER BY bucket DESC, spans DESC`,
		},
	}, nil
}
