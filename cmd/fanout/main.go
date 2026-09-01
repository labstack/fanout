package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/gzip" // Register gzip decompressor

	"github.com/labstack/fanout/internal/agent"
	"github.com/labstack/fanout/internal/alert"
	"github.com/labstack/fanout/internal/api"
	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/ingest"
	"github.com/labstack/fanout/internal/intelligence"
	"github.com/labstack/fanout/internal/mcp"
	appmetrics "github.com/labstack/fanout/internal/metrics"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/settings"
	"github.com/labstack/fanout/internal/store"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	"github.com/labstack/fanout/internal/ui"
)

var tokenRedactRe = regexp.MustCompile(`token=[^&]+`)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

type repairCommand struct {
	action string
	batch  string
}

func main() {
	configPath, showVersion, loginEmail, healthURL, repair, err := parseCommandLine(os.Args[1:], os.Stderr)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		os.Exit(2)
	}
	if showVersion {
		fmt.Println(version)
		return
	}
	if healthURL != "" {
		if err := checkHealth(healthURL); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	cfg, err := config.Load(config.LoadOptions{Path: configPath})
	if err != nil {
		slog.Error("invalid configuration", "err", err)
		os.Exit(1)
	}
	if repair != nil {
		if err := runRepair(cfg.TelemetryDir(), *repair, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "fanout repair:", err)
			os.Exit(1)
		}
		return
	}
	if loginEmail != "" {
		if err := createLoginLink(cfg, loginEmail, os.Stderr); err != nil {
			slog.Error("create login link failed", "err", err)
			os.Exit(1)
		}
		return
	}
	cfg.LogStartup()

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		slog.Error("create data dir failed", "err", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.ControlDir(), 0o755); err != nil {
		slog.Error("create control dir failed", "err", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize query cache with app context (cleanup goroutine stops on cancel)
	query.InitQueryCache(ctx)

	// Error channel for goroutine failures
	errCh := make(chan error, 4)

	repository, err := telemetrystore.Open(cfg.TelemetryDir())
	if err != nil {
		slog.Error("telemetry store init failed", "err", err)
		os.Exit(1)
	}
	defer repository.Close()

	// DuckDB is the query facade over open Parquet and its indexed trace sidecars.
	q, err := query.NewDuck(ctx, cfg, repository)
	if err != nil {
		slog.Error("duckdb init failed", "err", err)
		os.Exit(1)
	}
	defer q.Close()

	writer := telemetrystore.NewWriter(repository, cfg.IngestBatchSize)
	writerResult := make(chan error, 1)
	go func() {
		err := writer.Run(ctx)
		// Publish the result before notifying the process-wide error channel. The
		// shutdown path waits on writerResult after Writer.Wait, so a final-commit
		// failure cannot be lost in the close(done) -> goroutine-send scheduling gap.
		writerResult <- err
		if err != nil {
			errCh <- fmt.Errorf("telemetry writer: %w", err)
		}
	}()

	go q.RunRollups(ctx)

	// Start intelligence detector
	detector := intelligence.NewDetector(q, intelligence.DefaultDetectorConfig())
	go detector.Run(ctx)

	// Open SQLite for application state
	sqlite, err := store.NewSQLite(cfg.ControlSQLitePath())
	if err != nil {
		slog.Error("sqlite init failed", "err", err)
		os.Exit(1)
	}
	defer sqlite.Close()

	// Start alert engine
	var alertStore *alert.Store
	var alertEngine *alert.Engine
	if cfg.AlertEnabled {
		alertStore = alert.NewStore(sqlite.DB)
		alertEngine = alert.NewEngine(
			alertStore, q, detector,
			cfg.AlertEvaluationInterval,
			cfg.AlertHistoryDays,
		)
		go alertEngine.Run(ctx)
		slog.Info("alert engine enabled", "interval", cfg.AlertEvaluationInterval)
	}

	settingsStore := settings.NewStore(sqlite.DB)
	userStore := auth.NewUserStore(sqlite.DB)
	identityStore := auth.NewIdentityStore(sqlite.DB)
	browserSessions := auth.NewBrowserSessions(sqlite.DB, cfg.SessionIdleTTL, cfg.SessionAbsoluteTTL, cfg.SecureCookies())
	auditStore := auth.NewAuditStore(sqlite.DB)
	setup := auth.NewSetup()

	userCount, err := userStore.CountUsers()
	if err != nil {
		slog.Error("auth status init failed", "err", err)
		os.Exit(1)
	}
	if userCount == 0 {
		token, _, err := setup.Rotate()
		if err != nil {
			slog.Error("setup token init failed", "err", err)
			os.Exit(1)
		}
		printSetupBanner(cfg.HTTPAddr, token)
	}

	// Start gRPC ingest (OTLP)
	grpcLis, err := net.Listen("tcp", cfg.OTLPGRPCAddr)
	if err != nil {
		slog.Error("listen gRPC failed", "err", err)
		os.Exit(1)
	}
	grpcOpts, err := ingest.GRPCServerOptions(cfg, settingsStore)
	if err != nil {
		slog.Error("OTLP gRPC TLS init failed", "err", err)
		os.Exit(1)
	}
	grpcSrv := grpc.NewServer(grpcOpts...)
	ing := ingest.NewServer(cfg, writer)
	ingest.RegisterOTLP(grpcSrv, ing)
	otlpHTTPLis, err := net.Listen("tcp", cfg.OTLPHTTPAddr)
	if err != nil {
		slog.Error("listen OTLP HTTP failed", "err", err)
		os.Exit(1)
	}
	otlpHTTPSrv := &http.Server{
		Handler:           ingest.NewHTTPHandler(ing, settingsStore),
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13},
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	go func() {
		slog.Info("gRPC OTLP listening", "addr", cfg.OTLPGRPCAddr, "tls", cfg.TLSEnabled())
		if err := grpcSrv.Serve(grpcLis); err != nil && err != grpc.ErrServerStopped {
			errCh <- fmt.Errorf("gRPC server: %w", err)
		}
	}()
	go func() {
		slog.Info("HTTP OTLP listening", "addr", cfg.OTLPHTTPAddr, "tls", cfg.TLSEnabled())
		var serveErr error
		if cfg.TLSEnabled() {
			serveErr = otlpHTTPSrv.ServeTLS(otlpHTTPLis, cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			serveErr = otlpHTTPSrv.Serve(otlpHTTPLis)
		}
		if serveErr != nil && serveErr != http.ErrServerClosed {
			errCh <- fmt.Errorf("OTLP HTTP server: %w", serveErr)
		}
	}()

	// Start Echo HTTP API
	e := echo.New()
	if err := api.ConfigureClientIP(e, cfg.TrustedProxyCIDRs); err != nil {
		slog.Error("trusted proxy IP extraction configuration failed", "err", err)
		os.Exit(1)
	}
	if strings.TrimSpace(cfg.TrustedProxyCIDRs) != "" {
		slog.Info("trusted proxy IP extraction enabled", "header", "X-Forwarded-For", "cidrs", cfg.TrustedProxyCIDRs)
	}
	e.Use(middleware.Recover())
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogURI:       true,
		LogStatus:    true,
		LogLatency:   true,
		LogMethod:    true,
		LogRemoteIP:  true,
		LogUserAgent: true,
		HandleError:  true,
		LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
			uri := v.URI
			if strings.Contains(uri, "token=") {
				uri = tokenRedactRe.ReplaceAllString(uri, "token=REDACTED")
			}
			attrs := []any{"method", v.Method, "uri", uri, "status", v.Status, "latency", v.Latency}
			if v.Error != nil {
				attrs = append(attrs, "err", v.Error)
			}
			if v.Status >= 500 {
				slog.Error("request", attrs...)
			} else {
				slog.Info("request", attrs...)
			}
			return nil
		},
	}))

	api.RegisterAuthMiddleware(e, userStore, browserSessions, auditStore, cfg)

	// Health checks (liveness + readiness)
	api.RegisterHealthRoutes(e, q, cfg)

	// Prometheus metrics (internal/ops)
	e.GET("/-/metrics", echo.WrapHandler(promhttp.Handler()))

	// pprof for profiling under load (off by default; admin session required).
	if cfg.PprofEnabled {
		slog.Warn("pprof enabled at /debug/pprof — do not expose on an untrusted network")
		// Enable mutex/block sampling so those profiles aren't empty — this is how
		// write-gate / channel contention shows up under load. Small runtime cost,
		// acceptable because pprof is opt-in.
		runtime.SetMutexProfileFraction(5)
		runtime.SetBlockProfileRate(10_000) // sample a block event ~every 10µs
		operations := api.RequireCapability(api.ReadOperations)
		e.GET("/debug/pprof/", echo.WrapHandler(http.HandlerFunc(pprof.Index)), operations)
		e.GET("/debug/pprof/cmdline", echo.WrapHandler(http.HandlerFunc(pprof.Cmdline)), operations)
		e.GET("/debug/pprof/profile", echo.WrapHandler(http.HandlerFunc(pprof.Profile)), operations)
		e.GET("/debug/pprof/symbol", echo.WrapHandler(http.HandlerFunc(pprof.Symbol)), operations)
		e.GET("/debug/pprof/trace", echo.WrapHandler(http.HandlerFunc(pprof.Trace)), operations)
		e.GET("/debug/pprof/:name", echo.WrapHandler(http.HandlerFunc(pprof.Index)), operations) // heap, goroutine, allocs, mutex, block…
	}

	// Fanout owns telemetry semantics; agents and web clients consume this one
	// typed query kernel through deterministic HTTP or standard MCP tools.
	// Route both HTTP and MCP reads through Duck's retrying adapter. Passing the
	// raw *sql.DB here bypassed the Telemetry maintenance-race protection.
	queries := observability.New(q, q, cfg.RetentionDays)
	api.NewObservabilityHandler(queries).Register(e.Group("/api/observability", api.RequireCapability(api.ReadTelemetry)))
	api.RegisterIntelligenceRoutes(e, detector)
	dashboards := dashboard.New(sqlite.DB)
	api.RegisterDashboardRoutes(e, dashboards)

	// Alert management REST endpoints
	api.RegisterAlertRoutes(e, alertStore, alertEngine)

	// Auth routes (web-only setup + email-code login)
	smtpCfg := auth.SMTPConfig{
		Host: cfg.SMTPHost,
		Port: cfg.SMTPPort,
		User: cfg.SMTPUser,
		Pass: cfg.SMTPPass,
		From: cfg.SMTPFrom,
	}
	codeStore := auth.NewCodeStore(sqlite.DB, cfg.AuthCodeSecret)
	api.RegisterAuthRoutes(e, userStore, codeStore, setup, settingsStore, browserSessions, auditStore, smtpCfg, cfg)
	if err := api.RegisterOIDCRoutes(ctx, e, cfg, userStore, identityStore, browserSessions, auditStore); err != nil {
		slog.Error("OIDC initialization failed", "err", err)
		os.Exit(1)
	}
	api.RegisterUserRoutes(e, userStore, smtpCfg, cfg)
	api.RegisterSettingsRoutes(e, cfg, settingsStore, auditStore)
	slog.Info("auth enabled")

	// Hourly auth-state sweep: expired verification codes, expired/revoked OAuth
	// codes and tokens, and abandoned dynamically-registered OAuth clients.
	// Without it these tables grow without bound (open DCR on /oauth/register).
	oauthStore := auth.NewOAuthStore(sqlite.DB)
	go func() {
		sweep := func() {
			if err := codeStore.Cleanup(); err != nil {
				slog.Error("auth code cleanup failed", "err", err)
			}
			if n, err := oauthStore.CleanupExpired(ctx, time.Now()); err != nil {
				slog.Error("oauth cleanup failed", "err", err)
			} else if n > 0 {
				slog.Info("oauth cleanup", "deleted", n)
			}
			now := time.Now()
			if active, expired, err := browserSessions.CountStatus(ctx, now); err != nil {
				slog.Error("browser session count failed", "err", err)
			} else {
				appmetrics.BrowserSessions.WithLabelValues("active").Set(float64(active))
				appmetrics.BrowserSessions.WithLabelValues("expired").Set(float64(expired))
			}
			if n, err := browserSessions.CleanupExpired(ctx, now); err != nil {
				slog.Error("browser session cleanup failed", "err", err)
			} else if n > 0 {
				slog.Info("browser session cleanup", "deleted", n)
			}
			if n, err := auditStore.Cleanup(ctx, 90*24*time.Hour, time.Now()); err != nil {
				slog.Error("auth audit cleanup failed", "err", err)
			} else if n > 0 {
				slog.Info("auth audit cleanup", "deleted", n)
			}
		}
		sweep()
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	}()

	// The model executes the same standard MCP tools exposed to external clients.
	// The internal connection is in-memory, so the single binary has no HTTP
	// self-call, shared secret, or sidecar runtime.
	mcpServer := mcp.NewWithIntelligence(queries, dashboards, detector, version)
	if cfg.MCPEnabled {
		mcpResourceURL := cfg.MCPResourceURL()
		mcpAuthorization, err := api.NewMCPAuthorization(
			oauthStore, userStore, mcpResourceURL,
		)
		if err != nil {
			slog.Error("MCP OAuth init failed", "err", err)
			os.Exit(1)
		}
		mcpAuthorization.Register(e)
		e.Any("/mcp", echo.WrapHandler(mcpAuthorization.ProtectMCP(mcpServer.HTTPHandler())))
		e.Any("/api/mcp", api.ProtectBrowserMCP(browserSessions, mcpServer.HTTPHandler()))
		slog.Info("MCP server enabled", "path", "/mcp", "auth", "oauth", "resource", mcpResourceURL)
		slog.Info("browser MCP server enabled", "path", "/api/mcp", "auth", "session")
	}
	if cfg.AgentConfigured() {
		provider, err := agent.NewProvider(cfg.AIProvider, cfg.AIAPIKey, cfg.AIModel, cfg.AIBaseURL)
		if err != nil {
			slog.Error("agent provider init failed", "err", err)
			os.Exit(1)
		}
		toolRegistry, err := agent.NewToolRegistry(ctx, mcpServer.MCP())
		if err != nil {
			slog.Error("agent MCP registry init failed", "err", err)
			os.Exit(1)
		}
		defer toolRegistry.Close()
		agent.NewRuntime(provider, toolRegistry, agent.NewStore(sqlite.DB)).Register(e.Group("/api/agent", api.RequireCapability(api.RunAgent)))
		slog.Info("AG-UI agent enabled", "path", "/api/agent", "provider", cfg.AIProvider)
	} else {
		slog.Info("AG-UI agent disabled", "reason", "ai.api_key is not configured")
	}

	// The compiled browser client is an embedded asset, not a second runtime.
	spa := echo.WrapHandler(ui.Handler())
	e.GET("/", spa)
	e.GET("/*", spa)

	// Run HTTP
	httpCtx, httpCancel := context.WithCancel(context.Background())
	go func() {
		sc := echo.StartConfig{
			Address:         cfg.HTTPAddr,
			HideBanner:      true,
			GracefulTimeout: 5 * time.Second,
		}
		slog.Info("HTTP listening", "addr", cfg.HTTPAddr, "tls", cfg.TLSEnabled())
		var err error
		if cfg.TLSEnabled() {
			// Match the OTLP gRPC listener's TLS 1.3 floor; Echo otherwise defaults to 1.2.
			sc.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13}
			err = sc.StartTLS(httpCtx, e, cfg.TLSCertFile, cfg.TLSKeyFile)
		} else {
			err = sc.Start(httpCtx, e)
		}
		if err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("HTTP server: %w", err)
		}
	}()

	// Wait for shutdown signal or fatal goroutine error
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-stop:
		slog.Info("shutting down")
	case err := <-errCh:
		slog.Error("fatal error, shutting down", "err", err)
	}

	// Stop accepting OTLP and let in-flight exporters finish while the writer is
	// still draining. Cancelling the writer first could strand a handler on a full
	// channel or accept rows after its final drain.
	otlpShutdownCtx, otlpShutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := otlpHTTPSrv.Shutdown(otlpShutdownCtx); err != nil {
		slog.Error("OTLP HTTP graceful shutdown failed", "err", err)
		_ = otlpHTTPSrv.Close()
	}
	otlpShutdownCancel()
	grpcSrv.GracefulStop()
	cancel()
	writer.Wait()
	if err := <-writerResult; err != nil {
		slog.Error("telemetry writer stopped with unwritten telemetry", "err", err)
	}
	httpCancel() // triggers graceful HTTP shutdown (5s timeout)
}

