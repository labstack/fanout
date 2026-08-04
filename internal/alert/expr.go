package alert

import (
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
)

// Program is a compiled, ready-to-evaluate alert expression. Aliasing cel.Program
// keeps the expression engine an implementation detail of this file — the rest of
// the package (and callers) refer only to alert.Program.
type Program = cel.Program

// alertCELEnv is the CEL environment shared by every compile. Each AlertEnv field
// is declared as a typed variable so the type checker rejects unknown names and
// non-boolean expressions at compile time. CrossTypeNumericComparisons lets a
// double variable be compared against an integer literal (e.g. `p95 > 500`)
// instead of forcing `p95 > 500.0`.
var alertCELEnv = mustAlertCELEnv()

func mustAlertCELEnv() *cel.Env {
	env, err := cel.NewEnv(
		cel.CrossTypeNumericComparisons(true),
		cel.Variable("error_rate", cel.DoubleType),
		cel.Variable("p50", cel.DoubleType),
		cel.Variable("p95", cel.DoubleType),
		cel.Variable("throughput", cel.DoubleType),
		cel.Variable("log_count", cel.DoubleType),
		cel.Variable("z_score", cel.DoubleType),
		cel.Variable("health_score", cel.DoubleType),
		cel.Variable("error_rate_delta", cel.DoubleType),
		cel.Variable("p95_delta", cel.DoubleType),
		cel.Variable("throughput_delta", cel.DoubleType),
		cel.Variable("service", cel.StringType),
	)
	if err != nil {
		panic(fmt.Sprintf("alert: build CEL env: %v", err))
	}
	return env
}

// CompileExpression compiles a CEL expression against the alert environment and
// validates that it type-checks to a boolean.
func CompileExpression(expression string) (Program, error) {
	if expression == "" {
		return nil, fmt.Errorf("expression must not be empty")
	}
	ast, iss := alertCELEnv.Compile(expression)
	if iss != nil && iss.Err() != nil {
		return nil, fmt.Errorf("compile expression: %w", iss.Err())
	}
	if ast.OutputType().Kind() != types.BoolKind {
		return nil, fmt.Errorf("expression must evaluate to bool, got %s", ast.OutputType())
	}
	prog, err := alertCELEnv.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("build program: %w", err)
	}
	return prog, nil
}

// celActivation binds the CEL variables to an AlertEnv's values. The keys must
// stay in sync with the cel.Variable declarations in mustAlertCELEnv above;
// TestAlertEnvVariablesDeclaredAndBound enforces that against the AlertEnv `expr`
// field tags, so a drift fails the build instead of silently breaking a rule at
// eval time.
func celActivation(env AlertEnv) map[string]any {
	return map[string]any{
		"error_rate":       env.ErrorRate,
		"p50":              env.P50,
		"p95":              env.P95,
		"throughput":       env.Throughput,
		"log_count":        env.LogCount,
		"z_score":          env.ZScore,
		"health_score":     env.HealthScore,
		"error_rate_delta": env.ErrorRateDelta,
		"p95_delta":        env.P95Delta,
		"throughput_delta": env.ThroughputDelta,
		"service":          env.Service,
	}
}

// EvalExpression runs a compiled program with the given environment.
func EvalExpression(prog Program, env AlertEnv) (bool, error) {
	out, _, err := prog.Eval(celActivation(env))
	if err != nil {
		return false, fmt.Errorf("eval expression: %w", err)
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("expression result is not bool: %T", out.Value())
	}
	return b, nil
}

// SafeEval wraps EvalExpression with panic recovery, logging unexpected panics
// via slog. Returns false on panic.
func SafeEval(prog Program, env AlertEnv) (result bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("alert: expression eval panic", "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("expression eval panicked: %v", r)
		}
	}()
	return EvalExpression(prog, env)
}
