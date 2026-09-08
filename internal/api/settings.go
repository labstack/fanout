package api

import (
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/settings"
)

type SettingsHandler struct {
	cfg   config.Config
	store *settings.Store
	audit *auth.AuditStore
}

func RegisterSettingsRoutes(e *echo.Echo, cfg config.Config, store *settings.Store, audit *auth.AuditStore) {
	if store == nil || audit == nil {
		panic("api: settings route dependencies are required")
	}
	h := &SettingsHandler{cfg: cfg, store: store, audit: audit}
	// GET returns non-secret metadata (token_required, endpoint, header name)
	// used by the home empty state — readable by any authenticated user.
	e.GET("/api/settings/ingest", h.GetIngest, RequireCapability(ReadIngestMetadata))
	e.POST("/api/settings/ingest/rotate-token", h.RotateIngestToken, RequireCapability(ManageIngest))
}

// GetIngest returns the current ingest config: whether a token is set
// (but not the token itself) and the endpoint collectors should use.
func (h *SettingsHandler) GetIngest(c *echo.Context) error {
	current, err := h.store.GetIngest(c.Request().Context())
	if err != nil {
		slog.Error("settings: load ingest failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to load ingest config")
	}
	return c.JSON(http.StatusOK, ingestResponse{
		TokenRequired:     current.TokenHash != "",
		SuggestedEndpoint: suggestedIngestEndpoint(c.Request(), h.cfg),
		TLSConfigured:     h.cfg.TLSEnabled(),
		HeaderName:        "Authorization",
	})
}

// RotateIngestToken issues a new token, persists only its hash, and returns
// the plaintext exactly once in the response.
func (h *SettingsHandler) RotateIngestToken(c *echo.Context) error {
	token, hash, err := settings.GenerateIngestToken()
	if err != nil {
		slog.Error("settings: generate ingest token failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate ingest token")
	}
	event := auth.AuditEvent{EventType: "ingest_key.rotated", Outcome: "success", TargetType: "ingest", TargetID: "default", RemoteIP: c.RealIP(), UserAgent: c.Request().UserAgent()}
	if user := GetCurrentUser(c); user != nil {
		event.ActorUserID = user.ID
	}
	if err := h.store.SetIngestWithAudit(c.Request().Context(), settings.Ingest{TokenHash: hash}, h.audit, event); err != nil {
		slog.Error("settings: persist ingest token failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to update ingest config")
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, ingestResponse{
		TokenRequired:     true,
		SuggestedEndpoint: suggestedIngestEndpoint(c.Request(), h.cfg),
		TLSConfigured:     h.cfg.TLSEnabled(),
		HeaderName:        "Authorization",
		IngestToken:       token,
	})
}

type ingestResponse struct {
	TokenRequired     bool   `json:"token_required"`
	SuggestedEndpoint string `json:"suggested_endpoint"`
	TLSConfigured     bool   `json:"tls_configured"`
	HeaderName        string `json:"header_name"`
	IngestToken       string `json:"ingest_token,omitempty"`
}

func suggestedIngestEndpoint(req *http.Request, cfg config.Config) string {
	if cfg.PublicURL != "" {
		return strings.TrimRight(cfg.PublicURL, "/")
	}
	scheme := "http"
	if cfg.TLSEnabled() || req.TLS != nil {
		scheme = "https"
	}
	host := req.Host
	if host == "" {
		host = cfg.Addr
		if bindHost, port, err := net.SplitHostPort(host); err == nil && (bindHost == "" || bindHost == "0.0.0.0" || bindHost == "::") {
			host = net.JoinHostPort("localhost", port)
		}
		if host == "" {
			host = "localhost:7520"
		}
	}
	return scheme + "://" + host
}