func parseCommandLine(args []string, output io.Writer) (configPath string, showVersion bool, loginEmail, healthURL string, repair *repairCommand, err error) {
	if len(args) == 1 && args[0] == "version" {
		return "", true, "", "", nil, nil
	}
	if len(args) >= 1 && len(args) <= 2 && args[0] == "healthcheck" {
		if len(args) == 2 {
			return "", false, "", args[1], nil, nil
		}
		return "", false, "", "http://127.0.0.1:7520/healthz", nil, nil
	}

	flags := flag.NewFlagSet("fanout", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage of fanout:")
		fmt.Fprintln(output, "  fanout [flags]")
		fmt.Fprintln(output, "  fanout [--config path] login-link <email>")
		fmt.Fprintln(output, "  fanout [--config path] repair verify")
		fmt.Fprintln(output, "  fanout [--config path] repair quarantine --batch <id>")
		fmt.Fprintln(output, "  fanout healthcheck [url]")
		fmt.Fprintln(output, "  fanout version")
		fmt.Fprintln(output, "Flags:")
		flags.PrintDefaults()
	}
	flags.StringVar(&configPath, "config", "", "path to a Fanout YAML configuration file")
	flags.BoolVar(&showVersion, "version", false, "print the Fanout version")
	flags.BoolVar(&showVersion, "v", false, "print the Fanout version")
	if err := flags.Parse(args); err != nil {
		return "", false, "", "", nil, err
	}
	if flags.NArg() == 2 && flags.Arg(0) == "login-link" {
		return configPath, false, flags.Arg(1), "", nil, nil
	}
	if flags.NArg() > 0 && flags.Arg(0) == "repair" {
		repair, err := parseRepairCommand(flags.Args()[1:], output)
		return configPath, false, "", "", repair, err
	}
	if flags.NArg() != 0 {
		err := fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
		fmt.Fprintln(output, err)
		flags.Usage()
		return "", false, "", "", nil, err
	}
	return configPath, showVersion, "", "", nil, nil
}

