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
	s, _ := newConfigServer(t, config.Config{OTLPGRPCAddr: ":4317"})
	admin, _ := s.users.Create("admin@example.com", "", "admin")
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
	if resp.TokenRequired || resp.SuggestedEndpoint != "fanout.example.com:4317" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestRotateIngestToken_PersistsHashReturnsPlaintext(t *testing.T) {
	s, store := newConfigServer(t, config.Config{OTLPGRPCAddr: ":4317"})
	admin, _ := s.users.Create("admin@example.com", "", "admin")
	cookie := s.login(t, admin)
	req := sessionRequest(http.MethodPost, "/api/settings/ingest/rotate-token", nil, cookie)
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
	if err != nil || resp.IngestToken == "" || !settings.CheckIngestToken(resp.IngestToken, current.TokenHash) {
		t.Fatalf("response=%+v current=%+v err=%v", resp, current, err)
	}
}

func TestIngestSettingsCapabilities(t *testing.T) {
	s, _ := newConfigServer(t, config.Config{OTLPGRPCAddr: ":4317"})
	viewer, _ := s.users.Create("viewer@example.com", "", "viewer")
	cookie := s.login(t, viewer)
	readRec := httptest.NewRecorder()
	s.e.ServeHTTP(readRec, sessionRequest(http.MethodGet, "/api/settings/ingest", nil, cookie))
	if readRec.Code != http.StatusOK {
		t.Fatalf("viewer read = %d", readRec.Code)
	}
	writeRec := httptest.NewRecorder()
	s.e.ServeHTTP(writeRec, sessionRequest(http.MethodPost, "/api/settings/ingest/rotate-token", nil, cookie))
	if writeRec.Code != http.StatusForbidden {
		t.Fatalf("viewer rotate = %d, want 403", writeRec.Code)
	}
}

func TestSuggestedIngestEndpoint(t *testing.T) {
	tests := []struct{ name, grpcAddr, advertised, reqHost, want string }{
		{"advertised endpoint wins verbatim", ":4317", "https://ingest.example.com", "fanout.example.com", "https://ingest.example.com"},
		{"wildcard addr derives host from request", ":4317", "", "fanout.example.com:443", "fanout.example.com:4317"},
		{"explicit grpc host is used as-is", "1.2.3.4:5317", "", "ignored.example.com", "1.2.3.4:5317"},
		{"loopback bind advertises application host", "127.0.0.1:4317", "", "fanout.example.com", "fanout.example.com:4317"},
		{"missing request host falls back", ":4317", "", "", "localhost:4317"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/settings/ingest", nil)
			req.Host = tc.reqHost
			if got := suggestedIngestEndpoint(req, tc.grpcAddr, tc.advertised); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
