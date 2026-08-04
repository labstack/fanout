package alert

import (
	"context"
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

	env := AlertEnv{Service: "svc-a", ErrorRate: 0.5}
	eng.envOverride = map[string]AlertEnv{"svc-a": env}

	if err := eng.RecompileRule(rule.ID, rule.Expression); err != nil {
		t.Fatalf("RecompileRule: %v", err)
	}

	eng.transition(rule, "svc-a", true, env)

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

	env := AlertEnv{Service: "svc-a", ErrorRate: 0.5}
	eng.envOverride = map[string]AlertEnv{"svc-a": env}

	if err := eng.RecompileRule(rule.ID, rule.Expression); err != nil {
		t.Fatalf("RecompileRule: %v", err)
	}

	eng.transition(rule, "svc-a", true, env)

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

	env := AlertEnv{Service: "svc-a", ErrorRate: 0.5}
	eng.envOverride = map[string]AlertEnv{"svc-a": env}

	if err := eng.RecompileRule(rule.ID, rule.Expression); err != nil {
		t.Fatalf("RecompileRule: %v", err)
	}

	// First transition: condition true → pending.
	eng.transition(rule, "svc-a", true, env)

	a, err := store.GetAlert(rule.ID, "svc-a")
	if err != nil {
		t.Fatalf("GetAlert after pending: %v", err)
	}
	if a.State != "pending" {
		t.Fatalf("expected pending, got %q", a.State)
	}

	// Second transition: condition clears → alert should be deleted.
	eng.transition(rule, "svc-a", false, env)

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

	env := AlertEnv{Service: "svc-a", ErrorRate: 0.5}
	eng.envOverride = map[string]AlertEnv{"svc-a": env}

	if err := eng.RecompileRule(rule.ID, rule.Expression); err != nil {
		t.Fatalf("RecompileRule: %v", err)
	}

	// Fire it.
	eng.transition(rule, "svc-a", true, env)

	a, err := store.GetAlert(rule.ID, "svc-a")
	if err != nil {
		t.Fatalf("GetAlert: %v", err)
	}
	if a.State != "firing" {
		t.Fatalf("expected firing, got %q", a.State)
	}

	// Resolve it.
	envClear := AlertEnv{Service: "svc-a", ErrorRate: 0.01}
	eng.transition(rule, "svc-a", false, envClear)

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
	envs := map[string]AlertEnv{
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
	envs := map[string]AlertEnv{
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

	eng.envOverride = map[string]AlertEnv{
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
