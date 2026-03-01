package mcp

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/query"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// query - Raw SQL escape hatch with schema guidance

type QueryIn struct {
	SQL     string `json:"sql" jsonschema:"SQL query (SELECT/WITH only)"`
	MaxRows int    `json:"max_rows,omitempty" jsonschema:"Max rows to return,default=1000"`
}

type QueryOut struct {
	Results    []query.RowMap `json:"results"`
	RowCount   int            `json:"row_count"`
	ExecTimeMs int64          `json:"exec_time_ms"`
	Truncated  bool           `json:"truncated"`
	Schema     string         `json:"schema,omitempty"`
}

func (s *Server) query(ctx context.Context, req *mcp.CallToolRequest, in QueryIn) (*mcp.CallToolResult, QueryOut, error) {
	if in.SQL == "" {
		// Return schema help
		return nil, QueryOut{
			Results:  []query.RowMap{},
			Schema:   query.GetSchema(s.cfg.LakeDir),
			RowCount: 0,
		}, nil
	}

	if in.MaxRows == 0 {
		in.MaxRows = 1000
	}
	if in.MaxRows > 10000 {
		in.MaxRows = 10000
	}

	resp := s.duck.ExecuteSQL(ctx, query.SQLRequest{
		Query:   in.SQL,
		MaxRows: in.MaxRows,
	})

	if resp.Error != "" {
		return nil, QueryOut{
			Results: []query.RowMap{},
			Schema:  query.GetSchema(s.cfg.LakeDir),
		}, fmt.Errorf("%s", resp.Error)
	}

	return nil, QueryOut{
		Results:    resp.Results,
		RowCount:   resp.RowsReturned,
		ExecTimeMs: resp.ExecutionTimeMs,
		Truncated:  resp.RowsReturned >= in.MaxRows,
	}, nil
}
