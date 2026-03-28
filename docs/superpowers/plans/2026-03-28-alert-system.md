# Alert System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rule-based alerting with expr-lang expressions, SQLite state, webhooks, and MCP management.

**Architecture:** Background eval loop (30s) queries DuckDB rollups for all services, evaluates each rule via expr-lang, manages alert state in SQLite, fires webhooks async. Three MCP tools for rule CRUD, alert viewing, and expression building.

**Tech Stack:** `expr-lang/expr` (expression engine), `modernc.org/sqlite` (pure Go SQLite), `google/uuid` (already in go.mod), Go `text/template` (webhook body templates)

**Spec:** `docs/superpowers/specs/2026-03-27-alert-system-design.md`

---

## File Structure

```
internal/store/
    store.go           — SQLite connection, migrations, Close()

internal/alert/
    types.go           — Rule, Alert structs
    store.go           — CRUD operations on alert_rules and alerts tables
    store_test.go      — Store unit tests with in-memory SQLite
    expr.go            — AlertEnv struct, Compile, safeEval
    expr_test.go       — Expression compilation + eval tests
    engine.go          — Eval loop, buildEnvs, transition logic
    engine_test.go     — Engine tests with mock data
    webhook.go         — HTTP webhook execution, template rendering, retries
    webhook_test.go    — Webhook tests with httptest server

internal/mcp/
    tool_alerts.go     — alert_rules, alerts, alert_env MCP tool handlers

internal/config/
    config.go          — (modify) add AlertEnabled, AlertEvalInterval, AlertHistoryDays

cmd/fanout/
    main.go            — (modify) init SQLite store, alert engine, wire into MCP server
```

---

### Task 1: Add Dependencies

**Files:**
- Modify: `go.mod`

- [ ] **Step 1: Add expr-lang and modernc sqlite**

```bash
go get github.com/expr-lang/expr@latest
go get modernc.org/sqlite@latest
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: Success

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add expr-lang/expr and modernc.org/sqlite"
```

---

### Task 2: SQLite Store Layer

**Files:**
- Create: `internal/store/store.go`
- Create: `internal/store/store_test.go`

- [ ] **Step 1: Write the test**

```go
// internal/store/store_test.go
package store

import "testing"

func TestNewSQLite_InMemory(t *testing.T) {
	db, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite(:memory:) error = %v", err)
	}
	defer db.Close()

	// Verify tables exist
	tables := []string{"alert_rules", "alerts"}
	for _, table := range tables {
		var name string
		err := db.DB.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}
}

func TestNewSQLite_WALMode(t *testing.T) {
	db, err := NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite error = %v", err)
	}
	defer db.Close()

	var mode string
	db.DB.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if mode != "wal" && mode != "memory" {
		// :memory: may not support WAL; file-based DBs should use WAL
		t.Logf("journal_mode = %q (memory DBs may not use WAL)", mode)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: Implement the store**

```go
// internal/store/store.go
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// SQLite wraps a SQLite database connection for application state.
type SQLite struct {
	DB *sql.DB
}

