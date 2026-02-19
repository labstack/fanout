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
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/gzip" // Register gzip decompressor

	"github.com/labstack/fanout/internal/ai"
	"github.com/labstack/fanout/internal/api"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/ingest"
	"github.com/labstack/fanout/internal/intelligence"
	"github.com/labstack/fanout/internal/lake"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/service"
	"github.com/labstack/fanout/internal/web"
)

var tokenRedactRe = regexp.MustCompile(`token=[^&]+`)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	cfg := config.Load()

	if err := os.MkdirAll(cfg.LakeDir, 0o755); err != nil {
		slog.Error("create lake dir failed", "err", err)
		os.Exit(1)
	}

	// Clean up orphaned temp files from previous crashes
	lake.CleanupTempFiles(cfg.LakeDir)

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

	// Start Lake Writer
	writer := lake.NewWriter(cfg, chSpans, chLogs, chMetrics)
	go func() {
		if err := writer.Run(ctx); err != nil {
			errCh <- fmt.Errorf("lake writer: %w", err)
		}
	}()

	// Start DuckDB + rollups
	q, err := query.NewDuck(ctx, cfg)
	if err != nil {
		slog.Error("duckdb init failed", "err", err)
		os.Exit(1)
	}
	defer q.Close()

	go q.RunRollups(ctx)

	// Start retention pruner
	pruner := lake.NewPruner(cfg)
	go pruner.Run(ctx)

	// Start compactor (merges small hourly files into daily files)
	compactor := lake.NewCompactor(cfg, q.DB)
	go compactor.Run(ctx)

	// Start intelligence detector
	detector := intelligence.NewDetector(q, intelligence.DefaultDetectorConfig())
	go detector.Run(ctx)

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
	e.HideBanner = true
	e.Use(middleware.Recover())
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogURI:       true,
		LogStatus:    true,
		LogLatency:   true,
		LogMethod:    true,
		LogRemoteIP:  true,
		LogUserAgent: true,
		LogError:     true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			uri := v.URI
			if strings.Contains(uri, "token=") {
				uri = tokenRedactRe.ReplaceAllString(uri, "token=REDACTED")
			}
			slog.Info("request", "method", v.Method, "uri", uri, "status", v.Status, "latency", v.Latency)
			return nil
		},
	}))

	// Auth (optional) — skip health checks and metrics for orchestrators/LBs
	apiToken := strings.TrimSpace(cfg.APIToken)
	if apiToken != "" {
		tokenBytes := []byte(apiToken)
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				path := c.Request().URL.Path

				// Skip auth for health, metrics, and UI page routes
				if path == "/healthz" || path == "/readyz" || path == "/-/metrics" ||
					path == "/" || path == "/favicon.ico" || path == "/favicon.svg" ||
					strings.HasPrefix(path, "/static/") {
					return next(c)
				}

				// WebSocket: accept token via query param since browsers can't set headers
				if path == "/ws/chat" {
					token := c.QueryParam("token")
					if subtle.ConstantTimeCompare([]byte(token), tokenBytes) != 1 {
						return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
					}
					return next(c)
				}

				auth := c.Request().Header.Get("Authorization")
				if !strings.HasPrefix(auth, "Bearer ") {
					return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
				}
				provided := []byte(strings.TrimPrefix(auth, "Bearer "))
				if subtle.ConstantTimeCompare(provided, tokenBytes) != 1 {
					return echo.NewHTTPError(http.StatusUnauthorized, "invalid token")
				}
				return next(c)
			}
		})
	}

	// Health checks (liveness + readiness)
	api.RegisterHealthRoutes(e, q, cfg)

	// Prometheus metrics (internal/ops)
	e.GET("/-/metrics", echo.WrapHandler(promhttp.Handler()))

	// Create shared service layer
	svc := service.New(q, cfg)

	// AI orchestrator (optional — needs API key)
	var orch *ai.Orchestrator
	var wsHandler *ai.WSHandler

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

		tools := ai.NewToolRegistry(svc, q, cfg.LakeDir)
		orch = ai.NewOrchestrator(provider, tools, svc, cfg)
		wsHandler = ai.NewWSHandler(orch, svc)
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

	// Static viz assets (cache-busted JS/CSS bundles)
	web.RegisterStaticRoutes(e)

	// UI routes (chat page + WebSocket + bookmarks + suggestions)
	api.RegisterUIRoutes(e, cfg, orch, wsHandler, bookmarks)

	// Run HTTP
	go func() {
		slog.Info("HTTP listening", "addr", cfg.HTTPAddr)
		if err := e.Start(cfg.HTTPAddr); err != nil && err != http.ErrServerClosed {
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

	// Coordinated shutdown: cancel context → wait for writer flush → stop servers
	cancel()
	writer.Wait()
	grpcSrv.GracefulStop()
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	if err := e.Shutdown(ctxShutdown); err != nil {
		slog.Warn("HTTP server shutdown error", "err", err)
	}
}
