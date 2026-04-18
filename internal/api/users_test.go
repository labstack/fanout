package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/auth"
)

func TestUpdateUser_RejectsDemotingLastActiveAdmin(t *testing.T) {
	e, users, secret, _ := newTestAuthServer(t)
	admin, err := users.Create("admin@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	RegisterUserRoutes(e, users, auth.SMTPConfig{})

	token, err := auth.SignAccess(secret, admin.ID)
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/users/"+admin.ID, strings.NewReader(`{"role":"viewer"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestUpdateUser_RejectsDeactivatingLastActiveAdmin(t *testing.T) {
	e, users, secret, _ := newTestAuthServer(t)
	admin, err := users.Create("admin@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	RegisterUserRoutes(e, users, auth.SMTPConfig{})

	token, err := auth.SignAccess(secret, admin.ID)
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/users/"+admin.ID, strings.NewReader(`{"active":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestDeleteUser_RejectsDeletingLastActiveAdmin(t *testing.T) {
	e, users, secret, _ := newTestAuthServer(t)
	admin, err := users.Create("admin@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	RegisterUserRoutes(e, users, auth.SMTPConfig{})

	token, err := auth.SignAccess(secret, admin.ID)
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/users/"+admin.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestDeleteUser_AllowsDeletingAdminWhenAnotherActiveAdminExists(t *testing.T) {
	e, users, secret, _ := newTestAuthServer(t)
	admin, err := users.Create("admin@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create admin: %v", err)
	}
	otherAdmin, err := users.Create("other-admin@example.com", "", "admin")
	if err != nil {
		t.Fatalf("Create other admin: %v", err)
	}
	RegisterUserRoutes(e, users, auth.SMTPConfig{})

	token, err := auth.SignAccess(secret, admin.ID)
	if err != nil {
		t.Fatalf("SignAccess: %v", err)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/users/"+otherAdmin.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}

	count, err := users.CountActiveAdmins()
	if err != nil {
		t.Fatalf("CountActiveAdmins: %v", err)
	}
	if count != 1 {
		t.Fatalf("active admin count = %d, want 1", count)
	}
}
