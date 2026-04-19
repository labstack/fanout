package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/settings"
	appstore "github.com/labstack/fanout/internal/store"
)

func TestGetIngestConfig_DefaultPrivate(t *testing.T) {
	e, users, secret, store := newConfigServer(t, env.Config{OTLPGRPCAddr: ":4317"})
	_, token := createAdminForRuntimeConfigTest(t, users, secret)

	req := httptest.NewRequest(http.MethodGet, "/api/config/ingest", nil)
	req.Host = "fanout.example.com:7520"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp ingestConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if resp.Mode != "private" {
		t.Fatalf("mode = %q, want private", resp.Mode)
	}
	if resp.SuggestedEndpoint != "fanout.example.com:4317" {
		t.Fatalf("suggested endpoint = %q", resp.SuggestedEndpoint)
	}

	current, err := store.GetIngest(req.Context())
	if err != nil {
		t.Fatalf("GetIngest: %v", err)
	}
	if current.Mode != settings.IngestModePrivate {
		t.Fatalf("stored mode = %q, want private", current.Mode)
	}
}

func TestUpsertIngestConfig_PublicRequiresTLS(t *testing.T) {
	e, users, secret, _ := newConfigServer(t, env.Config{})
	_, token := createAdminForRuntimeConfigTest(t, users, secret)

	req := httptest.NewRequest(http.MethodPost, "/api/config/ingest", strings.NewReader(`{"mode":"public","public_endpoint":"fanout.example.com:4317"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestUpsertIngestConfig_PublicGeneratesToken(t *testing.T) {
	e, users, secret, store := newConfigServer(t, env.Config{
		OTLPGRPCAddr: ":4317",
		TLSCertFile:  "server.pem",
		TLSKeyFile:   "server-key.pem",
	})
	_, token := createAdminForRuntimeConfigTest(t, users, secret)

	req := httptest.NewRequest(http.MethodPost, "/api/config/ingest", strings.NewReader(`{"mode":"public","public_endpoint":"fanout.example.com:4317"}`))
	req.Host = "fanout.example.com:7520"
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp ingestConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if resp.IngestToken == "" {
		t.Fatal("ingest token is empty")
	}
	if resp.Mode != "public" {
		t.Fatalf("mode = %q, want public", resp.Mode)
	}

	current, err := store.GetIngest(req.Context())
	if err != nil {
		t.Fatalf("GetIngest: %v", err)
	}
	if current.Mode != settings.IngestModePublic {
		t.Fatalf("stored mode = %q, want public", current.Mode)
	}
	if !settings.CheckIngestToken(resp.IngestToken, current.TokenHash) {
		t.Fatal("stored token hash does not match returned ingest token")
	}
}

func newConfigServer(t *testing.T, cfg env.Config) (*echo.Echo, *auth.UserStore, string, *settings.Store) {
	t.Helper()

	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })

	secret := "0123456789abcdef0123456789abcdef"
	refreshSecret := "abcdef0123456789abcdef0123456789"
	users := auth.NewUserStore(sqlite.DB)
	codes := auth.NewCodeStore(sqlite.DB, secret)
	store := settings.NewStore(sqlite.DB)
	setup := auth.NewSetup()

	e := echo.New()
	RegisterAuthMiddleware(e, users, secret)
	RegisterAuthRoutes(e, users, codes, setup, secret, refreshSecret, auth.SMTPConfig{})
	RegisterConfigRoutes(e, cfg, store)
	return e, users, secret, store
}

func createAdminForRuntimeConfigTest(t *testing.T, users *auth.UserStore, secret string) (auth.User, string) {
	t.Helper()

	user, err := users.Create("admin@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	token, err := auth.SignAccess(secret, user.ID)
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}
	return user, token
}
