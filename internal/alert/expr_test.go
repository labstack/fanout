package alert

import (
	"reflect"
	"testing"
)

func TestCompileExpression_Valid(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{"threshold", "error_rate > 0.05"},
		{"compound", "error_rate > 0.05 && p95 > 500"},
		{"rate of change", "p95_delta > 100"},
		{"anomaly", "z_score > 3.0"},
		{"absence", "throughput < 1"},
		{"complex", "error_rate > 0.1 || (p95 > 1000 && health_score < 50)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := CompileExpression(tc.expr)
			if err != nil {
				t.Errorf("CompileExpression(%q): unexpected error: %v", tc.expr, err)
			}
			if prog == nil {
				t.Errorf("CompileExpression(%q): prog is nil", tc.expr)
			}
		})
	}
}

func TestCompileExpression_Invalid(t *testing.T) {
	cases := []struct {
		name string
		expr string
	}{
		{"unknown field", "unknown_field > 10"},
		{"syntax error", "error_rate >>"},
		{"empty", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := CompileExpression(tc.expr)
			if err == nil {
				t.Errorf("CompileExpression(%q): expected error, got nil", tc.expr)
			}
			if prog != nil {
				t.Errorf("CompileExpression(%q): expected nil prog on error", tc.expr)
			}
		})
	}
}

func TestCompileExpression_Invalid_NonBoolean(t *testing.T) {
	// error_rate + p95 is a double, not a bool — CompileExpression rejects any
	// expression whose CEL output type isn't bool.
	_, err := CompileExpression("error_rate + p95")
	if err == nil {
		t.Error("CompileExpression(non-bool): expected error, got nil")
	}
}

func TestEvalExpression(t *testing.T) {
	prog, err := CompileExpression("error_rate > 0.05 && p95 > 500")
	if err != nil {
		t.Fatalf("CompileExpression: %v", err)
	}

	cases := []struct {
		name     string
		env      AlertEnv
		expected bool
	}{
		{"both above threshold", AlertEnv{ErrorRate: 0.1, P95: 600}, true},
		{"error_rate below", AlertEnv{ErrorRate: 0.01, P95: 600}, false},
		{"p95 below", AlertEnv{ErrorRate: 0.1, P95: 100}, false},
		{"both below", AlertEnv{ErrorRate: 0.01, P95: 100}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := EvalExpression(prog, tc.env)
			if err != nil {
				t.Fatalf("EvalExpression: %v", err)
			}
			if result != tc.expected {
				t.Errorf("result = %v, want %v", result, tc.expected)
			}
		})
	}
}

func TestSafeEval_NoPanic(t *testing.T) {
	prog, err := CompileExpression("error_rate > 0.1")
	if err != nil {
		t.Fatalf("CompileExpression: %v", err)
	}

	env := AlertEnv{ErrorRate: 0.5}
	result, err := SafeEval(prog, env)
	if err != nil {
		t.Fatalf("SafeEval: unexpected error: %v", err)
	}
	if !result {
		t.Error("SafeEval: expected true, got false")
	}
}

// TestAlertEnvVariablesDeclaredAndBound makes the AlertEnv struct the single
// source of truth for alert variables: every field's `expr` tag must be both a
// declared CEL variable (so a rule can reference it) and a key in celActivation
// (so it's bound at eval). Without this, adding a field to the struct + the
// cel.Variable list but forgetting celActivation would compile fine yet fail
// every 30s eval cycle with "no such attribute" — a silent, partial failure.
func TestAlertEnvVariablesDeclaredAndBound(t *testing.T) {
	act := celActivation(AlertEnv{})
	tp := reflect.TypeOf(AlertEnv{})

	tags := map[string]bool{}
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		name := f.Tag.Get("expr")
		if name == "" {
			t.Errorf("AlertEnv.%s has no `expr` tag", f.Name)
			continue
		}
		tags[name] = true

		// (1) bound at eval time.
		if _, ok := act[name]; !ok {
			t.Errorf("variable %q (AlertEnv.%s) is not bound in celActivation", name, f.Name)
		}

		// (2) declared in the CEL env — a type-appropriate boolean expression
		// referencing it must compile.
		var expr string
		switch f.Type.Kind() {
		case reflect.Float64:
			expr = name + " >= 0"
		case reflect.String:
			expr = name + ` == ""`
		default:
			t.Errorf("AlertEnv.%s has unsupported kind %s for a CEL variable", f.Name, f.Type.Kind())
			continue
		}
		if _, err := CompileExpression(expr); err != nil {
			t.Errorf("variable %q (AlertEnv.%s) not declared/usable in the CEL env: %v", name, f.Name, err)
		}
	}

	// Reverse direction: no activation key without a backing struct field.
	for k := range act {
		if !tags[k] {
			t.Errorf("celActivation binds %q with no matching AlertEnv `expr` tag", k)
		}
	}
}

// TestEvalExpression_StringVar exercises the `service` variable — the only
// non-double field. A wrong type on its cel.Variable declaration (e.g. Double
// instead of String) would pass every other test but break real rules like this.
func TestEvalExpression_StringVar(t *testing.T) {
	prog, err := CompileExpression(`service == "checkout" && error_rate > 0.05`)
	if err != nil {
		t.Fatalf("CompileExpression: %v", err)
	}
	cases := []struct {
		name string
		env  AlertEnv
		want bool
	}{
		{"match + over threshold", AlertEnv{Service: "checkout", ErrorRate: 0.1}, true},
		{"service mismatch", AlertEnv{Service: "cart", ErrorRate: 0.1}, false},
		{"match but under threshold", AlertEnv{Service: "checkout", ErrorRate: 0.01}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvalExpression(prog, tc.env)
			if err != nil {
				t.Fatalf("EvalExpression: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
