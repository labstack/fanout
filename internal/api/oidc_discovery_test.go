package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/labstack/echo/v5"

	appauth "github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/engine"
	appstore "github.com/labstack/fanout/internal/store"
)

var fastOIDCDiscovery = oidcDiscoveryTiming{attempt: 5 * time.Second, initial: 5 * time.Millisecond, max: 20 * time.Millisecond}

// oidcOutageBody stands in for whatever an identity provider or its proxy
// returns during an outage; none of it may reach the public readiness response.
const oidcOutageBody = "upstream-internal-detail"

// flakyIdP fails discovery until up is set, counting discovery requests.
type flakyIdP struct {
	*httptest.Server
	up       atomic.Bool
	requests atomic.Int64
}

func newFlakyIdP(t *testing.T) *flakyIdP {
	t.Helper()
	idp := &flakyIdP{}
	idp.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		idp.requests.Add(1)
		if !idp.up.Load() {
			http.Error(w, oidcOutageBody, http.StatusServiceUnavailable)
			return
		}
		writeOIDCDiscoveryDocument(w, idp.URL)
	}))
	t.Cleanup(idp.Close)
	return idp
}

func writeOIDCDiscoveryDocument(w http.ResponseWriter, issuer string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer": issuer, "authorization_endpoint": issuer + "/authorize", "token_endpoint": issuer + "/token",
		"jwks_uri": issuer + "/keys", "response_types_supported": []string{"code"},
		"subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

type oidcDiscoveryFixture struct {
	echo     *echo.Echo
	cfg      config.Config
	users    *appauth.UserStore
	sessions *appauth.BrowserSessions
	audit    *appauth.AuditStore
	register func(context.Context, oidcDiscoveryTiming) (*OIDCDiscovery, error)
}

func newOIDCDiscoveryFixture(t *testing.T, issuer string) *oidcDiscoveryFixture {
	t.Helper()
	db, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &oidcDiscoveryFixture{
		echo: echo.New(),
		cfg: config.Config{
			AuthMode: "oidc", OIDCIssuerURL: issuer, OIDCClientID: "fanout-client", OIDCClientSecret: "client-secret",
			PublicURL: "https://fanout.example.com", OIDCEmailVerification: "required", OIDCDefaultRole: "viewer",
			DataDir: os.TempDir(),
		},
		users:    appauth.NewUserStore(db.DB),
		sessions: appauth.NewBrowserSessions(db.DB, 12*time.Hour, 7*24*time.Hour, false),
		audit:    appauth.NewAuditStore(db.DB),
	}
	f.echo.HTTPErrorHandler = HTTPErrorHandler
	RegisterAuthMiddleware(f.echo, f.users, f.sessions, f.audit, f.cfg)
	identities := appauth.NewIdentityStore(db.DB)
	f.register = func(ctx context.Context, timing oidcDiscoveryTiming) (*OIDCDiscovery, error) {
		return registerOIDCRoutes(ctx, f.echo, f.cfg, f.users, identities, f.sessions, f.audit, timing)
	}
	return f
}

func (f *oidcDiscoveryFixture) get(target string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	f.echo.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return recorder
}

func assertOIDCUnavailable(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After = %q, want 60", got)
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || body.Code != "unavailable" {
		t.Fatalf("body = %s, err %v, want code unavailable", recorder.Body.String(), err)
	}
}

func readinessWithHealthyEngine(t *testing.T, discovery *OIDCDiscovery, cfg config.Config) (*httptest.ResponseRecorder, HealthResponse) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.MatchExpectationsInOrder(false)
	mock.ExpectQuery("SELECT 1$").WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectQuery("SELECT 1 FROM telemetry.spans LIMIT 1").WillReturnRows(sqlmock.NewRows([]string{"1"}))
	mock.ExpectQuery("MAX\\(updated_at\\)").
		WillReturnRows(sqlmock.NewRows([]string{"max", "count", "age_seconds"}).AddRow(time.Now().UTC(), 2, int64(30)))

	cfg.RollupInterval = time.Minute
	e := echo.New()
	RegisterHealthRoutes(e, &engine.Duck{DB: db}, cfg, discovery)
	recorder := httptest.NewRecorder()
	e.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	var resp HealthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal readiness: %v", err)
	}
	return recorder, resp
}

