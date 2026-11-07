package server

import (
	"bytes"
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

// LoadClientCAs reads the FANOUT_TLS_CLIENT_CA_FILE bundle: one or more PEM
// CERTIFICATE blocks separated only by whitespace. It rejects anything else,
// including text around the blocks and malformed blocks that pem.Decode would
// otherwise skip, so a truncated or wrong file fails startup. Errors name the
// path, never file contents.
func LoadClientCAs(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("FANOUT_TLS_CLIENT_CA_FILE: read %q: %w", path, err)
	}
	pool := x509.NewCertPool()
	count := 0
	for {
		data = bytes.TrimLeft(data, " \t\r\n")
		if len(data) == 0 {
			break
		}
		block, rest := pem.Decode(data)
		consumed := data[:len(data)-len(rest)]
		// pem.Decode skips leading text and malformed blocks; only accept a
		// block that starts the remaining input and is the only one consumed.
		if block == nil || !bytes.HasPrefix(data, pemBegin) || bytes.Count(consumed, pemBegin) != 1 {
			return nil, fmt.Errorf("FANOUT_TLS_CLIENT_CA_FILE: %q contains data that is not a PEM block after certificate %d", path, count)
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
		data = rest
	}
	if count == 0 {
		return nil, fmt.Errorf("FANOUT_TLS_CLIENT_CA_FILE: no PEM certificates in %q", path)
	}
	return pool, nil
}

var pemBegin = []byte("-----BEGIN ")
