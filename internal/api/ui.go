package api

import (
	_ "embed"
	"fmt"

	"github.com/a-h/templ"
	"github.com/labstack/echo/v4"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/render"
	"github.com/labstack/fanout/internal/search"
	"github.com/labstack/fanout/internal/service"
	"github.com/labstack/fanout/internal/web"
	"golang.org/x/sync/errgroup"
)

//go:embed favicon.svg
var faviconSVG []byte

// UIHandler handles all UI routes
type UIHandler struct {
	svc *service.Service
	cfg config.Config
}

// NewUIHandler creates a new UI handler
func NewUIHandler(svc *service.Service, cfg config.Config) *UIHandler {
	return &UIHandler{svc: svc, cfg: cfg}
}

// RegisterUIRoutes registers all UI routes
func RegisterUIRoutes(e *echo.Echo, svc *service.Service, cfg config.Config) {
	h := NewUIHandler(svc, cfg)

	// Favicon
	e.GET("/favicon.ico", Favicon)
	e.GET("/favicon.svg", Favicon)

	// Full page routes
	e.GET("/", h.Overview)
	e.GET("/services", h.Services)
	e.GET("/services/:name", h.ServiceDetail)
	e.GET("/topology", h.Topology)
	e.GET("/traces", h.Traces)
	e.GET("/traces/:id", h.TraceDetail)
	e.GET("/logs", h.Logs)
	e.GET("/metrics", h.Metrics)
	e.GET("/unified", h.Unified)

	// API routes
	e.GET("/api/namespaces", h.Namespaces)

	// Component CSS (dynamic from registry)
	e.GET("/css/components.css", ComponentCSS)

	// htmx partial routes
	RegisterPartialRoutes(e, h)
}

// Namespaces returns discovered namespaces from the data
func (h *UIHandler) Namespaces(c echo.Context) error {
	namespaces := h.svc.Namespaces(h.cfg.LakeDir, "")
	return c.JSON(200, namespaces)
}

// ComponentCSS serves combined CSS from all registered components
func ComponentCSS(c echo.Context) error {
	css := render.AllCSS()
	c.Response().Header().Set("Content-Type", "text/css")
	c.Response().Header().Set("Cache-Control", "public, max-age=3600")
	return c.String(200, css)
}

// Favicon serves the SVG favicon
func Favicon(c echo.Context) error {
	c.Response().Header().Set("Content-Type", "image/svg+xml")
	c.Response().Header().Set("Cache-Control", "public, max-age=86400")
	return c.Blob(200, "image/svg+xml", faviconSVG)
}