// NewSQLite opens (or creates) a SQLite database and runs migrations.
// Pass ":memory:" for in-memory testing or a file path for persistence.
func NewSQLite(dbPath string) (*SQLite, error) {
	dsn := dbPath
	if dbPath != ":memory:" {
		dsn = dbPath + "?_journal_mode=wal&_busy_timeout=5000"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	s := &SQLite{DB: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *SQLite) migrate() error {
	migrations := []string{
		`CREATE TABLE IF NOT EXISTS alert_rules (
			id                TEXT PRIMARY KEY,
			name              TEXT NOT NULL,
			description       TEXT,
			enabled           INTEGER DEFAULT 1,
			service           TEXT,
			namespace         TEXT DEFAULT '',
			expression        TEXT NOT NULL,
			for_seconds       INTEGER DEFAULT 60,
			cooldown_s        INTEGER DEFAULT 600,
			repeat_interval_s INTEGER DEFAULT 3600,
			webhook_url       TEXT,
			webhook_headers   TEXT,
			webhook_template  TEXT,
			notify_on_resolve INTEGER DEFAULT 0,
			created_at        TEXT DEFAULT (datetime('now')),
			updated_at        TEXT DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS alerts (
			id                    TEXT PRIMARY KEY,
			rule_id               TEXT NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
			service               TEXT NOT NULL,
			state                 TEXT NOT NULL,
			value                 REAL,
			fired_at              TEXT,
			resolved_at           TEXT,
			repeated_at           TEXT,
			last_eval             TEXT,
			last_delivery_status  TEXT,
			last_delivery_at      TEXT,
			created_at            TEXT DEFAULT (datetime('now')),
			UNIQUE(rule_id, service)
		)`,
	}
	// Enable foreign keys
	if _, err := s.DB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	for _, m := range migrations {
		if _, err := s.DB.Exec(m); err != nil {
			return fmt.Errorf("migration: %w", err)
		}
	}
	return nil
}

// Close closes the database connection.
func (s *SQLite) Close() error {
	return s.DB.Close()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(alert): add SQLite store layer with migrations"
```

---

### Task 3: Alert Types

**Files:**
- Create: `internal/alert/types.go`

- [ ] **Step 1: Create types**

```go
// internal/alert/types.go
package alert

import "time"

// Rule defines an alert rule with its webhook config.
type Rule struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description,omitempty"`
	Enabled           bool   `json:"enabled"`
	Service           string `json:"service,omitempty"`
	Namespace         string `json:"namespace,omitempty"`
	Expression        string `json:"expression"`
	ForSeconds        int    `json:"for_seconds"`
	CooldownS         int    `json:"cooldown_s"`
	RepeatIntervalS   int    `json:"repeat_interval_s"`
	WebhookURL        string `json:"webhook_url,omitempty"`
	WebhookHeaders    string `json:"webhook_headers,omitempty"`
	WebhookTemplate   string `json:"webhook_template,omitempty"`
	NotifyOnResolve   bool   `json:"notify_on_resolve"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

// Alert represents a current or historical alert instance.
type Alert struct {
	ID                   string  `json:"id"`
	RuleID               string  `json:"rule_id"`
	Service              string  `json:"service"`
	State                string  `json:"state"` // pending, firing, resolved
	Value                float64 `json:"value,omitempty"`
	FiredAt              string  `json:"fired_at,omitempty"`
	ResolvedAt           string  `json:"resolved_at,omitempty"`
	RepeatedAt           string  `json:"repeated_at,omitempty"`
	LastEval             string  `json:"last_eval,omitempty"`
	LastDeliveryStatus   string  `json:"last_delivery_status,omitempty"`
	LastDeliveryAt       string  `json:"last_delivery_at,omitempty"`
	CreatedAt            string  `json:"created_at"`
}

// AlertEnv is the data available to expr-lang rule expressions.
type AlertEnv struct {
	ErrorRate       float64 `expr:"error_rate"`
	P50             float64 `expr:"p50"`
	P95             float64 `expr:"p95"`
	P99             float64 `expr:"p99"`
	Throughput      float64 `expr:"throughput"`
	LogCount        float64 `expr:"log_count"`
	ZScore          float64 `expr:"z_score"`
	HealthScore     float64 `expr:"health_score"`
	ErrorRateDelta  float64 `expr:"error_rate_delta"`
	P95Delta        float64 `expr:"p95_delta"`
	ThroughputDelta float64 `expr:"throughput_delta"`
	Service         string  `expr:"service"`
	Namespace       string  `expr:"namespace"`
}

// AlertSummary is returned by the alerts MCP tool.
type AlertSummary struct {
	Firing   int `json:"firing"`
	Pending  int `json:"pending"`
	Resolved int `json:"resolved"`
}

// ActionContext provides template variables for webhook body rendering.
type ActionContext struct {
	Rule  Rule     `json:"rule"`
	Alert Alert    `json:"alert"`
	Env   AlertEnv `json:"env"`
	Event string   `json:"event"` // "fire", "resolve", "reminder"
	Time  time.Time `json:"time"`
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/alert/`
Expected: Success

- [ ] **Step 3: Commit**

```bash
git add internal/alert/types.go
git commit -m "feat(alert): add Rule, Alert, AlertEnv types"
```

---

### Task 4: Alert Store — CRUD Operations

**Files:**
- Create: `internal/alert/store.go`
- Create: `internal/alert/store_test.go`

- [ ] **Step 1: Write the tests**

```go
// internal/alert/store_test.go
package alert

import (
	"testing"

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

func TestStore_CreateAndGetRule(t *testing.T) {
	s := newTestStore(t)

	rule := Rule{
		Name:       "high errors",
		Expression: "error_rate > 0.05",
		Service:    "checkout",
		ForSeconds: 60,
		CooldownS:  600,
		WebhookURL: "https://hooks.slack.com/test",
	}
	created, err := s.CreateRule(rule)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created rule has no ID")
	}
	if created.Name != "high errors" {
		t.Errorf("Name = %q, want %q", created.Name, "high errors")
	}
	if !created.Enabled {
		t.Error("new rule should be enabled by default")
	}

	got, err := s.GetRule(created.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got.Expression != "error_rate > 0.05" {
		t.Errorf("Expression = %q, want %q", got.Expression, "error_rate > 0.05")
	}
}

func TestStore_ListRules(t *testing.T) {
	s := newTestStore(t)

	s.CreateRule(Rule{Name: "rule1", Expression: "error_rate > 0.1"})
	s.CreateRule(Rule{Name: "rule2", Expression: "p95 > 1000"})

	rules, err := s.ListRules()
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 2 {
		t.Errorf("ListRules count = %d, want 2", len(rules))
	}
}

func TestStore_UpdateRule(t *testing.T) {
	s := newTestStore(t)

	created, _ := s.CreateRule(Rule{Name: "test", Expression: "error_rate > 0.05"})

	created.Name = "updated"
	created.Enabled = false
	updated, err := s.UpdateRule(created)
	if err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	if updated.Name != "updated" {
		t.Errorf("Name = %q, want %q", updated.Name, "updated")
	}
	if updated.Enabled {
		t.Error("should be disabled")
	}
}

func TestStore_DeleteRule(t *testing.T) {
	s := newTestStore(t)

	created, _ := s.CreateRule(Rule{Name: "test", Expression: "error_rate > 0.05"})

	err := s.DeleteRule(created.ID)
	if err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}

	_, err = s.GetRule(created.ID)
	if err == nil {
		t.Error("GetRule should fail after delete")
	}
}

func TestStore_ListEnabledRules(t *testing.T) {
	s := newTestStore(t)

	r1, _ := s.CreateRule(Rule{Name: "enabled", Expression: "error_rate > 0.05"})
	r2, _ := s.CreateRule(Rule{Name: "disabled", Expression: "p95 > 1000"})
	r2.Enabled = false
	s.UpdateRule(r2)
	_ = r1

	rules, err := s.ListEnabledRules()
	if err != nil {
		t.Fatalf("ListEnabledRules: %v", err)
	}
	if len(rules) != 1 {
		t.Errorf("ListEnabledRules count = %d, want 1", len(rules))
	}
	if rules[0].Name != "enabled" {
		t.Errorf("Name = %q, want %q", rules[0].Name, "enabled")
	}
}

func TestStore_UpsertAndGetAlert(t *testing.T) {
	s := newTestStore(t)

	rule, _ := s.CreateRule(Rule{Name: "test", Expression: "error_rate > 0.05"})

	alert := Alert{
		RuleID:  rule.ID,
		Service: "checkout",
		State:   "pending",
		Value:   0.08,
	}
	created, err := s.UpsertAlert(alert)
	if err != nil {
		t.Fatalf("UpsertAlert: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created alert has no ID")
	}
	if created.State != "pending" {
		t.Errorf("State = %q, want pending", created.State)
	}

	// Upsert again (same rule+service) should update
	alert.State = "firing"
	alert.ID = created.ID
	updated, err := s.UpsertAlert(alert)
	if err != nil {
		t.Fatalf("UpsertAlert update: %v", err)
	}
	if updated.ID != created.ID {
		t.Errorf("ID changed on upsert: %q != %q", updated.ID, created.ID)
	}
	if updated.State != "firing" {
		t.Errorf("State = %q, want firing", updated.State)
	}
}

func TestStore_ListAlerts(t *testing.T) {
	s := newTestStore(t)

	rule, _ := s.CreateRule(Rule{Name: "test", Expression: "error_rate > 0.05"})
	s.UpsertAlert(Alert{RuleID: rule.ID, Service: "svc1", State: "firing"})
	s.UpsertAlert(Alert{RuleID: rule.ID, Service: "svc2", State: "pending"})

	all, err := s.ListAlerts("", "", "")
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("ListAlerts count = %d, want 2", len(all))
	}

	firing, err := s.ListAlerts("firing", "", "")
	if err != nil {
		t.Fatalf("ListAlerts(firing): %v", err)
	}
	if len(firing) != 1 {
		t.Errorf("ListAlerts(firing) count = %d, want 1", len(firing))
	}
}

func TestStore_DeleteAlert(t *testing.T) {
	s := newTestStore(t)

	rule, _ := s.CreateRule(Rule{Name: "test", Expression: "error_rate > 0.05"})
	alert, _ := s.UpsertAlert(Alert{RuleID: rule.ID, Service: "svc1", State: "pending"})

	err := s.DeleteAlert(alert.ID)
	if err != nil {
		t.Fatalf("DeleteAlert: %v", err)
	}

	alerts, _ := s.ListAlerts("", "", "")
	if len(alerts) != 0 {
		t.Errorf("ListAlerts count = %d, want 0 after delete", len(alerts))
	}
}

func TestStore_DeleteRule_CascadesAlerts(t *testing.T) {
	s := newTestStore(t)

	rule, _ := s.CreateRule(Rule{Name: "test", Expression: "error_rate > 0.05"})
	s.UpsertAlert(Alert{RuleID: rule.ID, Service: "svc1", State: "firing"})

	s.DeleteRule(rule.ID)

	alerts, _ := s.ListAlerts("", "", "")
	if len(alerts) != 0 {
		t.Errorf("alerts should be cascade-deleted, got %d", len(alerts))
	}
}

func TestStore_AlertSummary(t *testing.T) {
	s := newTestStore(t)

	rule, _ := s.CreateRule(Rule{Name: "test", Expression: "error_rate > 0.05"})
	s.UpsertAlert(Alert{RuleID: rule.ID, Service: "svc1", State: "firing"})
	s.UpsertAlert(Alert{RuleID: rule.ID, Service: "svc2", State: "pending"})
	s.UpsertAlert(Alert{RuleID: rule.ID, Service: "svc3", State: "resolved"})

	summary, err := s.AlertSummary()
	if err != nil {
		t.Fatalf("AlertSummary: %v", err)
	}
	if summary.Firing != 1 {
		t.Errorf("Firing = %d, want 1", summary.Firing)
	}
	if summary.Pending != 1 {
		t.Errorf("Pending = %d, want 1", summary.Pending)
	}
	if summary.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1", summary.Resolved)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/alert/ -v`
Expected: FAIL — `NewStore` not defined

- [ ] **Step 3: Implement the alert store**

```go
// internal/alert/store.go
package alert

import (
	"database/sql"
	"fmt"

	"github.com/google/uuid"
)

// Store provides CRUD operations on alert_rules and alerts tables.
type Store struct {
	db *sql.DB
}

// NewStore creates a new alert store using the given SQLite database.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func newID() string {
	id, _ := uuid.NewV7()
	return id.String()
}

// CreateRule inserts a new alert rule.
func (s *Store) CreateRule(r Rule) (Rule, error) {
	r.ID = newID()
	_, err := s.db.Exec(`
		INSERT INTO alert_rules (id, name, description, enabled, service, namespace,
			expression, for_seconds, cooldown_s, repeat_interval_s,
			webhook_url, webhook_headers, webhook_template, notify_on_resolve)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Name, r.Description, true, r.Service, r.Namespace,
		r.Expression, r.ForSeconds, r.CooldownS, r.RepeatIntervalS,
		r.WebhookURL, r.WebhookHeaders, r.WebhookTemplate, r.NotifyOnResolve,
	)
	if err != nil {
		return Rule{}, fmt.Errorf("create rule: %w", err)
	}
	return s.GetRule(r.ID)
}

// GetRule returns a single rule by ID.
func (s *Store) GetRule(id string) (Rule, error) {
	var r Rule
	var enabled, notifyOnResolve int
	var desc, svc, ns, url, headers, tmpl sql.NullString
	err := s.db.QueryRow(`
		SELECT id, name, description, enabled, service, namespace,
			expression, for_seconds, cooldown_s, repeat_interval_s,
			webhook_url, webhook_headers, webhook_template, notify_on_resolve,
			created_at, updated_at
		FROM alert_rules WHERE id = ?`, id,
	).Scan(&r.ID, &r.Name, &desc, &enabled, &svc, &ns,
		&r.Expression, &r.ForSeconds, &r.CooldownS, &r.RepeatIntervalS,
		&url, &headers, &tmpl, &notifyOnResolve,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return Rule{}, fmt.Errorf("get rule %s: %w", id, err)
	}
	r.Enabled = enabled == 1
	r.NotifyOnResolve = notifyOnResolve == 1
	r.Description = desc.String
	r.Service = svc.String
	r.Namespace = ns.String
	r.WebhookURL = url.String
	r.WebhookHeaders = headers.String
	r.WebhookTemplate = tmpl.String
	return r, nil
}

// ListRules returns all rules.
func (s *Store) ListRules() ([]Rule, error) {
	return s.queryRules("SELECT id, name, description, enabled, service, namespace, expression, for_seconds, cooldown_s, repeat_interval_s, webhook_url, webhook_headers, webhook_template, notify_on_resolve, created_at, updated_at FROM alert_rules ORDER BY created_at DESC")
}

// ListEnabledRules returns only enabled rules.
func (s *Store) ListEnabledRules() ([]Rule, error) {
	return s.queryRules("SELECT id, name, description, enabled, service, namespace, expression, for_seconds, cooldown_s, repeat_interval_s, webhook_url, webhook_headers, webhook_template, notify_on_resolve, created_at, updated_at FROM alert_rules WHERE enabled = 1 ORDER BY created_at DESC")
}

func (s *Store) queryRules(query string, args ...any) ([]Rule, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query rules: %w", err)
	}
	defer rows.Close()

	var rules []Rule
	for rows.Next() {
		var r Rule
		var enabled, notifyOnResolve int
		var desc, svc, ns, url, headers, tmpl sql.NullString
		if err := rows.Scan(&r.ID, &r.Name, &desc, &enabled, &svc, &ns,
			&r.Expression, &r.ForSeconds, &r.CooldownS, &r.RepeatIntervalS,
			&url, &headers, &tmpl, &notifyOnResolve,
			&r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		r.Enabled = enabled == 1
		r.NotifyOnResolve = notifyOnResolve == 1
		r.Description = desc.String
		r.Service = svc.String
		r.Namespace = ns.String
		r.WebhookURL = url.String
		r.WebhookHeaders = headers.String
		r.WebhookTemplate = tmpl.String
		rules = append(rules, r)
	}
	if rules == nil {
		rules = []Rule{}
	}
	return rules, rows.Err()
}

// UpdateRule updates an existing rule.
func (s *Store) UpdateRule(r Rule) (Rule, error) {
	enabled := 0
	if r.Enabled {
		enabled = 1
	}
	notifyOnResolve := 0
	if r.NotifyOnResolve {
		notifyOnResolve = 1
	}
	_, err := s.db.Exec(`
		UPDATE alert_rules SET
			name = ?, description = ?, enabled = ?, service = ?, namespace = ?,
			expression = ?, for_seconds = ?, cooldown_s = ?, repeat_interval_s = ?,
			webhook_url = ?, webhook_headers = ?, webhook_template = ?,
			notify_on_resolve = ?, updated_at = datetime('now')
		WHERE id = ?`,
		r.Name, r.Description, enabled, r.Service, r.Namespace,
		r.Expression, r.ForSeconds, r.CooldownS, r.RepeatIntervalS,
		r.WebhookURL, r.WebhookHeaders, r.WebhookTemplate,
		notifyOnResolve, r.ID,
	)
	if err != nil {
		return Rule{}, fmt.Errorf("update rule: %w", err)
	}
	return s.GetRule(r.ID)
}

// DeleteRule removes a rule and its alerts (via CASCADE).
func (s *Store) DeleteRule(id string) error {
	_, err := s.db.Exec("DELETE FROM alert_rules WHERE id = ?", id)
	return err
}

// UpsertAlert inserts or updates an alert (keyed by rule_id + service).
func (s *Store) UpsertAlert(a Alert) (Alert, error) {
	if a.ID == "" {
		a.ID = newID()
	}
	_, err := s.db.Exec(`
		INSERT INTO alerts (id, rule_id, service, state, value, fired_at, resolved_at, repeated_at, last_eval, last_delivery_status, last_delivery_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, datetime('now'), ?, ?)
		ON CONFLICT(rule_id, service) DO UPDATE SET
			state = excluded.state,
			value = excluded.value,
			fired_at = COALESCE(excluded.fired_at, fired_at),
			resolved_at = excluded.resolved_at,
			repeated_at = excluded.repeated_at,
			last_eval = datetime('now'),
			last_delivery_status = COALESCE(excluded.last_delivery_status, last_delivery_status),
			last_delivery_at = COALESCE(excluded.last_delivery_at, last_delivery_at)`,
		a.ID, a.RuleID, a.Service, a.State, a.Value,
		a.FiredAt, a.ResolvedAt, a.RepeatedAt,
		a.LastDeliveryStatus, a.LastDeliveryAt,
	)
	if err != nil {
		return Alert{}, fmt.Errorf("upsert alert: %w", err)
	}
	return s.GetAlert(a.RuleID, a.Service)
}

// GetAlert returns an alert by rule_id + service.
func (s *Store) GetAlert(ruleID, service string) (Alert, error) {
	var a Alert
	var value sql.NullFloat64
	var firedAt, resolvedAt, repeatedAt, lastEval, deliveryStatus, deliveryAt sql.NullString
	err := s.db.QueryRow(`
		SELECT id, rule_id, service, state, value, fired_at, resolved_at, repeated_at,
			last_eval, last_delivery_status, last_delivery_at, created_at
		FROM alerts WHERE rule_id = ? AND service = ?`, ruleID, service,
	).Scan(&a.ID, &a.RuleID, &a.Service, &a.State, &value,
		&firedAt, &resolvedAt, &repeatedAt, &lastEval,
		&deliveryStatus, &deliveryAt, &a.CreatedAt,
	)
	if err != nil {
		return Alert{}, err
	}
	a.Value = value.Float64
	a.FiredAt = firedAt.String
	a.ResolvedAt = resolvedAt.String
	a.RepeatedAt = repeatedAt.String
	a.LastEval = lastEval.String
	a.LastDeliveryStatus = deliveryStatus.String
	a.LastDeliveryAt = deliveryAt.String
	return a, nil
}

// ListAlerts returns alerts filtered by state, service, and/or rule_id.
// Pass empty strings to skip filters.
func (s *Store) ListAlerts(state, service, ruleID string) ([]Alert, error) {
	query := "SELECT id, rule_id, service, state, value, fired_at, resolved_at, repeated_at, last_eval, last_delivery_status, last_delivery_at, created_at FROM alerts WHERE 1=1"
	var args []any
	if state != "" {
		query += " AND state = ?"
		args = append(args, state)
	}
	if service != "" {
		query += " AND service = ?"
		args = append(args, service)
	}
	if ruleID != "" {
		query += " AND rule_id = ?"
		args = append(args, ruleID)
	}
	query += " ORDER BY created_at DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list alerts: %w", err)
	}
	defer rows.Close()

	var alerts []Alert
	for rows.Next() {
		var a Alert
		var value sql.NullFloat64
		var firedAt, resolvedAt, repeatedAt, lastEval, deliveryStatus, deliveryAt sql.NullString
		if err := rows.Scan(&a.ID, &a.RuleID, &a.Service, &a.State, &value,
			&firedAt, &resolvedAt, &repeatedAt, &lastEval,
			&deliveryStatus, &deliveryAt, &a.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan alert: %w", err)
		}
		a.Value = value.Float64
		a.FiredAt = firedAt.String
		a.ResolvedAt = resolvedAt.String
		a.RepeatedAt = repeatedAt.String
		a.LastEval = lastEval.String
		a.LastDeliveryStatus = deliveryStatus.String
		a.LastDeliveryAt = deliveryAt.String
		alerts = append(alerts, a)
	}
	if alerts == nil {
		alerts = []Alert{}
	}
	return alerts, rows.Err()
}

