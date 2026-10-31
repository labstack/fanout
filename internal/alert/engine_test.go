package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appstore "github.com/labstack/fanout/internal/store"
)

// newTestEngine creates an Engine and Store backed by an in-memory SQLite DB.
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
		programs: make(map[string]Program),
		interval: 30 * time.Second,
	}
	return engine, store
}

// makeTestRule creates a rule in the store with the given expression and for-seconds.
func makeTestRule(t *testing.T, s *Store, expr string, forSeconds int) Rule {
	t.Helper()
	r := Rule{
		Name:            "test-rule",
		Enabled:         true,
		Service:         "svc-a",
		Expression:      expr,
		ForSeconds:      forSeconds,
		RepeatIntervalS: 3600,
	}
	created, err := s.CreateRule(r)
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	return created
}

// TestEngine_Transition_NoneToFiring verifies that a rule with for=0 fires immediately.
func TestEngine_Transition_NoneToFiring(t *testing.T) {
	eng, store := newTestEngine(t)

	rule := makeTestRule(t, store, "error_rate > 0.1", 0)

	env := Env{Service: "svc-a", ErrorRate: 0.5}
	eng.envOverride = map[string]Env{"svc-a": env}

	if err := eng.RecompileRule(rule.ID, rule.Expression); err != nil {
		t.Fatalf("RecompileRule: %v", err)
	}

	eng.transition(rule, "svc-a", true, env, time.Now())

	a, err := store.GetAlert(rule.ID, "svc-a")
	if err != nil {
		t.Fatalf("GetAlert: %v", err)
	}
	if a.State != "firing" {
		t.Errorf("State = %q, want %q", a.State, "firing")
	}
	if a.FiredAt == "" {
		t.Error("FiredAt should be set")
	}
}

// TestEngine_Transition_NoneToPending verifies that a rule with for>0 starts as pending.
func TestEngine_Transition_NoneToPending(t *testing.T) {
	eng, store := newTestEngine(t)

	rule := makeTestRule(t, store, "error_rate > 0.1", 60)

	env := Env{Service: "svc-a", ErrorRate: 0.5}
	eng.envOverride = map[string]Env{"svc-a": env}

	if err := eng.RecompileRule(rule.ID, rule.Expression); err != nil {
		t.Fatalf("RecompileRule: %v", err)
	}

	eng.transition(rule, "svc-a", true, env, time.Now())

	a, err := store.GetAlert(rule.ID, "svc-a")
	if err != nil {
		t.Fatalf("GetAlert: %v", err)
	}
	if a.State != "pending" {
		t.Errorf("State = %q, want %q", a.State, "pending")
	}
	if a.FiredAt != "" {
		t.Errorf("FiredAt should be empty in pending state, got %q", a.FiredAt)
	}
}

// TestEngine_Transition_PendingClears verifies that a pending alert is deleted when
// the condition clears before the for-duration elapses.
func TestEngine_Transition_PendingClears(t *testing.T) {
	eng, store := newTestEngine(t)

	rule := makeTestRule(t, store, "error_rate > 0.1", 60)

	env := Env{Service: "svc-a", ErrorRate: 0.5}
	eng.envOverride = map[string]Env{"svc-a": env}

	if err := eng.RecompileRule(rule.ID, rule.Expression); err != nil {
		t.Fatalf("RecompileRule: %v", err)
	}

	// First transition: condition true → pending.
	eng.transition(rule, "svc-a", true, env, time.Now())

	a, err := store.GetAlert(rule.ID, "svc-a")
	if err != nil {
		t.Fatalf("GetAlert after pending: %v", err)
	}
	if a.State != "pending" {
		t.Fatalf("expected pending, got %q", a.State)
	}

	// Second transition: condition clears → alert should be deleted.
	eng.transition(rule, "svc-a", false, env, time.Now())

	_, err = store.GetAlert(rule.ID, "svc-a")
	if err == nil {
		t.Error("GetAlert should return error after pending alert is deleted")
	}
}