func parseRepairCommand(args []string, output io.Writer) (*repairCommand, error) {
	if len(args) == 1 && args[0] == "verify" {
		return &repairCommand{action: "verify"}, nil
	}
	if len(args) == 0 || args[0] != "quarantine" {
		return nil, errors.New("repair requires 'verify' or 'quarantine --batch <id>'")
	}
	flags := flag.NewFlagSet("fanout repair quarantine", flag.ContinueOnError)
	flags.SetOutput(output)
	batch := flags.String("batch", "", "unreadable authoritative batch ID to set aside")
	if err := flags.Parse(args[1:]); err != nil {
		return nil, err
	}
	if strings.TrimSpace(*batch) == "" || flags.NArg() != 0 {
		return nil, errors.New("repair quarantine requires exactly --batch <id>")
	}
	return &repairCommand{action: "quarantine", batch: *batch}, nil
}

func runRepair(root string, command repairCommand, output io.Writer) error {
	switch command.action {
	case "verify":
		issues, err := telemetrystore.VerifyBatches(root)
		if err != nil {
			return err
		}
		if len(issues) == 0 {
			fmt.Fprintln(output, "all authoritative telemetry batches passed validation")
			return nil
		}
		for _, issue := range issues {
			fmt.Fprintf(output, "%s: %v\n", issue.ID, issue.Err)
		}
		return fmt.Errorf("%d unreadable authoritative telemetry batch(es)", len(issues))
	case "quarantine":
		destination, err := telemetrystore.QuarantineBatch(root, command.batch)
		if destination != "" {
			fmt.Fprintf(output, "batch %s set aside at %s\n", command.batch, destination)
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(output, "the quarantined telemetry is no longer queryable; preserve it for recovery from backup")
		return nil
	default:
		return fmt.Errorf("unknown repair action %q", command.action)
	}
}

func checkHealth(healthURL string) error {
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(healthURL)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: %s returned %s", healthURL, response.Status)
	}
	return nil
}

