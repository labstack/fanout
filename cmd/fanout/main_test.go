package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
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
	"github.com/labstack/fanout/internal/store"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

func TestParseCommandLine(t *testing.T) {
	t.Setenv("FANOUT_ADDR", "")
	t.Setenv("FANOUT_TLS_CERT_FILE", "")
	t.Setenv("FANOUT_AUTH_CODE_SECRET", "")
	tests := []struct {
		name        string
		args        []string
		wantVersion bool
		wantEmail   string
		wantHealth  string
		wantRepair  *repairCommand
		wantErr     bool
	}{
		{name: "server defaults", args: nil},
		{name: "version command", args: []string{"version"}, wantVersion: true},
		{name: "version flag", args: []string{"--version"}, wantVersion: true},
		{name: "short version flag", args: []string{"-v"}, wantVersion: true},
		{name: "login link", args: []string{"login-link", "admin@example.com"}, wantEmail: "admin@example.com"},
		{name: "default healthcheck", args: []string{"healthcheck"}, wantHealth: "http://127.0.0.1:7520/healthz"},
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
			showVersion, email, healthURL, repair, err := parseCommandLine(test.args, io.Discard)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
			if showVersion != test.wantVersion || email != test.wantEmail || healthURL != test.wantHealth {
				t.Fatalf("result = (%v, %q, %q), want (%v, %q, %q)", showVersion, email, healthURL, test.wantVersion, test.wantEmail, test.wantHealth)
			}
			if repair == nil != (test.wantRepair == nil) || repair != nil && *repair != *test.wantRepair {
				t.Fatalf("repair = %#v, want %#v", repair, test.wantRepair)
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
			if err := checkHealth(server.URL, false); (err != nil) != test.wantErr {
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

func TestHealthcheckURL(t *testing.T) {
	for _, tc := range []struct{ addr, cert, want string }{
		{"", "", "http://127.0.0.1:7520/healthz"},
		{":8080", "", "http://127.0.0.1:8080/healthz"},
		{"0.0.0.0:8080", "", "http://127.0.0.1:8080/healthz"},
		{"[::]:8080", "", "http://[::1]:8080/healthz"},
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
	_, _, probe, _, err := parseCommandLine([]string{"healthcheck"}, io.Discard)
	if err != nil || probe != "https://[::1]:9090/healthz" {
		t.Fatalf("probe = %s, %v", probe, err)
	}
}

func TestCheckHealthWithLocalTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	if err := checkHealth(server.URL+"/healthz", true); err != nil {
		t.Fatal(err)
	}
	if err := checkHealth(server.URL+"/healthz", false); err == nil {
		t.Fatal("explicit URL must verify the certificate")
	}
}

func TestCheckHealthWithInterfaceTLS(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{"fanout.example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" {
			t.Errorf("probe path = %q, want /healthz", request.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	for _, addr := range []string{"192.0.2.10:7520", "[2001:db8::10]:7520"} {
		t.Run(addr, func(t *testing.T) {
			original := http.DefaultTransport
			transport := original.(*http.Transport).Clone()
			transport.TLSClientConfig = &tls.Config{RootCAs: roots}
			// Route the interface bind to a local test listener so both IP families
			// exercise real TLS without requiring those addresses on the test host.
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
			t.Setenv("FANOUT_TLS_CERT_FILE", "cert.pem")
			_, _, probe, _, err := parseCommandLine([]string{"healthcheck"}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkHealth(probe, true); err != nil {
				t.Fatalf("derived TLS probe: %v", err)
			}
			_, _, explicitProbe, _, err := parseCommandLine([]string{"healthcheck", probe}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			var hostnameError x509.HostnameError
			if err := checkHealth(explicitProbe, false); !errors.As(err, &hostnameError) {
				t.Fatalf("explicit TLS probe must verify the hostname, got %v", err)
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
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/healthz" {
					t.Errorf("probe path = %q, want /healthz", request.URL.Path)
				}
				w.WriteHeader(http.StatusOK)
			}))
			certFile := ""
			if scheme == "https" {
				server.StartTLS()
				certFile = "cert.pem"
			} else {
				server.Start()
			}
			defer server.Close()
			const addr = "192.0.2.10:7520"
			original := http.DefaultTransport
			transport := original.(*http.Transport).Clone()
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
			_, _, probe, _, err := parseCommandLine([]string{"healthcheck"}, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if err := checkHealth(probe, true); err != nil {
				t.Fatalf("derived %s probe with unreachable proxy: %v", scheme, err)
			}
			if err := checkHealth(probe, false); err == nil || !strings.Contains(err.Error(), strings.TrimPrefix(os.Getenv("HTTP_PROXY"), "http://")) {
				t.Fatalf("explicit %s probe must use the unreachable proxy, got %v", scheme, err)
			}
		})
	}
}
