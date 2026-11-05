package alert

import (
	"strings"
	"testing"
	"time"

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

const episodeStart = "2026-10-31T20:00:00Z"

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

func TestStore_ReplaceRule(t *testing.T) {
	s := newTestStore(t)

	r, err := s.CreateRule(makeRule("update-me"))
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	r.Name = "updated-name"
	r.Enabled = false

	updated, err := s.ReplaceRule(r)
	if err != nil {
		t.Fatalf("ReplaceRule: %v", err)
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
	if _, err := s.ReplaceRule(r2); err != nil {
		t.Fatalf("ReplaceRule: %v", err)
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
		RuleID:    rule.ID,
		Service:   "api",
		State:     "firing",
		Value:     0.25,
		FiredAt:   "2026-01-01T00:00:00Z",
		CreatedAt: "2026-01-01T00:00:00Z",
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

	// Upsert the same episode with updated state.
	a = created
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
		if _, err := s.UpsertAlert(Alert{RuleID: rule.ID, Service: svc, State: state, CreatedAt: episodeStart}); err != nil {
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
	a, err := s.UpsertAlert(Alert{RuleID: rule.ID, Service: "svc", State: "firing", CreatedAt: episodeStart})
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
	if _, err := s.UpsertAlert(Alert{RuleID: rule.ID, Service: "svc", State: "firing", CreatedAt: episodeStart}); err != nil {
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
		if _, err := s.UpsertAlert(Alert{RuleID: rule.ID, Service: c.svc, State: c.state, CreatedAt: episodeStart}); err != nil {
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

// Episode starts are stored as fixed-width RFC 3339 UTC, so listing sorts
// them in time order whatever fraction or zone they were written with.
func TestStoreOrdersAlertsByEpisodeStart(t *testing.T) {
	s := newTestStore(t)
	rule, err := s.CreateRule(makeRule("order-rule"))
	if err != nil {
		t.Fatal(err)
	}
	for svc, created := range map[string]string{
		"first":  "2026-10-31T20:23:37Z",
		"second": "2026-10-31T20:23:37.3Z",
		"third":  "2026-10-31T13:23:37.31-07:00",
	} {
		if _, err := s.UpsertAlert(Alert{RuleID: rule.ID, Service: svc, State: "firing", CreatedAt: created}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.ListAlerts("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, a := range all {
		order = append(order, a.Service)
	}
	if strings.Join(order, ",") != "third,second,first" {
		t.Fatalf("order %v, want newest first", order)
	}
	if all[0].CreatedAt != "2026-10-31T20:23:37.310000000Z" {
		t.Fatalf("stored created_at %q", all[0].CreatedAt)
	}
}

// Only the engine starts an episode: an update without created_at keeps the
// stored start, and a new alert without one is refused.
func TestStoreUpsertKeepsEpisodeStart(t *testing.T) {
	s := newTestStore(t)
	rule, err := s.CreateRule(makeRule("keep-rule"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.UpsertAlert(Alert{RuleID: rule.ID, Service: "api", State: "firing", CreatedAt: "2026-10-31T20:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	a.CreatedAt, a.State = "", "resolved"
	updated, err := s.UpsertAlert(a)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CreatedAt != "2026-10-31T20:00:00.000000000Z" || updated.State != "resolved" {
		t.Fatalf("updated %+v", updated)
	}
	if _, err := s.UpsertAlert(Alert{RuleID: rule.ID, Service: "new", State: "firing"}); err == nil {
		t.Fatal("a new alert without an episode start was stored")
	}
}

// A rule and service may have many resolved episodes but only one open alert.
func TestStoreAllowsOneOpenAlertPerService(t *testing.T) {
	s := newTestStore(t)
	rule, err := s.CreateRule(makeRule("open-rule"))
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"resolved", "resolved", "firing"} {
		if _, err := s.UpsertAlert(Alert{RuleID: rule.ID, Service: "api", State: state, CreatedAt: episodeStart}); err != nil {
			t.Fatalf("store %s episode: %v", state, err)
		}
	}
	if _, err := s.UpsertAlert(Alert{RuleID: rule.ID, Service: "api", State: "pending", CreatedAt: episodeStart}); err == nil {
		t.Fatal("stored a second open alert for one rule and service")
	}
}

// Resolved times carry the engine's local offset; pruning compares instants,
// not text, so an alert resolved 30 hours ago in any zone is removed after one day.
func TestStorePruneResolvedComparesInstants(t *testing.T) {
	s := newTestStore(t)
	r, err := s.CreateRule(makeRule("prune"))
	if err != nil {
		t.Fatal(err)
	}
	// 30 hours ago written at +14:00 reads as a later calendar day than the
	// UTC cutoff, so a text comparison keeps it.
	old := time.Now().Add(-30 * time.Hour).In(time.FixedZone("LINT", 14*3600)).Format(time.RFC3339)
	recent := time.Now().Add(-time.Hour).In(time.FixedZone("IST", 5*3600+1800)).Format(time.RFC3339)
	for svc, resolved := range map[string]string{"old": old, "recent": recent} {
		if _, err := s.UpsertAlert(Alert{RuleID: r.ID, Service: svc, State: "resolved", ResolvedAt: resolved, CreatedAt: episodeStart}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.PruneResolved(1)
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, %v; want 1", n, err)
	}
	if _, err := s.GetAlert(r.ID, "recent"); err != nil {
		t.Fatalf("recent alert pruned: %v", err)
	}
}
