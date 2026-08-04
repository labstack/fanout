package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestConfigureClientIPTrustsOnlyConfiguredProxyNetworks(t *testing.T) {
	for _, tc := range []struct {
		name, cidrs, remote, want string
	}{
		{name: "trusted proxy", cidrs: "10.20.0.0/16", remote: "10.20.0.8:43100", want: "203.0.113.9"},
		{name: "untrusted private peer", cidrs: "10.20.0.0/16", remote: "192.168.1.8:43100", want: "192.168.1.8"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			if err := ConfigureClientIP(e, tc.cidrs); err != nil {
				t.Fatal(err)
			}
			e.GET("/", func(c *echo.Context) error { return c.String(http.StatusOK, c.RealIP()) })
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set("X-Forwarded-For", "203.0.113.9")
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			if rec.Body.String() != tc.want {
				t.Fatalf("RealIP = %q, want %q", rec.Body.String(), tc.want)
			}
		})
	}
}

func TestConfigureClientIPRejectsInvalidCIDR(t *testing.T) {
	if err := ConfigureClientIP(echo.New(), "private"); err == nil {
		t.Fatal("invalid trusted proxy CIDR was accepted")
	}
}
