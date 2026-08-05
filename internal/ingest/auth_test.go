package ingest

import (
	"context"
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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/settings"
	appstore "github.com/labstack/fanout/internal/store"
)

func TestGRPCServerOptions_NoTLS(t *testing.T) {
	store := newRuntimeStore(t)
	opts, err := GRPCServerOptions(env.Config{}, store)
	if err != nil {
		t.Fatalf("GRPCServerOptions: %v", err)
	}
	if len(opts) != 1 {
		t.Fatalf("len(opts) = %d, want 1", len(opts))
	}
}

func TestTLSConfig(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeServerTLSFiles(t, dir)

	tlsConfig, err := tlsServerConfig(env.Config{
		TLSCertFile: certFile,
		TLSKeyFile:  keyFile,
	})
	if err != nil {
		t.Fatalf("tlsServerConfig: %v", err)
	}
	if tlsConfig.MinVersion != tls.VersionTLS13 {
		t.Fatalf("MinVersion = %v, want TLS 1.3", tlsConfig.MinVersion)
	}
	if len(tlsConfig.Certificates) != 1 {
		t.Fatalf("len(Certificates) = %d, want 1", len(tlsConfig.Certificates))
	}
}

func TestAuthorize_RejectsWhenPreSetup(t *testing.T) {
	// With no token persisted (pre-admin-setup), every request is rejected —
	// collectors must wait for the operator to complete setup.
	store := newRuntimeStore(t)
	authorizer := newIngestAuthorizer(store)

	err := authorizer.authorize(context.Background())
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}
}

func TestAuthorize_AcceptsValidToken(t *testing.T) {
	store := newRuntimeStore(t)
	token, hash, err := settings.GenerateIngestToken()
	if err != nil {
		t.Fatalf("GenerateIngestToken: %v", err)
	}
	if err := store.SetIngest(context.Background(), settings.Ingest{TokenHash: hash}); err != nil {
		t.Fatalf("SetIngest: %v", err)
	}

	authorizer := newIngestAuthorizer(store)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-fanout-ingest-token", token))

	if err := authorizer.authorize(ctx); err != nil {
		t.Fatalf("authorize with valid token: %v", err)
	}
}

func TestAuthorize_RejectsWrongToken(t *testing.T) {
	store := newRuntimeStore(t)
	_, hash, err := settings.GenerateIngestToken()
	if err != nil {
		t.Fatalf("GenerateIngestToken: %v", err)
	}
	if err := store.SetIngest(context.Background(), settings.Ingest{TokenHash: hash}); err != nil {
		t.Fatalf("SetIngest: %v", err)
	}

	authorizer := newIngestAuthorizer(store)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-fanout-ingest-token", "fo_wrong"))

	err = authorizer.authorize(ctx)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}
}

func TestAuthorize_AcceptsBearerAuthorization(t *testing.T) {
	store := newRuntimeStore(t)
	token, hash, err := settings.GenerateIngestToken()
	if err != nil {
		t.Fatalf("GenerateIngestToken: %v", err)
	}
	if err := store.SetIngest(context.Background(), settings.Ingest{TokenHash: hash}); err != nil {
		t.Fatalf("SetIngest: %v", err)
	}

	authorizer := newIngestAuthorizer(store)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))

	if err := authorizer.authorize(ctx); err != nil {
		t.Fatalf("authorize with Bearer header: %v", err)
	}
}

func TestAuthorize_RejectsMissingToken(t *testing.T) {
	store := newRuntimeStore(t)
	_, hash, err := settings.GenerateIngestToken()
	if err != nil {
		t.Fatalf("GenerateIngestToken: %v", err)
	}
	if err := store.SetIngest(context.Background(), settings.Ingest{TokenHash: hash}); err != nil {
		t.Fatalf("SetIngest: %v", err)
	}

	authorizer := newIngestAuthorizer(store)

	err = authorizer.authorize(context.Background())
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}
}

func newRuntimeStore(t *testing.T) *settings.Store {
	t.Helper()

	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	return settings.NewStore(sqlite.DB)
}

func writeServerTLSFiles(t *testing.T, dir string) (string, string) {
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
	serverTemplate := certificateTemplate("fanout.example.com", false)
	serverTemplate.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	serverTemplate.DNSNames = []string{"fanout.example.com", "localhost"}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CreateCertificate(server): %v", err)
	}

	certFile := filepath.Join(dir, "server.pem")
	keyFile := filepath.Join(dir, "server-key.pem")

	writePEMFile(t, certFile, "CERTIFICATE", serverDER)
	writePEMFile(t, keyFile, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKey))
	_ = caDER
	return certFile, keyFile
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
