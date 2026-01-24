package api

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/fanout/internal/web"
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
}

// PartialOverview returns the full overview content (no layout)
func (h *UIHandler) PartialOverview(c echo.Context) error {
	cachePartial(c)
	ctx := c.Request().Context()
	window := parseWindow(c)
	namespace := parseNamespace(c)

	data := web.OverviewData{Window: window}

	status, _ := h.svc.Status(ctx, window, namespace, "")
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

	topo, _ := h.svc.Topology(ctx, window, namespace, "")
	for _, n := range topo.Nodes {
		data.Topology.Nodes = append(data.Topology.Nodes, web.ServiceNode{
			Name:      n.Name,
			Status:    n.Status,
			SpanCount: n.SpanCount,
			P95Ms:     n.P95Ms,
			ErrorRate: n.ErrorRate,
		})
	}

	timeline, _ := h.svc.Timeline(ctx, "", window, 5, namespace, "")
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
	status, _ := h.svc.Status(ctx, window, namespace, "")

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
	status, _ := h.svc.Status(ctx, window, namespace, "")
	return renderTempl(c, web.KeyMetrics(status.ThroughputPerMin, status.P95Ms, status.ErrorRate))
}

// PartialTopIssues returns just the top issues table
func (h *UIHandler) PartialTopIssues(c echo.Context) error {
	cachePartial(c)
	ctx := c.Request().Context()
	window := parseWindow(c)
	namespace := parseNamespace(c)
	status, _ := h.svc.Status(ctx, window, namespace, "")

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
	topo, _ := h.svc.Topology(ctx, window, namespace, "")

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

// PartialTimeline returns just the timeline chart
func (h *UIHandler) PartialTimeline(c echo.Context) error {
	cachePartial(c)
	ctx := c.Request().Context()
	window := parseWindow(c)
	namespace := parseNamespace(c)
	timeline, _ := h.svc.Timeline(ctx, "", window, 5, namespace, "")

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
