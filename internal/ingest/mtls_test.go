package ingest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
)

func TestGRPCServerOptions_Disabled(t *testing.T) {
	opts, err := GRPCServerOptions(config.Config{})
	if err != nil {
		t.Fatalf("GRPCServerOptions: %v", err)
	}
	if len(opts) != 0 {
		t.Fatalf("len(opts) = %d, want 0", len(opts))
	}
}

func TestOTLPTLSConfig(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, caFile := writeServerTLSFiles(t, dir)

	tlsConfig, err := otlpTLSConfig(config.Config{
		OTLPTLSCertFile:     certFile,
		OTLPTLSKeyFile:      keyFile,
		OTLPTLSClientCAFile: caFile,
	})
	if err != nil {
		t.Fatalf("otlpTLSConfig: %v", err)
	}
	if tlsConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %v, want TLS 1.3", tlsConfig.MinVersion)
	}
	if tlsConfig.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatalf("ClientAuth = %v, want RequireAndVerifyClientCert", tlsConfig.ClientAuth)
	}
	if len(tlsConfig.Certificates) != 1 {
		t.Fatalf("len(Certificates) = %d, want 1", len(tlsConfig.Certificates))
	}
	if tlsConfig.ClientCAs == nil {
		t.Fatal("ClientCAs is nil")
	}

	opts, err := GRPCServerOptions(config.Config{
		OTLPTLSCertFile:     certFile,
		OTLPTLSKeyFile:      keyFile,
		OTLPTLSClientCAFile: caFile,
	})
	if err != nil {
		t.Fatalf("GRPCServerOptions: %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("len(opts) = %d, want 1", len(opts))
	}
}

func TestOTLPTLSConfig_InvalidCA(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile, _ := writeServerTLSFiles(t, dir)
	caFile := filepath.Join(dir, "invalid-ca.pem")
	if err := os.WriteFile(caFile, []byte("not pem"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := otlpTLSConfig(config.Config{
		OTLPTLSCertFile:     certFile,
		OTLPTLSKeyFile:      keyFile,
		OTLPTLSClientCAFile: caFile,
	})
	if err == nil {
		t.Fatal("expected error for invalid client CA")
	}
}

func writeServerTLSFiles(t *testing.T, dir string) (string, string, string) {
	t.Helper()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey(ca): %v", err)
	}
	caTemplate := certificateTemplate("fanout-test-ca", true)
	caTemplate.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CreateCertificate(ca): %v", err)
	}

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey(server): %v", err)
	}
	serverTemplate := certificateTemplate("fanout.test", false)
	serverTemplate.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	serverTemplate.DNSNames = []string{"fanout.test", "localhost"}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CreateCertificate(server): %v", err)
	}

	certFile := filepath.Join(dir, "server.pem")
	keyFile := filepath.Join(dir, "server-key.pem")
	caFile := filepath.Join(dir, "ca.pem")

	writePEMFile(t, certFile, "CERTIFICATE", serverDER)
	writePEMFile(t, caFile, "CERTIFICATE", caDER)
	writePEMFile(t, keyFile, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKey))
	return certFile, keyFile, caFile
}

func certificateTemplate(commonName string, isCA bool) *x509.Certificate {
	now := time.Now().UTC()
	return &x509.Certificate{
		SerialNumber:          big.NewInt(now.UnixNano()),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(24 * time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}
}

func writePEMFile(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	block := &pem.Block{Type: blockType, Bytes: der}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}