func createLoginLink(cfg config.Config, rawEmail string, output io.Writer) error {
	if strings.ToLower(strings.TrimSpace(cfg.AuthMode)) != "local" {
		return fmt.Errorf("login links are available only in local auth mode")
	}
	email, err := auth.NormalizeEmail(rawEmail)
	if err != nil {
		return err
	}
	if _, err := os.Stat(cfg.ControlSQLitePath()); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("control database does not exist at %s", cfg.ControlSQLitePath())
		}
		return fmt.Errorf("inspect control database: %w", err)
	}
	sqlite, err := store.NewSQLite(cfg.ControlSQLitePath())
	if err != nil {
		return err
	}
	defer sqlite.Close()
	user, err := auth.NewUserStore(sqlite.DB).GetByEmail(email)
	if err != nil {
		return err
	}
	if !user.Active {
		return fmt.Errorf("user %s is inactive", email)
	}
	token, err := auth.NewCodeStore(sqlite.DB, cfg.AuthCodeSecret).CreateLoginLink(email)
	if err != nil {
		return err
	}
	if err := auth.NewAuditStore(sqlite.DB).Record(context.Background(), auth.AuditEvent{
		EventType:  "login_link.issued",
		Outcome:    "success",
		TargetType: "user",
		TargetID:   user.ID,
	}); err != nil {
		return err
	}
	loginURL := browserLoginURL(cfg)
	query := loginURL.Query()
	query.Set("login_token", token)
	loginURL.RawQuery = query.Encode()
	fmt.Fprintf(output, "fanout: login link for %s (valid 15 minutes, single use)\n  %s\n", email, loginURL.String())
	return nil
}

