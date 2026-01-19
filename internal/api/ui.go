package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v4"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/render"
	"github.com/labstack/fanout/internal/search"
	"github.com/labstack/fanout/internal/web"
	"golang.org/x/sync/errgroup"
)

// UIHandler handles all UI routes
type UIHandler struct {
	duck *query.Duck
	cfg  config.Config
}

// NewUIHandler creates a new UI handler
func NewUIHandler(duck *query.Duck, cfg config.Config) *UIHandler {
	return &UIHandler{duck: duck, cfg: cfg}
}

// RegisterUIRoutes registers all UI routes
func RegisterUIRoutes(e *echo.Echo, duck *query.Duck, cfg config.Config) {
	h := NewUIHandler(duck, cfg)

	// Full page routes
	e.GET("/", h.Overview)
	e.GET("/services", h.Services)
	e.GET("/services/:name", h.ServiceDetail)
	e.GET("/topology", h.Topology)
	e.GET("/traces", h.Traces)
	e.GET("/traces/:id", h.TraceDetail)
	e.GET("/logs", h.Logs)
	e.GET("/metrics", h.Metrics)

	// API routes
	e.GET("/api/namespaces", h.Namespaces)

	// Component CSS (dynamic from registry)
	e.GET("/css/components.css", ComponentCSS)

	// htmx partial routes
	RegisterPartialRoutes(e, h)
}

// Namespaces returns discovered namespaces from the data
func (h *UIHandler) Namespaces(c echo.Context) error {
	namespaces := h.discoverNamespaces()
	return c.JSON(200, namespaces)
}

// ComponentCSS serves combined CSS from all registered components
func ComponentCSS(c echo.Context) error {
	css := render.AllCSS()
	c.Response().Header().Set("Content-Type", "text/css")
	c.Response().Header().Set("Cache-Control", "public, max-age=3600")
	return c.String(200, css)
}

// Overview renders the main dashboard
func (h *UIHandler) Overview(c echo.Context) error {
	ctx := c.Request().Context()
	window := parseWindow(c)
	namespace := parseNamespace(c)

	data := web.OverviewData{Window: window}

	// Run queries in parallel
	var status statusResult
	var topo topoResult
	var timeline timelineResult

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		status = h.getStatus(gctx, window, namespace)
		return nil
	})

	g.Go(func() error {
		topo = h.getTopology(gctx, window, namespace)
		return nil
	})

	g.Go(func() error {
		timeline = h.getTimeline(gctx, "", window, 5, namespace)
		return nil
	})

	if err := g.Wait(); err != nil {
		return err
	}

	// Build response from parallel results
	data.Healthy = status.Healthy
	data.Summary = status.Summary
	data.Services = web.ServiceSummary{
		Total:     status.Services.Total,
		Healthy:   status.Services.Healthy,
		Degraded:  status.Services.Degraded,
		Unhealthy: status.Services.Unhealthy,
	}
	data.ThroughputPerMin = status.ThroughputPerMin
	data.P95Ms = status.P95Ms
	data.ErrorRate = status.ErrorRate

	for _, issue := range status.TopIssues {
		data.TopIssues = append(data.TopIssues, web.TopIssue{
			Service: issue.Service,
			Issue:   issue.Issue,
			Value:   issue.Value,
			Detail:  issue.Detail,
		})
	}

	for _, n := range topo.Nodes {
		data.Topology.Nodes = append(data.Topology.Nodes, web.ServiceNode{
			Name:      n.Name,
			Status:    n.Status,
			SpanCount: n.SpanCount,
			P95Ms:     n.P95Ms,
			ErrorRate: n.ErrorRate,
		})
	}

	for _, b := range timeline.Buckets {
		data.Timeline.Buckets = append(data.Timeline.Buckets, web.TimelineBucket{
			Time:         b.Time,
			RequestCount: b.RequestCount,
			ErrorCount:   b.ErrorCount,
			P95Ms:        b.P95Ms,
			ErrorRate:    b.ErrorRate,
			IsAnomaly:    b.IsAnomaly,
		})
	}
	for _, a := range timeline.Anomalies {
		data.Timeline.Anomalies = append(data.Timeline.Anomalies, web.Anomaly{
			Time:        a.Time,
			Type:        a.Type,
			Description: a.Description,
		})
	}

	return renderTempl(c, web.Overview(data))
}

// Services renders the services list page
func (h *UIHandler) Services(c echo.Context) error {
	ctx := c.Request().Context()
	filter := c.QueryParam("filter")
	window := parseWindow(c)
	namespace := parseNamespace(c)

	var topo topoResult
	var trends map[string][]int64

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		topo = h.getTopology(gctx, window, namespace)
		return nil
	})
	g.Go(func() error {
		trends = h.getServiceTrends(gctx, window, namespace)
		return nil
	})
	if err := g.Wait(); err != nil {
		return err
	}

	data := web.ServicesData{Filter: filter, Window: window}
	for _, n := range topo.Nodes {
		// Apply filter
		switch filter {
		case "errors":
			if n.ErrorRate < 0.01 {
				continue
			}
		case "slow":
			if n.P95Ms < 500 {
				continue
			}
		}
		data.Services = append(data.Services, web.ServiceRow{
			Name:      n.Name,
			Namespace: n.Namespace,
			Status:    n.Status,
			SpanCount: n.SpanCount,
			P95Ms:     n.P95Ms,
			ErrorRate: n.ErrorRate,
			Trend:     trends[n.Name],
		})
	}

	return renderTempl(c, web.Services(data))
}