// Overview renders the main dashboard
func (h *UIHandler) Overview(c echo.Context) error {
	ctx := c.Request().Context()
	window := parseWindow(c)
	namespace := parseNamespace(c)

	data := web.OverviewData{Window: window}

	var status *service.StatusResult
	var topo *service.TopologyResult
	var timeline *service.TimelineResult

	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		var err error
		status, err = h.svc.Status(gctx, window, namespace, "")
		return err
	})

	g.Go(func() error {
		var err error
		topo, err = h.svc.Topology(gctx, window, namespace, "")
		return err
	})

	g.Go(func() error {
		var err error
		timeline, err = h.svc.Timeline(gctx, "", window, 5, namespace, "")
		return err
	})

	if err := g.Wait(); err != nil {
		return err
	}

	// Map status result
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

	// Map topology nodes
	for _, n := range topo.Nodes {
		data.Topology.Nodes = append(data.Topology.Nodes, web.ServiceNode{
			Name:      n.Name,
			Status:    n.Status,
			SpanCount: n.SpanCount,
			P95Ms:     n.P95Ms,
			ErrorRate: n.ErrorRate,
		})
	}

	// Map timeline buckets
	for _, b := range timeline.Buckets {
		data.Timeline.Buckets = append(data.Timeline.Buckets, web.TimelineBucket{
			Time:         b.Time,
			RequestCount: b.Requests,
			ErrorCount:   b.Errors,
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

	var topo *service.TopologyResult
	var trends map[string][]int64

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		topo, err = h.svc.Topology(gctx, window, namespace, "")
		return err
	})
	g.Go(func() error {
		var err error
		trends, err = h.svc.ServiceTrends(gctx, window, namespace, "")
		return err
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

	var diag *service.DiagnoseResult
	var timeline *service.TimelineResult

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		var err error
		diag, err = h.svc.Diagnose(gctx, name, window, namespace, "")
		return err
	})
	g.Go(func() error {
		var err error
		timeline, err = h.svc.Timeline(gctx, name, window, 5, namespace, "")
		return err
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

	// Map timeline
	for _, b := range timeline.Buckets {
		data.Timeline = append(data.Timeline, web.TimelinePoint{
			Time:         b.Time,
			RequestCount: b.Requests,
			ErrorCount:   b.Errors,
			P50Ms:        b.P50Ms,
			P95Ms:        b.P95Ms,
			ErrorRate:    b.ErrorRate,
		})
	}

	// Map errors
	for _, e := range diag.TopErrors {
		data.TopErrors = append(data.TopErrors, web.ErrorInfo{
			Count:   e.Count,
			TraceID: e.TraceID,
			Message: e.Message,
		})
	}

	// Map slow ops
	for _, op := range diag.SlowOps {
		data.SlowOps = append(data.SlowOps, web.SlowOp{
			Operation: op.Name,
			P95Ms:     op.P95Ms,
			Count:     op.Count,
		})
	}

	// Map dependencies
	for _, dep := range diag.Dependencies {
		data.Dependencies = append(data.Dependencies, web.Dependency{
			Service:   dep.Service,
			CallCount: dep.CallCount,
			AvgMs:     dep.P95Ms,
			ErrorRate: dep.ErrorRate,
			Status:    service.DeriveHealth(dep.ErrorRate, dep.P95Ms),
		})
	}

	return renderTempl(c, web.ServiceDetail(data))
}

// Topology renders the service topology page
func (h *UIHandler) Topology(c echo.Context) error {
	ctx := c.Request().Context()
	window := parseWindow(c)
	namespace := parseNamespace(c)

	topo, err := h.svc.Topology(ctx, window, namespace, "")
	if err != nil {
		return err
	}

	var nodes []web.ServiceNode
	var edges []web.ServiceEdge

	for _, n := range topo.Nodes {
		nodes = append(nodes, web.ServiceNode{
			Name:      n.Name,
			Status:    n.Status,
			SpanCount: n.SpanCount,
			P95Ms:     n.P95Ms,
			ErrorRate: n.ErrorRate,
		})
	}

	for _, e := range topo.Edges {
		edges = append(edges, web.ServiceEdge{
			From:      e.From,
			To:        e.To,
			CallCount: e.CallCount,
			ErrorRate: e.ErrorRate,
		})
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

	// Parse query DSL
	q := search.Parse(queryStr)

	result, err := h.svc.SearchTraces(ctx, service.TraceSearchParams{
		Services:   q.Service(),
		Operations: q.Operation(),
		Status:     q.Status(),
		Duration:   q.Duration(),
		Attrs:      q.Attr(),
		TraceID:    q.TraceID(),
		SpanID:     q.SpanID(),
		Terms:      q.Terms,
		Exclude:    q.Exclude,
		Window:     window,
		Limit:      limit,
		Offset:     offset,
		Namespace:  namespace,
	})
	if err != nil {
		return err
	}

	var traces []web.TraceRow
	for _, t := range result.Traces {
		traces = append(traces, web.TraceRow{
			TraceID:   t.TraceID,
			Service:   t.Service,
			Namespace: t.Namespace,
			Operation: t.Operation,
			Duration:  t.Duration,
			Status:    t.Status,
			Time:      t.Time,
		})
	}

	// Convert facets
	var facets web.TraceFacets
	for _, f := range result.Facets.ByService {
		facets.ByService = append(facets.ByService, web.FacetCount{Value: f.Value, Count: f.Count})
	}
	for _, f := range result.Facets.ByStatus {
		facets.ByStatus = append(facets.ByStatus, web.FacetCount{Value: f.Value, Count: f.Count})
	}

	data := web.TracesData{
		Query:   queryStr,
		Traces:  traces,
		Limit:   limit,
		Offset:  offset,
		HasMore: result.HasMore,
		Window:  window,
		Facets:  facets,
	}

	return renderTempl(c, web.Traces(data))
}

// TraceDetail renders a trace detail page
func (h *UIHandler) TraceDetail(c echo.Context) error {
	ctx := c.Request().Context()
	traceID := c.Param("id")
	window := parseWindow(c)
	namespace := parseNamespace(c)

	result, err := h.svc.TraceDetail(ctx, traceID, window, namespace, "")
	if err != nil {
		return err
	}

	var spans []web.SpanInfo
	for _, sp := range result.Spans {
		// Convert events
		var events []web.SpanEvent
		for _, ev := range sp.Events {
			events = append(events, web.SpanEvent{
				Time:       ev.Time,
				Name:       ev.Name,
				Attributes: ev.Attributes,
			})
		}
		// Convert links
		var links []web.SpanLink
		for _, ln := range sp.Links {
			links = append(links, web.SpanLink{
				TraceID:    ln.TraceID,
				SpanID:     ln.SpanID,
				TraceState: ln.TraceState,
				Attributes: ln.Attributes,
			})
		}
		spans = append(spans, web.SpanInfo{
			SpanID:       sp.SpanID,
			ParentID:     sp.ParentID,
			Service:      sp.Service,
			Operation:    sp.Operation,
			Duration:     sp.Duration,
			SelfTime:     sp.SelfTime,
			Status:       sp.Status,
			StatusMsg:    sp.StatusMsg,
			Kind:         sp.Kind,
			StartOffset:  sp.StartOffset,
			Depth:        sp.Depth,
			Events:       events,
			Links:        links,
			TraceState:   sp.TraceState,
			Flags:        sp.Flags,
			ScopeName:    sp.ScopeName,
			ScopeVersion: sp.ScopeVersion,
			Attributes:   sp.Attributes,
		})
	}

	var logs []web.LogInfo
	for _, lg := range result.Logs {
		logs = append(logs, web.LogInfo{
			Time:           lg.Time,
			ObservedTime:   lg.ObservedTime,
			Service:        lg.Service,
			Severity:       lg.Severity,
			SeverityNumber: lg.SeverityNumber,
			Body:           lg.Body,
			SpanID:         lg.SpanID,
			Flags:          lg.Flags,
			ScopeName:      lg.ScopeName,
			ScopeVersion:   lg.ScopeVersion,
			Attributes:     lg.Attributes,
		})
	}

	data := web.TraceDetailData{
		TraceID:     result.TraceID,
		RootService: result.RootService,
		RootOp:      result.RootOp,
		Duration:    result.Duration,
		SpanCount:   result.SpanCount,
		RootCause:   result.RootCause,
		Spans:       spans,
		Logs:        logs,
		Window:      window,
	}

	return renderTempl(c, web.TraceDetail(data))
}

// Logs renders the logs search page
func (h *UIHandler) Logs(c echo.Context) error {
	ctx := c.Request().Context()
	queryStr := c.QueryParam("q")
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

	// Parse query DSL
	q := search.Parse(queryStr)

	result, err := h.svc.SearchLogs(ctx, service.LogSearchParams{
		Services:  q.Service(),
		Severity:  q.Severity(),
		Terms:     q.Terms,
		Exclude:   q.Exclude,
		Window:    window,
		Limit:     limit,
		Offset:    offset,
		Namespace: namespace,
	})
	if err != nil {
		return err
	}

	var logs []web.LogRow
	for _, lg := range result.Logs {
		logs = append(logs, web.LogRow{
			Time:           lg.Time,
			ObservedTime:   lg.ObservedTime,
			Service:        lg.Service,
			Namespace:      lg.Namespace,
			Severity:       lg.Severity,
			SeverityNumber: lg.SeverityNumber,
			Body:           lg.Body,
			TraceID:        lg.TraceID,
			SpanID:         lg.SpanID,
			Flags:          lg.Flags,
			ScopeName:      lg.ScopeName,
			ScopeVersion:   lg.ScopeVersion,
			Attributes:     lg.Attributes,
		})
	}

	data := web.LogsData{
		Query:   queryStr,
		Logs:    logs,
		Limit:   limit,
		Offset:  offset,
		Window:  window,
		HasMore: result.HasMore,
	}

	return renderTempl(c, web.Logs(data))
}

// Metrics renders the metrics explorer page
func (h *UIHandler) Metrics(c echo.Context) error {
	ctx := c.Request().Context()
	queryStr := c.QueryParam("q")
	window := parseWindow(c)
	namespace := parseNamespace(c)

	// Parse query DSL
	q := search.Parse(queryStr)

	result, err := h.svc.Metrics(ctx, service.MetricsParams{
		Names:     q.Name(),
		Services:  q.Service(),
		Types:     q.Type(),
		Terms:     q.Terms,
		Window:    window,
		Namespace: namespace,
	})
	if err != nil {
		return err
	}

	var metrics []web.MetricSummary
	for _, m := range result.Metrics {
		metrics = append(metrics, web.MetricSummary{
			Name:     m.Name,
			Type:     m.Type,
			Count:    m.Count,
			Avg:      m.Avg,
			Min:      m.Min,
			Max:      m.Max,
			Services: m.Services,
			Trend:    m.Trend,
		})
	}

	data := web.MetricsData{
		Query:   queryStr,
		Window:  window,
		Metrics: metrics,
	}

	return renderTempl(c, web.Metrics(data))
}

// Unified renders the unified timeline page
func (h *UIHandler) Unified(c echo.Context) error {
	ctx := c.Request().Context()
	queryStr := c.QueryParam("q")
	window := parseWindow(c)
	namespace := parseNamespace(c)

	// Parse query DSL
	q := search.Parse(queryStr)

	// Get service filter
	svc := ""
	if services := q.Service(); len(services) > 0 {
		svc = services[0]
	}

	result, err := h.svc.Unified(ctx, service.UnifiedParams{
		Service:   svc,
		Window:    window,
		Limit:     100,
		Namespace: namespace,
	})
	if err != nil {
		return err
	}

	var events []web.UnifiedEvent
	for _, e := range result.Events {
		events = append(events, web.UnifiedEvent{
			Time:     e.Time,
			Type:     e.Type,
			Service:  e.Service,
			Name:     e.Name,
			Value:    e.Value,
			Status:   e.Status,
			TraceID:  e.TraceID,
			SpanID:   e.SpanID,
			Severity: e.Severity,
			Duration: e.Duration,
		})
	}

	data := web.UnifiedData{
		Query:       queryStr,
		Service:     svc,
		Events:      events,
		SpanCount:   result.SpanCount,
		LogCount:    result.LogCount,
		MetricCount: result.MetricCount,
		HasMore:     result.HasMore,
		Window:      window,
		Limit:       100,
		Offset:      0,
	}

	return renderTempl(c, web.Unified(data))
}

// renderTempl is a helper to render templ components
func renderTempl(c echo.Context, component templ.Component) error {
	c.Response().Header().Set(echo.HeaderContentType, echo.MIMETextHTML)
	return component.Render(c.Request().Context(), c.Response().Writer)
}

// parseWindow reads window query param and returns minutes (default 60, max 90 days)
func parseWindow(c echo.Context) int {
	window := 60
	if w := c.QueryParam("window"); w != "" {
		fmt.Sscanf(w, "%d", &window)
	}
	if window < 1 {
		window = 1
	}
	if window > 129600 { // 90 days max
		window = 129600
	}
	return window
}

// parseNamespace reads namespace query param
func parseNamespace(c echo.Context) string {
	return c.QueryParam("ns")
}