// DeleteAlert removes an alert by ID.
func (s *Store) DeleteAlert(id string) error {
	_, err := s.db.Exec("DELETE FROM alerts WHERE id = ?", id)
	return err
}

// AlertSummary returns counts by state.
func (s *Store) AlertSummary() (AlertSummary, error) {
	var summary AlertSummary
	rows, err := s.db.Query("SELECT state, COUNT(*) FROM alerts GROUP BY state")
	if err != nil {
		return summary, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return summary, err
		}
		switch state {
		case "firing":
			summary.Firing = count
		case "pending":
			summary.Pending = count
		case "resolved":
			summary.Resolved = count
		}
	}
	return summary, rows.Err()
}

// PruneResolved deletes resolved alerts older than the given number of days.
func (s *Store) PruneResolved(days int) (int64, error) {
	result, err := s.db.Exec(
		"DELETE FROM alerts WHERE state = 'resolved' AND resolved_at < datetime('now', ?)",
		fmt.Sprintf("-%d days", days),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/alert/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/alert/store.go internal/alert/store_test.go
git commit -m "feat(alert): add alert store with CRUD operations"
```

---

### Task 5: Expression Compilation + Evaluation

**Files:**
- Create: `internal/alert/expr.go`
- Create: `internal/alert/expr_test.go`

- [ ] **Step 1: Write the tests**

```go
// internal/alert/expr_test.go
package alert

import "testing"

func TestCompileExpression_Valid(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{"simple threshold", "error_rate > 0.05"},
		{"compound", "error_rate > 0.05 && p95 > 1000"},
		{"rate of change", "p95_delta > 200"},
		{"anomaly", "z_score > 3.0"},
		{"absence", "throughput < 10"},
		{"complex", "(error_rate > 0.1 || p95 > 2000) && throughput > 100"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CompileExpression(tc.expr)
			if err != nil {
				t.Errorf("CompileExpression(%q) error = %v", tc.expr, err)
			}
		})
	}
}