// ServiceDetail renders a service detail page
func (h *UIHandler) ServiceDetail(c echo.Context) error {
	ctx := c.Request().Context()
	name := c.Param("name")
	window := parseWindow(c)
	namespace := parseNamespace(c)

	var diag diagnoseResult
	var timeline timelineResult

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		diag = h.getDiagnose(gctx, name, window, namespace)
		return nil
	})
	g.Go(func() error {
		timeline = h.getTimeline(gctx, name, window, 5, namespace)
		return nil
	})
	if err := g.Wait(); err != nil {
		return err
	}

	data := web.ServiceDetailData{
		Name:      name,
		Status:    diag.Status,
		P50Ms:     diag.P50Ms,
		P95Ms:     diag.P95Ms,
		P99Ms:     diag.P99Ms,
		ErrorRate: diag.ErrorRate,
		SpanCount: diag.SpanCount,
		Window:    window,
	}

	// Add timeline data for charts
	for _, b := range timeline.Buckets {
		data.Timeline = append(data.Timeline, web.TimelinePoint{
			Time:         b.Time,
			RequestCount: b.RequestCount,
			ErrorCount:   b.ErrorCount,
			P50Ms:        b.P50Ms,
			P95Ms:        b.P95Ms,
			ErrorRate:    b.ErrorRate,
		})
	}

	for _, e := range diag.TopErrors {
		data.TopErrors = append(data.TopErrors, web.ErrorInfo{
			Operation: e.Operation,
			Count:     e.Count,
			TraceID:   e.TraceID,
			Message:   e.Message,
		})
	}

	for _, op := range diag.SlowOps {
		data.SlowOps = append(data.SlowOps, web.SlowOp{
			Operation: op.Operation,
			P95Ms:     op.P95Ms,
			Count:     op.Count,
		})
	}

	for _, dep := range diag.Dependencies {
		data.Dependencies = append(data.Dependencies, web.Dependency{
			Service:   dep.Service,
			CallCount: dep.CallCount,
			AvgMs:     dep.AvgMs,
			ErrorRate: dep.ErrorRate,
			Status:    dep.Status,
		})
	}

	return renderTempl(c, web.ServiceDetail(data))
}

// Topology renders the service topology page
func (h *UIHandler) Topology(c echo.Context) error {
	ctx := c.Request().Context()
	window := parseWindow(c)
	namespace := parseNamespace(c)

	var nodes []web.ServiceNode
	var edges []web.ServiceEdge

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		topo := h.getTopology(gctx, window, namespace)
		for _, n := range topo.Nodes {
			nodes = append(nodes, web.ServiceNode{
				Name:      n.Name,
				Status:    n.Status,
				SpanCount: n.SpanCount,
				P95Ms:     n.P95Ms,
				ErrorRate: n.ErrorRate,
			})
		}
		return nil
	})

	g.Go(func() error {
		edges = h.getTopoEdges(gctx, window, namespace)
		return nil
	})

	if err := g.Wait(); err != nil {
		return err
	}

	data := web.TopologyData{
		Nodes:  nodes,
		Edges:  edges,
		Window: window,
	}

	return renderTempl(c, web.Topology(data))
}

// Traces renders the traces search page
func (h *UIHandler) Traces(c echo.Context) error {
	ctx := c.Request().Context()
	queryStr := c.QueryParam("q")
	window := parseWindow(c)
	namespace := parseNamespace(c)

	// Pagination
	limit := 50
	if l := c.QueryParam("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
		if limit <= 0 || limit > 100 {
			limit = 50
		}
	}
	offset := 0
	if o := c.QueryParam("offset"); o != "" {
		fmt.Sscanf(o, "%d", &offset)
		if offset < 0 {
			offset = 0
		}
	}

	traces, hasMore := h.searchTracesPage(ctx, queryStr, window, limit, offset, namespace)

	data := web.TracesData{
		Query:   queryStr,
		Traces:  traces,
		Limit:   limit,
		Offset:  offset,
		HasMore: hasMore,
		Window:  window,
	}

	return renderTempl(c, web.Traces(data))
}

// TraceDetail renders a trace detail page
func (h *UIHandler) TraceDetail(c echo.Context) error {
	ctx := c.Request().Context()
	traceID := c.Param("id")
	window := parseWindow(c)
	namespace := parseNamespace(c)

	detail := h.getTraceDetail(ctx, traceID, window, namespace)
	return renderTempl(c, web.TraceDetail(detail))
}

// Logs renders the logs search page
func (h *UIHandler) Logs(c echo.Context) error {
	ctx := c.Request().Context()
	query := c.QueryParam("q")
	window := parseWindow(c)
	namespace := parseNamespace(c)

	// Pagination
	limit := 100
	if l := c.QueryParam("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
		if limit <= 0 || limit > 200 {
			limit = 100
		}
	}
	offset := 0
	if o := c.QueryParam("offset"); o != "" {
		fmt.Sscanf(o, "%d", &offset)
		if offset < 0 {
			offset = 0
		}
	}

	logs, hasMore := h.searchLogsPage(ctx, query, window, limit, offset, namespace)

	data := web.LogsData{
		Query:   query,
		Logs:    logs,
		Limit:   limit,
		Offset:  offset,
		Window:  window,
		HasMore: hasMore,
	}

	return renderTempl(c, web.Logs(data))
}

// renderTempl is a helper to render templ components
func renderTempl(c echo.Context, component templ.Component) error {
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTML)
	return component.Render(c.Request().Context(), c.Response().Writer)
}

// parseWindow reads window query param and returns minutes (default 60)
func parseWindow(c echo.Context) int {
	window := 60
	if w := c.QueryParam("window"); w != "" {
		fmt.Sscanf(w, "%d", &window)
		// Clamp to valid values
		switch window {
		case 15, 30, 60, 180, 360, 1440:
			// valid
		default:
			window = 60
		}
	}
	return window
}

// parseNamespace reads namespace query param
func parseNamespace(c echo.Context) string {
	return c.QueryParam("ns")
}

// escapeSQL escapes single quotes for SQL string literals
func escapeSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// --- Query helpers (same logic as MCP tools) ---

