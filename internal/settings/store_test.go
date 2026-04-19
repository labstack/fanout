package settings

import (
	"context"
	"testing"

	appstore "github.com/labstack/fanout/internal/store"
)

func TestGetIngest_DefaultsToPrivate(t *testing.T) {
	store := newTestStore(t)

	cfg, err := store.GetIngest(context.Background())
	if err != nil {
		t.Fatalf("GetIngest: %v", err)
	}
	if cfg.Mode != IngestModePrivate {
		t.Fatalf("mode = %q, want %q", cfg.Mode, IngestModePrivate)
	}
}

func TestSetIngest_PersistsConfig(t *testing.T) {
	store := newTestStore(t)
	want := IngestConfig{
		Mode:           IngestModePublic,
		PublicEndpoint: "fanout.example.com:4317",
		TokenHash:      HashIngestToken("fi_test"),
	}

	if err := store.SetIngest(context.Background(), want); err != nil {
		t.Fatalf("SetIngest: %v", err)
	}

	got, err := store.GetIngest(context.Background())
	if err != nil {
		t.Fatalf("GetIngest: %v", err)
	}
	if got != want {
		t.Fatalf("config = %#v, want %#v", got, want)
	}
}

func TestGenerateAndCheckIngestToken(t *testing.T) {
	token, hash, err := GenerateIngestToken()
	if err != nil {
		t.Fatalf("GenerateIngestToken: %v", err)
	}
	if token == "" || hash == "" {
		t.Fatal("token or hash is empty")
	}
	if !CheckIngestToken(token, hash) {
		t.Fatal("CheckIngestToken returned false for the generated token")
	}
	if CheckIngestToken("fi_other", hash) {
		t.Fatal("CheckIngestToken returned true for the wrong token")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()

	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	return NewStore(sqlite.DB)
}