func TestCompileExpression_Invalid(t *testing.T) {
	tests := []struct {
		name string
		expr string
	}{
		{"unknown field", "unknown_field > 0.05"},
		{"non-boolean", "error_rate + 1"},
		{"syntax error", "error_rate >"},
		{"empty", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CompileExpression(tc.expr)
			if err == nil {
				t.Errorf("CompileExpression(%q) should fail", tc.expr)
			}
		})
	}
}

func TestEvalExpression(t *testing.T) {
	prog, err := CompileExpression("error_rate > 0.05 && p95 > 500")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}

	tests := []struct {
		name string
		env  AlertEnv
		want bool
	}{
		{
			name: "both conditions met",
			env:  AlertEnv{ErrorRate: 0.08, P95: 1000},
			want: true,
		},
		{
			name: "error rate below threshold",
			env:  AlertEnv{ErrorRate: 0.01, P95: 1000},
			want: false,
		},
		{
			name: "p95 below threshold",
			env:  AlertEnv{ErrorRate: 0.08, P95: 200},
			want: false,
		},
		{
			name: "both below",
			env:  AlertEnv{ErrorRate: 0.01, P95: 200},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvalExpression(prog, tc.env)
			if err != nil {
				t.Fatalf("eval: %v", err)
			}
			if got != tc.want {
				t.Errorf("eval = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSafeEval_NoPanic(t *testing.T) {
	prog, _ := CompileExpression("error_rate > 0.05")
	result, err := SafeEval(prog, AlertEnv{ErrorRate: 0.1})
	if err != nil {
		t.Fatalf("SafeEval: %v", err)
	}
	if !result {
		t.Error("expected true")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/alert/ -run TestCompile -v`
Expected: FAIL — `CompileExpression` not defined

- [ ] **Step 3: Implement expr.go**

```go
// internal/alert/expr.go
package alert

import (
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// CompileExpression compiles an expr-lang expression, type-checking against AlertEnv.
// Returns a compiled program that can be cached and reused across goroutines.
func CompileExpression(expression string) (*vm.Program, error) {
	if expression == "" {
		return nil, fmt.Errorf("expression is empty")
	}
	return expr.Compile(
		expression,
		expr.Env(AlertEnv{}),
		expr.AsBool(),
	)
}

// EvalExpression evaluates a compiled expression against an AlertEnv.
func EvalExpression(prog *vm.Program, env AlertEnv) (bool, error) {
	result, err := expr.Run(prog, env)
	if err != nil {
		return false, fmt.Errorf("eval: %w", err)
	}
	return result.(bool), nil
}

// SafeEval wraps EvalExpression with panic recovery.
func SafeEval(prog *vm.Program, env AlertEnv) (result bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("expression eval panic", "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
			err = fmt.Errorf("eval panic: %v", r)
		}
	}()
	return EvalExpression(prog, env)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/alert/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/alert/expr.go internal/alert/expr_test.go
git commit -m "feat(alert): add expression compilation and evaluation with expr-lang"
```

---

### Task 6: Webhook Execution

**Files:**
- Create: `internal/alert/webhook.go`
- Create: `internal/alert/webhook_test.go`

- [ ] **Step 1: Write the tests**

```go
// internal/alert/webhook_test.go
package alert

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRenderTemplate_Basic(t *testing.T) {
	ctx := ActionContext{
		Rule:  Rule{Name: "high errors"},
		Alert: Alert{Service: "checkout", State: "firing", Value: 0.08},
		Env:   AlertEnv{ErrorRate: 0.08, P95: 1200},
		Event: "fire",
	}
	tmpl := `{{.Alert.Service}}: {{.Rule.Name}} ({{.Event}})`
	got, err := RenderTemplate(tmpl, ctx)
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	want := "checkout: high errors (fire)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRenderTemplate_Empty(t *testing.T) {
	ctx := ActionContext{Alert: Alert{Service: "svc"}, Event: "fire"}
	got, err := RenderTemplate("", ctx)
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	if got == "" {
		t.Error("empty template should produce default body")
	}
}

func TestFireWebhook_Success(t *testing.T) {
	var called atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rule := Rule{WebhookURL: srv.URL}
	ctx := ActionContext{Rule: rule, Alert: Alert{Service: "svc"}, Event: "fire"}

	status, err := FireWebhook(rule, ctx)
	if err != nil {
		t.Fatalf("FireWebhook: %v", err)
	}
	if status != "success" {
		t.Errorf("status = %q, want success", status)
	}
	if called.Load() != 1 {
		t.Errorf("called = %d, want 1", called.Load())
	}
}

func TestFireWebhook_Retries5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rule := Rule{WebhookURL: srv.URL}
	ctx := ActionContext{Rule: rule, Alert: Alert{Service: "svc"}, Event: "fire"}

	status, err := FireWebhook(rule, ctx)
	if err != nil {
		t.Fatalf("FireWebhook: %v", err)
	}
	if status != "success" {
		t.Errorf("status = %q, want success", status)
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, want 3 (2 retries + success)", calls.Load())
	}
}

func TestFireWebhook_NoRetry4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	rule := Rule{WebhookURL: srv.URL}
	ctx := ActionContext{Rule: rule, Alert: Alert{Service: "svc"}, Event: "fire"}

	status, _ := FireWebhook(rule, ctx)
	if status != "failed" {
		t.Errorf("status = %q, want failed", status)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (no retry on 4xx)", calls.Load())
	}
}

func TestFireWebhook_NoURL(t *testing.T) {
	rule := Rule{} // no webhook_url
	ctx := ActionContext{Rule: rule, Alert: Alert{Service: "svc"}, Event: "fire"}

	status, err := FireWebhook(rule, ctx)
	if err != nil {
		t.Fatalf("FireWebhook: %v", err)
	}
	if status != "skipped" {
		t.Errorf("status = %q, want skipped", status)
	}
}

func TestFireWebhook_CustomHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom") != "test" {
			t.Errorf("X-Custom = %q, want test", r.Header.Get("X-Custom"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rule := Rule{
		WebhookURL:     srv.URL,
		WebhookHeaders: `{"X-Custom": "test"}`,
	}
	ctx := ActionContext{Rule: rule, Alert: Alert{Service: "svc"}, Event: "fire"}

	status, err := FireWebhook(rule, ctx)
	if err != nil {
		t.Fatalf("FireWebhook: %v", err)
	}
	if status != "success" {
		t.Errorf("status = %q, want success", status)
	}
}

func TestFireWebhook_TemplateRendered(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(strings.Builder)
		buf.ReadFrom(r.Body)
		body = buf.String()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rule := Rule{
		WebhookURL:      srv.URL,
		WebhookTemplate: `{"text":"{{.Alert.Service}} is {{.Event}}"}`,
	}
	ctx := ActionContext{Rule: rule, Alert: Alert{Service: "checkout"}, Event: "fire"}

	FireWebhook(rule, ctx)

	want := `{"text":"checkout is fire"}`
	if body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/alert/ -run TestFireWebhook -v`
Expected: FAIL

- [ ] **Step 3: Implement webhook.go**

```go
// internal/alert/webhook.go
package alert

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"text/template"
	"time"
)

var webhookClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		ResponseHeaderTimeout: 5 * time.Second,
	},
}

const defaultTemplate = `{"event":"{{.Event}}","rule":"{{.Rule.Name}}","service":"{{.Alert.Service}}","state":"{{.Alert.State}}"}`

// RenderTemplate renders a webhook body template with the given context.
// Returns a default JSON body if the template is empty.
func RenderTemplate(tmplStr string, ctx ActionContext) (string, error) {
	if tmplStr == "" {
		tmplStr = defaultTemplate
	}
	t, err := template.New("webhook").Parse(tmplStr)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// FireWebhook sends a webhook for the given rule and context.
// Returns "success", "failed", or "skipped" (when no URL configured).
func FireWebhook(rule Rule, ctx ActionContext) (string, error) {
	if rule.WebhookURL == "" {
		return "skipped", nil
	}

	body, err := RenderTemplate(rule.WebhookTemplate, ctx)
	if err != nil {
		slog.Error("webhook template render failed", "rule", rule.Name, "err", err)
		return "failed", err
	}

	// Parse custom headers
	headers := map[string]string{"Content-Type": "application/json"}
	if rule.WebhookHeaders != "" {
		json.Unmarshal([]byte(rule.WebhookHeaders), &headers)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*2) * time.Second)
		}

		req, err := http.NewRequest(http.MethodPost, rule.WebhookURL, strings.NewReader(body))
		if err != nil {
			return "failed", err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := webhookClient.Do(req)
		if err != nil {
			lastErr = err
			slog.Warn("webhook attempt failed", "rule", rule.Name, "attempt", attempt+1, "err", err)
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		if resp.StatusCode < 300 {
			slog.Info("webhook delivered", "rule", rule.Name, "service", ctx.Alert.Service, "event", ctx.Event, "status", resp.StatusCode)
			return "success", nil
		}
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			slog.Warn("webhook 4xx, not retrying", "rule", rule.Name, "status", resp.StatusCode)
			return "failed", nil
		}
		// 5xx — retry
		lastErr = err
		slog.Warn("webhook 5xx, retrying", "rule", rule.Name, "attempt", attempt+1, "status", resp.StatusCode)
	}

	slog.Error("webhook failed after retries", "rule", rule.Name, "err", lastErr)
	return "failed", lastErr
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/alert/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/alert/webhook.go internal/alert/webhook_test.go
git commit -m "feat(alert): add webhook execution with retries and template rendering"
```

---

### Task 7: Alert Engine — Eval Loop + State Machine

**Files:**
- Create: `internal/alert/engine.go`
- Create: `internal/alert/engine_test.go`

- [ ] **Step 1: Write the tests**

```go
// internal/alert/engine_test.go
package alert

import (
	"context"
	"testing"
	"time"

	"github.com/expr-lang/expr/vm"
	appstore "github.com/labstack/fanout/internal/store"
)

func newTestEngine(t *testing.T) (*Engine, *Store) {
	t.Helper()
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })

	store := NewStore(sqlite.DB)
	engine := &Engine{
		store:    store,
		programs: make(map[string]*vm.Program),
		interval: 30 * time.Second,
	}
	return engine, store
}

func TestEngine_Transition_NoneToFiring(t *testing.T) {
	engine, store := newTestEngine(t)

	rule, _ := store.CreateRule(Rule{
		Name:       "test",
		Expression: "error_rate > 0.05",
		Service:    "checkout",
		ForSeconds: 0, // fire immediately
	})

	env := AlertEnv{ErrorRate: 0.08, Service: "checkout"}
	engine.transition(rule, "checkout", true, env)

	alerts, _ := store.ListAlerts("firing", "", "")
	if len(alerts) != 1 {
		t.Fatalf("expected 1 firing alert, got %d", len(alerts))
	}
	if alerts[0].Service != "checkout" {
		t.Errorf("service = %q, want checkout", alerts[0].Service)
	}
}

func TestEngine_Transition_NoneToPending(t *testing.T) {
	engine, store := newTestEngine(t)

	rule, _ := store.CreateRule(Rule{
		Name:       "test",
		Expression: "error_rate > 0.05",
		Service:    "checkout",
		ForSeconds: 60, // must hold for 60s
	})

	env := AlertEnv{ErrorRate: 0.08, Service: "checkout"}
	engine.transition(rule, "checkout", true, env)

	alerts, _ := store.ListAlerts("pending", "", "")
	if len(alerts) != 1 {
		t.Fatalf("expected 1 pending alert, got %d", len(alerts))
	}
}

func TestEngine_Transition_PendingClears(t *testing.T) {
	engine, store := newTestEngine(t)

	rule, _ := store.CreateRule(Rule{
		Name:       "test",
		Expression: "error_rate > 0.05",
		Service:    "checkout",
		ForSeconds: 60,
	})

	env := AlertEnv{ErrorRate: 0.08, Service: "checkout"}
	engine.transition(rule, "checkout", true, env)

	// Condition clears
	engine.transition(rule, "checkout", false, env)

	alerts, _ := store.ListAlerts("", "", "")
	if len(alerts) != 0 {
		t.Errorf("pending alert should be deleted on false, got %d", len(alerts))
	}
}

func TestEngine_Transition_FiringToResolved(t *testing.T) {
	engine, store := newTestEngine(t)

	rule, _ := store.CreateRule(Rule{
		Name:       "test",
		Expression: "error_rate > 0.05",
		Service:    "checkout",
		ForSeconds: 0,
	})

	env := AlertEnv{ErrorRate: 0.08, Service: "checkout"}
	engine.transition(rule, "checkout", true, env)  // → firing
	engine.transition(rule, "checkout", false, env) // → resolved

	alerts, _ := store.ListAlerts("resolved", "", "")
	if len(alerts) != 1 {
		t.Fatalf("expected 1 resolved alert, got %d", len(alerts))
	}
}

func TestEngine_CompileRules(t *testing.T) {
	engine, store := newTestEngine(t)

	store.CreateRule(Rule{Name: "valid", Expression: "error_rate > 0.05"})
	store.CreateRule(Rule{Name: "also valid", Expression: "p95 > 1000"})

	rules, _ := store.ListEnabledRules()
	engine.compileRules(rules)

	if len(engine.programs) != 2 {
		t.Errorf("programs = %d, want 2", len(engine.programs))
	}
}

func TestEngine_ResolveServices_Wildcard(t *testing.T) {
	engine, _ := newTestEngine(t)

	envs := map[string]AlertEnv{
		"checkout": {Service: "checkout"},
		"payment":  {Service: "payment"},
		"api":      {Service: "api"},
	}

	rule := Rule{Service: "*"}
	services := engine.resolveServices(rule, envs)
	if len(services) != 3 {
		t.Errorf("wildcard should match all %d services, got %d", len(envs), len(services))
	}
}

func TestEngine_ResolveServices_Specific(t *testing.T) {
	engine, _ := newTestEngine(t)

	envs := map[string]AlertEnv{
		"checkout": {Service: "checkout"},
		"payment":  {Service: "payment"},
	}

	rule := Rule{Service: "checkout"}
	services := engine.resolveServices(rule, envs)
	if len(services) != 1 || services[0] != "checkout" {
		t.Errorf("expected [checkout], got %v", services)
	}
}

func TestEngine_RunOnce(t *testing.T) {
	engine, store := newTestEngine(t)
	ctx := context.Background()

	store.CreateRule(Rule{
		Name:       "test",
		Expression: "error_rate > 0.05",
		Service:    "checkout",
		ForSeconds: 0,
	})

	// Provide mock envs
	engine.envOverride = map[string]AlertEnv{
		"checkout": {ErrorRate: 0.08, P95: 200, Service: "checkout"},
	}

	engine.evaluateOnce(ctx)

	alerts, _ := store.ListAlerts("firing", "", "")
	if len(alerts) != 1 {
		t.Errorf("expected 1 firing alert after evaluateOnce, got %d", len(alerts))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/alert/ -run TestEngine -v`
Expected: FAIL

- [ ] **Step 3: Implement engine.go**

```go
// internal/alert/engine.go
package alert

import (
	"context"
	"database/sql"
	"log/slog"
	"runtime/debug"
	"sort"
	"time"

	"github.com/expr-lang/expr/vm"
	"github.com/labstack/fanout/internal/intelligence"
	"github.com/labstack/fanout/internal/query"
)

// Engine evaluates alert rules on a periodic loop.
type Engine struct {
	store    *Store
	duck     *query.Duck
	detector *intelligence.Detector
	programs map[string]*vm.Program // rule ID → compiled program
	interval time.Duration
	histDays int

	// For testing — when set, buildEnvs returns this instead of querying DuckDB.
	envOverride map[string]AlertEnv
}

// NewEngine creates a new alert engine.
func NewEngine(store *Store, duck *query.Duck, detector *intelligence.Detector, interval time.Duration, histDays int) *Engine {
	return &Engine{
		store:    store,
		duck:     duck,
		detector: detector,
		programs: make(map[string]*vm.Program),
		interval: interval,
		histDays: histDays,
	}
}

// Run starts the evaluation loop. Blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	slog.Info("alert engine starting", "interval", e.interval)
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	// Run once immediately
	e.safeEvaluateOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			slog.Info("alert engine stopping")
			return
		case <-ticker.C:
			e.safeEvaluateOnce(ctx)
			e.pruneOldAlerts()
		}
	}
}

