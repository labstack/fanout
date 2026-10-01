package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/observability"
)

type ObservabilityQueries interface {
	Overview(context.Context, observability.Scope, int) (observability.Result[observability.Overview], error)
	Topology(context.Context, observability.Scope, int) (observability.Result[observability.Topology], error)
	Dependencies(context.Context, observability.Scope, observability.DependencyOptions) (observability.Result[observability.Dependencies], error)
	Performance(context.Context, observability.Scope, observability.PerformanceOptions) (observability.Result[observability.Performance], error)
	Trace(context.Context, observability.Scope, string, string, int) (observability.Result[observability.TraceDetail], error)
	Logs(context.Context, observability.Scope, string, string, string, int) (observability.Result[observability.Logs], error)
}

type ObservabilityHandler struct {
	queries ObservabilityQueries
	now     func() time.Time
}

func NewObservabilityHandler(queries ObservabilityQueries) *ObservabilityHandler {
	return &ObservabilityHandler{queries: queries, now: time.Now}
}

// observabilityDeadline bounds one dashboard query.
//
// The SQL API has always been bounded; these endpoints, which every widget
// calls, had no deadline of their own — a query that went pathological held a
// connection until the client gave up, and the client's own timeout is not
// something the server can rely on.
//
// Twenty seconds bounds execution and pool waits under load and is stricter
// than the SQL API's own ceiling. It is deliberately shorter than the worst
// legitimate wait behind a stuck
// publication — the drain remainder plus the swap budget can exceed a minute —
// so a publication that goes wrong answers the dashboard instead of holding
// every widget's connection until it finishes.
const observabilityDeadline = 20 * time.Second

// Register mounts the deterministic product API, each endpoint under a
// deadline. These endpoints are for dashboards and drill-downs; the browser
// does not need an agent for ordinary telemetry navigation.
func (h *ObservabilityHandler) Register(group *echo.Group) {
	group.GET("/overview", h.bounded(h.overview))
	group.GET("/topology", h.bounded(h.topology))
	group.GET("/services/dependencies", h.bounded(h.dependencies))
	group.GET("/performance", h.bounded(h.performance))
	group.GET("/trace", h.bounded(h.trace))
	group.GET("/logs", h.bounded(h.logs))
}

// bounded gives a handler a deadline. The context reaches DuckDB through
// QueryContext, so an expired one stops the statement rather than only the
// reply — and releases the pool connection it was holding.
func (h *ObservabilityHandler) bounded(handler func(*echo.Context) error) func(*echo.Context) error {
	return func(c *echo.Context) error {
		ctx, cancel := context.WithTimeout(c.Request().Context(), observabilityDeadline)
		defer cancel()
		c.SetRequest(c.Request().WithContext(ctx))
		return handler(c)
	}
}

func (h *ObservabilityHandler) performance(c *echo.Context) error {
	scope, limit, err := h.request(c)
	if err != nil {
		return err
	}
	result, err := h.queries.Performance(c.Request().Context(), scope, observability.PerformanceOptions{
		Service: c.QueryParam("service"), Limit: limit, Heatmap: true,
	})
	if err != nil {
		return mapQueryError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *ObservabilityHandler) trace(c *echo.Context) error {
	scope, limit, err := h.request(c)
	if err != nil {
		return err
	}
	result, err := h.queries.Trace(c.Request().Context(), scope, c.QueryParam("trace_id"), c.QueryParam("service"), limit)
	if err != nil {
		return mapQueryError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *ObservabilityHandler) logs(c *echo.Context) error {
	scope, limit, err := h.request(c)
	if err != nil {
		return err
	}
	result, err := h.queries.Logs(c.Request().Context(), scope, c.QueryParam("service"), c.QueryParam("severity"), c.QueryParam("search"), limit)
	if err != nil {
		return mapQueryError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *ObservabilityHandler) overview(c *echo.Context) error {
	scope, limit, err := h.request(c)
	if err != nil {
		return err
	}
	result, err := h.queries.Overview(c.Request().Context(), scope, limit)
	if err != nil {
		return mapQueryError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *ObservabilityHandler) topology(c *echo.Context) error {
	scope, limit, err := h.request(c)
	if err != nil {
		return err
	}
	result, err := h.queries.Topology(c.Request().Context(), scope, limit)
	if err != nil {
		return mapQueryError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func (h *ObservabilityHandler) request(c *echo.Context) (observability.Scope, int, error) {
	window := time.Hour
	if raw := strings.TrimSpace(c.QueryParam("window")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			return observability.Scope{}, 0, echo.NewHTTPError(http.StatusBadRequest, "window must be a positive duration such as 15m or 1h")
		}
		window = parsed
	}
	limit := 0
	if raw := strings.TrimSpace(c.QueryParam("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return observability.Scope{}, 0, echo.NewHTTPError(http.StatusBadRequest, "limit must be an integer")
		}
		limit = parsed
	}
	end := h.now().UTC()
	return observability.Scope{
		Namespace: c.QueryParam("namespace"),
		Start:     end.Add(-window),
		End:       end,
	}, limit, nil
}

func (h *ObservabilityHandler) dependencies(c *echo.Context) error {
	scope, _, err := h.request(c)
	if err != nil {
		return err
	}
	options := observability.DependencyOptions{Service: c.QueryParam("service"), Direction: c.QueryParam("direction")}
	for _, field := range []struct {
		name  string
		value *int
	}{{"max_depth", &options.MaxDepth}, {"max_nodes", &options.MaxNodes}} {
		if raw := c.QueryParam(field.name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 {
				return echo.NewHTTPError(http.StatusBadRequest, field.name+" must be a positive integer")
			}
			*field.value = value
		}
	}
	result, err := h.queries.Dependencies(c.Request().Context(), scope, options)
	if err != nil {
		return mapQueryError(err)
	}
	return c.JSON(http.StatusOK, result)
}

func mapQueryError(err error) error {
	if errors.Is(err, observability.ErrInvalidScope) || errors.Is(err, observability.ErrInvalidLimit) {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	// A server deadline is a service-unavailable response; it remains a failed
	// server request. Client cancellations below have a separate classification.
	if errors.Is(err, context.DeadlineExceeded) {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "telemetry query took too long").Wrap(err)
	}
	if errors.Is(err, context.Canceled) {
		// A disconnected client is not a server failure. The nonstandard 499
		// status is used by our request logger to keep this out of the 5xx rate.
		return echo.NewHTTPError(499, "telemetry query canceled by client").Wrap(err)
	}
	return echo.NewHTTPError(http.StatusInternalServerError, "telemetry query failed").Wrap(err)
}
