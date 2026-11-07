package server

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
)

// RequireClientCertificates makes srv's TLS listener require and verify client
// certificates issued by a CA in pool. TLS 1.3 and ALPN (h2 and http/1.1) are
// unchanged, so gRPC and HTTP keep sharing the listener.
func RequireClientCertificates(srv *http.Server, pool *x509.CertPool) {
	if srv.TLSConfig == nil {
		srv.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS13}
	}
	srv.TLSConfig.ClientCAs = pool
	srv.TLSConfig.ClientAuth = tls.RequireAndVerifyClientCert
}

// LoadClientCAs reads the FANOUT_TLS_CLIENT_CA_FILE bundle. It rejects every
// block it cannot use instead of silently skipping it, so a truncated or wrong
// file fails startup. Errors name the path, never file contents.
func LoadClientCAs(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("FANOUT_TLS_CLIENT_CA_FILE: read %q: %w", path, err)
	}
	pool := x509.NewCertPool()
	count := 0
	for {
		var block *pem.Block
		block, data = pem.Decode(data)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("FANOUT_TLS_CLIENT_CA_FILE: %q contains a %q PEM block; only certificates are allowed", path, block.Type)
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("FANOUT_TLS_CLIENT_CA_FILE: parse certificate %d in %q: %w", count+1, path, err)
		}
		pool.AddCert(certificate)
		count++
	}
	if count == 0 {
		return nil, fmt.Errorf("FANOUT_TLS_CLIENT_CA_FILE: no PEM certificates in %q", path)
	}
	return pool, nil
}
