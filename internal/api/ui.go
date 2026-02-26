package api

import (
	_ "embed"
	"log/slog"
	"strings"

	"github.com/labstack/echo/v5"
	"github.com/microcosm-cc/bluemonday"

	"github.com/labstack/fanout/internal/ai"
	"github.com/labstack/fanout/internal/config"
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
	cfg       config.Config
	orch      *ai.Orchestrator
	wsHandler *ai.WSHandler
	bookmarks *ai.BookmarkStore
	sanitizer *bluemonday.Policy
}

// RegisterUIRoutes registers all routes and returns the handler.
func RegisterUIRoutes(e *echo.Echo, cfg config.Config, orch *ai.Orchestrator, wsHandler *ai.WSHandler, bookmarks *ai.BookmarkStore) *UIHandler {
	h := &UIHandler{
		cfg:       cfg,
		orch:      orch,
		wsHandler: wsHandler,
		bookmarks: bookmarks,
		sanitizer: newSanitizer(),
	}

	// Favicon
	e.GET("/favicon.ico", Favicon)
	e.GET("/favicon.svg", Favicon)

	// WebSocket
	e.GET("/ws/chat", h.WebSocket)

	// Bookmarks API
	e.GET("/api/bookmarks", h.ListBookmarks)
	e.POST("/api/bookmarks", h.CreateBookmark)
	e.DELETE("/api/bookmarks/:id", h.DeleteBookmark)

	// Suggestions API
	e.GET("/api/suggestions", h.Suggestions)

	return h
}

// Favicon serves the SVG favicon.
func Favicon(c *echo.Context) error {
	c.Response().Header().Set("Content-Type", "image/svg+xml")
	c.Response().Header().Set("Cache-Control", "public, max-age=86400")
	return c.Blob(200, "image/svg+xml", faviconSVG)
}

// WebSocket upgrades to WS and handles the chat session.
func (h *UIHandler) WebSocket(c *echo.Context) error {
	if h.wsHandler == nil {
		return echo.NewHTTPError(503, "AI chat not configured")
	}
	return h.wsHandler.Handle(c)
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

// Suggestions returns contextual starter questions.
func (h *UIHandler) Suggestions(c *echo.Context) error {
	if h.orch == nil {
		return c.JSON(200, []string{})
	}
	return c.JSON(200, h.orch.SuggestedQuestions(c.Request().Context()))
}
