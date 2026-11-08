package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"flag"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/server"
	"github.com/labstack/fanout/internal/store"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestParseCommandLine(t *testing.T) {
	t.Setenv("FANOUT_ADDR", "invalid")
	t.Setenv("FANOUT_TLS_CERT_FILE", "cert.pem")
	t.Setenv("FANOUT_AUTH_CODE_SECRET_FILE", "/missing")
	tests := []struct {
		name        string
		args        []string
		wantVersion bool
		wantLink    *loginLinkCommand
		wantHealth  string
		wantDerived bool
		wantRepair  *repairCommand
		wantErr     bool
	}{
		{name: "server defaults", args: nil},
		{name: "version command", args: []string{"version"}, wantVersion: true},
		{name: "version flag", args: []string{"--version"}, wantVersion: true},
		{name: "short version flag", args: []string{"-v"}, wantVersion: true},
		{name: "login link", args: []string{"login-link", "admin@example.com"}, wantLink: &loginLinkCommand{email: "admin@example.com"}},
		{name: "break-glass login link", args: []string{"login-link", "--break-glass", "admin@example.com"}, wantLink: &loginLinkCommand{email: "admin@example.com", breakGlass: true}},
		{name: "break-glass flag after email", args: []string{"login-link", "admin@example.com", "--break-glass"}, wantLink: &loginLinkCommand{email: "admin@example.com", breakGlass: true}},
		{name: "login link break-glass without email", args: []string{"login-link", "--break-glass"}, wantErr: true},
		{name: "login link unknown flag", args: []string{"login-link", "--force", "admin@example.com"}, wantErr: true},
		{name: "login link two emails", args: []string{"login-link", "a@example.com", "b@example.com"}, wantErr: true},
		{name: "default healthcheck", args: []string{"healthcheck"}, wantDerived: true},
		{name: "custom healthcheck", args: []string{"healthcheck", "http://fanout:8080/healthz"}, wantHealth: "http://fanout:8080/healthz"},
		{name: "repair verify", args: []string{"repair", "verify"}, wantRepair: &repairCommand{action: "verify"}},
		{name: "repair quarantine", args: []string{"repair", "quarantine", "--batch", "broken"}, wantRepair: &repairCommand{action: "quarantine", batch: "broken"}},
		{name: "repair quarantine missing batch", args: []string{"repair", "quarantine"}, wantErr: true},
		{name: "unknown repair action", args: []string{"repair", "delete"}, wantErr: true},
		{name: "login link missing email", args: []string{"login-link"}, wantErr: true},
		{name: "unexpected argument", args: []string{"serve"}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			showVersion, loginLink, healthURL, derivedProbe, repair, err := parseCommandLine(test.args, io.Discard)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
			if showVersion != test.wantVersion || healthURL != test.wantHealth || derivedProbe != test.wantDerived {
				t.Fatalf("result = (%v, %q, %v), want (%v, %q, %v)", showVersion, healthURL, derivedProbe, test.wantVersion, test.wantHealth, test.wantDerived)
			}
			if loginLink == nil != (test.wantLink == nil) || loginLink != nil && *loginLink != *test.wantLink {
				t.Fatalf("login link = %#v, want %#v", loginLink, test.wantLink)
			}
			if repair == nil != (test.wantRepair == nil) || repair != nil && *repair != *test.wantRepair {
				t.Fatalf("repair = %#v, want %#v", repair, test.wantRepair)
			}
		})
	}
}

func TestParseCommandLineHelp(t *testing.T) {
	_, _, _, _, _, err := parseCommandLine([]string{"--help"}, io.Discard)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("error = %v, want flag.ErrHelp", err)
	}
}

