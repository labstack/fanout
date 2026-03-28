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

func makeRule(name string) Rule {
	return Rule{
		Name:            name,
		Description:     "test rule",
		Enabled:         true,
		Service:         "api",
		Namespace:       "prod",
		Expression:      "error_rate > 0.1",
		ForSeconds:      60,
		CooldownS:       600,
		RepeatIntervalS: 3600,
		WebhookURL:      "https://example.com/webhook",
	}
}

func TestStore_CreateAndGetRule(t *testing.T) {
	s := newTestStore(t)

	r, err := s.CreateRule(makeRule("high-error-rate"))
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if r.ID == "" {
		t.Fatal("ID should be set after create")
	}
	if r.Name != "high-error-rate" {
		t.Errorf("Name = %q, want %q", r.Name, "high-error-rate")
	}
	if !r.Enabled {
		t.Error("Enabled should be true")
	}
	if r.CreatedAt == "" {
		t.Error("CreatedAt should be set")
	}

	got, err := s.GetRule(r.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got.ID != r.ID {
		t.Errorf("ID = %q, want %q", got.ID, r.ID)
	}
}

func TestStore_ListRules(t *testing.T) {
	s := newTestStore(t)

	for _, name := range []string{"rule-a", "rule-b", "rule-c"} {
		if _, err := s.CreateRule(makeRule(name)); err != nil {
			t.Fatalf("CreateRule(%q): %v", name, err)
		}
	}

	rules, err := s.ListRules()
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(rules) != 3 {
		t.Errorf("len = %d, want 3", len(rules))
	}
}

func TestStore_UpdateRule(t *testing.T) {
	s := newTestStore(t)

	r, err := s.CreateRule(makeRule("update-me"))
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	r.Name = "updated-name"
	r.Enabled = false

	updated, err := s.UpdateRule(r)
	if err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	if updated.Name != "updated-name" {
		t.Errorf("Name = %q, want %q", updated.Name, "updated-name")
	}
	if updated.Enabled {
		t.Error("Enabled should be false after update")
	}
}

func TestStore_DeleteRule(t *testing.T) {
	s := newTestStore(t)

	r, err := s.CreateRule(makeRule("delete-me"))
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if err := s.DeleteRule(r.ID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}

	_, err = s.GetRule(r.ID)
	if err == nil {
		t.Error("GetRule should return error after delete")
	}
}

func TestStore_ListEnabledRules(t *testing.T) {
	s := newTestStore(t)

	r1, _ := s.CreateRule(makeRule("enabled"))
	r2, _ := s.CreateRule(makeRule("disabled"))

	r2.Enabled = false
	if _, err := s.UpdateRule(r2); err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}

	enabled, err := s.ListEnabledRules()
	if err != nil {
		t.Fatalf("ListEnabledRules: %v", err)
	}
	if len(enabled) != 1 {
		t.Fatalf("len = %d, want 1", len(enabled))
	}
	if enabled[0].ID != r1.ID {
		t.Errorf("ID = %q, want %q", enabled[0].ID, r1.ID)
	}
}

func TestStore_UpsertAndGetAlert(t *testing.T) {
	s := newTestStore(t)

	rule, err := s.CreateRule(makeRule("alert-rule"))
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	a := Alert{
		RuleID:  rule.ID,
		Service: "api",
		State:   "firing",
		Value:   0.25,
		FiredAt: "2026-01-01T00:00:00Z",
	}

	created, err := s.UpsertAlert(a)
	if err != nil {
		t.Fatalf("UpsertAlert: %v", err)
	}
	if created.ID == "" {
		t.Error("ID should be set")
	}
	if created.State != "firing" {
		t.Errorf("State = %q, want %q", created.State, "firing")
	}

	// Upsert again with updated state.
	a.State = "resolved"
	a.ResolvedAt = "2026-01-01T01:00:00Z"
	updated, err := s.UpsertAlert(a)
	if err != nil {
		t.Fatalf("UpsertAlert(update): %v", err)
	}
	if updated.State != "resolved" {
		t.Errorf("State = %q, want %q", updated.State, "resolved")
	}

	got, err := s.GetAlert(rule.ID, "api")
	if err != nil {
		t.Fatalf("GetAlert: %v", err)
	}
	if got.State != "resolved" {
		t.Errorf("State = %q, want %q", got.State, "resolved")
	}
}

func TestStore_ListAlerts(t *testing.T) {
	s := newTestStore(t)

	rule, _ := s.CreateRule(makeRule("filter-rule"))

	states := []string{"firing", "pending", "resolved"}
	for i, state := range states {
		svc := []string{"svc-a", "svc-b", "svc-c"}[i]
		if _, err := s.UpsertAlert(Alert{RuleID: rule.ID, Service: svc, State: state}); err != nil {
			t.Fatalf("UpsertAlert(%q): %v", state, err)
		}
	}

	firing, err := s.ListAlerts("firing", "", "")
	if err != nil {
		t.Fatalf("ListAlerts(firing): %v", err)
	}
	if len(firing) != 1 {
		t.Errorf("len firing = %d, want 1", len(firing))
	}

	all, err := s.ListAlerts("", "", "")
	if err != nil {
		t.Fatalf("ListAlerts(all): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("len all = %d, want 3", len(all))
	}
}

func TestStore_DeleteAlert(t *testing.T) {
	s := newTestStore(t)

	rule, _ := s.CreateRule(makeRule("da-rule"))
	a, err := s.UpsertAlert(Alert{RuleID: rule.ID, Service: "svc", State: "firing"})
	if err != nil {
		t.Fatalf("UpsertAlert: %v", err)
	}

	if err := s.DeleteAlert(a.ID); err != nil {
		t.Fatalf("DeleteAlert: %v", err)
	}

	_, err = s.GetAlert(rule.ID, "svc")
	if err == nil {
		t.Error("GetAlert should return error after delete")
	}
}

func TestStore_DeleteRule_CascadesAlerts(t *testing.T) {
	s := newTestStore(t)

	rule, _ := s.CreateRule(makeRule("cascade-rule"))
	if _, err := s.UpsertAlert(Alert{RuleID: rule.ID, Service: "svc", State: "firing"}); err != nil {
		t.Fatalf("UpsertAlert: %v", err)
	}

	if err := s.DeleteRule(rule.ID); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}

	alerts, err := s.ListAlerts("", "", rule.ID)
	if err != nil {
		t.Fatalf("ListAlerts: %v", err)
	}
	if len(alerts) != 0 {
		t.Errorf("expected 0 alerts after rule cascade delete, got %d", len(alerts))
	}
}

func TestStore_AlertSummary(t *testing.T) {
	s := newTestStore(t)

	rule, _ := s.CreateRule(makeRule("summary-rule"))

	cases := []struct{ svc, state string }{
		{"a", "firing"},
		{"b", "firing"},
		{"c", "pending"},
		{"d", "resolved"},
	}
	for _, c := range cases {
		if _, err := s.UpsertAlert(Alert{RuleID: rule.ID, Service: c.svc, State: c.state}); err != nil {
			t.Fatalf("UpsertAlert(%q, %q): %v", c.svc, c.state, err)
		}
	}

	sum, err := s.AlertSummary()
	if err != nil {
		t.Fatalf("AlertSummary: %v", err)
	}
	if sum.Firing != 2 {
		t.Errorf("Firing = %d, want 2", sum.Firing)
	}
	if sum.Pending != 1 {
		t.Errorf("Pending = %d, want 1", sum.Pending)
	}
	if sum.Resolved != 1 {
		t.Errorf("Resolved = %d, want 1", sum.Resolved)
	}
}
