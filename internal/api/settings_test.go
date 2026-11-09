package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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
	RegisterSettingsRoutes(s.e, cfg, store)
	return s, store
}

func sessionFor(t *testing.T, s *testAuthServer, email string, role auth.Role) *http.Cookie {
	t.Helper()
	user, err := s.users.CreateWithAudit(email, "", role, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("create %s: %v", role, err)
	}
	return s.login(t, user)
}

func createTokenRequest(t *testing.T, s *testAuthServer, cookie *http.Cookie, name string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		t.Fatal(err)
	}
	req := sessionRequest(http.MethodPost, "/api/settings/ingest/tokens", strings.NewReader(string(body)), cookie)
	req.Header.Set("Content-Type", "application/json")
	req.Host = "fanout.example.com:7520"
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, req)
	return rec
}

func createdToken(t *testing.T, rec *httptest.ResponseRecorder) createIngestTokenResponse {
	t.Helper()
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d %s", rec.Code, rec.Body.String())
	}
	var resp createIngestTokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return resp
}

func serveSession(t *testing.T, s *testAuthServer, method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.e.ServeHTTP(rec, sessionRequest(method, path, nil, cookie))
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error %s: %v", rec.Body.String(), err)
	}
	return body.Code
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

func TestCreateIngestTokenReturnsPlaintextOnceWithNoStore(t *testing.T) {
	s, store := newConfigServer(t, config.Config{Addr: ":7520"})
	cookie := sessionFor(t, s, "admin@example.com", "admin")

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	rec := createTokenRequest(t, s, cookie, "collector-a")
	resp := createdToken(t, rec)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if resp.IngestToken == "" || resp.Token.Name != "collector-a" || resp.Token.ID == "" || resp.HeaderName != "Authorization" || !resp.TokenRequired {
		t.Fatalf("response = %+v", resp)
	}
	if ok, err := store.AuthenticateIngestToken(t.Context(), resp.IngestToken); err != nil || !ok {
		t.Fatalf("created token: ok = %v err = %v", ok, err)
	}

	digest := settings.HashIngestToken(resp.IngestToken)
	for _, later := range []string{"/api/settings/ingest", "/api/settings/ingest/tokens"} {
		next := serveSession(t, s, http.MethodGet, later, cookie)
		if next.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", later, next.Code)
		}
		if strings.Contains(next.Body.String(), resp.IngestToken) || strings.Contains(next.Body.String(), digest) {
			t.Fatalf("GET %s exposed token material", later)
		}
	}
	if revoked := serveSession(t, s, http.MethodPost, "/api/settings/ingest/tokens/"+resp.Token.ID+"/revoke", cookie); revoked.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", revoked.Code, revoked.Body.String())
	}
	if strings.Contains(logs.String(), resp.IngestToken) || strings.Contains(logs.String(), digest) {
		t.Fatal("logs exposed token material")
	}
}

