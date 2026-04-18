package api

import (
	"net"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	appconfig "github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/env"
)

type ConfigHandler struct {
	cfg   env.Config
	store *appconfig.Store
}

func RegisterConfigRoutes(e *echo.Echo, cfg env.Config, store *appconfig.Store) {
	h := &ConfigHandler{cfg: cfg, store: store}
	adminOnly := RequireRole("admin")

	e.GET("/api/config/ingest", h.GetIngestConfig, adminOnly)
	e.POST("/api/config/ingest", h.UpsertIngestConfig, adminOnly)
}

func (h *ConfigHandler) GetIngestConfig(c *echo.Context) error {
	current, err := h.store.GetIngest(c.Request().Context())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to load ingest config")
	}
	return c.JSON(http.StatusOK, ingestConfigResponse{
		Mode:              string(current.Mode),
		PublicEndpoint:    current.PublicEndpoint,
		SuggestedEndpoint: suggestedIngestEndpoint(c.Request(), h.cfg.OTLPGRPCAddr),
		TLSConfigured:     h.cfg.OTLPTLSEnabled(),
		HeaderName:        "x-fanout-ingest-token",
	})
}

func (h *ConfigHandler) UpsertIngestConfig(c *echo.Context) error {
	var req struct {
		Mode           string `json:"mode"`
		PublicEndpoint string `json:"public_endpoint"`
	}
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request")
	}

	actor := ""
	if user := GetCurrentUser(c); user != nil {
		actor = user.Email
	}

	switch appconfig.IngestMode(strings.TrimSpace(req.Mode)) {
	case appconfig.IngestModePrivate:
		if err := h.store.ResetIngest(c.Request().Context(), actor, "set ingest private"); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to update ingest config")
		}
		return c.JSON(http.StatusOK, ingestConfigResponse{
			Mode:              string(appconfig.IngestModePrivate),
			SuggestedEndpoint: suggestedIngestEndpoint(c.Request(), h.cfg.OTLPGRPCAddr),
			TLSConfigured:     h.cfg.OTLPTLSEnabled(),
			HeaderName:        "x-fanout-ingest-token",
		})
	case appconfig.IngestModePublic:
		if !h.cfg.OTLPTLSEnabled() {
			return echo.NewHTTPError(http.StatusConflict, "OTLP TLS must be configured before enabling public ingest")
		}
		token, hash, err := appconfig.GenerateIngestToken()
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate ingest token")
		}
		publicEndpoint := strings.TrimSpace(req.PublicEndpoint)
		if publicEndpoint == "" {
			publicEndpoint = suggestedIngestEndpoint(c.Request(), h.cfg.OTLPGRPCAddr)
		}
		next := appconfig.IngestConfig{
			Mode:           appconfig.IngestModePublic,
			PublicEndpoint: publicEndpoint,
			TokenHash:      hash,
		}
		if err := h.store.SetIngest(c.Request().Context(), next, actor, "enable public ingest"); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return c.JSON(http.StatusOK, ingestConfigResponse{
			Mode:              string(next.Mode),
			PublicEndpoint:    next.PublicEndpoint,
			SuggestedEndpoint: suggestedIngestEndpoint(c.Request(), h.cfg.OTLPGRPCAddr),
			TLSConfigured:     true,
			HeaderName:        "x-fanout-ingest-token",
			IngestToken:       token,
		})
	default:
		return echo.NewHTTPError(http.StatusBadRequest, "mode must be private or public")
	}
}

type ingestConfigResponse struct {
	Mode              string `json:"mode"`
	PublicEndpoint    string `json:"public_endpoint"`
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
