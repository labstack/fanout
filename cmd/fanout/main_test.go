package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/store"
)

func TestParseCommandLine(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantPath    string
		wantVersion bool
		wantEmail   string
		wantHealth  string
		wantErr     bool
	}{
		{name: "server defaults", args: nil},
		{name: "config", args: []string{"--config", "/etc/fanout.yaml"}, wantPath: "/etc/fanout.yaml"},
		{name: "version command", args: []string{"version"}, wantVersion: true},
		{name: "version flag", args: []string{"--version"}, wantVersion: true},
		{name: "short version flag", args: []string{"-v"}, wantVersion: true},
		{name: "login link", args: []string{"login-link", "admin@example.com"}, wantEmail: "admin@example.com"},
		{name: "login link with config", args: []string{"--config", "/etc/fanout.yaml", "login-link", "admin@example.com"}, wantPath: "/etc/fanout.yaml", wantEmail: "admin@example.com"},
		{name: "default healthcheck", args: []string{"healthcheck"}, wantHealth: "http://127.0.0.1:7520/healthz"},
		{name: "custom healthcheck", args: []string{"healthcheck", "http://fanout:8080/healthz"}, wantHealth: "http://fanout:8080/healthz"},
		{name: "login link missing email", args: []string{"login-link"}, wantErr: true},
		{name: "unexpected argument", args: []string{"serve"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, showVersion, email, healthURL, err := parseCommandLine(test.args, io.Discard)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
			if path != test.wantPath || showVersion != test.wantVersion || email != test.wantEmail || healthURL != test.wantHealth {
				t.Fatalf("result = (%q, %v, %q, %q), want (%q, %v, %q, %q)", path, showVersion, email, healthURL, test.wantPath, test.wantVersion, test.wantEmail, test.wantHealth)
			}
		})
	}
}

func TestParseCommandLineHelp(t *testing.T) {
	_, _, _, _, err := parseCommandLine([]string{"--help"}, io.Discard)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("error = %v, want flag.ErrHelp", err)
	}
}

func TestParseCommandLinePrintsOneErrorAndUsage(t *testing.T) {
	var output bytes.Buffer
	_, _, _, _, err := parseCommandLine([]string{"serve"}, &output)
	if err == nil {
		t.Fatal("expected unexpected-argument error")
	}
	if got := strings.Count(output.String(), "unexpected arguments: serve"); got != 1 {
		t.Fatalf("error appeared %d times in %q", got, output.String())
	}
	if !strings.Contains(output.String(), "Usage of fanout") {
		t.Fatalf("output does not include usage: %q", output.String())
	}
	if !strings.Contains(output.String(), "login-link <email>") {
		t.Fatalf("output does not include login-link command: %q", output.String())
	}
	if !strings.Contains(output.String(), "healthcheck [url]") {
		t.Fatalf("output does not include healthcheck command: %q", output.String())
	}
}

func TestCheckHealth(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
	}{
		{name: "healthy", statusCode: http.StatusOK},
		{name: "not ready", statusCode: http.StatusServiceUnavailable, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.statusCode)
			}))
			defer server.Close()
			if err := checkHealth(server.URL); (err != nil) != test.wantErr {
				t.Fatalf("checkHealth() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestCreateLoginLink(t *testing.T) {
	cfg := config.Config{
		AuthMode:       "local",
		AuthCodeSecret: "0123456789abcdef0123456789abcdef",
		DataDir:        t.TempDir(),
		HTTPAddr:       ":7520",
		PublicURL:      "https://fanout.example.com/base",
	}
	if err := os.MkdirAll(cfg.ControlDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sqlite, err := store.NewSQLite(cfg.ControlSQLitePath())
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	user, err := auth.NewUserStore(sqlite.DB).Create("admin@example.com", "Admin", "admin")
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	if err := sqlite.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var output bytes.Buffer
	if err := createLoginLink(cfg, " ADMIN@example.com ", &output); err != nil {
		t.Fatalf("createLoginLink: %v", err)
	}
	line := strings.TrimSpace(strings.Split(output.String(), "\n")[1])
	loginURL, err := url.Parse(line)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if loginURL.Scheme != "https" || loginURL.Host != "fanout.example.com" || loginURL.Path != "/login" {
		t.Fatalf("login URL = %s", loginURL)
	}

	sqlite, err = store.NewSQLite(cfg.ControlSQLitePath())
	if err != nil {
		t.Fatalf("reopen SQLite: %v", err)
	}
	defer sqlite.Close()
	email, ok, err := auth.NewCodeStore(sqlite.DB, cfg.AuthCodeSecret).VerifyLoginLink(loginURL.Query().Get("login_token"))
	if err != nil || !ok || email != user.Email {
		t.Fatalf("VerifyLoginLink = (%q, %v, %v), want (%q, true, nil)", email, ok, err, user.Email)
	}
	var events int
	if err := sqlite.DB.QueryRow(`SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'login_link.issued' AND target_id = ?`, user.ID).Scan(&events); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if events != 1 {
		t.Fatalf("issued audit events = %d, want 1", events)
	}
}
