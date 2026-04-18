package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"github.com/labstack/fanout/internal/auth"
	appstore "github.com/labstack/fanout/internal/store"
)

func newTestAuthServer(t *testing.T) (*echo.Echo, *auth.UserStore, string, string) {
	t.Helper()

	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })

	secret := "0123456789abcdef0123456789abcdef"
	refreshSecret := "abcdef0123456789abcdef0123456789"
	setupToken := "setup-token-012345"
	users := auth.NewUserStore(sqlite.DB)
	codes := auth.NewCodeStore(sqlite.DB, secret)

	e := echo.New()
	RegisterAuthMiddleware(e, users, secret)
	RegisterAuthRoutes(e, users, codes, setupToken, secret, refreshSecret, auth.SMTPConfig{})
	return e, users, secret, refreshSecret
}

func TestRequireRoleUsesCurrentUserState(t *testing.T) {
	e, users, secret, _ := newTestAuthServer(t)
	user, err := users.Create("admin@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := users.Create("second-admin@example.com", "", "admin"); err != nil {
		t.Fatalf("Create second admin: %v", err)
	}

	e.GET("/api/admin", func(c *echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	}, RequireRole("admin"))

	token, err := auth.SignAccess(secret, user.ID)
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	role := "viewer"
	if _, err := users.Update(user.ID, nil, nil, &role, nil); err != nil {
		t.Fatalf("Update: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}

func TestMeRejectsInactiveUser(t *testing.T) {
	e, users, secret, _ := newTestAuthServer(t)
	user, err := users.Create("user@example.com", "", "operator")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	token, err := auth.SignAccess(secret, user.ID)
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	active := false
	if _, err := users.Update(user.ID, nil, nil, nil, &active); err != nil {
		t.Fatalf("Update: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestRefreshRotatesAndLogoutRevokesSession(t *testing.T) {
	e, users, _, refreshSecret := newTestAuthServer(t)
	user, err := users.Create("user@example.com", "", "operator")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	issuedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	if err := users.TouchLoginAt(user.ID, issuedAt); err != nil {
		t.Fatalf("TouchLoginAt: %v", err)
	}
	refreshToken, err := auth.SignRefresh(refreshSecret, user.ID, issuedAt)
	if err != nil {
		t.Fatalf("SignRefresh: %v", err)
	}

	refreshReq := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	refreshReq.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshToken})
	refreshRec := httptest.NewRecorder()
	e.ServeHTTP(refreshRec, refreshReq)

	if refreshRec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want %d", refreshRec.Code, http.StatusOK)
	}

	newCookie := refreshRec.Result().Cookies()[0]
	newClaims, err := auth.VerifyRefresh(refreshSecret, newCookie.Value)
	if err != nil {
		t.Fatalf("VerifyRefresh(new cookie): %v", err)
	}
	if !newClaims.IssuedAt.Time.After(issuedAt) && !newClaims.IssuedAt.Time.Equal(issuedAt) {
		t.Fatalf("new refresh token should not be older than original")
	}

	reuseReq := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	reuseReq.AddCookie(&http.Cookie{Name: "refresh_token", Value: refreshToken})
	reuseRec := httptest.NewRecorder()
	e.ServeHTTP(reuseRec, reuseReq)
	if reuseRec.Code != http.StatusUnauthorized {
		t.Fatalf("reuse status = %d, want %d", reuseRec.Code, http.StatusUnauthorized)
	}

	logoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	logoutReq.AddCookie(newCookie)
	logoutRec := httptest.NewRecorder()
	e.ServeHTTP(logoutRec, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want %d", logoutRec.Code, http.StatusOK)
	}

	postLogoutReq := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	postLogoutReq.AddCookie(newCookie)
	postLogoutRec := httptest.NewRecorder()
	e.ServeHTTP(postLogoutRec, postLogoutReq)
	if postLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("post-logout refresh status = %d, want %d", postLogoutRec.Code, http.StatusUnauthorized)
	}
}

func TestStartDoesNotRevealAccountState(t *testing.T) {
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })

	secret := "0123456789abcdef0123456789abcdef"
	refreshSecret := "abcdef0123456789abcdef0123456789"
	users := auth.NewUserStore(sqlite.DB)
	codes := auth.NewCodeStore(sqlite.DB, secret)

	inactive, err := users.Create("inactive@example.com", "", "operator")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	active := false
	if _, err := users.Update(inactive.ID, nil, nil, nil, &active); err != nil {
		t.Fatalf("Update: %v", err)
	}

	e := echo.New()
	RegisterAuthMiddleware(e, users, secret)
	RegisterAuthRoutes(e, users, codes, "setup-token-012345", secret, refreshSecret, auth.SMTPConfig{})

	for _, email := range []string{"missing@example.com", "inactive@example.com"} {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/start", strings.NewReader(`{"email":"`+email+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", email, rec.Code, http.StatusOK)
		}

		var body map[string]bool
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s decode response: %v", email, err)
		}
		if !body["code_sent"] {
			t.Fatalf("%s code_sent = false, want true", email)
		}
	}
}

func TestStartReturnsServiceUnavailableWhenEmailDeliveryFails(t *testing.T) {
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })

	secret := "0123456789abcdef0123456789abcdef"
	refreshSecret := "abcdef0123456789abcdef0123456789"
	users := auth.NewUserStore(sqlite.DB)
	codes := auth.NewCodeStore(sqlite.DB, secret)
	if _, err := users.Create("active@example.com", "", "operator"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	e := echo.New()
	RegisterAuthMiddleware(e, users, secret)
	RegisterAuthRoutes(e, users, codes, "setup-token-012345", secret, refreshSecret, auth.SMTPConfig{
		Host: "127.0.0.1",
		Port: 1,
		User: "user",
		Pass: "pass",
		From: "Fanout <noreply@example.com>",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/auth/start", strings.NewReader(`{"email":"active@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestSetupRequiresValidSetupToken(t *testing.T) {
	e, users, _, _ := newTestAuthServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"email":"admin@example.com","setup_token":"wrong-token"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	count, err := users.CountUsers()
	if err != nil {
		t.Fatalf("CountUsers: %v", err)
	}
	if count != 0 {
		t.Fatalf("user count = %d, want 0", count)
	}
}

func TestSetupCreatesAdminWithValidSetupToken(t *testing.T) {
	e, users, _, _ := newTestAuthServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(`{"email":"admin@example.com","setup_token":"setup-token-012345"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	user, err := users.GetByEmail("admin@example.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if user.Role != "admin" {
		t.Fatalf("role = %q, want %q", user.Role, "admin")
	}
}