func (e *Engine) safeEvaluateOnce(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("alert engine panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	e.evaluateOnce(ctx)
}

func (e *Engine) evaluateOnce(ctx context.Context) {
	rules, err := e.store.ListEnabledRules()
	if err != nil {
		slog.Error("alert: list rules failed", "err", err)
		return
	}
	if len(rules) == 0 {
		return
	}

	e.compileRules(rules)

	envs := e.buildEnvs(ctx)
	if len(envs) == 0 {
		return
	}

	for _, rule := range rules {
		prog, ok := e.programs[rule.ID]
		if !ok {
			continue
		}

		services := e.resolveServices(rule, envs)
		for _, svc := range services {
			env := envs[svc]
			result, err := SafeEval(prog, env)
			if err != nil {
				slog.Error("alert eval failed", "rule", rule.Name, "service", svc, "err", err)
				continue
			}
			e.transition(rule, svc, result, env)
		}
	}
}

func (e *Engine) compileRules(rules []Rule) {
	for _, rule := range rules {
		if _, ok := e.programs[rule.ID]; ok {
			continue // already compiled
		}
		prog, err := CompileExpression(rule.Expression)
		if err != nil {
			slog.Error("alert: compile failed", "rule", rule.Name, "expr", rule.Expression, "err", err)
			continue
		}
		e.programs[rule.ID] = prog
	}
}

func (e *Engine) resolveServices(rule Rule, envs map[string]AlertEnv) []string {
	if rule.Service == "" || rule.Service == "*" {
		services := make([]string, 0, len(envs))
		for svc := range envs {
			services = append(services, svc)
		}
		sort.Strings(services)
		return services
	}
	if _, ok := envs[rule.Service]; ok {
		return []string{rule.Service}
	}
	return nil
}

func (e *Engine) transition(rule Rule, svc string, triggered bool, env AlertEnv) {
	existing, err := e.store.GetAlert(rule.ID, svc)
	notFound := err != nil // sql.ErrNoRows or similar

	now := time.Now().UTC().Format(time.RFC3339)

	switch {
	// No alert exists + triggered → create pending or firing
	case notFound && triggered:
		a := Alert{
			RuleID:  rule.ID,
			Service: svc,
			Value:   env.ErrorRate, // primary metric for display
		}
		if rule.ForSeconds == 0 {
			a.State = "firing"
			a.FiredAt = now
			a.LastEval = now
			e.store.UpsertAlert(a)
			e.fireWebhookAsync(rule, a, env, "fire")
		} else {
			a.State = "pending"
			a.LastEval = now
			e.store.UpsertAlert(a)
		}

	// No alert + not triggered → nothing to do
	case notFound && !triggered:
		// no-op

	// Pending + still triggered → check if for_seconds elapsed
	case existing.State == "pending" && triggered:
		existing.Value = env.ErrorRate
		existing.LastEval = now
		createdAt, _ := time.Parse(time.RFC3339, existing.CreatedAt)
		if time.Since(createdAt) >= time.Duration(rule.ForSeconds)*time.Second {
			existing.State = "firing"
			existing.FiredAt = now
			e.store.UpsertAlert(existing)
			e.fireWebhookAsync(rule, existing, env, "fire")
		} else {
			e.store.UpsertAlert(existing)
		}

	// Pending + no longer triggered → false alarm, delete
	case existing.State == "pending" && !triggered:
		e.store.DeleteAlert(existing.ID)

	// Firing + still triggered → check for repeat notification
	case existing.State == "firing" && triggered:
		existing.Value = env.ErrorRate
		existing.LastEval = now
		if rule.RepeatIntervalS > 0 && existing.RepeatedAt != "" {
			lastRepeat, _ := time.Parse(time.RFC3339, existing.RepeatedAt)
			if time.Since(lastRepeat) >= time.Duration(rule.RepeatIntervalS)*time.Second {
				existing.RepeatedAt = now
				e.store.UpsertAlert(existing)
				e.fireWebhookAsync(rule, existing, env, "reminder")
				return
			}
		} else if rule.RepeatIntervalS > 0 && existing.FiredAt != "" {
			firstFired, _ := time.Parse(time.RFC3339, existing.FiredAt)
			if time.Since(firstFired) >= time.Duration(rule.RepeatIntervalS)*time.Second {
				existing.RepeatedAt = now
				e.store.UpsertAlert(existing)
				e.fireWebhookAsync(rule, existing, env, "reminder")
				return
			}
		}
		e.store.UpsertAlert(existing)

	// Firing + no longer triggered → resolved
	case existing.State == "firing" && !triggered:
		existing.State = "resolved"
		existing.ResolvedAt = now
		existing.LastEval = now
		e.store.UpsertAlert(existing)
		if rule.NotifyOnResolve {
			e.fireWebhookAsync(rule, existing, env, "resolve")
		}

	// Resolved alerts are left for pruning
	}
}

func (e *Engine) fireWebhookAsync(rule Rule, alert Alert, env AlertEnv, event string) {
	ctx := ActionContext{
		Rule:  rule,
		Alert: alert,
		Env:   env,
		Event: event,
		Time:  time.Now(),
	}
	go func() {
		status, _ := FireWebhook(rule, ctx)
		// Update delivery status on the alert
		now := time.Now().UTC().Format(time.RFC3339)
		alert.LastDeliveryStatus = status
		alert.LastDeliveryAt = now
		e.store.UpsertAlert(alert)
	}()
}

func (e *Engine) buildEnvs(ctx context.Context) map[string]AlertEnv {
	if e.envOverride != nil {
		return e.envOverride
	}

	if e.duck == nil {
		return nil
	}

	resp := e.duck.ExecuteSQL(ctx, query.SQLRequest{
		Query: `
		WITH current AS (
			SELECT service,
				avg(error_rate) as error_rate,
				avg(p50_ms) as p50, avg(p95_ms) as p95, avg(p99_ms) as p99,
				sum(spans) as throughput, sum(log_count) as log_count
			FROM service_rollup
			WHERE bucket >= (SELECT max(bucket) FROM service_rollup) - INTERVAL '5 minutes'
			GROUP BY service
		),
		previous AS (
			SELECT service,
				avg(error_rate) as error_rate, avg(p95_ms) as p95, sum(spans) as throughput
			FROM service_rollup
			WHERE bucket >= (SELECT max(bucket) FROM service_rollup) - INTERVAL '10 minutes'
			  AND bucket < (SELECT max(bucket) FROM service_rollup) - INTERVAL '5 minutes'
			GROUP BY service
		)
		SELECT c.service, c.error_rate, c.p50, c.p95, c.p99, c.throughput, c.log_count,
			((c.error_rate - p.error_rate) / NULLIF(p.error_rate, 0)) * 100 as error_rate_delta,
			((c.p95 - p.p95) / NULLIF(p.p95, 0)) * 100 as p95_delta,
			((c.throughput - p.throughput) / NULLIF(p.throughput, 0)) * 100 as throughput_delta
		FROM current c LEFT JOIN previous p ON c.service = p.service`,
	})
	if resp.Error != "" {
		slog.Error("alert: buildEnvs query failed", "err", resp.Error)
		return nil
	}

	envs := make(map[string]AlertEnv, len(resp.Results))
	for _, row := range resp.Results {
		svc, _ := row["service"].(string)
		if svc == "" {
			continue
		}
		env := AlertEnv{
			Service:         svc,
			ErrorRate:       toFloat(row["error_rate"]),
			P50:             toFloat(row["p50"]),
			P95:             toFloat(row["p95"]),
			P99:             toFloat(row["p99"]),
			Throughput:      toFloat(row["throughput"]),
			LogCount:        toFloat(row["log_count"]),
			ErrorRateDelta:  toFloat(row["error_rate_delta"]),
			P95Delta:        toFloat(row["p95_delta"]),
			ThroughputDelta: toFloat(row["throughput_delta"]),
		}

		// Enrich with detector z-scores
		if e.detector != nil {
			if snap := e.detector.LatestSnapshot(); snap != nil {
				env.HealthScore = snap.HealthScore
				for _, a := range snap.Anomalies {
					if a.ServiceName == svc && a.ZScore > env.ZScore {
						env.ZScore = a.ZScore
					}
				}
			}
		}

		envs[svc] = env
	}
	return envs
}

func (e *Engine) pruneOldAlerts() {
	if e.histDays <= 0 {
		return
	}
	n, err := e.store.PruneResolved(e.histDays)
	if err != nil {
		slog.Error("alert prune failed", "err", err)
		return
	}
	if n > 0 {
		slog.Info("alert: pruned resolved alerts", "count", n)
	}
}

func toFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	default:
		return 0
	}
}

