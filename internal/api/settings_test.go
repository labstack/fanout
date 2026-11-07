package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/settings"
)

func newConfigServer(t *testing.T, cfg config.Config) (*testAuthServer, *settings.Store) {
	t.Helper()
	cfg.AuthMode = "local"
	s := newTestAuthServerWith(t, cfg, auth.SMTPConfig{})
	store := settings.NewStore(s.db.DB)
	RegisterSettingsRoutes(s.e, cfg, store, s.audit)
	return s, store
}

func TestGetIngest_EmptyBeforeSetup(t *testing.T) {
	s, _ := newConfigServer(t, config.Config{Addr: ":7520"})
	admin, _ := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	cookie := s.login(t, admin)
	req := sessionRequest(http.MethodGet, "/api/settings/ingest", nil, cookie)
	req.Host = "fanout.example.com:7520"
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var resp ingestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if resp.TokenRequired || resp.SuggestedEndpoint != "http://fanout.example.com:7520" || resp.HeaderName != "Authorization" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestRotateIngestToken_PersistsHashReturnsPlaintext(t *testing.T) {
	s, store := newConfigServer(t, config.Config{Addr: ":7520"})
	admin, _ := s.users.CreateWithAudit("admin@example.com", "", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	cookie := s.login(t, admin)
	req := sessionRequest(http.MethodPost, "/api/settings/ingest/token/rotate", nil, cookie)
	req.Host = "fanout.example.com:7520"
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rec.Code, rec.Body.String())
	}
	var resp ingestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	current, err := store.GetIngest(req.Context())
	if err != nil || resp.IngestToken == "" || resp.HeaderName != "Authorization" || !settings.CheckIngestToken(resp.IngestToken, current.TokenHash) {
		t.Fatalf("response=%+v current=%+v err=%v", resp, current, err)
	}
}

// An administrator created from FANOUT_FIRST_ADMIN_EMAIL never runs interactive setup, so
// the first ingest token comes from the rotate action on an empty setting.
func TestRotateIngestToken_IssuesFirstTokenForDesignatedAdministrator(t *testing.T) {
	s, store := newConfigServer(t, config.Config{Addr: ":7520"})
	firstAdmin, _, err := s.users.DesignateFirstAdmin("first@example.com")
	if err != nil {
		t.Fatalf("DesignateFirstAdmin: %v", err)
	}
	cookie := s.login(t, firstAdmin)
	read := func() ingestResponse {
		t.Helper()
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, sessionRequest(http.MethodGet, "/api/settings/ingest", nil, cookie))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET status = %d %s", rec.Code, rec.Body.String())
		}
		var resp ingestResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		return resp
	}
	if read().TokenRequired {
		t.Fatal("designated administrator database already has an ingest token")
	}
	rec := httptest.NewRecorder()
	req := sessionRequest(http.MethodPost, "/api/settings/ingest/token/rotate", nil, cookie)
	s.e.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d %s", rec.Code, rec.Body.String())
	}
	var resp ingestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	current, err := store.GetIngest(req.Context())
	if err != nil || !resp.TokenRequired || !settings.CheckIngestToken(resp.IngestToken, current.TokenHash) {
		t.Fatalf("response=%+v current=%+v err=%v", resp, current, err)
	}
	if !read().TokenRequired {
		t.Fatal("token_required is false after the first token was issued")
	}
	var events int
	if err := s.db.DB.QueryRow(`SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'ingest_key.rotated' AND actor_user_id = ?`, firstAdmin.ID).Scan(&events); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if events != 1 {
		t.Fatalf("ingest audit events = %d, want 1", events)
	}
}

func TestIngestSettingsCapabilities(t *testing.T) {
	s, _ := newConfigServer(t, config.Config{Addr: ":7520"})
	viewer, _ := s.users.CreateWithAudit("viewer@example.com", "", "viewer", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	cookie := s.login(t, viewer)
	readRec := httptest.NewRecorder()
	s.e.ServeHTTP(readRec, sessionRequest(http.MethodGet, "/api/settings/ingest", nil, cookie))
	if readRec.Code != http.StatusOK {
		t.Fatalf("viewer read = %d", readRec.Code)
	}
	writeRec := httptest.NewRecorder()
	s.e.ServeHTTP(writeRec, sessionRequest(http.MethodPost, "/api/settings/ingest/token/rotate", nil, cookie))
	if writeRec.Code != http.StatusForbidden {
		t.Fatalf("viewer rotate = %d, want 403", writeRec.Code)
	}
}

func TestSuggestedIngestEndpoint(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		host string
		want string
	}{
		{"public origin", config.Config{Addr: ":7520", PublicURL: "https://fanout.example.com/"}, "internal:7520", "https://fanout.example.com"},
		{"request port", config.Config{Addr: ":7520"}, "localhost:8080", "http://localhost:8080"},
		{"direct TLS", config.Config{TLSCertFile: "cert", TLSKeyFile: "key"}, "fanout.example.com:8443", "https://fanout.example.com:8443"},
		{"IPv6", config.Config{}, "[2001:db8::1]:7520", "http://[2001:db8::1]:7520"},
		{"missing host", config.Config{Addr: ":8080"}, "", "http://localhost:8080"},
		{"loopback bind", config.Config{Addr: "127.0.0.1:8080"}, "", "http://127.0.0.1:8080"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/settings/ingest", nil)
			req.Host = tc.host
			req.Header.Set("X-Forwarded-Host", "untrusted.example.com")
			req.Header.Set("X-Forwarded-Proto", "https")
			if got := suggestedIngestEndpoint(req, tc.cfg); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
