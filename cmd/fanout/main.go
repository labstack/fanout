package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/gzip" // Register gzip decompressor

	"github.com/labstack/fanout/internal/ai"
	"github.com/labstack/fanout/internal/alert"
	"github.com/labstack/fanout/internal/api"
	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/ingest"
	"github.com/labstack/fanout/internal/intelligence"
	"github.com/labstack/fanout/internal/lake"
	"github.com/labstack/fanout/internal/mcp"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/service"
	"github.com/labstack/fanout/internal/store"
	"github.com/labstack/fanout/internal/web"
)

var tokenRedactRe = regexp.MustCompile(`token=[^&]+`)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))

	cfg := config.Load()

	if err := os.MkdirAll(cfg.LakeDir, 0o755); err != nil {
		slog.Error("create lake dir failed", "err", err)
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

	// Start Lake Writer
	writer := lake.NewWriter(cfg, q.DB, chSpans, chLogs, chMetrics)
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
	sqlite, err := store.NewSQLite(filepath.Join(cfg.LakeDir, "fanout.sqlite"))
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

	// Start gRPC ingest (OTLP)
	grpcLis, err := net.Listen("tcp", cfg.OTLPGRPCAddr)
	if err != nil {
		slog.Error("listen gRPC failed", "err", err)
		os.Exit(1)
	}
	grpcSrv := grpc.NewServer()
	ing := ingest.NewServer(cfg, chSpans, chLogs, chMetrics)
	ingest.RegisterOTLP(grpcSrv, ing)

	go func() {
		slog.Info("gRPC OTLP listening", "addr", cfg.OTLPGRPCAddr)
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

	// Resolve JWT secret (generate ephemeral if not set)
	jwtSecret := cfg.JWTSecret
	if jwtSecret == "" && cfg.AuthEnabled() {
		jwtSecret = auth.GenerateSecret()
		slog.Warn("JWT_SECRET not set — generated ephemeral secret (sessions won't survive restart)")
	}

	// Create user store early so the middleware can look up API keys
	var userStore *auth.UserStore
	if cfg.AuthEnabled() {
		userStore = auth.NewUserStore(sqlite.DB)
	}

	// Auth middleware — supports JWT, per-user API keys, and legacy API_TOKEN
	apiToken := strings.TrimSpace(cfg.APIToken)
	if apiToken != "" || cfg.AuthEnabled() {
		tokenBytes := []byte(apiToken)
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c *echo.Context) error {
				path := c.Request().URL.Path

				// Skip auth for public endpoints. /api/auth/* is exempt EXCEPT /api/auth/me.
				isPublicAuth := strings.HasPrefix(path, "/api/auth/") && path != "/api/auth/me"
				if path == "/healthz" || path == "/readyz" || path == "/api/health" || path == "/-/metrics" ||
					path == "/favicon.ico" || path == "/favicon.svg" ||
					isPublicAuth ||
					(!strings.HasPrefix(path, "/api/") && path != "/mcp") {
					return next(c)
				}

				authHeader := c.Request().Header.Get("Authorization")
				if !strings.HasPrefix(authHeader, "Bearer ") {
					return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
				}
				bearer := strings.TrimPrefix(authHeader, "Bearer ")

				// Try legacy API_TOKEN (constant-time compare)
				if apiToken != "" && subtle.ConstantTimeCompare([]byte(bearer), tokenBytes) == 1 {
					return next(c)
				}

				// Try JWT access token
				if jwtSecret != "" {
					claims, err := auth.VerifyAccess(jwtSecret, bearer)
					if err == nil {
						c.Set("auth_claims", claims)
						return next(c)
					}
				}

				// Try per-user API key (fo_...)
				if userStore != nil && strings.HasPrefix(bearer, "fo_") {
					user, err := userStore.GetByAPIKey(bearer)
					if err == nil && user.Active {
						c.Set("auth_claims", &auth.Claims{
							Email: user.Email,
							Role:  user.Role,
						})
						return next(c)
					}
				}

				return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
			}
		})
	}

	// Health checks (liveness + readiness)
	api.RegisterHealthRoutes(e, q, cfg)

	// Prometheus metrics (internal/ops)
	e.GET("/-/metrics", echo.WrapHandler(promhttp.Handler()))

	// Create shared service layer
	svc := service.New(q, cfg)

	// Create MCP server unconditionally (AI orchestrator connects to it in-process)
	mcpServer := mcp.NewServer(svc, q, cfg, alertEngine)
	go mcp.RunCleanup(ctx)

	// AI orchestrator (optional — needs API key)
	var orch *ai.Orchestrator
	var sseHandler *ai.SSEHandler
	var aiTools *ai.ToolRegistry

	if cfg.AIAPIKey != "" {
		var provider ai.Provider
		switch cfg.AIProvider {
		case "openai":
			provider = ai.NewOpenAIProvider(cfg.AIAPIKey, cfg.AIModel, cfg.AIBaseURL)
			slog.Info("AI provider: OpenAI", "model", cfg.AIModel)
		case "anthropic", "":
			provider = ai.NewAnthropicProvider(cfg.AIAPIKey, cfg.AIModel, cfg.AIBaseURL)
			slog.Info("AI provider: Anthropic", "model", cfg.AIModel)
		default:
			slog.Error("unsupported AI_PROVIDER", "value", cfg.AIProvider, "supported", "anthropic, openai")
			os.Exit(1)
		}

		var err error
		aiTools, err = ai.NewToolRegistry(ctx, mcpServer.MCP(), svc, cfg)
		if err != nil {
			slog.Error("AI tool registry init failed", "err", err)
			os.Exit(1)
		}
		orch = ai.NewOrchestrator(provider, aiTools, svc, cfg)
		sseHandler = ai.NewSSEHandler(ctx, orch)
		if cfg.APIToken == "" {
			slog.Warn("AI chat enabled without API_TOKEN — chat endpoint is unauthenticated")
		}
	} else {
		slog.Warn("AI_API_KEY not set — chat disabled, ingest + health active")
	}

	bookmarks, err := ai.NewBookmarkStore(cfg.LakeDir)
	if err != nil {
		slog.Error("bookmarks init failed", "err", err)
		os.Exit(1)
	}

	// UI routes (chat SSE + bookmarks + suggestions)
	api.RegisterUIRoutes(e, cfg, orch, sseHandler, bookmarks, svc, alertStore)

	// Alert management REST endpoints
	api.RegisterAlertRoutes(e, alertStore, alertEngine)

	// Auth routes (only if SMTP configured)
	if cfg.AuthEnabled() {
		smtpCfg := auth.SMTPConfig{
			Host: cfg.SMTPHost,
			Port: cfg.SMTPPort,
			User: cfg.SMTPUser,
			Pass: cfg.SMTPPass,
			From: cfg.SMTPFrom,
		}
		codeStore := auth.NewCodeStore(sqlite.DB, jwtSecret)
		api.RegisterAuthRoutes(e, userStore, codeStore, jwtSecret, smtpCfg)
		api.RegisterUserRoutes(e, userStore, smtpCfg)
		slog.Info("auth enabled")
	}

	// MCP HTTP routes (Model Context Protocol) — expose if enabled
	if cfg.MCPEnabled {
		mcpServer.RegisterRoutes(e)
		slog.Info("MCP server enabled", "path", "/mcp")
	}

	// SPA catch-all — serves the embedded React app for any unmatched route.
	// API routes registered above take priority; everything else falls through here.
	spaFS, spaErr := web.ClientDist()
	if spaErr != nil {
		slog.Warn("React SPA not available (not built?)", "err", spaErr)
	} else {
		web.RegisterSPARoutes(e, spaFS)
		slog.Info("React SPA enabled", "path", "/*")
	}

	// Run HTTP
	httpCtx, httpCancel := context.WithCancel(context.Background())
	go func() {
		sc := echo.StartConfig{
			Address:         cfg.HTTPAddr,
			HideBanner:      true,
			GracefulTimeout: 5 * time.Second,
		}
		slog.Info("HTTP listening", "addr", cfg.HTTPAddr)
		if err := sc.Start(httpCtx, e); err != nil && err != http.ErrServerClosed {
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

	// Coordinated shutdown: cancel context → close AI session → flush writer → stop servers
	cancel()
	if aiTools != nil {
		if err := aiTools.Close(); err != nil {
			slog.Error("AI tool registry close failed", "err", err)
		}
	}
	writer.Wait()
	grpcSrv.GracefulStop()
	httpCancel() // triggers graceful HTTP shutdown (5s timeout)
}
