package settings

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"github.com/labstack/fanout/internal/db"
	"github.com/labstack/fanout/internal/secret"
	appstore "github.com/labstack/fanout/internal/store"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	return NewStore(sqlite.DB)
}

func configuredIngestToken(fill string) string {
	return "foi_" + strings.Repeat(fill, 43)
}

func createTestToken(t *testing.T, store *Store, name string) (IngestToken, string) {
	t.Helper()
	token, plaintext, err := store.CreateIngestToken(context.Background(), name, IngestTokenActor{})
	if err != nil {
		t.Fatalf("CreateIngestToken(%q): %v", name, err)
	}
	return token, plaintext
}

func authenticates(t *testing.T, store *Store, token string) bool {
	t.Helper()
	ok, err := store.AuthenticateIngestToken(context.Background(), token)
	if err != nil {
		t.Fatalf("AuthenticateIngestToken: %v", err)
	}
	return ok
}

func ingestTokenRowCount(t *testing.T, store *Store) int {
	t.Helper()
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM ingest_tokens`).Scan(&count); err != nil {
		t.Fatalf("count ingest tokens: %v", err)
	}
	return count
}

// oldIngestDatabase is a control database at the last schema that kept one
// token hash in the settings row, optionally already holding one.
func oldIngestDatabase(t *testing.T, tokenHash string) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control.sqlite")
	database, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, os.DirFS("../db/migrations"), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 20261108120000); err != nil {
		t.Fatal(err)
	}
	if tokenHash != "" {
		value, err := json.Marshal(map[string]string{"token_hash": tokenHash})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('ingest', ?, '2026-10-01T12:00:00Z')`, string(value)); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func TestMigrationMovesExistingTokenToDefaultAndStillAuthenticates(t *testing.T) {
	token, hash, err := GenerateIngestToken()
	if err != nil {
		t.Fatal(err)
	}
	database := oldIngestDatabase(t, hash)
	if err := db.Migrate(t.Context(), database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(database)

	var name, stored, source, createdAt string
	var revoked sql.NullString
	if err := database.QueryRow(`SELECT name, token_hash, source, created_at, revoked_at FROM ingest_tokens`).Scan(&name, &stored, &source, &createdAt, &revoked); err != nil {
		t.Fatalf("read migrated row: %v", err)
	}
	if name != "default" || stored != hash || source != "migrated" || createdAt != "2026-10-01T12:00:00Z" || revoked.Valid {
		t.Fatalf("migrated row = name %q digest kept %v source %q created %q revoked %v", name, stored == hash, source, createdAt, revoked)
	}
	if !authenticates(t, store, token) {
		t.Fatal("the migrated token no longer authenticates")
	}
	var legacy int
	if err := database.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'ingest'`).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatalf("legacy settings rows = %d err = %v", legacy, err)
	}
}

// An upgraded install whose token came from FANOUT_INGEST_TOKEN still carries
// that setting. It must not gain a second token, and revoking the migrated one
// must not let the setting bring it back on the next start.
func TestMigrationOfConfiguredTokenIsNotSeededAgain(t *testing.T) {
	token := configuredIngestToken("A")
	database := oldIngestDatabase(t, HashIngestToken(token))
	if err := db.Migrate(t.Context(), database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(database)
	ctx := context.Background()

	if applied, err := store.ApplyConfiguredIngestToken(ctx, token); err != nil || applied {
		t.Fatalf("applied = %v err = %v, want existing token kept", applied, err)
	}
	if got := ingestTokenRowCount(t, store); got != 1 {
		t.Fatalf("rows = %d, want the migrated row only", got)
	}
	tokens, err := store.ListActiveIngestTokens(ctx)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("tokens = %v err = %v", tokens, err)
	}
	if err := store.RevokeIngestToken(ctx, tokens[0].ID, IngestTokenActor{}); err != nil {
		t.Fatalf("RevokeIngestToken: %v", err)
	}
	restarted := NewStore(database)
	if applied, err := restarted.ApplyConfiguredIngestToken(ctx, token); err != nil || applied {
		t.Fatalf("after revoke applied = %v err = %v", applied, err)
	}
	if authenticates(t, restarted, token) {
		t.Fatal("a restart brought the revoked token back")
	}
}

func TestMigrationWithoutStoredTokenLeavesSeedAvailable(t *testing.T) {
	database := oldIngestDatabase(t, "")
	if err := db.Migrate(t.Context(), database); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(database)
	if got := ingestTokenRowCount(t, store); got != 0 {
		t.Fatalf("rows = %d, want 0", got)
	}
	if applied, err := store.ApplyConfiguredIngestToken(context.Background(), configuredIngestToken("A")); err != nil || !applied {
		t.Fatalf("applied = %v err = %v, want first token seeded", applied, err)
	}
}

func TestMigrationDropsMalformedStoredTokenRows(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{"invalid JSON", `not json`},
		{"truncated JSON", `{"token_hash":"abc`},
		{"array", `["token_hash"]`},
		{"number", `42`},
		{"numeric token_hash", `{"token_hash":1234567890}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database := oldIngestDatabase(t, "")
			if _, err := database.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('ingest', ?, '2026-10-01T12:00:00Z')`, tc.value); err != nil {
				t.Fatal(err)
			}
			if err := db.Migrate(t.Context(), database); err != nil {
				t.Fatalf("a malformed row must not stop startup: %v", err)
			}
			store := NewStore(database)
			if got := ingestTokenRowCount(t, store); got != 0 {
				t.Fatalf("rows = %d, want 0", got)
			}
			var legacy int
			if err := database.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'ingest'`).Scan(&legacy); err != nil || legacy != 0 {
				t.Fatalf("legacy settings rows = %d err = %v", legacy, err)
			}
			if applied, err := store.ApplyConfiguredIngestToken(context.Background(), configuredIngestToken("A")); err != nil || !applied {
				t.Fatalf("applied = %v err = %v, want the first token seeded", applied, err)
			}
		})
	}
}

func TestApplyConfiguredIngestTokenSeedsFreshInstall(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	token := configuredIngestToken("A")

	applied, err := store.ApplyConfiguredIngestToken(ctx, token)
	if err != nil || !applied {
		t.Fatalf("applied = %v err = %v", applied, err)
	}
	if !authenticates(t, store, token) {
		t.Fatal("the seeded token does not authenticate")
	}
	tokens, err := store.ListActiveIngestTokens(ctx)
	if err != nil || len(tokens) != 1 || tokens[0].Name != "configuration" || tokens[0].Hint != "AAAA" {
		t.Fatalf("tokens = %+v err = %v", tokens, err)
	}
	var hash string
	if err := store.db.QueryRow(`SELECT token_hash FROM ingest_tokens`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != HashIngestToken(token) {
		t.Fatal("the row does not hold the token digest")
	}
	var metadata string
	if err := store.db.QueryRow(`SELECT metadata_json FROM auth_audit_events WHERE event_type = 'ingest_token.created'`).Scan(&metadata); err != nil {
		t.Fatalf("read audit event: %v", err)
	}
	if strings.Contains(metadata, token) || !strings.Contains(metadata, `"configuration"`) {
		t.Fatalf("audit metadata = %s", metadata)
	}
}

func TestApplyConfiguredIngestTokenDoesNotReseedAfterRevokeAndRestart(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	token := configuredIngestToken("A")
	if _, err := store.ApplyConfiguredIngestToken(ctx, token); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tokens, err := store.ListActiveIngestTokens(ctx)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("tokens = %v err = %v", tokens, err)
	}
	if err := store.RevokeIngestToken(ctx, tokens[0].ID, IngestTokenActor{}); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	// A restart is a new Store over the same database, with the setting intact.
	restarted := NewStore(store.db)
	for _, value := range []string{token, configuredIngestToken("B")} {
		applied, err := restarted.ApplyConfiguredIngestToken(ctx, value)
		if err != nil || applied {
			t.Fatalf("reseed applied = %v err = %v", applied, err)
		}
	}
	if authenticates(t, restarted, token) {
		t.Fatal("the revoked token came back")
	}
	if got := ingestTokenRowCount(t, restarted); got != 1 {
		t.Fatalf("rows = %d, want 1", got)
	}
}

func TestIssueFirstIngestTokenRunsOnlyOnce(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	first, err := store.IssueFirstIngestToken(ctx, IngestTokenActor{})
	if err != nil || !secret.ValidSecret(first, "foi_") {
		t.Fatalf("first = %q err = %v", first, err)
	}
	tokens, err := store.ListActiveIngestTokens(ctx)
	if err != nil || len(tokens) != 1 || tokens[0].Name != "default" {
		t.Fatalf("tokens = %+v err = %v", tokens, err)
	}
	if again, err := store.IssueFirstIngestToken(ctx, IngestTokenActor{}); err != nil || again != "" {
		t.Fatalf("second issue = %q err = %v", again, err)
	}
}

func TestCreateIngestTokenStoresOnlyDigest(t *testing.T) {
	store := newTestStore(t)
	token, plaintext := createTestToken(t, store, "collector-a")
	if !secret.ValidSecret(plaintext, "foi_") {
		t.Fatalf("token format rejected: %d characters", len(plaintext))
	}
	if token.Hint != plaintext[len(plaintext)-4:] {
		t.Fatalf("hint = %q", token.Hint)
	}
	var dump string
	row := store.db.QueryRow(`SELECT id || name || token_hash || hint || source || created_at FROM ingest_tokens`)
	if err := row.Scan(&dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump, plaintext) || !strings.Contains(dump, HashIngestToken(plaintext)) {
		t.Fatal("the row must hold the digest and not the plaintext")
	}
}

func TestMultipleTokensAuthenticateIndependently(t *testing.T) {
	store := newTestStore(t)
	first, firstPlain := createTestToken(t, store, "collector-a")
	_, secondPlain := createTestToken(t, store, "collector-b")
	if !authenticates(t, store, firstPlain) || !authenticates(t, store, secondPlain) {
		t.Fatal("every active token must authenticate")
	}
	if authenticates(t, store, configuredIngestToken("Z")) {
		t.Fatal("an unknown token authenticated")
	}
	if err := store.RevokeIngestToken(context.Background(), first.ID, IngestTokenActor{}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if authenticates(t, store, firstPlain) {
		t.Fatal("a revoked token authenticated")
	}
	if !authenticates(t, store, secondPlain) {
		t.Fatal("revoking one token stopped another")
	}
}

func TestIngestTokenLookupUsesTheDigestIndex(t *testing.T) {
	store := newTestStore(t)
	rows, err := store.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM ingest_tokens WHERE token_hash = ? AND revoked_at IS NULL`, "x")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "USING INDEX") || !strings.Contains(plan.String(), "token_hash=") {
		t.Fatalf("plan does not use the digest index: %s", plan.String())
	}
}

