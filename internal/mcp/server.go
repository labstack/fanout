package mcp

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
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

// MCP returns the inner MCP server for in-process client connections.
func (s *Server) MCP() *mcp.Server { return s.mcp }

func NewServer(svc *service.Service, duck *query.Duck, cfg config.Config) *Server {
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "fanout",
		Version: "1.0.0",
	}, nil)

	// Initialize report store with configured lake dir
	InitReportStore(cfg.LakeDir)

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
	}, &mcp.StreamableHTTPOptions{
		Stateless: true, // Survives server restarts - no session persistence needed
	})
	e.Any("/mcp", echo.WrapHandler(handler))

	// Shareable report view
	e.GET("/view/r/:id", s.viewReport)

	// Report management
	e.GET("/reports", s.listReports)
	e.GET("/api/reports", s.apiListReports)
	e.DELETE("/api/reports/:id", s.apiDeleteReport)
}

func (s *Server) viewReport(c *echo.Context) error {
	id := c.Param("id")
	report := GetReport(id)
	if report == nil {
		return c.HTML(http.StatusNotFound, notFoundHTML)
	}
	return c.HTML(http.StatusOK, wrapReportHTML(report))
}

func (s *Server) listReports(c *echo.Context) error {
	reports := ListReports()
	return c.HTML(http.StatusOK, renderReportsPage(reports))
}

func (s *Server) apiListReports(c *echo.Context) error {
	reports := ListReports()
	return c.JSON(http.StatusOK, reports)
}

func (s *Server) apiDeleteReport(c *echo.Context) error {
	id := c.Param("id")
	if DeleteReport(id) {
		return c.JSON(http.StatusOK, map[string]string{"status": "deleted"})
	}
	return c.JSON(http.StatusNotFound, map[string]string{"error": "not found"})
}

func (s *Server) registerTools() {
	// 1. overview - Entry point, zero-config health overview with health score
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "overview",
		Description: `Get system health overview with health score. Start here. Returns a composite health score (0–1), per-service status, top issues, and global metrics.

Workflow: overview → diagnose (problem service) → find/trace (specific errors)

Params: window ("15m","1h","7d" or ISO range), include (["health","services","issues"]), sort_services_by ("severity","error_rate","latency","throughput")

Returns: health (score, total_services, by_status, throughput_per_min, global_error_rate, global_p95_ms), services (service, status, requests, error_rate, p50_ms, p95_ms), top_issues (service, issue, value, threshold)`,
	}, s.overview)

	// 2. diagnose - Deep-dive into a specific service
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "diagnose",
		Description: `Deep-dive into a service. Returns P50/P95/P99 latency, error rate, top errors with example traces, slow operations, and downstream dependencies.

Use after status identifies a problem service.

Returns: metrics (p50/p95/p99_ms, error_rate, request_count), top_errors (message, count, example_trace), slow_operations (name, p95_ms, count), dependencies (service, status, error_rate, p95_ms, calls)`,
	}, s.diagnose)

	// 3. find - Unified span/log search
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "find",
		Description: `Search spans and logs. Filter by pattern, service, status (error/slow), severity. Returns matching spans/logs with trace IDs for deeper investigation.

Use to find specific errors or patterns. Pass trace_id to trace tool for full context.

Returns: spans (trace_id, span_id, service, operation, duration_ms, status), logs (ts, service, severity, body, trace_id), suggestion (next action hint)`,
	}, s.find)

	// 4. trace - Request journey with auto root cause
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "trace",
		Description: `Get complete distributed trace with auto root-cause analysis. Shows all spans, correlated logs, critical path, and identifies the likely cause of errors or latency.

Use with trace_id from find or diagnose results.

Returns: spans (full tree with timing), logs (correlated by trace), critical_path (slowest chain), root_cause (identified issue with confidence)`,
	}, s.trace)

	// 5. timeline - Events with anomaly detection
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "timeline",
		Description: `Get time-bucketed metrics with automatic anomaly detection. Identifies latency spikes, error rate increases, and traffic drops.

Use for trend analysis and finding when issues started.

Returns: buckets (time, request_count, error_count, p95_ms, error_rate, is_anomaly), anomalies (time, type, description, value, expected)`,
	}, s.timeline)

	// 6. topology - Service map with health
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "topology",
		Description: `Get service dependency map. Shows all services as nodes with health status, and edges representing inter-service calls with call counts and error rates.

Use to understand service relationships and identify dependency issues.

Returns: nodes (service, status, request_count, error_rate, p95_ms), edges (source, target, calls, error_rate)`,
	}, s.topology)

	// 7. query - Raw SQL escape hatch
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "query",
		Description: queryToolDescription(s.cfg.LakeDir),
	}, s.query)

	// 8. schema - Database schema reference for LLM
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "schema",
		Description: `Get database schema reference with table definitions and example queries. Use this to understand the data model before writing SQL queries.

Call this before writing complex SQL to get full column details and working examples.

Returns: schema (markdown with all tables/columns), examples (working SQL queries for common tasks)`,
	}, s.schema)

	// 9. compare - Side-by-side service comparison
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "compare",
		Description: `Compare 2-4 services side-by-side. Returns requests, error rate, P50/P95 latency for each service with winner determination.

Use to benchmark services or compare before/after deployments.

Returns: services (array with service, requests, error_rate, p50_ms, p95_ms), winner (best performing service), summary`,
	}, s.compare)
}

