package api

import (
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/settings"
)

type ConfigHandler struct {
	cfg   env.Config
	store *settings.Store
}

func RegisterConfigRoutes(e *echo.Echo, cfg env.Config, store *settings.Store) {
	h := &ConfigHandler{cfg: cfg, store: store}
	adminOnly := RequireRole("admin")

	e.GET("/api/config/ingest", h.GetIngestConfig, adminOnly)
	e.POST("/api/config/ingest/rotate-token", h.RotateIngestToken, adminOnly)
	e.DELETE("/api/config/ingest/token", h.ClearIngestToken, adminOnly)
}

// GetIngestConfig returns the current ingest config: whether a token is set
// (but not the token itself) and the endpoint collectors should use.
func (h *ConfigHandler) GetIngestConfig(c *echo.Context) error {
	current, err := h.store.GetIngest(c.Request().Context())
	if err != nil {
		slog.Error("config: load ingest failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to load ingest config")
	}
	return c.JSON(http.StatusOK, ingestConfigResponse{
		TokenRequired:     current.TokenHash != "",
		SuggestedEndpoint: suggestedIngestEndpoint(c.Request(), h.cfg.OTLPGRPCAddr),
		TLSConfigured:     h.cfg.TLSEnabled(),
		HeaderName:        "x-fanout-ingest-token",
	})
}

// RotateIngestToken issues a new token, persists only its hash, and returns
// the plaintext exactly once in the response.
func (h *ConfigHandler) RotateIngestToken(c *echo.Context) error {
	token, hash, err := settings.GenerateIngestToken()
	if err != nil {
		slog.Error("config: generate ingest token failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate ingest token")
	}
	if err := h.store.SetIngest(c.Request().Context(), settings.Ingest{TokenHash: hash}); err != nil {
		slog.Error("config: persist ingest token failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to update ingest config")
	}
	return c.JSON(http.StatusOK, ingestConfigResponse{
		TokenRequired:     true,
		SuggestedEndpoint: suggestedIngestEndpoint(c.Request(), h.cfg.OTLPGRPCAddr),
		TLSConfigured:     h.cfg.TLSEnabled(),
		HeaderName:        "x-fanout-ingest-token",
		IngestToken:       token,
	})
}

// ClearIngestToken removes the stored token hash; ingest returns to unauthenticated.
func (h *ConfigHandler) ClearIngestToken(c *echo.Context) error {
	if err := h.store.ClearIngest(c.Request().Context()); err != nil {
		slog.Error("config: clear ingest failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to clear ingest config")
	}
	return c.JSON(http.StatusOK, ingestConfigResponse{
		TokenRequired:     false,
		SuggestedEndpoint: suggestedIngestEndpoint(c.Request(), h.cfg.OTLPGRPCAddr),
		TLSConfigured:     h.cfg.TLSEnabled(),
		HeaderName:        "x-fanout-ingest-token",
	})
}

type ingestConfigResponse struct {
	TokenRequired     bool   `json:"token_required"`
	SuggestedEndpoint string `json:"suggested_endpoint"`
	TLSConfigured     bool   `json:"tls_configured"`
	HeaderName        string `json:"header_name"`
	IngestToken       string `json:"ingest_token,omitempty"`
}

func suggestedIngestEndpoint(req *http.Request, grpcAddr string) string {
	host, port := splitHostPort(grpcAddr)
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = req.Host
		if requestHost, _, err := net.SplitHostPort(req.Host); err == nil {
			host = requestHost
		}
	}
	if host == "" {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
}

func splitHostPort(addr string) (string, string) {
	if host, port, err := net.SplitHostPort(addr); err == nil {
		return host, port
	}
	if strings.HasPrefix(addr, ":") {
		return "", strings.TrimPrefix(addr, ":")
	}
	return addr, "4317"
}
