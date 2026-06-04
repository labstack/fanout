package mcp

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/query"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// query - Raw SQL escape hatch with schema guidance

type QueryIn struct {
	SQL       string `json:"sql,omitempty"  jsonschema:"SQL query (SELECT/WITH only); omit for schema reference"`
	Explain   bool   `json:"explain,omitempty"   jsonschema:"Return query plan instead of results"`
	MaxRows   int    `json:"max_rows,omitempty"  jsonschema:"Max rows to return,default=1000"`
	TimeoutMs int    `json:"timeout_ms,omitempty" jsonschema:"Query timeout in milliseconds,default=30000"`
}

type QueryOut struct {
	Results    []query.RowMap  `json:"results,omitempty"`
	RowCount   int             `json:"row_count,omitempty"`
	ExecTimeMs int64           `json:"exec_time_ms,omitempty"`
	Truncated  bool            `json:"truncated,omitempty"`
	QueryPlan  string          `json:"query_plan,omitempty"`
	Warnings   []string        `json:"warnings,omitempty"`
	Schema     *SchemaResponse `json:"schema,omitempty"`
}

// SchemaResponse is returned when no SQL is provided.
type SchemaResponse struct {
	Views        []ViewSchema   `json:"views"`
	RollupTables []ViewSchema   `json:"rollup_tables"`
	Macros       []MacroInfo    `json:"macros"`
	Examples     []QueryExample `json:"examples"`
}

// ViewSchema describes a view or table.
type ViewSchema struct {
	Name    string       `json:"name"`
	Columns []ColumnInfo `json:"columns"`
}

// ColumnInfo describes a single column.
type ColumnInfo struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

// MacroInfo describes a SQL macro/helper.
type MacroInfo struct {
	Name        string `json:"name"`
	Signature   string `json:"signature"`
	Description string `json:"description"`
}

// QueryExample is a ready-to-use SQL example.
type QueryExample struct {
	Title string `json:"title"`
	SQL   string `json:"sql"`
}

