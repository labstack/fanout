package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
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
	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/ingest"
	"github.com/labstack/fanout/internal/intelligence"
	"github.com/labstack/fanout/internal/lake"
	"github.com/labstack/fanout/internal/mcp"
	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/settings"
	"github.com/labstack/fanout/internal/store"
	"github.com/labstack/fanout/internal/ui"
)

var tokenRedactRe = regexp.MustCompile(`token=[^&]+`)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-v", "-version", "--version", "version":
			fmt.Println(version)
			return
		}
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	cfg := env.Load()

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		slog.Error("create data dir failed", "err", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(cfg.ControlDir(), 0o755); err != nil {
		slog.Error("create control dir failed", "err", err)
		os.Exit(1)
	}

	// Channels for ingest → lake writer
	chSpans := make(chan lake.SpanRow, 10000)
	chLogs := make(chan lake.LogRow, 10000)
	chMetrics := make(chan lake.MetricRow, 10000)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize query cache with app context (cleanup goroutine stops on cancel)
	query.InitQueryCache(ctx)

	// Error channel for goroutine failures
	errCh := make(chan error, 3)

	// Start DuckDB + rollups
	q, err := query.NewDuck(ctx, cfg)
	if err != nil {
		slog.Error("duckdb init failed", "err", err)
		os.Exit(1)
	}
	defer q.Close()

	// Start Lake Writer. Share the query layer's write lock so appender flushes
	// serialize with rollup/maintenance commits when the pool holds >1 connection.
	writer := lake.NewWriter(cfg, q.DB, chSpans, chLogs, chMetrics)
	writer.UseWriteLock(q.WriteLock())
	go func() {
		if err := writer.Run(ctx); err != nil {
			errCh <- fmt.Errorf("lake writer: %w", err)
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
			time.Duration(cfg.AlertEvalInterval)*time.Second,
			cfg.AlertHistoryDays,
		)
		go alertEngine.Run(ctx)
		slog.Info("alert engine enabled", "interval", cfg.AlertEvalInterval)
	}

	settingsStore := settings.NewStore(sqlite.DB)
	userStore := auth.NewUserStore(sqlite.DB)
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
	ing := ingest.NewServer(cfg, chSpans, chLogs, chMetrics)
	ingest.RegisterOTLP(grpcSrv, ing)

	go func() {
		slog.Info("gRPC OTLP listening", "addr", cfg.OTLPGRPCAddr, "tls", cfg.TLSEnabled())
		if err := grpcSrv.Serve(grpcLis); err != nil && err != grpc.ErrServerStopped {
			errCh <- fmt.Errorf("gRPC server: %w", err)
		}
	}()

	// Start Echo HTTP API
	e := echo.New()
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
			slog.Info("request", "method", v.Method, "uri", uri, "status", v.Status, "latency", v.Latency)
			return nil
		},
	}))

	jwtSecret := cfg.JWTSecret
	refreshSecret := cfg.JWTRefreshSecret
	api.RegisterAuthMiddleware(e, userStore, jwtSecret, cfg.PublicRead)

	// Health checks (liveness + readiness)
	api.RegisterHealthRoutes(e, q, cfg)

	// Prometheus metrics (internal/ops)
	e.GET("/-/metrics", echo.WrapHandler(promhttp.Handler()))

	// pprof for profiling under load (off by default; routes are unauthenticated).
	if cfg.PprofEnabled {
		slog.Warn("pprof enabled at /debug/pprof — do not expose on an untrusted network")
		// Enable mutex/block sampling so those profiles aren't empty — this is how
		// writeMu / channel contention shows up under load. Small runtime cost,
		// acceptable because pprof is opt-in.
		runtime.SetMutexProfileFraction(5)
		runtime.SetBlockProfileRate(10_000) // sample a block event ~every 10µs
		e.GET("/debug/pprof/", echo.WrapHandler(http.HandlerFunc(pprof.Index)))
		e.GET("/debug/pprof/cmdline", echo.WrapHandler(http.HandlerFunc(pprof.Cmdline)))
		e.GET("/debug/pprof/profile", echo.WrapHandler(http.HandlerFunc(pprof.Profile)))
		e.GET("/debug/pprof/symbol", echo.WrapHandler(http.HandlerFunc(pprof.Symbol)))
		e.GET("/debug/pprof/trace", echo.WrapHandler(http.HandlerFunc(pprof.Trace)))
		e.GET("/debug/pprof/:name", echo.WrapHandler(http.HandlerFunc(pprof.Index))) // heap, goroutine, allocs, mutex, block…
	}

	// Fanout owns telemetry semantics; agents and web clients consume this one
	// typed query kernel through deterministic HTTP or standard MCP tools.
	queries := observability.New(q.DB, cfg.DefaultNS)
	api.NewObservabilityHandler(queries).Register(e.Group("/api/observability"))
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
	codeStore := auth.NewCodeStore(sqlite.DB, jwtSecret)
	api.RegisterAuthRoutes(e, userStore, codeStore, setup, settingsStore, jwtSecret, refreshSecret, smtpCfg, cfg)
	api.RegisterUserRoutes(e, userStore, smtpCfg)
	api.RegisterSettingsRoutes(e, cfg, settingsStore)
	slog.Info("auth enabled")

	// The model executes the same standard MCP tools exposed to external clients.
	// The internal connection is in-memory, so the single binary has no HTTP
	// self-call, shared secret, or sidecar runtime.
	mcpServer := mcp.New(queries, dashboards, version)
	if cfg.MCPEnabled {
		mcpAuthorization, err := api.NewMCPAuthorization(
			auth.NewOAuthStore(sqlite.DB), userStore, refreshSecret, cfg.MCPPublicURL,
		)
		if err != nil {
			slog.Error("MCP OAuth init failed", "err", err)
			os.Exit(1)
		}
		mcpAuthorization.Register(e)
		e.Any("/mcp", echo.WrapHandler(mcpAuthorization.ProtectMCP(mcpServer.HTTPHandler())))
		slog.Info("MCP server enabled", "path", "/mcp", "auth", "oauth", "resource", cfg.MCPPublicURL)
	}
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
	agent.NewRuntime(provider, toolRegistry, agent.NewStore(sqlite.DB)).Register(e.Group("/api/agent"))
	slog.Info("AG-UI agent enabled", "path", "/api/agent", "provider", cfg.AIProvider)

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

	// Coordinated shutdown: stop background work, flush ingest, then stop servers.
	cancel()
	writer.Wait()
	grpcSrv.GracefulStop()
	httpCancel() // triggers graceful HTTP shutdown (5s timeout)
}

func printSetupBanner(httpAddr, token string) {
	lines := []string{
		"============================================================",
		" FANOUT SETUP",
		"",
		" Open:  " + setupLoginURL(httpAddr),
		" Token: " + token,
		" Valid: one-time use, expires in 1 hour",
		" Note:  this token disappears after the first admin is created",
		"============================================================",
	}
	fmt.Fprintln(os.Stderr, strings.Join(lines, "\n"))
}

func setupLoginURL(_ string) string {
	return "https://demo.fanout.test/login"
}
