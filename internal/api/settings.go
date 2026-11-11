package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"

	"github.com/labstack/pitot/internal/config"
	"github.com/labstack/pitot/internal/settings"
)

type SettingsHandler struct {
	cfg   config.Config
	store *settings.Store
}

func RegisterSettingsRoutes(e *echo.Echo, cfg config.Config, store *settings.Store) {
	if store == nil {
		panic("api: settings route dependencies are required")
	}
	h := &SettingsHandler{cfg: cfg, store: store}
	// GET returns non-secret metadata (token_required, endpoint, header name)
	// used by the home empty state — readable by any authenticated user.
	e.GET("/api/settings/ingest", h.GetIngest, RequireCapability(ReadIngestMetadata))
	// Names and hints identify a workspace's collectors, so the token list is
	// for administrators only, unlike the metadata above.
	e.GET("/api/settings/ingest/tokens", h.ListIngestTokens, RequireCapability(ManageIngest))
	e.POST("/api/settings/ingest/tokens", h.CreateIngestToken, RequireCapability(ManageIngest))
	e.POST("/api/settings/ingest/tokens/:id/revoke", h.RevokeIngestToken, RequireCapability(ManageIngest))
}

// GetIngest returns connection metadata and whether any token is active. Token
// names and hints are not part of it.
func (h *SettingsHandler) GetIngest(c *echo.Context) error {
	active, err := h.store.HasActiveIngestToken(c.Request().Context())
	if err != nil {
		slog.Error("settings: load ingest failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to load ingest config")
	}
	return c.JSON(http.StatusOK, h.ingestConnection(c, active))
}

// ListIngestTokens returns active tokens only: revoked rows are kept for audit
// and for the seed rule, but no longer identify anything an administrator
// can act on.
func (h *SettingsHandler) ListIngestTokens(c *echo.Context) error {
	tokens, err := h.store.ListActiveIngestTokens(c.Request().Context())
	if err != nil {
		slog.Error("settings: list ingest tokens failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to list ingest tokens")
	}
	return c.JSON(http.StatusOK, ingestTokensResponse{Tokens: tokens, MaxActive: settings.MaxActiveIngestTokens})
}

// CreateIngestToken issues a named token. The plaintext appears only in this
// response, which must never be cached.
func (h *SettingsHandler) CreateIngestToken(c *echo.Context) error {
	var req createIngestTokenRequest
	if err := c.Bind(&req); err != nil {
		return newAPIError(http.StatusBadRequest, "invalid_ingest_token_name", invalidIngestTokenNameMessage)
	}
	token, plaintext, err := h.store.CreateIngestToken(c.Request().Context(), req.Name, ingestTokenActor(c))
	switch {
	case errors.Is(err, settings.ErrInvalidIngestTokenName):
		return newAPIError(http.StatusBadRequest, "invalid_ingest_token_name", invalidIngestTokenNameMessage)
	case errors.Is(err, settings.ErrIngestTokenNameTaken):
		return newAPIError(http.StatusConflict, "ingest_token_name_taken", "An active token already uses that name. Choose another name.")
	case errors.Is(err, settings.ErrIngestTokenLimitReached):
		return newAPIError(http.StatusConflict, "ingest_token_limit_reached", fmt.Sprintf("This workspace has %d active ingest tokens, the maximum. Revoke one before creating another.", settings.MaxActiveIngestTokens))
	case err != nil:
		slog.Error("settings: create ingest token failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to create ingest token")
	}
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusCreated, createIngestTokenResponse{
		ingestResponse: h.ingestConnection(c, true),
		Token:          token,
		IngestToken:    plaintext,
	})
}

// RevokeIngestToken stops one token. Revoking the last active token is allowed;
// the browser warns first because ingest then rejects every export.
func (h *SettingsHandler) RevokeIngestToken(c *echo.Context) error {
	err := h.store.RevokeIngestToken(c.Request().Context(), c.Param("id"), ingestTokenActor(c))
	if errors.Is(err, settings.ErrIngestTokenNotFound) {
		return newAPIError(http.StatusNotFound, "ingest_token_not_found", "That token does not exist or was already revoked.")
	}
	if err != nil {
		slog.Error("settings: revoke ingest token failed", "err", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to revoke ingest token")
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *SettingsHandler) ingestConnection(c *echo.Context, active bool) ingestResponse {
	return ingestResponse{
		TokenRequired:     active,
		SuggestedEndpoint: suggestedIngestEndpoint(c.Request(), h.cfg),
		TLSConfigured:     h.cfg.TLSEnabled(),
		HeaderName:        "Authorization",
	}
}

func ingestTokenActor(c *echo.Context) settings.IngestTokenActor {
	actor := settings.IngestTokenActor{RemoteIP: c.RealIP(), UserAgent: c.Request().UserAgent()}
	if user := GetCurrentUser(c); user != nil {
		actor.UserID = user.ID
	}
	return actor
}

var invalidIngestTokenNameMessage = fmt.Sprintf("Name must be 1 to %d printable characters.", settings.MaxIngestTokenNameLength)

type ingestResponse struct {
	TokenRequired     bool   `json:"token_required"`
	SuggestedEndpoint string `json:"suggested_endpoint"`
	TLSConfigured     bool   `json:"tls_configured"`
	HeaderName        string `json:"header_name"`
}

type ingestTokensResponse struct {
	Tokens    []settings.IngestToken `json:"tokens"`
	MaxActive int                    `json:"max_active"`
}

type createIngestTokenRequest struct {
	Name string `json:"name"`
}

type createIngestTokenResponse struct {
	ingestResponse
	Token       settings.IngestToken `json:"token"`
	IngestToken string               `json:"ingest_token"`
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