type statusResult struct {
	Healthy          bool
	Summary          string
	Services         serviceSummary
	TopIssues        []topIssue
	ThroughputPerMin int64
	P95Ms            float64
	ErrorRate        float64
}

type serviceSummary struct {
	Total     int
	Healthy   int
	Degraded  int
	Unhealthy int
}

type topIssue struct {
	Service string
	Issue   string
	Value   float64
	Detail  string
}

func (h *UIHandler) getStatus(ctx context.Context, window int, namespace string) statusResult {
	out := statusResult{TopIssues: []topIssue{}}

	// Use provided namespace or default
	if namespace == "" {
		namespace = h.duck.DefaultNamespace()
	}
	spansGlob := h.duck.SpansGlob(h.duck.DefaultTenantID(), namespace, window)

	q := fmt.Sprintf(`
SELECT
  "name=service_name" as service,
  COUNT(*) as cnt,
  COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p95_ms,
  COALESCE(AVG(CASE WHEN "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END), 0) as error_rate
FROM read_parquet(%s)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
GROUP BY "name=service_name"
ORDER BY cnt DESC
LIMIT 100;
`, spansGlob, window)

	rows, err := h.duck.DB.QueryContext(ctx, q)
	if err != nil {
		out.Healthy = true
		out.Summary = "No telemetry data yet"
		return out
	}
	defer rows.Close()

	var totalCount int64
	var totalP95, totalErrorRate float64
	var services []struct {
		name      string
		count     int64
		p95       float64
		errorRate float64
		status    string
	}

	for rows.Next() {
		var svc struct {
			name      string
			count     int64
			p95       float64
			errorRate float64
			status    string
		}
		if err := rows.Scan(&svc.name, &svc.count, &svc.p95, &svc.errorRate); err != nil {
			continue
		}
		svc.status = deriveHealth(svc.errorRate, svc.p95)
		totalCount += svc.count
		totalP95 += svc.p95 * float64(svc.count)
		totalErrorRate += svc.errorRate * float64(svc.count)
		services = append(services, svc)
	}

	if len(services) > 0 {
		out.P95Ms = totalP95 / float64(totalCount)
		out.ErrorRate = totalErrorRate / float64(totalCount)
	}
	out.ThroughputPerMin = totalCount / int64(window)

	for _, svc := range services {
		out.Services.Total++
		switch svc.status {
		case "healthy":
			out.Services.Healthy++
		case "degraded":
			out.Services.Degraded++
		case "unhealthy":
			out.Services.Unhealthy++
		}

		// Only add to TopIssues if there's a specific issue to report
		if len(out.TopIssues) < 5 {
			var issue topIssue
			if svc.errorRate > 0.05 {
				issue = topIssue{
					Service: svc.name,
					Issue:   "high_error_rate",
					Value:   svc.errorRate,
					Detail:  fmt.Sprintf("%.1f%% errors", svc.errorRate*100),
				}
			} else if svc.p95 > 1000 {
				issue = topIssue{
					Service: svc.name,
					Issue:   "high_latency",
					Value:   svc.p95,
					Detail:  fmt.Sprintf("p95 %.0fms", svc.p95),
				}
			}
			if issue.Issue != "" {
				out.TopIssues = append(out.TopIssues, issue)
			}
		}
	}

	out.Healthy = out.Services.Unhealthy == 0 && out.Services.Degraded == 0
	if out.Healthy {
		out.Summary = fmt.Sprintf("%d services healthy, %.0f req/min", out.Services.Total, float64(out.ThroughputPerMin))
	} else {
		out.Summary = fmt.Sprintf("%d degraded, %d unhealthy of %d services", out.Services.Degraded, out.Services.Unhealthy, out.Services.Total)
	}

	return out
}

type topoResult struct {
	Nodes []topoNode
	Edges []topoEdge
}

type topoNode struct {
	Name      string
	Namespace string
	Status    string
	SpanCount int64
	P95Ms     float64
	ErrorRate float64
}

type topoEdge struct {
	From      string
	To        string
	CallCount int64
	AvgMs     float64
	ErrorRate float64
	Status    string
}

func (h *UIHandler) getTopology(ctx context.Context, window int, namespace string) topoResult {
	out := topoResult{Nodes: []topoNode{}, Edges: []topoEdge{}}

	// Use provided namespace or default
	if namespace == "" {
		namespace = h.duck.DefaultNamespace()
	}
	spansGlob := h.duck.SpansGlob(h.duck.DefaultTenantID(), namespace, window)

	q := fmt.Sprintf(`
SELECT
  "name=service_name" as service,
  COUNT(*) as cnt,
  COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p95,
  COALESCE(AVG(CASE WHEN "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END), 0) as error_rate
FROM read_parquet(%s)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
GROUP BY "name=service_name"
ORDER BY cnt DESC
LIMIT 50;
`, spansGlob, window)

	rows, err := h.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return out
	}
	defer rows.Close()

	for rows.Next() {
		var n topoNode
		if err := rows.Scan(&n.Name, &n.SpanCount, &n.P95Ms, &n.ErrorRate); err != nil {
			continue
		}
		n.Namespace = namespace
		n.Status = deriveHealth(n.ErrorRate, n.P95Ms)
		out.Nodes = append(out.Nodes, n)
	}

	return out
}

