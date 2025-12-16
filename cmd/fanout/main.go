package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"google.golang.org/grpc"
	_ "google.golang.org/grpc/encoding/gzip" // Register gzip decompressor
	"google.golang.org/grpc/metadata"

	"github.com/labstack/fanout/internal/api"
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/ingest"
	"github.com/labstack/fanout/internal/lake"
	"github.com/labstack/fanout/internal/mcp"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/service"
)

func main() {
	cfg := config.Load()

	if err := os.MkdirAll(cfg.LakeDir, 0o755); err != nil {
		log.Fatalf("create lake dir: %v", err)
	}

	// Channels for ingest → lake writer
	chSpans := make(chan lake.SpanRow, 10000)
	chLogs := make(chan lake.LogRow, 10000)
	chMetrics := make(chan lake.MetricRow, 10000)

	// Start Lake Writer
	writer := lake.NewWriter(cfg, chSpans, chLogs, chMetrics)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := writer.Run(ctx); err != nil {
			log.Fatalf("lake writer error: %v", err)
		}
	}()

	// Start DuckDB + rollups
	q, err := query.NewDuck(ctx, cfg)
	if err != nil {
		log.Fatalf("duckdb init: %v", err)
	}
	defer q.Close()

	go q.RunRollups(ctx)

	// Start retention pruner
	pruner := lake.NewPruner(cfg)
	go pruner.Run(ctx)

	// Start gRPC ingest (OTLP)
	grpcLis, err := net.Listen("tcp", cfg.OTLPGRPCAddr)
	if err != nil {
		log.Fatalf("listen gRPC: %v", err)
	}
	grpcSrv := grpc.NewServer(
		grpc.UnaryInterceptor(withTenantInterceptor()),
	)
	ing := ingest.NewServer(chSpans, chLogs, chMetrics)
	ingest.RegisterOTLP(grpcSrv, ing)

	go func() {
		log.Printf("[ingest] gRPC OTLP listening on %s", cfg.OTLPGRPCAddr)
		if err := grpcSrv.Serve(grpcLis); err != nil && err != grpc.ErrServerStopped {
			log.Fatalf("grpc serve: %v", err)
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
			log.Printf("%s %s %d %s", v.Method, v.URI, v.Status, v.Latency)
			return nil
		},
	}))

	// Auth (optional)
	apiToken := strings.TrimSpace(cfg.APIToken)
	if apiToken != "" {
		e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				auth := c.Request().Header.Get("Authorization")
				if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != apiToken {
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

	// UI routes (Templ + HTMX + Vega-Lite)
	api.RegisterUIRoutes(e, q, cfg)

	// Create shared service layer
	svc := service.New(q, cfg)

	// MCP server (Model Context Protocol)
	if cfg.MCPEnabled {
		mcpServer := mcp.NewServer(svc, q, cfg)
		mcpServer.RegisterRoutes(e)
		log.Printf("[mcp] MCP server enabled at /mcp")

		// Start report cleanup goroutine
		go mcp.RunCleanup(ctx)
	}

	// Run HTTP
	go func() {
		log.Printf("[api] HTTP listening on %s", cfg.HTTPAddr)
		if err := e.Start(cfg.HTTPAddr); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http start: %v", err)
		}
	}()

	// Shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("shutting down...")
	cancel()
	grpcSrv.GracefulStop()
	ctxShutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	_ = e.Shutdown(ctxShutdown)
}

func withTenantInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		md, ok := metadata.FromIncomingContext(ctx)
		var tenant string
		if ok {
			if vals := md.Get("x-tenant-id"); len(vals) > 0 {
				tenant = vals[0]
			}
		}
		if tenant == "" {
			tenant = os.Getenv("DEFAULT_TENANT_ID")
		}
		return handler(context.WithValue(ctx, ingest.CtxTenantKey{}, tenant), req)
	}
}
