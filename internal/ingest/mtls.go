package ingest

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/labstack/fanout/internal/config"
)

func GRPCServerOptions(cfg config.Config) ([]grpc.ServerOption, error) {
	if !cfg.OTLPMTLSEnabled() {
		return nil, nil
	}

	tlsConfig, err := otlpTLSConfig(cfg)
	if err != nil {
		return nil, err
	}
	return []grpc.ServerOption{grpc.Creds(credentials.NewTLS(tlsConfig))}, nil
}

func otlpTLSConfig(cfg config.Config) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(cfg.OTLPTLSCertFile, cfg.OTLPTLSKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load OTLP server cert: %w", err)
	}

	caPEM, err := os.ReadFile(cfg.OTLPTLSClientCAFile)
	if err != nil {
		return nil, fmt.Errorf("read OTLP client CA: %w", err)
	}

	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("parse OTLP client CA")
	}

	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		NextProtos:   []string{"h2"},
	}, nil
}