func TestRevokeIngestTokenIsNotRepeatable(t *testing.T) {
	store := newTestStore(t)
	token, _ := createTestToken(t, store, "collector-a")
	ctx := context.Background()
	if err := store.RevokeIngestToken(ctx, token.ID, IngestTokenActor{}); err != nil {
		t.Fatalf("first revoke: %v", err)
	}
	if err := store.RevokeIngestToken(ctx, token.ID, IngestTokenActor{}); !errors.Is(err, ErrIngestTokenNotFound) {
		t.Fatalf("second revoke = %v, want not found", err)
	}
	if err := store.RevokeIngestToken(ctx, "missing", IngestTokenActor{}); !errors.Is(err, ErrIngestTokenNotFound) {
		t.Fatalf("unknown revoke = %v, want not found", err)
	}
	var revokedEvents int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM auth_audit_events WHERE event_type = 'ingest_token.revoked'`).Scan(&revokedEvents); err != nil || revokedEvents != 1 {
		t.Fatalf("revoked events = %d err = %v", revokedEvents, err)
	}
	if got := ingestTokenRowCount(t, store); got != 1 {
		t.Fatalf("rows = %d, want the revoked row retained", got)
	}
}

func TestIngestTokenAuditRecordsNameAndIDWithoutValue(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	token, plaintext := createTestToken(t, store, "collector-a")
	if err := store.RevokeIngestToken(ctx, token.ID, IngestTokenActor{}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	rows, err := store.db.Query(`SELECT event_type, target_type, target_id, metadata_json FROM auth_audit_events ORDER BY created_at, event_type`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var eventType, targetType, targetID, metadata string
		if err := rows.Scan(&eventType, &targetType, &targetID, &metadata); err != nil {
			t.Fatal(err)
		}
		if targetType != "ingest_token" || targetID != token.ID || !strings.Contains(metadata, `"collector-a"`) {
			t.Fatalf("%s: target %s/%s metadata %s", eventType, targetType, targetID, metadata)
		}
		if strings.Contains(metadata, plaintext) || strings.Contains(metadata, HashIngestToken(plaintext)) {
			t.Fatalf("%s audit metadata exposed token material", eventType)
		}
		seen[eventType] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !seen["ingest_token.created"] || !seen["ingest_token.revoked"] {
		t.Fatalf("audit events = %v", seen)
	}
}

func TestCreateIngestTokenValidatesName(t *testing.T) {
	store := newTestStore(t)
	for _, name := range []string{"", "   ", strings.Repeat("a", MaxIngestTokenNameLength+1), "line\nbreak", "tab\there", "bad\xff"} {
		if _, _, err := store.CreateIngestToken(context.Background(), name, IngestTokenActor{}); !errors.Is(err, ErrInvalidIngestTokenName) {
			t.Errorf("name %q: err = %v", name, err)
		}
	}
	token, _ := createTestToken(t, store, "  edge collector  ")
	if token.Name != "edge collector" {
		t.Fatalf("name = %q, want trimmed", token.Name)
	}
}

func TestIngestTokenNameUniqueAmongActiveTokens(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	first, _ := createTestToken(t, store, "collector")
	for _, name := range []string{"collector", "COLLECTOR"} {
		if _, _, err := store.CreateIngestToken(ctx, name, IngestTokenActor{}); !errors.Is(err, ErrIngestTokenNameTaken) {
			t.Fatalf("duplicate %q: err = %v", name, err)
		}
	}
	if err := store.RevokeIngestToken(ctx, first.ID, IngestTokenActor{}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := store.CreateIngestToken(ctx, "collector", IngestTokenActor{}); err != nil {
		t.Fatalf("a revoked name must be reusable: %v", err)
	}
}

func TestCreateIngestTokenCapIsCountedOnActiveTokens(t *testing.T) {
	store := newTestStore(t)
	var last IngestToken
	for i := range MaxActiveIngestTokens {
		last, _ = createTestToken(t, store, fmt.Sprintf("collector-%d", i))
	}
	ctx := context.Background()
	if _, _, err := store.CreateIngestToken(ctx, "one-too-many", IngestTokenActor{}); !errors.Is(err, ErrIngestTokenLimitReached) {
		t.Fatalf("err = %v, want limit reached", err)
	}
	if err := store.RevokeIngestToken(ctx, last.ID, IngestTokenActor{}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := store.CreateIngestToken(ctx, "replacement", IngestTokenActor{}); err != nil {
		t.Fatalf("create after revoke: %v", err)
	}
}

func TestConcurrentCreatesStopAtTheCap(t *testing.T) {
	sqlite, err := appstore.NewSQLite(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	store := NewStore(sqlite.DB)
	ctx := context.Background()

	existing := MaxActiveIngestTokens - 5
	for i := range existing {
		createTestToken(t, store, fmt.Sprintf("existing-%d", i))
	}

	const attempts = 40
	var wg sync.WaitGroup
	errs := make([]error, attempts)
	start := make(chan struct{})
	for i := range attempts {
		wg.Go(func() {
			<-start
			_, _, errs[i] = store.CreateIngestToken(ctx, fmt.Sprintf("racer-%d", i), IngestTokenActor{})
		})
	}
	close(start)
	wg.Wait()

	created := 0
	for _, err := range errs {
		switch {
		case err == nil:
			created++
		case errors.Is(err, ErrIngestTokenLimitReached):
		default:
			t.Fatalf("unexpected create error: %v", err)
		}
	}
	if created != 5 {
		t.Fatalf("concurrent creates = %d, want 5", created)
	}
	var active int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM ingest_tokens WHERE revoked_at IS NULL`).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != MaxActiveIngestTokens {
		t.Fatalf("active = %d, want %d", active, MaxActiveIngestTokens)
	}
}