func TestParseCommandLinePrintsOneErrorAndUsage(t *testing.T) {
	var output bytes.Buffer
	_, _, _, _, _, err := parseCommandLine([]string{"serve"}, &output)
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
	if !strings.Contains(output.String(), "repair quarantine --batch <id>") {
		t.Fatalf("output does not include repair command: %q", output.String())
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
			if err := checkHealth(server.URL, false, "", "", ""); (err != nil) != test.wantErr {
				t.Fatalf("checkHealth() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestRunRepairVerifiesAndQuarantinesUnreadableBatch(t *testing.T) {
	root := t.TempDir()
	repository, err := telemetrystore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.Commit(context.Background(), telemetrystore.Batch{
		ID: "broken", Spans: []telemetry.Span{{TraceID: "trace", SpanID: "span"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "parquet", "batches", "broken.batch", "trace.fidx"), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := runRepair(root, repairCommand{action: "verify"}, &output); err == nil || !strings.Contains(output.String(), "broken:") {
		t.Fatalf("verify = %v, output %q", err, output.String())
	}
	output.Reset()
	if err := runRepair(root, repairCommand{action: "quarantine", batch: "broken"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "no longer queryable") {
		t.Fatalf("quarantine output = %q", output.String())
	}
	output.Reset()
	if err := runRepair(root, repairCommand{action: "verify"}, &output); err != nil {
		t.Fatal(err)
	}
}

func TestCreateLoginLink(t *testing.T) {
	cfg := config.Config{
		AuthMode:       "local",
		AuthCodeSecret: "0123456789abcdef0123456789abcdef",
		DataDir:        t.TempDir(),
		Addr:           ":7520",
		PublicURL:      "https://fanout.example.com/base",
	}
	if err := os.MkdirAll(cfg.ControlDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sqlite, err := store.NewSQLite(cfg.ControlSQLitePath())
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	user, err := auth.NewUserStore(sqlite.DB).CreateWithAudit("admin@example.com", "Admin", "admin", auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	if err := sqlite.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var output bytes.Buffer
	if err := createLoginLink(cfg, " ADMIN@example.com ", false, &output); err != nil {
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

func breakGlassTestConfig(t *testing.T, role auth.Role, status auth.UserStatus) config.Config {
	t.Helper()
	cfg := config.Config{
		AuthMode:       "oidc",
		AuthCodeSecret: "0123456789abcdef0123456789abcdef",
		DataDir:        t.TempDir(),
		Addr:           ":7520",
		PublicURL:      "https://fanout.example.com",
	}
	if err := os.MkdirAll(cfg.ControlDir(), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	sqlite, err := store.NewSQLite(cfg.ControlSQLitePath())
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	defer sqlite.Close()
	users := auth.NewUserStore(sqlite.DB)
	if _, err := users.CreateWithAudit("keeper@example.com", "", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"}); err != nil {
		t.Fatalf("Create keeper: %v", err)
	}
	user, err := users.CreateWithAudit("user@example.com", "", auth.RoleAdmin, auth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	if _, err := users.UpdateWithAudit(user.ID, nil, nil, &role, &status, auth.AuditEvent{EventType: "user.updated", Outcome: "success"}); err != nil {
		t.Fatalf("Update user: %v", err)
	}
	return cfg
}

func loginLinkState(t *testing.T, cfg config.Config) (links, issued, breakGlassIssued int) {
	t.Helper()
	sqlite, err := store.NewSQLite(cfg.ControlSQLitePath())
	if err != nil {
		t.Fatalf("reopen SQLite: %v", err)
	}
	defer sqlite.Close()
	if err := sqlite.DB.QueryRow(`SELECT COUNT(*) FROM verifications`).Scan(&links); err != nil {
		t.Fatalf("count verifications: %v", err)
	}
	if err := sqlite.DB.QueryRow(`SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'login_link.issued'`).Scan(&issued); err != nil {
		t.Fatalf("count issued: %v", err)
	}
	if err := sqlite.DB.QueryRow(`SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'login_link.break_glass_issued'`).Scan(&breakGlassIssued); err != nil {
		t.Fatalf("count break-glass issued: %v", err)
	}
	return links, issued, breakGlassIssued
}

func TestCreateBreakGlassLoginLinkForActiveAdministrator(t *testing.T) {
	cfg := breakGlassTestConfig(t, auth.RoleAdmin, auth.UserStatusActive)
	var output bytes.Buffer
	if err := createLoginLink(cfg, "user@example.com", true, &output); err != nil {
		t.Fatalf("createLoginLink: %v", err)
	}
	if !strings.Contains(output.String(), "break-glass") {
		t.Fatalf("output = %q, want it to identify a break-glass link", output.String())
	}
	loginURL, err := url.Parse(strings.TrimSpace(strings.Split(output.String(), "\n")[1]))
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	sqlite, err := store.NewSQLite(cfg.ControlSQLitePath())
	if err != nil {
		t.Fatalf("reopen SQLite: %v", err)
	}
	defer sqlite.Close()
	codes := auth.NewCodeStore(sqlite.DB, cfg.AuthCodeSecret)
	if _, ok, err := codes.VerifyLoginLink(loginURL.Query().Get("login_token")); err != nil || ok {
		t.Fatalf("VerifyLoginLink(break-glass) = (%v, %v), want (false, nil)", ok, err)
	}
	if email, ok, err := codes.VerifyBreakGlassLoginLink(loginURL.Query().Get("login_token")); err != nil || !ok || email != "user@example.com" {
		t.Fatalf("VerifyBreakGlassLoginLink = (%q, %v, %v)", email, ok, err)
	}
	var issued, breakGlassIssued int
	if err := sqlite.DB.QueryRow(`SELECT
		COUNT(*) FILTER (WHERE event_type = 'login_link.issued'),
		COUNT(*) FILTER (WHERE event_type = 'login_link.break_glass_issued')
		FROM auth_audit_events`).Scan(&issued, &breakGlassIssued); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if issued != 0 || breakGlassIssued != 1 {
		t.Fatalf("issued = %d, break-glass issued = %d, want 0 and 1", issued, breakGlassIssued)
	}
}

func TestCreateBreakGlassLoginLinkRefusals(t *testing.T) {
	const email = "user@example.com"
	for _, tc := range []struct {
		name    string
		role    auth.Role
		status  auth.UserStatus
		mutate  func(*config.Config)
		email   string
		wantErr string
	}{
		{name: "viewer", role: auth.RoleViewer, status: auth.UserStatusActive, email: email, wantErr: "administrator"},
		{name: "operator", role: auth.RoleOperator, status: auth.UserStatusActive, email: email, wantErr: "administrator"},
		{name: "suspended administrator", role: auth.RoleAdmin, status: auth.UserStatusSuspended, email: email, wantErr: "suspended"},
		{name: "empty secret", role: auth.RoleAdmin, status: auth.UserStatusActive, email: email, mutate: func(c *config.Config) { c.AuthCodeSecret = "" }, wantErr: "FANOUT_AUTH_CODE_SECRET"},
		{name: "short secret", role: auth.RoleAdmin, status: auth.UserStatusActive, email: email, mutate: func(c *config.Config) { c.AuthCodeSecret = "short" }, wantErr: "FANOUT_AUTH_CODE_SECRET"},
		{name: "local mode", role: auth.RoleAdmin, status: auth.UserStatusActive, email: email, mutate: func(c *config.Config) { c.AuthMode = "local" }, wantErr: "oidc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := breakGlassTestConfig(t, tc.role, tc.status)
			if tc.mutate != nil {
				tc.mutate(&cfg)
			}
			var output bytes.Buffer
			err := createLoginLink(cfg, tc.email, true, &output)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
			if output.Len() != 0 {
				t.Fatalf("output = %q, want none", output.String())
			}
			if links, issued, breakGlassIssued := loginLinkState(t, cfg); links != 0 || issued != 0 || breakGlassIssued != 0 {
				t.Fatalf("state = %d links, %d issued, %d break-glass issued; want none", links, issued, breakGlassIssued)
			}
		})
	}
}

func TestCreateLoginLinkWithoutFlagStillRefusesOIDCMode(t *testing.T) {
	cfg := breakGlassTestConfig(t, auth.RoleAdmin, auth.UserStatusActive)
	var output bytes.Buffer
	err := createLoginLink(cfg, "user@example.com", false, &output)
	if err == nil || err.Error() != "login links are available only in local auth mode" {
		t.Fatalf("error = %v", err)
	}
	if links, issued, breakGlassIssued := loginLinkState(t, cfg); links != 0 || issued != 0 || breakGlassIssued != 0 {
		t.Fatalf("state = %d links, %d issued, %d break-glass issued; want none", links, issued, breakGlassIssued)
	}
}

func TestHealthcheckURL(t *testing.T) {
	for _, tc := range []struct{ addr, cert, want string }{
		{"", "", "http://127.0.0.1:7520/healthz"},
		{":8080", "", "http://127.0.0.1:8080/healthz"},
		{"0.0.0.0:8080", "", "http://127.0.0.1:8080/healthz"},
		{"[::]:8080", "", "http://127.0.0.1:8080/healthz"},
		{"[::1]:8080", "", "http://[::1]:8080/healthz"},
		{"[2001:db8::1]:8080", "", "http://[2001:db8::1]:8080/healthz"},
		{"127.0.0.1:9000", "cert.pem", "https://127.0.0.1:9000/healthz"},
		{"localhost:9000", "", "http://localhost:9000/healthz"},
		{"example.com:9000", "", "http://example.com:9000/healthz"},
	} {
		got, err := healthcheckURL(tc.addr, tc.cert)
		if err != nil || got != tc.want {
			t.Fatalf("healthcheckURL(%q) = %q, %v; want %q", tc.addr, got, err, tc.want)
		}
	}
	if _, err := healthcheckURL("invalid", ""); err == nil {
		t.Fatal("expected invalid address error")
	}
}

func TestHealthcheckUsesEnvironmentWithoutValidatingSecrets(t *testing.T) {
	t.Setenv("FANOUT_ADDR", "[::]:9090")
	t.Setenv("FANOUT_TLS_CERT_FILE", "cert.pem")
	t.Setenv("FANOUT_AUTH_CODE_SECRET_FILE", "/missing")
	probe, err := healthcheckURL(os.Getenv("FANOUT_ADDR"), os.Getenv("FANOUT_TLS_CERT_FILE"))
	if err != nil || probe != "https://127.0.0.1:9090/healthz" {
		t.Fatalf("probe = %s, %v", probe, err)
	}
}

func TestCheckHealthWithLocalTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	certFile := writeHealthcheckCertificate(t, server.Certificate().Raw)
	if err := checkHealth(server.URL+"/healthz", true, certFile, "", ""); err != nil {
		t.Fatal(err)
	}
	var verificationError *tls.CertificateVerificationError
	if err := checkHealth(server.URL+"/healthz", false, certFile, "", ""); !errors.As(err, &verificationError) {
		t.Fatalf("explicit URL must verify the certificate, got %v", err)
	}
	other := *server.Certificate()
	other.SerialNumber = big.NewInt(2)
	other.SignatureAlgorithm = x509.UnknownSignatureAlgorithm
	other.PublicKey = nil
	otherCertificate := newHealthcheckCertificate(t, &other)
	otherFile := writeHealthcheckCertificate(t, otherCertificate.Certificate[0])
	// Only the first certificate is trusted, even when the listener's leaf follows it.
	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: otherCertificate.Certificate[0]}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})...)
	if err := os.WriteFile(otherFile, chain, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkHealth(server.URL+"/healthz", true, otherFile, "", ""); !errors.As(err, &verificationError) {
		t.Fatalf("derived IP probe must reject a different certificate, got %v", err)
	}
	probe := strings.Replace(server.URL, "127.0.0.1", "localhost", 1) + "/healthz"
	if err := checkHealth(probe, true, certFile, "", ""); !errors.As(err, &verificationError) {
		t.Fatalf("derived hostname probe must verify the certificate, got %v", err)
	}
}

func writeHealthcheckCertificate(t *testing.T, der []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newHealthcheckCertificate(t *testing.T, certificate *x509.Certificate) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// The derived probe presents FANOUT_HEALTHCHECK_CERT_FILE to a listener that
// requires client certificates, and fails without it.
func TestCheckHealthWithClientCertificate(t *testing.T) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(10), Subject: pkix.Name{CommonName: "healthcheck CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(11), Subject: pkix.Name{CommonName: "fanout healthcheck"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}, ca, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	caFile := filepath.Join(dir, "client-ca.pem")
	clientCertFile := filepath.Join(dir, "healthcheck.pem")
	clientKeyFile := filepath.Join(dir, "healthcheck-key.pem")
	keyDER, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	for path, block := range map[string]*pem.Block{
		caFile:         {Type: "CERTIFICATE", Bytes: caDER},
		clientCertFile: {Type: "CERTIFICATE", Bytes: clientDER},
		clientKeyFile:  {Type: "PRIVATE KEY", Bytes: keyDER},
	} {
		if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	ts := httptest.NewUnstartedServer(nil)
	ts.Config = server.New("", http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" || len(request.TLS.PeerCertificates) == 0 {
			t.Errorf("probe path %q with %d client certificates", request.URL.Path, len(request.TLS.PeerCertificates))
		}
		w.WriteHeader(http.StatusOK)
	}), http.NotFoundHandler(), http.NotFoundHandler())
	clientCAs, err := server.LoadClientCAs(caFile)
	if err != nil {
		t.Fatal(err)
	}
	server.RequireClientCertificates(ts.Config, clientCAs)
	ts.TLS = ts.Config.TLSConfig.Clone()
	ts.TLS.Certificates = []tls.Certificate{newHealthcheckCertificate(t, &x509.Certificate{
		SerialNumber: big.NewInt(12), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})}
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	certFile := writeHealthcheckCertificate(t, ts.Certificate().Raw)

	probe, err := healthcheckURL(ts.Listener.Addr().String(), certFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkHealth(probe, true, certFile, clientCertFile, clientKeyFile); err != nil {
		t.Fatalf("healthcheck with client certificate: %v", err)
	}
	if err := checkHealth(probe, true, certFile, "", ""); err == nil {
		t.Fatal("healthcheck without a client certificate must fail")
	}
	if err := checkHealth(probe, true, certFile, clientCertFile, ""); err == nil || !strings.Contains(err.Error(), "FANOUT_HEALTHCHECK_KEY_FILE") {
		t.Fatalf("partial client pair must name both variables, got %v", err)
	}
	missing := filepath.Join(dir, "missing-key.pem")
	if err := checkHealth(probe, true, certFile, clientCertFile, missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("unreadable client key must name its path, got %v", err)
	}
	keyPEM, err := os.ReadFile(clientKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	// A key passed as the certificate must fail without echoing key material.
	if err := checkHealth(probe, true, certFile, clientKeyFile, clientKeyFile); err == nil || !strings.Contains(err.Error(), clientKeyFile) || strings.Contains(err.Error(), string(keyPEM[40:80])) {
		t.Fatalf("invalid client pair must name its path without contents, got %v", err)
	}
}

func TestCheckHealthCertificateFileErrors(t *testing.T) {
	const contents = "private file contents must not appear in errors"
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "unreadable"},
		{name: "non-certificate", data: []byte(contents)},
		{name: "other PEM block", data: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte(contents)})},
		{name: "invalid certificate", data: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte(contents)})},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cert.pem")
			if test.data != nil {
				if err := os.WriteFile(path, test.data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := checkHealth("https://127.0.0.1:7520/healthz", true, path, "", "")
			if err == nil || !strings.HasPrefix(err.Error(), "healthcheck:") || !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), contents) {
				t.Fatalf("certificate file error must name its path without contents, got %v", err)
			}
		})
	}
}

func TestCheckHealthTLSCertificate(t *testing.T) {
	for _, test := range []struct {
		name       string
		dnsNames   []string
		ips        []net.IP
		expired    bool
		clientOnly bool
		wantErr    bool
	}{
		{name: "IP SAN", ips: []net.IP{net.ParseIP("192.0.2.10")}},
		{name: "DNS before IP SAN", dnsNames: []string{"fanout.example.com", "other.example.com"}, ips: []net.IP{net.ParseIP("192.0.2.10")}},
		{name: "no SAN", wantErr: true},
		{name: "expired", dnsNames: []string{"fanout.example.com"}, expired: true, wantErr: true},
		{name: "client usage", dnsNames: []string{"fanout.example.com"}, clientOnly: true, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			certificate := &x509.Certificate{
				SerialNumber: big.NewInt(1), DNSNames: test.dnsNames, IPAddresses: test.ips,
				NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
				KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			}
			if test.expired {
				certificate.NotAfter = time.Now().Add(-time.Minute)
			}
			if test.clientOnly {
				certificate.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if len(test.dnsNames) > 0 && request.TLS.ServerName != test.dnsNames[0] {
					t.Errorf("TLS server name = %q, want %q", request.TLS.ServerName, test.dnsNames[0])
				}
				w.WriteHeader(http.StatusOK)
			}))
			server.TLS = &tls.Config{Certificates: []tls.Certificate{newHealthcheckCertificate(t, certificate)}}
			server.StartTLS()
			defer server.Close()
			certFile := writeHealthcheckCertificate(t, server.Certificate().Raw)
			err := checkHealth(server.URL+"/healthz", true, certFile, "", "")
			if (err != nil) != test.wantErr {
				t.Fatalf("checkHealth() error = %v, wantErr %v", err, test.wantErr)
			}
			if test.name == "no SAN" && (!strings.HasPrefix(err.Error(), "healthcheck:") || !strings.Contains(err.Error(), certFile)) {
				t.Fatalf("missing SAN error must name the certificate path, got %v", err)
			}
		})
	}
}