func TestListIngestTokensShowsActiveMetadataOnly(t *testing.T) {
	s, _ := newConfigServer(t, config.Config{Addr: ":7520"})
	cookie := sessionFor(t, s, "admin@example.com", "admin")
	first := createdToken(t, createTokenRequest(t, s, cookie, "collector-a"))
	second := createdToken(t, createTokenRequest(t, s, cookie, "collector-b"))
	if rec := serveSession(t, s, http.MethodPost, "/api/settings/ingest/tokens/"+first.Token.ID+"/revoke", cookie); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke = %d %s", rec.Code, rec.Body.String())
	}

	rec := serveSession(t, s, http.MethodGet, "/api/settings/ingest/tokens", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	var listed struct {
		Tokens    []map[string]any `json:"tokens"`
		MaxActive int              `json:"max_active"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if listed.MaxActive != settings.MaxActiveIngestTokens || len(listed.Tokens) != 1 {
		t.Fatalf("list = %s", rec.Body.String())
	}
	item := listed.Tokens[0]
	if item["id"] != second.Token.ID || item["name"] != "collector-b" || item["hint"] != second.Token.Hint || item["created_at"] == "" {
		t.Fatalf("item = %v", item)
	}
	if _, present := item["last_used_at"]; !present {
		t.Fatalf("item has no last_used_at: %v", item)
	}
	if len(item) != 5 {
		t.Fatalf("item fields = %v, want exactly the documented five", item)
	}
}

func TestIngestTokenRoutesRequireManageIngest(t *testing.T) {
	s, _ := newConfigServer(t, config.Config{Addr: ":7520"})
	adminCookie := sessionFor(t, s, "admin@example.com", "admin")
	created := createdToken(t, createTokenRequest(t, s, adminCookie, "secret-collector-name"))

	for _, role := range []auth.Role{"viewer", "operator"} {
		cookie := sessionFor(t, s, string(role)+"@example.com", role)
		t.Run(string(role), func(t *testing.T) {
			read := serveSession(t, s, http.MethodGet, "/api/settings/ingest", cookie)
			if read.Code != http.StatusOK {
				t.Fatalf("metadata read = %d", read.Code)
			}
			if body := read.Body.String(); strings.Contains(body, "secret-collector-name") || strings.Contains(body, created.Token.Hint) || strings.Contains(body, created.Token.ID) {
				t.Fatalf("metadata exposed token details: %s", body)
			}
			if rec := serveSession(t, s, http.MethodGet, "/api/settings/ingest/tokens", cookie); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "secret-collector-name") {
				t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
			}
			if rec := createTokenRequest(t, s, cookie, "nope"); rec.Code != http.StatusForbidden {
				t.Fatalf("create = %d", rec.Code)
			}
			if rec := serveSession(t, s, http.MethodPost, "/api/settings/ingest/tokens/"+created.Token.ID+"/revoke", cookie); rec.Code != http.StatusForbidden {
				t.Fatalf("revoke = %d", rec.Code)
			}
		})
	}
}

func TestRotateRouteIsGone(t *testing.T) {
	s, _ := newConfigServer(t, config.Config{Addr: ":7520"})
	cookie := sessionFor(t, s, "admin@example.com", "admin")
	if rec := serveSession(t, s, http.MethodPost, "/api/settings/ingest/token/rotate", cookie); rec.Code != http.StatusNotFound {
		t.Fatalf("rotate = %d, want 404", rec.Code)
	}
}

func TestCreateIngestTokenExplainsRejections(t *testing.T) {
	s, _ := newConfigServer(t, config.Config{Addr: ":7520"})
	cookie := sessionFor(t, s, "admin@example.com", "admin")

	bad := createTokenRequest(t, s, cookie, "   ")
	if bad.Code != http.StatusBadRequest || errorCode(t, bad) != "invalid_ingest_token_name" {
		t.Fatalf("blank name = %d %s", bad.Code, bad.Body.String())
	}
	createdToken(t, createTokenRequest(t, s, cookie, "collector"))
	taken := createTokenRequest(t, s, cookie, "Collector")
	if taken.Code != http.StatusConflict || errorCode(t, taken) != "ingest_token_name_taken" {
		t.Fatalf("duplicate = %d %s", taken.Code, taken.Body.String())
	}
	for i := 1; i < settings.MaxActiveIngestTokens; i++ {
		createdToken(t, createTokenRequest(t, s, cookie, "collector-"+strings.Repeat("x", i)))
	}
	limited := createTokenRequest(t, s, cookie, "one-too-many")
	if limited.Code != http.StatusConflict || errorCode(t, limited) != "ingest_token_limit_reached" || !strings.Contains(limited.Body.String(), "Revoke one") {
		t.Fatalf("over cap = %d %s", limited.Code, limited.Body.String())
	}
}

func TestRevokeUnknownIngestTokenIsNotFound(t *testing.T) {
	s, _ := newConfigServer(t, config.Config{Addr: ":7520"})
	cookie := sessionFor(t, s, "admin@example.com", "admin")
	rec := serveSession(t, s, http.MethodPost, "/api/settings/ingest/tokens/missing/revoke", cookie)
	if rec.Code != http.StatusNotFound || errorCode(t, rec) != "ingest_token_not_found" {
		t.Fatalf("revoke = %d %s", rec.Code, rec.Body.String())
	}
}

func TestIngestTokensPageIsAValidSignInReturnPath(t *testing.T) {
	if got := safeReturnTo("/settings/ingest"); got != "/settings/ingest" {
		t.Fatalf("safeReturnTo = %q, want the ingest token page", got)
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
				t.Fatalf("suggestedIngestEndpoint = %q, want %q", got, tc.want)
			}
		})
	}
}
