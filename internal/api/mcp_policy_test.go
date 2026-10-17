package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
)

func TestMCPOriginsPrecedeAuthenticationOnBothMounts(t *testing.T) {
	s := newTestAuthServerWith(t, config.Config{PublicURL: "https://FANOUT.example.com:443/path/"}, auth.SMTPConfig{})
	h, err := NewMCPAuthorization(auth.NewOAuthStore(s.db.DB), s.users, testMCPResource)
	if err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unauthenticated request reached MCP") })
	s.e.Any("/mcp", echo.WrapHandler(h.ProtectMCP(next)))
	s.e.Any("/api/mcp", h.ProtectBrowserMCP(s.sessions, next))
	for _, path := range []string{"/mcp", "/api/mcp"} {
		for _, origin := range []string{"absent", "https://fanout.example.com", "https://attacker.example", "null", "https://fanout.example.com/", "http://[broken"} {
			for _, method := range []string{"POST", "GET", "DELETE"} {
				headers := map[string]string{"Authorization": "Bearer invalid", "Fanout-Request": "1"}
				want := http.StatusForbidden
				if origin == "absent" || origin == "https://fanout.example.com" {
					want = http.StatusUnauthorized
				}
				if origin != "absent" {
					headers["Origin"] = origin
				}
				rec := serve(t, s.e, method, path, `{}`, headers)
				if rec.Code != want {
					t.Fatalf("%s %s origin %q = %d, want %d", method, path, origin, rec.Code, want)
				}
			}
		}
	}
	for _, path := range []string{"/mcp", "/api/mcp"} {
		r := httptest.NewRequest("POST", path, nil)
		r.Host = "fanout.example.com"
		r.Header["Origin"] = []string{"https://fanout.example.com", "https://fanout.example.com"}
		rec := httptest.NewRecorder()
		s.e.ServeHTTP(rec, r)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s multiple origins = %d, want 403", path, rec.Code)
		}
	}
	// A token lookup would now fail with 500; an untrusted origin still gets
	// 403 before either mount attempts authentication or session loading.
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/mcp", "/api/mcp"} {
		rec := serve(t, s.e, "POST", path, `{}`, map[string]string{"Authorization": "Bearer invalid", "Origin": "https://attacker.example"})
		if rec.Code != 403 {
			t.Fatalf("closed store %s = %d", path, rec.Code)
		}
	}
}