// getServiceTrends returns per-service request counts bucketed by 5 minutes
func (h *UIHandler) getServiceTrends(ctx context.Context, window int, namespace string) map[string][]int64 {
	out := make(map[string][]int64)

	// Use provided namespace or default
	if namespace == "" {
		namespace = h.duck.DefaultNamespace()
	}
	spansGlob := h.duck.SpansGlob(h.duck.DefaultTenantID(), namespace, window)

	q := fmt.Sprintf(`
SELECT
  "name=service_name" as service,
  time_bucket(INTERVAL '5 minutes', epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT))) as bucket,
  COUNT(*) as cnt
FROM read_parquet(%s)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
GROUP BY "name=service_name", bucket
ORDER BY service, bucket ASC;
`, spansGlob, window)

	rows, err := h.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return out
	}
	defer rows.Close()

	for rows.Next() {
		var service string
		var bucket any
		var cnt int64
		if err := rows.Scan(&service, &bucket, &cnt); err != nil {
			continue
		}
		out[service] = append(out[service], cnt)
	}

	return out
}

// getTopoEdges returns caller-callee relationships between services
func (h *UIHandler) getTopoEdges(ctx context.Context, window int, namespace string) []web.ServiceEdge {
	var out []web.ServiceEdge

	// Use provided namespace or default
	if namespace == "" {
		namespace = h.duck.DefaultNamespace()
	}
	spansGlob := h.duck.SpansGlob(h.duck.DefaultTenantID(), namespace, window)

	// Find parent-child span relationships across services
	q := fmt.Sprintf(`
WITH spans AS (
  SELECT
    "name=span_id" as span_id,
    "name=parent_span_id" as parent_id,
    "name=service_name" as service,
    "name=status_code" as status
  FROM read_parquet(%s)
  WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
)
SELECT
  parent.service as caller,
  child.service as callee,
  COUNT(*) as calls,
  AVG(CASE WHEN child.status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END) as error_rate
FROM spans child
JOIN spans parent ON child.parent_id = parent.span_id
WHERE parent.service != child.service
GROUP BY parent.service, child.service
HAVING COUNT(*) > 5
ORDER BY calls DESC
LIMIT 100;
`, spansGlob, window)

	rows, err := h.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return out
	}
	defer rows.Close()

	for rows.Next() {
		var e web.ServiceEdge
		if err := rows.Scan(&e.From, &e.To, &e.CallCount, &e.ErrorRate); err != nil {
			continue
		}
		out = append(out, e)
	}

	return out
}

type timelineResult struct {
	Buckets   []timelineBucket
	Anomalies []anomaly
	AvgP95Ms  float64
}

type timelineBucket struct {
	Time         string
	RequestCount int64
	ErrorCount   int64
	P50Ms        float64
	P95Ms        float64
	ErrorRate    float64
	IsAnomaly    bool
}

type anomaly struct {
	Time        string
	Type        string
	Description string
}

func (h *UIHandler) getTimeline(ctx context.Context, service string, window, granularity int, namespace string) timelineResult {
	out := timelineResult{Buckets: []timelineBucket{}, Anomalies: []anomaly{}}

	// Use provided namespace or default
	if namespace == "" {
		namespace = h.duck.DefaultNamespace()
	}
	spansGlob := h.duck.SpansGlob(h.duck.DefaultTenantID(), namespace, window)

	var args []any
	svcFilter := ""
	if service != "" {
		svcFilter = `AND "name=service_name" = ?`
		args = append(args, service)
	}

	q := fmt.Sprintf(`
SELECT
  time_bucket(INTERVAL '%d minutes', epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT))) as bucket,
  COUNT(*) as cnt,
  SUM(CASE WHEN "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1 ELSE 0 END) as errors,
  COALESCE(PERCENTILE_CONT(0.50) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p50,
  COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p95
FROM read_parquet(%s)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  %s
GROUP BY bucket
ORDER BY bucket ASC;
`, granularity, spansGlob, window, svcFilter)

	rows, err := h.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()

	var totalP95 float64
	for rows.Next() {
		var b timelineBucket
		var bucket any
		if err := rows.Scan(&bucket, &b.RequestCount, &b.ErrorCount, &b.P50Ms, &b.P95Ms); err != nil {
			continue
		}
		b.Time = fmt.Sprintf("%v", bucket)
		if b.RequestCount > 0 {
			b.ErrorRate = float64(b.ErrorCount) / float64(b.RequestCount)
		}
		totalP95 += b.P95Ms
		out.Buckets = append(out.Buckets, b)
	}

	if len(out.Buckets) > 0 {
		out.AvgP95Ms = totalP95 / float64(len(out.Buckets))
	}

	// Simple anomaly detection: > 2x average
	for i := range out.Buckets {
		b := &out.Buckets[i]
		if b.P95Ms > out.AvgP95Ms*2 && b.P95Ms > 100 {
			b.IsAnomaly = true
			out.Anomalies = append(out.Anomalies, anomaly{
				Time:        b.Time,
				Type:        "latency_spike",
				Description: fmt.Sprintf("P95 %.0fms vs avg %.0fms", b.P95Ms, out.AvgP95Ms),
			})
		}
	}

	return out
}

func deriveHealth(errorRate, p95 float64) string {
	if errorRate > 0.1 || p95 > 5000 {
		return "unhealthy"
	}
	if errorRate > 0.01 || p95 > 1000 {
		return "degraded"
	}
	return "healthy"
}

// --- Diagnose helper ---

type diagnoseResult struct {
	Status       string
	P50Ms        float64
	P95Ms        float64
	P99Ms        float64
	ErrorRate    float64
	SpanCount    int64
	TopErrors    []errorInfo
	SlowOps      []slowOp
	Dependencies []dependency
}

type errorInfo struct {
	Operation string
	Count     int64
	TraceID   string
	Message   string
}

type slowOp struct {
	Operation string
	P95Ms     float64
	Count     int64
}

type dependency struct {
	Service   string
	CallCount int64
	AvgMs     float64
	ErrorRate float64
	Status    string
}

