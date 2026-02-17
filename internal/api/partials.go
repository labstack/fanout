package api

import (
	"fmt"

	"github.com/labstack/echo/v4"
	"github.com/labstack/fanout/internal/search"
	"github.com/labstack/fanout/internal/service"
	"github.com/labstack/fanout/internal/web"
	"golang.org/x/sync/errgroup"
)

// cachePartial sets short cache headers for partial responses
func cachePartial(c echo.Context) {
	c.Response().Header().Set("Cache-Control", "max-age=10, stale-while-revalidate=30")
}

// RegisterPartialRoutes registers htmx partial routes
func RegisterPartialRoutes(e *echo.Echo, h *UIHandler) {
	p := e.Group("/partials")

	// Overview partials
	p.GET("/overview", h.PartialOverview)
	p.GET("/overview/stats", h.PartialStats)
	p.GET("/overview/metrics", h.PartialKeyMetrics)
	p.GET("/overview/issues", h.PartialTopIssues)
	p.GET("/overview/services", h.PartialServicesTable)
	p.GET("/overview/timeline", h.PartialTimeline)

	// Page content partials (for HTMX refresh)
	p.GET("/services", h.PartialServices)
	p.GET("/services/:name", h.PartialServiceDetail)
	p.GET("/traces", h.PartialTraces)
	p.GET("/logs", h.PartialLogs)
}

// PartialOverview returns the full overview content (no layout)
func (h *UIHandler) PartialOverview(c echo.Context) error {
	cachePartial(c)
	ctx := c.Request().Context()
	window := parseWindow(c)
	namespace := parseNamespace(c)

	data := web.OverviewData{Window: window}

	status, err := h.svc.Status(ctx, window, namespace, "")
	if err != nil {
		return err
	}
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

	topo, err := h.svc.Topology(ctx, window, namespace, "")
	if err != nil {
		return err
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

	timeline, err := h.svc.Timeline(ctx, "", window, 5, namespace, "")
	if err != nil {
		return err
	}
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

	return renderTempl(c, web.OverviewContent(data))
}

// PartialStats returns just the stats grid
func (h *UIHandler) PartialStats(c echo.Context) error {
	cachePartial(c)
	ctx := c.Request().Context()
	window := parseWindow(c)
	namespace := parseNamespace(c)
	status, err := h.svc.Status(ctx, window, namespace, "")
	if err != nil {
		return err
	}

	data := web.ServiceSummary{
		Total:     status.Services.Total,
		Healthy:   status.Services.Healthy,
		Degraded:  status.Services.Degraded,
		Unhealthy: status.Services.Unhealthy,
	}

	return renderTempl(c, web.StatsGrid(data))
}

// PartialKeyMetrics returns just the key metrics grid
func (h *UIHandler) PartialKeyMetrics(c echo.Context) error {
	cachePartial(c)
	ctx := c.Request().Context()
	window := parseWindow(c)
	namespace := parseNamespace(c)
	status, err := h.svc.Status(ctx, window, namespace, "")
	if err != nil {
		return err
	}
	return renderTempl(c, web.KeyMetrics(status.ThroughputPerMin, status.P95Ms, status.ErrorRate))
}

// PartialTopIssues returns just the top issues table
func (h *UIHandler) PartialTopIssues(c echo.Context) error {
	cachePartial(c)
	ctx := c.Request().Context()
	window := parseWindow(c)
	namespace := parseNamespace(c)
	status, err := h.svc.Status(ctx, window, namespace, "")
	if err != nil {
		return err
	}

	var issues []web.TopIssue
	for _, issue := range status.TopIssues {
		issues = append(issues, web.TopIssue{
			Service: issue.Service,
			Issue:   issue.Issue,
			Value:   issue.Value,
			Detail:  issue.Detail,
		})
	}

	return renderTempl(c, web.TopIssues(issues, window))
}

// PartialServicesTable returns just the services table
func (h *UIHandler) PartialServicesTable(c echo.Context) error {
	cachePartial(c)
	ctx := c.Request().Context()
	window := parseWindow(c)
	namespace := parseNamespace(c)
	topo, err := h.svc.Topology(ctx, window, namespace, "")
	if err != nil {
		return err
	}

	var nodes []web.ServiceNode
	for _, n := range topo.Nodes {
		nodes = append(nodes, web.ServiceNode{
			Name:      n.Name,
			Status:    n.Status,
			SpanCount: n.SpanCount,
			P95Ms:     n.P95Ms,
			ErrorRate: n.ErrorRate,
		})
	}

	return renderTempl(c, web.ServicesTable(nodes, window))
}

// PartialServices returns the services list content (no layout)
func (h *UIHandler) PartialServices(c echo.Context) error {
	cachePartial(c)
	ctx := c.Request().Context()
	window := parseWindow(c)
	namespace := parseNamespace(c)
	filter := c.QueryParam("filter")

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

	return renderTempl(c, web.ServicesContent(data))
}

// PartialServiceDetail returns the service detail content (no layout)
func (h *UIHandler) PartialServiceDetail(c echo.Context) error {
	cachePartial(c)
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
	for _, e := range diag.TopErrors {
		data.TopErrors = append(data.TopErrors, web.ErrorInfo{
			Count:   e.Count,
			TraceID: e.TraceID,
			Message: e.Message,
		})
	}
	for _, op := range diag.SlowOps {
		data.SlowOps = append(data.SlowOps, web.SlowOp{
			Operation: op.Name,
			P95Ms:     op.P95Ms,
			Count:     op.Count,
		})
	}

	return renderTempl(c, web.ServiceDetailContent(data))
}

// PartialTraces returns traces results (no layout)
func (h *UIHandler) PartialTraces(c echo.Context) error {
	cachePartial(c)
	ctx := c.Request().Context()
	queryStr := c.QueryParam("q")
	window := parseWindow(c)
	namespace := parseNamespace(c)

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

	data := web.TracesData{
		Query:   queryStr,
		Traces:  traces,
		Limit:   limit,
		Offset:  offset,
		HasMore: result.HasMore,
		Window:  window,
	}

	return renderTempl(c, web.TraceResultsContent(data))
}

// PartialLogs returns log results (no layout)
func (h *UIHandler) PartialLogs(c echo.Context) error {
	cachePartial(c)
	ctx := c.Request().Context()
	queryStr := c.QueryParam("q")
	window := parseWindow(c)
	namespace := parseNamespace(c)

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
			Time:     lg.Time,
			Service:  lg.Service,
			Severity: lg.Severity,
			Body:     lg.Body,
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

	return renderTempl(c, web.LogResultsContent(data))
}

// PartialTimeline returns just the timeline chart
func (h *UIHandler) PartialTimeline(c echo.Context) error {
	cachePartial(c)
	ctx := c.Request().Context()
	window := parseWindow(c)
	namespace := parseNamespace(c)
	timeline, err := h.svc.Timeline(ctx, "", window, 5, namespace, "")
	if err != nil {
		return err
	}

	var data web.TimelineData
	for _, b := range timeline.Buckets {
		data.Buckets = append(data.Buckets, web.TimelineBucket{
			Time:         b.Time,
			RequestCount: b.Requests,
			ErrorCount:   b.Errors,
			P95Ms:        b.P95Ms,
			ErrorRate:    b.ErrorRate,
			IsAnomaly:    b.IsAnomaly,
		})
	}

	return renderTempl(c, web.TimelineChart(data))
}
