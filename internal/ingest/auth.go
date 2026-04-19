package ingest

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/settings"
)

func GRPCServerOptions(cfg env.Config, settingsStore *settings.Store) ([]grpc.ServerOption, error) {
	opts := []grpc.ServerOption{
		grpc.UnaryInterceptor(newIngestAuthorizer(cfg, settingsStore).Unary()),
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
	cfg           env.Config
	settingsStore *settings.Store
}

func newIngestAuthorizer(cfg env.Config, settingsStore *settings.Store) *ingestAuthorizer {
	return &ingestAuthorizer{cfg: cfg, settingsStore: settingsStore}
}

func (a *ingestAuthorizer) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := a.authorize(ctx); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func (a *ingestAuthorizer) authorize(ctx context.Context) error {
	if a.settingsStore == nil {
		return nil
	}

	ingestCfg, err := a.settingsStore.GetIngest(ctx)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to load ingest config: %v", err)
	}

	switch ingestCfg.Mode {
	case settings.IngestModePrivate:
		if isPrivatePeer(ctx) {
			return nil
		}
		return status.Error(codes.PermissionDenied, "OTLP ingest is private")
	case settings.IngestModePublic:
		if !a.cfg.TLSEnabled() {
			return status.Error(codes.FailedPrecondition, "public OTLP requires TLS")
		}
		if !hasTLSPeer(ctx) {
			return status.Error(codes.Unauthenticated, "TLS required")
		}
		token := ingestTokenFromContext(ctx)
		if token == "" || !settings.CheckIngestToken(token, ingestCfg.TokenHash) {
			return status.Error(codes.Unauthenticated, "invalid ingest token")
		}
		return nil
	default:
		return status.Error(codes.FailedPrecondition, "invalid ingest mode")
	}
}

func hasTLSPeer(ctx context.Context) bool {
	p, ok := peer.FromContext(ctx)
	if !ok || p.AuthInfo == nil {
		return false
	}
	_, ok = p.AuthInfo.(credentials.TLSInfo)
	return ok
}

func isPrivatePeer(ctx context.Context) bool {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return false
	}
	if p.Addr.Network() == "unix" {
		return true
	}

	host := p.Addr.String()
	if tcpAddr, ok := p.Addr.(*net.TCPAddr); ok && tcpAddr.IP != nil {
		return isPrivateIP(tcpAddr.IP)
	}
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return isPrivateIP(ip)
}

func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
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
