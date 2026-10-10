package api

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/panel"
)

type SystemEngine interface {
	System(context.Context, panel.SystemRequest) (*panel.Dashboard, error)
}
type SystemHandler struct{ engine SystemEngine }

func RegisterSystemRoutes(e *echo.Echo, engine SystemEngine) {
	h := &SystemHandler{engine: engine}
	e.GET("/api/system", h.get, RequireCapability(ReadTelemetry))
}
func (h *SystemHandler) get(c *echo.Context) error {
	req, err := parseSystemRequest(c)
	if err != nil {
		return panelError(c, err, "system unavailable")
	}
	ctx, cancel := context.WithTimeout(c.Request().Context(), panelDeadline)
	defer cancel()
	spec, err := h.engine.System(ctx, req)
	if err != nil {
		return panelError(c, err, "system unavailable")
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, spec)
}
func parseSystemRequest(c *echo.Context) (panel.SystemRequest, error) {
	q := c.Request().URL.Query()
	req := panel.SystemRequest{Namespace: q.Get("namespace")}
	problem := func(path, message string) (panel.SystemRequest, error) {
		return req, panel.Problems{{Path: path, Message: message}}
	}
	for _, key := range []string{"range", "from", "to", "namespace"} {
		if len(q[key]) > 1 {
			return problem(key, "parameter must appear once")
		}
	}
	_, hasRange := q["range"]
	_, hasFrom := q["from"]
	_, hasTo := q["to"]
	if hasRange && (hasFrom || hasTo) {
		return problem("time", "use range or from/to, not both")
	}
	if hasFrom != hasTo {
		return problem("time", "both from and to are required")
	}
	if hasFrom {
		from, err := time.Parse(time.RFC3339Nano, q.Get("from"))
		if err != nil {
			return problem("time.from", "from must be RFC3339Nano")
		}
		to, err := time.Parse(time.RFC3339Nano, q.Get("to"))
		if err != nil {
			return problem("time.to", "to must be RFC3339Nano")
		}
		req.Time = panel.Time{From: &from, To: &to}
	} else {
		req.Time.Range = q.Get("range")
		if !hasRange {
			req.Time.Range = "1h"
		} else if req.Time.Range == "" {
			return problem("time.range", "range must not be empty")
		}
	}
	return req, nil
}
