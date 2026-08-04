package settings

import (
	"context"
	"testing"

	"github.com/labstack/fanout/internal/auth"
	appstore "github.com/labstack/fanout/internal/store"
)

func TestGetIngest_EmptyBeforeSetup(t *testing.T) {
	store := newTestStore(t)

	cfg, err := store.GetIngest(context.Background())
	if err != nil {
		t.Fatalf("GetIngest: %v", err)
	}
	if cfg.TokenHash != "" {
		t.Fatalf("token hash = %q, want empty for fresh store", cfg.TokenHash)
	}
}

func TestSetIngest_RoundTripsTokenHash(t *testing.T) {
	store := newTestStore(t)
	want := Ingest{TokenHash: HashIngestToken("fo_test")}

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

func TestSetIngestWithAuditSurvivesRequestCancellation(t *testing.T) {
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	store := NewStore(sqlite.DB)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	want := Ingest{TokenHash: HashIngestToken("fo_detached")}
	if err := store.SetIngestWithAudit(ctx, want, auth.NewAuditStore(sqlite.DB), auth.AuditEvent{
		EventType: "ingest_key.rotated", Outcome: "success", TargetType: "ingest", TargetID: "default",
	}); err != nil {
		t.Fatalf("SetIngestWithAudit after cancellation: %v", err)
	}
	got, err := store.GetIngest(context.Background())
	if err != nil || got != want {
		t.Fatalf("ingest = %#v, err %v", got, err)
	}
	var auditCount int
	if err := sqlite.DB.QueryRow(`SELECT COUNT(*) FROM auth_audit_events WHERE event_type = ?`, "ingest_key.rotated").Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 1 {
		t.Fatalf("audit events = %d, want 1", auditCount)
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
	if CheckIngestToken("fo_other", hash) {
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
