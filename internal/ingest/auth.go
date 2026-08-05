package ingest

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/settings"
)

func GRPCServerOptions(cfg env.Config, settingsStore *settings.Store) ([]grpc.ServerOption, error) {
	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(newIngestAuthorizer(settingsStore).Unary()),
	}
	if !cfg.TLSEnabled() {
		return opts, nil
	}

	tlsConfig, err := tlsServerConfig(cfg)
	if err != nil {
		return nil, err
	}
	return append(opts, grpc.Creds(credentials.NewTLS(tlsConfig))), nil
}

func tlsServerConfig(cfg env.Config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load TLS server cert: %w", err)
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2"},
	}, nil
}

type ingestAuthorizer struct {
	settingsStore *settings.Store
}

func newIngestAuthorizer(settingsStore *settings.Store) *ingestAuthorizer {
	return &ingestAuthorizer{settingsStore: settingsStore}
}

func (a *ingestAuthorizer) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := a.authorize(ctx); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// authorize requires a valid ingest token on every request (set up during
// admin creation, rotatable from the settings page). Peer IP is not
// considered — operators decide who reaches the port.
func (a *ingestAuthorizer) authorize(ctx context.Context) error {
	if a.settingsStore == nil {
		// Defensive: production always wires a store (cmd/fanout/main.go). Reaching
		// this path means a mis-wired init; fail closed and log so it surfaces.
		slog.Error("ingest: settings store not wired; rejecting request")
		return status.Error(codes.Internal, "ingest auth unavailable")
	}
	ingestCfg, err := a.settingsStore.GetIngest(ctx)
	if err != nil {
		// Don't leak the wrapped error to the client — log it server-side.
		slog.Error("ingest: load config failed", "err", err)
		return status.Error(codes.Internal, "ingest auth unavailable")
	}
	if ingestCfg.TokenHash == "" {
		// Pre-setup (no admin yet) — no token exists to check against. Reject
		// all requests until setup completes; collectors must wait.
		slog.Warn("ingest: rejecting request — no ingest token configured (setup not complete)")
		return status.Error(codes.Unauthenticated, "fanout not initialized")
	}
	token := ingestTokenFromContext(ctx)
	if token == "" || !settings.CheckIngestToken(token, ingestCfg.TokenHash) {
		return status.Error(codes.Unauthenticated, "invalid ingest token")
	}
	return nil
}

func ingestTokenFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if values := md.Get("x-fanout-ingest-token"); len(values) > 0 {
		return strings.TrimSpace(values[0])
	}
	if values := md.Get("authorization"); len(values) > 0 {
		const prefix = "Bearer "
		if strings.HasPrefix(values[0], prefix) {
			return strings.TrimSpace(strings.TrimPrefix(values[0], prefix))
		}
	}
	return ""
}