func buildSchemaResponse() *SchemaResponse {
	return &SchemaResponse{
		Views: []ViewSchema{
			{
				Name: "spans",
				Columns: []ColumnInfo{
					{Name: "trace_id", Type: "VARCHAR", Description: "Distributed trace identifier"},
					{Name: "span_id", Type: "VARCHAR", Description: "Unique span identifier"},
					{Name: "parent_span_id", Type: "VARCHAR", Description: "Parent span identifier (empty for root spans)"},
					{Name: "service", Type: "VARCHAR", Description: "Service name"},
					{Name: "namespace", Type: "VARCHAR", Description: "Service namespace"},
					{Name: "operation", Type: "VARCHAR", Description: "Span/operation name"},
					{Name: "kind", Type: "VARCHAR", Description: "Span kind (server, client, producer, consumer, internal)"},
					{Name: "start_time", Type: "TIMESTAMP", Description: "Span start time"},
					{Name: "end_time", Type: "TIMESTAMP", Description: "Span end time"},
					{Name: "duration_ms", Type: "DOUBLE", Description: "Duration in milliseconds"},
					{Name: "status", Type: "VARCHAR", Description: "Status code (ok, error, unset)"},
					{Name: "status_message", Type: "VARCHAR", Description: "Status message"},
					{Name: "attributes_json", Type: "VARCHAR", Description: "Span attributes (JSON)"},
					{Name: "resource_json", Type: "VARCHAR", Description: "Resource attributes (JSON)"},
					{Name: "events_json", Type: "VARCHAR", Description: "Span events (JSON)"},
				},
			},
			{
				Name: "logs",
				Columns: []ColumnInfo{
					{Name: "time", Type: "TIMESTAMP", Description: "Log timestamp"},
					{Name: "severity", Type: "VARCHAR", Description: "Log severity (TRACE, DEBUG, INFO, WARN, ERROR, FATAL)"},
					{Name: "body", Type: "VARCHAR", Description: "Log message body"},
					{Name: "service", Type: "VARCHAR", Description: "Service name"},
					{Name: "namespace", Type: "VARCHAR", Description: "Service namespace"},
					{Name: "trace_id", Type: "VARCHAR", Description: "Associated trace identifier"},
					{Name: "span_id", Type: "VARCHAR", Description: "Associated span identifier"},
					{Name: "attributes_json", Type: "VARCHAR", Description: "Log attributes (JSON)"},
					{Name: "resource_json", Type: "VARCHAR", Description: "Resource attributes (JSON)"},
				},
			},
			{
				Name: "metrics",
				Columns: []ColumnInfo{
					{Name: "time", Type: "TIMESTAMP", Description: "Metric timestamp"},
					{Name: "name", Type: "VARCHAR", Description: "Metric name"},
					{Name: "type", Type: "VARCHAR", Description: "Metric type (gauge, sum, histogram)"},
					{Name: "unit", Type: "VARCHAR", Description: "Metric unit"},
					{Name: "service", Type: "VARCHAR", Description: "Service name"},
					{Name: "namespace", Type: "VARCHAR", Description: "Service namespace"},
					{Name: "value", Type: "DOUBLE", Description: "Metric value"},
					{Name: "attributes_json", Type: "VARCHAR", Description: "Metric attributes (JSON)"},
					{Name: "resource_json", Type: "VARCHAR", Description: "Resource attributes (JSON)"},
				},
			},
		},
		RollupTables: []ViewSchema{
			{
				Name: "service_rollup",
				Columns: []ColumnInfo{
					{Name: "namespace", Type: "VARCHAR", Description: "Namespace identifier (empty query arg means all namespaces)"},
					{Name: "bucket", Type: "TIMESTAMP", Description: "1-minute time bucket"},
					{Name: "service", Type: "VARCHAR", Description: "Service name"},
					{Name: "spans", Type: "BIGINT", Description: "Request count"},
					{Name: "error_rate", Type: "DOUBLE", Description: "Error rate (0-1)"},
					{Name: "p50_ms", Type: "DOUBLE", Description: "P50 latency"},
					{Name: "p95_ms", Type: "DOUBLE", Description: "P95 latency"},
					{Name: "log_count", Type: "BIGINT", Description: "Log entries count"},
					{Name: "metric_count", Type: "BIGINT", Description: "Distinct metric names count"},
				},
			},
			{
				Name: "edge_rollup",
				Columns: []ColumnInfo{
					{Name: "namespace", Type: "VARCHAR", Description: "Namespace identifier (empty query arg means all namespaces)"},
					{Name: "bucket", Type: "TIMESTAMP", Description: "1-minute time bucket"},
					{Name: "caller", Type: "VARCHAR", Description: "Calling service"},
					{Name: "callee", Type: "VARCHAR", Description: "Called service"},
					{Name: "calls", Type: "BIGINT", Description: "Call count"},
					{Name: "avg_ms", Type: "DOUBLE", Description: "Average call duration"},
					{Name: "error_rate", Type: "DOUBLE", Description: "Error rate (0-1)"},
					{Name: "edge_type", Type: "TEXT", Description: "call or messaging"},
				},
			},
		},
		Macros: []MacroInfo{
			{
				Name:        "attr",
				Signature:   "attr(json_col, key)",
				Description: "Extract a flat attribute by literal (dotted) key: equivalent to json_extract_string(json_col, '$.\"' || key || '\"'). Prefer this over a raw '$.key' path, which mis-parses dotted keys.",
			},
		},
		Examples: []QueryExample{
			{
				Title: "Error spans",
				SQL:   "SELECT * FROM spans WHERE status = 'STATUS_CODE_ERROR' ORDER BY start_time DESC LIMIT 20",
			},
			{
				Title: "Service latency",
				SQL:   "SELECT service, approx_quantile(duration_ms, 0.95) as p95 FROM spans WHERE start_time > now() - INTERVAL 15 MINUTE GROUP BY service",
			},
			{
				Title: "Error logs",
				SQL:   "SELECT * FROM logs WHERE severity IN ('ERROR','FATAL') ORDER BY time DESC LIMIT 50",
			},
			{
				Title: "Metric names",
				SQL:   "SELECT DISTINCT name, type, unit, service FROM metrics ORDER BY name",
			},
		},
	}
}

func (s *Server) query(ctx context.Context, req *mcp.CallToolRequest, in QueryIn) (*mcp.CallToolResult, QueryOut, error) {
	if in.SQL == "" {
		// Return structured schema reference
		return nil, QueryOut{
			Schema: buildSchemaResponse(),
		}, nil
	}

	if in.MaxRows <= 0 {
		in.MaxRows = 1000
	}
	if in.MaxRows > 10000 {
		in.MaxRows = 10000
	}

	costWarnings := query.CheckQueryCost(in.SQL)

	resp := s.duck.ExecuteSQL(ctx, query.SQLRequest{
		Query:     in.SQL,
		MaxRows:   in.MaxRows,
		TimeoutMs: in.TimeoutMs,
		Explain:   in.Explain,
	})

	if resp.Error != "" {
		return nil, QueryOut{
			Warnings: costWarnings,
			Schema:   buildSchemaResponse(),
		}, fmt.Errorf("%s", resp.Error)
	}

	if in.Explain {
		return nil, QueryOut{
			QueryPlan:  resp.QueryPlan,
			ExecTimeMs: resp.ExecutionTimeMs,
			Warnings:   costWarnings,
		}, nil
	}

	return nil, QueryOut{
		Results:    resp.Results,
		RowCount:   resp.RowsReturned,
		ExecTimeMs: resp.ExecutionTimeMs,
		Truncated:  resp.RowsReturned >= in.MaxRows,
		Warnings:   costWarnings,
	}, nil
}