func queryToolDescription(lakeDir string) string {
	return strings.ReplaceAll(`Execute raw SQL against the data lake. For advanced analysis not covered by other tools. Call with empty sql to get schema reference.

Tables:
- service_rollup: Pre-aggregated 1-min buckets (fastest)
  Columns: bucket (TIMESTAMP), service, spans, p50_ms, p95_ms, error_rate

- read_parquet('{LAKE}/spans/**/*.parquet'): Raw trace spans
  Key columns (quote as "name=..."): trace_id, span_id, service_name, name, duration_ms, status_code, start_unix_nano, attributes_json

- read_parquet('{LAKE}/logs/**/*.parquet'): Log entries
  Key columns: time_unix_nano, severity, body, service_name, trace_id

- read_parquet('{LAKE}/metrics/**/*.parquet'): Metric points
  Key columns: time_unix_nano, name, mtype, service_name, value

Time filter (last N minutes):
WHERE "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - N*60) * 1000000000

Example - top endpoints by P95:
SELECT "name=name", COUNT(*) as cnt, ROUND(quantile_cont("name=duration_ms", 0.95), 2) as p95
FROM read_parquet('{LAKE}/spans/**/*.parquet', union_by_name=true)
WHERE "name=start_unix_nano" >= (EXTRACT(EPOCH FROM NOW()) - 900) * 1000000000
GROUP BY "name=name" ORDER BY p95 DESC LIMIT 10`, "{LAKE}", lakeDir)
}

const notFoundHTML = `<!DOCTYPE html>
<html><head><title>Report Not Found</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/@shoelace-style/shoelace@2.20.1/cdn/themes/light.css"/>
</head><body style="padding:2rem;font-family:system-ui">
<sl-alert variant="warning" open><sl-icon slot="icon" name="exclamation-triangle"></sl-icon>Report not found or expired.</sl-alert>
</body></html>`