// RecompileRule forces recompilation of a single rule (after update).
func (e *Engine) RecompileRule(ruleID, expression string) error {
	prog, err := CompileExpression(expression)
	if err != nil {
		return err
	}
	e.programs[ruleID] = prog
	return nil
}

// RemoveRule removes a compiled program (after delete).
func (e *Engine) RemoveRule(ruleID string) {
	delete(e.programs, ruleID)
}

// Store returns the underlying alert store (for MCP tools).
func (e *Engine) Store() *Store { return e.store }

// BuildEnvForService returns the current AlertEnv for a single service (for MCP test action).
func (e *Engine) BuildEnvForService(ctx context.Context, service string) (AlertEnv, bool) {
	envs := e.buildEnvs(ctx)
	env, ok := envs[service]
	return env, ok
}

// BuildAllEnvs returns all current environments (for MCP alert_env tool).
func (e *Engine) BuildAllEnvs(ctx context.Context) map[string]AlertEnv {
	return e.buildEnvs(ctx)
}

// Needed to satisfy sql import for error checking
var _ = sql.ErrNoRows
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/alert/ -v`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/alert/engine.go internal/alert/engine_test.go
git commit -m "feat(alert): add eval loop, state machine, and DuckDB env builder"
```

---

### Task 8: Config + Main Wiring

