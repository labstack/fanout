package api

import (
	"context"
	"fmt"

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

	// Component CSS (dynamic from registry)
	e.GET("/css/components.css", ComponentCSS)

	// htmx partial routes
	RegisterPartialRoutes(e, h)
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

	data := web.OverviewData{Window: window}

	// Run queries in parallel
	var status statusResult
	var topo topoResult
	var timeline timelineResult

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		status = h.getStatus(gctx, window)
		return nil
	})

	g.Go(func() error {
		topo = h.getTopology(gctx, window)
		return nil
	})

	g.Go(func() error {
		timeline = h.getTimeline(gctx, "", window, 5)
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

	var topo topoResult
	var trends map[string][]int64

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		topo = h.getTopology(gctx, window)
		return nil
	})
	g.Go(func() error {
		trends = h.getServiceTrends(gctx, window)
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

	var diag diagnoseResult
	var timeline timelineResult

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		diag = h.getDiagnose(gctx, name, window)
		return nil
	})
	g.Go(func() error {
		timeline = h.getTimeline(gctx, name, window, 5)
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

	var nodes []web.ServiceNode
	var edges []web.ServiceEdge

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		topo := h.getTopology(gctx, window)
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
		edges = h.getTopoEdges(gctx, window)
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
	query := c.QueryParam("q")
	service := c.QueryParam("service")
	status := c.QueryParam("status")
	window := parseWindow(c)

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

	traces, hasMore := h.searchTracesPage(ctx, query, service, status, window, limit, offset)
	services := h.getServices(ctx, window)

	data := web.TracesData{
		Query:    query,
		Service:  service,
		Services: services,
		Status:   status,
		Traces:   traces,
		Limit:    limit,
		Offset:   offset,
		HasMore:  hasMore,
		Window:   window,
	}

	return renderTempl(c, web.Traces(data))
}

// TraceDetail renders a trace detail page
func (h *UIHandler) TraceDetail(c echo.Context) error {
	ctx := c.Request().Context()
	traceID := c.Param("id")
	window := parseWindow(c)

	detail := h.getTraceDetail(ctx, traceID, window)
	return renderTempl(c, web.TraceDetail(detail))
}

// Logs renders the logs search page
func (h *UIHandler) Logs(c echo.Context) error {
	ctx := c.Request().Context()
	query := c.QueryParam("q")
	window := parseWindow(c)

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

	logs, hasMore := h.searchLogsPage(ctx, query, window, limit, offset)

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

func (h *UIHandler) getStatus(ctx context.Context, window int) statusResult {
	out := statusResult{TopIssues: []topIssue{}}

	spansGlob := h.duck.SpansGlob(window)
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

func (h *UIHandler) getTopology(ctx context.Context, window int) topoResult {
	out := topoResult{Nodes: []topoNode{}, Edges: []topoEdge{}}

	spansGlob := h.duck.SpansGlob(window)
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
		n.Status = deriveHealth(n.ErrorRate, n.P95Ms)
		out.Nodes = append(out.Nodes, n)
	}

	return out
}

// getServiceTrends returns per-service request counts bucketed by 5 minutes
func (h *UIHandler) getServiceTrends(ctx context.Context, window int) map[string][]int64 {
	out := make(map[string][]int64)

	spansGlob := h.duck.SpansGlob(window)
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
func (h *UIHandler) getTopoEdges(ctx context.Context, window int) []web.ServiceEdge {
	var out []web.ServiceEdge

	spansGlob := h.duck.SpansGlob(window)
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

func (h *UIHandler) getTimeline(ctx context.Context, service string, window, granularity int) timelineResult {
	out := timelineResult{Buckets: []timelineBucket{}, Anomalies: []anomaly{}}

	spansGlob := h.duck.SpansGlob(window)
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

func (h *UIHandler) getDiagnose(ctx context.Context, service string, window int) diagnoseResult {
	out := diagnoseResult{
		TopErrors:    []errorInfo{},
		SlowOps:      []slowOp{},
		Dependencies: []dependency{},
	}

	spansGlob := h.duck.SpansGlob(window)

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

func (h *UIHandler) searchTraces(ctx context.Context, searchQuery, service, status string, window, limit int) []web.TraceRow {
	traces, _ := h.searchTracesPage(ctx, searchQuery, service, status, window, limit, 0)
	return traces
}

func (h *UIHandler) searchTracesPage(ctx context.Context, searchQuery, service, status string, window, limit, offset int) ([]web.TraceRow, bool) {
	var out []web.TraceRow

	spansGlob := h.duck.SpansGlob(window)
	var args []any
	filters := []string{
		fmt.Sprintf(`epoch_ms(CAST("name=start_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE`, window),
	}

	if service != "" {
		filters = append(filters, `"name=service_name" = ?`)
		args = append(args, service)
	}
	if searchQuery != "" {
		filters = append(filters, `("name=name" ILIKE ? OR "name=trace_id" ILIKE ?)`)
		args = append(args, "%"+searchQuery+"%", "%"+searchQuery+"%")
	}
	if status == "error" {
		filters = append(filters, `"name=status_code" IN ('STATUS_CODE_ERROR', 'ERROR')`)
	} else if status == "slow" {
		filters = append(filters, `"name=duration_ms" > 1000`)
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

	rows, err := h.duck.DB.QueryContext(ctx, q, args...)
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

func (h *UIHandler) getTraceDetail(ctx context.Context, traceID string, window int) web.TraceDetailData {
	out := web.TraceDetailData{
		TraceID: traceID,
		Spans:   []web.SpanInfo{},
		Logs:    []web.LogInfo{},
		Window:  window,
	}

	// Get all spans for this trace
	spansGlob := h.duck.SpansGlob(window)
	q := fmt.Sprintf(`
	SELECT
	  "name=span_id" as span_id,
	  "name=parent_span_id" as parent_id,
	  "name=service_name" as service,
	  "name=name" as operation,
	  "name=duration_ms" as duration,
	  "name=status_code" as status,
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
			startNano int64
		}
		if err := rows.Scan(&s.spanID, &s.parentID, &s.service, &s.operation, &s.duration, &s.status, &s.startNano); err != nil {
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
	logsGlob := h.duck.LogsGlob(window)
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
	name := c.QueryParam("name")
	service := c.QueryParam("service")
	window := parseWindow(c)

	// Pagination
	limit := 100
	if l := c.QueryParam("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
		if limit <= 0 || limit > 500 {
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

	// Get distinct metric names and services
	names := h.getMetricNames(ctx, window)
	services := h.getServices(ctx, window)

	// Get metrics with filters
	metrics, hasMore := h.searchMetricsPage(ctx, name, service, window, limit, offset)

	data := web.MetricsData{
		Names:    names,
		Selected: name,
		Service:  service,
		Services: services,
		Window:   window,
		Metrics:  metrics,
		Limit:    limit,
		Offset:   offset,
		HasMore:  hasMore,
	}

	return renderTempl(c, web.Metrics(data))
}

func (h *UIHandler) getMetricNames(ctx context.Context, window int) []string {
	var names []string

	metricsGlob := h.duck.MetricsGlob(window)
	q := fmt.Sprintf(`
SELECT DISTINCT "name=name"
FROM read_parquet(%s)
WHERE epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE
ORDER BY "name=name"
LIMIT 100;
`, metricsGlob, window)

	rows, err := h.duck.DB.QueryContext(ctx, q)
	if err != nil {
		return names
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err == nil {
			names = append(names, name)
		}
	}
	return names
}

func (h *UIHandler) getServices(ctx context.Context, window int) []string {
	var services []string

	spansGlob := h.duck.SpansGlob(window)
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

func (h *UIHandler) searchMetricsPage(ctx context.Context, name, service string, window, limit, offset int) ([]web.MetricRow, bool) {
	var out []web.MetricRow

	metricsGlob := h.duck.MetricsGlob(window)
	var args []any
	filters := []string{
		fmt.Sprintf(`epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) >= now() - INTERVAL %d MINUTE`, window),
	}

	if name != "" {
		filters = append(filters, `"name=name" = ?`)
		args = append(args, name)
	}
	if service != "" {
		filters = append(filters, `"name=service_name" = ?`)
		args = append(args, service)
	}

	whereClause := ""
	for i, f := range filters {
		if i == 0 {
			whereClause = "WHERE " + f
		} else {
			whereClause += " AND " + f
		}
	}

	q := fmt.Sprintf(`
SELECT
  epoch_ms(CAST("name=time_unix_nano"/1000000 AS BIGINT)) as ts,
  "name=name" as metric_name,
  "name=service_name" as service,
  "name=mtype" as mtype,
  COALESCE("name=value", 0) as value
FROM read_parquet(%s)
%s
ORDER BY ts DESC
LIMIT %d OFFSET %d;
`, metricsGlob, whereClause, limit+1, offset)

	rows, err := h.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return out, false
	}
	defer rows.Close()

	for rows.Next() {
		var m web.MetricRow
		var ts any
		if err := rows.Scan(&ts, &m.Name, &m.Service, &m.Type, &m.Value); err != nil {
			continue
		}
		m.Time = fmt.Sprintf("%v", ts)
		out = append(out, m)
	}

	hasMore := len(out) > limit
	if hasMore {
		out = out[:limit]
	}

	return out, hasMore
}

// --- Logs helpers ---

func (h *UIHandler) searchLogsPage(ctx context.Context, searchQuery string, window, limit, offset int) ([]web.LogRow, bool) {
	var out []web.LogRow

	// Parse search query for advanced syntax
	parsed := search.Parse(searchQuery)

	logsGlob := h.duck.LogsGlob(window)
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