func browserLoginURL(cfg config.Config) *url.URL {
	if publicURL, err := url.Parse(strings.TrimSpace(cfg.PublicURL)); err == nil && publicURL.Scheme != "" && publicURL.Host != "" {
		publicURL.Path = "/login"
		publicURL.RawQuery = ""
		publicURL.Fragment = ""
		return publicURL
	}
	loginURL, _ := url.Parse(setupLoginURL(cfg.HTTPAddr))
	return loginURL
}

func printSetupBanner(httpAddr, token string) {
	lines := []string{
		"============================================================",
		" FANOUT SETUP",
		"",
		" Open:  " + setupLoginURL(httpAddr) + "?setup_token=" + url.QueryEscape(token),
		" Valid: one-time use, expires in 1 hour",
		" Note:  this URL disappears after the first admin is created",
		" Warn:  it contains an administrator credential — this output may",
		"        persist in container logs, log aggregators, and scrollback",
		"============================================================",
	}
	fmt.Fprintln(os.Stderr, strings.Join(lines, "\n"))
}

func setupLoginURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://127.0.0.1" + addr + "/login"
	}
	host, port, err := net.SplitHostPort(addr)
	if err == nil {
		switch host {
		case "", "0.0.0.0", "::":
			host = "127.0.0.1"
		}
		if strings.Contains(host, ":") {
			host = "[" + strings.Trim(host, "[]") + "]"
		}
		return "http://" + host + ":" + port + "/login"
	}
	return "http://" + addr + "/login"
}