func TestMCPOriginValidation(t *testing.T) {
	for _, tc := range []struct {
		name, publicURL, host string
		origins               []string
		want                  bool
	}{
		{"absent", "https://fanout.example.com", "internal:7520", nil, true},
		{"https default port", "https://fanout.example.com:443", "internal:7520", []string{"https://fanout.example.com"}, true},
		{"http default port", "http://fanout.example.com:80", "internal:7520", []string{"http://fanout.example.com"}, true},
		{"uppercase configuration", "HTTPS://FANOUT.example.com:443", "internal:7520", []string{"https://fanout.example.com"}, true},
		{"trailing slash configuration", "https://fanout.example.com/", "internal:7520", []string{"https://fanout.example.com"}, true},
		{"path configuration", "https://fanout.example.com/path/", "internal:7520", []string{"https://fanout.example.com"}, true},
		{"non-default port", "https://fanout.example.com:8443/path", "internal:7520", []string{"https://fanout.example.com:8443"}, true},
		{"non-default port required", "https://fanout.example.com:8443", "internal:7520", []string{"https://fanout.example.com"}, false},
		{"IPv6 default port", "https://[::1]:443/path", "internal:7520", []string{"https://[::1]"}, true},
		{"IPv6 non-default port", "https://[::1]:8443/path", "internal:7520", []string{"https://[::1]:8443"}, true},
		{"request must be serialized", "https://fanout.example.com", "internal:7520", []string{"https://FANOUT.example.com"}, false},
		{"explicit default request port", "https://fanout.example.com", "internal:7520", []string{"https://fanout.example.com:443"}, false},
		{"scheme mismatch", "https://fanout.example.com", "fanout.example.com", []string{"http://fanout.example.com"}, false},
		{"untrusted", "https://fanout.example.com", "attacker.example", []string{"https://attacker.example"}, false},
		{"empty", "https://fanout.example.com", "internal:7520", []string{""}, false},
		{"two values", "https://fanout.example.com", "internal:7520", []string{"https://fanout.example.com", "https://attacker.example"}, false},
		{"two identical values", "https://fanout.example.com", "internal:7520", []string{"https://fanout.example.com", "https://fanout.example.com"}, false},
		{"local absent", "", "localhost:7520", nil, true},
		{"localhost", "", "localhost:7520", []string{"http://localhost:7520"}, true},
		{"local HTTPS", "", "LOCALHOST:7520", []string{"HTTPS://localhost:7520"}, true},
		{"IPv4 loopback", "", "127.0.0.1:7520", []string{"http://127.0.0.1:7520"}, true},
		{"IPv6 loopback", "", "[::1]:7520", []string{"http://[::1]:7520"}, true},
		{"local wrong authority", "", "localhost:7520", []string{"http://localhost:7521"}, false},
		{"local attacker", "", "attacker.example", []string{"http://attacker.example"}, false},
		{"local private IP", "", "192.168.0.1", []string{"http://192.168.0.1"}, false},
		{"local two values", "", "localhost:7520", []string{"http://localhost:7520", "http://localhost:7520"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/mcp", nil)
			r.Host = tc.host
			if tc.origins != nil {
				r.Header["Origin"] = tc.origins
			}
			if got := trustedMCPOrigin(r, serializedMCPOrigin(tc.publicURL)); got != tc.want {
				t.Fatalf("trusted=%v want %v", got, tc.want)
			}
		})
	}
	for _, publicURL := range []string{"https://fanout.example.com", ""} {
		for _, origin := range []string{"null", "", "http://[broken", "https://user@fanout.example.com", "https://fanout.example.com/", "https://fanout.example.com/path", "https://fanout.example.com?", "https://fanout.example.com#", "https://fanout.example.com:99999", "https://fanout.example.com https://attacker.example", "http://localhost:7520/"} {
			r := httptest.NewRequest("POST", "/mcp", nil)
			r.Host = "localhost:7520"
			r.Header.Set("Origin", origin)
			if trustedMCPOrigin(r, serializedMCPOrigin(publicURL)) {
				t.Errorf("malformed origin %q accepted with public URL %q", origin, publicURL)
			}
		}
	}
}

func TestMCPToolLimiterEvictionAndCapacity(t *testing.T) {
	l := newMCPToolLimiter()
	now := time.Unix(100000, 0)
	l.now = func() time.Time { return now }
	for i := range mcpToolMaxEntries {
		if ok, _ := l.allow(mcpToolKey{fmt.Sprint(i), "client"}); !ok {
			t.Fatalf("entry %d rejected", i)
		}
	}
	if ok, retry := l.allow(mcpToolKey{"overflow", "client"}); ok || retry <= 0 || len(l.entries) != mcpToolMaxEntries {
		t.Fatalf("capacity=%d ok=%v retry=%d", len(l.entries), ok, retry)
	}
	now = now.Add(mcpToolIdleTTL)
	if ok, _ := l.allow(mcpToolKey{"new", "client"}); !ok || len(l.entries) != 1 {
		t.Fatalf("eviction: entries=%d allowed=%v", len(l.entries), ok)
	}
}

func TestMCPToolLimiterOAuthClientsAreIndependentAndRefill(t *testing.T) {
	l := newMCPToolLimiter()
	now := time.Unix(100000, 0)
	l.now = func() time.Time { return now }
	key := mcpToolKey{"user", "client"}
	for range mcpToolBurst {
		if ok, _ := l.allow(key); !ok {
			t.Fatal("burst rejected early")
		}
	}
	if ok, _ := l.allow(key); ok {
		t.Fatal("exhausted burst allowed")
	}
	if ok, _ := l.allow(mcpToolKey{"user", "other-client"}); !ok {
		t.Fatal("independent client limited")
	}
	now = now.Add(time.Second)
	for range int(mcpToolRate) {
		if ok, _ := l.allow(key); !ok {
			t.Fatal("refill rejected")
		}
	}
	if ok, _ := l.allow(key); ok {
		t.Fatal("refill exceeded")
	}
}
