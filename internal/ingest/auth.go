package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/labstack/pitot/internal/secret"
	"github.com/labstack/pitot/internal/settings"
)

// GRPCServerOptions authenticates exports. The shared HTTP server owns TLS.
func GRPCServerOptions(settingsStore *settings.Store) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.UnaryInterceptor(newIngestAuthorizer(settingsStore).Unary()),
	}
}

type ingestAuthorizer struct {
	settingsStore *settings.Store
}

var (
	errIngestAuthUnavailable = errors.New("ingest auth unavailable")
	errInvalidIngestToken    = errors.New("invalid ingest token")
)

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

// authorize requires an active ingest token on every request. Peer IP is not
// considered — operators decide who reaches the port.
func (a *ingestAuthorizer) authorize(ctx context.Context) error {
	err := a.authorizeToken(ctx, ingestTokenFromContext(ctx))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errInvalidIngestToken):
		return status.Error(codes.Unauthenticated, "invalid ingest token")
	default:
		return status.Error(codes.Internal, "ingest auth unavailable")
	}
}

// authorizeToken owns the transport-neutral credential policy. gRPC and HTTP
// extract tokens from their respective headers, then share this verification.
func (a *ingestAuthorizer) authorizeToken(ctx context.Context, token string) error {
	if !secret.ValidSecret(token, "foi_") {
		return errInvalidIngestToken
	}
	if a.settingsStore == nil {
		// Defensive: production always wires a store (cmd/pitot/main.go). Reaching
		// this path means a mis-wired init; fail closed and log so it surfaces.
		slog.Error("ingest: settings store not wired; rejecting request")
		return errIngestAuthUnavailable
	}
	authenticated, err := a.settingsStore.AuthenticateIngestToken(ctx, token)
	if err != nil {
		// Don't leak the wrapped error to the client — log it server-side.
		slog.Error("ingest: token lookup failed", "err", err)
		return fmt.Errorf("%w: %v", errIngestAuthUnavailable, err)
	}
	if !authenticated {
		return errInvalidIngestToken
	}
	return nil
}

func ingestTokenFromContext(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	if values := md.Get("authorization"); len(values) > 0 {
		return ingestTokenFromAuthorization(values[0])
	}
	return ""
}

func ingestTokenFromAuthorization(authorization string) string {
	authorization = strings.TrimSpace(authorization)
	const bearer = "Bearer "
	if len(authorization) >= len(bearer) && strings.EqualFold(authorization[:len(bearer)], bearer) {
		return strings.TrimSpace(authorization[len(bearer):])
	}
	return ""
}
