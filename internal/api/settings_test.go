package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/settings"
	appstore "github.com/labstack/fanout/internal/store"
)

func TestGetIngest_OpenByDefault(t *testing.T) {
	e, users, secret, _ := newConfigServer(t, env.Config{OTLPGRPCAddr: ":4317"})
	_, token := createAdminForRuntimeConfigTest(t, users, secret)

	req := httptest.NewRequest(http.MethodGet, "/api/settings/ingest", nil)
	req.Host = "fanout.example.com:7520"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp ingestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if resp.TokenRequired {
		t.Fatalf("token_required = true, want false for fresh store")
	}
	if resp.SuggestedEndpoint != "fanout.example.com:4317" {
		t.Fatalf("suggested endpoint = %q", resp.SuggestedEndpoint)
	}
}

func TestRotateIngestToken_PersistsHashReturnsPlaintext(t *testing.T) {
	e, users, secret, store := newConfigServer(t, env.Config{OTLPGRPCAddr: ":4317"})
	_, token := createAdminForRuntimeConfigTest(t, users, secret)

	req := httptest.NewRequest(http.MethodPost, "/api/settings/ingest/rotate-token", nil)
	req.Host = "fanout.example.com:7520"
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp ingestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if resp.IngestToken == "" {
		t.Fatal("ingest token is empty")
	}
	if !resp.TokenRequired {
		t.Fatal("token_required = false after rotate")
	}

	current, err := store.GetIngest(req.Context())
	if err != nil {
		t.Fatalf("GetIngest: %v", err)
	}
	if !settings.CheckIngestToken(resp.IngestToken, current.TokenHash) {
		t.Fatal("stored token hash does not match returned plaintext")
	}
}

func TestIngestEndpoints_RequireAdmin(t *testing.T) {
	e, users, secret, _ := newConfigServer(t, env.Config{OTLPGRPCAddr: ":4317"})

	viewer, err := users.Create("viewer@example.com", "", "viewer")
	if err != nil {
		t.Fatalf("Create viewer: %v", err)
	}
	nonAdminToken, err := auth.SignAccess(secret, viewer.ID)
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/settings/ingest"},
		{http.MethodPost, "/api/settings/ingest/rotate-token"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+nonAdminToken)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
			}
		})
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
	RegisterAuthRoutes(e, users, codes, setup, settings.NewStore(sqlite.DB), secret, refreshSecret, auth.SMTPConfig{})
	RegisterSettingsRoutes(e, cfg, store)
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