func wrapReportHTML(r *Report) string {
	safeTitle := html.EscapeString(r.Query)
	return fmt.Sprintf(`<!DOCTYPE html>
<html><head>
<title>%s - Fanout Report</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/@shoelace-style/shoelace@2.20.1/cdn/themes/light.css"/>
<link rel="stylesheet" href="/css/components.css"/>
<script type="module" src="https://cdn.jsdelivr.net/npm/@shoelace-style/shoelace@2.20.1/cdn/shoelace-autoloader.js"></script>
<script src="https://cdn.jsdelivr.net/npm/vega@6"></script>
<script src="https://cdn.jsdelivr.net/npm/vega-lite@6"></script>
<script src="https://cdn.jsdelivr.net/npm/vega-embed@7"></script>
<style>
:root {
  --accent: #3b82f6;
  --accent-light: #dbeafe;
  --success: #22c55e;
  --success-light: #dcfce7;
  --warning: #f59e0b;
  --warning-light: #fef3c7;
  --danger: #ef4444;
  --danger-light: #fee2e2;
  --text-primary: #0f172a;
  --text-secondary: #64748b;
  --text-muted: #94a3b8;
  --border: #e2e8f0;
  --border-color: #e2e8f0;
  --bg-card: #ffffff;
  --bg-page: #f8fafc;
  --bg-tertiary: #f1f5f9;
  --shadow: 0 1px 3px rgba(0,0,0,0.1), 0 1px 2px rgba(0,0,0,0.06);
  --shadow-lg: 0 4px 6px rgba(0,0,0,0.1), 0 2px 4px rgba(0,0,0,0.06);
}

* { box-sizing: border-box; }
body {
  font-family: system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  padding: 2rem;
  max-width: 1200px;
  margin: 0 auto;
  background: var(--bg-page);
  color: var(--text-primary);
  line-height: 1.5;
}

h1 { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.5rem 0; }
.meta {
  color: var(--text-secondary);
  font-size: 0.8rem;
  margin-bottom: 1.5rem;
  padding-bottom: 1rem;
  border-bottom: 1px solid var(--border);
  display: flex;
  gap: 1rem;
}

/* Compose */
.compose { display: flex; gap: 1rem; }
.compose-column { flex-direction: column; }
.compose-row { flex-direction: row; flex-wrap: wrap; }

/* Grid */
.grid { display: grid; gap: 0.75rem; }
.grid-2 { grid-template-columns: repeat(2, 1fr); }
.grid-3 { grid-template-columns: repeat(3, 1fr); }
.grid-4 { grid-template-columns: repeat(4, 1fr); }
@media (max-width: 768px) {
  .grid-3, .grid-4 { grid-template-columns: repeat(2, 1fr); }
}
@media (max-width: 480px) {
  .grid-2, .grid-3, .grid-4 { grid-template-columns: 1fr; }
}

/* Metric */
.metric {
  text-align: center;
  padding: 1rem;
  background: var(--bg-card);
  border-radius: 0.75rem;
  border: 1px solid var(--border);
  box-shadow: var(--shadow);
  transition: transform 0.15s, box-shadow 0.15s;
}
.metric:hover { transform: translateY(-2px); box-shadow: var(--shadow-lg); }
.metric-value {
  font-size: 1.75rem;
  font-weight: 700;
  color: var(--text-primary);
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.25rem;
}
.metric-value sl-icon { font-size: 1rem; }
.metric-value sl-icon[name="arrow-up"] { color: var(--success); }
.metric-value sl-icon[name="arrow-down"] { color: var(--danger); }
.metric-label {
  font-size: 0.7rem;
  color: var(--text-secondary);
  text-transform: uppercase;
  letter-spacing: 0.05em;
  margin-top: 0.5rem;
  font-weight: 500;
}

/* Table */
.table {
  width: 100%%;
  border-collapse: collapse;
  margin: 0.5rem 0;
  background: var(--bg-card);
  border-radius: 0.75rem;
  overflow: hidden;
  box-shadow: var(--shadow);
}
.table caption {
  padding: 1rem 1.25rem;
  font-weight: 600;
  font-size: 0.95rem;
  text-align: left;
  background: linear-gradient(to bottom, #f8fafc, #f1f5f9);
  border-bottom: 1px solid var(--border);
}
.table th, .table td {
  padding: 0.875rem 1.25rem;
  text-align: left;
  border-bottom: 1px solid var(--border);
}
.table th {
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-secondary);
  font-weight: 600;
  background: #f8fafc;
}
.table td { font-size: 0.9rem; }
.table tr:last-child td { border-bottom: none; }
.table tr:hover td { background: #f8fafc; }
.table tbody tr { transition: background 0.15s; }

/* Text styles */
.text-bold { font-weight: 600; display: block; padding: 0.5rem 0; }
.text-dim { color: var(--text-secondary); font-size: 0.875rem; display: block; padding: 0.5rem 0; }
.text-warning { color: var(--warning); font-weight: 500; display: block; padding: 0.5rem 0; }
.text-error { color: var(--danger); font-weight: 500; display: block; padding: 0.5rem 0; }
.text-success { color: var(--success); font-weight: 500; display: block; padding: 0.5rem 0; }

/* Sparkline */
.sparkline {
  padding: 0.875rem 1rem;
  background: var(--bg-card);
  border-radius: 0.5rem;
  border: 1px solid var(--border);
  margin: 0.375rem 0;
  display: flex;
  align-items: center;
  gap: 1rem;
}
.sparkline span {
  font-size: 0.8rem;
  color: var(--text-secondary);
  min-width: 80px;
  font-weight: 500;
}
.sparkline svg {
  flex: 1;
  height: 24px;
}
.sparkline svg polyline { stroke-width: 2; }

/* Bar container */
.bar-container {
  display: flex;
  align-items: center;
  gap: 1rem;
  padding: 0.75rem 0;
  border-bottom: 1px solid var(--border);
}
.bar-container:last-child { border-bottom: none; }
.bar-label {
  min-width: 100px;
  font-size: 0.85rem;
  color: var(--text-secondary);
  font-weight: 500;
}
.bar-container sl-progress-bar { flex: 1; --height: 8px; }
.bar-value {
  min-width: 60px;
  text-align: right;
  font-size: 0.85rem;
  font-weight: 600;
  color: var(--text-primary);
}

/* Tree */
.tree-value { font-family: 'SF Mono', Monaco, monospace; color: var(--accent); font-size: 0.85rem; }
sl-tree { --indent-size: 1.25rem; }
sl-tree-item::part(label) { font-size: 0.875rem; }
sl-tree-item::part(item) { padding: 0.25rem 0; }

/* Cards */
sl-card { margin-bottom: 1rem; }
sl-card::part(base) {
  background: var(--bg-card);
  border-radius: 0.75rem;
  box-shadow: var(--shadow);
  border: 1px solid var(--border);
}
sl-card::part(header) {
  padding: 1rem 1.25rem;
  border-bottom: 1px solid var(--border);
  background: linear-gradient(to bottom, #fafbfc, #f8fafc);
}
sl-card::part(body) { padding: 1.25rem; }
sl-card [slot="header"] { font-weight: 600; font-size: 0.95rem; }

/* Badges */
sl-badge {
  margin-right: 0.5rem;
  margin-bottom: 0.5rem;
  font-weight: 500;
}
sl-badge::part(base) {
  padding: 0.375rem 0.75rem;
  font-size: 0.8rem;
  border-radius: 9999px;
}

/* Charts */
.chart {
  min-height: 220px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #fafbfc;
  border-radius: 0.5rem;
  padding: 1rem;
}
.chart canvas, .chart svg { max-width: 100%%; }
.vega-embed { width: 100%%; }
.vega-embed .vega-actions { display: none; }
</style>
</head><body>
<h1>%s</h1>
<div class="meta"><span>Generated: %s</span><span>ID: %s</span></div>
%s
<script>
document.querySelectorAll('.chart[data-vega]').forEach(el => {
  try {
    const spec = JSON.parse(el.dataset.vega.replace(/&quot;/g, '"'));
    spec.autosize = { type: 'fit', contains: 'padding' };
    vegaEmbed(el, spec, { actions: false, renderer: 'svg' });
  } catch(e) { console.error('Vega error:', e); }
});
</script>
</body></html>`, safeTitle, safeTitle, r.CreatedAt.Format("2006-01-02 15:04:05"), r.ID, r.HTML)
}

