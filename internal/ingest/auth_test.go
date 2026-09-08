package ingest

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/labstack/fanout/internal/settings"
	appstore "github.com/labstack/fanout/internal/store"
)

func TestGRPCServerOptions(t *testing.T) {
	if opts := GRPCServerOptions(newRuntimeStore(t)); len(opts) != 1 {
		t.Fatalf("len(opts) = %d, want 1", len(opts))
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
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer fo_wrong"))

	err = authorizer.authorize(ctx)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}
}

func TestAuthorize_AcceptsCaseInsensitiveBearerScheme(t *testing.T) {
	store := newRuntimeStore(t)
	token, hash, err := settings.GenerateIngestToken()
	if err != nil {
		t.Fatalf("GenerateIngestToken: %v", err)
	}
	if err := store.SetIngest(context.Background(), settings.Ingest{TokenHash: hash}); err != nil {
		t.Fatalf("SetIngest: %v", err)
	}

	authorizer := newIngestAuthorizer(store)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "bearer "+token))

	if err := authorizer.authorize(ctx); err != nil {
		t.Fatalf("authorize with Bearer header: %v", err)
	}
}

func TestAuthorize_RejectsLegacyFanoutHeader(t *testing.T) {
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

	err = authorizer.authorize(ctx)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.Unauthenticated)
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