func TestCheckHealthWithInterfaceTLS(t *testing.T) {
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{"fanout.example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" {
			t.Errorf("probe path = %q, want /healthz", request.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{newHealthcheckCertificate(t, certificate)}}
	server.StartTLS()
	defer server.Close()
	certFile := writeHealthcheckCertificate(t, server.Certificate().Raw)
	for _, addr := range []string{"192.0.2.10:7520", "[2001:db8::10]:7520", "[fe80::1%eth0]:7520", "fanout.example.com:7520", "wrong.example.com:7520"} {
		t.Run(addr, func(t *testing.T) {
			original := http.DefaultTransport
			transport := original.(*http.Transport).Clone()
			// Route binds to a local test listener to exercise real TLS without
			// requiring those addresses or DNS entries on the test host.
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != addr {
					t.Errorf("probe address = %q, want %q", address, addr)
				}
				var dialer net.Dialer
				return dialer.DialContext(ctx, network, server.Listener.Addr().String())
			}
			http.DefaultTransport = transport
			t.Cleanup(func() {
				http.DefaultTransport = original
				transport.CloseIdleConnections()
			})
			t.Setenv("FANOUT_ADDR", addr)
			t.Setenv("FANOUT_TLS_CERT_FILE", certFile)
			probe, err := healthcheckURL(os.Getenv("FANOUT_ADDR"), os.Getenv("FANOUT_TLS_CERT_FILE"))
			if err != nil {
				t.Fatal(err)
			}
			err = checkHealth(probe, true, certFile, "", "")
			var verificationError *tls.CertificateVerificationError
			if strings.HasSuffix(addr, "example.com:7520") {
				if !errors.As(err, &verificationError) {
					t.Fatalf("derived hostname probe must use system verification, got %v", err)
				}
			} else if err != nil {
				t.Fatalf("derived TLS probe: %v", err)
			}
			_, _, explicitProbe, derivedProbe, _, err := parseCommandLine([]string{"healthcheck", probe}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			err = checkHealth(explicitProbe, derivedProbe, certFile, "", "")
			if !errors.As(err, &verificationError) {
				t.Fatalf("explicit TLS probe must use system verification, got %v", err)
			}
		})
	}
}

func TestCheckHealthDerivedIgnoresEnvironmentProxy(t *testing.T) {
	// ProxyFromEnvironment caches settings on first use, so use a fresh process
	// to ensure earlier tests cannot hide the configured proxies.
	if os.Getenv("FANOUT_TEST_PROXY_CHILD") != "1" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		proxy := "http://" + listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HTTP_PROXY", proxy)
		t.Setenv("HTTPS_PROXY", proxy)
		t.Setenv("NO_PROXY", "")
		t.Setenv("no_proxy", "")
		t.Setenv("REQUEST_METHOD", "")
		t.Setenv("FANOUT_TEST_PROXY_CHILD", "1")
		command := exec.Command(os.Args[0], "-test.run=^TestCheckHealthDerivedIgnoresEnvironmentProxy$", "-test.v")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("proxy regression subprocess: %v\n%s", err, output)
		}
		return
	}
	for _, tc := range []struct{ scheme, addr string }{
		{"http", "192.0.2.10:7520"}, {"https", "192.0.2.10:7520"},
		{"http", "fanout.example.com:7520"}, {"https", "example.com:7520"},
	} {
		t.Run(tc.scheme+"/"+tc.addr, func(t *testing.T) {
			scheme, addr := tc.scheme, tc.addr
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/healthz" {
					t.Errorf("probe path = %q, want /healthz", request.URL.Path)
				}
				w.WriteHeader(http.StatusOK)
			}))
			certFile := ""
			if scheme == "https" {
				server.StartTLS()
				certFile = writeHealthcheckCertificate(t, server.Certificate().Raw)
			} else {
				server.Start()
			}
			defer server.Close()
			original := http.DefaultTransport
			transport := original.(*http.Transport).Clone()
			if scheme == "https" && addr == "example.com:7520" {
				roots := x509.NewCertPool()
				roots.AddCert(server.Certificate())
				transport.TLSClientConfig = &tls.Config{RootCAs: roots}
			}
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if address == addr {
					address = server.Listener.Addr().String()
				} else if "http://"+address != os.Getenv("HTTP_PROXY") {
					return nil, errors.New("unexpected dial address: " + address)
				}
				var dialer net.Dialer
				return dialer.DialContext(ctx, network, address)
			}
			http.DefaultTransport = transport
			t.Cleanup(func() {
				http.DefaultTransport = original
				transport.CloseIdleConnections()
			})
			t.Setenv("FANOUT_ADDR", addr)
			t.Setenv("FANOUT_TLS_CERT_FILE", certFile)
			probe, err := healthcheckURL(os.Getenv("FANOUT_ADDR"), os.Getenv("FANOUT_TLS_CERT_FILE"))
			if err != nil {
				t.Fatal(err)
			}
			if err := checkHealth(probe, true, certFile, "", ""); err != nil {
				t.Fatalf("derived %s probe with unreachable proxy: %v", scheme, err)
			}
			if err := checkHealth(probe, false, certFile, "", ""); err == nil || !strings.Contains(err.Error(), strings.TrimPrefix(os.Getenv("HTTP_PROXY"), "http://")) {
				t.Fatalf("explicit %s probe must use the unreachable proxy, got %v", scheme, err)
			}
		})
	}
}
