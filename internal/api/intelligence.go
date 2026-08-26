package api

import (
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/intelligence"
)

// IntelligenceSnapshots is the read side of the detector used by HTTP and
// other consumers. Keeping the interface narrow makes the endpoint independent
// of how snapshots are generated or retained.
type IntelligenceSnapshots interface {
	LatestSnapshot() *intelligence.IntelligenceSnapshot
}

// RegisterIntelligenceRoutes exposes the detector's latest completed snapshot.
func RegisterIntelligenceRoutes(e *echo.Echo, snapshots IntelligenceSnapshots) {
	h := &intelligenceHandler{snapshots: snapshots}
	e.GET("/api/intelligence", h.latest, RequireCapability(ReadTelemetry))
}

type intelligenceHandler struct {
	snapshots IntelligenceSnapshots
}

func (h *intelligenceHandler) latest(c *echo.Context) error {
	if h.snapshots == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "intelligence snapshot is not ready")
	}
	snapshot := h.snapshots.LatestSnapshot()
	if snapshot == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "intelligence snapshot is not ready")
	}
	return c.JSON(http.StatusOK, snapshot)
}
