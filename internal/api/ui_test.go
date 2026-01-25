package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestParseWindow(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected int
	}{
		{"default", "", 60},
		{"15 minutes", "window=15", 15},
		{"1 hour", "window=60", 60},
		{"6 hours", "window=360", 360},
		{"24 hours", "window=1440", 1440},
		{"7 days", "window=10080", 10080},
		{"30 days", "window=43200", 43200},
		{"90 days max", "window=129600", 129600},
		{"over max clamped", "window=200000", 129600},
		{"negative clamped to 1", "window=-10", 1},
		{"zero clamped to 1", "window=0", 1},
		{"invalid defaults to 60", "window=abc", 60},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/?"+tc.query, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			result := parseWindow(c)
			if result != tc.expected {
				t.Errorf("parseWindow() with query %q = %d, want %d",
					tc.query, result, tc.expected)
			}
		})
	}
}

func TestParseNamespace(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		expected string
	}{
		{"empty", "", ""},
		{"default namespace", "ns=default", "default"},
		{"custom namespace", "ns=production", "production"},
		{"with other params", "ns=staging&window=60", "staging"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/?"+tc.query, nil)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			result := parseNamespace(c)
			if result != tc.expected {
				t.Errorf("parseNamespace() with query %q = %q, want %q",
					tc.query, result, tc.expected)
			}
		})
	}
}