func (h *UIHandler) getDiagnose(ctx context.Context, service string, window int, namespace string) diagnoseResult {
	out := diagnoseResult{
		TopErrors:    []errorInfo{},
		SlowOps:      []slowOp{},
		Dependencies: []dependency{},
	}

	// Use provided namespace or default
	if namespace == "" {
		namespace = h.duck.DefaultNamespace()
	}
	spansGlob := h.duck.SpansGlob(h.duck.DefaultTenantID(), namespace, window)

	// Get latency percentiles and error rate
	q := fmt.Sprintf(`
SELECT
  COUNT(*) as cnt,
  COALESCE(PERCENTILE_CONT(0.50) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p50,
  COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p95,
  COALESCE(PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p99,
  COALESCE(AVG(CASE WHEN "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END), 0) as error_rate
FROM read_parquet(%s)
WHERE "name=service_name" = ?
  AND epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE;
`, spansGlob, window)

	row := h.duck.DB.QueryRowContext(ctx, q, service)
	if err := row.Scan(&out.SpanCount, &out.P50Ms, &out.P95Ms, &out.P99Ms, &out.ErrorRate); err == nil {
		out.Status = deriveHealth(out.ErrorRate, out.P95Ms)
	}

	// Get top errors by operation
	q = fmt.Sprintf(`
SELECT
  "name=name" as operation,
  COUNT(*) as cnt,
  MAX("name=trace_id") as trace_id
FROM read_parquet(%s)
WHERE "name=service_name" = ?
  AND "name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR')
  AND epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
GROUP BY "name=name"
ORDER BY cnt DESC
LIMIT 5;
`, spansGlob, window)

	rows, err := h.duck.DB.QueryContext(ctx, q, service)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var e errorInfo
			if err := rows.Scan(&e.Operation, &e.Count, &e.TraceID); err == nil {
				out.TopErrors = append(out.TopErrors, e)
			}
		}
	}

	// Get slow operations
	q = fmt.Sprintf(`
SELECT
  "name=name" as operation,
  COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) as p95,
  COUNT(*) as cnt
FROM read_parquet(%s)
WHERE "name=service_name" = ?
  AND epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
GROUP BY "name=name"
HAVING COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY "name=duration_ms"), 0) > 100
ORDER BY p95 DESC
LIMIT 5;
`, spansGlob, window)

	rows, err = h.duck.DB.QueryContext(ctx, q, service)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var op slowOp
			if err := rows.Scan(&op.Operation, &op.P95Ms, &op.Count); err == nil {
				out.SlowOps = append(out.SlowOps, op)
			}
		}
	}

	// Get downstream dependencies
	q = fmt.Sprintf(`
WITH downstream AS (
  SELECT
    child."name=service_name" as dep_service,
    child."name=duration_ms" as duration_ms,
    child."name=status_code" as status
  FROM read_parquet(%s) parent
  JOIN read_parquet(%s) child
    ON parent."name=span_id" = child."name=parent_span_id"
    AND parent."name=trace_id" = child."name=trace_id"
  WHERE epoch_ms(CAST(parent."name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
    AND parent."name=service_name" = ?
    AND child."name=service_name" != ?
)
SELECT
  dep_service,
  COUNT(*) as calls,
  AVG(duration_ms) as avg_ms,
  AVG(CASE WHEN status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END) as error_rate
FROM downstream
GROUP BY dep_service
ORDER BY calls DESC
LIMIT 10;
`, spansGlob, spansGlob, window)

	rows, err = h.duck.DB.QueryContext(ctx, q, service, service)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var d dependency
			if err := rows.Scan(&d.Service, &d.CallCount, &d.AvgMs, &d.ErrorRate); err != nil {
				continue
			}
			d.Status = deriveHealth(d.ErrorRate, d.AvgMs)
			out.Dependencies = append(out.Dependencies, d)
		}
	}

	return out
}

// --- Traces helpers ---

func (h *UIHandler) searchTracesPage(ctx context.Context, queryStr string, window, limit, offset int, namespace string) ([]web.TraceRow, bool) {
	var out []web.TraceRow

	// Parse query DSL
	q := search.Parse(queryStr)

	if namespace == "" {
		namespace = h.duck.DefaultNamespace()
	}
	spansGlob := h.duck.SpansGlob(h.duck.DefaultTenantID(), namespace, window)
	var args []any
	filters := []string{
		fmt.Sprintf(`epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE`, window),
	}

	// Service filter
	if services := q.Service(); len(services) > 0 {
		placeholders := makePlaceholders(len(services))
		filters = append(filters, fmt.Sprintf(`"name=service_name" IN (%s)`, placeholders))
		for _, s := range services {
			args = append(args, s)
		}
	}

	// Operation filter
	if ops := q.Operation(); len(ops) > 0 {
		var opFilters []string
		for _, op := range ops {
			if containsWildcard(op) {
				opFilters = append(opFilters, `"name=name" ILIKE ?`)
				args = append(args, wildcardToLike(op))
			} else {
				opFilters = append(opFilters, `"name=name" ILIKE ?`)
				args = append(args, "%"+op+"%")
			}
		}
		filters = append(filters, "("+joinFilters(opFilters, " OR ")+")")
	}

	// Status filter
	if statuses := q.Status(); len(statuses) > 0 {
		var statusFilters []string
		for _, s := range statuses {
			switch s {
			case "error":
				statusFilters = append(statusFilters, `"name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR')`)
			case "slow":
				statusFilters = append(statusFilters, `"name=duration_ms" > 1000`)
			}
		}
		if len(statusFilters) > 0 {
			filters = append(filters, "("+joinFilters(statusFilters, " OR ")+")")
		}
	}

	// Duration filter (e.g., ">1000", "<500")
	if dur := q.Duration(); dur != "" {
		if len(dur) > 1 {
			op := string(dur[0])
			val := dur[1:]
			if op == ">" || op == "<" {
				filters = append(filters, fmt.Sprintf(`"name=duration_ms" %s ?`, op))
				args = append(args, val)
			}
		}
	}

	// Text search terms (match operation or trace_id)
	for _, term := range q.Terms {
		filters = append(filters, `("name=name" ILIKE ? OR "name=trace_id" ILIKE ?)`)
		args = append(args, "%"+term+"%", "%"+term+"%")
	}

	// Exclude terms
	for _, term := range q.Exclude {
		filters = append(filters, `"name=name" NOT ILIKE ?`)
		args = append(args, "%"+term+"%")
	}

	whereClause := buildWhereClause(filters)

	// Query limit+1 to detect if there are more results
	sqlQ := fmt.Sprintf(`
SELECT DISTINCT ON ("name=trace_id")
  "name=trace_id" as trace_id,
  "name=service_name" as service,
    "name=name" as operation,
  "name=duration_ms" as duration,
  "name=status_code" as status,
  epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) as ts
FROM read_parquet(%s)
%s
ORDER BY "name=trace_id", ts DESC
LIMIT %d OFFSET %d;
`, spansGlob, whereClause, limit+1, offset)

	rows, err := h.duck.DB.QueryContext(ctx, sqlQ, args...)
	if err != nil {
		return out, false
	}
	defer rows.Close()

	for rows.Next() {
		var t web.TraceRow
		var statusCode string
		var ts any
		if err := rows.Scan(&t.TraceID, &t.Service, &t.Operation, &t.Duration, &statusCode, &ts); err != nil {
			continue
		}
		if statusCode == "STATUS_CODE_ERROR" || statusCode == "ERROR" {
			t.Status = "error"
		} else {
			t.Status = "ok"
		}
		t.Time = fmt.Sprintf("%v", ts)
		out = append(out, t)
	}

	// Check if there are more results
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}

	return out, hasMore
}