// TestEngine_Transition_FiringToResolved verifies that a firing alert transitions to resolved.
func TestEngine_Transition_FiringToResolved(t *testing.T) {
	eng, store := newTestEngine(t)

	rule := makeTestRule(t, store, "error_rate > 0.1", 0)
	rule.NotifyOnResolve = true

	env := Env{Service: "svc-a", ErrorRate: 0.5}
	eng.envOverride = map[string]Env{"svc-a": env}

	if err := eng.RecompileRule(rule.ID, rule.Expression); err != nil {
		t.Fatalf("RecompileRule: %v", err)
	}

	// Fire it.
	eng.transition(rule, "svc-a", true, env, time.Now())

	a, err := store.GetAlert(rule.ID, "svc-a")
	if err != nil {
		t.Fatalf("GetAlert: %v", err)
	}
	if a.State != "firing" {
		t.Fatalf("expected firing, got %q", a.State)
	}

	// Resolve it.
	envClear := Env{Service: "svc-a", ErrorRate: 0.01}
	eng.transition(rule, "svc-a", false, envClear, time.Now())

	a, err = store.GetAlert(rule.ID, "svc-a")
	if err != nil {
		t.Fatalf("GetAlert after resolve: %v", err)
	}
	if a.State != "resolved" {
		t.Errorf("State = %q, want %q", a.State, "resolved")
	}
	if a.ResolvedAt == "" {
		t.Error("ResolvedAt should be set")
	}
}