func renderReportsPage(rpts []*Report) string {
	var rows string
	for _, r := range rpts {
		expired := ""
		if time.Now().After(r.ExpiresAt) {
			expired = `<sl-icon name="exclamation-circle" style="color:var(--danger)"></sl-icon>`
		}
		safeQuery := html.EscapeString(r.Query)
		rows += fmt.Sprintf(`<tr>
			<td><a href="/view/r/%s">%s</a></td>
			<td>%s</td>
			<td>%s %s</td>
			<td>
				<sl-button size="small" href="/view/r/%s"><sl-icon name="eye"></sl-icon></sl-button>
				<sl-button size="small" variant="danger" onclick="deleteReport('%s')"><sl-icon name="trash"></sl-icon></sl-button>
			</td>
		</tr>`, r.ID, safeQuery, r.CreatedAt.Format("2006-01-02 15:04"), r.ExpiresAt.Format("2006-01-02 15:04"), expired, r.ID, r.ID)
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html><head>
<title>Reports - Fanout</title>
<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/@shoelace-style/shoelace@2.20.1/cdn/themes/light.css"/>
<script type="module" src="https://cdn.jsdelivr.net/npm/@shoelace-style/shoelace@2.20.1/cdn/shoelace-autoloader.js"></script>
<style>
:root {
  --accent: #3b82f6;
  --danger: #ef4444;
  --text-primary: #0f172a;
  --text-secondary: #64748b;
  --border: #e2e8f0;
  --bg-card: #ffffff;
  --bg-page: #f8fafc;
}
* { box-sizing: border-box; }
body {
  font-family: system-ui, -apple-system, sans-serif;
  padding: 2rem;
  max-width: 1200px;
  margin: 0 auto;
  background: var(--bg-page);
  color: var(--text-primary);
}
h1 {
  font-size: 1.5rem;
  font-weight: 700;
  margin: 0 0 1.5rem 0;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.table {
  width: 100%%;
  border-collapse: collapse;
  background: var(--bg-card);
  border-radius: 0.75rem;
  overflow: hidden;
  box-shadow: 0 1px 3px rgba(0,0,0,0.1);
}
.table th, .table td {
  padding: 1rem 1.25rem;
  text-align: left;
  border-bottom: 1px solid var(--border);
}
.table th {
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--text-secondary);
  font-weight: 600;
  background: #f8fafc;
}
.table td { font-size: 0.9rem; }
.table tr:last-child td { border-bottom: none; }
.table tr:hover td { background: #f8fafc; }
.table a { color: var(--accent); text-decoration: none; font-weight: 500; }
.table a:hover { text-decoration: underline; }
.empty {
  text-align: center;
  padding: 3rem;
  color: var(--text-secondary);
}
sl-button { margin-right: 0.25rem; }
</style>
</head><body>
<h1><sl-icon name="file-earmark-bar-graph"></sl-icon> Reports</h1>
<table class="table">
<thead><tr><th>Title</th><th>Created</th><th>Expires</th><th>Actions</th></tr></thead>
<tbody>%s</tbody>
</table>
%s
<script>
async function deleteReport(id) {
  if (!confirm('Delete this report?')) return;
  const res = await fetch('/api/reports/' + id, { method: 'DELETE' });
  if (res.ok) location.reload();
}
</script>
</body></html>`, rows, func() string {
		if len(rpts) == 0 {
			return `<div class="empty"><sl-icon name="inbox" style="font-size:3rem"></sl-icon><p>No reports yet</p></div>`
		}
		return ""
	}())
}
