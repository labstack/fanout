package api

import (
	_ "embed"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/microcosm-cc/bluemonday"

	"github.com/labstack/fanout/internal/ai"
	"github.com/labstack/fanout/internal/alert"
	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/service"
)

// newSanitizer creates the bluemonday HTML sanitizer policy for bookmark content.
func newSanitizer() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowElements("sl-card", "sl-badge", "sl-tag", "sl-icon", "sl-progress-bar",
		"sl-spinner", "sl-tooltip", "sl-alert", "sl-button", "sl-divider",
		"sl-details", "sl-tab-group", "sl-tab", "sl-tab-panel")
	p.AllowStyles(
		"color", "background", "background-color", "font-size", "font-weight",
		"text-align", "display", "grid-template-columns", "gap", "padding", "margin",
		"border", "border-color", "border-radius", "width", "height", "max-width", "min-width",
		"flex", "flex-direction", "align-items", "justify-content",
		"opacity", "text-transform", "letter-spacing", "line-height",
		"overflow", "white-space", "text-overflow",
		"padding-left", "padding-right", "padding-top", "padding-bottom",
		"margin-left", "margin-right", "margin-top", "margin-bottom",
	).Globally()
	p.AllowAttrs("class").Globally()
	p.AllowAttrs("slot").Globally()
	p.AllowAttrs("variant", "size", "pill", "name", "label", "value", "open", "closable").Globally()
	p.AllowElements("svg", "path", "line", "rect", "circle", "text", "g", "defs",
		"linearGradient", "stop", "polyline", "polygon", "marker", "tspan")
	p.AllowAttrs("viewBox", "xmlns", "fill", "stroke", "stroke-width", "d", "x", "y",
		"x1", "y1", "x2", "y2", "cx", "cy", "r", "rx", "ry", "width", "height",
		"transform", "text-anchor", "font-size", "opacity", "points",
		"offset", "stop-color", "id", "gradientUnits",
		"refX", "refY", "markerWidth", "markerHeight", "orient", "marker-end",
		"fill-opacity", "stroke-opacity", "stroke-linecap", "stroke-linejoin",
		"stroke-dasharray", "font-weight", "font-family", "letter-spacing",
		"text-transform", "dominant-baseline", "text-decoration").Globally()
	return p
}

//go:embed favicon.svg
var faviconSVG []byte

// UIHandler handles the chat UI and API routes.
type UIHandler struct {
	cfg        env.Config
	orch       *ai.Orchestrator
	sseHandler *ai.SSEHandler
	bookmarks  *ai.BookmarkStore
	sanitizer  *bluemonday.Policy
	svc        *service.Service
	alertStore *alert.Store
	incidents  *service.IncidentTracker
}

// RegisterUIRoutes registers all routes and returns the handler.
func RegisterUIRoutes(e *echo.Echo, cfg env.Config, orch *ai.Orchestrator, sseHandler *ai.SSEHandler, bookmarks *ai.BookmarkStore, svc *service.Service, alertStore *alert.Store) *UIHandler {
	h := &UIHandler{
		cfg:        cfg,
		orch:       orch,
		sseHandler: sseHandler,
		bookmarks:  bookmarks,
		sanitizer:  newSanitizer(),
		svc:        svc,
		alertStore: alertStore,
		incidents:  service.NewIncidentTracker(),
	}

	// Favicon
	e.GET("/favicon.ico", Favicon)
	e.GET("/favicon.svg", Favicon)

	// Chat SSE
	e.POST("/api/chat", h.StreamChat)
	e.POST("/api/chat/cancel", h.CancelChat)
	e.POST("/api/chat/clear", h.ClearChat)

	// Bookmarks API
	e.GET("/api/bookmarks", h.ListBookmarks)
	e.POST("/api/bookmarks", h.CreateBookmark)
	e.DELETE("/api/bookmarks/:id", h.DeleteBookmark)

	// Suggestions API
	e.GET("/api/home", h.Home)
	e.GET("/api/services/:name", h.ServiceDetail)
	e.GET("/api/namespaces", h.Namespaces)

	return h
}

// Favicon serves the SVG favicon.
func Favicon(c *echo.Context) error {
	c.Response().Header().Set("Content-Type", "image/svg+xml")
	c.Response().Header().Set("Cache-Control", "public, max-age=86400")
	return c.Blob(200, "image/svg+xml", faviconSVG)
}

// StreamChat streams the AI response as SSE.
func (h *UIHandler) StreamChat(c *echo.Context) error {
	if h.sseHandler == nil {
		return echo.NewHTTPError(503, "AI chat not configured")
	}
	return h.sseHandler.StreamChat(c)
}

