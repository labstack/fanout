package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/ingest"
	httpserver "github.com/labstack/fanout/internal/server"
	"github.com/labstack/fanout/internal/settings"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"google.golang.org/grpc"
)

type discardExport struct{}

func (discardExport) Submit(context.Context, telemetrystore.Batch) error { return nil }

func TestSharedListenerCredentialBoundaries(t *testing.T) {
	s := newTestAuthServer(t)
	user, err := s.users.Create("listener@example.com", "", "admin")
	if err != nil {
		t.Fatal(err)
	}
	cookie := s.login(t, user)
	store := settings.NewStore(s.db.DB)
	token, hash, err := settings.GenerateIngestToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetIngest(context.Background(), settings.Ingest{TokenHash: hash}); err != nil {
		t.Fatal(err)
	}
	ing := ingest.NewServer(config.Config{}, discardExport{})
	g := grpc.NewServer(ingest.GRPCServerOptions(store)...)
	defer g.Stop()
	ingest.RegisterOTLP(g, ing)
	h := httpserver.New("", s.e, ingest.NewHTTPHandler(ing, store), g).Handler
	for _, tc := range []struct {
		name, method, path string
		session, ingest    bool
		want               int
	}{
		{"session reads API", "GET", "/api/auth/me", true, false, 200},
		{"ingest cannot read API", "GET", "/api/auth/me", false, true, 401},
		{"session cannot export", "POST", "/v1/traces", true, false, 401},
		{"ingest exports without browser CSRF header", "POST", "/v1/traces", false, true, 200},
		{"unknown ingest path", "GET", "/v1/unknown", true, false, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(""))
			req.Header.Set("Content-Type", "application/x-protobuf")
			if tc.session {
				req.AddCookie(cookie)
			}
			if tc.ingest {
				req.Header.Set("Authorization", "Bearer "+token)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
