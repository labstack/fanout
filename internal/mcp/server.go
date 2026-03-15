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
	// 1. overview — system health entry point
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "overview",
		Description: `System health overview. Start here. Returns composite health score (0–1), per-service status, and top issues.

Workflow: overview → diagnose (problem service) → spans/logs/trace (specific errors)

Params: window ("15m","1h","7d" or ISO range), include (["health","services","issues"]), sort_services_by ("severity","error_rate","latency","throughput")

Returns: health (score, total_services, by_status, throughput_per_min, global_error_rate, global_p95_ms), services (service, status, requests, error_rate, p50_ms, p95_ms), top_issues (service, issue, value, threshold)`,
	}, s.overview)

	// 2. topology — service dependency map
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "topology",
		Description: `Service dependency map with health status, blast radius, and critical paths.

Params: window, edge_type (call|messaging|all), depth (BFS hops from service), service (focus node), include_inactive, namespace, tenant

Returns: nodes (service, status, requests, error_rate, p50_ms, p95_ms, blast_radius, upstream_count, downstream_count), edges (source, target, calls, avg_ms, error_rate, edge_type), critical_paths (top 3 weighted paths)`,
	}, s.topology)

	// 3. spans — span search and aggregation
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "spans",
		Description: `Search, filter, and aggregate trace spans. Supports raw listing or group_by aggregation with percentile latency.

Params: query (substring match), operation (exact), service, status (error|ok|slow|all), kind (server|client|producer|consumer|internal), min_duration_ms, max_duration_ms, attrs (key-value), group_by (service|operation|status|kind|http.method|http.status_code), order_by (time|duration|error_rate|count), include_exemplars, window, namespace, tenant, limit

Returns (ungrouped): spans (trace_id, span_id, service, operation, kind, start_time, duration_ms, status, attributes), total_matched
Returns (grouped): groups (key, count, error_count, error_rate, p50_ms, p95_ms, p99_ms, exemplar_trace_ids), total_groups`,
	}, s.spans)

	// 4. logs — log search and aggregation
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "logs",
		Description: `Search, filter, and aggregate log entries. Supports raw listing or group_by aggregation with sample bodies and trace correlation.

Params: query (substring on body), severity (TRACE|DEBUG|INFO|WARN|ERROR|FATAL), trace_id (correlate to trace), service, attrs (key-value), group_by (service|severity), order_by (time|count|severity), window, namespace, tenant, limit

Returns (ungrouped): logs (time, service, severity, body, trace_id, span_id, attributes), total_matched
Returns (grouped): groups (key, count, sample_bodies, sample_trace_ids), total_groups`,
	}, s.logs)

	// 5. metrics — metric discovery and timeseries query
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "metrics",
		Description: `Discover and query OTLP metric timeseries with anomaly detection. Two actions: 'list' discovers metrics; 'query' returns bucketed timeseries.

Params: action (list|query), name, names (overlay multiple), aggregation (avg|sum|min|max|count), group_by, granularity (1m|5m|15m|1h|auto), service, attrs, window, namespace, tenant, limit

Returns (list): metrics (name, type, unit, services, description)
Returns (query): series (labels, metric, aggregation, unit, datapoints), anomalies (time, type, value, expected, deviation_sigma)`,
	}, s.metrics)

	// 6. trace — distributed trace with root cause analysis
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "trace",
		Description: `Distributed trace with auto root-cause analysis. Shows spans, correlated logs, critical path, and identifies the likely error or latency cause.

Params: trace_id (required), include_logs (default true), include_metrics (adds service_rollup context around trace time), compare_to (another trace_id for side-by-side diff)

Returns: spans (tree with timing/self_time), logs (correlated), critical_path, root_cause (reason, evidence), comparison (when compare_to set), metric_context (when include_metrics set)`,
	}, s.trace)

	// 7. diagnose — multi-signal root cause analysis
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "diagnose",
		Description: `Deep-dive into a service with baseline comparison, change point detection, and log correlation.

Params: service (required), symptom (auto|latency|errors|throughput_drop), window, namespace, tenant

Returns: metrics (p50/p95/p99_ms, error_rate, request_count, comparison_to_baseline), top_errors (message, count, example_trace), slow_operations, dependencies, change_points (time, metric, before, after), correlated_log_patterns (pattern, count, severity)`,
	}, s.diagnose)

	// 8. compare — side-by-side comparison (3 modes)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "compare",
		Description: `Side-by-side comparison with 3 modes. Services mode compares 2-4 services. Time mode compares same service across two windows. Operations mode compares two operations within a service.

Params: mode (services|time|operations), services (for services mode), service (for time/operations), left/right (mode-specific config), focus (["latency","errors","throughput"]), window

Returns: comparison (per-metric left/right values, change_pct, direction, statistically_significant), verdict`,
	}, s.compare)

	// 9. query — raw SQL with DuckDB views
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "query",
		Description: queryToolDescription(s.cfg.LakeDir),
	}, s.query)
}

func queryToolDescription(lakeDir string) string {
	return strings.ReplaceAll(`Execute raw SQL against DuckDB. For advanced analysis not covered by other tools. Omit sql to get structured schema reference.

DuckDB Views (clean column names — use these instead of raw Parquet):
- spans: trace_id, span_id, service, operation, kind, start_time, end_time, duration_ms, status, status_message, attributes_json, resource_json, events_json, namespace, tenant
- logs: time, severity, body, service, trace_id, span_id, attributes_json, resource_json, namespace, tenant
- metrics: time, name, type, value, unit, service, description, attributes_json, resource_json, namespace, tenant

Rollup tables:
- service_rollup: bucket, service, spans, error_rate, p50_ms, p95_ms, log_count, metric_count
- edge_rollup: bucket, caller, callee, calls, avg_ms, error_rate, edge_type

Macro: attr(json_col, 'key') — extracts JSON key from attributes_json

Params: sql, explain (returns query plan), max_rows (default 1000), timeout_ms (default 30000)

Example: SELECT service, approx_quantile(duration_ms, 0.95) as p95 FROM spans WHERE start_time > now() - INTERVAL 15 MINUTE GROUP BY service ORDER BY p95 DESC`, "{LAKE}", lakeDir)
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
