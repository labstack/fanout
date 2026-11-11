package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
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
		{name: "surrounding whitespace", data: append(append([]byte("\n \r\n"), certificate...), "\n\n"...)},
		{name: "trailing text", data: append(append([]byte{}, certificate...), contents...), wantErr: true},
		{name: "leading text", data: append([]byte(contents+"\n"), certificate...), wantErr: true},
		{name: "malformed block before certificate", data: append([]byte("-----BEGIN CERTIFICATE-----\n!!!\n-----END CERTIFICATE-----\n"), certificate...), wantErr: true},
		{name: "truncated certificate", data: certificate[:len(certificate)-30], wantErr: true},
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
			if err == nil || !strings.HasPrefix(err.Error(), "PITOT_TLS_CLIENT_CA_FILE:") || !strings.Contains(err.Error(), path) || strings.Contains(err.Error(), contents) {
				t.Fatalf("error must name the variable and path without contents, got %v", err)
			}
		})
	}
}

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(t *testing.T, name string, parent *testCA) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	issuer, signer := template, key
	if parent != nil {
		issuer, signer = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: certificate, key: key}
}

// issue returns a leaf certificate followed by any intermediates.
func (ca *testCA) issue(t *testing.T, usage []x509.ExtKeyUsage, ips []net.IP, chain ...*testCA) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "leaf"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: usage, IPAddresses: ips,
	}, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	for _, intermediate := range chain {
		certificate.Certificate = append(certificate.Certificate, intermediate.cert.Raw)
	}
	return certificate
}

// The stdlib verifier enforces the client-auth EKU and builds chains through
// intermediates the client sends, with only the root in the bundle.
func TestRequireClientCertificatesHandshake(t *testing.T) {
	root := newTestCA(t, "client root", nil)
	intermediate := newTestCA(t, "client intermediate", root)
	serverCA := newTestCA(t, "server CA", nil)
	clientAuth := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	for _, test := range []struct {
		name    string
		client  []tls.Certificate
		wantErr string
	}{
		{name: "client auth from root", client: []tls.Certificate{root.issue(t, clientAuth, nil)}},
		{name: "leaf and intermediate", client: []tls.Certificate{intermediate.issue(t, clientAuth, nil, intermediate)}},
		{name: "leaf without its intermediate", client: []tls.Certificate{intermediate.issue(t, clientAuth, nil)}, wantErr: "unknown authority"},
		{name: "server auth only", client: []tls.Certificate{root.issue(t, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, nil)}, wantErr: "incompatible key usage"},
		{name: "no certificate", wantErr: "didn't provide a certificate"},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv := New("", http.NotFoundHandler(), http.NotFoundHandler(), http.NotFoundHandler())
			pool := x509.NewCertPool()
			pool.AddCert(root.cert)
			RequireClientCertificates(srv, pool)
			serverTLS := srv.TLSConfig.Clone()
			serverTLS.Certificates = []tls.Certificate{serverCA.issue(t, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, []net.IP{net.ParseIP("127.0.0.1")})}
			serverTLS.NextProtos = []string{"h2", "http/1.1"}
			roots := x509.NewCertPool()
			roots.AddCert(serverCA.cert)
			// A buffered loopback socket, unlike net.Pipe, lets the server
			// write its alert after the client's handshake has returned.
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverErr := make(chan error, 1)
			go func() {
				serverConn, err := listener.Accept()
				if err != nil {
					serverErr <- err
					return
				}
				_ = serverConn.SetDeadline(time.Now().Add(10 * time.Second))
				conn := tls.Server(serverConn, serverTLS)
				err = conn.Handshake()
				if err == nil && conn.ConnectionState().NegotiatedProtocol != "h2" {
					err = fmt.Errorf("ALPN = %q, want h2", conn.ConnectionState().NegotiatedProtocol)
				}
				serverErr <- err
				_ = conn.Close()
			}()
			clientConn, err := net.Dial("tcp", listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer clientConn.Close()
			_ = clientConn.SetDeadline(time.Now().Add(10 * time.Second))
			clientTLS := &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS13, NextProtos: []string{"h2"}}
			if len(test.client) > 0 {
				// Always send the certificate, even when its issuer is not among
				// the server's acceptable CAs, so the server verifier decides.
				clientTLS.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &test.client[0], nil }
			}
			client := tls.Client(clientConn, clientTLS)
			_ = client.Handshake()
			// TLS 1.3 clients finish before the server checks their certificate,
			// so the server's result decides.
			err = <-serverErr
			if test.wantErr == "" && err != nil || test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("server handshake error = %v, want %q", err, test.wantErr)
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