func waitForOIDCDiscoveryExit(t *testing.T, discovery *OIDCDiscovery) {
	t.Helper()
	select {
	case <-discovery.done:
	case <-time.After(5 * time.Second):
		t.Fatal("discovery retry loop did not stop")
	}
}

func TestOIDCDiscoveryRecoversWithoutRestart(t *testing.T) {
	idp := newFlakyIdP(t)
	fixture := newOIDCDiscoveryFixture(t, idp.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	discovery, err := fixture.register(ctx, fastOIDCDiscovery)
	if err != nil {
		t.Fatalf("startup failed while the provider was down: %v", err)
	}
	assertOIDCUnavailable(t, fixture.get("/api/auth/oidc/start"))
	assertOIDCUnavailable(t, fixture.get("/api/auth/oidc/callback?code=c&state=s"))

	recorder, resp := readinessWithHealthyEngine(t, discovery, fixture.cfg)
	if recorder.Code != http.StatusOK {
		t.Fatalf("/readyz status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if check := resp.Checks["oidc"]; check.Status != "degraded" || check.Detail == "" {
		t.Fatalf("oidc check = %+v, want degraded with detail", check)
	}
	if strings.Contains(recorder.Body.String(), oidcOutageBody) || strings.Contains(recorder.Body.String(), idp.URL) {
		t.Fatalf("public readiness exposes discovery failure: %s", recorder.Body.String())
	}

	idp.up.Store(true)
	var redirect *httptest.ResponseRecorder
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if redirect = fixture.get("/api/auth/oidc/start"); redirect.Code != http.StatusServiceUnavailable {
			break
		}
	}
	if redirect.Code != http.StatusFound {
		t.Fatalf("OIDC start after recovery = %d, want 302: %s", redirect.Code, redirect.Body.String())
	}
	location, err := url.Parse(redirect.Header().Get("Location"))
	if err != nil || !strings.HasPrefix(location.String(), idp.URL+"/authorize") || location.Query().Get("code_challenge") == "" {
		t.Fatalf("authorization redirect = %q, err %v", redirect.Header().Get("Location"), err)
	}

	waitForOIDCDiscoveryExit(t, discovery)
	if _, resp = readinessWithHealthyEngine(t, discovery, fixture.cfg); resp.Checks["oidc"].Status != "ok" {
		t.Fatalf("oidc check after recovery = %+v, want ok", resp.Checks["oidc"])
	}
}

func TestOIDCDiscoveryOutageDoesNotConsumeSignInBudget(t *testing.T) {
	idp := newFlakyIdP(t)
	fixture := newOIDCDiscoveryFixture(t, idp.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	discovery, err := fixture.register(ctx, fastOIDCDiscovery)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	for range 40 {
		assertOIDCUnavailable(t, fixture.get("/api/auth/oidc/start"))
	}
	idp.up.Store(true)
	waitForOIDCDiscoveryExit(t, discovery)
	if got := fixture.get("/api/auth/oidc/start"); got.Code != http.StatusFound {
		t.Fatalf("OIDC start after outage = %d, want 302: %s", got.Code, got.Body.String())
	}
}

func TestOIDCDiscoverySuccessAtStartupServesImmediately(t *testing.T) {
	idp := newFlakyIdP(t)
	idp.up.Store(true)
	fixture := newOIDCDiscoveryFixture(t, idp.URL)

	discovery, err := fixture.register(context.Background(), fastOIDCDiscovery)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if got := fixture.get("/api/auth/oidc/start"); got.Code != http.StatusFound {
		t.Fatalf("OIDC start = %d, want 302: %s", got.Code, got.Body.String())
	}
	waitForOIDCDiscoveryExit(t, discovery)
	if got := idp.requests.Load(); got != 1 {
		t.Fatalf("discovery requests = %d, want 1", got)
	}
}

func TestOIDCDiscoveryRetriesStopAtShutdown(t *testing.T) {
	idp := newFlakyIdP(t)
	fixture := newOIDCDiscoveryFixture(t, idp.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	discovery, err := fixture.register(ctx, fastOIDCDiscovery)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); idp.requests.Load() < 3; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("discovery requests = %d, want retries", idp.requests.Load())
		}
	}
	cancel()
	waitForOIDCDiscoveryExit(t, discovery)
	stopped := idp.requests.Load()
	time.Sleep(10 * fastOIDCDiscovery.max)
	if got := idp.requests.Load(); got != stopped {
		t.Fatalf("discovery requests after shutdown = %d, want %d", got, stopped)
	}
}

