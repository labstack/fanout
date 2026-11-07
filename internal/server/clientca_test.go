package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadClientCAs(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "client CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	const contents = "private file contents must not appear in errors"
	for _, test := range []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{name: "one certificate", data: certificate},
		{name: "bundle", data: append(append([]byte{}, certificate...), certificate...)},
		{name: "unreadable", wantErr: true},
		{name: "empty", data: []byte{}, wantErr: true},
		{name: "not PEM", data: []byte(contents), wantErr: true},
		{name: "private key block", data: append(append([]byte{}, certificate...), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte(contents)})...), wantErr: true},
		{name: "invalid certificate", data: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte(contents)}), wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "client-ca.pem")
			if test.data != nil {
				if err := os.WriteFile(path, test.data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			pool, err := LoadClientCAs(path)
			if !test.wantErr {
				if err != nil || pool == nil {
					t.Fatalf("LoadClientCAs() = %v, %v", pool, err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), "FANOUT_TLS_CLIENT_CA_FILE:") || !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), contents) {
				t.Fatalf("error must name the variable and path without contents, got %v", err)
			}
		})
	}
}

func TestRequireClientCertificatesKeepsProtocols(t *testing.T) {
	srv := New("", http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler())
	pool := x509.NewCertPool()
	RequireClientCertificates(srv, pool)
	if srv.TLSConfig.ClientAuth != tls.RequireAndVerifyClientCert || srv.TLSConfig.ClientCAs != pool {
		t.Fatalf("client auth = %v, pool set = %v", srv.TLSConfig.ClientAuth, srv.TLSConfig.ClientCAs == pool)
	}
	if srv.TLSConfig.MinVersion != tls.VersionTLS13 || !srv.Protocols.HTTP2() || !srv.Protocols.HTTP1() {
		t.Fatalf("TLS min version %x or protocols changed", srv.TLSConfig.MinVersion)
	}
}