// CancelChat cancels the in-flight AI request.
func (h *UIHandler) CancelChat(c *echo.Context) error {
	if h.sseHandler == nil {
		return echo.NewHTTPError(503, "AI chat not configured")
	}
	return h.sseHandler.CancelChat(c)
}

// ClearChat resets conversation history.
func (h *UIHandler) ClearChat(c *echo.Context) error {
	if h.sseHandler == nil {
		return echo.NewHTTPError(503, "AI chat not configured")
	}
	return h.sseHandler.ClearChat(c)
}

// ListBookmarks returns all bookmarks.
func (h *UIHandler) ListBookmarks(c *echo.Context) error {
	if h.bookmarks == nil {
		return c.JSON(200, []ai.Bookmark{})
	}
	bookmarks, err := h.bookmarks.List()
	if err != nil {
		slog.Error("list bookmarks failed", "err", err)
		return echo.NewHTTPError(500, "failed to list bookmarks")
	}
	return c.JSON(200, bookmarks)
}

// CreateBookmark saves a new bookmark.
func (h *UIHandler) CreateBookmark(c *echo.Context) error {
	if h.bookmarks == nil {
		return echo.NewHTTPError(503, "bookmarks not configured")
	}
	var req struct {
		Question   string `json:"question"`
		AnswerHTML string `json:"answer_html"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(400, "invalid request")
	}
	if strings.TrimSpace(req.Question) == "" {
		return echo.NewHTTPError(400, "question is required")
	}
	// Sanitize HTML to prevent stored XSS
	answerHTML := h.sanitizer.Sanitize(req.AnswerHTML)
	b, err := h.bookmarks.Create(req.Question, answerHTML)
	if err != nil {
		slog.Error("create bookmark failed", "err", err)
		return echo.NewHTTPError(500, "failed to create bookmark")
	}
	return c.JSON(201, b)
}

// DeleteBookmark removes a bookmark by ID.
func (h *UIHandler) DeleteBookmark(c *echo.Context) error {
	if h.bookmarks == nil {
		return echo.NewHTTPError(503, "bookmarks not configured")
	}
	id := c.Param("id")
	if err := h.bookmarks.Delete(id); err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "not found") || strings.Contains(errStr, "invalid bookmark ID") {
			return echo.NewHTTPError(404, "bookmark not found")
		}
		slog.Error("delete bookmark failed", "id", id, "err", err)
		return echo.NewHTTPError(500, "failed to delete bookmark")
	}
	return c.NoContent(204)
}

// Namespaces returns discovered namespaces from telemetry data.
func (h *UIHandler) Namespaces(c *echo.Context) error {
	if h.svc == nil {
		return c.JSON(200, []string{})
	}
	ns := h.svc.Namespaces(c.Request().Context())
	if ns == nil {
		ns = []string{}
	}
	return c.JSON(200, ns)
}

// Home returns the deterministic triage home page data.
func (h *UIHandler) Home(c *echo.Context) error {
	window := 60
	if raw := strings.TrimSpace(c.QueryParam("window")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid window")
		}
		window = v
	}
	if window > 1440 {
		window = 1440
	}

	if h.svc == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "service layer not configured")
	}

	result, err := h.svc.Home(c.Request().Context(), window, c.QueryParam("namespace"), h.incidents)
	if err != nil {
		slog.Error("home query failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to build home data")
	}

	// Append firing alerts from alert store.
	if h.alertStore != nil {
		alerts, err := h.alertStore.ListAlerts("firing", "", "")
		if err != nil {
			slog.Error("failed to list firing alerts for home", "err", err)
		} else {
			for _, a := range alerts {
				result.Alerts = append(result.Alerts, service.HomeAlert{
					Rule:    a.RuleID,
					Service: a.Service,
					State:   a.State,
					Value:   a.Value,
					FiredAt: a.FiredAt,
				})
			}
		}
	}

	return c.JSON(http.StatusOK, result)
}

// ServiceDetail returns deterministic data for the Service Detail page.
func (h *UIHandler) ServiceDetail(c *echo.Context) error {
	name := c.Param("name")
	if strings.TrimSpace(name) == "" || len(name) > 256 {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid service name")
	}

	if h.svc == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "service layer not configured")
	}

	window := 60
	if raw := strings.TrimSpace(c.QueryParam("window")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid window")
		}
		window = v
	}
	if window > 1440 {
		window = 1440
	}

	result, err := h.svc.ServiceDetail(c.Request().Context(), name, window, c.QueryParam("namespace"))
	if err != nil {
		slog.Error("service detail failed", "service", name, "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to load service detail")
	}

	return c.JSON(http.StatusOK, result)
}