func TestOIDCDiscoveryShutdownCancelsInFlightAttempt(t *testing.T) {
	release := make(chan struct{})
	var requests atomic.Int64
	hanging := make(chan struct{})
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		close(hanging)
		<-release
	}))
	defer idp.Close()
	defer close(release)
	fixture := newOIDCDiscoveryFixture(t, idp.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	timing := fastOIDCDiscovery
	timing.attempt = time.Minute
	discovery, err := fixture.register(ctx, timing)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	select {
	case <-hanging:
	case <-time.After(5 * time.Second):
		t.Fatal("retry never reached the provider")
	}
	cancel()
	waitForOIDCDiscoveryExit(t, discovery)
}

func TestOIDCDiscoveryStartupAttemptIsBounded(t *testing.T) {
	release := make(chan struct{})
	idp := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer idp.Close()
	defer close(release)
	fixture := newOIDCDiscoveryFixture(t, idp.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	timing := fastOIDCDiscovery
	timing.attempt = 50 * time.Millisecond
	finished := make(chan error, 1)
	go func() {
		_, err := fixture.register(ctx, timing)
		finished <- err
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("hanging provider failed startup: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup blocked on a hanging provider")
	}
	assertOIDCUnavailable(t, fixture.get("/api/auth/oidc/start"))
}

func TestOIDCExistingSessionServedDuringDiscoveryOutage(t *testing.T) {
	idp := newFlakyIdP(t)
	fixture := newOIDCDiscoveryFixture(t, idp.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	user, err := fixture.users.CreateWithAudit("admin@example.com", "", "admin", appauth.AuditEvent{EventType: "user.created", Outcome: "success"})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	cookie := authenticatedSessionCookie(t, fixture.sessions, user)
	fixture.echo.GET("/api/auth/me", func(c *echo.Context) error { return c.NoContent(http.StatusNoContent) })
	if _, err := fixture.register(ctx, fastOIDCDiscovery); err != nil {
		t.Fatalf("register: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	fixture.echo.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("existing session = %d, want 204: %s", recorder.Code, recorder.Body.String())
	}
}

func TestOIDCInvalidConfigurationFailsStartup(t *testing.T) {
	wrongIssuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeOIDCDiscoveryDocument(w, "https://other.example.com")
	}))
	defer wrongIssuer.Close()

	for name, issuer := range map[string]string{
		"issuer mismatch":    wrongIssuer.URL,
		"unparseable issuer": " https://idp.example.com",
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newOIDCDiscoveryFixture(t, issuer)
			discovery, err := fixture.register(context.Background(), fastOIDCDiscovery)
			if err == nil {
				t.Fatal("invalid OIDC configuration started")
			}
			if discovery != nil {
				t.Fatal("failed startup returned a discovery")
			}
			if got := fixture.get("/api/auth/oidc/start"); got.Code != http.StatusNotFound {
				t.Fatalf("OIDC start = %d, want unregistered", got.Code)
			}
		})
	}

	t.Run("missing dependencies", func(t *testing.T) {
		cfg := config.Config{AuthMode: "oidc", OIDCIssuerURL: wrongIssuer.URL}
		if _, err := RegisterOIDCRoutes(context.Background(), echo.New(), cfg, nil, nil, nil, nil); err == nil {
			t.Fatal("missing dependencies started")
		}
	})
}