**Files:**
- Modify: `internal/config/config.go`
- Modify: `cmd/fanout/main.go`
- Modify: `internal/mcp/server.go`

- [ ] **Step 1: Add config fields**

Add to `Config` struct in `internal/config/config.go`:

```go
	// Alerting
	AlertEnabled      bool
	AlertEvalInterval int // seconds
	AlertHistoryDays  int
```

Add to `Load()`:

```go
		AlertEnabled:      getenvBool("ALERT_ENABLED", true),
		AlertEvalInterval: getenvInt("ALERT_EVAL_INTERVAL", 30),
		AlertHistoryDays:  getenvInt("ALERT_HISTORY_DAYS", 7),
```

- [ ] **Step 2: Wire into main.go**

Add imports:

```go
	"github.com/labstack/fanout/internal/alert"
	"github.com/labstack/fanout/internal/store"
```

After the detector startup (around line 92), add:

```go
	// Open SQLite for application state
	sqlite, err := store.NewSQLite(filepath.Join(cfg.LakeDir, "fanout.sqlite"))
	if err != nil {
		slog.Error("sqlite init failed", "err", err)
		os.Exit(1)
	}
	defer sqlite.Close()

	// Start alert engine
	var alertEngine *alert.Engine
	if cfg.AlertEnabled {
		alertStore := alert.NewStore(sqlite.DB)
		alertEngine = alert.NewEngine(
			alertStore, q, detector,
			time.Duration(cfg.AlertEvalInterval)*time.Second,
			cfg.AlertHistoryDays,
		)
		go alertEngine.Run(ctx)
		slog.Info("alert engine enabled", "interval", cfg.AlertEvalInterval)
	}
```

- [ ] **Step 3: Update MCP Server to accept alert engine**

Modify `Server` struct in `internal/mcp/server.go` to include the alert engine:

```go
type Server struct {
	mcp    *mcp.Server
	svc    *service.Service
	duck   *query.Duck
	cfg    config.Config
	alerts *alert.Engine // may be nil if alerting disabled
}
```

Update `NewServer` signature:

```go
func NewServer(svc *service.Service, duck *query.Duck, cfg config.Config, alerts *alert.Engine) *Server {
```

And the struct initialization:

```go
	s := &Server{
		mcp:    mcpServer,
		svc:    svc,
		duck:   duck,
		cfg:    cfg,
		alerts: alerts,
	}
```

Update the call in `main.go`:

```go
	mcpServer := mcp.NewServer(svc, q, cfg, alertEngine)
```

- [ ] **Step 4: Build and verify**

Run: `go build ./cmd/fanout`
Expected: Success

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go cmd/fanout/main.go internal/mcp/server.go
git commit -m "feat(alert): wire SQLite store and alert engine into main startup"
```

---

### Task 9: MCP Alert Tools

**Files:**
- Create: `internal/mcp/tool_alerts.go`
- Modify: `internal/mcp/server.go` (register tools)

- [ ] **Step 1: Create MCP tool handlers**

```go
// internal/mcp/tool_alerts.go
package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/labstack/fanout/internal/alert"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// AlertRulesIn holds input params for the alert_rules tool.
type AlertRulesIn struct {
	Action          string `json:"action"                    jsonschema:"Action: create|list|get|update|delete|enable|disable|test|test_webhook"`
	RuleID          string `json:"rule_id,omitempty"         jsonschema:"Rule ID (for get/update/delete/enable/disable/test_webhook)"`
	Name            string `json:"name,omitempty"            jsonschema:"Rule name"`
	Description     string `json:"description,omitempty"     jsonschema:"Rule description"`
	Expression      string `json:"expression,omitempty"      jsonschema:"expr-lang expression: error_rate > 0.05 && p95 > 1000"`
	Service         string `json:"service,omitempty"         jsonschema:"Target service, or '*' for all"`
	ForSeconds      *int   `json:"for_seconds,omitempty"     jsonschema:"Seconds condition must hold before firing (default 60)"`
	CooldownS       *int   `json:"cooldown_s,omitempty"      jsonschema:"Seconds before re-alerting after resolve (default 600)"`
	RepeatIntervalS *int   `json:"repeat_interval_s,omitempty" jsonschema:"Seconds between repeat notifications while firing (default 3600)"`
	WebhookURL      string `json:"webhook_url,omitempty"     jsonschema:"Webhook URL for notifications"`
	WebhookHeaders  string `json:"webhook_headers,omitempty" jsonschema:"JSON object of HTTP headers"`
	WebhookTemplate string `json:"webhook_template,omitempty" jsonschema:"Go template for webhook body"`
	NotifyOnResolve *bool  `json:"notify_on_resolve,omitempty" jsonschema:"Send webhook when alert resolves"`
}

type AlertRulesOut struct {
	Rule    *alert.Rule   `json:"rule,omitempty"`
	Rules   []alert.Rule  `json:"rules,omitempty"`
	Test    *TestResult   `json:"test,omitempty"`
	Webhook *WebhookResult `json:"webhook,omitempty"`
	Message string        `json:"message,omitempty"`
}

type TestResult struct {
	Triggered bool          `json:"triggered"`
	Env       alert.AlertEnv `json:"env"`
}

type WebhookResult struct {
	Status string `json:"status"`
}

