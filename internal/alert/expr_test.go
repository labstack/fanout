package alert

import (
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