func TestOIDCDiscoveryLocalModeRegistersNothing(t *testing.T) {
	e := echo.New()
	discovery, err := RegisterOIDCRoutes(context.Background(), e, config.Config{AuthMode: "local"}, nil, nil, nil, nil)
	if err != nil || discovery != nil {
		t.Fatalf("local mode = %v, %v; want nil, nil", discovery, err)
	}
	_, resp := readinessWithHealthyEngine(t, nil, config.Config{DataDir: os.TempDir()})
	if _, reported := resp.Checks["oidc"]; reported {
		t.Fatalf("local mode reports an oidc check: %+v", resp.Checks["oidc"])
	}
}

func TestOIDCDiscoveryBackoffIsBounded(t *testing.T) {
	want := []time.Duration{2, 4, 8, 16, 32, 60, 60, 60}
	delay := oidcDiscoveryInitialBackoff
	for i, seconds := range want {
		delay = nextOIDCDiscoveryBackoff(delay, oidcDiscoveryMaxBackoff)
		if delay != seconds*time.Second {
			t.Fatalf("backoff %d = %v, want %v", i, delay, seconds*time.Second)
		}
	}
}

type lockedLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestOIDCDiscoveryLogsTruncateProviderBody(t *testing.T) {
	logs := &lockedLogBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	body := strings.Repeat("x", 1<<20)
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, body, http.StatusServiceUnavailable)
	}))
	defer idp.Close()
	fixture := newOIDCDiscoveryFixture(t, idp.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	discovery, err := fixture.register(ctx, fastOIDCDiscovery)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(logs.String(), "sign-in stays unavailable"); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("retry failure was never logged")
		}
	}
	cancel()
	waitForOIDCDiscoveryExit(t, discovery)

	var failures int
	for line := range strings.SplitSeq(logs.String(), "\n") {
		if !strings.Contains(line, "OIDC discovery failed") {
			continue
		}
		failures++
		if !strings.Contains(line, "503 Service Unavailable") {
			t.Fatalf("log lost the failure status: %.400s", line)
		}
		if len(line) > 1024 {
			t.Fatalf("log line is %d bytes, want the provider body truncated", len(line))
		}
	}
	if failures < 2 {
		t.Fatalf("logged failures = %d, want the startup and retry sites", failures)
	}
}

func TestOIDCTruncatedDiscoveryErrorKeepsValidUTF8(t *testing.T) {
	got := truncatedOIDCDiscoveryError(errors.New(strings.Repeat("é", 400)))
	if !utf8.ValidString(got) || len(got) > oidcDiscoveryLogErrorLimit+len("...") {
		t.Fatalf("truncated error is %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
	if short := truncatedOIDCDiscoveryError(errors.New("short")); short != "short" {
		t.Fatalf("short error = %q", short)
	}
}

func TestOIDCDiscoveryWrongIssuerOnRetryStaysUnavailable(t *testing.T) {
	var wrongIssuer atomic.Bool
	var wrongAnswers atomic.Int64
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !wrongIssuer.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		wrongAnswers.Add(1)
		writeOIDCDiscoveryDocument(w, "https://other.example.com")
	}))
	defer idp.Close()
	fixture := newOIDCDiscoveryFixture(t, idp.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	discovery, err := fixture.register(ctx, fastOIDCDiscovery)
	if err != nil {
		t.Fatalf("startup failed while the provider was down: %v", err)
	}
	wrongIssuer.Store(true)
	for deadline := time.Now().Add(5 * time.Second); wrongAnswers.Load() < 3; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("wrong-issuer answers = %d, want retries to continue", wrongAnswers.Load())
		}
	}

	assertOIDCUnavailable(t, fixture.get("/api/auth/oidc/start"))
	assertOIDCUnavailable(t, fixture.get("/api/auth/oidc/callback?code=c&state=s"))
	recorder, resp := readinessWithHealthyEngine(t, discovery, fixture.cfg)
	if recorder.Code != http.StatusOK {
		t.Fatalf("/readyz status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if check := resp.Checks["oidc"]; check.Status != "degraded" {
		t.Fatalf("oidc check = %+v, want degraded", check)
	}
	select {
	case <-discovery.done:
		t.Fatal("retry loop stopped on a wrong issuer")
	default:
	}
}
