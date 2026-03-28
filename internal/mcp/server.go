package mcp

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/alert"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
	mcp    *mcp.Server
	svc    *service.Service
	duck   *query.Duck
	cfg    config.Config
	alerts *alert.Engine
}

// MCP returns the inner MCP server for in-process client connections.
func (s *Server) MCP() *mcp.Server { return s.mcp }

func NewServer(svc *service.Service, duck *query.Duck, cfg config.Config, alerts *alert.Engine) *Server {
	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "fanout",
		Version: "1.0.0",
	}, nil)

	// Initialize report store with configured lake dir
	InitReportStore(cfg.LakeDir)

	s := &Server{
		mcp:    mcpServer,
		svc:    svc,
		duck:   duck,
		cfg:    cfg,
		alerts: alerts,
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
		Description: `System health overview. Start here for any investigation.

When to use: First tool to call. Gives you the lay of the land — which services exist, which have issues.
Workflow: overview → diagnose(problem service) → trace(suggested_traces) → logs(trace_id)
Gotchas:
- sort_services_by="severity" (default) surfaces problems first; use "throughput" for traffic-based ranking.
- Returns at most 100 services. Use limit parameter to increase.

Params: window ("15m","1h","7d" or ISO range), include (["health","services","issues"]), sort_services_by ("severity","error_rate","latency","throughput"), namespace, tenant, limit (default 100)

Returns: health (score, total_services, by_status, throughput_per_min, global_error_rate, global_p95_ms), services (service, status, requests, error_rate, p50_ms, p95_ms), top_issues (service, issue, value, threshold)`,
	}, wrap("overview", s.overview))

	// 2. topology — service dependency map
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "topology",
		Description: `Service dependency map with health status, blast radius, and critical paths.

When to use: To understand which services call which, identify blast radius, or find critical dependency chains.
Workflow: topology → diagnose(unhealthy node) or topology(service=X, depth=2) to zoom in on a subgraph.
Gotchas:
- edge_type="messaging" shows async producer/consumer links; "call" shows synchronous RPC.
- blast_radius indicates how many downstream services are affected if this node fails.

Params: window, edge_type (call|messaging|all), depth (BFS hops from service), service (focus node), include_inactive, namespace, tenant

Returns: nodes (service, status, requests, error_rate, p50_ms, p95_ms, blast_radius, upstream_count, downstream_count), edges (source, target, calls, avg_ms, error_rate, edge_type), critical_paths (top 3 weighted paths)`,
	}, wrap("topology", s.topology))

	// 3. spans — span search and aggregation
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "spans",
		Description: `Search, filter, and aggregate trace spans.

When to use: To find specific spans matching criteria, or to get aggregated latency/error stats by service or operation.
Workflow: spans(service=X, status=error) → trace(trace_id) for full context. Use group_by for patterns before drilling in.
Gotchas:
- Without group_by, returns raw spans (use limit to control volume). With group_by, returns aggregated stats with percentiles.
- Use attributes tool first to discover filterable attribute keys for attrs parameter.
- status="slow" filters spans above service P95 baseline.

Params: query (substring match), operation (exact), service, status (error|ok|slow|all), kind (server|client|producer|consumer|internal), min_duration_ms, max_duration_ms, attrs (key-value), group_by (service|operation|status|kind|http.method|http.status_code), order_by (time|duration|error_rate|count), include_exemplars, window, namespace, tenant, limit

Returns (ungrouped): spans (trace_id, span_id, service, operation, kind, start_time, duration_ms, status, attributes), total_matched
Returns (grouped): groups (key, count, error_count, error_rate, p50_ms, p95_ms, p99_ms, exemplar_trace_ids), total_groups`,
	}, wrap("spans", s.spans))

	// 4. logs — log search and aggregation
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "logs",
		Description: `Search, filter, and aggregate log entries.

When to use: To find logs by pattern, severity, or trace correlation. Use after trace tool to get logs for a specific request.
Workflow: logs(trace_id=X) for request logs. logs(severity=["ERROR","FATAL"], service=X) → trace(trace_id) for error investigation.
Gotchas:
- severity is an array — pass ["ERROR", "FATAL"] for multiple levels.
- group_by=["service","severity"] gives a heatmap of log volume by service and level.
- Use attributes tool first to discover filterable attribute keys.

Params: query (substring on body), severity (TRACE|DEBUG|INFO|WARN|ERROR|FATAL), trace_id (correlate to trace), service, attrs (key-value), group_by (service|severity), order_by (time|count|severity), window, namespace, tenant, limit

Returns (ungrouped): logs (time, service, severity, body, trace_id, span_id, attributes), total_matched
Returns (grouped): groups (key, count, sample_bodies, sample_trace_ids), total_groups`,
	}, wrap("logs", s.logs))

	// 5. metrics — metric discovery and timeseries query
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "metrics",
		Description: `Discover and query OTLP metric timeseries with anomaly detection.

When to use: For metric-based investigation — CPU, memory, request rates, custom business metrics. Start with action="list" to discover what metrics exist.
Workflow: metrics(action=list) → metrics(action=query, name=X) → anomalies in response highlight spikes/drops.
Gotchas:
- Cumulative sum metrics (type="sum") are auto-converted to per-bucket deltas — you see rates, not raw counters.
- action="histogram" returns bucket distributions; action="exemplars" returns trace links from histogram exemplars.
- names=["metric1","metric2"] overlays multiple metrics in one query for comparison.

Params: action (list|query|histogram|exemplars), name, names (overlay multiple), aggregation (avg|sum|min|max|count), group_by, granularity (1m|5m|15m|1h|auto), service, attrs, window, namespace, tenant, limit

Returns (list): metrics (name, type, unit, services, description)
Returns (query): series (labels, metric, aggregation, unit, datapoints), anomalies (time, type, value, expected, deviation_sigma)`,
	}, wrap("metrics", s.metrics))

	// 6. trace — distributed trace with root cause analysis
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "trace",
		Description: `Distributed trace with auto root-cause analysis.

When to use: When you have a trace_id from spans, logs, diagnose, or metrics exemplars. Shows the full request journey with timing breakdown.
Workflow: trace(trace_id) → check root_cause → if latency issue, compare with trace(trace_id, compare_to=healthy_trace_id).
Gotchas:
- include_metrics=true adds service_rollup context around the trace's time — useful for seeing if the trace was during a spike.
- critical_path shows spans that consumed the most wall-clock time relative to the trace duration.
- compare_to gives a side-by-side diff highlighting which operations changed.

Params: trace_id (required), include_logs (default true), include_metrics (adds service_rollup context around trace time), compare_to (another trace_id for side-by-side diff), window

Returns: spans (tree with timing/self_time), logs (correlated), critical_path, root_cause (reason, evidence), comparison (when compare_to set), metric_context (when include_metrics set)`,
	}, wrap("trace", s.trace))

	// 7. diagnose — multi-signal root cause analysis
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "diagnose",
		Description: `Deep-dive into a service with baseline comparison, change point detection, and log correlation.

When to use: After overview identifies a problem service. For broad "what's wrong?" exploration — use spans/logs for specific known errors.
Workflow: overview → diagnose(service) → trace(suggested_traces[0]) → logs(trace_id) for full investigation.
Gotchas:
- symptom="auto" (default) detects the dominant issue; specify "latency" or "errors" to force focus.
- suggested_traces contains trace IDs ready for the trace tool — always use them for follow-up.
- change_points show when metrics shifted — feed the timestamp to compare(mode=time) for before/after.

Params: service (required), symptom (auto|latency|errors|throughput_drop), window, namespace, tenant

Returns: metrics (p50/p95/p99_ms, error_rate, request_count, comparison_to_baseline), top_errors (operation, message, exception_type, count, example_trace), slow_operations, dependencies, change_points, correlated_log_patterns, suggested_traces`,
	}, wrap("diagnose", s.diagnose))

	// 8. compare — side-by-side comparison (3 modes)
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "compare",
		Description: `Side-by-side comparison with 3 modes.

When to use: To quantify differences — between services, time windows, or operations — with statistical significance.
Workflow: diagnose → compare(mode=time, left.window=before_change, right.window=after_change) to confirm a regression.

Modes:
- services: Compare 2-4 services side-by-side. Pass services=["svc1","svc2"].
- time: Compare same service across two ISO time windows. Pass service, left.window, right.window.
- operations: Compare two operations within a service. Pass service, left.operation, right.operation.

Gotchas:
- Time mode requires ISO range windows (e.g., "2026-03-17T10:00:00Z/2026-03-17T11:00:00Z"), not durations.
- statistically_significant=true in comparison means the change is likely real, not noise.

Params: mode (services|time|operations), services, service, left/right, focus (["latency","errors","throughput"]), window
Returns: comparison (per-metric left/right values, change_pct, direction, statistically_significant), verdict`,
	}, wrap("compare", s.compare))

	// 9. attributes — attribute discovery
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "attributes",
		Description: `Discover what OTel attributes exist in the data.

When to use: Before using attrs={} filters on spans/logs/metrics tools. Tells you what keys are available and their value distributions.
Workflow: attributes(signal=spans, service=X) → spans(service=X, attrs={"http.status_code":"500"})
Gotchas:
- For spans, uses pre-extracted columns (fast, exact counts). For logs/metrics, samples 1000 rows from JSON (approximate counts).
- Additional span attributes may exist in the JSON blob beyond the pre-extracted columns — use query tool with json_keys(attributes_json) to discover them.

Params: signal (spans|logs|metrics, default: spans), service, operation (spans only), window (default: 1h), namespace, tenant, limit (default: 50)

Returns: attributes (key, count, cardinality, samples[]), resource_attributes (key, count, cardinality, samples[])`,
	}, wrap("attributes", s.attributes))

	// 10. query — raw SQL with DuckDB views
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "query",
		Description: queryToolDescription(s.cfg.LakeDir),
	}, wrap("query", s.query))

	// 11. alert_rules
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "alert_rules",
		Description: `Manage alert rules. Create, update, delete, enable/disable, test expressions and webhooks.

Actions: create, list, get, update, delete, enable, disable, test, test_webhook
Expressions use: error_rate, p50, p95, p99, throughput, log_count, z_score, health_score, error_rate_delta, p95_delta, throughput_delta
Use alert_env tool to see live values and example expressions.`,
	}, wrap("alert_rules", s.alertRules))

	// 12. alerts
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "alerts",
		Description: `View current and recent alerts.

Params: state (firing|pending|resolved|all), service, rule_id
Returns: alert list with delivery status + summary counts`,
	}, wrap("alerts", s.alertsList))

	// 13. alert_env
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "alert_env",
		Description: `Show available expression fields and live values for a service.

Use before creating rules to see available data and current values.
Params: service (optional)
Returns: field definitions, live values, example expressions`,
	}, wrap("alert_env", s.alertEnv))
}

func queryToolDescription(lakeDir string) string {
	return strings.ReplaceAll(`Raw SQL against DuckDB. Escape hatch for analysis not covered by other tools.

When to use: Only when other tools can't answer the question. Prefer overview/diagnose/spans/logs/metrics for standard queries.
Workflow: query(sql="") to get schema reference → write query using view/column names from schema → query(sql=...).
Gotchas:
- Use the views (spans, logs, metrics) not raw Parquet. Views have clean column names.
- Always add a time filter (WHERE start_time > now() - INTERVAL ...) to avoid full scans.
- Avoid GROUP BY trace_id, span_id, or attributes_json — these are high-cardinality and will be slow.
- Use attr(attributes_json, 'key') macro to extract JSON attributes.

DuckDB Views (clean column names):
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