// joinFilters joins filter strings with the given operator
func joinFilters(filters []string, op string) string {
	if len(filters) == 0 {
		return ""
	}
	result := filters[0]
	for i := 1; i < len(filters); i++ {
		result += op + filters[i]
	}
	return result
}

func (h *UIHandler) getTraceDetail(ctx context.Context, traceID string, window int, namespace string) web.TraceDetailData {
	out := web.TraceDetailData{
		TraceID: traceID,
		Spans:   []web.SpanInfo{},
		Logs:    []web.LogInfo{},
		Window:  window,
	}

	// Get all spans for this trace
	if namespace == "" {
		namespace = h.duck.DefaultNamespace()
	}
	spansGlob := h.duck.SpansGlob(h.duck.DefaultTenantID(), namespace, window)
	q := fmt.Sprintf(`
	SELECT
	  "name=span_id" as span_id,
	  "name=parent_span_id" as parent_id,
	  "name=service_name" as service,
	  "name=name" as operation,
	  "name=duration_ms" as duration,
	  "name=status_code" as status,
	  COALESCE("name=status_msg", '') as status_msg,
	  COALESCE("name=kind", '') as kind,
	  "name=start_unix_nano" as start_nano
	FROM read_parquet(%s)
	WHERE "name=trace_id" = ?
	ORDER BY "name=start_unix_nano" ASC;
	`, spansGlob)

	rows, err := h.duck.DB.QueryContext(ctx, q, traceID)
	if err != nil {
		return out
	}
	defer rows.Close()

	var minStart int64 = -1
	var maxEnd int64 = -1
	var spans []struct {
		spanID    string
		parentID  string
		service   string
		operation string
		duration  float64
		status    string
		statusMsg string
		kind      string
		startNano int64
	}

	for rows.Next() {
		var s struct {
			spanID    string
			parentID  string
			service   string
			operation string
			duration  float64
			status    string
			statusMsg string
			kind      string
			startNano int64
		}
		if err := rows.Scan(&s.spanID, &s.parentID, &s.service, &s.operation, &s.duration, &s.status, &s.statusMsg, &s.kind, &s.startNano); err != nil {
			continue
		}
		if minStart == -1 || s.startNano < minStart {
			minStart = s.startNano
		}
		endNano := s.startNano + int64(s.duration*1e6)
		if maxEnd == -1 || endNano > maxEnd {
			maxEnd = endNano
		}
		spans = append(spans, s)
	}

	// Build span tree and calculate depths
	parentMap := make(map[string]string)
	for _, s := range spans {
		parentMap[s.spanID] = s.parentID
	}

	getDepth := func(spanID string) int {
		depth := 0
		current := spanID
		for {
			parent, ok := parentMap[current]
			if !ok || parent == "" {
				break
			}
			depth++
			current = parent
			if depth > 20 {
				break
			}
		}
		return depth
	}

	for _, s := range spans {
		status := "ok"
		if s.status == "STATUS_CODE_ERROR" || s.status == "ERROR" {
			status = "error"
			if out.RootCause == "" {
				out.RootCause = fmt.Sprintf("%s: %s", s.service, s.operation)
			}
		}

		offset := float64(s.startNano-minStart) / 1e6
		if s.parentID == "" {
			out.RootService = s.service
			out.RootOp = s.operation
		}
		out.Spans = append(out.Spans, web.SpanInfo{
			SpanID:      s.spanID,
			ParentID:    s.parentID,
			Service:     s.service,
			Operation:   s.operation,
			Duration:    s.duration,
			Status:      status,
			StatusMsg:   s.statusMsg,
			Kind:        s.kind,
			StartOffset: offset,
			Depth:       getDepth(s.spanID),
		})
	}

	out.SpanCount = len(out.Spans)
	if minStart != -1 && maxEnd != -1 && maxEnd > minStart {
		out.Duration = float64(maxEnd-minStart) / 1e6
	}
	if len(out.Spans) > 0 && out.Duration == 0 {
		out.Duration = out.Spans[0].Duration
	}

	// Get correlated logs
	logsGlob := h.duck.LogsGlob(h.duck.DefaultTenantID(), namespace, window)
	q = fmt.Sprintf(`
	SELECT
	  epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) as ts,
	  "name=service_name" as service,
	  "name=severity" as severity,
	  "name=body" as body
	FROM read_parquet(%s)
	WHERE "name=trace_id" = ?
	ORDER BY "name=time_unix_nano" ASC
	LIMIT 50;
	`, logsGlob)

	rows, err = h.duck.DB.QueryContext(ctx, q, traceID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var log web.LogInfo
			var ts any
			if err := rows.Scan(&ts, &log.Service, &log.Severity, &log.Body); err == nil {
				log.Time = fmt.Sprintf("%v", ts)
				out.Logs = append(out.Logs, log)
			}
		}
	}

	return out
}