// TestEngine_CompileRules verifies that compileRules populates the programs map.
func TestEngine_CompileRules(t *testing.T) {
	eng, store := newTestEngine(t)

	rule, err := store.CreateRule(Rule{
		Name:       "compile-test",
		Enabled:    true,
		Expression: "p95 > 500",
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	eng.compileRules([]Rule{rule})

	if _, ok := eng.programs[rule.ID]; !ok {
		t.Errorf("expected programs[%q] to be set after compileRules", rule.ID)
	}
}

// TestEngine_ResolveServices_Wildcard verifies '*' matches all envs.
func TestEngine_ResolveServices_Wildcard(t *testing.T) {
	eng, _ := newTestEngine(t)

	rule := Rule{Service: "*"}
	envs := map[string]Env{
		"svc-a": {},
		"svc-b": {},
		"svc-c": {},
	}

	svcs := eng.resolveServices(rule, envs)
	if len(svcs) != 3 {
		t.Errorf("len = %d, want 3", len(svcs))
	}
	// Should be sorted.
	for i, expected := range []string{"svc-a", "svc-b", "svc-c"} {
		if svcs[i] != expected {
			t.Errorf("svcs[%d] = %q, want %q", i, svcs[i], expected)
		}
	}
}

// TestEngine_ResolveServices_Specific verifies a named service is returned alone.
func TestEngine_ResolveServices_Specific(t *testing.T) {
	eng, _ := newTestEngine(t)

	rule := Rule{Service: "svc-b"}
	envs := map[string]Env{
		"svc-a": {},
		"svc-b": {},
		"svc-c": {},
	}

	svcs := eng.resolveServices(rule, envs)
	if len(svcs) != 1 {
		t.Fatalf("len = %d, want 1", len(svcs))
	}
	if svcs[0] != "svc-b" {
		t.Errorf("svcs[0] = %q, want %q", svcs[0], "svc-b")
	}
}

// TestEngine_RunOnce sets envOverride and calls evaluateOnce to verify an alert is created.
func TestEngine_RunOnce(t *testing.T) {
	eng, store := newTestEngine(t)

	rule, err := store.CreateRule(Rule{
		Name:       "run-once-rule",
		Enabled:    true,
		Service:    "api",
		Expression: "error_rate > 0.05",
		ForSeconds: 0,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	eng.envOverride = map[string]Env{
		"api": {Service: "api", ErrorRate: 0.20},
	}

	eng.evaluateOnce(context.Background())

	a, err := store.GetAlert(rule.ID, "api")
	if err != nil {
		t.Fatalf("GetAlert: %v", err)
	}
	if a.State != "firing" {
		t.Errorf("State = %q, want %q", a.State, "firing")
	}
}

func TestEngineRunReturnsForNonPositiveInterval(t *testing.T) {
	engine := NewEngine(nil, nil, nil, 0, 0)
	engine.Run(context.Background())
}

// TestEngine_Transition_PendingPromotes verifies that a pending alert fires once
// its hold time has passed. SQLite writes created_at as "2006-01-02 15:04:05".
func TestEngine_Transition_PendingPromotes(t *testing.T) {
	sqlite, err := appstore.NewSQLite(":memory:")
	if err != nil {
		t.Fatalf("NewSQLite: %v", err)
	}
	t.Cleanup(func() { sqlite.Close() })
	store := NewStore(sqlite.DB)
	eng := &Engine{store: store, programs: make(map[string]Program), interval: 30 * time.Second}
	rule := makeTestRule(t, store, "error_rate > 0.1", 60)
	env := Env{Service: "svc-a", ErrorRate: 0.5}
	if err := eng.RecompileRule(rule.ID, rule.Expression); err != nil {
		t.Fatalf("RecompileRule: %v", err)
	}
	eng.transition(rule, "svc-a", true, env, time.Now())
	if _, err := sqlite.DB.Exec(`UPDATE alerts SET created_at = datetime('now', '-2 minutes') WHERE rule_id = ?`, rule.ID); err != nil {
		t.Fatal(err)
	}
	eng.transition(rule, "svc-a", true, env, time.Now())
	a, err := store.GetAlert(rule.ID, "svc-a")
	if err != nil {
		t.Fatalf("GetAlert: %v", err)
	}
	if a.State != "firing" || a.FiredAt == "" {
		t.Fatalf("alert = %+v, want firing", a)
	}
}

// The alert value is the first variable the expression reads, ignoring string literals.
func TestPrimaryValue(t *testing.T) {
	env := Env{ErrorRate: 0.2, P95: 300, Throughput: 40, LogCount: 1200, ThroughputDelta: -60, ZScore: 3}
	cases := []struct {
		expr string
		want float64
	}{
		{"log_count > 1000", 1200},
		{"throughput_delta < -50", -60},
		{"p95 > 200 && error_rate > 0.1", 300},
		{"service == \"p95\" && error_rate > 0.1", 0.2},
		{"throughput > 10 && error_rate > 0.1", 40},
		{"z_score > 2", 3},
	}
	for _, tc := range cases {
		if got := primaryValue(Rule{Expression: tc.expr}, env); got != tc.want {
			t.Errorf("primaryValue(%q) = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

// A zero value is a measurement, not a missing one.
func TestAlertJSONKeepsZeroValue(t *testing.T) {
	data, err := json.Marshal(Alert{State: "firing"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"value":0`) {
		t.Errorf("Alert JSON = %s, want value 0", data)
	}
}

// A test reports which services have no recent telemetry, with the measured values of the rest.
func TestEngineTestMarksServicesWithoutData(t *testing.T) {
	eng, _ := newTestEngine(t)
	eng.envOverride = map[string]Env{"checkout": {Service: "checkout", ErrorRate: 0.2}}
	results, err := eng.Test(context.Background(), Rule{Service: "checkuot", Expression: "error_rate > 0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].NoData || results[0].Triggered {
		t.Fatalf("misspelled service = %+v, want one result without data", results)
	}
	results, err = eng.Test(context.Background(), Rule{Expression: "error_rate > 0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].NoData || !results[0].Triggered || results[0].Env.ErrorRate != 0.2 {
		t.Fatalf("all services = %+v, want checkout firing with its values", results)
	}
	if data, _ := json.Marshal(results[0]); !strings.Contains(string(data), `"env":{"error_rate":0.2,`) {
		t.Errorf("result JSON = %s, want env keyed by variable name", data)
	}
}

// A hold is measured between evaluation ticks, not from when SQLite stored
// the pending row: a slow evaluation must not push firing to the next tick.
func TestEngineHoldFiresOnTheTickItEnds(t *testing.T) {
	eng, store := newTestEngine(t)
	rule := makeTestRule(t, store, "error_rate > 0.1", 90)
	env := Env{Service: "svc-a", ErrorRate: 0.5}
	start := time.Now().Add(-time.Hour)
	state := func(at time.Time) string {
		t.Helper()
		eng.transition(rule, "svc-a", true, env, at)
		a, err := store.GetAlert(rule.ID, "svc-a")
		if err != nil {
			t.Fatalf("GetAlert: %v", err)
		}
		return a.State
	}
	for i, want := range []string{"pending", "pending", "pending", "firing"} {
		// The ticker delivers the last tick a little earlier than the first.
		at := start.Add(time.Duration(i) * 30 * time.Second)
		if i == 3 {
			at = at.Add(-time.Millisecond)
		}
		if got := state(at); got != want {
			t.Fatalf("tick %d: state = %q, want %q", i, got, want)
		}
	}
}

// A resolved alert that triggers again holds for the whole hold time anew.
func TestEngineRepeatedPendingHoldsAgain(t *testing.T) {
	eng, store := newTestEngine(t)
	rule := makeTestRule(t, store, "error_rate > 0.1", 60)
	env := Env{Service: "svc-a", ErrorRate: 0.5}
	start := time.Now().Add(-time.Hour)
	at := func(s int) time.Time { return start.Add(time.Duration(s) * time.Second) }
	eng.transition(rule, "svc-a", true, env, at(0))
	eng.transition(rule, "svc-a", true, env, at(60))
	eng.transition(rule, "svc-a", false, env, at(90))
	eng.transition(rule, "svc-a", true, env, at(900))
	eng.transition(rule, "svc-a", true, env, at(930))
	a, err := store.GetAlert(rule.ID, "svc-a")
	if err != nil {
		t.Fatalf("GetAlert: %v", err)
	}
	if a.State != "pending" {
		t.Fatalf("state 30 s into a new hold = %q, want pending", a.State)
	}
	eng.transition(rule, "svc-a", true, env, at(960))
	if a, _ = store.GetAlert(rule.ID, "svc-a"); a.State != "firing" {
		t.Fatalf("state when the new hold ends = %q, want firing", a.State)
	}
}

// Disabling a rule resolves its firing alerts, keeps them in history, sends
// the resolve notification, and drops pending ones.
func TestEngineDisableRuleResolvesFiringAlerts(t *testing.T) {
	events := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Event string `json:"event"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		events <- body.Event
	}))
	defer srv.Close()
	eng, store := newTestEngine(t)
	rule, err := store.CreateRule(Rule{Name: "errors", Enabled: true, Expression: "error_rate > 0.1", WebhookURL: srv.URL, NotifyOnResolve: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.RecompileRule(rule.ID, rule.Expression); err != nil {
		t.Fatal(err)
	}
	env := Env{Service: "svc-a", ErrorRate: 0.5}
	eng.transition(rule, "svc-a", true, env, time.Now())
	if got := <-events; got != "firing" {
		t.Fatalf("first event = %q, want firing", got)
	}
	pending := rule
	pending.ForSeconds = 60
	eng.transition(pending, "svc-b", true, env, time.Now())

	rule.Enabled = false
	eng.DisableRule(rule)

	a, err := store.GetAlert(rule.ID, "svc-a")
	if err != nil {
		t.Fatalf("firing alert was deleted: %v", err)
	}
	if a.State != "resolved" || a.ResolvedAt == "" || a.FiredAt == "" {
		t.Fatalf("alert = %+v, want resolved with fired_at and resolved_at", a)
	}
	select {
	case got := <-events:
		if got != "resolved" {
			t.Fatalf("event = %q, want resolved", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no resolve notification")
	}
	if _, err := store.GetAlert(rule.ID, "svc-b"); err == nil {
		t.Fatal("pending alert of a disabled rule was kept")
	}
	eng.mu.RLock()
	_, compiled := eng.programs[rule.ID]
	eng.mu.RUnlock()
	if compiled {
		t.Fatal("disabled rule is still evaluated")
	}
}
