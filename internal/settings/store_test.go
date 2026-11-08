package settings

import (
	"context"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/auth"
	"github.com/labstack/fanout/internal/secret"
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
	want := Ingest{TokenHash: HashIngestToken("foi_test")}

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
	want := Ingest{TokenHash: HashIngestToken("foi_detached")}
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
	if !secret.ValidSecret(token, "foi_") || len(hash) != 64 {
		t.Fatal("token or hash is empty")
	}
	if !CheckIngestToken(token, hash) {
		t.Fatal("CheckIngestToken returned false for the generated token")
	}
	if CheckIngestToken("foi_"+strings.Repeat("A", 43), hash) {
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

func TestIngestRejectsRetiredTokenEvenWithMatchingDigest(t *testing.T) {
	token := "fo_" + strings.Repeat("a", 48)
	if CheckIngestToken(token, HashIngestToken(token)) {
		t.Fatal("retired ingest token accepted")
	}
}

func configuredIngestToken(fill string) string {
	return "foi_" + strings.Repeat(fill, 43)
}

func ingestAuditCount(t *testing.T, store *Store) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'ingest_key.rotated'`).Scan(&count); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	return count
}

func TestApplyConfiguredIngestTokenStoresOnlyHashOnEmptyInstall(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	token := configuredIngestToken("A")

	applied, err := store.ApplyConfiguredIngestToken(ctx, token, auth.NewAuditStore(store.db))
	if err != nil {
		t.Fatalf("ApplyConfiguredIngestToken: %v", err)
	}
	if !applied {
		t.Fatal("token was not applied to an empty install")
	}
	got, err := store.GetIngest(ctx)
	if err != nil {
		t.Fatalf("GetIngest: %v", err)
	}
	if got.TokenHash != HashIngestToken(token) || !CheckIngestToken(token, got.TokenHash) {
		t.Fatal("stored hash does not verify the configured token")
	}

	var value, metadata string
	if err := store.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, ingestKey).Scan(&value); err != nil {
		t.Fatalf("read setting: %v", err)
	}
	if err := store.db.QueryRow(`SELECT metadata_json FROM auth_audit_events WHERE event_type = 'ingest_key.rotated'`).Scan(&metadata); err != nil {
		t.Fatalf("read audit event: %v", err)
	}
	if strings.Contains(value, token) || strings.Contains(metadata, token) {
		t.Fatal("raw token was persisted")
	}
	if !strings.Contains(metadata, `"configuration"`) {
		t.Fatalf("audit metadata = %s, want configuration source", metadata)
	}
}

func TestApplyConfiguredIngestTokenIgnoredOnceHashExists(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	audit := auth.NewAuditStore(store.db)
	first := configuredIngestToken("A")

	if _, err := store.ApplyConfiguredIngestToken(ctx, first, audit); err != nil {
		t.Fatalf("first apply: %v", err)
	}

	// A later restart with a different value changes nothing.
	applied, err := store.ApplyConfiguredIngestToken(ctx, configuredIngestToken("B"), audit)
	if err != nil {
		t.Fatalf("second apply: %v", err)
	}
	got, err := store.GetIngest(ctx)
	if err != nil {
		t.Fatalf("GetIngest: %v", err)
	}
	if applied || got.TokenHash != HashIngestToken(first) {
		t.Fatal("a different configured value replaced the stored hash")
	}
	if count := ingestAuditCount(t, store); count != 1 {
		t.Fatalf("audit events = %d, want 1", count)
	}

	// Rotation from the UI survives restarts that still carry the setting.
	_, rotated, err := GenerateIngestToken()
	if err != nil {
		t.Fatalf("GenerateIngestToken: %v", err)
	}
	if err := store.SetIngest(ctx, Ingest{TokenHash: rotated}); err != nil {
		t.Fatalf("SetIngest: %v", err)
	}
	applied, err = store.ApplyConfiguredIngestToken(ctx, first, audit)
	if err != nil {
		t.Fatalf("apply after rotation: %v", err)
	}
	got, err = store.GetIngest(ctx)
	if err != nil {
		t.Fatalf("GetIngest: %v", err)
	}
	if applied || got.TokenHash != rotated {
		t.Fatal("configured value replaced a rotated token")
	}
}

func TestApplyConfiguredIngestTokenFillsEmptyStoredHash(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	token := configuredIngestToken("A")
	if err := store.SetIngest(ctx, Ingest{}); err != nil {
		t.Fatalf("SetIngest: %v", err)
	}

	applied, err := store.ApplyConfiguredIngestToken(ctx, token, auth.NewAuditStore(store.db))
	if err != nil {
		t.Fatalf("ApplyConfiguredIngestToken: %v", err)
	}
	got, err := store.GetIngest(ctx)
	if err != nil {
		t.Fatalf("GetIngest: %v", err)
	}
	if !applied || got.TokenHash != HashIngestToken(token) {
		t.Fatal("an empty stored hash must count as no token")
	}
}

func TestApplyConfiguredIngestTokenFailsOnCorruptSetting(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	token := configuredIngestToken("A")
	if err := store.Upsert(ctx, ingestKey, "not an ingest object"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	applied, err := store.ApplyConfiguredIngestToken(ctx, token, auth.NewAuditStore(store.db))
	if err == nil || applied || strings.Contains(err.Error(), token) {
		t.Fatalf("applied = %v err = %v, want redacted failure", applied, err)
	}
	if count := ingestAuditCount(t, store); count != 0 {
		t.Fatalf("audit events = %d, want 0", count)
	}
}