func (s *Server) alertRules(ctx context.Context, req *mcp.CallToolRequest, in AlertRulesIn) (*mcp.CallToolResult, AlertRulesOut, error) {
	if s.alerts == nil {
		return nil, AlertRulesOut{Message: "alerting is disabled"}, nil
	}
	store := s.alerts.Store()

	switch in.Action {
	case "create":
		if in.Expression == "" {
			return nil, AlertRulesOut{Message: "expression is required"}, nil
		}
		if _, err := alert.CompileExpression(in.Expression); err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("invalid expression: %s", err)}, nil
		}
		rule := alert.Rule{
			Name:            in.Name,
			Description:     in.Description,
			Expression:      in.Expression,
			Service:         in.Service,
			ForSeconds:      intOrDefault(in.ForSeconds, 60),
			CooldownS:       intOrDefault(in.CooldownS, 600),
			RepeatIntervalS: intOrDefault(in.RepeatIntervalS, 3600),
			WebhookURL:      in.WebhookURL,
			WebhookHeaders:  in.WebhookHeaders,
			WebhookTemplate: in.WebhookTemplate,
			NotifyOnResolve: boolOrDefault(in.NotifyOnResolve, false),
		}
		created, err := store.CreateRule(rule)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("create failed: %s", err)}, nil
		}
		s.alerts.RecompileRule(created.ID, created.Expression)
		return nil, AlertRulesOut{Rule: &created}, nil

	case "list":
		rules, err := store.ListRules()
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("list failed: %s", err)}, nil
		}
		return nil, AlertRulesOut{Rules: rules}, nil

	case "get":
		rule, err := store.GetRule(in.RuleID)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("rule not found: %s", err)}, nil
		}
		return nil, AlertRulesOut{Rule: &rule}, nil

	case "update":
		existing, err := store.GetRule(in.RuleID)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("rule not found: %s", err)}, nil
		}
		if in.Name != "" { existing.Name = in.Name }
		if in.Description != "" { existing.Description = in.Description }
		if in.Expression != "" {
			if _, err := alert.CompileExpression(in.Expression); err != nil {
				return nil, AlertRulesOut{Message: fmt.Sprintf("invalid expression: %s", err)}, nil
			}
			existing.Expression = in.Expression
		}
		if in.Service != "" { existing.Service = in.Service }
		if in.ForSeconds != nil { existing.ForSeconds = *in.ForSeconds }
		if in.CooldownS != nil { existing.CooldownS = *in.CooldownS }
		if in.RepeatIntervalS != nil { existing.RepeatIntervalS = *in.RepeatIntervalS }
		if in.WebhookURL != "" { existing.WebhookURL = in.WebhookURL }
		if in.WebhookHeaders != "" { existing.WebhookHeaders = in.WebhookHeaders }
		if in.WebhookTemplate != "" { existing.WebhookTemplate = in.WebhookTemplate }
		if in.NotifyOnResolve != nil { existing.NotifyOnResolve = *in.NotifyOnResolve }
		updated, err := store.UpdateRule(existing)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("update failed: %s", err)}, nil
		}
		s.alerts.RecompileRule(updated.ID, updated.Expression)
		return nil, AlertRulesOut{Rule: &updated}, nil

	case "delete":
		if err := store.DeleteRule(in.RuleID); err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("delete failed: %s", err)}, nil
		}
		s.alerts.RemoveRule(in.RuleID)
		return nil, AlertRulesOut{Message: "deleted"}, nil

	case "enable":
		rule, err := store.GetRule(in.RuleID)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("rule not found: %s", err)}, nil
		}
		rule.Enabled = true
		updated, _ := store.UpdateRule(rule)
		return nil, AlertRulesOut{Rule: &updated, Message: "enabled"}, nil

	case "disable":
		rule, err := store.GetRule(in.RuleID)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("rule not found: %s", err)}, nil
		}
		rule.Enabled = false
		updated, _ := store.UpdateRule(rule)
		return nil, AlertRulesOut{Rule: &updated, Message: "disabled"}, nil

	case "test":
		if in.Expression == "" {
			return nil, AlertRulesOut{Message: "expression is required"}, nil
		}
		prog, err := alert.CompileExpression(in.Expression)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("invalid expression: %s", err)}, nil
		}
		svc := in.Service
		if svc == "" {
			return nil, AlertRulesOut{Message: "service is required for test"}, nil
		}
		env, ok := s.alerts.BuildEnvForService(ctx, svc)
		if !ok {
			return nil, AlertRulesOut{Message: fmt.Sprintf("no data for service %q", svc)}, nil
		}
		triggered, err := alert.EvalExpression(prog, env)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("eval failed: %s", err)}, nil
		}
		return nil, AlertRulesOut{Test: &TestResult{Triggered: triggered, Env: env}}, nil

	case "test_webhook":
		rule, err := store.GetRule(in.RuleID)
		if err != nil {
			return nil, AlertRulesOut{Message: fmt.Sprintf("rule not found: %s", err)}, nil
		}
		actx := alert.ActionContext{
			Rule:  rule,
			Alert: alert.Alert{Service: "test", State: "test"},
			Env:   alert.AlertEnv{},
			Event: "test",
		}
		status, err := alert.FireWebhook(rule, actx)
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		return nil, AlertRulesOut{Webhook: &WebhookResult{Status: status}, Message: msg}, nil

	default:
		return nil, AlertRulesOut{Message: fmt.Sprintf("unknown action %q: use create|list|get|update|delete|enable|disable|test|test_webhook", in.Action)}, nil
	}
}

// AlertsIn holds input params for the alerts tool.
type AlertsIn struct {
	State   string `json:"state,omitempty"   jsonschema:"Filter: firing|pending|resolved|all. Default: all"`
	Service string `json:"service,omitempty" jsonschema:"Filter by service"`
	RuleID  string `json:"rule_id,omitempty" jsonschema:"Filter by rule ID"`
}

type AlertsOut struct {
	Alerts  []alert.Alert       `json:"alerts"`
	Summary alert.AlertSummary  `json:"summary"`
}

func (s *Server) alertsList(ctx context.Context, req *mcp.CallToolRequest, in AlertsIn) (*mcp.CallToolResult, AlertsOut, error) {
	if s.alerts == nil {
		return nil, AlertsOut{Alerts: []alert.Alert{}}, nil
	}
	store := s.alerts.Store()

	state := in.State
	if state == "all" {
		state = ""
	}
	alerts, err := store.ListAlerts(state, in.Service, in.RuleID)
	if err != nil {
		slog.Warn("alerts tool failed", "err", err)
		return nil, AlertsOut{Alerts: []alert.Alert{}}, nil
	}
	summary, _ := store.AlertSummary()
	return nil, AlertsOut{Alerts: alerts, Summary: summary}, nil
}

// AlertEnvIn holds input params for the alert_env tool.
type AlertEnvIn struct {
	Service string `json:"service,omitempty" jsonschema:"Show live values for this service"`
}

type AlertEnvField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

type AlertEnvOut struct {
	Fields   []AlertEnvField    `json:"fields,omitempty"`
	Env      *alert.AlertEnv    `json:"env,omitempty"`
	Examples []string           `json:"examples,omitempty"`
}

func (s *Server) alertEnv(ctx context.Context, req *mcp.CallToolRequest, in AlertEnvIn) (*mcp.CallToolResult, AlertEnvOut, error) {
	fields := []AlertEnvField{
		{Name: "error_rate", Type: "float", Description: "Error rate 0.0-1.0"},
		{Name: "p50", Type: "float", Description: "P50 latency in ms"},
		{Name: "p95", Type: "float", Description: "P95 latency in ms"},
		{Name: "p99", Type: "float", Description: "P99 latency in ms"},
		{Name: "throughput", Type: "float", Description: "Requests per minute"},
		{Name: "log_count", Type: "float", Description: "Log entries in window"},
		{Name: "z_score", Type: "float", Description: "Max anomaly z-score for service"},
		{Name: "health_score", Type: "float", Description: "System health 0-100"},
		{Name: "error_rate_delta", Type: "float", Description: "Error rate % change vs previous window"},
		{Name: "p95_delta", Type: "float", Description: "P95 % change vs previous window"},
		{Name: "throughput_delta", Type: "float", Description: "Throughput % change vs previous window"},
		{Name: "service", Type: "string", Description: "Service name"},
		{Name: "namespace", Type: "string", Description: "Namespace"},
	}
	examples := []string{
		"error_rate > 0.05",
		"p95 > 1000 && throughput > 100",
		"z_score > 3.0",
		"throughput < 10",
		"error_rate_delta > 200",
		"(error_rate > 0.1 || p95 > 2000) && throughput > 100",
	}

	if in.Service == "" || s.alerts == nil {
		return nil, AlertEnvOut{Fields: fields, Examples: examples}, nil
	}

	env, ok := s.alerts.BuildEnvForService(ctx, in.Service)
	if !ok {
		return nil, AlertEnvOut{Fields: fields, Examples: examples}, nil
	}
	return nil, AlertEnvOut{Fields: fields, Env: &env, Examples: examples}, nil
}

func intOrDefault(p *int, def int) int {
	if p != nil {
		return *p
	}
	return def
}

func boolOrDefault(p *bool, def bool) bool {
	if p != nil {
		return *p
	}
	return def
}
```

- [ ] **Step 2: Register tools in server.go**

Add to the `registerTools()` method in `internal/mcp/server.go`, after the existing tool registrations:

```go
	// 11. alert_rules — CRUD + test
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "alert_rules",
		Description: `Manage alert rules. Create, update, delete, enable/disable, test expressions and webhooks.

Actions:
- create: Create a new alert rule with expression and optional webhook
- list: List all rules
- get: Get a single rule by ID
- update: Update rule fields (pass only fields to change)
- delete: Delete a rule
- enable: Enable a disabled rule
- disable: Disable a rule
- test: Dry-run an expression against live data for a service
- test_webhook: Send a test webhook for a rule

Expressions use these fields: error_rate, p50, p95, p99, throughput, log_count, z_score, health_score, error_rate_delta, p95_delta, throughput_delta
Use alert_env tool to see live values and example expressions.`,
	}, wrap("alert_rules", s.alertRules))

	// 12. alerts — view active and recent alerts
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "alerts",
		Description: `View current and recent alerts.

Params: state (firing|pending|resolved|all), service, rule_id
Returns: alert list with delivery status + summary counts (firing/pending/resolved)`,
	}, wrap("alerts", s.alertsList))

	// 13. alert_env — expression reference and live values
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "alert_env",
		Description: `Show available expression fields and live values for a service.

Use this before creating rules to see what data is available and current values.
Params: service (optional — shows live values when provided)
Returns: field definitions, live values, example expressions`,
	}, wrap("alert_env", s.alertEnv))
```

- [ ] **Step 3: Build and verify**

Run: `go build ./cmd/fanout`
Expected: Success

- [ ] **Step 4: Run all tests**

Run: `go test ./...`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tool_alerts.go internal/mcp/server.go
git commit -m "feat(alert): add MCP tools — alert_rules, alerts, alert_env"
```

---

### Task 10: Final Verification

- [ ] **Step 1: Run all tests**

Run: `go test ./... -count=1`
Expected: All PASS

- [ ] **Step 2: Run linter**

Run: `just lint`
Expected: 0 issues

- [ ] **Step 3: Build binary**

Run: `go build ./cmd/fanout`
Expected: Success