// Metrics renders the metrics explorer page
func (h *UIHandler) Metrics(c echo.Context) error {
	ctx := c.Request().Context()
	query := c.QueryParam("q")
	window := parseWindow(c)
	namespace := parseNamespace(c)

	// Get metrics summary with filters
	metrics := h.getMetricsSummary(ctx, query, window, namespace)

	data := web.MetricsData{
		Query:   query,
		Window:  window,
		Metrics: metrics,
	}

	return renderTempl(c, web.Metrics(data))
}

// getMetricsSummary returns aggregated metrics with sparkline data
func (h *UIHandler) getMetricsSummary(ctx context.Context, queryStr string, window int, namespace string) []web.MetricSummary {
	var out []web.MetricSummary

	// Parse query DSL
	parsed := search.Parse(queryStr)

	if namespace == "" {
		namespace = h.duck.DefaultNamespace()
	}
	metricsGlob := h.duck.MetricsGlob(h.duck.DefaultTenantID(), namespace, window)
	var args []any
	filters := []string{
		fmt.Sprintf(`epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE`, window),
	}

	// Name filter (supports wildcards via LIKE)
	if nameFilter := parsed.Name(); len(nameFilter) > 0 {
		for _, n := range nameFilter {
			if containsWildcard(n) {
				filters = append(filters, `"name=name" ILIKE ?`)
				args = append(args, wildcardToLike(n))
			} else {
				filters = append(filters, `"name=name" = ?`)
				args = append(args, n)
			}
		}
	}

	// Service filter
	if svcFilter := parsed.Service(); len(svcFilter) > 0 {
		placeholders := makePlaceholders(len(svcFilter))
		for _, s := range svcFilter {
			args = append(args, s)
		}
		filters = append(filters, `"name=service_name" IN (`+placeholders+`)`)
	}

	// Type filter
	if typeFilter := parsed.Type(); len(typeFilter) > 0 {
		placeholders := makePlaceholders(len(typeFilter))
		for _, t := range typeFilter {
			args = append(args, t)
		}
		filters = append(filters, `"name=mtype" IN (`+placeholders+`)`)
	}

	// Text search terms (match metric name)
	for _, term := range parsed.Terms {
		filters = append(filters, `"name=name" ILIKE ?`)
		args = append(args, "%"+term+"%")
	}

	whereClause := buildWhereClause(filters)

	// Aggregation query
	q := fmt.Sprintf(`
SELECT
  "name=name" as metric_name,
  COALESCE("name=mtype", 'unknown') as mtype,
  COUNT(*) as cnt,
  AVG(COALESCE("name=value", 0)) as avg_val,
  MIN(COALESCE("name=value", 0)) as min_val,
  MAX(COALESCE("name=value", 0)) as max_val,
  LIST(DISTINCT "name=service_name") as services
FROM read_parquet(%s)
%s
GROUP BY "name=name", "name=mtype"
ORDER BY metric_name
LIMIT 100;
`, metricsGlob, whereClause)

	rows, err := h.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()

	var metricNames []string
	metricMap := make(map[string]*web.MetricSummary)

	for rows.Next() {
		var m web.MetricSummary
		var services any
		if err := rows.Scan(&m.Name, &m.Type, &m.Count, &m.Avg, &m.Min, &m.Max, &services); err != nil {
			continue
		}
		// Parse services list
		m.Services = parseServiceList(services)
		metricNames = append(metricNames, m.Name)
		metricMap[m.Name] = &m
		out = append(out, m)
	}

	// Get sparkline data (12 time buckets)
	if len(metricNames) > 0 {
		sparklines := h.getMetricSparklines(ctx, metricNames, window, namespace)
		for i := range out {
			if trend, ok := sparklines[out[i].Name]; ok {
				out[i].Trend = trend
			}
		}
	}

	return out
}

// getMetricSparklines returns 12-point trend data for each metric
func (h *UIHandler) getMetricSparklines(ctx context.Context, names []string, window int, namespace string) map[string][]float64 {
	out := make(map[string][]float64)

	metricsGlob := h.duck.MetricsGlob(h.duck.DefaultTenantID(), namespace, window)
	bucketMins := window / 12
	if bucketMins < 1 {
		bucketMins = 1
	}

	// Build name filter
	placeholders := makePlaceholders(len(names))
	var args []any
	for _, n := range names {
		args = append(args, n)
	}

	q := fmt.Sprintf(`
SELECT
  "name=name" as metric_name,
  time_bucket(INTERVAL '%d minutes', epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT))) as bucket,
  AVG(COALESCE("name=value", 0)) as avg_val
FROM read_parquet(%s)
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
  AND "name=name" IN (%s)
GROUP BY "name=name", bucket
ORDER BY metric_name, bucket ASC;
`, bucketMins, metricsGlob, window, placeholders)

	rows, err := h.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var bucket any
		var avg float64
		if err := rows.Scan(&name, &bucket, &avg); err != nil {
			continue
		}
		out[name] = append(out[name], avg)
	}

	return out
}

// Helper functions