func lastUsedOf(t *testing.T, store *Store, id string) sql.NullString {
	t.Helper()
	var lastUsed sql.NullString
	if err := store.db.QueryRow(`SELECT last_used_at FROM ingest_tokens WHERE id = ?`, id).Scan(&lastUsed); err != nil {
		t.Fatal(err)
	}
	return lastUsed
}

func TestLastUsedIsCoalescedUntilFlush(t *testing.T) {
	store := newTestStore(t)
	token, plaintext := createTestToken(t, store, "collector-a")
	base := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	for i := range 3 {
		store.now = func() time.Time { return base.Add(time.Duration(i) * time.Second) }
		if !authenticates(t, store, plaintext) {
			t.Fatal("token rejected")
		}
	}
	if got := lastUsedOf(t, store, token.ID); got.Valid {
		t.Fatalf("authentication wrote last used %q before a flush", got.String)
	}
	if err := store.flushIngestTokenLastUsed(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if got := lastUsedOf(t, store, token.ID); got.String != "2026-11-08T12:00:02Z" {
		t.Fatalf("last used = %v, want the latest touch", got)
	}
	tokens, err := store.ListActiveIngestTokens(context.Background())
	if err != nil || tokens[0].LastUsedAt == nil || *tokens[0].LastUsedAt != "2026-11-08T12:00:02Z" {
		t.Fatalf("listed = %+v err = %v", tokens, err)
	}
}

func TestLastUsedFlushKeepsTimesWhenWriteFails(t *testing.T) {
	store := newTestStore(t)
	token, plaintext := createTestToken(t, store, "collector-a")
	if !authenticates(t, store, plaintext) {
		t.Fatal("token rejected")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.flushIngestTokenLastUsed(cancelled); err == nil {
		t.Fatal("a cancelled flush must fail")
	}
	if err := store.flushIngestTokenLastUsed(context.Background()); err != nil {
		t.Fatalf("retry flush: %v", err)
	}
	if got := lastUsedOf(t, store, token.ID); !got.Valid {
		t.Fatal("the failed batch was lost")
	}
}

func TestLastUsedRestoreKeepsNewerTime(t *testing.T) {
	var buffer lastUsedBuffer
	older := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	newer := older.Add(time.Minute)
	buffer.touch("a", newer)
	buffer.restore(map[string]time.Time{"a": older, "b": older})
	got := buffer.drain()
	if !got["a"].Equal(newer) || !got["b"].Equal(older) {
		t.Fatalf("drained = %v", got)
	}
}

func TestFlusherWritesOnIntervalAndOnStop(t *testing.T) {
	sqlite, err := appstore.NewSQLite(filepath.Join(t.TempDir(), "control.sqlite"))
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })

	onStop := NewStore(sqlite.DB)
	onStop.lastUsedFlushInterval = time.Hour
	first, firstPlain := createTestToken(t, onStop, "shutdown")
	stop := onStop.StartIngestTokenLastUsedFlusher()
	if !authenticates(t, onStop, firstPlain) {
		t.Fatal("token rejected")
	}
	if got := lastUsedOf(t, onStop, first.ID); got.Valid {
		t.Fatal("an hour-long interval must not have flushed yet")
	}
	stop()
	if got := lastUsedOf(t, onStop, first.ID); !got.Valid {
		t.Fatal("shutdown did not flush")
	}

	ticking := NewStore(sqlite.DB)
	ticking.lastUsedFlushInterval = 5 * time.Millisecond
	second, secondPlain := createTestToken(t, ticking, "ticking")
	stopTicking := ticking.StartIngestTokenLastUsedFlusher()
	defer stopTicking()
	if !authenticates(t, ticking, secondPlain) {
		t.Fatal("token rejected")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !lastUsedOf(t, ticking, second.ID).Valid {
		if time.Now().After(deadline) {
			t.Fatal("the flusher never wrote last used")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestIngestTokenLifecycleDoesNotLogTokenMaterial(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	store := newTestStore(t)
	ctx := context.Background()
	configured := configuredIngestToken("A")
	if _, err := store.ApplyConfiguredIngestToken(ctx, configured); err != nil {
		t.Fatalf("seed: %v", err)
	}
	created, plaintext := createTestToken(t, store, "collector-a")
	if !authenticates(t, store, plaintext) {
		t.Fatal("token rejected")
	}
	if err := store.flushIngestTokenLastUsed(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if err := store.RevokeIngestToken(ctx, created.ID, IngestTokenActor{}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	for _, leaked := range []string{configured, plaintext, HashIngestToken(configured), HashIngestToken(plaintext)} {
		if strings.Contains(logs.String(), leaked) {
			t.Fatal("logs exposed token material")
		}
	}
}
