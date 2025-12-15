package mcp

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
	mcp  *mcp.Server
	svc  *service.Service
	duck *query.Duck
	cfg  config.Config
}

func NewServer(svc *service.Service, duck *query.Duck, cfg config.Config) *Server {
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "fanout",
		Version: "1.0.0",
	}, nil)

	s := &Server{
		mcp:  mcpServer,
		svc:  svc,
		duck: duck,
		cfg:  cfg,
	}

	s.registerTools()
	return s
}

func (s *Server) RegisterRoutes(e *echo.Echo) {
	handler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		return s.mcp
	}, nil)
	e.Any("/mcp", echo.WrapHandler(handler))
}

func (s *Server) registerTools() {
	// 1. status - Entry point, zero-config health overview
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "status",
		Description: "Get system health overview. Start here. Returns service health counts, top issues, and key metrics. No parameters required.",
	}, s.status)

	// 2. diagnose - Deep-dive into a specific service
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "diagnose",
		Description: "Deep-dive into a service. Returns P50/P95/P99 latency, error rate, top errors with example traces, slow operations, and downstream dependencies.",
	}, s.diagnose)

	// 3. find - Unified span/log search
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "find",
		Description: "Search spans and logs. Filter by pattern, service, status (error/slow), severity. Returns matching spans/logs with trace IDs for deeper investigation.",
	}, s.find)

	// 4. trace - Request journey with auto root cause
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "trace",
		Description: "Get complete distributed trace with auto root-cause analysis. Shows all spans, correlated logs, critical path, and identifies the likely cause of errors or latency.",
	}, s.trace)

	// 5. timeline - Events with anomaly detection
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "timeline",
		Description: "Get time-bucketed metrics with automatic anomaly detection. Identifies latency spikes, error rate increases, and traffic drops.",
	}, s.timeline)

	// 6. topology - Service map with health
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "topology",
		Description: "Get service dependency map. Shows all services as nodes with health status, and edges representing inter-service calls with call counts and error rates.",
	}, s.topology)

	// 7. query - Raw SQL escape hatch
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "query",
		Description: "Execute raw SQL against the data lake. For advanced analysis not covered by other tools. Call with empty sql to get schema reference.",
	}, s.query)
}