func containsWildcard(s string) bool {
	return len(s) > 0 && (s[0] == '*' || s[len(s)-1] == '*' || containsAny(s, "*?"))
}

func containsAny(s, chars string) bool {
	for _, c := range chars {
		for _, sc := range s {
			if c == sc {
				return true
			}
		}
	}
	return false
}

func wildcardToLike(s string) string {
	// Convert glob wildcards to SQL LIKE
	result := s
	result = replaceAll(result, "*", "%")
	result = replaceAll(result, "?", "_")
	return result
}

func replaceAll(s, old, new string) string {
	for {
		i := indexOf(s, old)
		if i < 0 {
			return s
		}
		s = s[:i] + new + s[i+len(old):]
	}
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func makePlaceholders(n int) string {
	if n <= 0 {
		return ""
	}
	result := "?"
	for i := 1; i < n; i++ {
		result += ", ?"
	}
	return result
}

func buildWhereClause(filters []string) string {
	if len(filters) == 0 {
		return ""
	}
	clause := "WHERE " + filters[0]
	for i := 1; i < len(filters); i++ {
		clause += " AND " + filters[i]
	}
	return clause
}

func parseServiceList(v any) []string {
	if v == nil {
		return nil
	}
	// DuckDB LIST returns []any
	if list, ok := v.([]any); ok {
		var out []string
		for _, item := range list {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func (h *UIHandler) getServices(ctx context.Context, window int) []string {
	var services []string

	spansGlob := h.duck.SpansGlob(h.duck.DefaultTenantID(), h.duck.DefaultNamespace(), window)
	q := fmt.Sprintf(`
SELECT DISTINCT "name=service_name"
FROM read_parquet(%s)
WHERE epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
ORDER BY "name=service_name"
LIMIT 100;
`, spansGlob, window)

	rows, err := h.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return services
	}
	defer rows.Close()

	for rows.Next() {
		var svc string
		if err := rows.Scan(&svc); err == nil && svc != "" {
			services = append(services, svc)
		}
	}
	return services
}

// discoverNamespaces scans the filesystem to find all namespaces
func (h *UIHandler) discoverNamespaces() []string {
	var namespaces []string
	seen := make(map[string]bool)

	// Scan lake directory for namespace=* directories
	tenantID := h.duck.DefaultTenantID()
	basePath := filepath.Join(h.cfg.LakeDir, "spans", fmt.Sprintf("tenant=%s", tenantID))

	entries, err := os.ReadDir(basePath)
	if err != nil {
		return namespaces
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "namespace=") {
			ns := strings.TrimPrefix(entry.Name(), "namespace=")
			if ns != "" && !seen[ns] {
				seen[ns] = true
				namespaces = append(namespaces, ns)
			}
		}
	}

	sort.Strings(namespaces)
	return namespaces
}

// --- Logs helpers ---

func (h *UIHandler) searchLogsPage(ctx context.Context, searchQuery string, window, limit, offset int, namespace string) ([]web.LogRow, bool) {
	var out []web.LogRow

	// Parse search query for advanced syntax
	parsed := search.Parse(searchQuery)

	if namespace == "" {
		namespace = h.duck.DefaultNamespace()
	}
	logsGlob := h.duck.LogsGlob(h.duck.DefaultTenantID(), namespace, window)
	var args []any
	filters := []string{
		fmt.Sprintf(`epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE`, window),
	}

	// Service filter from query DSL
	if svcFilter := parsed.Service(); len(svcFilter) > 0 {
		if len(svcFilter) == 1 {
			filters = append(filters, `"name=service_name" = ?`)
			args = append(args, svcFilter[0])
		} else {
			placeholders := ""
			for i, s := range svcFilter {
				if i > 0 {
					placeholders += ", "
				}
				placeholders += "?"
				args = append(args, s)
			}
			filters = append(filters, `"name=service_name" IN (`+placeholders+`)`)
		}
	}

	// Severity filter from query DSL
	if sevFilter := parsed.Severity(); len(sevFilter) > 0 {
		if len(sevFilter) == 1 {
			filters = append(filters, `"name=severity" = ?`)
			args = append(args, sevFilter[0])
		} else {
			placeholders := ""
			for i, s := range sevFilter {
				if i > 0 {
					placeholders += ", "
				}
				placeholders += "?"
				args = append(args, s)
			}
			filters = append(filters, `"name=severity" IN (`+placeholders+`)`)
		}
	}

	// Text search terms (AND'd together)
	for _, term := range parsed.Terms {
		filters = append(filters, `"name=body" ILIKE ?`)
		args = append(args, "%"+term+"%")
	}

	// Exclude terms
	for _, term := range parsed.Exclude {
		filters = append(filters, `"name=body" NOT ILIKE ?`)
		args = append(args, "%"+term+"%")
	}

	whereClause := ""
	for i, f := range filters {
		if i == 0 {
			whereClause = "WHERE " + f
		} else {
			whereClause += " AND " + f
		}
	}

	// Query limit+1 to detect if there are more results
	q := fmt.Sprintf(`
SELECT
  epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) as ts,
  "name=service_name" as service,
    "name=severity" as severity,
  "name=body" as body,
  COALESCE("name=trace_id", '') as trace_id
FROM read_parquet(%s)
%s
ORDER BY ts DESC
LIMIT %d OFFSET %d;
`, logsGlob, whereClause, limit+1, offset)

	rows, err := h.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return out, false
	}
	defer rows.Close()

	for rows.Next() {
		var log web.LogRow
		var ts any
		if err := rows.Scan(&ts, &log.Service, &log.Severity, &log.Body, &log.TraceID); err != nil {
			continue
		}
		log.Time = fmt.Sprintf("%v", ts)
		out = append(out, log)
	}

	// Check if there are more results
	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}

	return out, hasMore
}
